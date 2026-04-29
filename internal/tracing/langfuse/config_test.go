package langfuse

import (
	"testing"
	"time"
)

func TestLoadConfigFromEnv_AutoEnablesWithCredentials(t *testing.T) {
	t.Setenv("LANGFUSE_PUBLIC_KEY", "pk-test")
	t.Setenv("LANGFUSE_SECRET_KEY", "sk-test")
	t.Setenv("LANGFUSE_HOST", "https://example.langfuse.com")

	cfg := LoadConfigFromEnv()

	if !cfg.Enabled {
		t.Fatalf("expected Enabled=true when both keys are set, got false")
	}
	if cfg.Host != "https://example.langfuse.com" {
		t.Errorf("unexpected host: %q", cfg.Host)
	}
	if cfg.SampleRate != 1.0 {
		t.Errorf("expected default SampleRate=1.0, got %v", cfg.SampleRate)
	}
}

func TestLoadConfigFromEnv_DisabledWithoutKeys(t *testing.T) {
	t.Setenv("LANGFUSE_PUBLIC_KEY", "")
	t.Setenv("LANGFUSE_SECRET_KEY", "")
	t.Setenv("LANGFUSE_ENABLED", "")

	cfg := LoadConfigFromEnv()
	if cfg.Enabled {
		t.Fatalf("expected Enabled=false when no keys set")
	}
}

func TestLoadConfigFromEnv_ExplicitDisableOverridesKeys(t *testing.T) {
	t.Setenv("LANGFUSE_PUBLIC_KEY", "pk")
	t.Setenv("LANGFUSE_SECRET_KEY", "sk")
	t.Setenv("LANGFUSE_ENABLED", "false")

	cfg := LoadConfigFromEnv()
	if cfg.Enabled {
		t.Fatalf("expected Enabled=false when LANGFUSE_ENABLED=false")
	}
}

func TestLoadConfigFromEnv_FlushIntervalAcceptsSecondsAndDuration(t *testing.T) {
	t.Setenv("LANGFUSE_PUBLIC_KEY", "pk")
	t.Setenv("LANGFUSE_SECRET_KEY", "sk")

	t.Setenv("LANGFUSE_FLUSH_INTERVAL", "500ms")
	cfg := LoadConfigFromEnv()
	if cfg.FlushInterval != 500*time.Millisecond {
		t.Errorf("expected 500ms, got %v", cfg.FlushInterval)
	}

	t.Setenv("LANGFUSE_FLUSH_INTERVAL", "7")
	cfg = LoadConfigFromEnv()
	if cfg.FlushInterval != 7*time.Second {
		t.Errorf("expected 7s (bare integer), got %v", cfg.FlushInterval)
	}
}

func TestConfigValidate(t *testing.T) {
	cases := []struct {
		name    string
		cfg     Config
		wantErr bool
	}{
		{"disabled is always valid", Config{Enabled: false}, false},
		{"enabled without host fails", Config{Enabled: true, PublicKey: "pk", SecretKey: "sk"}, true},
		{"enabled without keys fails", Config{Enabled: true, Host: "https://x"}, true},
		{"enabled with all fields passes", Config{Enabled: true, Host: "https://x", PublicKey: "pk", SecretKey: "sk"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.cfg.Validate()
			if (err != nil) != tc.wantErr {
				t.Fatalf("err=%v wantErr=%v", err, tc.wantErr)
			}
		})
	}
}
