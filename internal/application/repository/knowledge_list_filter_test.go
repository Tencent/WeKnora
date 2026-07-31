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

// insertKnowledgeInKB seeds a knowledge row in the given KB with the given
// parse_status so we can assert the list filter's visibility rules.
func insertKnowledgeInKB(t *testing.T, db *gorm.DB, tenantID uint64, kbID, status string) string {
	t.Helper()
	id := uuid.New().String()
	require.NoError(t, db.Exec(`
		INSERT INTO knowledges (id, tenant_id, knowledge_base_id, type, title, source, parse_status)
		VALUES (?, ?, ?, 'file', 'list-filter-test', 'manual', ?)
	`, id, tenantID, kbID, status).Error)
	return id
}

// TestListPaged_ExcludesDeletingByDefault documents the fix for issue #2192:
// a knowledge row that is mid-deletion (parse_status = 'deleting') must not
// appear in the default document list, otherwise a successful async delete
// keeps showing the entry until the row is physically gone. Rows in other
// states (including the terminal 'failed' a stuck delete resolves to) stay
// visible, and an explicit parse_status=deleting filter can still surface them.
func TestListPaged_ExcludesDeletingByDefault(t *testing.T) {
	db := setupKnowledgeTestDB(t)
	repo := NewKnowledgeRepository(db).(*knowledgeRepository)
	ctx := context.Background()

	const tenantID = uint64(1)
	kbID := uuid.New().String()

	completedID := insertKnowledgeInKB(t, db, tenantID, kbID, "completed")
	failedID := insertKnowledgeInKB(t, db, tenantID, kbID, "failed")
	deletingID := insertKnowledgeInKB(t, db, tenantID, kbID, types.ParseStatusDeleting)

	page := &types.Pagination{Page: 1, PageSize: 100}

	// Default listing (no explicit parse_status): deleting rows are hidden.
	rows, total, err := repo.ListPagedKnowledgeByKnowledgeBaseID(
		ctx, tenantID, kbID, page, types.KnowledgeListFilter{},
	)
	require.NoError(t, err)
	ids := make([]string, 0, len(rows))
	for _, r := range rows {
		ids = append(ids, r.ID)
	}
	assert.ElementsMatch(t, []string{completedID, failedID}, ids,
		"default list must include completed + failed but exclude deleting")
	assert.Equal(t, int64(2), total, "count must match the filtered rows")

	// Explicit parse_status=deleting still surfaces the in-flight row so
	// operators / tooling can inspect stuck deletes.
	rows, total, err = repo.ListPagedKnowledgeByKnowledgeBaseID(
		ctx, tenantID, kbID, page,
		types.KnowledgeListFilter{ParseStatus: types.ParseStatusDeleting},
	)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, deletingID, rows[0].ID)
	assert.Equal(t, int64(1), total)
}

// TestListPaged_FolderFilter locks the folder dimensions of the list filter:
// no folder filter returns everything, FolderRootSentinel narrows to
// root-level rows, a concrete folder id narrows to that folder, and a
// pre-expanded FolderIDs subtree takes precedence over FolderID.
func TestListPaged_FolderFilter(t *testing.T) {
	db := setupKnowledgeTestDB(t)
	repo := NewKnowledgeRepository(db).(*knowledgeRepository)
	ctx := context.Background()

	const tenantID = uint64(1)
	kbID := uuid.New().String()

	insertInFolder := func(folderID string) string {
		id := uuid.New().String()
		require.NoError(t, db.Exec(`
			INSERT INTO knowledges (id, tenant_id, knowledge_base_id, type, title, source, parse_status, folder_id)
			VALUES (?, ?, ?, 'file', 'folder-filter-test', 'manual', 'completed', ?)
		`, id, tenantID, kbID, folderID).Error)
		return id
	}
	rootID := insertInFolder("")
	inA := insertInFolder("folder-a")
	inB := insertInFolder("folder-b")

	page := &types.Pagination{Page: 1, PageSize: 100}
	list := func(filter types.KnowledgeListFilter) []string {
		rows, _, err := repo.ListPagedKnowledgeByKnowledgeBaseID(ctx, tenantID, kbID, page, filter)
		require.NoError(t, err)
		ids := make([]string, 0, len(rows))
		for _, r := range rows {
			ids = append(ids, r.ID)
		}
		return ids
	}

	assert.ElementsMatch(t, []string{rootID, inA, inB}, list(types.KnowledgeListFilter{}),
		"no folder filter must return all rows")
	assert.ElementsMatch(t, []string{rootID}, list(types.KnowledgeListFilter{FolderID: types.FolderRootSentinel}),
		"root sentinel must narrow to root-level rows")
	assert.ElementsMatch(t, []string{inA}, list(types.KnowledgeListFilter{FolderID: "folder-a"}),
		"concrete folder id must narrow to that folder")
	assert.ElementsMatch(t, []string{inA, inB},
		list(types.KnowledgeListFilter{FolderID: "folder-a", FolderIDs: []string{"folder-a", "folder-b"}}),
		"pre-expanded subtree list must take precedence over the single id")
}
