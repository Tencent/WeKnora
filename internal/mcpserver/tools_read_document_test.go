package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// Keep the transport, endpoint guard, handler and shared reader real. Only
// storage is replaced, so these tests cover the contract external clients see.
type mcpReadChunkRepo struct {
	interfaces.ChunkRepository
	chunks []*types.Chunk
}

func (r *mcpReadChunkRepo) ListPagedChunksByKnowledgeID(
	_ context.Context, tenantID uint64, knowledgeID string, page *types.Pagination, _ []types.ChunkType,
	_ []string, _ string, _ string, _ string, _ string, _ *bool,
) ([]*types.Chunk, int64, error) {
	if tenantID != 1 || knowledgeID != "doc-1" {
		return nil, 0, fmt.Errorf("unexpected document scope: %d/%s", tenantID, knowledgeID)
	}
	start := min((page.Page-1)*page.PageSize, len(r.chunks))
	end := min(start+page.PageSize, len(r.chunks))
	return r.chunks[start:end], int64(len(r.chunks)), nil
}

func (r *mcpReadChunkRepo) ListChunksByParentIDs(context.Context, uint64, []string) ([]*types.Chunk, error) {
	return nil, nil
}

type mcpReadChunkService struct {
	interfaces.ChunkService
	repo *mcpReadChunkRepo
}

func (s *mcpReadChunkService) GetRepository() interfaces.ChunkRepository { return s.repo }

func newMCPReadDocumentEngine(t *testing.T, contents []string) *gin.Engine {
	t.Helper()
	kb := &types.KnowledgeBase{ID: "kb-1", TenantID: 1}
	doc := &types.Knowledge{ID: "doc-1", TenantID: 1, KnowledgeBaseID: kb.ID, Title: "Contract fixture"}
	repo := &mcpReadChunkRepo{}
	for i, content := range contents {
		repo.chunks = append(repo.chunks, &types.Chunk{
			ID: fmt.Sprintf("chunk-%d", i), TenantID: 1, KnowledgeID: doc.ID, KnowledgeBaseID: kb.ID,
			// Deliberately sparse: offsets are reading positions, not ChunkIndex values.
			ChunkIndex: 10 + i*3, ChunkType: types.ChunkTypeText, IsEnabled: true, Content: content,
		})
	}
	srv := NewServer(
		&stubKBService{kbs: map[string]*types.KnowledgeBase{kb.ID: kb}},
		&stubKnowledgeService{docs: map[string]*types.Knowledge{doc.ID: doc}},
		&mcpReadChunkService{repo: repo}, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil,
	)
	ep := &types.MCPEndpoint{
		ID: "ep-1", TenantID: 1, Enabled: true, KnowledgeBaseIDs: types.StringArray{kb.ID},
		Tools: types.StringArray{types.MCPEndpointToolReadDocument},
	}
	r := gin.New()
	r.POST("/mcp/:endpoint_id", func(c *gin.Context) {
		// Authentication is outside this contract test; inject an authenticated
		// endpoint with the same caller and API-key scope as production.
		c.Request = c.Request.WithContext(mcpCallContext(1, ep))
		c.Next()
	}, gin.WrapH(srv.Handler()))
	return r
}

type mcpReadDocumentData struct {
	KnowledgeID string `json:"knowledge_id"`
	MatchCount  int    `json:"match_count"`
	Truncated   bool   `json:"truncated"`
	NextOffset  *int   `json:"next_offset"`
	Chunks      []struct {
		ID    string `json:"chunk_id"`
		Index int    `json:"chunk_index"`
		Role  string `json:"role"`
	} `json:"chunks"`
}

func callMCPReadDocument(t *testing.T, r *gin.Engine, args map[string]any) mcpReadDocumentData {
	t.Helper()
	args["knowledge_id"] = "doc-1"
	response := rpc(t, r, "tools/call", map[string]any{
		"name": types.MCPEndpointToolReadDocument, "arguments": args,
	})
	require.NotContains(t, response, "error")
	raw, err := json.Marshal(response["result"])
	require.NoError(t, err)
	var result struct {
		IsError bool `json:"isError"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		Data mcpReadDocumentData `json:"structuredContent"`
	}
	require.NoError(t, json.Unmarshal(raw, &result))
	require.False(t, result.IsError, "%s", raw)
	require.Equal(t, "doc-1", result.Data.KnowledgeID)
	require.Len(t, result.Content, 1)
	require.Equal(t, "text", result.Content[0].Type)
	require.Contains(t, result.Content[0].Text, `knowledge_id="doc-1"`)
	for _, chunk := range result.Data.Chunks {
		require.Contains(t, result.Content[0].Text, `chunk_id="`+chunk.ID+`"`)
	}
	return result.Data
}

func mcpReadChunkIDs(data mcpReadDocumentData, role string) []string {
	ids := make([]string, 0, len(data.Chunks))
	for _, chunk := range data.Chunks {
		if role == "" || chunk.Role == role {
			ids = append(ids, chunk.ID)
		}
	}
	return ids
}

func TestMCPReadDocumentQueryMatchesLiteralTermsInAnyOrder(t *testing.T) {
	r := newMCPReadDocumentEngine(t, []string{
		"Before", "beta: build with C++ and ALPHA", "After", "alpha alone", "beta alone", "C only",
	})
	for _, query := range []string{"alpha beta", "  ALPHA\t\n beta  ", "lph et", "C++"} {
		t.Run(query, func(t *testing.T) {
			data := callMCPReadDocument(t, r, map[string]any{"query": query, "limit": 1})
			require.Equal(t, 1, data.MatchCount)
			require.Equal(t, []string{"chunk-1"}, mcpReadChunkIDs(data, "match"))
			require.Equal(t, []string{"chunk-0", "chunk-1", "chunk-2"}, mcpReadChunkIDs(data, ""))
			require.Equal(t, "context_before", data.Chunks[0].Role)
			require.Equal(t, "context_after", data.Chunks[2].Role)
			require.False(t, data.Truncated)
			require.Nil(t, data.NextOffset)
		})
	}
	data := callMCPReadDocument(t, r, map[string]any{"query": "absent"})
	require.Zero(t, data.MatchCount)
	require.Empty(t, data.Chunks)
	require.False(t, data.Truncated)
	require.Nil(t, data.NextOffset)
}

func TestMCPReadDocumentBlankQueryUsesSequentialPagination(t *testing.T) {
	r := newMCPReadDocumentEngine(t, []string{"zero", "one", "two", "three", "four"})
	for _, tc := range []struct {
		name  string
		query any
	}{
		{name: "omitted"},
		{name: "empty", query: ""},
		{name: "whitespace", query: " \t\n "},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args := map[string]any{"offset": 1, "limit": 2}
			if tc.query != nil {
				args["query"] = tc.query
			}
			first := callMCPReadDocument(t, r, args)
			require.Equal(t, []string{"chunk-1", "chunk-2"}, mcpReadChunkIDs(first, ""))
			require.Equal(t, 13, first.Chunks[0].Index)
			require.NotNil(t, first.NextOffset)
			require.Equal(t, 3, *first.NextOffset)
			args["offset"] = *first.NextOffset
			last := callMCPReadDocument(t, r, args)
			require.Equal(t, []string{"chunk-3", "chunk-4"}, mcpReadChunkIDs(last, ""))
			require.Nil(t, last.NextOffset)
		})
	}
}

func TestMCPReadDocumentQueryCapAndContinuationIgnoreLimit(t *testing.T) {
	contents := make([]string, 44)
	for i := range contents {
		contents[i] = "context"
		if i%2 == 1 {
			contents[i] = "needle"
		}
	}
	r := newMCPReadDocumentEngine(t, contents)
	first := callMCPReadDocument(t, r, map[string]any{"query": "needle", "limit": 1})
	require.Equal(t, 20, first.MatchCount)
	require.Len(t, first.Chunks, 41, "overlapping contexts are emitted only once per response")
	require.True(t, first.Truncated)
	require.NotNil(t, first.NextOffset)
	require.Equal(t, 41, *first.NextOffset, "resume at the first omitted match's reading position")
	last := callMCPReadDocument(t, r, map[string]any{
		"query": "needle", "limit": 1, "offset": *first.NextOffset,
	})
	require.Equal(t, 2, last.MatchCount)
	require.Equal(t, []string{"chunk-40", "chunk-41", "chunk-42", "chunk-43"}, mcpReadChunkIDs(last, ""))
	require.False(t, last.Truncated)
	require.Nil(t, last.NextOffset)
	want := make([]string, 0, 22)
	for i := 1; i < len(contents); i += 2 {
		want = append(want, fmt.Sprintf("chunk-%d", i))
	}
	require.Equal(t, want, append(mcpReadChunkIDs(first, "match"), mcpReadChunkIDs(last, "match")...))
}

func TestMCPReadDocumentOutputBudgetAllowsCompleteContinuation(t *testing.T) {
	// Exercise the actual default MCP budget, without injecting an Agent-only
	// context setting. These chunks exceed it well before the 20-match cap.
	contents := make([]string, 6)
	for i := range contents {
		contents[i] = "needle " + strings.Repeat("body ", 2000)
	}
	for _, query := range []string{"", "needle"} {
		t.Run(fmt.Sprintf("query=%q", query), func(t *testing.T) {
			r := newMCPReadDocumentEngine(t, contents)
			offset := 0
			var seen []string
			for page := 0; ; page++ {
				require.Less(t, page, len(contents), "continuation must terminate")
				data := callMCPReadDocument(t, r, map[string]any{"query": query, "offset": offset, "limit": 20})
				ids := mcpReadChunkIDs(data, "")
				if query != "" {
					ids = mcpReadChunkIDs(data, "match")
					require.Equal(t, len(ids), data.MatchCount)
				}
				require.NotEmpty(t, ids)
				seen = append(seen, ids...)
				if page == 0 {
					require.Less(t, len(ids), len(contents))
					require.NotNil(t, data.NextOffset)
				}
				if data.NextOffset == nil {
					require.False(t, data.Truncated)
					break
				}
				if query != "" {
					require.True(t, data.Truncated)
				}
				require.Greater(t, *data.NextOffset, offset)
				offset = *data.NextOffset
			}
			require.Equal(t, []string{"chunk-0", "chunk-1", "chunk-2", "chunk-3", "chunk-4", "chunk-5"}, seen)
		})
	}
}
