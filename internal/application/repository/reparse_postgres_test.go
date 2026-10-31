package repository

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// This opt-in test requires a disposable database named weknora_reparse_test.
// It never uses the application's configured production database.
func TestPostgresReparseConcurrentOverlapAndMigrationRoundTrip(t *testing.T) {
	dsn := os.Getenv("WEKNORA_REPARSE_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set WEKNORA_REPARSE_TEST_POSTGRES_DSN to a disposable weknora_reparse_test database")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	var name string
	require.NoError(t, db.Raw("SELECT current_database()").Scan(&name).Error)
	require.Equal(t, "weknora_reparse_test", name, "refuse to mutate any other database")
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	root := filepath.Join("..", "..", "..", "migrations", "versioned")
	up, err := os.ReadFile(filepath.Join(root, "000134_reparse_batches.up.sql"))
	require.NoError(t, err)
	down, err := os.ReadFile(filepath.Join(root, "000134_reparse_batches.down.sql"))
	require.NoError(t, err)
	require.NoError(t, db.Exec(string(up)).Error)
	t.Cleanup(func() {
		require.NoError(t, db.Exec(string(down)).Error)
		require.NoError(t, db.Exec("DROP TABLE IF EXISTS knowledges").Error)
	})
	require.NoError(t, db.Exec(`CREATE TABLE knowledges (
		id VARCHAR(36) PRIMARY KEY, tenant_id BIGINT NOT NULL, knowledge_base_id VARCHAR(36) NOT NULL,
		type TEXT NOT NULL, title TEXT NOT NULL, source TEXT NOT NULL,
		parse_status TEXT NOT NULL, summary_status TEXT, deleted_at TIMESTAMP
	)`).Error)
	require.NoError(
		t,
		db.Exec(
			`INSERT INTO knowledges VALUES
             ('synthetic-doc', 1, 'synthetic-kb', 'manual', 'Synthetic', 'manual', 'failed', 'completed', NULL)`,
		).Error,
	)
	repo := NewKnowledgeRepository(db).(*knowledgeRepository)
	batches, submissions := repo.ReparseBatchStore(), repo.ReparseSubmissionStore()
	ctx := context.Background()
	for _, id := range []string{"batch-a", "batch-b"} {
		require.NoError(
			t,
			batches.CreateReparseBatch(
				ctx,
				&types.ReparseBatch{
					ID:              id,
					TenantID:        1,
					KnowledgeBaseID: "synthetic-kb",
					Mode:            "managed",
					ManifestSHA256:  id,
				},
				[]string{"synthetic-doc"},
			),
		)
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	for _, id := range []string{"batch-a", "batch-b"} {
		go func(id string) {
			<-start
			receipt := &types.ReparseBatchSubmission{
				ID:             id,
				BatchID:        id,
				TenantID:       1,
				IdempotencyKey: id,
				RequestSHA256:  id,
				State:          "created",
				TaskID:         id,
			}
			results <- submissions.CreateAndAssignReparseSubmission(ctx, receipt, []string{"synthetic-doc"})
		}(id)
	}
	close(start)
	success, rejected := 0, 0
	for range 2 {
		if err := <-results; err == nil {
			success++
		} else {
			require.ErrorIs(t, err, ErrReparseSubmissionMembersUnavailable)
			rejected++
		}
	}
	require.Equal(t, 1, success)
	require.Equal(t, 1, rejected)
	var count int64
	require.NoError(t, db.Model(&types.ReparseBatchSubmission{}).Count(&count).Error)
	require.Equal(t, int64(1), count, "losing claim must roll back its receipt")
	// The same aggregation used in SQLite must also execute on PostgreSQL.
	var receipt types.ReparseBatchSubmission
	require.NoError(t, db.First(&receipt).Error)
	require.NoError(t, submissions.MarkReparseSubmissionQueued(ctx, receipt.ID, receipt.TaskID))
	view, err := batches.GetReparseBatchView(ctx, 1, receipt.BatchID)
	require.NoError(t, err)
	require.Equal(t, int64(1), view.Counts.Queued, "old failure cannot count as a new failed submission")
	require.Equal(t, int64(1), view.SummaryCounts.Completed)
	_, _, err = batches.ListReparseBatchViews(ctx, 1, "synthetic-kb", 0, 20)
	require.NoError(t, err)
	// Exercise the actual PostgreSQL rollback/up DDL with the business row intact.
	require.NoError(t, db.Exec(string(down)).Error)
	var missing bool
	require.NoError(t, db.Raw("SELECT to_regclass('reparse_batches') IS NULL").Scan(&missing).Error)
	require.True(t, missing)
	require.NoError(t, db.Exec(string(up)).Error)
	var status string
	require.NoError(t, db.Raw("SELECT parse_status FROM knowledges WHERE id = 'synthetic-doc'").Scan(&status).Error)
	require.Equal(t, "failed", status)
}
