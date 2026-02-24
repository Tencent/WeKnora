package file

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"path/filepath"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/google/uuid"
	"github.com/volcengine/ve-tos-golang-sdk/v2/tos"
	"github.com/volcengine/ve-tos-golang-sdk/v2/tos/enum"
)

// tosFileService implements the FileService interface for Volcengine TOS.
type tosFileService struct {
	client         *tos.ClientV2
	pathPrefix     string
	bucketName     string
	tempBucketName string
}

// NewTosFileService creates a TOS file service.
func NewTosFileService(endpoint, region, accessKey, secretKey, bucketName, pathPrefix string) (interfaces.FileService, error) {
	return NewTosFileServiceWithTempBucket(endpoint, region, accessKey, secretKey, bucketName, pathPrefix, "", "")
}

// NewTosFileServiceWithTempBucket creates a TOS file service with optional temp bucket.
func NewTosFileServiceWithTempBucket(endpoint, region, accessKey, secretKey, bucketName, pathPrefix, tempBucketName, tempRegion string) (interfaces.FileService, error) {
	client, err := tos.NewClientV2(
		endpoint,
		tos.WithRegion(region),
		tos.WithCredentials(tos.NewStaticCredentials(accessKey, secretKey)),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize TOS client: %w", err)
	}

	if err := ensureTOSBucket(client, bucketName); err != nil {
		return nil, err
	}

	if tempBucketName != "" {
		if tempRegion == "" {
			tempRegion = region
		}
		// Temporary bucket may belong to another region, so probe with a short-lived client.
		tempClient, err := tos.NewClientV2(
			endpoint,
			tos.WithRegion(tempRegion),
			tos.WithCredentials(tos.NewStaticCredentials(accessKey, secretKey)),
		)
		if err != nil {
			return nil, fmt.Errorf("failed to initialize TOS temp client: %w", err)
		}
		if err := ensureTOSBucket(tempClient, tempBucketName); err != nil {
			return nil, err
		}
	}

	return &tosFileService{
		client:         client,
		pathPrefix:     strings.Trim(pathPrefix, "/"),
		bucketName:     bucketName,
		tempBucketName: tempBucketName,
	}, nil
}

func ensureTOSBucket(client *tos.ClientV2, bucketName string) error {
	_, err := client.HeadBucket(context.Background(), &tos.HeadBucketInput{
		Bucket: bucketName,
	})
	if err == nil {
		return nil
	}

	var serverErr *tos.TosServerError
	if errors.As(err, &serverErr) && serverErr.StatusCode == 404 {
		_, createErr := client.CreateBucketV2(context.Background(), &tos.CreateBucketV2Input{
			Bucket: bucketName,
		})
		if createErr == nil {
			return nil
		}
		if errors.As(createErr, &serverErr) && serverErr.StatusCode == 409 {
			return nil
		}
		return fmt.Errorf("failed to create TOS bucket: %w", createErr)
	}

	return fmt.Errorf("failed to check TOS bucket: %w", err)
}

func joinTOSObjectKey(parts ...string) string {
	filtered := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.Trim(part, "/")
		if part != "" {
			filtered = append(filtered, part)
		}
	}
	return strings.Join(filtered, "/")
}

func parseTOSFilePath(filePath string) (bucketName string, objectKey string, err error) {
	const prefix = "tos://"
	if !strings.HasPrefix(filePath, prefix) {
		return "", "", fmt.Errorf("invalid TOS file path: %s", filePath)
	}

	rest := strings.TrimPrefix(filePath, prefix)
	parts := strings.SplitN(rest, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("invalid TOS file path: %s", filePath)
	}
	return parts[0], parts[1], nil
}

func (s *tosFileService) SaveFile(ctx context.Context, file *multipart.FileHeader, tenantID uint64, knowledgeID string) (string, error) {
	ext := filepath.Ext(file.Filename)
	objectName := joinTOSObjectKey(
		s.pathPrefix,
		fmt.Sprintf("%d", tenantID),
		knowledgeID,
		uuid.New().String()+ext,
	)

	src, err := file.Open()
	if err != nil {
		return "", fmt.Errorf("failed to open file: %w", err)
	}
	defer src.Close()

	_, err = s.client.PutObjectV2(ctx, &tos.PutObjectV2Input{
		PutObjectBasicInput: tos.PutObjectBasicInput{
			Bucket:      s.bucketName,
			Key:         objectName,
			ContentType: file.Header.Get("Content-Type"),
		},
		Content: src,
	})
	if err != nil {
		return "", fmt.Errorf("failed to upload file to TOS: %w", err)
	}

	return fmt.Sprintf("tos://%s/%s", s.bucketName, objectName), nil
}

func (s *tosFileService) SaveBytes(ctx context.Context, data []byte, tenantID uint64, fileName string, temp bool) (string, error) {
	ext := filepath.Ext(fileName)
	reader := bytes.NewReader(data)

	targetBucket := s.bucketName
	objectName := joinTOSObjectKey(
		s.pathPrefix,
		fmt.Sprintf("%d", tenantID),
		"exports",
		uuid.New().String()+ext,
	)

	if temp && s.tempBucketName != "" {
		targetBucket = s.tempBucketName
		objectName = joinTOSObjectKey(
			"exports",
			fmt.Sprintf("%d", tenantID),
			uuid.New().String()+ext,
		)
	}

	_, err := s.client.PutObjectV2(ctx, &tos.PutObjectV2Input{
		PutObjectBasicInput: tos.PutObjectBasicInput{
			Bucket:      targetBucket,
			Key:         objectName,
			ContentType: "text/csv; charset=utf-8",
		},
		Content: reader,
	})
	if err != nil {
		return "", fmt.Errorf("failed to upload bytes to TOS: %w", err)
	}

	return fmt.Sprintf("tos://%s/%s", targetBucket, objectName), nil
}

func (s *tosFileService) GetFile(ctx context.Context, filePath string) (io.ReadCloser, error) {
	bucketName, objectName, err := parseTOSFilePath(filePath)
	if err != nil {
		return nil, err
	}

	output, err := s.client.GetObjectV2(ctx, &tos.GetObjectV2Input{
		Bucket: bucketName,
		Key:    objectName,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get file from TOS: %w", err)
	}
	return output.Content, nil
}

func (s *tosFileService) DeleteFile(ctx context.Context, filePath string) error {
	bucketName, objectName, err := parseTOSFilePath(filePath)
	if err != nil {
		return err
	}

	_, err = s.client.DeleteObjectV2(ctx, &tos.DeleteObjectV2Input{
		Bucket: bucketName,
		Key:    objectName,
	})
	if err != nil {
		return fmt.Errorf("failed to delete file from TOS: %w", err)
	}
	return nil
}

func (s *tosFileService) GetFileURL(ctx context.Context, filePath string) (string, error) {
	bucketName, objectName, err := parseTOSFilePath(filePath)
	if err != nil {
		return "", err
	}

	output, err := s.client.PreSignedURL(&tos.PreSignedURLInput{
		HTTPMethod: enum.HttpMethodGet,
		Bucket:     bucketName,
		Key:        objectName,
		Expires:    int64((24 * time.Hour).Seconds()),
	})
	if err != nil {
		return "", fmt.Errorf("failed to generate TOS presigned URL: %w", err)
	}
	return output.SignedUrl, nil
}
