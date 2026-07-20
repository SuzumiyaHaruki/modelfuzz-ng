package core

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestMessageIDJSONRoundTrip(t *testing.T) {
	data, err := json.Marshal(MessageID(42))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(data), `"m42"`; got != want {
		t.Fatalf("Marshal(MessageID(42)) = %s, want %s", got, want)
	}

	for _, input := range []string{`"m42"`, `42`} {
		var id MessageID
		if err := json.Unmarshal([]byte(input), &id); err != nil {
			t.Fatalf("Unmarshal(%s): %v", input, err)
		}
		if id != 42 {
			t.Fatalf("Unmarshal(%s) = %v, want m42", input, id)
		}
	}
}

func TestMessageIDRejectsZero(t *testing.T) {
	var id MessageID
	err := json.Unmarshal([]byte(`"m0"`), &id)
	if !errors.Is(err, ErrInvalidValue) {
		t.Fatalf("Unmarshal(m0) error = %v, want ErrInvalidValue", err)
	}
}
