package mcpserver

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/Tencent/WeKnora/internal/agent/tools"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/mark3labs/mcp-go/mcp"
)

const (
	defaultWikiSearchLimit = 10
	maxWikiSearchLimit     = 50
	defaultWikiIndexLimit  = 50
	maxWikiIndexLimit      = 200
)

func wikiSearchTool() mcp.Tool {
	return mcp.NewTool(types.MCPEndpointToolWikiSearch,
		mcp.WithDescription("Search the generated wiki pages of the knowledge bases in scope by title, alias and "+
			"content. Returns page slugs that wiki_read_page accepts. Only knowledge bases with a wiki are "+
			"searched."),
		mcp.WithString("query", mcp.Required(), mcp.Description("Search terms or a regular expression")),
		mcp.WithArray("knowledge_base_ids", mcp.WithStringItems(),
			mcp.Description("Optional knowledge base ids or names to restrict the search")),
		mcp.WithNumber("limit", mcp.Description("Maximum pages to return, default 10, max 50")),
		mcp.WithReadOnlyHintAnnotation(true),
	)
}

func wikiReadPageTool() mcp.Tool {
	return mcp.NewTool(types.MCPEndpointToolWikiReadPage,
		mcp.WithDescription("Read one wiki page by slug, including its content and outgoing links."),
		mcp.WithString("slug", mcp.Required(), mcp.Description("Page slug from wiki_search or wiki_index, e.g. "+
			"concept/rag")),
		mcp.WithString("knowledge_base_id", mcp.Description("Knowledge base id or name, required only when the same "+
			"slug exists in several wikis")),
		mcp.WithReadOnlyHintAnnotation(true),
	)
}

func wikiIndexTool() mcp.Tool {
	return mcp.NewTool(types.MCPEndpointToolWikiIndex,
		mcp.WithDescription("Browse the table of contents of one knowledge base's wiki: the intro plus pages "+
			"grouped by type (summary, entity, concept, ...)."),
		mcp.WithString("knowledge_base_id", mcp.Required(), mcp.Description("Knowledge base id or exact name")),
		mcp.WithNumber("limit", mcp.Description("Maximum entries per page type, default 50, max 200")),
		mcp.WithReadOnlyHintAnnotation(true),
	)
}

func (s *Server) wikiScopeFromRequest(
	ctx context.Context, ep *types.MCPEndpoint, requested []string,
) ([]*types.KnowledgeBase, error) {
	kbs, err := s.selectKnowledgeBases(ctx, ep, requested)
	if err != nil {
		return nil, err
	}
	kbs = wikiKnowledgeBases(kbs)
	if len(kbs) == 0 {
		return nil, errNoWikiInScope
	}
	return kbs, nil
}

var errNoWikiInScope = &toolError{"none of the selected knowledge bases has a wiki; call list_knowledge_bases and " +
	"pick one whose capabilities include \"wiki\""}

type toolError struct{ msg string }

func (e *toolError) Error() string { return e.msg }

func (s *Server) handleWikiSearch(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ep, err := endpointFromContext(ctx)
	if err != nil {
		return mcp.NewToolResultError("unauthorized"), nil
	}
	query, err := req.RequireString("query")
	if err != nil || strings.TrimSpace(query) == "" {
		return mcp.NewToolResultError("query is required"), nil
	}
	kbs, err := s.wikiScopeFromRequest(ctx, ep, req.GetStringSlice("knowledge_base_ids", nil))
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	limit := req.GetInt("limit", defaultWikiSearchLimit)
	if limit < 1 {
		limit = defaultWikiSearchLimit
	}
	if limit > maxWikiSearchLimit {
		limit = maxWikiSearchLimit
	}
	tool := tools.NewWikiSearchTool(s.wikiService, s.knowledgeService, wikiScopesFor(kbs), tools.NewWikiRouteResolver())
	args, _ := json.Marshal(map[string]any{
		"queries": []string{strings.TrimSpace(query)},
		"limit":   limit,
	})
	res, execErr := tool.Execute(ctx, args)
	return toolResultFromAgentTool(res, execErr), nil
}

func (s *Server) handleWikiReadPage(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ep, err := endpointFromContext(ctx)
	if err != nil {
		return mcp.NewToolResultError("unauthorized"), nil
	}
	slug, err := req.RequireString("slug")
	if err != nil || strings.TrimSpace(slug) == "" {
		return mcp.NewToolResultError("slug is required"), nil
	}
	var requested []string
	if kbSel := strings.TrimSpace(req.GetString("knowledge_base_id", "")); kbSel != "" {
		requested = []string{kbSel}
	}
	kbs, err := s.wikiScopeFromRequest(ctx, ep, requested)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	tool := tools.NewWikiReadPageTool(
		s.wikiService, s.knowledgeService, wikiScopesFor(kbs), tools.NewWikiRouteResolver(),
	)
	args, _ := json.Marshal(map[string]any{"slug": strings.TrimSpace(slug)})
	res, execErr := tool.Execute(ctx, args)
	return toolResultFromAgentTool(res, execErr), nil
}

func (s *Server) handleWikiIndex(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ep, err := endpointFromContext(ctx)
	if err != nil {
		return mcp.NewToolResultError("unauthorized"), nil
	}
	selector, err := req.RequireString("knowledge_base_id")
	if err != nil {
		return mcp.NewToolResultError("knowledge_base_id is required"), nil
	}
	kbs, err := s.wikiScopeFromRequest(ctx, ep, []string{selector})
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	limit := req.GetInt("limit", defaultWikiIndexLimit)
	if limit < 1 {
		limit = defaultWikiIndexLimit
	}
	if limit > maxWikiIndexLimit {
		limit = maxWikiIndexLimit
	}
	view, err := s.wikiService.GetIndexView(ctx, kbs[0].ID, nil, limit, "")
	if err != nil {
		return mcp.NewToolResultErrorFromErr("failed to load wiki index", err), nil
	}
	return jsonResult(map[string]any{
		"knowledge_base_id": kbs[0].ID,
		"intro":             view.Intro,
		"groups":            view.Groups,
	})
}
