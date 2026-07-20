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
		{name: "crash", action: core.Action{Kind: core.ActionCrash, Node: 1}, wantError: true},
		{name: "restart", action: core.Action{Kind: core.ActionRestart, Node: 1}, wantError: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := mapper.ValidateAction(test.action, observation)
			if test.wantError != errors.Is(err, model.ErrUnsupportedByProfile) {
				t.Fatalf("error = %v, want unsupported=%v", err, test.wantError)
			}
		})
	}
}

func TestProfileRejectsUnsupportedAndOutOfBoundsMessages(t *testing.T) {
	mapper := NewMapper()
	for _, test := range []struct {
		kind, count, index string
	}{
		{kind: "MsgSnap", count: "0", index: "0"},
		{kind: "MsgApp", count: "6", index: "0"},
		{kind: "MsgApp", count: "invalid", index: "0"},
		{kind: "MsgApp", count: "2", index: "4"},
		{kind: "MsgApp", count: "1", index: "invalid"},
	} {
		observation := profileObservation(test.kind, test.count, test.index)
		err := mapper.ValidateAction(messageAction(core.ActionDeliver), observation)
		if !errors.Is(err, model.ErrUnsupportedByProfile) {
			t.Fatalf("%s/%s error = %v", test.kind, test.count, err)
		}
	}
}

func TestProfileAcceptsBoundedMultiEntryAppend(t *testing.T) {
	mapper := NewMapper()
	if err := mapper.ValidateAction(messageAction(core.ActionDeliver), profileObservation("MsgApp", "3", "1")); err != nil {
		t.Fatalf("bounded multi-entry append should be supported: %v", err)
	}
}

func profileObservation(kind, entryCount, index string) core.Observation {
	return core.Observation{
		Nodes: []core.NodeObservation{
			{ID: 1, Epoch: 1, Status: core.NodeRunning, Semantic: map[string]any{"role": "leader", "term": uint64(1), "last_index": uint64(0)}},
			{ID: 2, Epoch: 1, Status: core.NodeRunning},
			{ID: 3, Epoch: 1, Status: core.NodeRunning},
		},
		Messages: []core.MessageObservation{{
			ID: 1, From: 1, To: 2, SenderEpoch: 1, LinkSequence: 1,
			TypeHint: kind, Metadata: map[string]string{"entry_count": entryCount, "index": index},
		}},
	}
}

func messageAction(kind core.ActionKind) core.Action {
	return core.Action{Kind: kind, Message: 1, Selector: &core.MessageSelector{
		Link: core.LinkID{From: 1, To: 2}, Position: 0,
	}}
}
