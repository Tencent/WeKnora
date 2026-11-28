package repository

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestListingCheckpointLifecycle(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			dir, version := "sqlite", "000054"
			driver := sqlite.Open(":memory:")
			if dialect == "postgres" {
				dsn := os.Getenv("WEKNORA_TEST_POSTGRES_DSN")
				if dsn == "" {
					t.Skip("no PostgreSQL test database")
				}
				driver = postgres.Open(dsn)
				dir, version = "versioned", "000138"
			}
			db, err := gorm.Open(driver)
			require.NoError(t, err)
			tx := db.Begin()
			t.Cleanup(func() { tx.Rollback() })
			// Isolate actual migration DDL in the transaction.
			if dialect == "postgres" {
				require.NoError(t, tx.Exec("CREATE SCHEMA listing_test").Error)
				require.NoError(t, tx.Exec("SET LOCAL search_path TO listing_test").Error)
			}
			require.NoError(t, tx.Exec(`CREATE TABLE sync_logs(
                id varchar(36) PRIMARY KEY, tenant_id bigint, data_source_id text, status text)`).Error)
			migrate := func(direction string) {
				file := version + "_sync_listing_pages." + direction + ".sql"
				sql, err := os.ReadFile(filepath.Join("..", "..", "..", "migrations", dir, file))
				require.NoError(t, err)
				require.NoError(t, tx.Exec(string(sql)).Error)
			}
			migrate("up")
			ctx := t.Context()
			for _, status := range []string{"success", "partial", "canceled"} {
				ds := &types.DataSource{ID: "source", TenantID: 1, Config: types.JSON(`{"resource_ids":["a"]}`)}
				repo := NewListingCheckpointRepository(tx)
				require.NoError(t, tx.Exec("INSERT INTO sync_logs VALUES (?,1,'source','running')", status).Error)
				store := repo.ForJob(ds, status)
				require.NoError(t, store.Save(ctx, "page", []byte("ok")))
				require.NoError(t, store.Save(ctx, "page", []byte("updated")))
				data, err := repo.ForJob(ds, status).Load(ctx, "page")
				require.NoError(t, err)
				require.Equal(t, "updated", string(data))
				ds.TenantID = 2
				other := repo.ForJob(ds, status)
				data, err = other.Load(ctx, "page")
				require.NoError(t, err)
				require.Nil(t, data)
				require.Error(t, other.Save(ctx, "page", nil))
				ds.TenantID = 1
				ds.Config = types.JSON(`{"resource_ids":["b"]}`)
				data, err = repo.ForJob(ds, status).Load(ctx, "page")
				require.NoError(t, err)
				require.Nil(t, data)
				require.NoError(t, tx.Exec("UPDATE sync_listing_pages SET payload='corrupt' WHERE job_id=?",
					status).Error)
				_, err = store.Load(ctx, "page")
				require.ErrorContains(t, err, "checksum")
				require.NoError(t, tx.Exec("UPDATE sync_logs SET status=? WHERE id=?", status, status).Error)
				data, err = store.Load(ctx, "page")
				require.NoError(t, err)
				require.Nil(t, data)
				require.Error(t, store.Save(ctx, "page", nil))
			}
			migrate("down")
		})
	}
}
