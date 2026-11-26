package service

import (
	"context"
	"errors"
	"testing"

	werrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/models/embedding"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

// quotaChunkRepo records whether chunks were deleted after processChunks
// wrote them; the delete at the start of processChunks is the idempotent
// cleanup of a previous attempt and does not count.
type quotaChunkRepo struct {
	interfaces.ChunkRepository
	created             bool
	deletesAfterCreated int
}

func (r *quotaChunkRepo) DeleteChunksByKnowledgeID(context.Context, uint64, string) error {
	if r.created {
		r.deletesAfterCreated++
	}
	return nil
}

func (r *quotaChunkRepo) CreateChunks(context.Context, []*types.Chunk) error {
	r.created = true
	return nil
}

type quotaRetrieveEngine struct {
	parentChildRetrieveEngine
	estimate int64
}

func (e *quotaRetrieveEngine) EstimateStorageSize(
	context.Context, embedding.Embedder, []*types.IndexInfo, []types.RetrieverType,
) int64 {
	return e.estimate
}

type quotaTenantRepo struct {
	parentChildTenantRepo
	tenant *types.Tenant
	err    error
}

func (r quotaTenantRepo) GetTenantByID(context.Context, uint64) (*types.Tenant, error) {
	return r.tenant, r.err
}

// newQuotaTestService wires the minimum processChunks needs to reach the
// quota check: a vector-enabled knowledge base, a tenant override, and an
// optional span tracker (nil keeps the pipeline from recording spans).
func newQuotaTestService(
	knowledge *types.Knowledge,
	chunks *quotaChunkRepo,
	engine *quotaRetrieveEngine,
	tenantRepo quotaTenantRepo,
	tracker SpanTracker,
) *knowledgeService {
	return &knowledgeService{
		repo:           &parentChildKnowledgeRepo{knowledge: knowledge},
		chunkRepo:      chunks,
		modelService:   parentChildModelService{embedder: parentChildEmbedder{}},
		retrieveEngine: parentChildRetrieveRegistry{engine: engine},
		graphEngine:    parentChildGraphRepo{},
		tenantRepo:     tenantRepo,
		task:           parentChildTaskEnqueuer{},
		spanTracker:    tracker,
	}
}

// A knowledge that fails the storage quota check (or cannot re-read the
// tenant for it) must not leave the chunks written just before the check
// active under a failed knowledge — the BatchIndex failure path already
// removes them, and the quota branches have to do the same.
func TestProcessChunksDeletesChunksWhenStorageQuotaCheckFails(t *testing.T) {
	for _, tc := range []struct {
		name      string
		tenant    *types.Tenant
		tenantErr error
	}{
		{
			name:   "quota exceeded",
			tenant: &types.Tenant{ID: 1, StorageQuota: 100, StorageUsed: 90},
		},
		{
			name:      "tenant re-read fails",
			tenantErr: errors.New("tenant lookup failed"),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			knowledge := &types.Knowledge{
				ID:              "knowledge-1",
				TenantID:        1,
				KnowledgeBaseID: "kb-1",
				ParseStatus:     types.ParseStatusProcessing,
			}
			chunkRepo := &quotaChunkRepo{}
			retrieveEngine := &quotaRetrieveEngine{estimate: 50}
			tenant := &types.Tenant{
				ID:           1,
				StorageQuota: 100,
				RetrieverEngines: types.RetrieverEngines{Engines: []types.RetrieverEngineParams{
					{
						RetrieverType:       types.VectorRetrieverType,
						RetrieverEngineType: types.PostgresRetrieverEngineType,
					},
				}},
			}
			ctx := context.WithValue(context.Background(), types.TenantInfoContextKey, tenant)
			svc := newQuotaTestService(knowledge, chunkRepo, retrieveEngine,
				quotaTenantRepo{tenant: tc.tenant, err: tc.tenantErr}, nil)
			kb := &types.KnowledgeBase{
				ID:               "kb-1",
				TenantID:         1,
				EmbeddingModelID: "embedding-1",
				IndexingStrategy: types.IndexingStrategy{VectorEnabled: true},
			}

			// Terminal for this attempt: the failure is recorded, nothing to retry.
			require.NoError(t, svc.processChunks(ctx, kb, knowledge,
				[]types.ParsedChunk{{Content: "body", Seq: 0, Start: 0, End: 4}}))

			require.Equal(t, types.ParseStatusFailed, knowledge.ParseStatus)
			require.True(t, chunkRepo.created, "the quota check runs after the chunks are written")
			require.Equal(t, 1, chunkRepo.deletesAfterCreated,
				"the chunks written for this attempt must be deleted when it fails")
			require.Empty(t, retrieveEngine.indexed, "a failed quota check must not index anything")
		})
	}
}

// The embedding stage opens its span before the quota check runs, so the
// rejection has to close that span itself. Returning with it open left the
// stage "running" until the housekeeping reaper failed it as TASK_STALLED,
// and the UI then reported a stall instead of the quota rejection that
// actually stopped the ingest (#5936).
func TestProcessChunksClosesEmbeddingSpanWhenStorageQuotaCheckFails(t *testing.T) {
	for _, tc := range []struct {
		name      string
		tenant    *types.Tenant
		tenantErr error
		wantCode  string
	}{
		{
			name:     "quota exceeded",
			tenant:   &types.Tenant{ID: 1, StorageQuota: 100, StorageUsed: 90},
			wantCode: werrors.ErrCodeStorageQuotaExceeded,
		},
		{
			name:      "tenant re-read fails",
			tenantErr: errors.New("tenant lookup failed"),
			wantCode:  werrors.ErrCodeUnknown,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tracker, db := setupSpanTrackerTest(t)
			knowledge := &types.Knowledge{
				ID:              "knowledge-1",
				TenantID:        1,
				KnowledgeBaseID: "kb-1",
				ParseStatus:     types.ParseStatusProcessing,
			}
			tenant := &types.Tenant{
				ID:           1,
				StorageQuota: 100,
				RetrieverEngines: types.RetrieverEngines{Engines: []types.RetrieverEngineParams{
					{
						RetrieverType:       types.VectorRetrieverType,
						RetrieverEngineType: types.PostgresRetrieverEngineType,
					},
				}},
			}

			ctx := context.WithValue(context.Background(), types.TenantInfoContextKey, tenant)
			_, attempt, err := tracker.OpenAttempt(ctx, knowledge.ID, "")
			require.NoError(t, err)
			ctx = withAttempt(ctx, attempt)

			svc := newQuotaTestService(knowledge, &quotaChunkRepo{},
				&quotaRetrieveEngine{estimate: 50},
				quotaTenantRepo{tenant: tc.tenant, err: tc.tenantErr}, tracker)
			kb := &types.KnowledgeBase{
				ID:               "kb-1",
				TenantID:         1,
				EmbeddingModelID: "embedding-1",
				IndexingStrategy: types.IndexingStrategy{VectorEnabled: true},
			}

			require.NoError(t, svc.processChunks(ctx, kb, knowledge,
				[]types.ParsedChunk{{Content: "body", Seq: 0, Start: 0, End: 4}}))

			var span struct {
				Status       string
				ErrorCode    string
				ErrorMessage string
			}
			require.NoError(t, db.Table("knowledge_processing_spans").
				Select("status, error_code, error_message").
				Where("knowledge_id = ? AND attempt = ? AND name = ?",
					knowledge.ID, attempt, types.StageEmbedding).
				Take(&span).Error)
			require.Equal(t, types.SpanStatusFailed, span.Status,
				"the embedding span must be failed by the branch that stopped the ingest")
			require.Equal(t, tc.wantCode, span.ErrorCode,
				"the stage must carry the reason the ingest stopped, not a stall")
		})
	}
}
