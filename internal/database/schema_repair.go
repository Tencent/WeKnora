package database

import (
	"context"
	"fmt"

	"github.com/Tencent/WeKnora/internal/logger"
	"gorm.io/gorm"
)

// schemaRepair holds a single idempotent DDL statement that ensures a schema
// invariant required by the current code version. Each statement MUST use
// IF NOT EXISTS / IF EXISTS guards so it is safe to execute on every startup,
// even against a database whose versioned migrations are already up-to-date.
type schemaRepair struct {
	// Source tracks which migration introduced the invariant (for diagnostics).
	Source string
	// SQL is the idempotent statement to execute.
	SQL string
}

// criticalRepairs lists DDL statements that the application cannot function
// correctly without, regardless of whether the equivalent versioned migration
// has been applied. When users share databases, restore backups, or hit
// migration tracking corruption, these repairs close the gap so the app
// remains operational.
func criticalRepairs() []schemaRepair {
	return []schemaRepair{
		// 000078: editable chunks & custom document metadata
		{
			Source: "000078",
			SQL: `ALTER TABLE knowledges ADD COLUMN IF NOT EXISTS custom_metadata JSONB NOT NULL DEFAULT '{}'::JSONB`,
		},
		{
			Source: "000078",
			SQL: `ALTER TABLE chunks ADD COLUMN IF NOT EXISTS source_content TEXT NOT NULL DEFAULT ''`,
		},
		{
			Source: "000078",
			SQL: `ALTER TABLE chunks ADD COLUMN IF NOT EXISTS content_revision INT NOT NULL DEFAULT 0`,
		},
		{
			Source: "000078",
			SQL: `ALTER TABLE chunks ADD COLUMN IF NOT EXISTS index_status VARCHAR(16) NOT NULL DEFAULT 'ready'`,
		},
		{
			Source: "000078",
			SQL: `ALTER TABLE chunks ADD COLUMN IF NOT EXISTS last_editor_id VARCHAR(64) NOT NULL DEFAULT ''`,
		},
		{
			Source: "000078",
			SQL: `ALTER TABLE chunks ADD COLUMN IF NOT EXISTS context_header TEXT NOT NULL DEFAULT ''`,
		},
	}
}

// EnsureSchemaRepaired executes idempotent DDL statements that bring the
// database schema into compliance with the current code version, regardless
// of the golang-migrate tracking state. It is designed to run on every
// startup after versioned migrations have been attempted.
//
// Every DDL statement uses IF NOT EXISTS guards so it is a cheap no-op when
// the schema is already correct. This function never fails the startup —
// individual errors are logged and the caller decides whether to treat them
// as fatal.
func EnsureSchemaRepaired(db *gorm.DB) error {
	ctx := context.Background()

	for _, r := range criticalRepairs() {
		if err := db.Exec(r.SQL).Error; err != nil {
			logger.Errorf(ctx, "Schema repair [%s] failed: %v\n  SQL: %s", r.Source, err, r.SQL)
			return fmt.Errorf("schema repair [%s]: %w", r.Source, err)
		}
	}

	logger.Infof(ctx, "Schema repair complete (%d statements checked)", len(criticalRepairs()))
	return nil
}
