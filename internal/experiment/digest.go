package experiment

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/core"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/model"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/plan"
)

// digestPlan 只包含动作，不让来源、父子关系和 mutation seed 等 Metadata
// 把语义相同的 Plan 误算成不同输入。
func digestPlan(sequence plan.PlanSequence) string {
	if len(sequence.Actions) == 0 {
		return ""
	}
	return digestValue(sequence.Actions)
}

// digestTrace 排除 ExecutionID、Seed 和 Metadata，保留具体 Action、Effect、
// 逻辑时间、节点快照和 ObservationDigest。这样不同执行编号下相同的具体行为
// 会得到同一个摘要。
func digestTrace(trace core.Trace) string {
	if trace.Version == 0 {
		return ""
	}
	value := struct {
		Version uint32            `json:"version"`
		Steps   []core.StepRecord `json:"steps"`
	}{Version: trace.Version, Steps: trace.Steps}
	return digestValue(value)
}

// digestStatePath 保留 TLC 返回 State.Key 的顺序和重复项。相同状态集合以
// 不同顺序到达时，应被视为不同模型路径。
func digestStatePath(states []model.State) string {
	if len(states) == 0 {
		return ""
	}
	keys := make([]int64, len(states))
	for index, state := range states {
		keys[index] = state.Key
	}
	return digestValue(keys)
}

func digestValue(value any) string {
	data, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

func markNew(set map[string]struct{}, digest string) bool {
	if digest == "" {
		return false
	}
	if _, exists := set[digest]; exists {
		return false
	}
	set[digest] = struct{}{}
	return true
}
