package repository

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func testKnowledgeClaims(t *testing.T, db *gorm.DB) {
	t.Helper()
	ctx := context.Background()
	repo := NewTaskPendingOpsRepository(db)
	selected := repo.(interfaces.TaskPendingOpsKnowledgeClaimer)
	for _, entry := range []struct{ key, op string }{
		{"other", "ingest"}, {"selected", "ingest"}, {"selected", "retract"},
	} {
		op := makePendingOp("wiki:ingest", types.TaskScopeKnowledgeBase, "kb", entry.op, entry.key, []byte(`{}`))
		op.FailCount = 3
		require.NoError(t, repo.Enqueue(ctx, op))
	}
	stale := time.Now().Add(-time.Hour)
	rows, err := selected.ClaimKnowledge(ctx, "wiki:ingest", types.TaskScopeKnowledgeBase, "kb", "selected", stale)
	require.NoError(t, err)
	require.Len(t, rows, 2)
	for _, row := range rows {
		require.Equal(t, "selected", row.DedupKey)
		require.Equal(t, 3, row.FailCount)
		require.NotNil(t, row.ClaimedAt)
		require.JSONEq(t, `{}`, string(row.Payload))
	}
	// A late sibling must wait for every row already held by the first batch.
	require.NoError(t, repo.Enqueue(ctx, makePendingOp("wiki:ingest", types.TaskScopeKnowledgeBase,
		"kb", "ingest", "selected", []byte(`{}`))))
	held, err := selected.ClaimKnowledge(ctx, "wiki:ingest", types.TaskScopeKnowledgeBase, "kb", "selected", stale)
	require.NoError(t, err)
	require.Empty(t, held)
	normal, err := repo.ClaimBatch(ctx, "wiki:ingest", types.TaskScopeKnowledgeBase, "kb", 10, stale)
	require.NoError(t, err)
	require.Len(t, normal, 1)
	require.Equal(t, "other", normal[0].DedupKey)
	_, err = selected.ClaimKnowledge(ctx, "wiki:ingest", types.TaskScopeKnowledgeBase, "kb", "", stale)
	require.Error(t, err)
}

func TestClaimKnowledgePreservesWholeKeyExclusion(t *testing.T) {
	testKnowledgeClaims(t, setupTaskQueueTestDB(t))
}

func progressPostgresDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := os.Getenv("WEKNORA_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("WEKNORA_TEST_POSTGRES_DSN is not set")
	}
	require.True(t, strings.Contains(dsn, "dbname=weknora_task_progress_test"),
		"use a disposable task-progress test DB")
	config := &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)}
	base, err := gorm.Open(postgres.Open(dsn), config)
	require.NoError(t, err)
	schema := fmt.Sprintf("task_progress_%d", time.Now().UnixNano())
	require.NoError(t, base.Exec("CREATE SCHEMA "+schema).Error)
	db, err := gorm.Open(postgres.Open(dsn+" search_path="+schema), config)
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&types.TaskPendingOp{}))
	t.Cleanup(func() {
		connection, err := db.DB()
		require.NoError(t, err)
		require.NoError(t, connection.Close())
		require.NoError(t, base.Exec("DROP SCHEMA "+schema+" CASCADE").Error)
		connection, err = base.DB()
		require.NoError(t, err)
		require.NoError(t, connection.Close())
	})
	return db
}

func TestClaimKnowledgePostgresWholeKeyAndConcurrentAutomaticClaims(t *testing.T) {
	t.Run("whole key", func(t *testing.T) { testKnowledgeClaims(t, progressPostgresDB(t)) })
	t.Run("concurrent", func(t *testing.T) {
		db := progressPostgresDB(t)
		repo := NewTaskPendingOpsRepository(db)
		for i := 0; i < 20; i++ {
			for range 2 {
				require.NoError(t, repo.Enqueue(context.Background(), makePendingOp("wiki:ingest",
					types.TaskScopeKnowledgeBase, "kb", "ingest", fmt.Sprint(i), []byte(`{}`))))
			}
		}
		var wg sync.WaitGroup
		var mu sync.Mutex
		seen := map[int64]bool{}
		var failures []error
		for worker := 0; worker < 10; worker++ {
			wg.Add(1)
			go func(worker int) {
				defer wg.Done()
				var rows []*types.TaskPendingOp
				var err error
				stale := time.Now().Add(-time.Hour)
				if worker%2 == 0 {
					rows, err = repo.(interfaces.TaskPendingOpsKnowledgeClaimer).ClaimKnowledge(
						context.Background(), "wiki:ingest", types.TaskScopeKnowledgeBase, "kb", "0", stale)
				} else {
					rows, err = repo.ClaimBatch(context.Background(),
						"wiki:ingest", types.TaskScopeKnowledgeBase, "kb", 2, stale)
				}
				mu.Lock()
				defer mu.Unlock()
				if err != nil {
					failures = append(failures, err)
					return
				}
				counts := map[string]int{}
				for _, row := range rows {
					if seen[row.ID] {
						failures = append(failures, fmt.Errorf("row %d claimed twice", row.ID))
					}
					seen[row.ID] = true
					counts[row.DedupKey]++
				}
				for key, count := range counts {
					if count != 2 {
						failures = append(failures, fmt.Errorf("split key %s", key))
					}
				}
			}(worker)
		}
		wg.Wait()
		require.Empty(t, failures)
		require.NotEmpty(t, seen)
	})
}
