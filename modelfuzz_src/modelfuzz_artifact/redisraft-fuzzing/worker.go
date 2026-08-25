package main

import (
	"context"
	"errors"
	"path"
	"strconv"
	"time"
)

type FuzzerWorker struct {
	sync    *FuzzerSync
	ID      int
	config  FuzzerConfig
	network *FuzzerInterceptNetwork
	logger  *Logger

	networkAddr string
	doneCh      chan struct{}
	ctx         context.Context
}

func NewFuzzerWorker(id int, ctx context.Context, config FuzzerConfig, sync *FuzzerSync, logger *Logger) (*FuzzerWorker, error) {
	networkAddr := "localhost:" + strconv.Itoa(config.BaseNetworkPort+id)
	w := &FuzzerWorker{
		ID:          id,
		config:      config,
		sync:        sync,
		ctx:         ctx,
		networkAddr: networkAddr,
		logger:      logger,
		network:     NewInterceptNetwork(ctx, networkAddr, logger.With(LogParams{"type": "network"})),
	}

	w.network.Start()
	return w, nil
}

func (w *FuzzerWorker) Shutdown() {
	w.network.Shutdown()
}

func (w *FuzzerWorker) Reset() {
	w.doneCh = make(chan struct{})
	w.network.Reset()
}

func (w *FuzzerWorker) getCluster() *Cluster {
	cConfig := w.config.ClusterConfig.Copy()
	cConfig.BaseInterceptPort = 2023 + w.ID*10
	cConfig.BasePort = 5000 + w.ID*10
	cConfig.InterceptListenAddr = w.networkAddr
	cConfig.ID = w.ID
	cConfig.WorkingDir = path.Join(w.config.BaseWorkingDir, strconv.Itoa(w.ID))

	return NewCluster(cConfig, w.logger.With(LogParams{"type": "cluster"}))
}

func (w *FuzzerWorker) mimic(iter int, trace *Trace) {
	iterLogger := w.logger.With(LogParams{"iter": iter})
	iterLogger.Debug("Starting iteration")
	start := time.Now()

	defer w.network.Reset()
	cluster := w.getCluster()

	err := cluster.Start()
	if err != nil {
		iterLogger.With(LogParams{"error": err}).Error("failed to start cluster")
		cluster.Destroy()
		logs := cluster.GetLogs()
		w.sync.Update(iter, nil, nil, logs, err)
		return
	}

	ok := w.network.WaitForNodes(w.config.NumNodes)
	iterLogger.Debug("all nodes connected")
	if !ok {
		w.sync.Update(iter, nil, nil, "", errors.New("could not connect to all nodes"))
	}

	startPoints := make(map[int]int)
	crashPoints := make(map[int]int)
	scheduleFromNode := make([]int, w.config.Horizon)
	scheduleToNode := make([]int, w.config.Horizon)
	scheduleMaxMessages := make([]int, w.config.Horizon)
	clientRequests := make(map[int]string)

	for _, ch := range trace.Choices {
		switch ch.Type {
		case "Node":
			scheduleFromNode[ch.Step] = ch.From
			scheduleToNode[ch.Step] = ch.To
			scheduleMaxMessages[ch.Step] = ch.MaxMessages
		case "Start":
			startPoints[ch.Step] = ch.Node
		case "Crash":
			crashPoints[ch.Step] = ch.Node
		case "ClientRequest":
			clientRequests[ch.Step] = ch.Op
		}
	}
	crashedNodes := make(map[int]bool)

	for step := 0; step < w.config.Horizon; step++ {
		startNode, ok := startPoints[step]
		if ok {
			_, crashed := crashedNodes[startNode]
			if crashed {
				node, ok := cluster.GetNode(startNode)
				if ok {
					node.Start()
					w.network.AddEvent(Event{
						Name: "Add",
						Node: startNode,
						Params: map[string]interface{}{
							"i": startNode,
						},
					})
					delete(crashedNodes, startNode)
				}
			}
		}

		crashNode, ok := crashPoints[step]
		if ok {
			crashedNodes[crashNode] = true
			node, ok := cluster.GetNode(crashNode)
			if ok {
				node.Stop()
				w.network.AddEvent(Event{
					Name: "Remove",
					Node: crashNode,
					Params: map[string]interface{}{
						"i": crashNode,
					},
				})
			}
		}

		if _, ok := crashedNodes[scheduleToNode[step]]; !ok {
			w.network.Schedule(scheduleFromNode[step], scheduleToNode[step], scheduleMaxMessages[step])
		}

		op, ok := clientRequests[step]
		if ok {
			if op == "read" {
				cluster.ExecuteAsync("GET")
			} else {
				cluster.ExecuteAsync("INCR", "counter")
			}
		}

		time.Sleep(30 * time.Millisecond)
	}

	eventTrace := w.network.GetEventTrace()
	logs := ""
	err = cluster.Destroy()
	if err != nil {
		logs = cluster.GetLogs()
	}
	duration := time.Since(start)
	iterLogger.With(LogParams{"duration": duration.String()}).Info("Completed iteration")
	w.sync.Update(iter, trace.Copy(), eventTrace, logs, err)
}

func (w *FuzzerWorker) Run() {
	for {
		select {
		case <-w.ctx.Done():
			return
		default:
		}

		iter, mimic, ok := w.sync.GetTrace()
		if !ok {
			return
		}
		w.mimic(iter, mimic)
	}
}
