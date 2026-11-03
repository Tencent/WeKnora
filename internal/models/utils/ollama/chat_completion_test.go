package ollama

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/ollama/ollama/api"
)

func chatTestService(t *testing.T, body string, status int) *OllamaService {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.URL.Path != "/api/chat" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/x-ndjson")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(server.Close)
	baseURL, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	return &OllamaService{client: api.NewClient(baseURL, server.Client())}
}

func TestChatRejectsResponsesWithoutDone(t *testing.T) {
	for _, stream := range []bool{true, false} {
		for name, body := range map[string]string{
			"empty":          "",
			"partial answer": `{"message":{"role":"assistant","content":"partial"},"done":false}` + "\n",
			"partial tool round": `{"message":{"role":"assistant","tool_calls":[` +
				`{"function":{"name":"search","arguments":{"query":"x"}}}]},"done":false}` + "\n",
		} {
			t.Run(fmt.Sprintf("stream=%t/%s", stream, name), func(t *testing.T) {
				svc := chatTestService(t, body, http.StatusOK)
				req := &api.ChatRequest{Model: "m", Stream: &stream}
				err := svc.Chat(t.Context(), req, func(api.ChatResponse) error {
					return nil
				})
				if !errors.Is(err, io.ErrUnexpectedEOF) {
					t.Fatalf("Chat error = %v, want io.ErrUnexpectedEOF", err)
				}
			})
		}
	}
}

func TestChatPreservesCompletedResponses(t *testing.T) {
	for _, stream := range []bool{true, false} {
		for _, reason := range []string{"stop", "length"} {
			t.Run(fmt.Sprintf("stream=%t/%s", stream, reason), func(t *testing.T) {
				body := fmt.Sprintf(`{"message":{"role":"assistant","content":"answer"},`+
					`"done":true,"done_reason":%q,"eval_count":3}`, reason)
				wantCalls := 1
				if stream {
					body = `{"message":{"role":"assistant","content":"first"},"done":false}` + "\n" + body
					wantCalls++
				}
				svc := chatTestService(t, body, http.StatusOK)
				var responses []api.ChatResponse
				req := &api.ChatRequest{Model: "m", Stream: &stream}
				err := svc.Chat(t.Context(), req, func(resp api.ChatResponse) error {
					responses = append(responses, resp)
					return nil
				})
				if err != nil {
					t.Fatal(err)
				}
				if len(responses) != wantCalls {
					t.Fatalf("callback count = %d, want %d", len(responses), wantCalls)
				}
				last := responses[len(responses)-1]
				if !last.Done || last.DoneReason != reason || last.Message.Content != "answer" || last.EvalCount != 3 {
					t.Fatalf("completion changed: %+v", last)
				}
			})
		}
	}
}

func TestChatPreservesProviderAndCallbackErrors(t *testing.T) {
	t.Run("provider", func(t *testing.T) {
		svc := chatTestService(t, `{"error":"model unavailable"}`, http.StatusServiceUnavailable)
		err := svc.Chat(t.Context(), &api.ChatRequest{Model: "m"}, func(api.ChatResponse) error { return nil })
		var statusErr api.StatusError
		if !errors.As(err, &statusErr) || statusErr.StatusCode != http.StatusServiceUnavailable {
			t.Fatalf("Chat error = %v, want original 503", err)
		}
	})
	t.Run("callback", func(t *testing.T) {
		svc := chatTestService(t, `{"message":{"content":"partial"},"done":false}`, http.StatusOK)
		callbackErr := errors.New("consumer stopped")
		err := svc.Chat(t.Context(), &api.ChatRequest{Model: "m"}, func(api.ChatResponse) error { return callbackErr })
		if !errors.Is(err, callbackErr) {
			t.Fatalf("Chat error = %v, want callback error", err)
		}
	})
}

func TestChatPreservesCancellationBeforeDone(t *testing.T) {
	svc := chatTestService(t, `{"message":{"content":"partial"},"done":false}`, http.StatusOK)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	err := svc.Chat(ctx, &api.ChatRequest{Model: "m"}, func(api.ChatResponse) error {
		cancel()
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Chat error = %v, want context.Canceled", err)
	}
}
