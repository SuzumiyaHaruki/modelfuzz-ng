// Frozen diagnostic helpers for Branch/Evidence artifact recomputation.
// They are retained because accepted experiments and compatibility tests read
// the persisted schemas.
package main

import (
	"bufio"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/goalsearch"
)

func writeEvidenceCSV(path string, rows [][]string) error {
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	writer := csv.NewWriter(file)
	writeErr := writer.WriteAll(rows)
	writer.Flush()
	flushErr := writer.Error()
	closeErr := file.Close()
	return errors.Join(writeErr, flushErr, closeErr)
}

type branchEvidenceRecord struct {
	SchemaVersion      string                          `json:"schema_version"`
	RunID              string                          `json:"run_id"`
	CandidateIndex     int                             `json:"candidate_index"`
	PlannedTemplateID  goalsearch.BranchTemplateID     `json:"planned_branch_template_id"`
	Vector             goalsearch.BranchEvidenceVector `json:"evidence_vector"`
	CompletedWaypoints int                             `json:"completed_waypoint_count"`
	GoalReached        bool                            `json:"goal_reached"`
	BugDetected        bool                            `json:"bug_detected"`
	NewFacet           bool                            `json:"new_facet"`
	NewGoalProgress    bool                            `json:"new_goal_progress"`
	NewEvidence        bool                            `json:"new_evidence"`
	NewCommitment      bool                            `json:"new_commitment"`
}

type perEvidenceAggregate struct {
	ObservedCount         int     `json:"observed_count"`
	FirstObservedStep     int     `json:"first_observed_step"`
	InvalidationCount     int     `json:"invalidation_count"`
	NextStageSuccessCount int     `json:"next_stage_success_count"`
	Utility               float64 `json:"utility"`
	FalseProgressCount    int     `json:"false_progress_count"`
	SampleSufficient      bool    `json:"sample_sufficient"`
}

type branchEvidenceSummary struct {
	SchemaVersion                 string                           `json:"schema_version"`
	PlannedCount                  int                              `json:"planned_branch_count"`
	SupportedCount                int                              `json:"supported_branch_count"`
	CommittedCount                int                              `json:"committed_branch_count"`
	RealizedDecidableCount        int                              `json:"realized_decidable_count"`
	FullRealizedCount             int                              `json:"full_realized_count"`
	ContradictedCount             int                              `json:"contradicted_count"`
	SupportedRate                 float64                          `json:"supported_branch_rate"`
	CommitmentRate                float64                          `json:"commitment_reach_rate"`
	FullRealizedRate              float64                          `json:"full_realized_rate"`
	CommitmentToNextWaypointCount int                              `json:"commitment_to_next_waypoint_count"`
	CommitmentToNextWaypointRate  float64                          `json:"commitment_to_next_waypoint_rate"`
	EvidenceWithoutGoalProgress   int                              `json:"evidence_progress_without_goal_progress"`
	GoalProgressWithoutEvidence   int                              `json:"goal_progress_without_evidence_progress"`
	CommitmentWithoutFullRealized int                              `json:"commitment_without_full_realized"`
	BugBeforeCommitment           int                              `json:"bug_before_commitment"`
	BugAfterCommitment            int                              `json:"bug_after_commitment"`
	ByEvidence                    map[string]perEvidenceAggregate  `json:"by_evidence"`
	ByLevel                       map[goalsearch.EvidenceLevel]int `json:"by_evidence_level"`
	UtilityWindowCandidates       int                              `json:"utility_window_candidates"`
}

func recomputeBranchEvidenceSummary(
	path string,
	window int,
) (branchEvidenceSummary, error) {
	summary := branchEvidenceSummary{
		SchemaVersion:           goalsearch.BranchEvidenceSchemaVersion,
		ByEvidence:              make(map[string]perEvidenceAggregate),
		ByLevel:                 make(map[goalsearch.EvidenceLevel]int),
		UtilityWindowCandidates: window,
	}
	records, err := readBranchEvidenceRecords(path)
	if err != nil {
		return summary, err
	}
	for index, record := range records {
		vector := record.Vector
		summary.PlannedCount++
		summary.ByLevel[vector.HighestLevel]++
		if vector.SupportedCount > 0 {
			summary.SupportedCount++
		}
		if vector.Commitment.Reached {
			summary.CommittedCount++
		}
		if vector.RealizedDecidable {
			summary.RealizedDecidableCount++
		}
		if vector.FullRealized {
			summary.FullRealizedCount++
		}
		if vector.Contradicted {
			summary.ContradictedCount++
		}
		if record.NewEvidence && !record.NewGoalProgress {
			summary.EvidenceWithoutGoalProgress++
		}
		if record.NewGoalProgress && !record.NewEvidence {
			summary.GoalProgressWithoutEvidence++
		}
		if vector.Commitment.Reached && !vector.FullRealized {
			summary.CommitmentWithoutFullRealized++
		}
		if record.BugDetected {
			if vector.Commitment.Reached {
				summary.BugAfterCommitment++
			} else {
				summary.BugBeforeCommitment++
			}
		}
		for _, dimension := range vector.Dimensions {
			aggregate := summary.ByEvidence[dimension.EvidenceID]
			if aggregate.FirstObservedStep == 0 && aggregate.ObservedCount == 0 {
				aggregate.FirstObservedStep = -1
			}
			observed := dimension.Status == goalsearch.EvidenceSupported ||
				dimension.Status == goalsearch.EvidenceCommitted
			if observed {
				aggregate.ObservedCount++
				if aggregate.FirstObservedStep < 0 ||
					dimension.FirstObservedStep < aggregate.FirstObservedStep {
					aggregate.FirstObservedStep = dimension.FirstObservedStep
				}
				if evidenceHasNextStage(records, index, record, window) {
					aggregate.NextStageSuccessCount++
				} else {
					aggregate.FalseProgressCount++
				}
			}
			if dimension.Status == goalsearch.EvidenceInvalidated {
				aggregate.InvalidationCount++
			}
			summary.ByEvidence[dimension.EvidenceID] = aggregate
		}
		if vector.Commitment.Reached &&
			evidenceHasNextWaypoint(records, index, record, window) {
			summary.CommitmentToNextWaypointCount++
		}
	}
	if summary.CommittedCount > 0 {
		summary.CommitmentToNextWaypointRate =
			float64(summary.CommitmentToNextWaypointCount) / float64(summary.CommittedCount)
	}
	if summary.PlannedCount > 0 {
		summary.SupportedRate = float64(summary.SupportedCount) / float64(summary.PlannedCount)
		summary.CommitmentRate = float64(summary.CommittedCount) / float64(summary.PlannedCount)
		summary.FullRealizedRate = float64(summary.FullRealizedCount) / float64(summary.PlannedCount)
	}
	for id, aggregate := range summary.ByEvidence {
		if aggregate.ObservedCount > 0 {
			aggregate.Utility =
				float64(aggregate.NextStageSuccessCount) / float64(aggregate.ObservedCount)
		}
		aggregate.SampleSufficient = aggregate.ObservedCount >= 10
		summary.ByEvidence[id] = aggregate
	}
	return summary, nil
}

func readBranchEvidenceRecords(path string) ([]branchEvidenceRecord, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	var records []branchEvidenceRecord
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		var record branchEvidenceRecord
		if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
			return nil, fmt.Errorf("decode branch evidence: %w", err)
		}
		if record.SchemaVersion != goalsearch.BranchEvidenceSchemaVersion {
			return nil, fmt.Errorf("branch evidence schema=%q want %q",
				record.SchemaVersion, goalsearch.BranchEvidenceSchemaVersion)
		}
		records = append(records, record)
	}
	return records, scanner.Err()
}

func evidenceHasNextStage(
	records []branchEvidenceRecord,
	index int,
	record branchEvidenceRecord,
	window int,
) bool {
	end := min(len(records), index+window+1)
	currentRank := goalsearch.EvidenceLevelRank(record.Vector.HighestLevel)
	for _, next := range records[index+1 : end] {
		if next.PlannedTemplateID != record.PlannedTemplateID {
			continue
		}
		if goalsearch.EvidenceLevelRank(next.Vector.HighestLevel) > currentRank ||
			next.CompletedWaypoints > record.CompletedWaypoints {
			return true
		}
	}
	return false
}

func evidenceHasNextWaypoint(
	records []branchEvidenceRecord,
	index int,
	record branchEvidenceRecord,
	window int,
) bool {
	end := min(len(records), index+window+1)
	for _, next := range records[index+1 : end] {
		if next.PlannedTemplateID == record.PlannedTemplateID &&
			next.CompletedWaypoints > record.CompletedWaypoints {
			return true
		}
	}
	return false
}

func evidenceUtilityRows(summary branchEvidenceSummary) [][]string {
	ids := make([]string, 0, len(summary.ByEvidence))
	for id := range summary.ByEvidence {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	rows := [][]string{{
		"evidence_id", "observed_count", "first_observed_step",
		"next_stage_success_count", "utility", "false_progress_count",
		"invalidation_count", "sample_sufficient",
	}}
	for _, id := range ids {
		value := summary.ByEvidence[id]
		rows = append(rows, []string{
			id,
			fmt.Sprint(value.ObservedCount),
			fmt.Sprint(value.FirstObservedStep),
			fmt.Sprint(value.NextStageSuccessCount),
			fmt.Sprintf("%.6f", value.Utility),
			fmt.Sprint(value.FalseProgressCount),
			fmt.Sprint(value.InvalidationCount),
			fmt.Sprint(value.SampleSufficient),
		})
	}
	return rows
}
