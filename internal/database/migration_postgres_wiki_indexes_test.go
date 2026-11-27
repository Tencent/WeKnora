package database

import (
	"database/sql"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/golang-migrate/migrate/v4"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestPostgresMigrationDropsUnusedWikiIndexes(t *testing.T) {
	dsn := os.Getenv("WEKNORA_MIGRATION_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set WEKNORA_MIGRATION_TEST_POSTGRES_DSN to run PostgreSQL migration tests")
	}
	admin, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, admin.Close()) })
	schema := "wiki_indexes_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	_, err = admin.Exec("CREATE SCHEMA " + schema)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, err := admin.Exec("DROP SCHEMA " + schema + " CASCADE")
		require.NoError(t, err)
	})

	scopedDSN, err := url.Parse(dsn)
	require.NoError(t, err)
	params := scopedDSN.Query()
	params.Set("search_path", schema+",public")
	scopedDSN.RawQuery = params.Encode()
	db, err := sql.Open("postgres", scopedDSN.String())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })

	// Apply the historical migrations that create the wiki table and indexes.
	// The isolated fixture only needs a stub knowledge_bases table.
	_, err = db.Exec("CREATE TABLE knowledge_bases (id TEXT PRIMARY KEY)")
	require.NoError(t, err)
	dir := filepath.Join(sqliteRepoRoot(t), "migrations", "versioned")
	for _, name := range []string{"000037_wiki_and_indexing.up.sql", "000041_task_queue_and_wiki_indexes.up.sql"} {
		script, err := os.ReadFile(filepath.Join(dir, name))
		require.NoError(t, err)
		_, err = db.Exec(string(script))
		require.NoError(t, err)
	}
	_, err = db.Exec(`INSERT INTO wiki_pages (id, tenant_id, knowledge_base_id, slug, content, source_refs)
		VALUES ('page', 1, 'kb', 'page', 'page content', '["bare", "legacy|Title"]')`)
	require.NoError(t, err)

	m, err := migrate.New("file://"+dir, scopedDSN.String())
	require.NoError(t, err)
	t.Cleanup(func() {
		sourceErr, databaseErr := m.Close()
		require.NoError(t, sourceErr)
		require.NoError(t, databaseErr)
	})
	const previousVersion = 137
	require.NoError(t, m.Force(previousVersion))

	checkIndexes := func(unusedPresent bool) {
		t.Helper()
		for _, index := range []string{
			"idx_wiki_pages_fulltext", "idx_wiki_pages_source_refs", "idx_wiki_pages_source_refs_text",
			"idx_wiki_pages_title_trgm", "idx_wiki_pages_kb_slug",
		} {
			var present bool
			require.NoError(t, db.QueryRow(`SELECT EXISTS (
				SELECT 1 FROM pg_indexes WHERE schemaname = $1 AND tablename = 'wiki_pages' AND indexname = $2
			)`, schema, index).Scan(&present))
			want := unusedPresent || index == "idx_wiki_pages_title_trgm" || index == "idx_wiki_pages_kb_slug"
			require.Equal(t, want, present, "presence of %s", index)
		}
	}
	checkIndexes(true)
	// A checkout before the fix has no next migration. Check the actual
	// indexes in that case too, so the regression fails on schema state.
	if err := m.Steps(1); !os.IsNotExist(err) {
		require.NoError(t, err)
	}
	checkIndexes(false)
	require.NoError(t, m.Migrate(previousVersion))
	checkIndexes(true)

	// An operator may already have removed an index on a bloated installation.
	_, err = db.Exec("DROP INDEX idx_wiki_pages_fulltext")
	require.NoError(t, err)
	require.NoError(t, m.Steps(1))
	checkIndexes(false)
	var content, refs string
	row := db.QueryRow("SELECT content, source_refs::text FROM wiki_pages WHERE id = 'page'")
	require.NoError(t, row.Scan(&content, &refs))
	require.Equal(t, "page content", content)
	require.JSONEq(t, `["bare", "legacy|Title"]`, refs)
}
