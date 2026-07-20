package plan

import "fmt"

// PlanSequence 是一组按顺序处理的高层动作。它描述执行意图，不保证每一步
// 最终都能解析；具体执行结果由每个 Resolution 单独记录。
type PlanSequence struct {
	Actions  []PlanAction      `json:"actions"`
	Metadata map[string]string `json:"metadata,omitempty"`
}

func (s PlanSequence) Validate() error {
	for i, action := range s.Actions {
		if err := action.Validate(); err != nil {
			return fmt.Errorf("%w: action %d: %v", ErrInvalidPlan, i, err)
		}
	}
	return nil
}

func (s PlanSequence) Copy() PlanSequence {
	copy := PlanSequence{Actions: make([]PlanAction, len(s.Actions))}
	for i, action := range s.Actions {
		copy.Actions[i] = action.Copy()
	}
	if s.Metadata != nil {
		copy.Metadata = make(map[string]string, len(s.Metadata))
		for key, value := range s.Metadata {
			copy.Metadata[key] = value
		}
	}
	return copy
}
