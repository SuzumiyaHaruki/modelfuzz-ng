package modelfuzz

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// TLCResponse 描述 TLC server 对一次 event trace 执行请求的原始 JSON 响应。
//
// 在 ModelFuzz 的整体流程中，TLCClient 位于 Guider 和外部 TLC 控制服务之间。Guider 把
// Fuzzer 产生的 event trace 交给 TLCClient；TLCClient 通过 HTTP 发送给 TLC server；
// server 按 TLA+ 模型执行这些事件后，返回一条模型状态序列。TLCResponse 就是这条 HTTP
// 响应在 Go 侧的反序列化结构。
//
// States 和 Keys 是并行数组：States[i] 是第 i 个状态的人类可读表示，Keys[i] 是同一个
// 状态的稳定去重 key。Guider 主要用 key 做覆盖统计，用 repr 做记录和调试。
type TLCResponse struct {
	// States 是 TLC 返回的模型状态字符串表示，适合落盘和人工查看。
	States []string

	// Keys 是与 States 一一对应的状态唯一标识，用于 Guider 做状态去重。
	Keys []int64
}

// TLCClient 封装与 TLC server 通信的 HTTP 客户端逻辑。
//
// 它本身不维护覆盖信息，也不理解 Fuzzer 的调度选择；它只做一件事：把 event trace 发送
// 到 TLC 的 /execute 接口，并把响应转换成 ModelFuzz 内部使用的 []State。覆盖统计、trace
// hash、状态路径去重等逻辑都在 TLCStateGuider 中完成。
type TLCClient struct {
	// ClientAddr 是 TLC server 的地址，格式通常为 "host:port"，例如 "127.0.0.1:8080"。
	ClientAddr string
}

// NewTLCClient 创建一个指向指定 TLC server 的客户端。
func NewTLCClient(addr string) *TLCClient {
	return &TLCClient{
		// 这里只保存地址；真正的 HTTP 请求在 SendTrace 中发起。
		ClientAddr: addr,
	}
}

// SendTrace 把一条 event trace 发送给 TLC server，并返回 TLC 执行后的模型状态序列。
//
// trace 参数是 Fuzzer/Cluster 已经记录好的模型事件序列，例如 SendMessage、
// DeliverMessage、Add/Remove，以及 Cluster 通过 FuzzContext.AddEvent 追加的协议事件。
// TLC server 收到这些事件后，会在 TLA+ 模型里按顺序执行，并返回每一步到达的模型状态。
//
// 需要注意：本函数会在传入的 trace 末尾追加一个 Reset 事件。这是一个就地修改，会影响
// 调用方持有的同一个 eventTrace 对象。当前设计中 Check 之后这条 eventTrace 通常不再被
// 继续执行使用，因此这个副作用可以接受；如果未来需要多次发送同一条 trace，应考虑先拷贝。
func (c *TLCClient) SendTrace(trace *List[*Event]) ([]State, error) {
	// 在事件序列末尾追加 Reset，通知 TLC server 一次 trace 执行结束后重置模型执行上下文。
	trace.Append(&Event{Reset: true})
	// 将 event trace 序列化为 JSON，作为 /execute 请求体。
	data, err := json.Marshal(trace)
	if err != nil {
		// 序列化失败通常意味着 Event.Params 里包含不可 JSON 编码的对象。
		return []State{}, fmt.Errorf("error marshalling json: %s", err)
	}
	// 发送 HTTP POST 到 TLC server 的执行接口。content-type 使用 application/json。
	res, err := http.Post("http://"+c.ClientAddr+"/execute", "application/json", bytes.NewBuffer(data))
	if err != nil {
		// 网络不可达、server 未启动、地址错误等都会走到这里。
		return []State{}, fmt.Errorf("error sending trace to tlc: %s", err)
	}
	// 确保响应体最终关闭，避免长时间 fuzzing 时泄漏连接资源。
	defer res.Body.Close()
	// 读取完整响应体；TLCResponse 的 JSON 解析在下一步进行。
	resData, err := io.ReadAll(res.Body)
	if err != nil {
		// 响应体读取失败时，Guider 无法获得模型反馈。
		return []State{}, fmt.Errorf("error reading response from tlc: %s", err)
	}
	// 将 TLC server 返回的 JSON 解析成原始响应结构。
	tlcResponse := &TLCResponse{}
	if err = json.Unmarshal(resData, tlcResponse); err != nil {
		// 如果 server 返回非预期 JSON，说明 TLC 控制服务和客户端协议不匹配或执行失败。
		return []State{}, fmt.Errorf("error parsing tlc response: %s", err)
	}
	// 把并行数组 States/Keys 合并成 ModelFuzz 内部统一使用的 State 切片。
	result := make([]State, len(tlcResponse.States))
	for i, s := range tlcResponse.States {
		// Repr 用于记录和展示，Key 用于 Guider 的状态覆盖去重。
		result[i] = State{Repr: s, Key: tlcResponse.Keys[i]}
	}
	// 返回给 TLCStateGuider，由 Guider 更新 statesMap 和 stateTracesMap。
	return result, nil
}
