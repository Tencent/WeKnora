package embedding

import (
	"testing"
	"time"
)

func TestLoadEmbeddingRuntimeConfigDefaults(t *testing.T) {
	t.Setenv("WEKNORA_EMBED_TIMEOUT", "")
	t.Setenv("WEKNORA_EMBED_MAX_RETRIES", "")
	t.Setenv("WEKNORA_EMBED_MAX_CONCURRENCY", "")

	got := loadEmbeddingRuntimeConfig()
	if got.Timeout != defaultEmbeddingTimeout {
		t.Fatalf("timeout = %s, want %s", got.Timeout, defaultEmbeddingTimeout)
	}
	if got.MaxRetries != defaultEmbeddingMaxRetries {
		t.Fatalf("max retries = %d, want %d", got.MaxRetries, defaultEmbeddingMaxRetries)
	}
	if got.MaxConcurrency != defaultEmbeddingMaxConcurrency {
		t.Fatalf("max concurrency = %d, want %d", got.MaxConcurrency, defaultEmbeddingMaxConcurrency)
	}
}

func TestLoadEmbeddingRuntimeConfigParsesValues(t *testing.T) {
	t.Setenv("WEKNORA_EMBED_TIMEOUT", "2m")
	t.Setenv("WEKNORA_EMBED_MAX_RETRIES", "0")
	t.Setenv("WEKNORA_EMBED_MAX_CONCURRENCY", "7")

	got := loadEmbeddingRuntimeConfig()
	if got.Timeout != 2*time.Minute || got.MaxRetries != 0 || got.MaxConcurrency != 7 {
		t.Fatalf("unexpected runtime config: %+v", got)
	}
}

func TestLoadEmbeddingRuntimeConfigAcceptsTimeoutSeconds(t *testing.T) {
	t.Setenv("WEKNORA_EMBED_TIMEOUT", "45")
	if got := loadEmbeddingRuntimeConfig().Timeout; got != 45*time.Second {
		t.Fatalf("timeout = %s, want 45s", got)
	}
}

func TestLoadEmbeddingRuntimeConfigRejectsInvalidValues(t *testing.T) {
	t.Setenv("WEKNORA_EMBED_TIMEOUT", "-1s")
	t.Setenv("WEKNORA_EMBED_MAX_RETRIES", "-1")
	t.Setenv("WEKNORA_EMBED_MAX_CONCURRENCY", "0")

	got := loadEmbeddingRuntimeConfig()
	if got.Timeout != defaultEmbeddingTimeout || got.MaxRetries != defaultEmbeddingMaxRetries || got.MaxConcurrency != defaultEmbeddingMaxConcurrency {
		t.Fatalf("invalid values should use defaults, got %+v", got)
	}
}
