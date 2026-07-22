package policy

import (
	"context"
	"strings"
	"testing"

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/core"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/llm"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/plan"
)

type fakeJSONClient struct {
	content  string
	options  llm.Options
	messages []llm.Message
}

func (c *fakeJSONClient) CompleteJSON(_ context.Context, messages []llm.Message, options llm.Options) (llm.Completion, error) {
	c.messages, c.options = messages, options
	return llm.Completion{Content: []byte(c.content)}, nil
}

func TestLLMPlannerValidatesAndDeduplicatesMutation(t *testing.T) {
	parent := plan.PlanSequence{Actions: []plan.PlanAction{{Kind: plan.ActionTimeout, Node: 1}}}
	client := &fakeJSONClient{content: `{"plans":[
      {"actions":[{"kind":"timeout","node":1}]},
      {"actions":[{"kind":"timeout","node":2}]},
      {"actions":[{"kind":"crash","node":1}]},
      {"actions":[{"kind":"crash","node":1},{"kind":"restart","node":1}]},
      {"actions":[{"kind":"restart","node":1}]},
      {"actions":[{"kind":"request","node":1,"request":"99"}]}
    ]}`}
	planner, err := NewLLMPlanner(client, LLMConfig{
		NodeIDs: []core.NodeID{1, 2, 3}, MaxValue: 5, MaxTicks: 3, MaxActions: 10,
		MaxLogIndex: 5, LargestTerm: 5,
	})
	if err != nil {
		t.Fatal(err)
	}
	plans, err := planner.Generate(context.Background(), GenerationRequest{
		Mode: GenerationMutation, Count: 4, Parent: parent, ParentID: "corpus-1", NewStateKeys: []int64{7},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(plans) != 3 || plans[0].Actions[0].Node != 2 ||
		plans[1].Actions[0].Kind != plan.ActionCrash ||
		plans[2].Actions[1].Kind != plan.ActionRestart || client.options.Thinking || client.options.Purpose != "mutation" {
		t.Fatalf("plans/options = %+v/%+v", plans, client.options)
	}
	if len(client.messages) != 2 || !strings.Contains(client.messages[0].Content, `"kind":"crash"`) ||
		!strings.Contains(client.messages[0].Content, "At most 1 node may be crashed simultaneously") ||
		!strings.Contains(client.messages[0].Content, "forwards the request as MsgProp") {
		t.Fatalf("system prompt does not describe crash/restart constraints: %+v", client.messages)
	}
}

func TestLLMPlannerUsesThinkingForInitialization(t *testing.T) {
	client := &fakeJSONClient{content: `{"plans":[{"actions":[{"kind":"timeout","node":1}]}]}`}
	planner, _ := NewLLMPlanner(client, LLMConfig{
		NodeIDs: []core.NodeID{1, 2}, MaxValue: 1, MaxTicks: 1, MaxActions: 2,
		MaxLogIndex: 2, LargestTerm: 2,
	})
	if _, err := planner.Generate(context.Background(), GenerationRequest{Mode: GenerationInitial, Count: 1}); err != nil {
		t.Fatal(err)
	}
	if !client.options.Thinking || client.options.Purpose != "initial" {
		t.Fatalf("initial options = %+v", client.options)
	}
}

func TestLLMPlannerNormalizesNumericRequestValue(t *testing.T) {
	client := &fakeJSONClient{content: `{"plans":[{"actions":[
      {"kind":"timeout","node":1},
      {"kind":"request","node":1,"request":3}
    ]}]}`}
	planner, err := NewLLMPlanner(client, LLMConfig{
		NodeIDs: []core.NodeID{1, 2, 3}, MaxValue: 5, MaxTicks: 1, MaxActions: 3,
		MaxLogIndex: 3, LargestTerm: 3,
	})
	if err != nil {
		t.Fatal(err)
	}
	plans, err := planner.Generate(context.Background(), GenerationRequest{Mode: GenerationInitial, Count: 1})
	if err != nil {
		t.Fatal(err)
	}
	if got := plans[0].Actions[1].Request; got != "3" {
		t.Fatalf("normalized request = %q, want 3", got)
	}
}

func TestLLMPlannerRejectsPlansOutsideModelTermAndLogBounds(t *testing.T) {
	client := &fakeJSONClient{content: `{"plans":[{"actions":[
      {"kind":"timeout","node":1},
      {"kind":"timeout","node":2},
      {"kind":"request","node":1,"request":"1"}
    ]}]}`}
	planner, err := NewLLMPlanner(client, LLMConfig{
		NodeIDs: []core.NodeID{1, 2, 3}, MaxValue: 1, MaxTicks: 1, MaxActions: 4,
		MaxLogIndex: 2, LargestTerm: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := planner.Generate(context.Background(), GenerationRequest{Mode: GenerationInitial, Count: 1}); err == nil {
		t.Fatal("out-of-bounds model plan was accepted")
	}
}

func TestLLMPlannerRejectsIncompleteInitialBatchWithReason(t *testing.T) {
	client := &fakeJSONClient{content: `{"plans":[
      {"actions":[{"kind":"timeout","node":1}]},
      {"actions":[{"kind":"request","node":1,"request":"99"}]}
    ]}`}
	planner, err := NewLLMPlanner(client, LLMConfig{
		NodeIDs: []core.NodeID{1, 2, 3}, MaxValue: 2, MaxTicks: 1, MaxActions: 3,
		MaxLogIndex: 3, LargestTerm: 3,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = planner.Generate(context.Background(), GenerationRequest{Mode: GenerationInitial, Count: 2})
	if err == nil || !strings.Contains(err.Error(), "returned 1 valid initial plans, want 2") ||
		!strings.Contains(err.Error(), `request "99" is out of range`) {
		t.Fatalf("initial batch error = %v", err)
	}
}

func TestLLMPlannerAcceptsBoundedPartitionAndHeal(t *testing.T) {
	client := &fakeJSONClient{content: `{"plans":[{"actions":[
      {"kind":"partition","partition":{"groups":[[1],[2,3]]}},
      {"kind":"advance_ticks","ticks":1},
      {"kind":"heal"}
    ]}]}`}
	planner, err := NewLLMPlanner(client, LLMConfig{
		NodeIDs: []core.NodeID{1, 2, 3}, MaxValue: 2, MaxTicks: 2, MaxActions: 4,
		MaxLogIndex: 3, LargestTerm: 3,
	})
	if err != nil {
		t.Fatal(err)
	}
	plans, err := planner.Generate(context.Background(), GenerationRequest{Mode: GenerationInitial, Count: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(plans) != 1 || plans[0].Actions[0].Kind != plan.ActionPartition ||
		plans[0].Actions[0].Partition == nil || plans[0].Actions[2].Kind != plan.ActionHeal {
		t.Fatalf("partition plan = %+v", plans)
	}
	if !strings.Contains(client.messages[0].Content, `"kind":"partition"`) {
		t.Fatalf("partition schema absent from prompt: %s", client.messages[0].Content)
	}
}
