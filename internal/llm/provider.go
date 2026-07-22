package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

type Provider string

const (
	ProviderDeepSeek Provider = "deepseek"
	ProviderGLM      Provider = "glm"
	ProviderQwen     Provider = "qwen"
	ProviderKimi     Provider = "kimi"

	DefaultProvider = ProviderDeepSeek
)

const (
	DefaultDeepSeekBaseURL = "https://api.deepseek.com"
	DefaultDeepSeekModel   = "deepseek-v4-flash"
	DefaultGLMBaseURL      = "https://open.bigmodel.cn/api/paas/v4"
	DefaultGLMModel        = "glm-5.2"
	DefaultQwenBaseURL     = "https://dashscope.aliyuncs.com/compatible-mode/v1"
	DefaultQwenModel       = "qwen-plus"
	DefaultKimiBaseURL     = "https://api.moonshot.cn/v1"
	DefaultKimiModel       = "kimi-k2.6"
)

type thinkingStyle uint8

const (
	thinkingObject thinkingStyle = iota
	thinkingBoolean
)

// ProviderPreset 保存厂商的非敏感默认值。BaseURL 和模型都可以由 CLI 覆盖，
// API Key 只通过环境变量名称间接引用，不保存在实验配置中。
type ProviderPreset struct {
	Provider         Provider `json:"provider"`
	DefaultBaseURL   string   `json:"default_base_url"`
	DefaultModel     string   `json:"default_model"`
	DefaultAPIKeyEnv string   `json:"default_api_key_env"`
	thinking         thinkingStyle
}

var providerPresets = map[Provider]ProviderPreset{
	ProviderDeepSeek: {
		Provider: ProviderDeepSeek, DefaultBaseURL: DefaultDeepSeekBaseURL,
		DefaultModel: DefaultDeepSeekModel, DefaultAPIKeyEnv: "DEEPSEEK_API_KEY", thinking: thinkingObject,
	},
	ProviderGLM: {
		Provider: ProviderGLM, DefaultBaseURL: DefaultGLMBaseURL,
		DefaultModel: DefaultGLMModel, DefaultAPIKeyEnv: "ZHIPUAI_API_KEY", thinking: thinkingObject,
	},
	ProviderQwen: {
		Provider: ProviderQwen, DefaultBaseURL: DefaultQwenBaseURL,
		DefaultModel: DefaultQwenModel, DefaultAPIKeyEnv: "DASHSCOPE_API_KEY", thinking: thinkingBoolean,
	},
	ProviderKimi: {
		Provider: ProviderKimi, DefaultBaseURL: DefaultKimiBaseURL,
		DefaultModel: DefaultKimiModel, DefaultAPIKeyEnv: "MOONSHOT_API_KEY", thinking: thinkingObject,
	},
}

func ParseProvider(value string) (Provider, error) {
	provider := Provider(strings.ToLower(strings.TrimSpace(value)))
	if _, exists := providerPresets[provider]; !exists {
		return "", fmt.Errorf("unsupported LLM provider %q; supported providers: deepseek, glm, qwen, kimi", value)
	}
	return provider, nil
}

func Preset(provider Provider) (ProviderPreset, error) {
	preset, exists := providerPresets[provider]
	if !exists {
		return ProviderPreset{}, fmt.Errorf("unsupported LLM provider %q", provider)
	}
	return preset, nil
}

type Config struct {
	Provider Provider
	BaseURL  string
	APIKey   string
	Model    string
	Timeout  time.Duration
	Client   *http.Client
	MaxBytes int64
}

// Client 实现四家 OpenAI-compatible Chat Completions API 的公共部分，只在
// 思考开关等确有差异的字段上按 provider 编码。
type Client struct {
	provider Provider
	endpoint string
	apiKey   string
	model    string
	client   *http.Client
	maxBytes int64
	thinking thinkingStyle
	mutex    sync.Mutex
	stats    Stats
}

// CallStats 记录一类 LLM 请求的累计成本，不包含 prompt、响应或 API Key。
type CallStats struct {
	Calls            int   `json:"calls"`
	Failures         int   `json:"failures"`
	DurationMillis   int64 `json:"duration_millis"`
	PromptTokens     int   `json:"prompt_tokens"`
	CompletionTokens int   `json:"completion_tokens"`
	TotalTokens      int   `json:"total_tokens"`
}

// Stats 用于比较 LLM 带来的覆盖收益与时间/token 成本。ByPurpose 当前主要
// 区分 initial 和 mutation；空 Purpose 归入 unspecified。
type Stats struct {
	CallStats
	ByPurpose map[string]CallStats `json:"by_purpose,omitempty"`
}

func NewClient(config Config) (*Client, error) {
	if config.Provider == "" {
		config.Provider = DefaultProvider
	}
	preset, err := Preset(config.Provider)
	if err != nil {
		return nil, err
	}
	if config.BaseURL == "" {
		config.BaseURL = preset.DefaultBaseURL
	}
	parsed, err := url.Parse(config.BaseURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil, fmt.Errorf("invalid %s base URL %q", config.Provider, config.BaseURL)
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, fmt.Errorf("LLM base URL must not contain credentials, query, or fragment")
	}
	if parsed.Scheme != "https" && parsed.Hostname() != "127.0.0.1" && parsed.Hostname() != "localhost" && parsed.Hostname() != "::1" {
		return nil, fmt.Errorf("LLM base URL must use HTTPS except for a loopback test server")
	}
	config.APIKey = strings.TrimSpace(config.APIKey)
	if config.APIKey == "" {
		return nil, fmt.Errorf("%s API key must not be empty", config.Provider)
	}
	if config.Model == "" {
		config.Model = preset.DefaultModel
	}
	config.Model = strings.TrimSpace(config.Model)
	if config.Model == "" {
		return nil, fmt.Errorf("LLM model must not be empty")
	}
	if config.Timeout <= 0 {
		config.Timeout = 90 * time.Second
	}
	if config.Client == nil {
		config.Client = &http.Client{Timeout: config.Timeout}
	}
	if config.MaxBytes <= 0 {
		config.MaxBytes = 4 << 20
	}
	return &Client{
		provider: config.Provider,
		endpoint: strings.TrimRight(config.BaseURL, "/") + "/chat/completions",
		apiKey:   config.APIKey, model: config.Model, client: config.Client,
		maxBytes: config.MaxBytes, thinking: preset.thinking,
	}, nil
}

func (c *Client) Provider() Provider {
	if c == nil {
		return ""
	}
	return c.provider
}

func (c *Client) CompleteJSON(ctx context.Context, messages []Message, options Options) (completion Completion, completionErr error) {
	if c == nil {
		return Completion{}, fmt.Errorf("LLM client is nil")
	}
	started := time.Now()
	defer func() { c.record(options.Purpose, completion, completionErr, time.Since(started)) }()
	if len(messages) == 0 {
		return Completion{}, fmt.Errorf("LLM request needs at least one message")
	}
	if options.MaxTokens <= 0 {
		options.MaxTokens = 4096
	}
	body := map[string]any{
		"model": c.model, "messages": messages, "temperature": options.Temperature,
		"response_format": map[string]any{"type": "json_object"},
	}
	if c.provider == ProviderKimi {
		body["max_completion_tokens"] = options.MaxTokens
	} else {
		body["max_tokens"] = options.MaxTokens
	}
	switch c.thinking {
	case thinkingBoolean:
		body["enable_thinking"] = options.Thinking
	default:
		thinking := "disabled"
		if options.Thinking {
			thinking = "enabled"
		}
		body["thinking"] = map[string]any{"type": thinking}
	}
	// Kimi K3 始终推理，不能通过 thinking.type 关闭。用户显式选择 K3 时，
	// 初始化使用 max，时延敏感的变异降为 low。
	if c.provider == ProviderKimi && strings.HasPrefix(strings.ToLower(c.model), "kimi-k3") {
		delete(body, "thinking")
		body["reasoning_effort"] = "low"
		if options.Thinking {
			body["reasoning_effort"] = "max"
		}
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		return Completion{}, fmt.Errorf("encode %s request: %w", c.provider, err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(encoded))
	if err != nil {
		return Completion{}, fmt.Errorf("create %s request: %w", c.provider, err)
	}
	request.Header.Set("Authorization", "Bearer "+c.apiKey)
	request.Header.Set("Content-Type", "application/json")
	response, err := c.client.Do(request)
	if err != nil {
		return Completion{}, fmt.Errorf("call %s: %w", c.provider, err)
	}
	defer func() { _ = response.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(response.Body, c.maxBytes+1))
	if err != nil {
		return Completion{}, fmt.Errorf("read %s response: %w", c.provider, err)
	}
	if int64(len(data)) > c.maxBytes {
		return Completion{}, fmt.Errorf("%s response exceeds %d bytes", c.provider, c.maxBytes)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return Completion{}, fmt.Errorf("%s returned HTTP %d: %s", c.provider, response.StatusCode, truncate(string(data), 512))
	}
	var decoded struct {
		Model   string `json:"model"`
		Choices []struct {
			FinishReason string `json:"finish_reason"`
			Message      struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Usage Usage `json:"usage"`
	}
	if err := json.Unmarshal(data, &decoded); err != nil {
		return Completion{}, fmt.Errorf("decode %s response: %w", c.provider, err)
	}
	completion.Model, completion.Usage = decoded.Model, decoded.Usage
	if len(decoded.Choices) == 0 || decoded.Choices[0].Message.Content == "" {
		return completion, fmt.Errorf("%s response contains no JSON content", c.provider)
	}
	choice := decoded.Choices[0]
	completion.Content, completion.FinishReason = []byte(choice.Message.Content), choice.FinishReason
	switch choice.FinishReason {
	case "", "stop":
		return completion, nil
	case "length":
		return completion, fmt.Errorf("%s JSON was truncated at the configured token limit", c.provider)
	default:
		return completion, fmt.Errorf("%s JSON generation stopped with finish reason %q", c.provider, choice.FinishReason)
	}
}

func (c *Client) Stats() Stats {
	if c == nil {
		return Stats{}
	}
	c.mutex.Lock()
	defer c.mutex.Unlock()
	return c.stats.Copy()
}

func (c *Client) record(purpose string, completion Completion, err error, duration time.Duration) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	if purpose == "" {
		purpose = "unspecified"
	}
	delta := CallStats{
		Calls: 1, DurationMillis: duration.Milliseconds(),
		PromptTokens: completion.Usage.PromptTokens, CompletionTokens: completion.Usage.CompletionTokens,
		TotalTokens: completion.Usage.TotalTokens,
	}
	if err != nil {
		delta.Failures = 1
	}
	c.stats.CallStats = addCallStats(c.stats.CallStats, delta)
	if c.stats.ByPurpose == nil {
		c.stats.ByPurpose = make(map[string]CallStats)
	}
	c.stats.ByPurpose[purpose] = addCallStats(c.stats.ByPurpose[purpose], delta)
}

func addCallStats(left, right CallStats) CallStats {
	return CallStats{
		Calls: left.Calls + right.Calls, Failures: left.Failures + right.Failures,
		DurationMillis:   left.DurationMillis + right.DurationMillis,
		PromptTokens:     left.PromptTokens + right.PromptTokens,
		CompletionTokens: left.CompletionTokens + right.CompletionTokens,
		TotalTokens:      left.TotalTokens + right.TotalTokens,
	}
}

// Add 合并多段进程生命周期中的统计，用于 checkpoint 恢复后继续累计。
func (s Stats) Add(other Stats) Stats {
	result := Stats{CallStats: addCallStats(s.CallStats, other.CallStats)}
	if len(s.ByPurpose)+len(other.ByPurpose) > 0 {
		result.ByPurpose = make(map[string]CallStats, len(s.ByPurpose)+len(other.ByPurpose))
		for purpose, value := range s.ByPurpose {
			result.ByPurpose[purpose] = value
		}
		for purpose, value := range other.ByPurpose {
			result.ByPurpose[purpose] = addCallStats(result.ByPurpose[purpose], value)
		}
	}
	return result
}

// Copy 避免调用方修改 Client 内部的按用途统计 map。
func (s Stats) Copy() Stats {
	return Stats{}.Add(s)
}

func truncate(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	return value[:limit] + "..."
}
