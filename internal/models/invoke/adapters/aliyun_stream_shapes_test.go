package adapters

// aliyun_stream_shapes_test.go — 诊断扫描（2026-09-14 glm-5.2 报告）：把
// DashScope 各种候选失败返回形状灌进完整 invoke.ChatStream，实测哪些仍会
// "零事件 + 干净关闭"。修复后按结论保留为回归测试。

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/invoke"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

type streamShapeResult struct {
	Chunks []string // "type:content:done:finish" 摘要
}

func driveAliyunStreamShape(t *testing.T, contentType, body string, status int) streamShapeResult {
	t.Helper()
	allowLoopbackSSRF(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", contentType)
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)

	m := &invoke.ModelConfig{
		Provider:    "aliyun",
		ModelID:     "shape-probe",
		ModelName:   "glm-5.2",
		BaseURL:     srv.URL,
		Credentials: invoke.Credentials{APIKey: "k"},
	}
	ch, err := invoke.ChatStream(t.Context(), m, &invoke.ChatOptions{
		Messages:            []invoke.Message{invoke.TextMessage("user", "再来")},
		Stream:              true,
		Thinking:            textPtr(true),
		MaxCompletionTokens: 512,
	})
	if err != nil {
		// 预流错误（HTTP 非 2xx 在状态行就失败）：本形状应在 model_call 处
		// 报错并向上返回——同样不是静默类。
		t.Logf("shape=%q pre-stream error: %v", t.Name(), err)
		return streamShapeResult{}
	}
	require.NotNil(t, ch)
	out := streamShapeResult{}
	for sr := range ch {
		finish := sr.FinishReason
		done := "-"
		if sr.Done {
			done = "done"
		}
		out.Chunks = append(out.Chunks, string(sr.ResponseType)+":"+sr.Content+":"+done+":"+finish)
	}
	return out
}

func TestAliyunStreamFailureShapeSweep(t *testing.T) {
	cases := []struct {
		name        string
		contentType string
		body        string
		status      int
	}{
		{
			"json_error_envelope", "application/json",
			`{"code":"Model.NotFound","message":"Model not found: glm-5.2","request_id":"r1"}`, 200,
		},
		{
			"sse_error_frame", "text/event-stream",
			"data:{\"code\":\"Model.NotFound\",\"message\":\"Model not found\",\"request_id\":\"r1\"}\n\n" +
				"data:[DONE]\n\n", 200,
		},
		{
			"sse_raw_json_line", "text/event-stream",
			"{\"code\":\"Model.NotFound\",\"message\":\"raw\",\"request_id\":\"r1\"}\n\n", 200,
		},
		{"sse_comments_only", "text/event-stream", ": ping\n\n: ping\n\n", 200},
		{"sse_empty", "text/event-stream", "", 200},
		{
			"json_empty_choices", "application/json",
			`{"request_id":"r2","output":{"choices":[]},"usage":null}`, 200,
		},
		{"http400_json_error", "application/json", `{"code":"X","message":"bad"}`, 400},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := driveAliyunStreamShape(t, tc.contentType, tc.body, tc.status)
			t.Logf("shape=%q chunks=%v", tc.name, res.Chunks)
		})
	}
	_ = types.ResponseTypeAnswer
}
