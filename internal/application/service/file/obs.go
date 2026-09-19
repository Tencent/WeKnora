package file

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/Tencent/WeKnora/internal/utils"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/google/uuid"
)

type obsFileService struct {
	client      *s3.Client
	bucketName  string
	endpoint    string
	region      string
	pathPrefix  string
	proxyDomain string
}

// obsUsePathStyle reports whether the OBS client should keep path-style
// addressing for this endpoint. Huawei Cloud OBS rejects path-style bucket
// addressing on domain endpoints since 2023-12-30, so domain endpoints use
// virtual-hosted style ({bucket}.{host}); IP-literal or unparseable endpoints
// have no DNS label to prefix and keep path-style. See issue #3269.
func obsUsePathStyle(endpoint string) bool {
	u, err := url.Parse(endpoint)
	if err != nil || u.Host == "" {
		return true
	}
	return net.ParseIP(u.Hostname()) != nil
}

// obsS3Options builds the S3 client options shared by the file service and the
// connectivity check.
func obsS3Options(endpoint, region, accessKey, secretAccessKey string) s3.Options {
	return s3.Options{
		Region:       region,
		BaseEndpoint: aws.String(endpoint),
		Credentials:  credentials.NewStaticCredentialsProvider(accessKey, secretAccessKey, ""),
		// OBS is S3-compatible but commonly rejects the SDK's default
		// trailing checksum negotiation; align with the S3 driver (s3.go).
		RequestChecksumCalculation: aws.RequestChecksumCalculationWhenRequired,
		UsePathStyle:               obsUsePathStyle(endpoint),
		HTTPClient:                 objectStorageHTTPClient(),
	}
}

func NewObsFileService(
	endpoint, region, accessKeyID, secretAccessKey, bucketName string,
	pathPrefix string,
) (interfaces.FileService, error) {
	if err := utils.ValidateURLForSSRF(endpoint); err != nil {
		return nil, fmt.Errorf("unsafe OBS endpoint: %w", err)
	}

	client := s3.New(obsS3Options(endpoint, region, accessKeyID, secretAccessKey))

	headCtx, cancel := objectStorageSetupContext()
	_, err := client.HeadBucket(headCtx, &s3.HeadBucketInput{
		Bucket: aws.String(bucketName),
	})
	cancel()
	if err != nil {
		var notFound *s3types.NotFound
		if errors.As(err, &notFound) {
			createCtx, createCancel := objectStorageSetupContext()
			_, createErr := client.CreateBucket(createCtx, &s3.CreateBucketInput{
				Bucket: aws.String(bucketName),
			})
			createCancel()
			if createErr != nil {
				fmt.Printf("Warning: bucket %s may not exist or cannot be created: %v\n", bucketName, createErr)
			}
		} else {
			fmt.Printf("Warning: bucket %s may not exist or cannot be created: %v\n", bucketName, err)
		}
	}

	proxyDomain := strings.TrimSuffix(os.Getenv("OBS_PROXY_DOMAIN"), "/")

	return &obsFileService{
		client:      client,
		bucketName:  bucketName,
		endpoint:    endpoint,
		region:      region,
		pathPrefix:  strings.Trim(pathPrefix, "/"),
		proxyDomain: proxyDomain,
	}, nil
}

func CheckObsConnectivity(ctx context.Context, endpoint, region, accessKey, secretKey, bucketName string) error {
	if err := utils.ValidateURLForSSRF(endpoint); err != nil {
		return fmt.Errorf("unsafe OBS endpoint: %w", err)
	}
	checkCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	client := s3.New(obsS3Options(endpoint, region, accessKey, secretKey))

	_, err := client.HeadBucket(checkCtx, &s3.HeadBucketInput{
		Bucket: aws.String(bucketName),
	})
	if err != nil {
		return fmt.Errorf("OBS connectivity check failed: %w", err)
	}
	return nil
}

func (s *obsFileService) CheckConnectivity(ctx context.Context) error {
	checkCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	_, err := s.client.HeadBucket(checkCtx, &s3.HeadBucketInput{
		Bucket: aws.String(s.bucketName),
	})
	if err != nil {
		return fmt.Errorf("OBS connectivity check failed: %w", err)
	}
	return nil
}

func (s *obsFileService) parseObsFilePath(filePath string) (string, error) {
	prefix := s.getPrifix()

	if strings.HasPrefix(filePath, prefix) {
		rest := strings.TrimPrefix(filePath, prefix)
		// With proxy domain: path is {prefix}/{objectKey} (no bucket name)
		if s.proxyDomain != "" {
			rest = strings.TrimPrefix(rest, "/")
			if rest != "" {
				return rest, nil
			}
			return "", fmt.Errorf("invalid OBS file path: %s", filePath)
		}
		// Without proxy domain: path is {prefix}/{bucketName}/{objectKey}
		parts := strings.SplitN(rest, "/", 2)
		if len(parts) == 2 && parts[0] == s.bucketName && parts[1] != "" {
			return parts[1], nil
		}
		return "", fmt.Errorf("invalid OBS file path: %s", filePath)
	}
	return filePath, nil
}

func (s *obsFileService) getPrifix() string {
	if s.proxyDomain != "" {
		return s.proxyDomain + "/"
	}
	return "obs://"
}

func (s *obsFileService) SaveFile(ctx context.Context,
	file *multipart.FileHeader, tenantID uint64, knowledgeID string,
) (string, error) {
	ext := filepath.Ext(file.Filename)

	var objectKey string
	if s.pathPrefix != "" {
		objectKey = fmt.Sprintf("%s/%d/%s/%s%s", s.pathPrefix, tenantID, knowledgeID, uuid.New().String(), ext)
	} else {
		objectKey = fmt.Sprintf("%d/%s/%s%s", tenantID, knowledgeID, uuid.New().String(), ext)
	}

	src, err := file.Open()
	if err != nil {
		return "", fmt.Errorf("failed to open file: %w", err)
	}
	defer src.Close()

	contentType := file.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	ctx, cancel := objectStorageTransferContext(ctx)
	defer cancel()
	_, err = s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:        aws.String(s.bucketName),
		Key:           aws.String(objectKey),
		Body:          src,
		ContentLength: aws.Int64(file.Size),
		ContentType:   aws.String(contentType),
		// ACL:           "private",
	})
	if err != nil {
		return "", fmt.Errorf("failed to upload file to OBS: %w", err)
	}
	prefix := s.getPrifix()
	if s.proxyDomain != "" {
		return fmt.Sprintf("%s%s", prefix, objectKey), nil
	}
	return fmt.Sprintf("%s%s/%s", prefix, s.bucketName, objectKey), nil
}

func (s *obsFileService) GetFile(ctx context.Context, filePath string) (io.ReadCloser, error) {
	objectKey, err := s.parseObsFilePath(filePath)
	if err != nil {
		return nil, err
	}

	ctx, cancel := objectStorageTransferContext(ctx)
	output, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucketName),
		Key:    aws.String(objectKey),
	})
	if err != nil {
		cancel()
		return nil, fmt.Errorf("failed to get file from OBS: %w", err)
	}

	return objectStorageBoundReader(output.Body, cancel), nil
}

func (s *obsFileService) DeleteFile(ctx context.Context, filePath string) error {
	objectKey, err := s.parseObsFilePath(filePath)
	if err != nil {
		return err
	}

	ctx, cancel := objectStorageTransferContext(ctx)
	defer cancel()
	_, err = s.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(s.bucketName),
		Key:    aws.String(objectKey),
	})
	if err != nil {
		return fmt.Errorf("failed to delete file from OBS: %w", err)
	}

	return nil
}

func (s *obsFileService) GetFileURL(ctx context.Context, filePath string) (string, error) {
	if strings.HasPrefix(filePath, "http://") || strings.HasPrefix(filePath, "https://") {
		return filePath, nil
	}

	objectKey, err := s.parseObsFilePath(filePath)
	if err != nil {
		return "", err
	}

	if s.proxyDomain != "" {
		return s.proxyDomain + "/" + strings.TrimPrefix(objectKey, "/"), nil
	}

	// Mirror the client addressing so generated URLs stay reachable: OBS
	// rejects path-style on domain endpoints (issue #3269), so serve
	// {bucket}.{endpoint-host}/{key}; IP-literal endpoints keep path-style.
	if obsUsePathStyle(s.endpoint) {
		return fmt.Sprintf("%s/%s/%s", s.endpoint, s.bucketName, strings.TrimPrefix(objectKey, "/")), nil
	}
	endpointURL, err := url.Parse(s.endpoint)
	if err != nil {
		return "", fmt.Errorf("invalid OBS endpoint %q: %w", s.endpoint, err)
	}
	endpointURL.Host = s.bucketName + "." + endpointURL.Host
	endpointURL.Path = "/" + strings.TrimPrefix(objectKey, "/")
	return endpointURL.String(), nil
}

// CopyFile copies an existing OBS object to a new knowledge-owned object using a
// server-side CopyObject (OBS is S3-compatible). The destination uses the same
// layout as SaveFile. Returns ErrCrossBackendCopy when srcPath does not belong
// to this OBS service.
func (s *obsFileService) CopyFile(ctx context.Context,
	srcPath string, tenantID uint64, knowledgeID string,
) (string, error) {
	// Reject paths that do not use this service's prefix (proxy domain or obs://).
	// parseObsFilePath falls back to returning the raw input for unknown prefixes,
	// so guard explicitly here to detect cross-backend sources.
	if !strings.HasPrefix(srcPath, s.getPrifix()) {
		return "", fmt.Errorf("obs copy rejected source %q: %w", srcPath, ErrCrossBackendCopy)
	}
	srcKey, err := s.parseObsFilePath(srcPath)
	if err != nil {
		return "", fmt.Errorf("obs copy rejected source %q: %w", srcPath, ErrCrossBackendCopy)
	}

	ext := filepath.Ext(srcPath)
	var destKey string
	if s.pathPrefix != "" {
		destKey = fmt.Sprintf("%s/%d/%s/%s%s", s.pathPrefix, tenantID, knowledgeID, uuid.New().String(), ext)
	} else {
		destKey = fmt.Sprintf("%d/%s/%s%s", tenantID, knowledgeID, uuid.New().String(), ext)
	}

	// CopySource is "bucket/key"; the '/' separators must NOT be percent-encoded
	// (url.PathEscape would turn them into %2F and break the bucket/key split).
	ctx, cancel := objectStorageTransferContext(ctx)
	defer cancel()
	_, err = s.client.CopyObject(ctx, &s3.CopyObjectInput{
		Bucket:     aws.String(s.bucketName),
		CopySource: aws.String(s.bucketName + "/" + srcKey),
		Key:        aws.String(destKey),
	})
	if err != nil {
		return "", fmt.Errorf("failed to copy file in OBS: %w", err)
	}

	prefix := s.getPrifix()
	var newPath string
	if s.proxyDomain != "" {
		newPath = fmt.Sprintf("%s%s", prefix, destKey)
	} else {
		newPath = fmt.Sprintf("%s%s/%s", prefix, s.bucketName, destKey)
	}
	logger.Infof(ctx, "Copied OBS object %s to %s", srcPath, newPath)
	return newPath, nil
}

func (s *obsFileService) SaveBytes(ctx context.Context, data []byte, tenantID uint64, fileName string, temp bool) (string, error) {
	ext := filepath.Ext(fileName)

	var objectKey string
	if temp {
		if s.pathPrefix != "" {
			objectKey = fmt.Sprintf("%s/temp/%d/%s%s", s.pathPrefix, tenantID, uuid.New().String(), ext)
		} else {
			objectKey = fmt.Sprintf("temp/%d/%s%s", tenantID, uuid.New().String(), ext)
		}
	} else {
		if s.pathPrefix != "" {
			objectKey = fmt.Sprintf("%s/%d/%s%s", s.pathPrefix, tenantID, uuid.New().String(), ext)
		} else {
			objectKey = fmt.Sprintf("%d/%s%s", tenantID, uuid.New().String(), ext)
		}
	}

	ctx, cancel := objectStorageTransferContext(ctx)
	defer cancel()
	_, err := s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(s.bucketName),
		Key:         aws.String(objectKey),
		Body:        strings.NewReader(string(data)),
		ContentType: aws.String("application/octet-stream"),
		ACL:         "public-read",
	})
	if err != nil {
		return "", fmt.Errorf("failed to upload bytes to OBS: %w", err)
	}

	prefix := s.getPrifix()
	if s.proxyDomain != "" {
		return fmt.Sprintf("%s%s", prefix, objectKey), nil
	}
	return fmt.Sprintf("%s%s/%s", prefix, s.bucketName, objectKey), nil
}
