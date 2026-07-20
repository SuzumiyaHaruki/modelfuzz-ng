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

func TestEngineReturnsPartialArtifactsOnMappingFailure(t *testing.T) {
	executor := &recordingExecutor{}
	engine := newTestEngine(t, plan.DefaultResolverConfig(), executor, Config{})

	result, err := engine.Run(context.Background(), plan.PlanSequence{Actions: []plan.PlanAction{{
		Kind: plan.ActionCrash, Node: 1,
	}}})
	if !errors.Is(err, ErrMapping) || result.Status != StatusMappingFailed {
		t.Fatalf("result/error = %+v/%v, want mapping failure", result, err)
	}
	if len(result.Actions.Actions) != 1 || len(result.Trace.Steps) != 1 || executor.calls != 0 {
		t.Fatalf("partial artifacts = %+v, calls=%d", result, executor.calls)
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

func newTestEngine(t *testing.T, resolverConfig plan.ResolverConfig, executor model.Executor, engineConfig Config) *Engine {
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
	engine, err := New(runtime, resolver, mapper, executor, engineConfig)
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
