package raft_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/assurance/facet"
	facetraft "github.com/SuzumiyaHaruki/modelfuzz-ng/internal/assurance/facet/raft"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/assurance/facetbreadth"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/core"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/corpus"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/engine"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/experiment"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/model"
	modelraft "github.com/SuzumiyaHaruki/modelfuzz-ng/internal/model/raft"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/mutation"
)

type activeExecutorFactory func() model.Executor

type activeCandidateFact struct {
	Lineage             string        `json:"lineage"`
	ParentLineage       string        `json:"parent_lineage,omitempty"`
	Depth               int           `json:"depth"`
	ExecutionSeed       int64         `json:"execution_seed"`
	InputPlanDigest     string        `json:"input_plan_digest"`
	PlanDigest          string        `json:"plan_digest"`
	TraceDigest         string        `json:"trace_digest"`
	ModelPathDigest     string        `json:"model_state_path_digest"`
	EngineStatus        engine.Status `json:"engine_status"`
	ExperimentStatus    engine.Status `json:"experiment_status"`
	OracleCodes         []string      `json:"oracle_codes"`
	EvaluationIdentity  string        `json:"evaluation_identity"`
	SemanticStateKeys   []int64       `json:"semantic_state_keys"`
	SemanticTransitions []int64       `json:"semantic_transition_keys"`
	BaselineRetained    bool          `json:"baseline_retained"`
	BaselineReason      string        `json:"baseline_reason"`
	FacetAdmitted       bool          `json:"facet_admitted"`
	FacetReason         string        `json:"facet_reason"`
	ActiveAdmitted      bool          `json:"active_admitted"`
	Children            []string      `json:"children"`
}

type activeRepresentativeMetrics struct {
	FirstCount             int     `json:"first_count"`
	ShortestCount          int     `json:"shortest_count"`
	AverageFirstActions    float64 `json:"average_first_plan_actions"`
	MedianFirstActions     float64 `json:"median_first_plan_actions"`
	AverageShortestActions float64 `json:"average_shortest_plan_actions"`
	MedianShortestActions  float64 `json:"median_shortest_plan_actions"`
}

type activeCampaignMetrics struct {
	ExecutedCandidates      int                         `json:"executed_candidates"`
	InitialCandidates       int                         `json:"initial_candidates"`
	MutationCandidates      int                         `json:"mutation_candidates"`
	QueueExhausted          bool                        `json:"queue_exhausted"`
	TotalPlanActions        int                         `json:"total_plan_actions"`
	TotalConcreteActions    int                         `json:"total_concrete_actions"`
	TotalTraceSteps         int                         `json:"total_trace_steps"`
	TotalModelEvents        int                         `json:"total_model_events"`
	TotalModelStates        int                         `json:"total_model_states"`
	StatusCounts            map[string]int              `json:"status_counts"`
	TerminationCounts       map[string]int              `json:"termination_counts"`
	OracleFindingCodes      map[string]int              `json:"oracle_finding_codes"`
	UniquePlanDigests       int                         `json:"unique_plan_digests"`
	UniqueTraceDigests      int                         `json:"unique_trace_digests"`
	UniqueModelPathDigests  int                         `json:"unique_model_state_path_digests"`
	DuplicatePlanRatio      float64                     `json:"duplicate_plan_ratio"`
	DuplicateTraceRatio     float64                     `json:"duplicate_trace_ratio"`
	DuplicateModelPathRatio float64                     `json:"duplicate_model_state_path_ratio"`
	RawModelStates          int                         `json:"raw_model_states"`
	SemanticStates          int                         `json:"semantic_states"`
	SemanticTransitions     int                         `json:"semantic_transitions"`
	BaselineRetained        int                         `json:"baseline_retained_candidates"`
	BaselineReasons         map[string]int              `json:"baseline_admission_reasons"`
	FacetKeys               int                         `json:"facet_keys"`
	FacetKeysByFacet        map[string]int              `json:"facet_keys_by_facet"`
	FacetReasons            map[string]int              `json:"facet_decision_reasons"`
	FacetAdmitted           int                         `json:"facet_admitted_candidates"`
	FacetClasses            map[string][]string         `json:"facet_classes"`
	FacetFirstDiscovery     map[string]uint64           `json:"facet_first_discovery_ordinal"`
	Representatives         activeRepresentativeMetrics `json:"representatives"`
	InvalidEvidence         int                         `json:"invalid_evidence"`
	InsufficientEvidence    int                         `json:"insufficient_evidence"`
	AdmittedParents         int                         `json:"admitted_parents"`
	GeneratedChildren       int                         `json:"generated_children"`
	ExecutedChildren        int                         `json:"executed_children"`
	MaximumDepth            int                         `json:"maximum_generation_depth"`
	FinalQueueSize          int                         `json:"final_queue_size"`
}

type activeCampaignResult struct {
	Seed              int64                 `json:"seed"`
	Mode              activeMode            `json:"mode"`
	CandidateBudget   int                   `json:"candidate_budget"`
	LineageSequence   []string              `json:"lineage_sequence"`
	InitialPlanDigest []string              `json:"initial_plan_digests"`
	Facts             []activeCandidateFact `json:"candidate_facts"`
	Metrics           activeCampaignMetrics `json:"metrics"`
	FacetStateDigest  string                `json:"facet_state_digest"`
	CorpusDigest      string                `json:"corpus_digest"`
	FinalQueue        []string              `json:"final_queue"`
}

type activeAccumulator struct {
	metrics      activeCampaignMetrics
	plans        map[string]struct{}
	traces       map[string]struct{}
	modelPaths   map[string]struct{}
	facetClasses map[string]map[string]struct{}
}

func newActiveAccumulator() *activeAccumulator {
	return &activeAccumulator{
		metrics: activeCampaignMetrics{
			StatusCounts: make(map[string]int), TerminationCounts: make(map[string]int),
			OracleFindingCodes: make(map[string]int), BaselineReasons: make(map[string]int),
			FacetKeysByFacet: make(map[string]int), FacetReasons: make(map[string]int),
			FacetClasses:        make(map[string][]string),
			FacetFirstDiscovery: make(map[string]uint64),
		},
		plans: make(map[string]struct{}), traces: make(map[string]struct{}),
		modelPaths: make(map[string]struct{}), facetClasses: make(map[string]map[string]struct{}),
	}
}

func runActiveCampaign(
	t *testing.T,
	mode activeMode,
	campaignSeed int64,
	initial []activeCandidate,
	budget int,
	executorFactory activeExecutorFactory,
) activeCampaignResult {
	t.Helper()
	if mode != activeCurrentBaseline && mode != activeFacetOnly {
		t.Fatalf("unknown active mode %q", mode)
	}
	if len(initial) != activeInitialCount || budget < len(initial) {
		t.Fatalf("invalid campaign shape initial=%d budget=%d", len(initial), budget)
	}
	catalog, err := facetbreadth.BuildCatalogIdentityV1(facetraftCatalog())
	if err != nil {
		t.Fatal(err)
	}
	breadth, err := facetbreadth.NewCoverageStateV1(catalog)
	if err != nil {
		t.Fatal(err)
	}
	collection := corpus.NewWithConfig(corpus.Config{
		MinNewModelStates: 1, RequireSemanticNovelty: true,
	})
	mutator := newActiveMutator(t)
	queue := copyActiveCandidates(initial)
	accumulator := newActiveAccumulator()
	result := activeCampaignResult{
		Seed: campaignSeed, Mode: mode, CandidateBudget: budget,
		LineageSequence:   make([]string, 0, budget),
		InitialPlanDigest: make([]string, len(initial)),
		Facts:             make([]activeCandidateFact, 0, budget),
	}
	for index, candidate := range initial {
		result.InitialPlanDigest[index] = activePlanDigest(candidate.Plan)
	}

	for len(result.Facts) < budget && len(queue) > 0 {
		candidate := queue[0]
		queue = queue[1:]
		execution := runActiveCandidate(t, campaignSeed, candidate, executorFactory())
		projection, projectionErr := activeProjection(execution.Completion)
		if projectionErr != nil {
			t.Fatalf("%s semantic projection: %v", candidate.Lineage, projectionErr)
		}
		baselineEntry, baselineRetained, baselineReason := activeBaselineConsider(
			t, collection, len(result.Facts), execution, projection,
		)
		_ = baselineEntry
		summary, err := facetbreadth.BuildCandidateSummaryV1(execution.Record, execution.Evaluations)
		if err != nil {
			t.Fatalf("%s BuildCandidateSummaryV1: %v", candidate.Lineage, err)
		}
		decision, err := breadth.Apply(uint64(len(result.Facts)), summary)
		if err != nil {
			t.Fatalf("%s facet Apply: %v", candidate.Lineage, err)
		}
		activeAdmitted := baselineRetained
		if mode == activeFacetOnly {
			activeAdmitted = decision.Admitted
		}
		fact := activeFact(
			execution, projection, baselineRetained, baselineReason, decision, activeAdmitted,
		)
		if activeAdmitted {
			children := mutateActiveChildren(t, mutator, campaignSeed, execution, candidate)
			for _, child := range children {
				queue = append(queue, child)
				fact.Children = append(fact.Children, child.Lineage)
			}
		}
		result.LineageSequence = append(result.LineageSequence, candidate.Lineage)
		result.Facts = append(result.Facts, fact)
		accumulator.observe(execution, projection, baselineRetained, baselineReason, decision, activeAdmitted)
	}
	accumulator.metrics.QueueExhausted = len(queue) == 0 && len(result.Facts) < budget
	accumulator.metrics.FinalQueueSize = len(queue)
	accumulator.finish(collection.Snapshot(), breadth.Snapshot(), len(result.Facts))
	result.Metrics = accumulator.metrics
	result.FinalQueue = activeQueueLineages(queue)
	result.FacetStateDigest, err = breadth.Digest()
	if err != nil {
		t.Fatal(err)
	}
	result.CorpusDigest = activeDigest(collection.Snapshot())
	return result
}

func facetraftCatalog() []facet.Evaluator {
	return facetraft.CatalogV1()
}

func newActiveMutator(t *testing.T) *mutation.Random {
	t.Helper()
	value, err := mutation.NewRandom(mutation.RandomConfig{
		NodeIDs: []core.NodeID{1, 2, 3}, MaxValue: 5, MaxTicks: 5,
		MaxActions: activeMaxPlanActions, MaxCrashed: 1, LifecycleCooldown: 48,
		MaxCrashEpisodes: 4, CrashRestartPairPercent: 5, PartitionHealPairPercent: 5,
	})
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func activeProjection(completion experiment.Completion) (corpus.Projection, error) {
	if !completion.Run.Succeeded || len(completion.Execution.Result.ModelStates) == 0 {
		return corpus.Projection{}, nil
	}
	projected, err := modelraft.ProjectCoverage(
		completion.Execution.Result.ModelStates,
		completion.Execution.Result.ModelEvents,
	)
	if err != nil {
		return corpus.Projection{}, err
	}
	return corpus.Projection{
		StateKeys: projected.StateKeys, TransitionKeys: projected.TransitionKeys,
	}, nil
}

func activeBaselineConsider(
	t *testing.T,
	collection *corpus.Corpus,
	ordinal int,
	execution activeExecution,
	projection corpus.Projection,
) (corpus.Entry, bool, string) {
	t.Helper()
	if !execution.Completion.Run.Succeeded ||
		len(execution.Completion.Execution.Result.ModelStates) == 0 {
		return corpus.Entry{}, false, "not_considered_unsuccessful"
	}
	entry, retained, err := collection.Consider(corpus.Input{
		ParentID: execution.Candidate.ParentLineage,
		Source:   "stage6_" + activeLineageToken(execution.Candidate.Lineage),
		Depth:    execution.Candidate.Depth, RunIndex: ordinal, Seed: execution.Seed,
		Plan:                   execution.Completion.Execution.Plan.Copy(),
		States:                 append([]model.State(nil), execution.Completion.Execution.Result.ModelStates...),
		SemanticStateKeys:      append([]int64(nil), projection.StateKeys...),
		SemanticTransitionKeys: append([]int64(nil), projection.TransitionKeys...),
	})
	if err != nil {
		t.Fatalf("%s Corpus.Consider: %v", execution.Candidate.Lineage, err)
	}
	return entry, retained, string(entry.AdmissionReason)
}

func mutateActiveChildren(
	t *testing.T,
	mutator *mutation.Random,
	campaignSeed int64,
	execution activeExecution,
	parent activeCandidate,
) []activeCandidate {
	t.Helper()
	children := make([]activeCandidate, 0, activeChildren)
	for slot := 0; slot < activeChildren; slot++ {
		purpose := fmt.Sprintf("child-%d", slot)
		seed := activeDerivedSeed(campaignSeed, parent.Lineage, purpose)
		sequences, err := mutator.Mutate(context.Background(), mutation.Request{
			Entry: corpus.Entry{
				ID: parent.Lineage, ParentID: parent.ParentLineage,
				Source: "stage6_mutation_parent", Depth: parent.Depth,
				RunIndex: execution.Completion.Run.Index, Seed: execution.Seed,
				Plan: execution.Completion.Execution.Plan.Copy(),
			},
			Count: 1, Seed: seed,
		})
		if err != nil {
			t.Fatalf("%s %s mutation: %v", parent.Lineage, purpose, err)
		}
		if len(sequences) != 1 {
			t.Fatalf("%s %s mutation count=%d", parent.Lineage, purpose, len(sequences))
		}
		if err := sequences[0].Validate(); err != nil {
			t.Fatalf("%s %s invalid child: %v", parent.Lineage, purpose, err)
		}
		if len(sequences[0].Actions) > activeMaxPlanActions {
			t.Fatalf("%s %s child actions=%d", parent.Lineage, purpose, len(sequences[0].Actions))
		}
		children = append(children, activeCandidate{
			Lineage: parent.Lineage + "/" + purpose, ParentLineage: parent.Lineage,
			Depth: parent.Depth + 1, Plan: sequences[0].Copy(),
		})
	}
	return children
}

func activeFact(
	execution activeExecution,
	projection corpus.Projection,
	baselineRetained bool,
	baselineReason string,
	decision facetbreadth.DecisionV1,
	activeAdmitted bool,
) activeCandidateFact {
	result := execution.Completion.Execution.Result
	return activeCandidateFact{
		Lineage: execution.Candidate.Lineage, ParentLineage: execution.Candidate.ParentLineage,
		Depth: execution.Candidate.Depth, ExecutionSeed: execution.Seed,
		InputPlanDigest: activePlanDigest(execution.Candidate.Plan),
		PlanDigest:      execution.Completion.Run.PlanDigest,
		TraceDigest:     execution.Completion.Run.TraceDigest,
		ModelPathDigest: execution.Completion.Run.ModelStatePathDigest,
		EngineStatus:    result.Status, ExperimentStatus: execution.Completion.Run.Status,
		OracleCodes: activeOracleCodes(result), EvaluationIdentity: activeEvaluationIdentity(execution.Evaluations),
		SemanticStateKeys:   append([]int64(nil), projection.StateKeys...),
		SemanticTransitions: append([]int64(nil), projection.TransitionKeys...),
		BaselineRetained:    baselineRetained, BaselineReason: baselineReason,
		FacetAdmitted: decision.Admitted, FacetReason: string(decision.Reason),
		ActiveAdmitted: activeAdmitted, Children: []string{},
	}
}

func (accumulator *activeAccumulator) observe(
	execution activeExecution,
	projection corpus.Projection,
	baselineRetained bool,
	baselineReason string,
	decision facetbreadth.DecisionV1,
	activeAdmitted bool,
) {
	result := execution.Completion.Execution.Result
	metrics := &accumulator.metrics
	metrics.ExecutedCandidates++
	if execution.Candidate.Depth == 0 {
		metrics.InitialCandidates++
	} else {
		metrics.MutationCandidates++
		metrics.ExecutedChildren++
	}
	metrics.TotalPlanActions += len(execution.Completion.Execution.Plan.Actions)
	metrics.TotalConcreteActions += len(result.Actions.Actions)
	metrics.TotalTraceSteps += len(result.Trace.Steps)
	metrics.TotalModelEvents += len(result.ModelEvents)
	metrics.TotalModelStates += len(result.ModelStates)
	metrics.StatusCounts[string(result.Status)]++
	metrics.TerminationCounts[string(result.Termination)]++
	for _, code := range activeOracleCodes(result) {
		metrics.OracleFindingCodes[code]++
	}
	accumulator.plans[execution.Completion.Run.PlanDigest] = struct{}{}
	accumulator.traces[execution.Completion.Run.TraceDigest] = struct{}{}
	accumulator.modelPaths[execution.Completion.Run.ModelStatePathDigest] = struct{}{}
	if baselineRetained {
		metrics.BaselineRetained++
	}
	metrics.BaselineReasons[baselineReason]++
	if decision.Admitted {
		metrics.FacetAdmitted++
	}
	metrics.FacetReasons[string(decision.Reason)]++
	if activeAdmitted {
		metrics.AdmittedParents++
		metrics.GeneratedChildren += activeChildren
	}
	if execution.Candidate.Depth > metrics.MaximumDepth {
		metrics.MaximumDepth = execution.Candidate.Depth
	}
	for _, evaluation := range execution.Evaluations {
		if evaluation.Status == facet.StatusInvalidEvidence {
			metrics.InvalidEvidence++
		}
		if evaluation.Status == facet.StatusInsufficientEvidence {
			metrics.InsufficientEvidence++
		}
		if _, ok := accumulator.facetClasses[evaluation.FacetID]; !ok {
			accumulator.facetClasses[evaluation.FacetID] = make(map[string]struct{})
		}
		for _, observation := range evaluation.Observations {
			accumulator.facetClasses[evaluation.FacetID][observation.Key.ClassID] = struct{}{}
		}
	}
	_ = projection
}

func (accumulator *activeAccumulator) finish(
	corpusSnapshot corpus.Snapshot,
	facetSnapshot facetbreadth.CoverageSnapshotV1,
	executed int,
) {
	metrics := &accumulator.metrics
	metrics.UniquePlanDigests = len(accumulator.plans)
	metrics.UniqueTraceDigests = len(accumulator.traces)
	metrics.UniqueModelPathDigests = len(accumulator.modelPaths)
	metrics.DuplicatePlanRatio = duplicateRatio(executed, len(accumulator.plans))
	metrics.DuplicateTraceRatio = duplicateRatio(executed, len(accumulator.traces))
	metrics.DuplicateModelPathRatio = duplicateRatio(executed, len(accumulator.modelPaths))
	metrics.RawModelStates = len(corpusSnapshot.CoverageKeys)
	metrics.SemanticStates = len(corpusSnapshot.SemanticStateKeys)
	metrics.SemanticTransitions = len(corpusSnapshot.SemanticTransitionKeys)
	metrics.FacetKeys = len(facetSnapshot.Covered)
	firstLengths := make([]int, 0, len(facetSnapshot.Covered))
	shortestLengths := make([]int, 0, len(facetSnapshot.Covered))
	firstRecords := make(map[string]struct{})
	shortestRecords := make(map[string]struct{})
	for _, covered := range facetSnapshot.Covered {
		metrics.FacetKeysByFacet[covered.Key.FacetID]++
		metrics.FacetFirstDiscovery[covered.CanonicalString] = covered.First.ApplyOrdinal
		firstLengths = append(firstLengths, covered.First.PlanActionCount)
		shortestLengths = append(shortestLengths, covered.Shortest.PlanActionCount)
		firstRecords[covered.First.RecordDigest] = struct{}{}
		shortestRecords[covered.Shortest.RecordDigest] = struct{}{}
	}
	metrics.Representatives = activeRepresentativeMetrics{
		FirstCount: len(firstRecords), ShortestCount: len(shortestRecords),
		AverageFirstActions:    activeAverage(firstLengths),
		MedianFirstActions:     activeMedian(firstLengths),
		AverageShortestActions: activeAverage(shortestLengths),
		MedianShortestActions:  activeMedian(shortestLengths),
	}
	for facetID, values := range accumulator.facetClasses {
		classes := make([]string, 0, len(values))
		for classID := range values {
			classes = append(classes, classID)
		}
		sort.Strings(classes)
		metrics.FacetClasses[facetID] = classes
	}
}

func activeOracleCodes(result engine.Result) []string {
	set := make(map[string]struct{}, len(result.OracleFindings))
	for _, finding := range result.OracleFindings {
		set[finding.Oracle+":"+finding.Code] = struct{}{}
	}
	values := make([]string, 0, len(set))
	for value := range set {
		values = append(values, value)
	}
	sort.Strings(values)
	return values
}

func activeQueueLineages(queue []activeCandidate) []string {
	result := make([]string, len(queue))
	for index, candidate := range queue {
		result[index] = candidate.Lineage
	}
	return result
}

func activeDigest(value any) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}

func duplicateRatio(total, unique int) float64 {
	if total == 0 {
		return 0
	}
	return float64(total-unique) / float64(total)
}

func activeAverage(values []int) float64 {
	if len(values) == 0 {
		return 0
	}
	total := 0
	for _, value := range values {
		total += value
	}
	return float64(total) / float64(len(values))
}

func activeMedian(values []int) float64 {
	if len(values) == 0 {
		return 0
	}
	copy := append([]int(nil), values...)
	sort.Ints(copy)
	middle := len(copy) / 2
	if len(copy)%2 == 1 {
		return float64(copy[middle])
	}
	return float64(copy[middle-1]+copy[middle]) / 2
}

func assertActiveCampaignEqual(t *testing.T, left, right activeCampaignResult) {
	t.Helper()
	leftJSON, err := json.Marshal(left)
	if err != nil {
		t.Fatal(err)
	}
	rightJSON, err := json.Marshal(right)
	if err != nil {
		t.Fatal(err)
	}
	if string(leftJSON) != string(rightJSON) {
		t.Fatalf("campaign repeat mismatch\nleft=%s\nright=%s", leftJSON, rightJSON)
	}
}

func assertActiveOverlap(t *testing.T, left, right activeCampaignResult) int {
	t.Helper()
	rightByLineage := make(map[string]activeCandidateFact, len(right.Facts))
	for _, fact := range right.Facts {
		rightByLineage[fact.Lineage] = fact
	}
	overlap := 0
	for _, fact := range left.Facts {
		other, ok := rightByLineage[fact.Lineage]
		if !ok {
			continue
		}
		overlap++
		fact.BaselineRetained, other.BaselineRetained = false, false
		fact.BaselineReason, other.BaselineReason = "", ""
		fact.FacetAdmitted, other.FacetAdmitted = false, false
		fact.FacetReason, other.FacetReason = "", ""
		fact.ActiveAdmitted, other.ActiveAdmitted = false, false
		fact.Children, other.Children = nil, nil
		if activeDigest(fact) != activeDigest(other) {
			t.Fatalf("overlap lineage %s execution facts differ\nleft=%+v\nright=%+v", fact.Lineage, fact, other)
		}
	}
	return overlap
}

func activeExclusiveCount(left, right activeCampaignResult) int {
	rightSet := make(map[string]struct{}, len(right.Facts))
	for _, fact := range right.Facts {
		rightSet[fact.Lineage] = struct{}{}
	}
	count := 0
	for _, fact := range left.Facts {
		if _, found := rightSet[fact.Lineage]; !found {
			count++
		}
	}
	return count
}

func activeHasNaturalNonNew(results ...activeCampaignResult) bool {
	for _, result := range results {
		if result.Metrics.FacetReasons[string(facetbreadth.DecisionNoNovelty)] > 0 ||
			result.Metrics.FacetReasons[string(facetbreadth.DecisionShorterRepresentative)] > 0 ||
			result.Metrics.FacetReasons[string(facetbreadth.DecisionNewAndShorter)] > 0 {
			return true
		}
	}
	return false
}

func activeFailureStatus(status engine.Status) bool {
	switch status {
	case engine.StatusRuntimeFailed, engine.StatusMappingFailed, engine.StatusOracleFailed:
		return true
	default:
		return false
	}
}

func assertActiveNoHarnessFailures(t *testing.T, result activeCampaignResult) {
	t.Helper()
	if result.Metrics.InvalidEvidence != 0 || result.Metrics.InsufficientEvidence != 0 {
		t.Fatalf("%d/%s invalid=%d insufficient=%d", result.Seed, result.Mode,
			result.Metrics.InvalidEvidence, result.Metrics.InsufficientEvidence)
	}
	for status, count := range result.Metrics.StatusCounts {
		if activeFailureStatus(engine.Status(status)) && count != 0 {
			t.Fatalf("%d/%s unexpected status %s=%d", result.Seed, result.Mode, status, count)
		}
	}
	for code, count := range result.Metrics.OracleFindingCodes {
		if count > 0 {
			t.Fatalf("%d/%s unexpected Oracle finding %s=%d", result.Seed, result.Mode, code, count)
		}
	}
}

func activeSignal(results []activeCampaignResult) string {
	var baseline, facet activeCampaignMetrics
	for _, result := range results {
		if result.Mode == activeCurrentBaseline {
			baseline.UniqueTraceDigests += result.Metrics.UniqueTraceDigests
			baseline.FacetKeys += result.Metrics.FacetKeys
			baseline.QueueExhausted = baseline.QueueExhausted || result.Metrics.QueueExhausted
		} else {
			facet.UniqueTraceDigests += result.Metrics.UniqueTraceDigests
			facet.FacetKeys += result.Metrics.FacetKeys
			facet.QueueExhausted = facet.QueueExhausted || result.Metrics.QueueExhausted
		}
	}
	switch {
	case facet.UniqueTraceDigests > baseline.UniqueTraceDigests &&
		facet.FacetKeys >= baseline.FacetKeys && !facet.QueueExhausted:
		return "SIGNAL_POSITIVE"
	case facet.UniqueTraceDigests < baseline.UniqueTraceDigests ||
		facet.FacetKeys < baseline.FacetKeys || facet.QueueExhausted && !baseline.QueueExhausted:
		return "SIGNAL_NEGATIVE"
	default:
		return "SIGNAL_NEUTRAL"
	}
}

func activeStableResultSummary(results []activeCampaignResult) string {
	encoded, err := json.Marshal(struct {
		Schema  string                 `json:"schema"`
		Results []activeCampaignResult `json:"results"`
	}{Schema: "stage6-active-ab-smoke-result-v1", Results: results})
	if err != nil {
		panic(err)
	}
	return strings.TrimSpace(string(encoded))
}
