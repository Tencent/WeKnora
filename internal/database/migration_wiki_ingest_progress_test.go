package database

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/golang-migrate/migrate/v4"
	sqlite3migrate "github.com/golang-migrate/migrate/v4/database/sqlite3"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

const (
	wikiProgressSQLiteBaseVersion = 53
	wikiProgressSQLiteVersion     = 54
	wikiProgressPostgresMigration = "000138_wiki_ingest_progress"
)

// TestSQLiteWikiIngestProgressMigrationRoundTrip upgrades a real main schema,
// checks the SQL constraints, and proves down/up keeps existing source data.
func TestSQLiteWikiIngestProgressMigrationRoundTrip(t *testing.T) {
	root := sqliteRepoRoot(t)
	chdirAndRestore(t, copySQLiteMigrationsThrough(t, root, wikiProgressSQLiteBaseVersion))
	dbPath := filepath.Join(t.TempDir(), "wiki-progress-upgrade.db")
	require.NoError(t, RunMigrationsWithOptions("sqlite3://unused", MigrationOptions{SQLiteDBPath: dbPath}))
	chdirAndRestore(t, root)
	db, err := sql.Open("sqlite3", dbPath+"?_foreign_keys=on&_busy_timeout=5000")
	require.NoError(t, err)
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	version, dirty := sqliteMigrationState(t, db)
	require.Equal(t, wikiProgressSQLiteBaseVersion, version)
	require.False(t, dirty)
	seedWikiProgressMigrationSentinels(t, db)
	_, err = db.Exec(`INSERT INTO credential_vault_items
		(id, tenant_id, user_id, title, site, username, secret_ciphertext, created_at, updated_at)
		VALUES ('wiki-progress-credential', 1, 'user', 'preserved', 'example.invalid',
		'user', 'fixture', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`)
	require.NoError(t, err)

	driver, err := sqlite3migrate.WithInstance(db, &sqlite3migrate.Config{})
	require.NoError(t, err)
	m, err := migrate.NewWithDatabaseInstance("file://migrations/sqlite", "sqlite3", driver)
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = m.Close() })
	require.NoError(t, m.Migrate(wikiProgressSQLiteVersion))
	version, dirty = sqliteMigrationState(t, db)
	require.Equal(t, wikiProgressSQLiteVersion, version)
	require.False(t, dirty)
	assertWikiProgressMigrationConstraints(t, db)

	require.NoError(t, m.Migrate(wikiProgressSQLiteBaseVersion))
	assertWikiProgressMigrationRemoved(t, db)
	assertWikiProgressMigrationSentinels(t, db)
	var title string
	require.NoError(t, db.QueryRow(
		"SELECT title FROM credential_vault_items WHERE id = 'wiki-progress-credential'",
	).Scan(&title))
	require.Equal(t, "preserved", title)

	require.NoError(t, m.Migrate(wikiProgressSQLiteVersion))
	assertWikiProgressMigrationReset(t, db)
	assertWikiProgressMigrationSentinels(t, db)
}

// TestPostgresWikiIngestProgressMigrationSQLRoundTrip uses only an isolated
// schema and the real queue/migration SQL. It does not claim full extension-chain coverage.
func TestPostgresWikiIngestProgressMigrationSQLRoundTrip(t *testing.T) {
	dsn := os.Getenv("WEKNORA_MIGRATION_TEST_POSTGRES_DSN")
	if dsn == "" {
		dsn = os.Getenv("WEKNORA_REPOSITORY_TEST_POSTGRES_DSN")
	}
	if dsn == "" {
		t.Skip("set an isolated WEKNORA_MIGRATION_TEST_POSTGRES_DSN or WEKNORA_REPOSITORY_TEST_POSTGRES_DSN")
	}
	root := sqliteRepoRoot(t)
	db, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	// SET search_path remains on this one connection; no application schema is touched.
	db.SetMaxOpenConns(1)
	schema := "wiki_progress_migration_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	_, err = db.Exec("CREATE SCHEMA " + schema)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, dropErr := db.Exec("DROP SCHEMA " + schema + " CASCADE")
		require.NoError(t, dropErr)
		require.NoError(t, db.Close())
	})
	_, err = db.Exec("SET search_path TO " + schema)
	require.NoError(t, err)
	queueSQL := readWikiProgressMigrationSQL(t, root, "versioned", "000041_task_queue_and_wiki_indexes.up.sql")
	const tableStart = "CREATE TABLE IF NOT EXISTS task_pending_ops ("
	_, queueTable, found := strings.Cut(queueSQL, tableStart)
	require.True(t, found)
	queueTable, _, found = strings.Cut(queueTable, "\n);")
	require.True(t, found)
	_, err = db.Exec(tableStart + queueTable + "\n);")
	require.NoError(t, err)
	_, err = db.Exec(`CREATE TABLE wiki_pages (
		id VARCHAR(36) PRIMARY KEY, tenant_id BIGINT, knowledge_base_id VARCHAR(36), slug VARCHAR(255), content TEXT
	)`)
	require.NoError(t, err)
	seedWikiProgressMigrationSentinels(t, db)

	up := readWikiProgressMigrationSQL(t, root, "versioned", wikiProgressPostgresMigration+".up.sql")
	down := readWikiProgressMigrationSQL(t, root, "versioned", wikiProgressPostgresMigration+".down.sql")
	_, err = db.Exec(up)
	require.NoError(t, err)
	assertWikiProgressMigrationConstraints(t, db)
	_, err = db.Exec(down)
	require.NoError(t, err)
	assertWikiProgressMigrationRemoved(t, db)
	assertWikiProgressMigrationSentinels(t, db)
	_, err = db.Exec(up)
	require.NoError(t, err)
	assertWikiProgressMigrationReset(t, db)
	assertWikiProgressMigrationSentinels(t, db)
}

func readWikiProgressMigrationSQL(t *testing.T, root, dialect, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, "migrations", dialect, name))
	require.NoError(t, err)
	return strings.ReplaceAll(string(data), "\r\n", "\n")
}

func seedWikiProgressMigrationSentinels(t *testing.T, db *sql.DB) {
	t.Helper()
	_, err := db.Exec(`INSERT INTO task_pending_ops
		(id, tenant_id, task_type, scope, scope_id, op, dedup_key)
		VALUES (1001, 1, 'wiki:ingest', 'knowledge_base', 'kb', 'ingest', 'doc'),
		       (1002, 1, 'wiki:ingest', 'knowledge_base', 'kb', 'ingest', 'cascade-doc')`)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO wiki_pages (id, tenant_id, knowledge_base_id, slug, content)
		VALUES ('wiki-progress-page', 1, 'kb', 'entity/sentinel', 'preserved source page')`)
	require.NoError(t, err)
}

// assertWikiProgressMigrationConstraints exercises the actual migration SQL,
// including failures that GORM AutoMigrate fixtures do not necessarily enforce.
func assertWikiProgressMigrationConstraints(t *testing.T, db *sql.DB) {
	t.Helper()
	assertWikiProgressMigrationReset(t, db)
	const plan = `INSERT INTO wiki_ingest_plans
		(pending_op_id, tenant_id, knowledge_base_id, knowledge_id, op, payload)
		VALUES (1001, 1, 'kb', 'doc', 'ingest', '{}')`
	_, err := db.Exec(plan)
	require.NoError(t, err)
	_, err = db.Exec(plan)
	require.Error(t, err, "one frozen plan per pending operation")
	var attempt, sourceAttempt int
	require.NoError(t, db.QueryRow(
		"SELECT attempt, source_attempt FROM wiki_ingest_plans WHERE pending_op_id = 1001",
	).Scan(&attempt, &sourceAttempt))
	require.Zero(t, attempt)
	require.Zero(t, sourceAttempt)
	for _, invalid := range []string{
		`INSERT INTO wiki_ingest_plans
			(pending_op_id, tenant_id, knowledge_base_id, knowledge_id, op, payload)
			VALUES (9999, 1, 'kb', 'missing', 'ingest', '{}')`,
		`INSERT INTO wiki_ingest_plans
			(pending_op_id, tenant_id, knowledge_base_id, knowledge_id, op, payload)
			VALUES (1002, 1, 'kb', 'doc', 'ingest', NULL)`,
		`UPDATE task_pending_ops SET claim_epoch = NULL WHERE id = 1001`,
	} {
		_, err = db.Exec(invalid)
		require.Error(t, err)
	}
	_, err = db.Exec(`INSERT INTO wiki_ingest_page_progress (pending_op_id, slug)
		VALUES (1001, 'entity/a'), (1001, 'entity/b')`)
	require.NoError(t, err, "distinct page slugs share a pending operation")
	var applied, effectsDone, changed bool
	var pageID, oldLinks, newLinks string
	var pageVersion int
	require.NoError(t, db.QueryRow(`SELECT applied, effects_done, changed,
		page_id, page_version, old_out_links, new_out_links
		FROM wiki_ingest_page_progress WHERE pending_op_id = 1001 AND slug = 'entity/a'`).Scan(
		&applied, &effectsDone, &changed, &pageID, &pageVersion, &oldLinks, &newLinks))
	require.False(t, applied)
	require.False(t, effectsDone)
	require.False(t, changed)
	require.Empty(t, pageID)
	require.Zero(t, pageVersion)
	require.JSONEq(t, "[]", oldLinks)
	require.JSONEq(t, "[]", newLinks)
	for _, invalid := range []string{
		`INSERT INTO wiki_ingest_page_progress (pending_op_id, slug) VALUES (1001, 'entity/a')`,
		`INSERT INTO wiki_ingest_page_progress (pending_op_id, slug) VALUES (9999, 'entity/missing')`,
		`UPDATE wiki_ingest_page_progress SET effects_done = TRUE WHERE pending_op_id = 1001 AND slug = 'entity/a'`,
		`UPDATE wiki_ingest_page_progress SET applied = NULL WHERE pending_op_id = 1001 AND slug = 'entity/a'`,
	} {
		_, err = db.Exec(invalid)
		require.Error(t, err)
	}
	_, err = db.Exec(`INSERT INTO wiki_ingest_plans
		(pending_op_id, tenant_id, knowledge_base_id, knowledge_id, op, payload)
		VALUES (1002, 1, 'kb', 'cascade-doc', 'ingest', '{}')`)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO wiki_ingest_page_progress (pending_op_id, slug) VALUES (1002, 'entity/cascade')`)
	require.NoError(t, err)
	_, err = db.Exec("DELETE FROM task_pending_ops WHERE id = 1002")
	require.NoError(t, err)
	for _, table := range []string{"wiki_ingest_plans", "wiki_ingest_page_progress"} {
		var count int
		require.NoError(t, db.QueryRow("SELECT COUNT(*) FROM "+table+" WHERE pending_op_id = 1002").Scan(&count))
		require.Zero(t, count, "queue deletion cascades into %s", table)
	}
}

func assertWikiProgressMigrationRemoved(t *testing.T, db *sql.DB) {
	t.Helper()
	for _, query := range []string{
		"SELECT claim_epoch FROM task_pending_ops",
		"SELECT * FROM wiki_ingest_plans",
		"SELECT * FROM wiki_ingest_page_progress",
	} {
		rows, err := db.Query(query)
		if rows != nil {
			_ = rows.Close()
		}
		require.Error(t, err, "down migration removes only progress state")
	}
}

func assertWikiProgressMigrationReset(t *testing.T, db *sql.DB) {
	t.Helper()
	var epoch int64
	require.NoError(t, db.QueryRow("SELECT claim_epoch FROM task_pending_ops WHERE id = 1001").Scan(&epoch))
	require.Zero(t, epoch)
	for _, table := range []string{"wiki_ingest_plans", "wiki_ingest_page_progress"} {
		var count int
		require.NoError(t, db.QueryRow("SELECT COUNT(*) FROM "+table).Scan(&count))
		require.Zero(t, count, "up creates empty %s", table)
	}
}

func assertWikiProgressMigrationSentinels(t *testing.T, db *sql.DB) {
	t.Helper()
	var count int
	require.NoError(t, db.QueryRow(
		"SELECT COUNT(*) FROM task_pending_ops WHERE id = 1001 AND dedup_key = 'doc'",
	).Scan(&count))
	require.Equal(t, 1, count)
	var content string
	require.NoError(t, db.QueryRow("SELECT content FROM wiki_pages WHERE id = 'wiki-progress-page'").Scan(&content))
	require.Equal(t, "preserved source page", content)
}
