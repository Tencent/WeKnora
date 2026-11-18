package handler

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/require"
)

// firstProbeLine output lands in the sandbox deep-check panel detail
// (SandboxCheckItem.Message via describeProbeMismatch). Capping a probe's
// stderr line by bytes must not split a multi-byte rune, or the panel shows
// U+FFFD garbage for CJK container errors.
func TestFirstProbeLineKeepsValidUTF8(t *testing.T) {
	// 120 CJK runes = 360 bytes; the 300-byte cut lands inside rune 100.
	line := strings.Repeat("错", 120)
	got := firstProbeLine(line + "\nsecond line")
	require.True(t, utf8.ValidString(got), "probe preview must stay valid UTF-8, got %q", got)
	require.True(t, strings.HasSuffix(got, "…"), "expected truncation ellipsis: %q", got)

	// Mixed widths: 99 CJK (297 bytes) + two-byte runes; the cut lands
	// exactly on a two-byte rune boundary here — both boundary and mid-rune
	// shapes must stay valid.
	mixed := firstProbeLine(strings.Repeat("错", 99) + strings.Repeat("é", 51))
	require.True(t, utf8.ValidString(mixed), "probe preview must stay valid UTF-8, got %q", mixed)

	require.Equal(t, "short", firstProbeLine("  short  \n"), "short lines are trimmed, not cut")
	require.Empty(t, firstProbeLine("\n \n"), "blank output yields an empty preview")
}
