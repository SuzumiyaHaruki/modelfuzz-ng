package main

import (
	"strings"
	"testing"

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/breadthdepth"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/goalsearch"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/persistence"
)

func validBreadthDepthManifest() breadthDepthManifest {
	return breadthDepthManifest{
		Schema: breadthDepthBenchmarkSchema, Name: "test", Phase: "pilot",
		Config: "config.json", TLCAddress: "http://127.0.0.1:2027",
		Methods: []breadthdepth.Method{breadthdepth.MethodFacetThen},
		Goals:   []goalsearch.GoalID{goalsearch.GoalSnapshotCatchUpAfterPartition},
		Seeds:   []int64{1}, TotalCandidates: 90, GlobalCandidates: 60,
		LocalCandidates: 30, TotalActions: 16200, GlobalActions: 10800,
		LocalActions: 5400, MaxPlanActions: 180, HandoffTopK: 4,
		HandoffDiversity: true, HandoffFallback: true, FixedEnergy: 2,
		CorpusLimit: 128, InitialPopulation: 4, MaxReadyCandidates: 256,
		SnapshotThreshold: 3, RetainEntries: 1, LocalFrontierCapacity: 1,
		SaveAllRuns: true, ReplayVerify: true, StopOnTarget: true,
	}
}

func TestCompletedPrunedLocalPhaseRequiresVerifiedRetentionMarker(t *testing.T) {
	directory := t.TempDir()
	summary := breadthdepth.CombinedSummary{
		SchemaVersion: breadthdepth.SchemaVersion,
		StableKey:     strings.Repeat("a", 64),
		Local: &breadthdepth.LocalPhaseResult{
			SchemaVersion: breadthdepth.SchemaVersion,
			Candidates:    30,
			StableKey:     strings.Repeat("b", 64),
		},
	}
	retention := breadthDepthArtifactRetention{
		SchemaVersion: breadthDepthArtifactRetentionSchema,
		RawPruned:     true, PrunedPaths: []string{"local"},
		ArchivePath:       "archives/formal-local.tar.zst",
		ArchiveSHA256:     strings.Repeat("c", 64),
		ArchiveValidation: "zstd+tar+sha256-passed",
		CombinedStableKey: summary.StableKey,
		LocalStableKey:    summary.Local.StableKey,
		LocalCandidates:   30, TLCExecutedRuns: 30,
		RuntimeStatuses:   map[string]int{string("completed"): 30},
		FinalReportSHA256: strings.Repeat("d", 64),
	}
	if err := persistence.WriteJSONAtomic(
		directory+"/artifact-retention.json", retention); err != nil {
		t.Fatalf("write retention: %v", err)
	}
	if !completedPrunedLocalPhase(directory, summary) {
		t.Fatal("verified retention marker was rejected")
	}
	retention.TLCExecutedRuns = 29
	if err := persistence.WriteJSONAtomic(
		directory+"/artifact-retention.json", retention); err != nil {
		t.Fatalf("rewrite retention: %v", err)
	}
	if completedPrunedLocalPhase(directory, summary) {
		t.Fatal("incomplete strict TLC execution was accepted")
	}
}

func TestBreadthDepthManifestValidatesExactBudgetsAndExplicitFalse(t *testing.T) {
	manifest := validBreadthDepthManifest()
	if err := validateBreadthDepthManifest(manifest); err != nil {
		t.Fatalf("valid manifest: %v", err)
	}
	manifest.HandoffDiversity = false
	if err := validateBreadthDepthManifest(manifest); err == nil {
		t.Fatal("explicit handoff_diversity=false was silently replaced")
	}
	manifest = validBreadthDepthManifest()
	manifest.LocalActions++
	if err := validateBreadthDepthManifest(manifest); err == nil {
		t.Fatal("mismatched action budget accepted")
	}
}

func TestBreadthDepthBoundaryMethodsUseTheSameTotalBudget(t *testing.T) {
	manifest := validBreadthDepthManifest()
	facet := budgetForMethod(manifest, breadthdepth.MethodFacetOnly)
	local := budgetForMethod(manifest, breadthdepth.MethodLocalOnly)
	combined := budgetForMethod(manifest, breadthdepth.MethodFacetThen)
	for method, budget := range map[string]breadthdepth.Budget{
		"facet": facet, "local": local, "combined": combined,
	} {
		if err := budget.Validate(); err != nil {
			t.Fatalf("%s: %v", method, err)
		}
		if budget.TotalCandidates != 90 || budget.TotalActions != 16200 {
			t.Fatalf("%s total changed: %+v", method, budget)
		}
	}
	if facet.GlobalCandidates != 90 || facet.LocalCandidates != 0 {
		t.Fatalf("M0 budget=%+v", facet)
	}
	if local.GlobalCandidates != 0 || local.LocalCandidates != 90 {
		t.Fatalf("M1 budget=%+v", local)
	}
}

func TestRootHandoffSeedTracksDynamicFrontierAncestry(t *testing.T) {
	parents := map[string]string{
		"handoff-corpus-1": "",
		"frontier-1":       "handoff-corpus-1",
		"frontier-2":       "frontier-1",
	}
	if got := rootHandoffSeed("frontier-2", parents); got != "handoff-corpus-1" {
		t.Fatalf("root=%q", got)
	}
	parents["handoff-corpus-1"] = "frontier-2"
	if got := rootHandoffSeed("frontier-2", parents); got != "" {
		t.Fatalf("cycle root=%q", got)
	}
}

func TestNormalizeBreadthDepthSummaryCensorsUnsuccessfulLocalSearch(t *testing.T) {
	directory := t.TempDir()
	summary := breadthdepth.CombinedSummary{
		SchemaVersion: breadthdepth.SchemaVersion,
		Local: &breadthdepth.LocalPhaseResult{
			SchemaVersion: breadthdepth.SchemaVersion,
			GoalID:        string(goalsearch.GoalSnapshotCatchUpAfterPartition),
		},
	}
	if err := normalizeBreadthDepthSummary(directory, &summary); err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if !summary.BudgetExhausted || !summary.Local.BudgetExhausted ||
		summary.StableKey == "" || summary.Local.StableKey == "" {
		t.Fatalf("summary not censored or re-keyed: %+v", summary)
	}
	var persisted breadthdepth.CombinedSummary
	if err := persistence.ReadJSON(
		directory+"/combined-summary.json", &persisted); err != nil {
		t.Fatalf("read normalized summary: %v", err)
	}
	if !persisted.BudgetExhausted || !persisted.Local.BudgetExhausted {
		t.Fatalf("persisted summary not censored: %+v", persisted)
	}
}
