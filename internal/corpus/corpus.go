// Package corpus 保存触发了全局新模型状态的 Plan 和紧凑执行摘要。
package corpus

import (
	"fmt"
	"sort"
	"sync"

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/core"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/model"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/plan"
)

// Input 是一次成功执行交给 Corpus 判定的最小信息。
type Input struct {
	ParentID string
	Source   string
	Depth    int
	RunIndex int
	Seed     int64
	Plan     plan.PlanSequence
	Actions  core.ActionSequence
	States   []model.State
}

// Entry 只在一次执行至少发现一个全局新模型状态时创建。
// StateKeys 是本次执行访问的状态，NewStateKeys 是真正触发保留的增量。
type Entry struct {
	ID       string            `json:"id"`
	ParentID string            `json:"parent_id,omitempty"`
	Source   string            `json:"source"`
	Depth    int               `json:"depth"`
	RunIndex int               `json:"run_index"`
	Seed     int64             `json:"seed"`
	Plan     plan.PlanSequence `json:"plan"`
	// Actions 足以描述实际执行的具体序列；Effect、Observation 和 Payload 等
	// 大对象由逐运行产物按策略保存，不再复制进每个 Corpus/checkpoint 条目。
	Actions      core.ActionSequence `json:"actions"`
	StateKeys    []int64             `json:"state_keys"`
	NewStateKeys []int64             `json:"new_state_keys"`
}

// Snapshot 是可直接持久化的 Corpus 快照。
type Snapshot struct {
	CoverageKeys []int64 `json:"coverage_keys"`
	Entries      []Entry `json:"entries"`
}

// Corpus 使用模型执行器返回的稳定 State.Key 维护全局覆盖。
type Corpus struct {
	mutex    sync.RWMutex
	coverage map[int64]struct{}
	entries  []Entry
}

func New() *Corpus {
	return &Corpus{coverage: make(map[int64]struct{}), entries: make([]Entry, 0)}
}

// Restore 从持久化快照恢复 Corpus。恢复时重新校验覆盖集合、条目编号和
// NewStateKeys，避免损坏或来自其他实验的快照悄悄污染后续反馈。
func Restore(snapshot Snapshot) (*Corpus, error) {
	result := New()
	coverage := make(map[int64]struct{}, len(snapshot.CoverageKeys))
	for _, key := range snapshot.CoverageKeys {
		if _, exists := coverage[key]; exists {
			return nil, fmt.Errorf("corpus snapshot contains duplicate coverage key %d", key)
		}
		coverage[key] = struct{}{}
	}
	seenNew := make(map[int64]struct{})
	for index, entry := range snapshot.Entries {
		if entry.ID != fmt.Sprintf("corpus-%06d", index) {
			return nil, fmt.Errorf("corpus entry %d has unexpected ID %q", index, entry.ID)
		}
		if entry.Depth < 0 || entry.RunIndex < 0 {
			return nil, fmt.Errorf("corpus entry %s has negative depth or run index", entry.ID)
		}
		if err := entry.Plan.Validate(); err != nil {
			return nil, fmt.Errorf("corpus entry %s plan: %w", entry.ID, err)
		}
		if err := entry.Actions.Validate(); err != nil {
			return nil, fmt.Errorf("corpus entry %s actions: %w", entry.ID, err)
		}
		for _, key := range entry.NewStateKeys {
			if _, covered := coverage[key]; !covered {
				return nil, fmt.Errorf("corpus entry %s new state %d is absent from coverage", entry.ID, key)
			}
			if _, duplicate := seenNew[key]; duplicate {
				return nil, fmt.Errorf("corpus state %d is new in more than one entry", key)
			}
			seenNew[key] = struct{}{}
		}
		result.entries = append(result.entries, copyEntry(entry))
	}
	result.coverage = coverage
	return result, nil
}

// Consider 原子地合并覆盖，并在存在增量时保留输入。这样即使多个执行
// worker 同时完成，同一个模型状态也只会使一条轨迹进入 Corpus。
func (c *Corpus) Consider(input Input) (Entry, bool, error) {
	if c == nil {
		return Entry{}, false, fmt.Errorf("corpus is nil")
	}
	if err := input.Plan.Validate(); err != nil {
		return Entry{}, false, fmt.Errorf("invalid corpus plan: %w", err)
	}
	if len(input.Plan.Actions) == 0 {
		return Entry{}, false, fmt.Errorf("corpus plan must not be empty")
	}
	if err := input.Actions.Validate(); err != nil {
		return Entry{}, false, fmt.Errorf("invalid corpus actions: %w", err)
	}
	if input.Depth < 0 || input.RunIndex < 0 {
		return Entry{}, false, fmt.Errorf("corpus depth and run index must be non-negative")
	}
	stateKeys := uniqueStateKeys(input.States)

	c.mutex.Lock()
	defer c.mutex.Unlock()
	newKeys := make([]int64, 0, len(stateKeys))
	for _, key := range stateKeys {
		if _, exists := c.coverage[key]; !exists {
			newKeys = append(newKeys, key)
		}
	}
	if len(newKeys) == 0 {
		return Entry{}, false, nil
	}
	for _, key := range newKeys {
		c.coverage[key] = struct{}{}
	}
	entry := Entry{
		ID: fmt.Sprintf("corpus-%06d", len(c.entries)), ParentID: input.ParentID,
		Source: input.Source, Depth: input.Depth, RunIndex: input.RunIndex, Seed: input.Seed,
		Plan: input.Plan.Copy(), Actions: input.Actions.Copy(),
		StateKeys: append([]int64(nil), stateKeys...), NewStateKeys: append([]int64(nil), newKeys...),
	}
	c.entries = append(c.entries, entry)
	return copyEntry(entry), true, nil
}

func (c *Corpus) Len() int {
	if c == nil {
		return 0
	}
	c.mutex.RLock()
	defer c.mutex.RUnlock()
	return len(c.entries)
}

func (c *Corpus) CoverageLen() int {
	if c == nil {
		return 0
	}
	c.mutex.RLock()
	defer c.mutex.RUnlock()
	return len(c.coverage)
}

func (c *Corpus) Snapshot() Snapshot {
	if c == nil {
		return Snapshot{CoverageKeys: make([]int64, 0), Entries: make([]Entry, 0)}
	}
	c.mutex.RLock()
	defer c.mutex.RUnlock()
	snapshot := Snapshot{
		CoverageKeys: make([]int64, 0, len(c.coverage)),
		Entries:      make([]Entry, len(c.entries)),
	}
	for key := range c.coverage {
		snapshot.CoverageKeys = append(snapshot.CoverageKeys, key)
	}
	sort.Slice(snapshot.CoverageKeys, func(i, j int) bool { return snapshot.CoverageKeys[i] < snapshot.CoverageKeys[j] })
	for index, entry := range c.entries {
		snapshot.Entries[index] = copyEntry(entry)
	}
	return snapshot
}

func uniqueStateKeys(states []model.State) []int64 {
	set := make(map[int64]struct{}, len(states))
	for _, state := range states {
		set[state.Key] = struct{}{}
	}
	keys := make([]int64, 0, len(set))
	for key := range set {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	return keys
}

func copyEntry(entry Entry) Entry {
	entry.Plan = entry.Plan.Copy()
	entry.Actions = entry.Actions.Copy()
	entry.StateKeys = append([]int64(nil), entry.StateKeys...)
	entry.NewStateKeys = append([]int64(nil), entry.NewStateKeys...)
	return entry
}
