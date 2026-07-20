// Package sut 定义 Runtime 与具体被测系统实现之间的协议无关接口。
//
// Runtime 负责逻辑时钟、消息队列和 Trace；Adapter 只负责驱动被测系统，
// 并将产生的结果转换成 core.Effect 和 core.Observation。
package sut

import (
	"context"

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/core"
)

// ResetOptions 是一次执行开始时传给 Adapter 的通用参数。
// 节点数量、协议开关等系统专有配置应当在创建具体 Adapter 时传入。
type ResetOptions struct {
	Seed int64
}

// Capabilities 声明具体 Adapter 能够执行或观察的可选能力。
// Runtime 据此拒绝不受支持的动作；Engine 可在执行 Plan 前用它进行预检。
type Capabilities struct {
	ForceTimeout  bool
	CrashRestart  bool
	ClientRequest bool
}

// Adapter 是所有被测系统实现需要满足的最小执行接口。
//
// Reset 只建立初始状态，不产生脱离 Action 的 Effect。Tick 表示推进
// 一个逻辑时间单位。AdvanceTime 由 Runtime 展开成多次 Tick，因此
// 一次 Tick 内发生的自然超时和新消息都带有同一个 at。Adapter 刚
// 产生的出站消息没有 MessageID 和链路序号，由 Runtime 注册后再写入 Trace。
// Drop 和 Duplicate 只改变 Runtime 的消息队列，不需要进入被测系统，因而
// 不属于这个接口。
type Adapter interface {
	Capabilities() Capabilities

	Reset(ctx context.Context, options ResetOptions) error
	Tick(ctx context.Context, at core.LogicalTime) ([]core.Effect, error)
	Deliver(ctx context.Context, at core.LogicalTime, message core.Message) ([]core.Effect, error)
	ForceTimeout(ctx context.Context, at core.LogicalTime, node core.NodeID) ([]core.Effect, error)
	Crash(ctx context.Context, at core.LogicalTime, node core.NodeID) ([]core.Effect, error)
	Restart(ctx context.Context, at core.LogicalTime, node core.NodeID) ([]core.Effect, error)
	Request(ctx context.Context, at core.LogicalTime, node core.NodeID, request []byte) ([]core.Effect, error)

	Observe(ctx context.Context, at core.LogicalTime) (core.Observation, error)
}
