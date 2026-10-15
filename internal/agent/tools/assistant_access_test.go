package tools

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Tencent/WeKnora/internal/agent/approval"
	"github.com/Tencent/WeKnora/internal/agent/operations"
	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestAssistantReadOnlyBlocksAllWriteSurfaces(t *testing.T) {
	for _, name := range []string{
		ToolShellExec, ToolWriteSandboxFile, ToolEditSandboxFile, ToolWriteSkillFile, ToolEditSkillFile,
		ToolWikiWritePage, ToolWikiReplaceText, ToolWikiDeletePage, ToolWikiFlagIssue, ToolWikiUpdateIssue,
		ToolDiscoverMCPTools, ToolCallMCPTool, "mcp_external_write", "new_unknown_tool",
	} {
		t.Run(name, func(t *testing.T) {
			inner := &countingTool{BaseTool: NewBaseTool(name, "", json.RawMessage(`{"type":"object"}`))}
			gate := &fakeApprover{decision: approval.Decision{Approved: true}}
			registry := NewToolRegistry()
			registry.SetAssistantAccess(types.AssistantAccessReadOnly, gate)
			registry.RegisterTool(inner)
			require.NotContains(t, registry.ListTools(), name)
			registry.RegisterDeferredTool(inner)
			require.NotContains(t, registry.ListTools(), name)

			// Simulate a retained/deferred tool. Execution is guarded too, even
			// when a previous run's approval is carried by the resumed call.
			registry.tools[name] = inner
			ctx := WithToolExecContext(context.Background(), &ToolExecContext{
				EventBus: event.NewEventBus(),
				Answered: &types.AgentPrompt{Kind: types.AgentPromptKindToolApproval},
			})
			res, err := registry.ExecuteTool(ctx, name, json.RawMessage(`{}`))
			require.NoError(t, err)
			require.False(t, res.Success)
			require.Contains(t, res.Error, "read-only")
			require.Empty(t, inner.calls)
			require.Empty(t, gate.requests)
		})
	}
}

func TestAssistantRegistryApprovalPolicy(t *testing.T) {
	for _, tc := range []struct {
		name  string
		level types.AssistantAccessLevel
		asks  bool
	}{
		{ToolReadFile, types.AssistantAccessReadOnly, false},
		{ToolListSandboxFiles, types.AssistantAccessReadOnly, false},
		{ToolSearchKnowledge, types.AssistantAccessReadOnly, false},
		{ToolWriteSandboxFile, types.AssistantAccessConfirm, true},
		{ToolEditSandboxFile, types.AssistantAccessAutoWrite, true},
		{ToolWikiWritePage, types.AssistantAccessConfirm, true},
		{ToolWikiDeletePage, types.AssistantAccessAutoWrite, true},
		{ToolShellExec, types.AssistantAccessConfirm, true},
		{ToolShellExec, types.AssistantAccessAutoWrite, true},
		{"new_unknown_tool", types.AssistantAccessAutoWrite, true},
		{ToolWriteSandboxFile, types.AssistantAccessFullAuto, false},
		{ToolEditSandboxFile, types.AssistantAccessFullAuto, false},
		{ToolWikiDeletePage, types.AssistantAccessFullAuto, false},
		{ToolShellExec, types.AssistantAccessFullAuto, false},
		{"local_browser", types.AssistantAccessFullAuto, false},
	} {
		t.Run(tc.name+"/"+string(tc.level), func(t *testing.T) {
			inner := &countingTool{BaseTool: NewBaseTool(tc.name, "", json.RawMessage(`{"type":"object"}`))}
			gate := &fakeApprover{decision: approval.Decision{Approved: false, Reason: "declined"}}
			registry := NewToolRegistry()
			registry.SetAssistantAccess(tc.level, gate)
			registry.RegisterTool(inner)
			res, err := registry.ExecuteTool(interactiveCtx(), tc.name, json.RawMessage(`{}`))
			require.NoError(t, err)
			if tc.asks {
				require.False(t, res.Success)
				require.Empty(t, inner.calls)
				require.Len(t, gate.requests, 1)
				gate.decision = approval.Decision{Approved: true, PendingID: "p1"}
				res, err = registry.ExecuteTool(interactiveCtx(), tc.name, json.RawMessage(`{}`))
				require.NoError(t, err)
				require.True(t, res.Success)
				require.Equal(t, []string{"p1"}, gate.executed)
			} else {
				require.True(t, res.Success)
				require.Empty(t, gate.requests)
			}
			require.Len(t, inner.calls, 1)
		})
	}
}

func TestConfirmedToolReadOnlyRejectsPriorApproval(t *testing.T) {
	inner := &countingTool{BaseTool: NewBaseTool(ToolShellExec, "", json.RawMessage(`{}`))}
	gate := &fakeApprover{decision: approval.Decision{Approved: true}}
	tool := NewConfirmedTool(inner, operations.RiskDestructive,
		operations.Access{Level: types.AssistantAccessReadOnly}, gate)
	ctx := WithToolExecContext(context.Background(), &ToolExecContext{
		EventBus: event.NewEventBus(), Answered: &types.AgentPrompt{Kind: types.AgentPromptKindToolApproval},
	})
	res, err := tool.Execute(ctx, json.RawMessage(`{}`))
	require.NoError(t, err)
	require.False(t, res.Success)
	require.Empty(t, inner.calls)
	require.Empty(t, gate.executed)
}

func TestAssistantReadOnlyExecutesBuiltinWebReaders(t *testing.T) {
	const url = "https://example.com/specs"
	provider := &searchOnlyWebService{results: []*types.WebSearchResult{{URL: url, Title: "Specs"}}}
	fetcher := newStubWebContentFetcher(map[string]string{url: "official specifications"}, nil)
	pages := newWebFetchTool(fetcher)
	registry := NewToolRegistry()
	registry.SetAssistantAccess(types.AssistantAccessReadOnly, nil)
	registry.RegisterTool(NewWebSearchTool(provider, 5, "provider").WithPageReader(pages))
	registry.RegisterTool(pages)
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7))
	res, err := registry.ExecuteTool(ctx, ToolWebSearch, json.RawMessage(`{"query":"specs"}`))
	require.NoError(t, err)
	require.True(t, res.Success, res.Error)
	require.Equal(t, 1, provider.calls)
	res, err = registry.ExecuteTool(ctx, ToolWebFetch, webFetchArgs(WebFetchItem{URL: url}))
	require.NoError(t, err)
	require.True(t, res.Success, res.Error)
	require.Equal(t, 1, fetcher.callCount[url])
	require.Contains(t, res.Output, "official specifications")
}

func TestAssistantFileChangesRequireConfirmationWithoutBackup(t *testing.T) {
	const path = "/workspace/notes.txt"
	for _, tc := range []struct{ name, args, want string }{
		{ToolWriteSandboxFile, `{"path":"/workspace/notes.txt","content":""}`, ""},
		{ToolWriteSandboxFile, `{"path":"/workspace/notes.txt","content":" added","mode":"append"}`, "original added"},
		{
			ToolEditSandboxFile,
			`{"path":"/workspace/notes.txt","edits":[{"old_string":"original","new_string":"edited"}]}`, "edited",
		},
	} {
		t.Run(tc.name+tc.want, func(t *testing.T) {
			files := &fakeSandboxFileEditor{files: map[string][]byte{path: []byte("original")}}
			gate := &fakeApprover{decision: approval.Decision{Approved: false, Reason: "declined"}}
			r := NewToolRegistry()
			r.SetAssistantAccess(types.AssistantAccessAutoWrite, gate)
			r.RegisterTool(NewWriteSandboxFileTool(files, 0))
			r.RegisterTool(NewEditSandboxFileTool(files))
			res, err := r.ExecuteTool(interactiveCtx(), tc.name, json.RawMessage(tc.args))
			require.NoError(t, err)
			require.False(t, res.Success)
			require.Equal(t, "original", string(files.files[path]))
			require.Zero(t, files.writes)
			require.Len(t, gate.requests, 1)
			require.Contains(t, gate.requests[0].Description, path)
			gate.decision = approval.Decision{Suspended: &types.PromptSuspension{}}
			res, err = r.ExecuteTool(interactiveCtx(), tc.name, json.RawMessage(tc.args))
			require.NoError(t, err)
			require.NotNil(t, res.Suspend)
			require.Zero(t, files.writes)
			gate.decision = approval.Decision{Approved: true, PendingID: "p1"}
			res, err = r.ExecuteTool(interactiveCtx(), tc.name, json.RawMessage(tc.args))
			require.NoError(t, err)
			require.True(t, res.Success, res.Error)
			require.Equal(t, tc.want, string(files.files[path]))
			require.Equal(t, 1, files.writes)
			require.Equal(t, []string{"p1"}, gate.executed)
		})
	}
}

func TestAssistantFileApprovalValidatesEditedArguments(t *testing.T) {
	files := &fakeSandboxFileEditor{files: map[string][]byte{"/workspace/notes.txt": []byte("original")}}
	gate := &fakeApprover{decision: approval.Decision{
		Approved: true, ModifiedArgs: json.RawMessage(`{"path":"/workspace/notes.txt"}`),
	}}
	r := NewToolRegistry()
	r.SetAssistantAccess(types.AssistantAccessAutoWrite, gate)
	r.RegisterTool(NewWriteSandboxFileTool(files, 0))
	res, err := r.ExecuteTool(interactiveCtx(), ToolWriteSandboxFile,
		json.RawMessage(`{"path":"/workspace/notes.txt","content":"changed"}`))
	require.NoError(t, err)
	require.False(t, res.Success)
	require.Contains(t, res.Error, "modified_args")
	require.Zero(t, files.writes)
}
