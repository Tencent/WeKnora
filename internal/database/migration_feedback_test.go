package database

import (
	"database/sql"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"

	_ "github.com/go-sql-driver/mysql"
	"github.com/golang-migrate/migrate/v4"
	"github.com/google/uuid"
	_ "github.com/lib/pq"
)

func TestChunkFeedbackSQLiteMigrationUpDownUp(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			t.Errorf("close migration database: %v", err)
		}
	}()
	db.SetMaxOpenConns(1)

	_, testFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve migration test path")
	}
	migrationDir := filepath.Join(filepath.Dir(testFile), "..", "..", "migrations", "sqlite")
	execMigrationFile(t, db, migrationDir, "000000_init.up.sql")
	execMigrationFile(t, db, migrationDir, "000001_remove_wiki_log.up.sql")
	execMigrationFile(t, db, migrationDir, "000002_chunk_feedback.up.sql")

	allowed := []string{"like", "dislike", "cancel", "admin_reset", "content_delete", "legacy"}
	for i, triggerSource := range allowed {
		if _, err := db.Exec(`
			INSERT INTO chunk_feedback_audits
				(chunk_tenant_id, chunk_id, actor_tenant_id, actor_user_id, action, trigger_source, old_weight, new_weight)
			VALUES (1, printf('chunk-%d', ?), 1, 'user', 'feedback_weight_changed', ?, 1.0, 1.0)
		`, i, triggerSource); err != nil {
			t.Fatalf("insert trigger source %q: %v", triggerSource, err)
		}
	}
	if _, err := db.Exec(`
		INSERT INTO chunk_feedback_audits
			(chunk_tenant_id, chunk_id, actor_tenant_id, actor_user_id, action, trigger_source, old_weight, new_weight)
		VALUES (1, 'invalid-chunk', 1, 'user', 'feedback_weight_changed', 'invalid', 1.0, 1.0)
	`); err == nil {
		t.Fatal("invalid trigger source unexpectedly passed the migration constraint")
	}

	assertSQLiteColumn(t, db, "chunks", "feedback_reset_at", true)
	assertSQLiteColumn(t, db, "message_feedbacks", "feedback_at", true)
	assertSQLiteColumn(t, db, "message_feedbacks", "feedback_revision", false)
	assertSQLiteFeedbackUniqueConstraints(t, db)
	assertSQLiteFeedbackReasonConstraint(t, db)

	execMigrationFile(t, db, migrationDir, "000002_chunk_feedback.down.sql")
	assertSQLiteColumn(t, db, "chunks", "feedback_reset_at", false)
	assertSQLiteTable(t, db, "message_feedbacks", false)
	assertSQLiteTable(t, db, "message_chunk_references", false)
	assertSQLiteTable(t, db, "chunk_feedback_audits", false)

	execMigrationFile(t, db, migrationDir, "000002_chunk_feedback.up.sql")
	assertSQLiteColumn(t, db, "chunks", "feedback_reset_at", true)
	assertSQLiteTable(t, db, "message_feedbacks", true)

	if body, err := os.ReadFile(filepath.Join(migrationDir, "000002_chunk_feedback.up.sql")); err != nil {
		t.Fatal(err)
	} else if _, err := db.Exec(string(body)); err == nil {
		t.Fatal("reapplying an already-recorded SQLite migration unexpectedly succeeded")
	}
	assertSQLiteTable(t, db, "message_feedbacks", true)
	assertSQLiteTable(t, db, "message_chunk_references", true)
}

func TestChunkFeedbackMigrationVersionsAreUnique(t *testing.T) {
	root := feedbackMigrationRoot(t)
	cases := []struct {
		dir             string
		feedbackVersion int
	}{
		{dir: "versioned", feedbackVersion: 79},
		{dir: "sqlite", feedbackVersion: 2},
	}
	pattern := regexp.MustCompile(`^(\d{6})_.+\.(up|down)\.sql$`)
	for _, tc := range cases {
		t.Run(tc.dir, func(t *testing.T) {
			entries, err := os.ReadDir(filepath.Join(root, tc.dir))
			if err != nil {
				t.Fatal(err)
			}
			seen := make(map[string]string)
			feedbackFound := false
			for _, entry := range entries {
				if entry.IsDir() {
					continue
				}
				match := pattern.FindStringSubmatch(entry.Name())
				if match == nil {
					continue
				}
				key := match[1] + "." + match[2]
				if previous, exists := seen[key]; exists {
					t.Fatalf("migration version collision: %s and %s both claim %s", previous, entry.Name(), key)
				}
				seen[key] = entry.Name()
				if strings.Contains(entry.Name(), "chunk_feedback") {
					version, err := strconv.Atoi(match[1])
					if err != nil {
						t.Fatal(err)
					}
					if version != tc.feedbackVersion {
						t.Fatalf("feedback migration version = %d, want %d", version, tc.feedbackVersion)
					}
					feedbackFound = true
				}
			}
			if !feedbackFound {
				t.Fatal("feedback migration not found")
			}
		})
	}
}

func TestChunkFeedbackPostgresMigrationFreshUpDownUp(t *testing.T) {
	dsn := os.Getenv("WEKNORA_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("WEKNORA_TEST_POSTGRES_DSN is not set")
	}
	schema, scopedDSN, cleanup := createFeedbackPostgresSchema(t, dsn)
	defer cleanup()
	// Native PostgreSQL intentionally does not provide ParadeDB's vector and
	// pg_search extensions. Exercise the complete versioned migration chain
	// with its documented extension-independent mode.
	scopedDSN = postgresDSNWithRuntimeSetting(t, scopedDSN, "app.skip_embedding", "true")

	source := "file://" + filepath.ToSlash(filepath.Join(feedbackMigrationRoot(t), "versioned"))
	migrator, err := migrate.New(source, scopedDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = migrator.Close()
	}()

	if err := migrator.Up(); err != nil {
		t.Fatalf("fresh migration up: %v", err)
	}
	assertPostgresMigrationVersion(t, migrator, 79)

	db := openPostgresTestDB(t, scopedDSN)
	defer closeFeedbackTestDB(t, db)
	assertPostgresColumn(t, db, schema, "message_feedbacks", "feedback_at", true)
	assertPostgresColumn(t, db, schema, "chunks", "feedback_reset_at", true)

	if err := migrator.Steps(-1); err != nil {
		t.Fatalf("feedback migration down: %v", err)
	}
	assertPostgresMigrationVersion(t, migrator, 78)
	assertPostgresColumn(t, db, schema, "chunks", "feedback_reset_at", false)
	assertPostgresTable(t, db, schema, "message_feedbacks", false)

	if err := migrator.Steps(1); err != nil {
		t.Fatalf("feedback migration up again: %v", err)
	}
	assertPostgresMigrationVersion(t, migrator, 79)
	assertPostgresColumn(t, db, schema, "message_feedbacks", "feedback_at", true)
}

func TestChunkFeedbackPostgresDraftSchemaUpgrade(t *testing.T) {
	dsn := os.Getenv("WEKNORA_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("WEKNORA_TEST_POSTGRES_DSN is not set")
	}
	schema, scopedDSN, cleanup := createFeedbackPostgresSchema(t, dsn)
	defer cleanup()
	db := openPostgresTestDB(t, scopedDSN)
	defer closeFeedbackTestDB(t, db)

	_, err := db.Exec(`
		CREATE TABLE chunks (
			id varchar(36) PRIMARY KEY,
			like_count bigint NOT NULL DEFAULT 0,
			dislike_count bigint NOT NULL DEFAULT 0,
			positive_rate double precision,
			recall_weight double precision NOT NULL DEFAULT 1,
			needs_optimization boolean NOT NULL DEFAULT false,
			feedback_reset_at timestamptz,
			feedback_updated_at timestamptz
		);
		CREATE TABLE message_feedbacks (
			id varchar(36) PRIMARY KEY,
			session_tenant_id bigint NOT NULL,
			user_id varchar(512) NOT NULL,
			session_id varchar(36) NOT NULL,
			message_id varchar(36) NOT NULL,
			feedback_type varchar(16) NOT NULL,
			reason_code varchar(64) NOT NULL DEFAULT '',
			reason_text text NOT NULL DEFAULT '',
			feedback_at timestamptz NOT NULL,
			created_at timestamptz NOT NULL,
			updated_at timestamptz NOT NULL
		);
		CREATE UNIQUE INDEX idx_message_feedbacks_user_message
			ON message_feedbacks(session_tenant_id, user_id, message_id);
		CREATE TABLE message_chunk_references (
			id varchar(36) PRIMARY KEY,
			session_tenant_id bigint NOT NULL,
			chunk_tenant_id bigint NOT NULL,
			session_id varchar(36) NOT NULL,
			message_id varchar(36) NOT NULL,
			chunk_id varchar(36) NOT NULL,
			knowledge_base_id varchar(36) NOT NULL,
			knowledge_id varchar(36) NOT NULL,
			reference_rank integer NOT NULL DEFAULT 0,
			retrieval_score double precision NOT NULL DEFAULT 0,
			match_type varchar(64) NOT NULL DEFAULT '',
			created_at timestamptz NOT NULL
		);
		CREATE TABLE chunk_feedback_weight_logs (
			id varchar(36) PRIMARY KEY,
			chunk_tenant_id bigint NOT NULL,
			chunk_id varchar(36) NOT NULL,
			old_weight double precision NOT NULL,
			new_weight double precision NOT NULL,
			source varchar(64) NOT NULL,
			source_action varchar(64) NOT NULL,
			source_message_id varchar(36) NOT NULL DEFAULT '',
			source_feedback_id varchar(36) NOT NULL DEFAULT '',
			actor_tenant_id bigint NOT NULL DEFAULT 0,
			actor_user_id varchar(512) NOT NULL DEFAULT '',
			reason text NOT NULL DEFAULT '',
			created_at timestamptz NOT NULL
		);
		INSERT INTO chunks(id) VALUES ('chunk-legacy');
		INSERT INTO message_feedbacks
			(id, session_tenant_id, user_id, session_id, message_id, feedback_type,
			 reason_code, feedback_at, created_at, updated_at)
		VALUES
			('feedback-like', 11, 'user-like', 'session', 'message-like', 'like',
			 '', '2024-01-02T03:04:05Z', '2024-01-01T00:00:00Z', '2024-01-03T00:00:00Z'),
			('feedback-dislike', 11, 'user-dislike', 'session', 'message-dislike', 'dislike',
			 'draft-only-reason', '2024-02-02T03:04:05Z', '2024-02-01T00:00:00Z', '2024-02-03T00:00:00Z');
		INSERT INTO message_chunk_references
			(id, session_tenant_id, chunk_tenant_id, session_id, message_id, chunk_id,
			 knowledge_base_id, knowledge_id, created_at)
		VALUES
			('reference', 11, 11, 'session', 'message-like', 'chunk-legacy',
			 'kb', 'knowledge', '2024-01-01T00:00:00Z');
		INSERT INTO chunk_feedback_weight_logs
			(id, chunk_tenant_id, chunk_id, old_weight, new_weight, source, source_action,
			 actor_tenant_id, actor_user_id, created_at)
		VALUES
			('log', 11, 'chunk-legacy', 1, 0.8, 'feedback', 'dislike',
			 11, 'admin', '2024-01-04T00:00:00Z');
	`)
	if err != nil {
		t.Fatal(err)
	}
	execMigrationFile(t, db, filepath.Join(feedbackMigrationRoot(t), "versioned"), "000079_chunk_feedback.up.sql")

	assertPostgresColumn(t, db, schema, "chunks", "needs_optimization", false)
	assertPostgresColumn(t, db, schema, "chunks", "feedback_updated_at", false)
	assertPostgresColumn(t, db, schema, "message_feedbacks", "tenant_id", true)
	assertPostgresColumn(t, db, schema, "message_feedbacks", "session_tenant_id", false)
	assertPostgresColumn(t, db, schema, "message_chunk_references", "message_tenant_id", true)
	assertPostgresColumn(t, db, schema, "message_chunk_references", "session_tenant_id", false)

	var likeReason, dislikeReason sql.NullString
	var likeEvent string
	if err := db.QueryRow(`
		SELECT reason_code, feedback_at::text
		FROM message_feedbacks
		WHERE id = 'feedback-like'
	`).Scan(&likeReason, &likeEvent); err != nil {
		t.Fatal(err)
	}
	if likeReason.Valid {
		t.Fatalf("legacy like reason = %q, want NULL", likeReason.String)
	}
	if !strings.HasPrefix(likeEvent, "2024-01-02 03:04:05") {
		t.Fatalf("legacy feedback_at changed: %s", likeEvent)
	}
	if err := db.QueryRow(`
		SELECT reason_code
		FROM message_feedbacks
		WHERE id = 'feedback-dislike'
	`).Scan(&dislikeReason); err != nil {
		t.Fatal(err)
	}
	if !dislikeReason.Valid || dislikeReason.String != "other" {
		t.Fatalf("legacy dislike reason = %#v, want other", dislikeReason)
	}
	var auditCount int
	if err := db.QueryRow("SELECT COUNT(*) FROM chunk_feedback_audits").Scan(&auditCount); err != nil {
		t.Fatal(err)
	}
	if auditCount != 1 {
		t.Fatalf("migrated audit count = %d, want 1", auditCount)
	}
	assertPostgresTable(t, db, schema, "chunk_feedback_weight_logs", false)
}

func TestChunkFeedbackParadeDBFreshSchemaSmoke(t *testing.T) {
	dsn := os.Getenv("WEKNORA_TEST_PARADEDB_DSN")
	if dsn == "" {
		t.Skip("WEKNORA_TEST_PARADEDB_DSN is not set")
	}
	schema, scopedDSN, cleanup := createFeedbackPostgresSchema(t, dsn)
	defer cleanup()
	db := openPostgresTestDB(t, scopedDSN)
	defer closeFeedbackTestDB(t, db)
	execMigrationFile(t, db, filepath.Join(feedbackMigrationRoot(t), "paradedb"), "00-init-db.sql")
	assertPostgresColumn(t, db, schema, "chunks", "feedback_reset_at", true)
	assertPostgresColumn(t, db, schema, "message_feedbacks", "feedback_at", true)
	assertPostgresTable(t, db, schema, "message_chunk_references", true)
	assertPostgresTable(t, db, schema, "chunk_feedback_audits", true)
}

func TestChunkFeedbackMySQLFreshSchemaSmoke(t *testing.T) {
	dsn := os.Getenv("WEKNORA_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("WEKNORA_TEST_MYSQL_DSN is not set")
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer closeFeedbackTestDB(t, db)
	execMigrationFile(t, db, filepath.Join(feedbackMigrationRoot(t), "mysql"), "00-init-db.sql")
	for _, check := range []struct {
		table  string
		column string
	}{
		{table: "chunks", column: "feedback_reset_at"},
		{table: "message_feedbacks", column: "feedback_at"},
		{table: "message_chunk_references", column: "message_tenant_id"},
		{table: "chunk_feedback_audits", column: "trigger_source"},
	} {
		var count int
		if err := db.QueryRow(`
			SELECT COUNT(*)
			FROM information_schema.columns
			WHERE table_schema = DATABASE() AND table_name = ? AND column_name = ?
		`, check.table, check.column).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Fatalf("MySQL column %s.%s count = %d, want 1", check.table, check.column, count)
		}
	}
}

func assertSQLiteFeedbackReasonConstraint(t *testing.T, db *sql.DB) {
	t.Helper()
	for _, valid := range []struct {
		id           string
		feedbackType string
		reason       interface{}
	}{
		{id: "valid-like", feedbackType: "like"},
		{id: "valid-dislike", feedbackType: "dislike", reason: "inaccurate"},
	} {
		if _, err := db.Exec(`
			INSERT INTO message_feedbacks
				(id, tenant_id, user_id, session_id, message_id, feedback_type, reason_code)
			VALUES (?, 1, ?, 'session', ?, ?, ?)
		`, valid.id, valid.id, valid.id, valid.feedbackType, valid.reason); err != nil {
			t.Fatalf("valid feedback contract %q rejected: %v", valid.id, err)
		}
	}
	for _, invalid := range []struct {
		id           string
		feedbackType string
		reason       interface{}
	}{
		{id: "dislike-null", feedbackType: "dislike"},
		{id: "dislike-empty", feedbackType: "dislike", reason: ""},
		{id: "dislike-invalid", feedbackType: "dislike", reason: "invented"},
		{id: "like-reason", feedbackType: "like", reason: "inaccurate"},
	} {
		if _, err := db.Exec(`
			INSERT INTO message_feedbacks
				(id, tenant_id, user_id, session_id, message_id, feedback_type, reason_code)
			VALUES (?, 1, ?, 'session', ?, ?, ?)
		`, invalid.id, invalid.id, invalid.id, invalid.feedbackType, invalid.reason); err == nil {
			t.Fatalf("invalid feedback contract %q unexpectedly passed", invalid.id)
		}
	}
}

func assertSQLiteFeedbackUniqueConstraints(t *testing.T, db *sql.DB) {
	t.Helper()
	if _, err := db.Exec(`
		INSERT INTO message_chunk_references
			(id, message_tenant_id, chunk_tenant_id, message_id, chunk_id)
		VALUES ('ref-1', 1, 2, 'message-1', 'chunk-1')
	`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO message_chunk_references
			(id, message_tenant_id, chunk_tenant_id, message_id, chunk_id)
		VALUES ('ref-2', 1, 2, 'message-1', 'chunk-1')
	`); err == nil {
		t.Fatal("duplicate message/chunk attribution unexpectedly passed")
	}
	if _, err := db.Exec(`
		INSERT INTO message_feedbacks
			(id, tenant_id, user_id, session_id, message_id, feedback_type)
		VALUES ('feedback-1', 1, 'user-1', 'session-1', 'message-1', 'like')
	`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO message_feedbacks
			(id, tenant_id, user_id, session_id, message_id, feedback_type)
		VALUES ('feedback-2', 1, 'user-1', 'session-1', 'message-1', 'dislike')
	`); err == nil {
		t.Fatal("duplicate user/message feedback unexpectedly passed")
	}
}

func assertSQLiteColumn(t *testing.T, db *sql.DB, table, column string, want bool) {
	t.Helper()
	rows, err := db.Query("PRAGMA table_info(" + table + ")")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			t.Errorf("close schema rows: %v", err)
		}
	}()
	found := false
	for rows.Next() {
		var cid int
		var name, columnType string
		var notNull, primaryKey int
		var defaultValue interface{}
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			t.Fatal(err)
		}
		if name == column {
			found = true
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if found != want {
		t.Fatalf("column %s.%s present = %v, want %v", table, column, found, want)
	}
}

func assertSQLiteTable(t *testing.T, db *sql.DB, table string, want bool) {
	t.Helper()
	var count int
	if err := db.QueryRow(
		"SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?", table,
	).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if got := count == 1; got != want {
		t.Fatalf("table %s present = %v, want %v", table, got, want)
	}
}

func execMigrationFile(t *testing.T, db *sql.DB, migrationDir, name string) {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(migrationDir, name))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(string(body)); err != nil {
		t.Fatalf("execute %s: %v", name, err)
	}
}

func feedbackMigrationRoot(t *testing.T) string {
	t.Helper()
	_, testFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve migration test path")
	}
	return filepath.Join(filepath.Dir(testFile), "..", "..", "migrations")
}

func closeFeedbackTestDB(t *testing.T, db *sql.DB) {
	t.Helper()
	if err := db.Close(); err != nil {
		t.Errorf("close feedback test database: %v", err)
	}
}

func createFeedbackPostgresSchema(t *testing.T, dsn string) (string, string, func()) {
	t.Helper()
	schema := "feedback_migration_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	admin := openPostgresTestDB(t, dsn)
	if _, err := admin.Exec(`CREATE SCHEMA "` + schema + `"`); err != nil {
		closeFeedbackTestDB(t, admin)
		t.Fatal(err)
	}
	scopedDSN := postgresDSNWithSearchPath(t, dsn, schema+",public")
	return schema, scopedDSN, func() {
		if _, err := admin.Exec(`DROP SCHEMA IF EXISTS "` + schema + `" CASCADE`); err != nil {
			t.Errorf("drop PostgreSQL test schema: %v", err)
		}
		if err := admin.Close(); err != nil {
			t.Errorf("close PostgreSQL admin connection: %v", err)
		}
	}
}

func postgresDSNWithSearchPath(t *testing.T, dsn, searchPath string) string {
	t.Helper()
	parsed, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Scheme != "postgres" && parsed.Scheme != "postgresql" {
		t.Fatalf("PostgreSQL test DSN must be a URL, got scheme %q", parsed.Scheme)
	}
	query := parsed.Query()
	query.Set("search_path", searchPath)
	parsed.RawQuery = query.Encode()
	return parsed.String()
}

func postgresDSNWithRuntimeSetting(t *testing.T, dsn, name, value string) string {
	t.Helper()
	parsed, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	query.Set("options", "-c "+name+"="+value)
	parsed.RawQuery = query.Encode()
	return parsed.String()
}

func openPostgresTestDB(t *testing.T, dsn string) *sql.DB {
	t.Helper()
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Ping(); err != nil {
		closeFeedbackTestDB(t, db)
		t.Fatal(err)
	}
	return db
}

func assertPostgresMigrationVersion(t *testing.T, migrator *migrate.Migrate, want uint) {
	t.Helper()
	version, dirty, err := migrator.Version()
	if err != nil {
		t.Fatal(err)
	}
	if version != want || dirty {
		t.Fatalf("migration state = version %d dirty %v, want version %d clean", version, dirty, want)
	}
}

func assertPostgresColumn(t *testing.T, db *sql.DB, schema, table, column string, want bool) {
	t.Helper()
	var count int
	if err := db.QueryRow(`
		SELECT COUNT(*)
		FROM information_schema.columns
		WHERE table_schema = $1 AND table_name = $2 AND column_name = $3
	`, schema, table, column).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if got := count == 1; got != want {
		t.Fatalf("PostgreSQL column %s.%s present = %v, want %v", table, column, got, want)
	}
}

func assertPostgresTable(t *testing.T, db *sql.DB, schema, table string, want bool) {
	t.Helper()
	var count int
	if err := db.QueryRow(`
		SELECT COUNT(*)
		FROM information_schema.tables
		WHERE table_schema = $1 AND table_name = $2
	`, schema, table).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if got := count == 1; got != want {
		t.Fatalf("PostgreSQL table %s present = %v, want %v", table, got, want)
	}
}
