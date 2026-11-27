package service

import (
	"context"
	"crypto/md5"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type datasourceReplacementChunks struct {
	replaceFileChunks
	deleted int
	err     error
}

func (c *datasourceReplacementChunks) DeleteByKnowledgeList(context.Context, uint64, []string) error {
	c.deleted++
	return c.err
}

type datasourceReplacementTenant struct{ interfaces.TenantRepository }

func (datasourceReplacementTenant) AdjustStorageUsed(context.Context, uint64, int64) error {
	return nil
}

type datasourceReplacementQueue struct {
	t      *testing.T
	repo   interfaces.KnowledgeRepository
	oldID  string
	queued int
}

func (q *datasourceReplacementQueue) Enqueue(task *asynq.Task, _ ...asynq.Option) (*asynq.TaskInfo, error) {
	if task.Type() == types.TypeDocumentProcess {
		var payload types.DocumentProcessPayload
		require.NoError(q.t, json.Unmarshal(task.Payload(), &payload))
		_, err := q.repo.GetKnowledgeByID(context.Background(), 7, q.oldID)
		require.Error(q.t, err, "retirement must commit before a worker can start")
		created, err := q.repo.GetKnowledgeByID(context.Background(), 7, payload.KnowledgeID)
		require.NoError(q.t, err, "replacement must commit before a worker can start")
		require.Equal(q.t, "ds-1", created.GetMetadata()["datasource_id"])
		require.Equal(q.t, "doc-1", created.GetMetadata()["external_id"])
		q.queued++
	}
	return &asynq.TaskInfo{ID: "task-1", Queue: types.QueueDefault}, nil
}

func datasourceReplacementFixture(t *testing.T) (
	*replaceFileHarness, *gorm.DB, *datasourceReplacementChunks, *datasourceReplacementQueue,
) {
	t.Helper()
	h := newReplaceFileHarness(t)
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "datasource.db")), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	require.NoError(t, db.AutoMigrate(&types.Knowledge{}, &types.KnowledgeTag{}, &types.KnowledgeTagRelation{}))
	repo := repository.NewKnowledgeRepository(db)
	h.original.Metadata = types.JSON(`{"datasource_id":"ds-1","external_id":"doc-1"}`)
	require.NoError(t, repo.CreateKnowledge(h.ctx, &h.original))
	h.svc.repo = repo
	chunks := &datasourceReplacementChunks{}
	h.svc.chunkRepo = chunks
	h.svc.tenantRepo = datasourceReplacementTenant{}
	queue := &datasourceReplacementQueue{t: t, repo: repo, oldID: h.original.ID}
	h.svc.task = queue
	return h, db, chunks, queue
}

func TestDataSourceReplacementPreservesOldResourcesOnFailure(t *testing.T) {
	for _, stage := range []string{"validation", "json", "storage", "insert"} {
		t.Run(stage, func(t *testing.T) {
			h, db, chunks, queue := datasourceReplacementFixture(t)
			item := &types.FetchedItem{ExternalID: "doc-1", Content: []byte("new content"), FileName: "doc.md"}
			switch stage {
			case "validation":
				item.FileName = "doc.unsupported"
			case "json":
				item.FileName, item.Content = "doc.json", []byte("invalid JSON")
			case "storage":
				h.store.saveErr = errors.New("storage unavailable")
			case "insert":
				require.NoError(t, db.Exec(`CREATE TRIGGER fail_replacement BEFORE INSERT ON knowledges
					BEGIN SELECT RAISE(FAIL, 'forced insert failure'); END`).Error)
			}
			ds := &DataSourceService{knowledgeService: h.svc}
			_, err := ds.ingestItem(h.ctx,
				&types.DataSource{ID: "ds-1", TenantID: 7, KnowledgeBaseID: "kb-1"}, item, types.DataSourceSyncTags{})
			require.Error(t, err)
			old, err := h.svc.repo.GetKnowledgeByID(h.ctx, 7, h.original.ID)
			require.NoError(t, err)
			require.Equal(t, h.original.FilePath, old.FilePath)
			require.Equal(t, types.ParseStatusCompleted, old.ParseStatus)
			require.Zero(t, chunks.deleted)
			require.Zero(t, queue.queued)
			for _, path := range h.store.deleted {
				require.NotEqual(t, h.original.FilePath, path)
			}
			if stage == "insert" {
				require.Equal(t, []string{"new/file.md"}, h.store.deleted, "discard only the staged new file")
			}
		})
	}
}

func TestDataSourceReplacementCommitsBeforeEnqueueAndCleanup(t *testing.T) {
	for _, kind := range []string{"file", "url", "file_url"} {
		t.Run(kind, func(t *testing.T) {
			h, _, chunks, queue := datasourceReplacementFixture(t)
			tenant := h.ctx.Value(types.TenantInfoContextKey).(*types.Tenant)
			tenant.StorageQuota, tenant.StorageUsed = 100, 100
			require.NoError(t, h.svc.repo.UpdateKnowledgeColumn(h.ctx, h.original.ID, "storage_size", int64(10)))
			item := &types.FetchedItem{ExternalID: "doc-1", FileName: "doc.md"}
			switch kind {
			case "file":
				item.Content = []byte("new content")
			case "url":
				item.URL, item.FileName = "https://example.com/doc", ""
			case "file_url":
				item.URL = "https://example.com/doc.md"
			}
			ds := &DataSourceService{knowledgeService: h.svc}
			updated, err := ds.ingestItem(h.ctx,
				&types.DataSource{ID: "ds-1", TenantID: 7, KnowledgeBaseID: "kb-1"}, item, types.DataSourceSyncTags{})
			require.NoError(t, err)
			require.True(t, updated)
			require.Equal(t, 1, queue.queued)
			require.Equal(t, 1, chunks.deleted)
			require.Contains(t, h.store.deleted, h.original.FilePath)
			created, err := h.svc.repo.FindByDataSourceExternalID(h.ctx, 7, "kb-1", "ds-1", "doc-1")
			require.NoError(t, err)
			require.NotNil(t, created)
			require.Equal(t, kind, created.Type)
			require.NotEqual(t, h.original.ID, created.ID)
		})
	}
}

func TestDataSourceReplacementTagPolicy(t *testing.T) {
	for _, tc := range []struct {
		name         string
		preserve     bool
		before, want []string
	}{
		{"keep manual tags", true, []string{"manual", "review"}, []string{"manual", "review"}},
		{"keep cleared tags", true, nil, nil},
		{"restore source tags", false, []string{"manual"}, []string{"source"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, db, _, queue := datasourceReplacementFixture(t)
			h.svc.tagRepo = repository.NewKnowledgeTagRepository(db)
			for _, id := range []string{"manual", "review", "source"} {
				tag := &types.KnowledgeTag{ID: id, Name: id, TenantID: 7, KnowledgeBaseID: "kb-1"}
				require.NoError(t, h.svc.tagRepo.Create(h.ctx, tag))
			}
			require.NoError(t, h.svc.repo.SetKnowledgeTags(h.ctx, h.original.ID, tc.before))
			ds := &types.DataSource{
				ID: "ds-1", TenantID: 7, KnowledgeBaseID: "kb-1",
				ConflictStrategy: types.ConflictStrategyOverwrite,
			}
			item := &types.FetchedItem{ExternalID: "doc-1", Content: []byte("revised document"), FileName: "doc.md"}
			svc := &DataSourceService{knowledgeService: h.svc}
			tags := types.DataSourceSyncTags{SourceTagIDs: []string{"source"}, PreserveExisting: tc.preserve}
			updated, err := svc.ingestItem(h.ctx, ds, item, tags)
			require.NoError(t, err)
			require.True(t, updated)
			current, err := h.svc.repo.FindByDataSourceExternalID(
				h.ctx, ds.TenantID, ds.KnowledgeBaseID, ds.ID, item.ExternalID)
			require.NoError(t, err)
			require.NotNil(t, current)
			require.Equal(t, fmt.Sprintf("%x", md5.Sum(item.Content)), current.FileHash)
			require.Equal(t, 1, queue.queued)
			currentTags, err := h.svc.repo.GetKnowledgeTags(h.ctx, []string{current.ID})
			require.NoError(t, err)
			var got []string
			for _, tag := range currentTags[current.ID] {
				got = append(got, tag.ID)
				ids, err := h.svc.ListKnowledgeIDsByTagIDs(h.ctx, ds.TenantID, ds.KnowledgeBaseID, []string{tag.ID})
				require.NoError(t, err)
				require.Equal(t, []string{current.ID}, ids)
			}
			require.ElementsMatch(t, tc.want, got)
			require.Error(t, h.svc.repo.SetKnowledgeTags(h.ctx, h.original.ID, []string{"manual"}))
		})
	}
}

func TestDataSourceReplacementCleanupFailureKeepsCommittedReplacement(t *testing.T) {
	h, db, chunks, queue := datasourceReplacementFixture(t)
	chunks.err = errors.New("index cleanup unavailable")
	ds := &DataSourceService{knowledgeService: h.svc}
	_, err := ds.ingestItem(h.ctx, &types.DataSource{ID: "ds-1", TenantID: 7, KnowledgeBaseID: "kb-1"},
		&types.FetchedItem{ExternalID: "doc-1", Content: []byte("new content"), FileName: "doc.md"},
		types.DataSourceSyncTags{})
	require.NoError(t, err)
	require.Equal(t, 1, queue.queued)
	require.NotContains(t, h.store.deleted, h.original.FilePath)
	var retired types.Knowledge
	require.NoError(t, db.Unscoped().First(&retired, "id = ?", h.original.ID).Error)
	require.True(t, retired.DeletedAt.Valid)
	created, err := h.svc.repo.FindByDataSourceExternalID(h.ctx, 7, "kb-1", "ds-1", "doc-1")
	require.NoError(t, err)
	require.NotNil(t, created)
}

type uncertainReplacementRepo struct {
	interfaces.KnowledgeRepository
	committed bool
	failReads bool
}

func (r *uncertainReplacementRepo) ReplaceKnowledge(
	ctx context.Context, before, after *types.Knowledge, tags types.DataSourceSyncTags,
) error {
	if err := r.KnowledgeRepository.ReplaceKnowledge(ctx, before, after, tags); err != nil {
		return err
	}
	r.committed = true
	return errors.New("commit acknowledgement lost")
}

func (r *uncertainReplacementRepo) GetKnowledgeByID(
	ctx context.Context, tenant uint64, id string,
) (*types.Knowledge, error) {
	if r.committed && r.failReads {
		return nil, errors.New("database unavailable")
	}
	return r.KnowledgeRepository.GetKnowledgeByID(ctx, tenant, id)
}

func TestDataSourceReplacementRetainsFileOnUncertainCommit(t *testing.T) {
	for _, failReads := range []bool{false, true} {
		t.Run(map[bool]string{false: "commit confirmed", true: "reads unavailable"}[failReads], func(t *testing.T) {
			h, _, chunks, queue := datasourceReplacementFixture(t)
			h.svc.repo = &uncertainReplacementRepo{KnowledgeRepository: h.svc.repo, failReads: failReads}
			ds := &DataSourceService{knowledgeService: h.svc}
			_, err := ds.ingestItem(h.ctx, &types.DataSource{ID: "ds-1", TenantID: 7, KnowledgeBaseID: "kb-1"},
				&types.FetchedItem{ExternalID: "doc-1", Content: []byte("new content"), FileName: "doc.md"},
				types.DataSourceSyncTags{})
			require.NotContains(t, h.store.deleted, "new/file.md", "a committed row may reference this file")
			if failReads {
				require.ErrorContains(t, err, "commit acknowledgement lost")
				require.Zero(t, queue.queued)
				require.Zero(t, chunks.deleted)
			} else {
				require.NoError(t, err)
				require.Equal(t, 1, queue.queued)
				require.Equal(t, 1, chunks.deleted)
			}
		})
	}
}
