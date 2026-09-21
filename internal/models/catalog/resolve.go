package catalog

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Tencent/WeKnora/internal/models/api"
	"github.com/Tencent/WeKnora/internal/types"
)

// Extra-config keys honoured by Resolve. They predate the catalog and stay
// supported so existing model rows keep working unchanged.
const (
	// ExtraAPI forces a protocol ("openai-responses", "anthropic-messages", ...).
	ExtraAPI = "api"
	// ExtraRemoteModelName sends a different model id on the wire.
	ExtraRemoteModelName = "remote_model_name"
	// ExtraAPIVersion is the Azure api-version query parameter.
	ExtraAPIVersion = "api_version"
	// ExtraThinkingControl is the legacy thinking encoding selector written
	// by older UIs: none | enable_thinking | thinking_type | chat_template_kwargs.
	ExtraThinkingControl = "thinking_control"
)

// Ref identifies one configured model.
type Ref struct {
	Provider  string
	Model     string
	BaseURL   string
	ModelType types.ModelType
	Extra     map[string]string
	Override  *types.ModelSpecOverride
}

// Resolved is the fully merged view of one model: which vendor, which
// protocol, which settings.
type Resolved struct {
	Vendor *Vendor
	// Spec is the effective catalog entry (synthesized when unknown).
	Spec ModelSpec
	// Cataloged reports whether the model name matched a catalog entry.
	Cataloged bool
	API       api.API
	BaseURL   string
	// RemoteModel is the id sent on the wire.
	RemoteModel    string
	ThinkingLevels api.ThinkingLevelMap

	OpenAICompletions  OpenAICompletionsSettings
	OpenAIResponses    OpenAIResponsesSettings
	AnthropicMessages  AnthropicMessagesSettings
	GoogleGenerativeAI GoogleGenerativeAISettings
}

// Resolve merges the vendor, catalog entry, extra-config and per-row
// overrides for a model reference. It never fails for unknown vendors or
// models: they degrade to the generic OpenAI-compatible baseline.
func Resolve(ref Ref) (*Resolved, error) {
	providerID := strings.ToLower(strings.TrimSpace(ref.Provider))
	if providerID == "" {
		providerID = DetectByURL(ref.BaseURL)
	}
	vendor := GetOrGeneric(providerID)
	if vendor == nil {
		return nil, fmt.Errorf("catalog: no vendors registered")
	}
	modelType := ref.ModelType
	if modelType == "" {
		modelType = types.ModelTypeKnowledgeQA
	}

	baseURL := strings.TrimRight(strings.TrimSpace(ref.BaseURL), "/")
	if baseURL == "" {
		baseURL = strings.TrimRight(vendor.GetDefaultURL(modelType), "/")
	}

	spec, cataloged := vendor.FindModel(ref.Model)
	if !cataloged {
		spec = ModelSpec{ID: ref.Model, Type: types.ModelTypeKnowledgeQA}
	}
	if spec.API == "" {
		spec.API = vendor.API
	}
	if ref.Override != nil {
		applySpecOverride(&spec, ref.Override)
	}

	resolvedAPI := spec.API
	if forced := strings.TrimSpace(ref.Extra[ExtraAPI]); forced != "" {
		resolvedAPI = api.API(forced)
	} else {
		resolvedAPI = inferAPIFromURL(baseURL, resolvedAPI)
		if vendor.PreferAPI != nil {
			if preferred := vendor.PreferAPI(baseURL, spec); preferred != "" {
				resolvedAPI = preferred
			}
		}
	}
	if !resolvedAPI.Known() {
		return nil, fmt.Errorf("catalog: unknown api %q for provider %s", resolvedAPI, vendor.ID)
	}

	levels := api.ThinkingLevelMap{}
	for k, v := range vendor.ThinkingLevels {
		levels[k] = v
	}
	for k, v := range spec.ThinkingLevels {
		levels[k] = v
	}
	if ref.Override != nil {
		for k, v := range ref.Override.ThinkingLevels {
			levels[api.ReasoningEffort(k)] = v
		}
	}

	out := &Resolved{
		Vendor:         vendor,
		Spec:           spec,
		Cataloged:      cataloged,
		API:            resolvedAPI,
		BaseURL:        baseURL,
		RemoteModel:    ref.Model,
		ThinkingLevels: levels,
	}
	if override := strings.TrimSpace(ref.Extra[ExtraRemoteModelName]); override != "" {
		out.RemoteModel = override
	}

	overrideCompat := ref.Override.CompatJSON()

	// Every protocol's settings are resolved so callers can inspect any of
	// them, but only the layers whose API matches contribute model/override
	// compat (a flat compat object is meaningless for another protocol).
	completions := DefaultOpenAICompletions()
	apply(&completions, &vendor.Compat.OpenAICompletions)
	if err := applyRawCompat(
		&completions, &OpenAICompletionsCompat{}, spec.Compat, spec.API, api.APIOpenAICompletions,
	); err != nil {
		return nil, err
	}
	if err := applyRawCompat(
		&completions, &OpenAICompletionsCompat{}, overrideCompat, resolvedAPI, api.APIOpenAICompletions,
	); err != nil {
		return nil, err
	}
	applyLegacyThinkingControl(&completions, ref.Extra[ExtraThinkingControl])
	out.OpenAICompletions = completions

	responses := DefaultOpenAIResponses()
	apply(&responses, &vendor.Compat.OpenAIResponses)
	if err := applyRawCompat(
		&responses, &OpenAIResponsesCompat{}, spec.Compat, spec.API, api.APIOpenAIResponses,
	); err != nil {
		return nil, err
	}
	if err := applyRawCompat(
		&responses, &OpenAIResponsesCompat{}, overrideCompat, resolvedAPI, api.APIOpenAIResponses,
	); err != nil {
		return nil, err
	}
	out.OpenAIResponses = responses

	anthropic := DefaultAnthropicMessages()
	apply(&anthropic, &vendor.Compat.AnthropicMessages)
	if err := applyRawCompat(
		&anthropic, &AnthropicMessagesCompat{}, spec.Compat, spec.API, api.APIAnthropicMessages,
	); err != nil {
		return nil, err
	}
	if err := applyRawCompat(
		&anthropic, &AnthropicMessagesCompat{}, overrideCompat, resolvedAPI, api.APIAnthropicMessages,
	); err != nil {
		return nil, err
	}
	out.AnthropicMessages = anthropic

	google := DefaultGoogleGenerativeAI()
	apply(&google, &vendor.Compat.GoogleGenerativeAI)
	if err := applyRawCompat(
		&google, &GoogleGenerativeAICompat{}, spec.Compat, spec.API, api.APIGoogleGenerativeAI,
	); err != nil {
		return nil, err
	}
	if err := applyRawCompat(
		&google, &GoogleGenerativeAICompat{}, overrideCompat, resolvedAPI, api.APIGoogleGenerativeAI,
	); err != nil {
		return nil, err
	}
	out.GoogleGenerativeAI = google
	// An explicit legacy thinking_control is the operator saying "this row
	// does think, send the switch this way", so it outranks the catalog.
	if strings.TrimSpace(ref.Extra[ExtraThinkingControl]) == "" {
		out.silenceThinkingForNonReasoningModel()
	}

	return out, nil
}

// silenceThinkingForNonReasoningModel drops every thinking field for a
// catalogued model the vendor documents as non-reasoning (DeepSeek's
// non-thinking alias, LKEAP's V3-0324 generation, plain instruct models).
// Without this, a caller that asks for thinking — an agent whose config says
// so, or the model debug drawer — would send a switch the model does not
// understand, and some vendors answer 400.
//
// The gate deliberately applies only to catalogued entries: an unknown model
// on a self-hosted runtime carries Reasoning=false simply because nobody
// described it, and those deployments do rely on the thinking switch.
func (r *Resolved) silenceThinkingForNonReasoningModel() {
	if !r.Cataloged || r.Spec.Reasoning {
		return
	}
	r.OpenAICompletions.ThinkingFormat = ThinkingFormatNone
	r.OpenAICompletions.ThinkingBudgetField = ""
	r.OpenAICompletions.SupportsReasoningEffort = false
	r.GoogleGenerativeAI.ThinkingMode = GoogleThinkingNone
	r.ThinkingLevels = api.ThinkingLevelMap{}
	for _, level := range api.ReasoningLadder {
		r.ThinkingLevels[level] = nil
	}
	r.ThinkingLevels[api.ReasoningAuto] = nil
}

// applyRawCompat decodes raw into the overlay type and applies it when the
// layer's API matches the target protocol.
func applyRawCompat(settings any, overlayPtr any, raw json.RawMessage, layerAPI, target api.API) error {
	if len(raw) == 0 || layerAPI != target {
		return nil
	}
	if err := decodeCompat(raw, overlayPtr); err != nil {
		return fmt.Errorf("%s compat: %w", target, err)
	}
	apply(settings, overlayPtr)
	return nil
}

func applySpecOverride(spec *ModelSpec, o *types.ModelSpecOverride) {
	if o == nil {
		return
	}
	if o.API != "" {
		spec.API = api.API(o.API)
	}
	if o.Reasoning != nil {
		spec.Reasoning = *o.Reasoning
	}
	if len(o.Input) > 0 {
		spec.Input = o.Input
	}
	if o.ContextWindow > 0 {
		spec.ContextWindow = o.ContextWindow
	}
	if o.MaxOutputTokens > 0 {
		spec.MaxOutputTokens = o.MaxOutputTokens
	}
}

// inferAPIFromURL applies the two URL conventions vendors use to expose a
// second protocol on a sub-path: ".../openai" for an OpenAI-compatible
// facade (Gemini, LongCat, Novita) and ".../anthropic" for an Anthropic
// Messages facade (MiniMax, Zhipu, Moonshot, Volcengine coding plans).
func inferAPIFromURL(baseURL string, current api.API) api.API {
	lower := strings.ToLower(strings.TrimRight(baseURL, "/"))
	switch {
	case strings.HasSuffix(lower, "/anthropic") || strings.HasSuffix(lower, "/anthropic/v1"):
		return api.APIAnthropicMessages
	case current == api.APIGoogleGenerativeAI &&
		(strings.HasSuffix(lower, "/openai") || strings.Contains(lower, "/openai/")):
		return api.APIOpenAICompletions
	}
	return current
}

// applyLegacyThinkingControl maps the pre-catalog thinking_control value
// onto the new settings. Unknown non-empty values keep the historical
// fallback (chat_template_kwargs).
func applyLegacyThinkingControl(s *OpenAICompletionsSettings, value string) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "":
		return
	case "none":
		s.ThinkingFormat = ThinkingFormatNone
	case "enable_thinking":
		s.ThinkingFormat = ThinkingFormatEnableThinking
	case "thinking_type":
		s.ThinkingFormat = ThinkingFormatThinkingType
	default:
		s.ThinkingFormat = ThinkingFormatChatTemplateKwargs
	}
}

// Capabilities is the UI-facing summary of a resolved model.
type Capabilities struct {
	Provider        string                `json:"provider"`
	API             api.API               `json:"api"`
	Cataloged       bool                  `json:"cataloged"`
	Reasoning       bool                  `json:"reasoning"`
	ThinkingLevels  []api.ReasoningEffort `json:"thinking_levels"`
	ThinkingFormat  string                `json:"thinking_format"`
	Input           []string              `json:"input,omitempty"`
	ContextWindow   int                   `json:"context_window,omitempty"`
	MaxOutputTokens int                   `json:"max_output_tokens,omitempty"`
	MaxTokensField  string                `json:"max_tokens_field,omitempty"`
}

// Capabilities summarizes the resolved model for API responses.
func (r *Resolved) Capabilities() Capabilities {
	if r == nil {
		return Capabilities{}
	}
	caps := Capabilities{
		Provider:        r.Vendor.ID,
		API:             r.API,
		Cataloged:       r.Cataloged,
		Reasoning:       r.Spec.Reasoning,
		Input:           r.Spec.Input,
		ContextWindow:   r.Spec.ContextWindow,
		MaxOutputTokens: r.Spec.MaxOutputTokens,
	}
	switch r.API {
	case api.APIOpenAICompletions:
		caps.ThinkingFormat = string(r.OpenAICompletions.ThinkingFormat)
		caps.MaxTokensField = r.OpenAICompletions.MaxTokensField
		if caps.ThinkingFormat == string(ThinkingFormatNone) {
			caps.ThinkingLevels = []api.ReasoningEffort{}
			return caps
		}
	case api.APIOpenAIResponses:
		caps.ThinkingFormat = "reasoning.effort"
		caps.MaxTokensField = "max_output_tokens"
	case api.APIAnthropicMessages:
		caps.ThinkingFormat = "thinking." + string(r.AnthropicMessages.ThinkingMode)
		caps.MaxTokensField = "max_tokens"
	case api.APIGoogleGenerativeAI:
		caps.ThinkingFormat = "thinkingConfig." + string(r.GoogleGenerativeAI.ThinkingMode)
		caps.MaxTokensField = "maxOutputTokens"
		if r.GoogleGenerativeAI.ThinkingMode == GoogleThinkingNone {
			caps.ThinkingLevels = []api.ReasoningEffort{}
			return caps
		}
	case api.APIOllama:
		caps.ThinkingFormat = "think"
		caps.MaxTokensField = "num_predict"
	}
	caps.ThinkingLevels = r.ThinkingLevels.SupportedLevels()
	return caps
}
