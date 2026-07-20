package experiment

import (
	"context"
	"sync"
	"testing"

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/core"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/engine"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/model"
)

func TestRunnerDerivesSeedsPreservesOrderAndAggregatesCoverage(t *testing.T) {
	runner, err := New(Config{Runs: 4, BaseSeed: 40, Parallelism: 2})
	if err != nil {
		t.Fatal(err)
	}
	var mutex sync.Mutex
	seen := make(map[int64]int)
	report, err := runner.Run(context.Background(), func(_ context.Context, index int, seed int64) (engine.Result, error) {
		mutex.Lock()
		seen[seed]++
		mutex.Unlock()
		return engine.Result{
			Status:      engine.StatusCompleted,
			Actions:     core.ActionSequence{Actions: []core.Action{{Kind: core.ActionTimeout, Node: 1}}},
			ModelEvents: []model.Event{{Name: "event"}},
			ModelStates: []model.State{{Key: int64(index % 2)}},
		}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.Succeeded != 4 || report.Failed != 0 || report.TotalActions != 4 || report.UniqueModelStates != 2 {
		t.Fatalf("report = %+v", report)
	}
	for index, run := range report.Runs {
		if run.Index != index || run.Seed != int64(40+index) || seen[run.Seed] != 1 {
			t.Fatalf("run %d = %+v, seen=%v", index, run, seen)
		}
	}
}

func TestRunnerContinuesAfterIndividualFailure(t *testing.T) {
	runner, _ := New(Config{Runs: 3, BaseSeed: 1, Parallelism: 1})
	report, err := runner.Run(context.Background(), func(_ context.Context, index int, _ int64) (engine.Result, error) {
		if index == 1 {
			return engine.Result{Status: engine.StatusMappingFailed}, context.DeadlineExceeded
		}
		return engine.Result{Status: engine.StatusCompleted}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.Succeeded != 2 || report.Failed != 1 || report.StatusCounts[string(engine.StatusMappingFailed)] != 1 {
		t.Fatalf("report = %+v", report)
	}
}
