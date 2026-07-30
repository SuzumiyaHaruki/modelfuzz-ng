package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/core"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/coverageanalysis"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/model"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/persistence"
)

func TestCoverageFactorizeCommandReadsFullArtifactsWithoutMutation(t *testing.T) {
	root := t.TempDir()
	input := filepath.Join(root, "experiment")
	runDirectory := filepath.Join(input, "run-0000-seed-1")
	nodes := factorizationNodes()
	artifacts := map[string]any{
		"model-states.json": []model.State{{Text: coverageCompareState(2)}},
		"model-events.json": []model.Event{},
		"trace.json": core.Trace{
			Version: core.CurrentTraceVersion, ExecutionID: "factorize", Seed: 1,
		},
		"config.json": defaultCLIConfig(),
		"result.json": map[string]any{
			"initial_observation": core.Observation{Nodes: nodes},
		},
		"candidate.json": map[string]any{"source": "random_init"},
	}
	for name, value := range artifacts {
		if err := persistence.WriteJSONAtomic(filepath.Join(runDirectory, name), value); err != nil {
			t.Fatal(err)
		}
	}
	statePath := filepath.Join(runDirectory, "model-states.json")
	before, err := fileSHA256(statePath)
	if err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(root, "factorization.json")
	var stdout, stderr bytes.Buffer
	if err := runCLI(t.Context(), []string{
		"coverage-factorize", "-input", input, "-output", output,
	}, &stdout, &stderr); err != nil {
		t.Fatalf("coverage factorize: %v stderr=%s", err, stderr.String())
	}
	var report coverageanalysis.FactorizationReport
	if err := persistence.ReadJSON(output, &report); err != nil {
		t.Fatal(err)
	}
	if report.Executions != 1 || report.CoverageFrames != 1 ||
		len(report.Facets) != 5 || len(report.Interactions) != 4 ||
		!report.RepeatedAnalysisEqual {
		t.Fatalf("report=%+v", report)
	}
	after, _ := fileSHA256(statePath)
	if before != after {
		t.Fatal("offline factorization mutated source artifact")
	}
	if !strings.Contains(stdout.String(), "facets=5 interactions=4 deterministic=true") {
		t.Fatalf("stdout=%q", stdout.String())
	}
}

func TestCoverageFactorizeCommandRequiresContextAndExternalOutput(t *testing.T) {
	root := t.TempDir()
	input := filepath.Join(root, "experiment")
	statePath := filepath.Join(input, "run-0000", "model-states.json")
	if err := persistence.WriteJSONAtomic(
		statePath, []model.State{{Text: coverageCompareState(2)}}); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	err := runCLI(t.Context(), []string{
		"coverage-factorize", "-input", input,
	}, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "缺少 model-events.json") {
		t.Fatalf("missing context error=%v", err)
	}

	runDirectory := filepath.Dir(statePath)
	nodes := factorizationNodes()
	for name, value := range map[string]any{
		"model-events.json": []model.Event{},
		"trace.json": core.Trace{
			Version: core.CurrentTraceVersion, ExecutionID: "factorize", Seed: 1,
		},
		"config.json": defaultCLIConfig(),
		"result.json": map[string]any{
			"initial_observation": core.Observation{Nodes: nodes},
		},
	} {
		if err := persistence.WriteJSONAtomic(filepath.Join(runDirectory, name), value); err != nil {
			t.Fatal(err)
		}
	}
	err = runCLI(t.Context(), []string{
		"coverage-factorize", "-input", input,
		"-output", filepath.Join(input, "factorization.json"),
	}, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "只读") {
		t.Fatalf("in-place output error=%v", err)
	}
}

func factorizationNodes() []core.NodeObservation {
	nodes := make([]core.NodeObservation, 3)
	for index := range nodes {
		role := "follower"
		if index == 1 {
			role = "leader"
		}
		nodes[index] = core.NodeObservation{
			ID: core.NodeID(index + 1), Epoch: 1, Status: core.NodeRunning,
			Semantic: map[string]any{
				"role": role, "term": uint64(2), "last_index": uint64(1),
				"last_term": uint64(2), "commit": uint64(1),
			},
		}
	}
	return nodes
}
