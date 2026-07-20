package engine

import (
	"context"
	"errors"
	"fmt"

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/core"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/model"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/oracle"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/plan"
	runtimepkg "github.com/SuzumiyaHaruki/modelfuzz-ng/internal/runtime"
)

// Resolver 是 Engine 对 Plan 解析器所需的最小接口。
type Resolver interface {
	Resolve(action plan.PlanAction, observation core.Observation) plan.Resolution
}

// ActionSource 根据最新 Observation 在线生成下一条高层动作。返回 more=false
// 表示策略主动结束本次执行。
type ActionSource interface {
	Reset(initial core.Observation) error
	Next(observation core.Observation) (action plan.PlanAction, more bool, err error)
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
	profile  model.Profile
	executor model.Executor
	oracles  []oracle.Checker
	config   Config
}

// New 创建 Engine。模型 Executor 可以为 nil，此时仍会生成 ModelEvents，
// 但不会连接 TLC 或其他模型后端。
func New(runtime *runtimepkg.Runtime, resolver Resolver, mapper model.Mapper, executor model.Executor, config Config, checkers ...oracle.Checker) (*Engine, error) {
	if runtime == nil {
		return nil, fmt.Errorf("%w: runtime is nil", ErrInvalidConfig)
	}
	if resolver == nil {
		return nil, fmt.Errorf("%w: resolver is nil", ErrInvalidConfig)
	}
	if mapper == nil {
		return nil, fmt.Errorf("%w: mapper is nil", ErrInvalidConfig)
	}
	for index, checker := range checkers {
		if checker == nil {
			return nil, fmt.Errorf("%w: oracle %d is nil", ErrInvalidConfig, index)
		}
	}
	profile, _ := mapper.(model.Profile)
	return &Engine{
		runtime: runtime, resolver: resolver, mapper: mapper, profile: profile,
		executor: executor, oracles: append([]oracle.Checker(nil), checkers...), config: config,
	}, nil
}

// Run 执行一条 PlanSequence。每个 PlanAction 都使用上一条 Concrete Action
// 执行后的最新 Observation 解析，因此消息位置和相对时间不会提前固化。
func (e *Engine) Run(ctx context.Context, sequence plan.PlanSequence) (Result, error) {
	if err := sequence.Validate(); err != nil {
		return fail(newResult(), StatusInvalidPlan, fmt.Errorf("%w: %v", ErrInvalidPlan, err))
	}
	return e.run(ctx, len(sequence.Actions), false, nil, func(index int, _ core.Observation) (plan.PlanAction, bool, error) {
		return sequence.Actions[index].Copy(), true, nil
	})
}

// RunSource 使用在线策略执行至策略主动结束或达到 maxPlanActions。达到预算是
// 正常完成，并通过 Result.BudgetExhausted 标记，不作为错误。
func (e *Engine) RunSource(ctx context.Context, source ActionSource, maxPlanActions int) (Result, error) {
	if source == nil {
		return fail(newResult(), StatusPolicyFailed, fmt.Errorf("%w: action source is nil", ErrPolicy))
	}
	if maxPlanActions <= 0 {
		return fail(newResult(), StatusPolicyFailed, fmt.Errorf("%w: max plan actions must be positive", ErrPolicy))
	}
	return e.run(ctx, maxPlanActions, true, source.Reset, func(_ int, observation core.Observation) (plan.PlanAction, bool, error) {
		return source.Next(observation.Copy())
	})
}

type sourceInitializer func(core.Observation) error
type nextAction func(int, core.Observation) (plan.PlanAction, bool, error)

func (e *Engine) run(ctx context.Context, maximum int, budgeted bool, initialize sourceInitializer, next nextAction) (Result, error) {
	result := newResult()
	if e == nil || e.runtime == nil || e.resolver == nil || e.mapper == nil {
		return fail(result, StatusRuntimeFailed, fmt.Errorf("%w: engine is not initialized", ErrInvalidConfig))
	}
	if err := ctx.Err(); err != nil {
		return fail(result, StatusCanceled, err)
	}

	observation, err := e.runtime.Reset(ctx)
	if err != nil {
		e.capture(&result, core.Observation{})
		status := StatusRuntimeFailed
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			status = StatusCanceled
		}
		return fail(result, status, fmt.Errorf("%w: reset: %w", ErrRuntime, err))
	}
	result.Initial = observation.Copy()
	result.Final = observation.Copy()
	for _, checker := range e.oracles {
		findings := checker.Reset(observation.Copy())
		e.appendFindings(&result, findings, 0)
	}
	if len(result.OracleFindings) > 0 {
		e.capture(&result, observation)
		return fail(result, StatusOracleFailed, fmt.Errorf(
			"%w: initial state: %s", ErrOracle, result.OracleFindings[0].Message,
		))
	}
	if initialize != nil {
		if err := initialize(observation.Copy()); err != nil {
			e.capture(&result, observation)
			return fail(result, StatusPolicyFailed, fmt.Errorf("%w: reset: %v", ErrPolicy, err))
		}
	}

	processed := 0
	for planIndex := 0; planIndex < maximum; planIndex++ {
		if err := ctx.Err(); err != nil {
			e.capture(&result, observation)
			return fail(result, StatusCanceled, err)
		}
		planned, more, err := next(planIndex, observation)
		if err != nil {
			e.capture(&result, observation)
			return fail(result, StatusPolicyFailed, fmt.Errorf("%w: action %d: %v", ErrPolicy, planIndex, err))
		}
		if !more {
			break
		}
		if err := planned.Validate(); err != nil {
			e.capture(&result, observation)
			return fail(result, StatusInvalidPlan, fmt.Errorf("%w: generated action %d: %v", ErrInvalidPlan, planIndex, err))
		}
		processed++

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
			if e.profile != nil {
				if err := e.profile.ValidateAction(action, observation); err != nil {
					e.capture(&result, observation)
					return fail(result, StatusUnsupported, fmt.Errorf(
						"%w: plan action %d concrete action %d: %v", ErrUnsupported, planIndex, actionIndex, err,
					))
				}
			}
			step, err := e.runtime.Execute(ctx, action)
			if err != nil {
				e.capture(&result, observation)
				status := StatusRuntimeFailed
				if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
					status = StatusCanceled
				}
				return fail(result, status, fmt.Errorf(
					"%w: plan action %d concrete action %d: %w", ErrRuntime, planIndex, actionIndex, err,
				))
			}
			observation = step.Observation.Copy()
			result.Final = observation.Copy()
			result.Actions.Actions = append(result.Actions.Actions, action.Copy())

			transition := model.Transition{
				Before: step.BeforeObservation,
				Record: step.Record,
				After:  step.Observation,
			}
			events, err := e.mapper.Map(transition)
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
			for _, checker := range e.oracles {
				e.appendFindings(&result, checker.Check(transition.Copy()), len(result.Actions.Actions))
			}
			if len(result.OracleFindings) > 0 {
				e.capture(&result, observation)
				return fail(result, StatusOracleFailed, fmt.Errorf(
					"%w: plan action %d concrete action %d: %s",
					ErrOracle, planIndex, actionIndex, result.OracleFindings[0].Message,
				))
			}
		}
	}
	if budgeted && processed == maximum {
		result.BudgetExhausted = true
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

func (e *Engine) appendFindings(result *Result, findings []oracle.Finding, step int) {
	for _, finding := range findings {
		finding.Step = step
		result.OracleFindings = append(result.OracleFindings, finding)
	}
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
	result.Failure = e.runtime.Failure()
}

func fail(result Result, status Status, err error) (Result, error) {
	result.Status = status
	result.Error = err.Error()
	return result, err
}
