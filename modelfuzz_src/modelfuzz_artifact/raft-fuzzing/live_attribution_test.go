package main

import (
	"os"
	"testing"
)

// TestLiveTLCStateAttributionSmoke is skipped during normal test runs. Enable it explicitly
// after starting a TLC server to verify direct provenance or the legacy prefix fallback.
func TestLiveTLCStateAttributionSmoke(t *testing.T) {
	if os.Getenv("TLC_LIVE_SMOKE") != "1" {
		t.Skip("set TLC_LIVE_SMOKE=1 to run against a real TLC server")
	}
	addr := os.Getenv("TLC_ADDR")
	if addr == "" {
		addr = "127.0.0.1:2023"
	}

	events := NewList[*Event]()
	events.Append(&Event{
		Name:   "Timeout",
		Node:   1,
		Params: map[string]interface{}{"node": 1},
		Origin: &EventOrigin{Step: 0, Phase: EventPhaseTick, ChoiceIndex: -1, DeliveryOrdinal: -1, DeliveryCount: -1},
	})
	events.Append(&Event{
		Name:   "BecomeLeader",
		Node:   1,
		Params: map[string]interface{}{"node": 1},
		Origin: &EventOrigin{Step: 1, Phase: EventPhaseTick, ChoiceIndex: -1, DeliveryOrdinal: -1, DeliveryCount: -1},
	})
	events.Append(&Event{
		Name:   "ClientRequest",
		Node:   1,
		Params: map[string]interface{}{"leader": 1, "request": 1},
		Origin: &EventOrigin{Step: 2, Phase: EventPhaseClientRequest, ChoiceIndex: 0, DeliveryOrdinal: -1, DeliveryCount: -1},
	})
	trace := NewList[*SchedulingChoice]()
	trace.Append(&SchedulingChoice{Type: ClientRequest, Request: 1, Step: 2})

	guider := NewTLCStateGuider(addr, "", false).WithStateAttribution(true)
	newStates, _ := guider.Check(trace, events)
	if newStates != 3 {
		t.Fatalf("expected initial, leader, and client-request states, got %d", newStates)
	}
	located := make(map[int]StateAttribution)
	for _, hit := range guider.LastGuidance().NewStates {
		if hit.Status == AttributionLocated && hit.Origin != nil {
			located[hit.EventIndex] = hit
			t.Logf("located new state key=%d at event=%d origin=%+v", hit.State.Key, hit.EventIndex, *hit.Origin)
		}
	}
	for eventIndex, phase := range map[int]EventPhase{1: EventPhaseTick, 2: EventPhaseClientRequest} {
		hit, ok := located[eventIndex]
		if !ok || hit.Origin.Phase != phase {
			t.Fatalf("event %d attribution missing or incorrect: %#v", eventIndex, hit)
		}
	}
	if _, ok := located[0]; ok {
		t.Fatalf("abstract Timeout state unexpectedly reported as new: %#v", located[0])
	}
}

// TestLiveTLCIgnoredEventsPreserveEventIndex verifies that ignored implementation
// events do not collapse the source index used by direct transition provenance.
func TestLiveTLCIgnoredEventsPreserveEventIndex(t *testing.T) {
	if os.Getenv("TLC_LIVE_SMOKE") != "1" {
		t.Skip("set TLC_LIVE_SMOKE=1 to run against a real TLC server")
	}
	addr := os.Getenv("TLC_ADDR")
	if addr == "" {
		addr = "127.0.0.1:2023"
	}

	events := NewList[*Event]()
	events.Append(&Event{
		Name: "Remove", Params: map[string]interface{}{"i": 3},
		Origin: &EventOrigin{Step: 1, Phase: EventPhaseCrash, ChoiceIndex: 1, DeliveryOrdinal: -1, DeliveryCount: -1},
	})
	events.Append(&Event{
		Name: "SendMessage", Params: map[string]interface{}{},
		Origin: &EventOrigin{Step: 2, Phase: EventPhaseTick, ChoiceIndex: -1, DeliveryOrdinal: -1, DeliveryCount: -1},
	})
	events.Append(&Event{
		Name: "Timeout", Params: map[string]interface{}{"node": 1},
		Origin: &EventOrigin{Step: 3, Phase: EventPhaseTick, ChoiceIndex: -1, DeliveryOrdinal: -1, DeliveryCount: -1},
	})
	events.Append(&Event{
		Name: "BecomeLeader", Params: map[string]interface{}{"node": 1},
		Origin: &EventOrigin{Step: 4, Phase: EventPhaseTick, ChoiceIndex: -1, DeliveryOrdinal: -1, DeliveryCount: -1},
	})

	client := NewTLCClient(addr)
	execution, err := client.ExecuteTrace(events)
	if err != nil {
		t.Fatalf("execute provenance trace: %v", err)
	}
	if !execution.ProvenanceAvailable || len(execution.Transitions) != events.Size() {
		t.Fatalf("missing live transition provenance: %#v", execution)
	}
	wantStatuses := []string{"ignored", "ignored", "executed", "executed"}
	for i, transition := range execution.Transitions {
		if transition.EventIndex != i || transition.Status != wantStatuses[i] {
			t.Fatalf("transition %d=%#v, want status %q", i, transition, wantStatuses[i])
		}
	}

	guider := NewTLCStateGuider(addr, "", false).WithStateAttribution(true)
	newStates, _ := guider.Check(NewList[*SchedulingChoice](), events)
	if newStates != 2 {
		t.Fatalf("expected initial and leader states, got %d", newStates)
	}

	located := 0
	for _, hit := range guider.LastGuidance().NewStates {
		if hit.Source != AttributionSourceTransition {
			t.Fatalf("state %d used %q instead of server provenance", hit.State.Key, hit.Source)
		}
		if hit.Status == AttributionLocated {
			located++
			if hit.EventIndex != 3 {
				t.Fatalf("leader attributed to event %d instead of original index 3: %#v", hit.EventIndex, hit)
			}
		}
	}
	if located != 1 {
		t.Fatalf("located states=%d, want only leader", located)
	}
	stats := guider.AttributionStats()
	if stats.ProvenanceChecks != 1 || stats.TransitionRecords != events.Size() || stats.PrefixRequests != 0 {
		t.Fatalf("unexpected live provenance stats: %#v", stats)
	}
}
