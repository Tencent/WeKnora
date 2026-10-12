package chat

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/utils/ollama"
	secutils "github.com/Tencent/WeKnora/internal/utils"
)

func TestResolveImageForOllamaRejectsInternalURL(t *testing.T) {
	t.Setenv("SSRF_WHITELIST", "")
	secutils.ResetSSRFWhitelistForTest()
	t.Cleanup(secutils.ResetSSRFWhitelistForTest)

	if data := resolveImageForOllama("http://169.254.169.254/latest/meta-data/"); data != nil {
		t.Fatalf("resolveImageForOllama returned data for blocked internal URL")
	}
}

func TestResolveImageForOllamaBlocksRedirectToInternalURL(t *testing.T) {
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")
	secutils.ResetSSRFWhitelistForTest()
	t.Cleanup(secutils.ResetSSRFWhitelistForTest)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://169.254.169.254/latest/meta-data/", http.StatusFound)
	}))
	defer server.Close()

	if data := resolveImageForOllama(server.URL); data != nil {
		t.Fatalf("resolveImageForOllama returned data after redirect to blocked internal URL")
	}
}

// Graph extraction tells a prose refusal from an answer cut off by num_predict
// through FinishReason, so the non-streaming Chat must carry Ollama's
// done_reason instead of leaving it empty.
func TestOllamaChatPropagatesDoneReason(t *testing.T) {
	for _, reason := range []string{"stop", "length"} {
		t.Run(reason, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/":
					w.WriteHeader(http.StatusOK)
				case "/api/tags":
					_, _ = fmt.Fprint(w, `{"models":[{"name":"m:latest"}]}`)
				case "/api/chat":
					_, _ = fmt.Fprintf(w, `{"model":"m","message":{"role":"assistant","content":"Step 1"},`+
						`"done":true,"done_reason":%q,"eval_count":3}`, reason)
				default:
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			t.Setenv("OLLAMA_BASE_URL", server.URL)
			t.Setenv("OLLAMA_OPTIONAL", "")
			svc, err := ollama.GetOllamaService()
			if err != nil {
				t.Fatal(err)
			}

			c := &OllamaChat{modelName: "m", ollamaService: svc}
			resp, err := c.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}}, &ChatOptions{})
			if err != nil {
				t.Fatal(err)
			}
			if resp.FinishReason != reason {
				t.Fatalf("FinishReason = %q, want %q", resp.FinishReason, reason)
			}
		})
	}
}
