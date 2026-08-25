package fuzzing

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"slices"
	"strconv"
	"time"
	"github.com/egeberkaygulcan/2PC-Fuzzing/intercept"
)

type FuzzerConfig struct {
	Type               FuzzerType
	RunDuration        int
	Horizon            int
	SeedPopulationSize int
	ReseedFrequency    int
	RandomSeed         int64
	MaxMessages        int

	Guider Guider

	NumServers  int
	NumVars     int
	MaxVars     int
	NumRequests int
	Election    bool

	MutatorSwaps       int
	MutatorRandomCount int
	MutationCount      int

	StatsFilename string
}

type FuzzerType string

const (
	Random    FuzzerType = "Random"
	ModelFuzz FuzzerType = "ModelFuzz"
	Trace     FuzzerType = "Trace"
	Line      FuzzerType = "Line"
	RL 		  FuzzerType = "RL"
)

func StringToFuzzerType(s string) FuzzerType {
	switch s {
	case "Random":
		return Random
	case "ModelFuzz":
		return ModelFuzz
	case "Trace":
		return Trace
	case "Line":
		return Line
	case "RL":
		return RL
	}
	return ModelFuzz
}

type StepType string

const (
	Schedule StepType = "Schedule"
	CliReq   StepType = "ClientRequest"
)

type Step struct {
	Type        StepType
	Node        string
	From        string
	To          string
	Request     int
	MaxMessages int
	Step        int
	Vars        []int
}

type Stats struct {
	Coverage          []int
	RandomExecutions  int
	MutatedExecutions int
	// NumProcessedReqs  []int
	// NumCommittedReqs  []int
}

type Fuzzer struct {
	Type        FuzzerType
	RunDuration int // Minutes
	Horizon     int

	Config FuzzerConfig

	Cluster       *Cluster
	ScheduleQueue [][]Step
	Rand          *rand.Rand

	Mutator Mutator

	Stats Stats
}

func NewFuzzer(config FuzzerConfig) *Fuzzer {
	rand := rand.New(rand.NewSource(config.RandomSeed))

	nodes := make([]string, 0)
	for i := 0; i <= config.NumServers; i++ {
		nodes = append(nodes, strconv.Itoa(i))
	}
	initCov := [1]int{0}
	return &Fuzzer{Type: config.Type,
		RunDuration: config.RunDuration,
		Horizon:     config.Horizon,
		Config:      config,
		Cluster: NewCluster(ClusterConfig{NumServers: config.NumServers,
			NumVars:     config.NumVars,
			MaxVars:     config.MaxVars,
			NumRequests: config.NumRequests,
			Election:    config.Election,
			Rand:        rand}),
		ScheduleQueue: make([][]Step, 0),
		Rand:          rand,
		Mutator:       NewCombinedMutator(config.MutatorSwaps, config.MutatorRandomCount, rand, nodes),
		Stats:         Stats{Coverage: initCov[:]}, //, NumProcessedReqs: make([]int, 0), NumCommittedReqs: make([]int, 0)},
	}
}

func (f *Fuzzer) Start() {
	// fmt.Println("Starting fuzzer...")
	i := 0
	statTimer := time.NewTimer(1 * time.Minute)
loop:
	for timeout := time.After(time.Duration(f.RunDuration) * time.Minute); ; {
		select {
		case <-timeout:
			break loop
		case <-statTimer.C:
			f.Stats.Coverage = append(f.Stats.Coverage, f.Config.Guider.Coverage())
			f.WriteToJson()
			statTimer.Reset(1 * time.Minute)
			fmt.Println("States: " + strconv.Itoa(f.Stats.Coverage[len(f.Stats.Coverage)-1]))
		default:
		}

		if i%f.Config.ReseedFrequency == 0 {
			f.Seed()
			// fmt.Println("Seeding...")
		}

		var schedule []Step
		if len(f.ScheduleQueue) > 0 {
			schedule, f.ScheduleQueue = f.ScheduleQueue[0], f.ScheduleQueue[1:]
		} else {
			schedule = f.GenerateRandomSchedule()
			// fmt.Println("Seeding...")
		}

		// fmt.Println("Running iteration...")
		// fmt.Println(schedule)
		eventTrace := f.RunIteration(i, schedule)

		// fmt.Println(eventTrace)

		// Check states
		// fmt.Println("Checking new states...")
		newStates, numStates := f.Config.Guider.Check(strconv.Itoa(i), schedule, eventTrace)
		// fmt.Println("New states: " + strconv.Itoa(numStates))

		// Mutate
		if newStates && f.Type != Random {
			for m := 0; m < f.Config.MutationCount*numStates; m++ {
				sch := f.Mutator.Mutate(schedule)
				f.ScheduleQueue = append(f.ScheduleQueue, sch)
			}
		}

		// Update stats
		// f.Stats.NumProcessedReqs = append(f.Stats.NumProcessedReqs, f.Cluster.GetProcessedRequests())
		// f.Stats.NumCommittedReqs = append(f.Stats.NumCommittedReqs, f.Cluster.GetCommittedRequests())

		i++
		// fmt.Println(schedule)
		// break loop
	}
	f.Stats.Coverage = append(f.Stats.Coverage, f.Config.Guider.Coverage())
	f.WriteToJson()
	fmt.Print("Total states: ")
	fmt.Println(f.Config.Guider.Coverage())
	// fmt.Println(i)

	// fmt.Print("Max processed requests: ")
	// fmt.Println(slices.Max(f.Stats.NumProcessedReqs))
	// fmt.Print("Number of schedules with max processed: ")
	// fmt.Println(CountOccurances(f.Stats.NumProcessedReqs, slices.Max(f.Stats.NumProcessedReqs)))

	// fmt.Print("Max committed requests: ")
	// fmt.Println(slices.Max(f.Stats.NumCommittedReqs))
	// fmt.Print("Number of schedules with max committed: ")
	// fmt.Println(CountOccurances(f.Stats.NumCommittedReqs, slices.Max(f.Stats.NumCommittedReqs)))
}

func CountOccurances(s []int, v int) int {
	count := 0
	for _, val := range s {
		if val == v {
			count++
		}
	}
	return count
}

func (f *Fuzzer) RunIteration(iter int, schedule []Step) []intercept.Event {
	f.Cluster.Start()
	for i := 0; i < f.Horizon; i++ {
		step := schedule[i]

		switch step.Type {
		case Schedule:
			f.Cluster.Schedule(step.From, step.To, step.MaxMessages, step.Request)
		case CliReq:
			f.Cluster.ClientRequest(step.Vars)
		default:
		}
		f.Cluster.Tick()
		// time.Sleep(1 * time.Microsecond)
	}

	f.Cluster.Stop()
	eventTrace := f.Cluster.GetEventTrace()

	// f.Stats.NumProcessedReqs = append(f.Stats.NumProcessedReqs, f.Cluster.GetProcessedRequests())
	// f.Stats.NumCommittedReqs = append(f.Stats.NumCommittedReqs, f.Cluster.GetCommittedRequests())

	f.Cluster.Reset()
	return eventTrace
}

func (f *Fuzzer) GenerateRandomSchedule() []Step {
	sch := make([]Step, 0)
	nodes := make([]string, 0)
	for i := 0; i <= f.Config.NumServers; i++ {
		nodes = append(nodes, strconv.Itoa(i))
	}
	reqs := make([]int, 0)
	for i := 1; i <= f.Config.NumRequests; i++ {
		reqs = append(reqs, i)
	}
	for i := 0; i < f.Horizon; i++ {
		node := nodes[f.Rand.Intn(len(nodes))]
		to := "0"
		if node == "0" || f.Config.Election {
			nodeIndex := slices.Index(nodes, node)
			newNodes := slices.Delete(slices.Clone(nodes), nodeIndex, nodeIndex+1)
			to = newNodes[f.Rand.Intn(len(newNodes))]
		}

		req := f.Rand.Intn(len(reqs))

		sch = append(sch, Step{
			Type:        Schedule,
			Node:        node,
			From:        node,
			To:          to,
			Request:     reqs[req],
			MaxMessages: f.Rand.Intn(f.Config.MaxMessages) + 1,
		})
	}

	sch = slices.Insert(sch, 0, Step{Type: CliReq, Vars: nil}) //Step{Type: CliReq, Vars: f.Cluster.GenerateVars()})
	for i := 1; i < f.Config.NumRequests; i++ {
		index := f.Rand.Intn(len(sch))
		sch = slices.Insert(sch, index, Step{Type: CliReq, Vars: nil})
	}

	return sch
}

func (f *Fuzzer) Seed() {
	f.ScheduleQueue = make([][]Step, 0)
	for i := 0; i < f.Config.SeedPopulationSize; i++ {
		f.ScheduleQueue = append(f.ScheduleQueue, f.GenerateRandomSchedule())
	}
}

func (f *Fuzzer) WriteToJson() {
	json, _ := json.Marshal(f.Stats)
	err := os.WriteFile(f.Config.StatsFilename, json, 0644)
	if err != nil {
		fmt.Println("Error while writing json...")
	}
}

// func (f *Fuzzer) GenerateVars() []int {
// 	numVars := f.Rand.Intn(f.Config.MaxVars) + 1
// 	vars := make([]int, 0)
// 	for i := 1; i <= f.Config.NumVars; i++ {
// 		vars = append(vars, i)
// 	}

// 	randomVars := make([]int, 0)
// 	for i := 0; i < numVars; i++ {
// 		index := f.Rand.Intn(len(vars))
// 		v := vars[index]
// 		vars = slices.Delete(vars, index, index+1)
// 		randomVars = append(randomVars, v)
// 	}

// 	return randomVars
// 	// return []int{1}
// }
