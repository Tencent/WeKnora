package database

import (
	"database/sql"
	"os"
	"sort"
	"strings"
	"testing"

	_ "github.com/go-sql-driver/mysql"
	_ "github.com/jackc/pgx/v5/stdlib"
)

// TestMySQLPostgresLogicalSchemaParity compares the table/column contract of
// fully migrated primary databases. Database-specific generated helper columns
// and PostgreSQL rollback/extension tables are explicitly excluded.
func TestMySQLPostgresLogicalSchemaParity(t *testing.T) {
	mysqlDSN := os.Getenv("MYSQL_TEST_SQL_DSN")
	postgresDSN := os.Getenv("POSTGRES_TEST_SQL_DSN")
	if mysqlDSN == "" || postgresDSN == "" {
		t.Skip("set MYSQL_TEST_SQL_DSN and POSTGRES_TEST_SQL_DSN to compare schemas")
	}

	mysqlDB, err := sql.Open("mysql", mysqlDSN)
	if err != nil {
		t.Fatalf("open MySQL: %v", err)
	}
	defer mysqlDB.Close()
	postgresDB, err := sql.Open("pgx", postgresDSN)
	if err != nil {
		t.Fatalf("open PostgreSQL: %v", err)
	}
	defer postgresDB.Close()

	mysqlTables := readSchemaSet(t, mysqlDB, "SELECT table_name FROM information_schema.tables WHERE table_schema = DATABASE() AND table_type = 'BASE TABLE'")
	postgresTables := readSchemaSet(t, postgresDB, "SELECT table_name FROM information_schema.tables WHERE table_schema = 'public' AND table_type = 'BASE TABLE'")
	for _, name := range []string{"schema_migrations", "embeddings"} {
		delete(mysqlTables, name)
		delete(postgresTables, name)
	}
	for _, name := range []string{"organization_members_pre_plan3", "spatial_ref_sys"} {
		delete(postgresTables, name)
	}
	assertSchemaSetsEqual(t, "tables", postgresTables, mysqlTables)

	mysqlColumns := readSchemaSet(t, mysqlDB, "SELECT CONCAT(c.table_name, '.', c.column_name) FROM information_schema.columns c JOIN information_schema.tables t ON t.table_schema = c.table_schema AND t.table_name = c.table_name WHERE c.table_schema = DATABASE() AND t.table_type = 'BASE TABLE'")
	postgresColumns := readSchemaSet(t, postgresDB, "SELECT c.table_name || '.' || c.column_name FROM information_schema.columns c JOIN information_schema.tables t ON t.table_schema = c.table_schema AND t.table_name = c.table_name WHERE c.table_schema = 'public' AND t.table_type = 'BASE TABLE'")
	for item := range mysqlColumns {
		table, _, _ := strings.Cut(item, ".")
		if table == "schema_migrations" || table == "embeddings" {
			delete(mysqlColumns, item)
		}
	}
	for item := range postgresColumns {
		table, _, _ := strings.Cut(item, ".")
		if table == "schema_migrations" || table == "embeddings" || table == "organization_members_pre_plan3" || table == "spatial_ref_sys" {
			delete(postgresColumns, item)
		}
	}
	for _, item := range []string{
		"resources.active_location_hash",
		"storage_backends.active_legacy_provider",
		"storage_backends.active_name",
	} {
		delete(mysqlColumns, item)
	}
	assertSchemaSetsEqual(t, "columns", postgresColumns, mysqlColumns)

	mysqlNullable := readSchemaAttributes(t, mysqlDB, "SELECT CONCAT(table_name, '.', column_name), is_nullable FROM information_schema.columns WHERE table_schema = DATABASE()")
	postgresNullable := readSchemaAttributes(t, postgresDB, "SELECT table_name || '.' || column_name, is_nullable FROM information_schema.columns WHERE table_schema = 'public'")
	for item := range mysqlNullable {
		table, _, _ := strings.Cut(item, ".")
		if table == "schema_migrations" || table == "embeddings" {
			delete(mysqlNullable, item)
		}
	}
	for item := range postgresNullable {
		table, _, _ := strings.Cut(item, ".")
		if table == "schema_migrations" || table == "embeddings" || table == "organization_members_pre_plan3" || table == "spatial_ref_sys" {
			delete(postgresNullable, item)
		}
	}
	for _, item := range []string{
		"resources.active_location_hash",
		"storage_backends.active_legacy_provider",
		"storage_backends.active_name",
	} {
		delete(mysqlNullable, item)
	}
	assertSchemaAttributesEqual(t, "column nullability", postgresNullable, mysqlNullable)
}

func readSchemaSet(t *testing.T, db *sql.DB, query string) map[string]struct{} {
	t.Helper()
	rows, err := db.Query(query)
	if err != nil {
		t.Fatalf("query schema: %v", err)
	}
	defer rows.Close()
	set := make(map[string]struct{})
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			t.Fatalf("scan schema: %v", err)
		}
		set[value] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate schema: %v", err)
	}
	return set
}

func readSchemaAttributes(t *testing.T, db *sql.DB, query string) map[string]string {
	t.Helper()
	rows, err := db.Query(query)
	if err != nil {
		t.Fatalf("query schema attributes: %v", err)
	}
	defer rows.Close()
	values := make(map[string]string)
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			t.Fatalf("scan schema attributes: %v", err)
		}
		values[key] = strings.ToUpper(value)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate schema attributes: %v", err)
	}
	return values
}

func assertSchemaAttributesEqual(t *testing.T, label string, want, got map[string]string) {
	t.Helper()
	var differences []string
	for key, wantValue := range want {
		gotValue, ok := got[key]
		if !ok {
			continue // table/column parity reports missing keys separately
		}
		if gotValue != wantValue {
			differences = append(differences, key+": postgres="+wantValue+" mysql="+gotValue)
		}
	}
	sort.Strings(differences)
	if len(differences) > 0 {
		t.Fatalf("%s differ: %v", label, differences)
	}
}

func assertSchemaSetsEqual(t *testing.T, label string, want, got map[string]struct{}) {
	t.Helper()
	var missing, extra []string
	for item := range want {
		if _, ok := got[item]; !ok {
			missing = append(missing, item)
		}
	}
	for item := range got {
		if _, ok := want[item]; !ok {
			extra = append(extra, item)
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)
	if len(missing) > 0 || len(extra) > 0 {
		t.Fatalf("%s differ: missing in MySQL=%v extra in MySQL=%v", label, missing, extra)
	}
}
