// Frozen experimental surface: EvidenceFrontier is retained for accepted
// experiments and is never selected by the default Waypoint Frontier mode.
package goalsearch

import (
	"fmt"
	"sort"
	"strconv"
)

type EvidenceFrontierStats struct {
	Considered             int            `json:"considered"`
	Inserted               int            `json:"inserted"`
	Deduplicated           int            `json:"deduplicated"`
	Replaced               int            `json:"replaced"`
	Evicted                int            `json:"evicted"`
	RejectedByProgress     int            `json:"rejected_by_progress_guard"`
	RejectedSupportedLimit int            `json:"rejected_by_supported_slot_limit"`
	RetainedByLevel        map[string]int `json:"retained_by_evidence_level"`
	RetainedByCommitment   map[string]int `json:"retained_by_commitment"`
}

type EvidenceFrontierSnapshot struct {
	SchemaVersion      string                `json:"schema_version"`
	TotalCapacity      int                   `json:"total_capacity"`
	SupportedSlotLimit int                   `json:"supported_slot_limit"`
	Seeds              []FrontierSeed        `json:"seeds"`
	Stats              EvidenceFrontierStats `json:"stats"`
}

type EvidenceFrontier struct {
	capacity       int
	supportedLimit int
	seeds          []FrontierSeed
	stats          EvidenceFrontierStats
}

func NewEvidenceFrontier(totalCapacity, supportedSlotLimit int) (*EvidenceFrontier, error) {
	if totalCapacity <= 0 {
		return nil, fmt.Errorf("evidence frontier total capacity must be positive")
	}
	if supportedSlotLimit <= 0 || supportedSlotLimit >= totalCapacity && totalCapacity > 1 {
		return nil, fmt.Errorf("supported-only slot limit must be positive and smaller than capacity")
	}
	if totalCapacity == 1 {
		supportedSlotLimit = 1
	}
	return &EvidenceFrontier{
		capacity:       totalCapacity,
		supportedLimit: supportedSlotLimit,
		stats: EvidenceFrontierStats{
			RetainedByLevel:      make(map[string]int),
			RetainedByCommitment: make(map[string]int),
		},
	}, nil
}

func (f *EvidenceFrontier) Consider(seed FrontierSeed) (bool, error) {
	if f == nil {
		return false, fmt.Errorf("evidence frontier is nil")
	}
	if seed.EvidenceKey == "" {
		return false, fmt.Errorf("evidence frontier seed has no evidence key")
	}
	f.stats.Considered++
	before := append([]FrontierSeed(nil), f.seeds...)
	candidates := append([]FrontierSeed(nil), f.seeds...)
	replaced := false
	for index, existing := range candidates {
		if existing.EvidenceKey != seed.EvidenceKey {
			continue
		}
		if evidenceBetterSeed(seed, existing) {
			candidates[index] = copyFrontierSeed(seed)
			f.stats.Replaced++
			replaced = true
		} else {
			f.stats.Deduplicated++
			return false, nil
		}
		break
	}
	if !replaced {
		candidates = append(candidates, copyFrontierSeed(seed))
		f.stats.Inserted++
	}
	retained, rejectedProgress, rejectedSupported := f.retain(candidates)
	f.stats.RejectedByProgress += rejectedProgress
	f.stats.RejectedSupportedLimit += rejectedSupported
	f.seeds = retained
	retainedCandidate := false
	for _, current := range f.seeds {
		if current.ID == seed.ID {
			retainedCandidate = true
			break
		}
	}
	afterIDs := make(map[string]struct{}, len(f.seeds))
	for _, current := range f.seeds {
		afterIDs[current.ID] = struct{}{}
	}
	for _, previous := range before {
		if _, ok := afterIDs[previous.ID]; !ok {
			f.stats.Evicted++
		}
	}
	if !replaced && !retainedCandidate {
		f.stats.Evicted++
	}
	f.recount()
	return retainedCandidate, nil
}

func (f *EvidenceFrontier) retain(candidates []FrontierSeed) ([]FrontierSeed, int, int) {
	if len(candidates) == 0 {
		return nil, 0, 0
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		return evidenceBetterSeed(candidates[i], candidates[j])
	})
	bestCompleted := candidates[0].Progress.CompletedWaypointCount
	eligible := make([]FrontierSeed, 0, len(candidates))
	rejectedProgress := 0
	for _, seed := range candidates {
		if seed.Progress.CompletedWaypointCount+1 < bestCompleted {
			rejectedProgress++
			continue
		}
		eligible = append(eligible, seed)
	}
	type progressGroup struct {
		completed int
		distance  int
		seeds     []FrontierSeed
	}
	var groups []progressGroup
	for _, seed := range eligible {
		if len(groups) == 0 ||
			groups[len(groups)-1].completed != seed.Progress.CompletedWaypointCount ||
			groups[len(groups)-1].distance != seed.Progress.DistanceToCurrent {
			groups = append(groups, progressGroup{
				completed: seed.Progress.CompletedWaypointCount,
				distance:  seed.Progress.DistanceToCurrent,
			})
		}
		groups[len(groups)-1].seeds = append(groups[len(groups)-1].seeds, seed)
	}
	retained := make([]FrontierSeed, 0, f.capacity)
	supportedUsed, rejectedSupported := 0, 0
	for _, group := range groups {
		if len(retained) >= f.capacity {
			break
		}
		seenCommitment := make(map[string]struct{})
		// At identical Goal progress, first preserve different committed
		// causal preparations. The preceding group ordering keeps Goal
		// progress primary.
		for _, seed := range group.seeds {
			if len(retained) >= f.capacity || EvidenceLevelRank(seed.EvidenceLevel) < 2 ||
				seed.EvidenceContradicted {
				continue
			}
			key := seed.CommitmentKey
			if key == "" {
				key = string(seed.CommittedBranchID)
			}
			if _, duplicate := seenCommitment[key]; duplicate {
				continue
			}
			seenCommitment[key] = struct{}{}
			retained = append(retained, seed)
		}
		for _, seed := range group.seeds {
			if len(retained) >= f.capacity {
				break
			}
			if containsSeedID(retained, seed.ID) || seed.EvidenceContradicted {
				continue
			}
			// Only partial-evidence seeds consume the supported-only
			// allowance. Planned seeds remain eligible as an exploration
			// fallback until a committed seed exists.
			if seed.EvidenceLevel == EvidenceLevelSupported {
				if supportedUsed >= f.supportedLimit {
					rejectedSupported++
					continue
				}
				supportedUsed++
			}
			retained = append(retained, seed)
		}
	}
	sort.SliceStable(retained, func(i, j int) bool {
		return evidenceBetterSeed(retained[i], retained[j])
	})
	return retained, rejectedProgress, rejectedSupported
}

func (f *EvidenceFrontier) Select(offset int) (FrontierSeed, bool) {
	if f == nil || len(f.seeds) == 0 {
		return FrontierSeed{}, false
	}
	seeds := append([]FrontierSeed(nil), f.seeds...)
	sort.SliceStable(seeds, func(i, j int) bool {
		return evidenceBetterSeed(seeds[i], seeds[j])
	})
	if offset < 0 {
		offset = -offset
	}
	return copyFrontierSeed(seeds[offset%len(seeds)]), true
}

func (f *EvidenceFrontier) Snapshot() EvidenceFrontierSnapshot {
	if f == nil {
		return EvidenceFrontierSnapshot{SchemaVersion: BranchEvidenceSchemaVersion}
	}
	seeds := append([]FrontierSeed(nil), f.seeds...)
	sort.SliceStable(seeds, func(i, j int) bool {
		return evidenceBetterSeed(seeds[i], seeds[j])
	})
	return EvidenceFrontierSnapshot{
		SchemaVersion:      BranchEvidenceSchemaVersion,
		TotalCapacity:      f.capacity,
		SupportedSlotLimit: f.supportedLimit,
		Seeds:              seeds,
		Stats:              f.stats,
	}
}

func (f *EvidenceFrontier) recount() {
	f.stats.RetainedByLevel = make(map[string]int)
	f.stats.RetainedByCommitment = make(map[string]int)
	for _, seed := range f.seeds {
		f.stats.RetainedByLevel[string(seed.EvidenceLevel)]++
		if seed.CommitmentKey != "" {
			f.stats.RetainedByCommitment[seed.CommitmentKey]++
		}
	}
}

func evidenceBetterSeed(left, right FrontierSeed) bool {
	if left.Progress.CompletedWaypointCount != right.Progress.CompletedWaypointCount {
		return left.Progress.CompletedWaypointCount > right.Progress.CompletedWaypointCount
	}
	if left.Progress.DistanceToCurrent != right.Progress.DistanceToCurrent {
		return left.Progress.DistanceToCurrent < right.Progress.DistanceToCurrent
	}
	if EvidenceLevelRank(left.EvidenceLevel) != EvidenceLevelRank(right.EvidenceLevel) {
		return EvidenceLevelRank(left.EvidenceLevel) > EvidenceLevelRank(right.EvidenceLevel)
	}
	if left.NecessaryEvidenceCount != right.NecessaryEvidenceCount {
		return left.NecessaryEvidenceCount > right.NecessaryEvidenceCount
	}
	if left.NextEventGeneratable != right.NextEventGeneratable {
		return left.NextEventGeneratable
	}
	if left.Progress.PrefixLength != right.Progress.PrefixLength {
		return left.Progress.PrefixLength < right.Progress.PrefixLength
	}
	if left.EvidenceKey != right.EvidenceKey {
		return left.EvidenceKey < right.EvidenceKey
	}
	return left.ID < right.ID
}

func EvidenceSeedKey(seed FrontierSeed) string {
	return stableHash(struct {
		Goal       GoalID
		Completed  int
		Distance   int
		Level      EvidenceLevel
		Branch     BranchTemplateID
		Commitment string
		Necessary  int
		Next       bool
		Shape      string
	}{
		Goal:       seed.GoalID,
		Completed:  seed.Progress.CompletedWaypointCount,
		Distance:   seed.Progress.DistanceToCurrent,
		Level:      seed.EvidenceLevel,
		Branch:     seed.CommittedBranchID,
		Commitment: seed.CommitmentKey,
		Necessary:  seed.NecessaryEvidenceCount,
		Next:       seed.NextEventGeneratable,
		Shape:      seed.BranchSemanticKey + ":" + strconv.Itoa(seed.WaypointIndex),
	})
}

func containsSeedID(seeds []FrontierSeed, id string) bool {
	for _, seed := range seeds {
		if seed.ID == id {
			return true
		}
	}
	return false
}
