// Package etcdraft 实现对 etcd-raft v3.7 的内存被测系统适配。
package etcdraft

import (
	"context"
	"fmt"

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/core"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/sut"
	raft "go.etcd.io/raft/v3"
)

// Adapter 驱动一组内存中的 etcd-raft RawNode。逻辑时钟、网络队列和
// MessageID 均由上层 Runtime 管理。
type Adapter struct {
	config Config
	nodes  map[core.NodeID]*node
	seed   int64
	reset  bool
}

var _ sut.Adapter = (*Adapter)(nil)

func New(config Config) (*Adapter, error) {
	normalized, err := config.normalized()
	if err != nil {
		return nil, err
	}
	return &Adapter{config: normalized}, nil
}

func (a *Adapter) Capabilities() sut.Capabilities {
	return sut.Capabilities{ForceTimeout: true, CrashRestart: true, ClientRequest: true}
}

func (a *Adapter) Reset(ctx context.Context, options sut.ResetOptions) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	a.seed = options.Seed
	a.reset = false
	a.nodes = nil
	return a.resetCluster(ctx)
}

// Tick 将一个逻辑时间单位映射为每个存活节点各一次 RawNode.Tick。
// 节点按 ID 排序，保证同一轮内 Effect 顺序稳定。
func (a *Adapter) Tick(ctx context.Context, at core.LogicalTime) ([]core.Effect, error) {
	if !a.reset {
		return nil, ErrNotReset
	}
	effects := make([]core.Effect, 0)
	for _, id := range a.config.NodeIDs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		n := a.nodes[id]
		if !n.running {
			continue
		}
		before := n.raw.BasicStatus()
		n.raw.Tick()
		current, err := a.drainReady(n, at, true)
		if err != nil {
			return nil, fmt.Errorf("tick node %s: %w", id, err)
		}
		after := n.raw.BasicStatus()
		if naturalElectionFired(before, after) {
			current = append([]core.Effect{electionTimeoutEffect(at, n, core.TimerFireNatural, before, after)}, current...)
		} else if before.RaftState == raft.StateLeader && containsHeartbeat(current, id) {
			current = append([]core.Effect{heartbeatTimeoutEffect(at, n, after)}, current...)
		}
		effects = append(effects, current...)
	}
	return effects, nil
}

func (a *Adapter) Deliver(ctx context.Context, at core.LogicalTime, message core.Message) ([]core.Effect, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	n, err := a.node(message.To)
	if err != nil {
		return nil, err
	}
	if err := requireRunning(n); err != nil {
		return nil, err
	}
	payload, err := decodeMessage(message)
	if err != nil {
		return nil, err
	}
	if err := n.raw.Step(payload); err != nil {
		return nil, fmt.Errorf("step node %s with %s: %w", n.id, message.TypeHint, err)
	}
	effects, err := a.drainReady(n, at, true)
	if err != nil {
		return nil, err
	}
	// Action 中只有稳定的 MessageID。把实际交给 Raft 的消息语义记录下来，
	// 后续模型映射即使只读取序列化后的 StepRecord，也不需要依赖 Payload。
	return append([]core.Effect{deliveredMessageEffect(at, payload)}, effects...), nil
}

func (a *Adapter) ForceTimeout(ctx context.Context, at core.LogicalTime, id core.NodeID) ([]core.Effect, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	n, err := a.node(id)
	if err != nil {
		return nil, err
	}
	if err := requireRunning(n); err != nil {
		return nil, err
	}
	before := n.raw.BasicStatus()
	if err := n.raw.Campaign(); err != nil {
		return nil, fmt.Errorf("campaign node %s: %w", id, err)
	}
	effects, err := a.drainReady(n, at, true)
	if err != nil {
		return nil, err
	}
	after := n.raw.BasicStatus()
	return append([]core.Effect{electionTimeoutEffect(at, n, core.TimerFireForced, before, after)}, effects...), nil
}

func (a *Adapter) Crash(ctx context.Context, _ core.LogicalTime, id core.NodeID) ([]core.Effect, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	n, err := a.node(id)
	if err != nil {
		return nil, err
	}
	if err := requireRunning(n); err != nil {
		return nil, err
	}
	n.crash()
	return nil, nil
}

func (a *Adapter) Restart(ctx context.Context, at core.LogicalTime, id core.NodeID) ([]core.Effect, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	n, err := a.node(id)
	if err != nil {
		return nil, err
	}
	if n.running {
		return nil, fmt.Errorf("%w: node %s is already running", ErrNodeState, id)
	}
	nextEpoch := n.epoch + 1
	if err := n.restart(a.config, newNodeRand(a.seed, n.id, nextEpoch)); err != nil {
		return nil, fmt.Errorf("restart node %s: %w", id, err)
	}
	return a.drainReady(n, at, true)
}

func (a *Adapter) Request(ctx context.Context, at core.LogicalTime, id core.NodeID, request []byte) ([]core.Effect, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	n, err := a.node(id)
	if err != nil {
		return nil, err
	}
	if err := requireRunning(n); err != nil {
		return nil, err
	}
	if len(request) == 0 {
		return nil, fmt.Errorf("request must not be empty")
	}
	if err := n.raw.Propose(append([]byte(nil), request...)); err != nil {
		return nil, fmt.Errorf("propose on node %s: %w", id, err)
	}
	return a.drainReady(n, at, true)
}

func (a *Adapter) Observe(ctx context.Context, at core.LogicalTime) (core.Observation, error) {
	if err := ctx.Err(); err != nil {
		return core.Observation{}, err
	}
	if !a.reset {
		return core.Observation{}, ErrNotReset
	}
	return a.observation(at)
}
