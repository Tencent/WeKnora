package config

import "testing"

func TestPreviewConversionIsOffByDefault(t *testing.T) {
	t.Setenv("WEKNORA_DOCX_EMF_PREVIEW", "")
	t.Setenv("WEKNORA_DOCX_PREVIEW_CACHE_DIR", "")

	cfg := &Config{}
	applyPreviewEnvOverrides(cfg)

	if cfg.Preview == nil {
		t.Fatal("Preview config was not initialized")
	}
	if cfg.Preview.DocxEMF {
		t.Fatal("DOCX EMF preview must default to disabled")
	}
	if cfg.Preview.CacheDir != "" {
		t.Fatalf("CacheDir = %q, want empty default", cfg.Preview.CacheDir)
	}
}

func TestPreviewConversionReadsEnvOverrides(t *testing.T) {
	t.Setenv("WEKNORA_DOCX_EMF_PREVIEW", "true")
	t.Setenv("WEKNORA_DOCX_PREVIEW_CACHE_DIR", "/var/tmp/preview-cache")

	cfg := &Config{}
	applyPreviewEnvOverrides(cfg)

	if !cfg.Preview.DocxEMF {
		t.Fatal("DocxEMF = false, want true")
	}
	if cfg.Preview.CacheDir != "/var/tmp/preview-cache" {
		t.Fatalf("CacheDir = %q", cfg.Preview.CacheDir)
	}
}

func TestPreviewConversionIgnoresUnparsableValue(t *testing.T) {
	t.Setenv("WEKNORA_DOCX_EMF_PREVIEW", "not-a-bool")
	cfg := &Config{}
	applyPreviewEnvOverrides(cfg)
	if cfg.Preview.DocxEMF {
		t.Fatal("DocxEMF = true after an unparsable value")
	}
}
