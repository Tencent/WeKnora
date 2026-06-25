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

const customAgentsTestDDL = `
CREATE TABLE IF NOT EXISTS custom_agents (
    id VARCHAR(36) NOT NULL,
    name VARCHAR(255) NOT NULL,
    description TEXT,
    avatar VARCHAR(64),
    is_builtin BOOLEAN NOT NULL DEFAULT 0,
    tenant_id INTEGER NOT NULL,
    created_by VARCHAR(36),
    config TEXT NOT NULL DEFAULT '{}',
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    deleted_at DATETIME,
    PRIMARY KEY (id, tenant_id)
);
`

func setupModelUsageTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db := setupKBTestDB(t)
	require.NoError(t, db.Exec(customAgentsTestDDL).Error)
	return db
}

func TestCountByModelID_KnowledgeBase(t *testing.T) {
	ctx := context.Background()
	db := setupModelUsageTestDB(t)
	repo := NewKnowledgeBaseRepository(db)
	modelID := "embed-model-1"

	kb := makeKB(nil)
	kb.EmbeddingModelID = modelID
	require.NoError(t, db.Create(kb).Error)

	count, err := repo.CountByModelID(ctx, 1, modelID)
	require.NoError(t, err)
	assert.Equal(t, int64(1), count)

	count, err = repo.CountByModelID(ctx, 1, "other-model")
	require.NoError(t, err)
	assert.Equal(t, int64(0), count)

	kb2 := makeKB(nil)
	kb2.ID = uuid.New().String()
	kb2.VLMConfig = types.VLMConfig{Enabled: true, ModelID: modelID}
	require.NoError(t, db.Create(kb2).Error)

	count, err = repo.CountByModelID(ctx, 1, modelID)
	require.NoError(t, err)
	assert.Equal(t, int64(2), count)

	require.NoError(t, db.Delete(kb2).Error)
	count, err = repo.CountByModelID(ctx, 1, modelID)
	require.NoError(t, err)
	assert.Equal(t, int64(1), count)
}

func TestCountByModelID_CustomAgent(t *testing.T) {
	ctx := context.Background()
	db := setupModelUsageTestDB(t)
	repo := NewCustomAgentRepository(db)
	modelID := "chat-model-1"

	agent := &types.CustomAgent{
		ID:       uuid.New().String(),
		Name:     "test-agent",
		TenantID: 1,
		Config: types.CustomAgentConfig{
			ModelID: modelID,
		},
	}
	require.NoError(t, repo.CreateAgent(ctx, agent))

	count, err := repo.CountByModelID(ctx, 1, modelID)
	require.NoError(t, err)
	assert.Equal(t, int64(1), count)

	agent2 := &types.CustomAgent{
		ID:       uuid.New().String(),
		Name:     "rerank-agent",
		TenantID: 1,
		Config: types.CustomAgentConfig{
			RerankModelID: modelID,
		},
	}
	require.NoError(t, repo.CreateAgent(ctx, agent2))

	count, err = repo.CountByModelID(ctx, 1, modelID)
	require.NoError(t, err)
	assert.Equal(t, int64(2), count)

	count, err = repo.CountByModelID(ctx, 2, modelID)
	require.NoError(t, err)
	assert.Equal(t, int64(0), count)

	require.NoError(t, repo.DeleteAgent(ctx, agent2.ID, 1))
	count, err = repo.CountByModelID(ctx, 1, modelID)
	require.NoError(t, err)
	assert.Equal(t, int64(1), count)
}
