package mutation

import (
	"context"
	"fmt"

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/plan"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/policy"
)

type LLM struct {
	planner *policy.LLMPlanner
}

func NewLLM(planner *policy.LLMPlanner) (*LLM, error) {
	if planner == nil {
		return nil, fmt.Errorf("LLM planner must not be nil")
	}
	return &LLM{planner: planner}, nil
}

func (m *LLM) Name() string { return "llm_mutation" }

func (m *LLM) Mutate(ctx context.Context, request Request) ([]plan.PlanSequence, error) {
	if m == nil {
		return nil, fmt.Errorf("LLM mutator is nil")
	}
	return m.planner.Generate(ctx, policy.GenerationRequest{
		Mode: policy.GenerationMutation, Count: request.Count,
		Parent: request.Entry.Plan, ParentID: request.Entry.ID,
		NewStateKeys: append([]int64(nil), request.Entry.NewStateKeys...),
	})
}
