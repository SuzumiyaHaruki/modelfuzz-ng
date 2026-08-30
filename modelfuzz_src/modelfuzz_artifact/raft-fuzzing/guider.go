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
	"time"

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

const (
	AttributionNotRequested = "not_requested"
	AttributionInitialState = "initial_state"
	AttributionLocated      = "located"
	AttributionFailed       = "failed"

	AttributionSourceTransition = "server_transition"
	AttributionSourcePrefix     = "prefix_probe"
)

// StateAttribution 描述一个本轮新增模型状态首次出现在哪个实现事件之后。
// EventIndex 使用 0-based event trace 下标；初始状态和定位失败时为 -1。
type StateAttribution struct {
	State      State
	EventIndex int
	Origin     *EventOrigin
	Status     string
	Source     string `json:",omitempty"`
	Error      string `json:",omitempty"`
}

// Guidance 保存一次 Guider.Check 产生的细粒度模型反馈。
// 当前 Fuzzer 的 mutation energy 仍使用 Check 的原返回值，保证第一阶段不改变搜索策略。
type Guidance struct {
	NewStates []StateAttribution
	Stats     AttributionStats
}

func (g Guidance) Copy() Guidance {
	cpy := Guidance{NewStates: make([]StateAttribution, len(g.NewStates)), Stats: g.Stats}
	for i, hit := range g.NewStates {
		cpy.NewStates[i] = hit
		cpy.NewStates[i].Origin = hit.Origin.Copy()
	}
	return cpy
}

// AttributionStats 同时用作单次 Check 和累计归因统计。累计值的 Checks 大于 1。
type AttributionStats struct {
	Checks                 int
	Events                 int
	NewStates              int
	Located                int
	InitialStates          int
	Failed                 int
	MissingOrigins         int
	PrefixRequests         int
	PrefixCacheHits        int
	PrefixFallbackChecks   int
	ProvenanceChecks       int
	ProvenanceAttributions int
	TransitionRecords      int
	FullCheckDuration      time.Duration
	AttributionDuration    time.Duration
}

func (s *AttributionStats) Add(other AttributionStats) {
	s.Checks += other.Checks
	s.Events += other.Events
	s.NewStates += other.NewStates
	s.Located += other.Located
	s.InitialStates += other.InitialStates
	s.Failed += other.Failed
	s.MissingOrigins += other.MissingOrigins
	s.PrefixRequests += other.PrefixRequests
	s.PrefixCacheHits += other.PrefixCacheHits
	s.PrefixFallbackChecks += other.PrefixFallbackChecks
	s.ProvenanceChecks += other.ProvenanceChecks
	s.ProvenanceAttributions += other.ProvenanceAttributions
	s.TransitionRecords += other.TransitionRecords
	s.FullCheckDuration += other.FullCheckDuration
	s.AttributionDuration += other.AttributionDuration
}

// Guider 根据本轮 trace/eventTrace 判断是否发现了新的探索价值。
//
// Fuzzer.Run 会把 Check 返回的新增覆盖数量乘以 MutPerTrace，决定要为本轮 trace
// 生成多少条变异后续。因此 guider 不只是统计指标，也直接影响搜索方向。
// 返回值中的 int 通常表示新增覆盖数量，float64 是相对增益。
type Guider interface {
	Check(*List[*SchedulingChoice], *List[*Event]) (int, float64)
	LastGuidance() Guidance
	Coverage() CoverageStats
	Reset(string)
}

// TLCStateGuider 是 ModelFuzz 的核心 guider：把 event trace 送给 TLC，
// 用 TLA+ 模型状态覆盖率来引导下一批 trace 变异。
//
// 它同时维护三类去重集合：调度 trace、模型状态、模型状态路径。
// 真正用于奖励的主要是“新增模型状态数”，这体现了 ModelFuzz 和普通覆盖率 fuzzing 的区别。
type TLCStateGuider struct {
	TLCAddr          string
	statesMap        map[int64]bool
	tracesMap        map[string]bool
	stateTracesMap   map[string]bool
	tlcClient        *TLCClient
	recordPath       string
	recordTraces     bool
	attributeStates  bool
	count            int
	lastGuidance     Guidance
	attributionStats AttributionStats

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
		TLCAddr:          tlcAddr,
		statesMap:        make(map[int64]bool),
		tracesMap:        make(map[string]bool),
		stateTracesMap:   make(map[string]bool),
		tlcClient:        NewTLCClient(tlcAddr),
		recordPath:       recordPath,
		recordTraces:     recordTraces,
		attributeStates:  false,
		count:            0,
		lastGuidance:     Guidance{NewStates: make([]StateAttribution, 0)},
		attributionStats: AttributionStats{},
		lock:             new(sync.Mutex),
	}
}

// WithStateAttribution 控制是否为新增状态执行额外的 TLC 前缀探测。
// 默认关闭，避免 random/trace/line coverage 对照组承担额外 HTTP 开销。
func (t *TLCStateGuider) WithStateAttribution(enabled bool) *TLCStateGuider {
	t.lock.Lock()
	t.attributeStates = enabled
	t.lock.Unlock()
	return t
}

// LastGuidance 返回最近一次 Check 的结构化反馈副本。
func (t *TLCStateGuider) LastGuidance() Guidance {
	t.lock.Lock()
	defer t.lock.Unlock()
	return t.lastGuidance.Copy()
}

// AttributionStats 返回当前 guider 的累计归因质量与开销统计。
func (t *TLCStateGuider) AttributionStats() AttributionStats {
	t.lock.Lock()
	defer t.lock.Unlock()
	return t.attributionStats
}

func (t *TLCStateGuider) Reset(key string) {
	// key 目前没有用于路径区分，主要是为了和其他 guider/benchmark 接口保持一致。
	t.lock.Lock()
	t.statesMap = make(map[int64]bool)
	t.tracesMap = make(map[string]bool)
	t.stateTracesMap = make(map[string]bool)
	t.lastGuidance = Guidance{NewStates: make([]StateAttribution, 0)}
	t.attributionStats = AttributionStats{}
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
	newStates := make([]State, 0)
	fullCheckStart := time.Now()
	if execution, err := t.tlcClient.ExecuteTrace(eventTrace); err == nil {
		fullCheckDuration := time.Since(fullCheckStart)
		tlcStates := execution.States
		// TLC 返回的是执行 event trace 后经过的模型状态序列。
		// SendTrace 在独立切片上追加 Reset，因此 eventTrace 可以安全地用于后续前缀探测。
		for _, s := range tlcStates {
			t.lock.Lock()
			_, ok := t.statesMap[s.Key]
			if !ok {
				numNewStates += 1
				t.statesMap[s.Key] = true
				newStates = append(newStates, s)
			}
			t.lock.Unlock()
		}

		t.lock.Lock()
		attributeStates := t.attributeStates
		t.lock.Unlock()
		checkStats := AttributionStats{
			Checks:            1,
			Events:            eventTrace.Size(),
			NewStates:         len(newStates),
			FullCheckDuration: fullCheckDuration,
		}
		if execution.ProvenanceAvailable {
			checkStats.ProvenanceChecks = 1
			checkStats.TransitionRecords = len(execution.Transitions)
		}
		guidance := Guidance{NewStates: make([]StateAttribution, 0, len(newStates))}
		if attributeStates && len(newStates) > 0 {
			attributionStart := time.Now()
			if execution.ProvenanceAvailable {
				checkStats.ProvenanceAttributions = len(newStates)
				for _, state := range newStates {
					guidance.NewStates = append(guidance.NewStates,
						locateStateFromTransitions(state, execution, eventTrace))
				}
			} else {
				checkStats.PrefixFallbackChecks = 1
				probe := newPrefixStateProbe(t.tlcClient, eventTrace, tlcStates)
				for _, state := range newStates {
					guidance.NewStates = append(guidance.NewStates, probe.locate(state))
				}
				checkStats.PrefixRequests = probe.requests
				checkStats.PrefixCacheHits = probe.cacheHits
			}
			checkStats.AttributionDuration = time.Since(attributionStart)
		} else {
			for _, state := range newStates {
				guidance.NewStates = append(guidance.NewStates, StateAttribution{
					State:      state,
					EventIndex: -1,
					Status:     AttributionNotRequested,
				})
			}
		}
		for _, hit := range guidance.NewStates {
			switch hit.Status {
			case AttributionLocated:
				checkStats.Located++
				if hit.Origin == nil {
					checkStats.MissingOrigins++
				}
			case AttributionInitialState:
				checkStats.InitialStates++
			case AttributionFailed:
				checkStats.Failed++
			}
		}
		guidance.Stats = checkStats
		t.lock.Lock()
		t.lastGuidance = guidance.Copy()
		t.attributionStats.Add(checkStats)
		t.lock.Unlock()
		t.recordTrace(trace, eventTrace, execution, guidance)

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

// locateStateFromTransitions uses the modified TLC server's one-record-per-input-event
// response.  It does not assume that membership in an abstracted state sequence is
// monotonic across separately replayed prefixes.
func locateStateFromTransitions(state State, execution TLCExecution, trace *List[*Event]) StateAttribution {
	hit := StateAttribution{
		State:      state,
		EventIndex: -1,
		Status:     AttributionFailed,
		Source:     AttributionSourceTransition,
	}
	if len(execution.States) > 0 && execution.States[0].Key == state.Key {
		hit.Status = AttributionInitialState
		return hit
	}
	for _, transition := range execution.Transitions {
		if transition.PostKey != state.Key {
			continue
		}
		if transition.EventIndex < 0 || transition.EventIndex >= trace.Size() {
			hit.Error = fmt.Sprintf("TLC transition event index %d is outside trace of size %d",
				transition.EventIndex, trace.Size())
			return hit
		}
		event, ok := trace.Get(transition.EventIndex)
		if !ok {
			hit.Error = fmt.Sprintf("event %d is absent from local trace", transition.EventIndex)
			return hit
		}
		if transition.InputName != "" && transition.InputName != event.Name {
			hit.Error = fmt.Sprintf("TLC transition event %d is %q, local event is %q",
				transition.EventIndex, transition.InputName, event.Name)
			return hit
		}
		hit.EventIndex = transition.EventIndex
		hit.Origin = event.Origin.Copy()
		hit.Status = AttributionLocated
		return hit
	}
	hit.Error = fmt.Sprintf("state key %d is absent from TLC transition post-states", state.Key)
	return hit
}

type prefixProbeResult struct {
	keys map[int64]bool
	err  error
}

// prefixStateProbe 通过查询 event trace 前缀，定位某个状态第一次出现的事件边界。
// 完整 trace 的 TLC 结果会作为 cache 初始项复用，不重复发送。
type prefixStateProbe struct {
	client    *TLCClient
	trace     *List[*Event]
	cache     map[int]prefixProbeResult
	requests  int
	cacheHits int
}

func newPrefixStateProbe(client *TLCClient, trace *List[*Event], fullStates []State) *prefixStateProbe {
	fullKeys := make(map[int64]bool, len(fullStates))
	for _, state := range fullStates {
		fullKeys[state.Key] = true
	}
	return &prefixStateProbe{
		client: client,
		trace:  trace,
		cache: map[int]prefixProbeResult{
			trace.Size(): {keys: fullKeys},
		},
	}
}

func (p *prefixStateProbe) contains(prefixLength int, key int64) (bool, error) {
	if prefixLength < 0 || prefixLength > p.trace.Size() {
		return false, fmt.Errorf("invalid event prefix length %d", prefixLength)
	}
	result, ok := p.cache[prefixLength]
	if !ok {
		p.requests++
		prefix := NewList[*Event]()
		for i := 0; i < prefixLength; i++ {
			event, _ := p.trace.Get(i)
			prefix.Append(event)
		}
		states, err := p.client.SendTrace(prefix)
		result = prefixProbeResult{keys: make(map[int64]bool), err: err}
		for _, state := range states {
			result.keys[state.Key] = true
		}
		p.cache[prefixLength] = result
	} else {
		p.cacheHits++
	}
	if result.err != nil {
		return false, result.err
	}
	return result.keys[key], nil
}

func (p *prefixStateProbe) locate(state State) StateAttribution {
	hit := StateAttribution{
		State: state, EventIndex: -1, Status: AttributionFailed, Source: AttributionSourcePrefix,
	}
	inInitial, err := p.contains(0, state.Key)
	if err != nil {
		hit.Error = err.Error()
		return hit
	}
	if inInitial {
		hit.Status = AttributionInitialState
		return hit
	}
	if p.trace.Size() == 0 {
		hit.Error = "state is absent from the empty event trace"
		return hit
	}

	low, high := 1, p.trace.Size()
	for low < high {
		mid := low + (high-low)/2
		contains, probeErr := p.contains(mid, state.Key)
		if probeErr != nil {
			hit.Error = probeErr.Error()
			return hit
		}
		if contains {
			high = mid
		} else {
			low = mid + 1
		}
	}

	atBoundary, err := p.contains(low, state.Key)
	if err != nil {
		hit.Error = err.Error()
		return hit
	}
	beforeBoundary, err := p.contains(low-1, state.Key)
	if err != nil {
		hit.Error = err.Error()
		return hit
	}
	if !atBoundary || beforeBoundary {
		hit.Error = "TLC prefix results are not monotonic for this state"
		return hit
	}

	hit.EventIndex = low - 1
	hit.Status = AttributionLocated
	if event, ok := p.trace.Get(hit.EventIndex); ok {
		hit.Origin = event.Origin.Copy()
	}
	return hit
}

func (t *TLCStateGuider) recordTrace(trace *List[*SchedulingChoice], eventTrace *List[*Event], execution TLCExecution, guidance Guidance) {
	// 开启 recordTraces 后，每条被检查的 trace 都会落盘，方便之后重放和对照 TLC 状态路径。
	if !t.recordTraces {
		return
	}
	filePath := path.Join(t.recordPath, strconv.Itoa(t.count)+".json")
	t.count += 1
	data := map[string]interface{}{
		"trace":                    trace,
		"event_trace":              eventTrace,
		"event_origins":            eventOrigins(eventTrace),
		"state_trace":              parseTLCStateTrace(execution.States),
		"new_state_attributions":   guidance.NewStates,
		"tlc_provenance_available": execution.ProvenanceAvailable,
	}
	if execution.ProvenanceAvailable {
		data["tlc_transitions"] = execution.Transitions
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

func eventOrigins(events *List[*Event]) []*EventOrigin {
	origins := make([]*EventOrigin, events.Size())
	for i, event := range events.Iter() {
		origins[i] = event.Origin.Copy()
	}
	return origins
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
