// Package policy 提供从最新 Observation 在线生成 PlanAction 的策略。
package policy

import (
	"fmt"
	"math/rand"
	"sort"
	"strconv"

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/core"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/plan"
)

// RandomWeights 控制动作类别概率；同一类别内再均匀选择具体节点或消息。
type RandomWeights struct {
	Deliver      int `json:"deliver"`
	Drop         int `json:"drop"`
	Duplicate    int `json:"duplicate"`
	Timeout      int `json:"timeout"`
	Request      int `json:"request"`
	AdvanceTicks int `json:"advance_ticks"`
}

// RandomConfig 把随机基线限制在当前有界 Raft Profile 内。
type RandomConfig struct {
	MaxValue    int           `json:"max_value"`
	MaxLogIndex uint64        `json:"max_log_index"`
	LargestTerm uint64        `json:"largest_term"`
	Weights     RandomWeights `json:"weights"`
}

func DefaultRandomConfig() RandomConfig {
	return RandomConfig{
		MaxValue: 5, MaxLogIndex: 5, LargestTerm: 5,
		Weights: RandomWeights{Deliver: 50, Drop: 5, Duplicate: 5, Timeout: 20, Request: 15, AdvanceTicks: 5},
	}
}

// Random 是确定性的在线随机基线。同一个 seed 和同一串 Observation 必须产生
// 完全相同的动作序列。
type Random struct {
	config    RandomConfig
	seed      int64
	random    *rand.Rand
	generated []plan.PlanAction
}

func NewRandom(seed int64, config RandomConfig) (*Random, error) {
	if err := validateRandomConfig(config); err != nil {
		return nil, err
	}
	return &Random{config: config, seed: seed, random: rand.New(rand.NewSource(seed))}, nil
}

func (p *Random) Reset(initial core.Observation) error {
	if p == nil {
		return fmt.Errorf("random policy is nil")
	}
	if err := initial.Validate(); err != nil {
		return fmt.Errorf("invalid initial observation: %w", err)
	}
	p.random = rand.New(rand.NewSource(p.seed))
	p.generated = p.generated[:0]
	return nil
}

func (p *Random) Next(observation core.Observation) (plan.PlanAction, bool, error) {
	if p == nil || p.random == nil {
		return plan.PlanAction{}, false, fmt.Errorf("random policy is not initialized")
	}
	if err := observation.Validate(); err != nil {
		return plan.PlanAction{}, false, fmt.Errorf("invalid observation: %w", err)
	}
	groups := p.candidates(observation)
	total := 0
	for _, group := range groups {
		if len(group.actions) > 0 {
			total += group.weight
		}
	}
	if total == 0 {
		return plan.PlanAction{}, false, nil
	}
	draw := p.random.Intn(total)
	for _, group := range groups {
		if len(group.actions) == 0 {
			continue
		}
		if draw < group.weight {
			action := group.actions[p.random.Intn(len(group.actions))].Copy()
			p.generated = append(p.generated, action.Copy())
			return action, true, nil
		}
		draw -= group.weight
	}
	return plan.PlanAction{}, false, fmt.Errorf("random selection reached an impossible state")
}

func (p *Random) Sequence() plan.PlanSequence {
	return plan.PlanSequence{Actions: copyActions(p.generated), Metadata: map[string]string{
		"source": "random", "seed": strconv.FormatInt(p.seed, 10),
	}}
}

type actionGroup struct {
	weight  int
	actions []plan.PlanAction
}

func (p *Random) candidates(observation core.Observation) []actionGroup {
	messages := append([]core.MessageObservation(nil), observation.Messages...)
	sort.Slice(messages, func(i, j int) bool { return messages[i].ID < messages[j].ID })
	deliver := make([]plan.PlanAction, 0, len(messages))
	drop := make([]plan.PlanAction, 0, len(messages))
	duplicate := make([]plan.PlanAction, 0, len(messages))
	for _, message := range messages {
		action := messagePlanAction(plan.ActionDeliver, message)
		if p.messageWithinProfile(message) {
			deliver = append(deliver, action)
		}
		drop = append(drop, messagePlanAction(plan.ActionDrop, message))
		duplicate = append(duplicate, messagePlanAction(plan.ActionDuplicate, message))
	}

	nodes := append([]core.NodeObservation(nil), observation.Nodes...)
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].ID < nodes[j].ID })
	timeouts := make([]plan.PlanAction, 0, len(nodes))
	requests := make([]plan.PlanAction, 0, len(nodes)*p.config.MaxValue)
	for _, node := range nodes {
		if node.Status != core.NodeRunning {
			continue
		}
		role, _ := node.Semantic["role"].(string)
		term, termOK := semanticUint(node.Semantic["term"])
		if role != "leader" && termOK && term < p.config.LargestTerm {
			timeouts = append(timeouts, plan.PlanAction{Kind: plan.ActionTimeout, Node: node.ID})
		}
		lastIndex, indexOK := semanticUint(node.Semantic["last_index"])
		if role == "leader" && indexOK && lastIndex < p.config.MaxLogIndex {
			for value := 1; value <= p.config.MaxValue; value++ {
				requests = append(requests, plan.PlanAction{Kind: plan.ActionRequest, Node: node.ID, Request: strconv.Itoa(value)})
			}
		}
	}

	return []actionGroup{
		{weight: p.config.Weights.Deliver, actions: deliver},
		{weight: p.config.Weights.Drop, actions: drop},
		{weight: p.config.Weights.Duplicate, actions: duplicate},
		{weight: p.config.Weights.Timeout, actions: timeouts},
		{weight: p.config.Weights.Request, actions: requests},
		{weight: p.config.Weights.AdvanceTicks, actions: []plan.PlanAction{{Kind: plan.ActionAdvanceTicks, Ticks: 1}}},
	}
}

func (p *Random) messageWithinProfile(message core.MessageObservation) bool {
	switch message.TypeHint {
	case "MsgVote", "MsgVoteResp", "MsgAppResp", "MsgHeartbeat", "MsgHeartbeatResp", "MsgReadIndex", "MsgReadIndexResp":
		return true
	case "MsgApp":
		count, countErr := strconv.ParseUint(message.Metadata["entry_count"], 10, 64)
		index, indexErr := strconv.ParseUint(message.Metadata["index"], 10, 64)
		return countErr == nil && indexErr == nil && index <= p.config.MaxLogIndex && count <= p.config.MaxLogIndex-index
	default:
		return false
	}
}

func messagePlanAction(kind plan.ActionKind, message core.MessageObservation) plan.PlanAction {
	return plan.PlanAction{Kind: kind, Messages: &plan.MessageRangeSelector{
		Link: core.LinkID{From: message.From, To: message.To}, Start: message.Position, Count: 1,
	}}
}

func validateRandomConfig(config RandomConfig) error {
	if config.MaxValue <= 0 || config.MaxLogIndex == 0 || config.LargestTerm == 0 {
		return fmt.Errorf("random policy bounds must be positive")
	}
	weights := []int{config.Weights.Deliver, config.Weights.Drop, config.Weights.Duplicate,
		config.Weights.Timeout, config.Weights.Request, config.Weights.AdvanceTicks}
	total := 0
	for _, weight := range weights {
		if weight < 0 {
			return fmt.Errorf("random policy weights must be non-negative")
		}
		total += weight
	}
	if total == 0 {
		return fmt.Errorf("random policy needs at least one positive weight")
	}
	return nil
}

func semanticUint(value any) (uint64, bool) {
	switch number := value.(type) {
	case uint64:
		return number, true
	case int:
		if number >= 0 {
			return uint64(number), true
		}
	case float64:
		if number >= 0 && number == float64(uint64(number)) {
			return uint64(number), true
		}
	}
	return 0, false
}

func copyActions(actions []plan.PlanAction) []plan.PlanAction {
	result := make([]plan.PlanAction, len(actions))
	for index, action := range actions {
		result[index] = action.Copy()
	}
	return result
}
