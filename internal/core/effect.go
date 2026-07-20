package core

import "encoding/json"

// EffectKind 标识 Adapter 执行动作后产生的一种可观察结果。
type EffectKind string

const (
	EffectSendMessage EffectKind = "send_message"
	EffectTimerFired  EffectKind = "timer_fired"
	EffectModelEvent  EffectKind = "model_event"
)

func (k EffectKind) Valid() bool {
	switch k {
	case EffectSendMessage, EffectTimerFired, EffectModelEvent:
		return true
	default:
		return false
	}
}

// TimerFired 记录 Adapter 观察或推断出的一次超时。etcd-raft
// Adapter 通过 Tick 前后的角色、term 和输出变化识别它，不伪造内部 timer ID。
// TypeHint 和 RoleHint 由 Adapter 提供，core 不解释其语义。
type TimerFired struct {
	Node     NodeID            `json:"node"`
	Epoch    NodeEpoch         `json:"node_epoch"`
	Source   TimerFireSource   `json:"source"`
	TypeHint string            `json:"type_hint,omitempty"`
	RoleHint string            `json:"role_hint,omitempty"`
	Metadata map[string]string `json:"metadata,omitempty"`
}

func (e TimerFired) Validate() error {
	if !e.Node.Valid() {
		return invalidValue("timer_fired", "node", "must be non-zero")
	}
	if !e.Epoch.Valid() {
		return invalidValue("timer_fired", "node_epoch", "must be non-zero")
	}
	if !e.Source.Valid() {
		return invalidValue("timer_fired", "source", "is unknown")
	}
	return nil
}

func (e TimerFired) Copy() TimerFired {
	copy := e
	copy.Metadata = cloneStringMap(e.Metadata)
	return copy
}

// ModelEvent 是 Adapter 产生并供模型或事件 Oracle 使用的事件。
// Params 与具体协议相关，core 不解释其语义。
type ModelEvent struct {
	Name   string         `json:"name"`
	Node   NodeID         `json:"node,omitempty"`
	Params map[string]any `json:"params,omitempty"`
}

func (e ModelEvent) Validate() error {
	if e.Name == "" {
		return invalidValue("model_event", "name", "must not be empty")
	}
	if _, err := json.Marshal(e.Params); err != nil {
		return invalidValue("model_event", "params", "must be JSON serializable: "+err.Error())
	}
	return nil
}

func (e ModelEvent) Copy() ModelEvent {
	copy := e
	copy.Params = cloneAnyMap(e.Params)
	return copy
}

// Effect 是经过校验的带标签联合体，只能设置与 Kind 对应的一个 payload。
type Effect struct {
	// At 记录 Effect 的发生时间。当一次 AdvanceTime 执行多轮 Adapter Tick 时，
	// 该字段用于保留各个中间事件的真实逻辑时间。
	At   LogicalTime `json:"at"`
	Kind EffectKind  `json:"kind"`

	Message    *Message    `json:"message,omitempty"`
	TimerFired *TimerFired `json:"timer_fired,omitempty"`
	ModelEvent *ModelEvent `json:"model_event,omitempty"`
}

func (e Effect) Validate() error {
	if !e.Kind.Valid() {
		return invalidValue("effect", "kind", "is unknown")
	}

	payloads := 0
	for _, present := range []bool{
		e.Message != nil,
		e.TimerFired != nil,
		e.ModelEvent != nil,
	} {
		if present {
			payloads++
		}
	}
	if payloads != 1 {
		return invalidValue("effect", "", "must contain exactly one payload")
	}

	switch e.Kind {
	case EffectSendMessage:
		if e.Message == nil {
			return invalidValue("effect", "message", "is required for send_message")
		}
		return e.Message.Validate()
	case EffectTimerFired:
		if e.TimerFired == nil {
			return invalidValue("effect", "timer_fired", "is required for timer_fired")
		}
		return e.TimerFired.Validate()
	case EffectModelEvent:
		if e.ModelEvent == nil {
			return invalidValue("effect", "model_event", "is required for model_event")
		}
		return e.ModelEvent.Validate()
	default:
		return invalidValue("effect", "kind", "is unknown")
	}
}

func (e Effect) Copy() Effect {
	copy := e
	if e.Message != nil {
		message := e.Message.Copy()
		copy.Message = &message
	}
	if e.TimerFired != nil {
		fired := e.TimerFired.Copy()
		copy.TimerFired = &fired
	}
	if e.ModelEvent != nil {
		event := e.ModelEvent.Copy()
		copy.ModelEvent = &event
	}
	return copy
}
