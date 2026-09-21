package vendors

import (
	"bytes"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/api"
	"github.com/Tencent/WeKnora/internal/models/catalog"
	"github.com/Tencent/WeKnora/internal/types"
)

// expectedIDs is every built-in vendor this package links.
var expectedIDs = []string{
	"generic", "weknoracloud",
	"aliyun", "zhipu", "volcengine", "hunyuan", "siliconflow", "deepseek",
	"minimax", "moonshot", "mimo", "modelscope", "qianfan", "qiniu", "longcat", "lkeap",
	"openai", "azure_openai", "anthropic", "gemini",
	"openrouter", "litellm", "requesty",
	"jina", "nvidia", "novita", "gpustack",
}

func TestAllVendorsRegistered(t *testing.T) {
	if len(expectedIDs) != 27 {
		t.Fatalf("expected 27 vendor ids in the spec, got %d", len(expectedIDs))
	}
	for _, id := range expectedIDs {
		v, ok := catalog.Get(id)
		if !ok {
			t.Errorf("vendor %q is not registered", id)
			continue
		}
		if v.ID != strings.ToLower(v.ID) {
			t.Errorf("vendor %q: id is not lowercase", id)
		}
		if v.Name == "" {
			t.Errorf("vendor %q: empty Name", id)
		}
		if len(v.Icon) == 0 || !bytes.HasPrefix(bytes.TrimSpace(v.Icon), []byte("<svg")) {
			t.Errorf("vendor %q: icon is empty or not an <svg> document", id)
		}
		if len(v.Icon) > 6*1024 {
			t.Errorf("vendor %q: icon is %d bytes, over the 6 KB budget", id, len(v.Icon))
		}
		if v.RequiresAuth && v.Auth == "" {
			t.Errorf("vendor %q: RequiresAuth without Auth style", id)
		}
		if v.Auth == catalog.AuthSigned && v.Signer == nil {
			t.Errorf("vendor %q: AuthSigned without Signer", id)
		}
		if !v.API.Known() {
			t.Errorf("vendor %q: unknown default API %q", id, v.API)
		}
		if len(v.ModelTypes) == 0 {
			t.Errorf("vendor %q: no ModelTypes", id)
		}
	}
	for _, v := range catalog.List() {
		found := false
		for _, id := range expectedIDs {
			if id == v.ID {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("unexpected vendor %q registered", v.ID)
		}
	}
}

func TestEveryCatalogEntryResolves(t *testing.T) {
	for _, id := range expectedIDs {
		v, ok := catalog.Get(id)
		if !ok {
			t.Fatalf("vendor %q missing", id)
		}
		for _, m := range v.Models {
			if m.ID == "" && m.Match == "" {
				t.Errorf("%s: model entry %q has neither id nor match", id, m.Name)
				continue
			}
			name := m.ID
			if name == "" {
				// Exercise pattern entries with a name that matches the glob.
				name = strings.ReplaceAll(m.Match, "*", "x")
			}
			modelType := m.Type
			if modelType == "" {
				modelType = types.ModelTypeKnowledgeQA
			}
			r, err := catalog.Resolve(catalog.Ref{Provider: v.ID, Model: name, ModelType: modelType})
			if err != nil {
				t.Errorf("%s/%s: resolve: %v", id, name, err)
				continue
			}
			if !r.Cataloged {
				t.Errorf("%s/%s: did not match its own catalog entry", id, name)
			}
			if modelType == types.ModelTypeKnowledgeQA && !r.API.Known() {
				t.Errorf("%s/%s: resolved to unknown API %q", id, name, r.API)
			}
			if modelType == types.ModelTypeEmbedding && m.ID != "" && m.Dimension <= 0 {
				t.Logf("%s/%s: embedding entry without dimension", id, name)
			}
		}
	}
}

func TestDetectByURLRoundTrip(t *testing.T) {
	for _, id := range expectedIDs {
		v, _ := catalog.Get(id)
		if len(v.URLPatterns) == 0 {
			continue
		}
		u := v.GetDefaultURL(types.ModelTypeKnowledgeQA)
		if !strings.HasPrefix(u, "https://") || strings.Contains(u, "{") {
			continue // placeholder or http-only self-hosted default
		}
		if got := catalog.DetectByURL(u); got != v.ID {
			t.Errorf("DetectByURL(%q) = %q, want %q", u, got, v.ID)
		}
	}
	if got := catalog.DetectByURL("http://your_litellm_proxy/v1"); got != "litellm" {
		t.Errorf("litellm placeholder detected as %q", got)
	}
	// GPUStack 2.x serves the OpenAI-compatible surface at /v1; the 0.x
	// /v1-openai path must keep resolving for rows saved before the move.
	if got := catalog.DetectByURL("http://your_gpustack_server_url/v1-openai"); got != "gpustack" {
		t.Errorf("gpustack placeholder detected as %q", got)
	}
	if got := catalog.DetectByURL("https://api.moonshot.cn/v1"); got != "moonshot" {
		t.Errorf("moonshot.cn detected as %q", got)
	}
}

// TestOpenAIProtocolSelection pins the first-party / relay split: Responses on
// api.openai.com, Chat Completions everywhere else, extra_config.api wins.
func TestOpenAIProtocolSelection(t *testing.T) {
	cases := []struct {
		baseURL string
		extra   map[string]string
		want    api.API
	}{
		{"", nil, api.APIOpenAIResponses},
		{"https://api.openai.com/v1", nil, api.APIOpenAIResponses},
		{"https://API.openai.com/v1/", nil, api.APIOpenAIResponses},
		{"https://my-relay.example.com/v1", nil, api.APIOpenAICompletions},
		{"https://api.openai.com/v1", map[string]string{"api": "openai-completions"}, api.APIOpenAICompletions},
	}
	for _, tc := range cases {
		r, err := catalog.Resolve(catalog.Ref{
			Provider: "openai", Model: "gpt-5.5", BaseURL: tc.baseURL, Extra: tc.extra,
		})
		if err != nil {
			t.Fatalf("resolve openai %q: %v", tc.baseURL, err)
		}
		if r.API != tc.want {
			t.Errorf("openai base %q extra %v: api = %q, want %q", tc.baseURL, tc.extra, r.API, tc.want)
		}
	}
}

func resolve(t *testing.T, provider, model string) *catalog.Resolved {
	t.Helper()
	r, err := catalog.Resolve(catalog.Ref{Provider: provider, Model: model})
	if err != nil {
		t.Fatalf("resolve %s/%s: %v", provider, model, err)
	}
	return r
}

func TestFamilyExpectations(t *testing.T) {
	if r := resolve(t, "openai", "gpt-5.2"); r.OpenAICompletions.SupportsTemperature {
		t.Error("openai/gpt-5.2 should not support temperature")
	}
	if r := resolve(t, "openai", "gpt-5-turbo-future"); r.OpenAICompletions.SupportsTemperature || !r.Spec.Reasoning {
		t.Error("openai gpt-5* family should be reasoning without temperature")
	}
	if r := resolve(t, "openai", "o4-mini"); r.ThinkingLevels.Supports(api.ReasoningOff) {
		t.Error("openai/o4-mini should not allow thinking off")
	}
	if r := resolve(t, "azure_openai", "gpt-5.4-prod"); r.OpenAICompletions.SupportsTemperature {
		t.Error("azure gpt-5* deployment should not support temperature")
	}
	// A deployment named exactly gpt-5 / gpt-5-mini / gpt-5-nano must agree
	// with the openai vendor: the original GPT-5 trio rejects
	// reasoning_effort "none", so the picker must not offer thinking off.
	// Only GPT-5.1 and later, which the gpt-5* pattern covers, accept it.
	for _, deployment := range []string{"gpt-5", "gpt-5-mini", "gpt-5-nano"} {
		azureSpec := resolve(t, "azure_openai", deployment)
		if azureSpec.ThinkingLevels.Supports(api.ReasoningOff) {
			t.Errorf("azure/%s should not allow thinking off", deployment)
		}
		openaiSpec := resolve(t, "openai", deployment)
		if openaiSpec.ThinkingLevels.Supports(api.ReasoningOff) {
			t.Errorf("openai/%s should not allow thinking off", deployment)
		}
	}
	if r := resolve(t, "azure_openai", "gpt-5.1-prod"); !r.ThinkingLevels.Supports(api.ReasoningOff) {
		t.Error("azure gpt-5* family (GPT-5.1 and later) should allow thinking off")
	}
	if r := resolve(t, "aliyun", "qwen3-max"); !r.OpenAICompletions.ThinkingAlwaysSend {
		t.Error("aliyun/qwen3-max should always send the thinking switch")
	}
	if r := resolve(t, "aliyun", "qwen-plus-2026-01-01"); !r.OpenAICompletions.ThinkingAlwaysSend ||
		!r.OpenAICompletions.ThinkingDisableOnNonStream {
		t.Error("aliyun qwen-plus* family should carry the hybrid thinking flags")
	}
	if r := resolve(t, "aliyun", "qwq-plus"); r.ThinkingLevels.Supports(api.ReasoningOff) {
		t.Error("aliyun/qwq-plus should not allow thinking off")
	}
	if r := resolve(t, "moonshot", "moonshot-v1-8k"); r.OpenAICompletions.FixedTemperature == nil ||
		*r.OpenAICompletions.FixedTemperature != 1 {
		t.Error("moonshot/moonshot-v1-8k should pin temperature to 1")
	}
	if r := resolve(t, "moonshot", "kimi-k2.6"); r.OpenAICompletions.SupportsTemperature {
		t.Error("moonshot/kimi-k2.6 should not support temperature")
	}
	if r := resolve(t, "lkeap", "deepseek-r1"); r.ThinkingLevels.Supports(api.ReasoningOff) {
		t.Error("lkeap/deepseek-r1 should not allow thinking off")
	}
	if r := resolve(t, "lkeap", "my-deepseek-v3-route"); !r.Spec.Reasoning ||
		!r.ThinkingLevels.Supports(api.ReasoningOff) {
		t.Error("lkeap *deepseek-v3* family should be switchable reasoning")
	}
	if r := resolve(t, "zhipu", "glm-5.2"); r.ThinkingLevels.Value(api.ReasoningMedium) != "high" {
		t.Errorf("zhipu/glm-5.2 medium should map to high, got %q", r.ThinkingLevels.Value(api.ReasoningMedium))
	}
	if r := resolve(t, "zhipu", "glm-4.7"); r.ThinkingLevels.Value(api.ReasoningOff) != "none" {
		t.Error("zhipu vendor map should spell off as none")
	}
	if r := resolve(t, "gemini", "gemini-2.5-flash"); r.API != api.APIGoogleGenerativeAI {
		t.Errorf("gemini/gemini-2.5-flash API = %q", r.API)
	}
	if r := resolve(t, "gemini", "gemini-3.8-flash"); r.GoogleGenerativeAI.ThinkingMode != catalog.GoogleThinkingLevel {
		t.Errorf("gemini/gemini-3.8-flash thinking mode = %q", r.GoogleGenerativeAI.ThinkingMode)
	}
	if r := resolve(t, "anthropic", "claude-opus-5"); r.AnthropicMessages.ThinkingMode !=
		catalog.AnthropicThinkingAdaptive || !r.AnthropicMessages.SupportsEffort {
		t.Error("anthropic/claude-opus-5 should use adaptive thinking with effort")
	}
	if r := resolve(t, "anthropic", "claude-sonnet-4-5"); r.AnthropicMessages.ThinkingMode !=
		catalog.AnthropicThinkingBudget {
		t.Error("anthropic/claude-sonnet-4-5 should keep budget thinking")
	}
	if r := resolve(t, "anthropic", "claude-sonnet-4-5-20250929"); !r.Cataloged {
		t.Error("anthropic dated alias should resolve to the catalog entry")
	}
	if r := resolve(t, "openrouter", "anthropic/claude-haiku-4.5"); r.OpenAICompletions.CacheControlFormat !=
		"anthropic" {
		t.Error("openrouter anthropic/* family should use anthropic cache_control")
	}
	if r := resolve(t, "siliconflow", "deepseek-ai/DeepSeek-V4-Pro"); !r.OpenAICompletions.SupportsReasoningEffort ||
		r.ThinkingLevels.Value(api.ReasoningMax) != "max" {
		t.Error("siliconflow DeepSeek-V4-Pro should grade effort high/max")
	}
	if r := resolve(t, "siliconflow", "Pro/zai-org/GLM-5"); r.OpenAICompletions.SupportsReasoningEffort {
		t.Error("siliconflow GLM-5 should not send reasoning_effort")
	}
	if r := resolve(t, "minimax", "MiniMax-M3"); r.OpenAICompletions.ThinkingEnabledValue != "adaptive" {
		t.Error("minimax should enable thinking with type adaptive")
	}
	if r := resolve(t, "volcengine", "doubao-seed-1-6-251015"); r.OpenAICompletions.MaxTokensField !=
		"max_completion_tokens" {
		t.Error("volcengine must keep max_completion_tokens")
	}
	if r := resolve(t, "deepseek", "deepseek-reasoner"); r.ThinkingLevels.Supports(api.ReasoningOff) {
		t.Error("deepseek/deepseek-reasoner should not allow thinking off")
	}
	// required / named tool choices 400 in thinking mode, which is the
	// DeepSeek default, so neither may reach the wire.
	if r := resolve(t, "deepseek", "deepseek-v4-pro"); r.OpenAICompletions.AllowsToolChoice("required") ||
		r.OpenAICompletions.AllowsToolChoice("function") {
		t.Error("deepseek should not send required or named tool choices")
	}
	// GLM-5.2 reads "minimal" as give-up-thinking and folds "low" to high, so
	// rewriting minimal to low here would send the weakest rung as the
	// strongest one.
	if r := resolve(t, "zhipu", "glm-5.2"); r.ThinkingLevels.Value(api.ReasoningMinimal) != "minimal" {
		t.Errorf("zhipu/glm-5.2 minimal should stay minimal, got %q", r.ThinkingLevels.Value(api.ReasoningMinimal))
	}
	// Both entries grade with a top-level reasoning_effort; the vendor
	// default (chat_template_kwargs) would silently drop the level.
	for _, model := range []string{"z-ai/glm-5.3", "moonshotai/kimi-k3"} {
		r := resolve(t, "nvidia", model)
		if r.OpenAICompletions.ThinkingFormat != catalog.ThinkingFormatOpenAI ||
			!r.OpenAICompletions.SupportsReasoningEffort {
			t.Errorf("nvidia/%s should grade with a top-level reasoning_effort", model)
		}
	}
	if r := resolve(t, "nvidia", "nvidia/nemotron-3-ultra-550b-a55b"); r.OpenAICompletions.ThinkingFormat !=
		catalog.ThinkingFormatChatTemplateKwargs {
		t.Error("nvidia nemotron should keep the chat-template switch")
	}
	// DashScope errors when qwen3.8-max gets both fields.
	for _, model := range []string{"qwen3.8-max", "qwen3.8-flash", "qwen3.8-plus-2026-09-01"} {
		if r := resolve(t, "aliyun", model); !r.OpenAICompletions.ThinkingBudgetExcludesEffort {
			t.Errorf("aliyun/%s should not send thinking_budget next to reasoning_effort", model)
		}
	}
	if r := resolve(t, "aliyun", "qwen3-max"); r.OpenAICompletions.ThinkingBudgetExcludesEffort {
		t.Error("aliyun/qwen3-max has no effort to conflict with and should keep the budget")
	}
}

func TestHooks(t *testing.T) {
	azure, _ := catalog.Get("azure_openai")
	u, q := azure.Endpoint(catalog.EndpointRequest{
		BaseURL:   "https://my-res.openai.azure.com/",
		Model:     "gpt-4o deploy",
		ModelType: types.ModelTypeKnowledgeQA,
		Extra:     map[string]string{catalog.ExtraAPIVersion: "2025-01-01-preview"},
	})
	if u != "https://my-res.openai.azure.com/openai/deployments/gpt-4o%20deploy/chat/completions" {
		t.Errorf("azure endpoint = %q", u)
	}
	if q["api-version"] != "2025-01-01-preview" {
		t.Errorf("azure api-version = %q", q["api-version"])
	}
	// Without an explicit api_version the hook emits the v1 GA data plane,
	// which carries the deployment name in the body and takes no
	// api-version at all.
	u, q = azure.Endpoint(catalog.EndpointRequest{BaseURL: "https://x.openai.azure.com", Model: "d"})
	if u != "https://x.openai.azure.com/openai/v1/chat/completions" {
		t.Errorf("azure v1 endpoint = %q", u)
	}
	if len(q) != 0 {
		t.Errorf("azure v1 query = %v, want none", q)
	}
	if azure.Auth != catalog.AuthAPIKeyHeader {
		t.Errorf("azure auth = %q", azure.Auth)
	}
	// Embedding obeys the same rule as chat for the same stored row, so one
	// Azure resource never ends up serving chat on v1 and embedding on the
	// dated deployments path. internal/models/embedding calls this hook.
	u, q = azure.Endpoint(catalog.EndpointRequest{
		BaseURL: "https://x.openai.azure.com", Model: "embed-deploy",
		ModelType: types.ModelTypeEmbedding,
	})
	if u != "https://x.openai.azure.com/openai/v1/embeddings" {
		t.Errorf("azure v1 embedding endpoint = %q", u)
	}
	if len(q) != 0 {
		t.Errorf("azure v1 embedding query = %v, want none", q)
	}
	u, q = azure.Endpoint(catalog.EndpointRequest{
		BaseURL: "https://x.openai.azure.com", Model: "embed-deploy",
		ModelType: types.ModelTypeEmbedding,
		Extra:     map[string]string{catalog.ExtraAPIVersion: "2024-10-21"},
	})
	if u != "https://x.openai.azure.com/openai/deployments/embed-deploy/embeddings" {
		t.Errorf("azure legacy embedding endpoint = %q", u)
	}
	if q["api-version"] != "2024-10-21" {
		t.Errorf("azure legacy embedding api-version = %q", q["api-version"])
	}

	wk, _ := catalog.Get("weknoracloud")
	u, _ = wk.Endpoint(catalog.EndpointRequest{
		BaseURL: "https://weknora.weixin.qq.com/", ModelType: types.ModelTypeKnowledgeQA,
	})
	if u != "https://weknora.weixin.qq.com/api/v1/chat/completions" {
		t.Errorf("weknoracloud endpoint = %q", u)
	}
	if wk.Auth != catalog.AuthSigned || wk.Signer == nil {
		t.Error("weknoracloud should use a signer")
	}
	if err := wk.ValidateConfig(&catalog.Config{}); err != nil {
		t.Errorf("weknoracloud validate should pass without key: %v", err)
	}

	generic, _ := catalog.Get("generic")
	if err := generic.ValidateConfig(&catalog.Config{ModelName: "m"}); err == nil {
		t.Error("generic validate should require a base URL")
	}
	if err := generic.ValidateConfig(&catalog.Config{BaseURL: "http://x/v1", ModelName: "m"}); err != nil {
		t.Errorf("generic validate: %v", err)
	}
	gpustack, _ := catalog.Get("gpustack")
	if err := gpustack.ValidateConfig(&catalog.Config{APIKey: "k", ModelName: "m"}); err == nil {
		t.Error("gpustack validate should require a base URL")
	}

	for _, id := range []string{"volcengine", "lkeap"} {
		v, _ := catalog.Get(id)
		found := false
		for _, f := range v.ExtraFields {
			if f.Key == "secret_key" {
				found = true
				if !f.Secret || len(f.ModelTypes) != 1 || f.ModelTypes[0] != types.ModelTypeRerank {
					t.Errorf("%s secret_key field should be secret and rerank-only", id)
				}
			}
		}
		if !found {
			t.Errorf("%s should expose a secret_key extra field", id)
		}
	}
}

// TestSignedRerankCredentialLabels pins that the two signed rerank vendors
// name their first credential after the identity it actually is. The editor
// renders this instead of a hardcoded vendor table, so losing it silently
// sends operators back to pasting an sk- key into a signature field.
func TestSignedRerankCredentialLabels(t *testing.T) {
	for _, tc := range []struct{ vendor, wantLabel string }{
		{"lkeap", "SecretId"},
		{"volcengine", "Access Key ID"},
	} {
		t.Run(tc.vendor, func(t *testing.T) {
			v, ok := catalog.Get(tc.vendor)
			if !ok {
				t.Fatalf("vendor %s is not registered", tc.vendor)
			}
			label := v.CredentialLabelFor(types.ModelTypeRerank)
			if label == nil {
				t.Fatalf("rerank should override the credential label")
			}
			if label.Label != tc.wantLabel {
				t.Errorf("rerank credential label = %q, want %q", label.Label, tc.wantLabel)
			}
			if label.LocalizedLabel("zh-CN") == label.Label {
				t.Errorf("zh-CN label should differ from the default")
			}
			if label.Hint == "" {
				t.Errorf("the hint is what stops an sk- key being pasted here")
			}
			// Chat and embedding on these vendors do take a plain API key.
			if other := v.CredentialLabelFor(types.ModelTypeKnowledgeQA); other != nil {
				t.Errorf("chat should keep the generic API-key wording, got %q", other.Label)
			}
		})
	}
}
