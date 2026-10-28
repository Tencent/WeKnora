package service

import (
	"context"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type titleGenerationModel struct {
	messages []chat.Message
}

func (m *titleGenerationModel) Chat(
	_ context.Context, messages []chat.Message, _ *chat.ChatOptions,
) (*types.ChatResponse, error) {
	m.messages = messages
	return &types.ChatResponse{Content: "海底电力电缆寿命与短路电流计算方法及其工程应用分析"}, nil
}

func (*titleGenerationModel) ChatStream(
	context.Context, []chat.Message, *chat.ChatOptions,
) (<-chan types.StreamResponse, error) {
	panic("title generation must use non-streaming chat")
}

func (*titleGenerationModel) GetModelName() string { return "configured model" }
func (*titleGenerationModel) GetModelID() string   { return "qa-model" }

type titleGenerationModels struct {
	interfaces.ModelService
	model           *titleGenerationModel
	selectedID      string
	availableModels []*types.Model
}

func (m *titleGenerationModels) ListModels(context.Context) ([]*types.Model, error) {
	if m.availableModels != nil {
		return m.availableModels, nil
	}
	return []*types.Model{{ID: "qa-model", Type: types.ModelTypeKnowledgeQA, Status: types.ModelStatusActive}}, nil
}

func (m *titleGenerationModels) GetChatModel(_ context.Context, id string) (chat.Chat, error) {
	m.selectedID = id
	return m.model, nil
}

type titleGenerationRepository struct {
	interfaces.SessionRepository
	title   string
	ownerID string
	done    chan struct{}
}

func (r *titleGenerationRepository) Update(_ context.Context, session *types.Session, ownerID string) (int64, error) {
	r.title, r.ownerID = session.Title, ownerID
	if r.done != nil {
		close(r.done)
	}
	return 1, nil
}

func TestGenerateTitleSummarizesFirstUserMessageWithConfiguredModel(t *testing.T) {
	for _, requestedModel := range []string{"", "selected-model"} {
		t.Run("model="+requestedModel, func(t *testing.T) {
			model := &titleGenerationModel{}
			models := &titleGenerationModels{model: model}
			repo := &titleGenerationRepository{}
			svc := &sessionService{
				cfg:          &config.Config{Conversation: &config.ConversationConfig{}},
				modelService: models,
				sessionRepo:  repo,
			}
			session := &types.Session{ID: "session", UserID: "owner"}
			messages := []types.Message{
				{Role: "system", Content: "system context"},
				{Role: "user", Content: "请分析海缆寿命与短路电流"},
				{Role: "assistant", Content: "previous answer"},
				{Role: "user", Content: "later question"},
			}
			title, err := svc.GenerateTitle(context.Background(), session, messages, requestedModel)
			require.NoError(t, err)
			assert.Equal(t, "海底电力电缆寿命与短路电流计算方法及其工程应用分", title)
			assert.Equal(t, title, repo.title)
			assert.Equal(t, "owner", repo.ownerID)
			wantModel := requestedModel
			if wantModel == "" {
				wantModel = "qa-model"
			}
			assert.Equal(t, wantModel, models.selectedID)
			require.Len(t, model.messages, 2)
			assert.Contains(t, model.messages[1].Content, messages[1].Content)
			assert.NotContains(t, model.messages[1].Content, "later question")
		})
	}
}

func TestGenerateTitlePreservesExistingTitle(t *testing.T) {
	// No dependencies are needed: an existing title must bypass both the LLM
	// and persistence, including a title deliberately edited by the user.
	svc := &sessionService{}
	title, err := svc.GenerateTitle(context.Background(), &types.Session{Title: "My project"}, nil, "")
	require.NoError(t, err)
	assert.Equal(t, "My project", title)
}

func TestGenerateTitleAsyncUsesWorkspaceUtilityDefault(t *testing.T) {
	for _, explicit := range []string{"", "task-model"} {
		t.Run("explicit="+explicit, func(t *testing.T) {
			models := &titleGenerationModels{model: &titleGenerationModel{}, availableModels: []*types.Model{
				{ID: "chat", Type: types.ModelTypeKnowledgeQA, Status: types.ModelStatusActive, IsDefault: true},
				{ID: "utility", Type: types.ModelTypeKnowledgeQA, Status: types.ModelStatusActive},
			}}
			(&types.DefaultSettings{UtilityModelID: "utility"}).ApplyModelDefaults(models.availableModels)
			repo := &titleGenerationRepository{done: make(chan struct{})}
			svc := &sessionService{
				cfg:          &config.Config{Conversation: &config.ConversationConfig{}},
				modelService: models,
				sessionRepo:  repo,
			}
			ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1))
			svc.GenerateTitleAsync(ctx, &types.Session{ID: "session", UserID: "owner"}, "hello", explicit, nil)
			select {
			case <-repo.done:
			case <-time.After(5 * time.Second):
				t.Fatal("title was not saved")
			}
			want := explicit
			if want == "" {
				want = "utility"
			}
			assert.Equal(t, want, models.selectedID)
		})
	}
}
