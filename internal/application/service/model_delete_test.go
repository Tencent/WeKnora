package service

import (
	"context"
	"errors"
	"testing"
	"time"

	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type stubKBRepoForModelDelete struct {
	usages   []types.ModelUsageResource
	count    *int64
	usageErr error
}

func (s *stubKBRepoForModelDelete) CreateKnowledgeBase(context.Context, *types.KnowledgeBase) error {
	return nil
}
func (s *stubKBRepoForModelDelete) GetKnowledgeBaseByID(context.Context, string) (*types.KnowledgeBase, error) {
	return nil, nil
}
func (s *stubKBRepoForModelDelete) GetKnowledgeBaseByIDAndTenant(context.Context, string, uint64) (*types.KnowledgeBase, error) {
	return nil, nil
}
func (s *stubKBRepoForModelDelete) GetKnowledgeBaseByIDs(context.Context, []string) ([]*types.KnowledgeBase, error) {
	return nil, nil
}
func (s *stubKBRepoForModelDelete) ListKnowledgeBases(context.Context) ([]*types.KnowledgeBase, error) {
	return nil, nil
}
func (s *stubKBRepoForModelDelete) ListKnowledgeBasesByTenantID(context.Context, uint64) ([]*types.KnowledgeBase, error) {
	return nil, nil
}
func (s *stubKBRepoForModelDelete) UpdateKnowledgeBase(context.Context, *types.KnowledgeBase) error {
	return nil
}
func (s *stubKBRepoForModelDelete) DeleteKnowledgeBase(context.Context, string) error { return nil }
func (s *stubKBRepoForModelDelete) CountByVectorStoreID(context.Context, *gorm.DB, uint64, string) (int64, error) {
	return 0, nil
}
func (s *stubKBRepoForModelDelete) CountByModelID(context.Context, uint64, string) (int64, error) {
	if s.count != nil {
		return *s.count, s.usageErr
	}
	return int64(len(s.usages)), s.usageErr
}

func (s *stubKBRepoForModelDelete) ListModelUsages(
	context.Context, uint64, string,
) ([]types.ModelUsageResource, error) {
	return s.usages, s.usageErr
}
func (s *stubKBRepoForModelDelete) SetUserKBPin(context.Context, uint64, string, string, bool) (*time.Time, error) {
	return nil, nil
}
func (s *stubKBRepoForModelDelete) ListUserKBPinIDs(context.Context, uint64, string) (map[string]time.Time, error) {
	return nil, nil
}

type stubAgentRepoForModelDelete struct {
	usages   []types.ModelUsageResource
	count    *int64
	usageErr error
}

func (s *stubAgentRepoForModelDelete) CreateAgent(context.Context, *types.CustomAgent) error {
	return nil
}
func (s *stubAgentRepoForModelDelete) GetAgentByID(context.Context, string, uint64) (*types.CustomAgent, error) {
	return nil, nil
}
func (s *stubAgentRepoForModelDelete) ListAgentsByTenantID(context.Context, uint64) ([]*types.CustomAgent, error) {
	return nil, nil
}
func (s *stubAgentRepoForModelDelete) UpdateAgent(context.Context, *types.CustomAgent) error {
	return nil
}
func (s *stubAgentRepoForModelDelete) DeleteAgent(context.Context, string, uint64) error { return nil }
func (s *stubAgentRepoForModelDelete) CountByModelID(context.Context, uint64, string) (int64, error) {
	if s.count != nil {
		return *s.count, s.usageErr
	}
	return int64(len(s.usages)), s.usageErr
}

func (s *stubAgentRepoForModelDelete) ListModelUsages(
	context.Context, uint64, string,
) ([]types.ModelUsageResource, error) {
	return s.usages, s.usageErr
}
func (s *stubAgentRepoForModelDelete) CountBySandboxConfigID(context.Context, uint64, string) (int64, error) {
	return 0, nil
}
func (s *stubAgentRepoForModelDelete) ListNamesBySandboxConfigID(context.Context, uint64, string) ([]string, error) {
	return nil, nil
}

type stubModelRepoForDelete struct {
	model  *types.Model
	delete func(id string) error
	update func(model *types.Model) error
}

func (s *stubModelRepoForDelete) Create(context.Context, *types.Model) error { return nil }
func (s *stubModelRepoForDelete) GetByID(_ context.Context, _ uint64, id string) (*types.Model, error) {
	if s.model != nil && s.model.ID == id {
		return s.model, nil
	}
	return nil, nil
}
func (s *stubModelRepoForDelete) List(context.Context, uint64, types.ModelType, types.ModelSource) ([]*types.Model, error) {
	return nil, nil
}
func (s *stubModelRepoForDelete) Update(_ context.Context, model *types.Model) error {
	if s.update != nil {
		return s.update(model)
	}
	return nil
}
func (s *stubModelRepoForDelete) Delete(_ context.Context, _ uint64, id string) error {
	if s.delete != nil {
		return s.delete(id)
	}
	return nil
}
func (s *stubModelRepoForDelete) ClearDefaultByType(context.Context, uint, types.ModelType, string) error {
	return nil
}

func TestDeleteModel_RejectsWhenReferenced(t *testing.T) {
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1))
	modelID := "model-in-use"

	svc := NewModelService(
		&stubModelRepoForDelete{model: &types.Model{ID: modelID, TenantID: 1}},
		&stubKBRepoForModelDelete{usages: []types.ModelUsageResource{
			{ID: "kb-1", Name: "Product docs", Bindings: []types.ModelUsageBinding{types.ModelUsageBindingVLMModel}},
			{ID: "kb-2", Name: "Engineering", Bindings: []types.ModelUsageBinding{types.ModelUsageBindingVLMModel}},
		}},
		&stubAgentRepoForModelDelete{},
		nil, nil, nil,
	)

	err := svc.DeleteModel(ctx, modelID)
	require.Error(t, err)
	appErr, ok := apperrors.IsAppError(err)
	require.True(t, ok)
	assert.Equal(t, apperrors.ErrModelInUse, appErr.Code)
	assert.Contains(t, appErr.Message, "2 knowledge base(s)")
	details, ok := appErr.Details.(types.ModelUsageDetails)
	require.True(t, ok)
	require.Len(t, details.KnowledgeBases, 2)
	assert.Equal(t, int64(2), details.KnowledgeBaseTotal)
	assert.Equal(t, "Product docs", details.KnowledgeBases[0].Name)
	assert.Equal(t, []types.ModelUsageBinding{types.ModelUsageBindingVLMModel}, details.KnowledgeBases[0].Bindings)
	assert.Equal(t, "Engineering", details.KnowledgeBases[1].Name)
	assert.Equal(t, []types.ModelUsageBinding{types.ModelUsageBindingVLMModel}, details.KnowledgeBases[1].Bindings)
}

func TestDeleteModel_RejectsWhenUsedByAgent(t *testing.T) {
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1))
	modelID := "agent-model"

	svc := NewModelService(
		&stubModelRepoForDelete{model: &types.Model{ID: modelID, TenantID: 1}},
		&stubKBRepoForModelDelete{},
		&stubAgentRepoForModelDelete{usages: []types.ModelUsageResource{
			{ID: "agent-1", Name: "Support", Bindings: []types.ModelUsageBinding{types.ModelUsageBindingChatModel}},
			{ID: "agent-2", Name: "Writer", Bindings: []types.ModelUsageBinding{types.ModelUsageBindingFollowUpModel}},
		}},
		nil, nil, nil,
	)

	err := svc.DeleteModel(ctx, modelID)
	require.Error(t, err)
	appErr, ok := apperrors.IsAppError(err)
	require.True(t, ok)
	assert.Equal(t, apperrors.ErrModelInUse, appErr.Code)
	assert.Contains(t, appErr.Message, "2 agent(s)")
	details, ok := appErr.Details.(types.ModelUsageDetails)
	require.True(t, ok)
	assert.Len(t, details.Agents, 2)
	assert.Equal(t, int64(2), details.AgentTotal)
}

func TestDeleteModel_SucceedsWhenUnreferenced(t *testing.T) {
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1))
	modelID := "free-model"
	deleted := false

	svc := NewModelService(
		&stubModelRepoForDelete{
			model: &types.Model{ID: modelID, TenantID: 1},
			delete: func(id string) error {
				assert.Equal(t, modelID, id)
				deleted = true
				return nil
			},
		},
		&stubKBRepoForModelDelete{},
		&stubAgentRepoForModelDelete{},
		nil, nil, nil,
	)

	require.NoError(t, svc.DeleteModel(ctx, modelID))
	assert.True(t, deleted)
}

func TestGetModelUsageDetails_NormalizesEmptyCollections(t *testing.T) {
	svc := NewModelService(
		&stubModelRepoForDelete{},
		&stubKBRepoForModelDelete{},
		&stubAgentRepoForModelDelete{},
		nil, nil, nil,
	)

	details, err := svc.(*modelService).getModelUsageDetails(context.Background(), 1, "unused-model")
	require.NoError(t, err)
	assert.NotNil(t, details.KnowledgeBases)
	assert.NotNil(t, details.Agents)
	assert.NotNil(t, details.LongTermMemory.Bindings)
	assert.Equal(t, int64(0), details.KnowledgeBaseTotal)
	assert.Equal(t, int64(0), details.AgentTotal)
}

func TestDeleteModel_DoesNotDeleteWhenUsageLookupFails(t *testing.T) {
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1))
	modelID := "lookup-failure"
	wantErr := errors.New("usage lookup failed")
	deleted := false

	svc := NewModelService(
		&stubModelRepoForDelete{
			model: &types.Model{ID: modelID, TenantID: 1},
			delete: func(string) error {
				deleted = true
				return nil
			},
		},
		&stubKBRepoForModelDelete{usageErr: wantErr},
		&stubAgentRepoForModelDelete{},
		nil, nil, nil,
	)

	err := svc.DeleteModel(ctx, modelID)
	require.ErrorIs(t, err, wantErr)
	assert.False(t, deleted)
}

func TestDeleteModel_ReportsUntruncatedTotalsWhenListsAreCapped(t *testing.T) {
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1))
	modelID := "popular-embedding"
	kbTotal := int64(80)
	agentTotal := int64(12)

	svc := NewModelService(
		&stubModelRepoForDelete{model: &types.Model{ID: modelID, TenantID: 1}},
		&stubKBRepoForModelDelete{
			count: &kbTotal,
			usages: []types.ModelUsageResource{
				{ID: "kb-1", Name: "Alpha", Bindings: []types.ModelUsageBinding{types.ModelUsageBindingEmbeddingModel}},
				{ID: "kb-2", Name: "Beta", Bindings: []types.ModelUsageBinding{types.ModelUsageBindingEmbeddingModel}},
			},
		},
		&stubAgentRepoForModelDelete{
			count: &agentTotal,
			usages: []types.ModelUsageResource{
				{ID: "agent-1", Name: "Support", Bindings: []types.ModelUsageBinding{types.ModelUsageBindingChatModel}},
			},
		},
		nil, nil, nil,
	)

	err := svc.DeleteModel(ctx, modelID)
	require.Error(t, err)
	appErr, ok := apperrors.IsAppError(err)
	require.True(t, ok)
	assert.Contains(t, appErr.Message, "80 knowledge base(s)")
	assert.Contains(t, appErr.Message, "12 agent(s)")
	details, ok := appErr.Details.(types.ModelUsageDetails)
	require.True(t, ok)
	assert.Equal(t, int64(80), details.KnowledgeBaseTotal)
	assert.Len(t, details.KnowledgeBases, 2)
	assert.Equal(t, int64(12), details.AgentTotal)
	assert.Len(t, details.Agents, 1)
}

type stubTenantServiceForModelDelete struct {
	tenant *types.Tenant
}

func (s *stubTenantServiceForModelDelete) CreateTenant(context.Context, *types.Tenant) (*types.Tenant, error) {
	return nil, nil
}
func (s *stubTenantServiceForModelDelete) GetTenantByID(context.Context, uint64) (*types.Tenant, error) {
	return s.tenant, nil
}
func (s *stubTenantServiceForModelDelete) GetTenantsByIDs(context.Context, []uint64) (map[uint64]*types.Tenant, error) {
	return nil, nil
}
func (s *stubTenantServiceForModelDelete) ListTenants(context.Context) ([]*types.Tenant, error) {
	return nil, nil
}
func (s *stubTenantServiceForModelDelete) UpdateTenant(context.Context, *types.Tenant) (*types.Tenant, error) {
	return nil, nil
}
func (s *stubTenantServiceForModelDelete) DeleteTenant(context.Context, uint64) error { return nil }
func (s *stubTenantServiceForModelDelete) ListAllTenants(context.Context) ([]*types.Tenant, error) {
	return nil, nil
}
func (s *stubTenantServiceForModelDelete) BulkSetStorageQuota(context.Context, int64) (int64, error) {
	return 0, nil
}
func (s *stubTenantServiceForModelDelete) SearchTenants(context.Context, string, uint64, int, int) ([]*types.Tenant, int64, error) {
	return nil, 0, nil
}
func (s *stubTenantServiceForModelDelete) GetTenantByIDForUser(context.Context, uint64, string) (*types.Tenant, error) {
	return s.tenant, nil
}
func (s *stubTenantServiceForModelDelete) GetWeKnoraCloudCredentials(context.Context) *types.WeKnoraCloudCredentials {
	return nil
}

func TestDeleteModel_RejectsWhenUsedByMemory(t *testing.T) {
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1))
	modelID := "memory-embed"

	svc := NewModelService(
		&stubModelRepoForDelete{model: &types.Model{ID: modelID, TenantID: 1}},
		&stubKBRepoForModelDelete{},
		&stubAgentRepoForModelDelete{},
		nil, nil,
		&stubTenantServiceForModelDelete{
			tenant: &types.Tenant{
				ID:           1,
				MemoryConfig: &types.MemoryConfig{Enabled: true, EmbeddingModelID: modelID},
			},
		},
	)

	err := svc.DeleteModel(ctx, modelID)
	require.Error(t, err)
	appErr, ok := apperrors.IsAppError(err)
	require.True(t, ok)
	assert.Equal(t, apperrors.ErrModelInUse, appErr.Code)
	assert.Contains(t, appErr.Message, "long-term memory")
	details, ok := appErr.Details.(types.ModelUsageDetails)
	require.True(t, ok)
	assert.Equal(t, []types.ModelUsageBinding{types.ModelUsageBindingEmbeddingModel}, details.LongTermMemory.Bindings)
}

// The extraction model is pinned by the workspace exactly like the embedding
// one. Deleting it leaves memory_config pointing at a model that is gone, and
// distillation only warns when it cannot resolve one, so auto extraction would
// stop silently instead of the delete being refused.
func TestDeleteModel_RejectsWhenUsedByMemoryExtraction(t *testing.T) {
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1))
	modelID := "memory-extract"

	svc := NewModelService(
		&stubModelRepoForDelete{model: &types.Model{ID: modelID, TenantID: 1}},
		&stubKBRepoForModelDelete{},
		&stubAgentRepoForModelDelete{},
		nil, nil,
		&stubTenantServiceForModelDelete{
			tenant: &types.Tenant{
				ID: 1,
				MemoryConfig: &types.MemoryConfig{
					Enabled: true, ExtractModelID: modelID, EmbeddingModelID: "some-other-model",
				},
			},
		},
	)

	err := svc.DeleteModel(ctx, modelID)
	require.Error(t, err)
	appErr, ok := apperrors.IsAppError(err)
	require.True(t, ok)
	assert.Equal(t, apperrors.ErrModelInUse, appErr.Code)
	assert.Contains(t, appErr.Message, "long-term memory")
	details, ok := appErr.Details.(types.ModelUsageDetails)
	require.True(t, ok)
	assert.Equal(t, []types.ModelUsageBinding{types.ModelUsageBindingExtractModel}, details.LongTermMemory.Bindings)
}

func TestDeleteModel_ReportsAllMemoryBindings(t *testing.T) {
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1))
	modelID := "shared-memory-model"

	svc := NewModelService(
		&stubModelRepoForDelete{model: &types.Model{ID: modelID, TenantID: 1}},
		&stubKBRepoForModelDelete{},
		&stubAgentRepoForModelDelete{},
		nil, nil,
		&stubTenantServiceForModelDelete{
			tenant: &types.Tenant{
				ID: 1,
				MemoryConfig: &types.MemoryConfig{
					Enabled: true, EmbeddingModelID: modelID, ExtractModelID: modelID,
				},
			},
		},
	)

	err := svc.DeleteModel(ctx, modelID)
	require.Error(t, err)
	appErr, ok := apperrors.IsAppError(err)
	require.True(t, ok)
	details, ok := appErr.Details.(types.ModelUsageDetails)
	require.True(t, ok)
	assert.Equal(t, []types.ModelUsageBinding{
		types.ModelUsageBindingEmbeddingModel,
		types.ModelUsageBindingExtractModel,
	}, details.LongTermMemory.Bindings)
}

func TestFormatModelInUseMessage(t *testing.T) {
	t.Parallel()
	assert.Equal(t,
		"model is used by 1 knowledge base(s); reconfigure or remove those references before deleting",
		formatModelInUseMessage(1, 0, false),
	)
	assert.Equal(t,
		"model is used by 2 agent(s); reconfigure or remove those references before deleting",
		formatModelInUseMessage(0, 2, false),
	)
	assert.Equal(t,
		"model is used by 1 knowledge base(s) and 1 agent(s); reconfigure or remove those references before deleting",
		formatModelInUseMessage(1, 1, false),
	)
	assert.Equal(t,
		"model is used by long-term memory; reconfigure or remove those references before deleting",
		formatModelInUseMessage(0, 0, true),
	)
}
