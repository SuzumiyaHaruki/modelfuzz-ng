package tlc

import (
	"fmt"

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/model"
)

// State 保留原有包名兼容性；通用定义位于 model，使 Engine 不依赖 TLC。
// 结果通常包含初始状态，且服务端可能合并连续重复状态，因此它不与输入
// Event 按下标一一对应。
type State = model.State

type executeResponse struct {
	States []string `json:"States"`
	Keys   []int64  `json:"Keys"`
}

type executeErrorResponse struct {
	Error ExecutionError `json:"error"`
}

// ExecutionError 保留严格 TLC Server 返回的稳定错误分类。旧服务返回纯文本时，
// Client 仍使用普通错误，因而迁移期间可以同时连接两种服务。
type ExecutionError struct {
	StatusCode int    `json:"-"`
	Code       string `json:"code"`
	EventIndex int    `json:"event_index"`
	EventName  string `json:"event_name"`
	Message    string `json:"message"`
}

func (e *ExecutionError) Error() string {
	return fmt.Sprintf("TLC %s at event %d (%s): %s", e.Code, e.EventIndex, e.EventName, e.Message)
}
