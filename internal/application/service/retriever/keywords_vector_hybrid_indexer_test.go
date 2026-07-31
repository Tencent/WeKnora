package retriever

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/embedding"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

type capturingEmbedder struct {
	embedding.Embedder
	text       string
	batchTexts []string
	dimensions int
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
	e.batchTexts = append([]string(nil), texts...)
	embeddings := make([][]float32, len(texts))
	for i := range texts {
		embeddings[i] = []float32{1}
	}
	return embeddings, nil
}

func (e *capturingEmbedder) GetDimensions() int {
	return e.dimensions
}

type saveOnlyRepository struct {
	interfaces.RetrieveEngineRepository
	mu          sync.Mutex
	saveParams  map[string]any
	batchParams []map[string]any
}

func (r *saveOnlyRepository) Save(ctx context.Context, indexInfo *types.IndexInfo, params map[string]any) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.saveParams = params
	return nil
}

func (r *saveOnlyRepository) BatchSave(
	ctx context.Context,
	indexInfoList []*types.IndexInfo,
	params map[string]any,
) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.batchParams = append(r.batchParams, params)
	return nil
}

func TestMySQLKeywordOnlyIndexCarriesEmbeddingDimension(t *testing.T) {
	ctx := context.Background()
	embedder := &capturingEmbedder{dimensions: 768}
	repository := &saveOnlyRepository{}
	service := &KeywordsVectorHybridRetrieveEngineService{
		indexRepository: repository,
		engineType:      types.MySQLRetrieverEngineType,
	}

	err := service.Index(ctx, embedder, &types.IndexInfo{
		Content:  "keyword-only content",
		SourceID: "source-1",
	}, []types.RetrieverType{types.KeywordsRetrieverType})
	if err != nil {
		t.Fatalf("Index() error = %v", err)
	}
	if got := repository.saveParams["dimension"]; got != 768 {
		t.Fatalf("keyword-only Index() dimension = %#v, want 768", got)
	}

	err = service.BatchIndex(ctx, embedder, []*types.IndexInfo{{
		Content:  "keyword-only batch content",
		SourceID: "source-2",
	}}, []types.RetrieverType{types.KeywordsRetrieverType})
	if err != nil {
		t.Fatalf("BatchIndex() error = %v", err)
	}
	if len(repository.batchParams) != 1 {
		t.Fatalf("keyword-only BatchIndex() calls = %d, want 1", len(repository.batchParams))
	}
	if got := repository.batchParams[0]["dimension"]; got != 768 {
		t.Fatalf("keyword-only BatchIndex() dimension = %#v, want 768", got)
	}
}

func TestMySQLKeywordOnlyIndexRejectsMissingDimension(t *testing.T) {
	service := &KeywordsVectorHybridRetrieveEngineService{
		indexRepository: &saveOnlyRepository{},
		engineType:      types.MySQLRetrieverEngineType,
	}
	err := service.Index(
		context.Background(),
		&capturingEmbedder{},
		&types.IndexInfo{SourceID: "source-1"},
		[]types.RetrieverType{types.KeywordsRetrieverType},
	)
	if err == nil || !strings.Contains(err.Error(), "positive embedding dimension") {
		t.Fatalf("Index() error = %v, want positive-dimension error", err)
	}
}

func TestMySQLKeywordOnlyBoundedBatchCarriesDimension(t *testing.T) {
	embedder := &capturingEmbedder{dimensions: 1024}
	repository := &saveOnlyRepository{}
	service := &KeywordsVectorHybridRetrieveEngineService{
		indexRepository: repository,
		engineType:      types.MySQLRetrieverEngineType,
	}
	items := make([]*types.IndexInfo, 51)
	for i := range items {
		items[i] = &types.IndexInfo{SourceID: string(rune('a' + i%26))}
	}
	err := service.BatchIndex(
		context.Background(),
		embedder,
		items,
		[]types.RetrieverType{types.KeywordsRetrieverType},
	)
	if err != nil {
		t.Fatalf("BatchIndex() error = %v", err)
	}
	if len(repository.batchParams) != 6 {
		t.Fatalf("bounded BatchIndex() calls = %d, want 6", len(repository.batchParams))
	}
	for i, params := range repository.batchParams {
		if got := params["dimension"]; got != 1024 {
			t.Fatalf("bounded batch %d dimension = %#v, want 1024", i, got)
		}
	}
}

func TestNonMySQLKeywordOnlyBatchDoesNotReceiveDimensionParameter(t *testing.T) {
	repository := &saveOnlyRepository{}
	service := &KeywordsVectorHybridRetrieveEngineService{
		indexRepository: repository,
		engineType:      types.PostgresRetrieverEngineType,
	}
	err := service.BatchIndex(
		context.Background(),
		&capturingEmbedder{},
		[]*types.IndexInfo{{SourceID: "source-1"}},
		[]types.RetrieverType{types.KeywordsRetrieverType},
	)
	if err != nil {
		t.Fatalf("BatchIndex() error = %v", err)
	}
	if len(repository.batchParams) != 1 {
		t.Fatalf("BatchIndex() calls = %d, want 1", len(repository.batchParams))
	}
	if _, ok := repository.batchParams[0]["dimension"]; ok {
		t.Fatalf("non-MySQL keyword batch received dimension: %#v", repository.batchParams[0])
	}
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

func assertImagePayloadRemoved(t *testing.T, content string, payload string) {
	t.Helper()
	if strings.Contains(content, "data:image/png;base64") || strings.Contains(content, payload) {
		t.Fatalf("embedding input still contains inline image payload: %q", content)
	}
	if !strings.Contains(content, "before") || !strings.Contains(content, "after") {
		t.Fatalf("embedding input should preserve surrounding text, got %q", content)
	}
}
