package config

import (
	"reflect"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

func boolPtr(b bool) *bool { return &b }

// ─── ResolveParserOverrides ──────────────────────────────────────────────

// A1: tenant has values, no system default → tenant wins, no regression.
func TestResolveParserOverrides_TenantOnly_NoDefaults(t *testing.T) {
	tenant := &types.ParserEngineConfig{
		MinerUEndpoint: "http://tenant.example/parse",
		MinerUAPIKey:   "tenant-key",
	}

	got := ResolveParserOverrides(nil, tenant)
	want := map[string]string{
		"mineru_endpoint": "http://tenant.example/parse",
		"mineru_api_key":  "tenant-key",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ResolveParserOverrides(nil,tenant) = %v, want %v", got, want)
	}
}

// A2: tenant has values AND system defaults → tenant wins on every shared
// key. Single most important assertion in this PR.
func TestResolveParserOverrides_TenantWinsOverDefaults(t *testing.T) {
	cfg := &Config{
		ParserDefaults: &ParserDefaultsConfig{
			MinerU: &MinerUDefaults{
				Endpoint:      "http://system.example/parse",
				Model:         "pipeline",
				Language:      "en",
				EnableFormula: boolPtr(false),
			},
			MinerUCloud: &MinerUCloudDefaults{
				APIKey: "system-cloud-key",
				Model:  "vlm",
			},
		},
	}
	tenant := &types.ParserEngineConfig{
		MinerUEndpoint:      "http://tenant.example/parse",
		MinerUModel:         "vlm-vllm-engine",
		MinerULanguage:      "ch",
		MinerUEnableFormula: boolPtr(true),
		MinerUAPIKey:        "tenant-cloud-key",
		MinerUCloudModel:    "MinerU-HTML",
	}

	got := ResolveParserOverrides(cfg, tenant)
	want := map[string]string{
		"mineru_endpoint":        "http://tenant.example/parse",
		"mineru_model":           "vlm-vllm-engine",
		"mineru_language":        "ch",
		"mineru_enable_formula":  "true",
		"mineru_api_key":         "tenant-cloud-key",
		"mineru_cloud_model":     "MinerU-HTML",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ResolveParserOverrides tenant-wins = %v, want %v", got, want)
	}
}

// B1: no tenant config and no system defaults → nil, preserves
// "engine not configured" downstream behaviour.
func TestResolveParserOverrides_NoTenant_NoDefaults_ReturnsNil(t *testing.T) {
	if got := ResolveParserOverrides(nil, nil); got != nil {
		t.Errorf("ResolveParserOverrides(nil,nil) = %v, want nil", got)
	}
	if got := ResolveParserOverrides(&Config{}, nil); got != nil {
		t.Errorf("ResolveParserOverrides(empty,nil) = %v, want nil", got)
	}
}

// B2: tenant absent but system default present → overrides should hold
// the defaults so the parser becomes usable (the new behaviour).
func TestResolveParserOverrides_NoTenant_WithDefaults(t *testing.T) {
	cfg := &Config{
		ParserDefaults: &ParserDefaultsConfig{
			MinerU: &MinerUDefaults{
				Endpoint:    "http://system.example/parse",
				Language:    "ch",
				EnableOCR:   boolPtr(true),
				EnableTable: boolPtr(false),
			},
			MinerUCloud: &MinerUCloudDefaults{
				APIKey: "system-cloud-key",
			},
		},
	}

	got := ResolveParserOverrides(cfg, nil)
	want := map[string]string{
		"mineru_endpoint":      "http://system.example/parse",
		"mineru_language":      "ch",
		"mineru_enable_ocr":    "true",
		"mineru_enable_table":  "false",
		"mineru_api_key":       "system-cloud-key",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ResolveParserOverrides defaults-only = %v, want %v", got, want)
	}
}

// C2: tenant supplies only api_key, system supplies endpoint → merged.
func TestResolveParserOverrides_PartialTenantPartialDefaults(t *testing.T) {
	cfg := &Config{
		ParserDefaults: &ParserDefaultsConfig{
			MinerU: &MinerUDefaults{
				Endpoint: "http://system.example/parse",
				Model:    "pipeline",
			},
		},
	}
	tenant := &types.ParserEngineConfig{
		MinerUAPIKey: "tenant-cloud-key",
	}

	got := ResolveParserOverrides(cfg, tenant)
	want := map[string]string{
		"mineru_api_key":  "tenant-cloud-key",
		"mineru_endpoint": "http://system.example/parse",
		"mineru_model":    "pipeline",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ResolveParserOverrides partial = %v, want %v", got, want)
	}
}

// ─── ResolveStorageEngineConfig ──────────────────────────────────────────

// D1: tenant fully configured for MinIO, no system default → tenant.
func TestResolveStorageEngineConfig_TenantOnly_NoDefaults(t *testing.T) {
	tenant := &types.StorageEngineConfig{
		DefaultProvider: "minio",
		MinIO: &types.MinIOEngineConfig{
			Mode:            "remote",
			Endpoint:        "tenant.example:9000",
			AccessKeyID:     "ak",
			SecretAccessKey: "sk",
			BucketName:      "tenant-bucket",
			UseSSL:          true,
		},
	}

	got := ResolveStorageEngineConfig(nil, tenant)
	if got == nil {
		t.Fatal("ResolveStorageEngineConfig returned nil, want merged tenant config")
	}
	if got.DefaultProvider != "minio" {
		t.Errorf("DefaultProvider = %q, want minio", got.DefaultProvider)
	}
	if got.MinIO == nil || got.MinIO.Endpoint != "tenant.example:9000" {
		t.Errorf("MinIO endpoint = %v, want tenant.example:9000", got.MinIO)
	}
	if !got.MinIO.UseSSL {
		t.Errorf("UseSSL = false, want true (tenant value preserved)")
	}
	if got == tenant {
		t.Errorf("expected fresh struct, got the same pointer")
	}
}

// D3: tenant has no MinIO block, system default does → adopted wholesale.
func TestResolveStorageEngineConfig_DefaultsAdoptedWhenTenantEmpty(t *testing.T) {
	cfg := &Config{
		StorageDefaults: &StorageDefaultsConfig{
			DefaultProvider: "minio",
			MinIO: &types.MinIOEngineConfig{
				Mode:            "remote",
				Endpoint:        "sys.example:9000",
				AccessKeyID:     "sys-ak",
				SecretAccessKey: "sys-sk",
				BucketName:      "sys-bucket",
			},
		},
	}
	tenant := &types.StorageEngineConfig{
		// Tenant declared COS but never filled MinIO.
		COS: &types.COSEngineConfig{SecretID: "cos-id"},
	}

	got := ResolveStorageEngineConfig(cfg, tenant)
	if got == nil {
		t.Fatal("merged config nil")
	}
	if got.DefaultProvider != "minio" {
		t.Errorf("DefaultProvider = %q, want minio (from system default)", got.DefaultProvider)
	}
	if got.MinIO == nil || got.MinIO.Endpoint != "sys.example:9000" {
		t.Errorf("MinIO from defaults missing: %+v", got.MinIO)
	}
	if got.COS == nil || got.COS.SecretID != "cos-id" {
		t.Errorf("COS from tenant lost: %+v", got.COS)
	}
}

// Partial merge: tenant has endpoint+bucket, system fills credentials.
func TestResolveStorageEngineConfig_PartialMerge(t *testing.T) {
	cfg := &Config{
		StorageDefaults: &StorageDefaultsConfig{
			S3: &types.S3EngineConfig{
				Region:    "us-east-1",
				AccessKey: "sys-ak",
				SecretKey: "sys-sk",
			},
		},
	}
	tenant := &types.StorageEngineConfig{
		DefaultProvider: "s3",
		S3: &types.S3EngineConfig{
			Endpoint:   "https://s3.tenant.example",
			BucketName: "tenant-bucket",
		},
	}
	got := ResolveStorageEngineConfig(cfg, tenant)
	if got.S3.Endpoint != "https://s3.tenant.example" {
		t.Errorf("Endpoint = %q, want tenant value", got.S3.Endpoint)
	}
	if got.S3.BucketName != "tenant-bucket" {
		t.Errorf("BucketName = %q, want tenant value", got.S3.BucketName)
	}
	if got.S3.AccessKey != "sys-ak" {
		t.Errorf("AccessKey = %q, want system fallback", got.S3.AccessKey)
	}
	if got.S3.SecretKey != "sys-sk" {
		t.Errorf("SecretKey = %q, want system fallback", got.S3.SecretKey)
	}
	if got.S3.Region != "us-east-1" {
		t.Errorf("Region = %q, want system fallback", got.S3.Region)
	}
}

// H: bool conflict — tenant explicit false, default true. Bool fields are
// intentionally not merged; the tenant value must remain.
func TestResolveStorageEngineConfig_BoolFieldsNotMerged(t *testing.T) {
	cfg := &Config{
		StorageDefaults: &StorageDefaultsConfig{
			MinIO: &types.MinIOEngineConfig{
				Endpoint: "sys.example:9000",
				UseSSL:   true,
			},
		},
	}
	tenant := &types.StorageEngineConfig{
		MinIO: &types.MinIOEngineConfig{
			Endpoint: "tenant.example:9000",
			UseSSL:   false,
		},
	}
	got := ResolveStorageEngineConfig(cfg, tenant)
	if got.MinIO.UseSSL {
		t.Errorf("UseSSL = true; bool fields must not be overwritten by defaults")
	}
}

// MinIO.Mode must not be merged from defaults.
func TestResolveStorageEngineConfig_MinIOModeNotMerged(t *testing.T) {
	cfg := &Config{
		StorageDefaults: &StorageDefaultsConfig{
			MinIO: &types.MinIOEngineConfig{
				Mode:     "docker",
				Endpoint: "sys.example:9000",
			},
		},
	}
	tenant := &types.StorageEngineConfig{
		MinIO: &types.MinIOEngineConfig{
			Mode: "remote",
		},
	}
	got := ResolveStorageEngineConfig(cfg, tenant)
	if got.MinIO.Mode != "remote" {
		t.Errorf("Mode = %q, want remote (tenant preserved)", got.MinIO.Mode)
	}
	if got.MinIO.Endpoint != "sys.example:9000" {
		t.Errorf("Endpoint = %q, want sys.example:9000 (fallback)", got.MinIO.Endpoint)
	}
}

// Nil ⊕ nil → nil so callers continue to hit the "not configured" branch.
func TestResolveStorageEngineConfig_NilArguments(t *testing.T) {
	if got := ResolveStorageEngineConfig(nil, nil); got != nil {
		t.Errorf("ResolveStorageEngineConfig(nil,nil) = %+v, want nil", got)
	}
	if got := ResolveStorageEngineConfig(&Config{}, nil); got != nil {
		t.Errorf("ResolveStorageEngineConfig(empty,nil) = %+v, want nil", got)
	}
	if got := ResolveStorageEngineConfig(nil, &types.StorageEngineConfig{}); got != nil {
		t.Errorf("ResolveStorageEngineConfig(nil,empty) = %+v, want nil (empty struct after merge)", got)
	}
}

// Result must not alias caller's allocations: mutating it cannot reach back
// to tenant. We patch through every provider sub-struct.
func TestResolveStorageEngineConfig_DoesNotAliasInputs(t *testing.T) {
	tenant := &types.StorageEngineConfig{
		MinIO: &types.MinIOEngineConfig{Endpoint: "before"},
	}
	got := ResolveStorageEngineConfig(nil, tenant)
	if got.MinIO == tenant.MinIO {
		t.Fatal("MinIO pointer aliases tenant input")
	}
	got.MinIO.Endpoint = "after"
	if tenant.MinIO.Endpoint != "before" {
		t.Errorf("mutating result changed input: %q", tenant.MinIO.Endpoint)
	}
}
