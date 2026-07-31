package artifact

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type memoryRepository struct {
	mu        sync.Mutex
	artifacts map[string]*Record
	getErr    error
	putErr    error
}

func newMemoryRepository() *memoryRepository {
	return &memoryRepository{artifacts: map[string]*Record{}}
}

func artifactMapKey(tenantID uint64, stage string, keyVersion int, key string) string {
	return fmt.Sprintf("%d:%s:%d:%s", tenantID, stage, keyVersion, key)
}

func (r *memoryRepository) PutIfAbsent(_ context.Context, artifact *Record) (bool, error) {
	if r.putErr != nil {
		return false, r.putErr
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	key := artifactMapKey(artifact.TenantID, artifact.Stage, artifact.KeyVersion, artifact.ArtifactKey)
	if _, ok := r.artifacts[key]; ok {
		return false, nil
	}
	copy := *artifact
	r.artifacts[key] = &copy
	return true, nil
}

func (r *memoryRepository) Get(
	_ context.Context, tenantID uint64, stage string, keyVersion int, artifactKey string,
) (*Record, error) {
	if r.getErr != nil {
		return nil, r.getErr
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	key := artifactMapKey(tenantID, stage, keyVersion, artifactKey)
	artifact, ok := r.artifacts[key]
	if !ok {
		return nil, ErrCacheMiss
	}
	copy := *artifact
	return &copy, nil
}

func (r *memoryRepository) DeleteObservedChecksum(_ context.Context, tenantID uint64, id string, payloadChecksum string) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for key, artifact := range r.artifacts {
		if artifact.TenantID == tenantID && artifact.ID == id && artifact.PayloadChecksum == payloadChecksum {
			delete(r.artifacts, key)
			return true, nil
		}
	}
	return false, nil
}

func TestRuntimeCachesSuccessfulComputation(t *testing.T) {
	ctx := context.Background()
	repo := newMemoryRepository()
	runtime := NewRuntime(repo, RuntimeOptions{ReadEnabled: true, WriteEnabled: true, MaxInlineBytes: 1024})
	material := KeyMaterial{
		KeyVersion:      1,
		Stage:           "parse",
		DirectInputs:    []InputDigest{{Name: "file", Digest: "abc"}},
		Processor:       ProcessorIdentity{Name: "parser", Version: "v1"},
		RenderedRequest: map[string]any{"file": "abc.pdf"},
		OutputSchema:    "parse.v1",
	}
	var calls int

	first, firstMeta, err := runtime.GetOrCompute(ctx, 7, material, func(context.Context) ([]byte, error) {
		calls++
		return []byte(`{"chunks":[]}`), nil
	})
	require.NoError(t, err)
	second, secondMeta, err := runtime.GetOrCompute(ctx, 7, material, func(context.Context) ([]byte, error) {
		calls++
		return []byte(`{"should_not":"run"}`), nil
	})
	require.NoError(t, err)

	assert.Equal(t, []byte(`{"chunks":[]}`), first)
	assert.Equal(t, []byte(`{"chunks":[]}`), second)
	assert.Equal(t, OutcomeComputed, firstMeta.Outcome)
	assert.Equal(t, OutcomeHit, secondMeta.Outcome)
	assert.Equal(t, 1, calls)
}

func TestRuntimeDoesNotCacheProviderErrors(t *testing.T) {
	ctx := context.Background()
	repo := newMemoryRepository()
	runtime := NewRuntime(repo, RuntimeOptions{ReadEnabled: true, WriteEnabled: true, MaxInlineBytes: 1024})
	material := KeyMaterial{KeyVersion: 1, Stage: "vlm_ocr", OutputSchema: "ocr.v1"}
	providerErr := errors.New("provider timeout")

	_, _, err := runtime.GetOrCompute(ctx, 7, material, func(context.Context) ([]byte, error) {
		return nil, providerErr
	})
	require.ErrorIs(t, err, providerErr)
	_, meta, err := runtime.GetOrCompute(ctx, 7, material, func(context.Context) ([]byte, error) {
		return []byte(`{"text":""}`), nil
	})
	require.NoError(t, err)

	assert.Equal(t, OutcomeComputed, meta.Outcome)
}

func TestRuntimeFailOpenOnRepositoryErrors(t *testing.T) {
	ctx := context.Background()
	repo := newMemoryRepository()
	repo.getErr = errors.New("cache unavailable")
	repo.putErr = errors.New("cache write unavailable")
	runtime := NewRuntime(repo, RuntimeOptions{ReadEnabled: true, WriteEnabled: true, MaxInlineBytes: 1024})

	payload, meta, err := runtime.GetOrCompute(ctx, 7, KeyMaterial{KeyVersion: 1, Stage: "embedding"}, func(context.Context) ([]byte, error) {
		return []byte(`[1,2,3]`), nil
	})
	require.NoError(t, err)

	assert.Equal(t, []byte(`[1,2,3]`), payload)
	assert.Equal(t, OutcomeBypass, meta.Outcome)
}

func TestRuntimeRewritesAfterCorruptCacheEntry(t *testing.T) {
	ctx := context.Background()
	repo := newMemoryRepository()
	runtime := NewRuntime(repo, RuntimeOptions{ReadEnabled: true, WriteEnabled: true, MaxInlineBytes: 1024})
	material := KeyMaterial{
		KeyVersion:      1,
		Stage:           "parse",
		RenderedRequest: map[string]any{"file": "doc.pdf"},
		OutputSchema:    "parse.v1",
	}
	key, err := BuildKey(material)
	require.NoError(t, err)
	_, err = repo.PutIfAbsent(ctx, &Record{
		ID:              "corrupt-record",
		TenantID:        7,
		Stage:           "parse",
		KeyVersion:      1,
		ArtifactKey:     key,
		OutputSchema:    "parse.v1",
		Codec:           "json",
		Payload:         []byte(`{"old":true}`),
		PayloadChecksum: "not-the-payload-checksum",
		PayloadSize:     int64(len([]byte(`{"old":true}`))),
	})
	require.NoError(t, err)

	payload, meta, err := runtime.GetOrCompute(ctx, 7, material, func(context.Context) ([]byte, error) {
		return []byte(`{"fresh":true}`), nil
	})
	require.NoError(t, err)
	assert.Equal(t, []byte(`{"fresh":true}`), payload)
	assert.Equal(t, OutcomeComputed, meta.Outcome)

	got, err := repo.Get(ctx, 7, "parse", 1, key)
	require.NoError(t, err)
	assert.Equal(t, []byte(`{"fresh":true}`), got.Payload)
	assert.Equal(t, Checksum(got.Payload), got.PayloadChecksum)
}

func TestRuntimeReturnsIndependentPayloadCopies(t *testing.T) {
	ctx := context.Background()
	repo := newMemoryRepository()
	runtime := NewRuntime(repo, RuntimeOptions{ReadEnabled: true, WriteEnabled: true, MaxInlineBytes: 1024})
	material := KeyMaterial{
		KeyVersion:      1,
		Stage:           "summary",
		RenderedRequest: map[string]any{"input": "same"},
		OutputSchema:    "summary.v1",
	}

	first, _, err := runtime.GetOrCompute(ctx, 7, material, func(context.Context) ([]byte, error) {
		return []byte(`{"summary":"stable"}`), nil
	})
	require.NoError(t, err)
	first[0] = '['

	second, meta, err := runtime.GetOrCompute(ctx, 7, material, func(context.Context) ([]byte, error) {
		return []byte(`{"summary":"changed"}`), nil
	})
	require.NoError(t, err)
	assert.Equal(t, OutcomeHit, meta.Outcome)
	assert.Equal(t, []byte(`{"summary":"stable"}`), second)
}

func TestRuntimeSetsArtifactExpiryWhenRetentionConfigured(t *testing.T) {
	ctx := context.Background()
	repo := newMemoryRepository()
	runtime := NewRuntime(repo, RuntimeOptions{
		ReadEnabled:    true,
		WriteEnabled:   true,
		MaxInlineBytes: 1024,
		Retention:      time.Hour,
	})
	material := KeyMaterial{KeyVersion: 1, Stage: "summary", RenderedRequest: map[string]any{"input": "doc"}}
	key, err := BuildKey(material)
	require.NoError(t, err)

	before := time.Now()
	_, _, err = runtime.GetOrCompute(ctx, 7, material, func(context.Context) ([]byte, error) {
		return []byte(`{"summary":"cached"}`), nil
	})
	require.NoError(t, err)

	got, err := repo.Get(ctx, 7, "summary", 1, key)
	require.NoError(t, err)
	require.NotNil(t, got.ExpiresAt)
	require.True(t, got.ExpiresAt.After(before.Add(59*time.Minute)))
	require.True(t, got.ExpiresAt.Before(time.Now().Add(61*time.Minute)))
}

func TestRuntimeSingleflightComputesOnce(t *testing.T) {
	ctx := context.Background()
	repo := newMemoryRepository()
	runtime := NewRuntime(repo, RuntimeOptions{ReadEnabled: true, WriteEnabled: true, MaxInlineBytes: 1024})
	material := KeyMaterial{KeyVersion: 1, Stage: "embedding", RenderedRequest: map[string]any{"input": "same"}}

	start := make(chan struct{})
	var calls int
	var mu sync.Mutex
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, _, err := runtime.GetOrCompute(ctx, 7, material, func(context.Context) ([]byte, error) {
				mu.Lock()
				calls++
				mu.Unlock()
				return []byte(`{"vector":[1]}`), nil
			})
			require.NoError(t, err)
		}()
	}
	close(start)
	wg.Wait()

	assert.Equal(t, 1, calls)
}

func TestRuntimeBatchPartialHitDeduplicatesMissesAndRestoresOrder(t *testing.T) {
	ctx := context.Background()
	repo := newMemoryRepository()
	runtime := NewRuntime(repo, RuntimeOptions{ReadEnabled: true, WriteEnabled: true, MaxInlineBytes: 1024})
	hitMaterial := KeyMaterial{
		KeyVersion:      1,
		Stage:           "embedding",
		RenderedRequest: map[string]any{"input": "cached"},
		OutputSchema:    "embedding.v1",
	}
	hitKey, err := BuildKey(hitMaterial)
	require.NoError(t, err)
	payload := []byte(`[0.1]`)
	_, err = repo.PutIfAbsent(ctx, &Record{
		ID:              "cached-record",
		TenantID:        7,
		Stage:           "embedding",
		KeyVersion:      1,
		ArtifactKey:     hitKey,
		OutputSchema:    "embedding.v1",
		Codec:           "json",
		Payload:         payload,
		PayloadChecksum: Checksum(payload),
		PayloadSize:     int64(len(payload)),
	})
	require.NoError(t, err)

	missMaterial := KeyMaterial{
		KeyVersion:      1,
		Stage:           "embedding",
		RenderedRequest: map[string]any{"input": "new"},
		OutputSchema:    "embedding.v1",
	}
	items := []KeyMaterial{missMaterial, hitMaterial, missMaterial, hitMaterial}

	got, meta, err := runtime.GetOrComputeBatch(ctx, 7, items, func(_ context.Context, misses []BatchMiss) ([][]byte, error) {
		require.Len(t, misses, 1)
		assert.Equal(t, 0, misses[0].FirstIndex)
		return [][]byte{[]byte(`[0.2]`)}, nil
	})
	require.NoError(t, err)

	require.Len(t, got, 4)
	assert.Equal(t, []byte(`[0.2]`), got[0])
	assert.Equal(t, []byte(`[0.1]`), got[1])
	assert.Equal(t, []byte(`[0.2]`), got[2])
	assert.Equal(t, []byte(`[0.1]`), got[3])
	assert.Equal(t, 2, meta.Total)
	assert.Equal(t, 1, meta.Hits)
	assert.Equal(t, 1, meta.Misses)
	assert.Equal(t, 2, meta.Deduplicated)
}

func TestRuntimeBatchDoesNotCachePartialProviderOutput(t *testing.T) {
	ctx := context.Background()
	repo := newMemoryRepository()
	runtime := NewRuntime(repo, RuntimeOptions{ReadEnabled: true, WriteEnabled: true, MaxInlineBytes: 1024})
	items := []KeyMaterial{
		{KeyVersion: 1, Stage: "embedding", RenderedRequest: map[string]any{"input": "a"}, OutputSchema: "embedding.v1"},
		{KeyVersion: 1, Stage: "embedding", RenderedRequest: map[string]any{"input": "b"}, OutputSchema: "embedding.v1"},
	}

	_, _, err := runtime.GetOrComputeBatch(ctx, 7, items, func(context.Context, []BatchMiss) ([][]byte, error) {
		return [][]byte{[]byte(`[1]`)}, nil
	})
	require.Error(t, err)

	_, meta, err := runtime.GetOrComputeBatch(ctx, 7, items, func(context.Context, []BatchMiss) ([][]byte, error) {
		return [][]byte{[]byte(`[1]`), []byte(`[2]`)}, nil
	})
	require.NoError(t, err)
	assert.Equal(t, 2, meta.Misses)
}
