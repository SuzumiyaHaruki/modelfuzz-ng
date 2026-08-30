package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// TLCTransition 是 modified controlled TLC server 返回的一条输入事件执行记录。
// EventIndex 对应发送给服务端的原始 event trace 下标；ignored 事件也会保留。
type TLCTransition struct {
	EventIndex   int    `json:"eventIndex"`
	InputName    string `json:"inputName"`
	MappedAction string `json:"mappedAction"`
	Status       string `json:"status"`
	PreKey       int64  `json:"preKey"`
	PostKey      int64  `json:"postKey"`
}

// TLCResponse 同时兼容原始服务端和带 transition provenance 的服务端。
// 指针用于区分“字段不存在的旧协议”和“存在但没有事件的新协议”。
type TLCResponse struct {
	States      []string         `json:"states"`
	Keys        []int64          `json:"keys"`
	Transitions *[]TLCTransition `json:"transitions"`
}

type TLCExecution struct {
	States              []State
	Transitions         []TLCTransition
	ProvenanceAvailable bool
}

// TLCClient 负责把 event trace 发送给本地/远端 TLC server。
//
// TLC server 暴露 /execute 接口，接收 JSON 事件序列，按 TLA+ 模型执行这些事件，
// 返回经过的状态字符串和状态 key。Fuzzer 不直接理解 TLA+，只消费这里返回的 State。
type TLCClient struct {
	ClientAddr string
	client     *http.Client
}

func NewTLCClient(addr string) *TLCClient {
	return &TLCClient{
		ClientAddr: addr,
		client:     http.DefaultClient,
	}
}

func (c *TLCClient) SendTrace(trace *List[*Event]) ([]State, error) {
	execution, err := c.ExecuteTrace(trace)
	if err != nil {
		return []State{}, err
	}
	return execution.States, nil
}

// ExecuteTrace 返回覆盖状态以及可选的逐输入事件 provenance。SendTrace 保留为
// 向后兼容包装，现有非 attribution 调用方不需要知道服务端协议扩展。
func (c *TLCClient) ExecuteTrace(trace *List[*Event]) (TLCExecution, error) {
	// Reset 事件通知 TLC server 从模型初始状态开始执行这条 trace。
	// 在独立切片上追加，避免 Check 和前缀探测反复发送时污染调用方的 event trace。
	events := make([]*Event, 0, trace.Size()+1)
	events = append(events, trace.Iter()...)
	events = append(events, &Event{Reset: true})
	data, err := json.Marshal(events)
	if err != nil {
		return TLCExecution{}, fmt.Errorf("error marshalling json: %s", err)
	}
	addr := c.ClientAddr
	if !strings.HasPrefix(addr, "http://") && !strings.HasPrefix(addr, "https://") {
		addr = "http://" + addr
	}
	httpClient := c.client
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	res, err := httpClient.Post(strings.TrimRight(addr, "/")+"/execute", "application/json", bytes.NewBuffer(data))
	if err != nil {
		return TLCExecution{}, fmt.Errorf("error sending trace to tlc: %s", err)
	}
	defer res.Body.Close()
	resData, err := io.ReadAll(res.Body)
	if err != nil {
		return TLCExecution{}, fmt.Errorf("error reading response from tlc: %s", err)
	}
	if res.StatusCode < http.StatusOK || res.StatusCode >= http.StatusMultipleChoices {
		return TLCExecution{}, fmt.Errorf("tlc returned HTTP %d: %s", res.StatusCode, strings.TrimSpace(string(resData)))
	}
	tlcResponse := &TLCResponse{}
	if err = json.Unmarshal(resData, tlcResponse); err != nil {
		return TLCExecution{}, fmt.Errorf("error parsing tlc response: %s", err)
	}
	if len(tlcResponse.States) != len(tlcResponse.Keys) {
		return TLCExecution{}, fmt.Errorf("invalid tlc response: %d states but %d keys", len(tlcResponse.States), len(tlcResponse.Keys))
	}
	result := make([]State, len(tlcResponse.States))
	for i, s := range tlcResponse.States {
		result[i] = State{Repr: s, Key: tlcResponse.Keys[i]}
	}
	execution := TLCExecution{States: result}
	if tlcResponse.Transitions == nil {
		return execution, nil
	}
	if len(*tlcResponse.Transitions) != trace.Size() {
		return TLCExecution{}, fmt.Errorf(
			"invalid provenance response: %d transitions for %d input events",
			len(*tlcResponse.Transitions), trace.Size())
	}
	execution.Transitions = append([]TLCTransition(nil), (*tlcResponse.Transitions)...)
	execution.ProvenanceAvailable = true
	return execution, nil
}
