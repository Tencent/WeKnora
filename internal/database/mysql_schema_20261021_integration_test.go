//go:build mysqlintegration

package database

import (
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/testutil"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// This checks the shipped migration, never AutoMigrate: a missing persistent
// model field must fail even when unrelated repository tests do not write it.
func TestMySQLMain20261021ModelColumns(t *testing.T) {
	db := testutil.MySQL(t)
	models := []any{
		&types.Session{}, &types.Message{}, &types.Tenant{}, &types.User{}, &types.AuthToken{},
		&types.Model{}, &types.ModelCatalogConfig{}, &types.KnowledgeBase{}, &types.Knowledge{},
		&types.Chunk{}, &types.ChunkRevision{}, &types.KnowledgeTag{}, &types.KnowledgeTagRelation{},
		&types.CustomAgent{}, &types.MCPService{}, &types.MCPToolApproval{}, &types.MCPOAuthClient{},
		&types.MCPOAuthToken{}, &types.MCPEndpoint{}, &types.MCPMetadata{},
		&types.AgentPrompt{}, &types.AgentRunSuspension{}, &types.AgentGrant{},
		&types.Automation{}, &types.AutomationRun{}, &types.Notification{},
		&types.SessionSandboxAssignment{}, &types.SandboxUserMachine{},
		&types.TenantSandboxConfigEntity{}, &types.TenantUserEnvVar{}, &types.ForkSnapshotLease{},
		&types.TenantSkillEntity{}, &types.TenantSkillSnapshotEntity{}, &types.TenantSkillCatalogEntity{},
		&types.Organization{}, &types.OrganizationTenantMember{}, &types.OrganizationJoinRequest{},
		&types.KnowledgeBaseShare{}, &types.AgentShare{}, &types.TenantDisabledSharedAgent{},
		&types.TenantMember{}, &types.TenantInvitation{}, &types.TenantAPIKey{},
		&types.StoredResource{}, &types.ResourceBinding{}, &types.ResourceAccessGrant{},
		&types.StorageBackend{}, &types.VectorStore{}, &types.DataSource{}, &types.SyncLog{},
		&types.MemorySubject{}, &types.MemoryItem{}, &types.MemoryTopicStat{}, &types.MemoryDocAffinity{},
		&types.MemoryTombstone{}, &types.MemoryItemEmbedding{}, &types.MemoryExtractionSession{},
		&types.WikiPage{}, &types.WikiPageRevision{}, &types.WikiPageIssue{}, &types.WikiFolder{},
		&types.MessageArtifactRecord{}, &types.MessageSuggestionSet{}, &types.MessageSuggestionEvent{},
		&types.TaskDeadLetter{}, &types.TaskPendingOp{}, &types.TemporaryDocument{}, &types.EmbedChannel{},
		&types.KnowledgeProcessingSpan{}, &types.SystemSetting{}, &types.UserResourceFavorite{},
	}
	for _, model := range models {
		stmt := &gorm.Statement{DB: db}
		require.NoError(t, stmt.Parse(model))
		t.Run(stmt.Table, func(t *testing.T) {
			var columns []string
			require.NoError(t, db.Raw("SELECT column_name FROM information_schema.columns WHERE table_schema=DATABASE() AND table_name=?", stmt.Table).Scan(&columns).Error)
			for _, field := range stmt.Schema.Fields {
				if field.DBName != "" && !field.IgnoreMigration {
					assert.Contains(t, columns, field.DBName, "%s.%s", stmt.Table, field.DBName)
				}
			}
		})
	}
}

func TestMySQLMain20261021IndexesAndConstraints(t *testing.T) {
	db := testutil.MySQL(t)
	for table, indexes := range map[string][]string{
		"agent_prompts":               {"idx_agent_prompts_session_status", "idx_agent_prompts_status_lease", "idx_agent_prompts_status_expires", "idx_agent_prompts_message"},
		"agent_run_suspensions":       {"idx_agent_run_suspensions_session"},
		"sessions":                    {"idx_sessions_tenant_kind_user_updated"},
		"resources":                   {"idx_resources_tenant_content_hash"},
		"agent_collaborations":        {"idx_agent_collaborations_status", "idx_agent_collaborations_owner", "idx_agent_collaborations_active_parent"},
		"agent_grants":                {"idx_agent_grants_key", "idx_agent_grants_scope"},
		"automations":                 {"idx_automations_due", "idx_automations_owner", "idx_automations_deleted_at"},
		"automation_runs":             {"idx_automation_runs_slot", "idx_automation_runs_automation", "idx_automation_runs_status"},
		"notifications":               {"idx_notifications_user"},
		"session_sandbox_assignments": {"idx_session_sandbox_assignments_live_machine", "idx_session_sandbox_assignments_cleanup_due"},
		"sandbox_user_machines":       {"idx_sandbox_user_machines_config", "idx_sandbox_user_machines_owner", "idx_sandbox_user_machines_empty"},
	} {
		for _, index := range indexes {
			var n int64
			require.NoError(t, db.Raw("SELECT COUNT(*) FROM information_schema.statistics WHERE table_schema=DATABASE() AND table_name=? AND index_name=?", table, index).Scan(&n).Error)
			assert.Positive(t, n, "%s.%s", table, index)
		}
	}
	var oldAgentColumn int64
	require.NoError(t, db.Raw("SELECT COUNT(*) FROM information_schema.columns WHERE table_schema=DATABASE() AND table_name='automations' AND column_name='agent_id'").Scan(&oldAgentColumn).Error)
	assert.Zero(t, oldAgentColumn, "PG133 automations use the assistant, not agent_id")

	insertCollab := "INSERT INTO agent_collaborations(id,tenant_id,user_id,session_id,status,version,data,updated_at) VALUES (?,1,'user','session',?,0,'{}',UTC_TIMESTAMP(6))"
	require.NoError(t, db.Exec(insertCollab, "active", "active").Error)
	require.Error(t, db.Exec(insertCollab, "duplicate", "cancelling").Error)
	require.NoError(t, db.Exec(insertCollab, "finished", "completed").Error)
	require.NoError(t, db.Exec("UPDATE agent_collaborations SET status='completed' WHERE id='active'").Error)
	require.NoError(t, db.Exec(insertCollab, "replacement", "cancelling").Error)
	grant := "INSERT INTO agent_grants(id,tenant_id,user_id,scope,scope_id,grant_key,created_at,updated_at) VALUES (?,1,'user','session','session',?,UTC_TIMESTAMP(6),UTC_TIMESTAMP(6))"
	require.NoError(t, db.Exec(grant, "grant", "tool:A").Error)
	require.Error(t, db.Exec(grant, "duplicate", "tool:A").Error)
	require.NoError(t, db.Exec(grant, "case-sensitive", "tool:a").Error)
	run := "INSERT INTO automation_runs(id,automation_id,tenant_id,scheduled_at,trigger_kind,status,created_at,updated_at) VALUES (?,'automation',1,?,'schedule','pending',UTC_TIMESTAMP(6),UTC_TIMESTAMP(6))"
	scheduled := time.Date(2026, 10, 21, 1, 2, 3, 123456000, time.UTC)
	require.NoError(t, db.Exec(run, "run", scheduled).Error)
	require.Error(t, db.Exec(run, "duplicate", scheduled).Error)
	require.NoError(t, db.Exec(run, "next-slot", scheduled.Add(time.Microsecond)).Error)
}
