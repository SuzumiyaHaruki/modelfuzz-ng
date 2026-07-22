package policy

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/core"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/llm"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/plan"
)

type GenerationMode string

const (
	GenerationInitial  GenerationMode = "initial"
	GenerationMutation GenerationMode = "mutation"
)

type LLMConfig struct {
	NodeIDs     []core.NodeID
	MaxValue    int
	MaxTicks    uint64
	MaxActions  int
	MaxCrashed  int
	MaxLogIndex uint64
	LargestTerm uint64
}

type GenerationRequest struct {
	Mode         GenerationMode
	Count        int
	Parent       plan.PlanSequence
	ParentID     string
	NewStateKeys []int64
}

// generatedRequestValue 只用于解析 LLM 返回值。Plan 的规范 JSON 仍把 request
// 保存为字符串，但实际模型偶尔会把纯数字值输出成 JSON number；这里在进入
// Plan 校验前将其规范化，避免因为无关的表示差异丢弃整批结果。
type generatedRequestValue string

func (v *generatedRequestValue) UnmarshalJSON(data []byte) error {
	var text string
	if err := json.Unmarshal(data, &text); err == nil {
		*v = generatedRequestValue(text)
		return nil
	}
	var number json.Number
	if err := json.Unmarshal(data, &number); err != nil {
		return fmt.Errorf("request must be a JSON string or integer")
	}
	if _, err := strconv.Atoi(number.String()); err != nil {
		return fmt.Errorf("request number %q must be an integer", number)
	}
	*v = generatedRequestValue(number.String())
	return nil
}

type generatedPlanAction struct {
	Kind     plan.ActionKind            `json:"kind"`
	Node     core.NodeID                `json:"node,omitempty"`
	Messages *plan.MessageRangeSelector `json:"messages,omitempty"`
	Ticks    uint64                     `json:"ticks,omitempty"`
	Request  generatedRequestValue      `json:"request,omitempty"`
}

func (a generatedPlanAction) planAction() plan.PlanAction {
	return plan.PlanAction{
		Kind: a.Kind, Node: a.Node, Messages: a.Messages, Ticks: a.Ticks, Request: string(a.Request),
	}
}

type generatedPlan struct {
	Actions  []generatedPlanAction `json:"actions"`
	Metadata map[string]string     `json:"metadata,omitempty"`
}

func (p generatedPlan) planSequence() plan.PlanSequence {
	actions := make([]plan.PlanAction, len(p.Actions))
	for index, action := range p.Actions {
		actions[index] = action.planAction()
	}
	return plan.PlanSequence{Actions: actions, Metadata: p.Metadata}
}

// LLMPlanner 只负责生成并校验 Plan，不接触 Runtime。厂商 API 位于 llm 包，
// 因此以后替换模型时无需修改 Experiment 的反馈逻辑。
type LLMPlanner struct {
	client llm.JSONClient
	config LLMConfig
}

func NewLLMPlanner(client llm.JSONClient, config LLMConfig) (*LLMPlanner, error) {
	if client == nil {
		return nil, fmt.Errorf("LLM client must not be nil")
	}
	if len(config.NodeIDs) < 2 || config.MaxValue <= 0 || config.MaxTicks == 0 || config.MaxActions <= 0 ||
		config.MaxLogIndex == 0 || config.LargestTerm == 0 {
		return nil, fmt.Errorf("LLM planner bounds are invalid")
	}
	if config.MaxCrashed == 0 {
		config.MaxCrashed = 1
	}
	if config.MaxCrashed < 0 || config.MaxCrashed >= len(config.NodeIDs) {
		return nil, fmt.Errorf("LLM planner MaxCrashed must be in 1..%d", len(config.NodeIDs)-1)
	}
	seenNodes := make(map[core.NodeID]struct{}, len(config.NodeIDs))
	for _, node := range config.NodeIDs {
		if !node.Valid() {
			return nil, fmt.Errorf("LLM planner contains invalid node %d", node)
		}
		if _, duplicate := seenNodes[node]; duplicate {
			return nil, fmt.Errorf("LLM planner contains duplicate node %d", node)
		}
		seenNodes[node] = struct{}{}
	}
	config.NodeIDs = append([]core.NodeID(nil), config.NodeIDs...)
	return &LLMPlanner{client: client, config: config}, nil
}

func (p *LLMPlanner) Generate(ctx context.Context, request GenerationRequest) ([]plan.PlanSequence, error) {
	if p == nil {
		return nil, fmt.Errorf("LLM planner is nil")
	}
	if request.Count <= 0 {
		return nil, fmt.Errorf("LLM generation count must be positive")
	}
	if request.Mode != GenerationInitial && request.Mode != GenerationMutation {
		return nil, fmt.Errorf("unknown LLM generation mode %q", request.Mode)
	}
	if request.Mode == GenerationMutation {
		if len(request.Parent.Actions) == 0 {
			return nil, fmt.Errorf("LLM mutation parent must not be empty")
		}
		if err := request.Parent.Validate(); err != nil {
			return nil, fmt.Errorf("invalid LLM mutation parent: %w", err)
		}
	}
	messages, err := p.prompt(request)
	if err != nil {
		return nil, err
	}
	options := llm.Options{
		Thinking: request.Mode == GenerationInitial, Temperature: 0.6, MaxTokens: 8192,
		Purpose: string(request.Mode),
	}
	if request.Mode == GenerationMutation {
		options.Temperature = 0.4
	}
	completion, err := p.client.CompleteJSON(ctx, messages, options)
	if err != nil {
		return nil, err
	}
	var response struct {
		Plans []generatedPlan `json:"plans"`
	}
	decoder := json.NewDecoder(bytes.NewReader(completion.Content))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&response); err != nil {
		return nil, fmt.Errorf("decode LLM plans: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return nil, fmt.Errorf("LLM output contains trailing JSON values")
	}
	if len(response.Plans) == 0 {
		return nil, fmt.Errorf("LLM returned no plans")
	}
	seen := make(map[string]struct{})
	if request.Mode == GenerationMutation {
		seen[actionDigest(request.Parent)] = struct{}{}
	}
	result := make([]plan.PlanSequence, 0, request.Count)
	rejections := make([]string, 0)
	for index, generated := range response.Plans {
		if len(result) == request.Count {
			break
		}
		sequence := generated.planSequence()
		if err := p.validateSequence(sequence); err != nil {
			rejections = append(rejections, fmt.Sprintf("plan %d: %v", index, err))
			continue
		}
		digest := actionDigest(sequence)
		if _, duplicate := seen[digest]; duplicate {
			rejections = append(rejections, fmt.Sprintf("plan %d: duplicate actions", index))
			continue
		}
		seen[digest] = struct{}{}
		sequence.Metadata = map[string]string{"source": "llm_" + string(request.Mode)}
		if request.ParentID != "" {
			sequence.Metadata["parent_id"] = request.ParentID
		}
		result = append(result, sequence.Copy())
	}
	if len(result) == 0 {
		return nil, fmt.Errorf("LLM returned no distinct Plan within the configured schema and bounds%s", rejectionSummary(rejections))
	}
	// 初始化不足时若继续运行，Runner 会在队列耗尽后再次调用 initializer，
	// 造成隐式额外费用。Mutation 则保留已经生成的有效部分即可。
	if request.Mode == GenerationInitial && len(result) != request.Count {
		return nil, fmt.Errorf("LLM returned %d valid initial plans, want %d%s",
			len(result), request.Count, rejectionSummary(rejections))
	}
	return result, nil
}

func rejectionSummary(rejections []string) string {
	if len(rejections) == 0 {
		return ""
	}
	if len(rejections) > 4 {
		rejections = append(append([]string(nil), rejections[:4]...), fmt.Sprintf("and %d more", len(rejections)-4))
	}
	return "; rejected: " + strings.Join(rejections, "; ")
}

func (p *LLMPlanner) prompt(request GenerationRequest) ([]llm.Message, error) {
	nodes, _ := json.Marshal(p.config.NodeIDs)
	system := fmt.Sprintf(`You generate JSON execution plans for controlled Raft model fuzzing.
Return exactly one JSON object {"plans":[{"actions":[...]}]} and no prose.
Generate %d diverse, meaningful plans. Nodes are %s. Allowed action kinds:
- {"kind":"timeout","node":N}
- {"kind":"advance_ticks","ticks":K}, 1 <= K <= %d
- {"kind":"request","node":N,"request":"V"}, 1 <= V <= %d; request SHOULD be a JSON string (integer JSON numbers are also accepted)
- {"kind":"crash","node":N}; N must currently be running
- {"kind":"restart","node":N}; N must previously have been crashed
- {"kind":"deliver|drop|duplicate","messages":{"link":{"from":A,"to":B},"start":S,"count":C}}
Links must connect two distinct configured nodes; S >= 0 and C > 0. A selector position is relative to the current queue when that action executes. Delivery may be truncated when fewer messages exist. At most %d node may be crashed simultaneously. Normally pair a crash with a later restart of the same node, unless the plan intentionally tests a terminal node failure. Each plan must contain 1..%d actions. Aim for elections, delayed or reordered messages, leader failure, recovery with delayed messages, log replication and commit.
The bounded model permits terms up to %d and log indices up to %d. Each timeout can advance a term and each successful election or accepted request can grow the log, so keep these actions within the bounds. A leader accepts a request directly. A follower that has learned the current leader forwards the request as MsgProp, which must later be delivered to that leader. A candidate or follower without a known leader drops the request as a model stutter. JSON object output is mandatory.`,
		request.Count, nodes, p.config.MaxTicks, p.config.MaxValue, p.config.MaxCrashed, p.config.MaxActions,
		p.config.LargestTerm, p.config.MaxLogIndex)
	user := "Create initial seed plans that are likely to execute useful Raft behavior."
	if request.Mode == GenerationMutation {
		parent, err := json.Marshal(request.Parent)
		if err != nil {
			return nil, fmt.Errorf("encode mutation parent: %w", err)
		}
		user = fmt.Sprintf("Mutate this retained parent plan while preserving some useful prefix and changing scheduling choices. Parent ID: %s. Newly covered model state keys: %v. Parent JSON: %s",
			request.ParentID, request.NewStateKeys, parent)
	}
	return []llm.Message{{Role: "system", Content: system}, {Role: "user", Content: user}}, nil
}

func (p *LLMPlanner) validateSequence(sequence plan.PlanSequence) error {
	if len(sequence.Actions) == 0 || len(sequence.Actions) > p.config.MaxActions {
		return fmt.Errorf("LLM plan action count is out of range")
	}
	if err := sequence.Validate(); err != nil {
		return err
	}
	configured := make(map[core.NodeID]struct{}, len(p.config.NodeIDs))
	running := make(map[core.NodeID]bool, len(p.config.NodeIDs))
	for _, node := range p.config.NodeIDs {
		configured[node] = struct{}{}
		running[node] = true
	}
	crashed := 0
	timeouts := uint64(0)
	potentialLogGrowth := uint64(0)
	for _, action := range sequence.Actions {
		switch action.Kind {
		case plan.ActionCrash:
			if _, exists := configured[action.Node]; !exists {
				return fmt.Errorf("LLM plan uses unknown node %d", action.Node)
			}
			if !running[action.Node] {
				return fmt.Errorf("LLM plan crashes node %d while it is already crashed", action.Node)
			}
			if crashed >= p.config.MaxCrashed {
				return fmt.Errorf("LLM plan exceeds MaxCrashed %d", p.config.MaxCrashed)
			}
			running[action.Node] = false
			crashed++
		case plan.ActionRestart:
			if _, exists := configured[action.Node]; !exists {
				return fmt.Errorf("LLM plan uses unknown node %d", action.Node)
			}
			if running[action.Node] {
				return fmt.Errorf("LLM plan restarts node %d before it is crashed", action.Node)
			}
			running[action.Node] = true
			crashed--
		case plan.ActionTimeout:
			if _, exists := configured[action.Node]; !exists {
				return fmt.Errorf("LLM plan uses unknown node %d", action.Node)
			}
			timeouts++
			potentialLogGrowth++
		case plan.ActionRequest:
			if _, exists := configured[action.Node]; !exists {
				return fmt.Errorf("LLM plan uses unknown node %d", action.Node)
			}
			value, err := strconv.Atoi(action.Request)
			if err != nil || value < 1 || value > p.config.MaxValue {
				return fmt.Errorf("LLM request %q is out of range", action.Request)
			}
			potentialLogGrowth++
		case plan.ActionAdvanceTicks:
			if action.Ticks > p.config.MaxTicks {
				return fmt.Errorf("LLM ticks %d exceed maximum", action.Ticks)
			}
		case plan.ActionDeliver, plan.ActionDrop, plan.ActionDuplicate:
			if _, exists := configured[action.Messages.Link.From]; !exists {
				return fmt.Errorf("LLM link uses unknown source")
			}
			if _, exists := configured[action.Messages.Link.To]; !exists || action.Messages.Link.From == action.Messages.Link.To {
				return fmt.Errorf("LLM link uses invalid destination")
			}
		}
	}
	if timeouts > p.config.LargestTerm {
		return fmt.Errorf("LLM plan has %d timeouts, exceeding term bound %d", timeouts, p.config.LargestTerm)
	}
	if potentialLogGrowth > p.config.MaxLogIndex {
		return fmt.Errorf("LLM plan potential log growth %d exceeds bound %d", potentialLogGrowth, p.config.MaxLogIndex)
	}
	return nil
}

func actionDigest(sequence plan.PlanSequence) string {
	data, _ := json.Marshal(sequence.Actions)
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}
