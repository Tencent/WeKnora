package database

import (
	"database/sql"
	"errors"
	"os"
	"testing"

	_ "github.com/go-sql-driver/mysql"
	migrate "github.com/golang-migrate/migrate/v4"
)

// TestMySQLMigrationsIntegration executes the complete MySQL migration chain
// against a real server when MYSQL_TEST_MIGRATE_DSN and MYSQL_TEST_SQL_DSN are
// provided. It verifies down/up and the final schema, not just SQL text.
func TestMySQLMigrationsIntegration(t *testing.T) {
	migrateDSN := os.Getenv("MYSQL_TEST_MIGRATE_DSN")
	sqlDSN := os.Getenv("MYSQL_TEST_SQL_DSN")
	if migrateDSN == "" || sqlDSN == "" {
		t.Skip("set MYSQL_TEST_MIGRATE_DSN and MYSQL_TEST_SQL_DSN to run MySQL integration migrations")
	}

	m, err := migrate.New("file://../../migrations/mysql", migrateDSN)
	if err != nil {
		t.Fatalf("create MySQL migrator: %v", err)
	}
	defer m.Close()

	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		t.Fatalf("migrate MySQL up: %v", err)
	}
	assertMySQLMigrationVersion(t, m, 69)

	db, err := sql.Open("mysql", sqlDSN)
	if err != nil {
		t.Fatalf("open MySQL: %v", err)
	}
	defer db.Close()
	if err := db.Ping(); err != nil {
		t.Fatalf("ping MySQL: %v", err)
	}

	for _, table := range []string{
		"tenants", "tenant_api_keys", "message_suggestion_sets", "message_suggestion_events",
		"storage_backends", "resources", "resource_bindings", "resource_access_grants",
	} {
		var count int
		query := "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = DATABASE() AND table_name = ?"
		if err := db.QueryRow(query, table).Scan(&count); err != nil {
			t.Fatalf("inspect table %s: %v", table, err)
		}
		if count != 1 {
			t.Errorf("table %s count = %d, want 1", table, count)
		}
	}

	var dataType string
	var maxLength sql.NullInt64
	query := "SELECT data_type, character_maximum_length FROM information_schema.columns WHERE table_schema = DATABASE() AND table_name = 'knowledge_processing_spans' AND column_name = 'name'"
	if err := db.QueryRow(query).Scan(&dataType, &maxLength); err != nil {
		t.Fatalf("inspect knowledge_processing_spans.name: %v", err)
	}
	if dataType != "varchar" || !maxLength.Valid || maxLength.Int64 != 255 {
		t.Errorf("knowledge_processing_spans.name = %s(%v), want varchar(255)", dataType, maxLength)
	}

	for _, column := range []string{"agent_id", "agent_tenant_id", "model_id", "execution_context"} {
		var count int
		query := "SELECT COUNT(*) FROM information_schema.columns WHERE table_schema = DATABASE() AND table_name = 'messages' AND column_name = ?"
		if err := db.QueryRow(query, column).Scan(&count); err != nil {
			t.Fatalf("inspect messages.%s: %v", column, err)
		}
		if count != 1 {
			t.Errorf("messages.%s missing", column)
		}
	}

	if err := m.Down(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		t.Fatalf("migrate MySQL down: %v", err)
	}
	if _, _, err := m.Version(); !errors.Is(err, migrate.ErrNilVersion) {
		t.Fatalf("version after full down error = %v, want ErrNilVersion", err)
	}
	if err := m.Up(); err != nil {
		t.Fatalf("migrate MySQL up after down: %v", err)
	}
	assertMySQLMigrationVersion(t, m, 69)
}

func assertMySQLMigrationVersion(t *testing.T, m *migrate.Migrate, want uint) {
	t.Helper()
	version, dirty, err := m.Version()
	if err != nil {
		t.Fatalf("read MySQL migration version: %v", err)
	}
	if version != want || dirty {
		t.Fatalf("MySQL migration version = %d dirty=%v, want %d clean", version, dirty, want)
	}
}

func TestMySQLMigrationDSNWithSpecialPassword(t *testing.T) {
	password := os.Getenv("MYSQL_TEST_SPECIAL_PASSWORD")
	if password == "" {
		t.Skip("MYSQL_TEST_SPECIAL_PASSWORD is not set")
	}
	t.Setenv("DB_DRIVER", "mysql")
	t.Setenv("DB_HOST", envOrDefault("MYSQL_TEST_HOST", "127.0.0.1"))
	t.Setenv("DB_PORT", envOrDefault("MYSQL_TEST_PORT", "3306"))
	t.Setenv("DB_USER", envOrDefault("MYSQL_TEST_SPECIAL_USER", "weknora_special"))
	t.Setenv("DB_PASSWORD", password)
	t.Setenv("DB_NAME", envOrDefault("MYSQL_TEST_SPECIAL_DATABASE", "WeKnoraSpecial"))

	dsn, err := migrationDSNFromEnv()
	if err != nil {
		t.Fatalf("build special-password migration DSN: %v", err)
	}
	m, err := migrate.New("file://../../migrations/mysql", dsn)
	if err != nil {
		t.Fatalf("create special-password migrator: %v", err)
	}
	defer m.Close()
	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		t.Fatalf("migrate with special password: %v", err)
	}
	assertMySQLMigrationVersion(t, m, 69)
}

func envOrDefault(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
