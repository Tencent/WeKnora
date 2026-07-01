package service

import (
	"context"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/provider"
	"github.com/Tencent/WeKnora/internal/types"
)

type fakeModelRepositoryForVLM struct {
	model *types.Model
}

func (f *fakeModelRepositoryForVLM) Create(ctx context.Context, model *types.Model) error {
	return nil
}

func (f *fakeModelRepositoryForVLM) GetByID(ctx context.Context, tenantID uint64, id string) (*types.Model, error) {
	if f.model != nil && f.model.ID == id {
		return f.model, nil
	}
	return nil, nil
}

func (f *fakeModelRepositoryForVLM) List(
	ctx context.Context,
	tenantID uint64,
	modelType types.ModelType,
	source types.ModelSource,
) ([]*types.Model, error) {
	return nil, nil
}

func (f *fakeModelRepositoryForVLM) Update(ctx context.Context, model *types.Model) error {
	return nil
}

func (f *fakeModelRepositoryForVLM) Delete(ctx context.Context, tenantID uint64, id string) error {
	return nil
}

func (f *fakeModelRepositoryForVLM) ClearDefaultByType(
	ctx context.Context,
	tenantID uint,
	modelType types.ModelType,
	excludeID string,
) error {
	return nil
}

type fakeTenantServiceForVLM struct {
	creds *types.WeKnoraCloudCredentials
}

func (f *fakeTenantServiceForVLM) CreateTenant(ctx context.Context, tenant *types.Tenant) (*types.Tenant, error) {
	return nil, errors.New("not implemented")
}

func (f *fakeTenantServiceForVLM) GetTenantByID(ctx context.Context, id uint64) (*types.Tenant, error) {
	return nil, errors.New("not implemented")
}

func (f *fakeTenantServiceForVLM) GetTenantsByIDs(ctx context.Context, ids []uint64) (map[uint64]*types.Tenant, error) {
	return nil, errors.New("not implemented")
}

func (f *fakeTenantServiceForVLM) ListTenants(ctx context.Context) ([]*types.Tenant, error) {
	return nil, errors.New("not implemented")
}

func (f *fakeTenantServiceForVLM) UpdateTenant(ctx context.Context, tenant *types.Tenant) (*types.Tenant, error) {
	return nil, errors.New("not implemented")
}

func (f *fakeTenantServiceForVLM) DeleteTenant(ctx context.Context, id uint64) error {
	return errors.New("not implemented")
}

func (f *fakeTenantServiceForVLM) UpdateAPIKey(ctx context.Context, id uint64) (string, error) {
	return "", errors.New("not implemented")
}

func (f *fakeTenantServiceForVLM) ExtractTenantIDFromAPIKey(apiKey string) (uint64, error) {
	return 0, errors.New("not implemented")
}

func (f *fakeTenantServiceForVLM) ListAllTenants(ctx context.Context) ([]*types.Tenant, error) {
	return nil, errors.New("not implemented")
}

func (f *fakeTenantServiceForVLM) BulkSetStorageQuota(ctx context.Context, quotaBytes int64) (int64, error) {
	return 0, errors.New("not implemented")
}

func (f *fakeTenantServiceForVLM) SearchTenants(
	ctx context.Context,
	keyword string,
	tenantID uint64,
	page int,
	pageSize int,
) ([]*types.Tenant, int64, error) {
	return nil, 0, errors.New("not implemented")
}

func (f *fakeTenantServiceForVLM) GetTenantByIDForUser(
	ctx context.Context,
	tenantID uint64,
	userID string,
) (*types.Tenant, error) {
	return nil, errors.New("not implemented")
}

func (f *fakeTenantServiceForVLM) GetWeKnoraCloudCredentials(ctx context.Context) *types.WeKnoraCloudCredentials {
	return f.creds
}

func TestGetVLMModelWithFingerprint_UsesWeKnoraCloudTenantCredentialFallback(t *testing.T) {
	model := &types.Model{
		ID:       "vlm-1",
		TenantID: 7,
		Name:     "display-model",
		Type:     types.ModelTypeVLLM,
		Source:   types.ModelSourceRemote,
		Status:   types.ModelStatusActive,
		Parameters: types.ModelParameters{
			BaseURL:       "https://weknora.example.com",
			Provider:      string(provider.ProviderWeKnoraCloud),
			InterfaceType: "openai",
			ExtraConfig: map[string]string{
				"remote_model_name": "remote-vision",
				"api_key":           "must-not-leak",
			},
			CustomHeaders: map[string]string{
				"X-Route":       "vision",
				"Authorization": "Bearer must-not-leak",
			},
		},
	}
	svc := &modelService{
		repo: &fakeModelRepositoryForVLM{model: model},
		tenantService: &fakeTenantServiceForVLM{
			creds: &types.WeKnoraCloudCredentials{
				AppID:     "tenant-app-id",
				AppSecret: "tenant-app-secret",
			},
		},
	}
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7))

	resolved, err := svc.GetVLMModelWithFingerprint(ctx, "vlm-1")
	if err != nil {
		t.Fatalf("expected WeKnoraCloud VLM to use tenant credential fallback: %v", err)
	}
	if resolved.VLM == nil {
		t.Fatal("expected VLM instance")
	}
	if resolved.Model != model {
		t.Fatal("expected resolved model row")
	}
	if resolved.FingerprintPayload.RemoteModelName != "remote-vision" {
		t.Fatalf("unexpected remote model name: %+v", resolved.FingerprintPayload)
	}
	if _, ok := resolved.FingerprintPayload.ExtraConfig["api_key"]; ok {
		t.Fatalf("sensitive extra_config leaked: %+v", resolved.FingerprintPayload.ExtraConfig)
	}
	if _, ok := resolved.FingerprintPayload.CustomHeaders["Authorization"]; ok {
		t.Fatalf("sensitive custom header leaked: %+v", resolved.FingerprintPayload.CustomHeaders)
	}
	if resolved.ModelFingerprint == "" {
		t.Fatal("expected model fingerprint")
	}
}
