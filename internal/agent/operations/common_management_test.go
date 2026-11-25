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

func TestDataSourceOperationsExposeSavedStateOnly(t *testing.T) {
	for _, name := range []string{"list", "get", "test", "sync", "pause", "resume", "list_sync_logs", "get_sync_log"} {
		op, ok := Builtin().Get("data_source." + name)
		require.True(t, ok, name)
		wantRole := types.TenantRoleViewer
		if name == "test" || name == "sync" || name == "pause" || name == "resume" {
			wantRole = types.TenantRoleAdmin
		}
		assert.Equal(t, wantRole, op.Role, name)
		assert.Equal(t, name == "sync" || name == "pause" || name == "resume", op.Risk.Mutates(), name)
		if op.Risk.Mutates() {
			assert.Equal(t, &Target{Operation: "data_source.get", Label: "name"}, op.Target)
		}
		_, err := op.Build(map[string]json.RawMessage{"credentials": raw(map[string]string{"token": "secret"})})
		require.ErrorContains(t, err, "unknown parameter")
	}
	sync, _ := Builtin().Get("data_source.sync")
	assert.Equal(t, RiskDestructive, sync.Risk, "saved sync policy can delete documents")
	assert.True(t, (Access{Level: types.AssistantAccessAutoWrite}).NeedsApproval(sync))
	testOp, _ := Builtin().Get("data_source.test")
	assert.Equal(t, RiskProbe, testOp.Risk)
	for _, name := range []string{"create", "update", "delete"} {
		_, ok := Builtin().Get("data_source." + name)
		assert.False(t, ok)
	}

	payload := `{
  "id": "ds1",
  "knowledge_base_id": "kb1",
  "name": "Docs",
  "type": "notion",
  "status": "active",
  "sync_deletions": true,
  "tenant_id": 7,
  "config": {
    "credentials": {
      "token": "secret"
    },
    "settings": {
      "token": "secret"
    }
  },
  "credentials": {
    "token": "secret"
  },
  "last_sync_cursor": {
    "token": "secret"
  },
  "last_sync_result": {
    "token": "secret"
  },
  "latest_sync_log": {
    "id": "log1",
    "status": "failed",
    "items_failed": 2,
    "error_message": "timeout",
    "result": {
      "token": "secret"
    }
  }
}`
	get, _ := Builtin().Get("data_source.get")
	out := projectJSON(t, get, payload)
	assert.Contains(t, out, `"data_source_id":"ds1"`)
	assert.Contains(t, out, `"knowledge_base_id":"kb1"`)
	assert.NotContains(t, out, `"sync_log_id":"log1"`)
	assert.NotContains(t, out, `"latest_sync_log"`)
	for _, hidden := range []string{
		"secret",
		"credentials",
		"tenant_id",
		"config",
		"last_sync_cursor",
		"last_sync_result",
	} {
		assert.NotContains(t, out, hidden)
	}
	list, _ := Builtin().Get("data_source.list")
	assert.True(t, list.Result.List)
	assert.Contains(t, projectJSON(t, list, "["+payload+"]"), `"data_source_id":"ds1"`)
	call, err := list.Build(map[string]json.RawMessage{"knowledge_base_id": raw("kb1")})
	require.NoError(t, err)
	assert.Equal(t, "/api/v1/datasource?kb_id=kb1", call.URI())
}

func TestDataSourceSyncLogsAreBoundedAndFollowable(t *testing.T) {
	logs, _ := Builtin().Get("data_source.list_sync_logs")
	call, err := logs.Build(
		map[string]json.RawMessage{"data_source_id": raw("ds1"), "limit": raw(20), "offset": raw(20)},
	)
	require.NoError(t, err)
	assert.Equal(t, "/api/v1/datasource/ds1/logs?limit=20&offset=20", call.URI())
	for _, limit := range []int{0, 51} {
		_, err = logs.Build(map[string]json.RawMessage{"data_source_id": raw("ds1"), "limit": raw(limit)})
		require.Error(t, err)
	}
	for _, name := range []string{"data_source.sync", "data_source.get_sync_log"} {
		op, _ := Builtin().Get(name)
		assert.JSONEq(t, `{
  "data": {
    "sync_log_id": "log1",
    "data_source_id": "ds1",
    "status": "running",
    "items_created": 3
  }
}`, projectJSON(t, op, `{
  "id": "log1",
  "data_source_id": "ds1",
  "status": "running",
  "items_created": 3,
  "tenant_id": 7,
  "result": {
    "credentials": "secret"
  }
}`))
	}
	for _, name := range []string{"test", "pause", "resume"} {
		op, _ := Builtin().Get("data_source." + name)
		assert.JSONEq(t, `{"data":{"status":"active"}}`, projectJSON(t, op, `{"status":"active"}`))
	}
}

func TestChunkEditRequiresRevisionAndCompleteContent(t *testing.T) {
	get, _ := Builtin().Get("chunk.get")
	update, _ := Builtin().Get("chunk.update")
	require.True(t, update.Target.RequireComplete)
	assert.Equal(t, "chunk.get", update.Target.Operation)
	assert.ElementsMatch(
		t,
		[]string{"knowledge_id", "chunk_id", "content", "is_enabled", "expected_revision"},
		paramNames(update),
	)
	content := strings.Repeat("文", 3000)
	projected, err := get.Project(
		raw(
			map[string]any{
				"data": map[string]any{
					"id":                "c1",
					"knowledge_id":      "k1",
					"knowledge_base_id": "kb1",
					"content":           content,
					"content_revision":  0,
					"index_status":      "ready",
				},
			},
		),
	)
	require.NoError(t, err)
	require.False(t, projected.Truncated)
	assert.Equal(t, content, projected.Data.(map[string]any)["content"])
	params := map[string]json.RawMessage{
		"knowledge_id":      raw("k1"),
		"chunk_id":          raw("c1"),
		"content":           raw(content),
		"expected_revision": raw(0),
		"is_enabled":        raw(false),
	}
	call, err := update.Build(params)
	require.NoError(t, err)
	assert.Equal(t, http.MethodPut, call.Method)
	assert.Equal(t, "/api/v1/chunks/k1/c1", call.URI())
	var body map[string]any
	require.NoError(t, json.Unmarshal(call.Body, &body))
	assert.Equal(t, content, body["content"])
	assert.Equal(t, false, body["is_enabled"])
	delete(params, "expected_revision")
	_, err = update.Build(params)
	require.ErrorContains(t, err, "expected_revision: is required")
	params["expected_revision"] = raw(-1)
	_, err = update.Build(params)
	require.Error(t, err)
	projected, err = get.Project(raw(map[string]any{"data": map[string]any{"content": strings.Repeat("x", 8001)}}))
	require.NoError(t, err)
	assert.True(t, projected.Truncated)
	list, _ := Builtin().Get("chunk.list")
	projected, err = list.Project(raw(map[string]any{"data": []any{map[string]any{"content": content}}}))
	require.NoError(t, err)
	assert.True(t, projected.Truncated, "list previews stay compact")
}

func TestChunkRevertUsesSavedRevision(t *testing.T) {
	revisions, _ := Builtin().Get("chunk.list_revisions")
	assert.Equal(t, RiskRead, revisions.Risk)
	out := projectJSON(t, revisions, `{
  "data": [
    {
      "chunk_id": "c1",
      "revision": 0,
      "content": "original",
      "is_enabled": false,
      "edit_source": "manual",
      "edited_at": "2026-10-17",
      "editor_id": "private",
      "tenant_id": 7
    }
  ]
}`)
	assert.Contains(t, out, `"revision":0`)
	assert.NotContains(t, out, "editor_id")
	revert, _ := Builtin().Get("chunk.revert")
	call, err := revert.Build(
		map[string]json.RawMessage{
			"knowledge_id":      raw("k1"),
			"chunk_id":          raw("c1"),
			"revision":          raw(0),
			"expected_revision": raw(2),
		},
	)
	require.NoError(t, err)
	assert.JSONEq(t, `{"revision":0,"expected_revision":2}`, string(call.Body))
	assert.Equal(t, "/api/v1/chunks/k1/c1/revert", call.URI())
}

func TestKnowledgeBaseCopyCannotOverwriteAnExistingDestination(t *testing.T) {
	op, ok := Builtin().Get("knowledge_base.copy")
	require.True(t, ok)
	assert.ElementsMatch(t, []string{"knowledge_base_id"}, paramNames(op))
	call, err := op.Build(map[string]json.RawMessage{"knowledge_base_id": raw("source")})
	require.NoError(t, err)
	assert.JSONEq(t, `{"source_id":"source"}`, string(call.Body))
	for _, hidden := range []string{"target_id", "task_id", "target_knowledge_base_id"} {
		_, err := op.Build(map[string]json.RawMessage{"knowledge_base_id": raw("source"), hidden: raw("existing")})
		require.ErrorContains(t, err, "unknown parameter")
	}
	assert.JSONEq(t, `{
  "data": {
    "task_id": "task1",
    "source_knowledge_base_id": "source",
    "knowledge_base_id": "new"
  }
}`, projectJSON(t, op, `{"data":{"task_id":"task1","source_id":"source","target_id":"new"}}`))
	duplicate, _ := Builtin().Get("knowledge_base.duplicate")
	assert.Contains(t, duplicate.Description, "settings only")
	assert.Contains(t, projectJSON(t, duplicate, `{
  "data": {
    "source_id": "source",
    "target_id": "new",
    "knowledge_base": {
      "api_key": "secret"
    }
  }
}`), `"knowledge_base_id":"new"`)
	progress, _ := Builtin().Get("knowledge_base.get_copy_progress")
	assert.JSONEq(t, `{
  "data": {
    "task_id": "task1",
    "knowledge_base_id": "new",
    "status": "failed",
    "progress": 40,
    "error": "quota exceeded"
  }
}`, projectJSON(t, progress, `{
  "data": {
    "task_id": "task1",
    "target_id": "new",
    "status": "failed",
    "progress": 40,
    "error": "quota exceeded"
  }
}`))
}

func TestDataSourceOperationsOmitRawUpstreamErrors(t *testing.T) {
	failure := `Get "https://feeds.example.org/private?token=saved-secret": context deadline exceeded`
	for _, id := range []string{
		"data_source.get", "data_source.list", "data_source.update_sync",
		"data_source.sync", "data_source.get_sync_log", "data_source.list_sync_logs",
	} {
		t.Run(id, func(t *testing.T) {
			op, ok := Builtin().Get(id)
			require.True(t, ok)
			row := map[string]any{"id": "item", "status": "failed", "error_message": failure, "items_failed": 1}
			var payload any = row
			if op.Result.List {
				payload = []any{row}
			}
			out := projectJSON(t, op, string(raw(payload)))
			assert.Contains(t, out, `"status":"failed"`)
			assert.NotContains(t, out, "saved-secret")
			assert.NotContains(t, out, "error_message")
		})
	}
}

func TestManualKnowledgePreservesMarkdownWhitespace(t *testing.T) {
	for _, id := range []string{"knowledge.create_manual", "knowledge.update_manual"} {
		t.Run(id, func(t *testing.T) {
			op, ok := Builtin().Get(id)
			require.True(t, ok)
			params := map[string]json.RawMessage{
				"knowledge_base_id": raw("kb1"), "knowledge_id": raw("k1"),
				"content": raw("    code\n\nparagraph  \n"), "status": raw("draft"),
			}
			call, err := op.Build(SelectParams(op, params))
			require.NoError(t, err)
			var body map[string]any
			require.NoError(t, json.Unmarshal(call.Body, &body))
			assert.Equal(t, "    code\n\nparagraph  \n", body["content"])
		})
	}
}
