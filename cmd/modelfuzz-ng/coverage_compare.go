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

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/coverageanalysis"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/model"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/persistence"
)

func coverageCompareCommand(args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("modelfuzz-ng coverage-compare", flag.ContinueOnError)
	flags.SetOutput(stderr)
	inputPath := flags.String("input", "", "实验目录、run artifact 目录或 model-states.json")
	outputPath := flags.String("output", "", "可选的比较报告 JSON；必须位于输入实验目录之外")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("coverage-compare 不接受位置参数")
	}
	if strings.TrimSpace(*inputPath) == "" {
		return fmt.Errorf("coverage-compare 必须提供 -input")
	}
	executions, inputRoot, err := loadCoverageExecutions(*inputPath)
	if err != nil {
		return err
	}
	report, err := coverageanalysis.Compare(executions)
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
		return fmt.Errorf("解析 coverage comparison 输出路径: %w", err)
	}
	if pathInside(outputAbsolute, inputRoot) {
		return fmt.Errorf("coverage comparison 输出 %s 位于输入实验 %s 内；为保证离线分析只读，请选择外部路径",
			outputAbsolute, inputRoot)
	}
	if err := persistence.WriteJSONAtomic(outputAbsolute, report); err != nil {
		return err
	}
	_, err = fmt.Fprintf(stdout,
		"覆盖比较完成: executions=%d state_visits=%d v1=%d v2=%d reduction=%.2f%% v1_new_v2_old=%d v2_new=%d deterministic=%t output=%s\n",
		report.Executions, report.ModelStateVisits, report.DistinctV1States, report.DistinctV2States,
		report.ReductionRatio*100, report.V1NewButV2OldExecutions, report.V2NewExecutions,
		report.RepeatedV2AnalysisEqual, outputAbsolute)
	return err
}

func loadCoverageExecutions(input string) ([]coverageanalysis.Execution, string, error) {
	absolute, err := filepath.Abs(input)
	if err != nil {
		return nil, "", fmt.Errorf("解析 coverage comparison 输入路径: %w", err)
	}
	info, err := os.Stat(absolute)
	if err != nil {
		return nil, "", fmt.Errorf("读取 coverage comparison 输入 %s: %w", absolute, err)
	}
	root := absolute
	paths := make([]string, 0)
	if !info.IsDir() {
		root = filepath.Dir(absolute)
		paths = append(paths, absolute)
	} else {
		err = filepath.WalkDir(absolute, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if !entry.IsDir() && entry.Name() == "model-states.json" {
				paths = append(paths, path)
			}
			return nil
		})
		if err != nil {
			return nil, "", fmt.Errorf("扫描 model-states.json: %w", err)
		}
	}
	sort.Strings(paths)
	if len(paths) == 0 {
		return nil, "", fmt.Errorf(
			"输入 %s 中没有 model-states.json；runs.jsonl、Trace 或聚合 report 不包含重建 v2 所需的完整 TLC 状态文本",
			absolute)
	}
	executions := make([]coverageanalysis.Execution, 0, len(paths))
	for _, path := range paths {
		var states []model.State
		if err := persistence.ReadJSON(path, &states); err != nil {
			return nil, "", err
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return nil, "", fmt.Errorf("计算模型状态路径: %w", err)
		}
		name := filepath.ToSlash(filepath.Dir(relative))
		if name == "." {
			name = filepath.Base(path)
		}
		executions = append(executions, coverageanalysis.Execution{Name: name, States: states})
	}
	return executions, root, nil
}

func pathInside(path, root string) bool {
	relative, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return relative == "." || (relative != ".." &&
		!strings.HasPrefix(relative, ".."+string(filepath.Separator)))
}
