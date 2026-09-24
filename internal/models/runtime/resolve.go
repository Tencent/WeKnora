package runtime

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/Tencent/WeKnora/internal/models"
	"github.com/Tencent/WeKnora/internal/models/api"
	"github.com/Tencent/WeKnora/internal/models/catalog"
	"github.com/Tencent/WeKnora/internal/models/internal/configcopy"
	"github.com/Tencent/WeKnora/internal/models/providers"
	"github.com/Tencent/WeKnora/internal/types"
)

// Ref identifies one configured model.
type Ref struct {
	Provider  string
	Model     string
	BaseURL   string
	ModelType types.ModelType
	Extra     map[string]string
	Override  *types.ModelSpecOverride
	// TruncatePromptTokens is an embedding row's
	// embedding_parameters.truncate_prompt_tokens. It is a first-class column
	// rather than an extra_config key, which is why it arrives here instead
	// of in Extra.
	TruncatePromptTokens int
}

// Resolved is the fully merged view of one model: which vendor, which
// protocol, which settings.
type Resolved struct {
	Vendor *providers.Definition
	// Spec is the effective catalog entry (synthesized when unknown).
	Spec models.ModelSpec
	// Cataloged reports whether the model name matched a catalog entry.
	Cataloged bool
	API       api.API
	BaseURL   string
	// RemoteModel is the id sent on the wire.
	RemoteModel    string
	ThinkingLevels api.ThinkingLevelMap

	OpenAICompletions  api.OpenAICompletionsSettings
	OpenAIResponses    api.OpenAIResponsesSettings
	AnthropicMessages  api.AnthropicMessagesSettings
	GoogleGenerativeAI api.GoogleGenerativeAISettings

	// RerankAPI and Rerank are filled only when the reference asked for a
	// rerank model. Chat resolution is on every request's hot path, so the
	// rerank overlay is not merged for it.
	RerankAPI api.RerankAPI
	Rerank    api.RerankSettings

	// EmbeddingAPI and Embeddings are filled only for an embedding reference,
	// for the same reason.
	EmbeddingAPI api.EmbeddingAPI
	Embeddings   api.EmbeddingsSettings

	// TranscriptionAPI and Transcriptions are filled only for an ASR
	// reference.
	TranscriptionAPI api.TranscriptionAPI
	Transcriptions   api.TranscriptionsSettings
}

// Resolve merges the vendor, catalog entry, extra-config and per-row
// overrides for a model reference. It never fails for unknown vendors or
// models: they degrade to the generic OpenAI-compatible baseline.
func Resolve(ref Ref) (*Resolved, error) { return Default().Resolve(ref) }

// Resolve merges one model against a single generation of this runtime.
func (rt *Runtime) Resolve(ref Ref) (*Resolved, error) {
	return resolveWithVendor(ref, rt.selectProvider(ref.Provider, ref.BaseURL))
}

func resolveWithVendor(ref Ref, vendor *Provider) (*Resolved, error) {
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

	spec, cataloged := vendor.FindModel(ref.Model, modelType)
	if !cataloged {
		spec = models.ModelSpec{ID: ref.Model, Type: catalog.EntryType(modelType)}
	}
	if spec.API == "" {
		spec.API = vendor.API
	}
	catalogAPI := spec.API
	if ref.Override != nil {
		applySpecOverride(&spec, ref.Override)
	}

	if modelType == types.ModelTypeRerank {
		return resolveRerank(ref, vendor, spec, cataloged, baseURL)
	}
	if modelType == types.ModelTypeEmbedding {
		return resolveEmbeddings(ref, vendor, spec, cataloged, baseURL)
	}
	if modelType == types.ModelTypeASR {
		return resolveTranscriptions(ref, vendor, spec, cataloged, baseURL)
	}

	resolvedAPI := spec.API
	if forced := strings.TrimSpace(ref.Extra[models.ExtraAPI]); forced != "" {
		resolvedAPI = api.API(forced)
	} else if ref.Override == nil || ref.Override.API == "" {
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
		Vendor:         configcopy.Clone(vendor.Definition),
		Spec:           spec,
		Cataloged:      cataloged,
		API:            resolvedAPI,
		BaseURL:        baseURL,
		RemoteModel:    ref.Model,
		ThinkingLevels: levels,
	}
	if override := strings.TrimSpace(ref.Extra[models.ExtraRemoteModelName]); override != "" {
		out.RemoteModel = override
	}

	overrideCompat := ref.Override.CompatJSON()

	// Every protocol's settings are resolved so callers can inspect any of
	// them, but only the layers whose API matches contribute model/override
	// compat (a flat compat object is meaningless for another protocol).
	completions := api.DefaultOpenAICompletions()
	ApplyCompat(&completions, &vendor.Compat.OpenAICompletions)
	if err := applyRawCompat(
		&completions, &api.OpenAICompletionsCompat{}, spec.Compat, catalogAPI, api.APIOpenAICompletions,
	); err != nil {
		return nil, err
	}
	if err := applyRawCompat(
		&completions, &api.OpenAICompletionsCompat{}, overrideCompat, resolvedAPI, api.APIOpenAICompletions,
	); err != nil {
		return nil, err
	}
	applyLegacyThinkingControl(&completions, ref.Extra[models.ExtraThinkingControl])
	out.OpenAICompletions = completions

	responses := api.DefaultOpenAIResponses()
	ApplyCompat(&responses, &vendor.Compat.OpenAIResponses)
	if err := applyRawCompat(
		&responses, &api.OpenAIResponsesCompat{}, spec.Compat, catalogAPI, api.APIOpenAIResponses,
	); err != nil {
		return nil, err
	}
	if err := applyRawCompat(
		&responses, &api.OpenAIResponsesCompat{}, overrideCompat, resolvedAPI, api.APIOpenAIResponses,
	); err != nil {
		return nil, err
	}
	out.OpenAIResponses = responses

	anthropic := api.DefaultAnthropicMessages()
	ApplyCompat(&anthropic, &vendor.Compat.AnthropicMessages)
	if err := applyRawCompat(
		&anthropic, &api.AnthropicMessagesCompat{}, spec.Compat, catalogAPI, api.APIAnthropicMessages,
	); err != nil {
		return nil, err
	}
	if err := applyRawCompat(
		&anthropic, &api.AnthropicMessagesCompat{}, overrideCompat, resolvedAPI, api.APIAnthropicMessages,
	); err != nil {
		return nil, err
	}
	out.AnthropicMessages = anthropic

	google := api.DefaultGoogleGenerativeAI()
	ApplyCompat(&google, &vendor.Compat.GoogleGenerativeAI)
	if err := applyRawCompat(
		&google, &api.GoogleGenerativeAICompat{}, spec.Compat, catalogAPI, api.APIGoogleGenerativeAI,
	); err != nil {
		return nil, err
	}
	if err := applyRawCompat(
		&google, &api.GoogleGenerativeAICompat{}, overrideCompat, resolvedAPI, api.APIGoogleGenerativeAI,
	); err != nil {
		return nil, err
	}
	out.GoogleGenerativeAI = google
	// An explicit legacy thinking_control is the operator saying "this row
	// does think, send the switch this way", so it outranks the catalog.
	if strings.TrimSpace(ref.Extra[models.ExtraThinkingControl]) == "" {
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
	r.OpenAICompletions.ThinkingFormat = api.ThinkingFormatNone
	r.OpenAICompletions.ThinkingBudgetField = ""
	r.OpenAICompletions.SupportsReasoningEffort = false
	r.GoogleGenerativeAI.ThinkingMode = api.GoogleThinkingNone
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
	if err := DecodeCompat(raw, overlayPtr); err != nil {
		return fmt.Errorf("%s compat: %w", target, err)
	}
	ApplyCompat(settings, overlayPtr)
	return nil
}

func applySpecOverride(spec *models.ModelSpec, o *types.ModelSpecOverride) {
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

// Capabilities is the frontend contract, independent of runtime assembly.
type Capabilities = models.Capabilities

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
		if caps.ThinkingFormat == string(api.ThinkingFormatNone) {
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
		if r.GoogleGenerativeAI.ThinkingMode == api.GoogleThinkingNone {
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

// resolveRerank merges the rerank layers. Rerank has one settings struct
// rather than one per protocol, so the model entry's compat object is decoded
// unconditionally instead of being matched against a protocol.
func resolveRerank(
	ref Ref, vendor *Provider, spec models.ModelSpec, cataloged bool, baseURL string,
) (*Resolved, error) {
	protocol := vendor.RerankAPI
	if protocol == "" {
		protocol = api.RerankCohere
	}
	if !protocol.Known() {
		return nil, fmt.Errorf("catalog: unknown rerank api %q for provider %s", protocol, vendor.ID)
	}

	// Lowest precedence first: protocol default, vendor, model entry, row spec
	// override. extra_config is the operator speaking about this one row, so it
	// comes last.
	settings := api.DefaultRerank()
	ApplyCompat(&settings, &vendor.Compat.Rerank)
	if len(spec.Compat) > 0 {
		overlay := &api.RerankCompat{}
		if err := DecodeCompat(spec.Compat, overlay); err != nil {
			return nil, fmt.Errorf("rerank compat: %w", err)
		}
		ApplyCompat(&settings, overlay)
	}
	if raw := ref.Override.CompatJSON(); len(raw) > 0 {
		overlay := &api.RerankCompat{}
		if err := DecodeCompat(raw, overlay); err != nil {
			return nil, fmt.Errorf("rerank compat: %w", err)
		}
		ApplyCompat(&settings, overlay)
	}

	if raw := strings.TrimSpace(ref.Extra[models.ExtraScoreScale]); raw != "" {
		scale := api.ScoreScale(strings.ToLower(raw))
		if scale != api.ScoreProbability && scale != api.ScoreLogit {
			return nil, fmt.Errorf(
				"catalog: invalid %s in extra_config: %q (expected %q or %q)",
				models.ExtraScoreScale, raw, api.ScoreProbability, api.ScoreLogit,
			)
		}
		settings.ScoreScale = scale
	}
	if raw := strings.TrimSpace(ref.Extra[models.ExtraTruncatePromptTokens]); raw != "" {
		if !settings.AcceptsTruncatePromptTokens {
			return nil, fmt.Errorf(
				"catalog: %s is a vLLM extension and %s does not implement it; "+
					"remove it from extra_config (it is accepted by self-hosted runtimes only)",
				models.ExtraTruncatePromptTokens, vendor.ID,
			)
		}
		budget, err := strconv.Atoi(raw)
		if err != nil || budget <= 0 {
			return nil, fmt.Errorf("catalog: invalid %s in extra_config: %q", models.ExtraTruncatePromptTokens, raw)
		}
		settings.TruncatePromptTokens = budget
	}
	// Checked after every layer: a model entry is what declares a dialect
	// this build cannot speak.
	if settings.UnsupportedReason != "" {
		return nil, &UnsupportedModelError{Provider: vendor.ID, Model: spec.ID, Reason: settings.UnsupportedReason}
	}
	out := &Resolved{
		Vendor:      configcopy.Clone(vendor.Definition),
		Spec:        spec,
		Cataloged:   cataloged,
		BaseURL:     baseURL,
		RemoteModel: ref.Model,
		RerankAPI:   protocol,
		Rerank:      settings,
	}
	if override := strings.TrimSpace(ref.Extra[models.ExtraRemoteModelName]); override != "" {
		out.RemoteModel = override
	}
	return out, nil
}

// resolveEmbeddings merges the embedding layers, in the same order as
// resolveRerank: protocol default, vendor, model entry, row spec override,
// then extra_config, which is the operator speaking about this one row.
func resolveEmbeddings(
	ref Ref, vendor *Provider, spec models.ModelSpec, cataloged bool, baseURL string,
) (*Resolved, error) {
	protocol := vendor.EmbeddingAPI
	if protocol == "" {
		protocol = api.EmbeddingOpenAI
	}
	if !protocol.Known() {
		return nil, fmt.Errorf("catalog: unknown embedding api %q for provider %s", protocol, vendor.ID)
	}

	settings := api.DefaultEmbeddings()
	ApplyCompat(&settings, &vendor.Compat.Embeddings)
	if len(spec.Compat) > 0 {
		overlay := &api.EmbeddingsCompat{}
		if err := DecodeCompat(spec.Compat, overlay); err != nil {
			return nil, fmt.Errorf("embeddings compat: %w", err)
		}
		ApplyCompat(&settings, overlay)
	}
	if raw := ref.Override.CompatJSON(); len(raw) > 0 {
		overlay := &api.EmbeddingsCompat{}
		if err := DecodeCompat(raw, overlay); err != nil {
			return nil, fmt.Errorf("embeddings compat: %w", err)
		}
		ApplyCompat(&settings, overlay)
	}
	// The row may carry a truncation budget, but the extension is vLLM's and
	// appears in no managed vendor's schema. Dropping it where the vendor
	// does not implement it is the quiet direction on purpose: unlike the
	// rerank opt-in, this field has a UI control that every embedding row
	// has always carried, so refusing to resolve would break rows whose only
	// mistake is a non-zero default.
	//
	// Where it is accepted, an unset row keeps the budget the pre-catalog
	// client always sent. A self-hosted index was built with long chunks cut
	// at that length; embedding new chunks uncut would put them in a
	// different place from the old ones without anything failing.
	if settings.AcceptsTruncatePromptTokens {
		settings.TruncatePromptTokens = ref.TruncatePromptTokens
		if settings.TruncatePromptTokens <= 0 {
			settings.TruncatePromptTokens = models.LegacyTruncatePromptTokens
		}
	}

	// A model entry may name a different dialect than the vendor default.
	if settings.API != "" {
		if !settings.API.Known() {
			return nil, fmt.Errorf(
				"catalog: unknown embedding api %q on %s/%s", settings.API, vendor.ID, spec.ID)
		}
		protocol = settings.API
	}
	settings.API = protocol

	out := &Resolved{
		Vendor:       configcopy.Clone(vendor.Definition),
		Spec:         spec,
		Cataloged:    cataloged,
		BaseURL:      baseURL,
		RemoteModel:  ref.Model,
		EmbeddingAPI: protocol,
		Embeddings:   settings,
	}
	if override := strings.TrimSpace(ref.Extra[models.ExtraRemoteModelName]); override != "" {
		out.RemoteModel = override
	}
	return out, nil
}

// resolveTranscriptions merges the speech-to-text layers, in the same order
// as resolveEmbeddings: protocol default, vendor compat, the catalog entry,
// the row's own compat, then remote_model_name.
func resolveTranscriptions(
	ref Ref, vendor *Provider, spec models.ModelSpec, cataloged bool, baseURL string,
) (*Resolved, error) {
	protocol := vendor.TranscriptionAPI
	if protocol == "" {
		protocol = api.TranscriptionOpenAI
	}
	settings := api.DefaultTranscriptions()
	ApplyCompat(&settings, &vendor.Compat.Transcriptions)
	for _, raw := range []json.RawMessage{spec.Compat, ref.Override.CompatJSON()} {
		if len(raw) == 0 {
			continue
		}
		overlay := &api.TranscriptionsCompat{}
		if err := DecodeCompat(raw, overlay); err != nil {
			return nil, fmt.Errorf("transcriptions compat: %w", err)
		}
		ApplyCompat(&settings, overlay)
	}
	if settings.API != "" {
		protocol = settings.API
	}
	if !protocol.Known() {
		return nil, fmt.Errorf("catalog: unknown transcription api %q for %s/%s", protocol, vendor.ID, spec.ID)
	}
	settings.API = protocol
	switch settings.LanguageParam {
	case "", api.LanguageForm, api.LanguageHeader, api.LanguageASROptions:
	default:
		return nil, fmt.Errorf("catalog: unknown language_param %q for %s/%s",
			settings.LanguageParam, vendor.ID, spec.ID)
	}
	// Checked after every layer: an entry may lift a vendor-wide refusal for
	// the one model that takes the audio in the request.
	if settings.UnsupportedReason != "" {
		return nil, &UnsupportedModelError{Provider: vendor.ID, Model: spec.ID, Reason: settings.UnsupportedReason}
	}

	out := &Resolved{
		Vendor:           configcopy.Clone(vendor.Definition),
		Spec:             spec,
		Cataloged:        cataloged,
		BaseURL:          baseURL,
		RemoteModel:      ref.Model,
		TranscriptionAPI: protocol,
		Transcriptions:   settings,
	}
	if override := strings.TrimSpace(ref.Extra[models.ExtraRemoteModelName]); override != "" {
		out.RemoteModel = override
	}
	return out, nil
}
