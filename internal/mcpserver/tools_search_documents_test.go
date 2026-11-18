package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"sync"
	"testing"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/require"
)

type documentSearchCall struct {
	kbID   string
	params types.SearchParams
}

type documentSearchKBService struct {
	*stubKBService
	mu    sync.Mutex
	calls []documentSearchCall
	rows  []*types.SearchResult
}

func (s *documentSearchKBService) GetKnowledgeBasesByIDsOnly(
	_ context.Context, ids []string,
) ([]*types.KnowledgeBase, error) {
	var kbs []*types.KnowledgeBase
	for _, id := range ids {
		kbs = append(kbs, s.kbs[id])
	}
	return kbs, nil
}

func (s *documentSearchKBService) ResolveEmbeddingModelKeys(
	_ context.Context, kbs []*types.KnowledgeBase,
) map[string]string {
	keys := make(map[string]string, len(kbs))
	for _, kb := range kbs {
		keys[kb.ID] = "fixture-model"
	}
	return keys
}

func (s *documentSearchKBService) GetQueryEmbedding(context.Context, string, string) ([]float32, error) {
	return []float32{1, 0}, nil
}

func (s *documentSearchKBService) HybridSearch(
	_ context.Context, kbID string, params types.SearchParams,
) ([]*types.SearchResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, documentSearchCall{kbID: kbID, params: params})
	var rows []*types.SearchResult
	for _, row := range s.rows {
		inKB := row.KnowledgeBaseID == kbID
		if len(params.KnowledgeBaseIDs) > 0 {
			inKB = slices.Contains(params.KnowledgeBaseIDs, row.KnowledgeBaseID)
		}
		passesThreshold := (!params.DisableVectorMatch && row.Score >= params.VectorThreshold) ||
			(!params.DisableKeywordsMatch && row.Score >= params.KeywordThreshold)
		inDocumentScope := len(params.KnowledgeIDs) == 0 || slices.Contains(params.KnowledgeIDs, row.KnowledgeID)
		if inKB && passesThreshold && inDocumentScope {
			rows = append(rows, row)
		}
	}
	// Model the storage boundary: document filtering must precede candidate truncation.
	return rows[:min(len(rows), params.MatchCount)], nil
}

func newDocumentSearchFixture(t *testing.T) (*Server, *gin.Engine, *documentSearchKBService, *types.MCPEndpoint) {
	t.Helper()
	kbs := map[string]*types.KnowledgeBase{}
	for _, kb := range []*types.KnowledgeBase{
		{ID: "kb-own", Name: "Own", TenantID: 1},
		{ID: "kb-other", Name: "Other", TenantID: 1},
		{ID: "kb-shared", Name: "Shared", TenantID: 2},
		{ID: "kb-foreign", TenantID: 3},
		{ID: "kb-wiki", TenantID: 1},
	} {
		kb.IndexingStrategy.VectorEnabled = kb.ID != "kb-wiki"
		kb.IndexingStrategy.KeywordEnabled = kb.ID != "kb-wiki"
		kbs[kb.ID] = kb
	}
	docs := map[string]*types.Knowledge{}
	for id, kbID := range map[string]string{
		"doc-a": "kb-own", "doc-b": "kb-own", "doc-other": "kb-other",
		"doc-shared": "kb-shared", "doc-foreign": "kb-foreign", "doc-wiki": "kb-wiki",
	} {
		docs[id] = &types.Knowledge{ID: id, KnowledgeBaseID: kbID, TenantID: kbs[kbID].TenantID}
	}
	docs["doc-spoof"] = &types.Knowledge{ID: "doc-spoof", KnowledgeBaseID: "kb-shared", TenantID: 1}
	kbService := &documentSearchKBService{stubKBService: &stubKBService{kbs: kbs}}
	// A newer, higher-ranked same-topic document must not crowd doc-a out of the result limit.
	for i, id := range []string{"doc-b", "doc-a", "doc-other", "doc-shared"} {
		kbService.rows = append(kbService.rows, &types.SearchResult{
			ID: "chunk-" + id, KnowledgeID: id, KnowledgeBaseID: docs[id].KnowledgeBaseID,
			KnowledgeTitle: id, Content: id + " contract price", Score: 1 - float64(i)*0.1,
			ChunkType: string(types.ChunkTypeText),
		})
	}
	srv := NewServer(kbService, &stubKnowledgeService{docs: docs}, nil, nil, nil, nil, nil,
		&stubKBShareService{shared: map[string]types.OrgMemberRole{"kb-shared": types.OrgRoleViewer}},
		nil, nil, nil, &config.Config{Conversation: &config.ConversationConfig{
			EmbeddingTopK: 1, VectorThreshold: 0.95, KeywordThreshold: 0.95,
		}},
		nil, nil, nil, nil)
	ep := &types.MCPEndpoint{
		ID: "ep-1", TenantID: 1, Enabled: true,
		KnowledgeBaseIDs: types.StringArray{"kb-own", "kb-other", "kb-shared", "kb-wiki"},
		Tools:            types.StringArray{types.MCPEndpointToolSearchKnowledge, types.MCPEndpointToolGrepChunks},
	}
	r := gin.New()
	r.POST("/mcp/:endpoint_id", func(c *gin.Context) {
		c.Request = c.Request.WithContext(mcpCallContext(1, ep))
		c.Next()
	}, gin.WrapH(srv.Handler()))
	return srv, r, kbService, ep
}

func TestMCPSearchKnowledgeDocumentScopeBeforeRetrieval(t *testing.T) {
	for _, mode := range []string{"hybrid", "semantic", "keyword"} {
		t.Run(mode, func(t *testing.T) {
			_, r, kbService, _ := newDocumentSearchFixture(t)
			response := rpc(t, r, "tools/call", map[string]any{
				"name": types.MCPEndpointToolSearchKnowledge,
				"arguments": map[string]any{
					"query": "contract price", "mode": mode, "limit": 1,
					"knowledge_ids": []string{" doc-a ", "doc-a"},
				},
			})
			require.NotContains(t, response, "error")
			raw, err := json.Marshal(response["result"])
			require.NoError(t, err)
			var result struct {
				IsError bool                    `json:"isError"`
				Content []struct{ Text string } `json:"content"`
				Data    struct {
					Results []struct {
						KnowledgeID string `json:"knowledge_id"`
					} `json:"results"`
				} `json:"structuredContent"`
			}
			require.NoError(t, json.Unmarshal(raw, &result))
			require.False(t, result.IsError, "%s", raw)
			require.Len(t, result.Data.Results, 1)
			require.Equal(t, "doc-a", result.Data.Results[0].KnowledgeID)
			require.Contains(t, result.Content[0].Text, `knowledge_id="doc-a"`)
			require.NotContains(t, result.Content[0].Text, `knowledge_id="doc-b"`)
			require.Len(t, kbService.calls, 1)
			call := kbService.calls[0]
			require.Equal(t, "kb-own", call.kbID)
			require.Equal(t, []string{"doc-a"}, call.params.KnowledgeIDs)
			require.Empty(t, call.params.KnowledgeBaseIDs)
			require.Equal(t, 1, call.params.MatchCount)
			require.Equal(t, mode == "keyword", call.params.DisableVectorMatch)
			require.Equal(t, mode == "semantic", call.params.DisableKeywordsMatch)
			require.Zero(t, call.params.VectorThreshold)
			require.Zero(t, call.params.KeywordThreshold)
		})
	}
}

func TestMCPSearchKnowledgeMultipleDocumentsAndSharedScope(t *testing.T) {
	_, r, kbService, _ := newDocumentSearchFixture(t)
	response := rpc(t, r, "tools/call", map[string]any{
		"name": types.MCPEndpointToolSearchKnowledge,
		"arguments": map[string]any{
			"query": "contract", "knowledge_base_ids": []string{"Own", "Shared", "Other"},
			"knowledge_ids": []string{"doc-a", "doc-b", "doc-shared", "doc-a"},
		},
	})
	require.NotContains(t, response, "error")
	result := response["result"].(map[string]any)
	require.NotEqual(t, true, result["isError"], "%+v", result)
	var ids []string
	for _, raw := range result["structuredContent"].(map[string]any)["results"].([]any) {
		ids = append(ids, raw.(map[string]any)["knowledge_id"].(string))
	}
	require.ElementsMatch(t, []string{"doc-a", "doc-b", "doc-shared"}, ids)
	require.Len(t, kbService.calls, 2)
	byKB := map[string][]string{}
	for _, call := range kbService.calls {
		byKB[call.kbID] = call.params.KnowledgeIDs
	}
	require.Equal(t, map[string][]string{
		"kb-own": {"doc-a", "doc-b"}, "kb-shared": {"doc-shared"},
	}, byKB)
}

func TestMCPSearchKnowledgeRejectsInvalidDocumentScope(t *testing.T) {
	for _, tc := range []struct {
		name string
		ids  any
		kbs  []string
	}{
		{name: "missing document", ids: []string{"missing"}},
		{name: "foreign document", ids: []string{"doc-foreign"}},
		{name: "tenant mismatch", ids: []string{"doc-spoof"}},
		{name: "empty intersection", ids: []string{"doc-a"}, kbs: []string{"Shared"}},
		{name: "partial conflict", ids: []string{"doc-a", "doc-shared"}, kbs: []string{"Own"}},
		{name: "non-retrievable document", ids: []string{"doc-wiki"}},
		{name: "empty array", ids: []string{}},
		{name: "null", ids: nil},
		{name: "string", ids: "doc-a"},
		{name: "mixed types", ids: []any{"doc-a", 42}},
		{name: "null entry", ids: []any{"doc-a", nil}},
		{name: "blank entry", ids: []string{"doc-a", " "}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, _, kbService, ep := newDocumentSearchFixture(t)
			args := map[string]any{"query": "contract", "knowledge_ids": tc.ids}
			if tc.kbs != nil {
				args["knowledge_base_ids"] = tc.kbs
			}
			result, err := srv.handleSearchKnowledge(mcpCallContext(1, ep), mcp.CallToolRequest{
				Params: mcp.CallToolParams{Arguments: args},
			})
			require.NoError(t, err)
			require.True(t, result.IsError, "%+v", result)
			require.Empty(t, kbService.calls, "invalid scope must never fall back to whole-KB retrieval")
		})
	}
}

func TestMCPSearchKnowledgeOmittedDocumentScopeAndGrepKeepWholeKBTargets(t *testing.T) {
	for _, tool := range []string{types.MCPEndpointToolSearchKnowledge, types.MCPEndpointToolGrepChunks} {
		t.Run(tool, func(t *testing.T) {
			_, r, kbService, _ := newDocumentSearchFixture(t)
			response := rpc(t, r, "tools/call", map[string]any{
				"name": tool, "arguments": map[string]any{
					"query": "contract", "knowledge_base_ids": []string{"Own"},
				},
			})
			require.NotContains(t, response, "error")
			require.Len(t, kbService.calls, 1)
			require.Empty(t, kbService.calls[0].params.KnowledgeIDs)
			require.Equal(t, []string{"kb-own"}, kbService.calls[0].params.KnowledgeBaseIDs)
			require.Equal(t, 0.95, kbService.calls[0].params.VectorThreshold)
			require.Equal(t, 0.95, kbService.calls[0].params.KeywordThreshold)
		})
	}
}

func TestMCPSearchKnowledgeDocumentScopeSchema(t *testing.T) {
	_, r, _, _ := newDocumentSearchFixture(t)
	response := rpc(t, r, "tools/list", map[string]any{})
	result := response["result"].(map[string]any)
	for _, raw := range result["tools"].([]any) {
		tool := raw.(map[string]any)
		if tool["name"] != types.MCPEndpointToolSearchKnowledge {
			continue
		}
		schema := tool["inputSchema"].(map[string]any)
		property := schema["properties"].(map[string]any)["knowledge_ids"].(map[string]any)
		require.Equal(t, "array", property["type"])
		require.EqualValues(t, 1, property["minItems"])
		require.EqualValues(t, 100, property["maxItems"])
		require.Equal(t, "string", property["items"].(map[string]any)["type"])
		require.NotContains(t, schema["required"], "knowledge_ids")
		return
	}
	t.Fatal("search_knowledge is missing from tools/list")
}

func TestMCPSearchKnowledgeRechecksEndpointAndShareRestrictions(t *testing.T) {
	for _, tc := range []struct {
		name   string
		shared bool
	}{
		{name: "endpoint restriction"},
		{name: "revoked share", shared: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, r, kbService, ep := newDocumentSearchFixture(t)
			id := "doc-other"
			if tc.shared {
				id = "doc-shared"
				srv.kbShareService = &stubKBShareService{shared: map[string]types.OrgMemberRole{}}
			} else {
				ep.KnowledgeBaseIDs = types.StringArray{"kb-own"}
			}
			response := rpc(t, r, "tools/call", map[string]any{
				"name":      types.MCPEndpointToolSearchKnowledge,
				"arguments": map[string]any{"query": "contract", "knowledge_ids": []string{id}},
			})
			require.NotContains(t, response, "error")
			require.Equal(t, true, response["result"].(map[string]any)["isError"])
			require.Empty(t, kbService.calls)
		})
	}
}

// Count lookups to ensure oversized requests are rejected before any document I/O.
type countingDocumentService struct {
	*stubKnowledgeService
	lookups int
}

func (s *countingDocumentService) GetKnowledgeByIDOnly(ctx context.Context, id string) (*types.Knowledge, error) {
	s.lookups++
	return s.stubKnowledgeService.GetKnowledgeByIDOnly(ctx, id)
}

func TestMCPSearchKnowledgeDocumentScopeSizeLimit(t *testing.T) {
	for _, count := range []int{100, 101} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			srv, _, kbService, ep := newDocumentSearchFixture(t)
			service := &countingDocumentService{stubKnowledgeService: srv.knowledgeService.(*stubKnowledgeService)}
			srv.knowledgeService = service
			ids := make([]string, count)
			for i := range ids {
				ids[i] = fmt.Sprintf("selected-%d", i)
				service.docs[ids[i]] = &types.Knowledge{ID: ids[i], KnowledgeBaseID: "kb-own", TenantID: 1}
			}
			result, err := srv.handleSearchKnowledge(mcpCallContext(1, ep), mcp.CallToolRequest{
				Params: mcp.CallToolParams{Arguments: map[string]any{"query": "contract", "knowledge_ids": ids}},
			})
			require.NoError(t, err)
			if count == 100 {
				require.False(t, result.IsError)
				require.Equal(t, count, service.lookups)
				require.Len(t, kbService.calls, 1)
				require.Len(t, kbService.calls[0].params.KnowledgeIDs, count)
			} else {
				require.True(t, result.IsError)
				require.Zero(t, service.lookups)
				require.Empty(t, kbService.calls)
			}
		})
	}
}
