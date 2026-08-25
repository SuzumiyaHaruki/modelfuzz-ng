package fuzzing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"maps"
	"math/rand"
	"os"
	"strconv"
	"time"

	"github.com/zeu5/dist-rl-testing/core"
	"github.com/zeu5/dist-rl-testing/policies"
	"github.com/zeu5/dist-rl-testing/util"

	"github.com/egeberkaygulcan/2PC-Fuzzing/intercept"
	"github.com/egeberkaygulcan/2PC-Fuzzing/server"
)

type TwoPCRLMessageWrapper struct {
	intercept.Message
}

func (m TwoPCRLMessageWrapper) From() int {
	intVal, _ := strconv.Atoi(m.Message.Sender)
	if intVal <= 0 {
		log.Fatal("Id >= 0")
	}
	return intVal
}

func (m TwoPCRLMessageWrapper) To() int {
	intVal, _ := strconv.Atoi(m.Message.Receiver)
	if intVal <= 0 {
		log.Fatal("Id >= 0")
	}
	return intVal
}

func (m TwoPCRLMessageWrapper) Hash() string {
	return util.JsonHash(m.Message)
}

type TwoPCRLNodeState struct {
	Role         server.ServerRole
	Vars         []int
	PreparedMap  map[int][]string
	MessageQueue []intercept.Message
}

func (t *TwoPCRLNodeState) Copy() *TwoPCRLNodeState {
	cpyVars := make([]int, len(t.Vars))
	copy(cpyVars, t.Vars)

	cpyPreparedMap := make(map[int][]string)
	maps.Copy(cpyPreparedMap, t.PreparedMap)

	cpyMessageQueue := make([]intercept.Message, len(t.MessageQueue))
	copy(cpyMessageQueue, t.MessageQueue)

	return &TwoPCRLNodeState{
		Role:         t.Role,
		Vars:         cpyVars,
		PreparedMap:  cpyPreparedMap,
		MessageQueue: cpyMessageQueue,
	}
}

var _ core.NState = &TwoPCRLNodeState{}

type TwoPCRLState struct {
	nodeStates map[int]*TwoPCRLNodeState
	// messages   []core.Message
	PendingRequests []intercept.Message
	MessageMap      map[string]intercept.Message
}

func (t *TwoPCRLState) Copy() *TwoPCRLState {
	cpyNodeStates := make(map[int]*TwoPCRLNodeState)
	for key, value := range t.nodeStates {
		cpyNodeStates[key] = value.Copy()
	}

	cpyMessages := make(map[string]intercept.Message)
	maps.Copy(cpyMessages, t.MessageMap)

	cpyRequests := make([]intercept.Message, len(t.PendingRequests))
	copy(cpyRequests, t.PendingRequests)

	return &TwoPCRLState{
		nodeStates:      cpyNodeStates,
		MessageMap:      cpyMessages,
		PendingRequests: cpyRequests,
	}
}

func (t *TwoPCRLState) NodeState(node int) core.NState {
	return t.nodeStates[node]
}

func (t *TwoPCRLState) Messages() []core.Message {
	messages := make([]core.Message, len(t.MessageMap))
	i := 0
	for _, m := range t.MessageMap {
		messages[i] = TwoPCRLMessageWrapper{m}
		i++
	}
	return messages
	// return nil
}

func (t *TwoPCRLState) Requests() []core.Request {
	requests := make([]core.Request, len(t.PendingRequests))
	for i, r := range t.PendingRequests {
		requests[i] = r
	}
	return requests
	// return nil
}

func (t *TwoPCRLState) CanDeliverRequest() bool {
	return true
}

var _ core.PState = &TwoPCRLState{}

type TwoPCRLEnv struct {
	cluster  *Cluster
	curState *TwoPCRLState
	messages map[string]intercept.Message

	traces    [][]intercept.Event
	guider    *TLCStateGuider
	statTimer *time.Timer
	stats     Stats
}

var _ core.PEnvironment = &TwoPCRLEnv{}

func NewTwoPCRLEnv(config ClusterConfig) *TwoPCRLEnv {
	initCov := [1]int{0}
	return &TwoPCRLEnv{
		cluster:   NewCluster(config),
		traces:    make([][]intercept.Event, 0),
		guider:    NewTLCStateGuider("localhost:"+strconv.Itoa(config.TlcPort)),
		statTimer: time.NewTimer(1 * time.Minute),
		stats:     Stats{Coverage: initCov[:]},
	}
}

func (env *TwoPCRLEnv) Reset(eCtx *core.EpisodeContext) (core.PState, error) {
	trace := env.cluster.GetEventTrace()
	env.traces = append(env.traces, trace)
	if len(env.stats.Coverage) > 60 {
		eCtx.Error(errors.New("Finish"))
		fmt.Println("Total states: " + strconv.Itoa(env.stats.Coverage[len(env.stats.Coverage)-1]))
	}
	select {
	case <-env.statTimer.C:
		for _, trace := range env.traces {
			env.guider.Check(strconv.Itoa(eCtx.Episode), nil, trace)
		}
		env.traces = make([][]intercept.Event, 0)
		env.stats.Coverage = append(env.stats.Coverage, env.guider.Coverage())
		json, _ := json.Marshal(env.stats)
		err := os.WriteFile("rl_stats_"+strconv.Itoa(env.cluster.Config.Offset)+".json", json, 0644)
		if err != nil {
			fmt.Println("Error while writing json...")
		}
		env.statTimer.Reset(1 * time.Minute)
		fmt.Println("States: " + strconv.Itoa(env.stats.Coverage[len(env.stats.Coverage)-1]))
	default:
	}

	env.cluster.Reset()
	// fmt.Println(len(trace))

	env.cluster = NewCluster(env.cluster.Config)
	env.messages = make(map[string]intercept.Message)
	env.curState = &TwoPCRLState{
		nodeStates:      make(map[int]*TwoPCRLNodeState),
		PendingRequests: make([]intercept.Message, env.cluster.Config.NumRequests),
		MessageMap:      make(map[string]intercept.Message),
	}

	for i := 0; i < env.cluster.Config.NumRequests; i++ {
		env.curState.PendingRequests[i] = intercept.Message{Type: intercept.ClientRequest, Vars: env.cluster.GenerateVars(), RequestId: i}
	}
	// fmt.Println("PendingRequests")
	// fmt.Println(env.curState.PendingRequests)

	for i := 0; i <= env.cluster.Config.NumServers; i++ {
		nodeState := &TwoPCRLNodeState{
			Role:         env.cluster.Servers[i].Role,
			Vars:         env.cluster.Servers[i].Vars,
			PreparedMap:  env.cluster.Servers[i].PreparedMap,
			MessageQueue: env.cluster.Servers[i].MessageQueue,
		}
		env.curState.nodeStates[i+1] = nodeState
	}
	// fmt.Printf("Done resetting!\n")
	return env.curState, nil
}

func (env *TwoPCRLEnv) Tick(_ *core.StepContext) (core.PState, error) {
	env.cluster.Tick()
	newState := env.curState.Copy()
	for i, server := range env.cluster.Servers {
		msgs := env.cluster.GetMessages(server.ServerID)
		for _, msg := range msgs {
			intVal, _ := strconv.Atoi(msg.Receiver)
			msg.Receiver = strconv.Itoa(intVal + 1)

			intVal, _ = strconv.Atoi(msg.Sender)
			msg.Sender = strconv.Itoa(intVal + 1)

			msgK := util.JsonHash(msg)
			env.messages[msgK] = msg
		}
		serverState := &TwoPCRLNodeState{
			Role:         server.Role,
			Vars:         server.Vars,
			PreparedMap:  server.PreparedMap,
			MessageQueue: server.MessageQueue,
		}
		newState.nodeStates[i+1] = serverState
	}
	maps.Copy(newState.MessageMap, env.messages)
	copy(newState.PendingRequests, env.curState.PendingRequests)
	env.curState = newState

	return env.curState, nil
}

func (env *TwoPCRLEnv) DeliverMessages(messages []core.Message, _ *core.StepContext) (core.PState, error) {
	// fmt.Println(messages)
	for _, m := range messages {
		msg := m.(TwoPCRLMessageWrapper).Message
		msgK := util.JsonHash(msg)

		intVal, _ := strconv.Atoi(msg.Receiver)
		msg.Receiver = strconv.Itoa(intVal - 1)

		intVal, _ = strconv.Atoi(msg.Sender)
		msg.Sender = strconv.Itoa(intVal - 1)

		env.cluster.DeliverMessage(msg)
		delete(env.messages, msgK)
	}
	// Update state
	newState := env.curState.Copy()
	for i, server := range env.cluster.Servers {
		msgs := env.cluster.GetMessages(server.ServerID)
		for _, msg := range msgs {
			intVal, _ := strconv.Atoi(msg.Receiver)
			msg.Receiver = strconv.Itoa(intVal + 1)

			intVal, _ = strconv.Atoi(msg.Sender)
			msg.Sender = strconv.Itoa(intVal + 1)

			msgK := util.JsonHash(msg)
			env.messages[msgK] = msg
		}
		serverState := &TwoPCRLNodeState{
			Role:         server.Role,
			Vars:         server.Vars,
			PreparedMap:  server.PreparedMap,
			MessageQueue: server.MessageQueue,
		}
		newState.nodeStates[i+1] = serverState
	}
	maps.Copy(newState.MessageMap, env.messages)
	env.curState = newState
	return env.curState, nil
}

func (env *TwoPCRLEnv) DropMessages(_ []core.Message, _ *core.StepContext) (core.PState, error) {
	return env.curState, nil
}

func (env *TwoPCRLEnv) ReceiveRequest(req core.Request, _ *core.StepContext) (core.PState, error) {
	// fmt.Println("Requests")
	// fmt.Println(req)
	// fmt.Println("ReceiveRequest")
	// fmt.Println(env.curState.PendingRequests)
	newState := env.curState.Copy()
	newState.PendingRequests = make([]intercept.Message, 0)
	env.cluster.ClientRequestM(req.(intercept.Message))
	remainingRequests := env.curState.PendingRequests[1:]

	newState.PendingRequests = append(newState.PendingRequests, remainingRequests...)
	// fmt.Println(newState.PendingRequests)

	env.curState = newState
	return env.curState, nil
}

func (env *TwoPCRLEnv) StartNode(nodeId int, _ *core.StepContext) (core.PState, error) {
	env.cluster.StartServer(strconv.Itoa(nodeId))
	return env.curState, nil
}

func (env *TwoPCRLEnv) StopNode(nodeId int, _ *core.StepContext) (core.PState, error) {
	env.cluster.StopServer(strconv.Itoa(nodeId))
	return env.curState, nil
}

type TwoPCRLEnvCons struct {
	config ClusterConfig
}

var _ core.PEnvironmentConstructor = &TwoPCRLEnvCons{}

func (c *TwoPCRLEnvCons) NewPEnvironment(_ context.Context, _ int) core.PEnvironment {
	// fmt.Println("NewPEnvironment")
	return NewTwoPCRLEnv(c.config)
}

func ColorRole() core.KVPainter {
	return func(n core.NState) (string, interface{}) {
		ns := n.(*TwoPCRLNodeState)
		return "role", ns.Role
	}
}

func ColorPrepared() core.KVPainter {
	return func(n core.NState) (string, interface{}) {
		ns := n.(*TwoPCRLNodeState)
		return "prepared", ns.PreparedMap
	}
}

func ColorMessages() core.KVPainter {
	return func(n core.NState) (string, interface{}) {
		ns := n.(*TwoPCRLNodeState)
		return "messages", ns.MessageQueue
	}
}

func ColorVars() core.KVPainter {
	return func(n core.NState) (string, interface{}) {
		ns := n.(*TwoPCRLNodeState)
		return "vars", ns.Vars
	}
}

func (env *TwoPCRLEnv) WriteToJson(eCtx *core.EpisodeContext) {
	json, _ := json.Marshal(env.stats)
	err := os.WriteFile("rl_stats_"+strconv.Itoa(env.cluster.Config.Offset)+".json", json, 0644)
	if err != nil {
		fmt.Println("Error while writing json...")
	}
}

func RunRL(offset int, tlcPort int, fuzzerConfig FuzzerConfig) {
	twoPCConfig := &ClusterConfig{
		NumServers:  fuzzerConfig.NumServers,
		NumVars:     fuzzerConfig.NumVars,
		MaxVars:     fuzzerConfig.NumVars,
		NumRequests: fuzzerConfig.NumRequests,
		Election:    false,
		Rand:        rand.New(rand.NewSource(fuzzerConfig.RandomSeed)),
		Offset:      offset,
		TlcPort:     tlcPort}

	pEnvConfig := &core.PEnvironmentConfig{
		TicksBetweenPartition: 4,
		Painter: core.NewComposedPainter(
			ColorRole(),
			// ColorVars(),
			// ColorPrepared(),
			// ColorMessaegs(),
		).Painter(),
		// Total number of nodes
		NumNodes: 4,
		// Keep it high to ensure all messages are delivered in each step
		MaxMessagesPerTick: 100,
		StaySameUpto:       5,
		WithCrashes:        false,
		MaxCrashedNodes:    0,
		// If you want to bound terms or anything similar add it here.
		BoundaryPredicate: func(s core.PState) bool {
			return false
		},
	}

	cmp := core.NewParallelComparison()
	cmp.AddExperiment(&core.ParallelExperiment{
		Name: "TwoPCRL",
		Environment: (pEnvConfig).GetConstructor(&TwoPCRLEnvCons{
			config: *twoPCConfig,
		}),
		Policy: policies.NewBonusPolicyGreedyRewardConstructor(0.2, 0.95, 0.05),
	})

	fmt.Println("Running...")
	cmp.Run(context.Background(), 1, &core.RunConfig{
		Episodes: fuzzerConfig.RunDuration * 320, // Estimated, roughly runs 320 episodes per minute
		// Num steps
		Horizon: fuzzerConfig.Horizon,
		// If errors are encountered consecutively for these many steps then stop
		ThresholdConsecutiveErrors: 2,
		// If timeouts are encountered consecutively for these many steps then stop
		ThresholdConsecutiveTimeouts: 10,
		EpisodeTimeout:               10 * time.Minute,
	}, 1)
	
	// time.Sleep(10 * time.Second)
	// guider := NewTLCStateGuider("localhost:2026", "./", "./", "./")
}
