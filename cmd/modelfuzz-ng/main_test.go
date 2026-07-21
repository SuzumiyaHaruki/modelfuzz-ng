package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/core"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/engine"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/experiment"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/llm"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/model"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/persistence"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/plan"
	randompolicy "github.com/SuzumiyaHaruki/modelfuzz-ng/internal/policy"
	tracepkg "github.com/SuzumiyaHaruki/modelfuzz-ng/internal/trace"
)

func readRunSummaries(t *testing.T, path string) []experiment.Run {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	result := make([]experiment.Run, 0)
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var run experiment.Run
		if err := json.Unmarshal(scanner.Bytes(), &run); err != nil {
			t.Fatal(err)
		}
		result = append(result, run)
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Index < result[j].Index })
	return result
}

func TestRunCLIProducesCompleteArtifactsWithTLC(t *testing.T) {
	var received []model.Event
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/health" && request.Method == http.MethodGet {
			_ = json.NewEncoder(writer).Encode(map[string]any{"largest_term": 5, "max_log_index": 5})
			return
		}
		if request.URL.Path != "/execute" || request.Method != http.MethodPost {
			t.Errorf("TLC request = %s %s", request.Method, request.URL.Path)
		}
		if err := json.NewDecoder(request.Body).Decode(&received); err != nil {
			t.Errorf("decode TLC request: %v", err)
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"States":["initial","leader"],"Keys":[1,2]}`))
	}))
	defer server.Close()

	temporary := t.TempDir()
	planPath := filepath.Join(temporary, "plan.json")
	outputPath := filepath.Join(temporary, "run")
	sequence := electionPlan()
	if err := writeJSONFile(planPath, sequence); err != nil {
		t.Fatal(err)
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	err := runCLI(context.Background(), []string{
		"run", "-plan", planPath, "-output", outputPath,
		"-execution-id", "cli-test", "-seed", "42", "-tlc", server.URL,
	}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("runCLI: %v\nstderr: %s", err, stderr.String())
	}
	if !strings.Contains(stdout.String(), "status=completed") {
		t.Fatalf("stdout = %q", stdout.String())
	}
	if len(received) != 6 || !received[len(received)-1].Reset {
		t.Fatalf("TLC events = %+v", received)
	}

	for _, name := range []string{
		"config.json", "plan.json", "resolutions.json", "actions.json",
		"trace.json", "model-events.json", "model-states.json", "oracle-findings.json", "failure.json", "result.json",
	} {
		if _, err := os.Stat(filepath.Join(outputPath, name)); err != nil {
			t.Errorf("artifact %s: %v", name, err)
		}
	}
	data, err := os.ReadFile(filepath.Join(outputPath, "result.json"))
	if err != nil {
		t.Fatal(err)
	}
	var result engine.Result
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatal(err)
	}
	if result.Status != engine.StatusCompleted || !result.ModelExecuted || len(result.Trace.Steps) != 3 {
		t.Fatalf("persisted result = %+v", result)
	}
	if len(result.ModelEvents) != 5 || len(result.ModelStates) != 2 || len(result.OracleFindings) != 0 {
		t.Fatalf("persisted model output: events=%d states=%d", len(result.ModelEvents), len(result.ModelStates))
	}
}

func TestCompleteExamplePlans(t *testing.T) {
	examples := filepath.Join("..", "..", "examples")
	tests := []struct {
		name      string
		committed []core.NodeID
		lastIndex uint64
	}{
		{name: "election-commit-node1", committed: []core.NodeID{1, 2}, lastIndex: 1},
		{name: "election-commit-node2", committed: []core.NodeID{2, 3}, lastIndex: 1},
		{name: "client-request-commit", committed: []core.NodeID{1, 2}, lastIndex: 2},
		{name: "follower-catchup-multi-entry", committed: []core.NodeID{1, 2}, lastIndex: 4},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			output := filepath.Join(t.TempDir(), "run")
			err := runCLI(context.Background(), []string{
				"run", "-strict-plan",
				"-config", filepath.Join(examples, "config.json"),
				"-plan", filepath.Join(examples, "plans", test.name+".json"),
				"-output", output,
			}, &bytes.Buffer{}, &bytes.Buffer{})
			if err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(filepath.Join(output, "result.json"))
			if err != nil {
				t.Fatal(err)
			}
			var result engine.Result
			if err := json.Unmarshal(data, &result); err != nil {
				t.Fatal(err)
			}
			if result.Status != engine.StatusCompleted {
				t.Fatalf("result status = %s", result.Status)
			}
			if result.Failure != nil || result.Trace.Version != core.CurrentTraceVersion {
				t.Fatalf("successful run failure/version = %+v/%d", result.Failure, result.Trace.Version)
			}
			for _, resolution := range result.Resolutions {
				if resolution.Status != plan.ResolutionResolved {
					t.Fatalf("resolution = %+v, want resolved", resolution)
				}
			}
			committedDigest := ""
			for _, id := range test.committed {
				node := observedNode(t, result.Final, id)
				if semanticUint64(t, node, "commit") != test.lastIndex ||
					semanticUint64(t, node, "applied") != test.lastIndex ||
					semanticUint64(t, node, "last_index") != test.lastIndex {
					t.Fatalf("node %s final semantic = %+v", id, node.Semantic)
				}
				if available, _ := node.Semantic["committed_prefix_available"].(bool); !available {
					t.Fatalf("node %s committed prefix is unavailable", id)
				}
				prefixes, ok := node.Semantic["committed_prefix_digests"].(map[string]any)
				if !ok {
					t.Fatalf("node %s committed prefixes = %T(%v)", id,
						node.Semantic["committed_prefix_digests"], node.Semantic["committed_prefix_digests"])
				}
				digest, _ := prefixes[strconv.FormatUint(test.lastIndex, 10)].(string)
				if digest == "" {
					t.Fatalf("node %s does not expose commit checkpoint %d: %v", id, test.lastIndex, prefixes)
				}
				if committedDigest != "" && digest != committedDigest {
					t.Fatalf("committed prefix conflict at %d: %q != %q", test.lastIndex, digest, committedDigest)
				}
				committedDigest = digest
			}
		})
	}
}

func TestExperimentCLIRunsRandomFeedbackSeedsWithoutTLC(t *testing.T) {
	output := filepath.Join(t.TempDir(), "experiment")
	var stdout bytes.Buffer
	if err := runCLI(context.Background(), []string{
		"experiment", "-output", output, "-runs", "4", "-max-plan-actions", "8",
		"-parallelism", "2", "-seed", "700", "-random-seed-interval", "2",
	}, &stdout, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "runs=4") || !strings.Contains(stdout.String(), "failed=0") {
		t.Fatalf("stdout = %q", stdout.String())
	}
	data, err := os.ReadFile(filepath.Join(output, "experiment-report.json"))
	if err != nil {
		t.Fatal(err)
	}
	var report experiment.Report
	if err := json.Unmarshal(data, &report); err != nil {
		t.Fatal(err)
	}
	runs := readRunSummaries(t, filepath.Join(output, "runs.jsonl"))
	if report.Succeeded != 4 || report.TotalActions != 32 || len(report.Runs) != 0 || runs[3].Seed != 703 {
		t.Fatalf("report = %+v", report)
	}
	if !report.Feedback || report.CorpusEntries != 0 || report.InitialExecutions != 3 || report.PeriodicSeedExecutions != 1 {
		t.Fatalf("feedback fields = %+v", report)
	}
	if report.PlansObserved != 4 || report.UniquePlans == 0 || report.ModelStatePathsObserved != 0 {
		t.Fatalf("novelty fields = %+v", report)
	}
	if report.NoveltyBySource[string(experiment.CandidatePeriodicRandom)].Executions != 1 {
		t.Fatalf("source novelty = %+v", report.NoveltyBySource)
	}
	if _, err := os.Stat(filepath.Join(output, "corpus.json")); err != nil {
		t.Fatalf("corpus artifact: %v", err)
	}
	for index, run := range runs {
		directory := filepath.Join(output, fmt.Sprintf("run-%04d-seed-%d", index, run.Seed))
		for _, name := range []string{"plan.json", "trace.json", "result.json", "oracle-findings.json", "failure.json", "candidate.json"} {
			if _, err := os.Stat(filepath.Join(directory, name)); err != nil {
				t.Fatalf("run %d artifact %s: %v", index, name, err)
			}
		}
	}
}

func TestExperimentCLIUsesConfiguredPlanActionBudgetWhenFlagIsOmitted(t *testing.T) {
	temporary := t.TempDir()
	configPath := filepath.Join(temporary, "config.json")
	if err := os.WriteFile(configPath, []byte(`{"engine":{"max_plan_actions":3}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(temporary, "experiment")
	if err := runCLI(context.Background(), []string{
		"experiment", "-config", configPath, "-output", output, "-runs", "1",
		"-initial-population", "1", "-artifact-policy", "summary", "-seed", "704",
	}, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	var report experiment.Report
	if err := persistence.ReadJSON(filepath.Join(output, "experiment-report.json"), &report); err != nil {
		t.Fatal(err)
	}
	if report.TotalActions != 3 {
		t.Fatalf("total actions = %d, want configured PlanAction budget 3", report.TotalActions)
	}
}

func TestExperimentCLIPersistsMetricsAndResumesCompletedCheckpoint(t *testing.T) {
	output := filepath.Join(t.TempDir(), "persistent-experiment")
	if err := runCLI(context.Background(), []string{
		"experiment", "-output", output, "-runs", "3", "-max-plan-actions", "5",
		"-artifact-policy", "summary", "-checkpoint-every", "1", "-seed", "810",
	}, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"checkpoint.json", "progress.jsonl", "runs.jsonl", "corpus.jsonl", "experiment-report.json", "experiment-metrics.json"} {
		if _, err := os.Stat(filepath.Join(output, name)); err != nil {
			t.Fatalf("missing %s: %v", name, err)
		}
	}
	if matches, err := filepath.Glob(filepath.Join(output, "run-*")); err != nil || len(matches) != 0 {
		t.Fatalf("summary policy run directories = %v/%v", matches, err)
	}
	var checkpoint experiment.Checkpoint
	data, err := os.ReadFile(filepath.Join(output, "checkpoint.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &checkpoint); err != nil {
		t.Fatal(err)
	}
	if checkpoint.Completed != 3 || checkpoint.RunSummaryCount != 3 || checkpoint.Aggregation.Report.CompletedRuns != 3 ||
		len(checkpoint.Aggregation.Report.Runs) != 0 {
		t.Fatalf("checkpoint = %+v", checkpoint)
	}
	var metrics experiment.Statistics
	data, err = os.ReadFile(filepath.Join(output, "experiment-metrics.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &metrics); err != nil {
		t.Fatal(err)
	}
	if metrics.CompletedRuns != 3 || metrics.TotalActions != 15 || len(metrics.CoverageTimeline) != 3 {
		t.Fatalf("metrics = %+v", metrics)
	}

	var stdout bytes.Buffer
	if err := runCLI(context.Background(), []string{
		"experiment", "-resume", output,
	}, &stdout, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "runs=3") {
		t.Fatalf("resume stdout = %q", stdout.String())
	}
	journal, err := os.ReadFile(filepath.Join(output, "progress.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(journal, []byte(`"kind":"experiment_resumed"`)) {
		t.Fatalf("journal does not contain resume event: %s", journal)
	}
}

func TestExperimentCLISupportsGenericLLMProviderConfiguration(t *testing.T) {
	var llmCalls, tlcCalls int
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/chat/completions":
			llmCalls++
			if request.Header.Get("Authorization") != "Bearer placeholder-not-a-real-key" {
				t.Fatalf("LLM auth = %q", request.Header.Get("Authorization"))
			}
			var body struct {
				Thinking map[string]string `json:"thinking"`
			}
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			wantThinking := "enabled"
			node := 1
			if llmCalls == 2 {
				wantThinking = "disabled"
				node = 2
			}
			if body.Thinking["type"] != wantThinking {
				t.Fatalf("LLM call %d thinking = %q, want %q", llmCalls, body.Thinking["type"], wantThinking)
			}
			content := fmt.Sprintf(`{"plans":[{"actions":[{"kind":"timeout","node":%d}]}]}`, node)
			response := map[string]any{
				"model":   "deepseek-v4-flash",
				"choices": []any{map[string]any{"finish_reason": "stop", "message": map[string]any{"content": content}}},
				"usage":   map[string]any{"prompt_tokens": 1, "completion_tokens": 1, "total_tokens": 2},
			}
			_ = json.NewEncoder(writer).Encode(response)
		case "/execute":
			tlcCalls++
			keys := []int64{1}
			states := []string{"s1"}
			if tlcCalls == 2 {
				keys = append(keys, 2)
				states = append(states, "s2")
			}
			_ = json.NewEncoder(writer).Encode(map[string]any{"States": states, "Keys": keys})
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	t.Setenv("DEEPSEEK_API_KEY", "placeholder-not-a-real-key")
	output := filepath.Join(t.TempDir(), "llm-feedback")
	var stdout, stderr bytes.Buffer
	if err := runCLI(context.Background(), []string{
		"experiment", "-output", output, "-runs", "2", "-max-plan-actions", "3",
		"-initial-population", "1", "-llm-init", "-llm-mutate",
		"-llm-provider", "deepseek", "-llm-base-url", server.URL, "-tlc", server.URL,
	}, &stdout, &stderr); err != nil {
		t.Fatalf("LLM feedback: %v\nstderr=%s", err, stderr.String())
	}
	if llmCalls != 2 || tlcCalls != 2 {
		t.Fatalf("calls: llm=%d tlc=%d", llmCalls, tlcCalls)
	}
	var report experiment.Report
	data, err := os.ReadFile(filepath.Join(output, "experiment-report.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &report); err != nil {
		t.Fatal(err)
	}
	if report.CorpusEntries != 2 || report.ExecutedMutations != 1 || report.UniqueModelStates != 2 {
		t.Fatalf("report = %+v", report)
	}
	var stats llm.Stats
	data, err = os.ReadFile(filepath.Join(output, "llm-stats.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &stats); err != nil {
		t.Fatal(err)
	}
	if stats.Calls != 2 || stats.Failures != 0 || stats.ByPurpose["initial"].Calls != 1 ||
		stats.ByPurpose["mutation"].Calls != 1 {
		t.Fatalf("LLM stats = %+v", stats)
	}
	settings, err := os.ReadFile(filepath.Join(output, "experiment-settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(settings, []byte("placeholder-not-a-real-key")) {
		t.Fatal("experiment settings leaked API Key")
	}
	var persistedSettings struct {
		Provider string `json:"llm_provider"`
		Model    string `json:"llm_model"`
		KeyEnv   string `json:"llm_api_key_env"`
	}
	if err := json.Unmarshal(settings, &persistedSettings); err != nil {
		t.Fatal(err)
	}
	if persistedSettings.Provider != "deepseek" || persistedSettings.Model != "deepseek-v4-flash" || persistedSettings.KeyEnv != "DEEPSEEK_API_KEY" {
		t.Fatalf("LLM settings = %+v", persistedSettings)
	}

	// 完成态恢复不会再次调用 LLM，也不能用新 Client 的零统计覆盖原有成本。
	if err := runCLI(context.Background(), []string{"experiment", "-resume", output},
		&bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(filepath.Join(output, "llm-stats.json"))
	if err != nil {
		t.Fatal(err)
	}
	var resumedStats llm.Stats
	if err := json.Unmarshal(data, &resumedStats); err != nil {
		t.Fatal(err)
	}
	if resumedStats.Calls != 2 || resumedStats.TotalTokens != 4 || llmCalls != 2 {
		t.Fatalf("resumed LLM stats/calls = %+v/%d", resumedStats, llmCalls)
	}
}

func TestExperimentCLISelectsNonDefaultProviderAndKeyEnvironment(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/chat/completions" || request.Header.Get("Authorization") != "Bearer provider-test-key" {
			t.Fatalf("request path/auth = %s/%q", request.URL.Path, request.Header.Get("Authorization"))
		}
		var body map[string]any
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["model"] != "qwen-test" || body["enable_thinking"] != true {
			t.Fatalf("Qwen request = %+v", body)
		}
		_ = json.NewEncoder(writer).Encode(map[string]any{
			"model": "qwen-test",
			"choices": []any{map[string]any{
				"finish_reason": "stop",
				"message":       map[string]any{"content": `{"plans":[{"actions":[{"kind":"timeout","node":1}]}]}`},
			}},
			"usage": map[string]any{"prompt_tokens": 1, "completion_tokens": 1, "total_tokens": 2},
		})
	}))
	defer server.Close()
	t.Setenv("MODELFUZZ_TEST_LLM_KEY", "provider-test-key")
	output := filepath.Join(t.TempDir(), "qwen-init")
	if err := runCLI(context.Background(), []string{
		"experiment", "-output", output, "-runs", "1", "-max-plan-actions", "3",
		"-initial-population", "1", "-llm-init", "-llm-provider", "qwen",
		"-llm-model", "qwen-test", "-llm-base-url", server.URL,
		"-llm-api-key-env", "MODELFUZZ_TEST_LLM_KEY",
	}, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(output, "experiment-settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	var settings struct {
		Provider string `json:"llm_provider"`
		Model    string `json:"llm_model"`
		KeyEnv   string `json:"llm_api_key_env"`
	}
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatal(err)
	}
	if settings.Provider != "qwen" || settings.Model != "qwen-test" || settings.KeyEnv != "MODELFUZZ_TEST_LLM_KEY" {
		t.Fatalf("settings = %+v", settings)
	}
	if bytes.Contains(data, []byte("provider-test-key")) {
		t.Fatal("experiment settings leaked provider API Key")
	}
}

func TestLoadCLIConfigInheritsModelNodeIDs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	data := []byte(`{
  "raft": {"node_ids": [2, 4, 6]},
  "model": {"max_value": 3}
}`)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	config, err := loadCLIConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(config.Model.NodeIDs) != 3 || config.Model.NodeIDs[0] != 2 || config.Model.NodeIDs[2] != 6 {
		t.Fatalf("model nodes = %v, want inherited [2 4 6]", config.Model.NodeIDs)
	}
	if config.Model.MaxValue != 3 || config.Model.MaxLogIndex == 0 {
		t.Fatalf("model defaults were not merged: %+v", config.Model)
	}
}

func TestDefaultCLIConfigUsesFullConcreteActionBudget(t *testing.T) {
	config := defaultCLIConfig()
	if config.Engine.MaxPlanActions != defaultMaxPlanActions || defaultMaxPlanActions != 1000 {
		t.Fatalf("default max PlanActions = %d, want 1000", config.Engine.MaxPlanActions)
	}
	if config.Runtime.MaxActions < uint64(config.Engine.MaxPlanActions) {
		t.Fatalf("runtime max actions %d is below PlanAction budget %d",
			config.Runtime.MaxActions, config.Engine.MaxPlanActions)
	}
}

func TestRunCLIPersistsCrashResult(t *testing.T) {
	temporary := t.TempDir()
	planPath := filepath.Join(temporary, "crash.json")
	outputPath := filepath.Join(temporary, "run")
	sequence := plan.PlanSequence{Actions: []plan.PlanAction{{Kind: plan.ActionCrash, Node: 1}}}
	if err := writeJSONFile(planPath, sequence); err != nil {
		t.Fatal(err)
	}
	err := runCLI(context.Background(), []string{
		"run", "-plan", planPath, "-output", outputPath,
	}, &bytes.Buffer{}, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("run crash plan: %v", err)
	}
	data, readErr := os.ReadFile(filepath.Join(outputPath, "result.json"))
	if readErr != nil {
		t.Fatal(readErr)
	}
	var result engine.Result
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatal(err)
	}
	if result.Status != engine.StatusCompleted || len(result.Trace.Steps) != 1 ||
		result.Final.Nodes[0].Status != core.NodeCrashed || len(result.ModelEvents) != 1 ||
		result.ModelEvents[0].Name != "Remove" {
		t.Fatalf("crash result = %+v", result)
	}
}

func TestRunAndExperimentCLIOverrideModelBounds(t *testing.T) {
	temporary := t.TempDir()
	planPath := filepath.Join(temporary, "plan.json")
	if err := writeJSONFile(planPath, plan.PlanSequence{Actions: []plan.PlanAction{{
		Kind: plan.ActionTimeout, Node: 1,
	}}}); err != nil {
		t.Fatal(err)
	}
	runOutput := filepath.Join(temporary, "run")
	if err := runCLI(context.Background(), []string{
		"run", "-plan", planPath, "-output", runOutput,
		"-largest-term", "10", "-max-log-index", "9",
	}, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	var runConfig cliConfig
	if err := persistence.ReadJSON(filepath.Join(runOutput, "config.json"), &runConfig); err != nil {
		t.Fatal(err)
	}
	if runConfig.Model.LargestTerm != 10 || runConfig.Model.MaxLogIndex != 9 {
		t.Fatalf("run bounds = %+v", runConfig.Model)
	}

	experimentOutput := filepath.Join(temporary, "experiment")
	if err := runCLI(context.Background(), []string{
		"experiment", "-output", experimentOutput, "-runs", "1",
		"-largest-term", "10", "-max-log-index", "9",
	}, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	var policyConfig randompolicy.RandomConfig
	if err := persistence.ReadJSON(filepath.Join(experimentOutput, "policy-config.json"), &policyConfig); err != nil {
		t.Fatal(err)
	}
	if policyConfig.LargestTerm != 10 || policyConfig.MaxLogIndex != 9 {
		t.Fatalf("experiment policy bounds = %+v", policyConfig)
	}
	if err := runCLI(context.Background(), []string{
		"experiment", "-resume", experimentOutput, "-largest-term", "10",
	}, &bytes.Buffer{}, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "不能覆盖") {
		t.Fatalf("resume boundary override error = %v", err)
	}
}

func TestRunAndExperimentCLIConfigureSnapshotPolicyAndResumeRejectsOverride(t *testing.T) {
	temporary := t.TempDir()
	planPath := filepath.Join(temporary, "plan.json")
	if err := writeJSONFile(planPath, plan.PlanSequence{Actions: []plan.PlanAction{{
		Kind: plan.ActionTimeout, Node: 1,
	}}}); err != nil {
		t.Fatal(err)
	}
	runOutput := filepath.Join(temporary, "run")
	if err := runCLI(context.Background(), []string{
		"run", "-plan", planPath, "-output", runOutput,
		"-snapshot-threshold", "3", "-snapshot-retain-entries", "1",
	}, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	var runConfig cliConfig
	if err := persistence.ReadJSON(filepath.Join(runOutput, "config.json"), &runConfig); err != nil {
		t.Fatal(err)
	}
	if runConfig.Raft.Snapshot.Threshold != 3 || runConfig.Raft.Snapshot.RetainEntries != 1 {
		t.Fatalf("run snapshot policy = %+v", runConfig.Raft.Snapshot)
	}

	experimentOutput := filepath.Join(temporary, "experiment")
	if err := runCLI(context.Background(), []string{
		"experiment", "-output", experimentOutput, "-runs", "1",
		"-snapshot-threshold", "3", "-snapshot-retain-entries", "1",
	}, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if err := runCLI(context.Background(), []string{
		"experiment", "-resume", experimentOutput, "-snapshot-threshold", "3",
	}, &bytes.Buffer{}, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "SnapshotPolicy") {
		t.Fatalf("resume snapshot override error = %v", err)
	}
}

func TestValidateTLCModelBoundsRejectsMismatch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(writer).Encode(map[string]any{"largest_term": 10, "max_log_index": 10})
	}))
	defer server.Close()
	config := defaultCLIConfig()
	config.TLC.Address = server.URL
	err := validateTLCModelBounds(context.Background(), config, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "TLC/Go 模型边界不一致") {
		t.Fatalf("boundary mismatch error = %v", err)
	}
}

func TestLifecyclePlansRunAndReplay(t *testing.T) {
	tests := []struct {
		name      string
		steps     int
		node      core.NodeID
		epoch     core.NodeEpoch
		role      string
		commit    uint64
		lastIndex uint64
	}{
		{name: "follower-crash-restart", steps: 10, node: 3, epoch: 2, role: "follower", commit: 1, lastIndex: 1},
		{name: "leader-crash-reelection", steps: 13, node: 1, epoch: 2, role: "follower", commit: 1, lastIndex: 1},
		{name: "uncommitted-log-restart", steps: 17, node: 1, epoch: 2, role: "leader", commit: 3, lastIndex: 3},
		{name: "committed-log-restart", steps: 11, node: 1, epoch: 2, role: "follower", commit: 2, lastIndex: 2},
		{name: "repeated-crash-restart", steps: 3, node: 3, epoch: 2, role: "follower"},
	}
	configPath := filepath.Join("..", "..", "examples", "config.json")
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			temporary := t.TempDir()
			runOutput := filepath.Join(temporary, "run")
			planPath := filepath.Join("..", "..", "examples", "plans", test.name+".json")
			if err := runCLI(context.Background(), []string{
				"run", "-config", configPath, "-plan", planPath, "-output", runOutput,
			}, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(filepath.Join(runOutput, "result.json"))
			if err != nil {
				t.Fatal(err)
			}
			var result engine.Result
			if err := json.Unmarshal(data, &result); err != nil {
				t.Fatal(err)
			}
			if result.Status != engine.StatusCompleted || result.Termination != engine.TerminationPlanComplete ||
				len(result.Trace.Steps) != test.steps || len(result.OracleFindings) != 0 {
				t.Fatalf("result = %+v", result)
			}
			if err := result.Trace.Validate(); err != nil {
				t.Fatalf("trace: %v", err)
			}
			node := observedNode(t, result.Final, test.node)
			if node.Status != core.NodeRunning || node.Epoch != test.epoch || node.Semantic["role"] != test.role ||
				semanticUint64(t, node, "commit") != test.commit || semanticUint64(t, node, "last_index") != test.lastIndex {
				t.Fatalf("node = %+v", node)
			}
			seenRemove, seenAdd := false, false
			for _, event := range result.ModelEvents {
				seenRemove = seenRemove || event.Name == "Remove"
				seenAdd = seenAdd || event.Name == "Add"
			}
			if !seenRemove || !seenAdd {
				t.Fatalf("lifecycle events = %+v", result.ModelEvents)
			}

			replayOutput := filepath.Join(temporary, "replay")
			if err := runCLI(context.Background(), []string{
				"replay", "-trace", filepath.Join(runOutput, "trace.json"), "-output", replayOutput,
			}, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
				t.Fatal(err)
			}
			data, err = os.ReadFile(filepath.Join(replayOutput, "replay-result.json"))
			if err != nil {
				t.Fatal(err)
			}
			var replay tracepkg.Result
			if err := json.Unmarshal(data, &replay); err != nil {
				t.Fatal(err)
			}
			if replay.Status != tracepkg.StatusCompleted || replay.MatchedSteps != uint64(test.steps) {
				t.Fatalf("replay = %+v", replay)
			}
		})
	}
}

func TestReplayCLIReproducesRunTrace(t *testing.T) {
	temporary := t.TempDir()
	planPath := filepath.Join(temporary, "plan.json")
	runOutput := filepath.Join(temporary, "run")
	replayOutput := filepath.Join(temporary, "replay")
	if err := writeJSONFile(planPath, electionPlan()); err != nil {
		t.Fatal(err)
	}
	if err := runCLI(context.Background(), []string{
		"run", "-plan", planPath, "-output", runOutput,
	}, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	var stdout bytes.Buffer
	if err := runCLI(context.Background(), []string{
		"replay", "-trace", filepath.Join(runOutput, "trace.json"), "-output", replayOutput,
	}, &stdout, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "status=completed") {
		t.Fatalf("stdout = %q", stdout.String())
	}
	data, err := os.ReadFile(filepath.Join(replayOutput, "replay-result.json"))
	if err != nil {
		t.Fatal(err)
	}
	var result tracepkg.Result
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatal(err)
	}
	if result.Status != tracepkg.StatusCompleted || result.MatchedSteps != 3 {
		t.Fatalf("replay result = %+v", result)
	}
}

func TestLoadCLIConfigRejectsUnknownFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"unknown":true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := loadCLIConfig(path); err == nil {
		t.Fatal("unknown config field was accepted")
	}
}

func TestCreateOutputDirectoryRefusesOverwrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "existing")
	if err := createOutputDirectory(path); err != nil {
		t.Fatal(err)
	}
	if err := createOutputDirectory(path); err == nil {
		t.Fatal("existing output directory was accepted")
	}
}

func TestWriteArtifactsPersistsStructuredFailure(t *testing.T) {
	directory := t.TempDir()
	action := core.Action{Kind: core.ActionAdvanceTime, TargetTime: 1}
	result := engine.Result{
		Status: engine.StatusRuntimeFailed,
		Failure: &core.FailureRecord{
			Kind: core.FailureSUTPanic, Operation: "tick", Time: 1, Action: &action,
			Error: "adapter operation failed: tick panicked", PanicValue: "boom",
			Stack: "goroutine 1 [running]",
			ObservationBefore: core.Observation{Nodes: []core.NodeObservation{{
				ID: 1, Epoch: 1, Status: core.NodeRunning,
			}}},
		},
	}
	if err := writeArtifacts(directory, defaultCLIConfig(), plan.PlanSequence{}, result); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(filepath.Join(directory, "failure.json"))
	if err != nil {
		t.Fatal(err)
	}
	var persisted core.FailureRecord
	if err := json.Unmarshal(data, &persisted); err != nil {
		t.Fatal(err)
	}
	if persisted.Kind != core.FailureSUTPanic || persisted.Operation != "tick" ||
		persisted.Action == nil || persisted.Action.TargetTime != 1 || persisted.Stack == "" {
		t.Fatalf("persisted failure = %+v", persisted)
	}

	data, err = os.ReadFile(filepath.Join(directory, "result.json"))
	if err != nil {
		t.Fatal(err)
	}
	var persistedResult engine.Result
	if err := json.Unmarshal(data, &persistedResult); err != nil {
		t.Fatal(err)
	}
	if persistedResult.Failure == nil || persistedResult.Failure.PanicValue != "boom" {
		t.Fatalf("result did not embed failure: %+v", persistedResult.Failure)
	}
}

func electionPlan() plan.PlanSequence {
	return plan.PlanSequence{Actions: []plan.PlanAction{
		{Kind: plan.ActionTimeout, Node: 1},
		{Kind: plan.ActionDeliver, Messages: &plan.MessageRangeSelector{
			Link: core.LinkID{From: 1, To: 2}, Count: 1,
		}},
		{Kind: plan.ActionDeliver, Messages: &plan.MessageRangeSelector{
			Link: core.LinkID{From: 2, To: 1}, Count: 1,
		}},
	}}
}

func observedNode(t *testing.T, observation core.Observation, id core.NodeID) core.NodeObservation {
	t.Helper()
	for _, node := range observation.Nodes {
		if node.ID == id {
			return node
		}
	}
	t.Fatalf("node %s not found", id)
	return core.NodeObservation{}
}

func semanticUint64(t *testing.T, node core.NodeObservation, name string) uint64 {
	t.Helper()
	switch value := node.Semantic[name].(type) {
	case uint64:
		return value
	case float64:
		return uint64(value)
	default:
		t.Fatalf("node %s semantic %s = %T(%v)", node.ID, name, value, value)
		return 0
	}
}
