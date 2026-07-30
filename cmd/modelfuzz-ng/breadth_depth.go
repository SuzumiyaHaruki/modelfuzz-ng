package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"sort"
	"strconv"
	"time"

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/breadthdepth"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/core"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/corpus"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/coverageguidance"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/engine"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/goalsearch"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/persistence"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/protocolmutation"
	raftadvisor "github.com/SuzumiyaHaruki/modelfuzz-ng/internal/protocolmutation/raft"
)

const breadthDepthBenchmarkSchema = "raft-breadth-depth-benchmark-v1"
const breadthDepthArtifactRetentionSchema = "raft-breadth-depth-artifact-retention-v1"

type breadthDepthManifest struct {
	Schema                string                `json:"schema_version"`
	Name                  string                `json:"name"`
	Phase                 string                `json:"phase"`
	Config                string                `json:"config"`
	TLCAddress            string                `json:"tlc_address"`
	Methods               []breadthdepth.Method `json:"methods"`
	Goals                 []goalsearch.GoalID   `json:"goals"`
	Seeds                 []int64               `json:"seeds"`
	TotalCandidates       int                   `json:"total_candidate_budget"`
	GlobalCandidates      int                   `json:"global_candidate_budget"`
	LocalCandidates       int                   `json:"local_candidate_budget"`
	TotalActions          int                   `json:"total_action_budget"`
	GlobalActions         int                   `json:"global_action_budget"`
	LocalActions          int                   `json:"local_action_budget"`
	MaxPlanActions        int                   `json:"max_actions_per_plan"`
	HandoffTopK           int                   `json:"handoff_top_k"`
	HandoffDiversity      bool                  `json:"handoff_diversity"`
	HandoffFallback       bool                  `json:"handoff_fallback"`
	FixedEnergy           int                   `json:"fixed_energy"`
	CorpusLimit           int                   `json:"coverage_corpus_limit"`
	InitialPopulation     int                   `json:"initial_population"`
	MaxReadyCandidates    int                   `json:"max_ready_candidates"`
	SnapshotThreshold     uint64                `json:"snapshot_threshold"`
	RetainEntries         uint64                `json:"retain_entries"`
	LocalFrontierCapacity int                   `json:"local_frontier_capacity"`
	SaveAllRuns           bool                  `json:"save_all_runs"`
	ReplayVerify          bool                  `json:"replay_verify"`
	StopOnTarget          bool                  `json:"stop_on_target"`
}

type handoffReplayRecord struct {
	SchemaVersion          string `json:"schema_version"`
	GlobalCorpusID         string `json:"global_corpus_id"`
	Attempted              bool   `json:"attempted"`
	Succeeded              bool   `json:"succeeded"`
	Actions                int    `json:"replay_actions"`
	TraceEqual             bool   `json:"trace_equal"`
	ModelEventsEqual       bool   `json:"model_events_equal"`
	ObservationEqual       bool   `json:"full_observation_equal"`
	ObservationDigestEqual bool   `json:"observation_digest_equal"`
	GoalProgressEqual      bool   `json:"goal_progress_equal"`
	FacetEqual             bool   `json:"facet_equal"`
	StableKeyEqual         bool   `json:"stable_key_equal"`
	MessageIdentityEqual   bool   `json:"message_identity_equal"`
	Error                  string `json:"error,omitempty"`
}

type materializedHandoff struct {
	Candidates []breadthdepth.HandoffSeed
	Frontier   map[string]goalsearch.FrontierSeed
	Replays    []handoffReplayRecord
}

type breadthDepthCampaign struct {
	Method          breadthdepth.Method                    `json:"method"`
	Goal            goalsearch.GoalID                      `json:"goal_id"`
	Seed            int64                                  `json:"seed"`
	Directory       string                                 `json:"directory"`
	GlobalDirectory string                                 `json:"global_directory,omitempty"`
	Skipped         bool                                   `json:"skipped"`
	Combined        breadthdepth.CombinedSummary           `json:"combined"`
	GlobalCoverage  *coverageguidance.CrossCoverageSummary `json:"global_coverage,omitempty"`
	LocalReport     *goalSearchReport                      `json:"local_report,omitempty"`
}

type breadthDepthBenchmarkSummary struct {
	Schema    string                 `json:"schema_version"`
	Name      string                 `json:"name"`
	Phase     string                 `json:"phase"`
	Campaigns []breadthDepthCampaign `json:"campaigns"`
	Completed int                    `json:"completed"`
	Skipped   int                    `json:"skipped"`
	Failed    int                    `json:"failed"`
}

type breadthDepthArtifactRetention struct {
	SchemaVersion     string         `json:"schema_version"`
	RawPruned         bool           `json:"raw_pruned"`
	PrunedPaths       []string       `json:"pruned_paths"`
	ArchivePath       string         `json:"archive_path"`
	ArchiveSHA256     string         `json:"archive_sha256"`
	ArchiveValidation string         `json:"archive_validation"`
	CombinedStableKey string         `json:"combined_stable_key"`
	LocalStableKey    string         `json:"local_stable_key"`
	LocalCandidates   int            `json:"local_candidates"`
	TLCExecutedRuns   int            `json:"tlc_executed_runs"`
	RuntimeStatuses   map[string]int `json:"runtime_statuses"`
	FinalReportSHA256 string         `json:"final_report_sha256"`
	RecordedAt        string         `json:"recorded_at_utc"`
}

func breadthDepthBenchmarkCommand(
	ctx context.Context, args []string, stdout, stderr io.Writer,
) error {
	flags := flag.NewFlagSet("modelfuzz-ng breadth-depth-benchmark", flag.ContinueOnError)
	flags.SetOutput(stderr)
	manifestPath := flags.String("manifest", "", "frozen breadth/depth manifest")
	outputPath := flags.String("output", "", "isolated benchmark output root")
	resume := flags.Bool("resume", false, "resume incomplete global phases from checkpoints")
	skip := flags.Bool("skip-completed", true, "skip campaigns with a complete combined summary")
	reuseGlobalRoot := flags.String(
		"reuse-global-root", "",
		"read-only benchmark root whose matching _global phases are reused",
	)
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || *manifestPath == "" || *outputPath == "" {
		return fmt.Errorf("breadth-depth-benchmark requires -manifest and -output")
	}
	manifestAbsolute, err := filepath.Abs(*manifestPath)
	if err != nil {
		return err
	}
	manifest, err := readBreadthDepthManifest(manifestAbsolute)
	if err != nil {
		return err
	}
	if err := validateBreadthDepthManifest(manifest); err != nil {
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
	globalRoot := outputAbsolute
	if *reuseGlobalRoot != "" {
		globalRoot, err = filepath.Abs(*reuseGlobalRoot)
		if err != nil {
			return err
		}
		if globalRoot == outputAbsolute {
			return fmt.Errorf("reuse-global-root must differ from output")
		}
		provenance := map[string]any{
			"schema_version": "raft-breadth-depth-global-reuse-v1",
			"source_root":    globalRoot,
			"read_only":      true,
			"validation":     "method, seed, guidance mode, exact budget, and strict success",
		}
		if err := persistence.WriteJSONAtomic(
			filepath.Join(outputAbsolute, "global-reuse-provenance.json"), provenance); err != nil {
			return err
		}
	}
	if err := persistence.WriteJSONAtomic(filepath.Join(outputAbsolute, "manifest.json"), manifest); err != nil {
		return err
	}
	expandedConfig, err := loadCLIConfig(manifest.Config)
	if err != nil {
		return err
	}
	environment := map[string]any{
		"schema_version": breadthDepthBenchmarkSchema + "-environment",
		"go_version":     runtime.Version(), "goos": runtime.GOOS, "goarch": runtime.GOARCH,
		"command":  append([]string{"breadth-depth-benchmark"}, args...),
		"manifest": manifestAbsolute, "llm_calls": 0,
		"tlc_address":         manifest.TLCAddress,
		"model_config":        manifest.Config,
		"model_profile":       expandedConfig.Model.EffectiveProfile(),
		"model_node_ids":      expandedConfig.Model.NodeIDs,
		"model_max_log_index": expandedConfig.Model.MaxLogIndex,
		"model_largest_term":  expandedConfig.Model.LargestTerm,
		"snapshot_threshold":  manifest.SnapshotThreshold,
		"retain_entries":      manifest.RetainEntries,
		"generated_at":        time.Now().UTC().Format(time.RFC3339Nano),
	}
	for key, value := range breadthDepthBuildFingerprint() {
		environment[key] = value
	}
	if err := persistence.WriteJSONAtomic(filepath.Join(outputAbsolute, "environment.json"), environment); err != nil {
		return err
	}
	if err := persistence.WriteJSONAtomic(filepath.Join(outputAbsolute, "seed-manifest.json"), struct {
		Schema string  `json:"schema_version"`
		Seeds  []int64 `json:"seeds"`
	}{breadthDepthBenchmarkSchema + "-seeds", manifest.Seeds}); err != nil {
		return err
	}

	summary := breadthDepthBenchmarkSummary{
		Schema: breadthDepthBenchmarkSchema, Name: manifest.Name, Phase: manifest.Phase,
		Campaigns: make([]breadthDepthCampaign, 0,
			len(manifest.Methods)*len(manifest.Goals)*len(manifest.Seeds)),
	}
	for _, method := range manifest.Methods {
		for _, seed := range manifest.Seeds {
			globalDirectory := ""
			if method != breadthdepth.MethodLocalOnly {
				globalDirectory = filepath.Join(
					globalRoot, "_global", string(method), fmt.Sprintf("seed-%d", seed),
				)
				if err := ensureGlobalPhase(
					ctx, manifest, method, seed, globalDirectory, *resume,
					globalRoot != outputAbsolute, stdout, stderr,
				); err != nil {
					summary.Failed++
					_ = persistence.WriteJSONAtomic(
						filepath.Join(outputAbsolute, "benchmark-summary.json"), summary)
					return fmt.Errorf("%s seed %d global phase: %w", method, seed, err)
				}
			}
			for _, goalID := range manifest.Goals {
				if err := ctx.Err(); err != nil {
					return err
				}
				directory := filepath.Join(
					outputAbsolute, string(method), string(goalID), fmt.Sprintf("seed-%d", seed),
				)
				campaign := breadthDepthCampaign{
					Method: method, Goal: goalID, Seed: seed, Directory: directory,
					GlobalDirectory: globalDirectory,
				}
				if *skip && completedBreadthDepthCampaign(
					directory, globalDirectory, method, goalID, seed) {
					campaign.Skipped = true
					_ = persistence.ReadJSON(filepath.Join(directory, "combined-summary.json"), &campaign.Combined)
					if err := normalizeBreadthDepthSummary(directory, &campaign.Combined); err != nil {
						return err
					}
					campaign.GlobalCoverage = readCrossCoverage(globalDirectory)
					var local goalSearchReport
					if persistence.ReadJSON(filepath.Join(directory, "local", "final-report.json"), &local) == nil {
						campaign.LocalReport = &local
					}
					summary.Skipped++
					summary.Completed++
					summary.Campaigns = append(summary.Campaigns, campaign)
					continue
				}
				if _, statErr := os.Stat(directory); statErr == nil {
					return fmt.Errorf("incomplete campaign directory already exists: %s", directory)
				}
				completed, campaignErr := executeBreadthDepthCampaign(
					ctx, manifest, method, goalID, seed, directory, globalDirectory, stderr,
				)
				if campaignErr != nil {
					summary.Failed++
					summary.Campaigns = append(summary.Campaigns, campaign)
					_ = persistence.WriteJSONAtomic(
						filepath.Join(outputAbsolute, "benchmark-summary.json"), summary)
					return fmt.Errorf("%s %s seed %d: %w", method, goalID, seed, campaignErr)
				}
				campaign.Combined = completed
				campaign.GlobalCoverage = readCrossCoverage(globalDirectory)
				var local goalSearchReport
				if persistence.ReadJSON(filepath.Join(directory, "local", "final-report.json"), &local) == nil {
					campaign.LocalReport = &local
				}
				summary.Completed++
				summary.Campaigns = append(summary.Campaigns, campaign)
				if err := persistence.WriteJSONAtomic(
					filepath.Join(outputAbsolute, "benchmark-summary.json"), summary); err != nil {
					return err
				}
			}
		}
	}
	sort.Slice(summary.Campaigns, func(i, j int) bool {
		if summary.Campaigns[i].Method != summary.Campaigns[j].Method {
			return summary.Campaigns[i].Method < summary.Campaigns[j].Method
		}
		if summary.Campaigns[i].Goal != summary.Campaigns[j].Goal {
			return summary.Campaigns[i].Goal < summary.Campaigns[j].Goal
		}
		return summary.Campaigns[i].Seed < summary.Campaigns[j].Seed
	})
	if err := persistence.WriteJSONAtomic(filepath.Join(outputAbsolute, "benchmark-summary.json"), summary); err != nil {
		return err
	}
	if err := writeBreadthDepthTables(outputAbsolute, manifest, summary); err != nil {
		return err
	}
	_, err = fmt.Fprintf(stdout,
		"breadth/depth benchmark complete: phase=%s campaigns=%d skipped=%d output=%s\n",
		manifest.Phase, summary.Completed, summary.Skipped, outputAbsolute)
	return err
}

func breadthDepthBuildFingerprint() map[string]any {
	fingerprint := map[string]any{
		"vcs_revision":              "unknown",
		"vcs_modified":              "unknown",
		"etcd_raft_module":          "go.etcd.io/raft/v3",
		"etcd_raft_version":         "unknown",
		"etcd_raft_replace_path":    "",
		"etcd_raft_replace_version": "",
	}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return fingerprint
	}
	fingerprint["main_module"] = info.Main.Path
	fingerprint["main_module_version"] = info.Main.Version
	for _, setting := range info.Settings {
		switch setting.Key {
		case "vcs.revision":
			fingerprint["vcs_revision"] = setting.Value
		case "vcs.modified":
			fingerprint["vcs_modified"] = setting.Value
		}
	}
	for _, dependency := range info.Deps {
		if dependency.Path != "go.etcd.io/raft/v3" {
			continue
		}
		fingerprint["etcd_raft_version"] = dependency.Version
		if dependency.Replace != nil {
			fingerprint["etcd_raft_replace_path"] = dependency.Replace.Path
			fingerprint["etcd_raft_replace_version"] = dependency.Replace.Version
		}
		break
	}
	return fingerprint
}

func readBreadthDepthManifest(path string) (breadthDepthManifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return breadthDepthManifest{}, fmt.Errorf("read breadth/depth manifest: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var manifest breadthDepthManifest
	if err := decoder.Decode(&manifest); err != nil {
		return breadthDepthManifest{}, fmt.Errorf("decode breadth/depth manifest: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return breadthDepthManifest{}, fmt.Errorf("breadth/depth manifest has trailing JSON")
	}
	return manifest, nil
}

func validateBreadthDepthManifest(manifest breadthDepthManifest) error {
	if manifest.Schema != breadthDepthBenchmarkSchema {
		return fmt.Errorf("unsupported breadth/depth schema %q", manifest.Schema)
	}
	if manifest.Name == "" || manifest.Phase == "" || manifest.Config == "" ||
		len(manifest.Methods) == 0 || len(manifest.Goals) == 0 || len(manifest.Seeds) == 0 {
		return fmt.Errorf("breadth/depth manifest lacks name, phase, config, methods, goals, or seeds")
	}
	base := breadthdepth.Budget{
		TotalCandidates:  manifest.TotalCandidates,
		GlobalCandidates: manifest.GlobalCandidates, LocalCandidates: manifest.LocalCandidates,
		TotalActions:  manifest.TotalActions,
		GlobalActions: manifest.GlobalActions, LocalActions: manifest.LocalActions,
		MaxPlanActions: manifest.MaxPlanActions,
	}
	if err := base.Validate(); err != nil {
		return err
	}
	if manifest.HandoffTopK <= 0 || manifest.FixedEnergy <= 0 ||
		manifest.CorpusLimit <= 0 || manifest.InitialPopulation <= 0 ||
		manifest.MaxReadyCandidates <= 0 || manifest.LocalFrontierCapacity <= 0 {
		return fmt.Errorf("handoff, guidance, corpus, queue, and local Frontier settings must be positive")
	}
	if !manifest.HandoffDiversity {
		return fmt.Errorf("v1 requires handoff_diversity=true; false is not silently defaulted")
	}
	seenMethods := make(map[breadthdepth.Method]struct{})
	for _, method := range manifest.Methods {
		if err := method.Validate(); err != nil {
			return err
		}
		if _, duplicate := seenMethods[method]; duplicate {
			return fmt.Errorf("duplicate method %s", method)
		}
		seenMethods[method] = struct{}{}
		if err := budgetForMethod(manifest, method).Validate(); err != nil {
			return fmt.Errorf("%s budget: %w", method, err)
		}
	}
	seenGoals := make(map[goalsearch.GoalID]struct{})
	for _, goalID := range manifest.Goals {
		if _, err := goalsearch.Definition(goalID, 3); err != nil {
			return err
		}
		if _, duplicate := seenGoals[goalID]; duplicate {
			return fmt.Errorf("duplicate goal %s", goalID)
		}
		seenGoals[goalID] = struct{}{}
	}
	seenSeeds := make(map[int64]struct{})
	for _, seed := range manifest.Seeds {
		if _, duplicate := seenSeeds[seed]; duplicate {
			return fmt.Errorf("duplicate seed %d", seed)
		}
		seenSeeds[seed] = struct{}{}
	}
	return nil
}

func budgetForMethod(manifest breadthDepthManifest, method breadthdepth.Method) breadthdepth.Budget {
	budget := breadthdepth.Budget{
		TotalCandidates:  manifest.TotalCandidates,
		GlobalCandidates: manifest.GlobalCandidates, LocalCandidates: manifest.LocalCandidates,
		TotalActions:  manifest.TotalActions,
		GlobalActions: manifest.GlobalActions, LocalActions: manifest.LocalActions,
		MaxPlanActions: manifest.MaxPlanActions,
	}
	switch method {
	case breadthdepth.MethodFacetOnly:
		budget.GlobalCandidates, budget.LocalCandidates = budget.TotalCandidates, 0
		budget.GlobalActions, budget.LocalActions = budget.TotalActions, 0
	case breadthdepth.MethodLocalOnly:
		budget.GlobalCandidates, budget.LocalCandidates = 0, budget.TotalCandidates
		budget.GlobalActions, budget.LocalActions = 0, budget.TotalActions
	}
	return budget
}

func guidanceForMethod(method breadthdepth.Method) (coverageguidance.Mode, error) {
	switch method {
	case breadthdepth.MethodFacetOnly, breadthdepth.MethodFacetThen:
		return coverageguidance.ModeFacetFixed, nil
	case breadthdepth.MethodRandomThen:
		return coverageguidance.ModeRandom, nil
	case breadthdepth.MethodRawThen:
		return coverageguidance.ModeRawFixed, nil
	case breadthdepth.MethodV2Then:
		return coverageguidance.ModeV2Fixed, nil
	default:
		return "", fmt.Errorf("%s has no global guidance", method)
	}
}

func ensureGlobalPhase(
	ctx context.Context,
	manifest breadthDepthManifest,
	method breadthdepth.Method,
	seed int64,
	directory string,
	resume bool,
	readOnly bool,
	stdout, stderr io.Writer,
) error {
	budget := budgetForMethod(manifest, method)
	mode, err := guidanceForMethod(method)
	if err != nil {
		return err
	}
	if report := readExperimentReport(directory); report.CompletedRuns == budget.GlobalCandidates &&
		report.Succeeded == budget.GlobalCandidates && report.Failed == 0 {
		var phase breadthdepth.GlobalPhaseResult
		if cross := readCrossCoverage(directory); cross != nil && cross.Mode == mode &&
			persistence.ReadJSON(
				filepath.Join(directory, "global-phase-summary.json"), &phase) == nil &&
			phase.SchemaVersion == breadthdepth.SchemaVersion &&
			phase.GuidanceMode == mode && phase.Seed == seed &&
			phase.CandidateBudget == budget.GlobalCandidates &&
			phase.ActionBudget == budget.GlobalActions && phase.Frozen {
			return nil
		}
	}
	if readOnly {
		return fmt.Errorf("reused global phase failed frozen compatibility validation: %s", directory)
	}
	coverageManifest := coverageBenchmarkManifest{
		Schema: coverageBenchmarkSchema, Name: manifest.Name, Phase: manifest.Phase,
		Config: manifest.Config, TLCAddress: manifest.TLCAddress,
		Runs: budget.GlobalCandidates, MaxPlanActions: budget.MaxPlanActions,
		InitialPopulation: manifest.InitialPopulation, Parallelism: 1,
		FixedEnergy: manifest.FixedEnergy, FixedParentSelection: "admission-fifo-once",
		CoverageCorpusLimit: manifest.CorpusLimit,
		MaxReadyCandidates:  manifest.MaxReadyCandidates, ArtifactPolicy: "all",
		RecordAllCoverageMetrics: true, OfflineGoalEvaluation: true,
		SnapshotThreshold:     manifest.SnapshotThreshold,
		SnapshotRetainEntries: manifest.RetainEntries,
	}
	var arguments []string
	if _, statErr := os.Stat(filepath.Join(directory, "checkpoint.json")); statErr == nil {
		if !resume {
			return fmt.Errorf("global checkpoint exists but -resume=false: %s", directory)
		}
		arguments = []string{"-resume", directory}
	} else {
		arguments = coverageExperimentArguments(coverageManifest, mode, seed, directory)
	}
	var output bytes.Buffer
	err = experimentCommand(ctx, arguments, &output, stderr)
	if output.Len() > 0 {
		_, _ = io.Copy(stdout, &output)
	}
	if err != nil {
		return err
	}
	report := readExperimentReport(directory)
	if report.CompletedRuns != budget.GlobalCandidates {
		return fmt.Errorf("global candidates=%d want %d", report.CompletedRuns, budget.GlobalCandidates)
	}
	if report.Succeeded != budget.GlobalCandidates || report.Failed != 0 {
		return fmt.Errorf(
			"strict global execution incomplete: succeeded=%d failed=%d want=%d/0",
			report.Succeeded, report.Failed, budget.GlobalCandidates)
	}
	if report.TotalActions > budget.GlobalActions {
		return fmt.Errorf("global actions=%d exceed cap %d", report.TotalActions, budget.GlobalActions)
	}
	return writeGlobalPhaseArtifacts(directory, mode, seed, budget)
}

func writeGlobalPhaseArtifacts(
	directory string, mode coverageguidance.Mode, seed int64, budget breadthdepth.Budget,
) error {
	report := readExperimentReport(directory)
	cross := readCrossCoverage(directory)
	if cross == nil {
		return fmt.Errorf("missing cross coverage in %s", directory)
	}
	observations, err := persistence.ReadJSONLines[coverageguidance.CoverageObservation](
		filepath.Join(directory, "coverage-observations.jsonl"), report.CompletedRuns)
	if err != nil {
		return err
	}
	coverage := coverageguidance.CoverageCounts{
		Raw: cross.RawDistinct, V2: cross.V2Distinct,
		Facets: cloneIntMap(cross.FacetDistinct), Interactions: cloneIntMap(cross.InteractionDistinct),
	}
	summary := breadthdepth.GlobalPhaseResult{
		SchemaVersion: breadthdepth.SchemaVersion, GuidanceMode: mode, Seed: seed,
		CandidateBudget: budget.GlobalCandidates, ActionBudget: budget.GlobalActions,
		Candidates: report.CompletedRuns, Actions: report.TotalActions,
		CorpusEntries: cross.CorpusSize, Frozen: true, Coverage: coverage,
		SemanticTraces: cross.SemanticTraceCount,
	}
	summary.StableKey = breadthDepthStableKey(summary)
	if err := persistence.WriteJSONAtomic(filepath.Join(directory, "global-phase-summary.json"), summary); err != nil {
		return err
	}
	manifest := struct {
		SchemaVersion string   `json:"schema_version"`
		Frozen        bool     `json:"frozen"`
		CorpusSize    int      `json:"corpus_size"`
		CorpusIDs     []string `json:"corpus_ids"`
	}{
		SchemaVersion: breadthdepth.SchemaVersion, Frozen: true, CorpusSize: cross.CorpusSize,
	}
	entries, err := persistence.ReadJSONLines[corpus.Entry](
		filepath.Join(directory, "corpus.jsonl"), cross.CorpusSize)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		manifest.CorpusIDs = append(manifest.CorpusIDs, entry.ID)
	}
	if err := persistence.WriteJSONAtomic(filepath.Join(directory, "global-corpus-manifest.json"), manifest); err != nil {
		return err
	}
	growthRows := [][]string{{"candidate", "actions", "raw", "v2", "facets", "interactions"}}
	raw, v2 := make(map[int64]struct{}), make(map[int64]struct{})
	facets, interactions := make(map[int64]struct{}), make(map[int64]struct{})
	actions := 0
	for index, observation := range observations {
		actions += observation.ActionCount
		addCoverageValues(raw, observation.RawTLCFingerprints)
		addCoverageValues(v2, observation.V2StateKeys)
		for _, values := range observation.FacetKeys {
			addCoverageValues(facets, values)
		}
		for _, values := range observation.InteractionKeys {
			addCoverageValues(interactions, values)
		}
		growthRows = append(growthRows, []string{
			strconv.Itoa(index + 1), strconv.Itoa(actions), strconv.Itoa(len(raw)),
			strconv.Itoa(len(v2)), strconv.Itoa(len(facets)), strconv.Itoa(len(interactions)),
		})
	}
	return writeCSVRows(filepath.Join(directory, "coverage-growth-global.csv"), growthRows)
}

func executeBreadthDepthCampaign(
	ctx context.Context,
	manifest breadthDepthManifest,
	method breadthdepth.Method,
	goalID goalsearch.GoalID,
	seed int64,
	directory, globalDirectory string,
	stderr io.Writer,
) (breadthdepth.CombinedSummary, error) {
	if err := createOutputDirectory(directory); err != nil {
		return breadthdepth.CombinedSummary{}, err
	}
	budget := budgetForMethod(manifest, method)
	run := breadthdepth.BreadthDepthRun{
		SchemaVersion: breadthdepth.SchemaVersion, Method: method, GoalID: string(goalID),
		Seed: seed, Budget: budget, HandoffTopK: manifest.HandoffTopK, LLMCalls: 0,
	}
	settings := map[string]any{
		"schema_version": breadthdepth.SchemaVersion, "run": run,
		"global_phase_directory": globalDirectory,
		"global_guidance_mode": func() string {
			mode, _ := guidanceForMethod(method)
			return string(mode)
		}(),
		"global_focused_advisor": false, "global_active_goal": false,
		"local_goal": goalID, "local_mutation_advisor": "raft-focused",
		"local_frontier_mode": "standard", "local_frontier_capacity": manifest.LocalFrontierCapacity,
		"record_all_coverage": true, "offline_goal_evaluation": true,
		"branch_evidence": false, "llm_calls": 0,
	}
	if err := persistence.WriteJSONAtomic(filepath.Join(directory, "breadth-depth-settings.json"), settings); err != nil {
		return breadthdepth.CombinedSummary{}, err
	}
	var global *breadthdepth.GlobalPhaseResult
	var handoff *breadthdepth.HandoffSet
	var handoffMaterialized *materializedHandoff
	var bootstrap *goalSearchBootstrap
	config, err := loadCLIConfig(manifest.Config)
	if err != nil {
		return breadthdepth.CombinedSummary{}, err
	}
	config.TLC.Address = manifest.TLCAddress
	config.Raft.Snapshot.Threshold = manifest.SnapshotThreshold
	config.Raft.Snapshot.RetainEntries = manifest.RetainEntries
	config.Engine.MaxPlanActions = manifest.MaxPlanActions
	if method != breadthdepth.MethodLocalOnly {
		var loaded breadthdepth.GlobalPhaseResult
		if err := persistence.ReadJSON(filepath.Join(globalDirectory, "global-phase-summary.json"), &loaded); err != nil {
			return breadthdepth.CombinedSummary{}, err
		}
		global = &loaded
		materialized, err := buildHandoffCandidates(
			ctx, globalDirectory, config, goalID, manifest.ReplayVerify, stderr,
		)
		if err != nil {
			return breadthdepth.CombinedSummary{}, err
		}
		handoffMaterialized = &materialized
		selected, err := breadthdepth.SelectHandoff(
			string(goalID), materialized.Candidates, manifest.HandoffTopK,
		)
		if err != nil {
			return breadthdepth.CombinedSummary{}, err
		}
		if len(selected.Selected) == 0 {
			if !manifest.HandoffFallback && method != breadthdepth.MethodFacetOnly {
				return breadthdepth.CombinedSummary{}, fmt.Errorf("handoff-empty and fallback disabled")
			}
			selected.Fallback = method != breadthdepth.MethodFacetOnly
			selected.FallbackReason = "handoff-empty: no replayable entry-condition seed"
		}
		selected.StableKey = ""
		selected.StableKey = breadthDepthStableKey(selected)
		handoff = &selected
		if err := writeHandoffArtifacts(directory, materialized, selected); err != nil {
			return breadthdepth.CombinedSummary{}, err
		}
		if method != breadthdepth.MethodFacetOnly && len(selected.Selected) > 0 {
			bootstrap = &goalSearchBootstrap{}
			for _, chosen := range selected.Selected {
				seed, exists := materialized.Frontier[chosen.GlobalCorpusID]
				if !exists {
					return breadthdepth.CombinedSummary{}, fmt.Errorf(
						"selected handoff %s lacks Frontier seed", chosen.GlobalCorpusID)
				}
				bootstrap.Seeds = append(bootstrap.Seeds, seed)
			}
		}
	}
	var local *breadthdepth.LocalPhaseResult
	var localReport *goalSearchReport
	if method != breadthdepth.MethodFacetOnly {
		report, err := runBreadthDepthLocal(
			ctx, filepath.Join(directory, "local"), manifest, goalID, seed,
			budget, bootstrap, stderr,
		)
		if err != nil {
			return breadthdepth.CombinedSummary{}, err
		}
		localReport = &report
		local = &breadthdepth.LocalPhaseResult{
			SchemaVersion: breadthdepth.SchemaVersion, GoalID: string(goalID),
			CandidateBudget: budget.LocalCandidates, ActionBudget: budget.LocalActions,
			Candidates: report.Candidates, Actions: report.Actions,
			TargetReached:    report.TargetReached,
			BudgetExhausted:  !report.TargetReached,
			ContributingSeed: report.ContributingHandoffSeedID,
		}
		local.StableKey = breadthDepthStableKey(local)
		if err := persistence.WriteJSONAtomic(filepath.Join(directory, "local-phase-summary.json"), local); err != nil {
			return breadthdepth.CombinedSummary{}, err
		}
	}
	combined := breadthdepth.CombinedSummary{
		SchemaVersion: breadthdepth.SchemaVersion, Run: run,
		Global: global, Handoff: handoff, Local: local, MinimumDistance: 99,
	}
	if handoffMaterialized != nil {
		for _, candidate := range handoffMaterialized.Candidates {
			combined.DeepestWaypoint = max(combined.DeepestWaypoint, candidate.Progress.Completed)
			combined.MinimumDistance = min(combined.MinimumDistance, candidate.Progress.Distance)
			combined.GoalReached = combined.GoalReached || candidate.Progress.TargetReached
		}
	}
	if localReport != nil {
		combined.GoalReached = combined.GoalReached || localReport.TargetReached
		localDepth := 0
		for _, waypoint := range localReport.Waypoints {
			if waypoint.Reached {
				localDepth++
			}
		}
		combined.DeepestWaypoint = max(combined.DeepestWaypoint, localDepth)
		for _, frontierSeed := range localReport.Frontier.Seeds {
			combined.MinimumDistance = min(
				combined.MinimumDistance, frontierSeed.Progress.DistanceToCurrent)
		}
	}
	if combined.MinimumDistance == 99 {
		combined.MinimumDistance = 0
	}
	switch {
	case localReport != nil:
		combined.BudgetExhausted = !combined.GoalReached
	case global != nil:
		combined.BudgetExhausted = !combined.GoalReached &&
			(global.Candidates >= budget.GlobalCandidates ||
				global.Actions >= budget.GlobalActions)
	}
	if global != nil {
		combined.FinalCandidates += global.Candidates
		combined.FinalActions += global.Actions
	}
	if local != nil {
		combined.FinalCandidates += local.Candidates
		combined.FinalActions += local.Actions
	}
	finalCoverage, localNewCoverage, globalRetained, err := collectCampaignCoverage(
		directory, globalDirectory, filepath.Join(directory, "local"), config, localReport,
	)
	if err != nil {
		return combined, err
	}
	combined.FinalCoverage = finalCoverage
	combined.LocalNewCoverage = localNewCoverage
	combined.GlobalCoverageRetained = globalRetained
	combined.BudgetValid = combined.FinalCandidates <= budget.TotalCandidates &&
		combined.FinalActions <= budget.TotalActions
	if !combined.BudgetValid {
		return combined, fmt.Errorf(
			"combined budget exceeded: candidates=%d/%d actions=%d/%d",
			combined.FinalCandidates, budget.TotalCandidates,
			combined.FinalActions, budget.TotalActions,
		)
	}
	combined.StableKey = breadthDepthStableKey(combined)
	if err := persistence.WriteJSONAtomic(filepath.Join(directory, "combined-summary.json"), combined); err != nil {
		return combined, err
	}
	return combined, nil
}

func runBreadthDepthLocal(
	ctx context.Context,
	output string,
	manifest breadthDepthManifest,
	goalID goalsearch.GoalID,
	seed int64,
	budget breadthdepth.Budget,
	bootstrap *goalSearchBootstrap,
	stderr io.Writer,
) (goalSearchReport, error) {
	config, err := loadCLIConfig(manifest.Config)
	if err != nil {
		return goalSearchReport{}, err
	}
	definition, err := goalsearch.Definition(goalID, len(config.Raft.NodeIDs))
	if err != nil {
		return goalSearchReport{}, err
	}
	config.Seed = seed
	config.ExecutionID = core.ExecutionID(fmt.Sprintf("breadth-depth-%s-%d", goalID, seed))
	config.Engine.MaxPlanActions = manifest.MaxPlanActions
	config.Raft.Snapshot.Threshold = manifest.SnapshotThreshold
	config.Raft.Snapshot.RetainEntries = manifest.RetainEntries
	config.TLC.Address = manifest.TLCAddress
	if err := validateAlignedNodes(config.Raft.NodeIDs, config.Model.NodeIDs); err != nil {
		return goalSearchReport{}, err
	}
	if err := validateTLCModelBounds(ctx, config, stderr); err != nil {
		return goalSearchReport{}, err
	}
	settings := goalSearchSettings{
		SchemaVersion: goalsearch.SchemaVersion, ReleaseVersion: releaseVersion,
		GoalID: goalID, Mode: goalsearch.ModeFrontier, NodeCount: len(config.Raft.NodeIDs),
		Seed: seed, CandidateBudget: budget.LocalCandidates, ActionBudget: budget.LocalActions,
		MaxActionsPerPlan: manifest.MaxPlanActions, PerWaypointBudget: budget.LocalCandidates,
		FrontierTopK: 1, TotalFrontierCapacity: manifest.LocalFrontierCapacity,
		PerBranchMinimum: 1, HintStrength: goalsearch.HintWeak,
		DistanceMode: goalsearch.DistanceStaged, StrictTLC: true,
		TLCAddress: config.TLC.Address, GoalAwareMutation: true, PrefixPreservation: true,
		SaveAllRuns: manifest.SaveAllRuns, SnapshotThreshold: manifest.SnapshotThreshold,
		RetainEntries: manifest.RetainEntries, CrashQuota: 2, PartitionEnabled: true,
		Workers: 1, ReplayVerify: manifest.ReplayVerify,
		StopOnTarget: manifest.StopOnTarget, StopOnFailure: false,
		Subject: goalSubject(config), Config: config, CheckpointResume: false, LLMCalls: 0,
		MutationAdvisor: "raft-focused", FocusedGoalA: true, FocusedGoalB: true,
		AdvisorPriorityMultiplier: 16, AdvisorLocalActionCap: 9,
		AdvisorNoProgressCap: 8, AdvisorQueueLimit: 64,
		AdvisorAblation:     raftadvisor.AblationNone,
		BranchEvidenceMode:  goalsearch.BranchEvidenceOff,
		BranchBudgetMode:    goalsearch.BranchBudgetRoundRobin,
		MicroProgressPolicy: goalsearch.MicroProgressOff,
	}
	if err := createOutputDirectory(output); err != nil {
		return goalSearchReport{}, err
	}
	initialArtifacts := []struct {
		name  string
		value any
	}{
		{"goal-definition.json", definition},
		{"goal-settings.json", settings},
		{"branch-catalog.json", goalsearch.BranchCatalog()},
		{"branch-evidence-catalog.json", goalsearch.BranchEvidenceCatalog()},
		{"micro-progress-registry.json", goalsearch.MicroProgressRegistry()},
		{"mutation-advisor-settings.json", map[string]any{
			"schema_version": protocolmutation.SchemaVersion, "mode": "raft-focused",
			"record_only": false, "branch_evidence_record_only": false,
		}},
	}
	for _, artifact := range initialArtifacts {
		if err := persistence.WriteJSONAtomic(filepath.Join(output, artifact.name), artifact.value); err != nil {
			return goalSearchReport{}, err
		}
	}
	report, runErr := executeGoalSearchWithBootstrap(
		ctx, output, config, definition, settings, bootstrap, stderr,
	)
	if report.TLCExecutedRuns != report.Candidates {
		runErr = errors.Join(runErr, fmt.Errorf(
			"strict local TLC execution incomplete: executed=%d candidates=%d",
			report.TLCExecutedRuns, report.Candidates))
	}
	for status, count := range report.RuntimeStatuses {
		if status != engine.StatusCompleted && count > 0 {
			runErr = errors.Join(runErr, fmt.Errorf(
				"strict local runtime status %s=%d", status, count))
		}
	}
	writeErr := persistence.WriteJSONAtomic(filepath.Join(output, "final-report.json"), report)
	return report, errors.Join(runErr, writeErr)
}

func completedBreadthDepthCampaign(
	directory, globalDirectory string,
	method breadthdepth.Method,
	goalID goalsearch.GoalID,
	seed int64,
) bool {
	var summary breadthdepth.CombinedSummary
	if persistence.ReadJSON(filepath.Join(directory, "combined-summary.json"), &summary) != nil {
		return false
	}
	if summary.SchemaVersion != breadthdepth.SchemaVersion ||
		summary.Run.Method != method || summary.Run.GoalID != string(goalID) ||
		summary.Run.Seed != seed || !summary.BudgetValid {
		return false
	}
	if summary.Global != nil {
		report := readExperimentReport(globalDirectory)
		if report.CompletedRuns != summary.Global.Candidates ||
			report.Succeeded != summary.Global.Candidates || report.Failed != 0 {
			return false
		}
	}
	if summary.Local != nil {
		var report goalSearchReport
		if err := persistence.ReadJSON(
			filepath.Join(directory, "local", "final-report.json"), &report); err != nil {
			if !completedPrunedLocalPhase(directory, summary) {
				return false
			}
		} else {
			if report.TLCExecutedRuns != report.Candidates {
				return false
			}
			for status, count := range report.RuntimeStatuses {
				if status != engine.StatusCompleted && count > 0 {
					return false
				}
			}
		}
	}
	return true
}

func completedPrunedLocalPhase(
	directory string,
	summary breadthdepth.CombinedSummary,
) bool {
	if summary.Local == nil {
		return false
	}
	var retention breadthDepthArtifactRetention
	if persistence.ReadJSON(
		filepath.Join(directory, "artifact-retention.json"), &retention) != nil {
		return false
	}
	if retention.SchemaVersion != breadthDepthArtifactRetentionSchema ||
		!retention.RawPruned || len(retention.PrunedPaths) == 0 ||
		retention.ArchivePath == "" || len(retention.ArchiveSHA256) != 64 ||
		retention.ArchiveValidation != "zstd+tar+sha256-passed" ||
		retention.CombinedStableKey != summary.StableKey ||
		retention.LocalStableKey != summary.Local.StableKey ||
		retention.LocalCandidates != summary.Local.Candidates ||
		retention.TLCExecutedRuns != retention.LocalCandidates ||
		len(retention.FinalReportSHA256) != 64 {
		return false
	}
	for status, count := range retention.RuntimeStatuses {
		if engine.Status(status) != engine.StatusCompleted && count > 0 {
			return false
		}
	}
	return true
}

func normalizeBreadthDepthSummary(
	directory string,
	summary *breadthdepth.CombinedSummary,
) error {
	if summary == nil || summary.Local == nil || summary.GoalReached ||
		(summary.BudgetExhausted && summary.Local.BudgetExhausted) {
		return nil
	}
	summary.BudgetExhausted = true
	summary.Local.BudgetExhausted = true
	summary.Local.StableKey = ""
	summary.Local.StableKey = breadthDepthStableKey(summary.Local)
	if err := persistence.WriteJSONAtomic(
		filepath.Join(directory, "local-phase-summary.json"), summary.Local); err != nil {
		return err
	}
	summary.StableKey = ""
	summary.StableKey = breadthDepthStableKey(summary)
	return persistence.WriteJSONAtomic(
		filepath.Join(directory, "combined-summary.json"), summary)
}

func breadthDepthStableKey(value any) string {
	encoded, _ := json.Marshal(value)
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}

func cloneIntMap(input map[string]int) map[string]int {
	output := make(map[string]int, len(input))
	for key, value := range input {
		output[key] = value
	}
	return output
}

func addCoverageValues(set map[int64]struct{}, values []coverageguidance.CoverageValue) {
	for _, value := range values {
		set[value.Key] = struct{}{}
	}
}

func writeCSVRows(path string, rows [][]string) error {
	var buffer bytes.Buffer
	writer := csv.NewWriter(&buffer)
	for _, row := range rows {
		if err := writer.Write(row); err != nil {
			return err
		}
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		return err
	}
	return os.WriteFile(path, buffer.Bytes(), 0o644)
}

func equalJSON(left, right any) bool {
	leftData, leftErr := json.Marshal(left)
	rightData, rightErr := json.Marshal(right)
	return leftErr == nil && rightErr == nil && bytes.Equal(leftData, rightData)
}

func facetCombinationKey(observation coverageguidance.CoverageObservation) string {
	values := make([]string, 0)
	names := make([]string, 0, len(observation.FacetKeys))
	for name := range observation.FacetKeys {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		for _, value := range observation.FacetKeys[name] {
			values = append(values, name+":"+strconv.FormatInt(value.Key, 10))
		}
	}
	sort.Strings(values)
	return goalsearch.StringSequenceKey(values)
}

func writeHandoffArtifacts(
	directory string, materialized materializedHandoff, selected breadthdepth.HandoffSet,
) error {
	settings := map[string]any{
		"schema_version": breadthdepth.SchemaVersion, "top_k": selected.TopK,
		"ordering": []string{
			"completed_waypoint_count_desc", "staged_distance_asc",
			"relative_semantic_trace_diversity", "facet_combination_diversity",
			"queue_shape_diversity", "binding_role_diversity",
			"plan_prefix_length_asc", "stable_key_asc",
		},
		"identity_exclusions": []string{
			"node_id", "message_id", "absolute_term", "absolute_index", "plan_hash_as_diversity",
		},
	}
	if err := persistence.WriteJSONAtomic(filepath.Join(directory, "handoff-settings.json"), settings); err != nil {
		return err
	}
	if err := writeJSONLines(filepath.Join(directory, "handoff-candidates.jsonl"), materialized.Candidates); err != nil {
		return err
	}
	if err := persistence.WriteJSONAtomic(filepath.Join(directory, "handoff-selected.json"), selected); err != nil {
		return err
	}
	return writeJSONLines(filepath.Join(directory, "handoff-replay.jsonl"), materialized.Replays)
}

func writeJSONLines[T any](path string, values []T) error {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	for _, value := range values {
		if err := encoder.Encode(value); err != nil {
			return err
		}
	}
	return os.WriteFile(path, buffer.Bytes(), 0o644)
}

func readJSONLines[T any](path string, target *[]T) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	for {
		var value T
		if err := decoder.Decode(&value); err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
		*target = append(*target, value)
	}
}
