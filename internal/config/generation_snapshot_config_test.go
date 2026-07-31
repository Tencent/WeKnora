package config

import "testing"

func TestApplyReparseGenerationDefaultsPreservesExplicitNoopFastPathFalse(t *testing.T) {
	cfg := &Config{ReparseGeneration: &ReparseGenerationConfig{
		NoopFastPath: false,
	}}

	applyReparseGenerationDefaults(cfg, false, true)

	if cfg.ReparseGeneration.NoopFastPath {
		t.Fatal("explicit noop_fast_path=false must not be overwritten by defaults")
	}
	if cfg.ReparseGeneration.GCRetentionHours != 24 {
		t.Fatalf("gc_retention_hours default = %d, want 24", cfg.ReparseGeneration.GCRetentionHours)
	}
	if cfg.ReparseGeneration.GCBatchSize != 100 {
		t.Fatalf("gc_batch_size default = %d, want 100", cfg.ReparseGeneration.GCBatchSize)
	}
}

func TestApplyReparseGenerationDefaultsDefaultsNoopFastPathWhenUnset(t *testing.T) {
	cfg := &Config{ReparseGeneration: &ReparseGenerationConfig{}}

	applyReparseGenerationDefaults(cfg, false, false)

	if !cfg.ReparseGeneration.NoopFastPath {
		t.Fatal("noop_fast_path should default to true when not explicitly configured")
	}
}

func TestApplyReparseGenerationDefaultsDefaultsEnabledWhenUnset(t *testing.T) {
	cfg := &Config{ReparseGeneration: &ReparseGenerationConfig{}}

	applyReparseGenerationDefaults(cfg, false, false)

	if !cfg.ReparseGeneration.Enabled {
		t.Fatal("enabled should default to true when not explicitly configured")
	}
}

func TestApplyReparseGenerationDefaultsPreservesExplicitEnabledFalse(t *testing.T) {
	cfg := &Config{ReparseGeneration: &ReparseGenerationConfig{
		Enabled: false,
	}}

	applyReparseGenerationDefaults(cfg, true, false)

	if cfg.ReparseGeneration.Enabled {
		t.Fatal("explicit reparse_generation.enabled=false must not be overwritten by defaults")
	}
}

func TestApplyArtifactCacheDefaultsPreservesExplicitWriteDisabled(t *testing.T) {
	cfg := &Config{ArtifactCache: &ArtifactCacheConfig{
		WriteEnabled: false,
	}}

	applyArtifactCacheDefaults(cfg, false, true)

	if cfg.ArtifactCache.WriteEnabled {
		t.Fatal("explicit artifact_cache.write_enabled=false must not be overwritten by defaults")
	}
	if cfg.ArtifactCache.MaxInlineBytes != 16*1024*1024 {
		t.Fatalf("max_inline_bytes default = %d, want %d", cfg.ArtifactCache.MaxInlineBytes, 16*1024*1024)
	}
	if len(cfg.ArtifactCache.Stages) == 0 {
		t.Fatal("artifact cache stages should default when omitted")
	}
}

func TestApplyArtifactCacheDefaultsDefaultsWriteEnabledWhenUnset(t *testing.T) {
	cfg := &Config{ArtifactCache: &ArtifactCacheConfig{}}

	applyArtifactCacheDefaults(cfg, false, false)

	if !cfg.ArtifactCache.WriteEnabled {
		t.Fatal("write_enabled should default to true when not explicitly configured")
	}
}

func TestApplyArtifactCacheDefaultsDefaultsReadEnabledWhenUnset(t *testing.T) {
	cfg := &Config{ArtifactCache: &ArtifactCacheConfig{}}

	applyArtifactCacheDefaults(cfg, false, false)

	if !cfg.ArtifactCache.ReadEnabled {
		t.Fatal("read_enabled should default to true when not explicitly configured")
	}
}

func TestApplyArtifactCacheDefaultsPreservesExplicitReadDisabled(t *testing.T) {
	cfg := &Config{ArtifactCache: &ArtifactCacheConfig{
		ReadEnabled: false,
	}}

	applyArtifactCacheDefaults(cfg, true, false)

	if cfg.ArtifactCache.ReadEnabled {
		t.Fatal("explicit artifact_cache.read_enabled=false must not be overwritten by defaults")
	}
}
