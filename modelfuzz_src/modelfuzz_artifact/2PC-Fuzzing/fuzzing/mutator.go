package fuzzing

import (
	"math/rand"
	"slices"
)

type Mutator interface {
	Mutate([]Step) []Step
}

type SwapNodesMutator struct {
	Swaps  int
	Random *rand.Rand
}

func NewSwapNodesMutator(swaps int, rand *rand.Rand) *SwapNodesMutator {
	return &SwapNodesMutator{Swaps: swaps, Random: rand}
}

func (m *SwapNodesMutator) Mutate(schedule []Step) []Step {
	schIndexes := make([]int, 0)

	for i, s := range schedule {
		if s.Type == Schedule {
			schIndexes = append(schIndexes, i)
		}
	}

	for k := 0; k < m.Swaps; k++ {
		if len(schIndexes) < 2 {
			return schedule
		}
		i := m.Random.Intn(len(schIndexes))
		newIndexes := slices.Delete(slices.Clone(schIndexes), i, i+1)
		j := m.Random.Intn(len(newIndexes))

		nOne := schedule[schIndexes[i]]
		nTwo := schedule[newIndexes[j]]
		schedule[newIndexes[j]] = nOne
		schedule[schIndexes[i]] = nTwo
	}

	return schedule
}

type SwapMaxMessagesMutator struct {
	Swaps  int
	Random *rand.Rand
}

func NewSwapMaxMessagesMutator(swaps int, rand *rand.Rand) *SwapMaxMessagesMutator {
	return &SwapMaxMessagesMutator{Swaps: swaps, Random: rand}
}

func (m *SwapMaxMessagesMutator) Mutate(schedule []Step) []Step {
	schIndexes := make([]int, 0)

	for i, s := range schedule {
		if s.Type == Schedule {
			schIndexes = append(schIndexes, i)
		}
	}

	for k := 0; k < m.Swaps; k++ {
		if len(schIndexes) < 2 {
			return schedule
		}
		i := m.Random.Intn(len(schIndexes))
		newIndexes := slices.Delete(slices.Clone(schIndexes), i, i+1)
		j := m.Random.Intn(len(newIndexes))

		mmOne := schedule[schIndexes[i]].MaxMessages
		mmTwo := schedule[newIndexes[j]].MaxMessages
		schedule[newIndexes[j]].MaxMessages = mmOne
		schedule[schIndexes[i]].MaxMessages = mmTwo
	}

	return schedule
}

type RandomNodeMutator struct {
	Count  int
	Random *rand.Rand
	Nodes  []string
}

func NewRandomNodeMutator(count int, rand *rand.Rand, nodes []string) *RandomNodeMutator {
	return &RandomNodeMutator{Count: count, Random: rand, Nodes: nodes}
}

func (m *RandomNodeMutator) Mutate(schedule []Step) []Step {
	schIndexes := make([]int, 0)

	for i, s := range schedule {
		if s.Type == Schedule {
			schIndexes = append(schIndexes, i)
		}
	}

	for k := 0; k < m.Count; k++ {
		if len(schIndexes) < 1 {
			return schedule
		}
		n := schedule[schIndexes[m.Random.Intn(len(schIndexes))]].From
		nodeIndex := slices.Index(m.Nodes, n)
		otherNodes := slices.Delete(slices.Clone(m.Nodes), nodeIndex, nodeIndex+1)
		newNode := otherNodes[m.Random.Intn(len(otherNodes))]

		schedule[schIndexes[m.Random.Intn(len(schIndexes))]].From = newNode
		schedule[schIndexes[m.Random.Intn(len(schIndexes))]].Node = newNode
	}

	return schedule
}

type CombinedMutator struct {
	Mutators []Mutator
}

func NewCombinedMutator(swaps int, count int, rand *rand.Rand, nodes []string) *CombinedMutator {
	return &CombinedMutator{Mutators: []Mutator{NewSwapNodesMutator(swaps, rand), NewSwapMaxMessagesMutator(swaps, rand), NewRandomNodeMutator(count, rand, nodes)}}
}

func (m *CombinedMutator) Mutate(schedule []Step) []Step {
	nextSch := schedule
	for _, mutator := range m.Mutators {
		sch := mutator.Mutate(nextSch)
		nextSch = sch
	}

	return nextSch
}
