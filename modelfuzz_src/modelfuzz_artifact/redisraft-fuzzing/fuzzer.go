package main

import (
	"context"
	"os"
)

type FuzzerConfig struct {
	Iterations      int
	Horizon         int
	NumWorkers      int
	NumNodes        int
	Guider          Guider
	Mutator         Mutator
	RecordPath      string
	LogLevel        string
	BaseNetworkPort int
	BaseWorkingDir  string

	MutationsPerTrace int
	SeedPopulation    int
	NumRequests       int
	NumCrashes        int
	MaxMessages       int
	ReseedFrequency   int

	ClusterConfig *ClusterConfig
}

type Fuzzer struct {
	sync    *FuzzerSync
	workers []*FuzzerWorker
	config  FuzzerConfig

	ctx    context.Context
	logger *Logger
}

func NewFuzzer(ctx context.Context, config FuzzerConfig) (*Fuzzer, error) {
	f := &Fuzzer{
		workers: make([]*FuzzerWorker, config.NumWorkers),
		config:  config,
		ctx:     ctx,
		logger:  NewLogger(),
	}
	f.logger.SetLevel(config.LogLevel)

	if _, err := os.Stat(config.RecordPath); err == nil {
		os.RemoveAll(config.RecordPath)
	}
	os.MkdirAll(config.RecordPath, 0777)
	if _, err := os.Stat(config.BaseWorkingDir); err == nil {
		os.RemoveAll(config.BaseWorkingDir)
	}
	os.MkdirAll(config.BaseWorkingDir, 0777)

	f.sync = NewFuzzerSync(config, f.logger.With(LogParams{"type": "sync"}))
	for i := 0; i < config.NumWorkers; i++ {
		w, err := NewFuzzerWorker(i, ctx, config, f.sync, f.logger.With(LogParams{"worker": i + 1}))
		if err != nil {
			return nil, err
		}
		f.workers[i] = w
	}
	return f, nil
}

func (f *Fuzzer) Reset(p string, guider Guider, mutator Mutator) {
	guider.Reset()
	f.config.Guider = guider
	f.config.Mutator = mutator
	f.sync.Reset()
	f.sync.UpdateGM(p, guider, mutator)

	for _, w := range f.workers {
		w.Reset()
	}
}

func (f *Fuzzer) Run() {
	for _, w := range f.workers {
		go w.Run()
	}
	select {
	case <-f.sync.Done():
		return
	case <-f.ctx.Done():
		return
	}
}

func (f *Fuzzer) Cleanup() {
	for _, w := range f.workers {
		w.Shutdown()
	}
}

func (f *Fuzzer) GetStats() *Stats {
	return f.sync.GetStats()
}
