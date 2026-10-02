package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/utils"
	"github.com/stretchr/testify/require"
)

// fakeChatGateway answers every chat completion with the given status and body.
func fakeChatGateway(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	// The test server is a bare loopback IP; let the SSRF guard through.
	utils.SetSSRFWhitelistFromRaw("127.0.0.1")
	t.Cleanup(func() { utils.SetSSRFWhitelistFromRaw("") })
	return srv
}

func remoteChatModel(baseURL string) *types.Model {
	return &types.Model{
		Name:   "gpt-5.6-luna",
		Type:   types.ModelTypeKnowledgeQA,
		Source: types.ModelSourceRemote,
		Parameters: types.ModelParameters{
			BaseURL: baseURL,
			APIKey:  "sk-test",
		},
	}
}

// A reasoning model behind LiteLLM cannot finish within the 1-token probe and
// the gateway answers 400. The endpoint is reachable and the key accepted, so
// the check must pass, as it did before the HTTP client changed its wording.
func TestCheckChatModelConnectionTreats400AsReachable(t *testing.T) {
	srv := fakeChatGateway(t, http.StatusBadRequest,
		`{"error":{"message":"litellm.BadRequestError: OpenAIException - Could not finish the message `+
			`because max_tokens or model output limit was reached.","type":"invalid_request_error","code":"400"}}`)

	h := &InitializationHandler{}
	available, message := h.checkChatModelConnection(context.Background(), remoteChatModel(srv.URL), "", "")

	require.True(t, available, "message: %s", message)
}

func TestCheckChatModelConnectionReportsAuthFailure(t *testing.T) {
	srv := fakeChatGateway(t, http.StatusUnauthorized,
		`{"error":{"message":"Invalid API key","type":"invalid_request_error","code":"401"}}`)

	h := &InitializationHandler{}
	available, message := h.checkChatModelConnection(context.Background(), remoteChatModel(srv.URL), "", "")

	require.False(t, available)
	require.Contains(t, message, "认证失败")
}
