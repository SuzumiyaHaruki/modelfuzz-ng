// Package oracle 定义对真实系统状态进行在线安全性检查的最小接口。
package oracle

import (
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/core"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/model"
)

// Finding 是 Oracle 发现的一条可持久化违规记录。Step 为 0 时表示初始状态，
// 其他值是从 1 开始的 Concrete Action 序号。
type Finding struct {
	Oracle  string      `json:"oracle"`
	Code    string      `json:"code"`
	Message string      `json:"message"`
	Step    int         `json:"step"`
	Node    core.NodeID `json:"node,omitempty"`
	Term    uint64      `json:"term,omitempty"`
}

// Checker 在每次 Engine.Run 开始时重置状态，并逐条检查 Concrete Transition。
// 实现可以保存跨步骤历史，例如某个 term 曾经出现过的 leader。
type Checker interface {
	Reset(initial core.Observation) []Finding
	Check(transition model.Transition) []Finding
}
