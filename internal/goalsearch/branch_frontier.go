// Frozen experimental surface: DiversityFrontier is separate from the
// mainline Standard Frontier in frontier.go and is enabled only explicitly.
package goalsearch

import (
	"fmt"
	"sort"
	"strconv"

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/core"
)

type BranchFrontierStats struct {
	Considered      int            `json:"considered"`
	Inserted        int            `json:"inserted"`
	Deduplicated    int            `json:"deduplicated"`
	Replaced        int            `json:"replaced"`
	Evicted         int            `json:"evicted"`
	Retained        map[string]int `json:"retained_by_branch"`
	EvictedByBranch map[string]int `json:"evicted_by_branch"`
}

type BranchFrontierSnapshot struct {
	SchemaVersion    string              `json:"schema_version"`
	TotalCapacity    int                 `json:"total_capacity"`
	MinimumPerBranch int                 `json:"minimum_per_branch"`
	Awareness        BranchAwareness     `json:"awareness"`
	Seeds            []FrontierSeed      `json:"seeds"`
	SizesByBranch    map[string]int      `json:"sizes_by_branch"`
	Stats            BranchFrontierStats `json:"stats"`
}

type DiversityFrontier struct {
	capacity  int
	minimum   int
	awareness BranchAwareness
	seeds     []FrontierSeed
	stats     BranchFrontierStats
}

func NewDiversityFrontier(
	totalCapacity, minimumPerBranch int, awareness BranchAwareness,
) (*DiversityFrontier, error) {
	if totalCapacity <= 0 {
		return nil, fmt.Errorf("diversity frontier total capacity must be positive")
	}
	if minimumPerBranch <= 0 || minimumPerBranch > totalCapacity {
		return nil, fmt.Errorf("per-branch minimum must be in [1,total capacity]")
	}
	if err := awareness.Validate(); err != nil {
		return nil, err
	}
	return &DiversityFrontier{
		capacity: totalCapacity, minimum: minimumPerBranch, awareness: awareness,
		stats: BranchFrontierStats{
			Retained: make(map[string]int), EvictedByBranch: make(map[string]int),
		},
	}, nil
}

func (f *DiversityFrontier) branchKey(seed FrontierSeed) string {
	if f.awareness == BranchPlannedOnly {
		return seed.PlannedBranchKey
	}
	if seed.RealizedBranchDecidable && seed.RealizedBranchKey != "" {
		return seed.RealizedBranchKey
	}
	return "undecided:" + seed.PlannedBranchKey
}

func (f *DiversityFrontier) Consider(seed FrontierSeed) (bool, error) {
	if f == nil {
		return false, fmt.Errorf("diversity frontier is nil")
	}
	if seed.PlannedBranchKey == "" {
		return false, fmt.Errorf("diversity frontier seed has no planned branch")
	}
	if seed.BranchSemanticKey == "" {
		seed.BranchSemanticKey = BranchPrefixSemanticKey(seed)
	}
	f.stats.Considered++
	before := append([]FrontierSeed(nil), f.seeds...)
	candidates := append([]FrontierSeed(nil), f.seeds...)
	replaced := false
	for index, existing := range candidates {
		if f.branchKey(existing) != f.branchKey(seed) ||
			existing.BranchSemanticKey != seed.BranchSemanticKey {
			continue
		}
		if betterSeed(seed, existing) {
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
	f.seeds = f.retain(candidates)
	retained := false
	for _, value := range f.seeds {
		if value.ID == seed.ID {
			retained = true
			break
		}
	}
	previous := make(map[string]FrontierSeed, len(before))
	for _, value := range before {
		previous[value.ID] = value
	}
	current := make(map[string]struct{}, len(f.seeds))
	for _, value := range f.seeds {
		current[value.ID] = struct{}{}
	}
	for id, value := range previous {
		if _, ok := current[id]; !ok {
			f.stats.Evicted++
			f.stats.EvictedByBranch[f.branchKey(value)]++
		}
	}
	if !replaced && !retained {
		// The newly inserted candidate itself lost the fixed-capacity
		// competition. Count that as an eviction as the standard fixed-total
		// Frontier does, instead of reporting a misleading all-zero total.
		f.stats.Evicted++
		f.stats.EvictedByBranch[f.branchKey(seed)]++
	}
	f.recountRetained()
	return retained, nil
}

func (f *DiversityFrontier) retain(candidates []FrontierSeed) []FrontierSeed {
	if len(candidates) <= f.capacity {
		sortSeeds(candidates)
		return candidates
	}
	byBranch := make(map[string][]FrontierSeed)
	for _, seed := range candidates {
		key := f.branchKey(seed)
		byBranch[key] = append(byBranch[key], seed)
	}
	keys := make([]string, 0, len(byBranch))
	for key := range byBranch {
		keys = append(keys, key)
		sortSeeds(byBranch[key])
	}
	sort.Slice(keys, func(i, j int) bool {
		left, right := byBranch[keys[i]][0], byBranch[keys[j]][0]
		if betterSeed(left, right) {
			return true
		}
		if betterSeed(right, left) {
			return false
		}
		return keys[i] < keys[j]
	})
	retained := make([]FrontierSeed, 0, f.capacity)
	selected := make(map[string]int, len(keys))
	// Diversity first: one best seed for each branch. A second pass honors a
	// larger declared minimum without multiplying total capacity.
	for round := 0; round < f.minimum && len(retained) < f.capacity; round++ {
		for _, key := range keys {
			if len(retained) >= f.capacity || selected[key] >= len(byBranch[key]) {
				continue
			}
			retained = append(retained, byBranch[key][selected[key]])
			selected[key]++
		}
	}
	var remainder []FrontierSeed
	for _, key := range keys {
		remainder = append(remainder, byBranch[key][selected[key]:]...)
	}
	sortSeeds(remainder)
	for _, seed := range remainder {
		if len(retained) >= f.capacity {
			break
		}
		retained = append(retained, seed)
	}
	sortSeeds(retained)
	return retained
}

func (f *DiversityFrontier) Select(offset int) (FrontierSeed, bool) {
	if f == nil || len(f.seeds) == 0 {
		return FrontierSeed{}, false
	}
	// Keep only seeds no more than one completed waypoint behind the best;
	// this is the explicit progress guard against retaining a diverse but
	// clearly obsolete prefix forever.
	bestCompleted := f.seeds[0].Progress.CompletedWaypointCount
	eligible := make([]FrontierSeed, 0, len(f.seeds))
	for _, seed := range f.seeds {
		if seed.Progress.CompletedWaypointCount+1 >= bestCompleted {
			eligible = append(eligible, seed)
		}
	}
	sort.SliceStable(eligible, func(i, j int) bool {
		left, right := f.branchKey(eligible[i]), f.branchKey(eligible[j])
		if left != right {
			return left < right
		}
		return betterSeed(eligible[i], eligible[j])
	})
	if offset < 0 {
		offset = -offset
	}
	return copyFrontierSeed(eligible[offset%len(eligible)]), true
}

func (f *DiversityFrontier) Snapshot() BranchFrontierSnapshot {
	if f == nil {
		return BranchFrontierSnapshot{SchemaVersion: BranchSchemaVersion}
	}
	sizes := make(map[string]int)
	seeds := make([]FrontierSeed, len(f.seeds))
	for index, seed := range f.seeds {
		seeds[index] = copyFrontierSeed(seed)
		sizes[f.branchKey(seed)]++
	}
	sortSeeds(seeds)
	return BranchFrontierSnapshot{
		SchemaVersion: BranchSchemaVersion, TotalCapacity: f.capacity,
		MinimumPerBranch: f.minimum, Awareness: f.awareness,
		Seeds: seeds, SizesByBranch: sizes, Stats: f.stats,
	}
}

func (f *DiversityFrontier) recountRetained() {
	f.stats.Retained = make(map[string]int)
	for _, seed := range f.seeds {
		f.stats.Retained[f.branchKey(seed)]++
	}
}

// BranchPrefixSemanticKey removes concrete node and message identities while
// retaining binding roles, relative Raft shape, queue categories and progress.
func BranchPrefixSemanticKey(seed FrontierSeed) string {
	type nodeShape struct {
		Binding  string `json:"binding"`
		Status   string `json:"status"`
		Role     string `json:"role"`
		Term     string `json:"term"`
		Log      string `json:"log"`
		Commit   string `json:"commit"`
		Snapshot bool   `json:"snapshot"`
	}
	type queueShape struct {
		From    string `json:"from_role"`
		To      string `json:"to_role"`
		Type    string `json:"type"`
		Blocked bool   `json:"blocked"`
		Count   string `json:"count_bucket"`
	}
	role := func(id core.NodeID) string {
		for symbol, binding := range seed.Bindings {
			if binding.Node == id {
				return string(symbol)
			}
		}
		return "other"
	}
	var maxTerm, maxLast, maxCommit uint64
	for _, node := range seed.PrefixObservation.Nodes {
		term, _ := semanticUint(node.Semantic["term"])
		last, _ := semanticUint(node.Semantic["last_index"])
		commit, _ := semanticUint(node.Semantic["commit"])
		maxTerm, maxLast, maxCommit = max(maxTerm, term), max(maxLast, last), max(maxCommit, commit)
	}
	nodes := make([]nodeShape, 0, len(seed.PrefixObservation.Nodes))
	for _, node := range seed.PrefixObservation.Nodes {
		term, _ := semanticUint(node.Semantic["term"])
		last, _ := semanticUint(node.Semantic["last_index"])
		commit, _ := semanticUint(node.Semantic["commit"])
		snapshot, _ := semanticUint(node.Semantic["snapshot_index"])
		nodes = append(nodes, nodeShape{
			Binding: role(node.ID), Status: string(node.Status),
			Role: semanticString(node.Semantic["role"]),
			Term: relativeBucket(maxTerm, term), Log: relativeBucket(maxLast, last),
			Commit: relativeBucket(maxCommit, commit), Snapshot: snapshot > 0,
		})
	}
	sort.Slice(nodes, func(i, j int) bool {
		if nodes[i].Binding != nodes[j].Binding {
			return nodes[i].Binding < nodes[j].Binding
		}
		if nodes[i].Role != nodes[j].Role {
			return nodes[i].Role < nodes[j].Role
		}
		return nodes[i].Status < nodes[j].Status
	})
	counts := make(map[string]int)
	for _, message := range seed.PrefixObservation.Messages {
		key := role(message.From) + "|" + role(message.To) + "|" +
			message.TypeHint + "|" + strconv.FormatBool(message.Blocked)
		counts[key]++
	}
	keys := make([]string, 0, len(counts))
	for key := range counts {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	queues := make([]queueShape, 0, len(keys))
	for _, key := range keys {
		parts := splitBranchQueueKey(key)
		queues = append(queues, queueShape{
			From: parts[0], To: parts[1], Type: parts[2],
			Blocked: parts[3] == "true", Count: countBucket(counts[key]),
		})
	}
	return stableHash(struct {
		Progress GoalProgress `json:"progress"`
		Branch   string       `json:"branch"`
		Nodes    []nodeShape  `json:"nodes"`
		Queues   []queueShape `json:"queues"`
	}{
		Progress: GoalProgress{
			CompletedWaypointCount: seed.Progress.CompletedWaypointCount,
			CurrentWaypointIndex:   seed.Progress.CurrentWaypointIndex,
			DistanceToCurrent:      seed.Progress.DistanceToCurrent,
			EvidenceStrength:       seed.Progress.EvidenceStrength,
		},
		Branch: seed.RealizedBranchKey, Nodes: nodes, Queues: queues,
	})
}

func splitBranchQueueKey(value string) [4]string {
	var result [4]string
	index := 0
	start := 0
	for position := 0; position <= len(value) && index < 4; position++ {
		if position == len(value) || value[position] == '|' {
			result[index] = value[start:position]
			index++
			start = position + 1
		}
	}
	return result
}

func countBucket(count int) string {
	switch {
	case count <= 0:
		return "0"
	case count == 1:
		return "1"
	case count <= 3:
		return "2-3"
	default:
		return "4+"
	}
}
