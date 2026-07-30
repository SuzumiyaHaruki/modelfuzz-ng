package goalsearch

import (
	"fmt"
	"sort"
	"strconv"

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/core"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/engine"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/plan"
)

// FrontierSeed is a replayable prefix retained because it is one of the best
// known ways to reach a waypoint boundary. It deliberately stores both the
// high-level Plan prefix and exact concrete Trace prefix: mutation works on the
// former, while replay verification uses the latter.
type FrontierSeed struct {
	ID                      string             `json:"id"`
	ParentID                string             `json:"parent_id,omitempty"`
	RunIndex                int                `json:"run_index"`
	RuntimeSeed             int64              `json:"runtime_seed"`
	ExecutionID             core.ExecutionID   `json:"execution_id"`
	GoalID                  GoalID             `json:"goal_id"`
	WaypointIndex           int                `json:"waypoint_index"`
	WaypointID              string             `json:"waypoint_id,omitempty"`
	Progress                GoalProgress       `json:"progress"`
	Instance                GoalInstance       `json:"goal_instance"`
	Bindings                map[Symbol]Binding `json:"bindings"`
	PrefixPlan              plan.PlanSequence  `json:"prefix_plan"`
	PrefixTrace             core.Trace         `json:"prefix_trace"`
	PrefixObservation       core.Observation   `json:"prefix_observation"`
	PrefixPlanEnd           int                `json:"prefix_plan_end"`
	PrefixTraceEnd          int                `json:"prefix_trace_end"`
	SemanticKey             string             `json:"semantic_key"`
	PlannedBranchID         BranchTemplateID   `json:"planned_branch_template_id,omitempty"`
	PlannedBranchKey        string             `json:"planned_branch_key,omitempty"`
	RealizedBranchKey       string             `json:"realized_branch_key,omitempty"`
	RealizedBranchID        BranchTemplateID   `json:"realized_branch_template_id,omitempty"`
	RealizedBranchDecidable bool               `json:"realized_branch_decidable"`
	BranchSemanticKey       string             `json:"branch_semantic_key,omitempty"`
	EvidenceLevel           EvidenceLevel      `json:"branch_evidence_level,omitempty"`
	EvidenceKey             string             `json:"branch_evidence_key,omitempty"`
	CommittedBranchID       BranchTemplateID   `json:"committed_branch_template_id,omitempty"`
	CommitmentKey           string             `json:"branch_commitment_key,omitempty"`
	NecessaryEvidenceCount  int                `json:"necessary_evidence_count,omitempty"`
	NextEventGeneratable    bool               `json:"next_key_event_generatable,omitempty"`
	EvidenceContradicted    bool               `json:"branch_evidence_contradicted,omitempty"`
	SourceResultKey         string             `json:"source_result_key"`
	ReplayVerified          bool               `json:"replay_verified"`
	ReplayStatus            string             `json:"replay_status"`
	ReplayMatchedSteps      uint64             `json:"replay_matched_steps"`
}

type FrontierStats struct {
	Considered       int `json:"considered"`
	Inserted         int `json:"inserted"`
	Deduplicated     int `json:"deduplicated"`
	Replaced         int `json:"replaced"`
	Evicted          int `json:"evicted"`
	RejectedNoPrefix int `json:"rejected_no_prefix"`
}

type FrontierSnapshot struct {
	SchemaVersion string         `json:"schema_version"`
	TopK          int            `json:"top_k"`
	Seeds         []FrontierSeed `json:"seeds"`
	Sizes         map[string]int `json:"sizes_by_waypoint"`
	Diversity     map[string]int `json:"semantic_shapes_by_waypoint"`
	Stats         FrontierStats  `json:"stats"`
}

type Frontier struct {
	topK    int
	buckets map[int][]FrontierSeed
	stats   FrontierStats
}

// CapacityFrontier is the fixed-total-capacity control used for a fair
// comparison with DiversityFrontier. Frontier above intentionally retains its
// historical per-waypoint top-K behavior.
type CapacityFrontier struct {
	capacity int
	seeds    []FrontierSeed
	stats    FrontierStats
}

func NewCapacityFrontier(capacity int) (*CapacityFrontier, error) {
	if capacity <= 0 {
		return nil, fmt.Errorf("frontier total capacity must be positive")
	}
	return &CapacityFrontier{capacity: capacity}, nil
}

func (f *CapacityFrontier) Consider(seed FrontierSeed) (bool, error) {
	if f == nil {
		return false, fmt.Errorf("capacity frontier is nil")
	}
	f.stats.Considered++
	for index, existing := range f.seeds {
		if existing.SemanticKey != seed.SemanticKey {
			continue
		}
		if betterSeed(seed, existing) {
			f.seeds[index] = copyFrontierSeed(seed)
			f.stats.Replaced++
			sortSeeds(f.seeds)
			return true, nil
		}
		f.stats.Deduplicated++
		return false, nil
	}
	f.seeds = append(f.seeds, copyFrontierSeed(seed))
	f.stats.Inserted++
	sortSeeds(f.seeds)
	if len(f.seeds) > f.capacity {
		evicted := f.seeds[len(f.seeds)-1].ID
		f.seeds = f.seeds[:f.capacity]
		f.stats.Evicted++
		if evicted == seed.ID {
			return false, nil
		}
	}
	return true, nil
}

func (f *CapacityFrontier) Select(offset int) (FrontierSeed, bool) {
	if f == nil || len(f.seeds) == 0 {
		return FrontierSeed{}, false
	}
	seeds := append([]FrontierSeed(nil), f.seeds...)
	sortSeeds(seeds)
	if offset < 0 {
		offset = -offset
	}
	return copyFrontierSeed(seeds[offset%len(seeds)]), true
}

func (f *CapacityFrontier) Snapshot() FrontierSnapshot {
	if f == nil {
		return FrontierSnapshot{SchemaVersion: SchemaVersion}
	}
	seeds := append([]FrontierSeed(nil), f.seeds...)
	sortSeeds(seeds)
	return FrontierSnapshot{
		SchemaVersion: SchemaVersion, TopK: f.capacity, Seeds: seeds,
		Sizes:     map[string]int{"global": len(seeds)},
		Diversity: map[string]int{"global": len(seeds)}, Stats: f.stats,
	}
}

func NewFrontier(topK int) (*Frontier, error) {
	if topK <= 0 {
		return nil, fmt.Errorf("frontier top-K must be positive")
	}
	return &Frontier{topK: topK, buckets: make(map[int][]FrontierSeed)}, nil
}

// SeedFromResult cuts a plan and a concrete trace at the last real progress
// boundary. A result with progress only in the initial observation has no
// replayable prefix and is intentionally rejected.
func SeedFromResult(
	id, parentID string, runIndex int, runtimeSeed int64, executionID core.ExecutionID,
	sequence plan.PlanSequence, run engine.Result, evaluation EvaluationResult,
) (FrontierSeed, error) {
	if id == "" || !executionID.Valid() {
		return FrontierSeed{}, fmt.Errorf("frontier seed identity is incomplete")
	}
	planEnd, traceEnd := evaluation.PrefixEndActionIndex, evaluation.PrefixEndTraceStep
	if planEnd < 0 || traceEnd < 0 {
		return FrontierSeed{}, fmt.Errorf("evaluation has no replayable progress prefix")
	}
	if planEnd >= len(sequence.Actions) || traceEnd >= len(run.Trace.Steps) {
		return FrontierSeed{}, fmt.Errorf(
			"progress prefix is outside artifacts: plan=%d/%d trace=%d/%d",
			planEnd, len(sequence.Actions), traceEnd, len(run.Trace.Steps))
	}
	prefixPlan := plan.PlanSequence{
		Actions:  make([]plan.PlanAction, planEnd+1),
		Metadata: cloneStringMap(sequence.Metadata),
	}
	for index := range prefixPlan.Actions {
		prefixPlan.Actions[index] = sequence.Actions[index].Copy()
	}
	remainingConcrete := traceEnd + 1
	for planIndex := 0; planIndex <= planEnd; planIndex++ {
		if planIndex >= len(run.Resolutions) {
			return FrontierSeed{}, fmt.Errorf("missing resolution for prefix PlanAction %d", planIndex)
		}
		resolved := len(run.Resolutions[planIndex].Actions)
		included := min(resolved, remainingConcrete)
		remainingConcrete -= included
		if planIndex == planEnd && included < resolved {
			action := &prefixPlan.Actions[planIndex]
			if action.Messages == nil || included <= 0 {
				return FrontierSeed{}, fmt.Errorf(
					"progress splits non-message PlanAction %d resolution", planIndex)
			}
			action.Messages.Count = included
		}
	}
	if remainingConcrete != 0 {
		return FrontierSeed{}, fmt.Errorf(
			"trace prefix has %d concrete actions not accounted for by resolutions",
			remainingConcrete)
	}
	if err := prefixPlan.Validate(); err != nil {
		return FrontierSeed{}, fmt.Errorf("trimmed frontier Plan prefix: %w", err)
	}
	prefixTrace := run.Trace.Copy()
	prefixTrace.Steps = prefixTrace.Steps[:traceEnd+1]
	progress := evaluation.Instance.Progress
	waypointID := progress.CurrentWaypointID
	seed := FrontierSeed{
		ID: id, ParentID: parentID, RunIndex: runIndex, RuntimeSeed: runtimeSeed,
		ExecutionID: executionID, GoalID: evaluation.Instance.GoalID,
		WaypointIndex: progress.CurrentWaypointIndex, WaypointID: waypointID,
		Progress: progress, Instance: copyInstance(evaluation.Instance),
		Bindings:   cloneBindings(evaluation.Instance.Bindings),
		PrefixPlan: prefixPlan, PrefixTrace: prefixTrace,
		PrefixObservation: evaluation.PrefixObservation.Copy(),
		PrefixPlanEnd:     planEnd, PrefixTraceEnd: traceEnd,
		SourceResultKey: evaluation.StableKey,
	}
	seed.SemanticKey = frontierSemanticKey(seed)
	return seed, nil
}

// RefreshFrontierSeed recomputes the semantic deduplication key after an
// externally verified prefix replaces the seed's Goal instance.
func RefreshFrontierSeed(seed *FrontierSeed) error {
	if seed == nil {
		return fmt.Errorf("frontier seed is nil")
	}
	if seed.ID == "" || seed.GoalID == "" {
		return fmt.Errorf("frontier seed identity is incomplete")
	}
	seed.SemanticKey = frontierSemanticKey(*seed)
	return nil
}

func (f *Frontier) Consider(seed FrontierSeed) (bool, error) {
	if f == nil {
		return false, fmt.Errorf("frontier is nil")
	}
	f.stats.Considered++
	if seed.PrefixPlanEnd < 0 || seed.PrefixTraceEnd < 0 {
		f.stats.RejectedNoPrefix++
		return false, nil
	}
	if seed.SemanticKey == "" {
		seed.SemanticKey = frontierSemanticKey(seed)
	}
	bucket := append([]FrontierSeed(nil), f.buckets[seed.WaypointIndex]...)
	for index, existing := range bucket {
		if existing.SemanticKey != seed.SemanticKey {
			continue
		}
		if betterSeed(seed, existing) {
			bucket[index] = copyFrontierSeed(seed)
			f.stats.Replaced++
			sortSeeds(bucket)
			f.buckets[seed.WaypointIndex] = bucket
			return true, nil
		}
		f.stats.Deduplicated++
		return false, nil
	}
	bucket = append(bucket, copyFrontierSeed(seed))
	f.stats.Inserted++
	sortSeeds(bucket)
	if len(bucket) > f.topK {
		bucket = bucket[:f.topK]
		f.stats.Evicted++
	}
	f.buckets[seed.WaypointIndex] = bucket
	for _, retained := range bucket {
		if retained.ID == seed.ID {
			return true, nil
		}
	}
	return false, nil
}

func (f *Frontier) Best() (FrontierSeed, bool) {
	return f.Select(0)
}

// Select rotates deterministically among the top-K seeds at the most advanced
// waypoint. This preserves the progress ordering while preventing the shortest
// equal-progress prefix from starving semantically different causal prefixes.
func (f *Frontier) Select(offset int) (FrontierSeed, bool) {
	if f == nil {
		return FrontierSeed{}, false
	}
	highest := -1
	for waypoint, bucket := range f.buckets {
		if len(bucket) > 0 && waypoint > highest {
			highest = waypoint
		}
	}
	if highest < 0 {
		return FrontierSeed{}, false
	}
	seeds := append([]FrontierSeed(nil), f.buckets[highest]...)
	sortSeeds(seeds)
	if offset < 0 {
		offset = -offset
	}
	return copyFrontierSeed(seeds[offset%len(seeds)]), true
}

func (f *Frontier) Snapshot() FrontierSnapshot {
	if f == nil {
		return FrontierSnapshot{SchemaVersion: SchemaVersion}
	}
	sizes := make(map[string]int, len(f.buckets))
	diversity := make(map[string]int, len(f.buckets))
	for waypoint, seeds := range f.buckets {
		key := strconv.Itoa(waypoint)
		if len(seeds) > 0 && seeds[0].WaypointID != "" {
			key = seeds[0].WaypointID
		}
		sizes[key] = len(seeds)
		shapes := make(map[string]struct{}, len(seeds))
		for _, seed := range seeds {
			shapes[seed.SemanticKey] = struct{}{}
		}
		diversity[key] = len(shapes)
	}
	return FrontierSnapshot{
		SchemaVersion: SchemaVersion, TopK: f.topK,
		Seeds: f.allSeeds(), Sizes: sizes, Diversity: diversity, Stats: f.stats,
	}
}

func (f *Frontier) allSeeds() []FrontierSeed {
	indices := make([]int, 0, len(f.buckets))
	for index := range f.buckets {
		indices = append(indices, index)
	}
	sort.Sort(sort.Reverse(sort.IntSlice(indices)))
	var seeds []FrontierSeed
	for _, index := range indices {
		for _, seed := range f.buckets[index] {
			seeds = append(seeds, copyFrontierSeed(seed))
		}
	}
	return seeds
}

func sortSeeds(seeds []FrontierSeed) {
	sort.SliceStable(seeds, func(i, j int) bool {
		if BetterProgress(seeds[i].Progress, seeds[j].Progress) {
			return true
		}
		if BetterProgress(seeds[j].Progress, seeds[i].Progress) {
			return false
		}
		return seeds[i].ID < seeds[j].ID
	})
}

func betterSeed(left, right FrontierSeed) bool {
	if BetterProgress(left.Progress, right.Progress) {
		return true
	}
	if BetterProgress(right.Progress, left.Progress) {
		return false
	}
	if left.ReplayVerified != right.ReplayVerified {
		return left.ReplayVerified
	}
	return left.ID < right.ID
}

func frontierSemanticKey(seed FrontierSeed) string {
	type nodeSummary struct {
		ID          core.NodeID `json:"id"`
		Status      string      `json:"status"`
		Role        string      `json:"role"`
		Term        string      `json:"term"`
		Commit      string      `json:"commit"`
		Applied     string      `json:"applied"`
		Last        string      `json:"last"`
		First       string      `json:"first"`
		Snapshot    string      `json:"snapshot"`
		BoundSymbol Symbol      `json:"bound_symbol,omitempty"`
	}
	type messageSummary struct {
		Link    string `json:"link"`
		Type    string `json:"type"`
		Blocked bool   `json:"blocked"`
		Count   int    `json:"count"`
	}
	bound := make(map[core.NodeID]Symbol, len(seed.Bindings))
	for symbol, binding := range seed.Bindings {
		bound[binding.Node] = symbol
	}
	nodes := make([]nodeSummary, 0, len(seed.PrefixObservation.Nodes))
	for _, node := range seed.PrefixObservation.Nodes {
		nodes = append(nodes, nodeSummary{
			ID: node.ID, Status: string(node.Status), Role: semanticString(node.Semantic["role"]),
			Term: semanticBucket(node.Semantic["term"]), Commit: semanticBucket(node.Semantic["commit"]),
			Applied: semanticBucket(node.Semantic["applied"]), Last: semanticBucket(node.Semantic["last_index"]),
			First:    semanticBucket(node.Semantic["first_index"]),
			Snapshot: semanticBucket(node.Semantic["snapshot_index"]), BoundSymbol: bound[node.ID],
		})
	}
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].ID < nodes[j].ID })
	counts := make(map[string]int)
	for _, message := range seed.PrefixObservation.Messages {
		key := message.From.String() + "→" + message.To.String() + "|" +
			message.TypeHint + "|" + strconv.FormatBool(message.Blocked)
		counts[key]++
	}
	messageKeys := make([]string, 0, len(counts))
	for key := range counts {
		messageKeys = append(messageKeys, key)
	}
	sort.Strings(messageKeys)
	messages := make([]messageSummary, 0, len(messageKeys))
	for _, key := range messageKeys {
		messages = append(messages, messageSummary{
			Link: key, Count: counts[key],
		})
	}
	partition := ""
	if seed.PrefixObservation.NetworkPartition != nil {
		partition = stableHash(seed.PrefixObservation.NetworkPartition.Normalized())
	}
	return stableHash(struct {
		Goal      GoalID
		Waypoint  int
		Completed int
		Distance  int
		Nodes     []nodeSummary
		Messages  []messageSummary
		Partition string
	}{
		seed.GoalID, seed.WaypointIndex, seed.Progress.CompletedWaypointCount,
		seed.Progress.DistanceToCurrent, nodes, messages, partition,
	})
}

func semanticString(value any) string {
	if text, ok := value.(string); ok {
		return text
	}
	return ""
}

func semanticBucket(value any) string {
	number, ok := semanticUint(value)
	if !ok {
		return "unknown"
	}
	switch {
	case number == 0:
		return "0"
	case number == 1:
		return "1"
	case number <= 3:
		return "2-3"
	case number <= 7:
		return "4-7"
	default:
		return "8+"
	}
}

func copyFrontierSeed(seed FrontierSeed) FrontierSeed {
	copy := seed
	copy.Progress = seed.Progress
	copy.Instance = copyInstance(seed.Instance)
	copy.Bindings = cloneBindings(seed.Bindings)
	copy.PrefixPlan = seed.PrefixPlan.Copy()
	copy.PrefixTrace = seed.PrefixTrace.Copy()
	copy.PrefixObservation = seed.PrefixObservation.Copy()
	return copy
}

func cloneBindings(input map[Symbol]Binding) map[Symbol]Binding {
	if input == nil {
		return nil
	}
	output := make(map[Symbol]Binding, len(input))
	for key, value := range input {
		output[key] = value
	}
	return output
}
