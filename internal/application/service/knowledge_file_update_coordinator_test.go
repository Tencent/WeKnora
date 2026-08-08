package service

import (
	"context"
	"encoding/json"
	"testing"

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

type fileUpdateTenantRepoStub struct {
	interfaces.TenantRepository
}

func (fileUpdateTenantRepoStub) GetTenantByID(context.Context, uint64) (*types.Tenant, error) {
	return &types.Tenant{ID: 1}, nil
}

func setupFileUpdateCoordinatorRepo(t *testing.T) (*gorm.DB, interfaces.KnowledgeRepository) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+uuid.NewString()+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, db.Exec(`
		CREATE TABLE knowledges (
			id VARCHAR(36) PRIMARY KEY,
			tenant_id INTEGER NOT NULL,
			knowledge_base_id VARCHAR(36) NOT NULL,
			type VARCHAR(50) NOT NULL,
			parse_status VARCHAR(50) NOT NULL,
			file_path TEXT,
			file_hash VARCHAR(64),
			deleted_at DATETIME
		);
		CREATE TABLE knowledge_file_update_slots (
			knowledge_id VARCHAR(36) PRIMARY KEY,
			tenant_id INTEGER NOT NULL,
			knowledge_base_id VARCHAR(36) NOT NULL,
			latest_version INTEGER NOT NULL DEFAULT 0,
			active_version INTEGER,
			active_state VARCHAR(16) NOT NULL DEFAULT 'idle',
			active_payload TEXT,
			pending_version INTEGER,
			pending_payload TEXT,
			last_error TEXT NOT NULL DEFAULT '',
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		);
	`).Error)
	return db, repository.NewKnowledgeRepository(db)
}

func TestProcessKnowledgeFileUpdateDefersWhileCurrentVersionIsProcessing(t *testing.T) {
	db, repo := setupFileUpdateCoordinatorRepo(t)
	knowledgeID := uuid.NewString()
	kbID := uuid.NewString()
	require.NoError(t, db.Exec(`
		INSERT INTO knowledges(id, tenant_id, knowledge_base_id, type, parse_status, file_path, file_hash)
		VALUES (?, 1, ?, 'file', ?, 'current/path.md', 'current-hash')
	`, knowledgeID, kbID, types.ParseStatusProcessing).Error)
	active, err := json.Marshal(types.KnowledgeFileUpdatePayload{
		TenantID: 1, KnowledgeBaseID: kbID, KnowledgeID: knowledgeID,
		NewFilePath: "staged/latest.md", NewFileName: "latest.md", NewFileHash: "latest-hash",
	})
	require.NoError(t, err)
	staged, err := repo.StageKnowledgeFileUpdate(
		context.Background(), 1, knowledgeID, kbID, types.JSON(active), nil,
	)
	require.NoError(t, err)

	taskQueue := &fileUpdateTaskStub{}
	svc := &knowledgeService{
		repo:       repo,
		tenantRepo: fileUpdateTenantRepoStub{},
		kbService:  &createKnowledgeFileKBServiceStub{kb: &types.KnowledgeBase{ID: kbID}},
		task:       taskQueue,
	}
	wake, err := json.Marshal(types.KnowledgeFileUpdateTaskPayload{
		TenantID: 1, KnowledgeBaseID: kbID, KnowledgeID: knowledgeID, ActiveVersion: staged.ActiveVersion,
	})
	require.NoError(t, err)

	err = svc.ProcessKnowledgeFileUpdate(
		context.Background(), asynq.NewTask(types.TypeKnowledgeFileUpdate, wake),
	)
	require.NoError(t, err)

	slot, err := repo.GetKnowledgeFileUpdateSlot(context.Background(), 1, knowledgeID)
	require.NoError(t, err)
	assert.Equal(t, types.KnowledgeFileUpdateStateRetryWait, slot.ActiveState)
	require.Len(t, taskQueue.tasks, 1)
	assert.Equal(t, types.TypeKnowledgeFileUpdate, taskQueue.tasks[0].Type())
	var delayed types.KnowledgeFileUpdateTaskPayload
	require.NoError(t, json.Unmarshal(taskQueue.tasks[0].Payload(), &delayed))
	assert.Equal(t, uint64(1), delayed.WakeSequence,
		"the delayed wake must not share the currently executing task's unique fingerprint")

	var status string
	require.NoError(t, db.Raw(`SELECT parse_status FROM knowledges WHERE id = ?`, knowledgeID).Scan(&status).Error)
	assert.Equal(t, types.ParseStatusProcessing, status)
}

func TestProcessKnowledgeFileUpdateStaleWakeRearmsCurrentActive(t *testing.T) {
	db, repo := setupFileUpdateCoordinatorRepo(t)
	knowledgeID := uuid.NewString()
	kbID := uuid.NewString()
	require.NoError(t, db.Exec(`
		INSERT INTO knowledges(id, tenant_id, knowledge_base_id, type, parse_status, file_path, file_hash)
		VALUES (?, 1, ?, 'file', ?, 'current/path.md', 'current-hash')
	`, knowledgeID, kbID, types.ParseStatusCompleted).Error)
	firstPayload, err := json.Marshal(types.KnowledgeFileUpdatePayload{
		TenantID: 1, KnowledgeBaseID: kbID, KnowledgeID: knowledgeID,
		NewFilePath: "staged/first.md", NewFileHash: "first-hash",
	})
	require.NoError(t, err)
	first, err := repo.StageKnowledgeFileUpdate(
		context.Background(), 1, knowledgeID, kbID, types.JSON(firstPayload), nil,
	)
	require.NoError(t, err)
	latestPayload, err := json.Marshal(types.KnowledgeFileUpdatePayload{
		TenantID: 1, KnowledgeBaseID: kbID, KnowledgeID: knowledgeID,
		NewFilePath: "staged/latest.md", NewFileHash: "latest-hash",
	})
	require.NoError(t, err)
	latest, err := repo.StageKnowledgeFileUpdate(
		context.Background(), 1, knowledgeID, kbID, types.JSON(latestPayload), nil,
	)
	require.NoError(t, err)
	_, err = repo.CompleteKnowledgeFileUpdate(context.Background(), 1, knowledgeID, first.ActiveVersion)
	require.NoError(t, err)

	taskQueue := &fileUpdateTaskStub{}
	svc := &knowledgeService{repo: repo, tenantRepo: fileUpdateTenantRepoStub{}, task: taskQueue}
	staleWake, err := json.Marshal(types.KnowledgeFileUpdateTaskPayload{
		TenantID: 1, KnowledgeBaseID: kbID, KnowledgeID: knowledgeID, ActiveVersion: first.ActiveVersion,
	})
	require.NoError(t, err)

	err = svc.ProcessKnowledgeFileUpdate(
		context.Background(), asynq.NewTask(types.TypeKnowledgeFileUpdate, staleWake),
	)
	require.NoError(t, err)
	require.Len(t, taskQueue.tasks, 1)
	var currentWake types.KnowledgeFileUpdateTaskPayload
	require.NoError(t, json.Unmarshal(taskQueue.tasks[0].Payload(), &currentWake))
	assert.Equal(t, latest.Version, currentWake.ActiveVersion)
}
