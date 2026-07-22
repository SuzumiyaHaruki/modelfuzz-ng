package policy

import (
	"reflect"
	"strings"
	"testing"

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/core"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/plan"
)

func TestSnapshotPartitionRejectsInvalidConfiguration(t *testing.T) {
	valid := SnapshotPartitionConfig{NodeIDs: []core.NodeID{1, 2, 3}, MaxValue: 5, MaxLogIndex: 10, SnapshotThreshold: 2}
	tests := []struct {
		name   string
		change func(*SnapshotPartitionConfig)
	}{
		{name: "too few nodes", change: func(c *SnapshotPartitionConfig) { c.NodeIDs = []core.NodeID{1, 2} }},
		{name: "duplicate node", change: func(c *SnapshotPartitionConfig) { c.NodeIDs = []core.NodeID{1, 2, 2} }},
		{name: "zero threshold", change: func(c *SnapshotPartitionConfig) { c.SnapshotThreshold = 0 }},
		{name: "threshold above bound", change: func(c *SnapshotPartitionConfig) { c.SnapshotThreshold = 11 }},
		{name: "retain prevents compaction", change: func(c *SnapshotPartitionConfig) { c.RetainEntries = 10 }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := valid
			config.NodeIDs = append([]core.NodeID(nil), valid.NodeIDs...)
			test.change(&config)
			if _, err := NewSnapshotPartition(1, config); err == nil {
				t.Fatal("invalid configuration was accepted")
			}
		})
	}
}

func TestSnapshotPartitionNegativeSeedAndResetAreDeterministic(t *testing.T) {
	policy, err := NewSnapshotPartition(-1, snapshotPolicyConfig(false, 0))
	if err != nil {
		t.Fatal(err)
	}
	initial := snapshotObservation(0, 0, 1, 0, false)
	if err := policy.Reset(initial); err != nil {
		t.Fatal(err)
	}
	first, more, err := policy.Next(initial)
	if err != nil || !more || first.Kind != plan.ActionTimeout || first.Node != 3 {
		t.Fatalf("negative-seed first action = %+v, more=%v, err=%v", first, more, err)
	}
	firstSequence := policy.Sequence()
	if firstSequence.Metadata["source"] != "snapshot_partition" || firstSequence.Metadata["seed"] != "-1" {
		t.Fatalf("sequence metadata = %+v", firstSequence.Metadata)
	}
	if err := policy.Reset(initial); err != nil {
		t.Fatal(err)
	}
	second, _, err := policy.Next(initial)
	if err != nil || !reflect.DeepEqual(first, second) || !reflect.DeepEqual(firstSequence, policy.Sequence()) {
		t.Fatalf("reset changed sequence: %+v / %+v", firstSequence, policy.Sequence())
	}
}

func TestSnapshotPartitionRequiresMatchingObservationAndStablePartition(t *testing.T) {
	policy, _ := NewSnapshotPartition(0, snapshotPolicyConfig(false, 0))
	wrong := snapshotObservation(1, 0, 1, 1, false)
	wrong.Nodes = wrong.Nodes[:2]
	if err := policy.Reset(wrong); err == nil {
		t.Fatal("mismatched node set was accepted")
	}
	leader := snapshotObservation(1, 0, 1, 1, false)
	if err := policy.Reset(leader); err != nil {
		t.Fatal(err)
	}
	partition, more, err := policy.Next(leader)
	if err != nil || !more || partition.Kind != plan.ActionPartition {
		t.Fatalf("partition action = %+v, more=%v, err=%v", partition, more, err)
	}
	if _, more, err := policy.Next(leader); err == nil || more || !strings.Contains(err.Error(), "partition disappeared") {
		t.Fatalf("disappeared partition = more=%v, err=%v", more, err)
	}

	policy, _ = NewSnapshotPartition(0, snapshotPolicyConfig(false, 0))
	if err := policy.Reset(leader); err != nil {
		t.Fatal(err)
	}
	partition, _, _ = policy.Next(leader)
	lostLeader := snapshotObservation(0, 0, 1, 1, true)
	lostLeader.NetworkPartition = partition.Partition
	if _, more, err := policy.Next(lostLeader); err == nil || more || !strings.Contains(err.Error(), "lost leader") {
		t.Fatalf("lost leader = more=%v, err=%v", more, err)
	}
}

func TestSnapshotPartitionUsesLeaderFirstIndexNotSnapshotIndex(t *testing.T) {
	policy, _ := NewSnapshotPartition(0, snapshotPolicyConfig(false, 1))
	initial := snapshotObservation(1, 0, 1, 1, false)
	if err := policy.Reset(initial); err != nil {
		t.Fatal(err)
	}
	partitionAction, _, _ := policy.Next(initial)
	partitioned := snapshotObservation(1, 2, 1, 3, true)
	partitioned.NetworkPartition = partitionAction.Partition
	partitioned.Nodes[2].Semantic["last_index"] = uint64(0)
	partitioned.Nodes[2].Semantic["commit"] = uint64(0)
	partitioned.Nodes[2].Semantic["applied"] = uint64(0)
	action, more, err := policy.Next(partitioned)
	if err != nil || !more || action.Kind != plan.ActionRequest {
		t.Fatalf("retained log should continue commits: action=%+v, more=%v, err=%v", action, more, err)
	}
	partitioned.Nodes[0].Semantic["first_index"] = uint64(2)
	action, more, err = policy.Next(partitioned)
	if err != nil || !more || action.Kind != plan.ActionHeal {
		t.Fatalf("compacted log should heal: action=%+v, more=%v, err=%v", action, more, err)
	}
}

func TestSnapshotPartitionDuplicateSwitchAndRecoveryBudget(t *testing.T) {
	for _, duplicate := range []bool{false, true} {
		policy, _ := NewSnapshotPartition(0, snapshotPolicyConfig(duplicate, 0))
		policy.preferredLeader, policy.leader, policy.lagger = 1, 1, 3
		policy.partitionStarted, policy.healIssued, policy.targetSnapshot = true, true, 2
		observation := snapshotObservation(1, 2, 3, 3, false)
		observation.Messages = []core.MessageObservation{{
			ID: 1, From: 1, To: 3, SenderEpoch: 1, LinkSequence: 1, TypeHint: "MsgSnap",
		}}
		action, more, err := policy.Next(observation)
		want := plan.ActionDeliver
		if duplicate {
			want = plan.ActionDuplicate
		}
		if err != nil || !more || action.Kind != want {
			t.Fatalf("duplicate=%v action=%+v, more=%v, err=%v", duplicate, action, more, err)
		}
	}

	policy, _ := NewSnapshotPartition(0, snapshotPolicyConfig(false, 0))
	policy.preferredLeader, policy.leader, policy.lagger = 1, 1, 3
	policy.partitionStarted, policy.healIssued, policy.targetSnapshot = true, true, 2
	observation := snapshotObservation(1, 0, 1, 3, false)
	for index := 0; index < 16; index++ {
		if action, more, err := policy.Next(observation); err != nil || !more || action.Kind != plan.ActionAdvanceTicks {
			t.Fatalf("recovery tick %d = %+v, more=%v, err=%v", index, action, more, err)
		}
	}
	if _, more, err := policy.Next(observation); err == nil || more || !strings.Contains(err.Error(), "did not produce") {
		t.Fatalf("recovery exhaustion = more=%v, err=%v", more, err)
	}
}

func snapshotPolicyConfig(duplicate bool, retain uint64) SnapshotPartitionConfig {
	return SnapshotPartitionConfig{
		NodeIDs: []core.NodeID{1, 2, 3}, MaxValue: 5, MaxLogIndex: 10,
		SnapshotThreshold: 2, RetainEntries: retain, DuplicateSnapshot: duplicate,
	}
}

func snapshotObservation(leader, snapshotIndex, firstIndex, lastIndex uint64, partitioned bool) core.Observation {
	nodes := make([]core.NodeObservation, 3)
	for index := range nodes {
		id := core.NodeID(index + 1)
		role := "follower"
		if uint64(id) == leader {
			role = "leader"
		}
		nodes[index] = core.NodeObservation{ID: id, Epoch: 1, Status: core.NodeRunning, Semantic: map[string]any{
			"role": role, "leader": leader, "last_index": lastIndex, "snapshot_index": uint64(0),
			"first_index": uint64(1), "commit": lastIndex, "applied": lastIndex,
		}}
	}
	if leader != 0 {
		nodes[leader-1].Semantic["snapshot_index"] = snapshotIndex
		nodes[leader-1].Semantic["first_index"] = firstIndex
	}
	observation := core.Observation{Nodes: nodes}
	if partitioned {
		partition := core.NetworkPartition{Groups: [][]core.NodeID{{1, 2}, {3}}}
		observation.NetworkPartition = &partition
	}
	return observation
}
