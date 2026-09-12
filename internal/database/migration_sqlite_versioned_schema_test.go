package database

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// versionedSQLiteTables is the set of tables that SQLite migrations must
// create to stay in sync with the versioned (PostgreSQL) migrations:
// 000041 task queue, 000053 system settings, 000055 processing spans,
// 000063 knowledge multi-tags, 000093 browser authorization.
// Evaluation and observability tables are supplied by the independent topic3 chain.
var versionedSQLiteTables = []string{
	"task_pending_ops",
	"task_dead_letters",
	"system_settings",
	"knowledge_processing_spans",
	"knowledge_tag_relations",
	"browser_devices",
	"browser_pairings",
	"browser_task_interruptions",
	"evaluation_tasks",
	"evaluation_datasets",
	"evaluation_dataset_versions",
	"evaluation_dataset_passages",
	"evaluation_dataset_questions",
	"evaluation_dataset_relevance",
	"evaluation_question_results",
	"evaluation_task_labels",
	"model_price_versions",
	"model_call_records",
	"embedding_cache_entries",
	"embedding_cache_lookup_records",
	"evaluation_human_ratings",
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
	"evaluation_tasks": { // 000090-000095 (SQLite 000013-000018)
		"tenant_id", "dataset_id", "status", "start_time", "end_time", "total", "finished", "err_msg",
		"cleanup_errors", "params", "metric", "temporary_kb_id", "temporary_knowledge_id", "owner_id",
		"lease_expires_at", "heartbeat_at", "version", "created_at", "updated_at", "deleted_at",
		"cancel_requested_at", "dataset_version_id", "dataset_content_sha256", "experiment_snapshot",
		"experiment_sha256", "runtime_metrics",
	},
	"evaluation_question_results": {"usage_reported"},
	"mcp_tool_approvals":          {"enabled"},
}

const expectedSQLiteMigrationVersion = 14

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
	assertSQLiteEvaluationTaskSchema(t, db)
	assertSQLiteEvaluationQuestionResultsSchema(t, db)
	assertSQLiteEvaluationTaskLabelsSchema(t, db)
	assertSQLiteModelObservabilitySchema(t, db)
	assertSQLiteEmbeddingCacheSchema(t, db, false)
	assertSQLiteModelStatisticsSchema(t, db)
	assertSQLiteHumanRatingsSchema(t, db)
	require.False(t, sqliteColumnExists(t, db, "knowledges", "tag_id"),
		"SQLite migrations must drop legacy knowledges.tag_id after multi-tag "+"migration")
}

func assertSQLiteModelObservabilitySchema(t *testing.T, db *sql.DB) {
	t.Helper()
	for _, table := range []string{"model_price_versions", "model_call_records"} {
		require.Truef(t, sqliteTableExists(t, db, table), "SQLite must contain table %s", table)
	}
	for _, column := range []string{
		"model_snapshot", "purpose", "operation", "duration_ms", "provider_cache_status",
		"application_cache_status", "cost_microunits", "accounting_complete",
	} {
		require.Truef(t, sqliteColumnExists(t, db, "model_call_records", column),
			"SQLite model_call_records must contain column %s", column)
	}
}

func assertSQLiteEmbeddingCacheSchema(t *testing.T, db *sql.DB, checksumExpected bool) {
	t.Helper()
	require.True(t, sqliteTableExists(t, db, "embedding_cache_entries"))
	for _, column := range []string{
		"tenant_id", "model_id", "model_fingerprint", "request_options_sha256", "text_sha256",
		"embedding", "dimension", "expires_at", "accessed_at",
	} {
		require.Truef(t, sqliteColumnExists(t, db, "embedding_cache_entries", column),
			"SQLite embedding_cache_entries must contain column %s", column)
	}
	require.False(t, sqliteColumnExists(t, db, "embedding_cache_entries", "text"),
		"embedding cache schema must not persist source text")
	require.Equal(t, checksumExpected, sqliteColumnExists(t, db, "embedding_cache_entries", "checksum_sha256"))
}

func assertSQLiteModelStatisticsSchema(t *testing.T, db *sql.DB) {
	t.Helper()
	require.True(t, sqliteTableExists(t, db, "embedding_cache_lookup_records"))
	for _, column := range []string{
		"tenant_id", "model_id", "requested_items", "unique_items", "hit_items", "miss_items",
		"bypass_items", "status", "duration_ms", "occurred_at",
	} {
		require.Truef(t, sqliteColumnExists(t, db, "embedding_cache_lookup_records", column),
			"SQLite embedding_cache_lookup_records must contain column %s", column)
	}
	require.False(t, sqliteColumnExists(t, db, "embedding_cache_lookup_records", "text_sha256"),
		"cache statistics must not persist cache keys")
}

func assertSQLiteHumanRatingsSchema(t *testing.T, db *sql.DB) {
	t.Helper()
	require.True(t, sqliteTableExists(t, db, "evaluation_human_ratings"))
	for _, column := range []string{
		"tenant_id", "task_id", "sample_index", "revision", "rater_id",
		"rubric_key", "rubric_version", "rubric_snapshot", "score", "comment", "supersedes_id", "created_at",
	} {
		require.Truef(t, sqliteColumnExists(t, db, "evaluation_human_ratings", column),
			"SQLite evaluation_human_ratings must contain column %s", column)
	}
}

func TestSQLiteMigrationsUpgradeV4PreservesData(t *testing.T) {
	repoRoot := sqliteRepoRoot(t)

	dbPath := filepath.Join(t.TempDir(), "upgrade.db")
	dbSeed, err := sql.Open("sqlite3", dbPath)
	require.NoError(t, err)
	for _, name := range []string{
		"000000_init",
		"000001_remove_wiki_log",
		"000002_knowledge_folder_path",
		"000003_knowledge_base_auto_tag_config",
		"000004_memory",
	} {
		data, readErr := os.ReadFile(filepath.Join(repoRoot, "migrations", "sqlite", name+".up.sql"))
		require.NoError(t, readErr)
		_, execErr := dbSeed.Exec(string(data))
		require.NoError(t, execErr)
	}
	_, err = dbSeed.Exec("CREATE TABLE schema_migrations(version BIGINT NOT NULL PRIMARY KEY," +
		"dirty BOOLEAN NOT NULL); INSERT INTO schema_migrations VALUES(4,false)")
	require.NoError(t, err)
	require.NoError(t, dbSeed.Close())

	db := openSQLiteDB(t, dbPath)
	versionBefore, dirtyBefore := sqliteMigrationState(t, db)
	require.Equal(t, 4, versionBefore)
	require.False(t, dirtyBefore)
	_, err = db.Exec("INSERT INTO tenants (name, business) VALUES (?, ?)", "upgrade-sentinel", "migration-test")
	require.NoError(t, err)
	_, err = db.Exec(
		"INSERT INTO knowledges (id, tenant_id, knowledge_base_id, type, title,"+" source, tag_id) "+
			"VALUES (?, 1, ?, 'document', 'tagged-doc', 'manual', ?)",
		"legacy-knowledge-1", "legacy-kb-1", "legacy-tag-1",
	)
	require.NoError(t, err)

	// Run the full migration set from the repo root.
	chdirAndRestore(t, repoRoot)
	require.NoError(
		t,
		RunMigrationsWithOptions(
			"sqlite3://unused",
			MigrationOptions{SQLiteDBPath: dbPath, BackupID: "x03-v4-fixture"},
		),
	)

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
	require.NoError(
		t,
		db.QueryRow("SELECT name FROM tenants WHERE business = ?", "migration-test").Scan(&sentinelName),
	)
	require.Equal(t, "upgrade-sentinel", sentinelName)

	var relationCount int
	require.NoError(t, db.QueryRow(
		"SELECT COUNT(*) FROM knowledge_tag_relations WHERE knowledge_id = ? "+"AND tag_id = ?",
		"legacy-knowledge-1", "legacy-tag-1",
	).Scan(&relationCount))
	require.Equal(t, 1, relationCount)
	assertSQLiteEvaluationTaskSchema(t, db)
	assertSQLiteEmbeddingCacheSchema(t, db, false)
	assertSQLiteModelStatisticsSchema(t, db)
	assertSQLiteHumanRatingsSchema(t, db)
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
	// Foreign keys are connection-local; pinning the pool to one connection
	// keeps the pragma deterministic for cascade and FK assertions.
	db, err := sql.Open("sqlite3", dbPath+"?_foreign_keys=on")
	require.NoError(t, err)
	db.SetMaxOpenConns(1)
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
	require.NoError(t, db.QueryRow("SELECT COUNT(*) FROM pragma_table_info(?) WHERE name = ?", table, column).Scan(&n))
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
		"SELECT COUNT(*) FROM tenant_invitations WHERE tenant_id = 1 AND "+
			"invitee_user_id = '' AND status = 'pending'",
	).Scan(&count))
	require.Equal(t, 2, count)
}

func assertSQLiteMCPOAuthPrincipalUpsertWorks(t *testing.T, db *sql.DB) {
	t.Helper()
	_, err := db.Exec(
		"INSERT INTO mcp_services (id, tenant_id, name, transport_type) VALUES "+"(?, 1, 'svc', 'http')",
		"svc-migration-1",
	)
	require.NoError(t, err)

	tokenInsertPrefix := "INSERT INTO mcp_oauth_tokens " +
		"(id, tenant_id, user_id, service_id, principal_type, principal_id, " +
		"access_token) "
	_, err = db.Exec(
		tokenInsertPrefix + "VALUES ('tok-1', 1, 'u1', 'svc-migration-1', 'web_user', 'u1', " + "'token-1')",
	)
	require.NoError(t, err)

	_, err = db.Exec(
		tokenInsertPrefix + "VALUES ('tok-2', 1, 'u1', 'svc-migration-1', 'web_user', 'u1', " + "'token-2') " +
			"ON CONFLICT(tenant_id, principal_type, principal_id, service_id) " +
			"DO UPDATE SET access_token = excluded.access_token",
	)
	require.NoError(t, err)

	var accessToken string
	require.NoError(t, db.QueryRow(
		"SELECT access_token FROM mcp_oauth_tokens "+"WHERE tenant_id = 1 AND principal_type = 'web_user' "+
			"AND principal_id = 'u1' AND service_id = 'svc-migration-1'",
	).Scan(&accessToken))
	require.Equal(t, "token-2", accessToken)

	var rowCount int
	require.NoError(t, db.QueryRow(
		"SELECT COUNT(*) FROM mcp_oauth_tokens WHERE tenant_id = 1 AND "+"service_id = 'svc-migration-1'",
	).Scan(&rowCount))
	require.Equal(t, 1, rowCount)
}

func assertSQLiteEvaluationTaskSchema(t *testing.T, db *sql.DB) {
	t.Helper()

	assertSQLitePartialIndex(t, db, "idx_evaluation_tasks_tenant_started", "WHERE deleted_at IS NULL")
	assertSQLitePartialIndex(t, db, "idx_evaluation_tasks_tenant_status_started", "WHERE deleted_at IS NULL")
	assertSQLitePartialIndex(
		t,
		db,
		"idx_evaluation_tasks_active_lease",
		"WHERE deleted_at IS NULL AND status IN (0, 1)",
	)
	assertSQLitePartialIndex(
		t,
		db,
		"idx_evaluation_tasks_retention",
		"WHERE status IN (2, 3, 4, 5, 6) AND end_time IS NOT NULL",
	)

	insert := "INSERT INTO evaluation_tasks (\n\t\tid, tenant_id, dataset_id, status, " +
		"start_time, cleanup_errors, params, metric,\n\t\ttemporary_kb_id, " +
		"owner_id, lease_expires_at, heartbeat_at\n\t) VALUES (?, 1, 'default', " +
		"0, CURRENT_TIMESTAMP, ?, ?, ?, 'kb', 'owner', CURRENT_TIMESTAMP, " +
		"CURRENT_TIMESTAMP)"

	_, err := db.Exec(insert, "invalid-cleanup-errors", "not-json", `{}`, nil)
	require.Error(t, err)
	_, err = db.Exec(insert, "invalid-params", `[]`, "not-json", nil)
	require.Error(t, err)
	_, err = db.Exec(insert, "invalid-metric", `[]`, `{}`, "not-json")
	require.Error(t, err)

	_, err = db.Exec(insert, "nullable-lease", `[]`, `{}`, nil)
	require.NoError(t, err)
	_, err = db.Exec("UPDATE evaluation_tasks SET lease_expires_at = NULL WHERE id = ?", "nullable-lease")
	require.Error(t, err)
	_, err = db.Exec(
		"UPDATE evaluation_tasks SET status = 2, lease_expires_at = NULL WHERE "+"id = ?",
		"nullable-lease",
	)
	require.NoError(t, err)

	// 000095 / SQLite 000018: experiment snapshot columns and their partial index.
	assertSQLitePartialIndex(t, db, "idx_evaluation_tasks_dataset_version",
		"WHERE deleted_at IS NULL AND dataset_version_id IS NOT NULL")

	_, err = db.Exec("INSERT INTO evaluation_tasks (\n\t\tid, tenant_id, dataset_id, status, "+
		"start_time, cleanup_errors, params,\n\t\ttemporary_kb_id, owner_id, "+
		"lease_expires_at, heartbeat_at,\n\t\tdataset_version_id, "+
		"dataset_content_sha256, experiment_snapshot, experiment_sha256\n\t) "+
		"VALUES ('snapshot-row', 1, 'default', 0, CURRENT_TIMESTAMP, '[]', "+
		"'{}', 'kb', 'owner',\n\t\tCURRENT_TIMESTAMP, CURRENT_TIMESTAMP, "+
		"'version-1', ?, ?, ?)",
		strings.Repeat("a", 64), `{"schema_version":1}`, strings.Repeat("b", 64))
	require.NoError(t, err)

	// Pre-M3 rows keep null provenance.
	var nullSnapshot sql.NullString
	require.NoError(t, db.QueryRow(
		"SELECT experiment_snapshot FROM evaluation_tasks WHERE id = "+"'nullable-lease'",
	).Scan(&nullSnapshot))
	require.False(t, nullSnapshot.Valid)

	// Malformed hashes and non-JSON snapshots are rejected.
	_, err = db.Exec("UPDATE evaluation_tasks SET dataset_content_sha256 = 'short' WHERE id " +
		"= 'snapshot-row'")
	require.Error(t, err)
	_, err = db.Exec("UPDATE evaluation_tasks SET experiment_snapshot = 'not-json' WHERE id " +
		"= 'snapshot-row'")
	require.Error(t, err)
}

func assertSQLitePartialIndex(t *testing.T, db *sql.DB, name, predicate string) {
	t.Helper()
	var definition string
	require.NoError(t, db.QueryRow(
		"SELECT sql FROM sqlite_master WHERE type = 'index' AND name = ?", name,
	).Scan(&definition))
	normalizedDefinition := strings.Join(strings.Fields(strings.ToLower(definition)), " ")
	normalizedPredicate := strings.Join(strings.Fields(strings.ToLower(predicate)), " ")
	require.Contains(t, normalizedDefinition, normalizedPredicate)

	var partial int
	require.NoError(t, db.QueryRow(
		"SELECT partial FROM pragma_index_list('evaluation_tasks') WHERE name = ?",
		name,
	).Scan(&partial))
	require.Equal(t, 1, partial)
}

// assertSQLiteEvaluationQuestionResultsSchema verifies the 000096 / SQLite
// 000019 per-question table: composite foreign key, JSON validity checks,
// result hash length, and the partial pagination index.
func assertSQLiteEvaluationQuestionResultsSchema(t *testing.T, db *sql.DB) {
	t.Helper()

	var definition string
	require.NoError(t, db.QueryRow(
		"SELECT sql FROM sqlite_master WHERE type = 'index' AND name = "+
			"'idx_evaluation_question_results_task_page'",
	).Scan(&definition))
	require.Contains(t, strings.ToLower(definition), "where deleted_at is null")

	insertTask := "INSERT INTO evaluation_tasks (\n\t\tid, tenant_id, dataset_id, status, " +
		"start_time, cleanup_errors, params,\n\t\ttemporary_kb_id, owner_id, " +
		"lease_expires_at, heartbeat_at\n\t) VALUES (?, 1, 'default', 1, " +
		"CURRENT_TIMESTAMP, '[]', '{}', 'kb', 'owner',\n\t\tCURRENT_TIMESTAMP, " +
		"CURRENT_TIMESTAMP)"
	_, err := db.Exec(insertTask, "task-for-questions")
	require.NoError(t, err)

	insertRow := "INSERT INTO evaluation_question_results (\n\t\ttenant_id, task_id, " +
		"sample_index, qid, question, reference_answer,\n\t\tground_truth_pids, " +
		"search_results, rerank_results, generation_pids,\n\t\tper_sample_metrics," +
		" metric_observations, status, result_hash\n\t) VALUES (1, " +
		"'task-for-questions', ?, 'q1', 'question?', 'answer',\n\t\t'[3]', ?, ?, " +
		"'[3]', '{}', '[]', 'success', ?)"
	validRanked := `[{"rank":1,"pid":3,"score":0.9,"provenance":"known"}]`
	validHash := strings.Repeat("c", 64)
	_, err = db.Exec(insertRow, 0, validRanked, validRanked, validHash)
	require.NoError(t, err)

	// Composite primary key rejects duplicates.
	_, err = db.Exec(insertRow, 0, validRanked, validRanked, validHash)
	require.Error(t, err)
	// The composite foreign key rejects rows without a parent task.
	_, err = db.Exec("INSERT INTO evaluation_question_results (\n\t\ttenant_id, task_id, "+
		"sample_index, qid, question, status, result_hash\n\t) VALUES (1, "+
		"'task-missing', 0, 'q1', 'question?', 'success', ?)", validHash)
	require.Error(t, err)
	// JSON validity and hash length are enforced.
	_, err = db.Exec(insertRow, 1, "not-json", validRanked, validHash)
	require.Error(t, err)
	_, err = db.Exec(insertRow, 1, validRanked, validRanked, "short")
	require.Error(t, err)
	// Cascade delete removes the per-question rows with their task.
	_, err = db.Exec("DELETE FROM evaluation_tasks WHERE id = 'task-for-questions'")
	require.NoError(t, err)
	var remaining int
	require.NoError(t, db.QueryRow(
		"SELECT COUNT(*) FROM evaluation_question_results WHERE task_id = "+"'task-for-questions'",
	).Scan(&remaining))
	require.Zero(t, remaining)
}

func assertSQLiteEvaluationTaskLabelsSchema(t *testing.T, db *sql.DB) {
	t.Helper()
	require.True(t, sqliteTableExists(t, db, "evaluation_task_labels"))
	var definition string
	require.NoError(t, db.QueryRow(
		"SELECT sql FROM sqlite_master WHERE type = 'table' AND name = "+"'evaluation_task_labels'",
	).Scan(&definition))
	normalizedTableDefinition := strings.ToLower(definition)
	require.Contains(t, normalizedTableDefinition, "evaluation_task_labels_label_bytes_check")
	require.Contains(t, normalizedTableDefinition, "length(cast(label as blob)) <= 64")
	require.Contains(t, normalizedTableDefinition, "foreign key (tenant_id, task_id)")
	require.Contains(t, normalizedTableDefinition, "on delete cascade")
	require.NoError(t, db.QueryRow(
		"SELECT sql FROM sqlite_master WHERE type = 'index' "+
			"AND name = 'idx_evaluation_task_labels_tenant_label_task'",
	).Scan(&definition))
	require.Contains(t, strings.ToLower(definition), "tenant_id, label, task_id")
	for _, index := range []string{
		"idx_evaluation_tasks_tenant_dataset_started",
		"idx_evaluation_tasks_tenant_dataset_version_started",
	} {
		assertSQLitePartialIndex(t, db, index, "WHERE deleted_at IS NULL")
	}
}
