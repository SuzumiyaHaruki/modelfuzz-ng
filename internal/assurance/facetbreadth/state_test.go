package facetbreadth

import (
	"reflect"
	"strings"
	"testing"

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/assurance/executionrecord"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/assurance/facet"
	raftfacet "github.com/SuzumiyaHaruki/modelfuzz-ng/internal/assurance/facet/raft"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/engine"
)

func TestEligibilityDecisions(t *testing.T) {
	catalog := mustCatalog(t)
	record, evaluations := testRecordAndEvaluations(t)
	tests := []struct {
		name     string
		mutate   func([]facet.EvaluationV1) []facet.EvaluationV1
		eligible bool
	}{
		{"all evaluated", addSnapshotEvaluation(t), true},
		{"snapshot not applicable", func(values []facet.EvaluationV1) []facet.EvaluationV1 { return values }, true},
		{"election not applicable", setStatus(0, facet.StatusNotApplicable), false},
		{"replication not applicable", setStatus(1, facet.StatusNotApplicable), false},
		{"election invalid", setStatus(0, facet.StatusInvalidEvidence), false},
		{"replication insufficient", setStatus(1, facet.StatusInsufficientEvidence), false},
		{"snapshot invalid", setStatus(2, facet.StatusInvalidEvidence), false},
		{"zero key", func(values []facet.EvaluationV1) []facet.EvaluationV1 {
			for index := range values {
				values[index].Status = facet.StatusNotApplicable
				values[index].Observations = nil
			}
			return values
		}, false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			state, err := NewCoverageStateV1(catalog)
			if err != nil {
				t.Fatal(err)
			}
			summary, err := BuildCandidateSummaryV1(record, test.mutate(copyFacetEvaluations(evaluations)))
			if err != nil {
				t.Fatal(err)
			}
			decision, err := state.Apply(0, summary)
			if err != nil {
				t.Fatal(err)
			}
			got := decision.Reason != DecisionIneligibleEvidence
			if got != test.eligible {
				t.Fatalf("eligible=%v want %v decision=%+v", got, test.eligible, decision)
			}
			snapshot := state.Snapshot()
			if snapshot.NextApplyOrdinal != 1 || snapshot.AppliedCandidateCount != 1 {
				t.Fatal("successful Apply did not advance ordinal and count")
			}
		})
	}
}

func TestEligibilityDoesNotReclassifyCompletedOutcome(t *testing.T) {
	for _, status := range []engine.Status{
		engine.StatusRuntimeFailed,
		engine.StatusMappingFailed,
		engine.StatusOracleFailed,
		engine.StatusModelFailed,
	} {
		t.Run(string(status), func(t *testing.T) {
			record, evaluations := testRecordAndEvaluations(t)
			record.Engine.Status = status
			record.Experiment.Status = status
			record.Experiment.Succeeded = false
			summary := mustSummary(t, record, evaluations)
			state, err := NewCoverageStateV1(mustCatalog(t))
			if err != nil {
				t.Fatal(err)
			}
			decision, err := state.Apply(0, summary)
			if err != nil {
				t.Fatal(err)
			}
			if decision.Reason == DecisionIneligibleEvidence {
				t.Fatalf("engine/experiment status %s was reclassified as ineligible", status)
			}
		})
	}
}

func TestApplyRejectsCatalogAndEvaluationStructureMismatchAtomically(t *testing.T) {
	state, err := NewCoverageStateV1(mustCatalog(t))
	if err != nil {
		t.Fatal(err)
	}
	record, evaluations := testRecordAndEvaluations(t)
	summary := mustSummary(t, record, evaluations)
	before := state.Snapshot()
	cases := []func(CandidateFacetSummaryV1) CandidateFacetSummaryV1{
		func(value CandidateFacetSummaryV1) CandidateFacetSummaryV1 {
			value.CatalogFingerprint = testDigest("other-catalog")
			value.SummaryDigest = mustSummaryDigest(t, value)
			return value
		},
		func(value CandidateFacetSummaryV1) CandidateFacetSummaryV1 {
			value.Evaluations = value.Evaluations[:2]
			value.SummaryDigest = mustSummaryDigest(t, value)
			return value
		},
		func(value CandidateFacetSummaryV1) CandidateFacetSummaryV1 {
			value.Evaluations[1] = value.Evaluations[0]
			value.SummaryDigest = mustSummaryDigest(t, value)
			return value
		},
		func(value CandidateFacetSummaryV1) CandidateFacetSummaryV1 {
			value.Evaluations[0].Keys[0].CanonicalString = "bad"
			value.SummaryDigest = mustSummaryDigest(t, value)
			return value
		},
	}
	for index, mutate := range cases {
		if _, err := state.Apply(0, mutate(copySummary(summary))); err == nil {
			t.Fatalf("invalid summary case %d accepted", index)
		}
		if got := state.Snapshot(); !reflect.DeepEqual(before, got) {
			t.Fatalf("invalid summary case %d mutated state", index)
		}
	}
}

func TestApplyAllDecisionReasons(t *testing.T) {
	state, err := NewCoverageStateV1(mustCatalog(t))
	if err != nil {
		t.Fatal(err)
	}
	record, baseEvaluations := testRecordAndEvaluations(t)
	first := mustSummary(t, record, baseEvaluations)
	assertReason(t, state, 0, first, DecisionNewFacetClass)

	same := first
	same.RecordDigest = testDigest("same-record-2")
	same.CandidateID = "same-2"
	same.PlanActionCount = 99
	same.SummaryDigest = mustSummaryDigest(t, same)
	assertReason(t, state, 1, same, DecisionNoNovelty)

	shorter := same
	shorter.RecordDigest = testDigest("shorter")
	shorter.CandidateID = "shorter"
	shorter.PlanActionCount = 1
	shorter.SummaryDigest = mustSummaryDigest(t, shorter)
	assertReason(t, state, 2, shorter, DecisionShorterRepresentative)

	record.RecordDigest = testDigest("new-and-shorter")
	record.Candidate.ID = "new-and-shorter"
	record.Plan.ActionCount = 0
	withSnapshot := addSnapshotEvaluation(t)(copyFacetEvaluations(baseEvaluations))
	combined := mustSummary(t, record, withSnapshot)
	assertReason(t, state, 3, combined, DecisionNewAndShorter)

	ineligibleEvaluations := setStatus(0, facet.StatusNotApplicable)(copyFacetEvaluations(baseEvaluations))
	record.RecordDigest = testDigest("ineligible")
	record.Candidate.ID = "ineligible"
	ineligible := mustSummary(t, record, ineligibleEvaluations)
	assertReason(t, state, 4, ineligible, DecisionIneligibleEvidence)

	snapshot := state.Snapshot()
	if snapshot.AppliedCandidateCount != 5 || snapshot.EligibleCount != 4 ||
		snapshot.IneligibleCount != 1 || snapshot.NextApplyOrdinal != 5 {
		t.Fatalf("unexpected counters: %+v", snapshot)
	}
	for _, count := range snapshot.DecisionReasonCounts {
		if count.Count != 1 {
			t.Fatalf("reason %s count=%d want 1", count.Reason, count.Count)
		}
	}
}

func TestApplyIsAtomicOnValidationAndRepresentativeConflict(t *testing.T) {
	state, err := NewCoverageStateV1(mustCatalog(t))
	if err != nil {
		t.Fatal(err)
	}
	record, evaluations := testRecordAndEvaluations(t)
	summary := mustSummary(t, record, evaluations)
	if _, err := state.Apply(0, summary); err != nil {
		t.Fatal(err)
	}
	before := state.Snapshot()

	malformed := copySummary(summary)
	malformed.Evaluations[1].Keys[0].KeyDigest = testDigest("bad")
	malformed.SummaryDigest = mustSummaryDigest(t, malformed)
	if _, err := state.Apply(1, malformed); err == nil {
		t.Fatal("malformed later key accepted")
	}
	if got := state.Snapshot(); !reflect.DeepEqual(before, got) {
		t.Fatal("malformed key partially mutated state")
	}

	conflict := copySummary(summary)
	conflict.CandidateID = "conflicting-candidate"
	conflict.SummaryDigest = mustSummaryDigest(t, conflict)
	if _, err := state.Apply(1, conflict); err == nil {
		t.Fatal("conflicting representative identity accepted")
	}
	if got := state.Snapshot(); !reflect.DeepEqual(before, got) {
		t.Fatal("representative conflict partially mutated state")
	}
}

func TestFirstImmutableAndShortestComparator(t *testing.T) {
	base := RepresentativeRefV1{
		RecordDigest: strings.Repeat("f", 64), CandidateID: "candidate-a", RunIndex: 1,
		PlanDigest: strings.Repeat("f", 64), PlanActionCount: 5,
		TraceDigest: strings.Repeat("f", 64), TraceStepCount: 8, ApplyOrdinal: 0,
	}
	cases := []struct {
		name      string
		candidate RepresentativeRefV1
		shorter   bool
	}{
		{"fewer actions", mutateRef(base, func(ref *RepresentativeRefV1) { ref.PlanActionCount-- }), true},
		{"fewer steps", mutateRef(base, func(ref *RepresentativeRefV1) { ref.TraceStepCount-- }), true},
		{"smaller plan digest", mutateRef(base, func(ref *RepresentativeRefV1) { ref.PlanDigest = strings.Repeat("0", 64) }), true},
		{"smaller trace digest", mutateRef(base, func(ref *RepresentativeRefV1) { ref.TraceDigest = strings.Repeat("0", 64) }), true},
		{"smaller record digest", mutateRef(base, func(ref *RepresentativeRefV1) { ref.RecordDigest = strings.Repeat("0", 64) }), true},
		{"same", base, false},
		{"candidate ignored", mutateRef(base, func(ref *RepresentativeRefV1) { ref.CandidateID = "zzz" }), false},
		{"run ignored", mutateRef(base, func(ref *RepresentativeRefV1) { ref.RunIndex = 99 }), false},
		{"ordinal ignored", mutateRef(base, func(ref *RepresentativeRefV1) { ref.ApplyOrdinal = 99 }), false},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if got := shorterRepresentative(test.candidate, base); got != test.shorter {
				t.Fatalf("shorter=%v want %v", got, test.shorter)
			}
		})
	}

	state, err := NewCoverageStateV1(mustCatalog(t))
	if err != nil {
		t.Fatal(err)
	}
	record, evaluations := testRecordAndEvaluations(t)
	firstSummary := mustSummary(t, record, evaluations)
	if _, err := state.Apply(0, firstSummary); err != nil {
		t.Fatal(err)
	}
	firstBefore := state.Snapshot().Covered[0].First
	record.RecordDigest = testDigest("shortest-second")
	record.Candidate.ID = "shortest-second"
	record.Plan.ActionCount = 0
	if _, err := state.Apply(1, mustSummary(t, record, evaluations)); err != nil {
		t.Fatal(err)
	}
	after := state.Snapshot()
	if after.Covered[0].First != firstBefore {
		t.Fatal("shortest replacement changed immutable First")
	}
}

func TestApplyOrdinalErrorsDoNotAdvance(t *testing.T) {
	state, err := NewCoverageStateV1(mustCatalog(t))
	if err != nil {
		t.Fatal(err)
	}
	record, evaluations := testRecordAndEvaluations(t)
	summary := mustSummary(t, record, evaluations)
	for _, ordinal := range []uint64{1, 2, 99} {
		before := state.Snapshot()
		if _, err := state.Apply(ordinal, summary); err == nil {
			t.Fatalf("ordinal %d accepted", ordinal)
		}
		if after := state.Snapshot(); !reflect.DeepEqual(before, after) {
			t.Fatal("wrong ordinal changed state")
		}
	}
	if _, err := state.Apply(0, summary); err != nil {
		t.Fatal(err)
	}
	if _, err := state.Apply(0, summary); err == nil {
		t.Fatal("repeated ordinal accepted")
	}
}

func TestSnapshotAndDecisionAreDefensive(t *testing.T) {
	state, err := NewCoverageStateV1(mustCatalog(t))
	if err != nil {
		t.Fatal(err)
	}
	record, evaluations := testRecordAndEvaluations(t)
	decision, err := state.Apply(0, mustSummary(t, record, evaluations))
	if err != nil {
		t.Fatal(err)
	}
	want := state.Snapshot()
	decision.NewKeys[0] = "mutated"
	got := state.Snapshot()
	if !reflect.DeepEqual(want, got) {
		t.Fatal("Decision mutation polluted state")
	}
	got.Covered[0].CanonicalString = "mutated"
	got.Catalog.Facets[0].ClassIDs[0] = "mutated"
	got.DecisionReasonCounts[0].Count = 999
	if after := state.Snapshot(); !reflect.DeepEqual(want, after) {
		t.Fatal("Snapshot mutation polluted state")
	}
}

func mustCatalog(t *testing.T) CatalogIdentityV1 {
	t.Helper()
	catalog, err := BuildCatalogIdentityV1(raftfacet.CatalogV1())
	if err != nil {
		t.Fatal(err)
	}
	return catalog
}

func mustSummary(
	t *testing.T,
	record executionrecord.CompletedExecutionRecordV1,
	evaluations []facet.EvaluationV1,
) CandidateFacetSummaryV1 {
	t.Helper()
	summary, err := BuildCandidateSummaryV1(record, evaluations)
	if err != nil {
		t.Fatal(err)
	}
	return summary
}

func mustSummaryDigest(t *testing.T, summary CandidateFacetSummaryV1) string {
	t.Helper()
	digest, err := summaryDigest(summary)
	if err != nil {
		t.Fatal(err)
	}
	return digest
}

func setStatus(index int, status facet.EvaluationStatus) func([]facet.EvaluationV1) []facet.EvaluationV1 {
	return func(values []facet.EvaluationV1) []facet.EvaluationV1 {
		values[index].Status = status
		values[index].Observations = nil
		return values
	}
}

func addSnapshotEvaluation(t *testing.T) func([]facet.EvaluationV1) []facet.EvaluationV1 {
	t.Helper()
	return func(values []facet.EvaluationV1) []facet.EvaluationV1 {
		definition := raftfacet.NewSnapshotLifecycleEventV1().Definition()
		observation, err := facet.NewObservation(
			definition, definition.Classes[0].ID, facet.TransitionEffectOccurrence(0, 0), "",
		)
		if err != nil {
			t.Fatal(err)
		}
		evaluation, err := facet.NewEvaluation(
			definition, facet.StatusEvaluated, []facet.ObservationV1{observation}, "",
		)
		if err != nil {
			t.Fatal(err)
		}
		values[2] = evaluation
		return values
	}
}

func assertReason(
	t *testing.T,
	state *CoverageStateV1,
	ordinal uint64,
	summary CandidateFacetSummaryV1,
	want DecisionReasonV1,
) {
	t.Helper()
	decision, err := state.Apply(ordinal, summary)
	if err != nil {
		t.Fatal(err)
	}
	if decision.Reason != want {
		t.Fatalf("reason=%s want %s decision=%+v", decision.Reason, want, decision)
	}
	wantAdmitted := want == DecisionNewFacetClass ||
		want == DecisionShorterRepresentative || want == DecisionNewAndShorter
	if decision.Admitted != wantAdmitted {
		t.Fatalf("admitted=%v want %v", decision.Admitted, wantAdmitted)
	}
}

func mutateRef(base RepresentativeRefV1, mutate func(*RepresentativeRefV1)) RepresentativeRefV1 {
	result := base
	mutate(&result)
	return result
}
