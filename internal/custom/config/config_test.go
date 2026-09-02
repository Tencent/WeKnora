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

func TestLoadKeepsProductGraphConfigurationIndependentFromOfficialGraph(t *testing.T) {
	t.Setenv("NEO4J_ENABLE", "true")
	t.Setenv("NEO4J_URI", "bolt://official-graph:7687")
	t.Setenv("NEO4J_USERNAME", "official")
	t.Setenv("NEO4J_PASSWORD", "official-password")
	t.Setenv("WEKNORA_KB_ID", "weknora-kb")
	t.Setenv("WEKNORA_KNOWLEDGE_KB_ID", "knowledge-kb")
	t.Setenv("CUSTOM_WIKI_GRAPH_NEO4J_ENABLE", "")
	t.Setenv("CUSTOM_WIKI_GRAPH_NEO4J_URI", "")
	t.Setenv("CUSTOM_WIKI_GRAPH_KB_ID", "")

	cfg := Load()
	if cfg.WikiGraph.Enabled {
		t.Fatal("product Wiki graph must not inherit NEO4J_ENABLE")
	}
	if cfg.WikiGraph.URI != "" || cfg.WikiGraph.Username != "" || cfg.WikiGraph.KnowledgeBaseID != "knowledge-kb" {
		t.Fatalf("product Wiki graph inherited official graph settings: %+v", cfg.WikiGraph)
	}
}

func TestResolveKnowledgeBaseRolesUsesLegacyOnlyForEvidence(t *testing.T) {
	roles, err := (WeKnoraConfig{KBID: "legacy-evidence", KnowledgeKBID: "knowledge"}).ResolveKnowledgeBaseRoles()
	if err != nil {
		t.Fatal(err)
	}
	if roles.Evidence != "legacy-evidence" || roles.Knowledge != "knowledge" {
		t.Fatalf("unexpected roles: %+v", roles)
	}
}

func TestResolveKnowledgeBaseRolesFailsClosed(t *testing.T) {
	tests := []struct {
		name string
		cfg  WeKnoraConfig
		code string
	}{
		{name: "missing evidence", cfg: WeKnoraConfig{KnowledgeKBID: "knowledge"}, code: KBRouteEvidenceMissing},
		{name: "missing knowledge", cfg: WeKnoraConfig{EvidenceKBID: "evidence", KBID: "legacy"}, code: KBRouteKnowledgeMissing},
		{name: "same role", cfg: WeKnoraConfig{EvidenceKBID: "same", KnowledgeKBID: "same"}, code: KBRouteRoleConflict},
		{name: "legacy cannot backfill knowledge", cfg: WeKnoraConfig{KBID: "legacy"}, code: KBRouteKnowledgeMissing},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tt.cfg.ResolveKnowledgeBaseRoles()
			if err == nil || err.Error() != tt.code {
				t.Fatalf("error = %v, want %s", err, tt.code)
			}
		})
	}
}

func TestForKnowledgeBaseReturnsIndependentRoleCopies(t *testing.T) {
	base := WeKnoraConfig{KBID: "legacy", EvidenceKBID: "evidence", KnowledgeKBID: "knowledge"}
	evidence := base.ForKnowledgeBase("evidence")
	knowledge := base.ForKnowledgeBase("knowledge")
	if base.KBID != "legacy" || evidence.KBID != "evidence" || knowledge.KBID != "knowledge" {
		t.Fatalf("role copy mutated shared configuration: base=%+v evidence=%+v knowledge=%+v", base, evidence, knowledge)
	}
}

func TestLoadReadsTencentMPSProviderConfig(t *testing.T) {
	t.Setenv("CUSTOM_TRANSCRIPTION_PROVIDER", "tencent_mps")
	t.Setenv("TENCENTCLOUD_SECRET_ID", "secret-id")
	t.Setenv("TENCENTCLOUD_SECRET_KEY", "secret-key")
	t.Setenv("TENCENTCLOUD_REGION", "ap-shanghai")
	t.Setenv("TENCENTCLOUD_MPS_OUTPUT_BUCKET", "subtitle-123456")

	cfg := Load()
	if cfg.TranscriptionProvider != "tencent_mps" || cfg.MPS.Region != "ap-shanghai" || cfg.MPS.OutputBucket != "subtitle-123456" || cfg.MPS.TemplateID != 307 {
		t.Fatalf("unexpected MPS config: provider=%s mps=%+v", cfg.TranscriptionProvider, cfg.MPS)
	}
}

func TestNormalizeTranscriptionProviderCompatibility(t *testing.T) {
	for input, want := range map[string]string{"": "aliyun_tingwu", "tingwu": "aliyun_tingwu", "aliyun_tingwu": "aliyun_tingwu", "mps": "tencent_mps", "tencent_mps": "tencent_mps"} {
		got, err := NormalizeTranscriptionProvider(input)
		if err != nil || got != want {
			t.Fatalf("NormalizeTranscriptionProvider(%q)=(%q,%v), want %q", input, got, err, want)
		}
	}
	if _, err := NormalizeTranscriptionProvider("unknown"); err == nil {
		t.Fatal("invalid provider must be rejected")
	}
}
