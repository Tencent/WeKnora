package embedding

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// documentedMultimodalResponse is the response body of DashScope's multimodal
// embedding API, copied from the reference page so the decoder is pinned
// against the vendor's own example rather than against our structs:
// https://help.aliyun.com/zh/model-studio/multimodal-embedding-api-reference
//
// The position field is `index`. DashScope's *text* embedding API, one page
// over, calls it `text_index`; decoding that name here silently yields 0 for
// every element, which used to collapse a whole batch onto slot 0.
const documentedMultimodalResponse = `{
  "output": {
    "embeddings": [
      {"index": 0, "embedding": [0.1, 0.11], "type": "text"},
      {"index": 1, "embedding": [0.2, 0.22], "type": "text"},
      {"index": 2, "embedding": [0.3, 0.33], "type": "text"}
    ]
  },
  "usage": {"input_tokens": 3, "output_tokens": 0, "total_tokens": 3},
  "request_id": "1fff9502-a6c5-9472-9ee1-73930fdd04c5"
}`

func TestAliyunEmbedderRestoresBatchOrder(t *testing.T) {
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(documentedMultimodalResponse))
	}))
	defer server.Close()

	embedder, err := NewAliyunEmbedder("key", server.URL, "tongyi-embedding-vision-plus", 0, 0, "model-id", nil)
	if err != nil {
		t.Fatalf("NewAliyunEmbedder: %v", err)
	}

	got, err := embedder.BatchEmbed(context.Background(), []string{"a", "b", "c"})
	if err != nil {
		t.Fatalf("BatchEmbed: %v", err)
	}

	want := [][]float32{{0.1, 0.11}, {0.2, 0.22}, {0.3, 0.33}}
	if len(got) != len(want) {
		t.Fatalf("got %d embeddings, want %d", len(got), len(want))
	}
	for i := range want {
		if len(got[i]) != len(want[i]) {
			t.Fatalf("embedding %d: got %v, want %v", i, got[i], want[i])
		}
		for j := range want[i] {
			if got[i][j] != want[i][j] {
				t.Fatalf("embedding %d: got %v, want %v", i, got[i], want[i])
			}
		}
	}
}

// A response that skips an input must fail rather than hand back an empty
// vector: an empty vector is stored and poisons retrieval silently.
func TestAliyunEmbedderFailsOnMissingEmbedding(t *testing.T) {
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"output":{"embeddings":[{"index":0,"embedding":[0.1],"type":"text"}]}}`))
	}))
	defer server.Close()

	embedder, err := NewAliyunEmbedder("key", server.URL, "tongyi-embedding-vision-plus", 0, 0, "model-id", nil)
	if err != nil {
		t.Fatalf("NewAliyunEmbedder: %v", err)
	}

	if _, err := embedder.BatchEmbed(context.Background(), []string{"a", "b"}); err == nil {
		t.Fatal("expected an error when the response covers fewer inputs than requested")
	}
}
