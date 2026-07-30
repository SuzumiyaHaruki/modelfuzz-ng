package facet

import (
	"strings"
	"testing"

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/assurance/executionrecord"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/core"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/model"
)

func TestPrepareInputV1ValidatesRecordAndEvidence(t *testing.T) {
	trace := testTrace(nil)
	base := testInput(&trace)
	if _, status, detail := PrepareInputV1(base); status != StatusEvaluated {
		t.Fatalf("valid input = %s: %s", status, detail)
	}
	tests := []struct {
		name   string
		mutate func(*EvaluationInputV1)
	}{
		{name: "record-schema", mutate: func(input *EvaluationInputV1) { input.Record.SchemaID = "wrong" }},
		{name: "record-version", mutate: func(input *EvaluationInputV1) { input.Record.MajorVersion = 2 }},
		{name: "record-digest", mutate: func(input *EvaluationInputV1) { input.Record.RecordDigest = "bad" }},
		{name: "trace-version-summary", mutate: func(input *EvaluationInputV1) { input.Record.Trace.Version = 2 }},
		{name: "trace-execution", mutate: func(input *EvaluationInputV1) { input.Record.Trace.ExecutionID = "other" }},
		{name: "trace-seed", mutate: func(input *EvaluationInputV1) { input.Record.Trace.Seed++ }},
		{name: "trace-step-count", mutate: func(input *EvaluationInputV1) {
			input.Record.Trace.StepCount++
			input.Record.Engine.TraceStepCount++
		}},
		{name: "effect-count", mutate: func(input *EvaluationInputV1) { input.Record.Engine.EffectCount++ }},
		{name: "summary-count", mutate: func(input *EvaluationInputV1) { input.Record.Engine.TraceStepCount++ }},
		{name: "trace-validation", mutate: func(input *EvaluationInputV1) {
			trace := input.Trace.Copy()
			trace.Steps[0].Action.Kind = "unknown"
			input.Trace = &trace
		}},
		{name: "initial-invalid", mutate: func(input *EvaluationInputV1) {
			input.InitialObservation = &core.Observation{Nodes: []core.NodeObservation{{}}}
		}},
		{name: "model-events", mutate: func(input *EvaluationInputV1) {
			input.ModelEvents = []model.Event{model.NewEvent("X", nil)}
		}},
		{name: "model-states", mutate: func(input *EvaluationInputV1) {
			input.ModelStates = []model.State{{Key: 1}}
		}},
		{name: "model-event-invalid", mutate: func(input *EvaluationInputV1) {
			input.Record.Engine.ModelEventCount = 1
			input.Record.Model.EventCount = 1
			input.ModelEvents = []model.Event{{}}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := testInput(&trace)
			test.mutate(&input)
			if _, status, _ := PrepareInputV1(input); status != StatusInvalidEvidence {
				t.Fatalf("status = %s, want invalid_evidence", status)
			}
		})
	}
}

func TestPrepareInputV1OptionalModelEvidence(t *testing.T) {
	trace := testTrace(nil)
	input := testInput(&trace)
	input.Record.Model.EventCount = 1
	input.Record.Engine.ModelEventCount = 1
	input.Record.Model.StateCount = 1
	input.Record.Engine.ModelStateCount = 1
	if _, status, detail := PrepareInputV1(input); status != StatusEvaluated {
		t.Fatalf("omitted optional evidence = %s: %s", status, detail)
	}
}

func TestPrepareInputV1CopiesInput(t *testing.T) {
	trace := testTrace(nil)
	initial := core.Observation{Nodes: testNodes()}
	input := testInput(&trace)
	input.InitialObservation = &initial
	prepared, status, detail := PrepareInputV1(input)
	if status != StatusEvaluated {
		t.Fatalf("%s: %s", status, detail)
	}
	input.InitialObservation.Nodes[0].Semantic["term"] = uint64(99)
	input.Trace.Metadata["source"] = "mutated"
	input.Trace.Steps[0].NodesAfter[0].Semantic["term"] = uint64(99)
	if prepared.InitialObservation.Nodes[0].Semantic["term"] == uint64(99) ||
		prepared.Trace.Metadata["source"] == "mutated" ||
		prepared.Trace.Steps[0].NodesAfter[0].Semantic["term"] == uint64(99) {
		t.Fatal("prepared input shares caller memory")
	}
}

func testInput(trace *core.Trace) EvaluationInputV1 {
	effects := 0
	steps := 0
	version := uint32(0)
	executionID := core.ExecutionID("")
	seed := int64(7)
	if trace != nil {
		steps = len(trace.Steps)
		version = trace.Version
		executionID = trace.ExecutionID
		seed = trace.Seed
		for _, step := range trace.Steps {
			effects += len(step.Effects)
		}
	}
	return EvaluationInputV1{
		Record: executionrecord.CompletedExecutionRecordV1{
			SchemaID: executionrecord.SchemaIDV1, MajorVersion: executionrecord.MajorVersionV1,
			RecordDigest: strings.Repeat("a", 64),
			Engine: executionrecord.EngineOutcome{
				TraceStepCount: steps, EffectCount: effects,
			},
			Trace: executionrecord.TraceSummary{
				Version: version, ExecutionID: executionID, Seed: seed, StepCount: steps,
			},
			Model: executionrecord.ModelSummary{},
		},
		Trace: trace,
	}
}

func testTrace(effects []core.Effect) core.Trace {
	nodes := testNodes()
	return core.Trace{
		Version: core.CurrentTraceVersion, ExecutionID: "test-execution", Seed: 7,
		Metadata: map[string]string{"source": "test"},
		Steps: []core.StepRecord{{
			Index: 0, Action: core.Action{Kind: core.ActionHeal}, Effects: effects,
			NodesBefore: copyTestNodes(nodes), NodesAfter: copyTestNodes(nodes),
			ObservationDigest: strings.Repeat("b", 64),
		}},
	}
}

func testNodes() []core.NodeObservation {
	return []core.NodeObservation{{
		ID: 1, Epoch: 1, Status: core.NodeRunning,
		Semantic: map[string]any{
			"role": "follower", "term": uint64(1),
			"last_index": uint64(0), "commit": uint64(0), "applied": uint64(0),
		},
	}}
}

func copyTestNodes(nodes []core.NodeObservation) []core.NodeObservation {
	result := make([]core.NodeObservation, len(nodes))
	for index, node := range nodes {
		result[index] = node.Copy()
	}
	return result
}
