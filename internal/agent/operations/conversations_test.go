package operations

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func projectJSON(t *testing.T, op Operation, body string) string {
	t.Helper()
	projected, err := op.Project([]byte(body))
	require.NoError(t, err)
	out, err := json.Marshal(projected)
	require.NoError(t, err)
	return string(out)
}

func TestBuiltinConversationOperationsAreReadOnly(t *testing.T) {
	for _, op := range Builtin().All() {
		if op.Domain() != "session" && op.Domain() != "message" {
			continue
		}
		assert.False(t, op.Risk.Mutates(), op.ID)
		assert.Equal(t, types.TenantRoleViewer, op.Role, op.ID)
	}
}

func TestBuiltinSessionList(t *testing.T) {
	op, ok := Builtin().Get("session.list")
	require.True(t, ok)
	assert.Equal(t, RiskRead, op.Risk)
	assert.Equal(t, "/api/v1/sessions", op.Route)
	assert.ElementsMatch(t, []string{"page", "page_size", "kind", "keyword"}, paramNames(op))
	size, _ := op.Param("page_size")
	assert.Equal(t, 20, size.Maximum)

	// source and agent_id switch an admin to every member's channel
	// conversations, so the model cannot set them.
	for _, hidden := range []string{"source", "agent_id"} {
		_, err := op.Build(map[string]json.RawMessage{hidden: json.RawMessage(`"feishu"`)})
		require.Error(t, err, hidden)
	}
	call, err := op.Build(map[string]json.RawMessage{
		"keyword": json.RawMessage(`"部署"`), "page_size": json.RawMessage(`10`),
	})
	require.NoError(t, err)
	assert.Equal(t, http.MethodGet, call.Method)
	assert.Contains(t, call.URI(), "page_size=10")

	assert.JSONEq(t, `{"data":[{"session_id":"s1","title":"部署问题","is_pinned":true,
		"created_at":"2026-10-01T08:00:00Z","updated_at":"2026-10-15T08:00:00Z"}],
		"meta":{"total":1,"page":1,"page_size":10}}`,
		projectJSON(t, op, `{"success":true,"total":1,"page":1,"page_size":10,"data":[{"id":"s1",
		"title":"部署问题","user_id":"u1","tenant_id":7,"is_pinned":true,"kind":"",
		"last_request_state":{"agent_id":"a1"},"im_platform":"feishu","im_user_id":"ou_1",
		"created_at":"2026-10-01T08:00:00Z","updated_at":"2026-10-15T08:00:00Z"}]}`))
}

func TestBuiltinMessageList(t *testing.T) {
	op, ok := Builtin().Get("message.list")
	require.True(t, ok)
	assert.Equal(t, RiskRead, op.Risk)
	assert.ElementsMatch(t, []string{"session_id", "limit", "before_time"}, paramNames(op))
	limit, _ := op.Param("limit")
	assert.Equal(t, 20, limit.Maximum)
	_, err := op.Build(map[string]json.RawMessage{
		"session_id": json.RawMessage(`"s1"`), "resource_urls": json.RawMessage(`"public"`),
	})
	require.Error(t, err, "file links stay handles")

	long := strings.Repeat("长", 1600)
	projected, err := op.Project([]byte(`{"success":true,"data":[{"id":"m1","session_id":"s1",
		"request_id":"r1","role":"assistant","content":"` + long + `",
		"knowledge_references":[{"id":"c1"}],"agent_steps":[{"thought":"x"}],
		"created_at":"2026-10-15T08:00:00Z"}]}`))
	require.NoError(t, err)
	assert.True(t, projected.Truncated)
	out, err := json.Marshal(projected.Data)
	require.NoError(t, err)
	assert.NotContains(t, string(out), "knowledge_references")
	assert.NotContains(t, string(out), "agent_steps")
	var rows []map[string]any
	require.NoError(t, json.Unmarshal(out, &rows))
	require.Len(t, rows, 1)
	assert.Equal(t, "m1", rows[0]["message_id"])
	assert.Equal(t, "assistant", rows[0]["role"])
	assert.LessOrEqual(t, len([]rune(rows[0]["content"].(string))), 1501)
}

func TestBuiltinMessageSearch(t *testing.T) {
	op, ok := Builtin().Get("message.search")
	require.True(t, ok)
	assert.Equal(t, RiskProbe, op.Risk)
	assert.Equal(t, http.MethodPost, op.Method)
	assert.ElementsMatch(t, []string{"query", "limit"}, paramNames(op))
	query, _ := op.Param("query")
	assert.True(t, query.Required)
	limit, _ := op.Param("limit")
	assert.Equal(t, 20, limit.Maximum)

	call, err := op.Build(map[string]json.RawMessage{"query": json.RawMessage(`"上次的配置"`)})
	require.NoError(t, err)
	assert.JSONEq(t, `{"query":"上次的配置"}`, string(call.Body))

	assert.JSONEq(t, `{"data":[{"request_id":"r1","session_id":"s1","session_title":"部署",
		"query_content":"怎么配置","answer_content":"这样配置","match_type":"keyword",
		"created_at":"2026-10-15T08:00:00Z"}],"meta":{"total":1}}`,
		projectJSON(t, op, `{"success":true,"data":{"total":1,"items":[{"request_id":"r1",
		"session_id":"s1","session_title":"部署","query_content":"怎么配置","answer_content":"这样配置",
		"score":0.42,"match_type":"keyword","created_at":"2026-10-15T08:00:00Z"}]}}`))
}

func TestBuiltinChatHistoryStats(t *testing.T) {
	op, ok := Builtin().Get("message.chat_history_stats")
	require.True(t, ok)
	assert.Equal(t, RiskRead, op.Risk)
	assert.Empty(t, op.Params)
	assert.JSONEq(t, `{"data":{"enabled":true,"embedding_model_id":"emb-1","indexed_message_count":12}}`,
		projectJSON(t, op, `{"success":true,"data":{"enabled":true,"embedding_model_id":"emb-1",
		"knowledge_base_id":"kb-hidden","knowledge_base_name":"__chat_history__",
		"indexed_message_count":12,"has_indexed_messages":true}}`))
}
