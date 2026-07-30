package main

import (
	"bytes"
	"context"
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
	"strings"
	"time"

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/goalsearch"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/persistence"
	raftadvisor "github.com/SuzumiyaHaruki/modelfuzz-ng/internal/protocolmutation/raft"
)

const goalBenchmarkSchema = "raft-goal-benchmark-v1"

type goalBenchmarkManifest struct {
	SchemaVersion string                  `json:"schema_version"`
	Defaults      *goalBenchmarkCampaign  `json:"defaults,omitempty"`
	Campaigns     []goalBenchmarkCampaign `json:"campaigns"`
}

type goalBenchmarkCampaign struct {
	ID                          string                             `json:"id"`
	GoalID                      goalsearch.GoalID                  `json:"goal_id"`
	Method                      goalsearch.SearchMode              `json:"method"`
	HintStrength                goalsearch.HintStrength            `json:"hint_strength"`
	FrontierTopK                int                                `json:"frontier_top_k"`
	TotalFrontierCapacity       int                                `json:"total_frontier_capacity,omitempty"`
	PerBranchMinimum            int                                `json:"per_branch_minimum_capacity,omitempty"`
	BranchTemplateIDs           []goalsearch.BranchTemplateID      `json:"branch_template_ids,omitempty"`
	AllFeasibleBranches         bool                               `json:"all_feasible_branches"`
	BranchAwareness             goalsearch.BranchAwareness         `json:"branch_awareness,omitempty"`
	BranchDimensionAblation     goalsearch.BranchDimensionAblation `json:"branch_dimension_ablation,omitempty"`
	BranchBudgetAllocation      string                             `json:"branch_budget_allocation,omitempty"`
	BranchEvidenceMode          goalsearch.BranchEvidenceMode      `json:"branch_evidence_mode,omitempty"`
	BranchFrontierMode          string                             `json:"branch_frontier_mode,omitempty"`
	BranchBudgetMode            goalsearch.BranchBudgetMode        `json:"branch_budget_mode,omitempty"`
	StageBudget                 goalsearch.StageBudgetConfig       `json:"stage_budget,omitempty"`
	EvidenceAblation            string                             `json:"evidence_ablation,omitempty"`
	EvidencePriorityMultiplier  int                                `json:"evidence_priority_multiplier,omitempty"`
	MicroProgressPolicy         goalsearch.MicroProgressPolicy     `json:"micro_progress_policy,omitempty"`
	FormationFailureReport      bool                               `json:"formation_failure_report"`
	PrefixPreservation          bool                               `json:"prefix_preservation"`
	DistanceMode                goalsearch.DistanceMode            `json:"distance_mode"`
	Seeds                       []int64                            `json:"seeds"`
	ConfigPath                  string                             `json:"config"`
	NodeCount                   int                                `json:"node_count"`
	CandidateBudget             int                                `json:"candidate_budget"`
	ActionBudget                int                                `json:"action_budget"`
	MaxActionsPerPlan           int                                `json:"max_actions_per_plan"`
	PerWaypointBudget           int                                `json:"per_waypoint_budget"`
	SnapshotThreshold           uint64                             `json:"snapshot_threshold"`
	RetainEntries               uint64                             `json:"retain_entries"`
	CrashQuota                  int                                `json:"crash_quota"`
	PartitionEnabled            bool                               `json:"partition_enabled"`
	StrictTLC                   bool                               `json:"strict_tlc"`
	TLCAddress                  string                             `json:"tlc_address,omitempty"`
	ReplayVerify                bool                               `json:"replay_verify"`
	SaveAllRuns                 bool                               `json:"save_all_runs"`
	StopOnTarget                bool                               `json:"stop_on_target"`
	StopOnFailure               bool                               `json:"stop_on_failure"`
	MutationAdvisor             string                             `json:"mutation_advisor,omitempty"`
	FocusedGoalA                bool                               `json:"focused_goal_a"`
	FocusedGoalB                bool                               `json:"focused_goal_b"`
	AdvisorPriorityMultiplier   int                                `json:"advisor_priority_multiplier,omitempty"`
	AdvisorLocalActionCap       int                                `json:"advisor_local_action_cap,omitempty"`
	AdvisorNoProgressCap        int                                `json:"advisor_no_progress_cap,omitempty"`
	AdvisorQueueLimit           int                                `json:"advisor_queue_limit,omitempty"`
	AdvisorAblation             raftadvisor.Ablation               `json:"advisor_ablation,omitempty"`
	AdvisorRecordOnly           bool                               `json:"advisor_record_only"`
	BranchEvidenceRecordOnly    bool                               `json:"branch_evidence_record_only"`
	allFeasibleBranchesSet      bool
	formationFailureReportSet   bool
	prefixPreservationSet       bool
	partitionEnabledSet         bool
	strictTLCSet                bool
	replayVerifySet             bool
	saveAllRunsSet              bool
	stopOnTargetSet             bool
	stopOnFailureSet            bool
	focusedGoalASet             bool
	focusedGoalBSet             bool
	advisorRecordOnlySet        bool
	branchEvidenceRecordOnlySet bool
}

// UnmarshalJSON remembers whether a boolean was omitted or explicitly set to
// false. A plain bool cannot represent that distinction, and using logical OR
// during default inheritance made it impossible for a campaign to override a
// true default.
func (campaign *goalBenchmarkCampaign) UnmarshalJSON(data []byte) error {
	type alias goalBenchmarkCampaign
	var decoded alias
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	var presence struct {
		AllFeasibleBranches      *bool `json:"all_feasible_branches"`
		FormationFailureReport   *bool `json:"formation_failure_report"`
		PrefixPreservation       *bool `json:"prefix_preservation"`
		PartitionEnabled         *bool `json:"partition_enabled"`
		StrictTLC                *bool `json:"strict_tlc"`
		ReplayVerify             *bool `json:"replay_verify"`
		SaveAllRuns              *bool `json:"save_all_runs"`
		StopOnTarget             *bool `json:"stop_on_target"`
		StopOnFailure            *bool `json:"stop_on_failure"`
		FocusedGoalA             *bool `json:"focused_goal_a"`
		FocusedGoalB             *bool `json:"focused_goal_b"`
		AdvisorRecordOnly        *bool `json:"advisor_record_only"`
		BranchEvidenceRecordOnly *bool `json:"branch_evidence_record_only"`
	}
	if err := json.Unmarshal(data, &presence); err != nil {
		return err
	}
	*campaign = goalBenchmarkCampaign(decoded)
	campaign.allFeasibleBranchesSet = presence.AllFeasibleBranches != nil
	campaign.formationFailureReportSet = presence.FormationFailureReport != nil
	campaign.prefixPreservationSet = presence.PrefixPreservation != nil
	campaign.partitionEnabledSet = presence.PartitionEnabled != nil
	campaign.strictTLCSet = presence.StrictTLC != nil
	campaign.replayVerifySet = presence.ReplayVerify != nil
	campaign.saveAllRunsSet = presence.SaveAllRuns != nil
	campaign.stopOnTargetSet = presence.StopOnTarget != nil
	campaign.stopOnFailureSet = presence.StopOnFailure != nil
	campaign.focusedGoalASet = presence.FocusedGoalA != nil
	campaign.focusedGoalBSet = presence.FocusedGoalB != nil
	campaign.advisorRecordOnlySet = presence.AdvisorRecordOnly != nil
	campaign.branchEvidenceRecordOnlySet = presence.BranchEvidenceRecordOnly != nil
	return nil
}

type goalBenchmarkRunStatus struct {
	CampaignID string                `json:"campaign_id"`
	GoalID     goalsearch.GoalID     `json:"goal_id"`
	Method     goalsearch.SearchMode `json:"method"`
	Seed       int64                 `json:"seed"`
	Output     string                `json:"output"`
	Status     string                `json:"status"`
	Error      string                `json:"error,omitempty"`
	StartedAt  string                `json:"started_at,omitempty"`
	FinishedAt string                `json:"finished_at,omitempty"`
	Command    []string              `json:"command,omitempty"`
}

type goalBenchmarkStatus struct {
	SchemaVersion string                   `json:"schema_version"`
	ManifestPath  string                   `json:"manifest_path"`
	OutputRoot    string                   `json:"output_root"`
	Runs          []goalBenchmarkRunStatus `json:"runs"`
	LLMCalls      int                      `json:"llm_calls"`
}

type goalSeedManifestEntry struct {
	CampaignID           string                `json:"campaign_id"`
	Seed                 int64                 `json:"seed"`
	GoalID               goalsearch.GoalID     `json:"goal_id"`
	Method               goalsearch.SearchMode `json:"method"`
	ConfigPath           string                `json:"config"`
	Included             bool                  `json:"included"`
	ExclusionReason      string                `json:"exclusion_reason,omitempty"`
	InitialPlanKey       string                `json:"initial_plan_key,omitempty"`
	FinalTraceKey        string                `json:"final_trace_key,omitempty"`
	SemanticTraceKey     string                `json:"semantic_trace_key,omitempty"`
	ProgressPathKey      string                `json:"progress_path_key,omitempty"`
	FacetSequenceKey     string                `json:"facet_sequence_key,omitempty"`
	FrontierPrefixKeys   []string              `json:"frontier_prefix_keys,omitempty"`
	MessageQueueShapeKey string                `json:"message_queue_shape_key,omitempty"`
}

type goalSeedDiversityReport struct {
	SchemaVersion                string                           `json:"schema_version"`
	IncludedRuns                 int                              `json:"included_runs"`
	ActualDifferentInitialPlan   int                              `json:"actual_different_initial_plans"`
	ActualDifferentTrace         int                              `json:"actual_different_traces"`
	ActualDifferentSemanticTrace int                              `json:"actual_different_semantic_traces"`
	ActualDifferentProgressPath  int                              `json:"actual_different_progress_paths"`
	IdenticalTraceGroups         map[string][]string              `json:"identical_trace_groups"`
	SemanticScheduleGroups       map[string][]string              `json:"semantic_schedule_groups"`
	ByCampaign                   map[string]goalCampaignDiversity `json:"by_campaign"`
}

type goalCampaignDiversity struct {
	IncludedRuns                 int                 `json:"included_runs"`
	ActualDifferentInitialPlan   int                 `json:"actual_different_initial_plans"`
	ActualDifferentTrace         int                 `json:"actual_different_traces"`
	ActualDifferentSemanticTrace int                 `json:"actual_different_semantic_traces"`
	ActualDifferentProgressPath  int                 `json:"actual_different_progress_paths"`
	IdenticalTraceGroups         map[string][]string `json:"identical_trace_groups"`
	SemanticScheduleGroups       map[string][]string `json:"semantic_schedule_groups"`
}

type goalBenchmarkEnvironment struct {
	SchemaVersion   string            `json:"schema_version"`
	ProjectVersion  string            `json:"project_version"`
	GeneratedAt     string            `json:"generated_at"`
	GoVersion       string            `json:"go_version"`
	GOOS            string            `json:"goos"`
	GOARCH          string            `json:"goarch"`
	CPUModel        string            `json:"cpu_model,omitempty"`
	Hostname        string            `json:"hostname,omitempty"`
	Revision        string            `json:"git_revision,omitempty"`
	Modified        string            `json:"git_modified,omitempty"`
	TLCVersion      string            `json:"tlc_version"`
	EtcdRaftVersion string            `json:"etcd_raft_version"`
	BuildSettings   map[string]string `json:"build_settings,omitempty"`
}

func goalBenchmarkCommand(
	ctx context.Context,
	args []string,
	stdout, stderr io.Writer,
) error {
	flags := flag.NewFlagSet("modelfuzz-ng goal-benchmark", flag.ContinueOnError)
	flags.SetOutput(stderr)
	manifestPath := flags.String("manifest", "", "goal benchmark JSON manifest")
	outputRoot := flags.String("output", "", "benchmark output root; may already contain completed campaigns")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || *manifestPath == "" || *outputRoot == "" {
		flags.Usage()
		return fmt.Errorf("-manifest and -output are required")
	}
	var manifest goalBenchmarkManifest
	if err := persistence.ReadJSON(*manifestPath, &manifest); err != nil {
		return err
	}
	manifest = normalizeGoalBenchmarkManifest(manifest)
	if err := validateGoalBenchmarkManifest(manifest); err != nil {
		return err
	}
	if err := os.MkdirAll(*outputRoot, 0o755); err != nil {
		return err
	}
	if err := persistence.WriteJSONAtomic(
		filepath.Join(*outputRoot, "benchmark-manifest.json"), manifest,
	); err != nil {
		return err
	}
	if err := persistence.WriteJSONAtomic(
		filepath.Join(*outputRoot, "environment.json"), captureGoalBenchmarkEnvironment(),
	); err != nil {
		return err
	}
	status := goalBenchmarkStatus{
		SchemaVersion: goalBenchmarkSchema, ManifestPath: *manifestPath,
		OutputRoot: *outputRoot, LLMCalls: 0,
	}
	var campaignErrors []error
	for _, campaign := range manifest.Campaigns {
		for _, seed := range campaign.Seeds {
			if err := ctx.Err(); err != nil {
				return errors.Join(err, errors.Join(campaignErrors...))
			}
			runOutput := filepath.Join(
				*outputRoot, campaign.ID, fmt.Sprintf("seed-%d", seed),
			)
			runStatus := goalBenchmarkRunStatus{
				CampaignID: campaign.ID, GoalID: campaign.GoalID,
				Method: campaign.Method, Seed: seed, Output: runOutput,
			}
			callArgs := benchmarkGoalSearchArgs(campaign, seed, runOutput)
			runStatus.Command = append([]string{"modelfuzz-ng", "goal-search"}, callArgs...)
			if completedGoalRun(runOutput) {
				runStatus.Status = "skipped-complete"
				status.Runs = append(status.Runs, runStatus)
				continue
			}
			runStatus.Status = "running"
			runStatus.StartedAt = time.Now().UTC().Format(time.RFC3339Nano)
			var runStdout bytes.Buffer
			err := goalSearchCommand(ctx, callArgs, &runStdout, stderr)
			runStatus.FinishedAt = time.Now().UTC().Format(time.RFC3339Nano)
			if err != nil {
				runStatus.Status = "failed"
				runStatus.Error = err.Error()
				campaignErrors = append(campaignErrors,
					fmt.Errorf("campaign %s seed %d: %w", campaign.ID, seed, err))
			} else {
				runStatus.Status = "completed"
			}
			status.Runs = append(status.Runs, runStatus)
			if writeErr := persistence.WriteJSONAtomic(
				filepath.Join(*outputRoot, "benchmark-status.json"), status,
			); writeErr != nil {
				return errors.Join(writeErr, errors.Join(campaignErrors...))
			}
		}
	}
	if err := persistence.WriteJSONAtomic(
		filepath.Join(*outputRoot, "benchmark-status.json"), status,
	); err != nil {
		return errors.Join(err, errors.Join(campaignErrors...))
	}
	reports, err := loadGoalReports(*outputRoot)
	if err != nil {
		return errors.Join(err, errors.Join(campaignErrors...))
	}
	if len(reports) > 0 {
		summary := aggregateGoalReports(*outputRoot, reports)
		if err := persistence.WriteJSONAtomic(
			filepath.Join(*outputRoot, "comparison-summary.json"), summary,
		); err != nil {
			return errors.Join(err, errors.Join(campaignErrors...))
		}
		if err := writeGoalCSV(
			filepath.Join(*outputRoot, "figure-ready.csv"), reports,
		); err != nil {
			return errors.Join(err, errors.Join(campaignErrors...))
		}
		if err := writeBranchCSVs(*outputRoot, reports); err != nil {
			return errors.Join(err, errors.Join(campaignErrors...))
		}
	}
	seedManifest, diversity := buildGoalSeedManifest(status, manifest)
	if err := persistence.WriteJSONAtomic(
		filepath.Join(*outputRoot, "seed-manifest.json"), seedManifest,
	); err != nil {
		return errors.Join(err, errors.Join(campaignErrors...))
	}
	if err := persistence.WriteJSONAtomic(
		filepath.Join(*outputRoot, "seed-diversity.json"), diversity,
	); err != nil {
		return errors.Join(err, errors.Join(campaignErrors...))
	}
	_, outputErr := fmt.Fprintf(
		stdout, "Goal benchmark 结束: runs=%d reports=%d output=%s\n",
		len(status.Runs), len(reports), *outputRoot,
	)
	return errors.Join(errors.Join(campaignErrors...), outputErr)
}

func normalizeGoalBenchmarkManifest(manifest goalBenchmarkManifest) goalBenchmarkManifest {
	var defaults goalBenchmarkCampaign
	if manifest.Defaults != nil {
		defaults = *manifest.Defaults
	}
	for index := range manifest.Campaigns {
		campaign := &manifest.Campaigns[index]
		if campaign.GoalID == "" {
			campaign.GoalID = defaults.GoalID
		}
		if campaign.Method == "" {
			campaign.Method = defaults.Method
		}
		if campaign.HintStrength == "" {
			campaign.HintStrength = defaults.HintStrength
		}
		if campaign.FrontierTopK == 0 {
			campaign.FrontierTopK = defaults.FrontierTopK
		}
		if campaign.TotalFrontierCapacity == 0 {
			campaign.TotalFrontierCapacity = defaults.TotalFrontierCapacity
		}
		if campaign.PerBranchMinimum == 0 {
			campaign.PerBranchMinimum = defaults.PerBranchMinimum
		}
		if len(campaign.BranchTemplateIDs) == 0 {
			campaign.BranchTemplateIDs = append(
				[]goalsearch.BranchTemplateID(nil), defaults.BranchTemplateIDs...,
			)
		}
		if !campaign.allFeasibleBranchesSet && !campaign.AllFeasibleBranches {
			campaign.AllFeasibleBranches = defaults.AllFeasibleBranches
		}
		if campaign.BranchAwareness == "" {
			campaign.BranchAwareness = defaults.BranchAwareness
		}
		if campaign.BranchDimensionAblation == "" {
			campaign.BranchDimensionAblation = defaults.BranchDimensionAblation
		}
		if campaign.BranchBudgetAllocation == "" {
			campaign.BranchBudgetAllocation = defaults.BranchBudgetAllocation
		}
		if campaign.BranchEvidenceMode == "" {
			campaign.BranchEvidenceMode = defaults.BranchEvidenceMode
		}
		if campaign.BranchFrontierMode == "" {
			campaign.BranchFrontierMode = defaults.BranchFrontierMode
		}
		if campaign.BranchBudgetMode == "" {
			campaign.BranchBudgetMode = defaults.BranchBudgetMode
		}
		if campaign.StageBudget.InitialQuota == 0 {
			campaign.StageBudget = defaults.StageBudget
		}
		if campaign.EvidenceAblation == "" {
			campaign.EvidenceAblation = defaults.EvidenceAblation
		}
		if campaign.EvidencePriorityMultiplier == 0 {
			campaign.EvidencePriorityMultiplier = defaults.EvidencePriorityMultiplier
		}
		if campaign.MicroProgressPolicy == "" {
			campaign.MicroProgressPolicy = defaults.MicroProgressPolicy
		}
		if !campaign.formationFailureReportSet && !campaign.FormationFailureReport {
			campaign.FormationFailureReport = defaults.FormationFailureReport
		}
		if campaign.DistanceMode == "" {
			campaign.DistanceMode = defaults.DistanceMode
		}
		if !campaign.prefixPreservationSet && !campaign.PrefixPreservation {
			campaign.PrefixPreservation = defaults.PrefixPreservation
		}
		if len(campaign.Seeds) == 0 {
			campaign.Seeds = append([]int64(nil), defaults.Seeds...)
		}
		if campaign.ConfigPath == "" {
			campaign.ConfigPath = defaults.ConfigPath
		}
		if campaign.NodeCount == 0 {
			campaign.NodeCount = defaults.NodeCount
		}
		if campaign.CandidateBudget == 0 {
			campaign.CandidateBudget = defaults.CandidateBudget
		}
		if campaign.ActionBudget == 0 {
			campaign.ActionBudget = defaults.ActionBudget
		}
		if campaign.MaxActionsPerPlan == 0 {
			campaign.MaxActionsPerPlan = defaults.MaxActionsPerPlan
		}
		if campaign.PerWaypointBudget == 0 {
			campaign.PerWaypointBudget = defaults.PerWaypointBudget
		}
		if campaign.SnapshotThreshold == 0 {
			campaign.SnapshotThreshold = defaults.SnapshotThreshold
		}
		if campaign.RetainEntries == 0 {
			campaign.RetainEntries = defaults.RetainEntries
		}
		if campaign.CrashQuota == 0 {
			campaign.CrashQuota = defaults.CrashQuota
		}
		if !campaign.partitionEnabledSet && !campaign.PartitionEnabled {
			campaign.PartitionEnabled = defaults.PartitionEnabled
		}
		if !campaign.strictTLCSet && !campaign.StrictTLC {
			campaign.StrictTLC = defaults.StrictTLC
		}
		if campaign.TLCAddress == "" {
			campaign.TLCAddress = defaults.TLCAddress
		}
		if !campaign.replayVerifySet && !campaign.ReplayVerify {
			campaign.ReplayVerify = defaults.ReplayVerify
		}
		if !campaign.saveAllRunsSet && !campaign.SaveAllRuns {
			campaign.SaveAllRuns = defaults.SaveAllRuns
		}
		if !campaign.stopOnTargetSet && !campaign.StopOnTarget {
			campaign.StopOnTarget = defaults.StopOnTarget
		}
		if !campaign.stopOnFailureSet && !campaign.StopOnFailure {
			campaign.StopOnFailure = defaults.StopOnFailure
		}
		if campaign.MutationAdvisor == "" {
			campaign.MutationAdvisor = defaults.MutationAdvisor
		}
		if !campaign.focusedGoalASet && !campaign.FocusedGoalA {
			campaign.FocusedGoalA = defaults.FocusedGoalA
		}
		if !campaign.focusedGoalBSet && !campaign.FocusedGoalB {
			campaign.FocusedGoalB = defaults.FocusedGoalB
		}
		if campaign.AdvisorPriorityMultiplier == 0 {
			campaign.AdvisorPriorityMultiplier = defaults.AdvisorPriorityMultiplier
		}
		if campaign.AdvisorLocalActionCap == 0 {
			campaign.AdvisorLocalActionCap = defaults.AdvisorLocalActionCap
		}
		if campaign.AdvisorNoProgressCap == 0 {
			campaign.AdvisorNoProgressCap = defaults.AdvisorNoProgressCap
		}
		if campaign.AdvisorQueueLimit == 0 {
			campaign.AdvisorQueueLimit = defaults.AdvisorQueueLimit
		}
		if campaign.AdvisorAblation == "" {
			campaign.AdvisorAblation = defaults.AdvisorAblation
		}
		if !campaign.advisorRecordOnlySet && !campaign.AdvisorRecordOnly {
			campaign.AdvisorRecordOnly = defaults.AdvisorRecordOnly
		}
		if !campaign.branchEvidenceRecordOnlySet && !campaign.BranchEvidenceRecordOnly {
			campaign.BranchEvidenceRecordOnly = defaults.BranchEvidenceRecordOnly
		}
		if campaign.BranchAwareness == "" {
			campaign.BranchAwareness = goalsearch.BranchRealizedAware
		}
		if campaign.BranchDimensionAblation == "" {
			campaign.BranchDimensionAblation = goalsearch.BranchAblationNone
		}
		if campaign.BranchBudgetAllocation == "" {
			campaign.BranchBudgetAllocation = "round-robin"
		}
		if campaign.BranchEvidenceMode == "" {
			campaign.BranchEvidenceMode = goalsearch.BranchEvidenceOff
		}
		if campaign.BranchFrontierMode == "" {
			if campaign.Method == goalsearch.ModeDiversityFrontier {
				campaign.BranchFrontierMode = "diversity"
			} else {
				campaign.BranchFrontierMode = "standard"
			}
		}
		if campaign.BranchBudgetMode == "" {
			campaign.BranchBudgetMode = goalsearch.BranchBudgetRoundRobin
		}
		if campaign.StageBudget.InitialQuota == 0 {
			campaign.StageBudget = goalsearch.StageBudgetConfig{
				InitialQuota: 2, SupportedQuota: 2, CommitmentQuota: 2,
				NextWaypointQuota: 1, PerBranchTotalCap: 20,
			}
		}
		if campaign.EvidenceAblation == "" {
			campaign.EvidenceAblation = "none"
		}
		if campaign.EvidencePriorityMultiplier == 0 {
			campaign.EvidencePriorityMultiplier = 16
		}
		if campaign.MicroProgressPolicy == "" {
			campaign.MicroProgressPolicy = goalsearch.MicroProgressLegacy
		}
		if campaign.PerBranchMinimum == 0 {
			campaign.PerBranchMinimum = 1
		}
		if campaign.MutationAdvisor == "" {
			campaign.MutationAdvisor = "off"
		}
		if campaign.AdvisorPriorityMultiplier == 0 {
			campaign.AdvisorPriorityMultiplier = 16
		}
		if campaign.AdvisorLocalActionCap == 0 {
			campaign.AdvisorLocalActionCap = 9
		}
		if campaign.AdvisorNoProgressCap == 0 {
			campaign.AdvisorNoProgressCap = 8
		}
		if campaign.AdvisorQueueLimit == 0 {
			campaign.AdvisorQueueLimit = 64
		}
		if campaign.AdvisorAblation == "" {
			campaign.AdvisorAblation = raftadvisor.AblationNone
		}
	}
	manifest.Defaults = nil
	return manifest
}

func buildGoalSeedManifest(
	status goalBenchmarkStatus,
	manifest goalBenchmarkManifest,
) ([]goalSeedManifestEntry, goalSeedDiversityReport) {
	configByCampaign := make(map[string]string, len(manifest.Campaigns))
	for _, campaign := range manifest.Campaigns {
		configByCampaign[campaign.ID] = campaign.ConfigPath
	}
	entries := make([]goalSeedManifestEntry, 0, len(status.Runs))
	initials := make(map[string]struct{})
	traces := make(map[string][]string)
	semanticTraces := make(map[string][]string)
	progressPaths := make(map[string]struct{})
	type diversitySets struct {
		initials       map[string]struct{}
		traces         map[string][]string
		semanticTraces map[string][]string
		progressPaths  map[string]struct{}
		included       int
	}
	byCampaign := make(map[string]*diversitySets)
	for _, run := range status.Runs {
		entry := goalSeedManifestEntry{
			CampaignID: run.CampaignID, Seed: run.Seed, GoalID: run.GoalID,
			Method: run.Method, ConfigPath: configByCampaign[run.CampaignID],
		}
		var report goalSearchReport
		if err := persistence.ReadJSON(filepath.Join(run.Output, "final-report.json"), &report); err != nil {
			entry.ExclusionReason = "missing-or-invalid-final-report"
			entries = append(entries, entry)
			continue
		}
		entry.Included = true
		entry.InitialPlanKey = report.Diversity.InitialPlanKey
		entry.FinalTraceKey = report.Diversity.FinalTraceKey
		entry.SemanticTraceKey = report.Diversity.SemanticTraceKey
		entry.ProgressPathKey = report.Diversity.GoalProgressSequenceKey
		entry.FacetSequenceKey = report.Diversity.FacetSequenceKey
		entry.FrontierPrefixKeys = append([]string(nil), report.Diversity.FrontierPrefixKeys...)
		entry.MessageQueueShapeKey = report.Diversity.MessageQueueShapeKey
		label := fmt.Sprintf("%s/seed-%d", run.CampaignID, run.Seed)
		initials[entry.InitialPlanKey] = struct{}{}
		traces[entry.FinalTraceKey] = append(traces[entry.FinalTraceKey], label)
		semanticTraces[entry.SemanticTraceKey] =
			append(semanticTraces[entry.SemanticTraceKey], label)
		progressPaths[entry.ProgressPathKey] = struct{}{}
		sets := byCampaign[run.CampaignID]
		if sets == nil {
			sets = &diversitySets{
				initials:       make(map[string]struct{}),
				traces:         make(map[string][]string),
				semanticTraces: make(map[string][]string),
				progressPaths:  make(map[string]struct{}),
			}
			byCampaign[run.CampaignID] = sets
		}
		sets.included++
		sets.initials[entry.InitialPlanKey] = struct{}{}
		sets.traces[entry.FinalTraceKey] = append(sets.traces[entry.FinalTraceKey], label)
		sets.semanticTraces[entry.SemanticTraceKey] =
			append(sets.semanticTraces[entry.SemanticTraceKey], label)
		sets.progressPaths[entry.ProgressPathKey] = struct{}{}
		entries = append(entries, entry)
	}
	diversity := goalSeedDiversityReport{
		SchemaVersion:                goalBenchmarkSchema,
		ActualDifferentInitialPlan:   len(initials),
		ActualDifferentTrace:         len(traces),
		ActualDifferentSemanticTrace: len(semanticTraces),
		ActualDifferentProgressPath:  len(progressPaths),
		IdenticalTraceGroups:         duplicateGroups(traces),
		SemanticScheduleGroups:       duplicateGroups(semanticTraces),
		ByCampaign:                   make(map[string]goalCampaignDiversity),
	}
	for campaignID, sets := range byCampaign {
		diversity.ByCampaign[campaignID] = goalCampaignDiversity{
			IncludedRuns:                 sets.included,
			ActualDifferentInitialPlan:   len(sets.initials),
			ActualDifferentTrace:         len(sets.traces),
			ActualDifferentSemanticTrace: len(sets.semanticTraces),
			ActualDifferentProgressPath:  len(sets.progressPaths),
			IdenticalTraceGroups:         duplicateGroups(sets.traces),
			SemanticScheduleGroups:       duplicateGroups(sets.semanticTraces),
		}
	}
	for _, entry := range entries {
		if entry.Included {
			diversity.IncludedRuns++
		}
	}
	return entries, diversity
}

func captureGoalBenchmarkEnvironment() goalBenchmarkEnvironment {
	environment := goalBenchmarkEnvironment{
		SchemaVersion:   goalBenchmarkSchema,
		ProjectVersion:  releaseVersion,
		GeneratedAt:     time.Now().UTC().Format(time.RFC3339Nano),
		GoVersion:       runtime.Version(),
		GOOS:            runtime.GOOS,
		GOARCH:          runtime.GOARCH,
		TLCVersion:      "1.8.0",
		EtcdRaftVersion: "v3.7.0 (local replace ../raft)",
		BuildSettings:   make(map[string]string),
	}
	if hostname, err := os.Hostname(); err == nil {
		environment.Hostname = hostname
	}
	if raw, err := os.ReadFile("/proc/cpuinfo"); err == nil {
		for _, line := range strings.Split(string(raw), "\n") {
			key, value, found := strings.Cut(line, ":")
			if found && strings.TrimSpace(key) == "model name" {
				environment.CPUModel = strings.TrimSpace(value)
				break
			}
		}
	}
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, setting := range info.Settings {
			if strings.HasPrefix(setting.Key, "vcs.") || setting.Key == "-buildmode" {
				environment.BuildSettings[setting.Key] = setting.Value
			}
			switch setting.Key {
			case "vcs.revision":
				environment.Revision = setting.Value
			case "vcs.modified":
				environment.Modified = setting.Value
			}
		}
	}
	if len(environment.BuildSettings) == 0 {
		environment.BuildSettings = nil
	}
	return environment
}

func duplicateGroups(groups map[string][]string) map[string][]string {
	result := make(map[string][]string)
	for key, labels := range groups {
		if len(labels) < 2 {
			continue
		}
		sort.Strings(labels)
		result[key] = labels
	}
	return result
}

func validateGoalBenchmarkManifest(manifest goalBenchmarkManifest) error {
	if manifest.SchemaVersion != goalBenchmarkSchema {
		return fmt.Errorf("goal benchmark schema=%q want %q",
			manifest.SchemaVersion, goalBenchmarkSchema)
	}
	if len(manifest.Campaigns) == 0 {
		return fmt.Errorf("goal benchmark manifest has no campaigns")
	}
	seen := make(map[string]struct{}, len(manifest.Campaigns))
	for index, campaign := range manifest.Campaigns {
		if campaign.ID == "" || campaign.ConfigPath == "" || len(campaign.Seeds) == 0 {
			return fmt.Errorf("campaign %d identity, config, or seeds are incomplete", index)
		}
		if _, duplicate := seen[campaign.ID]; duplicate {
			return fmt.Errorf("duplicate campaign ID %q", campaign.ID)
		}
		seen[campaign.ID] = struct{}{}
		if err := campaign.Method.Validate(); err != nil {
			return err
		}
		if err := campaign.HintStrength.Validate(); err != nil {
			return err
		}
		if err := campaign.DistanceMode.Validate(); err != nil {
			return err
		}
		if campaign.BranchAwareness == "" {
			campaign.BranchAwareness = goalsearch.BranchRealizedAware
		}
		if err := campaign.BranchAwareness.Validate(); err != nil {
			return err
		}
		if campaign.BranchDimensionAblation == "" {
			campaign.BranchDimensionAblation = goalsearch.BranchAblationNone
		}
		if err := campaign.BranchDimensionAblation.Validate(); err != nil {
			return err
		}
		if campaign.BranchBudgetAllocation == "" {
			campaign.BranchBudgetAllocation = "round-robin"
		}
		if campaign.BranchBudgetAllocation != "round-robin" {
			return fmt.Errorf("campaign %q has unsupported branch budget allocation %q",
				campaign.ID, campaign.BranchBudgetAllocation)
		}
		if err := campaign.BranchEvidenceMode.Validate(); err != nil {
			return err
		}
		if err := campaign.BranchBudgetMode.Validate(); err != nil {
			return err
		}
		if err := campaign.StageBudget.Validate(); err != nil {
			return err
		}
		if err := campaign.MicroProgressPolicy.Validate(); err != nil {
			return err
		}
		switch campaign.BranchFrontierMode {
		case "standard", "diversity", "evidence-aware":
		default:
			return fmt.Errorf("campaign %q has unsupported Branch Frontier mode %q",
				campaign.ID, campaign.BranchFrontierMode)
		}
		if campaign.EvidenceAblation != "none" {
			return fmt.Errorf("campaign %q has unsupported evidence ablation %q",
				campaign.ID, campaign.EvidenceAblation)
		}
		if campaign.EvidencePriorityMultiplier <= 0 {
			return fmt.Errorf("campaign %q has invalid evidence priority multiplier",
				campaign.ID)
		}
		if campaign.MutationAdvisor != "off" && campaign.MutationAdvisor != "raft-focused" {
			return fmt.Errorf("campaign %q has unsupported mutation advisor %q",
				campaign.ID, campaign.MutationAdvisor)
		}
		if err := (raftadvisor.Config{
			GoalAEnabled: campaign.FocusedGoalA, GoalBEnabled: campaign.FocusedGoalB,
			PriorityMultiplier: campaign.AdvisorPriorityMultiplier,
			LocalActionCap:     campaign.AdvisorLocalActionCap,
			NoProgressCap:      campaign.AdvisorNoProgressCap,
			QueueLimit:         campaign.AdvisorQueueLimit, Ablation: campaign.AdvisorAblation,
		}).Validate(); err != nil {
			return fmt.Errorf("campaign %q: %w", campaign.ID, err)
		}
		if campaign.MutationAdvisor == "raft-focused" &&
			campaign.HintStrength != goalsearch.HintWeak {
			return fmt.Errorf("campaign %q focused advisor requires weak hints", campaign.ID)
		}
		if campaign.AdvisorRecordOnly && campaign.MutationAdvisor == "off" {
			return fmt.Errorf("campaign %q advisor record-only requires raft-focused", campaign.ID)
		}
		if campaign.TotalFrontierCapacity < 0 || campaign.PerBranchMinimum < 0 {
			return fmt.Errorf("campaign %q has invalid Branch Frontier capacity", campaign.ID)
		}
		if campaign.Method == goalsearch.ModeDiversityFrontier &&
			(campaign.TotalFrontierCapacity <= 0 || campaign.PerBranchMinimum <= 0) {
			return fmt.Errorf("campaign %q diversity Frontier requires positive total/minimum capacity", campaign.ID)
		}
		if campaign.Method == goalsearch.ModeEvidenceFrontier &&
			(campaign.TotalFrontierCapacity <= 0 ||
				campaign.BranchEvidenceMode == goalsearch.BranchEvidenceOff ||
				campaign.BranchFrontierMode != "evidence-aware") {
			return fmt.Errorf("campaign %q evidence Frontier requires capacity, evidence, and evidence-aware mode",
				campaign.ID)
		}
		if len(campaign.BranchTemplateIDs) > 0 && campaign.AllFeasibleBranches {
			return fmt.Errorf("campaign %q selects explicit and all feasible Branches", campaign.ID)
		}
		for _, id := range campaign.BranchTemplateIDs {
			template, err := goalsearch.BranchTemplate(id)
			if err != nil {
				return err
			}
			if template.GoalID != campaign.GoalID {
				return fmt.Errorf("campaign %q branch %q belongs to goal %q",
					campaign.ID, id, template.GoalID)
			}
		}
		if campaign.FrontierTopK < 1 || campaign.NodeCount < 3 ||
			campaign.CandidateBudget < 1 || campaign.ActionBudget < 1 ||
			campaign.MaxActionsPerPlan < 1 || campaign.PerWaypointBudget < 1 ||
			campaign.CrashQuota < 1 {
			return fmt.Errorf("campaign %q contains non-positive budgets or top-K", campaign.ID)
		}
		for _, seed := range campaign.Seeds {
			if seed == 0 {
				return fmt.Errorf("campaign %q contains zero seed", campaign.ID)
			}
		}
	}
	return nil
}

func benchmarkGoalSearchArgs(
	campaign goalBenchmarkCampaign,
	seed int64,
	output string,
) []string {
	goalAware := campaign.Method != goalsearch.ModeUnguided &&
		campaign.Method != goalsearch.ModeDirectedSnapshot
	branchIDs := make([]string, 0, len(campaign.BranchTemplateIDs))
	for _, id := range campaign.BranchTemplateIDs {
		branchIDs = append(branchIDs, string(id))
	}
	return []string{
		"-config", campaign.ConfigPath,
		"-goal", string(campaign.GoalID),
		"-mode", string(campaign.Method),
		"-output", output,
		"-nodes", strconv.Itoa(campaign.NodeCount),
		"-seed", strconv.FormatInt(seed, 10),
		"-candidate-budget", strconv.Itoa(campaign.CandidateBudget),
		"-action-budget", strconv.Itoa(campaign.ActionBudget),
		"-max-actions-per-plan", strconv.Itoa(campaign.MaxActionsPerPlan),
		"-per-waypoint-budget", strconv.Itoa(campaign.PerWaypointBudget),
		"-frontier-top-k", strconv.Itoa(campaign.FrontierTopK),
		"-total-frontier-capacity", strconv.Itoa(campaign.TotalFrontierCapacity),
		"-per-branch-minimum-capacity", strconv.Itoa(max(1, campaign.PerBranchMinimum)),
		"-branch-templates", strings.Join(branchIDs, ","),
		"-all-feasible-branches=" + strconv.FormatBool(campaign.AllFeasibleBranches),
		"-branch-awareness", string(campaign.BranchAwareness),
		"-branch-dimension-ablation", string(campaign.BranchDimensionAblation),
		"-branch-budget-allocation", campaign.BranchBudgetAllocation,
		"-branch-evidence-mode", string(campaign.BranchEvidenceMode),
		"-branch-frontier-mode", campaign.BranchFrontierMode,
		"-branch-budget-mode", string(campaign.BranchBudgetMode),
		"-branch-initial-quota", strconv.Itoa(campaign.StageBudget.InitialQuota),
		"-branch-supported-quota", strconv.Itoa(campaign.StageBudget.SupportedQuota),
		"-branch-commitment-quota", strconv.Itoa(campaign.StageBudget.CommitmentQuota),
		"-branch-next-waypoint-quota", strconv.Itoa(campaign.StageBudget.NextWaypointQuota),
		"-branch-total-cap", strconv.Itoa(campaign.StageBudget.PerBranchTotalCap),
		"-evidence-ablation", campaign.EvidenceAblation,
		"-evidence-priority-multiplier", strconv.Itoa(campaign.EvidencePriorityMultiplier),
		"-micro-progress-policy", string(campaign.MicroProgressPolicy),
		"-formation-failure-report=" + strconv.FormatBool(campaign.FormationFailureReport),
		"-hint-strength", string(campaign.HintStrength),
		"-distance-mode", string(campaign.DistanceMode),
		"-strict-tlc=" + strconv.FormatBool(campaign.StrictTLC),
		"-tlc", campaign.TLCAddress,
		"-goal-aware-mutation=" + strconv.FormatBool(goalAware),
		"-prefix-preservation=" + strconv.FormatBool(campaign.PrefixPreservation),
		"-save-all-runs=" + strconv.FormatBool(campaign.SaveAllRuns),
		"-snapshot-threshold", strconv.FormatUint(campaign.SnapshotThreshold, 10),
		"-retain-entries", strconv.FormatUint(campaign.RetainEntries, 10),
		"-crash-quota", strconv.Itoa(campaign.CrashQuota),
		"-partition-enabled=" + strconv.FormatBool(campaign.PartitionEnabled),
		"-workers", "1",
		"-replay-verify=" + strconv.FormatBool(campaign.ReplayVerify),
		"-stop-on-target=" + strconv.FormatBool(campaign.StopOnTarget),
		"-stop-on-failure=" + strconv.FormatBool(campaign.StopOnFailure),
		"-mutation-advisor", campaign.MutationAdvisor,
		"-focused-goal-a=" + strconv.FormatBool(campaign.FocusedGoalA),
		"-focused-goal-b=" + strconv.FormatBool(campaign.FocusedGoalB),
		"-advisor-priority-multiplier", strconv.Itoa(campaign.AdvisorPriorityMultiplier),
		"-advisor-local-action-cap", strconv.Itoa(campaign.AdvisorLocalActionCap),
		"-advisor-no-progress-cap", strconv.Itoa(campaign.AdvisorNoProgressCap),
		"-advisor-queue-limit", strconv.Itoa(campaign.AdvisorQueueLimit),
		"-advisor-ablation", string(campaign.AdvisorAblation),
		"-advisor-record-only=" + strconv.FormatBool(campaign.AdvisorRecordOnly),
		"-branch-evidence-record-only=" + strconv.FormatBool(campaign.BranchEvidenceRecordOnly),
	}
}

func completedGoalRun(output string) bool {
	var report goalSearchReport
	if err := persistence.ReadJSON(filepath.Join(output, "final-report.json"), &report); err != nil {
		return false
	}
	return report.SchemaVersion == goalsearch.SchemaVersion && report.LLMCalls == 0
}
