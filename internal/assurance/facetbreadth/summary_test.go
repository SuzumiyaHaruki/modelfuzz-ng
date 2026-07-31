package facetbreadth

import (
	"reflect"
	"testing"

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/assurance/facet"
	raftfacet "github.com/SuzumiyaHaruki/modelfuzz-ng/internal/assurance/facet/raft"
)

func TestCandidateSummaryFieldsAndExclusions(t *testing.T) {
	record, evaluations := testRecordAndEvaluations(t)
	summary, err := BuildCandidateSummaryV1(record, evaluations)
	if err != nil {
		t.Fatal(err)
	}
	if summary.RecordDigest != record.RecordDigest ||
		summary.CandidateID != record.Candidate.ID ||
		summary.RunIndex != record.Candidate.RunIndex ||
		summary.PlanDigest != record.Plan.Digest ||
		summary.PlanActionCount != record.Plan.ActionCount ||
		summary.TraceDigest != record.Trace.Digest ||
		summary.TraceStepCount != record.Trace.StepCount {
		t.Fatalf("summary did not consume record facts: %+v", summary)
	}
	if len(summary.Evaluations) != 3 || !validDigest(summary.SummaryDigest) {
		t.Fatal("summary identity is incomplete")
	}
}

func TestCandidateSummaryCanonicalizesKeysAndEarliestOccurrence(t *testing.T) {
	record, evaluations := testRecordAndEvaluations(t)
	definition := raftfacet.NewElectionRoleTermShapeV1().Definition()
	late, err := facet.NewObservation(
		definition, definition.Classes[1].ID, facet.TraceStepAfterOccurrence(4), "late",
	)
	if err != nil {
		t.Fatal(err)
	}
	early := late.Copy()
	early.Occurrence = facet.TraceStepAfterOccurrence(1)
	evaluations[0] = facet.EvaluationV1{
		FacetID: definition.ID, FacetVersion: definition.Version, Status: facet.StatusEvaluated,
		Observations: []facet.ObservationV1{late, early},
	}
	summary, err := BuildCandidateSummaryV1(record, evaluations)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(summary.Evaluations[0].Keys); got != 1 {
		t.Fatalf("deduplicated keys=%d want 1", got)
	}
	if got := *summary.Evaluations[0].Keys[0].FirstOccurrence.StepIndex; got != 1 {
		t.Fatalf("first occurrence step=%d want 1", got)
	}
}

func TestCandidateSummaryRejectsMalformedEvaluations(t *testing.T) {
	record, base := testRecordAndEvaluations(t)
	definition := raftfacet.NewElectionRoleTermShapeV1().Definition()
	observation, err := facet.NewObservation(
		definition, definition.Classes[0].ID, facet.ExplicitInitialOccurrence(), "",
	)
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]func([]facet.EvaluationV1) []facet.EvaluationV1{
		"missing": func(values []facet.EvaluationV1) []facet.EvaluationV1 {
			return values[:2]
		},
		"duplicate": func(values []facet.EvaluationV1) []facet.EvaluationV1 {
			values[1] = values[0]
			return values
		},
		"unknown": func(values []facet.EvaluationV1) []facet.EvaluationV1 {
			values[0].FacetID = "raft.unknown"
			return values
		},
		"bad status": func(values []facet.EvaluationV1) []facet.EvaluationV1 {
			values[0].Status = facet.EvaluationStatus("bad")
			return values
		},
		"non evaluated observations": func(values []facet.EvaluationV1) []facet.EvaluationV1 {
			values[0].Status = facet.StatusNotApplicable
			return values
		},
		"evaluated empty": func(values []facet.EvaluationV1) []facet.EvaluationV1 {
			values[0].Observations = nil
			return values
		},
		"bad digest": func(values []facet.EvaluationV1) []facet.EvaluationV1 {
			values[0].Observations[0].KeyDigest = testDigest("wrong")
			return values
		},
		"bad class": func(values []facet.EvaluationV1) []facet.EvaluationV1 {
			values[0].Observations[0].Key.ClassID = "unknown"
			return values
		},
		"bad scope": func(values []facet.EvaluationV1) []facet.EvaluationV1 {
			values[0].Observations[0].Key.Scope = facet.ScopeTransition
			return values
		},
		"bad occurrence": func(values []facet.EvaluationV1) []facet.EvaluationV1 {
			values[0].Observations[0].Occurrence = facet.Occurrence{}
			return values
		},
		"contradictory duplicate": func(values []facet.EvaluationV1) []facet.EvaluationV1 {
			conflict := observation.Copy()
			conflict.KeyDigest = testDigest("conflict")
			values[0].Observations = append(values[0].Observations, conflict)
			return values
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			values := copyFacetEvaluations(base)
			if _, err := BuildCandidateSummaryV1(record, mutate(values)); err == nil {
				t.Fatal("malformed evaluation accepted")
			}
		})
	}
}

func TestCandidateSummaryRejectsMalformedRecord(t *testing.T) {
	record, evaluations := testRecordAndEvaluations(t)
	cases := []func(){
		func() { record.SchemaID = "bad" },
		func() { record.RecordDigest = "bad" },
		func() { record.Plan.Digest = "bad" },
		func() { record.Trace.Digest = "bad" },
		func() { record.Candidate.ID = "" },
		func() { record.Candidate.RunIndex = -1 },
		func() { record.Plan.ActionCount = -1 },
		func() { record.Trace.StepCount = -1 },
	}
	for index, mutate := range cases {
		original := record
		mutate()
		if _, err := BuildCandidateSummaryV1(record, evaluations); err == nil {
			t.Fatalf("malformed record case %d accepted", index)
		}
		record = original
	}
}

func TestCandidateSummaryDefensiveCopiesAndIgnoresDebugText(t *testing.T) {
	record, evaluations := testRecordAndEvaluations(t)
	first, err := BuildCandidateSummaryV1(record, evaluations)
	if err != nil {
		t.Fatal(err)
	}
	evaluations[0].Detail = "changed"
	evaluations[0].Observations[0].Explanation = "changed"
	second, err := BuildCandidateSummaryV1(record, evaluations)
	if err != nil {
		t.Fatal(err)
	}
	if first.SummaryDigest != second.SummaryDigest || !reflect.DeepEqual(first, second) {
		t.Fatal("debug text affected summary")
	}
	evaluations[0].Observations[0].Key.ClassID = "mutated"
	if first.Evaluations[0].Keys[0].Key.ClassID == "mutated" {
		t.Fatal("summary aliases input")
	}
	first.Evaluations[0].Keys[0].CanonicalString = "mutated"
	third, err := BuildCandidateSummaryV1(record, copyFacetEvaluations(secondToEvaluations(t, second)))
	if err != nil {
		t.Fatal(err)
	}
	if third.Evaluations[0].Keys[0].CanonicalString == "mutated" {
		t.Fatal("summary mutation polluted later builds")
	}
}

func copyFacetEvaluations(values []facet.EvaluationV1) []facet.EvaluationV1 {
	result := make([]facet.EvaluationV1, len(values))
	for index, value := range values {
		result[index] = value.Copy()
	}
	return result
}

func secondToEvaluations(t *testing.T, summary CandidateFacetSummaryV1) []facet.EvaluationV1 {
	t.Helper()
	result := make([]facet.EvaluationV1, len(summary.Evaluations))
	for index, evaluation := range summary.Evaluations {
		result[index] = facet.EvaluationV1{
			FacetID: evaluation.FacetID, FacetVersion: evaluation.FacetVersion,
			Status: evaluation.Status,
		}
		for _, key := range evaluation.Keys {
			result[index].Observations = append(result[index].Observations, facet.ObservationV1{
				Key: key.Key, KeyDigest: key.KeyDigest, Occurrence: key.FirstOccurrence.Copy(),
			})
		}
	}
	return result
}
