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
		semantic := make(map[string]any)
		status := core.NodeCrashed
		if n.running {
			running++
			status = core.NodeRunning
			raftStatus := n.raw.BasicStatus()
			semantic = map[string]any{
				"role":       roleName(raftStatus.RaftState),
				"term":       raftStatus.GetTerm(),
				"vote":       raftStatus.GetVote(),
				"leader":     raftStatus.Lead,
				"commit":     raftStatus.GetCommit(),
				"applied":    raftStatus.Applied,
				"last_index": n.lastIndex,
				"last_term":  n.lastTerm,
				"log_digest": n.logDigest,
			}
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
		}
		digest, err := semanticDigest(semantic)
		if err != nil {
			return core.Observation{}, fmt.Errorf("digest node %s observation: %w", id, err)
		}
		nodes = append(nodes, core.NodeObservation{
			ID: id, Epoch: n.epoch, Status: status, Digest: digest, Semantic: semantic,
		})
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

func semanticDigest(value any) (string, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", sha256.Sum256(data)), nil
}
