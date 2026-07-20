package tlc

// State 是 TLC 返回的一项模型状态表示及其稳定键。结果通常包含初始状态，
// 且服务端可能合并连续重复状态，因此它不与输入 Event 按下标一一对应。
type State struct {
	Text string `json:"state"`
	Key  int64  `json:"key"`
}

type executeResponse struct {
	States []string `json:"States"`
	Keys   []int64  `json:"Keys"`
}
