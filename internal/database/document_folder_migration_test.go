package database_test

import (
	"database/sql"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/Tencent/WeKnora/internal/database"
	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/require"
)

// releasedSQLiteV0Schema is the portion of the released SQLite v0 schema
// touched by migrations 000001 and 000002. Keeping this fixture independent
// from the mutable init migration makes the upgrade scenarios reproducible.
const releasedSQLiteV0Schema = `
CREATE TABLE knowledges (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id INTEGER NOT NULL,
    knowledge_base_id VARCHAR(36) NOT NULL,
    type VARCHAR(50) NOT NULL,
    title VARCHAR(255) NOT NULL,
    source VARCHAR(2048) NOT NULL
);

CREATE TABLE wiki_pages (
    id VARCHAR(36) PRIMARY KEY,
    page_type VARCHAR(32) NOT NULL DEFAULT 'summary'
);

CREATE TABLE wiki_log_entries (
    id INTEGER PRIMARY KEY AUTOINCREMENT
);

INSERT INTO knowledges (
    id, tenant_id, knowledge_base_id, type, title, source
) VALUES (
    'legacy-document', 1, 'kb-1', 'file', 'Legacy document', 'file:///legacy.txt'
);

INSERT INTO wiki_pages (id, page_type) VALUES
    ('summary-page', 'summary'),
    ('log-page', 'log');

INSERT INTO wiki_log_entries DEFAULT VALUES;
`

const releasedSQLiteRetrieverSchema = `
CREATE TABLE lite_embeddings (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    created_at DATETIME,
    updated_at DATETIME,
    source_id TEXT NOT NULL,
    source_type INTEGER NOT NULL,
    chunk_id TEXT,
    knowledge_id TEXT,
    knowledge_base_id TEXT,
    tag_id TEXT,
    content TEXT NOT NULL,
    dimension INTEGER NOT NULL,
    is_enabled NUMERIC DEFAULT 1
);

INSERT INTO lite_embeddings (
    source_id, source_type, chunk_id, knowledge_id,
    knowledge_base_id, tag_id, content, dimension, is_enabled
) VALUES (
    'legacy-source', 1, 'legacy-chunk', 'legacy-document',
    'kb-1', '', 'legacy content', 3, 1
);
`

func TestDocumentFolderMigrationUpgradesReleasedSQLiteVersions(t *testing.T) {
	t.Chdir(repositoryRoot(t))

	tests := []struct {
		name                   string
		releasedVersion        uint
		applyWikiRemoval       bool
		includeSQLiteRetriever bool
	}{
		{
			name:            "v0 applies wiki v1 before folder v2",
			releasedVersion: 0,
		},
		{
			name:                   "wiki v1 upgrades existing sqlite retriever",
			releasedVersion:        1,
			applyWikiRemoval:       true,
			includeSQLiteRetriever: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dbPath := filepath.Join(t.TempDir(), "released.db")
			db := openSQLite(t, dbPath)
			require.NoError(t, execSQLiteScript(db, releasedSQLiteV0Schema))
			if tt.includeSQLiteRetriever {
				require.NoError(t, execSQLiteScript(db, releasedSQLiteRetrieverSchema))
			}
			if tt.applyWikiRemoval {
				require.NoError(t, execSQLiteMigration(db, "000001_remove_wiki_log.up.sql"))
			}
			setMigrationVersion(t, db, tt.releasedVersion)
			require.NoError(t, db.Close())

			require.NoError(t, database.RunMigrationsWithOptions(
				"sqlite3://"+dbPath,
				database.MigrationOptions{SQLiteDBPath: dbPath},
			))

			db = openSQLite(t, dbPath)
			t.Cleanup(func() { require.NoError(t, db.Close()) })
			assertMigrationVersion(t, db, 2)
			assertDocumentFolderSchema(t, db, tt.includeSQLiteRetriever)
			assertWikiV1State(t, db)
		})
	}
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	require.True(t, ok)
	return filepath.Clean(filepath.Join(filepath.Dir(filename), "..", ".."))
}

func openSQLite(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", path)
	require.NoError(t, err)
	require.NoError(t, db.Ping())
	return db
}

func execSQLiteMigration(db *sql.DB, name string) error {
	migrationSQL, err := os.ReadFile(filepath.Join("migrations", "sqlite", name))
	if err != nil {
		return err
	}
	return execSQLiteScript(db, string(migrationSQL))
}

func execSQLiteScript(db *sql.DB, script string) error {
	_, err := db.Exec(script)
	return err
}

func setMigrationVersion(t *testing.T, db *sql.DB, version uint) {
	t.Helper()
	_, err := db.Exec(`
CREATE TABLE schema_migrations (version uint64, dirty bool);
CREATE UNIQUE INDEX version_unique ON schema_migrations (version);
INSERT INTO schema_migrations (version, dirty) VALUES (?, 0);
`, version)
	require.NoError(t, err)
}

func assertMigrationVersion(t *testing.T, db *sql.DB, want uint) {
	t.Helper()
	var (
		version uint
		dirty   bool
	)
	require.NoError(t, db.QueryRow(
		`SELECT version, dirty FROM schema_migrations`,
	).Scan(&version, &dirty))
	require.Equal(t, want, version)
	require.False(t, dirty)
}

func assertDocumentFolderSchema(t *testing.T, db *sql.DB, expectLegacyEmbedding bool) {
	t.Helper()
	var objectCount int
	require.NoError(t, db.QueryRow(`
SELECT COUNT(*) FROM sqlite_master
WHERE (type = 'table' AND name = 'document_folders')
   OR (type = 'index' AND name IN (
	       'idx_doc_folders_parent_name',
	       'idx_doc_folders_scope_parent',
	       'idx_doc_folders_deleted_at',
	       'idx_knowledges_folder',
	       'idx_lite_embeddings_folder_id'
	   ))
	`).Scan(&objectCount))
	require.Equal(t, 6, objectCount, "folder table and relation/retriever indexes must exist")

	var columnCount int
	require.NoError(t, db.QueryRow(`
SELECT COUNT(*) FROM pragma_table_info('knowledges')
WHERE name = 'folder_id'
  AND type = 'VARCHAR(36)'
  AND "notnull" = 1
  AND dflt_value = "''"
`).Scan(&columnCount))
	require.Equal(t, 1, columnCount, "knowledges.folder_id must have the released root default")

	var folderID string
	require.NoError(t, db.QueryRow(
		`SELECT folder_id FROM knowledges WHERE id = 'legacy-document'`,
	).Scan(&folderID))
	require.Empty(t, folderID, "existing documents must remain at the virtual root")

	require.NoError(t, db.QueryRow(`
	SELECT COUNT(*) FROM pragma_table_info('lite_embeddings')
	WHERE name = 'folder_id'
	  AND type = 'TEXT'
	  AND "notnull" = 1
	  AND dflt_value = "''"
	`).Scan(&columnCount))
	require.Equal(t, 1, columnCount, "lite_embeddings.folder_id must have the released root default")

	if expectLegacyEmbedding {
		require.NoError(t, db.QueryRow(
			`SELECT folder_id FROM lite_embeddings WHERE source_id = 'legacy-source'`,
		).Scan(&folderID))
		require.Empty(t, folderID, "existing SQLite retriever rows must remain at the virtual root")
	}
}

func assertWikiV1State(t *testing.T, db *sql.DB) {
	t.Helper()
	var count int
	require.NoError(t, db.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'wiki_log_entries'`,
	).Scan(&count))
	require.Zero(t, count, "SQLite v0 must not skip the released Wiki v1 migration")

	require.NoError(t, db.QueryRow(
		`SELECT COUNT(*) FROM wiki_pages WHERE page_type = 'log'`,
	).Scan(&count))
	require.Zero(t, count)

	require.NoError(t, db.QueryRow(
		`SELECT COUNT(*) FROM wiki_pages WHERE page_type = 'summary'`,
	).Scan(&count))
	require.Equal(t, 1, count)
}
