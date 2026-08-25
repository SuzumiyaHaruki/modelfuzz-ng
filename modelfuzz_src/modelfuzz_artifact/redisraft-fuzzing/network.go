package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

type Message struct {
	From          string                 `json:"from"`
	To            string                 `json:"to"`
	Data          string                 `json:"data"`
	Type          string                 `json:"type"`
	ID            string                 `json:"id"`
	ParsedMessage map[string]interface{} `json:"-"`
}

type entry struct {
	Term int    `json:"Term"`
	Data string `json:"Data"`
}

func (m Message) to() int {
	to, _ := strconv.Atoi(m.To)
	return to
}

func (m Message) from() int {
	from, _ := strconv.Atoi(m.From)
	return from
}

func (m Message) Copy() Message {
	n := Message{
		From:          m.From,
		To:            m.To,
		Data:          m.Data,
		Type:          m.Type,
		ID:            m.ID,
		ParsedMessage: make(map[string]interface{}),
	}
	if m.ParsedMessage != nil {
		for k, v := range m.ParsedMessage {
			n.ParsedMessage[k] = v
		}
	}
	return n
}

type FuzzerInterceptNetwork struct {
	Addr   string
	ctx    context.Context
	server *http.Server
	logger *Logger

	lock       *sync.Mutex
	nodes      map[int]string
	mailboxes  map[string][]Message
	Events     *EventTrace
	requests   map[string]int
	requestCtr int
}

func NewInterceptNetwork(ctx context.Context, addr string, logger *Logger) *FuzzerInterceptNetwork {

	f := &FuzzerInterceptNetwork{
		Addr:       addr,
		ctx:        ctx,
		lock:       new(sync.Mutex),
		nodes:      make(map[int]string),
		mailboxes:  make(map[string][]Message),
		Events:     NewEventTrace(),
		requests:   make(map[string]int),
		requestCtr: 0,
		logger:     logger,
	}

	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.POST("/replica", f.handleReplica)
	r.POST("/event", f.handleEvent)
	r.POST("/message", f.handleMessage)
	f.server = &http.Server{
		Addr:         addr,
		Handler:      r,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 5 * time.Second,
	}

	return f
}

func (n *FuzzerInterceptNetwork) handleMessage(c *gin.Context) {
	m := Message{}
	if err := c.ShouldBindJSON(&m); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "failed to unmarshal request"})
		return
	}
	to := m.to()
	parsedMessage := make(map[string]interface{})
	if err := json.Unmarshal([]byte(m.Data), &parsedMessage); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "failed to unmarshal request"})
		return
	}
	n.logger.With(LogParams{"message": parsedMessage}).Debug("received message")
	m.ParsedMessage = parsedMessage
	sendEvent := Event{
		Name:   "SendMessage",
		Node:   m.from(),
		Params: n.getMessageEventParams(m),
	}

	from := m.from()
	mKey := fmt.Sprintf("%d_%d", from, to)
	n.lock.Lock()
	_, ok := n.mailboxes[mKey]
	if !ok {
		n.mailboxes[mKey] = make([]Message, 0)
	}
	n.mailboxes[mKey] = append(n.mailboxes[mKey], m.Copy())
	n.Events.Add(sendEvent)
	n.lock.Unlock()

	c.JSON(http.StatusOK, gin.H{"message": "ok"})
}

func (n *FuzzerInterceptNetwork) handleReplica(c *gin.Context) {
	replica := make(map[string]interface{})
	if err := c.ShouldBindJSON(&replica); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "failed to unmarshal request"})
		return
	}
	n.logger.With(LogParams{"replica": replica}).Debug("recieved replica info")
	nodeID := 0
	nodeIDI, ok := replica["id"]
	if !ok {
		c.JSON(http.StatusOK, gin.H{"message": "ok"})
		return
	}
	nodeIDS, ok := nodeIDI.(string)
	if !ok {
		c.JSON(http.StatusOK, gin.H{"message": "ok"})
		return
	}
	nodeID, err := strconv.Atoi(nodeIDS)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"message": "ok"})
		return
	}

	nodeAddrI, ok := replica["addr"]
	if !ok {
		c.JSON(http.StatusOK, gin.H{"message": "ok"})
		return
	}
	nodeAddr, ok := nodeAddrI.(string)
	if !ok {
		c.JSON(http.StatusOK, gin.H{"message": "ok"})
		return
	}

	n.lock.Lock()
	n.nodes[nodeID] = nodeAddr
	n.lock.Unlock()

	c.JSON(http.StatusOK, gin.H{"message": "ok"})
}

func (n *FuzzerInterceptNetwork) handleEvent(c *gin.Context) {
	event := make(map[string]interface{})
	if err := c.ShouldBindJSON(&event); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "failed to unmarshal request"})
		return
	}
	n.logger.With(LogParams{"event": event}).Debug("received event")
	nodeID := 0
	nodeIDI, ok := event["replica"]
	if !ok {
		c.JSON(http.StatusOK, gin.H{"message": "ok"})
		return
	}
	nodeIDS, ok := nodeIDI.(string)
	if !ok {
		c.JSON(http.StatusOK, gin.H{"message": "ok"})
		return
	}
	nodeID, err := strconv.Atoi(nodeIDS)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"message": "ok"})
		return
	}

	eventTypeI, ok := event["type"]
	if !ok {
		c.JSON(http.StatusOK, gin.H{"message": "ok"})
		return
	}
	eventType, ok := eventTypeI.(string)
	if !ok {
		c.JSON(http.StatusOK, gin.H{"message": "ok"})
		return
	}

	e := Event{
		Name:   eventType,
		Node:   nodeID,
		Params: n.mapEventToParams(eventType, event),
	}

	n.lock.Lock()
	n.Events.Add(e)
	n.lock.Unlock()
	c.JSON(http.StatusOK, gin.H{"message": "ok"})
}

func (n *FuzzerInterceptNetwork) getRequestNumber(data string) int {
	n.lock.Lock()
	defer n.lock.Unlock()

	ctr, ok := n.requests[data]
	if !ok {
		ctr = n.requestCtr
		n.requests[data] = ctr
		n.requestCtr += 1
	}

	return ctr
}

func (n *FuzzerInterceptNetwork) getMessageEventParams(m Message) map[string]interface{} {
	params := make(map[string]interface{})

	params["term"] = int(m.ParsedMessage["term"].(float64))
	params["from"] = m.from()
	params["to"] = m.to()

	switch m.Type {
	case "append_entries_request":
		params["type"] = "MsgApp"
		params["log_term"] = m.ParsedMessage["prev_log_term"]
		entries := make([]entry, 0)
		for _, eI := range m.ParsedMessage["entries"].([]interface{}) {
			e := eI.(map[string]interface{})
			data := e["data"].(string)
			if data == "" {
				continue
			}
			eTermI, ok := e["term"]
			if !ok {
				continue
			}
			entries = append(entries, entry{
				Term: int(eTermI.(float64)),
				Data: strconv.Itoa(n.getRequestNumber(data)),
			})
		}
		params["entries"] = entries
		params["index"] = m.ParsedMessage["prev_log_idx"]
		params["commit"] = m.ParsedMessage["leader_commit"]
		params["reject"] = false
	case "append_entries_response":
		params["type"] = "MsgAppResp"
		params["log_term"] = 0
		params["entries"] = []entry{}
		params["index"] = m.ParsedMessage["current_idx"]
		params["commit"] = 0
		params["reject"] = int(m.ParsedMessage["success"].(float64)) == 0
	case "request_vote_request":
		params["type"] = "MsgVote"
		params["log_term"] = m.ParsedMessage["last_log_term"]
		params["entries"] = []entry{}
		params["index"] = m.ParsedMessage["last_log_idx"]
		params["commit"] = 0
		params["reject"] = false
	case "request_vote_response":
		params["type"] = "MsgVoteResp"
		params["log_term"] = 0
		params["entries"] = []entry{}
		params["index"] = 0
		params["commit"] = 0
		params["reject"] = int(m.ParsedMessage["vote_granted"].(float64)) == 0
	}
	return params
}

func (n *FuzzerInterceptNetwork) mapEventToParams(t string, e map[string]interface{}) map[string]interface{} {
	params := make(map[string]interface{})
	eParams := e["params"].(map[string]interface{})
	switch t {
	case "ClientRequest":
		leader, _ := strconv.Atoi(eParams["leader"].(string))
		params["leader"] = leader
		params["request"] = n.getRequestNumber(eParams["request"].(string))
	case "BecomeLeader":
		node, _ := strconv.Atoi(eParams["node"].(string))
		term, _ := strconv.Atoi(eParams["term"].(string))
		params["node"] = node
		params["term"] = term
	case "Timeout":
		node, _ := strconv.Atoi(eParams["node"].(string))
		params["node"] = node
	case "MembershipChange":
		nodeI, ok := eParams["node"]
		if !ok || nodeI == nil {
			return params
		}
		node, _ := strconv.Atoi(nodeI.(string))
		actionI, ok := eParams["action"]
		if !ok || actionI == nil {
			return params
		}
		params["action"] = actionI.(string)
		params["node"] = node
	case "UpdateSnapshot":
		node, _ := strconv.Atoi(eParams["node"].(string))
		params["node"] = node
		params["snapshot_index"] = int(eParams["snapshot_index"].(float64))
	default:
		params = eParams
	}

	return params
}

func (n *FuzzerInterceptNetwork) Start() {
	go func() {
		n.server.ListenAndServe()
	}()

	go func() {
		<-n.ctx.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		n.server.Shutdown(ctx)
	}()
}

// Shutdown stops the server
func (n *FuzzerInterceptNetwork) Shutdown() {
	select {
	case <-n.ctx.Done():
		return
	default:
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	n.server.Shutdown(ctx)
}

func (n *FuzzerInterceptNetwork) Reset() {
	n.lock.Lock()
	defer n.lock.Unlock()

	n.Events = NewEventTrace()
	n.mailboxes = make(map[string][]Message)
	n.nodes = make(map[int]string)
	n.requests = make(map[string]int)
	n.requestCtr = 0
}

func (n *FuzzerInterceptNetwork) GetEventTrace() *EventTrace {
	n.lock.Lock()
	defer n.lock.Unlock()

	return n.Events.Copy()
}

func (n *FuzzerInterceptNetwork) AddEvent(e Event) {
	n.lock.Lock()
	defer n.lock.Unlock()
	n.Events.Add(e)
}

func (n *FuzzerInterceptNetwork) WaitForNodes(numNodes int) bool {
	timeout := time.After(2 * time.Second)
	numConnectedNodes := 0
	for numConnectedNodes != numNodes {
		select {
		case <-n.ctx.Done():
			return false
		case <-timeout:
			return false
		case <-time.After(1 * time.Millisecond):
		}
		n.lock.Lock()
		numConnectedNodes = len(n.nodes)
		n.lock.Unlock()
	}
	return true
}

func (n *FuzzerInterceptNetwork) Schedule(from, to int, maxMessages int) {
	messagesToSend := make([]Message, 0)
	nodeAddr := ""
	mKey := fmt.Sprintf("%d_%d", from, to)
	n.lock.Lock()
	mailbox, ok := n.mailboxes[mKey]
	if ok {
		offset := 0
		for i, m := range mailbox {
			if i < maxMessages {
				messagesToSend = append(messagesToSend, m.Copy())
				offset = i
			}
		}
		if offset == len(mailbox)-1 {
			n.mailboxes[mKey] = make([]Message, 0)
		} else {
			n.mailboxes[mKey] = n.mailboxes[mKey][offset:]
		}
	}
	nodeAddr = n.nodes[to]
	n.lock.Unlock()

	client := &http.Client{
		Transport: &http.Transport{
			DialContext: (&net.Dialer{
				Timeout:   5 * time.Second,
				KeepAlive: 5 * time.Second,
			}).DialContext,
			TLSHandshakeTimeout:   5 * time.Second,
			ResponseHeaderTimeout: 5 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
			DisableKeepAlives:     true,
		},
	}

	for _, m := range messagesToSend {
		go func(m Message, addr string, client *http.Client) {
			bs, err := json.Marshal(m)
			if err != nil {
				return
			}
			n.logger.With(LogParams{
				"message": string(bs),
			}).Debug("sending message")
			resp, err := client.Post("http://"+addr+"/message", "application/json", bytes.NewBuffer(bs))
			if err == nil {
				io.ReadAll(resp.Body)
				resp.Body.Close()
			}
		}(m.Copy(), nodeAddr, client)

		receiveEvent := Event{
			Name:   "DeliverMessage",
			Node:   to,
			Params: n.getMessageEventParams(m),
		}
		n.lock.Lock()
		n.Events.Add(receiveEvent)
		n.lock.Unlock()
	}
}
