package core

import (
	"errors"
	"testing"
)

func TestEffectValidation(t *testing.T) {
	message := validRegisteredMessage()
	fired := TimerFired{
		Node: 1, Epoch: 1, Source: TimerFireNatural,
		TypeHint: "election", RoleHint: "follower",
	}
	event := ModelEvent{Name: "BecomeLeader", Node: 1}

	valid := []Effect{
		{Kind: EffectSendMessage, Message: &message},
		{At: 10, Kind: EffectTimerFired, TimerFired: &fired},
		{Kind: EffectModelEvent, ModelEvent: &event},
	}
	for _, effect := range valid {
		if err := effect.Validate(); err != nil {
			t.Errorf("%s effect unexpectedly invalid: %v", effect.Kind, err)
		}
	}

	invalid := Effect{Kind: EffectSendMessage, Message: &message, TimerFired: &fired}
	if err := invalid.Validate(); !errors.Is(err, ErrInvalidValue) {
		t.Fatalf("multi-payload error = %v, want ErrInvalidValue", err)
	}
}

func TestEffectCopyDoesNotAliasMetadata(t *testing.T) {
	event := ModelEvent{Name: "event", Params: map[string]any{
		"value":   1,
		"entries": []map[string]any{{"Term": uint64(1), "Data": "1"}},
	}}
	effect := Effect{Kind: EffectModelEvent, ModelEvent: &event}
	copy := effect.Copy()
	copy.ModelEvent.Params["value"] = 2
	copy.ModelEvent.Params["entries"].([]map[string]any)[0]["Data"] = "changed"
	if effect.ModelEvent.Params["value"] != 1 {
		t.Fatal("copy mutated original event params")
	}
	if effect.ModelEvent.Params["entries"].([]map[string]any)[0]["Data"] != "1" {
		t.Fatal("copy mutated nested original event params")
	}
}

func TestModelEventRejectsNonJSONParams(t *testing.T) {
	event := ModelEvent{Name: "invalid", Params: map[string]any{
		"callback": func() {},
	}}
	if err := event.Validate(); !errors.Is(err, ErrInvalidValue) {
		t.Fatalf("non-JSON params error = %v, want ErrInvalidValue", err)
	}
}

func TestTimerFiredValidationAndCopy(t *testing.T) {
	fired := TimerFired{
		Node:     1,
		Epoch:    1,
		Source:   TimerFireForced,
		Metadata: map[string]string{"term": "3"},
	}
	if err := fired.Validate(); err != nil {
		t.Fatalf("timer-fired event invalid: %v", err)
	}

	copy := fired.Copy()
	copy.Metadata["term"] = "4"
	if fired.Metadata["term"] != "3" {
		t.Fatal("copy mutated original timer metadata")
	}

	fired.Source = "unknown"
	if err := fired.Validate(); !errors.Is(err, ErrInvalidValue) {
		t.Fatalf("unknown timer source error = %v, want ErrInvalidValue", err)
	}
}
