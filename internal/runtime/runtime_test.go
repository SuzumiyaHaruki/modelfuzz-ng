package runtime

import (
	"context"
	"errors"
	"sort"
	"testing"

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/core"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/sut"
)

type fakeNode struct {
	epoch  core.NodeEpoch
	status core.NodeStatus
}

type fakeAdapter struct {
	capabilities sut.Capabilities
	nodes        map[core.NodeID]fakeNode
	ticks        []core.LogicalTime
	delivered    []core.Message

	tickEffects    func(core.LogicalTime) []core.Effect
	deliverEffects func(core.LogicalTime, core.Message) []core.Effect
	timeoutEffects func(core.LogicalTime, core.NodeID) []core.Effect
}

func newFakeAdapter() *fakeAdapter {
	return &fakeAdapter{
		capabilities: sut.Capabilities{
			ForceTimeout:  true,
			CrashRestart:  true,
			ClientRequest: true,
		},
	}
}

func (f *fakeAdapter) Capabilities() sut.Capabilities {
	return f.capabilities
}

func (f *fakeAdapter) Reset(context.Context, sut.ResetOptions) error {
	f.nodes = map[core.NodeID]fakeNode{
		1: {epoch: 1, status: core.NodeRunning},
		2: {epoch: 1, status: core.NodeRunning},
		3: {epoch: 1, status: core.NodeRunning},
	}
	f.ticks = nil
	f.delivered = nil
	return nil
}

func (f *fakeAdapter) Tick(_ context.Context, at core.LogicalTime) ([]core.Effect, error) {
	f.ticks = append(f.ticks, at)
	if f.tickEffects != nil {
		return f.tickEffects(at), nil
	}
	return nil, nil
}

func (f *fakeAdapter) Deliver(_ context.Context, at core.LogicalTime, message core.Message) ([]core.Effect, error) {
	f.delivered = append(f.delivered, message.Copy())
	if f.deliverEffects != nil {
		return f.deliverEffects(at, message), nil
	}
	return nil, nil
}

func (f *fakeAdapter) ForceTimeout(_ context.Context, at core.LogicalTime, node core.NodeID) ([]core.Effect, error) {
	if f.timeoutEffects != nil {
		return f.timeoutEffects(at, node), nil
	}
	n := f.nodes[node]
	return []core.Effect{{
		At:   at,
		Kind: core.EffectTimerFired,
		TimerFired: &core.TimerFired{
			Node: node, Epoch: n.epoch, Source: core.TimerFireForced, TypeHint: "election",
		},
	}}, nil
}

func (f *fakeAdapter) Crash(_ context.Context, _ core.LogicalTime, node core.NodeID) ([]core.Effect, error) {
	n := f.nodes[node]
	n.status = core.NodeCrashed
	f.nodes[node] = n
	return nil, nil
}

func (f *fakeAdapter) Restart(_ context.Context, _ core.LogicalTime, node core.NodeID) ([]core.Effect, error) {
	n := f.nodes[node]
	n.epoch++
	n.status = core.NodeRunning
	f.nodes[node] = n
	return nil, nil
}

func (f *fakeAdapter) Request(_ context.Context, at core.LogicalTime, node core.NodeID, request []byte) ([]core.Effect, error) {
	return []core.Effect{{
		At:   at,
		Kind: core.EffectModelEvent,
		ModelEvent: &core.ModelEvent{
			Name: "ClientRequest", Node: node, Params: map[string]any{"request": string(request)},
		},
	}}, nil
}

func (f *fakeAdapter) Observe(_ context.Context, at core.LogicalTime) (core.Observation, error) {
	ids := make([]int, 0, len(f.nodes))
	for id := range f.nodes {
		ids = append(ids, int(id))
	}
	sort.Ints(ids)
	nodes := make([]core.NodeObservation, 0, len(ids))
	for _, rawID := range ids {
		id := core.NodeID(rawID)
		node := f.nodes[id]
		nodes = append(nodes, core.NodeObservation{ID: id, Epoch: node.epoch, Status: node.status})
	}
	return core.Observation{Time: at, Nodes: nodes}, nil
}

func newTestRuntime(t *testing.T, adapter *fakeAdapter) *Runtime {
	t.Helper()
	return newTestRuntimeWithConfig(t, adapter, Config{ExecutionID: "test-execution", Seed: 42})
}

func newTestRuntimeWithConfig(t *testing.T, adapter *fakeAdapter, config Config) *Runtime {
	t.Helper()
	runtime, err := New(adapter, config)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.Reset(context.Background()); err != nil {
		t.Fatal(err)
	}
	return runtime
}

func TestRuntimeEnforcesGlobalLimits(t *testing.T) {
	t.Run("actions", func(t *testing.T) {
		runtime := newTestRuntimeWithConfig(t, newFakeAdapter(), Config{
			ExecutionID: "action-limit", Seed: 42, Limits: Limits{MaxActions: 1},
		})
		if _, err := runtime.Execute(context.Background(), core.Action{Kind: core.ActionTimeout, Node: 1}); err != nil {
			t.Fatal(err)
		}
		_, err := runtime.Execute(context.Background(), core.Action{Kind: core.ActionTimeout, Node: 1})
		if !errors.Is(err, ErrBudgetExceeded) {
			t.Fatalf("second action error = %v, want ErrBudgetExceeded", err)
		}
	})

	t.Run("ticks", func(t *testing.T) {
		runtime := newTestRuntimeWithConfig(t, newFakeAdapter(), Config{
			ExecutionID: "tick-limit", Seed: 42, Limits: Limits{MaxTicks: 2},
		})
		_, err := runtime.Execute(context.Background(), core.Action{Kind: core.ActionAdvanceTime, TargetTime: 3})
		if !errors.Is(err, ErrBudgetExceeded) || runtime.Time() != 0 {
			t.Fatalf("advance error/time = %v/%d, want ErrBudgetExceeded/0", err, runtime.Time())
		}
	})

	t.Run("effects", func(t *testing.T) {
		adapter := newFakeAdapter()
		adapter.tickEffects = func(at core.LogicalTime) []core.Effect {
			return []core.Effect{
				{At: at, Kind: core.EffectModelEvent, ModelEvent: &core.ModelEvent{Name: "one"}},
				{At: at, Kind: core.EffectModelEvent, ModelEvent: &core.ModelEvent{Name: "two"}},
			}
		}
		runtime := newTestRuntimeWithConfig(t, adapter, Config{
			ExecutionID: "effect-limit", Seed: 42, Limits: Limits{MaxEffects: 1},
		})
		_, err := runtime.Execute(context.Background(), core.Action{Kind: core.ActionAdvanceTime, TargetTime: 1})
		if !errors.Is(err, ErrBudgetExceeded) {
			t.Fatalf("effect limit error = %v, want ErrBudgetExceeded", err)
		}
		if _, err := runtime.CurrentObservation(); !errors.Is(err, ErrTerminated) {
			t.Fatalf("runtime after partial adapter operation = %v, want ErrTerminated", err)
		}
	})

	t.Run("queued messages", func(t *testing.T) {
		adapter := newFakeAdapter()
		adapter.tickEffects = func(at core.LogicalTime) []core.Effect {
			first := outbound(1, 2, "first")
			second := outbound(1, 2, "second")
			return []core.Effect{
				{At: at, Kind: core.EffectSendMessage, Message: &first},
				{At: at, Kind: core.EffectSendMessage, Message: &second},
			}
		}
		runtime := newTestRuntimeWithConfig(t, adapter, Config{
			ExecutionID: "queue-limit", Seed: 42, Limits: Limits{MaxQueuedMessages: 1},
		})
		_, err := runtime.Execute(context.Background(), core.Action{Kind: core.ActionAdvanceTime, TargetTime: 1})
		if !errors.Is(err, ErrBudgetExceeded) {
			t.Fatalf("queue limit error = %v, want ErrBudgetExceeded", err)
		}
	})
}

func TestRuntimeAdvanceRegistersEffectsAndDeliverDrainsImmediately(t *testing.T) {
	adapter := newFakeAdapter()
	adapter.tickEffects = func(at core.LogicalTime) []core.Effect {
		switch at {
		case 1:
			message := outbound(1, 2, "heartbeat")
			return []core.Effect{{At: at, Kind: core.EffectSendMessage, Message: &message}}
		case 2:
			return []core.Effect{{
				At: at, Kind: core.EffectTimerFired,
				TimerFired: &core.TimerFired{
					Node: 3, Epoch: 1, Source: core.TimerFireNatural, TypeHint: "election",
				},
			}}
		default:
			return nil
		}
	}
	adapter.deliverEffects = func(at core.LogicalTime, delivered core.Message) []core.Effect {
		message := outbound(delivered.To, delivered.From, "heartbeat_response")
		return []core.Effect{{At: at, Kind: core.EffectSendMessage, Message: &message}}
	}
	runtime := newTestRuntime(t, adapter)

	advance, err := runtime.Execute(context.Background(), core.Action{
		Kind: core.ActionAdvanceTime, TargetTime: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := adapter.ticks; len(got) != 2 || got[0] != 1 || got[1] != 2 {
		t.Fatalf("ticks = %v, want [1 2]", got)
	}
	if len(advance.Record.Effects) != 2 || advance.Record.Effects[0].At != 1 || advance.Record.Effects[1].At != 2 {
		t.Fatalf("advance effects = %+v", advance.Record.Effects)
	}
	sent := advance.Record.Effects[0].Message
	if sent == nil || sent.ID != 1 || sent.Sequence != 1 {
		t.Fatalf("registered send effect = %+v", sent)
	}
	if len(advance.Observation.Messages) != 1 || advance.Observation.Messages[0].ID != sent.ID ||
		advance.Observation.Messages[0].EnqueuedAt != 1 {
		t.Fatalf("advance observation messages = %+v", advance.Observation.Messages)
	}
	if advance.Record.ObservationDigest == "" {
		t.Fatal("observation digest is empty")
	}
	if advance.BeforeObservation.Time != 0 || len(advance.BeforeObservation.Messages) != 0 {
		t.Fatalf("before observation = %+v", advance.BeforeObservation)
	}

	deliver, err := runtime.Execute(context.Background(), core.Action{
		Kind:    core.ActionDeliver,
		Message: sent.ID,
		Selector: &core.MessageSelector{
			Link: sent.Link(), Position: 0,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(adapter.delivered) != 1 || adapter.delivered[0].ID != sent.ID {
		t.Fatalf("delivered messages = %+v", adapter.delivered)
	}
	if len(deliver.Record.Effects) != 1 || deliver.Record.Effects[0].Message.ID != 2 {
		t.Fatalf("deliver effects = %+v", deliver.Record.Effects)
	}
	if deliver.Record.TimeBefore != 2 || deliver.Record.TimeAfter != 2 {
		t.Fatalf("deliver time = %d -> %d, want 2 -> 2", deliver.Record.TimeBefore, deliver.Record.TimeAfter)
	}
	if len(deliver.Observation.Messages) != 1 || deliver.Observation.Messages[0].TypeHint != "heartbeat_response" {
		t.Fatalf("messages after deliver = %+v", deliver.Observation.Messages)
	}

	trace, err := runtime.Trace()
	if err != nil {
		t.Fatal(err)
	}
	if len(trace.Steps) != 2 {
		t.Fatalf("trace steps = %d, want 2", len(trace.Steps))
	}
}

func TestRuntimeCrashRestartTimeoutAndRequest(t *testing.T) {
	runtime := newTestRuntime(t, newFakeAdapter())
	ctx := context.Background()

	crash, err := runtime.Execute(ctx, core.Action{Kind: core.ActionCrash, Node: 2})
	if err != nil {
		t.Fatal(err)
	}
	if crash.Observation.Nodes[1].Status != core.NodeCrashed {
		t.Fatalf("node 2 after crash = %+v", crash.Observation.Nodes[1])
	}
	if _, err := runtime.Execute(ctx, core.Action{Kind: core.ActionCrash, Node: 2}); !errors.Is(err, ErrInvalidAction) {
		t.Fatalf("second crash error = %v, want ErrInvalidAction", err)
	}

	restart, err := runtime.Execute(ctx, core.Action{Kind: core.ActionRestart, Node: 2})
	if err != nil {
		t.Fatal(err)
	}
	if restart.Observation.Nodes[1].Status != core.NodeRunning || restart.Observation.Nodes[1].Epoch != 2 {
		t.Fatalf("node 2 after restart = %+v", restart.Observation.Nodes[1])
	}

	timeout, err := runtime.Execute(ctx, core.Action{Kind: core.ActionTimeout, Node: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(timeout.Record.Effects) != 1 || timeout.Record.Effects[0].TimerFired.Source != core.TimerFireForced {
		t.Fatalf("timeout effects = %+v", timeout.Record.Effects)
	}

	request, err := runtime.Execute(ctx, core.Action{Kind: core.ActionRequest, Node: 2, Request: []byte("write")})
	if err != nil {
		t.Fatal(err)
	}
	if len(request.Record.Effects) != 1 || request.Record.Effects[0].ModelEvent.Name != "ClientRequest" {
		t.Fatalf("request effects = %+v", request.Record.Effects)
	}
}

func TestRuntimeDuplicateAndDropUseCurrentQueuePosition(t *testing.T) {
	adapter := newFakeAdapter()
	adapter.tickEffects = func(at core.LogicalTime) []core.Effect {
		if at != 1 {
			return nil
		}
		first := outbound(1, 2, "first")
		second := outbound(1, 2, "second")
		return []core.Effect{
			{At: at, Kind: core.EffectSendMessage, Message: &first},
			{At: at, Kind: core.EffectSendMessage, Message: &second},
		}
	}
	runtime := newTestRuntime(t, adapter)
	ctx := context.Background()
	advance, err := runtime.Execute(ctx, core.Action{Kind: core.ActionAdvanceTime, TargetTime: 1})
	if err != nil {
		t.Fatal(err)
	}
	link := core.LinkID{From: 1, To: 2}
	firstID := advance.Observation.Messages[0].ID
	secondID := advance.Observation.Messages[1].ID

	duplicated, err := runtime.Execute(ctx, core.Action{
		Kind: core.ActionDuplicate, Message: firstID,
		Selector: &core.MessageSelector{Link: link, Position: 0},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(duplicated.Record.Effects) != 0 || len(duplicated.Observation.Messages) != 3 {
		t.Fatalf("duplicate result = %+v", duplicated)
	}
	duplicate := duplicated.Observation.Messages[2]
	if duplicate.ParentID != firstID || duplicate.Position != 2 {
		t.Fatalf("duplicate observation = %+v", duplicate)
	}

	dropped, err := runtime.Execute(ctx, core.Action{
		Kind: core.ActionDrop, Message: secondID,
		Selector: &core.MessageSelector{Link: link, Position: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(dropped.Record.Effects) != 0 || len(dropped.Observation.Messages) != 2 {
		t.Fatalf("drop result = %+v", dropped)
	}
	if dropped.Observation.Messages[1].ID != duplicate.ID || dropped.Observation.Messages[1].Position != 1 {
		t.Fatalf("positions after drop = %+v", dropped.Observation.Messages)
	}

	_, err = runtime.Execute(ctx, core.Action{
		Kind: core.ActionDrop, Message: duplicate.ID,
		Selector: &core.MessageSelector{Link: link, Position: 2},
	})
	if !errors.Is(err, ErrInvalidAction) {
		t.Fatalf("stale position error = %v, want ErrInvalidAction", err)
	}
	if _, err := runtime.CurrentObservation(); err != nil {
		t.Fatalf("invalid action unexpectedly terminated runtime: %v", err)
	}
}

func TestRuntimeRejectsUnsupportedOptionalAction(t *testing.T) {
	adapter := newFakeAdapter()
	adapter.capabilities.ForceTimeout = false
	runtime := newTestRuntime(t, adapter)

	_, err := runtime.Execute(context.Background(), core.Action{Kind: core.ActionTimeout, Node: 1})
	if !errors.Is(err, ErrUnsupportedAction) {
		t.Fatalf("unsupported timeout error = %v, want ErrUnsupportedAction", err)
	}
	if _, err := runtime.CurrentObservation(); err != nil {
		t.Fatalf("unsupported action unexpectedly terminated runtime: %v", err)
	}
}

func TestRuntimeRejectsAdapterContractViolationAndTerminates(t *testing.T) {
	adapter := newFakeAdapter()
	adapter.tickEffects = func(core.LogicalTime) []core.Effect {
		message := outbound(1, 2, "wrong-time")
		return []core.Effect{{At: 0, Kind: core.EffectSendMessage, Message: &message}}
	}
	runtime := newTestRuntime(t, adapter)

	_, err := runtime.Execute(context.Background(), core.Action{Kind: core.ActionAdvanceTime, TargetTime: 1})
	if !errors.Is(err, ErrAdapterContract) {
		t.Fatalf("contract error = %v, want ErrAdapterContract", err)
	}
	if runtime.Time() != 1 {
		t.Fatalf("time after failed tick = %d, want 1", runtime.Time())
	}
	if _, err := runtime.Execute(context.Background(), core.Action{Kind: core.ActionAdvanceTime, TargetTime: 2}); !errors.Is(err, ErrTerminated) {
		t.Fatalf("execute after terminal error = %v, want ErrTerminated", err)
	}
}

func TestRuntimeRejectsIncorrectTimeoutSource(t *testing.T) {
	t.Run("tick cannot report forced timeout", func(t *testing.T) {
		adapter := newFakeAdapter()
		adapter.tickEffects = func(at core.LogicalTime) []core.Effect {
			return []core.Effect{{
				At: at, Kind: core.EffectTimerFired,
				TimerFired: &core.TimerFired{Node: 1, Epoch: 1, Source: core.TimerFireForced},
			}}
		}
		runtime := newTestRuntime(t, adapter)
		_, err := runtime.Execute(context.Background(), core.Action{Kind: core.ActionAdvanceTime, TargetTime: 1})
		if !errors.Is(err, ErrAdapterContract) {
			t.Fatalf("wrong tick timeout source error = %v, want ErrAdapterContract", err)
		}
	})

	t.Run("forced timeout must be recorded", func(t *testing.T) {
		adapter := newFakeAdapter()
		adapter.timeoutEffects = func(core.LogicalTime, core.NodeID) []core.Effect { return nil }
		runtime := newTestRuntime(t, adapter)
		_, err := runtime.Execute(context.Background(), core.Action{Kind: core.ActionTimeout, Node: 1})
		if !errors.Is(err, ErrAdapterContract) {
			t.Fatalf("missing forced timeout effect error = %v, want ErrAdapterContract", err)
		}
	})
}
