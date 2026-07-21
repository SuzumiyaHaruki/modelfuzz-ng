package etcdraft

import (
	"fmt"

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/core"
	raft "go.etcd.io/raft/v3"
	"go.etcd.io/raft/v3/raftpb"
)

// maybeActivateVoteQuorumFault reproduces the synthetic Etcd-raft bug used by
// the original ModelFuzz evaluation without modifying the raft dependency.
// Once the candidate has the faulty floor(n/3)+1 real grants (including its
// self vote), private synthetic grants drive the unmodified RawNode across its
// normal majority check. Those synthetic messages are deliberately not
// exposed to Runtime or the model: the observable SUT behavior is exactly the
// bug under test, namely BecomeLeader after too few real votes.
func (a *Adapter) maybeActivateVoteQuorumFault(
	n *node, at core.LogicalTime, before raft.BasicStatus, message *raftpb.Message,
) ([]core.Effect, error) {
	if a.config.Faults.VoteQuorumDivisor != weakenedVoteQuorumDivisor || before.RaftState != raft.StateCandidate ||
		message.GetType() != raftpb.MsgVoteResp || message.GetTerm() != before.GetTerm() {
		return nil, nil
	}
	term := before.GetTerm()
	if n.voteTerm != term {
		n.voteTerm = term
		n.voteResponses = make(map[core.NodeID]bool)
	}
	from := core.NodeID(message.GetFrom())
	if _, known := a.nodes[from]; !known || from == n.id {
		return nil, fmt.Errorf("%w: vote response source %s is not another voter", ErrInvalidMessage, from)
	}
	if _, duplicate := n.voteResponses[from]; !duplicate {
		n.voteResponses[from] = !message.GetReject()
	}

	actualGrants := 1 // a candidate votes for itself when campaigning.
	for _, granted := range n.voteResponses {
		if granted {
			actualGrants++
		}
	}
	faultyQuorum := len(a.config.NodeIDs)/a.config.Faults.VoteQuorumDivisor + 1
	if actualGrants < faultyQuorum || n.raw.BasicStatus().RaftState != raft.StateCandidate {
		return nil, nil
	}

	normalQuorum := len(a.config.NodeIDs)/2 + 1
	needed := normalQuorum - actualGrants
	if needed <= 0 {
		return nil, fmt.Errorf("activate weakened vote quorum on node %s: inconsistent candidate vote count %d", n.id, actualGrants)
	}
	synthetic := make([]uint64, 0, needed)
	for _, id := range a.config.NodeIDs {
		if len(synthetic) >= needed {
			break
		}
		if id == n.id {
			continue
		}
		if _, responded := n.voteResponses[id]; responded {
			continue
		}
		if err := n.raw.Step(&raftpb.Message{
			Type: raftpb.MsgVoteResp.Enum(), From: new(uint64(id)), To: new(uint64(n.id)), Term: new(term),
		}); err != nil {
			return nil, fmt.Errorf("activate weakened vote quorum on node %s: %w", n.id, err)
		}
		n.voteResponses[id] = true
		synthetic = append(synthetic, uint64(id))
	}
	if n.raw.BasicStatus().RaftState != raft.StateLeader {
		return nil, fmt.Errorf("activate weakened vote quorum on node %s: candidate did not become leader", n.id)
	}
	return []core.Effect{modelEffect(at, voteQuorumFaultEvent, n.id, map[string]any{
		"term": term, "actual_grants": actualGrants,
		"faulty_quorum": faultyQuorum, "normal_quorum": normalQuorum,
		"vote_quorum_divisor": a.config.Faults.VoteQuorumDivisor,
		"synthetic_grants":    synthetic,
	})}, nil
}
