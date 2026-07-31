package database

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	gomysql "github.com/go-sql-driver/mysql"
)

func TestMigrationSourceForDSNUsesMySQLDirectory(t *testing.T) {
	tests := []struct {
		dsn  string
		want string
	}{
		{dsn: "mysql://user:pass@tcp(mysql:3306)/WeKnora", want: "file://migrations/mysql"},
		{dsn: "postgres://user:pass@postgres:5432/WeKnora", want: "file://migrations/versioned"},
		{dsn: "sqlite3://data/weknora.db", want: "file://migrations/sqlite"},
	}

	for _, tt := range tests {
		if got := migrationSourceForDSN(tt.dsn); got != tt.want {
			t.Fatalf("migrationSourceForDSN(%q) = %q, want %q", tt.dsn, got, tt.want)
		}
	}
}

func TestMigrationDSNFromEnvBuildsMySQLURL(t *testing.T) {
	t.Setenv("DB_DRIVER", "mysql")
	t.Setenv("DB_HOST", "mysql")
	t.Setenv("DB_PORT", "3306")
	t.Setenv("DB_USER", "weknora")
	t.Setenv("DB_PASSWORD", "p@ss word#1")
	t.Setenv("DB_NAME", "WeKnora")

	dsn, err := migrationDSNFromEnv()
	if err != nil {
		t.Fatalf("migrationDSNFromEnv() error = %v", err)
	}
	for _, want := range []string{
		"mysql://weknora:",
		"@tcp(mysql:3306)/WeKnora",
		"charset=utf8mb4",
		"multiStatements=true",
		"parseTime=true",
		"loc=UTC",
		"sql_mode=",
		"time_zone=",
	} {
		if !strings.Contains(dsn, want) {
			t.Fatalf("mysql migration dsn missing %q in %s", want, dsn)
		}
	}
}

func TestMigrationRuntimeConfigFromEnvUsesNativeMySQLDSN(t *testing.T) {
	setMySQLMainDatabaseEnv(t)

	dsn, opts, err := migrationRuntimeConfigFromEnv()
	if err != nil {
		t.Fatalf("migrationRuntimeConfigFromEnv() error = %v", err)
	}
	if !strings.HasPrefix(dsn, "mysql://") {
		t.Fatalf("migration marker/source DSN = %q", dsn)
	}
	if opts.MySQLDSN == "" {
		t.Fatal("native MySQL migration DSN is empty")
	}
	cfg, err := gomysql.ParseDSN(opts.MySQLDSN)
	if err != nil {
		t.Fatalf("parse native MySQL migration DSN: %v", err)
	}
	if !cfg.MultiStatements {
		t.Fatal("native MySQL migration DSN must enable multiStatements")
	}
}

func TestMigrationScriptEnforcesMySQLSessionContract(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", "scripts", "migrate.sh"))
	if err != nil {
		t.Fatalf("read migration script: %v", err)
	}
	text := string(body)
	for _, want := range []string{
		`printf -v byte '%%%02X' "'$char"`,
		`MYSQL_TIME_ZONE_VALUE="%27%2B00%3A00%27"`,
		`MYSQL_SQL_MODE_VALUE="%27ONLY_FULL_GROUP_BY%2CSTRICT_TRANS_TABLES%2CNO_ZERO_IN_DATE%2CNO_ZERO_DATE%2CERROR_FOR_DIVISION_BY_ZERO%2CNO_ENGINE_SUBSTITUTION%27"`,
		`set_query_param "$DB_URL" "time_zone" "$MYSQL_TIME_ZONE_VALUE"`,
		`set_query_param "$DB_URL" "sql_mode" "$MYSQL_SQL_MODE_VALUE"`,
		`&time_zone=${MYSQL_TIME_ZONE_VALUE}&sql_mode=${MYSQL_SQL_MODE_VALUE}`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("scripts/migrate.sh missing MySQL session contract fragment %q", want)
		}
	}
	for _, forbidden := range []string{
		`command -v python3`,
		`echo "DB_URL:`,
		`echo "DB_PASSWORD:`,
	} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("scripts/migrate.sh must not contain unsafe fragment %q", forbidden)
		}
	}
}

func TestMySQLDirtyAutoRecoveryIsNeverAllowed(t *testing.T) {
	if dirtyAutoRecoveryAllowed("mysql://user:pass@tcp(mysql:3306)/WeKnora") {
		t.Fatal("MySQL dirty auto-recovery must be disabled")
	}
	for _, dsn := range []string{
		"postgres://user:pass@postgres:5432/WeKnora",
		"sqlite3://data/weknora.db",
	} {
		if !dirtyAutoRecoveryAllowed(dsn) {
			t.Fatalf("dirtyAutoRecoveryAllowed(%q) = false", dsn)
		}
	}
}

func TestMySQLDirtyStateErrorRequiresManualRepair(t *testing.T) {
	err := mysqlDirtyStateError(67, fmt.Errorf("duplicate column"))
	for _, want := range []string{
		"version 67",
		"automatic force/retry is disabled",
		"repair partial schema changes",
		"duplicate column",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("mysqlDirtyStateError() missing %q: %v", want, err)
		}
	}
}

func TestRunStartupMigrationsClosesDatabaseOnFailure(t *testing.T) {
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New() error = %v", err)
	}
	mock.ExpectClose()

	err = RunStartupMigrations(
		sqlDB,
		"unsupported://migration-driver",
		MigrationOptions{},
	)
	if err == nil || !strings.Contains(err.Error(), "database migration failed") {
		t.Fatalf("RunStartupMigrations() error = %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("database was not closed after migration failure: %v", err)
	}
}

func TestMySQLMigrationsCoverCurrentMainVersion(t *testing.T) {
	mysqlVersions := mysqlMigrationVersions(t)
	if len(mysqlVersions) == 0 {
		t.Fatal("no MySQL migrations found")
	}

	postgresFiles, err := filepath.Glob(filepath.Join("..", "..", "migrations", "versioned", "*.up.sql"))
	if err != nil {
		t.Fatalf("glob PostgreSQL migrations: %v", err)
	}
	postgresHead := 0
	for _, file := range postgresFiles {
		base := filepath.Base(file)
		if len(base) < 6 {
			continue
		}
		version, err := strconv.Atoi(base[:6])
		if err != nil {
			continue
		}
		if version > postgresHead {
			postgresHead = version
		}
		// Version 64 is a full MySQL baseline. Every PostgreSQL schema change
		// after that point requires a same-version MySQL up/down pair.
		if version >= 64 && !mysqlVersions[version] {
			t.Fatalf("missing MySQL migration for PostgreSQL version %06d", version)
		}
	}
	if postgresHead == 0 {
		t.Fatal("no PostgreSQL migrations found")
	}

	mysqlHead := 0
	for version := range mysqlVersions {
		if version > mysqlHead {
			mysqlHead = version
		}
	}
	if mysqlHead != postgresHead {
		t.Fatalf("MySQL migration head=%d, PostgreSQL migration head=%d", mysqlHead, postgresHead)
	}
}

func TestMySQLMigrationsMirrorRecentPostgresSchema(t *testing.T) {
	files := map[string][]string{
		filepath.Join("..", "..", "migrations", "mysql", "000075_wiki_page_revisions.up.sql"): {
			"ALTER TABLE wiki_pages",
			"ADD COLUMN last_edit_source",
			"CREATE TABLE IF NOT EXISTS wiki_page_revisions",
			"UNIQUE KEY idx_wiki_page_revisions_page_version",
		},
		filepath.Join("..", "..", "migrations", "mysql", "000076_knowledge_metadata_external_id_index.up.sql"): {
			"metadata_external_id VARCHAR(2048)",
			"JSON_UNQUOTE(JSON_EXTRACT(metadata, '$.\"external_id\"'))",
			"idx_knowledges_kb_metadata_external_id",
		},
		filepath.Join("..", "..", "migrations", "mysql", "000077_remove_wiki_log.up.sql"): {
			"DROP TABLE IF EXISTS wiki_log_entries",
			"DELETE FROM wiki_pages WHERE page_type = 'log'",
		},
		filepath.Join("..", "..", "migrations", "mysql", "000078_chunk_editing_and_custom_metadata.up.sql"): {
			"ADD COLUMN source_content",
			"ADD COLUMN custom_metadata JSON NOT NULL DEFAULT (JSON_OBJECT())",
			"CREATE TABLE IF NOT EXISTS chunk_revisions",
			"UNIQUE KEY idx_chunk_revisions_chunk_revision",
		},
		filepath.Join("..", "..", "migrations", "mysql", "000079_session_defaults.up.sql"): {
			"MODIFY COLUMN fallback_response TEXT NOT NULL",
			"DEFAULT ('很抱歉，我暂时无法回答这个问题。')",
			"MODIFY COLUMN summary_parameters JSON NOT NULL",
			"DEFAULT (JSON_OBJECT())",
		},
	}

	for file, wants := range files {
		body, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("read %s: %v", file, err)
		}
		text := string(body)
		for _, want := range wants {
			if !strings.Contains(text, want) {
				t.Fatalf("%s missing %q", file, want)
			}
		}
	}
}

func TestMySQLSessionDefaultsMatchApplicationCreate(t *testing.T) {
	baseline, err := os.ReadFile(filepath.Join("..", "..", "migrations", "mysql", "000064_mysql_baseline.up.sql"))
	if err != nil {
		t.Fatalf("read MySQL baseline: %v", err)
	}
	sessionDDL := mysqlCreateTableDDL(t, string(baseline), "sessions")
	for _, want := range []string{
		"fallback_response TEXT NOT NULL DEFAULT ('很抱歉，我暂时无法回答这个问题。')",
		"summary_parameters JSON NOT NULL DEFAULT (JSON_OBJECT())",
	} {
		if !strings.Contains(sessionDDL, want) {
			t.Fatalf("sessions baseline missing runtime create default %q", want)
		}
	}

	up, err := os.ReadFile(filepath.Join("..", "..", "migrations", "mysql", "000079_session_defaults.up.sql"))
	if err != nil {
		t.Fatalf("read MySQL session defaults migration: %v", err)
	}
	for _, want := range []string{
		"MODIFY COLUMN fallback_response TEXT NOT NULL",
		"DEFAULT ('很抱歉，我暂时无法回答这个问题。')",
		"MODIFY COLUMN summary_parameters JSON NOT NULL",
		"DEFAULT (JSON_OBJECT())",
	} {
		if !strings.Contains(string(up), want) {
			t.Fatalf("MySQL session defaults migration missing %q", want)
		}
	}

	down, err := os.ReadFile(filepath.Join("..", "..", "migrations", "mysql", "000079_session_defaults.down.sql"))
	if err != nil {
		t.Fatalf("read MySQL session defaults rollback: %v", err)
	}
	for _, want := range []string{
		"MODIFY COLUMN fallback_response TEXT NOT NULL",
		"DEFAULT ('很抱歉，我暂时无法回答这个问题。')",
		"MODIFY COLUMN summary_parameters JSON NOT NULL",
		"DEFAULT (JSON_OBJECT())",
	} {
		if !strings.Contains(string(down), want) {
			t.Fatalf("MySQL session defaults rollback must preserve version-78 default %q", want)
		}
	}
}

func TestMySQLBaselineTenantMembersUniqueIndexAllowsSoftDeletedReadd(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", "migrations", "mysql", "000064_mysql_baseline.up.sql"))
	if err != nil {
		t.Fatalf("read mysql baseline: %v", err)
	}
	text := string(body)
	for _, want := range []string{
		"active_unique_key TINYINT",
		"CASE WHEN deleted_at IS NULL THEN 1 ELSE NULL END",
		"CREATE UNIQUE INDEX idx_tenant_members_user_tenant_unique",
		"ON tenant_members(user_id, tenant_id, active_unique_key)",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("tenant_members MySQL baseline missing %q", want)
		}
	}

	for _, name := range []string{
		"000066_expand_knowledge_span_name.up.sql",
		"000066_expand_knowledge_span_name.down.sql",
	} {
		body, err := os.ReadFile(filepath.Join("..", "..", "migrations", "mysql", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if strings.Contains(string(body), "tenant_members") {
			t.Fatalf("%s must not change the baseline tenant_members invariant", name)
		}
	}
}

func TestMySQLBaselinePreservesPartialUniqueConstraints(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", "migrations", "mysql", "000064_mysql_baseline.up.sql"))
	if err != nil {
		t.Fatalf("read mysql baseline migration: %v", err)
	}
	text := string(body)
	wants := []string{
		"active_unique_key TINYINT",
		"pending_invitee_unique_key TINYINT",
		"active_token_unique_key TINYINT",
		"active_invite_code_unique_key TINYINT",
		"active_kb_share_unique_key TINYINT",
		"pending_request_unique_key TINYINT",
		"active_agent_share_unique_key TINYINT",
		"active_session_unique_key TINYINT",
		"active_thread_session_unique_key TINYINT",
		"active_bot_identity_unique_key TINYINT",
		"active_publish_token_unique_key TINYINT",
		"active_vector_store_unique_key TINYINT",
		"active_wiki_page_unique_key TINYINT",
		"active_wiki_folder_unique_key TINYINT",
		"token VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin",
		"CONSTRAINT chk_im_channels_session_mode",
		"CHECK (session_mode IN ('user', 'thread'))",
		"CREATE UNIQUE INDEX idx_tenant_invitations_unique_pending",
		"CREATE UNIQUE INDEX idx_tenant_invitations_token",
		"CREATE UNIQUE INDEX idx_organizations_invite_code",
		"CREATE UNIQUE INDEX idx_kb_shares_kb_org",
		"CREATE UNIQUE INDEX uq_org_join_requests_pending_per_tenant",
		"CREATE UNIQUE INDEX idx_agent_shares_agent_org",
		"CREATE UNIQUE INDEX idx_channel_lookup",
		"CREATE UNIQUE INDEX idx_channel_thread_lookup",
		"CREATE UNIQUE INDEX idx_im_channels_bot_identity",
		"CREATE UNIQUE INDEX idx_embed_channels_publish_token",
		"CREATE UNIQUE INDEX idx_vector_stores_name_tenant",
		"CREATE UNIQUE INDEX idx_wiki_pages_kb_slug",
		"CREATE UNIQUE INDEX idx_wiki_folders_parent_name",
		"claim_token CHAR(36) CHARACTER SET ascii COLLATE ascii_bin",
		"CREATE INDEX idx_task_pending_ops_claim_token",
	}
	for _, want := range wants {
		if !strings.Contains(text, want) {
			t.Errorf("mysql baseline migration missing %q", want)
		}
	}
}

func TestMySQLLargeContentColumnsSupportApplicationContracts(t *testing.T) {
	baseline, err := os.ReadFile(filepath.Join("..", "..", "migrations", "mysql", "000064_mysql_baseline.up.sql"))
	if err != nil {
		t.Fatalf("read MySQL baseline: %v", err)
	}
	baselineText := string(baseline)
	baselineWants := map[string][]string{
		"messages": {
			"content LONGTEXT NOT NULL",
			"rendered_content LONGTEXT",
		},
		"chunks": {
			"content LONGTEXT NOT NULL",
		},
		"wiki_pages": {
			"content LONGTEXT",
		},
	}
	for table, wants := range baselineWants {
		ddl := mysqlCreateTableDDL(t, baselineText, table)
		for _, want := range wants {
			if !strings.Contains(ddl, want) {
				t.Fatalf("%s must contain %q to preserve the application content contract", table, want)
			}
		}
	}

	migrationWants := map[string][]string{
		"000070_temporary_documents.up.sql": {
			"content LONGTEXT NOT NULL",
		},
		"000075_wiki_page_revisions.up.sql": {
			"content LONGTEXT NOT NULL",
		},
		"000078_chunk_editing_and_custom_metadata.up.sql": {
			"ADD COLUMN source_content LONGTEXT",
			"MODIFY COLUMN source_content LONGTEXT",
			"content LONGTEXT NOT NULL",
		},
	}
	for name, wants := range migrationWants {
		body, err := os.ReadFile(filepath.Join("..", "..", "migrations", "mysql", name))
		if err != nil {
			t.Fatalf("read MySQL migration %s: %v", name, err)
		}
		for _, want := range wants {
			if !strings.Contains(string(body), want) {
				t.Fatalf("MySQL migration %s missing %q", name, want)
			}
		}
	}
}

func TestMySQLOpaqueIdentifiersUseCaseSensitiveCollations(t *testing.T) {
	baseline, err := os.ReadFile(filepath.Join("..", "..", "migrations", "mysql", "000064_mysql_baseline.up.sql"))
	if err != nil {
		t.Fatalf("read MySQL baseline: %v", err)
	}
	text := string(baseline)
	tableWants := map[string][]string{
		"sessions": {
			"user_id VARCHAR(512) COLLATE utf8mb4_bin",
		},
		"mcp_oauth_tokens": {
			"principal_type VARCHAR(32) COLLATE utf8mb4_bin",
			"principal_id VARCHAR(512) COLLATE utf8mb4_bin",
		},
		"mcp_tool_approvals": {
			"tool_name VARCHAR(512) COLLATE utf8mb4_bin",
		},
		"im_channel_sessions": {
			"user_id VARCHAR(128) COLLATE utf8mb4_bin",
			"chat_id VARCHAR(128) COLLATE utf8mb4_bin",
			"thread_id VARCHAR(128) COLLATE utf8mb4_bin",
		},
		"im_channels": {
			"bot_identity VARCHAR(255) COLLATE utf8mb4_bin",
		},
		"embed_channels": {
			"publish_token VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin",
		},
	}
	for table, wants := range tableWants {
		ddl := mysqlCreateTableDDL(t, text, table)
		for _, want := range wants {
			if !strings.Contains(ddl, want) {
				t.Fatalf("%s opaque identifier must contain %q", table, want)
			}
		}
	}

	externalIDMigration, err := os.ReadFile(filepath.Join(
		"..", "..", "migrations", "mysql", "000076_knowledge_metadata_external_id_index.up.sql",
	))
	if err != nil {
		t.Fatalf("read MySQL external-id migration: %v", err)
	}
	if !strings.Contains(
		string(externalIDMigration),
		"metadata_external_id VARCHAR(2048) COLLATE utf8mb4_bin",
	) {
		t.Fatal("metadata_external_id must preserve case-sensitive external node identity")
	}

	resourceMigration, err := os.ReadFile(filepath.Join(
		"..", "..", "migrations", "mysql", "000069_resource_registry.up.sql",
	))
	if err != nil {
		t.Fatalf("read MySQL resource migration: %v", err)
	}
	if !strings.Contains(
		string(resourceMigration),
		"handle VARCHAR(22) CHARACTER SET ascii COLLATE ascii_bin",
	) {
		t.Fatal("resources.handle must preserve its case-sensitive base64url identity")
	}
}

func mysqlCreateTableDDL(t *testing.T, migration string, table string) string {
	t.Helper()
	header := "CREATE TABLE " + table
	start := strings.Index(migration, header)
	if start < 0 {
		t.Fatalf("locate %s table in MySQL baseline", table)
	}
	afterHeader := start + len(header)
	end := strings.Index(migration[afterHeader:], "CREATE TABLE")
	if end < 0 {
		end = len(migration) - afterHeader
	}
	return migration[start : afterHeader+end]
}

func TestMySQLDeploymentConfigWiresOfficialArtifacts(t *testing.T) {
	files := map[string][]string{
		filepath.Join("..", "..", "docker-compose.mysql.yml"): {
			"mysql:",
			"mysql-data:",
			"DB_DRIVER=mysql",
			"MYSQL_USERNAME=${DB_USER:-weknora}",
			"depends_on: !override",
			"condition: service_healthy",
		},
		filepath.Join("..", "..", "docker", "Dockerfile.app"): {
			"go install -tags 'postgres mysql sqlite3'",
		},
		filepath.Join("..", "..", "helm", "templates", "app.yaml"): {
			`value: {{ include "weknora.databaseDriver" . | quote }}`,
			`value: {{ include "weknora.databaseHost" . | quote }}`,
			`value: {{ include "weknora.databasePort" . | quote }}`,
			"name: MYSQL_USERNAME",
		},
		filepath.Join("..", "..", "helm", "values.yaml"): {
			"database:",
			"driver: postgres",
			"host: postgres",
			`port: "5432"`,
			"mysql:",
			`port: "3306"`,
		},
	}

	for file, wants := range files {
		body, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("read %s: %v", file, err)
		}
		text := string(body)
		for _, want := range wants {
			if !strings.Contains(text, want) {
				t.Fatalf("%s missing %q", file, want)
			}
		}
	}
}

func mysqlMigrationVersions(t *testing.T) map[int]bool {
	t.Helper()

	files, err := filepath.Glob(filepath.Join("..", "..", "migrations", "mysql", "*.up.sql"))
	if err != nil {
		t.Fatalf("glob mysql migrations: %v", err)
	}
	versions := make(map[int]bool, len(files))
	for _, file := range files {
		base := filepath.Base(file)
		if len(base) < 6 {
			continue
		}
		version, err := strconv.Atoi(base[:6])
		if err != nil {
			continue
		}
		down := filepath.Join(filepath.Dir(file), base[:6]+base[6:len(base)-len(".up.sql")]+".down.sql")
		if _, err := os.Stat(down); err != nil {
			t.Fatalf("missing down migration for %s: %v", base, err)
		}
		versions[version] = true
	}
	return versions
}
