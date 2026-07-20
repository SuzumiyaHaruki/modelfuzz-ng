package etcdraft

import (
	"context"
	"fmt"

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/core"
	"go.etcd.io/raft/v3/raftpb"
)

// resetCluster 初始化一组内存中的 etcd-raft RawNode，作为一次执行的初始状态。
func (a *Adapter) resetCluster(ctx context.Context) error {
	voters := make([]uint64, len(a.config.NodeIDs))
	for i, id := range a.config.NodeIDs {
		voters[i] = uint64(id)
	}
	confState := &raftpb.ConfState{Voters: voters}

	nodes := make(map[core.NodeID]*node, len(a.config.NodeIDs))
	for _, id := range a.config.NodeIDs {
		if err := ctx.Err(); err != nil {
			return err
		}
		n, err := newNode(a.config, id, confState, newNodeRand(a.seed, id, 1))
		if err != nil {
			return err
		}
		nodes[id] = n
	}
	a.nodes = nodes
	a.reset = true
	return nil
}

func (a *Adapter) node(id core.NodeID) (*node, error) {
	if !a.reset {
		return nil, ErrNotReset
	}
	n, exists := a.nodes[id]
	if !exists {
		return nil, fmt.Errorf("%w: %s", ErrUnknownNode, id)
	}
	return n, nil
}

func requireRunning(n *node) error {
	if !n.running || n.raw == nil {
		return fmt.Errorf("%w: node %s is crashed", ErrNodeState, n.id)
	}
	return nil
}
