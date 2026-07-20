package engine

import (
	"context"
	"errors"
	"fmt"

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/core"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/model"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/plan"
	runtimepkg "github.com/SuzumiyaHaruki/modelfuzz-ng/internal/runtime"
)

// Resolver 是 Engine 对 Plan 解析器所需的最小接口。
type Resolver interface {
	Resolve(action plan.PlanAction, observation core.Observation) plan.Resolution
}

// Config 控制 best-effort Plan 的边界。默认允许 partial、skipped 和
// empty_queue：这些状态会被记录，但不会终止整条 Plan。
type Config struct {
	FailOnPartial    bool `json:"fail_on_partial"`
	FailOnSkipped    bool `json:"fail_on_skipped"`
	FailOnEmptyQueue bool `json:"fail_on_empty_queue"`
}

// Engine 是单线程执行器。一个实例可以顺序运行多条 Plan；每次 Run 都会
// Reset Runtime，但不应并发调用同一个 Engine。
type Engine struct {
	runtime  *runtimepkg.Runtime
	resolver Resolver
	mapper   model.Mapper
	executor model.Executor
	config   Config
}

// New 创建 Engine。模型 Executor 可以为 nil，此时仍会生成 ModelEvents，
// 但不会连接 TLC 或其他模型后端。
func New(runtime *runtimepkg.Runtime, resolver Resolver, mapper model.Mapper, executor model.Executor, config Config) (*Engine, error) {
	if runtime == nil {
		return nil, fmt.Errorf("%w: runtime is nil", ErrInvalidConfig)
	}
	if resolver == nil {
		return nil, fmt.Errorf("%w: resolver is nil", ErrInvalidConfig)
	}
	if mapper == nil {
		return nil, fmt.Errorf("%w: mapper is nil", ErrInvalidConfig)
	}
	return &Engine{runtime: runtime, resolver: resolver, mapper: mapper, executor: executor, config: config}, nil
}

// Run 执行一条 PlanSequence。每个 PlanAction 都使用上一条 Concrete Action
// 执行后的最新 Observation 解析，因此消息位置和相对时间不会提前固化。
func (e *Engine) Run(ctx context.Context, sequence plan.PlanSequence) (Result, error) {
	result := newResult()
	if e == nil || e.runtime == nil || e.resolver == nil || e.mapper == nil {
		return fail(result, StatusRuntimeFailed, fmt.Errorf("%w: engine is not initialized", ErrInvalidConfig))
	}
	if err := ctx.Err(); err != nil {
		return fail(result, StatusCanceled, err)
	}
	if err := sequence.Validate(); err != nil {
		return fail(result, StatusInvalidPlan, fmt.Errorf("%w: %v", ErrInvalidPlan, err))
	}

	observation, err := e.runtime.Reset(ctx)
	if err != nil {
		status := StatusRuntimeFailed
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			status = StatusCanceled
		}
		return fail(result, status, fmt.Errorf("%w: reset: %v", ErrRuntime, err))
	}
	result.Initial = observation.Copy()
	result.Final = observation.Copy()

	for planIndex, planned := range sequence.Actions {
		if err := ctx.Err(); err != nil {
			e.capture(&result, observation)
			return fail(result, StatusCanceled, err)
		}

		resolution := e.resolver.Resolve(planned, observation)
		result.Resolutions = append(result.Resolutions, resolution.Copy())
		if err := resolution.Validate(); err != nil {
			e.capture(&result, observation)
			return fail(result, StatusResolutionFailed, fmt.Errorf(
				"%w: plan action %d returned invalid resolution: %v", ErrResolution, planIndex, err,
			))
		}
		if reason := e.rejectionReason(resolution); reason != "" {
			e.capture(&result, observation)
			return fail(result, StatusResolutionFailed, fmt.Errorf(
				"%w: plan action %d: %s", ErrResolution, planIndex, reason,
			))
		}

		for actionIndex, action := range resolution.Actions {
			step, err := e.runtime.Execute(ctx, action)
			if err != nil {
				e.capture(&result, observation)
				status := StatusRuntimeFailed
				if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
					status = StatusCanceled
				}
				return fail(result, status, fmt.Errorf(
					"%w: plan action %d concrete action %d: %v", ErrRuntime, planIndex, actionIndex, err,
				))
			}
			observation = step.Observation.Copy()
			result.Final = observation.Copy()
			result.Actions.Actions = append(result.Actions.Actions, action.Copy())

			events, err := e.mapper.Map(model.Transition{
				Before: step.BeforeObservation,
				Record: step.Record,
				After:  step.Observation,
			})
			if err != nil {
				e.capture(&result, observation)
				return fail(result, StatusMappingFailed, fmt.Errorf(
					"%w: plan action %d concrete action %d: %v", ErrMapping, planIndex, actionIndex, err,
				))
			}
			for eventIndex, event := range events {
				if err := event.Validate(); err != nil {
					e.capture(&result, observation)
					return fail(result, StatusMappingFailed, fmt.Errorf(
						"%w: plan action %d concrete action %d event %d: %v",
						ErrMapping, planIndex, actionIndex, eventIndex, err,
					))
				}
				result.ModelEvents = append(result.ModelEvents, event.Copy())
			}
		}
	}

	e.capture(&result, observation)
	if e.executor != nil {
		result.ModelExecuted = true
		states, err := e.executor.Execute(ctx, result.ModelEvents)
		if err != nil {
			status := StatusModelFailed
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				status = StatusCanceled
			}
			return fail(result, status, fmt.Errorf("%w: %v", ErrModel, err))
		}
		result.ModelStates = append(result.ModelStates, states...)
	}
	result.Status = StatusCompleted
	return result, nil
}

func (e *Engine) rejectionReason(resolution plan.Resolution) string {
	switch resolution.Status {
	case plan.ResolutionInvalid:
		return resolution.Reason
	case plan.ResolutionPartial:
		if e.config.FailOnPartial {
			return "partial resolution: " + resolution.Reason
		}
	case plan.ResolutionSkipped:
		if e.config.FailOnSkipped {
			return "skipped resolution: " + resolution.Reason
		}
	case plan.ResolutionEmptyQueue:
		if e.config.FailOnEmptyQueue {
			return "empty queue: " + resolution.Reason
		}
	}
	return ""
}

func (e *Engine) capture(result *Result, observation core.Observation) {
	result.Final = observation.Copy()
	if trace, err := e.runtime.Trace(); err == nil {
		result.Trace = trace
	}
}

func fail(result Result, status Status, err error) (Result, error) {
	result.Status = status
	result.Error = err.Error()
	return result, err
}
