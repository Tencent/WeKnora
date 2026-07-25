package file

import (
	"context"
	"fmt"
	"io"
	"mime/multipart"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	secutils "github.com/Tencent/WeKnora/internal/utils"
)

// backendScopedFileService makes the storage instance part of every newly
// persisted path while delegating actual I/O to the existing provider driver.
type backendScopedFileService struct {
	backendID string
	inner     interfaces.FileService
}

func NewBackendScopedFileService(backendID string, inner interfaces.FileService) interfaces.FileService {
	return &backendScopedFileService{backendID: backendID, inner: inner}
}

func (s *backendScopedFileService) unwrap(path string) (string, error) {
	id, inner, ok := types.ParseStorageBackendPath(path)
	if !ok {
		return path, nil
	}
	if id != s.backendID {
		return "", fmt.Errorf("storage backend mismatch")
	}
	return inner, nil
}

func (s *backendScopedFileService) wrap(path string) string {
	return types.BuildStorageBackendPath(s.backendID, path)
}
func (s *backendScopedFileService) CheckConnectivity(ctx context.Context) error {
	return s.inner.CheckConnectivity(ctx)
}
func (s *backendScopedFileService) SaveFile(ctx context.Context, f *multipart.FileHeader, tenantID uint64, knowledgeID string) (string, error) {
	p, err := s.inner.SaveFile(ctx, f, tenantID, knowledgeID)
	if err != nil {
		return "", err
	}
	return s.wrap(p), nil
}
func (s *backendScopedFileService) SaveBytes(ctx context.Context, data []byte, tenantID uint64, name string, temp bool) (string, error) {
	p, err := s.inner.SaveBytes(ctx, data, tenantID, name, temp)
	if err != nil {
		return "", err
	}
	return s.wrap(p), nil
}
func (s *backendScopedFileService) GetFile(ctx context.Context, path string) (io.ReadCloser, error) {
	p, err := s.unwrap(path)
	if err != nil {
		return nil, err
	}
	return s.inner.GetFile(ctx, p)
}
func (s *backendScopedFileService) GetFileURL(ctx context.Context, path string) (string, error) {
	p, err := s.unwrap(path)
	if err != nil {
		return "", err
	}
	result, err := s.inner.GetFileURL(ctx, p)
	if err != nil {
		return "", err
	}
	scoped := s.wrap(p)
	if result == p {
		return scoped, nil
	}
	// Local storage may return an app-level presigned URL. Re-sign it with
	// the scoped path so the proxy resolves the exact local instance instead
	// of falling back to another backend of the same provider.
	if u, parseErr := url.Parse(result); parseErr == nil && strings.HasSuffix(u.Path, "/api/v1/files/presigned") && u.Query().Get("file_path") == p {
		basePath := strings.TrimSuffix(u.Path, "/api/v1/files/presigned")
		baseURL := u.Scheme + "://" + u.Host + basePath
		ttl := time.Duration(0)
		if expires, convErr := strconv.ParseInt(u.Query().Get("expires"), 10, 64); convErr == nil {
			ttl = time.Until(time.Unix(expires, 0))
		}
		if signed, signErr := secutils.SignFileURL(baseURL, scoped, secutils.ParseTenantIDFromStoragePath(scoped), ttl); signErr == nil {
			return signed, nil
		}
	}
	return result, nil
}
func (s *backendScopedFileService) DeleteFile(ctx context.Context, path string) error {
	p, err := s.unwrap(path)
	if err != nil {
		return err
	}
	return s.inner.DeleteFile(ctx, p)
}
func (s *backendScopedFileService) CopyFile(ctx context.Context, path string, tenantID uint64, knowledgeID string) (string, error) {
	p, err := s.unwrap(path)
	if err != nil {
		return "", err
	}
	result, err := s.inner.CopyFile(ctx, p, tenantID, knowledgeID)
	if err != nil {
		return "", err
	}
	return s.wrap(result), nil
}
