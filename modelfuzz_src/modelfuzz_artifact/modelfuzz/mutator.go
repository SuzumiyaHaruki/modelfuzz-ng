package modelfuzz

import (
	"fmt"
	"math/rand"
	"time"
)

// Mutator 是 ModelFuzz 的 trace 变异接口。
//
// 在完整 fuzzing 流程中，Fuzzer 先执行一条 choice trace，Guider 再判断这条执行是否带来
// 新的模型状态覆盖。如果有新覆盖，Fuzzer 会把本轮实际输出的 choice trace 和 event trace
// 交给 Mutator，让 Mutator 生成一条“相近但不同”的新 choice trace，并放回
// mutatedTracesQueue 等待后续 iteration 重放。
//
// Mutator 的核心职责是修改“调度选择”，而不是修改真实系统事件。event trace 是真实 Cluster
// 按某条 choice trace 执行后产生的结果；变异器即使接收 eventTrace，也通常只用它来辅助
// 决定怎么改 trace。新 trace 真正执行时，会由 RunIteration 重新生成新的 event trace。
type Mutator interface {
	// Mutate 接收一条已执行的 choice trace 和对应 event trace，尝试生成一条新的 choice trace。
	//
	// 返回值中的 bool 表示这次变异是否成功。失败通常意味着 trace 中没有足够的目标 Choice
	// 可以修改，例如没有 Node choice 或可交换的选择数量不足。Fuzzer 只会把 ok=true 的
	// 新 trace 放入 mutatedTracesQueue。
	Mutate(*List[*Choice], *List[*Event]) (*List[*Choice], bool)
}

// randomMutator 是一个“不做任何变异”的占位实现。
//
// 它适合用在只想随机执行、不想基于覆盖反馈扩展新 trace 的实验中。名字叫 randomMutator，
// 但它并不会随机生成新 trace；随机 trace 是 RunIteration 在 mimic 为 nil 时生成的。
type randomMutator struct{}

func (r *randomMutator) Mutate(_ *List[*Choice], _ *List[*Event]) (*List[*Choice], bool) {
	// 返回 ok=false，告诉 Fuzzer 当前 trace 不会产生变异后续。
	return nil, false
}

// RandomMutator 返回一个不执行变异的 Mutator。
//
// 使用它时，Fuzzer 仍然可以通过 seed 和随机路径探索系统，但 Guider 发现新状态后不会围绕
// 当前 trace 生成额外的 mutated trace。
func RandomMutator() Mutator {
	return &randomMutator{}
}

// SwapCrashNodeMutator 通过交换宕机类 Choice 的 Node 字段来生成新 trace。
//
// 设计意图是：保留宕机发生的 step 不变，只改变“哪个节点在这些 step 宕机”。这会探索类似
// 故障时机下不同节点失效对协议行为的影响。
//
// 注意：当前实现匹配的是 ch.Type == "Crash"，而 fuzzer.go 中 CanCrash 写入的类型是
// "StopNode"。因此在当前这套 Fuzzer 生成的 trace 上，这个 mutator 很可能找不到目标。
// 这里仅按现有代码加注释，不改变行为。
type SwapCrashNodeMutator struct {
	// NumSwaps 表示要随机选择多少对宕机 Choice 并交换它们的 Node。
	NumSwaps int
	// r 是该 mutator 自己的随机源，用于选择交换位置。
	r *rand.Rand
}

// 编译期检查 SwapCrashNodeMutator 是否实现了 Mutator 接口。
var _ Mutator = &SwapCrashNodeMutator{}

// NewSwapCrashNodeMutator 创建一个交换宕机节点的 mutator。
func NewSwapCrashNodeMutator(swaps int) *SwapCrashNodeMutator {
	return &SwapCrashNodeMutator{
		// 保存本次变异希望执行的交换次数。
		NumSwaps: swaps,
		// 使用独立随机源，避免和 Fuzzer 主随机源共享状态。
		r: rand.New(rand.NewSource(time.Now().UnixNano())),
	}
}

func (s *SwapCrashNodeMutator) Mutate(trace *List[*Choice], eventTrace *List[*Event]) (*List[*Choice], bool) {
	// swaps 记录要交换的下标对：key 和 value 都是 trace 中 Choice 的位置。
	swaps := make(map[int]int)

	// nodeChoices 收集所有满足条件的宕机类 Choice 在 trace 中的下标。
	nodeChoices := make([]int, 0)
	for i, ch := range trace.Iter() {
		// 当前代码只匹配 "Crash"。如果 trace 使用 "StopNode"，这里不会收集到对应 Choice。
		if ch.Type == "Crash" {
			nodeChoices = append(nodeChoices, i)
		}
	}

	// 每次 swap 需要两个目标 Choice。数量不足时无法完成预期交换，返回失败。
	if len(nodeChoices) < s.NumSwaps*2 {
		return nil, false
	}

	// 随机选择 NumSwaps 对下标。sample 返回两个不重复的候选下标值。
	for len(swaps) < s.NumSwaps {
		sp := sample(nodeChoices, 2, s.r)
		swaps[sp[0]] = sp[1]
	}

	// 在拷贝上修改，避免破坏输入 trace；输入 trace 还可能被记录或其他 mutator 使用。
	newTrace := trace.Copy()
	for i, j := range swaps {
		// 取出要交换 Node 字段的两个 Choice。
		iCh, _ := newTrace.Get(i)
		jCh, _ := newTrace.Get(j)

		// 分别复制 Choice，再交换 Node，避免直接修改原对象造成别处引用受影响。
		iChNew := iCh.Copy()
		iChNew.Node = jCh.Node
		jChNew := jCh.Copy()
		jChNew.Node = iCh.Node

		// 写回新 trace 中对应位置。
		newTrace.Set(i, iChNew)
		newTrace.Set(j, jChNew)
	}
	// 返回变异后的新 choice trace；eventTrace 不会被修改或返回。
	return newTrace, true
}

// SwapNodeMutator 通过交换 Node 类型 Choice 的位置来改变消息调度顺序。
//
// Node Choice 表示一次“从 from->to 队列最多投递多少条消息”的 schedule。交换两个 Node
// Choice 相当于交换两个 step 的网络调度动作：原来较早投递的方向可能被推迟，原来较晚
// 投递的方向可能提前。这样可以探索相同消息集合在不同调度顺序下的协议行为。
//
// 这个 mutator 交换的是整个 Choice，而不只是 From/To 字段，因此 MaxMessages 也会跟着
// 对应的 Node Choice 一起移动。
type SwapNodeMutator struct {
	// NumSwaps 是最多尝试交换多少对 Node Choice。
	NumSwaps int
	// rand 是该 mutator 的独立随机源。
	rand *rand.Rand
}

// 编译期检查 SwapNodeMutator 是否实现了 Mutator 接口。
var _ Mutator = &SwapNodeMutator{}

// NewSwapNodeMutator 创建一个交换消息调度位置的 mutator。
func NewSwapNodeMutator(swaps int) *SwapNodeMutator {
	return &SwapNodeMutator{
		// 保存期望交换次数。
		NumSwaps: swaps,
		// 独立随机源用于选择交换下标。
		rand: rand.New(rand.NewSource(time.Now().UnixNano())),
	}
}

func (s *SwapNodeMutator) Mutate(trace *List[*Choice], _ *List[*Event]) (*List[*Choice], bool) {
	// nodeChoiceIndices 收集 trace 中所有 Type=="Node" 的下标。
	nodeChoiceIndices := make([]int, 0)
	for i, choice := range trace.Iter() {
		// 只变异消息投递 schedule，不触碰 StopNode/StartNode/ClientRequest 等外部输入。
		if choice.Type == "Node" {
			nodeChoiceIndices = append(nodeChoiceIndices, i)
		}
	}
	// 没有 Node Choice 就无法改变消息调度顺序。
	numNodeChoiceIndices := len(nodeChoiceIndices)
	if numNodeChoiceIndices == 0 {
		return nil, false
	}
	// 实际交换次数不超过 Node Choice 数量，避免在短 trace 上要求过多交换。
	choices := numNodeChoiceIndices
	if s.NumSwaps < choices {
		choices = s.NumSwaps
	}
	// toSwap 用字符串 key 去重，避免重复生成完全相同的交换对。
	toSwap := make(map[string]map[int]int)
	for len(toSwap) < choices {
		// 从所有 Node Choice 下标中随机挑选两个位置。
		i := nodeChoiceIndices[s.rand.Intn(numNodeChoiceIndices)]
		j := nodeChoiceIndices[s.rand.Intn(numNodeChoiceIndices)]
		// key 表示一个有方向的交换对；i==j 时也可能被选中，这种交换不会改变 trace。
		key := fmt.Sprintf("%d_%d", i, j)
		if _, ok := toSwap[key]; !ok {
			// 内层 map 只是为了在下面统一用 for i,j 的形式处理。
			toSwap[key] = map[int]int{i: j}
		}
	}
	// 在 trace 拷贝上应用交换，保持输入 trace 不变。
	newTrace := trace.Copy()
	for _, v := range toSwap {
		for i, j := range v {
			// 取出两个 Node Choice。
			first, _ := newTrace.Get(i)
			second, _ := newTrace.Get(j)
			// 交换整个 Choice，包含 From/To/MaxMessages 等字段。
			newTrace.Set(i, second.Copy())
			newTrace.Set(j, first.Copy())
		}
	}
	// 返回新的调度轨迹，等待后续 RunIteration 作为 mimicTrace 重放。
	return newTrace, true
}

// SwapMaxMessagesMutator 只交换 Node Choice 的 MaxMessages 字段。
//
// 与 SwapNodeMutator 不同，它不会改变每个 step 选择的 from/to 方向，只改变每次 schedule
// 最多投递多少条消息。这样可以探索“相同方向调度顺序下，不同批量投递大小”对系统状态的
// 影响。例如原来某一步只投递 1 条消息，变异后可能投递更多，从而改变后续事件序列。
type SwapMaxMessagesMutator struct {
	// NumSwaps 表示要随机选择多少对 Node Choice 并交换 MaxMessages。
	NumSwaps int
	// r 是该 mutator 的独立随机源。
	r *rand.Rand
}

// 编译期检查 SwapMaxMessagesMutator 是否实现了 Mutator 接口。
var _ Mutator = &SwapMaxMessagesMutator{}

// NewSwapMaxMessagesMutator 创建一个交换投递上限的 mutator。
func NewSwapMaxMessagesMutator(swaps int) *SwapMaxMessagesMutator {
	return &SwapMaxMessagesMutator{
		// 保存期望交换次数。
		NumSwaps: swaps,
		// 独立随机源用于选择交换位置。
		r: rand.New(rand.NewSource(time.Now().UnixNano())),
	}
}

func (s *SwapMaxMessagesMutator) Mutate(trace *List[*Choice], eventTrace *List[*Event]) (*List[*Choice], bool) {
	// swaps 记录要交换 MaxMessages 的 Node Choice 下标对。
	swaps := make(map[int]int)

	// nodeChoices 收集所有 Node Choice 的下标。
	nodeChoices := make([]int, 0)
	for i, ch := range trace.Iter() {
		// 只处理消息 schedule，避免改变故障注入或客户端请求。
		if ch.Type == "Node" {
			nodeChoices = append(nodeChoices, i)
		}
	}

	// 目标 Node Choice 数量不足时，无法完成 NumSwaps 次交换。
	// 注意每次 sample 需要两个元素；如果 NumSwaps>0 且 len(nodeChoices)==1，sample 会返回
	// 单元素切片，下面访问 sp[1] 会越界。这里保留原逻辑，仅在注释中说明边界。
	if len(nodeChoices) < s.NumSwaps {
		return nil, false
	}

	// 随机选择要交换 MaxMessages 的下标对。
	for len(swaps) < s.NumSwaps {
		sp := sample(nodeChoices, 2, s.r)
		swaps[sp[0]] = sp[1]
	}

	// 在拷贝上修改，保留输入 trace 原样。
	newTrace := trace.Copy()
	for i, j := range swaps {
		// 取出两个 Node Choice。
		iCh, _ := newTrace.Get(i)
		jCh, _ := newTrace.Get(j)

		// 复制后只交换 MaxMessages，From/To/Step 等其他字段保持原位置不变。
		iChNew := iCh.Copy()
		iChNew.MaxMessages = jCh.MaxMessages
		jChNew := jCh.Copy()
		jChNew.MaxMessages = iCh.MaxMessages

		// 写回新 trace。
		newTrace.Set(i, iChNew)
		newTrace.Set(j, jChNew)
	}
	// 返回变异后的 choice trace。
	return newTrace, true
}

// combinedMutator 将多个 Mutator 串联成一个复合变异器。
//
// Fuzzer 只调用一个 Mutator 接口；如果希望一次反馈后同时改变多个维度，例如先交换调度
// 顺序再交换 MaxMessages，就可以用 CombineMutators 把多个 mutator 包起来。
type combinedMutator struct {
	// mutators 按顺序保存要应用的变异器。
	mutators []Mutator
}

// 编译期检查 combinedMutator 是否实现了 Mutator 接口。
var _ Mutator = &combinedMutator{}

func (c *combinedMutator) Mutate(trace *List[*Choice], eventTrace *List[*Event]) (*List[*Choice], bool) {
	// 从输入 trace 的拷贝开始，避免复合变异过程修改原 trace。
	curTrace := trace.Copy()
	for _, m := range c.mutators {
		// 每个 mutator 都基于上一个 mutator 的输出继续变异。
		nextTrace, ok := m.Mutate(curTrace, eventTrace)
		if !ok {
			// 任一阶段失败，则整个复合变异失败，Fuzzer 不会入队这条结果。
			return nil, false
		}
		// 成功后把当前 trace 推进到下一阶段输出。
		curTrace = nextTrace
	}
	// 所有子 mutator 都成功时，返回最终变异结果。
	return curTrace, true
}

// CombineMutators 把多个 Mutator 组合成一个按顺序执行的 Mutator。
//
// 组合后的 mutator 会依次调用传入的 mutators。只要其中任意一个返回 ok=false，整个组合
// 就返回失败；全部成功时返回最后一个 mutator 的输出 trace。
func CombineMutators(mutators ...Mutator) Mutator {
	return &combinedMutator{
		// 保留调用方传入的顺序，这个顺序会影响最终变异结果。
		mutators: mutators,
	}
}

// sample 从整数切片 l 中随机抽取至多 size 个元素。
//
// 这个函数抽取的是“不重复的下标”，因此当 l 本身没有重复值时，返回值也不会重复。它常被
// 用来随机选择 step 或 trace 下标，例如随机选择 crash step、request step 或要交换的
// Node Choice 位置。
//
// 返回顺序来自 map 遍历，因此不保证稳定顺序；调用方不应依赖 samples 内部顺序。
func sample(l []int, size int, r *rand.Rand) []int {
	// 如果请求数量大于等于输入长度，直接返回原切片。
	// 这里没有拷贝，因此调用方如果修改返回值，也会修改原切片。
	if size >= len(l) {
		return l
	}
	// indexes 记录被选中的下标，map 自然去重。
	indexes := make(map[int]bool)
	for len(indexes) < size {
		// 在 l 的下标范围内随机选择一个位置。
		i := r.Intn(len(l))
		// 重复下标会覆盖同一个 map key，不会增加结果数量。
		indexes[i] = true
	}
	// 把选中的下标对应的值复制到结果切片。
	samples := make([]int, size)
	i := 0
	for k := range indexes {
		// k 是 l 的下标，l[k] 才是返回给调用方的采样值。
		samples[i] = l[k]
		i++
	}
	return samples
}

// intRange 返回半开区间 [start, end) 内的整数切片。
//
// Fuzzer 用它为 crash step 选择恢复 step，例如 intRange(c, Steps) 表示恢复可以发生在
// 宕机同一步或之后的某一步。如果希望恢复必须晚于宕机，应改用 intRange(c+1, Steps) 并
// 处理 c+1 == Steps 的边界。
func intRange(start, end int) []int {
	// 按区间长度预分配结果。调用方应保证 end >= start。
	res := make([]int, end-start)
	for i := start; i < end; i++ {
		// 将实际数值 i 映射到从 0 开始的切片下标。
		res[i-start] = i
	}
	return res
}
