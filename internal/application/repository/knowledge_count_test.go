package repository

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/types"
)

// TestCountKnowledgeByKnowledgeBaseID_ExcludesDeleting is the regression
// test for stranded `deleting` rows inflating the displayed knowledge base
// count: ListKnowledgeFolderCounts already excludes rows mid-deletion, but
// CountKnowledgeByKnowledgeBaseID (the query feeding KnowledgeBase.KnowledgeCount)
// did not, so a row stuck in `deleting` state kept counting toward the total.
func TestCountKnowledgeByKnowledgeBaseID_ExcludesDeleting(t *testing.T) {
	db := setupKnowledgeTestDB(t)
	repo := NewKnowledgeRepository(db).(*knowledgeRepository)
	ctx := context.Background()

	const tenantID = uint64(1)
	kbID := uuid.New().String()

	insertKnowledgeInFolder(t, db, tenantID, kbID, "", "readme.md")
	insertKnowledgeInFolder(t, db, tenantID, kbID, "docs", "intro.md")
	deleting := insertKnowledgeInFolder(t, db, tenantID, kbID, "docs", "vanishing.md")
	require.NoError(t, db.Exec(`UPDATE knowledges SET parse_status = ? WHERE id = ?`,
		types.ParseStatusDeleting, deleting).Error)

	count, err := repo.CountKnowledgeByKnowledgeBaseID(ctx, tenantID, kbID)
	require.NoError(t, err)
	require.Equal(t, int64(2), count)
}
