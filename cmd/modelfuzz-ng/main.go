package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/adapters/etcdraft"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/core"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/corpus"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/engine"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/experiment"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/llm"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/model"
	raftmodel "github.com/SuzumiyaHaruki/modelfuzz-ng/internal/model/raft"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/model/tlc"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/mutation"
	raftoracle "github.com/SuzumiyaHaruki/modelfuzz-ng/internal/oracle/raft"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/persistence"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/plan"
	randompolicy "github.com/SuzumiyaHaruki/modelfuzz-ng/internal/policy"
	runtimepkg "github.com/SuzumiyaHaruki/modelfuzz-ng/internal/runtime"
	tracepkg "github.com/SuzumiyaHaruki/modelfuzz-ng/internal/trace"
	raft "go.etcd.io/raft/v3"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := runCLI(ctx, os.Args[1:], os.Stdout, os.Stderr); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return
		}
		fmt.Fprintf(os.Stderr, "modelfuzz-ng: %v\n", err)
		os.Exit(1)
	}
}

func runCLI(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		printRootUsage(stderr)
		return fmt.Errorf("缺少子命令")
	}
	switch args[0] {
	case "run":
		return runCommand(ctx, args[1:], stdout, stderr)
	case "replay":
		return replayCommand(ctx, args[1:], stdout, stderr)
	case "experiment":
		return experimentCommand(ctx, args[1:], stdout, stderr)
	case "help", "-h", "--help":
		printRootUsage(stdout)
		return flag.ErrHelp
	default:
		printRootUsage(stderr)
		return fmt.Errorf("未知子命令 %q", args[0])
	}
}

func experimentCommand(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("modelfuzz-ng experiment", flag.ContinueOnError)
	flags.SetOutput(stderr)
	outputPath := flags.String("output", "", "新的批量实验目录；恢复时可省略")
	resumePath := flags.String("resume", "", "从已有实验目录的 checkpoint.json 恢复")
	artifactPolicyText := flags.String("artifact-policy", "all", "逐运行产物策略: all、retained、failures、summary")
	checkpointEvery := flags.Int("checkpoint-every", 1, "每完成多少条执行原子保存一次 checkpoint")
	configPath := flags.String("config", "", "可选的 JSON 配置文件")
	tlcAddress := flags.String("tlc", "", "覆盖配置中的 controlled TLC 地址")
	runs := flags.Int("runs", 10, "反馈闭环中的总执行次数")
	maxPlanActions := flags.Int("max-plan-actions", 0, "每条变长轨迹最多处理的 PlanAction 数；默认使用 config（内置1000）")
	largestTerm := flags.Uint64("largest-term", 0, "覆盖模型 LargestTerm 边界")
	maxLogIndex := flags.Uint64("max-log-index", 0, "覆盖模型 MaxLogIndex 边界")
	snapshotThreshold := flags.Uint64("snapshot-threshold", 0, "每新增多少条已应用日志创建 snapshot；0表示关闭")
	snapshotRetainEntries := flags.Uint64("snapshot-retain-entries", 0, "snapshot 压缩后额外保留的日志条目数")
	voteQuorumDivisor := flags.Int("vote-quorum-divisor", 0, "选举 quorum 除数：2为正常，3复现 ModelFuzz mutant")
	parallelism := flags.Int("parallelism", 1, "并发运行数；controlled TLC 当前只能为1")
	seedText := flags.String("seed", "", "覆盖第一条运行的随机种子；后续逐次加1")
	llmInit := flags.Bool("llm-init", false, "使用 LLM 生成初始 Plan；默认使用在线随机策略")
	llmMutate := flags.Bool("llm-mutate", false, "使用 LLM 变异 Corpus Plan；默认使用本地随机变异")
	initialPopulation := flags.Int("initial-population", 4, "开始反馈变异前准备的种子 Plan 数")
	mutationsPerState := flags.Int("mutations-per-state", 1, "每个全局新模型状态生成的变异数")
	maxMutationsPerCorpus := flags.Int("max-mutations-per-corpus", 2, "单个 Corpus 条目的最大变异数")
	maxReadyCandidates := flags.Int("max-ready-candidates", 4096, "Ready 候选队列上限；满时淘汰最旧候选")
	randomSeedInterval := flags.Int("random-seed-interval", 0, "每完成多少次执行优先注入在线随机种子；0 表示关闭")
	randomSeedsPerInterval := flags.Int("random-seeds-per-interval", 1, "每次周期注入的在线随机种子数")
	llmProvider := flags.String("llm-provider", string(llm.DefaultProvider), "LLM provider: deepseek、glm、qwen 或 kimi")
	llmModel := flags.String("llm-model", "", "覆盖 provider 的默认模型")
	llmBaseURL := flags.String("llm-base-url", "", "覆盖 provider 的 OpenAI-compatible API 基础地址")
	llmAPIKeyEnv := flags.String("llm-api-key-env", "", "覆盖保存 API Key 的环境变量名称")
	llmTimeout := flags.Duration("llm-timeout", 90*time.Second, "单次 LLM 请求超时")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("无法识别的位置参数: %v", flags.Args())
	}
	setFlags := make(map[string]bool)
	flags.Visit(func(value *flag.Flag) { setFlags[value.Name] = true })
	if *resumePath == "" && *outputPath == "" {
		flags.Usage()
		return fmt.Errorf("新实验需要 -output，恢复实验需要 -resume")
	}
	var resumeCheckpoint *experiment.Checkpoint
	var previousLLMStats llm.Stats
	if *resumePath != "" {
		resumeDirectory := filepath.Clean(*resumePath)
		if *outputPath != "" && filepath.Clean(*outputPath) != resumeDirectory {
			return fmt.Errorf("-resume 与 -output 必须指向同一目录")
		}
		*outputPath = resumeDirectory
		if setFlags["config"] {
			return fmt.Errorf("恢复实验使用原目录中的 config.json，不能同时指定 -config")
		}
		*configPath = filepath.Join(resumeDirectory, "config.json")
		resumeCheckpoint = &experiment.Checkpoint{}
		if err := persistence.ReadJSON(filepath.Join(resumeDirectory, "checkpoint.json"), resumeCheckpoint); err != nil {
			return fmt.Errorf("读取恢复点: %w", err)
		}
		var savedSettings experimentSettings
		if err := persistence.ReadJSON(filepath.Join(resumeDirectory, "experiment-settings.json"), &savedSettings); err != nil {
			return fmt.Errorf("读取原实验设置: %w", err)
		}
		if err := persistence.ReadJSON(filepath.Join(resumeDirectory, "llm-stats.json"), &previousLLMStats); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("读取原 LLM 统计: %w", err)
		}
		if !setFlags["artifact-policy"] {
			*artifactPolicyText = string(savedSettings.ArtifactPolicy)
		}
		if !setFlags["checkpoint-every"] {
			*checkpointEvery = savedSettings.CheckpointEvery
		}
		if !setFlags["llm-init"] {
			*llmInit = savedSettings.LLMInit
		}
		if !setFlags["llm-mutate"] {
			*llmMutate = savedSettings.LLMMutate
		}
		if !setFlags["llm-provider"] && savedSettings.LLMProvider != "" {
			*llmProvider = string(savedSettings.LLMProvider)
		}
		if !setFlags["llm-model"] {
			*llmModel = savedSettings.LLMModel
		}
		if !setFlags["llm-base-url"] {
			*llmBaseURL = savedSettings.LLMBaseURL
		}
		if !setFlags["llm-api-key-env"] {
			*llmAPIKeyEnv = savedSettings.LLMAPIKeyEnv
		}
		if !setFlags["llm-timeout"] && savedSettings.LLMTimeoutMillis > 0 {
			*llmTimeout = time.Duration(savedSettings.LLMTimeoutMillis) * time.Millisecond
		}
		resumeValues := map[string][2]int{
			"runs": {*runs, resumeCheckpoint.Config.Runs}, "parallelism": {*parallelism, resumeCheckpoint.Config.Parallelism},
			"initial-population":        {*initialPopulation, resumeCheckpoint.Config.InitialPopulation},
			"mutations-per-state":       {*mutationsPerState, resumeCheckpoint.Config.MutationsPerNewState},
			"max-mutations-per-corpus":  {*maxMutationsPerCorpus, resumeCheckpoint.Config.MaxMutationsPerCorpus},
			"max-ready-candidates":      {*maxReadyCandidates, resumeCheckpoint.Config.MaxReadyCandidates},
			"random-seed-interval":      {*randomSeedInterval, resumeCheckpoint.Config.RandomSeedInterval},
			"random-seeds-per-interval": {*randomSeedsPerInterval, resumeCheckpoint.Config.RandomSeedsPerInterval},
		}
		for name, values := range resumeValues {
			if setFlags[name] && values[0] != values[1] {
				return fmt.Errorf("恢复时 -%s=%d 与 checkpoint 中的 %d 不一致", name, values[0], values[1])
			}
		}
		*runs = resumeCheckpoint.Config.Runs
		*parallelism = resumeCheckpoint.Config.Parallelism
		*initialPopulation = resumeCheckpoint.Config.InitialPopulation
		*mutationsPerState = resumeCheckpoint.Config.MutationsPerNewState
		*maxMutationsPerCorpus = resumeCheckpoint.Config.MaxMutationsPerCorpus
		*maxReadyCandidates = resumeCheckpoint.Config.MaxReadyCandidates
		*randomSeedInterval = resumeCheckpoint.Config.RandomSeedInterval
		*randomSeedsPerInterval = resumeCheckpoint.Config.RandomSeedsPerInterval
	}
	if *checkpointEvery <= 0 {
		return fmt.Errorf("-checkpoint-every 必须为正数")
	}
	artifactPolicy, err := parseArtifactPolicy(*artifactPolicyText)
	if err != nil {
		return err
	}
	config, err := loadCLIConfig(*configPath)
	if err != nil {
		return err
	}
	if *tlcAddress != "" {
		config.TLC.Address = *tlcAddress
	}
	if resumeCheckpoint != nil && (*seedText != "" || setFlags["tlc"] || setFlags["largest-term"] ||
		setFlags["max-log-index"] || setFlags["snapshot-threshold"] || setFlags["snapshot-retain-entries"] ||
		setFlags["vote-quorum-divisor"]) {
		return fmt.Errorf("恢复时不能覆盖 seed、TLC 地址、模型边界、SnapshotPolicy 或 FaultPolicy")
	}
	if *seedText != "" {
		seed, err := strconv.ParseInt(*seedText, 10, 64)
		if err != nil {
			return fmt.Errorf("解析 -seed %q: %w", *seedText, err)
		}
		config.Seed = seed
	}
	if setFlags["largest-term"] {
		if *largestTerm == 0 {
			return fmt.Errorf("-largest-term 必须为正数")
		}
		config.Model.LargestTerm = *largestTerm
	}
	if setFlags["max-log-index"] {
		if *maxLogIndex == 0 {
			return fmt.Errorf("-max-log-index 必须为正数")
		}
		config.Model.MaxLogIndex = *maxLogIndex
	}
	if setFlags["snapshot-threshold"] {
		config.Raft.Snapshot.Threshold = *snapshotThreshold
	}
	if setFlags["snapshot-retain-entries"] {
		config.Raft.Snapshot.RetainEntries = *snapshotRetainEntries
	}
	if setFlags["vote-quorum-divisor"] {
		config.Raft.Faults.VoteQuorumDivisor = *voteQuorumDivisor
	}
	if resumeCheckpoint != nil {
		if setFlags["max-plan-actions"] && *maxPlanActions != config.Engine.MaxPlanActions {
			return fmt.Errorf("恢复时 -max-plan-actions=%d 与原配置中的 %d 不一致", *maxPlanActions, config.Engine.MaxPlanActions)
		}
		*maxPlanActions = config.Engine.MaxPlanActions
		if config.Seed != resumeCheckpoint.Config.BaseSeed {
			return fmt.Errorf("原 config.json 的 seed 与 checkpoint 不一致")
		}
	} else if setFlags["max-plan-actions"] {
		config.Engine.MaxPlanActions = *maxPlanActions
	} else {
		*maxPlanActions = config.Engine.MaxPlanActions
	}
	if *maxPlanActions <= 0 {
		return fmt.Errorf("-max-plan-actions 必须为正数")
	}
	if config.TLC.Address != "" && *parallelism != 1 {
		return fmt.Errorf("旧 controlled TLC 不保证请求隔离，连接 TLC 时 -parallelism 必须为1")
	}
	if err := validateTLCModelBounds(ctx, config, stderr); err != nil {
		return err
	}
	if err := validateAlignedNodes(config.Raft.NodeIDs, config.Model.NodeIDs); err != nil {
		return fmt.Errorf("raft/模型配置不一致: %w", err)
	}
	experimentConfig := experiment.Config{
		Runs: *runs, BaseSeed: config.Seed, Parallelism: *parallelism,
		InitialPopulation: *initialPopulation, MutationsPerNewState: *mutationsPerState,
		MaxMutationsPerCorpus: *maxMutationsPerCorpus, RandomSeedInterval: *randomSeedInterval,
		RandomSeedsPerInterval: *randomSeedsPerInterval, MaxReadyCandidates: *maxReadyCandidates,
	}
	runner, err := experiment.New(experimentConfig)
	if err != nil {
		return err
	}
	experimentConfig = runner.Config()
	policyConfig := randompolicy.DefaultRandomConfig()
	policyConfig.NodeIDs = append([]core.NodeID(nil), config.Raft.NodeIDs...)
	policyConfig.MaxValue = config.Model.MaxValue
	policyConfig.MaxLogIndex = config.Model.MaxLogIndex
	policyConfig.LargestTerm = config.Model.LargestTerm
	mutationConfig := mutation.RandomConfig{
		NodeIDs: append([]core.NodeID(nil), config.Raft.NodeIDs...), MaxValue: config.Model.MaxValue,
		MaxTicks: 5, MaxActions: *maxPlanActions, MaxCrashed: policyConfig.MaxCrashed,
	}
	localMutator, err := mutation.NewRandom(mutationConfig)
	if err != nil {
		return err
	}
	var selectedMutator mutation.Mutator = localMutator
	var llmClient *llm.Client
	var effectiveLLMProvider llm.Provider
	var effectiveLLMModel, effectiveLLMBaseURL, effectiveAPIKeyEnv string
	feedbackOptions := experiment.FeedbackOptions{InitializerName: "random_init"}
	if *llmInit || *llmMutate {
		effectiveLLMProvider, err = llm.ParseProvider(*llmProvider)
		if err != nil {
			return err
		}
		preset, err := llm.Preset(effectiveLLMProvider)
		if err != nil {
			return err
		}
		effectiveLLMModel = *llmModel
		if effectiveLLMModel == "" {
			effectiveLLMModel = preset.DefaultModel
		}
		effectiveLLMBaseURL = *llmBaseURL
		if effectiveLLMBaseURL == "" {
			effectiveLLMBaseURL = preset.DefaultBaseURL
		}
		effectiveAPIKeyEnv = *llmAPIKeyEnv
		if effectiveAPIKeyEnv == "" {
			effectiveAPIKeyEnv = preset.DefaultAPIKeyEnv
		}
		apiKey := os.Getenv(effectiveAPIKeyEnv)
		if apiKey == "" {
			return fmt.Errorf("开启 %s LLM 后必须通过 %s 环境变量提供 API Key；不要写入配置或仓库", effectiveLLMProvider, effectiveAPIKeyEnv)
		}
		llmClient, err = llm.NewClient(llm.Config{
			Provider: effectiveLLMProvider, BaseURL: effectiveLLMBaseURL,
			APIKey: apiKey, Model: effectiveLLMModel, Timeout: *llmTimeout,
		})
		if err != nil {
			return err
		}
		planner, err := randompolicy.NewLLMPlanner(llmClient, randompolicy.LLMConfig{
			NodeIDs: append([]core.NodeID(nil), config.Raft.NodeIDs...), MaxValue: config.Model.MaxValue,
			MaxTicks: mutationConfig.MaxTicks, MaxActions: *maxPlanActions,
			MaxCrashed: policyConfig.MaxCrashed, MaxLogIndex: config.Model.MaxLogIndex,
			LargestTerm: config.Model.LargestTerm,
		})
		if err != nil {
			return err
		}
		if *llmInit {
			feedbackOptions.InitializerName = "llm_init"
			feedbackOptions.Initializer = func(ctx context.Context, count int, _ int64) ([]plan.PlanSequence, error) {
				return planner.Generate(ctx, randompolicy.GenerationRequest{Mode: randompolicy.GenerationInitial, Count: count})
			}
		}
		if *llmMutate {
			selectedMutator, err = mutation.NewLLM(planner)
			if err != nil {
				return err
			}
		}
	}
	feedbackOptions.Mutator = selectedMutator
	feedbackOptions.Resume = resumeCheckpoint
	feedbackOptions.CheckpointEvery = *checkpointEvery
	if resumeCheckpoint == nil {
		if err := createOutputDirectory(*outputPath); err != nil {
			return err
		}
	} else if information, err := os.Stat(*outputPath); err != nil || !information.IsDir() {
		return fmt.Errorf("恢复目录 %s 不存在或不是目录", *outputPath)
	}
	settings := experimentSettings{
		LLMInit: *llmInit, LLMMutate: *llmMutate, Initializer: feedbackOptions.InitializerName,
		Mutator: selectedMutator.Name(), RandomMutation: mutationConfig,
		ArtifactPolicy: artifactPolicy, CheckpointEvery: *checkpointEvery,
	}
	if *llmInit || *llmMutate {
		settings.LLMProvider = effectiveLLMProvider
		settings.LLMModel = effectiveLLMModel
		settings.LLMBaseURL = effectiveLLMBaseURL
		settings.LLMAPIKeyEnv = effectiveAPIKeyEnv
		settings.LLMTimeoutMillis = llmTimeout.Milliseconds()
	}
	fingerprintSettings := settings
	fingerprintSettings.ArtifactPolicy = ""
	fingerprintSettings.CheckpointEvery = 0
	fingerprint, err := configurationFingerprint(config, experimentConfig, policyConfig, fingerprintSettings)
	if err != nil {
		return err
	}
	feedbackOptions.ConfigurationFingerprint = fingerprint
	if resumeCheckpoint != nil {
		if err := resumeCheckpoint.Validate(experimentConfig, fingerprint); err != nil {
			return fmt.Errorf("恢复点校验失败: %w", err)
		}
	}
	if resumeCheckpoint == nil {
		for _, artifact := range []struct {
			name  string
			value any
		}{
			{name: "config.json", value: config},
			{name: "policy-config.json", value: policyConfig},
			{name: "experiment-settings.json", value: settings},
		} {
			if err := writeJSONFile(filepath.Join(*outputPath, artifact.name), artifact.value); err != nil {
				return err
			}
		}
	}
	committedRunSummaries := 0
	committedCorpusEntries := 0
	if resumeCheckpoint != nil {
		committedRunSummaries = resumeCheckpoint.RunSummaryCount
		committedCorpusEntries = resumeCheckpoint.Corpus.EntryCount
	}
	store, err := openExperimentStore(*outputPath, artifactPolicy, committedRunSummaries, committedCorpusEntries)
	if err != nil {
		return err
	}
	store.config = config
	if llmClient != nil {
		store.llmStats = func() llm.Stats { return previousLLMStats.Add(llmClient.Stats()) }
	}
	if resumeCheckpoint != nil && store.lastEventSequence > resumeCheckpoint.EventSequence {
		resumeCheckpoint.EventSequence = store.lastEventSequence
	}
	feedbackOptions.Hooks = store.hooks()
	feedbackOptions.ResumeCorpusEntries = append([]corpus.Entry(nil), store.corpusEntries...)
	var tlcMetricsClient *tlc.Client
	tlcMetrics := tlcMetricsArtifact{Segments: make([]tlcMetricsSegment, 0, 1)}
	if resumeCheckpoint != nil {
		if metricsErr := persistence.ReadJSON(filepath.Join(*outputPath, "tlc-server-metrics.json"), &tlcMetrics); metricsErr != nil && !errors.Is(metricsErr, os.ErrNotExist) {
			_, _ = fmt.Fprintf(stderr, "警告: 读取已有 TLC 性能统计失败: %v\n", metricsErr)
			tlcMetrics = tlcMetricsArtifact{Segments: make([]tlcMetricsSegment, 0, 1)}
		}
	}
	tlcMetricsAvailable := false
	if config.TLC.Address != "" {
		client, clientErr := tlc.NewClient(config.TLC.Address)
		if clientErr == nil {
			metricsContext, cancelMetrics := context.WithTimeout(context.Background(), 5*time.Second)
			startMetrics, metricsErr := client.Metrics(metricsContext)
			cancelMetrics()
			if metricsErr == nil {
				tlcMetricsClient, tlcMetricsAvailable = client, true
				tlcMetrics.Segments = append(tlcMetrics.Segments, tlcMetricsSegment{
					StartedAt: time.Now().UTC(), Start: startMetrics,
				})
			} else {
				_, _ = fmt.Fprintf(stderr, "警告: TLC 服务未提供性能统计: %v\n", metricsErr)
			}
		}
	}
	if config.TLC.Address == "" {
		_, _ = fmt.Fprintln(stderr, "警告: 未连接 TLC，本次运行不会返回模型状态，Corpus 将保持为空，闭环会持续补充初始种子")
	}
	report, corpusCheckpoint, runErr := runner.RunFeedback(ctx, feedbackOptions,
		func(ctx context.Context, index int, seed int64, candidate experiment.Candidate) (experiment.FeedbackExecution, error) {
			runConfig := config
			runConfig.Seed = seed
			runConfig.ExecutionID = core.ExecutionID(fmt.Sprintf("%s-feedback-%04d", config.ExecutionID, index))
			runEngine, err := buildEngine(runConfig, stderr)
			if err != nil {
				return experiment.FeedbackExecution{}, err
			}
			var sequence plan.PlanSequence
			var result engine.Result
			var engineErr error
			if candidate.Plan == nil {
				policy, err := randompolicy.NewRandom(seed, policyConfig)
				if err != nil {
					return experiment.FeedbackExecution{}, err
				}
				result, engineErr = runEngine.RunSource(ctx, policy, *maxPlanActions)
				sequence = policy.Sequence()
			} else {
				sequence = candidate.Plan.Copy()
				result, engineErr = runEngine.Run(ctx, sequence)
			}
			execution := experiment.FeedbackExecution{Result: result, Plan: sequence}
			return execution, engineErr
		})
	storeErr := store.Close()
	rootArtifacts := []struct {
		name  string
		value any
	}{
		{name: "corpus.json", value: corpusCheckpoint},
		{name: "experiment-report.json", value: report},
		{name: "experiment-metrics.json", value: report.Statistics()},
	}
	if tlcMetricsAvailable {
		metricsContext, cancelMetrics := context.WithTimeout(context.Background(), 5*time.Second)
		endMetrics, metricsErr := tlcMetricsClient.Metrics(metricsContext)
		cancelMetrics()
		if metricsErr != nil {
			_, _ = fmt.Fprintf(stderr, "警告: 读取实验结束 TLC 性能统计失败: %v\n", metricsErr)
		} else {
			last := len(tlcMetrics.Segments) - 1
			tlcMetrics.Segments[last].EndedAt = time.Now().UTC()
			tlcMetrics.Segments[last].End = endMetrics
			rootArtifacts = append(rootArtifacts, struct {
				name  string
				value any
			}{name: "tlc-server-metrics.json", value: tlcMetrics})
		}
	}
	if store.llmStats != nil {
		rootArtifacts = append(rootArtifacts, struct {
			name  string
			value any
		}{name: "llm-stats.json", value: store.llmStats()})
	}
	for _, artifact := range rootArtifacts {
		if err := writeJSONFile(filepath.Join(*outputPath, artifact.name), artifact.value); err != nil {
			return errors.Join(runErr, storeErr, err)
		}
	}
	_, outputErr := fmt.Fprintf(stdout,
		"反馈实验结束: runs=%d succeeded=%d failed=%d corpus=%d mutations=%d periodic_seeds=%d actions=%d model_events=%d unique_model_states=%d unique_plans=%d unique_traces=%d unique_state_paths=%d output=%s\n",
		report.CompletedRuns, report.Succeeded, report.Failed, report.CorpusEntries,
		report.ExecutedMutations, report.PeriodicSeedExecutions, report.TotalActions, report.TotalModelEvents,
		report.UniqueModelStates, report.UniquePlans, report.UniqueTraces, report.UniqueModelStatePaths, *outputPath,
	)
	return errors.Join(runErr, storeErr, outputErr)
}

func replayCommand(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("modelfuzz-ng replay", flag.ContinueOnError)
	flags.SetOutput(stderr)
	tracePath := flags.String("trace", "", "待重放的 trace.json（必填）")
	outputPath := flags.String("output", "", "新的重放产物目录（必填，不覆盖）")
	configPath := flags.String("config", "", "配置文件；默认使用 Trace 同目录的 config.json")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("无法识别的位置参数: %v", flags.Args())
	}
	if *tracePath == "" || *outputPath == "" {
		flags.Usage()
		return fmt.Errorf("-trace 和 -output 均为必填项")
	}
	if *configPath == "" {
		*configPath = filepath.Join(filepath.Dir(*tracePath), "config.json")
	}
	config, err := loadCLIConfig(*configPath)
	if err != nil {
		return err
	}
	expected, err := tracepkg.Load(*tracePath)
	if err != nil {
		return err
	}
	runtime, err := buildRuntime(config, stderr)
	if err != nil {
		return err
	}
	replayer, err := tracepkg.NewReplayer(runtime)
	if err != nil {
		return err
	}
	if err := createOutputDirectory(*outputPath); err != nil {
		return err
	}
	result, replayErr := replayer.Replay(ctx, expected)
	writeErr := writeReplayArtifacts(*outputPath, config, expected, result)
	if writeErr != nil {
		return errors.Join(replayErr, writeErr)
	}
	_, outputErr := fmt.Fprintf(stdout, "重放结束: status=%s matched_steps=%d output=%s\n",
		result.Status, result.MatchedSteps, *outputPath)
	return errors.Join(replayErr, outputErr)
}

func runCommand(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("modelfuzz-ng run", flag.ContinueOnError)
	flags.SetOutput(stderr)
	planPath := flags.String("plan", "", "PlanSequence JSON 文件（必填）")
	outputPath := flags.String("output", "", "新的运行产物目录（必填，不覆盖）")
	configPath := flags.String("config", "", "可选的 JSON 配置文件")
	tlcAddress := flags.String("tlc", "", "覆盖配置中的 controlled TLC 地址；留空则只生成模型事件")
	executionID := flags.String("execution-id", "", "覆盖 execution_id")
	seedText := flags.String("seed", "", "覆盖确定性随机种子")
	strictPlan := flags.Bool("strict-plan", false, "partial、skipped 或 empty_queue 时终止")
	largestTerm := flags.Uint64("largest-term", 0, "覆盖模型 LargestTerm 边界")
	maxLogIndex := flags.Uint64("max-log-index", 0, "覆盖模型 MaxLogIndex 边界")
	snapshotThreshold := flags.Uint64("snapshot-threshold", 0, "每新增多少条已应用日志创建 snapshot；0表示关闭")
	snapshotRetainEntries := flags.Uint64("snapshot-retain-entries", 0, "snapshot 压缩后额外保留的日志条目数")
	voteQuorumDivisor := flags.Int("vote-quorum-divisor", 0, "选举 quorum 除数：2为正常，3复现 ModelFuzz mutant")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("无法识别的位置参数: %v", flags.Args())
	}
	if *planPath == "" || *outputPath == "" {
		flags.Usage()
		return fmt.Errorf("-plan 和 -output 均为必填项")
	}

	config, err := loadCLIConfig(*configPath)
	if err != nil {
		return err
	}
	if *executionID != "" {
		config.ExecutionID = core.ExecutionID(*executionID)
	}
	if *seedText != "" {
		seed, err := strconv.ParseInt(*seedText, 10, 64)
		if err != nil {
			return fmt.Errorf("解析 -seed %q: %w", *seedText, err)
		}
		config.Seed = seed
	}
	if *tlcAddress != "" {
		config.TLC.Address = *tlcAddress
	}
	if *largestTerm != 0 {
		config.Model.LargestTerm = *largestTerm
	} else if flagWasSet(flags, "largest-term") {
		return fmt.Errorf("-largest-term 必须为正数")
	}
	if *maxLogIndex != 0 {
		config.Model.MaxLogIndex = *maxLogIndex
	} else if flagWasSet(flags, "max-log-index") {
		return fmt.Errorf("-max-log-index 必须为正数")
	}
	if flagWasSet(flags, "snapshot-threshold") {
		config.Raft.Snapshot.Threshold = *snapshotThreshold
	}
	if flagWasSet(flags, "snapshot-retain-entries") {
		config.Raft.Snapshot.RetainEntries = *snapshotRetainEntries
	}
	if flagWasSet(flags, "vote-quorum-divisor") {
		config.Raft.Faults.VoteQuorumDivisor = *voteQuorumDivisor
	}
	if *strictPlan {
		config.Engine.FailOnPartial = true
		config.Engine.FailOnSkipped = true
		config.Engine.FailOnEmptyQueue = true
	}
	if err := validateAlignedNodes(config.Raft.NodeIDs, config.Model.NodeIDs); err != nil {
		return fmt.Errorf("raft/模型配置不一致: %w", err)
	}
	if err := validateTLCModelBounds(ctx, config, stderr); err != nil {
		return err
	}

	sequence, err := readPlan(*planPath)
	if err != nil {
		return err
	}
	runner, err := buildEngine(config, stderr)
	if err != nil {
		return err
	}
	if err := createOutputDirectory(*outputPath); err != nil {
		return err
	}

	result, runErr := runner.Run(ctx, sequence)
	writeErr := writeArtifacts(*outputPath, config, sequence, result)
	if writeErr != nil {
		return errors.Join(runErr, writeErr)
	}
	_, outputErr := fmt.Fprintf(stdout,
		"运行结束: status=%s actions=%d effects=%d model_events=%d model_states=%d oracle_findings=%d output=%s\n",
		result.Status, len(result.Actions.Actions), countEffects(result),
		len(result.ModelEvents), len(result.ModelStates), len(result.OracleFindings), *outputPath,
	)
	return errors.Join(runErr, outputErr)
}

func buildEngine(config cliConfig, logOutput io.Writer) (*engine.Engine, error) {
	runtime, err := buildRuntime(config, logOutput)
	if err != nil {
		return nil, err
	}
	resolver, err := plan.NewResolver(config.Resolver)
	if err != nil {
		return nil, fmt.Errorf("创建 Plan Resolver: %w", err)
	}
	mapper, err := raftmodel.NewMapperWithConfig(config.Model)
	if err != nil {
		return nil, fmt.Errorf("创建 Raft Mapper: %w", err)
	}

	var executor model.Executor
	if config.TLC.Address != "" {
		if config.TLC.TimeoutSeconds <= 0 {
			return nil, fmt.Errorf("TLC timeout_seconds 必须为正数")
		}
		client, err := tlc.NewClientWithHTTPClient(config.TLC.Address, &http.Client{
			Timeout: time.Duration(config.TLC.TimeoutSeconds) * time.Second,
		})
		if err != nil {
			return nil, fmt.Errorf("创建 TLC Client: %w", err)
		}
		executor = client
	}
	return engine.New(runtime, resolver, mapper, executor, config.Engine, raftoracle.New())
}

func validateTLCModelBounds(ctx context.Context, config cliConfig, warnings io.Writer) error {
	if config.TLC.Address == "" {
		return nil
	}
	client, err := tlc.NewClient(config.TLC.Address)
	if err != nil {
		return err
	}
	boundContext, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	bounds, err := client.Bounds(boundContext)
	if err != nil {
		_, _ = fmt.Fprintf(warnings, "警告: TLC 服务未暴露模型边界，无法自动核对 LargestTerm/MaxLogIndex: %v\n", err)
		return nil
	}
	if bounds.LargestTerm != config.Model.LargestTerm || bounds.MaxLogIndex != config.Model.MaxLogIndex {
		return fmt.Errorf(
			"TLC/Go 模型边界不一致: TLC LargestTerm=%d MaxLogIndex=%d，Go LargestTerm=%d MaxLogIndex=%d",
			bounds.LargestTerm, bounds.MaxLogIndex, config.Model.LargestTerm, config.Model.MaxLogIndex,
		)
	}
	return nil
}

func buildRuntime(config cliConfig, logOutput io.Writer) (*runtimepkg.Runtime, error) {
	adapterConfig := config.Raft.adapterConfig()
	raftLogOutput := io.Discard
	if config.Raft.VerboseLogging {
		raftLogOutput = logOutput
	}
	adapterConfig.Logger = &raft.DefaultLogger{Logger: log.New(raftLogOutput, "raft: ", log.LstdFlags)}
	adapter, err := etcdraft.New(adapterConfig)
	if err != nil {
		return nil, fmt.Errorf("创建 etcd-raft Adapter: %w", err)
	}
	runtime, err := runtimepkg.New(adapter, runtimepkg.Config{
		ExecutionID: config.ExecutionID,
		Seed:        config.Seed,
		Limits:      config.Runtime,
	})
	if err != nil {
		return nil, fmt.Errorf("创建 Runtime: %w", err)
	}
	return runtime, nil
}

func countEffects(result engine.Result) int {
	total := 0
	for _, step := range result.Trace.Steps {
		total += len(step.Effects)
	}
	return total
}

func printRootUsage(output io.Writer) {
	_, _ = fmt.Fprintln(output, "用法:")
	_, _ = fmt.Fprintln(output, "  modelfuzz-ng run -plan PLAN.json -output RUN_DIR [选项]")
	_, _ = fmt.Fprintln(output, "  modelfuzz-ng replay -trace TRACE.json -output RUN_DIR [选项]")
	_, _ = fmt.Fprintln(output, "  modelfuzz-ng experiment -output RUN_DIR [选项]")
	_, _ = fmt.Fprintln(output)
	_, _ = fmt.Fprintln(output, "使用 'modelfuzz-ng run -h' 查看 run 选项。")
}

func flagWasSet(flags *flag.FlagSet, name string) bool {
	set := false
	flags.Visit(func(value *flag.Flag) {
		if value.Name == name {
			set = true
		}
	})
	return set
}
