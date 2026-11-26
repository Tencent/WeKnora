package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRetrievalDetailed_ImageDiagnostics(t *testing.T) {
	for _, api := range []struct {
		name   string
		path   string
		search func(*Client) (any, error)
	}{
		{
			name: "knowledge",
			path: "/api/v1/knowledge-search",
			search: func(c *Client) (any, error) {
				return c.SearchKnowledgeDetailed(context.Background(), &SearchKnowledgeRequest{
					Query: "diagram", KnowledgeBaseID: "kb-1",
					Rerank: &RerankOptions{ModelID: "rerank-1"},
				})
			},
		},
		{
			name: "hybrid",
			path: "/api/v1/knowledge-bases/kb-1/hybrid-search",
			search: func(c *Client) (any, error) {
				return c.HybridSearchDetailed(context.Background(), "kb-1", &SearchParams{
					QueryText: "diagram", Rerank: &RerankOptions{ModelID: "rerank-1"},
				})
			},
		},
	} {
		for _, tc := range []struct {
			name         string
			imageFields  string
			imagesScored int
			imagesKept   int
		}{
			{name: "multimodal_images_scored", imageFields: `,"images_scored":2`, imagesScored: 2},
			{name: "text_rerank_images_kept", imageFields: `,"images_kept":1`, imagesKept: 1},
			{name: "text_only"},
		} {
			t.Run(api.name+"/"+tc.name, func(t *testing.T) {
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Method != http.MethodPost || r.URL.Path != api.path {
						t.Errorf("request = %s %s, want POST %s", r.Method, r.URL.Path, api.path)
					}
					w.Header().Set("Content-Type", "application/json")
					_, _ = fmt.Fprintf(w, `{
						"success":true,
						"data":[{"id":"chunk-1","content":"diagram description",
						"knowledge_id":"knowledge-1","score":0.9}],
						"meta":{"rerank":{
							"applied":true,"outcome":"ok","model_id":"rerank-1","model_source":"request",
							"threshold":0.5,"effective_threshold":0.5,"top_score":0.9,
							"candidate_count":2,"result_count":1%s
						}}
					}`, tc.imageFields)
				}))
				defer srv.Close()

				response, err := api.search(NewClient(srv.URL))
				if err != nil {
					t.Fatalf("search error = %v", err)
				}
				encoded, err := json.Marshal(response)
				if err != nil {
					t.Fatalf("marshal response: %v", err)
				}
				var roundTrip struct {
					Success bool            `json:"success"`
					Data    []*SearchResult `json:"data"`
					Meta    struct {
						Rerank map[string]any `json:"rerank"`
					} `json:"meta"`
				}
				if err := json.Unmarshal(encoded, &roundTrip); err != nil {
					t.Fatalf("decode marshaled response: %v", err)
				}
				if !roundTrip.Success || len(roundTrip.Data) != 1 || roundTrip.Data[0] == nil {
					t.Fatalf("response = %s, want success with one result", encoded)
				}
				result := roundTrip.Data[0]
				if result.ID != "chunk-1" || result.Content != "diagram description" ||
					result.KnowledgeID != "knowledge-1" || result.Score != 0.9 {
					t.Errorf("result = %+v, want the original search result", result)
				}
				for name, want := range map[string]any{
					"applied": true, "outcome": "ok", "model_id": "rerank-1", "model_source": "request",
					"threshold": 0.5, "effective_threshold": 0.5, "top_score": 0.9,
					"candidate_count": float64(2), "result_count": float64(1),
				} {
					if got := roundTrip.Meta.Rerank[name]; got != want {
						t.Errorf("rerank %s = %v, want %v", name, got, want)
					}
				}
				for _, field := range []struct {
					name string
					want int
				}{
					{name: "images_scored", want: tc.imagesScored},
					{name: "images_kept", want: tc.imagesKept},
				} {
					got, exists := roundTrip.Meta.Rerank[field.name]
					if field.want == 0 {
						if exists {
							t.Errorf("marshaled response contains %s = %v, want omitted", field.name, got)
						}
					} else if !exists {
						t.Errorf("marshaled response lost %s, want %d", field.name, field.want)
					} else if got != float64(field.want) {
						t.Errorf("%s = %v, want %d", field.name, got, field.want)
					}
				}
			})
		}
	}
}
