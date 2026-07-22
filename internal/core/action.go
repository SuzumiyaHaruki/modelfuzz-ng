package core

import "strconv"

// ActionKind 定义了 ModelFuzz 引擎中可执行的操作类型。
type ActionKind string

const (
	ActionDeliver     ActionKind = "deliver"
	ActionDrop        ActionKind = "drop"
	ActionDuplicate   ActionKind = "duplicate"
	ActionAdvanceTime ActionKind = "advance_time"
	// ActionTimeout 立即注入一次协议超时。自然到期的 timer 不属于可选动作，
	// 而是通过 EffectTimerFired 记录。
	ActionTimeout   ActionKind = "timeout"
	ActionCrash     ActionKind = "crash"
	ActionRestart   ActionKind = "restart"
	ActionRequest   ActionKind = "request"
	ActionPartition ActionKind = "partition"
	ActionHeal      ActionKind = "heal"
)

func (k ActionKind) Valid() bool {
	switch k {
	case ActionDeliver, ActionDrop, ActionDuplicate, ActionAdvanceTime,
		ActionTimeout, ActionCrash, ActionRestart, ActionRequest, ActionPartition, ActionHeal:
		return true
	default:
		return false
	}
}

// MessageSelector 是一个用于选择特定消息的结构体，通常用于指定要操作的消息在消息队列中的位置。
// 它包含一个 LinkID 和一个 Position 字段，其中 Position 表示消息在指定链接上的位置（从零开始计数）。
type MessageSelector struct {
	Link     LinkID `json:"link"`
	Position int    `json:"position"`
}

func (s MessageSelector) Validate() error {
	if err := s.Link.Validate(); err != nil {
		return err
	}
	if s.Position < 0 {
		return invalidValue("message_selector", "position", "must be non-negative")
	}
	return nil
}

// Action 表示已经解析完成、可以实际执行的低层动作。消息操作必须同时携带
// MessageID 和 Selector：MessageID 确定实际操作对象，Selector 记录该消息
// 在执行时所属的链路和队列位置。只有 Selector 的符号化消息选择属于 Plan 层，
// 不使用 core.Action 表示。
type Action struct {
	Kind ActionKind `json:"kind"`

	Node       NodeID            `json:"node,omitempty"`
	Message    MessageID         `json:"message,omitempty"`
	Selector   *MessageSelector  `json:"selector,omitempty"`
	TargetTime LogicalTime       `json:"target_time,omitempty"`
	Request    []byte            `json:"request,omitempty"`
	Partition  *NetworkPartition `json:"partition,omitempty"`
}

func (a Action) Validate() error {
	if !a.Kind.Valid() {
		return invalidValue("action", "kind", "is unknown")
	}

	switch a.Kind {
	case ActionDeliver, ActionDrop, ActionDuplicate:
		if !a.Message.Valid() {
			return invalidValue("action", "message", "concrete message action requires a message ID")
		}
		if a.Selector == nil {
			return invalidValue("action", "selector", "concrete message action requires a selector")
		}
		if err := a.Selector.Validate(); err != nil {
			return err
		}
		if a.Node.Valid() || a.TargetTime != 0 || len(a.Request) != 0 || a.Partition != nil {
			return invalidValue("action", "", "message action contains unrelated fields")
		}
	case ActionAdvanceTime:
		if a.TargetTime == 0 {
			return invalidValue("action", "target_time", "must be non-zero")
		}
		if a.Node.Valid() || a.Message.Valid() || a.Selector != nil || len(a.Request) != 0 || a.Partition != nil {
			return invalidValue("action", "", "advance-time action contains unrelated fields")
		}
	case ActionTimeout, ActionCrash, ActionRestart:
		if !a.Node.Valid() {
			return invalidValue("action", "node", "must be non-zero")
		}
		if a.Message.Valid() || a.Selector != nil || a.TargetTime != 0 || len(a.Request) != 0 || a.Partition != nil {
			return invalidValue("action", "", "node action contains unrelated fields")
		}
	case ActionRequest:
		if !a.Node.Valid() {
			return invalidValue("action", "node", "concrete request requires a target node")
		}
		if len(a.Request) == 0 {
			return invalidValue("action", "request", "must not be empty")
		}
		if a.Message.Valid() || a.Selector != nil || a.TargetTime != 0 || a.Partition != nil {
			return invalidValue("action", "", "request action contains unrelated fields")
		}
	case ActionPartition:
		if a.Partition == nil {
			return invalidValue("action", "partition", "partition action requires groups")
		}
		if err := a.Partition.Validate(); err != nil {
			return err
		}
		if a.Node.Valid() || a.Message.Valid() || a.Selector != nil || a.TargetTime != 0 || len(a.Request) != 0 {
			return invalidValue("action", "", "partition action contains unrelated fields")
		}
	case ActionHeal:
		if a.Node.Valid() || a.Message.Valid() || a.Selector != nil || a.TargetTime != 0 || len(a.Request) != 0 || a.Partition != nil {
			return invalidValue("action", "", "heal action contains unrelated fields")
		}
	}
	return nil
}

// Copy 函数创建一个 Action 的深拷贝，确保原始 Action 的可变字段不会被修改。
func (a Action) Copy() Action {
	copy := a
	copy.Request = append([]byte(nil), a.Request...)
	if a.Partition != nil {
		partition := a.Partition.Copy()
		copy.Partition = &partition
	}
	if a.Selector != nil {
		selector := *a.Selector
		copy.Selector = &selector
	}
	return copy
}

// ActionSequence 是 Plan 在运行过程中逐步解析、实际执行的一组具体 Action。
// 它不要求在运行 Plan 前就完整存在：每个 PlanStep 可以解析为零到多个 Action，
// 已应用的 Action 会依次追加到 ActionSequence。执行结束后，该序列可以绕过
// Plan 的动态解析过程，直接用于严格重放。
type ActionSequence struct {
	Actions []Action `json:"actions"`
}

func (s ActionSequence) Validate() error {
	for i, action := range s.Actions {
		if err := action.Validate(); err != nil {
			return invalidValue("action_sequence", "actions", "action at index "+strconv.Itoa(i)+" is invalid: "+err.Error())
		}
	}
	return nil
}

func (s ActionSequence) Copy() ActionSequence {
	copy := ActionSequence{Actions: make([]Action, len(s.Actions))}
	for i, action := range s.Actions {
		copy.Actions[i] = action.Copy()
	}
	return copy
}
