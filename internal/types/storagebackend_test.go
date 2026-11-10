package types

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStorageBackendPathRoundTrip(t *testing.T) {
	path := BuildStorageBackendPath("backend-a", "cos://bucket/region/7/file.pdf")
	id, inner, ok := ParseStorageBackendPath(path)
	require.True(t, ok)
	assert.Equal(t, "backend-a", id)
	assert.Equal(t, "cos://bucket/region/7/file.pdf", inner)
	assert.Equal(t, "cos", ParseProviderScheme(path))
}

func TestSharesStorageBackendWithUsesConcreteInstance(t *testing.T) {
	aID, bID := "cos-a", "cos-b"
	a := &KnowledgeBase{StorageBackendID: &aID, StorageProviderConfig: &StorageProviderConfig{Provider: "cos"}}
	b := &KnowledgeBase{StorageBackendID: &bID, StorageProviderConfig: &StorageProviderConfig{Provider: "cos"}}
	assert.False(t, a.SharesStorageBackendWith(b, "", "cos"))

	b.StorageBackendID = &aID
	assert.True(t, a.SharesStorageBackendWith(b, "", "cos"))
}

func TestNewStorageBackendResponseMasksCredentials(t *testing.T) {
	backend := &StorageBackend{Config: StorageBackendConfig{AccessKeyID: "id", SecretAccessKey: "secret"}}
	response := NewStorageBackendResponse(backend)
	assert.Equal(t, RedactedSecretPlaceholder, response.Config.AccessKeyID)
	assert.Equal(t, RedactedSecretPlaceholder, response.Config.SecretAccessKey)
	assert.Equal(t, "id", backend.Config.AccessKeyID)
}

func TestStorageBackendFromEnvironment(t *testing.T) {
	t.Setenv("STORAGE_TYPE", "s3")
	t.Setenv("S3_ENDPOINT", "https://s3.example.com")
	t.Setenv("S3_REGION", "ap-test-1")
	t.Setenv("S3_ACCESS_KEY", "access")
	t.Setenv("S3_SECRET_KEY", "secret")
	t.Setenv("S3_BUCKET_NAME", "bucket")

	backend := StorageBackendFromEnvironment(42)
	require.NotNil(t, backend)
	assert.Equal(t, uint64(42), backend.TenantID)
	assert.Equal(t, "s3", backend.Provider)
	assert.Equal(t, StorageBackendSourceEnv, backend.Source)
	assert.True(t, backend.LegacyAlias)
	assert.Equal(t, "bucket", backend.Config.BucketName)
}

func TestStorageBackendRejectsTraversingPathPrefix(t *testing.T) {
	backend := &StorageBackend{
		TenantID: 1, Name: "unsafe", Provider: "local",
		Config: StorageBackendConfig{PathPrefix: "../outside"},
	}
	require.Error(t, backend.Validate())
}
