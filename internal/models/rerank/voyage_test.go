package rerank

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func voyageRerankConfig(url, model string) *RerankerConfig {
	return &RerankerConfig{
		Source: types.ModelSourceRemote, Provider: "voyage", ModelName: model,
		BaseURL: url + "/v1", APIKey: "k",
	}
}

func voyageReply(t *testing.T, status int, body string) string {
	t.Helper()
	withRerankSSRFWhitelist(t, "127.0.0.1")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	return server.URL
}

// There is no default model: a rerank row that names none is refused.
func TestVoyageRerankRequiresAModel(t *testing.T) {
	up := newUpstream(t)
	for _, name := range []string{"", "  "} {
		_, err := newReranker(voyageRerankConfig(up.url, name))
		require.ErrorContains(t, err, "model name is required")
	}
	assert.Empty(t, up.requests)
}

// Another vendor's model or an unknown one is rejected by Voyage with
// {"detail": "..."}; the message is the error.
func TestVoyageRerankRejectionSurfacesTheDetailMessage(t *testing.T) {
	const detail = "Model rerank-v3.5 is not supported."
	url := voyageReply(t, http.StatusBadRequest, `{"detail": "`+detail+`"}`)
	r, err := newReranker(voyageRerankConfig(url, "rerank-v3.5"))
	require.NoError(t, err)
	_, err = r.Rerank(context.Background(), "q", []string{"a"})
	require.EqualError(t, err, "voyage API error (status 400): "+detail)
}

// An empty or partial ranking is an error, never a quietly smaller result.
func TestVoyageRerankMalformedRepliesAreErrors(t *testing.T) {
	for name, reply := range map[string]string{
		"error document with 200": `{"detail":"x"}`,
		"empty ranking":           `{"data":[]}`,
		"missing score":           `{"data":[{"index":0}]}`,
		"missing index":           `{"data":[{"relevance_score":0.5}]}`,
		"duplicate index":         `{"data":[{"index":0,"relevance_score":0.5},{"index":0,"relevance_score":0.4}]}`,
		"index out of range":      `{"data":[{"index":2,"relevance_score":0.5}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			r, err := newReranker(voyageRerankConfig(voyageReply(t, http.StatusOK, reply), "rerank-3"))
			require.NoError(t, err)
			_, err = r.Rerank(context.Background(), "q", []string{"a", "b"})
			require.Error(t, err)
		})
	}
}

// top_k is sent when the row opts in and asks for every document back; a
// smaller top_k from extra_body returns a shorter, still ranked, list.
func TestVoyageRerankTopK(t *testing.T) {
	up := newUpstream(t)
	cfg := voyageRerankConfig(up.url, "rerank-3")
	cfg.Spec = &types.ModelSpecOverride{Compat: map[string]any{"send_top_n": true}}
	r, err := newReranker(cfg)
	require.NoError(t, err)
	got, err := r.Rerank(context.Background(), "q", []string{"a", "bbb", "cc"})
	require.NoError(t, err)
	require.Len(t, got, 3)
	require.Len(t, up.requests, 1)
	assert.Equal(t, float64(3), up.requests[0].body["top_k"])
	assert.Equal(t, []int{1, 2, 0}, []int{got[0].Index, got[1].Index, got[2].Index}, "ranked, longest first")
}
