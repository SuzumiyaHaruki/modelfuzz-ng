package model

import (
	"errors"
	"fmt"

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/core"
)

var ErrUnsupportedByProfile = errors.New("action is unsupported by model profile")

// Profile 在 Action 改变 SUT 前检查当前形式化模型是否能表达该转换。
// 它只判断模型能力边界；Runtime 仍负责节点状态和消息位置等执行前置条件。
type Profile interface {
	ValidateAction(action core.Action, observation core.Observation) error
}

func Unsupported(action core.Action, reason string) error {
	return fmt.Errorf("%w: %s: %s", ErrUnsupportedByProfile, action.Kind, reason)
}
