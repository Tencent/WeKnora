package file

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/google/uuid"
	"github.com/tencentyun/cos-go-sdk-v5"
)

// cosFileService implements the FileService interface for Tencent Cloud COS
type cosFileService struct {
	client        *cos.Client
	bucketURL     string
	cosPathPrefix string
	// 临时桶配置（用于存放自动过期的临时文件）
	tempClient    *cos.Client
	tempBucketURL string
}

// NewCosFileService creates a new COS file service instance
func NewCosFileService(bucketName, region, secretId, secretKey, cosPathPrefix string) (interfaces.FileService, error) {
	return NewCosFileServiceWithTempBucket(bucketName, region, secretId, secretKey, cosPathPrefix, "", "")
}

// NewCosFileServiceWithTempBucket creates a new COS file service instance with optional temp bucket
func NewCosFileServiceWithTempBucket(bucketName, region, secretId, secretKey, cosPathPrefix, tempBucketName, tempRegion string) (interfaces.FileService, error) {
	bucketURL := fmt.Sprintf("https://%s.cos.%s.tencentcos.cn/", bucketName, region)
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

	svc := &cosFileService{
		client:        client,
		bucketURL:     bucketURL,
		cosPathPrefix: cosPathPrefix,
	}

	// 如果配置了临时桶，初始化临时桶客户端
	if tempBucketName != "" {
		if tempRegion == "" {
			tempRegion = region // 默认使用相同的 region
		}
		tempBucketURL := fmt.Sprintf("https://%s.cos.%s.tencentcos.cn/", tempBucketName, tempRegion)
		tempU, err := url.Parse(tempBucketURL)
		if err != nil {
			return nil, fmt.Errorf("failed to parse temp bucketURL: %w", err)
		}
		tempB := &cos.BaseURL{BucketURL: tempU}
		tempClient := cos.NewClient(tempB, &http.Client{
			Transport: &cos.AuthorizationTransport{
				SecretID:  secretId,
				SecretKey: secretKey,
			},
		})
		svc.tempClient = tempClient
		svc.tempBucketURL = tempBucketURL
	}

	return svc, nil
}

// SaveFile saves a file to COS storage
// It generates a unique name for the file and organizes it by tenant and knowledge ID
func (s *cosFileService) SaveFile(ctx context.Context,
	file *multipart.FileHeader, tenantID uint64, knowledgeID string,
) (string, error) {
	ext := filepath.Ext(file.Filename)
	objectName := fmt.Sprintf("%s/%d/%s/%s%s", s.cosPathPrefix, tenantID, knowledgeID, uuid.New().String(), ext)
	src, err := file.Open()
	if err != nil {
		return "", fmt.Errorf("failed to open file: %w", err)
	}
	defer func() {
		_ = src.Close()
	}()
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

// SaveBytes saves bytes data to COS
// If temp is true and temp bucket is configured, saves to temp bucket (with lifecycle auto-expiration)
// Otherwise saves to main bucket
func (s *cosFileService) SaveBytes(ctx context.Context, data []byte, tenantID uint64, fileName string, temp bool) (string, error) {
	ext := filepath.Ext(fileName)
	reader := bytes.NewReader(data)

	// 如果请求写入临时桶且临时桶已配置
	if temp && s.tempClient != nil {
		objectName := fmt.Sprintf("exports/%d/%s%s", tenantID, uuid.New().String(), ext)
		_, err := s.tempClient.Object.Put(ctx, objectName, reader, nil)
		if err != nil {
			return "", fmt.Errorf("failed to upload bytes to COS temp bucket: %w", err)
		}
		return fmt.Sprintf("%s%s", s.tempBucketURL, objectName), nil
	}

	// 写入主桶
	objectName := fmt.Sprintf("%s/%d/exports/%s%s", s.cosPathPrefix, tenantID, uuid.New().String(), ext)
	_, err := s.client.Object.Put(ctx, objectName, reader, nil)
	if err != nil {
		return "", fmt.Errorf("failed to upload bytes to COS: %w", err)
	}

	return fmt.Sprintf("%s%s", s.bucketURL, objectName), nil
}

// GetFileURL returns a presigned download URL for the file
func (s *cosFileService) GetFileURL(ctx context.Context, filePath string) (string, error) {
	// 判断文件属于哪个桶
	if s.tempClient != nil && strings.HasPrefix(filePath, s.tempBucketURL) {
		objectName := strings.TrimPrefix(filePath, s.tempBucketURL)
		// Generate presigned URL (valid for 24 hours)
		presignedURL, err := s.tempClient.Object.GetPresignedURL(ctx, http.MethodGet, objectName, s.tempClient.GetCredential().SecretID, s.tempClient.GetCredential().SecretKey, 24*time.Hour, nil)
		if err != nil {
			return "", fmt.Errorf("failed to generate presigned URL for temp bucket: %w", err)
		}
		return presignedURL.String(), nil
	}

	objectName := strings.TrimPrefix(filePath, s.bucketURL)
	// Generate presigned URL (valid for 24 hours)
	presignedURL, err := s.client.Object.GetPresignedURL(ctx, http.MethodGet, objectName, s.client.GetCredential().SecretID, s.client.GetCredential().SecretKey, 24*time.Hour, nil)
	if err != nil {
		return "", fmt.Errorf("failed to generate presigned URL: %w", err)
	}

	return presignedURL.String(), nil
}
