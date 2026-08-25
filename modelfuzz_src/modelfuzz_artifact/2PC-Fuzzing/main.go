package main

import (
	"flag"
	"fmt"
	"math/rand"
	"slices"
	"strconv"

	"github.com/egeberkaygulcan/2PC-Fuzzing/fuzzing"
)

// GenerateVars 生成一个随机变量集合，表示一次 2PC 客户端请求想要访问/锁定哪些变量。
//
// 在 2PC 协议里，RM(Resource Manager) 会根据自己当前已经锁住的 Vars 判断是否能够
// 对某个请求返回 Prepared。如果请求访问的变量和已有锁冲突，RM 就会返回 Aborted。
// 因此，“请求携带哪些 vars”会直接影响协议走向：同样的消息调度顺序，在不同变量冲突
// 模式下可能产生 commit 或 abort 两种完全不同的事件 trace。
//
// 注意：当前 main.go 中这个函数没有被真正调用；实际请求变量主要由 fuzzing.Cluster
// 内部的 GenerateVars 生成。它更像是早期实验入口遗留下来的辅助函数，用来表达
// “客户端请求 = 随机选一组变量”的建模方式。
func GenerateVars() []int {
	// 使用固定 seed=1 创建局部随机源。固定 seed 的好处是结果可复现；
	// 缺点是如果这个函数被反复调用，每次都会生成同一条随机序列的开头。
	rand := rand.New(rand.NewSource(1))
	// 随机决定本次请求包含几个变量，范围是 1..3。
	numVars := rand.Intn(3) + 1
	// vars 是候选变量池。这里使用 0、1、2 三个变量编号。
	vars := make([]int, 0)
	// 初始化候选变量池。
	for i := 0; i < 3; i++ {
		vars = append(vars, i)
	}

	// randomVars 保存本次请求最终选择出的变量集合。
	randomVars := make([]int, 0)
	// 每轮从候选池中随机抽一个变量，并从候选池删除它。
	// 这样可以保证同一次请求里的变量不会重复。
	for i := 0; i < numVars; i++ {
		// 在剩余候选变量中随机选择一个下标。
		index := rand.Intn(len(vars))
		// 取出该下标对应的变量编号。
		v := vars[index]
		// 删除已选变量，避免之后再次选中它。
		vars = slices.Delete(vars, index, index+1)
		// 把变量加入本次请求的变量集合。
		randomVars = append(randomVars, v)
	}

	// 返回本次请求要访问的变量集合。
	return randomVars
}

// GetGuider 根据命令行参数创建对应的反馈引导器。
//
// 在 ModelFuzz 这类系统中，Fuzzer 负责产生 schedule 并驱动真实 2PC 实现执行；
// Guider 则负责评价“这次执行是否值得继续变异”。不同 guider 代表不同的反馈信号：
//   - trace：关注事件 trace 结构是否新颖；
//   - tlc：关注 TLA+ 模型状态覆盖率；
//   - line：关注 Go 代码行覆盖率；
//
// 默认使用 tlc，因为这套系统的核心思想就是用 TLA+ 模型状态空间作为 fuzzing 反馈。
//
// tlcAddr 一般形如 "localhost:2023"，对应已经启动的 TLC controlled server。
func GetGuider(guiderType, tlcAddr string) fuzzing.Guider {
	// 根据字符串选择 guider 类型。这里的字符串来自 -guider 命令行参数。
	switch guiderType {
	case "trace":
		// TraceCoverageGuider 会把 event trace 转成按节点串联的结构图，
		// 再对结构做 hash，用“是否出现过新的事件路径”作为反馈。
		return fuzzing.NewTraceCoverageGuider(tlcAddr)
	case "tlc":
		// TLCStateGuider 会把 event trace 发送给 TLC server，
		// 用 TLA+ 模型返回的状态 key 判断是否覆盖了新的模型状态。
		return fuzzing.NewTLCStateGuider(tlcAddr)
	case "line":
		// LineCoverageGuider 会读取 Go 覆盖率信息，用 server 包中新增覆盖行数作为反馈。
		// 它适合做传统代码覆盖率 baseline。
		return fuzzing.NewLineCoverageGuider(tlcAddr)
	default:
		// 未识别 guider 类型时退回 TLC 状态覆盖，保证实验仍然可以运行。
		return fuzzing.NewTLCStateGuider(tlcAddr)
	}
}

// runFuzzer 启动普通 fuzzing 路径。
//
// 这里所谓“普通”是相对于 RL baseline 而言的：它会创建 fuzzing.Fuzzer，
// 由 Fuzzer 自己维护 schedule 队列、seed、mutation 和 guider feedback。
// 如果命令行选择 -type RL，则不会走这个函数，而是进入 fuzzing.RunRL。
func runFuzzer(config fuzzing.FuzzerConfig) {
	// 批量实验中可能同时跑很多配置，这条输出用于确认当前进程已经开始执行 fuzzer。
	fmt.Println("Running fuzzer...")
	// NewFuzzer 会根据配置创建 2PC cluster、随机源、mutator 和统计对象。
	fuzzer := fuzzing.NewFuzzer(config)
	// Start 是主循环：不断生成/取出 schedule，执行一次 iteration，
	// 将 event trace 交给 guider，若发现新覆盖则变异 schedule 并继续探索。
	fuzzer.Start()
}

// main 是 2PC-Fuzzing 可执行程序的入口。
//
// 从系统整体看，这个函数做三件事：
//  1. 定义并解析命令行参数，把实验规模、随机种子、搜索策略和输出文件名收集起来；
//  2. 把这些参数组装成 fuzzing.FuzzerConfig，这是 fuzzing 包内部统一消费的配置结构；
//  3. 根据 -type 决定运行普通 ModelFuzz/Random/Trace/Line 路径，还是运行 RL baseline。
//
// main.go 不直接实现 2PC 协议，也不直接和 TLC 通信。协议实现在 server 包，
// 网络拦截在 intercept 包，搜索逻辑在 fuzzing 包，TLC HTTP 交互在 fuzzing/tlc_client.go。
func main() {
	// offset 是实验编号。批量实验脚本会用 offset=0,1,2... 跑多次实验。
	// 它同时参与随机种子和 stats 文件名，避免不同重复实验产生完全相同输出。
	offset := flag.Int("offset", 0, "The number ID for the experiment.")
	// fuzzerType 决定使用哪类搜索策略：
	// ModelFuzz 使用 TLC/trace/line 等 guider 反馈进行 seed + mutate；
	// Random 基本只随机生成 schedule；RL 则切换到 dist-rl-testing 环境。
	fuzzerType := flag.String("type", "ModelFuzz", "Type of fuzzer to run.")
	// fuzzDuration 是本次实验运行时长，单位是分钟。
	// 普通 Fuzzer.Start 会按 wall-clock time 停止，而不是按固定 iteration 数停止。
	fuzzDuration := flag.Int("duration", 60, "Duration of fuzzing in minutes.")
	// horizon 是一次 iteration/episode 内最多执行多少个 step。
	// 在 2PC-Fuzzing 中，一个 step 通常是一次消息调度或一次客户端请求注入。
	horizon := flag.Int("horizon", 100, "Number of execution steps taken in a test iteration.")
	// seedPopulationSize 表示每次 seed 阶段生成多少条随机 schedule。
	// 这些随机 schedule 是后续 guider 引导和 mutation 的初始搜索材料。
	seedPopulationSize := flag.Int("seeds", 20, "Number of seed schedules.")
	// reseedFrequency 表示每隔多少个 iteration 重新注入一批随机 seed。
	// 这样可以避免搜索长期困在同一批 mutated schedule 周围。
	reseedFrequency := flag.Int("reseed", 500, "The number of iterations to process before reseeding.")
	// randomSeed 是基础随机种子。实际使用时会加上 offset，
	// 使第 k 次重复实验拥有不同但可复现的随机序列。
	randomSeed := flag.Int("random", 2025, "Random seed.")
	// maxMessages 控制一次 Schedule step 最多从某条 from->to 队列投递多少条消息。
	// 它影响网络批量投递粒度：值越大，一步内可能释放越多排队消息。
	maxMessages := flag.Int("mm", 5, "Maximum number of messages to deliver at each execution step.")

	// guider 选择反馈信号类型。默认 tlc，即使用 TLA+ 模型状态覆盖率。
	guider := flag.String("guider", "tlc", "Guider type.")
	// guiderPort 是 TLC controlled server 的端口。
	// GetGuider 会把它拼成 localhost:<port>，TLCClient 再向 /execute 发送 event trace。
	guiderPort := flag.String("gport", "2023", "Port of the guider.")

	// numServers 表示 RM(Resource Manager) 的数量。
	// Cluster.NewCluster 实际会创建 0..NumServers 共 NumServers+1 个节点，其中 0 是 TM。
	numServers := flag.Int("servers", 3, "Number of servers (RMs) in the cluster.")
	// numVars 表示系统中可以被事务请求访问/锁定的变量数量。
	// 变量冲突会影响 RM 是返回 Prepared 还是 Aborted。
	numVars := flag.Int("vars", 2, "Number of locked variables in RMs.")
	// numRequests 表示每条 schedule 中会插入多少个客户端请求。
	// 每个请求最终会触发一轮 2PC prepare/commit/abort 过程。
	numRequests := flag.Int("requests", 5, "Number of client requests to be sent.")

	// numSwaps 控制 mutator 进行多少次交换类变异。
	// 当前 mutator 会交换 Schedule step 或交换 MaxMessages 字段。
	numSwaps := flag.Int("swaps", 5, "Number of swaps to be applied by the mutator.")
	// numRandom 控制随机节点变异次数，即把某些 Schedule step 的 From/Node 替换成其他节点。
	numRandom := flag.Int("numr", 0, "Number of random mutations to be applied by the mutator.")
	// numMutations 表示每次发现新覆盖后，基于当前 schedule 生成多少批变异后 schedule。
	// 在 fuzzer.go 中实际数量还会乘以本次新增覆盖数量。
	numMutations := flag.Int("mutations", 1, "Number of mutations to apply on the schedules.")

	// statsFile 是统计输出文件名前缀。最终文件名会拼接 offset，例如 mf_stats_0.json。
	statsFile := flag.String("filename", "mf_stats", "File name for the stats.")

	// 解析命令行参数。flag.Parse 之后，上面每个指针变量才会变成用户传入的值。
	flag.Parse()

	// 将命令行参数组装成 fuzzing 包统一使用的 FuzzerConfig。
	// 这个结构是 main.go 和 fuzzing 包之间的边界：main 只负责解释实验参数，
	// 真正的 schedule 生成、执行、反馈检查和变异都在 fuzzing.Fuzzer 内部完成。
	conf := fuzzing.FuzzerConfig{
		// Type 控制 Fuzzer.Start 中的行为分支，例如 Random 不根据新覆盖生成 mutation。
		Type: fuzzing.StringToFuzzerType(*fuzzerType),
		// RunDuration 是普通 fuzzing 的 wall-clock 运行时长。
		RunDuration: *fuzzDuration,
		// Horizon 是每条 schedule 的 step 上限。
		Horizon: *horizon,
		// SeedPopulationSize 控制 seed 阶段随机 schedule 的数量。
		SeedPopulationSize: *seedPopulationSize,
		// ReseedFrequency 控制多久重新生成一批随机 schedule。
		ReseedFrequency: *reseedFrequency,
		// RandomSeed 加上 offset，让重复实验既彼此不同又可复现。
		RandomSeed: int64(*randomSeed + *offset),
		// MaxMessages 限制每个 Schedule step 的最大消息投递数量。
		MaxMessages: *maxMessages,

		// Guider 是反馈组件。这里根据 -guider 和 -gport 创建具体实现。
		Guider: GetGuider(*guider, "localhost:"+*guiderPort),

		// NumServers 是 RM 数量；Cluster 内部会额外创建节点 0 作为 TM。
		NumServers: *numServers,
		// NumVars 是变量总数。
		NumVars: *numVars,
		// MaxVars 控制一次请求最多选择多少个变量；当前设置为 NumVars，
		// 表示一次请求最多可以覆盖全部变量。
		MaxVars: *numVars,
		// NumRequests 控制 schedule 中插入的客户端请求数量。
		NumRequests: *numRequests,
		// Election 当前固定为 false，表示不启用 leader election 场景；
		// 节点 0 固定是 TM，其余节点固定是 RM。
		Election: false,

		// MutatorSwaps 和 MutatorRandomCount 控制 schedule 变异强度。
		MutatorSwaps:       *numSwaps,
		MutatorRandomCount: *numRandom,
		// MutationCount 控制发现新覆盖后派生多少条新 schedule。
		MutationCount: *numMutations,

		// StatsFilename 是最终统计输出路径。拼上 offset 可以支持 run-all.sh 批量实验。
		StatsFilename: *statsFile + "_" + strconv.Itoa(*offset) + ".json",
	}

	// RL 类型走单独路径：它不使用 fuzzing.Fuzzer 的 seed/mutate 主循环，
	// 而是把 2PC cluster 包装成 dist-rl-testing 的 PEnvironment，
	// 由 RL policy 决定何时投递消息、何时注入请求。
	if *fuzzerType == "RL" {
		// RL 环境构造函数需要整数端口，所以这里把 guiderPort 从字符串转成 int。
		// 当前代码忽略转换错误；正常命令行输入应传入类似 "2023" 的端口字符串。
		port, _ := strconv.Atoi(*guiderPort)
		// RunRL 内部会创建 TwoPCRLEnv，并使用 dist-rl-testing 的 ParallelComparison 运行实验。
		fuzzing.RunRL(*offset, port, conf)
	} else {
		// 非 RL 类型统一走普通 Fuzzer。具体是 ModelFuzz、Random、Trace 还是 Line，
		// 由 conf.Type 和 conf.Guider 在 fuzzing 包内部共同决定。
		runFuzzer(conf)
	}

}
