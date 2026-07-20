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

var ErrUnsupportedSemantics = errors.New("transition is not represented by the raft model")

// Config 与 models/raft/raft.cfg 的有界常量对应。Engine 创建 Adapter 和
// Mapper 时应从同一份运行配置填充这些值，避免模型和 SUT 静默漂移。
type Config struct {
	NodeIDs        []core.NodeID
	MaxValue       int
	MaxLogIndex    uint64
	LargestTerm    uint64
	EmitLeaderNoOp bool
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
	return c, nil
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
		if roleOf(transition.Before, action.Node) != "leader" {
			return nil, fmt.Errorf("%w: client request targets non-leader %s", ErrUnsupportedSemantics, action.Node)
		}
		if lastIndexOf(transition.Before, action.Node) >= m.config.MaxLogIndex {
			return nil, fmt.Errorf("%w: client request exceeds MaxLogIndex %d", ErrUnsupportedSemantics, m.config.MaxLogIndex)
		}
		request, err := numericRequest(action.Request, m.config.MaxValue)
		if err != nil {
			return nil, err
		}
		events = append(events, model.NewEvent("ClientRequest", map[string]any{
			"request": request,
			"leader":  uint64(action.Node),
		}))
	case core.ActionCrash, core.ActionRestart:
		return nil, fmt.Errorf("%w: %s is not enabled in the first raft model", ErrUnsupportedSemantics, action.Kind)
	}

	commitNodes := make([]core.NodeID, 0)
	seenCommit := make(map[core.NodeID]struct{})
	for _, effect := range transition.Record.Effects {
		switch effect.Kind {
		case core.EffectTimerFired:
			if effect.TimerFired.TypeHint == "election" {
				if termOf(transition.Before, effect.TimerFired.Node) >= m.config.LargestTerm {
					return nil, fmt.Errorf("%w: timeout exceeds LargestTerm %d", ErrUnsupportedSemantics, m.config.LargestTerm)
				}
				events = append(events, model.NewEvent("Timeout", map[string]any{
					"node": uint64(effect.TimerFired.Node),
				}))
			}
		case core.EffectModelEvent:
			event := effect.ModelEvent
			switch event.Name {
			case deliveredMessageEvent:
				mapped, err := m.mapDeliveredMessage(event.Params)
				if err != nil {
					return nil, err
				}
				if mapped != nil {
					events = append(events, *mapped)
				}
			case "raft.snapshot_applied", "raft.config_changed":
				return nil, fmt.Errorf("%w: model event %s", ErrUnsupportedSemantics, event.Name)
			case "raft.entry_committed":
				if roleOf(transition.After, event.Node) == "leader" {
					if _, exists := seenCommit[event.Node]; !exists {
						seenCommit[event.Node] = struct{}{}
						commitNodes = append(commitNodes, event.Node)
					}
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
	for _, node := range commitNodes {
		events = append(events, model.NewEvent("AdvanceCommitIndex", map[string]any{"i": uint64(node)}))
	}
	return events, nil
}

func (m *Mapper) mapDeliveredMessage(params map[string]any) (*model.Event, error) {
	messageType, ok := params["type"].(string)
	if !ok || messageType == "" {
		return nil, fmt.Errorf("%w: delivered message has no type", ErrUnsupportedSemantics)
	}

	switch messageType {
	case "MsgHeartbeatResp", "MsgReadIndex", "MsgReadIndexResp":
		// 当前配置关闭 CheckQuorum，且轻量模型不包含只读请求状态；这些消息
		// 对模型变量是明确的 stutter，而不是泛化的“未知消息忽略”。
		return nil, nil
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
		event := model.NewEvent("DeliverMessage", normalized)
		return &event, nil
	case "MsgVote":
		normalized, err := m.normalizeMessage(params, "from", "to", "term", "log_term", "index")
		if err != nil {
			return nil, err
		}
		normalized["type"] = messageType
		event := model.NewEvent("DeliverMessage", normalized)
		return &event, nil
	case "MsgVoteResp":
		normalized, err := m.normalizeMessage(params, "from", "to", "term", "reject")
		if err != nil {
			return nil, err
		}
		normalized["type"] = messageType
		event := model.NewEvent("DeliverMessage", normalized)
		return &event, nil
	case "MsgApp":
		normalized, err := m.normalizeMessage(params, "from", "to", "term", "commit", "log_term", "index")
		if err != nil {
			return nil, err
		}
		entries, err := normalizeEntries(params["entries"], m.config.MaxValue, m.config.LargestTerm)
		if err != nil {
			return nil, fmt.Errorf("%w: MsgApp: %v", ErrUnsupportedSemantics, err)
		}
		if len(entries) > 1 {
			return nil, fmt.Errorf("%w: MsgApp contains %d entries; model supports at most one", ErrUnsupportedSemantics, len(entries))
		}
		normalized["type"] = messageType
		normalized["entries"] = entries
		event := model.NewEvent("DeliverMessage", normalized)
		return &event, nil
	case "MsgAppResp":
		normalized, err := m.normalizeMessage(params, "from", "to", "term", "reject", "index")
		if err != nil {
			return nil, err
		}
		normalized["type"] = messageType
		event := model.NewEvent("DeliverMessage", normalized)
		return &event, nil
	default:
		return nil, fmt.Errorf("%w: delivered message type %s", ErrUnsupportedSemantics, messageType)
	}
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
		case "term", "log_term":
			if value > m.config.LargestTerm {
				return nil, fmt.Errorf("%w: %s %d exceeds LargestTerm %d", ErrUnsupportedSemantics, name, value, m.config.LargestTerm)
			}
		case "index", "commit":
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

func becameLeaders(before, after core.Observation) []core.NodeID {
	result := make([]core.NodeID, 0)
	for _, node := range after.Nodes {
		if node.Status == core.NodeRunning && roleOf(after, node.ID) == "leader" && roleOf(before, node.ID) != "leader" {
			result = append(result, node.ID)
		}
	}
	return result
}
