package repository

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCountKnowledgeByParseStatusScopesAndExcludesDeleting(t *testing.T) {
	db := setupKnowledgeTestDB(t)
	repo := NewKnowledgeRepository(db).(*knowledgeRepository)
	ctx := context.Background()

	const tenantID = uint64(1)
	kbID := uuid.New().String()

	insertKnowledgeInKB(t, db, tenantID, kbID, types.ParseStatusPending)
	insertKnowledgeInKB(t, db, tenantID, kbID, types.ParseStatusProcessing)
	insertKnowledgeInKB(t, db, tenantID, kbID, types.ParseStatusFinalizing)
	insertKnowledgeInKB(t, db, tenantID, kbID, types.ParseStatusCompleted)
	insertKnowledgeInKB(t, db, tenantID, kbID, types.ParseStatusCompleted)
	insertKnowledgeInKB(t, db, tenantID, kbID, types.ParseStatusFailed)
	insertKnowledgeInKB(t, db, tenantID, kbID, types.ParseStatusCancelled)
	insertKnowledgeInKB(t, db, tenantID, kbID, types.ManualKnowledgeStatusDraft)
	insertKnowledgeInKB(t, db, tenantID, kbID, types.ParseStatusDeleting)

	// These rows must not leak into the requested workspace and KB.
	insertKnowledgeInKB(t, db, tenantID+1, kbID, types.ParseStatusFailed)
	insertKnowledgeInKB(t, db, tenantID, uuid.New().String(), types.ParseStatusFailed)

	got, err := repo.CountKnowledgeByParseStatus(ctx, tenantID, kbID)
	require.NoError(t, err)
	assert.Equal(t, map[string]int64{
		types.ParseStatusPending:         1,
		types.ParseStatusProcessing:      1,
		types.ParseStatusFinalizing:      1,
		types.ParseStatusCompleted:       2,
		types.ParseStatusFailed:          1,
		types.ParseStatusCancelled:       1,
		types.ManualKnowledgeStatusDraft: 1,
	}, got)
}
