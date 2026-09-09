package provider

import "github.com/Tencent/WeKnora/internal/types"

// Level is the platform thinking-level vocabulary (五档词表). Vendors declare a
// subset in ThinkingCaps.SupportedLevels; continuous-budget vendors (Claude /
// Gemini / Qwen) map levels to native numeric budgets inside their adapter
// (design.md §4.3), so the vocabulary stays clean.
type Level string

const (
	LevelLow    Level = "low"
	LevelMedium Level = "medium"
	LevelHigh   Level = "high"
	LevelXHigh  Level = "xhigh"
	LevelMax    Level = "max"
)

// Modality is an input modality a chat model may accept.
type Modality string

const (
	ModalityText  Modality = "text"
	ModalityImage Modality = "image"
	ModalityAudio Modality = "audio"
)

// ProtocolFamily identifies the wire protocol a chat model speaks.
type ProtocolFamily string

const (
	ProtocolOpenAIChat        ProtocolFamily = "openai_chat"
	ProtocolAnthropicMessages ProtocolFamily = "anthropic_messages"
	ProtocolGoogleGenai       ProtocolFamily = "google_genai"
	ProtocolOllama            ProtocolFamily = "ollama"
)

// UsageReporting describes how completely a provider returns token usage.
// Declared even though its consumer (estimator calibration, proposal 5.3.1) is
// deferred (ADR 0003) — near-zero cost now, avoids a second pass later.
type UsageReporting string

const (
	UsageFull    UsageReporting = "full"
	UsagePartial UsageReporting = "partial"
	UsageNone    UsageReporting = "none"
)

// ThinkingCaps declares a vendor's extended-thinking capability. This is the
// provider-level upper bound; per-model selection (catalog hit or user pick)
// narrows it (ADR 0002). When Supported is false, SupportedLevels may be empty
// and the frontend hides both the toggle and the level control.
type ThinkingCaps struct {
	Supported       bool    `json:"supported"`                  // can thinking be controlled at all
	CanDisable      bool    `json:"can_disable"`                // can it be turned off (false for forced-thinking models)
	SupportedLevels []Level `json:"supported_levels,omitempty"` // subset of the platform vocabulary; dropdown options
	DefaultLevel    Level   `json:"default_level,omitempty"`    // provider-level fallback when the user leaves it empty
}

// ModelListingCaps declares whether the adapter implements remote model listing
// (ModelLister). Default false; proposal 5.10 turns it on per adapter.
type ModelListingCaps struct {
	Supported bool `json:"supported"`
}

// CommonCaps holds capabilities shared by every model type.
type CommonCaps struct {
	Streaming      bool             `json:"streaming"`
	HealthProbe    bool             `json:"health_probe"`
	TokenCounting  bool             `json:"token_counting"`
	UsageReporting UsageReporting   `json:"usage_reporting"`
	ModelListing   ModelListingCaps `json:"model_listing"`
}

// ChatCaps holds chat-specific capabilities (KnowledgeQA + VLLM share this).
type ChatCaps struct {
	Thinking          ThinkingCaps   `json:"thinking"`
	InputModalities   []Modality     `json:"input_modalities,omitempty"` // provider-level upper bound; model-level narrows
	Protocol          ProtocolFamily `json:"protocol"`
	ParallelToolCalls bool           `json:"parallel_tool_calls"`
}

// EmbeddingCaps holds embedding-specific capabilities. CanOverrideDimension
// absorbs the former EmbeddingParameters.supports_dimension_override (design §2).
type EmbeddingCaps struct {
	CanOverrideDimension bool  `json:"can_override_dimension"`
	Dimensions           []int `json:"dimensions,omitempty"`
	MaxBatchSize         int   `json:"max_batch_size,omitempty"`
}

// RerankCaps holds rerank-specific capabilities.
type RerankCaps struct {
	MaxDocuments int     `json:"max_documents,omitempty"`
	MinScore     float64 `json:"min_score,omitempty"`
	MaxScore     float64 `json:"max_score,omitempty"`
}

// ASRCaps holds ASR-specific capabilities.
type ASRCaps struct {
	AudioFormats []string `json:"audio_formats,omitempty"`
	Languages    []string `json:"languages,omitempty"`
	Streaming    bool     `json:"streaming"`
}

// Capabilities is the per-type capability declaration carried by ProviderInfo.
// Shards are nil when the provider does not serve that model type, mirroring the
// parameter sharding in ModelParameters (design §2). The frontend renders only
// the shard for the model's type.
type Capabilities struct {
	Common    CommonCaps     `json:"common"`
	Chat      *ChatCaps      `json:"chat,omitempty"`
	Embedding *EmbeddingCaps `json:"embedding,omitempty"`
	Rerank    *RerankCaps    `json:"rerank,omitempty"`
	ASR       *ASRCaps       `json:"asr,omitempty"`
}

// isZero reports whether c is the unset zero value, used by
// ProviderInfo.EffectiveCapabilities to decide explicit-vs-synthesized.
// Capabilities is comparable (no slice/map fields), so a plain == works.
func (c Capabilities) isZero() bool {
	return c == Capabilities{}
}

// protocolFor maps a provider to its chat wire protocol.
func protocolFor(name ProviderName) ProtocolFamily {
	switch name {
	case ProviderAnthropic:
		return ProtocolAnthropicMessages
	case ProviderGemini:
		return ProtocolGoogleGenai
	default:
		return ProtocolOpenAIChat
	}
}

// thinkingCapsFor returns the provider-level thinking declaration. Values track
// the vendor HTTP expressions in design.md §4.3 / proposal §5.4. Providers
// absent here do not expose thinking at the provider level; model-level catalog
// hits or user selection may still enable it per ADR 0002.
func thinkingCapsFor(name ProviderName) ThinkingCaps {
	standard := ThinkingCaps{
		Supported:       true,
		CanDisable:      true,
		SupportedLevels: []Level{LevelLow, LevelMedium, LevelHigh},
		DefaultLevel:    LevelMedium,
	}
	switch name {
	case ProviderOpenAI, ProviderAzureOpenAI, // reasoning_effort
		ProviderAnthropic, // thinking.budget_tokens (mapped in adapter)
		ProviderGemini,    // thinkingConfig.thinkingBudget
		ProviderAliyun,    // qwen thinking_budget / enable_thinking
		ProviderDeepSeek,  // R1 forced-thinking is a model-level CanDisable=false override
		ProviderLKEAP:     // thinking.type
		return standard
	case ProviderVolcengine: // Ark exposes a higher tier
		return ThinkingCaps{
			Supported:       true,
			CanDisable:      true,
			SupportedLevels: []Level{LevelLow, LevelMedium, LevelHigh, LevelXHigh},
			DefaultLevel:    LevelMedium,
		}
	default:
		return ThinkingCaps{} // Supported=false
	}
}

// defaultCapabilities synthesizes a provider-level Capabilities from the model
// types it serves. Providers may instead set ProviderInfo.Capabilities
// explicitly; EffectiveCapabilities prefers the explicit value.
func defaultCapabilities(name ProviderName, modelTypes []types.ModelType) Capabilities {
	caps := Capabilities{
		Common: CommonCaps{
			Streaming:      true,
			HealthProbe:    true,
			UsageReporting: UsageFull,
		},
	}
	serves := func(t types.ModelType) bool {
		for _, mt := range modelTypes {
			if mt == t {
				return true
			}
		}
		return false
	}
	if serves(types.ModelTypeKnowledgeQA) || serves(types.ModelTypeVLLM) {
		caps.Chat = &ChatCaps{
			Thinking:          thinkingCapsFor(name),
			InputModalities:   []Modality{ModalityText},
			Protocol:          protocolFor(name),
			ParallelToolCalls: true,
		}
	}
	if serves(types.ModelTypeEmbedding) {
		caps.Embedding = &EmbeddingCaps{}
	}
	if serves(types.ModelTypeRerank) {
		caps.Rerank = &RerankCaps{}
	}
	if serves(types.ModelTypeASR) {
		caps.ASR = &ASRCaps{}
	}
	return caps
}
