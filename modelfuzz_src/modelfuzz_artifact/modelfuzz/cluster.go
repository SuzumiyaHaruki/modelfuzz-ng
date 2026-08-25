package modelfuzz

// Cluster 是 ModelFuzz 和被测分布式系统之间的适配层。
//
// ModelFuzz 自己并不理解 Raft、Paxos、链复制或某个具体服务的内部状态机；
// 它只负责产生一条可重放的调度轨迹：在哪一步宕机/恢复哪个节点、投递哪些
// 网络消息、何时注入客户端请求，以及每一步结束后让系统向前推进一次。具体
// 系统如何启动节点、如何保存未发送消息、如何把客户端请求转成协议动作，都由
// Cluster 的实现完成。
//
// 一次 fuzz iteration 中，Fuzzer 的典型调用顺序是：
//  1. 通过 ClusterConstructor.NewCluster 创建一个和当前节点集合对应的实例。
//  2. 清空 Fuzzer 内部的网络队列，然后调用 Reset，让被测系统回到初始状态。
//  3. 在每个 step 中按轨迹选择执行 Stop/Start、ReceiveMessage、ClientRequest。
//  4. 调用 Tick 收集系统新产生的 Message，并放入 Fuzzer 的网络队列，等待之后
//     被 Schedule 选择性投递。
//  5. Fuzzer 将调度选择 trace 和实际事件 event trace 交给 Guider；例如
//     TLCStateGuider 会把 event trace 发送给 TLC 服务器，用 TLA+ 模型状态覆盖率
//     来决定后续 trace 是否值得变异。
//
// 因此，Cluster 实现的核心职责不是“自己做随机测试”，而是把真实系统包装成一个
// 可被外部精确调度、可记录、可重放的执行环境。所有会影响模型状态的系统动作，
// 要么通过返回 Message 让 Fuzzer 记录 SendMessage/DeliverMessage，要么通过
// FuzzContext.AddEvent 主动追加和 TLA+ 模型动作同名的 Event。
type Cluster interface {
	// Reset 将被测系统恢复到一次 iteration 的初始状态。
	//
	// Fuzzer 会在每次 RunIteration 开始时调用 Reset，同时也会清空自己的网络消息队列。
	// Cluster 实现应在这里重建或清理所有会跨执行残留的状态，例如节点进程、内存状态、
	// 定时器、mock 网络、持久化目录、客户端上下文等。Reset 之后，后续 Stop/Start、
	// ReceiveMessage、ClientRequest 和 Tick 的效果应只由本次 iteration 的调度轨迹决定，
	// 这样同一条 trace 才能被变异器和 guider 可靠地复现。
	Reset()

	// Stop 模拟指定节点在当前 step 宕机。
	//
	// Fuzzer 会先把这次故障写入 choice trace 和 event trace，再调用 Stop。Cluster
	// 应让该节点停止处理消息、停止产生新的协议输出，并使之后针对该节点的投递符合
	// “节点已宕机”的语义。Fuzzer 自身会跳过向已宕机目标节点投递消息，但具体系统中
	// 仍可能需要在这里处理进程暂停、连接关闭、定时器冻结、磁盘保留或清理等细节。
	//
	// 如果被测系统发现不可恢复的执行错误，可以返回 error，或通过 fCtx 记录错误后让
	// Fuzzer 结束当前 iteration。普通的协议分支不应作为 error 返回；error 更适合表示
	// 测试环境或实现违反了期望，后续会被 Fuzzer 统计到 execution_errors 中。
	Stop(fCtx *FuzzContext, node uint64) error

	// Start 模拟指定节点在此前宕机后恢复。
	//
	// Fuzzer 只会对自己记录为 crashed 的节点调用 Start。Cluster 实现需要恢复该节点
	// 能力，并按被测系统的故障模型决定是否保留宕机前的持久化状态、重置易失状态、
	// 重新注册定时器或重新连接 mock 网络。Start 产生的模型可见动作如果不能通过
	// Message 表达，应使用 fCtx.AddEvent 追加到 event trace 中，以便 guider/TLC 看到
	// 与真实系统一致的状态迁移。
	Start(fCtx *FuzzContext, node uint64) error

	// ReceiveMessage 把一条由 Fuzzer 选中的网络消息投递给目标节点。
	//
	// Tick 返回的 Message 不会立刻送达，而是先进入 Fuzzer 按 From/To 划分的队列。
	// 每个 step 中，Fuzzer 根据当前 trace 选择一个 from/to 方向和最多投递数量，然后
	// 对被选中的消息逐条调用 ReceiveMessage。也就是说，网络乱序、延迟、丢失和部分
	// 投递主要由 Fuzzer 的调度决定；Cluster 在这里应专注于“目标节点收到这条消息后”
	// 的协议处理。Fuzzer 会在调用前记录 DeliverMessage 事件，因此实现通常不需要为
	// 普通消息投递重复记录同名事件，只需要记录额外的、模型需要区分的内部动作。
	ReceiveMessage(fCtx *FuzzContext, message Message) error

	// ClientRequest 在当前 step 注入一个客户端请求。
	//
	// reqNum 是 Fuzzer 为本次 iteration 生成的请求编号字符串，用于让真实系统和 TLA+
	// 模型在 event trace 中谈论同一个客户端操作。Cluster 可以把它解释成写请求编号、
	// 操作 id、payload id，或再映射成被测系统需要的请求对象。重要的是：同一条 trace
	// 重放时，相同 reqNum 应导致相同的客户端语义，这样 guider 才能把不同执行路径和
	// 模型状态覆盖率稳定关联起来。
	ClientRequest(fCtx *FuzzContext, reqNum string) error

	// Tick 让被测系统在当前调度点前进一小步，并返回这一步产生的所有出站消息。
	//
	// Fuzzer 在每个 step 的末尾调用 Tick，然后为返回的每条 Message 记录 SendMessage，
	// 再放入内部网络队列，等待未来某个 step 决定是否投递。Cluster 实现应把 Tick 设计成
	// 有界且可重复的推进动作，例如触发一次逻辑时钟、执行一轮 ready/advance、处理到
	// 当前没有立即可执行任务为止等；不要在 Tick 内部自行随机投递网络消息，否则 trace
	// 就无法完整表达系统行为。
	//
	// Tick 返回的消息应包含稳定的 From、To、Type 和 Params。Type/Params 是连接真实
	// 实现与 TLA+ 模型的重要桥梁：它们会进入 event trace，并由 guider 发送给模型检查器
	// 解释。
	Tick(fCtx *FuzzContext) []Message
}

// ClusterConstructor 为每次 fuzz iteration 创建独立的 Cluster 实例。
//
// Fuzzer 会在初始化时生成节点 id 列表，并在每次 RunIteration 开始时把这份列表传给
// NewCluster。构造函数适合放置和“节点集合”相关的一次性搭建工作，例如创建节点对象、
// 初始化 mock 网络端点、准备临时目录或注入依赖。真正的执行状态仍应由 Cluster.Reset
// 明确初始化，这样同一个构造出来的环境即便将来被复用，也不会依赖上一次执行的残留。
type ClusterConstructor interface {
	NewCluster(nodes []uint64) Cluster
}

// Message 是 Cluster 暴露给 Fuzzer 的网络消息抽象。
//
// Fuzzer 不解析具体协议负载，只使用 From/To 决定消息进入哪条方向队列，并使用 Type
// 和 Params 生成 SendMessage/DeliverMessage 事件。换句话说，Message 同时承担两件事：
// 一是作为真实系统稍后 ReceiveMessage 的输入，二是作为 TLA+ 模型可理解的事件描述。
//
// 实现方应保证 Message 在进入 Fuzzer 队列后语义稳定。尤其是 Params 返回的内容会被
// 写入 event trace 并序列化发送给 guider；如果其中包含指针、可变 map 或不可 JSON 化的
// 对象，可能导致重放结果和模型检查结果不稳定。通常更推荐在 Message 中保存不可变的
// 标量、字符串、数组或 map 快照。
type Message interface {
	// From 返回发送方节点 id，用于决定消息所属的网络方向队列。
	From() uint64

	// To 返回接收方节点 id。Fuzzer 会在目标节点宕机时跳过对此节点的投递。
	To() uint64

	// Type 返回模型可识别的消息类型名，例如 AppendEntries、VoteRequest 等。
	Type() string

	// Params 返回模型执行该消息所需的参数快照。
	//
	// 这些参数会出现在 SendMessage/DeliverMessage 事件中，并最终被 TLC guider 序列化。
	// 因此它们应尽量保持确定、可 JSON 编码，并与 TLA+ 模型中的动作参数命名保持一致。
	Params() map[string]interface{}
}
