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
			{ID: 1, From: 1, To: 2, SenderEpoch: 1, LinkSequence: 1, Position: 0},
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
