package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/core"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/engine"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/persistence"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/plan"
	tracepkg "github.com/SuzumiyaHaruki/modelfuzz-ng/internal/trace"
)

func createOutputDirectory(path string) error {
	if path == "" {
		return fmt.Errorf("输出目录不能为空")
	}
	parent := filepath.Dir(path)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return fmt.Errorf("创建输出目录父路径 %s: %w", parent, err)
	}
	if err := os.Mkdir(path, 0o755); err != nil {
		if os.IsExist(err) {
			return fmt.Errorf("输出目录 %s 已存在；为避免覆盖旧轨迹，请使用新目录", path)
		}
		return fmt.Errorf("创建输出目录 %s: %w", path, err)
	}
	return nil
}

func writeReplayArtifacts(directory string, config cliConfig, expected core.Trace, result tracepkg.Result) error {
	artifacts := []struct {
		name  string
		value any
	}{
		{name: "config.json", value: config},
		{name: "expected-trace.json", value: expected},
		{name: "actual-trace.json", value: result.Actual},
		{name: "replay-result.json", value: result},
	}
	for _, artifact := range artifacts {
		if err := writeJSONFile(filepath.Join(directory, artifact.name), artifact.value); err != nil {
			return err
		}
	}
	return nil
}

func writeArtifacts(directory string, config cliConfig, sequence plan.PlanSequence, result engine.Result) error {
	artifacts := []struct {
		name  string
		value any
	}{
		{name: "config.json", value: config},
		{name: "plan.json", value: sequence},
		{name: "resolutions.json", value: result.Resolutions},
		{name: "actions.json", value: result.Actions},
		{name: "trace.json", value: result.Trace},
		{name: "model-events.json", value: result.ModelEvents},
		{name: "model-states.json", value: result.ModelStates},
		{name: "oracle-findings.json", value: result.OracleFindings},
		{name: "failure.json", value: result.Failure},
		{name: "result.json", value: result},
	}
	for _, artifact := range artifacts {
		if err := writeJSONFile(filepath.Join(directory, artifact.name), artifact.value); err != nil {
			return err
		}
	}
	return nil
}

func writeJSONFile(path string, value any) error {
	return persistence.WriteJSONAtomic(path, value)
}
