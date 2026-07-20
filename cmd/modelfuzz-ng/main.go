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
	"strconv"
	"syscall"
	"time"

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/adapters/etcdraft"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/core"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/engine"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/model"
	raftmodel "github.com/SuzumiyaHaruki/modelfuzz-ng/internal/model/raft"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/model/tlc"
	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/plan"
	runtimepkg "github.com/SuzumiyaHaruki/modelfuzz-ng/internal/runtime"
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
	case "help", "-h", "--help":
		printRootUsage(stdout)
		return flag.ErrHelp
	default:
		printRootUsage(stderr)
		return fmt.Errorf("未知子命令 %q", args[0])
	}
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
	if *strictPlan {
		config.Engine.FailOnPartial = true
		config.Engine.FailOnSkipped = true
		config.Engine.FailOnEmptyQueue = true
	}
	if err := validateAlignedNodes(config.Raft.NodeIDs, config.Model.NodeIDs); err != nil {
		return fmt.Errorf("Raft/模型配置不一致: %w", err)
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
	fmt.Fprintf(stdout,
		"运行结束: status=%s actions=%d effects=%d model_events=%d model_states=%d output=%s\n",
		result.Status, len(result.Actions.Actions), countEffects(result),
		len(result.ModelEvents), len(result.ModelStates), *outputPath,
	)
	return runErr
}

func buildEngine(config cliConfig, logOutput io.Writer) (*engine.Engine, error) {
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
	return engine.New(runtime, resolver, mapper, executor, config.Engine)
}

func countEffects(result engine.Result) int {
	total := 0
	for _, step := range result.Trace.Steps {
		total += len(step.Effects)
	}
	return total
}

func printRootUsage(output io.Writer) {
	fmt.Fprintln(output, "用法:")
	fmt.Fprintln(output, "  modelfuzz-ng run -plan PLAN.json -output RUN_DIR [选项]")
	fmt.Fprintln(output, "")
	fmt.Fprintln(output, "使用 'modelfuzz-ng run -h' 查看 run 选项。")
}
