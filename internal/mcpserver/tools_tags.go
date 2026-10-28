package mcpserver

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/mark3labs/mcp-go/mcp"
)

// listTagsTool exposes the tags curated under one knowledge base so external
// MCP clients can constrain ask/search retrieval to them via tag_scopes /
// tag selectors, mirroring the web UI's @tag mentions.
func listTagsTool() mcp.Tool {
	return mcp.NewTool(types.MCPEndpointToolListTags,
		mcp.WithDescription("List the tags defined under one knowledge base, with their id and name. "+
			"Tags partition a knowledge base's documents (e.g. by region or project); pass their ids in "+
			"ask's tag_scopes to constrain retrieval to the tagged documents. keyword filters tag names."),
		mcp.WithString("knowledge_base_id", mcp.Required(),
			mcp.Description("Knowledge base id or exact name, as returned by list_knowledge_bases")),
		mcp.WithString("keyword", mcp.Description("Optional keyword to filter tag names")),
		mcp.WithNumber("page", mcp.Description("Page number, default 1")),
		mcp.WithNumber("page_size", mcp.Description("Tags per page, default 20, max 100")),
		readOnlyEndpointAnnotations(),
	)
}

type tagSummary struct {
	ID             string `json:"id"`
	SeqID          int64  `json:"seq_id"`
	Name           string `json:"name"`
	Color          string `json:"color,omitempty"`
	SortOrder      int    `json:"sort_order"`
	KnowledgeCount int64  `json:"knowledge_count"`
	ChunkCount     int64  `json:"chunk_count"`
}

func (s *Server) handleListTags(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ep, err := endpointFromContext(ctx)
	if err != nil {
		return mcp.NewToolResultError("unauthorized"), nil
	}
	selector, err := req.RequireString("knowledge_base_id")
	if err != nil {
		return mcp.NewToolResultError("knowledge_base_id is required"), nil
	}
	kbs, err := s.selectKnowledgeBases(ctx, ep, []string{selector})
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	kb := kbs[0]
	// Tags live under the knowledge base owner; run the listing there.
	ctx, err = s.scopedKBContext(ctx, kb, types.OrgRoleViewer)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	page := req.GetInt("page", 1)
	if page < 1 {
		page = 1
	}
	pageSize := req.GetInt("page_size", defaultListPageSize)
	if pageSize < 1 {
		pageSize = defaultListPageSize
	}
	if pageSize > maxListPageSize {
		pageSize = maxListPageSize
	}
	result, err := s.tagService.ListTags(
		ctx, kb.ID,
		&types.Pagination{Page: page, PageSize: pageSize},
		req.GetString("keyword", ""),
	)
	if err != nil {
		return mcp.NewToolResultErrorFromErr("failed to list tags", err), nil
	}
	tags := []tagSummary{}
	if rows, ok := result.Data.([]*types.KnowledgeTagWithStats); ok {
		for _, t := range rows {
			if t == nil {
				continue
			}
			tags = append(tags, tagSummary{
				ID:             t.ID,
				SeqID:          t.SeqID,
				Name:           t.Name,
				Color:          t.Color,
				SortOrder:      t.SortOrder,
				KnowledgeCount: t.KnowledgeCount,
				ChunkCount:     t.ChunkCount,
			})
		}
	}
	return jsonResult(map[string]any{
		"knowledge_base_id": kb.ID,
		"total":             result.Total,
		"page":              result.Page,
		"page_size":         result.PageSize,
		"tags":              tags,
	})
}
