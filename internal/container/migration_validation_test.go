package container

import (
	"errors"
	"strings"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestValidateCriticalSchemaAfterMigrationFailureRejectsMissingWikiTaskTables(t *testing.T) {
	db := newMigrationValidationTestDB(t)

	err := validateCriticalSchemaAfterMigrationFailure(db, "postgres", errors.New("migration 41 failed"))
	if err == nil {
		t.Fatal("expected startup validation to fail when migration failed and critical tables are missing")
	}

	msg := err.Error()
	for _, want := range []string{
		"migration 41 failed",
		"task_pending_ops",
		"task_dead_letters",
		"wiki_pages",
		"wiki_log_entries",
		"pg_trgm",
	} {
		if !strings.Contains(msg, want) {
			t.Fatalf("expected error to mention %q, got %q", want, msg)
		}
	}
}

func TestValidateCriticalSchemaAfterMigrationFailureAllowsExternallyMigratedSchema(t *testing.T) {
	db := newMigrationValidationTestDB(t)
	for _, table := range criticalMigrationTables {
		if err := db.Table(table).AutoMigrate(&migrationValidationTestRow{}); err != nil {
			t.Fatalf("create table %s: %v", table, err)
		}
	}

	err := validateCriticalSchemaAfterMigrationFailure(db, "postgres", errors.New("migration 41 failed"))
	if err != nil {
		t.Fatalf("expected complete externally managed schema to keep startup allowed, got %v", err)
	}
}

func TestValidateCriticalSchemaAfterMigrationFailureRejectsDirtyMigrationState(t *testing.T) {
	db := newMigrationValidationTestDB(t)
	for _, table := range criticalMigrationTables {
		if err := db.Table(table).AutoMigrate(&migrationValidationTestRow{}); err != nil {
			t.Fatalf("create table %s: %v", table, err)
		}
	}
	if err := db.Exec("CREATE TABLE schema_migrations (version INTEGER, dirty BOOLEAN)").Error; err != nil {
		t.Fatalf("create schema_migrations: %v", err)
	}
	if err := db.Exec("INSERT INTO schema_migrations (version, dirty) VALUES (?, ?)", 41, true).Error; err != nil {
		t.Fatalf("insert dirty migration state: %v", err)
	}

	err := validateCriticalSchemaAfterMigrationFailure(db, "postgres", errors.New("migration 41 failed"))
	if err == nil {
		t.Fatal("expected dirty migration state to stop startup even when critical tables exist")
	}

	msg := err.Error()
	for _, want := range []string{"migration 41 failed", "schema_migrations", "dirty", "migrate-force"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("expected error to mention %q, got %q", want, msg)
		}
	}
}

func newMigrationValidationTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite db: %v", err)
	}
	return db
}

type migrationValidationTestRow struct {
	ID uint
}
