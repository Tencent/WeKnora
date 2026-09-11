package service

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/models/invoke"
	invoketest "github.com/Tencent/WeKnora/internal/models/invoke/invoketest"
	"github.com/Tencent/WeKnora/internal/models/rerank"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type stubModelService struct {
	modelsByID      map[string]*types.Model
	availableModels []*types.Model
	cfg             *invoke.ModelConfig // BuildModelConfig 产物（invoketest seam）
}

func TestEmitKnowledgeReferencesEventIgnoresCitationOutputSetting(t *testing.T) {
	bus := event.NewEventBus()
	var emitted []event.Event
	bus.On(event.EventAgentReferences, func(_ context.Context, evt event.Event) error {
		emitted = append(emitted, evt)
		return nil
	})
	disabled := false
	result := &types.SearchResult{ID: "chunk-1", KnowledgeTitle: "Doc"}
	cm := &types.ChatManage{
		PipelineRequest: types.PipelineRequest{CitationEnabled: &disabled},
		PipelineState: types.PipelineState{
			MergeResult: []*types.SearchResult{result},
		},
		PipelineContext: types.PipelineContext{EventBus: bus.AsEventBusInterface()},
	}

	emitKnowledgeReferencesEvent(context.Background(), cm)
	require.Len(t, emitted, 1)
	require.Equal(t, event.EventAgentReferences, emitted[0].Type)
	require.Equal(t, []*types.SearchResult{result}, emitted[0].Data.(event.AgentReferencesData).References)

	enabled := true
	cm.CitationEnabled = &enabled
	emitKnowledgeReferencesEvent(context.Background(), cm)
	require.Len(t, emitted, 2)
}

func (s *stubModelService) CreateModel(context.Context, *types.Model) error {
	return nil
}

func (s *stubModelService) GetModelByID(_ context.Context, id string) (*types.Model, error) {
	return s.modelsByID[id], nil
}

func (s *stubModelService) ListModels(context.Context) ([]*types.Model, error) {
	return s.availableModels, nil
}

func (s *stubModelService) UpdateModel(context.Context, *types.Model) error {
	return nil
}

func (s *stubModelService) DeleteModel(context.Context, string) error {
	return nil
}

func (s *stubModelService) UpdateModelCredentials(
	context.Context, string, *string, *string,
) (*types.Model, error) {
	return nil, nil
}

func (s *stubModelService) ClearModelCredential(context.Context, string, string) error {
	return nil
}

func (s *stubModelService) GetEmbeddingModel(context.Context, string) (interfaces.Embedder, error) {
	return nil, nil
}

func (s *stubModelService) GetEmbeddingModelForTenant(context.Context, string, uint64) (interfaces.Embedder, error) {
	return nil, nil
}

func (s *stubModelService) GetRerankModel(context.Context, string) (rerank.Reranker, error) {
	return nil, nil
}

func (s *stubModelService) GetASRModel(context.Context, string) (interfaces.ASR, error) {
	return nil, nil
}

func (s *stubModelService) BuildModelConfig(context.Context, *types.Model) (*invoke.ModelConfig, error) {
	return s.cfg, nil
}

func TestHandleModelFallback_IncludesHistoryMessages(t *testing.T) {
	fake := invoketest.New(t)
	fake.EnqueueStream(
		invoke.StreamEvent{Kind: invoke.StreamKindAnswer, Delta: &invoke.ContentDelta{Text: "继续"}},
		invoke.StreamEvent{Kind: invoke.StreamKindAnswer, Done: &invoke.FinishInfo{FinishReason: "stop"}},
	)
	svc := &sessionService{
		modelService: &stubModelService{
			modelsByID: map[string]*types.Model{"chat-model": {ID: "chat-model"}},
			cfg:        fake.Config(),
		},
	}

	bus := event.NewEventBus()
	cm := &types.ChatManage{
		PipelineRequest: types.PipelineRequest{
			SessionID:      "session-1",
			Query:          "现在还能继续讲吗？",
			ChatModelID:    "chat-model",
			FallbackPrompt: "Answer the latest user question: {{query}}",
			SummaryConfig: types.SummaryConfig{
				Temperature: 0.2,
			},
			Language: "zh-CN",
		},
		PipelineState: types.PipelineState{
			History: []*types.History{
				{
					Query:  "先介绍一下 WeKnora",
					Answer: "WeKnora 是一个知识库问答系统。",
				},
			},
		},
		PipelineContext: types.PipelineContext{
			EventBus: bus.AsEventBusInterface(),
		},
	}

	svc.handleModelFallback(context.Background(), cm)

	// Corrected fallback shape: a system message carries the fallback
	// instruction, history is replayed in the middle, and the turn ends on the
	// user's question. Previously the system message was dropped entirely.
	calls := fake.Calls()
	require.Len(t, calls, 1)
	msgs := calls[0].Opts.Messages
	require.Len(t, msgs, 4)
	assert.Equal(t, invoke.RoleSystem, msgs[0].Role)
	assert.Contains(t, msgs[0].Text(), "Answer the latest user question")
	assert.Equal(t, invoke.RoleUser, msgs[1].Role)
	assert.Equal(t, "先介绍一下 WeKnora", msgs[1].Text())
	assert.Equal(t, invoke.RoleAssistant, msgs[2].Role)
	assert.Equal(t, "WeKnora 是一个知识库问答系统。", msgs[2].Text())
	assert.Equal(t, invoke.RoleUser, msgs[3].Role)
	assert.Contains(t, msgs[3].Text(), "现在还能继续讲吗？")
}
