package database

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestSQLiteMigrationsCreateIntentPolicies 断言 sqlite 迁移创建
// intent_policies 表，且列集与设计文档 §6.1 一致（[cli] 验收的 sqlite 同构半）。
func TestSQLiteMigrationsCreateIntentPolicies(t *testing.T) {
	chdirAndRestore(t, sqliteRepoRoot(t))

	dbPath := filepath.Join(t.TempDir(), "migration.db")
	require.NoError(t, RunMigrationsWithOptions("sqlite3://unused", MigrationOptions{SQLiteDBPath: dbPath}))

	db, err := sql.Open("sqlite3", dbPath)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	wantColumns := []string{
		"id", "tenant_id", "scope_type", "scope_ref", "arg_path",
		"constraint_text", "rule_expr", "risk_tier", "mode",
		"version", "enabled", "created_by", "created_at", "updated_at",
	}
	for _, col := range wantColumns {
		require.True(t, sqliteColumnExists(t, db, "intent_policies", col),
			"intent_policies 缺列 %s（设计 §6.1）", col)
	}

	// scope 解析按 (tenant_id, scope_type, scope_ref) 取数，索引必须存在。
	require.True(t, sqliteIndexExists(t, db, "idx_intent_policies_scope"),
		"intent_policies 缺索引 idx_intent_policies_scope")
	// 谱系版本唯一性（并发防护，见 versioned 000114）。
	require.True(t, sqliteIndexExists(t, db, "idx_intent_policies_lineage_version"),
		"intent_policies 缺唯一索引 idx_intent_policies_lineage_version")
}
