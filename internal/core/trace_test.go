package core

import (
	"errors"
	"testing"
)

func TestTraceAppendAndCopy(t *testing.T) {
	trace := NewTrace("execution-1", 42)
	nodes := traceTestNodes()
	step := StepRecord{
		Index:       0,
		TimeBefore:  0,
		TimeAfter:   1,
		Action:      Action{Kind: ActionAdvanceTime, TargetTime: 1},
		NodesBefore: nodes,
		NodesAfter:  nodes,
	}
	if err := trace.Append(step); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := trace.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}

	copy := trace.Copy()
	copy.Steps[0].Action.TargetTime = 2
	copy.Steps[0].NodesAfter[0].Semantic["role"] = "leader"
	if trace.Steps[0].Action.TargetTime != 1 {
		t.Fatal("copy mutated original trace")
	}
	if trace.Steps[0].NodesAfter[0].Semantic["role"] != "follower" {
		t.Fatal("copy mutated original node snapshot")
	}
}

func TestTraceRejectsDiscontinuousIndexAndTimeRegression(t *testing.T) {
	trace := NewTrace("execution-1", 42)
	nodes := traceTestNodes()
	if err := trace.Append(StepRecord{
		Index: 0, TimeBefore: 0, TimeAfter: 2,
		Action: Action{Kind: ActionAdvanceTime, TargetTime: 2}, NodesBefore: nodes, NodesAfter: nodes,
	}); err != nil {
		t.Fatal(err)
	}

	err := trace.Append(StepRecord{
		Index: 2, TimeBefore: 2, TimeAfter: 3,
		Action: Action{Kind: ActionAdvanceTime, TargetTime: 3}, NodesBefore: nodes, NodesAfter: nodes,
	})
	if !errors.Is(err, ErrInvalidValue) {
		t.Fatalf("discontinuous index error = %v, want ErrInvalidValue", err)
	}

	err = trace.Append(StepRecord{
		Index: 1, TimeBefore: 1, TimeAfter: 3,
		Action: Action{Kind: ActionAdvanceTime, TargetTime: 3}, NodesBefore: nodes, NodesAfter: nodes,
	})
	if !errors.Is(err, ErrInvalidValue) {
		t.Fatalf("time regression error = %v, want ErrInvalidValue", err)
	}
}

func TestTraceVersionTwoRequiresNodeSnapshots(t *testing.T) {
	trace := NewTrace("execution-1", 42)
	err := trace.Append(StepRecord{
		Index: 0, TimeBefore: 0, TimeAfter: 1,
		Action: Action{Kind: ActionAdvanceTime, TargetTime: 1},
	})
	if !errors.Is(err, ErrInvalidValue) {
		t.Fatalf("missing snapshots error = %v, want ErrInvalidValue", err)
	}
}

func traceTestNodes() []NodeObservation {
	return []NodeObservation{{
		ID: 1, Epoch: 1, Status: NodeRunning,
		Semantic: map[string]any{"role": "follower"},
	}}
}

func TestStepRecordEnforcesClockAndEffectTimes(t *testing.T) {
	timerEvent := TimerFired{Node: 1, Epoch: 1, Source: TimerFireNatural}
	valid := StepRecord{
		Index:      0,
		TimeBefore: 10,
		TimeAfter:  12,
		Action:     Action{Kind: ActionAdvanceTime, TargetTime: 12},
		Effects: []Effect{
			{At: 11, Kind: EffectTimerFired, TimerFired: &timerEvent},
		},
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid timed step rejected: %v", err)
	}

	invalidEffectTime := valid.Copy()
	invalidEffectTime.Effects[0].At = 13
	if err := invalidEffectTime.Validate(); !errors.Is(err, ErrInvalidValue) {
		t.Fatalf("out-of-range effect error = %v, want ErrInvalidValue", err)
	}

	outOfOrder := valid.Copy()
	outOfOrder.Effects = append(outOfOrder.Effects,
		Effect{At: 10, Kind: EffectTimerFired, TimerFired: &timerEvent})
	if err := outOfOrder.Validate(); !errors.Is(err, ErrInvalidValue) {
		t.Fatalf("out-of-order effect error = %v, want ErrInvalidValue", err)
	}

	invalidClockMove := StepRecord{
		Index: 0, TimeBefore: 10, TimeAfter: 11,
		Action: Action{Kind: ActionTimeout, Node: 1},
	}
	if err := invalidClockMove.Validate(); !errors.Is(err, ErrInvalidValue) {
		t.Fatalf("non-time clock move error = %v, want ErrInvalidValue", err)
	}
}
