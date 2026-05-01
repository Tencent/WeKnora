package file

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	secutils "github.com/Tencent/WeKnora/internal/utils"
)

// NewFileServiceFromStorageConfig builds a provider-specific FileService from tenant storage config.
// provider can be empty; in that case it falls back to sec.DefaultProvider.
// Returns the resolved provider name together with the service.
func NewFileServiceFromStorageConfig(
	provider string,
	sec *types.StorageEngineConfig,
	localBaseDir string,
) (interfaces.FileService, string, error) {
	p := strings.ToLower(strings.TrimSpace(provider))
	if p == "" && sec != nil {
		p = strings.ToLower(strings.TrimSpace(sec.DefaultProvider))
	}
	if p == "" {
		return nil, "", fmt.Errorf("empty provider")
	}

	if localBaseDir == "" {
		localBaseDir = strings.TrimSpace(os.Getenv("LOCAL_STORAGE_BASE_DIR"))
	}
	if localBaseDir == "" {
		localBaseDir = "/data/files"
	}

	switch p {
	case "local":
		baseDir := localBaseDir
		if sec != nil && sec.Local != nil {
			rawPrefix := strings.TrimSpace(sec.Local.PathPrefix)
			prefix := strings.Trim(rawPrefix, "/\\")
			if prefix != "" {
				candidate := filepath.Join(baseDir, prefix)
				if safeBaseDir, err := secutils.SafePathUnderBase(baseDir, candidate); err == nil {
					baseDir = safeBaseDir
				}
			}
		}
		externalURL := strings.TrimSpace(os.Getenv("APP_EXTERNAL_URL"))
		return NewLocalFileService(baseDir, externalURL), p, nil

	case "minio":
		if sec == nil || sec.MinIO == nil {
			return nil, p, fmt.Errorf("missing minio config")
		}
		var endpoint, accessKeyID, secretAccessKey string
		if sec.MinIO.Mode == "remote" {
			endpoint = strings.TrimSpace(sec.MinIO.Endpoint)
			accessKeyID = strings.TrimSpace(sec.MinIO.AccessKeyID)
			secretAccessKey = strings.TrimSpace(sec.MinIO.SecretAccessKey)
		} else {
			endpoint = strings.TrimSpace(os.Getenv("MINIO_ENDPOINT"))
			accessKeyID = strings.TrimSpace(os.Getenv("MINIO_ACCESS_KEY_ID"))
			secretAccessKey = strings.TrimSpace(os.Getenv("MINIO_SECRET_ACCESS_KEY"))
		}
		bucketName := strings.TrimSpace(sec.MinIO.BucketName)
		if bucketName == "" {
			bucketName = strings.TrimSpace(os.Getenv("MINIO_BUCKET_NAME"))
		}
		if endpoint == "" || accessKeyID == "" || secretAccessKey == "" || bucketName == "" {
			return nil, p, fmt.Errorf("incomplete minio config")
		}
		svc, err := NewMinioFileService(endpoint, accessKeyID, secretAccessKey, bucketName, sec.MinIO.UseSSL)
		return svc, p, err

	case "cos":
		if sec == nil || sec.COS == nil || sec.COS.SecretID == "" || sec.COS.SecretKey == "" || sec.COS.BucketName == "" || sec.COS.Region == "" {
			return nil, p, fmt.Errorf("incomplete cos config")
		}
		pathPrefix := strings.TrimSpace(sec.COS.PathPrefix)
		if pathPrefix == "" {
			pathPrefix = "weknora"
		}
		svc, err := NewCosFileService(sec.COS.BucketName, sec.COS.Region, sec.COS.SecretID, sec.COS.SecretKey, pathPrefix)
		return svc, p, err

	case "tos":
		if sec == nil || sec.TOS == nil || sec.TOS.Endpoint == "" || sec.TOS.Region == "" || sec.TOS.AccessKey == "" || sec.TOS.SecretKey == "" || sec.TOS.BucketName == "" {
			return nil, p, fmt.Errorf("incomplete tos config")
		}
		svc, err := NewTosFileService(sec.TOS.Endpoint, sec.TOS.Region, sec.TOS.AccessKey, sec.TOS.SecretKey, sec.TOS.BucketName, sec.TOS.PathPrefix)
		return svc, p, err
	case "s3":
		if sec == nil || sec.S3 == nil || sec.S3.Endpoint == "" || sec.S3.Region == "" || sec.S3.AccessKey == "" || sec.S3.SecretKey == "" || sec.S3.BucketName == "" {
			return nil, p, fmt.Errorf("incomplete s3 config")
		}
		pathPrefix := strings.TrimSpace(sec.S3.PathPrefix)
		if pathPrefix == "" {
			pathPrefix = "weknora/"
		}
		svc, err := NewS3FileService(sec.S3.Endpoint, sec.S3.AccessKey, sec.S3.SecretKey, sec.S3.BucketName, sec.S3.Region, pathPrefix)
		return svc, p, err

	case "oss":
		if sec == nil || sec.OSS == nil || sec.OSS.Endpoint == "" || sec.OSS.Region == "" || sec.OSS.AccessKey == "" || sec.OSS.SecretKey == "" || sec.OSS.BucketName == "" {
			return nil, p, fmt.Errorf("incomplete oss config")
		}
		pathPrefix := strings.TrimSpace(sec.OSS.PathPrefix)
		if pathPrefix == "" {
			pathPrefix = "weknora/"
		}
		var svc interfaces.FileService
		var err error
		if sec.OSS.UseTempBucket && sec.OSS.TempBucketName != "" {
			svc, err = NewOssFileServiceWithTempBucket(
				sec.OSS.Endpoint, sec.OSS.Region, sec.OSS.AccessKey, sec.OSS.SecretKey,
				sec.OSS.BucketName, pathPrefix,
				sec.OSS.TempBucketName, sec.OSS.TempRegion,
			)
		} else {
			svc, err = NewOssFileService(
				sec.OSS.Endpoint, sec.OSS.Region, sec.OSS.AccessKey, sec.OSS.SecretKey,
				sec.OSS.BucketName, pathPrefix,
			)
		}
		return svc, p, err

	case "ks3":
		if sec == nil || sec.KS3 == nil || sec.KS3.Endpoint == "" || sec.KS3.Region == "" || sec.KS3.AccessKey == "" || sec.KS3.SecretKey == "" || sec.KS3.BucketName == "" {
			return nil, p, fmt.Errorf("incomplete ks3 config")
		}
		pathPrefix := strings.TrimSpace(sec.KS3.PathPrefix)
		if pathPrefix == "" {
			pathPrefix = "weknora/"
		}
		svc, err := NewKS3FileService(sec.KS3.Endpoint, sec.KS3.Region, sec.KS3.AccessKey, sec.KS3.SecretKey, sec.KS3.BucketName, pathPrefix)
		return svc, p, err

	default:
		return nil, p, fmt.Errorf("unsupported provider %q", p)
	}
}
