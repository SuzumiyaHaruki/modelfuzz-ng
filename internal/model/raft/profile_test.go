package raft

import (
	"errors"
	"testing"

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/core"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/model"
)

func TestProfileClassifiesBasicActions(t *testing.T) {
	mapper := NewMapper()
	observation := profileObservation("MsgVote", "0", "0")
	tests := []struct {
		name      string
		action    core.Action
		wantError bool
	}{
		{name: "timeout", action: core.Action{Kind: core.ActionTimeout, Node: 1}},
		{name: "drop", action: messageAction(core.ActionDrop)},
		{name: "deliver vote", action: messageAction(core.ActionDeliver)},
		{name: "crash", action: core.Action{Kind: core.ActionCrash, Node: 1}},
		{name: "restart", action: core.Action{Kind: core.ActionRestart, Node: 1}, wantError: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := mapper.ValidateAction(test.action, observation)
			if test.wantError != errors.Is(err, model.ErrActionInapplicable) {
				t.Fatalf("error = %v, want inapplicable=%v", err, test.wantError)
			}
		})
	}
}

func TestProfileAcceptsRestartOnlyForCrashedNode(t *testing.T) {
	mapper := NewMapper()
	observation := profileObservation("MsgVote", "0", "0")
	observation.Nodes[0].Status = core.NodeCrashed
	observation.Nodes[0].Semantic = map[string]any{
		"role": "crashed", "term": uint64(1), "last_index": uint64(0),
	}
	if err := mapper.ValidateAction(core.Action{Kind: core.ActionRestart, Node: 1}, observation); err != nil {
		t.Fatalf("restart crashed node: %v", err)
	}
	if err := mapper.ValidateAction(core.Action{Kind: core.ActionCrash, Node: 1}, observation); !errors.Is(err, model.ErrActionInapplicable) {
		t.Fatalf("crash crashed node error = %v", err)
	}
}

func TestProfileRejectsUnsupportedAndOutOfBoundsMessages(t *testing.T) {
	mapper := NewMapper()
	for _, test := range []struct {
		kind, count, index string
		category           error
	}{
		{kind: "MsgApp", count: "6", index: "0", category: model.ErrModelBoundReached},
		{kind: "MsgApp", count: "invalid", index: "0", category: model.ErrUnsupportedByProfile},
		{kind: "MsgApp", count: "2", index: "4", category: model.ErrModelBoundReached},
		{kind: "MsgApp", count: "1", index: "invalid", category: model.ErrUnsupportedByProfile},
	} {
		observation := profileObservation(test.kind, test.count, test.index)
		err := mapper.ValidateAction(messageAction(core.ActionDeliver), observation)
		if !errors.Is(err, test.category) {
			t.Fatalf("%s/%s error = %v, want category %v", test.kind, test.count, err, test.category)
		}
	}
}

func TestProfileAcceptsBoundedSnapshotMessage(t *testing.T) {
	mapper := NewMapper()
	observation := profileObservation("MsgSnap", "0", "0")
	observation.Messages[0].Metadata["snapshot_index"] = "2"
	observation.Messages[0].Metadata["snapshot_term"] = "1"
	observation.Messages[0].Metadata["snapshot_bytes"] = "128"
	if err := mapper.ValidateAction(messageAction(core.ActionDeliver), observation); err != nil {
		t.Fatalf("bounded MsgSnap rejected: %v", err)
	}
}

func TestProfileSeparatesInapplicableAndModelBound(t *testing.T) {
	config := DefaultConfig()
	config.LargestTerm = 1
	mapper, err := NewMapperWithConfig(config)
	if err != nil {
		t.Fatal(err)
	}
	observation := profileObservation("MsgVote", "0", "0")
	observation.Nodes[0].Semantic["role"] = "follower"
	if err := mapper.ValidateAction(core.Action{Kind: core.ActionRequest, Node: 1, Request: []byte("1")}, observation); err != nil {
		t.Fatalf("follower request should be forwarded or explicitly dropped by etcd-raft: %v", err)
	}
	observation.Nodes[0].Semantic["term"] = uint64(1)
	if err := mapper.ValidateAction(core.Action{Kind: core.ActionTimeout, Node: 1}, observation); !errors.Is(err, model.ErrModelBoundReached) || model.CodeOf(err) != CodeTimeoutTermBound {
		t.Fatalf("timeout error = %v, want model bound", err)
	}
	observation.Nodes[0].Semantic["election_ticks_remaining"] = uint64(1)
	if err := mapper.ValidateAction(core.Action{Kind: core.ActionAdvanceTime, TargetTime: 1}, observation); !errors.Is(err, model.ErrActionInapplicable) || model.CodeOf(err) != CodeAdvanceTermBound {
		t.Fatalf("advance-time error = %v, want inapplicable", err)
	}
	messageObservation := profileObservation("MsgVote", "0", "0")
	messageObservation.Messages[0].Metadata["term"] = "2"
	if err := mapper.ValidateAction(messageAction(core.ActionDeliver), messageObservation); !errors.Is(err, model.ErrModelBoundReached) || model.CodeOf(err) != CodeMessageTermBound {
		t.Fatalf("message error = %v, want model bound", err)
	}
}

func TestProfileAllowsTickThatDoesNotYetFireElectionTimerAtTermBound(t *testing.T) {
	config := DefaultConfig()
	config.LargestTerm = 1
	mapper, err := NewMapperWithConfig(config)
	if err != nil {
		t.Fatal(err)
	}
	observation := profileObservation("MsgVote", "0", "0")
	for index := range observation.Nodes {
		observation.Nodes[index].Semantic["term"] = uint64(1)
		observation.Nodes[index].Semantic["election_ticks_remaining"] = uint64(2)
	}
	if err := mapper.ValidateAction(core.Action{Kind: core.ActionAdvanceTime, TargetTime: 1}, observation); err != nil {
		t.Fatalf("one safe tick should remain applicable: %v", err)
	}
}

func TestProfileAcceptsForwardedProposal(t *testing.T) {
	mapper := NewMapper()
	observation := profileObservation("MsgProp", "1", "0")
	observation.Nodes[1].Semantic["role"] = "leader"
	if err := mapper.ValidateAction(messageAction(core.ActionDeliver), observation); err != nil {
		t.Fatalf("bounded proposal to leader should be represented: %v", err)
	}
	observation.Nodes[1].Semantic["last_index"] = uint64(5)
	if err := mapper.ValidateAction(messageAction(core.ActionDeliver), observation); !errors.Is(err, model.ErrModelBoundReached) || model.CodeOf(err) != CodeMessageLogBound {
		t.Fatalf("full leader log error = %v, want coded model bound", err)
	}
}

func TestProfileAcceptsBoundedMultiEntryAppend(t *testing.T) {
	mapper := NewMapper()
	if err := mapper.ValidateAction(messageAction(core.ActionDeliver), profileObservation("MsgApp", "3", "1")); err != nil {
		t.Fatalf("bounded multi-entry append should be supported: %v", err)
	}
}

func profileObservation(kind, entryCount, index string) core.Observation {
	timerState := func(role string) map[string]any {
		return map[string]any{
			"role": role, "term": uint64(1), "last_index": uint64(0),
			"election_ticks_remaining": uint64(10), "election_timeout": uint64(10),
		}
	}
	return core.Observation{
		Nodes: []core.NodeObservation{
			{ID: 1, Epoch: 1, Status: core.NodeRunning, Semantic: timerState("leader")},
			{ID: 2, Epoch: 1, Status: core.NodeRunning, Semantic: timerState("follower")},
			{ID: 3, Epoch: 1, Status: core.NodeRunning, Semantic: timerState("follower")},
		},
		Messages: []core.MessageObservation{{
			ID: 1, From: 1, To: 2, SenderEpoch: 1, LinkSequence: 1,
			TypeHint: kind, Metadata: map[string]string{
				"entry_count": entryCount, "index": index, "term": "1",
				"log_term": "0", "commit": "0", "reject": "false",
			},
		}},
	}
}

func messageAction(kind core.ActionKind) core.Action {
	return core.Action{Kind: kind, Message: 1, Selector: &core.MessageSelector{
		Link: core.LinkID{From: 1, To: 2}, Position: 0,
	}}
}
