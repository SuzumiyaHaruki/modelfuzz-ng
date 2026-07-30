package raft

import (
	"encoding/json"
	"math"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/assurance/executionrecord"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/assurance/facet"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/core"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/engine"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/model"
)

func TestCatalogV1FrozenDefinitionsAndDefensiveCopies(t *testing.T) {
	catalog := CatalogV1()
	if len(catalog) != 3 {
		t.Fatalf("catalog size = %d", len(catalog))
	}
	want := []struct {
		id          string
		scope       facet.Scope
		cardinality int
	}{
		{"raft.election_role_term_shape", facet.ScopeState, 13},
		{"raft.replication_alignment_shape", facet.ScopeState, 8},
		{"raft.snapshot_lifecycle_event", facet.ScopeTransition, 10},
	}
	for index, evaluator := range catalog {
		definition := evaluator.Definition()
		if err := definition.Validate(); err != nil {
			t.Fatal(err)
		}
		if definition.ID != want[index].id || definition.Scope != want[index].scope ||
			definition.Version != 1 || definition.CardinalityBound != want[index].cardinality ||
			len(definition.Classes) != want[index].cardinality {
			t.Fatalf("definition %d = %+v", index, definition)
		}
		if definition.Invariances != allInvariances() {
			t.Fatalf("%s invariances = %+v", definition.ID, definition.Invariances)
		}
		definition.Classes[0].ID = "mutated"
		if evaluator.Definition().Classes[0].ID == "mutated" {
			t.Fatalf("%s exposed mutable definition", want[index].id)
		}
	}
	again := CatalogV1()
	if reflect.ValueOf(catalog[0]).Pointer() == reflect.ValueOf(again[0]).Pointer() {
		t.Fatal("CatalogV1 returned shared evaluator instances")
	}
}

func TestStateEvaluatorEvidenceStatusesAndTraceFirstOccurrence(t *testing.T) {
	for _, evaluator := range []facet.Evaluator{
		NewElectionRoleTermShapeV1(), NewReplicationAlignmentShapeV1(),
	} {
		result := evaluate(t, evaluator, facet.EvaluationInputV1{Record: stateRecord()})
		assertStatus(t, result, facet.StatusInsufficientEvidence)

		invalid := stateRecord()
		invalid.RecordDigest = strings.Repeat("A", 64)
		result = evaluate(t, evaluator, facet.EvaluationInputV1{
			Record: invalid, InitialObservation: observationPointer(validState()),
		})
		assertStatus(t, result, facet.StatusInvalidEvidence)

		empty := core.Trace{
			Version: core.CurrentTraceVersion, ExecutionID: "empty", Seed: 4, Steps: []core.StepRecord{},
		}
		result = evaluate(t, evaluator, facet.EvaluationInputV1{
			Record: recordForTrace(empty), Trace: &empty,
		})
		assertStatus(t, result, facet.StatusInsufficientEvidence)
		result = evaluate(t, evaluator, facet.EvaluationInputV1{
			Record: recordForTrace(empty), Trace: &empty,
			InitialObservation: observationPointer(validState()),
		})
		if result.Status != facet.StatusEvaluated || len(result.Observations) != 1 ||
			result.Observations[0].Occurrence.Kind != facet.OccurrenceExplicitInitial {
			t.Fatalf("%s empty trace + initial result = %+v", evaluator.Definition().ID, result)
		}

		recordWithMissingTrace := stateRecord()
		recordWithMissingTrace.Trace.StepCount = 1
		recordWithMissingTrace.Engine.TraceStepCount = 1
		result = evaluate(t, evaluator, facet.EvaluationInputV1{Record: recordWithMissingTrace})
		assertStatus(t, result, facet.StatusInsufficientEvidence)

		trace := traceWithStates(validState(), validState(), validState())
		result = evaluate(t, evaluator, facet.EvaluationInputV1{
			Record: recordForTrace(trace), Trace: &trace,
		})
		if result.Status != facet.StatusEvaluated || len(result.Observations) != 1 ||
			result.Observations[0].Occurrence.Kind != facet.OccurrenceTraceInitialBefore {
			t.Fatalf("%s trace result = %+v", evaluator.Definition().ID, result)
		}
	}
}

func TestStateEvaluatorRejectsProjectionDiscontinuityAndInvalidFields(t *testing.T) {
	election := NewElectionRoleTermShapeV1()
	replication := NewReplicationAlignmentShapeV1()

	before := validState()
	after := validState()
	nextBefore := validState()
	nextBefore[0].Semantic["term"] = uint64(2)
	trace := twoStepTrace(before, after, nextBefore, nextBefore)
	result := evaluate(t, election, facet.EvaluationInputV1{
		Record: recordForTrace(trace), Trace: &trace,
	})
	assertStatus(t, result, facet.StatusInvalidEvidence)

	nextBefore = validState()
	nextBefore[0].Semantic["last_index"] = uint64(2)
	trace = twoStepTrace(before, after, nextBefore, nextBefore)
	result = evaluate(t, replication, facet.EvaluationInputV1{
		Record: recordForTrace(trace), Trace: &trace,
	})
	assertStatus(t, result, facet.StatusInvalidEvidence)

	trace = traceWithStates(validState(), validState(), validState())
	explicitMismatch := validState()
	explicitMismatch[0].Semantic["role"] = "leader"
	result = evaluate(t, election, facet.EvaluationInputV1{
		Record: recordForTrace(trace), Trace: &trace,
		InitialObservation: observationPointer(explicitMismatch),
	})
	assertStatus(t, result, facet.StatusInvalidEvidence)
	explicitMismatch = validState()
	explicitMismatch[0].Semantic["last_index"] = uint64(2)
	result = evaluate(t, replication, facet.EvaluationInputV1{
		Record: recordForTrace(trace), Trace: &trace,
		InitialObservation: observationPointer(explicitMismatch),
	})
	assertStatus(t, result, facet.StatusInvalidEvidence)

	missingRole := validState()
	delete(missingRole[0].Semantic, "role")
	result = evaluate(t, election, facet.EvaluationInputV1{
		Record: stateRecord(), InitialObservation: observationPointer(missingRole),
	})
	assertStatus(t, result, facet.StatusInsufficientEvidence)

	badRole := validState()
	badRole[0].Semantic["role"] = "observer"
	result = evaluate(t, election, facet.EvaluationInputV1{
		Record: stateRecord(), InitialObservation: observationPointer(badRole),
	})
	assertStatus(t, result, facet.StatusInvalidEvidence)

	missingCommit := validState()
	delete(missingCommit[0].Semantic, "commit")
	result = evaluate(t, replication, facet.EvaluationInputV1{
		Record: stateRecord(), InitialObservation: observationPointer(missingCommit),
	})
	assertStatus(t, result, facet.StatusInsufficientEvidence)

	badBoundary := validState()
	badBoundary[0].Semantic["commit"] = uint64(2)
	badBoundary[0].Semantic["last_index"] = uint64(1)
	result = evaluate(t, replication, facet.EvaluationInputV1{
		Record: stateRecord(), InitialObservation: observationPointer(badBoundary),
	})
	assertStatus(t, result, facet.StatusInvalidEvidence)
}

func TestElectionCatalogBoundaryCases(t *testing.T) {
	evaluator := NewElectionRoleTermShapeV1()
	allCrashed := validState()
	for index := range allCrashed {
		allCrashed[index].Status = core.NodeCrashed
		allCrashed[index].Semantic["role"] = "crashed"
	}
	assertOnlyClass(t, evaluator, allCrashed, "no_running_nodes")
	assertOnlyClass(t, evaluator, validState()[:1], "leaders_none_candidates_none_terms_uniform")

	mismatch := validState()
	mismatch[0].Status = core.NodeCrashed
	result := evaluate(t, evaluator, facet.EvaluationInputV1{
		Record: stateRecord(), InitialObservation: observationPointer(mismatch),
	})
	assertStatus(t, result, facet.StatusInvalidEvidence)

	for name, value := range map[string]any{
		"negative": int64(-1), "fraction": float64(1.5),
		"overflow": float64(18446744073709551616.0), "wrong_type": "1",
	} {
		t.Run(name, func(t *testing.T) {
			nodes := validState()
			nodes[0].Semantic["term"] = value
			result := evaluate(t, evaluator, facet.EvaluationInputV1{
				Record: stateRecord(), InitialObservation: observationPointer(nodes),
			})
			assertStatus(t, result, facet.StatusInvalidEvidence)
		})
	}
}

func TestReplicationCatalogBoundaryCases(t *testing.T) {
	evaluator := NewReplicationAlignmentShapeV1()
	assertOnlyClass(t, evaluator, validState()[:1], "log_aligned_commit_aligned_applied_aligned")

	withCrashed := validState()
	withCrashed[2].Status = core.NodeCrashed
	withCrashed[2].Semantic["last_index"] = uint64(2)
	assertOnlyClass(t, evaluator, withCrashed, "log_diverged_commit_aligned_applied_aligned")

	for _, field := range []string{"last_index", "commit", "applied"} {
		t.Run("missing_"+field, func(t *testing.T) {
			nodes := validState()
			delete(nodes[0].Semantic, field)
			result := evaluate(t, evaluator, facet.EvaluationInputV1{
				Record: stateRecord(), InitialObservation: observationPointer(nodes),
			})
			assertStatus(t, result, facet.StatusInsufficientEvidence)
		})
	}
	for name, value := range map[string]any{
		"negative": int64(-1), "fraction": float64(1.5),
		"overflow": float64(18446744073709551616.0), "wrong_type": "1",
	} {
		t.Run(name, func(t *testing.T) {
			nodes := validState()
			nodes[0].Semantic["last_index"] = value
			result := evaluate(t, evaluator, facet.EvaluationInputV1{
				Record: stateRecord(), InitialObservation: observationPointer(nodes),
			})
			assertStatus(t, result, facet.StatusInvalidEvidence)
		})
	}
	for name, mutate := range map[string]func(map[string]any){
		"applied_above_commit": func(values map[string]any) {
			values["applied"], values["commit"], values["last_index"] = uint64(2), uint64(1), uint64(2)
		},
		"commit_above_last": func(values map[string]any) {
			values["applied"], values["commit"], values["last_index"] = uint64(1), uint64(2), uint64(1)
		},
	} {
		t.Run(name, func(t *testing.T) {
			nodes := validState()
			mutate(nodes[0].Semantic)
			result := evaluate(t, evaluator, facet.EvaluationInputV1{
				Record: stateRecord(), InitialObservation: observationPointer(nodes),
			})
			assertStatus(t, result, facet.StatusInvalidEvidence)
		})
	}
}

func TestFailureStatusesStillEvaluateLegalTracePrefix(t *testing.T) {
	trace := traceWithStates(validState(), validState(), validState())
	for _, status := range []engine.Status{engine.StatusModelFailed, engine.StatusOracleFailed} {
		record := recordForTrace(trace)
		record.Engine.Status = status
		record.Experiment.Status = status
		for _, evaluator := range CatalogV1()[:2] {
			result := evaluate(t, evaluator, facet.EvaluationInputV1{Record: record, Trace: &trace})
			if result.Status != facet.StatusEvaluated || len(result.Observations) == 0 {
				t.Fatalf("%s/%s result = %+v", status, evaluator.Definition().ID, result)
			}
		}
	}
}

func TestSnapshotEvaluatorNotApplicableAndAtomicInvalidEvidence(t *testing.T) {
	evaluator := NewSnapshotLifecycleEventV1()
	result := evaluate(t, evaluator, facet.EvaluationInputV1{Record: stateRecord()})
	assertStatus(t, result, facet.StatusInsufficientEvidence)
	failureRecord := stateRecord()
	failureRecord.Engine.Status = engine.StatusRuntimeFailed
	failureRecord.Experiment.Status = engine.StatusRuntimeFailed
	result = evaluate(t, evaluator, facet.EvaluationInputV1{Record: failureRecord})
	assertStatus(t, result, facet.StatusInsufficientEvidence)

	trace := traceWithEffects(core.Effect{
		Kind:       core.EffectModelEvent,
		ModelEvent: modelEventPointer(core.ModelEvent{Name: "raft.role_changed", Node: 1}),
	})
	result = evaluate(t, evaluator, facet.EvaluationInputV1{
		Record: recordForTrace(trace), Trace: &trace,
	})
	assertStatus(t, result, facet.StatusNotApplicable)

	valid := core.ModelEvent{
		Name: "raft.snapshot_created", Node: 1,
		Params: map[string]any{"index": uint64(2), "term": uint64(1), "snapshot_bytes": uint64(16)},
	}
	invalid := valid.Copy()
	delete(invalid.Params, "snapshot_bytes")
	trace = traceWithEffects(
		core.Effect{Kind: core.EffectModelEvent, ModelEvent: modelEventPointer(valid)},
		core.Effect{Kind: core.EffectModelEvent, ModelEvent: modelEventPointer(invalid)},
	)
	result = evaluate(t, evaluator, facet.EvaluationInputV1{
		Record: recordForTrace(trace), Trace: &trace,
	})
	if result.Status != facet.StatusInvalidEvidence || len(result.Observations) != 0 {
		t.Fatalf("atomic invalid result = %+v", result)
	}
}

func TestSnapshotMarkerBoundaryValidation(t *testing.T) {
	validBase := map[string]any{
		"index": uint64(2), "term": uint64(1), "snapshot_bytes": uint64(16),
	}
	tests := []core.ModelEvent{
		{Name: "raft.snapshot_created", Node: 0, Params: validBase},
		{Name: "raft.snapshot_created", Node: 1, Params: map[string]any{
			"index": uint64(0), "term": uint64(1), "snapshot_bytes": uint64(16),
		}},
		{Name: "raft.log_compacted", Node: 1, Params: validBase},
		{Name: "raft.snapshot_sent", Node: 1, Params: map[string]any{
			"index": uint64(math.MaxUint64), "term": uint64(1), "snapshot_bytes": uint64(16),
			"to": uint64(2), "match_index": uint64(1), "next_index": uint64(0),
			"pending_snapshot": uint64(math.MaxUint64), "progress_state": "StateSnapshot",
		}},
		{Name: "raft.snapshot_status_reported", Node: 2, Params: map[string]any{
			"from": uint64(1), "to": uint64(2), "handled": "yes", "reject": false,
		}},
	}
	for _, marker := range tests {
		trace := traceWithEffects(core.Effect{
			Kind: core.EffectModelEvent, ModelEvent: modelEventPointer(marker),
		})
		result := evaluate(t, NewSnapshotLifecycleEventV1(), facet.EvaluationInputV1{
			Record: recordForTrace(trace), Trace: &trace,
		})
		assertStatus(t, result, facet.StatusInvalidEvidence)
	}
}

func TestSnapshotMultipleMarkersDeduplicateAtFirstOccurrence(t *testing.T) {
	created := core.ModelEvent{
		Name: "raft.snapshot_created", Node: 1,
		Params: map[string]any{"index": uint64(2), "term": uint64(1), "snapshot_bytes": uint64(16)},
	}
	applied := core.ModelEvent{
		Name: "raft.snapshot_applied", Node: 1,
		Params: map[string]any{"index": uint64(2), "term": uint64(1), "snapshot_bytes": uint64(16)},
	}
	trace := traceWithEffects(
		core.Effect{Kind: core.EffectModelEvent, ModelEvent: modelEventPointer(created)},
		core.Effect{Kind: core.EffectModelEvent, ModelEvent: modelEventPointer(applied)},
		core.Effect{Kind: core.EffectModelEvent, ModelEvent: modelEventPointer(created)},
	)
	result := evaluate(t, NewSnapshotLifecycleEventV1(), facet.EvaluationInputV1{
		Record: recordForTrace(trace), Trace: &trace,
	})
	if result.Status != facet.StatusEvaluated || len(result.Observations) != 2 {
		t.Fatalf("result = %+v", result)
	}
	for _, observation := range result.Observations {
		if observation.Key.ClassID == "snapshot_created" &&
			(observation.Occurrence.EffectIndex == nil || *observation.Occurrence.EffectIndex != 0) {
			t.Fatalf("snapshot_created first occurrence = %+v", observation.Occurrence)
		}
	}
}

func TestSnapshotStatusIgnoredPrecedesRejectAndReasonDoesNotEnterKey(t *testing.T) {
	ignored := core.ModelEvent{
		Name: "raft.snapshot_status_reported", Node: 2,
		Params: map[string]any{
			"from": uint64(1), "to": uint64(2), "handled": false, "reject": true,
		},
	}
	trace := traceWithEffects(core.Effect{
		Kind: core.EffectModelEvent, ModelEvent: modelEventPointer(ignored),
	})
	result := evaluate(t, NewSnapshotLifecycleEventV1(), facet.EvaluationInputV1{
		Record: recordForTrace(trace), Trace: &trace,
	})
	if len(result.Observations) != 1 ||
		result.Observations[0].Key.ClassID != "snapshot_status_ignored" {
		t.Fatalf("ignored status result = %+v", result)
	}

	base := core.ModelEvent{
		Name: "raft.snapshot_rejected_or_stale", Node: 1,
		Params: map[string]any{
			"index": uint64(2), "term": uint64(1), "snapshot_bytes": uint64(16),
			"reason": "stale",
		},
	}
	firstTrace := traceWithEffects(core.Effect{
		Kind: core.EffectModelEvent, ModelEvent: modelEventPointer(base),
	})
	first := snapshotKeys(t, recordForTrace(firstTrace), firstTrace)
	base.Params["reason"] = "different debug explanation"
	secondTrace := traceWithEffects(core.Effect{
		Kind: core.EffectModelEvent, ModelEvent: modelEventPointer(base),
	})
	second := snapshotKeys(t, recordForTrace(secondTrace), secondTrace)
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("optional reason affected key: %v / %v", first, second)
	}
}

func TestSnapshotSpecificMalformedPredicates(t *testing.T) {
	base := map[string]any{
		"index": uint64(5), "term": uint64(3), "snapshot_bytes": uint64(64),
		"to": uint64(2), "match_index": uint64(2), "next_index": uint64(6),
		"pending_snapshot": uint64(5), "progress_state": "StateSnapshot",
	}
	tests := map[string]core.ModelEvent{
		"endpoint": {
			Name: "raft.snapshot_sent", Node: 1,
			Params: replaceValues(base, "to", uint64(1)),
		},
		"pending": {
			Name: "raft.snapshot_sent", Node: 1,
			Params: replaceValues(base, "pending_snapshot", uint64(4)),
		},
		"next": {
			Name: "raft.snapshot_sent", Node: 1,
			Params: replaceValues(base, "next_index", uint64(7)),
		},
		"progress": {
			Name: "raft.snapshot_sent", Node: 1,
			Params: replaceValues(base, "progress_state", "StateProbe"),
		},
		"fast_forward_one_boundary": {
			Name: "raft.snapshot_fast_forwarded", Node: 1,
			Params: map[string]any{
				"index": uint64(5), "term": uint64(3), "snapshot_bytes": uint64(64),
				"commit_before": uint64(3),
			},
		},
		"fast_forward_regression": {
			Name: "raft.snapshot_fast_forwarded", Node: 1,
			Params: map[string]any{
				"index": uint64(5), "term": uint64(3), "snapshot_bytes": uint64(64),
				"commit_before": uint64(4), "commit_after": uint64(3),
			},
		},
	}
	for name, marker := range tests {
		t.Run(name, func(t *testing.T) {
			trace := traceWithEffects(core.Effect{
				Kind: core.EffectModelEvent, ModelEvent: modelEventPointer(marker),
			})
			result := evaluate(t, NewSnapshotLifecycleEventV1(), facet.EvaluationInputV1{
				Record: recordForTrace(trace), Trace: &trace,
			})
			assertStatus(t, result, facet.StatusInvalidEvidence)
		})
	}
}

func TestSnapshotIntegerNormalizationIsExactAndBounded(t *testing.T) {
	accepted := []any{
		uint(0), uint8(1), uint16(2), uint32(3), uint64(math.MaxUint64),
		int(0), int8(1), int16(2), int32(3), int64(4),
		float32(5), float64(6), json.Number("7"),
	}
	for _, value := range accepted {
		if _, ok := nonNegativeInteger(value); !ok {
			t.Fatalf("accepted integer %T(%v) rejected", value, value)
		}
	}
	rejected := []any{
		int(-1), int64(-1), float64(-1), float64(1.5), math.Inf(1), math.NaN(),
		float64(18446744073709551616.0), json.Number("-1"), json.Number("1.0"), "1",
	}
	for _, value := range rejected {
		if _, ok := nonNegativeInteger(value); ok {
			t.Fatalf("invalid integer %T(%v) accepted", value, value)
		}
	}
}

func TestCandidateUnionDeduplicatesAndOrdersFirstOccurrence(t *testing.T) {
	initial := validState()
	leader := copyNodes(initial)
	leader[0].Semantic["role"] = "leader"
	trace := twoStepTrace(initial, leader, leader, initial)
	result := evaluate(t, NewElectionRoleTermShapeV1(), facet.EvaluationInputV1{
		Record: recordForTrace(trace), InitialObservation: observationPointer(initial), Trace: &trace,
	})
	if result.Status != facet.StatusEvaluated || len(result.Observations) != 2 {
		t.Fatalf("result = %+v", result)
	}
	if !sort.SliceIsSorted(result.Observations, func(i, j int) bool {
		return result.Observations[i].Key.ClassID < result.Observations[j].Key.ClassID
	}) {
		t.Fatalf("observations are not canonical: %+v", result.Observations)
	}
	for _, observation := range result.Observations {
		if observation.Key.ClassID == "leaders_none_candidates_none_terms_uniform" &&
			observation.Occurrence.Kind != facet.OccurrenceExplicitInitial {
			t.Fatalf("first occurrence changed: %+v", observation.Occurrence)
		}
	}
}

func TestOptionalModelEvidenceIsCountCheckedButTextIsNotParsed(t *testing.T) {
	nodes := validState()
	trace := traceWithEffects(core.Effect{
		Kind: core.EffectModelEvent,
		ModelEvent: modelEventPointer(core.ModelEvent{
			Name: "raft.snapshot_created", Node: 1,
			Params: map[string]any{
				"index": uint64(2), "term": uint64(1), "snapshot_bytes": uint64(16),
			},
		}),
	})
	record := recordForTrace(trace)
	record.Engine.ModelStateCount = 1
	record.Model.StateCount = 1
	record.Engine.ModelEventCount = 1
	record.Model.EventCount = 1
	input := facet.EvaluationInputV1{
		Record: record, InitialObservation: observationPointer(nodes), Trace: &trace,
		ModelEvents: []model.Event{{
			Name: "OpaqueAction", Params: map[string]any{"opaque": true},
		}},
		ModelStates: []model.State{{Text: "arbitrary backend display text", Key: 19}},
	}
	before, err := facet.EvaluateAll(input, CatalogV1())
	if err != nil {
		t.Fatal(err)
	}
	input.ModelStates[0].Text = "completely different display text"
	after, err := facet.EvaluateAll(input, CatalogV1())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("model.State.Text affected Facet result: %+v / %+v", before, after)
	}
}

func evaluate(t *testing.T, evaluator facet.Evaluator, input facet.EvaluationInputV1) facet.EvaluationV1 {
	t.Helper()
	result, err := evaluator.Evaluate(input)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func assertStatus(t *testing.T, result facet.EvaluationV1, status facet.EvaluationStatus) {
	t.Helper()
	if result.Status != status || len(result.Observations) != 0 {
		t.Fatalf("result = %+v, want status %s and no observations", result, status)
	}
}

func validState() []core.NodeObservation {
	return []core.NodeObservation{semanticNode(1), semanticNode(2), semanticNode(3)}
}

func copyNodes(nodes []core.NodeObservation) []core.NodeObservation {
	result := make([]core.NodeObservation, len(nodes))
	for index, node := range nodes {
		result[index] = node.Copy()
	}
	return result
}

func observationPointer(nodes []core.NodeObservation) *core.Observation {
	observation := core.Observation{Nodes: copyNodes(nodes)}
	return &observation
}

func traceWithStates(before, after, final []core.NodeObservation) core.Trace {
	return twoStepTrace(before, after, after, final)
}

func twoStepTrace(
	firstBefore, firstAfter, secondBefore, secondAfter []core.NodeObservation,
) core.Trace {
	return core.Trace{
		Version: core.CurrentTraceVersion, ExecutionID: "state-trace", Seed: 33,
		Steps: []core.StepRecord{
			{
				Index: 0, Action: core.Action{Kind: core.ActionHeal},
				NodesBefore: copyNodes(firstBefore), NodesAfter: copyNodes(firstAfter),
				ObservationDigest: "observation-0",
			},
			{
				Index: 1, Action: core.Action{Kind: core.ActionHeal},
				NodesBefore: copyNodes(secondBefore), NodesAfter: copyNodes(secondAfter),
				ObservationDigest: "observation-1",
			},
		},
	}
}

func TestPrepareRejectsRecordAndTraceCountMismatch(t *testing.T) {
	trace := traceWithStates(validState(), validState(), validState())
	record := recordForTrace(trace)
	record.Engine.TraceStepCount++
	result := evaluate(t, NewElectionRoleTermShapeV1(), facet.EvaluationInputV1{
		Record: record, Trace: &trace,
	})
	assertStatus(t, result, facet.StatusInvalidEvidence)

	record = recordForTrace(trace)
	record.Trace.ExecutionID = "different"
	result = evaluate(t, NewElectionRoleTermShapeV1(), facet.EvaluationInputV1{
		Record: record, Trace: &trace,
	})
	assertStatus(t, result, facet.StatusInvalidEvidence)
}

func TestInputAndOutputAreDefensivelyIsolated(t *testing.T) {
	nodes := validState()
	input := facet.EvaluationInputV1{
		Record: stateRecord(), InitialObservation: observationPointer(nodes),
	}
	result := evaluate(t, NewElectionRoleTermShapeV1(), input)
	input.InitialObservation.Nodes[0].Semantic["role"] = "leader"
	if result.Observations[0].Key.ClassID != "leaders_none_candidates_none_terms_uniform" {
		t.Fatalf("input mutation changed result: %+v", result)
	}
	copy := result.Copy()
	copy.Observations[0].Key.ClassID = "mutated"
	if result.Observations[0].Key.ClassID == "mutated" {
		t.Fatal("result copy shares observations")
	}

	trace := traceWithStates(validState(), validState(), validState())
	before, err := json.Marshal(trace)
	if err != nil {
		t.Fatal(err)
	}
	_ = evaluate(t, NewElectionRoleTermShapeV1(), facet.EvaluationInputV1{
		Record: recordForTrace(trace), Trace: &trace,
	})
	after, err := json.Marshal(trace)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(after, before) {
		t.Fatalf("evaluator modified caller Trace:\nbefore=%s\nafter=%s", before, after)
	}
}

func assertOnlyClass(
	t *testing.T,
	evaluator facet.Evaluator,
	nodes []core.NodeObservation,
	classID string,
) {
	t.Helper()
	result := evaluate(t, evaluator, facet.EvaluationInputV1{
		Record: stateRecord(), InitialObservation: observationPointer(nodes),
	})
	if result.Status != facet.StatusEvaluated || len(result.Observations) != 1 ||
		result.Observations[0].Key.ClassID != classID {
		t.Fatalf("result = %+v, want %s", result, classID)
	}
}

func replaceValues(source map[string]any, key string, value any) map[string]any {
	result := make(map[string]any, len(source))
	for name, item := range source {
		result[name] = item
	}
	result[key] = value
	return result
}

func TestArtifactReferenceDoesNotControlFacetEvidence(t *testing.T) {
	record := stateRecord()
	record.Artifacts = []executionrecord.ArtifactReference{{
		Kind: executionrecord.ArtifactTrace, Path: "one/trace.json",
		SHA256: strings.Repeat("b", 64),
	}}
	input := facet.EvaluationInputV1{
		Record: record, InitialObservation: observationPointer(validState()),
	}
	first := evaluate(t, NewElectionRoleTermShapeV1(), input)
	input.Record.Artifacts[0].Path = "another/layout/trace.json"
	input.Record.Artifacts[0].SHA256 = strings.Repeat("c", 64)
	second := evaluate(t, NewElectionRoleTermShapeV1(), input)
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("artifact layout affected Facet result: %+v / %+v", first, second)
	}
}
