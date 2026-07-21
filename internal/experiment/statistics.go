package experiment

import "github.com/SuzumiyaHaruki/modelfuzz-ng/internal/metrics"

// Statistics 是 experiment-metrics.json 的稳定顶层结构。Report 保留每轮
// 明细，这里只保存适合画图和跨实验汇总的部分。
type Statistics struct {
	CompletedRuns                int                       `json:"completed_runs"`
	Succeeded                    int                       `json:"succeeded"`
	Failed                       int                       `json:"failed"`
	StatusCounts                 map[string]int            `json:"status_counts"`
	TotalActions                 int                       `json:"total_actions"`
	TotalEffects                 int                       `json:"total_effects"`
	TotalModelEvents             int                       `json:"total_model_events"`
	UniqueModelStates            int                       `json:"unique_model_states"`
	CorpusEntries                int                       `json:"corpus_entries"`
	RetainedRuns                 int                       `json:"retained_runs"`
	InitialExecutions            int                       `json:"initial_executions"`
	ExecutedMutations            int                       `json:"executed_mutations"`
	PeriodicSeedExecutions       int                       `json:"periodic_seed_executions"`
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
}

func (r Report) Statistics() Statistics {
	return Statistics{
		CompletedRuns: r.CompletedRuns, Succeeded: r.Succeeded, Failed: r.Failed,
		StatusCounts: r.StatusCounts, TotalActions: r.TotalActions, TotalEffects: r.TotalEffects,
		TotalModelEvents: r.TotalModelEvents, UniqueModelStates: r.UniqueModelStates,
		CorpusEntries: r.CorpusEntries, RetainedRuns: r.RetainedRuns,
		InitialExecutions: r.InitialExecutions, ExecutedMutations: r.ExecutedMutations,
		PeriodicSeedExecutions: r.PeriodicSeedExecutions,
		PlansObserved:          r.PlansObserved, TracesObserved: r.TracesObserved,
		ModelStatePathsObserved: r.ModelStatePathsObserved,
		UniquePlans:             r.UniquePlans, UniqueTraces: r.UniqueTraces,
		UniqueModelStatePaths: r.UniqueModelStatePaths,
		DuplicatePlanRatio:    r.DuplicatePlanRatio, DuplicateTraceRatio: r.DuplicateTraceRatio,
		DuplicateModelStatePathRatio: r.DuplicateModelStatePathRatio,
		NoveltyBySource:              copySourceNovelty(r.NoveltyBySource),
		ActionCounts:                 r.ActionCounts, EffectCounts: r.EffectCounts, MessageTypeCounts: r.MessageTypeCounts,
		ResolutionCounts: r.ResolutionCounts, DecisionCounts: r.DecisionCounts,
		DecisionCountsBySource: copyNestedCounts(r.DecisionCountsBySource),
		ModelEventCounts:       r.ModelEventCounts, OracleCounts: r.OracleCounts, TimerFireCounts: r.TimerFireCounts,
		FailureCounts: r.FailureCounts, TerminationCounts: r.TerminationCounts, Duration: r.Duration,
		ActionsPerSecond: r.ActionsPerSecond, RunsPerSecond: r.RunsPerSecond,
		MaxCorpusDepth: r.MaxCorpusDepth, MaxQueuedMessages: r.MaxQueuedMessages,
		CoverageTimeline: append([]CoveragePoint(nil), r.CoverageTimeline...),
		ElapsedMillis:    r.ElapsedMillis,
	}
}

func copySourceNovelty(source map[string]SourceNovelty) map[string]SourceNovelty {
	result := make(map[string]SourceNovelty, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

func copyNestedCounts(source map[string]map[string]int) map[string]map[string]int {
	result := make(map[string]map[string]int, len(source))
	for sourceName, counts := range source {
		result[sourceName] = make(map[string]int, len(counts))
		for code, count := range counts {
			result[sourceName][code] = count
		}
	}
	return result
}
