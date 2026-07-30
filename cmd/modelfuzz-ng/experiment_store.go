package main

import (
	"bytes"
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
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/coverageguidance"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/experiment"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/llm"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/model/tlc"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/mutation"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/persistence"
)

type experimentSettings struct {
	ReleaseVersion         string                `json:"release_version"`
	SemanticSchema         string                `json:"semantic_schema"`
	LLMInit                bool                  `json:"llm_init"`
	LLMMutate              bool                  `json:"llm_mutate"`
	Initializer            string                `json:"initializer"`
	OnlinePolicy           string                `json:"online_policy"`
	Mutator                string                `json:"mutator"`
	LLMProvider            llm.Provider          `json:"llm_provider,omitempty"`
	LLMModel               string                `json:"llm_model,omitempty"`
	LLMBaseURL             string                `json:"llm_base_url,omitempty"`
	LLMAPIKeyEnv           string                `json:"llm_api_key_env,omitempty"`
	LLMTimeoutMillis       int64                 `json:"llm_timeout_millis,omitempty"`
	RandomMutation         mutation.RandomConfig `json:"random_mutation"`
	ArtifactPolicy         artifactPolicy        `json:"artifact_policy"`
	CheckpointEvery        int                   `json:"checkpoint_every"`
	CoverageGuidanceMode   coverageguidance.Mode `json:"coverage_guidance_mode,omitempty"`
	CoverageGuidanceSchema string                `json:"coverage_guidance_schema,omitempty"`
	CoverageEnergyMode     string                `json:"coverage_energy_mode,omitempty"`
	FixedEnergy            int                   `json:"fixed_energy,omitempty"`
	FixedParentSelection   string                `json:"fixed_parent_selection,omitempty"`
	CoverageCorpusLimit    int                   `json:"coverage_corpus_limit,omitempty"`
	RecordAllCoverage      bool                  `json:"record_all_coverage_metrics,omitempty"`
	OfflineGoalEvaluation  bool                  `json:"offline_goal_evaluation,omitempty"`
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
	directory            string
	policy               artifactPolicy
	config               cliConfig
	journal              *persistence.Journal
	runs                 *persistence.Journal
	corpus               *persistence.Journal
	coverageObservations *persistence.Journal
	corpusDecisions      *persistence.Journal
	parentSelections     *persistence.Journal
	corpusEntries        []corpus.Entry
	lastEventSequence    uint64
	llmStats             func() llm.Stats
}

func (s *experimentStore) enableCoverageGuidance(committedRuns int) error {
	if s == nil {
		return fmt.Errorf("experiment store is nil")
	}
	paths := []string{"coverage-observations.jsonl", "corpus-decisions.jsonl"}
	for _, name := range paths {
		if err := persistence.KeepJSONLines(filepath.Join(s.directory, name), committedRuns); err != nil {
			return fmt.Errorf("calibrate %s: %w", name, err)
		}
	}
	var err error
	if s.coverageObservations, err = persistence.OpenJournal(filepath.Join(s.directory, paths[0])); err != nil {
		return err
	}
	if s.corpusDecisions, err = persistence.OpenJournal(filepath.Join(s.directory, paths[1])); err != nil {
		_ = s.coverageObservations.Close()
		return err
	}
	// Parent selections are emitted only when an admitted parent still has
	// execution budget. Trim any record written after the committed checkpoint.
	parentPath := filepath.Join(s.directory, "parent-selection.jsonl")
	keep := 0
	data, readErr := os.ReadFile(parentPath)
	if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
		return readErr
	}
	for _, line := range bytes.Split(data, []byte{'\n'}) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var selection coverageguidance.ParentSelection
		if err := json.Unmarshal(line, &selection); err != nil {
			break // KeepJSONLines below repairs an incomplete or invalid tail.
		}
		if selection.Sequence > committedRuns {
			break
		}
		keep++
	}
	if err := persistence.KeepJSONLines(parentPath, keep); err != nil {
		return err
	}
	s.parentSelections, err = persistence.OpenJournal(parentPath)
	return err
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
		OnCoverageGuidance: func(observation coverageguidance.CoverageObservation, decision coverageguidance.Decision) error {
			if s.coverageObservations == nil || s.corpusDecisions == nil {
				return fmt.Errorf("coverage guidance journals are not enabled")
			}
			if err := s.coverageObservations.Append(observation); err != nil {
				return err
			}
			return s.corpusDecisions.Append(decision)
		},
		OnParentSelection: func(selection coverageguidance.ParentSelection) error {
			if s.parentSelections == nil {
				return fmt.Errorf("parent selection journal is not enabled")
			}
			return s.parentSelections.Append(selection)
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
	var guidanceErr error
	if s.coverageObservations != nil {
		guidanceErr = errors.Join(guidanceErr, s.coverageObservations.Close())
	}
	if s.corpusDecisions != nil {
		guidanceErr = errors.Join(guidanceErr, s.corpusDecisions.Close())
	}
	if s.parentSelections != nil {
		guidanceErr = errors.Join(guidanceErr, s.parentSelections.Close())
	}
	return errors.Join(s.journal.Close(), s.runs.Close(), s.corpus.Close(), guidanceErr)
}
