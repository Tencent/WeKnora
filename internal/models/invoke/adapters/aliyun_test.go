package adapters

// aliyun_test.go — native DashScope wire tests (2026-09-12 native ruling).
// The golden reconcile suite (golden_vendor_reconcile_test.go /
// golden_embedding_reconcile_test.go) pins the wire bytes; this file covers
// the seams those scenarios don't reach: base normalization, budget-field
// strategy, the vision/tools gates, response/stream parsing (including the
// finish-frame tail fragment), and the native catalog facet.

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
		// 剥离只发生在 URL path 段——host 里含子串的主机必须原样通过，
		// 否则带 Bearer key 的请求会被重定向到错误主机（审查 P0 回归）。
		{"https://compatible-mode.example.com", "https://compatible-mode.example.com"},
		{"https://compatible-mode.example.com/compatible-mode/v1", "https://compatible-mode.example.com"},
		{
			"https://dashscope.aliyuncs.com/compatible-mode.example.com",
			"https://dashscope.aliyuncs.com/compatible-mode.example.com",
		},
	}
	for _, c := range cases {
		require.Equal(t, c.want, aliyunNativeBaseURL(c.in), "base %q", c.in)
	}
}

// 完成预算策略：思考模型（新一代）发 max_completion_tokens，其余发 max_tokens。
func TestAliyunBudgetFieldStrategy(t *testing.T) {
	a := newAliyunAdapter()
	build := func(model string) (maxCompletion, maxTokens int) {
		req, err := a.BuildChatRequest(invoke.Endpoint{}, model, &invoke.ChatOptions{
			Messages:            []invoke.Message{invoke.TextMessage("user", "hi")},
			MaxCompletionTokens: 64,
		})
		require.NoError(t, err)
		var body struct {
			Parameters struct {
				MaxCompletionTokens int `json:"max_completion_tokens"`
				MaxTokens           int `json:"max_tokens"`
			} `json:"parameters"`
		}
		require.NoError(t, json.Unmarshal(req.Body, &body))
		return body.Parameters.MaxCompletionTokens, body.Parameters.MaxTokens
	}
	mc, mt := build("qwen3-max")
	require.Equal(t, 64, mc)
	require.Equal(t, 0, mt)
	mc, mt = build("qwen2.5-72b-instruct")
	require.Equal(t, 64, mt, "老模型对 max_completion_tokens 会 400/静默忽略")
	require.Equal(t, 0, mc)
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
	require.Equal(t, "application/json", req.Header.Get("Accept"))
	require.Equal(t, "", req.Header.Get("X-DashScope-SSE"), "non-stream carries no SSE header")

	var body struct {
		Model string `json:"model"`
		Input struct {
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		} `json:"input"`
		Parameters struct {
			ResultFormat        string  `json:"result_format"`
			MaxCompletionTokens int     `json:"max_completion_tokens"`
			Temperature         float64 `json:"temperature"`
			EnableThinking      *bool   `json:"enable_thinking"`
		} `json:"parameters"`
	}
	require.NoError(t, json.Unmarshal(req.Body, &body))
	require.Equal(t, "qwen-plus", body.Model)
	require.Len(t, body.Input.Messages, 2)
	require.Equal(t, "be terse", body.Input.Messages[0].Content, "text path degrades to plain strings")
	require.Equal(t, "message", body.Parameters.ResultFormat)
	require.Equal(t, 32, body.Parameters.MaxCompletionTokens)
	require.Equal(t, 0.7, body.Parameters.Temperature)
	// qwen-plus 在 IsQwenThinkingModel 谓词内（qwen3/plus/max/turbo 前缀）：
	// 非流式钉 false（Qwen3 系非流式拒绝 thinking）。
	require.NotNil(t, body.Parameters.EnableThinking)
	require.False(t, *body.Parameters.EnableThinking)
	require.NotContains(t, string(req.Body), "cache_control")
	require.NotContains(t, string(req.Body), "frequency_penalty", "native schema 无此字段")
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

// tools 门控按模型名（qwen-vl/qwen-audio 排除），不按是否带图——文档的官方
// tools 示例就是在 multimodal-generation 端点上跑 qwen3.8-max。
func TestAliyunToolsGateByModelFamily(t *testing.T) {
	a := newAliyunAdapter()
	tools := []invoke.ToolDef{{Name: "get_weather", Description: "查天气"}}
	build := func(model string, images bool) (*invoke.Request, error) {
		msgs := []invoke.Message{invoke.TextMessage("user", "天气如何")}
		if images {
			msgs = []invoke.Message{{
				Role: "user",
				Content: []invoke.Part{
					{Image: &invoke.ImageRef{URL: "https://example.com/pic.jpg"}},
					{Text: "图里天气如何？"},
				},
			}}
		}
		return a.BuildChatRequest(invoke.Endpoint{}, model, &invoke.ChatOptions{
			Messages:   msgs,
			Tools:      tools,
			ToolChoice: "get_weather",
		})
	}
	assertTools := func(t *testing.T, req *invoke.Request, want bool) {
		t.Helper()
		var body struct {
			Parameters struct {
				Tools []json.RawMessage `json:"tools"`
			} `json:"parameters"`
		}
		require.NoError(t, json.Unmarshal(req.Body, &body))
		if want {
			require.Len(t, body.Parameters.Tools, 1)
		} else {
			require.Empty(t, body.Parameters.Tools)
		}
	}

	req, err := build("qwen-plus", false)
	require.NoError(t, err)
	assertTools(t, req, true)
	// 具名函数 tool_choice 走对象形态。
	var body struct {
		Parameters struct {
			ToolChoice struct {
				Type     string `json:"type"`
				Function struct {
					Name string `json:"name"`
				} `json:"function"`
			} `json:"tool_choice"`
		} `json:"parameters"`
	}
	require.NoError(t, json.Unmarshal(req.Body, &body))
	require.Equal(t, "function", body.Parameters.ToolChoice.Type)
	require.Equal(t, "get_weather", body.Parameters.ToolChoice.Function.Name)

	req, err = build("qwen3.8-max", true) // 视觉分支 + 新一代：tools 保留
	require.NoError(t, err)
	require.Equal(t, aliyunMultimodalGenerationPath, req.URL[len(req.URL)-len(aliyunMultimodalGenerationPath):])
	assertTools(t, req, true)

	req, err = build("qwen-vl-max", true) // qwen-vl 系：无 tools
	require.NoError(t, err)
	assertTools(t, req, false)
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
		`"prompt_tokens_details":{"cached_tokens":8}}}`
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
	require.Equal(t, 8, resp.Usage.CacheReadTokens, "cached_tokens（prompt_tokens_details）并入 prompt-cache 细节")
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

// 末帧同时携带最后一段增量（官方 incremental_output 语义：尾片段与
// finish_reason 同帧）——必须折入 Done.Delta，否则每条流式回答丢尾巴。
func TestAliyunFinishFrameCarriesTailFragment(t *testing.T) {
	a := newAliyunAdapter()
	state := invoke.NewStreamBridgeState()

	ev, err := a.TranslateStreamEvent(state, invoke.StreamChunk{Data: []byte(
		`{"output":{"choices":[{"finish_reason":null,"message":{"role":"assistant",` +
			`"content":"I like apple"}}]}}`)})
	require.NoError(t, err)
	require.Equal(t, "I like apple", ev.Delta.Text)

	ev, err = a.TranslateStreamEvent(state, invoke.StreamChunk{Data: []byte(
		`{"output":{"choices":[{"finish_reason":"stop","message":{"role":"assistant",` +
			`"content":"."}}]},"usage":{"input_tokens":4,"output_tokens":4,"total_tokens":8}}`)})
	require.NoError(t, err)
	require.NotNil(t, ev.Done)
	require.Equal(t, "stop", ev.Done.FinishReason)
	require.NotNil(t, ev.Delta, "末帧尾片段必须随 Done 事件透出")
	require.Equal(t, ".", ev.Delta.Text)
	require.NotNil(t, ev.Usage)
}

// 末帧携带完整 tool_calls 时先喂共享装配器——终结 Done 带全量调用，且
// 装配器存共享键（入口中断恢复读的就是它）。
func TestAliyunFinishFrameToolCallsRideSharedAssembler(t *testing.T) {
	a := newAliyunAdapter()
	state := invoke.NewStreamBridgeState()

	ev, err := a.TranslateStreamEvent(state, invoke.StreamChunk{Data: []byte(
		`{"output":{"choices":[{"finish_reason":"tool_calls","message":{"role":"assistant",` +
			`"content":"","tool_calls":[{"index":0,"id":"call_1","type":"function",` +
			`"function":{"name":"get_weather","arguments":"{}"}}]}}]}}`)})
	require.NoError(t, err)
	require.NotNil(t, ev.Done)
	require.Equal(t, "tool_calls", ev.Done.FinishReason)
	require.Len(t, ev.Done.ToolCalls, 1)
	require.Equal(t, "call_1", ev.Done.ToolCalls[0].ID)
	require.NotNil(t, invoke.ToolCallAssemblerFrom(state), "共享键必须可见（入口中断恢复依赖）")
}

// usage-only 帧（choices 空）→ StreamKindUsage，入口挂 pendingUsage。
func TestAliyunUsageOnlyFrame(t *testing.T) {
	a := newAliyunAdapter()
	state := invoke.NewStreamBridgeState()
	ev, err := a.TranslateStreamEvent(state, invoke.StreamChunk{Data: []byte(
		`{"output":{"choices":[]},"usage":{"input_tokens":9,"output_tokens":3,"total_tokens":12}}`)})
	require.NoError(t, err)
	require.Equal(t, invoke.StreamKindUsage, ev.Kind)
	require.Equal(t, 9, ev.Usage.PromptTokens)
}

// 裸 [DONE]（代理注入、无 SSE event 名）容忍并冲刷。
func TestAliyunBareDoneSentinel(t *testing.T) {
	a := newAliyunAdapter()
	state := invoke.NewStreamBridgeState()
	_, err := a.TranslateStreamEvent(state, invoke.StreamChunk{Data: []byte(
		`{"output":{"choices":[{"finish_reason":"stop","message":{"role":"assistant","content":""}}]}}`)})
	require.NoError(t, err)
	ev, err := a.TranslateStreamEvent(state, invoke.StreamChunk{Data: []byte("[DONE]")})
	require.NoError(t, err)
	require.NotNil(t, ev.Done)
	require.Equal(t, "stop", ev.Done.FinishReason)
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

// rerank 双协议 + base 规则：qwen3-rerank 走扁平 reranks，其余走 text-rerank
// 信封；base 含本协议端点路径则直用，否则归一回根拼协议路径。
func TestAliyunRerankWireDispatch(t *testing.T) {
	const root = "https://dashscope.aliyuncs.com"
	const textPath = "/api/v1/services/rerank/text-rerank/text-rerank"
	const flatPath = "/compatible-api/v1/reranks"

	cases := []struct {
		name, base, model, wantURL string
	}{
		{"envelope empty base", "", "gte-rerank-v2", root + textPath},
		{"envelope root base", root, "qwen3.7-text-rerank", root + textPath},
		{"envelope legacy compatible-mode base", root + "/compatible-mode/v1", "gte-rerank-v2", root + textPath},
		{"envelope full-endpoint base passes through", root + textPath, "gte-rerank-v2", root + textPath},
		{"flat empty base", "", "qwen3-rerank", root + flatPath},
		{"flat text-rerank prefill normalizes to root", root + textPath, "qwen3-rerank", root + flatPath},
		{"flat full-endpoint base passes through", root + flatPath, "qwen3-rerank", root + flatPath},
	}
	for _, c := range cases {
		req, err := buildAliyunRerank(
			invoke.Endpoint{BaseURL: c.base, Credentials: invoke.Credentials{APIKey: "sk"}},
			c.model, &invoke.RerankOptions{Query: "q", Documents: []string{"d1", "d2"}})
		require.NoError(t, err, c.name)
		require.Equal(t, c.wantURL, req.URL, c.name)
	}

	// 扁平请求体：query/documents 顶层、无 parameters、top_n 省略（厂商默认全量）。
	req, err := buildAliyunRerank(invoke.Endpoint{}, "qwen3-rerank",
		&invoke.RerankOptions{Query: "q", Documents: []string{"d1", "d2"}})
	require.NoError(t, err)
	var flat map[string]any
	require.NoError(t, json.Unmarshal(req.Body, &flat))
	require.Equal(t, "qwen3-rerank", flat["model"])
	require.Equal(t, "q", flat["query"])
	require.NotContains(t, flat, "input")
	require.NotContains(t, flat, "parameters")
	require.NotContains(t, flat, "top_n")

	// 解析分派：ParseRerankResponse 契约不带模型名——顶层 results 优先，
	// output.results 回落，两种信封都能吃。
	a := newAliyunAdapter()
	flatResp, err := a.ParseRerankResponse(200, nil,
		[]byte(`{"results":[{"index":1,"relevance_score":0.9}]}`))
	require.NoError(t, err)
	require.Equal(t, 1, flatResp.Results[0].Index)
	require.InDelta(t, 0.9, flatResp.Results[0].Score, 1e-9)
	envResp, err := a.ParseRerankResponse(200, nil,
		[]byte(`{"output":{"results":[{"index":0,"relevance_score":0.5}]}}`))
	require.NoError(t, err)
	require.Equal(t, 0, envResp.Results[0].Index)
	require.InDelta(t, 0.5, envResp.Results[0].Score, 1e-9)
}
