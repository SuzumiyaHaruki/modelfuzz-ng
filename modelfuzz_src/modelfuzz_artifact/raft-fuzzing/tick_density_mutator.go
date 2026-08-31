package main

import (
	"math/rand"
	"time"
)

// tickDensityMutator moves logical ticks between two step boundaries. It
// preserves the trace's total TickAll budget and never exceeds maxBurst.
// All configured deltas for one target reuse the same randomly selected pair,
// so strength is the only difference between those children.
type tickDensityMutator struct {
	maxBurst int
	deltas   []int
	rand     *rand.Rand
	stats    TickDensityMutationStats

	activeTarget tickDensityTarget
	activePair   mutationPair
	activeValid  bool
	hasActive    bool
	nextDelta    int
	lastDelta    int
	lastApplied  bool
}

type tickDensityTarget struct {
	status   string
	stateKey int64
	step     int
}

func NewTickDensityMutator(maxBurst int, deltas ...int) Mutator {
	if len(deltas) == 0 {
		deltas = []int{1}
	}
	return &tickDensityMutator{
		maxBurst: maxBurst,
		deltas:   append([]int(nil), deltas...),
		rand:     rand.New(rand.NewSource(time.Now().UnixNano())),
	}
}

func (m *tickDensityMutator) SetRandom(random *rand.Rand) {
	m.rand = random
}

func (m *tickDensityMutator) Mutate(trace *List[*SchedulingChoice], eventTrace *List[*Event]) (*List[*SchedulingChoice], bool) {
	return m.MutateForTarget(trace, eventTrace, StateAttribution{Status: AttributionInitialState})
}

func (m *tickDensityMutator) MutateForTarget(trace *List[*SchedulingChoice], _ *List[*Event], target StateAttribution) (*List[*SchedulingChoice], bool) {
	m.stats.Attempts++
	cutoff := -1
	if target.Status == AttributionLocated && target.Origin != nil {
		cutoff = target.Origin.Step
		m.stats.Guided++
	} else {
		m.stats.GlobalFallback++
	}
	targetID := tickDensityTarget{status: target.Status, stateKey: target.State.Key, step: cutoff}
	if !m.hasActive || m.activeTarget != targetID || m.nextDelta >= len(m.deltas) {
		m.activeTarget = targetID
		m.activePair, m.activeValid = m.selectPair(trace, cutoff, maxIntSlice(m.deltas))
		m.hasActive = true
		m.nextDelta = 0
	}
	delta := m.deltas[m.nextDelta]
	m.nextDelta++
	m.lastDelta = delta
	m.lastApplied = false
	if !m.activeValid {
		m.stats.Rejected++
		return nil, false
	}

	mutated := copyTrace(trace, defaultCopyFilter())
	donor, _ := mutated.Get(m.activePair.first)
	receiver, _ := mutated.Get(m.activePair.second)
	donorCopy := donor.Copy()
	receiverCopy := receiver.Copy()
	donorCopy.Count -= delta
	receiverCopy.Count += delta
	mutated.Set(m.activePair.first, donorCopy)
	mutated.Set(m.activePair.second, receiverCopy)
	m.stats.Generated++
	m.lastApplied = true
	return mutated, true
}

func (m *tickDensityMutator) selectPair(trace *List[*SchedulingChoice], cutoff, delta int) (mutationPair, bool) {
	tickIndices := make([]int, 0)
	for index, choice := range trace.Iter() {
		if choice.Type == TickAll && choice.Step > cutoff {
			tickIndices = append(tickIndices, index)
		}
	}
	pairs := make([]mutationPair, 0)
	for _, donorIndex := range tickIndices {
		donor, _ := trace.Get(donorIndex)
		if donor.Count < delta {
			continue
		}
		for _, receiverIndex := range tickIndices {
			if receiverIndex == donorIndex {
				continue
			}
			receiver, _ := trace.Get(receiverIndex)
			if receiver.Count+delta <= m.maxBurst {
				pairs = append(pairs, mutationPair{first: donorIndex, second: receiverIndex})
			}
		}
	}
	if len(pairs) == 0 {
		return mutationPair{}, false
	}
	return pairs[m.rand.Intn(len(pairs))], true
}

func (m *tickDensityMutator) TickDensityMutationStats() TickDensityMutationStats {
	return m.stats
}

func (m *tickDensityMutator) LastTickDensityDelta() (int, bool) {
	return m.lastDelta, m.lastApplied
}

func maxIntSlice(values []int) int {
	result := values[0]
	for _, value := range values[1:] {
		result = max(result, value)
	}
	return result
}

var _ Mutator = (*tickDensityMutator)(nil)
var _ RandomizedMutator = (*tickDensityMutator)(nil)
var _ TargetedGuidanceMutator = (*tickDensityMutator)(nil)
var _ TickDensityMutationStatsProvider = (*tickDensityMutator)(nil)
var _ TickDensityMutationMetadataProvider = (*tickDensityMutator)(nil)
