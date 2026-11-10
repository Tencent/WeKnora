package mcpserver

import (
	"context"
	"strings"

	"github.com/Tencent/WeKnora/internal/application/access"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/mark3labs/mcp-go/mcp"
)

func addDocumentTool() mcp.Tool {
	return mcp.NewTool(types.MCPEndpointToolAddDocument,
		mcp.WithDescription("Add a document to a knowledge base from Markdown text or from a URL. Text documents "+
			"are stored as editable Markdown pages; URLs are fetched and parsed asynchronously. Returns the new "+
			"document id."),
		mcp.WithString("knowledge_base_id", mcp.Required(), mcp.Description("Knowledge base id or exact name")),
		mcp.WithString("title", mcp.Required(), mcp.Description("Document title")),
		mcp.WithString("content", mcp.Description("Markdown content; required unless url is given")),
		mcp.WithString("url", mcp.Description("Web page or file URL to import instead of content")),
		mcp.WithBoolean("publish", mcp.Description("For text documents: publish immediately (default true) or keep "+
			"as draft")),
		mcp.WithReadOnlyHintAnnotation(false),
		mcp.WithDestructiveHintAnnotation(true),
	)
}

func updateDocumentTool() mcp.Tool {
	return mcp.NewTool(types.MCPEndpointToolUpdateDocument,
		mcp.WithDescription("Replace the content (and optionally the title) of a Markdown document created from "+
			"text. The document is re-indexed asynchronously."),
		mcp.WithString("knowledge_id", mcp.Required(), mcp.Description("Document id")),
		mcp.WithString("content", mcp.Required(), mcp.Description("New Markdown content")),
		mcp.WithString("title", mcp.Description("New title; keeps the current title when omitted")),
		mcp.WithBoolean("publish", mcp.Description("Publish (default true) or keep as draft")),
		mcp.WithDestructiveHintAnnotation(true),
	)
}

func deleteDocumentTool() mcp.Tool {
	return mcp.NewTool(types.MCPEndpointToolDeleteDocument,
		mcp.WithDescription("Permanently delete a document and its index data from its knowledge base."),
		mcp.WithString("knowledge_id", mcp.Required(), mcp.Description("Document id")),
		mcp.WithDestructiveHintAnnotation(true),
	)
}

func manualStatus(publish bool) string {
	if publish {
		return types.ManualKnowledgeStatusPublish
	}
	return types.ManualKnowledgeStatusDraft
}

func (s *Server) handleAddDocument(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ep, err := endpointFromContext(ctx)
	if err != nil {
		return mcp.NewToolResultError("unauthorized"), nil
	}
	selector, err := req.RequireString("knowledge_base_id")
	if err != nil {
		return mcp.NewToolResultError("knowledge_base_id is required"), nil
	}
	title := strings.TrimSpace(req.GetString("title", ""))
	if title == "" {
		return mcp.NewToolResultError("title is required"), nil
	}
	content := req.GetString("content", "")
	url := strings.TrimSpace(req.GetString("url", ""))
	if strings.TrimSpace(content) == "" && url == "" {
		return mcp.NewToolResultError("either content or url is required"), nil
	}
	kbs, err := s.selectKnowledgeBases(ctx, ep, []string{selector})
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	kb := kbs[0]
	ctx, err = s.scopedKBContext(ctx, kb, types.OrgRoleEditor)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if err := access.RequireKBWrite(ctx, kb); err != nil {
		return mcp.NewToolResultError("this endpoint is not allowed to write to knowledge base " + kb.ID), nil
	}

	var created *types.Knowledge
	if url != "" {
		created, err = s.knowledgeService.CreateKnowledgeFromURL(
			ctx, kb.ID, url, "", "", nil, title, nil, askChannel, nil,
		)
	} else {
		created, err = s.knowledgeService.CreateKnowledgeFromManual(ctx, kb.ID, &types.ManualKnowledgePayload{
			Title:   title,
			Content: content,
			Status:  manualStatus(req.GetBool("publish", true)),
			Channel: askChannel,
		}, askChannel)
	}
	if err != nil {
		return mcp.NewToolResultErrorFromErr("failed to add document", err), nil
	}
	return jsonResult(map[string]any{
		"knowledge_base_id": kb.ID,
		"document":          summarizeKnowledge(created),
		"note": "Indexing runs asynchronously; the document becomes searchable once parse_status is " +
			"completed.",
	})
}

func (s *Server) handleUpdateDocument(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ep, err := endpointFromContext(ctx)
	if err != nil {
		return mcp.NewToolResultError("unauthorized"), nil
	}
	knowledgeID, err := req.RequireString("knowledge_id")
	if err != nil {
		return mcp.NewToolResultError("knowledge_id is required"), nil
	}
	content := req.GetString("content", "")
	if strings.TrimSpace(content) == "" {
		return mcp.NewToolResultError("content is required"), nil
	}
	existing, kb, err := s.knowledgeInScope(ctx, ep, knowledgeID)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	ctx, err = s.scopedKBContext(ctx, kb, types.OrgRoleEditor)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	title := strings.TrimSpace(req.GetString("title", ""))
	if title == "" {
		title = existing.Title
	}
	updated, err := s.knowledgeService.UpdateManualKnowledge(ctx, existing.ID, &types.ManualKnowledgePayload{
		Title:   title,
		Content: content,
		Status:  manualStatus(req.GetBool("publish", true)),
		Channel: askChannel,
	})
	if err != nil {
		return mcp.NewToolResultErrorFromErr("failed to update document", err), nil
	}
	return jsonResult(map[string]any{
		"document": summarizeKnowledge(updated),
		"note":     "Re-indexing runs asynchronously.",
	})
}

func (s *Server) handleDeleteDocument(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ep, err := endpointFromContext(ctx)
	if err != nil {
		return mcp.NewToolResultError("unauthorized"), nil
	}
	knowledgeID, err := req.RequireString("knowledge_id")
	if err != nil {
		return mcp.NewToolResultError("knowledge_id is required"), nil
	}
	existing, kb, err := s.knowledgeInScope(ctx, ep, knowledgeID)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	ctx, err = s.scopedKBContext(ctx, kb, types.OrgRoleEditor)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if err := s.knowledgeService.DeleteKnowledge(ctx, existing.ID); err != nil {
		return mcp.NewToolResultErrorFromErr("failed to delete document", err), nil
	}
	return jsonResult(map[string]any{
		"deleted":      true,
		"knowledge_id": existing.ID,
		"title":        existing.Title,
	})
}
