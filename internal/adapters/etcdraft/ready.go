package etcdraft

import (
	"crypto/sha256"
	"fmt"

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/core"
	raft "go.etcd.io/raft/v3"
	"go.etcd.io/raft/v3/raftpb"
	"google.golang.org/protobuf/proto"
)

const maxReadyRounds = 1024

// drainReady 立即完成一次操作产生的全部 Ready 工作。这样 Step、Tick、
// Campaign 和 Propose 的输出都会归属于触发它们的同一条 Action。
func (a *Adapter) drainReady(n *node, at core.LogicalTime, emit bool) ([]core.Effect, error) {
	return a.drainReadyForwarded(n, at, emit, nil)
}

func (a *Adapter) drainReadyForwarded(
	n *node, at core.LogicalTime, emit bool, forwarded *core.Message,
) ([]core.Effect, error) {
	effects := make([]core.Effect, 0)
	for round := 0; n.raw.HasReady(); round++ {
		if round >= maxReadyRounds {
			return nil, fmt.Errorf("ready processing exceeded %d rounds", maxReadyRounds)
		}
		rd := n.raw.Ready()
		current, err := a.handleReady(n, at, rd, emit, forwarded)
		if err != nil {
			return nil, err
		}
		effects = append(effects, current...)
		n.raw.Advance(rd)
	}
	return effects, nil
}

func (a *Adapter) handleReady(
	n *node, at core.LogicalTime, rd raft.Ready, emit bool, forwarded *core.Message,
) ([]core.Effect, error) {
	effects := make([]core.Effect, 0, len(rd.CommittedEntries)+len(rd.Messages))
	logChanged := false
	if !raft.IsEmptySnap(rd.Snapshot) {
		current, err := a.applyReadySnapshot(n, at, rd.Snapshot, emit)
		if err != nil {
			return nil, err
		}
		effects = append(effects, current...)
	}
	if !raft.IsEmptyHardState(rd.HardState) {
		if err := n.storage.SetHardState(proto.Clone(rd.HardState).(*raftpb.HardState)); err != nil {
			return nil, fmt.Errorf("node %s persist hard state: %w", n.id, err)
		}
	}
	if err := n.storage.Append(cloneEntries(rd.Entries)); err != nil {
		return nil, fmt.Errorf("node %s append entries: %w", n.id, err)
	}
	if len(rd.Entries) != 0 {
		logChanged = true
	}
	if logChanged {
		if err := n.refreshLogState(); err != nil {
			return nil, fmt.Errorf("node %s refresh log state: %w", n.id, err)
		}
	}

	for _, entry := range rd.CommittedEntries {
		current, err := a.applyCommittedEntry(n, at, entry, emit)
		if err != nil {
			return nil, err
		}
		effects = append(effects, current...)
	}
	snapshotEffects, err := a.maybeSnapshot(n, at, emit)
	if err != nil {
		return nil, err
	}
	effects = append(effects, snapshotEffects...)
	for _, message := range rd.Messages {
		outbound, err := a.outboundMessage(message, forwarded)
		if err != nil {
			return nil, fmt.Errorf("node %s convert outbound message: %w", n.id, err)
		}
		if !emit {
			return nil, fmt.Errorf("node %s produced message %s during reset", n.id, outbound.TypeHint)
		}
		effects = append(effects, core.Effect{At: at, Kind: core.EffectSendMessage, Message: &outbound})
		if message.GetType() == raftpb.MsgSnap {
			progress, exists := n.raw.Status().Progress[message.GetTo()]
			if !exists {
				return nil, fmt.Errorf("node %s sent snapshot to untracked node n%d", n.id, message.GetTo())
			}
			effects = append(effects, snapshotEvent(at, "raft.snapshot_sent", n.id, message.GetSnapshot(), map[string]any{
				"to":               message.GetTo(),
				"match_index":      progress.Match,
				"next_index":       progress.Next,
				"pending_snapshot": progress.PendingSnapshot,
				"progress_state":   progress.State.String(),
			}))
		}
	}
	return effects, nil
}

func (a *Adapter) applyCommittedEntry(n *node, at core.LogicalTime, entry *raftpb.Entry, emit bool) ([]core.Effect, error) {
	if entry == nil {
		return nil, fmt.Errorf("node %s received nil committed entry", n.id)
	}
	if err := n.recordCommitted(entry); err != nil {
		return nil, fmt.Errorf("node %s record committed entry at %d: %w", n.id, entry.GetIndex(), err)
	}
	name := "raft.entry_committed"
	params := map[string]any{
		"index":       entry.GetIndex(),
		"term":        entry.GetTerm(),
		"entry_type":  entry.GetType().String(),
		"data_size":   len(entry.GetData()),
		"data_digest": fmt.Sprintf("%x", sha256.Sum256(entry.GetData())),
	}

	switch entry.GetType() {
	case raftpb.EntryConfChange:
		change := &raftpb.ConfChange{}
		if err := proto.Unmarshal(entry.GetData(), change); err != nil {
			return nil, fmt.Errorf("node %s decode conf change at %d: %w", n.id, entry.GetIndex(), err)
		}
		n.setConfState(n.raw.ApplyConfChange(change))
		name = "raft.config_changed"
	case raftpb.EntryConfChangeV2:
		change := &raftpb.ConfChangeV2{}
		if err := proto.Unmarshal(entry.GetData(), change); err != nil {
			return nil, fmt.Errorf("node %s decode conf change v2 at %d: %w", n.id, entry.GetIndex(), err)
		}
		n.setConfState(n.raw.ApplyConfChange(change))
		name = "raft.config_changed"
	}

	// MemoryStorage 的 InitialState 从 snapshot 读取 ConfState。每次应用成员
	// 变更后创建轻量内存 snapshot，保证节点崩溃重建时仍能恢复成员配置。
	if entry.GetType() == raftpb.EntryConfChange || entry.GetType() == raftpb.EntryConfChangeV2 {
		data, err := n.snapshotData(n.applied)
		if err != nil {
			return nil, fmt.Errorf("node %s build conf snapshot at %d: %w", n.id, n.applied, err)
		}
		snapshot, err := n.storage.CreateSnapshot(n.applied, n.confState, data)
		if err != nil {
			return nil, fmt.Errorf("node %s persist conf state at %d: %w", n.id, n.applied, err)
		}
		n.lastSnapshotIndex = snapshot.GetMetadata().GetIndex()
		n.lastSnapshotTerm = snapshot.GetMetadata().GetTerm()
		if err := n.refreshLogState(); err != nil {
			return nil, fmt.Errorf("node %s refresh snapshot state at %d: %w", n.id, n.applied, err)
		}
	}
	if !emit {
		return nil, nil
	}
	return []core.Effect{modelEffect(at, name, n.id, params)}, nil
}

func cloneEntries(entries []*raftpb.Entry) []*raftpb.Entry {
	result := make([]*raftpb.Entry, len(entries))
	for i, entry := range entries {
		if entry != nil {
			result[i] = proto.Clone(entry).(*raftpb.Entry)
		}
	}
	return result
}

func modelEffect(at core.LogicalTime, name string, node core.NodeID, params map[string]any) core.Effect {
	return core.Effect{
		At:   at,
		Kind: core.EffectModelEvent,
		ModelEvent: &core.ModelEvent{
			Name: name, Node: node, Params: params,
		},
	}
}
