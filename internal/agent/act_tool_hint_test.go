package agent

import (
	"strings"
	"testing"
	"unicode/utf8"

	agenttools "github.com/Tencent/WeKnora/internal/agent/tools"
	"github.com/stretchr/testify/require"
)

// The tool-call hint is rendered in the UI's tool-call chip. Truncating it by
// bytes splits multi-byte CJK arguments mid-rune and ships invalid UTF-8 in
// the EventAgentToolCall hint, so truncation must be rune-based — the same
// rule truncateForLangfuse already documents for Langfuse previews.
func TestFormatToolHintTruncatesByRunes(t *testing.T) {
	// 30 CJK runes (90 bytes): within the 40-rune budget, must survive intact.
	short := formatToolHint(agenttools.ToolSearchKnowledge,
		map[string]any{"query": strings.Repeat("数", 30)})
	require.True(t, utf8.ValidString(short), "hint must stay valid UTF-8, got %q", short)
	require.Equal(t, `检索知识库("`+strings.Repeat("数", 30)+`")`, short)

	// 60 CJK runes (180 bytes): truncated at the 40th rune, never mid-rune.
	long := formatToolHint(agenttools.ToolSearchKnowledge,
		map[string]any{"query": strings.Repeat("数", 60)})
	require.True(t, utf8.ValidString(long), "hint must stay valid UTF-8, got %q", long)
	require.Equal(t, `检索知识库("`+strings.Repeat("数", 40)+`…")`, long)

	// ASCII longer than the budget keeps its byte-oriented behavior.
	ascii := formatToolHint(agenttools.ToolSearchKnowledge,
		map[string]any{"query": strings.Repeat("a", 50)})
	require.Equal(t, `检索知识库("`+strings.Repeat("a", 40)+`…")`, ascii)
}
