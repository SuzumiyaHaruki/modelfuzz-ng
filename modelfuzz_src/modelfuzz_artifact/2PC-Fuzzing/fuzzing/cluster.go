package fuzzing

import (
	"math"
	"math/rand"
	"slices"
	"strconv"
	"sync"

	"github.com/egeberkaygulcan/2PC-Fuzzing/intercept"
	"github.com/egeberkaygulcan/2PC-Fuzzing/server"
)

type ClusterConfig struct {
	NumServers  int
	NumVars     int
	MaxVars     int
	NumRequests int
	Election    bool
	Rand        *rand.Rand
	Offset      int
	TlcPort     int
}

type Cluster struct {
	Servers      []*server.Server
	Config       ClusterConfig
	RequestCount int
	Interceptor  *intercept.Interceptor
}

func NewCluster(config ClusterConfig) *Cluster {
	servers := make([]*server.Server, 0)
	interceptor := intercept.NewInterceptor()
	for i := 0; i <= config.NumServers; i++ {
		groupMembers := make([]string, 0)
		for j := 1; j <= config.NumServers; j++ {
			if j != i {
				groupMembers = append(groupMembers, strconv.Itoa(j))
			}
		}

		var role server.ServerRole
		if config.Election {
			role = server.Candidate
		} else {
			role = server.RM
			if i == 0 {
				role = server.TM
			}
		}

		conf := server.ServerConfig{
			ServerId: strconv.Itoa(i),
			Role:     role,
			LeaderId: "0",

			NumVars:      config.NumVars,
			NumRequests:  config.NumRequests,
			GroupMembers: groupMembers,

			Interceptor: interceptor,
		}

		s := server.NewServer(conf)
		servers = append(servers, s)
	}

	return &Cluster{Servers: servers,
		Config:       config,
		RequestCount: 1,
		Interceptor:  interceptor,
	}
}

func (c *Cluster) Start() {
	c.Interceptor.Start()

	for _, server := range c.Servers {
		server.Start()
	}
}

func (c *Cluster) Stop() {
	for _, server := range c.Servers {
		server.Stop()
	}

	c.Interceptor.Stop()
}

func (c *Cluster) StartServer(id string) {
	for _, server := range c.Servers {
		if server.ServerID == id {
			server.Start()
		}
	}
}

func (c *Cluster) StopServer(id string) {
	for _, server := range c.Servers {
		if server.ServerID == id {
			server.Stop()
		}
	}
}

func (c *Cluster) Reset() {
	for _, server := range c.Servers {
		server.Reset()
	}

	c.Interceptor.Reset()
	c.RequestCount = 1
}

func (c *Cluster) Schedule(from string, to string, maxMessages int, request int) {
	messages := make([]intercept.Message, 0)
	// fromserverIndex := c.GetServerIndex(from)
	// fromServer := c.Servers[fromserverIndex]
	for i := 0; i < maxMessages; i++ {
		var m intercept.Message
		// if fromServer.Role != TM {
		// 	// fmt.Print("Role: RM. ")
		// 	m = c.Interceptor.GetMessage(from, to)
		// } else {
		// 	// fmt.Print("Role: TM. ")
		// 	m = c.Interceptor.GetTMMessage(from, to, request)
		// }

		m = c.Interceptor.GetMessage(from, to)
		if m.ID == 0 {
			break
		}
		messages = append(messages, m)
	}

	// fmt.Println("Scheduling from " + from + " to " + to + " for req " + strconv.Itoa(request) + ", " + strconv.Itoa(len(messages)) + " messages.")

	toServerIndex := c.GetServerIndex(to)
	toServer := c.Servers[toServerIndex]
	var wg sync.WaitGroup
	for _, m := range messages {
		// toServer.Execute(m)
		// if serverIndex == -1{
		// 	return
		// }
		wg.Add(1)
		go func(m intercept.Message) {
			defer wg.Done()
			toServer.DeliverMessage(m)
		}(m)
	}
	wg.Wait()
}

func (c *Cluster) DeliverMessage(m intercept.Message) {
	toServerIndex := c.GetServerIndex(m.Receiver)
	toServer := c.Servers[toServerIndex]
	toServer.DeliverMessage(m)
}

func (c *Cluster) Tick() {
	var wg sync.WaitGroup
	for _, server := range c.Servers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			server.Execute(1)
		}()
	}
	wg.Wait()
}

func (c *Cluster) GetMessages(serverId string) []intercept.Message {
	return c.Interceptor.RetrieveMessages(serverId)
}

func (c *Cluster) GetServerIndex(id string) int {
	for i, server := range c.Servers {
		if server.ServerID == id {
			return i
		}
	}
	return -1
}

func (c *Cluster) VarsToInt(vars []int) int {
	res := 0
	for _, v := range vars {
		res += int(math.Pow(2, float64(v)))
	}
	return res
}

func (c *Cluster) ClientRequest(vars []int) {
	// fmt.Println("Sending client request " + strconv.Itoa(c.RequestCount))
	if vars == nil {
		vars = c.GenerateVars()
	}
	for _, s := range c.Servers {
		if s.Role == server.TM {
			msg := intercept.Message{Type: intercept.ClientRequest, Vars: vars, RequestId: c.RequestCount}
			msg.ID = c.Interceptor.IDCounter.Add(1)
			go s.TMRcvClientReq(msg)
		}
	}
	c.RequestCount++
}

func (c *Cluster) ClientRequestM(m intercept.Message) {
	// fmt.Println("Sending client request " + strconv.Itoa(c.RequestCount))
	for _, s := range c.Servers {
		if s.Role == server.TM {
			m.ID = c.Interceptor.IDCounter.Add(1)
			go s.TMRcvClientReq(m)
		}
	}
	c.RequestCount++
}

func (c *Cluster) GenerateVars() []int {
	numVars := c.Config.Rand.Intn(c.Config.MaxVars) + 1
	vars := make([]int, 0)
	for i := 1; i <= c.Config.NumVars; i++ {
		vars = append(vars, i)
	}

	randomVars := make([]int, 0)
	for i := 0; i < numVars; i++ {
		index := c.Config.Rand.Intn(len(vars))
		v := vars[index]
		vars = slices.Delete(vars, index, index+1)
		randomVars = append(randomVars, v)
	}

	return randomVars
	// return []int{1}
}

func (c *Cluster) GetEventTrace() []intercept.Event {
	return c.Interceptor.GetEventTrace()
}

func (c *Cluster) GetProcessedRequests() int {
	res := 0
	for _, s := range c.Servers {
		if s.Role == server.TM {
			res = s.Processed
		}
	}
	return res
}

func (c *Cluster) GetCommittedRequests() int {
	res := 0
	for _, s := range c.Servers {
		if s.Role == server.TM {
			res = s.Committed
		}
	}
	return res
}
