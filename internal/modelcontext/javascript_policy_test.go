package modelcontext

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestRunJavaScriptDecodesCodeHandlesAndCompactsResultIDs(t *testing.T) {
	registry := NewRegistry(false)
	handle := registry.RegisterDocument("doc-real-id")

	calls := []types.LLMToolCall{{
		Function: types.FunctionCall{
			Name:      "run_javascript",
			Arguments: fmt.Sprintf(`{"code":"return tools.read_document({id:'%s'});"}`, handle),
		},
	}}
	registry.DecodeToolCalls(calls)

	require.Contains(t, calls[0].Function.Arguments, "doc-real-id")
	require.NotContains(t, calls[0].Function.Arguments, "doc://1")

	output := registry.ModelToolResultForTool("run_javascript", &types.ToolResult{
		Success: true,
		Output:  `{"result":{"id":"doc-real-id"},"calls":[]}`,
	})
	require.Contains(t, output, "doc://1")
	require.NotContains(t, output, "doc-real-id")
}
