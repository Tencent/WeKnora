package adapters

// golden_weknoracloud_reconcile_test.go — P1b reconciliation, WeKnoraCloud
// chat facet (v1 chat/golden_vendor_adapters2_test.go scenarios 24/25).
// Signature headers are masked in the golden baseline; HMAC correctness is
// asserted in-test by recomputing the signature over the captured body
// (signAssertion — the body-MD5 dependency is the hard constraint).

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/invoke"
	"github.com/stretchr/testify/require"
)

func newGoldenWeKnoraCloud(t *testing.T, g *reconcileServer) *invoke.ModelConfig {
	return newGoldenModelConfig(t, g.Server.URL, "weknoracloud", "weknora-chat", func(m *invoke.ModelConfig) {
		m.Credentials.AppID = "app-123"
		m.Credentials.AppSecret = "secret-456"
	})
}

// 场景 24：WeKnoraCloud 签名请求（非流式）。
func TestReconcileWeKnoraCloudSignedChat(t *testing.T) {
	allowLoopbackSSRF(t)
	g := newReconcileServer(t, jsonHandler(200, goldenOpenAIPlainResponse))
	m := newGoldenWeKnoraCloud(t, g)

	_, err := invoke.Chat(context.Background(), m, &invoke.ChatOptions{
		Messages:            []invoke.Message{textMsg("user", "Hi")},
		Temperature:         0.7,
		MaxCompletionTokens: 128,
	})
	require.NoError(t, err)
	signAssertion(t, g, 0, "app-123", "secret-456")
	assertRequestsMatchGolden(t, "weknoracloud_signed_chat", g)
}

// 场景 25：TransformMessages 多模态降级 + 协议字段保留。
func TestReconcileWeKnoraCloudMultimodalDowngrade(t *testing.T) {
	allowLoopbackSSRF(t)
	g := newReconcileServer(t, jsonHandler(200, goldenOpenAIPlainResponse))
	m := newGoldenWeKnoraCloud(t, g)

	_, err := invoke.Chat(context.Background(), m, &invoke.ChatOptions{
		Messages: []invoke.Message{
			textMsg("system", "You are helpful."),
			{
				Role: "user",
				Content: []invoke.Part{
					{Text: "Look at this:"},
					{Image: &invoke.ImageRef{URL: "data:image/png;base64,AAAA"}},
					{Text: "And this."},
				},
			},
			{
				Role: "assistant",
				ToolCalls: []invoke.ToolCall{{
					ID:       "call_1",
					Type:     "function",
					Function: invoke.FunctionCall{Name: "lookup", Arguments: `{"q":"x"}`},
				}},
			},
			{Role: "tool", Name: "lookup", ToolCallID: "call_1", Content: []invoke.Part{{Text: `{"result":"ok"}`}}},
		},
		MaxCompletionTokens: 128,
	})
	require.NoError(t, err)
	signAssertion(t, g, 0, "app-123", "secret-456")
	assertRequestsMatchGolden(t, "weknoracloud_multimodal_downgrade", g)
}
