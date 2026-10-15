package tools

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/Tencent/WeKnora/internal/agent/approval"
	"github.com/Tencent/WeKnora/internal/agent/operations"
	"github.com/Tencent/WeKnora/internal/types"
)

// assistantToolAccess belongs to one run, including deferred MCP tools. External
// hints and command text cannot establish that an arbitrary call is read-only.
type assistantToolAccess struct {
	operations.Access
	gate approval.BuiltinApproval
}

// SetAssistantAccess applies the assistant's policy to every registration and
// execution path. Ordinary agents leave this unset. Call before registering tools.
func (r *ToolRegistry) SetAssistantAccess(level types.AssistantAccessLevel, gate approval.BuiltinApproval) {
	r.assistantAccess = &assistantToolAccess{Access: operations.Access{Level: level}, gate: gate}
}

// assistantToolRisk is deliberately an allowlist: newly added tools must not
// silently acquire write access in read-only mode. Unknown effects ask in
// confirm and safe auto, including shell, browser automation and external MCP.
func assistantToolRisk(name string) operations.Risk {
	switch name {
	case ToolAskUser, ToolTodoWrite, ToolThinking,
		ToolWebSearch, ToolWebFetch,
		ToolSearchKnowledge, ToolReadDocument, ToolListDocuments, ToolQueryKnowledgeGraph,
		ToolSearchConversations, ToolSearchMemory, ToolDataSchema,
		ToolSearchWeKnoraDocs, ToolReadWeKnoraDoc,
		ToolWikiReadPage, ToolWikiSearch, ToolWikiReadIssue,
		ToolReadFile, ToolListSandboxFiles, ToolFindOperations:
		return operations.RiskRead
	case ToolWikiWritePage, ToolWikiReplaceText,
		ToolWikiFlagIssue, ToolWikiUpdateIssue:
		return operations.RiskWrite
	case ToolWikiRenamePage:
		// Rename recreates the page and deletes the old page's revision history.
		// Until it preserves that history, safe auto must ask before running it.
		return operations.RiskDestructive
	default:
		return operations.RiskDestructive
	}
}

func (a *assistantToolAccess) blocks(name string) bool {
	// run_operation enforces its operation-specific risk itself.
	return a != nil && a.Level == types.AssistantAccessReadOnly &&
		name != ToolRunOperation && assistantToolRisk(name) != operations.RiskRead
}

func (r *ToolRegistry) assistantExecutionTool(tool types.Tool) types.Tool {
	if r.assistantAccess == nil || tool.Name() == ToolRunOperation ||
		tool.Name() == ToolDiscoverMCPTools || tool.Name() == ToolCallMCPTool {
		return tool
	}
	switch tool.(type) {
	case *MCPTool, *MCPRegisteredTool:
		// MCP keeps its own service policy and OAuth flow, using the same
		// assistant approval requirement inside MCPTool.Execute.
		return tool
	}
	risk := assistantToolRisk(tool.Name())
	if risk == operations.RiskRead {
		return tool
	}
	confirmed := NewConfirmedTool(tool, risk, r.assistantAccess.Access, r.assistantAccess.gate)
	if writer, ok := tool.(*WriteSandboxFileTool); ok {
		return &assistantFileWriteTool{ConfirmedTool: confirmed, writer: writer}
	}
	return confirmed
}

// A workspace checkpoint taken after the turn is not proof that the current
// file contents can be restored. Existing files therefore need approval in
// safe auto; full_auto explicitly permits overwrites without asking.
// Only an atomic create may bypass it in auto_write; a stat-then-write would
// race other sessions or host processes and could overwrite their files.
type assistantFileWriteTool struct {
	*ConfirmedTool
	writer *WriteSandboxFileTool
}

var errFileApprovalRequired = errors.New("file mutation needs confirmation")

func (t *assistantFileWriteTool) Execute(ctx context.Context, args json.RawMessage) (*types.ToolResult, error) {
	meta, _ := ToolExecFromContext(ctx)
	resumed := meta != nil && meta.Answered != nil && meta.Answered.Kind == types.AgentPromptKindToolApproval
	if t.access.Level == types.AssistantAccessAutoWrite && !resumed {
		result, err := t.writer.execute(ctx, args, true)
		if !errors.Is(err, errFileApprovalRequired) {
			return result, err
		}
	}
	return t.ConfirmedTool.Execute(ctx, args)
}
