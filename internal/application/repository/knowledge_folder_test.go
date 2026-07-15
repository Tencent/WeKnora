package repository

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func setupKnowledgeFolderDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&types.KnowledgeFolder{}, &types.KnowledgeFolderClosure{}, &types.Knowledge{}))
	return db
}

func TestKnowledgeFolderClosureCreateMoveAndCycle(t *testing.T) {
	db := setupKnowledgeFolderDB(t)
	repo := NewKnowledgeFolderRepository(db).(*knowledgeFolderRepository)
	ctx := context.Background()

	a, err := repo.Create(ctx, 1, "kb-1", "", "A")
	require.NoError(t, err)
	b, err := repo.Create(ctx, 1, "kb-1", a.ID, "B")
	require.NoError(t, err)
	c, err := repo.Create(ctx, 1, "kb-1", b.ID, "C")
	require.NoError(t, err)
	d, err := repo.Create(ctx, 1, "kb-1", "", "D")
	require.NoError(t, err)

	var depth int
	require.NoError(t, db.Model(&types.KnowledgeFolderClosure{}).Select("depth").Where("ancestor_id = ? AND descendant_id = ?", a.ID, c.ID).Scan(&depth).Error)
	assert.Equal(t, 2, depth)

	newParent := d.ID
	_, err = repo.Update(ctx, 1, "kb-1", b.ID, nil, &newParent)
	require.NoError(t, err)
	var oldLinks int64
	require.NoError(t, db.Model(&types.KnowledgeFolderClosure{}).Where("ancestor_id = ? AND descendant_id IN ?", a.ID, []string{b.ID, c.ID}).Count(&oldLinks).Error)
	assert.Zero(t, oldLinks)
	require.NoError(t, db.Model(&types.KnowledgeFolderClosure{}).Select("depth").Where("ancestor_id = ? AND descendant_id = ?", d.ID, c.ID).Scan(&depth).Error)
	assert.Equal(t, 2, depth)

	cycleParent := c.ID
	_, err = repo.Update(ctx, 1, "kb-1", d.ID, nil, &cycleParent)
	assert.ErrorIs(t, err, ErrKnowledgeFolderCycle)
}

func TestKnowledgeFolderDepthConflictCountsAndDelete(t *testing.T) {
	db := setupKnowledgeFolderDB(t)
	repo := NewKnowledgeFolderRepository(db).(*knowledgeFolderRepository)
	ctx := context.Background()
	parent := ""
	var first *types.KnowledgeFolder
	for i := 1; i <= types.MaxKnowledgeFolderDepth; i++ {
		folder, err := repo.Create(ctx, 1, "kb-1", parent, fmt.Sprintf("level-%02d", i))
		require.NoError(t, err)
		if first == nil {
			first = folder
		}
		parent = folder.ID
	}
	_, err := repo.Create(ctx, 1, "kb-1", parent, "too-deep")
	assert.ErrorIs(t, err, ErrKnowledgeFolderTooDeep)
	_, err = repo.Create(ctx, 1, "kb-1", "", first.Name)
	assert.ErrorIs(t, err, ErrKnowledgeFolderConflict)

	doc := &types.Knowledge{ID: "doc-1", TenantID: 1, KnowledgeBaseID: "kb-1", FolderID: parent, Type: "file", Title: "doc", Source: "doc", ParseStatus: "completed", EnableStatus: "enabled"}
	require.NoError(t, db.Create(doc).Error)
	view, err := repo.Get(ctx, 1, "kb-1", first.ID)
	require.NoError(t, err)
	assert.EqualValues(t, 1, view.TotalKnowledgeCount)
	assert.EqualValues(t, 0, view.DirectKnowledgeCount)
	assert.True(t, view.HasChildren)
	assert.ErrorIs(t, repo.Delete(ctx, 1, "kb-1", first.ID), ErrKnowledgeFolderNotEmpty)
	assert.ErrorIs(t, repo.Delete(ctx, 1, "kb-1", parent), ErrKnowledgeFolderNotEmpty)
}

func TestKnowledgeFolderEnsurePathsAndScopedBatchMove(t *testing.T) {
	db := setupKnowledgeFolderDB(t)
	repo := NewKnowledgeFolderRepository(db).(*knowledgeFolderRepository)
	ctx := context.Background()
	paths := []types.EnsureFolderPath{{ClientKey: "one", Segments: []string{"src", "api"}}, {ClientKey: "two", Segments: []string{"src", "web"}}, {ClientKey: "again", Segments: []string{"src", "api"}}}
	result, err := repo.EnsurePaths(ctx, 1, "kb-1", "", paths)
	require.NoError(t, err)
	require.Len(t, result, 3)
	assert.Equal(t, result[0].FolderID, result[2].FolderID)
	var folders int64
	require.NoError(t, db.Model(&types.KnowledgeFolder{}).Count(&folders).Error)
	assert.EqualValues(t, 3, folders)

	require.NoError(t, db.Create(&types.Knowledge{ID: "doc-1", TenantID: 1, KnowledgeBaseID: "kb-1", Type: "file", Title: "one", Source: "one", ParseStatus: "completed", EnableStatus: "enabled"}).Error)
	require.NoError(t, db.Create(&types.Knowledge{ID: "doc-2", TenantID: 1, KnowledgeBaseID: "kb-2", Type: "file", Title: "two", Source: "two", ParseStatus: "completed", EnableStatus: "enabled"}).Error)
	err = repo.MoveKnowledge(ctx, 1, "kb-1", []string{"doc-1", "doc-2"}, result[0].FolderID)
	assert.True(t, errors.Is(err, ErrKnowledgeFolderScope))
	var folderID string
	require.NoError(t, db.Model(&types.Knowledge{}).Select("folder_id").Where("id = ?", "doc-1").Scan(&folderID).Error)
	assert.Empty(t, folderID, "validation must happen before the batch update")
	require.NoError(t, repo.MoveKnowledge(ctx, 1, "kb-1", []string{"doc-1"}, result[0].FolderID))
	require.NoError(t, db.Model(&types.Knowledge{}).Select("folder_id").Where("id = ?", "doc-1").Scan(&folderID).Error)
	assert.Equal(t, result[0].FolderID, folderID)
}

func TestKnowledgeFolderMoveBatchesBoundSQLParameters(t *testing.T) {
	ids := make([]string, maxKnowledgeFolderMoveBatchSize*2+1)
	for i := range ids {
		ids[i] = fmt.Sprintf("doc-%04d", i)
	}
	batches := knowledgeFolderMoveBatches(ids)
	require.Len(t, batches, 3)
	assert.Len(t, batches[0], maxKnowledgeFolderMoveBatchSize)
	assert.Len(t, batches[1], maxKnowledgeFolderMoveBatchSize)
	assert.Len(t, batches[2], 1)
	assert.Equal(t, ids, append(append(append([]string{}, batches[0]...), batches[1]...), batches[2]...))
}

func TestKnowledgeFolderKeywordSearchReturnsAncestorPaths(t *testing.T) {
	db := setupKnowledgeFolderDB(t)
	repo := NewKnowledgeFolderRepository(db).(*knowledgeFolderRepository)
	ctx := context.Background()

	root, err := repo.Create(ctx, 1, "kb-1", "", "Root")
	require.NoError(t, err)
	parent, err := repo.Create(ctx, 1, "kb-1", root.ID, "Parent")
	require.NoError(t, err)
	child, err := repo.Create(ctx, 1, "kb-1", parent.ID, "Needle")
	require.NoError(t, err)
	_, err = repo.Create(ctx, 2, "kb-1", "", "Foreign Needle")
	require.NoError(t, err)

	rows, total, err := repo.List(ctx, 1, "kb-1", "", "needle", &types.Pagination{Page: 1, PageSize: 20})
	require.NoError(t, err)
	require.EqualValues(t, 1, total)
	require.Len(t, rows, 1)
	require.Equal(t, child.ID, rows[0].ID)
	require.Len(t, rows[0].Ancestors, 2)
	require.Equal(t, []string{root.ID, parent.ID}, []string{rows[0].Ancestors[0].ID, rows[0].Ancestors[1].ID})
}

func TestTranslateKnowledgeFolderUniqueViolation(t *testing.T) {
	err := errors.New(`UNIQUE constraint failed: knowledge_folders.tenant_id, knowledge_folders.knowledge_base_id, knowledge_folders.parent_id, knowledge_folders.name`)
	require.ErrorIs(t, translateKnowledgeFolderWriteError(err), ErrKnowledgeFolderConflict)

	unrelated := errors.New(`UNIQUE constraint failed: users.email`)
	require.Same(t, unrelated, translateKnowledgeFolderWriteError(unrelated))
}

func TestKnowledgeListFolderFiltering(t *testing.T) {
	db := setupKnowledgeFolderDB(t)
	folderRepo := NewKnowledgeFolderRepository(db).(*knowledgeFolderRepository)
	knowledgeRepo := NewKnowledgeRepository(db).(*knowledgeRepository)
	ctx := context.Background()

	parent, err := folderRepo.Create(ctx, 1, "kb-1", "", "Parent")
	require.NoError(t, err)
	child, err := folderRepo.Create(ctx, 1, "kb-1", parent.ID, "Child")
	require.NoError(t, err)
	sibling, err := folderRepo.Create(ctx, 1, "kb-1", "", "Sibling")
	require.NoError(t, err)

	documents := []*types.Knowledge{
		{ID: "root", TenantID: 1, KnowledgeBaseID: "kb-1", FolderID: "", Type: "file", Title: "root report", FileName: "root.txt", Source: "root", ParseStatus: "completed", EnableStatus: "enabled"},
		{ID: "parent", TenantID: 1, KnowledgeBaseID: "kb-1", FolderID: parent.ID, Type: "file", Title: "parent report", FileName: "parent.txt", Source: "parent", ParseStatus: "completed", EnableStatus: "enabled"},
		{ID: "child", TenantID: 1, KnowledgeBaseID: "kb-1", FolderID: child.ID, Type: "file", Title: "child report", FileName: "child.txt", Source: "child", ParseStatus: "completed", EnableStatus: "enabled"},
		{ID: "sibling", TenantID: 1, KnowledgeBaseID: "kb-1", FolderID: sibling.ID, Type: "file", Title: "unrelated", FileName: "sibling.txt", Source: "sibling", ParseStatus: "completed", EnableStatus: "enabled"},
	}
	require.NoError(t, db.Create(&documents).Error)
	page := &types.Pagination{Page: 1, PageSize: 20}

	rows, total, err := knowledgeRepo.ListPagedKnowledgeByKnowledgeBaseID(ctx, 1, "kb-1", page, types.KnowledgeListFilter{})
	require.NoError(t, err)
	require.EqualValues(t, 4, total)
	require.Len(t, rows, 4)

	rows, total, err = knowledgeRepo.ListPagedKnowledgeByKnowledgeBaseID(ctx, 1, "kb-1", page, types.KnowledgeListFilter{FolderSet: true})
	require.NoError(t, err)
	require.EqualValues(t, 1, total)
	require.Equal(t, "root", rows[0].ID)

	rows, total, err = knowledgeRepo.ListPagedKnowledgeByKnowledgeBaseID(ctx, 1, "kb-1", page, types.KnowledgeListFilter{FolderSet: true, FolderID: parent.ID})
	require.NoError(t, err)
	require.EqualValues(t, 1, total)
	require.Equal(t, "parent", rows[0].ID)

	rows, total, err = knowledgeRepo.ListPagedKnowledgeByKnowledgeBaseID(ctx, 1, "kb-1", page, types.KnowledgeListFilter{
		FolderSet: true, FolderID: parent.ID, IncludeDescendants: true, Keyword: "child",
	})
	require.NoError(t, err)
	require.EqualValues(t, 1, total)
	require.Equal(t, "child", rows[0].ID)
}
