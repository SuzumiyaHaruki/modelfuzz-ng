package policy

import (
	"reflect"
	"testing"

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/core"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/plan"
)

func TestRandomPolicyIsDeterministic(t *testing.T) {
	first, _ := NewRandom(42, DefaultRandomConfig())
	second, _ := NewRandom(42, DefaultRandomConfig())
	observation := randomObservation()
	if err := first.Reset(observation); err != nil {
		t.Fatal(err)
	}
	if err := second.Reset(observation); err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 30; index++ {
		left, leftMore, leftErr := first.Next(observation)
		right, rightMore, rightErr := second.Next(observation)
		if leftErr != nil || rightErr != nil || leftMore != rightMore || !reflect.DeepEqual(left, right) {
			t.Fatalf("draw %d differs: %+v/%v/%v and %+v/%v/%v", index, left, leftMore, leftErr, right, rightMore, rightErr)
		}
	}
}

func TestRandomPolicyCanSelectNonFIFOMessage(t *testing.T) {
	config := DefaultRandomConfig()
	config.Weights = RandomWeights{Deliver: 1}
	policy, _ := NewRandom(7, config)
	observation := randomObservation()
	if err := policy.Reset(observation); err != nil {
		t.Fatal(err)
	}
	found := false
	for index := 0; index < 20; index++ {
		action, more, err := policy.Next(observation)
		if err != nil || !more || action.Validate() != nil {
			t.Fatalf("generated action = %+v, more=%v, err=%v", action, more, err)
		}
		if action.Messages.Start > 0 {
			found = true
		}
	}
	if !found {
		t.Fatal("20 deterministic draws never selected a non-FIFO position")
	}
}

func TestRandomPolicyNeverForcesLeaderTimeout(t *testing.T) {
	config := DefaultRandomConfig()
	config.TimeoutCooldown = 0
	config.Weights = RandomWeights{Timeout: 1}
	policy, _ := NewRandom(11, config)
	observation := randomObservation()
	if err := policy.Reset(observation); err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 10; index++ {
		action, more, err := policy.Next(observation)
		if err != nil || !more || action.Kind != plan.ActionTimeout || action.Node != 2 {
			t.Fatalf("timeout draw = %+v, more=%v, err=%v", action, more, err)
		}
	}
}

func TestRandomPolicyAppliesTimeoutCooldown(t *testing.T) {
	config := DefaultRandomConfig()
	config.TimeoutCooldown = 4
	config.Weights = RandomWeights{Timeout: 1, AdvanceTicks: 1}
	policy, err := NewRandom(13, config)
	if err != nil {
		t.Fatal(err)
	}
	observation := randomObservation()
	if err := policy.Reset(observation); err != nil {
		t.Fatal(err)
	}
	foundTimeout := false
	for index := 0; index < 100; index++ {
		action, more, nextErr := policy.Next(observation)
		if nextErr != nil || !more {
			t.Fatalf("draw %d = %+v/%v/%v", index, action, more, nextErr)
		}
		if action.Kind == plan.ActionTimeout {
			if foundTimeout {
				recent := policy.generated[len(policy.generated)-5 : len(policy.generated)-1]
				for _, previous := range recent {
					if previous.Kind == plan.ActionTimeout {
						t.Fatalf("timeout repeated inside cooldown: %+v", policy.generated)
					}
				}
			}
			foundTimeout = true
		}
	}
	if !foundTimeout {
		t.Fatal("deterministic draws never selected a timeout")
	}
}

func TestRandomPolicyChoosesOnlyExecutableCrashOrRestart(t *testing.T) {
	config := DefaultRandomConfig()
	config.Weights = RandomWeights{Crash: 1, Restart: 1}
	policy, err := NewRandom(17, config)
	if err != nil {
		t.Fatal(err)
	}
	observation := randomObservation()
	if err := policy.Reset(observation); err != nil {
		t.Fatal(err)
	}
	action, more, err := policy.Next(observation)
	if err != nil || !more || action.Kind != plan.ActionCrash {
		t.Fatalf("running cluster action = %+v, more=%v, err=%v", action, more, err)
	}

	observation.Nodes[1].Status = core.NodeCrashed
	observation.Nodes[1].Semantic["role"] = "crashed"
	action, more, err = policy.Next(observation)
	if err != nil || !more || action.Kind != plan.ActionRestart || action.Node != 2 {
		t.Fatalf("one crashed node action = %+v, more=%v, err=%v", action, more, err)
	}
}

func TestRandomPolicyCapsAndSpacesCrashEpisodes(t *testing.T) {
	config := DefaultRandomConfig()
	config.MaxCrashEpisodes = 2
	config.LifecycleCooldown = 3
	config.Weights = RandomWeights{Crash: 1, Restart: 1, AdvanceTicks: 1}
	policy, err := NewRandom(17, config)
	if err != nil {
		t.Fatal(err)
	}
	observation := randomObservation()
	if err := policy.Reset(observation); err != nil {
		t.Fatal(err)
	}
	crashes := 0
	lastRestart := -1
	for index := 0; index < 100; index++ {
		action, more, nextErr := policy.Next(observation)
		if nextErr != nil || !more {
			t.Fatalf("draw %d = %+v/%v/%v", index, action, more, nextErr)
		}
		switch action.Kind {
		case plan.ActionCrash:
			if lastRestart >= 0 && index-lastRestart-1 < config.LifecycleCooldown {
				t.Fatalf("crash at %d follows restart at %d", index, lastRestart)
			}
			crashes++
			for nodeIndex := range observation.Nodes {
				if observation.Nodes[nodeIndex].ID == action.Node {
					observation.Nodes[nodeIndex].Status = core.NodeCrashed
					observation.Nodes[nodeIndex].Semantic["role"] = "crashed"
				}
			}
		case plan.ActionRestart:
			lastRestart = index
			for nodeIndex := range observation.Nodes {
				if observation.Nodes[nodeIndex].ID == action.Node {
					observation.Nodes[nodeIndex].Status = core.NodeRunning
					observation.Nodes[nodeIndex].Semantic["role"] = "follower"
				}
			}
		}
	}
	if crashes != config.MaxCrashEpisodes {
		t.Fatalf("crashes=%d, want %d", crashes, config.MaxCrashEpisodes)
	}
}

func TestRandomPolicyDoesNotCrashLastRunningNode(t *testing.T) {
	config := DefaultRandomConfig()
	config.MaxCrashed = 2
	config.Weights = RandomWeights{Crash: 1}
	policy, _ := NewRandom(19, config)
	observation := randomObservation()
	observation.Nodes[0].Status = core.NodeCrashed
	observation.Nodes[0].Semantic["role"] = "crashed"
	if err := policy.Reset(observation); err != nil {
		t.Fatal(err)
	}
	if action, more, err := policy.Next(observation); err != nil || more {
		t.Fatalf("last running node produced action=%+v, more=%v, err=%v", action, more, err)
	}
}

func TestRandomPolicyDoesNotDeliverToCrashedNode(t *testing.T) {
	config := DefaultRandomConfig()
	config.Weights = RandomWeights{Deliver: 1}
	policy, _ := NewRandom(23, config)
	observation := randomObservation()
	observation.Nodes[1].Status = core.NodeCrashed
	observation.Nodes[1].Semantic["role"] = "crashed"
	if err := policy.Reset(observation); err != nil {
		t.Fatal(err)
	}
	if action, more, err := policy.Next(observation); err != nil || more {
		t.Fatalf("message to crashed node produced action=%+v, more=%v, err=%v", action, more, err)
	}
}

func TestRandomPolicyPartitionsAndThenHeals(t *testing.T) {
	config := DefaultRandomConfig()
	config.Weights = RandomWeights{Partition: 1, Heal: 1}
	policy, err := NewRandom(23, config)
	if err != nil {
		t.Fatal(err)
	}
	observation := randomObservation()
	if err := policy.Reset(observation); err != nil {
		t.Fatal(err)
	}
	action, more, err := policy.Next(observation)
	if err != nil || !more || action.Kind != plan.ActionPartition || action.Partition == nil {
		t.Fatalf("partition action=%+v, more=%v, err=%v", action, more, err)
	}
	nodes := []core.NodeID{1, 2}
	if !action.Partition.Covers(nodes) {
		t.Fatalf("partition does not cover observation nodes: %+v", action.Partition)
	}
	partition := action.Partition.Copy()
	observation.NetworkPartition = &partition
	for index := range observation.Messages {
		message := &observation.Messages[index]
		message.Blocked = partition.Blocks(core.LinkID{From: message.From, To: message.To})
	}
	action, more, err = policy.Next(observation)
	if err != nil || !more || action.Kind != plan.ActionHeal {
		t.Fatalf("heal action=%+v, more=%v, err=%v", action, more, err)
	}
}

func TestRandomPolicyOffersRequestsToFollowerWithUsableLeader(t *testing.T) {
	config := DefaultRandomConfig()
	config.Weights = RandomWeights{Request: 1}
	policy, _ := NewRandom(29, config)
	observation := randomObservation()
	if err := policy.Reset(observation); err != nil {
		t.Fatal(err)
	}
	foundFollower := false
	for index := 0; index < 50; index++ {
		action, more, err := policy.Next(observation)
		if err != nil || !more || action.Kind != plan.ActionRequest {
			t.Fatalf("request action = %+v, more=%v, err=%v", action, more, err)
		}
		if action.Node == 2 {
			foundFollower = true
		}
	}
	if !foundFollower {
		t.Fatal("random requests never targeted the follower that knows the current leader")
	}

	observation.Nodes[1].Semantic["leader"] = uint64(0)
	for index := 0; index < 20; index++ {
		action, more, err := policy.Next(observation)
		if err != nil || !more || action.Node != 1 {
			t.Fatalf("follower without leader was selected: action=%+v, more=%v, err=%v", action, more, err)
		}
	}
}

func TestRandomPolicyOnlyOffersSafeAdvanceTick(t *testing.T) {
	config := DefaultRandomConfig()
	config.LargestTerm = 1
	config.Weights = RandomWeights{AdvanceTicks: 1}
	policy, _ := NewRandom(31, config)
	observation := randomObservation()
	observation.Nodes[1].Semantic["term"] = uint64(1)
	observation.Nodes[1].Semantic["election_ticks_remaining"] = uint64(1)
	if err := policy.Reset(observation); err != nil {
		t.Fatal(err)
	}
	if action, more, err := policy.Next(observation); err != nil || more {
		t.Fatalf("unsafe tick produced action=%+v, more=%v, err=%v", action, more, err)
	}

	observation.Nodes[1].Semantic["election_ticks_remaining"] = uint64(2)
	action, more, err := policy.Next(observation)
	if err != nil || !more || action.Kind != plan.ActionAdvanceTicks || action.Ticks != 1 {
		t.Fatalf("safe tick action=%+v, more=%v, err=%v", action, more, err)
	}
}

func TestRandomPolicyOffersBoundedForwardedProposal(t *testing.T) {
	config := DefaultRandomConfig()
	config.Weights = RandomWeights{Deliver: 1}
	policy, _ := NewRandom(37, config)
	observation := randomObservation()
	observation.Messages = []core.MessageObservation{{
		ID: 10, From: 2, To: 1, SenderEpoch: 1, LinkSequence: 1, Position: 0,
		TypeHint: "MsgProp", Metadata: map[string]string{
			"term": "0", "entry_count": "1", "index": "0", "log_term": "0",
			"commit": "0", "reject": "false",
		},
	}}
	if err := policy.Reset(observation); err != nil {
		t.Fatal(err)
	}
	action, more, err := policy.Next(observation)
	if err != nil || !more || action.Kind != plan.ActionDeliver || action.Messages == nil || action.Messages.Link.From != 2 {
		t.Fatalf("forwarded proposal action=%+v, more=%v, err=%v", action, more, err)
	}
}

func randomObservation() core.Observation {
	nodeState := func(role string, leader uint64) map[string]any {
		return map[string]any{
			"role": role, "leader": leader, "term": uint64(1), "last_index": uint64(1),
			"election_ticks_remaining": uint64(10), "election_timeout": uint64(10),
		}
	}
	metadata := func(entryCount, index string) map[string]string {
		return map[string]string{
			"term": "1", "log_term": "0", "index": index, "commit": "0",
			"reject": "false", "entry_count": entryCount,
		}
	}
	return core.Observation{
		Nodes: []core.NodeObservation{
			{ID: 1, Epoch: 1, Status: core.NodeRunning, Semantic: nodeState("leader", 1)},
			{ID: 2, Epoch: 1, Status: core.NodeRunning, Semantic: nodeState("follower", 1)},
		},
		Messages: []core.MessageObservation{
			{ID: 1, From: 1, To: 2, SenderEpoch: 1, LinkSequence: 1, Position: 0, TypeHint: "MsgApp", Metadata: metadata("1", "0")},
			{ID: 2, From: 1, To: 2, SenderEpoch: 1, LinkSequence: 2, Position: 1, TypeHint: "MsgHeartbeat", Metadata: metadata("0", "0")},
			{ID: 3, From: 1, To: 2, SenderEpoch: 1, LinkSequence: 3, Position: 2, TypeHint: "MsgHeartbeat", Metadata: metadata("0", "0")},
		},
	}
}
