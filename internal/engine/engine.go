package engine

import (
	"context"
	"errors"
	"fmt"
	"strings"

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

// PrefixStep 是一次已完成 Concrete Action 的因果前缀视图。它只包含当前及
// 过去已经发生的信息，供显式启用的在线分析器使用；默认 Run 不创建该视图。
type PrefixStep struct {
	PlanActionIndex     int
	ConcreteActionIndex int
	ActionIndex         int
	Before              core.Observation
	Record              core.StepRecord
	After               core.Observation
	ModelEvents         []model.Event
}

// PrefixObserver 允许实验性分析器在执行过程中按 Action 顺序观察真实前缀。
// Observer 不能修改 Runtime、Observation 或模型事件。
type PrefixObserver interface {
	Reset(initial core.Observation) error
	Observe(step PrefixStep) error
}

// Config 控制 best-effort Plan 的边界。默认允许 partial、skipped 和
// empty_queue：这些状态会被记录，但不会终止整条 Plan。
type Config struct {
	FailOnPartial       bool `json:"fail_on_partial"`
	FailOnSkipped       bool `json:"fail_on_skipped"`
	FailOnEmptyQueue    bool `json:"fail_on_empty_queue"`
	MaxPlanActions      int  `json:"max_plan_actions"`
	MaxConsecutiveNoops int  `json:"max_consecutive_noops"`
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
	if config.MaxPlanActions < 0 || config.MaxConsecutiveNoops < 0 {
		return nil, fmt.Errorf("%w: engine budgets must not be negative", ErrInvalidConfig)
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
	return e.RunObserved(ctx, sequence, nil)
}

// RunObserved 与 Run 使用完全相同的执行路径，仅在显式提供 observer 时同步
// 发布已完成的 Concrete Action。默认 fuzz 调用 Run，行为不变。
func (e *Engine) RunObserved(ctx context.Context, sequence plan.PlanSequence, observer PrefixObserver) (Result, error) {
	if err := sequence.Validate(); err != nil {
		return fail(newResult(), StatusInvalidPlan, fmt.Errorf("%w: %v", ErrInvalidPlan, err))
	}
	maximum := len(sequence.Actions)
	budgeted := false
	if e != nil && e.config.MaxPlanActions > 0 && maximum > e.config.MaxPlanActions {
		maximum = e.config.MaxPlanActions
		budgeted = true
	}
	return e.run(ctx, maximum, budgeted, false, nil, observer, func(index int, _ core.Observation) (plan.PlanAction, bool, error) {
		return sequence.Actions[index].Copy(), true, nil
	})
}

// RunSource 使用在线策略执行至策略主动结束或达到 maxPlanActions。达到预算是
// 正常完成，并通过 Result.BudgetExhausted 标记，不作为错误。
func (e *Engine) RunSource(ctx context.Context, source ActionSource, maxPlanActions int) (Result, error) {
	return e.RunSourceObserved(ctx, source, maxPlanActions, nil)
}

// RunSourceObserved is the explicit analysis variant of RunSource. Default
// fuzz policies continue to call RunSource and do not construct an observer.
func (e *Engine) RunSourceObserved(
	ctx context.Context,
	source ActionSource,
	maxPlanActions int,
	observer PrefixObserver,
) (Result, error) {
	if source == nil {
		return fail(newResult(), StatusPolicyFailed, fmt.Errorf("%w: action source is nil", ErrPolicy))
	}
	if maxPlanActions <= 0 {
		return fail(newResult(), StatusPolicyFailed, fmt.Errorf("%w: max plan actions must be positive", ErrPolicy))
	}
	if e != nil && e.config.MaxPlanActions > 0 && maxPlanActions > e.config.MaxPlanActions {
		maxPlanActions = e.config.MaxPlanActions
	}
	return e.run(ctx, maxPlanActions, true, true, source.Reset, observer, func(_ int, observation core.Observation) (plan.PlanAction, bool, error) {
		return source.Next(observation.Copy())
	})
}

type sourceInitializer func(core.Observation) error
type nextAction func(int, core.Observation) (plan.PlanAction, bool, error)

func (e *Engine) run(
	ctx context.Context, maximum int, budgeted, online bool,
	initialize sourceInitializer, observer PrefixObserver, next nextAction,
) (Result, error) {
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
	if observer != nil {
		if err := observer.Reset(observation.Copy()); err != nil {
			e.capture(&result, observation)
			return fail(result, StatusPolicyFailed, fmt.Errorf("%w: prefix observer reset: %v", ErrPolicy, err))
		}
	}
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
	consecutiveNoops := 0
	policyComplete := false
	terminated := false

planLoop:
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
			policyComplete = true
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

		if len(resolution.Actions) == 0 {
			consecutiveNoops++
			if e.config.MaxConsecutiveNoops > 0 && consecutiveNoops >= e.config.MaxConsecutiveNoops {
				result.BudgetExhausted = true
				result.Termination = TerminationConsecutiveNoops
				break
			}
			continue
		}
		executedInResolution := make([]core.Action, 0, len(resolution.Actions))
		inapplicableReasons := make([]string, 0)
		inapplicableCodes := make([]plan.ResolutionReasonCode, 0)
		for actionIndex, action := range resolution.Actions {
			if e.profile != nil {
				if err := e.profile.ValidateAction(action, observation); err != nil {
					if errors.Is(err, model.ErrActionInapplicable) {
						inapplicableReasons = append(inapplicableReasons, err.Error())
						inapplicableCodes = append(inapplicableCodes, plan.ResolutionReasonCode(model.CodeOf(err)))
						continue
					}
					if errors.Is(err, model.ErrModelBoundReached) {
						result.Termination = TerminationModelBound
						result.TerminationCode = string(model.CodeOf(err))
						result.TerminationDetail = err.Error()
						if len(executedInResolution) == 0 {
							markResolutionStopped(
								&result.Resolutions[len(result.Resolutions)-1], plan.ResolutionModelBound,
								plan.ResolutionReasonCode(model.CodeOf(err)), err.Error(),
							)
						} else {
							markResolutionInapplicable(
								&result.Resolutions[len(result.Resolutions)-1], executedInResolution,
								[]plan.ResolutionReasonCode{plan.ResolutionReasonCode(model.CodeOf(err))}, []string{err.Error()},
							)
						}
						terminated = true
						break planLoop
					}
					result.TerminationCode = string(model.CodeOf(err))
					markResolutionStopped(
						&result.Resolutions[len(result.Resolutions)-1], plan.ResolutionUnsupported,
						plan.ResolutionReasonCode(model.CodeOf(err)), err.Error(),
					)
					e.capture(&result, observation)
					return fail(result, StatusUnsupported, fmt.Errorf(
						"%w: plan action %d concrete action %d: %v", ErrUnsupported, planIndex, actionIndex, err,
					))
				}
			}
			step, err := e.runtime.Execute(ctx, action)
			if err != nil {
				e.capture(&result, observation)
				if errors.Is(err, runtimepkg.ErrBudgetExceeded) {
					result.BudgetExhausted = true
					result.Termination = TerminationRuntimeBudget
					terminated = true
					break planLoop
				}
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
			executedInResolution = append(executedInResolution, action.Copy())

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
			if observer != nil {
				prefixEvents := make([]model.Event, len(events))
				for index, event := range events {
					prefixEvents[index] = event.Copy()
				}
				if err := observer.Observe(PrefixStep{
					PlanActionIndex: planIndex, ConcreteActionIndex: actionIndex,
					ActionIndex: len(result.Actions.Actions) - 1,
					Before:      step.BeforeObservation.Copy(), Record: step.Record.Copy(),
					After: step.Observation.Copy(), ModelEvents: prefixEvents,
				}); err != nil {
					e.capture(&result, observation)
					return fail(result, StatusPolicyFailed, fmt.Errorf(
						"%w: prefix observer action %d: %v", ErrPolicy, planIndex, err))
				}
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
		if len(inapplicableReasons) > 0 {
			markResolutionInapplicable(
				&result.Resolutions[len(result.Resolutions)-1], executedInResolution, inapplicableCodes, inapplicableReasons,
			)
		}
		if len(executedInResolution) == 0 {
			consecutiveNoops++
			if e.config.MaxConsecutiveNoops > 0 && consecutiveNoops >= e.config.MaxConsecutiveNoops {
				result.BudgetExhausted = true
				result.Termination = TerminationConsecutiveNoops
				break
			}
		} else {
			consecutiveNoops = 0
		}
	}
	if result.Termination == "" && budgeted && processed == maximum {
		result.BudgetExhausted = true
		result.Termination = TerminationPlanActionBudget
	} else if result.Termination == "" && policyComplete {
		result.Termination = TerminationPolicyComplete
	} else if result.Termination == "" && !online {
		result.Termination = TerminationPlanComplete
	} else if result.Termination == "" && terminated {
		result.Termination = TerminationRuntimeBudget
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

func markResolutionStopped(resolution *plan.Resolution, status plan.ResolutionStatus, code plan.ResolutionReasonCode, reason string) {
	resolution.Status = status
	resolution.Resolved = 0
	resolution.Actions = nil
	resolution.ReasonCode = code
	resolution.Reason = reason
}

func markResolutionInapplicable(resolution *plan.Resolution, executed []core.Action, codes []plan.ResolutionReasonCode, reasons []string) {
	resolution.Actions = make([]core.Action, len(executed))
	for index, action := range executed {
		resolution.Actions[index] = action.Copy()
	}
	resolution.Resolved = len(executed)
	resolution.ReasonCode = combinedReasonCode(codes)
	resolution.Reason = strings.Join(reasons, "; ")
	if resolution.Resolved == 0 {
		resolution.Status = plan.ResolutionInapplicable
	} else if resolution.Resolved < resolution.Requested {
		resolution.Status = plan.ResolutionPartial
	}
}

func combinedReasonCode(codes []plan.ResolutionReasonCode) plan.ResolutionReasonCode {
	if len(codes) == 0 {
		return ""
	}
	first := codes[0]
	for _, code := range codes[1:] {
		if code != first {
			return plan.ReasonMultipleDecisions
		}
	}
	return first
}

func fail(result Result, status Status, err error) (Result, error) {
	result.Status = status
	result.Error = err.Error()
	return result, err
}
