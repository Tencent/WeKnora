package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAgentStreamSendsScopeAndAgentPage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request AgentQARequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		if request.AssistantScope != AssistantScopePage || request.PageContext == nil ||
			request.PageContext.AgentID != "a1" {
			t.Errorf("unexpected request: %+v", request)
		}
		w.Header().Set("Content-Type", "text/event-stream")
	}))
	defer server.Close()
	err := NewClient(server.URL).AgentQAStreamWithRequest(context.Background(), "s", &AgentQARequest{
		Query:          "Check this agent",
		AgentEnabled:   true,
		AssistantScope: AssistantScopePage,
		PageContext:    &PageContext{AgentID: "a1"},
	}, func(*AgentStreamResponse) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
}
