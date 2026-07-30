// Frozen experimental surface: Branch Evidence, Commitment, and micro-progress
// schemas are retained for diagnostics and historical artifact compatibility.
package goalsearch

import (
	"fmt"
	"sort"
	"strings"

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/core"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/plan"
)

const (
	BranchEvidenceSchemaVersion   = "raft-branch-evidence-v1-prototype"
	FormationFailureSchemaVersion = "raft-branch-formation-failure-v1-prototype"
)

type EvidenceStatus string
type EvidenceLevel string
type EvidenceSource string
type BranchEvidenceMode string
type MicroProgressPolicy string
type MicroProgressClass string

const (
	EvidenceUnknown       EvidenceStatus = "unknown"
	EvidenceSupported     EvidenceStatus = "supported"
	EvidenceCommitted     EvidenceStatus = "committed"
	EvidenceContradicted  EvidenceStatus = "contradicted"
	EvidenceInvalidated   EvidenceStatus = "invalidated"
	EvidenceNotApplicable EvidenceStatus = "not_applicable"
)

const (
	EvidenceLevelPlanned           EvidenceLevel = "planned"
	EvidenceLevelSupported         EvidenceLevel = "supported"
	EvidenceLevelCommitted         EvidenceLevel = "committed"
	EvidenceLevelRealizedDecidable EvidenceLevel = "realized-decidable"
	EvidenceLevelFullRealized      EvidenceLevel = "full-realized"
	EvidenceLevelContradicted      EvidenceLevel = "contradicted"
)

const (
	EvidenceSourceAction      EvidenceSource = "action"
	EvidenceSourceEffect      EvidenceSource = "effect"
	EvidenceSourceModelEvent  EvidenceSource = "model-event"
	EvidenceSourceObservation EvidenceSource = "observation"
	EvidenceSourceGoal        EvidenceSource = "goal-waypoint"
	EvidenceSourceBranch      EvidenceSource = "branch-prefix"
)

const (
	BranchEvidenceOff        BranchEvidenceMode = "off"
	BranchEvidencePartial    BranchEvidenceMode = "partial"
	BranchEvidenceCommitment BranchEvidenceMode = "commitment"
)

const (
	MicroProgressLegacy        MicroProgressPolicy = "legacy"
	MicroProgressNecessaryOnly MicroProgressPolicy = "necessary-only"
	MicroProgressOff           MicroProgressPolicy = "off"
)

func (mode BranchEvidenceMode) Validate() error {
	switch mode {
	case BranchEvidenceOff, BranchEvidencePartial, BranchEvidenceCommitment:
		return nil
	default:
		return fmt.Errorf("unknown branch evidence mode %q", mode)
	}
}

const (
	MicroNecessary  MicroProgressClass = "necessary-causal-preparation"
	MicroUseful     MicroProgressClass = "useful-optional-diversity"
	MicroIncidental MicroProgressClass = "incidental-event"
	MicroNoisy      MicroProgressClass = "harmful-or-noisy"
)

func (status EvidenceStatus) Validate() error {
	switch status {
	case EvidenceUnknown, EvidenceSupported, EvidenceCommitted, EvidenceContradicted,
		EvidenceInvalidated, EvidenceNotApplicable:
		return nil
	default:
		return fmt.Errorf("unknown branch evidence status %q", status)
	}
}

func (policy MicroProgressPolicy) Validate() error {
	switch policy {
	case MicroProgressLegacy, MicroProgressNecessaryOnly, MicroProgressOff:
		return nil
	default:
		return fmt.Errorf("unknown micro-progress policy %q", policy)
	}
}

func EvidenceLevelRank(level EvidenceLevel) int {
	switch level {
	case EvidenceLevelPlanned:
		return 0
	case EvidenceLevelSupported:
		return 1
	case EvidenceLevelCommitted:
		return 2
	case EvidenceLevelRealizedDecidable:
		return 3
	case EvidenceLevelFullRealized:
		return 4
	case EvidenceLevelContradicted:
		return -1
	default:
		return -2
	}
}

// BranchEvidenceDefinition is static and contains no concrete node, term,
// index, MessageID, seed, or final-run result.
type BranchEvidenceDefinition struct {
	SchemaVersion              string           `json:"schema_version"`
	EvidenceID                 string           `json:"evidence_id"`
	BranchTemplateID           BranchTemplateID `json:"branch_template_id"`
	Description                string           `json:"description"`
	Source                     EvidenceSource   `json:"source"`
	RequiredForCommitment      bool             `json:"required_for_commitment"`
	RequiredForFullRealization bool             `json:"required_for_full_realization"`
	ContradictionRule          string           `json:"contradiction_rule,omitempty"`
	StableKey                  string           `json:"stable_key"`
}

type BranchEvidenceDimension struct {
	EvidenceID                 string           `json:"evidence_id"`
	Description                string           `json:"description"`
	Source                     EvidenceSource   `json:"source"`
	Status                     EvidenceStatus   `json:"status"`
	FirstObservedStep          int              `json:"first_observed_step"`
	LastObservedStep           int              `json:"last_observed_step"`
	SupportingAction           *core.Action     `json:"supporting_action,omitempty"`
	SupportingMessageIDs       []core.MessageID `json:"supporting_message_ids,omitempty"`
	SupportingModelEvents      []string         `json:"supporting_model_events,omitempty"`
	SupportingObservation      string           `json:"supporting_observation,omitempty"`
	RequiredForCommitment      bool             `json:"required_for_commitment"`
	RequiredForFullRealization bool             `json:"required_for_full_realization"`
	ContradictionRule          string           `json:"contradiction_rule,omitempty"`
	StableKey                  string           `json:"stable_key"`
}

type BranchCommitment struct {
	Reached      bool             `json:"reached"`
	CommitmentID string           `json:"commitment_id,omitempty"`
	BranchID     BranchTemplateID `json:"branch_template_id,omitempty"`
	FirstStep    int              `json:"first_step"`
	EvidenceIDs  []string         `json:"evidence_ids,omitempty"`
	StableKey    string           `json:"stable_key,omitempty"`
}

type BranchEvidenceVector struct {
	SchemaVersion        string                    `json:"schema_version"`
	GoalID               GoalID                    `json:"goal_id"`
	BranchTemplateID     BranchTemplateID          `json:"branch_template_id"`
	BranchInstanceID     string                    `json:"branch_instance_id"`
	Dimensions           []BranchEvidenceDimension `json:"dimensions"`
	HighestLevel         EvidenceLevel             `json:"highest_evidence_level"`
	SupportedCount       int                       `json:"supported_evidence_count"`
	NecessaryCount       int                       `json:"necessary_evidence_count"`
	Commitment           BranchCommitment          `json:"commitment"`
	RealizedDecidable    bool                      `json:"realized_decidable"`
	FullRealized         bool                      `json:"full_realized"`
	Contradicted         bool                      `json:"contradicted"`
	InvalidationCount    int                       `json:"invalidation_count"`
	NextEventGeneratable bool                      `json:"next_key_event_generatable"`
	PrefixProtectionStep int                       `json:"prefix_protection_step"`
	StableKey            string                    `json:"stable_key"`
}

func BranchEvidenceCatalog() []BranchEvidenceDefinition {
	var result []BranchEvidenceDefinition
	for _, template := range BranchCatalog() {
		result = append(result, EvidenceDefinitions(template)...)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].EvidenceID < result[j].EvidenceID
	})
	return result
}

func EvidenceDefinitions(template BehaviorBranchTemplate) []BranchEvidenceDefinition {
	add := func(id, description string, source EvidenceSource, commitment, full bool, contradiction string) BranchEvidenceDefinition {
		definition := BranchEvidenceDefinition{
			SchemaVersion:              BranchEvidenceSchemaVersion,
			EvidenceID:                 string(template.BranchTemplateID) + "." + id,
			BranchTemplateID:           template.BranchTemplateID,
			Description:                description,
			Source:                     source,
			RequiredForCommitment:      commitment,
			RequiredForFullRealization: full,
			ContradictionRule:          contradiction,
		}
		copyForKey := definition
		copyForKey.StableKey = ""
		definition.StableKey = stableHash(copyForKey)
		return definition
	}
	switch template.BranchTemplateID {
	case BranchADropAppend:
		return []BranchEvidenceDefinition{
			add("target-partitioned", "目标节点已被真实隔离", EvidenceSourceGoal, true, true, ""),
			add("target-msgapp-dropped", "Leader 到目标的 MsgApp 已被真实 Drop", EvidenceSourceAction, true, true, "lag construction must be drop-append"),
			add("drop-caused-lag-stage", "Drop 后目标 lag 进入新的语义阶段", EvidenceSourceObservation, true, true, "lag must not be caused by another route"),
		}
	case BranchADelayedDelivery:
		return []BranchEvidenceDefinition{
			add("target-partitioned", "目标节点已被真实隔离", EvidenceSourceGoal, true, true, ""),
			add("target-msgapp-retained", "目标 MsgApp 被保留而不是永久删除", EvidenceSourceBranch, true, true, "lag construction must be delayed-delivery"),
			add("msgapp-crossed-heal", "旧 MsgApp 在 Heal 后仍可观察", EvidenceSourceBranch, true, true, "old MsgApp must survive across Heal"),
		}
	case BranchASnapshotAfterHeal:
		return []BranchEvidenceDefinition{
			add("target-partitioned", "目标节点已被真实隔离", EvidenceSourceGoal, true, true, ""),
			add("heal-without-pending-snapshot", "Heal 时尚未生成发往目标的 MsgSnap", EvidenceSourceBranch, true, true, "MsgSnap must not predate Heal"),
			add("snapshot-generated-after-heal", "MsgSnap 首次在 Heal 后真实生成", EvidenceSourceEffect, true, true, "snapshot route must be generated-after-heal"),
		}
	case BranchASnapshotFailureRetry:
		return []BranchEvidenceDefinition{
			add("first-snapshot-failed", "第一次 MsgSnap 已真实失败或被受控 Drop", EvidenceSourceAction, true, true, ""),
			add("snapshot-retry-established", "后续协议行为已真实生成 retry MsgSnap", EvidenceSourceEffect, true, true, "retry must follow a real first failure"),
		}
	case BranchASnapshotBeforeHeal:
		return []BranchEvidenceDefinition{
			add("snapshot-generated-before-heal", "MsgSnap 在分区仍存在时生成", EvidenceSourceEffect, true, true, "snapshot must predate Heal"),
		}
	case BranchBHigherHeartbeat, BranchBHigherApp, BranchBHigherVote:
		class := template.PlannedDimensions.KeyMessageClass
		return []BranchEvidenceDefinition{
			add("target-crashed", "目标 Follower 已真实 Crash", EvidenceSourceGoal, true, true, ""),
			add("active-election-started", "目标离线期间 active 节点已真实开始并推进选举", EvidenceSourceBranch, true, true, ""),
			add("active-term-advanced", "目标离线期间 active 集群 term 已真实推进", EvidenceSourceGoal, true, true, ""),
			add("target-restarted-incomplete", "目标已 Restart 且恢复尚未完成", EvidenceSourceGoal, true, true, ""),
			add("higher-term-message-pending", "预期类别的 higher-term 消息已进入目标队列", EvidenceSourceGoal, true, true, "key message must be "+class),
			add("higher-term-message-delivered", "预期 higher-term 消息已被真实投递处理", EvidenceSourceGoal, false, true, "delivered message must be "+class),
		}
	default:
		return nil
	}
}

type evidenceObservation struct {
	supported bool
	committed bool
	first     int
	last      int
}

// AnalyzeBranchEvidence uses only the already evaluated concrete prefix. It
// never reads another candidate, a future Trace suffix, final campaign success,
// or bug status.
func AnalyzeBranchEvidence(
	template BehaviorBranchTemplate,
	instance BehaviorBranchInstance,
	evaluation EvaluationResult,
	trace core.Trace,
) (BranchEvidenceVector, error) {
	if template.BranchTemplateID != instance.BranchTemplateID {
		return BranchEvidenceVector{}, fmt.Errorf("evidence template %q differs from instance %q",
			template.BranchTemplateID, instance.BranchTemplateID)
	}
	definitions := EvidenceDefinitions(template)
	vector := BranchEvidenceVector{
		SchemaVersion:        BranchEvidenceSchemaVersion,
		GoalID:               template.GoalID,
		BranchTemplateID:     template.BranchTemplateID,
		BranchInstanceID:     instance.BranchInstanceID,
		HighestLevel:         EvidenceLevelPlanned,
		PrefixProtectionStep: -1,
		Commitment:           BranchCommitment{FirstStep: -1},
		RealizedDecidable:    instance.RealizedBranchSignature.Decidable,
		FullRealized: instance.RealizedBranchSignature.Decidable &&
			instance.RealizedBranchSignature.MatchedTemplateID != "",
		Contradicted: instance.Deviation.Occurred,
	}
	for _, definition := range definitions {
		observation := recognizeEvidence(definition, template, instance, evaluation)
		dimension := BranchEvidenceDimension{
			EvidenceID:                 definition.EvidenceID,
			Description:                definition.Description,
			Source:                     definition.Source,
			Status:                     EvidenceUnknown,
			FirstObservedStep:          -1,
			LastObservedStep:           -1,
			RequiredForCommitment:      definition.RequiredForCommitment,
			RequiredForFullRealization: definition.RequiredForFullRealization,
			ContradictionRule:          definition.ContradictionRule,
		}
		if observation.supported {
			dimension.Status = EvidenceSupported
			dimension.FirstObservedStep = observation.first
			dimension.LastObservedStep = observation.last
			if observation.committed {
				dimension.Status = EvidenceCommitted
			}
			attachEvidenceSupport(&dimension, evaluation, trace)
			vector.SupportedCount++
			if definition.RequiredForCommitment {
				vector.NecessaryCount++
			}
		}
		if contradictedEvidence(definition, template, instance) {
			dimension.Status = EvidenceContradicted
			if dimension.FirstObservedStep < 0 {
				dimension.FirstObservedStep = instance.Deviation.StepIndex
				dimension.LastObservedStep = instance.Deviation.StepIndex
			}
		}
		copyForKey := dimension
		copyForKey.FirstObservedStep = -1
		copyForKey.LastObservedStep = -1
		copyForKey.SupportingAction = nil
		copyForKey.SupportingMessageIDs = nil
		copyForKey.SupportingModelEvents = nil
		copyForKey.SupportingObservation = ""
		copyForKey.StableKey = ""
		dimension.StableKey = stableHash(copyForKey)
		vector.Dimensions = append(vector.Dimensions, dimension)
	}
	sort.Slice(vector.Dimensions, func(i, j int) bool {
		return vector.Dimensions[i].EvidenceID < vector.Dimensions[j].EvidenceID
	})
	required, satisfied := 0, 0
	var commitmentIDs []string
	commitmentStep := -1
	for _, dimension := range vector.Dimensions {
		if !dimension.RequiredForCommitment {
			continue
		}
		required++
		if dimension.Status == EvidenceSupported || dimension.Status == EvidenceCommitted {
			satisfied++
			commitmentIDs = append(commitmentIDs, dimension.EvidenceID)
			commitmentStep = max(commitmentStep, dimension.FirstObservedStep)
		}
	}
	if required > 0 && satisfied == required && !vector.Contradicted {
		vector.Commitment = BranchCommitment{
			Reached:      true,
			CommitmentID: string(template.BranchTemplateID) + ".commitment",
			BranchID:     template.BranchTemplateID,
			FirstStep:    commitmentStep,
			EvidenceIDs:  commitmentIDs,
		}
		vector.Commitment.StableKey = stableHash(struct {
			Branch BranchTemplateID
			IDs    []string
		}{template.BranchTemplateID, commitmentIDs})
	}
	switch {
	case vector.Contradicted:
		vector.HighestLevel = EvidenceLevelContradicted
	case vector.FullRealized:
		vector.HighestLevel = EvidenceLevelFullRealized
	case vector.RealizedDecidable:
		vector.HighestLevel = EvidenceLevelRealizedDecidable
	case vector.Commitment.Reached:
		vector.HighestLevel = EvidenceLevelCommitted
	case vector.SupportedCount > 0:
		vector.HighestLevel = EvidenceLevelSupported
	}
	vector.NextEventGeneratable = commitmentMakesNextEventGeneratable(template, vector, evaluation)
	vector.PrefixProtectionStep = necessaryEvidenceStep(vector)
	copyForKey := vector
	// Dimensions is a slice, so a plain struct copy would still share its
	// backing array with vector. Stable-key normalization must never erase the
	// concrete step/action/message support kept in the returned vector.
	copyForKey.Dimensions = append([]BranchEvidenceDimension(nil), vector.Dimensions...)
	copyForKey.BranchInstanceID = ""
	copyForKey.StableKey = ""
	for index := range copyForKey.Dimensions {
		copyForKey.Dimensions[index].FirstObservedStep = -1
		copyForKey.Dimensions[index].LastObservedStep = -1
		copyForKey.Dimensions[index].SupportingAction = nil
		copyForKey.Dimensions[index].SupportingMessageIDs = nil
		copyForKey.Dimensions[index].SupportingModelEvents = nil
		copyForKey.Dimensions[index].SupportingObservation = ""
	}
	copyForKey.Commitment.FirstStep = -1
	copyForKey.PrefixProtectionStep = -1
	vector.StableKey = stableHash(copyForKey)
	return vector, nil
}

func recognizeEvidence(
	definition BranchEvidenceDefinition,
	template BehaviorBranchTemplate,
	instance BehaviorBranchInstance,
	evaluation EvaluationResult,
) evidenceObservation {
	suffix := strings.TrimPrefix(definition.EvidenceID, string(template.BranchTemplateID)+".")
	goalStep := func(index int) (bool, int) {
		if index < 0 || index >= len(evaluation.Instance.WaypointResults) {
			return false, -1
		}
		result := evaluation.Instance.WaypointResults[index]
		return result.Reached, result.FirstReachedStep
	}
	branch := func(kind, category, relation string) evidenceObservation {
		first, last := -1, -1
		for _, evidence := range instance.Evidence {
			if evidence.Kind != kind ||
				(category != "" && evidence.Category != category) ||
				(relation != "" && evidence.Relation != relation) {
				continue
			}
			if first < 0 || evidence.StepIndex < first {
				first = evidence.StepIndex
			}
			last = max(last, evidence.StepIndex)
		}
		return evidenceObservation{supported: first >= 0, first: first, last: last}
	}
	supportedGoal := func(index int) evidenceObservation {
		reached, step := goalStep(index)
		return evidenceObservation{supported: reached, first: step, last: step}
	}
	switch suffix {
	case "target-partitioned":
		return supportedGoal(1)
	case "target-msgapp-dropped":
		return branch("drop", "MsgApp", "")
	case "drop-caused-lag-stage":
		value := supportedGoal(2)
		value.supported = value.supported &&
			instance.RealizedBranchSignature.Dimensions.LagConstructionClass == "drop-append"
		value.committed = value.supported
		return value
	case "target-msgapp-retained":
		value := branch("deliver", "delayed-MsgApp", "")
		if !value.supported {
			value = branch("drop", "delayed-MsgApp", "")
		}
		return value
	case "msgapp-crossed-heal":
		value := branch("heal", "", "")
		value.supported = value.supported &&
			instance.RealizedBranchSignature.Dimensions.LagConstructionClass == "delayed-delivery"
		value.committed = value.supported
		return value
	case "heal-without-pending-snapshot":
		heal := branch("heal", "", "")
		before := branch("snapshot-enqueued", "", "before-heal")
		heal.supported = heal.supported && !before.supported
		return heal
	case "snapshot-generated-after-heal":
		value := branch("snapshot-enqueued", "", "after-heal")
		value.committed = value.supported
		return value
	case "first-snapshot-failed":
		return branch("snapshot-failure", "MsgSnap", "")
	case "snapshot-retry-established":
		value := branch("snapshot-retry", "MsgSnap", "")
		value.committed = value.supported
		return value
	case "snapshot-generated-before-heal":
		value := branch("snapshot-enqueued", "", "before-heal")
		value.committed = value.supported
		return value
	case "target-crashed":
		return supportedGoal(1)
	case "active-term-advanced":
		return supportedGoal(2)
	case "active-election-started":
		value := branch("active-election-progress", "", "")
		if !value.supported {
			value = branch("active-election-control", "", "")
		}
		return value
	case "target-restarted-incomplete":
		return supportedGoal(3)
	case "higher-term-message-pending":
		value := supportedGoal(4)
		value.supported = value.supported &&
			instance.RealizedBranchSignature.Dimensions.KeyMessageClass ==
				template.PlannedDimensions.KeyMessageClass
		value.committed = value.supported
		return value
	case "higher-term-message-delivered":
		value := supportedGoal(5)
		value.supported = value.supported &&
			instance.RealizedBranchSignature.Dimensions.KeyMessageClass ==
				template.PlannedDimensions.KeyMessageClass
		return value
	default:
		return evidenceObservation{first: -1, last: -1}
	}
}

func contradictedEvidence(
	definition BranchEvidenceDefinition,
	template BehaviorBranchTemplate,
	instance BehaviorBranchInstance,
) bool {
	if !instance.Deviation.Occurred || definition.ContradictionRule == "" {
		return false
	}
	suffix := strings.TrimPrefix(definition.EvidenceID, string(template.BranchTemplateID)+".")
	switch {
	case strings.Contains(instance.Deviation.Reason, "key-message"):
		return suffix == "higher-term-message-pending" ||
			suffix == "higher-term-message-delivered"
	case strings.Contains(instance.Deviation.Reason, "lag-construction"):
		return strings.Contains(suffix, "msgapp") || strings.Contains(suffix, "lag")
	case strings.Contains(instance.Deviation.Reason, "heal-timing") ||
		strings.Contains(instance.Deviation.Reason, "snapshot-route"):
		return strings.Contains(suffix, "heal") || strings.Contains(suffix, "snapshot")
	default:
		return false
	}
}

func attachEvidenceSupport(
	dimension *BranchEvidenceDimension,
	evaluation EvaluationResult,
	trace core.Trace,
) {
	if dimension.FirstObservedStep >= 0 && dimension.FirstObservedStep < len(trace.Steps) {
		record := trace.Steps[dimension.FirstObservedStep]
		action := record.Action
		dimension.SupportingAction = &action
		dimension.SupportingObservation = record.ObservationDigest
	}
	for _, waypoint := range evaluation.Instance.WaypointResults {
		for _, evidence := range waypoint.Evidence {
			if evidence.StepIndex < dimension.FirstObservedStep ||
				evidence.StepIndex > dimension.LastObservedStep {
				continue
			}
			dimension.SupportingMessageIDs = append(
				dimension.SupportingMessageIDs, evidence.MessageIDs...,
			)
			if evidence.ModelEvent != "" {
				dimension.SupportingModelEvents = append(
					dimension.SupportingModelEvents, evidence.ModelEvent,
				)
			}
		}
	}
	sort.Slice(dimension.SupportingMessageIDs, func(i, j int) bool {
		return dimension.SupportingMessageIDs[i] < dimension.SupportingMessageIDs[j]
	})
	sort.Strings(dimension.SupportingModelEvents)
}

func necessaryEvidenceStep(vector BranchEvidenceVector) int {
	latest := -1
	for _, dimension := range vector.Dimensions {
		if !dimension.RequiredForCommitment ||
			(dimension.Status != EvidenceSupported && dimension.Status != EvidenceCommitted) {
			continue
		}
		latest = max(latest, dimension.LastObservedStep)
	}
	return latest
}

func commitmentMakesNextEventGeneratable(
	template BehaviorBranchTemplate,
	vector BranchEvidenceVector,
	evaluation EvaluationResult,
) bool {
	if !vector.Commitment.Reached {
		return false
	}
	switch template.GoalID {
	case GoalRestartHigherTermMessage:
		return len(evaluation.Instance.WaypointResults) > 4 &&
			evaluation.Instance.WaypointResults[4].Reached
	case GoalSnapshotCatchUpAfterPartition:
		return vector.Commitment.FirstStep >= 0
	default:
		return false
	}
}

// EvidencePrefixBoundary protects only registered necessary causal evidence.
// Historical BranchPrefixBoundary remains unchanged for legacy experiments.
func EvidencePrefixBoundary(
	initial core.Observation,
	trace core.Trace,
	resolutions []plan.Resolution,
	vector BranchEvidenceVector,
	policy MicroProgressPolicy,
) (traceStep, planIndex int, observation core.Observation, ok bool, err error) {
	if err := policy.Validate(); err != nil {
		return -1, -1, core.Observation{}, false, err
	}
	if policy == MicroProgressOff {
		return -1, -1, core.Observation{}, false, nil
	}
	latest := vector.PrefixProtectionStep
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
			"evidence prefix has %d concrete actions but %d plan mappings",
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

type MicroProgressDefinition struct {
	SchemaVersion                string             `json:"schema_version"`
	MicroProgressID              string             `json:"micro_progress_id"`
	BranchTemplateID             BranchTemplateID   `json:"branch_template_id"`
	BranchEvidenceKind           string             `json:"branch_evidence_kind"`
	Class                        MicroProgressClass `json:"class"`
	Necessary                    bool               `json:"necessary"`
	Reversible                   bool               `json:"reversible"`
	IncreasesNextEventGeneration bool               `json:"increases_next_event_generation"`
	MayExtendPrefix              bool               `json:"may_extend_prefix"`
	MayImproveFrontierOrdering   bool               `json:"may_improve_frontier_ordering"`
	SupportingSuccessfulTraces   []string           `json:"supporting_successful_traces,omitempty"`
	NegativeExamples             []string           `json:"negative_examples,omitempty"`
	StableKey                    string             `json:"stable_key"`
}

func MicroProgressRegistry() []MicroProgressDefinition {
	classify := func(template BranchTemplateID, kind string, class MicroProgressClass, reversible, generates bool) MicroProgressDefinition {
		definition := MicroProgressDefinition{
			SchemaVersion:                BranchEvidenceSchemaVersion,
			MicroProgressID:              string(template) + "." + kind,
			BranchTemplateID:             template,
			BranchEvidenceKind:           kind,
			Class:                        class,
			Necessary:                    class == MicroNecessary,
			Reversible:                   reversible,
			IncreasesNextEventGeneration: generates,
			MayExtendPrefix:              class == MicroNecessary,
			MayImproveFrontierOrdering:   class == MicroNecessary || class == MicroUseful,
		}
		if class == MicroIncidental || class == MicroNoisy {
			definition.NegativeExamples = []string{"事件发生但没有改变下一阶段可达条件"}
		}
		copyForKey := definition
		copyForKey.StableKey = ""
		definition.StableKey = stableHash(copyForKey)
		return definition
	}
	var result []MicroProgressDefinition
	for _, template := range BranchCatalog() {
		id := template.BranchTemplateID
		switch id {
		case BranchADropAppend:
			result = append(result,
				classify(id, "partition", MicroNecessary, false, true),
				classify(id, "drop:MsgApp", MicroNecessary, false, true),
				classify(id, "snapshot-trigger-probe", MicroUseful, true, true))
		case BranchADelayedDelivery:
			result = append(result,
				classify(id, "partition", MicroNecessary, false, true),
				classify(id, "heal", MicroNecessary, false, true),
				classify(id, "delayed-MsgApp", MicroNecessary, true, true))
		case BranchASnapshotAfterHeal:
			result = append(result,
				classify(id, "heal", MicroNecessary, false, true),
				classify(id, "snapshot-enqueued:after-heal", MicroNecessary, false, true),
				classify(id, "snapshot-trigger-probe", MicroUseful, true, true))
		case BranchASnapshotFailureRetry:
			result = append(result,
				classify(id, "snapshot-failure", MicroNecessary, false, true),
				classify(id, "snapshot-retry", MicroNecessary, false, true),
				classify(id, "snapshot-retry-control", MicroUseful, true, true))
		case BranchASnapshotBeforeHeal:
			result = append(result,
				classify(id, "partition", MicroNecessary, false, true),
				classify(id, "snapshot-enqueued:before-heal", MicroNecessary, false, true))
		case BranchBHigherHeartbeat, BranchBHigherApp, BranchBHigherVote:
			result = append(result,
				classify(id, "restart", MicroNecessary, false, true),
				classify(id, "active-election-control", MicroNecessary, true, true),
				classify(id, "active-election-progress", MicroNecessary, true, true),
				classify(id, "higher-term-pending", MicroNecessary, true, true),
				classify(id, "higher-term-delivery", MicroNecessary, false, true),
				classify(id, "pre-crash-election-preparation", MicroIncidental, true, false),
				classify(id, "pre-restart-message-filter", MicroUseful, true, true),
				classify(id, "pre-delivery-tick", MicroIncidental, true, false),
				classify(id, "pre-delivery-request", MicroIncidental, true, false))
		}
		result = append(result,
			classify(id, "repeated-unrelated-advance-ticks", MicroNoisy, true, false))
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].MicroProgressID < result[j].MicroProgressID
	})
	return result
}
