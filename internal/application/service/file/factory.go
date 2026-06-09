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
	if p == "" {
		p = types.ResolveDefaultProvider(sec)
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
		cfg, _ := types.ResolveLocalConfig(sec)
		baseDir := localBaseDir
		if cfg != nil {
			rawPrefix := strings.TrimSpace(cfg.PathPrefix)
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
		cfg, _ := types.ResolveMinIOConfig(sec)
		if cfg == nil {
			return nil, p, fmt.Errorf("missing minio config")
		}
		var endpoint, accessKeyID, secretAccessKey string
		if cfg.Mode == "remote" {
			endpoint = strings.TrimSpace(cfg.Endpoint)
			accessKeyID = strings.TrimSpace(cfg.AccessKeyID)
			secretAccessKey = strings.TrimSpace(cfg.SecretAccessKey)
		} else {
			endpoint = strings.TrimSpace(os.Getenv("MINIO_ENDPOINT"))
			accessKeyID = strings.TrimSpace(os.Getenv("MINIO_ACCESS_KEY_ID"))
			secretAccessKey = strings.TrimSpace(os.Getenv("MINIO_SECRET_ACCESS_KEY"))
		}
		bucketName := strings.TrimSpace(cfg.BucketName)
		if bucketName == "" {
			bucketName = strings.TrimSpace(os.Getenv("MINIO_BUCKET_NAME"))
		}
		if endpoint == "" || accessKeyID == "" || secretAccessKey == "" || bucketName == "" {
			return nil, p, fmt.Errorf("incomplete minio config")
		}
		svc, err := NewMinioFileService(endpoint, accessKeyID, secretAccessKey, bucketName, cfg.UseSSL)
		return svc, p, err

	case "cos":
		cfg, ok := types.ResolveCOSConfig(sec)
		if !ok {
			return nil, p, fmt.Errorf("incomplete cos config")
		}
		pathPrefix := strings.TrimSpace(cfg.PathPrefix)
		if pathPrefix == "" {
			pathPrefix = "weknora"
		}
		svc, err := NewCosFileService(cfg.BucketName, cfg.Region, cfg.SecretID, cfg.SecretKey, pathPrefix)
		return svc, p, err

	case "tos":
		cfg, ok := types.ResolveTOSConfig(sec)
		if !ok {
			return nil, p, fmt.Errorf("incomplete tos config")
		}
		svc, err := NewTosFileService(cfg.Endpoint, cfg.Region, cfg.AccessKey, cfg.SecretKey, cfg.BucketName, cfg.PathPrefix)
		return svc, p, err
	case "s3":
		cfg, ok := types.ResolveS3Config(sec)
		if !ok {
			return nil, p, fmt.Errorf("incomplete s3 config")
		}
		pathPrefix := strings.TrimSpace(cfg.PathPrefix)
		if pathPrefix == "" {
			pathPrefix = "weknora/"
		}
		svc, err := NewS3FileService(cfg.Endpoint, cfg.AccessKey, cfg.SecretKey, cfg.BucketName, cfg.Region, pathPrefix)
		return svc, p, err

	case "obs":
		cfg, ok := types.ResolveOBSConfig(sec)
		if !ok {
			return nil, p, fmt.Errorf("incomplete obs config")
		}
		svc, err := NewObsFileService(cfg.Endpoint, cfg.Region, cfg.AccessKey, cfg.SecretKey, cfg.BucketName, cfg.PathPrefix)
		return svc, p, err

	case "oss":
		cfg, ok := types.ResolveOSSConfig(sec)
		if !ok {
			return nil, p, fmt.Errorf("incomplete oss config")
		}
		pathPrefix := strings.TrimSpace(cfg.PathPrefix)
		if pathPrefix == "" {
			pathPrefix = "weknora/"
		}
		var svc interfaces.FileService
		var err error
		if cfg.UseTempBucket && cfg.TempBucketName != "" {
			svc, err = NewOssFileServiceWithTempBucket(
				cfg.Endpoint, cfg.Region, cfg.AccessKey, cfg.SecretKey,
				cfg.BucketName, pathPrefix,
				cfg.TempBucketName, cfg.TempRegion,
			)
		} else {
			svc, err = NewOssFileService(
				cfg.Endpoint, cfg.Region, cfg.AccessKey, cfg.SecretKey,
				cfg.BucketName, pathPrefix,
			)
		}
		return svc, p, err

	case "ks3":
		cfg, ok := types.ResolveKS3Config(sec)
		if !ok {
			return nil, p, fmt.Errorf("incomplete ks3 config")
		}
		pathPrefix := strings.TrimSpace(cfg.PathPrefix)
		if pathPrefix == "" {
			pathPrefix = "weknora/"
		}
		svc, err := NewKS3FileService(cfg.Endpoint, cfg.Region, cfg.AccessKey, cfg.SecretKey, cfg.BucketName, pathPrefix)
		return svc, p, err

	default:
		return nil, p, fmt.Errorf("unsupported provider %q", p)
	}
}
