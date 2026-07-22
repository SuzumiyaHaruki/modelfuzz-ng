// Package tlc 提供与 ModelFuzz controlled TLC HTTP 服务通信的客户端。
package tlc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/SuzumiyaHaruki/modelfuzz-ng/internal/model"
)

const maxResponseBytes int64 = 16 << 20

var (
	ErrInvalidAddress  = errors.New("invalid TLC address")
	ErrInvalidResponse = errors.New("invalid TLC response")
)

// Client 可以被一个执行序列复用，但每次 Execute 都会在请求末尾追加 Reset，
// 因而不同调用不会共享 TLC 模型状态。
type Client struct {
	endpoint        string
	metricsEndpoint string
	healthEndpoint  string
	httpClient      *http.Client
}

func NewClient(address string) (*Client, error) {
	return NewClientWithHTTPClient(address, &http.Client{Timeout: 30 * time.Second})
}

// NewClientWithHTTPClient 允许测试或上层系统注入自定义 transport 和超时策略。
func NewClientWithHTTPClient(address string, httpClient *http.Client) (*Client, error) {
	if httpClient == nil {
		return nil, fmt.Errorf("HTTP client must not be nil")
	}
	address = strings.TrimSpace(address)
	if address == "" {
		return nil, fmt.Errorf("%w: address must not be empty", ErrInvalidAddress)
	}
	if !strings.Contains(address, "://") {
		address = "http://" + address
	}
	parsed, err := url.Parse(address)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil, fmt.Errorf("%w: %q", ErrInvalidAddress, address)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, fmt.Errorf("%w: unsupported scheme %q", ErrInvalidAddress, parsed.Scheme)
	}
	basePath := strings.TrimRight(parsed.Path, "/")
	parsed.Path = basePath + "/execute"
	parsed.RawQuery = ""
	parsed.Fragment = ""
	executeEndpoint := parsed.String()
	parsed.Path = basePath + "/metrics"
	metricsEndpoint := parsed.String()
	parsed.Path = basePath + "/health"
	return &Client{
		endpoint: executeEndpoint, metricsEndpoint: metricsEndpoint,
		healthEndpoint: parsed.String(), httpClient: httpClient,
	}, nil
}

type ServerBounds struct {
	MaxLogIndex uint64   `json:"max_log_index"`
	LargestTerm uint64   `json:"largest_term"`
	ServerIDs   []uint64 `json:"server_ids,omitempty"`
	MaxValue    *uint64  `json:"max_value,omitempty"`
	NilValue    *int64   `json:"nil_value,omitempty"`
}

// Bounds 读取严格 TLC 服务从实际 cfg 加载的模型边界。旧服务未暴露边界时
// 返回 ErrInvalidResponse，调用方可选择只发出兼容性警告。
func (c *Client) Bounds(ctx context.Context) (ServerBounds, error) {
	if c == nil || c.httpClient == nil || c.healthEndpoint == "" {
		return ServerBounds{}, fmt.Errorf("TLC client is not initialized")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.healthEndpoint, nil)
	if err != nil {
		return ServerBounds{}, fmt.Errorf("create TLC health request: %w", err)
	}
	response, err := c.httpClient.Do(request)
	if err != nil {
		return ServerBounds{}, fmt.Errorf("read TLC health: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return ServerBounds{}, fmt.Errorf("TLC health returned %s", response.Status)
	}
	var result ServerBounds
	decoder := json.NewDecoder(io.LimitReader(response.Body, maxResponseBytes+1))
	if err := decoder.Decode(&result); err != nil {
		return ServerBounds{}, fmt.Errorf("decode TLC health: %w", err)
	}
	if result.MaxLogIndex == 0 || result.LargestTerm == 0 {
		return ServerBounds{}, fmt.Errorf("%w: TLC health does not expose model bounds", ErrInvalidResponse)
	}
	return result, nil
}

// ServerMetrics 是严格 TLC 服务的累计统计快照。Timing 中所有值均为纳秒。
type ServerMetrics struct {
	Requests      int64            `json:"requests"`
	Succeeded     int64            `json:"succeeded"`
	Failed        int64            `json:"failed"`
	ModelEvents   int64            `json:"model_events"`
	ActionLookups int64            `json:"action_lookups"`
	ErrorsByCode  map[string]int64 `json:"errors_by_code"`
	Timing        struct {
		MappingNanos       int64 `json:"mapping_nanos"`
		ActionLookupNanos  int64 `json:"action_lookup_nanos"`
		SuccessorNanos     int64 `json:"successor_nanos"`
		ValidationNanos    int64 `json:"validation_nanos"`
		SerializationNanos int64 `json:"serialization_nanos"`
	} `json:"timing"`
}

// Metrics 读取服务累计性能统计。旧 controlled TLC 没有该端点时会返回错误，
// 调用方可以将它视为可选能力而不影响模型执行。
func (c *Client) Metrics(ctx context.Context) (ServerMetrics, error) {
	if c == nil || c.httpClient == nil || c.metricsEndpoint == "" {
		return ServerMetrics{}, fmt.Errorf("TLC client is not initialized")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.metricsEndpoint, nil)
	if err != nil {
		return ServerMetrics{}, fmt.Errorf("create TLC metrics request: %w", err)
	}
	response, err := c.httpClient.Do(request)
	if err != nil {
		return ServerMetrics{}, fmt.Errorf("read TLC metrics: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return ServerMetrics{}, fmt.Errorf("TLC metrics returned %s", response.Status)
	}
	var result ServerMetrics
	decoder := json.NewDecoder(io.LimitReader(response.Body, maxResponseBytes+1))
	if err := decoder.Decode(&result); err != nil {
		return ServerMetrics{}, fmt.Errorf("decode TLC metrics: %w", err)
	}
	return result, nil
}

// Execute 从模型初始状态依次执行 events。末尾 Reset 只用于通知服务释放本次
// trace 的状态，不会出现在返回状态中，也不会修改调用方传入的切片。
func (c *Client) Execute(ctx context.Context, events []model.Event) ([]State, error) {
	if c == nil || c.httpClient == nil || c.endpoint == "" {
		return nil, fmt.Errorf("TLC client is not initialized")
	}
	requestEvents := make([]model.Event, 0, len(events)+1)
	for i, event := range events {
		if err := event.Validate(); err != nil {
			return nil, fmt.Errorf("event %d: %w", i, err)
		}
		if event.Reset {
			return nil, fmt.Errorf("event %d: reset is managed by TLC client", i)
		}
		requestEvents = append(requestEvents, event.Copy())
	}
	requestEvents = append(requestEvents, model.ResetEvent())

	body, err := json.Marshal(requestEvents)
	if err != nil {
		return nil, fmt.Errorf("encode TLC request: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create TLC request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")

	response, err := c.httpClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("execute TLC trace: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	limited := io.LimitReader(response.Body, maxResponseBytes+1)
	responseBody, err := io.ReadAll(limited)
	if err != nil {
		return nil, fmt.Errorf("read TLC response: %w", err)
	}
	if int64(len(responseBody)) > maxResponseBytes {
		return nil, fmt.Errorf("%w: body exceeds %d bytes", ErrInvalidResponse, maxResponseBytes)
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		var decodedError executeErrorResponse
		if json.Unmarshal(responseBody, &decodedError) == nil && decodedError.Error.Code != "" {
			decodedError.Error.StatusCode = response.StatusCode
			return nil, &decodedError.Error
		}
		return nil, fmt.Errorf("TLC returned %s: %s", response.Status, strings.TrimSpace(string(responseBody)))
	}

	var decoded executeResponse
	if err := json.Unmarshal(responseBody, &decoded); err != nil {
		return nil, fmt.Errorf("%w: decode JSON: %v", ErrInvalidResponse, err)
	}
	if len(decoded.States) != len(decoded.Keys) {
		return nil, fmt.Errorf("%w: got %d states but %d keys", ErrInvalidResponse, len(decoded.States), len(decoded.Keys))
	}
	states := make([]State, len(decoded.States))
	for i := range decoded.States {
		states[i] = State{Text: decoded.States[i], Key: decoded.Keys[i]}
	}
	return states, nil
}
