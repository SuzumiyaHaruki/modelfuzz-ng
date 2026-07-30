package main

import (
	"bytes"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/coverageanalysis"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/model"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/persistence"
)

func TestCoverageCompareCommandReadsArtifactsWithoutMutatingInput(t *testing.T) {
	root := t.TempDir()
	input := filepath.Join(root, "experiment")
	first := filepath.Join(input, "run-0000-seed-1", "model-states.json")
	second := filepath.Join(input, "run-0001-seed-2", "model-states.json")
	statesA := []model.State{{Key: 1, Text: coverageCompareState(2)}}
	statesB := []model.State{{Key: 2, Text: coverageCompareState(3)}}
	if err := persistence.WriteJSONAtomic(first, statesA); err != nil {
		t.Fatal(err)
	}
	if err := persistence.WriteJSONAtomic(second, statesB); err != nil {
		t.Fatal(err)
	}
	beforeFirst, err := fileSHA256(first)
	if err != nil {
		t.Fatal(err)
	}
	beforeSecond, err := fileSHA256(second)
	if err != nil {
		t.Fatal(err)
	}

	output := filepath.Join(root, "comparison.json")
	var stdout, stderr bytes.Buffer
	if err := runCLI(t.Context(), []string{
		"coverage-compare", "-input", input, "-output", output,
	}, &stdout, &stderr); err != nil {
		t.Fatalf("coverage compare: %v stderr=%s", err, stderr.String())
	}
	var report coverageanalysis.ComparisonReport
	if err := persistence.ReadJSON(output, &report); err != nil {
		t.Fatal(err)
	}
	if report.Executions != 2 || report.DistinctV1States != 2 ||
		report.DistinctV2States != 1 || !report.RepeatedV2AnalysisEqual {
		t.Fatalf("comparison report: %+v", report)
	}
	afterFirst, _ := fileSHA256(first)
	afterSecond, _ := fileSHA256(second)
	if beforeFirst != afterFirst || beforeSecond != afterSecond {
		t.Fatal("coverage comparison mutated input model-state artifacts")
	}
	if !strings.Contains(stdout.String(), "v1=2 v2=1") {
		t.Fatalf("summary output=%q", stdout.String())
	}
}

func TestCoverageCompareCommandRejectsMissingStatesAndInPlaceOutput(t *testing.T) {
	root := t.TempDir()
	input := filepath.Join(root, "experiment")
	if err := persistence.WriteJSONAtomic(filepath.Join(input, "trace.json"), map[string]any{
		"version": 1,
	}); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	err := runCLI(t.Context(), []string{"coverage-compare", "-input", input}, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "不包含重建 v2 所需的完整 TLC 状态文本") {
		t.Fatalf("missing-state error=%v", err)
	}

	statePath := filepath.Join(input, "run-0000", "model-states.json")
	if err := persistence.WriteJSONAtomic(statePath, []model.State{{Text: coverageCompareState(2)}}); err != nil {
		t.Fatal(err)
	}
	err = runCLI(t.Context(), []string{
		"coverage-compare", "-input", input, "-output", filepath.Join(input, "comparison.json"),
	}, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "只读") {
		t.Fatalf("in-place output error=%v", err)
	}
}

func coverageCompareState(entries int) string {
	parts := make([]string, entries)
	for index := range parts {
		parts[index] = "[term |-> 2, value |-> " + strconv.Itoa(index+1) + "]"
	}
	log := "<<" + strings.Join(parts, ", ") + ">>"
	progress := entries - 1
	progressText := strconv.Itoa(progress)
	return `/\ currentActive = {1, 2, 3}
/\ matchIndex = <<<<0, 0, 0>>, <<` + progressText + `, 0, ` + progressText + `>>, <<0, 0, 0>>>>
/\ log = <<` + log + `, ` + log + `, ` + log + `>>
/\ state = <<"follower", "leader", "follower">>
/\ commitIndex = <<` + progressText + `, ` + progressText + `, ` + progressText + `>>
/\ currentTerm = <<2, 2, 2>>
/\ votesGranted = <<{}, {}, {}>>
/\ votedFor = <<0, 0, 0>>`
}
