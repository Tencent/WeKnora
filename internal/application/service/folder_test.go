package service

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type folderServiceTestFixture struct {
	db      *gorm.DB
	service interfaces.FolderService
}

func setupFolderServiceTest(t *testing.T) folderServiceTestFixture {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	require.True(t, ok)
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(filename), "..", "..", ".."))
	initSQL, err := os.ReadFile(filepath.Join(repoRoot, "migrations", "sqlite", "000000_init.up.sql"))
	require.NoError(t, err)
	folderSQL, err := os.ReadFile(filepath.Join(repoRoot, "migrations", "sqlite", "000075_add_folders.up.sql"))
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
		INSERT INTO tenants (id, name, business) VALUES (10001, 'tenant-1', 'test'), (10002, 'tenant-2', 'test');
		INSERT INTO knowledge_bases (id, name, tenant_id, embedding_model_id, summary_model_id) VALUES
			('kb-1', 'KB 1', 10001, 'embedding', 'summary'),
			('kb-2', 'KB 2', 10001, 'embedding', 'summary'),
			('kb-3', 'KB 3', 10002, 'embedding', 'summary');
	`).Error)
	return folderServiceTestFixture{db: db, service: NewFolderService(repository.NewFolderRepository(db))}
}

func createServiceFolder(t *testing.T, service interfaces.FolderService, tenantID uint64, kbID string, parentID *string, name string) *types.Folder {
	t.Helper()
	folder, err := service.CreateFolder(context.Background(), tenantID, kbID, parentID, name)
	require.NoError(t, err)
	return folder
}

func TestFolderServiceCoreLifecycleAndSafety(t *testing.T) {
	fixture := setupFolderServiceTest(t)
	ctx := context.Background()
	root := createServiceFolder(t, fixture.service, 10001, "kb-1", nil, "  Root  ")
	child := createServiceFolder(t, fixture.service, 10001, "kb-1", &root.ID, "Child")
	otherKB := createServiceFolder(t, fixture.service, 10001, "kb-2", nil, "Other")
	assert.Equal(t, "Root", root.Name)

	_, err := fixture.service.CreateFolder(ctx, 10001, "kb-1", &root.ID, "Child")
	assert.ErrorIs(t, err, ErrFolderNameConflict)
	_, err = fixture.service.CreateFolder(ctx, 10001, "kb-1", nil, strings.Repeat("x", maxFolderNameLength+1))
	assert.ErrorIs(t, err, ErrInvalidFolderName)
	_, err = fixture.service.GetFolder(ctx, 10001, "kb-2", child.ID)
	assert.ErrorIs(t, err, ErrFolderNotFound)
	_, err = fixture.service.MoveFolder(ctx, 10001, "kb-1", child.ID, &otherKB.ID)
	assert.ErrorIs(t, err, ErrParentFolderNotFound)
	_, err = fixture.service.MoveFolder(ctx, 10001, "kb-1", root.ID, &child.ID)
	assert.ErrorIs(t, err, ErrFolderMoveCycle)

	renamed, err := fixture.service.RenameFolder(ctx, 10001, "kb-1", child.ID, "Renamed")
	require.NoError(t, err)
	assert.Equal(t, "Renamed", renamed.Name)
	moved, err := fixture.service.MoveFolder(ctx, 10001, "kb-1", child.ID, nil)
	require.NoError(t, err)
	assert.Nil(t, moved.ParentID)
	require.NoError(t, fixture.service.DeleteFolder(ctx, 10001, "kb-1", root.ID))

	require.NoError(t, fixture.db.Exec(`
		INSERT INTO knowledges (id, tenant_id, knowledge_base_id, folder_id, type, title, source)
		VALUES ('knowledge-1', 10001, 'kb-1', ?, 'file', 'Doc', 'doc.txt')
	`, child.ID).Error)
	err = fixture.service.DeleteFolder(ctx, 10001, "kb-1", child.ID)
	assert.ErrorIs(t, err, ErrFolderNotEmpty)
}
