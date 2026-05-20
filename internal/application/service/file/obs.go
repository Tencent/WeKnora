package file

import (
	"context"
	"fmt"
	"io"
	"mime/multipart"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
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

type obsEndpointResolver struct {
	url string
}

func (r *obsEndpointResolver) ResolveEndpoint(region string, options s3.EndpointResolverOptions) (aws.Endpoint, error) {
	return aws.Endpoint{
		URL:               r.url,
		SigningRegion:     region,
		HostnameImmutable: true,
	}, nil
}

func NewObsFileService(
	endpoint, region, accessKeyID, secretAccessKey, bucketName string,
	pathPrefix string,
) (interfaces.FileService, error) {

	client := s3.New(s3.Options{
		Region:           region,
		EndpointResolver: &obsEndpointResolver{url: endpoint},
		Credentials:      credentials.NewStaticCredentialsProvider(accessKeyID, secretAccessKey, ""),
		UsePathStyle:     true,
	})

	_, err := client.HeadBucket(context.Background(), &s3.HeadBucketInput{
		Bucket: aws.String(bucketName),
	})
	if err != nil {
		_, createErr := client.CreateBucket(context.Background(), &s3.CreateBucketInput{
			Bucket: aws.String(bucketName),
		})
		if createErr != nil {
			fmt.Printf("Warning: bucket %s may not exist or cannot be created: %v\n", bucketName, createErr)
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
	checkCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	client := s3.New(s3.Options{
		Region:           region,
		EndpointResolver: &obsEndpointResolver{url: endpoint},
		Credentials:      credentials.NewStaticCredentialsProvider(accessKey, secretKey, ""),
		UsePathStyle:     true,
	})

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

	output, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucketName),
		Key:    aws.String(objectKey),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get file from OBS: %w", err)
	}

	return output.Body, nil
}

func (s *obsFileService) DeleteFile(ctx context.Context, filePath string) error {
	objectKey, err := s.parseObsFilePath(filePath)
	if err != nil {
		return err
	}

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

	return fmt.Sprintf("%s/%s/%s", s.endpoint, s.bucketName, strings.TrimPrefix(objectKey, "/")), nil
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
