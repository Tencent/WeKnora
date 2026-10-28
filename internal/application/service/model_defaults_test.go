package service

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/handler/dto"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type defaultsModelRepository struct {
	interfaces.ModelRepository
	models []*types.Model
}

func (r *defaultsModelRepository) List(
	context.Context, uint64, types.ModelType, types.ModelSource,
) ([]*types.Model, error) {
	models := make([]*types.Model, len(r.models))
	for i, m := range r.models {
		cloned := *m
		models[i] = &cloned
	}
	return models, nil
}

type defaultsTenantService struct {
	interfaces.TenantService
	tenant      *types.Tenant
	requestedID uint64
}

func (s *defaultsTenantService) GetTenantByID(_ context.Context, id uint64) (*types.Tenant, error) {
	s.requestedID = id
	return s.tenant, nil
}

func TestListModelsResolvesBackgroundDefaultsByTenantID(t *testing.T) {
	repo := &defaultsModelRepository{models: []*types.Model{
		{ID: "chat", Type: types.ModelTypeKnowledgeQA, Status: types.ModelStatusActive, IsDefault: true},
		{ID: "utility", Type: types.ModelTypeKnowledgeQA, Status: types.ModelStatusActive},
		{
			ID: "vision", Type: types.ModelTypeKnowledgeQA, Status: types.ModelStatusActive,
			Parameters: types.ModelParameters{SupportsVision: true},
		},
	}}
	tenants := &defaultsTenantService{tenant: &types.Tenant{ID: 7, DefaultSettings: &types.DefaultSettings{
		UtilityModelID: "utility", VLMModelID: "vision",
	}}}
	svc := &modelService{repo: repo, tenantService: tenants}
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7))
	// A shared agent may switch tenant ID while retaining the originating tenant info.
	ctx = context.WithValue(ctx, types.TenantInfoContextKey, &types.Tenant{
		ID:              99,
		DefaultSettings: &types.DefaultSettings{UtilityModelID: "chat"},
	})
	models, err := svc.ListModels(ctx)
	require.NoError(t, err)
	assert.Equal(t, uint64(7), tenants.requestedID)
	assert.Equal(t, "utility", types.PickUtilityModel(models).ID)
	assert.Equal(t, "chat", types.PickDefaultModel(models, types.ModelTypeKnowledgeQA).ID)
	assert.Equal(t, "vision", types.PickDefaultModel(models, types.ModelTypeVLLM).ID)
	assert.Contains(t, dto.NewModelResponse(ctx, models[2]).DefaultFor, "VLLM")
	for _, model := range repo.models {
		assert.Empty(t, model.DefaultFor, "defaults must not be written into stored model rows")
	}
}
