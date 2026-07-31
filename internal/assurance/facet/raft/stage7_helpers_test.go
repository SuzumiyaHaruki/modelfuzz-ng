package raft_test

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"reflect"
	"sort"
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
	oracleraft "github.com/SuzumiyaHaruki/modelfuzz-ng/internal/oracle/raft"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/plan"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/policy"
	runtimepkg "github.com/SuzumiyaHaruki/modelfuzz-ng/internal/runtime"
	raftlib "go.etcd.io/raft/v3"
)

const (
	stage7PreregistrationSHA = "62a386a2017e4f5bf7e8164e0478b73d7a2506914db9ffeb1701a4be1ffdbf9c"
	stage7SeedListSHA        = "00da18df2172eada4938315b3db2e1abf3b7413ba6a58a1a6c03d2d5cc4be6f6"
	stage7ResultsSchema      = "modelfuzz-ng-stage7-campaign-result-v1"
	stage7ClosedBudget       = 64
	stage7NeutralBudget      = 128
	stage7HistoricalBudget   = 60
	stage7HistoricalActions  = 80
	stage7RightCensor        = 129
)

var stage7HistoricalSeeds = []int64{
	720001, 720101, 720201, 720301, 720401,
	720501, 720601, 720701, 720801, 720901,
}

var stage7HeldoutSeeds = []int64{
	8336454817672404382,
	1032517847817231170,
	1286863238667992368,
	4120408576676485977,
	1730231285957079546,
	7480122269411795644,
	1084788547682977977,
	9054400645830646887,
	418473828227667117,
	6566132269545533901,
	4936707319672310945,
	825097919939804635,
	8787426522159646979,
	4384378045862814132,
	3750190362025633128,
	3961371642060324897,
	5552216599351421507,
	7687411833197131243,
	7192736814863423931,
	1235846154754625999,
}

type stage7Block string

const (
	stage7Historical stage7Block = "historical-configuration-replication"
	stage7ClosedTree stage7Block = "heldout-closed-tree"
	stage7Neutral    stage7Block = "heldout-neutral-reseed"
	stage7Mutant     stage7Block = "mutant-snapshot-status-invert"
)

type stage7ExecutionConfig struct {
	CampaignSeed      int64
	MaxPlanActions    int
	SnapshotThreshold uint64
	RetainEntries     uint64
	SnapshotStatusMap string
}

func stage7DefaultExecutionConfig(seed int64) stage7ExecutionConfig {
	return stage7ExecutionConfig{
		CampaignSeed: seed, MaxPlanActions: activeMaxPlanActions,
		SnapshotThreshold: 3, RetainEntries: 1,
		SnapshotStatusMap: etcdraft.SnapshotStatusMappingCorrect,
	}
}

func stage7HistoricalExecutionConfig(seed int64) stage7ExecutionConfig {
	return stage7ExecutionConfig{
		CampaignSeed: seed, MaxPlanActions: stage7HistoricalActions,
		SnapshotThreshold: 2, RetainEntries: 1,
		SnapshotStatusMap: etcdraft.SnapshotStatusMappingCorrect,
	}
}

func stage7MutantExecutionConfig(seed int64) stage7ExecutionConfig {
	config := stage7DefaultExecutionConfig(seed)
	config.SnapshotStatusMap = etcdraft.SnapshotStatusMappingInvert
	return config
}

func stage7HeldoutSeed(index int) int64 {
	payload := fmt.Sprintf("modelfuzz-ng-facet-v1-heldout-20260730:%d", index)
	digest := sha256.Sum256([]byte(payload))
	value := int64(binary.BigEndian.Uint64(digest[:8]) & uint64(^uint64(0)>>1))
	return value
}

func stage7ConfigurationFingerprint(config stage7ExecutionConfig) string {
	payload := struct {
		Schema            string `json:"schema"`
		CampaignSeed      int64  `json:"campaign_seed"`
		Nodes             []int  `json:"nodes"`
		Profile           string `json:"profile"`
		MaxValue          int    `json:"max_value"`
		MaxLogIndex       uint64 `json:"max_log_index"`
		LargestTerm       uint64 `json:"largest_term"`
		SnapshotThreshold uint64 `json:"snapshot_threshold"`
		RetainEntries     uint64 `json:"retain_entries"`
		MaxPlanActions    int    `json:"max_plan_actions"`
		SnapshotStatusMap string `json:"snapshot_status_mapping"`
	}{
		Schema: "stage7-execution-config-v1", CampaignSeed: config.CampaignSeed,
		Nodes: []int{1, 2, 3}, Profile: pilotProfile, MaxValue: pilotMaxValue,
		MaxLogIndex: pilotMaxLogIndex, LargestTerm: pilotLargestTerm,
		SnapshotThreshold: config.SnapshotThreshold, RetainEntries: config.RetainEntries,
		MaxPlanActions: config.MaxPlanActions, SnapshotStatusMap: config.SnapshotStatusMap,
	}
	return activeDigest(payload)
}

func stage7ExecuteRealEtcdRaft(
	ctx context.Context,
	config stage7ExecutionConfig,
	candidate activeCandidate,
	seed int64,
	source engine.ActionSource,
	modelExecutor model.Executor,
) (engine.Result, error) {
	runtime, err := stage7NewRuntime(config, candidate, seed)
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
		engine.Config{MaxPlanActions: config.MaxPlanActions, MaxConsecutiveNoops: 32},
		oracleraft.New(),
	)
	if err != nil {
		return engine.Result{}, err
	}
	return runner.RunSource(ctx, source, config.MaxPlanActions)
}

func stage7NewRuntime(
	config stage7ExecutionConfig,
	candidate activeCandidate,
	seed int64,
) (*runtimepkg.Runtime, error) {
	adapterConfig := etcdraft.DefaultConfig()
	adapterConfig.Snapshot = etcdraft.SnapshotPolicy{
		Threshold: config.SnapshotThreshold, RetainEntries: config.RetainEntries,
	}
	adapterConfig.Faults.SnapshotStatusMap = config.SnapshotStatusMap
	adapterConfig.Logger = &raftlib.DefaultLogger{Logger: log.New(io.Discard, "", 0)}
	adapter, err := etcdraft.New(adapterConfig)
	if err != nil {
		return nil, err
	}
	runtime, err := runtimepkg.New(adapter, runtimepkg.Config{
		ExecutionID: core.ExecutionID("stage7-" + activeLineageToken(candidate.Lineage)),
		Seed:        seed,
		Limits: runtimepkg.Limits{
			MaxActions:        uint64(config.MaxPlanActions * 8),
			MaxTicks:          512,
			MaxEffects:        50000,
			MaxQueuedMessages: 10000,
		},
	})
	if err != nil {
		return nil, err
	}
	return runtime, nil
}

func runStage7Candidate(
	t *testing.T,
	config stage7ExecutionConfig,
	candidate activeCandidate,
	modelExecutor model.Executor,
) activeExecution {
	t.Helper()
	executionSeed := activeDerivedSeed(config.CampaignSeed, candidate.Lineage, "execute")
	var completion experiment.Completion
	completionCount := 0
	runner, err := experiment.New(experiment.Config{
		Runs: 1, BaseSeed: executionSeed, Parallelism: 1, InitialPopulation: 1,
		MutationsPerNewState: 1, MaxMutationsPerCorpus: 1, MaxReadyCandidates: 1,
		MinNewModelStates: 1, SemanticCoverage: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	fingerprint := stage7ConfigurationFingerprint(config)
	sourceName := "stage7_" + activeLineageToken(candidate.Lineage)
	report, _, err := runner.RunFeedback(
		context.Background(),
		experiment.FeedbackOptions{
			InitializerName: sourceName,
			Initializer: func(context.Context, int, int64) ([]plan.PlanSequence, error) {
				return []plan.PlanSequence{candidate.Plan.Copy()}, nil
			},
			Mutator:                  &noMutation{},
			ConfigurationFingerprint: fingerprint,
			Hooks: experiment.Hooks{OnRunComplete: func(got experiment.Completion) error {
				completion = got
				completionCount++
				return nil
			}},
		},
		func(ctx context.Context, _ int, seed int64, _ experiment.Candidate) (experiment.FeedbackExecution, error) {
			recorded := &recordingSource{
				inner: &staticPlanSource{sequence: candidate.Plan.Copy()}, source: sourceName, seed: seed,
			}
			result, executeErr := stage7ExecuteRealEtcdRaft(
				ctx, config, candidate, seed, recorded, modelExecutor,
			)
			return experiment.FeedbackExecution{Result: result, Plan: recorded.Sequence()}, executeErr
		},
	)
	if err != nil {
		t.Fatalf("%s RunFeedback: %v", candidate.Lineage, err)
	}
	if report.CompletedRuns != 1 || report.InitialExecutions != 1 ||
		report.ExecutedMutations != 0 || completionCount != 1 {
		t.Fatalf("%s runner counts report=%+v completions=%d", candidate.Lineage, report, completionCount)
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

func stage7RecordRandomPlan(
	t *testing.T,
	config stage7ExecutionConfig,
	lineage string,
	purpose string,
) plan.PlanSequence {
	t.Helper()
	seed := activeDerivedSeed(config.CampaignSeed, lineage, purpose)
	randomConfig := policy.DefaultRandomConfig()
	randomConfig.NodeIDs = []core.NodeID{1, 2, 3}
	randomConfig.MaxValue = pilotMaxValue
	randomConfig.MaxLogIndex = pilotMaxLogIndex
	randomConfig.LargestTerm = pilotLargestTerm
	source, err := policy.NewRandom(seed, randomConfig)
	if err != nil {
		t.Fatal(err)
	}
	recorded := &recordingSource{inner: source, source: lineage, seed: seed}
	candidate := activeCandidate{Lineage: "record/" + activeLineageToken(lineage)}
	recordingConfig := config
	recordingConfig.SnapshotStatusMap = "correct"
	result, executeErr := stage7ExecuteRealEtcdRaft(
		context.Background(), recordingConfig, candidate, seed, recorded, &deterministicModelExecutor{},
	)
	if executeErr != nil && result.Status != engine.StatusCompleted {
		t.Fatalf("record %s: %v status=%s", lineage, executeErr, result.Status)
	}
	sequence := recorded.Sequence()
	if err := sequence.Validate(); err != nil {
		t.Fatalf("record %s invalid Plan: %v", lineage, err)
	}
	if len(sequence.Actions) == 0 || len(sequence.Actions) > config.MaxPlanActions {
		t.Fatalf("record %s actions=%d outside 1..%d", lineage, len(sequence.Actions), config.MaxPlanActions)
	}
	return sequence
}

func stage7RandomPopulation(
	t *testing.T,
	config stage7ExecutionConfig,
	count int,
	prefix string,
) []activeCandidate {
	t.Helper()
	result := make([]activeCandidate, count)
	for slot := 0; slot < count; slot++ {
		lineage := fmt.Sprintf("%s/%d", prefix, slot)
		result[slot] = activeCandidate{
			Lineage: lineage,
			Plan:    stage7RecordRandomPlan(t, config, lineage, "record"),
		}
	}
	return result
}

func stage7ReseedCandidate(
	t *testing.T,
	config stage7ExecutionConfig,
	ordinal int,
) activeCandidate {
	t.Helper()
	lineage := fmt.Sprintf("reseed/%d", ordinal)
	return activeCandidate{
		Lineage: lineage,
		Plan:    stage7RecordRandomPlan(t, config, lineage, "record"),
	}
}

func stage7ResultPath(directory string, block stage7Block, seed int64, mode activeMode) string {
	return filepath.Join(directory, fmt.Sprintf("%s-%d-%s.json", block, seed, mode))
}

func writeStage7JSONAtomic(t *testing.T, path string, value any) {
	t.Helper()
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	temp, err := os.CreateTemp(directory, ".stage7-*.tmp")
	if err != nil {
		t.Fatal(err)
	}
	tempName := temp.Name()
	remove := true
	defer func() {
		if remove {
			_ = os.Remove(tempName)
		}
	}()
	encoder := json.NewEncoder(temp)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(value); err != nil {
		_ = temp.Close()
		t.Fatal(err)
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		t.Fatal(err)
	}
	if err := temp.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(tempName, path); err != nil {
		t.Fatal(err)
	}
	remove = false
}

func stage7SortedKeys(values map[string]uint64) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func stage7SemanticExecutionEqual(left, right activeExecution) bool {
	leftResult, rightResult := left.Completion.Execution.Result, right.Completion.Execution.Result
	leftResult.Error, rightResult.Error = "", ""
	leftResult.TerminationDetail, rightResult.TerminationDetail = "", ""
	leftRun, rightRun := left.Completion.Run, right.Completion.Run
	leftRun.Error, rightRun.Error = "", ""
	leftRun.DurationMillis, rightRun.DurationMillis = 0, 0
	leftRun.DurationMicros, rightRun.DurationMicros = 0, 0
	return reflect.DeepEqual(left.Candidate, right.Candidate) &&
		left.Seed == right.Seed &&
		reflect.DeepEqual(leftResult, rightResult) &&
		reflect.DeepEqual(leftRun, rightRun) &&
		reflect.DeepEqual(left.Record, right.Record) &&
		activeEvaluationIdentity(left.Evaluations) == activeEvaluationIdentity(right.Evaluations)
}
