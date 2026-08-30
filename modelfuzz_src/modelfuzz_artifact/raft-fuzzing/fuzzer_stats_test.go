package main

import "testing"

type alwaysInterestingGuider struct{}

func (*alwaysInterestingGuider) Check(*List[*SchedulingChoice], *List[*Event]) (int, float64) {
	return 1, 1
}
func (*alwaysInterestingGuider) LastGuidance() Guidance  { return Guidance{} }
func (*alwaysInterestingGuider) Coverage() CoverageStats { return CoverageStats{} }
func (*alwaysInterestingGuider) Reset(string)            {}

type onceMutator struct{ used bool }

func (m *onceMutator) Mutate(trace *List[*SchedulingChoice], _ *List[*Event]) (*List[*SchedulingChoice], bool) {
	if m.used {
		return nil, false
	}
	m.used = true
	return copyTrace(trace, defaultCopyFilter()), true
}

func TestFuzzerSeparatesExecutionKinds(t *testing.T) {
	fuzzer := NewFuzzer(&FuzzerConfig{
		Iterations:         4,
		Steps:              3,
		Mutator:            &onceMutator{},
		Guider:             &alwaysInterestingGuider{},
		MutPerTrace:        1,
		SeedPopulationSize: 1,
		MaxMessages:        2,
		ReseedFrequency:    100,
		RandomSeed:         17,
		RaftEnvironmentConfig: RaftEnvironmentConfig{
			Replicas: 3, ElectionTick: 20, HeartbeatTick: 4, TicksPerStep: 1,
		},
	})
	fuzzer.Run()

	want := map[string]int{
		"seed_generation_executions": 1,
		"seed_replay_executions":     1,
		"mutation_executions":        1,
		"random_executions":          2,
		"feedback_executions":        4,
		"total_executions":           5,
	}
	for name, expected := range want {
		if actual := fuzzer.stats[name].(int); actual != expected {
			t.Fatalf("%s=%d, want %d", name, actual, expected)
		}
	}
}
