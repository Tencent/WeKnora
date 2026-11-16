package service

import (
	"context"
	"strings"
	"testing"

	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

type scopedFollowUpMessages struct {
	interfaces.MessageService
	message *types.Message
	recent  []*types.Message
}

func (m scopedFollowUpMessages) GetMessage(context.Context, string, string) (*types.Message, error) {
	return m.message, nil
}

func (m scopedFollowUpMessages) GetRecentMessagesBySession(context.Context, string, int) ([]*types.Message, error) {
	return m.recent, nil
}

type scopedFollowUpKnowledge struct {
	interfaces.KnowledgeService
	knowledgeBaseID string
}

func (k scopedFollowUpKnowledge) GetKnowledgeBatchWithSharedAccess(
	context.Context, uint64, []string,
) ([]*types.Knowledge, error) {
	return []*types.Knowledge{{
		ID: "allowed-doc", KnowledgeBaseID: k.knowledgeBaseID, TenantID: 7,
		EnableStatus: "enabled", ParseStatus: types.ParseStatusCompleted,
	}}, nil
}

type scopedFollowUpCache struct {
	interfaces.MessageSuggestionRepository
	set    *types.MessageSuggestionSet
	events int
}

func (c *scopedFollowUpCache) GetByCacheKey(
	context.Context, uint64, string, string, string, string,
) (*types.MessageSuggestionSet, error) {
	return c.set, nil
}

func (c *scopedFollowUpCache) GetByID(context.Context, uint64, string, string) (*types.MessageSuggestionSet, error) {
	return c.set, nil
}

func (c *scopedFollowUpCache) CreateEvent(context.Context, *types.MessageSuggestionEvent) error {
	c.events++
	return nil
}

func (c *scopedFollowUpCache) AcquireGeneration(
	_ context.Context, candidate *types.MessageSuggestionSet, _ bool,
) (*types.MessageSuggestionSet, bool, error) {
	if c.set != nil {
		return c.set, false, nil
	}
	return candidate, true, nil
}

func (c *scopedFollowUpCache) Save(_ context.Context, set *types.MessageSuggestionSet) error {
	c.set = set
	return nil
}

func scopedFollowUpContext(keyID uint64, kbID string) context.Context {
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7))
	return types.WithTenantAPIKeyScope(types.WithCaller(ctx, types.Caller{TenantID: 7}),
		types.TenantAPIKeyScope{KeyID: keyID, KnowledgeBaseIDs: []string{kbID}})
}

func scopedFollowUpKBs() *suggestionKBService {
	return &suggestionKBService{kbs: map[string]*types.KnowledgeBase{
		"allowed-kb": {ID: "allowed-kb", TenantID: 7},
	}}
}

func scopedFollowUpMessage(mode string) *types.Message {
	return &types.Message{
		ID: "answer", SessionID: "session", RequestID: "current-request",
		Role: "assistant", IsCompleted: true, Content: "Current answer", AgentID: "agent",
		KnowledgeReferences: types.References{
			{KnowledgeID: "allowed-doc", KnowledgeBaseID: "allowed-kb", Content: "Approved evidence"},
		},
		ExecutionContext: types.MessageExecutionContext{
			DocumentScopeAPIKeyID: 99,
			KnowledgeIDs:          []string{"allowed-doc"},
			QuestionSuggestions: &types.QuestionSuggestionConfig{
				FollowUps: types.FollowUpSuggestionConfig{
					Enabled: true, Mode: mode, ModelID: "test-model", MaxContextTurns: 4,
				},
			},
		},
	}
}

func TestRestrictedFollowUpsRejectUnverifiedMessagesBeforeCache(t *testing.T) {
	for _, tc := range []struct {
		name   string
		keyID  uint64
		kbID   string
		change func(*types.Message)
	}{
		{name: "different API key", keyID: 100, kbID: "allowed-kb"},
		{name: "key scope changed", keyID: 99, kbID: "other-kb"},
		{name: "empty whitelist", keyID: 99, kbID: "allowed-kb", change: func(m *types.Message) {
			m.ExecutionContext.KnowledgeIDs = nil
		}},
		{name: "mixed whole KB", keyID: 99, kbID: "allowed-kb", change: func(m *types.Message) {
			m.ExecutionContext.KnowledgeBaseIDs = []string{"allowed-kb"}
		}},
		{name: "out of scope reference", keyID: 99, kbID: "allowed-kb", change: func(m *types.Message) {
			m.KnowledgeReferences = append(m.KnowledgeReferences, &types.SearchResult{
				KnowledgeID: "sibling-doc", KnowledgeBaseID: "allowed-kb",
			})
		}},
		{name: "reference without a verified library", keyID: 99, kbID: "allowed-kb", change: func(m *types.Message) {
			m.KnowledgeReferences[0].KnowledgeBaseID = ""
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			message := scopedFollowUpMessage(types.SuggestionModeGenerated)
			if tc.change != nil {
				tc.change(message)
			}
			// A cache call here would fail the test: no cache is configured.
			service := &messageSuggestionService{
				messageService:       scopedFollowUpMessages{message: message},
				knowledgeService:     scopedFollowUpKnowledge{knowledgeBaseID: "allowed-kb"},
				knowledgeBaseService: scopedFollowUpKBs(),
			}
			ctx := scopedFollowUpContext(tc.keyID, tc.kbID)
			_, getErr := service.GetFollowUps(ctx, "session", message.ID)
			_, ensureErr := service.EnsureFollowUps(ctx, "session", message.ID, false)
			for _, err := range []error{getErr, ensureErr} {
				var appErr *apperrors.AppError
				require.ErrorAs(t, err, &appErr)
				require.Equal(t, apperrors.ErrForbidden, appErr.Code)
			}
		})
	}
}

func TestRestrictedKeyKeepsExistingFollowUpsForUnscopedMessages(t *testing.T) {
	message := scopedFollowUpMessage(types.SuggestionModeKnowledge)
	message.ExecutionContext.DocumentScopeAPIKeyID = 0
	message.ExecutionContext.KnowledgeIDs = nil
	cache := &scopedFollowUpCache{set: &types.MessageSuggestionSet{ConfigHash: "no-agent-config"}}
	service := &messageSuggestionService{repo: cache, messageService: scopedFollowUpMessages{message: message}}
	ctx := scopedFollowUpContext(99, "allowed-kb")
	got, err := service.GetFollowUps(ctx, "session", message.ID)
	require.NoError(t, err)
	require.Equal(t, cache.set, got)
	require.NoError(t, service.authorizeFollowUpSet(ctx, cache.set))
}

type scopedFollowUpChat = chat.Chat

type scopedFollowUpModel struct {
	scopedFollowUpChat
	t *testing.T
}

func (m scopedFollowUpModel) Chat(
	_ context.Context, messages []chat.Message, _ *chat.ChatOptions,
) (*types.ChatResponse, error) {
	require.Contains(m.t, messages[0].Content, "directly supported by that evidence")
	require.Contains(m.t, messages[1].Content, "Approved evidence")
	require.Contains(m.t, messages[1].Content, "Current question")
	require.NotContains(m.t, messages[1].Content, "Other project")
	return &types.ChatResponse{Content: `{"questions":[{"text":"Approved follow-up?"}]}`}, nil
}

type scopedFollowUpModels struct {
	interfaces.ModelService
	model chat.Chat
}

func (m scopedFollowUpModels) GetChatModel(context.Context, string) (chat.Chat, error) {
	return m.model, nil
}

func TestRestrictedFollowUpsUseOnlyCurrentVerifiedEvidence(t *testing.T) {
	message := scopedFollowUpMessage(types.SuggestionModeGenerated)
	cache := &scopedFollowUpCache{}
	service := &messageSuggestionService{
		repo: cache,
		messageService: scopedFollowUpMessages{message: message, recent: []*types.Message{
			{ID: "old-user", RequestID: "old-request", Role: "user", Content: "Other project question"},
			{
				ID: "old-answer", RequestID: "old-request", Role: "assistant",
				IsCompleted: true, Content: "Other project answer",
			},
			{ID: "user", RequestID: "current-request", Role: "user", Content: "Current question"},
			message,
		}},
		knowledgeService:     scopedFollowUpKnowledge{knowledgeBaseID: "allowed-kb"},
		knowledgeBaseService: scopedFollowUpKBs(),
		modelService:         scopedFollowUpModels{model: scopedFollowUpModel{t: t}},
	}
	ctx := scopedFollowUpContext(99, "allowed-kb")
	generated, err := service.EnsureFollowUps(ctx, "session", message.ID, false)
	require.NoError(t, err)
	require.Equal(t, types.SuggestionStatusReady, generated.Status)
	require.Len(t, generated.Questions, 1)
	require.Equal(t, "Approved follow-up?", generated.Questions[0].Text)
	require.Equal(t, []string{"allowed-doc"}, generated.Questions[0].KnowledgeIDs)
	require.Equal(t, []string{"allowed-kb"}, generated.Questions[0].KnowledgeBaseIDs)
	require.True(t, strings.HasPrefix(generated.ConfigHash, documentFollowUpHashPrefix))
	require.NoError(t, service.authorizeFollowUpSet(ctx, generated))
	require.Error(t, service.authorizeFollowUpSet(context.Background(), generated))
	legacy := *generated
	legacy.ConfigHash = "no-agent-config"
	require.Error(t, service.authorizeFollowUpSet(ctx, &legacy))
	cached, err := service.GetFollowUps(ctx, "session", message.ID)
	require.NoError(t, err)
	require.Equal(t, generated, cached)

	service.knowledgeService = scopedFollowUpKnowledge{knowledgeBaseID: "other-kb"}
	_, err = service.GetFollowUps(ctx, "session", message.ID)
	var appErr *apperrors.AppError
	require.ErrorAs(t, err, &appErr)
	require.Equal(t, apperrors.ErrForbidden, appErr.Code)
}

func TestRestrictedFollowUpsSuppressGenerationWithoutDocumentEvidence(t *testing.T) {
	message := scopedFollowUpMessage(types.SuggestionModeGenerated)
	message.KnowledgeReferences = nil
	service := &messageSuggestionService{
		repo:                 &scopedFollowUpCache{},
		messageService:       scopedFollowUpMessages{message: message},
		knowledgeService:     scopedFollowUpKnowledge{knowledgeBaseID: "allowed-kb"},
		knowledgeBaseService: scopedFollowUpKBs(),
	}
	ctx := scopedFollowUpContext(99, "allowed-kb")
	set, err := service.EnsureFollowUps(ctx, "session", message.ID, false)
	require.NoError(t, err)
	require.Equal(t, types.SuggestionStatusSuppressed, set.Status)
	require.Equal(t, "no_candidates", set.SuppressionReason)
	require.Empty(t, set.Questions)
}

func TestRestrictedFollowUpSetIDCannotBypassKeyBinding(t *testing.T) {
	message := scopedFollowUpMessage(types.SuggestionModeKnowledge)
	set := &types.MessageSuggestionSet{
		ID: "known-set", SessionID: message.SessionID, AssistantMessageID: message.ID,
		Status:    types.SuggestionStatusReady,
		Questions: types.SuggestionItems{{ID: "known-question", Text: "What is the Cedar code?"}},
	}
	repo := &scopedFollowUpCache{set: set}
	service := &messageSuggestionService{
		repo: repo, messageService: scopedFollowUpMessages{message: message},
		knowledgeService:     scopedFollowUpKnowledge{knowledgeBaseID: "allowed-kb"},
		knowledgeBaseService: scopedFollowUpKBs(),
	}
	allowed := scopedFollowUpContext(99, "allowed-kb")
	verified, err := service.authorizeFollowUp(allowed, message)
	require.NoError(t, err)
	set.ConfigHash = followUpCacheHash(verified, message.ExecutionContext)
	attribution := &types.SuggestionAttribution{SuggestionSetID: set.ID, QuestionID: set.Questions[0].ID}
	for _, other := range []context.Context{scopedFollowUpContext(100, "allowed-kb"),
		context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7))} {
		err := service.RecordEvent(other, set.SessionID, set.ID, attribution.QuestionID, types.SuggestionEventClick)
		require.Error(t, err)
		require.Error(t, service.ValidateAttribution(other, set.SessionID, set.Questions[0].Text, attribution))
	}
	require.Zero(t, repo.events)
	err = service.RecordEvent(allowed, set.SessionID, set.ID, attribution.QuestionID, types.SuggestionEventClick)
	require.NoError(t, err)
	require.NoError(t, service.ValidateAttribution(allowed, set.SessionID, set.Questions[0].Text, attribution))
	require.Equal(t, 1, repo.events)
}

type scopedFollowUpAgentRepo struct {
	interfaces.CustomAgentRepository
}

func (scopedFollowUpAgentRepo) GetAgentByID(context.Context, string, uint64) (*types.CustomAgent, error) {
	return &types.CustomAgent{ID: "agent", TenantID: 7, Config: types.CustomAgentConfig{KBSelectionMode: "all"}}, nil
}

type scopedFollowUpChunks struct {
	interfaces.ChunkRepository
	t *testing.T
}

func (c scopedFollowUpChunks) ListRecommendedFAQChunks(
	_ context.Context, tenantID uint64, kbIDs, knowledgeIDs, tagIDs []string, _ int,
) ([]*types.Chunk, error) {
	require.Equal(c.t, uint64(7), tenantID)
	require.Empty(c.t, kbIDs)
	require.Empty(c.t, tagIDs)
	require.Equal(c.t, []string{"allowed-doc"}, knowledgeIDs)
	return []*types.Chunk{
		{
			KnowledgeID: "allowed-doc", KnowledgeBaseID: "allowed-kb",
			Metadata: types.JSON(`{"standard_question":"Allowed FAQ?"}`),
		},
		{
			KnowledgeID: "sibling-doc", KnowledgeBaseID: "allowed-kb",
			Metadata: types.JSON(`{"standard_question":"Sibling FAQ?"}`),
		},
	}, nil
}

func (c scopedFollowUpChunks) ListRecentDocumentChunksWithQuestions(
	_ context.Context, tenantID uint64, kbIDs, knowledgeIDs []string, _ int,
) ([]*types.Chunk, error) {
	require.Equal(c.t, uint64(7), tenantID)
	require.Empty(c.t, kbIDs)
	require.Equal(c.t, []string{"allowed-doc"}, knowledgeIDs)
	return []*types.Chunk{
		{
			KnowledgeID: "allowed-doc", KnowledgeBaseID: "allowed-kb",
			Metadata: types.JSON(`{"generated_questions":[{"question":"Allowed document follow-up?"}]}`),
		},
		{
			KnowledgeID: "allowed-doc", KnowledgeBaseID: "other-kb",
			Metadata: types.JSON(`{"generated_questions":[{"question":"Stale KB binding?"}]}`),
		},
	}, nil
}

func TestRestrictedFollowUpsQueryOnlyVerifiedDocumentsAndKeepStarterGuard(t *testing.T) {
	message := scopedFollowUpMessage(types.SuggestionModeKnowledge)
	agent := &customAgentService{repo: scopedFollowUpAgentRepo{}, chunkRepo: scopedFollowUpChunks{t: t}}
	service := &messageSuggestionService{
		repo: &scopedFollowUpCache{}, messageService: scopedFollowUpMessages{message: message},
		knowledgeService: scopedFollowUpKnowledge{knowledgeBaseID: "allowed-kb"}, customAgentService: agent,
		knowledgeBaseService: scopedFollowUpKBs(),
	}
	ctx := scopedFollowUpContext(99, "allowed-kb")
	set, err := service.EnsureFollowUps(ctx, "session", message.ID, false)
	require.NoError(t, err)
	require.Equal(t, types.SuggestionStatusReady, set.Status)
	require.Len(t, set.Questions, 2)
	require.ElementsMatch(t, []string{"Allowed FAQ?", "Allowed document follow-up?"},
		[]string{set.Questions[0].Text, set.Questions[1].Text})
	for _, item := range set.Questions {
		require.Equal(t, []string{"allowed-doc"}, item.KnowledgeIDs)
		require.Equal(t, []string{"allowed-kb"}, item.KnowledgeBaseIDs)
	}

	verifiedContext, err := service.authorizeFollowUp(ctx, message)
	require.NoError(t, err)
	_, err = agent.GetSuggestedQuestions(verifiedContext, "agent", nil, []string{"allowed-doc"}, nil, 3)
	var appErr *apperrors.AppError
	require.ErrorAs(t, err, &appErr)
	require.Equal(t, apperrors.ErrForbidden, appErr.Code)
}
