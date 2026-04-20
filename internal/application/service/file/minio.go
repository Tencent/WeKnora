package file

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"mime/multipart"
	"path/filepath"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/Tencent/WeKnora/internal/utils"
	"github.com/google/uuid"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// minioFileService MinIO file service implementation
type minioFileService struct {
	client     *minio.Client
	bucketName string
}

// newMinioClient creates a bare minioFileService with just the SDK client initialised.
// Shared by NewMinioFileService (which also ensures the bucket exists) and
// CheckMinioConnectivity (read-only probe).
func newMinioClient(endpoint, accessKeyID, secretAccessKey, bucketName string, useSSL bool) (*minioFileService, error) {
	client, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(accessKeyID, secretAccessKey, ""),
		Secure: useSSL,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to initialize MinIO client: %w", err)
	}
	return &minioFileService{client: client, bucketName: bucketName}, nil
}

// NewMinioFileService creates a MinIO file service.
// It verifies that the bucket exists and creates it if missing.
func NewMinioFileService(endpoint,
	accessKeyID, secretAccessKey, bucketName string, useSSL bool,
) (interfaces.FileService, error) {
	svc, err := newMinioClient(endpoint, accessKeyID, secretAccessKey, bucketName, useSSL)
	if err != nil {
		return nil, err
	}

	exists, err := svc.client.BucketExists(context.Background(), bucketName)
	if err != nil {
		return nil, fmt.Errorf("failed to check bucket: %w", err)
	}
	if !exists {
		if err = svc.client.MakeBucket(context.Background(), bucketName, minio.MakeBucketOptions{}); err != nil {
			return nil, fmt.Errorf("failed to create bucket: %w", err)
		}
	}

	return svc, nil
}

// CheckConnectivity verifies MinIO is reachable and, if a bucket is configured,
// that the bucket exists. This is a read-only probe — it never creates a bucket.
func (s *minioFileService) CheckConnectivity(ctx context.Context) error {
	checkCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	if s.bucketName != "" {
		exists, err := s.client.BucketExists(checkCtx, s.bucketName)
		if err != nil {
			return err
		}
		if !exists {
			return fmt.Errorf("bucket %q does not exist", s.bucketName)
		}
		return nil
	}
	_, err := s.client.ListBuckets(checkCtx)
	return err
}

// CheckMinioConnectivity tests MinIO connectivity using the provided credentials.
// It creates a temporary service instance internally and delegates to CheckConnectivity.
func CheckMinioConnectivity(ctx context.Context, endpoint, accessKeyID, secretAccessKey, bucketName string, useSSL bool) error {
	svc, err := newMinioClient(endpoint, accessKeyID, secretAccessKey, bucketName, useSSL)
	if err != nil {
		return err
	}
	return svc.CheckConnectivity(ctx)
}

// parseMinioFilePath extracts the object name from a provider scheme: minio://{bucket}/{objectKey}
func (s *minioFileService) parseMinioFilePath(filePath string) (string, error) {
	// Provider scheme format: minio://{bucket}/{objectKey}
	const prefix = "minio://"
	if !strings.HasPrefix(filePath, prefix) {
		return "", fmt.Errorf("invalid MinIO file path: %s", filePath)
	}
	rest := strings.TrimPrefix(filePath, prefix)
	parts := strings.SplitN(rest, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", fmt.Errorf("invalid MinIO file path: %s", filePath)
	}
	if parts[0] != s.bucketName {
		return "", fmt.Errorf("bucket mismatch in path: got %s, want %s", parts[0], s.bucketName)
	}
	if err := utils.SafeObjectKey(parts[1]); err != nil {
		return "", fmt.Errorf("invalid file path: %w", err)
	}
	return parts[1], nil
}

// SaveFile saves a file to MinIO
func (s *minioFileService) SaveFile(ctx context.Context,
	file *multipart.FileHeader, tenantID uint64, knowledgeID string,
) (string, error) {
	// Generate object name
	ext := filepath.Ext(file.Filename)
	objectName := fmt.Sprintf("%d/%s/%s%s", tenantID, knowledgeID, uuid.New().String(), ext)

	// Open file
	src, err := file.Open()
	if err != nil {
		return "", fmt.Errorf("failed to open file: %w", err)
	}
	defer src.Close()

	// Upload file to MinIO
	_, err = s.client.PutObject(ctx, s.bucketName, objectName, src, file.Size, minio.PutObjectOptions{
		ContentType: file.Header.Get("Content-Type"),
	})
	if err != nil {
		return "", fmt.Errorf("failed to upload file to MinIO: %w", err)
	}

	return fmt.Sprintf("minio://%s/%s", s.bucketName, objectName), nil
}

// GetFile gets a file from MinIO
func (s *minioFileService) GetFile(ctx context.Context, filePath string) (io.ReadCloser, error) {
	objectName, err := s.parseMinioFilePath(filePath)
	if err != nil {
		return nil, err
	}
	obj, err := s.client.GetObject(ctx, s.bucketName, objectName, minio.GetObjectOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get file from MinIO: %w", err)
	}
	return obj, nil
}

// DeleteFile deletes a file
func (s *minioFileService) DeleteFile(ctx context.Context, filePath string) error {
	objectName, err := s.parseMinioFilePath(filePath)
	if err != nil {
		return err
	}
	if err := s.client.RemoveObject(ctx, s.bucketName, objectName, minio.RemoveObjectOptions{
		GovernanceBypass: true,
	}); err != nil {
		return fmt.Errorf("failed to delete file: %w", err)
	}
	return nil
}

// SaveBytes saves bytes data to MinIO and returns the file path
// temp parameter is ignored for MinIO (no auto-expiration support in this implementation)
func (s *minioFileService) SaveBytes(ctx context.Context, data []byte, tenantID uint64, fileName string, temp bool) (string, error) {
	safeName, err := utils.SafeFileName(fileName)
	if err != nil {
		return "", fmt.Errorf("invalid file name: %w", err)
	}
	ext := filepath.Ext(safeName)
	objectName := fmt.Sprintf("%d/exports/%s%s", tenantID, uuid.New().String(), ext)

	// Upload bytes to MinIO
	reader := bytes.NewReader(data)
	_, err = s.client.PutObject(ctx, s.bucketName, objectName, reader, int64(len(data)), minio.PutObjectOptions{
		ContentType: utils.GetContentTypeByExt(ext),
	})
	if err != nil {
		return "", fmt.Errorf("failed to upload bytes to MinIO: %w", err)
	}

	return fmt.Sprintf("minio://%s/%s", s.bucketName, objectName), nil
}

// GetFileURL returns a presigned download URL for the file
func (s *minioFileService) GetFileURL(ctx context.Context, filePath string) (string, error) {
	objectName, err := s.parseMinioFilePath(filePath)
	if err != nil {
		return "", err
	}
	presignedURL, err := s.client.PresignedGetObject(ctx, s.bucketName, objectName, 24*time.Hour, nil)
	if err != nil {
		return "", fmt.Errorf("failed to generate presigned URL: %w", err)
	}
	return presignedURL.String(), nil
}
