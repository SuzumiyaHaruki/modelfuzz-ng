package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// TLCResponse 是 TLC server 返回的状态序列和对应 hash key。
type TLCResponse struct {
	States []string
	Keys   []int64
}

// TLCClient 负责把 event trace 发送给本地/远端 TLC server。
//
// TLC server 暴露 /execute 接口，接收 JSON 事件序列，按 TLA+ 模型执行这些事件，
// 返回经过的状态字符串和状态 key。Fuzzer 不直接理解 TLA+，只消费这里返回的 State。
type TLCClient struct {
	ClientAddr string
}

func NewTLCClient(addr string) *TLCClient {
	return &TLCClient{
		ClientAddr: addr,
	}
}

func (c *TLCClient) SendTrace(trace *List[*Event]) ([]State, error) {
	// Reset 事件通知 TLC server 从模型初始状态开始执行这条 trace。
	// 注意这里会修改传入的 trace：追加一个 Reset 事件。
	trace.Append(&Event{Reset: true})
	data, err := json.Marshal(trace)
	if err != nil {
		return []State{}, fmt.Errorf("error marshalling json: %s", err)
	}
	res, err := http.Post("http://"+c.ClientAddr+"/execute", "application/json", bytes.NewBuffer(data))
	if err != nil {
		return []State{}, fmt.Errorf("error sending trace to tlc: %s", err)
	}
	defer res.Body.Close()
	resData, err := io.ReadAll(res.Body)
	if err != nil {
		return []State{}, fmt.Errorf("error reading response from tlc: %s", err)
	}
	tlcResponse := &TLCResponse{}
	if err = json.Unmarshal(resData, tlcResponse); err != nil {
		return []State{}, fmt.Errorf("error parsing tlc response: %s", err)
	}
	result := make([]State, len(tlcResponse.States))
	for i, s := range tlcResponse.States {
		result[i] = State{Repr: s, Key: tlcResponse.Keys[i]}
	}
	return result, nil
}
