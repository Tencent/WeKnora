package repository

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestSyncLogRetentionPerSource(t *testing.T) {
	for _, backend := range []string{"sqlite", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			dialect := sqlite.Open(":memory:")
			if backend == "postgres" {
				dsn := os.Getenv("WEKNORA_REPOSITORY_TEST_POSTGRES_DSN")
				if dsn == "" {
					t.Skip("set WEKNORA_REPOSITORY_TEST_POSTGRES_DSN for PostgreSQL sync log retention tests")
				}
				dialect = postgres.Open(dsn)
			}
			db, err := gorm.Open(dialect, &gorm.Config{})
			require.NoError(t, err)
			sqlDB, err := db.DB()
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
			db = db.Begin()
			require.NoError(t, db.Error)
			t.Cleanup(func() { require.NoError(t, db.Rollback().Error) })
			// Temporary tables exercise both dialects without modifying any
			// existing deployment data when an integration-test DSN is supplied.
			require.NoError(t, db.Exec(`CREATE TEMP TABLE data_sources (
				id TEXT PRIMARY KEY, tenant_id BIGINT, sync_log_retention_days INT, deleted_at TIMESTAMP
			)`).Error)
			require.NoError(t, db.Exec(`CREATE TEMP TABLE sync_logs (
				id TEXT PRIMARY KEY, data_source_id TEXT, tenant_id BIGINT, status TEXT,
				started_at TIMESTAMP, finished_at TIMESTAMP
			)`).Error)
			now := time.Now().UTC()
			for _, source := range []struct {
				id      string
				tenant  uint64
				days    int
				deleted bool
			}{
				{"short", 1, 1, false},
				{"long", 2, 30, false},
				{"unrelated", 3, 90, false},
				{"unset", 1, 0, false},
				{"negative", 1, -1, false},
				{"deleted-long", 2, 90, true},
				{"deleted-short", 2, 1, true},
				{"large", 1, 1 << 30, false},
			} {
				var deletedAt *time.Time
				if source.deleted {
					deletedAt = &now
				}
				require.NoError(t, db.Exec("INSERT INTO data_sources VALUES (?, ?, ?, ?)",
					source.id, source.tenant, source.days, deletedAt).Error)
			}
			var want []string
			for _, log := range []struct {
				id          string
				source      string
				tenant      uint64
				status      string
				ageDays     int
				finishedAge int
				keep        bool
			}{
				{"short-expired", "short", 1, types.SyncLogStatusSuccess, 2, 2, false},
				{"partial-expired", "short", 1, types.SyncLogStatusPartial, 2, 2, false},
				{"canceled-expired", "short", 1, types.SyncLogStatusCanceled, 2, 2, false},
				{"recent", "short", 1, types.SyncLogStatusSuccess, 0, 0, true},
				{"recently-finished", "short", 1, types.SyncLogStatusSuccess, 60, 0, true},
				{"legacy-expired", "short", 1, types.SyncLogStatusSuccess, 2, -1, false},
				{"running", "short", 1, types.SyncLogStatusRunning, 60, -1, true},
				{"pending", "short", 1, "pending", 60, -1, true},
				{"retry", "short", 1, "retry", 60, -1, true},
				{"failed-retryable", "short", 1, types.SyncLogStatusFailed, 60, 60, true},
				{"long-retained", "long", 2, types.SyncLogStatusSuccess, 2, 2, true},
				{"long-expired", "long", 2, types.SyncLogStatusSuccess, 31, 31, false},
				{"unrelated-retained", "unrelated", 3, types.SyncLogStatusSuccess, 60, 60, true},
				{"unset-expired", "unset", 1, types.SyncLogStatusSuccess, 31, 31, false},
				{"negative-retained", "negative", 1, types.SyncLogStatusSuccess, 2, 2, true},
				{"negative-expired", "negative", 1, types.SyncLogStatusSuccess, 31, 31, false},
				{"deleted-policy-retained", "deleted-long", 2, types.SyncLogStatusSuccess, 60, 60, true},
				{"deleted-policy-expired", "deleted-short", 2, types.SyncLogStatusFailed, 2, 2, false},
				{"orphan-retained", "missing", 1, types.SyncLogStatusSuccess, 2, 2, true},
				{"orphan-expired", "missing", 1, types.SyncLogStatusFailed, 31, 31, false},
				{"tenant-mismatch-retained", "short", 9, types.SyncLogStatusSuccess, 60, 60, true},
				{"large-policy-retained", "large", 1, types.SyncLogStatusSuccess, 60, 60, true},
			} {
				var finishedAt *time.Time
				if log.finishedAge >= 0 {
					finished := now.AddDate(0, 0, -log.finishedAge)
					finishedAt = &finished
				}
				require.NoError(t, db.Exec("INSERT INTO sync_logs VALUES (?, ?, ?, ?, ?, ?)",
					log.id, log.source, log.tenant, log.status, now.AddDate(0, 0, -log.ageDays), finishedAt).Error)
				if log.keep {
					want = append(want, log.id)
				}
			}
			repo := NewSyncLogRepository(db)
			for _, defaultDays := range []int{30, 0} {
				require.NoError(t, repo.CleanupOldLogs(context.Background(), defaultDays))
				var got []string
				require.NoError(t, db.Table("sync_logs").Pluck("id", &got).Error)
				require.ElementsMatch(t, want, got, "cleanup must be isolated and idempotent")
			}
		})
	}
}
