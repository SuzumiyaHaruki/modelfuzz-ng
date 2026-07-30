// Frozen diagnostic surface: formation-failure classification explains why a
// Branch did not form; it does not guide the default search path.
package goalsearch

import (
	"fmt"
	"sort"

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/core"
)

type FormationFailureCause string

const (
	FormationNoEntryState                    FormationFailureCause = "no-entry-state"
	FormationBindingFailed                   FormationFailureCause = "binding-failed"
	FormationPrerequisiteNotGenerated        FormationFailureCause = "prerequisite-not-generated"
	FormationPrerequisiteGeneratedUnselected FormationFailureCause = "prerequisite-generated-not-selected"
	FormationRequiredMessageAbsent           FormationFailureCause = "required-message-absent"
	FormationRequiredMessageBlocked          FormationFailureCause = "required-message-blocked"
	FormationRequiredMessageDropped          FormationFailureCause = "required-message-dropped"
	FormationWrongMessageClass               FormationFailureCause = "wrong-message-class"
	FormationElectionNotCompleted            FormationFailureCause = "election-not-completed"
	FormationBackupLogNotFresh               FormationFailureCause = "backup-log-not-fresh"
	FormationLagInsufficient                 FormationFailureCause = "lag-insufficient"
	FormationCompactionNotCrossed            FormationFailureCause = "compaction-boundary-not-crossed"
	FormationHealTimingMissed                FormationFailureCause = "heal-timing-missed"
	FormationRetryNotTriggered               FormationFailureCause = "retry-not-triggered"
	FormationPrefixNotPreserved              FormationFailureCause = "prefix-not-preserved"
	FormationEvidenceInvalidated             FormationFailureCause = "evidence-invalidated"
	FormationBranchContradicted              FormationFailureCause = "branch-contradicted"
	FormationBudgetDiluted                   FormationFailureCause = "budget-diluted"
	FormationBudgetExhausted                 FormationFailureCause = "budget-exhausted"
	FormationCurrentlyInfeasible             FormationFailureCause = "currently-infeasible"
	FormationPermanentlyInfeasible           FormationFailureCause = "permanently-infeasible"
	FormationEvaluatorUndecidable            FormationFailureCause = "evaluator-undecidable"
)

type FormationFailureRecord struct {
	SchemaVersion                 string                `json:"schema_version"`
	RunID                         string                `json:"run_id"`
	CandidateIndex                int                   `json:"candidate_index"`
	GoalID                        GoalID                `json:"goal_id"`
	BranchTemplateID              BranchTemplateID      `json:"branch_template_id"`
	FailedStage                   string                `json:"failed_stage"`
	PrimaryCause                  FormationFailureCause `json:"primary_cause"`
	SuggestedDiagnosticCategory   string                `json:"suggested_diagnostic_category"`
	SupportingEvidence            []string              `json:"supporting_evidence,omitempty"`
	FirstBlockingStep             int                   `json:"first_blocking_step"`
	DeepestWaypoint               int                   `json:"deepest_waypoint"`
	DeepestEvidenceLevel          EvidenceLevel         `json:"deepest_evidence_level"`
	MissingEvidence               []string              `json:"missing_evidence,omitempty"`
	BlockingCondition             string                `json:"blocking_condition"`
	AvailableButUnselectedAction  string                `json:"available_but_unselected_action,omitempty"`
	RequiredMessageAbsent         bool                  `json:"required_message_absent"`
	RequiredMessagePresentBlocked bool                  `json:"required_message_present_but_blocked"`
	PrefixLost                    bool                  `json:"prefix_lost"`
	MutationGeneratedWrongClass   bool                  `json:"mutation_generated_wrong_class"`
	BudgetExhaustedBeforeStage    bool                  `json:"budget_exhausted_before_stage"`
	BranchContradicted            bool                  `json:"branch_contradicted"`
	Undecidable                   bool                  `json:"undecidable"`
	StableKey                     string                `json:"stable_key"`
}

func ClassifyFormationFailure(
	runID string,
	candidate int,
	template BehaviorBranchTemplate,
	instance BehaviorBranchInstance,
	vector BranchEvidenceVector,
	evaluation EvaluationResult,
	budgetExhausted bool,
	prefixLost bool,
) FormationFailureRecord {
	record := FormationFailureRecord{
		SchemaVersion:              FormationFailureSchemaVersion,
		RunID:                      runID,
		CandidateIndex:             candidate,
		GoalID:                     template.GoalID,
		BranchTemplateID:           template.BranchTemplateID,
		FailedStage:                evaluation.Instance.Progress.CurrentWaypointID,
		FirstBlockingStep:          -1,
		DeepestWaypoint:            evaluation.Instance.Progress.CompletedWaypointCount,
		DeepestEvidenceLevel:       vector.HighestLevel,
		PrefixLost:                 prefixLost,
		BudgetExhaustedBeforeStage: budgetExhausted,
		BranchContradicted:         vector.Contradicted,
	}
	for _, dimension := range vector.Dimensions {
		switch dimension.Status {
		case EvidenceSupported, EvidenceCommitted:
			record.SupportingEvidence = append(record.SupportingEvidence, dimension.EvidenceID)
		case EvidenceUnknown, EvidenceInvalidated, EvidenceContradicted:
			if dimension.RequiredForCommitment {
				record.MissingEvidence = append(record.MissingEvidence, dimension.EvidenceID)
			}
		}
	}
	sort.Strings(record.SupportingEvidence)
	sort.Strings(record.MissingEvidence)
	switch {
	case instance.Feasibility == BranchPermanentlyInfeasible:
		record.PrimaryCause = FormationPermanentlyInfeasible
		record.BlockingCondition = "static Branch applicability is permanently false"
	case vector.Contradicted:
		record.PrimaryCause = FormationBranchContradicted
		record.FirstBlockingStep = instance.Deviation.StepIndex
		record.BlockingCondition = instance.Deviation.Reason
		record.MutationGeneratedWrongClass =
			template.GoalID == GoalRestartHigherTermMessage
	case vector.InvalidationCount > 0:
		record.PrimaryCause = FormationEvidenceInvalidated
		record.BlockingCondition = "previously supported causal evidence no longer holds"
	case prefixLost:
		record.PrimaryCause = FormationPrefixNotPreserved
		record.BlockingCondition = "selected causal prefix was not preserved"
	case instance.Feasibility == BranchCurrentlyInfeasible:
		record.PrimaryCause = FormationCurrentlyInfeasible
		record.BlockingCondition = "current prefix cannot produce the next required event yet"
	default:
		classifyFormationByGoal(&record, template, evaluation)
	}
	if record.PrimaryCause == "" && budgetExhausted {
		record.PrimaryCause = FormationBudgetExhausted
		record.BlockingCondition = "candidate budget ended before commitment or full realization"
	}
	if record.PrimaryCause == "" {
		record.PrimaryCause = FormationEvaluatorUndecidable
		record.BlockingCondition = "available prefix evidence is insufficient for a more specific cause"
		record.Undecidable = true
	}
	if record.FirstBlockingStep < 0 {
		record.FirstBlockingStep = max(
			evaluation.Instance.Progress.LastProgressStep,
			vector.PrefixProtectionStep,
		)
	}
	if record.PrimaryCause == FormationRequiredMessageAbsent {
		record.RequiredMessageAbsent = true
	}
	if record.PrimaryCause == FormationRequiredMessageBlocked {
		record.RequiredMessagePresentBlocked = true
	}
	record.SuggestedDiagnosticCategory = formationDiagnosticCategory(record.PrimaryCause)
	copyForKey := record
	copyForKey.RunID = ""
	copyForKey.CandidateIndex = 0
	copyForKey.FirstBlockingStep = -1
	copyForKey.StableKey = ""
	record.StableKey = stableHash(copyForKey)
	return record
}

func formationDiagnosticCategory(cause FormationFailureCause) string {
	switch cause {
	case FormationNoEntryState, FormationBindingFailed:
		return "entry-and-binding"
	case FormationRequiredMessageAbsent, FormationRequiredMessageBlocked,
		FormationRequiredMessageDropped, FormationWrongMessageClass:
		return "message-generation-and-selection"
	case FormationElectionNotCompleted, FormationBackupLogNotFresh,
		FormationLagInsufficient, FormationCompactionNotCrossed:
		return "protocol-prerequisite"
	case FormationHealTimingMissed, FormationRetryNotTriggered,
		FormationPrefixNotPreserved, FormationEvidenceInvalidated,
		FormationBranchContradicted:
		return "causal-order-and-prefix"
	case FormationBudgetDiluted, FormationBudgetExhausted:
		return "budget-allocation"
	case FormationCurrentlyInfeasible, FormationPermanentlyInfeasible:
		return "branch-feasibility"
	default:
		return "evaluator-undecidable"
	}
}

func classifyFormationByGoal(
	record *FormationFailureRecord,
	template BehaviorBranchTemplate,
	evaluation EvaluationResult,
) {
	completed := evaluation.Instance.Progress.CompletedWaypointCount
	if completed == 0 {
		if _, leader := evaluation.Instance.Bindings[SymbolLeader]; !leader {
			record.PrimaryCause = FormationNoEntryState
			record.BlockingCondition = "no stable Leader and target binding"
		} else {
			record.PrimaryCause = FormationBindingFailed
			record.BlockingCondition = "entry state exists but target binding is incomplete"
		}
		return
	}
	if template.GoalID == GoalSnapshotCatchUpAfterPartition {
		switch completed {
		case 1:
			record.PrimaryCause = FormationPrerequisiteNotGenerated
			record.BlockingCondition = "target-isolating partition was not generated"
		case 2:
			record.PrimaryCause = FormationLagInsufficient
			record.BlockingCondition = "partition did not create the required semantic lag"
		case 3:
			record.PrimaryCause = FormationCompactionNotCrossed
			record.BlockingCondition = "leader progress did not cross the snapshot boundary"
		case 4:
			record.PrimaryCause = FormationHealTimingMissed
			record.BlockingCondition = "required Heal/Snapshot ordering was not formed"
		case 5:
			record.PrimaryCause = FormationRequiredMessageAbsent
			record.BlockingCondition = "required MsgSnap is absent from the target queue"
		case 6:
			if template.BranchTemplateID == BranchASnapshotFailureRetry {
				record.PrimaryCause = FormationRetryNotTriggered
				record.BlockingCondition = "snapshot failure occurred without a later retry"
			} else {
				record.PrimaryCause = FormationEvaluatorUndecidable
				record.BlockingCondition = "snapshot was delivered but full recovery evidence is absent"
			}
		}
		return
	}
	switch completed {
	case 1:
		record.PrimaryCause = FormationPrerequisiteNotGenerated
		record.BlockingCondition = "target Crash was not generated"
	case 2:
		record.PrimaryCause = FormationElectionNotCompleted
		record.BlockingCondition = "active peers did not complete the required term advance"
	case 3:
		record.PrimaryCause = FormationPrerequisiteNotGenerated
		record.BlockingCondition = "target Restart was not generated after term advance"
	case 4:
		class, blocked, present := requiredBranchMessage(
			template, evaluation.FinalObservation, evaluation.Instance.Bindings,
		)
		switch {
		case blocked:
			record.PrimaryCause = FormationRequiredMessageBlocked
			record.BlockingCondition = "required higher-term " + class + " exists but is blocked"
			record.AvailableButUnselectedAction = "heal or deliver required message after connectivity"
		case present:
			record.PrimaryCause = FormationPrerequisiteGeneratedUnselected
			record.BlockingCondition = "required higher-term " + class + " exists but mutation did not select it"
			record.AvailableButUnselectedAction = "deliver " + class + " to TargetFollower"
		default:
			record.PrimaryCause = FormationRequiredMessageAbsent
			record.BlockingCondition = "required higher-term " + class + " was not generated"
		}
	case 5:
		record.PrimaryCause = FormationPrerequisiteGeneratedUnselected
		record.BlockingCondition = "bound higher-term message was not delivered"
		record.AvailableButUnselectedAction = "deliver bound higher-term message"
	}
}

func requiredBranchMessage(
	template BehaviorBranchTemplate,
	observation core.Observation,
	bindings map[Symbol]Binding,
) (class string, blocked, present bool) {
	target := boundNode(bindings, SymbolTargetFollower)
	class = template.PlannedDimensions.KeyMessageClass
	for _, message := range observation.Messages {
		if message.To != target || branchMessageClass(message.TypeHint) != class ||
			messageTermRelation(observation, message, target) != "higher" {
			continue
		}
		present = true
		blocked = blocked || message.Blocked
	}
	return class, blocked, present
}

func (cause FormationFailureCause) Validate() error {
	switch cause {
	case FormationNoEntryState, FormationBindingFailed, FormationPrerequisiteNotGenerated,
		FormationPrerequisiteGeneratedUnselected, FormationRequiredMessageAbsent,
		FormationRequiredMessageBlocked, FormationRequiredMessageDropped,
		FormationWrongMessageClass, FormationElectionNotCompleted, FormationBackupLogNotFresh,
		FormationLagInsufficient, FormationCompactionNotCrossed, FormationHealTimingMissed,
		FormationRetryNotTriggered, FormationPrefixNotPreserved, FormationEvidenceInvalidated,
		FormationBranchContradicted, FormationBudgetDiluted, FormationBudgetExhausted,
		FormationCurrentlyInfeasible, FormationPermanentlyInfeasible,
		FormationEvaluatorUndecidable:
		return nil
	default:
		return fmt.Errorf("unknown formation failure cause %q", cause)
	}
}
