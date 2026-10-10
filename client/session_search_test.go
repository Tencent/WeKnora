package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSearchKnowledgeEmbeddingTopK(t *testing.T) {
	zero, depth := 0, 100
	for _, tt := range []struct {
		name  string
		depth *int
	}{
		{name: "omitted"},
		{name: "explicit zero", depth: &zero},
		{name: "independent recall depth", depth: &depth},
	} {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != "/api/v1/knowledge-search" {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
				}
				var body map[string]json.RawMessage
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Errorf("decode request: %v", err)
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				value, present := body["embedding_top_k"]
				if tt.depth == nil {
					if present {
						t.Error("omitted recall depth must not override tenant configuration")
					}
				} else {
					var got int
					if err := json.Unmarshal(value, &got); err != nil || got != *tt.depth {
						t.Errorf("embedding_top_k = %s, want %d (decode error: %v)", value, *tt.depth, err)
					}
				}
				if string(body["match_count"]) != "10" {
					t.Errorf("match_count = %s, want 10", body["match_count"])
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"success":true,"data":[]}`))
			}))
			defer server.Close()

			c := NewClient(server.URL, WithAPIKey("test-key"))
			_, err := c.SearchKnowledge(context.Background(), &SearchKnowledgeRequest{
				Query: "q", KnowledgeBaseIDs: []string{"kb-1"}, EmbeddingTopK: tt.depth, MatchCount: 10,
			})
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}
