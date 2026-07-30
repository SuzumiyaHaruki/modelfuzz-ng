package main

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/breadthdepth"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/engine"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/goalsearch"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/persistence"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/protocolmutation"
)

type breadthDepthMethodGoalStatistics struct {
	Metrics       map[string]descriptiveStatistics `json:"metrics"`
	GoalReach     rateStatistics                   `json:"goal_reach"`
	BudgetExhaust rateStatistics                   `json:"budget_exhausted"`
	WaypointReach map[string]rateStatistics        `json:"waypoint_reach"`
	EffectVsM1    map[string]float64               `json:"cliffs_delta_vs_m1"`
	EffectVsM5    map[string]float64               `json:"cliffs_delta_vs_m5"`
}

type breadthDepthStatistics struct {
	Schema       string                                      `json:"schema_version"`
	ByMethodGoal map[string]breadthDepthMethodGoalStatistics `json:"by_method_goal"`
	Inference    string                                      `json:"inference"`
}

func writeBreadthDepthTables(
	output string,
	manifest breadthDepthManifest,
	summary breadthDepthBenchmarkSummary,
) error {
	cross := newBreadthDepthCrossRows()
	handoff := newHandoffQualityRows()
	paths := newSuccessfulPathRows()
	complementarity := [][]string{{
		"method", "goal", "seed", "metric", "value", "interpretation",
	}}
	coverageGrowth := [][]string{{
		"method", "goal", "seed", "phase", "phase_candidate", "total_candidate",
		"total_actions", "raw", "v2", "election", "replication", "snapshot",
		"recovery", "network", "election_network", "replication_network",
		"snapshot_recovery", "recovery_term_relation", "semantic_traces",
	}}
	waypointGrowth := [][]string{{
		"method", "goal", "seed", "candidate", "parent_seed",
		"completed_waypoints", "current_waypoint", "distance", "target_reached",
		"new_facet", "action_count", "cumulative_actions",
	}}

	for _, campaign := range summary.Campaigns {
		cross = append(cross, breadthDepthCrossRow(campaign))
		if campaign.Combined.Handoff != nil {
			handoff = append(handoff, handoffQualityRow(campaign))
		}
		if campaign.LocalReport != nil && campaign.LocalReport.TargetReached {
			path, err := successfulPathRow(campaign)
			if err != nil {
				return err
			}
			paths = append(paths, path)
		}
		complementarity = append(
			complementarity, breadthDepthComplementarityRows(campaign)...)
		growth, err := prefixedCoverageGrowthRows(campaign)
		if err != nil {
			return err
		}
		coverageGrowth = append(coverageGrowth, growth...)
		waypoints, err := waypointGrowthRows(campaign)
		if err != nil {
			return err
		}
		waypointGrowth = append(waypointGrowth, waypoints...)
	}

	tables := []struct {
		name string
		rows [][]string
	}{
		{"breadth-depth-cross-matrix.csv", cross},
		{"handoff-quality.csv", handoff},
		{"successful-path-diversity.csv", paths},
		{"breadth-depth-complementarity.csv", complementarity},
		{"coverage-growth-final.csv", coverageGrowth},
		{"waypoint-growth-local.csv", waypointGrowth},
		{"figure-ready.csv", cross},
	}
	for _, table := range tables {
		if err := writeCSVRows(filepath.Join(output, table.name), table.rows); err != nil {
			return err
		}
	}
	return writeBreadthDepthStatistics(
		filepath.Join(output, "breadth-depth-statistics.json"), manifest, summary)
}

func newBreadthDepthCrossRows() [][]string {
	return [][]string{{
		"method", "goal", "seed", "global_candidates", "local_candidates",
		"total_candidates", "global_actions", "local_actions", "total_actions",
		"goal_reached", "deepest_waypoint", "minimum_distance", "budget_exhausted",
		"target_candidate", "target_actions", "target_millis",
		"handoff_selected", "handoff_retained", "handoff_fallback",
		"final_raw", "final_v2", "final_election", "final_replication",
		"final_snapshot", "final_recovery", "final_network",
		"final_election_network", "final_replication_network",
		"final_snapshot_recovery", "final_recovery_term_relation",
		"final_semantic_traces", "local_new_raw", "local_new_v2",
		"local_new_facets", "local_new_interactions", "global_coverage_retained",
		"budget_valid",
	}}
}

func breadthDepthCrossRow(campaign breadthDepthCampaign) []string {
	combined := campaign.Combined
	globalCandidates, globalActions, localCandidates, localActions := 0, 0, 0, 0
	selected, retained, fallback := 0, 0, false
	targetCandidate, targetActions, targetMillis := "", "", ""
	if combined.Global != nil {
		globalCandidates, globalActions = combined.Global.Candidates, combined.Global.Actions
	}
	if combined.Local != nil {
		localCandidates, localActions = combined.Local.Candidates, combined.Local.Actions
	}
	if combined.Handoff != nil {
		selected, fallback = len(combined.Handoff.Selected), combined.Handoff.Fallback
	}
	if campaign.LocalReport != nil {
		retained = campaign.LocalReport.RetainedHandoffSeeds
		if campaign.LocalReport.TargetReached {
			targetCandidate = strconv.Itoa(campaign.LocalReport.FirstTargetCandidate)
			targetActions = strconv.Itoa(campaign.LocalReport.FirstTargetActions)
			targetMillis = strconv.FormatInt(campaign.LocalReport.FirstTargetMillis, 10)
		}
	}
	return []string{
		string(campaign.Method), string(campaign.Goal), strconv.FormatInt(campaign.Seed, 10),
		strconv.Itoa(globalCandidates), strconv.Itoa(localCandidates),
		strconv.Itoa(combined.FinalCandidates), strconv.Itoa(globalActions),
		strconv.Itoa(localActions), strconv.Itoa(combined.FinalActions),
		strconv.FormatBool(combined.GoalReached), strconv.Itoa(combined.DeepestWaypoint),
		strconv.Itoa(combined.MinimumDistance), strconv.FormatBool(combined.BudgetExhausted),
		targetCandidate, targetActions, targetMillis,
		strconv.Itoa(selected), strconv.Itoa(retained), strconv.FormatBool(fallback),
		strconv.Itoa(combined.FinalCoverage.Raw), strconv.Itoa(combined.FinalCoverage.V2),
		strconv.Itoa(combined.FinalCoverage.Facets["election"]),
		strconv.Itoa(combined.FinalCoverage.Facets["replication"]),
		strconv.Itoa(combined.FinalCoverage.Facets["snapshot"]),
		strconv.Itoa(combined.FinalCoverage.Facets["recovery"]),
		strconv.Itoa(combined.FinalCoverage.Facets["network"]),
		strconv.Itoa(combined.FinalCoverage.Interactions["election_network"]),
		strconv.Itoa(combined.FinalCoverage.Interactions["replication_network"]),
		strconv.Itoa(combined.FinalCoverage.Interactions["snapshot_recovery"]),
		strconv.Itoa(combined.FinalCoverage.Interactions["recovery_term_relation"]),
		strconv.Itoa(combined.FinalCoverage.SemanticTraces),
		strconv.Itoa(combined.LocalNewCoverage.Raw),
		strconv.Itoa(combined.LocalNewCoverage.V2),
		strconv.Itoa(sumStringIntMap(combined.LocalNewCoverage.Facets)),
		strconv.Itoa(sumStringIntMap(combined.LocalNewCoverage.Interactions)),
		strconv.FormatBool(combined.GlobalCoverageRetained),
		strconv.FormatBool(combined.BudgetValid),
	}
}

func newHandoffQualityRows() [][]string {
	return [][]string{{
		"method", "goal", "seed", "corpus_entries", "replayable_entries",
		"entry_condition_entries", "w1_entries", "w2_entries", "w3_entries",
		"w4_or_deeper_entries", "deepest_initial_waypoint", "distance_distribution",
		"selected_entries", "retained_entries", "semantic_traces",
		"facet_combinations", "queue_shapes", "replay_success_rate", "fallback",
		"local_selection_counts", "contributing_handoff_seed", "compression_rate",
		"global_actions_per_corpus_entry", "unselected_deeper_posterior",
	}}
}

func handoffQualityRow(campaign breadthDepthCampaign) []string {
	var candidates []breadthdepth.HandoffSeed
	_ = readJSONLines(filepath.Join(campaign.Directory, "handoff-candidates.jsonl"), &candidates)
	replayable, entry, w1, w2, w3, w4, deepest := 0, 0, 0, 0, 0, 0, 0
	distances := make(map[int]int)
	for _, candidate := range candidates {
		if candidate.Replayable {
			replayable++
		}
		if candidate.Progress.EntryCondition {
			entry++
		}
		deepest = max(deepest, candidate.Progress.Completed)
		distances[candidate.Progress.Distance]++
		if candidate.Progress.Completed >= 1 {
			w1++
		}
		if candidate.Progress.Completed >= 2 {
			w2++
		}
		if candidate.Progress.Completed >= 3 {
			w3++
		}
		if candidate.Progress.Completed >= 4 {
			w4++
		}
	}
	replays := make([]handoffReplayRecord, 0)
	_ = readJSONLines(filepath.Join(campaign.Directory, "handoff-replay.jsonl"), &replays)
	replaySuccess := 0
	for _, replay := range replays {
		if replay.Succeeded {
			replaySuccess++
		}
	}
	replayRate := 0.0
	if len(replays) > 0 {
		replayRate = float64(replaySuccess) / float64(len(replays))
	}
	selected := campaign.Combined.Handoff.Selected
	semantic, facets, queues := map[string]struct{}{}, map[string]struct{}{}, map[string]struct{}{}
	for _, seed := range selected {
		semantic[seed.SemanticTraceDigest] = struct{}{}
		facets[seed.FacetCombinationKey] = struct{}{}
		queues[seed.QueueShapeKey] = struct{}{}
	}
	retained, contributing, selections := 0, "", map[string]int{}
	if campaign.LocalReport != nil {
		retained = campaign.LocalReport.RetainedHandoffSeeds
		contributing = campaign.LocalReport.ContributingHandoffSeedID
		selections = campaign.LocalReport.HandoffSeedSelections
	}
	corpusEntries, globalActions := len(candidates), 0
	if campaign.Combined.Global != nil {
		corpusEntries = campaign.Combined.Global.CorpusEntries
		globalActions = campaign.Combined.Global.Actions
	}
	compression, cost := 0.0, 0.0
	if corpusEntries > 0 {
		compression = float64(len(selected)) / float64(corpusEntries)
		cost = float64(globalActions) / float64(corpusEntries)
	}
	return []string{
		string(campaign.Method), string(campaign.Goal), strconv.FormatInt(campaign.Seed, 10),
		strconv.Itoa(corpusEntries), strconv.Itoa(replayable), strconv.Itoa(entry),
		strconv.Itoa(w1), strconv.Itoa(w2), strconv.Itoa(w3), strconv.Itoa(w4),
		strconv.Itoa(deepest), stableJSON(distances), strconv.Itoa(len(selected)),
		strconv.Itoa(retained), strconv.Itoa(len(semantic)), strconv.Itoa(len(facets)),
		strconv.Itoa(len(queues)), formatFloat(replayRate),
		strconv.FormatBool(campaign.Combined.Handoff.Fallback), stableJSON(selections),
		contributing, formatFloat(compression), formatFloat(cost),
		"not_evaluated_by_frozen_design",
	}
}

func newSuccessfulPathRows() [][]string {
	return [][]string{{
		"method", "goal", "seed", "target_candidate", "exact_trace",
		"relative_semantic_trace", "goal_progress_path", "handoff_semantic_class",
		"contributing_handoff_seed", "target_binding_semantic_class",
		"network_facets", "replication_facets", "recovery_facets",
		"advisor_reason_sequence",
	}}
}

func successfulPathRow(campaign breadthDepthCampaign) ([]string, error) {
	report := campaign.LocalReport
	index := report.FirstTargetCandidate - 1
	runDirectory := filepath.Join(
		campaign.Directory, "local", "runs", fmt.Sprintf("candidate-%06d", index))
	var result engine.Result
	if err := persistence.ReadJSON(filepath.Join(runDirectory, "result.json"), &result); err != nil {
		return nil, err
	}
	var evaluation goalsearch.EvaluationResult
	if err := persistence.ReadJSON(
		filepath.Join(runDirectory, "goal-progress-online.json"), &evaluation); err != nil {
		return nil, err
	}
	roles := make(map[string]string)
	for symbol, binding := range evaluation.Instance.Bindings {
		roles[string(symbol)] = breadthdepth.BindingRoleKey(result.Final, binding.Node)
	}
	var decisions []protocolmutation.Decision
	_ = readJSONLines(
		filepath.Join(campaign.Directory, "local", "mutation-advisor-decisions.jsonl"),
		&decisions)
	reasons := make([]string, 0, len(decisions))
	for _, decision := range decisions {
		if decision.CandidateIndex <= index {
			reasons = append(reasons, decision.Selected.ReasonCode)
		}
	}
	handoffClass := ""
	if campaign.Combined.Handoff != nil {
		for _, seed := range campaign.Combined.Handoff.Selected {
			if "handoff-"+seed.GlobalCorpusID == report.ContributingHandoffSeedID {
				handoffClass = seed.SemanticTraceDigest
				break
			}
		}
	}
	return []string{
		string(campaign.Method), string(campaign.Goal), strconv.FormatInt(campaign.Seed, 10),
		strconv.Itoa(report.FirstTargetCandidate), goalsearch.TraceKey(result.Trace),
		breadthdepth.RelativeSemanticTraceKey(result.Trace),
		report.Diversity.GoalProgressSequenceKey, handoffClass,
		report.ContributingHandoffSeedID, stableJSON(roles),
		strconv.Itoa(report.Coverage.Facets["network"]),
		strconv.Itoa(report.Coverage.Facets["replication"]),
		strconv.Itoa(report.Coverage.Facets["recovery"]),
		strings.Join(reasons, ">"),
	}, nil
}

func breadthDepthComplementarityRows(campaign breadthDepthCampaign) [][]string {
	prefix := []string{
		string(campaign.Method), string(campaign.Goal), strconv.FormatInt(campaign.Seed, 10),
	}
	rows := make([][]string, 0, 16)
	add := func(metric string, value any, interpretation string) {
		rows = append(rows, append(append([]string{}, prefix...),
			metric, fmt.Sprint(value), interpretation))
	}
	if campaign.LocalReport != nil {
		coverage := campaign.LocalReport.Coverage
		add("local_new_facet_without_goal_progress",
			coverage.NewFacetWithoutGoalProgress, "local record-only")
		add("local_goal_progress_without_new_facet",
			coverage.GoalProgressWithoutNewFacet, "local record-only")
		add("local_new_waypoint_without_new_facet",
			coverage.NewWaypointWithoutNewFacet, "local record-only")
		add("local_distance_improvement_without_new_facet",
			coverage.DistanceWithoutNewFacet, "local record-only")
	}
	var candidates []breadthdepth.HandoffSeed
	_ = readJSONLines(filepath.Join(campaign.Directory, "handoff-candidates.jsonl"), &candidates)
	globalFacetNoProgress, globalProgressNoFacet := 0, 0
	bestCompleted, bestDistance := -1, 99
	highestNovelty, deepestNovelty := -1, 0
	highestNoveltyIsDeepest, deepestHasNovelty := false, false
	for _, candidate := range candidates {
		progressed := candidate.Progress.Completed > bestCompleted ||
			(candidate.Progress.Completed == bestCompleted &&
				candidate.Progress.Distance < bestDistance)
		if candidate.NewFacet && !progressed {
			globalFacetNoProgress++
		}
		if progressed && !candidate.NewFacet {
			globalProgressNoFacet++
		}
		if progressed {
			bestCompleted, bestDistance =
				candidate.Progress.Completed, candidate.Progress.Distance
		}
		highestNovelty = max(highestNovelty, candidate.FacetNoveltyCount)
		deepestNovelty = max(deepestNovelty, candidate.Progress.Completed)
	}
	for _, candidate := range candidates {
		if candidate.FacetNoveltyCount == highestNovelty &&
			candidate.Progress.Completed == deepestNovelty {
			highestNoveltyIsDeepest = true
		}
		if candidate.Progress.Completed == deepestNovelty && candidate.FacetNoveltyCount > 0 {
			deepestHasNovelty = true
		}
	}
	add("global_new_facet_without_handoff_progress",
		globalFacetNoProgress, "corpus admission order")
	add("global_handoff_progress_without_new_facet",
		globalProgressNoFacet, "corpus admission order")
	add("highest_facet_novelty_seed_is_deepest_goal_seed",
		highestNoveltyIsDeepest, "ties count as overlap")
	add("deepest_goal_seed_has_facet_novelty",
		deepestHasNovelty, "ties count as overlap")
	add("local_new_raw", campaign.Combined.LocalNewCoverage.Raw, "exact set difference")
	add("local_new_v2", campaign.Combined.LocalNewCoverage.V2, "exact set difference")
	add("local_new_facet_units",
		sumStringIntMap(campaign.Combined.LocalNewCoverage.Facets), "exact set difference")
	add("local_new_interaction_units",
		sumStringIntMap(campaign.Combined.LocalNewCoverage.Interactions), "exact set difference")
	add("global_coverage_retained",
		campaign.Combined.GlobalCoverageRetained, "exact set containment")
	return rows
}

func prefixedCoverageGrowthRows(campaign breadthDepthCampaign) ([][]string, error) {
	path := filepath.Join(campaign.Directory, "coverage-growth-final.csv")
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	rows, err := csv.NewReader(file).ReadAll()
	if err != nil {
		return nil, err
	}
	result := make([][]string, 0, max(0, len(rows)-1))
	for _, row := range rows[1:] {
		prefix := []string{
			string(campaign.Method), string(campaign.Goal),
			strconv.FormatInt(campaign.Seed, 10),
		}
		result = append(result, append(prefix, row...))
	}
	return result, nil
}

func waypointGrowthRows(campaign breadthDepthCampaign) ([][]string, error) {
	if campaign.LocalReport == nil {
		return nil, nil
	}
	var records []goalProgressRecord
	if err := readJSONLines(
		filepath.Join(campaign.Directory, "local", "goal-progress.jsonl"),
		&records); err != nil {
		return nil, err
	}
	rows := make([][]string, 0, len(records))
	cumulative := 0
	for _, record := range records {
		cumulative += record.ActionCount
		rows = append(rows, []string{
			string(campaign.Method), string(campaign.Goal),
			strconv.FormatInt(campaign.Seed, 10), strconv.Itoa(record.CandidateIndex + 1),
			record.ParentSeedID, strconv.Itoa(record.CompletedWaypoints),
			record.CurrentWaypoint, strconv.Itoa(record.Distance),
			strconv.FormatBool(record.TargetReached), strconv.FormatBool(record.NewFacet),
			strconv.Itoa(record.ActionCount), strconv.Itoa(cumulative),
		})
	}
	return rows, nil
}

func writeBreadthDepthStatistics(
	path string,
	_ breadthDepthManifest,
	summary breadthDepthBenchmarkSummary,
) error {
	values := make(map[string]map[string][]float64)
	goalReach, budgetExhaust := make(map[string]int), make(map[string]int)
	totals := make(map[string]int)
	waypointReach := make(map[string]map[string]int)
	for _, campaign := range summary.Campaigns {
		key := breadthDepthGroupKey(campaign.Method, campaign.Goal)
		if values[key] == nil {
			values[key] = make(map[string][]float64)
			waypointReach[key] = make(map[string]int)
		}
		add := func(name string, value int) {
			values[key][name] = append(values[key][name], float64(value))
		}
		combined := campaign.Combined
		add("final_raw", combined.FinalCoverage.Raw)
		add("final_v2", combined.FinalCoverage.V2)
		add("final_semantic_traces", combined.FinalCoverage.SemanticTraces)
		add("final_candidates", combined.FinalCandidates)
		add("final_actions", combined.FinalActions)
		add("deepest_waypoint", combined.DeepestWaypoint)
		add("local_new_raw", combined.LocalNewCoverage.Raw)
		add("local_new_v2", combined.LocalNewCoverage.V2)
		for name, value := range combined.FinalCoverage.Facets {
			add("final_facet:"+name, value)
		}
		for name, value := range combined.FinalCoverage.Interactions {
			add("final_interaction:"+name, value)
		}
		if combined.GoalReached {
			goalReach[key]++
		}
		if combined.BudgetExhausted {
			budgetExhaust[key]++
		}
		if campaign.LocalReport != nil {
			if campaign.LocalReport.TargetReached {
				add("successful_candidates_to_target",
					campaign.LocalReport.FirstTargetCandidate)
				add("successful_actions_to_target", campaign.LocalReport.FirstTargetActions)
				add("successful_millis_to_target",
					int(campaign.LocalReport.FirstTargetMillis))
			}
			for _, waypoint := range campaign.LocalReport.Waypoints {
				if waypoint.Reached {
					waypointReach[key][waypoint.ID]++
				}
			}
		} else {
			var candidates []breadthdepth.HandoffSeed
			_ = readJSONLines(
				filepath.Join(campaign.Directory, "handoff-candidates.jsonl"), &candidates)
			reached := make(map[string]bool)
			for _, candidate := range candidates {
				for index := 1; index <= candidate.Progress.Completed; index++ {
					reached[fmt.Sprintf("W%d", index)] = true
				}
			}
			for waypoint := range reached {
				waypointReach[key][waypoint]++
			}
		}
		totals[key]++
	}
	artifact := breadthDepthStatistics{
		Schema:       "raft-breadth-depth-statistics-v1",
		ByMethodGoal: make(map[string]breadthDepthMethodGoalStatistics),
		Inference: "descriptive statistics and effect sizes; budget-exhausted runs are " +
			"censored and excluded from successful time/action-to-target distributions",
	}
	for key, metrics := range values {
		group := breadthDepthMethodGoalStatistics{
			Metrics:       make(map[string]descriptiveStatistics),
			GoalReach:     rateSummary(goalReach[key], totals[key]),
			BudgetExhaust: rateSummary(budgetExhaust[key], totals[key]),
			WaypointReach: make(map[string]rateStatistics),
			EffectVsM1:    make(map[string]float64),
			EffectVsM5:    make(map[string]float64),
		}
		goal := strings.SplitN(key, "|", 2)[1]
		m1 := values[breadthDepthGroupKey(breadthdepth.MethodLocalOnly, goalsearch.GoalID(goal))]
		m5 := values[breadthDepthGroupKey(breadthdepth.MethodFacetThen, goalsearch.GoalID(goal))]
		for name, metricValues := range metrics {
			group.Metrics[name] = describe(metricValues)
			if len(m1[name]) > 0 {
				group.EffectVsM1[name] = cliffsDelta(metricValues, m1[name])
			}
			if len(m5[name]) > 0 {
				group.EffectVsM5[name] = cliffsDelta(metricValues, m5[name])
			}
		}
		for waypoint, reached := range waypointReach[key] {
			group.WaypointReach[waypoint] = rateSummary(reached, totals[key])
		}
		artifact.ByMethodGoal[key] = group
	}
	return persistence.WriteJSONAtomic(path, artifact)
}

func breadthDepthGroupKey(method breadthdepth.Method, goal goalsearch.GoalID) string {
	return string(method) + "|" + string(goal)
}

func sumStringIntMap(values map[string]int) int {
	total := 0
	for _, value := range values {
		total += value
	}
	return total
}

func stableJSON(value any) string {
	data, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	return string(data)
}

func formatFloat(value float64) string {
	return strconv.FormatFloat(value, 'f', 6, 64)
}
