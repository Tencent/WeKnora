package database

import (
	"strings"
	"testing"
	"time"

	mysqlDriver "github.com/go-sql-driver/mysql"
)

func TestLoadMySQLSettingsFromEnv(t *testing.T) {
	t.Setenv("DB_HOST", "mysql.internal")
	t.Setenv("DB_PORT", "3307")
	t.Setenv("DB_USER", "app-user")
	t.Setenv("DB_PASSWORD", "p%40ss:@/word")
	t.Setenv("DB_NAME", "WeKnora test")
	t.Setenv("DB_MAX_OPEN_CONNS", "23")
	t.Setenv("DB_MAX_IDLE_CONNS", "7")
	t.Setenv("DB_CONN_MAX_LIFETIME", "17m")
	t.Setenv("DB_CONN_MAX_IDLE_TIME", "3m")

	settings, err := LoadMySQLSettingsFromEnv()
	if err != nil {
		t.Fatalf("LoadMySQLSettingsFromEnv() error = %v", err)
	}

	cfg, err := mysqlDriver.ParseDSN(settings.DSN)
	if err != nil {
		t.Fatalf("ParseDSN(settings.DSN) error = %v", err)
	}
	if cfg.Addr != "mysql.internal:3307" || cfg.User != "app-user" ||
		cfg.Passwd != "p%40ss:@/word" || cfg.DBName != "WeKnora test" {
		t.Fatalf("unexpected parsed DSN: %#v", cfg)
	}
	if !cfg.ParseTime || cfg.Loc != time.UTC || cfg.MultiStatements {
		t.Fatalf("unexpected application DSN options: %#v", cfg)
	}
	if !strings.Contains(settings.DSN, "charset=utf8mb4") || cfg.Params["time_zone"] != "'+00:00'" {
		t.Fatalf("unexpected DSN params: %#v", cfg.Params)
	}

	migrationCfg, err := mysqlDriver.ParseDSN(settings.MigrationDSN)
	if err != nil {
		t.Fatalf("ParseDSN(settings.MigrationDSN) error = %v", err)
	}
	if !migrationCfg.MultiStatements {
		t.Fatal("migration DSN must enable multiStatements")
	}
	if settings.MaxOpenConns != 23 || settings.MaxIdleConns != 7 ||
		settings.ConnMaxLifetime != 17*time.Minute ||
		settings.ConnMaxIdleTime != 3*time.Minute {
		t.Fatalf("unexpected pool settings: %#v", settings)
	}
}

func TestLoadMySQLSettingsFromEnvRequiresDatabaseName(t *testing.T) {
	t.Setenv("DB_USER", "weknora")
	t.Setenv("DB_NAME", " ")
	if _, err := LoadMySQLSettingsFromEnv(); err == nil {
		t.Fatal("expected missing DB_NAME error")
	}
}

func TestLoadMySQLSettingsFromEnvRequiresUser(t *testing.T) {
	t.Setenv("DB_USER", " ")
	t.Setenv("DB_NAME", "WeKnora")
	if _, err := LoadMySQLSettingsFromEnv(); err == nil {
		t.Fatal("expected missing DB_USER error")
	}
}

func TestLoadMySQLSettingsFromEnvRejectsIncompleteClientCertificate(t *testing.T) {
	t.Setenv("DB_USER", "weknora")
	t.Setenv("DB_NAME", "WeKnora")
	t.Setenv("DB_USE_TLS", "true")
	t.Setenv("DB_TLS_CERT", "client.pem")
	t.Setenv("DB_TLS_KEY", "")

	if _, err := LoadMySQLSettingsFromEnv(); err == nil {
		t.Fatal("expected incomplete client certificate error")
	}
}

func TestEnvParsersUseSafeFallbacks(t *testing.T) {
	t.Setenv("BAD_DURATION", "not-a-duration")
	t.Setenv("NEGATIVE_DURATION", "-1s")
	t.Setenv("BAD_INT", "-2")
	t.Setenv("BAD_BOOL", "perhaps")

	if got := envDuration("BAD_DURATION", time.Second); got != time.Second {
		t.Fatalf("envDuration invalid = %v", got)
	}
	if got := envDuration("NEGATIVE_DURATION", time.Second); got != time.Second {
		t.Fatalf("envDuration negative = %v", got)
	}
	if got := envNonNegativeInt("BAD_INT", 11); got != 11 {
		t.Fatalf("envNonNegativeInt negative = %d", got)
	}
	if got := envBool("BAD_BOOL", true); !got {
		t.Fatal("envBool invalid should return fallback")
	}
}
