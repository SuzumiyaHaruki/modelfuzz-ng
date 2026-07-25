package core

import (
	"errors"
	"testing"
)

func TestObservationNormalized(t *testing.T) {
	observation := Observation{
		Nodes: []NodeObservation{
			{ID: 2, Epoch: 1, Status: NodeRunning},
			{ID: 1, Epoch: 1, Status: NodeRunning},
		},
		Messages: []MessageObservation{
			{ID: 2, From: 2, To: 1, SenderEpoch: 1, LinkSequence: 1, Position: 0},
			{ID: 1, From: 1, To: 2, SenderEpoch: 1, LinkSequence: 1, Position: 0, Metadata: map[string]string{"entry_count": "1"}},
		},
	}
	if err := observation.Validate(); err != nil {
		t.Fatalf("observation invalid: %v", err)
	}

	normalized := observation.Normalized()
	if normalized.Nodes[0].ID != 1 || normalized.Messages[0].ID != 1 {
		t.Fatalf("observation was not normalized: %+v", normalized)
	}
	if observation.Nodes[0].ID != 2 {
		t.Fatal("normalization mutated original observation")
	}
	normalized.Messages[0].Metadata["entry_count"] = "2"
	if observation.Messages[1].Metadata["entry_count"] != "1" {
		t.Fatal("normalization shares message metadata with original")
	}
}

func TestObservationRejectsDuplicateIDs(t *testing.T) {
	observation := Observation{
		Nodes: []NodeObservation{
			{ID: 1, Epoch: 1, Status: NodeRunning},
			{ID: 1, Epoch: 2, Status: NodeCrashed},
		},
	}
	if err := observation.Validate(); !errors.Is(err, ErrInvalidValue) {
		t.Fatalf("duplicate node error = %v, want ErrInvalidValue", err)
	}
}

func TestObservationRejectsNonJSONSemanticState(t *testing.T) {
	observation := Observation{
		Nodes: []NodeObservation{{
			ID: 1, Epoch: 1, Status: NodeRunning,
			Semantic: map[string]any{"channel": make(chan struct{})},
		}},
	}
	if err := observation.Validate(); !errors.Is(err, ErrInvalidValue) {
		t.Fatalf("non-JSON semantic error = %v, want ErrInvalidValue", err)
	}
}

func TestObservationValidatesAndCopiesNetworkPartition(t *testing.T) {
	partition := NetworkPartition{Groups: [][]NodeID{{3, 2}, {1}}}
	observation := Observation{
		Nodes: []NodeObservation{
			{ID: 1, Epoch: 1, Status: NodeRunning},
			{ID: 2, Epoch: 1, Status: NodeRunning},
			{ID: 3, Epoch: 1, Status: NodeRunning},
		},
		Messages: []MessageObservation{
			{ID: 1, From: 1, To: 2, SenderEpoch: 1, LinkSequence: 1, Blocked: true},
			{ID: 2, From: 2, To: 3, SenderEpoch: 1, LinkSequence: 1},
		},
		NetworkPartition: &partition,
	}
	if err := observation.Validate(); err != nil {
		t.Fatal(err)
	}
	normalized := observation.Normalized()
	if normalized.NetworkPartition.Groups[0][0] != 1 || normalized.NetworkPartition.Groups[1][0] != 2 {
		t.Fatalf("normalized partition = %+v", normalized.NetworkPartition)
	}
	normalized.NetworkPartition.Groups[0][0] = 9
	if observation.NetworkPartition.Groups[1][0] != 1 {
		t.Fatal("partition copy aliases original")
	}

	invalid := observation.Copy()
	invalid.Messages[0].Blocked = false
	if err := invalid.Validate(); !errors.Is(err, ErrInvalidValue) {
		t.Fatalf("invalid blocked flag error = %v", err)
	}
}
