// Package etcdraft 实现对 etcd-raft v3.7 的内存被测系统适配。
package etcdraft

import (
	"context"
	"errors"
	"fmt"

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/core"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/sut"
	raft "go.etcd.io/raft/v3"
	"go.etcd.io/raft/v3/raftpb"
)

const proposalDroppedEvent = "raft.proposal_dropped"
const voteQuorumFaultEvent = "raft.vote_quorum_fault_activated"
const snapshotStatusMappingFaultEvent = "raft.snapshot_status_mapping_fault_activated"
const restartHardStateFaultEvent = "raft.restart_hard_state_fault_activated"

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
	deliveryEffects := []core.Effect{deliveredMessageEffect(at, payload)}
	if payload.GetType() == raftpb.MsgSnap {
		snapshot := payload.GetSnapshot()
		deliveryEffects = append(deliveryEffects, snapshotEvent(at, "raft.snapshot_delivered", n.id, snapshot, nil))
		if snapshot.GetMetadata().GetIndex() <= n.applied {
			deliveryEffects = append(deliveryEffects,
				snapshotEvent(at, "raft.snapshot_rejected_or_stale", n.id, snapshot, nil))
		}
	}
	before := n.raw.BasicStatus()
	if err := n.raw.Step(payload); err != nil {
		if errors.Is(err, raft.ErrProposalDropped) && payload.GetType() == raftpb.MsgProp {
			// follower 转发 proposal 后，目标节点可能已经失去 leader 身份，甚至
			// 当前集群暂时没有 leader。etcd-raft 把这种时序结果表示为
			// ErrProposalDropped；它不是 SUT 崩溃，也不应终止整条 fuzz 轨迹。
			status := n.raw.BasicStatus()
			return append(deliveryEffects, proposalDropEffect(at, n.id, status, "forwarded")), nil
		}
		return nil, fmt.Errorf("step node %s with %s: %w", n.id, message.TypeHint, err)
	}
	faultEffects, err := a.maybeActivateVoteQuorumFault(n, at, before, payload)
	if err != nil {
		return nil, err
	}
	effects, err := a.drainReadyForwarded(n, at, true, &message)
	if err != nil {
		return nil, err
	}
	if payload.GetType() == raftpb.MsgSnap &&
		!hasSnapshotDeliveryOutcome(deliveryEffects, effects) {
		snapshot := payload.GetSnapshot()
		index := snapshot.GetMetadata().GetIndex()
		after := n.raw.BasicStatus()
		if before.GetCommit() < index && after.GetCommit() >= index {
			deliveryEffects = append(deliveryEffects, snapshotEvent(
				at, "raft.snapshot_fast_forwarded", n.id, snapshot, map[string]any{
					"commit_before": before.GetCommit(), "commit_after": after.GetCommit(),
				},
			))
		} else {
			reason := "ignored"
			switch {
			case payload.GetTerm() < before.GetTerm():
				reason = "lower_term"
			case index <= before.GetCommit() || index <= n.applied:
				reason = "not_ahead_of_local_progress"
			}
			deliveryEffects = append(deliveryEffects, snapshotEvent(
				at, "raft.snapshot_rejected_or_stale", n.id, snapshot,
				map[string]any{"reason": reason},
			))
		}
	}
	// Action 中只有稳定的 MessageID。把实际交给 Raft 的消息语义记录下来，
	// 后续模型映射即使只读取序列化后的 StepRecord，也不需要依赖 Payload。
	deliveryEffects = append(deliveryEffects, faultEffects...)
	result := append(deliveryEffects, effects...)
	if payload.GetType() == raftpb.MsgSnap {
		statusEffects, err := a.reportSnapshotStatus(at, message.From, message.To, raft.SnapshotFinish)
		if err != nil {
			return nil, err
		}
		result = append(result, statusEffects...)
	}
	return result, nil
}

// Drop reports application-layer snapshot transport failure back to the sender.
// Other Raft messages need no local feedback when the controlled network drops them.
func (a *Adapter) Drop(ctx context.Context, at core.LogicalTime, message core.Message) ([]core.Effect, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	payload, err := decodeMessage(message)
	if err != nil {
		return nil, err
	}
	if payload.GetType() != raftpb.MsgSnap {
		return nil, nil
	}
	return a.reportSnapshotStatus(at, message.From, message.To, raft.SnapshotFailure)
}

func (a *Adapter) reportSnapshotStatus(
	at core.LogicalTime, leaderID, followerID core.NodeID, status raft.SnapshotStatus,
) ([]core.Effect, error) {
	leader, err := a.node(leaderID)
	if err != nil {
		return nil, err
	}
	actualReject := status == raft.SnapshotFailure
	reportedReject := actualReject
	mappingFault := a.config.Faults.SnapshotStatusMap == SnapshotStatusMappingInvert
	if mappingFault {
		reportedReject = !reportedReject
	}
	params := map[string]any{
		"from": uint64(followerID), "to": uint64(leaderID), "reject": reportedReject,
		"handled": false, "pending_before": uint64(0), "pending_after": uint64(0),
		"match_index": uint64(0), "next_before": uint64(0), "next_after": uint64(0),
	}
	statusEffects := func() []core.Effect {
		effects := []core.Effect{modelEffect(at, "raft.snapshot_status_reported", leaderID, params)}
		if mappingFault {
			effects = append(effects, modelEffect(at, snapshotStatusMappingFaultEvent, leaderID, map[string]any{
				"from": uint64(followerID), "actual_reject": actualReject, "reported_reject": reportedReject,
			}))
		}
		return effects
	}
	if !leader.running || leader.raw == nil {
		params["reason"] = "sender_not_running"
		return statusEffects(), nil
	}
	before, exists := leader.raw.Status().Progress[uint64(followerID)]
	if exists {
		params["pending_before"] = before.PendingSnapshot
		params["match_index"] = before.Match
		params["next_before"] = before.Next
		params["progress_before"] = before.State.String()
	}
	leader.raw.ReportSnapshot(uint64(followerID), status)
	effects, err := a.drainReady(leader, at, true)
	if err != nil {
		return nil, fmt.Errorf("node %s report snapshot status for %s: %w", leaderID, followerID, err)
	}
	after, stillTracked := leader.raw.Status().Progress[uint64(followerID)]
	if stillTracked {
		params["pending_after"] = after.PendingSnapshot
		params["next_after"] = after.Next
		params["progress_after"] = after.State.String()
	}
	handled := exists && stillTracked && before.State.String() == "StateSnapshot" && before.PendingSnapshot > 0
	params["handled"] = handled
	if !exists || !stillTracked {
		params["reason"] = "peer_not_tracked"
	} else if !handled {
		params["reason"] = "not_pending_snapshot"
	}
	return append(statusEffects(), effects...), nil
}

func hasSnapshotDeliveryOutcome(groups ...[]core.Effect) bool {
	for _, effects := range groups {
		for _, effect := range effects {
			if effect.Kind != core.EffectModelEvent || effect.ModelEvent == nil {
				continue
			}
			switch effect.ModelEvent.Name {
			case "raft.snapshot_applied", "raft.snapshot_fast_forwarded",
				"raft.snapshot_rejected_or_stale":
				return true
			}
		}
	}
	return false
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
	var lostHardState *raftpb.HardState
	if a.config.Faults.RestartLoseHardState {
		hardState, _, err := n.storage.InitialState()
		if err != nil {
			return nil, fmt.Errorf("read node %s hard state before restart fault: %w", id, err)
		}
		lostHardState = hardState
		if err := n.storage.SetHardState(&raftpb.HardState{}); err != nil {
			return nil, fmt.Errorf("clear node %s hard state for restart fault: %w", id, err)
		}
	}
	nextEpoch := n.epoch + 1
	if err := n.restart(a.config, newNodeRand(a.seed, n.id, nextEpoch)); err != nil {
		return nil, fmt.Errorf("restart node %s: %w", id, err)
	}
	effects, err := a.drainReady(n, at, true)
	if err != nil {
		return nil, err
	}
	if lostHardState != nil {
		effects = append([]core.Effect{modelEffect(at, restartHardStateFaultEvent, id, map[string]any{
			"term": lostHardState.GetTerm(), "vote": lostHardState.GetVote(), "commit": lostHardState.GetCommit(),
		})}, effects...)
	}
	return effects, nil
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
		if errors.Is(err, raft.ErrProposalDropped) {
			status := n.raw.BasicStatus()
			return []core.Effect{proposalDropEffect(at, id, status, "direct")}, nil
		}
		return nil, fmt.Errorf("propose on node %s: %w", id, err)
	}
	return a.drainReady(n, at, true)
}

func proposalDropEffect(at core.LogicalTime, id core.NodeID, status raft.BasicStatus, source string) core.Effect {
	return modelEffect(at, proposalDroppedEvent, id, map[string]any{
		"role": roleName(status.RaftState), "leader": status.Lead, "source": source,
	})
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
