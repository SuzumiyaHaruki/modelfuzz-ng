package raft

import (
	"testing"

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/core"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/plan"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/protocolmutation"
)

func testAdvisor(t *testing.T, ablation Ablation) *Advisor {
	t.Helper()
	advisor, err := New(Config{
		GoalAEnabled: true, GoalBEnabled: true, PriorityMultiplier: 16,
		LocalActionCap: 3, NoProgressCap: 20, QueueLimit: 64, Ablation: ablation,
	})
	if err != nil {
		t.Fatal(err)
	}
	return advisor
}

func request(goal string, waypoint int, observation core.Observation, roles map[string]core.NodeID) protocolmutation.Request {
	return protocolmutation.Request{
		GoalID: goal, Waypoint: "W", WaypointIndex: waypoint,
		Observation: observation, Roles: roles,
		AllowedActions: []plan.ActionKind{
			plan.ActionDeliver, plan.ActionDrop, plan.ActionAdvanceTicks,
			plan.ActionTimeout, plan.ActionCrash, plan.ActionRestart,
			plan.ActionRequest, plan.ActionPartition, plan.ActionHeal,
		},
	}
}

func node(id core.NodeID, status core.NodeStatus, role string, term, lastTerm, lastIndex uint64) core.NodeObservation {
	return core.NodeObservation{
		ID: id, Epoch: 1, Status: status,
		Semantic: map[string]any{
			"role": role, "term": term, "last_term": lastTerm,
			"last_index": lastIndex, "first_index": uint64(1),
		},
	}
}

func message(id core.MessageID, from, to core.NodeID, position int, kind string) core.MessageObservation {
	return core.MessageObservation{
		ID: id, From: from, To: to, SenderEpoch: 1, LinkSequence: uint64(id),
		Position: position, TypeHint: kind, Metadata: map[string]string{"term": "4"},
	}
}

func TestGoalAIsolatesSemanticTargetWithoutFixedNodeID(t *testing.T) {
	observation := core.Observation{Nodes: []core.NodeObservation{
		node(2, core.NodeRunning, "follower", 2, 1, 3),
		node(4, core.NodeRunning, "leader", 2, 1, 3),
		node(9, core.NodeRunning, "follower", 2, 1, 3),
	}}
	decision, err := testAdvisor(t, AblationNone).Advise(request(
		GoalA, 1, observation, map[string]core.NodeID{"Leader": 4, "TargetFollower": 9},
	))
	if err != nil {
		t.Fatal(err)
	}
	if decision.Selected.Action.Kind != plan.ActionPartition ||
		!decision.Selected.Action.Partition.Blocks(core.LinkID{From: 4, To: 9}) ||
		decision.Selected.Action.Partition.Blocks(core.LinkID{From: 4, To: 2}) {
		t.Fatalf("wrong semantic isolation: %+v", decision.Selected)
	}
}

func TestGoalAPrefersRealMajorityResponseOverAnotherRequest(t *testing.T) {
	observation := core.Observation{
		Nodes: []core.NodeObservation{
			node(1, core.NodeRunning, "leader", 1, 1, 3),
			node(2, core.NodeRunning, "follower", 1, 1, 3),
			node(3, core.NodeRunning, "follower", 1, 1, 1),
		},
		Messages:         []core.MessageObservation{message(17, 2, 1, 0, "MsgAppResp")},
		NetworkPartition: &core.NetworkPartition{Groups: [][]core.NodeID{{1, 2}, {3}}},
	}
	decision, err := testAdvisor(t, AblationNone).Advise(request(
		GoalA, 2, observation, map[string]core.NodeID{"Leader": 1, "TargetFollower": 3},
	))
	if err != nil {
		t.Fatal(err)
	}
	if decision.Selected.Class != "maintain-majority-response" ||
		decision.Selected.MessageID != 17 || decision.Selected.Action.Kind != plan.ActionDeliver {
		t.Fatalf("did not maintain quorum: %+v", decision.Selected)
	}
}

func TestGoalAStopsAtSnapshotBoundary(t *testing.T) {
	leader := node(1, core.NodeRunning, "leader", 1, 1, 8)
	leader.Semantic["first_index"] = uint64(5)
	observation := core.Observation{
		Nodes: []core.NodeObservation{
			leader, node(2, core.NodeRunning, "follower", 1, 1, 8),
			node(3, core.NodeRunning, "follower", 1, 1, 1),
		},
		NetworkPartition: &core.NetworkPartition{Groups: [][]core.NodeID{{1, 2}, {3}}},
	}
	decision, err := testAdvisor(t, AblationNone).Advise(request(
		GoalA, 3, observation, map[string]core.NodeID{"Leader": 1, "TargetFollower": 3},
	))
	if err != nil {
		t.Fatal(err)
	}
	if decision.LocalStage != "A5-snapshot-required-return-to-frontier" ||
		decision.Fallback != "snapshot-boundary-reached" {
		t.Fatalf("boundary was not recognized: %+v", decision)
	}
}

func TestGoalBUsesLogFreshActiveCandidate(t *testing.T) {
	observation := core.Observation{Nodes: []core.NodeObservation{
		node(1, core.NodeRunning, "leader", 3, 2, 9),
		node(2, core.NodeRunning, "follower", 3, 2, 9),
		node(3, core.NodeCrashed, "crashed", 3, 2, 9),
		node(4, core.NodeRunning, "follower", 3, 1, 5),
	}}
	decision, err := testAdvisor(t, AblationNone).Advise(request(
		GoalB, 2, observation, map[string]core.NodeID{"Leader": 1, "TargetFollower": 3},
	))
	if err != nil {
		t.Fatal(err)
	}
	if decision.Selected.Action.Kind != plan.ActionTimeout || decision.Selected.Action.Node != 2 {
		t.Fatalf("selected stale or fixed candidate: %+v", decision.Selected)
	}
}

func TestGoalBCompletesVoteBeforeRestart(t *testing.T) {
	observation := core.Observation{
		Nodes: []core.NodeObservation{
			node(1, core.NodeRunning, "follower", 4, 2, 9),
			node(2, core.NodeRunning, "candidate", 4, 2, 9),
			node(3, core.NodeCrashed, "crashed", 3, 2, 9),
		},
		Messages: []core.MessageObservation{message(21, 1, 2, 0, "MsgVoteResp")},
	}
	decision, err := testAdvisor(t, AblationNone).Advise(request(
		GoalB, 3, observation, map[string]core.NodeID{"Leader": 1, "TargetFollower": 3},
	))
	if err != nil {
		t.Fatal(err)
	}
	if decision.Selected.Action.Kind != plan.ActionDeliver || decision.Selected.MessageID != 21 {
		t.Fatalf("target restarted before vote completion: %+v", decision.Selected)
	}
}

func TestGoalBRestartsOnlyAfterNewLeader(t *testing.T) {
	observation := core.Observation{Nodes: []core.NodeObservation{
		node(1, core.NodeRunning, "follower", 4, 2, 9),
		node(2, core.NodeRunning, "leader", 4, 2, 9),
		node(3, core.NodeCrashed, "crashed", 3, 2, 9),
	}}
	decision, err := testAdvisor(t, AblationNone).Advise(request(
		GoalB, 3, observation, map[string]core.NodeID{"Leader": 1, "TargetFollower": 3},
	))
	if err != nil {
		t.Fatal(err)
	}
	if decision.Selected.Action.Kind != plan.ActionRestart || decision.Selected.Action.Node != 3 {
		t.Fatalf("did not restart after real election: %+v", decision.Selected)
	}
}

func TestNoVoteCompletionAblationAvoidsQueuedVote(t *testing.T) {
	observation := core.Observation{
		Nodes: []core.NodeObservation{
			node(1, core.NodeRunning, "follower", 4, 2, 9),
			node(2, core.NodeRunning, "candidate", 4, 2, 9),
			node(3, core.NodeCrashed, "crashed", 3, 2, 9),
		},
		Messages: []core.MessageObservation{message(21, 1, 2, 0, "MsgVoteResp")},
	}
	decision, err := testAdvisor(t, AblationNoVoteCompletion).Advise(request(
		GoalB, 3, observation, map[string]core.NodeID{"Leader": 1, "TargetFollower": 3},
	))
	if err != nil {
		t.Fatal(err)
	}
	if decision.Selected.MessageID == 21 {
		t.Fatalf("vote-completion ablation still selected vote: %+v", decision.Selected)
	}
}

func TestAdvisorIsDeterministicForSameObservation(t *testing.T) {
	observation := core.Observation{Nodes: []core.NodeObservation{
		node(7, core.NodeRunning, "follower", 2, 1, 3),
		node(4, core.NodeRunning, "leader", 2, 1, 3),
		node(9, core.NodeRunning, "follower", 2, 1, 3),
	}}
	advisor := testAdvisor(t, AblationNone)
	left, err := advisor.Advise(request(GoalA, 1, observation,
		map[string]core.NodeID{"Leader": 4, "TargetFollower": 9}))
	if err != nil {
		t.Fatal(err)
	}
	right, err := advisor.Advise(request(GoalA, 1, observation,
		map[string]core.NodeID{"Leader": 4, "TargetFollower": 9}))
	if err != nil {
		t.Fatal(err)
	}
	if left.StableKey != right.StableKey {
		t.Fatalf("same observation produced unstable advice: %s != %s", left.StableKey, right.StableKey)
	}
}

func TestUnknownGoalIsRejected(t *testing.T) {
	_, err := testAdvisor(t, AblationNone).Advise(request(
		"unknown-goal", 0, core.Observation{}, nil,
	))
	if err == nil {
		t.Fatal("unknown goal was accepted")
	}
}

func TestUnstableLeaderStartsElectionInsteadOfSubmittingRequests(t *testing.T) {
	observation := core.Observation{Nodes: []core.NodeObservation{
		node(2, core.NodeRunning, "follower", 1, 1, 2),
		node(4, core.NodeRunning, "follower", 1, 1, 3),
		node(9, core.NodeRunning, "follower", 1, 1, 1),
	}}
	decision, err := testAdvisor(t, AblationNone).Advise(request(GoalA, 0, observation, nil))
	if err != nil {
		t.Fatal(err)
	}
	if decision.Selected.Action.Kind != plan.ActionTimeout {
		t.Fatalf("unstable cluster got non-election advice: %+v", decision.Selected)
	}
}

func TestGoalADoesNotDeliverReplicationToIsolatedTarget(t *testing.T) {
	toTarget := message(31, 1, 3, 0, "MsgApp")
	toTarget.Blocked = true
	observation := core.Observation{
		Nodes: []core.NodeObservation{
			node(1, core.NodeRunning, "leader", 1, 1, 3),
			node(2, core.NodeRunning, "follower", 1, 1, 3),
			node(3, core.NodeRunning, "follower", 1, 1, 1),
		},
		Messages:         []core.MessageObservation{toTarget},
		NetworkPartition: &core.NetworkPartition{Groups: [][]core.NodeID{{1, 2}, {3}}},
	}
	decision, err := testAdvisor(t, AblationNone).Advise(request(
		GoalA, 2, observation, map[string]core.NodeID{"Leader": 1, "TargetFollower": 3},
	))
	if err != nil {
		t.Fatal(err)
	}
	if decision.Selected.MessageID == 31 {
		t.Fatalf("advisor selected target replication: %+v", decision.Selected)
	}
}

func TestNoQuorumMaintenanceRemovesReplicationWindow(t *testing.T) {
	observation := core.Observation{
		Nodes: []core.NodeObservation{
			node(1, core.NodeRunning, "leader", 1, 1, 3),
			node(2, core.NodeRunning, "follower", 1, 1, 3),
			node(3, core.NodeRunning, "follower", 1, 1, 1),
		},
		Messages:         []core.MessageObservation{message(17, 2, 1, 0, "MsgAppResp")},
		NetworkPartition: &core.NetworkPartition{Groups: [][]core.NodeID{{1, 2}, {3}}},
	}
	decision, err := testAdvisor(t, AblationNoQuorumMaintenance).Advise(request(
		GoalA, 2, observation, map[string]core.NodeID{"Leader": 1, "TargetFollower": 3},
	))
	if err != nil {
		t.Fatal(err)
	}
	actions := protocolmutation.EffectiveActions(decision.Selected)
	if len(actions) != 1 || actions[0].Kind != plan.ActionRequest {
		t.Fatalf("quorum ablation retained maintenance window: %+v", actions)
	}
}

func TestNoBoundaryAwarenessContinuesAtCompactionBoundary(t *testing.T) {
	leader := node(1, core.NodeRunning, "leader", 1, 1, 8)
	leader.Semantic["first_index"] = uint64(5)
	observation := core.Observation{
		Nodes: []core.NodeObservation{
			leader, node(2, core.NodeRunning, "follower", 1, 1, 8),
			node(3, core.NodeRunning, "follower", 1, 1, 1),
		},
		NetworkPartition: &core.NetworkPartition{Groups: [][]core.NodeID{{1, 2}, {3}}},
	}
	decision, err := testAdvisor(t, AblationNoBoundaryAwareness).Advise(request(
		GoalA, 3, observation, map[string]core.NodeID{"Leader": 1, "TargetFollower": 3},
	))
	if err != nil {
		t.Fatal(err)
	}
	if decision.Fallback == "snapshot-boundary-reached" {
		t.Fatalf("boundary ablation still stopped: %+v", decision)
	}
}

func TestEarlyRestartAblationRestartsBeforeLeaderFormation(t *testing.T) {
	observation := core.Observation{Nodes: []core.NodeObservation{
		node(1, core.NodeRunning, "follower", 4, 2, 9),
		node(2, core.NodeRunning, "candidate", 4, 2, 9),
		node(3, core.NodeCrashed, "crashed", 3, 2, 9),
	}}
	decision, err := testAdvisor(t, AblationEarlyRestart).Advise(request(
		GoalB, 3, observation, map[string]core.NodeID{"Leader": 1, "TargetFollower": 3},
	))
	if err != nil {
		t.Fatal(err)
	}
	if decision.Selected.Action.Kind != plan.ActionRestart {
		t.Fatalf("early restart ablation did not restore old behavior: %+v", decision.Selected)
	}
}

func TestInvalidAdvisorConfigIsRejected(t *testing.T) {
	if _, err := New(Config{}); err == nil {
		t.Fatal("zero advisor limits were accepted")
	}
	if _, err := New(Config{
		GoalAEnabled: true, GoalBEnabled: true, PriorityMultiplier: 1,
		LocalActionCap: 1, NoProgressCap: 1, QueueLimit: 1, Ablation: "unknown",
	}); err == nil {
		t.Fatal("unknown ablation was accepted")
	}
}
