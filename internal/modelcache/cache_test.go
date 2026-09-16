package modelcache

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"sync"
	"testing"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/embedding"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type cacheStore struct {
	mu        sync.Mutex
	entries   map[string]*types.EmbeddingCacheEntry
	getErr    error
	putErr    error
	recordErr error
	events    []*types.EmbeddingCacheLookupRecord
}

func cacheStoreKey(prefix CachePrefix, hash string) string {
	return fmt.Sprintf(
		"%d/%s/%s/%s/%s", prefix.TenantID, prefix.ModelID,
		prefix.ModelFingerprint, prefix.RequestOptionsSHA256, hash,
	)
}

func (s *cacheStore) GetEmbeddingCache(
	_ context.Context,
	prefix CachePrefix,
	hashes []string,
) (map[string]*types.EmbeddingCacheEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.getErr != nil {
		return nil, s.getErr
	}
	result := map[string]*types.EmbeddingCacheEntry{}
	for _, hash := range hashes {
		if entry := s.entries[cacheStoreKey(prefix, hash)]; entry != nil {
			cloned := *entry
			result[hash] = &cloned
		}
	}
	return result, nil
}

func (s *cacheStore) PutEmbeddingCache(_ context.Context, entries []*types.EmbeddingCacheEntry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.putErr != nil {
		return s.putErr
	}
	if s.entries == nil {
		s.entries = map[string]*types.EmbeddingCacheEntry{}
	}
	for _, entry := range entries {
		cloned := *entry
		cloned.Embedding = append([]byte(nil), entry.Embedding...)
		prefix := CachePrefix{
			TenantID: entry.TenantID, ModelID: entry.ModelID,
			ModelFingerprint: entry.ModelFingerprint, RequestOptionsSHA256: entry.RequestOptionsSHA256,
		}
		s.entries[cacheStoreKey(prefix, entry.TextSHA256)] = &cloned
	}
	return nil
}

func (s *cacheStore) RecordEmbeddingCacheLookup(_ context.Context, event *types.EmbeddingCacheLookupRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.recordErr != nil {
		return s.recordErr
	}
	cloned := *event
	s.events = append(s.events, &cloned)
	return nil
}

func TestEmbeddingCacheReadAndWriteFailuresAreFailOpen(t *testing.T) {
	store := &cacheStore{getErr: errors.New("read unavailable"), putErr: errors.New("write unavailable")}
	provider := &countingEmbedder{}
	wrapped := NewCoordinator(store).Wrap(&types.Model{ID: "embedding-1", TenantID: 7}, provider)
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7))
	result, err := wrapped.BatchEmbed(ctx, []string{"alpha", "alpha"})
	require.NoError(t, err)
	require.Len(t, result, 2)
	assert.Equal(t, result[0], result[1])
	require.Len(t, provider.batchInputs, 1)
}

func TestEmbeddingCachePersistFailuresLogSanitizedWarningAndStayFailOpen(t *testing.T) {
	for _, tc := range []struct {
		name, putKind, recordKind string
		putErr, recordErr         error
	}{
		{
			name: "storage", putKind: "storage", recordKind: "storage",
			putErr:    errors.New("alpha vector=[0.125,0.25] credential=synthetic-secret"),
			recordErr: errors.New("alpha vector=[0.125,0.25] credential=synthetic-secret"),
		},
		{
			name: "wrapped_context", putKind: "timeout", recordKind: "canceled",
			putErr:    fmt.Errorf("alpha credential=synthetic-secret: %w", context.DeadlineExceeded),
			recordErr: fmt.Errorf("alpha credential=synthetic-secret: %w", context.Canceled),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &cacheStore{putErr: tc.putErr, recordErr: tc.recordErr}
			provider := &countingEmbedder{}
			wrapped := NewCoordinator(store).Wrap(&types.Model{ID: "private-model-id", TenantID: 7}, provider)
			ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7))
			var logBuffer bytes.Buffer
			logger.SetOutput(&logBuffer)
			defer logger.SetOutput(os.Stdout)

			result, err := wrapped.BatchEmbed(ctx, []string{"alpha"})
			require.NoError(t, err)
			require.Len(t, result, 1)
			logs := logBuffer.String()
			assert.Contains(
				t,
				logs,
				"Embedding cache persist failed (vectors still returned): entries 1, error_kind "+tc.putKind,
			)
			assert.Contains(
				t,
				logs,
				"Embedding cache lookup record persist failed: status miss, error_kind "+tc.recordKind,
			)
			for _, sensitive := range []string{
				"alpha", "vector=[", "synthetic-secret", "private-model-id", "tenant 7",
			} {
				assert.NotContains(t, logs, sensitive)
			}

			// Failed persistence leaves a miss; the next call retries the provider.
			_, err = wrapped.BatchEmbed(ctx, []string{"alpha"})
			require.NoError(t, err)
			assert.Len(t, provider.batchInputs, 2)
			assert.Empty(t, store.events)
		})
	}
}

func TestEmbeddingCacheRejectsNonFiniteProviderVector(t *testing.T) {
	provider := &invalidEmbedder{}
	wrapped := NewCoordinator(&cacheStore{}).Wrap(&types.Model{ID: "embedding-1", TenantID: 7}, provider)
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7))
	_, err := wrapped.BatchEmbed(ctx, []string{"alpha"})
	require.ErrorContains(t, err, "non-finite")
}

func TestEmbeddingCacheRefetchesMalformedStoredVector(t *testing.T) {
	model := &types.Model{ID: "embedding-1", TenantID: 7}
	provider := &countingEmbedder{}
	prefix := CachePrefix{
		TenantID: 7, ModelID: model.ID,
		ModelFingerprint:     EmbeddingModelFingerprint(model),
		RequestOptionsSHA256: requestOptionsSHA256(provider.GetDimensions()),
	}
	textHash := sha256Hex([]byte("alpha"))
	store := &cacheStore{entries: map[string]*types.EmbeddingCacheEntry{
		cacheStoreKey(prefix, textHash): {
			TenantID: 7, ModelID: model.ID, ModelFingerprint: prefix.ModelFingerprint,
			RequestOptionsSHA256: prefix.RequestOptionsSHA256, TextSHA256: textHash,
			Embedding: []byte{1, 2, 3, 4}, Dimension: provider.GetDimensions(),
		},
	}}
	wrapped := NewCoordinator(store).Wrap(model, provider)
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7))

	result, err := wrapped.BatchEmbed(ctx, []string{"alpha"})
	require.NoError(t, err)
	require.Equal(t, [][]float32{{5, 1}}, result)
	require.Len(t, provider.batchInputs, 1)
}

type invalidEmbedder struct{ countingEmbedder }

func (*invalidEmbedder) BatchEmbed(context.Context, []string) ([][]float32, error) {
	return [][]float32{{float32(math.NaN()), 1}}, nil
}

func TestEmbeddingCacheSeparatesTenants(t *testing.T) {
	store := &cacheStore{}
	provider := &countingEmbedder{}
	wrapped := NewCoordinator(store).Wrap(&types.Model{ID: "embedding-1", TenantID: 7}, provider)
	for _, tenantID := range []uint64{7, 8} {
		ctx := context.WithValue(context.Background(), types.TenantIDContextKey, tenantID)
		_, err := wrapped.BatchEmbed(ctx, []string{"same"})
		require.NoError(t, err)
	}
	provider.mu.Lock()
	defer provider.mu.Unlock()
	assert.Len(t, provider.batchInputs, 2)
}
func (*cacheStore) DeleteExpiredEmbeddingCache(context.Context, int) (int64, error) { return 0, nil }

type countingEmbedder struct {
	mu          sync.Mutex
	batchInputs [][]string
}

func (e *countingEmbedder) Embed(_ context.Context, text string) ([]float32, error) {
	return []float32{float32(len(text)), 1}, nil
}

func (e *countingEmbedder) BatchEmbed(_ context.Context, texts []string) ([][]float32, error) {
	e.mu.Lock()
	e.batchInputs = append(e.batchInputs, append([]string(nil), texts...))
	e.mu.Unlock()
	result := make([][]float32, len(texts))
	for i, text := range texts {
		result[i] = []float32{float32(len(text)), float32(i + 1)}
	}
	return result, nil
}

func (e *countingEmbedder) BatchEmbedWithPool(
	ctx context.Context,
	_ embedding.Embedder,
	texts []string,
) ([][]float32, error) {
	return e.BatchEmbed(ctx, texts)
}
func (*countingEmbedder) GetModelName() string { return "embedding" }
func (*countingEmbedder) GetDimensions() int   { return 2 }
func (*countingEmbedder) GetModelID() string   { return "embedding-1" }

func TestBatchEmbeddingCacheDeduplicatesMissesAndRestoresOrder(t *testing.T) {
	store := &cacheStore{}
	coordinator := NewCoordinator(store)
	provider := &countingEmbedder{}
	model := &types.Model{
		ID: "embedding-1", TenantID: 7, Name: "embedding", Type: types.ModelTypeEmbedding,
	}
	wrapped := coordinator.Wrap(model, provider)
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7))

	first, err := wrapped.BatchEmbed(ctx, []string{"alpha", "beta", "alpha"})
	require.NoError(t, err)
	require.Len(t, provider.batchInputs, 1)
	assert.Equal(t, []string{"alpha", "beta"}, provider.batchInputs[0])
	assert.Equal(t, first[0], first[2])

	second, err := wrapped.BatchEmbed(ctx, []string{"alpha", "beta", "alpha"})
	require.NoError(t, err)
	require.Len(t, provider.batchInputs, 1, "hot cache must send zero provider input items")
	assert.Equal(t, first, second)
	store.mu.Lock()
	defer store.mu.Unlock()
	require.Len(t, store.entries, 2)
	require.Len(t, store.events, 2)
	assert.Equal(t, int64(3), store.events[0].RequestedItems)
	assert.Equal(t, int64(2), store.events[0].UniqueItems)
	assert.Equal(t, int64(2), store.events[0].MissItems)
	assert.Equal(t, types.EmbeddingCacheLookupStatusMiss, store.events[0].Status)
	assert.Equal(t, int64(2), store.events[1].HitItems)
	assert.Equal(t, types.EmbeddingCacheLookupStatusHit, store.events[1].Status)
}

func TestEmbeddingCacheSingleflightIsSharedAcrossWrappers(t *testing.T) {
	store := &cacheStore{}
	coordinator := NewCoordinator(store)
	provider := &countingEmbedder{}
	model := &types.Model{ID: "embedding-1", TenantID: 7, Name: "embedding", Type: types.ModelTypeEmbedding}
	first := coordinator.Wrap(model, provider)
	second := coordinator.Wrap(model, provider)
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7))
	start := make(chan struct{})
	var wait sync.WaitGroup
	wait.Add(2)
	for _, embedder := range []embedding.Embedder{first, second} {
		go func(item embedding.Embedder) {
			defer wait.Done()
			<-start
			_, _ = item.BatchEmbed(ctx, []string{"same"})
		}(embedder)
	}
	close(start)
	wait.Wait()
	provider.mu.Lock()
	defer provider.mu.Unlock()
	assert.Len(t, provider.batchInputs, 1)
}

func TestEmbeddingModelFingerprintUsesExplicitBehaviorRevision(t *testing.T) {
	model := &types.Model{
		ID: "embedding-1", Name: "fixture", Source: types.ModelSourceOpenAI,
		Parameters: types.ModelParameters{
			Provider: "openai",
			BaseURL:  "https://user:password@api.example.test/v1",
			CustomHeaders: map[string]string{
				"X-Model-Route": "blue",
				"X-Tenant-Auth": "credential-1",
				"X-Request-ID":  "request-1",
			},
			ExtraConfig: map[string]string{
				"behavior_revision": "route-v1",
				"tokenizer":         "cl100k_base",
				"max_tokens":        "2048",
				"session_token":     "secret-1",
				"openaiApiKey":      "secret-2",
			},
		},
	}
	baseline := EmbeddingModelFingerprint(model)

	identityChanged := *model
	identityChanged.Parameters = model.Parameters
	identityChanged.Parameters.BaseURL = "https://other:new-password@api.example.test/v1"
	identityChanged.Parameters.CustomHeaders = map[string]string{
		"X-Model-Route": "blue",
		"X-Tenant-Auth": "credential-2",
		"X-Request-ID":  "request-2",
	}
	identityChanged.Parameters.ExtraConfig = map[string]string{
		"tokenizer": "cl100k_base", "max_tokens": "2048",
		"behavior_revision": "route-v1", "session_token": "secret-3", "openaiApiKey": "secret-4",
	}
	require.Equal(t, baseline, EmbeddingModelFingerprint(&identityChanged))

	headerChanged := identityChanged
	headerChanged.Parameters = identityChanged.Parameters
	headerChanged.Parameters.CustomHeaders = map[string]string{"X-Model-Route": "another-route"}
	require.Equal(t, baseline, EmbeddingModelFingerprint(&headerChanged))

	behaviorChanged := identityChanged
	behaviorChanged.Parameters = identityChanged.Parameters
	behaviorChanged.Parameters.ExtraConfig = map[string]string{
		"behavior_revision": "route-v2", "tokenizer": "cl100k_base", "max_tokens": "2048",
	}
	require.NotEqual(t, baseline, EmbeddingModelFingerprint(&behaviorChanged))

	tokenizerChanged := identityChanged
	tokenizerChanged.Parameters = identityChanged.Parameters
	tokenizerChanged.Parameters.ExtraConfig = map[string]string{
		"behavior_revision": "route-v1", "tokenizer": "o200k_base", "max_tokens": "2048",
		"session_token": "secret-3", "openaiApiKey": "secret-4",
	}
	require.NotEqual(t, baseline, EmbeddingModelFingerprint(&tokenizerChanged))

	maxTokensChanged := identityChanged
	maxTokensChanged.Parameters = identityChanged.Parameters
	maxTokensChanged.Parameters.ExtraConfig = map[string]string{
		"behavior_revision": "route-v1", "tokenizer": "cl100k_base", "max_tokens": "4096",
		"session_token": "secret-3", "openaiApiKey": "secret-4",
	}
	require.NotEqual(t, baseline, EmbeddingModelFingerprint(&maxTokensChanged))
}
