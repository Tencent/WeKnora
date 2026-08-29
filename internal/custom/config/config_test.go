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

func TestLoadReadsDirectContentLLMConfig(t *testing.T) {
	t.Setenv("CUSTOM_LLM_PROVIDER", "openai-compatible")
	t.Setenv("CUSTOM_LLM_BASE_URL", "https://llm.example.test/v1")
	t.Setenv("CUSTOM_LLM_API_KEY", "test-key")
	t.Setenv("CUSTOM_LLM_MODEL", "model-1")
	t.Setenv("CUSTOM_LLM_PROMPT_VERSION", "prompt-v2")
	t.Setenv("CUSTOM_LLM_TIMEOUT_SECONDS", "90")
	t.Setenv("CUSTOM_LLM_MAX_TOKENS", "4096")

	cfg := Load()
	if cfg.LLM.Provider != "openai-compatible" || cfg.LLM.BaseURL != "https://llm.example.test/v1" || cfg.LLM.APIKey != "test-key" || cfg.LLM.Model != "model-1" || cfg.LLM.PromptVersion != "prompt-v2" || cfg.LLM.TimeoutSeconds != 90 || cfg.LLM.MaxTokens != 4096 {
		t.Fatalf("unexpected LLM config: %+v", cfg.LLM)
	}
}
