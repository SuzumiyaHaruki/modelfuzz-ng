package main

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

	"github.com/zeu5/gocov"
)

// CoverageStats 是每种 guider 对外汇报的覆盖率摘要。
// 三个数字看的是不同层面：
//   - UniqueStates：TLC 模型中覆盖到的唯一状态数；
//   - UniqueTraces：调度 trace 或 event trace 结构的唯一数量；
//   - UniqueStateTraces：TLC 返回的状态路径的唯一数量。
type CoverageStats struct {
	UniqueStates      int
	UniqueTraces      int
	UniqueStateTraces int
}

// Guider 根据本轮 trace/eventTrace 判断是否发现了新的探索价值。
//
// Fuzzer.Run 会把 Check 返回的新增覆盖数量乘以 MutPerTrace，决定要为本轮 trace
// 生成多少条变异后续。因此 guider 不只是统计指标，也直接影响搜索方向。
// 返回值中的 int 通常表示新增覆盖数量，float64 是相对增益。
type Guider interface {
	Check(*List[*SchedulingChoice], *List[*Event]) (int, float64)
	Coverage() CoverageStats
	Reset(string)
}

// TLCStateGuider 是 ModelFuzz 的核心 guider：把 event trace 送给 TLC，
// 用 TLA+ 模型状态覆盖率来引导下一批 trace 变异。
//
// 它同时维护三类去重集合：调度 trace、模型状态、模型状态路径。
// 真正用于奖励的主要是“新增模型状态数”，这体现了 ModelFuzz 和普通覆盖率 fuzzing 的区别。
type TLCStateGuider struct {
	TLCAddr        string
	statesMap      map[int64]bool
	tracesMap      map[string]bool
	stateTracesMap map[string]bool
	tlcClient      *TLCClient
	recordPath     string
	recordTraces   bool
	count          int

	lock *sync.Mutex
}

var _ Guider = &TLCStateGuider{}

func NewTLCStateGuider(tlcAddr, recordPath string, recordTraces bool) *TLCStateGuider {
	// recordPath 非空时会清空旧目录。compare 中不同 guider 共用 "traces" 参数时要注意这个副作用。
	if recordPath != "" {
		if _, err := os.Stat(recordPath); err == nil {
			os.RemoveAll(recordPath)
		}
		os.Mkdir(recordPath, 0777)
	}
	return &TLCStateGuider{
		TLCAddr:        tlcAddr,
		statesMap:      make(map[int64]bool),
		tracesMap:      make(map[string]bool),
		stateTracesMap: make(map[string]bool),
		tlcClient:      NewTLCClient(tlcAddr),
		recordPath:     recordPath,
		recordTraces:   recordTraces,
		count:          0,
		lock:           new(sync.Mutex),
	}
}

func (t *TLCStateGuider) Reset(key string) {
	// key 目前没有用于路径区分，主要是为了和其他 guider/benchmark 接口保持一致。
	t.lock.Lock()
	t.statesMap = make(map[int64]bool)
	t.tracesMap = make(map[string]bool)
	t.stateTracesMap = make(map[string]bool)
	t.lock.Unlock()
}

func (t *TLCStateGuider) Coverage() CoverageStats {
	t.lock.Lock()
	defer t.lock.Unlock()
	return CoverageStats{
		UniqueStates:      len(t.statesMap),
		UniqueTraces:      len(t.tracesMap),
		UniqueStateTraces: len(t.stateTracesMap),
	}
}

func (t *TLCStateGuider) Check(trace *List[*SchedulingChoice], eventTrace *List[*Event]) (int, float64) {
	// trace hash 用来统计调度选择本身的去重情况。
	// 即使两个调度 trace 不同，它们也可能在模型里走到相同状态；TLC 状态覆盖负责区分这一点。
	bs, _ := json.Marshal(trace)
	sum := sha256.Sum256(bs)
	hash := hex.EncodeToString(sum[:])
	t.lock.Lock()
	if _, ok := t.tracesMap[hash]; !ok {
		// fmt.Printf("New trace: %s\n", hash)
		t.tracesMap[hash] = true
	}
	t.lock.Unlock()

	t.lock.Lock()
	curStates := len(t.statesMap)
	t.lock.Unlock()
	numNewStates := 0
	if tlcStates, err := t.tlcClient.SendTrace(eventTrace); err == nil {
		// TLC 返回的是执行 event trace 后经过的模型状态序列。
		// SendTrace 会在 eventTrace 末尾追加 Reset 事件，因此同一个 eventTrace 不应被重复发送后再复用。
		t.recordTrace(trace, eventTrace, tlcStates)
		for _, s := range tlcStates {
			t.lock.Lock()
			_, ok := t.statesMap[s.Key]
			if !ok {
				numNewStates += 1
				t.statesMap[s.Key] = true
			}
			t.lock.Unlock()
		}
		bs, _ := json.Marshal(tlcStates)
		// state trace hash 用来区分“状态路径”是否新，即使最终覆盖状态数相同也可能路径不同。
		sum := sha256.Sum256(bs)
		stateTraceHash := hex.EncodeToString(sum[:])
		t.lock.Lock()
		if _, ok := t.stateTracesMap[stateTraceHash]; !ok {
			// fmt.Printf("New state trace: %s\n", stateTraceHash)
			t.stateTracesMap[stateTraceHash] = true
		}
		t.lock.Unlock()
	} else {
		panic(fmt.Sprintf("error connecting to tlc: %s", err))
	}
	return numNewStates, float64(numNewStates) / float64(max(curStates, 1))
}

func (t *TLCStateGuider) recordTrace(trace *List[*SchedulingChoice], eventTrace *List[*Event], states []State) {
	// 开启 recordTraces 后，每条被检查的 trace 都会落盘，方便之后重放和对照 TLC 状态路径。
	if !t.recordTraces {
		return
	}
	filePath := path.Join(t.recordPath, strconv.Itoa(t.count)+".json")
	t.count += 1
	data := map[string]interface{}{
		"trace":       trace,
		"event_trace": eventTrace,
		"state_trace": parseTLCStateTrace(states),
	}
	dataB, err := json.MarshalIndent(data, "", "\t")
	if err != nil {
		return
	}
	file, err := os.Create(filePath)
	if err != nil {
		return
	}
	defer file.Close()
	writer := bufio.NewWriter(file)
	writer.Write(dataB)
	writer.Flush()
}

func parseTLCStateTrace(states []State) []State {
	// TLC 状态字符串里有一些转义/换行，这里做轻量清洗，方便人读 JSON 结果。
	newStates := make([]State, len(states))
	for i, s := range states {
		repr := strings.ReplaceAll(s.Repr, "\n", ",")
		repr = strings.ReplaceAll(repr, "/\\", "")
		repr = strings.ReplaceAll(repr, "\u003e\u003e", "]")
		repr = strings.ReplaceAll(repr, "\u003c\u003c", "[")
		repr = strings.ReplaceAll(repr, "\u003e", ">")
		newStates[i] = State{
			Repr: repr,
			Key:  s.Key,
		}
	}
	return newStates
}

type TraceCoverageGuider struct {
	traces map[string]bool
	*TLCStateGuider
}

var _ Guider = &TraceCoverageGuider{}

func NewTraceCoverageGuider(tlcAddr, recordPath string, recordTraces bool) *TraceCoverageGuider {
	return &TraceCoverageGuider{
		traces:         make(map[string]bool),
		TLCStateGuider: NewTLCStateGuider(tlcAddr, recordPath, recordTraces),
	}
}

func (t *TraceCoverageGuider) Check(trace *List[*SchedulingChoice], events *List[*Event]) (int, float64) {
	// 仍然调用 TLCStateGuider，以便保留 TLC 状态统计；但是否奖励由 event trace 结构决定。
	// 换句话说，traceCov 优先探索“实现事件结构的新形状”，不直接以模型新状态作为奖励。
	t.TLCStateGuider.Check(trace, events)

	eTrace := newEventTrace(events)
	key := eTrace.Hash()

	new := 0
	t.lock.Lock()
	defer t.lock.Unlock()
	if _, ok := t.traces[key]; !ok {
		t.traces[key] = true
		new = 1
	}

	return new, float64(new) / float64(len(t.traces))
}

func (t *TraceCoverageGuider) Coverage() CoverageStats {
	c := t.TLCStateGuider.Coverage()
	t.lock.Lock()
	c.UniqueTraces = len(t.traces)
	t.lock.Unlock()
	return c
}

func (t *TraceCoverageGuider) Reset(key string) {
	t.lock.Lock()
	t.traces = make(map[string]bool)
	t.lock.Unlock()
	t.TLCStateGuider.Reset(key)
}

type eventTrace struct {
	Nodes map[string]*eventNode
}

func (e *eventTrace) Hash() string {
	bs, err := json.Marshal(e)
	if err != nil {
		return ""
	}
	hash := sha256.Sum256(bs)
	return hex.EncodeToString(hash[:])
}

type eventNode struct {
	*Event
	Node uint64
	// Prev 连接同一节点上的上一个事件，用于保留每个节点的局部事件顺序。
	Prev string
	ID   string `json:"-"`
}

func (e *eventNode) Hash() string {
	bs, err := json.Marshal(e)
	if err != nil {
		return ""
	}
	hash := sha256.Sum256(bs)
	return hex.EncodeToString(hash[:])
}

func newEventTrace(events *List[*Event]) *eventTrace {
	// 把线性的 event trace 转成按节点串起来的因果图，再对图做 hash。
	// 这样不同节点间无关事件的线性顺序变化，不一定会被当成完全不同的结构。
	eTrace := &eventTrace{
		Nodes: make(map[string]*eventNode),
	}
	curEvent := make(map[uint64]*eventNode)

	for _, e := range events.Iter() {
		node := &eventNode{
			Event: e,
			Node:  e.Node,
			Prev:  "",
		}
		prev, ok := curEvent[e.Node]
		if ok {
			node.Prev = prev.ID
		}
		node.ID = node.Hash()
		curEvent[e.Node] = node
		eTrace.Nodes[node.ID] = node
	}
	return eTrace
}

type LineCoverageGuider struct {
	covData *gocov.Coverage
	*TLCStateGuider
}

func NewLineCoverageGuider(tlcAddr, recordPath string, recordTraces bool) *LineCoverageGuider {
	return &LineCoverageGuider{
		covData:        nil,
		TLCStateGuider: NewTLCStateGuider(tlcAddr, recordPath, recordTraces),
	}
}

var _ Guider = &LineCoverageGuider{}

func (l *LineCoverageGuider) Check(trace *List[*SchedulingChoice], events *List[*Event]) (int, float64) {
	// 和 RedisRaft 版 gcov 不同，这里直接通过 gocov 读取 Go 包覆盖率。
	// 它仍调用 TLCStateGuider.Check，所以 Coverage() 里还能看到 TLC 状态统计；
	// 但本次是否产生 mutation 奖励，取决于新增 Go 源码行覆盖。
	l.TLCStateGuider.Check(trace, events)
	cov, err := gocov.GetCoverage(gocov.CoverageConfig{
		MatchPkgs: []string{"github.com/zeu5/raft-fuzzing/raft"},
	})
	if err != nil {
		fmt.Println("Error reading coverage data: " + err.Error())
		return 0, 0
	}
	l.lock.Lock()
	defer l.lock.Unlock()
	if l.covData == nil {
		l.covData = cov
		return cov.GetCoveredLines(), 1
	}
	curLines := l.covData.GetCoveredLines()
	l.covData.Data.Merge(cov.Data)
	updatedLines := l.covData.GetCoveredLines()
	newLines := updatedLines - curLines
	return newLines, float64(newLines) / float64(max(curLines, 1))
}

func (l *LineCoverageGuider) Reset(key string) {
	l.lock.Lock()
	fmt.Printf("Percentage of lines covered: %f\n", l.covData.GetPercent())
	l.covData.Reset()
	l.covData = nil
	l.lock.Unlock()
	l.TLCStateGuider.Reset(key)
}
