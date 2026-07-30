package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/core"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/corpus"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/coverageanalysis"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/engine"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/goalsearch"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/minimize"
	raftmodel "github.com/SuzumiyaHaruki/modelfuzz-ng/internal/model/raft"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/mutation"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/persistence"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/plan"
	policypkg "github.com/SuzumiyaHaruki/modelfuzz-ng/internal/policy"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/protocolmutation"
	raftadvisor "github.com/SuzumiyaHaruki/modelfuzz-ng/internal/protocolmutation/raft"
	tracepkg "github.com/SuzumiyaHaruki/modelfuzz-ng/internal/trace"
)

type goalSearchSettings struct {
	SchemaVersion         string                `json:"schema_version"`
	ReleaseVersion        string                `json:"release_version"`
	GoalID                goalsearch.GoalID     `json:"goal_id"`
	Mode                  goalsearch.SearchMode `json:"search_mode"`
	NodeCount             int                   `json:"node_count"`
	Seed                  int64                 `json:"seed"`
	CandidateBudget       int                   `json:"candidate_plan_budget"`
	ActionBudget          int                   `json:"total_action_budget"`
	MaxActionsPerPlan     int                   `json:"max_actions_per_plan"`
	PerWaypointBudget     int                   `json:"per_waypoint_budget"`
	FrontierTopK          int                   `json:"frontier_top_k"`
	TotalFrontierCapacity int                   `json:"total_frontier_capacity,omitempty"`

	// Frozen Branch/Evidence compatibility settings. These fields remain
	// persisted for historical artifact replay and accepted experiments, but
	// their defaults do not select an experimental search path.
	PerBranchMinimum           int                                  `json:"per_branch_minimum_capacity,omitempty"`
	BranchTemplateIDs          []goalsearch.BranchTemplateID        `json:"branch_template_ids,omitempty"`
	AllFeasibleBranches        bool                                 `json:"all_feasible_branches"`
	BranchAwareness            goalsearch.BranchAwareness           `json:"branch_awareness,omitempty"`
	BranchDimensionAblation    goalsearch.BranchDimensionAblation   `json:"branch_dimension_ablation,omitempty"`
	BranchBudgetAllocation     string                               `json:"branch_budget_allocation,omitempty"`
	BranchEvidenceMode         goalsearch.BranchEvidenceMode        `json:"branch_evidence_mode"`
	BranchFrontierMode         string                               `json:"branch_frontier_mode"`
	BranchBudgetMode           goalsearch.BranchBudgetMode          `json:"branch_budget_mode"`
	StageBudget                goalsearch.StageBudgetConfig         `json:"stage_budget"`
	EvidenceAblation           string                               `json:"evidence_ablation"`
	EvidencePriorityMultiplier int                                  `json:"evidence_priority_multiplier"`
	MicroProgressPolicy        goalsearch.MicroProgressPolicy       `json:"micro_progress_policy"`
	FormationFailureReport     bool                                 `json:"formation_failure_report"`
	BranchFeasibility          []goalsearch.BranchFeasibilityResult `json:"branch_feasibility,omitempty"`
	HintStrength               goalsearch.HintStrength              `json:"hint_strength"`
	DistanceMode               goalsearch.DistanceMode              `json:"distance_mode"`
	StrictTLC                  bool                                 `json:"strict_tlc"`
	TLCAddress                 string                               `json:"tlc_address,omitempty"`
	GoalAwareMutation          bool                                 `json:"goal_aware_mutation"`
	PrefixPreservation         bool                                 `json:"frontier_prefix_preservation"`
	SaveAllRuns                bool                                 `json:"save_all_runs"`
	SnapshotThreshold          uint64                               `json:"snapshot_threshold"`
	RetainEntries              uint64                               `json:"snapshot_retain_entries"`
	CrashQuota                 int                                  `json:"crash_quota"`
	PartitionEnabled           bool                                 `json:"partition_enabled"`
	Workers                    int                                  `json:"workers"`
	ReplayVerify               bool                                 `json:"deterministic_replay_verification"`
	StopOnTarget               bool                                 `json:"stop_on_target"`
	StopOnFailure              bool                                 `json:"stop_on_failure"`
	Subject                    string                               `json:"subject"`
	Config                     cliConfig                            `json:"effective_config"`
	CheckpointResume           bool                                 `json:"checkpoint_resume_supported"`
	LLMCalls                   int                                  `json:"llm_calls"`

	// Mainline protocol-aware local mutation settings.
	MutationAdvisor           string               `json:"mutation_advisor"`
	FocusedGoalA              bool                 `json:"focused_goal_a"`
	FocusedGoalB              bool                 `json:"focused_goal_b"`
	AdvisorPriorityMultiplier int                  `json:"advisor_priority_multiplier"`
	AdvisorLocalActionCap     int                  `json:"advisor_local_action_cap"`
	AdvisorNoProgressCap      int                  `json:"advisor_no_progress_cap"`
	AdvisorQueueLimit         int                  `json:"advisor_queue_limit"`
	AdvisorAblation           raftadvisor.Ablation `json:"advisor_ablation"`
	AdvisorRecordOnly         bool                 `json:"advisor_record_only"`
	BranchEvidenceRecordOnly  bool                 `json:"branch_evidence_record_only"`
}

type onOffFlag struct{ value *bool }

func (flagValue *onOffFlag) String() string {
	if flagValue == nil || flagValue.value == nil {
		return "off"
	}
	if *flagValue.value {
		return "on"
	}
	return "off"
}

func (flagValue *onOffFlag) Set(text string) error {
	if flagValue == nil || flagValue.value == nil {
		return fmt.Errorf("on/off flag has no target")
	}
	switch strings.ToLower(text) {
	case "on", "true", "1":
		*flagValue.value = true
	case "off", "false", "0":
		*flagValue.value = false
	default:
		return fmt.Errorf("expected on/off, got %q", text)
	}
	return nil
}

type goalProgressRecord struct {
	SchemaVersion              string                                   `json:"schema_version"`
	RunID                      string                                   `json:"run_id"`
	CandidateIndex             int                                      `json:"candidate_index"`
	ParentSeedID               string                                   `json:"parent_frontier_seed_id,omitempty"`
	Bindings                   map[goalsearch.Symbol]goalsearch.Binding `json:"bindings"`
	CurrentWaypoint            string                                   `json:"current_waypoint,omitempty"`
	CompletedWaypoints         int                                      `json:"completed_waypoint_count"`
	Distance                   int                                      `json:"distance"`
	Updates                    []goalsearch.ProgressUpdate              `json:"progress_changes"`
	FrontierChanged            bool                                     `json:"frontier_changed"`
	TargetReached              bool                                     `json:"target_reached"`
	RuntimeStatus              engine.Status                            `json:"runtime_status"`
	TLCExecuted                bool                                     `json:"tlc_executed"`
	OracleFindings             int                                      `json:"oracle_findings"`
	Failure                    *core.FailureRecord                      `json:"failure,omitempty"`
	ActionCount                int                                      `json:"action_count"`
	PlanLength                 int                                      `json:"plan_length"`
	ElapsedMilliseconds        int64                                    `json:"elapsed_milliseconds"`
	OnlineOfflineEqual         bool                                     `json:"online_offline_equal"`
	OfflineRecomputeError      string                                   `json:"offline_recompute_error,omitempty"`
	NewFacet                   bool                                     `json:"new_facet"`
	MutationOperator           string                                   `json:"mutation_operator,omitempty"`
	HintStrength               goalsearch.HintStrength                  `json:"hint_strength"`
	SelectedFrontierSeed       bool                                     `json:"selected_frontier_seed"`
	PrefixPreserved            bool                                     `json:"prefix_preserved"`
	WaypointRegression         bool                                     `json:"waypoint_regression"`
	CompletedWaypointDestroyed int                                      `json:"completed_waypoints_destroyed"`
	BugDetected                bool                                     `json:"bug_detected"`
	FailureSignature           *minimize.Signature                      `json:"failure_signature,omitempty"`
	FailureRelation            string                                   `json:"failure_relation,omitempty"`
	Branch                     *goalsearch.BehaviorBranchInstance       `json:"behavior_branch,omitempty"`
	NewRealizedBranch          bool                                     `json:"new_realized_branch"`
}

type branchProgressRecord struct {
	SchemaVersion      string                        `json:"schema_version"`
	RunID              string                        `json:"run_id"`
	CandidateIndex     int                           `json:"candidate_index"`
	PlannedTemplateID  goalsearch.BranchTemplateID   `json:"planned_branch_template_id"`
	PlannedKey         string                        `json:"planned_branch_key"`
	RealizedTemplateID goalsearch.BranchTemplateID   `json:"realized_branch_template_id,omitempty"`
	RealizedKey        string                        `json:"realized_branch_key"`
	RealizedDecidable  bool                          `json:"realized_branch_decidable"`
	Feasibility        goalsearch.BranchFeasibility  `json:"feasibility"`
	Agreement          bool                          `json:"planned_realized_agreement"`
	Deviation          goalsearch.BranchDeviation    `json:"deviation"`
	DeepestWaypoint    int                           `json:"deepest_waypoint"`
	GoalReached        bool                          `json:"goal_reached"`
	BugDetected        bool                          `json:"bug_detected"`
	ActionCount        int                           `json:"action_count"`
	FrontierChanged    bool                          `json:"frontier_changed"`
	EvictedBranches    []goalsearch.BranchTemplateID `json:"evicted_planned_branches,omitempty"`
	NewRealizedBranch  bool                          `json:"new_realized_branch"`
	NewFacet           bool                          `json:"new_facet"`
	StableKey          string                        `json:"stable_key"`
}

type branchAggregate struct {
	Attempts              int `json:"attempts"`
	Decidable             int `json:"decidable"`
	Agreements            int `json:"agreements"`
	Deviations            int `json:"deviations"`
	GoalReached           int `json:"goal_reached"`
	BugDetected           int `json:"bug_detected"`
	Actions               int `json:"actions"`
	DeepestWaypoint       int `json:"deepest_waypoint"`
	FrontierRetained      int `json:"frontier_retained"`
	FrontierEvicted       int `json:"frontier_evicted"`
	PermanentlyInfeasible int `json:"permanently_infeasible"`
}

type branchSearchSummary struct {
	SchemaVersion                  string                                          `json:"schema_version"`
	Enabled                        bool                                            `json:"enabled"`
	PlannedBranchCount             int                                             `json:"planned_branch_count"`
	RealizedBranchCount            int                                             `json:"realized_branch_count"`
	PlannedRealizedPairCount       int                                             `json:"planned_realized_pair_count"`
	SuccessfulBranchCount          int                                             `json:"successful_branch_count"`
	DecidableRuns                  int                                             `json:"decidable_runs"`
	AgreementRate                  float64                                         `json:"planned_realized_agreement_rate"`
	DeviationRate                  float64                                         `json:"branch_deviation_rate"`
	NewBranchWithoutNewFacet       int                                             `json:"new_branch_without_new_facet"`
	NewFacetWithoutNewBranch       int                                             `json:"new_facet_without_new_branch"`
	ByPlannedBranch                map[goalsearch.BranchTemplateID]branchAggregate `json:"by_planned_branch"`
	RealizedDistribution           map[string]int                                  `json:"realized_branch_distribution"`
	PlannedRealizedDistribution    map[string]int                                  `json:"planned_realized_distribution"`
	SuccessfulRealizedDistribution map[string]int                                  `json:"successful_realized_branch_distribution"`
}

type waypointAggregate struct {
	ID                     string `json:"id"`
	Reached                bool   `json:"reached"`
	FirstCandidate         int    `json:"first_candidate,omitempty"`
	FirstCumulativeActions int    `json:"first_cumulative_actions,omitempty"`
	AttemptsBeforeNext     int    `json:"attempts_before_next,omitempty"`
	TransitionSuccess      bool   `json:"transition_success"`
}

type coverageCounts struct {
	Available                   bool           `json:"available"`
	RawTLCStates                int            `json:"raw_tlc_states"`
	V1DistinctStates            int            `json:"v1_distinct_states"`
	V2DistinctStates            int            `json:"v2_distinct_states"`
	Facets                      map[string]int `json:"facets"`
	Interactions                map[string]int `json:"interactions"`
	NewFacetWithoutGoalProgress int            `json:"new_facet_without_goal_progress"`
	GoalProgressWithoutNewFacet int            `json:"goal_progress_without_new_facet"`
	NewWaypointWithoutNewFacet  int            `json:"new_waypoint_without_new_facet"`
	DistanceWithoutNewFacet     int            `json:"distance_improvement_without_new_facet"`
}

type goalDiversitySummary struct {
	InitialPlanKey          string                  `json:"initial_plan_key"`
	FinalPlanKey            string                  `json:"final_plan_key"`
	FinalTraceKey           string                  `json:"final_trace_key"`
	SemanticTraceKey        string                  `json:"semantic_trace_key"`
	GoalProgressSequenceKey string                  `json:"goal_progress_sequence_key"`
	FacetSequenceKey        string                  `json:"facet_sequence_key"`
	MessageQueueShapeKey    string                  `json:"message_queue_shape_key"`
	FrontierPrefixKeys      []string                `json:"frontier_prefix_keys"`
	ModelEventHistogram     map[string]int          `json:"model_event_histogram"`
	ActionTypeHistogram     map[core.ActionKind]int `json:"action_type_histogram"`
}

type goalSearchReport struct {
	SchemaVersion               string                              `json:"schema_version"`
	Settings                    goalSearchSettings                  `json:"settings"`
	StartedAt                   string                              `json:"started_at"`
	FinishedAt                  string                              `json:"finished_at"`
	ElapsedMillis               int64                               `json:"elapsed_milliseconds"`
	Candidates                  int                                 `json:"candidate_plans"`
	Actions                     int                                 `json:"executed_actions"`
	ValidPlans                  int                                 `json:"valid_candidate_plans"`
	InvalidPlans                int                                 `json:"invalid_candidate_plans"`
	CandidateValidityRate       float64                             `json:"candidate_plan_validity_rate"`
	Unexecutable                int                                 `json:"unexecutable_candidate_plans"`
	UnexecutableRate            float64                             `json:"unexecutable_candidate_plan_rate"`
	TargetReached               bool                                `json:"target_reached"`
	FirstTargetCandidate        int                                 `json:"first_target_candidate,omitempty"`
	FirstTargetActions          int                                 `json:"first_target_cumulative_actions,omitempty"`
	FirstTargetMillis           int64                               `json:"first_target_elapsed_milliseconds,omitempty"`
	TargetPlanLength            int                                 `json:"target_plan_length,omitempty"`
	TargetRuntimeStatus         engine.Status                       `json:"target_runtime_status,omitempty"`
	TargetTLCExecuted           bool                                `json:"target_tlc_executed"`
	TargetOracleFindings        int                                 `json:"target_oracle_findings"`
	Waypoints                   []waypointAggregate                 `json:"waypoints"`
	MostStalledWaypoint         string                              `json:"most_stalled_waypoint"`
	ProgressUpdates             int                                 `json:"progress_updates"`
	DistanceImprovements        int                                 `json:"distance_improvements"`
	DistanceWorsenings          int                                 `json:"distance_worsenings"`
	Frontier                    goalsearch.FrontierSnapshot         `json:"frontier"`
	PrefixReplayAttempts        int                                 `json:"prefix_replay_attempts"`
	PrefixReplaySuccess         int                                 `json:"prefix_replay_success"`
	PrefixReplayFailures        map[string]int                      `json:"prefix_replay_failures"`
	PrefixExecutionMismatch     int                                 `json:"prefix_execution_mismatches"`
	FrontierCandidates          int                                 `json:"frontier_generated_candidates"`
	ContributingSeedID          string                              `json:"target_contributing_frontier_seed_id,omitempty"`
	ContributingHandoffSeedID   string                              `json:"target_contributing_handoff_seed_id,omitempty"`
	InitialHandoffSeeds         int                                 `json:"initial_handoff_seed_count"`
	RetainedHandoffSeeds        int                                 `json:"retained_handoff_seed_count"`
	HandoffSeedSelections       map[string]int                      `json:"handoff_seed_selections,omitempty"`
	Mutation                    goalsearch.MutationStats            `json:"goal_mutation"`
	ActionKinds                 map[plan.ActionKind]int             `json:"action_kinds"`
	ConcreteActionKinds         map[core.ActionKind]int             `json:"concrete_action_kinds"`
	GoalAwareHintUses           int                                 `json:"goal_aware_hint_uses"`
	HintStrengthUses            map[goalsearch.HintStrength]int     `json:"hint_strength_uses"`
	FrontierSeedSelections      int                                 `json:"frontier_seed_selections"`
	WaypointRegressions         int                                 `json:"waypoint_regressions"`
	CompletedWaypointsDestroyed int                                 `json:"completed_waypoints_destroyed"`
	LifecycleRejected           int                                 `json:"lifecycle_check_rejections"`
	ForbiddenRejected           int                                 `json:"forbidden_pattern_rejections"`
	PrefixRejected              int                                 `json:"prefix_protection_rejections"`
	MessageSelectionFailures    int                                 `json:"message_selection_failures"`
	Coverage                    coverageCounts                      `json:"coverage"`
	OnlineOfflineMismatches     int                                 `json:"online_offline_mismatches"`
	ExpectedOfflineMapFailures  int                                 `json:"expected_offline_mapping_failures"`
	RuntimeStatuses             map[engine.Status]int               `json:"runtime_statuses"`
	TLCExecutedRuns             int                                 `json:"tlc_executed_runs"`
	OracleFindingRuns           int                                 `json:"oracle_finding_runs"`
	BugDetected                 bool                                `json:"bug_detected"`
	FirstFailureCandidate       int                                 `json:"first_failure_candidate,omitempty"`
	FirstFailureActions         int                                 `json:"first_failure_cumulative_actions,omitempty"`
	FirstFailureMillis          int64                               `json:"first_failure_elapsed_milliseconds,omitempty"`
	FirstFailureLayer           engine.Status                       `json:"first_failure_layer,omitempty"`
	FirstFailureWaypoint        string                              `json:"first_failure_waypoint,omitempty"`
	FirstFailureRelation        string                              `json:"first_failure_relation,omitempty"`
	FirstFailureSignature       *minimize.Signature                 `json:"first_failure_signature,omitempty"`
	FirstFailurePlannedBranch   goalsearch.BranchTemplateID         `json:"first_failure_planned_branch,omitempty"`
	FirstFailureRealizedBranch  goalsearch.BranchTemplateID         `json:"first_failure_realized_branch,omitempty"`
	FirstFailureRealizedKey     string                              `json:"first_failure_realized_branch_key,omitempty"`
	FirstFailureBranchDecidable bool                                `json:"first_failure_branch_decidable"`
	FirstFailureBranchDeviation goalsearch.BranchDeviation          `json:"first_failure_branch_deviation,omitempty"`
	LLMCalls                    int                                 `json:"llm_calls"`
	CheckpointResume            bool                                `json:"checkpoint_resume_supported"`
	MutationMetricNotes         []string                            `json:"mutation_metric_notes"`
	Diversity                   goalDiversitySummary                `json:"seed_diversity"`
	Branch                      branchSearchSummary                 `json:"behavior_branches"`
	BranchFrontier              goalsearch.BranchFrontierSnapshot   `json:"branch_frontier"`
	Evidence                    branchEvidenceSummary               `json:"branch_evidence"`
	EvidenceFrontier            goalsearch.EvidenceFrontierSnapshot `json:"evidence_frontier"`
	BranchBudget                goalsearch.BranchBudgetSummary      `json:"branch_budget"`
}

// goalSearchBootstrap is used only by the explicit breadth/depth command. The
// ordinary goal-search CLI passes nil, preserving its frozen initial Plan and
// Frontier behavior.
type goalSearchBootstrap struct {
	Seeds []goalsearch.FrontierSeed
}

type coverageAccumulator struct {
	raw, v1, v2  map[int64]struct{}
	facets       map[string]map[int64]struct{}
	interactions map[string]map[int64]struct{}
	sequence     []string
}

func newCoverageAccumulator() *coverageAccumulator {
	result := &coverageAccumulator{
		raw: make(map[int64]struct{}), v1: make(map[int64]struct{}), v2: make(map[int64]struct{}),
		facets: make(map[string]map[int64]struct{}), interactions: make(map[string]map[int64]struct{}),
	}
	for _, name := range []string{"election", "replication", "snapshot", "recovery", "network"} {
		result.facets[name] = make(map[int64]struct{})
	}
	return result
}

func goalSearchCommand(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("modelfuzz-ng goal-search", flag.ContinueOnError)
	flags.SetOutput(stderr)
	goalText := flags.String("goal", "", "Behavior Goal ID")
	modeText := flags.String("mode", string(goalsearch.ModeFrontier), "search mode")
	output := flags.String("output", "", "new output directory")
	configPath := flags.String("config", "", "optional JSON runtime/model configuration")
	nodes := flags.Int("nodes", 3, "cluster node count; must match config")
	seed := flags.Int64("seed", 1, "deterministic runtime and mutation seed")
	candidateBudget := flags.Int("candidate-budget", 50, "candidate Plan budget")
	actionBudget := flags.Int("action-budget", 5000, "total executed concrete Action budget")
	maxActions := flags.Int("max-actions-per-plan", 100, "PlanAction limit per candidate")
	waypointBudget := flags.Int("per-waypoint-budget", 25, "candidate attempts before a waypoint is declared stalled")
	topK := flags.Int("frontier-top-k", 4, "retained replayable prefixes per waypoint")
	totalCapacity := flags.Int("total-frontier-capacity", 0, "fixed total Frontier capacity; 0 keeps legacy per-waypoint top-K")
	perBranchMinimum := flags.Int("per-branch-minimum-capacity", 1, "minimum retained seed allocation per represented Branch")
	branchText := flags.String("branch-templates", "", "comma-separated Behavior Branch template IDs")
	allFeasibleBranches := flags.Bool("all-feasible-branches", false, "select every statically feasible Branch for the Goal")
	branchAwarenessText := flags.String("branch-awareness", string(goalsearch.BranchRealizedAware), "planned-only or realized-aware")
	branchAblationText := flags.String("branch-dimension-ablation", string(goalsearch.BranchAblationNone), "none, key-message, heal-timing, lag-construction, or term-advance")
	branchBudgetAllocation := flags.String("branch-budget-allocation", "round-robin", "deterministic Branch budget allocation")
	branchEvidenceText := flags.String("branch-evidence-mode", string(goalsearch.BranchEvidenceOff), "off, partial, or commitment")
	branchFrontierMode := flags.String("branch-frontier-mode", "standard", "standard, diversity, or evidence-aware")
	branchBudgetText := flags.String("branch-budget-mode", string(goalsearch.BranchBudgetRoundRobin), "round-robin or stage-budgeted")
	branchInitialQuota := flags.Int("branch-initial-quota", 2, "equal initial candidate quota per Branch")
	branchSupportedQuota := flags.Int("branch-supported-quota", 2, "additional quota after partial evidence")
	branchCommitmentQuota := flags.Int("branch-commitment-quota", 2, "additional quota after Branch commitment")
	branchNextWaypointQuota := flags.Int("branch-next-waypoint-quota", 1, "additional quota after a new Goal waypoint")
	branchTotalCap := flags.Int("branch-total-cap", 20, "hard candidate cap per Branch")
	evidenceAblation := flags.String("evidence-ablation", "none", "evidence definition ablation label; none keeps the registered catalog")
	evidencePriorityMultiplier := flags.Int("evidence-priority-multiplier", 16, "weak category-weight multiplier used only by Evidence Frontier")
	microProgressText := flags.String("micro-progress-policy", string(goalsearch.MicroProgressLegacy), "legacy, necessary-only, or off")
	formationFailureReport := flags.Bool("formation-failure-report", false, "write deterministic Failure-to-Form records")
	hintText := flags.String("hint-strength", "", "goal hint strength: none, weak, or strong")
	distanceText := flags.String("distance-mode", string(goalsearch.DistanceStaged), "progress mode: boolean-only or staged-distance")
	strictTLC := flags.Bool("strict-tlc", false, "require controlled TLC execution")
	tlcAddress := flags.String("tlc", "", "controlled TLC address")
	goalAware := flags.Bool("goal-aware-mutation", true, "enable registered goal-aware local operators")
	preservePrefix := flags.Bool("prefix-preservation", true, "preserve frontier prefix")
	saveAll := flags.Bool("save-all-runs", true, "save standard artifacts for every candidate")
	snapshotThreshold := flags.Uint64("snapshot-threshold", 4, "etcd-raft snapshot threshold")
	retainEntries := flags.Uint64("retain-entries", 1, "entries retained after compaction")
	crashQuota := flags.Int("crash-quota", 2, "maximum crash episodes used by local mutation")
	partitionEnabled := flags.Bool("partition-enabled", true, "allow partition goal/operator")
	workers := flags.Int("workers", 1, "worker count (prototype currently requires 1)")
	replayVerify := flags.Bool("replay-verify", true, "verify selected frontier prefix before mutation")
	stopOnTarget := flags.Bool("stop-on-target", true, "stop the campaign after the Goal is first reached")
	stopOnFailure := flags.Bool("stop-on-failure", false, "stop the campaign after the first detected failure")
	mutationAdvisor := flags.String("mutation-advisor", "off", "off or raft-focused")
	focusedGoalA := new(bool)
	focusedGoalB := new(bool)
	*focusedGoalA, *focusedGoalB = true, true
	flags.Var(&onOffFlag{value: focusedGoalA}, "focused-goal-a", "on/off: enable focused Goal A advice")
	flags.Var(&onOffFlag{value: focusedGoalB}, "focused-goal-b", "on/off: enable focused Goal B advice")
	advisorPriority := flags.Int("advisor-priority-multiplier", 16, "focused candidate priority multiplier")
	advisorLocalCap := flags.Int("advisor-local-action-cap", 9, "maximum adjacent local actions per decision")
	advisorNoProgress := flags.Int("advisor-no-progress-cap", 8, "fallback after this many no-progress decisions")
	advisorQueueLimit := flags.Int("advisor-queue-limit", 64, "queue pressure threshold")
	advisorAblationText := flags.String("advisor-ablation", string(raftadvisor.AblationNone), "focused mutation ablation")
	advisorRecordOnly := flags.Bool("advisor-record-only", false, "record advice without changing mutation")
	branchEvidenceRecordOnly := flags.Bool("branch-evidence-record-only", false, "record Branch/Evidence without changing search")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("无法识别的位置参数: %v", flags.Args())
	}
	if *goalText == "" || *output == "" {
		flags.Usage()
		return fmt.Errorf("-goal and -output are required")
	}
	mode := goalsearch.SearchMode(*modeText)
	if err := mode.Validate(); err != nil {
		return err
	}
	if *hintText == "" {
		if mode == goalsearch.ModeUnguided || mode == goalsearch.ModeDirectedSnapshot {
			*hintText = string(goalsearch.HintNone)
		} else {
			*hintText = string(goalsearch.HintStrong)
		}
	}
	hintStrength := goalsearch.HintStrength(*hintText)
	if err := hintStrength.Validate(); err != nil {
		return err
	}
	distanceMode := goalsearch.DistanceMode(*distanceText)
	if err := distanceMode.Validate(); err != nil {
		return err
	}
	if *candidateBudget <= 0 || *actionBudget <= 0 || *maxActions <= 0 ||
		*waypointBudget <= 0 || *topK <= 0 || *workers <= 0 || *crashQuota <= 0 {
		return fmt.Errorf("goal-search budgets, top-K, crash quota, and workers must be positive")
	}
	if *evidencePriorityMultiplier <= 0 {
		return fmt.Errorf("evidence priority multiplier must be positive")
	}
	if *mutationAdvisor != "off" && *mutationAdvisor != "raft-focused" {
		return fmt.Errorf("-mutation-advisor must be off or raft-focused")
	}
	advisorConfig := raftadvisor.Config{
		GoalAEnabled: *focusedGoalA, GoalBEnabled: *focusedGoalB,
		PriorityMultiplier: *advisorPriority, LocalActionCap: *advisorLocalCap,
		NoProgressCap: *advisorNoProgress, QueueLimit: *advisorQueueLimit,
		Ablation: raftadvisor.Ablation(*advisorAblationText),
	}
	if err := advisorConfig.Validate(); err != nil {
		return err
	}
	if *mutationAdvisor == "raft-focused" && hintStrength != goalsearch.HintWeak {
		return fmt.Errorf("raft-focused advisor requires -hint-strength=weak")
	}
	if *advisorRecordOnly && *mutationAdvisor == "off" {
		return fmt.Errorf("-advisor-record-only requires -mutation-advisor=raft-focused")
	}
	if *totalCapacity < 0 || *perBranchMinimum <= 0 {
		return fmt.Errorf("total Frontier capacity cannot be negative and per-branch minimum must be positive")
	}
	branchAwareness := goalsearch.BranchAwareness(*branchAwarenessText)
	if err := branchAwareness.Validate(); err != nil {
		return err
	}
	branchAblation := goalsearch.BranchDimensionAblation(*branchAblationText)
	if err := branchAblation.Validate(); err != nil {
		return err
	}
	branchEvidenceMode := goalsearch.BranchEvidenceMode(*branchEvidenceText)
	if err := branchEvidenceMode.Validate(); err != nil {
		return err
	}
	branchBudgetMode := goalsearch.BranchBudgetMode(*branchBudgetText)
	if err := branchBudgetMode.Validate(); err != nil {
		return err
	}
	microProgressPolicy := goalsearch.MicroProgressPolicy(*microProgressText)
	if err := microProgressPolicy.Validate(); err != nil {
		return err
	}
	stageBudget := goalsearch.StageBudgetConfig{
		InitialQuota: *branchInitialQuota, SupportedQuota: *branchSupportedQuota,
		CommitmentQuota: *branchCommitmentQuota, NextWaypointQuota: *branchNextWaypointQuota,
		PerBranchTotalCap: *branchTotalCap,
	}
	if err := stageBudget.Validate(); err != nil {
		return err
	}
	if *branchBudgetAllocation != "round-robin" {
		return fmt.Errorf("-branch-budget-allocation is a compatibility alias and must remain round-robin; use -branch-budget-mode")
	}
	switch *branchFrontierMode {
	case "standard", "diversity", "evidence-aware":
	default:
		return fmt.Errorf("unsupported branch frontier mode %q", *branchFrontierMode)
	}
	if *evidenceAblation != "none" {
		return fmt.Errorf("unsupported evidence ablation %q; prototype currently implements only none", *evidenceAblation)
	}
	if *workers != 1 {
		return fmt.Errorf("goal-search prototype currently requires -workers=1 for deterministic feedback ordering")
	}
	switch mode {
	case goalsearch.ModeUnguided:
		if *goalAware || *preservePrefix || hintStrength != goalsearch.HintNone {
			return fmt.Errorf("%s requires goal-aware=false, prefix-preservation=false, and hint-strength=none", mode)
		}
	case goalsearch.ModeGoalAware:
		if !*goalAware || *preservePrefix || hintStrength == goalsearch.HintNone {
			return fmt.Errorf("%s requires -goal-aware-mutation=true and -prefix-preservation=false", mode)
		}
	case goalsearch.ModeFrontier:
		if !*goalAware || !*preservePrefix || hintStrength == goalsearch.HintNone {
			return fmt.Errorf("%s requires goal-aware mutation and prefix preservation", mode)
		}
	case goalsearch.ModeDiversityFrontier:
		if !*goalAware || !*preservePrefix || hintStrength == goalsearch.HintNone {
			return fmt.Errorf("%s requires goal-aware mutation, prefix preservation, and weak/strong hints", mode)
		}
		if *totalCapacity <= 0 {
			return fmt.Errorf("%s requires -total-frontier-capacity > 0", mode)
		}
	case goalsearch.ModeEvidenceFrontier:
		if !*goalAware || !*preservePrefix || hintStrength == goalsearch.HintNone {
			return fmt.Errorf("%s requires goal-aware mutation, prefix preservation, and weak/strong hints", mode)
		}
		if *totalCapacity <= 0 {
			return fmt.Errorf("%s requires -total-frontier-capacity > 0", mode)
		}
		if branchEvidenceMode == goalsearch.BranchEvidenceOff {
			return fmt.Errorf("%s requires -branch-evidence-mode=partial or commitment", mode)
		}
		if *branchFrontierMode != "evidence-aware" {
			return fmt.Errorf("%s requires -branch-frontier-mode=evidence-aware", mode)
		}
	case goalsearch.ModeFrontierNoPrefix:
		if !*goalAware || *preservePrefix || hintStrength == goalsearch.HintNone {
			return fmt.Errorf("%s requires goal-aware mutation and prefix-preservation=false", mode)
		}
	case goalsearch.ModeDirectedSnapshot:
		if *goalAware || *preservePrefix || hintStrength != goalsearch.HintNone {
			return fmt.Errorf("%s requires goal-aware=false, prefix-preservation=false, and hint-strength=none", mode)
		}
		if goalsearch.GoalID(*goalText) != goalsearch.GoalSnapshotCatchUpAfterPartition {
			return fmt.Errorf("%s only supports %s", mode, goalsearch.GoalSnapshotCatchUpAfterPartition)
		}
		if *candidateBudget != 1 {
			return fmt.Errorf("%s requires -candidate-budget=1", mode)
		}
	}
	config, err := loadCLIConfig(*configPath)
	if err != nil {
		return err
	}
	if len(config.Raft.NodeIDs) != *nodes {
		return fmt.Errorf("-nodes=%d differs from configured Raft nodes=%d", *nodes, len(config.Raft.NodeIDs))
	}
	definition, err := goalsearch.Definition(goalsearch.GoalID(*goalText), *nodes)
	if err != nil {
		return err
	}
	if required := definition.ConfigurationConstraints.ModelProfile; required != "" &&
		config.Model.EffectiveProfile() != required {
		return fmt.Errorf(
			"goal %q requires model profile %q; configured profile is %q",
			definition.GoalID, required, config.Model.EffectiveProfile(),
		)
	}
	if definition.ConfigurationConstraints.RequiresSnapshot && *snapshotThreshold == 0 {
		return fmt.Errorf("goal %q requires a non-zero snapshot threshold", definition.GoalID)
	}
	if definition.ConfigurationConstraints.RequiresRetainLessMax &&
		*retainEntries >= config.Model.MaxLogIndex {
		return fmt.Errorf(
			"goal %q requires retain-entries (%d) below max_log_index (%d)",
			definition.GoalID, *retainEntries, config.Model.MaxLogIndex,
		)
	}
	if definition.GoalID == goalsearch.GoalSnapshotCatchUpAfterPartition && !*partitionEnabled {
		return fmt.Errorf("goal %q requires -partition-enabled=true", definition.GoalID)
	}
	branchTemplates, branchFeasibility, err := selectGoalBranches(
		definition.GoalID, *branchText, *allFeasibleBranches,
		goalsearch.BranchEnvironment{
			NodeCount: *nodes, ModelProfile: config.Model.EffectiveProfile(),
			SnapshotThreshold: *snapshotThreshold, PartitionEnabled: *partitionEnabled,
		},
	)
	if err != nil {
		return err
	}
	if (mode == goalsearch.ModeDiversityFrontier ||
		mode == goalsearch.ModeEvidenceFrontier) && len(branchTemplates) == 0 {
		return fmt.Errorf("%s requires at least one feasible Behavior Branch", mode)
	}
	if branchBudgetMode == goalsearch.BranchBudgetStageBudgeted &&
		len(branchTemplates) == 0 {
		return fmt.Errorf("stage-budgeted Branch allocation requires at least one feasible Branch")
	}
	if *perBranchMinimum > max(1, *totalCapacity) && *totalCapacity > 0 {
		return fmt.Errorf("per-branch minimum %d exceeds total Frontier capacity %d",
			*perBranchMinimum, *totalCapacity)
	}
	config.Seed = *seed
	config.ExecutionID = core.ExecutionID(fmt.Sprintf("goal-%s-%s-%d", definition.GoalID, mode, *seed))
	config.Engine.MaxPlanActions = *maxActions
	config.Raft.Snapshot.Threshold = *snapshotThreshold
	config.Raft.Snapshot.RetainEntries = *retainEntries
	if *strictTLC {
		if *tlcAddress != "" {
			config.TLC.Address = *tlcAddress
		}
		if config.TLC.Address == "" {
			return fmt.Errorf("-strict-tlc=true requires -tlc or a configured TLC address")
		}
	} else {
		config.TLC.Address = ""
	}
	if err := validateAlignedNodes(config.Raft.NodeIDs, config.Model.NodeIDs); err != nil {
		return err
	}
	if err := validateTLCModelBounds(ctx, config, stderr); err != nil {
		return err
	}
	settings := goalSearchSettings{
		SchemaVersion: goalsearch.SchemaVersion, ReleaseVersion: releaseVersion,
		GoalID: definition.GoalID, Mode: mode, NodeCount: *nodes, Seed: *seed,
		CandidateBudget: *candidateBudget, ActionBudget: *actionBudget,
		MaxActionsPerPlan: *maxActions, PerWaypointBudget: *waypointBudget,
		FrontierTopK: *topK, HintStrength: hintStrength, DistanceMode: distanceMode,
		TotalFrontierCapacity: *totalCapacity, PerBranchMinimum: *perBranchMinimum,
		BranchTemplateIDs:   branchTemplateIDs(branchTemplates),
		AllFeasibleBranches: *allFeasibleBranches, BranchAwareness: branchAwareness,
		BranchDimensionAblation:    branchAblation,
		BranchBudgetAllocation:     *branchBudgetAllocation,
		BranchEvidenceMode:         branchEvidenceMode,
		BranchFrontierMode:         *branchFrontierMode,
		BranchBudgetMode:           branchBudgetMode,
		StageBudget:                stageBudget,
		EvidenceAblation:           *evidenceAblation,
		EvidencePriorityMultiplier: *evidencePriorityMultiplier,
		MicroProgressPolicy:        microProgressPolicy,
		FormationFailureReport:     *formationFailureReport,
		BranchFeasibility:          branchFeasibility,
		StrictTLC:                  *strictTLC, TLCAddress: config.TLC.Address,
		GoalAwareMutation: *goalAware, PrefixPreservation: *preservePrefix,
		SaveAllRuns: *saveAll, SnapshotThreshold: *snapshotThreshold,
		RetainEntries: *retainEntries, CrashQuota: *crashQuota,
		PartitionEnabled: *partitionEnabled, Workers: *workers,
		ReplayVerify: *replayVerify, StopOnTarget: *stopOnTarget, StopOnFailure: *stopOnFailure,
		Subject: goalSubject(config), Config: config, CheckpointResume: false, LLMCalls: 0,
		MutationAdvisor: *mutationAdvisor, FocusedGoalA: *focusedGoalA,
		FocusedGoalB: *focusedGoalB, AdvisorPriorityMultiplier: *advisorPriority,
		AdvisorLocalActionCap: *advisorLocalCap, AdvisorNoProgressCap: *advisorNoProgress,
		AdvisorQueueLimit: *advisorQueueLimit, AdvisorAblation: raftadvisor.Ablation(*advisorAblationText),
		AdvisorRecordOnly:        *advisorRecordOnly,
		BranchEvidenceRecordOnly: *branchEvidenceRecordOnly,
	}
	if err := createOutputDirectory(*output); err != nil {
		return err
	}
	budgetLedger, err := persistence.OpenJournal(
		filepath.Join(*output, "branch-budget-ledger.jsonl"),
	)
	if err != nil {
		return err
	}
	if err := budgetLedger.Close(); err != nil {
		return err
	}
	if err := writeJSONFile(filepath.Join(*output, "goal-definition.json"), definition); err != nil {
		return err
	}
	if err := writeJSONFile(filepath.Join(*output, "goal-settings.json"), settings); err != nil {
		return err
	}
	advisorSettingsArtifact := map[string]any{
		"schema_version":              protocolmutation.SchemaVersion,
		"mode":                        *mutationAdvisor,
		"record_only":                 *advisorRecordOnly,
		"branch_evidence_record_only": *branchEvidenceRecordOnly,
		"raft":                        advisorConfig,
	}
	if err := writeJSONFile(filepath.Join(*output, "mutation-advisor-settings.json"), advisorSettingsArtifact); err != nil {
		return err
	}
	if err := writeJSONFile(filepath.Join(*output, "branch-catalog.json"), goalsearch.BranchCatalog()); err != nil {
		return err
	}
	if err := writeJSONFile(
		filepath.Join(*output, "branch-evidence-catalog.json"),
		goalsearch.BranchEvidenceCatalog(),
	); err != nil {
		return err
	}
	if err := writeJSONFile(
		filepath.Join(*output, "micro-progress-registry.json"),
		goalsearch.MicroProgressRegistry(),
	); err != nil {
		return err
	}
	branchSettings := struct {
		SchemaVersion     string             `json:"schema_version"`
		GoalSchemaVersion string             `json:"goal_schema_version"`
		Settings          goalSearchSettings `json:"settings"`
	}{
		SchemaVersion:     goalsearch.BranchSchemaVersion,
		GoalSchemaVersion: goalsearch.SchemaVersion,
		Settings:          settings,
	}
	if err := writeJSONFile(filepath.Join(*output, "branch-settings.json"), branchSettings); err != nil {
		return err
	}
	if err := writeJSONFile(filepath.Join(*output, "branch-feasibility.json"), branchFeasibility); err != nil {
		return err
	}
	report, searchErr := executeGoalSearch(ctx, *output, config, definition, settings, stderr)
	writeErr := writeJSONFile(filepath.Join(*output, "final-report.json"), report)
	_, outputErr := fmt.Fprintf(stdout,
		"Goal Search 结束: goal=%s mode=%s reached=%v candidates=%d actions=%d output=%s\n",
		definition.GoalID, mode, report.TargetReached, report.Candidates, report.Actions, *output,
	)
	return errors.Join(searchErr, writeErr, outputErr)
}

func executeGoalSearch(
	ctx context.Context,
	output string,
	config cliConfig,
	definition goalsearch.BehaviorGoalDefinition,
	settings goalSearchSettings,
	stderr io.Writer,
) (goalSearchReport, error) {
	return executeGoalSearchWithBootstrap(
		ctx, output, config, definition, settings, nil, stderr,
	)
}

func executeGoalSearchWithBootstrap(
	ctx context.Context,
	output string,
	config cliConfig,
	definition goalsearch.BehaviorGoalDefinition,
	settings goalSearchSettings,
	bootstrap *goalSearchBootstrap,
	stderr io.Writer,
) (goalSearchReport, error) {
	started := time.Now()
	report := goalSearchReport{
		SchemaVersion: goalsearch.SchemaVersion, Settings: settings,
		StartedAt:            started.UTC().Format(time.RFC3339Nano),
		FirstTargetCandidate: -1, PrefixReplayFailures: make(map[string]int),
		ActionKinds:           make(map[plan.ActionKind]int),
		ConcreteActionKinds:   make(map[core.ActionKind]int),
		RuntimeStatuses:       make(map[engine.Status]int),
		HintStrengthUses:      make(map[goalsearch.HintStrength]int),
		HandoffSeedSelections: make(map[string]int),
		Mutation: goalsearch.MutationStats{
			Operators:        make(map[string]int),
			HintStrengthUses: make(map[goalsearch.HintStrength]int),
		},
		CheckpointResume: false, LLMCalls: 0,
		MutationMetricNotes: []string{
			"mutation.Random 内部重试未暴露逐次 lifecycle/forbidden 拒绝原因；拒绝计数只包含 goal-search 外层可观察结果",
		},
		Diversity: goalDiversitySummary{
			ModelEventHistogram: make(map[string]int),
			ActionTypeHistogram: make(map[core.ActionKind]int),
		},
		Branch: branchSearchSummary{
			SchemaVersion:                  goalsearch.BranchSchemaVersion,
			Enabled:                        len(settings.BranchTemplateIDs) > 0,
			ByPlannedBranch:                make(map[goalsearch.BranchTemplateID]branchAggregate),
			RealizedDistribution:           make(map[string]int),
			PlannedRealizedDistribution:    make(map[string]int),
			SuccessfulRealizedDistribution: make(map[string]int),
		},
	}
	for _, waypoint := range definition.Waypoints {
		report.Waypoints = append(report.Waypoints, waypointAggregate{
			ID: waypoint.ID, FirstCandidate: -1,
		})
	}
	frontier, _ := goalsearch.NewFrontier(settings.FrontierTopK)
	var capacityFrontier *goalsearch.CapacityFrontier
	if settings.TotalFrontierCapacity > 0 &&
		settings.Mode != goalsearch.ModeDiversityFrontier &&
		settings.Mode != goalsearch.ModeEvidenceFrontier {
		capacityFrontier, _ = goalsearch.NewCapacityFrontier(settings.TotalFrontierCapacity)
	}
	var diversityFrontier *goalsearch.DiversityFrontier
	if settings.Mode == goalsearch.ModeDiversityFrontier {
		diversityFrontier, _ = goalsearch.NewDiversityFrontier(
			settings.TotalFrontierCapacity, settings.PerBranchMinimum, settings.BranchAwareness,
		)
	}
	var evidenceFrontier *goalsearch.EvidenceFrontier
	if settings.Mode == goalsearch.ModeEvidenceFrontier {
		supportedLimit := max(1, settings.TotalFrontierCapacity/2)
		if settings.TotalFrontierCapacity > 1 {
			supportedLimit = min(supportedLimit, settings.TotalFrontierCapacity-1)
		}
		var evidenceFrontierErr error
		evidenceFrontier, evidenceFrontierErr = goalsearch.NewEvidenceFrontier(
			settings.TotalFrontierCapacity, supportedLimit,
		)
		if evidenceFrontierErr != nil {
			return report, evidenceFrontierErr
		}
	}
	handoffParents := make(map[string]string)
	if bootstrap != nil {
		report.InitialHandoffSeeds = len(bootstrap.Seeds)
		for _, seed := range bootstrap.Seeds {
			switch {
			case evidenceFrontier != nil:
				_, _ = evidenceFrontier.Consider(seed)
			case diversityFrontier != nil:
				_, _ = diversityFrontier.Consider(seed)
			case capacityFrontier != nil:
				_, _ = capacityFrontier.Consider(seed)
			default:
				_, _ = frontier.Consider(seed)
			}
			handoffParents[seed.ID] = seed.ParentID
		}
		switch {
		case evidenceFrontier != nil:
			report.RetainedHandoffSeeds = len(evidenceFrontier.Snapshot().Seeds)
		case diversityFrontier != nil:
			report.RetainedHandoffSeeds = len(diversityFrontier.Snapshot().Seeds)
		case capacityFrontier != nil:
			report.RetainedHandoffSeeds = len(capacityFrontier.Snapshot().Seeds)
		default:
			report.RetainedHandoffSeeds = len(frontier.Snapshot().Seeds)
		}
	}
	branchTemplates := make([]goalsearch.BehaviorBranchTemplate, 0, len(settings.BranchTemplateIDs))
	for _, id := range settings.BranchTemplateIDs {
		template, templateErr := goalsearch.BranchTemplate(id)
		if templateErr != nil {
			return report, templateErr
		}
		branchTemplates = append(branchTemplates, template)
	}
	var stageAllocator *goalsearch.StageBudgetAllocator
	if settings.BranchBudgetMode == goalsearch.BranchBudgetStageBudgeted {
		var allocatorErr error
		stageAllocator, allocatorErr = goalsearch.NewStageBudgetAllocator(
			settings.BranchTemplateIDs, settings.StageBudget,
		)
		if allocatorErr != nil {
			return report, allocatorErr
		}
	}
	coverage := newCoverageAccumulator()
	progressJournal, err := persistence.OpenJournal(filepath.Join(output, "goal-progress.jsonl"))
	if err != nil {
		return report, err
	}
	defer func() { _ = progressJournal.Close() }()
	branchInstances, err := persistence.OpenJournal(filepath.Join(output, "branch-instances.jsonl"))
	if err != nil {
		return report, err
	}
	defer func() { _ = branchInstances.Close() }()
	branchProgress, err := persistence.OpenJournal(filepath.Join(output, "branch-progress.jsonl"))
	if err != nil {
		return report, err
	}
	defer func() { _ = branchProgress.Close() }()
	branchEvidence, err := persistence.OpenJournal(filepath.Join(output, "branch-evidence.jsonl"))
	if err != nil {
		return report, err
	}
	defer func() { _ = branchEvidence.Close() }()
	advisorJournal, err := persistence.OpenJournal(filepath.Join(output, "mutation-advisor-decisions.jsonl"))
	if err != nil {
		return report, err
	}
	defer func() { _ = advisorJournal.Close() }()
	branchCommitments, err := persistence.OpenJournal(filepath.Join(output, "branch-commitments.jsonl"))
	if err != nil {
		return report, err
	}
	defer func() { _ = branchCommitments.Close() }()
	formationFailures, err := persistence.OpenJournal(
		filepath.Join(output, "branch-formation-failures.jsonl"),
	)
	if err != nil {
		return report, err
	}
	defer func() { _ = formationFailures.Close() }()
	if err := os.MkdirAll(filepath.Join(output, "frontier-seeds"), 0o755); err != nil {
		return report, err
	}
	if err := os.MkdirAll(filepath.Join(output, "replay-verification"), 0o755); err != nil {
		return report, err
	}
	sequence, err := goalsearch.InitialPlan(config.Raft.NodeIDs, settings.MaxActionsPerPlan)
	if err != nil {
		return report, err
	}
	report.Diversity.InitialPlanKey = goalsearch.PlanKey(sequence)
	partitionPairPercent := 0
	if settings.PartitionEnabled {
		partitionPairPercent = 10
	}
	randomMutator, err := mutation.NewRandom(mutation.RandomConfig{
		NodeIDs: config.Raft.NodeIDs, MaxValue: config.Model.MaxValue,
		MaxTicks: config.Resolver.MaxAdvanceTicks, MaxActions: settings.MaxActionsPerPlan,
		MaxCrashed: max(1, len(config.Raft.NodeIDs)-2), MaxCrashEpisodes: settings.CrashQuota,
		LifecycleCooldown: 1, CrashRestartPairPercent: 10,
		PartitionHealPairPercent: partitionPairPercent,
	})
	if err != nil {
		return report, err
	}
	var focusedAdvisor protocolmutation.Advisor
	if settings.MutationAdvisor == "raft-focused" {
		focused, advisorErr := raftadvisor.New(raftadvisor.Config{
			GoalAEnabled: settings.FocusedGoalA, GoalBEnabled: settings.FocusedGoalB,
			PriorityMultiplier: settings.AdvisorPriorityMultiplier,
			LocalActionCap:     settings.AdvisorLocalActionCap,
			NoProgressCap:      settings.AdvisorNoProgressCap,
			QueueLimit:         settings.AdvisorQueueLimit, Ablation: settings.AdvisorAblation,
		})
		if advisorErr != nil {
			return report, advisorErr
		}
		focusedAdvisor = focused
	}
	advisorDecisions := make([]protocolmutation.Decision, 0, settings.CandidateBudget)
	var parentEval goalsearch.EvaluationResult
	var parentSeedID string
	globalBest := goalsearch.GoalProgress{
		DistanceToCurrent: 99, StableKey: "~",
	}
	cumulativeActions := 0
	stalledAt := make(map[string]int)
	progressPath := make([]goalsearch.ProgressPoint, 0, settings.CandidateBudget)
	seenRealizedBranches := make(map[string]struct{})
	seenEvidence := make(map[string]struct{})
	seenCommitments := make(map[string]struct{})
	for candidate := 0; candidate < settings.CandidateBudget && cumulativeActions < settings.ActionBudget; candidate++ {
		if err := ctx.Err(); err != nil {
			return report, err
		}
		operator := "common-initial"
		var pendingAdvisorDecision *protocolmutation.Decision
		var plannedBranch *goalsearch.BehaviorBranchTemplate
		if len(branchTemplates) > 0 {
			selected := branchTemplates[candidate%len(branchTemplates)]
			if stageAllocator != nil {
				selectedID, ok := stageAllocator.Next(candidate)
				if !ok {
					break
				}
				for _, template := range branchTemplates {
					if template.BranchTemplateID == selectedID {
						selected = template
						break
					}
				}
			}
			plannedBranch = &selected
		}
		var selectedPrefix *goalsearch.FrontierSeed
		var selectedParent *goalsearch.FrontierSeed
		if candidate > 0 || report.RetainedHandoffSeeds > 0 {
			switch settings.Mode {
			case goalsearch.ModeUnguided:
				mutated, mutateErr := randomMutator.Mutate(ctx, mutation.Request{
					Entry: corpus.Entry{
						ID:   fmt.Sprintf("goal-parent-%06d", candidate-1),
						Plan: sequence, Seed: settings.Seed, RunIndex: candidate - 1,
					},
					Count: 1, Seed: settings.Seed + int64(candidate)*7919,
				})
				report.Mutation.Attempts++
				if mutateErr != nil {
					report.InvalidPlans++
					continue
				}
				sequence = mutated[0]
				operator = sequence.Metadata["mutation_operation"]
				report.Mutation.Produced++
				report.Mutation.Operators[operator]++
			case goalsearch.ModeGoalAware:
				options := focusedMutationOptions(
					settings, plannedBranch, focusedAdvisor, candidate,
					stalledAt[parentEval.Instance.Progress.CurrentWaypointID],
				)
				mutated, stats, mutateErr := goalsearch.MutateTowardWaypointWithOptions(
					definition, sequence, parentEval, settings.Seed+int64(candidate)*7919,
					settings.MaxActionsPerPlan, options,
				)
				mergeMutationStats(&report.Mutation, stats)
				if mutateErr != nil {
					report.InvalidPlans++
					if bytes.Contains([]byte(mutateErr.Error()), []byte("message")) {
						report.MessageSelectionFailures++
					}
					continue
				}
				sequence, operator = mutated.Plan, mutated.Operator
				pendingAdvisorDecision = mutated.AdvisorDecision
				report.GoalAwareHintUses++
				report.HintStrengthUses[settings.HintStrength]++
			case goalsearch.ModeFrontier, goalsearch.ModeDiversityFrontier,
				goalsearch.ModeEvidenceFrontier,
				goalsearch.ModeFrontierNoPrefix:
				var seed goalsearch.FrontierSeed
				var ok bool
				switch {
				case evidenceFrontier != nil:
					seed, ok = evidenceFrontier.Select(candidate)
				case diversityFrontier != nil:
					seed, ok = diversityFrontier.Select(candidate)
				case capacityFrontier != nil:
					seed, ok = capacityFrontier.Select(candidate)
				default:
					seed, ok = frontier.Select(candidate)
				}
				if !ok {
					options := focusedMutationOptions(
						settings, plannedBranch, focusedAdvisor, candidate,
						stalledAt[parentEval.Instance.Progress.CurrentWaypointID],
					)
					mutated, stats, mutateErr := goalsearch.MutateTowardWaypointWithOptions(
						definition, sequence, parentEval, settings.Seed+int64(candidate)*7919,
						settings.MaxActionsPerPlan, options,
					)
					mergeMutationStats(&report.Mutation, stats)
					if mutateErr != nil {
						report.InvalidPlans++
						continue
					}
					sequence, operator = mutated.Plan, mutated.Operator
					pendingAdvisorDecision = mutated.AdvisorDecision
				} else {
					parentSeedID = seed.ID
					report.FrontierSeedSelections++
					if _, handoff := handoffParents[seed.ID]; handoff {
						report.HandoffSeedSelections[seed.ID]++
					}
					selected := seed
					selectedParent = &selected
					if settings.Mode == goalsearch.ModeFrontier ||
						settings.Mode == goalsearch.ModeDiversityFrontier ||
						settings.Mode == goalsearch.ModeEvidenceFrontier {
						selectedPrefix = &selected
					}
					if (settings.Mode == goalsearch.ModeFrontier ||
						settings.Mode == goalsearch.ModeDiversityFrontier ||
						settings.Mode == goalsearch.ModeEvidenceFrontier) && settings.ReplayVerify {
						report.PrefixReplayAttempts++
						replay, replayErr := verifyGoalPrefix(ctx, config, seed, stderr)
						verification := struct {
							SchemaVersion string          `json:"schema_version"`
							Candidate     int             `json:"candidate_index"`
							SeedID        string          `json:"frontier_seed_id"`
							Result        tracepkg.Result `json:"result"`
							Error         string          `json:"error,omitempty"`
						}{
							SchemaVersion: goalsearch.SchemaVersion,
							Candidate:     candidate,
							SeedID:        seed.ID,
							Result:        replay,
						}
						if replayErr != nil {
							verification.Error = replayErr.Error()
						}
						if writeErr := writeJSONFile(
							filepath.Join(output, "replay-verification",
								fmt.Sprintf("candidate-%06d.json", candidate)),
							verification,
						); writeErr != nil {
							return report, writeErr
						}
						if replayErr != nil || replay.Status != tracepkg.StatusCompleted {
							reason := "diverged"
							if replayErr != nil {
								reason = replayErr.Error()
							}
							report.PrefixReplayFailures[reason]++
							report.PrefixRejected++
							continue
						}
						report.PrefixReplaySuccess++
						seed.ReplayVerified = true
						seed.ReplayStatus = string(replay.Status)
						seed.ReplayMatchedSteps = replay.MatchedSteps
					}
					seedEval := parentEval
					seedEval.Instance = seed.Instance
					seedEval.Instance.Progress = seed.Progress
					seedEval.Instance.Bindings = seed.Bindings
					seedEval.PrefixEndActionIndex = seed.PrefixPlanEnd
					seedEval.FinalObservation = seed.PrefixObservation
					seedEval.PrefixObservation = seed.PrefixObservation
					options := focusedMutationOptions(
						settings, plannedBranch, focusedAdvisor, candidate,
						stalledAt[seedEval.Instance.Progress.CurrentWaypointID],
					)
					options.PreservePrefix = settings.Mode == goalsearch.ModeFrontier ||
						settings.Mode == goalsearch.ModeDiversityFrontier ||
						settings.Mode == goalsearch.ModeEvidenceFrontier
					options.AllowWholePlanMutation = settings.Mode == goalsearch.ModeFrontierNoPrefix
					mutated, stats, mutateErr := goalsearch.MutateTowardWaypointWithOptions(
						definition, seed.PrefixPlan, seedEval,
						settings.Seed+int64(candidate)*7919, settings.MaxActionsPerPlan,
						options,
					)
					mergeMutationStats(&report.Mutation, stats)
					if mutateErr != nil {
						report.InvalidPlans++
						report.PrefixRejected++
						continue
					}
					sequence, operator = mutated.Plan, mutated.Operator
					pendingAdvisorDecision = mutated.AdvisorDecision
					report.FrontierCandidates++
				}
				report.GoalAwareHintUses++
				report.HintStrengthUses[settings.HintStrength]++
			}
		}
		if err := sequence.Validate(); err != nil {
			report.InvalidPlans++
			continue
		}
		runConfig := config
		if selectedPrefix != nil {
			// A handed-off prefix was produced under the frozen Global runtime
			// identity. Preserve that identity so the concrete prefix, including
			// MessageID allocation, remains exactly replayable. Mutation RNG and
			// campaign accounting continue to use settings.Seed.
			runConfig.Seed = selectedPrefix.RuntimeSeed
			runConfig.ExecutionID = selectedPrefix.ExecutionID
		}
		report.ValidPlans++
		for _, action := range sequence.Actions {
			report.ActionKinds[action.Kind]++
		}
		runID := fmt.Sprintf("candidate-%06d", candidate)
		evaluator, evalErr := goalsearch.NewEvaluatorWithDistance(
			definition, runID, true, settings.DistanceMode,
		)
		if evalErr != nil {
			return report, evalErr
		}
		runner, buildErr := buildEngine(runConfig, stderr)
		if buildErr != nil {
			return report, buildErr
		}
		runStarted := time.Now()
		var result engine.Result
		var runErr error
		if settings.Mode == goalsearch.ModeDirectedSnapshot {
			directed, policyErr := policypkg.NewSnapshotPartition(
				settings.Seed, policypkg.SnapshotPartitionConfig{
					NodeIDs: config.Raft.NodeIDs, MaxValue: config.Model.MaxValue,
					MaxLogIndex:       config.Model.MaxLogIndex,
					SnapshotThreshold: settings.SnapshotThreshold,
					RetainEntries:     settings.RetainEntries,
				},
			)
			if policyErr != nil {
				return report, policyErr
			}
			result, runErr = runner.RunSourceObserved(
				ctx, directed, settings.MaxActionsPerPlan, evaluator,
			)
			sequence = directed.Sequence()
			report.Diversity.InitialPlanKey = goalsearch.PlanKey(sequence)
			operator = "snapshot-directed-policy"
		} else {
			result, runErr = runner.RunObserved(ctx, sequence, evaluator)
		}
		elapsed := time.Since(runStarted)
		if selectedPrefix != nil && !traceStartsWith(result.Trace, selectedPrefix.PrefixTrace) {
			report.PrefixExecutionMismatch++
			return report, fmt.Errorf(
				"candidate %d did not reproduce selected frontier Plan prefix %s",
				candidate, selectedPrefix.ID,
			)
		}
		online := evaluator.Result()
		if pendingAdvisorDecision != nil {
			finalized := protocolmutation.FinalizeDecision(*pendingAdvisorDecision, online.FinalObservation)
			advisorDecisions = append(advisorDecisions, finalized)
			if err := advisorJournal.Append(finalized); err != nil {
				return report, err
			}
		}
		offline, offlineErr := goalsearch.Recompute(goalsearch.ArtifactInput{
			Definition: definition, InstanceID: runID, Initial: result.Initial,
			DistanceMode: settings.DistanceMode,
			Trace:        result.Trace, Resolutions: result.Resolutions,
			ModelEvents: result.ModelEvents, ModelConfig: config.Model,
		})
		expectedOfflineMapFailure := offlineErr != nil &&
			result.Status == engine.StatusMappingFailed
		equal := (offlineErr == nil || expectedOfflineMapFailure) &&
			equivalentGoalEvaluation(online, offline)
		if expectedOfflineMapFailure {
			report.ExpectedOfflineMapFailures++
		}
		if !equal {
			report.OnlineOfflineMismatches++
		}
		var branchInstance *goalsearch.BehaviorBranchInstance
		var evidenceVector *goalsearch.BranchEvidenceVector
		if plannedBranch != nil {
			analyzed, branchErr := goalsearch.AnalyzeBranch(
				*plannedBranch, online, result.Initial, result.Trace,
				settings.BranchDimensionAblation,
			)
			if branchErr != nil {
				return report, branchErr
			}
			if offlineErr == nil || expectedOfflineMapFailure {
				offlineBranch, recomputeErr := goalsearch.AnalyzeBranch(
					*plannedBranch, offline, result.Initial, result.Trace,
					settings.BranchDimensionAblation,
				)
				if recomputeErr != nil || analyzed.StableKey != offlineBranch.StableKey {
					report.OnlineOfflineMismatches++
					if recomputeErr != nil {
						return report, recomputeErr
					}
				}
			}
			branchInstance = &analyzed
			if err := branchInstances.Append(analyzed); err != nil {
				return report, err
			}
			if settings.BranchEvidenceMode != goalsearch.BranchEvidenceOff {
				vector, evidenceErr := goalsearch.AnalyzeBranchEvidence(
					*plannedBranch, analyzed, online, result.Trace,
				)
				if evidenceErr != nil {
					return report, evidenceErr
				}
				if offlineErr == nil || expectedOfflineMapFailure {
					offlineVector, recomputeErr := goalsearch.AnalyzeBranchEvidence(
						*plannedBranch, analyzed, offline, result.Trace,
					)
					if recomputeErr != nil || vector.StableKey != offlineVector.StableKey {
						report.OnlineOfflineMismatches++
						if recomputeErr != nil {
							return report, recomputeErr
						}
					}
				}
				evidenceVector = &vector
			}
		}
		waypointRegression, destroyed := frontierRegression(selectedParent, online)
		if waypointRegression {
			report.WaypointRegressions++
			report.CompletedWaypointsDestroyed += destroyed
		}
		signature, bugDetected := goalFailureSignature(result)
		var signaturePointer *minimize.Signature
		failureRelation := ""
		if bugDetected {
			signatureCopy := signature
			signaturePointer = &signatureCopy
			switch {
			case report.TargetReached:
				failureRelation = "after-goal"
			case online.TargetReached:
				failureRelation = "at-goal"
			default:
				failureRelation = "before-goal"
			}
		}
		runActionCount := len(result.Trace.Steps)
		for _, action := range result.Actions.Actions {
			report.ConcreteActionKinds[action.Kind]++
			report.Diversity.ActionTypeHistogram[action.Kind]++
		}
		for _, event := range result.ModelEvents {
			report.Diversity.ModelEventHistogram[event.Name]++
		}
		cumulativeActions += runActionCount
		report.Candidates++
		report.Actions = cumulativeActions
		report.RuntimeStatuses[result.Status]++
		if result.ModelExecuted {
			report.TLCExecutedRuns++
		}
		if len(result.OracleFindings) > 0 {
			report.OracleFindingRuns++
		}
		if result.Status != engine.StatusCompleted {
			report.Unexecutable++
		}
		newFacet, coverageErr := coverage.add(runID, config, result)
		if coverageErr != nil && settings.StrictTLC {
			return report, errors.Join(runErr, coverageErr)
		}
		newRealizedBranch := false
		if branchInstance != nil && branchInstance.RealizedBranchSignature.Decidable {
			key := branchInstance.RealizedBranchSignature.StableKey
			if _, exists := seenRealizedBranches[key]; !exists {
				seenRealizedBranches[key] = struct{}{}
				newRealizedBranch = true
			}
			if newRealizedBranch && !newFacet {
				report.Branch.NewBranchWithoutNewFacet++
			}
			if newFacet && !newRealizedBranch {
				report.Branch.NewFacetWithoutNewBranch++
			}
		}
		newWaypoint := online.Instance.Progress.CompletedWaypointCount >
			globalBest.CompletedWaypointCount
		distanceImproved := online.Instance.Progress.CompletedWaypointCount ==
			globalBest.CompletedWaypointCount &&
			online.Instance.Progress.DistanceToCurrent < globalBest.DistanceToCurrent
		progressed := newWaypoint || distanceImproved
		if newFacet && !progressed {
			report.Coverage.NewFacetWithoutGoalProgress++
		}
		if progressed && !newFacet {
			report.Coverage.GoalProgressWithoutNewFacet++
		}
		if newWaypoint && !newFacet {
			report.Coverage.NewWaypointWithoutNewFacet++
		}
		if distanceImproved && !newFacet {
			report.Coverage.DistanceWithoutNewFacet++
		}
		if goalsearch.BetterProgress(online.Instance.Progress, globalBest) {
			globalBest = online.Instance.Progress
		}
		newEvidence := false
		newCommitment := false
		if evidenceVector != nil {
			if evidenceVector.SupportedCount > 0 {
				if _, exists := seenEvidence[evidenceVector.StableKey]; !exists {
					seenEvidence[evidenceVector.StableKey] = struct{}{}
					newEvidence = true
				}
			}
			if evidenceVector.Commitment.Reached {
				if _, exists := seenCommitments[evidenceVector.Commitment.StableKey]; !exists {
					seenCommitments[evidenceVector.Commitment.StableKey] = struct{}{}
					newCommitment = true
					if err := branchCommitments.Append(struct {
						SchemaVersion  string                      `json:"schema_version"`
						RunID          string                      `json:"run_id"`
						CandidateIndex int                         `json:"candidate_index"`
						Commitment     goalsearch.BranchCommitment `json:"commitment"`
					}{
						SchemaVersion: goalsearch.BranchEvidenceSchemaVersion,
						RunID:         runID, CandidateIndex: candidate,
						Commitment: evidenceVector.Commitment,
					}); err != nil {
						return report, err
					}
				}
			}
			evidenceRecord := branchEvidenceRecord{
				SchemaVersion: goalsearch.BranchEvidenceSchemaVersion,
				RunID:         runID, CandidateIndex: candidate,
				PlannedTemplateID:  plannedBranch.BranchTemplateID,
				Vector:             *evidenceVector,
				CompletedWaypoints: online.Instance.Progress.CompletedWaypointCount,
				GoalReached:        online.TargetReached, BugDetected: bugDetected,
				NewFacet: newFacet, NewGoalProgress: progressed,
				NewEvidence: newEvidence, NewCommitment: newCommitment,
			}
			if err := branchEvidence.Append(evidenceRecord); err != nil {
				return report, err
			}
			if stageAllocator != nil {
				stageAllocator.Observe(
					candidate, plannedBranch.BranchTemplateID, *evidenceVector,
					online.Instance.Progress.CompletedWaypointCount, runActionCount,
				)
			}
			if settings.FormationFailureReport && !evidenceVector.FullRealized {
				budgetExhausted := candidate+1 >= settings.CandidateBudget ||
					cumulativeActions >= settings.ActionBudget
				failure := goalsearch.ClassifyFormationFailure(
					runID, candidate, *plannedBranch, *branchInstance, *evidenceVector,
					online, budgetExhausted, waypointRegression,
				)
				if err := formationFailures.Append(failure); err != nil {
					return report, err
				}
			}
		}
		frontierChanged := false
		var evictedBranches []goalsearch.BranchTemplateID
		if (settings.Mode == goalsearch.ModeFrontier ||
			settings.Mode == goalsearch.ModeDiversityFrontier ||
			settings.Mode == goalsearch.ModeEvidenceFrontier ||
			settings.Mode == goalsearch.ModeFrontierNoPrefix) &&
			online.PrefixEndActionIndex >= 0 {
			seedID := fmt.Sprintf("frontier-%06d", candidate)
			seedEvaluation := online
			if diversityFrontier != nil && branchInstance != nil {
				traceEnd, planEnd, observation, boundaryOK, boundaryErr :=
					goalsearch.BranchPrefixBoundary(
						result.Initial, result.Trace, result.Resolutions, *branchInstance,
					)
				if boundaryErr != nil {
					return report, boundaryErr
				}
				if boundaryOK && traceEnd > seedEvaluation.PrefixEndTraceStep {
					seedEvaluation.PrefixEndTraceStep = traceEnd
					seedEvaluation.PrefixEndActionIndex = planEnd
					seedEvaluation.PrefixObservation = observation
					seedEvaluation.Instance.Progress.EvidenceStrength +=
						len(branchInstance.Evidence)
					seedEvaluation.Instance.Progress.PrefixLength = planEnd + 1
				}
			}
			if evidenceFrontier != nil && branchInstance != nil && evidenceVector != nil {
				eligible := settings.BranchEvidenceMode == goalsearch.BranchEvidencePartial ||
					evidenceVector.Commitment.Reached
				if eligible {
					var traceEnd, planEnd int
					var observation core.Observation
					var boundaryOK bool
					var boundaryErr error
					if settings.MicroProgressPolicy == goalsearch.MicroProgressLegacy {
						traceEnd, planEnd, observation, boundaryOK, boundaryErr =
							goalsearch.BranchPrefixBoundary(
								result.Initial, result.Trace, result.Resolutions, *branchInstance,
							)
					} else {
						traceEnd, planEnd, observation, boundaryOK, boundaryErr =
							goalsearch.EvidencePrefixBoundary(
								result.Initial, result.Trace, result.Resolutions,
								*evidenceVector, settings.MicroProgressPolicy,
							)
					}
					if boundaryErr != nil {
						return report, boundaryErr
					}
					if boundaryOK && traceEnd > seedEvaluation.PrefixEndTraceStep {
						seedEvaluation.PrefixEndTraceStep = traceEnd
						seedEvaluation.PrefixEndActionIndex = planEnd
						seedEvaluation.PrefixObservation = observation
						seedEvaluation.Instance.Progress.EvidenceStrength =
							evidenceVector.NecessaryCount
						seedEvaluation.Instance.Progress.PrefixLength = planEnd + 1
					}
				}
			}
			seed, seedErr := goalsearch.SeedFromResult(
				seedID, parentSeedID, candidate, runConfig.Seed, runConfig.ExecutionID,
				sequence, result, seedEvaluation,
			)
			if seedErr == nil {
				handoffParents[seed.ID] = parentSeedID
				if branchInstance != nil {
					seed.PlannedBranchID = branchInstance.BranchTemplateID
					seed.PlannedBranchKey = branchInstance.PlannedBranchSignature.StableKey
					seed.RealizedBranchKey = branchInstance.RealizedBranchSignature.StableKey
					seed.RealizedBranchID = branchInstance.RealizedBranchSignature.MatchedTemplateID
					seed.RealizedBranchDecidable =
						branchInstance.RealizedBranchSignature.Decidable
					seed.BranchSemanticKey = goalsearch.BranchPrefixSemanticKey(seed)
				}
				if evidenceVector != nil {
					seed.EvidenceLevel = evidenceVector.HighestLevel
					seed.CommittedBranchID = evidenceVector.Commitment.BranchID
					seed.CommitmentKey = evidenceVector.Commitment.StableKey
					seed.NecessaryEvidenceCount = evidenceVector.NecessaryCount
					seed.NextEventGeneratable = evidenceVector.NextEventGeneratable
					seed.EvidenceContradicted = evidenceVector.Contradicted
					seed.EvidenceKey = goalsearch.EvidenceSeedKey(seed)
				}
				switch {
				case evidenceFrontier != nil:
					before := evidenceFrontier.Snapshot()
					frontierChanged, _ = evidenceFrontier.Consider(seed)
					after := evidenceFrontier.Snapshot()
					evictedBranches = frontierEvictedBranches(
						before.Seeds, after.Seeds, seed,
						after.Stats.Inserted > before.Stats.Inserted,
					)
				case diversityFrontier != nil:
					before := diversityFrontier.Snapshot()
					frontierChanged, _ = diversityFrontier.Consider(seed)
					after := diversityFrontier.Snapshot()
					evictedBranches = frontierEvictedBranches(
						before.Seeds, after.Seeds, seed,
						after.Stats.Inserted > before.Stats.Inserted,
					)
				case capacityFrontier != nil:
					before := capacityFrontier.Snapshot()
					frontierChanged, _ = capacityFrontier.Consider(seed)
					after := capacityFrontier.Snapshot()
					evictedBranches = frontierEvictedBranches(
						before.Seeds, after.Seeds, seed,
						after.Stats.Inserted > before.Stats.Inserted,
					)
				default:
					before := frontier.Snapshot()
					frontierChanged, _ = frontier.Consider(seed)
					after := frontier.Snapshot()
					evictedBranches = frontierEvictedBranches(
						before.Seeds, after.Seeds, seed,
						after.Stats.Inserted > before.Stats.Inserted,
					)
				}
				if frontierChanged {
					_ = writeJSONFile(filepath.Join(output, "frontier-seeds", seedID+"-plan.json"), seed.PrefixPlan)
					_ = writeJSONFile(filepath.Join(output, "frontier-seeds", seedID+"-trace.json"), seed.PrefixTrace)
				}
			}
		}
		record := goalProgressRecord{
			SchemaVersion: goalsearch.SchemaVersion, RunID: runID, CandidateIndex: candidate,
			ParentSeedID: parentSeedID, Bindings: online.Instance.Bindings,
			CurrentWaypoint:    online.Instance.Progress.CurrentWaypointID,
			CompletedWaypoints: online.Instance.Progress.CompletedWaypointCount,
			Distance:           online.Instance.Progress.DistanceToCurrent, Updates: online.Updates,
			FrontierChanged: frontierChanged, TargetReached: online.TargetReached,
			RuntimeStatus: result.Status, TLCExecuted: result.ModelExecuted,
			OracleFindings: len(result.OracleFindings), Failure: result.Failure,
			ActionCount: runActionCount, PlanLength: len(sequence.Actions),
			ElapsedMilliseconds: elapsed.Milliseconds(), OnlineOfflineEqual: equal,
			NewFacet: newFacet, MutationOperator: operator,
			HintStrength:               settings.HintStrength,
			SelectedFrontierSeed:       selectedParent != nil,
			PrefixPreserved:            selectedPrefix != nil,
			WaypointRegression:         waypointRegression,
			CompletedWaypointDestroyed: destroyed,
			BugDetected:                bugDetected, FailureSignature: signaturePointer,
			FailureRelation: failureRelation,
			Branch:          branchInstance, NewRealizedBranch: newRealizedBranch,
		}
		if offlineErr != nil {
			record.OfflineRecomputeError = offlineErr.Error()
		}
		if err := progressJournal.Append(record); err != nil {
			return report, err
		}
		if branchInstance != nil {
			agreement := branchInstance.RealizedBranchSignature.Decidable &&
				!branchInstance.Deviation.Occurred
			branchRecord := branchProgressRecord{
				SchemaVersion: goalsearch.BranchSchemaVersion, RunID: runID,
				CandidateIndex:     candidate,
				PlannedTemplateID:  branchInstance.BranchTemplateID,
				PlannedKey:         branchInstance.PlannedBranchSignature.StableKey,
				RealizedTemplateID: branchInstance.RealizedBranchSignature.MatchedTemplateID,
				RealizedKey:        branchInstance.RealizedBranchSignature.StableKey,
				RealizedDecidable:  branchInstance.RealizedBranchSignature.Decidable,
				Feasibility:        branchInstance.Feasibility, Agreement: agreement,
				Deviation:       branchInstance.Deviation,
				DeepestWaypoint: branchInstance.Progress.CompletedWaypointCount,
				GoalReached:     online.TargetReached, BugDetected: bugDetected,
				ActionCount: runActionCount, FrontierChanged: frontierChanged,
				EvictedBranches:   evictedBranches,
				NewRealizedBranch: newRealizedBranch, NewFacet: newFacet,
				StableKey: branchInstance.StableKey,
			}
			if err := branchProgress.Append(branchRecord); err != nil {
				return report, err
			}
			updateBranchSummary(&report.Branch, branchRecord)
		}
		runDirectory := filepath.Join(output, "runs", runID)
		firstTargetThisRun := online.TargetReached && !report.TargetReached
		if settings.SaveAllRuns || runErr != nil || firstTargetThisRun {
			if err := os.MkdirAll(runDirectory, 0o755); err != nil {
				return report, err
			}
			if err := writeArtifacts(runDirectory, runConfig, sequence, result); err != nil {
				return report, err
			}
			if err := writeJSONFile(filepath.Join(runDirectory, "goal-progress-online.json"), online); err != nil {
				return report, err
			}
			if offlineErr == nil || expectedOfflineMapFailure {
				if err := writeJSONFile(filepath.Join(runDirectory, "goal-progress-offline.json"), offline); err != nil {
					return report, err
				}
			}
		}
		report.ProgressUpdates += online.ProgressUpdates
		report.DistanceImprovements += online.DistanceImprovements
		report.DistanceWorsenings += online.DistanceWorsenings
		updateWaypointAggregates(&report, online, candidate, cumulativeActions)
		if online.Instance.Progress.CurrentWaypointID != "" {
			stalledAt[online.Instance.Progress.CurrentWaypointID]++
		}
		parentEval = online
		progressPath = append(progressPath, goalsearch.ProgressPoint{
			Completed: online.Instance.Progress.CompletedWaypointCount,
			Distance:  online.Instance.Progress.DistanceToCurrent,
		})
		report.Diversity.FinalPlanKey = goalsearch.PlanKey(sequence)
		report.Diversity.FinalTraceKey = goalsearch.TraceKey(result.Trace)
		report.Diversity.SemanticTraceKey = goalsearch.SemanticTraceKey(result.Trace)
		report.Diversity.MessageQueueShapeKey =
			goalsearch.MessageQueueShapeKey(online.FinalObservation)
		if bugDetected && !report.BugDetected {
			report.BugDetected = true
			report.FirstFailureCandidate = candidate + 1
			report.FirstFailureActions = cumulativeActions
			report.FirstFailureMillis = time.Since(started).Milliseconds()
			report.FirstFailureLayer = result.Status
			report.FirstFailureWaypoint = online.Instance.Progress.CurrentWaypointID
			report.FirstFailureRelation = failureRelation
			report.FirstFailureSignature = signaturePointer
			if branchInstance != nil {
				report.FirstFailurePlannedBranch = branchInstance.BranchTemplateID
				report.FirstFailureRealizedBranch =
					branchInstance.RealizedBranchSignature.MatchedTemplateID
				report.FirstFailureRealizedKey =
					branchInstance.RealizedBranchSignature.StableKey
				report.FirstFailureBranchDecidable =
					branchInstance.RealizedBranchSignature.Decidable
				report.FirstFailureBranchDeviation = branchInstance.Deviation
			}
		}
		if online.TargetReached && !report.TargetReached {
			report.TargetReached = true
			report.FirstTargetCandidate = candidate + 1
			report.FirstTargetActions = cumulativeActions
			report.FirstTargetMillis = time.Since(started).Milliseconds()
			report.TargetPlanLength = len(sequence.Actions)
			report.TargetRuntimeStatus = result.Status
			report.TargetTLCExecuted = result.ModelExecuted
			report.TargetOracleFindings = len(result.OracleFindings)
			report.ContributingSeedID = parentSeedID
			report.ContributingHandoffSeedID =
				rootHandoffSeed(parentSeedID, handoffParents)
			target := struct {
				SchemaVersion string                      `json:"schema_version"`
				RunID         string                      `json:"run_id"`
				Candidate     int                         `json:"candidate"`
				Evaluation    goalsearch.EvaluationResult `json:"evaluation"`
			}{goalsearch.SchemaVersion, runID, candidate, online}
			if err := writeJSONFile(filepath.Join(output, "target-reached.json"), target); err != nil {
				return report, err
			}
			if settings.StopOnTarget {
				break
			}
		}
		if bugDetected && settings.StopOnFailure {
			break
		}
		if runErr != nil && result.Status == engine.StatusCanceled {
			return report, runErr
		}
		if online.Instance.Progress.CurrentWaypointID != "" &&
			stalledAt[online.Instance.Progress.CurrentWaypointID] >= settings.PerWaypointBudget {
			break
		}
	}
	switch {
	case evidenceFrontier != nil:
		report.EvidenceFrontier = evidenceFrontier.Snapshot()
		report.Frontier = goalsearch.FrontierSnapshot{
			SchemaVersion: goalsearch.SchemaVersion,
			TopK:          report.EvidenceFrontier.TotalCapacity,
			Seeds:         report.EvidenceFrontier.Seeds,
			Stats: goalsearch.FrontierStats{
				Considered:   report.EvidenceFrontier.Stats.Considered,
				Inserted:     report.EvidenceFrontier.Stats.Inserted,
				Deduplicated: report.EvidenceFrontier.Stats.Deduplicated,
				Replaced:     report.EvidenceFrontier.Stats.Replaced,
				Evicted:      report.EvidenceFrontier.Stats.Evicted,
			},
		}
	case diversityFrontier != nil:
		report.BranchFrontier = diversityFrontier.Snapshot()
		report.Frontier = goalsearch.FrontierSnapshot{
			SchemaVersion: goalsearch.SchemaVersion,
			TopK:          report.BranchFrontier.TotalCapacity,
			Seeds:         report.BranchFrontier.Seeds,
			Sizes:         report.BranchFrontier.SizesByBranch,
			Diversity:     report.BranchFrontier.SizesByBranch,
			Stats: goalsearch.FrontierStats{
				Considered:   report.BranchFrontier.Stats.Considered,
				Inserted:     report.BranchFrontier.Stats.Inserted,
				Deduplicated: report.BranchFrontier.Stats.Deduplicated,
				Replaced:     report.BranchFrontier.Stats.Replaced,
				Evicted:      report.BranchFrontier.Stats.Evicted,
			},
		}
	case capacityFrontier != nil:
		report.Frontier = capacityFrontier.Snapshot()
	default:
		report.Frontier = frontier.Snapshot()
	}
	report.Diversity.GoalProgressSequenceKey = goalsearch.ProgressPathKey(progressPath)
	report.Diversity.FacetSequenceKey = goalsearch.StringSequenceKey(coverage.sequence)
	for _, seed := range report.Frontier.Seeds {
		report.Diversity.FrontierPrefixKeys =
			append(report.Diversity.FrontierPrefixKeys, seed.SemanticKey)
	}
	sort.Strings(report.Diversity.FrontierPrefixKeys)
	report.Coverage = coverage.counts(report.Coverage)
	if total := report.ValidPlans + report.InvalidPlans; total > 0 {
		report.CandidateValidityRate = float64(report.ValidPlans) / float64(total)
	}
	if report.Candidates > 0 {
		report.UnexecutableRate = float64(report.Unexecutable) / float64(report.Candidates)
	}
	report.MostStalledWaypoint = mostStalled(stalledAt)
	recomputedBranch, recomputeBranchErr := recomputeBranchSummary(
		filepath.Join(output, "branch-progress.jsonl"), report.Branch.Enabled,
		settings.BranchFeasibility,
	)
	if recomputeBranchErr != nil {
		return report, recomputeBranchErr
	}
	report.Branch = recomputedBranch
	recomputedEvidence, recomputeEvidenceErr := recomputeBranchEvidenceSummary(
		filepath.Join(output, "branch-evidence.jsonl"), 3,
	)
	if recomputeEvidenceErr != nil {
		return report, recomputeEvidenceErr
	}
	report.Evidence = recomputedEvidence
	if stageAllocator != nil {
		report.BranchBudget = stageAllocator.Summary()
		budgetJournal, journalErr := persistence.OpenJournal(
			filepath.Join(output, "branch-budget-ledger.jsonl"),
		)
		if journalErr != nil {
			return report, journalErr
		}
		for _, record := range stageAllocator.Ledger() {
			if err := budgetJournal.Append(record); err != nil {
				_ = budgetJournal.Close()
				return report, err
			}
		}
		if err := budgetJournal.Close(); err != nil {
			return report, err
		}
	} else {
		report.BranchBudget = goalsearch.BranchBudgetSummary{
			SchemaVersion: goalsearch.BranchEvidenceSchemaVersion,
			Mode:          goalsearch.BranchBudgetRoundRobin,
		}
	}
	report.FinishedAt = time.Now().UTC().Format(time.RFC3339Nano)
	report.ElapsedMillis = time.Since(started).Milliseconds()
	if err := writeAdvisorArtifacts(output, settings, advisorDecisions, report); err != nil {
		return report, err
	}
	if err := writeJSONFile(filepath.Join(output, "frontier-manifest.json"), report.Frontier); err != nil {
		return report, err
	}
	if err := writeJSONFile(
		filepath.Join(output, "branch-frontier-manifest.json"), report.BranchFrontier,
	); err != nil {
		return report, err
	}
	if err := writeJSONFile(
		filepath.Join(output, "evidence-frontier-manifest.json"), report.EvidenceFrontier,
	); err != nil {
		return report, err
	}
	if err := writeJSONFile(
		filepath.Join(output, "planned-realized-mapping.json"),
		report.Branch.PlannedRealizedDistribution,
	); err != nil {
		return report, err
	}
	if err := writeJSONFile(filepath.Join(output, "branch-summary.json"), report.Branch); err != nil {
		return report, err
	}
	if err := writeJSONFile(
		filepath.Join(output, "branch-evidence-summary.json"), report.Evidence,
	); err != nil {
		return report, err
	}
	if err := writeEvidenceCSV(
		filepath.Join(output, "micro-progress-utility.csv"),
		evidenceUtilityRows(report.Evidence),
	); err != nil {
		return report, err
	}
	if err := writeJSONFile(
		filepath.Join(output, "branch-budget-summary.json"), report.BranchBudget,
	); err != nil {
		return report, err
	}
	return report, nil
}

func verifyGoalPrefix(
	ctx context.Context, config cliConfig, seed goalsearch.FrontierSeed, stderr io.Writer,
) (tracepkg.Result, error) {
	config.Seed = seed.RuntimeSeed
	config.ExecutionID = seed.ExecutionID
	runtime, err := buildRuntime(config, stderr)
	if err != nil {
		return tracepkg.Result{}, err
	}
	replayer, err := tracepkg.NewReplayer(runtime)
	if err != nil {
		return tracepkg.Result{}, err
	}
	return replayer.Replay(ctx, seed.PrefixTrace)
}

func evidencePriorityMultiplier(settings goalSearchSettings) int {
	if settings.Mode != goalsearch.ModeEvidenceFrontier {
		return 1
	}
	return max(1, settings.EvidencePriorityMultiplier)
}

func focusedMutationOptions(
	settings goalSearchSettings,
	plannedBranch *goalsearch.BehaviorBranchTemplate,
	advisor protocolmutation.Advisor,
	candidate, noProgress int,
) goalsearch.MutationOptions {
	if settings.BranchEvidenceRecordOnly {
		// The Branch is still analyzed after execution, but cannot influence
		// category weights, Plan selection, prefix selection, or RNG.
		plannedBranch = nil
	}
	return goalsearch.MutationOptions{
		HintStrength:               settings.HintStrength,
		PlannedBranch:              plannedBranch,
		EvidencePriorityMultiplier: evidencePriorityMultiplier(settings),
		Advisor:                    advisor, AdvisorCandidateIndex: candidate,
		AdvisorNoProgressCount: noProgress,
		AdvisorRecordOnly:      settings.AdvisorRecordOnly,
	}
}

func writeAdvisorArtifacts(
	output string,
	settings goalSearchSettings,
	decisions []protocolmutation.Decision,
	report goalSearchReport,
) error {
	summary := protocolmutation.Summarize(decisions)
	if err := writeJSONFile(filepath.Join(output, "mutation-advisor-summary.json"), summary); err != nil {
		return err
	}
	stageRows := [][]string{{"local_stage", "decision_count"}}
	stageNames := make([]string, 0, len(summary.ByStage))
	for stage := range summary.ByStage {
		stageNames = append(stageNames, stage)
	}
	sort.Strings(stageNames)
	for _, stage := range stageNames {
		stageRows = append(stageRows, []string{stage, strconv.Itoa(summary.ByStage[stage])})
	}
	if err := writeEvidenceCSV(filepath.Join(output, "local-stage-summary.csv"), stageRows); err != nil {
		return err
	}
	reasonRows := [][]string{{"reason_code", "decision_count"}}
	reasonNames := make([]string, 0, len(summary.ByReason))
	for reason := range summary.ByReason {
		reasonNames = append(reasonNames, reason)
	}
	sort.Strings(reasonNames)
	for _, reason := range reasonNames {
		reasonRows = append(reasonRows, []string{reason, strconv.Itoa(summary.ByReason[reason])})
	}
	if err := writeEvidenceCSV(filepath.Join(output, "advisor-reason-summary.csv"), reasonRows); err != nil {
		return err
	}
	ablationRows := [][]string{
		{"goal_id", "advisor", "ablation", "candidates", "target_reached", "first_target_candidate", "actions", "elapsed_ms"},
		{string(settings.GoalID), settings.MutationAdvisor, string(settings.AdvisorAblation),
			strconv.Itoa(report.Candidates), strconv.FormatBool(report.TargetReached),
			strconv.Itoa(report.FirstTargetCandidate), strconv.Itoa(report.Actions),
			strconv.FormatInt(report.ElapsedMillis, 10)},
	}
	if err := writeEvidenceCSV(filepath.Join(output, "focused-mutation-ablation.csv"), ablationRows); err != nil {
		return err
	}
	figureRows := [][]string{
		{"method", "goal_id", "seed", "reached", "first_target_candidate", "first_target_actions", "elapsed_ms", "advisor_decisions", "local_progress"},
		{settings.MutationAdvisor, string(settings.GoalID), strconv.FormatInt(settings.Seed, 10),
			strconv.FormatBool(report.TargetReached), strconv.Itoa(report.FirstTargetCandidate),
			strconv.Itoa(report.FirstTargetActions), strconv.FormatInt(report.ElapsedMillis, 10),
			strconv.Itoa(summary.DecisionCount), strconv.Itoa(summary.LocalProgressCount)},
	}
	if err := writeEvidenceCSV(filepath.Join(output, "focused-mutation-figure-ready.csv"), figureRows); err != nil {
		return err
	}
	coupling := map[string]any{
		"schema_version":            protocolmutation.SchemaVersion,
		"generic_boundary":          "internal/protocolmutation",
		"generic_interface_lines":   sourceLineCount("internal/protocolmutation/advisor.go"),
		"raft_implementation":       "internal/protocolmutation/raft",
		"raft_implementation_lines": sourceLineCount("internal/protocolmutation/raft/advisor.go"),
		"modified_core_files": []string{
			"internal/goalsearch/mutation.go",
			"cmd/modelfuzz-ng/goal_search.go",
			"cmd/modelfuzz-ng/goal_benchmark.go",
		},
		"search_owner": "internal/goalsearch and cmd/modelfuzz-ng",
		"raft_message_checks_in_generic_frontier": false,
		"runtime_bypass":            false,
		"future_trace_visible":      false,
		"future_message_id_visible": false,
		"replacement_rule":          "a new protocol implements protocolmutation.Advisor; Frontier, replay, persistence, and evaluator remain unchanged",
	}
	if err := writeJSONFile(filepath.Join(output, "protocol-coupling-report.json"), coupling); err != nil {
		return err
	}
	freeze := map[string]any{
		"schema_version":         goalsearch.BranchEvidenceSchemaVersion,
		"branch_evidence_status": "frozen",
		"search_influence": !settings.BranchEvidenceRecordOnly &&
			(settings.Mode == goalsearch.ModeDiversityFrontier || settings.Mode == goalsearch.ModeEvidenceFrontier),
		"record_only":           settings.BranchEvidenceRecordOnly,
		"new_branch_dimensions": false,
		"new_evidence_levels":   false,
		"reason":                "round 7 freezes Branch/Evidence and tests only diagnostic record-only use",
	}
	return writeJSONFile(filepath.Join(output, "branch-evidence-freeze.json"), freeze)
}

func sourceLineCount(path string) int {
	data, err := os.ReadFile(path)
	if err != nil {
		return -1
	}
	if len(data) == 0 {
		return 0
	}
	lines := bytes.Count(data, []byte{'\n'})
	if data[len(data)-1] != '\n' {
		lines++
	}
	return lines
}

func equivalentGoalEvaluation(left, right goalsearch.EvaluationResult) bool {
	left.Online, right.Online = false, false
	left.StableKey, right.StableKey = "", ""
	left.Instance.StableKey, right.Instance.StableKey = "", ""
	leftJSON, leftErr := json.Marshal(left)
	rightJSON, rightErr := json.Marshal(right)
	return leftErr == nil && rightErr == nil && bytes.Equal(leftJSON, rightJSON)
}

func traceStartsWith(actual, prefix core.Trace) bool {
	if actual.ExecutionID != prefix.ExecutionID || actual.Seed != prefix.Seed ||
		len(actual.Steps) < len(prefix.Steps) {
		return false
	}
	for index := range prefix.Steps {
		actualJSON, actualErr := json.Marshal(actual.Steps[index])
		prefixJSON, prefixErr := json.Marshal(prefix.Steps[index])
		if actualErr != nil || prefixErr != nil || !bytes.Equal(actualJSON, prefixJSON) {
			return false
		}
	}
	return true
}

func (c *coverageAccumulator) add(runID string, config cliConfig, result engine.Result) (bool, error) {
	if len(result.ModelStates) == 0 {
		return false, nil
	}
	before := c.totalFacetCount()
	for _, state := range result.ModelStates {
		c.raw[state.Key] = struct{}{}
	}
	v1, err := raftmodel.ProjectCoverage(result.ModelStates, result.ModelEvents)
	if err != nil {
		return false, err
	}
	for _, key := range v1.StateKeys {
		c.v1[key] = struct{}{}
	}
	v2, err := raftmodel.ProjectCoverageV2Prototype(result.ModelStates)
	if err != nil {
		return false, err
	}
	for _, key := range v2.StateKeys {
		c.v2[key] = struct{}{}
	}
	frames, err := coverageanalysis.BuildCoverageFrames(coverageanalysis.RunArtifact{
		Name: runID, Source: "goal-search", ModelConfig: config.Model,
		Initial: result.Initial, Trace: result.Trace,
		ModelEvents: result.ModelEvents, ModelStates: result.ModelStates,
	})
	if err != nil {
		return false, err
	}
	for _, frame := range frames {
		facets, facetErr := raftmodel.ProjectCoverageFacets(frame.ModelState, frame.Context)
		if facetErr != nil {
			return false, facetErr
		}
		c.facets["election"][facets.ElectionKey] = struct{}{}
		c.facets["replication"][facets.ReplicationKey] = struct{}{}
		c.facets["snapshot"][facets.SnapshotKey] = struct{}{}
		c.facets["recovery"][facets.RecoveryKey] = struct{}{}
		c.facets["network"][facets.NetworkKey] = struct{}{}
		c.sequence = append(c.sequence, fmt.Sprintf(
			"%d/%d/%d/%d/%d",
			facets.ElectionKey, facets.ReplicationKey, facets.SnapshotKey,
			facets.RecoveryKey, facets.NetworkKey,
		))
		for _, interaction := range facets.Interactions {
			if c.interactions[interaction.Name] == nil {
				c.interactions[interaction.Name] = make(map[int64]struct{})
			}
			c.interactions[interaction.Name][interaction.Key] = struct{}{}
		}
	}
	return c.totalFacetCount() > before, nil
}

func (c *coverageAccumulator) totalFacetCount() int {
	total := 0
	for _, values := range c.facets {
		total += len(values)
	}
	for _, values := range c.interactions {
		total += len(values)
	}
	return total
}

func (c *coverageAccumulator) counts(previous coverageCounts) coverageCounts {
	previous.Available = len(c.raw) > 0
	previous.RawTLCStates = len(c.raw)
	previous.V1DistinctStates = len(c.v1)
	previous.V2DistinctStates = len(c.v2)
	previous.Facets = make(map[string]int)
	previous.Interactions = make(map[string]int)
	for name, values := range c.facets {
		previous.Facets[name] = len(values)
	}
	for name, values := range c.interactions {
		previous.Interactions[name] = len(values)
	}
	return previous
}

func mergeMutationStats(target *goalsearch.MutationStats, source goalsearch.MutationStats) {
	target.Attempts += source.Attempts
	target.Produced += source.Produced
	target.RejectedMaxActions += source.RejectedMaxActions
	target.RejectedNoAction += source.RejectedNoAction
	target.WholePlanEdits += source.WholePlanEdits
	target.ExactMessageUses += source.ExactMessageUses
	if target.HintStrengthUses == nil {
		target.HintStrengthUses = make(map[goalsearch.HintStrength]int)
	}
	for strength, count := range source.HintStrengthUses {
		target.HintStrengthUses[strength] += count
	}
	if target.Operators == nil {
		target.Operators = make(map[string]int)
	}
	for name, count := range source.Operators {
		target.Operators[name] += count
	}
}

func frontierRegression(
	parent *goalsearch.FrontierSeed,
	evaluation goalsearch.EvaluationResult,
) (bool, int) {
	if parent == nil {
		return false, 0
	}
	expected := parent.Progress.CompletedWaypointCount
	actual := evaluation.Instance.Progress.CompletedWaypointCount
	if actual >= expected {
		return false, 0
	}
	return true, expected - actual
}

func rootHandoffSeed(seedID string, parents map[string]string) string {
	if seedID == "" {
		return ""
	}
	current := seedID
	seen := make(map[string]struct{})
	for current != "" {
		if _, duplicate := seen[current]; duplicate {
			return ""
		}
		seen[current] = struct{}{}
		parent, known := parents[current]
		if !known {
			return ""
		}
		if parent == "" {
			return current
		}
		current = parent
	}
	return ""
}

func goalFailureSignature(result engine.Result) (minimize.Signature, bool) {
	switch result.Status {
	case engine.StatusRuntimeFailed, engine.StatusMappingFailed,
		engine.StatusModelFailed, engine.StatusOracleFailed:
		return minimize.SignatureOf(result)
	default:
		return minimize.Signature{}, false
	}
}

func goalSubject(config cliConfig) string {
	switch {
	case config.Raft.Faults.VoteQuorumDivisor == 3:
		return "mutant-vote-quorum-one-third"
	case config.Raft.Faults.SnapshotStatusMap == "invert":
		return "mutant-snapshot-status-invert"
	case config.Raft.Faults.RestartLoseHardState:
		return "mutant-restart-lose-hard-state"
	default:
		return "control"
	}
}

func updateWaypointAggregates(
	report *goalSearchReport, evaluation goalsearch.EvaluationResult,
	candidate, actions int,
) {
	for index, result := range evaluation.Instance.WaypointResults {
		if !result.Reached || report.Waypoints[index].Reached {
			continue
		}
		report.Waypoints[index].Reached = true
		report.Waypoints[index].FirstCandidate = candidate + 1
		report.Waypoints[index].FirstCumulativeActions = actions
		if index > 0 {
			report.Waypoints[index-1].TransitionSuccess = true
			report.Waypoints[index-1].AttemptsBeforeNext =
				report.Waypoints[index].FirstCandidate - report.Waypoints[index-1].FirstCandidate
		}
	}
}

func mostStalled(counts map[string]int) string {
	keys := make([]string, 0, len(counts))
	for key := range counts {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	best, bestCount := "", -1
	for _, key := range keys {
		if counts[key] > bestCount {
			best, bestCount = key, counts[key]
		}
	}
	return best
}

func selectGoalBranches(
	goal goalsearch.GoalID,
	text string,
	allFeasible bool,
	environment goalsearch.BranchEnvironment,
) ([]goalsearch.BehaviorBranchTemplate, []goalsearch.BranchFeasibilityResult, error) {
	if strings.TrimSpace(text) != "" && allFeasible {
		return nil, nil, fmt.Errorf("-branch-templates and -all-feasible-branches are mutually exclusive")
	}
	var requested []goalsearch.BehaviorBranchTemplate
	if allFeasible {
		requested = goalsearch.BranchTemplates(goal)
	} else if strings.TrimSpace(text) != "" {
		seen := make(map[goalsearch.BranchTemplateID]struct{})
		for _, raw := range strings.Split(text, ",") {
			id := goalsearch.BranchTemplateID(strings.TrimSpace(raw))
			if id == "" {
				continue
			}
			if _, duplicate := seen[id]; duplicate {
				continue
			}
			template, err := goalsearch.BranchTemplate(id)
			if err != nil {
				return nil, nil, err
			}
			if template.GoalID != goal {
				return nil, nil, fmt.Errorf("branch %q belongs to goal %q, not %q",
					id, template.GoalID, goal)
			}
			seen[id] = struct{}{}
			requested = append(requested, template)
		}
	}
	sort.Slice(requested, func(i, j int) bool {
		return requested[i].BranchTemplateID < requested[j].BranchTemplateID
	})
	feasibility := make([]goalsearch.BranchFeasibilityResult, 0, len(requested))
	active := make([]goalsearch.BehaviorBranchTemplate, 0, len(requested))
	for _, template := range requested {
		result := goalsearch.EvaluateBranchFeasibility(template, environment)
		feasibility = append(feasibility, result)
		if result.Status != goalsearch.BranchPermanentlyInfeasible {
			active = append(active, template)
		}
	}
	return active, feasibility, nil
}

func branchTemplateIDs(
	templates []goalsearch.BehaviorBranchTemplate,
) []goalsearch.BranchTemplateID {
	result := make([]goalsearch.BranchTemplateID, 0, len(templates))
	for _, template := range templates {
		result = append(result, template.BranchTemplateID)
	}
	return result
}

func updateBranchSummary(summary *branchSearchSummary, record branchProgressRecord) {
	if summary == nil {
		return
	}
	aggregate := summary.ByPlannedBranch[record.PlannedTemplateID]
	aggregate.Attempts++
	aggregate.Actions += record.ActionCount
	aggregate.DeepestWaypoint = max(aggregate.DeepestWaypoint, record.DeepestWaypoint)
	// A real failure can occur before every Realized Branch dimension becomes
	// decidable. Always attribute it to the selected Planned Branch; only the
	// Realized distribution remains gated on complete causal evidence.
	if record.BugDetected {
		aggregate.BugDetected++
	}
	if record.RealizedDecidable && record.RealizedKey != "" {
		aggregate.Decidable++
		summary.DecidableRuns++
		realizedLabel := record.RealizedKey
		if record.RealizedTemplateID != "" {
			realizedLabel = string(record.RealizedTemplateID) + ":" + record.RealizedKey[:12]
		}
		summary.RealizedDistribution[realizedLabel]++
		pair := string(record.PlannedTemplateID) + "→" + realizedLabel
		summary.PlannedRealizedDistribution[pair]++
		if record.Agreement {
			aggregate.Agreements++
		}
		if record.Deviation.Occurred {
			aggregate.Deviations++
		}
		if record.GoalReached {
			aggregate.GoalReached++
			summary.SuccessfulRealizedDistribution[realizedLabel]++
		}
	}
	if record.FrontierChanged {
		aggregate.FrontierRetained++
	}
	summary.ByPlannedBranch[record.PlannedTemplateID] = aggregate
	for _, branchID := range record.EvictedBranches {
		evicted := summary.ByPlannedBranch[branchID]
		evicted.FrontierEvicted++
		summary.ByPlannedBranch[branchID] = evicted
	}
}

func frontierEvictedBranches(
	before, after []goalsearch.FrontierSeed,
	candidate goalsearch.FrontierSeed,
	inserted bool,
) []goalsearch.BranchTemplateID {
	retained := make(map[string]struct{}, len(after))
	for _, seed := range after {
		retained[seed.ID] = struct{}{}
	}
	var result []goalsearch.BranchTemplateID
	for _, seed := range before {
		if _, ok := retained[seed.ID]; ok || seed.PlannedBranchID == "" {
			continue
		}
		result = append(result, seed.PlannedBranchID)
	}
	if inserted {
		if _, ok := retained[candidate.ID]; !ok && candidate.PlannedBranchID != "" {
			result = append(result, candidate.PlannedBranchID)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result
}

func finalizeBranchSummary(summary *branchSearchSummary) {
	if summary == nil {
		return
	}
	summary.PlannedBranchCount = len(summary.ByPlannedBranch)
	summary.RealizedBranchCount = len(summary.RealizedDistribution)
	summary.PlannedRealizedPairCount = len(summary.PlannedRealizedDistribution)
	summary.SuccessfulBranchCount = len(summary.SuccessfulRealizedDistribution)
	agreements, deviations := 0, 0
	for _, aggregate := range summary.ByPlannedBranch {
		agreements += aggregate.Agreements
		deviations += aggregate.Deviations
	}
	if summary.DecidableRuns > 0 {
		summary.AgreementRate = float64(agreements) / float64(summary.DecidableRuns)
		summary.DeviationRate = float64(deviations) / float64(summary.DecidableRuns)
	}
}

func recomputeBranchSummary(
	path string,
	enabled bool,
	feasibility []goalsearch.BranchFeasibilityResult,
) (branchSearchSummary, error) {
	summary := branchSearchSummary{
		SchemaVersion: goalsearch.BranchSchemaVersion, Enabled: enabled,
		ByPlannedBranch:                make(map[goalsearch.BranchTemplateID]branchAggregate),
		RealizedDistribution:           make(map[string]int),
		PlannedRealizedDistribution:    make(map[string]int),
		SuccessfulRealizedDistribution: make(map[string]int),
	}
	for _, result := range feasibility {
		if result.Status != goalsearch.BranchPermanentlyInfeasible {
			continue
		}
		aggregate := summary.ByPlannedBranch[result.BranchTemplateID]
		aggregate.PermanentlyInfeasible++
		summary.ByPlannedBranch[result.BranchTemplateID] = aggregate
	}
	file, err := os.Open(path)
	if err != nil {
		return summary, err
	}
	defer func() { _ = file.Close() }()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		var record branchProgressRecord
		if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
			return summary, fmt.Errorf("decode branch progress: %w", err)
		}
		if record.SchemaVersion != goalsearch.BranchSchemaVersion {
			return summary, fmt.Errorf("branch progress schema=%q want %q",
				record.SchemaVersion, goalsearch.BranchSchemaVersion)
		}
		updateBranchSummary(&summary, record)
		if record.NewRealizedBranch && !record.NewFacet {
			summary.NewBranchWithoutNewFacet++
		}
		if record.NewFacet && !record.NewRealizedBranch {
			summary.NewFacetWithoutNewBranch++
		}
	}
	if err := scanner.Err(); err != nil {
		return summary, err
	}
	finalizeBranchSummary(&summary)
	return summary, nil
}
