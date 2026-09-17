package database

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func openDriftTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "drift.db")
	db, err := gorm.Open(sqlite.Open(path), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	return db
}

func TestDetectSchemaDriftReportsMissingCriticalObjects(t *testing.T) {
	db := openDriftTestDB(t)
	// Bare sessions table without sandbox_config_id — mirrors the intranet
	// failure mode after an upgrade with schema_migrations forced ahead.
	require.NoError(t, db.Exec(`CREATE TABLE sessions (id TEXT PRIMARY KEY)`).Error)

	drifts := DetectSchemaDrift(db)
	require.NotEmpty(t, drifts)

	foundSessionCol := false
	foundSandboxTable := false
	for _, drift := range drifts {
		if drift.Table == "sessions" && drift.Column == "sandbox_config_id" {
			foundSessionCol = true
		}
		if drift.Table == "tenant_sandbox_configs" && drift.Column == "" {
			foundSandboxTable = true
		}
	}
	require.True(t, foundSessionCol, "expected sessions.sandbox_config_id drift")
	require.True(t, foundSandboxTable, "expected tenant_sandbox_configs drift")
	require.Equal(t, 77, LowestRepairVersion(drifts))
}

func TestDetectSchemaDriftClearWhenRequirementsPresent(t *testing.T) {
	db := openDriftTestDB(t)
	stmts := []string{
		`CREATE TABLE knowledges (id TEXT, folder_path TEXT, custom_metadata TEXT)`,
		`CREATE TABLE chunks (id TEXT, source_content TEXT)`,
		`CREATE TABLE messages (id TEXT, artifacts TEXT, usage TEXT)`,
		`CREATE TABLE tenant_sandbox_configs (id TEXT)`,
		`CREATE TABLE sessions (id TEXT, sandbox_config_id TEXT)`,
		`CREATE TABLE org_units (id TEXT)`,
		`CREATE TABLE knowledge_bases (id TEXT, org_unit_id TEXT, share_with_descendants INTEGER)`,
		`CREATE TABLE guest_link_channels (id TEXT, session_secret TEXT)`,
		`CREATE TABLE agent_publish_api_keys (id TEXT)`,
	}
	for _, stmt := range stmts {
		require.NoError(t, db.Exec(stmt).Error)
	}
	require.Empty(t, DetectSchemaDrift(db))
	require.Equal(t, -1, LowestRepairVersion(nil))
	require.NoError(t, FormatSchemaDriftError(nil))
}

func TestFormatSchemaDriftErrorIncludesRepairHint(t *testing.T) {
	err := FormatSchemaDriftError([]SchemaDrift{{
		Table: "sessions", Column: "sandbox_config_id",
		SinceMigration: 83, SymptomHint: "session create",
	}})
	require.Error(t, err)
	require.Contains(t, err.Error(), "sessions.sandbox_config_id")
	require.Contains(t, err.Error(), "version=82")
	require.Contains(t, err.Error(), "SCHEMA_DRIFT_FATAL=false")
}
