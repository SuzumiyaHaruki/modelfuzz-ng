package policy

import (
	"testing"

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/core"
)

func TestSnapshotFastForwardRejectsInvalidConfiguration(t *testing.T) {
	valid := SnapshotFastForwardConfig{
		NodeIDs: []core.NodeID{1, 2, 3}, MaxValue: 5, MaxLogIndex: 10,
		SnapshotThreshold: 4, RetainEntries: 1,
	}
	for _, mutate := range []func(*SnapshotFastForwardConfig){
		func(config *SnapshotFastForwardConfig) { config.NodeIDs = []core.NodeID{1, 2} },
		func(config *SnapshotFastForwardConfig) { config.SnapshotThreshold = 2 },
		func(config *SnapshotFastForwardConfig) { config.SnapshotThreshold = 11 },
		func(config *SnapshotFastForwardConfig) { config.RetainEntries = 3 },
		func(config *SnapshotFastForwardConfig) { config.NodeIDs = []core.NodeID{1, 2, 2} },
	} {
		config := valid
		config.NodeIDs = append([]core.NodeID(nil), valid.NodeIDs...)
		mutate(&config)
		if _, err := NewSnapshotFastForward(1, config); err == nil {
			t.Fatalf("invalid fast-forward config accepted: %+v", config)
		}
	}
}
