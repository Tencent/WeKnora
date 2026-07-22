package service

import (
	"context"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/models/rerank"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type executeErrorAgentEngine struct{ err error }

func (e *executeErrorAgentEngine) Execute(context.Context, string, string, string, []chat.Message, ...[]string) (*types.AgentState, error) {
	return nil, e.err
}

type executeErrorAgentService struct {
	interfaces.AgentService
	engine interfaces.AgentEngine
}

func (s *executeErrorAgentService) CreateAgentEngine(context.Context, *types.AgentConfig, chat.Chat, rerank.Reranker, *event.EventBus, string, string) (interfaces.AgentEngine, error) {
	return s.engine, nil
}

func TestAgentQAExecuteErrorReturnsErrorWithoutEmittingDuplicate(t *testing.T) {
	executeErr := errors.New("agent execute failed")
	modelID := "chat-model"
	svc := &sessionService{
		cfg: &config.Config{},
		modelService: &stubModelService{chatModel: &captureChatModel{}, modelsByID: map[string]*types.Model{
			modelID: {ID: modelID, Type: types.ModelTypeKnowledgeQA},
		}},
		agentService: &executeErrorAgentService{engine: &executeErrorAgentEngine{err: executeErr}},
	}
	bus := event.NewEventBus()
	errorEvents := 0
	bus.On(event.EventError, func(context.Context, event.Event) error { errorEvents++; return nil })
	req := &types.QARequest{
		Session: &types.Session{ID: "session-1", TenantID: 1}, Query: "question",
		CustomAgent: &types.CustomAgent{ID: "agent-1", TenantID: 1, Config: types.CustomAgentConfig{
			ModelID: modelID, WebSearchProviderID: "unused-provider", KBSelectionMode: "none",
		}},
		AssistantMessageID: "message-1",
	}
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1))
	err := svc.AgentQA(ctx, req, bus)
	require.Error(t, err)
	assert.ErrorIs(t, err, executeErr)
	assert.Zero(t, errorEvents, "handler is the single owner of execution error events")
}
