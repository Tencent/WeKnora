package database

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestMainDBConnectionPoolConfigFromEnvDefaults(t *testing.T) {
	for _, key := range []string{
		"DB_MAX_OPEN_CONNS",
		"DB_MAX_IDLE_CONNS",
		"DB_CONN_MAX_LIFETIME",
		"DB_CONN_MAX_IDLE_TIME",
	} {
		t.Setenv(key, "")
	}
	cfg, err := MainDBConnectionPoolConfigFromEnv("mysql")
	if err != nil {
		t.Fatalf("MainDBConnectionPoolConfigFromEnv() error = %v", err)
	}
	want := ConnectionPoolConfig{
		MaxOpenConns:    20,
		MaxIdleConns:    10,
		ConnMaxLifetime: 10 * time.Minute,
		ConnMaxIdleTime: 5 * time.Minute,
	}
	if cfg != want {
		t.Fatalf("pool config = %+v, want %+v", cfg, want)
	}

	cfg, err = MainDBConnectionPoolConfigFromEnv("postgres")
	if err != nil {
		t.Fatalf("PostgreSQL MainDBConnectionPoolConfigFromEnv() error = %v", err)
	}
	want = ConnectionPoolConfig{
		MaxOpenConns:    0,
		MaxIdleConns:    10,
		ConnMaxLifetime: 10 * time.Minute,
		ConnMaxIdleTime: 0,
	}
	if cfg != want {
		t.Fatalf("PostgreSQL pool config = %+v, want legacy-compatible %+v", cfg, want)
	}
}

func TestMainDBConnectionPoolConfigFromEnvCustom(t *testing.T) {
	t.Setenv("DB_MAX_OPEN_CONNS", "12")
	t.Setenv("DB_MAX_IDLE_CONNS", "4")
	t.Setenv("DB_CONN_MAX_LIFETIME", "30m")
	t.Setenv("DB_CONN_MAX_IDLE_TIME", "2m")

	cfg, err := MainDBConnectionPoolConfigFromEnv("mysql")
	if err != nil {
		t.Fatalf("MainDBConnectionPoolConfigFromEnv() error = %v", err)
	}
	if cfg.MaxOpenConns != 12 || cfg.MaxIdleConns != 4 ||
		cfg.ConnMaxLifetime != 30*time.Minute || cfg.ConnMaxIdleTime != 2*time.Minute {
		t.Fatalf("pool config = %+v", cfg)
	}
}

func TestMainDBConnectionPoolConfigFromEnvRejectsInvalidValues(t *testing.T) {
	tests := []struct {
		name  string
		key   string
		value string
	}{
		{name: "negative max open", key: "DB_MAX_OPEN_CONNS", value: "-1"},
		{name: "negative idle", key: "DB_MAX_IDLE_CONNS", value: "-1"},
		{name: "invalid lifetime", key: "DB_CONN_MAX_LIFETIME", value: "forever"},
		{name: "negative idle time", key: "DB_CONN_MAX_IDLE_TIME", value: "-1s"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, key := range []string{
				"DB_MAX_OPEN_CONNS",
				"DB_MAX_IDLE_CONNS",
				"DB_CONN_MAX_LIFETIME",
				"DB_CONN_MAX_IDLE_TIME",
			} {
				t.Setenv(key, "")
			}
			t.Setenv(tt.key, tt.value)
			if _, err := MainDBConnectionPoolConfigFromEnv("mysql"); err == nil {
				t.Fatalf("expected %s=%q to fail", tt.key, tt.value)
			}
		})
	}
}

func TestMainDBConnectionPoolConfigRejectsIdleAboveOpen(t *testing.T) {
	t.Setenv("DB_MAX_OPEN_CONNS", "5")
	t.Setenv("DB_MAX_IDLE_CONNS", "6")
	t.Setenv("DB_CONN_MAX_LIFETIME", "")
	t.Setenv("DB_CONN_MAX_IDLE_TIME", "")
	if _, err := MainDBConnectionPoolConfigFromEnv("mysql"); err == nil {
		t.Fatal("idle connections above max open accepted")
	}
}

func TestApplyConnectionPoolConfigBoundsOpenConnections(t *testing.T) {
	sqlDB, _, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New() error = %v", err)
	}
	defer sqlDB.Close()

	ApplyConnectionPoolConfig(sqlDB, ConnectionPoolConfig{
		MaxOpenConns:    7,
		MaxIdleConns:    3,
		ConnMaxLifetime: time.Minute,
		ConnMaxIdleTime: time.Minute,
	})
	if got := sqlDB.Stats().MaxOpenConnections; got != 7 {
		t.Fatalf("MaxOpenConnections = %d, want 7", got)
	}
}

func TestCloseOnStartupErrorClosesDatabase(t *testing.T) {
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New() error = %v", err)
	}
	mock.ExpectClose()
	startupErr := errors.New("migration failed")
	got := CloseOnStartupError(sqlDB, startupErr)
	if !errors.Is(got, startupErr) {
		t.Fatalf("CloseOnStartupError() error = %v, want original error", got)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("database was not closed: %v", err)
	}
}

func TestCloseOnStartupErrorRetainsCloseFailure(t *testing.T) {
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New() error = %v", err)
	}
	closeErr := errors.New("close failed")
	mock.ExpectClose().WillReturnError(closeErr)
	startupErr := errors.New("migration failed")
	got := CloseOnStartupError(sqlDB, startupErr)
	if !errors.Is(got, startupErr) || !errors.Is(got, closeErr) {
		t.Fatalf("CloseOnStartupError() error = %v", got)
	}
	if !strings.Contains(got.Error(), "close database") {
		t.Fatalf("CloseOnStartupError() error = %v", got)
	}
}
