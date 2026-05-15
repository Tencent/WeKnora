package database

import (
	"context"
	"testing"
)

// TestValidateCriticalSchemaAfterMigrationFailure_CannotConnect verifies
// the function returns nil (safe-mode) when the DB is unreachable.
func TestValidateCriticalSchemaAfterMigrationFailure_CannotConnect(t *testing.T) {
	ctx := context.Background()
	err := ValidateCriticalSchemaAfterMigrationFailure(ctx, "postgres://postgres:invalid@localhost:5432/nonexistent?sslmode=disable")
	if err != nil {
		t.Fatalf("expected nil when DB cannot be reached (safe mode), got: %v", err)
	}
}

// TestValidateCriticalSchemaAfterMigrationFailure_SQLite always returns nil
// because schema validation is only implemented for PostgreSQL.
func TestValidateCriticalSchemaAfterMigrationFailure_SQLite(t *testing.T) {
	ctx := context.Background()
	err := ValidateCriticalSchemaAfterMigrationFailure(ctx, "sqlite3:///tmp/test.db")
	if err != nil {
		t.Fatalf("expected nil for sqlite3 DSN, got: %v", err)
	}
}
