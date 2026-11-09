package chat

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestRemoteGeminiChatAcceptsListedModelResource(t *testing.T) {
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1beta/models/gemini-2.5-pro:generateContent" {
			http.Error(w, "unknown model resource", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"candidates":[{"content":{"role":"model","parts":[{"text":"Hello"}]},`+
			`"finishReason":"STOP"}]}`)
	}))
	defer server.Close()
	model := &types.Model{
		ID: "listed-gemini", Name: "models/gemini-2.5-pro",
		Type: types.ModelTypeKnowledgeQA, Source: types.ModelSourceRemote,
		Parameters: types.ModelParameters{Provider: "gemini", BaseURL: server.URL, APIKey: "synthetic-test-key"},
	}
	client, err := NewRemoteChat(ConfigFromModel(model, "", ""))
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Chat(context.Background(), []Message{{Role: "user", Content: "Say hello"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if response.Content != "Hello" {
		t.Fatalf("response = %#v", response)
	}
}
