package executionrecord

import (
	"context"
	"reflect"
	"testing"

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/core"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/engine"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/experiment"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/model"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/plan"
	runtimepkg "github.com/SuzumiyaHaruki/modelfuzz-ng/internal/runtime"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/sut"
)

func TestFeatureOffExecutionEquivalence(t *testing.T) {
	sequence := plan.PlanSequence{Actions: []plan.PlanAction{{Kind: plan.ActionTimeout, Node: 1}}}

	offEngine, offExecutor := newEquivalenceEngine(t)
	off, err := offEngine.Run(context.Background(), sequence)
	if err != nil {
		t.Fatal(err)
	}

	onEngine, onExecutor := newEquivalenceEngine(t)
	on, err := onEngine.Run(context.Background(), sequence)
	if err != nil {
		t.Fatal(err)
	}
	input := completionForEquivalence(sequence, on)
	if _, err := BuildV1(input); err != nil {
		t.Fatal(err)
	}

	if !reflect.DeepEqual(off, on) {
		t.Fatalf("post-completion BuildV1 changed observable execution:\noff=%+v\non=%+v", off, on)
	}
	if offExecutor.calls != 1 || onExecutor.calls != 1 {
		t.Fatalf("model executor calls off/on = %d/%d", offExecutor.calls, onExecutor.calls)
	}
}

type equivalenceAdapter struct{}

func (*equivalenceAdapter) Capabilities() sut.Capabilities {
	return sut.Capabilities{ForceTimeout: true}
}

func (*equivalenceAdapter) Reset(context.Context, sut.ResetOptions) error { return nil }
func (*equivalenceAdapter) Tick(context.Context, core.LogicalTime) ([]core.Effect, error) {
	return nil, nil
}
func (*equivalenceAdapter) Deliver(context.Context, core.LogicalTime, core.Message) ([]core.Effect, error) {
	return nil, nil
}
func (*equivalenceAdapter) Drop(context.Context, core.LogicalTime, core.Message) ([]core.Effect, error) {
	return nil, nil
}
func (*equivalenceAdapter) ForceTimeout(_ context.Context, at core.LogicalTime, node core.NodeID) ([]core.Effect, error) {
	return []core.Effect{{
		At: at, Kind: core.EffectTimerFired,
		TimerFired: &core.TimerFired{
			Node: node, Epoch: 1, Source: core.TimerFireForced, TypeHint: "election",
		},
	}}, nil
}
func (*equivalenceAdapter) Crash(context.Context, core.LogicalTime, core.NodeID) ([]core.Effect, error) {
	return nil, nil
}
func (*equivalenceAdapter) Restart(context.Context, core.LogicalTime, core.NodeID) ([]core.Effect, error) {
	return nil, nil
}
func (*equivalenceAdapter) Request(context.Context, core.LogicalTime, core.NodeID, []byte) ([]core.Effect, error) {
	return nil, nil
}
func (*equivalenceAdapter) Observe(_ context.Context, at core.LogicalTime) (core.Observation, error) {
	return core.Observation{
		Time: at,
		Nodes: []core.NodeObservation{{
			ID: 1, Epoch: 1, Status: core.NodeRunning,
		}},
	}, nil
}

type equivalenceMapper struct{}

func (equivalenceMapper) Map(model.Transition) ([]model.Event, error) {
	return []model.Event{model.NewEvent("Timeout", map[string]any{"node": 1})}, nil
}

type equivalenceExecutor struct {
	calls int
}

func (e *equivalenceExecutor) Execute(context.Context, []model.Event) ([]model.State, error) {
	e.calls++
	return []model.State{{Text: "state", Key: 9}}, nil
}

func newEquivalenceEngine(t *testing.T) (*engine.Engine, *equivalenceExecutor) {
	t.Helper()
	runtime, err := runtimepkg.New(&equivalenceAdapter{}, runtimepkg.Config{
		ExecutionID: "execution-equivalence", Seed: 91,
	})
	if err != nil {
		t.Fatal(err)
	}
	resolver, err := plan.NewResolver(plan.DefaultResolverConfig())
	if err != nil {
		t.Fatal(err)
	}
	executor := &equivalenceExecutor{}
	result, err := engine.New(runtime, resolver, equivalenceMapper{}, executor, engine.Config{})
	if err != nil {
		t.Fatal(err)
	}
	return result, executor
}

func completionForEquivalence(sequence plan.PlanSequence, result engine.Result) BuildInput {
	effects := 0
	for _, step := range result.Trace.Steps {
		effects += len(step.Effects)
	}
	candidatePlan := sequence.Copy()
	return BuildInput{
		Completion: experiment.Completion{
			Candidate: experiment.Candidate{
				ID: "candidate-equivalence", Kind: experiment.CandidateInitial,
				Source: "test", Plan: &candidatePlan,
			},
			Run: experiment.Run{
				Completed: true, Index: 0, Seed: 91, Status: result.Status, Succeeded: true,
				Actions: len(result.Actions.Actions), Effects: effects,
				ModelEvents: len(result.ModelEvents), ModelStates: len(result.ModelStates),
				OracleFindings: len(result.OracleFindings), BudgetExhausted: result.BudgetExhausted,
				CandidateID: "candidate-equivalence", CandidateKind: experiment.CandidateInitial,
				Source: "test", Termination: result.Termination, TerminationCode: result.TerminationCode,
				PlanDigest: digestOf('8'), TraceDigest: digestOf('9'), ModelStatePathDigest: digestOf('a'),
			},
			Execution: experiment.FeedbackExecution{Plan: sequence.Copy(), Result: result},
		},
		ConfigurationFingerprint: digestOf('b'),
		Artifacts: []ArtifactReference{
			{Kind: ArtifactConfig, Path: "config.json", SHA256: digestOf('c')},
			{Kind: ArtifactTrace, Path: "trace.json", SHA256: digestOf('d')},
		},
		FailureSignature: FailureSignatureInput{Availability: FailureSignatureNotApplicable},
	}
}
