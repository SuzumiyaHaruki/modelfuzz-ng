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
	"reflect"
	"sort"
	"strconv"
	"syscall"
	"time"

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/adapters/etcdraft"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/core"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/corpus"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/coverageanalysis"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/coverageguidance"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/engine"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/experiment"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/llm"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/minimize"
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

const releaseVersion = "v1.0.0"

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
	case "minimize":
		return minimizeCommand(ctx, args[1:], stdout, stderr)
	case "experiment":
		return experimentCommand(ctx, args[1:], stdout, stderr)
	case "coverage-compare":
		return coverageCompareCommand(args[1:], stdout, stderr)
	case "coverage-factorize":
		return coverageFactorizeCommand(args[1:], stdout, stderr)
	case "coverage-benchmark":
		return coverageBenchmarkCommand(ctx, args[1:], stdout, stderr)
	case "coverage-summarize":
		return coverageSummarizeCommand(args[1:], stdout, stderr)
	case "breadth-depth-benchmark":
		return breadthDepthBenchmarkCommand(ctx, args[1:], stdout, stderr)
	case "handoff-probe-benchmark":
		return handoffProbeBenchmarkCommand(ctx, args[1:], stdout, stderr)
	case "handoff-diagnose":
		return handoffDiagnoseCommand(ctx, args[1:], stdout, stderr)
	case "goal-search":
		return goalSearchCommand(ctx, args[1:], stdout, stderr)
	case "goal-compare":
		return goalCompareCommand(args[1:], stdout, stderr)
	case "goal-benchmark":
		return goalBenchmarkCommand(ctx, args[1:], stdout, stderr)
	case "c2-differential-analysis":
		return c2DifferentialAnalysisCommand(args[1:], stdout, stderr)
	case "version":
		if len(args) != 1 {
			return fmt.Errorf("version 子命令不接受参数")
		}
		_, err := fmt.Fprintf(stdout, "modelfuzz-ng %s\n", releaseVersion)
		return err
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
	minNewModelStates := flags.Int("min-new-model-states", 25, "进入 Corpus 至少需要的全局新 TLC 原始状态数")
	semanticCoverage := flags.Bool("semantic-coverage", true, "要求 Corpus 候选增加归一化语义状态或语义转移覆盖")
	lifecycleCooldown := flags.Int("lifecycle-cooldown", 48, "两次 crash 周期之间至少间隔的 Action 数")
	maxCrashEpisodes := flags.Int("max-crash-episodes", 4, "单条轨迹允许的最大 crash/restart 周期数")
	crashRestartPairPercent := flags.Int("crash-restart-pair-percent", 5, "随机变异插入 crash/restart 对的百分比")
	partitionHealPairPercent := flags.Int("partition-heal-pair-percent", 5, "随机变异插入 partition/heal 对的百分比")
	crashWeight := flags.Int("crash-weight", 1, "在线 balanced 随机策略的 crash 权重")
	restartWeight := flags.Int("restart-weight", 10, "在线 balanced 随机策略的 restart 权重")
	partitionWeight := flags.Int("partition-weight", 2, "在线 balanced 随机策略的 partition 权重")
	healWeight := flags.Int("heal-weight", 8, "在线 balanced 随机策略的 heal 权重")
	randomSeedInterval := flags.Int("random-seed-interval", 0, "每完成多少次执行优先注入在线随机种子；0 表示关闭")
	randomSeedsPerInterval := flags.Int("random-seeds-per-interval", 1, "每次周期注入的在线随机种子数")
	initialPolicy := flags.String("initial-policy", "random", "在线种子策略: random、snapshot-partition、snapshot-failure 或 snapshot-fast-forward")
	llmProvider := flags.String("llm-provider", string(llm.DefaultProvider), "LLM provider: deepseek、glm、qwen 或 kimi")
	llmModel := flags.String("llm-model", "", "覆盖 provider 的默认模型")
	llmBaseURL := flags.String("llm-base-url", "", "覆盖 provider 的 OpenAI-compatible API 基础地址")
	llmAPIKeyEnv := flags.String("llm-api-key-env", "", "覆盖保存 API Key 的环境变量名称")
	llmTimeout := flags.Duration("llm-timeout", 90*time.Second, "单次 LLM 请求超时")
	coverageGuidanceModeText := flags.String(
		"coverage-guidance-mode", string(coverageguidance.ModeLegacyRaw),
		"显式覆盖引导模式: random、raw-fixed、v2-fixed、facet-fixed、facet-interaction-fixed、legacy-raw",
	)
	coverageEnergyMode := flags.String("coverage-energy-mode", "legacy", "覆盖引导能量模式: fixed 或 legacy")
	fixedEnergy := flags.Int("fixed-energy", 2, "G0-G4 每个准入父 Plan 的固定变异数")
	fixedParentSelection := flags.String(
		"fixed-parent-selection", "admission-fifo-once",
		"G0-G4 的固定父 Plan 调度；本版本仅支持 admission-fifo-once",
	)
	coverageCorpusLimit := flags.Int("coverage-corpus-limit", 4096, "G0-G4 Corpus 条目上限")
	recordAllCoverage := flags.Bool("record-all-coverage-metrics", true, "为每个候选记录 Raw/v2/Facet/Interaction")
	offlineGoalEvaluation := flags.Bool("offline-goal-evaluation", false, "实验后允许离线计算冻结 Goal；不参与在线决策")
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
		settingsPath := filepath.Join(resumeDirectory, "experiment-settings.json")
		settingsData, err := os.ReadFile(settingsPath)
		if err != nil {
			return fmt.Errorf("读取原实验设置: %w", err)
		}
		if err := decodeStrictJSON(settingsData, &savedSettings); err != nil {
			return fmt.Errorf("读取原实验设置: %w", err)
		}
		if savedSettings.ReleaseVersion != releaseVersion {
			return fmt.Errorf(
				"不支持 release version %q；当前版本要求 %q",
				savedSettings.ReleaseVersion, releaseVersion,
			)
		}
		if savedSettings.SemanticSchema != raftmodel.SemanticSchemaVersion {
			return fmt.Errorf(
				"不支持 semantic schema %q；当前版本要求 %q",
				savedSettings.SemanticSchema, raftmodel.SemanticSchemaVersion,
			)
		}
		if savedSettings.OnlinePolicy == "" {
			return fmt.Errorf("原实验设置缺少 online_policy")
		}
		savedGuidanceMode := savedSettings.CoverageGuidanceMode
		if savedGuidanceMode == "" {
			savedGuidanceMode = coverageguidance.ModeLegacyRaw
		}
		resumeGuidanceValues := map[string][2]string{
			"coverage-guidance-mode": {*coverageGuidanceModeText, string(savedGuidanceMode)},
			"coverage-energy-mode":   {*coverageEnergyMode, savedSettings.CoverageEnergyMode},
			"fixed-parent-selection": {*fixedParentSelection, savedSettings.FixedParentSelection},
		}
		for name, values := range resumeGuidanceValues {
			if setFlags[name] && values[0] != values[1] {
				return fmt.Errorf("恢复时 -%s=%s 与原实验中的 %s 不一致", name, values[0], values[1])
			}
		}
		if setFlags["fixed-energy"] && *fixedEnergy != savedSettings.FixedEnergy {
			return fmt.Errorf("恢复时 -fixed-energy=%d 与原实验中的 %d 不一致", *fixedEnergy, savedSettings.FixedEnergy)
		}
		if setFlags["coverage-corpus-limit"] && *coverageCorpusLimit != savedSettings.CoverageCorpusLimit {
			return fmt.Errorf("恢复时 -coverage-corpus-limit=%d 与原实验中的 %d 不一致", *coverageCorpusLimit, savedSettings.CoverageCorpusLimit)
		}
		if setFlags["record-all-coverage-metrics"] && *recordAllCoverage != savedSettings.RecordAllCoverage {
			return fmt.Errorf("恢复时 -record-all-coverage-metrics 与原实验不一致")
		}
		if setFlags["offline-goal-evaluation"] && *offlineGoalEvaluation != savedSettings.OfflineGoalEvaluation {
			return fmt.Errorf("恢复时 -offline-goal-evaluation 与原实验不一致")
		}
		*coverageGuidanceModeText = string(savedGuidanceMode)
		*coverageEnergyMode = savedSettings.CoverageEnergyMode
		*fixedParentSelection = savedSettings.FixedParentSelection
		*fixedEnergy = savedSettings.FixedEnergy
		*coverageCorpusLimit = savedSettings.CoverageCorpusLimit
		*recordAllCoverage = savedSettings.RecordAllCoverage
		*offlineGoalEvaluation = savedSettings.OfflineGoalEvaluation
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
		savedOnlinePolicy := savedSettings.OnlinePolicy
		if setFlags["initial-policy"] && *initialPolicy != savedOnlinePolicy {
			return fmt.Errorf("恢复时 -initial-policy=%s 与原实验中的 %s 不一致", *initialPolicy, savedOnlinePolicy)
		}
		*initialPolicy = savedOnlinePolicy
		resumeValues := map[string][2]int{
			"runs": {*runs, resumeCheckpoint.Config.Runs}, "parallelism": {*parallelism, resumeCheckpoint.Config.Parallelism},
			"initial-population":          {*initialPopulation, resumeCheckpoint.Config.InitialPopulation},
			"mutations-per-state":         {*mutationsPerState, resumeCheckpoint.Config.MutationsPerNewState},
			"max-mutations-per-corpus":    {*maxMutationsPerCorpus, resumeCheckpoint.Config.MaxMutationsPerCorpus},
			"max-ready-candidates":        {*maxReadyCandidates, resumeCheckpoint.Config.MaxReadyCandidates},
			"min-new-model-states":        {*minNewModelStates, resumeCheckpoint.Config.MinNewModelStates},
			"lifecycle-cooldown":          {*lifecycleCooldown, resumeCheckpoint.Config.LifecycleCooldown},
			"max-crash-episodes":          {*maxCrashEpisodes, resumeCheckpoint.Config.MaxCrashEpisodes},
			"crash-restart-pair-percent":  {*crashRestartPairPercent, resumeCheckpoint.Config.CrashRestartPairPercent},
			"crash-weight":                {*crashWeight, resumeCheckpoint.Config.CrashWeight},
			"restart-weight":              {*restartWeight, resumeCheckpoint.Config.RestartWeight},
			"partition-heal-pair-percent": {*partitionHealPairPercent, resumeCheckpoint.Config.PartitionHealPairPercent},
			"partition-weight":            {*partitionWeight, resumeCheckpoint.Config.PartitionWeight},
			"heal-weight":                 {*healWeight, resumeCheckpoint.Config.HealWeight},
			"random-seed-interval":        {*randomSeedInterval, resumeCheckpoint.Config.RandomSeedInterval},
			"random-seeds-per-interval":   {*randomSeedsPerInterval, resumeCheckpoint.Config.RandomSeedsPerInterval},
		}
		for name, values := range resumeValues {
			if setFlags[name] && values[0] != values[1] {
				return fmt.Errorf("恢复时 -%s=%d 与 checkpoint 中的 %d 不一致", name, values[0], values[1])
			}
		}
		if setFlags["semantic-coverage"] && *semanticCoverage != resumeCheckpoint.Config.SemanticCoverage {
			return fmt.Errorf("恢复时 -semantic-coverage=%v 与 checkpoint 中的 %v 不一致", *semanticCoverage, resumeCheckpoint.Config.SemanticCoverage)
		}
		*runs = resumeCheckpoint.Config.Runs
		*parallelism = resumeCheckpoint.Config.Parallelism
		*initialPopulation = resumeCheckpoint.Config.InitialPopulation
		*mutationsPerState = resumeCheckpoint.Config.MutationsPerNewState
		*maxMutationsPerCorpus = resumeCheckpoint.Config.MaxMutationsPerCorpus
		*maxReadyCandidates = resumeCheckpoint.Config.MaxReadyCandidates
		*minNewModelStates = resumeCheckpoint.Config.MinNewModelStates
		*lifecycleCooldown = resumeCheckpoint.Config.LifecycleCooldown
		*maxCrashEpisodes = resumeCheckpoint.Config.MaxCrashEpisodes
		*crashRestartPairPercent = resumeCheckpoint.Config.CrashRestartPairPercent
		*crashWeight = resumeCheckpoint.Config.CrashWeight
		*restartWeight = resumeCheckpoint.Config.RestartWeight
		*partitionHealPairPercent = resumeCheckpoint.Config.PartitionHealPairPercent
		*partitionWeight = resumeCheckpoint.Config.PartitionWeight
		*healWeight = resumeCheckpoint.Config.HealWeight
		*semanticCoverage = resumeCheckpoint.Config.SemanticCoverage
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
	if *initialPolicy != "random" && *initialPolicy != "snapshot-partition" &&
		*initialPolicy != "snapshot-failure" && *initialPolicy != "snapshot-fast-forward" {
		return fmt.Errorf(
			"未知 -initial-policy=%q；可选 random、snapshot-partition、snapshot-failure、snapshot-fast-forward",
			*initialPolicy,
		)
	}
	if *initialPolicy != "random" && config.Raft.Snapshot.Threshold == 0 {
		return fmt.Errorf("-initial-policy=%s 要求启用 snapshot-threshold", *initialPolicy)
	}
	guidanceMode, err := coverageguidance.ParseMode(*coverageGuidanceModeText)
	if err != nil {
		return err
	}
	if guidanceMode == coverageguidance.ModeLegacyRaw {
		if *coverageEnergyMode != "legacy" {
			return fmt.Errorf("legacy-raw 必须使用 -coverage-energy-mode=legacy")
		}
	} else {
		if *coverageEnergyMode != "fixed" {
			return fmt.Errorf("%s 必须显式使用 -coverage-energy-mode=fixed，不能继承 legacy energy", guidanceMode)
		}
		if *fixedEnergy <= 0 {
			return fmt.Errorf("-fixed-energy 必须为正数")
		}
		if *coverageCorpusLimit <= 0 {
			return fmt.Errorf("-coverage-corpus-limit 必须为正数")
		}
		if *fixedParentSelection != "admission-fifo-once" {
			return fmt.Errorf("未知 -fixed-parent-selection=%q；当前仅支持 admission-fifo-once", *fixedParentSelection)
		}
		if *llmInit || *llmMutate {
			return fmt.Errorf("本轮 G0-G4 广度实验禁止 LLM；请关闭 -llm-init 和 -llm-mutate")
		}
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
		MinNewModelStates: *minNewModelStates, SemanticCoverage: *semanticCoverage,
		LifecycleCooldown: *lifecycleCooldown, MaxCrashEpisodes: *maxCrashEpisodes,
		CrashRestartPairPercent: *crashRestartPairPercent, CrashWeight: *crashWeight, RestartWeight: *restartWeight,
		PartitionHealPairPercent: *partitionHealPairPercent, PartitionWeight: *partitionWeight, HealWeight: *healWeight,
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
	policyConfig.LifecycleCooldown = experimentConfig.LifecycleCooldown
	policyConfig.MaxCrashEpisodes = experimentConfig.MaxCrashEpisodes
	policyConfig.Weights.Crash = experimentConfig.CrashWeight
	policyConfig.Weights.Restart = experimentConfig.RestartWeight
	policyConfig.Weights.Partition = experimentConfig.PartitionWeight
	policyConfig.Weights.Heal = experimentConfig.HealWeight
	mutationConfig := mutation.RandomConfig{
		NodeIDs: append([]core.NodeID(nil), config.Raft.NodeIDs...), MaxValue: config.Model.MaxValue,
		MaxTicks: 5, MaxActions: *maxPlanActions, MaxCrashed: policyConfig.MaxCrashed,
		LifecycleCooldown: experimentConfig.LifecycleCooldown, MaxCrashEpisodes: experimentConfig.MaxCrashEpisodes,
		CrashRestartPairPercent:  experimentConfig.CrashRestartPairPercent,
		PartitionHealPairPercent: experimentConfig.PartitionHealPairPercent,
	}
	localMutator, err := mutation.NewRandom(mutationConfig)
	if err != nil {
		return err
	}
	var selectedMutator mutation.Mutator = localMutator
	var llmClient *llm.Client
	var effectiveLLMProvider llm.Provider
	var effectiveLLMModel, effectiveLLMBaseURL, effectiveAPIKeyEnv string
	feedbackOptions := experiment.FeedbackOptions{InitializerName: *initialPolicy + "_init"}
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
	feedbackOptions.CoverageProjector = func(states []model.State, events []model.Event) (corpus.Projection, error) {
		projection, projectionErr := raftmodel.ProjectCoverage(states, events)
		return corpus.Projection{StateKeys: projection.StateKeys, TransitionKeys: projection.TransitionKeys}, projectionErr
	}
	var guidanceController *coverageguidance.Controller
	if guidanceMode != coverageguidance.ModeLegacyRaw {
		guidanceController, err = coverageguidance.New(coverageguidance.Config{
			Mode: guidanceMode, FixedEnergy: *fixedEnergy, CorpusLimit: *coverageCorpusLimit,
		})
		if err != nil {
			return err
		}
		feedbackOptions.Guidance = guidanceController
		feedbackOptions.ObservationBuilder = func(
			index int, _ int64, candidate experiment.Candidate, execution experiment.FeedbackExecution,
		) (coverageguidance.CoverageObservation, error) {
			return coverageanalysis.BuildCoverageObservation(coverageanalysis.ObservationInput{
				RunID:       fmt.Sprintf("%s-feedback-%04d", config.ExecutionID, index),
				CandidateID: candidate.ID, ParentPlanKey: candidate.ParentPlanKey,
				Source: candidate.Source, Plan: execution.Plan, Result: execution.Result,
				ModelConfig: config.Model,
			})
		}
	}
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
		ReleaseVersion: releaseVersion, SemanticSchema: raftmodel.SemanticSchemaVersion,
		LLMInit: *llmInit, LLMMutate: *llmMutate, Initializer: feedbackOptions.InitializerName,
		OnlinePolicy: *initialPolicy, Mutator: selectedMutator.Name(), RandomMutation: mutationConfig,
		ArtifactPolicy: artifactPolicy, CheckpointEvery: *checkpointEvery,
		CoverageGuidanceMode: guidanceMode, CoverageEnergyMode: *coverageEnergyMode,
		FixedEnergy: *fixedEnergy, FixedParentSelection: *fixedParentSelection,
		CoverageCorpusLimit: *coverageCorpusLimit, RecordAllCoverage: *recordAllCoverage,
		OfflineGoalEvaluation: *offlineGoalEvaluation,
	}
	if guidanceMode != coverageguidance.ModeLegacyRaw {
		settings.CoverageGuidanceSchema = coverageguidance.SchemaVersion
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
		initialArtifacts := []struct {
			name  string
			value any
		}{
			{name: "config.json", value: config},
			{name: "policy-config.json", value: policyConfig},
			{name: "experiment-settings.json", value: settings},
		}
		if guidanceController != nil {
			initialArtifacts = append(initialArtifacts, struct {
				name  string
				value any
			}{name: "coverage-guidance-settings.json", value: settings})
		}
		for _, artifact := range initialArtifacts {
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
	if guidanceController != nil {
		if err := store.enableCoverageGuidance(committedRunSummaries); err != nil {
			_ = store.Close()
			return err
		}
		if committedRunSummaries > 0 {
			observations, readErr := persistence.ReadJSONLines[coverageguidance.CoverageObservation](
				filepath.Join(*outputPath, "coverage-observations.jsonl"), committedRunSummaries)
			if readErr != nil {
				_ = store.Close()
				return fmt.Errorf("读取 coverage observations: %w", readErr)
			}
			savedDecisions, readErr := persistence.ReadJSONLines[coverageguidance.Decision](
				filepath.Join(*outputPath, "corpus-decisions.jsonl"), committedRunSummaries)
			if readErr != nil {
				_ = store.Close()
				return fmt.Errorf("读取 corpus decisions: %w", readErr)
			}
			for index, observation := range observations {
				recomputed, recomputeErr := guidanceController.Observe(observation)
				if recomputeErr != nil {
					_ = store.Close()
					return fmt.Errorf("恢复时重算 guidance decision %d: %w", index, recomputeErr)
				}
				if !reflect.DeepEqual(recomputed, savedDecisions[index]) {
					_ = store.Close()
					return fmt.Errorf("恢复时 guidance decision %d 与原始 artifact 不一致", index)
				}
			}
			if guidanceController.Snapshot().Config.CorpusLimit < committedCorpusEntries {
				_ = store.Close()
				return fmt.Errorf("恢复时 guidance Corpus 超过配置上限")
			}
		}
	}
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
				policy, err := buildOnlinePolicy(*initialPolicy, seed, policyConfig,
					config.Raft.Snapshot.Threshold, config.Raft.Snapshot.RetainEntries)
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
	var guidanceArtifactErr error
	if guidanceController != nil {
		_, _, guidanceArtifactErr = writeCoverageGuidanceArtifacts(
			*outputPath, guidanceMode, report.CompletedRuns, report.ElapsedMillis,
			*offlineGoalEvaluation, nil,
		)
	}
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
			return errors.Join(runErr, storeErr, guidanceArtifactErr, err)
		}
	}
	_, outputErr := fmt.Fprintf(stdout,
		"反馈实验结束: runs=%d succeeded=%d failed=%d corpus=%d mutations=%d periodic_seeds=%d actions=%d model_events=%d unique_model_states=%d unique_plans=%d unique_traces=%d unique_state_paths=%d output=%s\n",
		report.CompletedRuns, report.Succeeded, report.Failed, report.CorpusEntries,
		report.ExecutedMutations, report.PeriodicSeedExecutions, report.TotalActions, report.TotalModelEvents,
		report.UniqueModelStates, report.UniquePlans, report.UniqueTraces, report.UniqueModelStatePaths, *outputPath,
	)
	return errors.Join(runErr, storeErr, guidanceArtifactErr, outputErr)
}

type sequencedActionSource interface {
	engine.ActionSource
	Sequence() plan.PlanSequence
}

func buildOnlinePolicy(name string, seed int64, randomConfig randompolicy.RandomConfig, snapshotThreshold, snapshotRetainEntries uint64) (sequencedActionSource, error) {
	switch name {
	case "random":
		return randompolicy.NewRandom(seed, randomConfig)
	case "snapshot-partition":
		return randompolicy.NewSnapshotPartition(seed, randompolicy.SnapshotPartitionConfig{
			NodeIDs: append([]core.NodeID(nil), randomConfig.NodeIDs...), MaxValue: randomConfig.MaxValue,
			MaxLogIndex: randomConfig.MaxLogIndex, SnapshotThreshold: snapshotThreshold,
			RetainEntries: snapshotRetainEntries, DuplicateSnapshot: true,
		})
	case "snapshot-failure":
		return randompolicy.NewSnapshotPartition(seed, randompolicy.SnapshotPartitionConfig{
			NodeIDs: append([]core.NodeID(nil), randomConfig.NodeIDs...), MaxValue: randomConfig.MaxValue,
			MaxLogIndex: randomConfig.MaxLogIndex, SnapshotThreshold: snapshotThreshold,
			RetainEntries: snapshotRetainEntries, FailFirstSnapshot: true,
		})
	case "snapshot-fast-forward":
		return randompolicy.NewSnapshotFastForward(seed, randompolicy.SnapshotFastForwardConfig{
			NodeIDs: append([]core.NodeID(nil), randomConfig.NodeIDs...), MaxValue: randomConfig.MaxValue,
			MaxLogIndex: randomConfig.MaxLogIndex, SnapshotThreshold: snapshotThreshold,
			RetainEntries: snapshotRetainEntries,
		})
	default:
		return nil, fmt.Errorf("unknown online policy %q", name)
	}
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

func minimizeCommand(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("modelfuzz-ng minimize", flag.ContinueOnError)
	flags.SetOutput(stderr)
	planPath := flags.String("plan", "", "产生失败的 PlanSequence JSON 文件（必填）")
	outputPath := flags.String("output", "", "新的最短反例产物目录（必填，不覆盖）")
	resumePath := flags.String("resume", "", "从已有最小化产物目录继续")
	configPath := flags.String("config", "", "配置文件；默认优先使用 Plan 同目录的 config.json")
	tlcAddress := flags.String("tlc", "", "覆盖配置中的 controlled TLC 地址")
	maxAttempts := flags.Int("max-attempts", minimize.DefaultConfig().MaxAttempts, "最多执行的缩减尝试数（包含稳定性验证）")
	verifyRuns := flags.Int("verify-runs", minimize.DefaultConfig().VerifyRuns, "缩减前重复验证原始 failure 签名的次数")
	finalVerifyRuns := flags.Int("final-verify-runs", minimize.DefaultConfig().FinalVerifyRuns, "最终候选独立重复验证次数")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("无法识别的位置参数: %v", flags.Args())
	}
	setFlags := make(map[string]bool)
	flags.Visit(func(value *flag.Flag) { setFlags[value.Name] = true })
	if *resumePath == "" && (*planPath == "" || *outputPath == "") {
		flags.Usage()
		return fmt.Errorf("新缩减需要 -plan 和 -output，恢复缩减需要 -resume")
	}
	var resumeCheckpoint *minimize.Checkpoint
	if *resumePath != "" {
		if *planPath != "" || *outputPath != "" || *configPath != "" || setFlags["tlc"] {
			return fmt.Errorf("-resume 不能与 -plan、-output、-config 或 -tlc 同时使用")
		}
		*outputPath = filepath.Clean(*resumePath)
		*planPath = filepath.Join(*outputPath, "original-plan.json")
		*configPath = filepath.Join(*outputPath, "config.json")
		resumeCheckpoint = &minimize.Checkpoint{}
		if err := persistence.ReadJSON(filepath.Join(*outputPath, "minimization-checkpoint.json"), resumeCheckpoint); err != nil {
			return fmt.Errorf("读取最小化恢复点: %w", err)
		}
		if resumeCheckpoint.Complete {
			return fmt.Errorf("最小化恢复点已经完成")
		}
		if !setFlags["verify-runs"] {
			*verifyRuns = resumeCheckpoint.VerifyRuns
		}
		if !setFlags["final-verify-runs"] {
			*finalVerifyRuns = resumeCheckpoint.FinalVerifyRuns
		}
	} else if *configPath == "" {
		adjacent := filepath.Join(filepath.Dir(*planPath), "config.json")
		if information, err := os.Stat(adjacent); err == nil && !information.IsDir() {
			*configPath = adjacent
		}
	}
	if *configPath == "" {
		return fmt.Errorf("minimize 要求显式 -config，或 Plan 同目录必须存在 config.json")
	}
	config, err := loadCLIConfig(*configPath)
	if err != nil {
		return err
	}
	if flagWasSet(flags, "tlc") {
		config.TLC.Address = *tlcAddress
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
	inputPlanSHA256, err := fileSHA256(*planPath)
	if err != nil {
		return err
	}
	configSHA256, err := fileSHA256(*configPath)
	if err != nil {
		return err
	}
	if resumeCheckpoint != nil {
		if inputPlanSHA256 != resumeCheckpoint.InputPlanSHA256 || configSHA256 != resumeCheckpoint.ConfigSHA256 {
			return fmt.Errorf("最小化恢复点的 Plan 或配置摘要不匹配")
		}
	} else {
		if err := createOutputDirectory(*outputPath); err != nil {
			return err
		}
		if err := writeJSONFile(filepath.Join(*outputPath, "config.json"), config); err != nil {
			return err
		}
		if err := writeJSONFile(filepath.Join(*outputPath, "original-plan.json"), sequence); err != nil {
			return err
		}
		inputPlanSHA256, err = fileSHA256(filepath.Join(*outputPath, "original-plan.json"))
		if err != nil {
			return err
		}
		configSHA256, err = fileSHA256(filepath.Join(*outputPath, "config.json"))
		if err != nil {
			return err
		}
	}
	var lastCheckpoint minimize.Checkpoint
	reduced, err := minimize.Reduce(ctx, sequence, minimize.Config{
		MaxAttempts: *maxAttempts, VerifyRuns: *verifyRuns, FinalVerifyRuns: *finalVerifyRuns,
		Resume: resumeCheckpoint, InputPlanSHA256: inputPlanSHA256, ConfigSHA256: configSHA256,
		OnCheckpoint: func(checkpoint minimize.Checkpoint) error {
			lastCheckpoint = checkpoint
			// Do not expose a completed checkpoint until all final artifacts below
			// have been written successfully.
			checkpoint.Complete = false
			return persistence.WriteJSONAtomic(filepath.Join(*outputPath, "minimization-checkpoint.json"), checkpoint)
		},
	}, func(trialContext context.Context, candidate plan.PlanSequence) (engine.Result, error) {
		trialEngine, buildErr := buildEngine(config, stderr)
		if buildErr != nil {
			return engine.Result{}, buildErr
		}
		return trialEngine.Run(trialContext, candidate)
	})
	if err != nil {
		return err
	}
	if err := writeArtifacts(*outputPath, config, reduced.Plan, reduced.MinimizedExecution); err != nil {
		return err
	}
	for _, artifact := range []struct {
		name  string
		value any
	}{
		{name: "original-plan.json", value: sequence},
		{name: "minimized-plan.json", value: reduced.Plan},
		{name: "baseline-result.json", value: reduced.BaselineExecution},
		{name: "minimization-report.json", value: reduced.Report},
	} {
		if err := writeJSONFile(filepath.Join(*outputPath, artifact.name), artifact.value); err != nil {
			return err
		}
	}
	lastCheckpoint.Complete = true
	if err := writeJSONFile(filepath.Join(*outputPath, "minimization-checkpoint.json"), lastCheckpoint); err != nil {
		return err
	}
	_, outputErr := fmt.Fprintf(stdout,
		"缩减结束: status=%s actions=%d->%d attempts=%d one_minimal=%v output=%s\n",
		reduced.Report.Signature.Status, reduced.Report.OriginalActions, reduced.Report.MinimizedActions,
		reduced.Report.Attempts, reduced.Report.OneMinimal, *outputPath,
	)
	return outputErr
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
	wantProfile := config.Model.EffectiveProfile()
	if bounds.ModelProfile == "" {
		_, _ = fmt.Fprintln(warnings, "警告: TLC 服务未暴露 model_profile，无法自动核对 basic/storage-snapshot 模型")
	} else if bounds.ModelProfile != wantProfile {
		return fmt.Errorf("TLC/Go 模型 profile 不一致: TLC=%s，Go=%s", bounds.ModelProfile, wantProfile)
	}
	if len(bounds.ServerIDs) == 0 || bounds.MaxValue == nil {
		_, _ = fmt.Fprintln(warnings, "警告: TLC 服务未暴露 Server/MaxValue，无法自动核对节点集合和取值边界")
		return nil
	}
	wantNodes := make([]uint64, len(config.Model.NodeIDs))
	for index, node := range config.Model.NodeIDs {
		wantNodes[index] = uint64(node)
	}
	sort.Slice(wantNodes, func(i, j int) bool { return wantNodes[i] < wantNodes[j] })
	gotNodes := append([]uint64(nil), bounds.ServerIDs...)
	sort.Slice(gotNodes, func(i, j int) bool { return gotNodes[i] < gotNodes[j] })
	if !reflect.DeepEqual(gotNodes, wantNodes) || *bounds.MaxValue != uint64(config.Model.MaxValue) {
		return fmt.Errorf("TLC/Go 模型边界不一致: TLC Server=%v MaxValue=%d，Go Server=%v MaxValue=%d",
			gotNodes, *bounds.MaxValue, wantNodes, config.Model.MaxValue)
	}
	if bounds.NilValue != nil && *bounds.NilValue != 0 {
		return fmt.Errorf("TLC/Go 模型边界不一致: TLC Nil=%d，Go Nil=0", *bounds.NilValue)
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
	_, _ = fmt.Fprintln(output, "  modelfuzz-ng minimize -plan PLAN.json -output RUN_DIR [选项]")
	_, _ = fmt.Fprintln(output, "  modelfuzz-ng experiment -output RUN_DIR [选项]")
	_, _ = fmt.Fprintln(output, "  modelfuzz-ng coverage-compare -input EXPERIMENT_DIR [-output REPORT.json]")
	_, _ = fmt.Fprintln(output, "  modelfuzz-ng coverage-factorize -input EXPERIMENT_DIR [-output REPORT.json]")
	_, _ = fmt.Fprintln(output, "  modelfuzz-ng coverage-benchmark -manifest MANIFEST.json -output OUTPUT_DIR")
	_, _ = fmt.Fprintln(output, "  modelfuzz-ng coverage-summarize -input CAMPAIGN_DIR")
	_, _ = fmt.Fprintln(output, "  modelfuzz-ng breadth-depth-benchmark -manifest MANIFEST.json -output OUTPUT_DIR")
	_, _ = fmt.Fprintln(output, "  modelfuzz-ng handoff-probe-benchmark -manifest MANIFEST.json -output OUTPUT_DIR")
	_, _ = fmt.Fprintln(output, "  modelfuzz-ng handoff-diagnose -source BENCHMARK_DIR -output OUTPUT_DIR [选项]")
	_, _ = fmt.Fprintln(output, "  modelfuzz-ng goal-search -goal GOAL_ID -mode MODE -output DIR [选项]")
	_, _ = fmt.Fprintln(output, "  modelfuzz-ng goal-compare -input GOAL_RUN_ROOT -output SUMMARY.json")
	_, _ = fmt.Fprintln(output, "  modelfuzz-ng goal-benchmark -manifest MANIFEST.json -output ROOT")
	_, _ = fmt.Fprintln(output, "  modelfuzz-ng version")
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
