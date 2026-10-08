package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestUpdateAgentAvatarPresence(t *testing.T) {
	empty, replacement := "", "🐱"
	for _, tc := range []struct {
		name   string
		avatar *string
		want   string
	}{
		{name: "omitted preserves avatar", want: "🤖"},
		{name: "explicit empty clears avatar", avatar: &empty},
		{name: "replacement", avatar: &replacement, want: replacement},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPut || r.URL.Path != "/api/v1/agents/agent-1" {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
				}
				var body map[string]json.RawMessage
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				avatar := "🤖"
				if raw, exists := body["avatar"]; exists {
					if tc.avatar == nil {
						t.Errorf("avatar must be omitted, got %s", raw)
					}
					if err := json.Unmarshal(raw, &avatar); err != nil {
						t.Error(err)
					}
				} else if tc.avatar != nil {
					t.Error("explicit avatar must be sent")
				}
				w.Header().Set("Content-Type", "application/json")
				if err := json.NewEncoder(w).Encode(AgentResponse{
					Success: true, Data: Agent{ID: "agent-1", Avatar: avatar},
				}); err != nil {
					t.Error(err)
				}
			}))
			defer srv.Close()

			agent, err := NewClient(srv.URL).UpdateAgent(context.Background(), "agent-1", &UpdateAgentRequest{
				Name: "Updated", Avatar: tc.avatar, Config: &AgentConfig{},
			})
			if err != nil {
				t.Fatal(err)
			}
			if agent.Avatar != tc.want {
				t.Errorf("avatar = %q, want %q", agent.Avatar, tc.want)
			}
		})
	}
}
