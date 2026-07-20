// Package experiment 负责多次独立执行及跨运行汇总。
package experiment

import (
	"context"
	"fmt"
	"math"
	"sort"
	"sync"
	"time"

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/engine"
)

type Config struct {
	Runs        int   `json:"runs"`
	BaseSeed    int64 `json:"base_seed"`
	Parallelism int   `json:"parallelism"`
}

// Execute 必须为每次调用创建独立 Engine/Runtime。result 即使失败也应包含
// Engine 已经产生的部分轨迹。
type Execute func(ctx context.Context, index int, seed int64) (result engine.Result, err error)

type Run struct {
	Index           int           `json:"index"`
	Seed            int64         `json:"seed"`
	Status          engine.Status `json:"status"`
	Error           string        `json:"error,omitempty"`
	Succeeded       bool          `json:"succeeded"`
	DurationMillis  int64         `json:"duration_millis"`
	Actions         int           `json:"actions"`
	Effects         int           `json:"effects"`
	ModelEvents     int           `json:"model_events"`
	ModelStates     int           `json:"model_states"`
	OracleFindings  int           `json:"oracle_findings"`
	BudgetExhausted bool          `json:"budget_exhausted,omitempty"`
	StateKeys       []int64       `json:"state_keys,omitempty"`
}

type Report struct {
	Config            Config         `json:"config"`
	Runs              []Run          `json:"runs"`
	Succeeded         int            `json:"succeeded"`
	Failed            int            `json:"failed"`
	StatusCounts      map[string]int `json:"status_counts"`
	TotalActions      int            `json:"total_actions"`
	TotalEffects      int            `json:"total_effects"`
	TotalModelEvents  int            `json:"total_model_events"`
	UniqueModelStates int            `json:"unique_model_states"`
}

type Runner struct {
	config Config
}

func New(config Config) (*Runner, error) {
	if config.Runs <= 0 {
		return nil, fmt.Errorf("experiment runs must be positive")
	}
	if config.Parallelism <= 0 {
		return nil, fmt.Errorf("experiment parallelism must be positive")
	}
	if config.Parallelism > config.Runs {
		config.Parallelism = config.Runs
	}
	if config.BaseSeed > math.MaxInt64-int64(config.Runs-1) {
		return nil, fmt.Errorf("experiment seed range overflows int64")
	}
	return &Runner{config: config}, nil
}

func (r *Runner) Run(ctx context.Context, execute Execute) (Report, error) {
	if r == nil || execute == nil {
		return Report{}, fmt.Errorf("experiment runner and execute callback must not be nil")
	}
	report := Report{Config: r.config, Runs: make([]Run, r.config.Runs), StatusCounts: make(map[string]int)}
	jobs := make(chan int)
	var workers sync.WaitGroup
	for worker := 0; worker < r.config.Parallelism; worker++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for index := range jobs {
				report.Runs[index] = executeRun(ctx, execute, index, r.config.BaseSeed+int64(index))
			}
		}()
	}
	for index := 0; index < r.config.Runs; index++ {
		select {
		case jobs <- index:
		case <-ctx.Done():
			close(jobs)
			workers.Wait()
			return aggregate(report), ctx.Err()
		}
	}
	close(jobs)
	workers.Wait()
	return aggregate(report), nil
}

func executeRun(ctx context.Context, execute Execute, index int, seed int64) Run {
	started := time.Now()
	result, err := execute(ctx, index, seed)
	run := Run{
		Index: index, Seed: seed, Status: result.Status,
		DurationMillis: time.Since(started).Milliseconds(),
		Actions:        len(result.Actions.Actions), Effects: countEffects(result),
		ModelEvents: len(result.ModelEvents), ModelStates: len(result.ModelStates),
		OracleFindings: len(result.OracleFindings), BudgetExhausted: result.BudgetExhausted,
	}
	if err != nil {
		run.Error = err.Error()
	} else {
		run.Succeeded = result.Status == engine.StatusCompleted
	}
	stateKeys := make(map[int64]struct{}, len(result.ModelStates))
	for _, state := range result.ModelStates {
		stateKeys[state.Key] = struct{}{}
	}
	for key := range stateKeys {
		run.StateKeys = append(run.StateKeys, key)
	}
	sort.Slice(run.StateKeys, func(i, j int) bool { return run.StateKeys[i] < run.StateKeys[j] })
	return run
}

func aggregate(report Report) Report {
	states := make(map[int64]struct{})
	for _, run := range report.Runs {
		if run.Succeeded {
			report.Succeeded++
		} else {
			report.Failed++
		}
		report.StatusCounts[string(run.Status)]++
		report.TotalActions += run.Actions
		report.TotalEffects += run.Effects
		report.TotalModelEvents += run.ModelEvents
		for _, key := range run.StateKeys {
			states[key] = struct{}{}
		}
	}
	report.UniqueModelStates = len(states)
	return report
}

func countEffects(result engine.Result) int {
	total := 0
	for _, step := range result.Trace.Steps {
		total += len(step.Effects)
	}
	return total
}
