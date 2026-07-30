package database

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	gomysql "github.com/go-sql-driver/mysql"
)

func TestBuildMySQLApplicationDSNEnforcesSessionContract(t *testing.T) {
	dsn := BuildMySQLApplicationDSN(
		"user:name",
		"p@ss/word:1",
		"mysql:3306",
		"WeKnora",
	)
	cfg, err := gomysql.ParseDSN(dsn)
	if err != nil {
		t.Fatalf("ParseDSN() error = %v", err)
	}
	if !cfg.ParseTime || cfg.Loc.String() != "UTC" {
		t.Fatalf("time parsing config = parseTime:%v loc:%v, want true/UTC", cfg.ParseTime, cfg.Loc)
	}
	if got := cfg.Params["time_zone"]; got != "'"+MySQLSessionTimeZone+"'" {
		t.Fatalf("time_zone = %q", got)
	}
	if got := cfg.Params["sql_mode"]; got != "'"+MySQLSessionSQLMode+"'" {
		t.Fatalf("sql_mode = %q", got)
	}
}

func TestBuildMySQLMigrationDSNEnforcesSessionContract(t *testing.T) {
	dsn := BuildMySQLMigrationDSN(
		"weknora",
		"p@ss word#1",
		"mysql:3306",
		"WeKnora",
	)
	if !strings.HasPrefix(dsn, "mysql://") {
		t.Fatalf("migration DSN = %q", dsn)
	}
	cfg, err := gomysql.ParseDSN(strings.TrimPrefix(dsn, "mysql://"))
	if err != nil {
		t.Fatalf("ParseDSN() error = %v", err)
	}
	if !cfg.MultiStatements || !cfg.ParseTime || cfg.Loc.String() != "UTC" {
		t.Fatalf(
			"migration config = multi:%v parseTime:%v loc:%v",
			cfg.MultiStatements,
			cfg.ParseTime,
			cfg.Loc,
		)
	}
	if got := cfg.Params["time_zone"]; got != "'"+MySQLSessionTimeZone+"'" {
		t.Fatalf("time_zone = %q", got)
	}
	if got := cfg.Params["sql_mode"]; got != "'"+MySQLSessionSQLMode+"'" {
		t.Fatalf("sql_mode = %q", got)
	}
}

func TestValidateMySQLVersion(t *testing.T) {
	tests := []struct {
		name    string
		version string
		comment string
		wantErr bool
	}{
		{name: "minimum", version: "8.0.16", comment: "MySQL Community Server - GPL"},
		{name: "current LTS", version: "8.4.0", comment: "MySQL Community Server - GPL"},
		{name: "checks not enforced", version: "8.0.15", comment: "MySQL Community Server - GPL", wantErr: true},
		{name: "mysql 5.7", version: "5.7.44", comment: "MySQL Community Server - GPL", wantErr: true},
		{name: "mariadb", version: "10.11.6-MariaDB", comment: "mariadb.org binary distribution", wantErr: true},
		{name: "tidb", version: "8.0.11-TiDB-v8.5.0", comment: "TiDB Server", wantErr: true},
		{name: "percona", version: "8.0.36-28", comment: "Percona Server (GPL)"},
		{name: "invalid", version: "unknown", comment: "unknown", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateMySQLVersion(tt.version, tt.comment)
			if (err != nil) != tt.wantErr {
				t.Fatalf("validateMySQLVersion() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestValidateMySQLSession(t *testing.T) {
	tests := []struct {
		name     string
		version  string
		comment  string
		timeZone string
		sqlMode  string
		wantErr  string
	}{
		{
			name:     "valid",
			version:  "8.4.0",
			comment:  "MySQL Community Server - GPL",
			timeZone: MySQLSessionTimeZone,
			sqlMode:  MySQLSessionSQLMode,
		},
		{
			name:     "non UTC session",
			version:  "8.4.0",
			comment:  "MySQL Community Server - GPL",
			timeZone: "SYSTEM",
			sqlMode:  MySQLSessionSQLMode,
			wantErr:  "time_zone",
		},
		{
			name:     "non strict session",
			version:  "8.4.0",
			comment:  "MySQL Community Server - GPL",
			timeZone: MySQLSessionTimeZone,
			sqlMode:  "NO_ENGINE_SUBSTITUTION",
			wantErr:  "strict mode",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sqlDB, mock, err := sqlmock.New()
			if err != nil {
				t.Fatalf("sqlmock.New() error = %v", err)
			}
			defer sqlDB.Close()
			mock.ExpectQuery(regexp.QuoteMeta(
				"SELECT VERSION(), @@version_comment, @@SESSION.time_zone, @@SESSION.sql_mode",
			)).WillReturnRows(sqlmock.NewRows(
				[]string{"version", "version_comment", "time_zone", "sql_mode"},
			).AddRow(tt.version, tt.comment, tt.timeZone, tt.sqlMode))

			err = ValidateMySQLSession(context.Background(), sqlDB)
			if tt.wantErr == "" && err != nil {
				t.Fatalf("ValidateMySQLSession() error = %v", err)
			}
			if tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)) {
				t.Fatalf("ValidateMySQLSession() error = %v, want substring %q", err, tt.wantErr)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatalf("unmet SQL expectations: %v", err)
			}
		})
	}
}

func TestHasStrictMySQLMode(t *testing.T) {
	for _, mode := range []string{
		"STRICT_TRANS_TABLES",
		"ONLY_FULL_GROUP_BY,STRICT_ALL_TABLES",
		"traditional",
	} {
		if !hasStrictMySQLMode(mode) {
			t.Fatalf("hasStrictMySQLMode(%q) = false", mode)
		}
	}
	if hasStrictMySQLMode("ONLY_FULL_GROUP_BY,NO_ENGINE_SUBSTITUTION") {
		t.Fatal("non-strict sql_mode accepted")
	}
}

func TestMainDatabaseDeploymentDefaultsAreSafe(t *testing.T) {
	files := map[string][]string{
		filepath.Join("..", "..", ".env.example"): {
			"AUTO_RECOVER_DIRTY=true",
			"# DB_MAX_OPEN_CONNS=20",
			"# DB_CONN_MAX_IDLE_TIME=5m",
		},
		filepath.Join("..", "..", "docker-compose.yml"): {
			"AUTO_RECOVER_DIRTY=${AUTO_RECOVER_DIRTY:-true}",
			"DB_MAX_OPEN_CONNS=${DB_MAX_OPEN_CONNS:-}",
			"DB_CONN_MAX_IDLE_TIME=${DB_CONN_MAX_IDLE_TIME:-}",
		},
		filepath.Join("..", "..", "docker-compose.mysql.yml"): {
			"--default-time-zone=+00:00",
			"AUTO_RECOVER_DIRTY=false",
			"DB_MAX_OPEN_CONNS=${DB_MAX_OPEN_CONNS:-20}",
			"DB_CONN_MAX_IDLE_TIME=${DB_CONN_MAX_IDLE_TIME:-5m}",
		},
		filepath.Join("..", "..", "helm", "values.yaml"): {
			`AUTO_RECOVER_DIRTY: "true"`,
			`maxOpenConns: "20"`,
			`connMaxIdleTime: "5m"`,
		},
		filepath.Join("..", "..", "helm", "templates", "app.yaml"): {
			"name: DB_MAX_OPEN_CONNS",
			"name: DB_CONN_MAX_IDLE_TIME",
			`ternary "false" .Values.app.env.AUTO_RECOVER_DIRTY`,
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
				t.Errorf("%s missing %q", file, want)
			}
		}
	}
}
