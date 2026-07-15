package handler

import (
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
