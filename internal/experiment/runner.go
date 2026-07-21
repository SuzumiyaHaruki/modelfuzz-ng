// Package experiment 负责多次执行、模型覆盖反馈、Corpus 保留和候选调度。
package experiment

import (
	"context"
	"fmt"
	"math"
	"sort"
	"sync"
	"time"

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/corpus"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/engine"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/metrics"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/mutation"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/plan"
)

type Config struct {
	Runs                   int   `json:"runs"`
	BaseSeed               int64 `json:"base_seed"`
	Parallelism            int   `json:"parallelism"`
	InitialPopulation      int   `json:"initial_population"`
	MutationsPerNewState   int   `json:"mutations_per_new_state"`
	MaxMutationsPerCorpus  int   `json:"max_mutations_per_corpus_entry"`
	RandomSeedInterval     int   `json:"random_seed_interval,omitempty"`
	RandomSeedsPerInterval int   `json:"random_seeds_per_interval,omitempty"`
}

// Execute 必须为每次调用创建独立 Engine/Runtime。result 即使失败也应包含
// Engine 已经产生的部分轨迹。
type Execute func(ctx context.Context, index int, seed int64) (result engine.Result, err error)

// Candidate 是反馈队列中的一个执行任务。Plan 为 nil 表示使用在线随机策略生成
// 种子；非 nil 表示执行 LLM 初始化 Plan 或 Corpus 变异 Plan。
type CandidateKind string

const (
	CandidateInitial        CandidateKind = "initial"
	CandidateMutation       CandidateKind = "mutation"
	CandidatePeriodicRandom CandidateKind = "periodic_random"
)

type Candidate struct {
	ID       string             `json:"id"`
	ParentID string             `json:"parent_id,omitempty"`
	Kind     CandidateKind      `json:"kind"`
	Source   string             `json:"source"`
	Depth    int                `json:"depth"`
	Plan     *plan.PlanSequence `json:"plan,omitempty"`
}

type FeedbackExecution struct {
	Result engine.Result
	Plan   plan.PlanSequence
}

type FeedbackExecute func(ctx context.Context, index int, seed int64, candidate Candidate) (FeedbackExecution, error)

// Initializer 为 nil 时，Runner 创建在线随机种子；非 nil 时由 LLM 等外部
// Plan 生成器返回静态种子。
type Initializer func(ctx context.Context, count int, seed int64) ([]plan.PlanSequence, error)

type FeedbackOptions struct {
	InitializerName string
	Initializer     Initializer
	Mutator         mutation.Mutator
	// Resume 恢复一个此前由同一 Config 产生的反馈实验。
	Resume *Checkpoint
	Hooks  Hooks
	// CheckpointEvery 表示每完成多少次执行写一次检查点；零等价于1。
	CheckpointEvery int
	// ConfigurationFingerprint 由 CLI 对 SUT、Engine、Policy 和 Mutator 配置
	// 计算；恢复时必须一致，避免只匹配 Runs/Seed 却换了实验语义。
	ConfigurationFingerprint string
}

type Run struct {
	// omitempty 让尚未分配的固定槽位编码为 {}。这样 Runs 仍可按 index 直接
	// 恢复，但大规模实验早期的 checkpoint 不会为每个未来运行写一套零值字段。
	Completed            bool                     `json:"completed,omitempty"`
	Index                int                      `json:"index"`
	Seed                 int64                    `json:"seed,omitempty"`
	Status               engine.Status            `json:"status,omitempty"`
	Error                string                   `json:"error,omitempty"`
	Succeeded            bool                     `json:"succeeded,omitempty"`
	DurationMillis       int64                    `json:"duration_millis,omitempty"`
	DurationMicros       int64                    `json:"duration_micros,omitempty"`
	Actions              int                      `json:"actions,omitempty"`
	Effects              int                      `json:"effects,omitempty"`
	ModelEvents          int                      `json:"model_events,omitempty"`
	ModelStates          int                      `json:"model_states,omitempty"`
	OracleFindings       int                      `json:"oracle_findings,omitempty"`
	BudgetExhausted      bool                     `json:"budget_exhausted,omitempty"`
	StateKeys            []int64                  `json:"state_keys,omitempty"`
	CandidateID          string                   `json:"candidate_id,omitempty"`
	CandidateKind        CandidateKind            `json:"candidate_kind,omitempty"`
	ParentID             string                   `json:"parent_id,omitempty"`
	Source               string                   `json:"source,omitempty"`
	Depth                int                      `json:"depth,omitempty"`
	Retained             bool                     `json:"retained,omitempty"`
	CorpusID             string                   `json:"corpus_id,omitempty"`
	NewStateKeys         []int64                  `json:"new_state_keys,omitempty"`
	Termination          engine.TerminationReason `json:"termination,omitempty"`
	TerminationCode      string                   `json:"termination_code,omitempty"`
	TerminationDetail    string                   `json:"termination_detail,omitempty"`
	Metrics              *metrics.RunMetrics      `json:"metrics,omitempty"`
	PlanDigest           string                   `json:"plan_digest,omitempty"`
	TraceDigest          string                   `json:"trace_digest,omitempty"`
	ModelStatePathDigest string                   `json:"model_state_path_digest,omitempty"`
	NewPlan              bool                     `json:"new_plan,omitempty"`
	NewTrace             bool                     `json:"new_trace,omitempty"`
	NewModelStatePath    bool                     `json:"new_model_state_path,omitempty"`
}

// SourceNovelty 区分随机初始化、周期性随机注入、本地变异以及未来 LLM 来源
// 各自贡献的输入和执行多样性。Unique*Discovered 按实际完成顺序归属首次发现者。
type SourceNovelty struct {
	Executions                 int `json:"executions"`
	PlansObserved              int `json:"plans_observed"`
	TracesObserved             int `json:"traces_observed"`
	ModelStatePathsObserved    int `json:"model_state_paths_observed"`
	UniquePlansDiscovered      int `json:"unique_plans_discovered"`
	UniqueTracesDiscovered     int `json:"unique_traces_discovered"`
	UniqueStatePathsDiscovered int `json:"unique_model_state_paths_discovered"`
	NewModelStates             int `json:"new_model_states"`
}

// CoveragePoint 记录覆盖随实际完成顺序增长的曲线。并发运行时 CompletedRuns
// 与 Run.Index 不一定相同，因此不从最终 Runs 数组事后推测。
type CoveragePoint struct {
	CompletedRuns         int   `json:"completed_runs"`
	TotalActions          int   `json:"total_actions"`
	UniqueModelStates     int   `json:"unique_model_states"`
	UniquePlans           int   `json:"unique_plans"`
	UniqueTraces          int   `json:"unique_traces"`
	UniqueModelStatePaths int   `json:"unique_model_state_paths"`
	CorpusEntries         int   `json:"corpus_entries"`
	ElapsedMillis         int64 `json:"elapsed_millis"`
}

type Report struct {
	Config                       Config                    `json:"config"`
	Runs                         []Run                     `json:"runs"`
	CompletedRuns                int                       `json:"completed_runs"`
	Succeeded                    int                       `json:"succeeded"`
	Failed                       int                       `json:"failed"`
	StatusCounts                 map[string]int            `json:"status_counts"`
	TotalActions                 int                       `json:"total_actions"`
	TotalEffects                 int                       `json:"total_effects"`
	TotalModelEvents             int                       `json:"total_model_events"`
	UniqueModelStates            int                       `json:"unique_model_states"`
	Feedback                     bool                      `json:"feedback"`
	CorpusEntries                int                       `json:"corpus_entries"`
	RetainedRuns                 int                       `json:"retained_runs"`
	InitialExecutions            int                       `json:"initial_executions"`
	GeneratedMutations           int                       `json:"generated_mutations"`
	ExecutedMutations            int                       `json:"executed_mutations"`
	MutationErrors               []string                  `json:"mutation_errors,omitempty"`
	ActionCounts                 map[string]int            `json:"action_counts"`
	EffectCounts                 map[string]int            `json:"effect_counts"`
	MessageTypeCounts            map[string]int            `json:"outbound_message_type_counts"`
	ResolutionCounts             map[string]int            `json:"resolution_counts"`
	DecisionCounts               map[string]int            `json:"decision_counts"`
	DecisionCountsBySource       map[string]map[string]int `json:"decision_counts_by_source"`
	ModelEventCounts             map[string]int            `json:"model_event_counts"`
	OracleCounts                 map[string]int            `json:"oracle_counts"`
	TimerFireCounts              map[string]int            `json:"timer_fire_counts"`
	FailureCounts                map[string]int            `json:"failure_counts"`
	TerminationCounts            map[string]int            `json:"termination_counts"`
	Duration                     metrics.DurationSummary   `json:"duration"`
	ActionsPerSecond             float64                   `json:"actions_per_second"`
	RunsPerSecond                float64                   `json:"runs_per_second"`
	MaxCorpusDepth               int                       `json:"max_corpus_depth"`
	MaxQueuedMessages            int                       `json:"max_queued_messages"`
	CoverageTimeline             []CoveragePoint           `json:"coverage_timeline"`
	ElapsedMillis                int64                     `json:"elapsed_millis"`
	PlansObserved                int                       `json:"plans_observed"`
	TracesObserved               int                       `json:"traces_observed"`
	ModelStatePathsObserved      int                       `json:"model_state_paths_observed"`
	UniquePlans                  int                       `json:"unique_plans"`
	UniqueTraces                 int                       `json:"unique_traces"`
	UniqueModelStatePaths        int                       `json:"unique_model_state_paths"`
	DuplicatePlanRatio           float64                   `json:"duplicate_plan_ratio"`
	DuplicateTraceRatio          float64                   `json:"duplicate_trace_ratio"`
	DuplicateModelStatePathRatio float64                   `json:"duplicate_model_state_path_ratio"`
	NoveltyBySource              map[string]SourceNovelty  `json:"novelty_by_source"`
	PeriodicSeedExecutions       int                       `json:"periodic_seed_executions"`
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
	if config.InitialPopulation == 0 {
		config.InitialPopulation = min(4, config.Runs)
	}
	if config.MutationsPerNewState == 0 {
		config.MutationsPerNewState = 2
	}
	if config.MaxMutationsPerCorpus == 0 {
		config.MaxMutationsPerCorpus = 8
	}
	if config.InitialPopulation < 0 || config.MutationsPerNewState < 0 || config.MaxMutationsPerCorpus < 0 {
		return nil, fmt.Errorf("feedback experiment bounds must be non-negative")
	}
	if config.RandomSeedInterval < 0 || config.RandomSeedsPerInterval < 0 {
		return nil, fmt.Errorf("periodic random seed settings must be non-negative")
	}
	if config.RandomSeedInterval > 0 && config.RandomSeedsPerInterval == 0 {
		return nil, fmt.Errorf("positive random seed interval requires seeds per interval")
	}
	if config.InitialPopulation > config.Runs {
		config.InitialPopulation = config.Runs
	}
	return &Runner{config: config}, nil
}

func (r *Runner) Config() Config {
	if r == nil {
		return Config{}
	}
	return r.config
}

func (r *Runner) Run(ctx context.Context, execute Execute) (Report, error) {
	if r == nil || execute == nil {
		return Report{}, fmt.Errorf("experiment runner and execute callback must not be nil")
	}
	report := newReport(r.config, false)
	started := time.Now()
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
			report.ElapsedMillis = time.Since(started).Milliseconds()
			return aggregate(report), ctx.Err()
		}
	}
	close(jobs)
	workers.Wait()
	report.ElapsedMillis = time.Since(started).Milliseconds()
	return aggregate(report), nil
}

type executionDone struct {
	index     int
	seed      int64
	candidate Candidate
	execution FeedbackExecution
	err       error
	duration  time.Duration
}

type mutationDone struct {
	entry corpus.Entry
	plans []plan.PlanSequence
	err   error
}

// RunFeedback 执行“种子 -> 模型状态覆盖 -> Corpus -> Mutation -> 新候选”的闭环。
// Mutation 在独立 goroutine 中运行，因此 LLM 等待可以与后续候选执行重叠。
func (r *Runner) RunFeedback(ctx context.Context, options FeedbackOptions, execute FeedbackExecute) (Report, corpus.Snapshot, error) {
	if r == nil || execute == nil {
		return Report{}, corpus.Snapshot{}, fmt.Errorf("experiment runner and feedback execute callback must not be nil")
	}
	if options.Mutator == nil {
		return Report{}, corpus.Snapshot{}, fmt.Errorf("feedback mutator must not be nil")
	}
	if options.InitializerName == "" {
		if options.Initializer == nil {
			options.InitializerName = "random_init"
		} else {
			options.InitializerName = "external_init"
		}
	}
	if options.CheckpointEvery <= 0 {
		options.CheckpointEvery = 1
	}
	report := newReport(r.config, true)
	collection := corpus.New()
	ready := make([]Candidate, 0)
	rerun := make([]ScheduledCandidate, 0)
	pending := make(map[string]mutation.Request)
	nextCandidateID, nextRunIndex, completed := 0, 0, 0
	nextRandomSeedAt, randomSeedsDue := 0, 0
	if r.config.RandomSeedInterval > 0 {
		nextRandomSeedAt = r.config.RandomSeedInterval
	}
	eventSequence := uint64(0)
	elapsedOffset := time.Duration(0)
	started := time.Now()

	if options.Resume != nil {
		if err := options.Resume.Validate(r.config, options.ConfigurationFingerprint); err != nil {
			return report, collection.Snapshot(), err
		}
		var err error
		collection, err = corpus.Restore(options.Resume.Corpus)
		if err != nil {
			return report, corpus.Snapshot{}, err
		}
		report = options.Resume.Report
		ready = copyCandidates(options.Resume.Ready)
		rerun = append(rerun, options.Resume.InFlight...)
		for _, request := range options.Resume.PendingMutations {
			pending[request.Entry.ID] = request
		}
		nextCandidateID, nextRunIndex, completed = options.Resume.NextCandidateID, options.Resume.NextRunIndex, options.Resume.Completed
		nextRandomSeedAt, randomSeedsDue = options.Resume.NextRandomSeedAt, options.Resume.RandomSeedsDue
		eventSequence = options.Resume.EventSequence
		elapsedOffset = time.Duration(options.Resume.ElapsedMillis) * time.Millisecond
	} else {
		var err error
		ready, nextCandidateID, err = r.initialCandidates(ctx, options, r.config.InitialPopulation, r.config.BaseSeed, 0)
		if err != nil {
			return aggregate(report), collection.Snapshot(), err
		}
	}
	novelty := newNoveltyTracker(report)
	runContext, cancel := context.WithCancel(ctx)
	defer cancel()

	mutationRequests := make(chan mutation.Request, r.config.Runs)
	mutationResults := make(chan mutationDone, r.config.Runs)
	var mutationWorker sync.WaitGroup
	mutationWorker.Add(1)
	go func() {
		defer mutationWorker.Done()
		for {
			select {
			case <-runContext.Done():
				return
			case request := <-mutationRequests:
				plans, err := options.Mutator.Mutate(runContext, request)
				select {
				case mutationResults <- mutationDone{entry: request.Entry, plans: plans, err: err}:
				case <-runContext.Done():
					return
				}
			}
		}
	}()

	executionResults := make(chan executionDone, r.config.Runs)
	var executionWorkers sync.WaitGroup
	inFlight := make(map[int]ScheduledCandidate)
	running := 0

	emit := func(event Event) error {
		if options.Hooks.OnEvent == nil {
			return nil
		}
		nextSequence := eventSequence + 1
		event.Sequence = nextSequence
		event.At = time.Now().UTC()
		event.CompletedRuns = completed
		event.CorpusEntries = collection.Len()
		if err := options.Hooks.OnEvent(event); err != nil {
			return err
		}
		eventSequence = nextSequence
		return nil
	}
	checkpoint := func(force bool) error {
		if options.Hooks.OnCheckpoint == nil || (!force && completed%options.CheckpointEvery != 0) {
			return nil
		}
		elapsedMillis := (elapsedOffset + time.Since(started)).Milliseconds()
		checkpointReport := finalizeFeedbackReport(report, collection, elapsedMillis)
		state := Checkpoint{
			Version: CheckpointVersion, SavedAt: time.Now().UTC(), Config: r.config, Report: checkpointReport,
			Corpus: collection.Snapshot(), Ready: copyCandidates(ready), InFlight: mergeScheduled(rerun, scheduledValues(inFlight)),
			PendingMutations: pendingValues(pending), NextCandidateID: nextCandidateID,
			NextRunIndex: nextRunIndex, Completed: completed, EventSequence: eventSequence,
			NextRandomSeedAt: nextRandomSeedAt, RandomSeedsDue: randomSeedsDue,
			ElapsedMillis:            elapsedMillis,
			ConfigurationFingerprint: options.ConfigurationFingerprint,
		}
		return options.Hooks.OnCheckpoint(state)
	}
	stopWithError := func(cause error) (Report, corpus.Snapshot, error) {
		cancel()
		executionWorkers.Wait()
		mutationWorker.Wait()
		_ = emit(Event{Kind: EventExperimentCanceled, Error: cause.Error()})
		_ = checkpoint(true)
		report = finalizeFeedbackReport(report, collection, (elapsedOffset + time.Since(started)).Milliseconds())
		return report, collection.Snapshot(), cause
	}
	if options.Resume != nil {
		if err := emit(Event{Kind: EventExperimentResumed}); err != nil {
			return stopWithError(err)
		}
	} else if err := emit(Event{Kind: EventExperimentStarted}); err != nil {
		return stopWithError(err)
	}
	if err := checkpoint(true); err != nil {
		return stopWithError(err)
	}
	// 未完成的 Mutation 在恢复后重新提交。它们是由 seed 确定的纯变异请求。
	for _, request := range pendingValues(pending) {
		mutationRequests <- request
	}

	launch := func(task ScheduledCandidate) {
		inFlight[task.Index] = task
		running++
		executionWorkers.Add(1)
		go func() {
			defer executionWorkers.Done()
			started := time.Now()
			execution, executeErr := execute(runContext, task.Index, task.Seed, copyCandidate(task.Candidate))
			done := executionDone{index: task.Index, seed: task.Seed, candidate: task.Candidate, execution: execution, err: executeErr, duration: time.Since(started)}
			select {
			case executionResults <- done:
			case <-runContext.Done():
			}
		}()
	}

	for completed < r.config.Runs {
		for running < r.config.Parallelism && len(rerun) > 0 {
			task := rerun[0]
			rerun = rerun[1:]
			launch(task)
		}
		for nextRunIndex < r.config.Runs && running < r.config.Parallelism && (randomSeedsDue > 0 || len(ready) > 0) {
			var candidate Candidate
			if randomSeedsDue > 0 {
				candidate = Candidate{
					ID: fmt.Sprintf("candidate-%06d", nextCandidateID), Kind: CandidatePeriodicRandom,
					Source: string(CandidatePeriodicRandom),
				}
				nextCandidateID++
				randomSeedsDue--
			} else {
				candidate = ready[0]
				ready = ready[1:]
			}
			task := ScheduledCandidate{Index: nextRunIndex, Seed: r.config.BaseSeed + int64(nextRunIndex), Candidate: candidate}
			nextRunIndex++
			launch(task)
		}

		if completed == r.config.Runs {
			break
		}
		// 队列和变异工作都已耗尽时才补充新种子，避免种子抢完运行预算，
		// 同时给刚产生的新覆盖留下执行变异后代的机会。
		if running == 0 && len(ready) == 0 && len(pending) == 0 && nextRunIndex < r.config.Runs {
			seeds, nextID, seedErr := r.initialCandidates(runContext, options, 1, r.config.BaseSeed+int64(nextRunIndex), nextCandidateID)
			if seedErr != nil {
				return stopWithError(seedErr)
			}
			ready = append(ready, seeds...)
			nextCandidateID = nextID
			continue
		}

		select {
		case <-ctx.Done():
			return stopWithError(ctx.Err())
		case done := <-executionResults:
			running--
			delete(inFlight, done.index)
			completed++
			// 阈值按实际完成数推进，而不是按 run index 推进。新种子获得下一批
			// 空闲槽位，但不会清空或覆盖 ready 中已经生成的变异候选。
			for nextRandomSeedAt > 0 && completed >= nextRandomSeedAt {
				randomSeedsDue += r.config.RandomSeedsPerInterval
				nextRandomSeedAt += r.config.RandomSeedInterval
			}
			run := feedbackRun(done)
			novelty.classify(&run)
			switch done.candidate.Kind {
			case CandidateMutation:
				report.ExecutedMutations++
			case CandidateInitial:
				report.InitialExecutions++
			}
			if run.Succeeded && len(done.execution.Result.ModelStates) > 0 {
				entry, retained, corpusErr := collection.Consider(corpus.Input{
					ParentID: done.candidate.ParentID, Source: done.candidate.Source,
					Depth: done.candidate.Depth, RunIndex: done.index, Seed: done.seed,
					Plan: done.execution.Plan, Actions: done.execution.Result.Actions,
					States: done.execution.Result.ModelStates,
				})
				if corpusErr != nil {
					run.Error = joinErrorText(run.Error, corpusErr.Error())
					run.Succeeded = false
				} else if retained {
					run.Retained, run.CorpusID = true, entry.ID
					run.NewStateKeys = append([]int64(nil), entry.NewStateKeys...)
					count := mutationCount(len(entry.NewStateKeys), r.config.MutationsPerNewState, r.config.MaxMutationsPerCorpus)
					// 已经发出的执行占满总预算时不再启动没有机会被执行的变异，
					// 对 LLM mutator 尤其可以避免一次无意义的远程调用。
					if count > 0 && nextRunIndex < r.config.Runs {
						request := mutation.Request{Entry: entry, Count: count, Seed: mutationSeed(r.config.BaseSeed, entry.RunIndex)}
						pending[entry.ID] = request
						mutationRequests <- request
					}
				}
			}
			report.Runs[done.index] = run
			report.CoverageTimeline = append(report.CoverageTimeline, coveragePoint(report, collection, completed,
				(elapsedOffset+time.Since(started)).Milliseconds()))
			completion := Completion{Run: run, Candidate: copyCandidate(done.candidate), Execution: done.execution}
			if options.Hooks.OnRunComplete != nil {
				if err := options.Hooks.OnRunComplete(completion); err != nil {
					return stopWithError(err)
				}
			}
			if err := emit(Event{Kind: EventRunCompleted, Run: &run, Candidate: &done.candidate, CorpusID: run.CorpusID}); err != nil {
				return stopWithError(err)
			}
			if err := checkpoint(false); err != nil {
				return stopWithError(err)
			}
		case mutated := <-mutationResults:
			delete(pending, mutated.entry.ID)
			if mutated.err != nil {
				report.MutationErrors = append(report.MutationErrors, fmt.Sprintf("%s: %v", mutated.entry.ID, mutated.err))
				if err := emit(Event{Kind: EventMutationCompleted, CorpusID: mutated.entry.ID, Error: mutated.err.Error()}); err != nil {
					return stopWithError(err)
				}
				continue
			}
			report.GeneratedMutations += len(mutated.plans)
			for _, sequence := range mutated.plans {
				if nextRunIndex+len(ready) >= r.config.Runs {
					break
				}
				copy := sequence.Copy()
				ready = append(ready, Candidate{
					ID: fmt.Sprintf("candidate-%06d", nextCandidateID), ParentID: mutated.entry.ID,
					Kind: CandidateMutation, Source: options.Mutator.Name(), Depth: mutated.entry.Depth + 1, Plan: &copy,
				})
				nextCandidateID++
			}
			if err := emit(Event{Kind: EventMutationCompleted, CorpusID: mutated.entry.ID, MutationCount: len(mutated.plans)}); err != nil {
				return stopWithError(err)
			}
		}
	}
	cancel()
	executionWorkers.Wait()
	mutationWorker.Wait()
	ready = make([]Candidate, 0)
	randomSeedsDue = 0
	for key := range pending {
		delete(pending, key)
	}
	if err := emit(Event{Kind: EventExperimentCompleted}); err != nil {
		report = finalizeFeedbackReport(report, collection, (elapsedOffset + time.Since(started)).Milliseconds())
		return report, collection.Snapshot(), err
	}
	if err := checkpoint(true); err != nil {
		report = finalizeFeedbackReport(report, collection, (elapsedOffset + time.Since(started)).Milliseconds())
		return report, collection.Snapshot(), err
	}
	report = finalizeFeedbackReport(report, collection, (elapsedOffset + time.Since(started)).Milliseconds())
	return report, collection.Snapshot(), nil
}

func finalizeFeedbackReport(report Report, collection *corpus.Corpus, elapsedMillis int64) Report {
	report.ElapsedMillis = elapsedMillis
	report = aggregate(report)
	report.CorpusEntries = collection.Len()
	report.RetainedRuns = report.CorpusEntries
	return report
}

func (r *Runner) initialCandidates(ctx context.Context, options FeedbackOptions, count int, seed int64, nextID int) ([]Candidate, int, error) {
	if count <= 0 {
		return nil, nextID, fmt.Errorf("initial candidate count must be positive")
	}
	sequences := make([]plan.PlanSequence, count)
	if options.Initializer != nil {
		generated, err := options.Initializer(ctx, count, seed)
		if err != nil {
			return nil, nextID, fmt.Errorf("%s failed: %w", options.InitializerName, err)
		}
		if len(generated) == 0 {
			return nil, nextID, fmt.Errorf("%s returned no plans", options.InitializerName)
		}
		if len(generated) < count {
			count = len(generated)
		}
		sequences = generated[:count]
	}
	result := make([]Candidate, 0, count)
	for index := 0; index < count; index++ {
		candidate := Candidate{ID: fmt.Sprintf("candidate-%06d", nextID), Kind: CandidateInitial, Source: options.InitializerName}
		if options.Initializer != nil {
			if len(sequences[index].Actions) == 0 {
				return nil, nextID, fmt.Errorf("%s returned empty plan %d", options.InitializerName, index)
			}
			if err := sequences[index].Validate(); err != nil {
				return nil, nextID, fmt.Errorf("%s returned invalid plan %d: %w", options.InitializerName, index, err)
			}
			copy := sequences[index].Copy()
			candidate.Plan = &copy
		}
		result = append(result, candidate)
		nextID++
	}
	return result, nextID, nil
}

func feedbackRun(done executionDone) Run {
	run := summarizeResult(done.index, done.seed, done.execution.Result, done.err, done.duration)
	run.CandidateID, run.CandidateKind = done.candidate.ID, done.candidate.Kind
	run.ParentID, run.Source, run.Depth = done.candidate.ParentID, done.candidate.Source, done.candidate.Depth
	run.PlanDigest = digestPlan(done.execution.Plan)
	run.TraceDigest = digestTrace(done.execution.Result.Trace)
	run.ModelStatePathDigest = digestStatePath(done.execution.Result.ModelStates)
	return run
}

func copyCandidate(candidate Candidate) Candidate {
	if candidate.Plan != nil {
		copy := candidate.Plan.Copy()
		candidate.Plan = &copy
	}
	return candidate
}

func mutationSeed(base int64, runIndex int) int64 {
	return base ^ (int64(runIndex+1) << 32) ^ 0x4d5554415445
}

func mutationCount(newStates, perState, maximum int) int {
	if newStates <= 0 || perState <= 0 || maximum <= 0 {
		return 0
	}
	if newStates > maximum/perState {
		return maximum
	}
	return min(newStates*perState, maximum)
}

func joinErrorText(existing, added string) string {
	if existing == "" {
		return added
	}
	return existing + "; " + added
}

func executeRun(ctx context.Context, execute Execute, index int, seed int64) Run {
	started := time.Now()
	result, err := execute(ctx, index, seed)
	return summarizeResult(index, seed, result, err, time.Since(started))
}

func summarizeResult(index int, seed int64, result engine.Result, err error, duration time.Duration) Run {
	run := Run{
		Completed: true, Index: index, Seed: seed, Status: result.Status,
		DurationMillis: duration.Milliseconds(),
		DurationMicros: duration.Microseconds(),
		Actions:        len(result.Actions.Actions), Effects: countEffects(result),
		ModelEvents: len(result.ModelEvents), ModelStates: len(result.ModelStates),
		OracleFindings: len(result.OracleFindings), BudgetExhausted: result.BudgetExhausted,
		Termination: result.Termination, TerminationCode: result.TerminationCode,
		TerminationDetail: result.TerminationDetail,
		Metrics:           pointerTo(metrics.Collect(result)),
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
	report.Succeeded, report.Failed, report.CompletedRuns = 0, 0, 0
	report.TotalActions, report.TotalEffects, report.TotalModelEvents, report.UniqueModelStates = 0, 0, 0, 0
	report.StatusCounts = make(map[string]int)
	report.ActionCounts = make(map[string]int)
	report.EffectCounts = make(map[string]int)
	report.MessageTypeCounts = make(map[string]int)
	report.ResolutionCounts = make(map[string]int)
	report.DecisionCounts = make(map[string]int)
	report.DecisionCountsBySource = make(map[string]map[string]int)
	report.ModelEventCounts = make(map[string]int)
	report.OracleCounts = make(map[string]int)
	report.TimerFireCounts = make(map[string]int)
	report.FailureCounts = make(map[string]int)
	report.TerminationCounts = make(map[string]int)
	report.MaxCorpusDepth, report.MaxQueuedMessages = 0, 0
	report.PlansObserved, report.TracesObserved, report.ModelStatePathsObserved = 0, 0, 0
	report.PeriodicSeedExecutions = 0
	report.NoveltyBySource = make(map[string]SourceNovelty)
	states := make(map[int64]struct{})
	plans := make(map[string]struct{})
	traces := make(map[string]struct{})
	statePaths := make(map[string]struct{})
	durations := make([]int64, 0, len(report.Runs))
	for _, run := range report.Runs {
		if !run.Completed {
			continue
		}
		report.CompletedRuns++
		var source SourceNovelty
		if run.Source != "" {
			source = report.NoveltyBySource[run.Source]
			source.Executions++
		}
		if run.PlanDigest != "" {
			report.PlansObserved++
			if run.Source != "" {
				source.PlansObserved++
			}
			plans[run.PlanDigest] = struct{}{}
		}
		if run.TraceDigest != "" {
			report.TracesObserved++
			if run.Source != "" {
				source.TracesObserved++
			}
			traces[run.TraceDigest] = struct{}{}
		}
		if run.ModelStatePathDigest != "" {
			report.ModelStatePathsObserved++
			if run.Source != "" {
				source.ModelStatePathsObserved++
			}
			statePaths[run.ModelStatePathDigest] = struct{}{}
		}
		if run.NewPlan && run.Source != "" {
			source.UniquePlansDiscovered++
		}
		if run.NewTrace && run.Source != "" {
			source.UniqueTracesDiscovered++
		}
		if run.NewModelStatePath && run.Source != "" {
			source.UniqueStatePathsDiscovered++
		}
		if run.Source != "" {
			source.NewModelStates += len(run.NewStateKeys)
			report.NoveltyBySource[run.Source] = source
		}
		if run.CandidateKind == CandidatePeriodicRandom {
			report.PeriodicSeedExecutions++
		}
		if run.Succeeded {
			report.Succeeded++
		} else {
			report.Failed++
		}
		report.StatusCounts[string(run.Status)]++
		report.TotalActions += run.Actions
		report.TotalEffects += run.Effects
		report.TotalModelEvents += run.ModelEvents
		durations = append(durations, run.DurationMicros)
		if run.Metrics != nil {
			mergeCounts(report.ActionCounts, run.Metrics.ActionCounts)
			mergeCounts(report.EffectCounts, run.Metrics.EffectCounts)
			mergeCounts(report.MessageTypeCounts, run.Metrics.MessageTypeCounts)
			mergeCounts(report.ResolutionCounts, run.Metrics.ResolutionCounts)
			mergeCounts(report.DecisionCounts, run.Metrics.DecisionCounts)
			if run.Source != "" && len(run.Metrics.DecisionCounts) > 0 {
				if report.DecisionCountsBySource[run.Source] == nil {
					report.DecisionCountsBySource[run.Source] = make(map[string]int)
				}
				mergeCounts(report.DecisionCountsBySource[run.Source], run.Metrics.DecisionCounts)
			}
			mergeCounts(report.ModelEventCounts, run.Metrics.ModelEventCounts)
			mergeCounts(report.OracleCounts, run.Metrics.OracleCounts)
			mergeCounts(report.TimerFireCounts, run.Metrics.TimerFireCounts)
			mergeCounts(report.FailureCounts, run.Metrics.FailureCounts)
		}
		if run.Termination != "" {
			report.TerminationCounts[string(run.Termination)]++
		}
		if run.Depth > report.MaxCorpusDepth {
			report.MaxCorpusDepth = run.Depth
		}
		if run.Metrics != nil && run.Metrics.EstimatedPeakQueuedMessages > report.MaxQueuedMessages {
			report.MaxQueuedMessages = run.Metrics.EstimatedPeakQueuedMessages
		}
		for _, key := range run.StateKeys {
			states[key] = struct{}{}
		}
	}
	report.UniqueModelStates = len(states)
	report.UniquePlans, report.UniqueTraces, report.UniqueModelStatePaths = len(plans), len(traces), len(statePaths)
	report.DuplicatePlanRatio = duplicateRatio(report.PlansObserved, report.UniquePlans)
	report.DuplicateTraceRatio = duplicateRatio(report.TracesObserved, report.UniqueTraces)
	report.DuplicateModelStatePathRatio = duplicateRatio(report.ModelStatePathsObserved, report.UniqueModelStatePaths)
	report.Duration = metrics.Durations(durations)
	var durationMicros int64
	for _, value := range durations {
		durationMicros += value
	}
	if report.ElapsedMillis > 0 {
		seconds := float64(report.ElapsedMillis) / float64(time.Second/time.Millisecond)
		report.ActionsPerSecond = float64(report.TotalActions) / seconds
		report.RunsPerSecond = float64(report.CompletedRuns) / seconds
	} else if durationMicros > 0 {
		seconds := float64(durationMicros) / float64(time.Second/time.Microsecond)
		report.ActionsPerSecond = float64(report.TotalActions) / seconds
		report.RunsPerSecond = float64(report.CompletedRuns) / seconds
	} else {
		report.ActionsPerSecond, report.RunsPerSecond = 0, 0
	}
	return report
}

func newReport(config Config, feedback bool) Report {
	return Report{
		Config: config, Runs: make([]Run, config.Runs), StatusCounts: make(map[string]int), Feedback: feedback,
		MutationErrors: make([]string, 0), ActionCounts: make(map[string]int), EffectCounts: make(map[string]int),
		MessageTypeCounts: make(map[string]int),
		ResolutionCounts:  make(map[string]int), DecisionCounts: make(map[string]int),
		DecisionCountsBySource: make(map[string]map[string]int),
		ModelEventCounts:       make(map[string]int), OracleCounts: make(map[string]int),
		TimerFireCounts: make(map[string]int), FailureCounts: make(map[string]int), TerminationCounts: make(map[string]int),
		CoverageTimeline: make([]CoveragePoint, 0, config.Runs),
		NoveltyBySource:  make(map[string]SourceNovelty),
	}
}

func duplicateRatio(observed, unique int) float64 {
	if observed == 0 {
		return 0
	}
	return float64(observed-unique) / float64(observed)
}

func mergeCounts(destination, source map[string]int) {
	for key, value := range source {
		destination[key] += value
	}
}

func pointerTo[T any](value T) *T {
	return &value
}

func coveragePoint(report Report, collection *corpus.Corpus, completed int, elapsedMillis int64) CoveragePoint {
	states := make(map[int64]struct{})
	plans := make(map[string]struct{})
	traces := make(map[string]struct{})
	statePaths := make(map[string]struct{})
	actions := 0
	for _, run := range report.Runs {
		if !run.Completed {
			continue
		}
		actions += run.Actions
		for _, key := range run.StateKeys {
			states[key] = struct{}{}
		}
		remember(plans, run.PlanDigest)
		remember(traces, run.TraceDigest)
		remember(statePaths, run.ModelStatePathDigest)
	}
	return CoveragePoint{
		CompletedRuns: completed, TotalActions: actions, UniqueModelStates: len(states),
		UniquePlans: len(plans), UniqueTraces: len(traces), UniqueModelStatePaths: len(statePaths),
		CorpusEntries: collection.Len(), ElapsedMillis: elapsedMillis,
	}
}

func copyCandidates(source []Candidate) []Candidate {
	result := make([]Candidate, len(source))
	for index, candidate := range source {
		result[index] = copyCandidate(candidate)
	}
	return result
}

func scheduledValues(values map[int]ScheduledCandidate) []ScheduledCandidate {
	result := make([]ScheduledCandidate, 0, len(values))
	for _, value := range values {
		value.Candidate = copyCandidate(value.Candidate)
		result = append(result, value)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Index < result[j].Index })
	return result
}

func mergeScheduled(groups ...[]ScheduledCandidate) []ScheduledCandidate {
	result := make([]ScheduledCandidate, 0)
	for _, group := range groups {
		for _, value := range group {
			value.Candidate = copyCandidate(value.Candidate)
			result = append(result, value)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Index < result[j].Index })
	return result
}

func pendingValues(values map[string]mutation.Request) []mutation.Request {
	result := make([]mutation.Request, 0, len(values))
	for _, value := range values {
		result = append(result, value)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Entry.RunIndex < result[j].Entry.RunIndex })
	return result
}

func countEffects(result engine.Result) int {
	total := 0
	for _, step := range result.Trace.Steps {
		total += len(step.Effects)
	}
	return total
}
