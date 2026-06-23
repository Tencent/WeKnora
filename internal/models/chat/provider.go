package chat

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/Tencent/WeKnora/internal/models/provider"
	modelutils "github.com/Tencent/WeKnora/internal/models/utils"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/google/uuid"
	"github.com/sashabaranov/go-openai"
)

// authCreds carries the credentials a providerAdapter needs to authenticate a
// raw HTTP request. APIKey covers the common Bearer / api-key cases; AppID and
// AppSecret are only used by signing providers (WeKnoraCloud).
type authCreds struct {
	APIKey    string
	AppID     string
	AppSecret string
}

// providerAdapter captures everything provider-specific about an
// OpenAI-compatible chat backend. Every method has a sensible default on
// baseProvider, so a new provider is added by embedding baseProvider and
// overriding only the one or two methods that actually differ.
type providerAdapter interface {
	// Name is the provider this adapter handles.
	Name() provider.ProviderName
	// Matches reports whether this adapter applies to the given model name.
	// Used for sub-provider routing (e.g. Qwen thinking models within Aliyun,
	// reasoning models within OpenAI). Default: true.
	Matches(model string) bool
	// Thinking is how this provider encodes ChatOptions.Thinking. Default: none.
	Thinking() ThinkingStrategy
	// ShapeRequest applies in-place parameter quirks to the standard request
	// (stripping unsupported fields, pinning temperature, …). Default: noop.
	ShapeRequest(req *openai.ChatCompletionRequest, opts *ChatOptions, isStream bool)
	// TransformMessages rewrites the converted messages (e.g. downgrading
	// multi-content to plain text). Default: identity.
	TransformMessages(msgs []openai.ChatCompletionMessage) []openai.ChatCompletionMessage
	// Endpoint overrides the request URL. Empty string means the standard
	// "<baseURL>/chat/completions" handled by the caller. Default: "".
	Endpoint(baseURL, modelID string, isStream bool) string
	// Auth sets authentication headers on a raw HTTP request. Default: Bearer.
	Auth(req *http.Request, creds authCreds, body []byte)
	// ForceRawHTTP forces the raw HTTP path even when the body is standard
	// (needed by providers that must sign the exact request bytes). Default: false.
	ForceRawHTTP() bool
	// ExtractToolCallMetadata captures provider-specific state from a raw
	// OpenAI-compatible tool_call object. Default: nil.
	ExtractToolCallMetadata(raw json.RawMessage) types.ToolCallMetadata
	// InjectToolCallMetadata writes provider-specific state back into an outbound
	// OpenAI-compatible tool_call object. Default: noop.
	InjectToolCallMetadata(toolCall map[string]any, metadata types.ToolCallMetadata)
}

// baseProvider supplies the default behavior for every providerAdapter method.
// It is also the fallback returned by resolveProvider for unknown providers:
// Bearer auth, standard endpoint, no thinking, no request shaping.
type baseProvider struct{}

func (baseProvider) Name() provider.ProviderName                                    { return "" }
func (baseProvider) Matches(string) bool                                            { return true }
func (baseProvider) Thinking() ThinkingStrategy                                     { return noThinking{} }
func (baseProvider) ShapeRequest(*openai.ChatCompletionRequest, *ChatOptions, bool) {}
func (baseProvider) TransformMessages(msgs []openai.ChatCompletionMessage) []openai.ChatCompletionMessage {
	return msgs
}
func (baseProvider) Endpoint(string, string, bool) string { return "" }
func (baseProvider) Auth(req *http.Request, creds authCreds, _ []byte) {
	req.Header.Set("Authorization", "Bearer "+creds.APIKey)
}
func (baseProvider) ForceRawHTTP() bool { return false }
func (baseProvider) ExtractToolCallMetadata(json.RawMessage) types.ToolCallMetadata {
	return nil
}
func (baseProvider) InjectToolCallMetadata(map[string]any, types.ToolCallMetadata) {}

// --- WeKnoraCloud: custom endpoint + request signing + multi-content downgrade ---

type weKnoraCloudProvider struct{ baseProvider }

func (weKnoraCloudProvider) Name() provider.ProviderName { return provider.ProviderWeKnoraCloud }

func (weKnoraCloudProvider) Endpoint(baseURL, _ string, _ bool) string {
	return strings.TrimRight(baseURL, "/") + "/api/v1/chat/completions"
}

func (weKnoraCloudProvider) ForceRawHTTP() bool { return true }

func (weKnoraCloudProvider) Auth(req *http.Request, creds authCreds, body []byte) {
	requestID := uuid.NewString()
	headers := modelutils.Sign(creds.AppID, creds.AppSecret, requestID, string(body))
	for k, v := range headers {
		req.Header.Set(k, v)
	}
}

// TransformMessages downgrades MultiContent to plain text while preserving
// tool_calls / tool_call_id / name so the function-calling protocol keeps working.
func (weKnoraCloudProvider) TransformMessages(messages []openai.ChatCompletionMessage) []openai.ChatCompletionMessage {
	result := make([]openai.ChatCompletionMessage, 0, len(messages))
	for _, m := range messages {
		msg := m
		if msg.Content == "" && len(msg.MultiContent) > 0 {
			var textParts []string
			for _, part := range msg.MultiContent {
				if part.Type == openai.ChatMessagePartTypeText && part.Text != "" {
					textParts = append(textParts, part.Text)
				}
			}
			msg.Content = strings.Join(textParts, "\n")
			msg.MultiContent = nil
		}
		result = append(result, msg)
	}
	return result
}

// --- Aliyun Qwen thinking models: enable_thinking (always sent, forced off non-stream) ---

type qwenThinkingProvider struct{ baseProvider }

func (qwenThinkingProvider) Name() provider.ProviderName { return provider.ProviderAliyun }
func (qwenThinkingProvider) Matches(model string) bool   { return provider.IsQwenThinkingModel(model) }
func (qwenThinkingProvider) Thinking() ThinkingStrategy {
	return enableThinking{alwaysSend: true, disableOnNonStream: true}
}

// --- LKEAP: thinking via { "thinking": { "type": ... } }, only for DeepSeek V3.x ---
// R1 series enables chain-of-thought by default and is left untouched (falls
// back to baseProvider). See https://cloud.tencent.com/document/product/1772/115963

type lkeapProvider struct{ baseProvider }

func (lkeapProvider) Name() provider.ProviderName { return provider.ProviderLKEAP }
func (lkeapProvider) Matches(model string) bool {
	return strings.Contains(strings.ToLower(model), "deepseek-v3")
}
func (lkeapProvider) Thinking() ThinkingStrategy { return thinkingTypeField{} }

// --- DeepSeek: does not support tool_choice ---

type deepseekProvider struct{ baseProvider }

func (deepseekProvider) Name() provider.ProviderName { return provider.ProviderDeepSeek }
func (deepseekProvider) ShapeRequest(req *openai.ChatCompletionRequest, opts *ChatOptions, _ bool) {
	if opts != nil && opts.ToolChoice != "" {
		req.ToolChoice = nil
	}
}

// --- Generic (vLLM) / NVIDIA: thinking via chat_template_kwargs ---

type genericProvider struct{ baseProvider }

func (genericProvider) Name() provider.ProviderName { return provider.ProviderGeneric }
func (genericProvider) Thinking() ThinkingStrategy  { return chatTemplateKwargs{} }

type nvidiaProvider struct{ baseProvider }

func (nvidiaProvider) Name() provider.ProviderName { return provider.ProviderNvidia }
func (nvidiaProvider) Thinking() ThinkingStrategy  { return chatTemplateKwargs{} }

// --- Gemini OpenAI compatibility: tool thought signatures live in extra_content ---

type geminiProvider struct{ baseProvider }

func (geminiProvider) Name() provider.ProviderName { return provider.ProviderGemini }
func (geminiProvider) ForceRawHTTP() bool          { return true }
func (geminiProvider) ExtractToolCallMetadata(raw json.RawMessage) types.ToolCallMetadata {
	var tc struct {
		ExtraContent map[string]json.RawMessage `json:"extra_content,omitempty"`
	}
	if err := json.Unmarshal(raw, &tc); err != nil {
		return nil
	}
	google, ok := tc.ExtraContent["google"]
	if !ok || len(google) == 0 {
		return nil
	}
	return types.ToolCallMetadata{"google": google}
}
func (geminiProvider) InjectToolCallMetadata(toolCall map[string]any, metadata types.ToolCallMetadata) {
	if len(metadata) == 0 {
		return
	}
	google, ok := metadata["google"]
	if !ok || len(google) == 0 {
		return
	}
	var googleValue any
	if err := json.Unmarshal(google, &googleValue); err != nil {
		return
	}
	toolCall["extra_content"] = map[string]any{"google": googleValue}
}

// --- Volcengine (火山引擎 Ark): thinking via { "thinking": { "type": ... } } ---

type volcengineProvider struct{ baseProvider }

func (volcengineProvider) Name() provider.ProviderName { return provider.ProviderVolcengine }
func (volcengineProvider) Thinking() ThinkingStrategy  { return thinkingTypeField{} }

// --- Azure OpenAI: api-key auth (reasoning variant also strips sampling params) ---

type azureProvider struct{ baseProvider }

func (azureProvider) Name() provider.ProviderName { return provider.ProviderAzureOpenAI }
func (azureProvider) Auth(req *http.Request, creds authCreds, _ []byte) {
	req.Header.Set("api-key", creds.APIKey)
}

type azureReasoningProvider struct{ azureProvider }

func (azureReasoningProvider) Matches(model string) bool {
	return provider.IsOpenAIReasoningOrGPT5Model(model)
}
func (azureReasoningProvider) ShapeRequest(req *openai.ChatCompletionRequest, _ *ChatOptions, _ bool) {
	shapeOpenAIReasoning(req)
}

// --- OpenAI reasoning / GPT-5: no sampling params, must use max_completion_tokens ---

type openAIReasoningProvider struct{ baseProvider }

func (openAIReasoningProvider) Name() provider.ProviderName { return provider.ProviderOpenAI }
func (openAIReasoningProvider) Matches(model string) bool {
	return provider.IsOpenAIReasoningOrGPT5Model(model)
}
func (openAIReasoningProvider) ShapeRequest(req *openai.ChatCompletionRequest, _ *ChatOptions, _ bool) {
	shapeOpenAIReasoning(req)
}

// --- Moonshot: v1 models accept only temperature=1 ---

type moonshotProvider struct{ baseProvider }

func (moonshotProvider) Name() provider.ProviderName { return provider.ProviderMoonshot }
func (moonshotProvider) Matches(model string) bool {
	return provider.IsMoonshotFixedTempModel(model)
}
func (moonshotProvider) ShapeRequest(req *openai.ChatCompletionRequest, _ *ChatOptions, _ bool) {
	// Pin temperature to 1 and drop the other sampling params, matching the
	// pre-refactor behavior where these fields were never set for this model.
	req.Temperature = 1
	req.TopP = 0
	req.FrequencyPenalty = 0
	req.PresencePenalty = 0
}

// shapeOpenAIReasoning strips sampling params (unsupported by o-series / GPT-5)
// and migrates max_tokens to max_completion_tokens. See issue #1283.
func shapeOpenAIReasoning(req *openai.ChatCompletionRequest) {
	req.Temperature = 0
	req.TopP = 0
	req.FrequencyPenalty = 0
	req.PresencePenalty = 0
	if req.MaxCompletionTokens == 0 && req.MaxTokens > 0 {
		req.MaxCompletionTokens = req.MaxTokens
	}
	req.MaxTokens = 0
}

// providerRegistry is ordered: more specific adapters (those with a real
// Matches predicate) must precede the generic catch-all for the same provider.
var providerRegistry = []providerAdapter{
	weKnoraCloudProvider{},
	qwenThinkingProvider{},
	lkeapProvider{},
	deepseekProvider{},
	genericProvider{},
	geminiProvider{},
	volcengineProvider{},
	nvidiaProvider{},
	azureReasoningProvider{},
	azureProvider{},
	openAIReasoningProvider{},
	moonshotProvider{},
}

// resolveProvider returns the adapter handling the given provider+model, or
// baseProvider{} (Bearer auth, standard endpoint, no thinking) when none matches.
func resolveProvider(name provider.ProviderName, model string) providerAdapter {
	for _, p := range providerRegistry {
		if p.Name() == name && p.Matches(model) {
			return p
		}
	}
	return baseProvider{}
}
