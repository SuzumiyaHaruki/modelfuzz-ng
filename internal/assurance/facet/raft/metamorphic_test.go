package raft

import (
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/assurance/executionrecord"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/assurance/facet"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/core"
)

func TestStateFacetsMetamorphicInvariances(t *testing.T) {
	baseNodes := validState()
	baseNodes[0].Semantic["role"] = "leader"
	baseNodes[0].Semantic["term"] = uint64(4)
	baseNodes[1].Semantic["term"] = uint64(3)
	baseNodes[2].Semantic["term"] = uint64(3)
	baseNodes[0].Semantic["last_index"] = uint64(8)
	baseNodes[0].Semantic["commit"] = uint64(7)
	baseNodes[0].Semantic["applied"] = uint64(6)
	baseNodes[1].Semantic["last_index"] = uint64(7)
	baseNodes[1].Semantic["commit"] = uint64(6)
	baseNodes[1].Semantic["applied"] = uint64(6)

	base := stateKeys(t, baseNodes)
	transforms := map[string]func([]core.NodeObservation) []core.NodeObservation{
		"node_renaming_and_order": func(nodes []core.NodeObservation) []core.NodeObservation {
			result := copyNodes(nodes)
			result[0].ID, result[1].ID, result[2].ID = 30, 10, 20
			return []core.NodeObservation{result[2], result[0], result[1]}
		},
		"uniform_term_shift": func(nodes []core.NodeObservation) []core.NodeObservation {
			result := copyNodes(nodes)
			for index := range result {
				result[index].Semantic["term"] = result[index].Semantic["term"].(uint64) + 100
			}
			return result
		},
		"uniform_index_shift": func(nodes []core.NodeObservation) []core.NodeObservation {
			result := copyNodes(nodes)
			for index := range result {
				for _, field := range []string{"last_index", "commit", "applied"} {
					result[index].Semantic[field] = result[index].Semantic[field].(uint64) + 100
				}
			}
			return result
		},
		"map_insertion_order": func(nodes []core.NodeObservation) []core.NodeObservation {
			result := copyNodes(nodes)
			for index := range result {
				source := result[index].Semantic
				result[index].Semantic = map[string]any{
					"applied": source["applied"], "last_index": source["last_index"],
					"unrelated_debug": "ignored", "term": source["term"],
					"commit": source["commit"], "role": source["role"],
				}
			}
			return result
		},
	}
	for name, transform := range transforms {
		t.Run(name, func(t *testing.T) {
			if got := stateKeys(t, transform(baseNodes)); !reflect.DeepEqual(got, base) {
				t.Fatalf("keys = %v, want %v", got, base)
			}
		})
	}
}

func TestSnapshotFacetMetamorphicInvariances(t *testing.T) {
	baseMarker := core.ModelEvent{
		Name: "raft.snapshot_sent", Node: 1,
		Params: map[string]any{
			"index": uint64(5), "term": uint64(3), "snapshot_bytes": uint64(64),
			"to": uint64(2), "match_index": uint64(2), "next_index": uint64(6),
			"pending_snapshot": uint64(5), "progress_state": "StateSnapshot",
		},
	}
	baseTrace := traceWithMessageAction(baseMarker, 1, 1, 2)
	baseRecord := recordForTrace(baseTrace)
	base := snapshotKeys(t, baseRecord, baseTrace)

	t.Run("node_and_message_renaming", func(t *testing.T) {
		marker := baseMarker.Copy()
		marker.Node = 30
		marker.Params["to"] = uint64(20)
		trace := traceWithMessageAction(marker, 999, 30, 20)
		record := recordForTrace(trace)
		if got := snapshotKeys(t, record, trace); !reflect.DeepEqual(got, base) {
			t.Fatalf("keys = %v, want %v", got, base)
		}
	})
	t.Run("uniform_term_and_index_shift", func(t *testing.T) {
		marker := baseMarker.Copy()
		marker.Params["term"] = uint64(103)
		marker.Params["index"] = uint64(105)
		marker.Params["match_index"] = uint64(102)
		marker.Params["next_index"] = uint64(106)
		marker.Params["pending_snapshot"] = uint64(105)
		trace := traceWithMessageAction(marker, 1, 1, 2)
		record := recordForTrace(trace)
		if got := snapshotKeys(t, record, trace); !reflect.DeepEqual(got, base) {
			t.Fatalf("keys = %v, want %v", got, base)
		}
	})
	t.Run("marker_map_insertion_order", func(t *testing.T) {
		marker := baseMarker.Copy()
		marker.Params = map[string]any{
			"progress_state": "StateSnapshot", "pending_snapshot": uint64(5),
			"next_index": uint64(6), "match_index": uint64(2), "to": uint64(2),
			"snapshot_bytes": uint64(64), "term": uint64(3), "index": uint64(5),
		}
		trace := traceWithMessageAction(marker, 1, 1, 2)
		if got := snapshotKeys(t, recordForTrace(trace), trace); !reflect.DeepEqual(got, base) {
			t.Fatalf("keys = %v, want %v", got, base)
		}
	})
	t.Run("record_debug_artifact_execution_identity", func(t *testing.T) {
		trace := baseTrace.Copy()
		trace.ExecutionID = "renamed-execution"
		trace.Seed = 999
		record := recordForTrace(trace)
		record.RecordDigest = strings.Repeat("d", 64)
		record.Engine.DebugError = "unrelated engine debug"
		record.Engine.TerminationDetail = "unrelated termination detail"
		record.Experiment.DebugError = "unrelated experiment debug"
		record.Artifacts = []executionrecord.ArtifactReference{{
			Kind: executionrecord.ArtifactTrace, Path: "different/layout/trace.json",
			SHA256: strings.Repeat("e", 64),
		}}
		if got := snapshotKeys(t, record, trace); !reflect.DeepEqual(got, base) {
			t.Fatalf("keys = %v, want %v", got, base)
		}
	})
}

func TestSameInputRepeatedTwentyTimesIsDeterministic(t *testing.T) {
	trace := traceWithEffects(core.Effect{
		Kind: core.EffectModelEvent,
		ModelEvent: modelEventPointer(core.ModelEvent{
			Name: "raft.snapshot_created", Node: 1,
			Params: map[string]any{
				"snapshot_bytes": uint64(32), "term": uint64(2), "index": uint64(4),
			},
		}),
	})
	input := facet.EvaluationInputV1{
		Record: recordForTrace(trace), InitialObservation: observationPointer(validState()), Trace: &trace,
	}
	first, err := facet.EvaluateAll(input, CatalogV1())
	if err != nil {
		t.Fatal(err)
	}
	for iteration := 0; iteration < 20; iteration++ {
		got, err := facet.EvaluateAll(input, CatalogV1())
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, first) {
			t.Fatalf("iteration %d differs: %+v / %+v", iteration, got, first)
		}
	}
}

func stateKeys(t *testing.T, nodes []core.NodeObservation) []string {
	t.Helper()
	input := facet.EvaluationInputV1{
		Record: stateRecord(), InitialObservation: observationPointer(nodes),
	}
	results, err := facet.EvaluateAll(input, CatalogV1()[:2])
	if err != nil {
		t.Fatal(err)
	}
	return observationIdentities(t, results)
}

func snapshotKeys(
	t *testing.T,
	record executionrecord.CompletedExecutionRecordV1,
	trace core.Trace,
) []string {
	t.Helper()
	result := evaluate(t, NewSnapshotLifecycleEventV1(), facet.EvaluationInputV1{
		Record: record, Trace: &trace,
	})
	return observationIdentities(t, []facet.EvaluationV1{result})
}

func observationIdentities(t *testing.T, evaluations []facet.EvaluationV1) []string {
	t.Helper()
	var result []string
	for _, evaluation := range evaluations {
		if evaluation.Status != facet.StatusEvaluated {
			t.Fatalf("%s status = %s", evaluation.FacetID, evaluation.Status)
		}
		for _, observation := range evaluation.Observations {
			result = append(result, observation.KeyDigest+":"+observation.Key.ClassID)
		}
	}
	sort.Strings(result)
	return result
}

func traceWithMessageAction(
	marker core.ModelEvent,
	message core.MessageID,
	from, to core.NodeID,
) core.Trace {
	trace := traceWithEffects(core.Effect{
		Kind: core.EffectModelEvent, ModelEvent: modelEventPointer(marker),
	})
	trace.Steps[0].Action = core.Action{
		Kind: core.ActionDeliver, Message: message,
		Selector: &core.MessageSelector{Link: core.LinkID{From: from, To: to}, Position: 0},
	}
	nodes := []core.NodeObservation{semanticNode(from), semanticNode(to), semanticNode(40)}
	trace.Steps[0].NodesBefore = nodes
	trace.Steps[0].NodesAfter = nodes
	return trace
}
