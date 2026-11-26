package cohererank

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/models/api"
)

// A malformed 2xx response must not be decoded into a ranking: a missing or
// duplicated index silently maps to the wrong documents, and a missing score
// silently makes every kept document score zero.
func TestRejectsMalformedRankings(t *testing.T) {
	tests := []struct {
		name    string
		payload string
		docs    []string
		wantErr string
	}{
		{
			name: "missing index", payload: `{"results":[{"relevance_score":0.5}]}`,
			docs: []string{"d0"}, wantErr: "missing index",
		},
		{
			name: "null index", payload: `{"results":[{"index":null,"relevance_score":0.5}]}`,
			docs: []string{"d0"}, wantErr: "missing index",
		},
		{
			name: "null row", payload: `{"results":[null]}`,
			docs: []string{"d0"}, wantErr: "missing index",
		},
		{
			name: "missing score", payload: `{"results":[{"index":0}]}`,
			docs: []string{"d0"}, wantErr: "missing relevance_score or score",
		},
		{
			name:    "malformed row after valid row",
			payload: `{"results":[{"index":0,"relevance_score":0.5},{"index":1}]}`,
			docs:    []string{"d0", "d1"}, wantErr: "missing relevance_score or score",
		},
		{
			name:    "duplicate index",
			payload: `{"results":[{"index":0,"relevance_score":0.5},{"index":0,"relevance_score":0.4}]}`,
			docs:    []string{"d0"}, wantErr: "duplicate index",
		},
		{
			name:    "out of range",
			payload: `{"results":[{"index":1,"relevance_score":0.5}]}`,
			docs:    []string{"d0"}, wantErr: "out of range",
		},
		{
			name: "bare array missing index", payload: `[{"score":0.5}]`,
			docs: []string{"d0"}, wantErr: "missing index",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server, _, _ := serve(t, tt.payload)
			defer server.Close()
			c := newClient(t, server.URL, api.RerankSettings{})

			got, err := c.Rerank(context.Background(), "q", tt.docs)
			require.ErrorContains(t, err, tt.wantErr)
			require.Nil(t, got)
		})
	}
}

// Both vendor spellings of the score remain accepted, and the ranked index
// order is preserved.
func TestAcceptsEitherScoreSpelling(t *testing.T) {
	server, _, _ := serve(t, `{"results":[{"index":1,"score":0.75},{"index":0,"relevance_score":0.25}]}`)
	defer server.Close()
	c := newClient(t, server.URL, api.RerankSettings{})

	got, err := c.Rerank(context.Background(), "q", []string{"d0", "d1"})
	require.NoError(t, err)
	require.Len(t, got, 2)
	require.Equal(t, 1, got[0].Index)
	require.Equal(t, 0.75, got[0].Score)
	require.Equal(t, 0, got[1].Index)
	require.Equal(t, 0.25, got[1].Score)
}
