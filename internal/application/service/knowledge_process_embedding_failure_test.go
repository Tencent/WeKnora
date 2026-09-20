package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/embedding"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

type embedFailureKnowledgeRepo struct {
	interfaces.KnowledgeRepository
	knowledge *types.Knowledge
	updates   []types.Knowledge
}

func (r *embedFailureKnowledgeRepo) GetKnowledgeByID(
	context.Context, uint64, string,
) (*types.Knowledge, error) {
	return r.knowledge, nil
}

func (r *embedFailureKnowledgeRepo) UpdateKnowledge(_ context.Context, k *types.Knowledge) error {
	r.updates = append(r.updates, *k)
	return nil
}

type embedFailureModelService struct {
	interfaces.ModelService
	err error
}

func (s embedFailureModelService) GetEmbeddingModel(
	context.Context, string,
) (embedding.Embedder, error) {
	return nil, s.err
}

// A KB that indexes vectors cannot proceed without an embedder, and
// processChunks reports nothing to its callers — so this failure has to be
// persisted here or the row stays "processing" with no task left to move it,
// visible to the user only as an hour-long spinner followed by a generic
// housekeeping failure.
//
// chunkRepo / graphEngine / retrieveEngine are deliberately left nil: any
// attempt to keep processing past the missing embedder would panic and fail
// this test, which is exactly the regression worth catching.
func TestProcessChunksFailsKnowledgeWhenEmbeddingModelUnavailable(t *testing.T) {
	modelErr := errors.New("embedding model not found")
	knowledge := &types.Knowledge{
		ID:              "knowledge-1",
		TenantID:        1,
		KnowledgeBaseID: "kb-1",
		ParseStatus:     types.ParseStatusProcessing,
	}
	repo := &embedFailureKnowledgeRepo{knowledge: knowledge}
	svc := &knowledgeService{
		repo:         repo,
		modelService: embedFailureModelService{err: modelErr},
	}
	kb := &types.KnowledgeBase{
		ID:               "kb-1",
		TenantID:         1,
		EmbeddingModelID: "embedding-1",
		IndexingStrategy: types.IndexingStrategy{VectorEnabled: true},
	}

	svc.processChunks(context.Background(), kb, knowledge,
		[]types.ParsedChunk{{Content: "body", Seq: 0, Start: 0, End: 4}})

	require.Len(t, repo.updates, 1, "the failure must be persisted exactly once")
	require.Equal(t, types.ParseStatusFailed, repo.updates[0].ParseStatus)
	require.True(t,
		strings.Contains(repo.updates[0].ErrorMessage, modelErr.Error()),
		"error_message should name the underlying cause, got %q", repo.updates[0].ErrorMessage,
	)
	require.Equal(t, types.ParseStatusFailed, knowledge.ParseStatus)
}

// A KB with vector and keyword indexing both off never resolves an embedder,
// so the guard above must not fire for it.
func TestProcessChunksSkipsEmbeddingModelWhenIndexingDisabled(t *testing.T) {
	knowledge := &types.Knowledge{
		ID:              "knowledge-1",
		TenantID:        1,
		KnowledgeBaseID: "kb-1",
		ParseStatus:     types.ParseStatusProcessing,
	}
	chunkRepo := &parentChildChunkService{}
	tenant := &types.Tenant{ID: 1}
	ctx := context.WithValue(context.Background(), types.TenantInfoContextKey, tenant)
	svc := &knowledgeService{
		repo:         &embedFailureKnowledgeRepo{knowledge: knowledge},
		chunkRepo:    chunkRepo,
		modelService: embedFailureModelService{err: errors.New("must not be called")},
		graphEngine:  parentChildGraphRepo{},
		tenantRepo:   parentChildTenantRepo{},
		task:         parentChildTaskEnqueuer{},
	}
	kb := &types.KnowledgeBase{ID: "kb-1", TenantID: 1}
	require.False(t, kb.NeedsEmbeddingModel())

	svc.processChunks(ctx, kb, knowledge,
		[]types.ParsedChunk{{Content: "body", Seq: 0, Start: 0, End: 4}})

	require.Len(t, chunkRepo.created, 1, "chunks are persisted even without vector indexing")
	require.NotEqual(t, types.ParseStatusFailed, knowledge.ParseStatus)
}
