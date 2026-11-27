package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestResourceURLModesReachPublicRequests(t *testing.T) {
	methods := []struct {
		name   string
		path   string
		method string
		invoke func(context.Context, *Client, []ResourceURLOptions) (string, error)
	}{
		{
			name: "message history", path: "/api/v1/messages/session-1/load", method: http.MethodGet,
			invoke: func(ctx context.Context, c *Client, opts []ResourceURLOptions) (string, error) {
				messages, err := c.LoadMessages(ctx, "session-1", 10, nil, opts...)
				if err != nil {
					return "", err
				}
				if len(messages) != 1 {
					return "", fmt.Errorf("messages=%d, want 1", len(messages))
				}
				return messages[0].Content, nil
			},
		},
		{
			name: "hybrid search", path: "/api/v1/knowledge-bases/kb-1/hybrid-search", method: http.MethodPost,
			invoke: func(ctx context.Context, c *Client, opts []ResourceURLOptions) (string, error) {
				results, err := c.HybridSearch(ctx, "kb-1", &SearchParams{QueryText: "figure"}, opts...)
				if err != nil {
					return "", err
				}
				if len(results) != 1 {
					return "", fmt.Errorf("results=%d, want 1", len(results))
				}
				return results[0].Content, nil
			},
		},
		{
			name: "knowledge search", path: "/api/v1/knowledge-search", method: http.MethodPost,
			invoke: func(ctx context.Context, c *Client, opts []ResourceURLOptions) (string, error) {
				results, err := c.SearchKnowledge(ctx, &SearchKnowledgeRequest{Query: "figure"}, opts...)
				if err != nil {
					return "", err
				}
				if len(results) != 1 {
					return "", fmt.Errorf("results=%d, want 1", len(results))
				}
				return results[0].Content, nil
			},
		},
		{
			name: "knowledge chat", path: "/api/v1/knowledge-chat/session-1", method: http.MethodPost,
			invoke: func(ctx context.Context, c *Client, opts []ResourceURLOptions) (string, error) {
				var answer string
				err := c.KnowledgeQAStream(ctx, "session-1", &KnowledgeQARequest{Query: "figure"}, func(event *StreamResponse) error {
					answer += event.Content
					return nil
				}, opts...)
				return answer, err
			},
		},
		{
			name: "agent chat", path: "/api/v1/agent-chat/session-1", method: http.MethodPost,
			invoke: func(ctx context.Context, c *Client, opts []ResourceURLOptions) (string, error) {
				var answer string
				err := c.AgentQAStreamWithRequest(ctx, "session-1", &AgentQARequest{Query: "figure"}, func(event *AgentStreamResponse) error {
					answer += event.Content
					return nil
				}, opts...)
				return answer, err
			},
		},
		{
			name: "resume", path: "/api/v1/sessions/continue-stream/session-1", method: http.MethodGet,
			invoke: func(ctx context.Context, c *Client, opts []ResourceURLOptions) (string, error) {
				var answer string
				err := c.ContinueStream(ctx, "session-1", "message-1", func(event *StreamResponse) error {
					answer += event.Content
					return nil
				}, opts...)
				return answer, err
			},
		},
	}
	modes := []struct {
		name string
		opts []ResourceURLOptions
		wire string
	}{
		{name: "omitted inherits public"},
		{name: "empty inherits public", opts: []ResourceURLOptions{{}}},
		{name: "explicit public", opts: []ResourceURLOptions{{ResourceURLs: ResourceURLModePublic}}, wire: "public"},
		{name: "explicit handle", opts: []ResourceURLOptions{{ResourceURLs: ResourceURLModeHandle}}, wire: "handle"},
	}
	for _, method := range methods {
		for _, mode := range modes {
			t.Run(method.name+"/"+mode.name, func(t *testing.T) {
				received := make(chan url.Values, 1)
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Method != method.method || r.URL.Path != method.path {
						http.Error(w, "unexpected request route", http.StatusBadRequest)
						return
					}
					query := r.URL.Query()
					received <- query
					content := "https://cdn.example.invalid/figure.png"
					if query.Get("resource_urls") == "handle" {
						content = "resource://figure-1"
					}
					if strings.Contains(method.path, "-chat/") || strings.Contains(method.path, "continue-stream/") {
						w.Header().Set("Content-Type", "text/event-stream")
						frame, err := json.Marshal(map[string]any{"response_type": "answer", "content": content, "done": false})
						if err != nil {
							http.Error(w, err.Error(), http.StatusInternalServerError)
							return
						}
						_, _ = fmt.Fprintf(w, "event: message\ndata: %s\n\nevent: message\ndata: {\"response_type\":\"complete\",\"done\":true}\n\n", frame)
						return
					}
					w.Header().Set("Content-Type", "application/json")
					_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "data": []map[string]any{{"content": content}}})
				}))
				t.Cleanup(server.Close)
				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				defer cancel()
				content, err := method.invoke(ctx, NewClient(server.URL), mode.opts)
				if err != nil {
					t.Fatal(err)
				}
				want := "https://cdn.example.invalid/figure.png"
				if mode.wire == "handle" {
					want = "resource://figure-1"
				}
				if content != want {
					t.Errorf("returned resource=%q, want %q", content, want)
				}
				var query url.Values
				select {
				case query = <-received:
				case <-ctx.Done():
					t.Fatal("SDK returned without making the expected request")
				}
				if got := query.Get("resource_urls"); got != mode.wire {
					t.Errorf("resource_urls=%q, want %q", got, mode.wire)
				}
				if mode.wire == "" && query.Has("resource_urls") {
					t.Errorf("inherited mode must leave resource_urls absent: %v", query)
				}
				if method.name == "message history" && query.Get("limit") != "10" {
					t.Errorf("message limit changed: %v", query)
				}
				if method.name == "resume" && query.Get("message_id") != "message-1" {
					t.Errorf("resume message id changed: %v", query)
				}
			})
		}
	}
}
