package raft_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"reflect"
	"sort"
	"sync"
	"testing"

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/adapters/etcdraft"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/assurance/executionrecord"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/assurance/facet"
	facetraft "github.com/SuzumiyaHaruki/modelfuzz-ng/internal/assurance/facet/raft"
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
	pilotMaxValue    = 5
	pilotMaxLogIndex = 10
	pilotLargestTerm = 10
	pilotProfile     = modelraft.ProfileStorageSnapshot
)

type sourceFactory func(seed int64) (engine.ActionSource, error)

type pilotScenario struct {
	ID                string
	Family            string
	SourceAsset       string
	Seed              int64
	MaxPlanActions    int
	SnapshotThreshold uint64
	RetainEntries     uint64
	InitializerPlan   plan.PlanSequence
	NewSource         sourceFactory
}

type pilotKey struct {
	Canonical  string           `json:"canonical"`
	Digest     string           `json:"digest"`
	ClassID    string           `json:"class_id"`
	Occurrence facet.Occurrence `json:"first_occurrence"`
}

type pilotFacet struct {
	ID     string                 `json:"facet_id"`
	Status facet.EvaluationStatus `json:"status"`
	Keys   []pilotKey             `json:"keys"`
}

type pilotRun struct {
	ScenarioID       string        `json:"scenario_id"`
	Family           string        `json:"family"`
	SourceAsset      string        `json:"source_asset"`
	Profile          string        `json:"profile"`
	Nodes            int           `json:"nodes"`
	Seed             int64         `json:"seed"`
	PlanActions      int           `json:"plan_actions"`
	ConcreteActions  int           `json:"concrete_actions"`
	TraceSteps       int           `json:"trace_steps"`
	Effects          int           `json:"effects"`
	ModelEvents      int           `json:"model_events"`
	EngineStatus     engine.Status `json:"engine_status"`
	ExperimentStatus engine.Status `json:"experiment_status"`
	PlanDigest       string        `json:"plan_digest"`
	TraceDigest      string        `json:"trace_digest"`
	RecordDigest     string        `json:"record_digest"`
	CorpusAdmission  string        `json:"corpus_admission"`
	CorpusRetained   bool          `json:"corpus_retained"`
	Facets           []pilotFacet  `json:"facets"`
	Trace            core.Trace    `json:"-"`
}

type staticPlanSource struct {
	sequence plan.PlanSequence
	next     int
}

func (source *staticPlanSource) Reset(core.Observation) error {
	source.next = 0
	return nil
}

func (source *staticPlanSource) Next(core.Observation) (plan.PlanAction, bool, error) {
	if source.next >= len(source.sequence.Actions) {
		return plan.PlanAction{}, false, nil
	}
	action := source.sequence.Actions[source.next].Copy()
	source.next++
	return action, true, nil
}

type recordingSource struct {
	inner   engine.ActionSource
	source  string
	seed    int64
	actions []plan.PlanAction
}

func (source *recordingSource) Reset(initial core.Observation) error {
	source.actions = source.actions[:0]
	return source.inner.Reset(initial)
}

func (source *recordingSource) Next(observation core.Observation) (plan.PlanAction, bool, error) {
	action, more, err := source.inner.Next(observation)
	if err == nil && more {
		source.actions = append(source.actions, action.Copy())
	}
	return action, more, err
}

func (source *recordingSource) Sequence() plan.PlanSequence {
	actions := make([]plan.PlanAction, len(source.actions))
	for index, action := range source.actions {
		actions[index] = action.Copy()
	}
	return plan.PlanSequence{
		Actions: actions,
		Metadata: map[string]string{
			"source": source.source,
			"seed":   fmt.Sprintf("%d", source.seed),
		},
	}
}

type noMutation struct {
	mu    sync.Mutex
	calls int
}

func (mutator *noMutation) Name() string { return "stage4_no_mutation" }

func (mutator *noMutation) Mutate(context.Context, mutation.Request) ([]plan.PlanSequence, error) {
	mutator.mu.Lock()
	defer mutator.mu.Unlock()
	mutator.calls++
	return nil, fmt.Errorf("Stage 4 must not execute mutation")
}

func (mutator *noMutation) Calls() int {
	mutator.mu.Lock()
	defer mutator.mu.Unlock()
	return mutator.calls
}

type deterministicModelExecutor struct {
	mu    sync.Mutex
	calls int
}

func (executor *deterministicModelExecutor) Execute(_ context.Context, events []model.Event) ([]model.State, error) {
	executor.mu.Lock()
	executor.calls++
	executor.mu.Unlock()
	if len(events) == 0 {
		return nil, nil
	}
	states := make([]model.State, len(events))
	for index := range events {
		states[index] = model.State{
			Text: fmt.Sprintf("stage4-state-%03d", index),
			Key:  int64(index + 1),
		}
	}
	return states, nil
}

func (executor *deterministicModelExecutor) Calls() int {
	executor.mu.Lock()
	defer executor.mu.Unlock()
	return executor.calls
}

func staticScenarioSource(sequence plan.PlanSequence) sourceFactory {
	return func(int64) (engine.ActionSource, error) {
		return &staticPlanSource{sequence: sequence.Copy()}, nil
	}
}

func snapshotPartitionSource(config policy.SnapshotPartitionConfig) sourceFactory {
	return func(seed int64) (engine.ActionSource, error) {
		return policy.NewSnapshotPartition(seed, config)
	}
}

func snapshotFastForwardSource(config policy.SnapshotFastForwardConfig) sourceFactory {
	return func(seed int64) (engine.ActionSource, error) {
		return policy.NewSnapshotFastForward(seed, config)
	}
}

func runPilotScenario(t *testing.T, scenario pilotScenario) pilotRun {
	t.Helper()
	if scenario.MaxPlanActions <= 0 {
		t.Fatalf("%s has invalid max plan actions", scenario.ID)
	}
	mutator := &noMutation{}
	modelExecutor := &deterministicModelExecutor{}
	var completion experiment.Completion
	completionCount := 0

	runner, err := experiment.New(experiment.Config{
		Runs:                  1,
		BaseSeed:              scenario.Seed,
		Parallelism:           1,
		InitialPopulation:     1,
		MutationsPerNewState:  1,
		MaxMutationsPerCorpus: 1,
		MaxReadyCandidates:    1,
		MinNewModelStates:     1,
	})
	if err != nil {
		t.Fatalf("%s runner: %v", scenario.ID, err)
	}
	configFingerprint := pilotConfigFingerprint(t, scenario)
	options := experiment.FeedbackOptions{
		InitializerName: "stage4_" + scenario.ID,
		Initializer: func(context.Context, int, int64) ([]plan.PlanSequence, error) {
			return []plan.PlanSequence{scenario.InitializerPlan.Copy()}, nil
		},
		Mutator:                  mutator,
		ConfigurationFingerprint: configFingerprint,
		Hooks: experiment.Hooks{
			OnRunComplete: func(got experiment.Completion) error {
				completion = got
				completionCount++
				return nil
			},
		},
	}
	report, _, err := runner.RunFeedback(
		context.Background(),
		options,
		func(ctx context.Context, _ int, seed int64, _ experiment.Candidate) (experiment.FeedbackExecution, error) {
			source, sourceErr := scenario.NewSource(seed)
			if sourceErr != nil {
				return experiment.FeedbackExecution{}, sourceErr
			}
			recorded := &recordingSource{inner: source, source: scenario.ID, seed: seed}
			execution, executeErr := executeRealEtcdRaft(
				ctx, scenario, seed, recorded, modelExecutor,
			)
			return experiment.FeedbackExecution{
				Result: execution,
				Plan:   recorded.Sequence(),
			}, executeErr
		},
	)
	if err != nil {
		t.Fatalf("%s RunFeedback: %v", scenario.ID, err)
	}
	if report.CompletedRuns != 1 || report.InitialExecutions != 1 ||
		report.ExecutedMutations != 0 || completionCount != 1 {
		t.Fatalf("%s runner counts: report=%+v completions=%d", scenario.ID, report, completionCount)
	}
	if mutator.Calls() != 0 {
		t.Fatalf("%s executed %d mutation calls", scenario.ID, mutator.Calls())
	}
	if completion.Execution.Result.Status != engine.StatusCompleted || !completion.Run.Succeeded {
		t.Fatalf(
			"%s did not complete: engine=%s experiment=%s error=%q",
			scenario.ID, completion.Execution.Result.Status, completion.Run.Status, completion.Run.Error,
		)
	}
	if completion.Execution.Result.BudgetExhausted {
		t.Fatalf("%s exhausted execution budget: %s", scenario.ID, completion.Execution.Result.Termination)
	}
	if modelExecutor.Calls() != 1 {
		t.Fatalf("%s model executor calls=%d, want 1", scenario.ID, modelExecutor.Calls())
	}

	record, err := executionrecord.BuildV1(executionrecord.BuildInput{
		Completion:               completion,
		ConfigurationFingerprint: configFingerprint,
		FailureSignature: executionrecord.FailureSignatureInput{
			Availability: executionrecord.FailureSignatureNotApplicable,
		},
	})
	if err != nil {
		t.Fatalf("%s BuildV1: %v", scenario.ID, err)
	}
	if record.Replay.Replayable {
		t.Fatalf("%s unexpectedly became replayable without artifact references", scenario.ID)
	}

	resultBefore, err := json.Marshal(completion.Execution.Result)
	if err != nil {
		t.Fatal(err)
	}
	runBefore := completion.Run
	executorCallsBefore := modelExecutor.Calls()
	initial := completion.Execution.Result.Initial.Copy()
	trace := completion.Execution.Result.Trace.Copy()
	evaluations, err := facet.EvaluateAll(facet.EvaluationInputV1{
		Record:             record,
		InitialObservation: &initial,
		Trace:              &trace,
		ModelEvents:        copyEvents(completion.Execution.Result.ModelEvents),
		ModelStates:        append([]model.State(nil), completion.Execution.Result.ModelStates...),
	}, facetraft.CatalogV1())
	if err != nil {
		t.Fatalf("%s EvaluateAll: %v", scenario.ID, err)
	}
	resultAfter, err := json.Marshal(completion.Execution.Result)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(resultBefore, resultAfter) {
		t.Fatalf("%s Facet evaluation changed engine result", scenario.ID)
	}
	if !reflect.DeepEqual(runBefore, completion.Run) {
		t.Fatalf("%s Facet evaluation changed experiment/corpus outcome", scenario.ID)
	}
	if modelExecutor.Calls() != executorCallsBefore {
		t.Fatalf("%s Facet evaluation invoked model executor", scenario.ID)
	}

	summary := summarizePilotRun(t, scenario, completion, record, evaluations)
	validateCompleteFacetStatuses(t, summary)
	return summary
}

func executeRealEtcdRaft(
	ctx context.Context,
	scenario pilotScenario,
	seed int64,
	source engine.ActionSource,
	modelExecutor model.Executor,
) (engine.Result, error) {
	adapterConfig := etcdraft.DefaultConfig()
	adapterConfig.Snapshot = etcdraft.SnapshotPolicy{
		Threshold:     scenario.SnapshotThreshold,
		RetainEntries: scenario.RetainEntries,
	}
	adapterConfig.Logger = &raftlib.DefaultLogger{Logger: log.New(io.Discard, "", 0)}
	adapter, err := etcdraft.New(adapterConfig)
	if err != nil {
		return engine.Result{}, err
	}
	runtime, err := runtimepkg.New(adapter, runtimepkg.Config{
		ExecutionID: core.ExecutionID("stage4-" + scenario.ID),
		Seed:        seed,
		Limits: runtimepkg.Limits{
			MaxActions:        uint64(scenario.MaxPlanActions * 8),
			MaxTicks:          512,
			MaxEffects:        50000,
			MaxQueuedMessages: 10000,
		},
	})
	if err != nil {
		return engine.Result{}, err
	}
	mapper, err := modelraft.NewMapperWithConfig(modelraft.Config{
		NodeIDs:        []core.NodeID{1, 2, 3},
		MaxValue:       pilotMaxValue,
		MaxLogIndex:    pilotMaxLogIndex,
		LargestTerm:    pilotLargestTerm,
		EmitLeaderNoOp: true,
		Profile:        pilotProfile,
	})
	if err != nil {
		return engine.Result{}, err
	}
	runner, err := engine.New(
		runtime,
		plan.NewDefaultResolver(),
		mapper,
		modelExecutor,
		engine.Config{
			MaxPlanActions:      scenario.MaxPlanActions,
			MaxConsecutiveNoops: 32,
		},
		oracleraft.New(),
	)
	if err != nil {
		return engine.Result{}, err
	}
	return runner.RunSource(ctx, source, scenario.MaxPlanActions)
}

func summarizePilotRun(
	t *testing.T,
	scenario pilotScenario,
	completion experiment.Completion,
	record executionrecord.CompletedExecutionRecordV1,
	evaluations []facet.EvaluationV1,
) pilotRun {
	t.Helper()
	summary := pilotRun{
		ScenarioID:       scenario.ID,
		Family:           scenario.Family,
		SourceAsset:      scenario.SourceAsset,
		Profile:          pilotProfile,
		Nodes:            3,
		Seed:             completion.Run.Seed,
		PlanActions:      len(completion.Execution.Plan.Actions),
		ConcreteActions:  len(completion.Execution.Result.Actions.Actions),
		TraceSteps:       len(completion.Execution.Result.Trace.Steps),
		Effects:          countEffects(completion.Execution.Result.Trace),
		ModelEvents:      len(completion.Execution.Result.ModelEvents),
		EngineStatus:     completion.Execution.Result.Status,
		ExperimentStatus: completion.Run.Status,
		PlanDigest:       completion.Run.PlanDigest,
		TraceDigest:      completion.Run.TraceDigest,
		RecordDigest:     record.RecordDigest,
		CorpusAdmission:  completion.Run.CorpusAdmission,
		CorpusRetained:   completion.Run.Retained,
		Facets:           make([]pilotFacet, len(evaluations)),
		Trace:            completion.Execution.Result.Trace.Copy(),
	}
	for evaluationIndex, evaluation := range evaluations {
		facetSummary := pilotFacet{
			ID:     evaluation.FacetID,
			Status: evaluation.Status,
			Keys:   make([]pilotKey, len(evaluation.Observations)),
		}
		for keyIndex, observation := range evaluation.Observations {
			canonical, err := observation.Key.CanonicalString()
			if err != nil {
				t.Fatalf("%s canonical key: %v", scenario.ID, err)
			}
			facetSummary.Keys[keyIndex] = pilotKey{
				Canonical:  canonical,
				Digest:     observation.KeyDigest,
				ClassID:    observation.Key.ClassID,
				Occurrence: observation.Occurrence.Copy(),
			}
		}
		summary.Facets[evaluationIndex] = facetSummary
	}
	return summary
}

func validateCompleteFacetStatuses(t *testing.T, summary pilotRun) {
	t.Helper()
	if len(summary.Facets) != 3 {
		t.Fatalf("%s evaluations=%d, want 3", summary.ScenarioID, len(summary.Facets))
	}
	for _, evaluation := range summary.Facets {
		switch evaluation.ID {
		case "raft.election_role_term_shape", "raft.replication_alignment_shape":
			if evaluation.Status != facet.StatusEvaluated {
				t.Fatalf("%s %s status=%s", summary.ScenarioID, evaluation.ID, evaluation.Status)
			}
		case "raft.snapshot_lifecycle_event":
			if evaluation.Status != facet.StatusEvaluated &&
				evaluation.Status != facet.StatusNotApplicable {
				t.Fatalf("%s %s status=%s", summary.ScenarioID, evaluation.ID, evaluation.Status)
			}
		default:
			t.Fatalf("%s unexpected Facet %s", summary.ScenarioID, evaluation.ID)
		}
		if evaluation.Status == facet.StatusInsufficientEvidence ||
			evaluation.Status == facet.StatusInvalidEvidence {
			t.Fatalf("%s %s produced %s", summary.ScenarioID, evaluation.ID, evaluation.Status)
		}
	}
}

func pilotConfigFingerprint(t *testing.T, scenario pilotScenario) string {
	t.Helper()
	payload := struct {
		Schema            string `json:"schema"`
		Scenario          string `json:"scenario"`
		Seed              int64  `json:"seed"`
		Nodes             int    `json:"nodes"`
		Profile           string `json:"profile"`
		MaxValue          int    `json:"max_value"`
		MaxLogIndex       uint64 `json:"max_log_index"`
		LargestTerm       uint64 `json:"largest_term"`
		SnapshotThreshold uint64 `json:"snapshot_threshold"`
		RetainEntries     uint64 `json:"retain_entries"`
		MaxPlanActions    int    `json:"max_plan_actions"`
	}{
		Schema: "stage4-realtrace-pilot-config-v1", Scenario: scenario.ID,
		Seed: scenario.Seed, Nodes: 3, Profile: pilotProfile,
		MaxValue: pilotMaxValue, MaxLogIndex: pilotMaxLogIndex,
		LargestTerm: pilotLargestTerm, SnapshotThreshold: scenario.SnapshotThreshold,
		RetainEntries: scenario.RetainEntries, MaxPlanActions: scenario.MaxPlanActions,
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}

func copyEvents(events []model.Event) []model.Event {
	result := make([]model.Event, len(events))
	for index, event := range events {
		result[index] = event.Copy()
	}
	return result
}

func countEffects(trace core.Trace) int {
	total := 0
	for _, step := range trace.Steps {
		total += len(step.Effects)
	}
	return total
}

func facetByID(summary pilotRun, id string) pilotFacet {
	for _, item := range summary.Facets {
		if item.ID == id {
			return item
		}
	}
	return pilotFacet{}
}

func canonicalSemanticSummary(summary pilotRun) []byte {
	copy := summary
	copy.Trace = core.Trace{}
	encoded, err := json.Marshal(copy)
	if err != nil {
		panic(err)
	}
	return encoded
}

func sortedKeys(set map[string]struct{}) []string {
	result := make([]string, 0, len(set))
	for key := range set {
		result = append(result, key)
	}
	sort.Strings(result)
	return result
}

func hasClass(summary pilotRun, facetID, classID string) bool {
	for _, key := range facetByID(summary, facetID).Keys {
		if key.ClassID == classID {
			return true
		}
	}
	return false
}

func statusCounts(reports []pilotRun) map[string]int {
	result := make(map[string]int)
	for _, report := range reports {
		for _, evaluation := range report.Facets {
			result[evaluation.ID+":"+string(evaluation.Status)]++
		}
	}
	return result
}
