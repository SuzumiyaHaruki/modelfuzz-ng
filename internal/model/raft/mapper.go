// Package raft 将 etcd-raft 的 Concrete Transition 映射为 Raft TLA+ 模型事件。
package raft

import (
	"errors"
	"fmt"
	"math"
	"strconv"

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/core"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/model"
)

const deliveredMessageEvent = "raft.message_delivered"
const proposalDroppedEvent = "raft.proposal_dropped"
const voteQuorumFaultEvent = "raft.vote_quorum_fault_activated"

const (
	ProfileBasic           = "basic"
	ProfileStorageSnapshot = "storage-snapshot"
)

var ErrUnsupportedSemantics = errors.New("transition is not represented by the raft model")

// Config 与 models/raft/raft.cfg 的有界常量对应。Engine 创建 Adapter 和
// Mapper 时应从同一份运行配置填充这些值，避免模型和 SUT 静默漂移。
type Config struct {
	NodeIDs        []core.NodeID `json:"node_ids"`
	MaxValue       int           `json:"max_value"`
	MaxLogIndex    uint64        `json:"max_log_index"`
	LargestTerm    uint64        `json:"largest_term"`
	EmitLeaderNoOp bool          `json:"emit_leader_no_op"`
	// Profile 为空时保持历史 basic 行为与旧 checkpoint JSON 指纹兼容。
	// storage-snapshot 启用 Storage 边界和 Leader snapshot progress 事件。
	Profile string `json:"profile,omitempty"`
}

func DefaultConfig() Config {
	return Config{
		NodeIDs:        []core.NodeID{1, 2, 3},
		MaxValue:       5,
		MaxLogIndex:    5,
		LargestTerm:    5,
		EmitLeaderNoOp: true,
	}
}

func (c Config) normalized() (Config, error) {
	defaults := DefaultConfig()
	if len(c.NodeIDs) == 0 {
		c.NodeIDs = defaults.NodeIDs
	}
	if c.MaxValue == 0 {
		c.MaxValue = defaults.MaxValue
	}
	if c.MaxLogIndex == 0 {
		c.MaxLogIndex = defaults.MaxLogIndex
	}
	if c.LargestTerm == 0 {
		c.LargestTerm = defaults.LargestTerm
	}
	c.NodeIDs = append([]core.NodeID(nil), c.NodeIDs...)
	seen := make(map[core.NodeID]struct{}, len(c.NodeIDs))
	for _, id := range c.NodeIDs {
		if !id.Valid() {
			return Config{}, fmt.Errorf("raft model node ID must be non-zero")
		}
		if _, exists := seen[id]; exists {
			return Config{}, fmt.Errorf("raft model contains duplicate node %s", id)
		}
		seen[id] = struct{}{}
	}
	if c.MaxValue < 1 || c.MaxLogIndex < 1 || c.LargestTerm < 1 {
		return Config{}, fmt.Errorf("raft model bounds must be positive")
	}
	if c.Profile != "" && c.Profile != ProfileBasic && c.Profile != ProfileStorageSnapshot {
		return Config{}, fmt.Errorf("unknown raft model profile %q", c.Profile)
	}
	return c, nil
}

// EffectiveProfile 返回对外使用的稳定 profile 名。空值是历史 basic 配置的别名。
func (c Config) EffectiveProfile() string {
	if c.Profile == "" {
		return ProfileBasic
	}
	return c.Profile
}

// Mapper 当前对齐 models/raft/raft.tla 以及原 ModelFuzz 的 RaftActionMapper。
type Mapper struct {
	config Config
	nodes  map[core.NodeID]struct{}
}

func NewMapper() *Mapper {
	mapper, _ := NewMapperWithConfig(DefaultConfig())
	return mapper
}

func NewMapperWithConfig(config Config) (*Mapper, error) {
	normalized, err := config.normalized()
	if err != nil {
		return nil, err
	}
	nodes := make(map[core.NodeID]struct{}, len(normalized.NodeIDs))
	for _, id := range normalized.NodeIDs {
		nodes[id] = struct{}{}
	}
	return &Mapper{config: normalized, nodes: nodes}, nil
}

// Map 将一条 Concrete Transition 映射为零到多条 Raft TLA+ 模型事件。
func (m *Mapper) Map(transition model.Transition) ([]model.Event, error) {
	if m == nil {
		return nil, fmt.Errorf("raft mapper must not be nil")
	}
	if err := transition.Validate(); err != nil {
		return nil, err
	}
	if err := m.validateObservedBounds(transition.Before); err != nil {
		return nil, err
	}
	if err := m.validateObservedBounds(transition.After); err != nil {
		return nil, err
	}

	events := make([]model.Event, 0)
	action := transition.Record.Action
	switch action.Kind {
	case core.ActionRequest:
		request, err := numericRequest(action.Request, m.config.MaxValue)
		if err != nil {
			return nil, err
		}
		if roleOf(transition.Before, action.Node) == "leader" {
			if lastIndexOf(transition.Before, action.Node) >= m.config.MaxLogIndex {
				return nil, fmt.Errorf("%w: client request exceeds MaxLogIndex %d", ErrUnsupportedSemantics, m.config.MaxLogIndex)
			}
			events = append(events, model.NewEvent("ClientRequest", map[string]any{
				"request": request,
				"leader":  uint64(action.Node),
			}))
		}
	case core.ActionCrash:
		if statusOf(transition.Before, action.Node) != core.NodeRunning ||
			statusOf(transition.After, action.Node) != core.NodeCrashed {
			return nil, fmt.Errorf("%w: crash node %s has inconsistent before/after status", ErrUnsupportedSemantics, action.Node)
		}
		// 保持与原 controlled TLC RaftActionMapper 的事件协议兼容：Remove
		// 会选择 TLA+ 中对应节点的 RemoveFromActive 动作。
		events = append(events, model.NewEvent("Remove", map[string]any{"i": uint64(action.Node)}))
	case core.ActionRestart:
		if statusOf(transition.Before, action.Node) != core.NodeCrashed ||
			statusOf(transition.After, action.Node) != core.NodeRunning {
			return nil, fmt.Errorf("%w: restart node %s has inconsistent before/after status", ErrUnsupportedSemantics, action.Node)
		}
		// AddToActive 同时恢复活动成员并重置该节点的 Raft 易失状态。
		events = append(events, model.NewEvent("Add", map[string]any{"i": uint64(action.Node)}))
	case core.ActionPartition, core.ActionHeal:
		// Runtime 拓扑变化不直接修改轻量 Raft 模型变量。
	}

	commitNodes := make([]core.NodeID, 0)
	seenCommit := make(map[core.NodeID]struct{})
	storageSnapshot := m.config.EffectiveProfile() == ProfileStorageSnapshot
	storageEvents := make([]model.Event, 0)
	for _, effect := range transition.Record.Effects {
		switch effect.Kind {
		case core.EffectTimerFired:
			if effect.TimerFired.TypeHint == "election" {
				beforeTerm, afterTerm, err := electionTimerTerms(effect.TimerFired, transition)
				if err != nil {
					return nil, err
				}
				// 对 Leader 调用 Campaign 时 etcd-raft 会保持状态不变。Adapter
				// 仍记录这次强制 timer 尝试，但模型必须明确 stutter，不能生成
				// 在当前 TLA+ 状态下 disabled 的 Timeout。
				if afterTerm == beforeTerm {
					continue
				}
				if beforeTerm >= m.config.LargestTerm {
					return nil, fmt.Errorf("%w: timeout exceeds LargestTerm %d", ErrUnsupportedSemantics, m.config.LargestTerm)
				}
				if afterTerm != beforeTerm+1 {
					return nil, fmt.Errorf("%w: election timer changed term from %d to %d", ErrUnsupportedSemantics, beforeTerm, afterTerm)
				}
				events = append(events, model.NewEvent("Timeout", map[string]any{
					"node": uint64(effect.TimerFired.Node),
				}))
			}
		case core.EffectModelEvent:
			event := effect.ModelEvent
			switch event.Name {
			case deliveredMessageEvent:
				mapped, err := m.mapDeliveredMessage(event.Params, transition.Record.Effects, transition.Before)
				if err != nil {
					return nil, err
				}
				events = append(events, mapped...)
			case proposalDroppedEvent, voteQuorumFaultEvent:
				// Candidate、无已知 leader 的 follower 或关闭转发时，etcd-raft
				// 明确丢弃 proposal；fault activation 只是 SUT 插桩记录。
				// 两者都不直接改变轻量模型状态；异常的 BecomeLeader 会由
				// 后续正确 quorum 前置条件拒绝。
				continue
			case "raft.snapshot_created":
				if !storageSnapshot {
					continue
				}
				index, term, err := snapshotBoundaryParams(event)
				if err != nil {
					return nil, err
				}
				if index > m.config.MaxLogIndex || term > m.config.LargestTerm {
					return nil, fmt.Errorf("%w: snapshot boundary index=%d term=%d exceeds model bounds", ErrUnsupportedSemantics, index, term)
				}
				storageEvents = append(storageEvents, model.NewEvent("CreateSnapshot", map[string]any{
					"i": uint64(event.Node), "index": index, "term": term,
				}))
			case "raft.log_compacted":
				if !storageSnapshot {
					continue
				}
				index, ok := unsignedParam(event.Params["compact_index"])
				if !ok || index == 0 || index > m.config.MaxLogIndex {
					return nil, fmt.Errorf("%w: compact event has invalid compact_index", ErrUnsupportedSemantics)
				}
				storageEvents = append(storageEvents, model.NewEvent("CompactLog", map[string]any{
					"i": uint64(event.Node), "index": index,
				}))
			case "raft.snapshot_sent":
				if !storageSnapshot {
					continue
				}
				params, err := m.snapshotSendParams(event)
				if err != nil {
					return nil, err
				}
				storageEvents = append(storageEvents, model.NewEvent("SendSnapshot", params))
			case "raft.snapshot_status_reported":
				if !storageSnapshot {
					continue
				}
				params, handled, err := m.snapshotStatusParams(event)
				if err != nil {
					return nil, err
				}
				if handled {
					storageEvents = append(storageEvents, model.NewEvent("HandleSnapshotStatus", params))
				}
			case "raft.snapshot_delivered", "raft.snapshot_applied", "raft.snapshot_fast_forwarded",
				"raft.snapshot_rejected_or_stale":
				// 第三阶段在对应的 MsgSnap delivery 上原子映射 follower outcome。
				// 独立 lifecycle marker 只作为观测证据，stutter 可避免重复执行。
				continue
			case "raft.config_changed":
				return nil, fmt.Errorf("%w: model event %s", ErrUnsupportedSemantics, event.Name)
			case "raft.entry_committed":
				if roleOf(transition.After, event.Node) == "leader" {
					if _, exists := seenCommit[event.Node]; !exists {
						seenCommit[event.Node] = struct{}{}
						commitNodes = append(commitNodes, event.Node)
					}
				}
				if storageSnapshot {
					index, ok := unsignedParam(event.Params["index"])
					if !ok || index == 0 || index > m.config.MaxLogIndex {
						return nil, fmt.Errorf("%w: committed entry has invalid index", ErrUnsupportedSemantics)
					}
					storageEvents = append(storageEvents, model.NewEvent("ApplyCommitted", map[string]any{
						"i": uint64(event.Node), "index": index,
					}))
				}
			}
		}
	}

	for _, node := range becameLeaders(transition.Before, transition.After) {
		events = append(events, model.NewEvent("BecomeLeader", map[string]any{"node": uint64(node)}))
		if m.config.EmitLeaderNoOp {
			if lastIndexOf(transition.Before, node) >= m.config.MaxLogIndex {
				return nil, fmt.Errorf("%w: leader no-op exceeds MaxLogIndex %d", ErrUnsupportedSemantics, m.config.MaxLogIndex)
			}
			events = append(events, model.NewEvent("ClientRequest", map[string]any{
				"request": 0,
				"leader":  uint64(node),
			}))
		}
	}
	// Adapter 的 Ready 可能在同一 Concrete Action 中依次成为 Leader、追加 no-op、
	// 推进 commit、应用 entry 并创建 snapshot。模型必须先完成角色/日志/commit
	// 转换，再重放应用与 Storage 边界，不能照 Effect 收集时机提前执行。
	for _, node := range commitNodes {
		events = append(events, model.NewEvent("AdvanceCommitIndex", map[string]any{"i": uint64(node)}))
	}
	if storageSnapshot {
		events = append(events, storageEvents...)
	}
	return events, nil
}

func snapshotBoundaryParams(event *core.ModelEvent) (uint64, uint64, error) {
	if event == nil || !event.Node.Valid() {
		return 0, 0, fmt.Errorf("%w: snapshot event has invalid node", ErrUnsupportedSemantics)
	}
	index, indexOK := unsignedParam(event.Params["index"])
	term, termOK := unsignedParam(event.Params["term"])
	if !indexOK || index == 0 || !termOK {
		return 0, 0, fmt.Errorf("%w: snapshot event has invalid index or term", ErrUnsupportedSemantics)
	}
	return index, term, nil
}

func (m *Mapper) snapshotSendParams(event *core.ModelEvent) (map[string]any, error) {
	index, term, err := snapshotBoundaryParams(event)
	if err != nil {
		return nil, err
	}
	to, toOK := unsignedParam(event.Params["to"])
	match, matchOK := unsignedParam(event.Params["match_index"])
	next, nextOK := unsignedParam(event.Params["next_index"])
	pending, pendingOK := unsignedParam(event.Params["pending_snapshot"])
	state, stateOK := event.Params["progress_state"].(string)
	if !toOK || !matchOK || !nextOK || !pendingOK || !stateOK {
		return nil, fmt.Errorf("%w: snapshot send has invalid progress metadata", ErrUnsupportedSemantics)
	}
	if _, exists := m.nodes[event.Node]; !exists {
		return nil, fmt.Errorf("%w: snapshot sender %s is outside model Server", ErrUnsupportedSemantics, event.Node)
	}
	if _, exists := m.nodes[core.NodeID(to)]; !exists || to == uint64(event.Node) {
		return nil, fmt.Errorf("%w: snapshot target %d is invalid", ErrUnsupportedSemantics, to)
	}
	if index > m.config.MaxLogIndex || term > m.config.LargestTerm ||
		match > m.config.MaxLogIndex || next == 0 || next > m.config.MaxLogIndex+1 ||
		pending > m.config.MaxLogIndex {
		return nil, fmt.Errorf("%w: snapshot send exceeds model bounds", ErrUnsupportedSemantics)
	}
	if state != "StateSnapshot" || pending != index || next != pending+1 {
		return nil, fmt.Errorf(
			"%w: snapshot send progress state=%q index=%d pending=%d next=%d is inconsistent",
			ErrUnsupportedSemantics, state, index, pending, next,
		)
	}
	return map[string]any{
		"i":       uint64(event.Node),
		"j":       to,
		"index":   index,
		"term":    term,
		"match":   match,
		"next":    next,
		"pending": pending,
	}, nil
}

func (m *Mapper) snapshotStatusParams(event *core.ModelEvent) (map[string]any, bool, error) {
	if event == nil || !event.Node.Valid() {
		return nil, false, fmt.Errorf("%w: snapshot status has invalid reporting node", ErrUnsupportedSemantics)
	}
	from, fromOK := unsignedParam(event.Params["from"])
	to, toOK := unsignedParam(event.Params["to"])
	reject, rejectOK := event.Params["reject"].(bool)
	handled, handledOK := event.Params["handled"].(bool)
	pendingBefore, pendingBeforeOK := unsignedParam(event.Params["pending_before"])
	pendingAfter, pendingAfterOK := unsignedParam(event.Params["pending_after"])
	match, matchOK := unsignedParam(event.Params["match_index"])
	nextAfter, nextAfterOK := unsignedParam(event.Params["next_after"])
	if !fromOK || !toOK || !rejectOK || !handledOK || !pendingBeforeOK ||
		!pendingAfterOK || !matchOK || !nextAfterOK {
		return nil, false, fmt.Errorf("%w: snapshot status has invalid progress metadata", ErrUnsupportedSemantics)
	}
	if to != uint64(event.Node) || from == to {
		return nil, false, fmt.Errorf("%w: snapshot status endpoints do not match reporter", ErrUnsupportedSemantics)
	}
	if _, exists := m.nodes[core.NodeID(from)]; !exists {
		return nil, false, fmt.Errorf("%w: snapshot status follower n%d is outside model Server", ErrUnsupportedSemantics, from)
	}
	if _, exists := m.nodes[core.NodeID(to)]; !exists {
		return nil, false, fmt.Errorf("%w: snapshot status leader n%d is outside model Server", ErrUnsupportedSemantics, to)
	}
	if !handled {
		return nil, false, nil
	}
	if pendingBefore == 0 || pendingBefore > m.config.MaxLogIndex || pendingAfter != 0 ||
		match > m.config.MaxLogIndex || nextAfter == 0 || nextAfter > m.config.MaxLogIndex+1 {
		return nil, false, fmt.Errorf("%w: handled snapshot status has inconsistent bounds", ErrUnsupportedSemantics)
	}
	expectedNext := match + 1
	if !reject && pendingBefore+1 > expectedNext {
		expectedNext = pendingBefore + 1
	}
	if nextAfter != expectedNext {
		return nil, false, fmt.Errorf(
			"%w: snapshot status reject=%v pending=%d match=%d produced next=%d, want %d",
			ErrUnsupportedSemantics, reject, pendingBefore, match, nextAfter, expectedNext,
		)
	}
	return map[string]any{
		"i": to, "j": from, "success": !reject, "next": nextAfter,
	}, true, nil
}

// electionTimerTerms 优先读取 Effect 记录的单次 timeout 边界。一次 AdvanceTime
// 可以包含同一节点的多次自然超时，此时 Transition 的总 before/after term 无法表示
// 每一次跳变。旧 Trace 没有该 metadata 时，退回到全局 Observation 以保持兼容。
func electionTimerTerms(fired *core.TimerFired, transition model.Transition) (uint64, uint64, error) {
	beforeText, hasBefore := fired.Metadata["term_before"]
	afterText, hasAfter := fired.Metadata["term_after"]
	if !hasBefore && !hasAfter {
		return termOf(transition.Before, fired.Node), termOf(transition.After, fired.Node), nil
	}
	if !hasBefore || !hasAfter {
		return 0, 0, fmt.Errorf("%w: election timer metadata must contain term_before and term_after", ErrUnsupportedSemantics)
	}
	before, beforeErr := strconv.ParseUint(beforeText, 10, 64)
	after, afterErr := strconv.ParseUint(afterText, 10, 64)
	if beforeErr != nil || afterErr != nil {
		return 0, 0, fmt.Errorf(
			"%w: election timer has invalid term metadata %q -> %q",
			ErrUnsupportedSemantics, beforeText, afterText,
		)
	}
	return before, after, nil
}

func (m *Mapper) mapDeliveredMessage(params map[string]any, effects []core.Effect, before core.Observation) ([]model.Event, error) {
	messageType, ok := params["type"].(string)
	if !ok || messageType == "" {
		return nil, fmt.Errorf("%w: delivered message has no type", ErrUnsupportedSemantics)
	}

	switch messageType {
	case "MsgHeartbeatResp", "MsgReadIndex", "MsgReadIndexResp":
		// 当前配置关闭 CheckQuorum，且轻量模型不包含只读请求状态；这些消息
		// 对模型变量是明确的 stutter，而不是泛化的“未知消息忽略”。
		return nil, nil
	case "MsgSnap":
		if m.config.EffectiveProfile() != ProfileStorageSnapshot {
			return nil, nil
		}
		return m.mapDeliveredSnapshot(params, effects)
	case "MsgHeartbeat":
		normalized, err := m.normalizeMessage(params, "from", "to", "term", "commit")
		if err != nil {
			return nil, err
		}
		// Heartbeat 可以推进 term、使 Candidate 降级并传播 commit，不能静默
		// 忽略。轻量模型用无 entry 的 AppendEntries 抽象它。
		normalized["type"] = "MsgApp"
		normalized["index"] = uint64(0)
		normalized["log_term"] = uint64(0)
		normalized["entries"] = []map[string]any{}
		return []model.Event{model.NewEvent("DeliverMessage", normalized)}, nil
	case "MsgProp":
		to, ok := unsignedParam(params["to"])
		if !ok {
			return nil, fmt.Errorf("%w: MsgProp to must be an unsigned integer", ErrUnsupportedSemantics)
		}
		if roleOf(before, core.NodeID(to)) != "leader" {
			// Follower 可能继续转发，Candidate 会明确丢弃；两者在模型中
			// 都尚未发生 ClientRequest 状态转换。
			return nil, nil
		}
		entries, err := normalizeEntries(params["entries"], m.config.MaxValue, m.config.LargestTerm)
		if err != nil {
			return nil, fmt.Errorf("%w: MsgProp: %v", ErrUnsupportedSemantics, err)
		}
		if len(entries) == 0 || uint64(len(entries)) > m.config.MaxLogIndex-lastIndexOf(before, core.NodeID(to)) {
			return nil, fmt.Errorf("%w: MsgProp entries exceed MaxLogIndex %d", ErrUnsupportedSemantics, m.config.MaxLogIndex)
		}
		result := make([]model.Event, len(entries))
		for index, entry := range entries {
			value, exists := entry["Data"].(string)
			if !exists {
				return nil, fmt.Errorf("%w: MsgProp entry %d has no client value", ErrUnsupportedSemantics, index)
			}
			request, err := numericRequest([]byte(value), m.config.MaxValue)
			if err != nil {
				return nil, err
			}
			result[index] = model.NewEvent("ClientRequest", map[string]any{
				"request": request,
				"leader":  to,
			})
		}
		return result, nil
	case "MsgVote":
		normalized, err := m.normalizeMessage(params, "from", "to", "term", "log_term", "index")
		if err != nil {
			return nil, err
		}
		normalized["type"] = messageType
		return []model.Event{model.NewEvent("DeliverMessage", normalized)}, nil
	case "MsgVoteResp":
		normalized, err := m.normalizeMessage(params, "from", "to", "term", "reject")
		if err != nil {
			return nil, err
		}
		normalized["type"] = messageType
		return []model.Event{model.NewEvent("DeliverMessage", normalized)}, nil
	case "MsgApp":
		normalized, err := m.normalizeMessage(params, "from", "to", "term", "commit", "log_term", "index")
		if err != nil {
			return nil, err
		}
		entries, err := normalizeEntries(params["entries"], m.config.MaxValue, m.config.LargestTerm)
		if err != nil {
			return nil, fmt.Errorf("%w: MsgApp: %v", ErrUnsupportedSemantics, err)
		}
		baseIndex := normalized["index"].(uint64)
		if uint64(len(entries)) > m.config.MaxLogIndex-baseIndex {
			return nil, fmt.Errorf("%w: MsgApp entries exceed MaxLogIndex %d", ErrUnsupportedSemantics, m.config.MaxLogIndex)
		}
		normalized["type"] = messageType
		// etcd-raft 对 prev index 已落在 committed 前面的 MsgApp 直接回复当前
		// commit index，不检查也不追加其中的 entries。把这种消息展开会让 TLC
		// 凭空追加 SUT 实际忽略的日志。用一个保证日志不变的 nil append 只抽象
		// 该消息可能造成的 term/role 更新，并保持 commit 不变。
		beforeCommit := commitIndexOf(before, core.NodeID(normalized["to"].(uint64)))
		if baseIndex < beforeCommit {
			normalized["index"] = uint64(0)
			normalized["log_term"] = uint64(0)
			normalized["commit"] = beforeCommit
			normalized["entries"] = []map[string]any{}
			return []model.Event{model.NewEvent("DeliverMessage", normalized)}, nil
		}
		if len(entries) <= 1 {
			normalized["entries"] = entries
			return []model.Event{model.NewEvent("DeliverMessage", normalized)}, nil
		}

		rejected, found, err := appendResponse(effects, normalized["to"].(uint64), normalized["from"].(uint64))
		if err != nil {
			return nil, err
		}
		if !found {
			// etcd-raft 对旧任期 MsgApp 会直接忽略且不发送响应。轻量模型中
			// 第一条 entry 已足以执行同一个拒绝/stutter 分支；继续展开不会
			// 增加语义，只会制造不存在的中间状态。
			normalized["entries"] = entries[:1]
			return []model.Event{model.NewEvent("DeliverMessage", normalized)}, nil
		}
		if rejected {
			// 原子批次被拒绝时，第一条单 entry 动作已足以表达日志不匹配；继续
			// 展开可能让后续 entry 在错误前缀上被模型单独接受。
			normalized["entries"] = entries[:1]
			return []model.Event{model.NewEvent("DeliverMessage", normalized)}, nil
		}

		// 当前 controlled TLC 的 RaftActionMapper 只接受单 entry。成功批次按
		// 日志顺序展开为一组模型事件，保持最终日志和 commit 与原子处理一致。
		// 这些中间模型状态只属于映射实现，不对应额外的 SUT Action。
		result := make([]model.Event, len(entries))
		previousTerm := normalized["log_term"].(uint64)
		for index, entry := range entries {
			projected := make(map[string]any, len(normalized)+1)
			for key, value := range normalized {
				projected[key] = value
			}
			projected["index"] = baseIndex + uint64(index)
			projected["log_term"] = previousTerm
			projected["entries"] = []map[string]any{entry}
			result[index] = model.NewEvent("DeliverMessage", projected)
			previousTerm = entry["Term"].(uint64)
		}
		return result, nil
	case "MsgAppResp":
		normalized, err := m.normalizeMessage(params, "from", "to", "term", "reject", "index")
		if err != nil {
			return nil, err
		}
		normalized["type"] = messageType
		return []model.Event{model.NewEvent("DeliverMessage", normalized)}, nil
	default:
		return nil, fmt.Errorf("%w: delivered message type %s", ErrUnsupportedSemantics, messageType)
	}
}

func (m *Mapper) mapDeliveredSnapshot(params map[string]any, effects []core.Effect) ([]model.Event, error) {
	normalized, err := m.normalizeMessage(
		params, "from", "to", "term", "snapshot_index", "snapshot_term",
	)
	if err != nil {
		return nil, err
	}
	index := normalized["snapshot_index"].(uint64)
	snapshotTerm := normalized["snapshot_term"].(uint64)
	if index == 0 {
		return nil, fmt.Errorf("%w: MsgSnap carries an empty snapshot", ErrUnsupportedSemantics)
	}
	outcome := ""
	for _, effect := range effects {
		if effect.Kind != core.EffectModelEvent || effect.ModelEvent == nil {
			continue
		}
		name := effect.ModelEvent.Name
		mappedName := ""
		switch name {
		case "raft.snapshot_applied":
			mappedName = "InstallSnapshot"
		case "raft.snapshot_fast_forwarded":
			mappedName = "FastForwardSnapshot"
		case "raft.snapshot_rejected_or_stale":
			mappedName = "RejectSnapshot"
		default:
			continue
		}
		eventIndex, eventTerm, boundaryErr := snapshotBoundaryParams(effect.ModelEvent)
		if boundaryErr != nil || uint64(effect.ModelEvent.Node) != normalized["to"].(uint64) ||
			eventIndex != index || eventTerm != snapshotTerm {
			return nil, fmt.Errorf("%w: MsgSnap lifecycle outcome does not match delivered snapshot", ErrUnsupportedSemantics)
		}
		if outcome != "" && outcome != mappedName {
			return nil, fmt.Errorf("%w: MsgSnap has conflicting lifecycle outcomes", ErrUnsupportedSemantics)
		}
		outcome = mappedName
	}
	if outcome == "" {
		return nil, fmt.Errorf("%w: MsgSnap delivery has no recorded lifecycle outcome", ErrUnsupportedSemantics)
	}
	return []model.Event{model.NewEvent(outcome, map[string]any{
		"i":             normalized["from"],
		"j":             normalized["to"],
		"index":         index,
		"snapshot_term": snapshotTerm,
		"term":          normalized["term"],
	})}, nil
}

func appendResponse(effects []core.Effect, from, to uint64) (rejected, found bool, err error) {
	for _, effect := range effects {
		if effect.Kind != core.EffectSendMessage || effect.Message == nil ||
			effect.Message.TypeHint != "MsgAppResp" || uint64(effect.Message.From) != from || uint64(effect.Message.To) != to {
			continue
		}
		value, parseErr := strconv.ParseBool(effect.Message.Metadata["reject"])
		if parseErr != nil {
			return false, false, fmt.Errorf("%w: MsgAppResp has invalid reject metadata", ErrUnsupportedSemantics)
		}
		return value, true, nil
	}
	return false, false, nil
}

func (m *Mapper) normalizeMessage(params map[string]any, names ...string) (map[string]any, error) {
	result := make(map[string]any, len(names)+1)
	for _, name := range names {
		if name == "reject" {
			value, ok := params[name].(bool)
			if !ok {
				return nil, fmt.Errorf("%w: message parameter %s must be boolean", ErrUnsupportedSemantics, name)
			}
			result[name] = value
			continue
		}
		value, ok := unsignedParam(params[name])
		if !ok {
			return nil, fmt.Errorf("%w: message parameter %s must be an unsigned integer", ErrUnsupportedSemantics, name)
		}
		switch name {
		case "from", "to":
			if _, exists := m.nodes[core.NodeID(value)]; !exists {
				return nil, fmt.Errorf("%w: message %s node n%d is outside model Server", ErrUnsupportedSemantics, name, value)
			}
		case "term", "log_term", "snapshot_term":
			if value > m.config.LargestTerm {
				return nil, fmt.Errorf("%w: %s %d exceeds LargestTerm %d", ErrUnsupportedSemantics, name, value, m.config.LargestTerm)
			}
		case "index", "commit", "snapshot_index":
			if value > m.config.MaxLogIndex {
				return nil, fmt.Errorf("%w: %s %d exceeds MaxLogIndex %d", ErrUnsupportedSemantics, name, value, m.config.MaxLogIndex)
			}
		}
		result[name] = value
	}
	return result, nil
}

func normalizeEntries(value any, maximumValue int, largestTerm uint64) ([]map[string]any, error) {
	var raw []map[string]any
	switch entries := value.(type) {
	case []map[string]any:
		raw = entries
	case []any:
		raw = make([]map[string]any, len(entries))
		for i, entry := range entries {
			mapped, ok := entry.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("entry %d is not an object", i)
			}
			raw[i] = mapped
		}
	case nil:
		raw = []map[string]any{}
	default:
		return nil, fmt.Errorf("entries must be an array")
	}

	result := make([]map[string]any, len(raw))
	for i, entry := range raw {
		term, ok := unsignedParam(entry["Term"])
		if !ok {
			return nil, fmt.Errorf("entry %d Term must be an unsigned integer", i)
		}
		if term > largestTerm {
			return nil, fmt.Errorf("entry %d Term %d exceeds LargestTerm %d", i, term, largestTerm)
		}
		if entryType, exists := entry["Type"].(string); exists && entryType != "EntryNormal" {
			return nil, fmt.Errorf("entry %d type %s is not modeled", i, entryType)
		}
		projected := map[string]any{"Term": term}
		if data, exists := entry["Data"]; exists {
			text, ok := data.(string)
			if !ok {
				return nil, fmt.Errorf("entry %d Data must be a string", i)
			}
			parsed, err := strconv.Atoi(text)
			if err != nil || parsed < 1 || parsed > maximumValue {
				return nil, fmt.Errorf("entry %d Data %q must be in 1..%d", i, text, maximumValue)
			}
			projected["Data"] = text
		}
		result[i] = projected
	}
	return result, nil
}

func unsignedParam(value any) (uint64, bool) {
	switch value := value.(type) {
	case uint64:
		return value, true
	case uint32:
		return uint64(value), true
	case uint:
		return uint64(value), true
	case int:
		return uint64(value), value >= 0
	case int64:
		return uint64(value), value >= 0
	case float64:
		// JSON number 使用 float64 解码；2^64 不能转换为 uint64，因此上界必须
		// 使用排他的 2^64，而不能与经 float64 舍入的 MaxUint64 比较。
		if value < 0 || value >= 18446744073709551616.0 || math.Trunc(value) != value {
			return 0, false
		}
		return uint64(value), true
	default:
		return 0, false
	}
}

func numericRequest(request []byte, maximum int) (int, error) {
	value, err := strconv.Atoi(string(request))
	if err != nil || value < 1 || value > maximum {
		return 0, fmt.Errorf("%w: request %q must be an integer in 1..%d", ErrUnsupportedSemantics, request, maximum)
	}
	return value, nil
}

func (m *Mapper) validateObservedBounds(observation core.Observation) error {
	if len(observation.Nodes) != len(m.nodes) {
		return fmt.Errorf(
			"%w: observation has %d nodes, model Server has %d",
			ErrUnsupportedSemantics, len(observation.Nodes), len(m.nodes),
		)
	}
	seen := make(map[core.NodeID]struct{}, len(observation.Nodes))
	for _, node := range observation.Nodes {
		if _, exists := m.nodes[node.ID]; !exists {
			return fmt.Errorf("%w: observed node %s is outside model Server", ErrUnsupportedSemantics, node.ID)
		}
		seen[node.ID] = struct{}{}
		role, ok := node.Semantic["role"].(string)
		if !ok || !validObservedRole(role, node.Status) {
			return fmt.Errorf("%w: node %s has unsupported role %q", ErrUnsupportedSemantics, node.ID, role)
		}
		term, ok := unsignedParam(node.Semantic["term"])
		if !ok {
			return fmt.Errorf("%w: node %s term is not an unsigned integer", ErrUnsupportedSemantics, node.ID)
		}
		if term > m.config.LargestTerm {
			return fmt.Errorf("%w: node %s term exceeds LargestTerm %d", ErrUnsupportedSemantics, node.ID, m.config.LargestTerm)
		}
		lastIndex, ok := unsignedParam(node.Semantic["last_index"])
		if !ok {
			return fmt.Errorf("%w: node %s last_index is not an unsigned integer", ErrUnsupportedSemantics, node.ID)
		}
		if lastIndex > m.config.MaxLogIndex {
			return fmt.Errorf("%w: node %s log exceeds MaxLogIndex %d", ErrUnsupportedSemantics, node.ID, m.config.MaxLogIndex)
		}
		for _, field := range []string{"last_term", "commit"} {
			value, ok := unsignedParam(node.Semantic[field])
			if !ok {
				return fmt.Errorf("%w: node %s %s is not an unsigned integer", ErrUnsupportedSemantics, node.ID, field)
			}
			limit := m.config.MaxLogIndex
			if field == "last_term" {
				limit = m.config.LargestTerm
			}
			if value > limit {
				return fmt.Errorf("%w: node %s %s %d exceeds bound %d", ErrUnsupportedSemantics, node.ID, field, value, limit)
			}
		}
	}
	for id := range m.nodes {
		if _, exists := seen[id]; !exists {
			return fmt.Errorf("%w: model node %s is missing from observation", ErrUnsupportedSemantics, id)
		}
	}
	return nil
}

func validObservedRole(role string, status core.NodeStatus) bool {
	if status == core.NodeCrashed {
		return role == "crashed"
	}
	switch role {
	case "follower", "candidate", "leader":
		return true
	default:
		return false
	}
}

func roleOf(observation core.Observation, id core.NodeID) string {
	for _, node := range observation.Nodes {
		if node.ID == id {
			role, _ := node.Semantic["role"].(string)
			return role
		}
	}
	return ""
}

func statusOf(observation core.Observation, id core.NodeID) core.NodeStatus {
	for _, node := range observation.Nodes {
		if node.ID == id {
			return node.Status
		}
	}
	return ""
}

func termOf(observation core.Observation, id core.NodeID) uint64 {
	for _, node := range observation.Nodes {
		if node.ID == id {
			value, _ := unsignedParam(node.Semantic["term"])
			return value
		}
	}
	return 0
}

func lastIndexOf(observation core.Observation, id core.NodeID) uint64 {
	for _, node := range observation.Nodes {
		if node.ID == id {
			value, _ := unsignedParam(node.Semantic["last_index"])
			return value
		}
	}
	return 0
}

func commitIndexOf(observation core.Observation, id core.NodeID) uint64 {
	for _, node := range observation.Nodes {
		if node.ID == id {
			value, _ := unsignedParam(node.Semantic["commit"])
			return value
		}
	}
	return 0
}

func becameLeaders(before, after core.Observation) []core.NodeID {
	result := make([]core.NodeID, 0)
	for _, node := range after.Nodes {
		if node.Status == core.NodeRunning && roleOf(after, node.ID) == "leader" && roleOf(before, node.ID) != "leader" {
			result = append(result, node.ID)
		}
	}
	return result
}
