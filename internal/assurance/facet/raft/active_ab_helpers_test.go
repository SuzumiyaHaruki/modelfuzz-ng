package raft_test

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"testing"

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/assurance/executionrecord"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/assurance/facet"
	facetraft "github.com/SuzumiyaHaruki/modelfuzz-ng/internal/assurance/facet/raft"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/core"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/engine"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/experiment"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/model"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/plan"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/policy"
)

const (
	activeCandidateBudget = 48
	activeInitialCount    = 6
	activeChildren        = 2
	activeMaxPlanActions  = 40
)

type activeMode string

const (
	activeCurrentBaseline activeMode = "current-baseline"
	activeFacetOnly       activeMode = "facet-only"
)

type activeCandidate struct {
	Lineage       string
	ParentLineage string
	Depth         int
	Plan          plan.PlanSequence
}

type activeExecution struct {
	Candidate   activeCandidate
	Seed        int64
	Completion  experiment.Completion
	Record      executionrecord.CompletedExecutionRecordV1
	Evaluations []facet.EvaluationV1
}

type activeFakeExecutor struct {
	calls int
}

func (executor *activeFakeExecutor) Execute(_ context.Context, events []model.Event) ([]model.State, error) {
	executor.calls++
	states := make([]model.State, len(events)+1)
	for index := range states {
		payload := events[:min(index, len(events))]
		encoded, err := json.Marshal(payload)
		if err != nil {
			return nil, err
		}
		digest := sha256.Sum256(encoded)
		key := int64(binary.BigEndian.Uint64(digest[:8]) & uint64(^uint64(0)>>1))
		if key == 0 {
			key = int64(index + 1)
		}
		states[index] = model.State{Text: activeFakeStateText, Key: key}
	}
	return states, nil
}

const activeFakeStateText = `/\ currentActive = {1, 2, 3}
/\ state = <<"follower", "follower", "follower">>
/\ currentTerm = <<0, 0, 0>>
/\ log = <<<<>>, <<>>, <<>>>>
/\ commitIndex = <<0, 0, 0>>
/\ matchIndex = <<<<0, 0, 0>>, <<0, 0, 0>>, <<0, 0, 0>>>>
/\ votesResponded = <<{}, {}, {}>>
/\ votesGranted = <<{}, {}, {}>>
/\ votedFor = <<0, 0, 0>>
/\ nextIndex = <<<<1, 1, 1>>, <<1, 1, 1>>, <<1, 1, 1>>>>
/\ appliedIndex = <<0, 0, 0>>
/\ snapshotIndex = <<0, 0, 0>>
/\ snapshotTerm = <<0, 0, 0>>
/\ firstIndex = <<1, 1, 1>>
/\ pendingSnapshot = <<<<0, 0, 0>>, <<0, 0, 0>>, <<0, 0, 0>>>>`

func activeInitialPopulation(t *testing.T, campaignSeed int64) []activeCandidate {
	t.Helper()
	plans := make([]plan.PlanSequence, activeInitialCount)
	plans[0] = readActivePlan(t, "examples/plans/election.json")
	plans[1] = readActivePlan(t, "examples/plans/client-request-commit.json")
	plans[2] = readActivePlan(t, "examples/plans/follower-crash-restart.json")

	snapshotConfig := policy.SnapshotPartitionConfig{
		NodeIDs: []core.NodeID{1, 2, 3}, MaxValue: 5, MaxLogIndex: 10,
		SnapshotThreshold: 3, RetainEntries: 1,
	}
	plans[3] = recordActivePolicy(t, campaignSeed, 3, func(seed int64) (engine.ActionSource, error) {
		return policy.NewSnapshotPartition(seed, snapshotConfig)
	})
	failureConfig := snapshotConfig
	failureConfig.FailFirstSnapshot = true
	plans[4] = recordActivePolicy(t, campaignSeed, 4, func(seed int64) (engine.ActionSource, error) {
		return policy.NewSnapshotPartition(seed, failureConfig)
	})
	randomConfig := policy.DefaultRandomConfig()
	randomConfig.NodeIDs = []core.NodeID{1, 2, 3}
	randomConfig.MaxValue = 5
	randomConfig.MaxLogIndex = 10
	randomConfig.LargestTerm = 10
	plans[5] = recordActivePolicy(t, campaignSeed, 5, func(seed int64) (engine.ActionSource, error) {
		return policy.NewRandom(seed, randomConfig)
	})

	result := make([]activeCandidate, len(plans))
	for slot, sequence := range plans {
		if err := sequence.Validate(); err != nil {
			t.Fatalf("initial/%d: %v", slot, err)
		}
		if len(sequence.Actions) == 0 || len(sequence.Actions) > activeMaxPlanActions {
			t.Fatalf("initial/%d action count=%d outside 1..%d", slot, len(sequence.Actions), activeMaxPlanActions)
		}
		result[slot] = activeCandidate{
			Lineage: fmt.Sprintf("initial/%d", slot), Depth: 0, Plan: sequence.Copy(),
		}
	}
	return result
}

func recordActivePolicy(
	t *testing.T,
	campaignSeed int64,
	slot int,
	factory sourceFactory,
) plan.PlanSequence {
	t.Helper()
	seed := activeDerivedSeed(campaignSeed, fmt.Sprintf("initial/%d", slot), "record")
	source, err := factory(seed)
	if err != nil {
		t.Fatal(err)
	}
	recorded := &recordingSource{
		inner: source, source: fmt.Sprintf("stage6-initial-%d", slot), seed: seed,
	}
	scenario := pilotScenario{
		ID:   fmt.Sprintf("stage6-initial-%d-%s", slot, activeLineageToken(fmt.Sprintf("%d", campaignSeed))),
		Seed: seed, MaxPlanActions: activeMaxPlanActions, SnapshotThreshold: 3, RetainEntries: 1,
	}
	executor := &deterministicModelExecutor{}
	result, err := executeRealEtcdRaft(
		context.Background(), scenario, seed, recorded, executor,
	)
	if err != nil && result.Status != engine.StatusCompleted {
		t.Fatalf("record initial/%d: %v status=%s", slot, err, result.Status)
	}
	sequence := recorded.Sequence()
	if len(sequence.Actions) == 0 {
		t.Fatalf("record initial/%d produced empty Plan", slot)
	}
	return sequence
}

func runActiveCandidate(
	t *testing.T,
	campaignSeed int64,
	candidate activeCandidate,
	modelExecutor model.Executor,
) activeExecution {
	t.Helper()
	executionSeed := activeDerivedSeed(campaignSeed, candidate.Lineage, "execute")
	var completion experiment.Completion
	completions := 0
	runner, err := experiment.New(experiment.Config{
		Runs: 1, BaseSeed: executionSeed, Parallelism: 1, InitialPopulation: 1,
		MutationsPerNewState: 1, MaxMutationsPerCorpus: 1, MaxReadyCandidates: 1,
		MinNewModelStates: 1, SemanticCoverage: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	fingerprint := activeConfigurationFingerprint(t, campaignSeed)
	sourceName := "stage6_" + activeLineageToken(candidate.Lineage)
	report, _, err := runner.RunFeedback(
		context.Background(),
		experiment.FeedbackOptions{
			InitializerName: sourceName,
			Initializer: func(context.Context, int, int64) ([]plan.PlanSequence, error) {
				return []plan.PlanSequence{candidate.Plan.Copy()}, nil
			},
			Mutator:                  &noMutation{},
			ConfigurationFingerprint: fingerprint,
			CoverageProjector:        nil,
			Hooks: experiment.Hooks{OnRunComplete: func(got experiment.Completion) error {
				completion = got
				completions++
				return nil
			}},
		},
		func(ctx context.Context, _ int, seed int64, _ experiment.Candidate) (experiment.FeedbackExecution, error) {
			recorded := &recordingSource{
				inner:  &staticPlanSource{sequence: candidate.Plan.Copy()},
				source: sourceName, seed: seed,
			}
			scenario := pilotScenario{
				ID:   "active-" + activeLineageToken(candidate.Lineage),
				Seed: seed, MaxPlanActions: activeMaxPlanActions,
				SnapshotThreshold: 3, RetainEntries: 1,
			}
			result, executeErr := executeRealEtcdRaft(ctx, scenario, seed, recorded, modelExecutor)
			return experiment.FeedbackExecution{Result: result, Plan: recorded.Sequence()}, executeErr
		},
	)
	if err != nil {
		t.Fatalf("%s RunFeedback: %v", candidate.Lineage, err)
	}
	if report.CompletedRuns != 1 || report.InitialExecutions != 1 ||
		report.ExecutedMutations != 0 || completions != 1 {
		t.Fatalf("%s runner counts report=%+v completions=%d", candidate.Lineage, report, completions)
	}
	availability := executionrecord.FailureSignatureNotApplicable
	if completion.Execution.Result.Status != engine.StatusCompleted &&
		completion.Execution.Result.Status != engine.StatusCanceled {
		availability = executionrecord.FailureSignatureUnavailable
	}
	record, err := executionrecord.BuildV1(executionrecord.BuildInput{
		Completion: completion, ConfigurationFingerprint: fingerprint,
		FailureSignature: executionrecord.FailureSignatureInput{Availability: availability},
	})
	if err != nil {
		t.Fatalf("%s BuildV1: %v", candidate.Lineage, err)
	}
	initial := completion.Execution.Result.Initial.Copy()
	trace := completion.Execution.Result.Trace.Copy()
	evaluations, err := facet.EvaluateAll(facet.EvaluationInputV1{
		Record: record, InitialObservation: &initial, Trace: &trace,
		ModelEvents: copyActiveEvents(completion.Execution.Result.ModelEvents),
		ModelStates: append([]model.State(nil), completion.Execution.Result.ModelStates...),
	}, facetraft.CatalogV1())
	if err != nil {
		t.Fatalf("%s EvaluateAll: %v", candidate.Lineage, err)
	}
	return activeExecution{
		Candidate: candidate, Seed: executionSeed, Completion: completion,
		Record: record, Evaluations: evaluations,
	}
}

func readActivePlan(t *testing.T, relative string) plan.PlanSequence {
	t.Helper()
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate Stage 6 test file")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(currentFile), "../../../.."))
	file, err := os.Open(filepath.Join(root, relative))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	var sequence plan.PlanSequence
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&sequence); err != nil {
		t.Fatal(err)
	}
	return sequence
}

func activeDerivedSeed(campaignSeed int64, lineage, purpose string) int64 {
	payload := struct {
		Schema       string `json:"schema"`
		CampaignSeed int64  `json:"campaign_seed"`
		Lineage      string `json:"lineage"`
		Purpose      string `json:"purpose"`
	}{
		Schema: "stage6-lineage-seed-v1", CampaignSeed: campaignSeed,
		Lineage: lineage, Purpose: purpose,
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		panic(err)
	}
	digest := sha256.Sum256(encoded)
	value := int64(binary.BigEndian.Uint64(digest[:8]) & uint64(^uint64(0)>>1))
	if value == 0 {
		return 1
	}
	return value
}

func activeLineageToken(lineage string) string {
	digest := sha256.Sum256([]byte(lineage))
	return hex.EncodeToString(digest[:8])
}

func activeConfigurationFingerprint(t *testing.T, campaignSeed int64) string {
	t.Helper()
	payload := struct {
		Schema            string        `json:"schema"`
		CampaignSeed      int64         `json:"campaign_seed"`
		Nodes             []core.NodeID `json:"nodes"`
		Profile           string        `json:"profile"`
		MaxValue          int           `json:"max_value"`
		MaxLogIndex       uint64        `json:"max_log_index"`
		LargestTerm       uint64        `json:"largest_term"`
		SnapshotThreshold uint64        `json:"snapshot_threshold"`
		RetainEntries     uint64        `json:"retain_entries"`
		MaxPlanActions    int           `json:"max_plan_actions"`
	}{
		Schema: "stage6-active-ab-config-v1", CampaignSeed: campaignSeed,
		Nodes: []core.NodeID{1, 2, 3}, Profile: pilotProfile, MaxValue: 5,
		MaxLogIndex: 10, LargestTerm: 10, SnapshotThreshold: 3,
		RetainEntries: 1, MaxPlanActions: activeMaxPlanActions,
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}

func activePlanDigest(sequence plan.PlanSequence) string {
	encoded, err := json.Marshal(sequence)
	if err != nil {
		panic(err)
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}

func copyActiveCandidates(values []activeCandidate) []activeCandidate {
	result := make([]activeCandidate, len(values))
	for index, value := range values {
		result[index] = value
		result[index].Plan = value.Plan.Copy()
	}
	return result
}

func copyActiveEvents(events []model.Event) []model.Event {
	result := make([]model.Event, len(events))
	for index, event := range events {
		result[index] = event.Copy()
	}
	return result
}

func activeEvaluationIdentity(evaluations []facet.EvaluationV1) string {
	copy := make([]facet.EvaluationV1, len(evaluations))
	for index, evaluation := range evaluations {
		copy[index] = evaluation.Copy()
		for observationIndex := range copy[index].Observations {
			copy[index].Observations[observationIndex].Explanation = ""
		}
		copy[index].Detail = ""
	}
	sort.Slice(copy, func(i, j int) bool { return copy[i].FacetID < copy[j].FacetID })
	encoded, err := json.Marshal(copy)
	if err != nil {
		panic(err)
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}

func assertActivePlansEqual(t *testing.T, left, right plan.PlanSequence) {
	t.Helper()
	if !reflect.DeepEqual(left, right) {
		t.Fatalf("Plans differ:\nleft=%+v\nright=%+v", left, right)
	}
}
