package database

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
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

var versionedMigrationName = regexp.MustCompile("^(\\d{6})_.+\\.(up|down)\\.sql$")

func migrationVersions(t *testing.T, dir string) map[int]map[string]bool {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read migrations %s: %v", dir, err)
	}
	versions := make(map[int]map[string]bool)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		match := versionedMigrationName.FindStringSubmatch(entry.Name())
		if match == nil {
			continue
		}
		version, err := strconv.Atoi(match[1])
		if err != nil {
			t.Fatalf("parse migration version %s: %v", entry.Name(), err)
		}
		if versions[version] == nil {
			versions[version] = make(map[string]bool)
		}
		versions[version][match[2]] = true
	}
	return versions
}

func maxMigrationVersion(versions map[int]map[string]bool) int {
	maxVersion := -1
	for version := range versions {
		if version > maxVersion {
			maxVersion = version
		}
	}
	return maxVersion
}

func TestMySQLMigrationsTrackCurrentPostgresVersion(t *testing.T) {
	repoRoot := filepath.Join("..", "..")
	postgresVersions := migrationVersions(t, filepath.Join(repoRoot, "migrations", "versioned"))
	mysqlVersions := migrationVersions(t, filepath.Join(repoRoot, "migrations", "mysql"))

	postgresMax := maxMigrationVersion(postgresVersions)
	mysqlMax := maxMigrationVersion(mysqlVersions)
	if mysqlMax != postgresMax {
		t.Fatalf("MySQL migration max version = %d, PostgreSQL = %d", mysqlMax, postgresMax)
	}

	for version, directions := range mysqlVersions {
		if !directions["up"] || !directions["down"] {
			t.Errorf("MySQL migration %06d must have paired up/down files: %#v", version, directions)
		}
	}
	for version := 66; version <= postgresMax; version++ {
		if _, ok := mysqlVersions[version]; !ok {
			t.Errorf("MySQL migration %06d is missing", version)
		}
	}
	if _, err := os.Stat(filepath.Join(repoRoot, "migrations", "mysql", "00-init-db.sql")); !os.IsNotExist(err) {
		t.Fatalf("legacy 00-init-db.sql must not coexist with versioned MySQL migrations")
	}
}

func TestMigrationDSNFromEnvMySQLValidationAndTLS(t *testing.T) {
	t.Setenv("DB_DRIVER", "mysql")
	t.Setenv("DB_HOST", "[2001:db8::1]")
	t.Setenv("DB_PORT", "3307")
	t.Setenv("DB_USER", "weknora")
	t.Setenv("DB_PASSWORD", "secret")
	t.Setenv("DB_NAME", "WeKnora")
	t.Setenv("DB_TLS_MODE", "preferred")
	dsn, err := migrationDSNFromEnv()
	if err != nil {
		t.Fatalf("migrationDSNFromEnv() error = %v", err)
	}
	for _, want := range []string{"tcp([2001:db8::1]:3307)", "tls=preferred"} {
		if !strings.Contains(dsn, want) {
			t.Fatalf("DSN %q missing %q", dsn, want)
		}
	}

	t.Setenv("DB_USER", "")
	if _, err := migrationDSNFromEnv(); err == nil {
		t.Fatal("missing DB_USER should fail")
	}
}
