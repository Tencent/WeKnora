package database

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/golang-migrate/migrate/v4"
	sqlite3migrate "github.com/golang-migrate/migrate/v4/database/sqlite3"
	"github.com/stretchr/testify/require"
)

// versionedSQLiteTables is the set of tables that SQLite migrations must
// create to stay in sync with the versioned (PostgreSQL) migrations:
// 000041 task queue, 000053 system settings, 000055 processing spans,
// 000063 knowledge multi-tags, 000093 browser authorization, 000103 message
// artifacts.
var versionedSQLiteTables = []string{
	"memory_extraction_sessions",
	"task_pending_ops",
	"task_dead_letters",
	"system_settings",
	"model_catalog_configs",
	"chunk_images",
	"knowledge_processing_spans",
	"knowledge_tag_relations",
	"browser_devices",
	"browser_pairings",
	"browser_task_interruptions",
	"fork_snapshot_leases",
	"mcp_endpoints",
	"mcp_metadata",
	"message_artifacts",
	"tenant_skills",
	"tenant_skill_snapshots",
	"tenant_skill_catalog",
	"tenant_user_env_vars",
	"agent_prompts",
	"agent_run_suspensions",
	"agent_collaborations",
	"agent_grants",
	"automations",
	"automation_runs",
	"notifications",
	"session_sandbox_assignments",
	"sandbox_user_machines",
}

// versionedSQLiteColumns maps each existing table to the columns that the
// versioned migrations add and the SQLite baseline was missing.
var versionedSQLiteColumns = map[string][]string{
	"automations":           {"model_id"},                                                  // 000049
	"automation_runs":       {"execution_duration_ms"},                                     // 000048
	"model_catalog_configs": {"version", "overlay", "history", "updated_by", "updated_at"}, // 000031
	"memory_subjects":       {"extraction_state"},                                          // 000094
	"memory_items":          {"replaces_id"},                                               // 000094
	"tenants":               {"api_principal_config"},                                      // 000064
	"users":                 {"is_system_admin"},                                           // 000053
	"knowledges":            {"pending_subtasks_count", "profile"},                         // 000056, 000101
	"knowledge_bases":       {"profile_config", "generated_profile"},                       // 000101
	// 000034/085/097/105/119
	"messages": {"attachments", "usage", "sandbox_checkpoint", "context_checkpoint", "run_state"},
	"sessions": {
		"kind",
		"parent_session_id", "forked_from_message_id", "fork_bootstrap", // 000097
		"sandbox_config_tenant_id", // 000027
		"host_workspace_dir",       // 000029
		"owner_auth_source",        // 000046
	},
	"session_sandbox_assignments": {"released_at", "cleanup_due_at"},    // 000047
	"tenant_invitations":          {"token", "accepted_count"},          // 000054
	"embed_channels":              {"allow_memory"},                     // 000060
	"im_channels":                 {"locale", "language_mode"},          // 000030/051
	"im_channel_sessions":         {"last_detected_language"},           // 000051
	"chunks":                      {"source_locators"},                  // 000033
	"mcp_oauth_tokens":            {"principal_type", "principal_id"},   // 000064
	"mcp_tool_approvals":          {"enabled", "risk", "observed_risk"}, // 000091, 000126
	"mcp_services":                {"usage_instructions"},               // 000092
	"message_artifacts":           {"deleted_at"},                       // 000107
	"tenant_skills": {
		"envs", "served", "catalog_id", "install_session_id", "install_message_id",
	}, // 000028
	"tenant_skill_snapshots": {"planned_name"}, // 000028
	"tenant_user_env_vars": {
		"principal_type", "principal_id", "sandbox_config_id", "skill_id", "name", "value",
	}, // 000028
}

const expectedSQLiteMigrationVersion = 51

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

	require.True(t, sqliteIndexExists(t, db, "idx_messages_session_created_id"),
		"SQLite migrations must add the session/created_at index") // 000106
	require.True(t, sqliteIndexExists(t, db, "idx_messages_live_attachments"),
		"SQLite migrations must index live user message references")
	assertSQLiteAgentHistoryQueriesUseTheIndex(t, db)

	var catalogVersion int
	var catalogOverlay string
	catalogRow := db.QueryRow("SELECT version, overlay FROM model_catalog_configs WHERE id = 1")
	require.NoError(t, catalogRow.Scan(&catalogVersion, &catalogOverlay))
	require.Zero(t, catalogVersion)
	require.JSONEq(t, `{"providers":{}}`, catalogOverlay)

	assertSQLiteShareLinkInvitationsWork(t, db)
	assertSQLiteMCPOAuthPrincipalUpsertWorks(t, db)
	require.False(t, sqliteColumnExists(t, db, "knowledges", "tag_id"),
		"SQLite migrations must drop legacy knowledges.tag_id after multi-tag migration")
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

func TestSQLiteMigrationsUpgradeV16AddsSessionForkColumns(t *testing.T) {
	repoRoot := sqliteRepoRoot(t)

	legacyRoot := copySQLiteMigrationsThrough(t, repoRoot, 16)
	chdirAndRestore(t, legacyRoot)

	dbPath := filepath.Join(t.TempDir(), "upgrade-v16.db")
	require.NoError(t, RunMigrationsWithOptions("sqlite3://unused", MigrationOptions{SQLiteDBPath: dbPath}))

	db := openSQLiteDB(t, dbPath)
	versionBefore, dirtyBefore := sqliteMigrationState(t, db)
	require.Equal(t, 16, versionBefore)
	require.False(t, dirtyBefore)
	require.False(t, sqliteColumnExists(t, db, "sessions", "parent_session_id"))
	require.False(t, sqliteColumnExists(t, db, "sessions", "forked_from_message_id"))
	require.False(t, sqliteColumnExists(t, db, "sessions", "fork_bootstrap"))
	require.False(t, sqliteColumnExists(t, db, "messages", "sandbox_checkpoint"))

	chdirAndRestore(t, repoRoot)
	require.NoError(t, RunMigrationsWithOptions("sqlite3://unused", MigrationOptions{SQLiteDBPath: dbPath}))

	db = openSQLiteDB(t, dbPath)
	versionAfter, dirtyAfter := sqliteMigrationState(t, db)
	require.Equal(t, expectedSQLiteMigrationVersion, versionAfter)
	require.False(t, dirtyAfter)
	for _, column := range versionedSQLiteColumns["sessions"] {
		require.Truef(
			t,
			sqliteColumnExists(t, db, "sessions", column),
			"upgraded SQLite DB must have column sessions.%s",
			column,
		)
	}
	require.True(t, sqliteColumnExists(t, db, "messages", "sandbox_checkpoint"))
}

// The allocation migration must come back off cleanly: both tables disappear,
// and applying it again restores them including the partial indexes.
func TestSQLiteSandboxAllocationMigrationRollsBack(t *testing.T) {
	repoRoot := sqliteRepoRoot(t)
	chdirAndRestore(t, repoRoot)

	dbPath := filepath.Join(t.TempDir(), "down.db")
	require.NoError(t, RunMigrationsWithOptions("sqlite3://unused", MigrationOptions{SQLiteDBPath: dbPath}))

	db := openSQLiteDB(t, dbPath)
	version, dirty := sqliteMigrationState(t, db)
	require.Equal(t, expectedSQLiteMigrationVersion, version)
	require.False(t, dirty)
	for _, table := range []string{"session_sandbox_assignments", "sandbox_user_machines"} {
		require.Truef(t, sqliteTableExists(t, db, table), "up must create %s", table)
	}
	require.True(t, sqliteIndexExists(t, db, "idx_session_sandbox_assignments_live_machine"))
	require.True(t, sqliteIndexExists(t, db, "idx_session_sandbox_assignments_cleanup_due"))
	require.True(t, sqliteIndexExists(t, db, "idx_sandbox_user_machines_empty"))
	require.NoError(t, db.Close())

	sqlDB, err := sql.Open("sqlite3", dbPath)
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	driver, err := sqlite3migrate.WithInstance(sqlDB, &sqlite3migrate.Config{})
	require.NoError(t, err)
	m, err := migrate.NewWithDatabaseInstance("file://migrations/sqlite", "sqlite3", driver)
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = m.Close() })

	// Target the allocation migration even when newer migrations exist.
	require.NoError(t, m.Migrate(46))
	version, dirty = sqliteMigrationState(t, sqlDB)
	require.Equal(t, 46, version)
	require.False(t, dirty)
	for _, table := range []string{"session_sandbox_assignments", "sandbox_user_machines"} {
		require.Falsef(t, sqliteTableExists(t, sqlDB, table), "down must drop %s", table)
	}
	require.True(t, sqliteColumnExists(t, sqlDB, "sessions", "owner_auth_source"),
		"rolling back the allocation tables keeps the owner source of 000046")

	require.NoError(t, m.Migrate(47))
	version, dirty = sqliteMigrationState(t, sqlDB)
	require.Equal(t, 47, version)
	require.False(t, dirty)
	require.True(t, sqliteTableExists(t, sqlDB, "session_sandbox_assignments"))
	require.True(t, sqliteTableExists(t, sqlDB, "sandbox_user_machines"))
	require.True(t, sqliteIndexExists(t, sqlDB, "idx_session_sandbox_assignments_live_machine"))
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

// assertSQLiteAgentHistoryQueriesUseTheIndex checks the two per-turn agent
// history queries walk idx_messages_session_created_id in order instead of
// sorting every message of the session.
func assertSQLiteAgentHistoryQueriesUseTheIndex(t *testing.T, db *sql.DB) {
	t.Helper()
	for name, query := range map[string]string{
		"backwards page": `SELECT * FROM messages WHERE session_id = 's'
			AND (created_at < '2026-01-01' OR (created_at = '2026-01-01' AND id < 'x'))
			AND deleted_at IS NULL ORDER BY created_at DESC, id DESC LIMIT 200`,
		"newest checkpoint": `SELECT id FROM messages WHERE session_id = 's' AND role = 'assistant'
			AND context_checkpoint IS NOT NULL AND deleted_at IS NULL
			ORDER BY created_at DESC, id DESC LIMIT 1`,
	} {
		rows, err := db.Query("EXPLAIN QUERY PLAN " + query)
		require.NoError(t, err, name)
		var plan strings.Builder
		for rows.Next() {
			var id, parent, unused int
			var detail string
			require.NoError(t, rows.Scan(&id, &parent, &unused, &detail), name)
			plan.WriteString(detail + "\n")
		}
		require.NoError(t, rows.Err(), name)
		require.NoError(t, rows.Close(), name)
		require.Contains(t, plan.String(), "idx_messages_session_created_id", "%s plan:\n%s", name, plan.String())
		require.NotContains(t, plan.String(), "TEMP B-TREE", "%s must not sort:\n%s", name, plan.String())
	}
}

func sqliteIndexExists(t *testing.T, db *sql.DB, index string) bool {
	t.Helper()
	var n int
	require.NoError(t, db.QueryRow(
		"SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND name = ?",
		index,
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
	t.Helper()
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
	copied := 0
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".up.sql") {
			continue
		}
		var version int
		_, scanErr := fmt.Sscanf(name, "%d_", &version)
		require.NoError(t, scanErr, "sqlite migration filename %s", name)
		if version > maxVersion {
			continue
		}
		data, readErr := os.ReadFile(filepath.Join(srcDir, name))
		require.NoError(t, readErr)
		require.NoError(t, os.WriteFile(filepath.Join(destDir, name), data, 0o600))
		copied++
	}
	require.Greater(t, copied, 0)
	return dest
}

func TestSQLiteSessionKindBackfillAndRollback(t *testing.T) {
	repoRoot := sqliteRepoRoot(t)
	chdirAndRestore(t, copySQLiteMigrationsThrough(t, repoRoot, 38))
	dbPath := filepath.Join(t.TempDir(), "session-kind.db")
	require.NoError(t, RunMigrationsWithOptions("sqlite3://unused", MigrationOptions{SQLiteDBPath: dbPath}))
	db := openSQLiteDB(t, dbPath)
	configs := []any{
		nil, "invalid JSON", `{"agent_id":"builtin-weknora-assistant"}`, `{}`, `null`, `[]`,
		`{"agent_id":"another-agent"}`, `{"agent_id":`,
	}
	for i, config := range configs {
		_, err := db.Exec("INSERT INTO sessions (id, tenant_id, agent_config) VALUES (?, 1, ?)", fmt.Sprint(i), config)
		require.NoError(t, err)
	}
	chdirAndRestore(t, repoRoot)
	require.NoError(t, RunMigrationsWithOptions("sqlite3://unused", MigrationOptions{SQLiteDBPath: dbPath}))
	for i := range configs {
		var kind string
		require.NoError(t, db.QueryRow("SELECT kind FROM sessions WHERE id = ?", fmt.Sprint(i)).Scan(&kind))
		if i == 2 {
			require.Equal(t, "assistant", kind)
		} else {
			require.Empty(t, kind)
		}
	}
	require.True(t, sqliteIndexExists(t, db, "idx_sessions_tenant_kind_user_updated"))
	down, err := os.ReadFile(filepath.Join(repoRoot, "migrations/sqlite/000039_session_kind.down.sql"))
	require.NoError(t, err)
	_, err = db.Exec(string(down))
	require.NoError(t, err)
	require.False(t, sqliteColumnExists(t, db, "sessions", "kind"))
	require.False(t, sqliteIndexExists(t, db, "idx_sessions_tenant_kind_user_updated"))
	var count int
	require.NoError(t, db.QueryRow("SELECT COUNT(*) FROM sessions").Scan(&count))
	require.Equal(t, len(configs), count)
}
