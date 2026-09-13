package memory

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func assistantMessage(sessionID, content string, at time.Time) *types.Message {
	return &types.Message{
		ID:        content,
		SessionID: sessionID,
		Role:      "assistant",
		Content:   content,
		CreatedAt: at,
	}
}

// The tiers decide who gets the budget; they must not decide the order. A
// transcript printed trust-first is no longer a transcript: "太细了" before the
// answer it rejects reads as a complaint about nothing.
func TestEvidenceIsPrintedInTheOrderItHappened(t *testing.T) {
	base := time.Now().Add(-time.Hour)
	segment := transcriptSegment{lines: []transcriptLine{
		{tier: tierUser, at: base, content: "帮我把季度汇报整理成大纲"},
		{tier: tierAssistant, at: base.Add(time.Minute), content: "好的，我先列出十页的框架"},
		{tier: tierCitation, at: base.Add(time.Minute), content: "季度经营分析模板"},
		{tier: tierUser, at: base.Add(2 * time.Minute), content: "太细了，这份只要三页"},
	}}

	rendered := segment.render(100000)
	positions := make([]int, 0, 4)
	for _, fragment := range []string{
		"帮我把季度汇报整理成大纲", "十页的框架", "季度经营分析模板", "太细了",
	} {
		at := strings.Index(rendered, fragment)
		require.GreaterOrEqual(t, at, 0, "%q was dropped from a segment that fits the budget", fragment)
		positions = append(positions, at)
	}
	require.IsIncreasing(t, positions, "rows must appear in chronological order, not in tier order")

	require.Contains(t, rendered, "[1] ")
	require.Contains(t, rendered, "[4] ")
	require.Contains(t, rendered, "[user] 太细了，这份只要三页")
	require.Contains(t, rendered, "[assistant] 好的，我先列出十页的框架")
	require.Contains(t, rendered, "[cited documents] 季度经营分析模板")
}

// When the budget runs out, what survives is the user's words. The whole point
// of tiering rather than truncating chronologically is that an answer is long
// and mostly restates the question, so spending the budget on it is how the
// only irreplaceable rows get pushed out.
func TestBudgetIsSpentOnTheUsersWordsFirst(t *testing.T) {
	base := time.Now().Add(-time.Hour)
	var lines []transcriptLine
	for i := 0; i < 12; i++ {
		lines = append(lines,
			transcriptLine{
				tier:    tierUser,
				at:      base.Add(time.Duration(i) * time.Minute),
				content: fmt.Sprintf("用户第%d句，说的是偏好", i),
			},
			transcriptLine{
				tier:    tierAssistant,
				at:      base.Add(time.Duration(i)*time.Minute + time.Second),
				content: strings.Repeat("这是一段很长的回答内容。", 30),
			},
		)
	}

	rendered := renderEvidence(lines, 1200, true)
	for i := 0; i < 12; i++ {
		require.Contains(t, rendered, fmt.Sprintf("用户第%d句", i),
			"a user line must not be evicted while assistant text remains")
	}
	require.Contains(t, rendered, evidenceOmitted,
		"rows the budget cut have to be announced, or the model reads a gap as silence")
	require.LessOrEqual(t, len([]rune(rendered)), 1200)
}

// The numbers are how the model can name the row it is describing. Numbering
// by position among the rows that survived would shift every number whenever
// the budget cut something, so positions are by index in the segment and gaps
// in the printed numbers are correct.
func TestNumberingSurvivesOmittedRows(t *testing.T) {
	base := time.Now().Add(-time.Hour)
	var lines []transcriptLine
	for i := 0; i < 6; i++ {
		lines = append(lines, transcriptLine{
			tier:    tierUser,
			at:      base.Add(time.Duration(i) * time.Minute),
			content: fmt.Sprintf("第%d句", i+1),
		})
	}
	// Room for roughly two rows, so selection has to evict and the newest win.
	rendered := renderEvidence(lines, 160, true)

	require.Contains(t, rendered, "[6] ", "the newest row survives")
	require.NotContains(t, rendered, "[1] ", "the oldest row is the one evicted")
	require.Contains(t, rendered, evidenceOmitted)
	// Whatever survived kept its own index: the number and the text agree.
	for _, row := range strings.Split(rendered, "\n") {
		if row == evidenceOmitted {
			continue
		}
		var number int
		_, err := fmt.Sscanf(row, "[%d]", &number)
		require.NoError(t, err, "every printed row carries its position: %q", row)
		require.Contains(t, row, fmt.Sprintf("第%d句", number),
			"row %q is printed under the wrong number", row)
	}
}

// One answer citing twelve chunks of the same handbook says what one citing
// three does, and the titles are the only part of a citation that is about the
// person rather than about the document.
func TestCitationRowsAreDedupedAndCapped(t *testing.T) {
	refs := types.References{}
	for i := 0; i < extractCitationMaxDocs+4; i++ {
		refs = append(refs, &types.SearchResult{
			KnowledgeTitle: fmt.Sprintf("文档%d", i), Content: "正文不应出现",
		})
	}
	// Same document, three more chunks.
	for i := 0; i < 3; i++ {
		refs = append(refs, &types.SearchResult{KnowledgeTitle: "文档0"})
	}
	titles := citedTitles(refs)
	require.Equal(t, extractCitationMaxDocs, strings.Count(titles, "；")+1)
	require.NotContains(t, titles, "正文不应出现")
	require.Equal(t, 1, strings.Count(titles, "文档0"))

	// A filename stands in when the document has no title, and a reference
	// with neither contributes nothing rather than an empty separator.
	require.Equal(t, "handbook.pdf", citedTitles(types.References{
		{KnowledgeFilename: "handbook.pdf"}, {},
	}))
}

// A conversation is read whole, gaps included. The account being written is a
// history, and a history cut into windows separated by silence loses exactly
// what makes it one: what was asked first, what the user rejected, what it
// ended up as.
func TestAWholeConversationIsReadAsOneUnit(t *testing.T) {
	svc, _, messages, _, _ := newExtractionHarness(t)
	base := time.Now().Add(-6 * time.Hour)
	messages.set("s1", []*types.Message{
		userMessage("s1", "先问第一件事", base),
		assistantMessage("s1", "第一件事的回答", base.Add(time.Minute)),
		// Hours later, and still the same conversation.
		userMessage("s1", "再问第二件事", base.Add(3*time.Hour)),
		assistantMessage("s1", "第二件事的回答", base.Add(3*time.Hour+time.Minute)),
	})

	segment, hasNew, err := svc.collectSessionTranscript(
		context.Background(), types.MemoryExtractionSession{SessionID: "s1"})
	require.NoError(t, err)
	require.True(t, hasNew)
	require.Equal(t, 2, segment.userLineCount())

	rendered := segment.render(100000)
	require.Contains(t, rendered, "先问第一件事")
	require.Contains(t, rendered, "第一件事的回答")
	require.Contains(t, rendered, "再问第二件事",
		"a three-hour gap is part of the conversation, not a boundary in it")

	// The watermark covers the assistant row too, or the next run re-reads it.
	require.Equal(t, "第二件事的回答", segment.endID)
}

// The watermark is what decides whether a rewrite happens at all, and only the
// user can move it: a conversation that grew an assistant retry says nothing
// new about the person and must not be paid for.
func TestARunIsNotOwedToAnAssistantRetry(t *testing.T) {
	svc, _, messages, _, _ := newExtractionHarness(t)
	base := time.Now().Add(-6 * time.Hour)
	messages.set("s1", []*types.Message{
		userMessage("s1", "先问第一件事", base),
		assistantMessage("s1", "重新答一遍", base.Add(time.Hour)),
	})

	_, hasNew, err := svc.collectSessionTranscript(context.Background(),
		types.MemoryExtractionSession{
			SessionID: "s1",
			Cursor:    types.MemoryMessageCursor{At: base.Add(time.Minute), ID: "先问第一件事"},
		})
	require.NoError(t, err)
	require.False(t, hasNew)
}

// System messages are the product talking to itself, and the instructions in
// them are exactly what must not be mistaken for something the user believes.
func TestSystemMessagesProduceNoEvidence(t *testing.T) {
	require.Empty(t, evidenceRows(&types.Message{Role: "system", Content: "你是一个助手"}))
	require.Empty(t, evidenceRows(&types.Message{Role: "user", Content: "   "}))
	require.Empty(t, evidenceRows(nil))
}

// Per-tier ceilings: a pasted stack trace is worth a thousand runes of a user
// message, while an answer that long is mostly restating the question.
func TestRowCeilingsDifferByTier(t *testing.T) {
	long := strings.Repeat("字", 2000)
	user := evidenceRows(&types.Message{Role: "user", Content: long})
	require.Len(t, user, 1)
	require.Len(t, []rune(user[0].content), extractUserRowMaxRunes)

	assistant := evidenceRows(&types.Message{Role: "assistant", Content: long})
	require.Len(t, assistant, 1)
	require.Len(t, []rune(assistant[0].content), extractAssistantRowMaxRunes)
}
