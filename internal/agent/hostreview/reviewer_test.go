package hostreview

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/models/api"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/Tencent/WeKnora/internal/types"
)

type stubModel struct {
	reply    *types.ChatResponse
	err      error
	block    bool
	messages []chat.Message
	opts     *chat.ChatOptions
}

func (m *stubModel) Chat(
	ctx context.Context, messages []chat.Message, opts *chat.ChatOptions,
) (*types.ChatResponse, error) {
	m.messages, m.opts = messages, opts
	if m.block {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return m.reply, m.err
}

func (m *stubModel) ChatStream(
	context.Context, []chat.Message, *chat.ChatOptions,
) (<-chan types.StreamResponse, error) {
	return nil, errors.New("not used")
}
func (m *stubModel) GetModelName() string { return "stub" }
func (m *stubModel) GetModelID() string   { return "stub" }

type stubMessages struct{ msgs []*types.Message }

func (s stubMessages) GetRecentUserMessages(context.Context, string, int) ([]*types.Message, error) {
	return s.msgs, nil
}

func allow(risk, auth string) *types.ChatResponse {
	return &types.ChatResponse{Content: `{"risk_level":"` + risk + `","user_authorization":"` + auth +
		`","outcome":"allow","rationale":"fine"}`}
}

func deleteRequest() sandbox.CommandApprovalRequest {
	return sandbox.CommandApprovalRequest{
		SessionID: "s1", Tool: "shell_exec", Command: "rm /Users/dev/Desktop/a.txt", Reason: "delete",
		GrantPath: "/Users/dev/Desktop", GrantKind: "directory", GrantAccess: "write", TargetsComplete: true,
	}
}

func thisTurn() Turn {
	return Turn{SessionID: "s1", RequestID: "r1"}
}

func currentTurn(content string) stubMessages {
	return stubMessages{msgs: []*types.Message{{RequestID: "r1", Content: content}}}
}

func TestReviewApprovesAndUsesAnIsolatedCallWithThinkingOff(t *testing.T) {
	model := &stubModel{reply: allow("low", "high")}
	r := New(model, stubMessages{msgs: []*types.Message{{RequestID: "r1", Content: "delete a.txt on my desktop"}}})

	v := r.Review(context.Background(), Turn{SessionID: "s1", RequestID: "r1"}, deleteRequest())
	require.True(t, v.Approved)
	require.Equal(t, "low", v.RiskLevel)

	require.Len(t, model.messages, 2)
	require.Equal(t, "system", model.messages[0].Role)
	require.Equal(t, "user", model.messages[1].Role)
	require.True(t, model.opts.Isolated)
	require.Empty(t, model.opts.Tools)
	require.Equal(t, api.ReasoningOff, model.opts.ReasoningEffort)
	require.Equal(t, 4096, model.opts.MaxCompletionTokens)
	require.Equal(t, "host-review:s1", model.opts.PromptCacheKey)
}

// Three fields claiming all is well still only approve what code proposed;
// the reviewer cannot widen the grant. The decision carries no grant at all.
func TestReviewCannotWidenTheProposal(t *testing.T) {
	r := New(&stubModel{reply: allow("low", "high")}, currentTurn("delete a.txt on my desktop"))
	v := r.Review(context.Background(), thisTurn(), deleteRequest())
	require.True(t, v.Approved)
	require.Equal(t, Verdict{Approved: true, Reason: "fine", RiskLevel: "low"}, v)
}

func TestReviewRefusesWhenItCannotSeeAScriptTheCommandRuns(t *testing.T) {
	model := &stubModel{reply: allow("low", "high")}
	req := deleteRequest()
	req.ScriptsIncomplete = true
	v := New(model, currentTurn("delete a.txt on my desktop")).Review(context.Background(), thisTurn(), req)
	require.False(t, v.Approved)
	require.Nil(t, model.messages, "the model is not asked to judge what it cannot see")
}

// Auto must not guess authorization from earlier messages, fork copies, or
// the untrusted action. Without this turn's user text the call refuses.
func TestReviewRefusesWhenThisTurnCannotBeIdentified(t *testing.T) {
	msgs := stubMessages{msgs: []*types.Message{
		{RequestID: "old", Content: "delete a.txt on my desktop"},
		{RequestID: "r1", Content: "delete a.txt on my desktop"},
	}}
	cases := []struct {
		name string
		turn Turn
		msgs stubMessages
	}{
		{name: "empty request id", turn: Turn{SessionID: "s1"}, msgs: msgs},
		{name: "no current-turn messages", turn: thisTurn(), msgs: stubMessages{msgs: []*types.Message{
			{RequestID: "old", Content: "delete a.txt on my desktop"},
		}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			model := &stubModel{reply: allow("low", "high")}
			v := New(model, tc.msgs).Review(context.Background(), tc.turn, deleteRequest())
			require.False(t, v.Approved)
			require.Contains(t, v.Reason, "this turn")
			require.Nil(t, model.messages, "must not call the model")
		})
	}
}

func TestReviewRefusesWithoutCallingTheModel(t *testing.T) {
	incomplete := deleteRequest()
	incomplete.TargetsComplete = false
	huge := deleteRequest()
	huge.Command = strings.Repeat("x", maxActionBytes+1)

	for name, req := range map[string]sandbox.CommandApprovalRequest{"targets": incomplete, "size": huge} {
		model := &stubModel{reply: allow("low", "high")}
		v := New(model, stubMessages{}).Review(context.Background(), Turn{SessionID: "s1"}, req)
		require.False(t, v.Approved, name)
		require.Nil(t, model.messages, name)
	}
}

func TestReviewRefusesACurrentTurnThatIsTooLongToReadWhole(t *testing.T) {
	model := &stubModel{reply: allow("low", "high")}
	msgs := []*types.Message{{
		RequestID: "r1",
		Content:   strings.Repeat("a", maxCurrentTurnBytes+1) + "不要删除",
	}}
	v := New(model, stubMessages{msgs: msgs}).Review(
		context.Background(), Turn{SessionID: "s1", RequestID: "r1"}, deleteRequest(),
	)
	require.False(t, v.Approved)
	require.Contains(t, v.Reason, "too long")
	require.Nil(t, model.messages)
}

func TestReviewFailsClosed(t *testing.T) {
	cases := map[string]*stubModel{
		"error": {err: errors.New("boom")},
		"empty": {reply: &types.ChatResponse{}},
		"toolcall": {reply: &types.ChatResponse{
			Content: allow("low", "high").Content, ToolCalls: []types.LLMToolCall{{ID: "x"}},
		}},
		"lowauth":  {reply: allow("low", "low")},
		"highrisk": {reply: allow("high", "high")},
	}
	for name, model := range cases {
		v := New(model, currentTurn("delete a.txt on my desktop")).Review(
			context.Background(), thisTurn(), deleteRequest(),
		)
		require.False(t, v.Approved, name)
		require.NotEmpty(t, v.Reason, name)
	}
}

func TestReviewTakesTheTurnIdFromContextWhenTheCallerOmitsIt(t *testing.T) {
	model := &stubModel{reply: allow("low", "high")}
	ctx := context.WithValue(context.Background(), types.RequestIDContextKey, "r1")
	v := New(model, currentTurn("delete a.txt on my desktop")).Review(
		ctx, Turn{SessionID: "s1"}, deleteRequest(),
	)
	require.True(t, v.Approved)
	require.NotNil(t, model.messages)
}

func TestReviewTimesOut(t *testing.T) {
	r := New(&stubModel{block: true}, currentTurn("delete a.txt on my desktop"))
	r.timeout = 20 * time.Millisecond
	v := r.Review(context.Background(), thisTurn(), deleteRequest())
	require.False(t, v.Approved)
	require.Contains(t, v.Reason, "timed out")
}
