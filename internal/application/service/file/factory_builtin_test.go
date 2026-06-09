package file

import (
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestFactory_COS_BuiltinFallback(t *testing.T) {
	types.ResetBuiltinStorageEngine()
	t.Cleanup(types.ResetBuiltinStorageEngine)
	setBuiltinCfgForTest(t, &types.StorageEngineConfig{
		COS: &types.COSEngineConfig{
			SecretID: "id-x", SecretKey: "sk-x", Region: "ap-shanghai", BucketName: "bkt",
		},
	})
	svc, p, err := NewFileServiceFromStorageConfig("cos", nil, "")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if p != "cos" {
		t.Fatalf("provider %q", p)
	}
	if svc == nil {
		t.Fatal("svc nil")
	}
}

func TestFactory_TOS_BuiltinFallback(t *testing.T) {
	types.ResetBuiltinStorageEngine()
	t.Cleanup(types.ResetBuiltinStorageEngine)
	setBuiltinCfgForTest(t, &types.StorageEngineConfig{
		TOS: &types.TOSEngineConfig{
			Endpoint: "https://tos.local", Region: "cn-bj", AccessKey: "ak", SecretKey: "sk", BucketName: "bkt",
		},
	})
	_, p, err := NewFileServiceFromStorageConfig("tos", nil, "")
	// Constructor may fail because TOS SDK does a BucketExists() network call.
	// We only assert the resolver provided a config (factory did not return "incomplete tos config").
	if err != nil && strings.Contains(err.Error(), "incomplete") {
		t.Fatalf("builtin fallback did not engage: %v", err)
	}
	if p != "tos" {
		t.Fatalf("provider %q", p)
	}
}

func TestFactory_Local_BuiltinFallback(t *testing.T) {
	types.ResetBuiltinStorageEngine()
	t.Cleanup(types.ResetBuiltinStorageEngine)
	setBuiltinCfgForTest(t, &types.StorageEngineConfig{
		Local: &types.LocalEngineConfig{PathPrefix: "weknora-builtin"},
	})
	_, p, err := NewFileServiceFromStorageConfig("local", nil, t.TempDir())
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if p != "local" {
		t.Fatalf("provider %q", p)
	}
}

func TestFactory_MinIO_DockerModePreserved(t *testing.T) {
	types.ResetBuiltinStorageEngine()
	t.Cleanup(types.ResetBuiltinStorageEngine)
	t.Setenv("MINIO_ENDPOINT", "minio.local:9000")
	t.Setenv("MINIO_ACCESS_KEY_ID", "ak")
	t.Setenv("MINIO_SECRET_ACCESS_KEY", "sk")
	t.Setenv("MINIO_BUCKET_NAME", "bkt")
	sec := &types.StorageEngineConfig{
		MinIO: &types.MinIOEngineConfig{Mode: "docker"},
	}
	_, p, err := NewFileServiceFromStorageConfig("minio", sec, "")
	// Constructor will network-call to MinIO; we tolerate that error.
	// What we verify: factory did not return "missing minio config" or "incomplete minio config".
	if err != nil {
		msg := err.Error()
		if strings.Contains(msg, "missing minio config") || strings.Contains(msg, "incomplete minio config") {
			t.Fatalf("docker-mode env path failed at config resolution: %v", err)
		}
	}
	if p != "minio" {
		t.Fatalf("provider %q", p)
	}
}

func TestFactory_DefaultProvider_BuiltinFallback(t *testing.T) {
	types.ResetBuiltinStorageEngine()
	t.Cleanup(types.ResetBuiltinStorageEngine)
	setBuiltinCfgForTest(t, &types.StorageEngineConfig{
		DefaultProvider: "cos",
		COS: &types.COSEngineConfig{SecretID: "id", SecretKey: "sk", Region: "r", BucketName: "b"},
	})
	_, p, err := NewFileServiceFromStorageConfig("", nil, "") // provider 入参空，sec 空
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if p != "cos" {
		t.Fatalf("expected default fallback to cos, got %q", p)
	}
}

func TestFactory_AllProviders_BothNil_ReturnsIncomplete(t *testing.T) {
	types.ResetBuiltinStorageEngine()
	t.Cleanup(types.ResetBuiltinStorageEngine)
	providers := []string{"cos", "tos", "s3", "oss", "ks3", "minio"}
	for _, prov := range providers {
		t.Run(prov, func(t *testing.T) {
			_, _, err := NewFileServiceFromStorageConfig(prov, nil, "")
			if err == nil {
				t.Fatalf("want incomplete err for %s, got nil", prov)
			}
		})
	}
}

func TestFactory_S3_BuiltinFallback(t *testing.T) {
	types.ResetBuiltinStorageEngine()
	t.Cleanup(types.ResetBuiltinStorageEngine)
	setBuiltinCfgForTest(t, &types.StorageEngineConfig{
		S3: &types.S3EngineConfig{
			Endpoint: "https://s3.example.com", Region: "us-east-1",
			AccessKey: "ak", SecretKey: "sk", BucketName: "bkt",
		},
	})
	_, p, err := NewFileServiceFromStorageConfig("s3", nil, "")
	// Constructor may network on init. Tolerate non-incomplete errors.
	if err != nil && strings.Contains(err.Error(), "incomplete") {
		t.Fatalf("builtin fallback did not engage: %v", err)
	}
	if p != "s3" {
		t.Fatalf("provider %q", p)
	}
}

func TestFactory_OSS_BuiltinFallback(t *testing.T) {
	types.ResetBuiltinStorageEngine()
	t.Cleanup(types.ResetBuiltinStorageEngine)
	setBuiltinCfgForTest(t, &types.StorageEngineConfig{
		OSS: &types.OSSEngineConfig{
			Endpoint: "oss-cn-hangzhou.aliyuncs.com", Region: "cn-hangzhou",
			AccessKey: "ak", SecretKey: "sk", BucketName: "bkt",
		},
	})
	_, p, err := NewFileServiceFromStorageConfig("oss", nil, "")
	if err != nil && strings.Contains(err.Error(), "incomplete") {
		t.Fatalf("builtin fallback did not engage: %v", err)
	}
	if p != "oss" {
		t.Fatalf("provider %q", p)
	}
}

func TestFactory_KS3_BuiltinFallback(t *testing.T) {
	types.ResetBuiltinStorageEngine()
	t.Cleanup(types.ResetBuiltinStorageEngine)
	setBuiltinCfgForTest(t, &types.StorageEngineConfig{
		KS3: &types.KS3EngineConfig{
			Endpoint: "ks3-cn-beijing.ksyuncs.com", Region: "cn-beijing",
			AccessKey: "ak", SecretKey: "sk", BucketName: "bkt",
		},
	})
	_, p, err := NewFileServiceFromStorageConfig("ks3", nil, "")
	if err != nil && strings.Contains(err.Error(), "incomplete") {
		t.Fatalf("builtin fallback did not engage: %v", err)
	}
	if p != "ks3" {
		t.Fatalf("provider %q", p)
	}
}

// setBuiltinCfgForTest is a test-only helper that calls the exported
// types.SetBuiltinStorageEngineForTest to populate the singleton.
func setBuiltinCfgForTest(t *testing.T, cfg *types.StorageEngineConfig) {
	t.Helper()
	types.SetBuiltinStorageEngineForTest(cfg)
}

func TestFactory_OBS_BuiltinFallback(t *testing.T) {
	types.ResetBuiltinStorageEngine()
	t.Cleanup(types.ResetBuiltinStorageEngine)
	types.ResetOBSEnvDeprecationOnceForTest()
	t.Cleanup(types.ResetOBSEnvDeprecationOnceForTest)
	setBuiltinCfgForTest(t, &types.StorageEngineConfig{
		OBS: &types.OBSEngineConfig{
			Endpoint: "obs.huawei.com", Region: "cn-north-4",
			AccessKey: "ak", SecretKey: "sk", BucketName: "bkt",
		},
	})
	_, p, err := NewFileServiceFromStorageConfig("obs", nil, "")
	if err != nil && strings.Contains(err.Error(), "incomplete") {
		t.Fatalf("builtin fallback did not engage: %v", err)
	}
	if p != "obs" {
		t.Fatalf("provider %q", p)
	}
}

func TestFactory_OBS_LegacyEnvDeprecationFallback(t *testing.T) {
	types.ResetBuiltinStorageEngine()
	t.Cleanup(types.ResetBuiltinStorageEngine)
	types.ResetOBSEnvDeprecationOnceForTest()
	t.Cleanup(types.ResetOBSEnvDeprecationOnceForTest)

	t.Setenv("OBS_ENDPOINT", "obs.huawei.com")
	t.Setenv("OBS_ACCESS_KEY", "ak")
	t.Setenv("OBS_SECRET_KEY", "sk")
	t.Setenv("OBS_BUCKET_NAME", "bkt")

	_, p, err := NewFileServiceFromStorageConfig("obs", nil, "")
	if err != nil && strings.Contains(err.Error(), "incomplete") {
		t.Fatalf("env fallback failed at resolver: %v", err)
	}
	if p != "obs" {
		t.Fatalf("provider %q", p)
	}
	if got := types.GetOBSEnvDeprecationWarnCountForTest(); got != 1 {
		t.Fatalf("expect 1 warn fired, got %d", got)
	}
}
