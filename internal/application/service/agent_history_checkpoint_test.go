package service

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	agenttools "github.com/Tencent/WeKnora/internal/agent/tools"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// historyRepo pages a fixed message list backwards the way the database does
// and serves one checkpoint lookup.
type historyRepo struct {
	interfaces.MessageRepository
	rows          []*types.Message
	checkpoint    *types.Message
	checkpointErr error
	updates       []string
	pages         int
}

func (r *historyRepo) ListMessagesBySessionBeforeCursor(
	_ context.Context, _ string, before time.Time, beforeID string, limit int,
) ([]*types.Message, error) {
	r.pages++
	sorted := append([]*types.Message(nil), r.rows...)
	sort.Slice(sorted, func(i, j int) bool { return !sortsAtOrBefore(sorted[i], sorted[j]) })
	var out []*types.Message
	for _, m := range sorted {
		cursor := &types.Message{ID: beforeID, CreatedAt: before}
		if (!before.IsZero() || beforeID != "") && sortsAtOrBefore(cursor, m) {
			continue
		}
		out = append(out, m)
		if len(out) == limit {
			break
		}
	}
	return out, nil
}

func (r *historyRepo) GetLatestContextCheckpoint(
	context.Context, string,
) (*types.Message, error) {
	return r.checkpoint, r.checkpointErr
}

func (r *historyRepo) UpdateMessageContextCheckpoint(
	_ context.Context, sessionID, messageID string, checkpoint *types.ContextCheckpoint,
) error {
	r.updates = append(r.updates, sessionID+"/"+messageID+"/"+checkpoint.Summary)
	return nil
}

var historyBase = time.Date(2026, 9, 18, 9, 0, 0, 0, time.UTC)

// storedTurnsOfSize builds n completed turns; turn i has user u<i> and
// assistant a<i>, and its answer is padded to roughly answerTokens tokens.
func storedTurnsOfSize(n, answerTokens int) []*types.Message {
	var rows []*types.Message
	for i := 1; i <= n; i++ {
		at := historyBase.Add(time.Duration(i) * time.Minute)
		req := fmt.Sprintf("req-%d", i)
		answer := fmt.Sprintf("answer %d", i)
		if answerTokens > 0 {
			answer += " " + strings.Repeat("word ", answerTokens)
		}
		rows = append(rows,
			&types.Message{
				ID: fmt.Sprintf("u%d", i), RequestID: req, Role: "user",
				Content: fmt.Sprintf("question %d", i), CreatedAt: at,
			},
			&types.Message{
				ID: fmt.Sprintf("a%d", i), RequestID: req, Role: "assistant",
				Content: answer, IsCompleted: true, CreatedAt: at.Add(time.Second),
			},
		)
	}
	return rows
}

func storedTurns(n int) []*types.Message { return storedTurnsOfSize(n, 0) }

func checkpointOn(msg *types.Message, summary string) *types.Message {
	return &types.Message{
		ID: msg.ID, SessionID: msg.SessionID, RequestID: msg.RequestID, Role: "assistant",
		CreatedAt: msg.CreatedAt, ContextCheckpoint: &types.ContextCheckpoint{Summary: summary},
	}
}

func contents(msgs []chat.Message) []string {
	out := make([]string, len(msgs))
	for i, m := range msgs {
		out[i] = m.Content
	}
	return out
}

func questions(msgs []chat.Message) []string {
	var out []string
	for _, m := range msgs {
		if m.Role == "user" && m.Kind == "" {
			out = append(out, m.Content)
		}
	}
	return out
}

const unlimitedBudget = 1 << 30

// Without a checkpoint history is what it always was, plus the turn tags the
// engine needs to recognize a summary that ends on a stored turn.
func TestLoadAgentHistoryTagsEveryMessageWithItsTurn(t *testing.T) {
	repo := &historyRepo{rows: storedTurns(2)}

	got, _, err := LoadAgentHistory(context.Background(), repo, "s1", unlimitedBudget, false)
	require.NoError(t, err)

	require.Equal(t, []string{"question 1", "answer 1", "question 2", "answer 2"}, contents(got))
	for i, want := range []string{"a1", "a1", "a2", "a2"} {
		assert.Equal(t, want, got[i].TurnID, "message %d", i)
	}
}

// The turn count no longer bounds history: every turn that fits the budget is
// replayed, however many there are.
func TestLoadAgentHistoryIsNotCappedByTurnCount(t *testing.T) {
	repo := &historyRepo{rows: storedTurns(40)}

	got, _, err := LoadAgentHistory(context.Background(), repo, "s1", unlimitedBudget, false)
	require.NoError(t, err)
	assert.Len(t, questions(got), 40)
}

// The summary replaces its own turn and every turn before it; later turns are
// replayed verbatim after it.
func TestLoadAgentHistoryResumesFromTheCheckpoint(t *testing.T) {
	rows := storedTurns(4)
	repo := &historyRepo{rows: rows, checkpoint: checkpointOn(rows[3], "## Goal\nturns one and two")}

	got, _, err := LoadAgentHistory(context.Background(), repo, "s1", unlimitedBudget, false)
	require.NoError(t, err)

	require.Len(t, got, 5)
	assert.Equal(t, chat.MessageKindCompactionSummary, got[0].Kind)
	assert.Equal(t, "user", got[0].Role)
	assert.Contains(t, got[0].Content, "turns one and two")
	assert.Empty(t, got[0].TurnID, "the summary is not a stored turn")
	assert.Equal(t, []string{"question 3", "answer 3", "question 4", "answer 4"}, contents(got[1:]))
	assert.Equal(t, "a3", got[1].TurnID)
	assert.Equal(t, "a4", got[4].TurnID)
}

// Rows the checkpoint already covers are never needed, so the read stops at
// the page that reaches it instead of walking the whole session.
func TestLoadAgentHistoryStopsReadingAtTheCheckpoint(t *testing.T) {
	rows := storedTurns(300) // 600 rows, three pages
	repo := &historyRepo{rows: rows, checkpoint: checkpointOn(rows[2*250-1], "## Goal\nthe first 250 turns")}

	got, _, err := LoadAgentHistory(context.Background(), repo, "s1", unlimitedBudget, false)
	require.NoError(t, err)

	assert.Equal(t, 1, repo.pages)
	qs := questions(got)
	require.Len(t, qs, 50)
	assert.Equal(t, "question 251", qs[0])
	assert.Equal(t, "question 300", qs[49])
}

// A session with no checkpoint is read only until the budget is full.
func TestLoadAgentHistoryStopsReadingOnceTheBudgetIsFull(t *testing.T) {
	repo := &historyRepo{rows: storedTurnsOfSize(300, 1000)}

	got, _, err := LoadAgentHistory(context.Background(), repo, "s1", 5500, false)
	require.NoError(t, err)

	assert.Equal(t, 1, repo.pages)
	assert.Equal(t, []string{"question 296", "question 297", "question 298", "question 299", "question 300"},
		questions(got), "the newest turns that fit, contiguous")
}

// The newest turn is what the next message most likely refers to. It is kept
// even when it alone exceeds the budget; compaction can split it.
func TestLoadAgentHistoryKeepsAnOversizedNewestTurn(t *testing.T) {
	repo := &historyRepo{rows: storedTurnsOfSize(3, 1000)}

	got, _, err := LoadAgentHistory(context.Background(), repo, "s1", 100, false)
	require.NoError(t, err)
	assert.Equal(t, []string{"question 3"}, questions(got))
}

// Turns after the checkpoint that no longer fit are dropped oldest first. The
// summary stays: it is the only record of how the session began.
func TestLoadAgentHistoryBudgetsTurnsAfterTheCheckpoint(t *testing.T) {
	rows := storedTurnsOfSize(6, 1000)
	repo := &historyRepo{rows: rows, checkpoint: checkpointOn(rows[1], "## Goal\nturn one")}

	got, _, err := LoadAgentHistory(context.Background(), repo, "s1", 2500, false)
	require.NoError(t, err)

	assert.Equal(t, chat.MessageKindCompactionSummary, got[0].Kind)
	assert.Equal(t, []string{"question 5", "question 6"}, questions(got))
}

// storedWikiTurns builds n completed turns that each read a wiki page of
// roughly pageTokens tokens. Stored history keeps the page in full.
func storedWikiTurns(n, pageTokens int) []*types.Message {
	rows := storedTurns(n)
	for i := 1; i < len(rows); i += 2 {
		rows[i].AgentSteps = types.AgentSteps{{
			Thought: "reading the page",
			ToolCalls: []types.ToolCall{{
				ID:     fmt.Sprintf("call-%d", i),
				Name:   agenttools.ToolWikiReadPage,
				Args:   map[string]interface{}{"slugs": []string{"overview"}},
				Result: &types.ToolResult{Success: true, Output: strings.Repeat("page ", pageTokens)},
			}},
		}}
	}
	return rows
}

// The engine sends an earlier turn's wiki page as a one-line marker unless the
// agent retains retrieval history. Pricing the stored page instead spent the
// budget on text the model never sees and dropped turns that would have fit.
func TestLoadAgentHistoryPricesTurnsAsTheEngineSendsThem(t *testing.T) {
	repo := &historyRepo{rows: storedWikiTurns(5, 2000)}

	got, _, err := LoadAgentHistory(context.Background(), repo, "s1", 1500, false)
	require.NoError(t, err)
	assert.Len(t, questions(got), 5, "redacted pages cost a line each, so every turn fits")
	var page string
	for _, m := range got {
		if m.Role == "tool" {
			page = m.Content
		}
	}
	assert.Contains(t, page, "page page", "the loader returns stored history; the engine redacts it")

	got, _, err = LoadAgentHistory(context.Background(), repo, "s1", 1500, true)
	require.NoError(t, err)
	assert.Equal(t, []string{"question 5"}, questions(got),
		"retained pages are sent in full and priced in full")
}

// When the budget fills before the read reaches the checkpoint, the
// checkpoint's own turn is never read and turns are matched against it by
// order instead. The summary still leads, and the newest turns follow it.
func TestLoadAgentHistoryUsesACheckpointOlderThanTheRowsRead(t *testing.T) {
	rows := storedTurnsOfSize(300, 1000) // 600 rows; the checkpoint sits two pages back
	repo := &historyRepo{rows: rows, checkpoint: checkpointOn(rows[2*10-1], "## Goal\nthe first ten turns")}

	got, _, err := LoadAgentHistory(context.Background(), repo, "s1", 2500, false)
	require.NoError(t, err)

	assert.Equal(t, 1, repo.pages, "the budget, not the checkpoint, ends this read")
	assert.Equal(t, chat.MessageKindCompactionSummary, got[0].Kind)
	assert.Equal(t, []string{"question 299", "question 300"}, questions(got))
}

// A later turn's question can share the checkpoint's timestamp. Matching by
// the assistant rows' (created_at, id) order keeps it; comparing its question
// time with the checkpoint's time dropped it.
func TestTurnsAfterCheckpointKeepsATurnThatSharesItsTimestamp(t *testing.T) {
	cp := &types.Message{ID: "a1", Role: "assistant", CreatedAt: historyBase}
	earlier := &agentHistoryTurn{
		users:     []*types.Message{{ID: "u0", CreatedAt: historyBase.Add(-time.Minute)}},
		assistant: &types.Message{ID: "a0", CreatedAt: historyBase.Add(-time.Minute)},
		createdAt: historyBase.Add(-time.Minute),
	}
	same := &agentHistoryTurn{
		users:     []*types.Message{{ID: "u2", CreatedAt: historyBase}},
		assistant: &types.Message{ID: "a2", CreatedAt: historyBase.Add(time.Second)},
		createdAt: historyBase,
	}

	got := turnsAfterCheckpoint([]*agentHistoryTurn{earlier, same}, cp)
	require.Len(t, got, 1)
	assert.Equal(t, "a2", got[0].assistant.ID)
}

// Turns are priced in the provider's tokens using the newest calibrated turn's
// scale, and that scale is returned so the engine's first compaction check
// starts from it too. A session whose provider spends half as many tokens as
// the estimate fits about twice as many turns in the same window.
func TestLoadAgentHistoryPricesTurnsWithTheLatestTokenScale(t *testing.T) {
	rows := storedTurnsOfSize(10, 1000)
	repo := &historyRepo{rows: rows}

	got, scale, err := LoadAgentHistory(context.Background(), repo, "s1", 4500, false)
	require.NoError(t, err)
	assert.Zero(t, scale, "no turn was calibrated")
	assert.Len(t, questions(got), 4)

	rows[1].Usage = &types.TokenUsage{ContextTokenScale: 1.4} // older, superseded
	rows[len(rows)-1].Usage = &types.TokenUsage{ContextTokenScale: 0.5}
	got, scale, err = LoadAgentHistory(context.Background(), repo, "s1", 4500, false)
	require.NoError(t, err)
	assert.Equal(t, 0.5, scale)
	assert.Len(t, questions(got), 8)
}

// A read that stops mid-session may hold only the tail of its oldest turn. That
// turn is left out rather than replayed without its original question.
func TestCompleteHistoryTurnsLeavesOutATurnCutByTheRead(t *testing.T) {
	rows := storedTurns(2)
	steer := &types.Message{
		ID: "s1", RequestID: "req-1", Role: "user", Content: "steered",
		CreatedAt: rows[1].CreatedAt.Add(time.Second),
	}
	// Turn 1's original question (rows[0]) was not read.
	read := []*types.Message{rows[3], rows[2], steer, rows[1]}

	turns := completeHistoryTurns(read, false)
	require.Len(t, turns, 1)
	assert.Equal(t, "a2", turns[0].assistant.ID)

	turns = completeHistoryTurns(append(read, rows[0]), true)
	require.Len(t, turns, 2)
	assert.Equal(t, "question 1", turns[0].users[0].Content)

	// Ending on the question itself leaves nothing of that turn unread.
	turns = completeHistoryTurns(append(read, rows[0]), false)
	require.Len(t, turns, 2, "a read that ends on a turn's question has the whole turn")
}

func TestLoadAgentHistoryWithoutABudgetLoadsNothing(t *testing.T) {
	repo := &historyRepo{rows: storedTurns(2)}

	got, _, err := LoadAgentHistory(context.Background(), repo, "s1", 0, false)
	require.NoError(t, err)
	assert.Empty(t, got)
	assert.Zero(t, repo.pages)
}

// A failed lookup costs the turn a re-summarization, not its history.
func TestLoadAgentHistoryWithoutACheckpointWhenTheLookupFails(t *testing.T) {
	repo := &historyRepo{rows: storedTurns(2), checkpointErr: errors.New("db down")}

	got, _, err := LoadAgentHistory(context.Background(), repo, "s1", unlimitedBudget, false)
	require.NoError(t, err)
	assert.Equal(t, []string{"question 1", "answer 1", "question 2", "answer 2"}, contents(got))
}

func TestLoadAgentHistoryIgnoresAnEmptyCheckpoint(t *testing.T) {
	rows := storedTurns(2)
	repo := &historyRepo{rows: rows, checkpoint: checkpointOn(rows[1], "  ")}

	got, _, err := LoadAgentHistory(context.Background(), repo, "s1", unlimitedBudget, false)
	require.NoError(t, err)
	assert.Equal(t, []string{"question 1", "answer 1", "question 2", "answer 2"}, contents(got))
}

func TestMessageCheckpointSinkWritesThroughTheSession(t *testing.T) {
	repo := &historyRepo{}
	sink := messageCheckpointSink{repo: repo, sessionID: "s1"}

	require.NoError(t, sink.SaveContextCheckpoint(context.Background(), "a2",
		&types.ContextCheckpoint{Summary: "sum"}))
	assert.Equal(t, []string{"s1/a2/sum"}, repo.updates)
}
