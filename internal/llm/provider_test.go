package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestProviderPresets(t *testing.T) {
	tests := []struct {
		provider Provider
		model    string
		keyEnv   string
	}{
		{ProviderDeepSeek, DefaultDeepSeekModel, "DEEPSEEK_API_KEY"},
		{ProviderGLM, DefaultGLMModel, "ZHIPUAI_API_KEY"},
		{ProviderQwen, DefaultQwenModel, "DASHSCOPE_API_KEY"},
		{ProviderKimi, DefaultKimiModel, "MOONSHOT_API_KEY"},
	}
	for _, test := range tests {
		preset, err := Preset(test.provider)
		if err != nil {
			t.Fatal(err)
		}
		if preset.DefaultModel != test.model || preset.DefaultAPIKeyEnv != test.keyEnv || preset.DefaultBaseURL == "" {
			t.Fatalf("preset %s = %+v", test.provider, preset)
		}
	}
	if _, err := ParseProvider("unknown"); err == nil {
		t.Fatal("未知 provider 被接受")
	}
}

func TestClientUsesProviderSpecificThinkingFields(t *testing.T) {
	tests := []struct {
		provider Provider
		model    string
		check    func(*testing.T, map[string]any)
	}{
		{ProviderDeepSeek, DefaultDeepSeekModel, checkThinkingObject},
		{ProviderGLM, DefaultGLMModel, checkThinkingObject},
		{ProviderQwen, DefaultQwenModel, func(t *testing.T, body map[string]any) {
			if enabled, ok := body["enable_thinking"].(bool); !ok || !enabled {
				t.Fatalf("Qwen enable_thinking = %T(%v)", body["enable_thinking"], body["enable_thinking"])
			}
		}},
		{ProviderKimi, DefaultKimiModel, checkThinkingObject},
	}
	for _, test := range tests {
		t.Run(string(test.provider), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(output http.ResponseWriter, request *http.Request) {
				if request.URL.Path != "/chat/completions" || request.Header.Get("Authorization") != "Bearer secret" {
					t.Fatalf("request path/auth = %s/%q", request.URL.Path, request.Header.Get("Authorization"))
				}
				var body map[string]any
				if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
					t.Fatal(err)
				}
				if body["model"] != test.model || body["response_format"].(map[string]any)["type"] != "json_object" {
					t.Fatalf("request body = %+v", body)
				}
				if test.provider == ProviderKimi {
					if body["max_completion_tokens"] == nil || body["max_tokens"] != nil {
						t.Fatalf("Kimi token limit fields = %+v", body)
					}
				} else if body["max_tokens"] == nil {
					t.Fatalf("%s max_tokens missing: %+v", test.provider, body)
				}
				test.check(t, body)
				output.Header().Set("Content-Type", "application/json")
				_, _ = output.Write([]byte(`{"model":"test","choices":[{"finish_reason":"stop","message":{"content":"{\"plans\":[]}"}}],"usage":{"prompt_tokens":10,"completion_tokens":3,"total_tokens":13}}`))
			}))
			defer server.Close()
			client, err := NewClient(Config{
				Provider: test.provider, BaseURL: server.URL, APIKey: "secret", Model: test.model, Timeout: time.Second,
			})
			if err != nil {
				t.Fatal(err)
			}
			completion, err := client.CompleteJSON(context.Background(), []Message{{Role: "user", Content: "return JSON"}}, Options{Thinking: true, Purpose: "initial"})
			if err != nil {
				t.Fatal(err)
			}
			if string(completion.Content) != `{"plans":[]}` || completion.Usage.TotalTokens != 13 || client.Provider() != test.provider {
				t.Fatalf("completion/client = %+v/%+v", completion, client)
			}
			if stats := client.Stats(); stats.Calls != 1 || stats.Failures != 0 || stats.TotalTokens != 13 ||
				stats.ByPurpose["initial"].TotalTokens != 13 {
				t.Fatalf("stats = %+v", stats)
			}
		})
	}
}

func TestKimiK3UsesReasoningEffort(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(output http.ResponseWriter, request *http.Request) {
		calls++
		var body map[string]any
		_ = json.NewDecoder(request.Body).Decode(&body)
		want := "max"
		if calls == 2 {
			want = "low"
		}
		if body["reasoning_effort"] != want || body["thinking"] != nil {
			t.Fatalf("Kimi K3 thinking body = %+v", body)
		}
		_, _ = output.Write([]byte(`{"choices":[{"finish_reason":"stop","message":{"content":"{}"}}]}`))
	}))
	defer server.Close()
	client, _ := NewClient(Config{Provider: ProviderKimi, BaseURL: server.URL, APIKey: "secret", Model: "kimi-k3"})
	if _, err := client.CompleteJSON(context.Background(), []Message{{Role: "user", Content: "JSON"}}, Options{Thinking: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.CompleteJSON(context.Background(), []Message{{Role: "user", Content: "JSON"}}, Options{Thinking: false}); err != nil {
		t.Fatal(err)
	}
}

func TestClientRejectsHTTPErrorAndUnsafeURL(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(output http.ResponseWriter, _ *http.Request) {
		http.Error(output, "quota exceeded", http.StatusPaymentRequired)
	}))
	defer server.Close()
	client, _ := NewClient(Config{Provider: ProviderDeepSeek, BaseURL: server.URL, APIKey: "secret"})
	if _, err := client.CompleteJSON(context.Background(), []Message{{Role: "user", Content: "json"}}, Options{}); err == nil {
		t.Fatal("HTTP error unexpectedly succeeded")
	}
	if stats := client.Stats(); stats.Calls != 1 || stats.Failures != 1 || stats.TotalTokens != 0 {
		t.Fatalf("stats = %+v", stats)
	}
	if _, err := NewClient(Config{Provider: ProviderDeepSeek, BaseURL: "https://secret@example.com", APIKey: "placeholder"}); err == nil {
		t.Fatal("credential-bearing base URL unexpectedly succeeded")
	}
}

func TestClientCountsUsageFromTruncatedCompletion(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(output http.ResponseWriter, _ *http.Request) {
		_, _ = output.Write([]byte(`{
          "model":"deepseek-v4-flash",
          "choices":[{"finish_reason":"length","message":{"content":"{}"}}],
          "usage":{"prompt_tokens":11,"completion_tokens":7,"total_tokens":18}
        }`))
	}))
	defer server.Close()
	client, err := NewClient(Config{Provider: ProviderDeepSeek, BaseURL: server.URL, APIKey: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	completion, err := client.CompleteJSON(context.Background(),
		[]Message{{Role: "user", Content: "JSON"}}, Options{Purpose: "mutation"})
	if err == nil || completion.Usage.TotalTokens != 18 {
		t.Fatalf("completion/error = %+v/%v", completion, err)
	}
	stats := client.Stats()
	if stats.Calls != 1 || stats.Failures != 1 || stats.TotalTokens != 18 ||
		stats.ByPurpose["mutation"].Failures != 1 || stats.ByPurpose["mutation"].TotalTokens != 18 {
		t.Fatalf("stats = %+v", stats)
	}
}

func TestStatsAddPreservesPurposeBreakdown(t *testing.T) {
	first := Stats{CallStats: CallStats{Calls: 1, TotalTokens: 10},
		ByPurpose: map[string]CallStats{"initial": {Calls: 1, TotalTokens: 10}}}
	second := Stats{CallStats: CallStats{Calls: 2, Failures: 1, TotalTokens: 20},
		ByPurpose: map[string]CallStats{"mutation": {Calls: 2, Failures: 1, TotalTokens: 20}}}
	combined := first.Add(second)
	if combined.Calls != 3 || combined.Failures != 1 || combined.TotalTokens != 30 ||
		combined.ByPurpose["initial"].Calls != 1 || combined.ByPurpose["mutation"].Calls != 2 {
		t.Fatalf("combined stats = %+v", combined)
	}
	combined.ByPurpose["initial"] = CallStats{}
	if first.ByPurpose["initial"].Calls != 1 {
		t.Fatal("Stats.Add reused input map")
	}
}

func checkThinkingObject(t *testing.T, body map[string]any) {
	t.Helper()
	thinking, ok := body["thinking"].(map[string]any)
	if !ok || thinking["type"] != "enabled" {
		t.Fatalf("thinking = %T(%v)", body["thinking"], body["thinking"])
	}
}
