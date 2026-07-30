package experiment

import (
	"fmt"
	"sort"
	"time"

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/corpus"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/metrics"
)

// AggregationSnapshot 是 checkpoint 中可恢复的增量统计。它只随实际唯一覆盖、
// 唯一输入和紧凑耗时样本增长，不保存完整 Run、Plan、Trace 或逐运行 Metrics。
type AggregationSnapshot struct {
	Report                Report   `json:"report"`
	CompletedRunIndices   []int    `json:"completed_run_indices"`
	ModelStateKeys        []int64  `json:"model_state_keys"`
	PlanDigests           []string `json:"plan_digests"`
	TraceDigests          []string `json:"trace_digests"`
	ModelStatePathDigests []string `json:"model_state_path_digests"`
	DurationMicros        []int64  `json:"duration_micros"`
}

func (s AggregationSnapshot) Validate(config Config, completed int) error {
	if s.Report.Config != config || !s.Report.Feedback || len(s.Report.Runs) != 0 {
		return fmt.Errorf("summary report has incompatible config, mode, or embedded runs")
	}
	if s.Report.CompletedRuns != completed || len(s.CompletedRunIndices) != completed || len(s.DurationMicros) != completed {
		return fmt.Errorf("summary counters do not match completed count %d", completed)
	}
	if s.Report.Succeeded+s.Report.Failed != completed ||
		s.Report.InitialExecutions+s.Report.ExecutedMutations+s.Report.PeriodicSeedExecutions != completed {
		return fmt.Errorf("summary success or candidate-source counters do not match completed count")
	}
	if s.Report.GeneratedMutations < 0 || s.Report.AdmittedMutations < 0 || s.Report.DiscardedMutations < 0 ||
		s.Report.ExecutedMutations > s.Report.AdmittedMutations || s.Report.PeakReadyCandidates < 0 ||
		s.Report.PeakReadyCandidates > config.MaxReadyCandidates || s.Report.UniqueSemanticStates < 0 ||
		s.Report.UniqueSemanticTransitions < 0 {
		return fmt.Errorf("summary mutation queue counters are invalid")
	}
	if err := validateCorpusAdmissions(s.Report); err != nil {
		return err
	}
	if err := validateUniqueInts(s.CompletedRunIndices, 0, config.Runs); err != nil {
		return fmt.Errorf("completed run indices: %w", err)
	}
	if err := validateUniqueInt64s(s.ModelStateKeys); err != nil {
		return fmt.Errorf("model state keys: %w", err)
	}
	if err := validateUniqueStrings(s.PlanDigests); err != nil {
		return fmt.Errorf("plan digests: %w", err)
	}
	if err := validateUniqueStrings(s.TraceDigests); err != nil {
		return fmt.Errorf("trace digests: %w", err)
	}
	if err := validateUniqueStrings(s.ModelStatePathDigests); err != nil {
		return fmt.Errorf("model state path digests: %w", err)
	}
	if s.Report.UniqueModelStates != len(s.ModelStateKeys) || s.Report.UniquePlans != len(s.PlanDigests) ||
		s.Report.UniqueTraces != len(s.TraceDigests) || s.Report.UniqueModelStatePaths != len(s.ModelStatePathDigests) {
		return fmt.Errorf("unique counters do not match persisted sets")
	}
	return nil
}

type reportAccumulator struct {
	report           Report
	completedIndices map[int]struct{}
	states           map[int64]struct{}
	plans            map[string]struct{}
	traces           map[string]struct{}
	statePaths       map[string]struct{}
	durations        []int64
}

func newReportAccumulator(config Config) *reportAccumulator {
	return &reportAccumulator{
		report: newFeedbackReport(config), completedIndices: make(map[int]struct{}),
		states: make(map[int64]struct{}), plans: make(map[string]struct{}),
		traces: make(map[string]struct{}), statePaths: make(map[string]struct{}),
		durations: make([]int64, 0),
	}
}

func restoreReportAccumulator(snapshot AggregationSnapshot) *reportAccumulator {
	result := &reportAccumulator{
		report: snapshot.Report, completedIndices: make(map[int]struct{}, len(snapshot.CompletedRunIndices)),
		states: make(map[int64]struct{}, len(snapshot.ModelStateKeys)), plans: make(map[string]struct{}, len(snapshot.PlanDigests)),
		traces:     make(map[string]struct{}, len(snapshot.TraceDigests)),
		statePaths: make(map[string]struct{}, len(snapshot.ModelStatePathDigests)),
		durations:  append([]int64(nil), snapshot.DurationMicros...),
	}
	for _, value := range snapshot.CompletedRunIndices {
		result.completedIndices[value] = struct{}{}
	}
	for _, value := range snapshot.ModelStateKeys {
		result.states[value] = struct{}{}
	}
	for _, value := range snapshot.PlanDigests {
		result.plans[value] = struct{}{}
	}
	for _, value := range snapshot.TraceDigests {
		result.traces[value] = struct{}{}
	}
	for _, value := range snapshot.ModelStatePathDigests {
		result.statePaths[value] = struct{}{}
	}
	return result
}

func (a *reportAccumulator) classify(run *Run) {
	run.NewPlan = markNew(a.plans, run.PlanDigest)
	run.NewTrace = markNew(a.traces, run.TraceDigest)
	run.NewModelStatePath = markNew(a.statePaths, run.ModelStatePathDigest)
}

func (a *reportAccumulator) addRun(run Run, corpusEntries int, elapsedMillis int64) error {
	if _, duplicate := a.completedIndices[run.Index]; duplicate {
		return fmt.Errorf("run %d was aggregated more than once", run.Index)
	}
	a.completedIndices[run.Index] = struct{}{}
	r := &a.report
	r.CompletedRuns++
	if run.Succeeded {
		r.Succeeded++
	} else {
		r.Failed++
	}
	r.StatusCounts[string(run.Status)]++
	r.TotalActions += run.Actions
	r.TotalEffects += run.Effects
	r.TotalModelEvents += run.ModelEvents
	a.durations = append(a.durations, run.DurationMicros)
	for _, key := range run.StateKeys {
		a.states[key] = struct{}{}
	}
	var source SourceNovelty
	if run.Source != "" {
		source = r.NoveltyBySource[run.Source]
		source.Executions++
	}
	if run.PlanDigest != "" {
		r.PlansObserved++
		if run.Source != "" {
			source.PlansObserved++
		}
	}
	if run.TraceDigest != "" {
		r.TracesObserved++
		if run.Source != "" {
			source.TracesObserved++
		}
	}
	if run.ModelStatePathDigest != "" {
		r.ModelStatePathsObserved++
		if run.Source != "" {
			source.ModelStatePathsObserved++
		}
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
		source.NewSemanticStates += run.NewSemanticStates
		source.NewSemanticTransitions += run.NewSemanticTransitions
		r.NoveltyBySource[run.Source] = source
	}
	r.UniqueSemanticStates += run.NewSemanticStates
	r.UniqueSemanticTransitions += run.NewSemanticTransitions
	addCorpusAdmission(r, run.CorpusAdmission)
	if run.CandidateKind == CandidatePeriodicRandom {
		r.PeriodicSeedExecutions++
	}
	if run.Metrics != nil {
		r.SnapshotsCreated += run.Metrics.SnapshotsCreated
		r.SnapshotsSent += run.Metrics.SnapshotsSent
		r.SnapshotsDelivered += run.Metrics.SnapshotsDelivered
		r.SnapshotsApplied += run.Metrics.SnapshotsApplied
		r.SnapshotsFastForwarded += run.Metrics.SnapshotsFastForwarded
		r.SnapshotsRejectedOrStale += run.Metrics.SnapshotsRejectedOrStale
		r.SnapshotStatusSucceeded += run.Metrics.SnapshotStatusSucceeded
		r.SnapshotStatusFailed += run.Metrics.SnapshotStatusFailed
		r.SnapshotStatusIgnored += run.Metrics.SnapshotStatusIgnored
		r.LogsCompacted += run.Metrics.LogsCompacted
		r.CompactedEntries += run.Metrics.CompactedEntries
		r.SnapshotBytes += run.Metrics.SnapshotBytes
		mergeCounts(r.ActionCounts, run.Metrics.ActionCounts)
		mergeCounts(r.EffectCounts, run.Metrics.EffectCounts)
		mergeCounts(r.MessageTypeCounts, run.Metrics.MessageTypeCounts)
		mergeCounts(r.ResolutionCounts, run.Metrics.ResolutionCounts)
		mergeCounts(r.DecisionCounts, run.Metrics.DecisionCounts)
		if run.Source != "" && len(run.Metrics.DecisionCounts) > 0 {
			if r.DecisionCountsBySource[run.Source] == nil {
				r.DecisionCountsBySource[run.Source] = make(map[string]int)
			}
			mergeCounts(r.DecisionCountsBySource[run.Source], run.Metrics.DecisionCounts)
		}
		mergeCounts(r.ModelEventCounts, run.Metrics.ModelEventCounts)
		mergeCounts(r.OracleCounts, run.Metrics.OracleCounts)
		mergeCounts(r.TimerFireCounts, run.Metrics.TimerFireCounts)
		mergeCounts(r.FailureCounts, run.Metrics.FailureCounts)
		if run.Metrics.EstimatedPeakQueuedMessages > r.MaxQueuedMessages {
			r.MaxQueuedMessages = run.Metrics.EstimatedPeakQueuedMessages
		}
	}
	if run.Termination != "" {
		r.TerminationCounts[string(run.Termination)]++
	}
	if run.Depth > r.MaxCorpusDepth {
		r.MaxCorpusDepth = run.Depth
	}
	r.UniqueModelStates, r.UniquePlans, r.UniqueTraces, r.UniqueModelStatePaths = len(a.states), len(a.plans), len(a.traces), len(a.statePaths)
	r.DuplicatePlanRatio = duplicateRatio(r.PlansObserved, r.UniquePlans)
	r.DuplicateTraceRatio = duplicateRatio(r.TracesObserved, r.UniqueTraces)
	r.DuplicateModelStatePathRatio = duplicateRatio(r.ModelStatePathsObserved, r.UniqueModelStatePaths)
	r.CorpusEntries, r.RetainedRuns = corpusEntries, corpusEntries
	point := CoveragePoint{
		CompletedRuns: r.CompletedRuns, TotalActions: r.TotalActions, UniqueModelStates: r.UniqueModelStates,
		UniqueSemanticStates: r.UniqueSemanticStates, UniqueSemanticTransitions: r.UniqueSemanticTransitions,
		UniquePlans: r.UniquePlans, UniqueTraces: r.UniqueTraces, UniqueModelStatePaths: r.UniqueModelStatePaths,
		CorpusEntries: corpusEntries, ElapsedMillis: elapsedMillis,
	}
	if shouldKeepCoveragePoint(r.CoverageTimeline, point) {
		r.CoverageTimeline = append(r.CoverageTimeline, point)
	}
	return nil
}

func shouldKeepCoveragePoint(existing []CoveragePoint, point CoveragePoint) bool {
	if point.CompletedRuns <= 100 || point.CompletedRuns%100 == 0 || len(existing) == 0 {
		return true
	}
	last := existing[len(existing)-1]
	return point.UniqueModelStates != last.UniqueModelStates || point.UniqueSemanticStates != last.UniqueSemanticStates ||
		point.UniqueSemanticTransitions != last.UniqueSemanticTransitions || point.CorpusEntries != last.CorpusEntries
}

func validateCorpusAdmissions(report Report) error {
	known := map[string]bool{
		string(corpus.AdmissionRetainedRaw):                        true,
		string(corpus.AdmissionRetainedSemanticState):              true,
		string(corpus.AdmissionRetainedSemanticTransition):         true,
		string(corpus.AdmissionRetainedSemanticStateAndTransition): true,
		string(corpus.AdmissionRejectedRawThreshold):               false,
		string(corpus.AdmissionRejectedNoSemanticNovelty):          false,
		"admitted_random_without_coverage":                         true,
		"admitted_new_raw":                                         true,
		"admitted_new_v2":                                          true,
		"admitted_new_facet":                                       true,
		"admitted_new_facet_and_interaction":                       true,
		"admitted_new_interaction":                                 true,
		"rejected_no_guidance_novelty":                             false,
		"rejected_unsuccessful_execution":                          false,
		"rejected_empty_plan_key":                                  false,
		"rejected_duplicate_plan":                                  false,
		"rejected_corpus_limit":                                    false,
	}
	total := 0
	retained := 0
	for reason, count := range report.CorpusAdmissionCounts {
		admitted, exists := known[reason]
		if !exists || count < 0 {
			return fmt.Errorf("summary contains invalid corpus admission %q=%d", reason, count)
		}
		total += count
		if admitted {
			retained += count
		}
	}
	counts := report.CorpusAdmissionCounts
	state := counts[string(corpus.AdmissionRetainedSemanticState)] + counts[string(corpus.AdmissionRetainedSemanticStateAndTransition)]
	transition := counts[string(corpus.AdmissionRetainedSemanticTransition)] + counts[string(corpus.AdmissionRetainedSemanticStateAndTransition)]
	if total > report.Succeeded || retained != report.CorpusEntries ||
		report.RejectedRawThreshold != counts[string(corpus.AdmissionRejectedRawThreshold)] ||
		report.RejectedNoSemanticNovelty != counts[string(corpus.AdmissionRejectedNoSemanticNovelty)] ||
		report.RetainedBySemanticState != state || report.RetainedBySemanticTransition != transition {
		return fmt.Errorf("summary corpus admission counters are inconsistent")
	}
	expectedDensity := noveltyPer100Actions(report.UniqueSemanticStates, report.UniqueSemanticTransitions, report.TotalActions)
	if report.SemanticNoveltyPer100Actions != expectedDensity {
		return fmt.Errorf("summary semantic novelty density is inconsistent")
	}
	return nil
}

func (a *reportAccumulator) finalize(corpusEntries int, elapsedMillis int64) Report {
	r := &a.report
	r.ElapsedMillis = elapsedMillis
	r.CorpusEntries, r.RetainedRuns = corpusEntries, corpusEntries
	r.Duration = metrics.Durations(a.durations)
	if elapsedMillis > 0 {
		seconds := float64(elapsedMillis) / float64(time.Second/time.Millisecond)
		r.ActionsPerSecond = float64(r.TotalActions) / seconds
		r.RunsPerSecond = float64(r.CompletedRuns) / seconds
	}
	r.SemanticNoveltyPer100Actions = noveltyPer100Actions(r.UniqueSemanticStates, r.UniqueSemanticTransitions, r.TotalActions)
	return *r
}

func (a *reportAccumulator) snapshot(corpusEntries int, elapsedMillis int64) AggregationSnapshot {
	report := a.finalize(corpusEntries, elapsedMillis)
	report.Runs = nil
	return AggregationSnapshot{
		Report: report, CompletedRunIndices: sortedIntSet(a.completedIndices), ModelStateKeys: sortedInt64Set(a.states),
		PlanDigests: sortedStringSet(a.plans), TraceDigests: sortedStringSet(a.traces),
		ModelStatePathDigests: sortedStringSet(a.statePaths), DurationMicros: append([]int64(nil), a.durations...),
	}
}

func sortedIntSet(values map[int]struct{}) []int {
	result := make([]int, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Ints(result)
	return result
}
func sortedInt64Set(values map[int64]struct{}) []int64 {
	result := make([]int64, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result
}
func sortedStringSet(values map[string]struct{}) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func validateUniqueInts(values []int, minimum, maximum int) error {
	seen := make(map[int]struct{}, len(values))
	for _, value := range values {
		if value < minimum || value >= maximum {
			return fmt.Errorf("value %d is out of range", value)
		}
		if _, exists := seen[value]; exists {
			return fmt.Errorf("duplicate value %d", value)
		}
		seen[value] = struct{}{}
	}
	return nil
}
func validateUniqueInt64s(values []int64) error {
	seen := make(map[int64]struct{}, len(values))
	for _, value := range values {
		if _, exists := seen[value]; exists {
			return fmt.Errorf("duplicate value %d", value)
		}
		seen[value] = struct{}{}
	}
	return nil
}
func validateUniqueStrings(values []string) error {
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if value == "" {
			return fmt.Errorf("empty digest")
		}
		if _, exists := seen[value]; exists {
			return fmt.Errorf("duplicate digest %s", value)
		}
		seen[value] = struct{}{}
	}
	return nil
}
