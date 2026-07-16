package database

import (
	"errors"
	"os"
	"testing"

	migrate "github.com/golang-migrate/migrate/v4"
)

func TestPostgresMigrationsIntegration(t *testing.T) {
	dsn := os.Getenv("POSTGRES_TEST_MIGRATE_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_TEST_MIGRATE_DSN is not set")
	}
	m, err := migrate.New("file://../../migrations/versioned", dsn)
	if err != nil {
		t.Fatalf("create PostgreSQL migrator: %v", err)
	}
	defer m.Close()
	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		t.Fatalf("migrate PostgreSQL up: %v", err)
	}
	version, dirty, err := m.Version()
	if err != nil {
		t.Fatalf("read PostgreSQL migration version: %v", err)
	}
	if version != 69 || dirty {
		t.Fatalf("PostgreSQL migration version = %d dirty=%v, want 69 clean", version, dirty)
	}
}
