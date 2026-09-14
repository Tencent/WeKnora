package embedding

import (
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	defaultEmbeddingTimeout        = 120 * time.Second
	defaultEmbeddingMaxRetries     = 3
	defaultEmbeddingMaxConcurrency = 20
)

// embeddingRuntimeConfig contains process-wide defaults for remote embedding
// requests. Model-level max_concurrency remains the higher-priority override.
type embeddingRuntimeConfig struct {
	Timeout        time.Duration
	MaxRetries     int
	MaxConcurrency int
}

func loadEmbeddingRuntimeConfig() embeddingRuntimeConfig {
	return embeddingRuntimeConfig{
		Timeout:        embeddingDurationEnv("WEKNORA_EMBED_TIMEOUT", defaultEmbeddingTimeout),
		MaxRetries:     embeddingNonNegativeIntEnv("WEKNORA_EMBED_MAX_RETRIES", defaultEmbeddingMaxRetries),
		MaxConcurrency: embeddingPositiveIntEnv("WEKNORA_EMBED_MAX_CONCURRENCY", defaultEmbeddingMaxConcurrency),
	}
}

func embeddingDurationEnv(name string, fallback time.Duration) time.Duration {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	if duration, err := time.ParseDuration(value); err == nil && duration > 0 {
		return duration
	}
	// Accept a plain number as seconds for consistency with the other numeric
	// deployment settings and to make the environment variable easy to use.
	if seconds, err := strconv.Atoi(value); err == nil && seconds > 0 {
		return time.Duration(seconds) * time.Second
	}
	return fallback
}

func embeddingNonNegativeIntEnv(name string, fallback int) int {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < 0 {
		return fallback
	}
	return parsed
}

func embeddingPositiveIntEnv(name string, fallback int) int {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
}
