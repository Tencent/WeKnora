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
	ks3aws "github.com/ks3sdklib/aws-sdk-go/aws"
	"github.com/ks3sdklib/aws-sdk-go/aws/credentials"
	ks3s3 "github.com/ks3sdklib/aws-sdk-go/service/s3"
)

const ks3Scheme = "ks3://"

// ks3FileService implements FileService for Kingsoft Cloud KS3.
// KS3 uses V2 signing by default and virtual-hosted style addressing,
// so it cannot be handled by the generic S3 provider without workarounds.
type ks3FileService struct {
	client     *ks3s3.S3
	bucketName string
	pathPrefix string
}

// NewKS3FileService creates a KS3 file service and ensures the bucket exists.
func NewKS3FileService(endpoint, region, accessKey, secretKey, bucketName, pathPrefix string) (interfaces.FileService, error) {
	client, err := newKS3Client(endpoint, region, accessKey, secretKey)
	if err != nil {
		return nil, err
	}

	pathPrefix = strings.Trim(pathPrefix, "/")

	svc := &ks3FileService{
		client:     client,
		bucketName: bucketName,
		pathPrefix: pathPrefix,
	}

	if err := ensureKS3Bucket(client, bucketName); err != nil {
		return nil, err
	}

	return svc, nil
}

func newKS3Client(endpoint, region, accessKey, secretKey string) (*ks3s3.S3, error) {
	creds := credentials.NewStaticCredentials(accessKey, secretKey, "")
	client := ks3s3.New(&ks3aws.Config{
		Credentials:      creds,
		Region:           region,
		Endpoint:         endpoint,
		DisableSSL:       false,
		S3ForcePathStyle: false, // KS3 uses virtual-hosted style
		SignerVersion:    "V2",  // KS3 recommends V2 signing
		MaxRetries:       3,
	})
	return client, nil
}

func ensureKS3Bucket(client *ks3s3.S3, bucketName string) error {
	_, err := client.HeadBucket(&ks3s3.HeadBucketInput{
		Bucket: ks3aws.String(bucketName),
	})
	if err == nil {
		return nil
	}
	// Bucket doesn't exist, try to create it
	_, createErr := client.CreateBucket(&ks3s3.CreateBucketInput{
		Bucket: ks3aws.String(bucketName),
	})
	if createErr != nil {
		return fmt.Errorf("failed to create KS3 bucket %q: %w", bucketName, createErr)
	}
	return nil
}

// CheckKS3Connectivity tests KS3 connectivity using the provided credentials.
func CheckKS3Connectivity(ctx context.Context, endpoint, region, accessKey, secretKey, bucketName string) error {
	client, err := newKS3Client(endpoint, region, accessKey, secretKey)
	if err != nil {
		return err
	}

	done := make(chan error, 1)
	go func() {
		_, err := client.HeadBucket(&ks3s3.HeadBucketInput{
			Bucket: ks3aws.String(bucketName),
		})
		done <- err
	}()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-done:
		return err
	}
}

func joinKS3Key(parts ...string) string {
	filtered := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.Trim(p, "/")
		if p != "" {
			filtered = append(filtered, p)
		}
	}
	return strings.Join(filtered, "/")
}

func parseKS3FilePath(filePath string) (bucket, objectKey string, err error) {
	if !strings.HasPrefix(filePath, ks3Scheme) {
		return "", "", fmt.Errorf("invalid KS3 file path: %s", filePath)
	}
	rest := strings.TrimPrefix(filePath, ks3Scheme)
	parts := strings.SplitN(rest, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("invalid KS3 file path: %s", filePath)
	}
	return parts[0], parts[1], nil
}

func (s *ks3FileService) SaveFile(ctx context.Context, file *multipart.FileHeader, tenantID uint64, knowledgeID string) (string, error) {
	ext := filepath.Ext(file.Filename)
	objectKey := joinKS3Key(s.pathPrefix, fmt.Sprintf("%d", tenantID), knowledgeID, uuid.New().String()+ext)

	src, err := file.Open()
	if err != nil {
		return "", fmt.Errorf("failed to open file: %w", err)
	}
	defer src.Close()

	contentType := file.Header.Get("Content-Type")
	if contentType == "" {
		contentType = utils.GetContentTypeByExt(ext)
	}

	_, err = s.client.PutObject(&ks3s3.PutObjectInput{
		Bucket:      ks3aws.String(s.bucketName),
		Key:         ks3aws.String(objectKey),
		Body:        src,
		ContentType: ks3aws.String(contentType),
	})
	if err != nil {
		return "", fmt.Errorf("failed to upload file to KS3: %w", err)
	}

	return fmt.Sprintf("%s%s/%s", ks3Scheme, s.bucketName, objectKey), nil
}

func (s *ks3FileService) SaveBytes(ctx context.Context, data []byte, tenantID uint64, fileName string, temp bool) (string, error) {
	safeName, err := utils.SafeFileName(fileName)
	if err != nil {
		return "", fmt.Errorf("invalid file name: %w", err)
	}
	ext := filepath.Ext(safeName)
	objectKey := joinKS3Key(s.pathPrefix, fmt.Sprintf("%d", tenantID), "exports", uuid.New().String()+ext)

	_, err = s.client.PutObject(&ks3s3.PutObjectInput{
		Bucket:      ks3aws.String(s.bucketName),
		Key:         ks3aws.String(objectKey),
		Body:        bytes.NewReader(data),
		ContentType: ks3aws.String(utils.GetContentTypeByExt(ext)),
	})
	if err != nil {
		return "", fmt.Errorf("failed to upload bytes to KS3: %w", err)
	}

	return fmt.Sprintf("%s%s/%s", ks3Scheme, s.bucketName, objectKey), nil
}

func (s *ks3FileService) GetFile(ctx context.Context, filePath string) (io.ReadCloser, error) {
	_, objectKey, err := parseKS3FilePath(filePath)
	if err != nil {
		return nil, err
	}
	if err := utils.SafeObjectKey(objectKey); err != nil {
		return nil, fmt.Errorf("invalid file path: %w", err)
	}

	resp, err := s.client.GetObject(&ks3s3.GetObjectInput{
		Bucket: ks3aws.String(s.bucketName),
		Key:    ks3aws.String(objectKey),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get file from KS3: %w", err)
	}

	return resp.Body, nil
}

func (s *ks3FileService) DeleteFile(ctx context.Context, filePath string) error {
	_, objectKey, err := parseKS3FilePath(filePath)
	if err != nil {
		return err
	}
	if err := utils.SafeObjectKey(objectKey); err != nil {
		return fmt.Errorf("invalid file path: %w", err)
	}

	_, err = s.client.DeleteObject(&ks3s3.DeleteObjectInput{
		Bucket: ks3aws.String(s.bucketName),
		Key:    ks3aws.String(objectKey),
	})
	if err != nil {
		return fmt.Errorf("failed to delete file from KS3: %w", err)
	}
	return nil
}

func (s *ks3FileService) CheckConnectivity(ctx context.Context) error {
	done := make(chan error, 1)
	go func() {
		_, err := s.client.HeadBucket(&ks3s3.HeadBucketInput{
			Bucket: ks3aws.String(s.bucketName),
		})
		done <- err
	}()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-done:
		return err
	}
}

func (s *ks3FileService) GetFileURL(ctx context.Context, filePath string) (string, error) {
	_, objectKey, err := parseKS3FilePath(filePath)
	if err != nil {
		return "", err
	}
	if err := utils.SafeObjectKey(objectKey); err != nil {
		return "", fmt.Errorf("invalid file path: %w", err)
	}

	url, err := s.client.GeneratePresignedUrl(&ks3s3.GeneratePresignedUrlInput{
		Bucket:     ks3aws.String(s.bucketName),
		Key:        ks3aws.String(objectKey),
		HTTPMethod: ks3s3.HTTPMethod("GET"),
		Expires:    int64((24 * time.Hour).Seconds()),
	})
	if err != nil {
		return "", fmt.Errorf("failed to generate KS3 presigned URL: %w", err)
	}

	return url, nil
}
