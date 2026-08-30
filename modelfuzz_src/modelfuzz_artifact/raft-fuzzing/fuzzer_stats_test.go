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

type targetedTestGuider struct {
	checks   int
	guidance Guidance
}

func (g *targetedTestGuider) Check(*List[*SchedulingChoice], *List[*Event]) (int, float64) {
	g.checks++
	if g.checks == 1 {
		return len(g.guidance.NewStates), 1
	}
	return 0, 0
}

func (g *targetedTestGuider) LastGuidance() Guidance              { return g.guidance.Copy() }
func (*targetedTestGuider) Coverage() CoverageStats               { return CoverageStats{} }
func (*targetedTestGuider) Reset(string)                          {}
func (*targetedTestGuider) LastExecutionContainsState(int64) bool { return true }

type targetRecordingMutator struct {
	targets []int64
}

func (m *targetRecordingMutator) Mutate(trace *List[*SchedulingChoice], _ *List[*Event]) (*List[*SchedulingChoice], bool) {
	return copyTrace(trace, defaultCopyFilter()), true
}

func (m *targetRecordingMutator) MutateForTarget(trace *List[*SchedulingChoice], _ *List[*Event], target StateAttribution) (*List[*SchedulingChoice], bool) {
	m.targets = append(m.targets, target.State.Key)
	return copyTrace(trace, defaultCopyFilter()), true
}

func TestFuzzerGeneratesMutationsForEachNewStateTarget(t *testing.T) {
	guidance := Guidance{NewStates: []StateAttribution{
		{State: State{Key: 20}, Status: AttributionLocated, Origin: &EventOrigin{Step: 20}},
		{State: State{Key: 35}, Status: AttributionLocated, Origin: &EventOrigin{Step: 35}},
		{State: State{Key: 48}, Status: AttributionLocated, Origin: &EventOrigin{Step: 48}},
		{State: State{Key: 65}, Status: AttributionLocated, Origin: &EventOrigin{Step: 65}},
	}}
	guider := &targetedTestGuider{guidance: guidance}
	mutator := &targetRecordingMutator{}
	fuzzer := NewFuzzer(&FuzzerConfig{
		Iterations:         2,
		Steps:              3,
		Mutator:            mutator,
		Guider:             guider,
		MutPerTrace:        2,
		SeedPopulationSize: 1,
		MaxMessages:        2,
		ReseedFrequency:    100,
		RandomSeed:         41,
		RaftEnvironmentConfig: RaftEnvironmentConfig{
			Replicas: 3, ElectionTick: 20, HeartbeatTick: 4, TicksPerStep: 1,
		},
	})
	fuzzer.Run()

	wantTargets := []int64{20, 20, 35, 35, 48, 48, 65, 65}
	if len(mutator.targets) != len(wantTargets) {
		t.Fatalf("target calls=%v, want %v", mutator.targets, wantTargets)
	}
	for i := range wantTargets {
		if mutator.targets[i] != wantTargets[i] {
			t.Fatalf("target calls=%v, want %v", mutator.targets, wantTargets)
		}
	}
	if got := fuzzer.stats["prefix_target_executions"].(int); got != 1 {
		t.Fatalf("prefix target executions=%d, want 1", got)
	}
	if got := fuzzer.stats["prefix_target_preserved"].(int); got != 1 {
		t.Fatalf("preserved targets=%d, want 1", got)
	}
}
