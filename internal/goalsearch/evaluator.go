package goalsearch

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/core"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/engine"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/model"
	raftmodel "github.com/SuzumiyaHaruki/modelfuzz-ng/internal/model/raft"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/plan"
)

type ProgressChange string

const (
	ChangeNone             ProgressChange = "none"
	ChangeWaypointReached  ProgressChange = "waypoint_reached"
	ChangeDistanceImproved ProgressChange = "distance_improved"
	ChangeDistanceWorsened ProgressChange = "distance_worsened"
	ChangeInvalid          ProgressChange = "invalid"
)

type ProgressUpdate struct {
	StepIndex       int            `json:"step_index"`
	PlanActionIndex int            `json:"plan_action_index"`
	ModelEventIndex int            `json:"model_event_index"`
	Change          ProgressChange `json:"change"`
	Before          GoalProgress   `json:"before"`
	After           GoalProgress   `json:"after"`
}

type EvaluationResult struct {
	SchemaVersion        string           `json:"schema_version"`
	Instance             GoalInstance     `json:"instance"`
	Updates              []ProgressUpdate `json:"updates"`
	ProgressUpdates      int              `json:"progress_updates"`
	DistanceImprovements int              `json:"distance_improvements"`
	DistanceWorsenings   int              `json:"distance_worsenings"`
	TargetReached        bool             `json:"target_reached"`
	TargetReachedStep    int              `json:"target_reached_step"`
	TargetReachedPlan    int              `json:"target_reached_plan_action"`
	PrefixEndActionIndex int              `json:"prefix_end_action_index"`
	PrefixEndTraceStep   int              `json:"prefix_end_trace_step"`
	PrefixObservation    core.Observation `json:"prefix_observation"`
	FinalObservation     core.Observation `json:"final_observation"`
	Online               bool             `json:"online"`
	StableKey            string           `json:"stable_key"`
}

type evalDistance struct {
	value       int
	explanation string
	decidable   bool
	reason      string
}

type predicateResult struct {
	satisfied   bool
	distance    evalDistance
	evidence    []Evidence
	bindings    []Binding
	messageIDs  []core.MessageID
	facet       map[string]string
	interaction map[string]string
	invalid     string
}

type evalFrame struct {
	stepIndex       int
	planActionIndex int
	actionIndex     int
	action          core.Action
	effects         []core.Effect
	before          core.Observation
	after           core.Observation
	modelEvent      *model.Event
	modelEventIndex int
	delivered       *core.Message
	justHealed      bool
}

type Evaluator struct {
	definition   BehaviorGoalDefinition
	instance     GoalInstance
	tracker      *prefixTracker
	updates      []ProgressUpdate
	online       bool
	distanceMode DistanceMode

	eventCursor       int
	actionCursor      int
	lastProgressStep  int
	lastProgressPlan  int
	prefixEndStep     int
	prefixObservation core.Observation
	finalObservation  core.Observation

	w2LeaderCommit  uint64
	crashTerm       uint64
	termAdvanceSeen bool
	snapshotMessage core.MessageID
	higherMessage   core.MessageID

	progressUpdates      int
	distanceImprovements int
	distanceWorsenings   int
}

func NewEvaluator(
	definition BehaviorGoalDefinition, instanceID string, online bool,
) (*Evaluator, error) {
	return NewEvaluatorWithDistance(definition, instanceID, online, DistanceStaged)
}

func NewEvaluatorWithDistance(
	definition BehaviorGoalDefinition,
	instanceID string,
	online bool,
	distanceMode DistanceMode,
) (*Evaluator, error) {
	if err := definition.Validate(); err != nil {
		return nil, err
	}
	if err := distanceMode.Validate(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(instanceID) == "" {
		return nil, fmt.Errorf("goal instance ID is empty")
	}
	results := make([]WaypointResult, len(definition.Waypoints))
	for index, waypoint := range definition.Waypoints {
		results[index] = WaypointResult{
			WaypointID: waypoint.ID, FirstReachedStep: -1, FirstReachedPlanAction: -1,
			Distance: 99, DistanceExplanation: "尚未求值",
			RelatedActionIndex: -1, RelatedModelEventIndex: -1,
		}
	}
	instance := GoalInstance{
		SchemaVersion: SchemaVersion, GoalID: definition.GoalID, InstanceID: instanceID,
		Bindings: make(map[Symbol]Binding), WaypointResults: results,
		EventEvidence: make([]Evidence, 0),
		Progress: GoalProgress{
			CurrentWaypointIndex: 0, CurrentWaypointID: definition.Waypoints[0].ID,
			DistanceToCurrent: 99, DistanceExplanation: "尚未求值",
			LastProgressStep: -1, LastProgressActionIndex: -1, LastProgressPlanIndex: -1,
		},
	}
	return &Evaluator{
		definition: definition, instance: instance, online: online,
		distanceMode: distanceMode,
		eventCursor:  -1, actionCursor: -1, lastProgressStep: -1, lastProgressPlan: -1,
		prefixEndStep: -1,
	}, nil
}

func (e *Evaluator) Reset(initial core.Observation) error {
	if e == nil {
		return fmt.Errorf("goal evaluator is nil")
	}
	if err := initial.Validate(); err != nil {
		return fmt.Errorf("goal evaluator initial observation: %w", err)
	}
	tracker, err := newPrefixTracker(initial)
	if err != nil {
		return err
	}
	e.tracker = tracker
	e.finalObservation = initial.Copy()
	frame := evalFrame{
		stepIndex: -1, planActionIndex: -1, actionIndex: -1,
		before: initial.Copy(), after: initial.Copy(), modelEventIndex: -1,
	}
	e.evaluateFrame(frame)
	return nil
}

func (e *Evaluator) Observe(step engine.PrefixStep) error {
	if e == nil || e.tracker == nil {
		return fmt.Errorf("goal evaluator is not reset")
	}
	if step.ActionIndex != e.actionCursor+1 {
		return fmt.Errorf("goal action index=%d want %d", step.ActionIndex, e.actionCursor+1)
	}
	if err := step.Record.Validate(); err != nil {
		return fmt.Errorf("goal prefix step: %w", err)
	}
	delivered, justHealed, err := e.tracker.apply(step.Record)
	if err != nil {
		return err
	}
	e.actionCursor = step.ActionIndex
	after := e.tracker.observation(
		step.Record.TimeAfter, step.Record.NodesAfter, &step.Record.Action)
	before := e.tracker.beforeObservation(
		step.Record.TimeBefore, step.Record.NodesBefore, &step.Record.Action, delivered)
	e.finalObservation = after.Copy()
	if len(step.ModelEvents) == 0 {
		e.evaluateFrame(evalFrame{
			stepIndex: int(step.Record.Index), planActionIndex: step.PlanActionIndex,
			actionIndex: step.ActionIndex, action: step.Record.Action.Copy(),
			effects: copyEffects(step.Record.Effects), before: before, after: after,
			modelEventIndex: -1, delivered: copyMessagePointer(delivered), justHealed: justHealed,
		})
		return nil
	}
	for _, event := range step.ModelEvents {
		e.eventCursor++
		copied := event.Copy()
		e.evaluateFrame(evalFrame{
			stepIndex: int(step.Record.Index), planActionIndex: step.PlanActionIndex,
			actionIndex: step.ActionIndex, action: step.Record.Action.Copy(),
			effects: copyEffects(step.Record.Effects), before: before, after: after,
			modelEvent: &copied, modelEventIndex: e.eventCursor,
			delivered: copyMessagePointer(delivered), justHealed: justHealed,
		})
	}
	return nil
}

func (e *Evaluator) Result() EvaluationResult {
	if e == nil {
		return EvaluationResult{}
	}
	e.refreshStableKeys()
	targetStep, targetPlan := -1, -1
	if len(e.instance.WaypointResults) > 0 {
		target := e.instance.WaypointResults[len(e.instance.WaypointResults)-1]
		if target.Reached {
			targetStep, targetPlan = target.FirstReachedStep, target.FirstReachedPlanAction
		}
	}
	instance := copyInstance(e.instance)
	instance.BudgetUsage = BudgetUsage{
		CandidatePlans: 1,
		Actions:        instance.Progress.TotalExecutedActions,
	}
	result := EvaluationResult{
		SchemaVersion: SchemaVersion, Instance: instance,
		Updates:              append([]ProgressUpdate(nil), e.updates...),
		ProgressUpdates:      e.progressUpdates,
		DistanceImprovements: e.distanceImprovements,
		DistanceWorsenings:   e.distanceWorsenings,
		TargetReached:        e.instance.Progress.TargetReached,
		TargetReachedStep:    targetStep, TargetReachedPlan: targetPlan,
		PrefixEndActionIndex: e.lastProgressPlan, PrefixEndTraceStep: e.prefixEndStep,
		PrefixObservation: e.prefixObservation.Copy(),
		FinalObservation:  e.finalObservation.Copy(), Online: e.online,
	}
	copyForKey := result
	copyForKey.Online = false
	copyForKey.StableKey = ""
	copyForKey.Instance.StableKey = ""
	copyForKey.Instance.Progress.StableKey = ""
	for index := range copyForKey.Updates {
		copyForKey.Updates[index].Before.StableKey = ""
		copyForKey.Updates[index].After.StableKey = ""
	}
	result.StableKey = stableHash(copyForKey)
	return result
}

func (e *Evaluator) evaluateFrame(frame evalFrame) {
	before := e.instance.Progress
	if e.instance.InvalidReason != "" || e.instance.Progress.TargetReached {
		e.updateCurrentSatisfaction(frame)
		e.instance.Progress.TotalExecutedActions = max(0, frame.actionIndex+1)
		e.finalizeProgressKey(frame)
		return
	}
	for e.instance.CurrentWaypoint < len(e.definition.Waypoints) {
		index := e.instance.CurrentWaypoint
		definition := e.definition.Waypoints[index]
		evaluated := e.evaluatePredicate(definition.Predicate, frame, false)
		evaluated.distance = e.effectiveDistance(evaluated.satisfied, evaluated.distance)
		result := &e.instance.WaypointResults[index]
		result.CurrentSatisfied = evaluated.satisfied
		result.Distance = evaluated.distance.value
		result.DistanceExplanation = evaluated.distance.explanation
		result.NotDecidableReason = ""
		if !evaluated.distance.decidable {
			result.NotDecidableReason = evaluated.distance.reason
		}
		result.InvalidReason = evaluated.invalid
		result.RelatedMessageIDs = uniqueMessageIDs(evaluated.messageIDs)
		result.RelatedFacetValues = cloneStringMap(evaluated.facet)
		result.RelatedInteractionValues = cloneStringMap(evaluated.interaction)
		if evaluated.invalid != "" {
			e.instance.InvalidReason = evaluated.invalid
			break
		}
		if !evaluated.satisfied {
			break
		}
		result.Reached = true
		result.FirstReachedStep = frame.stepIndex
		result.FirstReachedPlanAction = frame.planActionIndex
		result.Evidence = append([]Evidence(nil), evaluated.evidence...)
		result.BindingsCreated = append([]Binding(nil), evaluated.bindings...)
		result.RelatedActionIndex = frame.actionIndex
		result.RelatedModelEventIndex = frame.modelEventIndex
		e.instance.EventEvidence = append(e.instance.EventEvidence, evaluated.evidence...)
		for _, binding := range evaluated.bindings {
			e.instance.Bindings[binding.Symbol] = binding
		}
		e.instance.CurrentWaypoint++
		e.instance.Progress.CompletedWaypointCount = e.instance.CurrentWaypoint
		e.captureProgressPrefix(frame)
	}
	e.updateCurrentSatisfaction(frame)
	e.instance.Progress.TotalExecutedActions = max(0, frame.actionIndex+1)
	e.instance.Progress.PrefixLength = max(0, e.lastProgressPlan+1)
	e.instance.Progress.EvidenceStrength = evidenceStrength(e.instance)
	e.instance.Progress.LastProgressStep = e.lastProgressStep
	e.instance.Progress.LastProgressActionIndex = e.prefixEndStep
	e.instance.Progress.LastProgressPlanIndex = e.lastProgressPlan
	e.instance.Progress.TargetReached = e.instance.CurrentWaypoint == len(e.definition.Waypoints)
	if e.instance.Progress.TargetReached {
		e.instance.Progress.CurrentWaypointIndex = len(e.definition.Waypoints)
		e.instance.Progress.CurrentWaypointID = ""
		e.instance.Progress.DistanceToCurrent = 0
		e.instance.Progress.DistanceExplanation = "目标已达成"
	} else {
		current := e.instance.WaypointResults[e.instance.CurrentWaypoint]
		e.instance.Progress.CurrentWaypointIndex = e.instance.CurrentWaypoint
		e.instance.Progress.CurrentWaypointID = current.WaypointID
		e.instance.Progress.DistanceToCurrent = current.Distance
		e.instance.Progress.DistanceExplanation = current.DistanceExplanation
	}
	change := ChangeNone
	switch {
	case e.instance.InvalidReason != "":
		change = ChangeInvalid
	case e.instance.Progress.CompletedWaypointCount > before.CompletedWaypointCount:
		change = ChangeWaypointReached
	case e.instance.Progress.CurrentWaypointIndex == before.CurrentWaypointIndex &&
		e.instance.Progress.DistanceToCurrent < before.DistanceToCurrent:
		change = ChangeDistanceImproved
	case e.instance.Progress.CurrentWaypointIndex == before.CurrentWaypointIndex &&
		e.instance.Progress.DistanceToCurrent > before.DistanceToCurrent &&
		before.DistanceToCurrent != 99:
		change = ChangeDistanceWorsened
	}
	if change == ChangeWaypointReached || change == ChangeDistanceImproved {
		e.captureProgressPrefix(frame)
		e.progressUpdates++
		if change == ChangeDistanceImproved {
			e.distanceImprovements++
		}
	}
	if change == ChangeDistanceWorsened {
		e.distanceWorsenings++
	}
	e.finalizeProgressKey(frame)
	if change != ChangeNone {
		e.updates = append(e.updates, ProgressUpdate{
			StepIndex: frame.stepIndex, PlanActionIndex: frame.planActionIndex,
			ModelEventIndex: frame.modelEventIndex, Change: change,
			Before: before, After: e.instance.Progress,
		})
	}
}

func (e *Evaluator) effectiveDistance(satisfied bool, distance evalDistance) evalDistance {
	if e.distanceMode != DistanceBooleanOnly || satisfied {
		return distance
	}
	distance.value = 1
	distance.explanation = "boolean-only：当前 Waypoint 尚未完成"
	return distance
}

func (e *Evaluator) finalizeProgressKey(frame evalFrame) {
	keyInput := struct {
		Goal      GoalID
		Bindings  []Binding
		Completed int
		Current   int
		Distance  int
		Evidence  int
		Actions   int
	}{
		e.definition.GoalID, sortedBindingValues(e.instance.Bindings),
		e.instance.Progress.CompletedWaypointCount, e.instance.Progress.CurrentWaypointIndex,
		e.instance.Progress.DistanceToCurrent, e.instance.Progress.EvidenceStrength,
		max(0, frame.actionIndex+1),
	}
	e.instance.Progress.StableKey = stableHash(keyInput)
}

func (e *Evaluator) refreshStableKeys() {
	copy := e.instance
	copy.StableKey = ""
	copy.Progress.StableKey = ""
	e.instance.StableKey = stableHash(copy)
}

func (e *Evaluator) captureProgressPrefix(frame evalFrame) {
	if frame.planActionIndex < 0 {
		return
	}
	e.lastProgressStep = frame.stepIndex
	e.lastProgressPlan = frame.planActionIndex
	e.prefixEndStep = frame.stepIndex
	e.prefixObservation = frame.after.Copy()
}

func (e *Evaluator) updateCurrentSatisfaction(frame evalFrame) {
	for index := 0; index < min(e.instance.CurrentWaypoint, len(e.definition.Waypoints)); index++ {
		if e.definition.Waypoints[index].Type != WaypointState {
			continue
		}
		// higher-term-message-pending binds an exact MessageID. Re-running that
		// predicate after the waypoint was reached could silently replace the
		// causal object when another message appears. Its current satisfaction
		// is therefore checked against the already bound ID only.
		if e.definition.Waypoints[index].Predicate == PredicateHigherTermPending {
			e.instance.WaypointResults[index].CurrentSatisfied =
				messageObservationByID(frame.after.Messages, e.higherMessage).ID.Valid()
			continue
		}
		current := e.evaluatePredicate(e.definition.Waypoints[index].Predicate, frame, true)
		e.instance.WaypointResults[index].CurrentSatisfied = current.satisfied
	}
}

func (e *Evaluator) evaluatePredicate(
	predicate PredicateID, frame evalFrame, currentOnly bool,
) predicateResult {
	switch predicate {
	case PredicateStableLeader:
		return e.stableLeader(frame, currentOnly)
	case PredicateTargetPartitioned:
		return e.targetPartitioned(frame)
	case PredicateTargetLagging:
		return e.targetLagging(frame)
	case PredicateSnapshotRequired:
		return e.snapshotRequired(frame)
	case PredicateNetworkHealed:
		return e.networkHealed(frame)
	case PredicateSnapshotDelivered:
		return e.snapshotDelivered(frame)
	case PredicateSnapshotInstalled:
		return e.snapshotInstalled(frame)
	case PredicateTargetCrashed:
		return e.targetCrashed(frame)
	case PredicateClusterTermAdvanced:
		return e.clusterTermAdvanced(frame)
	case PredicateTargetRestarted:
		return e.targetRestarted(frame)
	case PredicateHigherTermPending:
		return e.higherTermPending(frame)
	case PredicateHigherTermDelivered:
		return e.higherTermDelivered(frame)
	default:
		return predicateResult{
			distance: evalDistance{value: 99, explanation: "未知谓词", reason: string(predicate)},
			invalid:  "unknown predicate " + string(predicate),
		}
	}
}

func (e *Evaluator) stableLeader(frame evalFrame, currentOnly bool) predicateResult {
	leaders := runningRoleNodes(frame.after.Nodes, "leader")
	candidates := runningRoleNodes(frame.after.Nodes, "candidate")
	distance := evalDistance{decidable: true}
	switch {
	case len(leaders) != 1 && len(candidates) > 0:
		distance.value, distance.explanation = 2, "存在 Candidate，但尚无唯一 Leader"
	case len(leaders) != 1:
		distance.value, distance.explanation = 3, "尚无唯一 Leader"
	case !leaderHasConnectedQuorum(frame.after, leaders[0].ID):
		distance.value, distance.explanation = 1, "存在唯一 Leader，但其连通分量没有 quorum"
	default:
		distance.value, distance.explanation = 0, "唯一 Leader 且连通 quorum 可用"
	}
	if distance.value != 0 {
		return predicateResult{distance: distance, facet: electionFacetSummary(frame.after)}
	}
	if currentOnly {
		leaderBinding, leaderOK := e.instance.Bindings[SymbolLeader]
		targetBinding, targetOK := e.instance.Bindings[SymbolTargetFollower]
		return predicateResult{
			satisfied: leaderOK && targetOK && leaderBinding.Node == leaders[0].ID &&
				nodeRunningNonLeader(frame.after, targetBinding.Node),
			distance: distance, facet: electionFacetSummary(frame.after),
		}
	}
	if _, bound := e.instance.Bindings[SymbolLeader]; bound {
		leader := e.instance.Bindings[SymbolLeader]
		target := e.instance.Bindings[SymbolTargetFollower]
		if leader.Node != leaders[0].ID {
			return predicateResult{distance: distance, invalid: fmt.Sprintf(
				"bound Leader %s changed to %s; automatic rebinding is disabled", leader.Node, leaders[0].ID)}
		}
		return predicateResult{
			satisfied: nodeRunningNonLeader(frame.after, target.Node), distance: distance,
			facet: electionFacetSummary(frame.after),
		}
	}
	targets := runningNonLeaderNodes(frame.after.Nodes, leaders[0].ID)
	if len(targets) == 0 {
		distance.value, distance.explanation = 1, "没有可绑定的 active follower"
		return predicateResult{distance: distance}
	}
	sort.Slice(targets, func(i, j int) bool {
		left, _ := semanticUint(targets[i].Semantic["last_index"])
		right, _ := semanticUint(targets[j].Semantic["last_index"])
		if left != right {
			return left < right
		}
		return targets[i].ID > targets[j].ID
	})
	leaderBinding := Binding{
		Symbol: SymbolLeader, Node: leaders[0].ID, BoundAtStep: frame.stepIndex,
		BoundAtPlanAction: frame.planActionIndex,
		Reason:            "唯一 active Leader", Evidence: leaders[0].Digest,
	}
	targetBinding := Binding{
		Symbol: SymbolTargetFollower, Node: targets[0].ID, BoundAtStep: frame.stepIndex,
		BoundAtPlanAction: frame.planActionIndex,
		Reason:            "最低 last_index；相同则选择最大 NodeID 的非 Leader active 节点",
		Evidence:          targets[0].Digest,
	}
	evidence := Evidence{
		Kind: "state", WaypointID: "W1", StepIndex: frame.stepIndex,
		PlanActionIndex: frame.planActionIndex, ModelEventIndex: frame.modelEventIndex,
		Details: map[string]string{
			"leader": leaders[0].ID.String(), "target_follower": targets[0].ID.String(),
		},
	}
	return predicateResult{
		satisfied: true, distance: distance, evidence: []Evidence{evidence},
		bindings: []Binding{leaderBinding, targetBinding}, facet: electionFacetSummary(frame.after),
	}
}

func (e *Evaluator) targetPartitioned(frame evalFrame) predicateResult {
	leader, target, ok := e.boundPair()
	distance := evalDistance{value: 2, explanation: "尚无活动分区", decidable: ok}
	if !ok {
		distance.reason = "Leader/TargetFollower 尚未绑定"
		return predicateResult{distance: distance}
	}
	if frame.after.NetworkPartition != nil {
		distance.value, distance.explanation = 1, "已有分区，但没有同时满足目标隔离与 Leader quorum"
	}
	satisfied := frame.action.Kind == core.ActionPartition &&
		frame.after.NetworkPartition != nil &&
		frame.after.NetworkPartition.Blocks(core.LinkID{From: leader, To: target}) &&
		leaderHasConnectedQuorum(frame.after, leader)
	if !satisfied {
		return predicateResult{
			distance: distance, facet: networkFacetSummary(frame.after, frame.justHealed),
		}
	}
	distance.value, distance.explanation = 0, "真实 Partition 隔离 TargetFollower，Leader 一侧保有 quorum"
	leaderNode, _ := findNode(frame.after.Nodes, leader)
	e.w2LeaderCommit, _ = semanticUint(leaderNode.Semantic["commit"])
	evidence := actionEvidence("partition", "W2", frame, nil, map[string]string{
		"leader": leader.String(), "target": target.String(),
		"leader_commit_at_partition": strconv.FormatUint(e.w2LeaderCommit, 10),
	})
	return predicateResult{
		satisfied: true, distance: distance, evidence: []Evidence{evidence},
		facet:       networkFacetSummary(frame.after, frame.justHealed),
		interaction: map[string]string{"election_network": "unique-leader×target-isolated-quorum"},
	}
}

func (e *Evaluator) targetLagging(frame evalFrame) predicateResult {
	leader, target, ok := e.boundPair()
	if !ok {
		return undecidable("Leader/TargetFollower 尚未绑定", 5)
	}
	leaderNode, leaderOK := findNode(frame.after.Nodes, leader)
	targetNode, targetOK := findNode(frame.after.Nodes, target)
	if !leaderOK || !targetOK || leaderNode.Status != core.NodeRunning {
		return undecidable("绑定节点当前不可观察", 5)
	}
	leaderLast, lastOK := semanticUint(leaderNode.Semantic["last_index"])
	targetLast, targetOKIndex := semanticUint(targetNode.Semantic["last_index"])
	leaderCommit, commitOK := semanticUint(leaderNode.Semantic["commit"])
	if !lastOK || !targetOKIndex || !commitOK {
		return undecidable("Observation 缺少 last_index/commit", 5)
	}
	lag := uint64(0)
	if leaderLast > targetLast {
		lag = leaderLast - targetLast
	}
	bucket, ordinal := lagClass(lag)
	distance := evalDistance{
		// Zero is reserved for a satisfied predicate. A large lag alone is not
		// enough: the majority commit must advance and the committed prefix must
		// remain compatible.
		value: max(1, 4-ordinal), decidable: true,
		explanation: fmt.Sprintf("TargetFollower replication lag=%s", bucket),
	}
	commitAdvanced := leaderCommit > e.w2LeaderCommit
	safe := committedPrefixSafe(leaderNode, targetNode)
	satisfied := commitAdvanced && ordinal >= 2 && safe
	if satisfied {
		distance.value = 0
		distance.explanation = "分区后多数派 commit 已推进，TargetFollower lag 至少为 small"
	}
	return predicateResult{
		satisfied: satisfied, distance: distance,
		evidence: conditionalEvidence(satisfied, Evidence{
			Kind: "state", WaypointID: "W3", StepIndex: frame.stepIndex,
			PlanActionIndex: frame.planActionIndex, ModelEventIndex: frame.modelEventIndex,
			Details: map[string]string{
				"lag_bucket": bucket, "commit_advanced": strconv.FormatBool(commitAdvanced),
				"committed_prefix_safe": strconv.FormatBool(safe),
			},
		}),
		facet: map[string]string{"replication": "lag=" + bucket},
	}
}

func (e *Evaluator) snapshotRequired(frame evalFrame) predicateResult {
	leader, target, ok := e.boundPair()
	if !ok {
		return undecidable("Leader/TargetFollower 尚未绑定", 5)
	}
	leaderNode, leaderOK := findNode(frame.after.Nodes, leader)
	targetNode, targetOK := findNode(frame.after.Nodes, target)
	if !leaderOK || !targetOK {
		return undecidable("绑定节点当前不可观察", 5)
	}
	required, reason, decidable := snapshotCatchUpRequired(leaderNode, targetNode, frame.after.Messages)
	leaderLast, _ := semanticUint(leaderNode.Semantic["last_index"])
	targetLast, _ := semanticUint(targetNode.Semantic["last_index"])
	lag := uint64(0)
	if leaderLast > targetLast {
		lag = leaderLast - targetLast
	}
	bucket, ordinal := lagClass(lag)
	distance := evalDistance{decidable: decidable}
	if !decidable {
		distance.value, distance.explanation, distance.reason = 5, "无法可靠判断 Snapshot 边界", reason
	} else if required {
		distance.value, distance.explanation = 0, reason
	} else {
		distance.value, distance.explanation = snapshotBoundaryDistance(
			leaderNode, targetNode, bucket, ordinal,
		)
	}
	return predicateResult{
		satisfied: required, distance: distance,
		evidence: conditionalEvidence(required, Evidence{
			Kind: "state", WaypointID: "W4", StepIndex: frame.stepIndex,
			PlanActionIndex: frame.planActionIndex, ModelEventIndex: frame.modelEventIndex,
			Details: map[string]string{"reason": reason, "lag_bucket": bucket},
		}),
		facet: map[string]string{"snapshot": reason, "replication": "lag=" + bucket},
	}
}

func (e *Evaluator) networkHealed(frame evalFrame) predicateResult {
	_, target, ok := e.boundPair()
	distance := evalDistance{value: 1, explanation: "尚未真实执行 Heal", decidable: ok}
	if !ok {
		distance.reason = "TargetFollower 尚未绑定"
		return predicateResult{distance: distance}
	}
	satisfied := frame.action.Kind == core.ActionHeal && frame.after.NetworkPartition == nil
	if satisfied {
		distance.value, distance.explanation = 0, "真实 Heal 已恢复网络连通性"
	}
	return predicateResult{
		satisfied: satisfied, distance: distance,
		evidence: conditionalEvidence(satisfied, actionEvidence(
			"heal", "W5", frame, nil, map[string]string{"target": target.String()})),
		facet: networkFacetSummary(frame.after, frame.justHealed),
	}
}

func (e *Evaluator) snapshotDelivered(frame evalFrame) predicateResult {
	leader, target, ok := e.boundPair()
	if !ok {
		return undecidable("TargetFollower 尚未绑定", 2)
	}
	pending := firstMessage(frame.after.Messages, func(message core.MessageObservation) bool {
		return message.To == target && message.TypeHint == "MsgSnap"
	})
	if !e.snapshotMessage.Valid() && pending.ID.Valid() {
		e.snapshotMessage = pending.ID
	}
	boundPending := messageObservationByID(frame.after.Messages, e.snapshotMessage)
	rejectionPending := firstMessage(frame.after.Messages, func(message core.MessageObservation) bool {
		return message.From == target && message.To == leader && message.TypeHint == "MsgAppResp"
	})
	replicationPending := firstMessage(frame.after.Messages, func(message core.MessageObservation) bool {
		return message.From == leader && message.To == target &&
			(message.TypeHint == "MsgApp" || message.TypeHint == "MsgHeartbeat")
	})
	delivered := frame.delivered != nil && frame.action.Kind == core.ActionDeliver &&
		frame.delivered.ID == e.snapshotMessage && frame.delivered.To == target &&
		frame.delivered.TypeHint == "MsgSnap"
	distance := evalDistance{decidable: true}
	switch {
	case delivered:
		distance.value, distance.explanation = 0, "指定 MsgSnap 已真实投递"
	case boundPending.ID.Valid() && !boundPending.Blocked:
		distance.value, distance.explanation = 1, "精确 MsgSnap MessageID 已绑定且可投递"
	case boundPending.ID.Valid():
		distance.value, distance.explanation = 2, "精确 MsgSnap MessageID 已绑定但当前被分区阻塞"
	case pending.ID.Valid() && !pending.Blocked:
		distance.value, distance.explanation = 2, "MsgSnap 已生成且可投递，尚未绑定因果 MessageID"
	case pending.ID.Valid():
		distance.value, distance.explanation = 3, "MsgSnap 已生成但当前被分区阻塞"
	case rejectionPending.ID.Valid():
		distance.value, distance.explanation = 4, "TargetFollower 的复制拒绝等待 Leader 处理"
	case replicationPending.ID.Valid():
		distance.value, distance.explanation = 5, "有待 TargetFollower 处理的复制消息"
	default:
		distance.value, distance.explanation = 6, "网络已 Heal，但尚无 Snapshot 恢复相关消息"
	}
	if !delivered {
		ids := make([]core.MessageID, 0, 1)
		for _, message := range []core.MessageObservation{pending, rejectionPending, replicationPending} {
			if message.ID.Valid() {
				ids = append(ids, message.ID)
				break
			}
		}
		return predicateResult{
			distance: distance, messageIDs: ids,
			facet: map[string]string{"snapshot": distance.explanation},
		}
	}
	evidence := actionEvidence("message-delivered", "W6", frame,
		[]core.MessageID{frame.delivered.ID}, cloneStringMap(frame.delivered.Metadata))
	return predicateResult{
		satisfied: true, distance: distance, evidence: []Evidence{evidence},
		messageIDs:  []core.MessageID{frame.delivered.ID},
		facet:       map[string]string{"snapshot": "delivered"},
		interaction: map[string]string{"snapshot_recovery": "MsgSnap-delivered×target-recovery"},
	}
}

func (e *Evaluator) snapshotInstalled(frame evalFrame) predicateResult {
	_, target, ok := e.boundPair()
	if !ok || !e.snapshotMessage.Valid() {
		return undecidable("尚无已投递的目标 MsgSnap MessageID", 2)
	}
	applied := false
	for _, effect := range frame.effects {
		if effect.Kind != core.EffectModelEvent || effect.ModelEvent == nil {
			continue
		}
		if effect.ModelEvent.Name == "raft.snapshot_applied" && effect.ModelEvent.Node == target {
			applied = true
		}
	}
	if frame.modelEvent != nil && frame.modelEvent.Name == "InstallSnapshot" {
		applied = true
	}
	distance := evalDistance{value: 1, explanation: "MsgSnap 已投递，但尚无安装 evidence", decidable: true}
	if !applied {
		return predicateResult{distance: distance, messageIDs: []core.MessageID{e.snapshotMessage}}
	}
	targetNode, found := findNode(frame.after.Nodes, target)
	if !found {
		return undecidable("安装后 TargetFollower 不可观察", 1)
	}
	snapshotIndex, snapshotOK := semanticUint(targetNode.Semantic["snapshot_index"])
	appliedIndex, appliedOK := semanticUint(targetNode.Semantic["applied"])
	if !snapshotOK || !appliedOK || appliedIndex < snapshotIndex {
		return predicateResult{distance: evalDistance{
			value: 1, explanation: "安装事件存在，但 Observation storage 边界尚未确认", decidable: false,
			reason: "target snapshot_index/applied unavailable or inconsistent",
		}}
	}
	leaderNode, leaderFound := currentUniqueLeader(frame.after.Nodes)
	safe := !leaderFound || committedPrefixSafe(leaderNode, targetNode)
	if !safe {
		return predicateResult{distance: distance, invalid: "snapshot install produced committed-prefix conflict"}
	}
	distance.value, distance.explanation = 0, "对应 Snapshot 已安装，storage 与 committed prefix 合理"
	evidence := Evidence{
		Kind: "snapshot-installed", WaypointID: "W7", StepIndex: frame.stepIndex,
		PlanActionIndex: frame.planActionIndex, ModelEventIndex: frame.modelEventIndex,
		MessageIDs: []core.MessageID{e.snapshotMessage},
		Details: map[string]string{
			"snapshot_index": strconv.FormatUint(snapshotIndex, 10),
			"applied":        strconv.FormatUint(appliedIndex, 10),
		},
	}
	return predicateResult{
		satisfied: true, distance: distance, evidence: []Evidence{evidence},
		messageIDs:  []core.MessageID{e.snapshotMessage},
		facet:       map[string]string{"snapshot": "installed", "recovery": "advanced"},
		interaction: map[string]string{"snapshot_recovery": "installed×recovery-advanced"},
	}
}

func (e *Evaluator) targetCrashed(frame evalFrame) predicateResult {
	_, target, ok := e.boundPair()
	if !ok {
		return undecidable("TargetFollower 尚未绑定", 1)
	}
	node, found := findNode(frame.after.Nodes, target)
	satisfied := found && node.Status == core.NodeCrashed && frame.action.Kind == core.ActionCrash &&
		frame.action.Node == target && runningCount(frame.after.Nodes) >= len(frame.after.Nodes)/2+1
	distance := evalDistance{value: 1, explanation: "TargetFollower 尚未真实 Crash", decidable: found}
	if satisfied {
		distance.value, distance.explanation = 0, "TargetFollower 已 Crash，其余 active 节点仍达到 quorum"
		beforeNode, _ := findNode(frame.before.Nodes, target)
		e.crashTerm, _ = semanticUint(beforeNode.Semantic["term"])
	}
	return predicateResult{
		satisfied: satisfied, distance: distance,
		evidence: conditionalEvidence(satisfied, actionEvidence(
			"crash", "W2", frame, nil,
			map[string]string{"target": target.String(), "crash_term": strconv.FormatUint(e.crashTerm, 10)})),
		facet: map[string]string{"recovery": "node-crashed"},
	}
}

func (e *Evaluator) clusterTermAdvanced(frame evalFrame) predicateResult {
	_, target, ok := e.boundPair()
	if !ok {
		return undecidable("TargetFollower 尚未绑定", 2)
	}
	targetNode, found := findNode(frame.after.Nodes, target)
	if !found || targetNode.Status != core.NodeCrashed {
		return predicateResult{distance: evalDistance{
			value: 2, explanation: "TargetFollower 不再处于 crashed", decidable: true,
		}, invalid: "target left crashed state before cluster term advance"}
	}
	maxTerm := maxRunningTerm(frame.after.Nodes)
	gap := uint64(0)
	if maxTerm > e.crashTerm {
		gap = maxTerm - e.crashTerm
	}
	if frame.modelEvent != nil &&
		(frame.modelEvent.Name == "Timeout" || frame.modelEvent.Name == "BecomeLeader") {
		e.termAdvanceSeen = true
	}
	for _, effect := range frame.effects {
		if effect.Kind == core.EffectTimerFired {
			e.termAdvanceSeen = true
		}
	}
	satisfied := gap > 0 && e.termAdvanceSeen
	distance := evalDistance{decidable: true}
	switch {
	case satisfied:
		distance.value, distance.explanation = 0, "目标离线期间活动集群 term 已真实推进"
	case gap > 0:
		distance.value, distance.explanation = 1, "已有相对 term gap，但尚缺 Timeout/Leader transition evidence"
	default:
		distance.value, distance.explanation = 2, "活动集群尚未超过 crash 前 target term"
	}
	return predicateResult{
		satisfied: satisfied, distance: distance,
		evidence: conditionalEvidence(satisfied, Evidence{
			Kind: "term-advance", WaypointID: "W3", StepIndex: frame.stepIndex,
			PlanActionIndex: frame.planActionIndex, ModelEventIndex: frame.modelEventIndex,
			ModelEvent: eventName(frame.modelEvent),
			Details: map[string]string{
				"crash_term":      strconv.FormatUint(e.crashTerm, 10),
				"active_max_term": strconv.FormatUint(maxTerm, 10),
				"gap_bucket":      lagBucketText(gap),
			},
		}),
		facet: map[string]string{"election": "term-gap=" + lagBucketText(gap)},
	}
}

func (e *Evaluator) targetRestarted(frame evalFrame) predicateResult {
	_, target, ok := e.boundPair()
	if !ok {
		return undecidable("TargetFollower 尚未绑定", 2)
	}
	node, found := findNode(frame.after.Nodes, target)
	restarted := found && node.Status == core.NodeRunning &&
		frame.action.Kind == core.ActionRestart && frame.action.Node == target
	if !restarted {
		return predicateResult{distance: evalDistance{
			value: 2, explanation: "TargetFollower 尚未真实 Restart", decidable: found,
		}}
	}
	leader, leaderFound := currentUniqueLeader(frame.after.Nodes)
	incomplete := !leaderFound || recoveryIncomplete(leader, node)
	if !incomplete {
		return predicateResult{
			distance: evalDistance{value: 1, explanation: "Restart 后已完成追赶", decidable: true},
			invalid:  "target completed recovery at restart; required ordering cannot be satisfied",
		}
	}
	distance := evalDistance{value: 0, explanation: "TargetFollower 已 Restart 且 term/log/commit 尚未完全追赶", decidable: true}
	return predicateResult{
		satisfied: true, distance: distance,
		evidence: []Evidence{actionEvidence("restart", "W4", frame, nil,
			map[string]string{"target": target.String(), "recovery_incomplete": "true"})},
		facet: map[string]string{"recovery": "restarted-waiting-catch-up"},
	}
}

func (e *Evaluator) higherTermPending(frame evalFrame) predicateResult {
	_, target, ok := e.boundPair()
	if !ok {
		return undecidable("TargetFollower 尚未绑定", 4)
	}
	targetNode, found := findNode(frame.after.Nodes, target)
	if !found || targetNode.Status != core.NodeRunning {
		return undecidable("TargetFollower 当前不运行", 4)
	}
	leader, leaderFound := currentUniqueLeader(frame.after.Nodes)
	if leaderFound && !recoveryIncomplete(leader, targetNode) {
		return predicateResult{
			distance: evalDistance{value: 4, explanation: "TargetFollower 已完成恢复", decidable: true},
			invalid:  "target recovered before a higher-term message became pending",
		}
	}
	targetTerm, termOK := semanticUint(targetNode.Semantic["term"])
	if !termOK {
		return undecidable("TargetFollower term 不可观察", 4)
	}
	var best core.MessageObservation
	bestRelation := "none"
	for _, message := range frame.after.Messages {
		if message.To != target || !protocolTermMessage(message.TypeHint) {
			continue
		}
		messageTerm, err := strconv.ParseUint(message.Metadata["term"], 10, 64)
		if err != nil {
			continue
		}
		relation := termRelation(messageTerm, targetTerm)
		if relation == "higher" && (!best.ID.Valid() || message.ID < best.ID) {
			best, bestRelation = message, relation
		} else if !best.ID.Valid() {
			bestRelation = relation
		}
	}
	distance := evalDistance{decidable: true}
	if best.ID.Valid() {
		distance.value, distance.explanation = 0, "队列中存在发往 TargetFollower 的 higher-term 协议消息"
		e.higherMessage = best.ID
		evidence := Evidence{
			Kind: "message-pending", WaypointID: "W5", StepIndex: frame.stepIndex,
			PlanActionIndex: frame.planActionIndex, ModelEventIndex: frame.modelEventIndex,
			MessageIDs: []core.MessageID{best.ID},
			Details: map[string]string{
				"type": best.TypeHint, "sender": best.From.String(),
				"receiver": best.To.String(), "term_relation": "higher",
			},
		}
		return predicateResult{
			satisfied: true, distance: distance, evidence: []Evidence{evidence},
			messageIDs:  []core.MessageID{best.ID},
			facet:       map[string]string{"recovery": "higher-term-message-pending"},
			interaction: map[string]string{"recovery_term_relation": "recovering×higher"},
		}
	}
	switch bestRelation {
	case "same":
		distance.value, distance.explanation = 2, "只有 same-term 协议消息"
	case "stale":
		distance.value, distance.explanation = 3, "只有 stale-term 协议消息"
	default:
		distance.value, distance.explanation = 4, "尚无可靠 term 的目标协议消息"
	}
	return predicateResult{distance: distance}
}

func (e *Evaluator) higherTermDelivered(frame evalFrame) predicateResult {
	_, target, ok := e.boundPair()
	if !ok || !e.higherMessage.Valid() {
		return undecidable("尚无绑定的 higher-term MessageID", 2)
	}
	delivered := frame.delivered != nil && frame.action.Kind == core.ActionDeliver &&
		frame.delivered.ID == e.higherMessage && frame.delivered.To == target &&
		protocolTermMessage(frame.delivered.TypeHint)
	if !delivered {
		return predicateResult{distance: evalDistance{
			value: 1, explanation: "higher-term MessageID 已绑定但尚未投递", decidable: true,
		}, messageIDs: []core.MessageID{e.higherMessage}}
	}
	beforeNode, beforeOK := findNode(frame.before.Nodes, target)
	afterNode, afterOK := findNode(frame.after.Nodes, target)
	if !beforeOK || !afterOK {
		return undecidable("投递前后 TargetFollower 不可观察", 1)
	}
	leader, leaderFound := currentUniqueLeader(frame.before.Nodes)
	if leaderFound && !recoveryIncomplete(leader, beforeNode) {
		return predicateResult{
			distance: evalDistance{value: 1, explanation: "消息投递前 TargetFollower 已完成恢复", decidable: true},
			invalid:  "higher-term message was delivered only after recovery completed",
		}
	}
	beforeTerm, beforeTermOK := semanticUint(beforeNode.Semantic["term"])
	afterTerm, afterTermOK := semanticUint(afterNode.Semantic["term"])
	messageTerm, messageErr := strconv.ParseUint(frame.delivered.Metadata["term"], 10, 64)
	if !beforeTermOK || !afterTermOK || messageErr != nil {
		return undecidable("投递前后或消息 term 不可观察", 1)
	}
	if messageTerm <= beforeTerm {
		return predicateResult{
			distance: evalDistance{value: 1, explanation: "指定消息不再是 higher-term", decidable: true},
			invalid:  "bound message is not higher-term at delivery",
		}
	}
	if afterTerm < beforeTerm || afterTerm < messageTerm {
		return predicateResult{
			distance: evalDistance{value: 1, explanation: "消息处理后的 term 非法", decidable: true},
			invalid:  "term regression or failure to advance after higher-term delivery",
		}
	}
	distance := evalDistance{value: 0, explanation: "指定 higher-term MessageID 已投递，目标 term 正确前进", decidable: true}
	evidence := actionEvidence("higher-term-message-delivered", "W6", frame,
		[]core.MessageID{e.higherMessage}, map[string]string{
			"type":         frame.delivered.TypeHint,
			"term_before":  strconv.FormatUint(beforeTerm, 10),
			"message_term": strconv.FormatUint(messageTerm, 10),
			"term_after":   strconv.FormatUint(afterTerm, 10),
		})
	return predicateResult{
		satisfied: true, distance: distance, evidence: []Evidence{evidence},
		messageIDs:  []core.MessageID{e.higherMessage},
		facet:       map[string]string{"recovery": "higher-term-message-processed"},
		interaction: map[string]string{"recovery_term_relation": "recovering×higher-delivered"},
	}
}

type ArtifactInput struct {
	Definition   BehaviorGoalDefinition
	InstanceID   string
	DistanceMode DistanceMode
	ModelConfig  raftmodel.Config
	Initial      core.Observation
	Trace        core.Trace
	ModelEvents  []model.Event
	Resolutions  []plan.Resolution
}

func Recompute(input ArtifactInput) (EvaluationResult, error) {
	if err := input.Trace.Validate(); err != nil {
		return EvaluationResult{}, fmt.Errorf("offline goal trace: %w", err)
	}
	mapper, err := raftmodel.NewMapperWithConfig(input.ModelConfig)
	if err != nil {
		return EvaluationResult{}, err
	}
	planIndices := make([]int, 0, len(input.Trace.Steps))
	for planIndex, resolution := range input.Resolutions {
		if err := resolution.Validate(); err != nil {
			return EvaluationResult{}, fmt.Errorf("offline resolution %d: %w", planIndex, err)
		}
		for range resolution.Actions {
			planIndices = append(planIndices, planIndex)
		}
	}
	if len(planIndices) != len(input.Trace.Steps) {
		return EvaluationResult{}, fmt.Errorf(
			"offline goal alignment has %d concrete resolution actions and %d trace steps",
			len(planIndices), len(input.Trace.Steps))
	}
	distanceMode := input.DistanceMode
	if distanceMode == "" {
		distanceMode = DistanceStaged
	}
	evaluator, err := NewEvaluatorWithDistance(
		input.Definition, input.InstanceID, false, distanceMode,
	)
	if err != nil {
		return EvaluationResult{}, err
	}
	if err := evaluator.Reset(input.Initial); err != nil {
		return EvaluationResult{}, err
	}
	eventCursor := 0
	for stepIndex, record := range input.Trace.Steps {
		transition, err := model.TransitionFromRecord(record)
		if err != nil {
			return EvaluationResult{}, fmt.Errorf("offline goal step %d: %w", stepIndex, err)
		}
		events, err := mapper.Map(transition)
		if err != nil {
			// Mapping-failure experiments intentionally end at a concrete action
			// that cannot be represented by the model. The online evaluator never
			// observes that failed step, so return the successfully evaluated
			// prefix together with the mapping error for an exact prefix comparison.
			return evaluator.Result(), fmt.Errorf("offline goal step %d map: %w", stepIndex, err)
		}
		if eventCursor+len(events) > len(input.ModelEvents) {
			return EvaluationResult{}, fmt.Errorf("offline goal step %d emits beyond persisted events", stepIndex)
		}
		for offset, event := range events {
			if !sameEvent(event, input.ModelEvents[eventCursor+offset]) {
				return EvaluationResult{}, fmt.Errorf(
					"offline goal step %d model event %d differs from persisted artifact",
					stepIndex, eventCursor+offset)
			}
		}
		before := core.Observation{Time: record.TimeBefore, Nodes: copyNodes(record.NodesBefore)}
		after := core.Observation{Time: record.TimeAfter, Nodes: copyNodes(record.NodesAfter)}
		if err := evaluator.Observe(engine.PrefixStep{
			PlanActionIndex: planIndices[stepIndex], ActionIndex: stepIndex,
			ConcreteActionIndex: concreteActionOffset(input.Resolutions, planIndices[stepIndex], stepIndex),
			Before:              before, Record: record.Copy(), After: after, ModelEvents: events,
		}); err != nil {
			return EvaluationResult{}, fmt.Errorf("offline goal step %d evaluate: %w", stepIndex, err)
		}
		eventCursor += len(events)
	}
	if eventCursor != len(input.ModelEvents) {
		return EvaluationResult{}, fmt.Errorf(
			"offline goal consumed %d/%d model events", eventCursor, len(input.ModelEvents))
	}
	return evaluator.Result(), nil
}

func concreteActionOffset(resolutions []plan.Resolution, planIndex, traceIndex int) int {
	before := 0
	for index := 0; index < planIndex; index++ {
		before += len(resolutions[index].Actions)
	}
	return traceIndex - before
}

type trackedMessage struct {
	message    core.Message
	enqueuedAt core.LogicalTime
	delayed    bool
}

type prefixTracker struct {
	queues       map[core.LinkID][]trackedMessage
	partition    *core.NetworkPartition
	maxMessageID core.MessageID
	beforeQueues map[core.LinkID][]trackedMessage
	beforePart   *core.NetworkPartition
}

func newPrefixTracker(initial core.Observation) (*prefixTracker, error) {
	tracker := &prefixTracker{queues: make(map[core.LinkID][]trackedMessage)}
	if initial.NetworkPartition != nil {
		partition := initial.NetworkPartition.Copy()
		tracker.partition = &partition
	}
	for _, observed := range initial.Messages {
		message := core.Message{
			ID: observed.ID, From: observed.From, To: observed.To,
			SenderEpoch: observed.SenderEpoch, Sequence: observed.LinkSequence,
			ParentID: observed.ParentID, TypeHint: observed.TypeHint,
			PayloadDigest: observed.PayloadDigest, Metadata: cloneStringMap(observed.Metadata),
		}
		if err := message.Validate(); err != nil {
			return nil, fmt.Errorf("goal initial message: %w", err)
		}
		tracker.enqueue(message, observed.EnqueuedAt, false)
	}
	return tracker, nil
}

func (t *prefixTracker) apply(record core.StepRecord) (*core.Message, bool, error) {
	t.beforeQueues = copyQueues(t.queues)
	t.beforePart = copyPartition(t.partition)
	var delivered *core.Message
	justHealed := false
	action := record.Action
	switch action.Kind {
	case core.ActionDeliver, core.ActionDrop:
		message, err := t.remove(action)
		if err != nil {
			return nil, false, err
		}
		delivered = &message
	case core.ActionDuplicate:
		message, err := t.resolve(action, t.queues)
		if err != nil {
			return nil, false, err
		}
		t.maxMessageID++
		duplicate := message.Copy()
		duplicate.ID = t.maxMessageID
		duplicate.ParentID = message.ID
		duplicate.Sequence = maxLinkSequence(t.queues[duplicate.Link()]) + 1
		t.enqueue(duplicate, record.TimeAfter, false)
	case core.ActionPartition:
		if action.Partition == nil {
			return nil, false, fmt.Errorf("goal partition action has no partition")
		}
		partition := action.Partition.Copy()
		t.partition = &partition
	case core.ActionHeal:
		if t.partition == nil {
			return nil, false, fmt.Errorf("goal heal has no active partition")
		}
		for link, queue := range t.queues {
			if !t.partition.Blocks(link) {
				continue
			}
			for index := range queue {
				queue[index].delayed = true
			}
			t.queues[link] = queue
		}
		t.partition = nil
		justHealed = true
	}
	for _, effect := range record.Effects {
		if effect.Kind == core.EffectSendMessage && effect.Message != nil {
			t.enqueue(effect.Message.Copy(), effect.At, false)
		}
	}
	return delivered, justHealed, nil
}

func (t *prefixTracker) resolve(
	action core.Action, queues map[core.LinkID][]trackedMessage,
) (core.Message, error) {
	if action.Selector == nil {
		return core.Message{}, fmt.Errorf("goal message action has no selector")
	}
	queue := queues[action.Selector.Link]
	if action.Selector.Position < 0 || action.Selector.Position >= len(queue) {
		return core.Message{}, fmt.Errorf("goal message selector position is unavailable")
	}
	message := queue[action.Selector.Position].message
	if message.ID != action.Message {
		return core.Message{}, fmt.Errorf(
			"goal message selector resolved %s want %s", message.ID, action.Message)
	}
	return message.Copy(), nil
}

func (t *prefixTracker) remove(action core.Action) (core.Message, error) {
	message, err := t.resolve(action, t.queues)
	if err != nil {
		return core.Message{}, err
	}
	link := action.Selector.Link
	queue := t.queues[link]
	queue = append(queue[:action.Selector.Position], queue[action.Selector.Position+1:]...)
	if len(queue) == 0 {
		delete(t.queues, link)
	} else {
		t.queues[link] = queue
	}
	return message, nil
}

func (t *prefixTracker) enqueue(message core.Message, at core.LogicalTime, delayed bool) {
	if message.ID > t.maxMessageID {
		t.maxMessageID = message.ID
	}
	link := message.Link()
	t.queues[link] = append(t.queues[link], trackedMessage{
		message: message.Copy(), enqueuedAt: at, delayed: delayed,
	})
}

func (t *prefixTracker) observation(
	at core.LogicalTime, nodes []core.NodeObservation, action *core.Action,
) core.Observation {
	return trackerObservation(at, nodes, action, t.queues, t.partition)
}

func (t *prefixTracker) beforeObservation(
	at core.LogicalTime, nodes []core.NodeObservation, action *core.Action, _ *core.Message,
) core.Observation {
	return trackerObservation(at, nodes, action, t.beforeQueues, t.beforePart)
}

func trackerObservation(
	at core.LogicalTime, nodes []core.NodeObservation, action *core.Action,
	queues map[core.LinkID][]trackedMessage, partition *core.NetworkPartition,
) core.Observation {
	observation := core.Observation{Time: at, Nodes: copyNodes(nodes)}
	links := make([]core.LinkID, 0, len(queues))
	for link := range queues {
		links = append(links, link)
	}
	sort.Slice(links, func(i, j int) bool {
		if links[i].From != links[j].From {
			return links[i].From < links[j].From
		}
		return links[i].To < links[j].To
	})
	for _, link := range links {
		for position, queued := range queues[link] {
			message := queued.message
			blocked := partition != nil && partition.Blocks(link)
			observation.Messages = append(observation.Messages, core.MessageObservation{
				ID: message.ID, From: message.From, To: message.To,
				SenderEpoch: message.SenderEpoch, LinkSequence: message.Sequence,
				ParentID: message.ParentID, Position: position, EnqueuedAt: queued.enqueuedAt,
				TypeHint: message.TypeHint, PayloadDigest: message.PayloadDigest,
				Metadata: cloneStringMap(message.Metadata), Blocked: blocked,
			})
		}
	}
	if partition != nil {
		copy := partition.Copy()
		observation.NetworkPartition = &copy
	}
	if action != nil {
		copy := action.Copy()
		observation.LastAction = &copy
	}
	return observation
}

func snapshotCatchUpRequired(
	leader, target core.NodeObservation, messages []core.MessageObservation,
) (bool, string, bool) {
	first, firstOK := semanticUint(leader.Semantic["first_index"])
	progress, progressOK := leader.Semantic["leader_progress"].(map[string]any)
	if !progressOK {
		if typed, ok := leader.Semantic["leader_progress"].(map[string]map[string]any); ok {
			progress = make(map[string]any, len(typed))
			for key, value := range typed {
				progress[key] = value
			}
			progressOK = true
		}
	}
	if firstOK && progressOK {
		// Adapter leader_progress uses decimal Raft IDs ("3"), whereas
		// core.NodeID.String is the human-facing form ("n3").
		entry, exists := progress[strconv.FormatUint(uint64(target.ID), 10)]
		fields, ok := entry.(map[string]any)
		if exists && ok {
			next, nextOK := semanticUint(fields["next"])
			pending, pendingOK := semanticUint(fields["pending_snapshot"])
			state, _ := fields["state"].(string)
			if pendingOK && pending > 0 {
				return true, "leader progress has pending_snapshot", true
			}
			if state == "StateSnapshot" {
				return true, "leader progress is StateSnapshot", true
			}
			if nextOK && next < first {
				return true, "leader next_index is behind first_index", true
			}
			if nextOK {
				return false, "leader next_index remains within retained log", true
			}
		}
	}
	for _, message := range messages {
		if message.To == target.ID && message.TypeHint == "MsgSnap" {
			return true, "runtime queue contains MsgSnap for target", true
		}
	}
	if !firstOK || !progressOK {
		return false, "leader first_index or leader_progress is unavailable", false
	}
	return false, "target leader progress entry is unavailable", false
}

func snapshotBoundaryDistance(
	leader, target core.NodeObservation,
	lagBucket string,
	lagOrdinal int,
) (int, string) {
	first, firstOK := semanticUint(leader.Semantic["first_index"])
	next, nextOK := leaderProgressUint(leader, target.ID, "next")
	switch {
	case firstOK && nextOK && next == first:
		return 1, "Leader next_index 已到压缩边界，下一次拒绝可能要求 Snapshot"
	case firstOK && nextOK && next == first+1:
		return 2, "Leader next_index 接近压缩边界"
	case lagOrdinal >= 3:
		return 3, "lag=large，但 next_index 尚未越过压缩边界"
	case lagOrdinal == 2:
		return 4, "lag=small，普通 AppendEntries 仍可追赶"
	case lagOrdinal == 1:
		return 5, "lag=one，距离 Snapshot 边界较远"
	default:
		return 6, fmt.Sprintf("lag=%s，Follower 尚未形成有效落后", lagBucket)
	}
}

func leaderProgressUint(
	leader core.NodeObservation,
	target core.NodeID,
	field string,
) (uint64, bool) {
	progress, ok := leader.Semantic["leader_progress"].(map[string]any)
	if !ok {
		if typed, typedOK := leader.Semantic["leader_progress"].(map[string]map[string]any); typedOK {
			progress = make(map[string]any, len(typed))
			for key, value := range typed {
				progress[key] = value
			}
			ok = true
		}
	}
	if !ok {
		return 0, false
	}
	entry, ok := progress[strconv.FormatUint(uint64(target), 10)]
	if !ok {
		return 0, false
	}
	fields, ok := entry.(map[string]any)
	if !ok {
		return 0, false
	}
	return semanticUint(fields[field])
}

func recoveryIncomplete(leader, target core.NodeObservation) bool {
	leaderTerm, leaderTermOK := semanticUint(leader.Semantic["term"])
	targetTerm, targetTermOK := semanticUint(target.Semantic["term"])
	leaderLast, leaderLastOK := semanticUint(leader.Semantic["last_index"])
	targetLast, targetLastOK := semanticUint(target.Semantic["last_index"])
	leaderCommit, leaderCommitOK := semanticUint(leader.Semantic["commit"])
	targetCommit, targetCommitOK := semanticUint(target.Semantic["commit"])
	if !leaderTermOK || !targetTermOK || !leaderLastOK || !targetLastOK ||
		!leaderCommitOK || !targetCommitOK {
		return true
	}
	return targetTerm < leaderTerm || targetLast < leaderLast || targetCommit < leaderCommit
}

func committedPrefixSafe(left, right core.NodeObservation) bool {
	leftCommit, leftOK := semanticUint(left.Semantic["commit"])
	rightCommit, rightOK := semanticUint(right.Semantic["commit"])
	if !leftOK || !rightOK {
		return false
	}
	index := min(leftCommit, rightCommit)
	if index == 0 {
		return true
	}
	leftDigest, leftFound := prefixDigest(left.Semantic["committed_prefix_digests"], index)
	rightDigest, rightFound := prefixDigest(right.Semantic["committed_prefix_digests"], index)
	return leftFound && rightFound && leftDigest == rightDigest
}

func prefixDigest(value any, index uint64) (string, bool) {
	key := strconv.FormatUint(index, 10)
	switch values := value.(type) {
	case map[string]string:
		result, ok := values[key]
		return result, ok
	case map[string]any:
		result, ok := values[key].(string)
		return result, ok
	default:
		return "", false
	}
}

func leaderHasConnectedQuorum(observation core.Observation, leader core.NodeID) bool {
	quorum := len(observation.Nodes)/2 + 1
	if observation.NetworkPartition == nil {
		return runningCount(observation.Nodes) >= quorum
	}
	for _, group := range observation.NetworkPartition.Groups {
		containsLeader, active := false, 0
		for _, id := range group {
			node, found := findNode(observation.Nodes, id)
			if !found || node.Status != core.NodeRunning {
				continue
			}
			active++
			if id == leader {
				containsLeader = true
			}
		}
		if containsLeader {
			return active >= quorum
		}
	}
	return false
}

func currentUniqueLeader(nodes []core.NodeObservation) (core.NodeObservation, bool) {
	leaders := runningRoleNodes(nodes, "leader")
	if len(leaders) != 1 {
		return core.NodeObservation{}, false
	}
	return leaders[0], true
}

func runningRoleNodes(nodes []core.NodeObservation, role string) []core.NodeObservation {
	result := make([]core.NodeObservation, 0)
	for _, node := range nodes {
		if node.Status == core.NodeRunning && node.Semantic["role"] == role {
			result = append(result, node.Copy())
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}

func runningNonLeaderNodes(nodes []core.NodeObservation, leader core.NodeID) []core.NodeObservation {
	result := make([]core.NodeObservation, 0)
	for _, node := range nodes {
		if node.Status == core.NodeRunning && node.ID != leader &&
			node.Semantic["role"] != "leader" {
			result = append(result, node.Copy())
		}
	}
	return result
}

func nodeRunningNonLeader(observation core.Observation, id core.NodeID) bool {
	node, found := findNode(observation.Nodes, id)
	return found && node.Status == core.NodeRunning && node.Semantic["role"] != "leader"
}

func findNode(nodes []core.NodeObservation, id core.NodeID) (core.NodeObservation, bool) {
	for _, node := range nodes {
		if node.ID == id {
			return node.Copy(), true
		}
	}
	return core.NodeObservation{}, false
}

func runningCount(nodes []core.NodeObservation) int {
	result := 0
	for _, node := range nodes {
		if node.Status == core.NodeRunning {
			result++
		}
	}
	return result
}

func maxRunningTerm(nodes []core.NodeObservation) uint64 {
	var maximum uint64
	for _, node := range nodes {
		if node.Status != core.NodeRunning {
			continue
		}
		term, ok := semanticUint(node.Semantic["term"])
		if ok && term > maximum {
			maximum = term
		}
	}
	return maximum
}

func semanticUint(value any) (uint64, bool) {
	switch value := value.(type) {
	case uint64:
		return value, true
	case uint32:
		return uint64(value), true
	case int:
		return uint64(value), value >= 0
	case float64:
		return uint64(value), value >= 0 && value == float64(uint64(value))
	case json.Number:
		parsed, err := strconv.ParseUint(value.String(), 10, 64)
		return parsed, err == nil
	default:
		return 0, false
	}
}

func lagClass(lag uint64) (string, int) {
	switch {
	case lag == 0:
		return "zero", 0
	case lag == 1:
		return "one", 1
	case lag <= raftmodel.PrototypeLagSmallMax:
		return "small", 2
	default:
		return "large", 3
	}
}

func lagBucketText(value uint64) string {
	bucket, _ := lagClass(value)
	return bucket
}

func protocolTermMessage(typeHint string) bool {
	switch typeHint {
	case "MsgApp", "MsgHeartbeat", "MsgVote", "MsgVoteResp", "MsgAppResp", "MsgHeartbeatResp", "MsgSnap":
		return true
	default:
		return false
	}
}

func termRelation(message, node uint64) string {
	switch {
	case message < node:
		return "stale"
	case message > node:
		return "higher"
	default:
		return "same"
	}
}

func (e *Evaluator) boundPair() (core.NodeID, core.NodeID, bool) {
	leader, leaderOK := e.instance.Bindings[SymbolLeader]
	target, targetOK := e.instance.Bindings[SymbolTargetFollower]
	return leader.Node, target.Node, leaderOK && targetOK
}

func actionEvidence(
	kind, waypoint string, frame evalFrame, messages []core.MessageID, details map[string]string,
) Evidence {
	return Evidence{
		Kind: kind, WaypointID: waypoint, StepIndex: frame.stepIndex,
		PlanActionIndex: frame.planActionIndex, ModelEventIndex: frame.modelEventIndex,
		ActionKind: frame.action.Kind, MessageIDs: append([]core.MessageID(nil), messages...),
		Details: cloneStringMap(details),
	}
}

func conditionalEvidence(condition bool, evidence Evidence) []Evidence {
	if !condition {
		return nil
	}
	return []Evidence{evidence}
}

func undecidable(reason string, distance int) predicateResult {
	return predicateResult{distance: evalDistance{
		value: distance, explanation: "当前信息不足，无法可靠判断", reason: reason,
	}}
}

func evidenceStrength(instance GoalInstance) int {
	strength := len(instance.EventEvidence)
	for _, result := range instance.WaypointResults {
		strength += len(result.RelatedMessageIDs)
	}
	return strength
}

func electionFacetSummary(observation core.Observation) map[string]string {
	leaders := len(runningRoleNodes(observation.Nodes, "leader"))
	candidates := len(runningRoleNodes(observation.Nodes, "candidate"))
	return map[string]string{
		"election": fmt.Sprintf("leaders=%d,candidates=%d,quorum=%t",
			leaders, candidates, runningCount(observation.Nodes) >= len(observation.Nodes)/2+1),
	}
}

func networkFacetSummary(observation core.Observation, healed bool) map[string]string {
	mode := "no-partition"
	if observation.NetworkPartition != nil {
		mode = fmt.Sprintf("partition-groups=%d", len(observation.NetworkPartition.Groups))
	} else if healed {
		mode = "healed"
	}
	return map[string]string{"network": mode}
}

func firstMessage(
	messages []core.MessageObservation, predicate func(core.MessageObservation) bool,
) core.MessageObservation {
	copy := append([]core.MessageObservation(nil), messages...)
	sort.Slice(copy, func(i, j int) bool { return copy[i].ID < copy[j].ID })
	for _, message := range copy {
		if predicate(message) {
			return message
		}
	}
	return core.MessageObservation{}
}

func messageObservationByID(
	messages []core.MessageObservation, id core.MessageID,
) core.MessageObservation {
	if !id.Valid() {
		return core.MessageObservation{}
	}
	return firstMessage(messages, func(message core.MessageObservation) bool {
		return message.ID == id
	})
}

func uniqueMessageIDs(values []core.MessageID) []core.MessageID {
	seen := make(map[core.MessageID]struct{}, len(values))
	result := make([]core.MessageID, 0, len(values))
	for _, value := range values {
		if !value.Valid() {
			continue
		}
		if _, duplicate := seen[value]; duplicate {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result
}

func sameEvent(left, right model.Event) bool {
	leftJSON, leftErr := json.Marshal(left)
	rightJSON, rightErr := json.Marshal(right)
	return leftErr == nil && rightErr == nil && bytes.Equal(leftJSON, rightJSON)
}

func eventName(event *model.Event) string {
	if event == nil {
		return ""
	}
	return event.Name
}

func copyNodes(nodes []core.NodeObservation) []core.NodeObservation {
	result := make([]core.NodeObservation, len(nodes))
	for index, node := range nodes {
		result[index] = node.Copy()
	}
	return result
}

func copyEffects(effects []core.Effect) []core.Effect {
	result := make([]core.Effect, len(effects))
	for index, effect := range effects {
		result[index] = effect.Copy()
	}
	return result
}

func copyMessagePointer(message *core.Message) *core.Message {
	if message == nil {
		return nil
	}
	copy := message.Copy()
	return &copy
}

func copyPartition(partition *core.NetworkPartition) *core.NetworkPartition {
	if partition == nil {
		return nil
	}
	copy := partition.Copy()
	return &copy
}

func copyQueues(values map[core.LinkID][]trackedMessage) map[core.LinkID][]trackedMessage {
	result := make(map[core.LinkID][]trackedMessage, len(values))
	for link, queue := range values {
		copied := make([]trackedMessage, len(queue))
		for index, message := range queue {
			copied[index] = trackedMessage{
				message: message.message.Copy(), enqueuedAt: message.enqueuedAt, delayed: message.delayed,
			}
		}
		result[link] = copied
	}
	return result
}

func maxLinkSequence(messages []trackedMessage) uint64 {
	var maximum uint64
	for _, message := range messages {
		if message.message.Sequence > maximum {
			maximum = message.message.Sequence
		}
	}
	return maximum
}

func cloneStringMap(values map[string]string) map[string]string {
	if values == nil {
		return nil
	}
	result := make(map[string]string, len(values))
	for key, value := range values {
		result[key] = value
	}
	return result
}

func copyInstance(instance GoalInstance) GoalInstance {
	encoded, _ := json.Marshal(instance)
	var result GoalInstance
	_ = json.Unmarshal(encoded, &result)
	return result
}
