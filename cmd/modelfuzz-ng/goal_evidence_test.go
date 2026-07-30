package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/goalsearch"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/persistence"
)

func TestBranchEvidenceSummaryRecomputesDeterministicallyFromJSONL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "branch-evidence.jsonl")
	journal, err := persistence.OpenJournal(path)
	if err != nil {
		t.Fatal(err)
	}
	records := []branchEvidenceRecord{
		{
			SchemaVersion: goalsearch.BranchEvidenceSchemaVersion,
			RunID:         "candidate-000000", CandidateIndex: 0,
			PlannedTemplateID: goalsearch.BranchBHigherVote,
			Vector: goalsearch.BranchEvidenceVector{
				SchemaVersion:    goalsearch.BranchEvidenceSchemaVersion,
				BranchTemplateID: goalsearch.BranchBHigherVote,
				HighestLevel:     goalsearch.EvidenceLevelSupported,
				SupportedCount:   1, NecessaryCount: 1,
				Commitment: goalsearch.BranchCommitment{FirstStep: -1},
				Dimensions: []goalsearch.BranchEvidenceDimension{{
					EvidenceID:        "vote.target-crashed",
					Status:            goalsearch.EvidenceSupported,
					FirstObservedStep: 4,
				}},
			},
			CompletedWaypoints: 2, NewEvidence: true,
		},
		{
			SchemaVersion: goalsearch.BranchEvidenceSchemaVersion,
			RunID:         "candidate-000001", CandidateIndex: 1,
			PlannedTemplateID: goalsearch.BranchBHigherVote,
			Vector: goalsearch.BranchEvidenceVector{
				SchemaVersion:    goalsearch.BranchEvidenceSchemaVersion,
				BranchTemplateID: goalsearch.BranchBHigherVote,
				HighestLevel:     goalsearch.EvidenceLevelCommitted,
				SupportedCount:   4, NecessaryCount: 4,
				Commitment: goalsearch.BranchCommitment{
					Reached: true, FirstStep: 9, StableKey: "commit-vote",
				},
				Dimensions: []goalsearch.BranchEvidenceDimension{{
					EvidenceID:        "vote.target-crashed",
					Status:            goalsearch.EvidenceCommitted,
					FirstObservedStep: 4,
				}},
			},
			CompletedWaypoints: 4, NewGoalProgress: true,
			NewEvidence: true, NewCommitment: true,
		},
		{
			SchemaVersion: goalsearch.BranchEvidenceSchemaVersion,
			RunID:         "candidate-000002", CandidateIndex: 2,
			PlannedTemplateID: goalsearch.BranchBHigherVote,
			Vector: goalsearch.BranchEvidenceVector{
				SchemaVersion:    goalsearch.BranchEvidenceSchemaVersion,
				BranchTemplateID: goalsearch.BranchBHigherVote,
				HighestLevel:     goalsearch.EvidenceLevelFullRealized,
				SupportedCount:   5, NecessaryCount: 4,
				Commitment: goalsearch.BranchCommitment{
					Reached: true, FirstStep: 9, StableKey: "commit-vote",
				},
				FullRealized: true, RealizedDecidable: true,
			},
			CompletedWaypoints: 5, NewGoalProgress: true,
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
	first, err := recomputeBranchEvidenceSummary(path, 3)
	if err != nil {
		t.Fatal(err)
	}
	second, err := recomputeBranchEvidenceSummary(path, 3)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) {
		firstJSON, _ := json.Marshal(first)
		secondJSON, _ := json.Marshal(second)
		t.Fatalf("evidence summary is unstable:\n%s\n%s", firstJSON, secondJSON)
	}
	if first.PlannedCount != 3 || first.SupportedCount != 3 ||
		first.CommittedCount != 2 || first.FullRealizedCount != 1 ||
		first.CommitmentToNextWaypointCount != 1 {
		t.Fatalf("evidence summary=%+v", first)
	}
	if first.ByEvidence["vote.target-crashed"].NextStageSuccessCount != 2 {
		t.Fatalf("per-evidence utility=%+v", first.ByEvidence)
	}
}

func TestWriteEvidenceCSV(t *testing.T) {
	path := filepath.Join(t.TempDir(), "utility.csv")
	if err := writeEvidenceCSV(path, [][]string{{"id", "utility"}, {"e1", "0.5"}}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "id,utility\ne1,0.5\n" {
		t.Fatalf("CSV=%q", raw)
	}
}
