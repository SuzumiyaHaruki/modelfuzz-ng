package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/core"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/engine"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/goalsearch"
	raftmodel "github.com/SuzumiyaHaruki/modelfuzz-ng/internal/model/raft"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/persistence"
)

func TestGoalSearchWritesVersionedArtifactsWithoutLLM(t *testing.T) {
	root := t.TempDir()
	config := defaultCLIConfig()
	config.Model.Profile = raftmodel.ProfileStorageSnapshot
	config.Model.MaxLogIndex = 10
	config.Model.LargestTerm = 10
	config.Raft.Snapshot.Threshold = 3
	config.Raft.Snapshot.RetainEntries = 1
	configPath := filepath.Join(root, "config.json")
	if err := writeJSONFile(configPath, config); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(root, "goal-run")
	var stdout, stderr bytes.Buffer
	err := goalSearchCommand(context.Background(), []string{
		"-config", configPath,
		"-goal", string(goalsearch.GoalSnapshotCatchUpAfterPartition),
		"-mode", string(goalsearch.ModeFrontier),
		"-output", output,
		"-candidate-budget", "1",
		"-action-budget", "100",
		"-max-actions-per-plan", "50",
		"-per-waypoint-budget", "1",
		"-frontier-top-k", "2",
		"-strict-tlc=false",
		"-goal-aware-mutation=true",
		"-prefix-preservation=true",
		"-save-all-runs=false",
		"-snapshot-threshold", "3",
		"-retain-entries", "1",
	}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("goal-search: %v\nstderr=%s", err, stderr.String())
	}
	for _, name := range []string{
		"goal-definition.json", "goal-settings.json", "goal-progress.jsonl",
		"frontier-manifest.json", "final-report.json",
		"branch-catalog.json", "branch-settings.json", "branch-instances.jsonl",
		"branch-progress.jsonl", "branch-frontier-manifest.json",
		"planned-realized-mapping.json", "branch-feasibility.json", "branch-summary.json",
		"branch-evidence-catalog.json", "branch-evidence.jsonl",
		"branch-commitments.jsonl", "branch-evidence-summary.json",
		"branch-formation-failures.jsonl", "branch-budget-ledger.jsonl",
		"branch-budget-summary.json",
		"micro-progress-registry.json", "micro-progress-utility.csv",
		"evidence-frontier-manifest.json",
	} {
		if _, err := os.Stat(filepath.Join(output, name)); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	var settings goalSearchSettings
	if err := persistence.ReadJSON(filepath.Join(output, "goal-settings.json"), &settings); err != nil {
		t.Fatal(err)
	}
	if settings.SchemaVersion != goalsearch.SchemaVersion || settings.LLMCalls != 0 {
		t.Fatalf("settings=%+v", settings)
	}
	var report goalSearchReport
	if err := persistence.ReadJSON(filepath.Join(output, "final-report.json"), &report); err != nil {
		t.Fatal(err)
	}
	if report.Candidates != 1 || report.LLMCalls != 0 || report.OnlineOfflineMismatches != 0 {
		t.Fatalf("report=%+v", report)
	}
}

func TestSelectGoalBranchesIsDeterministicGoalScopedAndSkipsPermanent(t *testing.T) {
	environment := goalsearch.BranchEnvironment{
		NodeCount: 3, ModelProfile: "storage-snapshot",
		SnapshotThreshold: 3, PartitionEnabled: true,
	}
	first, feasibility, err := selectGoalBranches(
		goalsearch.GoalSnapshotCatchUpAfterPartition, "", true, environment,
	)
	if err != nil {
		t.Fatal(err)
	}
	second, _, err := selectGoalBranches(
		goalsearch.GoalSnapshotCatchUpAfterPartition, "", true, environment,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 4 || !reflect.DeepEqual(first, second) ||
		len(feasibility) != 5 {
		t.Fatalf("selected=%d feasibility=%d stable=%v",
			len(first), len(feasibility), reflect.DeepEqual(first, second))
	}
	for index := 1; index < len(first); index++ {
		if first[index-1].BranchTemplateID >= first[index].BranchTemplateID {
			t.Fatalf("Branch allocation order is unstable: %v", branchTemplateIDs(first))
		}
	}
	disabled := environment
	disabled.SnapshotThreshold = 0
	active, results, err := selectGoalBranches(
		goalsearch.GoalSnapshotCatchUpAfterPartition,
		string(goalsearch.BranchASnapshotAfterHeal), false, disabled,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(active) != 0 || len(results) != 1 ||
		results[0].Status != goalsearch.BranchPermanentlyInfeasible {
		t.Fatalf("permanent Branch was not skipped: active=%v results=%+v", active, results)
	}
	if _, _, err := selectGoalBranches(
		goalsearch.GoalSnapshotCatchUpAfterPartition,
		string(goalsearch.BranchBHigherApp), false, environment,
	); err == nil {
		t.Fatal("cross-Goal Branch was accepted")
	}
}

func TestBranchSummaryRecomputeIsDeterministic(t *testing.T) {
	path := filepath.Join(t.TempDir(), "branch-progress.jsonl")
	journal, err := persistence.OpenJournal(path)
	if err != nil {
		t.Fatal(err)
	}
	records := []branchProgressRecord{
		{
			SchemaVersion: goalsearch.BranchSchemaVersion, RunID: "a",
			PlannedTemplateID:  goalsearch.BranchBHigherApp,
			RealizedTemplateID: goalsearch.BranchBHigherApp,
			PlannedKey:         "p", RealizedKey: strings.Repeat("a", 64),
			RealizedDecidable: true,
			Feasibility:       goalsearch.BranchCompleted, Agreement: true,
			DeepestWaypoint: 6, GoalReached: true, ActionCount: 10,
			FrontierChanged: true, NewRealizedBranch: true,
			EvictedBranches: []goalsearch.BranchTemplateID{
				goalsearch.BranchBHigherHeartbeat,
			},
		},
		{
			SchemaVersion: goalsearch.BranchSchemaVersion, RunID: "b",
			PlannedTemplateID:  goalsearch.BranchBHigherHeartbeat,
			RealizedTemplateID: goalsearch.BranchBHigherApp,
			PlannedKey:         "q", RealizedKey: strings.Repeat("a", 64),
			RealizedDecidable: true,
			Feasibility:       goalsearch.BranchViolated,
			Deviation:         goalsearch.BranchDeviation{Occurred: true, Reason: "key-message"},
			DeepestWaypoint:   5, BugDetected: true, ActionCount: 12, NewFacet: true,
		},
		{
			SchemaVersion: goalsearch.BranchSchemaVersion, RunID: "c",
			PlannedTemplateID: goalsearch.BranchBHigherVote,
			PlannedKey:        "r", RealizedKey: strings.Repeat("b", 64),
			Feasibility:     goalsearch.BranchCurrentlyInfeasible,
			DeepestWaypoint: 2, BugDetected: true, ActionCount: 9,
		},
	}
	for _, record := range records {
		if err := journal.Append(record); err != nil {
			t.Fatal(err)
		}
	}
	if err := journal.Close(); err != nil {
		t.Fatal(err)
	}
	first, err := recomputeBranchSummary(path, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	second, err := recomputeBranchSummary(path, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	firstJSON, _ := json.Marshal(first)
	secondJSON, _ := json.Marshal(second)
	if !bytes.Equal(firstJSON, secondJSON) {
		t.Fatalf("recomputed summaries differ\n%s\n%s", firstJSON, secondJSON)
	}
	if first.RealizedBranchCount != 1 || first.PlannedRealizedPairCount != 2 ||
		first.SuccessfulBranchCount != 1 || first.DecidableRuns != 2 {
		t.Fatalf("summary=%+v", first)
	}
	if first.ByPlannedBranch[goalsearch.BranchBHigherHeartbeat].FrontierEvicted != 1 {
		t.Fatalf("per-Branch eviction was not recomputed: %+v", first.ByPlannedBranch)
	}
	if first.ByPlannedBranch[goalsearch.BranchBHigherVote].BugDetected != 1 {
		t.Fatalf("pre-decidable bug lost Planned Branch attribution: %+v",
			first.ByPlannedBranch)
	}
}

func TestGoalSearchRejectsUnknownModeAndMismatchedControls(t *testing.T) {
	var output bytes.Buffer
	err := goalSearchCommand(context.Background(), []string{
		"-goal", string(goalsearch.GoalRestartHigherTermMessage),
		"-mode", "missing",
		"-output", filepath.Join(t.TempDir(), "missing"),
	}, &output, &output)
	if err == nil {
		t.Fatal("unknown mode accepted")
	}
	err = goalSearchCommand(context.Background(), []string{
		"-goal", string(goalsearch.GoalRestartHigherTermMessage),
		"-mode", string(goalsearch.ModeUnguided),
		"-output", filepath.Join(t.TempDir(), "controls"),
	}, &output, &output)
	if err == nil {
		t.Fatal("unguided mode accepted goal-aware/frontier defaults")
	}
}

func TestGoalSearchFrontierReachesBothRegisteredGoals(t *testing.T) {
	root := t.TempDir()
	config := defaultCLIConfig()
	config.Model.Profile = raftmodel.ProfileStorageSnapshot
	config.Model.MaxLogIndex = 10
	config.Model.LargestTerm = 10
	config.Raft.Snapshot.Threshold = 3
	config.Raft.Snapshot.RetainEntries = 1
	configPath := filepath.Join(root, "config.json")
	if err := writeJSONFile(configPath, config); err != nil {
		t.Fatal(err)
	}
	for _, goal := range []goalsearch.GoalID{
		goalsearch.GoalSnapshotCatchUpAfterPartition,
		goalsearch.GoalRestartHigherTermMessage,
	} {
		t.Run(string(goal), func(t *testing.T) {
			output := filepath.Join(root, string(goal))
			var stdout, stderr bytes.Buffer
			err := goalSearchCommand(context.Background(), []string{
				"-config", configPath,
				"-goal", string(goal),
				"-mode", string(goalsearch.ModeFrontier),
				"-output", output,
				"-seed", "1",
				"-candidate-budget", "15",
				"-action-budget", "1500",
				"-max-actions-per-plan", "140",
				"-per-waypoint-budget", "15",
				"-frontier-top-k", "6",
				"-strict-tlc=false",
				"-goal-aware-mutation=true",
				"-prefix-preservation=true",
				"-save-all-runs=false",
				"-snapshot-threshold", "3",
				"-retain-entries", "1",
				"-workers", "1",
				"-replay-verify=true",
			}, &stdout, &stderr)
			if err != nil {
				t.Fatalf("goal-search: %v\nstderr=%s", err, stderr.String())
			}
			var report goalSearchReport
			if err := persistence.ReadJSON(filepath.Join(output, "final-report.json"), &report); err != nil {
				t.Fatal(err)
			}
			if !report.TargetReached {
				t.Fatalf("goal was not reached: stalled=%s waypoints=%+v",
					report.MostStalledWaypoint, report.Waypoints)
			}
			for _, waypoint := range report.Waypoints {
				if !waypoint.Reached {
					t.Fatalf("waypoint %s was not reached", waypoint.ID)
				}
			}
			if report.OnlineOfflineMismatches != 0 ||
				report.PrefixReplayAttempts != report.PrefixReplaySuccess {
				t.Fatalf("alignment/replay report=%+v", report)
			}
			if _, err := os.Stat(filepath.Join(output, "target-reached.json")); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestSnapshotDirectedReferenceUsesGeneralGoalEvaluator(t *testing.T) {
	root := t.TempDir()
	config := defaultCLIConfig()
	config.Model.Profile = raftmodel.ProfileStorageSnapshot
	config.Model.MaxLogIndex = 10
	config.Model.LargestTerm = 10
	configPath := filepath.Join(root, "config.json")
	if err := writeJSONFile(configPath, config); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(root, "directed")
	var stdout, stderr bytes.Buffer
	err := goalSearchCommand(context.Background(), []string{
		"-config", configPath,
		"-goal", string(goalsearch.GoalSnapshotCatchUpAfterPartition),
		"-mode", string(goalsearch.ModeDirectedSnapshot),
		"-output", output,
		"-seed", "11",
		"-candidate-budget", "1",
		"-action-budget", "1000",
		"-max-actions-per-plan", "200",
		"-per-waypoint-budget", "20",
		"-frontier-top-k", "1",
		"-hint-strength", "none",
		"-distance-mode", "staged-distance",
		"-strict-tlc=false",
		"-goal-aware-mutation=false",
		"-prefix-preservation=false",
		"-save-all-runs=false",
		"-snapshot-threshold", "3",
		"-retain-entries", "1",
		"-replay-verify=false",
	}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("directed reference: %v\n%s", err, stderr.String())
	}
	var report goalSearchReport
	if err := persistence.ReadJSON(filepath.Join(output, "final-report.json"), &report); err != nil {
		t.Fatal(err)
	}
	if !report.TargetReached || report.Candidates != 1 ||
		report.Settings.Mode != goalsearch.ModeDirectedSnapshot ||
		report.OnlineOfflineMismatches != 0 {
		t.Fatalf("directed report=%+v", report)
	}
}

func TestAggregateGoalReportsSeparatesGoalAndMode(t *testing.T) {
	reports := []goalSearchReport{
		{
			Settings: goalSearchSettings{
				GoalID: goalsearch.GoalRestartHigherTermMessage,
				Mode:   goalsearch.ModeFrontier, Seed: 2,
			},
			TargetReached: true, Candidates: 5, Actions: 40,
			Waypoints:           []waypointAggregate{{ID: "W1", Reached: true}, {ID: "W2", Reached: true}},
			MostStalledWaypoint: "W2",
		},
		{
			Settings: goalSearchSettings{
				GoalID: goalsearch.GoalRestartHigherTermMessage,
				Mode:   goalsearch.ModeFrontier, Seed: 1,
			},
			Candidates: 10, Actions: 80,
			Waypoints:           []waypointAggregate{{ID: "W1", Reached: true}, {ID: "W2"}},
			MostStalledWaypoint: "W2",
		},
		{
			Settings: goalSearchSettings{
				GoalID: goalsearch.GoalRestartHigherTermMessage,
				Mode:   goalsearch.ModeGoalAware, Seed: 1,
			},
			Candidates: 9, Actions: 70,
			Waypoints:           []waypointAggregate{{ID: "W1", Reached: true}},
			MostStalledWaypoint: "W1",
		},
	}
	comparison := aggregateGoalReports("/tmp/input", reports)
	if comparison.ReportCount != 3 || len(comparison.Groups) != 2 {
		t.Fatalf("comparison=%+v", comparison)
	}
	for _, group := range comparison.Groups {
		if group.Mode != goalsearch.ModeFrontier {
			continue
		}
		if group.GoalReachRate != 0.5 || group.MeanExecutedCandidates != 7.5 ||
			group.WaypointReachRates["W2"] != 0.5 {
			t.Fatalf("frontier group=%+v", group)
		}
		if len(group.Seeds) != 2 || group.Seeds[0] != 1 || group.Seeds[1] != 2 {
			t.Fatalf("seeds=%v", group.Seeds)
		}
	}
}

func TestAggregateGoalReportsSeparatesAblationAndMutantDimensions(t *testing.T) {
	base := goalSearchReport{
		Settings: goalSearchSettings{
			GoalID: goalsearch.GoalSnapshotCatchUpAfterPartition,
			Mode:   goalsearch.ModeFrontier, HintStrength: goalsearch.HintWeak,
			FrontierTopK: 1, PrefixPreservation: true,
			DistanceMode: goalsearch.DistanceStaged, Subject: "control", Seed: 1,
		},
		TargetReached: true, FirstTargetCandidate: 2, FirstTargetActions: 20,
		FirstTargetMillis: 10,
	}
	differentK := base
	differentK.Settings.FrontierTopK = 2
	differentK.Settings.Seed = 2
	mutant := base
	mutant.Settings.Subject = "mutant-snapshot-status-invert"
	mutant.Settings.Seed = 3
	mutant.BugDetected = true
	mutant.FirstFailureCandidate = 3
	mutant.FirstFailureActions = 30
	controlFailure := base
	controlFailure.Settings.Seed = 4
	controlFailure.BugDetected = true
	controlFailure.FirstFailureCandidate = 4
	controlFailure.FirstFailureActions = 40
	comparison := aggregateGoalReports(
		"/tmp/input", []goalSearchReport{base, differentK, mutant, controlFailure},
	)
	if len(comparison.Groups) != 3 {
		t.Fatalf("ablation groups=%d want 3: %+v", len(comparison.Groups), comparison.Groups)
	}
	for _, group := range comparison.Groups {
		if group.Subject == "mutant-snapshot-status-invert" &&
			(group.BugDetectionRate != 1 || group.FalsePositiveRate != 0) {
			t.Fatalf("mutant group=%+v", group)
		}
		if group.Subject == "control" && group.FrontierTopK == 1 &&
			group.FalsePositiveRate != 0.5 {
			t.Fatalf("control false-positive group=%+v", group)
		}
	}
}

func TestGoalFailureSignatureExcludesSchedulingFailures(t *testing.T) {
	if _, detected := goalFailureSignature(engine.Result{
		Status: engine.StatusResolutionFailed,
	}); detected {
		t.Fatal("resolution failure was counted as a bug")
	}
	if _, detected := goalFailureSignature(engine.Result{
		Status: engine.StatusRuntimeFailed,
		Failure: &core.FailureRecord{
			Kind: core.FailureSUTPanic, Operation: "restart", PanicValue: "boom",
		},
	}); !detected {
		t.Fatal("runtime SUT failure was not counted as a bug")
	}
}

func TestSnapshotMappingMutantKeepsOfflinePrefixComparable(t *testing.T) {
	root := t.TempDir()
	config := defaultCLIConfig()
	config.Model.Profile = raftmodel.ProfileStorageSnapshot
	config.Model.MaxLogIndex = 10
	config.Model.LargestTerm = 10
	config.Raft.Snapshot.Threshold = 3
	config.Raft.Snapshot.RetainEntries = 1
	config.Raft.Faults.SnapshotStatusMap = "invert"
	configPath := filepath.Join(root, "config.json")
	if err := writeJSONFile(configPath, config); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(root, "mutant")
	var stdout, stderr bytes.Buffer
	err := goalSearchCommand(context.Background(), []string{
		"-config", configPath,
		"-goal", string(goalsearch.GoalSnapshotCatchUpAfterPartition),
		"-mode", string(goalsearch.ModeFrontier),
		"-output", output,
		"-seed", "4101",
		"-candidate-budget", "8",
		"-action-budget", "1200",
		"-max-actions-per-plan", "160",
		"-per-waypoint-budget", "20",
		"-frontier-top-k", "1",
		"-hint-strength", "strong",
		"-strict-tlc=false",
		"-goal-aware-mutation=true",
		"-prefix-preservation=true",
		"-save-all-runs=false",
		"-snapshot-threshold", "3",
		"-retain-entries", "1",
		"-replay-verify=true",
		"-stop-on-target=false",
		"-stop-on-failure=true",
	}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("goal-search mutant: %v\nstderr=%s", err, stderr.String())
	}
	var report goalSearchReport
	if err := persistence.ReadJSON(filepath.Join(output, "final-report.json"), &report); err != nil {
		t.Fatal(err)
	}
	if !report.BugDetected || report.FirstFailureLayer != engine.StatusMappingFailed ||
		report.OnlineOfflineMismatches != 0 || report.ExpectedOfflineMapFailures != 1 ||
		report.FirstFailureSignature == nil {
		t.Fatalf("mutant report=%+v", report)
	}
}

func TestGoalBenchmarkRunsIndependentCampaignsAndSkipsCompleted(t *testing.T) {
	root := t.TempDir()
	config := defaultCLIConfig()
	config.Model.Profile = raftmodel.ProfileStorageSnapshot
	config.Model.MaxLogIndex = 10
	config.Model.LargestTerm = 10
	config.Raft.Snapshot.Threshold = 3
	config.Raft.Snapshot.RetainEntries = 1
	configPath := filepath.Join(root, "config.json")
	if err := writeJSONFile(configPath, config); err != nil {
		t.Fatal(err)
	}
	campaign := func(id string, goal goalsearch.GoalID) goalBenchmarkCampaign {
		return goalBenchmarkCampaign{
			ID: id, GoalID: goal, Method: goalsearch.ModeFrontier,
			HintStrength: goalsearch.HintStrong, FrontierTopK: 2,
			PrefixPreservation: true, DistanceMode: goalsearch.DistanceStaged,
			Seeds: []int64{11}, ConfigPath: configPath, NodeCount: 3,
			CandidateBudget: 1, ActionBudget: 100, MaxActionsPerPlan: 50,
			PerWaypointBudget: 1, SnapshotThreshold: 3, RetainEntries: 1,
			CrashQuota: 2, PartitionEnabled: true, ReplayVerify: true,
			StopOnTarget: true,
		}
	}
	manifest := goalBenchmarkManifest{
		SchemaVersion: goalBenchmarkSchema,
		Campaigns: []goalBenchmarkCampaign{
			campaign("goal-a", goalsearch.GoalSnapshotCatchUpAfterPartition),
			campaign("goal-b", goalsearch.GoalRestartHigherTermMessage),
		},
	}
	manifestPath := filepath.Join(root, "manifest.json")
	if err := writeJSONFile(manifestPath, manifest); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(root, "benchmark")
	var stdout, stderr bytes.Buffer
	for attempt := 0; attempt < 2; attempt++ {
		if err := goalBenchmarkCommand(context.Background(), []string{
			"-manifest", manifestPath, "-output", output,
		}, &stdout, &stderr); err != nil {
			t.Fatalf("attempt %d: %v\n%s", attempt, err, stderr.String())
		}
	}
	var status goalBenchmarkStatus
	if err := persistence.ReadJSON(filepath.Join(output, "benchmark-status.json"), &status); err != nil {
		t.Fatal(err)
	}
	if len(status.Runs) != 2 || status.LLMCalls != 0 {
		t.Fatalf("status=%+v", status)
	}
	for _, run := range status.Runs {
		if run.Status != "skipped-complete" {
			t.Fatalf("second pass status=%+v", run)
		}
	}
	for _, name := range []string{
		"comparison-summary.json", "figure-ready.csv", "benchmark-manifest.json",
		"seed-manifest.json", "seed-diversity.json", "environment.json",
		"per-seed-branches.csv", "per-branch-bug-detection.csv",
	} {
		if _, err := os.Stat(filepath.Join(output, name)); err != nil {
			t.Fatal(err)
		}
	}
	firstManifest, firstDiversity := buildGoalSeedManifest(status, manifest)
	secondManifest, secondDiversity := buildGoalSeedManifest(status, manifest)
	if !reflect.DeepEqual(firstManifest, secondManifest) ||
		!reflect.DeepEqual(firstDiversity, secondDiversity) {
		t.Fatal("seed manifest generation is not deterministic")
	}
	csvBytes, err := os.ReadFile(filepath.Join(output, "figure-ready.csv"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.SplitN(string(csvBytes), "\n", 2)[0], "config") {
		t.Fatal("figure-ready CSV does not identify the effective config")
	}
	for _, name := range []string{"per-seed-branches.csv", "per-branch-bug-detection.csv"} {
		branchCSV, readErr := os.ReadFile(filepath.Join(output, name))
		if readErr != nil {
			t.Fatal(readErr)
		}
		header := strings.SplitN(string(branchCSV), "\n", 2)[0]
		for _, column := range []string{
			"hint_strength", "total_frontier_capacity", "branch_awareness",
			"branch_dimension_ablation",
		} {
			if !strings.Contains(header, column) {
				t.Fatalf("%s does not identify experiment dimension %s: %s",
					name, column, header)
			}
		}
	}
}

func TestGoalBenchmarkExplicitFalseOverridesTrueBooleanDefaults(t *testing.T) {
	raw := []byte(`{
		"schema_version":"raft-goal-benchmark-v1",
		"defaults":{
			"all_feasible_branches":true,
			"formation_failure_report":true,
			"prefix_preservation":true,
			"partition_enabled":true,
			"strict_tlc":true,
			"replay_verify":true,
			"save_all_runs":true,
			"stop_on_target":true,
			"stop_on_failure":true
		},
		"campaigns":[
			{"id":"explicit-false","all_feasible_branches":false,
			 "formation_failure_report":false,"prefix_preservation":false,
			 "partition_enabled":false,"strict_tlc":false,"replay_verify":false,
			 "save_all_runs":false,"stop_on_target":false,"stop_on_failure":false},
			{"id":"inherit"}
		]
	}`)
	var manifest goalBenchmarkManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	manifest = normalizeGoalBenchmarkManifest(manifest)
	explicit := manifest.Campaigns[0]
	for name, value := range map[string]bool{
		"all_feasible_branches": explicit.AllFeasibleBranches,
		"formation_failure":     explicit.FormationFailureReport,
		"prefix":                explicit.PrefixPreservation,
		"partition":             explicit.PartitionEnabled,
		"strict_tlc":            explicit.StrictTLC,
		"replay":                explicit.ReplayVerify,
		"save_all":              explicit.SaveAllRuns,
		"stop_target":           explicit.StopOnTarget,
		"stop_failure":          explicit.StopOnFailure,
	} {
		if value {
			t.Fatalf("explicit false for %s inherited true default", name)
		}
	}
	inherited := manifest.Campaigns[1]
	if !inherited.AllFeasibleBranches || !inherited.FormationFailureReport ||
		!inherited.PrefixPreservation || !inherited.PartitionEnabled ||
		!inherited.StrictTLC || !inherited.ReplayVerify ||
		!inherited.SaveAllRuns || !inherited.StopOnTarget ||
		!inherited.StopOnFailure {
		t.Fatalf("omitted booleans did not inherit defaults: %+v", inherited)
	}
}
