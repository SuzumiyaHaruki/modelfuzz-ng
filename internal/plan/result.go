package plan

import (
	"fmt"

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/core"
)

// ResolutionStatus 描述 PlanAction 根据当前 Observation 展开的结果。
// Resolved 只表示具体动作已经完整解析；Runtime 的执行结果由 Engine 另行记录。
type ResolutionStatus string

const (
	ResolutionResolved   ResolutionStatus = "resolved"
	ResolutionPartial    ResolutionStatus = "partial"
	ResolutionSkipped    ResolutionStatus = "skipped"
	ResolutionInvalid    ResolutionStatus = "invalid"
	ResolutionEmptyQueue ResolutionStatus = "empty_queue"
)

func (s ResolutionStatus) Valid() bool {
	switch s {
	case ResolutionResolved, ResolutionPartial, ResolutionSkipped,
		ResolutionInvalid, ResolutionEmptyQueue:
		return true
	default:
		return false
	}
}

// Resolution 保存一次解析的输入副本、状态和具体动作。Requested/Resolved
// 对消息批量动作分别表示请求数量和实际解析数量；其他动作的 Requested 为 1。
type Resolution struct {
	Plan      PlanAction       `json:"plan"`
	Status    ResolutionStatus `json:"status"`
	Requested int              `json:"requested"`
	Resolved  int              `json:"resolved"`
	Reason    string           `json:"reason,omitempty"`
	Actions   []core.Action    `json:"actions,omitempty"`
}

func (r Resolution) Validate() error {
	if !r.Status.Valid() {
		return fmt.Errorf("%w: unknown resolution status %q", ErrInvalidPlan, r.Status)
	}
	// invalid 专门用于保留无法通过校验的原始 PlanAction；其他结果必须来自
	// 一条结构合法的 PlanAction。
	if r.Status != ResolutionInvalid {
		if err := r.Plan.Validate(); err != nil {
			return err
		}
	}
	if r.Requested <= 0 {
		return fmt.Errorf("%w: resolution requested count must be positive", ErrInvalidPlan)
	}
	if r.Resolved < 0 || r.Resolved > r.Requested {
		return fmt.Errorf("%w: resolution count %d is outside 0..%d", ErrInvalidPlan, r.Resolved, r.Requested)
	}
	if len(r.Actions) != r.Resolved {
		return fmt.Errorf("%w: resolution has %d actions but resolved count is %d", ErrInvalidPlan, len(r.Actions), r.Resolved)
	}
	for i, action := range r.Actions {
		if err := action.Validate(); err != nil {
			return fmt.Errorf("%w: concrete action %d: %v", ErrInvalidPlan, i, err)
		}
	}

	switch r.Status {
	case ResolutionResolved:
		if r.Resolved != r.Requested {
			return fmt.Errorf("%w: resolved result must resolve every requested action", ErrInvalidPlan)
		}
	case ResolutionPartial:
		if r.Resolved == 0 || r.Resolved >= r.Requested {
			return fmt.Errorf("%w: partial resolution requires 0 < resolved < requested", ErrInvalidPlan)
		}
	case ResolutionSkipped, ResolutionInvalid, ResolutionEmptyQueue:
		if r.Resolved != 0 {
			return fmt.Errorf("%w: %s resolution must not contain actions", ErrInvalidPlan, r.Status)
		}
	}
	return nil
}

func (r Resolution) Copy() Resolution {
	copy := r
	copy.Plan = r.Plan.Copy()
	copy.Actions = make([]core.Action, len(r.Actions))
	for i, action := range r.Actions {
		copy.Actions[i] = action.Copy()
	}
	return copy
}
