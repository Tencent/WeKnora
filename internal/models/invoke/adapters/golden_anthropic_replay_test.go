package adapters

// golden_anthropic_replay_test.go: P1b reconciliation for the five anthropic
// golden scenarios. Same inputs as the v1 recordings, driven through the
// invoke entry against the recorded server replays. Request side (path /
// headers / body bytes) is the hard gate via assertRequestsMatchGolden.

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/invoke"
	"github.com/stretchr/testify/require"
)

const goldenAnthropicPlainResponse = `{"id":"msg_1","type":"message","role":"assistant",` +
	`"model":"golden-claude","content":[{"type":"text","text":"Hello there"}],` +
	`"stop_reason":"end_turn","usage":{"input_tokens":10,"output_tokens":5}}`

func anthropicConfig(t *testing.T, baseURL string) *invoke.ModelConfig {
	return newGoldenModelConfig(t, baseURL, "anthropic", "claude-sonnet-4-5", nil)
}

func anthropicMessages() []invoke.Message {
	return []invoke.Message{
		{Role: "system", Content: []invoke.Part{{Text: "You are helpful."}}},
		{Role: "user", Content: []invoke.Part{{Text: "Hi"}}},
	}
}

// 场景 26 对账：plain chat（retention none → 纯字符串 system）。
func TestGoldenReplayAnthropicPlainChat(t *testing.T) {
	allowLoopbackSSRF(t)
	g := newReconcileServer(t, jsonHandler(200, goldenAnthropicPlainResponse))

	resp, err := invoke.Chat(context.Background(), anthropicConfig(t, g.Server.URL), &invoke.ChatOptions{
		Messages:            anthropicMessages(),
		MaxCompletionTokens: 200,
		Temperature:         0.7,
		CacheRetention:      invoke.CacheRetentionNone,
	})
	require.NoError(t, err)

	assertRequestsMatchGolden(t, "anthropic_plain_chat", g)

	// 客户端视图：内容 / finish_reason / usage 三元组与 golden client 一致。
	require.Equal(t, "Hello there", resp.Content)
	require.Equal(t, "end_turn", resp.FinishReason)
	require.Equal(t, 10, resp.Usage.PromptTokens)
	require.Equal(t, 5, resp.Usage.CompletionTokens)
	require.Equal(t, 15, resp.Usage.TotalTokens)
}

// 场景 27 对账：cache_control 断点（默认 short retention）+ 缓存计数 usage。
func TestGoldenReplayAnthropicCacheControlChat(t *testing.T) {
	allowLoopbackSSRF(t)
	g := newReconcileServer(t, jsonHandler(200,
		`{"id":"msg_2","type":"message","role":"assistant","model":"golden-claude",`+
			`"content":[{"type":"text","text":"Answer"}],"stop_reason":"end_turn",`+
			`"usage":{"input_tokens":10,"output_tokens":5,`+
			`"cache_creation_input_tokens":150,"cache_read_input_tokens":0}}`))

	resp, err := invoke.Chat(context.Background(), anthropicConfig(t, g.Server.URL), &invoke.ChatOptions{
		Messages:            anthropicMessages(),
		MaxCompletionTokens: 200,
	})
	require.NoError(t, err)

	assertRequestsMatchGolden(t, "anthropic_cache_control_chat", g)

	// v1 prompt = input + cache_read + cache_write = 160；invoke.Usage 只保留
	// 三元组，缓存明细字段随 v2 Usage 视图退役（报告在案）。
	require.Equal(t, "Answer", resp.Content)
	require.Equal(t, 160, resp.Usage.PromptTokens)
	require.Equal(t, 5, resp.Usage.CompletionTokens)
	require.Equal(t, 165, resp.Usage.TotalTokens)
}

// 场景 28 对账：thinking 各档位（low/medium/high → budget_tokens 2048/8192/
// 16384，max_tokens 低于 budget 时抬高到 budget+4096）。
func TestGoldenReplayAnthropicThinkingLevelsChat(t *testing.T) {
	allowLoopbackSSRF(t)
	g := newReconcileServer(t, jsonHandler(200, goldenAnthropicPlainResponse))

	on := true
	for _, level := range []string{"low", "medium", "high"} {
		_, err := invoke.Chat(context.Background(), anthropicConfig(t, g.Server.URL), &invoke.ChatOptions{
			Messages:            []invoke.Message{{Role: "user", Content: []invoke.Part{{Text: "Hi"}}}},
			MaxCompletionTokens: 1024,
			Thinking:            &on,
			ThinkingLevel:       level,
		})
		require.NoError(t, err, "level %s", level)
	}

	assertRequestsMatchGolden(t, "anthropic_thinking_levels_chat", g)
}

// 场景 29 对账：流式事件序列（message_start / content_block_delta x2 /
// message_delta / message_stop → answer chunks + usage + Done）。
// 无 caller deadline：默认超时流式路径回归（executor cancel 绑定 stream
// 生命周期，骨架缺陷已修复）。
func TestGoldenReplayAnthropicStream(t *testing.T) {
	allowLoopbackSSRF(t)
	golden := loadGoldenDoc(t, "anthropic_stream")
	g := newReconcileServer(t, sseHandler(golden.SSE...))

	ch, err := invoke.ChatStream(context.Background(), anthropicConfig(t, g.Server.URL), &invoke.ChatOptions{
		Messages:            []invoke.Message{{Role: "user", Content: []invoke.Part{{Text: "Hi"}}}},
		MaxCompletionTokens: 200,
	})
	require.NoError(t, err)
	chunks := collectStream(t, ch)

	assertRequestsMatchGolden(t, "anthropic_stream", g)

	// 归一化对账：usage 事件并回 Done 块后必须与 v1 golden 视图逐字段一致
	// （含 usage 数值 25/5/30 与 finish_reason=end_turn）。
	require.Equal(t, goldenStreamView(t, golden.Client), normalizeStream(t, chunks))
}

// 场景 30 对账：401 → ErrAuth fail fast（单次尝试）。
func TestGoldenReplayAnthropic401Auth(t *testing.T) {
	allowLoopbackSSRF(t)
	g := newReconcileServer(t, jsonHandler(401,
		`{"type":"error","error":{"type":"authentication_error","message":"invalid x-api-key"}}`))

	_, err := invoke.Chat(context.Background(), anthropicConfig(t, g.Server.URL), &invoke.ChatOptions{
		Messages:            []invoke.Message{{Role: "user", Content: []invoke.Part{{Text: "Hi"}}}},
		MaxCompletionTokens: 16,
	})
	require.Error(t, err)

	assertRequestsMatchGolden(t, "anthropic_401_auth", g)
	assertErrorKind(t, "anthropic_401_auth", err)
}

// 适配器级钉死：流式 usage 合并算术（v1 mergeAnthropicUsage 移植）——
// prompt=25 / completion=5 / total=30 在 Done 事件上成立。
func TestAnthropicStreamUsageMergedIntoDone(t *testing.T) {
	state := invoke.NewStreamBridgeState()
	a := &AnthropicAdapter{}

	_, err := a.TranslateStreamEvent(state, invoke.StreamChunk{Event: "message_start", Data: []byte(
		`{"type":"message_start","message":{"usage":{"input_tokens":25,"output_tokens":1}}}`)})
	require.NoError(t, err)
	ev := firstEvent(t, a, state, invoke.StreamChunk{Event: "message_delta", Data: []byte(
		`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":5}}`)})
	require.Equal(t, invoke.StreamKindUsage, ev.Kind)
	require.Equal(t, 25, ev.Usage.PromptTokens)
	require.Equal(t, 5, ev.Usage.CompletionTokens)
	final := firstEvent(t, a, state, invoke.StreamChunk{
		Event: "message_stop", Data: []byte(`{"type":"message_stop"}`),
	})
	require.NotNil(t, final.Done)
	require.Equal(t, "end_turn", final.Done.FinishReason)
	require.Equal(t, 25, final.Usage.PromptTokens)
	require.Equal(t, 5, final.Usage.CompletionTokens)
	require.Equal(t, 30, final.Usage.TotalTokens)
}
