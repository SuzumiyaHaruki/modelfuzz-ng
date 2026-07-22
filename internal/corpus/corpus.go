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
	ParentID               string
	Source                 string
	Depth                  int
	RunIndex               int
	Seed                   int64
	Plan                   plan.PlanSequence
	States                 []model.State
	SemanticStateKeys      []int64
	SemanticTransitionKeys []int64
}

type Config struct {
	MinNewModelStates      int
	RequireSemanticNovelty bool
}

type Projection struct {
	StateKeys      []int64
	TransitionKeys []int64
}

// AdmissionReason 是一次成功执行经过覆盖门槛后的稳定判定码。它只描述
// Corpus 准入，不与 mutation ready 队列的 admitted/discarded 混用。
type AdmissionReason string

const (
	AdmissionRetainedRaw                        AdmissionReason = "retained_raw"
	AdmissionRetainedSemanticState              AdmissionReason = "retained_semantic_state"
	AdmissionRetainedSemanticTransition         AdmissionReason = "retained_semantic_transition"
	AdmissionRetainedSemanticStateAndTransition AdmissionReason = "retained_semantic_state_and_transition"
	AdmissionRejectedRawThreshold               AdmissionReason = "rejected_raw_threshold"
	AdmissionRejectedNoSemanticNovelty          AdmissionReason = "rejected_no_semantic_novelty"
)

// Entry 只在一次执行满足原始状态门槛和可选语义覆盖门槛时创建。
// 具体 Action、完整 Trace 和本次访问的全部状态都已经保存在 runs/artifact 中；
// Corpus 只保留后续变异真正需要的 Plan 和增量状态键。
type Entry struct {
	ID                        string            `json:"id"`
	ParentID                  string            `json:"parent_id,omitempty"`
	Source                    string            `json:"source"`
	Depth                     int               `json:"depth"`
	RunIndex                  int               `json:"run_index"`
	Seed                      int64             `json:"seed"`
	Plan                      plan.PlanSequence `json:"plan"`
	NewStateKeys              []int64           `json:"new_state_keys"`
	NewSemanticStateKeys      []int64           `json:"new_semantic_state_keys,omitempty"`
	NewSemanticTransitionKeys []int64           `json:"new_semantic_transition_keys,omitempty"`
	// AdmissionReason 是 Consider 的瞬时判定，不写入 corpus.jsonl；被保留的
	// Entry 在恢复时无需重新解释当时的准入策略。
	AdmissionReason AdmissionReason `json:"-"`
}

// Snapshot 是可直接持久化的 Corpus 快照。
type Snapshot struct {
	CoverageKeys           []int64 `json:"coverage_keys"`
	SemanticStateKeys      []int64 `json:"semantic_state_keys,omitempty"`
	SemanticTransitionKeys []int64 `json:"semantic_transition_keys,omitempty"`
	Entries                []Entry `json:"entries"`
}

// Checkpoint 是写入 experiment checkpoint 的紧凑 Corpus 水位。完整条目采用
// corpus.jsonl 追加保存，避免每次 checkpoint 重写全部 Plan。
type Checkpoint struct {
	CoverageKeys           []int64 `json:"coverage_keys"`
	SemanticStateKeys      []int64 `json:"semantic_state_keys,omitempty"`
	SemanticTransitionKeys []int64 `json:"semantic_transition_keys,omitempty"`
	EntryCount             int     `json:"entry_count"`
}

// Corpus 分开维护原始 State.Key、语义状态和语义转移覆盖。未达到准入门槛的
// 成功运行仍会更新覆盖集合，避免相同状态在后续运行中被重复算作新覆盖。
type Corpus struct {
	mutex               sync.RWMutex
	config              Config
	coverage            map[int64]struct{}
	semanticStates      map[int64]struct{}
	semanticTransitions map[int64]struct{}
	entries             []Entry
}

func New() *Corpus {
	return NewWithConfig(Config{MinNewModelStates: 1})
}

func NewWithConfig(config Config) *Corpus {
	if config.MinNewModelStates <= 0 {
		config.MinNewModelStates = 1
	}
	return &Corpus{
		config: config, coverage: make(map[int64]struct{}), semanticStates: make(map[int64]struct{}),
		semanticTransitions: make(map[int64]struct{}), entries: make([]Entry, 0),
	}
}

// Restore 从持久化快照恢复 Corpus。恢复时重新校验覆盖集合、条目编号和
// NewStateKeys，避免损坏或来自其他实验的快照悄悄污染后续反馈。
func Restore(snapshot Snapshot) (*Corpus, error) {
	return RestoreWithConfig(snapshot, Config{MinNewModelStates: 1})
}

func RestoreWithConfig(snapshot Snapshot, config Config) (*Corpus, error) {
	result := NewWithConfig(config)
	coverage := make(map[int64]struct{}, len(snapshot.CoverageKeys))
	for _, key := range snapshot.CoverageKeys {
		if _, exists := coverage[key]; exists {
			return nil, fmt.Errorf("corpus snapshot contains duplicate coverage key %d", key)
		}
		coverage[key] = struct{}{}
	}
	semanticStates, err := uniqueKeySet(snapshot.SemanticStateKeys, "semantic state")
	if err != nil {
		return nil, err
	}
	semanticTransitions, err := uniqueKeySet(snapshot.SemanticTransitionKeys, "semantic transition")
	if err != nil {
		return nil, err
	}
	seenNew := make(map[int64]struct{})
	seenSemanticStates := make(map[int64]struct{})
	seenSemanticTransitions := make(map[int64]struct{})
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
		if err := validateEntryKeys(entry.ID, entry.NewSemanticStateKeys, semanticStates, seenSemanticStates, "semantic state"); err != nil {
			return nil, err
		}
		if err := validateEntryKeys(entry.ID, entry.NewSemanticTransitionKeys, semanticTransitions, seenSemanticTransitions, "semantic transition"); err != nil {
			return nil, err
		}
		result.entries = append(result.entries, copyEntry(entry))
	}
	result.coverage = coverage
	result.semanticStates = semanticStates
	result.semanticTransitions = semanticTransitions
	return result, nil
}

// Consider 原子地合并覆盖，并在满足准入门槛时保留输入。这样即使多个执行
// worker 同时完成，同一个覆盖键也只会归属于最先完成的运行。
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
	semanticStateKeys := uniqueInt64s(input.SemanticStateKeys)
	semanticTransitionKeys := uniqueInt64s(input.SemanticTransitionKeys)

	c.mutex.Lock()
	defer c.mutex.Unlock()
	newKeys := make([]int64, 0, len(stateKeys))
	for _, key := range stateKeys {
		if _, exists := c.coverage[key]; !exists {
			newKeys = append(newKeys, key)
		}
	}
	newSemanticStates := unseenKeys(semanticStateKeys, c.semanticStates)
	newSemanticTransitions := unseenKeys(semanticTransitionKeys, c.semanticTransitions)
	for _, key := range stateKeys {
		c.coverage[key] = struct{}{}
	}
	for _, key := range semanticStateKeys {
		c.semanticStates[key] = struct{}{}
	}
	for _, key := range semanticTransitionKeys {
		c.semanticTransitions[key] = struct{}{}
	}
	entry := Entry{
		Source: input.Source, Depth: input.Depth, RunIndex: input.RunIndex, Seed: input.Seed,
		NewStateKeys:              append([]int64(nil), newKeys...),
		NewSemanticStateKeys:      append([]int64(nil), newSemanticStates...),
		NewSemanticTransitionKeys: append([]int64(nil), newSemanticTransitions...),
	}
	semanticNovelty := len(newSemanticStates)+len(newSemanticTransitions) > 0
	if len(newKeys) < c.config.MinNewModelStates {
		entry.AdmissionReason = AdmissionRejectedRawThreshold
		return entry, false, nil
	}
	if c.config.RequireSemanticNovelty && !semanticNovelty {
		entry.AdmissionReason = AdmissionRejectedNoSemanticNovelty
		return entry, false, nil
	}
	entry.AdmissionReason = retainedAdmissionReason(c.config.RequireSemanticNovelty, newSemanticStates, newSemanticTransitions)
	entry.ID = fmt.Sprintf("corpus-%06d", len(c.entries))
	entry.ParentID = input.ParentID
	entry.Plan = input.Plan.Copy()
	c.entries = append(c.entries, entry)
	return copyEntry(entry), true, nil
}

func retainedAdmissionReason(requireSemantic bool, states, transitions []int64) AdmissionReason {
	if !requireSemantic {
		return AdmissionRetainedRaw
	}
	if len(states) > 0 && len(transitions) > 0 {
		return AdmissionRetainedSemanticStateAndTransition
	}
	if len(states) > 0 {
		return AdmissionRetainedSemanticState
	}
	return AdmissionRetainedSemanticTransition
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
	for _, key := range entry.NewSemanticStateKeys {
		delete(c.semanticStates, key)
	}
	for _, key := range entry.NewSemanticTransitionKeys {
		delete(c.semanticTransitions, key)
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

func (c *Corpus) SemanticCoverageLen() (int, int) {
	if c == nil {
		return 0, 0
	}
	c.mutex.RLock()
	defer c.mutex.RUnlock()
	return len(c.semanticStates), len(c.semanticTransitions)
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
		CoverageKeys:           make([]int64, 0, len(c.coverage)),
		SemanticStateKeys:      make([]int64, 0, len(c.semanticStates)),
		SemanticTransitionKeys: make([]int64, 0, len(c.semanticTransitions)),
		Entries:                make([]Entry, len(c.entries)),
	}
	for key := range c.coverage {
		snapshot.CoverageKeys = append(snapshot.CoverageKeys, key)
	}
	for key := range c.semanticStates {
		snapshot.SemanticStateKeys = append(snapshot.SemanticStateKeys, key)
	}
	for key := range c.semanticTransitions {
		snapshot.SemanticTransitionKeys = append(snapshot.SemanticTransitionKeys, key)
	}
	sort.Slice(snapshot.CoverageKeys, func(i, j int) bool { return snapshot.CoverageKeys[i] < snapshot.CoverageKeys[j] })
	sort.Slice(snapshot.SemanticStateKeys, func(i, j int) bool { return snapshot.SemanticStateKeys[i] < snapshot.SemanticStateKeys[j] })
	sort.Slice(snapshot.SemanticTransitionKeys, func(i, j int) bool { return snapshot.SemanticTransitionKeys[i] < snapshot.SemanticTransitionKeys[j] })
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
	checkpoint := Checkpoint{
		CoverageKeys:           make([]int64, 0, len(c.coverage)),
		SemanticStateKeys:      make([]int64, 0, len(c.semanticStates)),
		SemanticTransitionKeys: make([]int64, 0, len(c.semanticTransitions)), EntryCount: len(c.entries),
	}
	for key := range c.coverage {
		checkpoint.CoverageKeys = append(checkpoint.CoverageKeys, key)
	}
	for key := range c.semanticStates {
		checkpoint.SemanticStateKeys = append(checkpoint.SemanticStateKeys, key)
	}
	for key := range c.semanticTransitions {
		checkpoint.SemanticTransitionKeys = append(checkpoint.SemanticTransitionKeys, key)
	}
	sort.Slice(checkpoint.CoverageKeys, func(i, j int) bool { return checkpoint.CoverageKeys[i] < checkpoint.CoverageKeys[j] })
	sort.Slice(checkpoint.SemanticStateKeys, func(i, j int) bool { return checkpoint.SemanticStateKeys[i] < checkpoint.SemanticStateKeys[j] })
	sort.Slice(checkpoint.SemanticTransitionKeys, func(i, j int) bool {
		return checkpoint.SemanticTransitionKeys[i] < checkpoint.SemanticTransitionKeys[j]
	})
	return checkpoint
}

// RestoreCheckpoint 将紧凑 checkpoint 与 corpus.jsonl 中前 EntryCount 条记录
// 组合成可验证的内存 Corpus。
func RestoreCheckpoint(checkpoint Checkpoint, entries []Entry) (*Corpus, error) {
	return RestoreCheckpointWithConfig(checkpoint, entries, Config{MinNewModelStates: 1})
}

func RestoreCheckpointWithConfig(checkpoint Checkpoint, entries []Entry, config Config) (*Corpus, error) {
	if checkpoint.EntryCount < 0 || checkpoint.EntryCount != len(entries) {
		return nil, fmt.Errorf("corpus checkpoint requires %d entries, got %d", checkpoint.EntryCount, len(entries))
	}
	return RestoreWithConfig(Snapshot{
		CoverageKeys:           append([]int64(nil), checkpoint.CoverageKeys...),
		SemanticStateKeys:      append([]int64(nil), checkpoint.SemanticStateKeys...),
		SemanticTransitionKeys: append([]int64(nil), checkpoint.SemanticTransitionKeys...),
		Entries:                append([]Entry(nil), entries...),
	}, config)
}

func uniqueKeySet(keys []int64, label string) (map[int64]struct{}, error) {
	result := make(map[int64]struct{}, len(keys))
	for _, key := range keys {
		if _, exists := result[key]; exists {
			return nil, fmt.Errorf("corpus snapshot contains duplicate %s key %d", label, key)
		}
		result[key] = struct{}{}
	}
	return result, nil
}

func validateEntryKeys(entryID string, keys []int64, coverage, seen map[int64]struct{}, label string) error {
	for _, key := range keys {
		if _, covered := coverage[key]; !covered {
			return fmt.Errorf("corpus entry %s new %s %d is absent from coverage", entryID, label, key)
		}
		if _, duplicate := seen[key]; duplicate {
			return fmt.Errorf("corpus %s %d is new in more than one entry", label, key)
		}
		seen[key] = struct{}{}
	}
	return nil
}

func unseenKeys(keys []int64, coverage map[int64]struct{}) []int64 {
	result := make([]int64, 0, len(keys))
	for _, key := range keys {
		if _, exists := coverage[key]; !exists {
			result = append(result, key)
		}
	}
	return result
}

func uniqueInt64s(keys []int64) []int64 {
	set := make(map[int64]struct{}, len(keys))
	for _, key := range keys {
		set[key] = struct{}{}
	}
	result := make([]int64, 0, len(set))
	for key := range set {
		result = append(result, key)
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result
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
	entry.NewSemanticStateKeys = append([]int64(nil), entry.NewSemanticStateKeys...)
	entry.NewSemanticTransitionKeys = append([]int64(nil), entry.NewSemanticTransitionKeys...)
	return entry
}
