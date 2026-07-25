package core

import (
	"errors"
	"testing"
)

func TestMessageActionValidation(t *testing.T) {
	selector := &MessageSelector{
		Link:     LinkID{From: 1, To: 2},
		Position: 3,
	}

	valid := []Action{
		{Kind: ActionDeliver, Message: 7, Selector: selector},
		{Kind: ActionDrop, Message: 7, Selector: selector},
		{Kind: ActionDuplicate, Message: 7, Selector: selector},
	}
	for _, action := range valid {
		if err := action.Validate(); err != nil {
			t.Errorf("%s action unexpectedly invalid: %v", action.Kind, err)
		}
	}

	invalid := []Action{
		{Kind: ActionDeliver},
		{Kind: ActionDeliver, Message: 7},
		{Kind: ActionDrop, Selector: selector},
		{Kind: ActionDrop, Message: 7, Selector: &MessageSelector{Link: LinkID{From: 1, To: 2}, Position: -1}},
		{Kind: ActionDuplicate, Message: 7, Selector: selector, Node: 1},
	}
	for _, action := range invalid {
		if err := action.Validate(); !errors.Is(err, ErrInvalidValue) {
			t.Errorf("%s action error = %v, want ErrInvalidValue", action.Kind, err)
		}
	}
}

func TestNonMessageActionValidation(t *testing.T) {
	valid := []Action{
		{Kind: ActionAdvanceTime, TargetTime: 10},
		{Kind: ActionTimeout, Node: 1},
		{Kind: ActionCrash, Node: 1},
		{Kind: ActionRestart, Node: 1},
		{Kind: ActionRequest, Node: 1, Request: []byte("write")},
		{Kind: ActionPartition, Partition: &NetworkPartition{Groups: [][]NodeID{{1}, {2, 3}}}},
		{Kind: ActionHeal},
	}
	for _, action := range valid {
		if err := action.Validate(); err != nil {
			t.Errorf("%s action unexpectedly invalid: %v", action.Kind, err)
		}
	}

	invalid := []Action{
		{Kind: ActionAdvanceTime},
		{Kind: ActionTimeout},
		{Kind: ActionTimeout, Node: 1, TargetTime: 10},
		{Kind: ActionRequest, Request: []byte("write")},
		{Kind: ActionPartition},
		{Kind: ActionPartition, Partition: &NetworkPartition{Groups: [][]NodeID{{1, 2}}}},
		{Kind: ActionHeal, Node: 1},
	}
	for _, action := range invalid {
		if err := action.Validate(); !errors.Is(err, ErrInvalidValue) {
			t.Errorf("%s action error = %v, want ErrInvalidValue", action.Kind, err)
		}
	}
}

func TestActionCopyDoesNotAliasMutableFields(t *testing.T) {
	action := Action{
		Kind:    ActionRequest,
		Node:    1,
		Request: []byte("abc"),
	}
	copy := action.Copy()
	copy.Request[0] = 'z'
	if string(action.Request) != "abc" {
		t.Fatalf("copy mutated original request: %q", action.Request)
	}

	selectorAction := Action{
		Kind:    ActionDeliver,
		Message: 7,
		Selector: &MessageSelector{
			Link: LinkID{From: 1, To: 2},
		},
	}
	selectorCopy := selectorAction.Copy()
	selectorCopy.Selector.Position = 4
	if selectorAction.Selector.Position != 0 {
		t.Fatal("copy mutated original selector")
	}

	partitionAction := Action{
		Kind: ActionPartition, Partition: &NetworkPartition{Groups: [][]NodeID{{1}, {2, 3}}},
	}
	partitionCopy := partitionAction.Copy()
	partitionCopy.Partition.Groups[1][0] = 4
	if partitionAction.Partition.Groups[1][0] != 2 {
		t.Fatal("copy mutated original partition")
	}
}
