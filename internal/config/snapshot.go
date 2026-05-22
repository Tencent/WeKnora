package config

import (
	"regexp"
	"sync/atomic"
)

// snapshotPtr stores the most recently loaded *Config, set by LoadConfig on
// success. Callers that need read-only access from package-level helpers
// (e.g. attachment_processor.go, im/service.go) read it via Snapshot()
// rather than threading config.Config through every signature.
//
// Snapshot is intentionally a fallback for hard-to-refactor sites; the
// preferred wiring is still constructor injection via the dig container.
var snapshotPtr atomic.Pointer[Config]

// Snapshot returns the most recently loaded Config, or nil if LoadConfig
// has not run yet. Callers must treat the result as read-only and tolerate
// nil so that test binaries which never call LoadConfig do not panic.
func Snapshot() *Config {
	return snapshotPtr.Load()
}

// storeSnapshot replaces the global snapshot pointer. Called from
// LoadConfig once validation passes so callers never observe a
// partially-built Config.
func storeSnapshot(cfg *Config) {
	if cfg == nil {
		return
	}
	snapshotPtr.Store(cfg)
}

// unresolvedPlaceholderPattern matches strings that LoadConfig's
// ${ENV_VAR} substitution left untouched because the variable was unset.
// Used by sanitizeUnresolvedPlaceholders to clear new-style defaults that
// would otherwise carry literal "${FOO}" text into runtime checks.
var unresolvedPlaceholderPattern = regexp.MustCompile(`^\$\{[A-Z0-9_]+\}$`)

// sanitizeUnresolvedPlaceholders walks the parser_defaults / storage_defaults
// subtrees and replaces any string field still equal to a literal "${VAR}"
// token with the empty string. This only affects the new system-default
// fields introduced together with this sanitizer; existing fields with the
// same latent bug (OIDC, models, …) are deliberately left alone so that the
// sanitizer change is reviewable in isolation.
func sanitizeUnresolvedPlaceholders(cfg *Config) {
	if cfg == nil {
		return
	}
	sanitizeParserDefaults(cfg.ParserDefaults)
	sanitizeStorageDefaults(cfg.StorageDefaults)
}

func clearIfUnresolved(s *string) {
	if s == nil {
		return
	}
	if unresolvedPlaceholderPattern.MatchString(*s) {
		*s = ""
	}
}

func sanitizeParserDefaults(p *ParserDefaultsConfig) {
	if p == nil {
		return
	}
	if p.MinerU != nil {
		clearIfUnresolved(&p.MinerU.Endpoint)
		clearIfUnresolved(&p.MinerU.Model)
		clearIfUnresolved(&p.MinerU.VLMServerURL)
		clearIfUnresolved(&p.MinerU.Language)
	}
	if p.MinerUCloud != nil {
		clearIfUnresolved(&p.MinerUCloud.APIKey)
		clearIfUnresolved(&p.MinerUCloud.Model)
		clearIfUnresolved(&p.MinerUCloud.Language)
	}
}

func sanitizeStorageDefaults(s *StorageDefaultsConfig) {
	if s == nil {
		return
	}
	clearIfUnresolved(&s.DefaultProvider)
	if s.Local != nil {
		clearIfUnresolved(&s.Local.PathPrefix)
	}
	if s.MinIO != nil {
		clearIfUnresolved(&s.MinIO.Mode)
		clearIfUnresolved(&s.MinIO.Endpoint)
		clearIfUnresolved(&s.MinIO.AccessKeyID)
		clearIfUnresolved(&s.MinIO.SecretAccessKey)
		clearIfUnresolved(&s.MinIO.BucketName)
		clearIfUnresolved(&s.MinIO.PathPrefix)
	}
	if s.COS != nil {
		clearIfUnresolved(&s.COS.SecretID)
		clearIfUnresolved(&s.COS.SecretKey)
		clearIfUnresolved(&s.COS.Region)
		clearIfUnresolved(&s.COS.BucketName)
		clearIfUnresolved(&s.COS.AppID)
		clearIfUnresolved(&s.COS.PathPrefix)
	}
	if s.TOS != nil {
		clearIfUnresolved(&s.TOS.Endpoint)
		clearIfUnresolved(&s.TOS.Region)
		clearIfUnresolved(&s.TOS.AccessKey)
		clearIfUnresolved(&s.TOS.SecretKey)
		clearIfUnresolved(&s.TOS.BucketName)
		clearIfUnresolved(&s.TOS.PathPrefix)
	}
	if s.S3 != nil {
		clearIfUnresolved(&s.S3.Endpoint)
		clearIfUnresolved(&s.S3.Region)
		clearIfUnresolved(&s.S3.AccessKey)
		clearIfUnresolved(&s.S3.SecretKey)
		clearIfUnresolved(&s.S3.BucketName)
		clearIfUnresolved(&s.S3.PathPrefix)
	}
	if s.OSS != nil {
		clearIfUnresolved(&s.OSS.Endpoint)
		clearIfUnresolved(&s.OSS.Region)
		clearIfUnresolved(&s.OSS.AccessKey)
		clearIfUnresolved(&s.OSS.SecretKey)
		clearIfUnresolved(&s.OSS.BucketName)
		clearIfUnresolved(&s.OSS.PathPrefix)
		clearIfUnresolved(&s.OSS.TempBucketName)
		clearIfUnresolved(&s.OSS.TempRegion)
	}
	if s.KS3 != nil {
		clearIfUnresolved(&s.KS3.Endpoint)
		clearIfUnresolved(&s.KS3.Region)
		clearIfUnresolved(&s.KS3.AccessKey)
		clearIfUnresolved(&s.KS3.SecretKey)
		clearIfUnresolved(&s.KS3.BucketName)
		clearIfUnresolved(&s.KS3.PathPrefix)
	}
	if s.OBS != nil {
		clearIfUnresolved(&s.OBS.Endpoint)
		clearIfUnresolved(&s.OBS.Region)
		clearIfUnresolved(&s.OBS.AccessKey)
		clearIfUnresolved(&s.OBS.SecretKey)
		clearIfUnresolved(&s.OBS.BucketName)
		clearIfUnresolved(&s.OBS.PathPrefix)
	}
}
