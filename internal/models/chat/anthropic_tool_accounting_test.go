package chat

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAnthropicToolUsagePreservesCacheBucketsAndMissingUsage(t *testing.T) {
	const tool = `data: {"type":"content_block_start","index":0,` +
		`"content_block":{"type":"tool_use","id":"call-1","name":"lookup","input":{"key":"value"}}}

data: {"type":"content_block_stop","index":0}

data: {"type":"message_delta","delta":{"stop_reason":"tool_use"}}

data: {"type":"message_stop"}

`
	for _, withUsage := range []bool{false, true} {
		body := tool
		if withUsage {
			body = `data: {"type":"message_start","message":{"usage":{"input_tokens":5,"output_tokens":2,` +
				`"cache_read_input_tokens":100,"cache_creation_input_tokens":50,` +
				`"cache_creation":{"ephemeral_5m_input_tokens":20,"ephemeral_1h_input_tokens":30}}}}

` + body
		}
		response, err := parseAnthropicSSE(strings.NewReader(body))
		require.NoError(t, err)
		require.Equal(t, "tool_use", response.FinishReason)
		require.Len(t, response.ToolCalls, 1)
		require.JSONEq(t, `{"key":"value"}`, response.ToolCalls[0].Function.Arguments)
		require.Equal(t, withUsage, response.Usage.UsageReported)
		if withUsage {
			require.Equal(t, 155, response.Usage.PromptTokens)
			require.Equal(t, 157, response.Usage.TotalTokens)
			require.Equal(t, 100, response.Usage.CacheReadTokens)
			require.Equal(t, 20, *response.Usage.CacheWrite5mTokens)
			require.Equal(t, 30, *response.Usage.CacheWrite1hTokens)
		}
	}
}
