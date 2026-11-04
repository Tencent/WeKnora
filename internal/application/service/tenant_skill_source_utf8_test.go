package service

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/require"
)

// truncateSkillError caps a remote error body at 200 bytes by slicing the
// string. The body comes from a third-party host, so it can be Chinese; the cut
// then lands inside a multi-byte rune and the 400 response this produces carries
// an invalid trailing byte.
//
// The sibling helper for provider error previews already backs off to a rune
// boundary (readErrorPreview / trimPartialUTF8 in internal/infrastructure/
// web_search); this one is the remaining byte-slicing cap on a user-visible
// error message.
func TestTruncateSkillErrorKeepsValidUTF8(t *testing.T) {
	// 199 ASCII bytes put byte 200 inside the next three-byte rune.
	for _, msg := range []string{
		strings.Repeat("错", 100),
		strings.Repeat("x", 199) + strings.Repeat("失败", 50),
		strings.Repeat("x", 198) + strings.Repeat("失败", 50),
	} {
		got := truncateSkillError(msg)

		require.True(t, utf8.ValidString(got),
			"truncateSkillError: result is not valid UTF-8 for a %d-byte body", len(msg))
		require.LessOrEqual(t, len(got), 200, "truncateSkillError: result is %d bytes, want at most 200", len(got))
		require.True(t, strings.HasPrefix(msg, got), "truncateSkillError: result is not a prefix of the body")
	}
}

// Text that already fits, and whose whitespace collapses to something that
// still fits, must come back unchanged apart from the whitespace normalization.
func TestTruncateSkillErrorLeavesShortInputAlone(t *testing.T) {
	for _, msg := range []string{
		"",
		"ok",
		"下载失败",
		"line one\n\nline   two",
		strings.Repeat("错", 66), // 198 bytes: under the cap
	} {
		got := truncateSkillError(msg)
		want := strings.Join(strings.Fields(msg), " ")

		require.Equal(t, want, got)
	}
}
