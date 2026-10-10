package types

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOSSAuthenticationValidation(t *testing.T) {
	for _, tc := range []struct {
		name, mode, role, ak, sk string
		valid                    bool
	}{
		{"legacy static keys", "", "", "ak", "sk", true},
		{"explicit static keys", OSSAuthAccessKey, "", "ak", "sk", true},
		{"missing keys do not select role", "", "", "", "", false},
		{"partial static pair", OSSAuthAccessKey, "", "ak", "", false},
		{"named role without keys", OSSAuthECSRAMRole, "zhongtaiOSS", "", "", true},
		{"discover role without keys", OSSAuthECSRAMRole, "", "", "", true},
		{"role rejects stored keys", OSSAuthECSRAMRole, "", "ak", "sk", false},
		{"unknown mode", "default", "", "ak", "sk", false},
		{"static mode rejects role", OSSAuthAccessKey, "role", "ak", "sk", false},
		{"role rejects traversal", OSSAuthECSRAMRole, "../other", "", "", false},
		{"role rejects parent directory", OSSAuthECSRAMRole, "..", "", "", false},
		{"role rejects current directory", OSSAuthECSRAMRole, ".", "", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			config := StorageBackendConfig{
				Endpoint:        "https://oss-cn-beijing-internal.aliyuncs.com",
				Region:          "cn-beijing",
				BucketName:      "sig-zhongtai",
				AuthType:        tc.mode,
				RoleName:        tc.role,
				AccessKeyID:     tc.ak,
				SecretAccessKey: tc.sk,
			}
			err := config.ValidateForProvider("oss")
			if tc.valid {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
		})
	}
}

func TestOSSRoleConfigurationRoundTrip(t *testing.T) {
	config := StorageBackendConfig{
		Endpoint:       "https://oss-cn-beijing-internal.aliyuncs.com",
		Region:         "cn-beijing",
		BucketName:     "sig-zhongtai",
		AuthType:       OSSAuthECSRAMRole,
		RoleName:       "zhongtaiOSS",
		UseTempBucket:  true,
		TempBucketName: "temp",
		TempRegion:     "cn-shanghai",
	}
	backend := StorageBackend{Provider: "oss", Config: config}
	legacy := backend.ToStorageEngineConfig()
	require.NoError(t, legacy.OSS.Validate())
	assert.Equal(t, config, StorageBackendFromLegacy(7, "oss", legacy).Config)

	stored, err := config.Value()
	require.NoError(t, err)
	var restored StorageBackendConfig
	require.NoError(t, restored.Scan(stored))
	assert.Equal(t, config, restored)
	encoded, err := json.Marshal(restored)
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), "access_key_id")
	assert.NotContains(t, string(encoded), "secret_access_key")

	static := config
	static.AuthType, static.RoleName, static.AccessKeyID, static.SecretAccessKey = OSSAuthAccessKey, "", "ak", "sk"
	assert.Equal(t, static.LocationKey("oss"), config.LocationKey("oss"),
		"switching authentication must not change object identity")
	assert.Empty(t, config.MergeSecrets(static).AccessKeyID,
		"cleared keys must not be restored when switching to a role")
	merged := MergeStorageEngineConfigForUpdate(
		backend.ToStorageEngineConfig(), (&StorageBackend{Provider: "oss", Config: static}).ToStorageEngineConfig(),
	)
	assert.Empty(t, merged.OSS.AccessKey, "legacy config updates must also clear old static keys")
	assert.Empty(t, merged.OSS.SecretKey)
}

func TestOSSRoleConfigurationFromEnvironment(t *testing.T) {
	t.Setenv("STORAGE_TYPE", "oss")
	t.Setenv("OSS_AUTH_TYPE", OSSAuthECSRAMRole)
	t.Setenv("OSS_ROLE_NAME", "zhongtaiOSS")
	t.Setenv("OSS_ENDPOINT", "https://oss-cn-beijing-internal.aliyuncs.com")
	t.Setenv("OSS_REGION", "cn-beijing")
	t.Setenv("OSS_BUCKET_NAME", "sig-zhongtai")
	t.Setenv("OSS_ACCESS_KEY", "")
	t.Setenv("OSS_SECRET_KEY", "")
	backend := StorageBackendFromEnvironment(7)
	require.NoError(t, backend.Validate())
	assert.Equal(t, OSSAuthECSRAMRole, backend.Config.AuthType)
	assert.Equal(t, "zhongtaiOSS", backend.ToStorageEngineConfig().OSS.RoleName)
}
