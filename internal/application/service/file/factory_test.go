package file

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveOBSStorageConfigPrefersTenantConfig(t *testing.T) {
	t.Setenv("OBS_ENDPOINT", "https://global.example.com")
	t.Setenv("OBS_REGION", "global-region")
	t.Setenv("OBS_ACCESS_KEY", "global-access-key")
	t.Setenv("OBS_SECRET_KEY", "global-secret-key")
	t.Setenv("OBS_BUCKET_NAME", "global-bucket")
	t.Setenv("OBS_PATH_PREFIX", "global-prefix/")

	config, err := resolveOBSStorageConfig(&types.StorageEngineConfig{
		OBS: &types.OBSEngineConfig{
			Endpoint:   "https://tenant.example.com",
			Region:     "tenant-region",
			AccessKey:  "tenant-access-key",
			SecretKey:  "tenant-secret-key",
			BucketName: "tenant-bucket",
			PathPrefix: "tenant-prefix/",
		},
	})

	require.NoError(t, err)
	assert.Equal(t, "https://tenant.example.com", config.endpoint)
	assert.Equal(t, "tenant-region", config.region)
	assert.Equal(t, "tenant-access-key", config.accessKey)
	assert.Equal(t, "tenant-secret-key", config.secretKey)
	assert.Equal(t, "tenant-bucket", config.bucketName)
	assert.Equal(t, "tenant-prefix/", config.pathPrefix)
}

func TestResolveOBSStorageConfigFillsMissingTenantFieldsFromGlobalConfig(t *testing.T) {
	t.Setenv("OBS_ENDPOINT", "https://global.example.com")
	t.Setenv("OBS_REGION", "global-region")
	t.Setenv("OBS_ACCESS_KEY", "global-access-key")
	t.Setenv("OBS_SECRET_KEY", "global-secret-key")
	t.Setenv("OBS_BUCKET_NAME", "global-bucket")

	config, err := resolveOBSStorageConfig(&types.StorageEngineConfig{
		OBS: &types.OBSEngineConfig{Endpoint: "https://tenant.example.com"},
	})

	require.NoError(t, err)
	assert.Equal(t, "https://tenant.example.com", config.endpoint)
	assert.Equal(t, "global-region", config.region)
	assert.Equal(t, "global-access-key", config.accessKey)
	assert.Equal(t, "global-secret-key", config.secretKey)
	assert.Equal(t, "global-bucket", config.bucketName)
}

func TestResolveOBSStorageConfigAppliesGlobalDefaultsWithoutTenantOBSConfig(t *testing.T) {
	t.Setenv("OBS_ENDPOINT", "https://global.example.com")
	t.Setenv("OBS_REGION", "")
	t.Setenv("OBS_ACCESS_KEY", "global-access-key")
	t.Setenv("OBS_SECRET_KEY", "global-secret-key")
	t.Setenv("OBS_BUCKET_NAME", "global-bucket")
	t.Setenv("OBS_PATH_PREFIX", "")

	config, err := resolveOBSStorageConfig(nil)

	require.NoError(t, err)
	assert.Equal(t, "cn-north-4", config.region)
	assert.Equal(t, "weknora/", config.pathPrefix)
}
