package adapters

// golden_ollama_replay_test.go: P1b reconciliation for the two ollama golden
// scenarios plus the new ListModels facet (design §7.1, GET /api/tags).
//
// v2 显式变更（§11，非漂移）：调用链不再做心跳/EnsureModelAvailable 自动拉取，
// golden 里被过滤掉的 /api/tags+HEAD 前置请求不复存在；/api/chat 请求体
// 逐字节对账不受影响。流式场景 v1 录制时 requests 为 null（录制缺口），
// 请求体改为就地捕获后直接断言 stream/think/options 字段。

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/invoke"
	"github.com/stretchr/testify/require"
)

func ollamaConfig(t *testing.T, baseURL string) *invoke.ModelConfig {
	return newGoldenModelConfig(t, baseURL, "ollama", "testmodel", func(m *invoke.ModelConfig) {
		m.Credentials = invoke.Credentials{} // ollama 凭证可空（匿名可达）
	})
}

func ollamaMessages() []invoke.Message {
	return []invoke.Message{
		{Role: "system", Content: []invoke.Part{{Text: "You are local."}}},
		{Role: "user", Content: []invoke.Part{{Text: "Hi"}}},
	}
}

// 场景 31 对账：非流式（options: temperature 恒发 / num_predict；
// v1 usage 口径 completion = eval_count - prompt_eval_count，含 -5 负值实录）。
func TestGoldenReplayOllamaPlainChat(t *testing.T) {
	allowLoopbackSSRF(t)
	g := newReconcileServer(t, jsonHandler(200,
		`{"model":"testmodel","created_at":"2026-01-01T00:00:00Z",`+
			`"message":{"role":"assistant","content":"Hello"},"done_reason":"stop",`+
			`"done":true,"prompt_eval_count":10,"eval_count":5}`))

	resp, err := invoke.Chat(context.Background(), ollamaConfig(t, g.Server.URL), &invoke.ChatOptions{
		Messages:            ollamaMessages(),
		Temperature:         0.7,
		MaxCompletionTokens: 64,
	})
	require.NoError(t, err)

	assertRequestsMatchGolden(t, "ollama_plain_chat", g)

	require.Equal(t, "Hello", resp.Content)
	require.Equal(t, 10, resp.Usage.PromptTokens)
	require.Equal(t, -5, resp.Usage.CompletionTokens) // v1 口径如实录
	require.Equal(t, 5, resp.Usage.TotalTokens)
}

// rawCaptureServer 记录原始请求（path/header/body）后放行 handler。
type rawCaptureServer struct {
	*httptest.Server
	path   string
	header http.Header
	body   []byte
}

func newRawCaptureServer(t *testing.T, handler http.HandlerFunc) *rawCaptureServer {
	t.Helper()
	c := &rawCaptureServer{}
	c.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		c.path, c.header, c.body = r.URL.Path, r.Header.Clone(), body
		handler(w, r)
	}))
	t.Cleanup(c.Close)
	return c
}

// 场景 32 对账：流式（NDJSON + think 思考流 + done chunk usage）。
// 入口级对账：thinking/answer/done 事件序列；usage 数值由适配器级断言钉死
// （契约一条 chunk 一事件，Done 事件携带 usage 但入口映射当前丢弃，已上报；
// thinking 收尾 marker 同为已上报的入口层差异，比较时剔除）。
func TestGoldenReplayOllamaStream(t *testing.T) {
	allowLoopbackSSRF(t)
	golden := loadGoldenDoc(t, "ollama_stream")
	g := newRawCaptureServer(t, ndjsonHandler(golden.SSE))

	on := true
	// 无 caller deadline：默认超时流式路径回归（executor cancel 绑定
	// stream 生命周期，骨架缺陷已修复）。
	ch, err := invoke.ChatStream(context.Background(), ollamaConfig(t, g.URL), &invoke.ChatOptions{
		Messages:            []invoke.Message{{Role: "user", Content: []invoke.Part{{Text: "Hi"}}}},
		MaxCompletionTokens: 64,
		Thinking:            &on,
	})
	require.NoError(t, err)
	chunks := collectStream(t, ch)

	var body struct {
		Model    string         `json:"model"`
		Stream   *bool          `json:"stream"`
		Think    *bool          `json:"think"`
		Options  map[string]any `json:"options"`
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	require.NoError(t, json.Unmarshal(g.body, &body))
	require.Equal(t, "/api/chat", g.path)
	require.Equal(t, "application/x-ndjson", g.header.Get("Accept"))
	require.Equal(t, "testmodel", body.Model)
	require.NotNil(t, body.Stream)
	require.True(t, *body.Stream)
	require.NotNil(t, body.Think)
	require.True(t, *body.Think)
	require.Equal(t, float64(64), body.Options["num_predict"])
	require.Len(t, body.Messages, 1)

	// 客户端事件序列对账（usage 除外，见函数注释）。
	require.Equal(t, stripUsage(goldenStreamView(t, golden.Client)), stripUsage(normalizeStream(t, chunks)))
}

// 适配器级钉死：done 行 usage 口径（prompt=PromptEvalCount,
// completion=EvalCount——与非流式的减法口径不同）。
func TestOllamaStreamDoneCarriesUsage(t *testing.T) {
	a := &OllamaAdapter{}
	ev, err := a.TranslateStreamEvent(invoke.NewStreamBridgeState(), invoke.StreamChunk{Data: []byte(
		`{"model":"testmodel","message":{"role":"assistant","content":""},` +
			`"done_reason":"stop","done":true,"prompt_eval_count":12,"eval_count":7}`)})
	require.NoError(t, err)
	require.NotNil(t, ev.Done)
	require.Equal(t, 12, ev.Usage.PromptTokens)
	require.Equal(t, 7, ev.Usage.CompletionTokens)
	require.Equal(t, 19, ev.Usage.TotalTokens)
}

// ListModels 分面（design §7.1，P1b 新建）：GET /api/tags → RemoteModel。
// 字段映射 schema：qualified "model" tag（如 llama3:8b）→ ID（目录模型 ID）、
// "name" → DisplayName；ollama 不暴露 context/output 上限，恒 0。
func TestOllamaListModels(t *testing.T) {
	allowLoopbackSSRF(t)
	g := newRawCaptureServer(t, jsonHandler(200,
		`{"models":[{"name":"testmodel:latest","model":"testmodel:latest","size":1},`+
			`{"name":"llama3","model":"llama3:8b","size":2}]}`))

	models, err := invoke.List(context.Background(), "ollama", &invoke.ListOptions{BaseURL: g.URL})
	require.NoError(t, err)

	require.Equal(t, "/api/tags", g.path)
	require.Equal(t, "application/json", g.header.Get("Accept"))

	require.Len(t, models, 2)
	require.Equal(t, "testmodel:latest", models[0].ID)
	require.Equal(t, "testmodel:latest", models[0].DisplayName)
	require.Equal(t, "llama3:8b", models[1].ID)
	require.Equal(t, "llama3", models[1].DisplayName)
	require.Zero(t, models[0].ContextWindow)
}
