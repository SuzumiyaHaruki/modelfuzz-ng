package plan

import (
	"fmt"

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/core"
)

// MessageRangeSelector 表示某条 Link 上从 Start 开始的 Count 条消息。
// Start 使用当前队列中的零基位置；具体 MessageID 只在 Resolve 时绑定。
type MessageRangeSelector struct {
	Link  core.LinkID `json:"link"`
	Start int         `json:"start"`
	Count int         `json:"count"`
}

func (s MessageRangeSelector) Validate() error {
	if err := s.Link.Validate(); err != nil {
		return fmt.Errorf("%w: invalid message link: %v", ErrInvalidPlan, err)
	}
	if s.Start < 0 {
		return fmt.Errorf("%w: message start must be non-negative", ErrInvalidPlan)
	}
	if s.Count <= 0 {
		return fmt.Errorf("%w: message count must be positive", ErrInvalidPlan)
	}
	return nil
}
