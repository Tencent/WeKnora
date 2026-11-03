package repository

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestKnowledgeFolderV3TransferRejectsRenamedSnapshot(t *testing.T) {
	db := setupKnowledgeTestDB(t)
	require.NoError(t, db.Exec(
		"CREATE TABLE tenants (id INTEGER PRIMARY KEY, storage_used BIGINT NOT NULL, deleted_at DATETIME)",
	).Error)
	require.NoError(t, db.Exec("INSERT INTO tenants (id, storage_used) VALUES (7, 100)").Error)
	repo := NewKnowledgeRepository(db)
	ctx := context.Background()
	require.NoError(t, repo.CreateKnowledgeFolder(ctx, 7, "source", "old"))
	require.NoError(t, repo.CreateKnowledgeFolder(ctx, 7, "source", "old/child"))
	require.NoError(t, db.Exec(`INSERT INTO knowledges
		(id, tenant_id, knowledge_base_id, folder_path, parse_status, storage_size, updated_at)
		VALUES ('transfer-doc', 7, 'source', 'old/child', 'completed', 10, '2020-01-01T00:00:00Z')`).Error)
	before, err := repo.GetKnowledgeByID(ctx, 7, "transfer-doc")
	require.NoError(t, err)
	after := *before
	after.KnowledgeBaseID = "target"
	after.StorageSize = 17

	affected, err := repo.RenameKnowledgeFolderPath(ctx, 7, "source", "old", "renamed")
	require.NoError(t, err)
	require.EqualValues(t, 1, affected)
	err = repo.UpdateKnowledgeForTransfer(ctx, before, &after)
	require.ErrorContains(t, err, "knowledge changed during transfer")
	current, err := repo.GetKnowledgeByID(ctx, 7, before.ID)
	require.NoError(t, err)
	require.Equal(t, "source", current.KnowledgeBaseID)
	require.Equal(t, "renamed/child", current.FolderPath)
	require.EqualValues(t, 10, current.StorageSize)
	var used int64
	require.NoError(t, db.Raw("SELECT storage_used FROM tenants WHERE id = 7").Scan(&used).Error)
	require.EqualValues(t, 100, used, "failed CAS must not adjust tenant accounting")

	// A deliberate retry with a fresh checkpoint keeps the renamed path.
	fresh := *current
	fresh.KnowledgeBaseID = "target"
	fresh.StorageSize = 17
	require.NoError(t, repo.UpdateKnowledgeForTransfer(ctx, current, &fresh))
	stored, err := repo.GetKnowledgeByID(ctx, 7, before.ID)
	require.NoError(t, err)
	require.Equal(t, "target", stored.KnowledgeBaseID)
	require.Equal(t, "renamed/child", stored.FolderPath)
	require.EqualValues(t, 17, stored.StorageSize)
	require.True(t, fresh.UpdatedAt.Equal(stored.UpdatedAt), "return the database timestamp for the next checkpoint")
	require.NoError(t, db.Raw("SELECT storage_used FROM tenants WHERE id = 7").Scan(&used).Error)
	require.EqualValues(t, 107, used)
}

// These tests run only against an explicitly selected synthetic PostgreSQL 15+
// database. Every invocation creates and drops only its own random schema.
func folderV3TransferPGDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := os.Getenv("FOLDER_TEST_POSTGRES_DSN")
	if dsn == "" {
		dsn = os.Getenv("FOLDER_TEST_PG17_DSN")
	}
	if dsn == "" {
		t.Skip("requires FOLDER_TEST_POSTGRES_DSN for an isolated PostgreSQL 15+ Unix-socket database")
	}
	cfg, err := pgx.ParseConfig(dsn)
	require.NoError(t, err)
	require.True(t, filepath.IsAbs(cfg.Host), "only a local Unix socket is allowed")
	require.True(t, strings.HasPrefix(cfg.Database, "folder_"), "requires a dedicated folder_* synthetic database")
	for _, fallback := range cfg.Fallbacks {
		require.True(t, filepath.IsAbs(fallback.Host), "TCP fallback is not permitted")
	}
	cfg.ConnectTimeout = 5 * time.Second
	cfg.RuntimeParams = map[string]string{
		"search_path": "pg_catalog", "statement_timeout": "15000", "lock_timeout": "10000",
		"default_transaction_isolation": "read committed",
	}
	control := stdlib.OpenDB(*cfg)
	t.Cleanup(func() { _ = control.Close() })
	var version int
	require.NoError(t, control.QueryRow("SHOW server_version_num").Scan(&version))
	require.GreaterOrEqual(t, version, 150000, "requires PostgreSQL 15 or later")
	schema := "folder_v3_transfer_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	_, err = control.Exec(`CREATE SCHEMA "` + schema + `"`)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, err := control.Exec(`DROP SCHEMA "` + schema + `" CASCADE`)
		if err != nil {
			t.Errorf("clean test schema: %v", err)
		}
	})
	cfg.RuntimeParams["search_path"] = schema + ",pg_catalog"
	cfg.RuntimeParams["application_name"] = schema
	connections := stdlib.OpenDB(*cfg)
	connections.SetMaxOpenConns(12)
	t.Cleanup(func() { _ = connections.Close() })
	db, err := gorm.Open(postgres.New(postgres.Config{Conn: connections}), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	require.NoError(t, err)
	require.NoError(t, db.Exec(`CREATE TABLE knowledges (
		id VARCHAR(36) PRIMARY KEY, tenant_id BIGINT NOT NULL, knowledge_base_id VARCHAR(36) NOT NULL,
		folder_path VARCHAR(1024) NOT NULL DEFAULT '', parse_status VARCHAR(32) NOT NULL DEFAULT 'completed',
		storage_size BIGINT NOT NULL DEFAULT 0, metadata JSONB, enable_status VARCHAR(32),
		embedding_model_id VARCHAR(64), description TEXT, processed_at TIMESTAMPTZ, error_message TEXT,
		deleted_at TIMESTAMPTZ, updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
	)`).Error)
	require.NoError(t, db.Exec(
		"CREATE TABLE tenants (id BIGINT PRIMARY KEY, storage_used BIGINT NOT NULL, deleted_at TIMESTAMPTZ)",
	).Error)
	require.NoError(t, db.Exec("INSERT INTO tenants (id, storage_used) VALUES (7, 100)").Error)
	require.NoError(t, db.Exec(`CREATE TABLE knowledge_bases (id VARCHAR(36) PRIMARY KEY, `+
		`tenant_id BIGINT NOT NULL, deleted_at TIMESTAMPTZ)`).Error)
	require.NoError(t, db.Exec(`INSERT INTO knowledge_bases(id, tenant_id) VALUES ('source', 7), ('target', 7)`).Error)
	up, err := os.ReadFile(filepath.Join(
		"..", "..", "..", "migrations", "versioned", "000136_knowledge_folders.up.sql",
	))
	require.NoError(t, err)
	require.NoError(t, db.Exec(string(up)).Error)
	return db
}

type folderV3PauseRenameKey struct{}

func TestKnowledgeFolderV3PG17TransferWaitsForBothKnowledgeBases(t *testing.T) {
	for _, renameKB := range []string{"source", "target"} {
		t.Run(renameKB, func(t *testing.T) {
			db := folderV3TransferPGDB(t)
			repo := NewKnowledgeRepository(db)
			ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
			defer cancel()
			require.NoError(t, repo.CreateKnowledgeFolder(ctx, 7, renameKB, "old"))
			require.NoError(t, repo.CreateKnowledgeFolder(ctx, 7, renameKB, "old/child"))
			require.NoError(t, db.Exec(`INSERT INTO knowledges
				(id, tenant_id, knowledge_base_id, folder_path, parse_status, storage_size, updated_at)
				VALUES ('transfer-doc', 7, 'source', 'old/child', 'completed', 10, '2020-01-01T00:00:00Z')`).Error)
			before, err := repo.GetKnowledgeByID(ctx, 7, "transfer-doc")
			require.NoError(t, err)
			after := *before
			after.KnowledgeBaseID = "target"
			after.StorageSize = 17
			entered, release := make(chan struct{}), make(chan struct{})
			var enteredOnce, releaseOnce sync.Once
			releaseRename := func() { releaseOnce.Do(func() { close(release) }) }
			defer releaseRename()
			callback := "folder_v3_pause_rename"
			require.NoError(t, db.Callback().Query().After("gorm:query").Register(callback, func(tx *gorm.DB) {
				if tx.Statement.Context.Value(folderV3PauseRenameKey{}) != true || tx.Statement.Table != "knowledges" {
					return
				}
				enteredOnce.Do(func() {
					close(entered)
					select {
					case <-release:
					case <-ctx.Done():
						_ = tx.AddError(ctx.Err())
					}
				})
			}))
			defer func() { require.NoError(t, db.Callback().Query().Remove(callback)) }()
			renameResult := make(chan error, 1)
			go func() {
				_, err := repo.RenameKnowledgeFolderPath(
					context.WithValue(ctx, folderV3PauseRenameKey{}, true), 7, renameKB, "old", "new",
				)
				renameResult <- err
			}()
			select {
			case <-entered:
			case <-ctx.Done():
				t.Fatal("rename did not reach its locked document snapshot")
			}
			transferResult := make(chan error, 1)
			go func() { transferResult <- repo.UpdateKnowledgeForTransfer(ctx, before, &after) }()
			folderV3RequireAdvisoryWait(ctx, t, db, transferResult)
			releaseRename()
			require.NoError(t, <-renameResult)
			transferErr := <-transferResult
			stored, err := repo.GetKnowledgeByID(ctx, 7, before.ID)
			require.NoError(t, err)
			var used int64
			require.NoError(t, db.Raw("SELECT storage_used FROM tenants WHERE id = 7").Scan(&used).Error)
			if renameKB == "source" {
				require.ErrorContains(t, transferErr, "knowledge changed during transfer")
				require.Equal(t, "source", stored.KnowledgeBaseID)
				require.Equal(t, "new/child", stored.FolderPath)
				require.EqualValues(t, 10, stored.StorageSize)
				require.EqualValues(t, 100, used)
			} else {
				require.NoError(t, transferErr)
				require.Equal(t, "target", stored.KnowledgeBaseID)
				// The transfer commits after the target rename, so it adds the
				// source document at its unchanged relative path.
				require.Equal(t, "old/child", stored.FolderPath)
				require.EqualValues(t, 17, stored.StorageSize)
				require.EqualValues(t, 107, used)
			}
			var paths []string
			require.NoError(t, db.Model(&types.KnowledgeFolder{}).
				Where("tenant_id = ? AND knowledge_base_id = ?", 7, renameKB).
				Order("path").Pluck("path", &paths).Error)
			require.Equal(t, []string{"new", "new/child"}, paths)
		})
	}
}

func folderV3RequireAdvisoryWait(ctx context.Context, t *testing.T, db *gorm.DB, result <-chan error) {
	t.Helper()
	var application string
	require.NoError(t, db.Raw("SELECT current_setting('application_name')").Scan(&application).Error)
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case err := <-result:
			t.Fatalf("transfer finished before the held folder lock was released: %v", err)
		case <-ticker.C:
			var waiting bool
			require.NoError(t, db.Raw(`SELECT EXISTS (SELECT 1 FROM pg_stat_activity
				WHERE datname = current_database() AND application_name = ? AND pid <> pg_backend_pid()
				AND state = 'active' AND wait_event_type = 'Lock' AND wait_event = 'advisory')`, application).
				Scan(&waiting).Error)
			if waiting {
				return
			}
		case <-ctx.Done():
			t.Fatal("transfer did not reach an observable PostgreSQL advisory lock wait")
		}
	}
}

func TestKnowledgeFolderV3PG17OppositeTransfersComplete(t *testing.T) {
	db := folderV3TransferPGDB(t)
	repo := NewKnowledgeRepository(db)
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	start := make(chan struct{})
	results := make(chan error, 16)
	for i := 0; i < 16; i++ {
		source, target := "a", "b"
		if i%2 == 1 {
			source, target = target, source
		}
		id := fmt.Sprintf("transfer-%02d", i)
		require.NoError(t, db.Exec(`INSERT INTO knowledges
			(id, tenant_id, knowledge_base_id, folder_path, parse_status)
			VALUES (?, 7, ?, 'same/child', 'completed')`, id, source).Error)
		before, err := repo.GetKnowledgeByID(ctx, 7, id)
		require.NoError(t, err)
		after := *before
		after.KnowledgeBaseID = target
		go func(before *types.Knowledge, after types.Knowledge) {
			<-start
			results <- repo.UpdateKnowledgeForTransfer(ctx, before, &after)
		}(before, after)
	}
	close(start)
	for i := 0; i < 16; i++ {
		select {
		case err := <-results:
			require.NoError(t, err)
		case <-ctx.Done():
			t.Fatal("opposite-direction transfers did not complete within the deadline")
		}
	}
	for i := 0; i < 16; i++ {
		wantKB := "b"
		if i%2 == 1 {
			wantKB = "a"
		}
		stored, err := repo.GetKnowledgeByID(ctx, 7, fmt.Sprintf("transfer-%02d", i))
		require.NoError(t, err)
		require.Equal(t, wantKB, stored.KnowledgeBaseID)
		require.Equal(t, "same/child", stored.FolderPath)
	}
}
