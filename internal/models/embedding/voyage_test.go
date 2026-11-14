package embedding

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func voyageConfig(url, model string) Config {
	return Config{
		Source: types.ModelSourceRemote, Provider: "voyage", ModelName: model,
		BaseURL: url + "/v1", APIKey: "k",
	}
}

// There is no default model: a row that names none is refused before any
// request is built, whatever the provider's catalog offers.
func TestVoyageEmbeddingRequiresAModel(t *testing.T) {
	up := newUpstream(t)
	for _, name := range []string{"", "   "} {
		_, err := newEmbedder(voyageConfig(up.url, name), nil, nil)
		require.ErrorContains(t, err, "model name is required")
	}
	assert.Empty(t, up.requests)
}

// voyageFailure answers every request with the status and body Voyage uses.
func voyageFailure(t *testing.T, status int, body string) string {
	t.Helper()
	allowLoopback(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	return server.URL
}

// Another vendor's model, an unknown model and an unsupported dimension are
// rejected by Voyage with {"detail": "..."}; the message reaches the caller
// as the error, on every one of the three embedding endpoints.
func TestVoyageRejectionsSurfaceTheDetailMessage(t *testing.T) {
	const detail = "Model text-embedding-3-small is not supported."
	url := voyageFailure(t, http.StatusBadRequest, `{"detail": "`+detail+`"}`)
	for _, model := range []string{"text-embedding-3-small", "voyage-context-4", "voyage-multimodal-3.5"} {
		t.Run(model, func(t *testing.T) {
			e, err := newEmbedder(voyageConfig(url, model), nil, nil)
			require.NoError(t, err)
			_, err = e.BatchEmbed(context.Background(), []string{"a"})
			require.EqualError(t, err, "voyage API error (status 400): "+detail)
		})
	}
}

// A 4XX is the vendor answering; repeating it neither fixes it nor is free.
func TestVoyageRejectionIsNotRetried(t *testing.T) {
	var calls int
	allowLoopback(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"detail": "bad"}`))
	}))
	defer server.Close()
	e, err := newEmbedder(voyageConfig(server.URL, "voyage-4"), nil, nil)
	require.NoError(t, err)
	_, err = e.BatchEmbed(context.Background(), []string{"a"})
	require.Error(t, err)
	assert.Equal(t, 1, calls)
}

// A contextualized text that Voyage splits further has no single vector. It
// is an error, not a dropped chunk or an average.
func TestVoyageContextualizedRefusesATextSplitIntoSeveralChunks(t *testing.T) {
	allowLoopback(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":[{"index":0,"data":[` +
			`{"index":0,"embedding":[1],"text":"a"},{"index":1,"embedding":[2],"text":"b"}]}]}`))
	}))
	defer server.Close()
	e, err := newEmbedder(voyageConfig(server.URL, "voyage-context-4"), nil, nil)
	require.NoError(t, err)
	_, err = e.BatchEmbed(context.Background(), []string{"one long document"})
	require.ErrorContains(t, err, "input 0 came back as 2 chunks")
}

// Replies that do not cover each input exactly once are refused rather than
// stored as an empty or misplaced vector.
func TestVoyageIncompleteRepliesAreErrors(t *testing.T) {
	for name, reply := range map[string]string{
		"missing index":   `{"data":[{"embedding":[1]},{"embedding":[2]}]}`,
		"duplicate index": `{"data":[{"index":0,"embedding":[1]},{"index":0,"embedding":[2]}]}`,
		"short":           `{"data":[{"index":0,"embedding":[1]}]}`,
		"empty vector":    `{"data":[{"index":0,"embedding":[1]},{"index":1,"embedding":[]}]}`,
		"out of range":    `{"data":[{"index":0,"embedding":[1]},{"index":5,"embedding":[2]}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			allowLoopback(t)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(reply))
			}))
			defer server.Close()
			e, err := newEmbedder(voyageConfig(server.URL, "voyage-4"), nil, nil)
			require.NoError(t, err)
			_, err = e.BatchEmbed(context.Background(), []string{"a", "b"})
			require.Error(t, err)
		})
	}
}

// Vectors of a batch that Voyage's input ceiling splits come back in the
// order the texts were given.
func TestVoyageSplitBatchesKeepInputOrder(t *testing.T) {
	up := newUpstream(t)
	e, err := newEmbedder(voyageConfig(up.url, "voyage-3.5-lite"), nil, nil)
	require.NoError(t, err)
	input := make([]string, 2500)
	for i := range input {
		input[i] = strings.Repeat("x", i%40+1)
	}
	got, err := e.BatchEmbed(context.Background(), input)
	require.NoError(t, err)
	require.Len(t, got, len(input))
	for i, vector := range got {
		require.Equal(t, []float32{float32(i%40 + 1)}, vector, "input %d", i)
	}
	require.Len(t, up.requests, 3)
	for i, want := range []int{1000, 1000, 500} {
		assert.Len(t, up.requests[i].body["input"], want)
	}
}
