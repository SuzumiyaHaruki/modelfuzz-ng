package main

import (
	"errors"
	"fmt"
	"math/rand"
	"os"
	"path"
	"sync"
	"time"
)

type FuzzerSync struct {
	doneCh  chan struct{}
	config  FuzzerConfig
	guider  Guider
	mutator Mutator
	logger  *Logger

	recordPathPrefix string
	traceQueue       []*Trace
	stats            *Stats
	iteration        int
	completed        map[int]bool
	lock             *sync.Mutex
	rand             *rand.Rand
}

func NewFuzzerSync(config FuzzerConfig, logger *Logger) *FuzzerSync {
	s := &FuzzerSync{
		doneCh:     make(chan struct{}),
		config:     config,
		guider:     nil,
		mutator:    nil,
		logger:     logger,
		traceQueue: make([]*Trace, 0),
		stats:      NewStats(),
		iteration:  0,
		completed:  make(map[int]bool),
		lock:       new(sync.Mutex),
		rand:       rand.New(rand.NewSource(time.Now().UnixMilli())),
	}
	return s
}

func (f *FuzzerSync) Done() chan struct{} {
	return f.doneCh
}

func (f *FuzzerSync) UpdateGM(recordPathPrefix string, guider Guider, mutator Mutator) {
	f.recordPathPrefix = recordPathPrefix
	f.guider = guider
	f.mutator = mutator
}

func (f *FuzzerSync) Reset() {
	f.lock.Lock()
	defer f.lock.Unlock()

	f.traceQueue = make([]*Trace, 0)
	f.stats = NewStats()
	f.iteration = 0
	f.completed = make(map[int]bool)

	f.doneCh = make(chan struct{})
}

func (f *FuzzerSync) GetStats() *Stats {
	f.lock.Lock()
	defer f.lock.Unlock()
	f.stats.FinalBranchCoverage = f.guider.BranchCoverage()
	return f.stats.Copy()
}

func (f *FuzzerSync) Update(iter int, trace *Trace, eventTrace *EventTrace, logs string, err error) {
	f.logger.With(LogParams{"iter": iter, "logs": len(logs)}).Debug("Compeleted iteration")
	iterS := fmt.Sprintf("%s_%d", f.recordPathPrefix, iter)
	if len(logs) != 0 || err != nil {
		if err != nil && !errors.Is(err, ErrRedisBug) {
			f.logger.With(LogParams{"error": err, "iter": iter}).Info("Error running iteration")
		}
		f.recordLogs(iterS, logs)
	}
	if trace != nil && eventTrace != nil {
		new, weight := f.guider.Check(iterS, trace, eventTrace, false)
		if new {
			mutatedTraces := make([]*Trace, 0)
			for i := 0; i < weight*f.config.MutationsPerTrace; i++ {
				newTrace, ok := f.mutator.Mutate(trace, eventTrace)
				if ok {
					mutatedTraces = append(mutatedTraces, newTrace.Copy())
				}
			}
			f.lock.Lock()
			f.traceQueue = append(f.traceQueue, mutatedTraces...)
			f.lock.Unlock()
		}
	}

	f.lock.Lock()
	f.stats.AddCoverage(f.guider.Coverage())
	f.completed[iter] = true
	allDone := true
	for i := 0; i < f.config.Iterations; i++ {
		if _, ok := f.completed[i]; !ok {
			allDone = false
			break
		}
	}
	f.lock.Unlock()

	if allDone {
		close(f.doneCh)
	}

}

func (f *FuzzerSync) recordLogs(filePrefix string, logs string) {
	filePath := path.Join(f.config.RecordPath, filePrefix+".log")
	f.logger.With(LogParams{"file": filePath}).Debug("Writing logs to file")
	os.WriteFile(filePath, []byte(logs), 0644)
}

func (f *FuzzerSync) GetTrace() (int, *Trace, bool) {
	f.lock.Lock()
	defer f.lock.Unlock()

	iteration := f.iteration

	if iteration == f.config.Iterations {
		return 0, nil, false
	}

	if iteration%f.config.ReseedFrequency == 0 {
		f.seed()
	}

	var trace *Trace = nil
	if len(f.traceQueue) > 0 {
		trace = f.traceQueue[0]
		f.traceQueue = f.traceQueue[1:]
		f.stats.MutatedTraces += 1
	}

	if trace == nil {
		trace = f.randomTrace()
		f.stats.RandomTraces += 1
	}

	f.iteration += 1

	return iteration, trace, true
}

func (f *FuzzerSync) seed() {
	traces := make([]*Trace, f.config.SeedPopulation)
	for i := 0; i < f.config.SeedPopulation; i++ {
		traces[i] = f.randomTrace().Copy()
	}
	f.traceQueue = traces
}

func (f *FuzzerSync) randomTrace() *Trace {
	trace := NewTrace()
	for i := 0; i < f.config.Horizon; i++ {
		fromIdx := f.rand.Intn(f.config.NumNodes) + 1
		toIdx := f.rand.Intn(f.config.NumNodes) + 1
		trace.Add(Choice{
			Type:        "Node",
			Step:        i,
			From:        fromIdx,
			To:          toIdx,
			MaxMessages: f.rand.Intn(f.config.MaxMessages),
		})
	}
	choices := make([]int, f.config.Horizon)
	for i := 0; i < f.config.Horizon; i++ {
		choices[i] = i
	}
	for _, c := range sample(choices, f.config.NumCrashes, f.rand) {
		idx := f.rand.Intn(f.config.NumNodes) + 1
		trace.Add(Choice{
			Type: "Crash",
			Node: idx,
			Step: c,
		})

		s := sample(intRange(c, f.config.Horizon), 1, f.rand)[0]
		trace.Add(Choice{
			Type: "Start",
			Node: idx,
			Step: s,
		})
	}
	for _, req := range sample(choices, f.config.NumRequests, f.rand) {
		op := "read"
		if f.rand.Intn(2) == 1 {
			op = "write"
		}
		trace.Add(Choice{
			Type: "ClientRequest",
			Op:   op,
			Step: req,
		})
	}
	return trace
}
