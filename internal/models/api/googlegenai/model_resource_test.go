package googlegenai

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/api"
	"github.com/Tencent/WeKnora/internal/types"
)

// models.list returns Model.name in models/{model} form. Both that resource
// name and the bare ID must reach the same native generateContent endpoint.
func TestChatAcceptsModelResourceNames(t *testing.T) {
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")
	for _, model := range []string{"gemini-2.5-pro", "models/gemini-2.5-pro"} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%t", model, stream), func(t *testing.T) {
				method := "generateContent"
				if stream {
					method = "streamGenerateContent"
				}
				expectedPath := "/v1beta/models/gemini-2.5-pro:" + method
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path != expectedPath {
						http.Error(w, "unknown model resource", http.StatusNotFound)
						return
					}
					if stream && r.URL.Query().Get("alt") != "sse" {
						t.Errorf("missing SSE query: %s", r.URL.RawQuery)
					}
					payload := `{"candidates":[{"content":{"role":"model","parts":[{"text":"Hello"}]},` +
						`"finishReason":"STOP"}]}`
					if stream {
						w.Header().Set("Content-Type", "text/event-stream")
						_, _ = fmt.Fprintf(w, "data: %s\n\n", payload)
					} else {
						w.Header().Set("Content-Type", "application/json")
						_, _ = fmt.Fprint(w, payload)
					}
				}))
				defer srv.Close()
				client := newTestClient(t, srv.URL, func(cfg *Config) { cfg.Endpoint.Model = model })
				messages := []api.Message{{Role: "user", Content: "Say hello"}}
				if stream {
					events, err := client.ChatStream(context.Background(), messages, nil)
					if err != nil {
						t.Fatal(err)
					}
					var text strings.Builder
					for event := range events {
						if event.ResponseType == types.ResponseTypeAnswer {
							text.WriteString(event.Content)
						}
						if event.ResponseType == types.ResponseTypeError {
							t.Fatalf("stream error: %#v", event)
						}
					}
					if text.String() != "Hello" {
						t.Fatalf("answer = %q", text.String())
					}
				} else {
					response, err := client.Chat(context.Background(), messages, nil)
					if err != nil {
						t.Fatal(err)
					}
					if response.Content != "Hello" {
						t.Fatalf("answer = %q", response.Content)
					}
				}
				if client.GetModelName() != model {
					t.Fatal("URL normalization must not rewrite the configured model identity")
				}
			})
		}
	}
}

func TestModelResourceNamePreservesEndpointConfiguration(t *testing.T) {
	cases := []struct{ name, base, override, want string }{
		{
			"versioned_proxy", "https://proxy.example.com/gemini/v1beta/", "",
			"https://proxy.example.com/gemini/v1beta/models/gemini-2.5-pro:generateContent",
		},
		{
			"explicit_method", "https://unused.example.com", "https://proxy.example.com/route:generateContent",
			"https://proxy.example.com/route:generateContent",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := newTestClient(t, tc.base, func(cfg *Config) {
				cfg.Endpoint.Model = "models/gemini-2.5-pro"
				cfg.Endpoint.URL = tc.override
				cfg.Endpoint.Query = map[string]string{"tenant": "test"}
			})
			if got := client.requestURL(false); got != tc.want+"?tenant=test" {
				t.Fatalf("requestURL = %q", got)
			}
			wantStream := strings.TrimSuffix(tc.want, ":generateContent") + ":streamGenerateContent"
			got := client.requestURL(true)
			parsed, err := url.Parse(got)
			if err != nil {
				t.Fatal(err)
			}
			if parsed.Query().Get("alt") != "sse" || parsed.Query().Get("tenant") != "test" {
				t.Fatalf("query = %q", parsed.RawQuery)
			}
			parsed.RawQuery = ""
			if parsed.String() != wantStream {
				t.Fatalf("requestURL = %q, want %q", parsed.String(), wantStream)
			}
		})
	}
}
