// Package runtime 负责执行 Concrete Action，并把 Adapter 产生的结果具体化为
// 可重放的 Effect、Observation 和 Trace。
package runtime

import (
	"context"
	"errors"
	"fmt"

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/core"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/sut"
)

var (
	ErrNotInitialized     = errors.New("runtime is not initialized")
	ErrTerminated         = errors.New("runtime execution is terminated")
	ErrInvalidConfig      = errors.New("invalid runtime config")
	ErrInvalidAction      = errors.New("invalid concrete action")
	ErrUnsupportedAction  = errors.New("adapter does not support action")
	ErrMessageUnavailable = errors.New("message is unavailable")
	ErrAdapter            = errors.New("adapter operation failed")
	ErrAdapterContract    = errors.New("adapter contract violation")
	ErrIDExhausted        = errors.New("runtime ID exhausted")
	ErrBudgetExceeded     = errors.New("runtime budget exceeded")
)

// Limits 是一次执行的硬安全边界。零表示该项不设上限。Plan Resolver 的
// 单步限制用于改善输入质量，Runtime Limits 则防止任意调用方绕过 Plan 后
// 造成无界 tick、Effect 或消息队列增长。
type Limits struct {
	MaxActions        uint64 `json:"max_actions"`
	MaxTicks          uint64 `json:"max_ticks"`
	MaxEffects        uint64 `json:"max_effects"`
	MaxQueuedMessages int    `json:"max_queued_messages"`
}

// Config 保存一次可重放执行的稳定输入。
type Config struct {
	ExecutionID core.ExecutionID
	Seed        int64
	Limits      Limits
}

func (c Config) validate() error {
	if !c.ExecutionID.Valid() {
		return fmt.Errorf("%w: execution ID must not be empty", ErrInvalidConfig)
	}
	if c.Limits.MaxQueuedMessages < 0 {
		return fmt.Errorf("%w: MaxQueuedMessages must not be negative", ErrInvalidConfig)
	}
	return nil
}

// StepResult 同时返回具体步骤及其执行前后的全局可观察状态。Concrete Trace v2
// 会在 Record 中保存前后节点快照；完整 Observation 仍主要供在线 Plan、模型映射
// 和 Oracle 使用，避免每一步重复保存整个消息队列。
type StepResult struct {
	Record            core.StepRecord
	BeforeObservation core.Observation
	Observation       core.Observation
}

// Runtime 持有一次执行的逻辑时钟、确定性网络和 Concrete Trace。
// Runtime 是单线程执行器；调用方不应并发调用 Reset、Execute 或读取方法。
type Runtime struct {
	adapter sut.Adapter
	config  Config
	network *network

	time        core.LogicalTime
	trace       *core.Trace
	lastAction  *core.Action
	observation core.Observation
	actionCount uint64
	effectCount uint64
	initialized bool
	terminated  bool
}

func New(adapter sut.Adapter, config Config) (*Runtime, error) {
	if adapter == nil {
		return nil, fmt.Errorf("%w: adapter is nil", ErrAdapterContract)
	}
	if err := config.validate(); err != nil {
		return nil, err
	}
	return &Runtime{
		adapter: adapter,
		config:  config,
		network: newNetwork(),
	}, nil
}

// Reset 开始一次全新执行。Reset 不产生 Effect，初始状态以 Observation 返回。
func (r *Runtime) Reset(ctx context.Context) (core.Observation, error) {
	if err := r.adapter.Reset(ctx, sut.ResetOptions{Seed: r.config.Seed}); err != nil {
		if r.initialized {
			r.terminated = true
		}
		return core.Observation{}, fmt.Errorf("%w: reset: %v", ErrAdapter, err)
	}

	r.network.reset()
	r.time = 0
	r.trace = core.NewTrace(r.config.ExecutionID, r.config.Seed)
	r.lastAction = nil
	r.actionCount = 0
	r.effectCount = 0
	r.initialized = true
	r.terminated = false

	observation, err := r.collectObservation(ctx)
	if err != nil {
		r.initialized = false
		return core.Observation{}, err
	}
	r.observation = observation.Copy()
	return observation.Copy(), nil
}

func (r *Runtime) Time() core.LogicalTime {
	return r.time
}

func (r *Runtime) CurrentObservation() (core.Observation, error) {
	if !r.initialized {
		return core.Observation{}, ErrNotInitialized
	}
	if r.terminated {
		return core.Observation{}, ErrTerminated
	}
	return r.observation.Copy(), nil
}

func (r *Runtime) Trace() (core.Trace, error) {
	if !r.initialized {
		return core.Trace{}, ErrNotInitialized
	}
	return r.trace.Copy(), nil
}

// collectObservation 调用 Adapter.Observe 并将 Runtime 的网络状态和上次执行的 Action 注入到 Observation 中。
func (r *Runtime) collectObservation(ctx context.Context) (core.Observation, error) {
	observation, err := r.adapter.Observe(ctx, r.time)
	if err != nil {
		return core.Observation{}, fmt.Errorf("%w: observe: %v", ErrAdapter, err)
	}
	if observation.Time != r.time {
		return core.Observation{}, fmt.Errorf(
			"%w: observation time is %d, want %d",
			ErrAdapterContract, observation.Time, r.time,
		)
	}
	if len(observation.Messages) != 0 {
		return core.Observation{}, fmt.Errorf(
			"%w: adapter observation must not contain runtime-owned messages",
			ErrAdapterContract,
		)
	}
	if observation.LastAction != nil {
		return core.Observation{}, fmt.Errorf(
			"%w: adapter observation must not contain runtime-owned last action",
			ErrAdapterContract,
		)
	}

	observation.Messages = r.network.observations()
	if r.lastAction != nil {
		action := r.lastAction.Copy()
		observation.LastAction = &action
	}
	observation = observation.Normalized()
	if err := observation.Validate(); err != nil {
		return core.Observation{}, fmt.Errorf("%w: invalid observation: %v", ErrAdapterContract, err)
	}
	return observation, nil
}

func (r *Runtime) nodeStatus(id core.NodeID) (core.NodeStatus, bool) {
	for _, node := range r.observation.Nodes {
		if node.ID == id {
			return node.Status, true
		}
	}
	return "", false
}
