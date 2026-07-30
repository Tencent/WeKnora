package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	gomysql "github.com/go-sql-driver/mysql"
	"github.com/golang-migrate/migrate/v4"
)

func TestMySQLSessionContractIntegration(t *testing.T) {
	dsn := os.Getenv("WEKNORA_MYSQL_TEST_DSN")
	if dsn == "" {
		t.Skip("set WEKNORA_MYSQL_TEST_DSN to run the real MySQL session integration test")
	}
	cfg, err := gomysql.ParseDSN(dsn)
	if err != nil {
		t.Fatalf("parse WEKNORA_MYSQL_TEST_DSN: %v", err)
	}
	db, err := sql.Open("mysql", BuildMySQLApplicationDSN(
		cfg.User,
		cfg.Passwd,
		cfg.Addr,
		cfg.DBName,
	))
	if err != nil {
		t.Fatalf("open MySQL test database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		t.Fatalf("ping MySQL test database: %v", err)
	}
	if err := ValidateMySQLSession(ctx, db); err != nil {
		t.Fatalf("validate live MySQL session: %v", err)
	}
}

func TestMySQLMigrationRoundTripIntegration(t *testing.T) {
	dsn := os.Getenv("WEKNORA_MYSQL_MIGRATION_TEST_DSN")
	if dsn == "" {
		t.Skip("set WEKNORA_MYSQL_MIGRATION_TEST_DSN to run the destructive migration integration test")
	}
	cfg, err := gomysql.ParseDSN(dsn)
	if err != nil {
		t.Fatalf("parse WEKNORA_MYSQL_MIGRATION_TEST_DSN: %v", err)
	}
	if !strings.HasPrefix(strings.ToLower(cfg.DBName), "weknora_mysql_test_") {
		t.Fatalf(
			"refusing destructive migration test for database %q; name must start with weknora_mysql_test_",
			cfg.DBName,
		)
	}

	migrationDSN := BuildMySQLMigrationDSN(cfg.User, cfg.Passwd, cfg.Addr, cfg.DBName)
	migrationsPath, err := filepath.Abs(filepath.Join("..", "..", "migrations", "mysql"))
	if err != nil {
		t.Fatalf("resolve MySQL migration directory: %v", err)
	}
	sourceURL := "file://" + filepath.ToSlash(migrationsPath)
	m, err := migrate.New(sourceURL, migrationDSN)
	if err != nil {
		t.Fatalf("create MySQL migrator: %v", err)
	}
	closeMigrator := func() {
		sourceErr, databaseErr := m.Close()
		if sourceErr != nil {
			t.Errorf("close migration source: %v", sourceErr)
		}
		if databaseErr != nil {
			t.Errorf("close migration database: %v", databaseErr)
		}
	}

	if err := m.Down(); err != nil &&
		!errors.Is(err, migrate.ErrNoChange) &&
		!errors.Is(err, migrate.ErrNilVersion) {
		closeMigrator()
		t.Fatalf("reset MySQL migration test database: %v", err)
	}
	assertMigrationVersion := func(stage string, want uint) {
		t.Helper()
		version, dirty, err := m.Version()
		if err != nil {
			t.Fatalf("%s version: %v", stage, err)
		}
		if version != want || dirty {
			t.Fatalf("%s version=%d dirty=%v, want version=%d dirty=false", stage, version, dirty, want)
		}
	}

	mysqlHead := 0
	for version := range mysqlMigrationVersions(t) {
		if version > mysqlHead {
			mysqlHead = version
		}
	}
	if err := m.Up(); err != nil {
		closeMigrator()
		t.Fatalf("first MySQL migration up: %v", err)
	}
	assertMigrationVersion("first up", uint(mysqlHead))
	if err := m.Migrate(78); err != nil {
		closeMigrator()
		t.Fatalf("roll MySQL schema back from version 79 to 78: %v", err)
	}
	assertMigrationVersion("rollback from 79 to 78", 78)
	version78DB, err := sql.Open("mysql", BuildMySQLApplicationDSN(
		cfg.User,
		cfg.Passwd,
		cfg.Addr,
		cfg.DBName,
	))
	if err != nil {
		closeMigrator()
		t.Fatalf("open version-78 MySQL database: %v", err)
	}
	if err := assertMySQLSessionDefaults(context.Background(), version78DB); err != nil {
		_ = version78DB.Close()
		closeMigrator()
		t.Fatalf("version-78 session defaults after migration-79 rollback: %v", err)
	}
	if err := version78DB.Close(); err != nil {
		closeMigrator()
		t.Fatalf("close version-78 MySQL database: %v", err)
	}
	if err := m.Up(); err != nil {
		closeMigrator()
		t.Fatalf("restore MySQL migrations after version-79 rollback: %v", err)
	}
	assertMigrationVersion("restore after rollback from 79 to 78", uint(mysqlHead))
	const rollbackSpanID = "mysql-test-long-span"
	longSpanName := strings.Repeat("rollback-span-", 8)
	if _, err := migrationFixtureExec(
		cfg,
		`INSERT INTO knowledge_processing_spans
			(knowledge_id, attempt, span_id, name, kind, status)
		 VALUES (?, 1, ?, ?, 'internal', 'completed')`,
		"mysql-test-rollback-knowledge",
		rollbackSpanID,
		longSpanName,
	); err != nil {
		closeMigrator()
		t.Fatalf("install migration-66 rollback fixture: %v", err)
	}
	if err := m.Migrate(65); err != nil {
		closeMigrator()
		t.Fatalf("roll MySQL schema back to version 65: %v", err)
	}
	assertMigrationVersion("rollback to 65", 65)
	rollbackDB, err := sql.Open("mysql", BuildMySQLApplicationDSN(
		cfg.User,
		cfg.Passwd,
		cfg.Addr,
		cfg.DBName,
	))
	if err != nil {
		closeMigrator()
		t.Fatalf("open version-65 MySQL database: %v", err)
	}
	if err := assertMySQLTenantMemberSoftDeleteUniqueConstraint(
		context.Background(),
		rollbackDB,
	); err != nil {
		_ = rollbackDB.Close()
		closeMigrator()
		t.Fatalf("version-65 tenant membership invariant: %v", err)
	}
	if err := assertMySQLInvitationTokenCaseSensitive(context.Background(), rollbackDB); err != nil {
		_ = rollbackDB.Close()
		closeMigrator()
		t.Fatalf("version-65 invitation token invariant: %v", err)
	}
	if err := assertMySQLIMSessionModeConstraint(context.Background(), rollbackDB); err != nil {
		_ = rollbackDB.Close()
		closeMigrator()
		t.Fatalf("version-65 IM session-mode invariant: %v", err)
	}
	var rolledBackSpanName string
	if err := rollbackDB.QueryRow(
		"SELECT name FROM knowledge_processing_spans WHERE span_id = ?",
		rollbackSpanID,
	).Scan(&rolledBackSpanName); err != nil {
		_ = rollbackDB.Close()
		closeMigrator()
		t.Fatalf("read migration-66 rollback fixture: %v", err)
	}
	if len(rolledBackSpanName) != 64 || rolledBackSpanName != longSpanName[:64] {
		_ = rollbackDB.Close()
		closeMigrator()
		t.Fatalf(
			"migration-66 rollback name=%q (%d bytes), want first 64 bytes",
			rolledBackSpanName,
			len(rolledBackSpanName),
		)
	}
	if err := rollbackDB.Close(); err != nil {
		closeMigrator()
		t.Fatalf("close version-65 MySQL database: %v", err)
	}
	if err := m.Up(); err != nil {
		closeMigrator()
		t.Fatalf("restore MySQL migrations after version-65 rollback: %v", err)
	}
	assertMigrationVersion("restore after rollback to 65", uint(mysqlHead))
	if err := m.Down(); err != nil {
		closeMigrator()
		t.Fatalf("MySQL migration down: %v", err)
	}
	if _, _, err := m.Version(); !errors.Is(err, migrate.ErrNilVersion) {
		closeMigrator()
		t.Fatalf("after down version error=%v, want ErrNilVersion", err)
	}
	if err := m.Up(); err != nil {
		closeMigrator()
		t.Fatalf("second MySQL migration up: %v", err)
	}
	assertMigrationVersion("second up", uint(mysqlHead))
	closeMigrator()

	db, err := sql.Open("mysql", BuildMySQLApplicationDSN(
		cfg.User,
		cfg.Passwd,
		cfg.Addr,
		cfg.DBName,
	))
	if err != nil {
		t.Fatalf("open migrated MySQL database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	if err := assertMySQLSoftDeleteUniqueConstraint(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err := assertMySQLLargeContentColumns(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err := assertMySQLSessionDefaults(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err := assertMySQLOpaqueIdentifierCaseSensitivity(ctx, db); err != nil {
		t.Fatal(err)
	}

	if _, err := db.ExecContext(
		ctx,
		"UPDATE schema_migrations SET version = ?, dirty = TRUE",
		mysqlHead,
	); err != nil {
		t.Fatalf("install dirty migration fixture: %v", err)
	}
	dirtyFixtureInstalled := true
	t.Cleanup(func() {
		if !dirtyFixtureInstalled {
			return
		}
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		if _, cleanupErr := db.ExecContext(
			cleanupCtx,
			"UPDATE schema_migrations SET dirty = FALSE",
		); cleanupErr != nil {
			t.Errorf("restore dirty migration fixture during cleanup: %v", cleanupErr)
		}
	})
	// Production starts from the repository/application root, where the
	// migration source paths used by RunMigrationsWithOptions are resolved.
	t.Chdir(filepath.Join("..", ".."))
	err = RunMigrationsWithOptions(migrationDSN, MigrationOptions{AutoRecoverDirty: true})
	if err == nil || !strings.Contains(err.Error(), "automatic force/retry is disabled") {
		t.Fatalf("dirty MySQL migration error=%v, want fail-closed manual repair guidance", err)
	}
	var version uint
	var dirty bool
	if err := db.QueryRowContext(
		ctx,
		"SELECT version, dirty FROM schema_migrations LIMIT 1",
	).Scan(&version, &dirty); err != nil {
		t.Fatalf("read dirty migration state: %v", err)
	}
	if version != uint(mysqlHead) || !dirty {
		t.Fatalf("dirty state changed to version=%d dirty=%v", version, dirty)
	}
	if _, err := db.ExecContext(
		ctx,
		"UPDATE schema_migrations SET dirty = FALSE",
	); err != nil {
		t.Fatalf("restore migration fixture: %v", err)
	}
	dirtyFixtureInstalled = false
}

func assertMySQLSoftDeleteUniqueConstraint(ctx context.Context, db *sql.DB) error {
	const (
		firstID  = "mysql-test-vector-store-000000000001"
		secondID = "mysql-test-vector-store-000000000002"
		name     = "mysql-test-soft-delete-unique"
		tenantID = 1418
	)
	if _, err := db.ExecContext(
		ctx,
		"DELETE FROM vector_stores WHERE id IN (?, ?)",
		firstID,
		secondID,
	); err != nil {
		return fmt.Errorf("clear vector-store uniqueness fixture: %w", err)
	}
	txn, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin vector-store uniqueness fixture: %w", err)
	}
	defer txn.Rollback()
	insert := `INSERT INTO vector_stores
		(id, name, engine_type, connection_config, index_config, tenant_id)
		VALUES (?, ?, 'mysql', JSON_OBJECT(), JSON_OBJECT(), ?)`
	if _, err := txn.ExecContext(ctx, insert, firstID, name, tenantID); err != nil {
		return fmt.Errorf("insert first active vector store: %w", err)
	}
	if _, err := txn.ExecContext(ctx, insert, secondID, name, tenantID); err == nil {
		return errors.New("duplicate active vector store was accepted")
	}
	if _, err := txn.ExecContext(
		ctx,
		"UPDATE vector_stores SET deleted_at = CURRENT_TIMESTAMP(3) WHERE id = ?",
		firstID,
	); err != nil {
		return fmt.Errorf("soft-delete first vector store: %w", err)
	}
	if _, err := txn.ExecContext(ctx, insert, secondID, name, tenantID); err != nil {
		return fmt.Errorf("reuse vector-store name after soft delete: %w", err)
	}
	return nil
}

func migrationFixtureExec(
	cfg *gomysql.Config,
	query string,
	args ...interface{},
) (sql.Result, error) {
	db, err := sql.Open("mysql", BuildMySQLApplicationDSN(
		cfg.User,
		cfg.Passwd,
		cfg.Addr,
		cfg.DBName,
	))
	if err != nil {
		return nil, err
	}
	defer db.Close()
	return db.Exec(query, args...)
}

func assertMySQLLargeContentColumns(ctx context.Context, db *sql.DB) error {
	const (
		chunkID    = "mysql-test-large-chunk-00000000001"
		revisionID = "mysql-test-large-revision-000000001"
		messageID  = "mysql-test-large-message-00000001"
		tempDocID  = "mysql-test-large-temp-doc-0000001"
		wikiPageID = "mysql-test-large-wiki-page-000001"
		wikiRevID  = "mysql-test-large-wiki-rev-0000001"
	)
	content := strings.Repeat("large-content-payload-", 4000)
	if len(content) <= 65535 {
		return fmt.Errorf("large content fixture is only %d bytes", len(content))
	}
	txn, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin large content fixture: %w", err)
	}
	defer txn.Rollback()
	if _, err := txn.ExecContext(
		ctx,
		`INSERT INTO messages
			(id, request_id, session_id, role, content, rendered_content)
		 VALUES (?, 'mysql-test-large-request', 'mysql-test-large-session', 'assistant', ?, ?)`,
		messageID,
		content,
		content,
	); err != nil {
		return fmt.Errorf("insert >64KiB message content: %w", err)
	}
	if _, err := txn.ExecContext(
		ctx,
		`INSERT INTO chunks
			(id, tenant_id, knowledge_base_id, knowledge_id, content, chunk_index, start_at, end_at, source_content)
			VALUES (?, ?, ?, ?, ?, 0, 0, ?, ?)`,
		chunkID,
		1418004,
		"mysql-test-large-chunk-kb",
		"mysql-test-large-chunk-knowledge",
		content,
		len(content),
		content,
	); err != nil {
		return fmt.Errorf("insert >64KiB application chunk: %w", err)
	}
	if _, err := txn.ExecContext(
		ctx,
		`INSERT INTO chunk_revisions
			(id, tenant_id, knowledge_base_id, knowledge_id, chunk_id, revision, content)
			VALUES (?, ?, ?, ?, ?, 0, ?)`,
		revisionID,
		1418004,
		"mysql-test-large-chunk-kb",
		"mysql-test-large-chunk-knowledge",
		chunkID,
		content,
	); err != nil {
		return fmt.Errorf("insert >64KiB chunk revision: %w", err)
	}
	if _, err := txn.ExecContext(
		ctx,
		`INSERT INTO temporary_documents
			(id, tenant_id, session_id, resource_ref, file_name, file_type, file_size, content, expires_at)
		 VALUES (?, 1418004, 'mysql-test-large-session', 'large-content-ref',
		         'large-content.txt', 'text', ?, ?, CURRENT_TIMESTAMP(3) + INTERVAL 1 DAY)`,
		tempDocID,
		len(content),
		content,
	); err != nil {
		return fmt.Errorf("insert >64KiB temporary document: %w", err)
	}
	if _, err := txn.ExecContext(
		ctx,
		`INSERT INTO wiki_pages
			(id, tenant_id, knowledge_base_id, slug, content)
		 VALUES (?, 1418004, 'mysql-test-large-wiki-kb', 'large-content', ?)`,
		wikiPageID,
		content,
	); err != nil {
		return fmt.Errorf("insert >64KiB wiki page: %w", err)
	}
	if _, err := txn.ExecContext(
		ctx,
		`INSERT INTO wiki_page_revisions
			(id, tenant_id, knowledge_base_id, page_id, slug, version, content)
		 VALUES (?, 1418004, 'mysql-test-large-wiki-kb', ?, 'large-content', 1, ?)`,
		wikiRevID,
		wikiPageID,
		content,
	); err != nil {
		return fmt.Errorf("insert >64KiB wiki page revision: %w", err)
	}

	var messageBytes, renderedBytes, chunkBytes, revisionBytes int
	if err := txn.QueryRowContext(
		ctx,
		`SELECT OCTET_LENGTH(m.content), OCTET_LENGTH(m.rendered_content),
		        OCTET_LENGTH(c.content), OCTET_LENGTH(r.content)
		 FROM messages AS m
		 JOIN chunks AS c ON c.id = ?
		 JOIN chunk_revisions AS r ON r.chunk_id = c.id
		 WHERE m.id = ? AND c.id = ? AND r.id = ?`,
		chunkID,
		messageID,
		chunkID,
		revisionID,
	).Scan(&messageBytes, &renderedBytes, &chunkBytes, &revisionBytes); err != nil {
		return fmt.Errorf("verify >64KiB chunk storage: %w", err)
	}
	var tempDocBytes, wikiPageBytes, wikiRevisionBytes int
	if err := txn.QueryRowContext(
		ctx,
		`SELECT OCTET_LENGTH(d.content), OCTET_LENGTH(p.content), OCTET_LENGTH(r.content)
		 FROM temporary_documents AS d
		 JOIN wiki_pages AS p ON p.id = ?
		 JOIN wiki_page_revisions AS r ON r.page_id = p.id
		 WHERE d.id = ? AND r.id = ?`,
		wikiPageID,
		tempDocID,
		wikiRevID,
	).Scan(&tempDocBytes, &wikiPageBytes, &wikiRevisionBytes); err != nil {
		return fmt.Errorf("verify >64KiB document and wiki storage: %w", err)
	}
	for column, got := range map[string]int{
		"messages.content":            messageBytes,
		"messages.rendered_content":   renderedBytes,
		"chunks.content":              chunkBytes,
		"chunk_revisions.content":     revisionBytes,
		"temporary_documents.content": tempDocBytes,
		"wiki_pages.content":          wikiPageBytes,
		"wiki_page_revisions.content": wikiRevisionBytes,
	} {
		if got != len(content) {
			return fmt.Errorf("%s bytes=%d, want %d", column, got, len(content))
		}
	}
	return nil
}

func assertMySQLSessionDefaults(ctx context.Context, db *sql.DB) error {
	txn, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin session defaults fixture: %w", err)
	}
	defer txn.Rollback()

	const (
		sessionID        = "mysql-session-defaults"
		tenantID         = 1418998
		expectedFallback = "很抱歉，我暂时无法回答这个问题。"
	)
	if _, err := txn.ExecContext(
		ctx,
		`INSERT INTO sessions (id, tenant_id) VALUES (?, ?)`,
		sessionID,
		tenantID,
	); err != nil {
		return fmt.Errorf("insert session using schema defaults: %w", err)
	}

	var fallback string
	var summaryJSON string
	if err := txn.QueryRowContext(
		ctx,
		`SELECT fallback_response, CAST(summary_parameters AS CHAR)
		 FROM sessions WHERE id = ?`,
		sessionID,
	).Scan(&fallback, &summaryJSON); err != nil {
		return fmt.Errorf("read session schema defaults: %w", err)
	}
	if fallback != expectedFallback {
		return fmt.Errorf("fallback_response=%q, want %q", fallback, expectedFallback)
	}
	if summaryJSON != "{}" {
		return fmt.Errorf("summary_parameters=%q, want empty JSON object", summaryJSON)
	}
	return nil
}

func assertMySQLOpaqueIdentifierCaseSensitivity(ctx context.Context, db *sql.DB) error {
	expectedCollations := []struct {
		table     string
		column    string
		collation string
	}{
		{table: "sessions", column: "user_id", collation: "utf8mb4_bin"},
		{table: "mcp_tool_approvals", column: "tool_name", collation: "utf8mb4_bin"},
		{table: "mcp_oauth_tokens", column: "principal_type", collation: "utf8mb4_bin"},
		{table: "mcp_oauth_tokens", column: "principal_id", collation: "utf8mb4_bin"},
		{table: "im_channel_sessions", column: "user_id", collation: "utf8mb4_bin"},
		{table: "im_channel_sessions", column: "chat_id", collation: "utf8mb4_bin"},
		{table: "im_channel_sessions", column: "thread_id", collation: "utf8mb4_bin"},
		{table: "im_channels", column: "bot_identity", collation: "utf8mb4_bin"},
		{table: "embed_channels", column: "publish_token", collation: "ascii_bin"},
		{table: "knowledges", column: "metadata_external_id", collation: "utf8mb4_bin"},
		{table: "resources", column: "handle", collation: "ascii_bin"},
	}
	for _, expected := range expectedCollations {
		var got string
		if err := db.QueryRowContext(
			ctx,
			`SELECT COLLATION_NAME FROM information_schema.columns
			 WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = ? AND COLUMN_NAME = ?`,
			expected.table,
			expected.column,
		).Scan(&got); err != nil {
			return fmt.Errorf("read %s.%s collation: %w", expected.table, expected.column, err)
		}
		if got != expected.collation {
			return fmt.Errorf(
				"%s.%s collation=%q, want %q",
				expected.table,
				expected.column,
				got,
				expected.collation,
			)
		}
	}

	txn, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin case-sensitive identifier fixture: %w", err)
	}
	defer txn.Rollback()

	const tenantID = 1418999
	for _, fixture := range []struct {
		query string
		args  []interface{}
	}{
		{
			query: `INSERT INTO sessions
				(id, tenant_id, fallback_response, summary_parameters, user_id)
			 VALUES (?, ?, '', JSON_OBJECT(), ?)`,
			args: []interface{}{"mysql-case-session-upper", tenantID, "api_external_user:7:Alice"},
		},
		{
			query: `INSERT INTO sessions
				(id, tenant_id, fallback_response, summary_parameters, user_id)
			 VALUES (?, ?, '', JSON_OBJECT(), ?)`,
			args: []interface{}{"mysql-case-session-lower", tenantID, "api_external_user:7:alice"},
		},
		{
			query: `INSERT INTO mcp_tool_approvals
				(id, tenant_id, service_id, tool_name, require_approval)
			 VALUES (?, ?, 'mysql-case-service', ?, ?)`,
			args: []interface{}{"mysql-case-tool-upper", tenantID, "DangerTool", true},
		},
		{
			query: `INSERT INTO mcp_tool_approvals
				(id, tenant_id, service_id, tool_name, require_approval)
			 VALUES (?, ?, 'mysql-case-service', ?, ?)`,
			args: []interface{}{"mysql-case-tool-lower", tenantID, "dangertool", false},
		},
		{
			query: `INSERT INTO mcp_oauth_tokens
				(id, tenant_id, user_id, principal_type, principal_id, service_id)
			 VALUES (?, ?, ?, 'api_external_user', ?, 'mysql-case-service')`,
			args: []interface{}{
				"mysql-case-mcp-upper",
				tenantID,
				"api_external_user:Alice",
				"Alice",
			},
		},
		{
			query: `INSERT INTO mcp_oauth_tokens
				(id, tenant_id, user_id, principal_type, principal_id, service_id)
			 VALUES (?, ?, ?, 'api_external_user', ?, 'mysql-case-service')`,
			args: []interface{}{
				"mysql-case-mcp-lower",
				tenantID,
				"api_external_user:alice",
				"alice",
			},
		},
		{
			query: `INSERT INTO im_channel_sessions
				(id, platform, user_id, chat_id, session_id, tenant_id, agent_id)
			 VALUES (?, 'mattermost', ?, 'mysql-case-chat', ?, ?, 'mysql-case-agent')`,
			args: []interface{}{
				"mysql-case-im-upper",
				"Alice",
				"mysql-case-im-session-upper",
				tenantID,
			},
		},
		{
			query: `INSERT INTO im_channel_sessions
				(id, platform, user_id, chat_id, session_id, tenant_id, agent_id)
			 VALUES (?, 'mattermost', ?, 'mysql-case-chat', ?, ?, 'mysql-case-agent')`,
			args: []interface{}{
				"mysql-case-im-lower",
				"alice",
				"mysql-case-im-session-lower",
				tenantID,
			},
		},
		{
			query: `INSERT INTO im_channels
				(id, tenant_id, agent_id, platform, bot_identity)
			 VALUES (?, ?, 'mysql-case-agent', 'mattermost', ?)`,
			args: []interface{}{"mysql-case-bot-upper", tenantID, "mattermost:wh:TOKEN-AbC"},
		},
		{
			query: `INSERT INTO im_channels
				(id, tenant_id, agent_id, platform, bot_identity)
			 VALUES (?, ?, 'mysql-case-agent', 'mattermost', ?)`,
			args: []interface{}{"mysql-case-bot-lower", tenantID, "mattermost:wh:token-abc"},
		},
		{
			query: `INSERT INTO embed_channels (id, tenant_id, publish_token)
			 VALUES (?, ?, ?)`,
			args: []interface{}{"mysql-case-embed-upper", tenantID, "em_TOKEN-AbC"},
		},
		{
			query: `INSERT INTO embed_channels (id, tenant_id, publish_token)
			 VALUES (?, ?, ?)`,
			args: []interface{}{"mysql-case-embed-lower", tenantID, "em_token-abc"},
		},
		{
			query: `INSERT INTO knowledges
				(id, tenant_id, knowledge_base_id, type, title, source, metadata)
			 VALUES (?, ?, 'mysql-case-kb', 'document', 'upper', 'upper', JSON_OBJECT('external_id', ?))`,
			args: []interface{}{"mysql-case-knowledge-upper", tenantID, "NodeA#child"},
		},
		{
			query: `INSERT INTO knowledges
				(id, tenant_id, knowledge_base_id, type, title, source, metadata)
			 VALUES (?, ?, 'mysql-case-kb', 'document', 'lower', 'lower', JSON_OBJECT('external_id', ?))`,
			args: []interface{}{"mysql-case-knowledge-lower", tenantID, "nodea#child"},
		},
		{
			query: `INSERT INTO resources
				(id, handle, tenant_id, provider, physical_path, location_hash)
			 VALUES (?, ?, ?, 'local', 'mysql-case-upper', 'mysql-case-location-upper')`,
			args: []interface{}{"mysql-case-resource-upper", "AbCdEfGhIjKlMnOpQrStUv", tenantID},
		},
		{
			query: `INSERT INTO resources
				(id, handle, tenant_id, provider, physical_path, location_hash)
			 VALUES (?, ?, ?, 'local', 'mysql-case-lower', 'mysql-case-location-lower')`,
			args: []interface{}{"mysql-case-resource-lower", "abcdefghijklmnopqrstuv", tenantID},
		},
	} {
		if _, err := txn.ExecContext(ctx, fixture.query, fixture.args...); err != nil {
			return fmt.Errorf("insert case-sensitive identifier fixture: %w", err)
		}
	}

	checks := []struct {
		name  string
		query string
		args  []interface{}
	}{
		{
			name:  "session owner",
			query: "SELECT COUNT(*) FROM sessions WHERE tenant_id = ? AND user_id = ?",
			args:  []interface{}{tenantID, "api_external_user:7:alice"},
		},
		{
			name: "MCP tool name",
			query: `SELECT COUNT(*) FROM mcp_tool_approvals
				WHERE tenant_id = ? AND service_id = 'mysql-case-service' AND tool_name = ?`,
			args: []interface{}{tenantID, "dangertool"},
		},
		{
			name: "MCP principal",
			query: `SELECT COUNT(*) FROM mcp_oauth_tokens
				WHERE tenant_id = ? AND principal_type = 'api_external_user'
				  AND principal_id = ? AND service_id = 'mysql-case-service'`,
			args: []interface{}{tenantID, "alice"},
		},
		{
			name: "IM user",
			query: `SELECT COUNT(*) FROM im_channel_sessions
				WHERE tenant_id = ? AND platform = 'mattermost' AND user_id = ?`,
			args: []interface{}{tenantID, "alice"},
		},
		{
			name:  "IM bot identity",
			query: "SELECT COUNT(*) FROM im_channels WHERE tenant_id = ? AND bot_identity = ?",
			args:  []interface{}{tenantID, "mattermost:wh:token-abc"},
		},
		{
			name:  "embed publish token",
			query: "SELECT COUNT(*) FROM embed_channels WHERE tenant_id = ? AND publish_token = ?",
			args:  []interface{}{tenantID, "em_token-abc"},
		},
		{
			name: "external metadata prefix",
			query: `SELECT COUNT(*) FROM knowledges
				WHERE tenant_id = ? AND knowledge_base_id = 'mysql-case-kb'
				  AND metadata_external_id LIKE 'nodea#%'`,
			args: []interface{}{tenantID},
		},
		{
			name:  "resource handle",
			query: "SELECT COUNT(*) FROM resources WHERE tenant_id = ? AND handle = ?",
			args:  []interface{}{tenantID, "abcdefghijklmnopqrstuv"},
		},
	}
	for _, check := range checks {
		var count int
		if err := txn.QueryRowContext(ctx, check.query, check.args...).Scan(&count); err != nil {
			return fmt.Errorf("query %s fixture: %w", check.name, err)
		}
		if count != 1 {
			return fmt.Errorf("%s case-sensitive match count=%d, want 1", check.name, count)
		}
	}
	var upperApproval, lowerApproval bool
	if err := txn.QueryRowContext(
		ctx,
		`SELECT
			(SELECT require_approval FROM mcp_tool_approvals
			 WHERE tenant_id = ? AND service_id = 'mysql-case-service' AND tool_name = 'DangerTool'),
			(SELECT require_approval FROM mcp_tool_approvals
			 WHERE tenant_id = ? AND service_id = 'mysql-case-service' AND tool_name = 'dangertool')`,
		tenantID,
		tenantID,
	).Scan(&upperApproval, &lowerApproval); err != nil {
		return fmt.Errorf("query case-sensitive MCP tool approvals: %w", err)
	}
	if !upperApproval || lowerApproval {
		return fmt.Errorf(
			"case-sensitive MCP approvals upper=%v lower=%v, want true/false",
			upperApproval,
			lowerApproval,
		)
	}
	return nil
}

func assertMySQLTenantMemberSoftDeleteUniqueConstraint(ctx context.Context, db *sql.DB) error {
	const (
		userID   = "mysql-test-member-user-000000001"
		tenantID = 1418001
	)
	txn, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tenant-member uniqueness fixture: %w", err)
	}
	defer txn.Rollback()
	insert := `INSERT INTO tenant_members (user_id, tenant_id) VALUES (?, ?)`
	if _, err := txn.ExecContext(ctx, insert, userID, tenantID); err != nil {
		return fmt.Errorf("insert first active tenant member: %w", err)
	}
	if _, err := txn.ExecContext(
		ctx,
		"UPDATE tenant_members SET deleted_at = CURRENT_TIMESTAMP(3) WHERE user_id = ? AND tenant_id = ?",
		userID,
		tenantID,
	); err != nil {
		return fmt.Errorf("soft-delete first tenant member: %w", err)
	}
	if _, err := txn.ExecContext(ctx, insert, userID, tenantID); err != nil {
		return fmt.Errorf("re-add tenant member after soft delete: %w", err)
	}
	return nil
}

func assertMySQLInvitationTokenCaseSensitive(ctx context.Context, db *sql.DB) error {
	const (
		token          = "AaBbCcDdEeFf00112233445566778899"
		wrongCaseToken = "aAbBcCdDeEfF00112233445566778899"
	)
	txn, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin invitation-token fixture: %w", err)
	}
	defer txn.Rollback()
	if _, err := txn.ExecContext(
		ctx,
		`INSERT INTO tenant_invitations
			(tenant_id, role, token, expires_at)
			VALUES (?, 'viewer', ?, CURRENT_TIMESTAMP(3) + INTERVAL 1 DAY)`,
		1418002,
		token,
	); err != nil {
		return fmt.Errorf("insert mixed-case invitation token: %w", err)
	}
	var matches int
	if err := txn.QueryRowContext(
		ctx,
		"SELECT COUNT(*) FROM tenant_invitations WHERE token = ?",
		wrongCaseToken,
	).Scan(&matches); err != nil {
		return fmt.Errorf("query wrong-case invitation token: %w", err)
	}
	if matches != 0 {
		return fmt.Errorf("wrong-case invitation token matched %d row(s)", matches)
	}
	return nil
}

func assertMySQLIMSessionModeConstraint(ctx context.Context, db *sql.DB) error {
	txn, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin IM session-mode fixture: %w", err)
	}
	defer txn.Rollback()
	_, err = txn.ExecContext(
		ctx,
		`INSERT INTO im_channels
			(id, tenant_id, agent_id, platform, session_mode)
			VALUES ('mysql-test-invalid-session-mode', ?, 'agent', 'test', 'invalid')`,
		1418003,
	)
	if err == nil {
		return errors.New("invalid im_channels.session_mode was accepted")
	}
	return nil
}
