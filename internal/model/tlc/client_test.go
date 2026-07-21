package tlc

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/model"
)

func TestExecuteAppendsResetWithoutMutatingInput(t *testing.T) {
	var received []model.Event
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/execute" || request.Method != http.MethodPost {
			t.Fatalf("request = %s %s", request.Method, request.URL.Path)
		}
		if err := json.NewDecoder(request.Body).Decode(&received); err != nil {
			t.Fatal(err)
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"States":["s1"],"Keys":[17]}`))
	}))
	defer server.Close()

	client, err := NewClientWithHTTPClient(server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	events := []model.Event{model.NewEvent("Timeout", map[string]any{"node": 1})}
	states, err := client.Execute(context.Background(), events)
	if err != nil {
		t.Fatal(err)
	}
	if len(states) != 1 || states[0].Text != "s1" || states[0].Key != 17 {
		t.Fatalf("states = %+v", states)
	}
	if len(events) != 1 || events[0].Reset {
		t.Fatalf("input events were mutated: %+v", events)
	}
	if len(received) != 2 || !received[1].Reset || received[1].Name != "" {
		t.Fatalf("request events = %+v", received)
	}
}

func TestExecuteRejectsMismatchedResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte(`{"States":["s1"],"Keys":[]}`))
	}))
	defer server.Close()
	client, err := NewClientWithHTTPClient(server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Execute(context.Background(), []model.Event{model.NewEvent("Timeout", map[string]any{"node": 1})})
	if err == nil {
		t.Fatal("Execute succeeded with mismatched states and keys")
	}
}

func TestExecuteReturnsStructuredStrictServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = writer.Write([]byte(`{"error":{"code":"disabled_action","event_index":3,"event_name":"Timeout","message":"action is disabled"}}`))
	}))
	defer server.Close()
	client, err := NewClientWithHTTPClient(server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Execute(context.Background(), []model.Event{model.NewEvent("Timeout", map[string]any{"node": 1})})
	var executionError *ExecutionError
	if !errors.As(err, &executionError) || executionError.StatusCode != http.StatusUnprocessableEntity ||
		executionError.Code != "disabled_action" || executionError.EventIndex != 3 || executionError.EventName != "Timeout" {
		t.Fatalf("structured error = %#v / %v", executionError, err)
	}
}

func TestMetricsUsesDedicatedEndpoint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/metrics" || request.Method != http.MethodGet {
			t.Fatalf("request = %s %s", request.Method, request.URL.Path)
		}
		_, _ = writer.Write([]byte(`{"requests":3,"succeeded":2,"failed":1,"model_events":7,"action_lookups":7,"errors_by_code":{"disabled_action":1},"timing":{"mapping_nanos":10,"action_lookup_nanos":2,"successor_nanos":20,"validation_nanos":3,"serialization_nanos":4}}`))
	}))
	defer server.Close()
	client, err := NewClientWithHTTPClient(server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	metrics, err := client.Metrics(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if metrics.Requests != 3 || metrics.ErrorsByCode["disabled_action"] != 1 || metrics.Timing.SuccessorNanos != 20 {
		t.Fatalf("metrics = %+v", metrics)
	}
}

func TestBoundsUsesHealthEndpoint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/health" || request.Method != http.MethodGet {
			t.Fatalf("request = %s %s", request.Method, request.URL.Path)
		}
		_, _ = writer.Write([]byte(`{"max_log_index":10,"largest_term":10}`))
	}))
	defer server.Close()
	client, err := NewClientWithHTTPClient(server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	bounds, err := client.Bounds(context.Background())
	if err != nil || bounds.MaxLogIndex != 10 || bounds.LargestTerm != 10 {
		t.Fatalf("bounds = %+v, err = %v", bounds, err)
	}
}
