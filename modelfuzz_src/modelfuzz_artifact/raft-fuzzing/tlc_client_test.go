package main

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func testJSONResponse(body string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func TestTLCClientSendTraceDoesNotMutateInput(t *testing.T) {
	var received []map[string]interface{}
	client := NewTLCClient("http://tlc.test")
	client.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/execute" {
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		return testJSONResponse(`{"states":["state-7"],"keys":[7]}`), nil
	})}

	trace := NewList[*Event]()
	trace.Append(&Event{
		Name: "Timeout",
		Params: map[string]interface{}{
			"node": 1,
		},
		Origin: &EventOrigin{Step: 3, Phase: EventPhaseTick, ChoiceIndex: -1},
	})

	states, err := client.SendTrace(trace)
	if err != nil {
		t.Fatalf("SendTrace: %v", err)
	}
	if trace.Size() != 1 {
		t.Fatalf("SendTrace mutated input trace: size=%d, want 1", trace.Size())
	}
	if len(states) != 1 || states[0].Key != 7 {
		t.Fatalf("unexpected states: %#v", states)
	}
	if len(received) != 2 {
		t.Fatalf("server received %d events, want event plus Reset", len(received))
	}
	if reset, _ := received[1]["Reset"].(bool); !reset {
		t.Fatalf("last event is not Reset: %#v", received[1])
	}
	if _, ok := received[0]["Origin"]; ok {
		t.Fatalf("local Origin metadata leaked into TLC request: %#v", received[0])
	}
}

func TestTLCClientRejectsMismatchedStateAndKeyCounts(t *testing.T) {
	client := NewTLCClient("http://tlc.test")
	client.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return testJSONResponse(`{"states":["one","two"],"keys":[1]}`), nil
	})}

	if _, err := client.SendTrace(NewList[*Event]()); err == nil {
		t.Fatal("SendTrace accepted mismatched state/key arrays")
	}
}

func TestTLCClientReadsTransitionProvenance(t *testing.T) {
	client := NewTLCClient("http://tlc.test")
	client.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return testJSONResponse(`{
			"states":["initial","after-remove"],
			"keys":[1,2],
			"stateEventIndices":[-1,0],
			"transitions":[{
				"eventIndex":0,
				"inputName":"Remove",
				"mappedAction":"RemoveFromActive",
				"status":"executed",
				"preKey":1,
				"postKey":2
			}]
		}`), nil
	})}

	trace := NewList[*Event]()
	trace.Append(&Event{Name: "Remove", Params: map[string]interface{}{"i": 3}})
	execution, err := client.ExecuteTrace(trace)
	if err != nil {
		t.Fatalf("ExecuteTrace: %v", err)
	}
	if !execution.ProvenanceAvailable || len(execution.States) == 0 || execution.States[0].Key != 1 {
		t.Fatalf("missing provenance: %#v", execution)
	}
	if len(execution.Transitions) != 1 || execution.Transitions[0].PostKey != 2 || execution.Transitions[0].EventIndex != 0 {
		t.Fatalf("unexpected transitions: %#v", execution.Transitions)
	}
	if len(execution.StateEventIndices) != 2 || execution.StateEventIndices[0] != -1 || execution.StateEventIndices[1] != 0 {
		t.Fatalf("unexpected state origins: %#v", execution.StateEventIndices)
	}
}

func TestTLCClientRejectsMismatchedStateOriginCount(t *testing.T) {
	client := NewTLCClient("http://tlc.test")
	client.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return testJSONResponse(`{"states":["initial"],"keys":[1],"stateEventIndices":[],"transitions":[]}`), nil
	})}
	if _, err := client.ExecuteTrace(NewList[*Event]()); err == nil {
		t.Fatal("ExecuteTrace accepted mismatched state origin count")
	}
}

func TestTLCClientRejectsMismatchedTransitionCount(t *testing.T) {
	client := NewTLCClient("http://tlc.test")
	client.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return testJSONResponse(`{"states":["initial"],"keys":[1],"transitions":[]}`), nil
	})}
	trace := NewList[*Event]()
	trace.Append(&Event{Name: "Timeout", Params: map[string]interface{}{"node": 1}})
	if _, err := client.ExecuteTrace(trace); err == nil {
		t.Fatal("ExecuteTrace accepted a response missing the input event transition")
	}
}
