package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Tencent/WeKnora/internal/agent/tools"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/mark3labs/mcp-go/mcp"
)

const (
	defaultListPageSize = 20
	maxListPageSize     = 100
	defaultChunkLimit   = 20
	maxChunkLimit       = 100
)

func listKnowledgeBasesTool() mcp.Tool {
	return mcp.NewTool(types.MCPEndpointToolListKnowledgeBases,
		mcp.WithDescription("List the knowledge bases this endpoint can access, with their id, name, description "+
			"and which retrieval modes (semantic, keyword, wiki) each supports. Call this first to learn what is in "+
			"scope; other tools accept either the id or the exact name."),
		mcp.WithReadOnlyHintAnnotation(true),
	)
}

func searchKnowledgeTool() mcp.Tool {
	return mcp.NewTool(types.MCPEndpointToolSearchKnowledge,
		mcp.WithDescription("Semantic search across the knowledge bases in scope. Returns the most relevant "+
			"passages with their document ids and titles. Use a natural-language question or topic; use grep_chunks "+
			"instead for exact keywords, codes or names."),
		mcp.WithString("query", mcp.Required(), mcp.Description("A natural-language question or topic to search for")),
		mcp.WithArray("knowledge_base_ids", mcp.WithStringItems(),
			mcp.Description("Optional knowledge base ids or names to restrict the search; defaults to every "+
				"knowledge base in scope")),
		mcp.WithReadOnlyHintAnnotation(true),
	)
}

func grepChunksTool() mcp.Tool {
	return mcp.NewTool(types.MCPEndpointToolGrepChunks,
		mcp.WithDescription("Case-insensitive regular-expression search over the raw text chunks of the knowledge "+
			"bases in scope. Best for exact terms, identifiers, error codes or product names that semantic search "+
			"may miss."),
		mcp.WithString("query", mcp.Required(), mcp.Description("Regular expression (POSIX, case-insensitive) to "+
			"match against chunk text")),
		mcp.WithArray("knowledge_base_ids", mcp.WithStringItems(),
			mcp.Description("Optional knowledge base ids or names to restrict the search")),
		mcp.WithReadOnlyHintAnnotation(true),
	)
}

func listDocumentsTool() mcp.Tool {
	return mcp.NewTool(types.MCPEndpointToolListDocuments,
		mcp.WithDescription("List the documents inside one knowledge base with pagination and an optional title "+
			"keyword filter. Returns document ids that read_document accepts."),
		mcp.WithString("knowledge_base_id", mcp.Required(), mcp.Description("Knowledge base id or exact name")),
		mcp.WithString("keyword", mcp.Description("Optional substring to filter document titles")),
		mcp.WithNumber("page", mcp.Description("1-based page number, default 1")),
		mcp.WithNumber("page_size", mcp.Description("Documents per page, default 20, max 100")),
		mcp.WithReadOnlyHintAnnotation(true),
	)
}

func readDocumentTool() mcp.Tool {
	return mcp.NewTool(types.MCPEndpointToolReadDocument,
		mcp.WithDescription("Read a document's metadata and its text chunks in order. Page through long documents "+
			"with offset and limit."),
		mcp.WithString("knowledge_id", mcp.Required(),
			mcp.Description("Document id from search results or list_documents")),
		mcp.WithNumber("offset", mcp.Description("Chunk offset to start from, default 0")),
		mcp.WithNumber("limit", mcp.Description("Number of chunks to return, default 20, max 100")),
		mcp.WithReadOnlyHintAnnotation(true),
	)
}

type knowledgeBaseSummary struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Description  string   `json:"description,omitempty"`
	Type         string   `json:"type"`
	Capabilities []string `json:"capabilities"`
}

func summarizeKnowledgeBase(kb *types.KnowledgeBase) knowledgeBaseSummary {
	caps := []string{}
	if kb.IsVectorEnabled() {
		caps = append(caps, "semantic")
	}
	if kb.IsKeywordEnabled() {
		caps = append(caps, "keyword")
	}
	if kb.IsWikiEnabled() {
		caps = append(caps, "wiki")
	}
	return knowledgeBaseSummary{
		ID:           kb.ID,
		Name:         kb.Name,
		Description:  kb.Description,
		Type:         kb.Type,
		Capabilities: caps,
	}
}

func (s *Server) handleListKnowledgeBases(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ep, err := endpointFromContext(ctx)
	if err != nil {
		return mcp.NewToolResultError("unauthorized"), nil
	}
	kbs, err := s.allowedKnowledgeBases(ctx, ep)
	if err != nil {
		return mcp.NewToolResultErrorFromErr("failed to list knowledge bases", err), nil
	}
	out := make([]knowledgeBaseSummary, 0, len(kbs))
	for _, kb := range kbs {
		out = append(out, summarizeKnowledgeBase(kb))
	}
	return jsonResult(map[string]any{"knowledge_bases": out, "total": len(out)})
}

func (s *Server) handleSearchKnowledge(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ep, err := endpointFromContext(ctx)
	if err != nil {
		return mcp.NewToolResultError("unauthorized"), nil
	}
	query, err := req.RequireString("query")
	if err != nil || strings.TrimSpace(query) == "" {
		return mcp.NewToolResultError("query is required"), nil
	}
	kbs, err := s.selectKnowledgeBases(ctx, ep, req.GetStringSlice("knowledge_base_ids", nil))
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	kbs = retrievableKnowledgeBases(kbs)
	if len(kbs) == 0 {
		return mcp.NewToolResultError(
			"none of the selected knowledge bases supports semantic or keyword retrieval"), nil
	}
	tool := tools.NewKnowledgeSearchTool(
		s.kbService, s.knowledgeService, s.chunkService, searchTargetsFor(kbs), nil, s.cfg,
	)
	args, _ := json.Marshal(map[string]any{
		"queries":            []string{strings.TrimSpace(query)},
		"knowledge_base_ids": knowledgeBaseIDs(kbs),
	})
	res, execErr := tool.Execute(ctx, args)
	return toolResultFromAgentTool(res, execErr), nil
}

func (s *Server) handleGrepChunks(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ep, err := endpointFromContext(ctx)
	if err != nil {
		return mcp.NewToolResultError("unauthorized"), nil
	}
	query, err := req.RequireString("query")
	if err != nil || strings.TrimSpace(query) == "" {
		return mcp.NewToolResultError("query is required"), nil
	}
	kbs, err := s.selectKnowledgeBases(ctx, ep, req.GetStringSlice("knowledge_base_ids", nil))
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	tool := tools.NewGrepChunksTool(s.db, searchTargetsFor(kbs))
	args, _ := json.Marshal(map[string]any{"query": strings.TrimSpace(query)})
	res, execErr := tool.Execute(ctx, args)
	return toolResultFromAgentTool(res, execErr), nil
}

type documentSummary struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	FileName    string `json:"file_name,omitempty"`
	FileType    string `json:"file_type,omitempty"`
	Source      string `json:"source,omitempty"`
	ParseStatus string `json:"parse_status"`
	Description string `json:"description,omitempty"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}

func summarizeKnowledge(k *types.Knowledge) documentSummary {
	return documentSummary{
		ID:          k.ID,
		Title:       k.Title,
		FileName:    k.FileName,
		FileType:    k.FileType,
		Source:      k.Source,
		ParseStatus: k.ParseStatus,
		Description: k.Description,
		CreatedAt:   k.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
		UpdatedAt:   k.UpdatedAt.Format("2006-01-02T15:04:05Z07:00"),
	}
}

func (s *Server) handleListDocuments(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
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
	// Documents live under the knowledge base owner; run the listing there.
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
	result, err := s.knowledgeService.ListPagedKnowledgeByKnowledgeBaseID(
		ctx, kb.ID,
		&types.Pagination{Page: page, PageSize: pageSize},
		types.KnowledgeListFilter{Keyword: strings.TrimSpace(req.GetString("keyword", ""))},
	)
	if err != nil {
		return mcp.NewToolResultErrorFromErr("failed to list documents", err), nil
	}
	docs := []documentSummary{}
	if rows, ok := result.Data.([]*types.Knowledge); ok {
		for _, k := range rows {
			docs = append(docs, summarizeKnowledge(k))
		}
	}
	return jsonResult(map[string]any{
		"knowledge_base_id": kb.ID,
		"total":             result.Total,
		"page":              result.Page,
		"page_size":         result.PageSize,
		"documents":         docs,
	})
}

func (s *Server) handleReadDocument(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ep, err := endpointFromContext(ctx)
	if err != nil {
		return mcp.NewToolResultError("unauthorized"), nil
	}
	knowledgeID, err := req.RequireString("knowledge_id")
	if err != nil {
		return mcp.NewToolResultError("knowledge_id is required"), nil
	}
	k, kb, err := s.knowledgeInScope(ctx, ep, knowledgeID)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	ctx, err = s.scopedKBContext(ctx, kb, types.OrgRoleViewer)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	limit := req.GetInt("limit", defaultChunkLimit)
	if limit < 1 {
		limit = defaultChunkLimit
	}
	if limit > maxChunkLimit {
		limit = maxChunkLimit
	}
	offset := req.GetInt("offset", 0)
	if offset < 0 {
		offset = 0
	}
	tool := tools.NewListKnowledgeChunksTool(
		s.knowledgeService, s.chunkService, searchTargetsFor([]*types.KnowledgeBase{kb}),
	)
	args, _ := json.Marshal(map[string]any{
		"knowledge_id": k.ID,
		"limit":        limit,
		"offset":       offset,
	})
	res, execErr := tool.Execute(ctx, args)
	if execErr != nil || res == nil || !res.Success {
		return toolResultFromAgentTool(res, execErr), nil
	}
	header := fmt.Sprintf("Document: %s\nID: %s\nKnowledge base: %s (%s)\nType: %s\nStatus: %s\n",
		k.Title, k.ID, kb.Name, kb.ID, k.FileType, k.ParseStatus)
	if k.Description != "" {
		header += "Summary: " + k.Description + "\n"
	}
	res.Output = header + "\n" + res.Output
	if res.Data == nil {
		res.Data = map[string]interface{}{}
	}
	res.Data["document"] = summarizeKnowledge(k)
	return toolResultFromAgentTool(res, nil), nil
}

// jsonResult renders structured data both as text (for clients that only
// read content) and as structuredContent.
func jsonResult(v any) (*mcp.CallToolResult, error) {
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return mcp.NewToolResultErrorFromErr("failed to encode result", err), nil
	}
	return mcp.NewToolResultStructured(v, string(raw)), nil
}
