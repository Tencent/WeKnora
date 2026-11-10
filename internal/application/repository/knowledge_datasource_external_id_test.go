package repository

import (
	"context"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFindByDataSourceExternalID(t *testing.T) {
	db := setupKnowledgeTestDB(t)
	repo := NewKnowledgeRepository(db).(*knowledgeRepository)
	ctx := context.Background()

	const tenantID uint64 = 50
	kbID := uuid.New().String()
	dsA := uuid.New().String()
	dsB := uuid.New().String()

	insertRow := func(dsID, extID string) string {
		id := uuid.New().String()
		metadata := fmt.Sprintf(`{"datasource_id":%q,"external_id":%q}`, dsID, extID)
		require.NoError(t, db.Exec(`
			INSERT INTO knowledges
			  (id, tenant_id, knowledge_base_id, type, title, source, parse_status, metadata)
			VALUES (?, ?, ?, 'document', ?, 'feishu', 'completed', ?)
		`, id, tenantID, kbID, extID, metadata).Error)
		return id
	}

	idA := insertRow(dsA, "file:shared")
	_ = insertRow(dsB, "file:shared")

	found, err := repo.FindByDataSourceExternalID(ctx, tenantID, kbID, dsA, "file:shared")
	require.NoError(t, err)
	require.NotNil(t, found)
	assert.Equal(t, idA, found.ID)
}

func TestFindByDataSourceExternalID_DeletedRowsExcluded(t *testing.T) {
	db := setupKnowledgeTestDB(t)
	repo := NewKnowledgeRepository(db).(*knowledgeRepository)
	ctx := context.Background()

	const tenantID uint64 = 51
	kbID := uuid.New().String()
	dsID := uuid.New().String()

	id := uuid.New().String()
	metadata := fmt.Sprintf(`{"datasource_id":%q,"external_id":%q}`, dsID, "file:gone")
	require.NoError(t, db.Exec(`
		INSERT INTO knowledges
		  (id, tenant_id, knowledge_base_id, type, title, source, parse_status, metadata, deleted_at)
		VALUES (?, ?, ?, 'document', 'gone', 'feishu', 'completed', ?, '2026-01-01 00:00:00')
	`, id, tenantID, kbID, metadata).Error)

	found, err := repo.FindByDataSourceExternalID(ctx, tenantID, kbID, dsID, "file:gone")
	require.NoError(t, err)
	assert.Nil(t, found)
}

func TestHardDeleteKnowledge(t *testing.T) {
	db := setupKnowledgeTestDB(t)
	repo := NewKnowledgeRepository(db).(*knowledgeRepository)
	ctx := context.Background()

	const tenantID uint64 = 52
	kbID := uuid.New().String()
	id := uuid.New().String()
	metadata := `{"datasource_id":"ds-1","external_id":"file:1"}`
	require.NoError(t, db.Exec(`
		INSERT INTO knowledges
		  (id, tenant_id, knowledge_base_id, type, title, source, parse_status, metadata, deleted_at)
		VALUES (?, ?, ?, 'document', 'file:1', 'feishu', 'completed', ?, '2026-01-01 00:00:00')
	`, id, tenantID, kbID, metadata).Error)

	require.NoError(t, repo.HardDeleteKnowledge(ctx, tenantID, id))

	var count int64
	require.NoError(t, db.Unscoped().Model(&struct{}{}).Table("knowledges").Where("id = ?", id).Count(&count).Error)
	assert.Equal(t, int64(0), count)
}
