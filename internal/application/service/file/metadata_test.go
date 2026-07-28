package file

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/assert"
)

type fileServiceWithoutMetadata struct {
	interfaces.FileService
}

func TestBuiltInFileServicesExposeStorageProvider(t *testing.T) {
	services := map[string]interfaces.FileService{
		"local": &localFileService{},
		"minio": &minioFileService{},
		"cos":   &cosFileService{},
		"tos":   &tosFileService{},
		"s3":    &s3FileService{},
		"obs":   &obsFileService{},
		"oss":   &ossFileService{},
		"ks3":   &ks3FileService{},
		"dummy": &DummyFileService{},
	}

	for want, service := range services {
		t.Run(want, func(t *testing.T) {
			assert.Equal(t, want, StorageProvider(service))
		})
	}
}

func TestStorageProviderIsOptional(t *testing.T) {
	service := fileServiceWithoutMetadata{FileService: &DummyFileService{}}

	assert.Empty(t, StorageProvider(service))
}

func TestBackendScopedServiceExposesStorageBackendID(t *testing.T) {
	service := NewBackendScopedFileService("backend-a", &DummyFileService{})

	assert.Equal(t, "backend-a", StorageBackendID(service))
	assert.Empty(t, StorageBackendID(&DummyFileService{}))
}

func TestOBSStoredPathMappingUsesAdapterState(t *testing.T) {
	service := &obsFileService{
		bucketName:  "tenant-bucket",
		proxyDomain: "https://files.example.com/obs",
	}
	proxyPath := "https://files.example.com/obs/weknora/7/object.bin"
	canonicalPath := "obs://tenant-bucket/weknora/7/object.bin"

	assert.Equal(t, canonicalPath, CanonicalStoredPath(service, proxyPath))
	assert.Equal(t, proxyPath, ServiceStoredPath(service, canonicalPath))
	assert.Equal(t, "https://cdn.example.com/object.bin", CanonicalStoredPath(
		service,
		"https://cdn.example.com/object.bin",
	))
}
