package adapters

// golden_vendor_reconcile_test.go — P1b reconciliation, openai-family vendor
// adapter scenarios (v1 chat/golden_vendor_adapters_test.go +
// golden_vendor_adapters2_test.go): azure / deepseek / volcengine / aliyun /
// zhipu / moonshot / lkeap / generic.

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/invoke"
	"github.com/stretchr/testify/require"
)

// 场景 13：Azure api-key 鉴权（部署路径 + api-version query）。
func TestReconcileAzurePlainChat(t *testing.T) {
	allowLoopbackSSRF(t)
	g := newReconcileServer(t, jsonHandler(200, goldenOpenAIPlainResponse))
	m := newGoldenModelConfig(t, g.Server.URL, "azure_openai", "gpt-4o", nil)

	_, err := invoke.Chat(context.Background(), m, &invoke.ChatOptions{
		Messages:            []invoke.Message{textMsg("user", "Hi")},
		Temperature:         0.5,
		MaxCompletionTokens: 128,
	})
	require.NoError(t, err)
	assertRequestsMatchGolden(t, "azure_plain_chat", g)
}

// 场景 14：Azure reasoning 模型剥参（o-series 采样清零 + effort 透传）。
func TestReconcileAzureReasoningChat(t *testing.T) {
	allowLoopbackSSRF(t)
	g := newReconcileServer(t, jsonHandler(200, goldenOpenAIPlainResponse))
	m := newGoldenModelConfig(t, g.Server.URL, "azure_openai", "o4-mini", nil)

	_, err := invoke.Chat(context.Background(), m, &invoke.ChatOptions{
		Messages:            []invoke.Message{textMsg("user", "Hi")},
		Temperature:         0.7,
		TopP:                0.9,
		MaxCompletionTokens: 512,
		Thinking:            textPtr(true),
		ThinkingLevel:       "medium",
	})
	require.NoError(t, err)
	assertRequestsMatchGolden(t, "azure_reasoning_chat", g)
}

// 场景 15：DeepSeek 裸 HTTP 非流式（tool_choice 剥除 + max_tokens + map 形体）。
func TestReconcileDeepSeekRawChat(t *testing.T) {
	allowLoopbackSSRF(t)
	g := newReconcileServer(t, jsonHandler(200, goldenOpenAIPlainResponse))
	m := newGoldenModelConfig(t, g.Server.URL, "deepseek", "deepseek-chat", nil)

	_, err := invoke.Chat(context.Background(), m, &invoke.ChatOptions{
		Messages:            []invoke.Message{textMsg("user", "Hi")},
		MaxCompletionTokens: 100,
		Tools:               []invoke.ToolDef{{Name: "lookup"}},
		ToolChoice:          "auto",
	})
	require.NoError(t, err)
	assertRequestsMatchGolden(t, "deepseek_raw_chat", g)
}

// 场景 16：DeepSeek 裸 HTTP 流式。
func TestReconcileDeepSeekRawStream(t *testing.T) {
	allowLoopbackSSRF(t)
	id := "chatcmpl-ds-stream"
	lines := []string{
		"data: " + openaiChunk(id, `{"role":"assistant","content":""}`), "",
		"data: " + openaiChunk(id, `{"reasoning_content":"step one"}`), "",
		"data: " + openaiChunk(id, `{"content":"Hi there"}`), "",
		"data: " + openaiChunk(id, `{"content":"","finish_reason":"stop"}`), "",
		"data: " + openaiUsageChunk(id,
			`{"prompt_tokens":1000,"completion_tokens":20,"total_tokens":1020,`+
				`"prompt_cache_hit_tokens":936,"prompt_cache_miss_tokens":64}`), "",
		"data: [DONE]", "",
	}
	g := newReconcileServer(t, sseHandler(lines...))
	m := newGoldenModelConfig(t, g.Server.URL, "deepseek", "deepseek-chat", nil)

	ch, err := invoke.ChatStream(context.Background(), m, &invoke.ChatOptions{
		Messages:            []invoke.Message{textMsg("user", "Hi")},
		MaxCompletionTokens: 100,
	})
	require.NoError(t, err)
	drainStream(t, ch)
	assertRequestsMatchGolden(t, "deepseek_raw_stream", g)
}

// 场景 17：Volcengine thinking.type（enabled / disabled 两次请求）。
func TestReconcileVolcengineThinkingField(t *testing.T) {
	allowLoopbackSSRF(t)
	g := newReconcileServer(t, jsonHandler(200, goldenOpenAIPlainResponse))
	m := newGoldenModelConfig(t, g.Server.URL, "volcengine", "doubao-seed-1-6", nil)

	for _, thinking := range []bool{true, false} {
		_, err := invoke.Chat(context.Background(), m, &invoke.ChatOptions{
			Messages:            []invoke.Message{textMsg("user", "Hi")},
			MaxCompletionTokens: 64,
			Thinking:            textPtr(thinking),
		})
		require.NoError(t, err, "thinking %v", thinking)
	}
	assertRequestsMatchGolden(t, "volcengine_thinking_field", g)
}

// 场景 18：Aliyun Qwen thinking 模型 enable_thinking 钉死。
func TestReconcileAliyunQwenThinkingPin(t *testing.T) {
	allowLoopbackSSRF(t)
	streamLines := []string{
		"data: " + openaiChunk("chatcmpl-qwen", `{"content":"你好"}`), "",
		"data: " + openaiChunk("chatcmpl-qwen", `{"content":"","finish_reason":"stop"}`), "",
		"data: [DONE]", "",
	}
	handler, _ := sequencingHandler(jsonHandler(200, goldenOpenAIPlainResponse), sseHandler(streamLines...))
	g := newReconcileServer(t, handler)
	m := newGoldenModelConfig(t, g.Server.URL, "aliyun", "qwen3-max", nil)

	_, err := invoke.Chat(context.Background(), m, &invoke.ChatOptions{
		Messages:            []invoke.Message{textMsg("user", "你好")},
		MaxCompletionTokens: 64,
		Thinking:            textPtr(true),
	})
	require.NoError(t, err)
	ch, err := invoke.ChatStream(context.Background(), m, &invoke.ChatOptions{
		Messages:            []invoke.Message{textMsg("user", "你好")},
		MaxCompletionTokens: 64,
	})
	require.NoError(t, err)
	drainStream(t, ch)
	assertRequestsMatchGolden(t, "aliyun_qwen_thinking_pin", g)
}

// 场景 19：Aliyun cache_control 断点（system + 最后一条会话消息，long TTL）。
func TestReconcileAliyunCacheControlBreakpoints(t *testing.T) {
	allowLoopbackSSRF(t)
	g := newReconcileServer(t, jsonHandler(200, goldenOpenAIPlainResponse))
	m := newGoldenModelConfig(t, g.Server.URL, "aliyun", "qwen2.5-72b-instruct", nil)

	_, err := invoke.Chat(context.Background(), m, &invoke.ChatOptions{
		Messages: []invoke.Message{
			textMsg("system", "You are a chemistry expert."), textMsg("user", "Ethanol properties?"),
		},
		MaxCompletionTokens: 64,
		CacheRetention:      invoke.CacheRetentionLong,
	})
	require.NoError(t, err)
	assertRequestsMatchGolden(t, "aliyun_cache_control_breakpoints", g)
}

// 场景 20：Zhipu（max_tokens wire 字段；baseProvider 行为）。
func TestReconcileZhipuPlainChat(t *testing.T) {
	allowLoopbackSSRF(t)
	g := newReconcileServer(t, jsonHandler(200, goldenOpenAIPlainResponse))
	m := newGoldenModelConfig(t, g.Server.URL, "zhipu", "glm-4", nil)

	_, err := invoke.Chat(context.Background(), m, &invoke.ChatOptions{
		Messages:            []invoke.Message{textMsg("user", "Hi")},
		Temperature:         0.6,
		MaxCompletionTokens: 128,
	})
	require.NoError(t, err)
	assertRequestsMatchGolden(t, "zhipu_plain_chat", g)
}

// 场景 21：Moonshot 固定温度（kimi-k2.5：temperature=1，其余采样清零）。
func TestReconcileMoonshotFixedTempChat(t *testing.T) {
	allowLoopbackSSRF(t)
	g := newReconcileServer(t, jsonHandler(200, goldenOpenAIPlainResponse))
	m := newGoldenModelConfig(t, g.Server.URL, "moonshot", "kimi-k2.5", nil)

	_, err := invoke.Chat(context.Background(), m, &invoke.ChatOptions{
		Messages:            []invoke.Message{textMsg("user", "Hi")},
		Temperature:         0.7,
		TopP:                0.9,
		FrequencyPenalty:    0.1,
		PresencePenalty:     0.2,
		MaxCompletionTokens: 128,
	})
	require.NoError(t, err)
	assertRequestsMatchGolden(t, "moonshot_fixed_temp_chat", g)
}

// 场景 22：LKEAP thinking.type + max_tokens（DeepSeek V3.x）。
func TestReconcileLkeapThinkingChat(t *testing.T) {
	allowLoopbackSSRF(t)
	g := newReconcileServer(t, jsonHandler(200, goldenOpenAIPlainResponse))
	m := newGoldenModelConfig(t, g.Server.URL, "lkeap", "deepseek-v3.1", nil)

	_, err := invoke.Chat(context.Background(), m, &invoke.ChatOptions{
		Messages:            []invoke.Message{textMsg("user", "Hi")},
		MaxCompletionTokens: 128,
		Thinking:            textPtr(true),
	})
	require.NoError(t, err)
	assertRequestsMatchGolden(t, "lkeap_thinking_chat", g)
}

// 场景 23：Generic（vLLM）chat_template_kwargs 流式。
func TestReconcileGenericVllmKwargsStream(t *testing.T) {
	allowLoopbackSSRF(t)
	id := "chatcmpl-vllm"
	lines := []string{
		"data: " + openaiChunk(id, `{"content":"你好"}`), "",
		"data: " + openaiChunk(id, `{"content":"","finish_reason":"stop"}`), "",
		"data: " + openaiUsageChunk(id, `{"prompt_tokens":12,"completion_tokens":4,"total_tokens":16}`), "",
		"data: [DONE]", "",
	}
	g := newReconcileServer(t, sseHandler(lines...))
	m := newGoldenModelConfig(t, g.Server.URL, "generic", "Qwen2.5-32B-Instruct", nil)

	ch, err := invoke.ChatStream(context.Background(), m, &invoke.ChatOptions{
		Messages:            []invoke.Message{textMsg("user", "你好")},
		MaxCompletionTokens: 128,
		Thinking:            textPtr(true),
	})
	require.NoError(t, err)
	drainStream(t, ch)
	assertRequestsMatchGolden(t, "generic_vllm_kwargs_stream", g)
}
