package agent

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/require"
)

func TestFormatToolHintKeepsMultiByteCharactersWhole(t *testing.T) {
	// 20 CJK characters are 60 bytes; a cut at byte 40 lands inside the 14th.
	query := strings.Repeat("中", 20)
	hint := formatToolHint("web_search", map[string]any{"query": query})

	require.True(t, utf8.ValidString(hint), "hint %q is not valid UTF-8", hint)
	require.True(t, strings.HasSuffix(hint, `…")`), hint)
	require.Contains(t, hint, strings.Repeat("中", 13)+"…")
}

func TestFormatToolHintLeavesShortAndASCIIArgumentsAlone(t *testing.T) {
	require.Contains(t, formatToolHint("web_search", map[string]any{"query": "中文"}), `("中文")`)
	long := strings.Repeat("a", 60)
	require.Contains(t, formatToolHint("web_search", map[string]any{"query": long}), `("`+strings.Repeat("a", 40)+`…")`)
}
