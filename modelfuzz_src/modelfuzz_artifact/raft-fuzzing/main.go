package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

var (
	// episodes 是一次 fuzz/compare 运行中执行的 iteration 数量。
	// 每个 iteration 会生成或重放一条调度 trace，并收集对应的 event trace。
	episodes int
	// horizon 是单条 trace 的长度，也就是每个 iteration 中 fuzzer 推进的 step 数。
	horizon int
	// savePath 是 compare 命令保存实验结果、覆盖率曲线和 trace 的目录。
	savePath string
	// replicas 是 RaftEnvironment 中模拟的 raft 节点数量。
	replicas int
	// requests 是每条 trace 中注入的客户端 proposal 数量。
	requests int
	// numRuns 是 compare 命令重复运行整组 benchmark 的次数，用于平滑随机性。
	numRuns int
	// recordTraces 控制 guider 是否把探索到的 trace/eventTrace/stateTrace 写入磁盘。
	recordTraces bool
)

func main() {
	// rootCommand 是所有子命令的入口。这里使用 cobra 只是为了统一解析命令行参数；
	// 真正的 fuzz 逻辑分别在 FuzzCommand、OneCommand(compare) 和 MeasureCommand 中。
	rootCommand := &cobra.Command{}
	rootCommand.PersistentFlags().IntVarP(&episodes, "episodes", "e", 10000, "Number of episodes to run")
	rootCommand.PersistentFlags().IntVar(&horizon, "horizon", 100, "Horizon of each episode")
	rootCommand.PersistentFlags().StringVarP(&savePath, "save", "s", "results", "Save the results to the specified path")
	rootCommand.PersistentFlags().IntVarP(&replicas, "replicas", "r", 3, "Num of replicas to run in environment")
	// ！！！
	rootCommand.PersistentFlags().IntVar(&requests, "requests", 1, "Number of client requests to inject per random trace")
	rootCommand.PersistentFlags().IntVar(&numRuns, "runs", 5, "Number of runs to average over")
	rootCommand.PersistentFlags().BoolVar(&recordTraces, "record-traces", false, "Record the traces explored")

	// fuzz：跑单一配置的 fuzz。
	// compare：对比 trace coverage、line coverage、TLC state coverage、random 等策略。
	// measure：对已经保存的 traces 重新计算 TLC 覆盖率。
	rootCommand.AddCommand(FuzzCommand())
	rootCommand.AddCommand(OneCommand())
	rootCommand.AddCommand(MeasureCommand())

	if err := rootCommand.Execute(); err != nil {
		fmt.Println(err)
	}
}

func MeasureCommand() *cobra.Command {
	var tracesPath string
	var tlcAddr string
	var outPath string

	// measure 不重新执行 raft 实现，只读取磁盘上的 traces，把 event trace 重新送给 TLC server，
	// 计算或汇总模型状态覆盖率。它适合在 fuzz 结束后离线分析已有结果。
	cmd := &cobra.Command{
		Use: "measure",
		Run: func(cmd *cobra.Command, args []string) {
			// 如果没有单独指定输出目录，就把测量结果写回 tracesPath。
			if outPath == "" {
				outPath = tracesPath
			}
			m := NewTLCCoverageMeasurer(tracesPath, outPath, tlcAddr)
			if err := m.Measure(); err != nil {
				fmt.Println(err)
			}
		},
	}
	cmd.Flags().StringVar(&tracesPath, "traces", "traces", "Path to traces")
	cmd.Flags().StringVar(&tlcAddr, "tlc", "localhost:2023", "TLC Server address")
	cmd.Flags().StringVar(&outPath, "out", "", "Output path")

	return cmd
}

func FuzzCommand() *cobra.Command {
	// fuzz 是最简单的单策略入口：构造一个 Fuzzer，使用固定配置跑 episodes 条 trace。
	// 当前配置使用 line coverage guider 和 EmptyMutator，因此更像是一次基础覆盖率探索，
	// 不做基于模型新状态的复杂 trace 变异。
	return &cobra.Command{
		Use: "fuzz",
		RunE: func(cmd *cobra.Command, args []string) error {
			fuzzer := NewFuzzer(&FuzzerConfig{
				Iterations: episodes,
				Steps:      horizon,
				Strategy:   NewRandomStrategy(),
				// LineCoverageGuider 仍会连接 TLC server，但主要反馈来自被测代码行覆盖率。
				Guider:  NewLineCoverageGuider("127.0.0.1:2023", "traces", recordTraces),
				Mutator: &EmptyMutator{},
				RaftEnvironmentConfig: RaftEnvironmentConfig{
					Replicas: replicas,
					// ElectionTick/HeartbeatTick 是 raft 逻辑时间参数；
					// 真实推进速度由下面的 TicksPerStep 决定。
					ElectionTick:  20,
					HeartbeatTick: 2,
					// 每个 fuzz step 中，每个节点调用 Tick 的次数。
					// 数值越大，选举和心跳越容易在较短 trace 内发生。
					TicksPerStep: 2,
				},
				// MutPerTrace 表示当 guider 发现新覆盖时，为该 trace 生成多少个变异后续。
				// 这里 Mutator 为空，所以该参数实际影响不大。
				MutPerTrace: 5,
				// ！！！
				NumberRequests: requests,
				// CrashQuota 控制每条随机 trace 中安排多少次节点宕机/恢复。
				CrashQuota: 2,
				// MaxMessages 控制一次网络调度最多从某个 from->to 队列投递多少条消息。
				MaxMessages: 10,
				// SeedPopulationSize 控制 reseed 时生成多少条随机种子 trace。
				SeedPopulationSize: 10,
			})
			fuzzer.Run()
			return nil
		},
	}
}

func OneCommand() *cobra.Command {
	// compare 是论文/实验风格的入口：相同环境下跑多种 guider/mutator 组合，
	// 比较它们随 iteration 增长的模型状态覆盖率和代码覆盖率。
	return &cobra.Command{
		Use: "compare",
		Run: func(cmd *cobra.Command, args []string) {

			c := NewComparision(savePath, &FuzzerConfig{
				Iterations: episodes,
				Steps:      horizon,
				Strategy:   NewRandomStrategy(),
				Mutator:    &EmptyMutator{},
				// Checker 是额外的 bug oracle。这里检查 committed 日志是否满足简单的串行一致性期望。
				Checker: SerializabilityChecker(),
				RaftEnvironmentConfig: RaftEnvironmentConfig{
					Replicas: replicas,
					// 较小的 ElectionTick 会让随机调度更容易触发超时和重新选举，
					// 因而更容易探索 leader 切换相关状态。
					ElectionTick:  20,
					HeartbeatTick: 4,
					// TicksPerStep 不宜超过 ElectionTick/(replica+1) 太多，
					// 否则单个 step 内时间推进过快，某些节点可能长期得不到消息投递机会。
					TicksPerStep: 3,
				},
				// MutPerTrace 太大可能导致搜索过度围绕少数 trace 局部扩展。
				MutPerTrace:    3,
				NumberRequests: requests,
				// CrashQuota 越大，故障场景越多；但对纯 random baseline 来说过多故障会让有效执行变少。
				CrashQuota: 10,
				// MaxMessages 越小，网络越“慢”，更容易形成延迟/乱序场景；
				// 但太小也可能让系统长期无法取得进展。
				MaxMessages:        5,
				SeedPopulationSize: 20,
				// 每隔 ReseedFrequency 次 iteration 重新注入一批随机种子 trace，
				// 避免搜索完全被早期发现的轨迹牵引。
				ReseedFrequency: 200,
			}, numRuns)
			// 组合 mutator 会在已有有效 trace 上同时扰动：
			//  1. 哪些节点 crash；
			//  2. 哪些 from/to 网络调度点被交换；
			//  3. 每次调度最多投递多少消息。
			combinedMutator := CombineMutators(NewSwapCrashNodeMutator(2), NewSwapNodeMutator(20), NewSwapMaxMessagesMutator(20))
			// traceCov：把 event trace 自身的新结构作为反馈。
			c.Add("traceCov", combinedMutator, NewTraceCoverageGuider("127.0.0.1:2023", "traces", recordTraces))
			// lineCov：以 Go 代码行覆盖率作为反馈。
			c.Add("lineCov", combinedMutator, NewLineCoverageGuider("127.0.0.1:2023", "traces", recordTraces))
			// tlcstate：以 TLA+ 模型状态覆盖率作为反馈，这是 ModelFuzz 的核心策略。
			c.Add("tlcstate", combinedMutator, NewTLCStateGuider("127.0.0.1:2023", "traces", recordTraces))
			// random：不做 trace 变异，只用随机 trace 作为 baseline。
			c.Add("random", &EmptyMutator{}, NewTLCStateGuider("127.0.0.1:2023", "traces", recordTraces))

			c.Run()
		},
	}
}
