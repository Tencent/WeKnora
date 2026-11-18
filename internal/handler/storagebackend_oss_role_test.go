package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type ossRoleTestRepository struct {
	interfaces.StorageBackendRepository
	backend  *types.StorageBackend
	tenantID uint64
}

func (r *ossRoleTestRepository) GetByID(_ context.Context, tenantID uint64, id string) (*types.StorageBackend, error) {
	r.tenantID = tenantID
	if id == "existing" {
		return r.backend, nil
	}
	return nil, nil
}

type ossRoleTestService struct {
	interfaces.StorageBackendService
	tested *types.StorageBackend
}

func (s *ossRoleTestService) Test(_ context.Context, b *types.StorageBackend) error {
	s.tested = b
	return b.Validate()
}

func TestStorageBackendTestRawOSSAuthChanges(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("STORAGE_ALLOW_LIST", "")
	stored := types.StorageBackendConfig{
		Endpoint:        "https://oss.example.com",
		Region:          "cn-beijing",
		BucketName:      "bucket",
		AccessKeyID:     "stored-ak",
		SecretAccessKey: "stored-sk",
	}
	for _, role := range []bool{false, true} {
		name := map[bool]string{false: "masked static keys", true: "tenant role authentication denied"}[role]
		t.Run(name, func(t *testing.T) {
			config := stored
			config.AccessKeyID = types.RedactedSecretPlaceholder
			config.SecretAccessKey = types.RedactedSecretPlaceholder
			if role {
				config.AuthType, config.RoleName = types.OSSAuthECSRAMRole, "zhongtaiOSS"
				config.AccessKeyID, config.SecretAccessKey = "", ""
			}
			body, err := json.Marshal(storageBackendRequest{
				ID:       "existing",
				Name:     "OSS",
				Provider: "oss",
				Config:   config,
			})
			require.NoError(t, err)
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/storage-backends/test", bytes.NewReader(body))
			c.Request.Header.Set("Content-Type", "application/json")
			c.Set(types.TenantIDContextKey.String(), uint64(7))
			repo := &ossRoleTestRepository{backend: &types.StorageBackend{Provider: "oss", Config: stored}}
			svc := &ossRoleTestService{}
			NewStorageBackendHandler(repo, svc).TestRaw(c)
			if role {
				require.Len(t, c.Errors, 1)
				assert.ErrorIs(t, c.Errors[0].Err, types.ErrOSSRoleRequiresEnvironment)
				assert.Nil(t, svc.tested)
			} else {
				require.Empty(t, c.Errors)
				require.Equal(t, http.StatusOK, w.Code)
				require.NotNil(t, svc.tested)
				assert.Equal(t, "stored-ak", svc.tested.Config.AccessKeyID)
				assert.Equal(t, "stored-sk", svc.tested.Config.SecretAccessKey)
			}
			assert.Equal(t, uint64(7), repo.tenantID, "saved credentials must be looked up within the caller's tenant")
			assert.Equal(t, stored, repo.backend.Config, "testing must not mutate saved configuration")
		})
	}
}

func TestStorageBackendTestRawSavedBackendNotFound(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	body := `{"id":"other-tenant","name":"OSS","provider":"oss","config":{}}`
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/storage-backends/test", bytes.NewBufferString(body))
	c.Request.Header.Set("Content-Type", "application/json")
	svc := &ossRoleTestService{}
	NewStorageBackendHandler(&ossRoleTestRepository{}, svc).TestRaw(c)
	require.Len(t, c.Errors, 1)
	assert.Contains(t, c.Errors[0].Error(), "not found")
	assert.Nil(t, svc.tested)
}

func TestStorageBackendTestRawRejectsSavedCredentialRedirect(t *testing.T) {
	gin.SetMode(gin.TestMode)
	stored := types.StorageBackendConfig{
		Endpoint: "https://oss.example.com", Region: "cn-beijing", BucketName: "bucket",
		AccessKeyID: "ak", SecretAccessKey: "sk",
	}
	incoming := stored
	incoming.Endpoint = "https://another.example.com"
	incoming.AccessKeyID, incoming.SecretAccessKey = types.RedactedSecretPlaceholder, types.RedactedSecretPlaceholder
	body, err := json.Marshal(storageBackendRequest{
		ID: "existing", Name: "OSS", Provider: "oss", Config: incoming,
	})
	require.NoError(t, err)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/storage-backends/test", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	svc := &ossRoleTestService{}
	repo := &ossRoleTestRepository{backend: &types.StorageBackend{Provider: "oss", Config: stored}}
	NewStorageBackendHandler(repo, svc).TestRaw(c)
	require.Len(t, c.Errors, 1)
	assert.Contains(t, c.Errors[0].Error(), "immutable")
	assert.Nil(t, svc.tested)
}

func TestIsOSSConfiguredWithRole(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	config := &types.OSSEngineConfig{
		Endpoint:   "https://oss.example.com",
		Region:     "cn-beijing",
		BucketName: "bucket",
		AuthType:   types.OSSAuthECSRAMRole,
	}
	c.Set(types.TenantInfoContextKey.String(), &types.Tenant{
		StorageEngineConfig: &types.StorageEngineConfig{OSS: config},
	})
	h := &SystemHandler{}
	assert.False(t, h.isOSSConfigured(c), "legacy tenant role configs are forbidden")
	config.AuthType = ""
	assert.False(t, h.isOSSConfigured(c), "missing static keys must not implicitly select the role")
}

func TestOSSRoleRejectedInLegacyTenantUpdateAndEngineCheck(t *testing.T) {
	t.Setenv("STORAGE_ALLOW_LIST", "")
	config := &types.OSSEngineConfig{
		Endpoint: "https://oss-cn-beijing.aliyuncs.com", Region: "cn-beijing", BucketName: "system-bucket",
		AuthType: types.OSSAuthECSRAMRole,
	}
	tenant := &types.Tenant{ID: 7}
	ctx := context.WithValue(context.Background(), types.TenantInfoContextKey, tenant)
	body, err := json.Marshal(types.StorageEngineConfig{DefaultProvider: "oss", OSS: config})
	require.NoError(t, err)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPut, "/tenants/kv/storage_engine_config", bytes.NewReader(body))
	c.Request = c.Request.WithContext(ctx)
	c.Request.Header.Set("Content-Type", "application/json")
	h := &TenantHandler{service: &stubTenantService{tenant: tenant}}
	h.updateTenantStorageEngineConfigInternal(c)
	require.Len(t, c.Errors, 1)
	require.ErrorIs(t, c.Errors[0].Err, types.ErrOSSRoleRequiresEnvironment)
	require.Nil(t, tenant.StorageEngineConfig, "rejected config must not mutate the in-memory tenant")

	w := httptest.NewRecorder()
	c, _ = gin.CreateTestContext(w)
	body, err = json.Marshal(StorageCheckRequest{Provider: "oss", OSS: config})
	require.NoError(t, err)
	c.Request = httptest.NewRequest(http.MethodPost, "/system/storage-engine-check", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	(&SystemHandler{}).CheckStorageEngine(c)
	require.Contains(t, w.Body.String(), "only available for environment-configured storage")
}

func TestStorageBackendTestRawCannotOverrideEnvironmentInstance(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	body := `{"id":"existing","name":"override env","provider":"oss","config":{}}`
	c.Request = httptest.NewRequest(http.MethodPost, "/storage-backends/test", bytes.NewBufferString(body))
	c.Request.Header.Set("Content-Type", "application/json")
	svc := &ossRoleTestService{}
	repo := &ossRoleTestRepository{backend: &types.StorageBackend{
		Provider: "oss", Source: types.StorageBackendSourceEnv,
	}}
	NewStorageBackendHandler(repo, svc).TestRaw(c)
	require.Len(t, c.Errors, 1)
	require.Contains(t, c.Errors[0].Error(), "read-only")
	require.Nil(t, svc.tested)
}

func TestStorageBackendTestRawRejectsProviderSpecificCredentialRedirect(t *testing.T) {
	for _, provider := range []string{"cos", "minio"} {
		t.Run(provider, func(t *testing.T) {
			stored := types.StorageBackendConfig{
				Endpoint: "storage.example.com", Region: "region", BucketName: "bucket",
				AccessKeyID: "ak", SecretAccessKey: "sk", UseSSL: true, AppID: "123",
			}
			incoming := stored
			if provider == "cos" {
				incoming.AppID = "456"
			} else {
				incoming.UseSSL = false
			}
			incoming.AccessKeyID = types.RedactedSecretPlaceholder
			incoming.SecretAccessKey = types.RedactedSecretPlaceholder
			body, err := json.Marshal(storageBackendRequest{
				ID: "existing", Name: provider, Provider: provider, Config: incoming,
			})
			require.NoError(t, err)
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/storage-backends/test", bytes.NewReader(body))
			c.Request.Header.Set("Content-Type", "application/json")
			svc := &ossRoleTestService{}
			repo := &ossRoleTestRepository{backend: &types.StorageBackend{Provider: provider, Config: stored}}
			NewStorageBackendHandler(repo, svc).TestRaw(c)
			require.Len(t, c.Errors, 1)
			require.Contains(t, c.Errors[0].Error(), "immutable")
			require.Nil(t, svc.tested)
		})
	}
}
