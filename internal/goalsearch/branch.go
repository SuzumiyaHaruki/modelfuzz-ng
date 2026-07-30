// Frozen experimental surface: Branch schemas and analysis are retained for
// historical artifact compatibility and diagnostics, not as the default
// Waypoint Frontier method.
package goalsearch

import (
	"fmt"
	"sort"
	"strconv"

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/core"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/plan"
)

const BranchSchemaVersion = "raft-behavior-branches-v1-prototype"

type BranchTemplateID string
type BindingPolicy string
type BranchFeasibility string
type BranchAwareness string
type BranchDimensionAblation string

const (
	BranchADropAppend           BranchTemplateID = "goal-a-drop-append"
	BranchADelayedDelivery      BranchTemplateID = "goal-a-delayed-delivery"
	BranchASnapshotBeforeHeal   BranchTemplateID = "goal-a-snapshot-before-heal"
	BranchASnapshotAfterHeal    BranchTemplateID = "goal-a-snapshot-after-heal"
	BranchASnapshotFailureRetry BranchTemplateID = "goal-a-snapshot-failure-retry"
	BranchBHigherHeartbeat      BranchTemplateID = "goal-b-higher-term-heartbeat"
	BranchBHigherApp            BranchTemplateID = "goal-b-higher-term-msgapp"
	BranchBHigherVote           BranchTemplateID = "goal-b-higher-term-vote"
)

const (
	BindingLeastAdvancedEligible BindingPolicy = "least-advanced-eligible"
)

const (
	BranchFeasible              BranchFeasibility = "feasible"
	BranchCurrentlyInfeasible   BranchFeasibility = "currently_infeasible"
	BranchPermanentlyInfeasible BranchFeasibility = "permanently_infeasible"
	BranchNotDecidable          BranchFeasibility = "not_decidable"
	BranchViolated              BranchFeasibility = "violated"
	BranchCompleted             BranchFeasibility = "completed"
)

const (
	BranchPlannedOnly   BranchAwareness = "planned-only"
	BranchRealizedAware BranchAwareness = "realized-aware"
)

const (
	BranchAblationNone        BranchDimensionAblation = "none"
	BranchAblationKeyMessage  BranchDimensionAblation = "key-message"
	BranchAblationHealTiming  BranchDimensionAblation = "heal-timing"
	BranchAblationLagMethod   BranchDimensionAblation = "lag-construction"
	BranchAblationTermAdvance BranchDimensionAblation = "term-advance"
)

func (a BranchAwareness) Validate() error {
	switch a {
	case BranchPlannedOnly, BranchRealizedAware:
		return nil
	default:
		return fmt.Errorf("unknown branch awareness %q", a)
	}
}

func (a BranchDimensionAblation) Validate() error {
	switch a {
	case BranchAblationNone, BranchAblationKeyMessage, BranchAblationHealTiming,
		BranchAblationLagMethod, BranchAblationTermAdvance:
		return nil
	default:
		return fmt.Errorf("unknown branch dimension ablation %q", a)
	}
}

// BranchDimensions contains only relative, semantic categories. Concrete
// bindings remain in BehaviorBranchInstance and are deliberately excluded.
type BranchDimensions struct {
	TargetSelectionClass   string `json:"target_selection_class,omitempty"`
	PartitionTopologyClass string `json:"partition_topology_class,omitempty"`
	LagConstructionClass   string `json:"lag_construction_class,omitempty"`
	FaultDurationClass     string `json:"fault_duration_class,omitempty"`
	TermAdvanceClass       string `json:"term_advance_class,omitempty"`
	HealTimingClass        string `json:"heal_timing_class,omitempty"`
	SnapshotRouteClass     string `json:"snapshot_route_class,omitempty"`
	RecoveryRouteClass     string `json:"recovery_route_class,omitempty"`
	KeyMessageClass        string `json:"key_message_class,omitempty"`
	PreDeliverySequence    string `json:"pre_delivery_sequence_class,omitempty"`
}

func (d BranchDimensions) Ablated(ablation BranchDimensionAblation) BranchDimensions {
	switch ablation {
	case BranchAblationKeyMessage:
		d.KeyMessageClass = ""
	case BranchAblationHealTiming:
		d.HealTimingClass = ""
	case BranchAblationLagMethod:
		d.LagConstructionClass = ""
	case BranchAblationTermAdvance:
		d.TermAdvanceClass = ""
	}
	return d
}

func (d BranchDimensions) StableKey(ablation BranchDimensionAblation) string {
	return stableHash(d.Ablated(ablation))
}

type BranchApplicability struct {
	RequiresSnapshot  bool   `json:"requires_snapshot"`
	RequiresPartition bool   `json:"requires_partition"`
	ModelProfile      string `json:"model_profile,omitempty"`
}

type BranchMutationPreference struct {
	ActionKind  string `json:"action_kind,omitempty"`
	MessageType string `json:"message_type,omitempty"`
	Weight      int    `json:"weight"`
	Condition   string `json:"condition,omitempty"`
}

type BehaviorBranchTemplate struct {
	SchemaVersion              string                     `json:"schema_version"`
	BranchTemplateID           BranchTemplateID           `json:"branch_template_id"`
	GoalID                     GoalID                     `json:"goal_id"`
	Name                       string                     `json:"name"`
	Description                string                     `json:"description"`
	ApplicabilityConstraints   BranchApplicability        `json:"applicability_constraints"`
	PlannedDimensions          BranchDimensions           `json:"planned_dimensions"`
	BindingPolicy              BindingPolicy              `json:"binding_policy"`
	MutationPreferences        []BranchMutationPreference `json:"mutation_preferences"`
	ForbiddenPatterns          []string                   `json:"forbidden_patterns"`
	ExpectedWaypointPath       []string                   `json:"expected_waypoint_path"`
	BranchFeasibilityPredicate string                     `json:"branch_feasibility_predicate"`
	RealizationEvidence        []string                   `json:"realization_evidence"`
	SupportedNodeCounts        []int                      `json:"supported_node_counts"`
	StableKey                  string                     `json:"stable_key"`
}

func (t BehaviorBranchTemplate) Validate() error {
	if t.SchemaVersion != BranchSchemaVersion {
		return fmt.Errorf("branch %q schema=%q want %q",
			t.BranchTemplateID, t.SchemaVersion, BranchSchemaVersion)
	}
	if t.BranchTemplateID == "" || t.GoalID == "" || t.Name == "" ||
		t.Description == "" || t.BindingPolicy == "" {
		return fmt.Errorf("branch template identity is incomplete")
	}
	if len(t.ExpectedWaypointPath) == 0 || len(t.SupportedNodeCounts) == 0 {
		return fmt.Errorf("branch %q has no waypoint path or supported node count", t.BranchTemplateID)
	}
	for _, preference := range t.MutationPreferences {
		if preference.Weight <= 0 {
			return fmt.Errorf("branch %q contains non-positive mutation weight", t.BranchTemplateID)
		}
	}
	copy := t
	copy.StableKey = ""
	want := stableHash(copy)
	if t.StableKey != "" && t.StableKey != want {
		return fmt.Errorf("branch %q stable key is stale", t.BranchTemplateID)
	}
	return nil
}

func finalizeBranchTemplate(template BehaviorBranchTemplate) BehaviorBranchTemplate {
	sort.Ints(template.SupportedNodeCounts)
	template.StableKey = ""
	template.StableKey = stableHash(template)
	return template
}

func BranchCatalog() []BehaviorBranchTemplate {
	aPath := []string{"W1", "W2", "W3", "W4", "W5", "W6", "W7"}
	bPath := []string{"W1", "W2", "W3", "W4", "W5", "W6"}
	commonA := func(id BranchTemplateID, name, description string, dimensions BranchDimensions,
		preferences []BranchMutationPreference, evidence []string) BehaviorBranchTemplate {
		return finalizeBranchTemplate(BehaviorBranchTemplate{
			SchemaVersion: BranchSchemaVersion, BranchTemplateID: id,
			GoalID: GoalSnapshotCatchUpAfterPartition, Name: name, Description: description,
			ApplicabilityConstraints: BranchApplicability{
				RequiresSnapshot: true, RequiresPartition: true, ModelProfile: "storage-snapshot",
			},
			PlannedDimensions: dimensions, BindingPolicy: BindingLeastAdvancedEligible,
			MutationPreferences:        preferences,
			ForbiddenPatterns:          []string{"rebind-goal-symbol", "modify-preserved-prefix", "complete-success-suffix"},
			ExpectedWaypointPath:       append([]string(nil), aPath...),
			BranchFeasibilityPredicate: "snapshot enabled; target-isolating partition supported",
			RealizationEvidence:        evidence, SupportedNodeCounts: []int{3, 5},
		})
	}
	commonB := func(
		id BranchTemplateID, name, messageType, preDelivery string,
	) BehaviorBranchTemplate {
		return finalizeBranchTemplate(BehaviorBranchTemplate{
			SchemaVersion: BranchSchemaVersion, BranchTemplateID: id,
			GoalID: GoalRestartHigherTermMessage, Name: name,
			Description:              "Restart 后通过真实 higher-term " + messageType + " 路径推进目标节点 term",
			ApplicabilityConstraints: BranchApplicability{ModelProfile: "storage-snapshot"},
			PlannedDimensions: BranchDimensions{
				TargetSelectionClass: "least-advanced-eligible",
				TermAdvanceClass:     "timeout-induced-term-advance",
				KeyMessageClass:      messageType,
				PreDeliverySequence:  preDelivery,
				RecoveryRouteClass:   "restart-before-higher-term",
			},
			BindingPolicy: BindingLeastAdvancedEligible,
			MutationPreferences: []BranchMutationPreference{
				{ActionKind: "deliver", MessageType: messageType, Weight: 8, Condition: "after-restart"},
				{ActionKind: "drop", Weight: 6, Condition: "discard-other-term-message-before-restart"},
				{ActionKind: "advance_ticks", Weight: 3, Condition: "create-selected-message-class"},
			},
			ForbiddenPatterns:          []string{"same-term-as-higher-term", "rebind-goal-symbol", "complete-success-suffix"},
			ExpectedWaypointPath:       append([]string(nil), bPath...),
			BranchFeasibilityPredicate: "selected higher-term message class is observable after restart",
			RealizationEvidence:        []string{"restart", "message-pending:type=" + messageType, "exact-message-delivery"},
			SupportedNodeCounts:        []int{3, 5},
		})
	}
	result := []BehaviorBranchTemplate{
		commonA(
			BranchADropAppend, "Drop-Append Lag",
			"分区期间丢弃发往目标的 MsgApp，同时保持多数派复制，随后恢复并通过 Snapshot 追赶",
			BranchDimensions{
				TargetSelectionClass:   "least-advanced-eligible",
				PartitionTopologyClass: "single-target-vs-quorum",
				LagConstructionClass:   "drop-append", FaultDurationClass: "long",
				RecoveryRouteClass: "snapshot-catch-up",
			},
			[]BranchMutationPreference{
				{ActionKind: "drop", MessageType: "MsgApp", Weight: 8, Condition: "leader-to-target-before-heal"},
				{ActionKind: "request", Weight: 4, Condition: "majority-remains-connected"},
			},
			[]string{"drop:MsgApp:leader-to-target", "snapshot-required", "snapshot-installed"},
		),
		commonA(
			BranchADelayedDelivery, "Delayed-Delivery Lag",
			"保留跨 Heal 的旧 MsgApp，观察旧复制消息与 MsgSnap 的真实先后关系",
			BranchDimensions{
				TargetSelectionClass:   "least-advanced-eligible",
				PartitionTopologyClass: "single-target-vs-quorum",
				LagConstructionClass:   "delayed-delivery", FaultDurationClass: "long",
				RecoveryRouteClass: "snapshot-catch-up",
			},
			[]BranchMutationPreference{
				{ActionKind: "request", Weight: 5, Condition: "while-target-isolated"},
				{ActionKind: "heal", Weight: 4, Condition: "after-snapshot-required"},
				{ActionKind: "deliver", MessageType: "MsgApp", Weight: 3, Condition: "after-heal"},
			},
			[]string{"blocked-MsgApp-survives-heal", "old-MsgApp-order", "snapshot-installed"},
		),
		commonA(
			BranchASnapshotBeforeHeal, "Snapshot Before Heal",
			"分区仍存在时真实产生发往目标的 MsgSnap，Heal 后再投递",
			BranchDimensions{
				TargetSelectionClass:   "least-advanced-eligible",
				PartitionTopologyClass: "single-target-vs-quorum",
				FaultDurationClass:     "long", HealTimingClass: "snapshot-before-heal",
				SnapshotRouteClass: "queued-while-partitioned",
				RecoveryRouteClass: "snapshot-catch-up",
			},
			[]BranchMutationPreference{
				{ActionKind: "request", Weight: 5, Condition: "keep-partition-until-MsgSnap"},
				{ActionKind: "deliver", MessageType: "MsgAppResp", Weight: 4, Condition: "drive-next-index-backoff"},
			},
			[]string{"MsgSnap-enqueued-while-partitioned", "heal-after-MsgSnap", "snapshot-installed"},
		),
		commonA(
			BranchASnapshotAfterHeal, "Snapshot After Heal",
			"Heal 时尚无 MsgSnap，恢复后由复制拒绝或后续协议行为真实触发 MsgSnap",
			BranchDimensions{
				TargetSelectionClass:   "least-advanced-eligible",
				PartitionTopologyClass: "single-target-vs-quorum",
				FaultDurationClass:     "long", HealTimingClass: "snapshot-after-heal",
				SnapshotRouteClass: "generated-after-heal",
				RecoveryRouteClass: "snapshot-catch-up",
			},
			[]BranchMutationPreference{
				{ActionKind: "heal", Weight: 5, Condition: "after-snapshot-required-before-MsgSnap"},
				{ActionKind: "deliver", MessageType: "MsgAppResp", Weight: 5, Condition: "after-heal"},
			},
			[]string{"heal-without-pending-MsgSnap", "MsgSnap-first-seen-after-heal", "snapshot-installed"},
		),
		commonA(
			BranchASnapshotFailureRetry, "Snapshot Failure and Retry",
			"第一次 MsgSnap 被受控 Drop 并报告失败，随后由真实 Raft retry 完成追赶",
			BranchDimensions{
				TargetSelectionClass:   "least-advanced-eligible",
				PartitionTopologyClass: "single-target-vs-quorum",
				FaultDurationClass:     "long",
				HealTimingClass:        "snapshot-after-heal",
				SnapshotRouteClass:     "failure-then-retry",
				RecoveryRouteClass:     "snapshot-catch-up",
			},
			[]BranchMutationPreference{
				{ActionKind: "drop", MessageType: "MsgSnap", Weight: 8, Condition: "first-snapshot-only"},
				{ActionKind: "advance_ticks", Weight: 5, Condition: "after-snapshot-failure"},
				{ActionKind: "deliver", MessageType: "MsgSnap", Weight: 6, Condition: "retry-snapshot"},
			},
			[]string{"first-MsgSnap-dropped", "snapshot-failure-reported", "retry-MsgSnap", "snapshot-installed"},
		),
		commonB(BranchBHigherHeartbeat, "Higher-Term MsgHeartbeat", "MsgHeartbeat", "tick-before-higher"),
		commonB(BranchBHigherApp, "Higher-Term MsgApp", "MsgApp", "request-before-higher"),
		commonB(BranchBHigherVote, "Higher-Term Vote", "vote-message", "immediate"),
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].BranchTemplateID < result[j].BranchTemplateID
	})
	return result
}

func BranchTemplates(goal GoalID) []BehaviorBranchTemplate {
	var result []BehaviorBranchTemplate
	for _, template := range BranchCatalog() {
		if template.GoalID == goal {
			result = append(result, template)
		}
	}
	return result
}

func BranchTemplate(id BranchTemplateID) (BehaviorBranchTemplate, error) {
	for _, template := range BranchCatalog() {
		if template.BranchTemplateID == id {
			if err := template.Validate(); err != nil {
				return BehaviorBranchTemplate{}, err
			}
			return template, nil
		}
	}
	return BehaviorBranchTemplate{}, fmt.Errorf("unknown behavior branch %q", id)
}

type BranchEnvironment struct {
	NodeCount         int
	ModelProfile      string
	SnapshotThreshold uint64
	PartitionEnabled  bool
}

type BranchFeasibilityResult struct {
	BranchTemplateID BranchTemplateID  `json:"branch_template_id"`
	Status           BranchFeasibility `json:"status"`
	Reason           string            `json:"reason"`
}

func EvaluateBranchFeasibility(
	template BehaviorBranchTemplate, environment BranchEnvironment,
) BranchFeasibilityResult {
	result := BranchFeasibilityResult{BranchTemplateID: template.BranchTemplateID}
	switch {
	case template.BranchTemplateID == BranchASnapshotBeforeHeal:
		result.Status, result.Reason = BranchPermanentlyInfeasible,
			"current target-isolating partition cannot obtain the target rejection needed to enqueue MsgSnap before Heal"
	case !containsInt(template.SupportedNodeCounts, environment.NodeCount):
		result.Status, result.Reason = BranchPermanentlyInfeasible, "unsupported node count"
	case template.ApplicabilityConstraints.ModelProfile != "" &&
		template.ApplicabilityConstraints.ModelProfile != environment.ModelProfile:
		result.Status, result.Reason = BranchPermanentlyInfeasible, "model profile mismatch"
	case template.ApplicabilityConstraints.RequiresSnapshot && environment.SnapshotThreshold == 0:
		result.Status, result.Reason = BranchPermanentlyInfeasible, "snapshot is disabled"
	case template.ApplicabilityConstraints.RequiresPartition && !environment.PartitionEnabled:
		result.Status, result.Reason = BranchPermanentlyInfeasible, "partition actions are disabled"
	default:
		result.Status, result.Reason = BranchFeasible, "static applicability constraints satisfied"
	}
	return result
}

type BranchEvidence struct {
	StepIndex int               `json:"step_index"`
	Kind      string            `json:"kind"`
	Category  string            `json:"category,omitempty"`
	Relation  string            `json:"relation,omitempty"`
	Details   map[string]string `json:"details,omitempty"`
}

type PlannedBranchSignature struct {
	BranchTemplateID BranchTemplateID `json:"branch_template_id"`
	Dimensions       BranchDimensions `json:"dimensions"`
	StableKey        string           `json:"stable_key"`
}

func PlannedSignature(template BehaviorBranchTemplate, ablation BranchDimensionAblation) PlannedBranchSignature {
	dimensions := template.PlannedDimensions.Ablated(ablation)
	return PlannedBranchSignature{
		BranchTemplateID: template.BranchTemplateID,
		Dimensions:       dimensions, StableKey: dimensions.StableKey(BranchAblationNone),
	}
}

type RealizedBranchSignature struct {
	Dimensions          BranchDimensions `json:"dimensions"`
	Decidable           bool             `json:"decidable"`
	FirstDeterminedStep int              `json:"first_determined_step"`
	MatchedTemplateID   BranchTemplateID `json:"matched_template_id,omitempty"`
	Evidence            []BranchEvidence `json:"evidence,omitempty"`
	StableKey           string           `json:"stable_key"`
}

type BranchDeviation struct {
	Occurred  bool             `json:"occurred"`
	Waypoint  string           `json:"waypoint,omitempty"`
	StepIndex int              `json:"step_index,omitempty"`
	Reason    string           `json:"reason,omitempty"`
	Evidence  []BranchEvidence `json:"evidence,omitempty"`
}

type BehaviorBranchInstance struct {
	SchemaVersion           string                  `json:"schema_version"`
	BranchTemplateID        BranchTemplateID        `json:"branch_template_id"`
	BranchInstanceID        string                  `json:"branch_instance_id"`
	GoalInstanceID          string                  `json:"goal_instance_id"`
	PlannedBranchSignature  PlannedBranchSignature  `json:"planned_branch_signature"`
	RealizedBranchSignature RealizedBranchSignature `json:"realized_branch_signature"`
	GoalBindings            map[Symbol]Binding      `json:"goal_bindings"`
	BranchBindings          map[string]string       `json:"branch_bindings,omitempty"`
	State                   string                  `json:"state"`
	CurrentWaypoint         string                  `json:"current_waypoint,omitempty"`
	Progress                GoalProgress            `json:"progress"`
	Evidence                []BranchEvidence        `json:"branch_evidence,omitempty"`
	Deviation               BranchDeviation         `json:"deviation"`
	Feasibility             BranchFeasibility       `json:"feasibility"`
	FrontierReferences      []string                `json:"frontier_references,omitempty"`
	StableKey               string                  `json:"stable_key"`
}

// AnalyzeBranch derives a causal path from a concrete prefix. Calling it on
// progressively longer prefixes never reads information beyond that prefix.
func AnalyzeBranch(
	template BehaviorBranchTemplate,
	evaluation EvaluationResult,
	initial core.Observation,
	trace core.Trace,
	ablation BranchDimensionAblation,
) (BehaviorBranchInstance, error) {
	if err := template.Validate(); err != nil {
		return BehaviorBranchInstance{}, err
	}
	if template.GoalID != evaluation.Instance.GoalID {
		return BehaviorBranchInstance{}, fmt.Errorf("branch %q belongs to goal %q, evaluation is %q",
			template.BranchTemplateID, template.GoalID, evaluation.Instance.GoalID)
	}
	tracker, err := newPrefixTracker(initial)
	if err != nil {
		return BehaviorBranchInstance{}, err
	}
	planned := PlannedSignature(template, ablation)
	realized := RealizedBranchSignature{FirstDeterminedStep: -1}
	dimensions := BranchDimensions{}
	evidence := make([]BranchEvidence, 0)
	leader := boundNode(evaluation.Instance.Bindings, SymbolLeader)
	target := boundNode(evaluation.Instance.Bindings, SymbolTargetFollower)
	if target.Valid() {
		dimensions.TargetSelectionClass = "least-advanced-eligible"
	}
	partitionStep, healStep, firstSnapshotStep, targetCrashStep, restartStep := -1, -1, -1, -1, -1
	lagEvidenceStep, termEvidenceStep, messageEvidenceStep := -1, -1, -1
	snapshotWhilePartitioned := false
	snapshotDropped := false
	snapshotSendCount := 0
	delayedAppendSeen, droppedAppend, suppressedResponse := false, false, false
	tickAfterRestart, requestAfterRestart, sameTermBeforeHigher := false, false, false
	termCause := ""
	keyMessage := ""
	lastTraceStep := -1
	if len(trace.Steps) > 0 {
		lastTraceStep = int(trace.Steps[len(trace.Steps)-1].Index)
	}
	for _, record := range trace.Steps {
		var queuedBefore trackedMessage
		if record.Action.Selector != nil {
			queue := tracker.queues[record.Action.Selector.Link]
			if record.Action.Selector.Position >= 0 && record.Action.Selector.Position < len(queue) {
				queuedBefore = queue[record.Action.Selector.Position]
			}
		}
		message, justHealed, applyErr := tracker.apply(record)
		if applyErr != nil {
			return BehaviorBranchInstance{}, fmt.Errorf("branch step %d: %w", record.Index, applyErr)
		}
		step := int(record.Index)
		switch record.Action.Kind {
		case core.ActionPartition:
			if template.GoalID == GoalSnapshotCatchUpAfterPartition &&
				partitionStep < 0 && target.Valid() && leader.Valid() &&
				record.Action.Partition != nil &&
				record.Action.Partition.Blocks(core.LinkID{From: leader, To: target}) {
				partitionStep = step
				dimensions.PartitionTopologyClass = "single-target-vs-quorum"
				evidence = append(evidence, BranchEvidence{
					StepIndex: step, Kind: "partition", Category: dimensions.PartitionTopologyClass,
				})
			}
			if template.GoalID == GoalRestartHigherTermMessage &&
				targetCrashStep >= 0 && termCause == "" {
				termCause, termEvidenceStep = "partition-induced-election", step
			}
		case core.ActionHeal:
			if justHealed && healStep < 0 {
				healStep = step
				for _, queue := range tracker.queues {
					for _, queued := range queue {
						if queued.message.From == leader && queued.message.To == target &&
							queued.message.TypeHint == "MsgApp" && queued.delayed {
							delayedAppendSeen = true
							lagEvidenceStep = step
						}
					}
				}
				evidence = append(evidence, BranchEvidence{StepIndex: step, Kind: "heal"})
			}
		case core.ActionRestart:
			if record.Action.Node == target && restartStep < 0 {
				restartStep = step
				evidence = append(evidence, BranchEvidence{StepIndex: step, Kind: "restart"})
			}
		case core.ActionAdvanceTime:
			if template.GoalID == GoalSnapshotCatchUpAfterPartition && snapshotDropped {
				evidence = append(evidence, BranchEvidence{
					StepIndex: step, Kind: "snapshot-retry-control", Category: "logical-time",
				})
			}
			if template.GoalID == GoalRestartHigherTermMessage &&
				restartStep >= 0 && step > restartStep && keyMessage == "" {
				tickAfterRestart = true
				evidence = append(evidence, BranchEvidence{
					StepIndex: step, Kind: "pre-delivery-tick", Category: "logical-time",
				})
			}
		case core.ActionRequest:
			if template.GoalID == GoalRestartHigherTermMessage &&
				restartStep >= 0 && step > restartStep && keyMessage == "" {
				requestAfterRestart = true
				evidence = append(evidence, BranchEvidence{
					StepIndex: step, Kind: "pre-delivery-request", Category: "client-request",
				})
			}
		case core.ActionTimeout:
			if template.GoalID == GoalRestartHigherTermMessage &&
				targetCrashStep >= 0 && restartStep < 0 &&
				termCause == "" {
				termCause, termEvidenceStep = "timeout-induced-term-advance", step
			}
			if template.GoalID == GoalRestartHigherTermMessage &&
				targetCrashStep >= 0 && restartStep < 0 {
				evidence = append(evidence, BranchEvidence{
					StepIndex: step, Kind: "active-election-control", Category: "timeout",
				})
			}
		case core.ActionCrash:
			if record.Action.Node == target && targetCrashStep < 0 {
				targetCrashStep = step
			} else if template.GoalID == GoalRestartHigherTermMessage &&
				targetCrashStep >= 0 && restartStep < 0 &&
				record.Action.Node == leader && termCause == "" {
				termCause, termEvidenceStep = "leader-crash-induced-election", step
			}
		}
		if message != nil {
			isLeaderTargetAppend := message.From == leader && message.To == target &&
				message.TypeHint == "MsgApp"
			isTargetLeaderResponse := message.From == target && message.To == leader &&
				message.TypeHint == "MsgAppResp"
			if record.Action.Kind == core.ActionDrop && isLeaderTargetAppend {
				droppedAppend, lagEvidenceStep = true, step
				evidence = append(evidence, BranchEvidence{StepIndex: step, Kind: "drop", Category: "MsgApp"})
			}
			if record.Action.Kind == core.ActionDrop && isTargetLeaderResponse {
				suppressedResponse, lagEvidenceStep = true, step
				evidence = append(evidence, BranchEvidence{StepIndex: step, Kind: "drop", Category: "MsgAppResp"})
			}
			if template.GoalID == GoalRestartHigherTermMessage &&
				targetCrashStep < 0 && record.Action.Kind == core.ActionDeliver &&
				message.From != target && message.To != target &&
				(message.From == leader || message.To == leader) {
				evidence = append(evidence, BranchEvidence{
					StepIndex: step, Kind: "pre-crash-election-preparation",
					Category: branchMessageClass(message.TypeHint),
				})
			}
			if template.GoalID == GoalRestartHigherTermMessage &&
				targetCrashStep >= 0 && keyMessage == "" &&
				record.Action.Kind == core.ActionDrop && message.To == target &&
				protocolTermMessage(message.TypeHint) {
				evidence = append(evidence, BranchEvidence{
					StepIndex: step, Kind: "pre-restart-message-filter",
					Category: branchMessageClass(message.TypeHint),
				})
			}
			if template.GoalID == GoalRestartHigherTermMessage &&
				targetCrashStep >= 0 && restartStep < 0 &&
				record.Action.Kind == core.ActionDeliver &&
				message.To != target &&
				(message.TypeHint == "MsgVote" || message.TypeHint == "MsgVoteResp") {
				evidence = append(evidence, BranchEvidence{
					StepIndex: step, Kind: "active-election-progress",
					Category: branchMessageClass(message.TypeHint),
				})
			}
			if template.GoalID == GoalSnapshotCatchUpAfterPartition &&
				partitionStep >= 0 && record.Action.Kind == core.ActionDeliver &&
				isTargetLeaderResponse {
				evidence = append(evidence, BranchEvidence{
					StepIndex: step, Kind: "snapshot-trigger-probe", Category: "MsgAppResp",
				})
			}
			if queuedBefore.delayed && isLeaderTargetAppend {
				delayedAppendSeen = true
				if lagEvidenceStep < 0 {
					lagEvidenceStep = step
				}
				evidence = append(evidence, BranchEvidence{
					StepIndex: step, Kind: string(record.Action.Kind),
					Category: "delayed-MsgApp", Relation: snapshotOrder(step, firstSnapshotStep),
				})
			}
			if record.Action.Kind == core.ActionDrop && message.To == target &&
				message.TypeHint == "MsgSnap" {
				snapshotDropped = true
				evidence = append(evidence, BranchEvidence{
					StepIndex: step, Kind: "snapshot-failure", Category: "MsgSnap",
				})
			}
			if template.GoalID == GoalSnapshotCatchUpAfterPartition &&
				snapshotDropped && record.Action.Kind == core.ActionDeliver &&
				((message.From == leader && message.To == target) ||
					(message.From == target && message.To == leader)) {
				evidence = append(evidence, BranchEvidence{
					StepIndex: step, Kind: "snapshot-retry-control",
					Category: branchMessageClass(message.TypeHint),
				})
			}
			if template.GoalID == GoalRestartHigherTermMessage &&
				restartStep >= 0 && step > restartStep && message.To == target &&
				protocolTermMessage(message.TypeHint) {
				beforeNode, found := findNode(record.NodesBefore, target)
				targetTerm, termOK := semanticUint(beforeNode.Semantic["term"])
				messageTerm, parseErr := strconv.ParseUint(message.Metadata["term"], 10, 64)
				if found && termOK && parseErr == nil {
					relation := termRelation(messageTerm, targetTerm)
					if relation == "same" && keyMessage == "" {
						sameTermBeforeHigher = true
					}
					if relation == "higher" && keyMessage == "" {
						keyMessage = branchMessageClass(message.TypeHint)
						messageEvidenceStep = step
						evidence = append(evidence, BranchEvidence{
							StepIndex: step, Kind: "higher-term-delivery",
							Category: keyMessage, Relation: relation,
						})
					}
				}
			}
		}
		for _, effect := range record.Effects {
			if effect.Kind != core.EffectSendMessage || effect.Message == nil {
				continue
			}
			sent := effect.Message
			if sent.To == target && sent.TypeHint == "MsgSnap" && firstSnapshotStep < 0 {
				firstSnapshotStep = step
				snapshotWhilePartitioned = tracker.partition != nil
				evidence = append(evidence, BranchEvidence{
					StepIndex: step, Kind: "snapshot-enqueued",
					Relation: map[bool]string{true: "before-heal", false: "after-heal"}[snapshotWhilePartitioned],
				})
			}
			if sent.To == target && sent.TypeHint == "MsgSnap" {
				snapshotSendCount++
				if snapshotDropped && snapshotSendCount >= 2 {
					evidence = append(evidence, BranchEvidence{
						StepIndex: step, Kind: "snapshot-retry", Category: "MsgSnap",
					})
				}
			}
			if template.GoalID == GoalRestartHigherTermMessage &&
				restartStep >= 0 && step >= restartStep && sent.To == target &&
				protocolTermMessage(sent.TypeHint) && keyMessage == "" {
				targetNode, found := findNode(record.NodesAfter, target)
				targetTerm, termOK := semanticUint(targetNode.Semantic["term"])
				messageTerm, parseErr := strconv.ParseUint(sent.Metadata["term"], 10, 64)
				if found && termOK && parseErr == nil && messageTerm > targetTerm {
					keyMessage = branchMessageClass(sent.TypeHint)
					messageEvidenceStep = step
					evidence = append(evidence, BranchEvidence{
						StepIndex: step, Kind: "higher-term-pending",
						Category: keyMessage, Relation: "higher",
					})
				}
			}
		}
	}
	if template.GoalID == GoalSnapshotCatchUpAfterPartition {
		switch {
		case droppedAppend:
			dimensions.LagConstructionClass = "drop-append"
		case suppressedResponse:
			dimensions.LagConstructionClass = "response-suppression"
		case delayedAppendSeen:
			dimensions.LagConstructionClass = "delayed-delivery"
		case waypointReachedBy(evaluation, 3, lastTraceStep):
			dimensions.LagConstructionClass = "partition-isolation"
			lagEvidenceStep = evaluation.Instance.WaypointResults[3].FirstReachedStep
		}
		switch {
		case waypointReachedBy(evaluation, 3, lastTraceStep):
			dimensions.FaultDurationClass = "long"
		case waypointReachedBy(evaluation, 2, lastTraceStep):
			dimensions.FaultDurationClass = "medium"
		case partitionStep >= 0 && healStep >= 0:
			dimensions.FaultDurationClass = "short"
		}
		if firstSnapshotStep >= 0 {
			if snapshotWhilePartitioned || healStep < 0 || firstSnapshotStep < healStep {
				dimensions.HealTimingClass = "snapshot-before-heal"
				dimensions.SnapshotRouteClass = "queued-while-partitioned"
			} else {
				dimensions.HealTimingClass = "snapshot-after-heal"
				dimensions.SnapshotRouteClass = "generated-after-heal"
			}
		}
		if snapshotDropped && snapshotSendCount >= 2 {
			dimensions.SnapshotRouteClass = "failure-then-retry"
		}
		if waypointReachedBy(evaluation, 6, lastTraceStep) {
			dimensions.RecoveryRouteClass = "snapshot-catch-up"
		}
	}
	if template.GoalID == GoalRestartHigherTermMessage {
		if termCause != "" {
			dimensions.TermAdvanceClass = termCause
		}
		if keyMessage == "" && len(evaluation.Instance.WaypointResults) > 4 &&
			evaluation.Instance.WaypointResults[4].Reached {
			for _, item := range evaluation.Instance.WaypointResults[4].Evidence {
				if value := item.Details["type"]; value != "" {
					keyMessage = branchMessageClass(value)
					messageEvidenceStep = item.StepIndex
					break
				}
			}
		}
		if keyMessage != "" {
			dimensions.KeyMessageClass = keyMessage
			switch {
			case sameTermBeforeHigher:
				dimensions.PreDeliverySequence = "same-term-before-higher"
			case tickAfterRestart:
				dimensions.PreDeliverySequence = "tick-before-higher"
			case requestAfterRestart:
				dimensions.PreDeliverySequence = "request-before-higher"
			default:
				dimensions.PreDeliverySequence = "immediate"
			}
			dimensions.RecoveryRouteClass = "restart-before-higher-term"
		}
	}
	dimensions = dimensions.Ablated(ablation)
	realized.Dimensions = dimensions
	realized.Evidence = append([]BranchEvidence(nil), evidence...)
	realized.StableKey = dimensions.StableKey(BranchAblationNone)
	realized.MatchedTemplateID = matchRealizedTemplate(template.GoalID, dimensions, ablation)
	requiredKnown, maxStep := comparableDimensionsKnown(
		planned.Dimensions, dimensions, partitionStep, lagEvidenceStep,
		termEvidenceStep, max(healStep, firstSnapshotStep), messageEvidenceStep,
	)
	realized.Decidable = requiredKnown
	if requiredKnown {
		realized.FirstDeterminedStep = maxStep
	}
	deviation := compareBranchDimensions(planned.Dimensions, dimensions, evidence)
	feasibility := BranchFeasible
	switch {
	case targetReachedBy(evaluation, lastTraceStep):
		feasibility = BranchCompleted
	case deviation.Occurred:
		feasibility = BranchViolated
	case !realized.Decidable:
		if (template.GoalID == GoalRestartHigherTermMessage && restartStep >= 0 && keyMessage == "") ||
			(template.GoalID == GoalSnapshotCatchUpAfterPartition &&
				waypointReachedBy(evaluation, 3, lastTraceStep) && firstSnapshotStep < 0) {
			feasibility = BranchCurrentlyInfeasible
		} else {
			feasibility = BranchNotDecidable
		}
	}
	instance := BehaviorBranchInstance{
		SchemaVersion: BranchSchemaVersion, BranchTemplateID: template.BranchTemplateID,
		BranchInstanceID:       evaluation.Instance.InstanceID + ":" + string(template.BranchTemplateID),
		GoalInstanceID:         evaluation.Instance.InstanceID,
		PlannedBranchSignature: planned, RealizedBranchSignature: realized,
		GoalBindings:    cloneBindings(evaluation.Instance.Bindings),
		BranchBindings:  map[string]string{"binding_policy": string(template.BindingPolicy)},
		State:           branchState(targetReachedBy(evaluation, lastTraceStep), evaluation, realized),
		CurrentWaypoint: evaluation.Instance.Progress.CurrentWaypointID,
		Progress:        evaluation.Instance.Progress, Evidence: evidence,
		Deviation: deviation, Feasibility: feasibility,
	}
	copyForKey := instance
	copyForKey.StableKey = ""
	copyForKey.GoalBindings = nil
	copyForKey.BranchInstanceID = ""
	copyForKey.GoalInstanceID = ""
	copyForKey.Evidence = nil
	copyForKey.RealizedBranchSignature.Evidence = nil
	copyForKey.Progress.StableKey = ""
	instance.StableKey = stableHash(copyForKey)
	return instance, nil
}

// BranchPrefixBoundary returns the newest causally evidenced Branch prefix.
// It is used only by the Diversity Frontier; the standard Frontier continues
// cutting prefixes at Goal progress boundaries.
func BranchPrefixBoundary(
	initial core.Observation,
	trace core.Trace,
	resolutions []plan.Resolution,
	instance BehaviorBranchInstance,
) (traceStep, planIndex int, observation core.Observation, ok bool, err error) {
	latest := -1
	for _, evidence := range instance.Evidence {
		latest = max(latest, evidence.StepIndex)
	}
	if latest < 0 || latest >= len(trace.Steps) {
		return -1, -1, core.Observation{}, false, nil
	}
	planIndices := make([]int, 0, len(trace.Steps))
	for index, resolution := range resolutions {
		for range resolution.Actions {
			planIndices = append(planIndices, index)
		}
	}
	if len(planIndices) != len(trace.Steps) {
		return -1, -1, core.Observation{}, false, fmt.Errorf(
			"branch prefix has %d concrete actions but %d plan mappings",
			len(trace.Steps), len(planIndices))
	}
	tracker, trackerErr := newPrefixTracker(initial)
	if trackerErr != nil {
		return -1, -1, core.Observation{}, false, trackerErr
	}
	for index, record := range trace.Steps {
		if _, _, applyErr := tracker.apply(record); applyErr != nil {
			return -1, -1, core.Observation{}, false, applyErr
		}
		if index == latest {
			observed := tracker.observation(record.TimeAfter, record.NodesAfter, &record.Action)
			return latest, planIndices[index], observed, true, nil
		}
	}
	return -1, -1, core.Observation{}, false, nil
}

func snapshotOrder(step, snapshotStep int) string {
	if snapshotStep < 0 {
		return "before-snapshot"
	}
	if step < snapshotStep {
		return "before-snapshot"
	}
	return "after-snapshot"
}

func branchMessageClass(messageType string) string {
	switch messageType {
	case "MsgHeartbeat":
		return "MsgHeartbeat"
	case "MsgApp":
		return "MsgApp"
	case "MsgVote", "MsgVoteResp":
		return "vote-message"
	default:
		return messageType
	}
}

func comparableDimensionsKnown(
	planned, realized BranchDimensions,
	partitionStep, lagStep, termStep, healStep, messageStep int,
) (bool, int) {
	known := true
	maxStep := -1
	check := func(wanted, actual string, step int) {
		if wanted == "" {
			return
		}
		if actual == "" {
			known = false
			return
		}
		if step > maxStep {
			maxStep = step
		}
	}
	check(planned.TargetSelectionClass, realized.TargetSelectionClass, partitionStep)
	check(planned.PartitionTopologyClass, realized.PartitionTopologyClass, partitionStep)
	check(planned.LagConstructionClass, realized.LagConstructionClass, lagStep)
	check(planned.FaultDurationClass, realized.FaultDurationClass, lagStep)
	check(planned.TermAdvanceClass, realized.TermAdvanceClass, termStep)
	check(planned.HealTimingClass, realized.HealTimingClass, healStep)
	check(planned.SnapshotRouteClass, realized.SnapshotRouteClass, healStep)
	check(planned.RecoveryRouteClass, realized.RecoveryRouteClass, max(healStep, messageStep))
	check(planned.KeyMessageClass, realized.KeyMessageClass, messageStep)
	check(planned.PreDeliverySequence, realized.PreDeliverySequence, messageStep)
	return known, maxStep
}

func compareBranchDimensions(
	planned, realized BranchDimensions, evidence []BranchEvidence,
) BranchDeviation {
	type pair struct {
		name          string
		planned, real string
	}
	pairs := []pair{
		{"target-selection", planned.TargetSelectionClass, realized.TargetSelectionClass},
		{"partition-topology", planned.PartitionTopologyClass, realized.PartitionTopologyClass},
		{"lag-construction", planned.LagConstructionClass, realized.LagConstructionClass},
		{"fault-duration", planned.FaultDurationClass, realized.FaultDurationClass},
		{"term-advance", planned.TermAdvanceClass, realized.TermAdvanceClass},
		{"heal-timing", planned.HealTimingClass, realized.HealTimingClass},
		{"snapshot-route", planned.SnapshotRouteClass, realized.SnapshotRouteClass},
		{"recovery-route", planned.RecoveryRouteClass, realized.RecoveryRouteClass},
		{"key-message", planned.KeyMessageClass, realized.KeyMessageClass},
		{"pre-delivery-sequence", planned.PreDeliverySequence, realized.PreDeliverySequence},
	}
	for _, item := range pairs {
		if item.name == "fault-duration" && item.real != "long" {
			// short/medium are causal prefix stages that may still evolve into
			// the planned long duration; they are not early deviations.
			continue
		}
		if item.planned == "" || item.real == "" || item.planned == item.real {
			continue
		}
		step := -1
		var causal []BranchEvidence
		for _, evidenceItem := range evidence {
			if evidenceItem.StepIndex >= step {
				step = evidenceItem.StepIndex
				causal = []BranchEvidence{evidenceItem}
			}
		}
		return BranchDeviation{
			Occurred: true, Waypoint: branchDimensionWaypoint(item.name),
			StepIndex: step,
			Reason:    fmt.Sprintf("%s planned=%s realized=%s", item.name, item.planned, item.real),
			Evidence:  causal,
		}
	}
	return BranchDeviation{}
}

func branchDimensionWaypoint(dimension string) string {
	switch dimension {
	case "target-selection":
		return "W1"
	case "partition-topology":
		return "W2"
	case "lag-construction", "fault-duration":
		return "W3"
	case "heal-timing", "snapshot-route":
		return "W5"
	case "term-advance":
		return "W3"
	case "recovery-route":
		return "W4"
	case "key-message":
		return "W5"
	case "pre-delivery-sequence":
		return "W6"
	default:
		return ""
	}
}

func matchRealizedTemplate(
	goal GoalID, dimensions BranchDimensions, ablation BranchDimensionAblation,
) BranchTemplateID {
	var best BehaviorBranchTemplate
	bestFields := -1
	for _, template := range BranchTemplates(goal) {
		planned := template.PlannedDimensions.Ablated(ablation)
		if !dimensionsCompatible(planned, dimensions) {
			continue
		}
		fields := populatedDimensionCount(planned)
		if fields > bestFields ||
			(fields == bestFields && template.BranchTemplateID < best.BranchTemplateID) {
			best, bestFields = template, fields
		}
	}
	return best.BranchTemplateID
}

func dimensionsCompatible(wanted, actual BranchDimensions) bool {
	checks := [][2]string{
		{wanted.TargetSelectionClass, actual.TargetSelectionClass},
		{wanted.PartitionTopologyClass, actual.PartitionTopologyClass},
		{wanted.LagConstructionClass, actual.LagConstructionClass},
		{wanted.FaultDurationClass, actual.FaultDurationClass},
		{wanted.TermAdvanceClass, actual.TermAdvanceClass},
		{wanted.HealTimingClass, actual.HealTimingClass},
		{wanted.SnapshotRouteClass, actual.SnapshotRouteClass},
		{wanted.RecoveryRouteClass, actual.RecoveryRouteClass},
		{wanted.KeyMessageClass, actual.KeyMessageClass},
		{wanted.PreDeliverySequence, actual.PreDeliverySequence},
	}
	for _, values := range checks {
		if values[0] != "" && values[1] != "" && values[0] != values[1] {
			return false
		}
	}
	return true
}

func populatedDimensionCount(d BranchDimensions) int {
	count := 0
	for _, value := range []string{
		d.TargetSelectionClass, d.PartitionTopologyClass, d.LagConstructionClass,
		d.FaultDurationClass, d.TermAdvanceClass, d.HealTimingClass,
		d.SnapshotRouteClass, d.RecoveryRouteClass, d.KeyMessageClass,
		d.PreDeliverySequence,
	} {
		if value != "" {
			count++
		}
	}
	return count
}

func branchState(
	targetReached bool, evaluation EvaluationResult, realized RealizedBranchSignature,
) string {
	switch {
	case targetReached:
		return "completed"
	case realized.Decidable:
		return "realized"
	case evaluation.Instance.Progress.CompletedWaypointCount > 0:
		return "forming"
	default:
		return "planned"
	}
}

func waypointReachedBy(evaluation EvaluationResult, index, lastStep int) bool {
	return index >= 0 && index < len(evaluation.Instance.WaypointResults) &&
		evaluation.Instance.WaypointResults[index].Reached &&
		evaluation.Instance.WaypointResults[index].FirstReachedStep >= -1 &&
		evaluation.Instance.WaypointResults[index].FirstReachedStep <= lastStep
}

func targetReachedBy(evaluation EvaluationResult, lastStep int) bool {
	return evaluation.TargetReached && evaluation.TargetReachedStep >= -1 &&
		evaluation.TargetReachedStep <= lastStep
}
