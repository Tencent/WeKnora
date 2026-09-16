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

func TestEvaluationTaskRepositoryPostgresCancellationMigrationAndTruth(t *testing.T) {
	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("TEST_POSTGRES_DSN is not configured")
	}

	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = sqlDB.Close()
	})

	schema := fmt.Sprintf("m2d_cancel_%d", time.Now().UnixNano())
	require.NoError(t, db.Exec("CREATE SCHEMA "+schema).Error)
	t.Cleanup(func() {
		cleanupDB, openErr := gorm.Open(postgres.Open(dsn), &gorm.Config{})
		if openErr == nil {
			_ = cleanupDB.Exec("DROP SCHEMA IF EXISTS " + schema + " CASCADE").Error
			if cleanupSQLDB, dbErr := cleanupDB.DB(); dbErr == nil {
				_ = cleanupSQLDB.Close()
			}
		}
	})
	require.NoError(t, db.Exec("SET search_path = "+schema+", pg_catalog").Error)

	migrationDir := filepath.Join("..", "..", "..", "migrations", "topic3", "postgres")
	up90, err := os.ReadFile(filepath.Join(migrationDir, "000001_evaluation_tasks.up.sql"))
	require.NoError(t, err)
	require.NoError(t, db.Exec(string(up90)).Error)
	up91, err := os.ReadFile(filepath.Join(migrationDir, "000002_evaluation_task_cancellation.up.sql"))
	require.NoError(t, err)
	require.NoError(t, db.Exec(string(up91)).Error)
	up95, err := os.ReadFile(filepath.Join(migrationDir, "000006_evaluation_experiment_snapshot.up.sql"))
	require.NoError(t, err)
	require.NoError(t, db.Exec(string(up95)).Error)
	up96, err := os.ReadFile(filepath.Join(migrationDir, "000007_evaluation_question_results.up.sql"))
	require.NoError(t, err)
	require.NoError(t, db.Exec(string(up96)).Error)
	up98, err := os.ReadFile(filepath.Join(migrationDir, "000009_evaluation_runtime_metrics.up.sql"))
	require.NoError(t, err)
	require.NoError(t, db.Exec(string(up98)).Error)

	var cancelColumn bool
	require.NoError(t, db.Raw(
		"SELECT EXISTS (SELECT 1 FROM information_schema.columns "+
			"WHERE table_schema = ? AND table_name = 'evaluation_tasks' AND column_name = 'cancel_requested_at')",
		schema,
	).Scan(&cancelColumn).Error)
	assert.True(t, cancelColumn, "000002 must add cancel_requested_at")

	repo := NewEvaluationTaskRepository(db)
	ctx := context.Background()
	claimAt := time.Date(2026, 8, 28, 10, 0, 0, 0, time.UTC)
	task := newRecoveryTask(71, "postgres-cancel", claimAt.Add(-time.Hour), claimAt.Add(time.Hour))
	require.NoError(t, repo.CreateTask(ctx, task.TenantID, task))
	started, err := repo.TryStartTask(ctx, types.EvaluationTaskStartCommand{
		TenantID:        task.TenantID,
		TaskID:          task.ID,
		OwnerID:         task.OwnerID,
		ExpectedVersion: task.Version,
		Now:             task.StartTime.Add(time.Minute),
		LeaseExpiresAt:  claimAt.Add(2 * time.Minute),
	})
	require.NoError(t, err)

	canceled, err := repo.RequestCancel(ctx, types.EvaluationTaskCancelCommand{
		TenantID: task.TenantID,
		TaskID:   task.ID,
		Now:      claimAt.Add(30 * time.Second),
	})
	require.NoError(t, err)
	require.NotNil(t, canceled.CancelRequestedAt)
	assert.Equal(t, started.Version, canceled.Version)

	_, err = repo.PublishTerminal(ctx, types.EvaluationTaskTerminalCommand{
		TenantID:        task.TenantID,
		TaskID:          task.ID,
		OwnerID:         task.OwnerID,
		ExpectedVersion: started.Version,
		Status:          types.EvaluationStatueSuccess,
		EndTime:         claimAt.Add(time.Minute),
		CleanupErrors:   types.JSON(`[]`),
	})
	require.ErrorIs(t, err, ErrEvaluationTaskStateConflict)

	terminal, err := repo.PublishTerminal(ctx, types.EvaluationTaskTerminalCommand{
		TenantID:        task.TenantID,
		TaskID:          task.ID,
		OwnerID:         task.OwnerID,
		ExpectedVersion: started.Version,
		Status:          types.EvaluationStatueCanceled,
		EndTime:         claimAt.Add(time.Minute),
		ErrMsg:          "evaluation task canceled",
		CleanupErrors:   types.JSON(`[]`),
	})
	require.NoError(t, err)
	assert.Equal(t, types.EvaluationStatueCanceled, terminal.Status)

	// The down migration removes the column again.
	down91, err := os.ReadFile(filepath.Join(migrationDir, "000002_evaluation_task_cancellation.down.sql"))
	require.NoError(t, err)
	require.NoError(t, db.Exec(string(down91)).Error)
	require.NoError(t, db.Raw(
		"SELECT EXISTS (SELECT 1 FROM information_schema.columns "+
			"WHERE table_schema = ? AND table_name = 'evaluation_tasks' AND column_name = 'cancel_requested_at')",
		schema,
	).Scan(&cancelColumn).Error)
	assert.False(t, cancelColumn, "000002 down must drop cancel_requested_at")
}
