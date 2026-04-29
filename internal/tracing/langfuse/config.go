// Package langfuse implements a lightweight client for the Langfuse ingestion
// API (https://langfuse.com/docs/api). It lets WeKnora record LLM traces,
// generations and token usage in Langfuse without pulling in a heavy SDK.
//
// The integration is fully opt-in: when disabled (the default), all public
// entry points are cheap no-ops, so callers can wire them unconditionally.
package langfuse

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config holds the runtime configuration for the Langfuse client.
//
// In practice users enable Langfuse purely through environment variables —
// Host / PublicKey / SecretKey — which matches every other Langfuse SDK and
// keeps WeKnora's YAML config free of secrets.
type Config struct {
	// Enabled is the master switch. If false the entire package is a no-op.
	Enabled bool
	// Host is the Langfuse base URL, e.g. https://cloud.langfuse.com or
	// https://us.cloud.langfuse.com or a self-hosted address.
	Host string
	// PublicKey / SecretKey are the project credentials used for Basic Auth.
	PublicKey string
	SecretKey string
	// FlushAt flushes the queued events once the buffer reaches this size.
	FlushAt int
	// FlushInterval is the maximum time between automatic flushes.
	FlushInterval time.Duration
	// QueueSize bounds the in-memory buffer to avoid unbounded growth if the
	// Langfuse endpoint is unreachable.
	QueueSize int
	// RequestTimeout is the HTTP timeout for a single ingestion batch.
	RequestTimeout time.Duration
	// Release / Environment are attached to every trace for filtering in the
	// Langfuse UI (e.g. release="v0.4.2", environment="production").
	Release     string
	Environment string
	// SampleRate (0..1) controls trace sampling. 0 means "use 1.0".
	SampleRate float64
	// Debug enables verbose logging of batch send errors.
	Debug bool
}

// LoadConfigFromEnv builds a Config by reading the LANGFUSE_* environment
// variables, mirroring the official Python / JS SDK conventions.
func LoadConfigFromEnv() Config {
	cfg := Config{
		Host:           firstNonEmpty(os.Getenv("LANGFUSE_HOST"), "https://cloud.langfuse.com"),
		PublicKey:      strings.TrimSpace(os.Getenv("LANGFUSE_PUBLIC_KEY")),
		SecretKey:      strings.TrimSpace(os.Getenv("LANGFUSE_SECRET_KEY")),
		Release:        strings.TrimSpace(os.Getenv("LANGFUSE_RELEASE")),
		Environment:    strings.TrimSpace(os.Getenv("LANGFUSE_ENVIRONMENT")),
		FlushAt:        15,
		FlushInterval:  3 * time.Second,
		QueueSize:      2048,
		RequestTimeout: 10 * time.Second,
		SampleRate:     1.0,
	}

	if v := strings.TrimSpace(os.Getenv("LANGFUSE_ENABLED")); v != "" {
		cfg.Enabled = parseBool(v)
	} else if cfg.PublicKey != "" && cfg.SecretKey != "" {
		// Auto-enable when credentials are present — matches the Python SDK.
		cfg.Enabled = true
	}

	if v := strings.TrimSpace(os.Getenv("LANGFUSE_FLUSH_AT")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.FlushAt = n
		}
	}
	if v := strings.TrimSpace(os.Getenv("LANGFUSE_FLUSH_INTERVAL")); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			cfg.FlushInterval = d
		} else if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.FlushInterval = time.Duration(n) * time.Second
		}
	}
	if v := strings.TrimSpace(os.Getenv("LANGFUSE_QUEUE_SIZE")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.QueueSize = n
		}
	}
	if v := strings.TrimSpace(os.Getenv("LANGFUSE_REQUEST_TIMEOUT")); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			cfg.RequestTimeout = d
		}
	}
	if v := strings.TrimSpace(os.Getenv("LANGFUSE_SAMPLE_RATE")); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil && f >= 0 && f <= 1 {
			cfg.SampleRate = f
		}
	}
	if v := strings.TrimSpace(os.Getenv("LANGFUSE_DEBUG")); v != "" {
		cfg.Debug = parseBool(v)
	}

	if cfg.SampleRate == 0 {
		cfg.SampleRate = 1.0
	}

	return cfg
}

// Validate verifies required fields are present when Langfuse is enabled.
func (c Config) Validate() error {
	if !c.Enabled {
		return nil
	}
	if strings.TrimSpace(c.Host) == "" {
		return fmt.Errorf("langfuse: host is required when enabled")
	}
	if c.PublicKey == "" || c.SecretKey == "" {
		return fmt.Errorf("langfuse: public_key and secret_key are required when enabled")
	}
	return nil
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if s := strings.TrimSpace(v); s != "" {
			return s
		}
	}
	return ""
}

func parseBool(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "t", "yes", "y", "on":
		return true
	}
	return false
}
