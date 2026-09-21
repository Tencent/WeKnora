package catalog

import (
	"encoding/json"
	"fmt"
	"reflect"

	"github.com/Tencent/WeKnora/internal/models/api"
)

// ThinkingFormat names how an OpenAI-compatible vendor encodes the thinking
// switch. It is the single most divergent field across vendors, which is
// why it is an enum rather than free text.
type ThinkingFormat string

const (
	// ThinkingFormatNone sends no thinking fields at all.
	ThinkingFormatNone ThinkingFormat = "none"
	// ThinkingFormatOpenAI sends only reasoning_effort (OpenAI, Azure, xAI).
	ThinkingFormatOpenAI ThinkingFormat = "openai"
	// ThinkingFormatThinkingType sends {"thinking": {"type": "enabled"|"disabled"}}
	// plus reasoning_effort when supported (DeepSeek, Zhipu, Volcengine Ark,
	// Moonshot K2.6+, MiniMax M3, LKEAP).
	ThinkingFormatThinkingType ThinkingFormat = "thinking-type"
	// ThinkingFormatEnableThinking sends enable_thinking: bool plus
	// thinking_budget (DashScope, SiliconFlow, Hunyuan).
	ThinkingFormatEnableThinking ThinkingFormat = "enable-thinking"
	// ThinkingFormatChatTemplateKwargs sends chat_template_kwargs.enable_thinking
	// (vLLM, SGLang, NIM, LiteLLM in front of them).
	ThinkingFormatChatTemplateKwargs ThinkingFormat = "chat-template-kwargs"
	// ThinkingFormatOpenRouter sends {"reasoning": {"effort"|"enabled"}}.
	ThinkingFormatOpenRouter ThinkingFormat = "openrouter"
)

// OpenAICompletionsCompat is the overlay form (every field optional) of the
// Chat Completions settings. JSON keys are the documented names used in
// models.json and config/models.json.
type OpenAICompletionsCompat struct {
	MaxTokensField               *string         `json:"max_tokens_field,omitempty"`
	ThinkingFormat               *ThinkingFormat `json:"thinking_format,omitempty"`
	ThinkingEnabledValue         *string         `json:"thinking_enabled_value,omitempty"`
	ThinkingAlwaysSend           *bool           `json:"thinking_always_send,omitempty"`
	ThinkingDisableOnNonStream   *bool           `json:"thinking_disable_on_non_stream,omitempty"`
	ThinkingBudgetField          *string         `json:"thinking_budget_field,omitempty"`
	ThinkingBudgetExcludesEffort *bool           `json:"thinking_budget_excludes_effort,omitempty"`
	ReasoningEffortField         *string         `json:"reasoning_effort_field,omitempty"`
	SupportsReasoningEffort      *bool           `json:"supports_reasoning_effort,omitempty"`
	SupportsDeveloperRole        *bool           `json:"supports_developer_role,omitempty"`
	SupportsStore                *bool           `json:"supports_store,omitempty"`
	SupportsUsageInStreaming     *bool           `json:"supports_usage_in_streaming,omitempty"`
	SupportsTemperature          *bool           `json:"supports_temperature,omitempty"`
	FixedTemperature             *float64        `json:"fixed_temperature,omitempty"`
	SupportsSeed                 *bool           `json:"supports_seed,omitempty"`
	ToolChoiceModes              []string        `json:"tool_choice_modes,omitempty"`
	SupportsParallelToolCalls    *bool           `json:"supports_parallel_tool_calls,omitempty"`
	SupportsResponseFormat       *bool           `json:"supports_response_format,omitempty"`
	SupportsMultiContent         *bool           `json:"supports_multi_content,omitempty"`
	ReplayReasoningContent       *bool           `json:"replay_reasoning_content,omitempty"`
	PromptCacheKey               *bool           `json:"prompt_cache_key,omitempty"`
	CacheControlFormat           *string         `json:"cache_control_format,omitempty"`
	PromptCacheAccounting        *bool           `json:"prompt_cache_accounting,omitempty"`
	ToolCallExtraFields          []string        `json:"tool_call_extra_fields,omitempty"`
	ExtraBody                    map[string]any  `json:"extra_body,omitempty"`
	ReasoningFields              []string        `json:"reasoning_fields,omitempty"`
}

// OpenAICompletionsSettings is the resolved (fully defaulted) form.
type OpenAICompletionsSettings struct {
	// MaxTokensField is "max_tokens" or "max_completion_tokens".
	MaxTokensField string
	ThinkingFormat ThinkingFormat
	// ThinkingEnabledValue is the "type" sent for thinking-type when enabled
	// ("enabled" by default; MiniMax M3 uses "adaptive").
	ThinkingEnabledValue string
	// ThinkingAlwaysSend pins the switch on every request even when the
	// caller expressed no preference (DashScope hybrid models require it).
	ThinkingAlwaysSend bool
	// ThinkingDisableOnNonStream forces the switch off for non-streaming
	// calls (Qwen3 rejects thinking without stream).
	ThinkingDisableOnNonStream bool
	// ThinkingBudgetField carries Options.ThinkingBudgetTokens ("thinking_budget").
	ThinkingBudgetField string
	// ThinkingBudgetExcludesEffort marks a vendor that rejects a request
	// carrying both the budget and the effort (DashScope's qwen3.8-max
	// series: "不支持 reasoning_effort 与 thinking_budget 同时设置，同时设置
	// 会报错"). The graded level is what the caller actually picked, so the
	// budget is the field that yields.
	ThinkingBudgetExcludesEffort bool
	// ReasoningEffortField is "reasoning_effort" unless a vendor renames it.
	ReasoningEffortField    string
	SupportsReasoningEffort bool
	// SupportsDeveloperRole switches the system prompt to the developer role
	// on reasoning models.
	SupportsDeveloperRole bool
	// SupportsStore sends store:false (OpenAI keeps completions otherwise).
	SupportsStore            bool
	SupportsUsageInStreaming bool
	// SupportsTemperature false drops every sampling parameter (o-series,
	// GPT-5 family, Kimi K2.5+).
	SupportsTemperature bool
	// FixedTemperature pins the value when the vendor only accepts one.
	FixedTemperature          *float64
	SupportsSeed              bool
	ToolChoiceModes           []string
	SupportsParallelToolCalls bool
	SupportsResponseFormat    bool
	// SupportsMultiContent false flattens image/text parts to plain text.
	SupportsMultiContent bool
	// ReplayReasoningContent sends reasoning_content on prior assistant turns.
	ReplayReasoningContent bool
	// PromptCacheKey sends prompt_cache_key / prompt_cache_retention and the
	// session-affinity headers.
	PromptCacheKey bool
	// CacheControlFormat "anthropic" injects cache_control breakpoints.
	CacheControlFormat string
	// PromptCacheAccounting states the vendor reports cache usage at all.
	PromptCacheAccounting bool
	// ToolCallExtraFields lists tool_call keys to round-trip opaquely
	// (Gemini-through-OpenAI "extra_content").
	ToolCallExtraFields []string
	// ExtraBody is merged into every request body (vendor knobs such as
	// enable_search, sampling defaults).
	ExtraBody map[string]any
	// ReasoningFields are the delta keys that carry thinking text, in
	// priority order.
	ReasoningFields []string
}

// DefaultOpenAICompletions is the protocol baseline: plain OpenAI semantics.
func DefaultOpenAICompletions() OpenAICompletionsSettings {
	return OpenAICompletionsSettings{
		MaxTokensField:            "max_completion_tokens",
		ThinkingFormat:            ThinkingFormatNone,
		ThinkingEnabledValue:      "enabled",
		ReasoningEffortField:      "reasoning_effort",
		SupportsUsageInStreaming:  true,
		SupportsTemperature:       true,
		SupportsSeed:              true,
		ToolChoiceModes:           []string{"none", "auto", "required", "function"},
		SupportsParallelToolCalls: true,
		SupportsResponseFormat:    true,
		SupportsMultiContent:      true,
		ReplayReasoningContent:    true,
		ReasoningFields:           []string{"reasoning_content", "reasoning", "reasoning_text"},
	}
}

// AllowsToolChoice reports whether a tool_choice mode is accepted.
func (s OpenAICompletionsSettings) AllowsToolChoice(mode string) bool {
	for _, m := range s.ToolChoiceModes {
		if m == mode {
			return true
		}
	}
	return false
}

// OpenAIResponsesCompat is the overlay for the Responses protocol.
type OpenAIResponsesCompat struct {
	SupportsDeveloperRole      *bool          `json:"supports_developer_role,omitempty"`
	SupportsMaxOutputTokens    *bool          `json:"supports_max_output_tokens,omitempty"`
	SupportsReasoningSummary   *bool          `json:"supports_reasoning_summary,omitempty"`
	SupportsEncryptedReasoning *bool          `json:"supports_encrypted_reasoning,omitempty"`
	SupportsStore              *bool          `json:"supports_store,omitempty"`
	SupportsTemperature        *bool          `json:"supports_temperature,omitempty"`
	PromptCacheKey             *bool          `json:"prompt_cache_key,omitempty"`
	SupportsLongCacheRetention *bool          `json:"supports_long_cache_retention,omitempty"`
	SupportsParallelToolCalls  *bool          `json:"supports_parallel_tool_calls,omitempty"`
	ExtraBody                  map[string]any `json:"extra_body,omitempty"`
}

// OpenAIResponsesSettings is the resolved Responses configuration.
type OpenAIResponsesSettings struct {
	SupportsDeveloperRole      bool
	SupportsMaxOutputTokens    bool
	SupportsReasoningSummary   bool
	SupportsEncryptedReasoning bool
	SupportsStore              bool
	SupportsTemperature        bool
	PromptCacheKey             bool
	SupportsLongCacheRetention bool
	SupportsParallelToolCalls  bool
	ExtraBody                  map[string]any
}

// DefaultOpenAIResponses is the api.openai.com baseline.
func DefaultOpenAIResponses() OpenAIResponsesSettings {
	return OpenAIResponsesSettings{
		SupportsDeveloperRole:      true,
		SupportsMaxOutputTokens:    true,
		SupportsReasoningSummary:   true,
		SupportsEncryptedReasoning: true,
		SupportsStore:              true,
		SupportsTemperature:        true,
		PromptCacheKey:             true,
		SupportsLongCacheRetention: true,
		SupportsParallelToolCalls:  true,
	}
}

// AnthropicThinkingMode selects the thinking request shape.
type AnthropicThinkingMode string

const (
	// AnthropicThinkingBudget sends {"type":"enabled","budget_tokens":N}.
	AnthropicThinkingBudget AnthropicThinkingMode = "budget"
	// AnthropicThinkingAdaptive sends {"type":"adaptive"} and steers the
	// intensity through output_config.effort.
	AnthropicThinkingAdaptive AnthropicThinkingMode = "adaptive"
)

// AnthropicMessagesCompat is the overlay for the Messages protocol.
type AnthropicMessagesCompat struct {
	ThinkingMode               *AnthropicThinkingMode      `json:"thinking_mode,omitempty"`
	SupportsEffort             *bool                       `json:"supports_effort,omitempty"`
	ThinkingBudgets            map[api.ReasoningEffort]int `json:"thinking_budgets,omitempty"`
	DefaultMaxTokens           *int                        `json:"default_max_tokens,omitempty"`
	SupportsTemperature        *bool                       `json:"supports_temperature,omitempty"`
	TemperatureWithThinking    *bool                       `json:"temperature_with_thinking,omitempty"`
	SupportsTopP               *bool                       `json:"supports_top_p,omitempty"`
	SupportsCacheControl       *bool                       `json:"supports_cache_control,omitempty"`
	SupportsCacheControlOnTool *bool                       `json:"supports_cache_control_on_tools,omitempty"`
	LongCacheTTL               *string                     `json:"long_cache_ttl,omitempty"`
	Version                    *string                     `json:"version,omitempty"`
	BetaHeaders                []string                    `json:"beta_headers,omitempty"`
	InterleavedThinkingBeta    *string                     `json:"interleaved_thinking_beta,omitempty"`
	PromptCacheAccounting      *bool                       `json:"prompt_cache_accounting,omitempty"`
	ExtraBody                  map[string]any              `json:"extra_body,omitempty"`
}

// AnthropicMessagesSettings is the resolved Messages configuration.
type AnthropicMessagesSettings struct {
	ThinkingMode   AnthropicThinkingMode
	SupportsEffort bool
	// ThinkingBudgets maps graded levels to budget_tokens for budget mode.
	ThinkingBudgets map[api.ReasoningEffort]int
	// DefaultMaxTokens is used when the caller set no budget (max_tokens is
	// mandatory on this protocol).
	DefaultMaxTokens           int
	SupportsTemperature        bool
	TemperatureWithThinking    bool
	SupportsTopP               bool
	SupportsCacheControl       bool
	SupportsCacheControlOnTool bool
	LongCacheTTL               string
	Version                    string
	BetaHeaders                []string
	// InterleavedThinkingBeta is added to anthropic-beta when thinking and
	// tools are both active (budget mode only). Empty disables it.
	InterleavedThinkingBeta string
	PromptCacheAccounting   bool
	ExtraBody               map[string]any
}

// DefaultAnthropicMessages is the api.anthropic.com baseline for the
// budget-thinking generation of models.
func DefaultAnthropicMessages() AnthropicMessagesSettings {
	return AnthropicMessagesSettings{
		ThinkingMode: AnthropicThinkingBudget,
		ThinkingBudgets: map[api.ReasoningEffort]int{
			api.ReasoningMinimal: 1024,
			api.ReasoningLow:     2048,
			api.ReasoningMedium:  8192,
			api.ReasoningHigh:    16384,
			api.ReasoningXHigh:   32768,
			api.ReasoningMax:     63999,
		},
		DefaultMaxTokens:           4096,
		SupportsTemperature:        true,
		TemperatureWithThinking:    false,
		SupportsTopP:               true,
		SupportsCacheControl:       true,
		SupportsCacheControlOnTool: true,
		LongCacheTTL:               "1h",
		Version:                    "2023-06-01",
		InterleavedThinkingBeta:    "interleaved-thinking-2025-05-14",
		PromptCacheAccounting:      true,
	}
}

// GoogleThinkingMode selects the thinkingConfig shape.
type GoogleThinkingMode string

const (
	// GoogleThinkingBudget sends thinkingConfig.thinkingBudget (Gemini 2.5).
	GoogleThinkingBudget GoogleThinkingMode = "budget"
	// GoogleThinkingLevel sends thinkingConfig.thinkingLevel (Gemini 3+).
	GoogleThinkingLevel GoogleThinkingMode = "level"
	// GoogleThinkingNone sends no thinkingConfig.
	GoogleThinkingNone GoogleThinkingMode = "none"
)

// GoogleGenerativeAICompat is the overlay for the Gemini protocol.
type GoogleGenerativeAICompat struct {
	ThinkingMode     *GoogleThinkingMode         `json:"thinking_mode,omitempty"`
	ThinkingBudgets  map[api.ReasoningEffort]int `json:"thinking_budgets,omitempty"`
	IncludeThoughts  *bool                       `json:"include_thoughts,omitempty"`
	SupportsSeed     *bool                       `json:"supports_seed,omitempty"`
	SupportsPenalty  *bool                       `json:"supports_penalty,omitempty"`
	ExtraGeneration  map[string]any              `json:"extra_generation_config,omitempty"`
	PromptCacheAcct  *bool                       `json:"prompt_cache_accounting,omitempty"`
	APIVersionPrefix *string                     `json:"api_version_prefix,omitempty"`
}

// GoogleGenerativeAISettings is the resolved Gemini configuration.
type GoogleGenerativeAISettings struct {
	ThinkingMode    GoogleThinkingMode
	ThinkingBudgets map[api.ReasoningEffort]int
	IncludeThoughts bool
	SupportsSeed    bool
	SupportsPenalty bool
	ExtraGeneration map[string]any
	PromptCacheAcct bool
	// APIVersionPrefix is appended to a bare host base URL ("/v1beta").
	APIVersionPrefix string
}

// DefaultGoogleGenerativeAI is the generativelanguage.googleapis.com baseline.
func DefaultGoogleGenerativeAI() GoogleGenerativeAISettings {
	return GoogleGenerativeAISettings{
		ThinkingMode: GoogleThinkingBudget,
		ThinkingBudgets: map[api.ReasoningEffort]int{
			api.ReasoningMinimal: 128,
			api.ReasoningLow:     2048,
			api.ReasoningMedium:  8192,
			api.ReasoningHigh:    24576,
			api.ReasoningXHigh:   32768,
			api.ReasoningMax:     32768,
		},
		IncludeThoughts:  true,
		SupportsSeed:     true,
		SupportsPenalty:  true,
		PromptCacheAcct:  true,
		APIVersionPrefix: "/v1beta",
	}
}

// VendorCompat carries a vendor's defaults for every protocol it may speak.
type VendorCompat struct {
	OpenAICompletions  OpenAICompletionsCompat  `json:"openai_completions,omitempty"`
	OpenAIResponses    OpenAIResponsesCompat    `json:"openai_responses,omitempty"`
	AnthropicMessages  AnthropicMessagesCompat  `json:"anthropic_messages,omitempty"`
	GoogleGenerativeAI GoogleGenerativeAICompat `json:"google_generative_ai,omitempty"`
	// Rerank is the rerank protocol overlay. It has no per-protocol variants:
	// a vendor serves exactly one rerank dialect.
	Rerank RerankCompat `json:"rerank,omitempty"`
	// Embeddings is the embedding protocol overlay, likewise one per vendor.
	Embeddings EmbeddingsCompat `json:"embeddings,omitempty"`
	// Transcriptions is the speech-to-text overlay, likewise one per vendor.
	Transcriptions TranscriptionsCompat `json:"transcriptions,omitempty"`
}

// apply writes the set fields of an overlay struct onto a settings struct
// with matching field names, dereferencing pointers. Slices and maps
// replace the default wholesale (no element merge) except map[string]any
// ExtraBody-style fields, which merge key by key.
func apply(settings any, compat any) {
	sv := reflect.ValueOf(settings).Elem()
	cv := reflect.ValueOf(compat).Elem()
	ct := cv.Type()
	for i := 0; i < cv.NumField(); i++ {
		field := ct.Field(i)
		if !field.IsExported() {
			continue
		}
		target := sv.FieldByName(field.Name)
		if !target.IsValid() {
			panic(fmt.Sprintf("catalog: overlay field %s.%s has no settings counterpart", ct.Name(), field.Name))
		}
		f := cv.Field(i)
		switch f.Kind() {
		case reflect.Pointer:
			if f.IsNil() {
				continue
			}
			if target.Kind() == reflect.Pointer {
				target.Set(f)
			} else {
				target.Set(f.Elem())
			}
		case reflect.Slice:
			if f.Len() > 0 {
				target.Set(f)
			}
		case reflect.Map:
			if f.Len() == 0 {
				continue
			}
			if target.IsNil() {
				target.Set(reflect.MakeMap(target.Type()))
			}
			iter := f.MapRange()
			for iter.Next() {
				target.SetMapIndex(iter.Key(), iter.Value())
			}
		}
	}
}

// decodeCompat unmarshals a flat compat object into the overlay type for an
// API. Unknown keys are rejected so typos in models.json surface early.
func decodeCompat(raw json.RawMessage, into any) error {
	if len(raw) == 0 {
		return nil
	}
	dec := json.NewDecoder(bytesReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(into); err != nil {
		return fmt.Errorf("decode compat: %w", err)
	}
	return nil
}
