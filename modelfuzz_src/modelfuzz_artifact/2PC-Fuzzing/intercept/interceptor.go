package intercept

import (
	"sync"
	"sync/atomic"
)

type Event struct {
	Name   string                 `json:"name"`
	Node   string                 `json:"-"`
	Params map[string]interface{} `json:"params"`
	Reset  bool                   `json:"Reset"`
}

type MessageType string

const (
	RMPrepared    MessageType = "RMPrepared"
	RMAborted                 = "RMAborted"
	Commit                    = "Commit"
	Abort                     = "Abort"
	Prepare                   = "Prepare"
	ClientRequest             = "NextRequest"
)

type Message struct {
	ID        uint64 `json:"id"`
	Sender    string `json:"sender"`
	Receiver  string `json:"receiver"`
	Type      string `json:"type"`
	RequestId int    `json:"requestId"`
	Vars      []int  `json:"vars"`
}

// Interceptor struct
// Server connector
// Fuzzer connector
type Interceptor struct {
	MessageQueues map[string]map[string][]Message
	TMQueue       map[string]map[string]map[int][]Message
	EventTrace    []Event
	Mu            sync.Mutex
	IDCounter     atomic.Uint64
}

func NewInterceptor() *Interceptor {
	i := &Interceptor{MessageQueues: make(map[string]map[string][]Message),
		TMQueue:    make(map[string]map[string]map[int][]Message),
		EventTrace: make([]Event, 0)}
	return i
}

func (i *Interceptor) Start() {

}

func (i *Interceptor) Stop() {

}

func (i *Interceptor) Reset() {
	defer i.Mu.Unlock()
	i.Mu.Lock()
	i.MessageQueues = make(map[string]map[string][]Message)
	i.IDCounter.Store(0)
	i.EventTrace = make([]Event, 0)
}

func (i *Interceptor) Send(m Message) {
	defer i.Mu.Unlock()
	// fmt.Println("RM " + m.Sender + " sending message " + m.Type + " for request " + strconv.Itoa(m.RequestId))
	i.Mu.Lock()
	m.ID = i.IDCounter.Add(1)
	// if i.MessageQueues == nil {
	// 	i.MessageQueues = make(map[string]map[string][]Message)
	// }
	// if i.MessageQueues[m.Sender] == nil {
	// 	i.MessageQueues[m.Sender] = make(map[string][]Message)
	// }
	if i.MessageQueues[m.Sender][m.Receiver] == nil {
		if i.MessageQueues[m.Sender] == nil {
			if i.MessageQueues == nil {
				i.MessageQueues = make(map[string]map[string][]Message)
			}
			i.MessageQueues[m.Sender] = make(map[string][]Message)
		}
		i.MessageQueues[m.Sender][m.Receiver] = make([]Message, 0)
	}

	i.MessageQueues[m.Sender][m.Receiver] = append(i.MessageQueues[m.Sender][m.Receiver], m)
	// fmt.Println(i.MessageQueues)
}

// func (i *Interceptor) TMSend(m Message) {
// 	defer i.Mu.Unlock()

// 	i.Mu.Lock()
// 	m.ID = i.IDCounter.Add(1)

// 	if i.TMQueue[m.Sender] == nil {
// 		i.TMQueue[m.Sender] = make(map[string]map[int][]Message)
// 		i.TMQueue[m.Sender][m.Receiver] = make(map[int][]Message)
// 		i.TMQueue[m.Sender][m.Receiver][m.RequestId] = make([]Message, 0)
// 	}
// 	if i.TMQueue[m.Sender][m.Receiver] == nil {
// 		i.TMQueue[m.Sender][m.Receiver] = make(map[int][]Message)
// 		i.TMQueue[m.Sender][m.Receiver][m.RequestId] = make([]Message, 0)
// 	}
// 	if i.TMQueue[m.Sender][m.Receiver][m.RequestId] == nil {
// 		i.TMQueue[m.Sender][m.Receiver][m.RequestId] = make([]Message, 0)
// 	}

// 	i.TMQueue[m.Sender][m.Receiver][m.RequestId] = append(i.TMQueue[m.Sender][m.Receiver][m.RequestId], m)
// }

func (i *Interceptor) SendEvent(e Event) {
	defer i.Mu.Unlock()
	i.Mu.Lock()
	i.EventTrace = append(i.EventTrace, e)
}

func (i *Interceptor) GetMessage(sender string, receiver string) Message {
	defer i.Mu.Unlock()
	i.Mu.Lock()
	var m Message = Message{ID: 0}
	if i.MessageQueues[sender][receiver] != nil && len(i.MessageQueues[sender][receiver]) > 0 {
		m, i.MessageQueues[sender][receiver] = i.MessageQueues[sender][receiver][0], i.MessageQueues[sender][receiver][1:]
	}
	return m
}

func (i *Interceptor) RetrieveMessages(sender string) []Message {
	defer i.Mu.Unlock()
	i.Mu.Lock()
	messages := make([]Message, 0)
	for _, msgMap := range i.MessageQueues[sender] {
		for _, msgs := range msgMap {
			messages = append(messages, msgs)
		}
	}
	return messages
}

// func (i *Interceptor) GetTMMessage(sender string, receiver string, request int) Message {
// 	defer i.Mu.Unlock()

// 	i.Mu.Lock()
// 	var m Message = Message{ID: 0}
// 	if i.TMQueue[sender][receiver][request] != nil && len(i.TMQueue[sender][receiver][request]) > 0 {
// 		m, i.TMQueue[sender][receiver][request] = i.TMQueue[sender][receiver][request][0], i.TMQueue[sender][receiver][request][1:]
// 	}

// 	return m
// }

func (i *Interceptor) GetEventTrace() []Event { return i.EventTrace }
