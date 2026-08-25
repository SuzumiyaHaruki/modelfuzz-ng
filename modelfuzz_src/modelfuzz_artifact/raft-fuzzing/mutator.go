package main

import (
	"fmt"
	"math/rand"
	"time"
)

// EmptyMutator 用作 random baseline：发现新覆盖后也不生成变异 trace。
// 因此使用它时，mutatedTracesQueue 很快会耗尽，搜索主要依赖 Fuzzer 随机生成的新 trace。
type EmptyMutator struct {
}

var _ Mutator = &EmptyMutator{}

func (e *EmptyMutator) Mutate(schedulerTrace *List[*SchedulingChoice], trace *List[*Event]) (*List[*SchedulingChoice], bool) {
	return nil, false
}

type ChoiceMutator struct {
	NumFlips int
	rand     *rand.Rand
}

func NewChoiceMutator(flips int) *ChoiceMutator {
	return &ChoiceMutator{
		NumFlips: flips,
		rand:     rand.New(rand.NewSource(time.Now().UnixNano())),
	}
}

var _ Mutator = &ChoiceMutator{}

func (c *ChoiceMutator) Mutate(trace *List[*SchedulingChoice], _ *List[*Event]) (*List[*SchedulingChoice], bool) {
	// 翻转 RandomBoolean 选择；当前 etcd-raft 版里这类选择主要是预留。
	// 如果未来把实现内部的布尔随机分支接入 FuzzContext，它就可以探索相同调度下的另一侧分支。
	booleanChoiceIndices := make([]int, 0)
	for i, choice := range trace.Iter() {
		if choice.Type == RandomBoolean {
			booleanChoiceIndices = append(booleanChoiceIndices, i)
		}
	}
	toFlip := make(map[int]bool)
	numIndices := len(booleanChoiceIndices)
	if numIndices == 0 {
		return nil, false
	}
	for len(toFlip) < c.NumFlips {
		next := booleanChoiceIndices[c.rand.Intn(numIndices)]
		if _, ok := toFlip[next]; !ok {
			toFlip[next] = true
		}
	}

	newTrace := NewList[*SchedulingChoice]()
	for i, choice := range trace.Iter() {
		if _, ok := toFlip[i]; ok {
			newTrace.Append(&SchedulingChoice{
				Type:          choice.Type,
				BooleanChoice: !choice.BooleanChoice,
			})
		} else {
			newTrace.Append(choice.Copy())
		}
	}

	return newTrace, true
}

type SkipNodeMutator struct {
	NumSkips int
	rand     *rand.Rand
}

var _ Mutator = &SkipNodeMutator{}

func NewSkipNodeMutator(skips int) *SkipNodeMutator {
	return &SkipNodeMutator{
		NumSkips: skips,
		rand:     rand.New(rand.NewSource(time.Now().UnixNano())),
	}
}

func (d *SkipNodeMutator) Mutate(trace *List[*SchedulingChoice], _ *List[*Event]) (*List[*SchedulingChoice], bool) {
	// 删除若干 Node 调度点，相当于让对应网络投递机会消失。
	// 注意它不是显式丢弃某条具体消息，而是删除一次“从某方向队列取消息”的机会。
	nodeChoiceIndices := make([]int, 0)
	for i, choice := range trace.Iter() {
		if choice.Type == Node {
			nodeChoiceIndices = append(nodeChoiceIndices, i)
		}
	}
	numNodeChoiceIndices := len(nodeChoiceIndices)
	if numNodeChoiceIndices == 0 {
		return nil, false
	}
	toSkip := make(map[int]bool)
	for len(toSkip) < d.NumSkips {
		next := nodeChoiceIndices[d.rand.Intn(numNodeChoiceIndices)]
		if _, ok := toSkip[next]; !ok {
			toSkip[next] = true
		}
	}
	newTrace := NewList[*SchedulingChoice]()
	for i, choice := range trace.Iter() {
		if _, ok := toSkip[i]; !ok {
			newTrace.Append(choice.Copy())
		}
	}
	return newTrace, true
}

type SwapNodeMutator struct {
	NumSwaps int
	rand     *rand.Rand
}

var _ Mutator = &SwapNodeMutator{}

func NewSwapNodeMutator(swaps int) *SwapNodeMutator {
	return &SwapNodeMutator{
		NumSwaps: swaps,
		rand:     rand.New(rand.NewSource(time.Now().UnixNano())),
	}
}

func (s *SwapNodeMutator) Mutate(trace *List[*SchedulingChoice], _ *List[*Event]) (*List[*SchedulingChoice], bool) {
	// 交换若干 Node 调度点，改变消息投递方向/时机。
	// compare 命令中的 combinedMutator 会使用它，这是当前主要变异之一。
	nodeChoiceIndices := make([]int, 0)
	for i, choice := range trace.Iter() {
		if choice.Type == Node {
			nodeChoiceIndices = append(nodeChoiceIndices, i)
		}
	}
	numNodeChoiceIndices := len(nodeChoiceIndices)
	if numNodeChoiceIndices == 0 {
		return nil, false
	}
	choices := numNodeChoiceIndices
	if s.NumSwaps < choices {
		choices = s.NumSwaps
	}
	toSwap := make(map[string]map[int]int)
	for len(toSwap) < choices {
		i := nodeChoiceIndices[s.rand.Intn(numNodeChoiceIndices)]
		j := nodeChoiceIndices[s.rand.Intn(numNodeChoiceIndices)]
		key := fmt.Sprintf("%d_%d", i, j)
		if _, ok := toSwap[key]; !ok {
			toSwap[key] = map[int]int{i: j}
		}
	}
	newTrace := copyTrace(trace, defaultCopyFilter())
	for _, v := range toSwap {
		for i, j := range v {
			first, _ := newTrace.Get(i)
			second, _ := newTrace.Get(j)
			newTrace.Set(i, second.Copy())
			newTrace.Set(j, first.Copy())
		}
	}
	return newTrace, true
}

type SwapIntegerChoiceMutator struct {
	NumSwaps int
	rand     *rand.Rand
}

func NewSwapIntegerChoiceMutator(numswaps int) *SwapIntegerChoiceMutator {
	return &SwapIntegerChoiceMutator{
		NumSwaps: numswaps,
		rand:     rand.New(rand.NewSource(time.Now().UnixNano())),
	}
}

func (s *SwapIntegerChoiceMutator) Mutate(trace *List[*SchedulingChoice], _ *List[*Event]) (*List[*SchedulingChoice], bool) {
	// 交换 RandomInteger 选择；设计上用于内部随机性可重放时的变异。
	// 当前 RaftRand 未启用时，trace 中通常没有 RandomInteger，因此该 mutator 多半返回 false。
	integerChoiceIndices := make([]int, 0)
	for i, choice := range trace.Iter() {
		if choice.Type == RandomInteger {
			integerChoiceIndices = append(integerChoiceIndices, i)
		}
	}
	numIntegerChoiceIndices := len(integerChoiceIndices)
	if numIntegerChoiceIndices == 0 {
		return nil, false
	}
	toSwap := make(map[int]map[int]bool)
	for len(toSwap) < s.NumSwaps {
		i := integerChoiceIndices[s.rand.Intn(numIntegerChoiceIndices)]
		j := integerChoiceIndices[s.rand.Intn(numIntegerChoiceIndices)]
		if _, ok := toSwap[i]; !ok {
			toSwap[i] = map[int]bool{j: true}
		}
	}
	newTrace := copyTrace(trace, defaultCopyFilter())
	for i, v := range toSwap {
		for j := range v {
			first, _ := newTrace.Get(i)
			second, _ := newTrace.Get(j)
			newTrace.Set(i, second)
			newTrace.Set(j, first)
		}
	}
	return newTrace, true
}

type ScaleDownIntChoiceMutator struct {
	NumPoints int
	rand      *rand.Rand
}

var _ Mutator = &ScaleDownIntChoiceMutator{}

func NewScaleDownIntChoiceMutator(numPoints int) *ScaleDownIntChoiceMutator {
	return &ScaleDownIntChoiceMutator{
		NumPoints: numPoints,
		rand:      rand.New(rand.NewSource(time.Now().UnixNano())),
	}
}

func (s *ScaleDownIntChoiceMutator) Mutate(trace *List[*SchedulingChoice], _ *List[*Event]) (*List[*SchedulingChoice], bool) {
	// 缩小部分 RandomInteger 值，常用于探索更短超时/更小随机分支。
	// 对 raft 来说，如果接入选举超时随机数，这会倾向于让节点更早超时。
	integerChoiceIndices := make([]int, 0)
	for i, choice := range trace.Iter() {
		if choice.Type == RandomInteger {
			integerChoiceIndices = append(integerChoiceIndices, i)
		}
	}
	numIntegerChoiceIndices := len(integerChoiceIndices)
	if numIntegerChoiceIndices == 0 {
		return nil, false
	}
	toScaleDown := make(map[int]bool)
	for len(toScaleDown) < s.NumPoints {
		next := s.rand.Intn(numIntegerChoiceIndices)
		toScaleDown[next] = true
	}
	newTrace := copyTrace(trace, defaultCopyFilter())
	for i := range toScaleDown {
		index := integerChoiceIndices[i]
		curChoice, ok := newTrace.Get(index)
		if !ok {
			continue
		}
		if curChoice.IntegerChoice > 0 {
			newChoice := &SchedulingChoice{
				Type:          RandomInteger,
				IntegerChoice: s.rand.Intn(curChoice.IntegerChoice),
			}
			newTrace.Set(index, newChoice)
		}
	}

	return newTrace, true
}

type ScaleUpIntChoiceMutator struct {
	NumPoints int
	Max       int
	rand      *rand.Rand
}

var _ Mutator = &ScaleUpIntChoiceMutator{}

func NewScaleUpIntChoiceMutator(numPoints, max int) *ScaleUpIntChoiceMutator {
	return &ScaleUpIntChoiceMutator{
		NumPoints: numPoints,
		Max:       max,
		rand:      rand.New(rand.NewSource(time.Now().UnixNano())),
	}
}

func (s *ScaleUpIntChoiceMutator) Mutate(trace *List[*SchedulingChoice], _ *List[*Event]) (*List[*SchedulingChoice], bool) {
	// 放大部分 RandomInteger 值，但不超过 Max。
	// 对 raft 选举超时来说，这会倾向于推迟某些节点超时。
	integerChoiceIndices := make([]int, 0)
	for i, choice := range trace.Iter() {
		if choice.Type == RandomInteger {
			integerChoiceIndices = append(integerChoiceIndices, i)
		}
	}
	numIntegerChoiceIndices := len(integerChoiceIndices)
	if numIntegerChoiceIndices == 0 {
		return nil, false
	}
	toScaleUp := make(map[int]bool)
	choices := numIntegerChoiceIndices
	if s.NumPoints < numIntegerChoiceIndices {
		choices = s.NumPoints
	}
	for len(toScaleUp) < choices {
		next := s.rand.Intn(numIntegerChoiceIndices)
		toScaleUp[next] = true
	}
	newTrace := copyTrace(trace, defaultCopyFilter())
	for i := range toScaleUp {
		index := integerChoiceIndices[i]
		curChoice, ok := newTrace.Get(index)
		if !ok {
			continue
		}
		newChoice := &SchedulingChoice{
			Type:          RandomInteger,
			IntegerChoice: min(s.Max, curChoice.IntegerChoice*2),
		}
		newTrace.Set(index, newChoice)
	}

	return newTrace, true
}

func copyTrace(t *List[*SchedulingChoice], filter func(*SchedulingChoice) bool) *List[*SchedulingChoice] {
	// trace 是可变异对象，mutator 一律复制后修改，避免污染原始有效 trace。
	newL := NewList[*SchedulingChoice]()
	for _, e := range t.Iter() {
		if filter(e) {
			newL.Append(e.Copy())
		}
	}
	return newL
}

func defaultCopyFilter() func(*SchedulingChoice) bool {
	return func(sc *SchedulingChoice) bool {
		return true
	}
}

type combinedMutator struct {
	mutators []Mutator
}

func (c *combinedMutator) Mutate(trace *List[*SchedulingChoice], eventTrace *List[*Event]) (*List[*SchedulingChoice], bool) {
	// 串联多个 mutator，任意一步失败则本次组合变异失败。
	curTrace := copyTrace(trace, defaultCopyFilter())
	for _, m := range c.mutators {
		nextTrace, ok := m.Mutate(curTrace, eventTrace)
		if !ok {
			return nil, false
		}
		curTrace = nextTrace
	}
	return curTrace, true
}

func CombineMutators(mutators ...Mutator) Mutator {
	// compare 中常用的组合是：交换 crash 节点 + 交换网络调度点 + 交换 MaxMessages。
	return &combinedMutator{
		mutators: mutators,
	}
}

type SwapCrashNodeMutator struct {
	NumSwaps int
	r        *rand.Rand
}

var _ Mutator = &SwapCrashNodeMutator{}

func NewSwapCrashNodeMutator(swaps int) *SwapCrashNodeMutator {
	return &SwapCrashNodeMutator{
		NumSwaps: swaps,
		r:        rand.New(rand.NewSource(time.Now().UnixNano())),
	}
}

func (s *SwapCrashNodeMutator) Mutate(trace *List[*SchedulingChoice], eventTrace *List[*Event]) (*List[*SchedulingChoice], bool) {
	// 交换 StopNode 选择中的节点编号，保留 crash 发生的 step。
	// 这样可以保留“故障发生的时间结构”，只改变哪个 replica 承受故障。
	swaps := make(map[int]int)

	nodeChoices := make([]int, 0)
	for i, ch := range trace.Iter() {
		if ch.Type == StopNode {
			nodeChoices = append(nodeChoices, i)
		}
	}

	if len(nodeChoices) < s.NumSwaps*2 {
		return nil, false
	}

	for len(swaps) < s.NumSwaps {
		sp := sample(nodeChoices, 2, s.r)
		swaps[sp[0]] = sp[1]
	}

	newTrace := copyTrace(trace, defaultCopyFilter())
	for i, j := range swaps {
		iCh, _ := newTrace.Get(i)
		jCh, _ := newTrace.Get(j)

		iChNew := iCh.Copy()
		iChNew.Node = jCh.Node
		jChNew := jCh.Copy()
		jChNew.Node = iCh.Node

		newTrace.Set(i, iChNew)
		newTrace.Set(j, jChNew)
	}
	return newTrace, true
}

type SwapMaxMessagesMutator struct {
	NumSwaps int
	r        *rand.Rand
}

var _ Mutator = &SwapMaxMessagesMutator{}

func NewSwapMaxMessagesMutator(swaps int) *SwapMaxMessagesMutator {
	return &SwapMaxMessagesMutator{
		NumSwaps: swaps,
		r:        rand.New(rand.NewSource(time.Now().UnixNano())),
	}
}

func (s *SwapMaxMessagesMutator) Mutate(trace *List[*SchedulingChoice], eventTrace *List[*Event]) (*List[*SchedulingChoice], bool) {
	// 交换 Node 调度点的 MaxMessages，改变一次投递的消息批量大小。
	// 这会影响网络拥塞程度：同样的 from/to 方向，批量越大越容易让 raft 快速追上。
	swaps := make(map[int]int)

	nodeChoices := make([]int, 0)
	for i, ch := range trace.Iter() {
		if ch.Type == Node {
			nodeChoices = append(nodeChoices, i)
		}
	}

	if len(nodeChoices) < s.NumSwaps {
		return nil, false
	}

	for len(swaps) < s.NumSwaps {
		sp := sample(nodeChoices, 2, s.r)
		swaps[sp[0]] = sp[1]
	}

	newTrace := copyTrace(trace, defaultCopyFilter())
	for i, j := range swaps {
		iCh, _ := newTrace.Get(i)
		jCh, _ := newTrace.Get(j)

		iChNew := iCh.Copy()
		iChNew.MaxMessages = jCh.MaxMessages
		jChNew := jCh.Copy()
		jChNew.MaxMessages = iCh.MaxMessages

		newTrace.Set(i, iChNew)
		newTrace.Set(j, jChNew)
	}
	return newTrace, true
}
