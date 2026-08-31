package database

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/infrastructure/chunker"
	"github.com/stretchr/testify/require"
)

// versionedSQLiteTables is the set of tables that SQLite migrations must
// create to stay in sync with the versioned (PostgreSQL) migrations:
// 000041 task queue, 000053 system settings, 000055 processing spans,
// 000063 knowledge multi-tags.
var versionedSQLiteTables = []string{
	"task_pending_ops",
	"task_dead_letters",
	"system_settings",
	"knowledge_processing_spans",
	"knowledge_tag_relations",
}

// versionedSQLiteColumns maps each existing table to the columns that the
// versioned migrations add and the SQLite baseline was missing.
var versionedSQLiteColumns = map[string][]string{
	"tenants":            {"api_principal_config"},           // 000064
	"users":              {"is_system_admin"},                // 000053
	"knowledges":         {"pending_subtasks_count"},         // 000056
	"messages":           {"attachments", "usage"},           // 000034, 000085
	"tenant_invitations": {"token", "accepted_count"},        // 000054
	"embed_channels":     {"allow_memory"},                   // 000060
	"mcp_oauth_tokens":   {"principal_type", "principal_id"}, // 000064
}

const expectedSQLiteMigrationVersion = 13

func TestSQLiteMigrationsCreateVersionedSchema(t *testing.T) {
	repoRoot := sqliteRepoRoot(t)
	chdirAndRestore(t, repoRoot)

	dbPath := filepath.Join(t.TempDir(), "fresh.db")
	require.NoError(t, RunMigrationsWithOptions("sqlite3://unused", MigrationOptions{SQLiteDBPath: dbPath}))

	db := openSQLiteDB(t, dbPath)
	version, dirty := sqliteMigrationState(t, db)
	require.Equal(t, expectedSQLiteMigrationVersion, version)
	require.False(t, dirty)

	for _, table := range versionedSQLiteTables {
		require.Truef(t, sqliteTableExists(t, db, table), "SQLite migrations must create table %s", table)
	}
	for table, columns := range versionedSQLiteColumns {
		for _, column := range columns {
			require.Truef(
				t,
				sqliteColumnExists(t, db, table, column),
				"SQLite migrations must add column %s.%s",
				table,
				column,
			)
		}
	}

	assertSQLiteShareLinkInvitationsWork(t, db)
	assertSQLiteMCPOAuthPrincipalUpsertWorks(t, db)
	require.False(t, sqliteColumnExists(t, db, "knowledges", "tag_id"),
		"SQLite migrations must drop legacy knowledges.tag_id after multi-tag migration")

	insertSQLiteKnowledgeBaseWithDefault(t, db, "fresh-kb-1", "fresh")
	assertSQLiteChunkingConfig(t, db, "fresh-kb-1", 512, 80, []string{"\n\n", "\n", "。"})
}

func TestSQLiteMigrationsUpgradeV12NormalizesChunkingConfig(t *testing.T) {
	repoRoot := sqliteRepoRoot(t)

	legacyRoot := copySQLiteMigrationsThrough(t, repoRoot, 12)
	chdirAndRestore(t, legacyRoot)

	dbPath := filepath.Join(t.TempDir(), "chunking-config-upgrade.db")
	require.NoError(t, RunMigrationsWithOptions("sqlite3://unused", MigrationOptions{SQLiteDBPath: dbPath}))

	db := openSQLiteDB(t, dbPath)
	versionBefore, dirtyBefore := sqliteMigrationState(t, db)
	require.Equal(t, 12, versionBefore)
	require.False(t, dirtyBefore)

	legacyChunkingConfig := `{"chunk_size": 700, "chunk_overlap": 50,
"split_markers": ["\n", "。"], "keep_separator": true}`
	insertSQLiteKnowledgeBase(t, db, "legacy-kb-1", "legacy-1", legacyChunkingConfig)

	mixedChunkingConfig := `{"chunk_size": 900, "chunk_overlap": 25,
"split_markers": ["old"], "separators": ["new"], "keep_separator": true}`
	insertSQLiteKnowledgeBase(t, db, "legacy-kb-2", "legacy-2", mixedChunkingConfig)

	chdirAndRestore(t, repoRoot)
	require.NoError(t, RunMigrationsWithOptions("sqlite3://unused", MigrationOptions{SQLiteDBPath: dbPath}))

	db = openSQLiteDB(t, dbPath)
	versionAfter, dirtyAfter := sqliteMigrationState(t, db)
	require.Equal(t, expectedSQLiteMigrationVersion, versionAfter)
	require.False(t, dirtyAfter)

	assertSQLiteChunkingConfig(t, db, "legacy-kb-1", 700, 50, []string{"\n", "。"})
	assertSQLiteChunkingConfig(t, db, "legacy-kb-2", 900, 25, []string{"new"})
}

func TestChunkingConfigDatabaseDefaultsMatchRuntime(t *testing.T) {
	repoRoot := sqliteRepoRoot(t)
	runtimeConfig := chunker.DefaultConfig()
	defaultPattern := regexp.MustCompile(
		`(?m)^\s*chunking_config\s+\w+\s+NOT NULL DEFAULT '([^']+)'`,
	)

	files := []string{
		"migrations/versioned/000000_init.up.sql",
		"migrations/paradedb/00-init-db.sql",
		"migrations/sqlite/000000_init.up.sql",
	}
	for _, file := range files {
		t.Run(file, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join(repoRoot, file))
			require.NoError(t, err)

			matches := defaultPattern.FindStringSubmatch(string(data))
			require.Len(t, matches, 2, "expected one chunking_config default in %s", file)

			var config struct {
				ChunkSize    int      `json:"chunk_size"`
				ChunkOverlap int      `json:"chunk_overlap"`
				Separators   []string `json:"separators"`
			}
			require.NoError(t, json.Unmarshal([]byte(matches[1]), &config))
			require.Equal(t, runtimeConfig.ChunkSize, config.ChunkSize)
			require.Equal(t, runtimeConfig.ChunkOverlap, config.ChunkOverlap)
			require.Equal(t, runtimeConfig.Separators, config.Separators)

			var keys map[string]json.RawMessage
			require.NoError(t, json.Unmarshal([]byte(matches[1]), &keys))
			require.NotContains(t, keys, "split_markers")
			require.NotContains(t, keys, "keep_separator")
		})
	}
}

func TestSQLiteMigrationsUpgradeV4PreservesData(t *testing.T) {
	repoRoot := sqliteRepoRoot(t)

	// Build a legacy v4 migration root (000000_init .. 000004_memory) so we
	// can prove the new migrations upgrade an existing Lite database without
	// replaying the baseline.
	legacyRoot := copySQLiteMigrationsV4(t, repoRoot)
	chdirAndRestore(t, legacyRoot)

	dbPath := filepath.Join(t.TempDir(), "upgrade.db")
	require.NoError(t, RunMigrationsWithOptions("sqlite3://unused", MigrationOptions{SQLiteDBPath: dbPath}))

	db := openSQLiteDB(t, dbPath)
	versionBefore, dirtyBefore := sqliteMigrationState(t, db)
	require.Equal(t, 4, versionBefore)
	require.False(t, dirtyBefore)
	_, err := db.Exec("INSERT INTO tenants (name, business) VALUES (?, ?)", "upgrade-sentinel", "migration-test")
	require.NoError(t, err)
	_, err = db.Exec(
		"INSERT INTO knowledges (id, tenant_id, knowledge_base_id, type, title, source, tag_id) "+
			"VALUES (?, 1, ?, 'document', 'tagged-doc', 'manual', ?)",
		"legacy-knowledge-1", "legacy-kb-1", "legacy-tag-1",
	)
	require.NoError(t, err)

	// Run the full migration set from the repo root.
	chdirAndRestore(t, repoRoot)
	require.NoError(t, RunMigrationsWithOptions("sqlite3://unused", MigrationOptions{SQLiteDBPath: dbPath}))

	db = openSQLiteDB(t, dbPath)
	versionAfter, dirtyAfter := sqliteMigrationState(t, db)
	require.Equal(t, expectedSQLiteMigrationVersion, versionAfter)
	require.False(t, dirtyAfter)

	for _, table := range versionedSQLiteTables {
		require.Truef(t, sqliteTableExists(t, db, table), "upgraded SQLite DB must have table %s", table)
	}
	for table, columns := range versionedSQLiteColumns {
		for _, column := range columns {
			require.Truef(
				t,
				sqliteColumnExists(t, db, table, column),
				"upgraded SQLite DB must have column %s.%s",
				table,
				column,
			)
		}
	}

	var sentinelName string
	require.NoError(t, db.QueryRow("SELECT name FROM tenants WHERE business = ?", "migration-test").Scan(&sentinelName))
	require.Equal(t, "upgrade-sentinel", sentinelName)

	var relationCount int
	require.NoError(t, db.QueryRow(
		"SELECT COUNT(*) FROM knowledge_tag_relations WHERE knowledge_id = ? AND tag_id = ?",
		"legacy-knowledge-1", "legacy-tag-1",
	).Scan(&relationCount))
	require.Equal(t, 1, relationCount)
	require.False(t, sqliteColumnExists(t, db, "knowledges", "tag_id"))
}

func sqliteRepoRoot(t *testing.T) string {
	t.Helper()
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	require.NoError(t, err)
	return repoRoot
}

func chdirAndRestore(t *testing.T, dir string) {
	t.Helper()
	previousDir, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(dir))
	t.Cleanup(func() { _ = os.Chdir(previousDir) })
}

func openSQLiteDB(t *testing.T, dbPath string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", dbPath)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func sqliteMigrationState(t *testing.T, db *sql.DB) (version int, dirty bool) {
	t.Helper()
	require.NoError(t, db.QueryRow("SELECT version, dirty FROM schema_migrations").Scan(&version, &dirty))
	return version, dirty
}

func sqliteTableExists(t *testing.T, db *sql.DB, table string) bool {
	t.Helper()
	var n int
	require.NoError(t, db.QueryRow(
		"SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?",
		table,
	).Scan(&n))
	return n == 1
}

func sqliteColumnExists(t *testing.T, db *sql.DB, table, column string) bool {
	t.Helper()
	var n int
	require.NoError(t, db.QueryRow(
		"SELECT COUNT(*) FROM pragma_table_info(?) WHERE name = ?",
		table,
		column,
	).Scan(&n))
	return n == 1
}

func insertSQLiteKnowledgeBase(t *testing.T, db *sql.DB, id, name, chunkingConfig string) {
	t.Helper()
	_, err := db.Exec(
		"INSERT INTO knowledge_bases (id, name, tenant_id, chunking_config, embedding_model_id, summary_model_id) "+
			"VALUES (?, ?, 1, ?, 'embedding-model', 'summary-model')",
		id, name, chunkingConfig,
	)
	require.NoError(t, err)
}

func insertSQLiteKnowledgeBaseWithDefault(t *testing.T, db *sql.DB, id, name string) {
	t.Helper()
	_, err := db.Exec(
		"INSERT INTO knowledge_bases (id, name, tenant_id, embedding_model_id, summary_model_id) "+
			"VALUES (?, ?, 1, 'embedding-model', 'summary-model')",
		id, name,
	)
	require.NoError(t, err)
}

func assertSQLiteChunkingConfig(
	t *testing.T,
	db *sql.DB,
	id string,
	expectedSize, expectedOverlap int,
	expectedSeparators []string,
) {
	t.Helper()
	var raw string
	require.NoError(t, db.QueryRow(
		"SELECT chunking_config FROM knowledge_bases WHERE id = ?", id,
	).Scan(&raw))

	var config struct {
		ChunkSize    int      `json:"chunk_size"`
		ChunkOverlap int      `json:"chunk_overlap"`
		Separators   []string `json:"separators"`
	}
	require.NoError(t, json.Unmarshal([]byte(raw), &config))
	require.Equal(t, expectedSize, config.ChunkSize)
	require.Equal(t, expectedOverlap, config.ChunkOverlap)
	require.Equal(t, expectedSeparators, config.Separators)

	var keys map[string]json.RawMessage
	require.NoError(t, json.Unmarshal([]byte(raw), &keys))
	require.NotContains(t, keys, "split_markers")
	require.NotContains(t, keys, "keep_separator")
}

func assertSQLiteShareLinkInvitationsWork(t *testing.T, db *sql.DB) {
	t.Helper()
	_, err := db.Exec("INSERT INTO tenants (name, business) VALUES (?, ?)", "share-link-tenant", "share-link-test")
	require.NoError(t, err)

	expiresAt := "2099-01-01 00:00:00"
	shareLinkInsert := "INSERT INTO tenant_invitations " +
		"(tenant_id, invitee_user_id, token, role, status, expires_at) " +
		"VALUES (1, '', ?, 'member', 'pending', ?)"
	_, err = db.Exec(shareLinkInsert, "token-a", expiresAt)
	require.NoError(t, err)
	_, err = db.Exec(shareLinkInsert, "token-b", expiresAt)
	require.NoError(t, err)

	var count int
	require.NoError(t, db.QueryRow(
		"SELECT COUNT(*) FROM tenant_invitations WHERE tenant_id = 1 AND invitee_user_id = '' AND status = 'pending'",
	).Scan(&count))
	require.Equal(t, 2, count)
}

func assertSQLiteMCPOAuthPrincipalUpsertWorks(t *testing.T, db *sql.DB) {
	t.Helper()
	_, err := db.Exec(
		"INSERT INTO mcp_services (id, tenant_id, name, transport_type) VALUES (?, 1, 'svc', 'http')",
		"svc-migration-1",
	)
	require.NoError(t, err)

	tokenInsertPrefix := "INSERT INTO mcp_oauth_tokens " +
		"(id, tenant_id, user_id, service_id, principal_type, principal_id, access_token) "
	_, err = db.Exec(
		tokenInsertPrefix +
			"VALUES ('tok-1', 1, 'u1', 'svc-migration-1', 'web_user', 'u1', 'token-1')",
	)
	require.NoError(t, err)

	_, err = db.Exec(
		tokenInsertPrefix +
			"VALUES ('tok-2', 1, 'u1', 'svc-migration-1', 'web_user', 'u1', 'token-2') " +
			"ON CONFLICT(tenant_id, principal_type, principal_id, service_id) " +
			"DO UPDATE SET access_token = excluded.access_token",
	)
	require.NoError(t, err)

	var accessToken string
	require.NoError(t, db.QueryRow(
		"SELECT access_token FROM mcp_oauth_tokens "+
			"WHERE tenant_id = 1 AND principal_type = 'web_user' "+
			"AND principal_id = 'u1' AND service_id = 'svc-migration-1'",
	).Scan(&accessToken))
	require.Equal(t, "token-2", accessToken)

	var rowCount int
	require.NoError(t, db.QueryRow(
		"SELECT COUNT(*) FROM mcp_oauth_tokens WHERE tenant_id = 1 AND service_id = 'svc-migration-1'",
	).Scan(&rowCount))
	require.Equal(t, 1, rowCount)
}

func copySQLiteMigrationsV4(t *testing.T, repoRoot string) string {
	return copySQLiteMigrationsThrough(t, repoRoot, 4)
}

func copySQLiteMigrationsThrough(t *testing.T, repoRoot string, maxVersion int) string {
	t.Helper()
	dest := t.TempDir()
	srcDir := filepath.Join(repoRoot, "migrations", "sqlite")
	destDir := filepath.Join(dest, "migrations", "sqlite")
	require.NoError(t, os.MkdirAll(destDir, 0o755))

	entries, err := os.ReadDir(srcDir)
	require.NoError(t, err)
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".up.sql") || len(name) < 6 {
			continue
		}
		version, err := strconv.Atoi(name[:6])
		require.NoError(t, err)
		if version > maxVersion {
			continue
		}
		data, err := os.ReadFile(filepath.Join(srcDir, name))
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filepath.Join(destDir, name), data, 0o600))
	}
	return dest
}
