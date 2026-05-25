package types

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
)

// ---------------------------------------------------------------------------
// Singleton: holds the loaded built-in StorageEngineConfig at process scope.
//
// atomic.Pointer makes startup-write/runtime-read lock-free. Tests may call
// ResetBuiltinStorageEngine via t.Cleanup; production code only writes once
// from LoadBuiltinStorageEngineConfig at startup.
// ---------------------------------------------------------------------------

var builtinStorageEngine atomic.Pointer[StorageEngineConfig]

// OBS env deprecation: prints warn at most once per process when the
// legacy OBS_* env path is used. To be removed in vip-v0.7.x.
var (
	obsEnvDeprecationOnce  sync.Once
	obsEnvDeprecationCount atomic.Int64 // test observability
)

// ResetOBSEnvDeprecationOnceForTest resets the once guard so a test can
// re-trigger the warn path. Production code MUST NOT call this.
func ResetOBSEnvDeprecationOnceForTest() {
	obsEnvDeprecationOnce = sync.Once{}
	obsEnvDeprecationCount.Store(0)
}

// GetOBSEnvDeprecationWarnCountForTest returns how many times the deprecation
// warn fired. Always 0 or 1 in practice due to sync.Once.
func GetOBSEnvDeprecationWarnCountForTest() int64 {
	return obsEnvDeprecationCount.Load()
}

// GetBuiltinStorageEngine returns the loaded built-in config, or nil if
// LoadBuiltinStorageEngineConfig has not been called or found no file.
func GetBuiltinStorageEngine() *StorageEngineConfig {
	return builtinStorageEngine.Load()
}

// ResetBuiltinStorageEngine clears the singleton. Intended for test t.Cleanup.
func ResetBuiltinStorageEngine() {
	builtinStorageEngine.Store(nil)
}

// SetBuiltinStorageEngineForTest is a test-only writer for cross-package tests.
// Production code MUST use LoadBuiltinStorageEngineConfig instead.
//
// Not protected by build tags — kept simple to match builtin_models style.
func SetBuiltinStorageEngineForTest(cfg *StorageEngineConfig) {
	builtinStorageEngine.Store(cfg)
}

// ---------------------------------------------------------------------------
// ResolveXxxConfig: tenant -> builtin fallback with empty-shell detection.
//
// Returns (*XEngineConfig, isValid). isValid means all required fields are
// populated; callers check it before constructing the provider client.
// A non-nil cfg with isValid=false signals "tenant has a partial config";
// callers should NOT silently fall back to builtin in that case (the user
// explicitly chose to overlay), they should report incomplete config.
// ---------------------------------------------------------------------------

func ResolveLocalConfig(sec *StorageEngineConfig) (*LocalEngineConfig, bool) {
	if sec != nil && sec.Local != nil && !isLocalEmpty(sec.Local) {
		return sec.Local, isLocalValid(sec.Local)
	}
	if b := GetBuiltinStorageEngine(); b != nil && b.Local != nil && !isLocalEmpty(b.Local) {
		return b.Local, isLocalValid(b.Local)
	}
	return nil, false
}

func ResolveMinIOConfig(sec *StorageEngineConfig) (*MinIOEngineConfig, bool) {
	if sec != nil && sec.MinIO != nil && !isMinIOEmpty(sec.MinIO) {
		return sec.MinIO, isMinIOValid(sec.MinIO)
	}
	if b := GetBuiltinStorageEngine(); b != nil && b.MinIO != nil && !isMinIOEmpty(b.MinIO) {
		return b.MinIO, isMinIOValid(b.MinIO)
	}
	return nil, false
}

func ResolveCOSConfig(sec *StorageEngineConfig) (*COSEngineConfig, bool) {
	if sec != nil && sec.COS != nil && !isCOSEmpty(sec.COS) {
		return sec.COS, isCOSValid(sec.COS)
	}
	if b := GetBuiltinStorageEngine(); b != nil && b.COS != nil && !isCOSEmpty(b.COS) {
		return b.COS, isCOSValid(b.COS)
	}
	return nil, false
}

func ResolveTOSConfig(sec *StorageEngineConfig) (*TOSEngineConfig, bool) {
	if sec != nil && sec.TOS != nil && !isTOSEmpty(sec.TOS) {
		return sec.TOS, isTOSValid(sec.TOS)
	}
	if b := GetBuiltinStorageEngine(); b != nil && b.TOS != nil && !isTOSEmpty(b.TOS) {
		return b.TOS, isTOSValid(b.TOS)
	}
	return nil, false
}

func ResolveS3Config(sec *StorageEngineConfig) (*S3EngineConfig, bool) {
	if sec != nil && sec.S3 != nil && !isS3Empty(sec.S3) {
		return sec.S3, isS3Valid(sec.S3)
	}
	if b := GetBuiltinStorageEngine(); b != nil && b.S3 != nil && !isS3Empty(b.S3) {
		return b.S3, isS3Valid(b.S3)
	}
	return nil, false
}

func ResolveOSSConfig(sec *StorageEngineConfig) (*OSSEngineConfig, bool) {
	if sec != nil && sec.OSS != nil && !isOSSEmpty(sec.OSS) {
		return sec.OSS, isOSSValid(sec.OSS)
	}
	if b := GetBuiltinStorageEngine(); b != nil && b.OSS != nil && !isOSSEmpty(b.OSS) {
		return b.OSS, isOSSValid(b.OSS)
	}
	return nil, false
}

func ResolveKS3Config(sec *StorageEngineConfig) (*KS3EngineConfig, bool) {
	if sec != nil && sec.KS3 != nil && !isKS3Empty(sec.KS3) {
		return sec.KS3, isKS3Valid(sec.KS3)
	}
	if b := GetBuiltinStorageEngine(); b != nil && b.KS3 != nil && !isKS3Empty(b.KS3) {
		return b.KS3, isKS3Valid(b.KS3)
	}
	return nil, false
}

// ResolveOBSConfig: three-tier fallback for OBS — tenant -> builtin -> legacy
// OBS_* env vars (deprecated, will be removed in vip-v0.7.x). The legacy env
// path emits a single deprecation warning per process via sync.Once.
func ResolveOBSConfig(sec *StorageEngineConfig) (*OBSEngineConfig, bool) {
	if sec != nil && sec.OBS != nil && !isOBSEmpty(sec.OBS) {
		return sec.OBS, isOBSValid(sec.OBS)
	}
	if b := GetBuiltinStorageEngine(); b != nil && b.OBS != nil && !isOBSEmpty(b.OBS) {
		return b.OBS, isOBSValid(b.OBS)
	}
	// Legacy env fallback - deprecated.
	if ep := os.Getenv("OBS_ENDPOINT"); ep != "" {
		obsEnvDeprecationOnce.Do(func() {
			obsEnvDeprecationCount.Add(1)
			fmt.Printf("WARN: OBS_* env-var configuration is deprecated; " +
				"migrate to config/builtin_storage_engine.yaml. " +
				"See docs/BUILTIN_STORAGE_ENGINE.md\n")
		})
		region := os.Getenv("OBS_REGION")
		if region == "" {
			region = "cn-north-4"
		}
		pathPrefix := os.Getenv("OBS_PATH_PREFIX")
		if pathPrefix == "" {
			pathPrefix = "weknora/"
		}
		cfg := &OBSEngineConfig{
			Endpoint:   ep,
			Region:     region,
			AccessKey:  os.Getenv("OBS_ACCESS_KEY"),
			SecretKey:  os.Getenv("OBS_SECRET_KEY"),
			BucketName: os.Getenv("OBS_BUCKET_NAME"),
			PathPrefix: pathPrefix,
		}
		return cfg, isOBSValid(cfg)
	}
	return nil, false
}

// ResolveDefaultProvider returns the lowercase default_provider (tenant -> builtin).
func ResolveDefaultProvider(sec *StorageEngineConfig) string {
	if sec != nil {
		if p := strings.ToLower(strings.TrimSpace(sec.DefaultProvider)); p != "" {
			return p
		}
	}
	if b := GetBuiltinStorageEngine(); b != nil {
		return strings.ToLower(strings.TrimSpace(b.DefaultProvider))
	}
	return ""
}

// ---------------------------------------------------------------------------
// Empty / valid predicates.
//
// isXEmpty: 关键字段 (endpoint/access/secret/bucket) 全空 -> 视为空壳，触发回退。
// isXValid: 必填字段都齐全 -> 视为可用。
// ---------------------------------------------------------------------------

func isLocalEmpty(c *LocalEngineConfig) bool { return c.PathPrefix == "" }

// isLocalValid: local has no required field; PathPrefix is optional
// (falls back to LOCAL_STORAGE_BASE_DIR in factory).
func isLocalValid(c *LocalEngineConfig) bool { return true }

func isMinIOEmpty(c *MinIOEngineConfig) bool {
	return c.Endpoint == "" && c.AccessKeyID == "" && c.SecretAccessKey == "" && c.BucketName == "" && c.Mode == ""
}
func isMinIOValid(c *MinIOEngineConfig) bool {
	// In "docker" mode the factory will read MINIO_* env vars, so Mode alone is enough.
	if c.Mode == "docker" {
		return true
	}
	return c.Endpoint != "" && c.AccessKeyID != "" && c.SecretAccessKey != "" && c.BucketName != ""
}

func isCOSEmpty(c *COSEngineConfig) bool {
	return c.SecretID == "" && c.SecretKey == "" && c.Region == "" && c.BucketName == ""
}
func isCOSValid(c *COSEngineConfig) bool {
	return c.SecretID != "" && c.SecretKey != "" && c.Region != "" && c.BucketName != ""
}

func isTOSEmpty(c *TOSEngineConfig) bool {
	return c.Endpoint == "" && c.AccessKey == "" && c.SecretKey == "" && c.BucketName == ""
}
func isTOSValid(c *TOSEngineConfig) bool {
	return c.Endpoint != "" && c.Region != "" && c.AccessKey != "" && c.SecretKey != "" && c.BucketName != ""
}

func isS3Empty(c *S3EngineConfig) bool {
	return c.Endpoint == "" && c.AccessKey == "" && c.SecretKey == "" && c.BucketName == ""
}
func isS3Valid(c *S3EngineConfig) bool {
	return c.Endpoint != "" && c.Region != "" && c.AccessKey != "" && c.SecretKey != "" && c.BucketName != ""
}

func isOSSEmpty(c *OSSEngineConfig) bool {
	return c.Endpoint == "" && c.AccessKey == "" && c.SecretKey == "" && c.BucketName == ""
}
func isOSSValid(c *OSSEngineConfig) bool {
	return c.Endpoint != "" && c.Region != "" && c.AccessKey != "" && c.SecretKey != "" && c.BucketName != ""
}

func isKS3Empty(c *KS3EngineConfig) bool {
	return c.Endpoint == "" && c.AccessKey == "" && c.SecretKey == "" && c.BucketName == ""
}
func isKS3Valid(c *KS3EngineConfig) bool {
	return c.Endpoint != "" && c.Region != "" && c.AccessKey != "" && c.SecretKey != "" && c.BucketName != ""
}

func isOBSEmpty(c *OBSEngineConfig) bool {
	return c.Endpoint == "" && c.Region == "" && c.AccessKey == "" && c.SecretKey == "" && c.BucketName == ""
}
func isOBSValid(c *OBSEngineConfig) bool {
	return c.Endpoint != "" && c.Region != "" && c.AccessKey != "" && c.SecretKey != "" && c.BucketName != ""
}
