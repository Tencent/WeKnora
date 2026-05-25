package types

import (
	"testing"
)

// ---------------------------------------------------------------------------
// OSS - 6 用例完整矩阵（其他 provider 共用同一模式，下方收纳）
// ---------------------------------------------------------------------------

func TestResolveOSSConfig_TenantFirst(t *testing.T) {
	ResetBuiltinStorageEngine()
	t.Cleanup(ResetBuiltinStorageEngine)
	setBuiltinStorageEngineForTest(t, &StorageEngineConfig{
		OSS: &OSSEngineConfig{Endpoint: "from-builtin"},
	})
	sec := &StorageEngineConfig{OSS: &OSSEngineConfig{
		Endpoint: "from-tenant", Region: "r", AccessKey: "ak", SecretKey: "sk", BucketName: "b",
	}}
	cfg, ok := ResolveOSSConfig(sec)
	if !ok || cfg.Endpoint != "from-tenant" {
		t.Fatalf("want tenant cfg, got cfg=%+v ok=%v", cfg, ok)
	}
}

func TestResolveOSSConfig_TenantNilProviderNil_BuiltinFallback(t *testing.T) {
	ResetBuiltinStorageEngine()
	t.Cleanup(ResetBuiltinStorageEngine)
	setBuiltinStorageEngineForTest(t, &StorageEngineConfig{
		OSS: &OSSEngineConfig{Endpoint: "ep", Region: "r", AccessKey: "ak", SecretKey: "sk", BucketName: "b"},
	})
	sec := &StorageEngineConfig{} // sec != nil 但 sec.OSS == nil
	cfg, ok := ResolveOSSConfig(sec)
	if !ok || cfg.Endpoint != "ep" {
		t.Fatalf("want builtin fallback, got cfg=%+v ok=%v", cfg, ok)
	}
}

func TestResolveOSSConfig_TenantEmptyShell_BuiltinFallback(t *testing.T) {
	ResetBuiltinStorageEngine()
	t.Cleanup(ResetBuiltinStorageEngine)
	setBuiltinStorageEngineForTest(t, &StorageEngineConfig{
		OSS: &OSSEngineConfig{Endpoint: "ep", Region: "r", AccessKey: "ak", SecretKey: "sk", BucketName: "b"},
	})
	sec := &StorageEngineConfig{OSS: &OSSEngineConfig{}} // 空壳指针
	cfg, ok := ResolveOSSConfig(sec)
	if !ok || cfg.Endpoint != "ep" {
		t.Fatalf("want builtin fallback for empty shell, got cfg=%+v ok=%v", cfg, ok)
	}
}

func TestResolveOSSConfig_TenantPartial_DoesNotFallback(t *testing.T) {
	ResetBuiltinStorageEngine()
	t.Cleanup(ResetBuiltinStorageEngine)
	setBuiltinStorageEngineForTest(t, &StorageEngineConfig{
		OSS: &OSSEngineConfig{Endpoint: "from-builtin", Region: "r", AccessKey: "ak", SecretKey: "sk", BucketName: "b"},
	})
	// tenant 只填了 endpoint，其他字段空 -> 视为"已配置"，但 isValid=false
	sec := &StorageEngineConfig{OSS: &OSSEngineConfig{Endpoint: "partial-tenant"}}
	cfg, ok := ResolveOSSConfig(sec)
	if cfg == nil || cfg.Endpoint != "partial-tenant" {
		t.Fatalf("want tenant partial cfg returned, got cfg=%+v", cfg)
	}
	if ok {
		t.Fatalf("want ok=false for partial config, got ok=true")
	}
}

func TestResolveOSSConfig_BothNil(t *testing.T) {
	ResetBuiltinStorageEngine()
	t.Cleanup(ResetBuiltinStorageEngine)
	cfg, ok := ResolveOSSConfig(nil)
	if cfg != nil || ok {
		t.Fatalf("want (nil,false), got cfg=%+v ok=%v", cfg, ok)
	}
}

func TestResolveOSSConfig_TenantNilBuiltinNil(t *testing.T) {
	ResetBuiltinStorageEngine()
	t.Cleanup(ResetBuiltinStorageEngine)
	sec := &StorageEngineConfig{} // sec != nil 但 sec.OSS == nil
	cfg, ok := ResolveOSSConfig(sec)
	if cfg != nil || ok {
		t.Fatalf("want (nil,false), got cfg=%+v ok=%v", cfg, ok)
	}
}

// ---------------------------------------------------------------------------
// 其他 7 个 provider 用 table-driven 复用 OSS 行为模式
// ---------------------------------------------------------------------------

func TestResolveMinIOConfig_BuiltinFallback(t *testing.T) {
	ResetBuiltinStorageEngine()
	t.Cleanup(ResetBuiltinStorageEngine)
	setBuiltinStorageEngineForTest(t, &StorageEngineConfig{
		MinIO: &MinIOEngineConfig{Endpoint: "ep", AccessKeyID: "ak", SecretAccessKey: "sk", BucketName: "b"},
	})
	cfg, ok := ResolveMinIOConfig(nil)
	if !ok || cfg.Endpoint != "ep" {
		t.Fatalf("got cfg=%+v ok=%v", cfg, ok)
	}
}

func TestResolveCOSConfig_BuiltinFallback(t *testing.T) {
	ResetBuiltinStorageEngine()
	t.Cleanup(ResetBuiltinStorageEngine)
	setBuiltinStorageEngineForTest(t, &StorageEngineConfig{
		COS: &COSEngineConfig{SecretID: "id", SecretKey: "sk", Region: "r", BucketName: "b"},
	})
	cfg, ok := ResolveCOSConfig(nil)
	if !ok || cfg.SecretID != "id" {
		t.Fatalf("got cfg=%+v ok=%v", cfg, ok)
	}
}

func TestResolveTOSConfig_BuiltinFallback(t *testing.T) {
	ResetBuiltinStorageEngine()
	t.Cleanup(ResetBuiltinStorageEngine)
	setBuiltinStorageEngineForTest(t, &StorageEngineConfig{
		TOS: &TOSEngineConfig{Endpoint: "ep", Region: "r", AccessKey: "ak", SecretKey: "sk", BucketName: "b"},
	})
	cfg, ok := ResolveTOSConfig(nil)
	if !ok || cfg.Endpoint != "ep" {
		t.Fatalf("got cfg=%+v ok=%v", cfg, ok)
	}
}

func TestResolveS3Config_BuiltinFallback(t *testing.T) {
	ResetBuiltinStorageEngine()
	t.Cleanup(ResetBuiltinStorageEngine)
	setBuiltinStorageEngineForTest(t, &StorageEngineConfig{
		S3: &S3EngineConfig{Endpoint: "ep", Region: "r", AccessKey: "ak", SecretKey: "sk", BucketName: "b"},
	})
	cfg, ok := ResolveS3Config(nil)
	if !ok || cfg.Endpoint != "ep" {
		t.Fatalf("got cfg=%+v ok=%v", cfg, ok)
	}
}

func TestResolveKS3Config_BuiltinFallback(t *testing.T) {
	ResetBuiltinStorageEngine()
	t.Cleanup(ResetBuiltinStorageEngine)
	setBuiltinStorageEngineForTest(t, &StorageEngineConfig{
		KS3: &KS3EngineConfig{Endpoint: "ep", Region: "r", AccessKey: "ak", SecretKey: "sk", BucketName: "b"},
	})
	cfg, ok := ResolveKS3Config(nil)
	if !ok || cfg.Endpoint != "ep" {
		t.Fatalf("got cfg=%+v ok=%v", cfg, ok)
	}
}

func TestResolveLocalConfig_BuiltinFallback(t *testing.T) {
	ResetBuiltinStorageEngine()
	t.Cleanup(ResetBuiltinStorageEngine)
	setBuiltinStorageEngineForTest(t, &StorageEngineConfig{
		Local: &LocalEngineConfig{PathPrefix: "weknora/"},
	})
	cfg, ok := ResolveLocalConfig(nil)
	if !ok || cfg.PathPrefix != "weknora/" {
		t.Fatalf("got cfg=%+v ok=%v", cfg, ok)
	}
}

// OBS resolver 暂时只测二级 fallback（env 兜底在 Task 6 补）
func TestResolveOBSConfig_BuiltinFallback(t *testing.T) {
	ResetBuiltinStorageEngine()
	t.Cleanup(ResetBuiltinStorageEngine)
	setBuiltinStorageEngineForTest(t, &StorageEngineConfig{
		OBS: &OBSEngineConfig{Endpoint: "ep", Region: "r", AccessKey: "ak", SecretKey: "sk", BucketName: "b"},
	})
	cfg, ok := ResolveOBSConfig(nil)
	if !ok || cfg.Endpoint != "ep" {
		t.Fatalf("got cfg=%+v ok=%v", cfg, ok)
	}
}

func TestResolveOBSConfig_PartialMissingRegion(t *testing.T) {
	ResetBuiltinStorageEngine()
	t.Cleanup(ResetBuiltinStorageEngine)
	sec := &StorageEngineConfig{OBS: &OBSEngineConfig{
		Endpoint: "ep", AccessKey: "ak", SecretKey: "sk", BucketName: "b",
		// Region intentionally missing
	}}
	cfg, ok := ResolveOBSConfig(sec)
	if cfg == nil {
		t.Fatalf("want partial sec.OBS returned (not nil)")
	}
	if cfg.Endpoint != "ep" {
		t.Fatalf("want sec.OBS returned (not builtin), got %+v", cfg)
	}
	if ok {
		t.Fatalf("want ok=false for OBS missing Region")
	}
}

// ---------------------------------------------------------------------------
// ResolveDefaultProvider
// ---------------------------------------------------------------------------

func TestResolveDefaultProvider_TenantFirst(t *testing.T) {
	ResetBuiltinStorageEngine()
	t.Cleanup(ResetBuiltinStorageEngine)
	setBuiltinStorageEngineForTest(t, &StorageEngineConfig{DefaultProvider: "cos"})
	p := ResolveDefaultProvider(&StorageEngineConfig{DefaultProvider: "OSS"})
	if p != "oss" {
		t.Fatalf("want lowercased tenant 'oss', got %q", p)
	}
}

func TestResolveDefaultProvider_BuiltinFallback(t *testing.T) {
	ResetBuiltinStorageEngine()
	t.Cleanup(ResetBuiltinStorageEngine)
	setBuiltinStorageEngineForTest(t, &StorageEngineConfig{DefaultProvider: "Cos"})
	p := ResolveDefaultProvider(nil)
	if p != "cos" {
		t.Fatalf("want lowercased builtin 'cos', got %q", p)
	}
}

func TestResolveDefaultProvider_None(t *testing.T) {
	ResetBuiltinStorageEngine()
	t.Cleanup(ResetBuiltinStorageEngine)
	if p := ResolveDefaultProvider(nil); p != "" {
		t.Fatalf("want empty, got %q", p)
	}
}

// ---------------------------------------------------------------------------
// 测试辅助：setBuiltinStorageEngineForTest 用反射式方法直接写 singleton。
// 真实代码 LoadBuiltinStorageEngineConfig 才会写；测试用此 helper 旁路。
// ---------------------------------------------------------------------------

func setBuiltinStorageEngineForTest(t *testing.T, cfg *StorageEngineConfig) {
	t.Helper()
	builtinStorageEngine.Store(cfg)
}

func TestResolveOBSConfig_EnvDeprecationFallback(t *testing.T) {
	ResetBuiltinStorageEngine()
	t.Cleanup(ResetBuiltinStorageEngine)
	ResetOBSEnvDeprecationOnceForTest()
	t.Cleanup(ResetOBSEnvDeprecationOnceForTest)

	t.Setenv("OBS_ENDPOINT", "obs.example.com")
	t.Setenv("OBS_ACCESS_KEY", "ak")
	t.Setenv("OBS_SECRET_KEY", "sk")
	t.Setenv("OBS_BUCKET_NAME", "bkt")

	cfg, ok := ResolveOBSConfig(nil)
	if !ok {
		t.Fatalf("env fallback should succeed, got ok=false cfg=%+v", cfg)
	}
	if cfg.Endpoint != "obs.example.com" {
		t.Fatalf("endpoint = %q", cfg.Endpoint)
	}
	if cfg.Region != "cn-north-4" { // default fallback
		t.Fatalf("region default = %q", cfg.Region)
	}
}

func TestResolveOBSConfig_EnvDeprecationOnce(t *testing.T) {
	ResetBuiltinStorageEngine()
	t.Cleanup(ResetBuiltinStorageEngine)
	ResetOBSEnvDeprecationOnceForTest()
	t.Cleanup(ResetOBSEnvDeprecationOnceForTest)

	t.Setenv("OBS_ENDPOINT", "obs.example.com")
	t.Setenv("OBS_ACCESS_KEY", "ak")
	t.Setenv("OBS_SECRET_KEY", "sk")
	t.Setenv("OBS_BUCKET_NAME", "bkt")

	// 三次调用应只触发一次 warn — 验证 sync.Once 行为通过 warnCount 计数
	for i := 0; i < 3; i++ {
		_, _ = ResolveOBSConfig(nil)
	}
	got := GetOBSEnvDeprecationWarnCountForTest()
	if got != 1 {
		t.Fatalf("want 1 warn, got %d", got)
	}
}

func TestResolveOBSConfig_BuiltinPreemptsEnv(t *testing.T) {
	ResetBuiltinStorageEngine()
	t.Cleanup(ResetBuiltinStorageEngine)
	ResetOBSEnvDeprecationOnceForTest()
	t.Cleanup(ResetOBSEnvDeprecationOnceForTest)

	setBuiltinStorageEngineForTest(t, &StorageEngineConfig{
		OBS: &OBSEngineConfig{Endpoint: "from-builtin", Region: "r", AccessKey: "ak", SecretKey: "sk", BucketName: "b"},
	})
	t.Setenv("OBS_ENDPOINT", "from-env")
	t.Setenv("OBS_ACCESS_KEY", "ak")
	t.Setenv("OBS_SECRET_KEY", "sk")
	t.Setenv("OBS_BUCKET_NAME", "b")

	cfg, ok := ResolveOBSConfig(nil)
	if !ok || cfg.Endpoint != "from-builtin" {
		t.Fatalf("builtin should preempt env, got endpoint=%q ok=%v", cfg.Endpoint, ok)
	}
	if got := GetOBSEnvDeprecationWarnCountForTest(); got != 0 {
		t.Fatalf("warn should NOT fire when builtin matches, got count=%d", got)
	}
}
