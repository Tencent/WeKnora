package service

import (
	"context"
	"errors"
	"math"
	"reflect"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/embedding"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type embeddingArtifactFakeStore struct {
	values        map[types.ProcessingArtifactKey][]byte
	getErr        error
	putErr        error
	invalidateErr error
	putCanonical  []byte
	getCalls      int
	putCalls      int
	invalidated   []types.ProcessingArtifactKey
	observed      [][]byte
}

type embeddingArtifactBatchFakeStore struct {
	*embeddingArtifactFakeStore
	getManyCalls     int
	putManyCalls     int
	putManyCanonical map[types.ProcessingArtifactKey][]byte
}

func newEmbeddingArtifactBatchFakeStore() *embeddingArtifactBatchFakeStore {
	return &embeddingArtifactBatchFakeStore{
		embeddingArtifactFakeStore: newEmbeddingArtifactFakeStore(),
	}
}

func (s *embeddingArtifactBatchFakeStore) GetMany(
	_ context.Context,
	keys []types.ProcessingArtifactKey,
) (map[types.ProcessingArtifactKey][]byte, error) {
	s.getManyCalls++
	result := make(map[types.ProcessingArtifactKey][]byte, len(keys))
	for _, key := range keys {
		if value, ok := s.values[key]; ok {
			result[key] = append([]byte(nil), value...)
		}
	}
	return result, nil
}

func (s *embeddingArtifactBatchFakeStore) PutManyIfAbsent(
	_ context.Context,
	values map[types.ProcessingArtifactKey][]byte,
) (map[types.ProcessingArtifactKey][]byte, error) {
	s.putManyCalls++
	result := make(map[types.ProcessingArtifactKey][]byte, len(values))
	for key, value := range values {
		if canonical, ok := s.putManyCanonical[key]; ok {
			result[key] = append([]byte(nil), canonical...)
			continue
		}
		if _, ok := s.values[key]; !ok {
			s.values[key] = append([]byte(nil), value...)
		}
		result[key] = append([]byte(nil), s.values[key]...)
	}
	return result, nil
}

func newEmbeddingArtifactFakeStore() *embeddingArtifactFakeStore {
	return &embeddingArtifactFakeStore{values: make(map[types.ProcessingArtifactKey][]byte)}
}

func (s *embeddingArtifactFakeStore) Get(
	_ context.Context,
	key types.ProcessingArtifactKey,
) ([]byte, bool, error) {
	s.getCalls++
	if s.getErr != nil {
		return nil, false, s.getErr
	}
	value, ok := s.values[key]
	return append([]byte(nil), value...), ok, nil
}

func (s *embeddingArtifactFakeStore) PutIfAbsent(
	_ context.Context,
	key types.ProcessingArtifactKey,
	value []byte,
) ([]byte, bool, error) {
	s.putCalls++
	if s.putErr != nil {
		return nil, false, s.putErr
	}
	if s.putCanonical != nil {
		return append([]byte(nil), s.putCanonical...), false, nil
	}
	if canonical, ok := s.values[key]; ok {
		return append([]byte(nil), canonical...), false, nil
	}
	s.values[key] = append([]byte(nil), value...)
	return append([]byte(nil), value...), true, nil
}

func (s *embeddingArtifactFakeStore) Invalidate(
	_ context.Context,
	key types.ProcessingArtifactKey,
	observed []byte,
) error {
	s.invalidated = append(s.invalidated, key)
	s.observed = append(s.observed, append([]byte(nil), observed...))
	if s.invalidateErr != nil {
		return s.invalidateErr
	}
	delete(s.values, key)
	return nil
}

type embeddingArtifactFakeEmbedder struct {
	modelID      string
	modelName    string
	dimensions   int
	batchResults [][]float32
	err          error
	embedCalls   []string
	batchCalls   [][]string
	poolCalls    int
	poolModel    embedding.Embedder
}

func (e *embeddingArtifactFakeEmbedder) Embed(_ context.Context, text string) ([]float32, error) {
	e.embedCalls = append(e.embedCalls, text)
	if e.err != nil {
		return nil, e.err
	}
	if len(e.batchResults) == 0 {
		return nil, nil
	}
	return append([]float32(nil), e.batchResults[0]...), nil
}

func (e *embeddingArtifactFakeEmbedder) BatchEmbed(_ context.Context, texts []string) ([][]float32, error) {
	e.batchCalls = append(e.batchCalls, append([]string(nil), texts...))
	if e.err != nil {
		return nil, e.err
	}
	return cloneEmbeddingVectors(e.batchResults), nil
}

func (e *embeddingArtifactFakeEmbedder) BatchEmbedWithPool(
	ctx context.Context,
	model embedding.Embedder,
	texts []string,
) ([][]float32, error) {
	e.poolCalls++
	e.poolModel = model
	return model.BatchEmbed(ctx, texts)
}

func (e *embeddingArtifactFakeEmbedder) GetModelName() string { return e.modelName }
func (e *embeddingArtifactFakeEmbedder) GetDimensions() int   { return e.dimensions }
func (e *embeddingArtifactFakeEmbedder) GetModelID() string   { return e.modelID }

func cloneEmbeddingVectors(vectors [][]float32) [][]float32 {
	result := make([][]float32, len(vectors))
	for i := range vectors {
		result[i] = append([]float32(nil), vectors[i]...)
	}
	return result
}

func documentEmbeddingContext() context.Context {
	return context.WithValue(context.Background(), types.EmbedDocumentContextKey, true)
}

func TestNewEmbeddingArtifactKeyNormalizesAndScopesInputs(t *testing.T) {
	base := embeddingArtifactKeyRequest{
		tenantID:             7,
		modelID:              "model-1",
		modelName:            "text-embedding",
		modelRevision:        "2026-07-16T10:00:00Z",
		dimensions:           3,
		normalizationVersion: "embedding-text-v1",
		text:                 "  alpha\r\nbeta\r  ",
	}

	baseKey, normalized, err := newEmbeddingArtifactKey(base)
	require.NoError(t, err)
	assert.Equal(t, "alpha\nbeta", normalized)
	assert.Equal(t, "embedding.vector", baseKey.Stage)
	assert.Equal(t, uint16(1), baseKey.KeyVersion)

	stable := base
	stable.text = "alpha\nbeta"
	stableKey, stableText, err := newEmbeddingArtifactKey(stable)
	require.NoError(t, err)
	assert.Equal(t, "alpha\nbeta", stableText)
	assert.Equal(t, baseKey, stableKey)

	tests := []struct {
		name   string
		mutate func(*embeddingArtifactKeyRequest)
	}{
		{name: "tenant", mutate: func(r *embeddingArtifactKeyRequest) { r.tenantID++ }},
		{name: "model ID", mutate: func(r *embeddingArtifactKeyRequest) { r.modelID = "model-2" }},
		{name: "model name", mutate: func(r *embeddingArtifactKeyRequest) { r.modelName = "other" }},
		{name: "model revision", mutate: func(r *embeddingArtifactKeyRequest) { r.modelRevision = "revision-2" }},
		{name: "dimensions", mutate: func(r *embeddingArtifactKeyRequest) { r.dimensions++ }},
		{name: "normalizer", mutate: func(r *embeddingArtifactKeyRequest) { r.normalizationVersion = "embedding-text-v2" }},
		{name: "text", mutate: func(r *embeddingArtifactKeyRequest) { r.text = "other" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			changed := base
			tt.mutate(&changed)
			key, _, err := newEmbeddingArtifactKey(changed)
			require.NoError(t, err)
			assert.NotEqual(t, baseKey, key)
		})
	}
}

func TestNewEmbeddingArtifactKeyRejectsMissingModelRevision(t *testing.T) {
	_, _, err := newEmbeddingArtifactKey(embeddingArtifactKeyRequest{
		tenantID:             7,
		modelID:              "model-1",
		modelName:            "text-embedding",
		dimensions:           2,
		normalizationVersion: embeddingArtifactNormalizationVersion,
		text:                 "text",
	})
	assert.Error(t, err)
}

func TestEmbeddingVectorCodecRoundTripAndRejectsInvalidPayload(t *testing.T) {
	encoded, err := encodeEmbeddingVector([]float32{1.25, -2.5, 0})
	require.NoError(t, err)
	decoded, err := decodeEmbeddingVector(encoded, 3)
	require.NoError(t, err)
	assert.Equal(t, []float32{1.25, -2.5, 0}, decoded)

	_, err = decodeEmbeddingVector([]byte{99, 0, 0, 0, 0}, 0)
	assert.Error(t, err)
	_, err = decodeEmbeddingVector(encoded[:len(encoded)-1], 3)
	assert.Error(t, err)
	_, err = decodeEmbeddingVector(encoded, 2)
	assert.Error(t, err)
}

func TestEmbeddingArtifactBatchPartialHitsDeduplicatesMissesAndRestoresOrder(t *testing.T) {
	store := newEmbeddingArtifactFakeStore()
	inner := &embeddingArtifactFakeEmbedder{
		modelID: "model-1", modelName: "text-embedding", dimensions: 2,
		batchResults: [][]float32{{2, 20}, {3, 30}},
	}
	wrapped := newEmbeddingArtifactEmbedder(inner, store, 7, "revision-1").(*embeddingArtifactEmbedder)

	hitKey, _, err := newEmbeddingArtifactKey(embeddingArtifactKeyRequest{
		tenantID: 7, modelID: "model-1", modelName: "text-embedding", modelRevision: "revision-1",
		dimensions: 2, normalizationVersion: embeddingArtifactNormalizationVersion, text: "hit",
	})
	require.NoError(t, err)
	store.values[hitKey], err = encodeEmbeddingVector([]float32{1, 10})
	require.NoError(t, err)

	got, err := wrapped.BatchEmbed(documentEmbeddingContext(), []string{
		" hit ", "miss\r\none", "miss\r\none", "miss two", "hit",
	})
	require.NoError(t, err)
	assert.Equal(t, [][]float32{{1, 10}, {2, 20}, {2, 20}, {3, 30}, {1, 10}}, got)
	assert.Equal(t, [][]string{{"miss\none", "miss two"}}, inner.batchCalls)
	assert.Equal(t, 2, store.putCalls)
}

func TestEmbeddingArtifactBatchUsesBulkStoreOperations(t *testing.T) {
	store := newEmbeddingArtifactBatchFakeStore()
	inner := &embeddingArtifactFakeEmbedder{
		modelID: "model-1", modelName: "text-embedding", dimensions: 2,
		batchResults: [][]float32{{2, 20}, {3, 30}},
	}
	wrapped := newEmbeddingArtifactEmbedder(inner, store, 7, "revision-1")

	hitKey, _, err := newEmbeddingArtifactKey(embeddingArtifactKeyRequest{
		tenantID: 7, modelID: "model-1", modelName: "text-embedding", modelRevision: "revision-1",
		dimensions: 2, normalizationVersion: embeddingArtifactNormalizationVersion, text: "hit",
	})
	require.NoError(t, err)
	store.values[hitKey], err = encodeEmbeddingVector([]float32{1, 10})
	require.NoError(t, err)

	got, err := wrapped.BatchEmbed(documentEmbeddingContext(), []string{
		"hit", "miss one", "miss one", "miss two",
	})
	require.NoError(t, err)
	assert.Equal(t, [][]float32{{1, 10}, {2, 20}, {2, 20}, {3, 30}}, got)
	assert.Equal(t, 1, store.getManyCalls)
	assert.Equal(t, 1, store.putManyCalls)
	assert.Zero(t, store.getCalls)
	assert.Zero(t, store.putCalls)
}

func TestEmbeddingArtifactBatchHandlesEmptyInput(t *testing.T) {
	store := newEmbeddingArtifactFakeStore()
	inner := &embeddingArtifactFakeEmbedder{dimensions: 2}
	wrapped := newEmbeddingArtifactEmbedder(inner, store, 7, "revision-1")

	got, err := wrapped.BatchEmbed(documentEmbeddingContext(), nil)
	require.NoError(t, err)
	assert.Empty(t, got)
	assert.Empty(t, inner.batchCalls)
	assert.Zero(t, store.getCalls)
}

func TestEmbeddingArtifactBatchRejectsWrongProviderCountWithoutStorage(t *testing.T) {
	store := newEmbeddingArtifactFakeStore()
	inner := &embeddingArtifactFakeEmbedder{dimensions: 2, batchResults: [][]float32{{1, 2}}}
	wrapped := newEmbeddingArtifactEmbedder(inner, store, 7, "revision-1")

	_, err := wrapped.BatchEmbed(documentEmbeddingContext(), []string{"one", "two"})
	assert.ErrorContains(t, err, "returned 1 embeddings for 2 inputs")
	assert.Zero(t, store.putCalls)
}

func TestEmbeddingArtifactUnknownDimensionsBypassesCache(t *testing.T) {
	store := newEmbeddingArtifactFakeStore()
	inner := &embeddingArtifactFakeEmbedder{
		modelID: "model-1", modelName: "text-embedding", dimensions: 0,
		batchResults: [][]float32{{1, 2}},
	}
	wrapped := newEmbeddingArtifactEmbedder(inner, store, 7, "revision-1")

	_, err := wrapped.BatchEmbed(documentEmbeddingContext(), []string{"text"})
	require.NoError(t, err)
	_, err = wrapped.BatchEmbed(documentEmbeddingContext(), []string{"text"})
	require.NoError(t, err)
	assert.Len(t, inner.batchCalls, 2)
	assert.Zero(t, store.getCalls)
	assert.Zero(t, store.putCalls)
}

func TestEmbeddingArtifactRejectsNonFiniteVectorWithoutStorage(t *testing.T) {
	for _, value := range []float32{float32(math.NaN()), float32(math.Inf(1)), float32(math.Inf(-1))} {
		store := newEmbeddingArtifactFakeStore()
		inner := &embeddingArtifactFakeEmbedder{dimensions: 1, batchResults: [][]float32{{value}}}
		wrapped := newEmbeddingArtifactEmbedder(inner, store, 7, "revision-1")

		_, err := wrapped.BatchEmbed(documentEmbeddingContext(), []string{"text"})
		assert.ErrorContains(t, err, "non-finite")
		assert.Zero(t, store.putCalls)
	}
}

func TestEmbeddingArtifactBatchProviderAndStoreErrorsPropagate(t *testing.T) {
	t.Run("provider error is not stored", func(t *testing.T) {
		store := newEmbeddingArtifactFakeStore()
		providerErr := errors.New("provider unavailable")
		inner := &embeddingArtifactFakeEmbedder{dimensions: 2, err: providerErr}
		wrapped := newEmbeddingArtifactEmbedder(inner, store, 7, "revision-1")

		_, err := wrapped.BatchEmbed(documentEmbeddingContext(), []string{"one"})
		assert.ErrorIs(t, err, providerErr)
		assert.Zero(t, store.putCalls)
	})

	t.Run("get error", func(t *testing.T) {
		storeErr := errors.New("get failed")
		store := newEmbeddingArtifactFakeStore()
		store.getErr = storeErr
		inner := &embeddingArtifactFakeEmbedder{dimensions: 2}
		wrapped := newEmbeddingArtifactEmbedder(inner, store, 7, "revision-1")

		_, err := wrapped.BatchEmbed(documentEmbeddingContext(), []string{"one"})
		assert.ErrorIs(t, err, storeErr)
		assert.Empty(t, inner.batchCalls)
	})

	t.Run("put error", func(t *testing.T) {
		storeErr := errors.New("put failed")
		store := newEmbeddingArtifactFakeStore()
		store.putErr = storeErr
		inner := &embeddingArtifactFakeEmbedder{dimensions: 2, batchResults: [][]float32{{1, 2}}}
		wrapped := newEmbeddingArtifactEmbedder(inner, store, 7, "revision-1")

		_, err := wrapped.BatchEmbed(documentEmbeddingContext(), []string{"one"})
		assert.ErrorIs(t, err, storeErr)
	})
}

func TestEmbeddingArtifactBatchUsesPutIfAbsentWinner(t *testing.T) {
	store := newEmbeddingArtifactFakeStore()
	winner, err := encodeEmbeddingVector([]float32{9, 9})
	require.NoError(t, err)
	store.putCanonical = winner
	inner := &embeddingArtifactFakeEmbedder{dimensions: 2, batchResults: [][]float32{{1, 2}}}
	wrapped := newEmbeddingArtifactEmbedder(inner, store, 7, "revision-1")

	got, err := wrapped.BatchEmbed(documentEmbeddingContext(), []string{"one"})
	require.NoError(t, err)
	assert.Equal(t, [][]float32{{9, 9}}, got)
}

func TestEmbeddingArtifactRejectsNonFinitePutIfAbsentWinner(t *testing.T) {
	store := newEmbeddingArtifactFakeStore()
	winner, err := encodeEmbeddingVector([]float32{float32(math.NaN())})
	require.NoError(t, err)
	store.putCanonical = winner
	inner := &embeddingArtifactFakeEmbedder{dimensions: 1, batchResults: [][]float32{{1}}}
	wrapped := newEmbeddingArtifactEmbedder(inner, store, 7, "revision-1").(*embeddingArtifactEmbedder)

	key, _, err := wrapped.key("one")
	require.NoError(t, err)
	got, err := wrapped.BatchEmbed(documentEmbeddingContext(), []string{"one"})
	require.NoError(t, err)
	assert.Equal(t, [][]float32{{1}}, got)
	assert.Equal(t, []types.ProcessingArtifactKey{key}, store.invalidated)
	assert.Equal(t, [][]byte{winner}, store.observed)
}

func TestEmbeddingArtifactBatchInvalidatesNonFiniteConcurrentWinner(t *testing.T) {
	store := newEmbeddingArtifactBatchFakeStore()
	winner, err := encodeEmbeddingVector([]float32{float32(math.NaN())})
	require.NoError(t, err)
	inner := &embeddingArtifactFakeEmbedder{dimensions: 1, batchResults: [][]float32{{1}}}
	wrapped := newEmbeddingArtifactEmbedder(inner, store, 7, "revision-1").(*embeddingArtifactEmbedder)
	key, _, err := wrapped.key("one")
	require.NoError(t, err)
	store.putManyCanonical = map[types.ProcessingArtifactKey][]byte{key: winner}

	got, err := wrapped.BatchEmbed(documentEmbeddingContext(), []string{"one"})
	require.NoError(t, err)
	assert.Equal(t, [][]float32{{1}}, got)
	assert.Equal(t, []types.ProcessingArtifactKey{key}, store.invalidated)
	assert.Equal(t, [][]byte{winner}, store.observed)
}

func TestEmbeddingArtifactInvalidationFailureStopsSingleAndBatchFallbacks(t *testing.T) {
	t.Run("single", func(t *testing.T) {
		store := newEmbeddingArtifactFakeStore()
		store.invalidateErr = errors.New("invalidate failed")
		inner := &embeddingArtifactFakeEmbedder{dimensions: 1, batchResults: [][]float32{{1}}}
		wrapped := newEmbeddingArtifactEmbedder(inner, store, 7, "revision-1").(*embeddingArtifactEmbedder)
		key, _, err := wrapped.key("one")
		require.NoError(t, err)
		store.values[key] = []byte("corrupt")

		_, err = wrapped.Embed(documentEmbeddingContext(), "one")
		assert.ErrorIs(t, err, store.invalidateErr)
		assert.Empty(t, inner.embedCalls)
		assert.Equal(t, []types.ProcessingArtifactKey{key}, store.invalidated)
	})

	t.Run("batch", func(t *testing.T) {
		store := newEmbeddingArtifactBatchFakeStore()
		store.invalidateErr = errors.New("invalidate failed")
		inner := &embeddingArtifactFakeEmbedder{dimensions: 1, batchResults: [][]float32{{1}}}
		wrapped := newEmbeddingArtifactEmbedder(inner, store, 7, "revision-1").(*embeddingArtifactEmbedder)
		key, _, err := wrapped.key("one")
		require.NoError(t, err)
		store.values[key] = []byte("corrupt")

		_, err = wrapped.BatchEmbed(documentEmbeddingContext(), []string{"one"})
		assert.ErrorIs(t, err, store.invalidateErr)
		assert.Empty(t, inner.batchCalls)
		assert.Equal(t, []types.ProcessingArtifactKey{key}, store.invalidated)
	})
}

func TestEmbeddingArtifactEmbedReusesDocumentsAndBypassesUnmarkedInputs(t *testing.T) {
	store := newEmbeddingArtifactFakeStore()
	inner := &embeddingArtifactFakeEmbedder{dimensions: 2, batchResults: [][]float32{{1, 2}}}
	wrapped := newEmbeddingArtifactEmbedder(inner, store, 7, "revision-1")

	first, err := wrapped.Embed(documentEmbeddingContext(), " doc\r\ntext ")
	require.NoError(t, err)
	second, err := wrapped.Embed(documentEmbeddingContext(), "doc\ntext")
	require.NoError(t, err)
	assert.Equal(t, first, second)
	assert.Equal(t, []string{"doc\ntext"}, inner.embedCalls)

	_, err = wrapped.Embed(context.Background(), " doc\r\ntext ")
	require.NoError(t, err)
	assert.Equal(t, []string{"doc\ntext", " doc\r\ntext "}, inner.embedCalls)
}

func TestEmbeddingArtifactBypassesQueryInputs(t *testing.T) {
	store := newEmbeddingArtifactFakeStore()
	inner := &embeddingArtifactFakeEmbedder{dimensions: 2, batchResults: [][]float32{{1, 2}}}
	wrapped := newEmbeddingArtifactEmbedder(inner, store, 7, "revision-1")
	ctx := context.WithValue(documentEmbeddingContext(), types.EmbedQueryContextKey, true)

	_, err := wrapped.Embed(ctx, "query")
	require.NoError(t, err)
	assert.Equal(t, []string{"query"}, inner.embedCalls)
	assert.Zero(t, store.getCalls)
	assert.Zero(t, store.putCalls)
}

func TestEmbeddingArtifactBatchWithPoolDelegatesMissesToInnerWithoutRecursion(t *testing.T) {
	store := newEmbeddingArtifactFakeStore()
	inner := &embeddingArtifactFakeEmbedder{
		dimensions: 2, batchResults: [][]float32{{1, 2}, {3, 4}},
	}
	wrapped := newEmbeddingArtifactEmbedder(inner, store, 7, "revision-1")

	got, err := wrapped.BatchEmbedWithPool(documentEmbeddingContext(), wrapped, []string{" one ", "two"})
	require.NoError(t, err)
	assert.Equal(t, [][]float32{{1, 2}, {3, 4}}, got)
	assert.Equal(t, 1, inner.poolCalls)
	assert.True(t, reflect.ValueOf(inner.poolModel).Pointer() == reflect.ValueOf(inner).Pointer())
	assert.Equal(t, [][]string{{"one", "two"}}, inner.batchCalls)

	_, err = wrapped.BatchEmbedWithPool(context.Background(), wrapped, []string{" query "})
	require.NoError(t, err)
	assert.Equal(t, 2, inner.poolCalls)
	assert.True(t, reflect.ValueOf(inner.poolModel).Pointer() == reflect.ValueOf(inner).Pointer())
}

func TestEmbeddingArtifactPreservesModelMetadataAndNilStoreDelegates(t *testing.T) {
	inner := &embeddingArtifactFakeEmbedder{
		modelID: "model-1", modelName: "text-embedding", dimensions: 2,
		batchResults: [][]float32{{1, 2}},
	}
	wrapped := newEmbeddingArtifactEmbedder(inner, nil, 7, "revision-1")

	assert.Equal(t, inner.modelID, wrapped.GetModelID())
	assert.Equal(t, inner.modelName, wrapped.GetModelName())
	assert.Equal(t, inner.dimensions, wrapped.GetDimensions())
	_, err := wrapped.BatchEmbed(documentEmbeddingContext(), []string{" original\r\n "})
	require.NoError(t, err)
	assert.Equal(t, [][]string{{" original\r\n "}}, inner.batchCalls)
}
