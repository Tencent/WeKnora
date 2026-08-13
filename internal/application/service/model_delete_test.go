package service

import (
	"context"
	"testing"
	"time"

	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type stubKBRepoForModelDelete struct {
	count int64
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
	return s.count, nil
}
func (s *stubKBRepoForModelDelete) SetUserKBPin(context.Context, uint64, string, string, bool) (*time.Time, error) {
	return nil, nil
}
func (s *stubKBRepoForModelDelete) ListUserKBPinIDs(context.Context, uint64, string) (map[string]time.Time, error) {
	return nil, nil
}

type stubAgentRepoForModelDelete struct {
	count int64
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
	return s.count, nil
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
		&stubKBRepoForModelDelete{count: 1},
		&stubAgentRepoForModelDelete{count: 0},
		nil, nil, nil,
	)

	err := svc.DeleteModel(ctx, modelID)
	require.Error(t, err)
	appErr, ok := apperrors.IsAppError(err)
	require.True(t, ok)
	assert.Equal(t, apperrors.ErrBadRequest, appErr.Code)
	assert.Contains(t, appErr.Message, "knowledge base")
}

func TestDeleteModel_RejectsWhenUsedByAgent(t *testing.T) {
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1))
	modelID := "agent-model"

	svc := NewModelService(
		&stubModelRepoForDelete{model: &types.Model{ID: modelID, TenantID: 1}},
		&stubKBRepoForModelDelete{count: 0},
		&stubAgentRepoForModelDelete{count: 2},
		nil, nil, nil,
	)

	err := svc.DeleteModel(ctx, modelID)
	require.Error(t, err)
	appErr, ok := apperrors.IsAppError(err)
	require.True(t, ok)
	assert.Contains(t, appErr.Message, "2 agent(s)")
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
	assert.Contains(t, appErr.Message, "long-term memory")
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
	assert.Contains(t, appErr.Message, "long-term memory")
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
