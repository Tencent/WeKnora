package database

import (
	"os"
	"path/filepath"
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

func TestMySQLMigrationsCoverCurrentMainVersion(t *testing.T) {
	const currentMainMigration = 78

	versions := mysqlMigrationVersions(t)
	if len(versions) == 0 {
		t.Fatal("no MySQL migrations found")
	}
	for version := 64; version <= currentMainMigration; version++ {
		if !versions[version] {
			t.Fatalf("missing MySQL migration version %06d", version)
		}
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

func TestMySQLTenantMembersUniqueIndexAllowsSoftDeletedReadd(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", "migrations", "mysql", "000066_expand_knowledge_span_name.up.sql"))
	if err != nil {
		t.Fatalf("read mysql migration 66: %v", err)
	}
	text := string(body)
	for _, want := range []string{
		"active_unique_key TINYINT",
		"CASE WHEN deleted_at IS NULL THEN 1 ELSE NULL END",
		"CREATE UNIQUE INDEX idx_tenant_members_user_tenant_unique",
		"ON tenant_members(user_id, tenant_id, active_unique_key)",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("tenant_members MySQL migration missing %q", want)
		}
	}
}

func TestMySQLDeploymentConfigWiresOfficialArtifacts(t *testing.T) {
	files := map[string][]string{
		filepath.Join("..", "..", "docker-compose.mysql.yml"): {
			"mysql:",
			"mysql-data:",
			"DB_HOST=${DB_HOST:-mysql}",
			"condition: service_healthy",
		},
		filepath.Join("..", "..", "docker", "Dockerfile.app"): {
			"go install -tags 'postgres mysql sqlite3'",
		},
		filepath.Join("..", "..", "helm", "templates", "app.yaml"): {
			"value: {{ .Values.database.driver | quote }}",
			"value: {{ .Values.database.host | quote }}",
			"value: {{ .Values.database.port | quote }}",
		},
		filepath.Join("..", "..", "helm", "values.yaml"): {
			"database:",
			"driver: postgres",
			"host: postgres",
			"port: \"5432\"",
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
