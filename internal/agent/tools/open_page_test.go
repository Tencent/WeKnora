package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/Tencent/WeKnora/internal/agent/approval"
	"github.com/Tencent/WeKnora/internal/agent/operations"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func ok(body string) *operations.Response {
	return &operations.Response{Status: http.StatusOK, Body: []byte(body)}
}

func openPageFixture(role types.TenantRole) (*OpenPageTool, *fakeDispatcher) {
	responses := map[string]string{
		"/api/v1/knowledge-bases/kb-1": `{"success":true,"data":{"id":"kb-1","name":"Handbook"}}`,
		"/api/v1/knowledge/doc-1":      `{"data":{"id":"doc-1","knowledge_base_id":"kb-1","title":"Leave policy"}}`,
		"/api/v1/knowledge/doc-other":  `{"data":{"id":"doc-other","knowledge_base_id":"kb-2","title":"Other"}}`,
		"/api/v1/agents/agent-1":       `{"success":true,"data":{"id":"agent-1","name":"Support bot"}}`,
		"/api/v1/models/m-1":           `{"success":true,"data":{"id":"m-1","name":"qwen-max","display_name":"Qwen"}}`,
		"/api/v1/mcp-services/mcp-1":   `{"success":true,"data":{"id":"mcp-1","name":"Jira"}}`,
		"/api/v1/automations/auto-1":   `{"success":true,"data":{"id":"auto-1","name":"Morning digest"}}`,
	}
	responses["/api/v1/knowledgebase/kb-1/wiki/pages/entity/acme"] = `{"slug":"entity/acme","title":"Acme"}`
	d := &fakeDispatcher{responses: map[string]*operations.Response{}}
	for path, body := range responses {
		d.responses["GET "+path] = ok(body)
	}
	tool := NewOpenPageTool(OperationsRuntime{
		Catalog:    operations.Builtin(),
		Dispatcher: d,
		Identity:   operations.Identity{TenantID: 7, UserID: "u1"},
		Access:     operations.Access{Role: role, EnforceRole: true},
	})
	return tool, d
}

func runOpenPage(t *testing.T, tool *OpenPageTool, args string) *types.ToolResult {
	t.Helper()
	ctx := context.WithValue(context.Background(), types.LanguageContextKey, "en-US")
	result, err := tool.Execute(ctx, json.RawMessage(args))
	require.NoError(t, err)
	return result
}

func TestOpenPageLinksCheckedTargets(t *testing.T) {
	tool, d := openPageFixture(types.TenantRoleAdmin)
	for _, tc := range []struct {
		name, args string
		want       map[string]any
	}{
		{
			name: "document",
			args: `{"page":"knowledge_base","knowledge_base_id":"kb-1","knowledge_id":"doc-1","note":"Reparse it"}`,
			want: map[string]any{
				"page": "knowledge_base", "label": "Handbook", "detail": "Leave policy",
				"knowledge_base_id": "kb-1", "knowledge_id": "doc-1", "note": "Reparse it",
			},
		},
		{
			name: "wiki page",
			args: `{"page":"wiki","knowledge_base_id":"kb-1","wiki_slug":"entity/acme"}`,
			want: map[string]any{"page": "wiki", "label": "Handbook", "detail": "Acme", "wiki_slug": "entity/acme"},
		},
		{
			name: "agent tab",
			args: `{"page":"agent_editor","agent_id":"agent-1","section":"knowledge"}`,
			want: map[string]any{"page": "agent_editor", "label": "Support bot", "section": "knowledge"},
		},
		{
			name: "existing model",
			args: `{"page":"settings_models","model_id":"m-1"}`,
			want: map[string]any{"page": "settings_models", "label": "Models", "detail": "Qwen", "model_id": "m-1"},
		},
		{
			name: "existing MCP service",
			args: `{"page":"mcp_services","mcp_service_id":"mcp-1"}`,
			want: map[string]any{"page": "mcp_services", "detail": "Jira", "mcp_service_id": "mcp-1"},
		},
		{
			name: "settings section",
			args: `{"page":"settings","section":"general"}`,
			want: map[string]any{
				"page": "settings", "label": "Settings", "detail": "General Settings", "section": "general",
			},
		},
		{
			name: "system settings user tab",
			args: `{"page":"settings","section":"system-global","panel":"users"}`,
			want: map[string]any{
				"page": "settings", "detail": "System Settings / User management",
				"section": "system-global", "panel": "users",
			},
		},
		{
			name: "settings integration",
			args: `{"page":"settings","section":"integration-chrome"}`,
			want: map[string]any{"page": "settings", "detail": "Chrome Extension", "section": "integration-chrome"},
		},
		{
			name: "toolbox tool",
			args: `{"page":"toolbox","section":"skills"}`,
			want: map[string]any{
				"page": "toolbox", "label": "Toolbox", "detail": "Skill Management", "section": "skills",
			},
		},
		{
			name: "agent list",
			args: `{"page":"agents"}`,
			want: map[string]any{"page": "agents", "label": "Agents"},
		},
		{
			name: "automation",
			args: `{"page":"automations","automation_id":"auto-1"}`,
			want: map[string]any{
				"page": "automations", "label": "Automations", "detail": "Morning digest", "automation_id": "auto-1",
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := runOpenPage(t, tool, tc.args)
			require.True(t, result.Success, result.Error)
			assert.Equal(t, ToolOpenPage, result.Data["display_type"])
			for key, value := range tc.want {
				assert.Equal(t, value, result.Data[key], key)
			}
			assert.Contains(t, result.Output, "Nothing was opened or saved")
		})
	}
	for _, call := range d.calls {
		assert.Equal(t, "u1", call.identity.UserID, "targets are read as the user")
	}
}

func TestOpenPageDrafts(t *testing.T) {
	tool, d := openPageFixture(types.TenantRoleAdmin)
	result := runOpenPage(t, tool, `{"page":"settings_models","model_draft":{
		"type":"chat","provider":"aliyun","model_name":"qwen-max","base_url":"https://dashscope.aliyuncs.com/v1"}}`)
	require.True(t, result.Success, result.Error)
	assert.Equal(t, "New model: qwen-max", result.Data["detail"])
	assert.Equal(t, map[string]any{
		"type": "chat", "provider": "aliyun", "model_name": "qwen-max", "base_url": "https://dashscope.aliyuncs.com/v1",
	}, result.Data["model_draft"])

	result = runOpenPage(t, tool, `{"page":"mcp_services","mcp_service_draft":{
		"name":"Jira","transport_type":"http-streamable","url":"https://mcp.example.com/mcp",
		"auth_type":"api_key","api_key_header":"X-Jira-Key"}}`)
	require.True(t, result.Success, result.Error)
	assert.Equal(t, "New service: Jira", result.Data["detail"])
	assert.Empty(t, d.calls, "a draft has nothing to read")
}

func TestOpenPageRefuses(t *testing.T) {
	tool, _ := openPageFixture(types.TenantRoleAdmin)
	for _, tc := range []struct {
		name, args, want string
	}{
		{
			"credential in a draft", `{"page":"settings_models","model_draft":{"type":"chat","api_key":"sk-1"}}`,
			"take no API keys",
		},
		{"credential in an MCP draft", `{"page":"mcp_services","mcp_service_draft":{"name":"x",
			"transport_type":"sse","url":"https://a.example","token":"t"}}`, "take no API keys"},
		{"stdio service", `{"page":"mcp_services","mcp_service_draft":{"name":"x","transport_type":"stdio",
			"url":"https://a.example"}}`, "stdio"},
		{
			"non-http url", `{"page":"settings_models","model_draft":{"type":"chat","base_url":"file:///etc/passwd"}}`,
			"http(s) URL",
		},
		{"url with credentials", `{"page":"mcp_services","mcp_service_draft":{"name":"x","transport_type":"sse",
			"url":"https://user:pw@a.example"}}`, "user name or password"},
		{
			"field of another page", `{"page":"agent_editor","agent_id":"agent-1","model_id":"m-1"}`,
			"does not apply",
		},
		{"edit and draft", `{"page":"settings_models","model_id":"m-1","model_draft":{"type":"chat"}}`, "not both"},
		{"unknown section", `{"page":"agent_editor","agent_id":"agent-1","section":"danger"}`, "section must be"},
		{"invisible target", `{"page":"agent_editor","agent_id":"someone-else"}`, "not visible to the user"},
		{"document of another base", `{"page":"knowledge_base","knowledge_base_id":"kb-1",
			"knowledge_id":"doc-other"}`, "not a document of that knowledge base"},
		{"unknown page", `{"page":"system"}`, "page must be one of"},
		{
			"unknown settings section", `{"page":"settings","section":"knowledge"}`,
			"section for page settings must be one of",
		},
		{"agent tab on the toolbox", `{"page":"toolbox","section":"tools"}`, "section for page toolbox must be one of"},
		{
			"panel outside system settings", `{"page":"settings","section":"members","panel":"users"}`,
			"panel applies only",
		},
		{
			"unknown system settings tab", `{"page":"settings","section":"system-global","panel":"billing"}`,
			"panel must be",
		},
		{"panel on a list page", `{"page":"agents","panel":"users"}`, "does not apply"},
		{"field of a list page", `{"page":"agents","agent_id":"agent-1"}`, "does not apply"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := runOpenPage(t, tool, tc.args)
			assert.False(t, result.Success)
			assert.Contains(t, result.Error, tc.want)
		})
	}
}

func TestViewPageNavigates(t *testing.T) {
	ctx := context.WithValue(pageToolContext(), types.LanguageContextKey, "en-US")
	run := func(actor approval.PageActor, args string) *types.ToolResult {
		fixture, _ := openPageFixture(types.TenantRoleAdmin)
		result, err := NewViewPageTool(fixture.rt, actor).Execute(ctx, json.RawMessage(args))
		require.NoError(t, err)
		require.True(t, result.Success, result.Error)
		return result
	}

	opened := &fakePageActor{res: approval.PageActionResult{Answered: true, Response: types.PageActionPromptResponse{
		Location: "Settings / General", Snapshot: `select "Font" = "System"`, ReturnPath: "/platform/agents",
	}}}
	result := run(opened, `{"page":"settings","section":"general"}`)
	require.Len(t, opened.got, 1)
	assert.Equal(t, types.PageActionPromptRequest{
		Action: types.PageActionNavigate,
		Target: &types.PageNavigateTarget{Page: "settings", Section: "general"},
	}, opened.got[0].Action)

	users := &fakePageActor{res: approval.PageActionResult{Answered: true, Response: types.PageActionPromptResponse{
		Location: "Settings / System", Snapshot: `tab "User management" [selected]`,
	}}}
	run(users, `{"page":"settings","section":"system-global","panel":"users"}`)
	assert.Equal(t, &types.PageNavigateTarget{Page: "settings", Section: "system-global", Panel: "users"},
		users.got[0].Action.Target)
	assert.Contains(t, result.Output, "Opened Settings")
	assert.Contains(t, result.Output, `select "Font" = "System"`)
	assert.Equal(t, true, result.Data["navigated"])
	assert.Equal(t, "/platform/agents", result.Data["return_path"])

	agent := &fakePageActor{res: approval.PageActionResult{Answered: true}}
	run(agent, `{"page":"agent_editor","agent_id":"agent-1","section":"tools"}`)
	assert.Equal(t, &types.PageNavigateTarget{Page: "agent_editor", AgentID: "agent-1", Section: "tools"},
		agent.got[0].Action.Target, "the checked link is what the browser opens")

	for name, tc := range map[string]struct {
		actor approval.PageActor
		want  string
	}{
		"unsaved changes": {&fakePageActor{res: approval.PageActionResult{
			Answered: true,
			Response: types.PageActionPromptResponse{Stayed: true},
		}}, "unsaved changes"},
		"page access off": {&fakePageActor{res: approval.PageActionResult{
			Answered: true,
			Response: types.PageActionPromptResponse{Declined: true},
		}}, "could not be opened"},
		"silent page":       {&fakePageActor{res: approval.PageActionResult{TimedOut: true}}, "could not be opened"},
		"no browser access": {nil, "could not be opened"},
	} {
		t.Run(name, func(t *testing.T) {
			result := run(tc.actor, `{"page":"settings","section":"general"}`)
			assert.Contains(t, result.Output, tc.want)
			assert.Contains(t, result.Output, "now sees a button", "the user still gets the button")
			assert.Nil(t, result.Data["navigated"])
		})
	}

	fixture, _ := openPageFixture(types.TenantRoleAdmin)
	result, err := NewViewPageTool(fixture.rt, opened).Execute(ctx,
		json.RawMessage(`{"page":"settings_models","model_draft":{"type":"chat"}}`))
	require.NoError(t, err)
	assert.False(t, result.Success)
	assert.Contains(t, result.Error, "takes no draft")

	result, err = fixture.Execute(ctx, json.RawMessage(`{"page":"settings","navigate":true}`))
	require.NoError(t, err)
	assert.False(t, result.Success, "open_page only offers a button")
}

func TestOpenPageModelAndMCPFormsNeedAdmin(t *testing.T) {
	tool, _ := openPageFixture(types.TenantRoleContributor)
	for _, args := range []string{
		`{"page":"settings_models","model_draft":{"type":"chat"}}`,
		`{"page":"mcp_services","mcp_service_id":"mcp-1"}`,
	} {
		result := runOpenPage(t, tool, args)
		assert.False(t, result.Success)
		assert.Contains(t, result.Error, "only workspace admins")
	}
	result := runOpenPage(t, tool, `{"page":"settings_models"}`)
	assert.True(t, result.Success, "anyone may be shown the model list")
}
