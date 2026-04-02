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

// s3FileService AWS S3 file service implementation
type s3FileService struct {
	client     *s3.Client
	bucketName string
	pathPrefix string
}

// newS3Client creates a bare s3FileService with just the SDK client initialised.
func newS3Client(endpoint, accessKey, secretKey, bucketName, region, pathPrefix string) (*s3FileService, error) {
	var cfg aws.Config
	var err error

	// Configure AWS SDK
	cfg, err = config.LoadDefaultConfig(context.Background(),
		config.WithRegion(region),
		config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(accessKey, secretKey, "")),
	)

	if err != nil {
		return nil, fmt.Errorf("failed to load AWS config: %w", err)
	}

	// Create S3 client with custom endpoint if provided.
	// For S3-compatible services (non-AWS), use path-style addressing
	// (endpoint/bucket/key) instead of virtual-hosted style (bucket.endpoint/key).
	var client *s3.Client
	if endpoint != "" {
		usePathStyle := !strings.Contains(endpoint, "amazonaws.com")
		client = s3.NewFromConfig(cfg, func(o *s3.Options) {
			o.BaseEndpoint = aws.String(endpoint)
			o.UsePathStyle = usePathStyle
		})
	} else {
		// Standard AWS S3
		client = s3.NewFromConfig(cfg)
	}

	// Normalize pathPrefix: ensure it ends with '/' if not empty
	if pathPrefix != "" && !strings.HasSuffix(pathPrefix, "/") {
		pathPrefix += "/"
	}

	return &s3FileService{
		client:     client,
		bucketName: bucketName,
		pathPrefix: pathPrefix,
	}, nil
}

// NewS3FileService creates an AWS S3 file service.
// It verifies that the bucket exists and creates it if missing.
func NewS3FileService(endpoint,
	accessKey, secretKey, bucketName, region, pathPrefix string,
) (interfaces.FileService, error) {
	svc, err := newS3Client(endpoint, accessKey, secretKey, bucketName, region, pathPrefix)
	if err != nil {
		return nil, err
	}

	// Check if bucket exists
	exists, err := svc.bucketExists(context.Background())
	if err != nil {
		return nil, fmt.Errorf("failed to check bucket: %w", err)
	}

	if !exists {
		if err = svc.createBucket(context.Background()); err != nil {
			return nil, fmt.Errorf("failed to create bucket: %w", err)
		}
	}

	return svc, nil
}

// bucketExists checks if the bucket exists
func (s *s3FileService) bucketExists(ctx context.Context) (bool, error) {
	_, err := s.client.HeadBucket(ctx, &s3.HeadBucketInput{
		Bucket: aws.String(s.bucketName),
	})
	if err != nil {
		// Check if the error is a NotFound error
		var notFound *types.NotFound
		if errors.As(err, &notFound) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// createBucket creates a new bucket
func (s *s3FileService) createBucket(ctx context.Context) error {
	_, err := s.client.CreateBucket(ctx, &s3.CreateBucketInput{
		Bucket: aws.String(s.bucketName),
	})
	return err
}

// CheckConnectivity verifies S3 is reachable and, if a bucket is configured,
// that the bucket exists. This is a read-only probe — it never creates a bucket.
func (s *s3FileService) CheckConnectivity(ctx context.Context) error {
	checkCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	if s.bucketName != "" {
		exists, err := s.bucketExists(checkCtx)
		if err != nil {
			return err
		}
		if !exists {
			return fmt.Errorf("bucket %q does not exist", s.bucketName)
		}
		return nil
	}

	// List buckets to verify connectivity
	_, err := s.client.ListBuckets(checkCtx, &s3.ListBucketsInput{})
	return err
}

// CheckS3Connectivity tests S3 connectivity using the provided credentials.
// It creates a temporary service instance internally and delegates to CheckConnectivity.
func CheckS3Connectivity(ctx context.Context, endpoint, accessKey, secretKey, bucketName, region string) error {
	svc, err := newS3Client(endpoint, accessKey, secretKey, bucketName, region, "")
	if err != nil {
		return err
	}
	return svc.CheckConnectivity(ctx)
}

// parseS3FilePath extracts the object name from a provider scheme: s3://{bucket}/{objectKey}
func (s *s3FileService) parseS3FilePath(filePath string) (string, error) {
	// Provider scheme format: s3://{bucket}/{objectKey}
	const prefix = "s3://"
	if !strings.HasPrefix(filePath, prefix) {
		return "", fmt.Errorf("invalid S3 file path: %s", filePath)
	}
	rest := strings.TrimPrefix(filePath, prefix)
	parts := strings.SplitN(rest, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", fmt.Errorf("invalid S3 file path: %s", filePath)
	}
	if parts[0] != s.bucketName {
		return "", fmt.Errorf("bucket mismatch in path: got %s, want %s", parts[0], s.bucketName)
	}
	if err := utils.SafeObjectKey(parts[1]); err != nil {
		return "", fmt.Errorf("invalid file path: %w", err)
	}
	return parts[1], nil
}

// getContentTypeByExt returns the content type based on file extension
func getContentTypeByExt(ext string) string {
	switch strings.ToLower(ext) {
	case ".csv":
		return "text/csv; charset=utf-8"
	case ".json":
		return "application/json"
	case ".pdf":
		return "application/pdf"
	case ".doc":
		return "application/msword"
	case ".docx":
		return "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
	case ".xls":
		return "application/vnd.ms-excel"
	case ".xlsx":
		return "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
	case ".ppt":
		return "application/vnd.ms-powerpoint"
	case ".pptx":
		return "application/vnd.openxmlformats-officedocument.presentationml.presentation"
	case ".txt":
		return "text/plain; charset=utf-8"
	case ".md":
		return "text/markdown"
	case ".html":
		return "text/html; charset=utf-8"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".gif":
		return "image/gif"
	case ".svg":
		return "image/svg+xml"
	case ".mp3":
		return "audio/mpeg"
	case ".wav":
		return "audio/wav"
	case ".m4a":
		return "audio/mp4"
	case ".flac":
		return "audio/flac"
	case ".ogg":
		return "audio/ogg"
	case ".mp4":
		return "video/mp4"
	default:
		return "application/octet-stream"
	}
}

// SaveFile saves a file to S3
func (s *s3FileService) SaveFile(ctx context.Context,
	file *multipart.FileHeader, tenantID uint64, knowledgeID string,
) (string, error) {
	// Generate object name
	ext := filepath.Ext(file.Filename)
	objectName := fmt.Sprintf("%s%d/%s/%s%s", s.pathPrefix, tenantID, knowledgeID, uuid.New().String(), ext)

	// Open file
	src, err := file.Open()
	if err != nil {
		return "", fmt.Errorf("failed to open file: %w", err)
	}
	defer src.Close()

	// Determine content type
	contentType := file.Header.Get("Content-Type")
	if contentType == "" {
		contentType = getContentTypeByExt(ext)
	}

	// Upload file to S3
	_, err = s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:        aws.String(s.bucketName),
		Key:           aws.String(objectName),
		Body:          src,
		ContentLength: aws.Int64(file.Size),
		ContentType:   aws.String(contentType),
	})
	if err != nil {
		return "", fmt.Errorf("failed to upload file to S3: %w", err)
	}

	return fmt.Sprintf("s3://%s/%s", s.bucketName, objectName), nil
}

// GetFile gets a file from S3
func (s *s3FileService) GetFile(ctx context.Context, filePath string) (io.ReadCloser, error) {
	objectName, err := s.parseS3FilePath(filePath)
	if err != nil {
		return nil, err
	}

	resp, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucketName),
		Key:    aws.String(objectName),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get file from S3: %w", err)
	}

	return resp.Body, nil
}

// DeleteFile deletes a file
func (s *s3FileService) DeleteFile(ctx context.Context, filePath string) error {
	objectName, err := s.parseS3FilePath(filePath)
	if err != nil {
		return err
	}

	_, err = s.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(s.bucketName),
		Key:    aws.String(objectName),
	})
	if err != nil {
		return fmt.Errorf("failed to delete file: %w", err)
	}

	return nil
}

// SaveBytes saves bytes data to S3 and returns the file path
// temp parameter is ignored for S3 (no auto-expiration support in this implementation)
func (s *s3FileService) SaveBytes(ctx context.Context, data []byte, tenantID uint64, fileName string, temp bool) (string, error) {
	safeName, err := utils.SafeFileName(fileName)
	if err != nil {
		return "", fmt.Errorf("invalid file name: %w", err)
	}
	ext := filepath.Ext(safeName)
	objectName := fmt.Sprintf("%s%d/exports/%s%s", s.pathPrefix, tenantID, uuid.New().String(), ext)

	// Upload bytes to S3
	reader := bytes.NewReader(data)
	_, err = s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:        aws.String(s.bucketName),
		Key:           aws.String(objectName),
		Body:          reader,
		ContentLength: aws.Int64(int64(len(data))),
		ContentType:   aws.String("text/csv; charset=utf-8"),
	})
	if err != nil {
		return "", fmt.Errorf("failed to upload bytes to S3: %w", err)
	}

	return fmt.Sprintf("s3://%s/%s", s.bucketName, objectName), nil
}

// GetFileURL returns a presigned download URL for the file
func (s *s3FileService) GetFileURL(ctx context.Context, filePath string) (string, error) {
	objectName, err := s.parseS3FilePath(filePath)
	if err != nil {
		return "", err
	}

	// Create presign client
	presignClient := s3.NewPresignClient(s.client)

	// Generate presigned URL
	presignedReq, err := presignClient.PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucketName),
		Key:    aws.String(objectName),
	}, s3.WithPresignExpires(24*time.Hour))
	if err != nil {
		return "", fmt.Errorf("failed to generate presigned URL: %w", err)
	}

	return presignedReq.URL, nil
}
