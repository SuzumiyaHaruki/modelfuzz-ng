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
