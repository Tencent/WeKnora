package tools

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/agent/approval"
	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/types"
)

type fakeAsker struct {
	answer  approval.Answer
	asked   []approval.AskRequest
	resumed []*types.AgentPrompt
}

func (f *fakeAsker) AskUser(_ context.Context, req approval.AskRequest) (approval.Answer, error) {
	f.asked = append(f.asked, req)
	return f.answer, nil
}

func (f *fakeAsker) ResumeAnswer(_ context.Context, p *types.AgentPrompt, _ *event.EventBus) approval.Answer {
	f.resumed = append(f.resumed, p)
	return f.answer
}

func askCtx(meta *ToolExecContext) context.Context {
	return WithToolExecContext(context.Background(), meta)
}

const formArgs = `{"question":"deploy where?","fields":[
	{"name":"env","type":"select","options":["dev","prod"],"required":true},
	{"name":"replicas","type":"number"}]}`

func TestAskUserTool_NotInteractiveAsksInReply(t *testing.T) {
	asker := &fakeAsker{}
	res, err := NewAskUserTool(asker).Execute(
		askCtx(&ToolExecContext{EventBus: event.NewEventBus()}), json.RawMessage(`{"question":"q"}`))
	require.NoError(t, err)
	assert.True(t, res.Success)
	assert.Contains(t, res.Output, "Ask the user directly in your reply")
	assert.Empty(t, asker.asked)
}

func TestAskUserTool_RejectsInvalidQuestion(t *testing.T) {
	res, err := NewAskUserTool(&fakeAsker{}).Execute(
		askCtx(&ToolExecContext{Interactive: true, EventBus: event.NewEventBus()}),
		json.RawMessage(`{"question":"q","options":["a"],"fields":[{"name":"n","type":"text"}]}`))
	require.NoError(t, err)
	assert.False(t, res.Success)
	assert.Contains(t, res.Error, "Invalid question")
}

func TestAskUserTool_ReturnsAnswer(t *testing.T) {
	asker := &fakeAsker{answer: approval.Answer{PromptID: "p1", Answered: true, Response: types.AskUserPromptResponse{
		Values: map[string]any{"replicas": float64(3), "env": "prod"},
	}}}
	meta := &ToolExecContext{
		Interactive: true, EventBus: event.NewEventBus(), SessionID: "s1", AssistantMessageID: "m1",
		ToolCallID: "tc1", UserID: "user:u1",
		Snapshot: func() ([]byte, int, error) { return []byte("state"), 1, nil },
	}
	res, err := NewAskUserTool(asker).Execute(askCtx(meta), json.RawMessage(formArgs))
	require.NoError(t, err)
	require.True(t, res.Success)
	assert.Equal(t, "The user answered.\nFields:\n- env: \"prod\"\n- replicas: 3", res.Output)
	require.Len(t, asker.asked, 1)
	assert.Equal(t, []byte("state"), asker.asked[0].Snapshot)
	assert.Equal(t, "tc1", asker.asked[0].ToolCallID)
	assert.Equal(t, "deploy where?", asker.asked[0].Question.Question)
}

func TestAskUserTool_Suspends(t *testing.T) {
	suspension := &types.PromptSuspension{PromptID: "p1", Kind: types.AgentPromptKindAskUser}
	asker := &fakeAsker{answer: approval.Answer{PromptID: "p1", Suspended: suspension}}
	res, err := NewAskUserTool(asker).Execute(
		askCtx(&ToolExecContext{Interactive: true, EventBus: event.NewEventBus()}), json.RawMessage(`{"question":"q"}`))
	require.NoError(t, err)
	assert.Same(t, suspension, res.Suspend)
}

func TestAskUserTool_ResumedUsesStoredAnswer(t *testing.T) {
	asker := &fakeAsker{answer: approval.Answer{PromptID: "p1", Answered: true, Response: types.AskUserPromptResponse{
		Text: "blue",
	}}}
	answered := &types.AgentPrompt{ID: "p1", Kind: types.AgentPromptKindAskUser}
	// A resumed run is served even if the client could not show a new prompt.
	res, err := NewAskUserTool(asker).Execute(
		askCtx(&ToolExecContext{Answered: answered, EventBus: event.NewEventBus()}),
		json.RawMessage(`{"question":"q"}`))
	require.NoError(t, err)
	assert.Equal(t, "The user answered.\nText: blue", res.Output)
	assert.Empty(t, asker.asked)
	assert.Equal(t, []*types.AgentPrompt{answered}, asker.resumed)
}

func TestAskUserTool_NoAnswer(t *testing.T) {
	asker := &fakeAsker{answer: approval.Answer{PromptID: "p1", Reason: "the user declined to answer"}}
	res, err := NewAskUserTool(asker).Execute(
		askCtx(&ToolExecContext{Interactive: true, EventBus: event.NewEventBus()}), json.RawMessage(`{"question":"q"}`))
	require.NoError(t, err)
	assert.True(t, res.Success)
	assert.Contains(t, res.Output, "did not answer (the user declined to answer)")
}

func TestAskUserUsesCallerTenantForSharedAgent(t *testing.T) {
	asker := &fakeAsker{}
	ctx := context.WithValue(t.Context(), types.TenantIDContextKey, uint64(1))
	ctx = types.WithExecutionTenant(ctx, 2)
	ctx = WithToolExecContext(ctx, &ToolExecContext{Interactive: true, EventBus: event.NewEventBus()})
	_, err := NewAskUserTool(asker).Execute(ctx, json.RawMessage(`{"question":"which?"}`))
	require.NoError(t, err)
	require.Len(t, asker.asked, 1)
	require.Equal(t, uint64(1), asker.asked[0].TenantID)
}

func TestAskUserRegistryValidatesSerializedOptions(t *testing.T) {
	malformed := `["first", "depends on "rules""]`
	cases := []struct {
		name    string
		options any
		valid   bool
		count   int
	}{
		{"real array", []string{"first", `depends on "rules"`}, true, 2},
		{"valid serialized array", `["first","second"]`, true, 2},
		{"single text", "first", true, 1},
		{"ordinary bracket label", []string{"[staging]"}, true, 1},
		{"malformed serialized array", malformed, false, 0},
		{"truncated serialized array", `["first",`, false, 0},
		{"wrapped malformed array", []string{malformed}, false, 0},
		{"wrapped valid array", []string{`["first","second"]`}, false, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			asker := &fakeAsker{}
			registry := NewToolRegistry()
			registry.RegisterTool(NewAskUserTool(asker))
			ctx := askCtx(&ToolExecContext{Interactive: true, EventBus: event.NewEventBus()})
			args, err := json.Marshal(map[string]any{"question": "which?", "options": tc.options})
			require.NoError(t, err)
			result, err := registry.ExecuteTool(ctx, ToolAskUser, args)
			require.NoError(t, err)
			require.Equal(t, tc.valid, result.Success, result.Error)
			if tc.valid {
				require.Len(t, asker.asked, 1)
				require.Len(t, asker.asked[0].Question.Options, tc.count)
			} else {
				require.Empty(t, asker.asked, "invalid input must never create a prompt")
				require.Contains(t, result.Error, "options")
			}
		})
	}
}

func TestAskUserRejectsSerializedFormChoices(t *testing.T) {
	asker := &fakeAsker{}
	result, err := NewAskUserTool(asker).Execute(
		askCtx(&ToolExecContext{Interactive: true, EventBus: event.NewEventBus()}),
		json.RawMessage(`{"question":"which?","fields":[
			{"name":"env","type":"select","options":["[\"dev\",\"prod\"]"]}
		]}`))
	require.NoError(t, err)
	require.False(t, result.Success)
	require.Contains(t, result.Error, "one choice per string")
	require.Empty(t, asker.asked)
}
