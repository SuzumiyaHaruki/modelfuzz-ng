package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path"
	"time"

	"gonum.org/v1/plot"
	"gonum.org/v1/plot/plotter"
	"gonum.org/v1/plot/plotutil"
	"gonum.org/v1/plot/vg"
)

// Comparision 管理一组 benchmark，在同一 FuzzerConfig 下比较不同 guider/mutator。
//
// 它不是 fuzz 核心逻辑，而是实验脚手架：同一套 raft 环境和参数下，分别跑 random、
// trace coverage、line coverage、TLC state coverage 等策略，然后输出覆盖率曲线和 JSON 汇总。
// 命名保留原代码拼写。
type Comparision struct {
	config     *FuzzerConfig
	benchmarks map[string]benchmark
	plotPath   string
	runs       int
	runInfos   []runInfo
}

type benchmark struct {
	// guider 决定什么算“新覆盖”，mutator 决定如何围绕有效 trace 继续搜索。
	guider  Guider
	mutator Mutator
	key     string
}

// runInfo 保存一次 run 中所有 benchmark 的覆盖率和运行统计。
type runInfo struct {
	runTimes  map[string]time.Duration
	coverages map[string][]CoverageStats
	stats     map[string]map[string]interface{}
}

func NewComparision(plotPath string, config *FuzzerConfig, runs int) *Comparision {
	if plotPath != "" {
		if _, err := os.Stat(plotPath); err == nil {
			os.RemoveAll(plotPath)
		}
		os.Mkdir(plotPath, 0777)
	}

	return &Comparision{
		plotPath:   plotPath,
		config:     config,
		runInfos:   make([]runInfo, runs),
		runs:       runs,
		benchmarks: make(map[string]benchmark),
	}
}

func (c *Comparision) Add(name string, mutator Mutator, guider Guider) {
	// 注册一个实验分支，例如 tlcstate、traceCov、lineCov 或 random。
	c.benchmarks[name] = benchmark{
		guider:  guider,
		mutator: mutator,
		key:     name,
	}
}

func (c *Comparision) doRun(run int) runInfo {
	fmt.Printf("Starting run %d...\n", run+1)
	rI := runInfo{
		runTimes:  make(map[string]time.Duration),
		coverages: make(map[string][]CoverageStats),
		stats:     make(map[string]map[string]interface{}),
	}
	for key, b := range c.benchmarks {
		// 每个 benchmark 复用基础配置，只替换 guider/mutator。
		// 这里会重新创建 Fuzzer，因此不同 benchmark 的消息队列、RaftEnvironment 和 stats 相互独立。
		c.config.Guider = b.guider
		c.config.Mutator = b.mutator
		rI.coverages[key] = make([]CoverageStats, 0)
		fuzzer := NewFuzzer(c.config)
		start := time.Now()
		fmt.Printf("Running for benchmark: %s\n", key)
		rI.coverages[key] = fuzzer.Run()
		end := time.Since(start)
		rI.runTimes[key] = end
		fmt.Printf("\nRun time: %s\n", end.String())
		rI.stats[key] = fuzzer.stats
		b.guider.Reset(key)
	}
	return rI
}

func (c *Comparision) Run() {
	// 多次重复实验，最后统一画图和写 JSON 汇总。
	for i := 0; i < c.runs; i++ {
		c.runInfos[i] = c.doRun(i)
	}
	fmt.Printf("Completed running.\nStarting analysis...\n")
	c.record()
	fmt.Println("Completed analysis.")
}

func (c *Comparision) record() {
	// 将每次 run 的曲线、最终覆盖率、耗时和 fuzzer stats 汇总到 plotPath。
	// PNG 主要用于快速看趋势，data.json 用于后续脚本做更细的统计分析。

	runTimes := make(map[string][]time.Duration)
	finalCoverages := make(map[string][]CoverageStats)
	uniqueStateCoverages := make(map[string][][]int)
	stats := make(map[string][]map[string]interface{})

	for i := 0; i < c.runs; i++ {
		plotFile := path.Join(c.plotPath, fmt.Sprintf("%d.png", i))
		p := plot.New()
		p.Title.Text = "Comparison"
		p.X.Label.Text = "Iteration"
		p.Y.Label.Text = "States covered"

		k := 0
		for name, points := range c.runInfos[i].coverages {
			plotPoints := make([]plotter.XY, len(points))
			coveragePoints := make([]int, len(points))
			for j, point := range points {
				plotPoints[j] = plotter.XY{
					X: float64(j),
					Y: float64(point.UniqueStates),
				}
				coveragePoints[j] = point.UniqueStates
			}
			line, err := plotter.NewLine(plotter.XYs(plotPoints))
			if err != nil {
				continue
			}
			line.Color = plotutil.Color(k)
			p.Add(line)
			p.Legend.Add(name, line)

			if _, ok := uniqueStateCoverages[name]; !ok {
				uniqueStateCoverages[name] = make([][]int, 0)
			}
			uniqueStateCoverages[name] = append(uniqueStateCoverages[name], coveragePoints)

			k++
		}
		p.Save(4*vg.Inch, 4*vg.Inch, plotFile)

		for name, duration := range c.runInfos[i].runTimes {
			if _, ok := runTimes[name]; !ok {
				runTimes[name] = make([]time.Duration, 0)
			}
			runTimes[name] = append(runTimes[name], duration)
		}

		for name, points := range c.runInfos[i].coverages {
			if _, ok := finalCoverages[name]; !ok {
				finalCoverages[name] = make([]CoverageStats, 0)
			}
			finalCoverages[name] = append(finalCoverages[name], points[len(points)-1])
		}

		for name, rStats := range c.runInfos[i].stats {
			if _, ok := stats[name]; !ok {
				stats[name] = make([]map[string]interface{}, 0)
			}
			stats[name] = append(stats[name], rStats)
		}
	}

	recordData := make(map[string]map[string]interface{})

	for name, rts := range runTimes {
		if _, ok := recordData[name]; !ok {
			recordData[name] = make(map[string]interface{})
		}
		sum := time.Duration(0)
		for _, rt := range rts {
			sum += rt
		}
		avg := int64(sum) / int64(len(rts))
		recordData[name]["average_runtime"] = time.Duration(avg).String()
		recordData[name]["runtimes"] = rts
	}

	for name, coverages := range finalCoverages {
		sum := CoverageStats{}
		for _, cov := range coverages {
			sum.UniqueStates += cov.UniqueStates
			sum.UniqueStateTraces += cov.UniqueStateTraces
			sum.UniqueTraces += cov.UniqueTraces
		}
		avg := CoverageStats{
			UniqueStates:      sum.UniqueStates / len(coverages),
			UniqueStateTraces: sum.UniqueStateTraces / len(coverages),
			UniqueTraces:      sum.UniqueTraces / len(coverages),
		}
		recordData[name]["average_coverage"] = avg
		fmt.Printf("Final average state coverage of %s is %d\n", name, avg.UniqueStates)
		recordData[name]["coverages"] = coverages
	}
	for name, kStats := range stats {
		recordData[name]["stats"] = kStats
	}
	for name, coverages := range uniqueStateCoverages {
		recordData[name]["coverages"] = coverages
	}

	recordPath := path.Join(c.plotPath, "data.json")

	if cov, err := json.Marshal(recordData); err == nil {
		os.WriteFile(recordPath, cov, 0644)
	}
}
