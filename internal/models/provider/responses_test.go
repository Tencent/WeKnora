package provider

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

// TDD red: ProviderResponses does not exist yet.
func TestResponsesProviderRegistered(t *testing.T) {
	p, ok := Get(ProviderResponses)
	if !ok {
		t.Fatalf("ProviderResponses not registered")
	}
	info := p.Info()
	if info.Name != ProviderResponses {
		t.Errorf("name = %q, want %q", info.Name, ProviderResponses)
	}
	hasChat, hasVLLM := false, false
	for _, mt := range info.ModelTypes {
		if mt == types.ModelTypeKnowledgeQA {
			hasChat = true
		}
		if mt == types.ModelTypeVLLM {
			hasVLLM = true
		}
	}
	if !hasChat || !hasVLLM {
		t.Errorf("ModelTypes = %v, want KnowledgeQA + VLLM", info.ModelTypes)
	}
	if len(info.DefaultURLs) != 0 {
		t.Errorf("DefaultURLs = %v, want empty (user-supplied base URL)", info.DefaultURLs)
	}
}

func TestResponsesValidateConfig(t *testing.T) {
	p, ok := Get(ProviderResponses)
	if !ok {
		t.Skip("provider not registered yet")
	}
	if err := p.ValidateConfig(&Config{BaseURL: "", ModelName: "m"}); err == nil {
		t.Error("empty BaseURL should fail")
	}
	if err := p.ValidateConfig(&Config{BaseURL: "https://x/v1", ModelName: ""}); err == nil {
		t.Error("empty ModelName should fail")
	}
	if err := p.ValidateConfig(&Config{BaseURL: "https://x/v1", ModelName: "m"}); err != nil {
		t.Errorf("valid config should pass: %v", err)
	}
}

func TestNormalizeBaseURL(t *testing.T) {
	cases := []struct {
		provider ProviderName
		in, want string
	}{
		{ProviderResponses, "https://opencode.ai/zen/go/v1", "https://opencode.ai/zen/go/v1"},
		{ProviderResponses, "https://opencode.ai/zen/go/v1/responses", "https://opencode.ai/zen/go/v1"},
		{ProviderResponses, "https://opencode.ai/zen/go/v1/responses/", "https://opencode.ai/zen/go/v1"},
		{ProviderResponses, "https://h/v1/chat/completions", "https://h/v1"},
		{ProviderResponses, "https://h/api/v1/chat/completions", "https://h"},
		// Non-responses providers must pass through untouched (Azure/proxies keep suffixes).
		{ProviderGeneric, "https://h/v1/chat/completions", "https://h/v1/chat/completions"},
		{ProviderOpenAI, "https://api.openai.com/v1/", "https://api.openai.com/v1"},
		{ProviderGeneric, "https://h/v1/", "https://h/v1"},
	}
	for _, tc := range cases {
		if got := NormalizeBaseURL(tc.provider, tc.in); got != tc.want {
			t.Errorf("NormalizeBaseURL(%q,%q) = %q, want %q", tc.provider, tc.in, got, tc.want)
		}
	}
}
