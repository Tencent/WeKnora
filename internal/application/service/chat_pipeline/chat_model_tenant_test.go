package chatpipeline

import (
	"context"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/asr"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/models/embedding"
	"github.com/Tencent/WeKnora/internal/models/rerank"
	"github.com/Tencent/WeKnora/internal/models/vlm"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

type tenantAwareModelService struct {
	callerHits int
	ownerHits  int
	ownerID    uint64
}

func (s *tenantAwareModelService) CreateModel(context.Context, *types.Model) error { return nil }
func (s *tenantAwareModelService) CopyModel(context.Context, string, string) (*types.Model, error) {
	return nil, nil
}
func (s *tenantAwareModelService) GetModelByID(context.Context, string) (*types.Model, error) {
	return nil, nil
}
func (s *tenantAwareModelService) ListModels(context.Context) ([]*types.Model, error) { return nil, nil }
func (s *tenantAwareModelService) UpdateModel(context.Context, *types.Model) error     { return nil }
func (s *tenantAwareModelService) DeleteModel(context.Context, string) error           { return nil }
func (s *tenantAwareModelService) UpdateModelCredentials(context.Context, string, *string, *string) (*types.Model, error) {
	return nil, nil
}
func (s *tenantAwareModelService) ClearModelCredential(context.Context, string, string) error {
	return nil
}
func (s *tenantAwareModelService) GetEmbeddingModel(context.Context, string) (embedding.Embedder, error) {
	return nil, nil
}
func (s *tenantAwareModelService) GetEmbeddingModelForTenant(context.Context, string, uint64) (embedding.Embedder, error) {
	return nil, nil
}
func (s *tenantAwareModelService) GetRerankModel(context.Context, string) (rerank.Reranker, error) {
	return nil, nil
}
func (s *tenantAwareModelService) GetChatModel(context.Context, string) (chat.Chat, error) {
	s.callerHits++
	return nil, errors.New("model not found for caller tenant")
}
func (s *tenantAwareModelService) GetChatModelForTenant(_ context.Context, _ string, tenantID uint64) (chat.Chat, error) {
	s.ownerHits++
	s.ownerID = tenantID
	return &fakeChat{}, nil
}
func (s *tenantAwareModelService) GetVLMModel(context.Context, string) (vlm.VLM, error) { return nil, nil }
func (s *tenantAwareModelService) GetASRModel(context.Context, string) (asr.ASR, error) { return nil, nil }

type fakeChat struct{}

func (f *fakeChat) Chat(context.Context, []chat.Message, *chat.ChatOptions) (*types.ChatResponse, error) {
	return nil, nil
}
func (f *fakeChat) ChatStream(context.Context, []chat.Message, *chat.ChatOptions) (<-chan types.StreamResponse, error) {
	return nil, nil
}
func (f *fakeChat) GetModelName() string { return "fake" }
func (f *fakeChat) GetModelID() string   { return "fake-id" }

func TestResolveChatModel_UsesOwnerTenantForSharedKB(t *testing.T) {
	svc := &tenantAwareModelService{}
	cm := &types.ChatManage{
		PipelineRequest: types.PipelineRequest{
			ChatModelID:       "owner-summary-model",
			ChatModelTenantID: 42,
		},
	}

	got, err := resolveChatModel(context.Background(), svc, cm)
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Equal(t, 0, svc.callerHits)
	require.Equal(t, 1, svc.ownerHits)
	require.Equal(t, uint64(42), svc.ownerID)
}

func TestResolveChatModel_CallerTenantWhenUnset(t *testing.T) {
	svc := &tenantAwareModelService{}
	cm := &types.ChatManage{
		PipelineRequest: types.PipelineRequest{
			ChatModelID: "caller-model",
		},
	}

	_, err := resolveChatModel(context.Background(), svc, cm)
	require.Error(t, err)
	require.Equal(t, 1, svc.callerHits)
	require.Equal(t, 0, svc.ownerHits)
}
