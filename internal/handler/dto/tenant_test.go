package dto

import (
	"encoding/json"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTenantResponse_ViewerOmitsSecrets(t *testing.T) {
	tenant := sampleSecretTenant()
	body, err := json.Marshal(NewTenantResponse(viewerContext(), tenant))
	require.NoError(t, err)
	s := string(body)
	assert.NotContains(t, s, "tenant-api-key-123")
	assert.NotContains(t, s, "legacy-search-secret-999")
	assert.NotContains(t, s, "wk-app-secret-def")
	assert.NotContains(t, s, "parser-secret-123")
	assert.NotContains(t, s, "minio-secret-789")
	assert.NotContains(t, s, "web_search_config")
	assert.NotContains(t, s, "parser_engine_config")
	assert.NotContains(t, s, "storage_engine_config")
	assert.NotContains(t, s, "credentials")
}

func TestTenantResponse_OwnerGetsAPIKeyOnly(t *testing.T) {
	tenant := sampleSecretTenant()
	body, err := json.Marshal(NewTenantResponse(ownerContext(), tenant))
	require.NoError(t, err)
	s := string(body)
	assert.Contains(t, s, "tenant-api-key-123")
	assert.NotContains(t, s, "legacy-search-secret-999")
	assert.NotContains(t, s, "parser-secret-123")
}

func TestTenantResponse_AdminGetsRedactedIntegrationConfigs(t *testing.T) {
	tenant := sampleSecretTenant()
	resp := NewTenantResponse(adminContext(), tenant)
	require.NotNil(t, resp.WebSearchConfig)
	assert.Equal(t, types.RedactedSecretPlaceholder, resp.WebSearchConfig.ProxyURL)
	assert.Empty(t, resp.WebSearchConfig.APIKey)
	require.NotNil(t, resp.ParserEngineConfig)
	assert.Equal(t, types.RedactedSecretPlaceholder, resp.ParserEngineConfig.MinerUAPIKey)
	require.NotNil(t, resp.StorageEngineConfig.MinIO)
	assert.Equal(t, types.RedactedSecretPlaceholder, resp.StorageEngineConfig.MinIO.SecretAccessKey)
}

func TestTenantResponsesCrossTenant_RedactsEvenForOwnerContext(t *testing.T) {
	tenant := sampleSecretTenant()
	body, err := json.Marshal(NewTenantResponsesCrossTenant([]*types.Tenant{tenant}))
	require.NoError(t, err)
	s := string(body)
	assert.NotContains(t, s, "tenant-api-key-123")
	assert.NotContains(t, s, "parser-secret-123")
}

func sampleSecretTenant() *types.Tenant {
	return &types.Tenant{
		ID:     42,
		Name:   "tenant",
		APIKey: "tenant-api-key-123",
		WebSearchConfig: &types.WebSearchConfig{
			APIKey:   "legacy-search-secret-999",
			ProxyURL: "http://proxy.internal:8080",
		},
		Credentials: &types.CredentialsConfig{
			WeKnoraCloud: &types.WeKnoraCloudCredentials{
				AppID:     "wk-app-id-abc",
				AppSecret: "wk-app-secret-def",
			},
		},
		ParserEngineConfig: &types.ParserEngineConfig{
			MinerUAPIKey:          "parser-secret-123",
			PaddleOCRVLCloudToken: "paddle-secret-456",
		},
		StorageEngineConfig: &types.StorageEngineConfig{
			DefaultProvider: "minio",
			MinIO: &types.MinIOEngineConfig{
				AccessKeyID:     "minio-access-id",
				SecretAccessKey: "minio-secret-789",
			},
		},
	}
}
