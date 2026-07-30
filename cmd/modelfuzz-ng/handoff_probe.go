package main

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/breadthdepth"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/core"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/goalsearch"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/persistence"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/plan"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/protocolmutation"
	raftadvisor "github.com/SuzumiyaHaruki/modelfuzz-ng/internal/protocolmutation/raft"
)

const (
	handoffProbeManifestSchema = "raft-handoff-probe-benchmark-v1"
	handoffProbeResultSchema   = "raft-handoff-probe-result-v1"
	handoffDiagnosisSchema     = "raft-handoff-diagnosis-v1"
)

type handoffProbeManifest struct {
	Schema                string              `json:"schema_version"`
	Name                  string              `json:"name"`
	Phase                 string              `json:"phase"`
	Config                string              `json:"config"`
	SourceBenchmarkRoot   string              `json:"source_benchmark_root"`
	TLCAddress            string              `json:"tlc_address"`
	Goals                 []goalsearch.GoalID `json:"goals"`
	Seeds                 []int64             `json:"seeds"`
	HandoffTopK           int                 `json:"handoff_top_k"`
	ProbeCandidateBudget  int                 `json:"probe_candidate_budget"`
	ProbeActionBudget     int                 `json:"probe_action_budget"`
	MaxPlanActions        int                 `json:"max_actions_per_plan"`
	SnapshotThreshold     uint64              `json:"snapshot_threshold"`
	RetainEntries         uint64              `json:"retain_entries"`
	LocalFrontierCapacity int                 `json:"local_frontier_capacity"`
	SaveAllRuns           bool                `json:"save_all_runs"`
	ReplayVerify          bool                `json:"replay_verify"`
}

type pendingControlSummary struct {
	RunningNodes            int            `json:"running_nodes"`
	CrashedNodes            int            `json:"crashed_nodes"`
	PendingMessages         int            `json:"pending_messages"`
	BlockedMessages         int            `json:"blocked_messages"`
	MessageTypes            map[string]int `json:"message_types,omitempty"`
	PartitionActive         bool           `json:"partition_active"`
	AvailableActionTypes    []string       `json:"available_action_types"`
	NoControllableSuccessor bool           `json:"no_controllable_successor"`
}

type handoffProbeResult struct {
	SchemaVersion             string                `json:"schema_version"`
	CampaignSeed              int64                 `json:"campaign_seed"`
	Goal                      goalsearch.GoalID     `json:"goal"`
	HandoffRank               int                   `json:"handoff_rank"`
	HandoffStableKey          string                `json:"handoff_stable_key"`
	GlobalCorpusID            string                `json:"global_corpus_id"`
	InitialCompletedWaypoints int                   `json:"initial_completed_waypoints"`
	InitialDistance           int                   `json:"initial_distance"`
	InitialTargetReached      bool                  `json:"initial_target_reached"`
	RelativeSemanticClass     string                `json:"relative_semantic_class"`
	FacetSignature            string                `json:"facet_signature"`
	FacetNoveltyCount         int                   `json:"facet_novelty_count"`
	PrefixLength              int                   `json:"prefix_length"`
	ReplayOK                  bool                  `json:"replay_ok"`
	MutationAttempted         int                   `json:"mutation_attempted"`
	MutationLegal             int                   `json:"mutation_legal"`
	MutationExecuted          int                   `json:"mutation_executed"`
	RejectionReasons          map[string]int        `json:"rejection_reasons"`
	BestCompletedWaypoints    int                   `json:"best_completed_waypoints"`
	BestDistance              int                   `json:"best_distance"`
	DistanceDelta             int                   `json:"distance_delta"`
	NewWaypointReached        bool                  `json:"new_waypoint_reached"`
	GoalReached               bool                  `json:"goal_reached"`
	Actions                   int                   `json:"actions"`
	WallTimeMillis            int64                 `json:"wall_time_ms"`
	PendingControlSummary     pendingControlSummary `json:"pending_control_summary"`
	Outcome                   string                `json:"outcome"`
	Error                     string                `json:"error,omitempty"`
	PosteriorRank             int                   `json:"posterior_rank,omitempty"`
}

type handoffProbeSummary struct {
	SchemaVersion               string                    `json:"schema_version"`
	Name                        string                    `json:"name"`
	Results                     int                       `json:"results"`
	Campaigns                   int                       `json:"campaigns"`
	RankOnePosteriorBest        int                       `json:"rank_1_posterior_best"`
	UnselectedStrictlyBetter    int                       `json:"campaigns_unselected_strictly_better"`
	RankOneTopOneRegret         map[string]int            `json:"rank_1_top_1_regret_distribution"`
	RankLegalRates              map[string]float64        `json:"rank_legal_mutation_rates"`
	StaticPosteriorSpearmanMean float64                   `json:"static_rank_posterior_spearman_mean"`
	BestOfKGoalReach            int                       `json:"best_of_k_goal_reach"`
	RankOneGoalReach            int                       `json:"rank_1_goal_reach"`
	BestOfKWaypointImprovements int                       `json:"best_of_k_waypoint_improvements"`
	BestOfKDistanceImprovements int                       `json:"best_of_k_distance_improvements"`
	InitialProgressContinuation map[string]map[string]int `json:"initial_progress_continuation"`
	SemanticContinuation        map[string]map[string]int `json:"semantic_class_continuation"`
	FacetContinuation           map[string]map[string]int `json:"facet_continuation"`
	SelectedPerCampaign         map[string]int            `json:"selected_candidate_counts"`
	GeneratedAt                 string                    `json:"generated_at"`
}

type mutationAttemptDiagnostic struct {
	SchemaVersion                string                `json:"schema_version"`
	Attempt                      int                   `json:"attempt"`
	MutationSeed                 int64                 `json:"mutation_seed"`
	TargetNextWaypoint           string                `json:"target_next_waypoint"`
	AdvisorBasis                 string                `json:"advisor_basis"`
	RejectionLayer               string                `json:"rejection_layer"`
	ReasonCode                   string                `json:"reason_code"`
	FailedPrecondition           string                `json:"failed_precondition"`
	ProposalRaw                  *plan.PlanSequence    `json:"proposal_raw"`
	ProposalNormalized           *plan.PlanSequence    `json:"proposal_normalized"`
	Operator                     string                `json:"operator,omitempty"`
	PrefixLength                 int                   `json:"prefix_length"`
	MaxActionsPerPlan            int                   `json:"max_actions_per_plan"`
	PendingControls              pendingControlSummary `json:"pending_control_summary"`
	TheoreticalProtocolSuccessor bool                  `json:"theoretical_protocol_successor"`
	MutationProduced             bool                  `json:"mutation_produced"`
	Error                        string                `json:"error,omitempty"`
}

type diagnosisStateRow struct {
	Label                    string                `json:"label"`
	CampaignSeed             int64                 `json:"campaign_seed"`
	HandoffRank              int                   `json:"handoff_rank"`
	HandoffStableKey         string                `json:"handoff_stable_key"`
	ObservationDigest        string                `json:"observation_digest"`
	CompletedWaypoints       int                   `json:"completed_waypoints"`
	Distance                 int                   `json:"distance"`
	CurrentWaypoint          string                `json:"current_waypoint"`
	PrefixLength             int                   `json:"prefix_length"`
	RemainingPlanActions     int                   `json:"remaining_plan_actions"`
	RelativeSemanticClass    string                `json:"relative_semantic_class"`
	FacetSignature           string                `json:"facet_signature"`
	PendingControls          pendingControlSummary `json:"pending_control_summary"`
	OriginalMutationLegal    int                   `json:"original_mutation_legal,omitempty"`
	OriginalMutationAttempts int                   `json:"original_mutation_attempts,omitempty"`
}

func handoffProbeBenchmarkCommand(
	ctx context.Context, args []string, stdout, stderr io.Writer,
) error {
	flags := flag.NewFlagSet("modelfuzz-ng handoff-probe-benchmark", flag.ContinueOnError)
	flags.SetOutput(stderr)
	manifestPath := flags.String("manifest", "", "frozen handoff probe manifest")
	outputPath := flags.String("output", "", "isolated probe output root")
	skip := flags.Bool("skip-completed", true, "reuse validated probe-result.json files")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || *manifestPath == "" || *outputPath == "" {
		return fmt.Errorf("handoff-probe-benchmark requires -manifest and -output")
	}
	manifestAbs, err := filepath.Abs(*manifestPath)
	if err != nil {
		return err
	}
	manifest, err := readHandoffProbeManifest(manifestAbs)
	if err != nil {
		return err
	}
	if err := validateHandoffProbeManifest(manifest); err != nil {
		return err
	}
	if !filepath.IsAbs(manifest.Config) {
		manifest.Config = filepath.Join(filepath.Dir(manifestAbs), manifest.Config)
	}
	if !filepath.IsAbs(manifest.SourceBenchmarkRoot) {
		manifest.SourceBenchmarkRoot = filepath.Join(
			filepath.Dir(manifestAbs), manifest.SourceBenchmarkRoot)
	}
	manifest.SourceBenchmarkRoot, err = filepath.Abs(manifest.SourceBenchmarkRoot)
	if err != nil {
		return err
	}
	outputAbs, err := filepath.Abs(*outputPath)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(outputAbs, 0o755); err != nil {
		return err
	}
	if err := persistence.WriteJSONAtomic(filepath.Join(outputAbs, "manifest.json"), manifest); err != nil {
		return err
	}
	provenance := map[string]any{
		"schema_version":        handoffProbeManifestSchema + "-provenance-v1",
		"source_benchmark_root": manifest.SourceBenchmarkRoot,
		"global_corpus_access":  "read-only references; no corpus files copied",
		"frontier_isolation":    "one newly allocated capacity-1 Frontier per selected seed",
		"llm_calls":             0,
		"generated_at":          time.Now().UTC().Format(time.RFC3339Nano),
	}
	if err := persistence.WriteJSONAtomic(filepath.Join(outputAbs, "provenance.json"), provenance); err != nil {
		return err
	}

	results := make([]handoffProbeResult, 0, len(manifest.Seeds)*len(manifest.Goals)*manifest.HandoffTopK)
	for _, goalID := range manifest.Goals {
		for _, seed := range manifest.Seeds {
			if err := ctx.Err(); err != nil {
				return err
			}
			campaignResults, runErr := runHandoffProbeCampaign(
				ctx, outputAbs, manifest, goalID, seed, *skip, stderr)
			results = append(results, campaignResults...)
			if runErr != nil {
				return runErr
			}
			if _, err := fmt.Fprintf(stdout,
				"handoff-probe goal=%s seed=%d selected=%d complete\n",
				goalID, seed, len(campaignResults)); err != nil {
				return err
			}
		}
	}
	sortProbeResults(results)
	if err := writeJSONLines(filepath.Join(outputAbs, "probe-results.jsonl"), results); err != nil {
		return err
	}
	if err := writeProbeCSV(filepath.Join(outputAbs, "probe-results.csv"), results); err != nil {
		return err
	}
	summary := summarizeHandoffProbes(manifest, results)
	if err := persistence.WriteJSONAtomic(filepath.Join(outputAbs, "probe-summary.json"), summary); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(outputAbs, "probe-summary.md"),
		[]byte(renderProbeSummary(summary)), 0o644); err != nil {
		return err
	}
	retention := map[string]any{
		"schema_version": "raft-stage1-artifact-retention-v1",
		"kept": []string{
			"manifest/provenance", "per-rank settings and final reports",
			"StableKey and replay verification", "root JSONL/CSV/JSON/Markdown",
		},
		"global_corpus":         "referenced in place; never copied",
		"full_trace_policy":     "rank-1, posterior-best, no-progress, every goal/failure, and seed 9501 retained",
		"ordinary_probe_policy": "currently retained pending verified post-experiment compaction",
	}
	return persistence.WriteJSONAtomic(filepath.Join(outputAbs, "artifact-retention.json"), retention)
}

func runHandoffProbeCampaign(
	ctx context.Context,
	outputRoot string,
	manifest handoffProbeManifest,
	goalID goalsearch.GoalID,
	seed int64,
	skip bool,
	stderr io.Writer,
) ([]handoffProbeResult, error) {
	globalDir := filepath.Join(
		manifest.SourceBenchmarkRoot, "_global",
		string(breadthdepth.MethodFacetThen), fmt.Sprintf("seed-%d", seed))
	sourceCampaign := filepath.Join(
		manifest.SourceBenchmarkRoot, string(breadthdepth.MethodFacetThen),
		string(goalID), fmt.Sprintf("seed-%d", seed))
	config, err := loadCLIConfig(manifest.Config)
	if err != nil {
		return nil, err
	}
	config.TLC.Address = manifest.TLCAddress
	config.Engine.MaxPlanActions = manifest.MaxPlanActions
	config.Raft.Snapshot.Threshold = manifest.SnapshotThreshold
	config.Raft.Snapshot.RetainEntries = manifest.RetainEntries
	materialized, err := buildHandoffCandidates(
		ctx, globalDir, config, goalID, manifest.ReplayVerify, stderr)
	if err != nil {
		return nil, fmt.Errorf("materialize goal=%s seed=%d: %w", goalID, seed, err)
	}
	var frozen []breadthdepth.HandoffSeed
	if err := readJSONLines(filepath.Join(sourceCampaign, "handoff-candidates.jsonl"), &frozen); err != nil {
		return nil, fmt.Errorf("read frozen handoff candidates: %w", err)
	}
	if !equalJSON(frozen, materialized.Candidates) {
		validationDir := filepath.Join(outputRoot, "_input-validation",
			string(goalID), fmt.Sprintf("seed-%d", seed))
		if err := os.MkdirAll(validationDir, 0o755); err != nil {
			return nil, err
		}
		if err := writeJSONLines(
			filepath.Join(validationDir, "frozen-handoff-candidates.jsonl"), frozen); err != nil {
			return nil, err
		}
		if err := writeJSONLines(
			filepath.Join(validationDir, "recomputed-handoff-candidates.jsonl"),
			materialized.Candidates); err != nil {
			return nil, err
		}
		return nil, fmt.Errorf(
			"goal=%s seed=%d recomputed handoff candidates differ from frozen artifact; copies saved under %s",
			goalID, seed, validationDir)
	}
	selected, err := breadthdepth.SelectHandoff(
		string(goalID), materialized.Candidates, manifest.HandoffTopK)
	if err != nil {
		return nil, err
	}
	campaignDir := filepath.Join(outputRoot, string(goalID), fmt.Sprintf("seed-%d", seed))
	if err := os.MkdirAll(campaignDir, 0o755); err != nil {
		return nil, err
	}
	if err := persistence.WriteJSONAtomic(
		filepath.Join(campaignDir, "handoff-selected.json"), selected); err != nil {
		return nil, err
	}
	replayByCorpus := make(map[string]handoffReplayRecord, len(materialized.Replays))
	for _, replay := range materialized.Replays {
		replayByCorpus[replay.GlobalCorpusID] = replay
	}
	results := make([]handoffProbeResult, 0, len(selected.Selected))
	for _, candidate := range selected.Selected {
		rankDir := filepath.Join(campaignDir, fmt.Sprintf("rank-%02d", candidate.SelectionRank))
		resultPath := filepath.Join(rankDir, "probe-result.json")
		if skip {
			var existing handoffProbeResult
			if persistence.ReadJSON(resultPath, &existing) == nil &&
				existing.SchemaVersion == handoffProbeResultSchema &&
				existing.HandoffStableKey == candidate.StableKey &&
				existing.CampaignSeed == seed && existing.Goal == goalID {
				results = append(results, existing)
				continue
			}
		}
		frontierSeed, ok := materialized.Frontier[candidate.GlobalCorpusID]
		if !ok {
			result := initialProbeResult(seed, goalID, candidate)
			result.Outcome = "handoff_replay_failure"
			result.Error = "selected candidate lacks replayable Frontier seed"
			results = append(results, result)
			if err := os.MkdirAll(rankDir, 0o755); err != nil {
				return results, err
			}
			if err := persistence.WriteJSONAtomic(resultPath, result); err != nil {
				return results, err
			}
			continue
		}
		replay := replayByCorpus[candidate.GlobalCorpusID]
		result := initialProbeResult(seed, goalID, candidate)
		result.ReplayOK = replay.Succeeded && replay.TraceEqual &&
			replay.ObservationDigestEqual && replay.GoalProgressEqual
		started := time.Now()
		localManifest := probeLocalManifest(manifest)
		localBudget := breadthdepth.Budget{
			TotalCandidates: manifest.ProbeCandidateBudget,
			LocalCandidates: manifest.ProbeCandidateBudget,
			TotalActions:    manifest.ProbeActionBudget,
			LocalActions:    manifest.ProbeActionBudget,
			MaxPlanActions:  manifest.MaxPlanActions,
		}
		// The bootstrap contains exactly one copied seed. executeGoalSearch creates
		// a fresh Frontier inside this call, so no rank shares mutable local state.
		report, runErr := runBreadthDepthLocal(
			ctx, rankDir, localManifest, goalID, seed, localBudget,
			&goalSearchBootstrap{Seeds: []goalsearch.FrontierSeed{frontierSeed}}, stderr)
		result.WallTimeMillis = time.Since(started).Milliseconds()
		populateProbeOutcome(&result, report, runErr)
		if err := persistence.WriteJSONAtomic(resultPath, result); err != nil {
			return results, err
		}
		results = append(results, result)
	}
	return results, nil
}

func probeLocalManifest(manifest handoffProbeManifest) breadthDepthManifest {
	return breadthDepthManifest{
		Schema: breadthDepthBenchmarkSchema, Name: manifest.Name, Phase: manifest.Phase,
		Config: manifest.Config, TLCAddress: manifest.TLCAddress,
		MaxPlanActions:    manifest.MaxPlanActions,
		SnapshotThreshold: manifest.SnapshotThreshold, RetainEntries: manifest.RetainEntries,
		LocalFrontierCapacity: manifest.LocalFrontierCapacity,
		SaveAllRuns:           manifest.SaveAllRuns, ReplayVerify: manifest.ReplayVerify,
		StopOnTarget: true, HandoffTopK: 1, HandoffDiversity: true,
		HandoffFallback: true, FixedEnergy: 2, CorpusLimit: 128,
		InitialPopulation: 4, MaxReadyCandidates: 256,
	}
}

func initialProbeResult(
	seed int64, goalID goalsearch.GoalID, candidate breadthdepth.HandoffSeed,
) handoffProbeResult {
	return handoffProbeResult{
		SchemaVersion: handoffProbeResultSchema, CampaignSeed: seed, Goal: goalID,
		HandoffRank: candidate.SelectionRank, HandoffStableKey: candidate.StableKey,
		GlobalCorpusID:            candidate.GlobalCorpusID,
		InitialCompletedWaypoints: candidate.Progress.Completed,
		InitialDistance:           candidate.Progress.Distance,
		InitialTargetReached:      candidate.Progress.TargetReached,
		RelativeSemanticClass:     candidate.SemanticTraceDigest,
		FacetSignature:            candidate.FacetCombinationKey,
		FacetNoveltyCount:         candidate.FacetNoveltyCount,
		PrefixLength:              candidate.PlanPrefixLength,
		BestCompletedWaypoints:    candidate.Progress.Completed,
		BestDistance:              candidate.Progress.Distance,
		RejectionReasons:          make(map[string]int),
		PendingControlSummary:     summarizePendingControls(candidate.Observation),
	}
}

func populateProbeOutcome(
	result *handoffProbeResult, report goalSearchReport, runErr error,
) {
	result.MutationAttempted = report.Mutation.Attempts
	result.MutationLegal = report.ValidPlans
	result.MutationExecuted = report.Candidates
	result.Actions = report.Actions
	result.GoalReached = report.TargetReached || result.InitialTargetReached
	// The report includes the original bootstrap seed in its final snapshot.
	for _, seed := range report.Frontier.Seeds {
		progress := seed.Progress
		if progress.CompletedWaypointCount > result.BestCompletedWaypoints ||
			(progress.CompletedWaypointCount == result.BestCompletedWaypoints &&
				progress.DistanceToCurrent < result.BestDistance) {
			result.BestCompletedWaypoints = progress.CompletedWaypointCount
			result.BestDistance = progress.DistanceToCurrent
		}
	}
	if report.TargetReached {
		result.BestCompletedWaypoints = max(result.BestCompletedWaypoints, len(report.Waypoints))
		result.BestDistance = 0
	}
	result.DistanceDelta = result.InitialDistance - result.BestDistance
	result.NewWaypointReached =
		result.BestCompletedWaypoints > result.InitialCompletedWaypoints
	result.RejectionReasons = rejectionReasons(report, runErr)
	switch {
	case runErr != nil && strings.Contains(strings.ToLower(runErr.Error()), "tlc"):
		result.Outcome = "tlc_failure"
	case runErr != nil:
		result.Outcome = "candidate_execution_failure"
	case result.MutationLegal == 0:
		result.Outcome = "no_legal_mutation"
	case result.GoalReached:
		result.Outcome = "goal_reached"
	case result.NewWaypointReached || result.DistanceDelta > 0:
		result.Outcome = "progress"
	case result.MutationAttempted >= report.Settings.CandidateBudget:
		result.Outcome = "budget_exhausted"
	default:
		result.Outcome = "no_progress"
	}
	if runErr != nil {
		result.Error = runErr.Error()
	}
}

func rejectionReasons(report goalSearchReport, runErr error) map[string]int {
	result := make(map[string]int)
	if report.Mutation.RejectedMaxActions > 0 {
		result["budget_or_length_limit"] = report.Mutation.RejectedMaxActions
	}
	if report.Mutation.RejectedNoAction > 0 {
		result["advisor_no_legal_successor"] = report.Mutation.RejectedNoAction
	}
	for reason, count := range report.PrefixReplayFailures {
		result["prefix_incompatible:"+reason] += count
	}
	if report.Unexecutable > 0 {
		result["action_not_available"] += report.Unexecutable
	}
	known := report.Mutation.RejectedMaxActions + report.Mutation.RejectedNoAction +
		report.Unexecutable
	for _, count := range report.PrefixReplayFailures {
		known += count
	}
	if other := report.InvalidPlans - known; other > 0 {
		result["other"] += other
	}
	if runErr != nil {
		result["execution_error"]++
	}
	return result
}

func summarizePendingControls(observation core.Observation) pendingControlSummary {
	result := pendingControlSummary{MessageTypes: make(map[string]int)}
	for _, node := range observation.Nodes {
		if node.Status == core.NodeRunning {
			result.RunningNodes++
		} else if node.Status == core.NodeCrashed {
			result.CrashedNodes++
		}
	}
	for _, message := range observation.Messages {
		result.PendingMessages++
		if message.Blocked {
			result.BlockedMessages++
		}
		key := message.TypeHint
		if key == "" {
			key = "unknown"
		}
		result.MessageTypes[key]++
	}
	result.PartitionActive = observation.NetworkPartition != nil
	available := make([]string, 0, 10)
	if result.PendingMessages > 0 {
		available = append(available, string(plan.ActionDeliver),
			string(plan.ActionDrop), string(plan.ActionDuplicate))
	}
	if result.RunningNodes > 0 {
		available = append(available, string(plan.ActionAdvanceTicks),
			string(plan.ActionTimeout), string(plan.ActionRequest), string(plan.ActionCrash))
	}
	if result.CrashedNodes > 0 {
		available = append(available, string(plan.ActionRestart))
	}
	if result.PartitionActive {
		available = append(available, string(plan.ActionHeal))
	} else if result.RunningNodes > 1 {
		available = append(available, string(plan.ActionPartition))
	}
	sort.Strings(available)
	result.AvailableActionTypes = available
	result.NoControllableSuccessor = len(available) == 0
	return result
}

func probeResultLess(left, right handoffProbeResult) bool {
	if left.GoalReached != right.GoalReached {
		return left.GoalReached
	}
	if left.BestCompletedWaypoints != right.BestCompletedWaypoints {
		return left.BestCompletedWaypoints > right.BestCompletedWaypoints
	}
	if left.BestDistance != right.BestDistance {
		return left.BestDistance < right.BestDistance
	}
	if left.NewWaypointReached != right.NewWaypointReached {
		return left.NewWaypointReached
	}
	if left.MutationLegal != right.MutationLegal {
		return left.MutationLegal > right.MutationLegal
	}
	if left.MutationExecuted != right.MutationExecuted {
		return left.MutationExecuted > right.MutationExecuted
	}
	if left.Actions != right.Actions {
		return left.Actions < right.Actions
	}
	return left.HandoffStableKey < right.HandoffStableKey
}

func sortProbeResults(results []handoffProbeResult) {
	sort.Slice(results, func(i, j int) bool {
		if results[i].Goal != results[j].Goal {
			return results[i].Goal < results[j].Goal
		}
		if results[i].CampaignSeed != results[j].CampaignSeed {
			return results[i].CampaignSeed < results[j].CampaignSeed
		}
		return results[i].HandoffRank < results[j].HandoffRank
	})
}

func summarizeHandoffProbes(
	manifest handoffProbeManifest, results []handoffProbeResult,
) handoffProbeSummary {
	summary := handoffProbeSummary{
		SchemaVersion: handoffProbeResultSchema + "-summary",
		Name:          manifest.Name, Results: len(results),
		RankOneTopOneRegret:         make(map[string]int),
		RankLegalRates:              make(map[string]float64),
		InitialProgressContinuation: make(map[string]map[string]int),
		SemanticContinuation:        make(map[string]map[string]int),
		FacetContinuation:           make(map[string]map[string]int),
		SelectedPerCampaign:         make(map[string]int),
		GeneratedAt:                 time.Now().UTC().Format(time.RFC3339Nano),
	}
	groups := make(map[string][]handoffProbeResult)
	rankAttempted := make(map[int]int)
	rankLegal := make(map[int]int)
	for _, result := range results {
		key := string(result.Goal) + "/" + strconv.FormatInt(result.CampaignSeed, 10)
		groups[key] = append(groups[key], result)
		rankAttempted[result.HandoffRank] += result.MutationAttempted
		rankLegal[result.HandoffRank] += result.MutationLegal
		recordContinuation(summary.InitialProgressContinuation,
			fmt.Sprintf("%d/%d", result.InitialCompletedWaypoints, result.InitialDistance), result)
		recordContinuation(summary.SemanticContinuation, result.RelativeSemanticClass, result)
		recordContinuation(summary.FacetContinuation, result.FacetSignature, result)
	}
	spearmanSum := 0.0
	for key, group := range groups {
		summary.Campaigns++
		summary.SelectedPerCampaign[key] = len(group)
		posterior := append([]handoffProbeResult(nil), group...)
		sort.Slice(posterior, func(i, j int) bool { return probeResultLess(posterior[i], posterior[j]) })
		posteriorPosition := make(map[string]int, len(posterior))
		for index := range posterior {
			posterior[index].PosteriorRank = index + 1
			posteriorPosition[posterior[index].HandoffStableKey] = index + 1
		}
		var rankOne handoffProbeResult
		for _, result := range group {
			if result.HandoffRank == 1 {
				rankOne = result
				break
			}
		}
		best := posterior[0]
		regret := posteriorPosition[rankOne.HandoffStableKey] - 1
		summary.RankOneTopOneRegret[strconv.Itoa(regret)]++
		if regret == 0 {
			summary.RankOnePosteriorBest++
		} else {
			summary.UnselectedStrictlyBetter++
		}
		if rankOne.GoalReached {
			summary.RankOneGoalReach++
		}
		if best.GoalReached {
			summary.BestOfKGoalReach++
		}
		if best.BestCompletedWaypoints > rankOne.BestCompletedWaypoints {
			summary.BestOfKWaypointImprovements++
		}
		if best.BestCompletedWaypoints == rankOne.BestCompletedWaypoints &&
			best.BestDistance < rankOne.BestDistance {
			summary.BestOfKDistanceImprovements++
		}
		spearmanSum += spearmanForGroup(group, posteriorPosition)
	}
	if summary.Campaigns > 0 {
		summary.StaticPosteriorSpearmanMean = spearmanSum / float64(summary.Campaigns)
	}
	for rank, attempted := range rankAttempted {
		rate := 0.0
		if attempted > 0 {
			rate = float64(rankLegal[rank]) / float64(attempted)
		}
		summary.RankLegalRates[strconv.Itoa(rank)] = rate
	}
	return summary
}

func recordContinuation(
	target map[string]map[string]int, key string, result handoffProbeResult,
) {
	if key == "" {
		key = "none"
	}
	if target[key] == nil {
		target[key] = map[string]int{"probes": 0, "legal": 0, "progress": 0, "goal": 0}
	}
	target[key]["probes"]++
	target[key]["legal"] += result.MutationLegal
	if result.NewWaypointReached || result.DistanceDelta > 0 {
		target[key]["progress"]++
	}
	if result.GoalReached {
		target[key]["goal"]++
	}
}

func spearmanForGroup(
	group []handoffProbeResult, posterior map[string]int,
) float64 {
	n := len(group)
	if n < 2 {
		return 0
	}
	sum := 0.0
	for _, result := range group {
		d := float64(result.HandoffRank - posterior[result.HandoffStableKey])
		sum += d * d
	}
	return 1 - (6*sum)/(float64(n)*(float64(n*n)-1))
}

func writeProbeCSV(path string, results []handoffProbeResult) error {
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	writer := csv.NewWriter(file)
	rows := [][]string{{
		"campaign_seed", "goal", "handoff_rank", "handoff_stable_key",
		"initial_completed_waypoints", "initial_distance", "prefix_length",
		"replay_ok", "mutation_attempted", "mutation_legal", "mutation_executed",
		"best_completed_waypoints", "best_distance", "distance_delta",
		"new_waypoint_reached", "goal_reached", "actions", "wall_time_ms", "outcome",
	}}
	for _, result := range results {
		rows = append(rows, []string{
			strconv.FormatInt(result.CampaignSeed, 10), string(result.Goal),
			strconv.Itoa(result.HandoffRank), result.HandoffStableKey,
			strconv.Itoa(result.InitialCompletedWaypoints), strconv.Itoa(result.InitialDistance),
			strconv.Itoa(result.PrefixLength), strconv.FormatBool(result.ReplayOK),
			strconv.Itoa(result.MutationAttempted), strconv.Itoa(result.MutationLegal),
			strconv.Itoa(result.MutationExecuted), strconv.Itoa(result.BestCompletedWaypoints),
			strconv.Itoa(result.BestDistance), strconv.Itoa(result.DistanceDelta),
			strconv.FormatBool(result.NewWaypointReached), strconv.FormatBool(result.GoalReached),
			strconv.Itoa(result.Actions), strconv.FormatInt(result.WallTimeMillis, 10), result.Outcome,
		})
	}
	for _, row := range rows {
		if err := writer.Write(row); err != nil {
			_ = file.Close()
			return err
		}
	}
	writer.Flush()
	return errors.Join(writer.Error(), file.Close())
}

func renderProbeSummary(summary handoffProbeSummary) string {
	return fmt.Sprintf(`# Handoff Top-K counterfactual probe

- Schema: %s
- Campaigns: %d
- Probe results: %d
- Rank-1 posterior best: %d/%d
- Campaigns with an unselected seed posterior-better than rank-1: %d/%d
- Rank-1 Goal reach: %d/%d
- Best-of-K Goal reach: %d/%d
- Best-of-K waypoint improvements: %d
- Best-of-K same-waypoint distance improvements: %d
- Mean per-campaign Spearman(static rank, posterior rank): %.6f

The posterior order is deterministic and lexicographic: Goal reach, completed
waypoints, staged Distance, new waypoint, legal/executed candidates, fewer
actions, then StableKey. It is not a unified floating-point score.
`,
		summary.SchemaVersion, summary.Campaigns, summary.Results,
		summary.RankOnePosteriorBest, summary.Campaigns,
		summary.UnselectedStrictlyBetter, summary.Campaigns,
		summary.RankOneGoalReach, summary.Campaigns,
		summary.BestOfKGoalReach, summary.Campaigns,
		summary.BestOfKWaypointImprovements, summary.BestOfKDistanceImprovements,
		summary.StaticPosteriorSpearmanMean)
}

func readHandoffProbeManifest(path string) (handoffProbeManifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return handoffProbeManifest{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var manifest handoffProbeManifest
	if err := decoder.Decode(&manifest); err != nil {
		return manifest, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return manifest, fmt.Errorf("handoff probe manifest has trailing JSON")
	}
	return manifest, nil
}

func validateHandoffProbeManifest(manifest handoffProbeManifest) error {
	if manifest.Schema != handoffProbeManifestSchema {
		return fmt.Errorf("unsupported handoff probe schema %q", manifest.Schema)
	}
	if manifest.Name == "" || manifest.Phase == "" || manifest.Config == "" ||
		manifest.SourceBenchmarkRoot == "" || len(manifest.Goals) == 0 ||
		len(manifest.Seeds) == 0 {
		return fmt.Errorf("handoff probe manifest lacks required identity or inputs")
	}
	if manifest.HandoffTopK != 8 || manifest.ProbeCandidateBudget != 5 ||
		manifest.ProbeActionBudget != 900 || manifest.MaxPlanActions != 180 ||
		manifest.SnapshotThreshold != 3 || manifest.RetainEntries != 1 ||
		manifest.LocalFrontierCapacity != 1 || !manifest.SaveAllRuns ||
		!manifest.ReplayVerify {
		return fmt.Errorf("stage1 probe requires frozen K=8, 5/900/180, snapshot=3/retain=1, capacity=1, save/replay settings")
	}
	seenGoals := make(map[goalsearch.GoalID]bool)
	for _, goalID := range manifest.Goals {
		if _, err := goalsearch.Definition(goalID, 3); err != nil {
			return err
		}
		if seenGoals[goalID] {
			return fmt.Errorf("duplicate goal %s", goalID)
		}
		seenGoals[goalID] = true
	}
	seenSeeds := make(map[int64]bool)
	for _, seed := range manifest.Seeds {
		if seenSeeds[seed] {
			return fmt.Errorf("duplicate seed %d", seed)
		}
		seenSeeds[seed] = true
	}
	return nil
}

func handoffDiagnoseCommand(
	ctx context.Context, args []string, stdout, stderr io.Writer,
) error {
	flags := flag.NewFlagSet("modelfuzz-ng handoff-diagnose", flag.ContinueOnError)
	flags.SetOutput(stderr)
	source := flags.String("source", "", "formal breadth/depth benchmark root")
	output := flags.String("output", "", "new diagnosis output directory")
	configPath := flags.String("config", "examples/config-facet-guidance-control.json", "frozen config")
	tlcAddress := flags.String("tlc", "http://127.0.0.1:2027", "strict TLC address")
	goalText := flags.String("goal", string(goalsearch.GoalSnapshotCatchUpAfterPartition), "goal ID")
	seed := flags.Int64("seed", 9501, "diagnosed campaign seed")
	controlSeed := flags.Int64("control-seed", 9502, "successful same-goal M5 control")
	probeRoot := flags.String("probe-root", "", "optional completed Top-K probe root")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || *source == "" || *output == "" {
		return fmt.Errorf("handoff-diagnose requires -source and -output")
	}
	goalID := goalsearch.GoalID(*goalText)
	if _, err := goalsearch.Definition(goalID, 3); err != nil {
		return err
	}
	sourceAbs, err := filepath.Abs(*source)
	if err != nil {
		return err
	}
	outputAbs, err := filepath.Abs(*output)
	if err != nil {
		return err
	}
	configAbs, err := filepath.Abs(*configPath)
	if err != nil {
		return err
	}
	if err := createOutputDirectory(outputAbs); err != nil {
		return err
	}
	config, err := loadCLIConfig(configAbs)
	if err != nil {
		return err
	}
	config.TLC.Address = *tlcAddress
	config.Engine.MaxPlanActions = 180
	config.Raft.Snapshot.Threshold = 3
	config.Raft.Snapshot.RetainEntries = 1
	globalDir := filepath.Join(sourceAbs, "_global", string(breadthdepth.MethodFacetThen),
		fmt.Sprintf("seed-%d", *seed))
	sourceCampaign := filepath.Join(sourceAbs, string(breadthdepth.MethodFacetThen),
		string(goalID), fmt.Sprintf("seed-%d", *seed))
	materialized, err := buildHandoffCandidates(
		ctx, globalDir, config, goalID, true, stderr)
	if err != nil {
		return err
	}
	selected, err := breadthdepth.SelectHandoff(string(goalID), materialized.Candidates, 8)
	if err != nil {
		return err
	}
	if len(selected.Selected) == 0 {
		return fmt.Errorf("diagnosed campaign has no eligible handoff seed")
	}
	rankOne := selected.Selected[0]
	frontierSeed, ok := materialized.Frontier[rankOne.GlobalCorpusID]
	if !ok {
		return fmt.Errorf("rank-1 seed %s has no replayable Frontier materialization", rankOne.StableKey)
	}
	var frozenSelected breadthdepth.HandoffSet
	if err := persistence.ReadJSON(
		filepath.Join(sourceCampaign, "handoff-selected.json"), &frozenSelected); err != nil {
		return err
	}
	if len(frozenSelected.Selected) != 1 ||
		frozenSelected.Selected[0].StableKey != rankOne.StableKey {
		return fmt.Errorf("diagnosed rank-1 StableKey differs from frozen formal selection")
	}
	replay := handoffReplayRecord{}
	for _, record := range materialized.Replays {
		if record.GlobalCorpusID == rankOne.GlobalCorpusID {
			replay = record
			break
		}
	}
	observationDigest := lastObservationDigest(rankOne.Trace)
	replayVerification := map[string]any{
		"schema_version":  handoffDiagnosisSchema + "-replay",
		"source_campaign": sourceCampaign,
		"campaign_seed":   *seed, "goal": goalID,
		"selected_stable_key":        rankOne.StableKey,
		"frozen_selected_stable_key": frozenSelected.Selected[0].StableKey,
		"observation_digest":         observationDigest,
		"goal_progress":              rankOne.Progress,
		"facet_signature":            rankOne.FacetCombinationKey,
		"prefix_length":              rankOne.PlanPrefixLength,
		"max_actions_per_plan":       180,
		"replay":                     replay,
		"verified": replay.Succeeded && replay.TraceEqual &&
			replay.ObservationDigestEqual && replay.GoalProgressEqual && replay.FacetEqual,
	}
	if err := persistence.WriteJSONAtomic(
		filepath.Join(outputAbs, "replay-verification.json"), replayVerification); err != nil {
		return err
	}

	reproductionManifest := breadthDepthManifest{
		Schema: breadthDepthBenchmarkSchema, Name: "seed-9501-diagnosis",
		Phase: "stage1", Config: configAbs, TLCAddress: *tlcAddress,
		MaxPlanActions: 180, SnapshotThreshold: 3, RetainEntries: 1,
		LocalFrontierCapacity: 1, SaveAllRuns: true, ReplayVerify: true,
		StopOnTarget: true, HandoffTopK: 1, HandoffDiversity: true,
		HandoffFallback: true, FixedEnergy: 2, CorpusLimit: 128,
		InitialPopulation: 4, MaxReadyCandidates: 256,
	}
	localBudget := breadthdepth.Budget{
		TotalCandidates: 30, LocalCandidates: 30,
		TotalActions: 5400, LocalActions: 5400, MaxPlanActions: 180,
	}
	reproductionReport, runErr := runBreadthDepthLocal(
		ctx, filepath.Join(outputAbs, "reproduction-local"), reproductionManifest,
		goalID, *seed, localBudget,
		&goalSearchBootstrap{Seeds: []goalsearch.FrontierSeed{frontierSeed}}, stderr)
	if runErr != nil {
		return fmt.Errorf("diagnostic reproduction: %w", runErr)
	}

	attempts, err := diagnoseMutationAttempts(goalID, *seed, frontierSeed, 30, 180)
	if err != nil {
		return err
	}
	if err := writeJSONLines(filepath.Join(outputAbs, "mutation-attempts.jsonl"), attempts); err != nil {
		return err
	}
	reasons := make(map[string]int)
	layers := make(map[string]int)
	for _, attempt := range attempts {
		reasons[attempt.ReasonCode]++
		layers[attempt.RejectionLayer]++
	}
	reproductionStable := reproductionReport.Mutation.Attempts == 30 &&
		reproductionReport.Mutation.RejectedMaxActions == 30 &&
		reproductionReport.Candidates == 0 && reproductionReport.ValidPlans == 0 &&
		reproductionReport.InvalidPlans == 30 && reproductionReport.TLCExecutedRuns == 0
	rejectionSummary := map[string]any{
		"schema_version": handoffDiagnosisSchema + "-rejections",
		"attempts":       len(attempts), "reason_codes": reasons, "layers": layers,
		"reason_code_taxonomy": []string{
			"schema_invalid", "normalization_failed", "prefix_incompatible",
			"action_not_available", "precondition_unsatisfied",
			"fault_model_violation", "duplicate_or_noop",
			"budget_or_length_limit", "advisor_no_legal_successor", "other",
		},
		"original_expected": map[string]int{
			"attempts": 30, "invalid": 30, "executed": 0, "rejected_max_actions": 30,
		},
		"reproduction": map[string]any{
			"mutation":     reproductionReport.Mutation,
			"valid":        reproductionReport.ValidPlans,
			"invalid":      reproductionReport.InvalidPlans,
			"executed":     reproductionReport.Candidates,
			"tlc_executed": reproductionReport.TLCExecutedRuns,
		},
		"stable_reproduction": reproductionStable,
	}
	if err := persistence.WriteJSONAtomic(
		filepath.Join(outputAbs, "rejection-summary.json"), rejectionSummary); err != nil {
		return err
	}

	rows, err := diagnosisComparisonRows(
		ctx, sourceAbs, config, goalID, *seed, *controlSeed, selected,
		materialized, *probeRoot, stderr)
	if err != nil {
		return err
	}
	comparison := map[string]any{
		"schema_version": handoffDiagnosisSchema + "-state-comparison",
		"rows":           rows,
		"comparison_dimensions": []string{
			"initial waypoint/distance", "pending controls", "prefix capacity",
			"semantic class", "Facet signature", "observed legal mutation rate",
		},
	}
	if err := persistence.WriteJSONAtomic(
		filepath.Join(outputAbs, "state-comparison.json"), comparison); err != nil {
		return err
	}
	minimal := map[string]any{
		"schema_version": handoffDiagnosisSchema + "-minimal-reproducer",
		"goal":           goalID, "campaign_seed": *seed,
		"handoff_stable_key":      rankOne.StableKey,
		"global_corpus_id":        rankOne.GlobalCorpusID,
		"observation_digest":      observationDigest,
		"prefix_length":           len(frontierSeed.PrefixPlan.Actions),
		"prefix_end_action_index": frontierSeed.PrefixPlanEnd,
		"max_actions_per_plan":    180,
		"prefix_preservation":     true,
		"mutation_seed":           *seed,
		"failed_precondition":     "len(prefix) < max_actions_per_plan",
		"actual_expression": fmt.Sprintf("%d < %d == false",
			len(frontierSeed.PrefixPlan.Actions), 180),
		"error":       attempts[0].Error,
		"reason_code": attempts[0].ReasonCode,
		"reproduce_command": fmt.Sprintf(
			"modelfuzz-ng handoff-diagnose -source %s -output NEW_DIR -config %s -goal %s -seed %d -control-seed %d -tlc %s",
			sourceAbs, configAbs, goalID, *seed, *controlSeed, *tlcAddress),
	}
	if err := persistence.WriteJSONAtomic(
		filepath.Join(outputAbs, "minimal-reproducer.json"), minimal); err != nil {
		return err
	}
	diagnosis := renderDiagnosis(
		goalID, *seed, rankOne, reproductionReport, reproductionStable, rows)
	if err := os.WriteFile(filepath.Join(outputAbs, "diagnosis.md"), []byte(diagnosis), 0o644); err != nil {
		return err
	}
	retention := map[string]any{
		"schema_version": "raft-stage1-artifact-retention-v1",
		"kept":           "all seed-9501 diagnosis artifacts and full reproduction-local data",
		"discarded":      []string{},
	}
	if err := persistence.WriteJSONAtomic(
		filepath.Join(outputAbs, "artifact-retention.json"), retention); err != nil {
		return err
	}
	_, err = fmt.Fprintf(stdout,
		"handoff diagnosis goal=%s seed=%d stable_reproduction=%t root_cause=prefix_length_budget_interface\n",
		goalID, *seed, reproductionStable)
	return err
}

func diagnoseMutationAttempts(
	goalID goalsearch.GoalID,
	campaignSeed int64,
	seed goalsearch.FrontierSeed,
	count, maxActions int,
) ([]mutationAttemptDiagnostic, error) {
	definition, err := goalsearch.Definition(goalID, 3)
	if err != nil {
		return nil, err
	}
	advisor, err := raftadvisor.New(raftadvisor.Config{
		GoalAEnabled: true, GoalBEnabled: true,
		PriorityMultiplier: 16, LocalActionCap: 9,
		NoProgressCap: 8, QueueLimit: 64, Ablation: raftadvisor.AblationNone,
	})
	if err != nil {
		return nil, err
	}
	evaluation := goalsearch.EvaluationResult{
		Instance: seed.Instance, PrefixEndActionIndex: seed.PrefixPlanEnd,
		FinalObservation:  seed.PrefixObservation,
		PrefixObservation: seed.PrefixObservation,
	}
	evaluation.Instance.Progress = seed.Progress
	evaluation.Instance.Bindings = seed.Bindings
	results := make([]mutationAttemptDiagnostic, 0, count)
	for index := 0; index < count; index++ {
		mutationSeed := campaignSeed + int64(index)*7919
		options := goalsearch.MutationOptions{
			HintStrength: goalsearch.HintWeak, PreservePrefix: true,
			Advisor: protocolmutation.Advisor(advisor), AdvisorCandidateIndex: index,
		}
		mutated, stats, mutateErr := goalsearch.MutateTowardWaypointWithOptions(
			definition, seed.PrefixPlan, evaluation, mutationSeed, maxActions, options)
		reason, layer, precondition := classifyMutationRejection(stats, mutateErr)
		attempt := mutationAttemptDiagnostic{
			SchemaVersion: handoffDiagnosisSchema + "-attempt",
			Attempt:       index + 1, MutationSeed: mutationSeed,
			TargetNextWaypoint: seed.Progress.CurrentWaypointID,
			AdvisorBasis:       "focused Advisor is configured, but plan-length precheck runs before Advisor.Advise",
			RejectionLayer:     layer, ReasonCode: reason, FailedPrecondition: precondition,
			PrefixLength: len(seed.PrefixPlan.Actions), MaxActionsPerPlan: maxActions,
			PendingControls:              summarizePendingControls(seed.PrefixObservation),
			TheoreticalProtocolSuccessor: !summarizePendingControls(seed.PrefixObservation).NoControllableSuccessor,
			MutationProduced:             mutateErr == nil,
		}
		if mutateErr != nil {
			attempt.Error = mutateErr.Error()
		} else {
			raw := mutated.Plan.Copy()
			normalized := mutated.Plan.Copy()
			attempt.ProposalRaw = &raw
			attempt.ProposalNormalized = &normalized
			attempt.Operator = mutated.Operator
		}
		results = append(results, attempt)
	}
	return results, nil
}

func classifyMutationRejection(
	stats goalsearch.MutationStats, err error,
) (reason, layer, precondition string) {
	switch {
	case err == nil:
		return "", "accepted", ""
	case stats.RejectedMaxActions > 0:
		return "budget_or_length_limit", "pre_advisor_plan_length",
			"preserved prefix length must be strictly less than max actions per plan"
	case stats.RejectedNoAction > 0:
		return "advisor_no_legal_successor", "advisor_or_waypoint_generation",
			"mutation generator must return at least one legal local action"
	case strings.Contains(err.Error(), "valid progress prefix"):
		return "prefix_incompatible", "prefix_validation",
			"progress prefix end must index the parent plan"
	case strings.Contains(err.Error(), "whole-plan"):
		return "prefix_incompatible", "mutation_options",
			"whole-plan mutation and prefix preservation are mutually exclusive"
	case strings.Contains(err.Error(), "max actions"):
		return "budget_or_length_limit", "mutation_budget", "max actions must be positive"
	default:
		return "other", "mutation_generation", err.Error()
	}
}

func diagnosisComparisonRows(
	ctx context.Context,
	sourceRoot string,
	config cliConfig,
	goalID goalsearch.GoalID,
	diagnosedSeed, controlSeed int64,
	selected breadthdepth.HandoffSet,
	diagnosed materializedHandoff,
	probeRoot string,
	stderr io.Writer,
) ([]diagnosisStateRow, error) {
	rows := make([]diagnosisStateRow, 0, 4)
	for index, candidate := range selected.Selected {
		if index > 2 {
			break
		}
		label := fmt.Sprintf("seed-%d-rank-%d", diagnosedSeed, index+1)
		row := candidateStateRow(label, diagnosedSeed, candidate)
		if probeRoot != "" {
			var probe handoffProbeResult
			path := filepath.Join(probeRoot, string(goalID),
				fmt.Sprintf("seed-%d", diagnosedSeed), fmt.Sprintf("rank-%02d", index+1),
				"probe-result.json")
			if persistence.ReadJSON(path, &probe) == nil {
				row.OriginalMutationAttempts = probe.MutationAttempted
				row.OriginalMutationLegal = probe.MutationLegal
			}
		}
		rows = append(rows, row)
	}
	controlGlobal := filepath.Join(sourceRoot, "_global", string(breadthdepth.MethodFacetThen),
		fmt.Sprintf("seed-%d", controlSeed))
	controlMaterialized, err := buildHandoffCandidates(
		ctx, controlGlobal, config, goalID, true, stderr)
	if err != nil {
		return nil, err
	}
	controlSelected, err := breadthdepth.SelectHandoff(
		string(goalID), controlMaterialized.Candidates, 1)
	if err != nil || len(controlSelected.Selected) == 0 {
		return nil, errors.Join(err, fmt.Errorf("control seed has no selected handoff"))
	}
	control := controlSelected.Selected[0]
	controlRow := candidateStateRow(
		fmt.Sprintf("successful-control-seed-%d-rank-1", controlSeed), controlSeed, control)
	var originalReport goalSearchReport
	if persistence.ReadJSON(filepath.Join(sourceRoot, string(breadthdepth.MethodFacetThen),
		string(goalID), fmt.Sprintf("seed-%d", controlSeed), "local", "final-report.json"),
		&originalReport) == nil {
		controlRow.OriginalMutationAttempts = originalReport.Mutation.Attempts
		controlRow.OriginalMutationLegal = originalReport.ValidPlans
	}
	rows = append(rows, controlRow)
	_ = diagnosed
	return rows, nil
}

func candidateStateRow(
	label string, seed int64, candidate breadthdepth.HandoffSeed,
) diagnosisStateRow {
	return diagnosisStateRow{
		Label: label, CampaignSeed: seed, HandoffRank: candidate.SelectionRank,
		HandoffStableKey:      candidate.StableKey,
		ObservationDigest:     lastObservationDigest(candidate.Trace),
		CompletedWaypoints:    candidate.Progress.Completed,
		Distance:              candidate.Progress.Distance,
		CurrentWaypoint:       candidate.Progress.CurrentWaypoint,
		PrefixLength:          candidate.PlanPrefixLength,
		RemainingPlanActions:  180 - candidate.PlanPrefixLength,
		RelativeSemanticClass: candidate.SemanticTraceDigest,
		FacetSignature:        candidate.FacetCombinationKey,
		PendingControls:       summarizePendingControls(candidate.Observation),
	}
}

func lastObservationDigest(trace core.Trace) string {
	if len(trace.Steps) == 0 {
		return ""
	}
	return trace.Steps[len(trace.Steps)-1].ObservationDigest
}

func renderDiagnosis(
	goalID goalsearch.GoalID,
	seed int64,
	selected breadthdepth.HandoffSeed,
	report goalSearchReport,
	stable bool,
	rows []diagnosisStateRow,
) string {
	controlText := "not available"
	if len(rows) > 0 {
		control := rows[len(rows)-1]
		controlText = fmt.Sprintf(
			"%s: prefix=%d remaining=%d completed=%d distance=%d legal=%d/%d",
			control.Label, control.PrefixLength, control.RemainingPlanActions,
			control.CompletedWaypoints, control.Distance,
			control.OriginalMutationLegal, control.OriginalMutationAttempts)
	}
	return fmt.Sprintf(`# Seed %d Handoff diagnosis

## Reproduction

- Goal: %s
- Selected StableKey: %s
- Prefix length: %d
- Max actions per Plan: 180
- Stable reproduction of 30 rejected / 0 executed: %t
- Reproduced mutation attempts: %d
- Rejected at max-actions precheck: %d
- Valid candidates: %d
- Executed candidates/TLC runs: %d/%d

## Root cause

This is a deterministic Handoff–local-search length-budget interface defect.
Prefix preservation requires the local mutation to append an action, while the
rank-1 Handoff prefix already contains 180 PlanActions, exactly the frozen
per-Plan maximum. MutateTowardWaypointWithOptions rejects at its pre-Advisor
length check, so the focused Advisor is never invoked and strict TLC has no
candidate to execute.

It is not evidence that the protocol state is a dead-end: the compact state
snapshot exposes controllable protocol actions. It is not a TLC, Oracle,
Mapper, action-legality, or statistics-only failure. The legality policy is
behaving as implemented; the incompatible Handoff prefix was admitted without
checking remaining local append capacity.

Successful same-Goal control: %s.

## Recommendation (not applied)

For the next bounded correction, add remaining append capacity as a Handoff
eligibility precondition (or reserve a documented local suffix budget) before
static ranking. Keep the frozen Goal, Waypoints, staged Distance, prefix
preservation, focused Advisor, TLC, Mapper, and Oracle semantics unchanged.
`,
		seed, goalID, selected.StableKey, selected.PlanPrefixLength, stable,
		report.Mutation.Attempts, report.Mutation.RejectedMaxActions,
		report.ValidPlans, report.Candidates, report.TLCExecutedRuns, controlText)
}
