package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/core"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/engine"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/experiment"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/model"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/plan"
	tracepkg "github.com/SuzumiyaHaruki/modelfuzz-ng/internal/trace"
)

func TestRunCLIProducesCompleteArtifactsWithTLC(t *testing.T) {
	var received []model.Event
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
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

func TestExperimentCLIRunsIndependentRandomTraces(t *testing.T) {
	output := filepath.Join(t.TempDir(), "experiment")
	var stdout bytes.Buffer
	if err := runCLI(context.Background(), []string{
		"experiment", "-output", output, "-runs", "4", "-steps", "8",
		"-parallelism", "2", "-seed", "700",
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
	if report.Succeeded != 4 || report.TotalActions != 32 || report.Runs[3].Seed != 703 {
		t.Fatalf("report = %+v", report)
	}
	for index, run := range report.Runs {
		directory := filepath.Join(output, fmt.Sprintf("run-%04d-seed-%d", index, run.Seed))
		for _, name := range []string{"plan.json", "trace.json", "result.json", "oracle-findings.json", "failure.json"} {
			if _, err := os.Stat(filepath.Join(directory, name)); err != nil {
				t.Fatalf("run %d artifact %s: %v", index, name, err)
			}
		}
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

func TestRunCLIPersistsUnsupportedResultBeforeMutation(t *testing.T) {
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
	if err == nil {
		t.Fatal("unsupported crash mapping unexpectedly succeeded")
	}
	data, readErr := os.ReadFile(filepath.Join(outputPath, "result.json"))
	if readErr != nil {
		t.Fatal(readErr)
	}
	var result engine.Result
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatal(err)
	}
	if result.Status != engine.StatusUnsupported || len(result.Trace.Steps) != 0 ||
		result.Final.Nodes[0].Status != core.NodeRunning {
		t.Fatalf("partial result = %+v", result)
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
