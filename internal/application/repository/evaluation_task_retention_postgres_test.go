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

func TestEvaluationTaskRepositoryPostgresRetentionMigrationAndPlan(t *testing.T) {
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

	schema := fmt.Sprintf("m2e_retention_%d", time.Now().UnixNano())
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
	for _, name := range []string{
		"000001_evaluation_tasks.up.sql",
		"000002_evaluation_task_cancellation.up.sql",
		"000003_evaluation_task_list.up.sql",
		"000004_evaluation_task_retention.up.sql",
		"000006_evaluation_experiment_snapshot.up.sql",
		"000007_evaluation_question_results.up.sql",
		"000009_evaluation_runtime_metrics.up.sql",
	} {
		migrationSQL, err := os.ReadFile(filepath.Join(migrationDir, name))
		require.NoError(t, err)
		require.NoError(t, db.Exec(string(migrationSQL)).Error)
	}

	var indexDef string
	require.NoError(t, db.Raw(
		"SELECT pg_get_indexdef(i.oid) FROM pg_class i JOIN pg_namespace n ON n.oid = i.relnamespace "+
			"WHERE n.nspname = ? AND i.relname = 'idx_evaluation_tasks_retention'",
		schema,
	).Scan(&indexDef).Error)
	assert.Contains(t, indexDef, "end_time")
	assert.Contains(t, indexDef, "status = ANY")

	// The retention scan can use the partial index (seq scan disabled to prove usability).
	require.NoError(t, db.Exec("SET enable_seqscan = off").Error)
	planRows, err := sqlDB.Query(
		`EXPLAIN (COSTS OFF) SELECT id FROM evaluation_tasks
			WHERE status IN (2, 3, 4, 5, 6) AND end_time IS NOT NULL AND end_time < NOW()
			ORDER BY end_time ASC LIMIT 500`,
	)
	require.NoError(t, err)
	plan := ""
	for planRows.Next() {
		var line string
		require.NoError(t, planRows.Scan(&line))
		plan += line + "\n"
	}
	require.NoError(t, planRows.Close())
	assert.Contains(t, plan, "idx_evaluation_tasks_retention")
	require.NoError(t, db.Exec("SET enable_seqscan = on").Error)

	repo := NewEvaluationTaskRepository(db)
	ctx := context.Background()
	cutoff := time.Date(2026, 8, 28, 0, 0, 0, 0, time.UTC)
	before := cutoff.Add(-time.Hour)
	expired := newRetentionTask(101, "pg-retention-expired", types.EvaluationStatueSuccess, &before)
	require.NoError(t, db.Create(expired).Error)
	active := newRetentionTask(101, "pg-retention-active", types.EvaluationStatueRunning, &before)
	require.NoError(t, db.Create(active).Error)

	deleted, err := repo.DeleteExpiredTerminalTasks(ctx, cutoff, 500)
	require.NoError(t, err)
	assert.Equal(t, int64(1), deleted)

	var remaining int64
	require.NoError(t, db.Raw("SELECT COUNT(*) FROM evaluation_tasks").Scan(&remaining).Error)
	assert.Equal(t, int64(1), remaining, "the active task survives retention")

	downSQL, err := os.ReadFile(filepath.Join(migrationDir, "000004_evaluation_task_retention.down.sql"))
	require.NoError(t, err)
	require.NoError(t, db.Exec(string(downSQL)).Error)
	var indexExists bool
	require.NoError(t, db.Raw(
		"SELECT EXISTS (SELECT 1 FROM pg_class i JOIN pg_namespace n ON n.oid = i.relnamespace "+
			"WHERE n.nspname = ? AND i.relname = 'idx_evaluation_tasks_retention')",
		schema,
	).Scan(&indexExists).Error)
	assert.False(t, indexExists, "000004 down must drop the retention index")
}
