package mcpserver

import (
	"fmt"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestMCPSearchRejectsMalformedKnowledgeBaseSelectors(t *testing.T) {
	for _, name := range []string{
		types.MCPEndpointToolSearchKnowledge, types.MCPEndpointToolGrepChunks,
		types.MCPEndpointToolWikiSearch, types.MCPEndpointToolAsk,
	} {
		for _, selector := range []any{
			"kb-own", nil, []any{123}, []any{"kb-own", 123}, []any{" "}, []any{"kb-own", ""},
		} {
			t.Run(fmt.Sprintf("%s/%v", name, selector), func(t *testing.T) {
				_, r, kbService, ep := newDocumentSearchFixture(t)
				ep.Tools = append(ep.Tools, types.MCPEndpointToolWikiSearch, types.MCPEndpointToolAsk)
				response := rpc(t, r, "tools/call", map[string]any{
					"name": name,
					"arguments": map[string]any{
						"query": "contract", "question": "contract", "knowledge_base_ids": selector,
					},
				})
				result, ok := response["result"].(map[string]any)
				require.True(t, ok, "%v", response)
				require.Equal(t, true, result["isError"], "%v", response)
				require.Empty(t, kbService.calls, "invalid selectors must not start retrieval")
			})
		}
	}
}

func TestMCPSearchValidKnowledgeBaseSelectors(t *testing.T) {
	for _, tc := range []struct {
		name      string
		selectors any
		include   bool
		want      []string
	}{
		{name: "omitted", want: []string{"kb-own", "kb-other", "kb-shared"}},
		{name: "empty", include: true, selectors: []string{}, want: []string{"kb-own", "kb-other", "kb-shared"}},
		{
			name: "id and name", include: true,
			selectors: []string{"kb-own", "Other"}, want: []string{"kb-own", "kb-other"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, r, _, _ := newDocumentSearchFixture(t)
			args := map[string]any{"query": "contract"}
			if tc.include {
				args["knowledge_base_ids"] = tc.selectors
			}
			response := rpc(t, r, "tools/call", map[string]any{
				"name": types.MCPEndpointToolSearchKnowledge, "arguments": args,
			})
			result := response["result"].(map[string]any)
			require.NotEqual(t, true, result["isError"], "%v", response)
			data := result["structuredContent"].(map[string]any)
			require.Equal(t, tc.want, stringsFromAnySlice(t, data["knowledge_base_ids"]))
		})
	}
}

func stringsFromAnySlice(t *testing.T, value any) []string {
	t.Helper()
	items := value.([]any)
	result := make([]string, 0, len(items))
	for _, item := range items {
		result = append(result, item.(string))
	}
	return result
}
