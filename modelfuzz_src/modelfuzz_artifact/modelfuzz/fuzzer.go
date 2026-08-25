package modelfuzz

import (
	"fmt"
	"math/rand"
	"strconv"
	"time"
)

// Fuzzer 是 ModelFuzz 的核心调度器。
//
// 从整个系统看，Fuzzer 位于三类组件之间：
//  1. Cluster：真实被测分布式系统的适配层，负责执行 Stop/Start、接收消息、
//     处理客户端请求，并在 Tick 时吐出新的网络消息。
//  2. Guider：模型侧反馈组件，例如 TLCStateGuider 会把实际执行产生的 event trace
//     发送给 TLA+ 模型检查器，并返回本次执行是否覆盖了新的模型状态。
//  3. Mutator：基于已有 trace 生成相近但不同的调度轨迹，使测试不只是随机游走，
//     而是围绕“能探索新模型状态”的执行继续扩展。
//
// Fuzzer 自己维护的主要状态是 messageQueues 和 trace 队列。messageQueues 模拟一个
// 可控网络：Cluster.Tick 产生的消息不会立刻送达，而是先按 from/to 方向进入队列；
// 随后 Fuzzer 根据 Choice 选择某条方向队列的前若干条消息投递给目标节点。这样，
// 网络延迟、乱序、部分投递和节点故障都被外部调度显式控制，并能被记录成可重放的
// choice trace。
//
// 每次 iteration 会同时产出两条轨迹：
//   - trace：Fuzzer 做出的调度选择，例如投递哪个方向的消息、在哪一步宕机/恢复、
//     注入哪个客户端请求。Mutator 主要修改的是这条轨迹。
//   - eventTrace：真实系统发生的模型可见事件，例如 SendMessage、DeliverMessage、
//     Add/Remove 节点，以及 Cluster 通过 FuzzContext.AddEvent 追加的协议事件。
//     Guider/TLC 使用这条轨迹判断模型状态覆盖率。
//
// 因此，Fuzzer 的目标不是直接断言某个具体协议行为是否正确，而是把真实实现驱动成
// 一系列可记录、可重放、可交给模型解释的执行，然后用模型覆盖反馈来引导下一批执行。
type Fuzzer struct {
	// messageQueues 是 Fuzzer 持有的模拟网络，key 为 "from_to"，value 为该方向上的 FIFO 消息队列。
	messageQueues map[string]*Queue[Message]
	// nodes 保存本次测试使用的节点 id 集合，当前实现会包含 0..NumNodes。
	nodes []uint64
	// config 保存运行参数、Cluster 构造器、Mutator 和 Guider 等外部策略。
	config *FuzzerConfig
	// mutatedTracesQueue 保存跨 iteration 的“待执行 trace”。Run 会从这里取出一条作为下一轮的 mimicTrace。
	mutatedTracesQueue *Queue[*List[*Choice]]
	// rand 是 Fuzzer 内部的随机源，负责生成随机 trace、故障点和请求点。
	rand *rand.Rand
	// clusterConstructor 用于在每条 iteration 开始时创建被测系统适配实例。
	clusterConstructor ClusterConstructor

	// stats 记录运行过程中的粗粒度统计和错误索引，便于测试结束后分析搜索质量。
	stats map[string]interface{}
}

// traceCtx 是单次 iteration 的临时执行上下文。
//
// Fuzzer 是跨 iteration 存活的全局对象，而 traceCtx 只描述当前这一轮执行。它同时保存
// 两类信息：一类是“输入计划”，也就是本轮准备按什么 schedule、crash、start 和 request
// 去驱动系统；另一类是“输出记录”，也就是本轮边执行边写出的 choice trace 和 event trace。
// 把这些状态放在 traceCtx 里，可以让 RunIteration 的主循环保持“按 step 消费计划、同时
// 记录实际发生内容”的形态，也方便 Cluster 通过 FuzzContext 将额外事件写回当前 event trace。
//
// mimicTrace 是本轮开始前拿到的“目标轨迹”或“参考轨迹”。如果 mimicTrace 不为空，
// RunIteration 会先把它拆成 nodeChoices、crashPoints、startPoints 和 clientRequests，
// 然后主循环按这些计划执行。trace 则不同：trace 是本轮执行过程中重新写出来的“实际轨迹”。
// 因此 mimicTrace 是输入，trace 是输出；二者通常很接近，但 trace 会反映本轮实际采用的
// 调度选择，例如 mimic 中的 Node Choice 用完后临时随机补出来的选择也会写进 trace。
type traceCtx struct {
	// trace 是本轮的输出 choice trace：每执行到一个调度点，就把实际采用的选择追加进来。
	trace *List[*Choice]
	// mimicTrace 是本轮的输入目标 trace：它来自 mutatedTracesQueue，指导本轮尽量按已有轨迹重放。
	mimicTrace *List[*Choice]
	// eventTrace 记录真实执行中模型可见的事件，Guider/TLC 会消费它。
	eventTrace *List[*Event]
	// nodeChoices 只保存 Type=="Node" 的消息投递选择；crash/start/request 会分别放进下面的 map。
	nodeChoices *Queue[*Choice]
	// booleanChoices 预留给需要布尔随机选择的扩展 mutator 或调度逻辑。
	booleanChoices *Queue[bool]
	// integerChoices 预留给需要整数随机选择的扩展 mutator 或调度逻辑。
	integerChoices *Queue[int]
	// crashPoints 以 step 为 key，保存从 trace 中抽出的 StopNode 选择。
	crashPoints map[int]uint64
	// startPoints 以 step 为 key，保存从 trace 中抽出的 StartNode 选择。
	startPoints map[int]uint64
	// clientRequests 以 step 为 key，保存从 trace 中抽出的 ClientRequest 选择。
	clientRequests map[int]string
	// rand 引用 Fuzzer 的随机源，用于 trace 消费完后的兜底随机选择。
	rand *rand.Rand

	// Error 保存本轮执行中的第一个致命错误；主循环看到它后会提前结束本轮。
	Error error
	// fuzzer 反向引用全局 Fuzzer，用于访问节点集合和配置。
	fuzzer *Fuzzer
}

// SetError 允许 Cluster 或 FuzzContext 相关逻辑把“本轮执行应当提前结束”的错误写入
// traceCtx。RunIteration 会在每个外部动作后检查该错误，并把错误归入 Fuzzer.stats。
func (t *traceCtx) SetError(err error) {
	// 只记录错误本身；是否停止执行由 RunIteration 在动作边界统一判断。
	t.Error = err
}

// GetError 返回本轮执行中由被测系统或适配层报告的错误。
func (t *traceCtx) GetError() error {
	// 错误对象会被 RunIteration 转成字符串写入 stats。
	return t.Error
}

// IsError 表示当前 iteration 是否已经出现需要停止执行的错误。
func (t *traceCtx) IsError() bool {
	// 统一用 nil 判断，避免调用方直接依赖 Error 字段。
	return t.Error != nil
}

// GetNextNodeChoice 取得当前 step 的消息投递调度选择。
//
// 一个 Node 类型 Choice 并不直接指定某条具体消息，而是指定 from/to 方向队列和最多
// 投递多少条消息。真正被投递的消息由 Fuzzer.Schedule 从对应队列头部取出。因此，
// trace 记录的是“网络调度策略”，而消息本身来自此前 Tick 产生并入队的真实系统输出。
//
// 当本轮由 mimicTrace 驱动时，nodeChoices 里装的是从 mimicTrace 拆出来的目标选择；
// 如果这些目标选择已经用完，就退回随机选择，保证 iteration 仍能继续运行到配置的 Steps。
// 无论选择来自 mimicTrace 还是临时随机生成，每次选择都会立刻追加到 trace。也就是说，
// mimicTrace 负责“想怎么跑”，trace 负责“这一轮实际上怎么跑”。
func (t *traceCtx) GetNextNodeChoice() (uint64, uint64, int) {
	// fromChoice/toChoice/maxMessages 是本 step 将被写入 trace 的最终调度选择。
	var fromChoice uint64
	var toChoice uint64
	var maxMessages int
	if t.nodeChoices.Size() > 0 {
		// 优先消费输入计划中的 Choice；随机 trace 和 mimicTrace 最终都会被转成 nodeChoices。
		c, _ := t.nodeChoices.Pop()
		fromChoice = c.From
		toChoice = c.To
		maxMessages = c.MaxMessages
	} else {
		// 如果输入计划里的 Node Choice 不够，就临时随机补齐，避免 iteration 提前失去调度输入。
		i := t.rand.Intn(len(t.fuzzer.nodes))
		j := t.rand.Intn(len(t.fuzzer.nodes))
		fromChoice = t.fuzzer.nodes[i]
		toChoice = t.fuzzer.nodes[j]
		maxMessages = t.rand.Intn(t.fuzzer.config.MaxMessages)
	}
	// 写入输出 trace：这里记录的是本轮实际采用的投递方向和投递上限。
	t.trace.Append(&Choice{
		Type:        "Node",
		From:        fromChoice,
		To:          toChoice,
		MaxMessages: maxMessages,
	})

	// RunIteration 随后会用这三个值调用 Schedule，真正取出待投递消息。
	return fromChoice, toChoice, maxMessages
}

// CanCrash 判断当前 step 是否安排了节点宕机，并在发生时同步记录两种轨迹。
//
// choice trace 中的 StopNode 记录的是 Fuzzer 的故障注入选择，供之后重放/变异；
// event trace 中的 Remove 是模型侧可见事件，供 TLA+ 模型把节点集合或节点状态推进到
// 对应状态。这里先记录事件，再由 RunIteration 调用 Cluster.Stop 执行真实系统动作。
func (t *traceCtx) CanCrash(step int) (uint64, bool) {
	// crashPoints 只表示“计划”，是否成功执行由调用方继续调用 Cluster.Stop 决定。
	node, ok := t.crashPoints[step]
	if ok {
		// Remove 是模型侧事件，表示该节点从可用节点集合或活动状态中移除。
		t.eventTrace.Append(&Event{
			Name: "Remove",
			Node: node,
			Params: map[string]interface{}{
				// "i" 与模型动作参数对应，用 int 是为了更容易 JSON 编码给 TLC。
				"i": int(node),
			},
		})
		// StopNode 是调度侧选择，后续可以被 copy/mutate/replay。
		t.trace.Append(&Choice{
			Type: "StopNode",
			Node: node,
			Step: step,
		})
	}
	// ok=false 表示当前 step 没有故障注入，调用方不需要触碰 Cluster。
	return node, ok
}

// CanStart 判断当前 step 是否安排了节点恢复，并记录与模型对应的 Add 事件。
//
// RunIteration 还会检查该节点是否真的处于 crashed 集合中，避免对未宕机节点调用
// Cluster.Start。也就是说，trace 可以表达“第几步尝试恢复哪个节点”，而主循环负责把
// 这个选择解释成符合当前执行状态的真实动作。
func (t *traceCtx) CanStart(step int) (uint64, bool) {
	// startPoints 只说明 trace 计划在该 step 恢复哪个节点。
	node, ok := t.startPoints[step]
	if ok {
		// Add 是模型侧事件，和 CanCrash 中的 Remove 形成节点故障模型的一对动作。
		t.eventTrace.Append(&Event{
			Name: "Add",
			Node: node,
			Params: map[string]interface{}{
				// 参数名保持为 "i"，使 Add/Remove 在 TLA+ 模型侧可复用同一节点参数约定。
				"i": int(node),
			},
		})
		// StartNode 记录调度意图；主循环还会检查该节点当前是否确实 crashed。
		t.trace.Append(&Choice{
			Type: "StartNode",
			Node: node,
			Step: step,
		})
	}
	// 返回 node 和 ok，让 RunIteration 决定是否调用 Cluster.Start。
	return node, ok
}

// IsClientRequest 判断当前 step 是否需要注入客户端请求。
//
// 客户端请求作为 choice trace 的一部分被记录下来，是因为它会明显改变系统输入空间；
// 同一条 trace 重放时必须在同一步注入同一个 request id，才能让真实系统执行和模型
// event trace 保持可比。
func (t *traceCtx) IsClientRequest(step int) (string, bool) {
	// clientRequests 的 key 是 step，value 是稳定的请求编号字符串。
	req, ok := t.clientRequests[step]
	if ok {
		// ClientRequest 进入 choice trace，保证同一请求能在重放时出现在同一调度点。
		t.trace.Append(&Choice{
			Type:    "ClientRequest",
			Request: req,
		})
	}
	// ok=false 表示本 step 没有外部客户端输入。
	return req, ok
}

// FuzzerConfig 描述一次 ModelFuzz 运行的搜索空间和反馈策略。
//
// 这些参数共同决定 Fuzzer 在“随机探索”和“模型反馈引导探索”之间如何取舍：
// Steps、CrashQuota、NumberRequests 和 MaxMessages 决定单条 trace 的形状；
// SeedPopulationSize、ReseedFrequency、Mutator 和 MutPerTrace 决定从已有有效轨迹周围
// 继续扩展的力度；Guider 决定什么样的执行被认为“有价值”，通常是覆盖了新的 TLA+
// 模型状态。
type FuzzerConfig struct {
	// Iterations 是总共执行多少条 fuzz iteration。
	Iterations int

	// Steps 是每条 trace 的调度步数。每个 step 最多包含一次故障/恢复判断、一次方向队列
	// 投递、一次客户端请求注入，以及一次 Cluster.Tick。
	Steps int

	// Mutator 基于已执行 trace 生成新 trace。它通常只改 choice trace，而不是直接操作
	// event trace；event trace 会在新 trace 被真实 Cluster 重放后重新生成。
	Mutator Mutator

	// Guider 检查本轮 trace/eventTrace 的模型覆盖反馈，并提供累计覆盖统计。
	Guider Guider

	// NumNodes 是被测集群的节点数量上界。当前实现会生成 0..NumNodes 这组节点 id，
	// 其中随机调度会刻意避开 0，因此 0 更像是保留 id 或外部环境占位。
	NumNodes int

	// ClusterConstructor 创建真实被测系统的 Cluster 适配实例。
	ClusterConstructor ClusterConstructor

	// MutPerTrace 控制发现新模型状态后，为该 trace 生成多少条变异后续。
	MutPerTrace int

	// SeedPopulationSize 是每次 reseed 时随机生成的种子 trace 数量，用于填充
	// mutatedTracesQueue，避免搜索只依赖单一路径。
	SeedPopulationSize int

	// NumberRequests 是每条随机 trace 中安排多少个客户端请求注入点。
	NumberRequests int

	// CrashQuota 是每条随机 trace 中安排多少个节点宕机点；每个宕机点还会随机安排一个
	// 后续恢复点。
	CrashQuota int

	// MaxMessages 是一次 Node 调度最多从某个 from/to 队列投递多少条消息。
	MaxMessages int

	// ReseedFrequency 控制每隔多少次 iteration 重新生成一批随机种子 trace。
	ReseedFrequency int
}

// NewFuzzer 根据配置创建一个 Fuzzer，并预先建立所有 from/to 方向的网络队列。
//
// Fuzzer 使用 map[string]*Queue[Message] 表示可控网络，key 形如 "from_to"。这样同一
// 方向上的消息保持 FIFO，而不同方向之间的相对投递顺序完全由后续 Node Choice 决定。
// 这里同时初始化统计字段；这些统计不是测试判定本身，而是帮助理解随机执行、变异执行
// 和错误执行在整个搜索中的比例。
func NewFuzzer(config *FuzzerConfig) *Fuzzer {
	// 创建 Fuzzer 主对象；此时还没有真实 Cluster，Cluster 会在每条 iteration 中创建。
	f := &Fuzzer{
		// 保存外部传入的配置，后续所有搜索参数都从这里读取。
		config: config,
		// nodes 稍后根据 NumNodes 填充。
		nodes: make([]uint64, 0),
		// messageQueues 稍后为每个 from/to 方向创建一个独立 FIFO 队列。
		messageQueues: make(map[string]*Queue[Message]),
		// mutatedTracesQueue 初始为空，seed 或 guider 反馈会往里面放 trace。
		mutatedTracesQueue: NewQueue[*List[*Choice]](),
		// 使用当前时间作为随机种子，使每次完整 fuzz 运行默认探索不同路径。
		rand: rand.New(rand.NewSource(time.Now().UnixNano())),
		// ClusterConstructor 来自配置，是 Fuzzer 和被测系统之间的入口。
		clusterConstructor: config.ClusterConstructor,
		// stats 用 map 存储不同类型统计，便于后续按名字扩展。
		stats: make(map[string]interface{}),
	}
	for i := 0; i <= f.config.NumNodes; i++ {
		// 节点 id 使用 0..NumNodes；随机调度中会跳过 0。
		f.nodes = append(f.nodes, uint64(i))
		for j := 0; j <= f.config.NumNodes; j++ {
			// 每个方向单独建队列，避免不同 from/to 的消息混在一起。
			key := fmt.Sprintf("%d_%d", i, j)
			// 队列在不同 iteration 之间复用对象，但每轮开始会 Reset。
			f.messageQueues[key] = NewQueue[Message]()
		}
	}
	// random_executions 统计没有 mimic trace、靠随机生成调度的 iteration 数。
	f.stats["random_executions"] = 0
	// mutated_executions 统计从 mutatedTracesQueue 取 trace 重放的 iteration 数。
	f.stats["mutated_executions"] = 0
	// execution_errors 记录出现过哪些错误字符串。
	f.stats["execution_errors"] = make(map[string]bool, 0)
	// error_executions 记录每类错误分别出现在哪些 iteration 名称中。
	f.stats["error_executions"] = make(map[string][]string)
	// buggy_executions 预留给更高层 bug 判定使用，当前文件里尚未写入。
	f.stats["buggy_executions"] = make(map[string]bool, 0)
	return f
}

// Schedule 从指定 from/to 方向队列取出最多 maxMessages 条消息。
//
// 这是 Fuzzer 控制网络的关键点：Cluster.Tick 只是“发送”消息，消息是否、何时、按什么
// 批量送达，由 Schedule 和 RunIteration 的调度选择决定。当前语义是按队列 FIFO 取前
// maxMessages 条，因此它假设同一方向队列内部的消息入队顺序具有稳定意义；如果某个
// Cluster 实现会以不确定顺序向同一 from/to 方向产生多条消息，那么相同 choice trace
// 可能在重放时投递到不同的具体消息。
func (f *Fuzzer) Schedule(from uint64, to uint64, maxMessages int) []Message {
	// 队列 key 只由发送方和接收方决定，因此同一方向内部保持 FIFO。
	key := fmt.Sprintf("%d_%d", from, to)
	// 查找该方向的消息队列；理论上 NewFuzzer 已经创建所有方向，这里仍做防御检查。
	queue, ok := f.messageQueues[key]
	if !ok || queue.Size() == 0 {
		// 没有该方向队列或队列为空时，本 step 不投递任何消息。
		return []Message{}
	}
	// messages 收集本次调度真正要交给 Cluster.ReceiveMessage 的消息。
	messages := make([]Message, 0)
	for i := 0; i < maxMessages; i++ {
		// Pop 从队头取消息，体现同一 from/to 方向的 FIFO 网络语义。
		message, ok := queue.Pop()
		if !ok {
			// 队列不足 maxMessages 条时提前结束，已经取出的消息仍会被投递。
			break
		}
		// 被取出的消息会离开 Fuzzer 网络队列，表示它已经被本次调度选择交付。
		messages = append(messages, message)
	}
	return messages
}

// recordReceive 将真实消息投递动作翻译成模型可见的 DeliverMessage 事件。
//
// Fuzzer 在调用 Cluster.ReceiveMessage 前记录该事件，因此 event trace 表示的是外部
// 调度已经决定“这条消息被交付给目标节点”。Cluster.ReceiveMessage 内部若产生额外的
// 协议状态变化，可以继续通过 FuzzContext.AddEvent 追加更细粒度事件。
func recordReceive(message Message, eventTrace *List[*Event]) {
	// DeliverMessage 是模型看到的“消息到达”动作，发生在真实 ReceiveMessage 调用之前。
	eventTrace.Append(&Event{
		// Name 必须和 TLA+ 模型中表示交付的动作名称保持一致。
		Name: "DeliverMessage",
		// Node 用于标记该事件发生在哪个节点上，但当前 Event 的 JSON 会忽略这个字段。
		Node: message.To(),
		// Params 是真正传给模型解释的参数集合。
		Params: map[string]interface{}{
			// type/params 描述协议消息内容。
			"type":   message.Type(),
			"params": message.Params(),
			// from/to 描述网络端点。
			"from": message.From(),
			"to":   message.To(),
		},
	})
}

// recordSend 将 Cluster.Tick 返回的出站消息翻译成模型可见的 SendMessage 事件。
//
// Tick 返回的消息随后会进入 Fuzzer 的方向队列，等待未来 step 投递。SendMessage 和
// DeliverMessage 分开记录，使模型能够表达“消息已发送但尚未送达”的网络状态。
func recordSend(message Message, eventTrace *List[*Event]) {
	// SendMessage 是模型看到的“节点发出了消息”动作，和之后是否交付分离。
	eventTrace.Append(&Event{
		// Name 必须和 TLA+ 模型中表示发送的动作名称保持一致。
		Name: "SendMessage",
		// Node 标记发送方节点，便于记录和调试。
		Node: message.From(),
		// Params 中保存消息内容和端点，使 TLC 能更新模型中的网络状态。
		Params: map[string]interface{}{
			"type":   message.Type(),
			"params": message.Params(),
			"from":   message.From(),
			"to":     message.To(),
		},
	})
}

// seed 生成一批随机 trace 作为后续变异的起点。
//
// 这里会实际运行 RunIteration，而不是只构造 Choice 列表，因为只有真实执行后才能知道
// 随机调度在当前 Cluster 下会产生什么 event trace。seed 阶段保存的是 choice trace 的
// 拷贝；之后 Run 会从 mutatedTracesQueue 取出其中一条，作为某次 RunIteration 的
// mimicTrace 输入重新执行，并根据 guider 反馈决定是否继续变异。
func (f *Fuzzer) seed() {
	// 重新播种时先丢弃旧的待变异 trace，避免队列长期受历史路径支配。
	f.mutatedTracesQueue.Reset()
	for i := 0; i < f.config.SeedPopulationSize; i++ {
		// nil mimic 表示这次 seed 不参考已有目标轨迹，而是生成并执行一条全新的随机 trace。
		trace, _ := f.RunIteration(fmt.Sprintf("pop_%d", i), nil)
		// 入队前复制 trace，避免后续 mutator 或执行过程修改同一个底层对象。
		f.mutatedTracesQueue.Push(copyTrace(trace, defaultCopyFilter()))
	}
}

// Run 执行完整的模型引导 fuzzing 循环。
//
// 每次 iteration 的输入要么来自 mutatedTracesQueue 中已有的变异 trace，要么在没有可用
// trace 时由 RunIteration 随机生成。来自 mutatedTracesQueue 的 trace 会作为本轮的
// mimicTrace，表示“这一轮尽量照着这条目标轨迹跑”。执行结束后，Fuzzer 将本轮实际输出的
// choice trace 和 event trace
// 交给 Guider。若 Guider 认为本轮带来了新的模型状态覆盖，Fuzzer 就把这条 trace 交给
// Mutator 生成更多相邻调度，放回 mutatedTracesQueue。这样搜索会自然偏向那些已经证明
// 能推动模型进入新状态的调度区域。
//
// ReseedFrequency 会周期性清空并重新生成种子集合，避免搜索长期困在某一批变异轨迹
// 附近。函数返回最后一次记录到的覆盖统计。
func (f *Fuzzer) Run() CoverageStats {
	// coverages 保存每轮结束后的累计覆盖统计，函数最后返回最后一次。
	coverages := make([]CoverageStats, 0)
	for i := 0; i < f.config.Iterations; i++ {
		// 周期性重新生成随机种子，给搜索注入新的起点。
		if i%f.config.ReseedFrequency == 0 {
			f.seed()
		}
		// 简单输出当前进度；\r 使终端在同一行刷新。
		fmt.Printf("\rRunning iteration: %d/%d", i+1, f.config.Iterations)
		// mimic 为 nil 表示本轮没有目标轨迹，RunIteration 会自己随机生成调度计划。
		var mimic *List[*Choice] = nil
		if f.mutatedTracesQueue.Size() > 0 {
			// 优先从全局待执行队列取一条 trace，作为本轮 RunIteration 的 mimicTrace。
			f.stats["mutated_executions"] = f.stats["mutated_executions"].(int) + 1
			mimic, _ = f.mutatedTracesQueue.Pop()
		} else {
			// 没有变异 trace 可用时退回随机探索。
			f.stats["random_executions"] = f.stats["random_executions"].(int) + 1
		}
		// 执行一轮真实 Cluster：mimic 是输入目标轨迹，返回的 trace 是本轮实际输出轨迹。
		trace, eventTrace := f.RunIteration(fmt.Sprintf("fuzz_%d", i), mimic)
		// Guider.Check 会把 eventTrace 交给模型侧，并返回本轮带来的新状态数量/比例。
		if _, numNewStates := f.config.Guider.Check(trace, eventTrace); numNewStates > 0 {
			// 新覆盖越多，就围绕该 trace 生成越多变异后续。
			numMutations := int(numNewStates) * f.config.MutPerTrace
			for j := 0; j < numMutations; j++ {
				// Mutator 基于当前 trace/eventTrace 尝试生成一条新的 choice trace。
				new, ok := f.config.Mutator.Mutate(trace, eventTrace)
				if ok {
					// 成功生成的变异 trace 入队，等待后续 iteration 重放。
					f.mutatedTracesQueue.Push(copyTrace(new, defaultCopyFilter()))
				}
			}
		}
		// 记录本轮结束后的累计覆盖，便于返回最终覆盖结果。
		coverages = append(coverages, f.config.Guider.Coverage())
	}
	// 返回最后一轮后的覆盖统计；调用方通常关心最终 UniqueStates 等指标。
	return coverages[len(coverages)-1]
}

// RunIteration 执行一条具体 trace，并返回本轮实际采用的 choice trace 和 event trace。
//
// 如果 mimic 为 nil，本函数会随机生成一条调度计划：每个 step 的 from/to 投递方向、
// 若干宕机点及其后续恢复点、若干客户端请求注入点。如果 mimic 非 nil，mimic 就是本轮的
// 输入目标轨迹，函数会把其中的 Choice 拆解成同样的数据结构，用同一个 episode loop
// 尽量按目标轨迹执行。无论输入来自随机生成还是 mimic，函数返回的 trace 都是本轮边执行
// 边记录出的实际轨迹。
//
// episode loop 中每个 step 的顺序固定为：
//  1. 按计划 Stop 节点，并记录 Remove 事件。
//  2. 按计划 Start 已宕机节点，并记录 Add 事件。
//  3. 根据 Node Choice 从某个 from/to 队列投递最多 maxMessages 条消息。
//  4. 按计划注入客户端请求。
//  5. 调用 Cluster.Tick，收集新出站消息，记录 SendMessage 并入队。
//
// 这个顺序就是 ModelFuzz 对被测系统施加的离散时间语义。Cluster 实现越能把内部并发
// 和真实时间压缩成确定的 Tick/Receive/ClientRequest 效果，trace 的重放和模型对照就越
// 稳定。
func (f *Fuzzer) RunIteration(iteration string, mimic *List[*Choice]) (*List[*Choice], *List[*Event]) {
	// 为本轮执行创建独立上下文。trace/eventTrace 都从空开始，所有预定调度也只在本轮
	// 生效，避免不同 iteration 之间共享临时执行状态。
	tCtx := &traceCtx{
		// trace 从空开始，后续每次调度选择都会追加进去。
		trace: NewList[*Choice](),
		// eventTrace 从空开始，后续 Send/Deliver/Add/Remove 和 Cluster 自定义事件都会追加进去。
		eventTrace: NewList[*Event](),
		// nodeChoices 保存每个 step 的 from/to/maxMessages 选择。
		nodeChoices: NewQueue[*Choice](),
		// booleanChoices 当前未在本文件使用，保留给扩展调度维度。
		booleanChoices: NewQueue[bool](),
		// integerChoices 当前未在本文件使用，保留给扩展调度维度。
		integerChoices: NewQueue[int](),
		// crashPoints/startPoints/clientRequests 用 map 表示“在哪个 step 触发什么外部动作”。
		crashPoints:    make(map[int]uint64),
		startPoints:    make(map[int]uint64),
		clientRequests: make(map[int]string),
		// rand 复用 Fuzzer 的随机源，使本轮所有随机选择来自同一源。
		rand: f.rand,
		// fuzzer 让 traceCtx 方法能访问全局节点列表和配置。
		fuzzer: f,
	}
	// 每轮创建新的 Cluster 适配实例，使被测系统的生命周期和 iteration 对齐。
	cluster := f.clusterConstructor.NewCluster(f.nodes)
	if mimic != nil {
		// 重放/变异路径：mimic 是本轮的输入目标轨迹，先把线性的 Choice 序列恢复成
		// episode loop 更容易消费的索引结构。
		// 保存原始 mimicTrace，便于调试时知道本轮是由哪条目标轨迹驱动的。
		tCtx.mimicTrace = mimic
		for i := 0; i < mimic.Size(); i++ {
			// 按序读取每个 Choice；Get 返回的 ok 在这里被忽略，因为 i 来自 Size 范围内。
			ch, _ := mimic.Get(i)
			// mimicTrace 是混合 trace：Node 进入 nodeChoices 按顺序消费；
			// StopNode/StartNode/ClientRequest 则按 step 放进 map，到对应 step 触发。
			switch ch.Type {
			case "Node":
				// Node Choice 是有序队列，因为每个 step 都会消费下一个消息调度选择。
				tCtx.nodeChoices.Push(ch.Copy())
			case "StartNode":
				// StartNode 按 step 建索引，方便主循环 O(1) 判断当前 step 是否恢复节点。
				tCtx.startPoints[ch.Step] = ch.Node
			case "StopNode":
				// StopNode 按 step 建索引，方便主循环 O(1) 判断当前 step 是否宕机节点。
				tCtx.crashPoints[ch.Step] = ch.Node
			case "ClientRequest":
				// ClientRequest 按 step 建索引，保证重放时请求出现在原来的调度点。
				tCtx.clientRequests[ch.Step] = ch.Request
			}
		}
	} else {
		// 随机路径：本轮没有输入目标轨迹，因此先为每个 step 预生成一次方向队列选择。
		// 节点 id 0 会被跳过，保持和 NewFuzzer 中节点集合的保留 id 约定一致。
		for i := 0; i < f.config.Steps; i++ {
			// fromIdx 先置 0，再循环随机，直到选中非 0 节点。
			var fromIdx int = 0
			for fromIdx == 0 {
				fromIdx = f.rand.Intn(len(f.nodes))
			}
			// toIdx 同样跳过 0，避免随机调度把消息投递给保留节点。
			var toIdx int = 0
			for toIdx == 0 {
				toIdx = f.rand.Intn(len(f.nodes))
			}
			// 把本 step 的网络调度选择放入队列，真正记录到 trace 要等执行时发生。
			tCtx.nodeChoices.Push(&Choice{
				// Type="Node" 表示这是一次消息方向调度，而不是节点本身的状态变化。
				Type: "Node",
				// From/To 指定本 step 要查看哪条方向队列。
				From: f.nodes[fromIdx],
				To:   f.nodes[toIdx],
				// MaxMessages 指定最多投递多少条队头消息，可能为 0。
				MaxMessages: f.rand.Intn(f.config.MaxMessages),
			})
		}
		// crash/start/request 都是按 step 预先采样的外部输入。主循环只负责在走到对应
		// step 时解释这些输入，并把它们同步写入 choice trace 与 event trace。
		// choices 是所有合法 step 编号的候选集合。
		choices := make([]int, f.config.Steps)
		for i := 0; i < f.config.Steps; i++ {
			// 每个元素就是一个 step 编号，供 sample 随机抽取。
			choices[i] = i
		}
		// 随机抽取 CrashQuota 个 step 作为宕机点。
		for _, c := range sample(choices, f.config.CrashQuota, f.rand) {
			// 随机选择一个非 0 节点作为宕机节点。
			var idx int = 0
			for idx == 0 {
				idx = f.rand.Intn(len(f.nodes))
			}
			// 记录 step c 宕机节点 idx。
			tCtx.crashPoints[c] = uint64(idx)
			// 恢复点从 [c, Steps) 中抽取，表示节点可能在宕机当步或之后恢复。
			s := sample(intRange(c, f.config.Steps), 1, f.rand)[0]
			// 记录恢复点和宕机点使用同一个节点。
			tCtx.startPoints[s] = uint64(idx)
		}
		// 请求编号从 "1" 开始递增，作为模型和实现之间的稳定请求 id。
		i := 1
		// 随机抽取 NumberRequests 个 step 注入客户端请求。
		for _, req := range sample(choices, f.config.NumberRequests, f.rand) {
			// map 的 key 是 step，value 是请求编号字符串。
			tCtx.clientRequests[req] = strconv.Itoa(i)
			// 下一个请求使用新的编号，避免多个请求在模型里无法区分。
			i++
		}
	}

	// 每条 trace 都从干净网络和干净 Cluster 状态开始。messageQueues 属于 Fuzzer，
	// Cluster 内部状态属于被测系统适配层，两者都需要重置才能保证重放可比。
	for _, q := range f.messageQueues {
		// 清空所有方向队列，避免上一轮未投递消息泄漏到本轮。
		q.Reset()
	}
	// 重置真实被测系统，使后续执行只由本轮 trace 决定。
	cluster.Reset()

	// crashed 是 Fuzzer 侧维护的节点故障集合，用于跳过对宕机目标节点的消息投递。
	crashed := make(map[uint64]bool)
	// fCtx 是传给 Cluster 的受限上下文，允许 Cluster 追加模型事件。
	fCtx := &FuzzContext{traceCtx: tCtx}
EpisodeLoop:
	for j := 0; j < f.config.Steps; j++ {
		// 故障注入优先于消息投递，表示从本 step 开始该节点已经不可接收后续消息。
		if toCrash, ok := tCtx.CanCrash(j); ok {
			// 调用真实 Cluster 停止该节点，使实现状态和模型事件保持同步。
			err := cluster.Stop(fCtx, toCrash)
			if err != nil || tCtx.IsError() {
				// Cluster 返回错误或主动设置错误时，本轮 trace 提前结束。
				break EpisodeLoop
			}
			// Fuzzer 记录该节点已经宕机，后续投递逻辑会参考这个集合。
			crashed[toCrash] = true
		}
		// 恢复只对当前确实宕机的节点生效。这样随机/变异 trace 中多余的 StartNode 不会
		// 直接破坏 Cluster 的故障模型。
		if toStart, ok := tCtx.CanStart(j); ok {
			// 检查恢复目标是否在 Fuzzer 侧故障集合中。
			_, isCrashed := crashed[toStart]
			if isCrashed {
				// 调用真实 Cluster 恢复该节点。
				err := cluster.Start(fCtx, toStart)
				if err != nil || tCtx.IsError() {
					// 恢复过程中出错同样终止本轮。
					break EpisodeLoop
				}
				// 恢复成功后，从故障集合中移除。
				delete(crashed, toStart)
			}
		}
		// 从 Fuzzer 的可控网络中取消息投递。若目标节点处于 crashed 状态，本 step 的
		// 方向选择仍会写入 choice trace，但不会实际投递消息。
		from, to, maxMessages := tCtx.GetNextNodeChoice()
		// 只检查目标节点是否宕机；发送方是否宕机影响的是 Cluster.Tick 是否继续产生消息。
		if _, ok := crashed[to]; !ok {
			// 从指定方向队列按 FIFO 取出最多 maxMessages 条消息。
			messages := f.Schedule(from, to, maxMessages)
			for _, m := range messages {
				// 先记录 DeliverMessage，再让真实系统处理，保证模型看到同一投递动作。
				recordReceive(m, tCtx.eventTrace)
				// 把消息交给 Cluster，触发真实协议处理。
				err := cluster.ReceiveMessage(fCtx, m)
				if err != nil || tCtx.IsError() {
					// 消息处理过程中出现错误时终止本轮。
					break EpisodeLoop
				}
			}
		}

		// 客户端请求是外部环境输入，放在消息投递之后、Tick 之前，使被测系统有机会在
		// 同一 step 的 Tick 中把请求转化为出站协议消息。
		if reqNum, ok := tCtx.IsClientRequest(j); ok {
			// 把稳定请求编号交给 Cluster，由适配层转成真实客户端操作。
			err := cluster.ClientRequest(fCtx, reqNum)
			if err != nil || tCtx.IsError() {
				// 客户端请求触发错误时终止本轮。
				break EpisodeLoop
			}
		}

		// Tick 是本 step 的最后一次系统推进。所有出站消息都先记录为 SendMessage，
		// 再进入可控网络队列，等待之后的 Node Choice 决定是否交付。
		for _, n := range cluster.Tick(fCtx) {
			// 把真实系统产生的出站消息记录为模型侧 SendMessage。
			recordSend(n, tCtx.eventTrace)
			// 根据消息端点找到对应方向队列。
			key := fmt.Sprintf("%d_%d", n.From(), n.To())
			// 消息不会立即送达，而是先进入 Fuzzer 控制的网络。
			f.messageQueues[key].Push(n)
		}
	}
	// Cluster 通过返回 error 或设置 tCtx.Error 报告执行异常后，Fuzzer 会保留错误到
	// stats 中，便于之后定位哪些 iteration 触发了同类问题。
	if tCtx.IsError() {
		// 以错误字符串作为聚合 key，同类错误只在 execution_errors 中出现一次。
		errS := tCtx.GetError().Error()
		f.stats["execution_errors"].(map[string]bool)[errS] = true
		if _, ok := f.stats["error_executions"].(map[string][]string)[errS]; !ok {
			// 第一次遇到该错误时，先创建 iteration 名称列表。
			f.stats["error_executions"].(map[string][]string)[errS] = make([]string, 0)
		}
		// 记录本轮 iteration 名称，方便之后定位对应 trace。
		f.stats["error_executions"].(map[string][]string)[errS] = append(f.stats["error_executions"].(map[string][]string)[errS], iteration)
	}

	// 返回 choice trace 和 event trace；前者供变异，后者供模型覆盖检查。
	return tCtx.trace, tCtx.eventTrace
}

// FuzzContext 是暴露给 Cluster 的受限上下文。
//
// Cluster 不应该直接操作 Fuzzer 或 traceCtx 的内部数据结构；它只通过 FuzzContext 把
// 真实系统中模型可见、但不能由 SendMessage/DeliverMessage/Add/Remove 自动表达的事件
// 追加到 event trace。这样 Cluster 既能补充协议特有事件，又不会破坏 Fuzzer 对调度和
// 网络队列的集中控制。
type FuzzContext struct {
	traceCtx *traceCtx
}

// AddEvent 把 Cluster 观察到的模型事件追加到本轮 event trace。
//
// 典型用途包括记录客户端请求被提交、状态机应用命令、选举超时、持久化状态变化等
// TLA+ 模型需要看见的动作。事件名称和参数应与模型动作保持一致，并尽量使用稳定、
// 可 JSON 序列化的数据。
func (f *FuzzContext) AddEvent(e *Event) {
	// Cluster 追加的事件会和 Fuzzer 自动记录的 Send/Deliver/Add/Remove 位于同一 event trace。
	f.traceCtx.eventTrace.Append(e)
}
