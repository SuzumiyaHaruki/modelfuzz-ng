package main

import (
	"fmt"
	"math/rand"
	"strconv"
	"time"

	pb "github.com/zeu5/raft-fuzzing/raft/raftpb"
)

// Fuzzer 负责生成/重放调度 trace，并用 guider 的反馈决定哪些 trace 值得继续变异。
//
// 一次 fuzz iteration 中有两条并行的轨迹：
//   - SchedulingChoice trace：fuzzer 的输入/调度决策，例如投递哪个方向的消息、
//     在哪个 step crash/start 哪个节点、什么时候注入客户端请求。
//   - Event trace：被测 raft 实现实际发生的模型可见事件，例如 SendMessage、
//     DeliverMessage、Timeout、BecomeLeader、ClientRequest。
//
// Guider 只看执行后产生的 trace/eventTrace，判断是否覆盖了新的模型状态或代码路径；
// Mutator 只改 SchedulingChoice trace，让下一次执行在“相似但不同”的调度下探索。
//
// 和通用 modelfuzz 不同，这里没有 Cluster 接口字段，而是直接持有 etcd-raft 的
// RaftEnvironment。也就是说，Fuzzer 和被测系统适配层耦合得更紧，少了一层接口抽象。
type Fuzzer struct {
	// messageQueues 模拟可控网络，key 是 "from_to"。
	// RaftEnvironment.Tick 产生的消息先进入这里，只有被某个 Node 类型 SchedulingChoice
	// 选中后才会交付给目标节点。这样网络延迟、乱序和丢包都可以通过 trace 表达。
	messageQueues map[string]*Queue[pb.Message]
	// nodes 包含 0..Replicas。0 不是 raft replica，而是客户端/外部环境占位。
	nodes  []uint64
	config *FuzzerConfig
	// candidateTracesQueue 同时保存待反馈检查的 seed replay 和真正的 mutation。
	candidateTracesQueue *Queue[queuedTrace]
	rand                 *rand.Rand
	// raftEnvironment 是 etcd-raft 的内存执行环境，相当于通用版的 Cluster 实现。
	raftEnvironment *RaftEnvironment

	// stats 记录错误、随机执行数、变异执行数和 checker 发现的 buggy iteration。
	stats map[string]interface{}
}

type queuedTrace struct {
	trace      *List[*SchedulingChoice]
	isMutation bool
}

// traceCtx 保存单次 iteration 的输入计划和输出记录。
// mimicTrace 是输入；trace/eventTrace 是本轮实际执行后写出的结果。
type traceCtx struct {
	trace          *List[*SchedulingChoice]
	mimicTrace     *List[*SchedulingChoice]
	eventTrace     *List[*Event]
	nodeChoices    *Queue[*SchedulingChoice]
	booleanChoices *Queue[bool]
	integerChoices *Queue[int]
	crashPoints    map[int]uint64
	startPoints    map[int]uint64
	clientRequests map[int]int
	rand           *rand.Rand

	Error  error
	fuzzer *Fuzzer
}

func (t *traceCtx) SetError(err error) {
	// RaftEnvironment 可以通过 SetError 把 panic、启动失败等异常反馈给本轮执行。
	// RunIteration 会在动作边界检查该错误并提前结束当前 iteration。
	t.Error = err
}

func (t *traceCtx) GetError() error {
	return t.Error
}

func (t *traceCtx) IsError() bool {
	return t.Error != nil
}

func (t *traceCtx) GetNextNodeChoice() (uint64, uint64, int, int) {
	// Node choice 不指定具体消息，而是指定一条方向队列和最多投递条数。
	// 具体投递哪些消息，由 Fuzzer.Schedule 从该队列头部取出。
	var fromChoice uint64
	var toChoice uint64
	var maxMessages int
	if t.nodeChoices.Size() > 0 {
		// mimic/seed 中已有计划时优先重放。
		c, _ := t.nodeChoices.Pop()
		fromChoice = c.From
		toChoice = c.To
		maxMessages = c.MaxMessages
	} else {
		// 如果计划耗尽，则临时随机补一个选择，保证 iteration 可以继续跑满 Steps。
		i := t.rand.Intn(len(t.fuzzer.nodes))
		j := t.rand.Intn(len(t.fuzzer.nodes))
		fromChoice = t.fuzzer.nodes[i]
		toChoice = t.fuzzer.nodes[j]
		maxMessages = t.rand.Intn(t.fuzzer.config.MaxMessages)
	}
	choiceIndex := t.trace.Size()
	t.trace.Append(&SchedulingChoice{
		Type:        Node,
		From:        fromChoice,
		To:          toChoice,
		MaxMessages: maxMessages,
	})

	return fromChoice, toChoice, maxMessages, choiceIndex
}

func (t *traceCtx) GetRandomBoolean() (choice bool) {
	// 如果 mimic trace 中已有随机选择，则重放；否则生成并记录一个新选择。
	// 这类 choice 用来把“调度外”的随机分支纳入可重放轨迹。
	if t.booleanChoices.Size() > 0 {
		choice, _ = t.booleanChoices.Pop()
	} else {
		choice = t.rand.Intn(2) == 0
	}
	choiceIndex := t.trace.Size()
	t.trace.Append(&SchedulingChoice{
		Type:          RandomBoolean,
		BooleanChoice: choice,
	})
	t.eventTrace.Append(&Event{
		Name: "RandomBooleanChoice",
		Params: map[string]interface{}{
			"choice": choice,
		},
		Origin: &EventOrigin{
			Step:            -1,
			Phase:           EventPhaseRandom,
			ChoiceIndex:     choiceIndex,
			DeliveryOrdinal: -1,
			DeliveryCount:   -1,
		},
	})
	return
}

func (t *traceCtx) GetRandomInteger(max int) (choice int) {
	// 设计上用于把内部随机 int 选择纳入 trace；当前 RaftRand 接入在 raft.go 中被注释掉。
	// 如果未来重新启用 RaftRand，raft 内部 randomizedElectionTimeout 的 rand.Intn
	// 就可以通过这里记录和重放。
	if t.integerChoices.Size() > 0 {
		choice, _ = t.integerChoices.Pop()
	} else {
		choice = t.rand.Intn(max)
	}
	choiceIndex := t.trace.Size()
	t.trace.Append(&SchedulingChoice{
		Type:          RandomInteger,
		IntegerChoice: choice,
	})
	t.eventTrace.Append(&Event{
		Name: "RandomIntegerChoice",
		Params: map[string]interface{}{
			"choice": choice,
		},
		Origin: &EventOrigin{
			Step:            -1,
			Phase:           EventPhaseRandom,
			ChoiceIndex:     choiceIndex,
			DeliveryOrdinal: -1,
			DeliveryCount:   -1,
		},
	})
	return
}

func (t *traceCtx) CanCrash(step int) (uint64, int, bool) {
	// crashPoints 是“计划表”，key 是 step。这里只记录调度 choice；只有 RunIteration
	// 确认节点当前存活并实际调用 Stop 时，才记录模型可见的 Remove event。
	node, ok := t.crashPoints[step]
	if ok {
		choiceIndex := t.trace.Size()
		t.trace.Append(&SchedulingChoice{
			Type: StopNode,
			Node: node,
			Step: step,
		})
		return node, choiceIndex, true
	}
	return node, -1, false
}

func (t *traceCtx) CanStart(step int) (uint64, int, bool) {
	// startPoints 与 crashPoints 配对使用。这里只记录调度 choice；只有 RunIteration
	// 确认节点确实处于 crashed 集合并调用 Start 时，才记录 Add event。
	node, ok := t.startPoints[step]
	if ok {
		choiceIndex := t.trace.Size()
		t.trace.Append(&SchedulingChoice{
			Type: StartNode,
			Node: node,
			Step: step,
		})
		return node, choiceIndex, true
	}
	return node, -1, false
}

func (t *traceCtx) IsClientRequest(step int) (int, int, bool) {
	// 客户端请求也是 trace 的一部分，否则同一调度重放时输入请求数量和位置会不稳定。
	req, ok := t.clientRequests[step]
	if ok {
		choiceIndex := t.trace.Size()
		t.trace.Append(&SchedulingChoice{
			Type:    ClientRequest,
			Request: req,
			Step:    step,
		})
		return req, choiceIndex, true
	}
	return req, -1, false
}

type FuzzerConfig struct {
	// Iterations 是总执行轮数；Steps 是每轮 trace 的长度。
	Iterations int
	Steps      int
	// Checker 是额外 bug oracle，不影响 guider 的覆盖率判断。
	Checker Checker
	Mutator Mutator
	Guider  Guider
	// Strategy 是早期调度策略抽象，当前主路径基本由 trace/mutator 决定。
	Strategy              Strategy
	RaftEnvironmentConfig RaftEnvironmentConfig
	// MutPerTrace 控制发现新覆盖后围绕该 trace 扩展多少条变异 trace。
	MutPerTrace        int
	SeedPopulationSize int
	// NumberRequests 控制随机种子 trace 中安排多少个 ClientRequest step。
	// 变异/重放 trace 时，请求数量由 trace 本身决定。
	NumberRequests int
	// CrashQuota 控制随机种子 trace 中安排多少个 StopNode，并为每个 StopNode 随机安排一个 StartNode。
	CrashQuota int
	// MaxMessages 是单个 Node choice 从某方向队列最多投递的消息数。
	MaxMessages int
	// ReseedFrequency 控制多久重新生成一批随机种子，避免陷入局部搜索。
	ReseedFrequency int
	// RandomSeed 非零时固定 Fuzzer 和所有 RandomizedMutator 的共享随机源；
	// 0 保留原有的基于当前时间播种行为。
	RandomSeed int64
}

func NewFuzzer(config *FuzzerConfig) *Fuzzer {
	seed := config.RandomSeed
	if seed == 0 {
		seed = time.Now().UnixNano()
	}
	environmentConfig := config.RaftEnvironmentConfig
	environmentConfig.RandomSeed = seed
	f := &Fuzzer{
		config:               config,
		nodes:                make([]uint64, 0),
		messageQueues:        make(map[string]*Queue[pb.Message]),
		candidateTracesQueue: NewQueue[queuedTrace](),
		rand:                 rand.New(rand.NewSource(seed)),
		raftEnvironment:      NewRaftEnvironment(environmentConfig),
		stats:                make(map[string]interface{}),
	}
	for i := 0; i <= f.config.RaftEnvironmentConfig.Replicas; i++ {
		// 节点 0 用作客户端/外部环境占位，真实 raft replica 是 1..Replicas。
		f.nodes = append(f.nodes, uint64(i))
		for j := 0; j <= f.config.RaftEnvironmentConfig.Replicas; j++ {
			key := fmt.Sprintf("%d_%d", i, j)
			f.messageQueues[key] = NewQueue[pb.Message]()
		}
	}
	f.stats["total_executions"] = 0
	f.stats["feedback_executions"] = 0
	f.stats["seed_generation_executions"] = 0
	f.stats["seed_replay_executions"] = 0
	f.stats["mutation_executions"] = 0
	f.stats["random_executions"] = 0
	f.stats["execution_errors"] = make(map[string]bool, 0)
	f.stats["error_executions"] = make(map[string][]string)
	f.stats["buggy_executions"] = make(map[string]bool, 0)
	f.stats["random_seed"] = seed
	if randomized, ok := config.Mutator.(RandomizedMutator); ok {
		randomized.SetRandom(f.rand)
	}
	return f
}

func (f *Fuzzer) incrementStat(name string) {
	f.stats[name] = f.stats[name].(int) + 1
}

func (f *Fuzzer) Schedule(from uint64, to uint64, maxMessages int) []pb.Message {
	// 从指定方向的网络队列中取出最多 maxMessages 条消息，是否投递由当前 SchedulingChoice 决定。
	key := fmt.Sprintf("%d_%d", from, to)
	queue, ok := f.messageQueues[key]
	if !ok || queue.Size() == 0 {
		return []pb.Message{}
	}
	messages := make([]pb.Message, 0)
	for i := 0; i < maxMessages; i++ {
		message, ok := queue.Pop()
		if !ok {
			break
		}
		messages = append(messages, message)
	}
	return messages
}

func recordReceive(message pb.Message, eventTrace *List[*Event], origin *EventOrigin) {
	// DeliverMessage 是模型看到的“消息被交付给目标节点”事件。
	eventTrace.Append(&Event{
		Name:   "DeliverMessage",
		Node:   message.To,
		Origin: origin.Copy(),
		Params: map[string]interface{}{
			"type":     message.Type.String(),
			"term":     message.Term,
			"from":     message.From,
			"to":       message.To,
			"log_term": message.LogTerm,
			"entries":  message.Entries,
			"index":    message.Index,
			"commit":   message.Commit,
			"vote":     message.Vote,
			"reject":   message.Reject,
		},
	})
}

func recordSend(message pb.Message, eventTrace *List[*Event], origin *EventOrigin) {
	// SendMessage 是模型看到的“节点发出了网络消息”事件。
	eventTrace.Append(&Event{
		Name:   "SendMessage",
		Node:   message.From,
		Origin: origin.Copy(),
		Params: map[string]interface{}{
			"type":     message.Type.String(),
			"term":     message.Term,
			"from":     message.From,
			"to":       message.To,
			"log_term": message.LogTerm,
			"entries":  message.Entries,
			"index":    message.Index,
			"commit":   message.Commit,
			"vote":     message.Vote,
			"reject":   message.Reject,
		},
	})
}

func (f *Fuzzer) seed() {
	// reseed 时先随机跑一批 trace，再把它们作为后续变异的种子。
	// 注意 seed 本身也会真实执行 raft，因此可能产生 eventTrace 和副作用；随后会在下一轮前 reset。
	f.candidateTracesQueue.Reset()
	for i := 0; i < f.config.SeedPopulationSize; i++ {
		trace, _ := f.RunIteration(fmt.Sprintf("pop_%d", i), nil)
		f.incrementStat("seed_generation_executions")
		f.incrementStat("total_executions")
		f.candidateTracesQueue.Push(queuedTrace{
			trace: copyTrace(trace, defaultCopyFilter()),
		})
	}
}

func (f *Fuzzer) Run() []CoverageStats {
	// Run 是外层反馈闭环：
	// 取 trace -> 执行 -> guider 评分 -> 对有新覆盖的 trace 做变异 -> 入队等待后续执行。
	coverages := make([]CoverageStats, 0)
	for i := 0; i < f.config.Iterations; i++ {
		if i%f.config.ReseedFrequency == 0 {
			f.seed()
		}
		fmt.Printf("\rRunning iteration: %d/%d", i+1, f.config.Iterations)
		var mimic *List[*SchedulingChoice] = nil
		if f.candidateTracesQueue.Size() > 0 {
			candidate, _ := f.candidateTracesQueue.Pop()
			mimic = candidate.trace
			if candidate.isMutation {
				f.incrementStat("mutation_executions")
			} else {
				f.incrementStat("seed_replay_executions")
			}
		} else {
			f.incrementStat("random_executions")
		}
		trace, eventTrace := f.RunIteration(fmt.Sprintf("fuzz_%d", i), mimic)
		f.incrementStat("feedback_executions")
		f.incrementStat("total_executions")
		if numNewStates, _ := f.config.Guider.Check(trace, eventTrace); numNewStates > 0 {
			if guided, ok := f.config.Mutator.(GuidanceAwareMutator); ok {
				guided.SetGuidance(f.config.Guider.LastGuidance())
			}
			// 新覆盖越多，围绕这条 trace 生成的变异越多。
			numMutations := numNewStates * f.config.MutPerTrace
			for j := 0; j < numMutations; j++ {
				new, ok := f.config.Mutator.Mutate(trace, eventTrace)
				if ok {
					f.candidateTracesQueue.Push(queuedTrace{
						trace:      copyTrace(new, defaultCopyFilter()),
						isMutation: true,
					})
				}
			}
		}
		coverages = append(coverages, f.config.Guider.Coverage())
	}
	if provider, ok := f.config.Mutator.(MutationSelectionStatsProvider); ok {
		local, global := provider.MutationSelectionStats()
		f.stats["local_mutation_attempts"] = local
		f.stats["global_mutation_attempts"] = global
	}
	if provider, ok := f.config.Mutator.(PrefixMutationStatsProvider); ok {
		stats := provider.PrefixMutationStats()
		f.stats["prefix_mutation_attempts"] = stats.Attempts
		f.stats["prefix_guided_attempts"] = stats.Guided
		f.stats["prefix_global_fallback_attempts"] = stats.GlobalFallback
		f.stats["prefix_generated_attempts"] = stats.Generated
		f.stats["prefix_rejected_attempts"] = stats.Rejected
	}
	return coverages
}

func (f *Fuzzer) RunIteration(iteration string, mimic *List[*SchedulingChoice]) (*List[*SchedulingChoice], *List[*Event]) {
	// 初始化本轮 iteration 的执行上下文。
	tCtx := &traceCtx{
		trace:          NewList[*SchedulingChoice](),
		eventTrace:     NewList[*Event](),
		nodeChoices:    NewQueue[*SchedulingChoice](),
		booleanChoices: NewQueue[bool](),
		integerChoices: NewQueue[int](),
		crashPoints:    make(map[int]uint64),
		startPoints:    make(map[int]uint64),
		clientRequests: make(map[int]int),
		rand:           f.rand,
		fuzzer:         f,
	}
	if mimic != nil {
		// 重放/变异路径：把已有 trace 拆成各类计划队列，之后按 step 消费。
		// mimic trace 可能来自随机 seed，也可能来自 mutator；执行时仍会重新生成实际 trace。
		tCtx.mimicTrace = mimic
		for i := 0; i < mimic.Size(); i++ {
			ch, _ := mimic.Get(i)
			switch ch.Type {
			case Node:
				tCtx.nodeChoices.Push(ch.Copy())
			case RandomBoolean:
				tCtx.booleanChoices.Push(ch.BooleanChoice)
			case RandomInteger:
				tCtx.integerChoices.Push(ch.IntegerChoice)
			case StartNode:
				tCtx.startPoints[ch.Step] = ch.Node
			case StopNode:
				tCtx.crashPoints[ch.Step] = ch.Node
			case ClientRequest:
				tCtx.clientRequests[ch.Step] = ch.Request
			}
		}
	} else {
		// 随机种子路径：随机生成消息调度、节点故障/恢复和客户端请求注入点。
		// 这些随机选择会被写进输出 trace，因此后续可以被重放和变异。
		for i := 0; i < f.config.Steps; i++ {
			var fromIdx int = 0
			for fromIdx == 0 {
				fromIdx = f.rand.Intn(len(f.nodes))
			}
			var toIdx int = 0
			for toIdx == 0 {
				toIdx = f.rand.Intn(len(f.nodes))
			}
			tCtx.nodeChoices.Push(&SchedulingChoice{
				Type:        Node,
				From:        f.nodes[fromIdx],
				To:          f.nodes[toIdx],
				MaxMessages: f.rand.Intn(f.config.MaxMessages),
			})
		}
		choices := make([]int, f.config.Steps)
		for i := 0; i < f.config.Steps; i++ {
			choices[i] = i
		}
		for _, c := range sample(choices, f.config.CrashQuota, f.rand) {
			// 每个 crash 会随机选择一个后续 step 恢复同一节点。
			var idx int = 0
			for idx == 0 {
				idx = f.rand.Intn(len(f.nodes))
			}
			tCtx.crashPoints[c] = uint64(idx)
			s := sample(intRange(c, f.config.Steps), 1, f.rand)[0]
			tCtx.startPoints[s] = uint64(idx)
		}
		i := 1
		for _, req := range sample(choices, f.config.NumberRequests, f.rand) {
			// request id 从 1 开始递增；request=0 在 raft.go 中用于 leader no-op/模型对齐事件。
			tCtx.clientRequests[req] = i
			i++
		}
	}

	// 每轮开始前清空模拟网络，并重置内存中的 raft 集群。
	for _, q := range f.messageQueues {
		q.Reset()
	}
	f.raftEnvironment.Reset(&FuzzContext{traceCtx: tCtx})

	crashed := make(map[uint64]bool)
	fCtx := &FuzzContext{traceCtx: tCtx}
EpisodeLoop:
	for j := 0; j < f.config.Steps; j++ {
		// 一个 step 的顺序：故障/恢复 -> 投递消息 -> 客户端请求 -> Tick 收集新消息。
		// 这个固定顺序本身也是测试语义的一部分：例如本 step crash 后，投递到该节点的消息会被跳过。
		if toCrash, choiceIndex, ok := tCtx.CanCrash(j); ok {
			origin := &EventOrigin{
				Step:            j,
				Phase:           EventPhaseCrash,
				ChoiceIndex:     choiceIndex,
				DeliveryOrdinal: -1,
				DeliveryCount:   -1,
			}
			if _, alreadyCrashed := crashed[toCrash]; !alreadyCrashed {
				fCtx.SetOrigin(origin)
				tCtx.eventTrace.Append(&Event{
					Name:   "Remove",
					Node:   toCrash,
					Params: map[string]interface{}{"i": int(toCrash)},
					Origin: origin.Copy(),
				})
				f.raftEnvironment.Stop(fCtx, toCrash)
				if tCtx.IsError() {
					break EpisodeLoop
				}
				crashed[toCrash] = true
			}
		}
		if toStart, choiceIndex, ok := tCtx.CanStart(j); ok {
			_, isCrashed := crashed[toStart]
			if isCrashed {
				origin := &EventOrigin{
					Step:            j,
					Phase:           EventPhaseRestart,
					ChoiceIndex:     choiceIndex,
					DeliveryOrdinal: -1,
					DeliveryCount:   -1,
				}
				fCtx.SetOrigin(origin)
				tCtx.eventTrace.Append(&Event{
					Name:   "Add",
					Node:   toStart,
					Params: map[string]interface{}{"i": int(toStart)},
					Origin: origin.Copy(),
				})
				f.raftEnvironment.Start(fCtx, toStart)
				if tCtx.IsError() {
					break EpisodeLoop
				}
				delete(crashed, toStart)
			}
		}
		from, to, maxMessages, choiceIndex := tCtx.GetNextNodeChoice()
		if _, ok := crashed[to]; !ok {
			// 只跳过目标已宕机的投递；源节点是否已宕机不在这里检查，
			// 因为消息可能是该源节点宕机前已经进入网络队列的旧消息。
			messages := f.Schedule(from, to, maxMessages)
			for ordinal, m := range messages {
				origin := &EventOrigin{
					Step:            j,
					Phase:           EventPhaseDeliver,
					ChoiceIndex:     choiceIndex,
					DeliveryOrdinal: ordinal,
					DeliveryCount:   len(messages),
				}
				fCtx.SetOrigin(origin)
				recordReceive(m, tCtx.eventTrace, origin)
				f.raftEnvironment.Step(fCtx, m)
				if tCtx.IsError() {
					break EpisodeLoop
				}
			}
		}

		if reqNum, requestChoiceIndex, ok := tCtx.IsClientRequest(j); ok {
			// 客户端请求被包装成 MsgProp；RaftEnvironment.Step 会把它交给当前 leader。
			req := pb.Message{
				Type: pb.MsgProp,
				From: uint64(0),
				Entries: []pb.Entry{
					{Data: []byte(strconv.Itoa(reqNum))},
				},
			}
			fCtx.SetOrigin(&EventOrigin{
				Step:            j,
				Phase:           EventPhaseClientRequest,
				ChoiceIndex:     requestChoiceIndex,
				DeliveryOrdinal: -1,
				DeliveryCount:   -1,
			})
			f.raftEnvironment.Step(fCtx, req)
			if tCtx.IsError() {
				break EpisodeLoop
			}
		}

		tickOrigin := &EventOrigin{
			Step:            j,
			Phase:           EventPhaseTick,
			ChoiceIndex:     -1,
			DeliveryOrdinal: -1,
			DeliveryCount:   -1,
		}
		fCtx.SetOrigin(tickOrigin)
		for _, n := range f.raftEnvironment.Tick(fCtx) {
			// Tick 产生的新消息不会立刻投递，而是进入 from/to 队列等待未来调度。
			recordSend(n, tCtx.eventTrace, tickOrigin)
			key := fmt.Sprintf("%d_%d", n.From, n.To)
			f.messageQueues[key].Push(n)
		}
	}
	if tCtx.IsError() {
		// 环境错误按错误字符串聚合，便于后续定位哪些 trace 触发了同类问题。
		errS := tCtx.GetError().Error()
		f.stats["execution_errors"].(map[string]bool)[errS] = true
		if _, ok := f.stats["error_executions"].(map[string][]string)[errS]; !ok {
			f.stats["error_executions"].(map[string][]string)[errS] = make([]string, 0)
		}
		f.stats["error_executions"].(map[string][]string)[errS] = append(f.stats["error_executions"].(map[string][]string)[errS], iteration)
	}

	if f.config.Checker != nil && !f.config.Checker(f.raftEnvironment) {
		// Checker 失败说明发现潜在 bug，但本轮仍然返回 trace/eventTrace 供分析。
		buggyExecutions := f.stats["buggy_executions"].(map[string]bool)
		buggyExecutions[iteration] = true
		f.stats["buggy_executions"] = buggyExecutions
	}

	return tCtx.trace, tCtx.eventTrace
}

type Mutator interface {
	// Mutate 基于上一轮有效 trace 生成新的调度 trace；返回 false 表示本 mutator 无法应用。
	Mutate(*List[*SchedulingChoice], *List[*Event]) (*List[*SchedulingChoice], bool)
}

type GuidanceAwareMutator interface {
	SetGuidance(Guidance)
}

type MutationSelectionStatsProvider interface {
	MutationSelectionStats() (local, global int)
}

type PrefixMutationStats struct {
	Attempts       int
	Guided         int
	GlobalFallback int
	Generated      int
	Rejected       int
}

type PrefixMutationStatsProvider interface {
	PrefixMutationStats() PrefixMutationStats
}

// RandomizedMutator 允许 Fuzzer 把自己的确定性随机源注入 Mutator。
// 组合 Mutator 应把同一个源继续传给所有子 Mutator，使一个 seed 能控制完整变异过程。
type RandomizedMutator interface {
	SetRandom(*rand.Rand)
}

// FuzzContext 是 RaftEnvironment 向 fuzzer 反向记录事件/随机选择的窄接口。
type FuzzContext struct {
	traceCtx *traceCtx
	origin   *EventOrigin
}

func (f *FuzzContext) AddEvent(e *Event) {
	if e.Origin == nil {
		e.Origin = f.origin.Copy()
	}
	f.traceCtx.eventTrace.Append(e)
}

func (f *FuzzContext) SetOrigin(origin *EventOrigin) {
	f.origin = origin.Copy()
}

func (f *FuzzContext) RandomBooleanChoice() bool {
	return f.traceCtx.GetRandomBoolean()
}

func (f *FuzzContext) RandomIntegerChoice(max int) int {
	return f.traceCtx.GetRandomInteger(max)
}
