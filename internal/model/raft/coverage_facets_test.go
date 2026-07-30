package raft

import (
	"reflect"
	"strings"
	"testing"

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/core"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/model"
)

func TestCoverageFacetsAreDeterministicAndSymmetryInvariant(t *testing.T) {
	left := projectFacets(t, prototypeSymmetryStateA(), FacetContext{})
	right := projectFacets(t, prototypeSymmetryStateB(), FacetContext{})
	if left.Schema != CoverageFacetsSchemaVersion {
		t.Fatalf("schema=%q", left.Schema)
	}
	if left.ElectionKey != right.ElectionKey || left.ReplicationKey != right.ReplicationKey ||
		left.SnapshotKey != right.SnapshotKey || left.RecoveryKey != right.RecoveryKey ||
		left.NetworkKey != right.NetworkKey {
		t.Fatalf("symmetric node rename changed facet keys:\nleft=%+v\nright=%+v", left, right)
	}
	for attempt := 0; attempt < 20; attempt++ {
		next := projectFacets(t, prototypeSymmetryStateA(), FacetContext{})
		if !reflect.DeepEqual(left, next) {
			t.Fatalf("attempt %d changed projection", attempt)
		}
	}
}

func TestElectionFacetIgnoresAbsoluteTermButKeepsElectionBoundaries(t *testing.T) {
	base := prototypeSymmetryStateA()
	shifted := strings.NewReplacer(
		`currentTerm = <<2, 2, 2>>`, `currentTerm = <<8, 8, 8>>`,
		`term |-> 2`, `term |-> 8`,
	).Replace(base)
	if projectFacets(t, base, FacetContext{}).ElectionKey !=
		projectFacets(t, shifted, FacetContext{}).ElectionKey {
		t.Fatal("absolute term shift changed election facet")
	}
	noLeader := strings.Replace(base,
		`/\ state = <<"follower", "leader", "follower">>`,
		`/\ state = <<"follower", "follower", "follower">>`, 1)
	if projectFacets(t, base, FacetContext{}).ElectionKey ==
		projectFacets(t, noLeader, FacetContext{}).ElectionKey {
		t.Fatal("stable leader and no leader were merged")
	}
	votes := []string{`{1}`, `{1, 2}`, `{1, 2, 3}`}
	keys := make(map[int64]struct{})
	for _, value := range votes {
		keys[projectFacets(t, prototypeFiveNodeCandidateState(value), FacetContext{}).ElectionKey] = struct{}{}
	}
	if len(keys) != len(votes) {
		t.Fatalf("candidate vote boundaries merged: %d keys", len(keys))
	}
	oneCandidate := prototypeFiveNodeCandidateState(`{1}`)
	multipleCandidates := strings.Replace(oneCandidate,
		`/\ state = <<"candidate", "follower", "follower", "follower", "follower">>`,
		`/\ state = <<"candidate", "candidate", "follower", "follower", "follower">>`, 1)
	if projectFacets(t, oneCandidate, FacetContext{}).ElectionKey ==
		projectFacets(t, multipleCandidates, FacetContext{}).ElectionKey {
		t.Fatal("one and multiple candidates were merged")
	}
	splitTerm := strings.Replace(oneCandidate,
		`/\ currentTerm = <<4, 4, 4, 4, 4>>`,
		`/\ currentTerm = <<4, 3, 3, 3, 3>>`, 1)
	if projectFacets(t, oneCandidate, FacetContext{}).ElectionKey ==
		projectFacets(t, splitTerm, FacetContext{}).ElectionKey {
		t.Fatal("same-term and split-term elections were merged")
	}
}

func TestReplicationFacetIgnoresIndexShiftAndKeepsCatchUpModes(t *testing.T) {
	if projectFacets(t, prototypeShiftedStorageState(false), FacetContext{}).ReplicationKey !=
		projectFacets(t, prototypeShiftedStorageState(true), FacetContext{}).ReplicationKey {
		t.Fatal("absolute log/index shift changed replication facet")
	}
	appendCatchUp := projectFacets(t, prototypeSnapshotCatchUpState(false), FacetContext{}).Replication
	snapshotCatchUp := projectFacets(t, prototypeSnapshotCatchUpState(true), FacetContext{}).Replication
	if !appendCatchUp.AppendCatchUp || appendCatchUp.SnapshotRequired {
		t.Fatalf("ordinary catch-up classified incorrectly: %+v", appendCatchUp)
	}
	if !snapshotCatchUp.SnapshotRequired {
		t.Fatalf("snapshot-required catch-up classified incorrectly: %+v", snapshotCatchUp)
	}
	uncommittedConflict := strings.Replace(prototypeSymmetryStateA(),
		`/\ log = <<<<[term |-> 2, value |-> 1]>>, <<[term |-> 2, value |-> 1], [term |-> 2, value |-> 2]>>, <<[term |-> 2, value |-> 1], [term |-> 2, value |-> 2]>>>>`,
		`/\ log = <<<<[term |-> 2, value |-> 1]>>, <<[term |-> 2, value |-> 1], [term |-> 2, value |-> 2]>>, <<[term |-> 2, value |-> 1], [term |-> 2, value |-> 9]>>>>`, 1)
	if !projectFacets(t, uncommittedConflict, FacetContext{}).Replication.UncommittedConflict {
		t.Fatal("uncommitted suffix conflict was not represented")
	}
	committedConflict := strings.Replace(prototypeSymmetryStateA(),
		`/\ log = <<<<[term |-> 2, value |-> 1]>>, <<[term |-> 2, value |-> 1], [term |-> 2, value |-> 2]>>, <<[term |-> 2, value |-> 1], [term |-> 2, value |-> 2]>>>>`,
		`/\ log = <<<<[term |-> 2, value |-> 9]>>, <<[term |-> 2, value |-> 1], [term |-> 2, value |-> 2]>>, <<[term |-> 2, value |-> 1], [term |-> 2, value |-> 2]>>>>`, 1)
	if !projectFacets(t, committedConflict, FacetContext{}).Replication.CommittedConflict {
		t.Fatal("committed prefix conflict was not represented")
	}
}

func TestSnapshotRecoveryAndNetworkFacetsUseExecutionContext(t *testing.T) {
	state := prototypeSymmetryStateA()
	base := projectFacets(t, state, FacetContext{})
	snapshot := projectFacets(t, state, FacetContext{
		SnapshotOutcome: "installed", SnapshotRetryPending: true,
	})
	if base.SnapshotKey == snapshot.SnapshotKey ||
		snapshot.Snapshot.Outcome != "installed" || !snapshot.Snapshot.RetryPending {
		t.Fatalf("snapshot outcome context was ignored: %+v", snapshot.Snapshot)
	}
	outcomeKeys := make(map[int64]struct{})
	for _, outcome := range []string{"pending", "installed", "failed", "retry-succeeded", "fast-forward"} {
		outcomeKeys[projectFacets(t, state, FacetContext{SnapshotOutcome: outcome}).SnapshotKey] = struct{}{}
	}
	if len(outcomeKeys) != 5 {
		t.Fatalf("observable snapshot outcomes were merged: %d keys", len(outcomeKeys))
	}
	available := projectFacets(t, prototypeShiftedStorageState(false), FacetContext{})
	if available.Snapshot.Mode == "no-snapshot" {
		t.Fatalf("available snapshot was not represented: %+v", available.Snapshot)
	}
	recovering := projectFacets(t, state, FacetContext{
		RestartedNodes: []core.NodeID{3}, RecoveringNodes: []core.NodeID{3},
		RecoveryMode: "snapshot", MessageTermRelation: "older",
	})
	if recovering.Recovery.Phase != "restarted-waiting-catch-up" ||
		recovering.Recovery.RecoveryMode != "snapshot" ||
		recovering.Recovery.MessageTermRelation != "older" ||
		recovering.RecoveryKey == base.RecoveryKey {
		t.Fatalf("recovery context was ignored: %+v", recovering.Recovery)
	}
	completed := projectFacets(t, state, FacetContext{
		RestartedNodes: []core.NodeID{3}, RecoveredThisFrame: 1,
	})
	if completed.Recovery.Phase != "recovery-completed" {
		t.Fatalf("completed recovery phase=%q", completed.Recovery.Phase)
	}
	crashedState := strings.Replace(state,
		`/\ currentActive = {1, 2, 3}`, `/\ currentActive = {1, 2}`, 1)
	if projectFacets(t, crashedState, FacetContext{}).Recovery.Phase != "node-crashed" {
		t.Fatal("crashed follower was not represented")
	}
	restartedRecovered := projectFacets(t, state, FacetContext{RestartedNodes: []core.NodeID{3}})
	if restartedRecovered.Recovery.Phase != "restarted-recovered" {
		t.Fatalf("restarted recovered phase=%q", restartedRecovered.Recovery.Phase)
	}

	leaderPartition := core.NetworkPartition{Groups: [][]core.NodeID{{2}, {1, 3}}}
	leaderIsolated := projectFacets(t, state, FacetContext{NetworkPartition: &leaderPartition})
	if leaderIsolated.Network.Mode != "leader-isolated" ||
		leaderIsolated.Network.LeaderPlacement != "leader-isolated" ||
		!leaderIsolated.Network.ConnectedQuorum {
		t.Fatalf("leader partition classified incorrectly: %+v", leaderIsolated.Network)
	}
	followerPartition := core.NetworkPartition{Groups: [][]core.NodeID{{1}, {2, 3}}}
	followerIsolated := projectFacets(t, state, FacetContext{NetworkPartition: &followerPartition})
	if followerIsolated.Network.Mode != "single-follower-isolated" {
		t.Fatalf("follower partition classified incorrectly: %+v", followerIsolated.Network)
	}
	noQuorumPartition := core.NetworkPartition{Groups: [][]core.NodeID{{1}, {2}, {3}}}
	noQuorum := projectFacets(t, state, FacetContext{NetworkPartition: &noQuorumPartition})
	if noQuorum.Network.Mode != "no-connected-quorum" || noQuorum.Network.ConnectedQuorum {
		t.Fatalf("no-quorum partition classified incorrectly: %+v", noQuorum.Network)
	}
	healed := projectFacets(t, state, FacetContext{
		JustHealed: true, DelayedMessages: true,
	})
	if healed.Network.Mode != "healed" || !healed.Network.DelayedMessagesPending ||
		healed.NetworkKey == base.NetworkKey {
		t.Fatalf("heal/delayed context classified incorrectly: %+v", healed.Network)
	}
}

func projectFacets(t *testing.T, text string, context FacetContext) CoverageFacetProjection {
	t.Helper()
	projection, err := ProjectCoverageFacets(model.State{Text: text}, context)
	if err != nil {
		t.Fatal(err)
	}
	return projection
}
