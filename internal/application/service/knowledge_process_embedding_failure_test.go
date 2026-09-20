package service

import (
	"context"
	"errors"
	"fmt"
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
	// stored is what the database starts returning once storedAfterReads
	// reads have been served. The delay matters: processChunks already
	// guards its entry, so a test that returns the cancelled / replaced row
	// from the very first read never reaches the code under test. Delaying
	// it models the real window — resolving an embedding model reads the
	// database, so a cancel or a replacement can land while it runs.
	stored           *types.Knowledge
	storedAfterReads int
	reads            int
	updates          []types.Knowledge
}

func (r *embedFailureKnowledgeRepo) GetKnowledgeByID(
	context.Context, uint64, string,
) (*types.Knowledge, error) {
	r.reads++
	if r.stored != nil && r.reads > r.storedAfterReads {
		return r.stored, nil
	}
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

// A cancelled context means the run was interrupted, not that the model is
// unusable — the user cancelled (asynq CancelProcessing cancels the handler
// context), the worker was preempted, or the process is shutting down.
// Recording a model failure here would both mislabel the cause and overwrite
// the cancelled status the abort path just wrote.
func TestProcessChunksLeavesStatusAloneWhenEmbeddingModelCallIsCancelled(t *testing.T) {
	for _, tc := range []struct {
		name      string
		modelErr  error
		cancelCtx bool
	}{
		{name: "error is context.Canceled", modelErr: context.Canceled},
		{name: "error wraps context.DeadlineExceeded", modelErr: fmt.Errorf("query: %w", context.DeadlineExceeded)},
		{name: "context already cancelled", modelErr: errors.New("driver: bad connection"), cancelCtx: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			knowledge := &types.Knowledge{
				ID:              "knowledge-1",
				TenantID:        1,
				KnowledgeBaseID: "kb-1",
				ParseStatus:     types.ParseStatusProcessing,
			}
			repo := &embedFailureKnowledgeRepo{knowledge: knowledge}
			svc := &knowledgeService{
				repo:         repo,
				modelService: embedFailureModelService{err: tc.modelErr},
			}
			kb := &types.KnowledgeBase{
				ID:               "kb-1",
				TenantID:         1,
				EmbeddingModelID: "embedding-1",
				IndexingStrategy: types.IndexingStrategy{VectorEnabled: true},
			}
			ctx := context.Background()
			if tc.cancelCtx {
				cancelled, cancel := context.WithCancel(ctx)
				cancel()
				ctx = cancelled
			}

			svc.processChunks(ctx, kb, knowledge,
				[]types.ParsedChunk{{Content: "body", Seq: 0, Start: 0, End: 4}})

			require.Empty(t, repo.updates, "an interrupted run must not be written as a failure")
			require.Equal(t, types.ParseStatusProcessing, knowledge.ParseStatus)
		})
	}
}

// The row was cancelled after the entry guard ran. Resolving a model reads the
// database, so that window is real — and the cancelled status must survive it.
func TestProcessChunksDoesNotOverwriteCancelledRowOnEmbeddingFailure(t *testing.T) {
	knowledge := &types.Knowledge{
		ID:              "knowledge-1",
		TenantID:        1,
		KnowledgeBaseID: "kb-1",
		ParseStatus:     types.ParseStatusProcessing,
	}
	repo := &embedFailureKnowledgeRepo{
		knowledge: knowledge,
		// The entry guard's read still sees a live row; the cancel lands
		// while the embedding model is being resolved.
		storedAfterReads: 1,
		stored: &types.Knowledge{
			ID:          "knowledge-1",
			TenantID:    1,
			ParseStatus: types.ParseStatusCancelled,
		},
	}
	svc := &knowledgeService{
		repo:         repo,
		modelService: embedFailureModelService{err: errors.New("embedding model not found")},
	}
	kb := &types.KnowledgeBase{
		ID:               "kb-1",
		TenantID:         1,
		EmbeddingModelID: "embedding-1",
		IndexingStrategy: types.IndexingStrategy{VectorEnabled: true},
	}

	svc.processChunks(context.Background(), kb, knowledge,
		[]types.ParsedChunk{{Content: "body", Seq: 0, Start: 0, End: 4}})

	require.Greater(t, repo.reads, 1, "the guard must re-read after resolving the model")
	require.Empty(t, repo.updates, "cancelled must not be overwritten with failed")
}

// ReplaceKnowledgeFile keeps the knowledge ID and swaps file_path, so a stale
// worker must not persist its row: UpdateKnowledge is a full-row Save and
// file_path is not omitted, so it would roll the path back over the
// replacement and mark the replacement's live attempt failed.
func TestProcessChunksSkipsEmbeddingFailureWriteWhenSourceReplaced(t *testing.T) {
	knowledge := &types.Knowledge{
		ID:              "knowledge-1",
		TenantID:        1,
		KnowledgeBaseID: "kb-1",
		ParseStatus:     types.ParseStatusProcessing,
		FilePath:        "tenant/1/old.pdf",
	}
	repo := &embedFailureKnowledgeRepo{
		knowledge: knowledge,
		// Reads 1-2 are the entry guard (aborted + source-replaced) and still
		// see this worker's own file; the replacement lands while the
		// embedding model is being resolved.
		storedAfterReads: 2,
		stored: &types.Knowledge{
			ID:          "knowledge-1",
			TenantID:    1,
			ParseStatus: types.ParseStatusProcessing,
			FilePath:    "tenant/1/replacement.pdf",
		},
	}
	svc := &knowledgeService{
		repo:         repo,
		modelService: embedFailureModelService{err: errors.New("embedding model not found")},
	}
	kb := &types.KnowledgeBase{
		ID:               "kb-1",
		TenantID:         1,
		EmbeddingModelID: "embedding-1",
		IndexingStrategy: types.IndexingStrategy{VectorEnabled: true},
	}

	svc.processChunks(context.Background(), kb, knowledge,
		[]types.ParsedChunk{{Content: "body", Seq: 0, Start: 0, End: 4}})

	require.Greater(t, repo.reads, 2, "the guard must re-read after resolving the model")
	require.Empty(t, repo.updates,
		"a replaced source must not have this attempt's failure written back")
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
