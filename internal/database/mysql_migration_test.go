package database

import (
	"database/sql"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/golang-migrate/migrate/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMySQLMigrationSchemaParity(t *testing.T) {
	up, err := os.ReadFile("../../migrations/mysql/000074_init.up.sql")
	require.NoError(t, err)
	down, err := os.ReadFile("../../migrations/mysql/000074_init.down.sql")
	require.NoError(t, err)

	tablePattern := regexp.MustCompile(`(?m)^CREATE TABLE ([a-z_]+) \(`)
	matches := tablePattern.FindAllStringSubmatch(string(up), -1)
	actual := make([]string, 0, len(matches))
	for _, match := range matches {
		actual = append(actual, match[1])
	}
	sort.Strings(actual)

	expected := []string{
		"agent_shares",
		"audit_logs",
		"auth_tokens",
		"chunks",
		"custom_agents",
		"data_sources",
		"embed_channels",
		"im_channel_sessions",
		"im_channels",
		"kb_shares",
		"knowledge_bases",
		"knowledge_processing_spans",
		"knowledge_tag_relations",
		"knowledge_tags",
		"knowledges",
		"mcp_oauth_clients",
		"mcp_oauth_tokens",
		"mcp_services",
		"mcp_tool_approvals",
		"message_suggestion_events",
		"message_suggestion_sets",
		"messages",
		"models",
		"organization_join_requests",
		"organization_tenant_members",
		"organizations",
		"resource_access_grants",
		"resource_bindings",
		"resources",
		"sessions",
		"storage_backends",
		"sync_logs",
		"system_settings",
		"task_dead_letters",
		"task_pending_ops",
		"temporary_documents",
		"tenant_api_keys",
		"tenant_disabled_shared_agents",
		"tenant_invitations",
		"tenant_members",
		"tenants",
		"user_kb_pins",
		"user_resource_favorites",
		"users",
		"vector_stores",
		"web_search_providers",
		"wiki_folders",
		"wiki_log_entries",
		"wiki_page_issues",
		"wiki_pages",
	}
	assert.Equal(t, expected, actual)
	assert.NotContains(t, string(up), "CREATE TABLE embeddings")

	for _, table := range expected {
		assert.Contains(t, string(down), "DROP TABLE IF EXISTS "+table+";")
	}

	sqlWithoutComments := regexp.MustCompile(`(?m)--.*$`).ReplaceAllString(string(up), "")
	unsupported := regexp.MustCompile(`(?m)\b(ILIKE|JSONB|BIGSERIAL|AUTOINCREMENT)\b|::|^\s*WHERE\s`)
	assert.Empty(t, unsupported.FindAllString(sqlWithoutComments, -1))
}

func TestMySQLMigrationRoundTrip(t *testing.T) {
	migrationURL := os.Getenv("WEKNORA_MYSQL_TEST_MIGRATION_URL")
	sqlDSN := os.Getenv("WEKNORA_MYSQL_TEST_DSN")
	if migrationURL == "" || sqlDSN == "" {
		t.Skip("set WEKNORA_MYSQL_TEST_MIGRATION_URL and WEKNORA_MYSQL_TEST_DSN")
	}

	workingDirectory, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir("../.."))
	t.Cleanup(func() {
		require.NoError(t, os.Chdir(workingDirectory))
	})

	db, err := sql.Open("mysql", sqlDSN)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, db.Close())
	})
	require.NoError(t, db.Ping())

	require.NoError(t, RunMigrationsWithOptions(migrationURL, MigrationOptions{
		FailOnDirty: true,
	}))
	assertMigrationState(t, db, 74, false)
	assertBusinessTableCount(t, db, 50)

	migrator, err := migrate.New("file://migrations/mysql", migrationURL)
	require.NoError(t, err)
	require.NoError(t, migrator.Down())
	assertBusinessTableCount(t, db, 0)
	require.NoError(t, migrator.Up())
	assertMigrationState(t, db, 74, false)
	assertBusinessTableCount(t, db, 50)
	sourceErr, databaseErr := migrator.Close()
	require.NoError(t, sourceErr)
	require.NoError(t, databaseErr)

	assertMySQLSoftDeleteUniqueness(t, db)

	_, err = db.Exec("UPDATE schema_migrations SET dirty = 1 WHERE version = 74")
	require.NoError(t, err)
	err = RunMigrationsWithOptions(migrationURL, MigrationOptions{
		AutoRecoverDirty: true,
		FailOnDirty:      true,
	})
	require.ErrorContains(t, err, "automatic recovery is disabled")
	_, restoreErr := db.Exec("UPDATE schema_migrations SET dirty = 0 WHERE version = 74")
	require.NoError(t, restoreErr)
}

func assertMigrationState(t *testing.T, db *sql.DB, version int, dirty bool) {
	t.Helper()
	var actualVersion int
	var actualDirty bool
	require.NoError(t, db.QueryRow(
		"SELECT version, dirty FROM schema_migrations",
	).Scan(&actualVersion, &actualDirty))
	assert.Equal(t, version, actualVersion)
	assert.Equal(t, dirty, actualDirty)
}

func assertBusinessTableCount(t *testing.T, db *sql.DB, expected int) {
	t.Helper()
	var count int
	require.NoError(t, db.QueryRow(`
		SELECT COUNT(*)
		FROM information_schema.tables
		WHERE table_schema = DATABASE()
		  AND table_name <> 'schema_migrations'
	`).Scan(&count))
	assert.Equal(t, expected, count)

	var embeddings int
	require.NoError(t, db.QueryRow(`
		SELECT COUNT(*)
		FROM information_schema.tables
		WHERE table_schema = DATABASE()
		  AND table_name = 'embeddings'
	`).Scan(&embeddings))
	assert.Zero(t, embeddings)
}

func assertMySQLSoftDeleteUniqueness(t *testing.T, db *sql.DB) {
	t.Helper()
	_, err := db.Exec(`
		INSERT INTO storage_backends
			(id, tenant_id, name, provider, config, deleted_at)
		VALUES
			('deleted-backend', 1, 'shared-name', 'local', JSON_OBJECT(), NOW(6)),
			('live-backend', 1, 'shared-name', 'local', JSON_OBJECT(), NULL)
	`)
	require.NoError(t, err)

	_, err = db.Exec(`
		INSERT INTO storage_backends
			(id, tenant_id, name, provider, config)
		VALUES
			('duplicate-live-backend', 1, 'shared-name', 'cos', JSON_OBJECT())
	`)
	require.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), "Duplicate entry"), err.Error())
}
