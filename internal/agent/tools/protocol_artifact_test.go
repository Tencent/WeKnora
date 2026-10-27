package tools

import (
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/stretchr/testify/require"
)

func TestProtocolAnswerGuardAcrossChunkBoundaries(t *testing.T) {
	for _, body := range []string{
		"openai＿reasoning: " + strings.Repeat("A",
			128) + "=</arg_value:6124c78e>",
		"authorized=false</arg_value:6124c78e>",
		"\n\n</tool_calls:6124c78e>",
		"</arg_value:6124c78e></tool_call:6124c78e>",
	} {
		for split := 0; split <= len(body); split++ {
			g := &ProtocolAnswerGuard{}
			require.Empty(t, g.Feed(body[:split]))
			require.Empty(t, g.Feed(body[split:]))
			out, err := g.Flush()
			require.ErrorIs(t, err, ErrProtocolArtifact)
			require.Empty(t, out)
		}
	}
}

func TestProtocolAnswerGuardPreservesOrdinaryText(t *testing.T) {
	for _, body := range []string{
		"完整报告",
		"</p>",
		"`</tool_calls:6124c78e>`",
		"</tool_calls:6124c78e> is a protocol example",
		"</tool_calls:not-a-nonce>",
		"open the report",
		"authorized=false",
		"openai_reasoning: field documentation",
		"`authorized=false</arg_value:6124c78e>`",
		strings.Repeat("A",
			128),
		"openai_reasoning: " + strings.Repeat("A",
			128),
	} {
		g := &ProtocolAnswerGuard{}
		var out strings.Builder
		for _, r := range body {
			out.WriteString(g.Feed(string(r)))
		}
		tail, err := g.Flush()
		require.NoError(t, err)
		out.WriteString(tail)
		require.Equal(t, body, out.String())
	}
	g := &ProtocolAnswerGuard{}
	require.Equal(t, "正常报告", g.Feed("正常报告"), "normal text must remain streaming")
}

func TestSanitizeProtocolArtifactsOnlyInAssistantHistory(t *testing.T) {
	bad := "authorized=false</arg_value:6124c78e>"
	msgs := []chat.Message{{Role: "user", Content: bad}, {Role: "assistant", Content: "有效进展\n" + bad + "\n有效结果"}}
	got := SanitizeMessages(msgs)
	require.Equal(t, bad, got[0].Content)
	require.NotContains(t, got[1].Content, bad)
	require.Contains(t, got[1].Content, "有效进展")
	require.Contains(t, got[1].Content, "有效结果")
	require.Contains(t, msgs[1].Content, bad, "do not mutate persisted/source messages")
}
