package service

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/embedding"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

type chunkIndexWindowEngine struct {
	parentChildRetrieveEngine
	estimateSizes []int
	indexSizes    []int
	indexed       []types.IndexInfo
	failAt        int
	cancel        context.CancelFunc
	indexDeletes  int
}

func (e *chunkIndexWindowEngine) EstimateStorageSize(
	_ context.Context, _ embedding.Embedder, infos []*types.IndexInfo, _ []types.RetrieverType,
) int64 {
	e.estimateSizes = append(e.estimateSizes, len(infos))
	var total int64
	for _, info := range infos {
		total += int64(len(info.Content)) + 4
	}
	return total
}

func (e *chunkIndexWindowEngine) BatchIndex(
	_ context.Context, _ embedding.Embedder, infos []*types.IndexInfo, _ []types.RetrieverType,
) error {
	e.indexSizes = append(e.indexSizes, len(infos))
	if len(e.indexSizes) == e.failAt {
		return errors.New("index write failed")
	}
	for _, info := range infos {
		e.indexed = append(e.indexed, *info)
	}
	if e.cancel != nil {
		e.cancel()
	}
	return nil
}

func (e *chunkIndexWindowEngine) DeleteByKnowledgeIDList(context.Context, []string, int, string) error {
	e.indexDeletes++
	e.indexed = nil
	return nil
}

type chunkIndexWindowTenantRepo struct {
	quotaTenantRepo
	adjusted int64
}

func (r *chunkIndexWindowTenantRepo) AdjustStorageUsed(_ context.Context, _ uint64, delta int64) error {
	r.adjusted += delta
	return nil
}

func chunkIndexWindowFixture(ctx context.Context, count int) (
	context.Context, *knowledgeService, *types.KnowledgeBase, *types.Knowledge,
	[]types.ParsedChunk, *chunkIndexWindowEngine, *chunkIndexWindowTenantRepo,
) {
	knowledge := &types.Knowledge{
		ID: "knowledge-1", TenantID: 1, KnowledgeBaseID: "kb-1",
		Title: "title", ParseStatus: types.ParseStatusProcessing,
	}
	engine := &chunkIndexWindowEngine{}
	tenant := &types.Tenant{
		ID: 1,
		RetrieverEngines: types.RetrieverEngines{Engines: []types.RetrieverEngineParams{{
			RetrieverType:       types.VectorRetrieverType,
			RetrieverEngineType: types.PostgresRetrieverEngineType,
		}}},
	}
	tenantRepo := &chunkIndexWindowTenantRepo{quotaTenantRepo: quotaTenantRepo{tenant: tenant}}
	svc := &knowledgeService{
		repo:           &parentChildKnowledgeRepo{knowledge: knowledge},
		chunkRepo:      &indexRetryChunkRepo{},
		modelService:   parentChildModelService{embedder: parentChildEmbedder{}},
		retrieveEngine: parentChildRetrieveRegistry{engine: engine},
		graphEngine:    parentChildGraphRepo{},
		tenantRepo:     tenantRepo,
		task:           parentChildTaskEnqueuer{},
	}
	kb := &types.KnowledgeBase{
		ID: "kb-1", TenantID: 1, EmbeddingModelID: "embedding-1",
		IndexingStrategy: types.IndexingStrategy{VectorEnabled: true},
	}
	chunks := make([]types.ParsedChunk, count)
	for i := range chunks {
		chunks[i] = types.ParsedChunk{
			Content: fmt.Sprintf("body %03d", i), ContextHeader: "Section", Seq: i, ParentIndex: 0,
		}
	}
	return context.WithValue(ctx, types.TenantInfoContextKey, tenant), svc, kb, knowledge, chunks, engine, tenantRepo
}

func TestProcessChunksBoundsIndexInputs(t *testing.T) {
	for _, count := range []int{0, 200, 201, 437} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			ctx, svc, kb, knowledge, chunks, engine, tenantRepo := chunkIndexWindowFixture(context.Background(), count)
			wantStorage := int64(count * (len("title\nSection\n\nbody 000") + 4))
			tenantRepo.tenant.StorageQuota = wantStorage + 13
			tenantRepo.tenant.StorageUsed = 13
			err := svc.processChunks(ctx, kb, knowledge, chunks, ProcessChunksOptions{
				ParentChunks: []types.ParsedParentChunk{{Content: "parent stays out of the index"}},
			})
			require.NoError(t, err)

			for _, size := range engine.estimateSizes {
				require.LessOrEqual(t, size, 200, "storage estimation must stay within one index window")
			}
			for _, size := range engine.indexSizes {
				require.LessOrEqual(t, size, 200, "generated index content must be released between windows")
			}
			require.Len(t, engine.indexed, count)
			want := make([]types.IndexInfo, 0, count)
			for i, chunk := range chunks {
				want = append(want, types.IndexInfo{
					Content:  fmt.Sprintf("title\nSection\n\nbody %03d", i),
					SourceID: chunk.ChunkID, ChunkID: chunk.ChunkID,
					KnowledgeID: knowledge.ID, KnowledgeBaseID: kb.ID,
					SourceType: types.ChunkSourceType, IsEnabled: true,
				})
			}
			require.ElementsMatch(t, want, engine.indexed)
			require.Equal(t, wantStorage, knowledge.StorageSize)
			require.Equal(t, wantStorage, tenantRepo.adjusted)
			require.Equal(t, wantStorage+13, tenantRepo.tenant.StorageUsed)
			require.Equal(t, "enabled", knowledge.EnableStatus)
		})
	}
}

func TestProcessChunksChecksWholeDocumentQuotaBeforeIndexingWindows(t *testing.T) {
	ctx, svc, kb, knowledge, chunks, engine, tenantRepo := chunkIndexWindowFixture(context.Background(), 437)
	// The first window fits, but the whole document does not.
	tenantRepo.tenant.StorageQuota = int64(300 * (len("title\nSection\n\nbody 000") + 4))
	require.NoError(t, svc.processChunks(ctx, kb, knowledge, chunks))
	require.Equal(t, types.ParseStatusFailed, knowledge.ParseStatus)
	require.Empty(t, engine.indexSizes, "quota must be checked before writing any window")
	require.Equal(t, 2, svc.chunkRepo.(*indexRetryChunkRepo).deletes)
	require.Zero(t, tenantRepo.adjusted)
}

func TestProcessChunksRollsBackEarlierIndexWindows(t *testing.T) {
	for _, retryCount := range []int{0, 3} {
		t.Run(fmt.Sprint(retryCount), func(t *testing.T) {
			base := types.WithTaskRetryMetadata(context.Background(), retryCount, 3)
			ctx, svc, kb, knowledge, chunks, engine, tenantRepo := chunkIndexWindowFixture(base, 437)
			engine.failAt = 2
			err := svc.processChunks(ctx, kb, knowledge, chunks)
			if retryCount < 3 {
				require.ErrorContains(t, err, "index write failed")
				require.Equal(t, types.ParseStatusProcessing, knowledge.ParseStatus)
			} else {
				require.NoError(t, err)
				require.Equal(t, types.ParseStatusFailed, knowledge.ParseStatus)
				require.Contains(t, knowledge.ErrorMessage, "index write failed")
			}
			require.Len(t, engine.indexSizes, 2, "do not send later windows after a failure")
			require.Empty(t, engine.indexed, "earlier windows must be rolled back with the failed document")
			require.Equal(t, 2, engine.indexDeletes)
			require.Equal(t, 2, svc.chunkRepo.(*indexRetryChunkRepo).deletes)
			require.Zero(t, tenantRepo.adjusted)
			require.Zero(t, knowledge.StorageSize)
			require.Empty(t, knowledge.EnableStatus)
		})
	}
}

func TestProcessChunksStopsIndexWindowsOnCancellation(t *testing.T) {
	base, cancel := context.WithCancel(types.WithTaskRetryMetadata(context.Background(), 0, 3))
	defer cancel()
	ctx, svc, kb, knowledge, chunks, engine, tenantRepo := chunkIndexWindowFixture(base, 437)
	engine.cancel = cancel
	_ = svc.processChunks(ctx, kb, knowledge, chunks)
	require.Len(t, engine.indexSizes, 1, "a cancelled run must not write another window")
	require.Zero(t, tenantRepo.adjusted)
	require.Empty(t, knowledge.EnableStatus)
}
