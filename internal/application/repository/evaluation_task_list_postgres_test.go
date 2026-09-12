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

func TestEvaluationTaskRepositoryPostgresListMigrationAndKeyset(t *testing.T) {
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

	schema := fmt.Sprintf("m4_list_%d", time.Now().UnixNano())
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
		"000005_evaluation_datasets.up.sql",
		"000006_evaluation_experiment_snapshot.up.sql",
		"000007_evaluation_question_results.up.sql",
		"000008_evaluation_task_labels.up.sql",
		"000009_evaluation_runtime_metrics.up.sql",
	} {
		migrationSQL, err := os.ReadFile(filepath.Join(migrationDir, name))
		require.NoError(t, err)
		require.NoError(t, db.Exec(string(migrationSQL)).Error)
	}

	var indexDef string
	require.NoError(t, db.Raw(
		"SELECT pg_get_indexdef(i.oid) FROM pg_class i JOIN pg_namespace n ON n.oid = i.relnamespace "+
			"WHERE n.nspname = ? AND i.relname = 'idx_evaluation_tasks_tenant_status_started'",
		schema,
	).Scan(&indexDef).Error)
	assert.Contains(t, indexDef, "start_time DESC")
	assert.Contains(t, indexDef, "id DESC")
	assert.Contains(t, indexDef, "deleted_at IS NULL")

	repo := NewEvaluationTaskRepository(db)
	ctx := context.Background()
	sharedStart := time.Date(2026, 8, 28, 9, 0, 0, 0, time.UTC)
	for i := 0; i < 3; i++ {
		task := newListTask(81, fmt.Sprintf("pg-list-%d", i), sharedStart, types.EvaluationStatueSuccess)
		endTime := sharedStart.Add(time.Minute)
		task.EndTime = &endTime
		task.LeaseExpiresAt = nil
		versionID := "version-a"
		task.DatasetVersionID = &versionID
		hash := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		task.DatasetContentSHA256 = &hash
		task.ExperimentSHA256 = &hash
		task.ExperimentSnapshot = types.JSON(`{"models":{"chat":{"id":"chat-a"}}}`)
		require.NoError(t, db.Create(task).Error)
	}
	hidden := newListTask(82, "pg-hidden", sharedStart, types.EvaluationStatueSuccess)
	hidden.LeaseExpiresAt = nil
	require.NoError(t, db.Create(hidden).Error)
	comparisonRows, err := repo.GetTasksByIDs(ctx, 81, []string{
		"pg-list-0", "pg-list-2", "pg-hidden", "pg-missing",
	})
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"pg-list-0", "pg-list-2"}, mapKeys(comparisonRows))
	require.NoError(t, repo.ReplaceTaskLabels(
		ctx,
		81,
		"pg-list-2",
		[]string{"baseline", "retrieval"},
		sharedStart,
	))
	filtered, err := repo.ListTasks(ctx, 81, types.EvaluationTaskListQuery{
		DatasetVersionID: "version-a",
		ModelID:          "chat-a",
		Labels:           []string{"baseline", "retrieval"},
		Limit:            10,
	})
	require.NoError(t, err)
	require.Len(t, filtered, 1)
	assert.Equal(t, "pg-list-2", filtered[0].ID)

	first, err := repo.ListTasks(ctx, 81, types.EvaluationTaskListQuery{Limit: 2})
	require.NoError(t, err)
	require.Len(t, first, 2)
	boundary := first[1].StartTime.UTC()
	second, err := repo.ListTasks(ctx, 81, types.EvaluationTaskListQuery{
		Limit:       2,
		StartBefore: &boundary,
		IDBefore:    first[1].ID,
	})
	require.NoError(t, err)
	require.Len(t, second, 1)
	assert.Equal(t, "pg-list-0", second[0].ID)
	assert.Equal(t, []string{"pg-list-2", "pg-list-1"}, []string{first[0].ID, first[1].ID})

	downSQL, err := os.ReadFile(filepath.Join(migrationDir, "000003_evaluation_task_list.down.sql"))
	require.NoError(t, err)
	require.NoError(t, db.Exec(string(downSQL)).Error)
	var indexExists bool
	require.NoError(t, db.Raw(
		"SELECT EXISTS (SELECT 1 FROM pg_class i JOIN pg_namespace n ON n.oid = i.relnamespace "+
			"WHERE n.nspname = ? AND i.relname = 'idx_evaluation_tasks_tenant_status_started')",
		schema,
	).Scan(&indexExists).Error)
	assert.False(t, indexExists, "000003 down must drop the status-started index")
}
