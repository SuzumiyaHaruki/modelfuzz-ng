// Package corpus 保存触发了全局新模型状态的 Plan 和紧凑执行摘要。
package corpus

import (
	"fmt"
	"sort"
	"sync"

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
	States   []model.State
}

// Entry 只在一次执行至少发现一个全局新模型状态时创建。
// 具体 Action、完整 Trace 和本次访问的全部状态都已经保存在 runs/artifact 中；
// Corpus 只保留后续变异真正需要的 Plan 和增量状态键。
type Entry struct {
	ID           string            `json:"id"`
	ParentID     string            `json:"parent_id,omitempty"`
	Source       string            `json:"source"`
	Depth        int               `json:"depth"`
	RunIndex     int               `json:"run_index"`
	Seed         int64             `json:"seed"`
	Plan         plan.PlanSequence `json:"plan"`
	NewStateKeys []int64           `json:"new_state_keys"`
}

// Snapshot 是可直接持久化的 Corpus 快照。
type Snapshot struct {
	CoverageKeys []int64 `json:"coverage_keys"`
	Entries      []Entry `json:"entries"`
}

// Checkpoint 是写入 experiment checkpoint 的紧凑 Corpus 水位。完整条目采用
// corpus.jsonl 追加保存，避免每次 checkpoint 重写全部 Plan。
type Checkpoint struct {
	CoverageKeys []int64 `json:"coverage_keys"`
	EntryCount   int     `json:"entry_count"`
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
	if len(seenNew) != len(coverage) {
		return nil, fmt.Errorf("corpus entries introduce %d states but coverage contains %d", len(seenNew), len(coverage))
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
		Plan: input.Plan.Copy(), NewStateKeys: append([]int64(nil), newKeys...),
	}
	c.entries = append(c.entries, entry)
	return copyEntry(entry), true, nil
}

// RollbackLast 只用于持久化新增条目失败的边界：撤回尚未对外提交的最后一次
// Consider，确保旧 checkpoint 与 corpus.jsonl 仍可恢复。
func (c *Corpus) RollbackLast(entry Entry) error {
	if c == nil {
		return fmt.Errorf("corpus is nil")
	}
	c.mutex.Lock()
	defer c.mutex.Unlock()
	if len(c.entries) == 0 || c.entries[len(c.entries)-1].ID != entry.ID {
		return fmt.Errorf("corpus rollback entry %s is not the latest entry", entry.ID)
	}
	for _, key := range entry.NewStateKeys {
		delete(c.coverage, key)
	}
	c.entries = c.entries[:len(c.entries)-1]
	return nil
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

func (c *Corpus) Entry(id string) (Entry, bool) {
	if c == nil {
		return Entry{}, false
	}
	c.mutex.RLock()
	defer c.mutex.RUnlock()
	for _, entry := range c.entries {
		if entry.ID == id {
			return copyEntry(entry), true
		}
	}
	return Entry{}, false
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

func (c *Corpus) Checkpoint() Checkpoint {
	if c == nil {
		return Checkpoint{CoverageKeys: make([]int64, 0)}
	}
	c.mutex.RLock()
	defer c.mutex.RUnlock()
	checkpoint := Checkpoint{CoverageKeys: make([]int64, 0, len(c.coverage)), EntryCount: len(c.entries)}
	for key := range c.coverage {
		checkpoint.CoverageKeys = append(checkpoint.CoverageKeys, key)
	}
	sort.Slice(checkpoint.CoverageKeys, func(i, j int) bool { return checkpoint.CoverageKeys[i] < checkpoint.CoverageKeys[j] })
	return checkpoint
}

// RestoreCheckpoint 将紧凑 checkpoint 与 corpus.jsonl 中前 EntryCount 条记录
// 组合成可验证的内存 Corpus。
func RestoreCheckpoint(checkpoint Checkpoint, entries []Entry) (*Corpus, error) {
	if checkpoint.EntryCount < 0 || checkpoint.EntryCount != len(entries) {
		return nil, fmt.Errorf("corpus checkpoint requires %d entries, got %d", checkpoint.EntryCount, len(entries))
	}
	return Restore(Snapshot{
		CoverageKeys: append([]int64(nil), checkpoint.CoverageKeys...),
		Entries:      append([]Entry(nil), entries...),
	})
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
	entry.NewStateKeys = append([]int64(nil), entry.NewStateKeys...)
	return entry
}
