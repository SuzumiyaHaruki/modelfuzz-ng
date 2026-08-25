package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path"
	"strconv"
	"strings"
)

type LineCovGuider struct {
	ObjectPath      string
	GcovProgramPath string

	Lines map[string]bool

	*TLCStateGuider
}

var _ Guider = &LineCovGuider{}

func NewLineCovGuider(objectPath, tlcAddr, recordPath string) *LineCovGuider {
	return &LineCovGuider{
		ObjectPath:      objectPath,
		GcovProgramPath: "/usr/bin/gcov",
		Lines:           make(map[string]bool),
		TLCStateGuider:  NewTLCStateGuider(tlcAddr, recordPath, objectPath, "/usr/bin/gcov"),
	}
}

func (l *LineCovGuider) Check(iter string, trace *Trace, events *EventTrace, record bool) (bool, int) {
	l.TLCStateGuider.Check(iter, trace, events, record)

	// Get new lines
	lines, err := getLines(l.ObjectPath, l.GcovProgramPath)
	if err != nil {
		return false, 0
	}
	l.lock.Lock()
	oldLines := coveredLines(l.Lines)
	l.Lines = mergeCoverage(l.Lines, lines)
	newLines := coveredLines(l.Lines)
	l.lock.Unlock()

	return newLines > oldLines, newLines - oldLines
}

func (l *LineCovGuider) Reset() {
	l.TLCStateGuider.Reset()
	l.lock.Lock()
	l.Lines = make(map[string]bool)
	l.lock.Unlock()
}

type gcovOutput struct {
	CurrentWorkingDirectory string            `json:"current_working_directory"`
	DataFile                string            `json:"data_file"`
	FormatVersion           string            `json:"format_version"`
	GCCVersion              string            `json:"gcc_version"`
	Files                   []*gcovFileOutput `json:"files"`
}

type gcovFileOutput struct {
	File      string            `json:"file"`
	Functions []interface{}     `json:"functions"`
	Lines     []*gcovLineOutput `json:"lines"`
}

type gcovLineOutput struct {
	BlockIDs        []interface{}       `json:"block_ids"`
	Branches        []*gcovBranchOutput `json:"branches"`
	Calls           []interface{}       `json:"calls"`
	Count           int                 `json:"count"`
	LineNumber      int                 `json:"line_number"`
	UnexecutedBlock bool                `json:"unexecuted_block"`
	FunctionName    string              `json:"function_name"`
}

type gcovBranchOutput struct {
	Count       int  `json:"count"`
	Fallthrough bool `json:"fallthrough"`
	Throw       bool `json:"throw"`
}

func (g *gcovOutput) GetLines() map[string]bool {
	lines := make(map[string]bool)
	for _, f := range g.Files {
		for _, l := range f.Lines {
			key := f.File + "_" + strconv.Itoa(l.LineNumber)
			lines[key] = !l.UnexecutedBlock
		}
	}
	return lines
}

func (g *gcovOutput) GetBranches() map[string]bool {
	branches := make(map[string]bool)
	for _, f := range g.Files {
		for _, l := range f.Lines {
			for i, b := range l.Branches {
				if b.Count > 0 {
					key := fmt.Sprintf("%s_%d_%d", f.File, l.LineNumber, i)
					branches[key] = true
				}
			}
		}
	}
	return branches
}

func getLines(objectFilePath string, gcovProgramPath string) (map[string]bool, error) {
	lines := make(map[string]bool)

	if info, err := os.Stat(objectFilePath); err != nil || !info.IsDir() {
		return lines, fmt.Errorf("invalid object directory path: %s", err)
	}

	files, err := os.ReadDir(objectFilePath)
	if err != nil {
		return lines, err
	}
	for _, file := range files {
		if strings.Contains(file.Name(), "gcda") {
			args := make([]string, 0)
			args = append(args, "--json-format", "--stdout", file.Name())
			out := new(bytes.Buffer)
			cmd := exec.Command(gcovProgramPath, args...)
			cmd.Dir = objectFilePath
			cmd.Stdout = out

			if err := cmd.Run(); err != nil {
				return lines, fmt.Errorf("error running gcov program: %s", err)
			}

			data := &gcovOutput{}
			if err := json.Unmarshal(out.Bytes(), data); err != nil {
				return lines, fmt.Errorf("error unmarshalling gcov output: %s", err)
			}

			lines = mergeCoverage(lines, data.GetLines())
		}
	}

	return lines, nil
}

func mergeCoverage(one map[string]bool, two map[string]bool) map[string]bool {
	out := make(map[string]bool)
	for k, v := range one {
		out[k] = v
	}
	for k, v := range two {
		val, ok := one[k]
		if !ok {
			out[k] = v
		}
		out[k] = val || v
	}
	return out
}

func coveredLines(l map[string]bool) int {
	c := 0
	for _, v := range l {
		if v {
			c += 1
		}
	}
	return c
}

func getBranches(objectFilePath string, gcovProgramPath string) (map[string]bool, error) {
	branches := make(map[string]bool)

	if info, err := os.Stat(objectFilePath); err != nil || !info.IsDir() {
		return branches, fmt.Errorf("invalid object directory path: %s", err)
	}

	files, err := os.ReadDir(objectFilePath)
	if err != nil {
		return branches, err
	}
	for _, file := range files {
		if strings.Contains(file.Name(), "gcda") {
			args := make([]string, 0)
			args = append(args, "--json-format", "--stdout", "-b", file.Name())
			out := new(bytes.Buffer)
			cmd := exec.Command(gcovProgramPath, args...)
			cmd.Dir = objectFilePath
			cmd.Stdout = out

			if err := cmd.Run(); err != nil {
				return branches, fmt.Errorf("error running gcov program: %s", err)
			}

			data := &gcovOutput{}
			if err := json.Unmarshal(out.Bytes(), data); err != nil {
				return branches, fmt.Errorf("error unmarshalling gcov output: %s", err)
			}

			branches = mergeCoverage(branches, data.GetBranches())
		}
	}

	return branches, nil
}

func clearCovData(objectFilePath string) error {
	if info, err := os.Stat(objectFilePath); err != nil || !info.IsDir() {
		return fmt.Errorf("invalid object directory path: %s", err)
	}

	files, err := os.ReadDir(objectFilePath)
	if err != nil {
		return err
	}
	for _, file := range files {
		if strings.Contains(file.Name(), "gcda") {
			if err := os.Remove(path.Join(objectFilePath, file.Name())); err != nil {
				return fmt.Errorf("error removing file %s: %s", file.Name(), err)
			}
		}
	}
	return nil
}

type BranchCovGuider struct {
	ObjectPath      string
	GcovProgramPath string

	Branches map[string]bool

	*TLCStateGuider
}

var _ Guider = &BranchCovGuider{}

func NewBranchCovGuider(objectPath, tlcAddr, recordPath string) *BranchCovGuider {
	return &BranchCovGuider{
		ObjectPath:      objectPath,
		GcovProgramPath: "/usr/bin/gcov",
		Branches:        make(map[string]bool),
		TLCStateGuider:  NewTLCStateGuider(tlcAddr, recordPath, objectPath, "/usr/bin/gcov"),
	}
}

func (l *BranchCovGuider) Check(iter string, trace *Trace, events *EventTrace, record bool) (bool, int) {
	l.TLCStateGuider.Check(iter, trace, events, record)

	// Get new lines
	branches, err := getBranches(l.ObjectPath, l.GcovProgramPath)
	if err != nil {
		return false, 0
	}
	oldBranches := len(l.Branches)
	l.Branches = mergeCoverage(l.Branches, branches)
	newBranches := len(l.Branches)

	return newBranches > oldBranches, newBranches - oldBranches
}

func (l *BranchCovGuider) Reset() {
	l.TLCStateGuider.Reset()
	l.Branches = make(map[string]bool)
}
