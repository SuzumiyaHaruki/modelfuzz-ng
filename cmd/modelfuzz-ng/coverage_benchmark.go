package main

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"time"

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/coverageguidance"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/experiment"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/persistence"
)

const coverageBenchmarkSchema = "facet-guidance-benchmark-v1"

type coverageBenchmarkManifest struct {
	Schema                   string   `json:"schema"`
	Name                     string   `json:"name"`
	Phase                    string   `json:"phase"`
	Config                   string   `json:"config"`
	TLCAddress               string   `json:"tlc_address"`
	Modes                    []string `json:"modes"`
	Seeds                    []int64  `json:"seeds"`
	Runs                     int      `json:"runs"`
	MaxPlanActions           int      `json:"max_plan_actions"`
	InitialPopulation        int      `json:"initial_population"`
	Parallelism              int      `json:"parallelism"`
	FixedEnergy              int      `json:"fixed_energy"`
	FixedParentSelection     string   `json:"fixed_parent_selection"`
	CoverageCorpusLimit      int      `json:"coverage_corpus_limit"`
	MaxReadyCandidates       int      `json:"max_ready_candidates"`
	ArtifactPolicy           string   `json:"artifact_policy"`
	RecordAllCoverageMetrics bool     `json:"record_all_coverage_metrics"`
	OfflineGoalEvaluation    bool     `json:"offline_goal_evaluation"`
	SnapshotThreshold        uint64   `json:"snapshot_threshold,omitempty"`
	SnapshotRetainEntries    uint64   `json:"snapshot_retain_entries,omitempty"`
}

type coverageCampaignResult struct {
	Mode          coverageguidance.Mode                  `json:"mode"`
	Seed          int64                                  `json:"seed"`
	Directory     string                                 `json:"directory"`
	Skipped       bool                                   `json:"skipped"`
	Report        experiment.Report                      `json:"report"`
	CrossCoverage *coverageguidance.CrossCoverageSummary `json:"cross_coverage,omitempty"`
}

type coverageBenchmarkSummary struct {
	Schema       string                   `json:"schema"`
	ManifestName string                   `json:"manifest_name"`
	Phase        string                   `json:"phase"`
	Campaigns    []coverageCampaignResult `json:"campaigns"`
	Completed    int                      `json:"completed"`
	Skipped      int                      `json:"skipped"`
	Failed       int                      `json:"failed"`
}

func coverageBenchmarkCommand(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("modelfuzz-ng coverage-benchmark", flag.ContinueOnError)
	flags.SetOutput(stderr)
	manifestPath := flags.String("manifest", "", "冻结的 Facet guidance benchmark manifest")
	outputPath := flags.String("output", "", "各 mode/seed 完全隔离的输出根目录")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || *manifestPath == "" || *outputPath == "" {
		return fmt.Errorf("coverage-benchmark requires -manifest and -output")
	}
	manifestAbsolute, err := filepath.Abs(*manifestPath)
	if err != nil {
		return err
	}
	var manifest coverageBenchmarkManifest
	manifestData, err := os.ReadFile(manifestAbsolute)
	if err != nil {
		return fmt.Errorf("read coverage benchmark manifest: %w", err)
	}
	manifest, err = decodeCoverageBenchmarkManifest(manifestData)
	if err != nil {
		return fmt.Errorf("read coverage benchmark manifest: %w", err)
	}
	if err := validateCoverageBenchmarkManifest(manifest); err != nil {
		return err
	}
	if !filepath.IsAbs(manifest.Config) {
		manifest.Config = filepath.Join(filepath.Dir(manifestAbsolute), manifest.Config)
	}
	outputAbsolute, err := filepath.Abs(*outputPath)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(outputAbsolute, 0o755); err != nil {
		return err
	}
	if err := persistence.WriteJSONAtomic(filepath.Join(outputAbsolute, "manifest.json"), manifest); err != nil {
		return err
	}
	if err := persistence.WriteJSONAtomic(filepath.Join(outputAbsolute, "seed-manifest.json"), struct {
		Schema string  `json:"schema"`
		Seeds  []int64 `json:"seeds"`
	}{Schema: "coverage-seed-manifest-v1", Seeds: manifest.Seeds}); err != nil {
		return err
	}
	environment := struct {
		Schema      string   `json:"schema"`
		GoVersion   string   `json:"go_version"`
		GOOS        string   `json:"goos"`
		GOARCH      string   `json:"goarch"`
		CPU         int      `json:"cpu"`
		LLMCalls    int      `json:"llm_calls"`
		Command     []string `json:"command"`
		Manifest    string   `json:"manifest"`
		GeneratedAt string   `json:"generated_at"`
	}{
		Schema: "coverage-benchmark-environment-v1", GoVersion: runtime.Version(),
		GOOS: runtime.GOOS, GOARCH: runtime.GOARCH, CPU: runtime.NumCPU(), LLMCalls: 0,
		Command: append([]string{"coverage-benchmark"}, args...), Manifest: manifestAbsolute,
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
	}
	if err := persistence.WriteJSONAtomic(filepath.Join(outputAbsolute, "environment.json"), environment); err != nil {
		return err
	}
	summary := coverageBenchmarkSummary{
		Schema: coverageBenchmarkSchema, ManifestName: manifest.Name, Phase: manifest.Phase,
		Campaigns: make([]coverageCampaignResult, 0, len(manifest.Modes)*len(manifest.Seeds)),
	}
	for _, modeText := range manifest.Modes {
		mode, _ := coverageguidance.ParseMode(modeText)
		for _, seed := range manifest.Seeds {
			if err := ctx.Err(); err != nil {
				return err
			}
			directory := filepath.Join(outputAbsolute, string(mode), fmt.Sprintf("seed-%d", seed))
			result := coverageCampaignResult{Mode: mode, Seed: seed, Directory: directory}
			complete, report := completedCoverageCampaign(directory, manifest.Runs, mode)
			if complete {
				result.Skipped, result.Report = true, report
				result.CrossCoverage = readCrossCoverage(directory)
				summary.Skipped++
				summary.Completed++
				summary.Campaigns = append(summary.Campaigns, result)
				continue
			}
			var experimentArgs []string
			if _, statErr := os.Stat(filepath.Join(directory, "checkpoint.json")); statErr == nil {
				experimentArgs = []string{"-resume", directory}
			} else {
				experimentArgs = coverageExperimentArguments(manifest, mode, seed, directory)
			}
			var campaignOutput bytes.Buffer
			campaignErr := experimentCommand(ctx, experimentArgs, &campaignOutput, stderr)
			if campaignOutput.Len() > 0 {
				_, _ = io.Copy(stdout, &campaignOutput)
			}
			if campaignErr != nil {
				summary.Failed++
				summary.Campaigns = append(summary.Campaigns, result)
				_ = persistence.WriteJSONAtomic(filepath.Join(outputAbsolute, "benchmark-summary.json"), summary)
				return fmt.Errorf("%s seed %d: %w", mode, seed, campaignErr)
			}
			if mode == coverageguidance.ModeLegacyRaw {
				if err := coverageSummarizeCommand(
					[]string{"-input", directory}, stdout, stderr); err != nil {
					return fmt.Errorf("summarize legacy-raw seed %d: %w", seed, err)
				}
			}
			result.Report = readExperimentReport(directory)
			result.CrossCoverage = readCrossCoverage(directory)
			summary.Completed++
			summary.Campaigns = append(summary.Campaigns, result)
			if err := persistence.WriteJSONAtomic(filepath.Join(outputAbsolute, "benchmark-summary.json"), summary); err != nil {
				return err
			}
		}
	}
	sort.Slice(summary.Campaigns, func(i, j int) bool {
		if summary.Campaigns[i].Mode != summary.Campaigns[j].Mode {
			return summary.Campaigns[i].Mode < summary.Campaigns[j].Mode
		}
		return summary.Campaigns[i].Seed < summary.Campaigns[j].Seed
	})
	if err := persistence.WriteJSONAtomic(filepath.Join(outputAbsolute, "benchmark-summary.json"), summary); err != nil {
		return err
	}
	if err := writeCrossCoverageMatrix(filepath.Join(outputAbsolute, "cross-coverage-matrix.csv"), summary.Campaigns); err != nil {
		return err
	}
	if err := writeCoverageBenchmarkStatistics(
		filepath.Join(outputAbsolute, "benchmark-statistics.json"), summary.Campaigns); err != nil {
		return err
	}
	if err := writeCoverageConsistencySummary(
		filepath.Join(outputAbsolute, "online-offline-consistency-summary.json"),
		summary.Campaigns,
	); err != nil {
		return err
	}
	_, err = fmt.Fprintf(stdout, "coverage benchmark complete: phase=%s campaigns=%d skipped=%d output=%s\n",
		manifest.Phase, summary.Completed, summary.Skipped, outputAbsolute)
	return err
}

func writeCoverageConsistencySummary(path string, campaigns []coverageCampaignResult) error {
	aggregate := coverageConsistencyReport{
		Schema: "online-offline-coverage-consistency-summary-v1",
	}
	for _, campaign := range campaigns {
		var report coverageConsistencyReport
		if err := persistence.ReadJSON(
			filepath.Join(campaign.Directory, "online-offline-consistency.json"), &report,
		); err != nil {
			continue
		}
		aggregate.Candidates += report.Candidates
		aggregate.Compared += report.Compared
		aggregate.Mismatches += report.Mismatches
		aggregate.RawMismatch += report.RawMismatch
		aggregate.V2Mismatch += report.V2Mismatch
		aggregate.FacetMismatch += report.FacetMismatch
		aggregate.InteractionMismatch += report.InteractionMismatch
		aggregate.StableKeyMismatch += report.StableKeyMismatch
		aggregate.DecisionRecomputeMismatch += report.DecisionRecomputeMismatch
		aggregate.UnavailableArtifacts += report.UnavailableArtifacts
	}
	return persistence.WriteJSONAtomic(path, aggregate)
}

type descriptiveStatistics struct {
	N      int     `json:"n"`
	Mean   float64 `json:"mean"`
	Median float64 `json:"median"`
	SD     float64 `json:"standard_deviation"`
	IQR    float64 `json:"iqr"`
	Min    float64 `json:"min"`
	Max    float64 `json:"max"`
}

type rateStatistics struct {
	Success  int        `json:"success"`
	Total    int        `json:"total"`
	Rate     float64    `json:"rate"`
	Wilson95 [2]float64 `json:"wilson_95"`
}

type censoredFailureObservation struct {
	Mode       coverageguidance.Mode `json:"mode"`
	Seed       int64                 `json:"seed"`
	Detected   bool                  `json:"detected"`
	Censored   bool                  `json:"censored"`
	Candidate  int                   `json:"candidate"`
	Actions    int                   `json:"actions"`
	WallMillis int64                 `json:"wall_millis"`
}

type modeBenchmarkStatistics struct {
	Metrics           map[string]descriptiveStatistics `json:"metrics"`
	GoalA             rateStatistics                   `json:"goal_a"`
	GoalB             rateStatistics                   `json:"goal_b"`
	Failure           rateStatistics                   `json:"failure"`
	FailureSignatures map[string]int                   `json:"failure_signatures"`
	EffectVsRandom    map[string]float64               `json:"cliffs_delta_vs_random"`
}

type benchmarkStatisticsArtifact struct {
	Schema          string                                            `json:"schema"`
	ByMode          map[coverageguidance.Mode]modeBenchmarkStatistics `json:"by_mode"`
	CensoredFailure []censoredFailureObservation                      `json:"censored_failure"`
	Inference       string                                            `json:"inference"`
}

func writeCoverageBenchmarkStatistics(path string, campaigns []coverageCampaignResult) error {
	values := make(map[coverageguidance.Mode]map[string][]float64)
	rates := make(map[coverageguidance.Mode]map[string]int)
	totals := make(map[coverageguidance.Mode]int)
	failureSignatures := make(map[coverageguidance.Mode]map[string]int)
	censored := make([]censoredFailureObservation, 0, len(campaigns))
	for _, campaign := range campaigns {
		if campaign.CrossCoverage == nil {
			continue
		}
		if values[campaign.Mode] == nil {
			values[campaign.Mode] = make(map[string][]float64)
			rates[campaign.Mode] = make(map[string]int)
			failureSignatures[campaign.Mode] = make(map[string]int)
		}
		cross := campaign.CrossCoverage
		add := func(name string, value int) {
			values[campaign.Mode][name] = append(values[campaign.Mode][name], float64(value))
		}
		add("raw", cross.RawDistinct)
		add("v2", cross.V2Distinct)
		for name, value := range cross.FacetDistinct {
			add("facet:"+name, value)
		}
		for name, value := range cross.InteractionDistinct {
			add("interaction:"+name, value)
		}
		add("corpus", cross.CorpusSize)
		add("semantic_traces", cross.SemanticTraceCount)
		add("actions", campaign.Report.TotalActions)
		add("model_events", campaign.Report.TotalModelEvents)
		add("elapsed_millis", int(campaign.Report.ElapsedMillis))
		add("peak_ready_candidates", campaign.Report.PeakReadyCandidates)
		add("max_queued_messages", campaign.Report.MaxQueuedMessages)
		add("unique_plans", campaign.Report.UniquePlans)
		add("unique_traces", campaign.Report.UniqueTraces)
		add("unique_model_state_paths", campaign.Report.UniqueModelStatePaths)
		add("snapshots_created", campaign.Report.SnapshotsCreated)
		add("snapshots_delivered", campaign.Report.SnapshotsDelivered)
		add("snapshots_applied", campaign.Report.SnapshotsApplied)
		var guidanceSummary coverageguidance.Summary
		if err := persistence.ReadJSON(
			filepath.Join(campaign.Directory, "coverage-guidance-summary.json"),
			&guidanceSummary,
		); err == nil {
			addSummaryMetrics(values[campaign.Mode], guidanceSummary)
			for signature, count := range guidanceSummary.FailureSignatures {
				failureSignatures[campaign.Mode][signature] += count
			}
		}
		addTLCMetrics(values[campaign.Mode], campaign.Directory)
		totals[campaign.Mode]++
		if cross.GoalAReached > 0 {
			rates[campaign.Mode]["goal_a"]++
		}
		if cross.GoalBReached > 0 {
			rates[campaign.Mode]["goal_b"]++
		}
		if cross.Failures > 0 {
			rates[campaign.Mode]["failure"]++
		}
		failure := censoredFailureObservation{
			Mode: campaign.Mode, Seed: campaign.Seed, Censored: true,
			Candidate: campaign.Report.CompletedRuns, Actions: campaign.Report.TotalActions,
			WallMillis: campaign.Report.ElapsedMillis,
		}
		observations, err := persistence.ReadJSONLines[coverageguidance.CoverageObservation](
			filepath.Join(campaign.Directory, "coverage-observations.jsonl"), campaign.Report.CompletedRuns)
		if err == nil {
			actions := 0
			for index, observation := range observations {
				actions += observation.ActionCount
				if !observation.Outcome.Succeeded {
					failure.Detected, failure.Censored = true, false
					failure.Candidate, failure.Actions = index+1, actions
					failure.WallMillis = observation.ElapsedMillis
					break
				}
			}
		}
		censored = append(censored, failure)
	}
	artifact := benchmarkStatisticsArtifact{
		Schema:          "coverage-benchmark-statistics-v1",
		ByMode:          make(map[coverageguidance.Mode]modeBenchmarkStatistics),
		CensoredFailure: censored,
		Inference:       "descriptive statistics only; n below 20 is not used to claim statistical significance",
	}
	random := values[coverageguidance.ModeRandom]
	for mode, metrics := range values {
		modeStats := modeBenchmarkStatistics{
			Metrics:           make(map[string]descriptiveStatistics),
			FailureSignatures: failureSignatures[mode],
			EffectVsRandom:    make(map[string]float64),
		}
		for name, metricValues := range metrics {
			modeStats.Metrics[name] = describe(metricValues)
			if mode != coverageguidance.ModeRandom && len(random[name]) > 0 {
				modeStats.EffectVsRandom[name] = cliffsDelta(metricValues, random[name])
			}
		}
		total := totals[mode]
		modeStats.GoalA = rateSummary(rates[mode]["goal_a"], total)
		modeStats.GoalB = rateSummary(rates[mode]["goal_b"], total)
		modeStats.Failure = rateSummary(rates[mode]["failure"], total)
		artifact.ByMode[mode] = modeStats
	}
	return persistence.WriteJSONAtomic(path, artifact)
}

type benchmarkTLCSnapshot struct {
	Requests      int64 `json:"requests"`
	Succeeded     int64 `json:"succeeded"`
	Failed        int64 `json:"failed"`
	ModelEvents   int64 `json:"model_events"`
	ActionLookups int64 `json:"action_lookups"`
	Timing        struct {
		MappingNanos       int64 `json:"mapping_nanos"`
		ActionLookupNanos  int64 `json:"action_lookup_nanos"`
		SuccessorNanos     int64 `json:"successor_nanos"`
		ValidationNanos    int64 `json:"validation_nanos"`
		SerializationNanos int64 `json:"serialization_nanos"`
	} `json:"timing"`
}

func addTLCMetrics(values map[string][]float64, directory string) {
	var artifact struct {
		Segments []struct {
			Start benchmarkTLCSnapshot `json:"start"`
			End   benchmarkTLCSnapshot `json:"end"`
		} `json:"segments"`
	}
	if err := persistence.ReadJSON(filepath.Join(directory, "tlc-server-metrics.json"), &artifact); err != nil {
		return
	}
	totals := make(map[string]int64)
	add := func(name string, value int64) { totals[name] += value }
	for _, segment := range artifact.Segments {
		add("requests", segment.End.Requests-segment.Start.Requests)
		add("succeeded", segment.End.Succeeded-segment.Start.Succeeded)
		add("failed", segment.End.Failed-segment.Start.Failed)
		add("model_events", segment.End.ModelEvents-segment.Start.ModelEvents)
		add("action_lookups", segment.End.ActionLookups-segment.Start.ActionLookups)
		add("mapping_nanos", segment.End.Timing.MappingNanos-segment.Start.Timing.MappingNanos)
		add("action_lookup_nanos", segment.End.Timing.ActionLookupNanos-segment.Start.Timing.ActionLookupNanos)
		add("successor_nanos", segment.End.Timing.SuccessorNanos-segment.Start.Timing.SuccessorNanos)
		add("validation_nanos", segment.End.Timing.ValidationNanos-segment.Start.Timing.ValidationNanos)
		add("serialization_nanos", segment.End.Timing.SerializationNanos-segment.Start.Timing.SerializationNanos)
	}
	for name, value := range totals {
		values["tlc:"+name] = append(values["tlc:"+name], float64(value))
	}
}

func addSummaryMetrics(values map[string][]float64, summary coverageguidance.Summary) {
	add := func(name string, value float64) {
		values[name] = append(values[name], value)
	}
	dimensionNames := make([]string, 0, len(summary.Dimensions))
	for name := range summary.Dimensions {
		dimensionNames = append(dimensionNames, name)
	}
	sort.Strings(dimensionNames)
	for _, name := range dimensionNames {
		dimension := summary.Dimensions[name]
		prefix := "dimension:" + name + ":"
		add(prefix+"auc_candidate", dimension.AUCByCandidate)
		add(prefix+"auc_action", dimension.AUCByAction)
		add(prefix+"auc_time", dimension.AUCByTime)
		add(prefix+"last_novel_candidate", float64(dimension.LastNovelCandidate))
		add(prefix+"new_per_1000_actions", dimension.NewPerThousandActions)
		add(prefix+"singletons", float64(dimension.Singletons))
		if dimension.ApproximatelySaturated {
			add(prefix+"approximately_saturated", 1)
		} else {
			add(prefix+"approximately_saturated", 0)
		}
		for index, quartile := range dimension.Quartiles {
			add(prefix+fmt.Sprintf("q%d_new_units", index+1), float64(quartile.NewUnits))
		}
		if dimension.Q4ToQ1 != nil {
			add(prefix+"q4_to_q1", *dimension.Q4ToQ1)
		}
	}
	add("corpus:admission_rate", summary.Corpus.AdmissionRate)
	add("corpus:multi_facet_admission_rate", summary.Corpus.MultiFacetAdmissionRate)
	add("corpus:semantic_duplicate_ratio", summary.Corpus.SemanticDuplicateRatio)
	add("corpus:raw_new_facet_old", float64(summary.Corpus.RawNewFacetOld))
	add("corpus:facet_new_raw_old", float64(summary.Corpus.FacetNewRawOld))
	add("corpus:v2_new_facet_old", float64(summary.Corpus.V2NewFacetOld))
	add("corpus:facet_new_v2_old", float64(summary.Corpus.FacetNewV2Old))
	add("corpus:parent_selections", float64(summary.Corpus.ParentSelections))
	add("corpus:parents_with_novel_child", float64(summary.Corpus.ParentsWithNovelChild))
	add("corpus:parent_novelty_yield", summary.Corpus.ParentNoveltyYield)
	add("corpus:candidate_legal_rate", summary.Corpus.CandidateLegalRate)
	add("corpus:candidate_execution_rate", summary.Corpus.CandidateExecutionRate)
	add("corpus:mean_executed_actions", summary.Corpus.MeanExecutedActions)
	add("corpus:median_executed_actions", summary.Corpus.MedianExecutedActions)
	add("corpus:new_units_per_admission", summary.Corpus.NewUnitsPerAdmission)
	add("throughput:candidates_per_second", summary.Throughput.CandidatesPerSecond)
	add("throughput:actions_per_second", summary.Throughput.ActionsPerSecond)
	add("throughput:model_events_per_second", summary.Throughput.ModelEventsPerSecond)
	add("throughput:raw_coverage_nanos", float64(summary.Throughput.RawCoverageNanos))
	add("throughput:v2_coverage_nanos", float64(summary.Throughput.V2CoverageNanos))
	add("throughput:coverage_frame_nanos", float64(summary.Throughput.CoverageFrameNanos))
	add("throughput:facet_coverage_nanos", float64(summary.Throughput.FacetCoverageNanos))
	add("throughput:corpus_decision_nanos", float64(summary.Throughput.CorpusDecisionNanos))
}

func describe(values []float64) descriptiveStatistics {
	if len(values) == 0 {
		return descriptiveStatistics{}
	}
	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)
	total := 0.0
	for _, value := range sorted {
		total += value
	}
	mean := total / float64(len(sorted))
	var variance float64
	if len(sorted) > 1 {
		for _, value := range sorted {
			variance += (value - mean) * (value - mean)
		}
		variance /= float64(len(sorted) - 1)
	}
	return descriptiveStatistics{
		N: len(sorted), Mean: mean, Median: percentile(sorted, 0.5),
		SD: math.Sqrt(variance), IQR: percentile(sorted, 0.75) - percentile(sorted, 0.25),
		Min: sorted[0], Max: sorted[len(sorted)-1],
	}
}

func percentile(sorted []float64, quantile float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	position := quantile * float64(len(sorted)-1)
	lower := int(math.Floor(position))
	upper := int(math.Ceil(position))
	if lower == upper {
		return sorted[lower]
	}
	weight := position - float64(lower)
	return sorted[lower]*(1-weight) + sorted[upper]*weight
}

func rateSummary(success, total int) rateStatistics {
	result := rateStatistics{Success: success, Total: total}
	if total == 0 {
		return result
	}
	result.Rate = float64(success) / float64(total)
	z := 1.959963984540054
	n, p := float64(total), result.Rate
	denominator := 1 + z*z/n
	center := (p + z*z/(2*n)) / denominator
	margin := z * math.Sqrt(p*(1-p)/n+z*z/(4*n*n)) / denominator
	result.Wilson95 = [2]float64{math.Max(0, center-margin), math.Min(1, center+margin)}
	return result
}

func cliffsDelta(left, right []float64) float64 {
	if len(left) == 0 || len(right) == 0 {
		return 0
	}
	greater, less := 0, 0
	for _, l := range left {
		for _, r := range right {
			if l > r {
				greater++
			} else if l < r {
				less++
			}
		}
	}
	return float64(greater-less) / float64(len(left)*len(right))
}

func validateCoverageBenchmarkManifest(manifest coverageBenchmarkManifest) error {
	if manifest.Schema != coverageBenchmarkSchema {
		return fmt.Errorf("unsupported coverage benchmark schema %q", manifest.Schema)
	}
	if manifest.Name == "" || manifest.Phase == "" || manifest.Config == "" ||
		len(manifest.Modes) == 0 || len(manifest.Seeds) == 0 {
		return fmt.Errorf("coverage benchmark manifest lacks name, phase, config, modes, or seeds")
	}
	if manifest.Runs <= 0 || manifest.MaxPlanActions <= 0 || manifest.InitialPopulation <= 0 ||
		manifest.Parallelism <= 0 || manifest.MaxReadyCandidates <= 0 {
		return fmt.Errorf("coverage benchmark budgets must be positive")
	}
	seenModes := make(map[coverageguidance.Mode]struct{})
	for _, value := range manifest.Modes {
		mode, err := coverageguidance.ParseMode(value)
		if err != nil {
			return err
		}
		if _, duplicate := seenModes[mode]; duplicate {
			return fmt.Errorf("duplicate coverage benchmark mode %s", mode)
		}
		seenModes[mode] = struct{}{}
	}
	seenSeeds := make(map[int64]struct{})
	for _, seed := range manifest.Seeds {
		if _, duplicate := seenSeeds[seed]; duplicate {
			return fmt.Errorf("duplicate coverage benchmark seed %d", seed)
		}
		seenSeeds[seed] = struct{}{}
	}
	if manifest.FixedEnergy <= 0 || manifest.CoverageCorpusLimit <= 0 ||
		manifest.FixedParentSelection != "admission-fifo-once" {
		return fmt.Errorf("coverage benchmark fixed energy, corpus limit, or parent policy is invalid")
	}
	switch manifest.ArtifactPolicy {
	case "all", "retained", "failures", "summary":
	default:
		return fmt.Errorf("unknown coverage benchmark artifact policy %q", manifest.ArtifactPolicy)
	}
	return nil
}

func coverageExperimentArguments(
	manifest coverageBenchmarkManifest, mode coverageguidance.Mode, seed int64, directory string,
) []string {
	energyMode := "fixed"
	if mode == coverageguidance.ModeLegacyRaw {
		energyMode = "legacy"
	}
	arguments := []string{
		"-config", manifest.Config, "-output", directory,
		"-runs", strconv.Itoa(manifest.Runs), "-max-plan-actions", strconv.Itoa(manifest.MaxPlanActions),
		"-initial-population", strconv.Itoa(manifest.InitialPopulation),
		"-parallelism", strconv.Itoa(manifest.Parallelism),
		"-max-ready-candidates", strconv.Itoa(manifest.MaxReadyCandidates),
		"-artifact-policy", manifest.ArtifactPolicy, "-seed", strconv.FormatInt(seed, 10),
		"-coverage-guidance-mode", string(mode), "-coverage-energy-mode", energyMode,
		"-fixed-energy", strconv.Itoa(manifest.FixedEnergy),
		"-fixed-parent-selection", manifest.FixedParentSelection,
		"-coverage-corpus-limit", strconv.Itoa(manifest.CoverageCorpusLimit),
		"-record-all-coverage-metrics=" + strconv.FormatBool(manifest.RecordAllCoverageMetrics),
		"-offline-goal-evaluation=" + strconv.FormatBool(manifest.OfflineGoalEvaluation),
		"-snapshot-threshold", strconv.FormatUint(manifest.SnapshotThreshold, 10),
		"-snapshot-retain-entries", strconv.FormatUint(manifest.SnapshotRetainEntries, 10),
	}
	if manifest.TLCAddress != "" {
		arguments = append(arguments, "-tlc", manifest.TLCAddress)
	}
	return arguments
}

func completedCoverageCampaign(directory string, runs int, mode coverageguidance.Mode) (bool, experiment.Report) {
	report := readExperimentReport(directory)
	if report.CompletedRuns != runs {
		return false, experiment.Report{}
	}
	var cross coverageguidance.CrossCoverageSummary
	if err := persistence.ReadJSON(filepath.Join(directory, "cross-coverage-summary.json"), &cross); err != nil ||
		cross.Mode != mode {
		return false, experiment.Report{}
	}
	return true, report
}

func readExperimentReport(directory string) experiment.Report {
	var report experiment.Report
	_ = persistence.ReadJSON(filepath.Join(directory, "experiment-report.json"), &report)
	return report
}

func readCrossCoverage(directory string) *coverageguidance.CrossCoverageSummary {
	var result coverageguidance.CrossCoverageSummary
	if err := persistence.ReadJSON(filepath.Join(directory, "cross-coverage-summary.json"), &result); err != nil {
		return nil
	}
	return &result
}

func writeCrossCoverageMatrix(path string, campaigns []coverageCampaignResult) error {
	var buffer bytes.Buffer
	writer := csv.NewWriter(&buffer)
	header := []string{
		"mode", "seed", "raw", "v2", "election", "replication", "snapshot", "recovery", "network",
		"election_network", "replication_network", "snapshot_recovery", "recovery_term_relation",
		"all_interactions", "corpus", "semantic_traces", "goal_a", "goal_b", "failures",
		"actions", "model_events", "elapsed_millis",
	}
	_ = writer.Write(header)
	for _, campaign := range campaigns {
		if campaign.CrossCoverage == nil {
			continue
		}
		cross := campaign.CrossCoverage
		row := []string{
			string(campaign.Mode), strconv.FormatInt(campaign.Seed, 10),
			strconv.Itoa(cross.RawDistinct), strconv.Itoa(cross.V2Distinct),
			strconv.Itoa(cross.FacetDistinct["election"]), strconv.Itoa(cross.FacetDistinct["replication"]),
			strconv.Itoa(cross.FacetDistinct["snapshot"]), strconv.Itoa(cross.FacetDistinct["recovery"]),
			strconv.Itoa(cross.FacetDistinct["network"]),
			strconv.Itoa(cross.InteractionDistinct["election_network"]),
			strconv.Itoa(cross.InteractionDistinct["replication_network"]),
			strconv.Itoa(cross.InteractionDistinct["snapshot_recovery"]),
			strconv.Itoa(cross.InteractionDistinct["recovery_term_relation"]),
			strconv.Itoa(cross.AllInteractionDistinct), strconv.Itoa(cross.CorpusSize),
			strconv.Itoa(cross.SemanticTraceCount), strconv.Itoa(cross.GoalAReached),
			strconv.Itoa(cross.GoalBReached), strconv.Itoa(cross.Failures),
			strconv.Itoa(campaign.Report.TotalActions), strconv.Itoa(campaign.Report.TotalModelEvents),
			strconv.FormatInt(campaign.Report.ElapsedMillis, 10),
		}
		_ = writer.Write(row)
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		return err
	}
	if err := os.WriteFile(path, buffer.Bytes(), 0o644); err != nil {
		return err
	}
	return nil
}

func decodeCoverageBenchmarkManifest(data []byte) (coverageBenchmarkManifest, error) {
	var manifest coverageBenchmarkManifest
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return coverageBenchmarkManifest{}, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return coverageBenchmarkManifest{}, fmt.Errorf("coverage benchmark manifest has trailing JSON")
		}
		return coverageBenchmarkManifest{}, err
	}
	return manifest, nil
}
