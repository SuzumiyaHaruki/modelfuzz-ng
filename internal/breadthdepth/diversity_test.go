package breadthdepth

import (
	"testing"

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/core"
)

func TestRelativeDiversityIgnoresNodeAndMessageIdentity(t *testing.T) {
	left := core.Observation{
		Nodes: []core.NodeObservation{
			{ID: 1, Status: core.NodeRunning, Semantic: map[string]any{"role": "leader", "term": uint64(3)}},
			{ID: 2, Status: core.NodeRunning, Semantic: map[string]any{"role": "follower", "term": uint64(2)}},
		},
		Messages: []core.MessageObservation{{ID: 1, From: 1, To: 2, TypeHint: "MsgApp"}},
	}
	right := core.Observation{
		Nodes: []core.NodeObservation{
			{ID: 9, Status: core.NodeRunning, Semantic: map[string]any{"role": "follower", "term": uint64(8)}},
			{ID: 7, Status: core.NodeRunning, Semantic: map[string]any{"role": "leader", "term": uint64(9)}},
		},
		Messages: []core.MessageObservation{{ID: 999, From: 7, To: 9, TypeHint: "MsgApp"}},
	}
	if RelativeQueueShapeKey(left) != RelativeQueueShapeKey(right) {
		t.Fatal("queue shape depends on node/message identity or absolute term")
	}
}
