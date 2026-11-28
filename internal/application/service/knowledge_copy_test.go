package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/access"
	filesvc "github.com/Tencent/WeKnora/internal/application/service/file"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/require"
)

func copyTask(t *testing.T, ids ...string) *asynq.Task {
	t.Helper()
	raw, err := json.Marshal(types.KnowledgeCopyPayload{
		TenantID: 7, TaskID: "copy-task", SourceKBID: "kb", TargetKBID: "other",
		KnowledgeIDs: ids, Mode: "reuse_vectors",
	})
	require.NoError(t, err)
	return asynq.NewTask(types.TypeKnowledgeCopy, raw)
}

func TestKnowledgeCopyKeepsSourceAndRetriesWithoutDuplicating(t *testing.T) {
	t.Setenv("RETRIEVE_DRIVER", "postgres")
	f := transferFixture(t, access.KBTransferCopy)
	for _, kb := range f.kbs.values {
		kb.Type = types.KnowledgeBaseTypeDocument
		kb.EmbeddingModelID = "model"
		kb.IndexingStrategy.VectorEnabled = true
	}
	engine := &cloneCopyEngine{}
	f.svc.retrieveEngine = parentChildRetrieveRegistry{engine: engine}
	f.svc.modelService = parentChildModelService{embedder: parentChildEmbedder{}}
	ctx := context.Background()
	files := filesvc.NewLocalFileService(t.TempDir(), "")
	f.svc.fileSvc = files
	sourcePath, err := files.SaveBytes(ctx, []byte("original file"), 7, "example.txt", false)
	require.NoError(t, err)
	datasourceMetadata := types.JSON(`{
		"datasource_id":"sync-1","external_id":"remote-1","source_resource_id":"space-1"
	}`)
	require.NoError(t, f.db.Model(&types.Knowledge{}).Where("id = ?", "doc").
		Updates(map[string]any{
			"type":               "file",
			"file_path":          sourcePath,
			"file_hash":          "source-hash",
			"file_type":          "txt",
			"embedding_model_id": "model",
			"enable_status":      "enabled",
			"metadata":           datasourceMetadata,
		}).Error)

	task := copyTask(t, "doc", "doc")
	fault := &cloneFaultChunks{ChunkRepository: f.chunkRepo, fail: true}
	f.svc.chunkRepo = fault
	require.ErrorContains(t, f.svc.ProcessKnowledgeCopy(ctx, task), "lost clone acknowledgement")
	fault.fail = false
	require.NoError(t, f.svc.ProcessKnowledgeCopy(ctx, task))

	copies, err := f.repo.ListKnowledgeByKnowledgeBaseID(f.ctx, 7, "other")
	require.NoError(t, err)
	require.Len(t, copies, 2, "preserve the existing target document")
	var dst *types.Knowledge
	for _, candidate := range copies {
		if candidate.ID == "other-doc" {
			continue
		}
		dst = candidate
		require.Equal(t, types.ParseStatusCompleted, candidate.ParseStatus)
		require.Equal(t, "enabled", candidate.EnableStatus)
		require.Empty(t, candidate.GetMetadata()["datasource_id"])
	}
	require.NotNil(t, dst)
	require.NotEqual(t, sourcePath, dst.FilePath)
	chunks, err := f.chunkRepo.ListAllChunksByKnowledgeID(ctx, 7, dst.ID)
	require.NoError(t, err)
	require.Len(t, chunks, 1)
	require.Equal(t, "original", chunks[0].Content)
	require.Equal(t, chunks[0].ID, engine.copied["chunk"])

	require.NoError(t, f.svc.ProcessKnowledgeCopy(ctx, task))
	replayed, err := f.repo.GetKnowledgeByID(ctx, 7, dst.ID)
	require.NoError(t, err)
	require.Equal(t, "other", replayed.KnowledgeBaseID)
	copies, err = f.repo.ListKnowledgeByKnowledgeBaseID(f.ctx, 7, "other")
	require.NoError(t, err)
	require.Len(t, copies, 2, "replaying the task does not produce another copy")

	source, err := f.repo.GetKnowledgeByID(ctx, 7, "doc")
	require.NoError(t, err)
	require.Equal(t, "kb", source.KnowledgeBaseID)
	require.Equal(t, types.ParseStatusCompleted, source.ParseStatus)
	require.Equal(t, "sync-1", source.GetMetadata()["datasource_id"])
	require.Equal(t, sourcePath, source.FilePath)
	sourceChunk, err := f.chunkRepo.GetChunkByID(ctx, 7, "chunk")
	require.NoError(t, err)
	require.Equal(t, "doc", sourceChunk.KnowledgeID)
	progress, err := f.svc.GetKnowledgeCopyProgress(ctx, "copy-task")
	require.NoError(t, err)
	require.Equal(t, types.KBCloneStatusCompleted, progress.Status)
	require.Equal(t, 100, progress.Progress)
	require.Equal(t, 1, progress.Total)

	var tenant types.Tenant
	require.NoError(t, f.db.First(&tenant, 7).Error)
	require.EqualValues(t, 10, tenant.StorageUsed)

	// The stored file remains readable after the source object disappears.
	require.NoError(t, files.DeleteFile(ctx, sourcePath))
	copiedFile, err := files.GetFile(ctx, dst.FilePath)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, copiedFile.Close()) })
	body, err := io.ReadAll(copiedFile)
	require.NoError(t, err)
	require.Equal(t, "original file", string(body))
}

func TestKnowledgeCopyDoesNotReplaceDuplicateTargetFile(t *testing.T) {
	f := transferFixture(t, access.KBTransferCopy)
	for _, kb := range f.kbs.values {
		kb.Type = types.KnowledgeBaseTypeDocument
	}
	require.NoError(t, f.db.Model(&types.Knowledge{}).Where("id IN ?", []string{"doc", "other-doc"}).
		Updates(map[string]any{"type": "file", "file_hash": "same-bytes", "file_type": "pdf"}).Error)
	ctx := context.Background()
	require.ErrorContains(t, f.svc.ProcessKnowledgeCopy(ctx, copyTask(t, "doc")), "already exists")
	target, err := f.repo.ListKnowledgeByKnowledgeBaseID(ctx, 7, "other")
	require.NoError(t, err)
	require.Len(t, target, 1)
	require.Equal(t, "other-doc", target[0].ID)
	require.Zero(t, f.chunkRepo.writes)
}

func TestKnowledgeCopyRejectsInvalidBatchBeforeWriting(t *testing.T) {
	f := transferFixture(t, access.KBTransferCopy)
	for _, kb := range f.kbs.values {
		kb.Type = types.KnowledgeBaseTypeDocument
	}
	ctx := context.Background()
	require.ErrorContains(t, f.svc.ProcessKnowledgeCopy(ctx, copyTask(t, "doc", "other-doc")), "other-doc")
	f.kbs.values["other"].ChunkingConfig.TokenLimit = 256
	require.ErrorContains(t, f.svc.ProcessKnowledgeCopy(ctx, copyTask(t, "doc")), "different chunking")
	target, err := f.repo.ListKnowledgeByKnowledgeBaseID(ctx, 7, "other")
	require.NoError(t, err)
	require.Len(t, target, 1)
	require.Equal(t, "other-doc", target[0].ID)
	require.Zero(t, f.chunkRepo.writes)
}

// Fail after a committed chunk write to exercise a genuinely partial batch.
type copyFailureChunks struct {
	interfaces.ChunkRepository
	calls  int
	failAt int
}

func (r *copyFailureChunks) CreateChunks(ctx context.Context, chunks []*types.Chunk) error {
	if err := r.ChunkRepository.CreateChunks(ctx, chunks); err != nil {
		return err
	}
	r.calls++
	if r.calls == r.failAt {
		return errors.New("lost copy acknowledgement")
	}
	return nil
}

func TestKnowledgeCopyRetryReusesCompletedSnapshotAfterSourceChanges(t *testing.T) {
	for _, change := range []string{"deleted", "moved", "reparsing", "embedding changed"} {
		t.Run(change, func(t *testing.T) {
			f := transferFixture(t, access.KBTransferCopy)
			for _, kb := range f.kbs.values {
				kb.Type = types.KnowledgeBaseTypeDocument
			}
			require.NoError(t, f.db.Create(&types.Knowledge{
				ID: "remaining", TenantID: 7, KnowledgeBaseID: "kb", Title: "remaining",
				Type: types.KnowledgeTypeManual, ParseStatus: types.ParseStatusCompleted,
			}).Error)
			require.NoError(t, f.db.Create(&types.Chunk{
				ID: "remaining-chunk", TenantID: 7, KnowledgeID: "remaining", KnowledgeBaseID: "kb",
				ChunkType: types.ChunkTypeText, Content: "remaining",
			}).Error)
			fault := &copyFailureChunks{ChunkRepository: f.chunkRepo, failAt: 2}
			f.svc.chunkRepo = fault
			ctx := types.WithTaskRetryMetadata(context.Background(), 0, 3)
			task := copyTask(t, "doc", "remaining")
			require.ErrorContains(t, f.svc.ProcessKnowledgeCopy(ctx, task), "lost copy acknowledgement")
			switch change {
			case "deleted":
				require.NoError(t, f.db.Unscoped().Where("id = ?", "doc").Delete(&types.Knowledge{}).Error)
			case "moved":
				require.NoError(t, f.db.Model(&types.Knowledge{}).Where("id = ?", "doc").
					Update("knowledge_base_id", "third").Error)
			case "reparsing":
				require.NoError(t, f.db.Model(&types.Knowledge{}).Where("id = ?", "doc").
					Update("parse_status", types.ParseStatusProcessing).Error)
			case "embedding changed":
				require.NoError(t, f.db.Model(&types.Knowledge{}).Where("id = ?", "doc").
					Update("embedding_model_id", "changed-model").Error)
			}
			fault.failAt = 0
			require.NoError(t, f.svc.ProcessKnowledgeCopy(ctx, task))
			rows, err := f.repo.ListKnowledgeByKnowledgeBaseID(f.ctx, 7, "other")
			require.NoError(t, err)
			require.Len(t, rows, 3)
			for _, row := range rows {
				require.Equal(t, types.ParseStatusCompleted, row.ParseStatus)
			}
			progress, err := f.svc.GetKnowledgeCopyProgress(ctx, "copy-task")
			require.NoError(t, err)
			require.Equal(t, types.KBCloneStatusCompleted, progress.Status)
			require.Equal(t, 2, progress.Processed)
			require.Zero(t, progress.Failed)
		})
	}
}

func TestKnowledgeCopyProgressHonorsLiteRetryMetadata(t *testing.T) {
	for _, attempt := range []int{0, 3} {
		t.Run(fmt.Sprint(attempt), func(t *testing.T) {
			f := transferFixture(t, access.KBTransferCopy)
			for _, kb := range f.kbs.values {
				kb.Type = types.KnowledgeBaseTypeDocument
			}
			// Exercise Lite's in-memory progress path as well as its retry metadata.
			redisClient := f.svc.redisClient
			f.svc.redisClient = nil
			t.Cleanup(func() { f.svc.redisClient = redisClient })
			f.svc.chunkRepo = &cloneFaultChunks{ChunkRepository: f.chunkRepo, fail: true}
			ctx := types.WithTaskRetryMetadata(context.Background(), attempt, 3)
			require.ErrorContains(t, f.svc.ProcessKnowledgeCopy(ctx, copyTask(t, "doc")), "lost clone acknowledgement")
			progress, err := f.svc.GetKnowledgeCopyProgress(ctx, "copy-task")
			require.NoError(t, err)
			expected := types.KBCloneStatusProcessing
			if attempt == 3 {
				expected = types.KBCloneStatusFailed
			}
			require.Equal(t, expected, progress.Status)
		})
	}
}
