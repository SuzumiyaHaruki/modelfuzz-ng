package server

import (
	"slices"
	"sort"
	"sync"

	"github.com/egeberkaygulcan/2PC-Fuzzing/intercept"
)

type ServerConfig struct {
	ServerId string
	Role     ServerRole
	LeaderId string

	NumVars      int
	NumRequests  int
	GroupMembers []string

	Interceptor *intercept.Interceptor
}

// Role enum
type ServerRole int

const (
	TM        ServerRole = 0
	RM        ServerRole = 1
	Candidate ServerRole = 2
)

// Server struct
type Server struct {
	ServerID     string
	Role         ServerRole
	Interceptor  *intercept.Interceptor
	Vars         []int
	Config       ServerConfig
	Mu           sync.Mutex
	GroupMembers []string
	PreparedMap  map[int][]string
	LeaderId     string
	Processed    int
	Committed    int
	MessageQueue []intercept.Message
}

func NewServer(config ServerConfig) *Server {
	server := &Server{ServerID: config.ServerId,
		Role:         config.Role,
		Vars:         make([]int, 0),
		Config:       config,
		GroupMembers: config.GroupMembers,
		PreparedMap:  make(map[int][]string, config.NumRequests),
		LeaderId:     config.LeaderId,
		Interceptor:  config.Interceptor,
		Processed:    0,
		Committed:    0,
		MessageQueue: make([]intercept.Message, 0),
	}

	return server
}

func (s *Server) Start() {
	// s.Interceptor.Start()
}

func (s *Server) Stop() {
	// s.Interceptor.Stop()
}

func (s *Server) Reset() {
	defer s.Mu.Unlock()
	s.Mu.Lock()
	s.Role = s.Config.Role
	s.Vars = make([]int, 0)
	s.Committed = 0
	s.Processed = 0
	s.MessageQueue = make([]intercept.Message, 0)
	// s.Mu.Unlock()

	s.PreparedMap = make(map[int][]string, s.Config.NumRequests)
	s.LeaderId = s.Config.LeaderId
}

func (s *Server) DeliverMessage(m intercept.Message) {
	defer s.Mu.Unlock()
	s.Mu.Lock()
	s.MessageQueue = append(s.MessageQueue, m)
}

func (s *Server) RetrieveMessages() []intercept.Message {
	return s.Interceptor.RetrieveMessages(s.ServerID)
}

func (s *Server) Execute(count int) {
	// fmt.Println("Received message at server " + s.ServerID + " from " + m.Sender + " of type " + m.Type)
	for i := 0; i < count; i++ {
		if len(s.MessageQueue) > 0 {
			s.Mu.Lock()
			m := s.MessageQueue[0]
			s.MessageQueue = s.MessageQueue[1:]
			s.Mu.Unlock()

			switch m.Type {
			case string(intercept.Prepare):
				s.RMRcvPrepareReq(m)
			case string(intercept.RMPrepared):
				s.TMRcvPrepared(m)
			case string(intercept.RMAborted):
				s.TMRcvAborted(m)
			case string(intercept.Commit):
				s.RMRcvGlobalAbort(m)
			case string(intercept.Abort):
				s.RMRcvGlobalAbort(m)
			case string(intercept.ClientRequest):
				s.TMRcvClientReq(m)
			}
		} else {
			break
		}
	}
}

func (s *Server) TMRcvClientReq(m intercept.Message) {
	// fmt.Println("TMRcvClientReq at server: " + s.ServerID + ", for request: " + strconv.Itoa(m.RequestId) + ", with vars: " + strconv.Itoa(m.Vars))
	if s.Role == TM {
		s.Interceptor.SendEvent(intercept.Event{Name: "SendEvent",
			Params: map[string]interface{}{
				"event":      "NextRequest",
				"request_id": m.RequestId,
			}})
		s.TMSendPrepareReq(m.RequestId, m.Vars)
	}
}

func (s *Server) TMSendPrepareReq(requestId int, requestVars []int) {
	// fmt.Println("TMSendPrepareReq at server: " + s.ServerID + ", for request: " + strconv.Itoa(requestId) + ", with vars: " + strconv.Itoa(requestVars))
	if s.Role == TM {
		s.Interceptor.SendEvent(intercept.Event{Name: "SendEvent",
			Params: map[string]interface{}{
				"event":      "TMSendPrepareReq",
				"request_id": requestId,
			}})
		for _, id := range s.GroupMembers {
			// s.Interceptor.TMSend(Message{Type: Prepare,
			// 	Sender:    s.ServerID,
			// 	Receiver:  id,
			// 	RequestId: requestId,
			// 	Vars:      requestVars})
			go s.Interceptor.Send(intercept.Message{Type: intercept.Prepare,
				Sender:    s.ServerID,
				Receiver:  id,
				RequestId: requestId,
				Vars:      requestVars})
		}
	}
}

func (s *Server) RMRcvPrepareReq(m intercept.Message) {
	// fmt.Println("RMRcvPrepareReq at server: " + s.ServerID + ", for request: " + strconv.Itoa(m.RequestId) + ", with vars: " + strconv.Itoa(m.Vars))
	if s.Role == RM {
		s.Interceptor.SendEvent(intercept.Event{Name: "ReceiveEvent",
			Params: map[string]interface{}{
				"event":       "RMRcvPrepareReq",
				"receiver_id": s.ServerID,
				"request_id":  m.RequestId,
			},
			Node: s.ServerID})
		s.RespondPrepareReq(m)
	}
}

func (s *Server) CheckVars(vars []int) bool {
	res := true
	for _, v := range vars {
		if slices.Contains(s.Vars, v) {
			res = false
		}
	}
	return res
}

func (s *Server) RespondPrepareReq(m intercept.Message) {
	if s.Role == RM {
		if s.CheckVars(m.Vars) {
			s.RMSendPrepared(m)
		} else {
			s.RMSendAborted(m)
		}
	}
}

func (s *Server) RMSendPrepared(m intercept.Message) {
	// fmt.Println("RMSendPrepared at server: " + s.ServerID + ", for request: " + strconv.Itoa(m.RequestId) + ", with vars: " + strconv.Itoa(m.Vars))
	defer s.Mu.Unlock()
	if s.Role == RM {
		s.Interceptor.SendEvent(intercept.Event{Name: "SendEvent",
			Params: map[string]interface{}{
				"event":      "RMSendPrepared",
				"sender_id":  s.ServerID,
				"request_id": m.RequestId,
				"vars":       m.Vars,
			},
			Node: s.ServerID})
		go s.Interceptor.Send(intercept.Message{Type: string(intercept.RMPrepared),
			Sender:    s.ServerID,
			Receiver:  s.LeaderId,
			RequestId: m.RequestId,
			Vars:      m.Vars})

		s.Mu.Lock()
		for _, v := range m.Vars {
			s.Vars = append(s.Vars, v)
		}
	}
}

func (s *Server) RMSendAborted(m intercept.Message) {
	// fmt.Println("RMSendAborted at server: " + s.ServerID + ", for request: " + strconv.Itoa(m.RequestId) + ", with vars: " + strconv.Itoa(m.Vars))
	if s.Role == RM {
		s.Interceptor.SendEvent(intercept.Event{Name: "SendEvent",
			Params: map[string]interface{}{
				"event":      "RMSendAborted",
				"sender_id":  s.ServerID,
				"request_id": m.RequestId,
				"vars":       m.Vars,
			},
			Node: s.ServerID})
		go s.Interceptor.Send(intercept.Message{Type: intercept.RMAborted,
			Sender:    s.ServerID,
			Receiver:  s.LeaderId,
			RequestId: m.RequestId,
			Vars:      m.Vars})
	}
}

func (s *Server) TMRcvPrepared(m intercept.Message) {
	defer s.Mu.Unlock()
	// fmt.Println("TMRcvPrepared at server: " + s.ServerID + ", from: " + m.Sender + ", for request: " + strconv.Itoa(m.RequestId) + ", with vars: " + strconv.Itoa(m.Vars))
	if s.Role == TM {
		s.Mu.Lock()
		if s.PreparedMap[m.RequestId] == nil {
			s.PreparedMap[m.RequestId] = make([]string, 0)
		}
		s.PreparedMap[m.RequestId] = append(s.PreparedMap[m.RequestId], m.Sender)
		sort.Strings(s.PreparedMap[m.RequestId])
		s.Interceptor.SendEvent(intercept.Event{Name: "ReceiveEvent",
			Params: map[string]interface{}{
				"event":      "TMRcvPrepared",
				"sender_id":  m.Sender,
				"request_id": m.RequestId,
			},
			Node: s.ServerID})

		if slices.Equal(s.PreparedMap[m.RequestId], s.GroupMembers) {
			s.TMSendGlobalCommit(m)
		}
	}
}

func (s *Server) TMSendGlobalCommit(m intercept.Message) {
	// fmt.Println("TMSendGlobalCommit at server: " + s.ServerID + ", for request: " + strconv.Itoa(m.RequestId))
	if s.Role == TM {
		s.Interceptor.SendEvent(intercept.Event{Name: "SendEvent",
			Params: map[string]interface{}{
				"event":      "TMSendGlobalCommit",
				"sender_id":  s.ServerID,
				"request_id": m.RequestId,
			},
			Node: s.ServerID})
		s.Processed++
		s.Committed++
		for _, id := range s.GroupMembers {
			// s.Interceptor.TMSend(Message{Type: Commit,
			// 	Sender:    s.ServerID,
			// 	Receiver:  id,
			// 	RequestId: m.RequestId,
			// 	Vars:      m.Vars})
			go s.Interceptor.Send(intercept.Message{Type: intercept.Commit,
				Sender:    s.ServerID,
				Receiver:  id,
				RequestId: m.RequestId,
				Vars:      m.Vars})
		}
	}
}

func (s *Server) TMRcvAborted(m intercept.Message) {
	// fmt.Println("TMRcvAborted at server: " + s.ServerID + ", from: " + m.Sender + ", for request: " + strconv.Itoa(m.RequestId) + ", with vars: " + strconv.Itoa(m.Vars))
	if s.Role == TM {
		s.Interceptor.SendEvent(intercept.Event{Name: "ReceiveEvent",
			Params: map[string]interface{}{
				"event":      "TMRcvAborted",
				"sender_id":  m.Sender,
				"request_id": m.RequestId,
			},
			Node: s.ServerID})
		s.Processed++
		for _, id := range s.GroupMembers {
			// s.Interceptor.TMSend(Message{Type: Abort,
			// 	Sender:    s.ServerID,
			// 	Receiver:  id,
			// 	RequestId: m.RequestId,
			// 	Vars:      m.Vars})
			go s.Interceptor.Send(intercept.Message{Type: intercept.Abort,
				Sender:    s.ServerID,
				Receiver:  id,
				RequestId: m.RequestId,
				Vars:      m.Vars})
		}
	}
}

func (s *Server) RMRcvGlobalCommit(m intercept.Message) {
	// fmt.Println("RMRcvGlobalCommit at server: " + s.ServerID + ", for request: " + strconv.Itoa(m.RequestId) + ", with vars: " + strconv.Itoa(m.Vars))
	defer s.Mu.Unlock()
	s.Interceptor.SendEvent(intercept.Event{Name: "ReceiveEvent",
		Params: map[string]interface{}{
			"event":       "RMRcvGlobalCommit",
			"receiver_id": s.ServerID,
			"request_id":  m.RequestId,
			"vars":        m.Vars,
		},
		Node: s.ServerID})

	s.Mu.Lock()
	for _, v := range m.Vars {
		index := slices.Index(s.Vars, v)
		if index > -1 {
			s.Vars = append(s.Vars[:index], s.Vars[index+1:]...)
		}
	}
}

func (s *Server) RMRcvGlobalAbort(m intercept.Message) {
	// fmt.Println("RMRcvGlobalAbort at server: " + s.ServerID + ", for request: " + strconv.Itoa(m.RequestId) + ", with vars: " + strconv.Itoa(m.Vars))
	defer s.Mu.Unlock()
	s.Interceptor.SendEvent(intercept.Event{Name: "ReceiveEvent",
		Params: map[string]interface{}{
			"event":       "RMRcvGlobalAbort",
			"receiver_id": s.ServerID,
			"request_id":  m.RequestId,
			"vars":        m.Vars,
		},
		Node: s.ServerID})

	s.Mu.Lock()
	for _, v := range m.Vars {
		index := slices.Index(s.Vars, v)
		if index > -1 {
			s.Vars = append(s.Vars[:index], s.Vars[index+1:]...)
		}
	}
}
