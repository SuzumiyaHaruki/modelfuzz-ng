package mutation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"math/rand"
	"strconv"

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/core"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/plan"
)

type RandomConfig struct {
	NodeIDs    []core.NodeID `json:"node_ids"`
	MaxValue   int           `json:"max_value"`
	MaxTicks   uint64        `json:"max_ticks"`
	MaxActions int           `json:"max_actions"`
	MaxCrashed int           `json:"max_crashed"`
}

// Random 对已经执行过的 Plan 做小范围结构变异。交换动作是对原 ModelFuzz
// trace mutation 的直接继承；删除、复制和字段扰动用于适配 NG 更丰富的动作。
type Random struct {
	config RandomConfig
}

func NewRandom(config RandomConfig) (*Random, error) {
	if len(config.NodeIDs) < 2 {
		return nil, fmt.Errorf("random mutation needs at least two nodes")
	}
	seenNodes := make(map[core.NodeID]struct{}, len(config.NodeIDs))
	for _, node := range config.NodeIDs {
		if !node.Valid() {
			return nil, fmt.Errorf("random mutation contains invalid node %d", node)
		}
		if _, duplicate := seenNodes[node]; duplicate {
			return nil, fmt.Errorf("random mutation contains duplicate node %d", node)
		}
		seenNodes[node] = struct{}{}
	}
	if config.MaxValue <= 0 || config.MaxTicks == 0 || config.MaxTicks > math.MaxInt64 || config.MaxActions <= 0 {
		return nil, fmt.Errorf("random mutation bounds must be positive")
	}
	if config.MaxCrashed == 0 {
		config.MaxCrashed = 1
	}
	if config.MaxCrashed < 0 || config.MaxCrashed >= len(config.NodeIDs) {
		return nil, fmt.Errorf("random mutation MaxCrashed must be in 1..%d", len(config.NodeIDs)-1)
	}
	config.NodeIDs = append([]core.NodeID(nil), config.NodeIDs...)
	return &Random{config: config}, nil
}

func (m *Random) Name() string { return "random_mutation" }

func (m *Random) Mutate(ctx context.Context, request Request) ([]plan.PlanSequence, error) {
	if m == nil {
		return nil, fmt.Errorf("random mutator is nil")
	}
	if request.Count <= 0 {
		return nil, fmt.Errorf("mutation count must be positive")
	}
	if err := request.Entry.Plan.Validate(); err != nil {
		return nil, fmt.Errorf("invalid parent plan: %w", err)
	}
	if len(request.Entry.Plan.Actions) == 0 {
		return nil, fmt.Errorf("cannot mutate an empty plan")
	}
	random := rand.New(rand.NewSource(request.Seed))
	seen := map[string]struct{}{planDigest(request.Entry.Plan): {}}
	result := make([]plan.PlanSequence, 0, request.Count)
	for attempts := 0; len(result) < request.Count && attempts < request.Count*32; attempts++ {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		candidate := request.Entry.Plan.Copy()
		operation := random.Intn(6)
		operationName := ""
		switch operation {
		case 0:
			m.swapActions(random, &candidate)
			operationName = "swap"
		case 1:
			m.perturbAction(random, &candidate)
			operationName = "perturb"
		case 2:
			m.deleteAction(random, &candidate)
			operationName = "delete"
		case 3:
			m.duplicateAction(random, &candidate)
			operationName = "duplicate"
		case 4:
			m.insertAction(random, &candidate)
			operationName = "insert"
		case 5:
			if m.insertCrashRestartPair(random, &candidate) {
				operationName = "crash_restart_pair"
			} else {
				m.perturbAction(random, &candidate)
				operationName = "perturb"
			}
		}
		if len(candidate.Actions) == 0 || len(candidate.Actions) > m.config.MaxActions {
			continue
		}
		if err := candidate.Validate(); err != nil {
			continue
		}
		if err := m.validateLifecycle(candidate); err != nil {
			continue
		}
		digest := planDigest(candidate)
		if _, duplicate := seen[digest]; duplicate {
			continue
		}
		seen[digest] = struct{}{}
		candidate.Metadata = map[string]string{
			"source": m.Name(), "parent_id": request.Entry.ID,
			"mutation_seed": strconv.FormatInt(request.Seed, 10), "mutation_operation": operationName,
		}
		result = append(result, candidate)
	}
	if len(result) == 0 {
		return nil, fmt.Errorf("could not produce a distinct valid mutation")
	}
	return result, nil
}

func (m *Random) swapActions(random *rand.Rand, sequence *plan.PlanSequence) {
	if len(sequence.Actions) < 2 {
		m.perturbAction(random, sequence)
		return
	}
	first := random.Intn(len(sequence.Actions))
	second := random.Intn(len(sequence.Actions) - 1)
	if second >= first {
		second++
	}
	sequence.Actions[first], sequence.Actions[second] = sequence.Actions[second], sequence.Actions[first]
}

func (m *Random) perturbAction(random *rand.Rand, sequence *plan.PlanSequence) {
	index := random.Intn(len(sequence.Actions))
	action := &sequence.Actions[index]
	switch action.Kind {
	case plan.ActionDeliver, plan.ActionDrop, plan.ActionDuplicate:
		selector := action.Messages
		switch random.Intn(3) {
		case 0:
			selector.Start = random.Intn(3)
		case 1:
			selector.Count = 1 + random.Intn(3)
		case 2:
			selector.Link = m.randomLink(random)
		}
	case plan.ActionAdvanceTicks:
		action.Ticks = 1 + uint64(random.Int63n(int64(m.config.MaxTicks)))
	case plan.ActionTimeout, plan.ActionCrash, plan.ActionRestart:
		action.Node = m.randomNodeExcept(random, action.Node)
	case plan.ActionRequest:
		if random.Intn(2) == 0 {
			action.Node = m.randomNodeExcept(random, action.Node)
		} else {
			action.Request = strconv.Itoa(1 + random.Intn(m.config.MaxValue))
		}
	}
}

func (m *Random) deleteAction(random *rand.Rand, sequence *plan.PlanSequence) {
	if len(sequence.Actions) == 1 {
		m.perturbAction(random, sequence)
		return
	}
	index := random.Intn(len(sequence.Actions))
	sequence.Actions = append(sequence.Actions[:index], sequence.Actions[index+1:]...)
}

func (m *Random) duplicateAction(random *rand.Rand, sequence *plan.PlanSequence) {
	if len(sequence.Actions) >= m.config.MaxActions {
		m.perturbAction(random, sequence)
		return
	}
	index := random.Intn(len(sequence.Actions))
	duplicated := sequence.Actions[index].Copy()
	sequence.Actions = append(sequence.Actions, plan.PlanAction{})
	copy(sequence.Actions[index+2:], sequence.Actions[index+1:])
	sequence.Actions[index+1] = duplicated
}

func (m *Random) insertAction(random *rand.Rand, sequence *plan.PlanSequence) {
	if len(sequence.Actions) >= m.config.MaxActions {
		m.perturbAction(random, sequence)
		return
	}
	action := m.randomAction(random)
	index := random.Intn(len(sequence.Actions) + 1)
	sequence.Actions = append(sequence.Actions, plan.PlanAction{})
	copy(sequence.Actions[index+1:], sequence.Actions[index:])
	sequence.Actions[index] = action
}

// insertCrashRestartPair 在一段已有动作的两侧插入同一节点的停止/恢复动作。
// 候选必须保持节点生命周期合法，并且不能超过同时停止节点上限。区间至少包含
// 一个已有动作，避免生成没有状态探索价值的相邻 crash/restart。
func (m *Random) insertCrashRestartPair(random *rand.Rand, sequence *plan.PlanSequence) bool {
	if len(sequence.Actions)+2 > m.config.MaxActions || len(sequence.Actions) == 0 {
		return false
	}
	// 先随机采样不同长度的停止窗口，避免对长 Plan 枚举所有 O(n²) 区间。
	// 采样失败后再检查单动作窗口，保证存在简单合法位置时一定能够插入。
	for attempts := 0; attempts < 64; attempts++ {
		node := m.randomNode(random)
		start := random.Intn(len(sequence.Actions))
		end := start + 1 + random.Intn(len(sequence.Actions)-start)
		candidate := sequence.Copy()
		candidate.Actions = insertLifecyclePair(candidate.Actions, node, start, end)
		if err := m.validateLifecycle(candidate); err == nil {
			sequence.Actions = candidate.Actions
			return true
		}
	}
	for _, node := range m.config.NodeIDs {
		for start := 0; start < len(sequence.Actions); start++ {
			candidate := sequence.Copy()
			candidate.Actions = insertLifecyclePair(candidate.Actions, node, start, start+1)
			if err := m.validateLifecycle(candidate); err == nil {
				sequence.Actions = candidate.Actions
				return true
			}
		}
	}
	return false
}

func insertLifecyclePair(actions []plan.PlanAction, node core.NodeID, start, end int) []plan.PlanAction {
	result := make([]plan.PlanAction, 0, len(actions)+2)
	result = append(result, actions[:start]...)
	result = append(result, plan.PlanAction{Kind: plan.ActionCrash, Node: node})
	result = append(result, actions[start:end]...)
	result = append(result, plan.PlanAction{Kind: plan.ActionRestart, Node: node})
	result = append(result, actions[end:]...)
	return result
}

// validateLifecycle 只校验离线可判断的节点生命周期约束。停止节点上的普通动作
// 仍交给 Resolver 在运行时按 Observation 解析为 resolved 或 skipped。
func (m *Random) validateLifecycle(sequence plan.PlanSequence) error {
	running := make(map[core.NodeID]bool, len(m.config.NodeIDs))
	for _, node := range m.config.NodeIDs {
		running[node] = true
	}
	crashed := 0
	for index, action := range sequence.Actions {
		switch action.Kind {
		case plan.ActionCrash:
			if _, exists := running[action.Node]; !exists {
				return fmt.Errorf("action %d crashes unknown node %d", index, action.Node)
			}
			if !running[action.Node] {
				return fmt.Errorf("action %d crashes node %d twice", index, action.Node)
			}
			if crashed >= m.config.MaxCrashed {
				return fmt.Errorf("action %d exceeds MaxCrashed %d", index, m.config.MaxCrashed)
			}
			running[action.Node] = false
			crashed++
		case plan.ActionRestart:
			if _, exists := running[action.Node]; !exists {
				return fmt.Errorf("action %d restarts unknown node %d", index, action.Node)
			}
			if running[action.Node] {
				return fmt.Errorf("action %d restarts running node %d", index, action.Node)
			}
			running[action.Node] = true
			crashed--
		}
	}
	return nil
}

func (m *Random) randomAction(random *rand.Rand) plan.PlanAction {
	switch random.Intn(10) {
	case 0:
		return plan.PlanAction{Kind: plan.ActionTimeout, Node: m.randomNode(random)}
	case 1, 2:
		return plan.PlanAction{Kind: plan.ActionAdvanceTicks, Ticks: 1 + uint64(random.Int63n(int64(m.config.MaxTicks)))}
	case 3, 4, 5:
		return plan.PlanAction{Kind: plan.ActionRequest, Node: m.randomNode(random), Request: strconv.Itoa(1 + random.Intn(m.config.MaxValue))}
	default:
		return plan.PlanAction{Kind: plan.ActionDeliver, Messages: &plan.MessageRangeSelector{Link: m.randomLink(random), Count: 1}}
	}
}

func (m *Random) randomNode(random *rand.Rand) core.NodeID {
	return m.config.NodeIDs[random.Intn(len(m.config.NodeIDs))]
}

func (m *Random) randomNodeExcept(random *rand.Rand, excluded core.NodeID) core.NodeID {
	for attempts := 0; attempts < len(m.config.NodeIDs)*2; attempts++ {
		node := m.randomNode(random)
		if node != excluded {
			return node
		}
	}
	return excluded
}

func (m *Random) randomLink(random *rand.Rand) core.LinkID {
	from := m.randomNode(random)
	to := m.randomNodeExcept(random, from)
	return core.LinkID{From: from, To: to}
}

func planDigest(sequence plan.PlanSequence) string {
	// Metadata 不参与去重；来源和父子关系不改变可执行动作。
	data, _ := json.Marshal(sequence.Actions)
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}
