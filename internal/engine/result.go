package engine

import (
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/core"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/model"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/oracle"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/plan"
)

// Status 是一次 Engine 执行的最终状态。它与 Plan 的 ResolutionStatus 分离：
// 前者描述整条轨迹，后者只描述某条高层动作能否解析。
type Status string

const (
	StatusCompleted        Status = "completed"
	StatusCanceled         Status = "canceled"
	StatusInvalidPlan      Status = "invalid_plan"
	StatusResolutionFailed Status = "resolution_failed"
	StatusRuntimeFailed    Status = "runtime_failed"
	StatusMappingFailed    Status = "mapping_failed"
	StatusUnsupported      Status = "unsupported_by_model"
	StatusOracleFailed     Status = "oracle_failed"
	StatusPolicyFailed     Status = "policy_failed"
	StatusModelFailed      Status = "model_failed"
)

// Result 保存一次执行可以持久化的全部核心产物。即使 Run 返回错误，Result
// 也会尽量包含错误发生前已经完成的 Resolution、Action、Trace 和模型事件。
type Result struct {
	Status          Status              `json:"status"`
	Error           string              `json:"error,omitempty"`
	ModelExecuted   bool                `json:"model_executed"`
	BudgetExhausted bool                `json:"budget_exhausted,omitempty"`
	Resolutions     []plan.Resolution   `json:"resolutions"`
	Actions         core.ActionSequence `json:"actions"`
	Trace           core.Trace          `json:"trace"`
	ModelEvents     []model.Event       `json:"model_events"`
	ModelStates     []model.State       `json:"model_states"`
	OracleFindings  []oracle.Finding    `json:"oracle_findings"`
	Initial         core.Observation    `json:"initial_observation"`
	Final           core.Observation    `json:"final_observation"`
}

func newResult() Result {
	return Result{
		Resolutions:    make([]plan.Resolution, 0),
		Actions:        core.ActionSequence{Actions: make([]core.Action, 0)},
		ModelEvents:    make([]model.Event, 0),
		ModelStates:    make([]model.State, 0),
		OracleFindings: make([]oracle.Finding, 0),
	}
}
