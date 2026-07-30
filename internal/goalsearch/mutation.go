package goalsearch

import (
	"fmt"
	"math/rand"
	"sort"
	"strconv"

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/core"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/plan"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/protocolmutation"
)

type SearchMode string

const (
	ModeUnguided          SearchMode = "unguided-local-mutation"
	ModeGoalAware         SearchMode = "goal-aware-operators-only"
	ModeFrontier          SearchMode = "waypoint-frontier"
	ModeDiversityFrontier SearchMode = "diversity-aware-frontier"
	ModeEvidenceFrontier  SearchMode = "evidence-aware-frontier"
	ModeFrontierNoPrefix  SearchMode = "frontier-no-prefix-preservation"
	ModeDirectedSnapshot  SearchMode = "snapshot-directed-reference"
)

func (m SearchMode) Validate() error {
	switch m {
	case ModeUnguided, ModeGoalAware, ModeFrontier, ModeDiversityFrontier, ModeEvidenceFrontier,
		ModeFrontierNoPrefix, ModeDirectedSnapshot:
		return nil
	default:
		return fmt.Errorf("unknown goal-search mode %q", m)
	}
}

type HintStrength string

const (
	HintNone   HintStrength = "none"
	HintWeak   HintStrength = "weak"
	HintStrong HintStrength = "strong"
)

func (s HintStrength) Validate() error {
	switch s {
	case HintNone, HintWeak, HintStrong:
		return nil
	default:
		return fmt.Errorf("unknown goal hint strength %q", s)
	}
}

type MutationOptions struct {
	HintStrength               HintStrength
	PreservePrefix             bool
	AllowWholePlanMutation     bool
	PlannedBranch              *BehaviorBranchTemplate
	EvidencePriorityMultiplier int
	Advisor                    protocolmutation.Advisor
	AdvisorCandidateIndex      int
	AdvisorNoProgressCount     int
	AdvisorRecordOnly          bool
}

type MutationStats struct {
	Attempts           int                  `json:"attempts"`
	Produced           int                  `json:"produced"`
	RejectedMaxActions int                  `json:"rejected_max_actions"`
	RejectedNoAction   int                  `json:"rejected_no_action"`
	WholePlanEdits     int                  `json:"whole_plan_edits"`
	ExactMessageUses   int                  `json:"exact_message_uses"`
	HintStrengthUses   map[HintStrength]int `json:"hint_strength_uses"`
	Operators          map[string]int       `json:"operators"`
}

type GoalMutation struct {
	Plan                  plan.PlanSequence          `json:"plan"`
	Operator              string                     `json:"operator"`
	PreservedPrefixLength int                        `json:"preserved_prefix_length"`
	AdvisorDecision       *protocolmutation.Decision `json:"advisor_decision,omitempty"`
}

// InitialPlan is deliberately goal-neutral. Every search mode starts with this
// same deterministic message-draining/election/request skeleton.
func InitialPlan(nodeIDs []core.NodeID, maxActions int) (plan.PlanSequence, error) {
	if len(nodeIDs) < 3 || maxActions <= 0 {
		return plan.PlanSequence{}, fmt.Errorf("initial plan needs at least three nodes and a positive action budget")
	}
	nodes := append([]core.NodeID(nil), nodeIDs...)
	sort.Slice(nodes, func(i, j int) bool { return nodes[i] < nodes[j] })
	actions := []plan.PlanAction{
		{Kind: plan.ActionTimeout, Node: nodes[0]},
	}
	// Two delivery sweeps are sufficient to resolve vote traffic without
	// assuming which protocol message appears at which queue position.
	for sweep := 0; sweep < 2; sweep++ {
		for _, from := range nodes {
			for _, to := range nodes {
				if from == to {
					continue
				}
				selector := &plan.MessageRangeSelector{
					Link: core.LinkID{From: from, To: to}, Start: 0, Count: 8,
				}
				actions = append(actions, plan.PlanAction{Kind: plan.ActionDeliver, Messages: selector})
			}
		}
	}
	actions = append(actions,
		plan.PlanAction{Kind: plan.ActionRequest, Node: nodes[0], Request: "1"},
		plan.PlanAction{Kind: plan.ActionAdvanceTicks, Ticks: 1},
	)
	if len(actions) > maxActions {
		actions = actions[:maxActions]
	}
	sequence := plan.PlanSequence{
		Actions:  actions,
		Metadata: map[string]string{"source": "goal-search-common-initial"},
	}
	return sequence, sequence.Validate()
}

// MutateTowardWaypoint appends a small local operator chosen from the current
// concrete observation and manually declared waypoint hints. It does not
// encode an entire successful trace.
func MutateTowardWaypoint(
	definition BehaviorGoalDefinition,
	parent plan.PlanSequence,
	evaluation EvaluationResult,
	seed int64,
	maxActions int,
	preservePrefix bool,
) (GoalMutation, MutationStats, error) {
	return MutateTowardWaypointWithOptions(
		definition, parent, evaluation, seed, maxActions,
		MutationOptions{HintStrength: HintStrong, PreservePrefix: preservePrefix},
	)
}

func MutateTowardWaypointWithOptions(
	definition BehaviorGoalDefinition,
	parent plan.PlanSequence,
	evaluation EvaluationResult,
	seed int64,
	maxActions int,
	options MutationOptions,
) (GoalMutation, MutationStats, error) {
	stats := MutationStats{
		Attempts: 1, Operators: make(map[string]int),
		HintStrengthUses: make(map[HintStrength]int),
	}
	if err := options.HintStrength.Validate(); err != nil {
		return GoalMutation{}, stats, err
	}
	if options.PreservePrefix && options.AllowWholePlanMutation {
		return GoalMutation{}, stats, fmt.Errorf("whole-plan mutation cannot preserve a prefix")
	}
	if maxActions <= 0 {
		return GoalMutation{}, stats, fmt.Errorf("max actions must be positive")
	}
	base := parent.Copy()
	preserved := 0
	if options.PreservePrefix {
		if evaluation.PrefixEndActionIndex < 0 ||
			evaluation.PrefixEndActionIndex >= len(parent.Actions) {
			return GoalMutation{}, stats, fmt.Errorf("frontier mutation has no valid progress prefix")
		}
		base.Actions = base.Actions[:evaluation.PrefixEndActionIndex+1]
		preserved = len(base.Actions)
	}
	if len(base.Actions) >= maxActions {
		stats.RejectedMaxActions++
		return GoalMutation{}, stats, fmt.Errorf("parent plan already reaches max actions")
	}
	current := evaluation.Instance.Progress.CurrentWaypointIndex
	random := rand.New(rand.NewSource(seed))
	if options.AllowWholePlanMutation && len(base.Actions) > 1 {
		index := random.Intn(len(base.Actions))
		base.Actions = append(base.Actions[:index], base.Actions[index+1:]...)
		stats.WholePlanEdits++
	}
	var actions []plan.PlanAction
	var operator string
	var advisorDecision *protocolmutation.Decision
	if options.Advisor != nil && options.HintStrength == HintWeak &&
		current >= 0 && current < len(definition.Waypoints) {
		roles := make(map[string]core.NodeID)
		for symbol, binding := range evaluation.Instance.Bindings {
			roles[string(symbol)] = binding.Node
		}
		decision, advisorErr := options.Advisor.Advise(protocolmutation.Request{
			GoalID:        string(definition.GoalID),
			Waypoint:      definition.Waypoints[current].ID,
			WaypointIndex: current,
			Observation:   evaluation.FinalObservation,
			Roles:         roles,
			AllowedActions: []plan.ActionKind{
				plan.ActionDeliver, plan.ActionDrop, plan.ActionDuplicate,
				plan.ActionAdvanceTicks, plan.ActionTimeout, plan.ActionCrash,
				plan.ActionRestart, plan.ActionRequest, plan.ActionPartition,
				plan.ActionHeal,
			},
			CandidateIndex:  options.AdvisorCandidateIndex,
			NoProgressCount: options.AdvisorNoProgressCount,
		})
		if advisorErr != nil {
			return GoalMutation{}, stats, fmt.Errorf("mutation advisor: %w", advisorErr)
		}
		advisorDecision = &decision
		if decision.Fallback == "" && !options.AdvisorRecordOnly {
			actions = protocolmutation.EffectiveActions(decision.Selected)
			operator = "focused-" + decision.Selected.Class
		}
	}
	switch {
	case len(actions) > 0:
		// The protocol-aware Advisor contributes one bounded local action.
	case current >= len(definition.Waypoints):
		actions, operator = weightedCategoryAction(
			BehaviorGoalDefinition{}, -1, EvaluationResult{},
			evaluation.FinalObservation, random, false, nil, 1,
		)
		operator = "post-goal-" + operator
	case options.HintStrength == HintStrong:
		actions, operator = waypointActions(
			definition.GoalID, current, evaluation, evaluation.FinalObservation, random,
			options.PlannedBranch, base.Metadata,
		)
	case options.HintStrength == HintWeak:
		priorityMultiplier := 1
		if options.EvidencePriorityMultiplier > 1 {
			priorityMultiplier = options.EvidencePriorityMultiplier
		}
		actions, operator = weightedCategoryAction(
			definition, current, evaluation, evaluation.FinalObservation, random, true,
			options.PlannedBranch, priorityMultiplier,
		)
	case options.HintStrength == HintNone:
		actions, operator = weightedCategoryAction(
			BehaviorGoalDefinition{}, -1, EvaluationResult{},
			evaluation.FinalObservation, random, false, nil, 1,
		)
	}
	if len(actions) == 0 {
		stats.RejectedNoAction++
		return GoalMutation{}, stats, fmt.Errorf("no applicable goal-aware action for waypoint %d", current)
	}
	remaining := maxActions - len(base.Actions)
	if len(actions) > remaining {
		actions = actions[:remaining]
	}
	for _, action := range actions {
		if err := action.Validate(); err != nil {
			return GoalMutation{}, stats, fmt.Errorf("goal-aware operator %s: %w", operator, err)
		}
		base.Actions = append(base.Actions, action.Copy())
	}
	if base.Metadata == nil {
		base.Metadata = make(map[string]string)
	}
	base.Metadata["source"] = "local-category-mutation"
	if options.HintStrength != HintNone {
		base.Metadata["source"] = "goal-aware-local"
		base.Metadata["goal_id"] = string(definition.GoalID)
		if current < len(definition.Waypoints) {
			base.Metadata["waypoint"] = definition.Waypoints[current].ID
		} else {
			base.Metadata["waypoint"] = "target-reached"
		}
	} else {
		delete(base.Metadata, "goal_id")
		delete(base.Metadata, "waypoint")
	}
	base.Metadata["hint_strength"] = string(options.HintStrength)
	base.Metadata["mutation_operator"] = operator
	if advisorDecision != nil {
		base.Metadata["mutation_advisor"] = advisorDecision.AdvisorID
		base.Metadata["mutation_advisor_stage"] = advisorDecision.LocalStage
		base.Metadata["mutation_advisor_key"] = advisorDecision.StableKey
		if advisorDecision.Fallback != "" {
			base.Metadata["mutation_advisor_fallback"] = advisorDecision.Fallback
		}
	}
	if options.PlannedBranch != nil {
		base.Metadata["planned_branch"] = string(options.PlannedBranch.BranchTemplateID)
		base.Metadata["branch_schema"] = BranchSchemaVersion
	}
	if operator == "drop-first-snapshot-for-retry" {
		base.Metadata["branch_snapshot_failure_injected"] = "true"
	}
	base.Metadata["mutation_seed"] = strconv.FormatInt(seed, 10)
	base.Metadata["preserved_prefix_length"] = strconv.Itoa(preserved)
	stats.Produced++
	stats.HintStrengthUses[options.HintStrength]++
	stats.Operators[operator]++
	if options.HintStrength == HintStrong &&
		(operator == "deliver-exact-snapshot" ||
			operator == "deliver-bound-higher-term-message") {
		stats.ExactMessageUses++
	}
	return GoalMutation{
		Plan: base, Operator: operator, PreservedPrefixLength: preserved,
		AdvisorDecision: advisorDecision,
	}, stats, nil
}

type weightedAction struct {
	action      plan.PlanAction
	kind        plan.ActionKind
	messageType string
	weight      int
}

// weightedCategoryAction is the weak/none control. It samples a feasible
// action after changing only action-category and message-category weights.
// It never looks at, stores, or compares a MessageID. The resulting Plan must
// still contain a queue position because that is the public scheduling API.
func weightedCategoryAction(
	definition BehaviorGoalDefinition,
	waypoint int,
	evaluation EvaluationResult,
	observation core.Observation,
	random *rand.Rand,
	useHints bool,
	branch *BehaviorBranchTemplate,
	priorityMultiplier int,
) ([]plan.PlanAction, string) {
	priorityMultiplier = max(1, priorityMultiplier)
	actionWeights := make(map[plan.ActionKind]int)
	messageWeights := make(map[string]int)
	if useHints && waypoint >= 0 && waypoint < len(definition.Waypoints) {
		waypointID := definition.Waypoints[waypoint].ID
		for _, hint := range definition.RecommendedMutationHints {
			if hint.WaypointID != waypointID {
				continue
			}
			for _, kind := range hint.RecommendedActions {
				actionWeights[kind] = 4 * priorityMultiplier
			}
			for _, messageType := range hint.RecommendedMessageType {
				messageWeights[messageType] = 3 * priorityMultiplier
			}
		}
	}
	if useHints && branch != nil {
		for _, preference := range branch.MutationPreferences {
			if preference.ActionKind != "" {
				actionWeights[plan.ActionKind(preference.ActionKind)] =
					max(actionWeights[plan.ActionKind(preference.ActionKind)], preference.Weight)
			}
			if preference.MessageType != "" {
				messageWeights[preference.MessageType] =
					max(messageWeights[preference.MessageType], preference.Weight)
			}
		}
	}
	weight := func(kind plan.ActionKind) int {
		if value := actionWeights[kind]; value > 0 {
			return value
		}
		return 1
	}
	candidates := make([]weightedAction, 0)
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
		if message.Blocked {
			continue
		}
		messageWeight := 1
		if value := messageWeights[message.TypeHint]; value > 0 {
			messageWeight = value
		}
		candidates = append(candidates, weightedAction{
			action: deliverMessage(message), kind: plan.ActionDeliver,
			messageType: message.TypeHint, weight: weight(plan.ActionDeliver) * messageWeight,
		})
		if branch != nil && branchAllowsMessageAction(
			*branch, plan.ActionDrop, message, evaluation.Instance.Bindings,
		) {
			candidates = append(candidates, weightedAction{
				action: dropMessage(message), kind: plan.ActionDrop,
				messageType: message.TypeHint,
				weight:      weight(plan.ActionDrop) * messageWeight,
			})
		}
	}
	running := runningNodes(observation)
	for _, node := range running {
		candidates = append(candidates,
			weightedAction{
				action: plan.PlanAction{Kind: plan.ActionTimeout, Node: node},
				kind:   plan.ActionTimeout, weight: weight(plan.ActionTimeout),
			},
			weightedAction{
				action: plan.PlanAction{Kind: plan.ActionRequest, Node: node, Request: "1"},
				kind:   plan.ActionRequest, weight: weight(plan.ActionRequest),
			},
		)
		if len(running) > len(observation.Nodes)/2 {
			candidates = append(candidates, weightedAction{
				action: plan.PlanAction{Kind: plan.ActionCrash, Node: node},
				kind:   plan.ActionCrash, weight: weight(plan.ActionCrash),
			})
		}
	}
	for _, node := range crashedNodes(observation) {
		candidates = append(candidates, weightedAction{
			action: plan.PlanAction{Kind: plan.ActionRestart, Node: node},
			kind:   plan.ActionRestart, weight: weight(plan.ActionRestart),
		})
	}
	candidates = append(candidates, weightedAction{
		action: plan.PlanAction{Kind: plan.ActionAdvanceTicks, Ticks: 1},
		kind:   plan.ActionAdvanceTicks, weight: weight(plan.ActionAdvanceTicks),
	})
	if observation.NetworkPartition != nil {
		candidates = append(candidates, weightedAction{
			action: plan.PlanAction{Kind: plan.ActionHeal},
			kind:   plan.ActionHeal, weight: weight(plan.ActionHeal),
		})
	} else {
		for _, target := range running {
			candidates = append(candidates, weightedAction{
				action: partitionTarget(observation.Nodes, target),
				kind:   plan.ActionPartition, weight: weight(plan.ActionPartition),
			})
		}
	}
	total := 0
	for _, candidate := range candidates {
		total += candidate.weight
	}
	if total == 0 {
		return nil, ""
	}
	selected := random.Intn(total)
	for _, candidate := range candidates {
		if selected < candidate.weight {
			operator := "category-" + string(candidate.kind)
			if candidate.messageType != "" {
				operator += "-" + candidate.messageType
			}
			return []plan.PlanAction{candidate.action}, operator
		}
		selected -= candidate.weight
	}
	return nil, ""
}

func waypointActions(
	goal GoalID,
	waypoint int,
	evaluation EvaluationResult,
	observation core.Observation,
	random *rand.Rand,
	branch *BehaviorBranchTemplate,
	metadata map[string]string,
) ([]plan.PlanAction, string) {
	leader := boundNode(evaluation.Instance.Bindings, SymbolLeader)
	target := boundNode(evaluation.Instance.Bindings, SymbolTargetFollower)
	switch goal {
	case GoalSnapshotCatchUpAfterPartition:
		switch waypoint {
		case 0:
			return electionNudge(observation, random)
		case 1:
			if target.Valid() && observation.NetworkPartition == nil {
				return []plan.PlanAction{partitionTarget(observation.Nodes, target)}, "partition-bound-target"
			}
		case 2, 3:
			if branch != nil && branch.BranchTemplateID == BranchADropAppend {
				if message := selectMessage(observation, func(candidate core.MessageObservation) bool {
					return candidate.From == leader && candidate.To == target &&
						candidate.TypeHint == "MsgApp"
				}); message.ID.Valid() {
					return []plan.PlanAction{dropMessage(message)}, "drop-target-append"
				}
			}
			if leader.Valid() {
				return append(
					[]plan.PlanAction{{Kind: plan.ActionRequest, Node: leader, Request: "1"}},
					replicationRound(observation, leader)...,
				), "advance-majority-log"
			}
		case 4:
			if branch != nil && branch.BranchTemplateID == BranchASnapshotBeforeHeal {
				if message := selectMessage(observation, func(candidate core.MessageObservation) bool {
					return candidate.To == target && candidate.TypeHint == "MsgSnap"
				}); message.ID.Valid() {
					return []plan.PlanAction{{Kind: plan.ActionHeal}}, "heal-after-snapshot-enqueued"
				}
				return deliverTowardQuorum(observation, leader), "keep-partition-drive-snapshot"
			}
			if observation.NetworkPartition != nil {
				return []plan.PlanAction{{Kind: plan.ActionHeal}}, "heal-network"
			}
		case 5:
			if branch != nil && branch.BranchTemplateID == BranchASnapshotFailureRetry {
				if message := selectMessage(observation, func(candidate core.MessageObservation) bool {
					return candidate.To == target && candidate.TypeHint == "MsgSnap" &&
						!candidate.Blocked
				}); message.ID.Valid() {
					if metadata["branch_snapshot_failure_injected"] != "true" {
						return []plan.PlanAction{dropMessage(message)},
							"drop-first-snapshot-for-retry"
					}
					return []plan.PlanAction{deliverMessage(message)},
						"deliver-retried-snapshot"
				}
				if metadata["branch_snapshot_failure_injected"] != "true" {
					if message := selectMessage(observation, func(candidate core.MessageObservation) bool {
						return candidate.From == target && candidate.To == leader &&
							candidate.TypeHint == "MsgAppResp" && !candidate.Blocked
					}); message.ID.Valid() {
						return []plan.PlanAction{deliverMessage(message)},
							"drive-first-snapshot-for-failure"
					}
					return deliverTowardNode(observation, target),
						"prepare-first-snapshot-for-failure"
				}
				if message := selectMessage(observation, func(candidate core.MessageObservation) bool {
					return !candidate.Blocked &&
						((candidate.From == leader && candidate.To == target) ||
							(candidate.From == target && candidate.To == leader))
				}); message.ID.Valid() {
					return []plan.PlanAction{deliverMessage(message)},
						"drive-snapshot-failure-recovery"
				}
				return []plan.PlanAction{{Kind: plan.ActionAdvanceTicks, Ticks: 1}},
					"drive-snapshot-retry"
			}
			for _, id := range evaluation.Instance.WaypointResults[waypoint].RelatedMessageIDs {
				message := messageByID(observation.Messages, id)
				if message.ID.Valid() && message.To == target &&
					message.TypeHint == "MsgSnap" && !message.Blocked {
					return []plan.PlanAction{deliverMessage(message)}, "deliver-exact-snapshot"
				}
			}
			if message := selectMessage(observation, func(candidate core.MessageObservation) bool {
				return candidate.To == target && candidate.TypeHint == "MsgSnap" && !candidate.Blocked
			}); message.ID.Valid() {
				return []plan.PlanAction{deliverMessage(message)}, "deliver-exact-snapshot"
			}
			if message := selectMessage(observation, func(candidate core.MessageObservation) bool {
				return candidate.From == target && candidate.To == leader &&
					candidate.TypeHint == "MsgAppResp" && !candidate.Blocked
			}); message.ID.Valid() {
				return []plan.PlanAction{deliverMessage(message)}, "deliver-target-rejection"
			}
			return deliverTowardNode(observation, target), "drive-snapshot-generation"
		case 6:
			return deliverTowardNode(observation, target), "drive-snapshot-installation"
		}
	case GoalRestartHigherTermMessage:
		switch waypoint {
		case 0:
			return electionNudge(observation, random)
		case 1:
			if branch != nil && target.Valid() {
				if peer, needsCatchUp := branchElectionPeer(observation, leader, target); needsCatchUp {
					if message := selectMessage(observation, func(candidate core.MessageObservation) bool {
						return !candidate.Blocked &&
							((candidate.From == leader && candidate.To == peer) ||
								(candidate.From == peer && candidate.To == leader))
					}); message.ID.Valid() {
						return []plan.PlanAction{deliverMessage(message)},
							"prepare-active-election-peer"
					}
					return []plan.PlanAction{{Kind: plan.ActionAdvanceTicks, Ticks: 1}},
						"create-active-peer-catchup-message"
				}
			}
			if target.Valid() && nodeStatus(observation, target) == core.NodeRunning {
				return []plan.PlanAction{{Kind: plan.ActionCrash, Node: target}}, "crash-bound-target"
			}
		case 2:
			if node := runningNonLeaderExcept(observation, target); node.Valid() {
				actions := []plan.PlanAction{{Kind: plan.ActionTimeout, Node: node}}
				if branch != nil {
					return actions, "start-branch-term-advance"
				}
				actions = append(actions, electionDeliveryRound(observation, node, target)...)
				return actions, "advance-active-cluster-term"
			}
		case 3:
			wanted := branchKeyMessage(branch)
			if wanted != "" {
				if !observedLeader(observation).Valid() {
					maximumTerm := maxObservationTerm(observation)
					if message := selectMessage(observation, func(candidate core.MessageObservation) bool {
						messageTerm, err := strconv.ParseUint(candidate.Metadata["term"], 10, 64)
						return candidate.To != target && !candidate.Blocked &&
							err == nil && messageTerm == maximumTerm &&
							(candidate.TypeHint == "MsgVote" ||
								candidate.TypeHint == "MsgVoteResp")
					}); message.ID.Valid() {
						return []plan.PlanAction{deliverMessage(message)},
							"complete-active-election-before-restart"
					}
					if candidate := highestTermCandidate(observation, target); candidate.Valid() {
						return []plan.PlanAction{{Kind: plan.ActionTimeout, Node: candidate}},
							"retry-active-election-before-restart"
					}
					return []plan.PlanAction{{Kind: plan.ActionAdvanceTicks, Ticks: 1}},
						"wait-active-election-before-restart"
				}
				if message := selectMessage(observation, func(candidate core.MessageObservation) bool {
					return candidate.To == target && protocolTermMessage(candidate.TypeHint) &&
						branchMessageClass(candidate.TypeHint) != wanted
				}); message.ID.Valid() {
					return []plan.PlanAction{dropMessage(message)}, "drop-nonbranch-term-message"
				}
			}
			if target.Valid() && nodeStatus(observation, target) == core.NodeCrashed {
				return []plan.PlanAction{{Kind: plan.ActionRestart, Node: target}}, "restart-bound-target"
			}
		case 4:
			wanted := branchKeyMessage(branch)
			if message := selectMessage(observation, func(candidate core.MessageObservation) bool {
				return candidate.To == target && protocolTermMessage(candidate.TypeHint) &&
					!candidate.Blocked && (wanted == "" ||
					branchMessageClass(candidate.TypeHint) == wanted) &&
					messageTermRelation(observation, candidate, target) == "higher"
			}); message.ID.Valid() {
				return []plan.PlanAction{{Kind: plan.ActionAdvanceTicks, Ticks: 1}}, "hold-higher-term-message-pending"
			}
			if wanted != "" {
				if message := selectMessage(observation, func(candidate core.MessageObservation) bool {
					return candidate.To == target && protocolTermMessage(candidate.TypeHint) &&
						!candidate.Blocked
				}); message.ID.Valid() {
					return []plan.PlanAction{dropMessage(message)}, "filter-before-higher-term-message"
				}
				if wanted == "MsgApp" {
					if currentLeader := observedLeader(observation); currentLeader.Valid() {
						return []plan.PlanAction{{
							Kind: plan.ActionRequest, Node: currentLeader, Request: "1",
						}}, "create-branch-higher-term-msgapp"
					}
				}
				if wanted == "vote-message" {
					if candidate := runningNonLeaderExcept(observation, target); candidate.Valid() {
						return []plan.PlanAction{{
							Kind: plan.ActionTimeout, Node: candidate,
						}}, "create-branch-higher-term-vote"
					}
				}
				return []plan.PlanAction{{Kind: plan.ActionAdvanceTicks, Ticks: 1}},
					"create-branch-higher-term-message"
			}
			return deliverTowardNode(observation, target), "create-higher-term-message"
		case 5:
			wanted := branchKeyMessage(branch)
			ids := evaluation.Instance.WaypointResults[waypoint-1].RelatedMessageIDs
			for _, id := range ids {
				if message := messageByID(observation.Messages, id); message.ID.Valid() &&
					!message.Blocked && (wanted == "" ||
					branchMessageClass(message.TypeHint) == wanted) {
					return []plan.PlanAction{deliverMessage(message)}, "deliver-bound-higher-term-message"
				}
			}
			if message := selectMessage(observation, func(candidate core.MessageObservation) bool {
				return candidate.To == target && !candidate.Blocked &&
					(wanted == "" || branchMessageClass(candidate.TypeHint) == wanted)
			}); message.ID.Valid() {
				return []plan.PlanAction{deliverMessage(message)}, "deliver-branch-higher-term-message"
			}
			return deliverTowardNode(observation, target), "deliver-higher-term-message"
		}
	}
	return genericNudge(observation, random)
}

func branchElectionPeer(
	observation core.Observation, leader, target core.NodeID,
) (core.NodeID, bool) {
	var leaderLast, leaderCommit uint64
	for _, node := range observation.Nodes {
		if node.ID == leader {
			leaderLast, _ = semanticUint(node.Semantic["last_index"])
			leaderCommit, _ = semanticUint(node.Semantic["commit"])
			break
		}
	}
	for _, node := range observation.Nodes {
		if node.ID == leader || node.ID == target || node.Status != core.NodeRunning {
			continue
		}
		last, _ := semanticUint(node.Semantic["last_index"])
		commit, _ := semanticUint(node.Semantic["commit"])
		if last < leaderLast || commit < leaderCommit {
			return node.ID, true
		}
		return node.ID, false
	}
	return 0, false
}

func highestTermCandidate(
	observation core.Observation, excluded core.NodeID,
) core.NodeID {
	maximum := maxObservationTerm(observation)
	for _, node := range observation.Nodes {
		term, _ := semanticUint(node.Semantic["term"])
		if node.ID != excluded && node.Status == core.NodeRunning &&
			term == maximum && semanticString(node.Semantic["role"]) == "candidate" {
			return node.ID
		}
	}
	return 0
}

func maxObservationTerm(observation core.Observation) uint64 {
	var maximum uint64
	for _, node := range observation.Nodes {
		if node.Status != core.NodeRunning {
			continue
		}
		term, _ := semanticUint(node.Semantic["term"])
		maximum = max(maximum, term)
	}
	return maximum
}

func observedLeader(observation core.Observation) core.NodeID {
	maximum := maxObservationTerm(observation)
	for _, node := range observation.Nodes {
		term, _ := semanticUint(node.Semantic["term"])
		if node.Status == core.NodeRunning &&
			semanticString(node.Semantic["role"]) == "leader" && term == maximum {
			return node.ID
		}
	}
	return 0
}

func electionNudge(observation core.Observation, random *rand.Rand) ([]plan.PlanAction, string) {
	if message := selectMessage(observation, func(candidate core.MessageObservation) bool {
		return !candidate.Blocked
	}); message.ID.Valid() {
		return []plan.PlanAction{deliverMessage(message)}, "drain-election-message"
	}
	nodes := runningNodes(observation)
	if len(nodes) > 0 {
		return []plan.PlanAction{{Kind: plan.ActionTimeout, Node: nodes[random.Intn(len(nodes))]}}, "trigger-election-timeout"
	}
	return []plan.PlanAction{{Kind: plan.ActionAdvanceTicks, Ticks: 1}}, "advance-time"
}

func genericNudge(observation core.Observation, random *rand.Rand) ([]plan.PlanAction, string) {
	if message := selectMessage(observation, func(candidate core.MessageObservation) bool {
		return !candidate.Blocked
	}); message.ID.Valid() {
		return []plan.PlanAction{deliverMessage(message)}, "deliver-applicable-message"
	}
	return electionNudge(observation, random)
}

func deliverTowardNode(observation core.Observation, target core.NodeID) []plan.PlanAction {
	if message := selectMessage(observation, func(candidate core.MessageObservation) bool {
		return candidate.To == target && !candidate.Blocked
	}); message.ID.Valid() {
		return []plan.PlanAction{deliverMessage(message)}
	}
	return []plan.PlanAction{{Kind: plan.ActionAdvanceTicks, Ticks: 1}}
}

func deliverTowardQuorum(observation core.Observation, leader core.NodeID) []plan.PlanAction {
	if message := selectMessage(observation, func(candidate core.MessageObservation) bool {
		return !candidate.Blocked && (candidate.From == leader || candidate.To == leader)
	}); message.ID.Valid() {
		return []plan.PlanAction{deliverMessage(message)}
	}
	return []plan.PlanAction{{Kind: plan.ActionAdvanceTicks, Ticks: 1}}
}

func replicationRound(observation core.Observation, leader core.NodeID) []plan.PlanAction {
	var peer core.NodeID
	for _, node := range runningNodes(observation) {
		if node == leader {
			continue
		}
		link := core.LinkID{From: leader, To: node}
		if observation.NetworkPartition == nil || !observation.NetworkPartition.Blocks(link) {
			peer = node
			break
		}
	}
	if !peer.Valid() {
		return []plan.PlanAction{{Kind: plan.ActionAdvanceTicks, Ticks: 1}}
	}
	result := make([]plan.PlanAction, 0, 4)
	for sweep := 0; sweep < 2; sweep++ {
		for _, link := range []core.LinkID{
			{From: leader, To: peer},
			{From: peer, To: leader},
		} {
			result = append(result, plan.PlanAction{
				Kind: plan.ActionDeliver,
				Messages: &plan.MessageRangeSelector{
					Link: link, Start: 0, Count: 8,
				},
			})
		}
	}
	return result
}

func partitionTarget(nodes []core.NodeObservation, target core.NodeID) plan.PlanAction {
	others := make([]core.NodeID, 0, len(nodes)-1)
	for _, node := range nodes {
		if node.ID != target {
			others = append(others, node.ID)
		}
	}
	return plan.PlanAction{
		Kind:      plan.ActionPartition,
		Partition: &core.NetworkPartition{Groups: [][]core.NodeID{{target}, others}},
	}
}

func deliverMessage(message core.MessageObservation) plan.PlanAction {
	return plan.PlanAction{
		Kind: plan.ActionDeliver,
		Messages: &plan.MessageRangeSelector{
			Link:  core.LinkID{From: message.From, To: message.To},
			Start: message.Position, Count: 1,
		},
	}
}

func dropMessage(message core.MessageObservation) plan.PlanAction {
	action := deliverMessage(message)
	action.Kind = plan.ActionDrop
	return action
}

func branchKeyMessage(branch *BehaviorBranchTemplate) string {
	if branch == nil {
		return ""
	}
	return branch.PlannedDimensions.KeyMessageClass
}

func branchAllowsMessageAction(
	branch BehaviorBranchTemplate,
	kind plan.ActionKind,
	message core.MessageObservation,
	bindings map[Symbol]Binding,
) bool {
	leader := boundNode(bindings, SymbolLeader)
	target := boundNode(bindings, SymbolTargetFollower)
	for _, preference := range branch.MutationPreferences {
		if plan.ActionKind(preference.ActionKind) != kind ||
			(preference.MessageType != "" &&
				branchMessageClass(message.TypeHint) != branchMessageClass(preference.MessageType)) {
			continue
		}
		switch preference.Condition {
		case "leader-to-target-before-heal":
			return leader.Valid() && target.Valid() &&
				message.From == leader && message.To == target
		case "discard-other-term-message-before-restart":
			wanted := branch.PlannedDimensions.KeyMessageClass
			return target.Valid() && message.To == target &&
				protocolTermMessage(message.TypeHint) &&
				branchMessageClass(message.TypeHint) != wanted
		default:
			return true
		}
	}
	return false
}

func messageTermRelation(
	observation core.Observation, message core.MessageObservation, target core.NodeID,
) string {
	nodeTerm := uint64(0)
	found := false
	for _, node := range observation.Nodes {
		if node.ID != target {
			continue
		}
		nodeTerm, found = semanticUint(node.Semantic["term"])
		break
	}
	messageTerm, err := strconv.ParseUint(message.Metadata["term"], 10, 64)
	if !found || err != nil {
		return "unknown"
	}
	return termRelation(messageTerm, nodeTerm)
}

func selectMessage(
	observation core.Observation, predicate func(core.MessageObservation) bool,
) core.MessageObservation {
	messages := append([]core.MessageObservation(nil), observation.Messages...)
	sort.Slice(messages, func(i, j int) bool { return messages[i].ID < messages[j].ID })
	for _, message := range messages {
		if predicate(message) {
			return message
		}
	}
	return core.MessageObservation{}
}

func messageByID(messages []core.MessageObservation, id core.MessageID) core.MessageObservation {
	for _, message := range messages {
		if message.ID == id {
			return message
		}
	}
	return core.MessageObservation{}
}

func boundNode(bindings map[Symbol]Binding, symbol Symbol) core.NodeID {
	return bindings[symbol].Node
}

func nodeStatus(observation core.Observation, id core.NodeID) core.NodeStatus {
	for _, node := range observation.Nodes {
		if node.ID == id {
			return node.Status
		}
	}
	return ""
}

func runningNodes(observation core.Observation) []core.NodeID {
	result := make([]core.NodeID, 0, len(observation.Nodes))
	for _, node := range observation.Nodes {
		if node.Status == core.NodeRunning {
			result = append(result, node.ID)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result
}

func crashedNodes(observation core.Observation) []core.NodeID {
	result := make([]core.NodeID, 0, len(observation.Nodes))
	for _, node := range observation.Nodes {
		if node.Status == core.NodeCrashed {
			result = append(result, node.ID)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result
}

func runningNodeExcept(observation core.Observation, excluded core.NodeID) core.NodeID {
	for _, node := range runningNodes(observation) {
		if node != excluded {
			return node
		}
	}
	return 0
}

func runningNonLeaderExcept(observation core.Observation, excluded core.NodeID) core.NodeID {
	for _, node := range observation.Nodes {
		if node.ID == excluded || node.Status != core.NodeRunning ||
			semanticString(node.Semantic["role"]) == "leader" {
			continue
		}
		return node.ID
	}
	return runningNodeExcept(observation, excluded)
}

func electionDeliveryRound(
	observation core.Observation, candidate, excluded core.NodeID,
) []plan.PlanAction {
	var voter core.NodeID
	for _, node := range runningNodes(observation) {
		if node != candidate && node != excluded {
			voter = node
			break
		}
	}
	if !voter.Valid() {
		return []plan.PlanAction{{Kind: plan.ActionAdvanceTicks, Ticks: 1}}
	}
	actions := make([]plan.PlanAction, 0, 4)
	for sweep := 0; sweep < 2; sweep++ {
		for _, link := range []core.LinkID{
			{From: candidate, To: voter},
			{From: voter, To: candidate},
		} {
			actions = append(actions, plan.PlanAction{
				Kind: plan.ActionDeliver,
				Messages: &plan.MessageRangeSelector{
					Link: link, Start: 0, Count: 8,
				},
			})
		}
	}
	return actions
}
