package embedding

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestEmbeddersReportUnreachableUpstream guards a crash that every
// OpenAI-style embedder here is one keystroke away from.
//
// Each doRequestWithRetry keeps `resp` and `err` in the enclosing scope and
// returns `(nil, err)` once the retries run out. Writing
// `req, err := http.NewRequestWithContext(...)` inside the loop introduces a
// loop-local `err`, so `resp, err = client.Do(req)` updates that copy only and
// the enclosing `err` stays nil. The function then returns `(nil, nil)`,
// BatchEmbed's `if err != nil` does not fire, and the next line dereferences
// `resp.Body` — a nil-pointer panic that takes the process down instead of
// surfacing "connection refused". It was found, documented and fixed in
// openai.go, then re-fixed in zhipu.go and azure_openai.go, while jina.go,
// nvidia.go, aliyun.go and volcengine.go kept the original spelling.
//
// The retry budget is zeroed so the table runs without the backoff sleeps.
func TestEmbeddersReportUnreachableUpstream(t *testing.T) {
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")

	// A server that is started and immediately closed gives a reachable-looking
	// URL whose port refuses connections.
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := server.URL
	server.Close()

	cases := []struct {
		name string
		make func() (Embedder, error)
	}{
		{"openai", func() (Embedder, error) {
			e, err := NewOpenAIEmbedder("k", url, "m", 0, 0, "id", nil)
			if e != nil {
				e.maxRetries = 0
			}
			return e, err
		}},
		{"zhipu", func() (Embedder, error) {
			e, err := NewZhipuEmbedder("k", url, "m", 0, 0, "id", nil)
			if e != nil {
				e.maxRetries = 0
			}
			return e, err
		}},
		{"jina", func() (Embedder, error) {
			e, err := NewJinaEmbedder("k", url, "m", 0, 0, "id", nil)
			if e != nil {
				e.maxRetries = 0
			}
			return e, err
		}},
		{"nvidia", func() (Embedder, error) {
			e, err := NewNvidiaEmbedder("k", url, "m", 0, "id", nil)
			if e != nil {
				e.maxRetries = 0
			}
			return e, err
		}},
		{"gemini", func() (Embedder, error) {
			e, err := NewGeminiEmbedder("k", url, "m", 0, 0, "id", nil)
			if e != nil {
				e.maxRetries = 0
			}
			return e, err
		}},
		{"aliyun", func() (Embedder, error) {
			e, err := NewAliyunEmbedder("k", url, "m", 0, 0, "id", nil)
			if e != nil {
				e.maxRetries = 0
			}
			return e, err
		}},
		{"volcengine", func() (Embedder, error) {
			e, err := NewVolcengineEmbedder("k", url, "m", 0, 0, "id", nil)
			if e != nil {
				e.maxRetries = 0
			}
			return e, err
		}},
		{"azure_openai", func() (Embedder, error) {
			e, err := NewAzureOpenAIEmbedder("k", url, "m", 0, 0, "id", "", nil)
			if e != nil {
				e.maxRetries = 0
			}
			return e, err
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			embedder, err := tc.make()
			if err != nil {
				t.Fatalf("constructor: %v", err)
			}
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("BatchEmbed panicked instead of returning an error: %v", r)
				}
			}()
			if _, err := embedder.BatchEmbed(context.Background(), []string{"a"}); err == nil {
				t.Fatal("expected an error when the upstream refuses connections")
			}
		})
	}
}
