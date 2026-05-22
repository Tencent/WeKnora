package config

import (
	"fmt"
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
)

// ResolveParserOverrides returns the parser-engine overrides map that the
// document parser pipeline should use for the given tenant. It is a strict
// fallback merge:
//
//   - The tenant's ParserEngineConfig.ToOverridesMap() supplies the base.
//   - For any key the tenant left empty (i.e. missing from the map), the
//     matching system-level default from cfg.ParserDefaults is injected.
//
// Either argument may be nil:
//
//   - cfg / cfg.ParserDefaults nil → degenerate to the tenant-only behaviour
//     ToOverridesMap() already provides (returns nil when both sides are
//     empty so the downstream "no parser configured" error still fires).
//   - tenant nil → start from an empty map and fill in only the system
//     defaults that are present.
//
// Boolean overrides ("…_enable_formula" etc.) are formatted exactly the way
// tenant.ParserEngineConfig.ToOverridesMap() formats them, so downstream
// consumers can't tell whether the value came from the tenant or the system
// default.
func ResolveParserOverrides(cfg *Config, tenant *types.ParserEngineConfig) map[string]string {
	tenantMap := tenant.ToOverridesMap()
	pd := parserDefaults(cfg)
	if pd == nil {
		return tenantMap
	}

	if tenantMap == nil {
		tenantMap = make(map[string]string)
	}

	// MinerU self-hosted defaults
	if m := pd.MinerU; m != nil {
		setIfAbsent(tenantMap, "mineru_endpoint", m.Endpoint)
		setIfAbsent(tenantMap, "mineru_model", m.Model)
		setIfAbsent(tenantMap, "mineru_vlm_server_url", m.VLMServerURL)
		setIfAbsent(tenantMap, "mineru_language", m.Language)
		setBoolIfAbsent(tenantMap, "mineru_enable_formula", m.EnableFormula)
		setBoolIfAbsent(tenantMap, "mineru_enable_table", m.EnableTable)
		setBoolIfAbsent(tenantMap, "mineru_enable_ocr", m.EnableOCR)
	}

	// MinerU cloud API defaults
	if c := pd.MinerUCloud; c != nil {
		setIfAbsent(tenantMap, "mineru_api_key", c.APIKey)
		setIfAbsent(tenantMap, "mineru_cloud_model", c.Model)
		setIfAbsent(tenantMap, "mineru_cloud_language", c.Language)
		setBoolIfAbsent(tenantMap, "mineru_cloud_enable_formula", c.EnableFormula)
		setBoolIfAbsent(tenantMap, "mineru_cloud_enable_table", c.EnableTable)
		setBoolIfAbsent(tenantMap, "mineru_cloud_enable_ocr", c.EnableOCR)
	}

	if len(tenantMap) == 0 {
		return nil
	}
	return tenantMap
}

// ResolveStorageEngineConfig merges the tenant's StorageEngineConfig with
// the system defaults from cfg.StorageDefaults. It always returns a fresh
// *types.StorageEngineConfig — never mutates the inputs — so callers can
// safely pass the result to file-service factories that expect to take
// ownership of the value.
//
// Merge semantics:
//
//   - String fields fall back to the system default only when the tenant
//     value is empty. Tenant non-empty always wins, regardless of which
//     side was "more specific".
//   - When the tenant did not configure a provider sub-struct at all, the
//     system default sub-struct is adopted wholesale (deep-copied).
//   - DefaultProvider follows the same string-fallback rule.
//   - Bool fields (UseSSL / UseTempBucket / ForcePathStyle) are NOT merged
//     because a plain bool cannot distinguish "tenant set false" from
//     "tenant left unset". Operators who need a system-wide use_ssl=true
//     must set it in the tenant config (or accept the per-tenant default).
//   - MinIOEngineConfig.Mode is also not merged: the tenant's deliberate
//     "docker" vs "remote" choice must not be silently overridden by an
//     operator default.
//
// Either argument may be nil; the function returns a non-nil result as
// long as at least one provider sub-struct exists on either side. When
// both sides contribute nothing, it returns nil so callers still hit the
// "no storage configured" branch they had before.
func ResolveStorageEngineConfig(cfg *Config, tenant *types.StorageEngineConfig) *types.StorageEngineConfig {
	sd := storageDefaults(cfg)
	if sd == nil && tenant == nil {
		return nil
	}

	out := &types.StorageEngineConfig{}
	if tenant != nil {
		*out = *tenant
		// Reset pointers; we'll merge each below so the originals stay
		// pristine (no aliasing back to tenant's allocations).
		out.Local = nil
		out.MinIO = nil
		out.COS = nil
		out.TOS = nil
		out.S3 = nil
		out.OSS = nil
		out.KS3 = nil
		out.OBS = nil
	}

	// DefaultProvider: tenant wins; fall back to system default.
	if out.DefaultProvider == "" && sd != nil {
		out.DefaultProvider = strings.TrimSpace(sd.DefaultProvider)
	}

	sdView := sdAsTypes(sd)
	out.Local = mergeLocal(pickLocal(tenant), sdView.Local)
	out.MinIO = mergeMinIO(pickMinIO(tenant), sdView.MinIO)
	out.COS = mergeCOS(pickCOS(tenant), sdView.COS)
	out.TOS = mergeTOS(pickTOS(tenant), sdView.TOS)
	out.S3 = mergeS3(pickS3(tenant), sdView.S3)
	out.OSS = mergeOSS(pickOSS(tenant), sdView.OSS)
	out.KS3 = mergeKS3(pickKS3(tenant), sdView.KS3)
	out.OBS = mergeOBS(pickOBS(tenant), sdView.OBS)

	if isEmptyStorageEngineConfig(out) {
		return nil
	}
	return out
}

// ─── nil-safe accessors ───────────────────────────────────────────────────

func parserDefaults(cfg *Config) *ParserDefaultsConfig {
	if cfg == nil {
		return nil
	}
	return cfg.ParserDefaults
}

func storageDefaults(cfg *Config) *StorageDefaultsConfig {
	if cfg == nil {
		return nil
	}
	return cfg.StorageDefaults
}

// sdAsTypes flattens a *StorageDefaultsConfig into a struct shaped like
// types.StorageEngineConfig so the merge step can treat both sides
// symmetrically. Returns a zero-valued sentinel (rather than nil) when sd
// is nil so callers do not need extra nil checks.
type storageDefaultsView struct {
	Local *types.LocalEngineConfig
	MinIO *types.MinIOEngineConfig
	COS   *types.COSEngineConfig
	TOS   *types.TOSEngineConfig
	S3    *types.S3EngineConfig
	OSS   *types.OSSEngineConfig
	KS3   *types.KS3EngineConfig
	OBS   *types.OBSEngineConfig
}

func sdAsTypes(sd *StorageDefaultsConfig) storageDefaultsView {
	if sd == nil {
		return storageDefaultsView{}
	}
	return storageDefaultsView{
		Local: sd.Local,
		MinIO: sd.MinIO,
		COS:   sd.COS,
		TOS:   sd.TOS,
		S3:    sd.S3,
		OSS:   sd.OSS,
		KS3:   sd.KS3,
		OBS:   sd.OBS,
	}
}

func pickLocal(t *types.StorageEngineConfig) *types.LocalEngineConfig {
	if t == nil {
		return nil
	}
	return t.Local
}
func pickMinIO(t *types.StorageEngineConfig) *types.MinIOEngineConfig {
	if t == nil {
		return nil
	}
	return t.MinIO
}
func pickCOS(t *types.StorageEngineConfig) *types.COSEngineConfig {
	if t == nil {
		return nil
	}
	return t.COS
}
func pickTOS(t *types.StorageEngineConfig) *types.TOSEngineConfig {
	if t == nil {
		return nil
	}
	return t.TOS
}
func pickS3(t *types.StorageEngineConfig) *types.S3EngineConfig {
	if t == nil {
		return nil
	}
	return t.S3
}
func pickOSS(t *types.StorageEngineConfig) *types.OSSEngineConfig {
	if t == nil {
		return nil
	}
	return t.OSS
}
func pickKS3(t *types.StorageEngineConfig) *types.KS3EngineConfig {
	if t == nil {
		return nil
	}
	return t.KS3
}
func pickOBS(t *types.StorageEngineConfig) *types.OBSEngineConfig {
	if t == nil {
		return nil
	}
	return t.OBS
}

// ─── string + bool helpers ───────────────────────────────────────────────

func setIfAbsent(m map[string]string, key, value string) {
	if value == "" {
		return
	}
	if _, ok := m[key]; ok {
		return
	}
	m[key] = value
}

// setBoolIfAbsent mirrors types.ParserEngineConfig.ToOverridesMap, which
// formats *bool values via fmt.Sprintf("%v", *b). Keeping the same format
// lets the rest of the pipeline stay oblivious to whether the value came
// from a tenant or a system default.
func setBoolIfAbsent(m map[string]string, key string, value *bool) {
	if value == nil {
		return
	}
	if _, ok := m[key]; ok {
		return
	}
	m[key] = fmt.Sprintf("%v", *value)
}

func mergeLocal(t, d *types.LocalEngineConfig) *types.LocalEngineConfig {
	if t == nil && d == nil {
		return nil
	}
	if t == nil {
		c := *d
		return &c
	}
	if d == nil {
		c := *t
		return &c
	}
	out := *t
	if out.PathPrefix == "" {
		out.PathPrefix = d.PathPrefix
	}
	return &out
}

func mergeMinIO(t, d *types.MinIOEngineConfig) *types.MinIOEngineConfig {
	if t == nil && d == nil {
		return nil
	}
	if t == nil {
		c := *d
		return &c
	}
	if d == nil {
		c := *t
		return &c
	}
	out := *t
	// Mode is deliberately NOT merged — see ResolveStorageEngineConfig
	// doc comment.
	if out.Endpoint == "" {
		out.Endpoint = d.Endpoint
	}
	if out.AccessKeyID == "" {
		out.AccessKeyID = d.AccessKeyID
	}
	if out.SecretAccessKey == "" {
		out.SecretAccessKey = d.SecretAccessKey
	}
	if out.BucketName == "" {
		out.BucketName = d.BucketName
	}
	if out.PathPrefix == "" {
		out.PathPrefix = d.PathPrefix
	}
	return &out
}

func mergeCOS(t, d *types.COSEngineConfig) *types.COSEngineConfig {
	if t == nil && d == nil {
		return nil
	}
	if t == nil {
		c := *d
		return &c
	}
	if d == nil {
		c := *t
		return &c
	}
	out := *t
	if out.SecretID == "" {
		out.SecretID = d.SecretID
	}
	if out.SecretKey == "" {
		out.SecretKey = d.SecretKey
	}
	if out.Region == "" {
		out.Region = d.Region
	}
	if out.BucketName == "" {
		out.BucketName = d.BucketName
	}
	if out.AppID == "" {
		out.AppID = d.AppID
	}
	if out.PathPrefix == "" {
		out.PathPrefix = d.PathPrefix
	}
	return &out
}

func mergeTOS(t, d *types.TOSEngineConfig) *types.TOSEngineConfig {
	if t == nil && d == nil {
		return nil
	}
	if t == nil {
		c := *d
		return &c
	}
	if d == nil {
		c := *t
		return &c
	}
	out := *t
	if out.Endpoint == "" {
		out.Endpoint = d.Endpoint
	}
	if out.Region == "" {
		out.Region = d.Region
	}
	if out.AccessKey == "" {
		out.AccessKey = d.AccessKey
	}
	if out.SecretKey == "" {
		out.SecretKey = d.SecretKey
	}
	if out.BucketName == "" {
		out.BucketName = d.BucketName
	}
	if out.PathPrefix == "" {
		out.PathPrefix = d.PathPrefix
	}
	return &out
}

func mergeS3(t, d *types.S3EngineConfig) *types.S3EngineConfig {
	if t == nil && d == nil {
		return nil
	}
	if t == nil {
		c := *d
		return &c
	}
	if d == nil {
		c := *t
		return &c
	}
	out := *t
	if out.Endpoint == "" {
		out.Endpoint = d.Endpoint
	}
	if out.Region == "" {
		out.Region = d.Region
	}
	if out.AccessKey == "" {
		out.AccessKey = d.AccessKey
	}
	if out.SecretKey == "" {
		out.SecretKey = d.SecretKey
	}
	if out.BucketName == "" {
		out.BucketName = d.BucketName
	}
	if out.PathPrefix == "" {
		out.PathPrefix = d.PathPrefix
	}
	// UseSSL / ForcePathStyle deliberately not merged.
	return &out
}

func mergeOSS(t, d *types.OSSEngineConfig) *types.OSSEngineConfig {
	if t == nil && d == nil {
		return nil
	}
	if t == nil {
		c := *d
		return &c
	}
	if d == nil {
		c := *t
		return &c
	}
	out := *t
	if out.Endpoint == "" {
		out.Endpoint = d.Endpoint
	}
	if out.Region == "" {
		out.Region = d.Region
	}
	if out.AccessKey == "" {
		out.AccessKey = d.AccessKey
	}
	if out.SecretKey == "" {
		out.SecretKey = d.SecretKey
	}
	if out.BucketName == "" {
		out.BucketName = d.BucketName
	}
	if out.PathPrefix == "" {
		out.PathPrefix = d.PathPrefix
	}
	if out.TempBucketName == "" {
		out.TempBucketName = d.TempBucketName
	}
	if out.TempRegion == "" {
		out.TempRegion = d.TempRegion
	}
	// UseTempBucket deliberately not merged.
	return &out
}

func mergeKS3(t, d *types.KS3EngineConfig) *types.KS3EngineConfig {
	if t == nil && d == nil {
		return nil
	}
	if t == nil {
		c := *d
		return &c
	}
	if d == nil {
		c := *t
		return &c
	}
	out := *t
	if out.Endpoint == "" {
		out.Endpoint = d.Endpoint
	}
	if out.Region == "" {
		out.Region = d.Region
	}
	if out.AccessKey == "" {
		out.AccessKey = d.AccessKey
	}
	if out.SecretKey == "" {
		out.SecretKey = d.SecretKey
	}
	if out.BucketName == "" {
		out.BucketName = d.BucketName
	}
	if out.PathPrefix == "" {
		out.PathPrefix = d.PathPrefix
	}
	return &out
}

func mergeOBS(t, d *types.OBSEngineConfig) *types.OBSEngineConfig {
	if t == nil && d == nil {
		return nil
	}
	if t == nil {
		c := *d
		return &c
	}
	if d == nil {
		c := *t
		return &c
	}
	out := *t
	if out.Endpoint == "" {
		out.Endpoint = d.Endpoint
	}
	if out.Region == "" {
		out.Region = d.Region
	}
	if out.AccessKey == "" {
		out.AccessKey = d.AccessKey
	}
	if out.SecretKey == "" {
		out.SecretKey = d.SecretKey
	}
	if out.BucketName == "" {
		out.BucketName = d.BucketName
	}
	if out.PathPrefix == "" {
		out.PathPrefix = d.PathPrefix
	}
	// UseSSL deliberately not merged.
	return &out
}

func isEmptyStorageEngineConfig(c *types.StorageEngineConfig) bool {
	if c == nil {
		return true
	}
	return c.DefaultProvider == "" &&
		c.Local == nil && c.MinIO == nil && c.COS == nil &&
		c.TOS == nil && c.S3 == nil && c.OSS == nil &&
		c.KS3 == nil && c.OBS == nil
}
