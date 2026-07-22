package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/core"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/corpus"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/experiment"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/llm"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/model/tlc"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/mutation"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/persistence"
)

type experimentSettings struct {
	LLMInit          bool                  `json:"llm_init"`
	LLMMutate        bool                  `json:"llm_mutate"`
	Initializer      string                `json:"initializer"`
	OnlinePolicy     string                `json:"online_policy"`
	Mutator          string                `json:"mutator"`
	LLMProvider      llm.Provider          `json:"llm_provider,omitempty"`
	LLMModel         string                `json:"llm_model,omitempty"`
	LLMBaseURL       string                `json:"llm_base_url,omitempty"`
	LLMAPIKeyEnv     string                `json:"llm_api_key_env,omitempty"`
	LLMTimeoutMillis int64                 `json:"llm_timeout_millis,omitempty"`
	RandomMutation   mutation.RandomConfig `json:"random_mutation"`
	ArtifactPolicy   artifactPolicy        `json:"artifact_policy"`
	CheckpointEvery  int                   `json:"checkpoint_every"`
}

// legacyExperimentSettingsV8 preserves the exact JSON field order used by v8
// before online_policy was added without a checkpoint version bump.
type legacyExperimentSettingsV8 struct {
	LLMInit          bool                  `json:"llm_init"`
	LLMMutate        bool                  `json:"llm_mutate"`
	Initializer      string                `json:"initializer"`
	Mutator          string                `json:"mutator"`
	LLMProvider      llm.Provider          `json:"llm_provider,omitempty"`
	LLMModel         string                `json:"llm_model,omitempty"`
	LLMBaseURL       string                `json:"llm_base_url,omitempty"`
	LLMAPIKeyEnv     string                `json:"llm_api_key_env,omitempty"`
	LLMTimeoutMillis int64                 `json:"llm_timeout_millis,omitempty"`
	RandomMutation   mutation.RandomConfig `json:"random_mutation"`
	ArtifactPolicy   artifactPolicy        `json:"artifact_policy"`
	CheckpointEvery  int                   `json:"checkpoint_every"`
}

func legacyV8Settings(settings experimentSettings) legacyExperimentSettingsV8 {
	return legacyExperimentSettingsV8{
		LLMInit: settings.LLMInit, LLMMutate: settings.LLMMutate, Initializer: settings.Initializer,
		Mutator: settings.Mutator, LLMProvider: settings.LLMProvider, LLMModel: settings.LLMModel,
		LLMBaseURL: settings.LLMBaseURL, LLMAPIKeyEnv: settings.LLMAPIKeyEnv,
		LLMTimeoutMillis: settings.LLMTimeoutMillis, RandomMutation: settings.RandomMutation,
		ArtifactPolicy: settings.ArtifactPolicy, CheckpointEvery: settings.CheckpointEvery,
	}
}

type tlcMetricsArtifact struct {
	Segments []tlcMetricsSegment `json:"segments"`
}

type tlcMetricsSegment struct {
	StartedAt time.Time         `json:"started_at"`
	EndedAt   time.Time         `json:"ended_at"`
	Start     tlc.ServerMetrics `json:"start"`
	End       tlc.ServerMetrics `json:"end"`
}

func configurationFingerprint(values ...any) (string, error) {
	hash := sha256.New()
	encoder := json.NewEncoder(hash)
	encoder.SetEscapeHTML(false)
	for _, value := range values {
		if err := encoder.Encode(value); err != nil {
			return "", fmt.Errorf("计算实验配置指纹: %w", err)
		}
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

type artifactPolicy string

const (
	artifactsAll      artifactPolicy = "all"
	artifactsRetained artifactPolicy = "retained"
	artifactsFailures artifactPolicy = "failures"
	artifactsSummary  artifactPolicy = "summary"
)

func parseArtifactPolicy(value string) (artifactPolicy, error) {
	policy := artifactPolicy(value)
	switch policy {
	case artifactsAll, artifactsRetained, artifactsFailures, artifactsSummary:
		return policy, nil
	default:
		return "", fmt.Errorf("未知 artifact policy %q；可选 all、retained、failures、summary", value)
	}
}

func (p artifactPolicy) saves(run experiment.Run) bool {
	switch p {
	case artifactsAll:
		return true
	case artifactsRetained:
		// 安全性失败即使没有增加模型覆盖也必须保留。
		return run.Retained || !run.Succeeded
	case artifactsFailures:
		return !run.Succeeded
	default:
		return false
	}
}

type experimentStore struct {
	directory         string
	policy            artifactPolicy
	config            cliConfig
	journal           *persistence.Journal
	runs              *persistence.Journal
	corpus            *persistence.Journal
	corpusEntries     []corpus.Entry
	lastEventSequence uint64
	llmStats          func() llm.Stats
}

func openExperimentStore(directory string, policy artifactPolicy, committedRunSummaries, committedCorpusEntries int) (*experimentStore, error) {
	runsPath := filepath.Join(directory, "runs.jsonl")
	if err := persistence.KeepJSONLines(runsPath, committedRunSummaries); err != nil {
		return nil, fmt.Errorf("校准 run summary journal: %w", err)
	}
	runs, err := persistence.OpenJournal(runsPath)
	if err != nil {
		return nil, err
	}
	corpusPath := filepath.Join(directory, "corpus.jsonl")
	if err := persistence.KeepJSONLines(corpusPath, committedCorpusEntries); err != nil {
		_ = runs.Close()
		return nil, fmt.Errorf("校准 corpus journal: %w", err)
	}
	corpusEntries, err := persistence.ReadJSONLines[corpus.Entry](corpusPath, committedCorpusEntries)
	if err != nil {
		_ = runs.Close()
		return nil, fmt.Errorf("读取 corpus journal: %w", err)
	}
	corpusJournal, err := persistence.OpenJournal(corpusPath)
	if err != nil {
		_ = runs.Close()
		return nil, err
	}
	journal, err := persistence.OpenJournal(filepath.Join(directory, "progress.jsonl"))
	if err != nil {
		_ = runs.Close()
		_ = corpusJournal.Close()
		return nil, err
	}
	store := &experimentStore{
		directory: directory, policy: policy, journal: journal, runs: runs,
		corpus: corpusJournal, corpusEntries: corpusEntries,
	}
	var event experiment.Event
	if err := persistence.ReadLastJSONLine(filepath.Join(directory, "progress.jsonl"), &event); err == nil {
		store.lastEventSequence = event.Sequence
	} else if !errors.Is(err, io.EOF) {
		_ = journal.Close()
		_ = runs.Close()
		_ = corpusJournal.Close()
		return nil, fmt.Errorf("读取实验 journal 最后一条记录: %w", err)
	}
	return store, nil
}

func (s *experimentStore) hooks() experiment.Hooks {
	return experiment.Hooks{
		OnEvent: func(event experiment.Event) error {
			if err := s.journal.Append(event); err != nil {
				return err
			}
			// Mutation 完成不再触发庞大的调度 checkpoint；LLM 调用统计很小，
			// 因此单独原子更新，避免进程崩溃时漏记已经产生的远程调用成本。
			if event.Kind == experiment.EventMutationCompleted && s.llmStats != nil {
				return persistence.WriteJSONAtomic(filepath.Join(s.directory, "llm-stats.json"), s.llmStats())
			}
			return nil
		},
		OnCheckpoint: func(checkpoint experiment.Checkpoint) error {
			// 先保存已经实际发生的 LLM 成本。若随后在 checkpoint 替换前崩溃，
			// 恢复重试产生的成本会继续累加，而不会把第一次调用悄悄漏掉。
			if s.llmStats != nil {
				if err := persistence.WriteJSONAtomic(filepath.Join(s.directory, "llm-stats.json"), s.llmStats()); err != nil {
					return err
				}
			}
			return persistence.WriteJSONAtomic(filepath.Join(s.directory, "checkpoint.json"), checkpoint)
		},
		OnCorpusEntry: func(entry corpus.Entry) error {
			return s.corpus.Append(entry)
		},
		OnRunComplete: s.writeCompletion,
	}
}

func (s *experimentStore) writeCompletion(completion experiment.Completion) error {
	if err := s.runs.Append(completion.Run); err != nil {
		return fmt.Errorf("追加 run summary: %w", err)
	}
	if !s.policy.saves(completion.Run) {
		return nil
	}
	run := completion.Run
	runConfig := s.config
	runConfig.Seed = run.Seed
	runConfig.ExecutionID = core.ExecutionID(fmt.Sprintf("%s-feedback-%04d", s.config.ExecutionID, run.Index))
	directory := filepath.Join(s.directory, fmt.Sprintf("run-%04d-seed-%d", run.Index, run.Seed))
	// 恢复点可能落在产物写完、checkpoint 替换之前。此时允许精确的 run
	// 目录被同一 index/seed 的确定性重跑结果原子更新。
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return fmt.Errorf("创建运行产物目录 %s: %w", directory, err)
	}
	if err := writeArtifacts(directory, runConfig, completion.Execution.Plan, completion.Execution.Result); err != nil {
		return err
	}
	if err := writeJSONFile(filepath.Join(directory, "candidate.json"), completion.Candidate); err != nil {
		return err
	}
	return writeJSONFile(filepath.Join(directory, "run-summary.json"), run)
}

func (s *experimentStore) Close() error {
	if s == nil {
		return nil
	}
	return errors.Join(s.journal.Close(), s.runs.Close(), s.corpus.Close())
}
