package compaction

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	agenttoken "github.com/Tencent/WeKnora/internal/agent/token"
	"github.com/Tencent/WeKnora/internal/models/invoke"
	"github.com/Tencent/WeKnora/internal/models/invoke/invoketest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubChat records what the summarizer was asked and returns a canned reply,
// backed by the shared invoketest seam (the compactor calls invoke.Chat).
type stubChat struct {
	fake         *invoketest.Fake
	response     string
	finishReason string
	err          error
	base         int
}

func newStubChat(t *testing.T, s *stubChat) *stubChat {
	t.Helper()
	s.fake = invoketest.New(t)
	s.refill()
	return s
}

// refill queues enough calls to cover the summarizer's retry budget.
func (s *stubChat) refill() {
	for range 8 {
		if s.err != nil {
			s.fake.EnqueueError(500, `{"error":{"message":"stub failure"}}`)
			continue
		}
		s.fake.EnqueueResponse(invoke.ChatResponse{Content: s.response, FinishReason: s.finishReason})
	}
}

func (s *stubChat) config() *invoke.ModelConfig { return s.fake.Config() }

// promptsView returns the last non-system message per dispatched call after
// the reset point — the prompt each summarization request carried.
func (s *stubChat) promptsView() []string {
	var out []string
	for _, c := range s.fake.Calls()[s.base:] {
		if msgs := c.Opts.Messages; len(msgs) > 1 {
			out = append(out, msgs[len(msgs)-1].Text())
		}
	}
	return out
}

// resetPrompts drops recorded prompts so a later read sees only new calls.
func (s *stubChat) resetPrompts() { s.base = len(s.fake.Calls()) }

// callCount returns the number of dispatched summarization calls.
func (s *stubChat) callCount() int { return len(s.fake.Calls()) - s.base }

func newEstimator(t *testing.T) *agenttoken.Estimator {
	t.Helper()
	est, err := agenttoken.NewEstimator()
	require.NoError(t, err)
	return est
}

func testSettings() Settings {
	return Settings{
		Enabled:          true,
		MaxContextTokens: 40000,
		ReserveTokens:    8000,
		KeepRecentTokens: 2000,
	}
}

// filler produces text large enough to matter against the token budgets above.
func filler(n int) string { return strings.Repeat("some conversation content ", n) }

// reactTurn builds what an agent turn actually looks like: one user message
// followed by many assistant/tool rounds, with no further user message. This
// is the shape that the previous "never touch the current turn" rule made
// impossible to compact.
func reactTurn(rounds int) []invoke.Message {
	msgs := []invoke.Message{
		{Role: "system", Content: []invoke.Part{{Text: "you are an agent"}}},
		{Role: "user", Content: []invoke.Part{{Text: "build me a deck"}}},
	}
	for i := 0; i < rounds; i++ {
		msgs = append(msgs,
			invoke.Message{
				Role:    "assistant",
				Content: []invoke.Part{{Text: filler(20)}},
				ToolCalls: []invoke.ToolCall{{
					ID:   "call-" + string(rune('a'+i%26)),
					Type: "function",
					Function: invoke.FunctionCall{
						Name:      "write_sandbox_file",
						Arguments: `{"path":"/workspace/out.html","content":"` + filler(40) + `"}`,
					},
				}},
			},
			invoke.Message{
				Role:       "tool",
				Name:       "write_sandbox_file",
				ToolCallID: "call-" + string(rune('a'+i%26)),
				Content:    []invoke.Part{{Text: filler(30)}},
			},
		)
	}
	return msgs
}

// The regression this package exists for: a single ReAct turn with no second
// user message must still compact. The old cut rule exempted everything after
// the last user message, so a twenty-round turn was entirely untouchable and
// every round paid for a summarization that freed nothing.
func TestCompactsInsideASingleTurn(t *testing.T) {
	est := newEstimator(t)
	llm := newStubChat(t, &stubChat{response: "## Goal\nbuild a deck"})
	c := New(llm.config(), est, testSettings())
	require.NotNil(t, c)

	msgs := reactTurn(40)
	require.Equal(t, 1, countRole(msgs, "user"), "fixture must have no turn boundary to hide behind")

	result, err := c.Compact(context.Background(), msgs, ReasonThreshold)
	require.NoError(t, err)

	assert.Greater(t, result.Freed(), 0, "compaction must actually shrink the context")
	assert.Less(t, result.TokensAfter, result.TokensBefore/2)
	assert.True(t, result.SplitTurn, "a turn larger than the budget has to be split")
	assert.Equal(t, invoke.RoleSystem, result.Messages[0].Role)
	assert.Equal(t, invoke.MessageKindCompactionSummary, result.Messages[1].Kind)
}

// Cutting mid-turn discards the user's original request, so the prefix gets its
// own summary. Without it the retained tail is a pile of tool calls with no
// statement of what they were for.
func TestSplitTurnSummarizesThePrefixSeparately(t *testing.T) {
	est := newEstimator(t)
	llm := newStubChat(t, &stubChat{response: "summary text"})
	c := New(llm.config(), est, testSettings())

	result, err := c.Compact(context.Background(), reactTurn(12), ReasonThreshold)
	require.NoError(t, err)

	require.True(t, result.SplitTurn)
	assert.Contains(t, result.Summary, "Turn Context (split turn)")
	require.GreaterOrEqual(t, len(llm.promptsView()), 1)
	assert.Contains(t, llm.promptsView()[len(llm.promptsView())-1], "PREFIX of a turn",
		"the prefix must be summarized with the prefix instructions, not the history ones")
}

// The retained tail has to stay valid for the provider: a tool result whose
// originating assistant message was summarized away is rejected outright.
func TestKeptTailNeverStartsWithAnOrphanToolResult(t *testing.T) {
	est := newEstimator(t)
	c := New(newStubChat(t, &stubChat{response: "s"}).config(), est, testSettings())

	for rounds := 4; rounds <= 40; rounds++ {
		result, err := c.Compact(context.Background(), reactTurn(rounds), ReasonThreshold)
		if errors.Is(err, ErrNothingToCompact) {
			continue // still inside the keep-recent budget
		}
		require.NoError(t, err)

		pending := map[string]bool{}
		for _, msg := range result.Messages {
			for _, tc := range msg.ToolCalls {
				pending[tc.ID] = true
			}
			if msg.Role == "tool" {
				assert.True(t, pending[msg.ToolCallID],
					"rounds=%d: tool result %s has no preceding call", rounds, msg.ToolCallID)
			}
		}
	}
}

// The keep-recent budget is a ceiling. Treating it as a floor — stop at the
// first message that reaches the budget and keep that message too — is what
// made compaction fire every couple of rounds: one knowledge_search result is
// routinely larger than the whole budget, so the retained tail overshot and
// left almost no headroom before the next threshold crossing.
func TestRetainedTailStaysWithinTheKeepRecentBudget(t *testing.T) {
	est := newEstimator(t)
	s := testSettings()
	stub := newStubChat(t, &stubChat{response: "summary"})
	c := New(stub.config(), est, s)

	// A search result far larger than the budget, followed by small rounds.
	msgs := []invoke.Message{
		{Role: "system", Content: []invoke.Part{{Text: "you are an agent"}}},
		{Role: "user", Content: []invoke.Part{{Text: "research this"}}},
	}
	for i := 0; i < 6; i++ {
		id := "search-" + string(rune('a'+i))
		msgs = append(msgs,
			invoke.Message{Role: "assistant", ToolCalls: []invoke.ToolCall{{
				ID: id, Type: "function",
				Function: invoke.FunctionCall{Name: "knowledge_search", Arguments: `{"query":"x"}`},
			}}},
			invoke.Message{
				Role: "tool", Name: "knowledge_search", ToolCallID: id,
				Content: []invoke.Part{{Text: filler(600)}},
			},
		)
	}

	result, err := c.Compact(context.Background(), msgs, ReasonThreshold)
	require.NoError(t, err)

	// Measure the verbatim tail only: the summary is budgeted separately.
	tail := result.Messages[2:]
	tailTokens := est.EstimateMessages(tail)
	assert.LessOrEqual(t, tailTokens, s.Normalize().KeepRecentTokens,
		"the retained tail must fit the budget, not merely reach it")
}

// The point of compaction is the headroom it leaves, not the tokens it frees.
// Landing just under the threshold counts as a successful compaction and still
// re-triggers a round or two later, which is what the user sees as "constantly
// compacting".
func TestCompactionLeavesRoomForSeveralMoreRounds(t *testing.T) {
	est := newEstimator(t)
	s := testSettings().Normalize()
	stub := newStubChat(t, &stubChat{response: strings.Repeat("summary line\n", 40)})
	c := New(stub.config(), est, s)

	result, err := c.Compact(context.Background(), reactTurn(40), ReasonThreshold)
	require.NoError(t, err)

	assert.Less(t, result.TokensAfter, s.Threshold()*2/3,
		"post-compaction context must sit well below the threshold, not just under it")
	assert.False(t, s.ShouldCompact(result.TokensAfter),
		"a compaction that still leaves the context over the threshold is a no-op")
}

// Compacting an already-compacted context must report that it cannot help,
// rather than spending another summarization call to produce the same result.
// This is what stops the every-round loop.
func TestSecondCompactionReportsNothingToDo(t *testing.T) {
	est := newEstimator(t)
	llm := newStubChat(t, &stubChat{response: "## Goal\nbuild a deck"})
	c := New(llm.config(), est, testSettings())

	first, err := c.Compact(context.Background(), reactTurn(12), ReasonThreshold)
	require.NoError(t, err)
	callsAfterFirst := llm.callCount()

	_, err = c.Compact(context.Background(), first.Messages, ReasonThreshold)
	require.ErrorIs(t, err, ErrNothingToCompact)
	assert.Equal(t, callsAfterFirst, llm.callCount(), "a no-op compaction must not call the LLM")
}

// A summary must never be summarized again: successive passes would degrade it
// into a summary of a summary. It is instead handed to the update prompt as
// prior context, and only the messages after it are new input.
func TestPreviousSummaryIsUpdatedNotResummarized(t *testing.T) {
	est := newEstimator(t)
	llm := newStubChat(t, &stubChat{response: "## Goal\nfirst pass"})
	c := New(llm.config(), est, testSettings())

	first, err := c.Compact(context.Background(), reactTurn(12), ReasonThreshold)
	require.NoError(t, err)

	// New work arrives after the compaction, so there is something to fold in.
	grown := append(append([]invoke.Message{}, first.Messages...), reactTurn(10)[2:]...)
	llm.resetPrompts()
	second, err := c.Compact(context.Background(), grown, ReasonThreshold)
	require.NoError(t, err)

	prompt := llm.promptsView()[0]
	assert.Contains(t, prompt, "<previous-summary>")
	assert.Contains(t, prompt, "first pass")
	conversation := prompt[strings.Index(prompt, "<conversation>"):strings.Index(prompt, "</conversation>")]
	assert.NotContains(t, conversation, "compacted into the following summary",
		"the prior summary must not re-enter as conversation input")

	assert.Equal(t, 1, countKind(second.Messages, invoke.MessageKindCompactionSummary),
		"exactly one summary survives; they must not stack")
}

// A summary cut off by the token cap reads like a valid checkpoint but ends
// mid-section, and every later round inherits that truncation as its only
// memory. A length stop is a failure, not a result.
func TestTruncatedSummaryIsRejected(t *testing.T) {
	est := newEstimator(t)
	llm := newStubChat(t, &stubChat{response: "## Goal\nhalf a sum", finishReason: "length"})
	c := New(llm.config(), est, testSettings())

	result, err := c.Compact(context.Background(), reactTurn(12), ReasonThreshold)
	require.NoError(t, err)

	assert.True(t, result.Degraded)
	assert.Contains(t, result.Summary, "Raw conversation archive")
	assert.NotContains(t, result.Summary, "half a sum")
}

func TestSummarizerFailureFallsBackToArchive(t *testing.T) {
	est := newEstimator(t)
	c := New(newStubChat(t, &stubChat{err: assert.AnError}).config(), est, testSettings())

	result, err := c.Compact(context.Background(), reactTurn(12), ReasonThreshold)
	require.NoError(t, err)

	assert.True(t, result.Degraded)
	assert.Greater(t, result.Freed(), 0, "a degraded summary still has to free room")
	assert.Contains(t, result.Summary, "Raw conversation archive")
}

// Sandbox paths are the detail a prose summarizer drops first, and losing them
// makes the agent rebuild files it already wrote.
func TestFilePathsSurviveCompaction(t *testing.T) {
	est := newEstimator(t)
	c := New(newStubChat(t, &stubChat{response: "prose that mentions no paths"}).config(), est, testSettings())

	result, err := c.Compact(context.Background(), reactTurn(12), ReasonThreshold)
	require.NoError(t, err)

	assert.Contains(t, result.Summary, "<modified-files>")
	assert.Contains(t, result.Summary, "/workspace/out.html")
}

// A conversation that already fits has nothing outside the keep-recent budget.
func TestSmallConversationIsLeftAlone(t *testing.T) {
	est := newEstimator(t)
	llm := newStubChat(t, &stubChat{response: "s"})
	c := New(llm.config(), est, testSettings())

	_, err := c.Compact(context.Background(), []invoke.Message{
		{Role: "system", Content: []invoke.Part{{Text: "sys"}}},
		{Role: "user", Content: []invoke.Part{{Text: "hi"}}},
		{Role: "assistant", Content: []invoke.Part{{Text: "hello"}}},
	}, ReasonThreshold)

	require.ErrorIs(t, err, ErrNothingToCompact)
	assert.Zero(t, llm.callCount())
}

func TestKeepRecentIsScaledDownOnSmallWindows(t *testing.T) {
	// A 20k window cannot keep the 20k default and still have room for the
	// reserve and the summary; keeping it would guarantee a re-trigger.
	s := Settings{MaxContextTokens: 20000, ReserveTokens: 8000}.Normalize()
	assert.Equal(t, 3000, s.KeepRecentTokens, "a quarter of the usable window")

	// A large window keeps the default untouched.
	big := Settings{MaxContextTokens: 200000, ReserveTokens: 16384}.Normalize()
	assert.Equal(t, DefaultKeepRecentTokens, big.KeepRecentTokens)
}

func TestThresholdAndShouldCompact(t *testing.T) {
	s := Settings{Enabled: true, MaxContextTokens: 128000, ReserveTokens: 28672}.Normalize()
	assert.Equal(t, 128000-28672, s.Threshold())
	assert.False(t, s.ShouldCompact(s.Threshold()))
	assert.True(t, s.ShouldCompact(s.Threshold()+1))

	// No window means no budget to enforce.
	assert.Zero(t, Settings{Enabled: true}.Threshold())
	assert.False(t, Settings{Enabled: true}.ShouldCompact(1_000_000))

	// Disabled never triggers, whatever the size.
	assert.False(t, Settings{MaxContextTokens: 1000, ReserveTokens: 100}.
		Normalize().ShouldCompact(1_000_000))
}

// Kind is engine bookkeeping; a provider must never see it.
func TestSummaryKindIsNotSerialized(t *testing.T) {
	msg := SummaryMessage("text")
	encoded, err := marshalMessage(msg)
	require.NoError(t, err)
	assert.NotContains(t, encoded, "compaction_summary")
	assert.NotContains(t, encoded, "Kind")
}

func marshalMessage(msg invoke.Message) (string, error) {
	encoded, err := json.Marshal(msg)
	return string(encoded), err
}

func countRole(msgs []invoke.Message, role invoke.Role) int {
	n := 0
	for _, m := range msgs {
		if m.Role == role {
			n++
		}
	}
	return n
}

func countKind(msgs []invoke.Message, kind invoke.MessageKind) int {
	n := 0
	for _, m := range msgs {
		if m.Kind == kind {
			n++
		}
	}
	return n
}
