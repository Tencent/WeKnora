package repository

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func setupFolderRepositoryTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	require.True(t, ok)
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(filename), "..", "..", ".."))
	initSQL, err := os.ReadFile(filepath.Join(repoRoot, "migrations", "sqlite", "000000_init.up.sql"))
	require.NoError(t, err)
	folderSQL, err := os.ReadFile(filepath.Join(repoRoot, "migrations", "sqlite", "000071_add_folders.up.sql"))
	require.NoError(t, err)

	db, err := gorm.Open(sqlite.Open("file:"+uuid.NewString()+"?mode=memory&cache=shared&_foreign_keys=on"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, db.Exec(string(initSQL)).Error)
	require.NoError(t, db.Exec(string(folderSQL)).Error)
	require.NoError(t, db.Exec(`
		INSERT INTO tenants (id, name, business) VALUES
			(10001, 'tenant-1', 'test'), (10002, 'tenant-2', 'test');
		INSERT INTO knowledge_bases (id, name, tenant_id, embedding_model_id, summary_model_id) VALUES
			('kb-1', 'KB 1', 10001, 'embedding', 'summary'),
			('kb-2', 'KB 2', 10001, 'embedding', 'summary'),
			('kb-3', 'KB 3', 10002, 'embedding', 'summary');
	`).Error)
	return db
}

func folderStringPtr(value string) *string { return &value }

func createFolder(t *testing.T, repo interface {
	Create(context.Context, *types.Folder) error
}, folder *types.Folder) *types.Folder {
	t.Helper()
	require.NoError(t, repo.Create(context.Background(), folder))
	return folder
}

func insertFolderKnowledge(t *testing.T, db *gorm.DB, id string, tenantID uint64, kbID string, folderID *string, deleted bool) {
	t.Helper()
	var deletedAt any
	if deleted {
		deletedAt = time.Now()
	}
	require.NoError(t, db.Exec(`
		INSERT INTO knowledges (id, tenant_id, knowledge_base_id, folder_id, type, title, source, deleted_at)
		VALUES (?, ?, ?, ?, 'file', ?, ?, ?)
	`, id, tenantID, kbID, folderID, id, id+".txt", deletedAt).Error)
}

func TestFolderRepositoryScopedHierarchyAndRollback(t *testing.T) {
	db := setupFolderRepositoryTestDB(t)
	repo := NewFolderRepository(db)
	ctx := context.Background()
	root := createFolder(t, repo, &types.Folder{ID: "root", TenantID: 10001, KnowledgeBaseID: "kb-1", Name: "Root"})
	child := createFolder(t, repo, &types.Folder{ID: "child", TenantID: 10001, KnowledgeBaseID: "kb-1", ParentID: &root.ID, Name: "Child"})
	createFolder(t, repo, &types.Folder{ID: "other-kb", TenantID: 10001, KnowledgeBaseID: "kb-2", Name: "Other"})

	roots, err := repo.ListChildren(ctx, 10001, "kb-1", nil)
	require.NoError(t, err)
	require.Len(t, roots, 1)
	assert.Equal(t, root.ID, roots[0].ID)
	children, err := repo.ListChildren(ctx, 10001, "kb-1", &root.ID)
	require.NoError(t, err)
	require.Len(t, children, 1)
	assert.Equal(t, child.ID, children[0].ID)
	_, err = repo.GetByID(ctx, 10001, "kb-2", child.ID)
	assert.ErrorIs(t, err, ErrFolderNotFound)

	rollback := errors.New("rollback")
	err = repo.WithinTransaction(ctx, func(tx interfaces.FolderRepository) error {
		require.NoError(t, tx.UpdateName(ctx, 10001, "kb-1", child.ID, "Changed"))
		return rollback
	})
	assert.ErrorIs(t, err, rollback)
	unchanged, err := repo.GetByID(ctx, 10001, "kb-1", child.ID)
	require.NoError(t, err)
	assert.Equal(t, "Child", unchanged.Name)
}
