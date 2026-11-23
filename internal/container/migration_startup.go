package container

import (
	"context"
	"fmt"
	"os"

	"github.com/Tencent/WeKnora/internal/logger"
)

// runStartupMigrations aborts startup on migration failure unless explicitly
// configured to preserve the previous warn-and-continue behavior (issue #5481).
func runStartupMigrations(migrate func() error) error {
	if err := migrate(); err != nil {
		if migrationFailOpen() {
			logger.Warnf(context.Background(), "Database migration failed: %v", err)
			logger.Warnf(
				context.Background(),
				"AUTO_MIGRATE_FAIL_OPEN=true: continuing with application startup. "+
					"Please run migrations manually if needed.",
			)
			return nil
		}
		return fmt.Errorf(
			"database migration failed: %w "+
				"(set AUTO_MIGRATE=false if the schema is managed externally, or "+
				"AUTO_MIGRATE_FAIL_OPEN=true to keep starting on migration errors)",
			err,
		)
	}
	return nil
}

// migrationFailOpen reports whether a failed startup migration should keep
// the process starting with a warning instead of aborting startup. The
// default is fail-fast: with AUTO_MIGRATE enabled, a failed migration leaves
// required tables missing and the app only looks healthy while every query
// fails (issue #5481). Set AUTO_MIGRATE_FAIL_OPEN=true to restore the
// previous warn-and-continue behavior.
func migrationFailOpen() bool {
	return os.Getenv("AUTO_MIGRATE_FAIL_OPEN") == "true"
}
