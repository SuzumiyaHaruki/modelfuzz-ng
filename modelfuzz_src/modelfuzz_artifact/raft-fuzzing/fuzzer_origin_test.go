package main

import (
	"testing"

	pb "github.com/zeu5/raft-fuzzing/raft/raftpb"
)

func TestDeliveryAndContextEventsCopyTheirOrigins(t *testing.T) {
	events := NewList[*Event]()
	deliveryOrigin := &EventOrigin{
		Step:            4,
		Phase:           EventPhaseDeliver,
		ChoiceIndex:     9,
		DeliveryOrdinal: 2,
		DeliveryCount:   5,
	}
	recordReceive(pb.Message{From: 1, To: 2, Type: pb.MsgApp}, events, deliveryOrigin)
	deliveryOrigin.DeliveryOrdinal = 99

	tickOrigin := &EventOrigin{
		Step:            4,
		Phase:           EventPhaseTick,
		ChoiceIndex:     -1,
		DeliveryOrdinal: -1,
		DeliveryCount:   -1,
	}
	ctx := &FuzzContext{
		traceCtx: &traceCtx{eventTrace: events},
		origin:   tickOrigin,
	}
	ctx.AddEvent(&Event{Name: "Timeout", Params: map[string]interface{}{"node": 2}})
	tickOrigin.Step = 99

	delivery, _ := events.Get(0)
	if delivery.Origin == nil || delivery.Origin.DeliveryOrdinal != 2 || delivery.Origin.ChoiceIndex != 9 {
		t.Fatalf("delivery origin was not preserved: %#v", delivery.Origin)
	}
	tick, _ := events.Get(1)
	if tick.Origin == nil || tick.Origin.Step != 4 || tick.Origin.Phase != EventPhaseTick {
		t.Fatalf("context origin was not preserved: %#v", tick.Origin)
	}
}
