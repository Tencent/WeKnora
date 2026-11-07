package service

import (
	"context"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/application/access"
	chatpipeline "github.com/Tencent/WeKnora/internal/application/service/chat_pipeline"
	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

type sharedChatKBs struct{ tagTargetKnowledgeBaseService }

func (s *sharedChatKBs) GetKnowledgeBaseByID(_ context.Context, id string) (*types.KnowledgeBase, error) {
	return s.kbs[id], nil
}

type sharedChatModels struct {
	stubModelService
	lookups []string
	tenants []uint64
}

func (s *sharedChatModels) GetModelByID(ctx context.Context, id string) (*types.Model, error) {
	s.lookups = append(s.lookups, id)
	tenant := types.MustTenantIDFromContext(ctx)
	model := s.modelsByID[id]
	if model == nil || (model.TenantID != tenant && !model.IsBuiltin) {
		return nil, ErrModelNotFound
	}
	return model, nil
}

func (s *sharedChatModels) GetChatModel(ctx context.Context, id string) (chat.Chat, error) {
	s.tenants = append(s.tenants, types.MustTenantIDFromContext(ctx))
	if _, err := s.GetModelByID(ctx, id); err != nil {
		return nil, err
	}
	return s.chatModel, nil
}

func sharedChatFixture() (context.Context, *sessionService, *sharedChatModels) {
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(2))
	kb := &types.KnowledgeBase{ID: "shared", TenantID: 1, SummaryModelID: "owner-model"}
	ctx = (&access.KBAccess{
		KnowledgeBase: kb, Caller: types.CallerFromContext(ctx), EffectiveTenantID: 1, Permission: types.OrgRoleViewer,
	}).WithGrant(ctx)
	models := &sharedChatModels{stubModelService: stubModelService{
		chatModel: &captureChatModel{},
		modelsByID: map[string]*types.Model{
			"owner-model": {
				ID: "owner-model", TenantID: 1, Type: types.ModelTypeKnowledgeQA, Source: types.ModelSourceRemote,
			},
			"caller-model": {
				ID: "caller-model", TenantID: 2, Type: types.ModelTypeKnowledgeQA, Source: types.ModelSourceRemote,
			},
			"owner-other": {ID: "owner-other", TenantID: 1, Type: types.ModelTypeKnowledgeQA},
		},
	}}
	svc := &sessionService{
		cfg:          &config.Config{Conversation: &config.ConversationConfig{Summary: &config.SummaryConfig{}}},
		modelService: models,
		knowledgeBaseService: &sharedChatKBs{tagTargetKnowledgeBaseService{
			kbs: map[string]*types.KnowledgeBase{"shared": kb},
		}},
		knowledgeService: &tagTargetKnowledgeService{knowledges: []*types.Knowledge{
			{ID: "doc", KnowledgeBaseID: "shared", TenantID: 1},
		}},
	}
	return ctx, svc, models
}

func TestSharedKBChatModelSelection(t *testing.T) {
	for _, tc := range []struct {
		name, override, wantID     string
		documents, denied, builtin bool
		wantTenant                 uint64
	}{
		{name: "owner private model", wantID: "owner-model", wantTenant: 1},
		{name: "document only", documents: true, wantID: "owner-model", wantTenant: 1},
		{name: "caller override", override: "caller-model", wantID: "caller-model", wantTenant: 2},
		{
			name: "foreign override cannot choose unrelated model", override: "owner-other",
			wantID: "owner-model", wantTenant: 1,
		},
		{name: "builtin bound to KB", builtin: true, wantID: "owner-model", wantTenant: 1},
		{name: "unshared KB rejected", denied: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, svc, models := sharedChatFixture()
			if tc.denied {
				ctx = context.WithValue(context.Background(), types.TenantIDContextKey, uint64(2))
			}
			if tc.builtin {
				models.modelsByID["owner-model"].IsBuiltin = true
				models.modelsByID["owner-model"].TenantID = 99
			}
			kbIDs, knowledgeIDs := []string{"shared"}, []string(nil)
			if tc.documents {
				kbIDs, knowledgeIDs = nil, []string{"doc"}
			}
			req := &types.QARequest{SummaryModelID: tc.override}
			id, tenant, err := svc.resolveChatModelID(ctx, req, kbIDs, knowledgeIDs)
			if tc.denied {
				require.Error(t, err)
				require.Empty(t, models.lookups, "no model lookup before KB authorization")
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.wantID, id)
			require.Equal(t, tc.wantTenant, tenant)
			require.Equal(t, uint64(2), types.MustTenantIDFromContext(ctx))
			require.Equal(t, uint64(2), types.CallerFromContext(ctx).TenantID)
		})
	}
}

// Exercise the public service entry and real streaming completion plugin, not
// just the selector: dropping model ownership from ChatManage breaks this test.
func TestKnowledgeQASharedKBStreamsWithOwnerModel(t *testing.T) {
	for _, override := range []string{"", "caller-model"} {
		t.Run("override="+override, func(t *testing.T) {
			ctx, svc, models := sharedChatFixture()
			svc.eventManager = chatpipeline.NewEventManager()
			chatpipeline.NewPluginChatCompletionStream(svc.eventManager, models)
			bus := event.NewEventBus()
			done := make(chan event.AgentFinalAnswerData, 1)
			bus.On(event.EventAgentFinalAnswer, func(_ context.Context, evt event.Event) error {
				data := evt.Data.(event.AgentFinalAnswerData)
				if data.Done {
					done <- data
				}
				return nil
			})
			err := svc.KnowledgeQA(ctx, &types.QARequest{
				Session: &types.Session{ID: "session-b", TenantID: 2}, TurnLeaseHeld: true,
				KnowledgeBaseIDs: []string{"shared"}, SummaryModelID: override, Query: "question",
			}, bus)
			require.NoError(t, err)
			select {
			case answer := <-done:
				require.Equal(t, "ok", answer.Content)
			case <-time.After(3 * time.Second):
				t.Fatal("answer stream did not finish")
			}
			expected := uint64(1)
			if override != "" {
				expected = 2
			}
			require.Equal(t, []uint64{expected}, models.tenants)
			require.Equal(t, uint64(2), types.MustTenantIDFromContext(ctx))
		})
	}
}

func TestSharedKBModelFallbackUsesOwner(t *testing.T) {
	ctx, svc, models := sharedChatFixture()
	bus := event.NewEventBus()
	done := make(chan event.AgentFinalAnswerData, 1)
	bus.On(event.EventAgentFinalAnswer, func(_ context.Context, evt event.Event) error {
		data := evt.Data.(event.AgentFinalAnswerData)
		if data.Done {
			done <- data
		}
		return nil
	})
	cm := &types.ChatManage{PipelineRequest: types.PipelineRequest{
		ChatModelID: "owner-model", ChatModelTenantID: 1, FallbackPrompt: "Answer {{.Query}}", Query: "question",
	}, PipelineContext: types.PipelineContext{EventBus: bus.AsEventBusInterface()}}
	svc.handleModelFallback(ctx, cm)
	select {
	case answer := <-done:
		require.Equal(t, "ok", answer.Content)
	case <-time.After(3 * time.Second):
		t.Fatal("fallback did not finish")
	}
	require.Equal(t, []uint64{1}, models.tenants)
}

func TestSharedKBMissingModelReturnsPipelineError(t *testing.T) {
	ctx, svc, models := sharedChatFixture()
	delete(models.modelsByID, "owner-model")
	svc.eventManager = chatpipeline.NewEventManager()
	chatpipeline.NewPluginChatCompletionStream(svc.eventManager, models)
	err := svc.KnowledgeQA(ctx, &types.QARequest{
		Session: &types.Session{ID: "session-b", TenantID: 2}, TurnLeaseHeld: true,
		KnowledgeBaseIDs: []string{"shared"}, Query: "question",
	}, event.NewEventBus())
	require.ErrorIs(t, err, ErrModelNotFound)
	require.Equal(t, []uint64{1}, models.tenants)
}

func TestSharedKBRemoteModelPriority(t *testing.T) {
	ctx, svc, models := sharedChatFixture()
	kbs := svc.knowledgeBaseService.(*sharedChatKBs)
	kbs.kbs["local"] = &types.KnowledgeBase{ID: "local", TenantID: 2, SummaryModelID: "caller-model"}
	models.modelsByID["caller-model"].Source = types.ModelSourceLocal
	id, tenant, err := svc.resolveChatModelID(ctx, &types.QARequest{}, []string{"local", "shared"}, nil)
	require.NoError(t, err)
	require.Equal(t, "owner-model", id)
	require.Equal(t, uint64(1), tenant)
}
