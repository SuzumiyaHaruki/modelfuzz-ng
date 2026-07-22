package raft

import (
	"fmt"
	"strconv"

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/core"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/model"
)

var _ model.Profile = (*Mapper)(nil)

const (
	CodeProfileUnavailable      model.DecisionCode = "profile_unavailable"
	CodeAdvanceTermBound        model.DecisionCode = "advance_would_exceed_term"
	CodeTimerStateUnavailable   model.DecisionCode = "timer_state_unavailable"
	CodeCrashTargetNotRunning   model.DecisionCode = "crash_target_not_running"
	CodeRestartTargetNotCrashed model.DecisionCode = "restart_target_not_crashed"
	CodeRequestLogBound         model.DecisionCode = "request_log_bound"
	CodeRequestInvalidValue     model.DecisionCode = "request_invalid_value"
	CodeTimeoutTermBound        model.DecisionCode = "timeout_term_bound"
	CodeMessageMissing          model.DecisionCode = "message_missing"
	CodeMessageMetadataInvalid  model.DecisionCode = "message_metadata_invalid"
	CodeMessageTermBound        model.DecisionCode = "message_term_bound"
	CodeMessageLogBound         model.DecisionCode = "message_log_bound"
	CodeLeaderNoopLogBound      model.DecisionCode = "leader_noop_log_bound"
	CodeMessageTypeNotModeled   model.DecisionCode = "message_type_not_modeled"
	CodeLinkPartitioned         model.DecisionCode = "link_partitioned"
)

// ValidateAction 实现基础 Raft Profile 的执行前预检。Drop/Duplicate 只改变
// Runtime 网络，因此即使消息语义未建模也允许执行。
func (m *Mapper) ValidateAction(action core.Action, observation core.Observation) error {
	if m == nil {
		return model.UnsupportedCode(action, CodeProfileUnavailable, "raft mapper is nil")
	}
	switch action.Kind {
	case core.ActionAdvanceTime:
		if action.TargetTime <= observation.Time {
			return model.InapplicableCode(action, CodeTimerStateUnavailable, fmt.Sprintf(
				"target time %d must be later than current time %d", action.TargetTime, observation.Time,
			))
		}
		ticks := uint64(action.TargetTime - observation.Time)
		for _, node := range observation.Nodes {
			if node.Status != core.NodeRunning || roleOf(observation, node.ID) == "leader" {
				continue
			}
			fires, ok := maximumElectionFires(node, ticks)
			if !ok {
				return model.InapplicableCode(action, CodeTimerStateUnavailable, fmt.Sprintf(
					"node %s does not expose valid election timer state", node.ID,
				))
			}
			term := termOf(observation, node.ID)
			if term > m.config.LargestTerm {
				return model.BoundReachedCode(action, CodeAdvanceTermBound, fmt.Sprintf(
					"node %s term %d already exceeds LargestTerm %d", node.ID, term, m.config.LargestTerm,
				))
			}
			if fires > m.config.LargestTerm-term {
				return model.InapplicableCode(action, CodeAdvanceTermBound, fmt.Sprintf(
					"advancing %d ticks may make node %s exceed LargestTerm %d", ticks, node.ID, m.config.LargestTerm,
				))
			}
		}
	case core.ActionCrash:
		if statusOf(observation, action.Node) != core.NodeRunning {
			return model.InapplicableCode(action, CodeCrashTargetNotRunning, fmt.Sprintf("crash target %s is not running", action.Node))
		}
	case core.ActionRestart:
		if statusOf(observation, action.Node) != core.NodeCrashed {
			return model.InapplicableCode(action, CodeRestartTargetNotCrashed, fmt.Sprintf("restart target %s is not crashed", action.Node))
		}
	case core.ActionRequest:
		if roleOf(observation, action.Node) == "leader" && lastIndexOf(observation, action.Node) >= m.config.MaxLogIndex {
			return model.BoundReachedCode(action, CodeRequestLogBound, fmt.Sprintf("request reaches MaxLogIndex %d", m.config.MaxLogIndex))
		}
		if _, err := numericRequest(action.Request, m.config.MaxValue); err != nil {
			return model.UnsupportedCode(action, CodeRequestInvalidValue, err.Error())
		}
	case core.ActionTimeout:
		if termOf(observation, action.Node) >= m.config.LargestTerm {
			return model.BoundReachedCode(action, CodeTimeoutTermBound, fmt.Sprintf("timeout reaches LargestTerm %d", m.config.LargestTerm))
		}
	case core.ActionDeliver:
		message, ok := observedMessage(observation, action.Message)
		if !ok {
			return model.InapplicableCode(action, CodeMessageMissing, fmt.Sprintf("message %s is absent from observation", action.Message))
		}
		if message.Blocked {
			return model.InapplicableCode(action, CodeLinkPartitioned, fmt.Sprintf("message %s crosses an active network partition", action.Message))
		}
		if err := m.validateMessageMetadataBounds(action, message); err != nil {
			return err
		}
		switch message.TypeHint {
		case "MsgVote", "MsgAppResp", "MsgHeartbeat",
			"MsgHeartbeatResp", "MsgReadIndex", "MsgReadIndexResp", "MsgSnap":
			return nil
		case "MsgProp":
			count, err := strconv.ParseUint(message.Metadata["entry_count"], 10, 64)
			if err != nil || count == 0 {
				return model.UnsupportedCode(action, CodeMessageMetadataInvalid, "MsgProp has no positive entry_count metadata")
			}
			if roleOf(observation, message.To) == "leader" {
				index := lastIndexOf(observation, message.To)
				if index > m.config.MaxLogIndex || count > m.config.MaxLogIndex-index {
					return model.BoundReachedCode(action, CodeMessageLogBound, fmt.Sprintf(
						"MsgProp adds %d entries beyond MaxLogIndex %d", count, m.config.MaxLogIndex,
					))
				}
			}
		case "MsgApp":
			count, err := strconv.Atoi(message.Metadata["entry_count"])
			if err != nil || count < 0 {
				return model.UnsupportedCode(action, CodeMessageMetadataInvalid, "MsgApp has no valid entry_count metadata")
			}
			index, err := strconv.ParseUint(message.Metadata["index"], 10, 64)
			if err != nil {
				return model.UnsupportedCode(action, CodeMessageMetadataInvalid, "MsgApp has no valid index metadata")
			}
			if index > m.config.MaxLogIndex || uint64(count) > m.config.MaxLogIndex-index {
				return model.BoundReachedCode(action, CodeMessageLogBound, fmt.Sprintf(
					"MsgApp index %q plus %d entries exceeds MaxLogIndex %d",
					message.Metadata["index"], count, m.config.MaxLogIndex,
				))
			}
		case "MsgVoteResp":
			rejected, err := strconv.ParseBool(message.Metadata["reject"])
			if err != nil {
				return model.UnsupportedCode(action, CodeMessageMetadataInvalid, "MsgVoteResp has no valid reject metadata")
			}
			if !rejected && roleOf(observation, message.To) == "candidate" &&
				lastIndexOf(observation, message.To) >= m.config.MaxLogIndex {
				return model.BoundReachedCode(action, CodeLeaderNoopLogBound, fmt.Sprintf(
					"successful vote may append leader no-op beyond MaxLogIndex %d", m.config.MaxLogIndex,
				))
			}
		default:
			return model.UnsupportedCode(action, CodeMessageTypeNotModeled, "message type "+message.TypeHint+" is not represented")
		}
	case core.ActionPartition, core.ActionHeal:
		// 网络拓扑由 Runtime 管理；基础 Raft TLA+ 模型没有网络变量，因而
		// partition/heal 本身是 stutter，后续实际投递仍按消息事件映射。
		return nil
	}
	return nil
}

func (m *Mapper) validateMessageMetadataBounds(action core.Action, message core.MessageObservation) error {
	termFields := []string{"term"}
	if message.TypeHint == "MsgVote" || message.TypeHint == "MsgApp" {
		termFields = append(termFields, "log_term")
	} else if message.TypeHint == "MsgSnap" {
		termFields = append(termFields, "snapshot_term")
	}
	for _, field := range termFields {
		value, err := strconv.ParseUint(message.Metadata[field], 10, 64)
		if err != nil {
			return model.UnsupportedCode(action, CodeMessageMetadataInvalid, fmt.Sprintf("%s has no valid %s metadata", message.TypeHint, field))
		}
		if value > m.config.LargestTerm {
			return model.BoundReachedCode(action, CodeMessageTermBound, fmt.Sprintf(
				"%s %s %d exceeds LargestTerm %d", message.TypeHint, field, value, m.config.LargestTerm,
			))
		}
	}
	indexFields := make([]string, 0, 2)
	switch message.TypeHint {
	case "MsgVote":
		indexFields = append(indexFields, "index")
	case "MsgApp":
		indexFields = append(indexFields, "index", "commit")
	case "MsgAppResp":
		indexFields = append(indexFields, "index")
	case "MsgHeartbeat":
		indexFields = append(indexFields, "commit")
	case "MsgSnap":
		indexFields = append(indexFields, "snapshot_index")
	}
	for _, field := range indexFields {
		value, err := strconv.ParseUint(message.Metadata[field], 10, 64)
		if err != nil {
			return model.UnsupportedCode(action, CodeMessageMetadataInvalid, fmt.Sprintf("%s has no valid %s metadata", message.TypeHint, field))
		}
		if value > m.config.MaxLogIndex {
			return model.BoundReachedCode(action, CodeMessageLogBound, fmt.Sprintf(
				"%s %s %d exceeds MaxLogIndex %d", message.TypeHint, field, value, m.config.MaxLogIndex,
			))
		}
	}
	return nil
}

func maximumElectionFires(node core.NodeObservation, ticks uint64) (uint64, bool) {
	remaining, remainingOK := unsignedParam(node.Semantic["election_ticks_remaining"])
	electionTimeout, timeoutOK := unsignedParam(node.Semantic["election_timeout"])
	if !remainingOK || !timeoutOK || electionTimeout == 0 {
		return 0, false
	}
	if ticks == 0 {
		return 0, true
	}
	if remaining == 0 {
		remaining = 1
	}
	if ticks < remaining {
		return 0, true
	}
	return 1 + (ticks-remaining)/electionTimeout, true
}

func observedMessage(observation core.Observation, id core.MessageID) (core.MessageObservation, bool) {
	for _, message := range observation.Messages {
		if message.ID == id {
			return message, true
		}
	}
	return core.MessageObservation{}, false
}
