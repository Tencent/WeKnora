//go:build integration

package database

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	_ "github.com/go-sql-driver/mysql"
)

// TestRunMigrationsMySQLIntegration verifies that the application's migration
// runner selects migrations/mysql and reaches the final version on a blank
// MySQL database. It is opt-in because it needs a disposable MySQL server.
func TestRunMigrationsMySQLIntegration(t *testing.T) {
	migrationDSN := os.Getenv("WEKNORA_MYSQL_MIGRATION_TEST_DSN")
	sqlDSN := os.Getenv("WEKNORA_MYSQL_SQL_TEST_DSN")
	if migrationDSN == "" || sqlDSN == "" {
		t.Skip("set WEKNORA_MYSQL_MIGRATION_TEST_DSN and WEKNORA_MYSQL_SQL_TEST_DSN to run MySQL integration test")
	}

	// The application starts from the repository root, where the relative
	// file://migrations/mysql source is resolved. Go package tests start in
	// their package directory, so reproduce the application working directory.
	workingDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("get working directory: %v", err)
	}
	repositoryRoot, err := filepath.Abs(filepath.Join(workingDir, "../.."))
	if err != nil {
		t.Fatalf("resolve repository root: %v", err)
	}
	if err := os.Chdir(repositoryRoot); err != nil {
		t.Fatalf("change to repository root: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(workingDir); err != nil {
			t.Errorf("restore working directory: %v", err)
		}
	})

	if err := RunMigrations(migrationDSN); err != nil {
		t.Fatalf("RunMigrations() error = %v", err)
	}

	db, err := sql.Open("mysql", sqlDSN)
	if err != nil {
		t.Fatalf("open MySQL test database: %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close MySQL test database: %v", err)
		}
	})

	var version uint
	var dirty bool
	if err := db.QueryRow("SELECT version, dirty FROM schema_migrations LIMIT 1").Scan(&version, &dirty); err != nil {
		t.Fatalf("read schema_migrations: %v", err)
	}
	if version != 77 || dirty {
		t.Fatalf("unexpected migration state: version=%d dirty=%t, want version=77 dirty=false", version, dirty)
	}

	var count int
	if err := db.QueryRow(`
		SELECT COUNT(*)
		FROM information_schema.columns
		WHERE table_schema = DATABASE()
		  AND table_name = 'knowledges'
		  AND column_name = 'pending_subtasks_count'`).Scan(&count); err != nil {
		t.Fatalf("check knowledges.pending_subtasks_count: %v", err)
	}
	if count != 1 {
		t.Fatal("knowledges.pending_subtasks_count was not created")
	}
	if err := db.QueryRow(`
		SELECT COUNT(*)
		FROM information_schema.columns
		WHERE table_schema = DATABASE()
		  AND table_name = 'chunks'
		  AND column_name = 'flags'`).Scan(&count); err != nil {
		t.Fatalf("check chunks.flags: %v", err)
	}
	if count != 1 {
		t.Fatal("chunks.flags was not created")
	}
	var extra string
	if err := db.QueryRow(`
		SELECT EXTRA
		FROM information_schema.columns
		WHERE table_schema = DATABASE()
		  AND table_name = 'chunks'
		  AND column_name = 'seq_id'`).Scan(&extra); err != nil {
		t.Fatalf("check chunks.seq_id: %v", err)
	}
	if extra != "auto_increment" {
		t.Fatalf("chunks.seq_id EXTRA = %q, want auto_increment", extra)
	}
	if err := db.QueryRow(`
		SELECT COUNT(*)
		FROM information_schema.tables
		WHERE table_schema = DATABASE() AND table_name = 'user_kb_pins'`).Scan(&count); err != nil {
		t.Fatalf("check user_kb_pins table: %v", err)
	}
	if count != 1 {
		t.Fatal("user_kb_pins was not created")
	}

	const probeUserID = "mysql-migration-user-probe"
	if _, err := db.Exec("DELETE FROM users WHERE id = ?", probeUserID); err != nil {
		t.Fatalf("clear user preference probe: %v", err)
	}
	t.Cleanup(func() {
		if _, err := db.Exec("DELETE FROM users WHERE id = ?", probeUserID); err != nil {
			t.Errorf("clean user preference probe: %v", err)
		}
	})
	if _, err := db.Exec(`
		INSERT INTO users (
			id, username, email, password_hash, avatar, tenant_id, is_active,
			can_access_all_tenants, is_system_admin, created_at, updated_at
		) VALUES (?, 'mysql-migration-probe', 'mysql-migration-probe@example.test',
			'password-hash', '', 1, true, false, false, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`, probeUserID); err != nil {
		t.Fatalf("insert user without preferences: %v", err)
	}
	var preferences string
	if err := db.QueryRow("SELECT preferences FROM users WHERE id = ?", probeUserID).Scan(&preferences); err != nil {
		t.Fatalf("read default user preferences: %v", err)
	}
	if preferences != "{}" {
		t.Fatalf("default user preferences = %q, want {}", preferences)
	}

	const probeSessionID = "mysql-migration-session-probe"
	if _, err := db.Exec("DELETE FROM sessions WHERE id = ?", probeSessionID); err != nil {
		t.Fatalf("clear session default probe: %v", err)
	}
	t.Cleanup(func() {
		if _, err := db.Exec("DELETE FROM sessions WHERE id = ?", probeSessionID); err != nil {
			t.Errorf("clean session default probe: %v", err)
		}
	})
	if _, err := db.Exec(`
		INSERT INTO sessions (
			id, title, description, tenant_id, user_id, is_pinned, pinned_at,
			agent_config, created_at, updated_at
		) VALUES (?, '', '', 1, 'mysql-migration-probe-user', false, NULL,
			NULL, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`, probeSessionID); err != nil {
		t.Fatalf("insert session without fallback fields: %v", err)
	}
	var fallbackResponse, summaryParameters string
	if err := db.QueryRow(
		"SELECT fallback_response, summary_parameters FROM sessions WHERE id = ?", probeSessionID,
	).Scan(&fallbackResponse, &summaryParameters); err != nil {
		t.Fatalf("read session defaults: %v", err)
	}
	if fallbackResponse != "很抱歉，我暂时无法回答这个问题。" || summaryParameters != "{}" {
		t.Fatalf("unexpected session defaults: fallback=%q summary=%q", fallbackResponse, summaryParameters)
	}
}
