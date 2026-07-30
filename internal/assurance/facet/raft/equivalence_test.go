package raft

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/assurance/executionrecord"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/assurance/facet"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/core"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/engine"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/experiment"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/model"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/plan"
	runtimepkg "github.com/SuzumiyaHaruki/modelfuzz-ng/internal/runtime"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/sut"
)

func TestFeatureOffEvaluationEquivalence(t *testing.T) {
	sequence := plan.PlanSequence{Actions: []plan.PlanAction{{Kind: plan.ActionTimeout, Node: 1}}}

	offEngine, offExecutor := newFacetEquivalenceEngine(t)
	off, err := offEngine.Run(context.Background(), sequence)
	if err != nil {
		t.Fatal(err)
	}

	onEngine, onExecutor := newFacetEquivalenceEngine(t)
	on, err := onEngine.Run(context.Background(), sequence)
	if err != nil {
		t.Fatal(err)
	}
	record, err := executionrecord.BuildV1(facetCompletion(sequence, on))
	if err != nil {
		t.Fatal(err)
	}
	evaluations, err := facet.EvaluateAll(facet.EvaluationInputV1{
		Record: record, InitialObservation: &on.Initial, Trace: &on.Trace,
		ModelEvents: on.ModelEvents, ModelStates: on.ModelStates,
	}, CatalogV1())
	if err != nil {
		t.Fatal(err)
	}
	if len(evaluations) != 3 ||
		evaluations[0].Status != facet.StatusEvaluated ||
		evaluations[1].Status != facet.StatusEvaluated ||
		evaluations[2].Status != facet.StatusNotApplicable {
		t.Fatalf("unexpected offline evaluations: %+v", evaluations)
	}
	if !reflect.DeepEqual(off, on) {
		t.Fatalf("post-completion Facet evaluation changed execution:\noff=%+v\non=%+v", off, on)
	}
	if offExecutor.calls != 1 || onExecutor.calls != 1 {
		t.Fatalf("model executor calls off/on = %d/%d", offExecutor.calls, onExecutor.calls)
	}
}

type facetEquivalenceAdapter struct{}

func (*facetEquivalenceAdapter) Capabilities() sut.Capabilities {
	return sut.Capabilities{ForceTimeout: true}
}

func (*facetEquivalenceAdapter) Reset(context.Context, sut.ResetOptions) error { return nil }
func (*facetEquivalenceAdapter) Tick(context.Context, core.LogicalTime) ([]core.Effect, error) {
	return nil, nil
}
func (*facetEquivalenceAdapter) Deliver(
	context.Context, core.LogicalTime, core.Message,
) ([]core.Effect, error) {
	return nil, nil
}
func (*facetEquivalenceAdapter) Drop(
	context.Context, core.LogicalTime, core.Message,
) ([]core.Effect, error) {
	return nil, nil
}
func (*facetEquivalenceAdapter) ForceTimeout(
	_ context.Context, at core.LogicalTime, node core.NodeID,
) ([]core.Effect, error) {
	return []core.Effect{{
		At: at, Kind: core.EffectTimerFired,
		TimerFired: &core.TimerFired{
			Node: node, Epoch: 1, Source: core.TimerFireForced, TypeHint: "election",
		},
	}}, nil
}
func (*facetEquivalenceAdapter) Crash(
	context.Context, core.LogicalTime, core.NodeID,
) ([]core.Effect, error) {
	return nil, nil
}
func (*facetEquivalenceAdapter) Restart(
	context.Context, core.LogicalTime, core.NodeID,
) ([]core.Effect, error) {
	return nil, nil
}
func (*facetEquivalenceAdapter) Request(
	context.Context, core.LogicalTime, core.NodeID, []byte,
) ([]core.Effect, error) {
	return nil, nil
}
func (*facetEquivalenceAdapter) Observe(
	_ context.Context, at core.LogicalTime,
) (core.Observation, error) {
	return core.Observation{
		Time: at,
		Nodes: []core.NodeObservation{{
			ID: 1, Epoch: 1, Status: core.NodeRunning,
			Semantic: map[string]any{
				"role": "follower", "term": uint64(1),
				"last_index": uint64(1), "commit": uint64(1), "applied": uint64(1),
			},
		}},
	}, nil
}

type facetEquivalenceMapper struct{}

func (facetEquivalenceMapper) Map(model.Transition) ([]model.Event, error) {
	return []model.Event{model.NewEvent("Timeout", map[string]any{"node": 1})}, nil
}

type facetEquivalenceExecutor struct {
	calls int
}

func (executor *facetEquivalenceExecutor) Execute(
	context.Context, []model.Event,
) ([]model.State, error) {
	executor.calls++
	return []model.State{{Text: "opaque state text", Key: 9}}, nil
}

func newFacetEquivalenceEngine(t *testing.T) (*engine.Engine, *facetEquivalenceExecutor) {
	t.Helper()
	runtime, err := runtimepkg.New(&facetEquivalenceAdapter{}, runtimepkg.Config{
		ExecutionID: "facet-equivalence", Seed: 91,
	})
	if err != nil {
		t.Fatal(err)
	}
	resolver, err := plan.NewResolver(plan.DefaultResolverConfig())
	if err != nil {
		t.Fatal(err)
	}
	executor := &facetEquivalenceExecutor{}
	result, err := engine.New(runtime, resolver, facetEquivalenceMapper{}, executor, engine.Config{})
	if err != nil {
		t.Fatal(err)
	}
	return result, executor
}

func facetCompletion(sequence plan.PlanSequence, result engine.Result) executionrecord.BuildInput {
	effects := 0
	for _, step := range result.Trace.Steps {
		effects += len(step.Effects)
	}
	candidatePlan := sequence.Copy()
	return executionrecord.BuildInput{
		Completion: experiment.Completion{
			Candidate: experiment.Candidate{
				ID: "facet-candidate", Kind: experiment.CandidateInitial,
				Source: "test", Plan: &candidatePlan,
			},
			Run: experiment.Run{
				Completed: true, Index: 0, Seed: 91, Status: result.Status, Succeeded: true,
				Actions: len(result.Actions.Actions), Effects: effects,
				ModelEvents: len(result.ModelEvents), ModelStates: len(result.ModelStates),
				OracleFindings: len(result.OracleFindings), BudgetExhausted: result.BudgetExhausted,
				CandidateID: "facet-candidate", CandidateKind: experiment.CandidateInitial,
				Source: "test", Termination: result.Termination, TerminationCode: result.TerminationCode,
				PlanDigest: digest('8'), TraceDigest: digest('9'), ModelStatePathDigest: digest('a'),
			},
			Execution: experiment.FeedbackExecution{Plan: sequence.Copy(), Result: result},
		},
		ConfigurationFingerprint: digest('b'),
		Artifacts: []executionrecord.ArtifactReference{
			{Kind: executionrecord.ArtifactConfig, Path: "config.json", SHA256: digest('c')},
			{Kind: executionrecord.ArtifactTrace, Path: "trace.json", SHA256: digest('d')},
		},
		FailureSignature: executionrecord.FailureSignatureInput{
			Availability: executionrecord.FailureSignatureNotApplicable,
		},
	}
}

func digest(character byte) string {
	return strings.Repeat(string(character), 64)
}
