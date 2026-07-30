package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/breadthdepth"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/goalsearch"
)

func validHandoffProbeManifest() handoffProbeManifest {
	return handoffProbeManifest{
		Schema: handoffProbeManifestSchema, Name: "stage1", Phase: "test",
		Config: "config.json", SourceBenchmarkRoot: "formal",
		TLCAddress: "http://127.0.0.1:2027",
		Goals: []goalsearch.GoalID{
			goalsearch.GoalSnapshotCatchUpAfterPartition,
			goalsearch.GoalRestartHigherTermMessage,
		},
		Seeds: []int64{9501, 9502}, HandoffTopK: 8,
		ProbeCandidateBudget: 5, ProbeActionBudget: 900, MaxPlanActions: 180,
		SnapshotThreshold: 3, RetainEntries: 1, LocalFrontierCapacity: 1,
		SaveAllRuns: true, ReplayVerify: true,
	}
}

func TestHandoffProbeManifestFreezesStageOneBudget(t *testing.T) {
	manifest := validHandoffProbeManifest()
	if err := validateHandoffProbeManifest(manifest); err != nil {
		t.Fatalf("valid manifest: %v", err)
	}
	manifest.ProbeCandidateBudget = 6
	if err := validateHandoffProbeManifest(manifest); err == nil {
		t.Fatal("non-frozen probe candidate budget accepted")
	}
}

func TestLocalOnly30ManifestMatchesFormalM5LocalBudget(t *testing.T) {
	repository, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	localOnly, err := readBreadthDepthManifest(
		filepath.Join(repository, "examples", "breadth-depth-stage1-local-only-30.json"))
	if err != nil {
		t.Fatal(err)
	}
	formal, err := readBreadthDepthManifest(
		filepath.Join(repository, "examples", "breadth-depth-formal.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := validateBreadthDepthManifest(localOnly); err != nil {
		t.Fatalf("local-only-30 manifest: %v", err)
	}
	if localOnly.TotalCandidates != formal.LocalCandidates ||
		localOnly.TotalActions != formal.LocalActions ||
		localOnly.MaxPlanActions != formal.MaxPlanActions ||
		localOnly.SnapshotThreshold != formal.SnapshotThreshold ||
		localOnly.RetainEntries != formal.RetainEntries {
		t.Fatalf("local-only-30 does not match M5 local phase")
	}
	if len(localOnly.Methods) != 1 ||
		localOnly.Methods[0] != breadthdepth.MethodLocalOnly ||
		localOnly.GlobalCandidates != 0 || localOnly.LocalCandidates != 30 {
		t.Fatalf("local-only-30 method/budget is not isolated: %+v", localOnly)
	}
}

func TestProbePosteriorLexicographicOrder(t *testing.T) {
	base := handoffProbeResult{
		BestCompletedWaypoints: 4, BestDistance: 2,
		MutationLegal: 4, MutationExecuted: 4,
		Actions: 100, HandoffStableKey: "b",
	}
	goal := base
	goal.GoalReached = true
	if !probeResultLess(goal, base) {
		t.Fatal("Goal reach did not dominate")
	}
	waypoint := base
	waypoint.BestCompletedWaypoints = 5
	if !probeResultLess(waypoint, base) {
		t.Fatal("completed waypoint count did not dominate")
	}
	distance := base
	distance.BestDistance = 1
	if !probeResultLess(distance, base) {
		t.Fatal("staged Distance did not dominate")
	}
	stable := base
	stable.HandoffStableKey = "a"
	if !probeResultLess(stable, base) {
		t.Fatal("StableKey did not break an exact tie")
	}
}

func TestProbeKeepsEightSeedsIndependentOfCapacityOne(t *testing.T) {
	candidates := make([]breadthdepth.HandoffSeed, 8)
	for index := range candidates {
		candidates[index] = breadthdepth.HandoffSeed{
			GlobalCorpusID: string(rune('a' + index)),
			Progress: breadthdepth.GoalProgress{
				EntryCondition: true, Completed: 2, Distance: 3,
			},
			Replayable: true, StableKey: string(rune('a' + index)),
		}
	}
	selected, err := breadthdepth.SelectHandoff("goal", candidates, 8)
	if err != nil {
		t.Fatal(err)
	}
	if len(selected.Selected) != 8 {
		t.Fatalf("K=8 was compressed before probing: %d", len(selected.Selected))
	}
	for _, candidate := range selected.Selected {
		frontier, err := goalsearch.NewCapacityFrontier(1)
		if err != nil {
			t.Fatal(err)
		}
		seed := goalsearch.FrontierSeed{
			ID: candidate.GlobalCorpusID, SemanticKey: candidate.StableKey,
			Progress: goalsearch.GoalProgress{
				CompletedWaypointCount: candidate.Progress.Completed,
				DistanceToCurrent:      candidate.Progress.Distance,
			},
		}
		if inserted, err := frontier.Consider(seed); err != nil || !inserted {
			t.Fatalf("independent Frontier rejected %s: inserted=%t err=%v",
				seed.ID, inserted, err)
		}
		if got := len(frontier.Snapshot().Seeds); got != 1 {
			t.Fatalf("independent Frontier size=%d", got)
		}
	}
}

func TestNoLegalMutationReasonCodes(t *testing.T) {
	maxStats := goalsearch.MutationStats{RejectedMaxActions: 1}
	reason, layer, _ := classifyMutationRejection(maxStats, errors.New("max"))
	if reason != "budget_or_length_limit" || layer != "pre_advisor_plan_length" {
		t.Fatalf("max rejection classified as %s/%s", reason, layer)
	}
	noAction := goalsearch.MutationStats{RejectedNoAction: 1}
	reason, _, _ = classifyMutationRejection(noAction, errors.New("none"))
	if reason != "advisor_no_legal_successor" {
		t.Fatalf("no-action rejection classified as %s", reason)
	}
}

func TestDiagnosisAttemptSchemaSerializes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "attempts.jsonl")
	attempt := mutationAttemptDiagnostic{
		SchemaVersion: handoffDiagnosisSchema + "-attempt",
		Attempt:       1, ReasonCode: "budget_or_length_limit",
	}
	if err := writeJSONLines(path, []mutationAttemptDiagnostic{attempt}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) == 0 {
		t.Fatal("empty diagnosis attempt artifact")
	}
}
