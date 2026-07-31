package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

const knowledgeBaseDeleteKnowledgeTestDDL = `
CREATE TABLE knowledge_bases (
    id         VARCHAR(36) PRIMARY KEY,
    tenant_id  INTEGER NOT NULL,
    deleted_at DATETIME
);
CREATE TABLE knowledges (
    id                 VARCHAR(36) PRIMARY KEY,
    tenant_id          INTEGER NOT NULL,
    knowledge_base_id  VARCHAR(36) NOT NULL,
    parse_status       VARCHAR(32) NOT NULL DEFAULT 'completed',
    type               VARCHAR(50) NOT NULL DEFAULT '',
    embedding_model_id VARCHAR(64),
    file_path          TEXT,
    storage_size       BIGINT NOT NULL DEFAULT 0,
    created_at         DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at         DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted_at         DATETIME
);
CREATE TABLE knowledge_folder_index_pending (
    id                VARCHAR(36) PRIMARY KEY,
    tenant_id         INTEGER NOT NULL,
    knowledge_base_id VARCHAR(36) NOT NULL,
    knowledge_id      VARCHAR(36) NOT NULL,
    target_folder_id  VARCHAR(36) NOT NULL DEFAULT '',
    requested_version INTEGER NOT NULL CHECK (requested_version > 0),
    created_at        DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at        DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (tenant_id, knowledge_base_id, knowledge_id)
);
`

type knowledgeBaseDeleteKnowledgeRegistryStub struct {
	interfaces.RetrieveEngineRegistry
}

func (*knowledgeBaseDeleteKnowledgeRegistryStub) GetRetrieveEngineService(
	types.RetrieverEngineType,
) (interfaces.RetrieveEngineService, error) {
	return nil, errors.New("forced ordinary vector cleanup failure")
}

type knowledgeBaseDeleteKnowledgeChunkRepositoryStub struct {
	interfaces.ChunkRepository
	deletedKnowledgeIDs []string
}

func (*knowledgeBaseDeleteKnowledgeChunkRepositoryStub) ListImageInfoByKnowledgeIDs(
	context.Context,
	uint64,
	[]string,
) ([]interfaces.ChunkImageInfo, error) {
	return nil, nil
}

func (r *knowledgeBaseDeleteKnowledgeChunkRepositoryStub) DeleteChunksByKnowledgeID(
	_ context.Context,
	_ uint64,
	knowledgeID string,
) error {
	r.deletedKnowledgeIDs = append(r.deletedKnowledgeIDs, knowledgeID)
	return nil
}

func setupKnowledgeBaseDeleteKnowledgeTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := "file:" + uuid.NewString() + "?mode=memory&cache=shared&_busy_timeout=5000"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, db.Exec(knowledgeBaseDeleteKnowledgeTestDDL).Error)
	t.Cleanup(func() { _ = sqlDB.Close() })
	return db
}

func TestProcessKBDeleteCoordinatesKnowledgeDeleteUnderSoftDeletedKnowledgeBase(t *testing.T) {
	db := setupKnowledgeBaseDeleteKnowledgeTestDB(t)
	const (
		tenantID    = uint64(7)
		kbID        = "kb-1"
		knowledgeID = "knowledge-1"
	)
	require.NoError(t, db.Exec(
		`INSERT INTO knowledge_bases (id, tenant_id, deleted_at) VALUES (?, ?, ?)`,
		kbID,
		tenantID,
		time.Date(2026, 7, 25, 14, 0, 0, 0, time.UTC),
	).Error)
	require.NoError(t, db.Exec(`
		INSERT INTO knowledges (
			id,
			tenant_id,
			knowledge_base_id,
			parse_status,
			type,
			embedding_model_id,
			file_path,
			storage_size
		) VALUES (?, ?, ?, ?, '', '', '', 0)
	`, knowledgeID, tenantID, kbID, types.ParseStatusCompleted).Error)
	require.NoError(t, db.Exec(`
		INSERT INTO knowledge_folder_index_pending (
			id,
			tenant_id,
			knowledge_base_id,
			knowledge_id,
			requested_version
		) VALUES (?, ?, ?, ?, 1)
	`, uuid.NewString(), tenantID, kbID, knowledgeID).Error)

	chunkRepo := &knowledgeBaseDeleteKnowledgeChunkRepositoryStub{}
	svc := &knowledgeBaseService{
		kgRepo:         repository.NewKnowledgeRepository(db),
		chunkRepo:      chunkRepo,
		retrieveEngine: &knowledgeBaseDeleteKnowledgeRegistryStub{},
	}
	payload, err := json.Marshal(types.KBDeletePayload{
		TenantID:        tenantID,
		KnowledgeBaseID: kbID,
		EffectiveEngines: []types.RetrieverEngineParams{{
			RetrieverEngineType: types.PostgresRetrieverEngineType,
			RetrieverType:       types.VectorRetrieverType,
		}},
	})
	require.NoError(t, err)

	err = svc.ProcessKBDelete(
		context.Background(),
		asynq.NewTask(types.TypeKBDelete, payload),
	)

	require.NoError(t, err)
	var knowledge types.Knowledge
	require.NoError(t, db.Unscoped().
		Where("tenant_id = ? AND knowledge_base_id = ? AND id = ?", tenantID, kbID, knowledgeID).
		Take(&knowledge).Error)
	assert.Equal(t, types.ParseStatusDeleting, knowledge.ParseStatus)
	assert.True(t, knowledge.DeletedAt.Valid)

	var pendingCount int64
	require.NoError(t, db.Model(&types.KnowledgeFolderIndexPending{}).
		Where("tenant_id = ? AND knowledge_base_id = ? AND knowledge_id = ?", tenantID, kbID, knowledgeID).
		Count(&pendingCount).Error)
	assert.Zero(t, pendingCount)

	var knowledgeBase types.KnowledgeBase
	require.NoError(t, db.Unscoped().
		Where("tenant_id = ? AND id = ?", tenantID, kbID).
		Take(&knowledgeBase).Error)
	assert.True(t, knowledgeBase.DeletedAt.Valid)
	assert.Equal(t, []string{knowledgeID}, chunkRepo.deletedKnowledgeIDs)
}
