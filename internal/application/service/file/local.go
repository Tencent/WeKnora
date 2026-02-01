package file

import (
	"context"
	"fmt"
	"io"
	"mime/multipart"
	"os"
	"path/filepath"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// localFileService implements the FileService interface for local file system storage
type localFileService struct {
	baseDir string // Base directory for file storage
}

// NewLocalFileService creates a new local file service instance
func NewLocalFileService(baseDir string) interfaces.FileService {
	return &localFileService{
		baseDir: baseDir,
	}
}

// SaveFile stores an uploaded file to the local file system
// The file is stored in a directory structure: baseDir/tenantID/knowledgeID/filename
// Returns the full file path or an error if saving fails
func (s *localFileService) SaveFile(ctx context.Context,
	file *multipart.FileHeader, tenantID uint64, knowledgeID string,
) (string, error) {
	logger.Info(ctx, "Starting to save file locally")
	logger.Infof(ctx, "File information: name=%s, size=%d, tenant ID=%d, knowledge ID=%s",
		file.Filename, file.Size, tenantID, knowledgeID)

	// Create storage directory with tenant and knowledge ID
	dir := filepath.Join(s.baseDir, fmt.Sprintf("%d", tenantID), knowledgeID)
	logger.Infof(ctx, "Creating directory: %s", dir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		logger.Errorf(ctx, "Failed to create directory: %v", err)
		return "", fmt.Errorf("failed to create directory: %w", err)
	}

	// Generate unique filename using timestamp
	ext := filepath.Ext(file.Filename)
	filename := fmt.Sprintf("%d%s", time.Now().UnixNano(), ext)
	filePath := filepath.Join(dir, filename)
	logger.Infof(ctx, "Generated file path: %s", filePath)

	// Open source file for reading
	logger.Info(ctx, "Opening source file")
	src, err := file.Open()
	if err != nil {
		logger.Errorf(ctx, "Failed to open source file: %v", err)
		return "", fmt.Errorf("failed to open file: %w", err)
	}
	defer func() {
		if closeErr := src.Close(); closeErr != nil {
			logger.Warnf(ctx, "Failed to close source file: %v", closeErr)
		}
	}()

	// Create destination file for writing
	logger.Info(ctx, "Creating destination file")
	dst, err := os.Create(filePath)
	if err != nil {
		logger.Errorf(ctx, "Failed to create destination file: %v", err)
		return "", fmt.Errorf("failed to create file: %w", err)
	}
	defer func() {
		if closeErr := dst.Close(); closeErr != nil {
			logger.Warnf(ctx, "Failed to close destination file: %v", closeErr)
		}
	}()

	// Copy content from source to destination
	logger.Info(ctx, "Copying file content")
	if _, err := io.Copy(dst, src); err != nil {
		logger.Errorf(ctx, "Failed to copy file content: %v", err)
		return "", fmt.Errorf("failed to save file: %w", err)
	}

	logger.Infof(ctx, "File saved successfully: %s", filePath)
	return filePath, nil
}

// GetFile retrieves a file from the local file system by its path
// Returns a ReadCloser for reading the file content
func (s *localFileService) GetFile(ctx context.Context, filePath string) (io.ReadCloser, error) {
	logger.Infof(ctx, "Getting file: %s", filePath)

	// Open the file for reading
	file, err := os.Open(filePath)
	if err != nil {
		logger.Errorf(ctx, "Failed to open file: %v", err)
		return nil, fmt.Errorf("failed to open file: %w", err)
	}

	logger.Info(ctx, "File opened successfully")
	return file, nil
}

// DeleteFile removes a file from the local file system
// Returns an error if deletion fails
func (s *localFileService) DeleteFile(ctx context.Context, filePath string) error {
	logger.Infof(ctx, "Deleting file: %s", filePath)

	// Remove the file
	err := os.Remove(filePath)
	if err != nil {
		logger.Errorf(ctx, "Failed to delete file: %v", err)
		return fmt.Errorf("failed to delete file: %w", err)
	}

	logger.Info(ctx, "File deleted successfully")
	return nil
}

// SaveBytes saves bytes data to a file and returns the file path
// temp parameter is ignored for local storage (no auto-expiration support)
func (s *localFileService) SaveBytes(ctx context.Context, data []byte, tenantID uint64, fileName string, temp bool) (string, error) {
	logger.Infof(ctx, "Saving bytes data: fileName=%s, size=%d, tenantID=%d, temp=%v", fileName, len(data), tenantID, temp)

	// Create storage directory with tenant ID
	dir := filepath.Join(s.baseDir, fmt.Sprintf("%d", tenantID), "exports")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		logger.Errorf(ctx, "Failed to create directory: %v", err)
		return "", fmt.Errorf("failed to create directory: %w", err)
	}

	// Generate unique filename using timestamp
	ext := filepath.Ext(fileName)
	baseName := fileName[:len(fileName)-len(ext)]
	uniqueFileName := fmt.Sprintf("%s_%d%s", baseName, time.Now().UnixNano(), ext)
	filePath := filepath.Join(dir, uniqueFileName)

	// Write data to file
	if err := os.WriteFile(filePath, data, 0o644); err != nil {
		logger.Errorf(ctx, "Failed to write file: %v", err)
		return "", fmt.Errorf("failed to write file: %w", err)
	}

	logger.Infof(ctx, "Bytes data saved successfully: %s", filePath)
	return filePath, nil
}

// GetFileURL returns a download URL for the file
// For local storage, returns the file path itself (no URL support)
func (s *localFileService) GetFileURL(_ context.Context, filePath string) (string, error) {
	// Local storage doesn't support URLs, return the path
	return filePath, nil
}
