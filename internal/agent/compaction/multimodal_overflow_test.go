package compaction

import (
	"strings"
	"testing"

	agenttools "github.com/Tencent/WeKnora/internal/agent/tools"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/stretchr/testify/require"
)

func TestOverflowAfterSanitizationPreservesPreviousSummary(t *testing.T) {
	const marker = "KEEP_PRIOR_USER_DECISION_7E93"
	live := reactTurn(40)
	summary := SummaryMessage(marker + "\n<modified-files>\n/workspace/prior.txt\n</modified-files>")
	image := chat.Message{Role: "user", MultiContent: []chat.MessageContentPart{
		{Type: "image_url", ImageURL: &chat.ImageURL{URL: "data:image/png;base64,AA=="}},
		{Type: "text", Text: "continue from this screenshot"},
	}}
	messages := append([]chat.Message{live[0], summary, image}, live[2:]...)
	sanitized := agenttools.SanitizeMessages(messages)
	require.Equal(t, chat.MessageKindCompactionSummary, sanitized[1].Kind)
	require.Empty(t, sanitized[1].Content, "exercise the multipart-only summary produced by sanitization")
	require.NotEmpty(t, sanitized[1].MultiContent)

	llm := &stubChat{response: "## Goal\ncontinue the task"}
	c := New(llm, newEstimator(t), testSettings())
	result, err := c.Compact(t.Context(), sanitized, ReasonOverflow)
	require.NoError(t, err)
	require.NotEmpty(t, llm.prompts)
	require.Contains(t, strings.Join(llm.prompts, "\n"), marker)
	require.Contains(t, strings.Join(llm.prompts, "\n"), "<previous-summary>")
	require.Contains(t, result.Summary, "/workspace/prior.txt")
	require.Equal(t, marker+"\n<modified-files>\n/workspace/prior.txt\n</modified-files>",
		unwrapSummary(messages[1].Content), "caller-owned messages must not be changed")
}

func TestCompactionSerializesMultipartTextWithoutLegacyDuplication(t *testing.T) {
	for _, role := range []string{"user", "assistant", "tool"} {
		t.Run(role, func(t *testing.T) {
			message := chat.Message{Role: role, Content: "stale legacy text", MultiContent: []chat.MessageContentPart{
				{Type: "text", Text: "keep first"},
				{Type: "image_url", ImageURL: &chat.ImageURL{URL: "https://example.com/image.png"}},
				{Type: "text", Text: " and second"},
			}}
			for _, rendered := range []string{serializeConversation([]chat.Message{message}), archiveLine(&message)} {
				require.Contains(t, rendered, "keep first and second")
				require.NotContains(t, rendered, "stale legacy text")
				require.NotContains(t, rendered, "https://example.com")
			}
		})
	}
}

func TestCompactionInheritsMultipartFileOperations(t *testing.T) {
	message := chat.Message{Role: "user", MultiContent: []chat.MessageContentPart{
		{Type: "text", Text: "<modified-files>\n/workspace/kept.txt\n</modified-files>"},
	}}
	ops := extractFileOps("", []chat.Message{message})
	_, modified := ops.resolve()
	require.Equal(t, []string{"/workspace/kept.txt"}, modified)
}
