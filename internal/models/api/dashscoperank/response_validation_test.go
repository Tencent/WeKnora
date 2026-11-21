package dashscoperank

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/api"
	"github.com/stretchr/testify/require"
)

func TestRejectsMalformedRankings(t *testing.T) {
	tests := []struct {
		name    string
		payload string
		wantErr string
	}{
		{"null response", `null`, "results"},
		{"empty object", `{}`, "results"},
		{"error envelope", `{"error":{"message":"model is not loaded"}}`, "results"},
		{"wrong envelope", `{"results":[]}`, "results"},
		{"null output", `{"output":null}`, "results"},
		{"missing results", `{"output":{}}`, "results"},
		{"null results", `{"output":{"results":null}}`, "results"},
		{"null row", `{"output":{"results":[null]}}`, "index or relevance_score"},
		{"empty row", `{"output":{"results":[{}]}}`, "index or relevance_score"},
		{"missing index", `{"output":{"results":[{"relevance_score":0.5}]}}`, "index or relevance_score"},
		{"null index", `{"output":{"results":[{"index":null,"relevance_score":0.5}]}}`, "index or relevance_score"},
		{"missing score", `{"output":{"results":[{"index":0}]}}`, "index or relevance_score"},
		{"null score", `{"output":{"results":[{"index":0,"relevance_score":null}]}}`, "index or relevance_score"},
		{"negative index", `{"output":{"results":[{"index":-1,"relevance_score":0.5}]}}`, "out of range"},
		{"out of range index", `{"output":{"results":[{"index":2,"relevance_score":0.5}]}}`, "out of range"},
		{
			"malformed row after valid row",
			`{"output":{"results":[{"index":1,"relevance_score":0.9},{"index":0}]}}`,
			"index or relevance_score",
		},
		{
			"duplicate index",
			`{"output":{"results":[{"index":0,"relevance_score":0.9},{"index":0,"relevance_score":0.5}]}}`,
			"duplicate index",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("SSRF_WHITELIST", "127.0.0.1")
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(tt.payload))
			}))
			defer server.Close()
			c := newClient(t, server.URL, api.RerankSettings{})

			got, err := c.Rerank(context.Background(), "q", []string{"d0", "d1"})
			require.ErrorContains(t, err, tt.wantErr)
			require.Nil(t, got)
		})
	}
}

func TestAcceptsValidRankings(t *testing.T) {
	tests := []struct {
		name    string
		payload string
		want    []api.RerankResult
	}{
		{"empty results", `{"output":{"results":[]}}`, []api.RerankResult{}},
		{
			"zero index and score",
			`{"output":{"results":[{"index":1,"relevance_score":0.9},{"index":0,"relevance_score":0}]}}`,
			[]api.RerankResult{{Index: 1, Score: 0.9}, {Index: 0, Score: 0}},
		},
		{
			"partial ranking with echoed document",
			`{"output":{"results":[{"index":1,"relevance_score":0.9,"document":{"text":"d1"}}]}}`,
			[]api.RerankResult{{Index: 1, Score: 0.9, Text: "d1"}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("SSRF_WHITELIST", "127.0.0.1")
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(tt.payload))
			}))
			defer server.Close()
			c := newClient(t, server.URL, api.RerankSettings{})

			got, err := c.Rerank(context.Background(), "q", []string{"d0", "d1"})
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}
