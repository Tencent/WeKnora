package file

import (
	"context"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"

	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/google/uuid"
	"github.com/tencentyun/cos-go-sdk-v5"
)

// cosFileService implements the FileService interface for Tencent Cloud COS
type cosFileService struct {
	client        *cos.Client
	bucketURL     string
	cosPathPrefix string
}

// NewCosFileService creates a new COS file service instance
func NewCosFileService(bucketName, region, secretId, secretKey, cosPathPrefix string) (interfaces.FileService, error) {
	bucketURL := fmt.Sprintf("https://%s.cos.%s.myqcloud.com/", bucketName, region)
	u, err := url.Parse(bucketURL)
	if err != nil {
		return nil, fmt.Errorf("failed to parse bucketURL: %w", err)
	}
	b := &cos.BaseURL{BucketURL: u}
	client := cos.NewClient(b, &http.Client{
		Transport: &cos.AuthorizationTransport{
			SecretID:  secretId,
			SecretKey: secretKey,
		},
	})
	return &cosFileService{
		client:        client,
		bucketURL:     bucketURL,
		cosPathPrefix: cosPathPrefix,
	}, nil
}

// SaveFile saves a file to COS storage
// It generates a unique name for the file and organizes it by tenant and knowledge ID
func (s *cosFileService) SaveFile(ctx context.Context,
	file *multipart.FileHeader, tenantID uint, knowledgeID string,
) (string, error) {
	ext := filepath.Ext(file.Filename)
	objectName := fmt.Sprintf("%s/%d/%s/%s%s", s.cosPathPrefix, tenantID, knowledgeID, uuid.New().String(), ext)
	src, err := file.Open()
	if err != nil {
		return "", fmt.Errorf("failed to open file: %w", err)
	}
	defer src.Close()
	_, err = s.client.Object.Put(ctx, objectName, src, nil)
	if err != nil {
		return "", fmt.Errorf("failed to upload file to COS: %w", err)
	}
	return fmt.Sprintf("%s%s", s.bucketURL, objectName), nil
}

// GetFile retrieves a file from COS storage by its path URL
func (s *cosFileService) GetFile(ctx context.Context, filePathUrl string) (io.ReadCloser, error) {
	objectName := strings.TrimPrefix(filePathUrl, s.bucketURL)
	resp, err := s.client.Object.Get(ctx, objectName, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get file from COS: %w", err)
	}
	return resp.Body, nil
}

// DeleteFile removes a file from COS storage
func (s *cosFileService) DeleteFile(ctx context.Context, filePath string) error {
	objectName := strings.TrimPrefix(filePath, s.bucketURL)
	_, err := s.client.Object.Delete(ctx, objectName)
	if err != nil {
		return fmt.Errorf("failed to delete file: %w", err)
	}
	return nil
}
