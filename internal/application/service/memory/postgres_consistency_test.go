package memory

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Opt in with a disposable test database. Each run uses an isolated schema.
func TestMemoryConsistencyPostgres(t *testing.T) {
	dsn := os.Getenv("WEKNORA_MEMORY_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set WEKNORA_MEMORY_TEST_POSTGRES_DSN to run PostgreSQL integration tests")
	}
	admin, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Discard})
	require.NoError(t, err)
	sqlAdmin, err := admin.DB()
	require.NoError(t, err)
	defer func() { require.NoError(t, sqlAdmin.Close()) }()
	require.NoError(t, admin.Exec(`CREATE EXTENSION IF NOT EXISTS "uuid-ossp"`).Error)
	schema := "memory_test_" + uuid.NewString()[:8]
	require.NoError(t, admin.Exec("CREATE SCHEMA "+schema).Error)
	defer admin.Exec("DROP SCHEMA " + schema + " CASCADE")
	db, err := gorm.Open(postgres.Open(dsn+" search_path="+schema+",public"), &gorm.Config{Logger: logger.Discard})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	defer func() { require.NoError(t, sqlDB.Close()) }()

	execMemoryMigration(t, db, "../../../../migrations/versioned/000084_memory.up.sql")
	execMemoryMigration(t, db, "../../../../migrations/versioned/000094_memory_consistency.up.sql")
	repo := repository.NewMemoryRepository(db)
	ctx := context.Background()
	scope := interfaces.MemoryScope{TenantID: 7, SubjectID: "alice"}
	_, err = repo.EnsureSubject(ctx, scope)
	require.NoError(t, err)
	var wg sync.WaitGroup
	errs := make(chan error, 20)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, err := repo.EnqueuePendingSession(ctx, scope, "session", time.Minute)
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	batch, err := repo.ClaimPendingSessions(ctx, scope, "", "worker", time.Minute)
	require.NoError(t, err)
	require.Len(t, batch.Sessions, 1)
	require.EqualValues(t, 20, batch.Sessions[0].Revision)
	duplicate, err := repo.ClaimPendingSessions(ctx, scope, "", "duplicate", time.Minute)
	require.NoError(t, err)
	require.False(t, duplicate.RetryAt.IsZero())
	end := types.MemoryMessageCursor{At: time.Now().UTC().Truncate(time.Microsecond), ID: "last"}
	for i := 1; i <= 3; i++ {
		skip, err := repo.RecordExtractionFailure(ctx, scope, "worker", interfaces.MemoryExtractionFailure{
			Session: batch.Sessions[0], End: end, Code: "invalid_model_output",
		})
		require.NoError(t, err)
		require.Equal(t, i == 3, skip)
	}
	require.NoError(t, repo.CheckpointExtraction(ctx, scope, "worker", batch.Sessions[0], end, true))
	require.NoError(t, repo.FinishExtraction(ctx, scope, "worker"))
	pending, err := repo.HasPendingExtraction(ctx, scope)
	require.NoError(t, err)
	require.False(t, pending)
}
