package common

import (
	"crypto/tls"
	"testing"
)

func resetRedisTLSEnv(t *testing.T) {
	t.Helper()
	t.Setenv("REDIS_USE_TLS", "")
	t.Setenv("REDIS_TLS_SERVER_NAME", "")
	t.Setenv("REDIS_TLS_INSECURE_SKIP_VERIFY", "")
}

func TestRedisTLSConfig_DisabledByDefault(t *testing.T) {
	resetRedisTLSEnv(t)

	if cfg := RedisTLSConfig(); cfg != nil {
		t.Fatalf("expected nil tls.Config when REDIS_USE_TLS is unset, got %#v", cfg)
	}

	t.Setenv("REDIS_USE_TLS", "false")
	if cfg := RedisTLSConfig(); cfg != nil {
		t.Fatalf("expected nil tls.Config when REDIS_USE_TLS=false, got %#v", cfg)
	}
}

func TestRedisTLSConfig_EnabledSecureByDefault(t *testing.T) {
	resetRedisTLSEnv(t)
	t.Setenv("REDIS_USE_TLS", "true")

	cfg := RedisTLSConfig()
	if cfg == nil {
		t.Fatal("expected non-nil tls.Config when REDIS_USE_TLS=true")
	}
	if cfg.MinVersion != tls.VersionTLS12 {
		t.Errorf("expected MinVersion TLS 1.2, got %x", cfg.MinVersion)
	}
	if cfg.InsecureSkipVerify {
		t.Error("expected certificate verification to be enabled by default")
	}
	if cfg.ServerName != "" {
		t.Errorf("expected empty ServerName by default, got %q", cfg.ServerName)
	}
}

func TestRedisTLSConfig_Options(t *testing.T) {
	resetRedisTLSEnv(t)
	t.Setenv("REDIS_USE_TLS", "TRUE") // case-insensitive
	t.Setenv("REDIS_TLS_SERVER_NAME", "redis.example.com")
	t.Setenv("REDIS_TLS_INSECURE_SKIP_VERIFY", "true")

	cfg := RedisTLSConfig()
	if cfg == nil {
		t.Fatal("expected non-nil tls.Config")
	}
	if !cfg.InsecureSkipVerify {
		t.Error("expected InsecureSkipVerify=true when opted in")
	}
	if cfg.ServerName != "redis.example.com" {
		t.Errorf("expected ServerName redis.example.com, got %q", cfg.ServerName)
	}
}
