package main

import (
	"encoding/json"
	"testing"
)

func newDeterministicTestFuzzer(seed int64) *Fuzzer {
	return NewFuzzer(&FuzzerConfig{
		Iterations: 1,
		Steps:      20,
		Mutator: CombineMutators(
			NewSwapCrashNodeMutator(1),
			NewSwapNodeMutator(3),
			NewSwapMaxMessagesMutator(3),
		),
		Guider: NewTLCStateGuider("127.0.0.1:1", "", false),
		RaftEnvironmentConfig: RaftEnvironmentConfig{
			Replicas:      3,
			ElectionTick:  20,
			HeartbeatTick: 4,
			TicksPerStep:  3,
		},
		MutPerTrace:        3,
		SeedPopulationSize: 1,
		NumberRequests:     1,
		CrashQuota:         4,
		MaxMessages:        5,
		ReseedFrequency:    200,
		RandomSeed:         seed,
	})
}

func TestRandomSeedReproducesExecutionAndMutationTraces(t *testing.T) {
	first := newDeterministicTestFuzzer(20260830)
	second := newDeterministicTestFuzzer(20260830)

	firstTrace, firstEvents := first.RunIteration("first", nil)
	secondTrace, secondEvents := second.RunIteration("second", nil)
	assertSameJSON(t, "execution trace", firstTrace, secondTrace)
	assertSameJSON(t, "event trace", firstEvents, secondEvents)

	firstMutation, firstOK := first.config.Mutator.Mutate(firstTrace, firstEvents)
	secondMutation, secondOK := second.config.Mutator.Mutate(secondTrace, secondEvents)
	if !firstOK || !secondOK {
		t.Fatalf("deterministic mutators failed: first=%t second=%t", firstOK, secondOK)
	}
	assertSameJSON(t, "mutated trace", firstMutation, secondMutation)
}

func assertSameJSON(t *testing.T, name string, first, second interface{}) {
	t.Helper()
	firstJSON, err := json.Marshal(first)
	if err != nil {
		t.Fatalf("marshal first %s: %v", name, err)
	}
	secondJSON, err := json.Marshal(second)
	if err != nil {
		t.Fatalf("marshal second %s: %v", name, err)
	}
	if string(firstJSON) != string(secondJSON) {
		t.Fatalf("%s differs with the same seed\nfirst:  %s\nsecond: %s", name, firstJSON, secondJSON)
	}
}
