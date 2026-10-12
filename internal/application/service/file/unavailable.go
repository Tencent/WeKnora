package file

import (
	"context"
	"io"
	"mime/multipart"

	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// NewUnavailableFileService preserves a denied resolution in callers whose
// interface returns only a FileService. Operations fail without using a fallback
// identity or turning a configuration error into a nil-pointer panic.
func NewUnavailableFileService(err error) interfaces.FileService {
	return &unavailableFileService{err: err}
}

// IsUnavailableFileService identifies a failed resolution before callers try
// a second fallback based on a path's provider scheme.
func IsUnavailableFileService(service interfaces.FileService) bool {
	_, ok := service.(*unavailableFileService)
	return ok
}

type unavailableFileService struct{ err error }

func (s *unavailableFileService) CheckConnectivity(context.Context) error { return s.err }
func (s *unavailableFileService) SaveFile(context.Context, *multipart.FileHeader, uint64, string) (string, error) {
	return "", s.err
}

func (s *unavailableFileService) SaveBytes(context.Context, []byte, uint64, string, bool) (string, error) {
	return "", s.err
}

func (s *unavailableFileService) GetFile(context.Context, string) (io.ReadCloser, error) {
	return nil, s.err
}

func (s *unavailableFileService) GetFileURL(context.Context, string) (string, error) {
	return "", s.err
}
func (s *unavailableFileService) DeleteFile(context.Context, string) error { return s.err }
func (s *unavailableFileService) CopyFile(context.Context, string, uint64, string) (string, error) {
	return "", s.err
}
