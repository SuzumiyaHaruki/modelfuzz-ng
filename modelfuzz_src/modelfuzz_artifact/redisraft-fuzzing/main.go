package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path"

	"github.com/spf13/cobra"
)

func MainCommand() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.AddCommand(CompareCommand())
	cmd.AddCommand(MeasureCommand())
	return cmd
}

func MeasureCommand() *cobra.Command {
	var tracesPath string
	var tlcAddr string
	var outPath string
	cmd := &cobra.Command{
		Use: "measure",
		Run: func(cmd *cobra.Command, args []string) {
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

func CompareCommand() *cobra.Command {
	var episodes int
	var horizon int
	var savePath string
	var workPath string
	var nodes int
	var requests int
	var workers int
	var logLevel string
	var runs int

	cmd := &cobra.Command{
		Use: "compare",
		Run: func(cmd *cobra.Command, args []string) {
			fConfig := FuzzerConfig{
				Iterations:      episodes,
				Horizon:         horizon,
				NumWorkers:      workers,
				NumNodes:        nodes,
				RecordPath:      savePath,
				BaseNetworkPort: 7074,
				BaseWorkingDir:  workPath,
				LogLevel:        logLevel,

				MutationsPerTrace: 3,
				SeedPopulation:    20,
				NumRequests:       requests,
				NumCrashes:        10,
				MaxMessages:       5,
				ReseedFrequency:   200,

				ClusterConfig: &ClusterConfig{
					NumNodes:              nodes,
					RaftModulePath:        "/Fuzzing/redis-instrumented/redisraft/redisraft.so",
					RedisServerBinaryPath: "/Fuzzing/redis-instrumented/bin/redis-server",
				},
			}
			baseObjectPath := "/Fuzzing/redis-instrumented/redisraft/build/deps/raft/CMakeFiles/raft.dir/src"
			gCovPath := "/usr/bin/gcov"

			mutator := CombineMutators(NewSwapCrashNodeMutator(2), NewSwapNodeMutator(20), NewSwapMaxMessagesMutator(20))

			c := NewComparison(fConfig)
			c.AddBenchmark(
				"line",
				NewLineCovGuider(baseObjectPath, "127.0.0.1:2023", path.Join(savePath, "line_traces")), mutator,
			)
			c.AddBenchmark(
				"random",
				NewTLCStateGuider(
					"127.0.0.1:2023",
					path.Join(savePath, "random_traces"),
					baseObjectPath,
					gCovPath,
				),
				RandomMutator(),
			)
			c.AddBenchmark(
				"tlc",
				NewTLCStateGuider(
					"127.0.0.1:2023",
					path.Join(savePath, "tlc_traces"),
					baseObjectPath,
					gCovPath,
				),
				mutator,
			)
			c.AddBenchmark(
				"trace",
				NewTraceCoverageGuider(
					"127.0.0.1:2023",
					path.Join(savePath, "trace_traces"),
					baseObjectPath,
					gCovPath,
				),
				mutator,
			)

			sigCh := make(chan os.Signal, 1)
			signal.Notify(sigCh, os.Interrupt)

			doneCh := make(chan struct{})

			ctx, cancel := context.WithCancel(context.Background())
			go func() {
				select {
				case <-sigCh:
				case <-doneCh:
				}
				cancel()
			}()

			fmt.Println("Running...")
			c.Run(ctx, runs)
			fmt.Println("Completed.")
			close(doneCh)
		},
	}
	cmd.PersistentFlags().IntVarP(&episodes, "episodes", "e", 1000, "Number of iterations")
	cmd.PersistentFlags().IntVar(&horizon, "horizon", 100, "Number of steps for fuzzer to take")
	cmd.PersistentFlags().IntVarP(&nodes, "nodes", "n", 3, "Number of nodes")
	cmd.PersistentFlags().IntVar(&runs, "runs", 3, "Number of runs")
	cmd.PersistentFlags().IntVarP(&requests, "requests", "r", 3, "Number of requests")
	cmd.PersistentFlags().IntVarP(&workers, "workers", "w", 1, "Number of workers")
	cmd.PersistentFlags().StringVarP(&savePath, "save", "s", "results", "Path to save coverage comparisons")
	cmd.PersistentFlags().StringVar(&workPath, "work-path", "tmp", "Work directory to store the temporary files for redis server")
	cmd.PersistentFlags().StringVarP(&logLevel, "log-level", "v", "info", "Log level")
	return cmd
}

func main() {
	cmd := MainCommand()
	cmd.Execute()
}
