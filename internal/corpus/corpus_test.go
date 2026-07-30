package corpus

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/model"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/plan"
)

func TestCorpusRetainsOnlyGlobalCoverageIncrease(t *testing.T) {
	collection := New()
	base := Input{
		Source: "random_init", RunIndex: 0,
		Plan:   plan.PlanSequence{Actions: []plan.PlanAction{{Kind: plan.ActionTimeout, Node: 1}}},
		States: []model.State{{Key: 9}, {Key: 3}, {Key: 9}},
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
		States:   []model.State{{Key: 1}},
	}
	if _, retained, err := collection.Consider(input); err != nil || !retained {
		t.Fatalf("retained=%v err=%v", retained, err)
	}
	input.Plan.Actions[0].Node = 2
	snapshot := collection.Snapshot()
	if snapshot.Entries[0].Plan.Actions[0].Node != 1 {
		t.Fatalf("corpus aliased caller plan: %+v", snapshot.Entries[0].Plan)
	}
}

func TestExternalAdmissionDoesNotChangeLegacyCoverageBookkeeping(t *testing.T) {
	collection := NewWithConfig(Config{MinNewModelStates: 25, RequireSemanticNovelty: true})
	input := Input{
		RunIndex:          0,
		Plan:              plan.PlanSequence{Actions: []plan.PlanAction{{Kind: plan.ActionTimeout, Node: 1}}},
		States:            []model.State{{Key: 7}},
		SemanticStateKeys: []int64{70},
	}
	rejected, retained, err := collection.ConsiderExternal(
		input, false, AdmissionReason("rejected_no_guidance_novelty"))
	if err != nil || retained || rejected.AdmissionReason != "rejected_no_guidance_novelty" ||
		collection.CoverageLen() != 1 || collection.Len() != 0 {
		t.Fatalf("external rejection = %+v retained=%v err=%v snapshot=%+v",
			rejected, retained, err, collection.Snapshot())
	}
	input.RunIndex = 1
	retainedEntry, retained, err := collection.ConsiderExternal(
		input, true, AdmissionReason("admitted_random_without_coverage"))
	if err != nil || !retained || retainedEntry.ID != "corpus-000000" ||
		len(retainedEntry.NewStateKeys) != 0 || collection.Len() != 1 {
		t.Fatalf("external admission = %+v retained=%v err=%v", retainedEntry, retained, err)
	}
}

func TestRestoreRoundTrip(t *testing.T) {
	collection := New()
	input := Input{
		Source: "random_init", RunIndex: 0,
		Plan:   plan.PlanSequence{Actions: []plan.PlanAction{{Kind: plan.ActionTimeout, Node: 1}}},
		States: []model.State{{Key: 3}, {Key: 1}, {Key: 3}},
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

func TestCheckpointKeepsEntriesOutOfRepeatedSnapshot(t *testing.T) {
	collection := New()
	input := Input{
		RunIndex: 0,
		Plan:     plan.PlanSequence{Actions: []plan.PlanAction{{Kind: plan.ActionTimeout, Node: 1}}},
		States:   []model.State{{Key: 1}},
	}
	if _, retained, err := collection.Consider(input); err != nil || !retained {
		t.Fatalf("retained=%v err=%v", retained, err)
	}
	data, err := json.Marshal(collection.Checkpoint())
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte(`"entries"`)) || bytes.Contains(data, []byte(`"actions"`)) ||
		!bytes.Contains(data, []byte(`"entry_count":1`)) {
		t.Fatalf("checkpoint is not compact: %s", data)
	}
	restored, err := RestoreCheckpoint(collection.Checkpoint(), collection.Snapshot().Entries)
	if err != nil || restored.Len() != 1 || restored.CoverageLen() != 1 {
		t.Fatalf("restore checkpoint = %v/%v", restored, err)
	}
}

func TestRollbackLastRemovesUncommittedCoverage(t *testing.T) {
	collection := New()
	entry, retained, err := collection.Consider(Input{
		RunIndex: 0,
		Plan:     plan.PlanSequence{Actions: []plan.PlanAction{{Kind: plan.ActionTimeout, Node: 1}}},
		States:   []model.State{{Key: 7}},
	})
	if err != nil || !retained {
		t.Fatalf("consider = %+v/%v/%v", entry, retained, err)
	}
	if err := collection.RollbackLast(entry); err != nil {
		t.Fatal(err)
	}
	if collection.Len() != 0 || collection.CoverageLen() != 0 {
		t.Fatalf("corpus after rollback = %+v", collection.Snapshot())
	}
}

func TestCorpusRequiresRawThresholdAndSemanticNovelty(t *testing.T) {
	collection := NewWithConfig(Config{MinNewModelStates: 2, RequireSemanticNovelty: true})
	base := Input{
		Source: "test", Plan: plan.PlanSequence{Actions: []plan.PlanAction{{Kind: plan.ActionTimeout, Node: 1}}},
	}
	base.States = []model.State{{Key: 1}}
	base.SemanticStateKeys = []int64{101}
	entry, retained, err := collection.Consider(base)
	if err != nil || retained || len(entry.NewStateKeys) != 1 || entry.AdmissionReason != AdmissionRejectedRawThreshold {
		t.Fatalf("below threshold: entry=%+v retained=%v err=%v", entry, retained, err)
	}
	base.RunIndex = 1
	base.States = []model.State{{Key: 2}, {Key: 3}}
	base.SemanticStateKeys = []int64{101}
	if entry, retained, err = collection.Consider(base); err != nil || retained || entry.AdmissionReason != AdmissionRejectedNoSemanticNovelty {
		t.Fatalf("no semantic novelty retained=%v err=%v", retained, err)
	}
	base.RunIndex = 2
	base.States = []model.State{{Key: 4}, {Key: 5}}
	base.SemanticStateKeys = []int64{102}
	base.SemanticTransitionKeys = []int64{201}
	entry, retained, err = collection.Consider(base)
	if err != nil || !retained || len(entry.NewSemanticStateKeys) != 1 || len(entry.NewSemanticTransitionKeys) != 1 {
		t.Fatalf("semantic novelty entry=%+v retained=%v err=%v", entry, retained, err)
	}
	if entry.AdmissionReason != AdmissionRetainedSemanticStateAndTransition {
		t.Fatalf("admission reason=%q", entry.AdmissionReason)
	}
	if collection.CoverageLen() != 5 {
		t.Fatalf("raw coverage=%d, want rejected observations included", collection.CoverageLen())
	}
}
