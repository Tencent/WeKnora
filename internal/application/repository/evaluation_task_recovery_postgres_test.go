package repository

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestEvaluationTaskRepositoryPostgresConnectionsClaimExpiredTaskMutuallyExclusively(t *testing.T) {
	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("TEST_POSTGRES_DSN is not configured")
	}

	firstDB, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	secondDB, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	firstSQLDB, err := firstDB.DB()
	require.NoError(t, err)
	secondSQLDB, err := secondDB.DB()
	require.NoError(t, err)
	firstSQLDB.SetMaxOpenConns(1)
	secondSQLDB.SetMaxOpenConns(1)
	t.Cleanup(func() {
		_ = firstSQLDB.Close()
		_ = secondSQLDB.Close()
	})

	schema := fmt.Sprintf("m2c_recovery_%d", time.Now().UnixNano())
	require.NoError(t, firstDB.Exec("CREATE SCHEMA "+schema).Error)
	t.Cleanup(func() {
		cleanupDB, openErr := gorm.Open(postgres.Open(dsn), &gorm.Config{})
		if openErr == nil {
			_ = cleanupDB.Exec("DROP SCHEMA IF EXISTS " + schema + " CASCADE").Error
			if sqlDB, dbErr := cleanupDB.DB(); dbErr == nil {
				_ = sqlDB.Close()
			}
		}
	})
	require.NoError(t, firstDB.Exec("SET search_path = "+schema+", pg_catalog").Error)
	require.NoError(t, secondDB.Exec("SET search_path = "+schema+", pg_catalog").Error)
	migrationPath := filepath.Join(
		"..", "..", "..", "migrations", "topic3", "postgres", "000001_evaluation_tasks.up.sql",
	)
	migrationSQL, err := os.ReadFile(migrationPath)
	require.NoError(t, err)
	require.NoError(t, firstDB.Exec(string(migrationSQL)).Error)
	cancelMigrationPath := filepath.Join(
		"..", "..", "..", "migrations", "topic3", "postgres", "000002_evaluation_task_cancellation.up.sql",
	)
	cancelMigrationSQL, err := os.ReadFile(cancelMigrationPath)
	require.NoError(t, err)
	require.NoError(t, firstDB.Exec(string(cancelMigrationSQL)).Error)
	snapshotMigrationPath := filepath.Join(
		"..", "..", "..", "migrations", "topic3", "postgres", "000006_evaluation_experiment_snapshot.up.sql",
	)
	snapshotMigrationSQL, err := os.ReadFile(snapshotMigrationPath)
	require.NoError(t, err)
	require.NoError(t, firstDB.Exec(string(snapshotMigrationSQL)).Error)
	questionMigrationPath := filepath.Join(
		"..", "..", "..", "migrations", "topic3", "postgres", "000007_evaluation_question_results.up.sql")
	questionMigrationSQL, err := os.ReadFile(questionMigrationPath)
	require.NoError(t, err)
	require.NoError(t, firstDB.Exec(string(questionMigrationSQL)).Error)
	runtimeMigrationPath := filepath.Join(
		"..", "..", "..", "migrations", "topic3", "postgres", "000009_evaluation_runtime_metrics.up.sql")
	runtimeMigrationSQL, err := os.ReadFile(runtimeMigrationPath)
	require.NoError(t, err)
	require.NoError(t, firstDB.Exec(string(runtimeMigrationSQL)).Error)

	claimAt := time.Date(2026, 8, 28, 10, 0, 0, 0, time.UTC)
	task := newRecoveryTask(51, "postgres-mutual-claim", claimAt.Add(-time.Hour), claimAt)
	require.NoError(t, NewEvaluationTaskRepository(firstDB).CreateTask(context.Background(), task.TenantID, task))

	start := make(chan struct{})
	results := make(chan []*types.EvaluationTaskEntity, 2)
	errors := make(chan error, 2)
	var wg sync.WaitGroup
	claim := func(db *gorm.DB, ownerID string) {
		defer wg.Done()
		<-start
		claimed, claimErr := NewEvaluationTaskRepository(db).ClaimExpiredTasks(
			context.Background(),
			types.EvaluationTaskClaimExpiredCommand{
				OwnerID:        ownerID,
				Now:            claimAt,
				LeaseExpiresAt: claimAt.Add(time.Minute),
				Limit:          1,
			},
		)
		results <- claimed
		errors <- claimErr
	}

	wg.Add(2)
	go claim(firstDB, "recovery-owner-a")
	go claim(secondDB, "recovery-owner-b")
	close(start)
	wg.Wait()
	close(results)
	close(errors)

	for claimErr := range errors {
		require.NoError(t, claimErr)
	}
	var allClaimed []*types.EvaluationTaskEntity
	for claimed := range results {
		allClaimed = append(allClaimed, claimed...)
	}
	require.Len(t, allClaimed, 1)
	assert.Equal(t, task.ID, allClaimed[0].ID)
	assert.Contains(t, []string{"recovery-owner-a", "recovery-owner-b"}, allClaimed[0].OwnerID)
	assert.Equal(t, task.Version+1, allClaimed[0].Version)
}
