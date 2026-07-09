package database

import (
	"strings"
	"testing"
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
	} {
		if !strings.Contains(dsn, want) {
			t.Fatalf("mysql migration dsn missing %q in %s", want, dsn)
		}
	}
}
