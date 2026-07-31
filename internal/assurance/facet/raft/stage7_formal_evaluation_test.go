package raft_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/assurance/facetbreadth"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/engine"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/model"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/model/tlc"
)

type stage7PairSummary struct {
	Block                   stage7Block `json:"block"`
	Seed                    int64       `json:"seed"`
	BaselineExecuted        int         `json:"baseline_executed"`
	FacetExecuted           int         `json:"facet_executed"`
	BaselineUniqueTrace     int         `json:"baseline_unique_trace"`
	FacetUniqueTrace        int         `json:"facet_unique_trace"`
	BaselineTraceRatio      float64     `json:"baseline_unique_trace_ratio"`
	FacetTraceRatio         float64     `json:"facet_unique_trace_ratio"`
	BaselineQueueExhausted  bool        `json:"baseline_queue_exhausted"`
	FacetQueueExhausted     bool        `json:"facet_queue_exhausted"`
	BaselineExhaustion      int         `json:"baseline_exhaustion_ordinal"`
	FacetExhaustion         int         `json:"facet_exhaustion_ordinal"`
	BaselineRareReached     int         `json:"baseline_rare_reached"`
	FacetRareReached        int         `json:"facet_rare_reached"`
	BaselineRaw             int         `json:"baseline_raw_states"`
	FacetRaw                int         `json:"facet_raw_states"`
	BaselineSemanticStates  int         `json:"baseline_semantic_states"`
	FacetSemanticStates     int         `json:"facet_semantic_states"`
	BaselineTransitions     int         `json:"baseline_semantic_transitions"`
	FacetTransitions        int         `json:"facet_semantic_transitions"`
	OverlapLineages         int         `json:"overlap_lineages"`
	BaselineFailureDetected bool        `json:"baseline_failure_detected"`
	FacetFailureDetected    bool        `json:"facet_failure_detected"`
	BaselineFailureOrdinal  int         `json:"baseline_failure_ordinal"`
	FacetFailureOrdinal     int         `json:"facet_failure_ordinal"`
}

type stage7FormalSummary struct {
	Schema                   string                `json:"schema"`
	PreregistrationSHA       string                `json:"preregistration_sha256"`
	SeedListSHA              string                `json:"heldout_seed_list_sha256"`
	Bounds                   tlc.ServerBounds      `json:"tlc_bounds"`
	MetricsBefore            tlc.ServerMetrics     `json:"tlc_metrics_before"`
	MetricsAfter             tlc.ServerMetrics     `json:"tlc_metrics_after"`
	HistoricalStatus         string                `json:"historical_status"`
	HistoricalPairs          []stage7PairSummary   `json:"historical_pairs"`
	ClosedTreePairs          []stage7PairSummary   `json:"closed_tree_pairs"`
	NeutralReseedPairs       []stage7PairSummary   `json:"neutral_reseed_pairs"`
	MutantPairs              []stage7PairSummary   `json:"mutant_pairs"`
	ClosedConfirmedNegative  bool                  `json:"closed_tree_confirmed_negative"`
	NeutralPerformanceFutile bool                  `json:"neutral_performance_futile"`
	Replay                   stage7ReplaySummary   `json:"representative_replay"`
	Minimize                 stage7MinimizeSummary `json:"mutant_minimize"`
	FinalCeiling             string                `json:"final_conclusion_ceiling"`
}

func TestStage7ClosedTreeAndMutantRepeatabilityStrictTLC(t *testing.T) {
	address := os.Getenv("MODELFUZZ_STAGE7_TLC_URL")
	if address == "" {
		t.Skip("MODELFUZZ_STAGE7_TLC_URL is required")
	}
	client, err := tlc.NewClient(address)
	if err != nil {
		t.Fatal(err)
	}
	bounds, err := client.Bounds(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	assertActiveTLCBounds(t, bounds)
	factory := func() model.Executor { return client }
	seed := stage7HeldoutSeeds[0]

	closedInitial := activeInitialPopulation(t, seed)
	for _, mode := range []activeMode{activeCurrentBaseline, activeFacetOnly} {
		options := stage7CampaignOptions{
			Block: stage7ClosedTree, Mode: mode, Config: stage7DefaultExecutionConfig(seed),
			Initial: copyActiveCandidates(closedInitial), Budget: stage7ClosedBudget,
			Executor: factory,
		}
		first := runStage7Campaign(t, options)
		options.Initial = copyActiveCandidates(closedInitial)
		second := runStage7Campaign(t, options)
		assertStage7CampaignEqual(t, first, second)
	}

	mutantInitial := stage7MutantInitial(t, seed)
	for _, mode := range []activeMode{activeCurrentBaseline, activeFacetOnly} {
		options := stage7CampaignOptions{
			Block: stage7Mutant, Mode: mode, Config: stage7MutantExecutionConfig(seed),
			Initial: copyActiveCandidates(mutantInitial), Budget: stage7NeutralBudget,
			NeutralReseed: true, Executor: factory,
		}
		first := runStage7Campaign(t, options)
		options.Initial = copyActiveCandidates(mutantInitial)
		second := runStage7Campaign(t, options)
		assertStage7CampaignEqual(t, first, second)
	}
}

func runStage7FormalEvaluation(t *testing.T) {
	t.Helper()
	address := os.Getenv("MODELFUZZ_STAGE7_TLC_URL")
	resultsDir := os.Getenv("MODELFUZZ_STAGE7_RESULTS_DIR")
	assertStage7FrozenInputHashes(t, resultsDir)
	client, err := tlc.NewClient(address)
	if err != nil {
		t.Fatal(err)
	}
	bounds, err := client.Bounds(context.Background())
	if err != nil {
		t.Fatalf("strict TLC health: %v", err)
	}
	assertActiveTLCBounds(t, bounds)
	metricsBefore, err := client.Metrics(context.Background())
	if err != nil {
		t.Fatalf("strict TLC metrics before: %v", err)
	}
	summary := stage7FormalSummary{
		Schema:             "modelfuzz-ng-stage7-formal-summary-v1",
		PreregistrationSHA: stage7PreregistrationSHA, SeedListSHA: stage7SeedListSHA,
		Bounds: bounds, MetricsBefore: metricsBefore,
		HistoricalStatus: "HISTORICAL_CONFIGURATION_REPLICATION_ONLY",
		HistoricalPairs:  []stage7PairSummary{}, ClosedTreePairs: []stage7PairSummary{},
		NeutralReseedPairs: []stage7PairSummary{}, MutantPairs: []stage7PairSummary{},
		FinalCeiling: "PARTIAL",
	}

	for _, seed := range stage7HistoricalSeeds {
		initial := stage7HistoricalInitial(t, seed)
		config := stage7HistoricalExecutionConfig(seed)
		baseline := runStage7Campaign(t, stage7CampaignOptions{
			Block: stage7Historical, Mode: activeCurrentBaseline, Config: config,
			Initial: copyActiveCandidates(initial), Budget: stage7HistoricalBudget,
			Executor: func() model.Executor { return client }, ResultsDir: resultsDir,
		})
		facetOnly := runStage7Campaign(t, stage7CampaignOptions{
			Block: stage7Historical, Mode: activeFacetOnly, Config: config,
			Initial: copyActiveCandidates(initial), Budget: stage7HistoricalBudget,
			Executor: func() model.Executor { return client }, ResultsDir: resultsDir,
		})
		assertStage7CorrectCampaign(t, baseline)
		assertStage7CorrectCampaign(t, facetOnly)
		summary.HistoricalPairs = append(
			summary.HistoricalPairs, stage7Pair(t, baseline, facetOnly),
		)
		t.Logf("STAGE7_PROGRESS historical seed=%d complete", seed)
	}

	neutralCampaigns := make([]stage7CampaignResult, 0, len(stage7HeldoutSeeds)*2)
	for index, seed := range stage7HeldoutSeeds {
		initial := activeInitialPopulation(t, seed)
		config := stage7DefaultExecutionConfig(seed)
		closedBaseline := runStage7Campaign(t, stage7CampaignOptions{
			Block: stage7ClosedTree, Mode: activeCurrentBaseline, Config: config,
			Initial: copyActiveCandidates(initial), Budget: stage7ClosedBudget,
			Executor: func() model.Executor { return client }, ResultsDir: resultsDir,
		})
		closedFacet := runStage7Campaign(t, stage7CampaignOptions{
			Block: stage7ClosedTree, Mode: activeFacetOnly, Config: config,
			Initial: copyActiveCandidates(initial), Budget: stage7ClosedBudget,
			Executor: func() model.Executor { return client }, ResultsDir: resultsDir,
		})
		assertStage7CorrectCampaign(t, closedBaseline)
		assertStage7CorrectCampaign(t, closedFacet)
		summary.ClosedTreePairs = append(
			summary.ClosedTreePairs, stage7Pair(t, closedBaseline, closedFacet),
		)

		neutralBaseline := runStage7Campaign(t, stage7CampaignOptions{
			Block: stage7Neutral, Mode: activeCurrentBaseline, Config: config,
			Initial: copyActiveCandidates(initial), Budget: stage7NeutralBudget,
			NeutralReseed: true, Executor: func() model.Executor { return client },
			ResultsDir: resultsDir,
		})
		neutralFacet := runStage7Campaign(t, stage7CampaignOptions{
			Block: stage7Neutral, Mode: activeFacetOnly, Config: config,
			Initial: copyActiveCandidates(initial), Budget: stage7NeutralBudget,
			NeutralReseed: true, Executor: func() model.Executor { return client },
			ResultsDir: resultsDir,
		})
		assertStage7CorrectCampaign(t, neutralBaseline)
		assertStage7CorrectCampaign(t, neutralFacet)
		summary.NeutralReseedPairs = append(
			summary.NeutralReseedPairs, stage7Pair(t, neutralBaseline, neutralFacet),
		)
		if index == 0 {
			repeatedBaseline := runStage7Campaign(t, stage7CampaignOptions{
				Block: stage7Neutral, Mode: activeCurrentBaseline, Config: config,
				Initial: copyActiveCandidates(initial), Budget: stage7NeutralBudget,
				NeutralReseed: true, Executor: func() model.Executor { return client },
			})
			repeatedFacet := runStage7Campaign(t, stage7CampaignOptions{
				Block: stage7Neutral, Mode: activeFacetOnly, Config: config,
				Initial: copyActiveCandidates(initial), Budget: stage7NeutralBudget,
				NeutralReseed: true, Executor: func() model.Executor { return client },
			})
			assertStage7CampaignEqual(t, neutralBaseline, repeatedBaseline)
			assertStage7CampaignEqual(t, neutralFacet, repeatedFacet)
		}
		neutralCampaigns = append(
			neutralCampaigns,
			stage7KeepRepresentativeExecutions(t, neutralBaseline),
			stage7KeepRepresentativeExecutions(t, neutralFacet),
		)
		t.Logf("STAGE7_PROGRESS heldout seed_index=%d seed=%d complete", index, seed)
	}

	mutantCampaigns := make([]stage7CampaignResult, 0, 20)
	for index, seed := range stage7HeldoutSeeds[:10] {
		initial := stage7MutantInitial(t, seed)
		config := stage7MutantExecutionConfig(seed)
		baseline := runStage7Campaign(t, stage7CampaignOptions{
			Block: stage7Mutant, Mode: activeCurrentBaseline, Config: config,
			Initial: copyActiveCandidates(initial), Budget: stage7NeutralBudget,
			NeutralReseed: true, Executor: func() model.Executor { return client },
			ResultsDir: resultsDir,
		})
		facetOnly := runStage7Campaign(t, stage7CampaignOptions{
			Block: stage7Mutant, Mode: activeFacetOnly, Config: config,
			Initial: copyActiveCandidates(initial), Budget: stage7NeutralBudget,
			NeutralReseed: true, Executor: func() model.Executor { return client },
			ResultsDir: resultsDir,
		})
		assertStage7MutantCampaign(t, baseline)
		assertStage7MutantCampaign(t, facetOnly)
		summary.MutantPairs = append(summary.MutantPairs, stage7Pair(t, baseline, facetOnly))
		mutantCampaigns = append(
			mutantCampaigns,
			stage7KeepFailureExecution(t, baseline),
			stage7KeepFailureExecution(t, facetOnly),
		)
		t.Logf("STAGE7_PROGRESS mutant seed_index=%d seed=%d complete", index, seed)
	}

	summary.ClosedConfirmedNegative = stage7ClosedNegative(summary.ClosedTreePairs)
	summary.NeutralPerformanceFutile = stage7NeutralFutile(
		summary.NeutralReseedPairs, summary.MutantPairs,
	)
	summary.Replay = stage7ReplayRepresentatives(t, client, neutralCampaigns, resultsDir)
	summary.Minimize = stage7MinimizeMutants(t, client, mutantCampaigns, resultsDir)
	metricsAfter, err := client.Metrics(context.Background())
	if err != nil {
		t.Fatalf("strict TLC metrics after: %v", err)
	}
	summary.MetricsAfter = metricsAfter
	if metricsAfter.Requests <= metricsBefore.Requests {
		t.Fatalf("strict TLC request count did not increase: before=%d after=%d",
			metricsBefore.Requests, metricsAfter.Requests)
	}
	writeStage7JSONAtomic(t, filepath.Join(resultsDir, "formal-evaluation-summary.json"), summary)
	t.Logf("STAGE7_FORMAL_COMPLETE historical=%d closed=%d neutral=%d mutant=%d replay=%d minimize=%d",
		len(summary.HistoricalPairs), len(summary.ClosedTreePairs),
		len(summary.NeutralReseedPairs), len(summary.MutantPairs),
		len(summary.Replay.Items), len(summary.Minimize.Items))
}

func stage7KeepRepresentativeExecutions(
	t *testing.T,
	result stage7CampaignResult,
) stage7CampaignResult {
	t.Helper()
	keep := make(map[string]activeExecution)
	for _, covered := range result.facetSnapshot.Covered {
		execution, exists := result.executions[covered.Shortest.RecordDigest]
		if !exists {
			t.Fatalf("%d/%s missing retained representative %s",
				result.Seed, result.Mode, covered.Shortest.RecordDigest)
		}
		keep[covered.Shortest.RecordDigest] = execution
	}
	result.executions = keep
	result.LineageSequence = nil
	result.Facts = nil
	result.FinalQueue = nil
	result.InitialPlanDigests = nil
	result.ReseedPlanDigests = nil
	return result
}

func stage7KeepFailureExecution(
	t *testing.T,
	result stage7CampaignResult,
) stage7CampaignResult {
	t.Helper()
	keep := make(map[string]activeExecution)
	if result.Failure.Detected {
		execution, exists := result.executions[result.Failure.RecordDigest]
		if !exists {
			t.Fatalf("%d/%s missing retained failure %s",
				result.Seed, result.Mode, result.Failure.RecordDigest)
		}
		keep[result.Failure.RecordDigest] = execution
	}
	result.executions = keep
	result.LineageSequence = nil
	result.Facts = nil
	result.FinalQueue = nil
	result.InitialPlanDigests = nil
	result.ReseedPlanDigests = nil
	result.facetSnapshot = facetbreadth.CoverageSnapshotV1{}
	return result
}

func assertStage7FrozenInputHashes(t *testing.T, resultsDir string) {
	t.Helper()
	values := []struct {
		path string
		want string
	}{
		{filepath.Join(resultsDir, "..", "STAGE7_PREREGISTRATION.md"), stage7PreregistrationSHA},
		{filepath.Join(resultsDir, "heldout-seeds.json"), stage7SeedListSHA},
	}
	for _, value := range values {
		body, err := os.ReadFile(filepath.Clean(value.path))
		if err != nil {
			t.Fatal(err)
		}
		digest := sha256.Sum256(body)
		if got := hex.EncodeToString(digest[:]); got != value.want {
			t.Fatalf("frozen input %s SHA=%s want %s", value.path, got, value.want)
		}
	}
}

func assertStage7CorrectCampaign(t *testing.T, result stage7CampaignResult) {
	t.Helper()
	if result.Metrics.InvalidEvidence != 0 || result.Metrics.InsufficientEvidence != 0 {
		t.Fatalf("%d/%s invalid=%d insufficient=%d", result.Seed, result.Mode,
			result.Metrics.InvalidEvidence, result.Metrics.InsufficientEvidence)
	}
	for status, count := range result.Metrics.StatusCounts {
		if engine.Status(status) != engine.StatusCompleted && count != 0 {
			t.Fatalf("%d/%s unexpected correct status %s=%d", result.Seed, result.Mode, status, count)
		}
	}
	if len(result.Metrics.OracleFindingCodes) != 0 {
		t.Fatalf("%d/%s unexpected Oracle findings: %+v",
			result.Seed, result.Mode, result.Metrics.OracleFindingCodes)
	}
	if result.Metrics.ExecutedCandidates > result.CandidateBudget {
		t.Fatalf("%d/%s exceeded budget", result.Seed, result.Mode)
	}
}

func assertStage7MutantCampaign(t *testing.T, result stage7CampaignResult) {
	t.Helper()
	if result.Metrics.InvalidEvidence != 0 || result.Metrics.InsufficientEvidence != 0 {
		t.Fatalf("mutant %d/%s invalid=%d insufficient=%d", result.Seed, result.Mode,
			result.Metrics.InvalidEvidence, result.Metrics.InsufficientEvidence)
	}
	if result.Metrics.ExecutedCandidates != result.CandidateBudget {
		t.Fatalf("mutant %d/%s executed=%d want %d",
			result.Seed, result.Mode, result.Metrics.ExecutedCandidates, result.CandidateBudget)
	}
}

func stage7Pair(
	t *testing.T,
	baseline, facetOnly stage7CampaignResult,
) stage7PairSummary {
	t.Helper()
	if baseline.Seed != facetOnly.Seed ||
		baseline.Block != facetOnly.Block ||
		!reflect.DeepEqual(baseline.InitialPlanDigests, facetOnly.InitialPlanDigests) {
		t.Fatalf("pair identity mismatch baseline=%d/%s facet=%d/%s",
			baseline.Seed, baseline.Block, facetOnly.Seed, facetOnly.Block)
	}
	commonReseeds := len(baseline.ReseedPlanDigests)
	if len(facetOnly.ReseedPlanDigests) < commonReseeds {
		commonReseeds = len(facetOnly.ReseedPlanDigests)
	}
	for index := 0; index < commonReseeds; index++ {
		if baseline.ReseedPlanDigests[index] != facetOnly.ReseedPlanDigests[index] {
			t.Fatalf("%d/%s neutral reseed Plan %d differs",
				baseline.Seed, baseline.Block, index)
		}
	}
	return stage7PairSummary{
		Block: baseline.Block, Seed: baseline.Seed,
		BaselineExecuted:       baseline.Metrics.ExecutedCandidates,
		FacetExecuted:          facetOnly.Metrics.ExecutedCandidates,
		BaselineUniqueTrace:    baseline.Metrics.UniqueTraceDigests,
		FacetUniqueTrace:       facetOnly.Metrics.UniqueTraceDigests,
		BaselineTraceRatio:     stage7TraceRatio(baseline),
		FacetTraceRatio:        stage7TraceRatio(facetOnly),
		BaselineQueueExhausted: baseline.Metrics.QueueExhausted,
		FacetQueueExhausted:    facetOnly.Metrics.QueueExhausted,
		BaselineExhaustion:     baseline.QueueExhaustionOrdinal,
		FacetExhaustion:        facetOnly.QueueExhaustionOrdinal,
		BaselineRareReached:    stage7RareReached(baseline.Rare),
		FacetRareReached:       stage7RareReached(facetOnly.Rare),
		BaselineRaw:            baseline.Metrics.RawModelStates, FacetRaw: facetOnly.Metrics.RawModelStates,
		BaselineSemanticStates:  baseline.Metrics.SemanticStates,
		FacetSemanticStates:     facetOnly.Metrics.SemanticStates,
		BaselineTransitions:     baseline.Metrics.SemanticTransitions,
		FacetTransitions:        facetOnly.Metrics.SemanticTransitions,
		OverlapLineages:         assertStage7Overlap(t, baseline, facetOnly),
		BaselineFailureDetected: baseline.Failure.Detected,
		FacetFailureDetected:    facetOnly.Failure.Detected,
		BaselineFailureOrdinal:  baseline.Failure.CandidateOrdinal,
		FacetFailureOrdinal:     facetOnly.Failure.CandidateOrdinal,
	}
}

func stage7TraceRatio(result stage7CampaignResult) float64 {
	if result.Metrics.ExecutedCandidates == 0 {
		return 0
	}
	return float64(result.Metrics.UniqueTraceDigests) / float64(result.Metrics.ExecutedCandidates)
}

func stage7RareReached(values map[string]stage7RareResult) int {
	count := 0
	for _, classID := range stage7RareClasses {
		if values[classID].Reached {
			count++
		}
	}
	return count
}

func stage7ClosedNegative(pairs []stage7PairSummary) bool {
	earlier, ratios, rareNotHigher := 0, make([]float64, 0, len(pairs)), true
	for _, pair := range pairs {
		if pair.FacetExhaustion < pair.BaselineExhaustion {
			earlier++
		}
		if pair.BaselineTraceRatio > 0 {
			ratios = append(ratios, pair.FacetTraceRatio/pair.BaselineTraceRatio)
		}
		if pair.FacetRareReached > pair.BaselineRareReached {
			rareNotHigher = false
		}
	}
	sort.Float64s(ratios)
	median := 0.0
	if len(ratios) > 0 {
		median = ratios[len(ratios)/2]
	}
	return earlier >= 14 && median <= 0.85 && rareNotHigher
}

func stage7NeutralFutile(neutral, mutant []stage7PairSummary) bool {
	lowerTrace, rareNotHigher, lowerSemantic := 0, true, 0
	for _, pair := range neutral {
		if pair.FacetTraceRatio < pair.BaselineTraceRatio {
			lowerTrace++
		}
		if pair.FacetRareReached > pair.BaselineRareReached {
			rareNotHigher = false
		}
		baselineTotal := pair.BaselineRaw + pair.BaselineSemanticStates + pair.BaselineTransitions
		facetTotal := pair.FacetRaw + pair.FacetSemanticStates + pair.FacetTransitions
		if facetTotal < baselineTotal {
			lowerSemantic++
		}
	}
	mutantNotBetter := true
	for _, pair := range mutant {
		if pair.FacetFailureDetected && (!pair.BaselineFailureDetected ||
			pair.FacetFailureOrdinal < pair.BaselineFailureOrdinal) {
			mutantNotBetter = false
		}
	}
	return lowerTrace >= 14 && rareNotHigher &&
		lowerSemantic > len(neutral)/2 && mutantNotBetter
}
