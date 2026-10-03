package compaction

import (
	"context"
	"fmt"
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

// A summarizer that keeps failing used to leave no checkpoint, so every later
// turn loaded the same history, compacted it, and failed again. The raw
// archive is kept as a degraded checkpoint instead; the next compaction folds
// it in as the previous summary.
func TestDegradedSummaryIsStillACheckpoint(t *testing.T) {
	c := New(&stubChat{err: assert.AnError}, newEstimator(t), testSettings())

	msgs := withStoredHistory(reactTurn(12), storedTurn("turn-a", 1, "/workspace/a.txt"))
	result, err := c.Compact(context.Background(), msgs, ReasonThreshold)
	require.NoError(t, err)
	require.True(t, result.Degraded)

	require.NotNil(t, result.Checkpoint)
	assert.Equal(t, "turn-a", result.Checkpoint.TurnID)
	assert.True(t, result.Checkpoint.Degraded)
	assert.Contains(t, result.Checkpoint.Summary, "Raw conversation archive")
	assert.Contains(t, result.Checkpoint.Summary, "/workspace/a.txt")
}

// The archive stands in for a summary, so it is held to the summary budget,
// newest messages first. Unbounded, an archive of a history that filled the
// window was nearly as large as the history, and the compaction freed nothing.
func TestRawArchiveIsHeldToTheSummaryBudget(t *testing.T) {
	est := newEstimator(t)
	c := New(&stubChat{err: assert.AnError}, est, testSettings())

	msgs := []chat.Message{{Role: "system", Content: "you are an agent"}}
	for i := 0; i < 150; i++ {
		msgs = append(msgs, storedTurn(fmt.Sprintf("turn-%03d", i), 1, "/workspace/f.txt")...)
	}
	msgs = append(msgs, chat.Message{Role: "user", Content: "next"})

	result, err := c.Compact(context.Background(), msgs, ReasonThreshold)
	require.NoError(t, err)
	require.NotNil(t, result.Checkpoint)
	archive := result.Checkpoint.Summary

	assert.LessOrEqual(t, est.EstimateString(archive), c.Settings().summaryBudget()+300,
		"the archive must fit where a summary would")
	assert.Contains(t, archive, "earlier messages omitted]")
	assert.Contains(t, archive, "question turn-14", "the newest messages are the ones kept")
	assert.NotContains(t, archive, "question turn-000")
	assert.Less(t, result.TokensAfter, result.TokensBefore/2, "a degraded compaction still frees room")
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

// History can reach the whole window by design, so the part being summarized
// can outgrow one summarization request. An overflowing request degrades to a
// raw archive, which is never persisted, so every later turn would fail the
// same way. The oldest messages are left out instead, the prompt says so, and
// their file paths are still recorded.
func TestSummarizerInputIsFittedToTheWindow(t *testing.T) {
	est := newEstimator(t)
	llm := &stubChat{response: "## Goal\nsummary"}
	settings := Settings{Enabled: true, MaxContextTokens: 20000, ReserveTokens: 8000, KeepRecentTokens: 2000}
	c := New(llm, est, settings)

	msgs := []chat.Message{{Role: "system", Content: "you are an agent"}}
	for i := 0; i < 150; i++ {
		id := fmt.Sprintf("turn-%03d", i)
		msgs = append(msgs, storedTurn(id, 1, "/workspace/"+id+".txt")...)
	}
	msgs = append(msgs, chat.Message{Role: "user", Content: "next"})

	result, err := c.Compact(context.Background(), msgs, ReasonThreshold)
	require.NoError(t, err)
	require.False(t, result.Degraded)
	require.Positive(t, result.Omitted)

	prompt := llm.prompts[0]
	request := est.EstimateString(summarizationSystemPrompt) + est.EstimateString(prompt) +
		c.Settings().summaryBudget()
	assert.LessOrEqual(t, request, settings.MaxContextTokens, "the request must fit the window")
	assert.Contains(t, prompt, fmt.Sprintf("[%d earlier messages are not shown", result.Omitted))
	assert.NotContains(t, prompt, "question turn-000", "the oldest messages are the ones left out")
	assert.Contains(t, prompt, "question turn-140", "the newest summarized messages stay")

	require.NotNil(t, result.Checkpoint)
	assert.Contains(t, result.Checkpoint.Summary, "/workspace/turn-000.txt",
		"paths come from every summarized message, not only the ones shown")
}

func TestSummarizerInputIsUntouchedWhenItFits(t *testing.T) {
	llm := &stubChat{response: "## Goal\nsummary"}
	c := New(llm, newEstimator(t), testSettings())

	result, err := c.Compact(context.Background(),
		withStoredHistory(reactTurn(12), storedTurn("turn-a", 1, "/workspace/a.txt")), ReasonThreshold)
	require.NoError(t, err)
	assert.Zero(t, result.Omitted)
	assert.NotContains(t, llm.prompts[0], "earlier messages are not shown")
}
