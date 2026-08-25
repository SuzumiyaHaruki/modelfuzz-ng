package modelfuzz

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"strconv"
	"strings"
	"sync"
)

// CoverageStats 是 Guider 暴露给 Fuzzer 和调用方的覆盖率统计摘要。
//
// Fuzzer 的搜索循环并不直接理解 TLA+ 模型内部状态；它只关心 Guider 总结出的覆盖反馈。
// 当前统计只包含 UniqueStates，也就是已经见过多少个唯一模型状态。后续如果需要更丰富的
// 指标，例如唯一状态轨迹数、唯一调度数、错误状态数，也可以从这里扩展。
type CoverageStats struct {
	// UniqueStates 是 Guider 到目前为止累计见过的唯一模型状态数量。
	UniqueStates int
}

// Guider 是 ModelFuzz 的“反馈层”接口。
//
// Fuzzer 负责生成和执行 trace，Cluster 负责驱动真实系统，而 Guider 负责回答一个更高层的
// 问题：这次执行是否把模型带到了新的状态空间区域？在默认实现 TLCStateGuider 中，这个
// 回答来自 TLC 服务器：Fuzzer 把 event trace 发给 TLC，TLC 按 TLA+ 模型执行这些事件并
// 返回状态序列，Guider 再统计哪些状态是第一次出现。
//
// 因此 Guider 不直接调度消息，也不直接修改 Cluster。它只消费一次 iteration 产生的两条
// 轨迹：
//   - choice trace：Fuzzer 的调度选择，主要用于记录、去重和后续变异。
//   - event trace：真实系统发生的模型可见事件，是发送给模型检查器的主要输入。
//
// Fuzzer.Run 会根据 Check 的返回值决定是否把当前 trace 交给 Mutator 继续扩展。
type Guider interface {
	// Check 检查一次 iteration 的执行结果，并返回“新增模型状态数量”和“相对新增比例”。
	//
	// trace 是本轮实际输出的 choice trace，eventTrace 是对应的模型事件轨迹。具体 Guider
	// 可以只使用 eventTrace 做模型执行，也可以同时使用 trace 做去重、记录或调试。
	Check(*List[*Choice], *List[*Event]) (int, float64)

	// Coverage 返回当前累计覆盖统计，供 Fuzzer 在每轮结束后记录或最终返回。
	Coverage() CoverageStats

	// Reset 清空 Guider 已累计的覆盖状态。key 可用于区分实验阶段或外部调用场景；
	// 当前 TLCStateGuider 实现没有使用这个参数。
	Reset(string)
}

// TLCStateGuider 是基于 TLC 状态覆盖率的 Guider 实现。
//
// 它把 Fuzzer 生成的 event trace 发送给 TLC server，由 TLC 根据 TLA+ 模型执行事件序列，
// 返回一串模型状态。TLCStateGuider 再用状态 key 维护全局去重集合，把“本轮新增了多少个
// 模型状态”作为反馈传回 Fuzzer。这样 Fuzzer 不需要知道模型变量如何定义，只需要根据
// 新状态数量决定是否围绕当前 trace 继续变异。
//
// 除了状态覆盖，它还记录两类辅助去重信息：
//   - tracesMap：choice trace 的 hash，用于知道哪些调度轨迹已经出现过。
//   - stateTracesMap：TLC 返回的状态序列 hash，用于知道哪些模型状态路径已经出现过。
//
// recordTraces 打开时，Guider 会把 choice trace、event trace 和模型状态 trace 一起写成
// JSON 文件，方便离线分析某条调度如何映射到模型执行。
type TLCStateGuider struct {
	// TLCAddr 是 TLC server 地址，格式通常是 "host:port"。
	TLCAddr string
	// statesMap 以 TLC 返回的状态 key 为索引，记录全局已经覆盖过的模型状态。
	statesMap map[int64]bool
	// tracesMap 以 choice trace 的 hash 为索引，记录已经执行过的调度轨迹。
	tracesMap map[string]bool
	// stateTracesMap 以状态序列 hash 为索引，记录已经出现过的模型状态路径。
	stateTracesMap map[string]bool
	// tlcClient 封装 HTTP 请求，负责把 event trace 发给 TLC server。
	tlcClient *TLCClient
	// recordPath 是 trace 记录文件的输出目录；为空时通常表示不写文件。
	recordPath string
	// recordTraces 控制是否把每次 Check 的 trace/eventTrace/stateTrace 落盘。
	recordTraces bool
	// count 是记录文件的递增编号，用于生成 0.json、1.json 等文件名。
	count int

	// lock 保护 statesMap、tracesMap、stateTracesMap 等共享状态。
	lock *sync.Mutex
}

// 编译期检查 TLCStateGuider 是否实现了 Guider 接口。
var _ Guider = &TLCStateGuider{}

// NewTLCStateGuider 创建一个使用 TLC server 的状态覆盖率 Guider。
//
// tlcAddr 指向已经启动的 TLC 控制服务；recordPath 指定可选的 trace 记录目录；
// recordTraces 为 true 时，每次 Check 都会把调度轨迹、事件轨迹和 TLC 状态轨迹写入 JSON。
func NewTLCStateGuider(tlcAddr, recordPath string, recordTraces bool) *TLCStateGuider {
	// 如果指定了记录目录，先清理旧结果，确保本次实验输出不会和上次混在一起。
	if recordPath != "" {
		// 目录已存在时递归删除旧内容。
		if _, err := os.Stat(recordPath); err == nil {
			os.RemoveAll(recordPath)
		}
		// 创建新的记录目录；这里忽略错误，后续 os.Create 失败时 recordTrace 会直接返回。
		os.Mkdir(recordPath, 0777)
	}
	// 初始化所有覆盖集合和 TLC HTTP client。
	return &TLCStateGuider{
		// 保存 server 地址，主要用于调试和外部查看配置。
		TLCAddr: tlcAddr,
		// 初始时还没有见过任何模型状态。
		statesMap: make(map[int64]bool),
		// 初始时还没有记录过任何 choice trace。
		tracesMap: make(map[string]bool),
		// 初始时还没有记录过任何状态路径。
		stateTracesMap: make(map[string]bool),
		// TLCClient 负责后续 Check 中的 HTTP 通信。
		tlcClient: NewTLCClient(tlcAddr),
		// 保存 trace 记录目录和开关。
		recordPath:   recordPath,
		recordTraces: recordTraces,
		// 第一个记录文件从 0.json 开始。
		count: 0,
		// 多个 goroutine 共享 Guider 时，用这把锁保护 map。
		lock: new(sync.Mutex),
	}
}

// Reset 清空 Guider 的累计覆盖状态。
//
// Fuzzer.Run 当前不会主动调用 Reset；这个方法主要留给外部实验控制逻辑，或者在同一个
// Guider 实例上开始新一组实验时使用。key 参数当前未使用。
func (t *TLCStateGuider) Reset(key string) {
	// 写 map 前加锁，避免并发 Check/Coverage 时读写冲突。
	t.lock.Lock()
	// 清空已覆盖模型状态。
	t.statesMap = make(map[int64]bool)
	// 清空已见 choice trace。
	t.tracesMap = make(map[string]bool)
	// 清空已见状态路径。
	t.stateTracesMap = make(map[string]bool)
	// 释放锁；这里没有 defer，是因为函数非常短。
	t.lock.Unlock()
}

// Coverage 返回当前累计模型状态覆盖情况。
func (t *TLCStateGuider) Coverage() CoverageStats {
	// 加锁读取 statesMap，避免和 Check 中写入新状态并发冲突。
	t.lock.Lock()
	defer t.lock.Unlock()
	return CoverageStats{
		// statesMap 的大小就是当前唯一模型状态数量。
		UniqueStates: len(t.statesMap),
	}
}

// Check 把一次真实执行产生的 event trace 交给 TLC，并用返回的模型状态更新覆盖集合。
//
// 返回值含义：
//   - 第一个返回值 numNewStates：本次 eventTrace 产生的状态序列里，有多少状态此前没见过。
//   - 第二个返回值 ratio：numNewStates 相对检查前已有状态数的比例，已有状态数为 0 时按 1
//     处理，避免除零。
//
// trace 本身不会发送给 TLC；它用于记录和去重。eventTrace 才是模型执行输入，因为其中包含
// SendMessage、DeliverMessage、Add/Remove 以及 Cluster.AddEvent 追加的协议事件。
func (t *TLCStateGuider) Check(trace *List[*Choice], eventTrace *List[*Event]) (int, float64) {
	// 把 choice trace 序列化，用 hash 表示“这条调度轨迹”。
	bs, _ := json.Marshal(trace)
	// sha256 让不同长度的 trace 都能映射成固定长度 key。
	sum := sha256.Sum256(bs)
	// hex 字符串适合放进 map，也便于调试输出。
	hash := hex.EncodeToString(sum[:])
	// 更新 tracesMap，记录这条调度轨迹已经出现过。
	t.lock.Lock()
	if _, ok := t.tracesMap[hash]; !ok {
		// fmt.Printf("New trace: %s\n", hash)
		t.tracesMap[hash] = true
	}
	t.lock.Unlock()

	// 记录调用 TLC 前已经覆盖的状态数，用于计算本轮相对新增比例。
	t.lock.Lock()
	curStates := len(t.statesMap)
	t.lock.Unlock()
	// numNewStates 只统计本次 TLC 返回状态中第一次出现在 statesMap 的状态。
	numNewStates := 0
	// eventTrace 发送给 TLC server 后，TLC 会按模型执行事件并返回状态序列。
	if tlcStates, err := t.tlcClient.SendTrace(eventTrace); err == nil {
		// 如果开启记录，把本轮输入 trace、eventTrace 和输出模型状态一起写入文件。
		t.recordTrace(trace, eventTrace, tlcStates)
		// 遍历 TLC 返回的每个模型状态，更新全局状态覆盖集合。
		for _, s := range tlcStates {
			t.lock.Lock()
			// State.Key 是 TLC 侧给状态的稳定唯一 key，比直接比较 Repr 更适合作为去重依据。
			_, ok := t.statesMap[s.Key]
			if !ok {
				// 第一次见到该状态，本轮新增状态数加一。
				numNewStates += 1
				// 写入全局状态集合，之后再看到同一 key 就不再算新增。
				t.statesMap[s.Key] = true
			}
			t.lock.Unlock()
		}
		// 除了单个状态覆盖，还把整条状态序列做 hash，用于记录模型路径是否新出现。
		bs, _ := json.Marshal(tlcStates)
		sum := sha256.Sum256(bs)
		stateTraceHash := hex.EncodeToString(sum[:])
		t.lock.Lock()
		if _, ok := t.stateTracesMap[stateTraceHash]; !ok {
			// fmt.Printf("New state trace: %s\n", stateTraceHash)
			// 记录这条状态路径已经出现过。
			t.stateTracesMap[stateTraceHash] = true
		}
		t.lock.Unlock()
	} else {
		// TLC 连接失败表示反馈层不可用；当前实现选择直接 panic，让实验尽早失败。
		panic(fmt.Sprintf("error connecting to tlc: %s", err))
	}
	// 第二个返回值是相对新增比例：新增状态数 / 检查前已有状态数。
	return numNewStates, float64(numNewStates) / float64(max(curStates, 1))
}

// recordTrace 将一次 Check 的完整材料写成 JSON 文件。
//
// 输出文件包含三部分：
//   - trace：Fuzzer 的调度选择，即“这轮怎么调度真实系统”。
//   - event_trace：发送给 TLC 的模型事件，即“真实系统发生了哪些模型动作”。
//   - state_trace：TLC 返回的模型状态序列，即“模型如何跟着这些事件演化”。
//
// 这些文件通常用于离线调试：当某条 trace 发现新状态或触发错误时，可以查看三条轨迹之间
// 的对应关系。
func (t *TLCStateGuider) recordTrace(trace *List[*Choice], eventTrace *List[*Event], states []State) {
	// 未开启记录时直接返回，不影响覆盖统计。
	if !t.recordTraces {
		return
	}
	// 每次记录使用递增编号，避免覆盖之前的 trace 文件。
	filePath := path.Join(t.recordPath, strconv.Itoa(t.count)+".json")
	// count 先递增；即使后面写文件失败，下次也会使用新编号。
	t.count += 1
	// data 是最终 JSON 的顶层对象，包含调度、事件和模型状态三种视角。
	data := map[string]interface{}{
		"trace":       trace,
		"event_trace": eventTrace,
		// TLC 原始状态字符串可读性较差，写文件前做一层展示格式清理。
		"state_trace": parseTLCStateTrace(states),
	}
	// 使用缩进 JSON，便于人工查看和 diff。
	dataB, err := json.MarshalIndent(data, "", "\t")
	if err != nil {
		// 记录失败不应影响 fuzz 主流程，因此直接返回。
		return
	}
	// 创建或覆盖当前编号对应的记录文件。
	file, err := os.Create(filePath)
	if err != nil {
		// 文件创建失败同样不影响主流程。
		return
	}
	defer file.Close()
	// 使用 bufio.Writer 减少直接写文件的系统调用，也统一最后 Flush。
	writer := bufio.NewWriter(file)
	writer.Write(dataB)
	writer.Flush()
}

// parseTLCStateTrace 清理 TLC 返回状态的字符串表示，使落盘记录更容易阅读。
//
// TLC 的状态 Repr 通常包含换行、TLA+ 连接符或 JSON 转义后的尖括号。这个函数只改变用于
// 展示的 Repr，不改变 State.Key；因此它不会影响状态去重和覆盖统计。
func parseTLCStateTrace(states []State) []State {
	// 创建新切片，避免修改调用方传入的原始状态。
	newStates := make([]State, len(states))
	for i, s := range states {
		// 把多行状态压成单行，便于 JSON 文件里横向查看。
		repr := strings.ReplaceAll(s.Repr, "\n", ",")
		// 去掉 TLA+ 输出中常见的 conjunction 标记 "/\"。
		repr = strings.ReplaceAll(repr, "/\\", "")
		// JSON 中的 \u003e\u003e 实际表示 ">>"，这里替换成更接近 TLA+ tuple/sequence 的 "]"。
		repr = strings.ReplaceAll(repr, "\u003e\u003e", "]")
		// JSON 中的 \u003c\u003c 实际表示 "<<"，这里替换成更接近 TLA+ tuple/sequence 的 "["。
		repr = strings.ReplaceAll(repr, "\u003c\u003c", "[")
		// 剩余单个 \u003e 替换为普通尖括号，提升可读性。
		repr = strings.ReplaceAll(repr, "\u003e", ">")
		// 保留原始 Key，因为 Key 才是状态覆盖去重使用的稳定标识。
		newStates[i] = State{
			Repr: repr,
			Key:  s.Key,
		}
	}
	return newStates
}
