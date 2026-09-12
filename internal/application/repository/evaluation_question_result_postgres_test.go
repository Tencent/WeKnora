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

// TestEvaluationQuestionResultPostgresContract verifies the per-question
// publication against the real PostgreSQL 000001+000006+000007 migrations in
// an isolated schema with transactional rollback.
func TestEvaluationQuestionResultPostgresContract(t *testing.T) {
	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("TEST_POSTGRES_DSN is not configured")
	}

	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	tx := db.Begin()
	require.NoError(t, tx.Error)
	t.Cleanup(func() { _ = tx.Rollback().Error })

	schema := fmt.Sprintf("m3_question_results_%d", time.Now().UnixNano())
	require.NoError(t, tx.Exec("CREATE SCHEMA "+schema).Error)
	require.NoError(t, tx.Exec("SET LOCAL search_path = "+schema+", pg_catalog").Error)

	for _, name := range []string{
		"000001_evaluation_tasks.up.sql",
		"000002_evaluation_task_cancellation.up.sql",
		"000006_evaluation_experiment_snapshot.up.sql",
		"000007_evaluation_question_results.up.sql",
		"000009_evaluation_runtime_metrics.up.sql",
	} {
		migrationSQL, err := os.ReadFile(filepath.Join("..", "..", "..", "migrations", "topic3", "postgres", name))
		require.NoError(t, err)
		require.NoError(t, tx.Exec(string(migrationSQL)).Error)
	}

	taskRepo := NewEvaluationTaskRepository(tx)
	repo := NewEvaluationQuestionResultRepository(tx)
	ctx := context.Background()

	task := newEvaluationTaskEntity(41, "postgres-questions")
	started := startEvaluationQuestionTask(t, taskRepo, task)

	updated, inserted, err := repo.PublishQuestionResult(ctx,
		newEvaluationQuestionCommandFixture(started, started.Version, 0))
	require.NoError(t, err)
	require.True(t, inserted)
	assert.Equal(t, 1, updated.Finished)

	jump := newEvaluationQuestionCommandFixture(updated, updated.Version, 1)
	jump.Finished = 3
	jumped, inserted, err := repo.PublishQuestionResult(ctx, jump)
	require.ErrorIs(t, err, ErrEvaluationTaskStateConflict)
	require.Nil(t, jumped)
	require.False(t, inserted)
	current, err := taskRepo.GetTask(ctx, task.TenantID, task.ID)
	require.NoError(t, err)
	assert.Equal(t, 1, current.Finished)
	assert.Equal(t, updated.Version, current.Version)

	// Idempotent retry and conflict semantics hold on PostgreSQL as on SQLite.
	retry := newEvaluationQuestionCommandFixture(updated, updated.Version, 0)
	_, inserted, err = repo.PublishQuestionResult(ctx, retry)
	require.NoError(t, err)
	assert.False(t, inserted)

	conflict := newEvaluationQuestionCommandFixture(updated, updated.Version, 0)
	conflict.Result.GeneratedText = "tampered"
	_, _, err = repo.PublishQuestionResult(ctx, conflict)
	require.ErrorIs(t, err, ErrEvaluationQuestionResultConflict)

	rows, err := repo.ListQuestionResults(ctx, task.TenantID, task.ID, 0, 10)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, time.UTC, rows[0].CreatedAt.Location())

	withoutRows := newEvaluationTaskEntity(41, "postgres-success-without-rows")
	withoutRowsStarted := startEvaluationQuestionTask(t, taskRepo, withoutRows)
	progressAt := withoutRowsStarted.HeartbeatAt.Add(time.Second)
	withoutRowsProgress, err := taskRepo.PublishProgress(ctx, types.EvaluationTaskProgressCommand{
		TenantID: withoutRows.TenantID, TaskID: withoutRows.ID, OwnerID: withoutRows.OwnerID,
		ExpectedVersion: withoutRowsStarted.Version, Total: 2, Finished: 2,
		Now: progressAt, LeaseExpiresAt: progressAt.Add(time.Minute),
	})
	require.NoError(t, err)
	terminal, err := taskRepo.PublishTerminal(ctx, types.EvaluationTaskTerminalCommand{
		TenantID: withoutRows.TenantID, TaskID: withoutRows.ID, OwnerID: withoutRows.OwnerID,
		ExpectedVersion: withoutRowsProgress.Version, Status: types.EvaluationStatueSuccess,
		EndTime: progressAt.Add(time.Second), CleanupErrors: types.JSON(`[]`),
	})
	require.ErrorIs(t, err, ErrEvaluationTaskStateConflict)
	require.Nil(t, terminal)

	current = updated
	for sampleIndex := 1; sampleIndex < 3; sampleIndex++ {
		command := newEvaluationQuestionCommandFixture(current, current.Version, sampleIndex)
		current, inserted, err = repo.PublishQuestionResult(ctx, command)
		require.NoError(t, err)
		require.True(t, inserted)
	}
	terminal, err = taskRepo.PublishTerminal(ctx, types.EvaluationTaskTerminalCommand{
		TenantID: task.TenantID, TaskID: task.ID, OwnerID: task.OwnerID,
		ExpectedVersion: current.Version, Status: types.EvaluationStatueSuccess,
		EndTime: time.Now().UTC(), CleanupErrors: types.JSON(`[]`),
	})
	require.NoError(t, err)
	assert.Equal(t, types.EvaluationStatueSuccess, terminal.Status)

	// The composite foreign key rejects dangling rows at the database level.
	require.Error(t, tx.Exec(`INSERT INTO evaluation_question_results (
		tenant_id, task_id, sample_index, qid, question, status, result_hash
	) VALUES (41, 'task-missing', 0, 'q1', 'question?', 'success',
		'cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc')`).Error)
}
