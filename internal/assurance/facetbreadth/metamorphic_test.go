package facetbreadth

import (
	"fmt"
	"reflect"
	"sync"
	"testing"

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/assurance/facet"
	raftfacet "github.com/SuzumiyaHaruki/modelfuzz-ng/internal/assurance/facet/raft"
)

func TestStateDeterministicAcrossRepeatedApplySequences(t *testing.T) {
	record, evaluations := testRecordAndEvaluations(t)
	base := mustSummary(t, record, evaluations)
	var want CoverageSnapshotV1
	for repetition := 0; repetition < 20; repetition++ {
		state, err := NewCoverageStateV1(mustCatalog(t))
		if err != nil {
			t.Fatal(err)
		}
		for ordinal := uint64(0); ordinal < 10; ordinal++ {
			summary := copySummary(base)
			summary.RecordDigest = testDigest(fmt.Sprintf("record-%d", ordinal))
			summary.CandidateID = fmt.Sprintf("candidate-%d", ordinal)
			summary.PlanActionCount = 3 + int(ordinal)
			summary.SummaryDigest = mustSummaryDigest(t, summary)
			if _, err := state.Apply(ordinal, summary); err != nil {
				t.Fatal(err)
			}
		}
		got := state.Snapshot()
		if repetition == 0 {
			want = got
		} else if !reflect.DeepEqual(want, got) {
			t.Fatalf("repetition %d produced different state", repetition)
		}
		digest, err := state.Digest()
		if err != nil {
			t.Fatal(err)
		}
		if digest != got.StateDigest {
			t.Fatal("Digest and Snapshot disagree")
		}
	}
}

func TestStateCompactAfterTenThousandNoNoveltyApplies(t *testing.T) {
	state, err := NewCoverageStateV1(mustCatalog(t))
	if err != nil {
		t.Fatal(err)
	}
	record, evaluations := testRecordAndEvaluations(t)
	summary := mustSummary(t, record, evaluations)
	if _, err := state.Apply(0, summary); err != nil {
		t.Fatal(err)
	}
	initialCovered := len(state.Snapshot().Covered)
	for ordinal := uint64(1); ordinal <= 10_000; ordinal++ {
		next := copySummary(summary)
		next.RecordDigest = testDigest(fmt.Sprintf("repeat-%d", ordinal))
		next.CandidateID = "repeat"
		next.PlanActionCount = summary.PlanActionCount + 1
		next.SummaryDigest = mustSummaryDigest(t, next)
		decision, err := state.Apply(ordinal, next)
		if err != nil {
			t.Fatal(err)
		}
		if decision.Reason != DecisionNoNovelty {
			t.Fatalf("ordinal %d reason=%s", ordinal, decision.Reason)
		}
	}
	snapshot := state.Snapshot()
	if len(snapshot.Covered) != initialCovered {
		t.Fatalf("covered grew from %d to %d", initialCovered, len(snapshot.Covered))
	}
	if snapshot.AppliedCandidateCount != 10_001 || snapshot.NextApplyOrdinal != 10_001 {
		t.Fatalf("counts did not advance: %+v", snapshot)
	}
	if len(snapshot.Covered)*2 > MaxRepresentativeSlotsV1 {
		t.Fatal("representative slot bound exceeded")
	}
}

func TestMaximumCatalogBoundIsThirtyOneKeysAndSixtyTwoSlots(t *testing.T) {
	record, _ := testRecordAndEvaluations(t)
	evaluations := make([]facet.EvaluationV1, 0, len(raftfacet.CatalogV1()))
	for _, evaluator := range raftfacet.CatalogV1() {
		definition := evaluator.Definition()
		observations := make([]facet.ObservationV1, 0, len(definition.Classes))
		for classIndex, class := range definition.Classes {
			occurrence := facet.TraceStepAfterOccurrence(classIndex)
			if definition.Scope == facet.ScopeTransition {
				occurrence = facet.TransitionEffectOccurrence(classIndex, 0)
			}
			observation, err := facet.NewObservation(definition, class.ID, occurrence, "")
			if err != nil {
				t.Fatal(err)
			}
			observations = append(observations, observation)
		}
		evaluation, err := facet.NewEvaluation(
			definition, facet.StatusEvaluated, observations, "",
		)
		if err != nil {
			t.Fatal(err)
		}
		evaluations = append(evaluations, evaluation)
	}
	summary := mustSummary(t, record, evaluations)
	state, err := NewCoverageStateV1(mustCatalog(t))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := state.Apply(0, summary); err != nil {
		t.Fatal(err)
	}
	snapshot := state.Snapshot()
	if got := len(snapshot.Covered); got != MaxCatalogKeysV1 {
		t.Fatalf("covered=%d want %d", got, MaxCatalogKeysV1)
	}
	if got := len(snapshot.Covered) * 2; got != MaxRepresentativeSlotsV1 {
		t.Fatalf("slots=%d want %d", got, MaxRepresentativeSlotsV1)
	}
}

func TestConcurrentReadOnlySnapshotAndDigest(t *testing.T) {
	state, err := NewCoverageStateV1(mustCatalog(t))
	if err != nil {
		t.Fatal(err)
	}
	record, evaluations := testRecordAndEvaluations(t)
	if _, err := state.Apply(0, mustSummary(t, record, evaluations)); err != nil {
		t.Fatal(err)
	}
	want := state.Snapshot()
	var wait sync.WaitGroup
	for worker := 0; worker < 16; worker++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			for iteration := 0; iteration < 100; iteration++ {
				if got := state.Snapshot(); !reflect.DeepEqual(want, got) {
					t.Errorf("concurrent snapshot differs")
					return
				}
				digest, err := state.Digest()
				if err != nil || digest != want.StateDigest {
					t.Errorf("concurrent digest=%q err=%v", digest, err)
					return
				}
			}
		}()
	}
	wait.Wait()
	if after := state.Snapshot(); !reflect.DeepEqual(want, after) {
		t.Fatal("read-only calls mutated state")
	}
}

func TestCoverageStatesDoNotShareData(t *testing.T) {
	catalog := mustCatalog(t)
	left, err := NewCoverageStateV1(catalog)
	if err != nil {
		t.Fatal(err)
	}
	right, err := NewCoverageStateV1(catalog)
	if err != nil {
		t.Fatal(err)
	}
	record, evaluations := testRecordAndEvaluations(t)
	if _, err := left.Apply(0, mustSummary(t, record, evaluations)); err != nil {
		t.Fatal(err)
	}
	if len(right.Snapshot().Covered) != 0 || right.Snapshot().NextApplyOrdinal != 0 {
		t.Fatal("independent state was mutated")
	}
}
