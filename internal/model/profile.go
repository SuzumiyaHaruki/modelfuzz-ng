package model

import (
	"errors"
	"fmt"

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/core"
)

// DecisionCode 是可跨实验稳定聚合的 Profile 判定原因。人类可读的错误文本
// 可以包含节点、消息和边界值，统计不得依赖这些动态文本。
type DecisionCode string

const DecisionCodeUnspecified DecisionCode = "unspecified"

type DecisionError struct {
	Category error
	Code     DecisionCode
	Action   core.ActionKind
	Reason   string
}

func (e *DecisionError) Error() string {
	return fmt.Sprintf("%v: %s: %s: %s", e.Category, e.Code, e.Action, e.Reason)
}

func (e *DecisionError) Unwrap() error { return e.Category }

var (
	// ErrActionInapplicable 表示动作在当前运行状态下暂时没有执行对象。例如，
	// 待投递消息已经不在队列中。这不是 SUT 失败，也不表示模型缺少相应语义。
	ErrActionInapplicable = errors.New("action is inapplicable in current state")
	// ErrModelBoundReached 表示动作本身受模型支持，但执行后会超出当前有限模型
	// 的状态边界。Engine 应正常结束当前前缀，而不是把它统计为失败。
	ErrModelBoundReached = errors.New("model bound reached")
	// ErrUnsupportedByProfile 只保留给当前 Mapper/Profile 确实无法表达的动作。
	ErrUnsupportedByProfile = errors.New("action is unsupported by model profile")
)

// Profile 在 Action 改变 SUT 前检查当前形式化模型是否能表达该转换。
// 它只判断模型能力边界；Runtime 仍负责节点状态和消息位置等执行前置条件。
type Profile interface {
	ValidateAction(action core.Action, observation core.Observation) error
}

func Unsupported(action core.Action, reason string) error {
	return UnsupportedCode(action, DecisionCodeUnspecified, reason)
}

func Inapplicable(action core.Action, reason string) error {
	return InapplicableCode(action, DecisionCodeUnspecified, reason)
}

func BoundReached(action core.Action, reason string) error {
	return BoundReachedCode(action, DecisionCodeUnspecified, reason)
}

func UnsupportedCode(action core.Action, code DecisionCode, reason string) error {
	return decisionError(ErrUnsupportedByProfile, action, code, reason)
}

func InapplicableCode(action core.Action, code DecisionCode, reason string) error {
	return decisionError(ErrActionInapplicable, action, code, reason)
}

func BoundReachedCode(action core.Action, code DecisionCode, reason string) error {
	return decisionError(ErrModelBoundReached, action, code, reason)
}

func CodeOf(err error) DecisionCode {
	var decision *DecisionError
	if errors.As(err, &decision) {
		return decision.Code
	}
	return DecisionCodeUnspecified
}

func decisionError(category error, action core.Action, code DecisionCode, reason string) error {
	if code == "" {
		code = DecisionCodeUnspecified
	}
	return &DecisionError{Category: category, Code: code, Action: action.Kind, Reason: reason}
}
