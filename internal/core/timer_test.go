package core

import (
	"errors"
	"math"
	"testing"
)

func TestLogicalTimeAdd(t *testing.T) {
	got, err := LogicalTime(10).Add(5)
	if err != nil || got != 15 {
		t.Fatalf("LogicalTime.Add = %d, %v; want 15, nil", got, err)
	}
	if _, err := LogicalTime(math.MaxUint64).Add(1); !errors.Is(err, ErrInvalidValue) {
		t.Fatalf("overflow error = %v, want ErrInvalidValue", err)
	}
}

func TestTimerFireSourceValidation(t *testing.T) {
	for _, source := range []TimerFireSource{TimerFireNatural, TimerFireForced} {
		if !source.Valid() {
			t.Fatalf("valid timer fire source rejected: %q", source)
		}
	}
	if TimerFireSource("unknown").Valid() {
		t.Fatal("unknown timer fire source accepted")
	}
}
