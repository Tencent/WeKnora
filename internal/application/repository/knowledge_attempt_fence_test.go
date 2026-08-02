package repository

import (
	"context"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestUpdateKnowledgeIfAttemptCurrentIsDatabaseFenced(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&types.Knowledge{}))
	require.NoError(t, db.Exec(`
		CREATE TABLE knowledge_processing_spans (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			knowledge_id TEXT NOT NULL,
			attempt INTEGER NOT NULL,
			kind TEXT NOT NULL
		)
	`).Error)

	knowledge := &types.Knowledge{
		ID:              uuid.New().String(),
		TenantID:        1,
		KnowledgeBaseID: uuid.New().String(),
		Type:            types.KnowledgeTypeManual,
		Title:           "before",
		Source:          "manual",
		ParseStatus:     types.ParseStatusProcessing,
		EnableStatus:    "enabled",
	}
	require.NoError(t, db.Create(knowledge).Error)
	repository := &knowledgeRepository{db: db}

	knowledge.Title = "attempt one"
	published, err := repository.UpdateKnowledgeIfAttemptCurrent(
		context.Background(),
		knowledge,
		1,
	)
	require.NoError(t, err)
	require.True(t, published)

	require.NoError(t, db.Exec(`
		INSERT INTO knowledge_processing_spans (knowledge_id, attempt, kind)
		VALUES (?, 2, ?)
	`, knowledge.ID, types.SpanKindRoot).Error)
	knowledge.Title = "stale attempt one"
	published, err = repository.UpdateKnowledgeIfAttemptCurrent(
		context.Background(),
		knowledge,
		1,
	)
	require.NoError(t, err)
	assert.False(t, published)

	var stored types.Knowledge
	require.NoError(t, db.First(&stored, "id = ?", knowledge.ID).Error)
	assert.Equal(t, "attempt one", stored.Title)

	knowledge.Title = "attempt two"
	published, err = repository.UpdateKnowledgeIfAttemptCurrent(
		context.Background(),
		knowledge,
		2,
	)
	require.NoError(t, err)
	assert.True(t, published)
	require.NoError(t, db.First(&stored, "id = ?", knowledge.ID).Error)
	assert.Equal(t, "attempt two", stored.Title)

	published, err = repository.UpdateKnowledgeColumnsIfAttemptCurrent(
		context.Background(),
		knowledge.TenantID,
		knowledge.ID,
		1,
		map[string]interface{}{"pending_subtasks_count": 7},
	)
	require.NoError(t, err)
	assert.False(t, published)

	published, err = repository.UpdateKnowledgeColumnsIfAttemptCurrent(
		context.Background(),
		knowledge.TenantID,
		knowledge.ID,
		2,
		map[string]interface{}{"pending_subtasks_count": 3},
	)
	require.NoError(t, err)
	assert.True(t, published)
	require.NoError(t, db.First(&stored, "id = ?", knowledge.ID).Error)
	assert.Equal(t, 3, stored.PendingSubtasksCount)
}

func TestPublishKnowledgeIfAttemptCurrentAtomicallyAdjustsStorageOnce(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+uuid.NewString()+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&types.Knowledge{}, &types.Tenant{}))
	require.NoError(t, db.Exec(`
		CREATE TABLE knowledge_processing_spans (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			knowledge_id TEXT NOT NULL,
			attempt INTEGER NOT NULL,
			kind TEXT NOT NULL
		)
	`).Error)

	tenant := &types.Tenant{ID: 7, Name: "tenant", StorageUsed: 1_000}
	require.NoError(t, db.Create(tenant).Error)
	knowledge := &types.Knowledge{
		ID:              uuid.NewString(),
		TenantID:        tenant.ID,
		KnowledgeBaseID: uuid.NewString(),
		Type:            types.KnowledgeTypeManual,
		Source:          "manual",
		ParseStatus:     types.ParseStatusProcessing,
		EnableStatus:    "enabled",
		StorageSize:     20,
	}
	require.NoError(t, db.Create(knowledge).Error)
	repository := &knowledgeRepository{db: db}

	knowledge.StorageSize = 50
	knowledge.UpdatedAt = time.Now()
	published, err := repository.PublishKnowledgeIfAttemptCurrent(
		context.Background(),
		knowledge,
		1,
	)
	require.NoError(t, err)
	require.True(t, published)

	var storedTenant types.Tenant
	require.NoError(t, db.First(&storedTenant, tenant.ID).Error)
	assert.Equal(t, int64(1_030), storedTenant.StorageUsed)

	// A worker retry after crashing immediately after publish must not charge
	// the same 30-byte generation a second time.
	knowledge.UpdatedAt = time.Now().Add(time.Second)
	published, err = repository.PublishKnowledgeIfAttemptCurrent(
		context.Background(),
		knowledge,
		1,
	)
	require.NoError(t, err)
	require.True(t, published)
	require.NoError(t, db.First(&storedTenant, tenant.ID).Error)
	assert.Equal(t, int64(1_030), storedTenant.StorageUsed)

	require.NoError(t, db.Exec(`
		INSERT INTO knowledge_processing_spans (knowledge_id, attempt, kind)
		VALUES (?, 2, ?)
	`, knowledge.ID, types.SpanKindRoot).Error)
	knowledge.StorageSize = 80
	published, err = repository.PublishKnowledgeIfAttemptCurrent(
		context.Background(),
		knowledge,
		1,
	)
	require.NoError(t, err)
	assert.False(t, published)
	require.NoError(t, db.First(&storedTenant, tenant.ID).Error)
	assert.Equal(t, int64(1_030), storedTenant.StorageUsed)
}
