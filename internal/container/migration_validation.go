package container

import (
	"fmt"
	"strings"

	"gorm.io/gorm"
)

var criticalMigrationTables = []string{
	"wiki_pages",
	"wiki_log_entries",
	"task_pending_ops",
	"task_dead_letters",
}

func validateCriticalSchemaAfterMigrationFailure(db *gorm.DB, dbDriver string, migrationErr error) error {
	dirty, err := hasDirtyMigrationState(db)
	if err != nil {
		return fmt.Errorf(
			"database migration failed: %w; unable to inspect schema_migrations dirty state: %v",
			migrationErr,
			err,
		)
	}

	missing := missingCriticalMigrationTables(db)
	if !dirty && len(missing) == 0 {
		return nil
	}

	advice := "Run database migrations manually before restarting the application."
	if dirty {
		advice += " If schema_migrations is dirty, inspect the partially applied migration and use make migrate-force version=<last-good-version> only after repairing it."
	}
	if strings.EqualFold(dbDriver, "postgres") {
		advice += " For PostgreSQL/ParadeDB deployments, verify that pg_trgm is available before rerunning migration 000041: CREATE EXTENSION IF NOT EXISTS pg_trgm;"
	}

	reasons := make([]string, 0, 2)
	if dirty {
		reasons = append(reasons, "schema_migrations is dirty")
	}
	if len(missing) > 0 {
		reasons = append(reasons, "missing critical tables after failed migration: "+strings.Join(missing, ", "))
	}

	return fmt.Errorf(
		"database migration failed: %w; %s. "+
			"Refusing to continue application startup because wiki ingest and task processing would break silently. %s",
		migrationErr,
		strings.Join(reasons, "; "),
		advice,
	)
}

func missingCriticalMigrationTables(db *gorm.DB) []string {
	missing := make([]string, 0, len(criticalMigrationTables))
	for _, tableName := range criticalMigrationTables {
		if !db.Migrator().HasTable(tableName) {
			missing = append(missing, tableName)
		}
	}
	return missing
}

func hasDirtyMigrationState(db *gorm.DB) (bool, error) {
	if !db.Migrator().HasTable("schema_migrations") {
		return false, nil
	}

	var dirty bool
	if err := db.Raw("SELECT dirty FROM schema_migrations LIMIT 1").Scan(&dirty).Error; err != nil {
		return false, err
	}
	return dirty, nil
}
