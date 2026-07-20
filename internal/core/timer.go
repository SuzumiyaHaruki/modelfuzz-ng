package core

import "math"

// LogicalTime 是一次执行内单调递增的逻辑时钟，与墙上时间没有关系。
// 每个 Adapter 自行定义一个时间单位的含义；etcd-raft Adapter 将一个单位
// 映射为所有存活节点各执行一次 Tick 的全局轮次。
type LogicalTime uint64

func (t LogicalTime) Add(delta uint64) (LogicalTime, error) {
	if delta > math.MaxUint64-uint64(t) {
		return 0, invalidValue("logical_time", "delta", "overflows logical time")
	}
	return t + LogicalTime(delta), nil
}

// TimerFireSource 区分自然到期的 timer 和 Plan 主动注入的强制超时。
// 它属于 Trace 元数据，不是独立的可执行 ActionKind。
type TimerFireSource string

const (
	TimerFireNatural TimerFireSource = "natural"
	TimerFireForced  TimerFireSource = "forced"
)

func (s TimerFireSource) Valid() bool {
	switch s {
	case TimerFireNatural, TimerFireForced:
		return true
	default:
		return false
	}
}
