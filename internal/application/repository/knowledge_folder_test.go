package repository

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// insertKnowledgeInFolder seeds a completed knowledge row stored in the given
// folder so the folder filter and folder aggregation can be asserted.
func insertKnowledgeInFolder(t *testing.T, db *gorm.DB, tenantID uint64, kbID, folderPath, fileName string) string {
	t.Helper()
	id := uuid.New().String()
	require.NoError(t, db.Exec(`
		INSERT INTO knowledges (id, tenant_id, knowledge_base_id, type, title, source, parse_status, file_name, folder_path)
		VALUES (?, ?, ?, 'file', ?, 'manual', 'completed', ?, ?)
	`, id, tenantID, kbID, fileName, fileName, folderPath).Error)
	return id
}

func TestListPagedKnowledgeFolderScope(t *testing.T) {
	db := setupKnowledgeTestDB(t)
	repo := NewKnowledgeRepository(db).(*knowledgeRepository)
	ctx := context.Background()

	const tenantID = uint64(1)
	kbID := uuid.New().String()

	rootID := insertKnowledgeInFolder(t, db, tenantID, kbID, "", "readme.md")
	docsID := insertKnowledgeInFolder(t, db, tenantID, kbID, "docs", "intro.md")
	specID := insertKnowledgeInFolder(t, db, tenantID, kbID, "docs/spec", "design.md")
	otherID := insertKnowledgeInFolder(t, db, tenantID, kbID, "assets", "logo.png")

	page := &types.Pagination{Page: 1, PageSize: 100}
	list := func(filter types.KnowledgeListFilter) ([]string, int64) {
		rows, total, err := repo.ListPagedKnowledgeByKnowledgeBaseID(ctx, tenantID, kbID, page, filter)
		require.NoError(t, err)
		ids := make([]string, 0, len(rows))
		for _, row := range rows {
			ids = append(ids, row.ID)
		}
		return ids, total
	}

	// No folder scope: the flat listing is unchanged, which keeps every existing
	// caller (and the "all documents" view) working.
	ids, total := list(types.KnowledgeListFilter{})
	assert.ElementsMatch(t, []string{rootID, docsID, specID, otherID}, ids)
	assert.Equal(t, int64(4), total)

	// Exact scope with an empty path means "knowledge base root only".
	ids, total = list(types.KnowledgeListFilter{FolderScope: types.FolderScopeExact})
	assert.Equal(t, []string{rootID}, ids)
	assert.Equal(t, int64(1), total)

	ids, total = list(types.KnowledgeListFilter{FolderPath: "docs", FolderScope: types.FolderScopeExact})
	assert.Equal(t, []string{docsID}, ids)
	assert.Equal(t, int64(1), total)

	ids, total = list(types.KnowledgeListFilter{FolderPath: "docs", FolderScope: types.FolderScopeSubtree})
	assert.ElementsMatch(t, []string{docsID, specID}, ids)
	assert.Equal(t, int64(2), total)

	// Subtree scope at the root spans the whole knowledge base.
	ids, _ = list(types.KnowledgeListFilter{FolderScope: types.FolderScopeSubtree})
	assert.ElementsMatch(t, []string{rootID, docsID, specID, otherID}, ids)

	// Folder scope composes with the other filter dimensions.
	ids, _ = list(types.KnowledgeListFilter{
		FolderPath:  "docs",
		FolderScope: types.FolderScopeSubtree,
		Keyword:     "design",
	})
	assert.Equal(t, []string{specID}, ids)
}

func TestListPagedKnowledgeFolderScopeUnderscoreInPath(t *testing.T) {
	db := setupKnowledgeTestDB(t)
	repo := NewKnowledgeRepository(db).(*knowledgeRepository)
	ctx := context.Background()

	const tenantID = uint64(1)
	kbID := uuid.New().String()

	parentID := insertKnowledgeInFolder(t, db, tenantID, kbID, "my_docs", "intro.md")
	childID := insertKnowledgeInFolder(t, db, tenantID, kbID, "my_docs/spec", "design.md")
	// A sibling whose name merely shares a prefix must not be swept into subtree scope.
	lookalikeID := insertKnowledgeInFolder(t, db, tenantID, kbID, "myXdocs/spec", "other.md")

	page := &types.Pagination{Page: 1, PageSize: 100}
	rows, total, err := repo.ListPagedKnowledgeByKnowledgeBaseID(ctx, tenantID, kbID, page, types.KnowledgeListFilter{
		FolderPath:  "my_docs",
		FolderScope: types.FolderScopeSubtree,
	})
	require.NoError(t, err)
	ids := make([]string, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.ID)
	}
	assert.ElementsMatch(t, []string{parentID, childID}, ids)
	assert.Equal(t, int64(2), total)
	assert.NotContains(t, ids, lookalikeID)
}

func TestListKnowledgeFolderCounts(t *testing.T) {
	db := setupKnowledgeTestDB(t)
	repo := NewKnowledgeRepository(db).(*knowledgeRepository)
	ctx := context.Background()

	const tenantID = uint64(1)
	kbID := uuid.New().String()
	otherKBID := uuid.New().String()

	insertKnowledgeInFolder(t, db, tenantID, kbID, "", "readme.md")
	insertKnowledgeInFolder(t, db, tenantID, kbID, "docs/spec", "design.md")
	insertKnowledgeInFolder(t, db, tenantID, kbID, "docs/spec", "api.md")
	insertKnowledgeInFolder(t, db, tenantID, otherKBID, "docs/spec", "other-kb.md")
	// Rows mid-deletion are excluded so the tree counts agree with the list.
	deleting := insertKnowledgeInFolder(t, db, tenantID, kbID, "docs", "vanishing.md")
	require.NoError(t, db.Exec(`UPDATE knowledges SET parse_status = ? WHERE id = ?`,
		types.ParseStatusDeleting, deleting).Error)

	counts, err := repo.ListKnowledgeFolderCounts(ctx, tenantID, kbID)
	require.NoError(t, err)

	byPath := map[string]int64{}
	for _, row := range counts {
		byPath[row.FolderPath] = row.Count
	}
	assert.Equal(t, map[string]int64{"": 1, "docs/spec": 2}, byPath)

	tree := types.BuildKnowledgeFolderTree(counts)
	assert.Equal(t, int64(1), tree.RootDocumentCount)
	assert.Equal(t, int64(3), tree.TotalDocumentCount)
	require.Len(t, tree.Folders, 1)
	assert.Equal(t, "docs", tree.Folders[0].Path)
	assert.Equal(t, int64(2), tree.Folders[0].TotalCount)
}
