package embedding

import (
	"context"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/models/usage"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

type fakeUsageRepo struct {
	mu     sync.Mutex
	usages []*types.ModelUsage
}

func (f *fakeUsageRepo) Create(_ context.Context, u *types.ModelUsage) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.usages = append(f.usages, u)
	return nil
}

func (f *fakeUsageRepo) last() *types.ModelUsage {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.usages) == 0 {
		return nil
	}
	return f.usages[len(f.usages)-1]
}

func testEmbeddingConfig() Config {
	return Config{
		ModelID: "embed-model", ModelName: "embed-safe", Type: types.ModelTypeEmbedding,
		Source: types.ModelSourceOpenAI, Provider: "openai", TenantID: 10000,
	}
}

func tenantCtx(id uint64) context.Context {
	return context.WithValue(context.Background(), types.TenantIDContextKey, id)
}

func TestCacheStatusFromSummary(t *testing.T) {
	require.Equal(t, types.EmbeddingCacheStatusFullHit,
		cacheStatusFromSummary(&cacheRequestSummary{inputs: 5, hits: 5, misses: 0}))
	require.Equal(t, types.EmbeddingCacheStatusPartial,
		cacheStatusFromSummary(&cacheRequestSummary{inputs: 5, hits: 3, misses: 2}))
	require.Equal(t, types.EmbeddingCacheStatusMiss,
		cacheStatusFromSummary(&cacheRequestSummary{inputs: 5, hits: 0, misses: 5}))
}

func TestEmbeddingUsageCacheDisabled(t *testing.T) {
	repo := &fakeUsageRepo{}
	usage.SetRecorder(usage.NewRecorder(repo))
	defer usage.SetRecorder(nil)

	resolved := "provider-embedding"
	w := wrapEmbeddingUsage(&usageFakeEmbedder{}, testEmbeddingConfig(), &resolved)
	before := time.Now()
	_, err := w.BatchEmbed(tenantCtx(1), []string{"a", "b", "c"})
	after := time.Now()
	require.NoError(t, err)

	u := repo.last()
	require.NotNil(t, u)
	require.Equal(t, types.CallTypeEmbedding, u.CallType)
	require.Equal(t, uint64(1), u.TenantID)
	require.Equal(t, uint64(10000), u.ModelTenantID)
	require.Equal(t, "provider-embedding", *u.ResolvedModelName)
	require.False(t, u.StartedAt.Before(before))
	require.False(t, u.StartedAt.After(after))
	require.Equal(t, 3, u.EmbeddingInputs)
	require.Equal(t, 3, u.ProviderInputs, "cache disabled: every input reaches the provider")
	require.Equal(t, 0, u.CacheHits)
	require.Equal(t, 0, u.CacheMisses)
	require.NotNil(t, u.EmbeddingCacheStatus)
	require.Equal(t, types.EmbeddingCacheStatusDisabled, *u.EmbeddingCacheStatus)
	require.Equal(t, types.TokenProvenanceUnreported, u.TokenProvenance)
	require.Equal(t, 1, u.LogicalRequests)
}

func TestEmbeddingUsageCacheDisabledFailureKeepsOutboundInputs(t *testing.T) {
	repo := &fakeUsageRepo{}
	usage.SetRecorder(usage.NewRecorder(repo))
	defer usage.SetRecorder(nil)
	w := wrapEmbeddingUsage(&failingUsageEmbedder{}, testEmbeddingConfig(), nil)
	_, err := w.BatchEmbed(tenantCtx(1), []string{"a", "b"})
	require.Error(t, err)
	require.Equal(t, 1, repo.last().ProviderRequests)
	require.Equal(t, 2, repo.last().ProviderInputs)
}

func TestEmbeddingUsageFailedPoolDoesNotGuessAllInputs(t *testing.T) {
	repo := &fakeUsageRepo{}
	usage.SetRecorder(usage.NewRecorder(repo))
	defer usage.SetRecorder(nil)
	w := wrapEmbeddingUsage(&failingPooledUsageEmbedder{}, testEmbeddingConfig(), nil)
	_, err := w.BatchEmbedWithPool(tenantCtx(1), w, []string{"a", "b", "c"})
	require.Error(t, err)
	require.Equal(t, 1, repo.last().ProviderRequests)
	require.Equal(t, 0, repo.last().ProviderInputs, "partial pooled delivery is unknown and must not be guessed")
}

func TestEmbeddingUsageCacheAccounting(t *testing.T) {
	for _, tc := range []struct {
		name     string
		summary  *cacheRequestSummary
		status   types.EmbeddingCacheStatus
		hits     int
		misses   int
		provIn   int
		readErr  int
		writeErr int
	}{
		{
			"full hit", &cacheRequestSummary{inputs: 4, hits: 4, misses: 0, providerInputs: 0},
			types.EmbeddingCacheStatusFullHit, 4, 0, 0, 0, 0,
		},
		{
			"partial", &cacheRequestSummary{inputs: 4, hits: 1, misses: 3, providerInputs: 3},
			types.EmbeddingCacheStatusPartial, 1, 3, 3, 0, 0,
		},
		{
			"miss with errors",
			&cacheRequestSummary{
				inputs: 4, hits: 0, misses: 4, providerInputs: 4,
				readError: true, writeError: true,
			},
			types.EmbeddingCacheStatusMiss, 0, 4, 4, 1, 1,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := &fakeUsageRepo{}
			usage.SetRecorder(usage.NewRecorder(repo))
			defer usage.SetRecorder(nil)

			w := wrapEmbeddingUsage(&usageFakeEmbedder{}, testEmbeddingConfig(), nil).(*usageEmbedder)
			span := &usageSpan{}
			span.cacheSummary = tc.summary
			w.record(tenantCtx(1), span, time.Now(), tc.summary.inputs, nil, true)

			u := repo.last()
			require.NotNil(t, u)
			require.Equal(t, tc.status, *u.EmbeddingCacheStatus)
			require.Equal(t, tc.hits, u.CacheHits)
			require.Equal(t, tc.misses, u.CacheMisses)
			require.Equal(t, tc.provIn, u.ProviderInputs)
			require.Equal(t, tc.readErr, u.CacheReadErrors)
			require.Equal(t, tc.writeErr, u.CacheWriteErrors)
		})
	}
}

func TestEmbeddingUsageProviderRequestCounter(t *testing.T) {
	ctx, span := withUsageSpan(context.Background())
	noteEmbeddingProviderRequest(ctx)
	noteEmbeddingProviderRequest(ctx)
	noteEmbeddingProviderRequest(ctx)
	require.Equal(t, int64(3), span.providerRequests.Load(), "each outbound attempt must increment the counter")
}

func TestEmbeddingUsageNativeTokens(t *testing.T) {
	cases := []struct {
		name      string
		input     *int
		total     *int
		wantProv  types.TokenProvenance
		wantInput *int
		wantTotal *int
	}{
		{
			"reported positive", intPtr(20), intPtr(30),
			types.TokenProvenanceProviderReported, intPtr(20), intPtr(30),
		},
		{
			"reported zero", intPtr(0), intPtr(0),
			types.TokenProvenanceProviderReported, intPtr(0), intPtr(0),
		},
		{
			"total only", nil, intPtr(7),
			types.TokenProvenanceProviderReported, nil, intPtr(7),
		},
		{
			"omitted usage", nil, nil,
			types.TokenProvenanceUnreported, nil, nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := &fakeUsageRepo{}
			usage.SetRecorder(usage.NewRecorder(repo))
			defer usage.SetRecorder(nil)

			w := wrapEmbeddingUsage(&usageFakeEmbedder{}, testEmbeddingConfig(), nil).(*usageEmbedder)
			ctx, span := withUsageSpan(tenantCtx(1))
			noteEmbeddingTokens(ctx, tc.input, tc.total)
			start := time.Date(2026, 8, 31, 1, 2, 3, 4, time.UTC)
			w.record(ctx, span, start, 2, nil, true)

			u := repo.last()
			require.Equal(t, start, *u.StartedAt)
			require.Equal(t, tc.wantProv, u.TokenProvenance)
			if tc.wantInput != nil {
				require.NotNil(t, u.InputTokens)
				require.Equal(t, *tc.wantInput, *u.InputTokens)
			} else {
				require.Nil(t, u.InputTokens)
			}
			if tc.wantTotal != nil {
				require.NotNil(t, u.TotalTokens)
				require.Equal(t, *tc.wantTotal, *u.TotalTokens)
			} else {
				require.Nil(t, u.TotalTokens)
			}
			require.Nil(t, u.OutputTokens, "embedding has no output tokens")
		})
	}
}

func intPtr(v int) *int { return &v }

func TestUsageCountingTransportCountsAttempts(t *testing.T) {
	ctx, span := withUsageSpan(context.Background())
	transport := &usageCountingTransport{inner: roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody}, nil
	})}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://example.invalid", nil)
	require.NoError(t, err)

	for i := 0; i < 2; i++ {
		_, err = transport.RoundTrip(req)
		require.NoError(t, err)
	}
	require.Equal(t, int64(2), span.providerRequests.Load(), "each transport round-trip is one outbound attempt")
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func TestEmbeddingUsageEvaluationAttributionAndPurpose(t *testing.T) {
	repo := &fakeUsageRepo{}
	usage.SetRecorder(usage.NewRecorder(repo))
	defer usage.SetRecorder(nil)

	w := wrapEmbeddingUsage(&usageFakeEmbedder{}, testEmbeddingConfig(), nil)
	ctx := types.WithEvaluationRunID(tenantCtx(1), "run-7")
	_, err := w.BatchEmbed(ctx, []string{"a"})
	require.NoError(t, err)

	u := repo.last()
	require.NotNil(t, u.EvaluationRunID)
	require.Equal(t, "run-7", *u.EvaluationRunID)
	require.Equal(t, "", u.Purpose, "unknown embedding must not be defaulted to index_embedding")
}

type usageFakeEmbedder struct{}

type failingUsageEmbedder struct{ usageFakeEmbedder }

func (f *failingUsageEmbedder) BatchEmbed(ctx context.Context, _ []string) ([][]float32, error) {
	noteEmbeddingProviderRequest(ctx)
	return nil, context.DeadlineExceeded
}

type failingPooledUsageEmbedder struct{ usageFakeEmbedder }

func (f *failingPooledUsageEmbedder) BatchEmbedWithPool(
	ctx context.Context, _ Embedder, _ []string,
) ([][]float32, error) {
	noteEmbeddingProviderRequest(ctx)
	return nil, context.DeadlineExceeded
}

func (f *usageFakeEmbedder) Embed(ctx context.Context, _ string) ([]float32, error) {
	noteEmbeddingProviderRequest(ctx)
	return []float32{1}, nil
}

func (f *usageFakeEmbedder) BatchEmbed(ctx context.Context, _ []string) ([][]float32, error) {
	noteEmbeddingProviderRequest(ctx)
	return [][]float32{{1}}, nil
}

func (f *usageFakeEmbedder) BatchEmbedWithPool(ctx context.Context, _ Embedder, _ []string) ([][]float32, error) {
	noteEmbeddingProviderRequest(ctx)
	return [][]float32{{1}}, nil
}
func (f *usageFakeEmbedder) GetModelName() string { return "fake" }
func (f *usageFakeEmbedder) GetDimensions() int   { return 1 }
func (f *usageFakeEmbedder) GetModelID() string   { return "fake-id" }

func TestEffectiveEmbeddingModelNameUsesProviderOverride(t *testing.T) {
	e := &WeKnoraCloudEmbedder{modelName: "configured", remoteModelName: "provider-effective"}
	require.Equal(t, "provider-effective", *effectiveEmbeddingModelName(e))
}
