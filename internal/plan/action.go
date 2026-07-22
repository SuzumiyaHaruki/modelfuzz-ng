// Package plan 定义高层、best-effort 的执行意图，并将其解析为 core.Action。
// Plan 的来源可以是人工、JSON、随机策略或 LLM；本包不依赖任何具体生成方式。
package plan

import (
	"errors"
	"fmt"

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/core"
)

var ErrInvalidPlan = errors.New("invalid plan")

// ActionKind 是 Plan 层的动作类型。AdvanceTicks 使用相对时间，其余动作
// 虽然与 core.ActionKind 名称相近，但仍然属于尚未具体化的高层意图。
type ActionKind string

const (
	ActionDeliver      ActionKind = "deliver"
	ActionDrop         ActionKind = "drop"
	ActionDuplicate    ActionKind = "duplicate"
	ActionAdvanceTicks ActionKind = "advance_ticks"
	ActionTimeout      ActionKind = "timeout"
	ActionCrash        ActionKind = "crash"
	ActionRestart      ActionKind = "restart"
	ActionRequest      ActionKind = "request"
	ActionPartition    ActionKind = "partition"
	ActionHeal         ActionKind = "heal"
)

func (k ActionKind) Valid() bool {
	switch k {
	case ActionDeliver, ActionDrop, ActionDuplicate, ActionAdvanceTicks,
		ActionTimeout, ActionCrash, ActionRestart, ActionRequest, ActionPartition, ActionHeal:
		return true
	default:
		return false
	}
}

// PlanAction 是一条尚未解析到具体 MessageID 或绝对 TargetTime 的高层动作。
// Request 使用字符串以方便人工和 LLM 生成 JSON；解析后会转换为 []byte。
type PlanAction struct {
	Kind ActionKind `json:"kind"`

	Node      core.NodeID            `json:"node,omitempty"`
	Messages  *MessageRangeSelector  `json:"messages,omitempty"`
	Ticks     uint64                 `json:"ticks,omitempty"`
	Request   string                 `json:"request,omitempty"`
	Partition *core.NetworkPartition `json:"partition,omitempty"`
}

func (a PlanAction) Validate() error {
	if !a.Kind.Valid() {
		return fmt.Errorf("%w: unknown action kind %q", ErrInvalidPlan, a.Kind)
	}

	switch a.Kind {
	case ActionDeliver, ActionDrop, ActionDuplicate:
		if a.Messages == nil {
			return fmt.Errorf("%w: %s requires a message selector", ErrInvalidPlan, a.Kind)
		}
		if err := a.Messages.Validate(); err != nil {
			return err
		}
		if a.Node.Valid() || a.Ticks != 0 || a.Request != "" || a.Partition != nil {
			return fmt.Errorf("%w: %s contains unrelated fields", ErrInvalidPlan, a.Kind)
		}
	case ActionAdvanceTicks:
		if a.Ticks == 0 {
			return fmt.Errorf("%w: advance_ticks requires a positive ticks value", ErrInvalidPlan)
		}
		if a.Node.Valid() || a.Messages != nil || a.Request != "" || a.Partition != nil {
			return fmt.Errorf("%w: advance_ticks contains unrelated fields", ErrInvalidPlan)
		}
	case ActionTimeout, ActionCrash, ActionRestart:
		if !a.Node.Valid() {
			return fmt.Errorf("%w: %s requires a node", ErrInvalidPlan, a.Kind)
		}
		if a.Messages != nil || a.Ticks != 0 || a.Request != "" || a.Partition != nil {
			return fmt.Errorf("%w: %s contains unrelated fields", ErrInvalidPlan, a.Kind)
		}
	case ActionRequest:
		if !a.Node.Valid() {
			return fmt.Errorf("%w: request requires a node", ErrInvalidPlan)
		}
		if a.Request == "" {
			return fmt.Errorf("%w: request value must not be empty", ErrInvalidPlan)
		}
		if a.Messages != nil || a.Ticks != 0 || a.Partition != nil {
			return fmt.Errorf("%w: request contains unrelated fields", ErrInvalidPlan)
		}
	case ActionPartition:
		if a.Partition == nil {
			return fmt.Errorf("%w: partition requires groups", ErrInvalidPlan)
		}
		if err := a.Partition.Validate(); err != nil {
			return fmt.Errorf("%w: invalid partition: %v", ErrInvalidPlan, err)
		}
		if a.Node.Valid() || a.Messages != nil || a.Ticks != 0 || a.Request != "" {
			return fmt.Errorf("%w: partition contains unrelated fields", ErrInvalidPlan)
		}
	case ActionHeal:
		if a.Node.Valid() || a.Messages != nil || a.Ticks != 0 || a.Request != "" || a.Partition != nil {
			return fmt.Errorf("%w: heal contains unrelated fields", ErrInvalidPlan)
		}
	}
	return nil
}

func (a PlanAction) Copy() PlanAction {
	copy := a
	if a.Messages != nil {
		selector := *a.Messages
		copy.Messages = &selector
	}
	if a.Partition != nil {
		partition := a.Partition.Copy()
		copy.Partition = &partition
	}
	return copy
}
