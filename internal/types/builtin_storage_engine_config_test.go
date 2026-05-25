package types

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadBuiltinStorageEngine_FileMissing(t *testing.T) {
	ResetBuiltinStorageEngine()
	t.Cleanup(ResetBuiltinStorageEngine)
	dir := t.TempDir()  // 不写文件 -> 文件缺失场景

	if err := LoadBuiltinStorageEngineConfig(dir); err != nil {
		t.Fatalf("want nil err on missing file, got %v", err)
	}
	if GetBuiltinStorageEngine() != nil {
		t.Fatalf("singleton should remain nil")
	}
}

func TestLoadBuiltinStorageEngine_InvalidYAML(t *testing.T) {
	ResetBuiltinStorageEngine()
	t.Cleanup(ResetBuiltinStorageEngine)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "builtin_storage_engine.yaml"),
		[]byte("not: : valid: yaml"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := LoadBuiltinStorageEngineConfig(dir); err != nil {
		t.Fatalf("want nil err on invalid yaml, got %v", err)
	}
	if GetBuiltinStorageEngine() != nil {
		t.Fatalf("singleton should remain nil")
	}
}

func TestLoadBuiltinStorageEngine_EnvInterpolation(t *testing.T) {
	ResetBuiltinStorageEngine()
	t.Cleanup(ResetBuiltinStorageEngine)
	t.Setenv("TEST_OSS_ENDPOINT", "oss-endpoint-from-env")
	t.Setenv("TEST_OSS_AK", "ak-from-env")

	dir := t.TempDir()
	yaml := `
storage_engine:
  default_provider: oss
  oss:
    endpoint: ${TEST_OSS_ENDPOINT}
    access_key: ${TEST_OSS_AK}
    region: cn-hangzhou
    secret_key: literal-sk
    bucket_name: my-bucket
`
	if err := os.WriteFile(filepath.Join(dir, "builtin_storage_engine.yaml"),
		[]byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := LoadBuiltinStorageEngineConfig(dir); err != nil {
		t.Fatalf("load err: %v", err)
	}
	got := GetBuiltinStorageEngine()
	if got == nil {
		t.Fatal("singleton not loaded")
	}
	if got.DefaultProvider != "oss" {
		t.Errorf("default_provider = %q", got.DefaultProvider)
	}
	if got.OSS == nil || got.OSS.Endpoint != "oss-endpoint-from-env" {
		t.Errorf("OSS.Endpoint not interpolated: %+v", got.OSS)
	}
	if got.OSS.AccessKey != "ak-from-env" {
		t.Errorf("OSS.AccessKey not interpolated: %+v", got.OSS)
	}
}

func TestLoadBuiltinStorageEngine_EmptyConfig(t *testing.T) {
	ResetBuiltinStorageEngine()
	t.Cleanup(ResetBuiltinStorageEngine)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "builtin_storage_engine.yaml"),
		[]byte("storage_engine: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := LoadBuiltinStorageEngineConfig(dir); err != nil {
		t.Fatalf("load err: %v", err)
	}
	if GetBuiltinStorageEngine() != nil {
		t.Fatalf("empty cfg should NOT populate singleton")
	}
}

func TestLoadBuiltinStorageEngine_EnvOverridePath(t *testing.T) {
	ResetBuiltinStorageEngine()
	t.Cleanup(ResetBuiltinStorageEngine)
	dir := t.TempDir()
	customPath := filepath.Join(dir, "custom.yaml")
	if err := os.WriteFile(customPath,
		[]byte("storage_engine:\n  default_provider: cos\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BUILTIN_STORAGE_ENGINE_CONFIG", customPath)

	if err := LoadBuiltinStorageEngineConfig("/nonexistent"); err != nil {
		t.Fatalf("load err: %v", err)
	}
	got := GetBuiltinStorageEngine()
	if got == nil || got.DefaultProvider != "cos" {
		t.Fatalf("env override path not honored, got %+v", got)
	}
}

func TestLoadBuiltinStorageEngine_AtomicReplace(t *testing.T) {
	ResetBuiltinStorageEngine()
	t.Cleanup(ResetBuiltinStorageEngine)
	dir := t.TempDir()
	p := filepath.Join(dir, "builtin_storage_engine.yaml")

	if err := os.WriteFile(p, []byte("storage_engine:\n  default_provider: oss\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_ = LoadBuiltinStorageEngineConfig(dir)
	if GetBuiltinStorageEngine().DefaultProvider != "oss" {
		t.Fatal("first load failed")
	}

	if err := os.WriteFile(p, []byte("storage_engine:\n  default_provider: cos\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_ = LoadBuiltinStorageEngineConfig(dir)
	if GetBuiltinStorageEngine().DefaultProvider != "cos" {
		t.Fatal("second load did not replace")
	}
}
