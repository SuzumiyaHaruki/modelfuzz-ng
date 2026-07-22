package trace

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/core"
	runtimepkg "github.com/SuzumiyaHaruki/modelfuzz-ng/internal/runtime"
)

var (
	ErrInvalidTrace = errors.New("invalid replay trace")
	ErrDivergence   = errors.New("trace replay diverged")
)

type Status string

const (
	StatusCompleted Status = "completed"
	StatusDiverged  Status = "diverged"
	StatusCanceled  Status = "canceled"
)

// Divergence 定位第一处重放差异。Step 为预期 StepRecord 下标；Field 使用
// 稳定字段名，Expected/Actual 保存便于机器和人工检查的 JSON 值。
type Divergence struct {
	Step     uint64 `json:"step"`
	Field    string `json:"field"`
	Expected any    `json:"expected,omitempty"`
	Actual   any    `json:"actual,omitempty"`
	Reason   string `json:"reason"`
}

type Result struct {
	Status       Status      `json:"status"`
	Error        string      `json:"error,omitempty"`
	MatchedSteps uint64      `json:"matched_steps"`
	Divergence   *Divergence `json:"divergence,omitempty"`
	Actual       core.Trace  `json:"actual_trace"`
}

type Replayer struct {
	runtime *runtimepkg.Runtime
}

func NewReplayer(runtime *runtimepkg.Runtime) (*Replayer, error) {
	if runtime == nil {
		return nil, fmt.Errorf("runtime must not be nil")
	}
	return &Replayer{runtime: runtime}, nil
}

// Replay 从相同 Runtime 配置的初始状态执行 Trace 中的 Concrete Action，
// 并逐字段比较时间、节点快照、Effect 和 ObservationDigest。
func (r *Replayer) Replay(ctx context.Context, expected core.Trace) (Result, error) {
	result := Result{Status: StatusDiverged}
	if r == nil || r.runtime == nil {
		return result, fmt.Errorf("%w: replayer is not initialized", ErrInvalidTrace)
	}
	if err := expected.Validate(); err != nil {
		return result, fmt.Errorf("%w: %v", ErrInvalidTrace, err)
	}
	observation, err := r.runtime.Reset(ctx)
	if err != nil {
		return r.fail(result, nil, fmt.Errorf("reset runtime: %w", err))
	}
	actualTrace, err := r.runtime.Trace()
	if err != nil {
		return r.fail(result, nil, err)
	}
	if actualTrace.ExecutionID != expected.ExecutionID || actualTrace.Seed != expected.Seed {
		divergence := &Divergence{
			Field: "trace_identity", Expected: map[string]any{"execution_id": expected.ExecutionID, "seed": expected.Seed},
			Actual: map[string]any{"execution_id": actualTrace.ExecutionID, "seed": actualTrace.Seed},
			Reason: "Runtime 配置与待重放 Trace 不一致",
		}
		return r.fail(result, divergence, ErrDivergence)
	}

	for index, expectedStep := range expected.Steps {
		if err := ctx.Err(); err != nil {
			result.Status = StatusCanceled
			return r.fail(result, nil, err)
		}
		stepIndex := uint64(index)
		if expectedStep.TimeBefore != r.runtime.Time() {
			return r.diverged(result, stepIndex, "time_before", expectedStep.TimeBefore, r.runtime.Time(), "逻辑时间不一致")
		}
		expectedBefore := expectedStep.NodesBefore
		actualBefore := observation.Nodes
		if equal, err := equalJSON(expectedBefore, actualBefore); err != nil || !equal {
			return r.diverged(result, stepIndex, "nodes_before", expectedBefore, actualBefore, comparisonReason(err))
		}

		step, err := r.runtime.Execute(ctx, expectedStep.Action)
		if err != nil {
			return r.diverged(result, stepIndex, "action", expectedStep.Action, nil, "Concrete Action 无法执行: "+err.Error())
		}
		observation = step.Observation.Copy()
		comparisons := []struct {
			field         string
			expected, got any
		}{
			{field: "time_after", expected: expectedStep.TimeAfter, got: step.Record.TimeAfter},
			{field: "action", expected: expectedStep.Action, got: step.Record.Action},
			{field: "effects", expected: expectedStep.Effects, got: step.Record.Effects},
			{field: "nodes_after", expected: expectedStep.NodesAfter, got: step.Record.NodesAfter},
			{field: "observation_digest", expected: expectedStep.ObservationDigest, got: step.Record.ObservationDigest},
		}
		for _, comparison := range comparisons {
			if comparison.field == "effects" && len(expectedStep.Effects) == 0 && len(step.Record.Effects) == 0 {
				continue
			}
			equal, err := equalJSON(comparison.expected, comparison.got)
			if err != nil || !equal {
				return r.diverged(result, stepIndex, comparison.field, comparison.expected, comparison.got, comparisonReason(err))
			}
		}
		result.MatchedSteps++
	}
	result.Actual, _ = r.runtime.Trace()
	result.Status = StatusCompleted
	return result, nil
}

func (r *Replayer) diverged(result Result, step uint64, field string, expected, actual any, reason string) (Result, error) {
	divergence := &Divergence{Step: step, Field: field, Expected: expected, Actual: actual, Reason: reason}
	return r.fail(result, divergence, fmt.Errorf("%w: step %d field %s: %s", ErrDivergence, step, field, reason))
}

func (r *Replayer) fail(result Result, divergence *Divergence, err error) (Result, error) {
	result.Divergence = divergence
	result.Error = err.Error()
	if trace, traceErr := r.runtime.Trace(); traceErr == nil {
		result.Actual = trace
	}
	return result, err
}

func equalJSON(left, right any) (bool, error) {
	leftJSON, err := json.Marshal(left)
	if err != nil {
		return false, err
	}
	rightJSON, err := json.Marshal(right)
	if err != nil {
		return false, err
	}
	return bytes.Equal(leftJSON, rightJSON), nil
}

func comparisonReason(err error) string {
	if err != nil {
		return "比较值无法序列化: " + err.Error()
	}
	return "实际值与预期值不同"
}
