package database

import (
	"database/sql"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/golang-migrate/migrate/v4"
	migratesqlite "github.com/golang-migrate/migrate/v4/database/sqlite3"
	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/require"
)

func newFolderMigrationTestDB(t *testing.T) (*sql.DB, *migrate.Migrate) {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	require.True(t, ok)
	db, err := sql.Open("sqlite3", filepath.Join(t.TempDir(), "folders.db")+"?_foreign_keys=on")
	require.NoError(t, err)
	driver, err := migratesqlite.WithInstance(db, &migratesqlite.Config{})
	require.NoError(t, err)
	m, err := migrate.NewWithDatabaseInstance(
		"file://"+filepath.ToSlash(filepath.Join(filepath.Dir(filename), "..", "..", "migrations", "sqlite")),
		"sqlite3",
		driver,
	)
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = m.Close() })
	return db, m
}

func TestSQLiteFolderMigrationUpDownAndScopedConstraints(t *testing.T) {
	db, m := newFolderMigrationTestDB(t)
	require.NoError(t, m.Migrate(0))
	_, err := db.Exec(`
		INSERT INTO tenants (id, name, business) VALUES (10001, 'tenant', 'test');
		INSERT INTO knowledge_bases (id, name, tenant_id, embedding_model_id, summary_model_id) VALUES
			('kb-1', 'KB 1', 10001, 'embedding', 'summary'),
			('kb-2', 'KB 2', 10001, 'embedding', 'summary');
		INSERT INTO knowledges (id, tenant_id, knowledge_base_id, type, title, source)
		VALUES ('doc-1', 10001, 'kb-1', 'file', 'Existing', 'doc.txt');
	`)
	require.NoError(t, err)
	require.NoError(t, m.Up())
	version, dirty, err := m.Version()
	require.NoError(t, err)
	require.Equal(t, uint(75), version)
	require.False(t, dirty)

	_, err = db.Exec(`
		INSERT INTO folders (id, tenant_id, knowledge_base_id, parent_id, name) VALUES
			('root', 10001, 'kb-1', NULL, 'Root'),
			('child', 10001, 'kb-1', 'root', 'Child'),
			('other-kb', 10001, 'kb-2', NULL, 'Other');
	`)
	require.NoError(t, err)
	_, err = db.Exec("INSERT INTO folders (id, tenant_id, knowledge_base_id, parent_id, name) VALUES ('duplicate', 10001, 'kb-1', 'root', 'Child')")
	require.Error(t, err)
	_, err = db.Exec("INSERT INTO folders (id, tenant_id, knowledge_base_id, parent_id, name) VALUES ('cross-kb', 10001, 'kb-2', 'root', 'Invalid')")
	require.Error(t, err)
	_, err = db.Exec("UPDATE knowledges SET folder_id = 'child' WHERE id = 'doc-1'")
	require.NoError(t, err)
	_, err = db.Exec("UPDATE knowledges SET folder_id = 'other-kb' WHERE id = 'doc-1'")
	require.Error(t, err)

	require.NoError(t, m.Steps(-1))
	var folders int
	require.NoError(t, db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='folders'").Scan(&folders))
	require.Zero(t, folders)
	rows, err := db.Query("PRAGMA table_info(knowledges)")
	require.NoError(t, err)
	hasFolderID := false
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, columnType string
		var defaultValue any
		require.NoError(t, rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey))
		hasFolderID = hasFolderID || name == "folder_id"
	}
	require.NoError(t, rows.Close())
	require.False(t, hasFolderID)
}
