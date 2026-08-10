package retriever

import (
	"context"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/embedding"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

type capturingEmbedder struct {
	embedding.Embedder
	text       string
	batchTexts []string
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

type saveOnlyRepository struct {
	interfaces.RetrieveEngineRepository
}

func (r *saveOnlyRepository) Save(ctx context.Context, indexInfo *types.IndexInfo, params map[string]any) error {
	return nil
}

func (r *saveOnlyRepository) BatchSave(
	ctx context.Context,
	indexInfoList []*types.IndexInfo,
	params map[string]any,
) error {
	return nil
}

type crossDimensionDeleteRepository struct {
	interfaces.RetrieveEngineRepository
	allDimensionCalls int
	configuredCalls   int
	knowledgeIDs      []string
	knowledgeType     string
}

func (r *crossDimensionDeleteRepository) DeleteByKnowledgeIDList(
	context.Context, []string, int, string,
) error {
	r.configuredCalls++
	return nil
}

func (r *crossDimensionDeleteRepository) DeleteByKnowledgeIDListAllDimensions(
	_ context.Context, knowledgeIDs []string, knowledgeType string,
) error {
	r.allDimensionCalls++
	r.knowledgeIDs = append([]string(nil), knowledgeIDs...)
	r.knowledgeType = knowledgeType
	return nil
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

func TestDeleteByKnowledgeIDListUsesCrossDimensionRepositoryCapability(t *testing.T) {
	repo := &crossDimensionDeleteRepository{}
	service := &KeywordsVectorHybridRetrieveEngineService{indexRepository: repo}

	err := service.DeleteByKnowledgeIDList(
		context.Background(), []string{"knowledge-1"}, 2048, "file",
	)
	if err != nil {
		t.Fatalf("DeleteByKnowledgeIDList returned error: %v", err)
	}
	if repo.allDimensionCalls != 1 || repo.configuredCalls != 0 {
		t.Fatalf("all-dimension calls = %d, configured-dimension calls = %d, want 1 and 0",
			repo.allDimensionCalls, repo.configuredCalls)
	}
	if len(repo.knowledgeIDs) != 1 || repo.knowledgeIDs[0] != "knowledge-1" || repo.knowledgeType != "file" {
		t.Fatalf("unexpected delete payload: ids=%v type=%q", repo.knowledgeIDs, repo.knowledgeType)
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
