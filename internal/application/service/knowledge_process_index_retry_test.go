package service

import (
	"context"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/embedding"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

type indexRetryKnowledgeRepo struct {
	parentChildKnowledgeRepo
	updates []types.Knowledge
}

func (r *indexRetryKnowledgeRepo) UpdateKnowledge(_ context.Context, k *types.Knowledge) error {
	r.updates = append(r.updates, *k)
	return nil
}

type indexRetryChunkRepo struct {
	parentChildChunkService
	deletes int
}

func (s *indexRetryChunkRepo) DeleteChunksByKnowledgeID(context.Context, uint64, string) error {
	s.deletes++
	return nil
}

type failingIndexEngine struct {
	parentChildRetrieveEngine
	err          error
	indexDeletes int
}

func (e *failingIndexEngine) BatchIndex(
	context.Context, embedding.Embedder, []*types.IndexInfo, []types.RetrieverType,
) error {
	return e.err
}

func (e *failingIndexEngine) DeleteByKnowledgeIDList(context.Context, []string, int, string) error {
	e.indexDeletes++
	return nil
}

func runProcessChunksWithFailingIndex(
	ctx context.Context, t *testing.T,
) (*indexRetryKnowledgeRepo, *indexRetryChunkRepo, *failingIndexEngine, error) {
	t.Helper()
	knowledge := &types.Knowledge{
		ID:              "knowledge-1",
		TenantID:        1,
		KnowledgeBaseID: "kb-1",
		ParseStatus:     types.ParseStatusProcessing,
	}
	repo := &indexRetryKnowledgeRepo{parentChildKnowledgeRepo: parentChildKnowledgeRepo{knowledge: knowledge}}
	chunks := &indexRetryChunkRepo{}
	engine := &failingIndexEngine{err: errors.New("embedding endpoint unavailable")}
	tenant := &types.Tenant{
		ID: 1,
		RetrieverEngines: types.RetrieverEngines{Engines: []types.RetrieverEngineParams{{
			RetrieverType:       types.VectorRetrieverType,
			RetrieverEngineType: types.PostgresRetrieverEngineType,
		}}},
	}
	ctx = context.WithValue(ctx, types.TenantInfoContextKey, tenant)
	svc := &knowledgeService{
		repo:           repo,
		chunkRepo:      chunks,
		modelService:   parentChildModelService{embedder: parentChildEmbedder{}},
		retrieveEngine: parentChildRetrieveRegistry{engine: engine},
		graphEngine:    parentChildGraphRepo{},
		tenantRepo:     parentChildTenantRepo{},
		task:           parentChildTaskEnqueuer{},
	}
	kb := &types.KnowledgeBase{
		ID:               "kb-1",
		TenantID:         1,
		EmbeddingModelID: "embedding-1",
		IndexingStrategy: types.IndexingStrategy{VectorEnabled: true},
	}
	err := svc.processChunks(ctx, kb, knowledge, []types.ParsedChunk{
		{Content: "document body", Seq: 0, Start: 0, End: 13, ParentIndex: -1},
	})
	return repo, chunks, engine, err
}

// A transient embedding or vector-store outage must reach the task queue while
// retries remain. Acking it here would mark the document failed after the
// first attempt and leave the configured document-task retries unused.
func TestProcessChunksRetriesBatchIndexFailureBeforeLastAttempt(t *testing.T) {
	ctx := types.WithTaskRetryMetadata(context.Background(), 0, 3)

	repo, chunks, engine, err := runProcessChunksWithFailingIndex(ctx, t)

	require.ErrorContains(t, err, "embedding endpoint unavailable")
	for _, update := range repo.updates {
		require.NotEqual(t, types.ParseStatusFailed, update.ParseStatus)
	}
	// Partial chunks and vectors are still removed; the retry rebuilds them.
	require.Equal(t, 2, chunks.deletes)
	require.Equal(t, 2, engine.indexDeletes)
}

func TestProcessChunksFailsKnowledgeOnLastBatchIndexAttempt(t *testing.T) {
	ctx := types.WithTaskRetryMetadata(context.Background(), 3, 3)

	repo, chunks, engine, err := runProcessChunksWithFailingIndex(ctx, t)

	require.NoError(t, err)
	require.NotEmpty(t, repo.updates)
	last := repo.updates[len(repo.updates)-1]
	require.Equal(t, types.ParseStatusFailed, last.ParseStatus)
	require.Equal(t, "embedding endpoint unavailable", last.ErrorMessage)
	require.Equal(t, 2, chunks.deletes)
	require.Equal(t, 2, engine.indexDeletes)
}
