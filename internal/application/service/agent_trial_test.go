package service

import (
	"context"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type trialAgents struct {
	interfaces.CustomAgentService
	agents map[string]*types.CustomAgent
}

func (a *trialAgents) GetAgentByID(_ context.Context, id string) (*types.CustomAgent, error) {
	if agent, ok := a.agents[id]; ok {
		return agent, nil
	}
	return nil, errors.New("agent not found")
}

type trialMessages struct {
	interfaces.MessageService
	created []*types.Message
}

func (m *trialMessages) CreateMessage(_ context.Context, msg *types.Message) (*types.Message, error) {
	msg.ID = "m" + string(rune('0'+len(m.created)))
	m.created = append(m.created, msg)
	return msg, nil
}

type trialSessions struct {
	interfaces.SessionService
	created *types.Session
	deleted []string
	agentQA func(ctx context.Context, req *types.QARequest, bus *event.EventBus) error
}

func (s *trialSessions) CreateSession(_ context.Context, session *types.Session) (*types.Session, error) {
	session.ID = "trial-session"
	s.created = session
	return session, nil
}

func (s *trialSessions) DeleteSession(_ context.Context, id string) error {
	s.deleted = append(s.deleted, id)
	return nil
}

func (s *trialSessions) AgentQA(ctx context.Context, req *types.QARequest, bus *event.EventBus) error {
	return s.agentQA(ctx, req, bus)
}

func trialCtx() context.Context {
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7))
	return types.WithCaller(types.WithPrincipal(ctx,
		types.Principal{Type: types.PrincipalWebUser, ID: "u1"}),
		types.Caller{TenantID: 7, UserID: "u1"})
}

func smartAgent(id string) *types.CustomAgent {
	return &types.CustomAgent{ID: id, Name: "Support bot", Config: types.CustomAgentConfig{
		AgentMode: types.AgentModeSmartReasoning,
	}}
}

func TestAgentTrialRunsInAHiddenSessionAndCollectsTheRun(t *testing.T) {
	sessions := &trialSessions{}
	sessions.agentQA = func(ctx context.Context, req *types.QARequest, bus *event.EventBus) error {
		assert.False(t, req.Interactive, "nobody can answer a prompt inside a trial")
		assert.False(t, req.Suspendable)
		assert.Equal(t, "Where is the VPN guide?", req.Query)
		emit := func(typ event.EventType, data any) { require.NoError(t, bus.Emit(ctx, event.NewEvent(typ, data))) }
		emit(event.EventAgentToolCall, event.AgentToolCallData{ToolCallID: "c1", ToolName: "knowledge_search"})
		emit(event.EventAgentToolResult, event.AgentToolResultData{
			ToolCallID: "c1", ToolName: "knowledge_search", Success: true,
		})
		emit(event.EventAgentToolCall, event.AgentToolCallData{ToolCallID: "c2", ToolName: "web_fetch"})
		emit(event.EventAgentToolResult, event.AgentToolResultData{
			ToolCallID: "c2", ToolName: "web_fetch", Error: "blocked",
		})
		ref := &types.SearchResult{ID: "ch1", KnowledgeTitle: "VPN guide", ChunkIndex: 2, Score: 0.8}
		emit(event.EventAgentReferences, event.AgentReferencesData{References: []*types.SearchResult{ref, ref}})
		emit(event.EventAgentFinalAnswer, event.AgentFinalAnswerData{Content: "See the VPN guide."})
		emit(event.EventAgentFinalAnswer, event.AgentFinalAnswerData{Done: true})
		emit(event.EventAgentComplete, event.AgentCompleteData{})
		return nil
	}
	messages := &trialMessages{}
	runner := NewAgentTrialRunner(&trialAgents{agents: map[string]*types.CustomAgent{"a1": smartAgent("a1")}}, messages)
	runner.Bind(sessions)

	trial, err := runner.TryAgent(trialCtx(), "a1", "Where is the VPN guide?")
	require.NoError(t, err)

	require.NotNil(t, sessions.created)
	assert.Equal(t, types.SessionKindAgentTrial, sessions.created.Kind)
	assert.False(t, types.IsValidSessionKind(types.SessionKindAgentTrial), "clients cannot create trial sessions")
	assert.Equal(t, uint64(7), sessions.created.TenantID)
	assert.Equal(t, []string{"trial-session"}, sessions.deleted, "the trial session is removed")
	require.Len(t, messages.created, 2)
	assert.Equal(t, "Support bot", trial.AgentName)
	assert.Equal(t, "smart_reasoning", trial.AgentMode)
	assert.Equal(t, "See the VPN guide.", trial.Answer)
	assert.Empty(t, trial.Error)
	require.Len(t, trial.ToolCalls, 2)
	assert.Equal(t, "knowledge_search", trial.ToolCalls[0].Name)
	assert.True(t, trial.ToolCalls[0].Success)
	assert.False(t, trial.ToolCalls[1].Success)
	assert.Equal(t, "blocked", trial.ToolCalls[1].Error)
	require.Len(t, trial.References, 1, "a chunk cited twice is listed once")
	assert.Equal(t, "VPN guide", trial.References[0].Title)
}

func TestAgentTrialReportsAFailedRun(t *testing.T) {
	sessions := &trialSessions{agentQA: func(context.Context, *types.QARequest, *event.EventBus) error {
		return errors.New("model unavailable")
	}}
	runner := NewAgentTrialRunner(&trialAgents{agents: map[string]*types.CustomAgent{"a1": smartAgent("a1")}},
		&trialMessages{})
	runner.Bind(sessions)

	trial, err := runner.TryAgent(trialCtx(), "a1", "q")
	require.NoError(t, err, "a failed answer is a trial result the assistant can report")
	assert.Equal(t, "model unavailable", trial.Error)
	assert.Equal(t, []string{"trial-session"}, sessions.deleted)
}

func TestAgentTrialRefusesAgentsThatServeWeKnora(t *testing.T) {
	agents := &trialAgents{agents: map[string]*types.CustomAgent{
		"a1": smartAgent("a1"),
		types.BuiltinWeKnoraAssistantID: {
			ID: types.BuiltinWeKnoraAssistantID, IsBuiltin: true, Name: "Assistant",
		},
	}}
	sessions := &trialSessions{}
	runner := NewAgentTrialRunner(agents, &trialMessages{})

	_, err := runner.TryAgent(trialCtx(), "a1", "q")
	require.Error(t, err, "an unbound runner refuses")

	runner.Bind(sessions)
	for _, id := range []string{
		types.BuiltinWeKnoraAssistantID, types.BuiltinWikiFixerID, types.BuiltinSkillInstallerID, "missing",
	} {
		_, err := runner.TryAgent(trialCtx(), id, "q")
		require.Error(t, err, id)
		_, err = runner.AgentName(trialCtx(), id)
		require.Error(t, err, id)
	}
	assert.Nil(t, sessions.created, "a refused trial creates nothing")

	name, err := runner.AgentName(trialCtx(), "a1")
	require.NoError(t, err)
	assert.Equal(t, "Support bot", name)
}
