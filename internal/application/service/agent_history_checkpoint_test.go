package service

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// checkpointHistoryRepo serves a fixed message list and one checkpoint lookup.
type checkpointHistoryRepo struct {
	interfaces.MessageRepository
	rows          []*types.Message
	checkpoint    *types.Message
	checkpointErr error
	updates       []string
}

func (r *checkpointHistoryRepo) GetRecentMessagesBySession(
	context.Context, string, int,
) ([]*types.Message, error) {
	return r.rows, nil
}

func (r *checkpointHistoryRepo) GetLatestContextCheckpoint(
	context.Context, string,
) (*types.Message, error) {
	return r.checkpoint, r.checkpointErr
}

func (r *checkpointHistoryRepo) UpdateMessageContextCheckpoint(
	_ context.Context, sessionID, messageID string, checkpoint *types.ContextCheckpoint,
) error {
	r.updates = append(r.updates, sessionID+"/"+messageID+"/"+checkpoint.Summary)
	return nil
}

var historyBase = time.Date(2026, 9, 18, 9, 0, 0, 0, time.UTC)

// storedTurns builds n completed turns; turn i has user u<i> and assistant a<i>.
func storedTurns(n int) []*types.Message {
	var rows []*types.Message
	for i := 1; i <= n; i++ {
		at := historyBase.Add(time.Duration(i) * time.Minute)
		req := fmt.Sprintf("req-%d", i)
		rows = append(rows,
			&types.Message{
				ID: fmt.Sprintf("u%d", i), RequestID: req, Role: "user",
				Content: fmt.Sprintf("question %d", i), CreatedAt: at,
			},
			&types.Message{
				ID: fmt.Sprintf("a%d", i), RequestID: req, Role: "assistant",
				Content: fmt.Sprintf("answer %d", i), IsCompleted: true, CreatedAt: at.Add(time.Second),
			},
		)
	}
	return rows
}

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

// Without a checkpoint history is what it always was, plus the turn tags the
// engine needs to recognize a summary that ends on a stored turn.
func TestLoadAgentHistoryTagsEveryMessageWithItsTurn(t *testing.T) {
	repo := &checkpointHistoryRepo{rows: storedTurns(2)}

	got, err := LoadAgentHistory(context.Background(), repo, "s1", 5)
	require.NoError(t, err)

	require.Equal(t, []string{"question 1", "answer 1", "question 2", "answer 2"}, contents(got))
	for i, want := range []string{"a1", "a1", "a2", "a2"} {
		assert.Equal(t, want, got[i].TurnID, "message %d", i)
	}
}

// The summary replaces its own turn and every turn before it; later turns are
// replayed verbatim after it.
func TestLoadAgentHistoryResumesFromTheCheckpoint(t *testing.T) {
	rows := storedTurns(4)
	repo := &checkpointHistoryRepo{rows: rows, checkpoint: checkpointOn(rows[3], "## Goal\nturns one and two")}

	got, err := LoadAgentHistory(context.Background(), repo, "s1", 5)
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

// A checkpoint can predate the rows fetched for the window. It still covers
// everything before it, and every fetched turn is newer.
func TestLoadAgentHistoryUsesACheckpointOlderThanTheWindow(t *testing.T) {
	rows := storedTurns(4)
	repo := &checkpointHistoryRepo{rows: rows[4:], checkpoint: checkpointOn(rows[1], "## Goal\nturn one")}

	got, err := LoadAgentHistory(context.Background(), repo, "s1", 5)
	require.NoError(t, err)

	require.Len(t, got, 5)
	assert.Contains(t, got[0].Content, "turn one")
	assert.Equal(t, []string{"question 3", "answer 3", "question 4", "answer 4"}, contents(got[1:]))
}

// The turn cap bounds the verbatim turns after the checkpoint, not the
// summary, which is already bounded by the summarizer's budget.
func TestLoadAgentHistoryCapsTurnsAfterTheCheckpoint(t *testing.T) {
	rows := storedTurns(3)
	repo := &checkpointHistoryRepo{rows: rows, checkpoint: checkpointOn(rows[1], "## Goal\nturn one")}

	got, err := LoadAgentHistory(context.Background(), repo, "s1", 1)
	require.NoError(t, err)

	require.Len(t, got, 3)
	assert.Equal(t, chat.MessageKindCompactionSummary, got[0].Kind)
	assert.Equal(t, []string{"question 3", "answer 3"}, contents(got[1:]))
}

// A failed lookup costs the turn a re-summarization, not its history.
func TestLoadAgentHistoryWithoutACheckpointWhenTheLookupFails(t *testing.T) {
	repo := &checkpointHistoryRepo{rows: storedTurns(2), checkpointErr: errors.New("db down")}

	got, err := LoadAgentHistory(context.Background(), repo, "s1", 5)
	require.NoError(t, err)
	assert.Equal(t, []string{"question 1", "answer 1", "question 2", "answer 2"}, contents(got))
}

func TestLoadAgentHistoryIgnoresAnEmptyCheckpoint(t *testing.T) {
	rows := storedTurns(2)
	repo := &checkpointHistoryRepo{rows: rows, checkpoint: checkpointOn(rows[1], "  ")}

	got, err := LoadAgentHistory(context.Background(), repo, "s1", 5)
	require.NoError(t, err)
	assert.Equal(t, []string{"question 1", "answer 1", "question 2", "answer 2"}, contents(got))
}

func TestMessageCheckpointSinkWritesThroughTheSession(t *testing.T) {
	repo := &checkpointHistoryRepo{}
	sink := messageCheckpointSink{repo: repo, sessionID: "s1"}

	require.NoError(t, sink.SaveContextCheckpoint(context.Background(), "a2",
		&types.ContextCheckpoint{Summary: "sum"}))
	assert.Equal(t, []string{"s1/a2/sum"}, repo.updates)
}
