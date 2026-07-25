package model

import (
	"fmt"

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/core"
)

// Transition 是模型映射的完整输入。Record 说明发生了什么，Before 和 After
// 用于判断角色变化等不能仅凭单个 Effect 得出的语义状态转换。
type Transition struct {
	Before core.Observation
	Record core.StepRecord
	After  core.Observation
}

// TransitionFromRecord 使用 Trace 中保存的节点快照重建模型映射输入。
// 消息队列不属于当前协议模型状态，因此无需恢复完整 Observation。
func TransitionFromRecord(record core.StepRecord) (Transition, error) {
	if len(record.NodesBefore) == 0 || len(record.NodesAfter) == 0 {
		return Transition{}, fmt.Errorf("%w: step record does not contain node snapshots", ErrInvalidTransition)
	}
	transition := Transition{
		Before: core.Observation{Time: record.TimeBefore, Nodes: copyNodes(record.NodesBefore)},
		Record: record.Copy(),
		After:  core.Observation{Time: record.TimeAfter, Nodes: copyNodes(record.NodesAfter)},
	}
	if err := transition.Validate(); err != nil {
		return Transition{}, err
	}
	return transition, nil
}

func copyNodes(nodes []core.NodeObservation) []core.NodeObservation {
	result := make([]core.NodeObservation, len(nodes))
	for i, node := range nodes {
		result[i] = node.Copy()
	}
	return result
}

func (t Transition) Validate() error {
	if err := t.Record.Validate(); err != nil {
		return fmt.Errorf("%w: record: %v", ErrInvalidTransition, err)
	}
	if err := t.Before.Validate(); err != nil {
		return fmt.Errorf("%w: before observation: %v", ErrInvalidTransition, err)
	}
	if err := t.After.Validate(); err != nil {
		return fmt.Errorf("%w: after observation: %v", ErrInvalidTransition, err)
	}
	if t.Before.Time != t.Record.TimeBefore {
		return fmt.Errorf("%w: before time is %d, want %d", ErrInvalidTransition, t.Before.Time, t.Record.TimeBefore)
	}
	if t.After.Time != t.Record.TimeAfter {
		return fmt.Errorf("%w: after time is %d, want %d", ErrInvalidTransition, t.After.Time, t.Record.TimeAfter)
	}
	return nil
}

func (t Transition) Copy() Transition {
	return Transition{
		Before: t.Before.Copy(),
		Record: t.Record.Copy(),
		After:  t.After.Copy(),
	}
}
