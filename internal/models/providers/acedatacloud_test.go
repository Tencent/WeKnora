package providers_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/api"
	"github.com/Tencent/WeKnora/internal/models/api/anthropicmessages"
	"github.com/Tencent/WeKnora/internal/models/providers"
	modelruntime "github.com/Tencent/WeKnora/internal/models/runtime"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAceDataCloudNativeMessages(t *testing.T) {
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		assert.Equal(t, "/v1/messages", req.URL.Path)
		assert.Equal(t, "Bearer local-test-key", req.Header.Get("Authorization"))
		assert.Empty(t, req.Header.Get("x-api-key"))
		var body map[string]any
		assert.NoError(t, json.NewDecoder(req.Body).Decode(&body))
		assert.Equal(t, "claude-sonnet-5-5", body["model"])
		assert.Equal(t, float64(12), body["max_tokens"])
		assert.NotContains(t, body, "thinking")
		assert.NotContains(t, body, "output_config")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"test","type":"message","role":"assistant","model":"claude-sonnet-5-5",
			"content":[{"type":"text","text":"hello"}],"stop_reason":"end_turn",
			"usage":{"input_tokens":1,"output_tokens":1}
		}`))
	}))
	defer server.Close()
	r, err := modelruntime.Resolve(modelruntime.Ref{
		Provider: providers.AceDataCloudID, Model: "claude-sonnet-5-5", BaseURL: server.URL + "/v1",
	})
	require.NoError(t, err)
	require.Equal(t, api.APIAnthropicMessages, r.API)
	require.Empty(t, r.Capabilities().ThinkingLevels)
	ep, err := r.Endpoint(types.ModelTypeKnowledgeQA, modelruntime.Connection{
		Credentials: api.Credentials{APIKey: "local-test-key"}, Client: server.Client(),
	})
	require.NoError(t, err)
	c := anthropicmessages.New(anthropicmessages.Config{
		Endpoint: ep, Settings: r.AnthropicMessages, ThinkingLevels: r.ThinkingLevels, Reasoning: r.Spec.Reasoning,
	})
	response, err := c.Chat(context.Background(), []api.Message{{Role: "user", Content: "hello"}}, &api.Options{
		MaxTokens: 12, ReasoningEffort: api.ReasoningHigh,
	})
	require.NoError(t, err)
	require.Equal(t, "hello", response.Content)
}

func TestAceDataCloudProtocolAndOperatorOverrides(t *testing.T) {
	models := []string{"claude-opus-5-5", "gpt-4o-image", "gpt-5-all", "gpt-5.1-all", "gpt-6.2-sol", "glm-4.5v"}
	for _, id := range models {
		r, err := modelruntime.Resolve(modelruntime.Ref{Provider: providers.AceDataCloudID, Model: id})
		require.NoError(t, err)
		require.True(t, r.Cataloged, id)
		require.Equal(t, api.APIOpenAICompletions, r.API, id)
	}
	r, err := modelruntime.Resolve(modelruntime.Ref{
		Provider: providers.AceDataCloudID, Model: "claude-sonnet-5-5", BaseURL: "https://custom.example/v1",
		Override: &types.ModelSpecOverride{API: string(api.APIOpenAICompletions), ContextWindow: 1234},
	})
	require.NoError(t, err)
	require.Equal(t, api.APIOpenAICompletions, r.API)
	require.Equal(t, "https://custom.example/v1", r.BaseURL)
	require.Equal(t, 1234, r.Spec.ContextWindow)
}

func TestAceDataCloudCatalogGenerator(t *testing.T) {
	command := exec.Command("python3", "-m", "unittest", "discover", "-s",
		filepath.Join("..", "..", "..", "scripts", "model-catalog"), "-p", "test_*.py")
	output, err := command.CombinedOutput()
	require.NoError(t, err, string(output))
}
