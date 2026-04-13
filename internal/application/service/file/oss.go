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
	"github.com/Tencent/WeKnora/internal/utils"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/google/uuid"
)

// ossFileService implements the FileService interface for Aliyun OSS
// using the S3-compatible protocol with virtual-hosted style addressing.
type ossFileService struct {
	client         *s3.Client
	tempClient     *s3.Client
	pathPrefix     string
	bucketName     string
	tempBucketName string
}

const ossScheme = "oss://"

// newOSSClient creates a bare s3.Client configured for OSS S3-compatible mode.
// OSS uses virtual-hosted style addressing and does not support aws-chunked encoding.
func newOSSClient(endpoint, region, accessKey, secretKey string) (*s3.Client, error) {
	cfg, err := config.LoadDefaultConfig(context.Background(),
		config.WithRegion(region),
		config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(accessKey, secretKey, "")),
		// Disable automatic aws-chunked encoding — OSS does not support it.
		config.WithRequestChecksumCalculation(aws.RequestChecksumCalculationWhenRequired),
		config.WithResponseChecksumValidation(aws.ResponseChecksumValidationWhenRequired),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to load AWS config: %w", err)
	}

	client := s3.NewFromConfig(cfg, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(endpoint)
		o.UsePathStyle = false
	})

	return client, nil
}

// ossBucketExists checks if the bucket exists using the provided client.
func ossBucketExists(ctx context.Context, client *s3.Client, bucketName string) (bool, error) {
	_, err := client.HeadBucket(ctx, &s3.HeadBucketInput{
		Bucket: aws.String(bucketName),
	})
	if err != nil {
		var notFound *types.NotFound
		if errors.As(err, &notFound) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// ossCreateBucket creates a new bucket using the provided client.
func ossCreateBucket(ctx context.Context, client *s3.Client, bucketName string) error {
	_, err := client.CreateBucket(ctx, &s3.CreateBucketInput{
		Bucket: aws.String(bucketName),
	})
	return err
}

// ossEnsureBucket checks if the bucket exists and creates it if missing.
func ossEnsureBucket(ctx context.Context, client *s3.Client, bucketName string) error {
	exists, err := ossBucketExists(ctx, client, bucketName)
	if err != nil {
		return fmt.Errorf("failed to check bucket: %w", err)
	}
	if !exists {
		if err := ossCreateBucket(ctx, client, bucketName); err != nil {
			return fmt.Errorf("failed to create bucket: %w", err)
		}
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

	if err := ossEnsureBucket(context.Background(), client, bucketName); err != nil {
		return nil, err
	}

	var tempClient *s3.Client
	if tempBucketName != "" {
		if tempRegion == "" {
			tempRegion = region
		}
		tempClient, err = newOSSClient(endpoint, tempRegion, accessKey, secretKey)
		if err != nil {
			return nil, fmt.Errorf("failed to initialize OSS temp client: %w", err)
		}
		if err := ossEnsureBucket(context.Background(), tempClient, tempBucketName); err != nil {
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

	checkCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	exists, err := ossBucketExists(checkCtx, client, bucketName)
	if err != nil {
		return fmt.Errorf("failed to check bucket: %w", err)
	}
	if !exists {
		return fmt.Errorf("bucket %q does not exist", bucketName)
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

	exists, err := ossBucketExists(checkCtx, s.client, s.bucketName)
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("bucket %q does not exist", s.bucketName)
	}
	return nil
}

// SaveFile saves a file to OSS.
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
		contentType = getContentTypeByExt(ext)
	}

	_, err = s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(s.bucketName),
		Key:         aws.String(objectName),
		Body:        src,
		ContentType: aws.String(contentType),
	})
	if err != nil {
		return "", fmt.Errorf("failed to upload file to OSS: %w", err)
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

	// If requesting temp bucket and it is configured, use it
	if temp && s.tempClient != nil {
		objectName := fmt.Sprintf("exports/%d/%s%s", tenantID, uuid.New().String(), ext)
		_, err := s.tempClient.PutObject(ctx, &s3.PutObjectInput{
			Bucket:      aws.String(s.tempBucketName),
			Key:         aws.String(objectName),
			Body:        bytes.NewReader(data),
			ContentType: aws.String("text/csv; charset=utf-8"),
		})
		if err != nil {
			return "", fmt.Errorf("failed to upload bytes to OSS temp bucket: %w", err)
		}
		return fmt.Sprintf("oss://%s/%s", s.tempBucketName, objectName), nil
	}

	// Save to main bucket
	objectName := fmt.Sprintf("%s%d/exports/%s%s", s.pathPrefix, tenantID, uuid.New().String(), ext)
	_, err = s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(s.bucketName),
		Key:         aws.String(objectName),
		Body:        bytes.NewReader(data),
		ContentType: aws.String("text/csv; charset=utf-8"),
	})
	if err != nil {
		return "", fmt.Errorf("failed to upload bytes to OSS: %w", err)
	}

	return fmt.Sprintf("oss://%s/%s", s.bucketName, objectName), nil
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

	resp, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(objectName),
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

	_, err = s.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(objectName),
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
	var client *s3.Client
	if bucketName == s.tempBucketName && s.tempClient != nil {
		client = s.tempClient
	} else {
		client = s.client
	}

	presignClient := s3.NewPresignClient(client)

	presignedReq, err := presignClient.PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(objectName),
	}, s3.WithPresignExpires(24*time.Hour))
	if err != nil {
		return "", fmt.Errorf("failed to generate OSS presigned URL: %w", err)
	}

	return presignedReq.URL, nil
}
