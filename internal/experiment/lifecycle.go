package experiment

import (
	"fmt"
	"time"

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/corpus"
)

// Version 6 将完整 Corpus 条目移出 checkpoint，改由 corpus.jsonl 追加保存。
// checkpoint 只保留覆盖键、Corpus 水位、可恢复调度状态和增量汇总。
const CheckpointVersion uint32 = 6

type EventKind string

const (
	EventExperimentStarted   EventKind = "experiment_started"
	EventExperimentResumed   EventKind = "experiment_resumed"
	EventRunCompleted        EventKind = "run_completed"
	EventMutationCompleted   EventKind = "mutation_completed"
	EventExperimentCompleted EventKind = "experiment_completed"
	EventExperimentCanceled  EventKind = "experiment_canceled"
)

// Event 是追加到 progress.jsonl 的轻量生命周期记录。完整 Trace 单独按
// ArtifactPolicy 保存，journal 因而可以频繁 fsync 而不会急剧膨胀。
type Event struct {
	Sequence      uint64     `json:"sequence"`
	Kind          EventKind  `json:"kind"`
	At            time.Time  `json:"at"`
	Run           *Run       `json:"run,omitempty"`
	Candidate     *Candidate `json:"candidate,omitempty"`
	RunIndex      int        `json:"run_index,omitempty"`
	CandidateID   string     `json:"candidate_id,omitempty"`
	CorpusID      string     `json:"corpus_id,omitempty"`
	MutationCount int        `json:"mutation_count,omitempty"`
	Error         string     `json:"error,omitempty"`
	CompletedRuns int        `json:"completed_runs"`
	CorpusEntries int        `json:"corpus_entries"`
}

// ScheduledCandidate 是已经取得稳定 run index 和 seed、但在进程退出前尚未
// 完成的候选。恢复时会原样重跑，因此不会改变后续 seed 编号。
type ScheduledCandidate struct {
	Index     int       `json:"index"`
	Seed      int64     `json:"seed"`
	Candidate Candidate `json:"candidate"`
}

// PendingMutation 只在 checkpoint 中保存对 corpus.jsonl 条目的稳定引用；完整
// Entry 在恢复时按 Corpus 水位读回，避免 checkpoint 再次嵌入 Plan。
type PendingMutation struct {
	EntryID string `json:"entry_id"`
	Count   int    `json:"count"`
	Seed    int64  `json:"seed"`
}

// Checkpoint 保存继续反馈调度所需的全部状态。正在执行的候选会放入
// InFlight；进程恢复时允许重跑它们，因为它们尚未产生已提交的 Run 记录。
type Checkpoint struct {
	Version                  uint32               `json:"version"`
	SavedAt                  time.Time            `json:"saved_at"`
	Config                   Config               `json:"config"`
	Aggregation              AggregationSnapshot  `json:"aggregation"`
	Corpus                   corpus.Checkpoint    `json:"corpus"`
	Ready                    []Candidate          `json:"ready"`
	InFlight                 []ScheduledCandidate `json:"in_flight"`
	PendingMutations         []PendingMutation    `json:"pending_mutations"`
	NextCandidateID          int                  `json:"next_candidate_id"`
	NextRunIndex             int                  `json:"next_run_index"`
	Completed                int                  `json:"completed"`
	RunSummaryCount          int                  `json:"run_summary_count"`
	NextRandomSeedAt         int                  `json:"next_random_seed_at,omitempty"`
	RandomSeedsDue           int                  `json:"random_seeds_due,omitempty"`
	EventSequence            uint64               `json:"event_sequence"`
	ElapsedMillis            int64                `json:"elapsed_millis"`
	ConfigurationFingerprint string               `json:"configuration_fingerprint,omitempty"`
}

func (c Checkpoint) Validate(config Config, fingerprint string) error {
	if c.Version != CheckpointVersion {
		return fmt.Errorf("unsupported checkpoint version %d", c.Version)
	}
	if c.Config != config {
		return fmt.Errorf("checkpoint experiment config does not match requested config")
	}
	if c.ConfigurationFingerprint != fingerprint {
		return fmt.Errorf("checkpoint configuration fingerprint does not match current configuration")
	}
	if c.Completed < 0 || c.Completed > config.Runs || c.NextRunIndex < c.Completed || c.NextRunIndex > config.Runs {
		return fmt.Errorf("checkpoint scheduling counters are invalid")
	}
	if c.NextRandomSeedAt < 0 || c.RandomSeedsDue < 0 {
		return fmt.Errorf("checkpoint periodic random seed counters are invalid")
	}
	if config.RandomSeedInterval == 0 {
		if c.NextRandomSeedAt != 0 || c.RandomSeedsDue != 0 {
			return fmt.Errorf("checkpoint has periodic random seed state while injection is disabled")
		}
	} else if c.NextRandomSeedAt <= c.Completed {
		return fmt.Errorf("checkpoint next random seed threshold %d is not after completed count %d", c.NextRandomSeedAt, c.Completed)
	}
	if c.RunSummaryCount != c.Completed {
		return fmt.Errorf("checkpoint has %d completed runs but %d committed run summaries", c.Completed, c.RunSummaryCount)
	}
	if len(c.Ready) > config.MaxReadyCandidates || c.Aggregation.Report.PeakReadyCandidates < len(c.Ready) {
		return fmt.Errorf("checkpoint ready queue counters are invalid")
	}
	seenPending := make(map[string]struct{}, len(c.PendingMutations))
	validCorpusIDs := make(map[string]struct{}, c.Corpus.EntryCount)
	for index := 0; index < c.Corpus.EntryCount; index++ {
		validCorpusIDs[fmt.Sprintf("corpus-%06d", index)] = struct{}{}
	}
	for _, pending := range c.PendingMutations {
		if pending.EntryID == "" || pending.Count <= 0 {
			return fmt.Errorf("checkpoint contains invalid pending mutation")
		}
		if _, duplicate := seenPending[pending.EntryID]; duplicate {
			return fmt.Errorf("checkpoint contains duplicate pending mutation for %s", pending.EntryID)
		}
		if _, validEntry := validCorpusIDs[pending.EntryID]; !validEntry {
			return fmt.Errorf("checkpoint pending mutation references unknown corpus entry %s", pending.EntryID)
		}
		seenPending[pending.EntryID] = struct{}{}
	}
	if err := c.Aggregation.Validate(config, c.Completed); err != nil {
		return fmt.Errorf("checkpoint aggregation: %w", err)
	}
	completedIndices := make(map[int]struct{}, len(c.Aggregation.CompletedRunIndices))
	for _, index := range c.Aggregation.CompletedRunIndices {
		completedIndices[index] = struct{}{}
	}
	seenScheduled := make(map[int]struct{}, len(c.InFlight))
	for _, scheduled := range c.InFlight {
		if scheduled.Index < 0 || scheduled.Index >= c.NextRunIndex || scheduled.Seed != config.BaseSeed+int64(scheduled.Index) {
			return fmt.Errorf("checkpoint in-flight run %d has invalid index or seed", scheduled.Index)
		}
		if _, completed := completedIndices[scheduled.Index]; completed {
			return fmt.Errorf("checkpoint run %d is both completed and in-flight", scheduled.Index)
		}
		if _, duplicate := seenScheduled[scheduled.Index]; duplicate {
			return fmt.Errorf("checkpoint contains duplicate in-flight run %d", scheduled.Index)
		}
		seenScheduled[scheduled.Index] = struct{}{}
	}
	if c.Completed+len(c.InFlight) != c.NextRunIndex {
		return fmt.Errorf("checkpoint assigned runs are neither completed nor in-flight")
	}
	for index := 0; index < c.NextRunIndex; index++ {
		_, completed := completedIndices[index]
		_, scheduled := seenScheduled[index]
		if completed == scheduled {
			return fmt.Errorf("checkpoint run %d is not in exactly one scheduling state", index)
		}
	}
	if c.Corpus.EntryCount < 0 {
		return fmt.Errorf("checkpoint corpus entry count is negative")
	}
	if err := validateUniqueInt64s(c.Corpus.CoverageKeys); err != nil {
		return fmt.Errorf("checkpoint corpus coverage: %w", err)
	}
	if c.Aggregation.Report.CorpusEntries != c.Corpus.EntryCount {
		return fmt.Errorf("checkpoint summary and corpus entry counts differ")
	}
	if c.Aggregation.Report.UniqueModelStates != len(c.Aggregation.ModelStateKeys) {
		return fmt.Errorf("checkpoint aggregate model-state count is inconsistent")
	}
	modelStates := make(map[int64]struct{}, len(c.Aggregation.ModelStateKeys))
	for _, key := range c.Aggregation.ModelStateKeys {
		modelStates[key] = struct{}{}
	}
	for _, key := range c.Corpus.CoverageKeys {
		if _, exists := modelStates[key]; !exists {
			return fmt.Errorf("checkpoint corpus state %d is absent from aggregate coverage", key)
		}
	}
	return nil
}

// Completion 只在 Corpus 判定完成后发出，因此 Run.Retained 已经是最终值。
type Completion struct {
	Run       Run               `json:"run"`
	Candidate Candidate         `json:"candidate"`
	Execution FeedbackExecution `json:"-"`
}

// Hooks 将调度器与文件系统解耦。回调同步执行；返回错误会安全停止实验并
// 尝试保存最后一个检查点。
type Hooks struct {
	OnEvent       func(Event) error
	OnCorpusEntry func(corpus.Entry) error
	OnRunComplete func(Completion) error
	OnCheckpoint  func(Checkpoint) error
}
