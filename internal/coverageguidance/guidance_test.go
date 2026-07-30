package coverageguidance

import (
	"reflect"
	"testing"
)

func observation(candidate, plan string, raw, v2 int64, facet, interaction int64) CoverageObservation {
	result := CoverageObservation{
		RunID: "run-" + candidate, CandidateID: candidate, PlanKey: plan, TraceKey: "trace-" + candidate,
		Outcome:            Outcome{Status: "completed", Succeeded: true},
		RawTLCFingerprints: []CoverageValue{{Key: raw, Value: "raw"}},
		V2StateKeys:        []CoverageValue{{Key: v2, Value: "v2"}},
		FacetKeys: map[string][]CoverageValue{
			"election": {{Key: facet, Value: "election"}},
		},
		InteractionKeys: map[string][]CoverageValue{
			"election_network": {{Key: interaction, Value: "interaction"}},
		},
	}
	_ = NormalizeObservation(&result)
	return result
}

func TestSchemaAndUnknownMode(t *testing.T) {
	if SchemaVersion != "raft-online-coverage-guidance-v1-prototype" {
		t.Fatalf("schema = %q", SchemaVersion)
	}
	if _, err := ParseMode("unknown"); err == nil {
		t.Fatal("unknown mode accepted")
	}
}

func TestFixedGuidanceDecisions(t *testing.T) {
	tests := []struct {
		mode Mode
		new  CoverageObservation
	}{
		{ModeRawFixed, observation("b", "p2", 2, 1, 1, 1)},
		{ModeV2Fixed, observation("b", "p2", 1, 2, 1, 1)},
		{ModeFacetFixed, observation("b", "p2", 1, 1, 2, 1)},
		{ModeFacetInteractionFixed, observation("b", "p2", 1, 1, 1, 2)},
	}
	for _, test := range tests {
		t.Run(string(test.mode), func(t *testing.T) {
			controller, err := New(Config{Mode: test.mode, FixedEnergy: 3, CorpusLimit: 10})
			if err != nil {
				t.Fatal(err)
			}
			first, err := controller.Observe(observation("a", "p1", 1, 1, 1, 1))
			if err != nil || !first.WasAdmitted || first.FixedEnergy != 3 {
				t.Fatalf("first decision = %+v err=%v", first, err)
			}
			second, err := controller.Observe(test.new)
			if err != nil || !second.WasAdmitted {
				t.Fatalf("novel decision = %+v err=%v", second, err)
			}
			old := observation("c", "p3", 1, 1, 1, 1)
			third, err := controller.Observe(old)
			if err != nil || third.WasAdmitted || third.AdmissionReason != "rejected_no_guidance_novelty" {
				t.Fatalf("old decision = %+v err=%v", third, err)
			}
		})
	}
}

func TestRandomDoesNotUseCoverageAndAllModesDeduplicatePlans(t *testing.T) {
	controller, _ := New(Config{Mode: ModeRandom, FixedEnergy: 2, CorpusLimit: 2})
	first, _ := controller.Observe(observation("a", "same", 1, 1, 1, 1))
	second, _ := controller.Observe(observation("b", "other", 1, 1, 1, 1))
	duplicate, _ := controller.Observe(observation("c", "same", 99, 99, 99, 99))
	limited, _ := controller.Observe(observation("d", "third", 100, 100, 100, 100))
	if !first.WasAdmitted || !second.WasAdmitted ||
		duplicate.AdmissionReason != "rejected_duplicate_plan" ||
		limited.AdmissionReason != "rejected_corpus_limit" {
		t.Fatalf("unexpected decisions: %+v %+v %+v %+v", first, second, duplicate, limited)
	}
}

func TestFacetNoveltyRemainsFactorizedAndRecomputeIsExact(t *testing.T) {
	observations := []CoverageObservation{
		observation("a", "p1", 1, 1, 1, 1),
		observation("b", "p2", 1, 1, 2, 2),
	}
	observations[1].FacetKeys["network"] = []CoverageValue{{Key: 7, Value: "network"}}
	_ = NormalizeObservation(&observations[1])
	config := Config{Mode: ModeFacetFixed, FixedEnergy: 4, CorpusLimit: 10}
	left, snapshot, err := Recompute(config, observations)
	if err != nil {
		t.Fatal(err)
	}
	right, repeated, err := Recompute(config, observations)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(left, right) || !reflect.DeepEqual(snapshot, repeated) {
		t.Fatal("recomputation differs")
	}
	if got := left[1].NewCoverageUnits.Facets["election"]; len(got) != 1 ||
		len(left[1].NewCoverageUnits.Facets["network"]) != 1 {
		t.Fatalf("facet novelty was not kept separate: %+v", left[1].NewCoverageUnits.Facets)
	}
}

func TestObservationTimingDoesNotChangeStableKey(t *testing.T) {
	first := observation("a", "p1", 1, 1, 1, 1)
	second := first
	second.Computation = ComputationTiming{
		RawNanos: 10, V2Nanos: 20, FrameNanos: 30, FacetNanos: 40,
		TotalNanos: 100, CorpusDecisionNanos: 5,
	}
	second.ElapsedMillis = 1234
	if err := NormalizeObservation(&second); err != nil {
		t.Fatal(err)
	}
	if first.StableKey != second.StableKey {
		t.Fatalf("timing changed stable key: %s != %s", first.StableKey, second.StableKey)
	}
}
