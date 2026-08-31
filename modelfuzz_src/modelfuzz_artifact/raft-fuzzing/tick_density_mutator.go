package main

import (
	"math/rand"
	"time"
)

// tickDensityMutator moves one logical tick between two step boundaries. It
// preserves the trace's total TickAll budget and never exceeds maxBurst.
// Located targets restrict both boundaries to the suffix after the target.
type tickDensityMutator struct {
	maxBurst int
	rand     *rand.Rand
	stats    TickDensityMutationStats
}

func NewTickDensityMutator(maxBurst int) Mutator {
	return &tickDensityMutator{
		maxBurst: maxBurst,
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

	tickIndices := make([]int, 0)
	for index, choice := range trace.Iter() {
		if choice.Type == TickAll && choice.Step > cutoff {
			tickIndices = append(tickIndices, index)
		}
	}
	pairs := make([]mutationPair, 0)
	for _, donorIndex := range tickIndices {
		donor, _ := trace.Get(donorIndex)
		if donor.Count == 0 {
			continue
		}
		for _, receiverIndex := range tickIndices {
			if receiverIndex == donorIndex {
				continue
			}
			receiver, _ := trace.Get(receiverIndex)
			if receiver.Count < m.maxBurst {
				pairs = append(pairs, mutationPair{first: donorIndex, second: receiverIndex})
			}
		}
	}
	if len(pairs) == 0 {
		m.stats.Rejected++
		return nil, false
	}

	pair := pairs[m.rand.Intn(len(pairs))]
	mutated := copyTrace(trace, defaultCopyFilter())
	donor, _ := mutated.Get(pair.first)
	receiver, _ := mutated.Get(pair.second)
	donorCopy := donor.Copy()
	receiverCopy := receiver.Copy()
	donorCopy.Count--
	receiverCopy.Count++
	mutated.Set(pair.first, donorCopy)
	mutated.Set(pair.second, receiverCopy)
	m.stats.Generated++
	return mutated, true
}

func (m *tickDensityMutator) TickDensityMutationStats() TickDensityMutationStats {
	return m.stats
}

var _ Mutator = (*tickDensityMutator)(nil)
var _ RandomizedMutator = (*tickDensityMutator)(nil)
var _ TargetedGuidanceMutator = (*tickDensityMutator)(nil)
var _ TickDensityMutationStatsProvider = (*tickDensityMutator)(nil)
