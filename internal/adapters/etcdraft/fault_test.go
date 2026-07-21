package etcdraft

import (
	"context"
	"testing"

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/core"
	runtimepkg "github.com/SuzumiyaHaruki/modelfuzz-ng/internal/runtime"
)

func TestVoteQuorumFaultMakesFiveNodeCandidateLeaderWithTwoRealVotes(t *testing.T) {
	run := func(t *testing.T, divisor int) runtimepkg.StepResult {
		t.Helper()
		config := testConfig(1, 2, 3, 4, 5)
		config.Faults.VoteQuorumDivisor = divisor
		r := newTestRuntime(t, config)
		timeout, err := r.Execute(context.Background(), core.Action{Kind: core.ActionTimeout, Node: 1})
		if err != nil {
			t.Fatal(err)
		}
		vote := deliverObserved(t, r, findMessage(t, timeout.Observation, "MsgVote", 1, 2))
		return deliverObserved(t, r, findMessage(t, vote.Observation, "MsgVoteResp", 2, 1))
	}

	normal := run(t, normalVoteQuorumDivisor)
	if role := normal.Observation.Nodes[0].Semantic["role"]; role != "candidate" {
		t.Fatalf("normal five-node candidate role after two votes = %v, want candidate", role)
	}
	mutant := run(t, weakenedVoteQuorumDivisor)
	if role := mutant.Observation.Nodes[0].Semantic["role"]; role != "leader" {
		t.Fatalf("n/3+1 mutant role after two votes = %v, want leader", role)
	}
	found := false
	for _, effect := range mutant.Record.Effects {
		if effect.Kind != core.EffectModelEvent || effect.ModelEvent == nil ||
			effect.ModelEvent.Name != voteQuorumFaultEvent {
			continue
		}
		found = effect.ModelEvent.Params["actual_grants"] == 2 &&
			effect.ModelEvent.Params["faulty_quorum"] == 2 &&
			effect.ModelEvent.Params["normal_quorum"] == 3
	}
	if !found {
		t.Fatalf("mutant activation effect absent or malformed: %+v", mutant.Record.Effects)
	}
}

func TestVoteQuorumFaultRejectsThreeNodeNoOpConfiguration(t *testing.T) {
	config := testConfig(1, 2, 3)
	config.Faults.VoteQuorumDivisor = weakenedVoteQuorumDivisor
	if _, err := New(config); err == nil {
		t.Fatal("three-node n/3 quorum configuration was accepted even though it equals majority")
	}
}
