//go:build mysqlintegration

package database

import (
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/Tencent/WeKnora/internal/testutil"
	"github.com/stretchr/testify/require"
)

// These scenarios exercise the current PostgreSQL constraints at the database
// boundary, where racing application-level existence checks cannot enforce them.
func TestMySQLActiveUniqueness(t *testing.T) {
	db := testutil.MySQL(t)
	require.NoError(t, db.Exec("INSERT INTO sessions(id,tenant_id) VALUES ('session',1)").Error)
	require.NoError(t, db.Exec("INSERT INTO organizations(id,name,owner_id) VALUES ('org','Org','owner')").Error)
	require.NoError(t, db.Exec("INSERT INTO knowledge_bases(id,name,tenant_id,embedding_model_id,summary_model_id) VALUES ('kb','KB',1,'','')").Error)
	require.NoError(t, db.Exec("INSERT INTO custom_agents(id,name,tenant_id) VALUES ('agent','Agent',1)").Error)

	t.Run("IM agents in user and thread lookups", func(t *testing.T) {
		for _, thread := range []string{"", "thread"} {
			prefix := "user"
			if thread != "" {
				prefix = "thread"
			}
			insert := "INSERT INTO im_channel_sessions(id,platform,user_id,chat_id,tenant_id,session_id,agent_id,thread_id) VALUES (?,'test',?,?,1,'session',?,?)"
			require.NoError(t, db.Exec(insert, prefix+"-a", prefix, prefix, "a", thread).Error)
			require.NoError(t, db.Exec(insert, prefix+"-b", prefix, prefix, "b", thread).Error)
			require.Error(t, db.Exec(insert, prefix+"-dup", prefix, prefix, "a", thread).Error)
			require.NoError(t, db.Exec("UPDATE im_channel_sessions SET deleted_at=UTC_TIMESTAMP(6) WHERE id=?", prefix+"-a").Error)
			require.NoError(t, db.Exec(insert, prefix+"-new", prefix, prefix, "a", thread).Error)
		}
	})
	for _, scenario := range []struct{ name, table, insert string }{
		{"member rejoin", "tenant_members", "INSERT INTO tenant_members(user_id,tenant_id) VALUES ('member',1)"},
		{"invite code reuse", "organizations", "INSERT INTO organizations(id,name,owner_id,invite_code) VALUES ('%s','Invite','owner','code')"},
		{"KB revoke and reshare", "kb_shares", "INSERT INTO kb_shares(id,knowledge_base_id,organization_id,shared_by_user_id,source_tenant_id) VALUES ('%s','kb','org','owner',1)"},
		{"agent revoke and reshare", "agent_shares", "INSERT INTO agent_shares(id,agent_id,organization_id,shared_by_user_id,source_tenant_id) VALUES ('%s','agent','org','owner',1)"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			insert := func(id string) string {
				if strings.Contains(scenario.insert, "%s") {
					return fmt.Sprintf(scenario.insert, id)
				}
				return scenario.insert
			}
			require.NoError(t, db.Exec(insert("first")).Error)
			require.Error(t, db.Exec(insert("duplicate")).Error)
			where := "id='first'"
			if scenario.table == "tenant_members" {
				where = "user_id='member'"
			}
			require.NoError(t, db.Exec("UPDATE "+scenario.table+" SET deleted_at=UTC_TIMESTAMP(6) WHERE "+where).Error)
			require.NoError(t, db.Exec(insert("replacement")).Error)
		})
	}
	t.Run("racing shares leave one live grant", func(t *testing.T) {
		require.NoError(t, db.Exec("INSERT INTO organizations(id,name,owner_id) VALUES ('racing-org','Race','owner')").Error)
		for _, scenario := range []struct{ table, insert string }{
			{"kb_shares", "INSERT INTO kb_shares(id,knowledge_base_id,organization_id,shared_by_user_id,source_tenant_id) VALUES (?,'kb','racing-org','owner',1)"},
			{"agent_shares", "INSERT INTO agent_shares(id,agent_id,organization_id,shared_by_user_id,source_tenant_id) VALUES (?,'agent','racing-org','owner',1)"},
		} {
			start := make(chan struct{})
			results := make(chan error, 2)
			var ready sync.WaitGroup
			ready.Add(2)
			for i := 0; i < 2; i++ {
				go func(id int) {
					ready.Done()
					<-start
					results <- db.Exec(scenario.insert, fmt.Sprintf("racing-%d", id)).Error
				}(i)
			}
			ready.Wait()
			close(start)
			first, second := <-results, <-results
			require.True(t, (first == nil) != (second == nil), "%s: exactly one competing insert succeeds: %v / %v", scenario.table, first, second)
			var count int64
			require.NoError(t, db.Table(scenario.table).Where("organization_id='racing-org' AND deleted_at IS NULL").Count(&count).Error)
			require.EqualValues(t, 1, count)
			require.NoError(t, db.Exec("UPDATE "+scenario.table+" SET deleted_at=UTC_TIMESTAMP(6) WHERE organization_id='racing-org'").Error)
			require.NoError(t, db.Table(scenario.table).Where("organization_id='racing-org' AND deleted_at IS NULL").Count(&count).Error)
			require.Zero(t, count)
		}
	})
}

func TestMySQLForeignKeyParity(t *testing.T) {
	db := testutil.MySQL(t)
	for _, fk := range []struct{ table, columns, parent, parentColumns, deleteRule string }{
		{"users", "tenant_id", "tenants", "id", "SET NULL"},
		{"auth_tokens", "user_id", "users", "id", "CASCADE"},
		{"organization_tenant_members", "organization_id", "organizations", "id", "CASCADE"},
		{"kb_shares", "knowledge_base_id", "knowledge_bases", "id", "CASCADE"},
		{"kb_shares", "organization_id", "organizations", "id", "CASCADE"},
		{"organization_join_requests", "organization_id", "organizations", "id", "CASCADE"},
		{"agent_shares", "organization_id", "organizations", "id", "CASCADE"},
		{"agent_shares", "agent_id,source_tenant_id", "custom_agents", "id,tenant_id", "CASCADE"},
		{"im_channel_sessions", "session_id", "sessions", "id", "CASCADE"},
		{"sync_logs", "data_source_id", "data_sources", "id", "CASCADE"},
		{"mcp_tool_approvals", "service_id", "mcp_services", "id", "CASCADE"},
		{"mcp_oauth_clients", "service_id", "mcp_services", "id", "CASCADE"},
		{"mcp_oauth_tokens", "service_id", "mcp_services", "id", "CASCADE"},
		{"tenant_api_keys", "tenant_id", "tenants", "id", "CASCADE"},
		{"message_suggestion_sets", "tenant_id", "tenants", "id", "CASCADE"},
		{"message_suggestion_events", "tenant_id", "tenants", "id", "CASCADE"},
		{"message_suggestion_events", "suggestion_set_id", "message_suggestion_sets", "id", "CASCADE"},
		{"resource_bindings", "resource_id", "resources", "id", "CASCADE"},
		{"resource_access_grants", "resource_id", "resources", "id", "CASCADE"},
		{"mcp_metadata", "service_id", "mcp_services", "id", "CASCADE"},
	} {
		t.Run(fk.table+"."+fk.columns, func(t *testing.T) {
			var n int64
			query := `SELECT COUNT(*) FROM (SELECT k.constraint_name, k.referenced_table_name, r.delete_rule,
			GROUP_CONCAT(k.column_name ORDER BY k.ordinal_position) AS child_columns,
			GROUP_CONCAT(k.referenced_column_name ORDER BY k.ordinal_position) AS parent_columns
			FROM information_schema.key_column_usage k JOIN information_schema.referential_constraints r
			ON r.constraint_schema=k.constraint_schema AND r.constraint_name=k.constraint_name
			WHERE k.table_schema=DATABASE() AND k.table_name=? AND k.referenced_table_name IS NOT NULL
			GROUP BY k.constraint_name,k.referenced_table_name,r.delete_rule) f
			WHERE child_columns=? AND referenced_table_name=? AND parent_columns=? AND delete_rule=?`
			require.NoError(t, db.Raw(query, fk.table, fk.columns, fk.parent, fk.parentColumns, fk.deleteRule).Scan(&n).Error)
			require.EqualValues(t, 1, n)
		})
	}
	// An inline REFERENCES clause is silently ignored by MySQL. Verify actual
	// rejection and cascading deletes as well as the catalog entries above.
	require.Error(t, db.Exec("INSERT INTO mcp_metadata(tenant_id,service_id,config_fingerprint,tools,synced_at) VALUES (1,'missing','f',JSON_ARRAY(),UTC_TIMESTAMP(6))").Error)
	require.NoError(t, db.Exec("INSERT INTO mcp_services(id,tenant_id,name,transport_type) VALUES ('svc',1,'Service','stdio')").Error)
	require.NoError(t, db.Exec("INSERT INTO mcp_metadata(tenant_id,service_id,config_fingerprint,tools,synced_at) VALUES (1,'svc','f',JSON_ARRAY(),UTC_TIMESTAMP(6))").Error)
	require.NoError(t, db.Exec("DELETE FROM mcp_services WHERE id='svc'").Error)
	var count int64
	require.NoError(t, db.Table("mcp_metadata").Count(&count).Error)
	require.Zero(t, count)
	require.NoError(t, db.Exec("INSERT INTO resources(id,handle,tenant_id,provider,physical_path,location_hash) VALUES ('resource','handle',1,'local','/test','hash')").Error)
	require.NoError(t, db.Exec("INSERT INTO resource_bindings(id,resource_id,tenant_id,owner_type,owner_id) VALUES ('binding','resource',1,'message','message')").Error)
	require.NoError(t, db.Exec("INSERT INTO resource_access_grants(id,token_hash,resource_id,expires_at) VALUES ('grant','token','resource',UTC_TIMESTAMP(6))").Error)
	require.NoError(t, db.Exec("DELETE FROM resources WHERE id='resource'").Error)
	for _, table := range []string{"resource_bindings", "resource_access_grants"} {
		require.NoError(t, db.Table(table).Count(&count).Error)
		require.Zero(t, count, table)
	}
	require.NoError(t, db.Exec("INSERT INTO tenants(id,name,business) VALUES (888,'Tenant','test')").Error)
	require.NoError(t, db.Exec("INSERT INTO users(id,username,email,password_hash,tenant_id) VALUES ('user','user','user@example.test','hash',888)").Error)
	require.NoError(t, db.Exec("INSERT INTO message_suggestion_sets(id,tenant_id,session_id,assistant_message_id,placement,config_hash,status) VALUES ('set',888,'session','message','follow_up','hash','ready')").Error)
	require.Error(t, db.Exec("INSERT INTO message_suggestion_events(tenant_id,session_id,suggestion_set_id,event_type) VALUES (888,'session','missing','click')").Error)
	require.NoError(t, db.Exec("INSERT INTO message_suggestion_events(tenant_id,session_id,suggestion_set_id,event_type) VALUES (888,'session','set','click')").Error)
	require.NoError(t, db.Exec("DELETE FROM message_suggestion_sets WHERE id='set'").Error)
	require.NoError(t, db.Table("message_suggestion_events").Count(&count).Error)
	require.Zero(t, count)
	require.NoError(t, db.Exec("DELETE FROM tenants WHERE id=888").Error)
	var tenantID *int64
	require.NoError(t, db.Raw("SELECT tenant_id FROM users WHERE id='user'").Scan(&tenantID).Error)
	require.Nil(t, tenantID, "tenant deletion retains the account and clears its tenant")
}

func TestMySQLImageProjectionJSONNull(t *testing.T) {
	db := testutil.MySQL(t)
	const insert = "INSERT INTO chunks(id,tenant_id,knowledge_base_id,knowledge_id,content,chunk_index,start_at,end_at,image_info) VALUES ('chunk',1,'kb','knowledge','',0,0,0,?)"
	image := `[{"url":null,"original_url":null,"caption":null,"ocr_text":null,"attrs":{"attrs":null}},null,"skip",{"url":"null","caption":"null"}]`
	require.NoError(t, db.Exec(insert, image).Error)
	var rows []struct {
		ImageIndex                                   int
		ImageKey, URL, OriginalURL, Caption, OCRText string
		Attrs                                        string
	}
	require.NoError(t, db.Table("chunk_images").Order("image_index").Find(&rows).Error)
	require.Len(t, rows, 2)
	require.Equal(t, "chunk#0", rows[0].ImageKey)
	require.Empty(t, rows[0].URL)
	require.Empty(t, rows[0].OriginalURL)
	require.Empty(t, rows[0].Caption)
	require.Empty(t, rows[0].OCRText)
	require.JSONEq(t, "{}", rows[0].Attrs)
	require.Equal(t, "null", rows[1].URL, "JSON string null is preserved")
	require.Equal(t, "null", rows[1].Caption)
	require.NoError(t, db.Exec("UPDATE chunks SET image_info=? WHERE id='chunk'", `[{"url":null,"original_url":"fallback"}]`).Error)
	rows = nil
	require.NoError(t, db.Table("chunk_images").Find(&rows).Error)
	require.Len(t, rows, 1)
	require.Equal(t, "fallback", rows[0].ImageKey)
	for _, invalid := range []string{"bad json", "{}", "null", "[]"} {
		require.NoError(t, db.Exec("UPDATE chunks SET image_info=? WHERE id='chunk'", invalid).Error)
		var count int64
		require.NoError(t, db.Table("chunk_images").Count(&count).Error)
		require.Zero(t, count, invalid)
	}
}
