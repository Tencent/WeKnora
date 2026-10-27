package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSessionKindCreateAndList(t *testing.T) {
	for _, kind := range []string{"", "assistant"} {
		t.Run(kind, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/v1/sessions" {
					t.Errorf("unexpected path: %s", r.URL.Path)
				}
				w.Header().Set("Content-Type", "application/json")
				switch r.Method {
				case http.MethodPost:
					var request CreateSessionRequest
					if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
						t.Error(err)
					}
					if request.Kind != kind {
						t.Errorf("kind = %q, want %q", request.Kind, kind)
					}
					if err := json.NewEncoder(w).Encode(SessionResponse{
						Success: true, Data: Session{ID: "s", Kind: kind},
					}); err != nil {
						t.Error(err)
					}
				case http.MethodGet:
					if got := r.URL.Query().Get("kind"); got != kind {
						t.Errorf("kind = %q, want %q", got, kind)
					}
					if r.URL.Query().Get("page") != "2" || r.URL.Query().Get("page_size") != "20" {
						t.Error("missing pagination")
					}
					if err := json.NewEncoder(w).Encode(SessionListResponse{
						Success: true, Data: []Session{{ID: "s", Kind: kind}}, Total: 1,
					}); err != nil {
						t.Error(err)
					}
				default:
					t.Errorf("unexpected method: %s", r.Method)
				}
			}))
			defer server.Close()
			c := NewClient(server.URL)
			session, err := c.CreateSession(context.Background(), &CreateSessionRequest{Kind: kind})
			if err != nil {
				t.Fatal(err)
			}
			if session.Kind != kind {
				t.Errorf("created kind = %q, want %q", session.Kind, kind)
			}
			var opts []SessionListOptions
			if kind != "" {
				opts = append(opts, SessionListOptions{Kind: kind})
			}
			sessions, total, err := c.GetSessionsByTenant(context.Background(), 2, 20, opts...)
			if err != nil {
				t.Fatal(err)
			}
			if total != 1 || len(sessions) != 1 || sessions[0].Kind != kind {
				t.Errorf("unexpected sessions: %+v, total %d", sessions, total)
			}
		})
	}
}
