// Package raft implements protocol-aware local mutation advice for the two
// frozen Raft Behavior Goals. It does not own search, Frontier, replay, runtime,
// or Goal evaluation.
package raft

import (
	"fmt"
	"sort"
	"strconv"

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/core"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/plan"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/protocolmutation"
)

const (
	AdvisorID = "raft-focused-v1"
	GoalA     = "snapshot-catchup-after-partition"
	GoalB     = "restart-then-higher-term-message"
)

type Ablation string

const (
	AblationNone                Ablation = "none"
	AblationNoQuorumMaintenance Ablation = "no-quorum-maintenance"
	AblationNoBoundaryAwareness Ablation = "no-boundary-awareness"
	AblationNoTargetSuppression Ablation = "no-target-suppression"
	AblationNoLogFreshness      Ablation = "no-log-freshness"
	AblationNoVoteCompletion    Ablation = "no-vote-completion"
	AblationEarlyRestart        Ablation = "early-restart"
)

type Config struct {
	GoalAEnabled       bool     `json:"focused_goal_a"`
	GoalBEnabled       bool     `json:"focused_goal_b"`
	PriorityMultiplier int      `json:"priority_multiplier"`
	LocalActionCap     int      `json:"local_action_cap"`
	NoProgressCap      int      `json:"no_progress_cap"`
	QueueLimit         int      `json:"queue_limit"`
	Ablation           Ablation `json:"ablation"`
}

func (c Config) Validate() error {
	if c.PriorityMultiplier <= 0 || c.LocalActionCap <= 0 ||
		c.NoProgressCap <= 0 || c.QueueLimit <= 0 {
		return fmt.Errorf("focused advisor limits and multiplier must be positive")
	}
	switch c.Ablation {
	case AblationNone, AblationNoQuorumMaintenance, AblationNoBoundaryAwareness,
		AblationNoTargetSuppression, AblationNoLogFreshness,
		AblationNoVoteCompletion, AblationEarlyRestart:
		return nil
	default:
		return fmt.Errorf("unknown focused mutation ablation %q", c.Ablation)
	}
}

type Advisor struct{ config Config }

func New(config Config) (*Advisor, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}
	return &Advisor{config: config}, nil
}

func (a *Advisor) ID() string { return AdvisorID }

func (a *Advisor) Advise(request protocolmutation.Request) (protocolmutation.Decision, error) {
	switch request.GoalID {
	case GoalA:
		if !a.config.GoalAEnabled {
			return protocolmutation.Decision{}, fmt.Errorf("focused Goal A is disabled")
		}
		return a.goalA(request)
	case GoalB:
		if !a.config.GoalBEnabled {
			return protocolmutation.Decision{}, fmt.Errorf("focused Goal B is disabled")
		}
		return a.goalB(request)
	default:
		return protocolmutation.Decision{}, fmt.Errorf("Raft advisor does not support goal %q", request.GoalID)
	}
}

func (a *Advisor) goalA(request protocolmutation.Request) (protocolmutation.Decision, error) {
	leader := role(request, "Leader")
	target := role(request, "TargetFollower")
	if !leader.Valid() {
		leader = observedLeader(request.Observation)
	}
	if !target.Valid() && leader.Valid() {
		target = chooseFollower(request.Observation, leader)
	}
	stage := "A0-establish-stable-leader"
	if leader.Valid() {
		stage = "A1-isolate-target"
	}
	if request.Observation.NetworkPartition != nil {
		stage = "A2-majority-progress"
	}
	if snapshotRequired(request.Observation, leader, target) {
		stage = "A5-snapshot-required-return-to-frontier"
	}
	decision := protocolmutation.NewDecision(AdvisorID, stage, request)
	decision.Preconditions = []string{
		"current observation only", "selected action is legal",
		"no future MessageID is assumed",
	}

	if stage == "A5-snapshot-required-return-to-frontier" &&
		a.config.Ablation != AblationNoBoundaryAwareness {
		return a.fallback(request, decision, "snapshot-boundary-reached",
			"已达到 snapshot-required 边界，停止局部脚本并交还 Standard Frontier")
	}
	if request.NoProgressCount >= a.config.NoProgressCap {
		return a.fallback(request, decision, "local-no-progress-cap",
			"局部建议连续无进展，退回普通弱变异")
	}
	if !leader.Valid() {
		return a.electionNudge(request, decision, 0)
	}
	if request.Observation.NetworkPartition == nil {
		if !target.Valid() {
			return a.fallback(request, decision, "no-target-role", "尚无可隔离的运行 follower")
		}
		return a.finish(request, decision, candidateForAction(
			"isolate-target", partitionTarget(request.Observation, target), "",
			leader, target, "target-isolation", "隔离语义角色 TargetFollower",
			"target becomes partitioned", a.config.PriorityMultiplier,
		))
	}

	// A majority response is the highest-value next action: it commits the
	// finite request before another request is introduced.
	if a.config.Ablation != AblationNoQuorumMaintenance {
		if message, ok := selectMessage(request.Observation, func(message core.MessageObservation) bool {
			return !message.Blocked && message.To == leader && message.From != target &&
				(message.TypeHint == "MsgAppResp" || message.TypeHint == "MsgHeartbeatResp")
		}); ok {
			return a.finishMessage(request, decision, "maintain-majority-response", message,
				"quorum-response", "交付活跃多数派对 leader 的真实响应",
				"leader replication/commit progress advances")
		}
		if message, ok := selectMessage(request.Observation, func(message core.MessageObservation) bool {
			return !message.Blocked && message.From == leader && message.To != target &&
				message.TypeHint == "MsgApp"
		}); ok {
			candidate := candidateForAction(
				"maintain-majority-append", messageAction(plan.ActionDeliver, message),
				message.TypeHint, message.From, message.To, "quorum-append",
				"交付 leader 到活跃多数派的真实 MsgApp，并接收同链路反向响应",
				"active follower append and leader progress advance",
				a.config.PriorityMultiplier,
			)
			candidate.MessageID = message.ID
			peer := message.To
			candidate.Actions = []plan.PlanAction{
				candidate.Action,
				linkAction(plan.ActionDeliver, message.To, message.From, 0, 8),
				{Kind: plan.ActionRequest, Node: leader, Request: "1"},
				linkAction(plan.ActionDeliver, leader, peer, 0, 8),
				linkAction(plan.ActionDeliver, peer, leader, 0, 8),
				{Kind: plan.ActionRequest, Node: leader, Request: "1"},
				linkAction(plan.ActionDeliver, leader, peer, 0, 8),
				linkAction(plan.ActionDeliver, peer, leader, 0, 8),
			}
			return a.finish(request, decision, candidate)
		}
	}
	if len(request.Observation.Messages) >= a.config.QueueLimit {
		if message, ok := selectMessage(request.Observation, func(message core.MessageObservation) bool {
			return !message.Blocked && message.From != target && message.To != target
		}); ok {
			return a.finishMessage(request, decision, "bounded-queue-drain", message,
				"queue-pressure", "队列达到上限，只清理活跃多数派的真实消息",
				"queue pressure decreases without target catch-up")
		}
	}
	candidate := candidateForAction(
		"finite-leader-request",
		plan.PlanAction{Kind: plan.ActionRequest, Node: leader, Request: "1"}, "",
		leader, target, "finite-request", "向当前 leader 注入一条有限请求",
		"leader creates a new log entry for majority replication", a.config.PriorityMultiplier,
	)
	if peer := activePeer(request.Observation, leader, target); peer.Valid() &&
		a.config.Ablation != AblationNoQuorumMaintenance {
		candidate.Actions = []plan.PlanAction{
			candidate.Action,
			linkAction(plan.ActionDeliver, leader, peer, 0, 8),
			linkAction(plan.ActionDeliver, peer, leader, 0, 8),
			{Kind: plan.ActionRequest, Node: leader, Request: "1"},
			linkAction(plan.ActionDeliver, leader, peer, 0, 8),
			linkAction(plan.ActionDeliver, peer, leader, 0, 8),
			{Kind: plan.ActionRequest, Node: leader, Request: "1"},
			linkAction(plan.ActionDeliver, leader, peer, 0, 8),
			linkAction(plan.ActionDeliver, peer, leader, 0, 8),
		}
	}
	return a.finish(request, decision, candidate)
}

func (a *Advisor) goalB(request protocolmutation.Request) (protocolmutation.Decision, error) {
	leader := role(request, "Leader")
	target := role(request, "TargetFollower")
	if !leader.Valid() {
		leader = observedLeader(request.Observation)
	}
	if !target.Valid() && leader.Valid() {
		target = chooseFollower(request.Observation, leader)
	}
	stage := "B0-establish-stable-leader"
	if leader.Valid() {
		stage = "B1-crash-target"
	}
	if target.Valid() && status(request.Observation, target) == core.NodeCrashed {
		stage = "B2-select-log-fresh-active-candidate"
	}
	maxTerm := maximumTerm(request.Observation)
	if candidate := candidateAtTerm(request.Observation, target, maxTerm); candidate.Valid() {
		stage = "B3-complete-real-vote"
	}
	if current := leaderAtTerm(request.Observation, maxTerm); current.Valid() &&
		target.Valid() && status(request.Observation, target) == core.NodeCrashed &&
		maxTerm > nodeTerm(request.Observation, target) {
		stage = "B5-restart-after-election"
	}
	if target.Valid() && status(request.Observation, target) == core.NodeRunning &&
		request.WaypointIndex >= 4 {
		stage = "B6-return-to-goal-frontier"
	}
	decision := protocolmutation.NewDecision(AdvisorID, stage, request)
	decision.Preconditions = []string{
		"current observation only", "candidate belongs to active subset",
		"election uses actual MsgVote/MsgVoteResp",
	}
	if request.NoProgressCount >= a.config.NoProgressCap {
		return a.fallback(request, decision, "local-no-progress-cap",
			"局部建议连续无进展，退回普通弱变异")
	}
	if !leader.Valid() && stage == "B0-establish-stable-leader" {
		return a.electionNudge(request, decision, target)
	}
	if !target.Valid() {
		return a.fallback(request, decision, "no-target-role", "尚无可崩溃的运行 follower")
	}
	if status(request.Observation, target) == core.NodeRunning && request.WaypointIndex <= 1 {
		return a.finish(request, decision, candidateForAction(
			"crash-target", plan.PlanAction{Kind: plan.ActionCrash, Node: target}, "",
			leader, target, "target-crash", "崩溃语义角色 TargetFollower",
			"target leaves the active subset", a.config.PriorityMultiplier,
		))
	}
	if status(request.Observation, target) == core.NodeCrashed {
		if a.config.Ablation == AblationEarlyRestart {
			return a.restart(request, decision, target, "early-restart-ablation")
		}
		if vote, ok := selectMessage(request.Observation, func(message core.MessageObservation) bool {
			return !message.Blocked && message.From != target && message.To != target &&
				messageTerm(message) == maxTerm && maxTerm > nodeTerm(request.Observation, target) &&
				(message.TypeHint == "MsgVote" ||
					(message.TypeHint == "MsgVoteResp" &&
						semanticRole(request.Observation, message.To) == "candidate"))
		}); ok && a.config.Ablation != AblationNoVoteCompletion {
			candidate := candidateForAction(
				"complete-active-election", messageAction(plan.ActionDeliver, vote),
				vote.TypeHint, vote.From, vote.To, "vote-completion",
				"按队列中的真实投票因果顺序完成选举，之后才重启目标",
				"active-subset election completes before target restart",
				a.config.PriorityMultiplier,
			)
			candidate.MessageID = vote.ID
			candidate.Actions = []plan.PlanAction{candidate.Action}
			if vote.TypeHint == "MsgVote" {
				candidate.Actions = append(candidate.Actions,
					linkAction(plan.ActionDeliver, vote.To, vote.From, 0, 8))
			}
			candidate.Actions = append(candidate.Actions,
				plan.PlanAction{Kind: plan.ActionRestart, Node: target})
			return a.finish(request, decision, candidate)
		}
		if newLeader := leaderAtTerm(request.Observation, maxTerm); newLeader.Valid() &&
			nodeTerm(request.Observation, target) < maxTerm {
			return a.restart(request, decision, target, "restart-after-election")
		}
		candidate := logFreshCandidate(request.Observation, target, leader,
			a.config.Ablation != AblationNoLogFreshness)
		if candidate.Valid() {
			if leader.Valid() && candidate != leader && a.config.Ablation != AblationNoLogFreshness {
				if message, ok := selectMessage(request.Observation, func(message core.MessageObservation) bool {
					return !message.Blocked && message.From == leader && message.To == candidate &&
						message.TypeHint == "MsgApp"
				}); ok {
					preparation := candidateForAction(
						"prepare-log-fresh-candidate", messageAction(plan.ActionDeliver, message),
						message.TypeHint, message.From, message.To, "log-freshness",
						"先完成候选者日志追赶，再对该活跃 follower 触发真实超时",
						"candidate becomes log-fresh before entering a higher term",
						a.config.PriorityMultiplier,
					)
					preparation.MessageID = message.ID
					preparation.Actions = []plan.PlanAction{
						preparation.Action,
						linkAction(plan.ActionDeliver, candidate, leader, 0, 8),
						{Kind: plan.ActionTimeout, Node: candidate},
					}
					return a.finish(request, decision, preparation)
				}
			}
			return a.finish(request, decision, candidateForAction(
				"real-active-timeout", plan.PlanAction{Kind: plan.ActionTimeout, Node: candidate}, "",
				candidate, target, "active-timeout", "对日志足够新的活跃节点触发真实选举超时",
				"active candidate enters a higher term and emits MsgVote",
				a.config.PriorityMultiplier,
			))
		}
		return a.fallback(request, decision, "no-eligible-active-candidate",
			"当前活跃子集中没有满足条件的候选者")
	}
	// Once the target has restarted, focused mutation has completed its local
	// purpose; the frozen Goal logic chooses and delivers the final higher-term
	// message.
	return a.fallback(request, decision, "target-already-restarted",
		"目标已在真实选举完成后重启，交还普通 Goal/Frontier 逻辑")
}

func (a *Advisor) electionNudge(request protocolmutation.Request, decision protocolmutation.Decision, excluded core.NodeID) (protocolmutation.Decision, error) {
	if message, ok := selectMessage(request.Observation, func(message core.MessageObservation) bool {
		return !message.Blocked && message.To != excluded &&
			(message.TypeHint == "MsgVote" || message.TypeHint == "MsgVoteResp")
	}); ok {
		return a.finishMessage(request, decision, "establish-leader-vote", message,
			"real-election-message", "交付当前队列里的真实投票消息", "leader election advances")
	}
	candidate := logFreshCandidate(request.Observation, excluded, 0, true)
	if !candidate.Valid() {
		return a.fallback(request, decision, "no-running-candidate", "没有可触发超时的运行节点")
	}
	return a.finish(request, decision, candidateForAction(
		"establish-leader-timeout", plan.PlanAction{Kind: plan.ActionTimeout, Node: candidate}, "",
		candidate, excluded, "real-election-timeout", "触发日志最新活跃节点的真实超时",
		"candidate emits real vote requests", a.config.PriorityMultiplier,
	))
}

func (a *Advisor) restart(request protocolmutation.Request, decision protocolmutation.Decision, target core.NodeID, code string) (protocolmutation.Decision, error) {
	return a.finish(request, decision, candidateForAction(
		"restart-target", plan.PlanAction{Kind: plan.ActionRestart, Node: target}, "",
		0, target, code, "仅在活跃子集完成选举后重启目标节点",
		"target returns while higher-term traffic remains protocol-generated",
		a.config.PriorityMultiplier,
	))
}

func (a *Advisor) fallback(request protocolmutation.Request, decision protocolmutation.Decision, code, reason string) (protocolmutation.Decision, error) {
	decision.Fallback = code
	action := plan.PlanAction{Kind: plan.ActionAdvanceTicks, Ticks: 1}
	return a.finish(request, decision, candidateForAction(
		"return-to-legacy", action, "", 0, 0, code, reason,
		"ordinary Frontier mutation resumes", 1,
	))
}

func (a *Advisor) finishMessage(request protocolmutation.Request, decision protocolmutation.Decision, class string, message core.MessageObservation, code, reason, expected string) (protocolmutation.Decision, error) {
	return a.finishMessageKind(request, decision, plan.ActionDeliver, class, message, code, reason, expected)
}

func (a *Advisor) finishMessageKind(request protocolmutation.Request, decision protocolmutation.Decision, kind plan.ActionKind, class string, message core.MessageObservation, code, reason, expected string) (protocolmutation.Decision, error) {
	candidate := candidateForAction(class, messageAction(kind, message), message.TypeHint,
		message.From, message.To, code, reason, expected, a.config.PriorityMultiplier)
	candidate.MessageID = message.ID
	return a.finish(request, decision, candidate)
}

func (a *Advisor) finish(request protocolmutation.Request, decision protocolmutation.Decision, candidate protocolmutation.Candidate) (protocolmutation.Decision, error) {
	actions := protocolmutation.EffectiveActions(candidate)
	if len(actions) > a.config.LocalActionCap {
		actions = actions[:a.config.LocalActionCap]
		candidate.Actions = actions
	}
	for _, action := range actions {
		if !protocolmutation.ActionAllowed(request, action.Kind) {
			return protocolmutation.Decision{}, fmt.Errorf("advisor selected disallowed action %s", action.Kind)
		}
	}
	decision.Candidates = append(decision.Candidates, candidate)
	decision.RecommendedClasses = append(decision.RecommendedClasses, candidate.Class)
	if len(decision.Candidates) > a.config.LocalActionCap {
		decision.Candidates = decision.Candidates[:a.config.LocalActionCap]
	}
	if err := protocolmutation.FinishDecision(&decision); err != nil {
		return protocolmutation.Decision{}, err
	}
	return decision, nil
}

func candidateForAction(class string, action plan.PlanAction, messageType string, from, to core.NodeID, code, reason, expected string, weight int) protocolmutation.Candidate {
	roles := make(map[string]core.NodeID)
	if from.Valid() {
		roles["source"] = from
	}
	if to.Valid() {
		roles["target"] = to
	}
	return protocolmutation.Candidate{
		Class: class, Action: action, MessageType: messageType, From: from, To: to,
		Roles: roles, Weight: weight, ReasonCode: code, Reason: reason,
		ExpectedEffect: expected,
	}
}

func messageAction(kind plan.ActionKind, message core.MessageObservation) plan.PlanAction {
	return plan.PlanAction{Kind: kind, Messages: &plan.MessageRangeSelector{
		Link:  core.LinkID{From: message.From, To: message.To},
		Start: message.Position, Count: 1,
	}}
}

func linkAction(kind plan.ActionKind, from, to core.NodeID, start, count int) plan.PlanAction {
	return plan.PlanAction{Kind: kind, Messages: &plan.MessageRangeSelector{
		Link: core.LinkID{From: from, To: to}, Start: start, Count: count,
	}}
}

func activePeer(observation core.Observation, leader, target core.NodeID) core.NodeID {
	for _, node := range observation.Nodes {
		if node.ID != leader && node.ID != target && node.Status == core.NodeRunning {
			return node.ID
		}
	}
	return 0
}

func partitionTarget(observation core.Observation, target core.NodeID) plan.PlanAction {
	active := make([]core.NodeID, 0, len(observation.Nodes)-1)
	for _, node := range observation.Nodes {
		if node.ID != target {
			active = append(active, node.ID)
		}
	}
	return plan.PlanAction{Kind: plan.ActionPartition, Partition: &core.NetworkPartition{
		Groups: [][]core.NodeID{active, {target}},
	}}
}

func selectMessage(observation core.Observation, predicate func(core.MessageObservation) bool) (core.MessageObservation, bool) {
	messages := append([]core.MessageObservation(nil), observation.Messages...)
	sort.Slice(messages, func(i, j int) bool {
		if messages[i].From != messages[j].From {
			return messages[i].From < messages[j].From
		}
		if messages[i].To != messages[j].To {
			return messages[i].To < messages[j].To
		}
		return messages[i].Position < messages[j].Position
	})
	for _, message := range messages {
		if predicate(message) {
			return message, true
		}
	}
	return core.MessageObservation{}, false
}

func role(request protocolmutation.Request, name string) core.NodeID { return request.Roles[name] }

func status(observation core.Observation, id core.NodeID) core.NodeStatus {
	for _, node := range observation.Nodes {
		if node.ID == id {
			return node.Status
		}
	}
	return ""
}

func observedLeader(observation core.Observation) core.NodeID {
	return leaderAtTerm(observation, maximumTerm(observation))
}

func leaderAtTerm(observation core.Observation, term uint64) core.NodeID {
	for _, node := range observation.Nodes {
		if node.Status == core.NodeRunning && semanticString(node, "role") == "leader" &&
			semanticUint(node, "term") == term {
			return node.ID
		}
	}
	return 0
}

func candidateAtTerm(observation core.Observation, excluded core.NodeID, term uint64) core.NodeID {
	for _, node := range observation.Nodes {
		if node.ID != excluded && node.Status == core.NodeRunning &&
			semanticString(node, "role") == "candidate" && semanticUint(node, "term") == term {
			return node.ID
		}
	}
	return 0
}

func maximumTerm(observation core.Observation) uint64 {
	var result uint64
	for _, node := range observation.Nodes {
		result = max(result, semanticUint(node, "term"))
	}
	return result
}

func nodeTerm(observation core.Observation, id core.NodeID) uint64 {
	for _, node := range observation.Nodes {
		if node.ID == id {
			return semanticUint(node, "term")
		}
	}
	return 0
}

func semanticRole(observation core.Observation, id core.NodeID) string {
	for _, node := range observation.Nodes {
		if node.ID == id {
			return semanticString(node, "role")
		}
	}
	return ""
}

func messageTerm(message core.MessageObservation) uint64 {
	value, err := strconv.ParseUint(message.Metadata["term"], 10, 64)
	if err != nil {
		return 0
	}
	return value
}

func chooseFollower(observation core.Observation, leader core.NodeID) core.NodeID {
	var candidates []core.NodeObservation
	for _, node := range observation.Nodes {
		if node.ID != leader && node.Status == core.NodeRunning {
			candidates = append(candidates, node)
		}
	}
	sort.Slice(candidates, func(i, j int) bool {
		left, right := semanticUint(candidates[i], "last_index"), semanticUint(candidates[j], "last_index")
		if left != right {
			return left < right
		}
		return candidates[i].ID > candidates[j].ID
	})
	if len(candidates) == 0 {
		return 0
	}
	return candidates[0].ID
}

func logFreshCandidate(observation core.Observation, excluded, currentLeader core.NodeID, enforce bool) core.NodeID {
	var candidates []core.NodeObservation
	for _, node := range observation.Nodes {
		if node.ID != excluded && node.ID != currentLeader && node.Status == core.NodeRunning {
			candidates = append(candidates, node)
		}
	}
	if len(candidates) == 0 && currentLeader.Valid() && currentLeader != excluded {
		for _, node := range observation.Nodes {
			if node.ID == currentLeader && node.Status == core.NodeRunning {
				candidates = append(candidates, node)
			}
		}
	}
	sort.Slice(candidates, func(i, j int) bool {
		if !enforce {
			return candidates[i].ID < candidates[j].ID
		}
		leftTerm, rightTerm := semanticUint(candidates[i], "last_term"), semanticUint(candidates[j], "last_term")
		if leftTerm != rightTerm {
			return leftTerm > rightTerm
		}
		leftIndex, rightIndex := semanticUint(candidates[i], "last_index"), semanticUint(candidates[j], "last_index")
		if leftIndex != rightIndex {
			return leftIndex > rightIndex
		}
		// Prefer a follower so that the active subset really performs a new election.
		if candidates[i].ID == currentLeader {
			return false
		}
		if candidates[j].ID == currentLeader {
			return true
		}
		return candidates[i].ID < candidates[j].ID
	})
	if len(candidates) == 0 {
		return 0
	}
	return candidates[0].ID
}

func snapshotRequired(observation core.Observation, leader, target core.NodeID) bool {
	if !leader.Valid() || !target.Valid() {
		return false
	}
	var leaderNode, targetNode *core.NodeObservation
	for index := range observation.Nodes {
		switch observation.Nodes[index].ID {
		case leader:
			leaderNode = &observation.Nodes[index]
		case target:
			targetNode = &observation.Nodes[index]
		}
	}
	if leaderNode == nil || targetNode == nil {
		return false
	}
	first := semanticUint(*leaderNode, "first_index")
	targetLast := semanticUint(*targetNode, "last_index")
	if first > 0 && targetLast+1 < first {
		return true
	}
	progress, ok := leaderNode.Semantic["leader_progress"].(map[string]any)
	if !ok {
		return false
	}
	raw, ok := progress[strconv.FormatUint(uint64(target), 10)].(map[string]any)
	if !ok {
		return false
	}
	return anyUint(raw["pending_snapshot"]) > 0
}

func semanticUint(node core.NodeObservation, key string) uint64 { return anyUint(node.Semantic[key]) }
func semanticString(node core.NodeObservation, key string) string {
	value, _ := node.Semantic[key].(string)
	return value
}
func anyUint(value any) uint64 {
	switch typed := value.(type) {
	case uint64:
		return typed
	case int:
		if typed >= 0 {
			return uint64(typed)
		}
	case float64:
		if typed >= 0 {
			return uint64(typed)
		}
	}
	return 0
}
