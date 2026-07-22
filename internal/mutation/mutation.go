// Package mutation 定义 Corpus Plan 的变异接口。
package mutation

import (
	"context"

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/corpus"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/plan"
)

type Request struct {
	Entry corpus.Entry `json:"entry"`
	Count int          `json:"count"`
	Seed  int64        `json:"seed"`
}

// Mutator 可以由本地随机实现或 LLM 实现。Experiment 只依赖这个接口，
// 因而两种策略共享完全相同的反馈和保留逻辑。
type Mutator interface {
	Name() string
	Mutate(ctx context.Context, request Request) ([]plan.PlanSequence, error)
}
