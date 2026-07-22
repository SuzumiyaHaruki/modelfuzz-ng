// Package llm 定义与具体厂商无关的 JSON 补全接口。
package llm

import "context"

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type Options struct {
	Thinking    bool
	Temperature float64
	MaxTokens   int
	// Purpose 用于把初始化和变异的调用成本分开统计，不发送给 provider。
	Purpose string
}

type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type Completion struct {
	Content      []byte
	Model        string
	FinishReason string
	Usage        Usage
}

// JSONClient 返回一个 JSON 对象。调用方仍必须按自己的 schema 严格解析和校验。
type JSONClient interface {
	CompleteJSON(ctx context.Context, messages []Message, options Options) (Completion, error)
}
