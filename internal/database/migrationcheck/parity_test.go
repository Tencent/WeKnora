package migrationcheck

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckMySQLParityRequiresCurrentHeadBaseline(t *testing.T) {
	root := t.TempDir()
	writeMigration(t, root, "versioned", "000078_current_head.up.sql")
	writeMigration(t, root, "versioned", "000078_current_head.down.sql")

	err := CheckMySQLParity(root)

	if err == nil || !strings.Contains(err.Error(), "MySQL migration baseline for current PostgreSQL head 000078") {
		t.Fatalf("CheckMySQLParity() error = %v", err)
	}
}

func TestCheckMySQLParityAcceptsCurrentHeadBaseline(t *testing.T) {
	root := t.TempDir()
	writeMigration(t, root, "versioned", "000078_current_head.up.sql")
	writeMigration(t, root, "versioned", "000078_current_head.down.sql")
	writeMigration(t, root, "mysql", "000078_mysql_baseline.up.sql")
	writeMigration(t, root, "mysql", "000078_mysql_baseline.down.sql")

	if err := CheckMySQLParity(root); err != nil {
		t.Fatalf("CheckMySQLParity() error = %v", err)
	}
}

func TestCheckMySQLParityRequiresPairedPostBaselineMigrations(t *testing.T) {
	root := t.TempDir()
	writeMigration(t, root, "versioned", "000078_current_head.up.sql")
	writeMigration(t, root, "versioned", "000078_current_head.down.sql")
	writeMigration(t, root, "versioned", "000079_next_change.up.sql")
	writeMigration(t, root, "versioned", "000079_next_change.down.sql")
	writeMigration(t, root, "mysql", "000078_mysql_baseline.up.sql")
	writeMigration(t, root, "mysql", "000078_mysql_baseline.down.sql")
	writeMigration(t, root, "mysql", "000079_next_change.up.sql")

	err := CheckMySQLParity(root)

	if err == nil || !strings.Contains(err.Error(), "missing MySQL migration migrations/mysql/000079_next_change.down.sql") {
		t.Fatalf("CheckMySQLParity() error = %v", err)
	}
}

func TestCheckMySQLParityRejectsNonVersionedMySQLSQL(t *testing.T) {
	root := t.TempDir()
	writeMigration(t, root, "versioned", "000078_current_head.up.sql")
	writeMigration(t, root, "versioned", "000078_current_head.down.sql")
	writeMigration(t, root, "mysql", "000078_mysql_baseline.up.sql")
	writeMigration(t, root, "mysql", "000078_mysql_baseline.down.sql")
	writeMigration(t, root, "mysql", "00-init-db.sql")

	err := CheckMySQLParity(root)

	if err == nil || !strings.Contains(err.Error(), "non-versioned MySQL migration file") {
		t.Fatalf("CheckMySQLParity() error = %v", err)
	}
}

func TestCheckMySQLParityRejectsUnsupportedMySQLIndexSyntax(t *testing.T) {
	root := t.TempDir()
	writeMigration(t, root, "versioned", "000078_current_head.up.sql")
	writeMigration(t, root, "versioned", "000078_current_head.down.sql")
	writeMigration(t, root, "mysql", "000078_mysql_baseline.down.sql")
	writeMigrationContent(t, root, "mysql", "000078_mysql_baseline.up.sql", `
CREATE TABLE t (
    id BIGINT PRIMARY KEY,
    deleted_at DATETIME(6)
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_t_active ON t(id) WHERE deleted_at IS NULL;
`)

	err := CheckMySQLParity(root)

	if err == nil || !strings.Contains(err.Error(), "CREATE INDEX IF NOT EXISTS") {
		t.Fatalf("CheckMySQLParity() error = %v", err)
	}
}

func TestCheckMySQLParityRejectsMySQLBOMAndDatetimePrecisionMismatch(t *testing.T) {
	root := t.TempDir()
	writeMigration(t, root, "versioned", "000078_current_head.up.sql")
	writeMigration(t, root, "versioned", "000078_current_head.down.sql")
	writeMigration(t, root, "mysql", "000078_mysql_baseline.down.sql")
	writeMigrationContent(t, root, "mysql", "000078_mysql_baseline.up.sql", "\uFEFFCREATE TABLE t (created_at DATETIME(6) DEFAULT CURRENT_TIMESTAMP);")

	err := CheckMySQLParity(root)

	if err == nil || !strings.Contains(err.Error(), "UTF-8 BOM") {
		t.Fatalf("CheckMySQLParity() error = %v", err)
	}

	writeMigrationContent(t, root, "mysql", "000078_mysql_baseline.up.sql", "CREATE TABLE t (created_at DATETIME(6) DEFAULT CURRENT_TIMESTAMP);")
	err = CheckMySQLParity(root)
	if err == nil || !strings.Contains(err.Error(), "matching precision") {
		t.Fatalf("CheckMySQLParity() error = %v", err)
	}
}

func writeMigration(t *testing.T, root, dir, name string) {
	t.Helper()
	writeMigrationContent(t, root, dir, name, "-- test\n")
}

func writeMigrationContent(t *testing.T, root, dir, name, content string) {
	t.Helper()
	path := filepath.Join(root, dir)
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
