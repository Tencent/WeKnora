package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/storageallowlist"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeStorageBackendRepo is a minimal StorageBackendRepository used to exercise
// GetStorageEngineStatus' multi-instance awareness without a database.
type fakeStorageBackendRepo struct{ backends []*types.StorageBackend }

func (f *fakeStorageBackendRepo) Create(context.Context, *types.StorageBackend) error {
	return nil
}
func (f *fakeStorageBackendRepo) GetByID(context.Context, uint64, string) (*types.StorageBackend, error) {
	return nil, nil
}
func (f *fakeStorageBackendRepo) List(context.Context, uint64) ([]*types.StorageBackend, error) {
	return f.backends, nil
}
func (f *fakeStorageBackendRepo) Update(context.Context, *types.StorageBackend) error { return nil }
func (f *fakeStorageBackendRepo) Delete(context.Context, uint64, string) error        { return nil }
func (f *fakeStorageBackendRepo) FindLegacyAlias(context.Context, uint64, string) (*types.StorageBackend, error) {
	return nil, nil
}

func TestGetStorageEngineStatus_IncludesOBS(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv(storageallowlist.AllowListEnv, "")

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/system/storage-engine-status", nil)

	h := &SystemHandler{}
	h.GetStorageEngineStatus(c)

	require.Equal(t, http.StatusOK, w.Code)

	var resp struct {
		Code int `json:"code"`
		Data struct {
			Engines []StorageEngineStatusItem `json:"engines"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Equal(t, 0, resp.Code)

	names := make([]string, 0, len(resp.Data.Engines))
	obsStatus := StorageEngineStatusItem{}
	for _, engine := range resp.Data.Engines {
		names = append(names, engine.Name)
		if engine.Name == "obs" {
			obsStatus = engine
		}
	}
	assert.Contains(t, names, "obs")
	assert.True(t, obsStatus.Allowed)
	assert.False(t, obsStatus.Available)
}

func TestGetStorageEngineStatus_OBSConfiguredFromTenant(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv(storageallowlist.AllowListEnv, "")

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/system/storage-engine-status", nil)
	tenant := &types.Tenant{
		StorageEngineConfig: &types.StorageEngineConfig{
			OBS: &types.OBSEngineConfig{
				Endpoint:   "obs.example.com",
				Region:     "cn-north-4",
				AccessKey:  "ak",
				SecretKey:  "sk",
				BucketName: "bucket",
			},
		},
	}
	c.Set(types.TenantInfoContextKey.String(), tenant)

	h := &SystemHandler{}
	h.GetStorageEngineStatus(c)

	require.Equal(t, http.StatusOK, w.Code)

	var resp struct {
		Data struct {
			Engines []StorageEngineStatusItem `json:"engines"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))

	var obsStatus *StorageEngineStatusItem
	for i := range resp.Data.Engines {
		if resp.Data.Engines[i].Name == "obs" {
			obsStatus = &resp.Data.Engines[i]
			break
		}
	}
	require.NotNil(t, obsStatus)
	assert.True(t, obsStatus.Available)
}

// A workspace that configured COS only through the new multi-instance Storage
// settings (storage_backends), with an empty legacy StorageEngineConfig, must
// still be reported as available.
func TestGetStorageEngineStatus_AvailableFromActiveBackend(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv(storageallowlist.AllowListEnv, "")

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/system/storage-engine-status", nil)
	c.Set(types.TenantInfoContextKey.String(), &types.Tenant{ID: 42})

	h := &SystemHandler{storageBackendRepo: &fakeStorageBackendRepo{backends: []*types.StorageBackend{
		{Provider: "cos", Status: types.StorageBackendStatusActive},
		{Provider: "s3", Status: types.StorageBackendStatusDisabled},
	}}}
	h.GetStorageEngineStatus(c)

	require.Equal(t, http.StatusOK, w.Code)

	var resp struct {
		Data struct {
			Engines []StorageEngineStatusItem `json:"engines"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))

	status := map[string]bool{}
	for _, engine := range resp.Data.Engines {
		status[engine.Name] = engine.Available
	}
	assert.True(t, status["cos"], "active COS backend should be available")
	assert.False(t, status["s3"], "disabled S3 backend should not be available")
}
