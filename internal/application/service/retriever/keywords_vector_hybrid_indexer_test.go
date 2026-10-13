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

type capturingBatchRepository struct {
	interfaces.RetrieveEngineRepository
	saved [][]string // per BatchSave call, the source IDs in that batch
}

func (r *capturingBatchRepository) BatchSave(
	ctx context.Context,
	indexInfoList []*types.IndexInfo,
	params map[string]any,
) error {
	ids := make([]string, 0, len(indexInfoList))
	for _, indexInfo := range indexInfoList {
		ids = append(ids, indexInfo.SourceID)
	}
	r.saved = append(r.saved, ids)
	return nil
}

// TestBatchIndexSkipsBlankEmbeddingInputs records that whitespace-only
// chunks are dropped before the embed call: embedding providers reject
// empty inputs or return fewer vectors for them, and one blank chunk
// failed the whole document with an opaque VECTORSTORE_WRITE_FAILED
// (#4199). A chunk that is only an inline image payload scrubs down to
// the "[image]" placeholder text and is NOT blank — it still embeds.
func TestBatchIndexSkipsBlankEmbeddingInputs(t *testing.T) {
	ctx := context.Background()
	embedder := &capturingEmbedder{}
	repo := &capturingBatchRepository{}
	service := &KeywordsVectorHybridRetrieveEngineService{indexRepository: repo}

	imageOnly := `<img src="data:image/png;base64,` + strings.Repeat("A", 300) + `">`
	err := service.BatchIndex(ctx, embedder, []*types.IndexInfo{
		{Content: "alpha", SourceID: "s1"},
		{Content: "  \n\t ", SourceID: "s2"},
		{Content: imageOnly, SourceID: "s3"},
		{Content: "beta", SourceID: "s4"},
	}, []types.RetrieverType{types.VectorRetrieverType})
	if err != nil {
		t.Fatalf("BatchIndex returned error: %v", err)
	}

	if got := strings.Join(embedder.batchTexts, ","); got != "alpha,[image],beta" {
		t.Fatalf("embedding inputs = %q, want [alpha [image] beta]", embedder.batchTexts)
	}
	// Alignment: saved rows must be exactly the embedded ones, in order.
	if len(repo.saved) != 1 {
		t.Fatalf("expected one save batch, got %d", len(repo.saved))
	}
	if got := strings.Join(repo.saved[0], ","); got != "s1,s3,s4" {
		t.Fatalf("saved source IDs = %q, want [s1 s3 s4]", repo.saved[0])
	}
}

func TestBatchIndexAllBlankInputsSucceedsWithNothingIndexed(t *testing.T) {
	ctx := context.Background()
	embedder := &capturingEmbedder{}
	repo := &capturingBatchRepository{}
	service := &KeywordsVectorHybridRetrieveEngineService{indexRepository: repo}

	err := service.BatchIndex(ctx, embedder, []*types.IndexInfo{
		{Content: " ", SourceID: "s1"},
		{Content: "\t", SourceID: "s2"},
	}, []types.RetrieverType{types.VectorRetrieverType})
	if err != nil {
		t.Fatalf("BatchIndex returned error: %v", err)
	}
	if len(embedder.batchTexts) != 0 || len(repo.saved) != 0 {
		t.Fatalf("all-blank input must not embed or save anything")
	}
}

func TestIndexSkipsBlankEmbeddingInput(t *testing.T) {
	ctx := context.Background()
	embedder := &capturingEmbedder{}
	service := &KeywordsVectorHybridRetrieveEngineService{indexRepository: &saveOnlyRepository{}}

	err := service.Index(ctx, embedder, &types.IndexInfo{
		Content:  "   ",
		SourceID: "source-1",
	}, []types.RetrieverType{types.VectorRetrieverType})
	if err != nil {
		t.Fatalf("Index returned error: %v", err)
	}
	if embedder.text != "" {
		t.Fatalf("blank input must not reach the embedding provider, got %q", embedder.text)
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
