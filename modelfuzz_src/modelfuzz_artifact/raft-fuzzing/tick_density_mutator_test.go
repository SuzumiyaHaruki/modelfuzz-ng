package main

import (
	"math/rand"
	"testing"
)

func TestTickDensityMutatorPreservesBudgetAndTargetPrefix(t *testing.T) {
	trace := tickTrace(10, 3)
	mutator := NewTickDensityMutator(6)
	mutator.(RandomizedMutator).SetRandom(rand.New(rand.NewSource(43)))
	target := StateAttribution{Status: AttributionLocated, Origin: &EventOrigin{Step: 4}}
	mutated, ok := mutator.(TargetedGuidanceMutator).MutateForTarget(trace, NewList[*Event](), target)
	if !ok {
		t.Fatal("tick-density mutation failed")
	}

	beforeTotal, afterTotal := 0, 0
	changed := 0
	for i := 0; i < trace.Size(); i++ {
		before, _ := trace.Get(i)
		after, _ := mutated.Get(i)
		beforeTotal += before.Count
		afterTotal += after.Count
		if before.Step <= 4 && *before != *after {
			t.Fatalf("target prefix changed at step %d: before=%#v after=%#v", before.Step, before, after)
		}
		if *before != *after {
			changed++
		}
		if after.Count < 0 || after.Count > 6 {
			t.Fatalf("tick count outside [0,6] at step %d: %d", after.Step, after.Count)
		}
	}
	if beforeTotal != afterTotal {
		t.Fatalf("tick budget changed: before=%d after=%d", beforeTotal, afterTotal)
	}
	if changed != 2 {
		t.Fatalf("changed tick boundaries=%d, want 2", changed)
	}
	stats := mutator.(TickDensityMutationStatsProvider).TickDensityMutationStats()
	if stats.Attempts != 1 || stats.Guided != 1 || stats.Generated != 1 || stats.Rejected != 0 {
		t.Fatalf("unexpected tick-density stats: %#v", stats)
	}
}

func TestTickDensityMutatorRejectsTargetWithoutTwoSuffixBoundaries(t *testing.T) {
	mutator := NewTickDensityMutator(6)
	target := StateAttribution{Status: AttributionLocated, Origin: &EventOrigin{Step: 8}}
	if mutated, ok := mutator.(TargetedGuidanceMutator).MutateForTarget(tickTrace(10, 3), NewList[*Event](), target); ok || mutated != nil {
		t.Fatalf("late target unexpectedly produced mutation: %#v", mutated)
	}
}

func TestTickDensityMultiscaleUsesOnePairForAllDeltas(t *testing.T) {
	trace := tickTrace(10, 3)
	mutator := NewTickDensityMutator(6, 1, 2, 3)
	mutator.(RandomizedMutator).SetRandom(rand.New(rand.NewSource(59)))
	target := StateAttribution{State: State{Key: 7}, Status: AttributionLocated, Origin: &EventOrigin{Step: 4}}
	var changedSteps []int
	for _, delta := range []int{1, 2, 3} {
		mutated, ok := mutator.(TargetedGuidanceMutator).MutateForTarget(trace, NewList[*Event](), target)
		if !ok {
			t.Fatalf("delta %d mutation failed", delta)
		}
		currentSteps := make([]int, 0, 2)
		total := 0
		for index, before := range trace.Iter() {
			after, _ := mutated.Get(index)
			total += after.Count
			if before.Count != after.Count {
				currentSteps = append(currentSteps, before.Step)
				if abs(before.Count-after.Count) != delta {
					t.Fatalf("step %d changed by %d for delta %d", before.Step, abs(before.Count-after.Count), delta)
				}
			}
		}
		if total != 30 {
			t.Fatalf("delta %d changed total Tick budget to %d", delta, total)
		}
		if len(currentSteps) != 2 {
			t.Fatalf("delta %d changed steps %v, want two", delta, currentSteps)
		}
		if changedSteps == nil {
			changedSteps = currentSteps
		} else if changedSteps[0] != currentSteps[0] || changedSteps[1] != currentSteps[1] {
			t.Fatalf("deltas used different boundaries: first=%v delta%d=%v", changedSteps, delta, currentSteps)
		}
		if got, applied := mutator.(TickDensityMutationMetadataProvider).LastTickDensityDelta(); !applied || got != delta {
			t.Fatalf("last delta=(%d,%t), want (%d,true)", got, applied, delta)
		}
	}
	stats := mutator.(TickDensityMutationStatsProvider).TickDensityMutationStats()
	if stats.Attempts != 3 || stats.Guided != 3 || stats.Generated != 3 || stats.Rejected != 0 {
		t.Fatalf("unexpected multiscale stats: %#v", stats)
	}
}

func TestExplicitTickTraceReplaysItsDensity(t *testing.T) {
	fuzzer := NewFuzzer(&FuzzerConfig{
		Steps:         4,
		Mutator:       &EmptyMutator{},
		ExplicitTicks: true,
		MaxMessages:   2,
		RandomSeed:    47,
		RaftEnvironmentConfig: RaftEnvironmentConfig{
			Replicas: 3, ElectionTick: 20, HeartbeatTick: 4, TicksPerStep: 3,
		},
	})
	trace, _ := fuzzer.RunIteration("seed", nil)
	want := map[int]int{0: 2, 1: 4, 2: 1, 3: 5}
	mutated := copyTrace(trace, defaultCopyFilter())
	for _, choice := range mutated.Iter() {
		if choice.Type == TickAll {
			choice.Count = want[choice.Step]
		}
	}
	replayed, _ := fuzzer.RunIteration("replay", mutated)
	got := make(map[int]int)
	for _, choice := range replayed.Iter() {
		if choice.Type == TickAll {
			got[choice.Step] = choice.Count
		}
	}
	if len(got) != len(want) {
		t.Fatalf("replayed TickAll choices=%v, want %v", got, want)
	}
	for step, count := range want {
		if got[step] != count {
			t.Fatalf("replayed tick count at step %d=%d, want %d", step, got[step], count)
		}
	}
}

func TestLegacyTraceDoesNotAddTickChoices(t *testing.T) {
	fuzzer := newDeterministicTestFuzzer(53)
	trace, _ := fuzzer.RunIteration("legacy", nil)
	for _, choice := range trace.Iter() {
		if choice.Type == TickAll {
			t.Fatalf("legacy trace unexpectedly contains TickAll choice: %#v", choice)
		}
	}
}

func tickTrace(steps, count int) *List[*SchedulingChoice] {
	trace := NewList[*SchedulingChoice]()
	for step := 0; step < steps; step++ {
		trace.Append(&SchedulingChoice{Type: TickAll, Step: step, Count: count})
	}
	return trace
}
