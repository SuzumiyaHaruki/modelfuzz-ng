package tlc

import "github.com/SuzumiyaHaruki/modelfuzz-ng/internal/model"

// State 保留原有包名兼容性；通用定义位于 model，使 Engine 不依赖 TLC。
// 结果通常包含初始状态，且服务端可能合并连续重复状态，因此它不与输入
// Event 按下标一一对应。
type State = model.State

type executeResponse struct {
	States []string `json:"States"`
	Keys   []int64  `json:"Keys"`
}
