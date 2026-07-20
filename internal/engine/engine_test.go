package engine

import (
	"context"
	"errors"
	"io"
	"log"
	"testing"

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/adapters/etcdraft"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/core"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/model"
	raftmodel "github.com/SuzumiyaHaruki/modelfuzz-ng/internal/model/raft"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/oracle"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/plan"
	runtimepkg "github.com/SuzumiyaHaruki/modelfuzz-ng/internal/runtime"
	raft "go.etcd.io/raft/v3"
)

type recordingExecutor struct {
	events []model.Event
	states []model.State
	err    error
	calls  int
}

type rejectingOracle struct{}

type adaptiveSource struct {
	step int
}

func (s *adaptiveSource) Reset(initial core.Observation) error {
	s.step = 0
	if len(initial.Messages) != 0 {
		return errors.New("initial network is not empty")
	}
	return nil
}

func (s *adaptiveSource) Next(observation core.Observation) (plan.PlanAction, bool, error) {
	switch s.step {
	case 0:
		s.step++
		return plan.PlanAction{Kind: plan.ActionTimeout, Node: 1}, true, nil
	case 1:
		s.step++
		if len(observation.Messages) == 0 {
			return plan.PlanAction{}, false, errors.New("timeout produced no vote messages")
		}
		message := observation.Messages[0]
		return plan.PlanAction{Kind: plan.ActionDeliver, Messages: &plan.MessageRangeSelector{
			Link: core.LinkID{From: message.From, To: message.To}, Start: message.Position, Count: 1,
		}}, true, nil
	default:
		return plan.PlanAction{}, false, nil
	}
}

func (rejectingOracle) Reset(core.Observation) []oracle.Finding { return nil }

func (rejectingOracle) Check(model.Transition) []oracle.Finding {
	return []oracle.Finding{{Oracle: "test", Code: "injected", Message: "injected violation"}}
}

func (e *recordingExecutor) Execute(_ context.Context, events []model.Event) ([]model.State, error) {
	e.calls++
	e.events = make([]model.Event, len(events))
	for i, event := range events {
		e.events[i] = event.Copy()
	}
	return append([]model.State(nil), e.states...), e.err
}

func TestEngineRunsPlanThroughRaftMapperAndModel(t *testing.T) {
	executor := &recordingExecutor{states: []model.State{{Text: "model-state", Key: 7}}}
	engine := newTestEngine(t, plan.DefaultResolverConfig(), executor, Config{})
	sequence := plan.PlanSequence{Actions: []plan.PlanAction{
		{Kind: plan.ActionTimeout, Node: 1},
		{Kind: plan.ActionDeliver, Messages: &plan.MessageRangeSelector{
			Link: core.LinkID{From: 1, To: 2}, Count: 1,
		}},
		{Kind: plan.ActionDeliver, Messages: &plan.MessageRangeSelector{
			Link: core.LinkID{From: 2, To: 1}, Count: 1,
		}},
	}}

	result, err := engine.Run(context.Background(), sequence)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != StatusCompleted || !result.ModelExecuted || executor.calls != 1 {
		t.Fatalf("result status = %+v, executor calls = %d", result, executor.calls)
	}
	if len(result.Resolutions) != 3 || len(result.Actions.Actions) != 3 || len(result.Trace.Steps) != 3 {
		t.Fatalf("execution sizes: resolutions=%d actions=%d steps=%d",
			len(result.Resolutions), len(result.Actions.Actions), len(result.Trace.Steps))
	}
	if err := result.Trace.Validate(); err != nil {
		t.Fatalf("trace invalid: %v", err)
	}
	wantEvents := []string{"Timeout", "DeliverMessage", "DeliverMessage", "BecomeLeader", "ClientRequest"}
	if len(result.ModelEvents) != len(wantEvents) {
		t.Fatalf("model events = %+v", result.ModelEvents)
	}
	for i, name := range wantEvents {
		if result.ModelEvents[i].Name != name || executor.events[i].Name != name {
			t.Fatalf("event %d = %+v, executor = %+v, want %s", i, result.ModelEvents[i], executor.events[i], name)
		}
	}
	if len(result.ModelStates) != 1 || result.ModelStates[0].Key != 7 {
		t.Fatalf("model states = %+v", result.ModelStates)
	}
	if role := nodeRole(result.Final, 1); role != "leader" {
		t.Fatalf("final node 1 role = %q, want leader", role)
	}
}

func TestEngineRunSourceUsesLatestObservationAndStopsVoluntarily(t *testing.T) {
	engine := newTestEngine(t, plan.DefaultResolverConfig(), nil, Config{})
	result, err := engine.RunSource(context.Background(), &adaptiveSource{}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != StatusCompleted || result.BudgetExhausted || len(result.Resolutions) != 2 || len(result.Trace.Steps) != 2 {
		t.Fatalf("source result = %+v", result)
	}
	if result.Actions.Actions[1].Kind != core.ActionDeliver {
		t.Fatalf("second action = %+v", result.Actions.Actions[1])
	}
}

func TestEngineRecordsBestEffortEmptyQueue(t *testing.T) {
	executor := &recordingExecutor{}
	engine := newTestEngine(t, plan.DefaultResolverConfig(), executor, Config{})
	sequence := plan.PlanSequence{Actions: []plan.PlanAction{{
		Kind: plan.ActionDrop,
		Messages: &plan.MessageRangeSelector{
			Link: core.LinkID{From: 2, To: 3}, Count: 1,
		},
	}}}

	result, err := engine.Run(context.Background(), sequence)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != StatusCompleted || result.Resolutions[0].Status != plan.ResolutionEmptyQueue {
		t.Fatalf("result = %+v", result)
	}
	if len(result.Actions.Actions) != 0 || len(result.Trace.Steps) != 0 || executor.calls != 1 {
		t.Fatalf("best-effort execution = %+v, calls=%d", result, executor.calls)
	}
}

func TestEngineStopsOnInvalidResolution(t *testing.T) {
	resolverConfig := plan.DefaultResolverConfig()
	resolverConfig.MaxAdvanceTicks = 1
	executor := &recordingExecutor{}
	engine := newTestEngine(t, resolverConfig, executor, Config{})

	result, err := engine.Run(context.Background(), plan.PlanSequence{Actions: []plan.PlanAction{{
		Kind: plan.ActionAdvanceTicks, Ticks: 2,
	}}})
	if !errors.Is(err, ErrResolution) || result.Status != StatusResolutionFailed {
		t.Fatalf("result/error = %+v/%v, want resolution failure", result, err)
	}
	if len(result.Resolutions) != 1 || result.Resolutions[0].Status != plan.ResolutionInvalid || executor.calls != 0 {
		t.Fatalf("resolution failure result = %+v, calls=%d", result, executor.calls)
	}
	if result.Trace.Version != core.CurrentTraceVersion || len(result.Trace.Steps) != 0 {
		t.Fatalf("partial trace = %+v", result.Trace)
	}
}

func TestEngineRejectsUnsupportedActionBeforeRuntimeMutation(t *testing.T) {
	executor := &recordingExecutor{}
	engine := newTestEngine(t, plan.DefaultResolverConfig(), executor, Config{})

	result, err := engine.Run(context.Background(), plan.PlanSequence{Actions: []plan.PlanAction{{
		Kind: plan.ActionCrash, Node: 1,
	}}})
	if !errors.Is(err, ErrUnsupported) || result.Status != StatusUnsupported {
		t.Fatalf("result/error = %+v/%v, want unsupported failure", result, err)
	}
	if len(result.Actions.Actions) != 0 || len(result.Trace.Steps) != 0 || executor.calls != 0 {
		t.Fatalf("unsupported action mutated runtime = %+v, calls=%d", result, executor.calls)
	}
	if result.Final.Nodes[0].Status != core.NodeRunning {
		t.Fatalf("node was crashed before profile rejection: %+v", result.Final.Nodes[0])
	}
}

func TestEngineClassifiesModelFailure(t *testing.T) {
	executor := &recordingExecutor{err: errors.New("TLC unavailable")}
	engine := newTestEngine(t, plan.DefaultResolverConfig(), executor, Config{})

	result, err := engine.Run(context.Background(), plan.PlanSequence{})
	if !errors.Is(err, ErrModel) || result.Status != StatusModelFailed || !result.ModelExecuted {
		t.Fatalf("result/error = %+v/%v, want model failure", result, err)
	}
}

func TestEnginePersistsViolatingStepAndStopsBeforeModelExecution(t *testing.T) {
	executor := &recordingExecutor{}
	engine := newTestEngineWithOracles(t, plan.DefaultResolverConfig(), executor, Config{}, rejectingOracle{})
	result, err := engine.Run(context.Background(), plan.PlanSequence{Actions: []plan.PlanAction{{
		Kind: plan.ActionTimeout, Node: 1,
	}}})
	if !errors.Is(err, ErrOracle) || result.Status != StatusOracleFailed {
		t.Fatalf("result/error = %+v/%v, want oracle failure", result, err)
	}
	if len(result.Actions.Actions) != 1 || len(result.Trace.Steps) != 1 || len(result.ModelEvents) != 1 {
		t.Fatalf("violating transition was not fully persisted: %+v", result)
	}
	if len(result.OracleFindings) != 1 || result.OracleFindings[0].Step != 1 || executor.calls != 0 {
		t.Fatalf("oracle findings/executor = %+v/%d", result.OracleFindings, executor.calls)
	}
}

func newTestEngine(t *testing.T, resolverConfig plan.ResolverConfig, executor model.Executor, engineConfig Config) *Engine {
	return newTestEngineWithOracles(t, resolverConfig, executor, engineConfig)
}

func newTestEngineWithOracles(t *testing.T, resolverConfig plan.ResolverConfig, executor model.Executor, engineConfig Config, checkers ...oracle.Checker) *Engine {
	t.Helper()
	adapterConfig := etcdraft.DefaultConfig()
	adapterConfig.ElectionTick = 100
	adapterConfig.Logger = &raft.DefaultLogger{Logger: log.New(io.Discard, "", 0)}
	adapter, err := etcdraft.New(adapterConfig)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := runtimepkg.New(adapter, runtimepkg.Config{
		ExecutionID: "engine-test", Seed: 42,
	})
	if err != nil {
		t.Fatal(err)
	}
	resolver, err := plan.NewResolver(resolverConfig)
	if err != nil {
		t.Fatal(err)
	}
	mapper, err := raftmodel.NewMapperWithConfig(raftmodel.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	engine, err := New(runtime, resolver, mapper, executor, engineConfig, checkers...)
	if err != nil {
		t.Fatal(err)
	}
	return engine
}

func nodeRole(observation core.Observation, id core.NodeID) string {
	for _, node := range observation.Nodes {
		if node.ID == id {
			role, _ := node.Semantic["role"].(string)
			return role
		}
	}
	return ""
}
