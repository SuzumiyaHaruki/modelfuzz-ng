package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
)

var ErrNoTraceFile = errors.New("no trace file")

type TLCCoverageMeasurer struct {
	tracesPath string
	tlcAddr    string
	outPath    string

	tlcClient *TLCClient
	cov       map[int64]int
}

func NewTLCCoverageMeasurer(tracesPath, outPath, tlcAddr string) *TLCCoverageMeasurer {
	return &TLCCoverageMeasurer{
		tracesPath: tracesPath,
		tlcAddr:    tlcAddr,
		outPath:    outPath,

		tlcClient: NewTLCClient(tlcAddr),
		cov:       make(map[int64]int),
	}
}

func (p *TLCCoverageMeasurer) parseTrace(filePath string) (*EventTrace, error) {
	data, err := os.ReadFile(filePath)
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNoTraceFile
	}
	if err != nil {
		return nil, fmt.Errorf("error reading trace file: %s", err)
	}
	traceEvents := make([]Event, 0)
	if err = json.Unmarshal(data, &traceEvents); err != nil {
		return nil, fmt.Errorf("error parsing trace file: %s", err)
	}
	trace := &EventTrace{Events: traceEvents}
	return trace, nil
}

func (p *TLCCoverageMeasurer) Measure() error {
	tracePathCount, err := p.loadTracePathCount()
	if err != nil {
		return fmt.Errorf("error loading trace Paths: %s", err)
	}
	coverages := make([]int, 0)
	coverages = append(coverages, 0)
	for i := 1; i < tracePathCount; i++ {
		tracePath := path.Join(p.tracesPath, fmt.Sprintf("traces_bonusRlMax_%d.json", i))
		fmt.Printf("\rChecking %d/%d trace", i, tracePathCount)
		trace, err := p.parseTrace(tracePath)
		if errors.Is(err, ErrNoTraceFile) {
			continue
		}
		if err != nil {
			return fmt.Errorf("error parsing trace: %s", err)
		}
		states, err := p.tlcClient.SendTrace(trace)
		if err != nil {
			return fmt.Errorf("error sending trace to tlc: %s", err)
		}
		for _, state := range states {
			p.cov[state.Key]++
		}
		coverages = append(coverages, len(p.cov))
	}
	fmt.Println("... Done")
	if len(coverages) != 0 {
		fmt.Printf("Final coverage on the model is %d\n", coverages[len(coverages)-1])
	}
	jsonData, err := json.Marshal(map[string]interface{}{
		"coverages": coverages,
	})
	if err != nil {
		return fmt.Errorf("error marshalling json: %s", err)
	}
	if err = os.WriteFile(filepath.Join(p.outPath, "tlccoverage.json"), jsonData, 0644); err != nil {
		return fmt.Errorf("error writing coverage file: %s", err)
	}
	return nil
}

func (p *TLCCoverageMeasurer) loadTracePathCount() (int, error) {
	files, err := os.ReadDir(p.tracesPath)
	if err != nil {
		return 0, fmt.Errorf("error reading traces directory: %s", err)
	}
	count := 0
	for _, file := range files {
		if !strings.HasSuffix(file.Name(), ".json") {
			continue
		}

		splitFileNameParts := strings.Split(strings.TrimSuffix(file.Name(), ".json"), "_")
		if len(splitFileNameParts) != 3 {
			continue
		}
		val, err := strconv.Atoi(splitFileNameParts[2])
		if err != nil {
			continue
		}
		if val > count {
			count = val
		}
	}
	return count, nil
}
