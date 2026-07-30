package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/core"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/coverageanalysis"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/model"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/persistence"
)

func coverageFactorizeCommand(args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("modelfuzz-ng coverage-factorize", flag.ContinueOnError)
	flags.SetOutput(stderr)
	inputPath := flags.String("input", "", "实验目录、run artifact 目录或 model-states.json")
	outputPath := flags.String("output", "", "可选的因子化报告 JSON；必须位于输入实验目录之外")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("coverage-factorize 不接受位置参数")
	}
	if strings.TrimSpace(*inputPath) == "" {
		return fmt.Errorf("coverage-factorize 必须提供 -input")
	}
	runs, inputRoot, err := loadFactorizationRuns(*inputPath)
	if err != nil {
		return err
	}
	report, err := coverageanalysis.Factorize(runs)
	if err != nil {
		return err
	}
	if *outputPath == "" {
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")
		encoder.SetEscapeHTML(false)
		return encoder.Encode(report)
	}
	outputAbsolute, err := filepath.Abs(*outputPath)
	if err != nil {
		return fmt.Errorf("解析 coverage factorization 输出路径: %w", err)
	}
	if pathInside(outputAbsolute, inputRoot) {
		return fmt.Errorf(
			"coverage factorization 输出 %s 位于输入实验 %s 内；为保证离线分析只读，请选择外部路径",
			outputAbsolute, inputRoot)
	}
	if err := persistence.WriteJSONAtomic(outputAbsolute, report); err != nil {
		return err
	}
	_, err = fmt.Fprintf(stdout,
		"覆盖因子化完成: executions=%d frames=%d v2=%d facets=%d interactions=%d deterministic=%t output=%s\n",
		report.Executions, report.CoverageFrames, report.StateComparison.DistinctV2States,
		len(report.Facets), len(report.Interactions), report.RepeatedAnalysisEqual, outputAbsolute)
	return err
}

func loadFactorizationRuns(input string) ([]coverageanalysis.RunArtifact, string, error) {
	absolute, err := filepath.Abs(input)
	if err != nil {
		return nil, "", fmt.Errorf("解析 coverage factorization 输入路径: %w", err)
	}
	info, err := os.Stat(absolute)
	if err != nil {
		return nil, "", fmt.Errorf("读取 coverage factorization 输入 %s: %w", absolute, err)
	}
	root := absolute
	statePaths := make([]string, 0)
	if !info.IsDir() {
		if filepath.Base(absolute) != "model-states.json" {
			return nil, "", fmt.Errorf("文件输入必须是 model-states.json: %s", absolute)
		}
		root = filepath.Dir(absolute)
		statePaths = append(statePaths, absolute)
	} else {
		err = filepath.WalkDir(absolute, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if !entry.IsDir() && entry.Name() == "model-states.json" {
				statePaths = append(statePaths, path)
			}
			return nil
		})
		if err != nil {
			return nil, "", fmt.Errorf("扫描 model-states.json: %w", err)
		}
	}
	sort.Strings(statePaths)
	if len(statePaths) == 0 {
		return nil, "", fmt.Errorf(
			"输入 %s 中没有 model-states.json；因子化还需要同目录的 trace、model-events、config 和 result",
			absolute)
	}
	runs := make([]coverageanalysis.RunArtifact, 0, len(statePaths))
	for _, statePath := range statePaths {
		run, err := loadFactorizationRun(root, filepath.Dir(statePath))
		if err != nil {
			return nil, "", err
		}
		runs = append(runs, run)
	}
	return runs, root, nil
}

func loadFactorizationRun(root, directory string) (coverageanalysis.RunArtifact, error) {
	required := []string{"model-states.json", "model-events.json", "trace.json", "config.json", "result.json"}
	for _, name := range required {
		if _, err := os.Stat(filepath.Join(directory, name)); err != nil {
			return coverageanalysis.RunArtifact{}, fmt.Errorf(
				"run artifact %s 缺少 %s: %w", directory, name, err)
		}
	}
	var states []model.State
	var events []model.Event
	var trace core.Trace
	var config cliConfig
	var result struct {
		Initial core.Observation `json:"initial_observation"`
	}
	if err := persistence.ReadJSON(filepath.Join(directory, "model-states.json"), &states); err != nil {
		return coverageanalysis.RunArtifact{}, err
	}
	if err := persistence.ReadJSON(filepath.Join(directory, "model-events.json"), &events); err != nil {
		return coverageanalysis.RunArtifact{}, err
	}
	if err := persistence.ReadJSON(filepath.Join(directory, "trace.json"), &trace); err != nil {
		return coverageanalysis.RunArtifact{}, err
	}
	if err := persistence.ReadJSON(filepath.Join(directory, "config.json"), &config); err != nil {
		return coverageanalysis.RunArtifact{}, err
	}
	if err := persistence.ReadJSON(filepath.Join(directory, "result.json"), &result); err != nil {
		return coverageanalysis.RunArtifact{}, err
	}
	source := "unknown"
	var candidate struct {
		Source string `json:"source"`
	}
	if err := persistence.ReadJSON(filepath.Join(directory, "candidate.json"), &candidate); err == nil &&
		strings.TrimSpace(candidate.Source) != "" {
		source = candidate.Source
	}
	relative, err := filepath.Rel(root, directory)
	if err != nil {
		return coverageanalysis.RunArtifact{}, fmt.Errorf("计算 run artifact 路径: %w", err)
	}
	name := filepath.ToSlash(relative)
	if name == "." {
		name = filepath.Base(directory)
	}
	return coverageanalysis.RunArtifact{
		Name: name, Source: source, ModelConfig: config.Model, Initial: result.Initial,
		Trace: trace, ModelEvents: events, ModelStates: states,
	}, nil
}
