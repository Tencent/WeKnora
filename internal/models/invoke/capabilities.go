package invoke

import "github.com/Tencent/WeKnora/internal/types"

// Level is the platform thinking-level vocabulary (五档词表). Vendors declare a
// subset in ThinkingCaps.SupportedLevels; continuous-budget vendors (Claude /
// Gemini / Qwen) map levels to native numeric budgets inside their adapter
// (design.md §4.3), so the vocabulary stays clean.
type Level string

// Level values — the platform thinking-level vocabulary (五档词表).
const (
	LevelLow    Level = "low"
	LevelMedium Level = "medium"
	LevelHigh   Level = "high"
	LevelXHigh  Level = "xhigh"
	LevelMax    Level = "max"
)

// Modality is an input modality a chat model may accept.
type Modality string

// Modality values — input modalities a chat model may declare.
const (
	ModalityText  Modality = "text"
	ModalityImage Modality = "image"
	ModalityAudio Modality = "audio"
)

// ProtocolFamily identifies the wire protocol a chat model speaks.
type ProtocolFamily string

// ProtocolFamily values — the wire protocols a chat model can speak.
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

// UsageReporting values — how completely a provider reports token usage.
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
	CanDisable      bool    `json:"can_disable"`                // can thinking be turned off (forced-thinking: false)
	SupportedLevels []Level `json:"supported_levels,omitempty"` // platform vocabulary subset; dropdown options
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
	InputModalities   []Modality     `json:"input_modalities,omitempty"` // provider-level bound; models narrow
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

// CredentialFieldSpec declares one credential slot a provider needs (design
// v2 §6.8). The frontend renders credential forms from this spec — zero
// vendor hardcoding in UI or storage. Key aligns with the three fixed
// ModelParameters slots (api_key/app_id/app_secret); semantics are the
// adapter's business (api_key covers Bearer/x-api-key/api-key style tokens).
type CredentialFieldSpec struct {
	Key      string `json:"key"`
	Required bool   `json:"required"`
	// LabelKey is the frontend i18n key for the field label.
	LabelKey string `json:"label_key,omitempty"`
}

// Credential slot keys — fixed storage vocabulary (design §6.8).
const (
	CredentialKeyAPIKey    = "api_key"
	CredentialKeyAppID     = "app_id"
	CredentialKeyAppSecret = "app_secret"
)

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
	// Credentials declares the credential slots the provider needs (§6.8);
	// the frontend renders its credential form from this spec.
	Credentials []CredentialFieldSpec `json:"credentials,omitempty"`
}

// isZero reports whether c is the unset zero value, used by
// ProviderInfo.EffectiveCapabilities to decide explicit-vs-synthesized.
// Credentials (a slice) breaks plain ==, so compare field-wise.
func (c Capabilities) isZero() bool {
	return c.Common == CommonCaps{} && c.Chat == nil && c.Embedding == nil &&
		c.Rerank == nil && c.ASR == nil && len(c.Credentials) == 0
}

// credentialsFor returns the provider-level credential declaration (§6.8):
// openai-family/anthropic take a required api_key; self-hosted/custom
// deployments (generic, ollama) take an optional one; weknoracloud declares
// NO per-model credentials — its credentials live in space-level settings
// with tenant fallback (empty slice, not nil, so the DTO shows `[]`).
// lkeap/volcengine additionally take an optional app_secret: their rerank
// facades are Tencent/Volcengine AK/SK signed (SecretId=api_key,
// SecretKey=app_secret; missing → constructor error at call time, see
// rerank/lkeap_reranker.go:44 / rerank/volcengine_reranker.go). Chat/embedding
// paths need only api_key, hence optional at the provider-level spec.
func credentialsFor(name ProviderName) []CredentialFieldSpec {
	switch name {
	case ProviderWeKnoraCloud:
		return []CredentialFieldSpec{}
	case ProviderGeneric:
		// 自定义/自部署（含本地 Ollama 记录，P1c 构造点映射后归入 generic/
		// ollama 适配器）：凭证可空（匿名可达的服务）。
		return []CredentialFieldSpec{{Key: CredentialKeyAPIKey}}
	case ProviderLKEAP, ProviderVolcengine:
		return []CredentialFieldSpec{
			{Key: CredentialKeyAPIKey, Required: true, LabelKey: "model.credentials.apiKey"},
			{Key: CredentialKeyAppSecret, LabelKey: "model.editor.appSecretLabel"},
		}
	default:
		return []CredentialFieldSpec{{Key: CredentialKeyAPIKey, Required: true, LabelKey: "model.credentials.apiKey"}}
	}
}

// protocolFor maps a provider to its chat wire protocol.
func protocolFor(name ProviderName) ProtocolFamily {
	switch name {
	case ProviderAnthropic:
		return ProtocolAnthropicMessages
	default:
		// gemini 亦落此处：走官方 OpenAI 兼容层（裁定 #31），wire 即 OpenAI Chat 形态。
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
	caps.Credentials = credentialsFor(name)
	return caps
}
