package service

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/access"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/require"
)

// reparseRecordingEnqueuer records what a submission queued without a broker.
type reparseRecordingEnqueuer struct {
	queued []string
}

func (e *reparseRecordingEnqueuer) Enqueue(
	task *asynq.Task,
	_ ...asynq.Option,
) (*asynq.TaskInfo, error) {
	e.queued = append(e.queued, task.Type())
	return &asynq.TaskInfo{ID: "task-1", Queue: types.QueueDefault}, nil
}

// reparseCleanupSpy counts the destructive cleanup paths so a test can prove
// which of them ran.
type reparseCleanupSpy struct {
	interfaces.ChunkRepository
	chunkDeletes int
	images       []interfaces.ChunkImageInfo
}

func (s *reparseCleanupSpy) DeleteChunksByKnowledgeID(context.Context, uint64, string) error {
	s.chunkDeletes++
	return nil
}

func (s *reparseCleanupSpy) ListImageInfoByKnowledgeIDs(
	context.Context, uint64, []string,
) ([]interfaces.ChunkImageInfo, error) {
	return s.images, nil
}

type reparseGraphSpy struct {
	interfaces.RetrieveGraphRepository
	calls int
}

func (s *reparseGraphSpy) DelGraph(context.Context, []types.NameSpace) error {
	s.calls++
	return nil
}

type reparseFileSpy struct {
	interfaces.FileService
	deleted []string
}

func (s *reparseFileSpy) DeleteFile(_ context.Context, path string) error {
	s.deleted = append(s.deleted, path)
	return nil
}

type reparseTenantSpy struct {
	interfaces.TenantRepository
	adjustments []int64
}

func (s *reparseTenantSpy) GetTenantByID(_ context.Context, id uint64) (*types.Tenant, error) {
	return &types.Tenant{ID: id}, nil
}

func (s *reparseTenantSpy) AdjustStorageUsed(_ context.Context, _ uint64, delta int64) error {
	s.adjustments = append(s.adjustments, delta)
	return nil
}

// TestReparseSubmissionKeepsIndexedContent covers #5002: submitting a reparse
// used to delete the document's chunks, index rows, images and graph data right
// away, while the replacement was only written when the document's own task
// ran. The knowledge base therefore dropped out of retrieval for the whole
// batch, and a document whose parse failed stayed empty permanently. Submission
// must only queue the work; the previous content stays searchable until the
// processing task replaces it.
func TestReparseSubmissionKeepsIndexedContent(t *testing.T) {
	kb := &types.KnowledgeBase{ID: "kb", TenantID: 7}
	knowledge := &types.Knowledge{
		ID:              "file-doc",
		TenantID:        7,
		KnowledgeBaseID: "kb",
		Title:           "manual.pdf",
		Type:            "file",
		ParseStatus:     types.ParseStatusCompleted,
		FilePath:        "local://7/kb/manual.pdf",
		FileName:        "manual.pdf",
		StorageSize:     4096,
	}

	enqueuer := &reparseRecordingEnqueuer{}
	cleanup := &reparseCleanupSpy{
		images: []interfaces.ChunkImageInfo{{KnowledgeID: knowledge.ID, ImageInfo: `[{"url":"page-1.png"}]`}},
	}
	graph := &reparseGraphSpy{}
	files := &reparseFileSpy{}
	tenants := &reparseTenantSpy{}
	svc := &knowledgeService{
		repo:         &reparseFailureKnowledgeRepo{knowledge: knowledge},
		kbService:    &reparseFailureKBService{kb: kb},
		task:         enqueuer,
		chunkRepo:    cleanup,
		chunkService: &chunkService{chunkRepository: cleanup},
		graphEngine:  graph,
		fileSvc:      files,
		tenantRepo:   tenants,
	}

	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7))
	ctx = context.WithValue(ctx, types.TenantInfoContextKey, &types.Tenant{ID: 7, StorageUsed: 10000})
	ctx, err := access.WithKBTaskWrite(ctx, kb, 7)
	require.NoError(t, err)

	submitted, err := svc.ReparseKnowledge(ctx, knowledge.ID, nil)
	require.NoError(t, err)
	require.NotNil(t, submitted)

	// The reparse is queued for the document's own task...
	require.Equal(t, []string{types.TypeDocumentProcess}, enqueuer.queued)

	// ...and nothing the document currently serves was removed.
	require.Zero(t, cleanup.chunkDeletes, "submission must not delete chunks retrieval still serves")
	require.Zero(t, graph.calls, "submission must not drop the graph data")
	require.Empty(t, files.deleted, "submission must not delete extracted images")
	require.Empty(t, tenants.adjustments, "submission must not release storage")
	require.Equal(t, int64(4096), knowledge.StorageSize)
}

// TestReleaseReplacedKnowledgeContent pins the timing that makes the fix safe:
// the previous attempt's images and accounted storage are released only when a
// replacement attempt is about to write, and only when the document still
// accounts for previous content. Dropping either guard would either resurrect
// the #5002 wipe for documents that have not been parsed yet, or make every
// first-time ingest walk a cleanup path built for existing content.
func TestReleaseReplacedKnowledgeContent(t *testing.T) {
	cases := []struct {
		name        string
		attempt     int
		storageSize int64
		wantCleanup bool
	}{
		{name: "first ingest releases nothing", attempt: 0, storageSize: 0},
		{name: "first ingest of a replacement attempt", attempt: 1, storageSize: 0},
		{name: "replacement with nothing accounted", attempt: 0, storageSize: 4096},
		{name: "replacement of indexed content", attempt: 1, storageSize: 4096, wantCleanup: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			kb := &types.KnowledgeBase{ID: "kb", TenantID: 7}
			knowledge := &types.Knowledge{
				ID:              "file-doc",
				TenantID:        7,
				KnowledgeBaseID: "kb",
				Type:            "file",
				ParseStatus:     types.ParseStatusCompleted,
				FilePath:        "local://7/kb/manual.pdf",
				FileName:        "manual.pdf",
				StorageSize:     tc.storageSize,
			}

			cleanup := &reparseCleanupSpy{
				images: []interfaces.ChunkImageInfo{{KnowledgeID: knowledge.ID, ImageInfo: `[{"url":"page-1.png"}]`}},
			}
			graph := &reparseGraphSpy{}
			files := &reparseFileSpy{}
			tenants := &reparseTenantSpy{}
			svc := &knowledgeService{
				kbService:    &reparseFailureKBService{kb: kb},
				chunkRepo:    cleanup,
				chunkService: &chunkService{chunkRepository: cleanup},
				graphEngine:  graph,
				fileSvc:      files,
				tenantRepo:   tenants,
			}

			ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7))
			ctx = context.WithValue(ctx, types.TenantInfoContextKey, &types.Tenant{ID: 7, StorageUsed: 10000})
			ctx, err := access.WithKBTaskWrite(ctx, kb, 7)
			require.NoError(t, err)

			svc.releaseReplacedKnowledgeContent(ctx, knowledge, tc.attempt)

			if !tc.wantCleanup {
				require.Zero(t, cleanup.chunkDeletes)
				require.Empty(t, files.deleted)
				require.Empty(t, tenants.adjustments)
				require.Equal(t, tc.storageSize, knowledge.StorageSize)
				return
			}

			// The previous attempt's resources are gone before the replacement
			// is written, and the tenant stops paying for content that no
			// longer exists.
			require.Equal(t, 1, cleanup.chunkDeletes)
			require.Equal(t, []string{"page-1.png"}, files.deleted)
			require.Equal(t, []int64{-4096}, tenants.adjustments)
			require.Zero(t, knowledge.StorageSize)
		})
	}
}
