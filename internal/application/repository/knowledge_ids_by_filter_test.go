package repository

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestListKnowledgeIDsByFilter_MatchesListFilter(t *testing.T) {
	db := setupKnowledgeTestDB(t)
	repo := NewKnowledgeRepository(db).(*knowledgeRepository)
	ctx := context.Background()

	const tenantID = uint64(1)
	kbID := uuid.New().String()

	completedID := insertKnowledgeInKB(t, db, tenantID, kbID, "completed")
	failedID := insertKnowledgeInKB(t, db, tenantID, kbID, "failed")
	deletingID := insertKnowledgeInKB(t, db, tenantID, kbID, types.ParseStatusDeleting)

	ids, err := repo.ListKnowledgeIDsByFilter(ctx, tenantID, kbID, types.KnowledgeListFilter{})
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{completedID, failedID}, ids)
	assert.NotContains(t, ids, deletingID)

	ids, err = repo.ListKnowledgeIDsByFilter(ctx, tenantID, kbID, types.KnowledgeListFilter{
		ParseStatus: "completed",
	})
	require.NoError(t, err)
	assert.Equal(t, []string{completedID}, ids)
}
