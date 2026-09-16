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

func TestEvaluationTaskRepositoryPostgresContract(t *testing.T) {
	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("TEST_POSTGRES_DSN is not configured")
	}

	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	tx := db.Begin()
	require.NoError(t, tx.Error)
	t.Cleanup(func() { _ = tx.Rollback().Error })

	schema := fmt.Sprintf("m2a_repository_%d", time.Now().UnixNano())
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

	// 000006 adds the nullable experiment snapshot columns on top of 000001.
	snapshotMigrationPath := filepath.Join(
		"..", "..", "..", "migrations", "topic3", "postgres", "000006_evaluation_experiment_snapshot.up.sql")
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

	now := time.Date(2026, 8, 28, 8, 0, 0, 0, time.UTC)
	leaseExpiresAt := now.Add(time.Minute)
	task := &types.EvaluationTaskEntity{
		ID:                       "postgres-evaluation-task",
		TenantID:                 7,
		DatasetID:                "default",
		StartTime:                now,
		Params:                   types.JSON(`{"chat_model_id":"chat-1"}`),
		TemporaryKnowledgeBaseID: "kb-postgres",
		OwnerID:                  "owner-postgres",
		LeaseExpiresAt:           &leaseExpiresAt,
		HeartbeatAt:              now,
	}
	repo := NewEvaluationTaskRepository(tx)

	require.NoError(t, repo.CreateTask(context.Background(), task.TenantID, task))
	got, err := repo.GetTask(context.Background(), task.TenantID, task.ID)
	require.NoError(t, err)
	assert.Equal(t, task.ID, got.ID)
	assert.Equal(t, task.TenantID, got.TenantID)
	assert.Equal(t, task.Status, got.Status)
	assert.Equal(t, time.UTC, got.LeaseExpiresAt.Location())
	assert.JSONEq(t, task.Params.ToString(), got.Params.ToString())

	err = repo.CreateTask(context.Background(), task.TenantID, task)
	require.ErrorIs(t, err, ErrEvaluationTaskAlreadyExists)

	// Experiment snapshot columns accept a frozen manifest and keep null
	// provenance for pre-M3 rows.
	datasetVersionID := "dataset-version-pg"
	contentSHA256 := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	experimentSHA256 := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	snapshotTask := &types.EvaluationTaskEntity{
		ID: "postgres-evaluation-task-snapshot", TenantID: 7, DatasetID: "default",
		StartTime: now, Params: types.JSON(`{"chat_model_id":"chat-1"}`),
		TemporaryKnowledgeBaseID: "kb-postgres-2", OwnerID: "owner-postgres",
		LeaseExpiresAt: &leaseExpiresAt, HeartbeatAt: now,
		DatasetVersionID: &datasetVersionID, DatasetContentSHA256: &contentSHA256,
		ExperimentSnapshot: types.JSON(`{"schema_version":1}`),
		ExperimentSHA256:   &experimentSHA256,
	}
	require.NoError(t, repo.CreateTask(context.Background(), snapshotTask.TenantID, snapshotTask))
	snapshotGot, err := repo.GetTask(context.Background(), snapshotTask.TenantID, snapshotTask.ID)
	require.NoError(t, err)
	require.NotNil(t, snapshotGot.DatasetVersionID)
	assert.Equal(t, datasetVersionID, *snapshotGot.DatasetVersionID)
	assert.JSONEq(t, `{"schema_version":1}`, string(snapshotGot.ExperimentSnapshot))
	require.NotNil(t, snapshotGot.ExperimentSHA256)
	assert.Equal(t, experimentSHA256, *snapshotGot.ExperimentSHA256)
	assert.Nil(t, got.DatasetVersionID, "pre-M3 rows keep null provenance")
	assert.Nil(t, got.ExperimentSHA256, "pre-M3 rows keep null provenance")
}
