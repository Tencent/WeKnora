package repository

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFindByMetadataKey_SQLiteUsesJSONPath(t *testing.T) {
	db := setupKnowledgeTestDB(t)
	repo := NewKnowledgeRepository(db).(*knowledgeRepository)
	ctx := context.Background()
	kbID := uuid.New().String()

	activeID := uuid.New().String()
	require.NoError(t, db.Exec(`
		INSERT INTO knowledges (
			id, tenant_id, knowledge_base_id, type, title, source,
			parse_status, metadata, deleted_at
		) VALUES (
			?, 1, ?, 'document', 'active', 'manual',
			'completed', '{"external_id":"remote-1"}', NULL
		)
	`, activeID, kbID).Error)
	require.NoError(t, db.Exec(`
		INSERT INTO knowledges (
			id, tenant_id, knowledge_base_id, type, title, source,
			parse_status, metadata, deleted_at
		) VALUES (
			?, 1, ?, 'document', 'deleted', 'manual',
			'completed', '{"external_id":"remote-1"}', '2026-07-24 10:00:00'
		)
	`, uuid.New().String(), kbID).Error)

	got, err := repo.FindByMetadataKey(ctx, 1, kbID, "external_id", "remote-1")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, activeID, got.ID)

	missing, err := repo.FindByMetadataKey(ctx, 1, kbID, "external_id", "missing")
	require.NoError(t, err)
	assert.Nil(t, missing)
}
