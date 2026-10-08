package embedding

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/models/api"
	"github.com/Tencent/WeKnora/internal/models/limiter"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/panjf2000/ants/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// An upstream that refuses connections must surface as an error from every
// protocol. The hand-written clients this replaced could each return
// (nil, nil) from their retry loop and dereference a nil response, which took
// the process down instead (Tencent/WeKnora#3484).
func TestUnreachableUpstreamIsAnErrorForEveryProtocol(t *testing.T) {
	allowLoopback(t)
	saved := retryPolicy
	retryPolicy = func() api.RetryPolicy { return api.RetryPolicy{} }
	t.Cleanup(func() { retryPolicy = saved })

	// A server that is started and closed gives a URL whose port refuses.
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := server.URL
	server.Close()

	for _, tc := range []struct{ provider, model string }{
		{"openai", "text-embedding-3-small"},
		{"aliyun", "tongyi-embedding-vision-plus"},
		{"volcengine", "doubao-embedding-vision-251215"},
		{"gemini", "gemini-embedding-001"},
	} {
		t.Run(tc.provider, func(t *testing.T) {
			embedder, err := newEmbedder(Config{
				Source: types.ModelSourceRemote, Provider: tc.provider,
				BaseURL: url, ModelName: tc.model, APIKey: "k",
			}, nil, nil)
			require.NoError(t, err)
			_, err = embedder.BatchEmbed(context.Background(), []string{"a"})
			assert.Error(t, err)
		})
	}
}

// A signing vendor without its identity pair would send unsigned requests
// and fail at the far end with a less useful error.
func TestSignedVendorNeedsItsIdentityPair(t *testing.T) {
	_, err := newEmbedder(Config{
		Source: types.ModelSourceRemote, Provider: "weknoracloud",
		BaseURL: "https://weknora.weixin.qq.com", ModelName: "m", AppSecret: "s",
	}, nil, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "AppID is required")
}

func TestModelNameIsRequired(t *testing.T) {
	_, err := newEmbedder(Config{Source: types.ModelSourceRemote, Provider: "openai"}, nil, nil)
	assert.Error(t, err)
}

// newPooledEmbedder builds the embedder the way NewEmbedder does for a
// background caller: behind the batch pool and the per-model gate.
func newPooledEmbedder(t *testing.T, handler http.HandlerFunc) Embedder {
	t.Helper()
	allowLoopback(t)
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	pool, err := ants.NewPool(4)
	require.NoError(t, err)
	t.Cleanup(pool.Release)
	raw, err := newEmbedder(Config{
		Source: types.ModelSourceRemote, Provider: "generic",
		BaseURL: server.URL + "/v1", ModelName: "m", ModelID: "pooled",
	}, NewBatchEmbedder(pool), nil)
	require.NoError(t, err)
	return wrapEmbeddingConcurrency(raw, 1)
}

func TestPoolBatchesAndPreservesOrder(t *testing.T) {
	t.Setenv("BATCH_EMBED_SIZE", "2")
	var mu sync.Mutex
	var sizes []int
	embedder := newPooledEmbedder(t, func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		sizes = append(sizes, len(body["input"].([]any)))
		mu.Unlock()
		_, _ = w.Write([]byte(answer(r.URL.Path, body)))
	})
	got, err := embedder.BatchEmbedWithPool(
		context.Background(), embedder, []string{"a", "bb", "ccc", "dddd", "eeeee"})
	require.NoError(t, err)
	assert.Equal(t, [][]float32{{1}, {2}, {3}, {4}, {5}}, got)
	assert.Len(t, sizes, 3)
	for _, size := range sizes {
		assert.LessOrEqual(t, size, 2, "request size violates BATCH_EMBED_SIZE=2")
	}
}

func TestPoolHonoursConcurrencyLimit(t *testing.T) {
	t.Setenv("BATCH_EMBED_SIZE", "1")
	limiter.SetGovernor(limiter.NewLocalLimiter(), 10)
	t.Cleanup(func() { limiter.SetGovernor(nil, 0) })
	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	embedder := newPooledEmbedder(t, func(w http.ResponseWriter, _ *http.Request) {
		entered <- struct{}{}
		<-release
		_, _ = fmt.Fprint(w, `{"data":[{"index":0,"embedding":[1]}]}`)
	})
	ctx := types.WithBackgroundTask(context.Background())
	var wg sync.WaitGroup
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	t.Cleanup(func() { unblock(); wg.Wait() })
	errs := make(chan error, 2)
	// Separate callers stand for simultaneous batches sharing one model.
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := embedder.BatchEmbedWithPool(ctx, embedder, []string{"x"})
			errs <- err
		}()
	}
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("first request did not reach the upstream")
	}
	select {
	case <-entered:
		t.Error("two requests reached the upstream at once with a model limit of 1")
	case <-time.After(150 * time.Millisecond):
	}
	unblock()
	for range 2 {
		select {
		case err := <-errs:
			assert.NoError(t, err)
		case <-time.After(2 * time.Second):
			t.Fatal("pooled embedding did not complete after release")
		}
	}
}

// Embedding a query is a single call that must carry the query mark all the
// way down; the retrieval pipeline reaches it through Embed, not BatchEmbed.
func TestEmbedCarriesTheQueryMark(t *testing.T) {
	up := newUpstream(t)
	embedder, err := newEmbedder(Config{
		Source: types.ModelSourceRemote, Provider: "nvidia",
		BaseURL: up.url + "/v1", ModelName: "nvidia/nemotron-3-embed-1b", APIKey: "k",
	}, nil, nil)
	require.NoError(t, err)

	_, err = embedder.Embed(types.WithEmbedQuery(context.Background()), "what is it")
	require.NoError(t, err)
	_, err = embedder.Embed(context.Background(), strings.Repeat("passage ", 3))
	require.NoError(t, err)

	require.Len(t, up.requests, 2)
	assert.Equal(t, "query", up.requests[0].body["input_type"])
	assert.Equal(t, "passage", up.requests[1].body["input_type"])
}
