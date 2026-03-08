package interfaces

import (
	"context"
	"io"
	"mime/multipart"
)

// FileService is the interface for file services.
// FileService provides methods to save, retrieve, and delete files.
type FileService interface {
	// CheckConnectivity verifies that the storage backend is reachable and
	// properly configured (e.g. bucket exists, credentials valid).
	CheckConnectivity(ctx context.Context) error
	// SaveFile saves a file.
	SaveFile(ctx context.Context, file *multipart.FileHeader, tenantID uint64, knowledgeID string) (string, error)
	// SaveBytes saves bytes data to a file and returns the file path.
	// If temp is true, the file will be saved to a temporary storage that may auto-expire.
	SaveBytes(ctx context.Context, data []byte, tenantID uint64, fileName string, temp bool) (string, error)
	// GetFile retrieves a file.
	GetFile(ctx context.Context, filePath string) (io.ReadCloser, error)
	// GetFileURL returns a download URL for the file (if supported by the storage backend).
	GetFileURL(ctx context.Context, filePath string) (string, error)
	// DeleteFile deletes a file.
	DeleteFile(ctx context.Context, filePath string) error
}
