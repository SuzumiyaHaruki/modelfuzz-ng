package plan_test

import (
	"context"
	"io"
	"log"
	"testing"

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/adapters/etcdraft"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/core"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/plan"
	runtimepkg "github.com/SuzumiyaHaruki/modelfuzz-ng/internal/runtime"
	raft "go.etcd.io/raft/v3"
)

func TestPlanResolvesElectionAgainstLiveRuntime(t *testing.T) {
	config := etcdraft.DefaultConfig()
	config.NodeIDs = []core.NodeID{1, 2, 3}
	config.ElectionTick = 100
	config.Logger = &raft.DefaultLogger{Logger: log.New(io.Discard, "", 0)}
	adapter, err := etcdraft.New(config)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := runtimepkg.New(adapter, runtimepkg.Config{ExecutionID: "plan-election", Seed: 42})
	if err != nil {
		t.Fatal(err)
	}
	observation, err := runtime.Reset(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	resolver := plan.NewDefaultResolver()
	sequence := plan.PlanSequence{Actions: []plan.PlanAction{
		{Kind: plan.ActionTimeout, Node: 1},
		{
			Kind: plan.ActionDeliver,
			Messages: &plan.MessageRangeSelector{
				Link: core.LinkID{From: 1, To: 2}, Count: 1,
			},
		},
		{
			Kind: plan.ActionDeliver,
			Messages: &plan.MessageRangeSelector{
				Link: core.LinkID{From: 2, To: 1}, Count: 1,
			},
		},
	}}
	if err := sequence.Validate(); err != nil {
		t.Fatal(err)
	}

	concrete := core.ActionSequence{}
	for index, planned := range sequence.Actions {
		resolution := resolver.Resolve(planned, observation)
		if resolution.Status != plan.ResolutionResolved {
			t.Fatalf("plan action %d resolution = %+v", index, resolution)
		}
		for _, action := range resolution.Actions {
			step, err := runtime.Execute(context.Background(), action)
			if err != nil {
				t.Fatalf("execute plan action %d: %v", index, err)
			}
			concrete.Actions = append(concrete.Actions, action.Copy())
			observation = step.Observation
		}
	}

	if len(concrete.Actions) != 3 {
		t.Fatalf("concrete action count = %d, want 3", len(concrete.Actions))
	}
	if role := nodeRole(observation, 1); role != "leader" {
		t.Fatalf("node 1 role = %q, want leader", role)
	}
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
