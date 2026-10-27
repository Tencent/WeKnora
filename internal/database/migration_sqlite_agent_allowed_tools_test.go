package database

import (
	"database/sql"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/Tencent/WeKnora/internal/agent/tools"
	"github.com/stretchr/testify/require"
)

// agentToolListRows is the custom_agents data both upgrade tests start from:
// every way an allowed_tools value can be stored, in both agent modes.
var agentToolListRows = map[string]string{
	"reasoning-empty":   `{"agent_mode":"smart-reasoning","allowed_tools":[]}`,
	"reasoning-null":    `{"agent_mode":"smart-reasoning","allowed_tools":null}`,
	"reasoning-missing": `{"agent_mode":"smart-reasoning"}`,
	"reasoning-picked":  `{"agent_mode":"smart-reasoning","allowed_tools":["thinking"]}`,
	"quick-empty":       `{"agent_mode":"quick-answer","allowed_tools":[]}`,
	// Malformed values must neither break the migration nor be rewritten.
	"reasoning-string": `{"agent_mode":"smart-reasoning","allowed_tools":"thinking"}`,
	"reasoning-object": `{"agent_mode":"smart-reasoning","allowed_tools":{}}`,
}

// requireAgentToolListsUpgraded checks that an upgrade keeps every agent's
// effective tools: the empty smart-reasoning list, which used to select the
// default tools, now names them, and nothing else changes.
func requireAgentToolListsUpgraded(t *testing.T, db *sql.DB, query string) {
	t.Helper()
	for _, id := range []string{"reasoning-string", "reasoning-object"} {
		var raw string
		require.NoError(t, db.QueryRow(query, id).Scan(&raw), id)
		var got, want struct {
			AllowedTools json.RawMessage `json:"allowed_tools"`
		}
		require.NoError(t, json.Unmarshal([]byte(raw), &got), id)
		require.NoError(t, json.Unmarshal([]byte(agentToolListRows[id]), &want), id)
		require.JSONEq(t, string(want.AllowedTools), string(got.AllowedTools), "%s must be left as it is", id)
	}
	// Migration 35 freezes the defaults that existed when it was written.
	// Later defaults must not silently grant tools to explicit lists.
	legacyDefaults := []string{"search_knowledge", "read_document", "list_documents", "search_conversations"}
	want := map[string][]string{
		"reasoning-empty":   legacyDefaults,
		"reasoning-null":    nil,
		"reasoning-missing": nil,
		"reasoning-picked":  {"thinking"},
		"quick-empty":       {},
	}
	for id, expected := range want {
		var raw string
		require.NoError(t, db.QueryRow(query, id).Scan(&raw), id)
		var config struct {
			AllowedTools []string `json:"allowed_tools"`
		}
		require.NoError(t, json.Unmarshal([]byte(raw), &config), id)
		require.Equal(t, expected, config.AllowedTools, id)
		// The default tools still apply to the agents that never set a list.
		before := tools.EffectiveAllowedTools(decodeAllowedTools(t, agentToolListRows[id]))
		if id == "reasoning-empty" {
			before = legacyDefaults
		}
		require.ElementsMatch(t, before, tools.EffectiveAllowedTools(config.AllowedTools),
			"%s must keep its effective tools", id)
	}
}

// decodeAllowedTools reads a pre-upgrade row under the old meaning, in which
// an empty list also selected the default tools.
func decodeAllowedTools(t *testing.T, raw string) []string {
	t.Helper()
	var config struct {
		AgentMode    string   `json:"agent_mode"`
		AllowedTools []string `json:"allowed_tools"`
	}
	require.NoError(t, json.Unmarshal([]byte(raw), &config))
	if config.AgentMode == "smart-reasoning" && config.AllowedTools != nil && len(config.AllowedTools) == 0 {
		return nil
	}
	return config.AllowedTools
}

func TestSQLiteMigrationsKeepAgentToolsWhenAnEmptyListMeansNone(t *testing.T) {
	repoRoot := sqliteRepoRoot(t)
	chdirAndRestore(t, copySQLiteMigrationsThrough(t, repoRoot, 34))
	dbPath := filepath.Join(t.TempDir(), "upgrade-agent-tools.db")
	opts := MigrationOptions{SQLiteDBPath: dbPath}
	require.NoError(t, RunMigrationsWithOptions("sqlite3://unused", opts))
	db := openSQLiteDB(t, dbPath)
	for id, config := range agentToolListRows {
		_, err := db.Exec(`INSERT INTO custom_agents (id, name, tenant_id, config) VALUES (?, ?, 1, ?)`,
			id, id, config)
		require.NoError(t, err)
	}

	chdirAndRestore(t, repoRoot)
	require.NoError(t, RunMigrationsWithOptions("sqlite3://unused", opts))
	requireAgentToolListsUpgraded(t, db, `SELECT config FROM custom_agents WHERE id = ?`)
}
