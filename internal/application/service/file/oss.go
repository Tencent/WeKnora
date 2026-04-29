package file

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/Tencent/WeKnora/internal/utils"
	"github.com/aliyun/alibabacloud-oss-go-sdk-v2/oss"
	"github.com/aliyun/alibabacloud-oss-go-sdk-v2/oss/credentials"
	"github.com/google/uuid"
)

// ossFileService implements the FileService interface for Aliyun OSS
// using the official Aliyun OSS SDK v2 (github.com/aliyun/alibabacloud-oss-go-sdk-v2).
type ossFileService struct {
	client         *oss.Client
	tempClient     *oss.Client
	pathPrefix     string
	bucketName     string
	tempBucketName string
}

const ossScheme = "oss://"

// newOSSClient creates an OSS client using the official Aliyun SDK v2.
func newOSSClient(endpoint, region, accessKey, secretKey string) (*oss.Client, error) {
	creds := credentials.NewStaticCredentialsProvider(accessKey, secretKey, "")

	cfg := oss.LoadDefaultConfig().
		WithCredentialsProvider(creds).
		WithRegion(region).
		WithEndpoint(endpoint)

	return oss.NewClient(cfg), nil
}

// ossEnsureBucket checks if the bucket exists and creates it if missing.
func ossEnsureBucket(client *oss.Client, bucketName string) error {
	exists, err := client.IsBucketExist(context.Background(), bucketName)
	if err != nil {
		return fmt.Errorf("failed to check OSS bucket: %w", err)
	}
	if exists {
		return nil
	}

	_, err = client.PutBucket(context.Background(), &oss.PutBucketRequest{
		Bucket: oss.Ptr(bucketName),
	})
	if err != nil {
		var svcErr *oss.ServiceError
		if errors.As(err, &svcErr) && svcErr.StatusCode == http.StatusConflict {
			return nil
		}
		return fmt.Errorf("failed to create OSS bucket: %w", err)
	}
	return nil
}

// NewOssFileService creates an Aliyun OSS file service.
// It verifies that the bucket exists and creates it if missing.
func NewOssFileService(endpoint, region, accessKey, secretKey, bucketName, pathPrefix string) (interfaces.FileService, error) {
	return NewOssFileServiceWithTempBucket(endpoint, region, accessKey, secretKey, bucketName, pathPrefix, "", "")
}

// NewOssFileServiceWithTempBucket creates an Aliyun OSS file service with optional temp bucket.
func NewOssFileServiceWithTempBucket(endpoint, region, accessKey, secretKey, bucketName, pathPrefix, tempBucketName, tempRegion string) (interfaces.FileService, error) {
	client, err := newOSSClient(endpoint, region, accessKey, secretKey)
	if err != nil {
		return nil, err
	}

	if err := ossEnsureBucket(client, bucketName); err != nil {
		return nil, err
	}

	var tempClient *oss.Client
	if tempBucketName != "" {
		if tempRegion == "" {
			tempRegion = region
		}
		tempClient, err = newOSSClient(endpoint, tempRegion, accessKey, secretKey)
		if err != nil {
			return nil, fmt.Errorf("failed to initialize OSS temp client: %w", err)
		}
		if err := ossEnsureBucket(tempClient, tempBucketName); err != nil {
			return nil, err
		}
	}

	// Normalize pathPrefix: ensure it ends with '/' if not empty
	if pathPrefix != "" && !strings.HasSuffix(pathPrefix, "/") {
		pathPrefix += "/"
	}

	return &ossFileService{
		client:         client,
		tempClient:     tempClient,
		pathPrefix:     pathPrefix,
		bucketName:     bucketName,
		tempBucketName: tempBucketName,
	}, nil
}

// CheckOssConnectivity tests OSS connectivity using the provided credentials.
func CheckOssConnectivity(ctx context.Context, endpoint, region, accessKey, secretKey, bucketName string) error {
	client, err := newOSSClient(endpoint, region, accessKey, secretKey)
	if err != nil {
		return err
	}

	exists, err := client.IsBucketExist(ctx, bucketName)
	if err != nil {
		return fmt.Errorf("failed to check OSS bucket: %w", err)
	}
	if !exists {
		return fmt.Errorf("bucket %q does not exist or is not accessible", bucketName)
	}
	return nil
}

// parseOssFilePath extracts bucket and object key from: oss://{bucket}/{objectKey}
func parseOssFilePath(filePath string) (bucketName string, objectKey string, err error) {
	if !strings.HasPrefix(filePath, ossScheme) {
		return "", "", fmt.Errorf("invalid OSS file path: %s", filePath)
	}

	rest := strings.TrimPrefix(filePath, ossScheme)
	parts := strings.SplitN(rest, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("invalid OSS file path: %s", filePath)
	}
	return parts[0], parts[1], nil
}

// CheckConnectivity verifies OSS is reachable and the main bucket exists.
func (s *ossFileService) CheckConnectivity(ctx context.Context) error {
	checkCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	exists, err := s.client.IsBucketExist(checkCtx, s.bucketName)
	if err != nil {
		return fmt.Errorf("failed to check OSS bucket: %w", err)
	}
	if !exists {
		return fmt.Errorf("bucket %q does not exist", s.bucketName)
	}
	return nil
}

// SaveFile saves a file to OSS using the Uploader manager for large files.
func (s *ossFileService) SaveFile(ctx context.Context,
	file *multipart.FileHeader, tenantID uint64, knowledgeID string,
) (string, error) {
	ext := filepath.Ext(file.Filename)
	objectName := fmt.Sprintf("%s%d/%s/%s%s", s.pathPrefix, tenantID, knowledgeID, uuid.New().String(), ext)

	src, err := file.Open()
	if err != nil {
		return "", fmt.Errorf("failed to open file: %w", err)
	}
	defer src.Close()

	contentType := file.Header.Get("Content-Type")
	if contentType == "" {
		contentType = utils.GetContentTypeByExt(ext)
	}

	// Use Uploader for files > 10MB (auto multipart with concurrent uploads)
	const multipartThreshold = 10 * 1024 * 1024
	if file.Size > multipartThreshold {
		uploader := s.client.NewUploader(func(uo *oss.UploaderOptions) {
			uo.PartSize = 10 * 1024 * 1024 // 10MB per part
			uo.ParallelNum = 3             // 3 concurrent uploads
		})

		_, err = uploader.UploadFrom(ctx,
			&oss.PutObjectRequest{
				Bucket:      oss.Ptr(s.bucketName),
				Key:         oss.Ptr(objectName),
				ContentType: oss.Ptr(contentType),
			},
			src,
		)
		if err != nil {
			return "", fmt.Errorf("failed to upload file to OSS (multipart): %w", err)
		}
	} else {
		_, err = s.client.PutObject(ctx, &oss.PutObjectRequest{
			Bucket:      oss.Ptr(s.bucketName),
			Key:         oss.Ptr(objectName),
			Body:        src,
			ContentType: oss.Ptr(contentType),
		})
		if err != nil {
			return "", fmt.Errorf("failed to upload file to OSS: %w", err)
		}
	}

	return fmt.Sprintf("oss://%s/%s", s.bucketName, objectName), nil
}

// SaveBytes saves bytes data to OSS.
// If temp is true and temp bucket is configured, saves to temp bucket.
// Otherwise saves to main bucket.
func (s *ossFileService) SaveBytes(ctx context.Context, data []byte, tenantID uint64, fileName string, temp bool) (string, error) {
	safeName, err := utils.SafeFileName(fileName)
	if err != nil {
		return "", fmt.Errorf("invalid file name: %w", err)
	}
	ext := filepath.Ext(safeName)

	targetBucket := s.bucketName
	client := s.client
	objectName := fmt.Sprintf("%s%d/exports/%s%s", s.pathPrefix, tenantID, uuid.New().String(), ext)

	if temp && s.tempClient != nil {
		targetBucket = s.tempBucketName
		client = s.tempClient
		objectName = fmt.Sprintf("exports/%d/%s%s", tenantID, uuid.New().String(), ext)
	}

	_, err = client.PutObject(ctx, &oss.PutObjectRequest{
		Bucket:      oss.Ptr(targetBucket),
		Key:         oss.Ptr(objectName),
		Body:        bytes.NewReader(data),
		ContentType: oss.Ptr(utils.GetContentTypeByExt(ext)),
	})
	if err != nil {
		return "", fmt.Errorf("failed to upload bytes to OSS: %w", err)
	}

	return fmt.Sprintf("oss://%s/%s", targetBucket, objectName), nil
}

// GetFile retrieves a file from OSS by its path.
func (s *ossFileService) GetFile(ctx context.Context, filePath string) (io.ReadCloser, error) {
	bucketName, objectName, err := parseOssFilePath(filePath)
	if err != nil {
		return nil, err
	}
	if err := utils.SafeObjectKey(objectName); err != nil {
		return nil, fmt.Errorf("invalid file path: %w", err)
	}

	var client *oss.Client
	if bucketName == s.tempBucketName && s.tempClient != nil {
		client = s.tempClient
	} else {
		client = s.client
	}

	resp, err := client.GetObject(ctx, &oss.GetObjectRequest{
		Bucket: oss.Ptr(bucketName),
		Key:    oss.Ptr(objectName),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get file from OSS: %w", err)
	}

	return resp.Body, nil
}

// DeleteFile removes a file from OSS.
func (s *ossFileService) DeleteFile(ctx context.Context, filePath string) error {
	bucketName, objectName, err := parseOssFilePath(filePath)
	if err != nil {
		return err
	}
	if err := utils.SafeObjectKey(objectName); err != nil {
		return fmt.Errorf("invalid file path: %w", err)
	}

	var client *oss.Client
	if bucketName == s.tempBucketName && s.tempClient != nil {
		client = s.tempClient
	} else {
		client = s.client
	}

	_, err = client.DeleteObject(ctx, &oss.DeleteObjectRequest{
		Bucket: oss.Ptr(bucketName),
		Key:    oss.Ptr(objectName),
	})
	if err != nil {
		return fmt.Errorf("failed to delete file from OSS: %w", err)
	}

	return nil
}

// GetFileURL returns a presigned download URL for the file.
func (s *ossFileService) GetFileURL(ctx context.Context, filePath string) (string, error) {
	bucketName, objectName, err := parseOssFilePath(filePath)
	if err != nil {
		return "", err
	}
	if err := utils.SafeObjectKey(objectName); err != nil {
		return "", fmt.Errorf("invalid file path: %w", err)
	}

	// Determine which client to use
	var client *oss.Client
	if bucketName == s.tempBucketName && s.tempClient != nil {
		client = s.tempClient
	} else {
		client = s.client
	}

	// Generate presigned URL (valid for 24 hours)
	result, err := client.Presign(ctx, &oss.GetObjectRequest{
		Bucket: oss.Ptr(bucketName),
		Key:    oss.Ptr(objectName),
	}, oss.PresignExpires(24*time.Hour))
	if err != nil {
		return "", fmt.Errorf("failed to generate OSS presigned URL: %w", err)
	}

	return result.URL, nil
}
