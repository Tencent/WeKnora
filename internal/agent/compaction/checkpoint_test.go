package compaction

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// storedTurn is a turn replayed from the database: every message carries the
// ID of the assistant message that closes it.
func storedTurn(id string, rounds int, path string) []chat.Message {
	msgs := []chat.Message{{Role: "user", Content: "question " + id}}
	for i := 0; i < rounds; i++ {
		callID := id + "-call-" + string(rune('a'+i%26))
		msgs = append(msgs,
			chat.Message{
				Role:    "assistant",
				Content: filler(20),
				ToolCalls: []chat.ToolCall{{
					ID:   callID,
					Type: "function",
					Function: chat.FunctionCall{
						Name:      "write_sandbox_file",
						Arguments: `{"path":"` + path + `","content":"` + filler(40) + `"}`,
					},
				}},
			},
			chat.Message{Role: "tool", Name: "write_sandbox_file", ToolCallID: callID, Content: filler(30)},
		)
	}
	msgs = append(msgs, chat.Message{Role: "assistant", Content: "answer " + id})
	for i := range msgs {
		msgs[i].TurnID = id
	}
	return msgs
}

// withStoredHistory puts stored turns between the system prompt and the live
// turn, the way the engine assembles a request.
func withStoredHistory(live []chat.Message, turns ...[]chat.Message) []chat.Message {
	out := []chat.Message{live[0]}
	for _, turn := range turns {
		out = append(out, turn...)
	}
	return append(out, live[1:]...)
}

// The common case: the live turn outgrows the budget, so every stored turn is
// summarized and the cut splits the live turn. The history part ends on the
// last stored turn and can stand in for it later; the live turn's prefix
// cannot, because that turn will be replayed verbatim once it is stored.
func TestCheckpointEndsOnTheLastStoredTurn(t *testing.T) {
	llm := &stubChat{response: "## Goal\nsummary"}
	c := New(llm, newEstimator(t), testSettings())

	msgs := withStoredHistory(reactTurn(12),
		storedTurn("turn-a", 1, "/workspace/a.txt"),
		storedTurn("turn-b", 1, "/workspace/b.txt"))
	result, err := c.Compact(context.Background(), msgs, ReasonThreshold)
	require.NoError(t, err)
	require.True(t, result.SplitTurn)

	require.NotNil(t, result.Checkpoint)
	assert.Equal(t, "turn-b", result.Checkpoint.TurnID)
	assert.Contains(t, result.Summary, "Turn Context (split turn)")
	assert.NotContains(t, result.Checkpoint.Summary, "Turn Context (split turn)",
		"the live turn's prefix must not outlive the turn")
	assert.Contains(t, result.Checkpoint.Summary, "/workspace/b.txt")
	assert.NotContains(t, result.Checkpoint.Summary, "/workspace/out.html",
		"files touched only by the live turn belong to its own replay")
}

// A cut that splits a stored turn still ends the history part on the turn
// before it. The split turn's prefix is summarized for this request only; the
// next request replays that turn whole.
func TestCheckpointStopsBeforeASplitStoredTurn(t *testing.T) {
	c := New(&stubChat{response: "## Goal\nsummary"}, newEstimator(t), testSettings())

	live := []chat.Message{{Role: "system", Content: "you are an agent"}, {Role: "user", Content: "next"}}
	msgs := withStoredHistory(live,
		storedTurn("turn-a", 1, "/workspace/a.txt"),
		storedTurn("turn-b", 12, "/workspace/b.txt"))
	result, err := c.Compact(context.Background(), msgs, ReasonThreshold)
	require.NoError(t, err)
	require.True(t, result.SplitTurn)

	require.NotNil(t, result.Checkpoint)
	assert.Equal(t, "turn-a", result.Checkpoint.TurnID)
	assert.NotContains(t, result.Checkpoint.Summary, "/workspace/b.txt")
}

// A steered message opens a compaction turn but not a stored one. Cutting
// there summarizes half of a stored turn as history, which no stored boundary
// describes, so nothing is persisted.
func TestNoCheckpointWhenHistoryEndsInsideAStoredTurn(t *testing.T) {
	c := New(&stubChat{response: "## Goal\nsummary"}, newEstimator(t), testSettings())

	turn := []chat.Message{
		{Role: "user", Content: "question"},
		{Role: "assistant", ToolCalls: []chat.ToolCall{{
			ID: "call-1", Type: "function",
			Function: chat.FunctionCall{Name: "read_file", Arguments: `{"path":"/workspace/big.txt"}`},
		}}},
		{Role: "tool", Name: "read_file", ToolCallID: "call-1", Content: filler(800)},
		{Role: "user", Content: "steered: also check the footer"},
		{Role: "assistant", Content: "done"},
	}
	for i := range turn {
		turn[i].TurnID = "turn-a"
	}
	live := []chat.Message{{Role: "system", Content: "you are an agent"}, {Role: "user", Content: "next"}}

	result, err := c.Compact(context.Background(), withStoredHistory(live, turn), ReasonThreshold)
	require.NoError(t, err)
	require.False(t, result.SplitTurn, "the steered message is where the cut lands")
	assert.Nil(t, result.Checkpoint)
}

func TestNoCheckpointForALiveTurnOnly(t *testing.T) {
	c := New(&stubChat{response: "## Goal\nsummary"}, newEstimator(t), testSettings())

	result, err := c.Compact(context.Background(), reactTurn(12), ReasonThreshold)
	require.NoError(t, err)
	assert.Nil(t, result.Checkpoint, "nothing stored was summarized")
}

// A raw archive is a stopgap for the request that needed it. Persisting it
// would make every later turn carry the archive instead of retrying the
// summarizer.
func TestNoCheckpointFromADegradedSummary(t *testing.T) {
	c := New(&stubChat{err: assert.AnError}, newEstimator(t), testSettings())

	msgs := withStoredHistory(reactTurn(12), storedTurn("turn-a", 1, "/workspace/a.txt"))
	result, err := c.Compact(context.Background(), msgs, ReasonThreshold)
	require.NoError(t, err)
	require.True(t, result.Degraded)
	assert.Nil(t, result.Checkpoint)
}

// History loaded from a checkpoint starts with that summary. The next
// checkpoint must fold it in, including the files it recorded, or everything
// before the loaded turns is lost the moment a new checkpoint replaces it.
func TestCheckpointCarriesTheLoadedSummaryForward(t *testing.T) {
	llm := &stubChat{response: "## Goal\nupdated"}
	c := New(llm, newEstimator(t), testSettings())

	loaded := SummaryMessage("## Goal\nearlier work" + fileOps{written: []string{"/workspace/old.txt"}}.format())
	live := reactTurn(12)
	msgs := append([]chat.Message{live[0], loaded}, storedTurn("turn-c", 1, "/workspace/c.txt")...)
	msgs = append(msgs, live[1:]...)

	result, err := c.Compact(context.Background(), msgs, ReasonThreshold)
	require.NoError(t, err)

	require.NotNil(t, result.Checkpoint)
	assert.Equal(t, "turn-c", result.Checkpoint.TurnID)
	assert.Contains(t, llm.prompts[0], "<previous-summary>")
	assert.Contains(t, llm.prompts[0], "earlier work")
	assert.Contains(t, result.Checkpoint.Summary, "/workspace/old.txt")
	assert.Contains(t, result.Checkpoint.Summary, "/workspace/c.txt")
}
