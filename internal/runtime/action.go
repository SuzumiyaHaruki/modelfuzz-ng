package runtime

import (
	"context"
	"fmt"

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/core"
)

// Execute 执行一条已经具体化的 Action，并将实际 Effect 和最终 Observation
// 记录为一条 StepRecord。返回 error 后 Adapter 可能已经发生部分变化，本次
// Runtime 应视为终止，不应继续执行后续 Action。
func (r *Runtime) Execute(ctx context.Context, action core.Action) (StepResult, error) {
	if !r.initialized {
		return StepResult{}, ErrNotInitialized
	}
	if r.terminated {
		return StepResult{}, ErrTerminated
	}
	if err := ctx.Err(); err != nil {
		return StepResult{}, err
	}
	if err := action.Validate(); err != nil {
		return StepResult{}, fmt.Errorf("%w: %v", ErrInvalidAction, err)
	}
	if err := r.validateBudget(action); err != nil {
		return StepResult{}, err
	}
	if err := r.validatePreconditions(action); err != nil {
		return StepResult{}, err
	}

	beforeObservation := r.observation.Copy()
	timeBefore := r.time
	effects, err := r.applyAction(ctx, action)
	if err != nil {
		r.terminated = true
		return StepResult{}, err
	}

	actionCopy := action.Copy()
	r.lastAction = &actionCopy
	observation, err := r.collectObservation(ctx)
	if err != nil {
		r.terminated = true
		return StepResult{}, err
	}
	digest, err := observationDigest(observation)
	if err != nil {
		r.terminated = true
		return StepResult{}, err
	}

	record := core.StepRecord{
		Index:             uint64(len(r.trace.Steps)),
		TimeBefore:        timeBefore,
		TimeAfter:         r.time,
		Action:            action.Copy(),
		Effects:           effects,
		NodesBefore:       copyNodes(beforeObservation.Nodes),
		NodesAfter:        copyNodes(observation.Nodes),
		ObservationDigest: digest,
	}
	if err := r.trace.Append(record); err != nil {
		r.terminated = true
		return StepResult{}, fmt.Errorf("%w: invalid step record: %v", ErrAdapterContract, err)
	}
	r.actionCount++
	r.observation = observation.Copy()

	return StepResult{
		Record:            record.Copy(),
		BeforeObservation: beforeObservation,
		Observation:       observation.Copy(),
	}, nil
}

func (r *Runtime) validateBudget(action core.Action) error {
	limits := r.config.Limits
	if limits.MaxActions != 0 && r.actionCount >= limits.MaxActions {
		return fmt.Errorf("%w: action limit %d reached", ErrBudgetExceeded, limits.MaxActions)
	}
	if action.Kind == core.ActionAdvanceTime && limits.MaxTicks != 0 && uint64(action.TargetTime) > limits.MaxTicks {
		return fmt.Errorf("%w: target time %d exceeds tick limit %d", ErrBudgetExceeded, action.TargetTime, limits.MaxTicks)
	}
	if action.Kind == core.ActionDuplicate && limits.MaxQueuedMessages != 0 &&
		r.network.len() >= limits.MaxQueuedMessages {
		return fmt.Errorf("%w: queued message limit %d reached", ErrBudgetExceeded, limits.MaxQueuedMessages)
	}
	return nil
}

func copyNodes(nodes []core.NodeObservation) []core.NodeObservation {
	result := make([]core.NodeObservation, len(nodes))
	for i, node := range nodes {
		result[i] = node.Copy()
	}
	return result
}

// validatePreconditions 检查 Action 是否满足执行前置条件。若不满足，返回 ErrInvalidAction。
func (r *Runtime) validatePreconditions(action core.Action) error {
	capabilities := r.adapter.Capabilities()
	switch action.Kind {
	case core.ActionTimeout:
		if !capabilities.ForceTimeout {
			return fmt.Errorf("%w: timeout", ErrUnsupportedAction)
		}
	case core.ActionCrash, core.ActionRestart:
		if !capabilities.CrashRestart {
			return fmt.Errorf("%w: %s", ErrUnsupportedAction, action.Kind)
		}
	case core.ActionRequest:
		if !capabilities.ClientRequest {
			return fmt.Errorf("%w: request", ErrUnsupportedAction)
		}
	}
	if action.Kind == core.ActionAdvanceTime && action.TargetTime <= r.time {
		return fmt.Errorf(
			"%w: target time %d must be greater than current time %d",
			ErrInvalidAction, action.TargetTime, r.time,
		)
	}

	switch action.Kind {
	case core.ActionDeliver, core.ActionDrop, core.ActionDuplicate:
		selected, err := r.network.resolve(action.Message, *action.Selector)
		if err != nil {
			return fmt.Errorf("%w: %v", ErrInvalidAction, err)
		}
		if action.Kind == core.ActionDeliver {
			status, ok := r.nodeStatus(selected.message.To)
			if !ok {
				return fmt.Errorf("%w: target node %s is not observable", ErrInvalidAction, selected.message.To)
			}
			if status != core.NodeRunning {
				return fmt.Errorf("%w: target node %s is not running", ErrInvalidAction, selected.message.To)
			}
		}
	case core.ActionTimeout, core.ActionCrash, core.ActionRequest:
		status, ok := r.nodeStatus(action.Node)
		if !ok {
			return fmt.Errorf("%w: node %s is not observable", ErrInvalidAction, action.Node)
		}
		if status != core.NodeRunning {
			return fmt.Errorf("%w: node %s is not running", ErrInvalidAction, action.Node)
		}
	case core.ActionRestart:
		status, ok := r.nodeStatus(action.Node)
		if !ok {
			return fmt.Errorf("%w: node %s is not observable", ErrInvalidAction, action.Node)
		}
		if status != core.NodeCrashed {
			return fmt.Errorf("%w: node %s is not crashed", ErrInvalidAction, action.Node)
		}
	}
	return nil
}

func (r *Runtime) applyAction(ctx context.Context, action core.Action) ([]core.Effect, error) {
	switch action.Kind {
	case core.ActionDeliver:
		selected, err := r.network.resolve(action.Message, *action.Selector)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalidAction, err)
		}
		effects, err := r.adapter.Deliver(ctx, r.time, selected.message.Copy())
		if err != nil {
			return nil, fmt.Errorf("%w: deliver %s: %v", ErrAdapter, action.Message, err)
		}
		if _, err := r.network.remove(action.Message, *action.Selector); err != nil {
			return nil, fmt.Errorf("%w: remove delivered message: %v", ErrAdapterContract, err)
		}
		return r.processAdapterEffects(effects, r.time)

	case core.ActionDrop:
		if _, err := r.network.remove(action.Message, *action.Selector); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalidAction, err)
		}
		return nil, nil

	case core.ActionDuplicate:
		if _, err := r.network.duplicate(action.Message, *action.Selector, r.time); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalidAction, err)
		}
		return nil, nil

	case core.ActionAdvanceTime:
		return r.advanceTime(ctx, action.TargetTime)

	case core.ActionTimeout:
		effects, err := r.adapter.ForceTimeout(ctx, r.time, action.Node)
		if err != nil {
			return nil, fmt.Errorf("%w: timeout node %s: %v", ErrAdapter, action.Node, err)
		}
		concrete, err := r.processAdapterEffects(effects, r.time)
		if err != nil {
			return nil, err
		}
		if err := validateForcedTimeoutEffects(concrete, action.Node); err != nil {
			return nil, err
		}
		return concrete, nil

	case core.ActionCrash:
		effects, err := r.adapter.Crash(ctx, r.time, action.Node)
		if err != nil {
			return nil, fmt.Errorf("%w: crash node %s: %v", ErrAdapter, action.Node, err)
		}
		return r.processAdapterEffects(effects, r.time)

	case core.ActionRestart:
		effects, err := r.adapter.Restart(ctx, r.time, action.Node)
		if err != nil {
			return nil, fmt.Errorf("%w: restart node %s: %v", ErrAdapter, action.Node, err)
		}
		return r.processAdapterEffects(effects, r.time)

	case core.ActionRequest:
		effects, err := r.adapter.Request(ctx, r.time, action.Node, append([]byte(nil), action.Request...))
		if err != nil {
			return nil, fmt.Errorf("%w: request node %s: %v", ErrAdapter, action.Node, err)
		}
		return r.processAdapterEffects(effects, r.time)

	default:
		return nil, fmt.Errorf("%w: unknown action kind %q", ErrInvalidAction, action.Kind)
	}
}
