package mcpserver

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/require"
)

type stubTagService struct {
	interfaces.KnowledgeTagService
	kbID    string
	page    *types.Pagination
	keyword string
	result  *types.PageResult
	err     error
}

func (s *stubTagService) ListTags(
	_ context.Context, kbID string, page *types.Pagination, keyword string,
) (*types.PageResult, error) {
	s.kbID = kbID
	s.page = page
	s.keyword = keyword
	if s.err != nil {
		return nil, s.err
	}
	return s.result, nil
}

func TestListTagsToolSchema(t *testing.T) {
	tool := listTagsTool()
	require.Equal(t, types.MCPEndpointToolListTags, tool.Name)
	propJSON, err := json.Marshal(tool.InputSchema.Properties["knowledge_base_id"])
	require.NoError(t, err)
	require.Contains(t, string(propJSON), `"type":"string"`)
	require.Contains(t, tool.InputSchema.Required, "knowledge_base_id")

	ann := tool.Annotations
	require.NotNil(t, ann.ReadOnlyHint)
	require.True(t, *ann.ReadOnlyHint)
	require.NotNil(t, ann.DestructiveHint)
	require.False(t, *ann.DestructiveHint)
}

func TestListTagsCatalogEntry(t *testing.T) {
	def, ok := types.LookupMCPEndpointTool(types.MCPEndpointToolListTags)
	require.True(t, ok, "list_tags must be in the endpoint tool catalog")
	require.Equal(t, types.MCPEndpointToolGroupRetrieve, def.Group)
	require.False(t, def.Destructive, "list_tags is read-only and belongs in default endpoint tools")
	require.Contains(t, types.DefaultMCPEndpointTools(), types.MCPEndpointToolListTags)
}

func TestHandleListTagsResolvesScopeAndPaginates(t *testing.T) {
	kb := &types.KnowledgeBase{ID: "kb-1", TenantID: 1, Name: "Product Docs"}
	srv := newScopeTestServer(kb)
	srv.kbShareService = &stubKBShareService{}
	tagRows := []*types.KnowledgeTagWithStats{
		{
			KnowledgeTag:   types.KnowledgeTag{ID: "tag-1", Name: "华东", Color: "#fff"},
			KnowledgeCount: 3, ChunkCount: 12,
		},
		{KnowledgeTag: types.KnowledgeTag{ID: "tag-2", Name: "华南"}},
	}
	tags := &stubTagService{result: types.NewPageResult(2, &types.Pagination{Page: 1, PageSize: 20}, tagRows)}
	srv.tagService = tags
	ep := &types.MCPEndpoint{ID: "ep", TenantID: 1}
	ctx := mcpCallContext(1, ep)

	result, err := srv.handleListTags(ctx, mcp.CallToolRequest{Params: mcp.CallToolParams{
		Arguments: map[string]any{
			"knowledge_base_id": "product docs",
			"keyword":           "华",
			"page":              2,
			"page_size":         500, // clamped to maxListPageSize
		},
	}})
	require.NoError(t, err)
	require.False(t, result.IsError, "%#v", result.Content)

	require.Equal(t, "kb-1", tags.kbID, "name selector must resolve to the KB id")
	require.Equal(t, "华", tags.keyword)
	require.Equal(t, 2, tags.page.Page)
	require.Equal(t, maxListPageSize, tags.page.PageSize)

	raw, err := json.Marshal(result.StructuredContent)
	require.NoError(t, err)
	var parsed struct {
		KnowledgeBaseID string       `json:"knowledge_base_id"`
		Total           int64        `json:"total"`
		Tags            []tagSummary `json:"tags"`
	}
	require.NoError(t, json.Unmarshal(raw, &parsed))
	require.Equal(t, "kb-1", parsed.KnowledgeBaseID)
	require.Equal(t, int64(2), parsed.Total)
	require.Len(t, parsed.Tags, 2)
	require.Equal(t, "tag-1", parsed.Tags[0].ID)
	require.Equal(t, int64(12), parsed.Tags[0].ChunkCount)
}

func TestHandleListTagsRejectsOutOfScopeKB(t *testing.T) {
	srv := newScopeTestServer(
		&types.KnowledgeBase{ID: "kb-1", TenantID: 1, Name: "Product Docs"},
		&types.KnowledgeBase{ID: "kb-2", TenantID: 2, Name: "Foreign"},
	)
	srv.tagService = &stubTagService{}
	ep := &types.MCPEndpoint{ID: "ep", TenantID: 1}
	ctx := mcpCallContext(1, ep)

	result, err := srv.handleListTags(ctx, mcp.CallToolRequest{Params: mcp.CallToolParams{
		Arguments: map[string]any{"knowledge_base_id": "kb-2"},
	}})
	require.NoError(t, err)
	require.True(t, result.IsError)
	require.Contains(t, result.Content[0].(mcp.TextContent).Text, "outside this endpoint's scope")
}

func askReq(tagScopes any) mcp.CallToolRequest {
	args := map[string]any{"question": "q"}
	if tagScopes != nil {
		args["tag_scopes"] = tagScopes
	}
	return mcp.CallToolRequest{Params: mcp.CallToolParams{Arguments: args}}
}

func TestResolveAskTagScopes(t *testing.T) {
	srv := newScopeTestServer(
		&types.KnowledgeBase{ID: "kb-1", TenantID: 1, Name: "Product Docs"},
		&types.KnowledgeBase{ID: "kb-2", TenantID: 1, Name: "Support"},
	)
	ep := &types.MCPEndpoint{ID: "ep", TenantID: 1}
	ctx := mcpCallContext(1, ep)

	// Absent argument: no scopes, no error.
	scopes, err := srv.resolveAskTagScopes(ctx, ep, askReq(nil))
	require.NoError(t, err)
	require.Empty(t, scopes)

	// Valid scopes: KB names resolve to ids, empty tag ids are dropped.
	scopes, err = srv.resolveAskTagScopes(ctx, ep, askReq([]any{
		map[string]any{"knowledge_base_id": "product docs", "tag_ids": []any{"tag-1", " ", ""}},
		map[string]any{"knowledge_base_id": "kb-2", "tag_ids": []any{"tag-9"}},
	}))
	require.NoError(t, err)
	require.Len(t, scopes, 2)
	require.Equal(t, types.TagScope{KnowledgeBaseID: "kb-1", TagIDs: []string{"tag-1"}}, scopes[0])
	require.Equal(t, types.TagScope{KnowledgeBaseID: "kb-2", TagIDs: []string{"tag-9"}}, scopes[1])

	// Missing knowledge_base_id.
	_, err = srv.resolveAskTagScopes(ctx, ep, askReq([]any{map[string]any{"tag_ids": []any{"tag-1"}}}))
	require.ErrorContains(t, err, "knowledge_base_id")

	// Scope without tag ids points at list_tags.
	_, err = srv.resolveAskTagScopes(ctx, ep, askReq([]any{
		map[string]any{"knowledge_base_id": "kb-1", "tag_ids": []any{""}},
	}))
	require.ErrorContains(t, err, "list_tags")

	// Foreign knowledge base.
	_, err = srv.resolveAskTagScopes(ctx, ep, askReq([]any{
		map[string]any{"knowledge_base_id": "nope", "tag_ids": []any{"tag-1"}},
	}))
	require.ErrorContains(t, err, "outside this endpoint's scope")

	// Wrong shape.
	_, err = srv.resolveAskTagScopes(ctx, ep, askReq([]any{"not-an-object"}))
	require.ErrorContains(t, err, "tag_scopes must be an array")
}

func TestAskToolDeclaresTagScopes(t *testing.T) {
	tool := askTool()
	prop, ok := tool.InputSchema.Properties["tag_scopes"]
	require.True(t, ok, "ask must declare tag_scopes")
	propJSON, err := json.Marshal(prop)
	require.NoError(t, err)
	require.Contains(t, string(propJSON), `"type":"array"`)
}
