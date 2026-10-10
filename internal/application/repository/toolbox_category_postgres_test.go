package repository

import (
	"context"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"github.com/Tencent/WeKnora/internal/types"
)

// Uses the same disposable PostgreSQL opt-in as the other repository tests.
func newToolboxCategoryPostgresRepository(t *testing.T) *toolboxCategoryRepository {
	t.Helper()
	dsn := os.Getenv("WEKNORA_REPOSITORY_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set WEKNORA_REPOSITORY_TEST_POSTGRES_DSN to test concurrent tag writes")
	}
	config, err := pgx.ParseConfig(dsn)
	require.NoError(t, err)
	schema := "toolbox_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	config.RuntimeParams["search_path"] = schema
	sqlDB := stdlib.OpenDB(*config)
	t.Cleanup(func() { _ = sqlDB.Close() })
	db, err := gorm.Open(postgres.New(postgres.Config{Conn: sqlDB}), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.Exec("CREATE SCHEMA "+schema).Error)
	require.NoError(t, db.Exec("CREATE TABLE tenant_skill_catalog (id VARCHAR(36) PRIMARY KEY)").Error)
	require.NoError(t, db.Exec("CREATE TABLE mcp_services (id VARCHAR(36) PRIMARY KEY)").Error)
	migration, err := os.ReadFile("../../../migrations/versioned/000116_toolbox_categories.up.sql")
	require.NoError(t, err)
	require.NoError(t, db.Exec(string(migration)).Error)
	return &toolboxCategoryRepository{db: db}
}

func TestToolboxCategoryPostgresWriteConflicts(t *testing.T) {
	repo := newToolboxCategoryPostgresRepository(t)
	ctx := context.Background()
	require.NoError(t, repo.Create(ctx, &types.ToolboxCategory{ID: "one", TenantID: 1, Name: "Contracts"}))
	require.ErrorIs(t, repo.Create(ctx, &types.ToolboxCategory{
		ID: "two", TenantID: 1, Name: "Contracts",
	}), ErrToolboxCategoryNameExists)
	other := &types.ToolboxCategory{ID: "other", TenantID: 1, Name: "Customers"}
	require.NoError(t, repo.Create(ctx, other))
	other.Name = "Contracts"
	require.ErrorIs(t, repo.Update(ctx, other), ErrToolboxCategoryNameExists)
}

func TestToolboxCategoryPostgresIndependentWorkspaces(t *testing.T) {
	repo := newToolboxCategoryPostgresRepository(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, repo.db.Exec("INSERT INTO mcp_services VALUES ('builtin')").Error)
	require.NoError(t, repo.Create(ctx, &types.ToolboxCategory{ID: "one", TenantID: 1, Name: "one"}))
	require.NoError(t, repo.Create(ctx, &types.ToolboxCategory{ID: "two", TenantID: 2, Name: "two"}))
	deleted, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	var deletes atomic.Int32
	require.NoError(t, repo.db.Callback().Raw().After("gorm:raw").Register("hold_workspace_one", func(tx *gorm.DB) {
		if strings.HasPrefix(tx.Statement.SQL.String(), "DELETE FROM toolbox_category_") && deletes.Add(1) == 1 {
			close(deleted)
			select {
			case <-release:
			case <-ctx.Done():
			}
		}
	}))
	done := make(chan error, 1)
	go func() {
		done <- repo.ReplaceResourceCategories(ctx, 1, types.ToolboxResourceMCPService, "builtin", []string{"one"})
	}()
	select {
	case <-deleted:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	// This must complete while workspace one's transaction is still held.
	independent, stop := context.WithTimeout(ctx, time.Second)
	defer stop()
	require.NoError(t, repo.ReplaceResourceCategories(
		independent, 2, types.ToolboxResourceMCPService, "builtin", []string{"two"},
	))
	once.Do(func() { close(release) })
	require.NoError(t, <-done)
	for tenantID, name := range map[uint64]string{1: "one", 2: "two"} {
		saved, err := repo.ListByResourceIDs(ctx, tenantID, types.ToolboxResourceMCPService, []string{"builtin"})
		require.NoError(t, err)
		require.Equal(t, []string{name}, categoryNames(saved["builtin"]))
	}
}

func TestToolboxCategoryPostgresConcurrentReplacement(t *testing.T) {
	for _, resourceType := range []string{types.ToolboxResourceSkill, types.ToolboxResourceMCPService} {
		for _, initial := range []bool{false, true} {
			for _, sameTarget := range []bool{false, true} {
				name := resourceType
				if initial {
					name += "/existing"
				} else {
					name += "/empty"
				}
				if sameTarget {
					name += "/same_target"
				} else {
					name += "/different_targets"
				}
				t.Run(name, func(t *testing.T) {
					repo := newToolboxCategoryPostgresRepository(t)
					ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
					defer cancel()
					require.NoError(t, repo.db.Exec("INSERT INTO tenant_skill_catalog VALUES ('resource')").Error)
					require.NoError(t, repo.db.Exec("INSERT INTO mcp_services VALUES ('resource')").Error)
					for _, id := range []string{"initial", "first", "second"} {
						require.NoError(t, repo.Create(ctx, &types.ToolboxCategory{ID: id, TenantID: 1, Name: id}))
					}
					if initial {
						require.NoError(t, repo.ReplaceResourceCategories(
							ctx, 1, resourceType, "resource", []string{"initial"},
						))
					}
					firstDeleted, secondDeleted := make(chan struct{}), make(chan struct{})
					release := make(chan struct{})
					var once sync.Once
					defer once.Do(func() { close(release) })
					var deletes atomic.Int32
					holdFirst := func(tx *gorm.DB) {
						if !strings.HasPrefix(tx.Statement.SQL.String(), "DELETE FROM toolbox_category_") {
							return
						}
						switch deletes.Add(1) {
						case 1:
							close(firstDeleted)
							select {
							case <-release:
							case <-ctx.Done():
							}
						case 2:
							close(secondDeleted)
						}
					}
					require.NoError(t, repo.db.Callback().Raw().After("gorm:raw").Register(
						"hold_first_replacement", holdFirst,
					))
					firstDone, secondDone := make(chan error, 1), make(chan error, 1)
					go func() {
						firstDone <- repo.ReplaceResourceCategories(ctx, 1, resourceType, "resource", []string{"first"})
					}()
					select {
					case <-firstDeleted:
					case <-ctx.Done():
						t.Fatal(ctx.Err())
					}
					target := "second"
					if sameTarget {
						target = "first"
					}
					go func() {
						secondDone <- repo.ReplaceResourceCategories(ctx, 1, resourceType, "resource", []string{target})
					}()
					// Let the second transaction reach the delete or wait for the first
					// transaction's database lock before releasing the first writer.
					require.Eventually(t, func() bool {
						select {
						case <-secondDeleted:
							return true
						default:
						}
						var waiting int64
						err := repo.db.WithContext(ctx).Raw(
							"SELECT count(*) FROM pg_locks l JOIN pg_stat_activity a USING (pid) " +
								"WHERE a.datname = current_database() AND NOT l.granted",
						).Scan(&waiting).Error
						return err == nil && waiting > 0
					}, 5*time.Second, 10*time.Millisecond)
					once.Do(func() { close(release) })
					require.NoError(t, <-firstDone)
					require.NoError(t, <-secondDone)
					saved, err := repo.ListByResourceIDs(ctx, 1, resourceType, []string{"resource"})
					require.NoError(t, err)
					require.Equal(t, []string{target}, categoryNames(saved["resource"]))
				})
			}
		}
	}
}
