package database

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestSQLiteMigrationsCreateIntentVerdicts 断言 sqlite 迁移创建
// intent_verdicts 表，且列集与设计文档 §6.2 一致（[cli] 验收的 sqlite 同构半）。
func TestSQLiteMigrationsCreateIntentVerdicts(t *testing.T) {
	chdirAndRestore(t, sqliteRepoRoot(t))

	dbPath := filepath.Join(t.TempDir(), "migration.db")
	require.NoError(t, RunMigrationsWithOptions("sqlite3://unused", MigrationOptions{SQLiteDBPath: dbPath}))

	db, err := sql.Open("sqlite3", dbPath)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	wantColumns := []string{
		"id", "tenant_id", "session_id", "assistant_message_id", "tool_call_id",
		"policy_id", "policy_version", "tool_name", "args_digest",
		"layer", "verdict", "reason", "mode_at_decision",
		"latency_ms", "judge_tokens", "judge_model", "human_override", "created_at",
	}
	for _, col := range wantColumns {
		require.True(t, sqliteColumnExists(t, db, "intent_verdicts", col),
			"intent_verdicts 缺列 %s（设计 §6.2）", col)
	}

	// 按 session / policy 查询是验收路径，索引必须存在。
	for _, idx := range []string{"idx_intent_verdicts_session", "idx_intent_verdicts_policy"} {
		require.True(t, sqliteIndexExists(t, db, idx),
			"intent_verdicts 缺索引 %s", idx)
	}
}
