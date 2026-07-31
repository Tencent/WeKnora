package database

import (
	"database/sql"
	"os"
	"path/filepath"
	"runtime"
	"testing"
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
	assertSQLiteColumn(t, db, "message_chunk_references", "chunk_knowledge_base_id", true)
	assertSQLiteTable(t, db, "message_feedbacks", true)
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
			(id, message_tenant_id, chunk_tenant_id, chunk_knowledge_base_id, message_id, chunk_id)
		VALUES ('ref-1', 1, 2, 'kb-1', 'message-1', 'chunk-1')
	`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO message_chunk_references
			(id, message_tenant_id, chunk_tenant_id, chunk_knowledge_base_id, message_id, chunk_id)
		VALUES ('ref-2', 1, 2, 'kb-1', 'message-1', 'chunk-1')
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
