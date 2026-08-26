package config

import "testing"

func TestLoadReadsTongyiContentWorkerConfig(t *testing.T) {
	t.Setenv("TONGYI_API_KEY", "legacy-key")
	t.Setenv("TONGYI_ACCESS_KEY_ID", "access-key-id")
	t.Setenv("TONGYI_ACCESS_KEY_SECRET", "access-key-secret")
	t.Setenv("TONGYI_APP_KEY", "app-key")
	t.Setenv("TONGYI_ENDPOINT", "https://tingwu.example.test")

	cfg := Load()

	if cfg.Tongyi.APIKey != "legacy-key" {
		t.Fatalf("Tongyi.APIKey = %q, want legacy-key", cfg.Tongyi.APIKey)
	}
	if cfg.Tongyi.AccessKeyID != "access-key-id" {
		t.Fatalf("Tongyi.AccessKeyID = %q, want access-key-id", cfg.Tongyi.AccessKeyID)
	}
	if cfg.Tongyi.AccessKeySecret != "access-key-secret" {
		t.Fatalf("Tongyi.AccessKeySecret = %q, want access-key-secret", cfg.Tongyi.AccessKeySecret)
	}
	if cfg.Tongyi.AppKey != "app-key" {
		t.Fatalf("Tongyi.AppKey = %q, want app-key", cfg.Tongyi.AppKey)
	}
	if cfg.Tongyi.Endpoint != "https://tingwu.example.test" {
		t.Fatalf("Tongyi.Endpoint = %q, want test endpoint", cfg.Tongyi.Endpoint)
	}
}

func TestLoadUsesCanonicalTongyiEndpointByDefault(t *testing.T) {
	for _, key := range []string{
		"TONGYI_ENDPOINT",
		"TONGYI_API_KEY",
		"TONGYI_ACCESS_KEY_ID",
		"TONGYI_ACCESS_KEY_SECRET",
		"TONGYI_APP_KEY",
	} {
		t.Setenv(key, "")
	}

	cfg := Load()
	if cfg.Tongyi.Endpoint != "https://tingwu.cn-beijing.aliyuncs.com" {
		t.Fatalf("Tongyi.Endpoint = %q, want canonical default", cfg.Tongyi.Endpoint)
	}
}
