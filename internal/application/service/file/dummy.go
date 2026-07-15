package file

import (
	"bytes"
	"context"
	"io"
	"io/fs"
	"mime/multipart"
	"sync"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/google/uuid"
)

// DummyFileService keeps byte objects in memory for tests and no-storage modes.
type DummyFileService struct {
	mu      sync.Mutex
	objects map[string][]byte
}

// CheckConnectivity always succeeds for the dummy service.
func (s *DummyFileService) CheckConnectivity(ctx context.Context) error {
	return nil
}

// NewDummyFileService creates a new instance of DummyFileService
func NewDummyFileService() interfaces.FileService {
	return &DummyFileService{objects: make(map[string][]byte)}
}

// SaveFile pretends to save a file but just returns a random UUID
// This is useful for testing without actual file operations
func (s *DummyFileService) SaveFile(ctx context.Context,
	file *multipart.FileHeader, tenantID uint64, knowledgeID string,
) (string, error) {
	return uuid.New().String(), nil
}

// GetFile returns a cloned view of a byte object saved by SaveBytes.
func (s *DummyFileService) GetFile(ctx context.Context, filePath string) (io.ReadCloser, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, ok := s.objects[filePath]
	if !ok {
		return nil, fs.ErrNotExist
	}
	return io.NopCloser(bytes.NewReader(bytes.Clone(data))), nil
}

func (s *DummyFileService) DeleteFile(ctx context.Context, filePath string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.objects, filePath)
	return nil
}

// SaveBytes creates a uniquely owned in-memory object.
func (s *DummyFileService) SaveBytes(ctx context.Context, data []byte, tenantID uint64, fileName string, temp bool) (string, error) {
	path := "dummy://" + uuid.New().String()
	s.mu.Lock()
	if s.objects == nil {
		s.objects = make(map[string][]byte)
	}
	s.objects[path] = bytes.Clone(data)
	s.mu.Unlock()
	return path, nil
}

// CopyFile is a no-op for the dummy service: it logs a warning and returns the
// source path unchanged (the shared reference is intentional in this stub).
func (s *DummyFileService) CopyFile(ctx context.Context, srcPath string, tenantID uint64, knowledgeID string) (string, error) {
	logger.Warnf(ctx, "[dummy] CopyFile no-op: returning source path %q unchanged (no real copy performed)", srcPath)
	return srcPath, nil
}

// GetFileURL returns the file path as URL (dummy implementation)
func (s *DummyFileService) GetFileURL(ctx context.Context, filePath string) (string, error) {
	return filePath, nil
}
