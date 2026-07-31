package database

import (
	"crypto/sha256"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/database/sqlitemigrations"
	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	knowledgeFolderIndexPendingTable       = "knowledge_folder_index_pending"
	knowledgeFolderIndexPendingUnique      = "uq_knowledge_folder_index_pending_scope"
	knowledgeFolderIndexPendingScopeUpdate = "idx_knowledge_folder_index_pending_scope_updated"
)

type sqliteKnowledgeFolderIndexPendingColumn struct {
	columnType   string
	notNull      int
	defaultValue sql.NullString
	primaryKey   int
}

func compactKnowledgeFolderIndexPendingSQL(value string) string {
	compacted := strings.Join(strings.Fields(strings.ToLower(value)), " ")
	return strings.NewReplacer(
		"( ", "(",
		" )", ")",
	).Replace(compacted)
}

func readSQLiteKnowledgeFolderIndexPendingColumns(
	t *testing.T,
	db *sql.DB,
) map[string]sqliteKnowledgeFolderIndexPendingColumn {
	t.Helper()

	rows, err := db.Query(`PRAGMA table_info(knowledge_folder_index_pending)`)
	require.NoError(t, err)
	defer rows.Close()

	columns := make(map[string]sqliteKnowledgeFolderIndexPendingColumn)
	for rows.Next() {
		var cid int
		var name string
		var column sqliteKnowledgeFolderIndexPendingColumn
		require.NoError(t, rows.Scan(
			&cid,
			&name,
			&column.columnType,
			&column.notNull,
			&column.defaultValue,
			&column.primaryKey,
		))
		columns[name] = column
	}
	require.NoError(t, rows.Err())
	return columns
}

func TestKnowledgeFolderIndexPendingMigrationStaticContract(t *testing.T) {
	postgresUp := compactKnowledgeFolderIndexPendingSQL(readKnowledgeFolderMigration(
		t,
		"versioned",
		"000072_knowledge_folder_index_pending.up.sql",
	))
	postgresDown := compactKnowledgeFolderIndexPendingSQL(readKnowledgeFolderMigration(
		t,
		"versioned",
		"000072_knowledge_folder_index_pending.down.sql",
	))
	sqliteUp := compactKnowledgeFolderIndexPendingSQL(readKnowledgeFolderMigration(
		t,
		"sqlite",
		"000002_knowledge_folder_index_pending.up.sql",
	))
	sqliteDown := compactKnowledgeFolderIndexPendingSQL(readKnowledgeFolderMigration(
		t,
		"sqlite",
		"000002_knowledge_folder_index_pending.down.sql",
	))

	for dialect, up := range map[string]string{
		"postgres": postgresUp,
		"sqlite":   sqliteUp,
	} {
		t.Run("M35-01_"+dialect+"_uses_frozen_singular_table_name", func(t *testing.T) {
			assert.Contains(t, up, "create table if not exists "+knowledgeFolderIndexPendingTable)
			assert.NotContains(t, up, "knowledge_folder_index_pendings")
		})

		t.Run("M35-02_"+dialect+"_has_exact_columns_and_required_constraints", func(t *testing.T) {
			for _, fragment := range []string{
				"id varchar(36) not null primary key",
				"knowledge_base_id varchar(36) not null",
				"knowledge_id varchar(36) not null",
				"target_folder_id varchar(36) not null default ''",
				"not null check (requested_version > 0)",
				"not null default current_timestamp",
			} {
				assert.Contains(t, up, fragment, fragment)
			}
			if dialect == "postgres" {
				for _, fragment := range []string{
					"tenant_id bigint not null",
					"requested_version bigint not null check (requested_version > 0)",
					"created_at timestamp with time zone not null default current_timestamp",
					"updated_at timestamp with time zone not null default current_timestamp",
				} {
					assert.Contains(t, up, fragment, fragment)
				}
			} else {
				for _, fragment := range []string{
					"tenant_id integer not null",
					"requested_version integer not null check (requested_version > 0)",
					"created_at datetime not null default current_timestamp",
					"updated_at datetime not null default current_timestamp",
				} {
					assert.Contains(t, up, fragment, fragment)
				}
			}
		})

		t.Run("M35-05_"+dialect+"_uses_scoped_knowledge_uniqueness", func(t *testing.T) {
			assert.Contains(
				t,
				up,
				"constraint "+knowledgeFolderIndexPendingUnique+
					" unique (tenant_id, knowledge_base_id, knowledge_id)",
			)
		})

		t.Run("M35-06_"+dialect+"_has_only_the_frozen_auxiliary_index", func(t *testing.T) {
			assert.Contains(
				t,
				up,
				"create index if not exists "+knowledgeFolderIndexPendingScopeUpdate+
					" on "+knowledgeFolderIndexPendingTable+
					" (tenant_id, knowledge_base_id, updated_at, knowledge_id)",
			)
			assert.Equal(t, 1, strings.Count(up, "create index if not exists"))
			assert.NotContains(t, up, "create unique index")
		})

		t.Run("M35-07_"+dialect+"_does_not_grow_into_a_generic_queue", func(t *testing.T) {
			for _, forbidden := range []string{
				"foreign key",
				" references ",
				"deleted_at",
				"claimed_at",
				"fail_count",
				"retry",
				"json",
				"payload",
				" op ",
				"operation",
			} {
				assert.NotContains(t, up, forbidden, forbidden)
			}
		})
	}

	for dialect, down := range map[string]string{
		"postgres": postgresDown,
		"sqlite":   sqliteDown,
	} {
		t.Run("M35-08_"+dialect+"_down_drops_index_then_table", func(t *testing.T) {
			dropIndex := "drop index if exists " + knowledgeFolderIndexPendingScopeUpdate
			dropTable := "drop table if exists " + knowledgeFolderIndexPendingTable
			assert.Contains(t, down, dropIndex)
			assert.Contains(t, down, dropTable)
			assert.Less(t, strings.Index(down, dropIndex), strings.Index(down, dropTable))
		})
	}
}

func TestSQLiteKnowledgeFolderIndexPendingMigrationSchemaAndRollback(t *testing.T) {
	db, err := sql.Open(
		"sqlite3",
		filepath.Join(t.TempDir(), "knowledge-folder-index-pending.db"),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	up := readKnowledgeFolderMigration(
		t,
		"sqlite",
		"000002_knowledge_folder_index_pending.up.sql",
	)
	_, err = db.Exec(up)
	require.NoError(t, err)

	t.Run("M35-02_exact_columns_types_and_nullability", func(t *testing.T) {
		columns := readSQLiteKnowledgeFolderIndexPendingColumns(t, db)
		require.Equal(t, map[string]string{
			"id":                "VARCHAR(36)",
			"tenant_id":         "INTEGER",
			"knowledge_base_id": "VARCHAR(36)",
			"knowledge_id":      "VARCHAR(36)",
			"target_folder_id":  "VARCHAR(36)",
			"requested_version": "INTEGER",
			"created_at":        "DATETIME",
			"updated_at":        "DATETIME",
		}, map[string]string{
			"id":                columns["id"].columnType,
			"tenant_id":         columns["tenant_id"].columnType,
			"knowledge_base_id": columns["knowledge_base_id"].columnType,
			"knowledge_id":      columns["knowledge_id"].columnType,
			"target_folder_id":  columns["target_folder_id"].columnType,
			"requested_version": columns["requested_version"].columnType,
			"created_at":        columns["created_at"].columnType,
			"updated_at":        columns["updated_at"].columnType,
		})
		assert.Len(t, columns, 8)
		for name, column := range columns {
			assert.Equal(t, 1, column.notNull, name)
		}
		assert.Equal(t, 1, columns["id"].primaryKey)
	})

	t.Run("M35-03_root_target_and_timestamps_have_database_defaults", func(t *testing.T) {
		columns := readSQLiteKnowledgeFolderIndexPendingColumns(t, db)
		assert.Equal(t, "''", columns["target_folder_id"].defaultValue.String)
		assert.Equal(t, "CURRENT_TIMESTAMP", columns["created_at"].defaultValue.String)
		assert.Equal(t, "CURRENT_TIMESTAMP", columns["updated_at"].defaultValue.String)

		_, err := db.Exec(`
			INSERT INTO knowledge_folder_index_pending (
				id,
				tenant_id,
				knowledge_base_id,
				knowledge_id,
				requested_version
			) VALUES ('pending-1', 1, 'kb-1', 'knowledge-1', 1)
		`)
		require.NoError(t, err)

		var targetFolderID string
		var requestedVersion uint64
		var createdAtPresent, updatedAtPresent bool
		err = db.QueryRow(`
			SELECT
				target_folder_id,
				requested_version,
				created_at IS NOT NULL,
				updated_at IS NOT NULL
			FROM knowledge_folder_index_pending
			WHERE id = 'pending-1'
		`).Scan(
			&targetFolderID,
			&requestedVersion,
			&createdAtPresent,
			&updatedAtPresent,
		)
		require.NoError(t, err)
		assert.Empty(t, targetFolderID)
		assert.Equal(t, uint64(1), requestedVersion)
		assert.True(t, createdAtPresent)
		assert.True(t, updatedAtPresent)
	})

	t.Run("M35-04_requested_version_must_be_positive", func(t *testing.T) {
		_, err := db.Exec(`
			INSERT INTO knowledge_folder_index_pending (
				id,
				tenant_id,
				knowledge_base_id,
				knowledge_id,
				requested_version
			) VALUES ('pending-zero', 1, 'kb-1', 'knowledge-zero', 0)
		`)
		require.Error(t, err)
	})

	t.Run("M35-05_uniqueness_is_scoped_by_tenant_and_knowledge_base", func(t *testing.T) {
		_, err := db.Exec(`
			INSERT INTO knowledge_folder_index_pending (
				id,
				tenant_id,
				knowledge_base_id,
				knowledge_id,
				target_folder_id,
				requested_version
			) VALUES ('pending-duplicate', 1, 'kb-1', 'knowledge-1', 'folder-2', 2)
		`)
		require.Error(t, err)

		_, err = db.Exec(`
			INSERT INTO knowledge_folder_index_pending (
				id,
				tenant_id,
				knowledge_base_id,
				knowledge_id,
				requested_version
			) VALUES
				('pending-other-tenant', 2, 'kb-1', 'knowledge-1', 1),
				('pending-other-kb', 1, 'kb-2', 'knowledge-1', 1)
		`)
		require.NoError(t, err)
	})

	t.Run("M35-06_explicit_auxiliary_index_has_exact_column_order", func(t *testing.T) {
		var indexSQL string
		err := db.QueryRow(`
			SELECT sql
			FROM sqlite_master
			WHERE type = 'index'
			  AND name = ?
		`, knowledgeFolderIndexPendingScopeUpdate).Scan(&indexSQL)
		require.NoError(t, err)
		assert.Contains(
			t,
			compactKnowledgeFolderIndexPendingSQL(indexSQL),
			"on "+knowledgeFolderIndexPendingTable+
				" (tenant_id, knowledge_base_id, updated_at, knowledge_id)",
		)

		var explicitIndexCount int
		err = db.QueryRow(`
			SELECT COUNT(*)
			FROM sqlite_master
			WHERE type = 'index'
			  AND tbl_name = ?
			  AND sql IS NOT NULL
		`, knowledgeFolderIndexPendingTable).Scan(&explicitIndexCount)
		require.NoError(t, err)
		assert.Equal(t, 1, explicitIndexCount)
	})

	t.Run("M35-07_has_no_foreign_keys_or_queue_columns", func(t *testing.T) {
		columns := readSQLiteKnowledgeFolderIndexPendingColumns(t, db)
		for _, forbidden := range []string{
			"deleted_at",
			"claimed_at",
			"fail_count",
			"retry_count",
			"payload",
			"op",
		} {
			assert.NotContains(t, columns, forbidden)
		}

		rows, err := db.Query(`PRAGMA foreign_key_list(knowledge_folder_index_pending)`)
		require.NoError(t, err)
		defer rows.Close()
		assert.False(t, rows.Next())
		require.NoError(t, rows.Err())
	})

	down := readKnowledgeFolderMigration(
		t,
		"sqlite",
		"000002_knowledge_folder_index_pending.down.sql",
	)
	_, err = db.Exec(down)
	require.NoError(t, err)

	t.Run("M35-08_down_removes_table_and_named_index", func(t *testing.T) {
		var objectCount int
		err := db.QueryRow(`
			SELECT COUNT(*)
			FROM sqlite_master
			WHERE name IN (?, ?)
		`, knowledgeFolderIndexPendingTable, knowledgeFolderIndexPendingScopeUpdate).
			Scan(&objectCount)
		require.NoError(t, err)
		assert.Zero(t, objectCount)
	})
}

func TestKnowledgeFolderIndexPendingMigrationInventoryContract(t *testing.T) {
	t.Run("M35-09_sqlite_requires_and_packages_version_two", func(t *testing.T) {
		assert.Equal(t, uint(2), sqlitemigrations.RequiredVersion)

		directory := filepath.Join(
			knowledgeFolderMigrationRoot(t),
			"migrations",
			"sqlite",
		)
		inventory, err := sqlitemigrations.ValidateDirectory(
			directory,
			sqlitemigrations.RequiredVersion,
		)
		require.NoError(t, err)
		require.GreaterOrEqual(t, len(inventory.Files), 2)
		assert.Equal(t, []string{
			"000002_knowledge_folder_index_pending.up.sql",
			"000002_knowledge_folder_index_pending.down.sql",
		}, inventory.Files[len(inventory.Files)-2:])
	})

	t.Run("M35-09_postgres_packages_complete_version_seventy_two", func(t *testing.T) {
		directory := filepath.Join(
			knowledgeFolderMigrationRoot(t),
			"migrations",
			"versioned",
		)
		inventory, err := validatePostgresMigrationsDirectory(directory)
		require.NoError(t, err)
		require.NotEmpty(t, inventory.versions)
		assert.Equal(t, uint(72), inventory.versions[len(inventory.versions)-1])
		assert.Contains(t, inventory.files, "000072_knowledge_folder_index_pending.up.sql")
		assert.Contains(t, inventory.files, "000072_knowledge_folder_index_pending.down.sql")
	})
}

func TestKnowledgeFolderIndexPendingMigrationKeepsPriorFolderMigrationsImmutable(
	t *testing.T,
) {
	root := knowledgeFolderMigrationRoot(t)
	expected := map[string]string{
		"migrations/versioned/000071_knowledge_folders.up.sql":   "5b6390889dc3588f73fc20206a9a0f26eb4f560c1d3d39c2eb824b1f95eb2e2f",
		"migrations/versioned/000071_knowledge_folders.down.sql": "6ca0a9e20fc7a285808b8560500705fdf3357db2bcf450c1efc579cb768e77ec",
		"migrations/sqlite/000001_knowledge_folders.up.sql":      "e920d4e9c320ec14f0649d2475c8ce58dda4dab932e22e151d1035c1adb5ee20",
		"migrations/sqlite/000001_knowledge_folders.down.sql":    "4d134bd754298344217c9923e4b0d983cce5b65803553a101f136dc538cca928",
	}

	for relativePath, expectedHash := range expected {
		content, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(relativePath)))
		require.NoError(t, err)
		actualHash := fmt.Sprintf("%x", sha256.Sum256(content))
		assert.Equal(t, expectedHash, actualHash, relativePath)
	}
}
