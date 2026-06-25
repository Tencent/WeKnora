package service

import (
	"context"
	"testing"
	"time"

	agenttools "github.com/Tencent/WeKnora/internal/agent/tools"
	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/models/rerank"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type captureAgentEngine struct {
	query      string
	llmContext []chat.Message
}

func (e *captureAgentEngine) Execute(
	ctx context.Context,
	sessionID string,
	messageID string,
	query string,
	llmContext []chat.Message,
	imageURLs ...[]string,
) (*types.AgentState, error) {
	_, _, _, _ = ctx, sessionID, messageID, imageURLs
	e.query = query
	e.llmContext = append([]chat.Message(nil), llmContext...)
	return &types.AgentState{}, nil
}

type captureAgentService struct {
	engine *captureAgentEngine
}

func (s *captureAgentService) CreateAgentEngine(
	context.Context,
	*types.AgentConfig,
	chat.Chat,
	rerank.Reranker,
	*event.EventBus,
	string,
) (interfaces.AgentEngine, error) {
	return s.engine, nil
}

func (s *captureAgentService) ValidateConfig(*types.AgentConfig) error {
	return nil
}

type agentQAMessageRepo struct {
	messages []*types.Message
}

func (r *agentQAMessageRepo) CreateMessage(context.Context, *types.Message) (*types.Message, error) {
	return nil, nil
}

func (r *agentQAMessageRepo) GetMessage(context.Context, string, string) (*types.Message, error) {
	return nil, nil
}

func (r *agentQAMessageRepo) GetMessagesBySession(context.Context, string, int, int) ([]*types.Message, error) {
	return nil, nil
}

func (r *agentQAMessageRepo) GetRecentMessagesBySession(context.Context, string, int) ([]*types.Message, error) {
	return r.messages, nil
}

func (r *agentQAMessageRepo) GetMessagesBySessionBeforeTime(
	context.Context,
	string,
	time.Time,
	int,
) ([]*types.Message, error) {
	return nil, nil
}

func (r *agentQAMessageRepo) UpdateMessage(context.Context, *types.Message) error {
	return nil
}

func (r *agentQAMessageRepo) UpdateMessageImages(context.Context, string, string, types.MessageImages) error {
	return nil
}

func (r *agentQAMessageRepo) UpdateMessageRenderedContent(context.Context, string, string, string) error {
	return nil
}

func (r *agentQAMessageRepo) DeleteMessage(context.Context, string, string) error {
	return nil
}

func (r *agentQAMessageRepo) DeleteMessagesBySessionID(context.Context, string) error {
	return nil
}

func (r *agentQAMessageRepo) GetFirstMessageOfUser(context.Context, string) (*types.Message, error) {
	return nil, nil
}

func (r *agentQAMessageRepo) SearchMessagesByKeyword(
	context.Context,
	uint64,
	string,
	[]string,
	int,
) ([]*types.MessageWithSession, error) {
	return nil, nil
}

func (r *agentQAMessageRepo) GetMessagesByKnowledgeIDs(context.Context, []string) ([]*types.MessageWithSession, error) {
	return nil, nil
}

func (r *agentQAMessageRepo) GetMessagesByRequestIDs(context.Context, []string) ([]*types.MessageWithSession, error) {
	return nil, nil
}

func (r *agentQAMessageRepo) GetKnowledgeIDsBySessionID(context.Context, string) ([]string, error) {
	return nil, nil
}

func (r *agentQAMessageRepo) UpdateMessageKnowledgeID(context.Context, string, string) error {
	return nil
}

func TestAgentQAUsesRewrittenQueryForAgentExecution(t *testing.T) {
	rewriteModel := &captureChatModel{
		chatResponse: &types.ChatResponse{
			Content: `{"rewrite_query":"改写后的完整问题","intent":"kb_search"}`,
		},
	}
	engine := &captureAgentEngine{}
	now := time.Now()
	svc := &sessionService{
		cfg: &config.Config{
			Conversation: &config.ConversationConfig{
				RewritePromptSystem: "rewrite system",
				RewritePromptUser:   "history={{conversation}}\nquery={{query}}",
			},
		},
		modelService: &stubModelService{
			chatModel: rewriteModel,
			modelByID: &types.Model{ID: "chat-model"},
		},
		messageRepo: &agentQAMessageRepo{
			messages: []*types.Message{
				{
					ID:          "user-1",
					SessionID:   "session-1",
					RequestID:   "request-1",
					Role:        "user",
					Content:     "上一轮问题",
					IsCompleted: true,
					CreatedAt:   now.Add(-2 * time.Minute),
				},
				{
					ID:          "assistant-1",
					SessionID:   "session-1",
					RequestID:   "request-1",
					Role:        "assistant",
					Content:     "上一轮回答",
					IsCompleted: true,
					CreatedAt:   now.Add(-time.Minute),
				},
			},
		},
		agentService: &captureAgentService{engine: engine},
	}
	req := &types.QARequest{
		Session:            &types.Session{ID: "session-1", TenantID: 1},
		Query:              "它的价格是多少？",
		AssistantMessageID: "assistant-2",
		CustomAgent: &types.CustomAgent{
			ID:       "agent-1",
			TenantID: 1,
			Config: types.CustomAgentConfig{
				AgentMode:           "smart-reasoning",
				ModelID:             "chat-model",
				AllowedTools:        []string{agenttools.ToolFinalAnswer},
				MultiTurnEnabled:    true,
				HistoryTurns:        5,
				EnableRewrite:       true,
				WebSearchProviderID: "provider-1",
			},
		},
		WebSearchEnabled: true,
	}

	err := svc.AgentQA(context.Background(), req, event.NewEventBus())

	require.NoError(t, err)
	assert.Equal(t, "改写后的完整问题", engine.query)
	assert.Len(t, engine.llmContext, 2)
	require.Len(t, rewriteModel.lastMessages, 2)
	assert.Contains(t, rewriteModel.lastMessages[1].Content, "上一轮问题")
	assert.Contains(t, rewriteModel.lastMessages[1].Content, "它的价格是多少？")
}

func TestAgentQAKeepsOriginalQueryWhenRewriteDisabled(t *testing.T) {
	rewriteModel := &captureChatModel{
		chatResponse: &types.ChatResponse{
			Content: `{"rewrite_query":"不应该被使用","intent":"kb_search"}`,
		},
	}
	engine := &captureAgentEngine{}
	svc := &sessionService{
		cfg: &config.Config{
			Conversation: &config.ConversationConfig{
				RewritePromptSystem: "rewrite system",
				RewritePromptUser:   "query={{query}}",
			},
		},
		modelService: &stubModelService{
			chatModel: rewriteModel,
			modelByID: &types.Model{ID: "chat-model"},
		},
		messageRepo:  &agentQAMessageRepo{},
		agentService: &captureAgentService{engine: engine},
	}
	req := &types.QARequest{
		Session:            &types.Session{ID: "session-1", TenantID: 1},
		Query:              "原始问题",
		AssistantMessageID: "assistant-1",
		CustomAgent: &types.CustomAgent{
			ID:       "agent-1",
			TenantID: 1,
			Config: types.CustomAgentConfig{
				AgentMode:           "smart-reasoning",
				ModelID:             "chat-model",
				AllowedTools:        []string{agenttools.ToolFinalAnswer},
				EnableRewrite:       false,
				WebSearchProviderID: "provider-1",
			},
		},
	}

	err := svc.AgentQA(context.Background(), req, event.NewEventBus())

	require.NoError(t, err)
	assert.Equal(t, "原始问题", engine.query)
	assert.Empty(t, rewriteModel.lastMessages)
}

func TestAgentQAFallsBackToOriginalQueryWhenRewriteIsEmpty(t *testing.T) {
	rewriteModel := &captureChatModel{
		chatResponse: &types.ChatResponse{
			Content: `{"intent":"kb_search"}`,
		},
	}
	engine := &captureAgentEngine{}
	svc := &sessionService{
		cfg: &config.Config{
			Conversation: &config.ConversationConfig{
				RewritePromptSystem: "rewrite system",
				RewritePromptUser:   "query={{query}}",
			},
		},
		modelService: &stubModelService{
			chatModel: rewriteModel,
			modelByID: &types.Model{ID: "chat-model"},
		},
		messageRepo:  &agentQAMessageRepo{},
		agentService: &captureAgentService{engine: engine},
	}
	req := &types.QARequest{
		Session:            &types.Session{ID: "session-1", TenantID: 1},
		Query:              "原始问题",
		AssistantMessageID: "assistant-1",
		CustomAgent: &types.CustomAgent{
			ID:       "agent-1",
			TenantID: 1,
			Config: types.CustomAgentConfig{
				AgentMode:           "smart-reasoning",
				ModelID:             "chat-model",
				AllowedTools:        []string{agenttools.ToolFinalAnswer},
				EnableRewrite:       true,
				WebSearchProviderID: "provider-1",
			},
		},
	}

	err := svc.AgentQA(context.Background(), req, event.NewEventBus())

	require.NoError(t, err)
	assert.Equal(t, "原始问题", engine.query)
	require.Len(t, rewriteModel.lastMessages, 2)
}

func TestBuildAgentRewriteQueryContentIncludesImageDescriptionAndAttachments(t *testing.T) {
	req := &types.QARequest{
		Query:            "总结这份材料",
		ImageDescription: "图片里有船期表",
		Attachments: types.MessageAttachments{
			{
				FileName: "rate.txt",
				FileType: ".txt",
				FileSize: 1024,
				Content:  "上海到洛杉矶运价",
			},
		},
	}

	content := buildAgentRewriteQueryContent(req)

	assert.Contains(t, content, "总结这份材料")
	assert.Contains(t, content, "图片里有船期表")
	assert.Contains(t, content, "rate.txt")
	assert.Contains(t, content, "上海到洛杉矶运价")
}
