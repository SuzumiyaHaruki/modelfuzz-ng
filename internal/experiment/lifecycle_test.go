package experiment

import (
	"strings"
	"testing"
)

func TestCheckpointRejectsOlderSchemaVersion(t *testing.T) {
	checkpoint := Checkpoint{Version: CheckpointVersion - 1}
	err := checkpoint.Validate(Config{}, "")
	if err == nil || !strings.Contains(err.Error(), "unsupported checkpoint version") {
		t.Fatalf("old checkpoint validation error = %v", err)
	}
}
