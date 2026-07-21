package experiment

import "github.com/SuzumiyaHaruki/modelfuzz-ng/internal/metrics"

// Statistics 是 experiment-metrics.json 的稳定顶层结构，只保存适合画图和
// 跨实验汇总的部分。
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
	GeneratedMutations           int                       `json:"generated_mutations"`
	AdmittedMutations            int                       `json:"admitted_mutations"`
	DiscardedMutations           int                       `json:"discarded_mutations"`
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
	PeakReadyCandidates          int                       `json:"peak_ready_candidates"`
	CoverageTimeline             []CoveragePoint           `json:"coverage_timeline"`
	ElapsedMillis                int64                     `json:"elapsed_millis"`
	SnapshotsCreated             int                       `json:"snapshots_created"`
	SnapshotsSent                int                       `json:"snapshots_sent"`
	SnapshotsDelivered           int                       `json:"snapshots_delivered"`
	SnapshotsApplied             int                       `json:"snapshots_applied"`
	SnapshotsRejectedOrStale     int                       `json:"snapshots_rejected_or_stale"`
	LogsCompacted                int                       `json:"logs_compacted"`
	CompactedEntries             uint64                    `json:"compacted_entries"`
	SnapshotBytes                uint64                    `json:"snapshot_bytes"`
}

func (r Report) Statistics() Statistics {
	return Statistics{
		CompletedRuns: r.CompletedRuns, Succeeded: r.Succeeded, Failed: r.Failed,
		StatusCounts: r.StatusCounts, TotalActions: r.TotalActions, TotalEffects: r.TotalEffects,
		TotalModelEvents: r.TotalModelEvents, UniqueModelStates: r.UniqueModelStates,
		CorpusEntries: r.CorpusEntries, RetainedRuns: r.RetainedRuns,
		InitialExecutions: r.InitialExecutions, GeneratedMutations: r.GeneratedMutations,
		AdmittedMutations: r.AdmittedMutations, DiscardedMutations: r.DiscardedMutations,
		ExecutedMutations:      r.ExecutedMutations,
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
		PeakReadyCandidates: r.PeakReadyCandidates,
		CoverageTimeline:    append([]CoveragePoint(nil), r.CoverageTimeline...),
		ElapsedMillis:       r.ElapsedMillis,
		SnapshotsCreated:    r.SnapshotsCreated, SnapshotsSent: r.SnapshotsSent,
		SnapshotsDelivered: r.SnapshotsDelivered, SnapshotsApplied: r.SnapshotsApplied,
		SnapshotsRejectedOrStale: r.SnapshotsRejectedOrStale, LogsCompacted: r.LogsCompacted,
		CompactedEntries: r.CompactedEntries, SnapshotBytes: r.SnapshotBytes,
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
