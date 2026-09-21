// Package parity holds the cross-cutting safety net for the vendor catalog:
//
//   - TestLegacyWireParity pins the outbound request shape against the
//     behaviour of the pre-catalog implementation (the providerAdapter /
//     ThinkingStrategy / wireCompletionTokenField tables that lived in
//     internal/models/chat before the refactor). Every deliberate divergence
//     is listed explicitly with the reason, so an accidental one fails here.
//   - TestLegacyProviderDetection pins DetectProvider's URL table.
//   - TestCatalogInvariants and TestEveryChatModelRequestShape are
//     data-driven: they walk every registered vendor and every catalog model,
//     so a newly added vendor or model is covered without touching this file.
//
// The tests drive the real stack: chat.NewRemoteChat resolves the catalog and
// returns the protocol client, and the body asserted here is exactly what
// would go on the wire.
package parity

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/api"
	"github.com/Tencent/WeKnora/internal/models/catalog"
	"github.com/Tencent/WeKnora/internal/models/chat"
	_ "github.com/Tencent/WeKnora/internal/models/vendors"
	"github.com/Tencent/WeKnora/internal/types"
	secutils "github.com/Tencent/WeKnora/internal/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMain whitelists the loopback host: several vendors ship a placeholder
// default URL (self-hosted runtimes) or a templated one (Azure), which the
// SSRF guard rejects before any request is built. Those cases are re-pointed
// at 127.0.0.1 by reachableBaseURL.
func TestMain(m *testing.M) {
	secutils.SetSSRFWhitelistFromRaw("127.0.0.1")
	defer secutils.ResetSSRFWhitelistForTest()
	os.Exit(m.Run())
}

// reachableBaseURL replaces a placeholder or templated vendor URL with a
// loopback one so the SSRF guard lets the request through. The protocol and
// compat resolution under test do not depend on the host.
func reachableBaseURL(url string) string {
	if url == "" {
		return ""
	}
	if strings.Contains(url, "your_") || strings.Contains(url, "{") {
		return "http://127.0.0.1:9/v1"
	}
	return url
}

// bodyBuilder is implemented by every protocol client; it returns the exact
// JSON object the client would send.
type bodyBuilder interface {
	BuildRequestBody(messages []api.Message, opts *api.Options, stream bool) (map[string]any, error)
}

func ptrBool(b bool) *bool { return &b }

var userTurn = []api.Message{{Role: "user", Content: "hi"}}

// buildBody runs the production path: ChatConfig -> catalog.Resolve ->
// protocol client -> request body, then round-trips through JSON so the
// assertions see the real wire types.
func buildBody(t *testing.T, cfg *chat.ChatConfig, opts *api.Options, stream bool) map[string]any {
	t.Helper()
	if cfg.Source == "" {
		cfg.Source = types.ModelSourceRemote
	}
	if cfg.APIKey == "" {
		cfg.APIKey = "k"
	}
	cfg.BaseURL = reachableBaseURL(cfg.BaseURL)
	client, err := chat.NewRemoteChat(cfg)
	require.NoError(t, err, "NewRemoteChat(%s/%s)", cfg.Provider, cfg.ModelName)
	builder, ok := client.(bodyBuilder)
	require.True(t, ok, "%s/%s: protocol client does not expose BuildRequestBody", cfg.Provider, cfg.ModelName)
	body, err := builder.BuildRequestBody(userTurn, opts, stream)
	require.NoError(t, err)
	raw, err := json.Marshal(body)
	require.NoError(t, err)
	var out map[string]any
	require.NoError(t, json.Unmarshal(raw, &out))
	return out
}

func resolve(t *testing.T, provider, model string) *catalog.Resolved {
	t.Helper()
	r, err := catalog.Resolve(catalog.Ref{Provider: provider, Model: model})
	require.NoError(t, err)
	return r
}

// TestLegacyWireParity compares today's wire body with the pre-refactor
// behaviour, case by case.
//
// `want` lists fields that must match the old implementation; a nil value
// means the field must be absent, exactly as before. `divergence` documents
// a deliberate behaviour change — those entries assert the NEW behaviour and
// carry the reason, so the change stays visible in review.
func TestLegacyWireParity(t *testing.T) {
	cases := []struct {
		name       string
		provider   string
		model      string
		baseURL    string
		extra      map[string]string
		opts       *api.Options
		stream     bool
		want       map[string]any
		divergence string
	}{
		// ---- completion budget field (old wireCompletionTokenField table) ----
		{
			name: "deepseek keeps max_tokens", provider: "deepseek", model: "deepseek-v4-pro",
			opts: &api.Options{MaxTokens: 100},
			want: map[string]any{"max_tokens": float64(100), "max_completion_tokens": nil},
		},
		{
			name: "zhipu keeps max_tokens", provider: "zhipu", model: "glm-4.7",
			opts: &api.Options{MaxTokens: 100},
			want: map[string]any{"max_tokens": float64(100), "max_completion_tokens": nil},
		},
		{
			name: "siliconflow keeps max_tokens", provider: "siliconflow", model: "deepseek-ai/DeepSeek-V4-Pro",
			opts: &api.Options{MaxTokens: 100},
			want: map[string]any{"max_tokens": float64(100), "max_completion_tokens": nil},
		},
		{
			name: "moonshot moved to max_completion_tokens", provider: "moonshot", model: "kimi-k2.6",
			opts: &api.Options{MaxTokens: 100},
			want: map[string]any{"max_completion_tokens": float64(100), "max_tokens": nil},
			divergence: "the old table sent max_tokens. platform.kimi.com now documents " +
				"max_completion_tokens as the field and max_tokens as deprecated, so the vendor " +
				"audit switched it; both are still accepted upstream.",
		},
		// The six vendors below moved the other way: the old table left them
		// on the default max_completion_tokens and the audit put them on
		// max_tokens. Each entry carries the documentation that decided it, so
		// a later flip back is a visible change rather than a silent one.
		{
			name: "hunyuan moved to max_tokens", provider: "hunyuan", model: "hunyuan-turbos-latest",
			opts: &api.Options{MaxTokens: 100},
			want: map[string]any{"max_tokens": float64(100), "max_completion_tokens": nil},
			divergence: "the OpenAI-compatible reference documents only max_tokens (default 4096); " +
				"max_completion_tokens appears nowhere in it.",
		},
		{
			name: "modelscope moved to max_tokens", provider: "modelscope", model: "ZhipuAI/GLM-4.6",
			opts: &api.Options{MaxTokens: 100},
			want: map[string]any{"max_tokens": float64(100), "max_completion_tokens": nil},
			divergence: "ModelScope API-Inference documents no output-cap parameter at all. " +
				"max_tokens is carried over from the DashScope/Bailian channel behind it and is " +
				"marked unverified in the vendor package, so this case pins a guess, not a fact.",
		},
		{
			name: "qiniu moved to max_tokens", provider: "qiniu", model: "qwen3.5-397b-a17b",
			opts:       &api.Options{MaxTokens: 100},
			want:       map[string]any{"max_tokens": float64(100), "max_completion_tokens": nil},
			divergence: "max_completion_tokens is not in the Qiniu reference.",
		},
		{
			name: "longcat moved to max_tokens", provider: "longcat", model: "LongCat-2.0",
			opts: &api.Options{MaxTokens: 100},
			want: map[string]any{"max_tokens": float64(100), "max_completion_tokens": nil},
			divergence: "max_completion_tokens is not part of the LongCat reference; max_tokens " +
				"defaults to and tops out at the model's 131072 output budget.",
		},
		{
			name: "requesty moved to max_tokens", provider: "requesty", model: "anthropic/claude-opus-5",
			opts:       &api.Options{MaxTokens: 100},
			want:       map[string]any{"max_tokens": float64(100), "max_completion_tokens": nil},
			divergence: "max_completion_tokens appears nowhere in Requesty's documentation.",
		},
		{
			name: "novita moved to max_tokens", provider: "novita", model: "moonshotai/kimi-k3",
			opts: &api.Options{MaxTokens: 100},
			want: map[string]any{"max_tokens": float64(100), "max_completion_tokens": nil},
			divergence: "Novita documents max_tokens as required and never mentions " +
				"max_completion_tokens.",
		},
		// NVIDIA is the one vendor whose output-cap field is per model, so the
		// vendor-level case above ("nvidia keeps max_tokens", an uncatalogued
		// deployment) does not cover these two.
		{
			name: "nvidia kimi-k3 overrides the vendor default", provider: "nvidia", model: "moonshotai/kimi-k3",
			opts: &api.Options{MaxTokens: 100},
			want: map[string]any{"max_completion_tokens": float64(100), "max_tokens": nil},
			divergence: "NVIDIA's own curl and Python samples use max_tokens, which is why that is " +
				"the vendor default, but the build.nvidia.com cards for kimi-k3 and " +
				"gemma-4-31b-it show max_completion_tokens; those two entries override it.",
		},
		{
			name:     "nvidia gemma-4 overrides the vendor default",
			provider: "nvidia", model: "google/gemma-4-31b-it",
			opts:       &api.Options{MaxTokens: 100},
			want:       map[string]any{"max_completion_tokens": float64(100), "max_tokens": nil},
			divergence: "see the kimi-k3 case above; the model card shows max_completion_tokens.",
		},
		{
			name: "nvidia keeps max_tokens", provider: "nvidia", model: "some-nim-model",
			opts: &api.Options{MaxTokens: 100},
			want: map[string]any{"max_tokens": float64(100), "max_completion_tokens": nil},
		},
		{
			name: "generic keeps max_tokens", provider: "generic", model: "Qwen/Qwen3-32B",
			opts: &api.Options{MaxTokens: 100},
			want: map[string]any{"max_tokens": float64(100), "max_completion_tokens": nil},
		},
		{
			name: "gpustack keeps max_tokens", provider: "gpustack", model: "qwen3",
			baseURL: "http://127.0.0.1:9/v1-openai",
			opts:    &api.Options{MaxTokens: 100},
			want:    map[string]any{"max_tokens": float64(100), "max_completion_tokens": nil},
		},
		{
			name: "lkeap keeps max_tokens", provider: "lkeap", model: "deepseek-r1-0528",
			opts: &api.Options{MaxTokens: 100},
			want: map[string]any{"max_tokens": float64(100), "max_completion_tokens": nil},
		},
		{
			name: "volcengine keeps max_completion_tokens", provider: "volcengine", model: "doubao-seed-1-6-251015",
			opts: &api.Options{MaxTokens: 100},
			want: map[string]any{"max_completion_tokens": float64(100), "max_tokens": nil},
		},
		{
			// Not a divergence: the pre-catalog table left DashScope on the
			// default too. The audit briefly moved it to max_tokens because
			// the compatible mode has always accepted that field; it was
			// moved back because DashScope's parameter table marks
			// max_tokens 即将废弃 and names max_completion_tokens its
			// successor, and following a vendor that has announced a
			// replacement is the cheaper side of the bet.
			name: "aliyun keeps max_completion_tokens", provider: "aliyun", model: "qwen3-max",
			opts: &api.Options{MaxTokens: 100},
			want: map[string]any{"max_completion_tokens": float64(100), "max_tokens": nil},
		},
		{
			name: "litellm keeps max_completion_tokens", provider: "litellm", model: "gpt-4o",
			baseURL: "http://127.0.0.1:9/v1",
			opts:    &api.Options{MaxTokens: 100},
			want:    map[string]any{"max_completion_tokens": float64(100), "max_tokens": nil},
		},
		{
			name:     "openai reasoning model keeps max_completion_tokens and drops sampling",
			provider: "openai", model: "gpt-5.5",
			extra: map[string]string{catalog.ExtraAPI: "openai-completions"},
			opts:  &api.Options{MaxTokens: 100, Temperature: 0.7, TopP: 0.9},
			want: map[string]any{
				"max_completion_tokens": float64(100), "max_tokens": nil,
				"temperature": nil, "top_p": nil,
			},
		},
		{
			name: "azure reasoning deployment drops sampling", provider: "azure_openai", model: "gpt-5-prod",
			baseURL: "http://127.0.0.1:9",
			opts:    &api.Options{MaxTokens: 100, Temperature: 0.7},
			want:    map[string]any{"max_completion_tokens": float64(100), "temperature": nil},
		},

		// ---- thinking encodings (old ThinkingStrategy table) ----
		{
			name:     "aliyun qwen thinking model always pins enable_thinking, off in non-stream",
			provider: "aliyun", model: "qwen3-max",
			opts:   &api.Options{Thinking: ptrBool(true)},
			stream: false,
			want:   map[string]any{"enable_thinking": false},
		},
		{
			name:     "aliyun qwen thinking model streams with enable_thinking true",
			provider: "aliyun", model: "qwen3-max",
			opts:   &api.Options{Thinking: ptrBool(true)},
			stream: true,
			want:   map[string]any{"enable_thinking": true},
		},
		{
			name:     "aliyun sends the switch even without a caller preference",
			provider: "aliyun", model: "qwen3-max",
			opts: &api.Options{Temperature: 0.2},
			want: map[string]any{"enable_thinking": false},
		},
		{
			name: "volcengine uses thinking.type", provider: "volcengine", model: "doubao-seed-1-6-251015",
			opts: &api.Options{Thinking: ptrBool(true)},
			want: map[string]any{"thinking": map[string]any{"type": "enabled"}},
		},
		{
			name: "lkeap deepseek v3 uses thinking.type", provider: "lkeap", model: "deepseek-v3.2",
			opts: &api.Options{Thinking: ptrBool(true)},
			want: map[string]any{"thinking": map[string]any{"type": "enabled"}},
		},
		{
			name:     "lkeap deepseek r1 is always-on so no switch is sent",
			provider: "lkeap", model: "deepseek-r1",
			opts: &api.Options{Thinking: ptrBool(false)},
			want: map[string]any{"thinking": nil, "enable_thinking": nil, "reasoning_effort": nil},
		},
		{
			name: "generic uses chat_template_kwargs", provider: "generic", model: "Qwen/Qwen3-32B",
			opts: &api.Options{Thinking: ptrBool(true)},
			want: map[string]any{"chat_template_kwargs": map[string]any{"enable_thinking": true}},
		},
		{
			name: "nvidia uses chat_template_kwargs", provider: "nvidia", model: "some-nim-model",
			opts: &api.Options{Thinking: ptrBool(true)},
			want: map[string]any{"chat_template_kwargs": map[string]any{"enable_thinking": true}},
		},
		{
			name:     "moonshot v1 pins temperature to 1 and drops other sampling",
			provider: "moonshot", model: "moonshot-v1-8k",
			opts: &api.Options{Temperature: 0.2, TopP: 0.9},
			want: map[string]any{"temperature": 1.0, "top_p": nil},
		},
		{
			// deepseek-chat is catalogued as non-reasoning, so this also pins the
			// precedence rule: an explicit legacy override outranks that gate.
			name:     "legacy extra_config.thinking_control still overrides the vendor default",
			provider: "deepseek", model: "deepseek-chat",
			extra: map[string]string{catalog.ExtraThinkingControl: "chat_template_kwargs"},
			opts:  &api.Options{Thinking: ptrBool(true)},
			want: map[string]any{
				"chat_template_kwargs": map[string]any{"enable_thinking": true}, "thinking": nil,
			},
		},
		{
			name:     "legacy extra_config.thinking_control=none silences thinking",
			provider: "volcengine", model: "doubao-seed-1-6-251015",
			extra: map[string]string{catalog.ExtraThinkingControl: "none"},
			opts:  &api.Options{Thinking: ptrBool(true)},
			want:  map[string]any{"thinking": nil, "reasoning_effort": nil},
		},
		{
			name:     "remote_model_name still overrides the wire model id",
			provider: "deepseek", model: "deepseek-chat",
			extra: map[string]string{catalog.ExtraRemoteModelName: "deepseek-v4-pro-internal"},
			opts:  &api.Options{},
			want:  map[string]any{"model": "deepseek-v4-pro-internal"},
		},

		{
			name:     "a catalogued non-reasoning model never receives thinking fields",
			provider: "deepseek", model: "deepseek-chat",
			opts: &api.Options{Thinking: ptrBool(true)},
			want: map[string]any{"thinking": nil, "reasoning_effort": nil, "enable_thinking": nil},
			divergence: "DeepSeek's non-thinking alias is marked reasoning:false in the catalog, so " +
				"Resolve now silences the thinking switch even when the caller asks for it.",
		},

		// ---- deliberate divergences from the old behaviour ----
		{
			name:     "deepseek now honours thinking=false instead of ignoring it",
			provider: "deepseek", model: "deepseek-v4-pro",
			opts: &api.Options{Thinking: ptrBool(false)},
			want: map[string]any{"thinking": map[string]any{"type": "disabled"}},
			divergence: "old code had no thinking strategy for DeepSeek, so an agent with thinking off " +
				"still got the vendor default (thinking on). The vendor documents thinking.type, so the " +
				"user's setting is now honoured.",
		},
		{
			name: "zhipu now honours thinking=false", provider: "zhipu", model: "glm-4.7",
			opts:       &api.Options{Thinking: ptrBool(false)},
			want:       map[string]any{"thinking": map[string]any{"type": "disabled"}},
			divergence: "same as DeepSeek: GLM documents thinking.type and the old code sent nothing.",
		},
		{
			name:     "openai first-party now uses the Responses protocol",
			provider: "openai", model: "gpt-5.5",
			opts: &api.Options{MaxTokens: 100},
			want: map[string]any{"max_output_tokens": float64(100), "max_completion_tokens": nil},
			divergence: "api.openai.com now speaks the Responses protocol (reasoning items and encrypted " +
				"reasoning can be replayed); relays keep Chat Completions.",
		},
		{
			name:     "openai through a relay keeps Chat Completions",
			provider: "openai", model: "gpt-5.5",
			opts: &api.Options{MaxTokens: 100},
			want: map[string]any{"max_completion_tokens": float64(100), "max_output_tokens": nil},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &chat.ChatConfig{
				Provider: tc.provider, ModelName: tc.model, BaseURL: tc.baseURL, ExtraConfig: tc.extra,
			}
			if strings.Contains(tc.name, "through a relay") {
				cfg.BaseURL = "http://127.0.0.1:9/v1"
			}
			body := buildBody(t, cfg, tc.opts, tc.stream)
			for key, want := range tc.want {
				got, present := body[key]
				if want == nil {
					assert.False(t, present, "field %q must be absent, got %v", key, got)
					continue
				}
				assert.Equal(t, want, got, "field %q", key)
			}
		})
	}
}

// TestLegacyProviderDetection pins the URL table of the old DetectProvider
// switch: a stored model row without an explicit provider must still resolve
// to the same vendor as before.
func TestLegacyProviderDetection(t *testing.T) {
	cases := map[string]string{
		"https://dashscope.aliyuncs.com/compatible-mode/v1": "aliyun",
		"https://open.bigmodel.cn/api/paas/v4":              "zhipu",
		"https://openrouter.ai/api/v1":                      "openrouter",
		"https://router.requesty.ai/v1":                     "requesty",
		"https://api.siliconflow.cn/v1":                     "siliconflow",
		"https://api.jina.ai/v1":                            "jina",
		"https://my-resource.openai.azure.com":              "azure_openai",
		"https://api.openai.com/v1":                         "openai",
		"https://api.anthropic.com/v1":                      "anthropic",
		"https://api.deepseek.com/v1":                       "deepseek",
		"https://generativelanguage.googleapis.com/v1beta":  "gemini",
		"https://ark.cn-beijing.volces.com/api/v3":          "volcengine",
		"https://api.hunyuan.cloud.tencent.com/v1":          "hunyuan",
		"https://api.minimaxi.com/v1":                       "minimax",
		"https://api.xiaomimimo.com/v1":                     "mimo",
		"https://api-inference.modelscope.cn/v1":            "modelscope",
		"https://api.qnaigc.com/v1":                         "qiniu",
		"https://api.moonshot.ai/v1":                        "moonshot",
		"https://qianfan.baidubce.com/v2":                   "qianfan",
		"https://api.longcat.chat/openai/v1":                "longcat",
		"https://api.lkeap.cloud.tencent.com/v1":            "lkeap",
		"https://integrate.api.nvidia.com/v1":               "nvidia",
		"https://api.novita.ai/openai/v1":                   "novita",
		"https://weknora.weixin.qq.com":                     "weknoracloud",
		"http://localhost:8000/v1":                          "generic",
		"":                                                  "generic",
	}
	for url, want := range cases {
		assert.Equal(t, want, catalog.DetectByURL(url), "DetectByURL(%q)", url)
	}
}

// TestCatalogInvariants walks every registered vendor and model. It is the
// net that catches a newly added vendor or model entry that is malformed,
// contradictory, or unresolvable — without anyone updating this test.
func TestCatalogInvariants(t *testing.T) {
	vendors := catalog.List()
	require.NotEmpty(t, vendors, "no vendors registered")
	seenID := map[string]bool{}

	for _, v := range vendors {
		t.Run(v.ID, func(t *testing.T) {
			assert.False(t, seenID[v.ID], "duplicate vendor id")
			seenID[v.ID] = true
			assert.Equal(t, strings.ToLower(v.ID), v.ID, "vendor id must be lowercase")
			assert.NotEmpty(t, v.Name, "vendor needs a display name")
			assert.NotEmpty(t, v.Description, "vendor needs a description for the picker")
			assert.True(t, v.API.Known(), "unknown default API %q", v.API)
			assert.NotEmpty(t, v.ModelTypes, "vendor supports no model type")
			assert.True(t, strings.HasPrefix(strings.TrimSpace(string(v.Icon)), "<svg"), "icon must be an SVG")
			assert.LessOrEqual(t, len(v.Icon), 6*1024, "icon over the 6 KB budget")
			if v.RequiresAuth {
				assert.NotEmpty(t, v.Auth, "RequiresAuth without an auth style")
			}
			if v.Auth == catalog.AuthSigned {
				assert.NotNil(t, v.Signer, "signed auth without a Signer hook")
			}

			for _, field := range v.ExtraFields {
				assert.NotEmpty(t, field.Key, "extra field without a key")
				assert.NotEmpty(t, field.Label, "extra field %q without a label", field.Key)
				assert.Contains(t, []string{"string", "number", "boolean", "select", "password"}, field.Type,
					"extra field %q has an unsupported type", field.Key)
				if field.Type == "select" {
					assert.NotEmpty(t, field.Options, "select field %q without options", field.Key)
				}
			}

			// Default URLs must be absolute and only declared for supported types.
			for mt, url := range v.DefaultBaseURLs {
				assert.True(t, strings.HasPrefix(url, "http://") || strings.HasPrefix(url, "https://"),
					"%s default URL %q is not absolute", mt, url)
				assert.True(t, v.SupportsType(mt), "default URL for unsupported model type %s", mt)
			}

			ids := map[string]bool{}
			for _, m := range v.Models {
				label := m.ID
				if label == "" {
					label = "match:" + m.Match
				}
				assert.True(t, m.ID != "" || m.Match != "", "model entry needs an id or a match pattern")
				if m.ID != "" {
					assert.False(t, ids[strings.ToLower(m.ID)], "duplicate model id %q", m.ID)
					ids[strings.ToLower(m.ID)] = true
				}
				assert.GreaterOrEqual(t, m.ContextWindow, 0, "%s: negative context window", label)
				assert.GreaterOrEqual(t, m.MaxOutputTokens, 0, "%s: negative max output", label)
				if m.ContextWindow > 0 && m.MaxOutputTokens > 0 {
					assert.LessOrEqual(t, m.MaxOutputTokens, m.ContextWindow,
						"%s: max output exceeds the context window", label)
				}
				for _, in := range m.Input {
					assert.Contains(t, []string{"text", "image", "audio", "video"}, in,
						"%s: unknown input modality %q", label, in)
				}
				for level := range m.ThinkingLevels {
					_, ok := api.ParseReasoningEffort(string(level))
					assert.True(t, ok, "%s: unknown thinking level %q", label, level)
				}
				if len(m.ThinkingLevels) > 0 {
					assert.True(t, m.Reasoning, "%s: thinking levels on a non-reasoning model", label)
				}
				if m.Cost != nil {
					assert.GreaterOrEqual(t, m.Cost.Input, 0.0, "%s: negative input cost", label)
					assert.GreaterOrEqual(t, m.Cost.Output, 0.0, "%s: negative output cost", label)
				}
				if m.ID != "" {
					// Resolve validates the compat object against the model's
					// protocol: an unknown or misspelled key fails here.
					_, err := catalog.Resolve(catalog.Ref{Provider: v.ID, Model: m.ID, ModelType: m.Type})
					if bytes.Contains(m.Compat, []byte("unsupported_reason")) {
						// An entry that declares itself unserveable must say so
						// by refusing, not by resolving into a request shaped
						// for a protocol this build does not speak.
						assert.Error(t, err, "%s: declares unsupported_reason but still resolves", label)
						continue
					}
					assert.NoError(t, err, "%s: does not resolve", label)
				}
			}
		})
	}
}

// TestEveryChatModelRequestShape builds a real request for every catalog chat
// model, with thinking on and off, and asserts the invariants that hold for
// every vendor. A new model whose compat contradicts itself fails here.
func TestEveryChatModelRequestShape(t *testing.T) {
	for _, v := range catalog.List() {
		if !v.SupportsType(types.ModelTypeKnowledgeQA) {
			continue
		}
		for _, m := range v.ModelsByType(types.ModelTypeKnowledgeQA) {
			m := m
			t.Run(v.ID+"/"+m.ID, func(t *testing.T) {
				resolved := resolve(t, v.ID, m.ID)
				cfg := &chat.ChatConfig{
					Provider: v.ID, ModelName: m.ID,
					BaseURL: reachableBaseURL(v.GetDefaultURL(types.ModelTypeKnowledgeQA)),
					AppID:   "app", AppSecret: "secret", // only used by signing vendors
				}
				for _, thinking := range []bool{true, false} {
					opts := &api.Options{MaxTokens: 256, Temperature: 0.5, Thinking: ptrBool(thinking)}
					body := buildBody(t, cfg, opts, true)

					_, hasMaxTokens := body["max_tokens"]
					_, hasMaxCompletion := body["max_completion_tokens"]
					_, hasMaxOutput := body["max_output_tokens"]
					_, hasGenerationConfig := body["generationConfig"]
					budgets := 0
					for _, present := range []bool{hasMaxTokens, hasMaxCompletion, hasMaxOutput, hasGenerationConfig} {
						if present {
							budgets++
						}
					}
					assert.Equal(t, 1, budgets,
						"thinking=%v: exactly one completion-budget field expected, body=%v", thinking, body)

					if resolved.API != api.APIOpenAICompletions {
						continue
					}
					settings := resolved.OpenAICompletions
					if !settings.SupportsTemperature {
						assert.NotContains(t, body, "temperature",
							"thinking=%v: model rejects sampling parameters", thinking)
					}
					if settings.ThinkingFormat == catalog.ThinkingFormatNone {
						for _, field := range []string{"thinking", "enable_thinking", "reasoning_effort", "reasoning"} {
							assert.NotContains(t, body, field,
								"thinking=%v: %s must not be sent when the vendor has no thinking format",
								thinking, field)
						}
					}
					if !thinking && !resolved.ThinkingLevels.Supports(api.ReasoningOff) {
						for _, field := range []string{"thinking", "enable_thinking"} {
							if value, ok := body[field]; ok {
								assert.NotEqual(t, map[string]any{"type": "disabled"}, value,
									"always-on model must not be told to disable thinking")
								assert.NotEqual(t, false, value,
									"always-on model must not be told to disable thinking")
							}
						}
					}
					if !settings.SupportsReasoningEffort {
						assert.NotContains(t, body, "reasoning_effort",
							"thinking=%v: vendor does not accept reasoning_effort", thinking)
					}
				}
			})
		}
	}
}

// TestWeKnoraCloudTransport pins the two things that vendor needs and no
// other vendor does: a signed request to a non-standard path, and plain-text
// content because the endpoint rejects multi-part messages.
func TestWeKnoraCloudTransport(t *testing.T) {
	v, ok := catalog.Get("weknoracloud")
	require.True(t, ok)
	require.NotNil(t, v.Endpoint, "WeKnora Cloud needs a custom endpoint")
	url, _ := v.Endpoint(catalog.EndpointRequest{
		BaseURL: "https://weknora.weixin.qq.com", ModelType: types.ModelTypeKnowledgeQA,
	})
	assert.Equal(t, "https://weknora.weixin.qq.com/api/v1/chat/completions", url)

	resolved := resolve(t, "weknoracloud", "any-model")
	assert.False(t, resolved.OpenAICompletions.SupportsMultiContent,
		"WeKnora Cloud needs multi-content flattened to text")

	cfg := &chat.ChatConfig{Provider: "weknoracloud", ModelName: "any-model", AppID: "app", AppSecret: "secret"}
	body := buildBody(t, cfg, &api.Options{}, false)
	messages, _ := json.Marshal(body["messages"])
	assert.Contains(t, string(messages), `"content":"hi"`, "content must be a plain string")
}

// TestGeminiLegacyBaseURLKeepsOpenAIProtocol covers the rows that exist in
// databases today: Gemini chat models were stored with the OpenAI-compatible
// base URL, and those must keep speaking that protocol after the switch to
// native generateContent as the default.
func TestGeminiLegacyBaseURLKeepsOpenAIProtocol(t *testing.T) {
	r, err := catalog.Resolve(catalog.Ref{
		Provider: "gemini", Model: "gemini-2.5-pro",
		BaseURL: "https://generativelanguage.googleapis.com/v1beta/openai",
	})
	require.NoError(t, err)
	assert.Equal(t, api.APIOpenAICompletions, r.API)

	native := resolve(t, "gemini", "gemini-2.5-pro")
	assert.Equal(t, api.APIGoogleGenerativeAI, native.API, "new rows use the native protocol")
}

// TestGeminiCredentialFollowsTheProtocol pins the header each Gemini path
// authenticates with. The two surfaces document different credentials —
// x-goog-api-key for native generateContent, Authorization: Bearer for the
// OpenAI-compatible facade (https://ai.google.dev/gemini-api/docs/openai) —
// and the auth style is a vendor-wide setting, so without the per-protocol
// override every row stored before the native protocol became the default
// would silently change credential: they all carry the /v1beta/openai base
// URL that the pre-catalog code paired with a bearer token.
func TestGeminiCredentialFollowsTheProtocol(t *testing.T) {
	var got http.Header
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}],` +
			`"candidates":[{"content":{"parts":[{"text":"ok"}]}}]}`))
	}))
	defer server.Close()

	for _, tc := range []struct {
		name       string
		baseURL    string
		wantHeader string
		otherEmpty string
	}{
		{
			name: "openai facade keeps the bearer token", baseURL: server.URL + "/v1beta/openai",
			wantHeader: "Authorization", otherEmpty: "x-goog-api-key",
		},
		{
			name: "native protocol uses the google api key", baseURL: server.URL + "/v1beta",
			wantHeader: "x-goog-api-key", otherEmpty: "Authorization",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client, err := chat.NewRemoteChat(&chat.ChatConfig{
				Source: types.ModelSourceRemote, Provider: "gemini",
				ModelName: "gemini-2.5-pro", BaseURL: tc.baseURL, APIKey: "KEY",
			})
			require.NoError(t, err)
			_, _ = client.Chat(context.Background(), []chat.Message{{Role: "user", Content: "hi"}},
				&chat.ChatOptions{})

			assert.NotEmpty(t, got.Get(tc.wantHeader), "%s must carry the credential", tc.wantHeader)
			assert.Empty(t, got.Get(tc.otherEmpty), "%s must not be sent on this protocol", tc.otherEmpty)
		})
	}
}

// TestConcurrentResolve runs the resolution path from many goroutines at
// once, the way a busy server does: every chat call resolves the catalog
// before building its request. Registration happens once at init, but the
// registry map and the per-vendor slices are shared, so a resolver that
// mutated them would corrupt another request. Run with -race.
func TestConcurrentResolve(t *testing.T) {
	vendors := catalog.List()
	require.NotEmpty(t, vendors)

	const goroutines = 32
	done := make(chan struct{}, goroutines)
	for i := 0; i < goroutines; i++ {
		go func(i int) {
			defer func() { done <- struct{}{} }()
			for _, v := range vendors {
				models := v.ModelsByType(types.ModelTypeKnowledgeQA)
				name := "unknown-model"
				if len(models) > 0 {
					name = models[i%len(models)].ID
				}
				r, err := catalog.Resolve(catalog.Ref{Provider: v.ID, Model: name})
				if err != nil {
					t.Errorf("resolve %s/%s: %v", v.ID, name, err)
					return
				}
				// Touch the derived views the handlers use.
				_ = r.Capabilities()
				_ = r.ThinkingLevels.SupportedLevels()
				_ = catalog.DetectByURL(r.BaseURL)
			}
		}(i)
	}
	for i := 0; i < goroutines; i++ {
		<-done
	}
}

// TestEveryCatalogModelDocumentsItsSource pins that each entry can answer
// "where is this documented".
//
// Two things depend on it. The facts in models.json are only auditable if the
// page they came from is recorded next to them, and the model editor links an
// operator straight to that page from the picker. models.json may state the
// URL once at file level for the entries a single page covers, so this also
// covers MustParseModels pushing it down — before that it was parsed and
// dropped, and most entries had nothing to link to.
func TestEveryCatalogModelDocumentsItsSource(t *testing.T) {
	for _, vendor := range catalog.List() {
		for _, model := range vendor.Models {
			name := model.ID
			if name == "" {
				name = model.Match
			}
			t.Run(vendor.ID+"/"+name, func(t *testing.T) {
				if model.Source == "" {
					t.Fatalf("no source: add one to the entry, or a file-level source to models.json")
				}
				if !strings.HasPrefix(model.Source, "https://") && !strings.HasPrefix(model.Source, "http://") {
					t.Errorf("source %q is not a URL the editor can link to", model.Source)
				}
			})
		}
	}
}
