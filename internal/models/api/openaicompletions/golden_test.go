package openaicompletions

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/api"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newClient(t *testing.T, mutate func(*Config)) *Client {
	t.Helper()
	cfg := Config{
		Endpoint: api.Endpoint{
			BaseURL: "https://example.com/v1", Model: "m", ModelID: "id", Auth: api.BearerAuth("k"),
		},
		Settings:       api.DefaultOpenAICompletions(),
		ThinkingLevels: api.ThinkingLevelMap{},
	}
	if mutate != nil {
		mutate(&cfg)
	}
	return New(cfg)
}

func bodyJSON(t *testing.T, c *Client, msgs []api.Message, opts *api.Options, stream bool) map[string]any {
	t.Helper()
	body, err := c.BuildRequestBody(msgs, opts, stream)
	require.NoError(t, err)
	data, err := json.Marshal(body)
	require.NoError(t, err)
	var out map[string]any
	require.NoError(t, json.Unmarshal(data, &out))
	return out
}

func ptrBool(b bool) *bool { return &b }

var conversation = []api.Message{
	{Role: "system", Content: "You are helpful."},
	{Role: "user", Content: "hi"},
	{Role: "assistant", ReasoningContent: "thinking...", ToolCalls: []api.ToolCall{{
		ID: "call_1", Type: "function", Function: api.FunctionCall{Name: "search", Arguments: `{"q":"x"}`},
	}}},
	{Role: "tool", ToolCallID: "call_1", Name: "search", Content: "result"},
}

var toolOpts = &api.Options{
	Temperature: 0.7, MaxTokens: 1000,
	Tools: []api.Tool{{Type: "function", Function: api.FunctionDef{
		Name: "search", Description: "d", Parameters: json.RawMessage(`{"type":"object","properties":{}}`),
	}}},
	ToolChoice: "required", ParallelToolCalls: ptrBool(true),
}

// TestGolden pins the exact wire body per vendor dialect. Each case is one
// row of the vendor fact table; changing a vendor's behaviour means changing
// the expectation here on purpose.
func TestGolden(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Config)
		opts   *api.Options
		stream bool
		want   map[string]any // keys asserted; value nil means "must be absent"
	}{
		{
			name: "openai baseline: max_completion_tokens, store, developer role off for non-reasoning",
			mutate: func(c *Config) {
				c.Settings.SupportsStore = true
				c.Settings.SupportsDeveloperRole = true
				c.Settings.ThinkingFormat = api.ThinkingFormatOpenAI
				c.Settings.SupportsReasoningEffort = true
			},
			opts:   toolOpts,
			stream: true,
			want: map[string]any{
				"max_completion_tokens": float64(1000), "max_tokens": nil,
				"store": false, "stream": true, "stream_options": map[string]any{"include_usage": true},
				"temperature": 0.7, "tool_choice": "required", "parallel_tool_calls": true,
				"reasoning_effort": nil,
			},
		},
		{
			name: "openai reasoning model: developer role, no sampling, effort medium",
			mutate: func(c *Config) {
				c.Reasoning = true
				c.Settings.SupportsDeveloperRole = true
				c.Settings.SupportsTemperature = false
				c.Settings.ThinkingFormat = api.ThinkingFormatOpenAI
				c.Settings.SupportsReasoningEffort = true
			},
			opts: &api.Options{Temperature: 0.7, MaxTokens: 10, ReasoningEffort: api.ReasoningMedium},
			want: map[string]any{
				"temperature": nil, "reasoning_effort": "medium", "max_completion_tokens": float64(10),
			},
		},
		{
			name: "openai gpt-5.1 off maps to reasoning_effort none",
			mutate: func(c *Config) {
				c.Settings.ThinkingFormat = api.ThinkingFormatOpenAI
				c.Settings.SupportsReasoningEffort = true
				c.ThinkingLevels = api.ThinkingLevelMap{api.ReasoningOff: api.StringPtr("none")}
			},
			opts: &api.Options{Thinking: ptrBool(false)},
			want: map[string]any{"reasoning_effort": "none"},
		},
		{
			name: "deepseek: max_tokens, thinking.type + effort clamp, tool_choice required dropped",
			mutate: func(c *Config) {
				c.Settings.MaxTokensField = "max_tokens"
				c.Settings.ThinkingFormat = api.ThinkingFormatThinkingType
				c.Settings.SupportsReasoningEffort = true
				c.Settings.ToolChoiceModes = []string{"none", "auto"}
				c.ThinkingLevels = api.ThinkingLevelMap{api.ReasoningMedium: api.StringPtr("high")}
			},
			opts: func() *api.Options { o := *toolOpts; o.ReasoningEffort = api.ReasoningMedium; return &o }(),
			want: map[string]any{
				"max_tokens": float64(1000), "max_completion_tokens": nil,
				"thinking": map[string]any{"type": "enabled"}, "reasoning_effort": "high",
				"tool_choice": nil, "parallel_tool_calls": true,
			},
		},
		{
			name: "deepseek reasoner: always-on, off request sends nothing",
			mutate: func(c *Config) {
				c.Settings.ThinkingFormat = api.ThinkingFormatThinkingType
				c.ThinkingLevels = api.ThinkingLevelMap{api.ReasoningOff: nil}
			},
			opts: &api.Options{Thinking: ptrBool(false)},
			want: map[string]any{"thinking": nil},
		},
		{
			name: "zhipu: thinking disabled explicitly",
			mutate: func(c *Config) {
				c.Settings.ThinkingFormat = api.ThinkingFormatThinkingType
			},
			opts: &api.Options{Thinking: ptrBool(false)},
			want: map[string]any{"thinking": map[string]any{"type": "disabled"}},
		},
		{
			name: "minimax m3: adaptive thinking value",
			mutate: func(c *Config) {
				c.Settings.ThinkingFormat = api.ThinkingFormatThinkingType
				c.Settings.ThinkingEnabledValue = "adaptive"
			},
			opts: &api.Options{Thinking: ptrBool(true)},
			want: map[string]any{"thinking": map[string]any{"type": "adaptive"}, "reasoning_effort": nil},
		},
		{
			name: "dashscope hybrid: enable_thinking always sent, budget, non-stream forces off",
			mutate: func(c *Config) {
				c.Settings.MaxTokensField = "max_tokens"
				c.Settings.ThinkingFormat = api.ThinkingFormatEnableThinking
				c.Settings.ThinkingBudgetField = "thinking_budget"
				c.Settings.ThinkingAlwaysSend = true
				c.Settings.ThinkingDisableOnNonStream = true
			},
			opts:   &api.Options{Thinking: ptrBool(true), ThinkingBudgetTokens: 4096},
			stream: false,
			want:   map[string]any{"enable_thinking": false, "thinking_budget": nil},
		},
		{
			name: "dashscope qwen3.8: effort and budget are mutually exclusive, budget yields",
			mutate: func(c *Config) {
				c.Settings.ThinkingFormat = api.ThinkingFormatEnableThinking
				c.Settings.ThinkingBudgetField = "thinking_budget"
				c.Settings.ThinkingBudgetExcludesEffort = true
				c.Settings.SupportsReasoningEffort = true
				c.ThinkingLevels = api.ThinkingLevelMap{api.ReasoningMax: api.StringPtr("xhigh")}
			},
			opts: &api.Options{
				Thinking: ptrBool(true), ReasoningEffort: api.ReasoningMax, ThinkingBudgetTokens: 4096,
			},
			stream: true,
			want: map[string]any{
				"enable_thinking": true, "reasoning_effort": "xhigh", "thinking_budget": nil,
			},
		},
		{
			name: "dashscope qwen3.8: budget still rides along when no effort is graded",
			mutate: func(c *Config) {
				c.Settings.ThinkingFormat = api.ThinkingFormatEnableThinking
				c.Settings.ThinkingBudgetField = "thinking_budget"
				c.Settings.ThinkingBudgetExcludesEffort = true
			},
			opts:   &api.Options{Thinking: ptrBool(true), ThinkingBudgetTokens: 4096},
			stream: true,
			want: map[string]any{
				"enable_thinking": true, "reasoning_effort": nil, "thinking_budget": float64(4096),
			},
		},
		{
			name: "dashscope hybrid streaming: enable_thinking true with budget",
			mutate: func(c *Config) {
				c.Settings.ThinkingFormat = api.ThinkingFormatEnableThinking
				c.Settings.ThinkingBudgetField = "thinking_budget"
				c.Settings.ThinkingAlwaysSend = true
				c.Settings.ThinkingDisableOnNonStream = true
			},
			opts:   &api.Options{Thinking: ptrBool(true), ThinkingBudgetTokens: 4096},
			stream: true,
			want:   map[string]any{"enable_thinking": true, "thinking_budget": float64(4096)},
		},
		{
			name: "dashscope: no preference still pins enable_thinking false",
			mutate: func(c *Config) {
				c.Settings.ThinkingFormat = api.ThinkingFormatEnableThinking
				c.Settings.ThinkingAlwaysSend = true
			},
			opts: &api.Options{Temperature: 0.1},
			want: map[string]any{"enable_thinking": false},
		},
		{
			name: "vllm: chat_template_kwargs",
			mutate: func(c *Config) {
				c.Settings.ThinkingFormat = api.ThinkingFormatChatTemplateKwargs
			},
			opts: &api.Options{Thinking: ptrBool(true)},
			want: map[string]any{"chat_template_kwargs": map[string]any{"enable_thinking": true}},
		},
		{
			name: "openrouter: reasoning effort object and enabled=false",
			mutate: func(c *Config) {
				c.Settings.ThinkingFormat = api.ThinkingFormatOpenRouter
				c.Settings.SupportsReasoningEffort = true
			},
			opts: &api.Options{ReasoningEffort: api.ReasoningHigh},
			want: map[string]any{"reasoning": map[string]any{"effort": "high"}},
		},
		{
			name: "openrouter: auto enables without effort",
			mutate: func(c *Config) {
				c.Settings.ThinkingFormat = api.ThinkingFormatOpenRouter
				c.Settings.SupportsReasoningEffort = true
			},
			opts: &api.Options{ReasoningEffort: api.ReasoningAuto},
			want: map[string]any{"reasoning": map[string]any{"enabled": true}},
		},
		{
			name: "moonshot v1: fixed temperature, other sampling dropped",
			mutate: func(c *Config) {
				one := 1.0
				c.Settings.FixedTemperature = &one
			},
			opts: &api.Options{Temperature: 0.2, TopP: 0.9},
			want: map[string]any{"temperature": 1.0, "top_p": nil},
		},
		{
			name: "no thinking preference on a plain vendor sends no thinking fields",
			mutate: func(c *Config) {
				c.Settings.ThinkingFormat = api.ThinkingFormatThinkingType
			},
			opts: &api.Options{Temperature: 0.2},
			want: map[string]any{"thinking": nil, "reasoning_effort": nil, "enable_thinking": nil},
		},
		{
			name: "prompt cache key and long retention",
			mutate: func(c *Config) {
				c.Settings.PromptCacheKey = true
			},
			opts: &api.Options{PromptCacheKey: "sess-1", CacheRetention: api.CacheRetentionLong},
			want: map[string]any{"prompt_cache_key": "sess-1", "prompt_cache_retention": "24h"},
		},
		{
			name: "extra_body merges vendor knobs without overriding caller fields",
			mutate: func(c *Config) {
				c.Settings.ExtraBody = map[string]any{"enable_search": true, "temperature": 0.9}
			},
			opts: &api.Options{Temperature: 0.2},
			want: map[string]any{"enable_search": true, "temperature": 0.2},
		},
		{
			name: "both caller budget aliases produce only the configured wire field",
			opts: &api.Options{MaxTokens: 100, MaxCompletionTokens: 200},
			want: map[string]any{"max_completion_tokens": float64(200), "max_tokens": nil},
		},
		{
			name: "extra_body cannot add the legacy budget beside the caller completion budget",
			mutate: func(c *Config) {
				c.Settings.ExtraBody = map[string]any{"max_tokens": 900, "max_completion_tokens": 800}
			},
			opts: &api.Options{MaxTokens: 100},
			want: map[string]any{"max_completion_tokens": float64(100), "max_tokens": nil},
		},
		{
			name: "conflicting extra_body budgets follow the configured field without caller options",
			mutate: func(c *Config) {
				c.Settings.ExtraBody = map[string]any{"max_tokens": 900, "max_completion_tokens": 800}
			},
			want: map[string]any{"max_completion_tokens": float64(800), "max_tokens": nil},
		},
		{
			name: "legacy budget vendors discard the conflicting completion budget",
			mutate: func(c *Config) {
				c.Settings.MaxTokensField = "max_tokens"
				c.Settings.ExtraBody = map[string]any{"max_completion_tokens": 800}
			},
			opts: &api.Options{MaxTokens: 100},
			want: map[string]any{"max_tokens": float64(100), "max_completion_tokens": nil},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := newClient(t, tc.mutate)
			got := bodyJSON(t, c, conversation, tc.opts, tc.stream)
			for key, want := range tc.want {
				if want == nil {
					_, present := got[key]
					assert.False(t, present, "field %q must be absent, got %v", key, got[key])
					continue
				}
				assert.Equal(t, want, got[key], "field %q", key)
			}
		})
	}
}

func TestMessages_RolesToolsAndReasoningReplay(t *testing.T) {
	c := newClient(t, func(c *Config) {
		c.Reasoning = true
		c.Settings.SupportsDeveloperRole = true
	})
	got := bodyJSON(t, c, conversation, nil, false)
	msgs := got["messages"].([]any)
	require.Len(t, msgs, 4)
	assert.Equal(t, "developer", msgs[0].(map[string]any)["role"])
	assistant := msgs[2].(map[string]any)
	assert.Equal(t, "thinking...", assistant["reasoning_content"])
	_, hasContent := assistant["content"]
	assert.False(t, hasContent, "empty content omitted next to tool_calls")
	tc := assistant["tool_calls"].([]any)[0].(map[string]any)
	assert.Equal(t, "call_1", tc["id"])
	assert.Equal(t, `{"q":"x"}`, tc["function"].(map[string]any)["arguments"])
	tool := msgs[3].(map[string]any)
	assert.Equal(t, "call_1", tool["tool_call_id"])
	assert.Equal(t, "search", tool["name"])

	plain := newClient(t, func(c *Config) { c.Settings.ReplayReasoningContent = false })
	got = bodyJSON(t, plain, conversation, nil, false)
	assistant = got["messages"].([]any)[2].(map[string]any)
	_, has := assistant["reasoning_content"]
	assert.False(t, has)
	assert.Equal(t, "system", got["messages"].([]any)[0].(map[string]any)["role"])
}

func TestMessages_ImagesAndMultiContentDowngrade(t *testing.T) {
	msgs := []api.Message{{Role: "user", Content: "look", Images: []string{"https://img/x.png"}}}
	c := newClient(t, nil)
	got := bodyJSON(t, c, msgs, nil, false)
	parts := got["messages"].([]any)[0].(map[string]any)["content"].([]any)
	require.Len(t, parts, 2)
	assert.Equal(t, "image_url", parts[0].(map[string]any)["type"])
	assert.Equal(t, "look", parts[1].(map[string]any)["text"])

	flat := newClient(t, func(c *Config) { c.Settings.SupportsMultiContent = false })
	got = bodyJSON(t, flat, msgs, nil, false)
	assert.Equal(t, "look", got["messages"].([]any)[0].(map[string]any)["content"])
}

func TestToolCallExtraFieldsRoundTrip(t *testing.T) {
	c := newClient(t, func(c *Config) { c.Settings.ToolCallExtraFields = []string{"extra_content"} })
	md := c.extractToolCallMetadata(json.RawMessage(`{"id":"call_1","type":"function",` +
		`"function":{"name":"f","arguments":"{}"},"extra_content":{"google":{"thought_signature":"abc"}}}`))
	require.NotNil(t, md)
	assert.JSONEq(t, `{"google":{"thought_signature":"abc"}}`, string(md["extra_content"]))

	msgs := []api.Message{{Role: "assistant", ToolCalls: []api.ToolCall{{
		ID: "call_1", Type: "function", Function: api.FunctionCall{Name: "f", Arguments: "{}"}, ProviderMetadata: md,
	}}}}
	got := bodyJSON(t, c, msgs, nil, false)
	tc := got["messages"].([]any)[0].(map[string]any)["tool_calls"].([]any)[0].(map[string]any)
	assert.Equal(t, map[string]any{"google": map[string]any{"thought_signature": "abc"}}, tc["extra_content"])
}

func TestCacheControlBreakpoints(t *testing.T) {
	c := newClient(t, func(c *Config) { c.Settings.CacheControlFormat = "anthropic" })
	got := bodyJSON(t, c, conversation, toolOpts, false)
	data, _ := json.Marshal(got)
	assert.Equal(t, 3, strings.Count(string(data), `"cache_control"`))

	none := bodyJSON(t, c, conversation, &api.Options{CacheRetention: api.CacheRetentionNone}, false)
	data, _ = json.Marshal(none)
	assert.NotContains(t, string(data), "cache_control")
}

func TestChat_NonStreamDecoding(t *testing.T) {
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")
	var captured http.Header
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = r.Header.Clone()
		assert.Equal(t, "/v1/chat/completions", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"index":0,"message":{"role":"assistant",` +
			`"content":"<think>deep</think>answer",` +
			`"tool_calls":[{"id":"c1","type":"function","function":{"name":"f","arguments":"{\"a\":1}"}}]},` +
			`"finish_reason":"tool_calls"}],` +
			`"usage":{"prompt_tokens":30,"completion_tokens":5,` +
			`"prompt_cache_hit_tokens":20,"prompt_cache_miss_tokens":10}}`))
	}))
	defer server.Close()

	c := New(Config{
		Endpoint: api.Endpoint{
			BaseURL: server.URL + "/v1", Model: "m", Auth: api.BearerAuth("sk"),
			Headers: map[string]string{"X-Custom": "1", "Authorization": "Bearer evil"},
		},
		Settings: func() api.OpenAICompletionsSettings {
			s := api.DefaultOpenAICompletions()
			s.PromptCacheAccounting = true
			return s
		}(),
	})
	resp, err := c.Chat(context.Background(), []api.Message{{Role: "user", Content: "hi"}}, nil)
	require.NoError(t, err)
	assert.Equal(t, "Bearer sk", captured.Get("Authorization"), "user headers cannot override auth")
	assert.Equal(t, "1", captured.Get("X-Custom"))
	assert.Equal(t, "answer", resp.Content)
	assert.Equal(t, "deep", resp.ReasoningContent)
	assert.Equal(t, "tool_calls", resp.FinishReason)
	require.Len(t, resp.ToolCalls, 1)
	assert.Equal(t, 20, resp.Usage.CacheReadTokens)
	assert.Equal(t, 10, resp.Usage.CacheMissTokens)
}

func TestChat_HTTPErrorSurfacesBody(t *testing.T) {
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"message":"max_tokens is not supported"}}`))
	}))
	defer server.Close()
	c := New(Config{
		Endpoint: api.Endpoint{BaseURL: server.URL, Model: "m"}, Settings: api.DefaultOpenAICompletions(),
	})
	_, err := c.Chat(context.Background(), []api.Message{{Role: "user", Content: "hi"}}, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "status 400")
	assert.Contains(t, err.Error(), "max_tokens is not supported")
}

const streamFixture = `data: {"choices":[{"index":0,"delta":{"role":"assistant","reasoning_content":"thi"}}]}

data: {"choices":[{"index":0,"delta":{"reasoning_content":"nk"}}]}

data: {"choices":[{"index":0,"delta":{"content":"Hel"}}]}

data: {"choices":[{"index":0,"delta":{"content":"lo"}}]}

data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"c1","type":"function",` +
	`"function":{"name":"search"}}]}}]}

data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"q\":"}}]}}]}

data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"x\"}"}}]}}]}

data: {"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}

data: {"choices":[],"usage":{"prompt_tokens":12,"completion_tokens":7,"prompt_tokens_details":{"cached_tokens":4}}}

data: [DONE]
`

func TestChatStream_Sequence(t *testing.T) {
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		assert.Equal(t, true, body["stream"])
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(streamFixture))
	}))
	defer server.Close()

	c := New(Config{
		Endpoint: api.Endpoint{BaseURL: server.URL, Model: "m"}, Settings: api.DefaultOpenAICompletions(),
	})
	ch, err := c.ChatStream(context.Background(), []api.Message{{Role: "user", Content: "hi"}}, nil)
	require.NoError(t, err)

	var thinking, answer strings.Builder
	var seenTypes []types.ResponseType
	var last types.StreamResponse
	thinkingDone := false
	for chunk := range ch {
		seenTypes = append(seenTypes, chunk.ResponseType)
		switch chunk.ResponseType {
		case types.ResponseTypeThinking:
			if chunk.Done {
				thinkingDone = true
			}
			thinking.WriteString(chunk.Content)
		case types.ResponseTypeAnswer:
			answer.WriteString(chunk.Content)
			if chunk.Done {
				last = chunk
			}
		case types.ResponseTypeToolCall:
			assert.Equal(t, "search", chunk.Data["tool_name"])
			assert.Equal(t, "c1", chunk.Data["tool_call_id"])
		case types.ResponseTypeError:
			t.Fatalf("unexpected error: %s", chunk.Content)
		}
	}
	assert.Equal(t, "think", thinking.String())
	assert.True(t, thinkingDone, "thinking done marker emitted before the answer")
	assert.Equal(t, "Hello", answer.String())
	assert.Contains(t, seenTypes, types.ResponseTypeToolCall)
	require.Len(t, last.ToolCalls, 1)
	assert.Equal(t, `{"q":"x"}`, last.ToolCalls[0].Function.Arguments)
	assert.Equal(t, "tool_calls", last.FinishReason)
	require.NotNil(t, last.Usage)
	assert.Equal(t, 12, last.Usage.PromptTokens)
	assert.Equal(t, 4, last.Usage.CacheReadTokens)
}

func TestSplitThinkTags(t *testing.T) {
	answer, reasoning := splitThinkTags("<think>a</think>b")
	assert.Equal(t, "b", answer)
	assert.Equal(t, "a", reasoning)
	answer, reasoning = splitThinkTags("<think>truncated")
	assert.Equal(t, "", answer)
	assert.Equal(t, "truncated", reasoning)
	answer, reasoning = splitThinkTags("plain")
	assert.Equal(t, "plain", answer)
	assert.Equal(t, "", reasoning)
}

// A response without a usage block must still classify the cache status:
// leaving it empty makes "the vendor reported nothing" look like a real miss
// on the usage dashboards. The pre-split implementation always classified.
func TestChat_ClassifiesCacheStatusWithoutUsageBlock(t *testing.T) {
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"index":0,"message":{"role":"assistant","content":"hi"},` +
			`"finish_reason":"stop"}]}`))
	}))
	defer server.Close()

	for _, tc := range []struct {
		name       string
		accounting bool
		want       types.PromptCacheStatus
	}{
		{"vendor documents cache accounting", true, types.PromptCacheStatusUnreported},
		{"vendor has no cache accounting", false, types.PromptCacheStatusUnsupported},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := New(Config{
				Endpoint: api.Endpoint{BaseURL: server.URL + "/v1", Model: "m", Auth: api.BearerAuth("sk")},
				Settings: func() api.OpenAICompletionsSettings {
					s := api.DefaultOpenAICompletions()
					s.PromptCacheAccounting = tc.accounting
					return s
				}(),
			})
			resp, err := c.Chat(context.Background(), []api.Message{{Role: "user", Content: "hi"}}, nil)
			require.NoError(t, err)
			assert.Equal(t, tc.want, resp.Usage.CacheStatus)
		})
	}
}

// A stream that ends on the [DONE] sentinel without a space after the colon
// must still terminate cleanly rather than logging a JSON parse failure.
func TestChatStream_TolerantDoneSentinel(t *testing.T) {
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(
			"data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hi\"},\"finish_reason\":\"stop\"}]}\n\n" +
				"data:[DONE]\n\n"))
	}))
	defer server.Close()

	c := New(Config{
		Endpoint: api.Endpoint{BaseURL: server.URL + "/v1", Model: "m", Auth: api.BearerAuth("sk")},
		Settings: api.DefaultOpenAICompletions(),
	})
	ch, err := c.ChatStream(context.Background(), []api.Message{{Role: "user", Content: "hi"}}, nil)
	require.NoError(t, err)
	var answer strings.Builder
	for chunk := range ch {
		if chunk.ResponseType == types.ResponseTypeError {
			t.Fatalf("unexpected error chunk: %s", chunk.Content)
		}
		answer.WriteString(chunk.Content)
	}
	assert.Equal(t, "hi", answer.String())
}

// A chunk that will not decode is a hole in the answer. Skipping it (the old
// behaviour) ended the stream on EOF, so a proxy that corrupted one packet
// produced a truncated reply that every caller recorded as a successful one.
// The Anthropic and Gemini loops fail in this situation; so does this one.
func TestChatStream_UndecodableChunkFailsTheStream(t *testing.T) {
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(
			"data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"Hel\"}}]}\n\n" +
				"data: <html>502 Bad Gateway</html>\n\n" +
				"data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"lo\"},\"finish_reason\":\"stop\"}]}\n\n" +
				"data: [DONE]\n\n"))
	}))
	defer server.Close()

	c := New(Config{
		Endpoint: api.Endpoint{BaseURL: server.URL + "/v1", Model: "m", Auth: api.BearerAuth("sk")},
		Settings: api.DefaultOpenAICompletions(),
	})
	ch, err := c.ChatStream(context.Background(), []api.Message{{Role: "user", Content: "hi"}}, nil)
	require.NoError(t, err)

	var sawError bool
	var answer strings.Builder
	for chunk := range ch {
		if chunk.ResponseType == types.ResponseTypeError {
			sawError = true
			assert.Contains(t, chunk.Content, "decode stream chunk")
			continue
		}
		answer.WriteString(chunk.Content)
	}
	assert.True(t, sawError, "a truncated stream must surface as an error, not as a short answer")
	assert.Equal(t, "Hel", answer.String(), "decoding stops at the bad chunk")
}

// A tool_calls entry we cannot decode used to be skipped, so a round that
// wanted to call a tool arrived at the agent as a plain answer with nothing
// to act on. Losing the action is worse than failing the round.
func TestChat_MalformedToolCallIsAnError(t *testing.T) {
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"index":0,"message":{"role":"assistant","content":"",` +
			`"tool_calls":["not-an-object"]},"finish_reason":"tool_calls"}]}`))
	}))
	defer server.Close()

	c := New(Config{
		Endpoint: api.Endpoint{BaseURL: server.URL + "/v1", Model: "m", Auth: api.BearerAuth("sk")},
		Settings: api.DefaultOpenAICompletions(),
	})
	_, err := c.Chat(context.Background(), []api.Message{{Role: "user", Content: "hi"}}, nil)
	require.Error(t, err, "a tool call that will not decode must not pass as a plain answer")
	assert.Contains(t, err.Error(), "decode tool call")
}

// The streaming counterpart: the round must end as an error rather than as a
// short answer whose tool call quietly vanished.
func TestChatStream_MalformedToolCallFailsTheStream(t *testing.T) {
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(
			"data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"ok\"}}]}\n\n" +
				"data: {\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[42]}}]}\n\n" +
				"data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"tool_calls\"}]}\n\n" +
				"data: [DONE]\n\n"))
	}))
	defer server.Close()

	c := New(Config{
		Endpoint: api.Endpoint{BaseURL: server.URL + "/v1", Model: "m", Auth: api.BearerAuth("sk")},
		Settings: api.DefaultOpenAICompletions(),
	})
	ch, err := c.ChatStream(context.Background(), []api.Message{{Role: "user", Content: "hi"}}, nil)
	require.NoError(t, err)

	var sawError bool
	var answer strings.Builder
	for chunk := range ch {
		if chunk.ResponseType == types.ResponseTypeError {
			sawError = true
			assert.Contains(t, chunk.Content, "decode tool call")
			continue
		}
		answer.WriteString(chunk.Content)
	}
	assert.True(t, sawError, "a dropped tool call must surface as an error")
	assert.Equal(t, "ok", answer.String(), "decoding stops at the bad chunk")
}
