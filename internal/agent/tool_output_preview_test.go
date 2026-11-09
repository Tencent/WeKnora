package agent

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/require"
)

func TestToolOutputPreviewKeepsMultiByteCharactersWhole(t *testing.T) {
	// Every character is three bytes, so a byte budget of 500 lands mid-character.
	output := strings.Repeat("知", 300)

	preview := toolOutputPreview(output)

	require.True(t, utf8.ValidString(preview))
	require.True(t, strings.HasSuffix(preview, "... (truncated)"))
	body := strings.TrimSuffix(preview, "... (truncated)")
	require.LessOrEqual(t, len(body), toolOutputPreviewBytes)
	require.NotEmpty(t, body)
	require.True(t, strings.HasPrefix(output, body))
}

func TestToolOutputPreviewLeavesShortOutputUntouched(t *testing.T) {
	output := "完成了三个步骤"

	require.Equal(t, output, toolOutputPreview(output))
}

func TestToolOutputPreviewCutsAsciiOnTheByteBudget(t *testing.T) {
	output := strings.Repeat("a", toolOutputPreviewBytes+40)

	require.Equal(
		t,
		strings.Repeat("a", toolOutputPreviewBytes)+"... (truncated)",
		toolOutputPreview(output),
	)
}
