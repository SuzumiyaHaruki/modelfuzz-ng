package etcdraft

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/core"
)

// observation 返回当前逻辑时间下的集群状态快照，包含每个节点的语义信息和摘要。
func (a *Adapter) observation(at core.LogicalTime) (core.Observation, error) {
	nodes := make([]core.NodeObservation, 0, len(a.config.NodeIDs))
	running := 0
	for _, id := range a.config.NodeIDs {
		n := a.nodes[id]
		var semantic map[string]any
		status := core.NodeCrashed
		if n.running {
			running++
			status = core.NodeRunning
			raftStatus := n.raw.BasicStatus()
			semantic = map[string]any{
				"role":                        roleName(raftStatus.RaftState),
				"term":                        raftStatus.GetTerm(),
				"vote":                        raftStatus.GetVote(),
				"leader":                      raftStatus.Lead,
				"commit":                      raftStatus.GetCommit(),
				"applied":                     raftStatus.Applied,
				"last_index":                  n.lastIndex,
				"last_term":                   n.lastTerm,
				"log_digest":                  n.logDigest,
				"election_elapsed":            raftStatus.ElectionElapsed,
				"election_timeout":            raftStatus.ElectionTimeout,
				"randomized_election_timeout": raftStatus.RandomizedElectionTimeout,
				"election_ticks_remaining":    ticksRemaining(raftStatus.RandomizedElectionTimeout, raftStatus.ElectionElapsed),
				"heartbeat_elapsed":           raftStatus.HeartbeatElapsed,
				"heartbeat_timeout":           raftStatus.HeartbeatTimeout,
				"heartbeat_ticks_remaining":   ticksRemaining(raftStatus.HeartbeatTimeout, raftStatus.HeartbeatElapsed),
			}
			addSnapshotObservation(a.config.Snapshot, n, semantic)
		} else {
			hardState, _, err := n.storage.InitialState()
			if err != nil {
				return core.Observation{}, fmt.Errorf("observe crashed node %s: %w", id, err)
			}
			semantic = map[string]any{
				"role":       "crashed",
				"term":       hardState.GetTerm(),
				"vote":       hardState.GetVote(),
				"leader":     uint64(0),
				"commit":     hardState.GetCommit(),
				"applied":    n.applied,
				"last_index": n.lastIndex,
				"last_term":  n.lastTerm,
				"log_digest": n.logDigest,
			}
			addSnapshotObservation(a.config.Snapshot, n, semantic)
		}
		nodes = append(nodes, core.NodeObservation{
			ID: id, Epoch: n.epoch, Status: status, Semantic: semantic,
		})
	}

	// 两个节点需比较的索引一定是它们当前 commit 的较小值，因此只需
	// 暴露当前集群中出现的 commit 索引，无需把 1..commit 全部写入轨迹。
	checkpoints := committedCheckpoints(nodes)
	for index := range nodes {
		n := a.nodes[nodes[index].ID]
		commit, _ := nodes[index].Semantic["commit"].(uint64)
		prefixes, available := n.committedPrefixDigests(commit, checkpoints)
		nodes[index].Semantic["committed_prefix_available"] = available
		if available {
			nodes[index].Semantic["committed_prefix_digests"] = prefixes
		}
		digest, err := semanticDigest(nodes[index].Semantic)
		if err != nil {
			return core.Observation{}, fmt.Errorf("digest node %s observation: %w", nodes[index].ID, err)
		}
		nodes[index].Digest = digest
	}
	return core.Observation{
		Time:  at,
		Nodes: nodes,
		Semantic: map[string]any{
			"adapter":       "etcdraft-v3.7",
			"cluster_size":  len(a.config.NodeIDs),
			"running_nodes": running,
		},
	}, nil
}

func addSnapshotObservation(policy SnapshotPolicy, n *node, semantic map[string]any) {
	if policy.Threshold == 0 {
		return
	}
	semantic["first_index"] = n.firstIndex
	semantic["snapshot_index"] = n.lastSnapshotIndex
	semantic["snapshot_term"] = n.lastSnapshotTerm
	semantic["snapshots_created"] = n.snapshotsCreated
	semantic["snapshots_applied"] = n.snapshotsApplied
	semantic["logs_compacted"] = n.logsCompacted
	semantic["compacted_entries"] = n.compactedEntries
}

func ticksRemaining(timeout, elapsed int) int {
	if timeout <= elapsed {
		return 0
	}
	return timeout - elapsed
}

func committedCheckpoints(nodes []core.NodeObservation) []uint64 {
	seen := make(map[uint64]struct{})
	result := make([]uint64, 0, len(nodes))
	for _, node := range nodes {
		commit, _ := node.Semantic["commit"].(uint64)
		if commit == 0 {
			continue
		}
		if _, exists := seen[commit]; exists {
			continue
		}
		seen[commit] = struct{}{}
		result = append(result, commit)
	}
	return result
}

func semanticDigest(value any) (string, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", sha256.Sum256(data)), nil
}
