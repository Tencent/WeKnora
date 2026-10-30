package openaicompletions

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/api"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMessageContent_StringOrParts(t *testing.T) {
	cases := []struct {
		name, in, text, thinking string
	}{
		{"string", `"hi"`, "hi", ""},
		{"null", `null`, "", ""},
		{"text parts", `[{"type":"text","text":"Hel"},{"type":"text","text":"lo"}]`, "Hello", ""},
		{
			"mistral thinking part",
			`[{"type":"thinking","thinking":[{"type":"text","text":"let me see"}]},{"type":"text","text":"answer"}]`,
			"answer", "let me see",
		},
		{
			"reference part carries no text",
			`[{"type":"reference","reference_ids":[1]},{"type":"text","text":"a"}]`,
			"a", "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var m rawMessage
			require.NoError(t, json.Unmarshal([]byte(`{"content":`+tc.in+`}`), &m))
			assert.Equal(t, tc.text, m.Content.Text)
			assert.Equal(t, tc.thinking, m.Content.Thinking)
		})
	}
}

// Mistral types delta.content as string | ContentChunk[]. A chunk sent as a
// list of parts used to fail the whole stream with "cannot unmarshal array".
func TestChatStream_ContentPartsAreDecoded(t *testing.T) {
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(
			`data: {"choices":[{"index":0,"delta":{"role":"assistant","content":` +
				`[{"type":"thinking","thinking":[{"type":"text","text":"plan"}]}]}}]}` + "\n\n" +
				`data: {"choices":[{"index":0,"delta":{"content":[{"type":"text","text":"Hel"}]}}]}` + "\n\n" +
				`data: {"choices":[{"index":0,"delta":{"content":"lo"},"finish_reason":"stop"}]}` + "\n\n" +
				"data: [DONE]\n\n"))
	}))
	defer server.Close()

	c := New(Config{
		Endpoint: api.Endpoint{BaseURL: server.URL + "/v1", Model: "m", Auth: api.BearerAuth("sk")},
		Settings: api.DefaultOpenAICompletions(),
	})
	ch, err := c.ChatStream(context.Background(), []api.Message{{Role: "user", Content: "hi"}}, nil)
	require.NoError(t, err)

	var thinking, answer strings.Builder
	for chunk := range ch {
		switch chunk.ResponseType {
		case types.ResponseTypeError:
			t.Fatalf("unexpected error: %s", chunk.Content)
		case types.ResponseTypeThinking:
			thinking.WriteString(chunk.Content)
		case types.ResponseTypeAnswer:
			answer.WriteString(chunk.Content)
		}
	}
	assert.Equal(t, "plan", thinking.String())
	assert.Equal(t, "Hello", answer.String())
}

func TestChat_NonStreamContentParts(t *testing.T) {
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"index":0,"message":{"role":"assistant","content":` +
			`[{"type":"thinking","thinking":[{"type":"text","text":"deep"}]},{"type":"text","text":"answer"}]},` +
			`"finish_reason":"stop"}]}`))
	}))
	defer server.Close()

	c := New(Config{
		Endpoint: api.Endpoint{BaseURL: server.URL + "/v1", Model: "m", Auth: api.BearerAuth("sk")},
		Settings: api.DefaultOpenAICompletions(),
	})
	resp, err := c.Chat(context.Background(), []api.Message{{Role: "user", Content: "hi"}}, nil)
	require.NoError(t, err)
	assert.Equal(t, "answer", resp.Content)
	assert.Equal(t, "deep", resp.ReasoningContent)
}
