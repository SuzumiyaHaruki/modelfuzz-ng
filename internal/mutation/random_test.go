package mutation

import (
	"context"
	"math/rand"
	"reflect"
	"testing"

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/core"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/corpus"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/plan"
)

func TestRandomMutationIsDeterministicAndValid(t *testing.T) {
	mutator, err := NewRandom(RandomConfig{NodeIDs: []core.NodeID{1, 2, 3}, MaxValue: 5, MaxTicks: 3, MaxActions: 20})
	if err != nil {
		t.Fatal(err)
	}
	entry := corpus.Entry{ID: "corpus-1", Plan: plan.PlanSequence{Actions: []plan.PlanAction{
		{Kind: plan.ActionTimeout, Node: 1},
		{Kind: plan.ActionDeliver, Messages: &plan.MessageRangeSelector{Link: core.LinkID{From: 1, To: 2}, Count: 1}},
		{Kind: plan.ActionRequest, Node: 1, Request: "1"},
	}}}
	request := Request{Entry: entry, Count: 8, Seed: 99}
	first, err := mutator.Mutate(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	second, err := mutator.Mutate(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) || len(first) != 8 {
		t.Fatalf("mutations are not deterministic: first=%+v second=%+v", first, second)
	}
	for _, candidate := range first {
		if err := candidate.Validate(); err != nil {
			t.Fatalf("invalid mutation %+v: %v", candidate, err)
		}
		if err := mutator.validateLifecycle(candidate); err != nil {
			t.Fatalf("invalid mutation lifecycle %+v: %v", candidate, err)
		}
		if candidate.Metadata["mutation_operation"] == "" {
			t.Fatalf("mutation operation was not recorded: %+v", candidate.Metadata)
		}
		if reflect.DeepEqual(candidate.Actions, entry.Plan.Actions) {
			t.Fatalf("mutation did not change actions: %+v", candidate)
		}
	}
}

func TestRandomMutationRejectsEmptyPlan(t *testing.T) {
	mutator, _ := NewRandom(RandomConfig{NodeIDs: []core.NodeID{1, 2}, MaxValue: 1, MaxTicks: 1, MaxActions: 2})
	_, err := mutator.Mutate(context.Background(), Request{Entry: corpus.Entry{Plan: plan.PlanSequence{}}, Count: 1})
	if err == nil {
		t.Fatal("empty plan mutation unexpectedly succeeded")
	}
}

func TestRandomMutationInsertsLegalCrashRestartPair(t *testing.T) {
	mutator, err := NewRandom(RandomConfig{
		NodeIDs: []core.NodeID{1, 2, 3}, MaxValue: 5, MaxTicks: 3, MaxActions: 10, MaxCrashed: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	sequence := plan.PlanSequence{Actions: []plan.PlanAction{
		{Kind: plan.ActionTimeout, Node: 1},
		{Kind: plan.ActionAdvanceTicks, Ticks: 1},
	}}
	if !mutator.insertCrashRestartPair(rand.New(rand.NewSource(7)), &sequence) {
		t.Fatal("没有生成 crash/restart 对")
	}
	if len(sequence.Actions) != 4 {
		t.Fatalf("actions = %+v", sequence.Actions)
	}
	crashIndex, restartIndex := -1, -1
	var crashedNode core.NodeID
	for index, action := range sequence.Actions {
		switch action.Kind {
		case plan.ActionCrash:
			crashIndex, crashedNode = index, action.Node
		case plan.ActionRestart:
			restartIndex = index
			if action.Node != crashedNode {
				t.Fatalf("restart node = %d, crash node = %d", action.Node, crashedNode)
			}
		}
	}
	if crashIndex < 0 || restartIndex-crashIndex < 2 {
		t.Fatalf("crash/restart 没有包围已有动作: %+v", sequence.Actions)
	}
	if err := mutator.validateLifecycle(sequence); err != nil {
		t.Fatalf("生命周期无效: %v", err)
	}
}

func TestRandomMutationPairRespectsExistingCrashWindow(t *testing.T) {
	mutator, _ := NewRandom(RandomConfig{
		NodeIDs: []core.NodeID{1, 2, 3}, MaxValue: 2, MaxTicks: 2, MaxActions: 10, MaxCrashed: 1,
	})
	sequence := plan.PlanSequence{Actions: []plan.PlanAction{
		{Kind: plan.ActionCrash, Node: 1},
		{Kind: plan.ActionAdvanceTicks, Ticks: 1},
		{Kind: plan.ActionRestart, Node: 1},
		{Kind: plan.ActionRequest, Node: 2, Request: "1"},
	}}
	if !mutator.insertCrashRestartPair(rand.New(rand.NewSource(3)), &sequence) {
		t.Fatal("存在合法窗口但没有生成 crash/restart 对")
	}
	if err := mutator.validateLifecycle(sequence); err != nil {
		t.Fatalf("插入后超过停止上限或生命周期冲突: %v\nactions=%+v", err, sequence.Actions)
	}
}
