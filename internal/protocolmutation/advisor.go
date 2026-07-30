// Package protocolmutation defines the protocol-neutral boundary for local,
// observation-driven mutation advice.  Implementations may understand a
// protocol, but callers only exchange current observations and legal Plan
// actions; an Advisor never receives or emits a future execution trace.
package protocolmutation

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/core"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/plan"
)

const SchemaVersion = "mutation-advisor-v1"

type Request struct {
	GoalID          string                 `json:"goal_id"`
	Waypoint        string                 `json:"waypoint"`
	WaypointIndex   int                    `json:"waypoint_index"`
	Observation     core.Observation       `json:"observation"`
	Roles           map[string]core.NodeID `json:"roles,omitempty"`
	AllowedActions  []plan.ActionKind      `json:"allowed_actions"`
	CandidateIndex  int                    `json:"candidate_index"`
	NoProgressCount int                    `json:"no_progress_count"`
}

type Candidate struct {
	Class          string                 `json:"class"`
	Action         plan.PlanAction        `json:"action"`
	Actions        []plan.PlanAction      `json:"local_actions,omitempty"`
	MessageID      core.MessageID         `json:"current_message_id,omitempty"`
	MessageType    string                 `json:"message_type,omitempty"`
	From           core.NodeID            `json:"from,omitempty"`
	To             core.NodeID            `json:"to,omitempty"`
	Roles          map[string]core.NodeID `json:"roles,omitempty"`
	Weight         int                    `json:"weight"`
	ReasonCode     string                 `json:"reason_code"`
	Reason         string                 `json:"reason"`
	ExpectedEffect string                 `json:"expected_effect"`
}

type RejectedCandidate struct {
	Class      string `json:"class"`
	ReasonCode string `json:"reason_code"`
	Reason     string `json:"reason"`
}

// Decision is deliberately verbose because it is the raw, recomputable audit
// record. ActualEffect and LocalProgress are filled after the selected Plan has
// executed; they are not inputs to the decision.
type Decision struct {
	SchemaVersion        string              `json:"schema_version"`
	AdvisorID            string              `json:"advisor_id"`
	GoalID               string              `json:"goal_id"`
	Waypoint             string              `json:"waypoint"`
	LocalStage           string              `json:"local_stage"`
	Preconditions        []string            `json:"preconditions"`
	RecommendedClasses   []string            `json:"recommended_action_classes"`
	Candidates           []Candidate         `json:"candidates"`
	Selected             Candidate           `json:"selected"`
	RejectedCandidates   []RejectedCandidate `json:"rejected_candidates,omitempty"`
	Fallback             string              `json:"fallback,omitempty"`
	ExpectedEffect       string              `json:"expected_effect"`
	ActualEffect         []string            `json:"actual_effect,omitempty"`
	LocalProgress        bool                `json:"local_progress"`
	BeforeObservationKey string              `json:"before_observation_key"`
	AfterObservationKey  string              `json:"after_observation_key,omitempty"`
	CandidateIndex       int                 `json:"candidate_index"`
	StableKey            string              `json:"stable_key"`
}

type Advisor interface {
	ID() string
	Advise(Request) (Decision, error)
}

type Summary struct {
	SchemaVersion              string         `json:"schema_version"`
	AdvisorCalls               int            `json:"advisor_calls"`
	CandidatesProduced         int            `json:"candidates_produced"`
	DecisionCount              int            `json:"decision_count"`
	LocalProgressCount         int            `json:"local_progress_count"`
	FallbackCount              int            `json:"fallback_count"`
	CurrentMessageUses         int            `json:"current_message_uses"`
	RecommendedActionsExecuted int            `json:"recommended_actions_executed"`
	ExpectedEffectReached      int            `json:"expected_effect_reached"`
	GoalAMajorityRounds        int            `json:"goal_a_majority_rounds"`
	GoalACompactionBoundaries  int            `json:"goal_a_compaction_boundaries"`
	GoalBEligibleCandidates    int            `json:"goal_b_eligible_candidates"`
	GoalBElectionsStarted      int            `json:"goal_b_elections_started"`
	GoalBElectionsCompleted    int            `json:"goal_b_elections_completed"`
	GoalBEarlyRestarts         int            `json:"goal_b_early_restarts"`
	InferenceNotes             []string       `json:"inference_notes"`
	ByStage                    map[string]int `json:"by_local_stage"`
	ByReason                   map[string]int `json:"by_reason_code"`
	ByAction                   map[string]int `json:"by_action_class"`
}

func Summarize(decisions []Decision) Summary {
	summary := Summary{
		SchemaVersion: SchemaVersion, AdvisorCalls: len(decisions),
		CandidatesProduced: len(decisions), DecisionCount: len(decisions),
		ByStage: make(map[string]int), ByReason: make(map[string]int),
		ByAction: make(map[string]int),
		InferenceNotes: []string{
			"executed/effect counts require a selected current message to leave the queue or the selected lifecycle/network postcondition to hold",
			"goal-specific counts are recomputed from stable LocalStage and ReasonCode fields",
		},
	}
	for _, decision := range decisions {
		summary.ByStage[decision.LocalStage]++
		summary.ByReason[decision.Selected.ReasonCode]++
		summary.ByAction[decision.Selected.Class]++
		if decision.LocalProgress {
			summary.LocalProgressCount++
			summary.RecommendedActionsExecuted++
			summary.ExpectedEffectReached++
		}
		if decision.Fallback != "" {
			summary.FallbackCount++
		}
		if decision.Selected.MessageID.Valid() {
			summary.CurrentMessageUses++
		}
		switch decision.Selected.ReasonCode {
		case "quorum-append", "quorum-response":
			summary.GoalAMajorityRounds++
		case "log-freshness", "active-timeout":
			summary.GoalBEligibleCandidates++
		case "vote-completion":
			summary.GoalBElectionsCompleted++
		case "early-restart-ablation":
			summary.GoalBEarlyRestarts++
		}
		if decision.LocalStage == "A5-snapshot-required-return-to-frontier" {
			summary.GoalACompactionBoundaries++
		}
		if decision.Selected.ReasonCode == "active-timeout" ||
			decision.Selected.ReasonCode == "log-freshness" {
			summary.GoalBElectionsStarted++
		}
	}
	return summary
}

func FinalizeDecision(decision Decision, after core.Observation) Decision {
	decision.AfterObservationKey = observationKey(after)
	decision.ActualEffect = observableEffects(decision, after)
	decision.LocalProgress = len(decision.ActualEffect) > 0
	return decision
}

func NewDecision(advisorID, stage string, request Request) Decision {
	return Decision{
		SchemaVersion: SchemaVersion,
		AdvisorID:     advisorID, GoalID: request.GoalID, Waypoint: request.Waypoint,
		LocalStage: stage, CandidateIndex: request.CandidateIndex,
		BeforeObservationKey: observationKey(request.Observation),
	}
}

func FinishDecision(decision *Decision) error {
	if len(decision.Candidates) == 0 {
		return fmt.Errorf("%s produced no candidate", decision.AdvisorID)
	}
	decision.Selected = decision.Candidates[0]
	decision.ExpectedEffect = decision.Selected.ExpectedEffect
	keyInput := struct {
		Advisor string
		Goal    string
		Stage   string
		Class   string
		Kinds   []plan.ActionKind
		Type    string
		From    core.NodeID
		To      core.NodeID
		Roles   map[string]core.NodeID
	}{
		decision.AdvisorID, decision.GoalID, decision.LocalStage,
		decision.Selected.Class, actionKinds(decision.Selected),
		decision.Selected.MessageType, decision.Selected.From,
		decision.Selected.To, decision.Selected.Roles,
	}
	encoded, err := json.Marshal(keyInput)
	if err != nil {
		return err
	}
	decision.StableKey = fmt.Sprintf("%x", sha256.Sum256(encoded))
	for _, action := range EffectiveActions(decision.Selected) {
		if err := action.Validate(); err != nil {
			return err
		}
	}
	return nil
}

func EffectiveActions(candidate Candidate) []plan.PlanAction {
	if len(candidate.Actions) > 0 {
		result := make([]plan.PlanAction, len(candidate.Actions))
		for index, action := range candidate.Actions {
			result[index] = action.Copy()
		}
		return result
	}
	return []plan.PlanAction{candidate.Action.Copy()}
}

func actionKinds(candidate Candidate) []plan.ActionKind {
	actions := EffectiveActions(candidate)
	result := make([]plan.ActionKind, len(actions))
	for index, action := range actions {
		result[index] = action.Kind
	}
	return result
}

func ActionAllowed(request Request, kind plan.ActionKind) bool {
	for _, allowed := range request.AllowedActions {
		if allowed == kind {
			return true
		}
	}
	return false
}

func observationKey(observation core.Observation) string {
	normalized := observation.Normalized()
	encoded, _ := json.Marshal(normalized)
	return fmt.Sprintf("%x", sha256.Sum256(encoded))
}

func observableEffects(decision Decision, after core.Observation) []string {
	if decision.Fallback != "" {
		return nil
	}
	selected := decision.Selected
	for _, message := range after.Messages {
		if selected.MessageID.Valid() && message.ID == selected.MessageID {
			return nil
		}
	}
	if selected.MessageID.Valid() {
		return []string{"selected-current-message-left-queue"}
	}
	switch selected.Action.Kind {
	case plan.ActionPartition:
		if after.NetworkPartition != nil {
			return []string{"partition-active"}
		}
	case plan.ActionHeal:
		if after.NetworkPartition == nil {
			return []string{"partition-healed"}
		}
	case plan.ActionCrash:
		for _, node := range after.Nodes {
			if node.ID == selected.Action.Node && node.Status == core.NodeCrashed {
				return []string{"selected-node-crashed"}
			}
		}
	case plan.ActionRestart:
		for _, node := range after.Nodes {
			if node.ID == selected.Action.Node && node.Status == core.NodeRunning {
				return []string{"selected-node-running"}
			}
		}
	case plan.ActionTimeout:
		for _, node := range after.Nodes {
			if node.ID != selected.Action.Node {
				continue
			}
			role, _ := node.Semantic["role"].(string)
			if role == "candidate" || role == "leader" {
				return []string{"selected-node-entered-election"}
			}
		}
	case plan.ActionRequest:
		if decision.BeforeObservationKey != observationKey(after) {
			return []string{"request-changed-observation"}
		}
	case plan.ActionAdvanceTicks:
		if decision.BeforeObservationKey != observationKey(after) {
			return []string{"logical-time-advanced"}
		}
	}
	return nil
}
