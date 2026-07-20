package model

import "testing"

func TestEventCopyDoesNotShareNestedParams(t *testing.T) {
	original := NewEvent("DeliverMessage", map[string]any{
		"entries": []map[string]any{{"Term": uint64(1), "Data": "1"}},
	})
	copy := original.Copy()
	copy.Params["entries"].([]map[string]any)[0]["Data"] = "changed"
	if original.Params["entries"].([]map[string]any)[0]["Data"] != "1" {
		t.Fatal("Event.Copy shares nested params with the original")
	}
}

func TestResetEventValidation(t *testing.T) {
	if err := ResetEvent().Validate(); err != nil {
		t.Fatal(err)
	}
	if err := (Event{Reset: true, Name: "Timeout"}).Validate(); err == nil {
		t.Fatal("reset event with a name was accepted")
	}
}
