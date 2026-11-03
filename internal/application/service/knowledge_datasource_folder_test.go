package service

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestDataSourceReplacementKeepsLocalFolderAndEmptyChildren(t *testing.T) {
	h, db, chunks, queue := datasourceReplacementFixture(t)
	require.NoError(t, db.AutoMigrate(&types.KnowledgeFolder{}))
	require.NoError(
		t,
		db.Exec(
			`CREATE TABLE knowledge_bases (id TEXT PRIMARY KEY, tenant_id INTEGER NOT NULL, deleted_at DATETIME)`,
		).Error,
	)
	require.NoError(t, db.Exec(`INSERT INTO knowledge_bases(id, tenant_id) VALUES ('kb-1', 7)`).Error)
	repo := h.svc.repo
	// The upstream fixture already has a document in notes. Seed the explicit
	// marker to model a folder created before that upload.
	require.NoError(t, db.Create(&types.KnowledgeFolder{TenantID: 7, KnowledgeBaseID: "kb-1", Path: "notes"}).Error)
	require.NoError(t, repo.CreateKnowledgeFolder(h.ctx, 7, "kb-1", "notes/empty"))
	var originalMarkers []types.KnowledgeFolder
	require.NoError(t, db.Order("path").Find(&originalMarkers).Error)

	ds := &DataSourceService{knowledgeService: h.svc}
	updated, err := ds.ingestItem(h.ctx,
		&types.DataSource{ID: "ds-1", TenantID: 7, KnowledgeBaseID: "kb-1"},
		&types.FetchedItem{
			ExternalID: "doc-1",
			Content:    []byte("updated source"),
			FileName:   "upstream/new/doc.md",
		}, nil)
	require.NoError(t, err)
	require.True(t, updated)
	current, err := repo.FindByDataSourceExternalID(h.ctx, 7, "kb-1", "ds-1", "doc-1")
	require.NoError(t, err)
	require.NotNil(t, current)
	require.NotEqual(t, h.original.ID, current.ID, "preserve upstream atomic replace-ID semantics")
	require.Equal(t, "notes", current.FolderPath, "refresh must not apply an upstream folder move")
	require.Equal(t, "doc.md", current.FileName)
	require.Equal(t, 1, queue.queued)
	require.Equal(t, 1, chunks.deleted)
	var markers []types.KnowledgeFolder
	require.NoError(t, db.Order("path").Find(&markers).Error)
	require.Equal(t, originalMarkers, markers, "refresh keeps empty folders and creation timestamps")
}

func TestDataSourceReplacementRejectsRenameDuringStorageAndRetriesInNewFolder(t *testing.T) {
	h, db, chunks, queue := datasourceReplacementFixture(t)
	require.NoError(t, db.AutoMigrate(&types.KnowledgeFolder{}))
	require.NoError(
		t,
		db.Exec(
			`CREATE TABLE knowledge_bases (id TEXT PRIMARY KEY, tenant_id INTEGER NOT NULL, deleted_at DATETIME)`,
		).Error,
	)
	require.NoError(t, db.Exec(`INSERT INTO knowledge_bases(id, tenant_id) VALUES ('kb-1', 7)`).Error)
	repo := h.svc.repo
	// The upstream fixture already has a document in notes. Seed the explicit
	// marker to model a folder created before that upload.
	require.NoError(t, db.Create(&types.KnowledgeFolder{TenantID: 7, KnowledgeBaseID: "kb-1", Path: "notes"}).Error)
	require.NoError(t, repo.CreateKnowledgeFolder(h.ctx, 7, "kb-1", "notes/empty"))
	h.store.beforeSaveReturn = func() {
		moved, err := repo.RenameKnowledgeFolderPath(h.ctx, 7, "kb-1", "notes", "renamed")
		require.NoError(t, err)
		require.EqualValues(t, 1, moved)
	}
	ds := &DataSourceService{knowledgeService: h.svc}
	source := &types.DataSource{ID: "ds-1", TenantID: 7, KnowledgeBaseID: "kb-1"}
	item := &types.FetchedItem{ExternalID: "doc-1", Content: []byte("new source"), FileName: "upstream/old/doc.md"}
	updated, err := ds.ingestItem(h.ctx, source, item, nil)
	require.ErrorContains(t, err, "knowledge changed during replacement")
	require.False(t, updated)
	original, err := repo.GetKnowledgeByID(h.ctx, 7, h.original.ID)
	require.NoError(t, err)
	require.Equal(t, "renamed", original.FolderPath)
	require.Equal(t, h.original.FilePath, original.FilePath)
	require.Equal(t, types.ParseStatusCompleted, original.ParseStatus)
	require.Zero(t, chunks.deleted, "a lost CAS must not delete the old index")
	require.Zero(t, queue.queued)
	require.NotContains(t, h.store.deleted, h.original.FilePath)
	require.Equal(t, []string{"new/file.md"}, h.store.deleted)
	var count int64
	require.NoError(t, db.Model(&types.Knowledge{}).Count(&count).Error)
	require.EqualValues(t, 1, count, "failed refresh cannot create another live document")

	h.store.beforeSaveReturn = nil
	updated, err = ds.ingestItem(h.ctx, source, item, nil)
	require.NoError(t, err)
	require.True(t, updated)
	current, err := repo.FindByDataSourceExternalID(h.ctx, 7, "kb-1", "ds-1", "doc-1")
	require.NoError(t, err)
	require.NotNil(t, current)
	require.NotEqual(t, h.original.ID, current.ID)
	require.Equal(t, "renamed", current.FolderPath)
	require.Equal(t, 1, queue.queued)
	require.Equal(t, 1, chunks.deleted)
	var paths []string
	require.NoError(t, db.Model(&types.KnowledgeFolder{}).Order("path").Pluck("path", &paths).Error)
	require.Equal(t, []string{"renamed", "renamed/empty"}, paths)
}
