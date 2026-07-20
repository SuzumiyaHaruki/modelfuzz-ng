package core

import "testing"

func TestFailureRecordCopyDoesNotShareMutableState(t *testing.T) {
	action := Action{Kind: ActionRequest, Node: 1, Request: []byte("set x=1")}
	original := FailureRecord{
		Kind: FailureSUTPanic, Operation: "request", Time: 3, Action: &action,
		Error: "request panicked", PanicValue: "boom", Stack: "goroutine 1",
		ObservationBefore: Observation{Time: 3, Nodes: []NodeObservation{{
			ID: 1, Epoch: 1, Status: NodeRunning,
			Semantic: map[string]any{"metadata": map[string]string{"role": "leader"}},
		}}},
	}
	if err := original.Validate(); err != nil {
		t.Fatal(err)
	}

	copy := original.Copy()
	copy.Action.Request[0] = 'X'
	copy.ObservationBefore.Nodes[0].Semantic["metadata"].(map[string]string)["role"] = "follower"
	if string(original.Action.Request) != "set x=1" {
		t.Fatalf("action request was aliased: %q", original.Action.Request)
	}
	if got := original.ObservationBefore.Nodes[0].Semantic["metadata"].(map[string]string)["role"]; got != "leader" {
		t.Fatalf("observation semantic was aliased: %q", got)
	}
}

func TestFailureRecordRequiresPanicDetails(t *testing.T) {
	record := FailureRecord{Kind: FailureSUTPanic, Operation: "tick", Error: "failed"}
	if err := record.Validate(); err == nil {
		t.Fatal("panic without value and stack was accepted")
	}
}
