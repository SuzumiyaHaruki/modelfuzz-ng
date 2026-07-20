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
	endpoint   string
	httpClient *http.Client
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
	parsed.Path = strings.TrimRight(parsed.Path, "/") + "/execute"
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return &Client{endpoint: parsed.String(), httpClient: httpClient}, nil
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
	defer response.Body.Close()
	limited := io.LimitReader(response.Body, maxResponseBytes+1)
	responseBody, err := io.ReadAll(limited)
	if err != nil {
		return nil, fmt.Errorf("read TLC response: %w", err)
	}
	if int64(len(responseBody)) > maxResponseBytes {
		return nil, fmt.Errorf("%w: body exceeds %d bytes", ErrInvalidResponse, maxResponseBytes)
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
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
