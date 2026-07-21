package corpus

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/core"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/model"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/plan"
)

func TestCorpusRetainsOnlyGlobalCoverageIncrease(t *testing.T) {
	collection := New()
	base := Input{
		Source: "random_init", RunIndex: 0,
		Plan:    plan.PlanSequence{Actions: []plan.PlanAction{{Kind: plan.ActionTimeout, Node: 1}}},
		Actions: core.ActionSequence{Actions: []core.Action{{Kind: core.ActionTimeout, Node: 1}}},
		States:  []model.State{{Key: 9}, {Key: 3}, {Key: 9}},
	}
	entry, retained, err := collection.Consider(base)
	if err != nil {
		t.Fatal(err)
	}
	if !retained || entry.ID != "corpus-000000" || len(entry.NewStateKeys) != 2 || entry.NewStateKeys[0] != 3 {
		t.Fatalf("first entry = %+v, retained=%v", entry, retained)
	}
	base.RunIndex = 1
	base.States = []model.State{{Key: 3}, {Key: 9}}
	if _, retained, err := collection.Consider(base); err != nil || retained {
		t.Fatalf("duplicate coverage retained=%v err=%v", retained, err)
	}
	base.RunIndex = 2
	base.States = []model.State{{Key: 9}, {Key: 12}}
	entry, retained, err = collection.Consider(base)
	if err != nil || !retained || len(entry.NewStateKeys) != 1 || entry.NewStateKeys[0] != 12 {
		t.Fatalf("incremental entry = %+v, retained=%v err=%v", entry, retained, err)
	}
	snapshot := collection.Snapshot()
	if len(snapshot.Entries) != 2 || len(snapshot.CoverageKeys) != 3 {
		t.Fatalf("snapshot = %+v", snapshot)
	}
}

func TestCorpusCopiesRetainedInput(t *testing.T) {
	collection := New()
	input := Input{
		RunIndex: 0,
		Plan:     plan.PlanSequence{Actions: []plan.PlanAction{{Kind: plan.ActionTimeout, Node: 1}}},
		Actions:  core.ActionSequence{Actions: []core.Action{{Kind: core.ActionTimeout, Node: 1}}}, States: []model.State{{Key: 1}},
	}
	if _, retained, err := collection.Consider(input); err != nil || !retained {
		t.Fatalf("retained=%v err=%v", retained, err)
	}
	input.Plan.Actions[0].Node = 2
	input.Actions.Actions[0].Node = 2
	snapshot := collection.Snapshot()
	if snapshot.Entries[0].Plan.Actions[0].Node != 1 {
		t.Fatalf("corpus aliased caller plan: %+v", snapshot.Entries[0].Plan)
	}
	if snapshot.Entries[0].Actions.Actions[0].Node != 1 {
		t.Fatalf("corpus aliased caller actions: %+v", snapshot.Entries[0].Actions)
	}
}

func TestRestoreRoundTrip(t *testing.T) {
	collection := New()
	input := Input{
		Source: "random_init", RunIndex: 0,
		Plan:    plan.PlanSequence{Actions: []plan.PlanAction{{Kind: plan.ActionTimeout, Node: 1}}},
		Actions: core.ActionSequence{Actions: []core.Action{{Kind: core.ActionTimeout, Node: 1}}},
		States:  []model.State{{Key: 3}, {Key: 1}, {Key: 3}},
	}
	if _, retained, err := collection.Consider(input); err != nil || !retained {
		t.Fatalf("retained=%v err=%v", retained, err)
	}
	restored, err := Restore(collection.Snapshot())
	if err != nil {
		t.Fatal(err)
	}
	got := restored.Snapshot()
	if len(got.Entries) != 1 || len(got.CoverageKeys) != 2 || got.CoverageKeys[0] != 1 || got.CoverageKeys[1] != 3 {
		t.Fatalf("restored = %+v", got)
	}
}

func TestSnapshotUsesCompactActionsInsteadOfTrace(t *testing.T) {
	collection := New()
	input := Input{
		RunIndex: 0,
		Plan:     plan.PlanSequence{Actions: []plan.PlanAction{{Kind: plan.ActionTimeout, Node: 1}}},
		Actions:  core.ActionSequence{Actions: []core.Action{{Kind: core.ActionTimeout, Node: 1}}},
		States:   []model.State{{Key: 1}},
	}
	if _, retained, err := collection.Consider(input); err != nil || !retained {
		t.Fatalf("retained=%v err=%v", retained, err)
	}
	data, err := json.Marshal(collection.Snapshot())
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte(`"trace"`)) || !bytes.Contains(data, []byte(`"actions"`)) {
		t.Fatalf("snapshot is not compact: %s", data)
	}
}
