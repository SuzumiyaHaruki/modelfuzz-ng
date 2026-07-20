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
	config.Weights = RandomWeights{Timeout: 1}
	policy, _ := NewRandom(11, config)
	observation := randomObservation()
	policy.Reset(observation)
	for index := 0; index < 10; index++ {
		action, more, err := policy.Next(observation)
		if err != nil || !more || action.Kind != plan.ActionTimeout || action.Node != 2 {
			t.Fatalf("timeout draw = %+v, more=%v, err=%v", action, more, err)
		}
	}
}

func randomObservation() core.Observation {
	return core.Observation{
		Nodes: []core.NodeObservation{
			{ID: 1, Epoch: 1, Status: core.NodeRunning, Semantic: map[string]any{"role": "leader", "term": uint64(1), "last_index": uint64(1)}},
			{ID: 2, Epoch: 1, Status: core.NodeRunning, Semantic: map[string]any{"role": "follower", "term": uint64(1), "last_index": uint64(1)}},
		},
		Messages: []core.MessageObservation{
			{ID: 1, From: 1, To: 2, SenderEpoch: 1, LinkSequence: 1, Position: 0, TypeHint: "MsgApp", Metadata: map[string]string{"index": "0", "entry_count": "1"}},
			{ID: 2, From: 1, To: 2, SenderEpoch: 1, LinkSequence: 2, Position: 1, TypeHint: "MsgHeartbeat", Metadata: map[string]string{"index": "0", "entry_count": "0"}},
			{ID: 3, From: 1, To: 2, SenderEpoch: 1, LinkSequence: 3, Position: 2, TypeHint: "MsgHeartbeat", Metadata: map[string]string{"index": "0", "entry_count": "0"}},
		},
	}
}
