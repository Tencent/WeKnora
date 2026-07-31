package retriever

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/Tencent/WeKnora/internal/artifact"
	"github.com/Tencent/WeKnora/internal/models/embedding"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

type capturingEmbedder struct {
	embedding.Embedder
	text       string
	batchTexts []string
	batchCalls int
	modelID    string
	modelName  string
}

func (e *capturingEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	e.text = text
	return []float32{1}, nil
}

func (e *capturingEmbedder) BatchEmbedWithPool(
	ctx context.Context,
	model embedding.Embedder,
	texts []string,
) ([][]float32, error) {
	e.batchCalls++
	e.batchTexts = append([]string(nil), texts...)
	embeddings := make([][]float32, len(texts))
	for i := range texts {
		embeddings[i] = []float32{1}
	}
	return embeddings, nil
}

func (e *capturingEmbedder) GetModelName() string {
	if e.modelName != "" {
		return e.modelName
	}
	return "test-embedding-model"
}

func (e *capturingEmbedder) GetModelID() string {
	if e.modelID != "" {
		return e.modelID
	}
	return "model-1"
}

func (e *capturingEmbedder) GetDimensions() int {
	return 1
}

type saveOnlyRepository struct {
	interfaces.RetrieveEngineRepository
	mu     sync.Mutex
	saves  []map[string][]float32
	embeds []map[string][]float32
}

func (r *saveOnlyRepository) Save(ctx context.Context, indexInfo *types.IndexInfo, params map[string]any) error {
	if embeddingMap, ok := params["embedding"].(map[string][]float32); ok {
		r.mu.Lock()
		defer r.mu.Unlock()
		copyMap := make(map[string][]float32, len(embeddingMap))
		for key, value := range embeddingMap {
			copyMap[key] = append([]float32(nil), value...)
		}
		r.saves = append(r.saves, copyMap)
	}
	return nil
}

func (r *saveOnlyRepository) BatchSave(
	ctx context.Context,
	indexInfoList []*types.IndexInfo,
	params map[string]any,
) error {
	if embeddingMap, ok := params["embedding"].(map[string][]float32); ok {
		r.mu.Lock()
		defer r.mu.Unlock()
		copyMap := make(map[string][]float32, len(embeddingMap))
		for key, value := range embeddingMap {
			copyMap[key] = append([]float32(nil), value...)
		}
		r.embeds = append(r.embeds, copyMap)
	}
	return nil
}

type generationFilterRepository struct {
	saveOnlyRepository
	supports bool
}

func (r *generationFilterRepository) SupportsGenerationFilter() bool {
	return r.supports
}

type testArtifactStore struct {
	mu    sync.Mutex
	items map[string]*artifact.Record
}

func newTestArtifactStore() *testArtifactStore {
	return &testArtifactStore{items: map[string]*artifact.Record{}}
}

func testArtifactStoreKey(tenantID uint64, stage string, keyVersion int, artifactKey string) string {
	return fmt.Sprintf("%d:%s:%d:%s", tenantID, stage, keyVersion, artifactKey)
}

func (s *testArtifactStore) PutIfAbsent(_ context.Context, record *artifact.Record) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := testArtifactStoreKey(record.TenantID, record.Stage, record.KeyVersion, record.ArtifactKey)
	if _, ok := s.items[key]; ok {
		return false, nil
	}
	copy := *record
	s.items[key] = &copy
	return true, nil
}

func (s *testArtifactStore) Get(
	_ context.Context, tenantID uint64, stage string, keyVersion int, artifactKey string,
) (*artifact.Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.items[testArtifactStoreKey(tenantID, stage, keyVersion, artifactKey)]
	if !ok {
		return nil, artifact.ErrCacheMiss
	}
	copy := *record
	return &copy, nil
}

func (s *testArtifactStore) DeleteObservedChecksum(
	_ context.Context, tenantID uint64, id string, payloadChecksum string,
) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for key, record := range s.items {
		if record.TenantID == tenantID && record.ID == id && record.PayloadChecksum == payloadChecksum {
			delete(s.items, key)
			return true, nil
		}
	}
	return false, nil
}

func TestIndexRemovesInlineImagePayloadBeforeEmbedding(t *testing.T) {
	ctx := context.Background()
	embedder := &capturingEmbedder{}
	service := &KeywordsVectorHybridRetrieveEngineService{indexRepository: &saveOnlyRepository{}}
	payload := strings.Repeat("A", 300)
	content := "before <img src=\"data:image/png;base64," + payload + "\"> after"

	err := service.Index(ctx, embedder, &types.IndexInfo{
		Content:  content,
		SourceID: "source-1",
	}, []types.RetrieverType{types.VectorRetrieverType})
	if err != nil {
		t.Fatalf("Index returned error: %v", err)
	}
	assertImagePayloadRemoved(t, embedder.text, payload)
}

func TestBatchIndexRemovesInlineImagePayloadBeforeEmbedding(t *testing.T) {
	ctx := context.Background()
	embedder := &capturingEmbedder{}
	service := &KeywordsVectorHybridRetrieveEngineService{indexRepository: &saveOnlyRepository{}}
	payload := strings.Repeat("A", 300)
	content := "before ![chart](data:image/png;base64," + payload + ") after"

	err := service.BatchIndex(ctx, embedder, []*types.IndexInfo{{
		Content:  content,
		SourceID: "source-1",
	}}, []types.RetrieverType{types.VectorRetrieverType})
	if err != nil {
		t.Fatalf("BatchIndex returned error: %v", err)
	}
	if len(embedder.batchTexts) != 1 {
		t.Fatalf("expected one embedding input, got %d", len(embedder.batchTexts))
	}
	assertImagePayloadRemoved(t, embedder.batchTexts[0], payload)
}

func TestBatchIndexTruncatesOversizedEmbeddingInput(t *testing.T) {
	ctx := context.Background()
	embedder := &capturingEmbedder{}
	service := &KeywordsVectorHybridRetrieveEngineService{indexRepository: &saveOnlyRepository{}}

	err := service.BatchIndex(ctx, embedder, []*types.IndexInfo{{
		Content:  strings.Repeat("x", safetyMaxChars+10),
		SourceID: "source-1",
	}}, []types.RetrieverType{types.VectorRetrieverType})
	if err != nil {
		t.Fatalf("BatchIndex returned error: %v", err)
	}
	if len(embedder.batchTexts) != 1 {
		t.Fatalf("expected one embedding input, got %d", len(embedder.batchTexts))
	}
	if got := len([]rune(embedder.batchTexts[0])); got > safetyMaxChars {
		t.Fatalf("embedding input length = %d, want <= %d", got, safetyMaxChars)
	}
}

func TestBatchIndexUsesArtifactCacheForRepeatedEmbeddingInputs(t *testing.T) {
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7))
	embedder := &capturingEmbedder{}
	repo := &saveOnlyRepository{}
	runtime := artifact.NewRuntime(newTestArtifactStore(), artifact.RuntimeOptions{
		ReadEnabled: true, WriteEnabled: true, MaxInlineBytes: 1024,
	})
	service := NewKVHybridRetrieveEngine(
		repo,
		types.SQLiteRetrieverEngineType,
		WithArtifactRuntime(runtime),
	)
	items := []*types.IndexInfo{
		{Content: "same", SourceID: "source-1"},
		{Content: "same", SourceID: "source-2"},
	}

	if err := service.BatchIndex(ctx, embedder, items, []types.RetrieverType{types.VectorRetrieverType}); err != nil {
		t.Fatalf("first BatchIndex returned error: %v", err)
	}
	if err := service.BatchIndex(ctx, embedder, items, []types.RetrieverType{types.VectorRetrieverType}); err != nil {
		t.Fatalf("second BatchIndex returned error: %v", err)
	}

	if len(embedder.batchTexts) != 1 {
		t.Fatalf("provider inputs after repeated calls = %d, want 1", len(embedder.batchTexts))
	}
	if len(repo.embeds) != 2 {
		t.Fatalf("BatchSave calls = %d, want 2", len(repo.embeds))
	}
}

func TestKVHybridRetrieveEnginePropagatesGenerationFilterCapability(t *testing.T) {
	supported := NewKVHybridRetrieveEngine(
		&generationFilterRepository{supports: true},
		types.SQLiteRetrieverEngineType,
	)
	unsupported := NewKVHybridRetrieveEngine(
		&generationFilterRepository{supports: false},
		types.SQLiteRetrieverEngineType,
	)
	unspecified := NewKVHybridRetrieveEngine(
		&saveOnlyRepository{},
		types.SQLiteRetrieverEngineType,
	)

	if !supported.(interfaces.GenerationFilterCapability).SupportsGenerationFilter() {
		t.Fatal("expected wrapper to report backend generation filter support")
	}
	if unsupported.(interfaces.GenerationFilterCapability).SupportsGenerationFilter() {
		t.Fatal("expected wrapper to report backend generation filter unsupported")
	}
	if unspecified.(interfaces.GenerationFilterCapability).SupportsGenerationFilter() {
		t.Fatal("expected wrapper without backend capability to report unsupported")
	}
}

func TestBatchIndexArtifactCacheComputesOnlyMisses(t *testing.T) {
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7))
	embedder := &capturingEmbedder{}
	runtime := artifact.NewRuntime(newTestArtifactStore(), artifact.RuntimeOptions{
		ReadEnabled: true, WriteEnabled: true, MaxInlineBytes: 1024,
	})
	service := NewKVHybridRetrieveEngine(
		&saveOnlyRepository{},
		types.SQLiteRetrieverEngineType,
		WithArtifactRuntime(runtime),
	)
	cached := []*types.IndexInfo{{Content: "cached", SourceID: "source-cached"}}
	mixed := []*types.IndexInfo{
		{Content: "cached", SourceID: "source-cached-2"},
		{Content: "new", SourceID: "source-new"},
	}

	if err := service.BatchIndex(ctx, embedder, cached, []types.RetrieverType{types.VectorRetrieverType}); err != nil {
		t.Fatalf("priming BatchIndex returned error: %v", err)
	}
	embedder.batchTexts = nil
	if err := service.BatchIndex(ctx, embedder, mixed, []types.RetrieverType{types.VectorRetrieverType}); err != nil {
		t.Fatalf("mixed BatchIndex returned error: %v", err)
	}

	if len(embedder.batchTexts) != 1 || embedder.batchTexts[0] != "new" {
		t.Fatalf("provider inputs = %v, want only [new]", embedder.batchTexts)
	}
}

func TestBatchIndexArtifactCacheInvalidatesOnEmbeddingModelIdentity(t *testing.T) {
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7))
	modelOne := &capturingEmbedder{modelID: "model-1", modelName: "embedding-one"}
	modelTwo := &capturingEmbedder{modelID: "model-2", modelName: "embedding-two"}
	runtime := artifact.NewRuntime(newTestArtifactStore(), artifact.RuntimeOptions{
		ReadEnabled: true, WriteEnabled: true, MaxInlineBytes: 1024,
	})
	service := NewKVHybridRetrieveEngine(
		&saveOnlyRepository{},
		types.SQLiteRetrieverEngineType,
		WithArtifactRuntime(runtime),
	)
	items := []*types.IndexInfo{{Content: "same", SourceID: "source-1"}}

	if err := service.BatchIndex(ctx, modelOne, items, []types.RetrieverType{types.VectorRetrieverType}); err != nil {
		t.Fatalf("first BatchIndex returned error: %v", err)
	}
	if err := service.BatchIndex(ctx, modelOne, items, []types.RetrieverType{types.VectorRetrieverType}); err != nil {
		t.Fatalf("second BatchIndex returned error: %v", err)
	}
	if err := service.BatchIndex(ctx, modelTwo, items, []types.RetrieverType{types.VectorRetrieverType}); err != nil {
		t.Fatalf("model-changed BatchIndex returned error: %v", err)
	}

	if modelOne.batchCalls != 1 {
		t.Fatalf("model one provider calls = %d, want 1", modelOne.batchCalls)
	}
	if modelTwo.batchCalls != 1 || len(modelTwo.batchTexts) != 1 || modelTwo.batchTexts[0] != "same" {
		t.Fatalf("model two provider calls=%d inputs=%v, want one miss for [same]", modelTwo.batchCalls, modelTwo.batchTexts)
	}
}

func TestIndexUsesArtifactCacheForRepeatedEmbeddingInput(t *testing.T) {
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7))
	embedder := &capturingEmbedder{}
	repo := &saveOnlyRepository{}
	runtime := artifact.NewRuntime(newTestArtifactStore(), artifact.RuntimeOptions{
		ReadEnabled: true, WriteEnabled: true, MaxInlineBytes: 1024,
	})
	service := NewKVHybridRetrieveEngine(
		repo,
		types.SQLiteRetrieverEngineType,
		WithArtifactRuntime(runtime),
	)

	if err := service.Index(ctx, embedder, &types.IndexInfo{
		Content: "same", SourceID: "source-1",
	}, []types.RetrieverType{types.VectorRetrieverType}); err != nil {
		t.Fatalf("first Index returned error: %v", err)
	}
	if err := service.Index(ctx, embedder, &types.IndexInfo{
		Content: "same", SourceID: "source-2",
	}, []types.RetrieverType{types.VectorRetrieverType}); err != nil {
		t.Fatalf("second Index returned error: %v", err)
	}

	if embedder.text != "" {
		t.Fatalf("Embed should not be called when artifact runtime is available")
	}
	if len(embedder.batchTexts) != 1 || embedder.batchTexts[0] != "same" {
		t.Fatalf("provider batch inputs = %v, want one [same]", embedder.batchTexts)
	}
	if len(repo.saves) != 2 {
		t.Fatalf("Save calls = %d, want 2", len(repo.saves))
	}
}

func assertImagePayloadRemoved(t *testing.T, content string, payload string) {
	t.Helper()
	if strings.Contains(content, "data:image/png;base64") || strings.Contains(content, payload) {
		t.Fatalf("embedding input still contains inline image payload: %q", content)
	}
	if !strings.Contains(content, "before") || !strings.Contains(content, "after") {
		t.Fatalf("embedding input should preserve surrounding text, got %q", content)
	}
}
