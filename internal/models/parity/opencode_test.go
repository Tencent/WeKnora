package parity

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestOpencodeRequestRoute pins where the OpenCode presets send a chat call
// and with which credential. Zen and Go document each model on exactly one
// route under their OpenAI-compatible base (https://opencode.ai/docs/zen/,
// https://opencode.ai/docs/go/): GPT / Grok / Muse on /responses, the rest on
// /chat/completions, all with `Authorization: Bearer`. The Chat Completions
// route carries the cap as `max_tokens`, the field @ai-sdk/openai-compatible
// sends; Responses keeps `max_output_tokens`.
func TestOpencodeRequestRoute(t *testing.T) {
	var gotPath, gotAuth string
	var gotBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotAuth = r.URL.Path, r.Header.Get("Authorization")
		gotBody = nil
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}],` +
			`"output":[{"type":"message","content":[{"type":"output_text","text":"ok"}]}]}`))
	}))
	defer server.Close()

	const zen, goBase = "/inference/openai/v1", "/inference/go/openai/v1"
	for _, tc := range []struct {
		provider, model, base, route, wantCap string
	}{
		{"opencode_zen", "kimi-k3", zen, "/chat/completions", "max_tokens"},
		{"opencode_zen", "gpt-6.1-sol", zen, "/responses", "max_output_tokens"},
		{"opencode_go", "glm-5.3", goBase, "/chat/completions", "max_tokens"},
		{"opencode_go", "grok-4.7", goBase, "/responses", "max_output_tokens"},
	} {
		t.Run(tc.provider+"/"+tc.model, func(t *testing.T) {
			gotPath, gotAuth = "", ""
			client, err := chat.NewRemoteChat(&chat.ChatConfig{
				Source: types.ModelSourceRemote, Provider: tc.provider,
				ModelName: tc.model, BaseURL: server.URL + tc.base, APIKey: "KEY",
			})
			require.NoError(t, err)
			_, err = client.Chat(context.Background(), []chat.Message{{Role: "user", Content: "hi"}},
				&chat.ChatOptions{MaxTokens: 256})
			require.NoError(t, err)

			assert.Equal(t, tc.base+tc.route, gotPath)
			assert.Equal(t, "Bearer KEY", gotAuth)
			assert.EqualValues(t, 256, gotBody[tc.wantCap], "body=%v", gotBody)
			for _, other := range []string{"max_tokens", "max_completion_tokens", "max_output_tokens"} {
				if other != tc.wantCap {
					assert.NotContains(t, gotBody, other)
				}
			}
		})
	}
}
