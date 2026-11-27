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
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func finalizeAttemptPostgresDB(t *testing.T) (*gorm.DB, string) {
	t.Helper()
	dsn := os.Getenv("WEKNORA_REPOSITORY_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set WEKNORA_REPOSITORY_TEST_POSTGRES_DSN for PostgreSQL finalizer tests")
	}
	admin, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	schema := "finalize_attempt_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	require.NoError(t, admin.Exec("CREATE SCHEMA "+schema).Error)
	t.Cleanup(func() {
		require.NoError(t, admin.Exec("DROP SCHEMA "+schema+" CASCADE").Error)
		sqlDB, err := admin.DB()
		require.NoError(t, err)
		require.NoError(t, sqlDB.Close())
	})
	separator := " "
	if strings.Contains(dsn, "://") {
		separator = "?"
		if strings.Contains(dsn, "?") {
			separator = "&"
		}
	}
	dsn += separator + "search_path=" + schema
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	require.NoError(t, db.AutoMigrate(&types.Knowledge{}, &types.KnowledgeProcessingSpan{}))
	return db, dsn
}

func seedFinalizerKnowledge(t *testing.T, db *gorm.DB) string {
	t.Helper()
	id := uuid.NewString()
	require.NoError(t, NewKnowledgeRepository(db).CreateKnowledge(context.Background(), &types.Knowledge{
		ID: id, TenantID: 1, KnowledgeBaseID: uuid.NewString(), Type: "file", ParseStatus: types.ParseStatusProcessing,
	}))
	return id
}

func seedFinalizerSpan(t *testing.T, db *gorm.DB, id string, attempt int) {
	t.Helper()
	require.NoError(t, db.Create(&types.KnowledgeProcessingSpan{
		KnowledgeID: id, Attempt: attempt, SpanID: uuid.NewString(), Name: "knowledge_processing",
		Kind: types.SpanKindRoot, Status: types.SpanStatusRunning,
	}).Error)
}

func TestFinalizeSubtaskFencesParseAttempt(t *testing.T) {
	for _, backend := range []string{"sqlite", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			var db *gorm.DB
			if backend == "sqlite" {
				db = setupKnowledgeTestDB(t)
				require.NoError(t, db.AutoMigrate(&types.Knowledge{}, &types.KnowledgeProcessingSpan{}))
			} else {
				db, _ = finalizeAttemptPostgresDB(t)
			}
			for _, tc := range []struct {
				name                         string
				attempt, latest, slots, want int
			}{
				{"stale", 1, 2, 2, 2},
				{"stale cannot promote", 1, 2, 1, 1},
				{"current", 2, 2, 2, 1},
				{"current promotes", 2, 2, 1, 0},
				{"legacy", 0, 2, 2, 1},
				{"tracking unavailable", 1, 0, 2, 1},
			} {
				t.Run(tc.name, func(t *testing.T) {
					id := seedFinalizerKnowledge(t, db)
					repo := NewKnowledgeRepository(db)
					seeded, err := repo.SetFinalizing(context.Background(), id, tc.slots)
					require.NoError(t, err)
					require.True(t, seeded)
					if tc.latest > 0 {
						seedFinalizerSpan(t, db, id, tc.latest)
					}
					count, promoted, err := repo.FinalizeSubtask(context.Background(), id, tc.attempt)
					require.NoError(t, err)
					require.Equal(t, tc.want, count)
					require.Equal(t, tc.want == 0, promoted)
					current, err := repo.GetKnowledgeByID(context.Background(), 1, id)
					require.NoError(t, err)
					require.Equal(t, tc.want, current.PendingSubtasksCount)
					if tc.want > 0 {
						require.Equal(t, types.ParseStatusFinalizing, current.ParseStatus)
					}
				})
			}
		})
	}
}

func TestFinalizeSubtaskAttemptCheckSerializesWithNewSeedPostgres(t *testing.T) {
	db, dsn := finalizeAttemptPostgresDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	id := seedFinalizerKnowledge(t, db)
	require.NoError(t, db.Model(&types.Knowledge{}).Where("id = ?", id).UpdateColumn("pending_subtasks_count", 1).Error)
	seedFinalizerSpan(t, db, id, 1)
	checked, release := make(chan struct{}), make(chan struct{})
	var releaseOnce, checkedOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	defer unblock()
	require.NoError(t, db.Callback().Row().After("gorm:row").Register("test:hold_attempt", func(tx *gorm.DB) {
		if strings.Contains(tx.Statement.SQL.String(), "MAX(attempt)") {
			checkedOnce.Do(func() { close(checked) })
			select {
			case <-release:
			case <-ctx.Done():
			}
		}
	}))
	defer func() { require.NoError(t, db.Callback().Row().Remove("test:hold_attempt")) }()
	finished := make(chan error, 1)
	go func() { _, _, err := NewKnowledgeRepository(db).FinalizeSubtask(ctx, id, 1); finished <- err }()
	select {
	case <-checked:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	seedFinalizerSpan(t, db, id, 2)
	name := "finalizer_seed_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	separator := " "
	if strings.Contains(dsn, "://") {
		separator = "&"
	}
	writer, err := gorm.Open(postgres.Open(dsn+separator+"application_name="+name), &gorm.Config{})
	require.NoError(t, err)
	sqlWriter, err := writer.DB()
	require.NoError(t, err)
	defer func() { require.NoError(t, sqlWriter.Close()) }()
	seeded := make(chan error, 1)
	go func() {
		ok, err := NewKnowledgeRepository(writer).SetFinalizing(ctx, id, 2)
		if err == nil && !ok {
			err = fmt.Errorf("new parse was not seeded")
		}
		seeded <- err
	}()
	for {
		select {
		case err := <-seeded:
			t.Fatalf("new seed escaped the finalizer's row lock: %v", err)
		default:
		}
		var waiting bool
		err := db.WithContext(ctx).Raw(
			"SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE application_name = ? AND wait_event_type = 'Lock')",
			name).Scan(&waiting).Error
		require.NoError(t, err)
		if waiting {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(5 * time.Millisecond):
		}
	}
	unblock()
	require.NoError(t, <-finished)
	require.NoError(t, <-seeded)
	current, err := NewKnowledgeRepository(db).GetKnowledgeByID(ctx, 1, id)
	require.NoError(t, err)
	require.Equal(t, 2, current.PendingSubtasksCount)
	require.Equal(t, types.ParseStatusFinalizing, current.ParseStatus)
}

func TestFinalizeSubtaskAttemptLookupFailureDoesNotRelease(t *testing.T) {
	db := setupKnowledgeTestDB(t) // No spans table: simulate a failed ownership read.
	id := insertProcessingKnowledge(t, db)
	repo := NewKnowledgeRepository(db)
	_, err := repo.SetFinalizing(context.Background(), id, 2)
	require.NoError(t, err)
	_, _, err = repo.FinalizeSubtask(context.Background(), id, 1)
	require.Error(t, err)
	var count int
	require.NoError(t, db.Model(&types.Knowledge{}).Where("id = ?", id).
		Select("pending_subtasks_count").Scan(&count).Error)
	require.Equal(t, 2, count)
}
