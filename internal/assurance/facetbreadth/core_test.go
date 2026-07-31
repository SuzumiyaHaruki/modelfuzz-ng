package facetbreadth

import (
	"crypto/sha256"
	"encoding/hex"
	"reflect"
	"testing"

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/assurance/executionrecord"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/assurance/facet"
	raftfacet "github.com/SuzumiyaHaruki/modelfuzz-ng/internal/assurance/facet/raft"
)

func TestFrozenCatalogAndSummaryCanonicalization(t *testing.T) {
	catalog, err := BuildCatalogIdentityV1(raftfacet.CatalogV1())
	if err != nil {
		t.Fatal(err)
	}
	if got, want := len(catalog.Facets), 3; got != want {
		t.Fatalf("facets=%d want %d", got, want)
	}
	total := 0
	for _, identity := range catalog.Facets {
		total += len(identity.ClassIDs)
	}
	if total != 31 {
		t.Fatalf("classes=%d want 31", total)
	}

	record, evaluations := testRecordAndEvaluations(t)
	reversed := append([]facet.EvaluationV1(nil), evaluations...)
	for left, right := 0, len(reversed)-1; left < right; left, right = left+1, right-1 {
		reversed[left], reversed[right] = reversed[right], reversed[left]
	}
	first, err := BuildCandidateSummaryV1(record, evaluations)
	if err != nil {
		t.Fatal(err)
	}
	second, err := BuildCandidateSummaryV1(record, reversed)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatal("input order changed canonical summary")
	}
}

func testDigest(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}

func TestCoverageStateApplyReasonsAndAtomicity(t *testing.T) {
	catalog, err := BuildCatalogIdentityV1(raftfacet.CatalogV1())
	if err != nil {
		t.Fatal(err)
	}
	state, err := NewCoverageStateV1(catalog)
	if err != nil {
		t.Fatal(err)
	}
	record, evaluations := testRecordAndEvaluations(t)
	summary, err := BuildCandidateSummaryV1(record, evaluations)
	if err != nil {
		t.Fatal(err)
	}
	first, err := state.Apply(0, summary)
	if err != nil {
		t.Fatal(err)
	}
	if first.Reason != DecisionNewFacetClass || !first.Admitted {
		t.Fatalf("first decision=%+v", first)
	}
	before := state.Snapshot()
	if _, err := state.Apply(0, summary); err == nil {
		t.Fatal("repeated ordinal accepted")
	}
	if after := state.Snapshot(); !reflect.DeepEqual(before, after) {
		t.Fatal("failed Apply mutated state")
	}
	second, err := state.Apply(1, summary)
	if err != nil {
		t.Fatal(err)
	}
	if second.Reason != DecisionNoNovelty || second.Admitted {
		t.Fatalf("second decision=%+v", second)
	}
}

func testRecordAndEvaluations(t *testing.T) (executionrecord.CompletedExecutionRecordV1, []facet.EvaluationV1) {
	t.Helper()
	record := executionrecord.CompletedExecutionRecordV1{
		SchemaID:     executionrecord.SchemaIDV1,
		MajorVersion: executionrecord.MajorVersionV1,
		RecordDigest: testDigest("record"),
		Candidate: executionrecord.CandidateIdentity{
			ID: "candidate-1", RunIndex: 0,
		},
		Plan:  executionrecord.PlanSummary{Digest: testDigest("plan"), ActionCount: 3},
		Trace: executionrecord.TraceSummary{Digest: testDigest("trace"), StepCount: 4},
	}
	evaluations := make([]facet.EvaluationV1, 0, 3)
	for _, evaluator := range raftfacet.CatalogV1() {
		definition := evaluator.Definition()
		if definition.ID == "raft.snapshot_lifecycle_event" {
			evaluation, err := facet.NewEvaluation(definition, facet.StatusNotApplicable, nil, "ignored")
			if err != nil {
				t.Fatal(err)
			}
			evaluations = append(evaluations, evaluation)
			continue
		}
		observation, err := facet.NewObservation(
			definition, definition.Classes[0].ID, facet.ExplicitInitialOccurrence(), "ignored",
		)
		if err != nil {
			t.Fatal(err)
		}
		evaluation, err := facet.NewEvaluation(definition, facet.StatusEvaluated, []facet.ObservationV1{observation}, "ignored")
		if err != nil {
			t.Fatal(err)
		}
		evaluations = append(evaluations, evaluation)
	}
	return record, evaluations
}
