package raft_test

import (
	"context"
	"fmt"
	"os"
	"reflect"
	"sort"
	"testing"

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/assurance/facet"
	facetraft "github.com/SuzumiyaHaruki/modelfuzz-ng/internal/assurance/facet/raft"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/assurance/facetbreadth"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/core"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/corpus"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/engine"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/minimize"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/model"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/mutation"
)

var stage7RareClasses = []string{
	"snapshot_fast_forwarded",
	"snapshot_rejected_or_stale",
	"snapshot_status_ignored",
}

type stage7RareResult struct {
	Reached      bool `json:"reached"`
	FirstOrdinal int  `json:"first_ordinal"`
}

type stage7FailureResult struct {
	Detected              bool               `json:"detected"`
	CandidateOrdinal      int                `json:"candidate_ordinal"`
	ConcreteActionOrdinal int                `json:"concrete_action_ordinal"`
	Lineage               string             `json:"lineage,omitempty"`
	PlanDigest            string             `json:"plan_digest,omitempty"`
	RecordDigest          string             `json:"record_digest,omitempty"`
	Status                engine.Status      `json:"status,omitempty"`
	Signature             minimize.Signature `json:"signature,omitempty"`
}

type stage7CampaignResult struct {
	Schema                 string                      `json:"schema"`
	PreregistrationSHA     string                      `json:"preregistration_sha256"`
	SeedListSHA            string                      `json:"heldout_seed_list_sha256"`
	Block                  stage7Block                 `json:"block"`
	Seed                   int64                       `json:"seed"`
	Mode                   activeMode                  `json:"mode"`
	CandidateBudget        int                         `json:"candidate_budget"`
	ConfigFingerprint      string                      `json:"config_fingerprint"`
	InitialPlanDigests     []string                    `json:"initial_plan_digests"`
	ReseedPlanDigests      []string                    `json:"reseed_plan_digests"`
	LineageSequence        []string                    `json:"lineage_sequence"`
	Facts                  []activeCandidateFact       `json:"candidate_facts"`
	Metrics                activeCampaignMetrics       `json:"metrics"`
	Rare                   map[string]stage7RareResult `json:"rare_snapshot_classes"`
	Failure                stage7FailureResult         `json:"first_failure"`
	FacetStateDigest       string                      `json:"facet_state_digest"`
	CorpusDigest           string                      `json:"corpus_digest"`
	FinalQueue             []string                    `json:"final_queue"`
	QueueExhaustionOrdinal int                         `json:"queue_exhaustion_ordinal"`

	executionConfig stage7ExecutionConfig
	executions      map[string]activeExecution
	facetSnapshot   facetbreadth.CoverageSnapshotV1
}

type stage7CampaignOptions struct {
	Block         stage7Block
	Mode          activeMode
	Config        stage7ExecutionConfig
	Initial       []activeCandidate
	Budget        int
	NeutralReseed bool
	Executor      activeExecutorFactory
	ResultsDir    string
}

func runStage7Campaign(t *testing.T, options stage7CampaignOptions) stage7CampaignResult {
	t.Helper()
	if options.Mode != activeCurrentBaseline && options.Mode != activeFacetOnly {
		t.Fatalf("unknown mode %q", options.Mode)
	}
	if options.Budget < len(options.Initial) || len(options.Initial) == 0 {
		t.Fatalf("invalid campaign shape initial=%d budget=%d", len(options.Initial), options.Budget)
	}
	catalog, err := facetbreadth.BuildCatalogIdentityV1(facetraft.CatalogV1())
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
	mutator := newStage7Mutator(t, options.Config.MaxPlanActions)
	queue := copyActiveCandidates(options.Initial)
	accumulator := newActiveAccumulator()
	result := stage7CampaignResult{
		Schema: stage7ResultsSchema, PreregistrationSHA: stage7PreregistrationSHA,
		SeedListSHA: stage7SeedListSHA, Block: options.Block,
		Seed: options.Config.CampaignSeed, Mode: options.Mode,
		CandidateBudget:    options.Budget,
		ConfigFingerprint:  stage7ConfigurationFingerprint(options.Config),
		InitialPlanDigests: make([]string, len(options.Initial)),
		ReseedPlanDigests:  []string{}, LineageSequence: make([]string, 0, options.Budget),
		Facts: make([]activeCandidateFact, 0, options.Budget),
		Rare:  make(map[string]stage7RareResult, len(stage7RareClasses)),
		Failure: stage7FailureResult{
			CandidateOrdinal:      stage7RightCensor,
			ConcreteActionOrdinal: stage7RightCensor,
		},
		QueueExhaustionOrdinal: options.Budget,
		executionConfig:        options.Config,
		executions:             make(map[string]activeExecution),
	}
	for index, candidate := range options.Initial {
		result.InitialPlanDigests[index] = activePlanDigest(candidate.Plan)
	}
	for _, classID := range stage7RareClasses {
		result.Rare[classID] = stage7RareResult{FirstOrdinal: options.Budget + 1}
	}

	reseedOrdinal := 0
	for len(result.Facts) < options.Budget {
		if len(queue) == 0 {
			if !options.NeutralReseed {
				result.QueueExhaustionOrdinal = len(result.Facts)
				break
			}
			reseed := stage7ReseedCandidate(t, options.Config, reseedOrdinal)
			reseedOrdinal++
			result.ReseedPlanDigests = append(result.ReseedPlanDigests, activePlanDigest(reseed.Plan))
			queue = append(queue, reseed)
		}
		candidate := queue[0]
		queue = queue[1:]
		ordinal := len(result.Facts)
		execution := runStage7Candidate(t, options.Config, candidate, options.Executor())
		projection, projectionErr := activeProjection(execution.Completion)
		if projectionErr != nil {
			t.Fatalf("%s semantic projection: %v", candidate.Lineage, projectionErr)
		}
		_, baselineRetained, baselineReason := activeBaselineConsider(
			t, collection, ordinal, execution, projection,
		)
		summary, err := facetbreadth.BuildCandidateSummaryV1(execution.Record, execution.Evaluations)
		if err != nil {
			t.Fatalf("%s BuildCandidateSummaryV1: %v", candidate.Lineage, err)
		}
		decision, err := breadth.Apply(uint64(ordinal), summary)
		if err != nil {
			t.Fatalf("%s facet Apply: %v", candidate.Lineage, err)
		}
		activeAdmitted := baselineRetained
		if options.Mode == activeFacetOnly {
			activeAdmitted = decision.Admitted
		}
		fact := activeFact(
			execution, projection, baselineRetained, baselineReason, decision, activeAdmitted,
		)
		if activeAdmitted {
			children := mutateStage7Children(
				t, mutator, options.Config, execution, candidate,
			)
			for _, child := range children {
				queue = append(queue, child)
				fact.Children = append(fact.Children, child.Lineage)
			}
		}
		result.LineageSequence = append(result.LineageSequence, candidate.Lineage)
		result.Facts = append(result.Facts, fact)
		accumulator.observe(
			execution, projection, baselineRetained, baselineReason, decision, activeAdmitted,
		)
		stage7ObserveRare(result.Rare, execution.Evaluations, ordinal)
		stage7ObserveFailure(&result.Failure, execution, ordinal)
		if previous, exists := result.executions[execution.Record.RecordDigest]; exists &&
			!stage7SemanticExecutionEqual(previous, execution) {
			t.Fatalf("record digest %s has conflicting execution", execution.Record.RecordDigest)
		}
		result.executions[execution.Record.RecordDigest] = execution
	}
	accumulator.metrics.QueueExhausted = len(queue) == 0 && len(result.Facts) < options.Budget
	accumulator.metrics.FinalQueueSize = len(queue)
	result.facetSnapshot = breadth.Snapshot()
	accumulator.finish(collection.Snapshot(), result.facetSnapshot, len(result.Facts))
	result.Metrics = accumulator.metrics
	result.FinalQueue = activeQueueLineages(queue)
	result.FacetStateDigest, err = breadth.Digest()
	if err != nil {
		t.Fatal(err)
	}
	result.CorpusDigest = activeDigest(collection.Snapshot())
	if options.NeutralReseed && len(result.Facts) != options.Budget {
		t.Fatalf("%d/%s neutral reseed stopped at %d/%d",
			options.Config.CampaignSeed, options.Mode, len(result.Facts), options.Budget)
	}
	if options.ResultsDir != "" {
		writeStage7JSONAtomic(
			t,
			stage7ResultPath(options.ResultsDir, options.Block, options.Config.CampaignSeed, options.Mode),
			result,
		)
	}
	return result
}

func newStage7Mutator(t *testing.T, maxActions int) *mutation.Random {
	t.Helper()
	value, err := mutation.NewRandom(mutation.RandomConfig{
		NodeIDs: []core.NodeID{1, 2, 3}, MaxValue: 5, MaxTicks: 5,
		MaxActions: maxActions, MaxCrashed: 1, LifecycleCooldown: 48,
		MaxCrashEpisodes: 4, CrashRestartPairPercent: 5, PartitionHealPairPercent: 5,
	})
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func mutateStage7Children(
	t *testing.T,
	mutator *mutation.Random,
	config stage7ExecutionConfig,
	execution activeExecution,
	parent activeCandidate,
) []activeCandidate {
	t.Helper()
	children := make([]activeCandidate, 0, activeChildren)
	for slot := 0; slot < activeChildren; slot++ {
		purpose := fmt.Sprintf("child-%d", slot)
		seed := activeDerivedSeed(config.CampaignSeed, parent.Lineage, purpose)
		sequences, err := mutator.Mutate(context.Background(), mutation.Request{
			Entry: corpus.Entry{
				ID: parent.Lineage, ParentID: parent.ParentLineage,
				Source: "stage7_mutation_parent", Depth: parent.Depth,
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
		if len(sequences[0].Actions) > config.MaxPlanActions {
			t.Fatalf("%s %s child actions=%d", parent.Lineage, purpose, len(sequences[0].Actions))
		}
		children = append(children, activeCandidate{
			Lineage: parent.Lineage + "/" + purpose, ParentLineage: parent.Lineage,
			Depth: parent.Depth + 1, Plan: sequences[0].Copy(),
		})
	}
	return children
}

func stage7ObserveRare(
	values map[string]stage7RareResult,
	evaluations []facet.EvaluationV1,
	ordinal int,
) {
	for _, evaluation := range evaluations {
		for _, observation := range evaluation.Observations {
			current, tracked := values[observation.Key.ClassID]
			if tracked && !current.Reached {
				values[observation.Key.ClassID] = stage7RareResult{
					Reached: true, FirstOrdinal: ordinal,
				}
			}
		}
	}
}

func stage7ObserveFailure(
	current *stage7FailureResult,
	execution activeExecution,
	ordinal int,
) {
	if current.Detected {
		return
	}
	signature, failed := minimize.SignatureOf(execution.Completion.Execution.Result)
	if !failed {
		return
	}
	actions := len(execution.Completion.Execution.Result.Actions.Actions)
	current.Detected = true
	current.CandidateOrdinal = ordinal
	current.ConcreteActionOrdinal = actions
	current.Lineage = execution.Candidate.Lineage
	current.PlanDigest = execution.Completion.Run.PlanDigest
	current.RecordDigest = execution.Record.RecordDigest
	current.Status = execution.Completion.Execution.Result.Status
	current.Signature = signature
}

func stage7AsActive(result stage7CampaignResult) activeCampaignResult {
	return activeCampaignResult{
		Seed: result.Seed, Mode: result.Mode, CandidateBudget: result.CandidateBudget,
		LineageSequence:   append([]string(nil), result.LineageSequence...),
		InitialPlanDigest: append([]string(nil), result.InitialPlanDigests...),
		Facts:             append([]activeCandidateFact(nil), result.Facts...),
		Metrics:           result.Metrics, FacetStateDigest: result.FacetStateDigest,
		CorpusDigest: result.CorpusDigest, FinalQueue: append([]string(nil), result.FinalQueue...),
	}
}

func assertStage7CampaignEqual(t *testing.T, left, right stage7CampaignResult) {
	t.Helper()
	left.executions, right.executions = nil, nil
	left.facetSnapshot, right.facetSnapshot = facetbreadth.CoverageSnapshotV1{}, facetbreadth.CoverageSnapshotV1{}
	left.executionConfig, right.executionConfig = stage7ExecutionConfig{}, stage7ExecutionConfig{}
	if !reflect.DeepEqual(left, right) {
		t.Fatalf("Stage 7 campaign repeat mismatch for %d/%s/%s", left.Seed, left.Block, left.Mode)
	}
}

func assertStage7Overlap(t *testing.T, left, right stage7CampaignResult) int {
	t.Helper()
	return assertActiveOverlap(t, stage7AsActive(left), stage7AsActive(right))
}

func stage7HistoricalInitial(t *testing.T, seed int64) []activeCandidate {
	t.Helper()
	return stage7RandomPopulation(
		t, stage7HistoricalExecutionConfig(seed), 4, "historical-initial",
	)
}

func stage7MutantInitial(t *testing.T, seed int64) []activeCandidate {
	t.Helper()
	// Initial Plan generation is neutral: the frozen mutant is applied only
	// when the paired campaigns execute these shared Plans.
	return stage7RandomPopulation(t, stage7DefaultExecutionConfig(seed), 6, "initial")
}

func TestStage7SeedGenerationDeterministic(t *testing.T) {
	if len(stage7HeldoutSeeds) != 20 {
		t.Fatalf("held-out seed count=%d", len(stage7HeldoutSeeds))
	}
	seen := make(map[int64]struct{}, len(stage7HeldoutSeeds))
	for index, want := range stage7HeldoutSeeds {
		if got := stage7HeldoutSeed(index); got != want {
			t.Fatalf("held-out[%d]=%d want %d", index, got, want)
		}
		if want <= 0 {
			t.Fatalf("held-out[%d] is not positive", index)
		}
		if _, duplicate := seen[want]; duplicate {
			t.Fatalf("duplicate held-out seed %d", want)
		}
		seen[want] = struct{}{}
	}
	for _, seed := range append(append([]int64{}, stage7HistoricalSeeds...), 6601, 6602, 6603) {
		if _, overlap := seen[seed]; overlap {
			t.Fatalf("held-out seed overlaps %d", seed)
		}
	}
}

func TestStage7HistoricalAuditFixture(t *testing.T) {
	if len(stage7HistoricalSeeds) != 10 {
		t.Fatalf("historical seed count=%d", len(stage7HistoricalSeeds))
	}
	if !sort.SliceIsSorted(stage7HistoricalSeeds, func(i, j int) bool {
		return stage7HistoricalSeeds[i] < stage7HistoricalSeeds[j]
	}) {
		t.Fatal("historical seeds are not sorted")
	}
	config := stage7HistoricalExecutionConfig(stage7HistoricalSeeds[0])
	if config.MaxPlanActions != 80 || config.SnapshotThreshold != 2 ||
		config.RetainEntries != 1 || config.SnapshotStatusMap != "correct" {
		t.Fatalf("historical config=%+v", config)
	}
}

func TestStage7NeutralReseedFairness(t *testing.T) {
	config := stage7DefaultExecutionConfig(stage7HeldoutSeeds[0])
	left := stage7ReseedCandidate(t, config, 0)
	right := stage7ReseedCandidate(t, config, 0)
	if !reflect.DeepEqual(left, right) || activePlanDigest(left.Plan) != activePlanDigest(right.Plan) {
		t.Fatal("same neutral reseed ordinal produced different Plan")
	}
	different := stage7ReseedCandidate(t, config, 1)
	if left.Lineage == different.Lineage {
		t.Fatal("different reseed ordinals share lineage")
	}
}

func TestStage7MutantInitialPopulationIsNeutralAndDeterministic(t *testing.T) {
	seed := stage7HeldoutSeeds[0]
	left := stage7MutantInitial(t, seed)
	right := stage7MutantInitial(t, seed)
	if !reflect.DeepEqual(left, right) || len(left) != 6 {
		t.Fatal("mutant initial population is not deterministic")
	}
	for index := range left {
		if activePlanDigest(left[index].Plan) != activePlanDigest(right[index].Plan) {
			t.Fatalf("mutant initial slot %d digest differs", index)
		}
	}
	mutantConfig := stage7MutantExecutionConfig(seed)
	leftReseed := stage7ReseedCandidate(t, mutantConfig, 7)
	rightReseed := stage7ReseedCandidate(t, mutantConfig, 7)
	if !reflect.DeepEqual(leftReseed, rightReseed) ||
		activePlanDigest(leftReseed.Plan) != activePlanDigest(rightReseed.Plan) {
		t.Fatal("mutant neutral reseed is not deterministic")
	}
}

func TestStage7NeutralReseedCampaignFast(t *testing.T) {
	seed := stage7HeldoutSeeds[0]
	initial := activeInitialPopulation(t, seed)
	factory := func() model.Executor { return &activeFakeExecutor{} }
	left := runStage7Campaign(t, stage7CampaignOptions{
		Block: stage7Neutral, Mode: activeCurrentBaseline,
		Config: stage7DefaultExecutionConfig(seed), Initial: copyActiveCandidates(initial),
		Budget: 12, NeutralReseed: true, Executor: factory,
	})
	right := runStage7Campaign(t, stage7CampaignOptions{
		Block: stage7Neutral, Mode: activeCurrentBaseline,
		Config: stage7DefaultExecutionConfig(seed), Initial: copyActiveCandidates(initial),
		Budget: 12, NeutralReseed: true, Executor: factory,
	})
	assertStage7CampaignEqual(t, left, right)
	if left.Metrics.ExecutedCandidates != 12 || left.Metrics.InvalidEvidence != 0 ||
		left.Metrics.InsufficientEvidence != 0 {
		t.Fatalf("fast campaign metrics=%+v", left.Metrics)
	}
}

func TestStage7FormalEvaluationStrictTLC(t *testing.T) {
	if os.Getenv("MODELFUZZ_STAGE7_TLC_URL") == "" ||
		os.Getenv("MODELFUZZ_STAGE7_RESULTS_DIR") == "" {
		t.Skip("MODELFUZZ_STAGE7_TLC_URL and MODELFUZZ_STAGE7_RESULTS_DIR are required")
	}
	runStage7FormalEvaluation(t)
}
