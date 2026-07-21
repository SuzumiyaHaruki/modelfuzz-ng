// Package policy 提供从最新 Observation 在线生成 PlanAction 的策略。
package policy

import (
	"fmt"
	"math/rand"
	"sort"
	"strconv"

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/core"
	raftmodel "github.com/SuzumiyaHaruki/modelfuzz-ng/internal/model/raft"
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
	Crash        int `json:"crash"`
	Restart      int `json:"restart"`
}

// RandomConfig 把随机基线限制在当前有界 Raft Profile 内。
type RandomConfig struct {
	MaxValue    int           `json:"max_value"`
	MaxLogIndex uint64        `json:"max_log_index"`
	LargestTerm uint64        `json:"largest_term"`
	MaxCrashed  int           `json:"max_crashed"`
	Weights     RandomWeights `json:"weights"`
}

func DefaultRandomConfig() RandomConfig {
	return RandomConfig{
		MaxValue: 5, MaxLogIndex: 5, LargestTerm: 5, MaxCrashed: 1,
		Weights: RandomWeights{
			Deliver: 50, Drop: 5, Duplicate: 5, Timeout: 20, Request: 15,
			AdvanceTicks: 5, Crash: 5, Restart: 10,
		},
	}
}

// Random 是确定性的在线随机基线。同一个 seed 和同一串 Observation 必须产生
// 完全相同的动作序列。
type Random struct {
	config    RandomConfig
	seed      int64
	random    *rand.Rand
	generated []plan.PlanAction
	profile   *raftmodel.Mapper
}

func NewRandom(seed int64, config RandomConfig) (*Random, error) {
	if err := validateRandomConfig(config); err != nil {
		return nil, err
	}
	profileConfig := raftmodel.DefaultConfig()
	profileConfig.MaxValue = config.MaxValue
	profileConfig.MaxLogIndex = config.MaxLogIndex
	profileConfig.LargestTerm = config.LargestTerm
	profile, err := raftmodel.NewMapperWithConfig(profileConfig)
	if err != nil {
		return nil, fmt.Errorf("create random policy profile: %w", err)
	}
	return &Random{config: config, seed: seed, random: rand.New(rand.NewSource(seed)), profile: profile}, nil
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
		if p.messageWithinProfile(observation, message) && observedNodeRunning(observation, message.To) {
			deliver = append(deliver, action)
		}
		drop = append(drop, messagePlanAction(plan.ActionDrop, message))
		duplicate = append(duplicate, messagePlanAction(plan.ActionDuplicate, message))
	}

	nodes := append([]core.NodeObservation(nil), observation.Nodes...)
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].ID < nodes[j].ID })
	timeouts := make([]plan.PlanAction, 0, len(nodes))
	requests := make([]plan.PlanAction, 0, len(nodes)*p.config.MaxValue)
	crashes := make([]plan.PlanAction, 0, len(nodes))
	restarts := make([]plan.PlanAction, 0, len(nodes))
	runningCount := 0
	crashedCount := 0
	for _, node := range nodes {
		switch node.Status {
		case core.NodeRunning:
			runningCount++
		case core.NodeCrashed:
			crashedCount++
		}
	}
	for _, node := range nodes {
		if node.Status == core.NodeCrashed {
			restarts = append(restarts, plan.PlanAction{Kind: plan.ActionRestart, Node: node.ID})
			continue
		}
		if node.Status != core.NodeRunning {
			continue
		}
		if runningCount > 1 && crashedCount < p.config.MaxCrashed {
			crashes = append(crashes, plan.PlanAction{Kind: plan.ActionCrash, Node: node.ID})
		}
		role, _ := node.Semantic["role"].(string)
		term, termOK := semanticUint(node.Semantic["term"])
		lastIndex, indexOK := semanticUint(node.Semantic["last_index"])
		// 成为 Leader 时 etcd-raft 会自动追加 no-op，因此日志已到模型上限的
		// 节点不能再由有界随机策略主动发起新选举。
		if role != "leader" && termOK && term < p.config.LargestTerm &&
			indexOK && lastIndex < p.config.MaxLogIndex {
			timeouts = append(timeouts, plan.PlanAction{Kind: plan.ActionTimeout, Node: node.ID})
		}
		if requestTargetUsable(observation, node, p.config.MaxLogIndex) {
			for value := 1; value <= p.config.MaxValue; value++ {
				requests = append(requests, plan.PlanAction{Kind: plan.ActionRequest, Node: node.ID, Request: strconv.Itoa(value)})
			}
		}
	}
	advance := make([]plan.PlanAction, 0, 1)
	advanceAction := core.Action{Kind: core.ActionAdvanceTime, TargetTime: observation.Time + 1}
	if observation.Time < ^core.LogicalTime(0) && p.profile.ValidateAction(advanceAction, observation) == nil {
		advance = append(advance, plan.PlanAction{Kind: plan.ActionAdvanceTicks, Ticks: 1})
	}

	return []actionGroup{
		{weight: p.config.Weights.Deliver, actions: deliver},
		{weight: p.config.Weights.Drop, actions: drop},
		{weight: p.config.Weights.Duplicate, actions: duplicate},
		{weight: p.config.Weights.Timeout, actions: timeouts},
		{weight: p.config.Weights.Request, actions: requests},
		{weight: p.config.Weights.AdvanceTicks, actions: advance},
		{weight: p.config.Weights.Crash, actions: crashes},
		{weight: p.config.Weights.Restart, actions: restarts},
	}
}

func observedNodeRunning(observation core.Observation, id core.NodeID) bool {
	for _, node := range observation.Nodes {
		if node.ID == id {
			return node.Status == core.NodeRunning
		}
	}
	return false
}

func requestTargetUsable(observation core.Observation, target core.NodeObservation, maxLogIndex uint64) bool {
	role, _ := target.Semantic["role"].(string)
	if role == "leader" {
		lastIndex, ok := semanticUint(target.Semantic["last_index"])
		return ok && lastIndex < maxLogIndex
	}
	if role != "follower" {
		return false
	}
	leader, ok := semanticUint(target.Semantic["leader"])
	if !ok || leader == 0 {
		return false
	}
	for _, node := range observation.Nodes {
		if uint64(node.ID) != leader || node.Status != core.NodeRunning || node.Semantic["role"] != "leader" {
			continue
		}
		lastIndex, indexOK := semanticUint(node.Semantic["last_index"])
		return indexOK && lastIndex < maxLogIndex
	}
	return false
}

func (p *Random) messageWithinProfile(observation core.Observation, message core.MessageObservation) bool {
	action := core.Action{
		Kind: core.ActionDeliver, Message: message.ID,
		Selector: &core.MessageSelector{
			Link: core.LinkID{From: message.From, To: message.To}, Position: message.Position,
		},
	}
	return p.profile != nil && p.profile.ValidateAction(action, observation) == nil
}

func messagePlanAction(kind plan.ActionKind, message core.MessageObservation) plan.PlanAction {
	return plan.PlanAction{Kind: kind, Messages: &plan.MessageRangeSelector{
		Link: core.LinkID{From: message.From, To: message.To}, Start: message.Position, Count: 1,
	}}
}

func validateRandomConfig(config RandomConfig) error {
	if config.MaxValue <= 0 || config.MaxLogIndex == 0 || config.LargestTerm == 0 || config.MaxCrashed <= 0 {
		return fmt.Errorf("random policy bounds must be positive")
	}
	weights := []int{config.Weights.Deliver, config.Weights.Drop, config.Weights.Duplicate,
		config.Weights.Timeout, config.Weights.Request, config.Weights.AdvanceTicks,
		config.Weights.Crash, config.Weights.Restart}
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
