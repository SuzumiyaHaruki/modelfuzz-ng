package main

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/coverageguidance"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/engine"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/experiment"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/goalsearch"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/persistence"
)

type offlineGoalRunEvaluation struct {
	RunIndex         int               `json:"run_index"`
	Seed             int64             `json:"seed"`
	GoalID           goalsearch.GoalID `json:"goal_id"`
	Reached          bool              `json:"reached"`
	DeepestWaypoint  int               `json:"deepest_waypoint"`
	ReachedWaypoints []string          `json:"reached_waypoints"`
	TargetStep       int               `json:"target_step"`
	TargetPlanAction int               `json:"target_plan_action"`
	StableKey        string            `json:"stable_key"`
	Error            string            `json:"error,omitempty"`
}

type offlineGoalSummary struct {
	GoalID                   goalsearch.GoalID `json:"goal_id"`
	Runs                     int               `json:"runs"`
	Reached                  int               `json:"reached"`
	FirstCandidate           int               `json:"first_candidate"`
	FirstCumulativeAction    int               `json:"first_cumulative_action"`
	WaypointReached          map[string]int    `json:"waypoint_reached"`
	DeepestWaypointHistogram map[int]int       `json:"deepest_waypoint_histogram"`
	RelevantInteractionRuns  int               `json:"relevant_interaction_runs"`
}

type offlineGoalEvaluationArtifact struct {
	Schema                string                        `json:"schema"`
	Enabled               bool                          `json:"enabled"`
	Status                string                        `json:"status"`
	Goals                 map[string]offlineGoalSummary `json:"goals"`
	Runs                  []offlineGoalRunEvaluation    `json:"runs"`
	OnlineComparisons     int                           `json:"online_comparisons"`
	OnlineOfflineMismatch int                           `json:"online_offline_mismatch"`
	UnavailableArtifacts  int                           `json:"unavailable_artifacts"`
	Reason                string                        `json:"reason,omitempty"`
}

const offlineGoalEvaluationSchema = "offline-goal-evaluation-v2"

func writeCoverageGuidanceArtifacts(
	directory string, mode coverageguidance.Mode, completed int, elapsedMillis int64,
	offlineGoals bool, reusableGoals *offlineGoalEvaluationArtifact,
) (coverageguidance.Summary, coverageguidance.CrossCoverageSummary, error) {
	observations, err := persistence.ReadJSONLines[coverageguidance.CoverageObservation](
		filepath.Join(directory, "coverage-observations.jsonl"), completed)
	if err != nil {
		return coverageguidance.Summary{}, coverageguidance.CrossCoverageSummary{}, err
	}
	decisions, err := persistence.ReadJSONLines[coverageguidance.Decision](
		filepath.Join(directory, "corpus-decisions.jsonl"), completed)
	if err != nil {
		return coverageguidance.Summary{}, coverageguidance.CrossCoverageSummary{}, err
	}
	summary, cross, err := coverageguidance.Summarize(mode, observations, decisions, elapsedMillis)
	if err != nil {
		return coverageguidance.Summary{}, coverageguidance.CrossCoverageSummary{}, err
	}
	if err := persistence.WriteJSONAtomic(
		filepath.Join(directory, "coverage-guidance-summary.json"), summary); err != nil {
		return coverageguidance.Summary{}, coverageguidance.CrossCoverageSummary{}, err
	}
	if err := persistence.WriteJSONAtomic(
		filepath.Join(directory, "cross-coverage-summary.json"), cross); err != nil {
		return coverageguidance.Summary{}, coverageguidance.CrossCoverageSummary{}, err
	}
	if err := writeGrowthCSV(filepath.Join(directory, "facet-growth.csv"), summary,
		[]string{"election", "replication", "snapshot", "recovery", "network"}); err != nil {
		return coverageguidance.Summary{}, coverageguidance.CrossCoverageSummary{}, err
	}
	if err := writeGrowthCSV(filepath.Join(directory, "interaction-growth.csv"), summary,
		[]string{
			"interaction:election_network", "interaction:replication_network",
			"interaction:snapshot_recovery", "interaction:recovery_term_relation",
		}); err != nil {
		return coverageguidance.Summary{}, coverageguidance.CrossCoverageSummary{}, err
	}
	if err := writeCorpusEfficiencyCSV(
		filepath.Join(directory, "corpus-efficiency.csv"), observations, decisions); err != nil {
		return coverageguidance.Summary{}, coverageguidance.CrossCoverageSummary{}, err
	}
	goalArtifact := offlineGoalEvaluationArtifact{
		Schema: offlineGoalEvaluationSchema, Enabled: offlineGoals,
		Status: "not_requested", Goals: make(map[string]offlineGoalSummary),
		Runs: make([]offlineGoalRunEvaluation, 0),
	}
	if offlineGoals {
		if reusableGoals != nil && reusableGoals.Schema == offlineGoalEvaluationSchema &&
			reusableGoals.Enabled &&
			(reusableGoals.Status == "complete" || reusableGoals.Status == "partial") &&
			len(reusableGoals.Runs) == completed*2 {
			goalArtifact = *reusableGoals
		} else {
			goalArtifact, err = evaluateOfflineGoals(directory, observations)
			if err != nil {
				return coverageguidance.Summary{}, coverageguidance.CrossCoverageSummary{}, err
			}
		}
		cross.GoalAReached = goalArtifact.Goals[string(goalsearch.GoalSnapshotCatchUpAfterPartition)].Reached
		cross.GoalBReached = goalArtifact.Goals[string(goalsearch.GoalRestartHigherTermMessage)].Reached
		if err := persistence.WriteJSONAtomic(
			filepath.Join(directory, "cross-coverage-summary.json"), cross); err != nil {
			return coverageguidance.Summary{}, coverageguidance.CrossCoverageSummary{}, err
		}
	}
	if err := persistence.WriteJSONAtomic(
		filepath.Join(directory, "offline-goal-evaluation.json"), goalArtifact); err != nil {
		return coverageguidance.Summary{}, coverageguidance.CrossCoverageSummary{}, err
	}
	return summary, cross, nil
}

func evaluateOfflineGoals(
	directory string, observations []coverageguidance.CoverageObservation,
) (offlineGoalEvaluationArtifact, error) {
	artifact := offlineGoalEvaluationArtifact{
		Schema: offlineGoalEvaluationSchema, Enabled: true, Status: "complete",
		Goals: make(map[string]offlineGoalSummary), Runs: make([]offlineGoalRunEvaluation, 0),
	}
	var config cliConfig
	if err := persistence.ReadJSON(filepath.Join(directory, "config.json"), &config); err != nil {
		return artifact, err
	}
	runs, err := persistence.ReadJSONLines[experiment.Run](
		filepath.Join(directory, "runs.jsonl"), len(observations))
	if err != nil {
		return artifact, err
	}
	goals := []goalsearch.GoalID{
		goalsearch.GoalSnapshotCatchUpAfterPartition,
		goalsearch.GoalRestartHigherTermMessage,
	}
	cumulativeActions := 0
	for index, run := range runs {
		runDirectory := filepath.Join(directory, fmt.Sprintf("run-%04d-seed-%d", run.Index, run.Seed))
		var result engine.Result
		resultErr := persistence.ReadJSON(filepath.Join(runDirectory, "result.json"), &result)
		for _, goalID := range goals {
			evaluation := offlineGoalRunEvaluation{
				RunIndex: run.Index, Seed: run.Seed, GoalID: goalID,
				TargetStep: -1, TargetPlanAction: -1,
				ReachedWaypoints: make([]string, 0),
			}
			summary := artifact.Goals[string(goalID)]
			if summary.GoalID == "" {
				summary = offlineGoalSummary{
					GoalID: goalID, FirstCandidate: -1, FirstCumulativeAction: -1,
					WaypointReached:          make(map[string]int),
					DeepestWaypointHistogram: make(map[int]int),
				}
			}
			summary.Runs++
			if resultErr != nil {
				evaluation.Error = resultErr.Error()
				artifact.UnavailableArtifacts++
			} else {
				definition, definitionErr := goalsearch.Definition(goalID, len(config.Raft.NodeIDs))
				if definitionErr != nil {
					return artifact, definitionErr
				}
				recomputed, recomputeErr := goalsearch.Recompute(goalsearch.ArtifactInput{
					Definition:  definition,
					InstanceID:  fmt.Sprintf("offline-%s-%04d", goalID, run.Index),
					ModelConfig: config.Model, Initial: result.Initial, Trace: result.Trace,
					ModelEvents: result.ModelEvents, Resolutions: result.Resolutions,
				})
				if recomputeErr != nil {
					evaluation.Error = recomputeErr.Error()
				} else {
					evaluation.Reached = recomputed.TargetReached
					evaluation.TargetStep = recomputed.TargetReachedStep
					evaluation.TargetPlanAction = recomputed.TargetReachedPlan
					evaluation.StableKey = recomputed.StableKey
					for waypointIndex, waypoint := range recomputed.Instance.WaypointResults {
						if !waypoint.Reached {
							continue
						}
						evaluation.DeepestWaypoint = waypointIndex + 1
						evaluation.ReachedWaypoints = append(evaluation.ReachedWaypoints, waypoint.WaypointID)
						summary.WaypointReached[waypoint.WaypointID]++
					}
					if recomputed.TargetReached {
						summary.Reached++
						if summary.FirstCandidate < 0 {
							summary.FirstCandidate = index + 1
							summary.FirstCumulativeAction = cumulativeActions + recomputed.TargetReachedStep + 1
						}
					}
				}
			}
			summary.DeepestWaypointHistogram[evaluation.DeepestWaypoint]++
			if hasGoalRelevantInteraction(observations[index], goalID) {
				summary.RelevantInteractionRuns++
			}
			artifact.Goals[string(goalID)] = summary
			artifact.Runs = append(artifact.Runs, evaluation)
		}
		cumulativeActions += run.Actions
	}
	if artifact.UnavailableArtifacts > 0 {
		artifact.Status = "partial"
		artifact.Reason = "one or more complete run result artifacts were unavailable"
	}
	// Formal breadth runs intentionally have no online Goal evaluator. Zero
	// comparisons and zero mismatches are reported separately rather than
	// claiming an online/offline check that did not happen.
	artifact.OnlineComparisons = 0
	artifact.OnlineOfflineMismatch = 0
	return artifact, nil
}

func hasGoalRelevantInteraction(
	observation coverageguidance.CoverageObservation, goalID goalsearch.GoalID,
) bool {
	switch goalID {
	case goalsearch.GoalSnapshotCatchUpAfterPartition:
		for _, value := range observation.InteractionKeys["snapshot_recovery"] {
			var projected struct {
				Value struct {
					SnapshotMode    string `json:"snapshot_mode"`
					SnapshotOutcome string `json:"snapshot_outcome"`
				} `json:"value"`
			}
			if json.Unmarshal([]byte(value.Value), &projected) == nil &&
				projected.Value.SnapshotMode != "no-snapshot" &&
				(projected.Value.SnapshotOutcome == "delivered" ||
					projected.Value.SnapshotOutcome == "installed") {
				return true
			}
		}
	case goalsearch.GoalRestartHigherTermMessage:
		for _, value := range observation.InteractionKeys["recovery_term_relation"] {
			var projected struct {
				Value struct {
					RecoveryPhase       string `json:"recovery_phase"`
					MessageTermRelation string `json:"message_term_relation"`
				} `json:"value"`
			}
			if json.Unmarshal([]byte(value.Value), &projected) == nil &&
				(projected.Value.RecoveryPhase == "restarted-waiting-catch-up" ||
					projected.Value.RecoveryPhase == "restarted-recovered") &&
				projected.Value.MessageTermRelation == "higher" {
				return true
			}
		}
	}
	return false
}

func writeGrowthCSV(path string, summary coverageguidance.Summary, names []string) error {
	var buffer bytes.Buffer
	writer := csv.NewWriter(&buffer)
	if err := writer.Write([]string{
		"mode", "dimension", "candidate", "cumulative_actions", "elapsed_millis",
		"new_units", "cumulative_units",
	}); err != nil {
		return err
	}
	for _, name := range names {
		dimension, found := summary.Dimensions[name]
		if !found {
			return fmt.Errorf("coverage summary lacks dimension %s", name)
		}
		for _, point := range dimension.Growth {
			if err := writer.Write([]string{
				string(summary.Mode), name, strconv.Itoa(point.Candidate),
				strconv.Itoa(point.CumulativeActions), strconv.FormatInt(point.ElapsedMillis, 10),
				strconv.Itoa(point.New), strconv.Itoa(point.Cumulative),
			}); err != nil {
				return err
			}
		}
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		return err
	}
	return os.WriteFile(path, buffer.Bytes(), 0o644)
}

func writeCorpusEfficiencyCSV(
	path string, observations []coverageguidance.CoverageObservation, decisions []coverageguidance.Decision,
) error {
	var buffer bytes.Buffer
	writer := csv.NewWriter(&buffer)
	if err := writer.Write([]string{
		"candidate", "candidate_id", "plan_key", "semantic_trace_digest", "admitted",
		"reason", "corpus_before", "corpus_after", "fixed_energy", "new_raw", "new_v2",
		"new_election", "new_replication", "new_snapshot", "new_recovery", "new_network",
		"new_interactions", "plan_actions",
	}); err != nil {
		return err
	}
	for index, decision := range decisions {
		interactions := 0
		for _, values := range decision.NewCoverageUnits.Interactions {
			interactions += len(values)
		}
		row := []string{
			strconv.Itoa(index + 1), decision.CandidateID, decision.PlanKey,
			observations[index].SemanticTraceDigest, strconv.FormatBool(decision.WasAdmitted),
			decision.AdmissionReason, strconv.Itoa(decision.CorpusSizeBefore),
			strconv.Itoa(decision.CorpusSizeAfter), strconv.Itoa(decision.FixedEnergy),
			strconv.Itoa(len(decision.NewCoverageUnits.Raw)),
			strconv.Itoa(len(decision.NewCoverageUnits.V2)),
			strconv.Itoa(len(decision.NewCoverageUnits.Facets["election"])),
			strconv.Itoa(len(decision.NewCoverageUnits.Facets["replication"])),
			strconv.Itoa(len(decision.NewCoverageUnits.Facets["snapshot"])),
			strconv.Itoa(len(decision.NewCoverageUnits.Facets["recovery"])),
			strconv.Itoa(len(decision.NewCoverageUnits.Facets["network"])),
			strconv.Itoa(interactions), strconv.Itoa(observations[index].ActionCount),
		}
		if err := writer.Write(row); err != nil {
			return err
		}
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		return err
	}
	return os.WriteFile(path, buffer.Bytes(), 0o644)
}
