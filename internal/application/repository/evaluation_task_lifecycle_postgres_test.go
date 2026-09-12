package repository

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestEvaluationTaskRepositoryPostgresLifecycleContract(t *testing.T) {
	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("TEST_POSTGRES_DSN is not configured")
	}

	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	tx := db.Begin()
	require.NoError(t, tx.Error)
	t.Cleanup(func() { _ = tx.Rollback().Error })

	schema := fmt.Sprintf("m2b_lifecycle_%d", time.Now().UnixNano())
	require.NoError(t, tx.Exec("CREATE SCHEMA "+schema).Error)
	require.NoError(t, tx.Exec("SET LOCAL search_path = "+schema+", pg_catalog").Error)

	migrationPath := filepath.Join(
		"..", "..", "..", "migrations", "topic3", "postgres", "000001_evaluation_tasks.up.sql",
	)
	migrationSQL, err := os.ReadFile(migrationPath)
	require.NoError(t, err)
	require.NoError(t, tx.Exec(string(migrationSQL)).Error)
	cancelMigrationPath := filepath.Join(
		"..", "..", "..", "migrations", "topic3", "postgres", "000002_evaluation_task_cancellation.up.sql",
	)
	cancelMigrationSQL, err := os.ReadFile(cancelMigrationPath)
	require.NoError(t, err)
	require.NoError(t, tx.Exec(string(cancelMigrationSQL)).Error)
	snapshotMigrationPath := filepath.Join(
		"..", "..", "..", "migrations", "topic3", "postgres", "000006_evaluation_experiment_snapshot.up.sql",
	)
	snapshotMigrationSQL, err := os.ReadFile(snapshotMigrationPath)
	require.NoError(t, err)
	require.NoError(t, tx.Exec(string(snapshotMigrationSQL)).Error)
	questionMigrationPath := filepath.Join(
		"..", "..", "..", "migrations", "topic3", "postgres", "000007_evaluation_question_results.up.sql")
	questionMigrationSQL, err := os.ReadFile(questionMigrationPath)
	require.NoError(t, err)
	require.NoError(t, tx.Exec(string(questionMigrationSQL)).Error)
	runtimeMigrationPath := filepath.Join(
		"..", "..", "..", "migrations", "topic3", "postgres", "000009_evaluation_runtime_metrics.up.sql")
	runtimeMigrationSQL, err := os.ReadFile(runtimeMigrationPath)
	require.NoError(t, err)
	require.NoError(t, tx.Exec(string(runtimeMigrationSQL)).Error)

	ctx := context.Background()
	repo := NewEvaluationTaskRepository(tx)
	offset := time.FixedZone("UTC+9", 9*60*60)
	createdAt := time.Date(2026, 8, 28, 17, 0, 0, 0, offset)
	leaseExpiresAt := createdAt.Add(5 * time.Minute)
	task := &types.EvaluationTaskEntity{
		ID:                       "postgres-lifecycle-task",
		TenantID:                 27,
		DatasetID:                "default",
		StartTime:                createdAt,
		Params:                   types.JSON(`{"chat_model_id":"chat-postgres"}`),
		TemporaryKnowledgeBaseID: "kb-postgres-lifecycle",
		OwnerID:                  "owner-postgres-lifecycle",
		LeaseExpiresAt:           &leaseExpiresAt,
		HeartbeatAt:              createdAt,
		CreatedAt:                createdAt,
		UpdatedAt:                createdAt,
	}
	require.NoError(t, repo.CreateTask(ctx, task.TenantID, task))
	assert.Equal(t, uint64(1), task.Version)
	assert.Equal(t, time.UTC, task.StartTime.Location())
	assert.Equal(t, createdAt.UTC(), task.StartTime)

	startedAt := createdAt.Add(10 * time.Second)
	started, err := repo.TryStartTask(ctx, types.EvaluationTaskStartCommand{
		TenantID:        task.TenantID,
		TaskID:          task.ID,
		OwnerID:         task.OwnerID,
		ExpectedVersion: task.Version,
		Now:             startedAt,
		LeaseExpiresAt:  startedAt.Add(5 * time.Minute),
	})
	require.NoError(t, err)
	require.NotNil(t, started)
	assert.Equal(t, types.EvaluationStatueRunning, started.Status)
	assert.Equal(t, uint64(2), started.Version)
	assert.Equal(t, startedAt.UTC(), started.HeartbeatAt)
	assert.Equal(t, time.UTC, started.HeartbeatAt.Location())
	require.NotNil(t, started.LeaseExpiresAt)
	assert.Equal(t, startedAt.Add(5*time.Minute).UTC(), *started.LeaseExpiresAt)
	assert.Equal(t, time.UTC, started.LeaseExpiresAt.Location())

	knowledgeAt := startedAt.Add(10 * time.Second)
	withKnowledge, err := repo.RecordTemporaryKnowledge(ctx, types.EvaluationTaskKnowledgeCommand{
		TenantID:             task.TenantID,
		TaskID:               task.ID,
		OwnerID:              task.OwnerID,
		ExpectedVersion:      started.Version,
		TemporaryKnowledgeID: "knowledge-postgres-lifecycle",
		UpdatedAt:            knowledgeAt,
	})
	require.NoError(t, err)
	require.NotNil(t, withKnowledge)
	assert.Equal(t, "knowledge-postgres-lifecycle", withKnowledge.TemporaryKnowledgeID)
	assert.Equal(t, uint64(3), withKnowledge.Version)
	assert.Equal(t, knowledgeAt.UTC(), withKnowledge.UpdatedAt)
	assert.Equal(t, time.UTC, withKnowledge.UpdatedAt.Location())

	progressAt := knowledgeAt.Add(10 * time.Second)
	progressMetric := types.JSON(`{"retrieval_metrics":{"precision":0.75}}`)
	progress, err := repo.PublishProgress(ctx, types.EvaluationTaskProgressCommand{
		TenantID:        task.TenantID,
		TaskID:          task.ID,
		OwnerID:         task.OwnerID,
		ExpectedVersion: withKnowledge.Version,
		Total:           4,
		Finished:        2,
		Metric:          progressMetric,
		Now:             progressAt,
		LeaseExpiresAt:  progressAt.Add(5 * time.Minute),
	})
	require.NoError(t, err)
	require.NotNil(t, progress)
	assert.Equal(t, 4, progress.Total)
	assert.Equal(t, 2, progress.Finished)
	assert.JSONEq(t, progressMetric.ToString(), progress.Metric.ToString())
	assert.Equal(t, uint64(4), progress.Version)
	assert.Equal(t, progressAt.UTC(), progress.HeartbeatAt)
	assert.Equal(t, time.UTC, progress.HeartbeatAt.Location())

	stale, err := repo.PublishProgress(ctx, types.EvaluationTaskProgressCommand{
		TenantID:        task.TenantID,
		TaskID:          task.ID,
		OwnerID:         task.OwnerID,
		ExpectedVersion: withKnowledge.Version,
		Total:           4,
		Finished:        3,
		Metric:          progressMetric,
		Now:             progressAt.Add(time.Second),
		LeaseExpiresAt:  progressAt.Add(5 * time.Minute),
	})
	require.ErrorIs(t, err, ErrEvaluationTaskVersionConflict)
	assert.Nil(t, stale)

	endedAt := progressAt.Add(15 * time.Second)
	cleanupErrors := types.JSON(`["knowledge cleanup failed"]`)
	terminalMetric := types.JSON(`{"retrieval_metrics":{"precision":0.875}}`)
	terminal, err := repo.PublishTerminal(ctx, types.EvaluationTaskTerminalCommand{
		TenantID:        task.TenantID,
		TaskID:          task.ID,
		OwnerID:         task.OwnerID,
		ExpectedVersion: progress.Version,
		Status:          types.EvaluationStatueFailed,
		EndTime:         endedAt,
		ErrMsg:          "evaluation failed",
		CleanupErrors:   cleanupErrors,
		Metric:          terminalMetric,
	})
	require.NoError(t, err)
	require.NotNil(t, terminal)
	assert.Equal(t, types.EvaluationStatueFailed, terminal.Status)
	assert.Equal(t, uint64(5), terminal.Version)
	require.NotNil(t, terminal.EndTime)
	assert.Equal(t, endedAt.UTC(), *terminal.EndTime)
	assert.Equal(t, time.UTC, terminal.EndTime.Location())
	assert.Equal(t, "evaluation failed", terminal.ErrMsg)
	assert.JSONEq(t, cleanupErrors.ToString(), terminal.CleanupErrors.ToString())
	assert.JSONEq(t, terminalMetric.ToString(), terminal.Metric.ToString())
	assert.Nil(t, terminal.LeaseExpiresAt)

	late, err := repo.PublishProgress(ctx, types.EvaluationTaskProgressCommand{
		TenantID:        task.TenantID,
		TaskID:          task.ID,
		OwnerID:         task.OwnerID,
		ExpectedVersion: terminal.Version,
		Total:           4,
		Finished:        4,
		Metric:          progressMetric,
		Now:             endedAt.Add(time.Second),
		LeaseExpiresAt:  endedAt.Add(5 * time.Minute),
	})
	require.ErrorIs(t, err, ErrEvaluationTaskStateConflict)
	assert.Nil(t, late)

	persisted, err := repo.GetTask(ctx, task.TenantID, task.ID)
	require.NoError(t, err)
	assert.Equal(t, terminal.Status, persisted.Status)
	assert.Equal(t, terminal.Version, persisted.Version)
	assert.Equal(t, terminal.EndTime, persisted.EndTime)
	assert.JSONEq(t, task.Params.ToString(), persisted.Params.ToString())
	assert.JSONEq(t, terminal.Metric.ToString(), persisted.Metric.ToString())
	assert.JSONEq(t, terminal.CleanupErrors.ToString(), persisted.CleanupErrors.ToString())
	assert.Nil(t, persisted.LeaseExpiresAt)

	var storage struct {
		ParamsType        string `gorm:"column:params_type"`
		MetricType        string `gorm:"column:metric_type"`
		CleanupErrorsType string `gorm:"column:cleanup_errors_type"`
		LeaseReleased     bool   `gorm:"column:lease_released"`
	}
	require.NoError(t, tx.Raw(`
		SELECT pg_typeof(params)::text AS params_type,
		       pg_typeof(metric)::text AS metric_type,
		       pg_typeof(cleanup_errors)::text AS cleanup_errors_type,
		       lease_expires_at IS NULL AS lease_released
		FROM evaluation_tasks
		WHERE tenant_id = ? AND id = ?
	`, task.TenantID, task.ID).Scan(&storage).Error)
	assert.Equal(t, "jsonb", storage.ParamsType)
	assert.Equal(t, "jsonb", storage.MetricType)
	assert.Equal(t, "jsonb", storage.CleanupErrorsType)
	assert.True(t, storage.LeaseReleased)
}
