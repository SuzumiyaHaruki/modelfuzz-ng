package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
)

func TestTLCStateGuiderLocatesNewStatesByEventPrefix(t *testing.T) {
	client := NewTLCClient("http://tlc.test")
	client.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		var events []Event
		if err := json.NewDecoder(r.Body).Decode(&events); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		prefixLength := 0
		for _, event := range events {
			if !event.Reset {
				prefixLength++
			}
		}

		states := []string{"initial"}
		keys := []int64{1}
		if prefixLength >= 2 {
			states = append(states, "after-event-1")
			keys = append(keys, 42)
		}
		if prefixLength >= 4 {
			states = append(states, "after-event-3")
			keys = append(keys, 99)
		}
		response, err := json.Marshal(TLCResponse{States: states, Keys: keys})
		if err != nil {
			t.Fatalf("encode response: %v", err)
		}
		return testJSONResponse(string(response)), nil
	})}

	events := NewList[*Event]()
	for i := 0; i < 5; i++ {
		events.Append(&Event{
			Name:   fmt.Sprintf("event-%d", i),
			Params: map[string]interface{}{},
			Origin: &EventOrigin{
				Step:            10 + i,
				Phase:           EventPhaseDeliver,
				ChoiceIndex:     20 + i,
				DeliveryOrdinal: i,
				DeliveryCount:   5,
			},
		})
	}

	guider := NewTLCStateGuider("http://tlc.test", "", false).WithStateAttribution(true)
	guider.tlcClient = client
	newStates, _ := guider.Check(NewList[*SchedulingChoice](), events)
	if newStates != 3 {
		t.Fatalf("new states=%d, want initial plus two transitions", newStates)
	}
	if events.Size() != 5 {
		t.Fatalf("prefix probing mutated event trace: size=%d, want 5", events.Size())
	}

	hits := make(map[int64]StateAttribution)
	for _, hit := range guider.LastGuidance().NewStates {
		hits[hit.State.Key] = hit
	}
	if hit := hits[1]; hit.Status != AttributionInitialState || hit.EventIndex != -1 {
		t.Fatalf("initial state attribution=%#v", hit)
	}
	if hit := hits[42]; hit.Status != AttributionLocated || hit.EventIndex != 1 || hit.Origin == nil || hit.Origin.ChoiceIndex != 21 {
		t.Fatalf("state 42 attribution=%#v", hit)
	}
	if hit := hits[99]; hit.Status != AttributionLocated || hit.EventIndex != 3 || hit.Origin == nil || hit.Origin.Step != 13 {
		t.Fatalf("state 99 attribution=%#v", hit)
	}
	stats := guider.AttributionStats()
	if stats.Checks != 1 || stats.Events != 5 || stats.NewStates != 3 || stats.Located != 2 || stats.InitialStates != 1 || stats.Failed != 0 {
		t.Fatalf("unexpected first-check attribution stats: %#v", stats)
	}
	if stats.PrefixRequests == 0 || stats.PrefixCacheHits == 0 {
		t.Fatalf("prefix probe counters were not recorded: %#v", stats)
	}

	newStates, _ = guider.Check(NewList[*SchedulingChoice](), events)
	if newStates != 0 || len(guider.LastGuidance().NewStates) != 0 {
		t.Fatalf("second Check reported old states as new: count=%d guidance=%#v", newStates, guider.LastGuidance())
	}
	stats = guider.AttributionStats()
	if stats.Checks != 2 || stats.Events != 10 || stats.NewStates != 3 {
		t.Fatalf("cumulative attribution stats were not updated: %#v", stats)
	}
}

func TestTLCStateGuiderPrefersServerTransitionProvenance(t *testing.T) {
	requests := 0
	client := NewTLCClient("http://tlc.test")
	client.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		requests++
		transitions := []TLCTransition{
			{EventIndex: 0, InputName: "Remove", MappedAction: "RemoveFromActive", Status: "executed", PreKey: 1, PostKey: 2},
			{EventIndex: 1, InputName: "Timeout", MappedAction: "Timeout", Status: "executed", PreKey: 2, PostKey: 3},
			{EventIndex: 2, InputName: "SendMessage", Status: "ignored", PreKey: 3, PostKey: 3},
			{EventIndex: 3, InputName: "SendMessage", Status: "ignored", PreKey: 3, PostKey: 3},
			{EventIndex: 4, InputName: "Remove", MappedAction: "RemoveFromActive", Status: "executed", PreKey: 3, PostKey: 4},
		}
		response, err := json.Marshal(TLCResponse{
			States:      []string{"A", "B", "C"},
			Keys:        []int64{1, 2, 4},
			Transitions: &transitions,
		})
		if err != nil {
			t.Fatalf("encode response: %v", err)
		}
		return testJSONResponse(string(response)), nil
	})}

	events := NewList[*Event]()
	names := []string{"Remove", "Timeout", "SendMessage", "SendMessage", "Remove"}
	for i, name := range names {
		events.Append(&Event{
			Name:   name,
			Params: map[string]interface{}{},
			Origin: &EventOrigin{Step: i, Phase: EventPhaseTick, ChoiceIndex: i},
		})
	}

	guider := NewTLCStateGuider("http://tlc.test", "", false).WithStateAttribution(true)
	guider.tlcClient = client
	newStates, _ := guider.Check(NewList[*SchedulingChoice](), events)
	if newStates != 3 {
		t.Fatalf("new states=%d, want 3", newStates)
	}
	hits := make(map[int64]StateAttribution)
	for _, hit := range guider.LastGuidance().NewStates {
		hits[hit.State.Key] = hit
	}
	if hit := hits[1]; hit.Status != AttributionInitialState || hit.Source != AttributionSourceTransition {
		t.Fatalf("initial state attribution=%#v", hit)
	}
	if hit := hits[2]; hit.Status != AttributionLocated || hit.EventIndex != 0 || hit.Source != AttributionSourceTransition {
		t.Fatalf("state B attribution=%#v", hit)
	}
	if hit := hits[4]; hit.Status != AttributionLocated || hit.EventIndex != 4 || hit.Source != AttributionSourceTransition {
		t.Fatalf("state C attribution=%#v", hit)
	}
	stats := guider.AttributionStats()
	if requests != 1 || stats.PrefixRequests != 0 || stats.PrefixFallbackChecks != 0 {
		t.Fatalf("transition attribution unexpectedly replayed prefixes: requests=%d stats=%#v", requests, stats)
	}
	if stats.ProvenanceChecks != 1 || stats.ProvenanceAttributions != 3 || stats.TransitionRecords != 5 {
		t.Fatalf("unexpected provenance stats: %#v", stats)
	}
}

func TestClientRequestChoicePreservesStep(t *testing.T) {
	ctx := &traceCtx{
		trace:          NewList[*SchedulingChoice](),
		clientRequests: map[int]int{7: 3},
	}
	req, choiceIndex, ok := ctx.IsClientRequest(7)
	if !ok || req != 3 || choiceIndex != 0 {
		t.Fatalf("IsClientRequest=(%d,%d,%t), want (3,0,true)", req, choiceIndex, ok)
	}
	choice, _ := ctx.trace.Get(0)
	if choice.Step != 7 {
		t.Fatalf("ClientRequest step=%d, want 7", choice.Step)
	}
}

func TestPlannedMembershipChoiceDoesNotRecordUnexecutedEvent(t *testing.T) {
	ctx := &traceCtx{
		trace:       NewList[*SchedulingChoice](),
		eventTrace:  NewList[*Event](),
		crashPoints: map[int]uint64{2: 1},
		startPoints: map[int]uint64{3: 1},
	}
	if _, _, ok := ctx.CanCrash(2); !ok {
		t.Fatal("planned crash was not returned")
	}
	if _, _, ok := ctx.CanStart(3); !ok {
		t.Fatal("planned restart was not returned")
	}
	if ctx.trace.Size() != 2 {
		t.Fatalf("choice trace size=%d, want 2", ctx.trace.Size())
	}
	if ctx.eventTrace.Size() != 0 {
		t.Fatalf("planned but unexecuted membership choices emitted %d events", ctx.eventTrace.Size())
	}
}
