package adapters

// aliyun_test.go — native DashScope wire tests (2026-09-12 native ruling).
// The golden reconcile suite (golden_vendor_reconcile_test.go /
// golden_embedding_reconcile_test.go) pins the wire bytes; this file covers
// the seams those scenarios don't reach: base normalization, the vision
// branch, response/stream parsing, and the native catalog facet.

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/invoke"
	"github.com/stretchr/testify/require"
)

func TestAliyunNativeBaseURL(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", invoke.AliyunBaseURL},
		{"https://dashscope.aliyuncs.com", "https://dashscope.aliyuncs.com"},
		{"https://dashscope.aliyuncs.com/", "https://dashscope.aliyuncs.com"},
		// 兼容模式时代的 record base 归一回 DashScope 根。
		{"https://dashscope.aliyuncs.com/compatible-mode/v1", "https://dashscope.aliyuncs.com"},
		{"https://dashscope.aliyuncs.com/compatible-mode", "https://dashscope.aliyuncs.com"},
		// SDK 风格 base 不允许路径翻倍。
		{"https://dashscope.aliyuncs.com/api/v1", "https://dashscope.aliyuncs.com"},
		{"https://dashscope-intl.aliyuncs.com/compatible-mode/v1/", "https://dashscope-intl.aliyuncs.com"},
	}
	for _, c := range cases {
		require.Equal(t, c.want, aliyunNativeBaseURL(c.in), "base %q", c.in)
	}
}

func TestAliyunBuildChatRequestTextShape(t *testing.T) {
	a := newAliyunAdapter()
	req, err := a.BuildChatRequest(invoke.Endpoint{Credentials: invoke.Credentials{APIKey: "sk"}},
		"qwen-plus", &invoke.ChatOptions{
			Messages: []invoke.Message{
				invoke.TextMessage("system", "be terse"), invoke.TextMessage("user", "hi"),
			},
			MaxCompletionTokens: 32,
			Temperature:         0.7,
		})
	require.NoError(t, err)
	require.Equal(t, "https://dashscope.aliyuncs.com/api/v1/services/aigc/text-generation/generation", req.URL)
	require.False(t, req.Stream)
	require.Equal(t, "Bearer sk", req.Header.Get("Authorization"))
	require.Equal(t, "", req.Header.Get("X-DashScope-SSE"), "non-stream carries no SSE header")

	var body map[string]any
	require.NoError(t, json.Unmarshal(req.Body, &body))
	require.Equal(t, "qwen-plus", body["model"])
	input := body["input"].(map[string]any)
	msgs := input["messages"].([]any)
	require.Len(t, msgs, 2)
	require.Equal(t, "be terse", msgs[0].(map[string]any)["content"], "text path degrades to plain strings")
	params := body["parameters"].(map[string]any)
	require.Equal(t, "message", params["result_format"])
	require.Equal(t, float64(32), params["max_completion_tokens"])
	require.Equal(t, float64(0.7), params["temperature"])
	// qwen-plus 在 IsQwenThinkingModel 谓词内（qwen3/plus/max/turbo 前缀）：
	// 非流式钉 false（Qwen3 系非流式拒绝 thinking）。
	require.NotNil(t, params["enable_thinking"])
	require.Equal(t, false, params["enable_thinking"])
	require.NotContains(t, body, "cache_control")
	require.NotContains(t, params, "frequency_penalty", "native schema 无此字段")
}

func TestAliyunBuildChatRequestStreamHeadersAndIncremental(t *testing.T) {
	a := newAliyunAdapter()
	on := true
	req, err := a.BuildChatRequest(invoke.Endpoint{Credentials: invoke.Credentials{APIKey: "sk"}},
		"qwen3-max", &invoke.ChatOptions{
			Messages: []invoke.Message{invoke.TextMessage("user", "hi")},
			Thinking: &on,
			Stream:   true,
		})
	require.NoError(t, err)
	require.True(t, req.Stream)
	require.Equal(t, "enable", req.Header.Get("X-DashScope-SSE"))
	require.Equal(t, "text/event-stream", req.Header.Get("Accept"))

	var body struct {
		Parameters struct {
			IncrementalOutput bool  `json:"incremental_output"`
			EnableThinking    *bool `json:"enable_thinking"`
		} `json:"parameters"`
	}
	require.NoError(t, json.Unmarshal(req.Body, &body))
	require.True(t, body.Parameters.IncrementalOutput)
	require.NotNil(t, body.Parameters.EnableThinking)
	require.True(t, *body.Parameters.EnableThinking, "流式保留平台的 Thinking=true")
}

func TestAliyunThinkingPinFalseOnNonStream(t *testing.T) {
	a := newAliyunAdapter()
	on := true
	req, err := a.BuildChatRequest(invoke.Endpoint{}, "qwen3-max", &invoke.ChatOptions{
		Messages: []invoke.Message{invoke.TextMessage("user", "hi")},
		Thinking: &on,
	})
	require.NoError(t, err)
	var body struct {
		Parameters struct {
			EnableThinking *bool `json:"enable_thinking"`
		} `json:"parameters"`
	}
	require.NoError(t, json.Unmarshal(req.Body, &body))
	require.NotNil(t, body.Parameters.EnableThinking)
	require.False(t, *body.Parameters.EnableThinking, "Qwen3 非流式拒绝 thinking，钉 false")
}

func TestAliyunBuildChatRequestVisionBranch(t *testing.T) {
	a := newAliyunAdapter()
	req, err := a.BuildChatRequest(invoke.Endpoint{}, "qwen-vl-max", &invoke.ChatOptions{
		Messages: []invoke.Message{{
			Role: "user",
			Content: []invoke.Part{
				{Image: &invoke.ImageRef{URL: "https://example.com/pic.jpg"}},
				{Text: "这是什么？"},
			},
		}},
		MaxCompletionTokens: 128,
	})
	require.NoError(t, err)
	require.Equal(t, "https://dashscope.aliyuncs.com/api/v1/services/aigc/multimodal-generation/generation", req.URL)

	var body struct {
		Input struct {
			Messages []struct {
				Content []struct {
					Image string `json:"image"`
					Text  string `json:"text"`
				} `json:"content"`
			} `json:"messages"`
		} `json:"input"`
		Parameters struct {
			MaxCompletionTokens int `json:"max_completion_tokens"`
			MaxTokens           int `json:"max_tokens"`
		} `json:"parameters"`
	}
	require.NoError(t, json.Unmarshal(req.Body, &body))
	parts := body.Input.Messages[0].Content
	require.Equal(t, "https://example.com/pic.jpg", parts[0].Image, "原生内容部件是 {image}, 不是 image_url")
	require.Equal(t, "这是什么？", parts[1].Text)
	require.Equal(t, 128, body.Parameters.MaxTokens, "视觉分支走通用的 max_tokens")
	require.Equal(t, 0, body.Parameters.MaxCompletionTokens)
}

func TestAliyunParseChatResponse(t *testing.T) {
	a := newAliyunAdapter()
	body := `{"request_id":"r1","output":{"choices":[{"finish_reason":"tool_calls","message":{` +
		`"role":"assistant","content":"","reasoning_content":"思考中……",` +
		`"tool_calls":[{"id":"call_1","type":"function","index":0,` +
		`"function":{"name":"get_weather","arguments":"{\"city\":\"hangzhou\"}"}}]}}]},` +
		`"usage":{"input_tokens":30,"output_tokens":12,"total_tokens":42,` +
		`"input_tokens_details":{"cached_tokens":8}}}`
	resp, err := a.ParseChatResponse(200, nil, []byte(body))
	require.NoError(t, err)
	require.Equal(t, "", resp.Content)
	require.Equal(t, "思考中……", resp.ReasoningContent, "多轮回传通道（qwen3.8 preserve_thinking）")
	require.Equal(t, "tool_calls", resp.FinishReason)
	require.Len(t, resp.ToolCalls, 1)
	require.Equal(t, "get_weather", resp.ToolCalls[0].Function.Name)
	require.Equal(t, 30, resp.Usage.PromptTokens)
	require.Equal(t, 12, resp.Usage.CompletionTokens)
	require.Equal(t, 42, resp.Usage.TotalTokens)
	require.Equal(t, 8, resp.Usage.CacheReadTokens, "cached_tokens 并入 prompt-cache 细节")
	require.True(t, resp.Usage.CacheReported)
}

func TestAliyunTranslateStreamEventSequence(t *testing.T) {
	a := newAliyunAdapter()
	state := invoke.NewStreamBridgeState()

	// 思考增量。
	ev, err := a.TranslateStreamEvent(state, invoke.StreamChunk{Data: []byte(
		`{"output":{"choices":[{"finish_reason":null,"message":{"role":"assistant",` +
			`"content":"","reasoning_content":"想一下"}}]}}`)})
	require.NoError(t, err)
	require.NotNil(t, ev)
	require.Equal(t, invoke.StreamKindThinking, ev.Kind)
	require.Equal(t, "想一下", ev.Delta.Text)

	// 回答增量。
	ev, err = a.TranslateStreamEvent(state, invoke.StreamChunk{Data: []byte(
		`{"output":{"choices":[{"finish_reason":null,"message":{"role":"assistant",` +
			`"content":"答案"}}]}}`)})
	require.NoError(t, err)
	require.Equal(t, invoke.StreamKindAnswer, ev.Kind)
	require.Equal(t, "答案", ev.Delta.Text)

	// 末帧：finish_reason + usage → Done 事件自带 usage（无 [DONE] 哨兵）。
	ev, err = a.TranslateStreamEvent(state, invoke.StreamChunk{Data: []byte(
		`{"output":{"choices":[{"finish_reason":"stop","message":{"role":"assistant",` +
			`"content":""}}]},"usage":{"input_tokens":5,"output_tokens":2,"total_tokens":7}}`)})
	require.NoError(t, err)
	require.NotNil(t, ev.Done)
	require.Equal(t, "stop", ev.Done.FinishReason)
	require.NotNil(t, ev.Usage)
	require.Equal(t, 5, ev.Usage.PromptTokens)
	require.Equal(t, 2, ev.Usage.CompletionTokens)
}

func TestAliyunTranslateStreamEventToolCalls(t *testing.T) {
	a := newAliyunAdapter()
	state := invoke.NewStreamBridgeState()

	ev, err := a.TranslateStreamEvent(state, invoke.StreamChunk{Data: []byte(
		`{"output":{"choices":[{"finish_reason":null,"message":{"role":"assistant",` +
			`"tool_calls":[{"index":0,"id":"call_1","type":"function",` +
			`"function":{"name":"get_weather","arguments":"{}"}}]}}]}}`)})
	require.NoError(t, err)
	require.Equal(t, invoke.StreamKindToolCall, ev.Kind)
	require.Equal(t, "get_weather", ev.ToolCallDelta.Name)

	ev, err = a.TranslateStreamEvent(state, invoke.StreamChunk{Data: []byte(
		`{"output":{"choices":[{"finish_reason":"tool_calls","message":{"role":"assistant",` +
			`"content":""}}]},"usage":{"input_tokens":9,"output_tokens":4,"total_tokens":13}}`)})
	require.NoError(t, err)
	require.NotNil(t, ev.Done)
	require.Equal(t, "tool_calls", ev.Done.FinishReason)
	require.Len(t, ev.Done.ToolCalls, 1)
	require.Equal(t, "call_1", ev.Done.ToolCalls[0].ID)
}

func TestAliyunListFacet(t *testing.T) {
	a := newAliyunAdapter()
	req, err := a.BuildListRequest(invoke.Endpoint{Credentials: invoke.Credentials{APIKey: "sk"}})
	require.NoError(t, err)
	require.Equal(t, http.MethodGet, req.Method)
	require.Equal(t, "https://dashscope.aliyuncs.com/api/v1/models?capabilities=TG&page_no=1&page_size=100", req.URL)
	require.Equal(t, "Bearer sk", req.Header.Get("Authorization"))

	models, err := a.ParseListResponse(200, nil, []byte(`{"request_id":"r","output":{`+
		`"total":2,"page_no":1,"page_size":100,"models":[`+
		`{"model":"qwen3-max","name":"通义千问3-Max","model_info":{"context_window":262144,"max_output_tokens":65536}},`+
		`{"model":"","name":"skipped"}]}}`))
	require.NoError(t, err)
	require.Len(t, models, 1)
	require.Equal(t, "qwen3-max", models[0].ID)
	require.Equal(t, "通义千问3-Max", models[0].DisplayName)
	require.Equal(t, 262144, models[0].ContextWindow)
	require.Equal(t, 65536, models[0].MaxOutputTokens)
}

func TestAliyunEmbeddingTextNativeWire(t *testing.T) {
	req, err := buildAliyunEmbedding(
		invoke.Endpoint{
			BaseURL:     "https://dashscope.aliyuncs.com/compatible-mode/v1",
			Credentials: invoke.Credentials{APIKey: "sk"},
		},
		"text-embedding-v4",
		&invoke.EmbeddingOptions{Inputs: []string{"a", "b"}, SupportsDimensionOverride: true, Dimensions: 512},
	)
	require.NoError(t, err)
	require.Equal(t, "https://dashscope.aliyuncs.com/api/v1/services/embeddings/text-embedding/text-embedding", req.URL)
	require.Equal(t, "Bearer sk", req.Header.Get("Authorization"))

	var body struct {
		Model string `json:"model"`
		Input struct {
			Texts []string `json:"texts"`
		} `json:"input"`
		Parameters *struct {
			Dimension int `json:"dimension"`
		} `json:"parameters"`
	}
	require.NoError(t, json.Unmarshal(req.Body, &body))
	require.Equal(t, "text-embedding-v4", body.Model)
	require.Equal(t, []string{"a", "b"}, body.Input.Texts)
	require.NotNil(t, body.Parameters)
	require.Equal(t, 512, body.Parameters.Dimension)
}
