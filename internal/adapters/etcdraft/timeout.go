package etcdraft

import (
	"strconv"

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/core"
	raft "go.etcd.io/raft/v3"
)

func electionTimeoutEffect(at core.LogicalTime, n *node, source core.TimerFireSource, before, after raft.BasicStatus) core.Effect {
	return core.Effect{
		At:   at,
		Kind: core.EffectTimerFired,
		TimerFired: &core.TimerFired{
			Node:     n.id,
			Epoch:    n.epoch,
			Source:   source,
			TypeHint: "election",
			RoleHint: roleName(before.RaftState),
			Metadata: map[string]string{
				"term_before": strconv.FormatUint(before.GetTerm(), 10),
				"term_after":  strconv.FormatUint(after.GetTerm(), 10),
				"role_after":  roleName(after.RaftState),
			},
		},
	}
}

func heartbeatTimeoutEffect(at core.LogicalTime, n *node, status raft.BasicStatus) core.Effect {
	return core.Effect{
		At:   at,
		Kind: core.EffectTimerFired,
		TimerFired: &core.TimerFired{
			Node:     n.id,
			Epoch:    n.epoch,
			Source:   core.TimerFireNatural,
			TypeHint: "heartbeat",
			RoleHint: "leader",
			Metadata: map[string]string{
				"term": strconv.FormatUint(status.GetTerm(), 10),
			},
		},
	}
}

func naturalElectionFired(before, after raft.BasicStatus) bool {
	if before.RaftState == raft.StateLeader {
		return false
	}
	return after.GetTerm() > before.GetTerm() &&
		(after.RaftState == raft.StateCandidate || after.RaftState == raft.StateLeader)
}

func containsHeartbeat(effects []core.Effect, from core.NodeID) bool {
	for _, effect := range effects {
		if effect.Kind == core.EffectSendMessage && effect.Message != nil &&
			effect.Message.From == from && effect.Message.TypeHint == "MsgHeartbeat" {
			return true
		}
	}
	return false
}

func roleName(role raft.StateType) string {
	switch role {
	case raft.StateFollower:
		return "follower"
	case raft.StateCandidate:
		return "candidate"
	case raft.StateLeader:
		return "leader"
	case raft.StatePreCandidate:
		return "pre_candidate"
	default:
		return "unknown"
	}
}
