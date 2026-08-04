package service

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

const knowledgeFolderServiceTestDDL = `
CREATE TABLE knowledge_folders (
    id TEXT PRIMARY KEY,
    tenant_id INTEGER NOT NULL,
    knowledge_base_id TEXT NOT NULL,
    parent_id TEXT,
    name TEXT NOT NULL,
    path TEXT NOT NULL,
    depth INTEGER NOT NULL,
    created_at DATETIME,
    updated_at DATETIME,
    deleted_at DATETIME
);
CREATE UNIQUE INDEX folders_root_name
    ON knowledge_folders (tenant_id, knowledge_base_id, name)
    WHERE parent_id IS NULL AND deleted_at IS NULL;
CREATE UNIQUE INDEX folders_sibling_name
    ON knowledge_folders (tenant_id, knowledge_base_id, parent_id, name)
    WHERE parent_id IS NOT NULL AND deleted_at IS NULL;
CREATE TABLE knowledges (
    id TEXT PRIMARY KEY,
    tenant_id INTEGER NOT NULL,
    knowledge_base_id TEXT NOT NULL,
    folder_id TEXT,
    updated_at DATETIME,
    deleted_at DATETIME
);
`

func setupKnowledgeFolderServiceTest(t *testing.T) (*gorm.DB, *knowledgeFolderService) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.Exec(knowledgeFolderServiceTestDDL).Error)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	return db, &knowledgeFolderService{
		repo: repository.NewKnowledgeFolderRepository(db),
	}
}

func TestKnowledgeFolderServiceCreateAndMoveTree(t *testing.T) {
	_, svc := setupKnowledgeFolderServiceTest(t)
	ctx := context.Background()

	root, err := svc.Create(ctx, 1, "kb-1", nil, "Root")
	require.NoError(t, err)
	child, err := svc.Create(ctx, 1, "kb-1", &root.ID, "Child")
	require.NoError(t, err)
	grandchild, err := svc.Create(ctx, 1, "kb-1", &child.ID, "Grandchild")
	require.NoError(t, err)

	_, err = svc.Update(ctx, 1, "kb-1", root.ID, nil, &grandchild.ID, true)
	require.ErrorIs(t, err, ErrKnowledgeFolderCycle)

	moved, err := svc.Update(ctx, 1, "kb-1", child.ID, nil, nil, true)
	require.NoError(t, err)
	assert.Nil(t, moved.ParentID)
	assert.Equal(t, "/"+child.ID, moved.Path)
	assert.Equal(t, 1, moved.Depth)

	folders, err := svc.List(ctx, 1, "kb-1")
	require.NoError(t, err)
	byID := make(map[string]*types.KnowledgeFolder, len(folders))
	for _, folder := range folders {
		byID[folder.ID] = folder
	}
	assert.Equal(t, "/"+child.ID+"/"+grandchild.ID, byID[grandchild.ID].Path)
	assert.Equal(t, 2, byID[grandchild.ID].Depth)
}

func TestKnowledgeFolderServiceRejectsSiblingConflict(t *testing.T) {
	_, svc := setupKnowledgeFolderServiceTest(t)
	ctx := context.Background()

	_, err := svc.Create(ctx, 1, "kb-1", nil, "Reports")
	require.NoError(t, err)
	_, err = svc.Create(ctx, 1, "kb-1", nil, "Reports")
	require.ErrorIs(t, err, ErrKnowledgeFolderConflict)
}

func TestKnowledgeFolderServiceMoveKnowledgeRollsBackPartialMatch(t *testing.T) {
	db, svc := setupKnowledgeFolderServiceTest(t)
	ctx := context.Background()
	folder, err := svc.Create(ctx, 1, "kb-1", nil, "Target")
	require.NoError(t, err)
	existingID := uuid.NewString()
	missingID := uuid.NewString()
	require.NoError(t, db.Exec(`
INSERT INTO knowledges (id, tenant_id, knowledge_base_id)
VALUES (?, 1, 'kb-1')
`, existingID).Error)

	_, err = svc.MoveKnowledge(
		ctx,
		1,
		"kb-1",
		[]string{existingID, missingID},
		&folder.ID,
	)
	require.ErrorIs(t, err, repository.ErrKnowledgeNotFound)

	var folderID *string
	require.NoError(t, db.Raw(
		"SELECT folder_id FROM knowledges WHERE id = ?",
		existingID,
	).Scan(&folderID).Error)
	assert.Nil(t, folderID)
}

func TestKnowledgeFolderServiceResolveSubtreeAndDeleteGuard(t *testing.T) {
	db, svc := setupKnowledgeFolderServiceTest(t)
	ctx := context.Background()
	root, err := svc.Create(ctx, 1, "kb-1", nil, "Root")
	require.NoError(t, err)
	child, err := svc.Create(ctx, 1, "kb-1", &root.ID, "Child")
	require.NoError(t, err)
	rootDocID := uuid.NewString()
	childDocID := uuid.NewString()
	require.NoError(t, db.Exec(`
INSERT INTO knowledges (id, tenant_id, knowledge_base_id, folder_id)
VALUES (?, 1, 'kb-1', ?), (?, 1, 'kb-1', ?)
`, rootDocID, root.ID, childDocID, child.ID).Error)

	ids, err := svc.ResolveKnowledgeIDs(ctx, 1, "kb-1", []string{root.ID})
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{rootDocID, childDocID}, ids)
	require.ErrorIs(t, svc.Delete(ctx, 1, "kb-1", root.ID), repository.ErrKnowledgeFolderNotEmpty)
}

func TestKnowledgeFolderServiceEmptyFolderResolvesToEmpty(t *testing.T) {
	_, svc := setupKnowledgeFolderServiceTest(t)
	ctx := context.Background()
	folder, err := svc.Create(ctx, 1, "kb-1", nil, "Empty")
	require.NoError(t, err)

	ids, err := svc.ResolveKnowledgeIDs(ctx, 1, "kb-1", []string{folder.ID})

	require.NoError(t, err)
	assert.Empty(t, ids)
}
