package plan

import (
	"fmt"
	"sort"

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/core"
)

const (
	defaultMaxAdvanceTicks uint64 = 100
	defaultMaxBatch        int    = 1024
)

// ResolverConfig 限制单条 PlanAction 的展开规模，避免一个错误计划产生过多
// tick 或 Concrete Action。限制不属于 core.Action 的语义。
type ResolverConfig struct {
	MaxAdvanceTicks   uint64 `json:"max_advance_ticks"`
	MaxBatch          int    `json:"max_batch"`
	ClampMessageStart bool   `json:"clamp_message_start"`
}

func DefaultResolverConfig() ResolverConfig {
	return ResolverConfig{
		MaxAdvanceTicks:   defaultMaxAdvanceTicks,
		MaxBatch:          defaultMaxBatch,
		ClampMessageStart: true,
	}
}

func NewDefaultResolver() *Resolver {
	return &Resolver{config: DefaultResolverConfig()}
}

type Resolver struct {
	config ResolverConfig
}

func NewResolver(config ResolverConfig) (*Resolver, error) {
	if config.MaxAdvanceTicks == 0 {
		return nil, fmt.Errorf("%w: MaxAdvanceTicks must be positive", ErrInvalidPlan)
	}
	if config.MaxBatch <= 0 {
		return nil, fmt.Errorf("%w: MaxBatch must be positive", ErrInvalidPlan)
	}
	return &Resolver{config: config}, nil
}

// Resolve 根据当前 Observation 将一条 PlanAction 解析为零到多个 Concrete
// Action。它是纯函数式操作，不执行动作，也不修改传入的 Plan 或 Observation。
func (r *Resolver) Resolve(action PlanAction, observation core.Observation) Resolution {
	result := Resolution{Plan: action.Copy(), Requested: 1}
	if r == nil {
		return invalidResolution(result, ReasonResolverUnavailable, "resolver is nil")
	}
	if err := action.Validate(); err != nil {
		return invalidResolution(result, ReasonInvalidPlan, err.Error())
	}
	if err := observation.Validate(); err != nil {
		return invalidResolution(result, ReasonInvalidObservation, "invalid observation: "+err.Error())
	}

	switch action.Kind {
	case ActionDeliver, ActionDrop, ActionDuplicate:
		return r.resolveMessages(result, action, observation)
	case ActionAdvanceTicks:
		return r.resolveTime(result, action, observation)
	case ActionTimeout, ActionCrash, ActionRestart, ActionRequest:
		return r.resolveNode(result, action, observation)
	case ActionPartition, ActionHeal:
		return r.resolvePartition(result, action, observation)
	default:
		return invalidResolution(result, ReasonUnknownActionKind, "unknown action kind")
	}
}

func (r *Resolver) resolveMessages(result Resolution, action PlanAction, observation core.Observation) Resolution {
	selector := *action.Messages
	result.Requested = selector.Count
	if selector.Count > r.config.MaxBatch {
		return invalidResolution(result, ReasonBatchLimit, fmt.Sprintf("message count %d exceeds MaxBatch %d", selector.Count, r.config.MaxBatch))
	}
	if action.Kind == ActionDeliver && observation.NetworkPartition != nil &&
		observation.NetworkPartition.Blocks(selector.Link) {
		return skippedResolution(result, ReasonLinkPartitioned, fmt.Sprintf("link %s is blocked by the active network partition", selector.Link))
	}
	if action.Kind == ActionDeliver {
		target, found := observedNode(observation, selector.Link.To)
		if !found {
			return skippedResolution(result, ReasonNodeNotObserved, fmt.Sprintf("target node %s is not present in the observation", selector.Link.To))
		}
		if target.Status != core.NodeRunning {
			return skippedResolution(result, ReasonTargetNotRunning, fmt.Sprintf("target node %s is not running", selector.Link.To))
		}
	}

	queue := messagesOnLink(observation, selector.Link)
	for position, message := range queue {
		if message.Position != position {
			return invalidResolution(result, ReasonInvalidQueue, fmt.Sprintf(
				"link %s has non-contiguous position %d at queue index %d",
				selector.Link, message.Position, position,
			))
		}
	}
	if len(queue) == 0 {
		result.Status = ResolutionEmptyQueue
		result.ReasonCode = ReasonMessageNotAvailable
		result.Reason = fmt.Sprintf("link %s has no message at position %d", selector.Link, selector.Start)
		return result
	}
	start := selector.Start
	clamped := false
	if start >= len(queue) {
		if !r.config.ClampMessageStart {
			result.Status = ResolutionEmptyQueue
			result.ReasonCode = ReasonMessageNotAvailable
			result.Reason = fmt.Sprintf("link %s has no message at position %d", selector.Link, selector.Start)
			return result
		}
		start = len(queue) - 1
		clamped = true
	}

	end := start + selector.Count
	if end > len(queue) {
		end = len(queue)
	}
	selected := queue[start:end]
	result.Actions = make([]core.Action, len(selected))
	for i, message := range selected {
		position := message.Position
		if action.Kind == ActionDeliver || action.Kind == ActionDrop {
			// 前一个动作移除消息后，下一条选中消息会移动到相同的 Start。
			position = start
		}
		result.Actions[i] = core.Action{
			Kind:    concreteMessageKind(action.Kind),
			Message: message.ID,
			Selector: &core.MessageSelector{
				Link: selector.Link, Position: position,
			},
		}
	}
	result.Resolved = len(result.Actions)
	if result.Resolved < result.Requested {
		result.Status = ResolutionPartial
		if clamped {
			result.ReasonCode = ReasonSelectorStartClamped
			result.Reason = fmt.Sprintf("message start %d clamped to %d; requested %d messages but only %d are available", selector.Start, start, result.Requested, result.Resolved)
		} else {
			result.ReasonCode = ReasonPartialAvailability
			result.Reason = fmt.Sprintf("requested %d messages but only %d are available", result.Requested, result.Resolved)
		}
	} else {
		result.Status = ResolutionResolved
		if clamped {
			result.ReasonCode = ReasonSelectorStartClamped
			result.Reason = fmt.Sprintf("message start %d clamped to %d", selector.Start, start)
		}
	}
	return result
}

func (r *Resolver) resolveTime(result Resolution, action PlanAction, observation core.Observation) Resolution {
	if action.Ticks > r.config.MaxAdvanceTicks {
		return invalidResolution(result, ReasonAdvanceLimit, fmt.Sprintf("ticks %d exceeds MaxAdvanceTicks %d", action.Ticks, r.config.MaxAdvanceTicks))
	}
	target, err := observation.Time.Add(action.Ticks)
	if err != nil {
		return invalidResolution(result, ReasonTimeOverflow, err.Error())
	}
	result.Status = ResolutionResolved
	result.Resolved = 1
	result.Actions = []core.Action{{Kind: core.ActionAdvanceTime, TargetTime: target}}
	return result
}

func (r *Resolver) resolveNode(result Resolution, action PlanAction, observation core.Observation) Resolution {
	node, found := observedNode(observation, action.Node)
	if !found {
		result.Status = ResolutionSkipped
		result.ReasonCode = ReasonNodeNotObserved
		result.Reason = fmt.Sprintf("node %s is not present in the observation", action.Node)
		return result
	}

	switch action.Kind {
	case ActionCrash:
		if node.Status == core.NodeCrashed {
			return skippedResolution(result, ReasonNodeAlreadyCrashed, fmt.Sprintf("node %s is already crashed", action.Node))
		}
	case ActionRestart:
		if node.Status == core.NodeRunning {
			return skippedResolution(result, ReasonNodeAlreadyRunning, fmt.Sprintf("node %s is already running", action.Node))
		}
	case ActionTimeout:
		if node.Status != core.NodeRunning {
			return skippedResolution(result, ReasonTargetNotRunning, fmt.Sprintf("node %s is not running", action.Node))
		}
	case ActionRequest:
		if node.Status != core.NodeRunning {
			return skippedResolution(result, ReasonTargetNotRunning, fmt.Sprintf("node %s is not running", action.Node))
		}
	}

	concrete := core.Action{Kind: concreteNodeKind(action.Kind), Node: action.Node}
	if action.Kind == ActionRequest {
		concrete.Request = []byte(action.Request)
	}
	result.Status = ResolutionResolved
	result.Resolved = 1
	result.Actions = []core.Action{concrete}
	return result
}

func (r *Resolver) resolvePartition(result Resolution, action PlanAction, observation core.Observation) Resolution {
	switch action.Kind {
	case ActionPartition:
		if observation.NetworkPartition != nil {
			return skippedResolution(result, ReasonPartitionActive, "a network partition is already active")
		}
		nodes := make([]core.NodeID, len(observation.Nodes))
		for index, node := range observation.Nodes {
			nodes[index] = node.ID
		}
		if action.Partition == nil || !action.Partition.Covers(nodes) {
			return invalidResolution(result, ReasonPartitionNodes, "partition must cover every observed node exactly once")
		}
		partition := action.Partition.Normalized()
		result.Status = ResolutionResolved
		result.Resolved = 1
		result.Actions = []core.Action{{Kind: core.ActionPartition, Partition: &partition}}
		return result
	case ActionHeal:
		if observation.NetworkPartition == nil {
			return skippedResolution(result, ReasonPartitionInactive, "no network partition is active")
		}
		result.Status = ResolutionResolved
		result.Resolved = 1
		result.Actions = []core.Action{{Kind: core.ActionHeal}}
		return result
	default:
		return invalidResolution(result, ReasonUnknownActionKind, "unknown network action kind")
	}
}

func invalidResolution(result Resolution, code ResolutionReasonCode, reason string) Resolution {
	result.Status = ResolutionInvalid
	result.ReasonCode = code
	result.Reason = reason
	result.Resolved = 0
	result.Actions = nil
	return result
}

func skippedResolution(result Resolution, code ResolutionReasonCode, reason string) Resolution {
	result.Status = ResolutionSkipped
	result.ReasonCode = code
	result.Reason = reason
	result.Resolved = 0
	result.Actions = nil
	return result
}

func messagesOnLink(observation core.Observation, link core.LinkID) []core.MessageObservation {
	result := make([]core.MessageObservation, 0)
	for _, message := range observation.Messages {
		if message.From == link.From && message.To == link.To {
			result = append(result, message)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Position < result[j].Position })
	return result
}

func observedNode(observation core.Observation, id core.NodeID) (core.NodeObservation, bool) {
	for _, node := range observation.Nodes {
		if node.ID == id {
			return node, true
		}
	}
	return core.NodeObservation{}, false
}

func concreteMessageKind(kind ActionKind) core.ActionKind {
	switch kind {
	case ActionDeliver:
		return core.ActionDeliver
	case ActionDrop:
		return core.ActionDrop
	case ActionDuplicate:
		return core.ActionDuplicate
	default:
		return ""
	}
}

func concreteNodeKind(kind ActionKind) core.ActionKind {
	switch kind {
	case ActionTimeout:
		return core.ActionTimeout
	case ActionCrash:
		return core.ActionCrash
	case ActionRestart:
		return core.ActionRestart
	case ActionRequest:
		return core.ActionRequest
	default:
		return ""
	}
}
