package service

// embedding_client_test.go — unit coverage for the caller-side embedder seam
// (P2 port of the v1 client-package behaviors that lived above the wire):
// the empty-result retry loop and the BATCH_EMBED_SIZE sub-batch pooler.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/models/invoke"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	secutils "github.com/Tencent/WeKnora/internal/utils"
	"github.com/panjf2000/ants/v2"
	"github.com/stretchr/testify/require"
)

// emptyEmbedAdapter serves empty-success embedding responses over an
// httptest server so invoke.Embed succeeds with zero vectors. Registered
// under a unique provider name (the global registry has no unregister).
type emptyEmbedAdapter struct {
	name  string
	calls atomic.Int64
}

func (a *emptyEmbedAdapter) Provider() string { return a.name }

func (a *emptyEmbedAdapter) Capabilities() invoke.Capabilities {
	return invoke.Capabilities{Embedding: &invoke.EmbeddingCaps{}}
}

func (a *emptyEmbedAdapter) BuildEmbeddingRequest(
	ep invoke.Endpoint, model string, opts *invoke.EmbeddingOptions,
) (*invoke.Request, error) {
	body, _ := json.Marshal(map[string]any{"model": model, "input": opts.Inputs})
	h := http.Header{}
	h.Set("Content-Type", "application/json")
	return &invoke.Request{
		Method: http.MethodPost, URL: ep.BaseURL + "/embeddings", Header: h, Body: body,
	}, nil
}

func (a *emptyEmbedAdapter) ParseEmbeddingResponse(_ int, _ http.Header, _ []byte) (*invoke.EmbeddingResponse, error) {
	a.calls.Add(1)
	return &invoke.EmbeddingResponse{Vectors: [][]float32{}}, nil
}

// Embed retries up to three times when the upstream answers with an empty
// success (v1 client loop), then fails with "no embedding returned".
func TestInvokeEmbedderEmbedRetriesOnEmpty(t *testing.T) {
	// Loopback must be whitelisted for the executor's SSRF gate.
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")
	secutils.ResetSSRFWhitelistForTest()
	t.Cleanup(secutils.ResetSSRFWhitelistForTest)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, `{"data":[]}`)
	}))
	t.Cleanup(srv.Close)

	providerName := fmt.Sprintf("empty-embed-%d", time.Now().UnixNano())
	fake := &emptyEmbedAdapter{name: providerName}
	require.NoError(t, invoke.Default.Register(fake))

	e := newInvokeEmbedder(&invoke.ModelConfig{
		Provider:  providerName,
		ModelID:   "m",
		ModelName: "n",
		BaseURL:   srv.URL,
	}, 1024, false, 0, nil)

	_, err := e.Embed(context.Background(), "x")
	require.Error(t, err)
	require.Contains(t, err.Error(), "no embedding returned")
	require.Equal(t, int64(3), fake.calls.Load(), "three attempts on empty successes (v1 loop)")
}

// BatchEmbed propagates provider errors through the unified error model.
func TestInvokeEmbedderBatchEmbedUnknownProvider(t *testing.T) {
	e := newInvokeEmbedder(&invoke.ModelConfig{Provider: "no-such-adapter"}, 0, false, 0, nil)
	_, err := e.BatchEmbed(context.Background(), []string{"x"})
	require.Error(t, err)
	var pe *invoke.ProviderError
	require.True(t, errors.As(err, &pe), "invoke errors must be ProviderError")
}

// The pooler sub-batches by BATCH_EMBED_SIZE and validates counts.
func TestBatchEmbedPoolerSubBatchesAndValidates(t *testing.T) {
	t.Setenv("BATCH_EMBED_SIZE", "2")
	pool, err := ants.NewPool(8)
	require.NoError(t, err)
	t.Cleanup(func() { pool.Release() })
	p := NewBatchEmbedPooler(pool)

	probe := &countingEmbedder{reply: func(n int) ([][]float32, error) {
		// non-empty vectors — the pooler now rejects empty ones (2026-09-14)
		vectors := make([][]float32, n)
		for i := range vectors {
			vectors[i] = []float32{0.1}
		}
		return vectors, nil
	}}
	got, err := p.BatchEmbedWithPool(context.Background(), probe, []string{"a", "b", "c", "d", "e"})
	require.NoError(t, err)
	require.Len(t, got, 5)
	require.Equal(t, int32(3), probe.calls.Load(), "5 inputs / batch size 2 → 3 sub-batches")

	// Count mismatch from any sub-batch fails the whole batch.
	bad := &countingEmbedder{reply: func(int) ([][]float32, error) {
		return make([][]float32, 1), nil
	}}
	_, err = p.BatchEmbedWithPool(context.Background(), bad, []string{"a", "b"})
	require.Error(t, err)
}

type countingEmbedder struct {
	// atomic: BatchEmbed runs on pool workers concurrently — a bare int was
	// a data race and flaked the sub-batch count (observed 2 vs 3).
	calls atomic.Int32
	reply func(n int) ([][]float32, error)
}

func (c *countingEmbedder) Embed(context.Context, string) ([]float32, error) {
	return nil, nil
}

func (c *countingEmbedder) BatchEmbed(_ context.Context, texts []string) ([][]float32, error) {
	c.calls.Add(1)
	return c.reply(len(texts))
}

func (c *countingEmbedder) GetModelName() string { return "t" }
func (c *countingEmbedder) GetDimensions() int   { return 1 }
func (c *countingEmbedder) GetModelID() string   { return "t" }
func (c *countingEmbedder) BatchEmbedWithPool(
	ctx context.Context, m interfaces.Embedder, texts []string,
) ([][]float32, error) {
	return m.BatchEmbed(ctx, texts)
}

// BatchEmbed enforces the count contract (P2 review finding 4): a short
// response fails loudly instead of returning fewer vectors than inputs.
func TestInvokeEmbedderBatchEmbedRejectsShortResponse(t *testing.T) {
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")
	secutils.ResetSSRFWhitelistForTest()
	t.Cleanup(secutils.ResetSSRFWhitelistForTest)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, `{"data":[]}`) // empty success = 0 vectors
	}))
	t.Cleanup(srv.Close)

	providerName := fmt.Sprintf("short-embed-%d", time.Now().UnixNano())
	require.NoError(t, invoke.Default.Register(&emptyEmbedAdapter{name: providerName}))

	e := newInvokeEmbedder(&invoke.ModelConfig{
		Provider: providerName, ModelID: "m", ModelName: "n", BaseURL: srv.URL,
	}, 0, false, 0, nil)

	_, err := e.BatchEmbed(context.Background(), []string{"a", "b"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "returned 0 embeddings for 2 inputs")
}

// TestBatchEmbedPoolerRejectsEmptyVectors pins the 2026-09-14 guard: a
// sub-batch whose vendor response contains an EMPTY vector fails the whole
// pooler call instead of flowing a 0-dimension row into the vector store.
func TestBatchEmbedPoolerRejectsEmptyVectors(t *testing.T) {
	pool, err := ants.NewPool(4)
	require.NoError(t, err)
	t.Cleanup(func() { pool.Release() })
	p := NewBatchEmbedPooler(pool)

	empty := &countingEmbedder{reply: func(n int) ([][]float32, error) {
		vectors := make([][]float32, n)
		for i := range vectors {
			vectors[i] = []float32{0.1, 0.2}
		}
		vectors[0] = []float32{} // one empty vector among a full count
		return vectors, nil
	}}
	_, err = p.BatchEmbedWithPool(context.Background(), empty, []string{"a", "b"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "empty vector")
}
