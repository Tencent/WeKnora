// Package adapters hosts the per-vendor invoke adapters (design §6.2): each
// file registers its adapters into invoke.Default from init() (compile-time
// registration, v1 convention) and implements the facet interfaces against
// the vendor's native wire format. Adapters never touch HTTP; they produce a
// invoke.Request ("native call description") that the unified executor sends.
package adapters

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/Tencent/WeKnora/internal/models/invoke"
	"github.com/sashabaranov/go-openai"
)

// openaiAdapter is the OpenAI-compatible fallback chat adapter (design §6.7):
// every vendor without a bespoke protocol rides it. It is a behavior port of
// v1 internal/models/chat/{remote_api,openai_request,provider,thinking,
// completion_budget,prompt_cache}.go — migration, not a rewrite.
//
// One instance serves one provider name; v1's per-adapter Matches() predicates
// become per-model branches inside shapeFor/thinkingFor.
type openaiAdapter struct {
	name invoke.ProviderName
	// spec carries the v1 providerAdapter overrides for this vendor.
	spec openaiVendorSpec
	// caps is the Chat capability shard (trimmed: shards this adapter does not
	// implement yet stay nil so registration lock #2 holds — embedding/rerank
	// migrate in P2/P3 and the frontend DTO still comes from the provider
	// package until the strangler switch completes).
	caps *invoke.ChatCaps
	// OpenAIStreamBridge is the exported openai-shape default bridge, embedded
	// so TranslateStreamEvent is promoted (seam ⑤, P1c: the former
	// package-local port is deduped into invoke.OpenAIStreamBridge).
	invoke.OpenAIStreamBridge
}

// openaiVendorSpec encodes the v1 providerAdapter deltas (provider.go): empty
// fields fall back to baseProvider behavior (Bearer auth, standard endpoint,
// no shaping, no thinking).
type openaiVendorSpec struct {
	// forceRaw mirrors ForceRawHTTP: the wire body must be the map-form
	// roundtrip of the request (buildProviderOpenAIRequest) so vendor-only
	// fields survive. v1: deepseek (native cache counters), gemini
	// (thought signatures), weknoracloud (signing).
	forceRaw bool
	// azure switches auth to the api-key header and the URL to the
	// deployment path + api-version query.
	azure bool
	// sign marks the body-HMAC vendors (WeKnoraCloud): the signature covers
	// the final body bytes, so it is applied after marshaling.
	sign bool
	// transform mirrors TransformMessages (v1 weknoracloud multi-content
	// downgrade).
	transform func([]openai.ChatCompletionMessage) []openai.ChatCompletionMessage
	// shape mirrors ShapeRequest (parameter post-shaping).
	shape func(req *openai.ChatCompletionRequest, opts *invoke.ChatOptions)
	// thinking mirrors ThinkingStrategy.Apply: returns (customBody, true) when
	// the wire body must be a vendor wrapper struct, or mutates req in place
	// and returns (req, true).
	thinking func(req *openai.ChatCompletionRequest, opts *invoke.ChatOptions, isStream bool) (any, bool)
}

// compile-time lock #1 (design §6.2).
var _ invoke.ChatAdapter = (*openaiAdapter)(nil)

func (a *openaiAdapter) Provider() string { return string(a.name) }

// Capabilities returns the trimmed adapter capability declaration: the Chat
// shard plus the model-listing flag this adapter serves (BuildListRequest in
// list.go promotes to every composite here). azure_openai shares the struct;
// its listing attempt fails at Build (deployment mode), so the flag reads
// true family-wide but the azure probe degrades with an explicit reason —
// v1 sent the doomed request and degraded on the 404 instead.
func (a *openaiAdapter) Capabilities() invoke.Capabilities {
	return invoke.Capabilities{
		Common: invoke.CommonCaps{ModelListing: invoke.ModelListingCaps{Supported: true}},
		Chat:   a.caps,
	}
}

// chatCapsFor resolves the provider-level Chat shard from the v1 capability
// registry (nil when the provider does not serve chat).
func chatCapsFor(name invoke.ProviderName) *invoke.ChatCaps {
	info, ok := providerInfoFor(name)
	if !ok {
		return nil
	}
	return info.EffectiveCapabilities().Chat
}

// specFor ports the v1 providerRegistry overrides (chat/provider.go:296-312).
func specFor(name invoke.ProviderName) openaiVendorSpec {
	var spec openaiVendorSpec
	switch name {
	case invoke.ProviderDeepSeek:
		// deepseekProvider: ForceRawHTTP (native prompt-cache hit/miss
		// counters) + tool_choice strip (unsupported by DeepSeek).
		spec.forceRaw = true
		spec.shape = shapeDeepSeek
	case invoke.ProviderGemini:
		// UNREACHABLE since P5-1: gemini registers via GeminiAdapter (native
		// generateContent route) and no openaiAdapter instance carries this
		// name anymore. Kept as the compat-layer record: ForceRawHTTP let
		// vendor-only fields survive the SDK marshal; tool thought-signature
		// metadata had no channel in the neutral message model (§6.1).
		spec.forceRaw = true
	case invoke.ProviderAzureOpenAI:
		spec.azure = true
	case invoke.ProviderVolcengine:
		// volcengineProvider: thinking via { "thinking": { "type": ... } }.
		spec.thinking = thinkingTypeApply
	case invoke.ProviderGeneric, invoke.ProviderNvidia, invoke.ProviderLiteLLM:
		// generic/nvidia/LiteLLMProvider: chat_template_kwargs.enable_thinking.
		spec.thinking = chatTemplateKwargsApply
	}
	return spec
}

// shapeFor resolves the model-conditional shaping (v1 Matches() predicates):
// openai/azure reasoning models strip sampling params and pass the thinking
// level through as reasoning_effort; moonshot fixed-temp models pin
// temperature=1.
func (a *openaiAdapter) shapeFor(model string) func(*openai.ChatCompletionRequest, *invoke.ChatOptions) {
	switch a.name {
	case invoke.ProviderOpenAI, invoke.ProviderAzureOpenAI:
		if invoke.IsOpenAIReasoningOrGPT5Model(model) {
			return shapeOpenAIReasoning
		}
	case invoke.ProviderMoonshot:
		if invoke.IsMoonshotFixedTempModel(model) {
			return shapeMoonshotFixedTemp
		}
	}
	return nil
}

// thinkingFor resolves the model-conditional thinking strategy (v1
// qwenThinkingProvider / lkeapProvider Matches predicates).
func (a *openaiAdapter) thinkingFor(
	model string,
) func(*openai.ChatCompletionRequest, *invoke.ChatOptions, bool) (any, bool) {
	if a.spec.thinking != nil {
		return a.spec.thinking
	}
	switch a.name {
	case invoke.ProviderAliyun:
		// qwenThinkingProvider: enable_thinking always sent, forced off
		// non-stream (Qwen3 rejects thinking in non-stream mode).
		if invoke.IsQwenThinkingModel(model) {
			return enableThinkingApply
		}
	case invoke.ProviderLKEAP:
		// lkeapProvider: { "thinking": { "type": ... } } for DeepSeek V3.x
		// only; R1 enables chain-of-thought by default and stays untouched.
		if invoke.IsLKEAPDeepSeekV3Model(model) {
			return thinkingTypeApply
		}
	}
	return nil
}

// thinkingControlOverride maps the entry-folded ExtraConfig["thinking_control"]
// token (v1 parseThinkingOverride, chat/thinking.go:124-141) onto a strategy:
// "" → nil (the per-vendor/model default in thinkingFor applies), "none" →
// noThinking, "enable_thinking" / "thinking_type" / "chat_template_kwargs" →
// their strategies, and ANY unknown non-empty value falls back to
// chat_template_kwargs (legacy default-mode behavior — never an error).
func thinkingControlOverride(token string) func(*openai.ChatCompletionRequest, *invoke.ChatOptions, bool) (any, bool) {
	switch token {
	case "":
		return nil
	case "none":
		return noThinkingApply
	case "enable_thinking":
		return enableThinkingApply
	case "thinking_type":
		return thinkingTypeApply
	default:
		return chatTemplateKwargsApply
	}
}

// noThinkingApply ports noThinking (thinking.go:55-59): send no thinking
// fields at all, overriding the adapter's default strategy.
func noThinkingApply(*openai.ChatCompletionRequest, *invoke.ChatOptions, bool) (any, bool) {
	return nil, false
}

// BuildChatRequest ports the v1 outbound funnel (remote_api.go buildOutbound
// + shapedRequest) to the adapter contract: build → transform → shape →
// thinking → provider body form → prompt cache → marshal. It returns the full
// native call description (URL/headers/body); the entry overlays user custom
// headers (§6.4) and the executor sends it.
func (a *openaiAdapter) BuildChatRequest(
	ep invoke.Endpoint, model string, opts *invoke.ChatOptions,
) (*invoke.Request, error) {
	isStream := opts != nil && opts.Stream

	// v1 openai_request.go BuildChatCompletionRequest + ConvertMessages.
	req := buildChatCompletionRequest(a.name, model, opts, isStream)
	// v1 providerAdapter.TransformMessages (weknoracloud multi-content downgrade).
	if a.spec.transform != nil {
		req.Messages = a.spec.transform(req.Messages)
	}
	// v1 providerAdapter.ShapeRequest: vendor-level spec override first, then
	// the model-conditional shaping (v1 Matches() predicates).
	if a.spec.shape != nil {
		a.spec.shape(&req, opts)
	}
	if shape := a.shapeFor(model); shape != nil {
		shape(&req, opts)
	}
	// v1 thinking strategies (thinking.go): the entry-folded
	// ExtraConfig["thinking_control"] override (ep.ThinkingControl, v1
	// parseThinkingOverride semantics) wins over the per-vendor/model default;
	// nil/absent = send nothing.
	useRaw := false
	var body any = &req
	applyThinking := a.thinkingFor(model)
	if override := thinkingControlOverride(ep.ThinkingControl); override != nil {
		applyThinking = override
	}
	if applyThinking != nil {
		if custom, useCustom := applyThinking(&req, opts, isStream); custom != nil {
			body = custom
			useRaw = useCustom
		}
	}
	// v1 shapeProviderRequest: ForceRawHTTP vendors send the map-form
	// roundtrip so vendor-only wire fields survive the SDK boundary.
	useRaw = useRaw || a.spec.forceRaw
	if a.spec.forceRaw {
		m, err := providerRequestMap(body)
		if err != nil {
			return nil, err
		}
		body = m
	}
	// v1 prompt cache injection (prompt_cache.go): routing key + cache_control
	// breakpoints, applied after shaping exactly as buildOutbound did.
	retention := resolveCacheRetention(opts)
	policy := promptCachePolicyFor(a.name, ep.BaseURL)
	sessionID := clampPromptCacheKey(opts.PromptCacheKey)
	body, cacheRewritten, err := applyPromptCacheToJSONBody(body, policy, sessionID, retention)
	if err != nil {
		return nil, err
	}

	data, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	header := make(http.Header)
	header.Set("Content-Type", "application/json")
	// Accept mirrors v1 path selection: SDK non-stream requests carry
	// Accept application/json (go-openai sendRequest); raw-HTTP requests
	// carry none unless streaming (text/event-stream).
	useRaw = useRaw || cacheRewritten
	if isStream {
		header.Set("Accept", "text/event-stream")
	} else if !useRaw {
		header.Set("Accept", "application/json")
	}
	a.setAuthHeader(header, ep)
	attachPromptCacheHeaders(header, policy, sessionID)

	return &invoke.Request{
		Method: http.MethodPost,
		URL:    a.requestURL(ep, model),
		Header: header,
		Body:   data,
		Stream: isStream,
	}, nil
}

// setAuthHeader ports baseProvider.Auth / azureProvider.Auth (provider.go).
func (a *openaiAdapter) setAuthHeader(header http.Header, ep invoke.Endpoint) {
	if a.spec.azure {
		header.Set("api-key", ep.Credentials.APIKey)
		return
	}
	if !a.spec.sign {
		header.Set("Authorization", "Bearer "+ep.Credentials.APIKey)
	}
}

// api-version query (v1 go-openai DefaultAzureConfig), weknoracloud's
// /api/v1/chat/completions, and the standard /chat/completions suffix.
// Empty baseURL falls back to the vendor default exactly as the v1
// constructor configured the SDK (openai default / deepseek official URL).
func (a *openaiAdapter) requestURL(ep invoke.Endpoint, model string) string {
	base := strings.TrimRight(ep.BaseURL, "/")
	switch {
	case a.spec.azure:
		// v1 overrides AzureModelMapperFunc to identity (deployment = model);
		// api_version folds from ExtraConfig via Endpoint.APIVersion (seam ④),
		// falling back to the go-openai DefaultAzureConfig default.
		apiVersion := ep.APIVersion
		if apiVersion == "" {
			apiVersion = azureAPIVersionDefault
		}
		return fmt.Sprintf("%s/openai/deployments/%s/chat/completions?api-version=%s", base, model, apiVersion)
	case a.name == invoke.ProviderWeKnoraCloud:
		return base + "/api/v1/chat/completions"
	}
	if base == "" {
		// v1 NewRemoteAPIChat: empty baseURL → SDK default (openai) or the
		// DeepSeek official endpoint.
		if a.name == invoke.ProviderDeepSeek {
			base = invoke.DeepSeekBaseURL
		} else {
			base = openAIDefaultBaseURL
		}
	}
	return base + "/chat/completions"
}

const (
	// azureAPIVersionDefault mirrors go-openai DefaultAzureConfig's default;
	// the ExtraConfig["api_version"] override reaches here via
	// Endpoint.APIVersion (entry-folded from the model record, P1c seam ④).
	azureAPIVersionDefault = "2023-05-15"
	// openAIDefaultBaseURL mirrors go-openai's default base URL for providers
	// constructed without an explicit baseURL.
	openAIDefaultBaseURL = invoke.OpenAIBaseURL
)

// ParseChatResponse ports v1 parseCompletionResponse (openai_stream.go:18-51)
// + applyRawPromptCacheUsage: first choice, <think> stripping, tool calls,
// usage. Response errors surface as *ProviderError only (design §6.5).
func (a *openaiAdapter) ParseChatResponse(_ int, _ http.Header, body []byte) (*invoke.ChatResponse, error) {
	var resp openai.ChatCompletionResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, invoke.ClassifyError(fmt.Errorf("decode response: %w", err))
	}
	if len(resp.Choices) == 0 {
		return nil, invoke.ClassifyError(fmt.Errorf("no response from API"))
	}
	choice := resp.Choices[0]
	// 思考模型兜底：Thinking=false 但模型仍输出 <think> 包裹内容时剥离
	// （v1 removeThinkingContent，Miniax-M2.1 等）。
	content := removeThinkingContent(choice.Message.Content)
	out := &invoke.ChatResponse{
		Content:      content,
		FinishReason: string(choice.FinishReason),
		Usage: invoke.Usage{
			PromptTokens:     resp.Usage.PromptTokens,
			CompletionTokens: resp.Usage.CompletionTokens,
			TotalTokens:      resp.Usage.TotalTokens,
		},
	}
	// v1 applyRawPromptCacheUsage (remote_api.go:329): native cache counters
	// the SDK shape drops (deepseek hit/miss, anthropic-style read/creation,
	// openai prompt_tokens_details) are captured from the raw body (seam ③).
	invoke.ApplyRawPromptCacheUsage(body, &out.Usage)

	if len(choice.Message.ToolCalls) > 0 {
		out.ToolCalls = make([]invoke.ToolCall, 0, len(choice.Message.ToolCalls))
		for _, tc := range choice.Message.ToolCalls {
			out.ToolCalls = append(out.ToolCalls, invoke.ToolCall{
				ID:   tc.ID,
				Type: string(tc.Type),
				Function: invoke.FunctionCall{
					Name:      tc.Function.Name,
					Arguments: tc.Function.Arguments,
				},
			})
		}
	}
	return out, nil
}

// TranslateStreamEvent rides the embedded invoke.OpenAIStreamBridge (seam ⑤);
// no local override needed.

// openAIFamilyProviders lists the providers the fallback serves. v1's
// registry dispatches by exact name, so every openai-family provider from the
// catalog registers an instance (anthropic/ollama/weknoracloud excluded —
// they have their own adapters).
var openAIFamilyProviders = []invoke.ProviderName{
	invoke.ProviderOpenAI,
	invoke.ProviderGeneric,
	invoke.ProviderAliyun,
	invoke.ProviderZhipu,
	invoke.ProviderVolcengine,
	invoke.ProviderHunyuan,
	invoke.ProviderSiliconFlow,
	invoke.ProviderDeepSeek,
	invoke.ProviderMiniMax,
	invoke.ProviderMoonshot,
	invoke.ProviderModelScope,
	invoke.ProviderQianfan,
	invoke.ProviderQiniu,
	// gemini moved out in P5-1: the native generateContent adapter
	// (gemini.go) replaces the openai-compat chat route; embedding stays on
	// the same native pair via GeminiAdapter.
	invoke.ProviderOpenRouter,
	invoke.ProviderLiteLLM,
	invoke.ProviderRequesty,
	invoke.ProviderMimo,
	invoke.ProviderLongCat,
	invoke.ProviderLKEAP,
	invoke.ProviderGPUStack,
	invoke.ProviderNvidia,
	invoke.ProviderNovita,
	invoke.ProviderAzureOpenAI,
	// Embedding-only vendor (P2): no chat facet; the init loop registers it
	// through the jinaEmbeddingAdapter branch.
	invoke.ProviderJina,
}

func init() {
	for _, name := range openAIFamilyProviders {
		caps := chatCapsFor(name)
		ecaps := embeddingCapsFor(name)
		rcaps := rerankCapsFor(name)
		// P3 interim ruling: lkeap (TC3) and volcengine (IAM) rerank stay on
		// their v1 SDK clients — signature protocols are P5-class work. Their
		// adapters keep the rerank shard TRIMMED here (P1a interim convention:
		// adapter caps = implemented facets; the frontend DTO still reads the
		// provider package) and the service layer branches those two vendors
		// to the v1 clients.
		if name == invoke.ProviderLKEAP || name == invoke.ProviderVolcengine {
			rcaps = nil
		}
		acaps := asrCapsFor(name)
		switch {
		case caps != nil && ecaps != nil && acaps != nil && rcaps == nil:
			// chat + embedding + ASR (azure_openai — no rerank shard).
			adapter := &openaiASREmbeddingAdapter{openaiEmbeddingAdapter{
				openaiAdapter: openaiAdapter{name: name, spec: specFor(name), caps: caps},
				espec:         embedSpecFor(name),
			}}
			if err := invoke.Default.Register(adapter); err != nil {
				panic(fmt.Sprintf("invoke/adapters: register openai-family adapter %s: %v", name, err))
			}
		case caps != nil && ecaps != nil && acaps != nil:
			// Quad-facet vendor (openai/generic/azure_openai/siliconflow/
			// gpustack): chat + embedding + rerank + ASR.
			adapter := &openaiASRRerankAdapter{openaiRerankAdapter{
				openaiEmbeddingAdapter: openaiEmbeddingAdapter{
					openaiAdapter: openaiAdapter{name: name, spec: specFor(name), caps: caps},
					espec:         embedSpecFor(name),
				},
				rspec: rerankSpecFor(name),
			}}
			if err := invoke.Default.Register(adapter); err != nil {
				panic(fmt.Sprintf("invoke/adapters: register openai-family adapter %s: %v", name, err))
			}
		case caps != nil && ecaps != nil && name == invoke.ProviderVolcengine:
			// Ark multimodal embedding is single-input-per-request — register
			// the marked composite so the entry fans batches out per input.
			adapter := &volcengineEmbeddingAdapter{openaiEmbeddingAdapter{
				openaiAdapter: openaiAdapter{name: name, spec: specFor(name), caps: caps},
				espec:         embedSpecFor(name),
			}}
			if err := invoke.Default.Register(adapter); err != nil {
				panic(fmt.Sprintf("invoke/adapters: register volcengine adapter %s: %v", name, err))
			}
		case caps != nil && ecaps != nil && rcaps != nil:
			// Triple-facet vendor: chat + embedding + rerank (openai shape or
			// a per-vendor delta via rerankSpecFor).
			adapter := &openaiRerankAdapter{
				openaiEmbeddingAdapter: openaiEmbeddingAdapter{
					openaiAdapter: openaiAdapter{name: name, spec: specFor(name), caps: caps},
					espec:         embedSpecFor(name),
				},
				rspec: rerankSpecFor(name),
			}
			if err := invoke.Default.Register(adapter); err != nil {
				panic(fmt.Sprintf("invoke/adapters: register openai-family adapter %s: %v", name, err))
			}
		case caps != nil && ecaps != nil:
			// Dual-facet vendor: chat + embedding.
			adapter := &openaiEmbeddingAdapter{
				openaiAdapter: openaiAdapter{name: name, spec: specFor(name), caps: caps},
				espec:         embedSpecFor(name),
			}
			if err := invoke.Default.Register(adapter); err != nil {
				panic(fmt.Sprintf("invoke/adapters: register openai-family adapter %s: %v", name, err))
			}
		case caps != nil:
			// Chat-only vendor (deepseek/minimax/moonshot/qiniu/mimo/longcat/
			// lkeap — no migrated embedding shard).
			adapter := &openaiAdapter{name: name, spec: specFor(name), caps: caps}
			if err := invoke.Default.Register(adapter); err != nil {
				panic(fmt.Sprintf("invoke/adapters: register openai-family adapter %s: %v", name, err))
			}
		case ecaps != nil && rcaps != nil:
			// Embedding+rerank vendor (jina) — no chat facet to pair with.
			adapter := &jinaRerankAdapter{
				jinaEmbeddingAdapter: jinaEmbeddingAdapter{name: name},
				rspec:                rerankSpecFor(name),
			}
			if err := invoke.Default.Register(adapter); err != nil {
				panic(fmt.Sprintf("invoke/adapters: register embedding+rerank adapter %s: %v", name, err))
			}
		case ecaps != nil:
			// Embedding-only vendor — no chat facet to pair with.
			adapter := &jinaEmbeddingAdapter{name: name}
			if err := invoke.Default.Register(adapter); err != nil {
				panic(fmt.Sprintf("invoke/adapters: register embedding-only adapter %s: %v", name, err))
			}
		}
	}
}
