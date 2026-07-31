package raft_test

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"sort"
	"testing"

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/assurance/facetbreadth"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/model"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/model/tlc"
)

func TestActiveABFairInitialPopulation(t *testing.T) {
	initial := activeInitialPopulation(t, 6601)
	baseline := copyActiveCandidates(initial)
	facetOnly := copyActiveCandidates(initial)
	if len(baseline) != activeInitialCount || len(facetOnly) != activeInitialCount {
		t.Fatalf("initial population sizes = %d/%d", len(baseline), len(facetOnly))
	}
	for index := range baseline {
		assertActivePlansEqual(t, baseline[index].Plan, facetOnly[index].Plan)
		if baseline[index].Lineage != facetOnly[index].Lineage {
			t.Fatalf("slot %d lineage differs", index)
		}
		if activePlanDigest(baseline[index].Plan) != activePlanDigest(facetOnly[index].Plan) {
			t.Fatalf("slot %d Plan digest differs", index)
		}
	}
	baseline[0].Plan.Actions[0].Node = 3
	if reflect.DeepEqual(baseline[0].Plan, facetOnly[0].Plan) {
		t.Fatal("initial population copies share Plan state")
	}
}

func TestActiveABMutationDeterminism(t *testing.T) {
	initial := activeInitialPopulation(t, 6601)
	execution := activeExecution{Candidate: initial[0], Seed: 1234}
	execution.Completion.Execution.Plan = initial[0].Plan.Copy()
	leftParent := initial[0]
	rightParent := initial[0]
	left := mutateActiveChildren(t, newActiveMutator(t), 6601, execution, leftParent)
	right := mutateActiveChildren(t, newActiveMutator(t), 6601, execution, rightParent)
	if !reflect.DeepEqual(left, right) {
		t.Fatalf("same lineage mutation differs:\nleft=%+v\nright=%+v", left, right)
	}
	assertActivePlansEqual(t, initial[0].Plan, leftParent.Plan)
	if len(left) != activeChildren ||
		left[0].Lineage != "initial/0/child-0" ||
		left[1].Lineage != "initial/0/child-1" {
		t.Fatalf("unexpected children: %+v", left)
	}
}

func TestActiveABRepeatabilityFakeExecutor(t *testing.T) {
	initial := activeInitialPopulation(t, 6601)
	factory := func() model.Executor { return &activeFakeExecutor{} }
	for _, mode := range []activeMode{activeCurrentBaseline, activeFacetOnly} {
		first := runActiveCampaign(t, mode, 6601, copyActiveCandidates(initial), 16, factory)
		second := runActiveCampaign(t, mode, 6601, copyActiveCandidates(initial), 16, factory)
		assertActiveCampaignEqual(t, first, second)
		if first.Metrics.ExecutedCandidates > 16 || first.Metrics.InitialCandidates != activeInitialCount {
			t.Fatalf("%s violated budget or initial count: %+v", mode, first.Metrics)
		}
		if first.Metrics.InvalidEvidence != 0 || first.Metrics.InsufficientEvidence != 0 {
			t.Fatalf("%s produced invalid/insufficient evidence", mode)
		}
	}
}

func TestActiveABFixedBudgetAndGuidance(t *testing.T) {
	initial := activeInitialPopulation(t, 6602)
	factory := func() model.Executor { return &activeFakeExecutor{} }
	baseline := runActiveCampaign(
		t, activeCurrentBaseline, 6602, copyActiveCandidates(initial), 20, factory,
	)
	facetOnly := runActiveCampaign(
		t, activeFacetOnly, 6602, copyActiveCandidates(initial), 20, factory,
	)
	for _, result := range []activeCampaignResult{baseline, facetOnly} {
		if result.Metrics.ExecutedCandidates > 20 {
			t.Fatalf("%s exceeded candidate budget: %d", result.Mode, result.Metrics.ExecutedCandidates)
		}
		for _, fact := range result.Facts {
			want := fact.BaselineRetained
			if result.Mode == activeFacetOnly {
				want = fact.FacetAdmitted
			}
			if fact.ActiveAdmitted != want {
				t.Fatalf("%s %s active admission=%t want %t", result.Mode, fact.Lineage, fact.ActiveAdmitted, want)
			}
			if fact.ActiveAdmitted && len(fact.Children) != activeChildren {
				t.Fatalf("%s %s generated %d children", result.Mode, fact.Lineage, len(fact.Children))
			}
			if !fact.ActiveAdmitted && len(fact.Children) != 0 {
				t.Fatalf("%s %s generated children without admission", result.Mode, fact.Lineage)
			}
		}
		if result.Metrics.GeneratedChildren != result.Metrics.AdmittedParents*activeChildren {
			t.Fatalf("%s fixed energy mismatch: %+v", result.Mode, result.Metrics)
		}
	}
	if overlap := assertActiveOverlap(t, baseline, facetOnly); overlap < activeInitialCount {
		t.Fatalf("overlap=%d, want at least initial population", overlap)
	}
}

func TestActiveFacetABSmokeStrictTLC(t *testing.T) {
	address := os.Getenv("MODELFUZZ_STAGE6_TLC_URL")
	if address == "" {
		t.Skip("MODELFUZZ_STAGE6_TLC_URL is not set")
	}
	client, err := tlc.NewClient(address)
	if err != nil {
		t.Fatal(err)
	}
	bounds, err := client.Bounds(context.Background())
	if err != nil {
		t.Fatalf("strict TLC health: %v", err)
	}
	assertActiveTLCBounds(t, bounds)

	seeds := []int64{6601, 6602, 6603}
	results := make([]activeCampaignResult, 0, len(seeds)*2)
	overlapBySeed := make(map[int64]int)
	exclusiveBySeed := make(map[int64]map[string]int)
	diverged := false
	for _, seed := range seeds {
		initial := activeInitialPopulation(t, seed)
		factory := func() model.Executor { return client }
		baseline := runActiveCampaign(
			t, activeCurrentBaseline, seed, copyActiveCandidates(initial),
			activeCandidateBudget, factory,
		)
		facetOnly := runActiveCampaign(
			t, activeFacetOnly, seed, copyActiveCandidates(initial),
			activeCandidateBudget, factory,
		)
		assertActiveStrictCampaign(t, baseline)
		assertActiveStrictCampaign(t, facetOnly)
		if !reflect.DeepEqual(baseline.InitialPlanDigest, facetOnly.InitialPlanDigest) {
			t.Fatalf("seed %d initial Plan digests differ", seed)
		}
		overlapBySeed[seed] = assertActiveOverlap(t, baseline, facetOnly)
		exclusiveBySeed[seed] = map[string]int{
			string(activeCurrentBaseline): activeExclusiveCount(baseline, facetOnly),
			string(activeFacetOnly):       activeExclusiveCount(facetOnly, baseline),
		}
		if exclusiveBySeed[seed][string(activeCurrentBaseline)] > 0 ||
			exclusiveBySeed[seed][string(activeFacetOnly)] > 0 {
			diverged = true
		}
		results = append(results, baseline, facetOnly)

		if seed == 6601 {
			repeatedBaseline := runActiveCampaign(
				t, activeCurrentBaseline, seed, copyActiveCandidates(initial),
				activeCandidateBudget, factory,
			)
			repeatedFacet := runActiveCampaign(
				t, activeFacetOnly, seed, copyActiveCandidates(initial),
				activeCandidateBudget, factory,
			)
			assertActiveCampaignEqual(t, baseline, repeatedBaseline)
			assertActiveCampaignEqual(t, facetOnly, repeatedFacet)
		}
	}
	if !diverged {
		t.Fatal("no campaign candidate stream diverged after the initial population")
	}
	if !activeHasNaturalNonNew(results...) {
		t.Fatal("FACET_DECISION_DEGENERATE: no no_novelty or shortest replacement occurred")
	}
	signal := activeSignal(results)
	reportResults := make([]activeCampaignReport, len(results))
	for index, result := range results {
		reportResults[index] = activeReportFromCampaign(result)
	}
	payload := struct {
		Schema          string                   `json:"schema"`
		Mechanism       string                   `json:"mechanism"`
		Signal          string                   `json:"signal"`
		Bounds          tlc.ServerBounds         `json:"tlc_bounds"`
		OverlapBySeed   map[int64]int            `json:"overlap_lineages_by_seed"`
		ExclusiveBySeed map[int64]map[string]int `json:"exclusive_lineages_by_seed"`
		Results         []activeCampaignReport   `json:"results"`
	}{
		Schema: "stage6-active-ab-smoke-result-v1", Mechanism: "GO", Signal: signal,
		Bounds: bounds, OverlapBySeed: overlapBySeed, ExclusiveBySeed: exclusiveBySeed,
		Results: reportResults,
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("STAGE6_FORMAL_RESULT=%s", encoded)
}

type activeCampaignReport struct {
	Seed               int64                 `json:"seed"`
	Mode               activeMode            `json:"mode"`
	CandidateBudget    int                   `json:"candidate_budget"`
	LineageSequence    []string              `json:"lineage_sequence"`
	InitialPlanDigests []string              `json:"initial_plan_digests"`
	Metrics            activeCampaignMetrics `json:"metrics"`
	FacetStateDigest   string                `json:"facet_state_digest"`
	CorpusDigest       string                `json:"corpus_digest"`
	FinalQueue         []string              `json:"final_queue"`
}

func activeReportFromCampaign(result activeCampaignResult) activeCampaignReport {
	return activeCampaignReport{
		Seed: result.Seed, Mode: result.Mode, CandidateBudget: result.CandidateBudget,
		LineageSequence:    append([]string(nil), result.LineageSequence...),
		InitialPlanDigests: append([]string(nil), result.InitialPlanDigest...),
		Metrics:            result.Metrics, FacetStateDigest: result.FacetStateDigest,
		CorpusDigest: result.CorpusDigest, FinalQueue: append([]string(nil), result.FinalQueue...),
	}
}

func assertActiveStrictCampaign(t *testing.T, result activeCampaignResult) {
	t.Helper()
	assertActiveNoHarnessFailures(t, result)
	if result.Metrics.ExecutedCandidates > activeCandidateBudget {
		t.Fatalf("%d/%s executed=%d exceeds budget %d",
			result.Seed, result.Mode, result.Metrics.ExecutedCandidates, activeCandidateBudget)
	}
	if result.Metrics.ExecutedCandidates < activeCandidateBudget &&
		!result.Metrics.QueueExhausted {
		t.Fatalf("%d/%s stopped at %d without queue exhaustion",
			result.Seed, result.Mode, result.Metrics.ExecutedCandidates)
	}
	if result.Metrics.MutationCandidates == 0 || result.Metrics.ExecutedChildren == 0 {
		t.Fatalf("%d/%s executed no mutation child", result.Seed, result.Mode)
	}
	if result.Metrics.AdmittedParents == 0 {
		t.Fatalf("%d/%s admitted no parent", result.Seed, result.Mode)
	}
	if result.Metrics.GeneratedChildren != result.Metrics.AdmittedParents*activeChildren {
		t.Fatalf("%d/%s fixed mutation count violated", result.Seed, result.Mode)
	}
	if result.Metrics.InitialCandidates != activeInitialCount {
		t.Fatalf("%d/%s initial candidates=%d", result.Seed, result.Mode, result.Metrics.InitialCandidates)
	}
}

func assertActiveTLCBounds(t *testing.T, bounds tlc.ServerBounds) {
	t.Helper()
	if bounds.MaxLogIndex != 10 || bounds.LargestTerm != 10 ||
		!reflect.DeepEqual(bounds.ServerIDs, []uint64{1, 2, 3}) ||
		bounds.MaxValue == nil || *bounds.MaxValue != 5 ||
		bounds.NilValue == nil || *bounds.NilValue != 0 ||
		bounds.ModelProfile != pilotProfile {
		t.Fatalf("strict TLC bounds mismatch: %+v", bounds)
	}
}

func TestActiveABResultCanonicalJSON(t *testing.T) {
	initial := activeInitialPopulation(t, 6603)
	result := runActiveCampaign(
		t, activeFacetOnly, 6603, initial, 12,
		func() model.Executor { return &activeFakeExecutor{} },
	)
	encoded := activeStableResultSummary([]activeCampaignResult{result})
	var decoded struct {
		Schema string `json:"schema"`
	}
	if err := json.Unmarshal([]byte(encoded), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Schema != "stage6-active-ab-smoke-result-v1" {
		t.Fatalf("unexpected result schema %q", decoded.Schema)
	}
	reasons := make([]string, 0, len(result.Metrics.FacetReasons))
	for reason := range result.Metrics.FacetReasons {
		reasons = append(reasons, reason)
	}
	sort.Strings(reasons)
	for _, reason := range reasons {
		switch facetbreadth.DecisionReasonV1(reason) {
		case facetbreadth.DecisionNewFacetClass,
			facetbreadth.DecisionShorterRepresentative,
			facetbreadth.DecisionNewAndShorter,
			facetbreadth.DecisionNoNovelty,
			facetbreadth.DecisionIneligibleEvidence:
		default:
			t.Fatalf("unknown decision reason %q", reason)
		}
	}
}
