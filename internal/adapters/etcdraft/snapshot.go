package etcdraft

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/core"
	raft "go.etcd.io/raft/v3"
	"go.etcd.io/raft/v3/raftpb"
)

const snapshotDataVersion uint32 = 1

type snapshotState struct {
	Version       uint32   `json:"version"`
	Index         uint64   `json:"index"`
	PrefixDigests []string `json:"prefix_digests"`
}

func initialPrefixDigests() map[uint64]string {
	base := sha256.Sum256([]byte("modelfuzz-ng/raft-log-prefix/v1"))
	return map[uint64]string{0: hex.EncodeToString(base[:])}
}

func (n *node) recordCommitted(entry *raftpb.Entry) error {
	index := entry.GetIndex()
	if index == 0 {
		return fmt.Errorf("committed entry has zero index")
	}
	if index <= n.applied {
		return nil
	}
	if index != n.applied+1 {
		return fmt.Errorf("committed entry index jumped from %d to %d", n.applied, index)
	}
	if !n.snapshotEnabled {
		if n.prefixHash == nil {
			return fmt.Errorf("committed prefix hash state is unavailable")
		}
		if err := writeProtoDigest(n.prefixHash, entry); err != nil {
			return err
		}
		n.prefixDigests[index] = hex.EncodeToString(n.prefixHash.Sum(nil))
		n.applied = index
		return nil
	}
	previous, exists := n.prefixDigests[index-1]
	if !exists {
		return fmt.Errorf("committed prefix digest at index %d is unavailable", index-1)
	}
	previousBytes, err := hex.DecodeString(previous)
	if err != nil {
		return fmt.Errorf("decode committed prefix digest at %d: %w", index-1, err)
	}
	hash := sha256.New()
	if _, err := hash.Write(previousBytes); err != nil {
		return err
	}
	if err := writeProtoDigest(hash, entry); err != nil {
		return err
	}
	n.prefixDigests[index] = hex.EncodeToString(hash.Sum(nil))
	n.applied = index
	return nil
}

func (n *node) snapshotData(index uint64) ([]byte, error) {
	digests := make([]string, index+1)
	for current := uint64(0); current <= index; current++ {
		digest, exists := n.prefixDigests[current]
		if !exists || digest == "" {
			return nil, fmt.Errorf("committed prefix digest at index %d is unavailable", current)
		}
		digests[current] = digest
	}
	return json.Marshal(snapshotState{Version: snapshotDataVersion, Index: index, PrefixDigests: digests})
}

func decodeSnapshotState(snapshot *raftpb.Snapshot) (snapshotState, error) {
	index := snapshot.GetMetadata().GetIndex()
	if index == 0 && len(snapshot.GetData()) == 0 {
		base := initialPrefixDigests()[0]
		return snapshotState{Version: snapshotDataVersion, PrefixDigests: []string{base}}, nil
	}
	var state snapshotState
	if err := json.Unmarshal(snapshot.GetData(), &state); err != nil {
		return snapshotState{}, fmt.Errorf("decode snapshot data: %w", err)
	}
	if state.Version != snapshotDataVersion || state.Index != index || uint64(len(state.PrefixDigests)) != index+1 {
		return snapshotState{}, fmt.Errorf("snapshot data metadata does not match index %d", index)
	}
	for position, digest := range state.PrefixDigests {
		if decoded, err := hex.DecodeString(digest); err != nil || len(decoded) != sha256.Size {
			return snapshotState{}, fmt.Errorf("snapshot prefix digest at %d is invalid", position)
		}
	}
	return state, nil
}

func (n *node) restoreSnapshotPrefix(snapshot *raftpb.Snapshot) error {
	state, err := decodeSnapshotState(snapshot)
	if err != nil {
		return err
	}
	index := snapshot.GetMetadata().GetIndex()
	if local, exists := n.prefixDigests[index]; exists && local != state.PrefixDigests[index] {
		return fmt.Errorf("snapshot committed prefix conflicts at index %d", index)
	}
	restored := make(map[uint64]string, len(state.PrefixDigests))
	for position, digest := range state.PrefixDigests {
		restored[uint64(position)] = digest
	}
	n.prefixDigests = restored
	n.prefixHash = nil
	return nil
}

func (a *Adapter) applyReadySnapshot(n *node, at core.LogicalTime, snapshot *raftpb.Snapshot, emit bool) ([]core.Effect, error) {
	index := snapshot.GetMetadata().GetIndex()
	if index <= n.lastSnapshotIndex || index < n.applied {
		if emit {
			return []core.Effect{snapshotEvent(at, "raft.snapshot_rejected_or_stale", n.id, snapshot, nil)}, nil
		}
		return nil, nil
	}
	if err := n.restoreSnapshotPrefix(snapshot); err != nil {
		return nil, fmt.Errorf("node %s restore snapshot state: %w", n.id, err)
	}
	if err := n.storage.ApplySnapshot(snapshot); err != nil {
		if errors.Is(err, raft.ErrSnapOutOfDate) {
			if emit {
				return []core.Effect{snapshotEvent(at, "raft.snapshot_rejected_or_stale", n.id, snapshot, nil)}, nil
			}
			return nil, nil
		}
		return nil, fmt.Errorf("node %s apply snapshot: %w", n.id, err)
	}
	n.applied = index
	n.lastSnapshotIndex = index
	n.lastSnapshotTerm = snapshot.GetMetadata().GetTerm()
	n.snapshotsApplied++
	n.setConfState(snapshot.GetMetadata().GetConfState())
	if err := n.refreshLogState(); err != nil {
		return nil, fmt.Errorf("node %s refresh snapshot state: %w", n.id, err)
	}
	if !emit {
		return nil, nil
	}
	return []core.Effect{snapshotEvent(at, "raft.snapshot_applied", n.id, snapshot, nil)}, nil
}

func (a *Adapter) maybeSnapshot(n *node, at core.LogicalTime, emit bool) ([]core.Effect, error) {
	policy := a.config.Snapshot
	if policy.Threshold == 0 || n.applied == 0 || n.applied <= n.lastSnapshotIndex ||
		n.applied-n.lastSnapshotIndex < policy.Threshold {
		return nil, nil
	}
	data, err := n.snapshotData(n.applied)
	if err != nil {
		return nil, fmt.Errorf("node %s build snapshot data at %d: %w", n.id, n.applied, err)
	}
	snapshot, err := n.storage.CreateSnapshot(n.applied, n.confState, data)
	if err != nil {
		if errors.Is(err, raft.ErrSnapOutOfDate) {
			return nil, nil
		}
		return nil, fmt.Errorf("node %s create snapshot at %d: %w", n.id, n.applied, err)
	}
	n.lastSnapshotIndex = snapshot.GetMetadata().GetIndex()
	n.lastSnapshotTerm = snapshot.GetMetadata().GetTerm()
	n.snapshotsCreated++
	effects := make([]core.Effect, 0, 2)
	if emit {
		effects = append(effects, snapshotEvent(at, "raft.snapshot_created", n.id, snapshot, nil))
	}

	if n.lastSnapshotIndex > policy.RetainEntries {
		compactIndex := n.lastSnapshotIndex - policy.RetainEntries
		first, err := n.storage.FirstIndex()
		if err != nil {
			return nil, fmt.Errorf("node %s read first index before compact: %w", n.id, err)
		}
		if compactIndex >= first {
			compacted := compactIndex - first + 1
			if err := n.storage.Compact(compactIndex); err != nil && !errors.Is(err, raft.ErrCompacted) {
				return nil, fmt.Errorf("node %s compact at %d: %w", n.id, compactIndex, err)
			}
			n.logsCompacted++
			n.compactedEntries += compacted
			if emit {
				effects = append(effects, snapshotEvent(at, "raft.log_compacted", n.id, snapshot, map[string]any{
					"compact_index": compactIndex, "compacted_entries": compacted,
				}))
			}
		}
	}
	if err := n.refreshLogState(); err != nil {
		return nil, fmt.Errorf("node %s refresh compacted log: %w", n.id, err)
	}
	return effects, nil
}

func snapshotEvent(
	at core.LogicalTime, name string, node core.NodeID, snapshot *raftpb.Snapshot, extra map[string]any,
) core.Effect {
	params := map[string]any{
		"index":          snapshot.GetMetadata().GetIndex(),
		"term":           snapshot.GetMetadata().GetTerm(),
		"snapshot_bytes": len(snapshot.GetData()),
	}
	for key, value := range extra {
		params[key] = value
	}
	return modelEffect(at, name, node, params)
}
