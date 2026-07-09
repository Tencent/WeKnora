package embedding

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

// mockEmbedder is a test double for the Embedder interface.
type mockEmbedder struct {
	modelName string
	dim       int
	modelID   string

	batchCalls int
	batchArgs  []string

	// vectors maps raw input text to the vector that BatchEmbed should return.
	// If a text is absent, BatchEmbed returns a deterministic fallback vector.
	vectors map[string][]float32

	// err, if non-nil, causes BatchEmbed to return this error.
	err error
}

func (m *mockEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	vecs, err := m.BatchEmbed(ctx, []string{text})
	if err != nil {
		return nil, err
	}
	if len(vecs) == 0 {
		return nil, errors.New("mock embed returned no vector")
	}
	return vecs[0], nil
}

func (m *mockEmbedder) BatchEmbed(ctx context.Context, texts []string) ([][]float32, error) {
	m.batchCalls++
	m.batchArgs = append([]string{}, texts...)
	if m.err != nil {
		return nil, m.err
	}

	out := make([][]float32, len(texts))
	for i, t := range texts {
		if v, ok := m.vectors[t]; ok {
			out[i] = v
			continue
		}
		// Deterministic fallback so tests that do not pre-seed every text still
		// receive a stable, dimension-correct vector.
		v := make([]float32, m.dim)
		for j := range v {
			v[j] = float32(i+1)*0.1 + float32(j)*0.01
		}
		out[i] = v
	}
	return out, nil
}

func (m *mockEmbedder) BatchEmbedWithPool(ctx context.Context, model Embedder, texts []string) ([][]float32, error) {
	return m.BatchEmbed(ctx, texts)
}

func (m *mockEmbedder) GetModelName() string { return m.modelName }
func (m *mockEmbedder) GetDimensions() int   { return m.dim }
func (m *mockEmbedder) GetModelID() string   { return m.modelID }

// mockVectorCacheStore is a test double for VectorCacheStore.
type mockVectorCacheStore struct {
	entries map[string][]float32 // text_hash -> vector

	getCalls       int
	getArgsHashes  []string
	getArgsModelID string
	getArgsDim     int

	putCalls int
	putRows  []types.EmbeddingCache

	getErr error
	putErr error
}

func (m *mockVectorCacheStore) GetBatch(ctx context.Context, textHashes []string, modelID string, dim int) (map[string][]float32, error) {
	m.getCalls++
	m.getArgsHashes = append([]string{}, textHashes...)
	m.getArgsModelID = modelID
	m.getArgsDim = dim
	if m.getErr != nil {
		return nil, m.getErr
	}

	hits := make(map[string][]float32, len(textHashes))
	for _, h := range textHashes {
		if v, ok := m.entries[h]; ok {
			hits[h] = v
		}
	}
	return hits, nil
}

func (m *mockVectorCacheStore) Put(ctx context.Context, rows []types.EmbeddingCache) error {
	m.putCalls++
	m.putRows = append(m.putRows, rows...)
	if m.putErr != nil {
		return m.putErr
	}
	if m.entries == nil {
		m.entries = make(map[string][]float32)
	}
	for _, r := range rows {
		var vec []float32
		if err := json.Unmarshal([]byte(r.Vector), &vec); err != nil {
			continue
		}
		m.entries[r.TextHash] = vec
	}
	return nil
}

func floatsEqual(a, b []float32) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestCachedEmbedder_NilStorePassthrough(t *testing.T) {
	t.Parallel()

	inner := &mockEmbedder{modelID: "m", dim: 4}
	if got := NewCachedEmbedder(inner, nil); got != inner {
		t.Fatalf("store=nil: expected inner itself, got %T", got)
	}

	if got := NewCachedEmbedder(nil, &mockVectorCacheStore{}); got != nil {
		t.Fatalf("inner=nil: expected nil, got %T", got)
	}

	if got := NewCachedEmbedder(nil, nil); got != nil {
		t.Fatalf("both nil: expected nil, got %T", got)
	}
}

func TestCachedEmbedder_FullCacheHit_NoInnerCall(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	texts := []string{"hello world", "foo bar"}
	vec1 := []float32{0.1, 0.2, 0.3, 0.4}
	vec2 := []float32{0.5, 0.6, 0.7, 0.8}

	store := &mockVectorCacheStore{
		entries: map[string][]float32{
			types.StableContentHash(texts[0]): vec1,
			types.StableContentHash(texts[1]): vec2,
		},
	}
	inner := &mockEmbedder{modelID: "test-model", dim: 4}
	cached := NewCachedEmbedder(inner, store).(*cachedEmbedder)

	out, err := cached.BatchEmbed(ctx, texts)
	if err != nil {
		t.Fatalf("BatchEmbed error: %v", err)
	}
	if inner.batchCalls != 0 {
		t.Fatalf("expected 0 inner calls, got %d", inner.batchCalls)
	}
	if len(out) != len(texts) {
		t.Fatalf("expected %d vectors, got %d", len(texts), len(out))
	}
	if !floatsEqual(out[0], vec1) {
		t.Fatalf("first vector mismatch: got %v want %v", out[0], vec1)
	}
	if !floatsEqual(out[1], vec2) {
		t.Fatalf("second vector mismatch: got %v want %v", out[1], vec2)
	}
}

func TestCachedEmbedder_FullMiss_CallsInnerAndPuts(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	texts := []string{"alpha", "beta"}
	vec1 := []float32{0.1, 0.2, 0.3, 0.4}
	vec2 := []float32{0.5, 0.6, 0.7, 0.8}

	inner := &mockEmbedder{
		modelID: "test-model",
		dim:     4,
		vectors: map[string][]float32{
			texts[0]: vec1,
			texts[1]: vec2,
		},
	}
	store := &mockVectorCacheStore{entries: map[string][]float32{}}
	cached := NewCachedEmbedder(inner, store).(*cachedEmbedder)

	out, err := cached.BatchEmbed(ctx, texts)
	if err != nil {
		t.Fatalf("BatchEmbed error: %v", err)
	}

	if inner.batchCalls != 1 {
		t.Fatalf("expected 1 inner call, got %d", inner.batchCalls)
	}
	if len(inner.batchArgs) != len(texts) {
		t.Fatalf("expected inner called with %d texts, got %d", len(texts), len(inner.batchArgs))
	}
	for i, want := range texts {
		if inner.batchArgs[i] != want {
			t.Fatalf("inner arg[%d]: got %q want %q", i, inner.batchArgs[i], want)
		}
	}

	if store.putCalls != 1 {
		t.Fatalf("expected 1 Put call, got %d", store.putCalls)
	}
	if len(store.putRows) != len(texts) {
		t.Fatalf("expected %d Put rows, got %d", len(texts), len(store.putRows))
	}

	wantHashes := map[string][]float32{
		types.StableContentHash(texts[0]): vec1,
		types.StableContentHash(texts[1]): vec2,
	}
	for i, row := range store.putRows {
		wantVec := wantHashes[row.TextHash]
		if wantVec == nil {
			t.Fatalf("unexpected TextHash in Put row: %s", row.TextHash)
		}
		if row.ModelID != inner.modelID {
			t.Fatalf("Put row ModelID: got %q want %q", row.ModelID, inner.modelID)
		}
		if row.Dimension != inner.dim {
			t.Fatalf("Put row Dimension: got %d want %d", row.Dimension, inner.dim)
		}
		var gotVec []float32
		if err := json.Unmarshal([]byte(row.Vector), &gotVec); err != nil {
			t.Fatalf("unmarshal Put vector: %v", err)
		}
		if !floatsEqual(gotVec, wantVec) {
			t.Fatalf("Put vector mismatch for %s: got %v want %v", row.TextHash, gotVec, wantVec)
		}
		_ = i
	}

	if !floatsEqual(out[0], vec1) || !floatsEqual(out[1], vec2) {
		t.Fatalf("output vectors mismatch: got %v %v", out[0], out[1])
	}
}

func TestCachedEmbedder_PartialHit_OnlyMissesToInner(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	texts := []string{"cached text", "miss text"}
	cachedVec := []float32{1.0, 2.0, 3.0, 4.0}
	missVec := []float32{5.0, 6.0, 7.0, 8.0}

	store := &mockVectorCacheStore{
		entries: map[string][]float32{
			types.StableContentHash(texts[0]): cachedVec,
		},
	}
	inner := &mockEmbedder{
		modelID: "test-model",
		dim:     4,
		vectors: map[string][]float32{
			texts[1]: missVec,
		},
	}
	cached := NewCachedEmbedder(inner, store).(*cachedEmbedder)

	out, err := cached.BatchEmbed(ctx, texts)
	if err != nil {
		t.Fatalf("BatchEmbed error: %v", err)
	}

	if inner.batchCalls != 1 {
		t.Fatalf("expected 1 inner call, got %d", inner.batchCalls)
	}
	if len(inner.batchArgs) != 1 || inner.batchArgs[0] != texts[1] {
		t.Fatalf("expected inner called with only miss text %q, got %v", texts[1], inner.batchArgs)
	}

	if !floatsEqual(out[0], cachedVec) {
		t.Fatalf("hit vector mismatch: got %v want %v", out[0], cachedVec)
	}
	if !floatsEqual(out[1], missVec) {
		t.Fatalf("miss vector mismatch: got %v want %v", out[1], missVec)
	}
}

func TestCachedEmbedder_SecondCallHitsAfterPut(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	texts := []string{"first", "second"}
	vec1 := []float32{0.1, 0.2, 0.3, 0.4}
	vec2 := []float32{0.5, 0.6, 0.7, 0.8}

	inner := &mockEmbedder{
		modelID: "test-model",
		dim:     4,
		vectors: map[string][]float32{
			texts[0]: vec1,
			texts[1]: vec2,
		},
	}
	store := &mockVectorCacheStore{entries: map[string][]float32{}}
	cached := NewCachedEmbedder(inner, store).(*cachedEmbedder)

	if _, err := cached.BatchEmbed(ctx, texts); err != nil {
		t.Fatalf("first BatchEmbed error: %v", err)
	}
	if inner.batchCalls != 1 || store.putCalls != 1 {
		t.Fatalf("first call: expected 1 inner call and 1 Put, got %d inner, %d put", inner.batchCalls, store.putCalls)
	}

	out2, err := cached.BatchEmbed(ctx, texts)
	if err != nil {
		t.Fatalf("second BatchEmbed error: %v", err)
	}
	if inner.batchCalls != 1 {
		t.Fatalf("second call: expected inner still called once, got %d", inner.batchCalls)
	}
	if store.putCalls != 1 {
		t.Fatalf("second call: expected Put still called once, got %d", store.putCalls)
	}
	if !floatsEqual(out2[0], vec1) || !floatsEqual(out2[1], vec2) {
		t.Fatalf("second call output mismatch: got %v %v", out2[0], out2[1])
	}
}

func TestCachedEmbedder_DuplicateTextInBatch_SameKey(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	text := "duplicate"
	vec := []float32{0.9, 0.8, 0.7, 0.6}

	inner := &mockEmbedder{
		modelID: "test-model",
		dim:     4,
		vectors: map[string][]float32{text: vec},
	}
	store := &mockVectorCacheStore{entries: map[string][]float32{}}
	cached := NewCachedEmbedder(inner, store).(*cachedEmbedder)

	out, err := cached.BatchEmbed(ctx, []string{text, text})
	if err != nil {
		t.Fatalf("BatchEmbed error: %v", err)
	}

	if inner.batchCalls != 1 {
		t.Fatalf("expected 1 inner call for duplicate key, got %d", inner.batchCalls)
	}
	if len(inner.batchArgs) != 1 || inner.batchArgs[0] != text {
		t.Fatalf("expected inner called with 1 unique text %q, got %v", text, inner.batchArgs)
	}

	if len(out) != 2 {
		t.Fatalf("expected 2 output vectors, got %d", len(out))
	}
	if !floatsEqual(out[0], vec) {
		t.Fatalf("first duplicate vector mismatch: got %v want %v", out[0], vec)
	}
	if !floatsEqual(out[1], vec) {
		t.Fatalf("second duplicate vector mismatch: got %v want %v", out[1], vec)
	}
}

func TestCachedEmbedder_PutFailureDoesNotFailEmbed(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	text := "hello"
	vec := []float32{0.1, 0.2, 0.3, 0.4}

	inner := &mockEmbedder{
		modelID: "test-model",
		dim:     4,
		vectors: map[string][]float32{text: vec},
	}
	store := &mockVectorCacheStore{
		entries: map[string][]float32{},
		putErr:  errors.New("put failed"),
	}
	cached := NewCachedEmbedder(inner, store).(*cachedEmbedder)

	out, err := cached.BatchEmbed(ctx, []string{text})
	if err != nil {
		t.Fatalf("BatchEmbed should not fail when Put fails: %v", err)
	}
	if store.putCalls != 1 {
		t.Fatalf("expected 1 Put call, got %d", store.putCalls)
	}
	if len(out) != 1 || !floatsEqual(out[0], vec) {
		t.Fatalf("expected vector %v, got %v", vec, out)
	}
}

func TestCachedEmbedder_GetBatchFailure_FallsBackToInner(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	text := "hello"
	vec := []float32{0.1, 0.2, 0.3, 0.4}

	inner := &mockEmbedder{
		modelID: "test-model",
		dim:     4,
		vectors: map[string][]float32{text: vec},
	}
	store := &mockVectorCacheStore{
		entries: map[string][]float32{},
		getErr:  errors.New("get failed"),
	}
	cached := NewCachedEmbedder(inner, store).(*cachedEmbedder)

	out, err := cached.BatchEmbed(ctx, []string{text})
	if err != nil {
		t.Fatalf("BatchEmbed should not fail when GetBatch fails: %v", err)
	}
	if inner.batchCalls != 1 {
		t.Fatalf("expected 1 inner fallback call, got %d", inner.batchCalls)
	}
	if len(out) != 1 || !floatsEqual(out[0], vec) {
		t.Fatalf("expected vector %v, got %v", vec, out)
	}
}

func TestCachedEmbedder_EmptyBatch(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	inner := &mockEmbedder{modelID: "test-model", dim: 4}
	store := &mockVectorCacheStore{}
	cached := NewCachedEmbedder(inner, store).(*cachedEmbedder)

	out, err := cached.BatchEmbed(ctx, []string{})
	if err != nil {
		t.Fatalf("empty batch should not error: %v", err)
	}
	if len(out) != 0 {
		t.Fatalf("expected empty output, got %d vectors", len(out))
	}
	if inner.batchCalls != 0 {
		t.Fatalf("expected 0 inner calls, got %d", inner.batchCalls)
	}
	if store.getCalls != 0 {
		t.Fatalf("expected 0 GetBatch calls, got %d", store.getCalls)
	}
	if store.putCalls != 0 {
		t.Fatalf("expected 0 Put calls, got %d", store.putCalls)
	}
}

func TestCachedEmbedder_EmbedDelegatesToBatchEmbed(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	text := "hello"
	vec := []float32{0.1, 0.2, 0.3, 0.4}

	inner := &mockEmbedder{
		modelID: "test-model",
		dim:     4,
		vectors: map[string][]float32{text: vec},
	}
	store := &mockVectorCacheStore{}
	cached := NewCachedEmbedder(inner, store).(*cachedEmbedder)

	out, err := cached.Embed(ctx, text)
	if err != nil {
		t.Fatalf("Embed error: %v", err)
	}
	if !floatsEqual(out, vec) {
		t.Fatalf("Embed vector mismatch: got %v want %v", out, vec)
	}
	if inner.batchCalls != 1 {
		t.Fatalf("expected 1 BatchEmbed call, got %d", inner.batchCalls)
	}
	if len(inner.batchArgs) != 1 || inner.batchArgs[0] != text {
		t.Fatalf("expected BatchEmbed called with %q, got %v", text, inner.batchArgs)
	}

	// Error propagation from BatchEmbed to Embed.
	wantErr := errors.New("embed failed")
	inner2 := &mockEmbedder{modelID: "test-model", dim: 4, err: wantErr}
	store2 := &mockVectorCacheStore{}
	cached2 := NewCachedEmbedder(inner2, store2).(*cachedEmbedder)
	if _, err := cached2.Embed(ctx, text); err != wantErr {
		t.Fatalf("expected error %v, got %v", wantErr, err)
	}
}
