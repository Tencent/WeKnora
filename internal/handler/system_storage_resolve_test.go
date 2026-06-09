package handler

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
)

// makeCtxWithTenant injects a Tenant into the gin.Context just like the
// real middleware does.
func makeCtxWithTenant(tenant *types.Tenant) *gin.Context {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(nil)
	c.Set(types.TenantInfoContextKey.String(), tenant)
	return c
}

func TestIsOSSConfigured_BuiltinFallback(t *testing.T) {
	types.ResetBuiltinStorageEngine()
	t.Cleanup(types.ResetBuiltinStorageEngine)
	types.SetBuiltinStorageEngineForTest(&types.StorageEngineConfig{
		OSS: &types.OSSEngineConfig{
			Endpoint: "oss.local", Region: "r", AccessKey: "ak", SecretKey: "sk", BucketName: "b",
		},
	})
	h := &SystemHandler{}
	c := makeCtxWithTenant(&types.Tenant{}) // tenant 没配 OSS
	if !h.isOSSConfigured(c) {
		t.Fatal("want true via builtin fallback")
	}
}

func TestIsOSSConfigured_TenantOnly(t *testing.T) {
	types.ResetBuiltinStorageEngine()
	t.Cleanup(types.ResetBuiltinStorageEngine)
	h := &SystemHandler{}
	c := makeCtxWithTenant(&types.Tenant{
		StorageEngineConfig: &types.StorageEngineConfig{
			OSS: &types.OSSEngineConfig{
				Endpoint: "oss.tenant", Region: "r", AccessKey: "ak", SecretKey: "sk", BucketName: "b",
			},
		},
	})
	if !h.isOSSConfigured(c) {
		t.Fatal("want true with tenant config")
	}
}

func TestIsOSSConfigured_BothEmpty(t *testing.T) {
	types.ResetBuiltinStorageEngine()
	t.Cleanup(types.ResetBuiltinStorageEngine)
	h := &SystemHandler{}
	c := makeCtxWithTenant(&types.Tenant{})
	if h.isOSSConfigured(c) {
		t.Fatal("want false when both empty")
	}
}

func TestIsCOSConfigured_BuiltinFallback(t *testing.T) {
	types.ResetBuiltinStorageEngine()
	t.Cleanup(types.ResetBuiltinStorageEngine)
	types.SetBuiltinStorageEngineForTest(&types.StorageEngineConfig{
		COS: &types.COSEngineConfig{SecretID: "id", SecretKey: "sk", Region: "r", BucketName: "b"},
	})
	h := &SystemHandler{}
	c := makeCtxWithTenant(&types.Tenant{})
	if !h.isCOSConfigured(c) {
		t.Fatal("want true via builtin")
	}
}

func TestIsTOSConfigured_BuiltinFallback(t *testing.T) {
	types.ResetBuiltinStorageEngine()
	t.Cleanup(types.ResetBuiltinStorageEngine)
	types.SetBuiltinStorageEngineForTest(&types.StorageEngineConfig{
		TOS: &types.TOSEngineConfig{Endpoint: "ep", Region: "r", AccessKey: "ak", SecretKey: "sk", BucketName: "b"},
	})
	h := &SystemHandler{}
	c := makeCtxWithTenant(&types.Tenant{})
	if !h.isTOSConfigured(c) {
		t.Fatal("want true via builtin")
	}
}

func TestIsS3Configured_BuiltinFallback(t *testing.T) {
	types.ResetBuiltinStorageEngine()
	t.Cleanup(types.ResetBuiltinStorageEngine)
	types.SetBuiltinStorageEngineForTest(&types.StorageEngineConfig{
		S3: &types.S3EngineConfig{Endpoint: "ep", Region: "r", AccessKey: "ak", SecretKey: "sk", BucketName: "b"},
	})
	h := &SystemHandler{}
	c := makeCtxWithTenant(&types.Tenant{})
	if !h.isS3Configured(c) {
		t.Fatal("want true via builtin")
	}
}

func TestIsKS3Configured_BuiltinFallback(t *testing.T) {
	types.ResetBuiltinStorageEngine()
	t.Cleanup(types.ResetBuiltinStorageEngine)
	types.SetBuiltinStorageEngineForTest(&types.StorageEngineConfig{
		KS3: &types.KS3EngineConfig{Endpoint: "ep", Region: "r", AccessKey: "ak", SecretKey: "sk", BucketName: "b"},
	})
	h := &SystemHandler{}
	c := makeCtxWithTenant(&types.Tenant{})
	if !h.isKS3Configured(c) {
		t.Fatal("want true via builtin")
	}
}

func TestIsMinioConfigured_BuiltinFallback(t *testing.T) {
	types.ResetBuiltinStorageEngine()
	t.Cleanup(types.ResetBuiltinStorageEngine)
	types.SetBuiltinStorageEngineForTest(&types.StorageEngineConfig{
		MinIO: &types.MinIOEngineConfig{
			Mode: "remote", Endpoint: "minio:9000", AccessKeyID: "ak",
			SecretAccessKey: "sk", BucketName: "b",
		},
	})
	h := &SystemHandler{}
	c := makeCtxWithTenant(&types.Tenant{})
	if !h.isMinioConfigured(c) {
		t.Fatal("want true via builtin")
	}
}
