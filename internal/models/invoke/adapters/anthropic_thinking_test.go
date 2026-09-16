package adapters

// anthropic_thinking_test.go pins the extended-thinking surfacing: the
// adapter requests thinking via budget_tokens, so the response side must
// deliver it — thinking_delta frames bridge to StreamKindThinking and fold
// into ChatResponse.Thinking (2026-09-15: they were silently dropped and the
// UI never showed a thinking panel even when the vendor thought).

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/invoke"
	"github.com/stretchr/testify/require"
)

const anthropicThinkingSSE = "data: {\"type\":\"content_block_start\",\"index\":0," +
	"\"content_block\":{\"type\":\"thinking\"}}\n\n" +
	"data: {\"type\":\"content_block_delta\",\"index\":0," +
	"\"delta\":{\"type\":\"thinking_delta\",\"thinking\":\"step one \"}}\n\n" +
	"data: {\"type\":\"content_block_delta\",\"index\":0," +
	"\"delta\":{\"type\":\"thinking_delta\",\"thinking\":\"step two\"}}\n\n" +
	"data: {\"type\":\"content_block_delta\",\"index\":1," +
	"\"delta\":{\"type\":\"text_delta\",\"text\":\"answer\"}}\n\n" +
	"data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"}}\n\n" +
	"data: {\"type\":\"message_stop\"}\n\n"

func TestAnthropicThinkingAggregate(t *testing.T) {
	parsed, err := aggregateAnthropicSSE([]byte(anthropicThinkingSSE))
	require.NoError(t, err)
	require.NotNil(t, parsed.Thinking, "thinking must surface on the aggregate")
	require.Equal(t, "step one step two", *parsed.Thinking)
	require.Equal(t, "answer", parsed.Content)
}

func TestAnthropicThinkingBridge(t *testing.T) {
	adapter := &AnthropicAdapter{}
	state := invoke.NewStreamBridgeState()
	demux := invoke.NewDemuxer("text/event-stream", strings.NewReader(anthropicThinkingSSE))
	var thinking strings.Builder
	sawAnswer := false
	for {
		chunk, ok := demux.Next()
		if !ok {
			break
		}
		events, err := adapter.TranslateStreamEvent(state, chunk)
		require.NoError(t, err)
		for _, event := range events {
			switch {
			case event.Kind == invoke.StreamKindThinking && event.Delta != nil:
				thinking.WriteString(event.Delta.Text)
			case event.Kind == invoke.StreamKindAnswer && event.Delta != nil:
				sawAnswer = true
			}
		}
	}
	require.Equal(t, "step one step two", thinking.String())
	require.True(t, sawAnswer, "answer deltas must still flow alongside thinking")
}

func TestAnthropicNonStreamingThinking(t *testing.T) {
	var response anthropicResponse
	require.NoError(t, json.Unmarshal([]byte(
		`{"content":[{"type":"thinking","thinking":"reasoning here"},`+
			`{"type":"text","text":"answer"}],"stop_reason":"end_turn"}`), &response))
	parsed := parseAnthropicResponse(&response)
	require.NotNil(t, parsed.Thinking)
	require.Equal(t, "reasoning here", *parsed.Thinking)
	require.Equal(t, "answer", parsed.Content)
}
