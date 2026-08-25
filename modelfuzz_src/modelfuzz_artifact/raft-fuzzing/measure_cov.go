package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// TLCCoverageMeasurer 用于离线重放已经保存的 event trace，重新计算 TLC 状态覆盖曲线。
//
// 它适合在 fuzz 已经结束、TLC server 或统计方式有变化时重新计算覆盖率，
// 不需要再次运行 etcd-raft 实现。
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

func (p *TLCCoverageMeasurer) parseTrace(filePath string) (*List[*Event], error) {
	// 这里读取的是单独保存的 event trace JSON，而不是 SchedulingChoice trace。
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("error reading trace file: %s", err)
	}
	trace := &List[*Event]{}
	if err = json.Unmarshal(data, trace); err != nil {
		return nil, fmt.Errorf("error parsing trace file: %s", err)
	}
	return trace, nil
}

func (p *TLCCoverageMeasurer) Measure() error {
	// 按 trace_*.json 顺序发送给 TLC，并记录累计 unique state 数量。
	// 输出 tlccoverage.json，里面是一条随 trace 数增长的累计覆盖曲线。
	tracePathCount, err := p.loadTracePathCount()
	if err != nil {
		return fmt.Errorf("error loading trace Paths: %s", err)
	}
	coverages := make([]int, 0)
	coverages = append(coverages, 0)
	for i := 1; i < tracePathCount; i++ {
		tracePath := path.Join(p.tracesPath, fmt.Sprintf("trace_%d.json", i))
		fmt.Printf("\rChecking %d/%d trace", i, tracePathCount)
		trace, err := p.parseTrace(tracePath)
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
	// 原实现只统计 json 文件数量，默认文件名为 trace_1.json ... trace_n.json。
	files, err := os.ReadDir(p.tracesPath)
	if err != nil {
		return 0, fmt.Errorf("error reading traces directory: %s", err)
	}
	count := 0
	for _, file := range files {
		if !strings.HasSuffix(file.Name(), ".json") {
			continue
		}
		count++
	}
	return count, nil
}
