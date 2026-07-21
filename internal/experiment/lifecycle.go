package experiment

import (
	"fmt"
	"time"

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/corpus"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/mutation"
)

// Version 4 对应 NG 自有严格 TLC Server 和补强后的 Raft invariant。官方
// TLC v1.8.0 的状态 fingerprint 与旧 ModelFuzz fork 不同，因此禁止把 v3
// Corpus/coverage 接到新模型执行语义后继续运行。
const CheckpointVersion uint32 = 4

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

// Checkpoint 保存继续反馈调度所需的全部状态。正在执行的候选会放入
// InFlight；进程恢复时允许重跑它们，因为它们尚未产生已提交的 Run 记录。
type Checkpoint struct {
	Version                  uint32               `json:"version"`
	SavedAt                  time.Time            `json:"saved_at"`
	Config                   Config               `json:"config"`
	Report                   Report               `json:"report"`
	Corpus                   corpus.Snapshot      `json:"corpus"`
	Ready                    []Candidate          `json:"ready"`
	InFlight                 []ScheduledCandidate `json:"in_flight"`
	PendingMutations         []mutation.Request   `json:"pending_mutations"`
	NextCandidateID          int                  `json:"next_candidate_id"`
	NextRunIndex             int                  `json:"next_run_index"`
	Completed                int                  `json:"completed"`
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
	if len(c.Report.Runs) != config.Runs {
		return fmt.Errorf("checkpoint report has %d run slots, want %d", len(c.Report.Runs), config.Runs)
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
	completed := 0
	for index, run := range c.Report.Runs {
		if run.Completed {
			completed++
			if run.Index != index {
				return fmt.Errorf("checkpoint run %d stores index %d", index, run.Index)
			}
		}
	}
	if completed != c.Completed {
		return fmt.Errorf("checkpoint completed count %d does not match %d runs", c.Completed, completed)
	}
	seenScheduled := make(map[int]struct{}, len(c.InFlight))
	for _, scheduled := range c.InFlight {
		if scheduled.Index < 0 || scheduled.Index >= c.NextRunIndex || scheduled.Seed != config.BaseSeed+int64(scheduled.Index) {
			return fmt.Errorf("checkpoint in-flight run %d has invalid index or seed", scheduled.Index)
		}
		if c.Report.Runs[scheduled.Index].Completed {
			return fmt.Errorf("checkpoint run %d is both completed and in-flight", scheduled.Index)
		}
		if _, duplicate := seenScheduled[scheduled.Index]; duplicate {
			return fmt.Errorf("checkpoint contains duplicate in-flight run %d", scheduled.Index)
		}
		seenScheduled[scheduled.Index] = struct{}{}
	}
	if completed+len(c.InFlight) != c.NextRunIndex {
		return fmt.Errorf("checkpoint assigned runs are neither completed nor in-flight")
	}
	if _, err := corpus.Restore(c.Corpus); err != nil {
		return fmt.Errorf("checkpoint corpus: %w", err)
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
	OnRunComplete func(Completion) error
	OnCheckpoint  func(Checkpoint) error
}
