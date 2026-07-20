package raft

import (
	"fmt"
	"strconv"

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/core"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/model"
)

var _ model.Profile = (*Mapper)(nil)

// ValidateAction 实现基础 Raft Profile 的执行前预检。Drop/Duplicate 只改变
// Runtime 网络，因此即使消息语义未建模也允许执行。
func (m *Mapper) ValidateAction(action core.Action, observation core.Observation) error {
	if m == nil {
		return model.Unsupported(action, "raft mapper is nil")
	}
	switch action.Kind {
	case core.ActionCrash, core.ActionRestart:
		return model.Unsupported(action, "crash/restart is not represented by the basic raft model")
	case core.ActionRequest:
		if roleOf(observation, action.Node) != "leader" {
			return model.Unsupported(action, fmt.Sprintf("client request target %s is not leader", action.Node))
		}
		if lastIndexOf(observation, action.Node) >= m.config.MaxLogIndex {
			return model.Unsupported(action, fmt.Sprintf("request exceeds MaxLogIndex %d", m.config.MaxLogIndex))
		}
		if _, err := numericRequest(action.Request, m.config.MaxValue); err != nil {
			return model.Unsupported(action, err.Error())
		}
	case core.ActionTimeout:
		if termOf(observation, action.Node) >= m.config.LargestTerm {
			return model.Unsupported(action, fmt.Sprintf("timeout exceeds LargestTerm %d", m.config.LargestTerm))
		}
	case core.ActionDeliver:
		message, ok := observedMessage(observation, action.Message)
		if !ok {
			return model.Unsupported(action, fmt.Sprintf("message %s is absent from observation", action.Message))
		}
		switch message.TypeHint {
		case "MsgVote", "MsgVoteResp", "MsgAppResp", "MsgHeartbeat",
			"MsgHeartbeatResp", "MsgReadIndex", "MsgReadIndexResp":
			return nil
		case "MsgApp":
			count, err := strconv.Atoi(message.Metadata["entry_count"])
			if err != nil || count < 0 {
				return model.Unsupported(action, "MsgApp has no valid entry_count metadata")
			}
			index, err := strconv.ParseUint(message.Metadata["index"], 10, 64)
			if err != nil || index > m.config.MaxLogIndex || uint64(count) > m.config.MaxLogIndex-index {
				return model.Unsupported(action, fmt.Sprintf(
					"MsgApp index %q plus %d entries exceeds MaxLogIndex %d",
					message.Metadata["index"], count, m.config.MaxLogIndex,
				))
			}
		default:
			return model.Unsupported(action, "message type "+message.TypeHint+" is not represented")
		}
	}
	return nil
}

func observedMessage(observation core.Observation, id core.MessageID) (core.MessageObservation, bool) {
	for _, message := range observation.Messages {
		if message.ID == id {
			return message, true
		}
	}
	return core.MessageObservation{}, false
}
