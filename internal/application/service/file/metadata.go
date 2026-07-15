package file

import (
	"strings"

	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

type storageProvider interface {
	StorageProvider() string
}

type storedPathMapper interface {
	CanonicalStoredPath(string) string
	ServiceStoredPath(string) string
}

// StorageProvider returns optional, non-secret provider identity advertised by a file service.
func StorageProvider(service interfaces.FileService) string {
	provider, ok := service.(storageProvider)
	if !ok {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(provider.StorageProvider()))
}

// CanonicalStoredPath maps a service-returned path to its stable persisted form when supported.
func CanonicalStoredPath(service interfaces.FileService, path string) string {
	mapper, ok := service.(storedPathMapper)
	if !ok {
		return path
	}
	return mapper.CanonicalStoredPath(path)
}

// ServiceStoredPath maps a stable persisted path to the form expected by the service when supported.
func ServiceStoredPath(service interfaces.FileService, path string) string {
	mapper, ok := service.(storedPathMapper)
	if !ok {
		return path
	}
	return mapper.ServiceStoredPath(path)
}

func (*localFileService) StorageProvider() string { return "local" }
func (*minioFileService) StorageProvider() string { return "minio" }
func (*cosFileService) StorageProvider() string   { return "cos" }
func (*tosFileService) StorageProvider() string   { return "tos" }
func (*s3FileService) StorageProvider() string    { return "s3" }
func (*obsFileService) StorageProvider() string   { return "obs" }
func (*ossFileService) StorageProvider() string   { return "oss" }
func (*ks3FileService) StorageProvider() string   { return "ks3" }
func (*DummyFileService) StorageProvider() string { return "dummy" }

func (s *obsFileService) CanonicalStoredPath(path string) string {
	proxyDomain := strings.TrimRight(strings.TrimSpace(s.proxyDomain), "/")
	bucketName := strings.TrimSpace(s.bucketName)
	if proxyDomain == "" || bucketName == "" || !strings.HasPrefix(path, proxyDomain+"/") {
		return path
	}
	objectKey := strings.TrimPrefix(path, proxyDomain+"/")
	if objectKey == "" {
		return path
	}
	return "obs://" + bucketName + "/" + objectKey
}

func (s *obsFileService) ServiceStoredPath(path string) string {
	proxyDomain := strings.TrimRight(strings.TrimSpace(s.proxyDomain), "/")
	bucketName := strings.TrimSpace(s.bucketName)
	canonicalPrefix := "obs://" + bucketName + "/"
	if proxyDomain == "" || bucketName == "" || !strings.HasPrefix(path, canonicalPrefix) {
		return path
	}
	objectKey := strings.TrimPrefix(path, canonicalPrefix)
	if objectKey == "" {
		return path
	}
	return proxyDomain + "/" + objectKey
}
