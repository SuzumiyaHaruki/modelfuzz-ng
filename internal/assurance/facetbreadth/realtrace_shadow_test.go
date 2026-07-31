package facetbreadth_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"reflect"
	"testing"

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/adapters/etcdraft"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/assurance/executionrecord"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/assurance/facet"
	raftfacet "github.com/SuzumiyaHaruki/modelfuzz-ng/internal/assurance/facet/raft"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/assurance/facetbreadth"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/core"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/engine"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/experiment"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/model"
	modelraft "github.com/SuzumiyaHaruki/modelfuzz-ng/internal/model/raft"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/mutation"
	oracleraft "github.com/SuzumiyaHaruki/modelfuzz-ng/internal/oracle/raft"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/plan"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/policy"
	runtimepkg "github.com/SuzumiyaHaruki/modelfuzz-ng/internal/runtime"
	raftlib "go.etcd.io/raft/v3"
)

const (
	shadowMaxValue    = 5
	shadowMaxLogIndex = 10
	shadowLargestTerm = 10
)

type shadowSourceFactory func(int64) (engine.ActionSource, error)

type shadowScenario struct {
	id                string
	seed              int64
	maxPlanActions    int
	snapshotThreshold uint64
	retainEntries     uint64
	initializer       plan.PlanSequence
	newSource         shadowSourceFactory
}

type shadowPlanSource struct {
	sequence plan.PlanSequence
	next     int
}

func (source *shadowPlanSource) Reset(core.Observation) error {
	source.next = 0
	return nil
}

func (source *shadowPlanSource) Next(core.Observation) (plan.PlanAction, bool, error) {
	if source.next >= len(source.sequence.Actions) {
		return plan.PlanAction{}, false, nil
	}
	action := source.sequence.Actions[source.next].Copy()
	source.next++
	return action, true, nil
}

type shadowRecordingSource struct {
	inner   engine.ActionSource
	id      string
	seed    int64
	actions []plan.PlanAction
}

func (source *shadowRecordingSource) Reset(initial core.Observation) error {
	source.actions = source.actions[:0]
	return source.inner.Reset(initial)
}

func (source *shadowRecordingSource) Next(observation core.Observation) (plan.PlanAction, bool, error) {
	action, more, err := source.inner.Next(observation)
	if err == nil && more {
		source.actions = append(source.actions, action.Copy())
	}
	return action, more, err
}

func (source *shadowRecordingSource) Sequence() plan.PlanSequence {
	actions := make([]plan.PlanAction, len(source.actions))
	for index, action := range source.actions {
		actions[index] = action.Copy()
	}
	return plan.PlanSequence{
		Actions:  actions,
		Metadata: map[string]string{"source": source.id, "seed": fmt.Sprintf("%d", source.seed)},
	}
}

type shadowNoMutation struct{}

func (*shadowNoMutation) Name() string { return "stage5_shadow_no_mutation" }

func (*shadowNoMutation) Mutate(context.Context, mutation.Request) ([]plan.PlanSequence, error) {
	return nil, fmt.Errorf("Stage 5 shadow must not execute mutation")
}

type shadowModelExecutor struct {
	calls int
}

func (executor *shadowModelExecutor) Execute(_ context.Context, events []model.Event) ([]model.State, error) {
	executor.calls++
	if len(events) == 0 {
		return nil, nil
	}
	states := make([]model.State, len(events))
	for index := range events {
		states[index] = model.State{
			Text: fmt.Sprintf("stage5-shadow-state-%03d", index), Key: int64(index + 1),
		}
	}
	return states, nil
}

type shadowResult struct {
	completion  experiment.Completion
	record      executionrecord.CompletedExecutionRecordV1
	evaluations []facet.EvaluationV1
	modelCalls  int
}

func TestRealTraceBreadthShadow(t *testing.T) {
	catalog, err := facetbreadth.BuildCatalogIdentityV1(raftfacet.CatalogV1())
	if err != nil {
		t.Fatal(err)
	}
	left, err := facetbreadth.NewCoverageStateV1(catalog)
	if err != nil {
		t.Fatal(err)
	}
	right, err := facetbreadth.NewCoverageStateV1(catalog)
	if err != nil {
		t.Fatal(err)
	}
	var decisions []facetbreadth.DecisionV1
	for ordinal, scenario := range shadowScenarios() {
		result := runShadowScenario(t, scenario)
		resultBefore, err := json.Marshal(result.completion.Execution.Result)
		if err != nil {
			t.Fatal(err)
		}
		runBefore := result.completion.Run
		recordBefore := result.record
		evaluationsBefore := copyShadowEvaluations(result.evaluations)
		callsBefore := result.modelCalls

		summary, err := facetbreadth.BuildCandidateSummaryV1(result.record, result.evaluations)
		if err != nil {
			t.Fatalf("%s BuildCandidateSummaryV1: %v", scenario.id, err)
		}
		decision, err := left.Apply(uint64(ordinal), summary)
		if err != nil {
			t.Fatalf("%s Apply left: %v", scenario.id, err)
		}
		if _, err := right.Apply(uint64(ordinal), summary); err != nil {
			t.Fatalf("%s Apply right: %v", scenario.id, err)
		}
		decisions = append(decisions, decision)

		resultAfter, err := json.Marshal(result.completion.Execution.Result)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(resultBefore, resultAfter) ||
			!reflect.DeepEqual(runBefore, result.completion.Run) ||
			!reflect.DeepEqual(recordBefore, result.record) ||
			!reflect.DeepEqual(evaluationsBefore, result.evaluations) ||
			callsBefore != result.modelCalls {
			t.Fatalf("%s breadth shadow changed completed execution facts", scenario.id)
		}
	}
	leftSnapshot := left.Snapshot()
	rightSnapshot := right.Snapshot()
	if !reflect.DeepEqual(leftSnapshot, rightSnapshot) {
		t.Fatal("identical real-trace Apply sequences produced different states")
	}
	counts := map[string]int{}
	for _, covered := range leftSnapshot.Covered {
		counts[covered.Key.FacetID]++
		if covered.First.RecordDigest == "" || covered.Shortest.RecordDigest == "" {
			t.Fatalf("key %s lacks representatives", covered.CanonicalString)
		}
	}
	want := map[string]int{
		"raft.election_role_term_shape":    6,
		"raft.replication_alignment_shape": 4,
		"raft.snapshot_lifecycle_event":    10,
	}
	if !reflect.DeepEqual(counts, want) || len(leftSnapshot.Covered) != 20 {
		t.Fatalf("shadow union counts=%v total=%d want %v total=20", counts, len(leftSnapshot.Covered), want)
	}
	if len(leftSnapshot.Covered) > facetbreadth.MaxCatalogKeysV1 ||
		len(leftSnapshot.Covered)*2 > facetbreadth.MaxRepresentativeSlotsV1 {
		t.Fatal("shadow union exceeded compact bounds")
	}
	if len(decisions) != 8 {
		t.Fatalf("decisions=%d want 8", len(decisions))
	}
	t.Logf("Stage 5 shadow decisions: %+v", decisions)
}

func TestFeatureOffExecutionEquivalence(t *testing.T) {
	scenario := shadowScenarios()[0]
	off := runShadowScenario(t, scenario)
	on := runShadowScenario(t, scenario)

	catalog, err := facetbreadth.BuildCatalogIdentityV1(raftfacet.CatalogV1())
	if err != nil {
		t.Fatal(err)
	}
	state, err := facetbreadth.NewCoverageStateV1(catalog)
	if err != nil {
		t.Fatal(err)
	}
	summary, err := facetbreadth.BuildCandidateSummaryV1(on.record, on.evaluations)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := state.Apply(0, summary); err != nil {
		t.Fatal(err)
	}

	offResult, err := json.Marshal(off.completion.Execution.Result)
	if err != nil {
		t.Fatal(err)
	}
	onResult, err := json.Marshal(on.completion.Execution.Result)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(offResult, onResult) {
		t.Fatal("feature-on engine.Result differs from feature-off")
	}
	offRun, onRun := off.completion.Run, on.completion.Run
	offRun.DurationMillis, offRun.DurationMicros = 0, 0
	onRun.DurationMillis, onRun.DurationMicros = 0, 0
	if !reflect.DeepEqual(offRun, onRun) {
		t.Fatalf("feature-on semantic experiment.Run differs from feature-off:\noff=%+v\non=%+v", offRun, onRun)
	}
	if !reflect.DeepEqual(off.record, on.record) {
		t.Fatal("feature-on completed record differs from feature-off")
	}
	if !reflect.DeepEqual(off.evaluations, on.evaluations) {
		t.Fatal("feature-on Facet evaluations differ from feature-off")
	}
	if off.modelCalls != on.modelCalls {
		t.Fatal("feature-on model executor call count differs from feature-off")
	}
}

func runShadowScenario(t *testing.T, scenario shadowScenario) shadowResult {
	t.Helper()
	var completion experiment.Completion
	completionCount := 0
	runner, err := experiment.New(experiment.Config{
		Runs: 1, BaseSeed: scenario.seed, Parallelism: 1, InitialPopulation: 1,
		MutationsPerNewState: 1, MaxMutationsPerCorpus: 1, MaxReadyCandidates: 1,
		MinNewModelStates: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	fingerprint := shadowConfigFingerprint(t, scenario)
	modelExecutor := &shadowModelExecutor{}
	report, _, err := runner.RunFeedback(
		context.Background(),
		experiment.FeedbackOptions{
			InitializerName: "stage5_shadow_" + scenario.id,
			Initializer: func(context.Context, int, int64) ([]plan.PlanSequence, error) {
				return []plan.PlanSequence{scenario.initializer.Copy()}, nil
			},
			Mutator:                  &shadowNoMutation{},
			ConfigurationFingerprint: fingerprint,
			Hooks: experiment.Hooks{OnRunComplete: func(got experiment.Completion) error {
				completion = got
				completionCount++
				return nil
			}},
		},
		func(ctx context.Context, _ int, seed int64, _ experiment.Candidate) (experiment.FeedbackExecution, error) {
			source, err := scenario.newSource(seed)
			if err != nil {
				return experiment.FeedbackExecution{}, err
			}
			recorded := &shadowRecordingSource{inner: source, id: scenario.id, seed: seed}
			result, err := executeShadowEtcdRaft(ctx, scenario, seed, recorded, modelExecutor)
			return experiment.FeedbackExecution{Result: result, Plan: recorded.Sequence()}, err
		},
	)
	if err != nil {
		t.Fatalf("%s RunFeedback: %v", scenario.id, err)
	}
	if report.CompletedRuns != 1 || report.InitialExecutions != 1 ||
		report.ExecutedMutations != 0 || completionCount != 1 {
		t.Fatalf("%s runner report=%+v completions=%d", scenario.id, report, completionCount)
	}
	if completion.Execution.Result.Status != engine.StatusCompleted || !completion.Run.Succeeded {
		t.Fatalf("%s did not complete: engine=%s experiment=%s", scenario.id,
			completion.Execution.Result.Status, completion.Run.Status)
	}
	if modelExecutor.calls != 1 {
		t.Fatalf("%s model calls=%d want 1", scenario.id, modelExecutor.calls)
	}
	record, err := executionrecord.BuildV1(executionrecord.BuildInput{
		Completion: completion, ConfigurationFingerprint: fingerprint,
		FailureSignature: executionrecord.FailureSignatureInput{
			Availability: executionrecord.FailureSignatureNotApplicable,
		},
	})
	if err != nil {
		t.Fatalf("%s BuildV1: %v", scenario.id, err)
	}
	initial := completion.Execution.Result.Initial.Copy()
	trace := completion.Execution.Result.Trace.Copy()
	evaluations, err := facet.EvaluateAll(facet.EvaluationInputV1{
		Record: record, InitialObservation: &initial, Trace: &trace,
		ModelEvents: copyShadowEvents(completion.Execution.Result.ModelEvents),
		ModelStates: append([]model.State(nil), completion.Execution.Result.ModelStates...),
	}, raftfacet.CatalogV1())
	if err != nil {
		t.Fatalf("%s EvaluateAll: %v", scenario.id, err)
	}
	for _, evaluation := range evaluations {
		if evaluation.Status == facet.StatusInvalidEvidence ||
			evaluation.Status == facet.StatusInsufficientEvidence {
			t.Fatalf("%s facet %s status=%s", scenario.id, evaluation.FacetID, evaluation.Status)
		}
	}
	return shadowResult{
		completion: completion, record: record, evaluations: evaluations, modelCalls: modelExecutor.calls,
	}
}

func executeShadowEtcdRaft(
	ctx context.Context,
	scenario shadowScenario,
	seed int64,
	source engine.ActionSource,
	modelExecutor model.Executor,
) (engine.Result, error) {
	adapterConfig := etcdraft.DefaultConfig()
	adapterConfig.Snapshot = etcdraft.SnapshotPolicy{
		Threshold: scenario.snapshotThreshold, RetainEntries: scenario.retainEntries,
	}
	adapterConfig.Logger = &raftlib.DefaultLogger{Logger: log.New(io.Discard, "", 0)}
	adapter, err := etcdraft.New(adapterConfig)
	if err != nil {
		return engine.Result{}, err
	}
	runtime, err := runtimepkg.New(adapter, runtimepkg.Config{
		ExecutionID: core.ExecutionID("stage5-shadow-" + scenario.id), Seed: seed,
		Limits: runtimepkg.Limits{
			MaxActions: uint64(scenario.maxPlanActions * 8), MaxTicks: 512,
			MaxEffects: 50000, MaxQueuedMessages: 10000,
		},
	})
	if err != nil {
		return engine.Result{}, err
	}
	mapper, err := modelraft.NewMapperWithConfig(modelraft.Config{
		NodeIDs: []core.NodeID{1, 2, 3}, MaxValue: shadowMaxValue,
		MaxLogIndex: shadowMaxLogIndex, LargestTerm: shadowLargestTerm,
		EmitLeaderNoOp: true, Profile: modelraft.ProfileStorageSnapshot,
	})
	if err != nil {
		return engine.Result{}, err
	}
	executor, err := engine.New(
		runtime, plan.NewDefaultResolver(), mapper, modelExecutor,
		engine.Config{MaxPlanActions: scenario.maxPlanActions, MaxConsecutiveNoops: 32},
		oracleraft.New(),
	)
	if err != nil {
		return engine.Result{}, err
	}
	return executor.RunSource(ctx, source, scenario.maxPlanActions)
}

func shadowScenarios() []shadowScenario {
	election := plan.PlanSequence{Actions: []plan.PlanAction{
		shadowTimeout(1), shadowMessage(plan.ActionDeliver, 1, 2),
		shadowMessage(plan.ActionDeliver, 2, 1),
	}}
	contention := plan.PlanSequence{Actions: []plan.PlanAction{
		shadowTimeout(1), shadowTimeout(2), shadowMessage(plan.ActionDeliver, 1, 3),
		shadowMessage(plan.ActionDeliver, 3, 1),
	}}
	replication := plan.PlanSequence{Actions: []plan.PlanAction{
		shadowTimeout(1), shadowMessage(plan.ActionDeliver, 1, 2),
		shadowMessage(plan.ActionDeliver, 2, 1),
		{Kind: plan.ActionRequest, Node: 1, Request: "1"},
		shadowMessage(plan.ActionDeliver, 1, 2), shadowMessage(plan.ActionDeliver, 2, 1),
		shadowMessage(plan.ActionDeliver, 1, 2), shadowMessage(plan.ActionDeliver, 2, 1),
		shadowMessage(plan.ActionDeliver, 1, 2),
	}}
	recovery := plan.PlanSequence{Actions: []plan.PlanAction{
		{Kind: plan.ActionCrash, Node: 3}, shadowTimeout(1),
		shadowMessage(plan.ActionDeliver, 1, 2), shadowMessage(plan.ActionDeliver, 2, 1),
		{Kind: plan.ActionRestart, Node: 3}, shadowMessage(plan.ActionDeliver, 1, 3),
		shadowMessage(plan.ActionDeliver, 3, 1), shadowMessage(plan.ActionDeliver, 1, 3),
		shadowMessage(plan.ActionDeliver, 3, 1), shadowMessage(plan.ActionDeliver, 1, 3),
	}}
	snapshot := policy.SnapshotPartitionConfig{
		NodeIDs: []core.NodeID{1, 2, 3}, MaxValue: shadowMaxValue,
		MaxLogIndex: shadowMaxLogIndex, SnapshotThreshold: 3, RetainEntries: 1,
	}
	failure := snapshot
	failure.FailFirstSnapshot = true
	duplicate := snapshot
	duplicate.DuplicateSnapshot = true
	fastForward := policy.SnapshotFastForwardConfig{
		NodeIDs: []core.NodeID{1, 2, 3}, MaxValue: shadowMaxValue,
		MaxLogIndex: shadowMaxLogIndex, SnapshotThreshold: 4, RetainEntries: 1,
	}
	return []shadowScenario{
		shadowStatic("election_stabilization", 4401, election),
		shadowStatic("election_contention", 4402, contention),
		shadowStatic("replication_lag_catchup", 4403, replication),
		shadowStatic("crash_restart_recovery", 4404, recovery),
		shadowDirected("snapshot_catchup_success", 4405, 3, func(seed int64) (engine.ActionSource, error) {
			return policy.NewSnapshotPartition(seed, snapshot)
		}),
		shadowDirected("snapshot_failure_retry", 4406, 3, func(seed int64) (engine.ActionSource, error) {
			return policy.NewSnapshotPartition(seed, failure)
		}),
		shadowDirected("snapshot_duplicate_stale", 4407, 3, func(seed int64) (engine.ActionSource, error) {
			return policy.NewSnapshotPartition(seed, duplicate)
		}),
		shadowDirected("snapshot_fast_forward", 4408, 4, func(seed int64) (engine.ActionSource, error) {
			return policy.NewSnapshotFastForward(seed, fastForward)
		}),
	}
}

func shadowStatic(id string, seed int64, sequence plan.PlanSequence) shadowScenario {
	return shadowScenario{
		id: id, seed: seed, maxPlanActions: len(sequence.Actions) + 1,
		snapshotThreshold: 3, retainEntries: 1, initializer: sequence.Copy(),
		newSource: func(int64) (engine.ActionSource, error) {
			return &shadowPlanSource{sequence: sequence.Copy()}, nil
		},
	}
}

func shadowDirected(
	id string,
	seed int64,
	threshold uint64,
	factory shadowSourceFactory,
) shadowScenario {
	return shadowScenario{
		id: id, seed: seed, maxPlanActions: 180, snapshotThreshold: threshold, retainEntries: 1,
		initializer: plan.PlanSequence{Actions: []plan.PlanAction{shadowTimeout(core.NodeID(1 + seed%3))}},
		newSource:   factory,
	}
}

func shadowTimeout(node core.NodeID) plan.PlanAction {
	return plan.PlanAction{Kind: plan.ActionTimeout, Node: node}
}

func shadowMessage(kind plan.ActionKind, from, to core.NodeID) plan.PlanAction {
	return plan.PlanAction{
		Kind: kind, Messages: &plan.MessageRangeSelector{
			Link: core.LinkID{From: from, To: to}, Count: 1,
		},
	}
}

func shadowConfigFingerprint(t *testing.T, scenario shadowScenario) string {
	t.Helper()
	payload := struct {
		Schema            string `json:"schema"`
		Scenario          string `json:"scenario"`
		Seed              int64  `json:"seed"`
		MaxPlanActions    int    `json:"max_plan_actions"`
		SnapshotThreshold uint64 `json:"snapshot_threshold"`
		RetainEntries     uint64 `json:"retain_entries"`
	}{
		Schema: "stage5-realtrace-shadow-config-v1", Scenario: scenario.id, Seed: scenario.seed,
		MaxPlanActions: scenario.maxPlanActions, SnapshotThreshold: scenario.snapshotThreshold,
		RetainEntries: scenario.retainEntries,
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}

func copyShadowEvents(events []model.Event) []model.Event {
	result := make([]model.Event, len(events))
	for index, event := range events {
		result[index] = event.Copy()
	}
	return result
}

func copyShadowEvaluations(evaluations []facet.EvaluationV1) []facet.EvaluationV1 {
	result := make([]facet.EvaluationV1, len(evaluations))
	for index, evaluation := range evaluations {
		result[index] = evaluation.Copy()
	}
	return result
}
