package runtime

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/core"
)

// processAdapterEffects 校验 Adapter 边界，并为出站消息分配稳定身份。
// Adapter 返回的 EffectSendMessage 必须携带未注册消息；返回值中的消息则
// 已经进入 Runtime 网络，可以直接写入 Concrete Trace。
func (r *Runtime) processAdapterEffects(effects []core.Effect, at core.LogicalTime) ([]core.Effect, error) {
	limits := r.config.Limits
	if limits.MaxEffects != 0 && uint64(len(effects)) > limits.MaxEffects-r.effectCount {
		return nil, fmt.Errorf("%w: effects would exceed limit %d", ErrBudgetExceeded, limits.MaxEffects)
	}
	outboundCount := 0
	for _, effect := range effects {
		if effect.Kind == core.EffectSendMessage {
			outboundCount++
		}
	}
	if limits.MaxQueuedMessages != 0 && r.network.len()+outboundCount > limits.MaxQueuedMessages {
		return nil, fmt.Errorf("%w: messages would exceed queue limit %d", ErrBudgetExceeded, limits.MaxQueuedMessages)
	}

	prepared := make([]core.Effect, len(effects))
	for i, original := range effects {
		effect := original.Copy()
		if effect.At != at {
			return nil, fmt.Errorf(
				"%w: effect %d occurred at %d, want %d",
				ErrAdapterContract, i, effect.At, at,
			)
		}

		if effect.Kind == core.EffectSendMessage {
			if effect.Message == nil || effect.TimerFired != nil || effect.ModelEvent != nil {
				return nil, fmt.Errorf(
					"%w: send-message effect %d must contain only a message payload",
					ErrAdapterContract, i,
				)
			}
			if err := effect.Message.ValidateOutbound(); err != nil {
				return nil, fmt.Errorf(
					"%w: send-message effect %d contains invalid outbound message: %v",
					ErrAdapterContract, i, err,
				)
			}
		} else if err := effect.Validate(); err != nil {
			return nil, fmt.Errorf("%w: effect %d is invalid: %v", ErrAdapterContract, i, err)
		}
		prepared[i] = effect
	}

	concrete := make([]core.Effect, len(prepared))
	for i, effect := range prepared {
		if effect.Kind == core.EffectSendMessage {
			message, err := r.network.registerOutbound(effect.Message.Copy(), at)
			if err != nil {
				return nil, err
			}
			effect.Message = &message
		}
		if err := effect.Validate(); err != nil {
			return nil, fmt.Errorf("%w: concrete effect %d is invalid: %v", ErrAdapterContract, i, err)
		}
		concrete[i] = effect.Copy()
	}
	r.effectCount += uint64(len(concrete))
	return concrete, nil
}

func observationDigest(observation core.Observation) (string, error) {
	normalized := observation.Normalized()
	encoded, err := json.Marshal(normalized)
	if err != nil {
		return "", fmt.Errorf("%w: observation is not serializable: %v", ErrAdapterContract, err)
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

func validateNaturalTimeoutEffects(effects []core.Effect) error {
	for i, effect := range effects {
		if effect.Kind == core.EffectTimerFired && effect.TimerFired.Source != core.TimerFireNatural {
			return fmt.Errorf(
				"%w: tick effect %d reports a non-natural timeout",
				ErrAdapterContract, i,
			)
		}
	}
	return nil
}

func validateForcedTimeoutEffects(effects []core.Effect, node core.NodeID) error {
	found := false
	for i, effect := range effects {
		if effect.Kind != core.EffectTimerFired {
			continue
		}
		if effect.TimerFired.Source != core.TimerFireForced || effect.TimerFired.Node != node {
			return fmt.Errorf(
				"%w: forced-timeout effect %d has source %q and node %s, want forced timeout for %s",
				ErrAdapterContract, i, effect.TimerFired.Source, effect.TimerFired.Node, node,
			)
		}
		found = true
	}
	if !found {
		return fmt.Errorf(
			"%w: successful forced timeout for %s did not produce EffectTimerFired",
			ErrAdapterContract, node,
		)
	}
	return nil
}
