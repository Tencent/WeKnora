package repository

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/database"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func setupEvaluationTaskRepositoryTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	repoRoot, err := filepath.Abs(filepath.Join("..", "..", ".."))
	require.NoError(t, err)
	previousDir, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(repoRoot))
	dbPath := filepath.Join(t.TempDir(), "evaluation-task.db")
	migrationErr := database.RunMigrationsWithOptions(
		"sqlite3://unused",
		database.MigrationOptions{SQLiteDBPath: dbPath},
	)
	require.NoError(t, os.Chdir(previousDir))
	require.NoError(t, migrationErr)

	db, err := gorm.Open(sqlite.Open(dbPath+"?_foreign_keys=on"), &gorm.Config{})
	require.NoError(t, err)
	return db
}

func TestEvaluationTaskRepositoryPersistsTenantScopedSnapshot(t *testing.T) {
	db := setupEvaluationTaskRepositoryTestDB(t)
	ctx := context.Background()
	createdAt := time.Date(2026, 8, 28, 8, 0, 0, 0, time.UTC)
	leaseExpiresAt := createdAt.Add(time.Minute)
	task := &types.EvaluationTaskEntity{
		ID:                       "evaluation_7_1787875200000_a1b2c3d4_default",
		TenantID:                 7,
		DatasetID:                "default",
		Status:                   types.EvaluationStatuePending,
		StartTime:                createdAt,
		Params:                   types.JSON(`{"chat_model_id":"chat-1"}`),
		TemporaryKnowledgeBaseID: "kb-evaluation",
		OwnerID:                  "a1b2c3d4-e5f6-47a8-9012-3456789abcde",
		LeaseExpiresAt:           &leaseExpiresAt,
		HeartbeatAt:              createdAt,
		Version:                  1,
		CreatedAt:                createdAt,
		UpdatedAt:                createdAt,
	}

	repo := NewEvaluationTaskRepository(db)
	require.NoError(t, repo.CreateTask(ctx, task.TenantID, task))
	wantCleanupErrors := types.JSON(`["cleanup warning"]`)
	wantMetric := types.JSON(`{"retrieval_metrics":{"precision":0.5}}`)
	require.NoError(t, db.Model(&types.EvaluationTaskEntity{}).
		Where("tenant_id = ? AND id = ?", task.TenantID, task.ID).
		Updates(map[string]any{
			"cleanup_errors": wantCleanupErrors,
			"metric":         wantMetric,
		}).Error)

	// A fresh repository instance represents a service reconstructed after a
	// process restart. The database row remains the source of truth.
	restartedRepo := NewEvaluationTaskRepository(db)
	got, err := restartedRepo.GetTask(ctx, task.TenantID, task.ID)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, task.ID, got.ID)
	assert.Equal(t, task.TenantID, got.TenantID)
	assert.Equal(t, task.DatasetID, got.DatasetID)
	assert.Equal(t, task.Status, got.Status)
	assert.Equal(t, task.StartTime, got.StartTime)
	assert.Equal(t, task.TemporaryKnowledgeBaseID, got.TemporaryKnowledgeBaseID)
	assert.Equal(t, task.OwnerID, got.OwnerID)
	assert.Equal(t, task.LeaseExpiresAt, got.LeaseExpiresAt)
	assert.JSONEq(t, wantCleanupErrors.ToString(), got.CleanupErrors.ToString())
	assert.JSONEq(t, task.Params.ToString(), got.Params.ToString())
	assert.JSONEq(t, wantMetric.ToString(), got.Metric.ToString())

	_, err = restartedRepo.GetTask(ctx, 8, task.ID)
	require.ErrorIs(t, err, ErrEvaluationTaskNotFound)
}

func TestEvaluationTaskRepositoryCreateDefaultsAndSoftDelete(t *testing.T) {
	db := setupEvaluationTaskRepositoryTestDB(t)
	ctx := context.Background()
	task := &types.EvaluationTaskEntity{
		ID:                       "evaluation_9_1787875200000_b1c2d3e4_default",
		TenantID:                 9,
		DatasetID:                "default",
		TemporaryKnowledgeBaseID: "kb-evaluation-defaults",
		OwnerID:                  "b1c2d3e4-f5a6-47b8-9012-3456789abcde",
		LeaseExpiresAt:           ptrToTime(time.Now().UTC().Add(time.Minute)),
	}
	repo := NewEvaluationTaskRepository(db)

	require.NoError(t, repo.CreateTask(ctx, task.TenantID, task))
	got, err := repo.GetTask(ctx, task.TenantID, task.ID)
	require.NoError(t, err)
	assert.False(t, got.StartTime.IsZero())
	assert.False(t, got.HeartbeatAt.IsZero())
	assert.False(t, got.CreatedAt.IsZero())
	assert.False(t, got.UpdatedAt.IsZero())
	assert.Equal(t, uint64(1), got.Version)
	assert.JSONEq(t, `[]`, got.CleanupErrors.ToString())
	assert.JSONEq(t, `{}`, got.Params.ToString())

	deleteResult := db.Delete(
		&types.EvaluationTaskEntity{}, "tenant_id = ? AND id = ?", task.TenantID, task.ID,
	)
	require.NoError(t, deleteResult.Error)
	_, err = repo.GetTask(ctx, task.TenantID, task.ID)
	require.ErrorIs(t, err, ErrEvaluationTaskNotFound)
}

func TestEvaluationTaskRepositoryRejectsIncompleteTask(t *testing.T) {
	db := setupEvaluationTaskRepositoryTestDB(t)
	repo := NewEvaluationTaskRepository(db)
	ctx := context.Background()
	leaseExpiresAt := time.Now().UTC().Add(time.Minute)

	tests := []struct {
		name string
		task *types.EvaluationTaskEntity
	}{
		{name: "nil task"},
		{name: "missing identity", task: &types.EvaluationTaskEntity{}},
		{name: "missing owner", task: &types.EvaluationTaskEntity{
			ID: "task", TenantID: 1, DatasetID: "default",
			TemporaryKnowledgeBaseID: "kb", LeaseExpiresAt: &leaseExpiresAt,
		}},
		{name: "missing lease", task: &types.EvaluationTaskEntity{
			ID: "task", TenantID: 1, DatasetID: "default",
			TemporaryKnowledgeBaseID: "kb", OwnerID: "owner",
		}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			require.Error(t, repo.CreateTask(ctx, 1, tc.task))
		})
	}

	var count int64
	require.NoError(t, db.Model(&types.EvaluationTaskEntity{}).Count(&count).Error)
	assert.Zero(t, count)
}

func TestEvaluationTaskRepositoryRejectsTenantContextMismatch(t *testing.T) {
	db := setupEvaluationTaskRepositoryTestDB(t)
	repo := NewEvaluationTaskRepository(db)
	ctx := context.Background()
	task := newEvaluationTaskEntity(7, "tenant-mismatch")

	err := repo.CreateTask(ctx, 8, task)
	require.ErrorIs(t, err, ErrEvaluationTaskTenantMismatch)

	var count int64
	require.NoError(t, db.Model(&types.EvaluationTaskEntity{}).Count(&count).Error)
	assert.Zero(t, count)
}

func TestEvaluationTaskRepositoryReturnsStableAlreadyExistsError(t *testing.T) {
	db := setupEvaluationTaskRepositoryTestDB(t)
	repo := NewEvaluationTaskRepository(db)
	ctx := context.Background()
	task := newEvaluationTaskEntity(7, "duplicate")

	require.NoError(t, repo.CreateTask(ctx, task.TenantID, task))
	err := repo.CreateTask(ctx, task.TenantID, task)
	require.ErrorIs(t, err, ErrEvaluationTaskAlreadyExists)
}

func TestEvaluationTaskRepositoryNormalizesTimesToUTC(t *testing.T) {
	db := setupEvaluationTaskRepositoryTestDB(t)
	repo := NewEvaluationTaskRepository(db)
	offset := time.FixedZone("UTC+9", 9*60*60)
	leaseExpiresAt := time.Date(2026, 8, 28, 17, 30, 0, 0, offset)
	endTime := leaseExpiresAt.Add(-time.Minute)
	task := newEvaluationTaskEntity(7, "utc-normalization")
	task.StartTime = leaseExpiresAt.Add(-time.Hour)
	task.LeaseExpiresAt = &leaseExpiresAt
	task.HeartbeatAt = leaseExpiresAt.Add(-time.Minute)
	task.CreatedAt = leaseExpiresAt.Add(-time.Hour)
	task.UpdatedAt = leaseExpiresAt.Add(-time.Minute)

	require.NoError(t, repo.CreateTask(context.Background(), task.TenantID, task))
	require.NoError(t, db.Model(&types.EvaluationTaskEntity{}).
		Where("tenant_id = ? AND id = ?", task.TenantID, task.ID).
		Update("end_time", endTime).Error)

	got, err := repo.GetTask(context.Background(), task.TenantID, task.ID)
	require.NoError(t, err)
	assert.Equal(t, time.UTC, got.StartTime.Location())
	assert.Equal(t, time.UTC, got.EndTime.Location())
	assert.Equal(t, time.UTC, got.LeaseExpiresAt.Location())
	assert.Equal(t, time.UTC, got.HeartbeatAt.Location())
	assert.Equal(t, time.UTC, got.CreatedAt.Location())
	assert.Equal(t, time.UTC, got.UpdatedAt.Location())

	activeOffset := time.FixedZone("UTC-5", -5*60*60)
	activeLease := time.Date(2026, 8, 28, 4, 30, 0, 0, activeOffset)
	activeTask := newEvaluationTaskEntity(7, "utc-active")
	activeTask.StartTime = activeLease.Add(-time.Hour)
	activeTask.HeartbeatAt = activeLease.Add(-time.Minute)
	activeTask.LeaseExpiresAt = &activeLease
	require.NoError(t, repo.CreateTask(context.Background(), activeTask.TenantID, activeTask))

	cutoff := time.Date(2026, 8, 28, 9, 0, 0, 0, time.UTC)
	var expiredIDs []string
	require.NoError(t, db.Model(&types.EvaluationTaskEntity{}).
		Where("lease_expires_at < ?", cutoff).
		Order("id").
		Pluck("id", &expiredIDs).Error)
	assert.Equal(t, []string{task.ID}, expiredIDs)
}

func TestEvaluationTaskRepositoryRejectsNonInitialSnapshot(t *testing.T) {
	db := setupEvaluationTaskRepositoryTestDB(t)
	repo := NewEvaluationTaskRepository(db)
	now := time.Date(2026, 8, 28, 8, 0, 0, 0, time.UTC)

	tests := []struct {
		name   string
		mutate func(*types.EvaluationTaskEntity)
	}{
		{name: "running status", mutate: func(task *types.EvaluationTaskEntity) {
			task.Status = types.EvaluationStatueRunning
		}},
		{name: "end time", mutate: func(task *types.EvaluationTaskEntity) { task.EndTime = &now }},
		{name: "total", mutate: func(task *types.EvaluationTaskEntity) { task.Total = 1 }},
		{name: "finished", mutate: func(task *types.EvaluationTaskEntity) { task.Finished = 1 }},
		{name: "error message", mutate: func(task *types.EvaluationTaskEntity) { task.ErrMsg = "failed" }},
		{name: "cleanup errors", mutate: func(task *types.EvaluationTaskEntity) {
			task.CleanupErrors = types.JSON(`["warning"]`)
		}},
		{name: "null cleanup errors", mutate: func(task *types.EvaluationTaskEntity) {
			task.CleanupErrors = types.JSON(`null`)
		}},
		{name: "non-object params", mutate: func(task *types.EvaluationTaskEntity) {
			task.Params = types.JSON(`[]`)
		}},
		{name: "null params", mutate: func(task *types.EvaluationTaskEntity) {
			task.Params = types.JSON(`null`)
		}},
		{name: "metric", mutate: func(task *types.EvaluationTaskEntity) { task.Metric = types.JSON(`{}`) }},
		{name: "temporary knowledge", mutate: func(task *types.EvaluationTaskEntity) {
			task.TemporaryKnowledgeID = "knowledge"
		}},
		{name: "advanced version", mutate: func(task *types.EvaluationTaskEntity) { task.Version = 2 }},
		{name: "deleted task", mutate: func(task *types.EvaluationTaskEntity) {
			task.DeletedAt = gorm.DeletedAt{Time: now, Valid: true}
		}},
		{name: "start after lease", mutate: func(task *types.EvaluationTaskEntity) {
			task.StartTime = task.LeaseExpiresAt.Add(time.Second)
		}},
		{name: "heartbeat after lease", mutate: func(task *types.EvaluationTaskEntity) {
			task.HeartbeatAt = task.LeaseExpiresAt.Add(time.Second)
		}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			task := newEvaluationTaskEntity(7, "non-initial-"+tc.name)
			tc.mutate(task)
			require.Error(t, repo.CreateTask(context.Background(), task.TenantID, task))
		})
	}

	var count int64
	require.NoError(t, db.Model(&types.EvaluationTaskEntity{}).Count(&count).Error)
	assert.Zero(t, count)
}

func newEvaluationTaskEntity(tenantID uint64, suffix string) *types.EvaluationTaskEntity {
	now := time.Date(2026, 8, 28, 8, 0, 0, 0, time.UTC)
	return &types.EvaluationTaskEntity{
		ID:                       "evaluation-" + suffix,
		TenantID:                 tenantID,
		DatasetID:                "default",
		StartTime:                now,
		TemporaryKnowledgeBaseID: "kb-" + suffix,
		OwnerID:                  "owner-" + suffix,
		LeaseExpiresAt:           ptrToTime(now.Add(time.Minute)),
		HeartbeatAt:              now,
		CreatedAt:                now,
		UpdatedAt:                now,
	}
}

func ptrToTime(value time.Time) *time.Time {
	return &value
}
