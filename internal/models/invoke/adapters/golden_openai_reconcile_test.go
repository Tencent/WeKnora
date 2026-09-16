package adapters

// golden_openai_reconcile_test.go — P1b reconciliation, OpenAI funnel
// scenarios (v1 chat/golden_openai_funnel_test.go + golden_openai_errors_
// cache_test.go). Same inputs, invoke engine; requests reconciled byte-exact
// against testdata/golden.

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/models/invoke"
	"github.com/stretchr/testify/require"
)

const goldenOpenAIPlainResponse = `{"id":"chatcmpl-1","object":"chat.completion",` +
	`"created":1700000000,"model":"golden-model","choices":[{"index":0,` +
	`"message":{"role":"assistant","content":"Hello there"},"finish_reason":"stop"}],` +
	`"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`

func textMsg(role invoke.Role, text string) invoke.Message {
	return invoke.Message{Role: role, Content: []invoke.Part{{Text: text}}}
}

func textPtr(b bool) *bool { return &b }

// 场景 1：普通 OpenAI 模型（baseProvider 透传）。
func TestReconcileOpenAIPlainChat(t *testing.T) {
	allowLoopbackSSRF(t)
	g := newReconcileServer(t, jsonHandler(200, goldenOpenAIPlainResponse))
	m := newGoldenModelConfig(t, g.Server.URL, "openai", "gpt-4o-mini", nil)

	_, err := invoke.Chat(context.Background(), m, &invoke.ChatOptions{
		Messages:            []invoke.Message{textMsg("system", "You are helpful."), textMsg("user", "Hi")},
		Temperature:         0.7,
		MaxCompletionTokens: 256,
	})
	require.NoError(t, err)
	assertRequestsMatchGolden(t, "openai_plain_chat", g)
}

// 场景 2：GPT-5 ShapeRequest（采样参数清零、预算迁 mct、reasoning_effort）。
func TestReconcileOpenAIReasoningGPT5Chat(t *testing.T) {
	allowLoopbackSSRF(t)
	g := newReconcileServer(t, jsonHandler(200, goldenOpenAIPlainResponse))
	m := newGoldenModelConfig(t, g.Server.URL, "openai", "gpt-5-mini", nil)

	_, err := invoke.Chat(context.Background(), m, &invoke.ChatOptions{
		Messages:            []invoke.Message{textMsg("user", "Hi")},
		Temperature:         0.7,
		TopP:                0.9,
		FrequencyPenalty:    0.1,
		PresencePenalty:     0.2,
		MaxCompletionTokens: 512,
		Thinking:            textPtr(true),
		ThinkingLevel:       "high",
	})
	require.NoError(t, err)
	assertRequestsMatchGolden(t, "openai_reasoning_gpt5_chat", g)
}

// 场景 3：reasoning_effort 各档位。
func TestReconcileOpenAIReasoningEffortLevels(t *testing.T) {
	allowLoopbackSSRF(t)
	g := newReconcileServer(t, jsonHandler(200, goldenOpenAIPlainResponse))
	m := newGoldenModelConfig(t, g.Server.URL, "openai", "gpt-5-mini", nil)

	for _, level := range []string{"low", "medium", "high"} {
		_, err := invoke.Chat(context.Background(), m, &invoke.ChatOptions{
			Messages:            []invoke.Message{textMsg("user", "Hi")},
			MaxCompletionTokens: 256,
			Thinking:            textPtr(true),
			ThinkingLevel:       level,
		})
		require.NoError(t, err, "level %s", level)
	}
	assertRequestsMatchGolden(t, "openai_reasoning_effort_levels", g)
}

// 场景 4：工具调用流式 delta + reasoning_content 思考流 + usage + [DONE]。
func TestReconcileOpenAIStreamReasoningTools(t *testing.T) {
	allowLoopbackSSRF(t)
	id := "chatcmpl-stream-1"
	lines := []string{
		"data: " + openaiChunk(id, `{"role":"assistant","content":""}`), "",
		"data: " + openaiChunk(id, `{"reasoning_content":"Let me check the weather."}`), "",
		"data: " + openaiChunk(id, `{"content":"Hel"}`), "",
		"data: " + openaiChunk(id, `{"content":"lo"}`), "",
		"data: " + openaiChunk(id, `{"tool_calls":[{"index":0,"id":"call_1",`+
			`"type":"function","function":{"name":"get_weather","arguments":""}}]}`), "",
		"data: " + openaiChunk(id, `{"tool_calls":[{"index":0,"function":{"arguments":"{\"city\":"}}]}`), "",
		"data: " + openaiChunk(id, `{"tool_calls":[{"index":0,"function":{"arguments":"\"SF\"}"}}]}`), "",
		"data: " + openaiChunk(id, `{"content":"","finish_reason":"tool_calls"}`), "",
		"data: " + openaiUsageChunk(id, `{"prompt_tokens":21,"completion_tokens":9,"total_tokens":30}`), "",
		"data: [DONE]", "",
	}
	g := newReconcileServer(t, sseHandler(lines...))
	m := newGoldenModelConfig(t, g.Server.URL, "openai", "gpt-4o-mini", nil)

	ch, err := invoke.ChatStream(context.Background(), m, &invoke.ChatOptions{
		Messages:            []invoke.Message{textMsg("user", "Weather in SF?")},
		MaxCompletionTokens: 256,
		Tools: []invoke.ToolDef{{
			Name:        "get_weather",
			Description: "Get weather",
			Parameters:  json.RawMessage(`{"type":"object"}`),
		}},
	})
	require.NoError(t, err)
	drainStream(t, ch)
	assertRequestsMatchGolden(t, "openai_stream_reasoning_tools", g)
}

// 场景 5：reasoning_content 多轮回传（DeepSeek/MiMo 400 生命线）。
func TestReconcileOpenAIReasoningContentRoundtrip(t *testing.T) {
	allowLoopbackSSRF(t)
	g := newReconcileServer(t, jsonHandler(200, goldenOpenAIPlainResponse))
	m := newGoldenModelConfig(t, g.Server.URL, "openai", "deepseek-reasoner", nil)

	_, err := invoke.Chat(context.Background(), m, &invoke.ChatOptions{
		Messages: []invoke.Message{
			textMsg("user", "What is 2+2?"),
			{Role: "assistant", Content: []invoke.Part{{Text: "4"}}, ReasoningContent: "addition of two integers"},
			textMsg("user", "and times 3?"),
		},
		MaxCompletionTokens: 256,
	})
	require.NoError(t, err)
	assertRequestsMatchGolden(t, "openai_reasoning_content_roundtrip", g)
}

// 场景 6：多模态消息（content parts 透传，detail 保留）。
func TestReconcileOpenAIMultimodalChat(t *testing.T) {
	allowLoopbackSSRF(t)
	g := newReconcileServer(t, jsonHandler(200, goldenOpenAIPlainResponse))
	m := newGoldenModelConfig(t, g.Server.URL, "openai", "gpt-4o-mini", nil)

	_, err := invoke.Chat(context.Background(), m, &invoke.ChatOptions{
		Messages: []invoke.Message{{
			Role: "user",
			Content: []invoke.Part{
				{Text: "What is in this picture?"},
				{Image: &invoke.ImageRef{URL: "data:image/png;base64,AAAA", Detail: "auto"}},
			},
		}},
		MaxCompletionTokens: 256,
	})
	require.NoError(t, err)
	assertRequestsMatchGolden(t, "openai_multimodal_chat", g)
}

// 场景 7：多模态图片降级重试（入口 chatWithFallback 编排）。
func TestReconcileOpenAIMultimodalDegradeRetry(t *testing.T) {
	allowLoopbackSSRF(t)
	h, callCount := sequencingHandler(
		jsonHandler(400, `{"error":{"message":"Image input not supported by this model",`+
			`"type":"invalid_request_error"}}`),
		jsonHandler(200, goldenOpenAIPlainResponse),
	)
	g := newReconcileServer(t, h)
	m := newGoldenModelConfig(t, g.Server.URL, "openai", "gpt-4o-mini", nil)

	_, err := invoke.Chat(context.Background(), m, &invoke.ChatOptions{
		Messages: []invoke.Message{{
			Role: "user",
			Content: []invoke.Part{
				{Image: &invoke.ImageRef{URL: "data:image/png;base64,AAAA", Detail: "auto"}},
				{Text: "What is in this picture?"},
			},
		}},
		MaxCompletionTokens: 256,
	})
	require.NoError(t, err)
	require.Equal(t, 2, callCount(), "expected 2 attempts")
	assertRequestsMatchGolden(t, "openai_multimodal_degrade_retry", g)
}

// 场景 8：流中断（服务端半途断连）。
func TestReconcileOpenAIStreamInterrupted(t *testing.T) {
	allowLoopbackSSRF(t)
	id := "chatcmpl-broken"
	lines := []string{
		"data: " + openaiChunk(id, `{"tool_calls":[{"index":0,"id":"call_1",`+
			`"type":"function","function":{"name":"get_weather","arguments":""}}]}`), "",
		"data: " + openaiChunk(id, `{"tool_calls":[{"index":0,"function":{"arguments":"{\"city\":"}}]}`), "",
	}
	sse := sseHandler(lines...)
	handler := func(w http.ResponseWriter, r *http.Request) {
		sse(w, r)
		hj, ok := w.(http.Hijacker)
		if !ok {
			t.Errorf("response is not a hijacker")
			return
		}
		conn, _, err := hj.Hijack()
		if err != nil {
			t.Errorf("hijack: %v", err)
			return
		}
		_ = conn.Close()
	}
	g := newReconcileServer(t, handler)
	m := newGoldenModelConfig(t, g.Server.URL, "openai", "gpt-4o-mini", nil)

	ch, err := invoke.ChatStream(context.Background(), m, &invoke.ChatOptions{
		Messages:            []invoke.Message{textMsg("user", "Weather?")},
		MaxCompletionTokens: 256,
	})
	require.NoError(t, err)
	done := make(chan struct{})
	go func() { drainStream(t, ch); close(done) }()
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("interrupted stream did not terminate")
	}
	assertRequestsMatchGolden(t, "openai_stream_interrupted", g)
}

// 场景 9：401 鉴权失败（fail fast，ErrAuth）。
func TestReconcileOpenAI401Auth(t *testing.T) {
	allowLoopbackSSRF(t)
	body := `{"error":{"message":"Incorrect API key provided","type":"invalid_request_error"}}`
	g := newReconcileServer(t, jsonHandler(401, body))
	m := newGoldenModelConfig(t, g.Server.URL, "openai", "gpt-4o-mini", nil)

	_, err := invoke.Chat(context.Background(), m, &invoke.ChatOptions{
		Messages:            []invoke.Message{textMsg("user", "Hi")},
		MaxCompletionTokens: 8,
	})
	assertErrorKind(t, "openai_401_auth", err)
	assertRequestsMatchGolden(t, "openai_401_auth", g)
}

// 场景 10：429 限流（两次 429 后第三次成功，3 次请求全部录制）。
func TestReconcileOpenAI429RetryThenSuccess(t *testing.T) {
	allowLoopbackSSRF(t)
	body := `{"error":{"message":"Rate limit reached for requests","type":"requests","code":"rate_limit_exceeded"}}`
	h, _ := sequencingHandler(
		jsonHandler(429, body),
		jsonHandler(429, body),
		jsonHandler(200, goldenOpenAIPlainResponse),
	)
	g := newReconcileServer(t, h)
	m := newGoldenModelConfig(t, g.Server.URL, "openai", "gpt-4o-mini", nil)

	_, err := invoke.Chat(context.Background(), m, &invoke.ChatOptions{
		Messages:            []invoke.Message{textMsg("user", "Hi")},
		MaxCompletionTokens: 8,
	})
	require.NoError(t, err)
	assertRequestsMatchGolden(t, "openai_429_retry_then_success", g)
}

// 场景 11：400 上下文超限（措辞命中 contextOverflowPatterns）。
func TestReconcileOpenAI400ContextExceeded(t *testing.T) {
	allowLoopbackSSRF(t)
	body := `{"error":{"message":"This model's maximum context length is 8192 tokens. ` +
		`However, your messages resulted in 9000 tokens.","type":"invalid_request_error"}}`
	g := newReconcileServer(t, jsonHandler(400, body))
	m := newGoldenModelConfig(t, g.Server.URL, "openai", "gpt-4o-mini", nil)

	_, err := invoke.Chat(context.Background(), m, &invoke.ChatOptions{
		Messages:            []invoke.Message{textMsg("user", "long prompt")},
		MaxCompletionTokens: 8,
	})
	assertErrorKind(t, "openai_400_context_exceeded", err)
	assertRequestsMatchGolden(t, "openai_400_context_exceeded", g)
}

// 场景 12：prompt cache 出站（PromptCacheKey + retention long）。
func TestReconcileOpenAIPromptCacheKeyRetention(t *testing.T) {
	allowLoopbackSSRF(t)
	g := newReconcileServer(t, jsonHandler(200, goldenOpenAIPlainResponse))
	m := newGoldenModelConfig(t, g.Server.URL, "openai", "gpt-4o-mini", nil)

	_, err := invoke.Chat(context.Background(), m, &invoke.ChatOptions{
		Messages:            []invoke.Message{textMsg("system", "You are helpful."), textMsg("user", "Hi")},
		MaxCompletionTokens: 64,
		PromptCacheKey:      "sess-abc123",
		CacheRetention:      invoke.CacheRetentionLong,
	})
	require.NoError(t, err)
	assertRequestsMatchGolden(t, "openai_prompt_cache_key_retention", g)
}
