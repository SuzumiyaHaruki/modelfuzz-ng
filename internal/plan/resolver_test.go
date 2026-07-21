package plan

import (
	"math"
	"testing"

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/core"
)

func TestResolvePartialMessageBatch(t *testing.T) {
	resolver := newTestResolver(t)
	link := core.LinkID{From: 1, To: 2}
	observation := testObservation()
	observation.Nodes[1].Status = core.NodeRunning
	observation.Nodes[1].Semantic["role"] = "follower"
	result := resolver.Resolve(PlanAction{
		Kind: ActionDeliver,
		Messages: &MessageRangeSelector{
			Link: link, Start: 0, Count: 5,
		},
	}, observation)

	if result.Status != ResolutionPartial || result.Requested != 5 || result.Resolved != 3 {
		t.Fatalf("resolution = %+v", result)
	}
	if err := result.Validate(); err != nil {
		t.Fatalf("resolution is invalid: %v", err)
	}
	for i, action := range result.Actions {
		if action.Kind != core.ActionDeliver || action.Message != core.MessageID(10+i) {
			t.Fatalf("action %d = %+v", i, action)
		}
		// Deliver 会移除队首，所以下一条原始消息执行时仍位于 position 0。
		if action.Selector == nil || action.Selector.Link != link || action.Selector.Position != 0 {
			t.Fatalf("action %d selector = %+v", i, action.Selector)
		}
	}
}

func TestResolveSkipsDeliveryToCrashedNodeButAllowsQueueControl(t *testing.T) {
	resolver := newTestResolver(t)
	link := core.LinkID{From: 1, To: 2}
	deliver := resolver.Resolve(PlanAction{Kind: ActionDeliver, Messages: &MessageRangeSelector{
		Link: link, Count: 1,
	}}, testObservation())
	if deliver.Status != ResolutionSkipped || deliver.Resolved != 0 {
		t.Fatalf("deliver to crashed node = %+v", deliver)
	}
	drop := resolver.Resolve(PlanAction{Kind: ActionDrop, Messages: &MessageRangeSelector{
		Link: link, Count: 1,
	}}, testObservation())
	if drop.Status != ResolutionResolved || drop.Resolved != 1 {
		t.Fatalf("drop queued message for crashed node = %+v", drop)
	}
}

func TestResolveMessageRangeAndDuplicatePositions(t *testing.T) {
	resolver := newTestResolver(t)
	link := core.LinkID{From: 1, To: 2}
	result := resolver.Resolve(PlanAction{
		Kind: ActionDuplicate,
		Messages: &MessageRangeSelector{
			Link: link, Start: 1, Count: 2,
		},
	}, testObservation())
	if result.Status != ResolutionResolved || result.Resolved != 2 {
		t.Fatalf("resolution = %+v", result)
	}
	for i, action := range result.Actions {
		wantPosition := i + 1
		if action.Message != core.MessageID(11+i) || action.Selector.Position != wantPosition {
			t.Fatalf("action %d = %+v, want position %d", i, action, wantPosition)
		}
	}
}

func TestResolveEmptyQueue(t *testing.T) {
	resolver := newTestResolver(t)
	result := resolver.Resolve(PlanAction{
		Kind: ActionDrop,
		Messages: &MessageRangeSelector{
			Link: core.LinkID{From: 2, To: 3}, Count: 1,
		},
	}, testObservation())
	if result.Status != ResolutionEmptyQueue || result.Resolved != 0 || len(result.Actions) != 0 {
		t.Fatalf("resolution = %+v", result)
	}
}

func TestResolveAdvanceTicks(t *testing.T) {
	resolver := newTestResolver(t)
	result := resolver.Resolve(PlanAction{Kind: ActionAdvanceTicks, Ticks: 2}, testObservation())
	if result.Status != ResolutionResolved || len(result.Actions) != 1 ||
		result.Actions[0].Kind != core.ActionAdvanceTime || result.Actions[0].TargetTime != 12 {
		t.Fatalf("resolution = %+v", result)
	}

	overLimit := resolver.Resolve(PlanAction{Kind: ActionAdvanceTicks, Ticks: 11}, testObservation())
	if overLimit.Status != ResolutionInvalid {
		t.Fatalf("over-limit resolution = %+v", overLimit)
	}

	overflowObservation := testObservation()
	overflowObservation.Time = core.LogicalTime(math.MaxUint64)
	overflow := resolver.Resolve(PlanAction{Kind: ActionAdvanceTicks, Ticks: 1}, overflowObservation)
	if overflow.Status != ResolutionInvalid {
		t.Fatalf("overflow resolution = %+v", overflow)
	}
}

func TestResolveNodeActionsBestEffort(t *testing.T) {
	resolver := newTestResolver(t)
	observation := testObservation()
	tests := []struct {
		name   string
		action PlanAction
		status ResolutionStatus
		kind   core.ActionKind
	}{
		{name: "timeout follower", action: PlanAction{Kind: ActionTimeout, Node: 1}, status: ResolutionResolved, kind: core.ActionTimeout},
		{name: "timeout leader is adapter policy", action: PlanAction{Kind: ActionTimeout, Node: 3}, status: ResolutionResolved, kind: core.ActionTimeout},
		{name: "crash running", action: PlanAction{Kind: ActionCrash, Node: 1}, status: ResolutionResolved, kind: core.ActionCrash},
		{name: "crash crashed", action: PlanAction{Kind: ActionCrash, Node: 2}, status: ResolutionSkipped},
		{name: "restart crashed", action: PlanAction{Kind: ActionRestart, Node: 2}, status: ResolutionResolved, kind: core.ActionRestart},
		{name: "restart running", action: PlanAction{Kind: ActionRestart, Node: 1}, status: ResolutionSkipped},
		{name: "request running", action: PlanAction{Kind: ActionRequest, Node: 1, Request: "4"}, status: ResolutionResolved, kind: core.ActionRequest},
		{name: "request crashed", action: PlanAction{Kind: ActionRequest, Node: 2, Request: "4"}, status: ResolutionSkipped},
		{name: "unknown node", action: PlanAction{Kind: ActionCrash, Node: 99}, status: ResolutionSkipped},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := resolver.Resolve(test.action, observation)
			if result.Status != test.status {
				t.Fatalf("status = %s, want %s: %+v", result.Status, test.status, result)
			}
			if test.status == ResolutionResolved {
				if len(result.Actions) != 1 || result.Actions[0].Kind != test.kind {
					t.Fatalf("actions = %+v, want kind %s", result.Actions, test.kind)
				}
			}
		})
	}
}

func TestResolveRejectsInvalidQueuePositions(t *testing.T) {
	resolver := newTestResolver(t)
	observation := testObservation()
	observation.Nodes[1].Status = core.NodeRunning
	observation.Nodes[1].Semantic["role"] = "follower"
	observation.Messages[1].Position = 4
	result := resolver.Resolve(PlanAction{
		Kind: ActionDeliver,
		Messages: &MessageRangeSelector{
			Link: core.LinkID{From: 1, To: 2}, Count: 1,
		},
	}, observation)
	if result.Status != ResolutionInvalid {
		t.Fatalf("resolution = %+v", result)
	}
}

func newTestResolver(t *testing.T) *Resolver {
	t.Helper()
	resolver, err := NewResolver(ResolverConfig{MaxAdvanceTicks: 10, MaxBatch: 10})
	if err != nil {
		t.Fatal(err)
	}
	return resolver
}

func testObservation() core.Observation {
	return core.Observation{
		Time: 10,
		Nodes: []core.NodeObservation{
			{ID: 1, Epoch: 1, Status: core.NodeRunning, Semantic: map[string]any{"role": "follower"}},
			{ID: 2, Epoch: 1, Status: core.NodeCrashed, Semantic: map[string]any{"role": "crashed"}},
			{ID: 3, Epoch: 1, Status: core.NodeRunning, Semantic: map[string]any{"role": "leader"}},
		},
		Messages: []core.MessageObservation{
			messageObservation(10, 1, 2, 0),
			messageObservation(11, 1, 2, 1),
			messageObservation(12, 1, 2, 2),
			messageObservation(20, 3, 1, 0),
		},
	}
}

func messageObservation(id core.MessageID, from, to core.NodeID, position int) core.MessageObservation {
	return core.MessageObservation{
		ID: id, From: from, To: to, SenderEpoch: 1,
		LinkSequence: uint64(position + 1), Position: position,
	}
}
