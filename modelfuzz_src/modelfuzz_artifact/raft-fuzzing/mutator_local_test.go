package main

import (
	"math/rand"
	"testing"
)

func TestLocalizedModelFuzzMutatorOnlyChangesNearestCandidatePools(t *testing.T) {
	trace := NewList[*SchedulingChoice]()
	for step := 0; step < 30; step++ {
		if step%5 == 0 {
			trace.Append(&SchedulingChoice{Type: StopNode, Node: uint64(step + 1), Step: step})
		}
		trace.Append(&SchedulingChoice{
			Type: Node, From: uint64(step + 1), To: uint64(step + 101), MaxMessages: step + 1,
		})
	}

	mutator := NewLocalizedModelFuzzMutator()
	mutator.(RandomizedMutator).SetRandom(rand.New(rand.NewSource(17)))
	mutator.(GuidanceAwareMutator).SetGuidance(Guidance{NewStates: []StateAttribution{{
		Status: AttributionLocated,
		Origin: &EventOrigin{Step: 15},
	}}})

	localized := mutator.(*combinedMutator)
	localNodes := indexSet(mutationChoiceIndices(trace, Node, 20, localized.scope))
	localCrashes := indexSet(mutationChoiceIndices(trace, StopNode, 4, localized.scope))
	mutated, ok := mutator.Mutate(trace, NewList[*Event]())
	if !ok {
		t.Fatal("localized combined mutation failed")
	}

	changedInside := false
	for index, original := range trace.Iter() {
		updated, _ := mutated.Get(index)
		local := localNodes[index] || localCrashes[index]
		if !local && *original != *updated {
			t.Fatalf("choice %d outside local pools changed: before=%#v after=%#v", index, original, updated)
		}
		if local && *original != *updated {
			changedInside = true
		}
	}
	if !changedInside {
		t.Fatal("localized mutation did not change any choice in its candidate pools")
	}
}

func TestPrefixPreservingModelFuzzMutatorOnlyChangesSuffix(t *testing.T) {
	trace := NewList[*SchedulingChoice]()
	for step := 0; step < 40; step++ {
		if step%5 == 0 {
			trace.Append(&SchedulingChoice{Type: StopNode, Node: uint64(step + 1), Step: step})
		}
		trace.Append(&SchedulingChoice{
			Type: Node, From: uint64(step + 1), To: uint64(step + 101), MaxMessages: step + 1,
		})
	}

	mutator := NewPrefixPreservingModelFuzzMutator()
	mutator.(RandomizedMutator).SetRandom(rand.New(rand.NewSource(23)))
	// Guidance order must not affect the earliest preserved state boundary.
	mutator.(GuidanceAwareMutator).SetGuidance(Guidance{NewStates: []StateAttribution{
		{Status: AttributionLocated, Origin: &EventOrigin{Step: 20}},
		{Status: AttributionLocated, Origin: &EventOrigin{Step: 10}},
	}})

	mutated, ok := mutator.Mutate(trace, NewList[*Event]())
	if !ok {
		t.Fatal("prefix-preserving combined mutation failed")
	}

	changedSuffix := false
	nodeStep := 0
	for index, original := range trace.Iter() {
		step := original.Step
		if original.Type == Node {
			step = nodeStep
			nodeStep++
		}
		updated, _ := mutated.Get(index)
		if step <= 10 && *original != *updated {
			t.Fatalf("choice %d at preserved step %d changed: before=%#v after=%#v", index, step, original, updated)
		}
		if step > 10 && *original != *updated {
			changedSuffix = true
		}
	}
	if !changedSuffix {
		t.Fatal("prefix-preserving mutation did not change the suffix")
	}
	stats := mutator.(PrefixMutationStatsProvider).PrefixMutationStats()
	if stats.Attempts != 1 || stats.Guided != 1 || stats.Generated != 1 || stats.GlobalFallback != 0 || stats.Rejected != 0 {
		t.Fatalf("unexpected successful suffix stats: %#v", stats)
	}
}

func TestPrefixPreservingModelFuzzMutatorDoesNotCrossBoundaryWhenSuffixIsTooShort(t *testing.T) {
	trace := NewList[*SchedulingChoice]()
	for step := 0; step < 40; step++ {
		if step%5 == 0 {
			trace.Append(&SchedulingChoice{Type: StopNode, Node: uint64(step + 1), Step: step})
		}
		trace.Append(&SchedulingChoice{Type: Node, From: 1, To: 2, MaxMessages: step + 1})
	}

	mutator := NewPrefixPreservingModelFuzzMutator()
	mutator.(RandomizedMutator).SetRandom(rand.New(rand.NewSource(29)))
	mutator.(GuidanceAwareMutator).SetGuidance(Guidance{NewStates: []StateAttribution{{
		Status: AttributionLocated,
		Origin: &EventOrigin{Step: 35},
	}}})

	if mutated, ok := mutator.Mutate(trace, NewList[*Event]()); ok || mutated != nil {
		t.Fatalf("short suffix unexpectedly fell back across the preserved boundary: %#v", mutated)
	}
	stats := mutator.(PrefixMutationStatsProvider).PrefixMutationStats()
	if stats.Attempts != 1 || stats.Guided != 1 || stats.Rejected != 1 || stats.Generated != 0 || stats.GlobalFallback != 0 {
		t.Fatalf("unexpected rejected suffix stats: %#v", stats)
	}
}

func TestPrefixPreservingModelFuzzMutatorFallsBackWithoutLocatedOrigin(t *testing.T) {
	trace := NewList[*SchedulingChoice]()
	for step := 0; step < 40; step++ {
		if step%5 == 0 {
			trace.Append(&SchedulingChoice{Type: StopNode, Node: uint64(step + 1), Step: step})
		}
		trace.Append(&SchedulingChoice{Type: Node, From: uint64(step + 1), To: 1, MaxMessages: step + 1})
	}

	mutator := NewPrefixPreservingModelFuzzMutator()
	mutator.(RandomizedMutator).SetRandom(rand.New(rand.NewSource(31)))
	mutator.(GuidanceAwareMutator).SetGuidance(Guidance{NewStates: []StateAttribution{{
		Status: AttributionInitialState,
	}}})
	if _, ok := mutator.Mutate(trace, NewList[*Event]()); !ok {
		t.Fatal("initial-only guidance did not fall back to global candidates")
	}
	stats := mutator.(PrefixMutationStatsProvider).PrefixMutationStats()
	if stats.Attempts != 1 || stats.GlobalFallback != 1 || stats.Generated != 1 || stats.Guided != 0 || stats.Rejected != 0 {
		t.Fatalf("unexpected initial-only fallback stats: %#v", stats)
	}
}

func indexSet(indices []int) map[int]bool {
	result := make(map[int]bool, len(indices))
	for _, index := range indices {
		result[index] = true
	}
	return result
}

type countingMutator struct{ calls int }

func (m *countingMutator) Mutate(trace *List[*SchedulingChoice], _ *List[*Event]) (*List[*SchedulingChoice], bool) {
	m.calls++
	return trace, true
}

func TestMixedModelFuzzMutatorUsesExactInterleavedQuota(t *testing.T) {
	for _, localPercent := range []int{50, 70} {
		local := &countingMutator{}
		global := &countingMutator{}
		mixed := &mixedModelFuzzMutator{
			local: local, global: global, localPercent: localPercent, localCredit: 100 - localPercent,
		}
		for i := 0; i < 10; i++ {
			mixed.Mutate(NewList[*SchedulingChoice](), NewList[*Event]())
		}
		wantLocal := localPercent / 10
		if local.calls != wantLocal || global.calls != 10-wantLocal {
			t.Fatalf("%d%% quota selected local/global=%d/%d, want %d/%d",
				localPercent, local.calls, global.calls, wantLocal, 10-wantLocal)
		}
		statsLocal, statsGlobal := mixed.MutationSelectionStats()
		if statsLocal != local.calls || statsGlobal != global.calls {
			t.Fatalf("%d%% selection stats=%d/%d, calls=%d/%d",
				localPercent, statsLocal, statsGlobal, local.calls, global.calls)
		}
	}
}
