package main

import (
	"encoding/csv"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/goalsearch"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/persistence"
)

type goalComparisonGroup struct {
	GoalID                      goalsearch.GoalID                  `json:"goal_id"`
	Mode                        goalsearch.SearchMode              `json:"search_mode"`
	HintStrength                goalsearch.HintStrength            `json:"hint_strength"`
	FrontierTopK                int                                `json:"frontier_top_k"`
	TotalFrontierCapacity       int                                `json:"total_frontier_capacity"`
	BranchTemplateIDs           []goalsearch.BranchTemplateID      `json:"branch_template_ids,omitempty"`
	BranchAwareness             goalsearch.BranchAwareness         `json:"branch_awareness,omitempty"`
	BranchDimensionAblation     goalsearch.BranchDimensionAblation `json:"branch_dimension_ablation,omitempty"`
	BranchEvidenceMode          goalsearch.BranchEvidenceMode      `json:"branch_evidence_mode"`
	BranchFrontierMode          string                             `json:"branch_frontier_mode"`
	BranchBudgetMode            goalsearch.BranchBudgetMode        `json:"branch_budget_mode"`
	MicroProgressPolicy         goalsearch.MicroProgressPolicy     `json:"micro_progress_policy"`
	EvidencePriorityMultiplier  int                                `json:"evidence_priority_multiplier"`
	PrefixPreservation          bool                               `json:"prefix_preservation"`
	DistanceMode                goalsearch.DistanceMode            `json:"distance_mode"`
	Subject                     string                             `json:"subject"`
	Seeds                       []int64                            `json:"seeds"`
	Runs                        int                                `json:"runs"`
	SuccessfulSeeds             int                                `json:"successful_seeds"`
	FailedSeeds                 int                                `json:"failed_seeds"`
	GoalReachRate               float64                            `json:"goal_reach_rate"`
	GoalReachWilsonLow          float64                            `json:"goal_reach_wilson_low"`
	GoalReachWilsonHigh         float64                            `json:"goal_reach_wilson_high"`
	BugDetectedSeeds            int                                `json:"bug_detected_seeds"`
	BugDetectionRate            float64                            `json:"bug_detection_rate"`
	BugDetectionWilsonLow       float64                            `json:"bug_detection_wilson_low"`
	BugDetectionWilsonHigh      float64                            `json:"bug_detection_wilson_high"`
	FalsePositiveRate           float64                            `json:"false_positive_rate"`
	MeanExecutedCandidates      float64                            `json:"mean_executed_candidates"`
	MeanExecutedActions         float64                            `json:"mean_executed_actions"`
	MeanElapsedMilliseconds     float64                            `json:"mean_elapsed_milliseconds"`
	WaypointReachRates          map[string]float64                 `json:"waypoint_reach_rates"`
	WaypointTransitionRates     map[string]float64                 `json:"waypoint_transition_success_rates"`
	MeanAttemptsToNext          map[string]float64                 `json:"mean_attempts_to_next_waypoint"`
	MostCommonStalledWaypoint   string                             `json:"most_common_stalled_waypoint"`
	OnlineOfflineMismatches     int                                `json:"online_offline_mismatches"`
	ExpectedOfflineMapFailures  int                                `json:"expected_offline_mapping_failures"`
	PrefixReplayAttempts        int                                `json:"prefix_replay_attempts"`
	PrefixReplaySuccess         int                                `json:"prefix_replay_success"`
	PrefixExecutionMismatch     int                                `json:"prefix_execution_mismatches"`
	FrontierInserted            int                                `json:"frontier_inserted"`
	FrontierReplaced            int                                `json:"frontier_replaced"`
	FrontierEvicted             int                                `json:"frontier_evicted"`
	FrontierSeedSelections      int                                `json:"frontier_seed_selections"`
	WaypointRegressions         int                                `json:"waypoint_regressions"`
	CompletedDestroyed          int                                `json:"completed_waypoints_destroyed"`
	NewFacetWithoutGoalProgress int                                `json:"new_facet_without_goal_progress"`
	GoalProgressWithoutNewFacet int                                `json:"goal_progress_without_new_facet"`
	NewWaypointWithoutNewFacet  int                                `json:"new_waypoint_without_new_facet"`
	DistanceWithoutNewFacet     int                                `json:"distance_improvement_without_new_facet"`
	PlannedBranchCount          int                                `json:"planned_branch_count"`
	RealizedBranchCount         int                                `json:"realized_branch_count"`
	SuccessfulBranchCount       int                                `json:"successful_branch_count"`
	BranchDeviations            int                                `json:"branch_deviations"`
	NewBranchWithoutNewFacet    int                                `json:"new_branch_without_new_facet"`
	NewFacetWithoutNewBranch    int                                `json:"new_facet_without_new_branch"`
	SupportedBranchCount        int                                `json:"supported_branch_count"`
	CommittedBranchCount        int                                `json:"committed_branch_count"`
	FullRealizedCount           int                                `json:"full_realized_count"`
	ContradictedCount           int                                `json:"contradicted_count"`
	BudgetGranted               int                                `json:"branch_budget_granted"`
	BudgetUsed                  int                                `json:"branch_budget_used"`
	BudgetUnused                int                                `json:"branch_budget_unused"`
	BudgetActions               int                                `json:"branch_budget_actions"`
	BudgetReallocations         int                                `json:"branch_budget_reallocations"`
	GoalAwareHintUses           int                                `json:"goal_aware_hint_uses"`
	RuntimeFailures             int                                `json:"runtime_failures"`
	TLCExecutedRuns             int                                `json:"tlc_executed_runs"`
	OracleFindingRuns           int                                `json:"oracle_finding_runs"`
	LLMCalls                    int                                `json:"llm_calls"`
	SuccessfulCandidates        numericSummary                     `json:"successful_candidates_to_target"`
	SuccessfulActions           numericSummary                     `json:"successful_actions_to_target"`
	SuccessfulMillis            numericSummary                     `json:"successful_milliseconds_to_target"`
	FailureCandidates           numericSummary                     `json:"candidates_to_first_failure"`
	FailureActions              numericSummary                     `json:"actions_to_first_failure"`
	FailureMillis               numericSummary                     `json:"milliseconds_to_first_failure"`
}

type numericSummary struct {
	Count  int       `json:"count"`
	Mean   float64   `json:"mean"`
	Median float64   `json:"median"`
	StdDev float64   `json:"standard_deviation"`
	Q1     float64   `json:"q1"`
	Q3     float64   `json:"q3"`
	Min    float64   `json:"min"`
	Max    float64   `json:"max"`
	Raw    []float64 `json:"raw"`
}

type goalComparisonReport struct {
	SchemaVersion string                `json:"schema_version"`
	Input         string                `json:"input"`
	ReportCount   int                   `json:"report_count"`
	Groups        []goalComparisonGroup `json:"groups"`
	LLMCalls      int                   `json:"llm_calls"`
}

func goalCompareCommand(args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("modelfuzz-ng goal-compare", flag.ContinueOnError)
	flags.SetOutput(stderr)
	input := flags.String("input", "", "directory containing goal-search final-report.json files")
	output := flags.String("output", "", "new comparison JSON path")
	csvOutput := flags.String("csv", "", "figure-ready per-seed CSV path; defaults beside output")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || *input == "" || *output == "" {
		flags.Usage()
		return fmt.Errorf("-input and -output are required")
	}
	reports, err := loadGoalReports(*input)
	if err != nil {
		return err
	}
	if len(reports) == 0 {
		return fmt.Errorf("no goal-search final-report.json found under %s", *input)
	}
	comparison := aggregateGoalReports(*input, reports)
	if err := persistence.WriteJSONAtomic(*output, comparison); err != nil {
		return err
	}
	if *csvOutput == "" {
		*csvOutput = strings.TrimSuffix(*output, filepath.Ext(*output)) + ".csv"
	}
	if err := writeGoalCSV(*csvOutput, reports); err != nil {
		return err
	}
	if err := writeBranchCSVs(filepath.Dir(*csvOutput), reports); err != nil {
		return err
	}
	_, err = fmt.Fprintf(stdout, "Goal 对照汇总完成: reports=%d groups=%d output=%s\n",
		len(reports), len(comparison.Groups), *output)
	return err
}

func loadGoalReports(root string) ([]goalSearchReport, error) {
	var reports []goalSearchReport
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || entry.Name() != "final-report.json" {
			return nil
		}
		var report goalSearchReport
		if err := persistence.ReadJSON(path, &report); err != nil {
			return fmt.Errorf("read goal report %s: %w", path, err)
		}
		if report.SchemaVersion != goalsearch.SchemaVersion {
			return fmt.Errorf("goal report %s has schema %q", path, report.SchemaVersion)
		}
		reports = append(reports, report)
		return nil
	})
	return reports, err
}

func aggregateGoalReports(input string, reports []goalSearchReport) goalComparisonReport {
	type key struct {
		goal          goalsearch.GoalID
		mode          goalsearch.SearchMode
		hint          goalsearch.HintStrength
		topK          int
		prefix        bool
		distance      goalsearch.DistanceMode
		subject       string
		totalCapacity int
		branches      string
		awareness     goalsearch.BranchAwareness
		ablation      goalsearch.BranchDimensionAblation
		evidence      goalsearch.BranchEvidenceMode
		frontierMode  string
		budgetMode    goalsearch.BranchBudgetMode
		microPolicy   goalsearch.MicroProgressPolicy
		priority      int
	}
	grouped := make(map[key][]goalSearchReport)
	for _, report := range reports {
		k := key{
			report.Settings.GoalID, report.Settings.Mode, report.Settings.HintStrength,
			report.Settings.FrontierTopK, report.Settings.PrefixPreservation,
			report.Settings.DistanceMode, report.Settings.Subject,
			report.Settings.TotalFrontierCapacity,
			branchIDText(report.Settings.BranchTemplateIDs),
			report.Settings.BranchAwareness, report.Settings.BranchDimensionAblation,
			report.Settings.BranchEvidenceMode, report.Settings.BranchFrontierMode,
			report.Settings.BranchBudgetMode, report.Settings.MicroProgressPolicy,
			report.Settings.EvidencePriorityMultiplier,
		}
		grouped[k] = append(grouped[k], report)
	}
	keys := make([]key, 0, len(grouped))
	for k := range grouped {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].goal != keys[j].goal {
			return keys[i].goal < keys[j].goal
		}
		left := fmt.Sprintf("%s|%s|%04d|%t|%s|%s|%04d|%s|%s|%s|%s|%s|%s|%s|%04d",
			keys[i].mode, keys[i].hint, keys[i].topK, keys[i].prefix,
			keys[i].distance, keys[i].subject, keys[i].totalCapacity,
			keys[i].branches, keys[i].awareness, keys[i].ablation,
			keys[i].evidence, keys[i].frontierMode, keys[i].budgetMode, keys[i].microPolicy,
			keys[i].priority)
		right := fmt.Sprintf("%s|%s|%04d|%t|%s|%s|%04d|%s|%s|%s|%s|%s|%s|%s|%04d",
			keys[j].mode, keys[j].hint, keys[j].topK, keys[j].prefix,
			keys[j].distance, keys[j].subject, keys[j].totalCapacity,
			keys[j].branches, keys[j].awareness, keys[j].ablation,
			keys[j].evidence, keys[j].frontierMode, keys[j].budgetMode, keys[j].microPolicy,
			keys[j].priority)
		return left < right
	})
	result := goalComparisonReport{
		SchemaVersion: goalsearch.SchemaVersion, Input: input, ReportCount: len(reports),
	}
	for _, k := range keys {
		runs := grouped[k]
		group := goalComparisonGroup{
			GoalID: k.goal, Mode: k.mode, Runs: len(runs),
			HintStrength: k.hint, FrontierTopK: k.topK,
			PrefixPreservation: k.prefix, DistanceMode: k.distance, Subject: k.subject,
			TotalFrontierCapacity: k.totalCapacity,
			BranchTemplateIDs: append([]goalsearch.BranchTemplateID(nil),
				runs[0].Settings.BranchTemplateIDs...),
			BranchAwareness: k.awareness, BranchDimensionAblation: k.ablation,
			BranchEvidenceMode: k.evidence, BranchFrontierMode: k.frontierMode,
			BranchBudgetMode: k.budgetMode, MicroProgressPolicy: k.microPolicy,
			EvidencePriorityMultiplier: k.priority,
			WaypointReachRates:         make(map[string]float64),
			WaypointTransitionRates:    make(map[string]float64),
			MeanAttemptsToNext:         make(map[string]float64),
		}
		waypointCounts := make(map[string]int)
		transitionEligible := make(map[string]int)
		transitionSuccess := make(map[string]int)
		transitionAttempts := make(map[string]int)
		stalledCounts := make(map[string]int)
		var candidates, actions, elapsed int
		var targetCandidates, targetActions, targetMillis []float64
		var failureCandidates, failureActions, failureMillis []float64
		for _, report := range runs {
			group.Seeds = append(group.Seeds, report.Settings.Seed)
			candidates += report.Candidates
			actions += report.Actions
			elapsed += int(report.ElapsedMillis)
			if report.TargetReached {
				group.SuccessfulSeeds++
				targetCandidates = append(targetCandidates, float64(report.FirstTargetCandidate))
				targetActions = append(targetActions, float64(report.FirstTargetActions))
				targetMillis = append(targetMillis, float64(report.FirstTargetMillis))
			}
			if report.BugDetected {
				group.BugDetectedSeeds++
				failureCandidates = append(failureCandidates, float64(report.FirstFailureCandidate))
				failureActions = append(failureActions, float64(report.FirstFailureActions))
				failureMillis = append(failureMillis, float64(report.FirstFailureMillis))
			}
			for index, waypoint := range report.Waypoints {
				if waypoint.Reached {
					waypointCounts[waypoint.ID]++
					if index+1 < len(report.Waypoints) {
						transitionEligible[waypoint.ID]++
					}
				}
				if waypoint.TransitionSuccess {
					transitionSuccess[waypoint.ID]++
					transitionAttempts[waypoint.ID] += waypoint.AttemptsBeforeNext
				}
			}
			stalledCounts[report.MostStalledWaypoint]++
			group.OnlineOfflineMismatches += report.OnlineOfflineMismatches
			group.ExpectedOfflineMapFailures += report.ExpectedOfflineMapFailures
			group.PrefixReplayAttempts += report.PrefixReplayAttempts
			group.PrefixReplaySuccess += report.PrefixReplaySuccess
			group.PrefixExecutionMismatch += report.PrefixExecutionMismatch
			frontierStats := effectiveFrontierStats(report)
			group.FrontierInserted += frontierStats.Inserted
			group.FrontierReplaced += frontierStats.Replaced
			group.FrontierEvicted += frontierStats.Evicted
			group.FrontierSeedSelections += report.FrontierSeedSelections
			group.WaypointRegressions += report.WaypointRegressions
			group.CompletedDestroyed += report.CompletedWaypointsDestroyed
			group.NewFacetWithoutGoalProgress += report.Coverage.NewFacetWithoutGoalProgress
			group.GoalProgressWithoutNewFacet += report.Coverage.GoalProgressWithoutNewFacet
			group.NewWaypointWithoutNewFacet += report.Coverage.NewWaypointWithoutNewFacet
			group.DistanceWithoutNewFacet += report.Coverage.DistanceWithoutNewFacet
			group.PlannedBranchCount += report.Branch.PlannedBranchCount
			group.RealizedBranchCount += report.Branch.RealizedBranchCount
			group.SuccessfulBranchCount += report.Branch.SuccessfulBranchCount
			for _, aggregate := range report.Branch.ByPlannedBranch {
				group.BranchDeviations += aggregate.Deviations
			}
			group.NewBranchWithoutNewFacet += report.Branch.NewBranchWithoutNewFacet
			group.NewFacetWithoutNewBranch += report.Branch.NewFacetWithoutNewBranch
			group.SupportedBranchCount += report.Evidence.SupportedCount
			group.CommittedBranchCount += report.Evidence.CommittedCount
			group.FullRealizedCount += report.Evidence.FullRealizedCount
			group.ContradictedCount += report.Evidence.ContradictedCount
			group.BudgetGranted += report.BranchBudget.TotalGranted
			group.BudgetUsed += report.BranchBudget.TotalUsed
			group.BudgetUnused += report.BranchBudget.TotalUnused
			group.BudgetActions += report.BranchBudget.TotalActions
			group.BudgetReallocations += report.BranchBudget.Reallocations
			group.GoalAwareHintUses += report.GoalAwareHintUses
			group.RuntimeFailures += report.Unexecutable
			group.TLCExecutedRuns += report.TLCExecutedRuns
			group.OracleFindingRuns += report.OracleFindingRuns
			group.LLMCalls += report.LLMCalls
			result.LLMCalls += report.LLMCalls
		}
		sort.Slice(group.Seeds, func(i, j int) bool { return group.Seeds[i] < group.Seeds[j] })
		group.FailedSeeds = group.Runs - group.SuccessfulSeeds
		group.GoalReachRate = float64(group.SuccessfulSeeds) / float64(group.Runs)
		group.GoalReachWilsonLow, group.GoalReachWilsonHigh =
			wilsonInterval(group.SuccessfulSeeds, group.Runs)
		group.BugDetectionRate = float64(group.BugDetectedSeeds) / float64(group.Runs)
		group.BugDetectionWilsonLow, group.BugDetectionWilsonHigh =
			wilsonInterval(group.BugDetectedSeeds, group.Runs)
		if group.Subject == "control" {
			group.FalsePositiveRate = group.BugDetectionRate
		}
		group.MeanExecutedCandidates = float64(candidates) / float64(group.Runs)
		group.MeanExecutedActions = float64(actions) / float64(group.Runs)
		group.MeanElapsedMilliseconds = float64(elapsed) / float64(group.Runs)
		for waypoint, count := range waypointCounts {
			group.WaypointReachRates[waypoint] = float64(count) / float64(group.Runs)
		}
		for waypoint, eligible := range transitionEligible {
			if eligible == 0 {
				continue
			}
			group.WaypointTransitionRates[waypoint] =
				float64(transitionSuccess[waypoint]) / float64(eligible)
			if transitionSuccess[waypoint] > 0 {
				group.MeanAttemptsToNext[waypoint] =
					float64(transitionAttempts[waypoint]) / float64(transitionSuccess[waypoint])
			}
		}
		group.MostCommonStalledWaypoint = mostCommonString(stalledCounts)
		group.SuccessfulCandidates = summarizeNumbers(targetCandidates)
		group.SuccessfulActions = summarizeNumbers(targetActions)
		group.SuccessfulMillis = summarizeNumbers(targetMillis)
		group.FailureCandidates = summarizeNumbers(failureCandidates)
		group.FailureActions = summarizeNumbers(failureActions)
		group.FailureMillis = summarizeNumbers(failureMillis)
		result.Groups = append(result.Groups, group)
	}
	return result
}

func mostCommonString(counts map[string]int) string {
	keys := make([]string, 0, len(counts))
	for key := range counts {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	best, bestCount := "", -1
	for _, key := range keys {
		if counts[key] > bestCount {
			best, bestCount = key, counts[key]
		}
	}
	return best
}

func summarizeNumbers(values []float64) numericSummary {
	if len(values) == 0 {
		return numericSummary{Raw: []float64{}}
	}
	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)
	var sum float64
	for _, value := range sorted {
		sum += value
	}
	mean := sum / float64(len(sorted))
	var squares float64
	for _, value := range sorted {
		delta := value - mean
		squares += delta * delta
	}
	stddev := 0.0
	if len(sorted) > 1 {
		stddev = math.Sqrt(squares / float64(len(sorted)-1))
	}
	return numericSummary{
		Count: len(sorted), Mean: mean, Median: quantile(sorted, 0.5),
		StdDev: stddev, Q1: quantile(sorted, 0.25), Q3: quantile(sorted, 0.75),
		Min: sorted[0], Max: sorted[len(sorted)-1], Raw: sorted,
	}
}

func quantile(sorted []float64, probability float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	position := probability * float64(len(sorted)-1)
	lower := int(math.Floor(position))
	upper := int(math.Ceil(position))
	if lower == upper {
		return sorted[lower]
	}
	weight := position - float64(lower)
	return sorted[lower]*(1-weight) + sorted[upper]*weight
}

func wilsonInterval(successes, total int) (float64, float64) {
	if total <= 0 {
		return 0, 0
	}
	const z = 1.959963984540054
	n := float64(total)
	p := float64(successes) / n
	denominator := 1 + z*z/n
	center := (p + z*z/(2*n)) / denominator
	margin := z * math.Sqrt((p*(1-p)+z*z/(4*n))/n) / denominator
	return max(0.0, center-margin), min(1.0, center+margin)
}

func writeGoalCSV(path string, reports []goalSearchReport) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".goal-figure-*.csv")
	if err != nil {
		return fmt.Errorf("create goal CSV %s: %w", path, err)
	}
	temporary := file.Name()
	defer func() { _ = os.Remove(temporary) }()
	writer := csv.NewWriter(file)
	header := []string{
		"goal", "method", "seed", "config", "subject", "hint_strength", "frontier_top_k",
		"total_frontier_capacity", "branch_templates", "branch_awareness", "branch_dimension_ablation",
		"prefix_preservation", "distance_mode", "nodes", "snapshot_threshold",
		"retain_entries", "strict_tlc", "target_reached", "bug_detected",
		"first_target_candidate", "first_target_actions", "first_target_ms",
		"first_failure_candidate", "first_failure_actions", "first_failure_ms",
		"failure_layer", "failure_relation", "candidates", "actions", "elapsed_ms",
		"termination_reason", "budget_exhausted", "failure_signature",
		"waypoint_w1", "waypoint_w2", "waypoint_w3",
		"waypoint_w4", "waypoint_w5", "waypoint_w6", "waypoint_w7",
		"most_stalled_waypoint", "frontier_inserted", "frontier_replaced",
		"frontier_evicted", "frontier_seed_selections", "waypoint_regressions",
		"completed_waypoints_destroyed", "distance_improvements",
		"new_facet_without_goal_progress", "goal_progress_without_new_facet",
		"planned_branch_count", "realized_branch_count", "successful_branch_count",
		"branch_deviation_rate", "new_branch_without_new_facet", "new_facet_without_new_branch",
		"branch_evidence_mode", "branch_frontier_mode", "branch_budget_mode",
		"evidence_priority_multiplier",
		"supported_branch_count", "committed_branch_count", "full_realized_count",
		"contradicted_count", "commitment_to_next_waypoint_rate",
		"evidence_without_goal_progress", "goal_progress_without_evidence",
		"branch_budget_granted", "branch_budget_used", "branch_budget_unused",
		"branch_budget_actions", "branch_budget_reallocations",
		"llm_calls",
	}
	if err := writer.Write(header); err != nil {
		_ = file.Close()
		return err
	}
	sorted := append([]goalSearchReport(nil), reports...)
	sort.Slice(sorted, func(i, j int) bool {
		left := sorted[i].Settings
		right := sorted[j].Settings
		if left.GoalID != right.GoalID {
			return left.GoalID < right.GoalID
		}
		if left.Mode != right.Mode {
			return left.Mode < right.Mode
		}
		if left.Seed != right.Seed {
			return left.Seed < right.Seed
		}
		return left.Subject < right.Subject
	})
	for _, report := range sorted {
		settings := report.Settings
		waypointReached := make(map[string]bool, len(report.Waypoints))
		for _, waypoint := range report.Waypoints {
			waypointReached[waypoint.ID] = waypoint.Reached
		}
		signatureJSON := ""
		if report.FirstFailureSignature != nil {
			encoded, _ := json.Marshal(report.FirstFailureSignature)
			signatureJSON = string(encoded)
		}
		row := []string{
			string(settings.GoalID), string(settings.Mode), strconv.FormatInt(settings.Seed, 10),
			string(settings.Config.ExecutionID), settings.Subject, string(settings.HintStrength),
			strconv.Itoa(settings.FrontierTopK),
			strconv.Itoa(settings.TotalFrontierCapacity),
			branchIDText(settings.BranchTemplateIDs), string(settings.BranchAwareness),
			string(settings.BranchDimensionAblation),
			strconv.FormatBool(settings.PrefixPreservation), string(settings.DistanceMode),
			strconv.Itoa(settings.NodeCount), strconv.FormatUint(settings.SnapshotThreshold, 10),
			strconv.FormatUint(settings.RetainEntries, 10), strconv.FormatBool(settings.StrictTLC),
			strconv.FormatBool(report.TargetReached), strconv.FormatBool(report.BugDetected),
			strconv.Itoa(report.FirstTargetCandidate), strconv.Itoa(report.FirstTargetActions),
			strconv.FormatInt(report.FirstTargetMillis, 10),
			strconv.Itoa(report.FirstFailureCandidate), strconv.Itoa(report.FirstFailureActions),
			strconv.FormatInt(report.FirstFailureMillis, 10), string(report.FirstFailureLayer),
			report.FirstFailureRelation, strconv.Itoa(report.Candidates), strconv.Itoa(report.Actions),
			strconv.FormatInt(report.ElapsedMillis, 10), goalTerminationReason(report),
			strconv.FormatBool(goalBudgetExhausted(report)), signatureJSON,
			strconv.FormatBool(waypointReached["W1"]), strconv.FormatBool(waypointReached["W2"]),
			strconv.FormatBool(waypointReached["W3"]), strconv.FormatBool(waypointReached["W4"]),
			strconv.FormatBool(waypointReached["W5"]), strconv.FormatBool(waypointReached["W6"]),
			strconv.FormatBool(waypointReached["W7"]), report.MostStalledWaypoint,
			strconv.Itoa(effectiveFrontierStats(report).Inserted),
			strconv.Itoa(effectiveFrontierStats(report).Replaced),
			strconv.Itoa(effectiveFrontierStats(report).Evicted),
			strconv.Itoa(report.FrontierSeedSelections),
			strconv.Itoa(report.WaypointRegressions), strconv.Itoa(report.CompletedWaypointsDestroyed),
			strconv.Itoa(report.DistanceImprovements),
			strconv.Itoa(report.Coverage.NewFacetWithoutGoalProgress),
			strconv.Itoa(report.Coverage.GoalProgressWithoutNewFacet),
			strconv.Itoa(report.Branch.PlannedBranchCount),
			strconv.Itoa(report.Branch.RealizedBranchCount),
			strconv.Itoa(report.Branch.SuccessfulBranchCount),
			strconv.FormatFloat(report.Branch.DeviationRate, 'f', 6, 64),
			strconv.Itoa(report.Branch.NewBranchWithoutNewFacet),
			strconv.Itoa(report.Branch.NewFacetWithoutNewBranch),
			string(settings.BranchEvidenceMode), settings.BranchFrontierMode,
			string(settings.BranchBudgetMode),
			strconv.Itoa(settings.EvidencePriorityMultiplier),
			strconv.Itoa(report.Evidence.SupportedCount),
			strconv.Itoa(report.Evidence.CommittedCount),
			strconv.Itoa(report.Evidence.FullRealizedCount),
			strconv.Itoa(report.Evidence.ContradictedCount),
			strconv.FormatFloat(report.Evidence.CommitmentToNextWaypointRate, 'f', 6, 64),
			strconv.Itoa(report.Evidence.EvidenceWithoutGoalProgress),
			strconv.Itoa(report.Evidence.GoalProgressWithoutEvidence),
			strconv.Itoa(report.BranchBudget.TotalGranted),
			strconv.Itoa(report.BranchBudget.TotalUsed),
			strconv.Itoa(report.BranchBudget.TotalUnused),
			strconv.Itoa(report.BranchBudget.TotalActions),
			strconv.Itoa(report.BranchBudget.Reallocations),
			strconv.Itoa(report.LLMCalls),
		}
		if err := writer.Write(row); err != nil {
			_ = file.Close()
			return err
		}
	}
	writer.Flush()
	writeErr := writer.Error()
	if writeErr == nil {
		writeErr = file.Sync()
	}
	closeErr := file.Close()
	if err := errors.Join(writeErr, closeErr); err != nil {
		return err
	}
	if err := os.Rename(temporary, path); err != nil {
		return fmt.Errorf("replace goal CSV %s: %w", path, err)
	}
	return nil
}

func branchIDText(ids []goalsearch.BranchTemplateID) string {
	values := make([]string, 0, len(ids))
	for _, id := range ids {
		values = append(values, string(id))
	}
	sort.Strings(values)
	return strings.Join(values, ";")
}

func effectiveFrontierStats(report goalSearchReport) goalsearch.FrontierStats {
	if report.Settings.Mode == goalsearch.ModeEvidenceFrontier {
		stats := report.EvidenceFrontier.Stats
		return goalsearch.FrontierStats{
			Considered: stats.Considered, Inserted: stats.Inserted,
			Deduplicated: stats.Deduplicated, Replaced: stats.Replaced,
			Evicted: stats.Evicted,
		}
	}
	if report.Settings.Mode != goalsearch.ModeDiversityFrontier {
		return report.Frontier.Stats
	}
	stats := report.BranchFrontier.Stats
	return goalsearch.FrontierStats{
		Considered: stats.Considered, Inserted: stats.Inserted,
		Deduplicated: stats.Deduplicated, Replaced: stats.Replaced,
		Evicted: stats.Evicted,
	}
}

func writeBranchCSVs(directory string, reports []goalSearchReport) error {
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return err
	}
	type outputSpec struct {
		name   string
		header []string
		rows   [][]string
	}
	perSeed := outputSpec{
		name: "per-seed-branches.csv",
		header: []string{
			"goal", "method", "hint_strength", "frontier_top_k",
			"total_frontier_capacity", "branch_templates", "branch_awareness",
			"branch_dimension_ablation", "seed", "subject", "planned_branch_count",
			"realized_branch_count", "planned_realized_pair_count",
			"successful_branch_count", "agreement_rate", "deviation_rate",
			"new_branch_without_new_facet", "new_facet_without_new_branch",
			"exact_trace_key", "semantic_trace_key", "progress_path_key",
			"bug_detected", "first_failure_waypoint", "first_failure_relation",
			"first_failure_planned_branch", "first_failure_realized_branch",
			"first_failure_realized_branch_key", "first_failure_branch_decidable",
			"first_failure_branch_deviated",
		},
	}
	perBranch := outputSpec{
		name: "per-branch-bug-detection.csv",
		header: []string{
			"goal", "method", "hint_strength", "frontier_top_k",
			"total_frontier_capacity", "branch_templates", "branch_awareness",
			"branch_dimension_ablation", "seed", "subject", "planned_branch", "attempts",
			"decidable", "agreements", "deviations", "deepest_waypoint",
			"goal_reached", "bug_detected", "actions", "frontier_retained",
			"frontier_evicted", "permanently_infeasible",
		},
	}
	singleBranch := outputSpec{
		name: "single-branch-reachability.csv",
		header: []string{
			"goal", "method", "seed", "planned_branch", "target_reached",
			"supported", "committed", "full_realized", "contradicted",
			"deepest_waypoint", "candidates", "actions", "elapsed_ms",
			"prefix_replay_attempts", "prefix_replay_success",
			"online_offline_mismatches", "budget_exhausted",
		},
	}
	perEvidence := outputSpec{
		name: "per-evidence-result.csv",
		header: []string{
			"goal", "method", "seed", "evidence_id", "observed_count",
			"first_observed_step", "invalidation_count", "next_stage_success_count",
			"utility", "false_progress_count", "sample_sufficient",
		},
	}
	perBudget := outputSpec{
		name: "per-branch-budget.csv",
		header: []string{
			"goal", "method", "seed", "budget_mode", "planned_branch",
			"granted", "used", "action_used", "unused", "supported_quota_granted",
			"commitment_quota_granted", "highest_waypoint", "stopped", "stop_reason",
		},
	}
	sorted := append([]goalSearchReport(nil), reports...)
	sort.Slice(sorted, func(i, j int) bool {
		left, right := sorted[i].Settings, sorted[j].Settings
		if left.GoalID != right.GoalID {
			return left.GoalID < right.GoalID
		}
		if left.Mode != right.Mode {
			return left.Mode < right.Mode
		}
		if left.Seed != right.Seed {
			return left.Seed < right.Seed
		}
		return left.Subject < right.Subject
	})
	for _, report := range sorted {
		settings := report.Settings
		common := []string{
			string(settings.GoalID), string(settings.Mode), string(settings.HintStrength),
			strconv.Itoa(settings.FrontierTopK),
			strconv.Itoa(settings.TotalFrontierCapacity),
			branchIDText(settings.BranchTemplateIDs), string(settings.BranchAwareness),
			string(settings.BranchDimensionAblation),
			strconv.FormatInt(settings.Seed, 10), settings.Subject,
		}
		perSeed.rows = append(perSeed.rows, []string{
			common[0], common[1], common[2], common[3], common[4],
			common[5], common[6], common[7], common[8], common[9],
			strconv.Itoa(report.Branch.PlannedBranchCount),
			strconv.Itoa(report.Branch.RealizedBranchCount),
			strconv.Itoa(report.Branch.PlannedRealizedPairCount),
			strconv.Itoa(report.Branch.SuccessfulBranchCount),
			strconv.FormatFloat(report.Branch.AgreementRate, 'f', 6, 64),
			strconv.FormatFloat(report.Branch.DeviationRate, 'f', 6, 64),
			strconv.Itoa(report.Branch.NewBranchWithoutNewFacet),
			strconv.Itoa(report.Branch.NewFacetWithoutNewBranch),
			report.Diversity.FinalTraceKey, report.Diversity.SemanticTraceKey,
			report.Diversity.GoalProgressSequenceKey,
			strconv.FormatBool(report.BugDetected), report.FirstFailureWaypoint,
			report.FirstFailureRelation, string(report.FirstFailurePlannedBranch),
			string(report.FirstFailureRealizedBranch), report.FirstFailureRealizedKey,
			strconv.FormatBool(report.FirstFailureBranchDecidable),
			strconv.FormatBool(report.FirstFailureBranchDeviation.Occurred),
		})
		if len(settings.BranchTemplateIDs) == 1 {
			branchID := settings.BranchTemplateIDs[0]
			aggregate := report.Branch.ByPlannedBranch[branchID]
			singleBranch.rows = append(singleBranch.rows, []string{
				string(settings.GoalID), string(settings.Mode),
				strconv.FormatInt(settings.Seed, 10), string(branchID),
				strconv.FormatBool(report.TargetReached),
				strconv.FormatBool(report.Evidence.SupportedCount > 0),
				strconv.FormatBool(report.Evidence.CommittedCount > 0),
				strconv.FormatBool(report.Evidence.FullRealizedCount > 0),
				strconv.FormatBool(report.Evidence.ContradictedCount > 0),
				strconv.Itoa(aggregate.DeepestWaypoint),
				strconv.Itoa(report.Candidates), strconv.Itoa(report.Actions),
				strconv.FormatInt(report.ElapsedMillis, 10),
				strconv.Itoa(report.PrefixReplayAttempts),
				strconv.Itoa(report.PrefixReplaySuccess),
				strconv.Itoa(report.OnlineOfflineMismatches),
				strconv.FormatBool(goalBudgetExhausted(report)),
			})
		}
		evidenceIDs := make([]string, 0, len(report.Evidence.ByEvidence))
		for id := range report.Evidence.ByEvidence {
			evidenceIDs = append(evidenceIDs, id)
		}
		sort.Strings(evidenceIDs)
		for _, id := range evidenceIDs {
			aggregate := report.Evidence.ByEvidence[id]
			perEvidence.rows = append(perEvidence.rows, []string{
				string(settings.GoalID), string(settings.Mode),
				strconv.FormatInt(settings.Seed, 10), id,
				strconv.Itoa(aggregate.ObservedCount),
				strconv.Itoa(aggregate.FirstObservedStep),
				strconv.Itoa(aggregate.InvalidationCount),
				strconv.Itoa(aggregate.NextStageSuccessCount),
				strconv.FormatFloat(aggregate.Utility, 'f', 6, 64),
				strconv.Itoa(aggregate.FalseProgressCount),
				strconv.FormatBool(aggregate.SampleSufficient),
			})
		}
		budgetIDs := make([]goalsearch.BranchTemplateID, 0, len(report.BranchBudget.States))
		for id := range report.BranchBudget.States {
			budgetIDs = append(budgetIDs, id)
		}
		sort.Slice(budgetIDs, func(i, j int) bool { return budgetIDs[i] < budgetIDs[j] })
		for _, id := range budgetIDs {
			state := report.BranchBudget.States[id]
			perBudget.rows = append(perBudget.rows, []string{
				string(settings.GoalID), string(settings.Mode),
				strconv.FormatInt(settings.Seed, 10),
				string(report.BranchBudget.Mode), string(id),
				strconv.Itoa(state.Granted), strconv.Itoa(state.Used),
				strconv.Itoa(state.ActionUsed), strconv.Itoa(state.Unused),
				strconv.FormatBool(state.SupportedGranted),
				strconv.FormatBool(state.CommitmentGranted),
				strconv.Itoa(state.HighestWaypoint),
				strconv.FormatBool(state.Stopped), state.StopReason,
			})
		}
		if len(budgetIDs) == 0 {
			ids := make([]goalsearch.BranchTemplateID, 0, len(report.Branch.ByPlannedBranch))
			for id := range report.Branch.ByPlannedBranch {
				ids = append(ids, id)
			}
			sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
			for _, id := range ids {
				aggregate := report.Branch.ByPlannedBranch[id]
				perBudget.rows = append(perBudget.rows, []string{
					string(settings.GoalID), string(settings.Mode),
					strconv.FormatInt(settings.Seed, 10),
					string(goalsearch.BranchBudgetRoundRobin), string(id),
					strconv.Itoa(aggregate.Attempts), strconv.Itoa(aggregate.Attempts),
					strconv.Itoa(aggregate.Actions), "0", "false", "false",
					strconv.Itoa(aggregate.DeepestWaypoint), "false", "",
				})
			}
		}
		ids := make([]goalsearch.BranchTemplateID, 0, len(report.Branch.ByPlannedBranch))
		for id := range report.Branch.ByPlannedBranch {
			ids = append(ids, id)
		}
		sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
		for _, id := range ids {
			aggregate := report.Branch.ByPlannedBranch[id]
			perBranch.rows = append(perBranch.rows, []string{
				common[0], common[1], common[2], common[3], common[4],
				common[5], common[6], common[7], common[8], common[9], string(id),
				strconv.Itoa(aggregate.Attempts), strconv.Itoa(aggregate.Decidable),
				strconv.Itoa(aggregate.Agreements), strconv.Itoa(aggregate.Deviations),
				strconv.Itoa(aggregate.DeepestWaypoint), strconv.Itoa(aggregate.GoalReached),
				strconv.Itoa(aggregate.BugDetected), strconv.Itoa(aggregate.Actions),
				strconv.Itoa(aggregate.FrontierRetained), strconv.Itoa(aggregate.FrontierEvicted),
				strconv.Itoa(aggregate.PermanentlyInfeasible),
			})
		}
	}
	for _, spec := range []outputSpec{
		perSeed, perBranch, singleBranch, perEvidence, perBudget,
	} {
		path := filepath.Join(directory, spec.name)
		file, err := os.CreateTemp(directory, ".branch-csv-*.tmp")
		if err != nil {
			return err
		}
		temp := file.Name()
		writer := csv.NewWriter(file)
		writeErr := writer.Write(spec.header)
		for _, row := range spec.rows {
			if writeErr == nil {
				writeErr = writer.Write(row)
			}
		}
		writer.Flush()
		if writeErr == nil {
			writeErr = writer.Error()
		}
		if writeErr == nil {
			writeErr = file.Sync()
		}
		writeErr = errors.Join(writeErr, file.Close())
		if writeErr != nil {
			_ = os.Remove(temp)
			return writeErr
		}
		if err := os.Rename(temp, path); err != nil {
			_ = os.Remove(temp)
			return err
		}
	}
	return nil
}

func goalBudgetExhausted(report goalSearchReport) bool {
	return !report.BugDetected && !report.TargetReached &&
		(report.Candidates >= report.Settings.CandidateBudget ||
			report.Actions >= report.Settings.ActionBudget)
}

func goalTerminationReason(report goalSearchReport) string {
	switch {
	case report.BugDetected:
		return "bug-detected"
	case report.TargetReached && report.Settings.StopOnTarget:
		return "target-reached"
	case report.Candidates >= report.Settings.CandidateBudget:
		return "candidate-budget-exhausted"
	case report.Actions >= report.Settings.ActionBudget:
		return "action-budget-exhausted"
	case !report.TargetReached:
		return "waypoint-stall-budget"
	default:
		return "completed-after-target"
	}
}
