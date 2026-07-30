package breadthdepth

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
)

// SelectHandoff performs a deterministic lexicographic selection. Goal
// progress always dominates diversity. Diversity only breaks ties at the same
// completed-waypoint and staged-distance point.
func SelectHandoff(goalID string, candidates []HandoffSeed, topK int) (HandoffSet, error) {
	if goalID == "" {
		return HandoffSet{}, fmt.Errorf("handoff goal ID is required")
	}
	if topK <= 0 {
		return HandoffSet{}, fmt.Errorf("handoff top-K must be positive")
	}
	eligible := make([]HandoffSeed, 0, len(candidates))
	for index := range candidates {
		candidate := copySeed(candidates[index])
		candidate.SchemaVersion = SchemaVersion
		candidate.Selected = false
		candidate.SelectionRank = 0
		if candidate.StableKey == "" {
			candidate.StableKey = stableKey(seedKeyInput(candidate))
		}
		if candidate.Progress.EntryCondition && candidate.Replayable {
			eligible = append(eligible, candidate)
		}
	}
	sort.SliceStable(eligible, func(i, j int) bool {
		return progressLess(eligible[i], eligible[j])
	})

	selected := make([]HandoffSeed, 0, min(topK, len(eligible)))
	remaining := eligible
	for len(selected) < topK && len(remaining) > 0 {
		best := 0
		for index := 1; index < len(remaining); index++ {
			if selectionLess(remaining[index], remaining[best], selected) {
				best = index
			}
		}
		chosen := remaining[best]
		chosen.Selected = true
		chosen.SelectionRank = len(selected) + 1
		selected = append(selected, chosen)
		remaining = append(remaining[:best], remaining[best+1:]...)
	}
	result := HandoffSet{
		SchemaVersion: SchemaVersion, GoalID: goalID, TopK: topK,
		Candidates: len(candidates), Eligible: len(eligible), Selected: selected,
	}
	result.StableKey = stableKey(struct {
		Schema   string
		Goal     string
		TopK     int
		Selected []string
	}{SchemaVersion, goalID, topK, selectedKeys(selected)})
	return result, nil
}

func progressLess(left, right HandoffSeed) bool {
	if left.Progress.Completed != right.Progress.Completed {
		return left.Progress.Completed > right.Progress.Completed
	}
	if left.Progress.Distance != right.Progress.Distance {
		return left.Progress.Distance < right.Progress.Distance
	}
	if left.Progress.TargetReached != right.Progress.TargetReached {
		return left.Progress.TargetReached
	}
	if left.PlanPrefixLength != right.PlanPrefixLength {
		return left.PlanPrefixLength < right.PlanPrefixLength
	}
	return left.StableKey < right.StableKey
}

func selectionLess(left, right HandoffSeed, selected []HandoffSeed) bool {
	// Do not let diversity cross a Goal-progress boundary.
	if left.Progress.Completed != right.Progress.Completed ||
		left.Progress.Distance != right.Progress.Distance ||
		left.Progress.TargetReached != right.Progress.TargetReached {
		return progressLess(left, right)
	}
	leftDiversity := diversityGain(left, selected)
	rightDiversity := diversityGain(right, selected)
	for index := range leftDiversity {
		if leftDiversity[index] != rightDiversity[index] {
			return leftDiversity[index] > rightDiversity[index]
		}
	}
	if left.PlanPrefixLength != right.PlanPrefixLength {
		return left.PlanPrefixLength < right.PlanPrefixLength
	}
	return left.StableKey < right.StableKey
}

// The ordered components match the documented policy: relative semantic
// trace, Facet combination, queue shape, and semantic binding roles.
func diversityGain(candidate HandoffSeed, selected []HandoffSeed) [4]int {
	if len(selected) == 0 {
		return [4]int{1, 1, 1, 1}
	}
	result := [4]int{1, 1, 1, 1}
	binding := stableKey(candidate.Progress.BindingRoles)
	for _, existing := range selected {
		if candidate.SemanticTraceDigest == existing.SemanticTraceDigest {
			result[0] = 0
		}
		if candidate.FacetCombinationKey == existing.FacetCombinationKey {
			result[1] = 0
		}
		if candidate.QueueShapeKey == existing.QueueShapeKey {
			result[2] = 0
		}
		if binding == stableKey(existing.Progress.BindingRoles) {
			result[3] = 0
		}
	}
	return result
}

func seedKeyInput(seed HandoffSeed) any {
	return struct {
		Schema       string
		Corpus       string
		Admission    int
		Progress     GoalProgress
		PlanLength   int
		Semantic     string
		Facet        string
		Queue        string
		Replayable   bool
		ReplayStatus string
	}{
		SchemaVersion, seed.GlobalCorpusID, seed.GlobalAdmissionRank, seed.Progress,
		seed.PlanPrefixLength, seed.SemanticTraceDigest, seed.FacetCombinationKey,
		seed.QueueShapeKey, seed.Replayable, seed.ReplayStatus,
	}
}

func selectedKeys(values []HandoffSeed) []string {
	result := make([]string, len(values))
	for index := range values {
		result[index] = values[index].StableKey
	}
	return result
}

func copySeed(seed HandoffSeed) HandoffSeed {
	result := seed
	result.Plan = seed.Plan.Copy()
	result.Trace = seed.Trace.Copy()
	result.Observation = seed.Observation.Copy()
	if seed.Progress.BindingRoles != nil {
		result.Progress.BindingRoles = make(map[string]string, len(seed.Progress.BindingRoles))
		for key, value := range seed.Progress.BindingRoles {
			result.Progress.BindingRoles[key] = value
		}
	}
	return result
}

func stableKey(value any) string {
	encoded, _ := json.Marshal(value)
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}
