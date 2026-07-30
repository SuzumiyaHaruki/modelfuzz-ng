// Package goalsearch implements the explicitly enabled manual Raft behavior
// goal and waypoint prototype. It is independent from the default Corpus.
package goalsearch

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/core"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/plan"
)

const SchemaVersion = "raft-behavior-goals-v1-prototype"

const (
	GoalSnapshotCatchUpAfterPartition GoalID = "snapshot-catchup-after-partition"
	GoalRestartHigherTermMessage      GoalID = "restart-then-higher-term-message"
)

type GoalID string
type WaypointType string
type PredicateID string
type Symbol string
type DistanceMode string

const (
	DistanceBooleanOnly DistanceMode = "boolean-only"
	DistanceStaged      DistanceMode = "staged-distance"
)

func (m DistanceMode) Validate() error {
	switch m {
	case DistanceBooleanOnly, DistanceStaged:
		return nil
	default:
		return fmt.Errorf("unknown goal distance mode %q", m)
	}
}

const (
	WaypointState WaypointType = "state"
	WaypointEvent WaypointType = "event_evidence"
)

const (
	SymbolLeader         Symbol = "Leader"
	SymbolTargetFollower Symbol = "TargetFollower"
)

const (
	PredicateStableLeader        PredicateID = "stable-leader-and-target"
	PredicateTargetPartitioned   PredicateID = "target-partitioned"
	PredicateTargetLagging       PredicateID = "target-significantly-lagging"
	PredicateSnapshotRequired    PredicateID = "snapshot-required"
	PredicateNetworkHealed       PredicateID = "network-healed"
	PredicateSnapshotDelivered   PredicateID = "snapshot-message-delivered"
	PredicateSnapshotInstalled   PredicateID = "snapshot-installed-and-recovering"
	PredicateTargetCrashed       PredicateID = "target-crashed"
	PredicateClusterTermAdvanced PredicateID = "cluster-term-advanced-while-target-down"
	PredicateTargetRestarted     PredicateID = "target-restarted-before-catch-up"
	PredicateHigherTermPending   PredicateID = "higher-term-message-pending"
	PredicateHigherTermDelivered PredicateID = "higher-term-message-delivered-and-processed"
)

type ConfigurationConstraint struct {
	MinimumNodes          int    `json:"minimum_nodes"`
	MaximumNodes          int    `json:"maximum_nodes"`
	RequiresSnapshot      bool   `json:"requires_snapshot"`
	RequiresRetainLessMax bool   `json:"requires_retain_less_than_max_log"`
	ModelProfile          string `json:"model_profile,omitempty"`
}

type MutationHint struct {
	WaypointID             string            `json:"waypoint_id"`
	RecommendedActions     []plan.ActionKind `json:"recommended_actions"`
	RecommendedMessageType []string          `json:"recommended_message_types,omitempty"`
	Description            string            `json:"description"`
}

type WaypointDefinition struct {
	ID              string       `json:"id"`
	Name            string       `json:"name"`
	Type            WaypointType `json:"type"`
	Predicate       PredicateID  `json:"predicate"`
	Sticky          bool         `json:"sticky"`
	LocalPlanBudget int          `json:"local_plan_budget"`
	Description     string       `json:"description"`
}

type BehaviorGoalDefinition struct {
	SchemaVersion            string                  `json:"schema_version"`
	GoalID                   GoalID                  `json:"goal_id"`
	Name                     string                  `json:"name"`
	Description              string                  `json:"description"`
	SupportedNodeCounts      []int                   `json:"supported_node_counts"`
	ConfigurationConstraints ConfigurationConstraint `json:"configuration_constraints"`
	EntryCondition           PredicateID             `json:"entry_condition"`
	Waypoints                []WaypointDefinition    `json:"waypoints"`
	TargetPredicate          PredicateID             `json:"target_predicate"`
	AllowedActionTypes       []plan.ActionKind       `json:"allowed_action_types"`
	RecommendedMutationHints []MutationHint          `json:"recommended_mutation_hints"`
	ForbiddenPatterns        []string                `json:"forbidden_patterns"`
	DefaultWaypointBudget    int                     `json:"default_waypoint_plan_budget"`
	DefaultPlanBudget        int                     `json:"default_candidate_plan_budget"`
	DefaultActionBudget      int                     `json:"default_total_action_budget"`
	SuccessEvidence          []string                `json:"success_evidence"`
}

func Definitions() []BehaviorGoalDefinition {
	return []BehaviorGoalDefinition{snapshotGoalDefinition(), restartGoalDefinition()}
}

func Definition(id GoalID, nodeCount int) (BehaviorGoalDefinition, error) {
	definitions := Definitions()
	seen := make(map[GoalID]struct{}, len(definitions))
	for _, definition := range definitions {
		if _, duplicate := seen[definition.GoalID]; duplicate {
			return BehaviorGoalDefinition{}, fmt.Errorf("duplicate goal ID %q", definition.GoalID)
		}
		seen[definition.GoalID] = struct{}{}
		if err := definition.Validate(); err != nil {
			return BehaviorGoalDefinition{}, err
		}
		if definition.GoalID != id {
			continue
		}
		if !containsInt(definition.SupportedNodeCounts, nodeCount) {
			return BehaviorGoalDefinition{}, fmt.Errorf(
				"goal %q does not support %d nodes; supported=%v",
				id, nodeCount, definition.SupportedNodeCounts)
		}
		return definition, nil
	}
	return BehaviorGoalDefinition{}, fmt.Errorf("unknown behavior goal %q", id)
}

func (d BehaviorGoalDefinition) Validate() error {
	if d.SchemaVersion != SchemaVersion {
		return fmt.Errorf("goal %q schema=%q want %q", d.GoalID, d.SchemaVersion, SchemaVersion)
	}
	if d.GoalID == "" || d.Name == "" || d.Description == "" {
		return fmt.Errorf("goal definition identity is incomplete")
	}
	if len(d.SupportedNodeCounts) == 0 || len(d.Waypoints) == 0 {
		return fmt.Errorf("goal %q has no node counts or waypoints", d.GoalID)
	}
	waypoints := make(map[string]struct{}, len(d.Waypoints))
	for index, waypoint := range d.Waypoints {
		if waypoint.ID == "" || waypoint.Predicate == "" || waypoint.Name == "" {
			return fmt.Errorf("goal %q waypoint %d is incomplete", d.GoalID, index)
		}
		if waypoint.Type != WaypointState && waypoint.Type != WaypointEvent {
			return fmt.Errorf("goal %q waypoint %q has invalid type %q", d.GoalID, waypoint.ID, waypoint.Type)
		}
		if _, duplicate := waypoints[waypoint.ID]; duplicate {
			return fmt.Errorf("goal %q contains duplicate waypoint %q", d.GoalID, waypoint.ID)
		}
		waypoints[waypoint.ID] = struct{}{}
	}
	if d.TargetPredicate != d.Waypoints[len(d.Waypoints)-1].Predicate {
		return fmt.Errorf("goal %q target predicate is not its final waypoint", d.GoalID)
	}
	for _, action := range d.AllowedActionTypes {
		if !action.Valid() {
			return fmt.Errorf("goal %q contains invalid allowed action %q", d.GoalID, action)
		}
	}
	return nil
}

func SerializeDefinition(definition BehaviorGoalDefinition) (string, error) {
	if err := definition.Validate(); err != nil {
		return "", err
	}
	encoded, err := json.Marshal(definition)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

type Binding struct {
	Symbol            Symbol      `json:"symbol"`
	Node              core.NodeID `json:"node"`
	BoundAtStep       int         `json:"bound_at_step"`
	BoundAtPlanAction int         `json:"bound_at_plan_action"`
	Reason            string      `json:"reason"`
	Evidence          string      `json:"evidence"`
}

type Evidence struct {
	Kind            string            `json:"kind"`
	WaypointID      string            `json:"waypoint_id,omitempty"`
	StepIndex       int               `json:"step_index"`
	PlanActionIndex int               `json:"plan_action_index"`
	ModelEventIndex int               `json:"model_event_index,omitempty"`
	ActionKind      core.ActionKind   `json:"action_kind,omitempty"`
	ModelEvent      string            `json:"model_event,omitempty"`
	MessageIDs      []core.MessageID  `json:"message_ids,omitempty"`
	Details         map[string]string `json:"details,omitempty"`
}

type WaypointResult struct {
	WaypointID               string            `json:"waypoint_id"`
	Reached                  bool              `json:"reached"`
	FirstReachedStep         int               `json:"first_reached_step"`
	FirstReachedPlanAction   int               `json:"first_reached_plan_action"`
	CurrentSatisfied         bool              `json:"current_satisfied"`
	Evidence                 []Evidence        `json:"evidence,omitempty"`
	Distance                 int               `json:"distance"`
	DistanceExplanation      string            `json:"distance_explanation"`
	BindingsCreated          []Binding         `json:"bindings_created,omitempty"`
	RelatedMessageIDs        []core.MessageID  `json:"related_message_ids,omitempty"`
	RelatedActionIndex       int               `json:"related_action_index,omitempty"`
	RelatedModelEventIndex   int               `json:"related_model_event_index,omitempty"`
	RelatedFacetValues       map[string]string `json:"related_facet_values,omitempty"`
	RelatedInteractionValues map[string]string `json:"related_interaction_values,omitempty"`
	NotDecidableReason       string            `json:"not_decidable_reason,omitempty"`
	InvalidReason            string            `json:"invalid_reason,omitempty"`
}

type GoalProgress struct {
	CompletedWaypointCount  int    `json:"completed_waypoint_count"`
	CurrentWaypointIndex    int    `json:"current_waypoint_index"`
	CurrentWaypointID       string `json:"current_waypoint_id,omitempty"`
	DistanceToCurrent       int    `json:"distance_to_current_waypoint"`
	DistanceExplanation     string `json:"distance_explanation"`
	EvidenceStrength        int    `json:"evidence_strength"`
	LastProgressStep        int    `json:"last_progress_step"`
	LastProgressActionIndex int    `json:"last_progress_action_index"`
	LastProgressPlanIndex   int    `json:"last_progress_plan_action_index"`
	TotalExecutedActions    int    `json:"total_executed_actions"`
	PrefixLength            int    `json:"prefix_length"`
	TargetReached           bool   `json:"target_reached"`
	StableKey               string `json:"stable_key"`
}

type BudgetUsage struct {
	CandidatePlans int `json:"candidate_plans"`
	Actions        int `json:"actions"`
}

type GoalInstance struct {
	SchemaVersion   string             `json:"schema_version"`
	GoalID          GoalID             `json:"goal_id"`
	InstanceID      string             `json:"instance_id"`
	Bindings        map[Symbol]Binding `json:"bindings"`
	CurrentWaypoint int                `json:"current_waypoint"`
	WaypointResults []WaypointResult   `json:"waypoint_results"`
	EventEvidence   []Evidence         `json:"event_evidence"`
	Progress        GoalProgress       `json:"progress"`
	BudgetUsage     BudgetUsage        `json:"budget_usage"`
	InvalidReason   string             `json:"invalid_reason,omitempty"`
	StableKey       string             `json:"stable_key"`
}

func BetterProgress(left, right GoalProgress) bool {
	if left.CompletedWaypointCount != right.CompletedWaypointCount {
		return left.CompletedWaypointCount > right.CompletedWaypointCount
	}
	if left.DistanceToCurrent != right.DistanceToCurrent {
		return left.DistanceToCurrent < right.DistanceToCurrent
	}
	if left.EvidenceStrength != right.EvidenceStrength {
		return left.EvidenceStrength > right.EvidenceStrength
	}
	if left.PrefixLength != right.PrefixLength {
		return left.PrefixLength < right.PrefixLength
	}
	return left.StableKey < right.StableKey
}

func stableHash(value any) string {
	encoded, _ := json.Marshal(value)
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}

func containsInt(values []int, wanted int) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func allGoalActions() []plan.ActionKind {
	return []plan.ActionKind{
		plan.ActionDeliver, plan.ActionDrop, plan.ActionDuplicate, plan.ActionAdvanceTicks,
		plan.ActionTimeout, plan.ActionCrash, plan.ActionRestart, plan.ActionRequest,
		plan.ActionPartition, plan.ActionHeal,
	}
}

func snapshotGoalDefinition() BehaviorGoalDefinition {
	waypoints := []WaypointDefinition{
		{"W1", "稳定 Leader 与目标 Follower", WaypointState, PredicateStableLeader, true, 20, "唯一 Leader、连通 quorum，并确定性绑定非 Leader Follower"},
		{"W2", "隔离目标 Follower", WaypointEvent, PredicateTargetPartitioned, true, 20, "真实 Partition，Leader 一侧仍有 quorum"},
		{"W3", "目标 Follower 明显落后", WaypointState, PredicateTargetLagging, true, 30, "分区后多数派提交，目标进入 small 或更大 lag"},
		{"W4", "需要 Snapshot 追赶", WaypointState, PredicateSnapshotRequired, true, 30, "next/first、pending snapshot 或 MsgSnap 证明普通追赶不足"},
		{"W5", "恢复网络", WaypointEvent, PredicateNetworkHealed, true, 10, "真实 Heal 后重新连通"},
		{"W6", "投递 MsgSnap", WaypointEvent, PredicateSnapshotDelivered, true, 20, "指定发往目标的 MsgSnap 被真实投递"},
		{"W7", "安装 Snapshot 并推进恢复", WaypointEvent, PredicateSnapshotInstalled, true, 20, "对应 Snapshot 安装且 committed prefix 保持安全"},
	}
	return BehaviorGoalDefinition{
		SchemaVersion: SchemaVersion, GoalID: GoalSnapshotCatchUpAfterPartition,
		Name:                "Partition 后通过 Snapshot 完成追赶",
		Description:         "Partition → lag → snapshot-required → Heal → MsgSnap deliver/install → recovery",
		SupportedNodeCounts: []int{3, 5},
		ConfigurationConstraints: ConfigurationConstraint{
			MinimumNodes: 3, MaximumNodes: 5, RequiresSnapshot: true,
			RequiresRetainLessMax: true, ModelProfile: "storage-snapshot",
		},
		EntryCondition: PredicateStableLeader, Waypoints: waypoints,
		TargetPredicate: PredicateSnapshotInstalled, AllowedActionTypes: allGoalActions(),
		RecommendedMutationHints: []MutationHint{
			{"W1", []plan.ActionKind{plan.ActionTimeout, plan.ActionDeliver, plan.ActionAdvanceTicks}, []string{"MsgVote", "MsgVoteResp"}, "形成唯一 Leader"},
			{"W2", []plan.ActionKind{plan.ActionPartition}, nil, "隔离已绑定 TargetFollower"},
			{"W3", []plan.ActionKind{plan.ActionRequest, plan.ActionDeliver, plan.ActionAdvanceTicks}, []string{"MsgApp", "MsgAppResp"}, "只在多数派推进提交"},
			{"W4", []plan.ActionKind{plan.ActionRequest, plan.ActionDeliver}, []string{"MsgAppResp"}, "推进压缩边界直到需要 Snapshot"},
			{"W5", []plan.ActionKind{plan.ActionHeal}, nil, "恢复连通"},
			{"W6", []plan.ActionKind{plan.ActionDeliver}, []string{"MsgSnap"}, "投递绑定目标的 Snapshot"},
			{"W7", []plan.ActionKind{plan.ActionDeliver, plan.ActionAdvanceTicks}, []string{"MsgAppResp", "MsgHeartbeat"}, "处理恢复响应和后续复制"},
		},
		ForbiddenPatterns: []string{
			"nested-partition", "heal-without-partition", "crash-already-crashed",
			"restart-running-node", "modify-preserved-prefix",
		},
		DefaultWaypointBudget: 30, DefaultPlanBudget: 80, DefaultActionBudget: 6400,
		SuccessEvidence: []string{
			"partition-action", "snapshot-required-relation", "heal-action",
			"MsgSnap-message-id-delivered", "snapshot-applied-effect",
		},
	}
}

func restartGoalDefinition() BehaviorGoalDefinition {
	waypoints := []WaypointDefinition{
		{"W1", "稳定 Leader 与目标 Follower", WaypointState, PredicateStableLeader, true, 20, "唯一 Leader、连通 quorum，绑定目标并记录 term"},
		{"W2", "Crash 目标 Follower", WaypointEvent, PredicateTargetCrashed, true, 15, "真实 Crash 且其余节点可形成 quorum"},
		{"W3", "目标离线期间 term 推进", WaypointEvent, PredicateClusterTermAdvanced, true, 30, "真实 Timeout/选举证据与相对 term gap"},
		{"W4", "Restart 但尚未追赶", WaypointEvent, PredicateTargetRestarted, true, 15, "真实 Restart，term/log/commit 至少一项仍落后"},
		{"W5", "更高 term 协议消息待投递", WaypointState, PredicateHigherTermPending, true, 20, "队列存在发往目标的可靠 higher-term 协议消息"},
		{"W6", "投递并处理更高 term 消息", WaypointEvent, PredicateHigherTermDelivered, true, 20, "指定 MessageID 投递，目标 term 无回退并正确前进"},
	}
	return BehaviorGoalDefinition{
		SchemaVersion: SchemaVersion, GoalID: GoalRestartHigherTermMessage,
		Name:                "Restart 节点在追赶前处理更高 term 消息",
		Description:         "Crash → active cluster term advance → Restart incomplete → higher-term message pending/delivered",
		SupportedNodeCounts: []int{3, 5},
		ConfigurationConstraints: ConfigurationConstraint{
			MinimumNodes: 3, MaximumNodes: 5, ModelProfile: "storage-snapshot",
		},
		EntryCondition: PredicateStableLeader, Waypoints: waypoints,
		TargetPredicate: PredicateHigherTermDelivered, AllowedActionTypes: allGoalActions(),
		RecommendedMutationHints: []MutationHint{
			{"W1", []plan.ActionKind{plan.ActionTimeout, plan.ActionDeliver, plan.ActionAdvanceTicks}, []string{"MsgVote", "MsgVoteResp"}, "形成唯一 Leader"},
			{"W2", []plan.ActionKind{plan.ActionCrash}, nil, "Crash 已绑定 Follower"},
			{"W3", []plan.ActionKind{plan.ActionTimeout, plan.ActionAdvanceTicks, plan.ActionDeliver}, []string{"MsgVote", "MsgVoteResp"}, "在目标离线时推进活动集群 term"},
			{"W4", []plan.ActionKind{plan.ActionRestart}, nil, "Restart 已绑定节点"},
			{"W5", []plan.ActionKind{plan.ActionAdvanceTicks, plan.ActionDeliver}, []string{"MsgHeartbeat", "MsgApp", "MsgVote"}, "产生 higher-term 协议消息"},
			{"W6", []plan.ActionKind{plan.ActionDeliver}, []string{"MsgHeartbeat", "MsgApp", "MsgVote", "MsgVoteResp"}, "投递指定 higher-term MessageID"},
		},
		ForbiddenPatterns: []string{
			"restart-before-crash", "deliver-client-request-as-term-evidence",
			"target-recovered-before-higher-term-delivery", "modify-preserved-prefix",
		},
		DefaultWaypointBudget: 25, DefaultPlanBudget: 70, DefaultActionBudget: 5600,
		SuccessEvidence: []string{
			"crash-action", "term-advance-model-event", "restart-action",
			"higher-term-message-id", "deliver-action", "post-delivery-term",
		},
	}
}

func sortedBindingValues(bindings map[Symbol]Binding) []Binding {
	result := make([]Binding, 0, len(bindings))
	for _, binding := range bindings {
		result = append(result, binding)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Symbol < result[j].Symbol })
	return result
}
