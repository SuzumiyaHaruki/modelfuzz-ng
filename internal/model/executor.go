package model

import "context"

// State 是模型执行器返回的一项状态表示及其稳定键。不同模型后端可以用
// 不同文本格式描述状态，但 Key 应在同一后端和配置下保持稳定。
type State struct {
	Text string `json:"state"`
	Key  int64  `json:"key"`
}

// Executor 执行一组已经映射完成的模型事件。实现可以是 controlled TLC、
// 本地解释器或测试替身；Engine 不依赖具体传输协议。
type Executor interface {
	Execute(ctx context.Context, events []Event) ([]State, error)
}
