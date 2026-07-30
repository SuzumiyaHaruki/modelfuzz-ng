// Package breadthdepth defines the protocol-neutral schema and deterministic
// handoff policy used by the explicitly enabled two-phase breadth/depth
// benchmark. Protocol-specific Goal and coverage projection code remains in
// the existing semantic packages.
package breadthdepth

import (
	"fmt"

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/core"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/coverageguidance"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/plan"
)

const SchemaVersion = "raft-breadth-depth-handoff-v1-prototype"

type Method string

const (
	MethodFacetOnly  Method = "M0-facet-only-breadth"
	MethodLocalOnly  Method = "M1-local-only-depth"
	MethodRandomThen Method = "M2-random-global-local"
	MethodRawThen    Method = "M3-raw-global-local"
	MethodV2Then     Method = "M4-v2-global-local"
	MethodFacetThen  Method = "M5-facet-global-local"
)

func (m Method) Validate() error {
	switch m {
	case MethodFacetOnly, MethodLocalOnly, MethodRandomThen, MethodRawThen,
		MethodV2Then, MethodFacetThen:
		return nil
	default:
		return fmt.Errorf("unknown breadth/depth method %q", m)
	}
}

type Budget struct {
	TotalCandidates  int `json:"total_candidate_budget"`
	GlobalCandidates int `json:"global_candidate_budget"`
	LocalCandidates  int `json:"local_candidate_budget"`
	TotalActions     int `json:"total_action_budget"`
	GlobalActions    int `json:"global_action_budget"`
	LocalActions     int `json:"local_action_budget"`
	MaxPlanActions   int `json:"max_actions_per_plan"`
}

func (b Budget) Validate() error {
	if b.TotalCandidates <= 0 || b.TotalActions <= 0 || b.MaxPlanActions <= 0 {
		return fmt.Errorf("total candidate/action and per-plan action budgets must be positive")
	}
	if b.GlobalCandidates < 0 || b.LocalCandidates < 0 ||
		b.GlobalActions < 0 || b.LocalActions < 0 {
		return fmt.Errorf("phase budgets cannot be negative")
	}
	if b.GlobalCandidates+b.LocalCandidates != b.TotalCandidates {
		return fmt.Errorf("global candidates %d + local candidates %d != total %d",
			b.GlobalCandidates, b.LocalCandidates, b.TotalCandidates)
	}
	if b.GlobalActions+b.LocalActions != b.TotalActions {
		return fmt.Errorf("global actions %d + local actions %d != total %d",
			b.GlobalActions, b.LocalActions, b.TotalActions)
	}
	if b.GlobalCandidates > 0 && b.GlobalActions <= 0 {
		return fmt.Errorf("non-empty global phase requires a positive action budget")
	}
	if b.LocalCandidates > 0 && b.LocalActions <= 0 {
		return fmt.Errorf("non-empty local phase requires a positive action budget")
	}
	return nil
}

type BreadthDepthRun struct {
	SchemaVersion string `json:"schema_version"`
	Method        Method `json:"method"`
	GoalID        string `json:"goal_id"`
	Seed          int64  `json:"seed"`
	Budget        Budget `json:"budget"`
	HandoffTopK   int    `json:"handoff_top_k"`
	LLMCalls      int    `json:"llm_calls"`
}

type GlobalEntry struct {
	SchemaVersion string                               `json:"schema_version"`
	CorpusID      string                               `json:"corpus_id"`
	RunIndex      int                                  `json:"run_index"`
	RuntimeSeed   int64                                `json:"runtime_seed"`
	ExecutionID   core.ExecutionID                     `json:"execution_id"`
	Plan          plan.PlanSequence                    `json:"plan"`
	Trace         core.Trace                           `json:"trace"`
	Observation   core.Observation                     `json:"observation"`
	Coverage      coverageguidance.CoverageObservation `json:"coverage"`
	Admission     coverageguidance.Decision            `json:"admission"`
	AdmissionRank int                                  `json:"admission_rank"`
	ReplayStatus  string                               `json:"replay_status"`
	StableKey     string                               `json:"stable_key"`
}

type GlobalPhaseResult struct {
	SchemaVersion   string                          `json:"schema_version"`
	GuidanceMode    coverageguidance.Mode           `json:"guidance_mode"`
	Seed            int64                           `json:"seed"`
	CandidateBudget int                             `json:"candidate_budget"`
	ActionBudget    int                             `json:"action_budget"`
	Candidates      int                             `json:"candidates"`
	Actions         int                             `json:"actions"`
	CorpusEntries   int                             `json:"corpus_entries"`
	Frozen          bool                            `json:"corpus_frozen"`
	Coverage        coverageguidance.CoverageCounts `json:"coverage"`
	SemanticTraces  int                             `json:"semantic_trace_count"`
	StableKey       string                          `json:"stable_key"`
}

// GoalProgress is deliberately protocol-neutral. Bindings are represented by
// semantic role labels, not concrete node identities.
type GoalProgress struct {
	EntryCondition    bool              `json:"entry_condition"`
	Completed         int               `json:"completed_waypoint_count"`
	CurrentWaypoint   string            `json:"current_waypoint,omitempty"`
	Distance          int               `json:"distance"`
	TargetReached     bool              `json:"target_reached"`
	BindingRoles      map[string]string `json:"binding_roles,omitempty"`
	ProgressStableKey string            `json:"progress_stable_key"`
}

type HandoffSeed struct {
	SchemaVersion       string            `json:"schema_version"`
	GlobalCorpusID      string            `json:"global_corpus_id"`
	GlobalAdmissionRank int               `json:"global_admission_rank"`
	Progress            GoalProgress      `json:"goal_progress"`
	Plan                plan.PlanSequence `json:"plan"`
	Trace               core.Trace        `json:"trace"`
	Observation         core.Observation  `json:"observation"`
	PlanPrefixLength    int               `json:"plan_prefix_length"`
	SemanticTraceDigest string            `json:"semantic_trace_digest"`
	FacetCombinationKey string            `json:"facet_combination_key"`
	NewFacet            bool              `json:"new_facet"`
	FacetNoveltyCount   int               `json:"facet_novelty_count"`
	QueueShapeKey       string            `json:"queue_shape_key"`
	Replayable          bool              `json:"replayable"`
	ReplayStatus        string            `json:"replay_status"`
	Selected            bool              `json:"selected"`
	SelectionRank       int               `json:"selection_rank,omitempty"`
	StableKey           string            `json:"stable_key"`
}

type HandoffSet struct {
	SchemaVersion  string        `json:"schema_version"`
	GoalID         string        `json:"goal_id"`
	TopK           int           `json:"top_k"`
	Candidates     int           `json:"candidate_count"`
	Eligible       int           `json:"eligible_count"`
	Selected       []HandoffSeed `json:"selected"`
	Fallback       bool          `json:"fallback"`
	FallbackReason string        `json:"fallback_reason,omitempty"`
	StableKey      string        `json:"stable_key"`
}

type LocalPhaseResult struct {
	SchemaVersion    string `json:"schema_version"`
	GoalID           string `json:"goal_id"`
	CandidateBudget  int    `json:"candidate_budget"`
	ActionBudget     int    `json:"action_budget"`
	Candidates       int    `json:"candidates"`
	Actions          int    `json:"actions"`
	TargetReached    bool   `json:"target_reached"`
	BudgetExhausted  bool   `json:"budget_exhausted"`
	ContributingSeed string `json:"contributing_handoff_seed,omitempty"`
	StableKey        string `json:"stable_key"`
}

type CombinedSummary struct {
	SchemaVersion          string             `json:"schema_version"`
	Run                    BreadthDepthRun    `json:"run"`
	Global                 *GlobalPhaseResult `json:"global_phase,omitempty"`
	Handoff                *HandoffSet        `json:"handoff,omitempty"`
	Local                  *LocalPhaseResult  `json:"local_phase,omitempty"`
	GoalReached            bool               `json:"goal_reached"`
	DeepestWaypoint        int                `json:"deepest_waypoint"`
	MinimumDistance        int                `json:"minimum_distance"`
	BudgetExhausted        bool               `json:"budget_exhausted"`
	FinalCoverage          CoverageSummary    `json:"final_coverage"`
	LocalNewCoverage       CoverageSummary    `json:"local_new_coverage"`
	GlobalCoverageRetained bool               `json:"global_coverage_retained"`
	FinalCandidates        int                `json:"final_candidates"`
	FinalActions           int                `json:"final_actions"`
	BudgetValid            bool               `json:"budget_valid"`
	StableKey              string             `json:"stable_key"`
}

type CoverageSummary struct {
	Raw            int            `json:"raw"`
	V2             int            `json:"v2"`
	Facets         map[string]int `json:"facets"`
	Interactions   map[string]int `json:"interactions"`
	SemanticTraces int            `json:"semantic_traces"`
}
