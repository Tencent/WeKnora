package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// Document-lifecycle actions for the KB sidebar assistant (Phase 3 skill
// pack, v1): reparse and cancel-parse. These are the two operations the
// assistant visibly lacked ("you re-triggered the parse — I cannot") and
// both are reversible: reparse re-runs the pipeline, cancel-parse stops it
// while keeping written chunks. Deletion deliberately stays out — destructive
// operations wait for an explicit HITL confirmation design.
//
// Authorization mirrors the wiki write tools: the registration site passes
// config.WritableKBIDs (the caller's own + editor-shared KBs), and the tool
// refuses any document outside that list. The knowledge service re-checks
// the grant again inside loadKnowledgeWrite, so a stale allowlist cannot
// widen the boundary.

// ManageDocumentTool triggers document-lifecycle operations on KBs the
// calling user may edit.
type ManageDocumentTool struct {
	BaseTool
	knowledge interfaces.KnowledgeService
	inbox     interfaces.AgentInboxService
	writable  []string
	// authorize mints the KB write grant the knowledge service requires
	// (RequireKBWrite checks context grants, not caller ownership alone).
	// The registration site wires it to access.ResolveKB with the session's
	// share lookups, so the same editor-permission gate the HTTP handler
	// applies is enforced here.
	authorize func(context.Context, *types.KnowledgeBase) (context.Context, error)
}

// NewManageDocumentTool creates manage_document scoped to writable KBs.
func NewManageDocumentTool(
	knowledge interfaces.KnowledgeService,
	inbox interfaces.AgentInboxService,
	writableKBIDs []string,
	authorize func(context.Context, *types.KnowledgeBase) (context.Context, error),
) *ManageDocumentTool {
	return &ManageDocumentTool{
		BaseTool: NewBaseTool(
			ToolManageDocument,
			"Trigger document lifecycle operations in knowledge bases you can edit: "+
				"'reparse' re-runs parsing for a document (use after parse failures, when the "+
				"user asks to rebuild it, or when its content looks stale); 'cancel_parse' stops "+
				"an in-progress parse (already written chunks are kept). Requires a knowledge_id. "+
				"Never invent one — take it from list_documents, search results or the inbox.",
			json.RawMessage(`{
				"type": "object",
				"properties": {
					"operation": {
						"type": "string",
						"enum": ["reparse", "cancel_parse"],
						"description": "Which lifecycle operation to run."
					},
					"knowledge_id": {
						"type": "string",
						"description": "The document (knowledge) id to operate on."
					}
				},
				"required": ["operation", "knowledge_id"]
			}`),
		),
		knowledge: knowledge,
		inbox:     inbox,
		writable:  writableKBIDs,
		authorize: authorize,
	}
}

// Execute validates scope, runs the requested operation and files an
// agent_action receipt so the next turn (and the user's inbox panel) can see
// what the assistant did on its own initiative.
func (t *ManageDocumentTool) Execute(ctx context.Context, args json.RawMessage) (*types.ToolResult, error) {
	if t.knowledge == nil {
		return &types.ToolResult{Success: false, Error: "document service is not available"}, nil
	}
	var params struct {
		Operation   string `json:"operation"`
		KnowledgeID string `json:"knowledge_id"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return &types.ToolResult{Success: false, Error: "Invalid arguments: " + err.Error()}, nil
	}
	if params.KnowledgeID == "" {
		return &types.ToolResult{Success: false, Error: "knowledge_id is required"}, nil
	}

	doc, err := t.knowledge.GetKnowledgeByIDOnly(ctx, params.KnowledgeID)
	if err != nil || doc == nil {
		return &types.ToolResult{Success: false, Error: "document not found: " + params.KnowledgeID}, nil
	}
	if !containsID(t.writable, doc.KnowledgeBaseID) {
		return &types.ToolResult{
			Success: false,
			Error:   "You do not have edit access to the knowledge base this document belongs to.",
		}, nil
	}
	if t.authorize == nil {
		return &types.ToolResult{
			Success: false,
			Error:   "document management is not authorized in this session",
		}, nil
	}
	effCtx, authErr := t.authorize(ctx, &types.KnowledgeBase{
		ID:       doc.KnowledgeBaseID,
		TenantID: doc.TenantID,
	})
	if authErr != nil {
		return &types.ToolResult{
			Success: false,
			Error:   "You do not have edit access to the knowledge base this document belongs to.",
		}, nil
	}
	ctx = effCtx

	title := doc.Title
	switch params.Operation {
	case "reparse":
		if _, err := t.knowledge.ReparseKnowledge(ctx, params.KnowledgeID, nil); err != nil {
			return &types.ToolResult{
				Success: false,
				Error:   fmt.Sprintf("Reparse failed for %q: %v", title, err),
			}, nil
		}
		t.emitReceipt(ctx, types.InboxTypeAgentDocReparse, doc, title)
		return &types.ToolResult{
			Success: true,
			Output: fmt.Sprintf("Reparse started for %q. Parsing runs in the background; "+
				"the parse.completed / parse.failed inbox events will report the outcome.", title),
		}, nil
	case "cancel_parse":
		if _, err := t.knowledge.CancelKnowledgeParse(ctx, params.KnowledgeID); err != nil {
			return &types.ToolResult{
				Success: false,
				Error:   fmt.Sprintf("Cancel-parse failed for %q: %v", title, err),
			}, nil
		}
		t.emitReceipt(ctx, types.InboxTypeAgentDocCancelParse, doc, title)
		return &types.ToolResult{
			Success: true,
			Output: fmt.Sprintf("Parse cancelled for %q. Already written chunks are kept; "+
				"reparse can be triggered later.", title),
		}, nil
	default:
		return &types.ToolResult{
			Success: false,
			Error:   "Unknown operation " + params.Operation + " (use \"reparse\" or \"cancel_parse\")",
		}, nil
	}
}

// emitReceipt files the agent-action receipt. Best-effort: an inbox problem
// must never fail an already-executed operation.
func (t *ManageDocumentTool) emitReceipt(
	ctx context.Context, receiptType string, doc *types.Knowledge, title string,
) {
	if t.inbox == nil {
		return
	}
	userID := ""
	caller := types.CallerFromContext(ctx)
	userID = caller.UserID
	t.inbox.Emit(ctx, receiptType, doc.KnowledgeBaseID, userID, types.JSONMap{
		"knowledge_id": doc.ID,
		"title":        title,
		"route":        "tool:" + ToolManageDocument,
		"method":       "agent",
		"dedup_key":    "",
	})
}

// containsID reports whether the list contains the id.
func containsID(ids []string, id string) bool {
	for _, v := range ids {
		if v == id {
			return true
		}
	}
	return false
}
