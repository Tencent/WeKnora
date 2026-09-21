// Package catalog is the model/vendor directory. It answers one question for
// the rest of the system: "given a provider id, a model name and whatever the
// operator configured, how exactly do we talk to this model?"
//
// Facts come from three layers, lowest precedence first:
//  1. built-in vendor packages under internal/models/vendors (vendor.go +
//     models.json + icon.svg), registered through Register;
//  2. the deployment overlay config/models.json (same schema as pi's
//     ~/.pi/agent/models.json): new vendors, base URL / key overrides,
//     per-model overrides;
//  3. per-model overrides stored on the models table.
//
// Resolve merges those layers into a Resolved value that the protocol
// packages consume verbatim. No protocol or vendor package is imported here.
package catalog

import (
	"encoding/json"

	"github.com/Tencent/WeKnora/internal/models/api"
	"github.com/Tencent/WeKnora/internal/types"
)

// AuthStyle names how a vendor expects credentials.
type AuthStyle string

const (
	// AuthBearer sends Authorization: Bearer <key>.
	AuthBearer AuthStyle = "bearer"
	// AuthAPIKeyHeader sends api-key: <key> (Azure OpenAI).
	AuthAPIKeyHeader AuthStyle = "api-key"
	// AuthXAPIKey sends x-api-key: <key> (Anthropic).
	AuthXAPIKey AuthStyle = "x-api-key"
	// AuthGoogleAPIKey sends x-goog-api-key: <key> (Gemini native).
	AuthGoogleAPIKey AuthStyle = "x-goog-api-key"
	// AuthNone sends nothing (local deployments).
	AuthNone AuthStyle = "none"
	// AuthSigned delegates to the vendor's Signer hook (WeKnora Cloud).
	AuthSigned AuthStyle = "signed"
)

// ExtraField describes one vendor-specific configuration input the model
// editor renders dynamically (Azure api-version, LKEAP secret key, ...).
type ExtraField struct {
	Key         string            `json:"key"`
	Label       string            `json:"label"`
	Labels      map[string]string `json:"labels,omitempty"`
	Type        string            `json:"type"` // "string", "number", "boolean", "select", "password"
	Required    bool              `json:"required"`
	Default     string            `json:"default,omitempty"`
	Placeholder string            `json:"placeholder,omitempty"`
	// Placeholders carries localized variants of Placeholder.
	Placeholders map[string]string  `json:"placeholders,omitempty"`
	Options      []ExtraFieldOption `json:"options,omitempty"`
	// ModelTypes restricts the field to some model types; empty means all.
	ModelTypes []types.ModelType `json:"model_types,omitempty"`
	// Secret marks values that must never be echoed back to the UI.
	Secret bool `json:"secret,omitempty"`
}

// CredentialLabel renames the primary credential input for one vendor.
//
// Most vendors take an API key and need none of this. LKEAP and Volcengine
// authenticate their rerank API with a CAM / IAM identity pair instead, where
// the first field is a SecretId / AccessKeyId — calling it "API Key" leads
// operators to paste an `sk-` token that can never authenticate. The vendor
// says what the field is called rather than the editor hardcoding a table of
// vendor ids, which is what this whole layer exists to avoid.
type CredentialLabel struct {
	Label        string            `json:"label"`
	Labels       map[string]string `json:"labels,omitempty"`
	Placeholder  string            `json:"placeholder,omitempty"`
	Placeholders map[string]string `json:"placeholders,omitempty"`
	// Hint is rendered under the input, for a warning the label cannot carry.
	Hint  string            `json:"hint,omitempty"`
	Hints map[string]string `json:"hints,omitempty"`
	// ModelTypes restricts the override; empty means every type.
	ModelTypes []types.ModelType `json:"model_types,omitempty"`
	// Required marks the credential mandatory even where the generic label
	// calls it optional.
	Required bool `json:"required,omitempty"`
}

// LocalizedLabel / LocalizedPlaceholder / LocalizedHint resolve one locale,
// falling back to the default string.
func (c CredentialLabel) LocalizedLabel(locale string) string {
	return localizedOr(c.Labels, locale, c.Label)
}

// LocalizedPlaceholder resolves the placeholder for a locale.
func (c CredentialLabel) LocalizedPlaceholder(locale string) string {
	return localizedOr(c.Placeholders, locale, c.Placeholder)
}

// LocalizedHint resolves the hint for a locale.
func (c CredentialLabel) LocalizedHint(locale string) string {
	return localizedOr(c.Hints, locale, c.Hint)
}

func localizedOr(table map[string]string, locale, fallback string) string {
	if v, ok := table[locale]; ok && v != "" {
		return v
	}
	return fallback
}

// LocalizedPlaceholder resolves the placeholder for a locale.
func (f ExtraField) LocalizedPlaceholder(locale string) string {
	return localizedOr(f.Placeholders, locale, f.Placeholder)
}

// CredentialLabelFor returns the credential naming for a model type, or nil
// when the vendor uses the generic API-key wording.
func (v *Vendor) CredentialLabelFor(modelType types.ModelType) *CredentialLabel {
	for i := range v.CredentialLabels {
		c := &v.CredentialLabels[i]
		if len(c.ModelTypes) == 0 {
			return c
		}
		for _, mt := range c.ModelTypes {
			if mt == modelType {
				return c
			}
		}
	}
	return nil
}

// ExtraFieldOption is one choice of a select field. Labels carries localized
// variants keyed by locale, like every other operator-facing string here: an
// option whose label is prose rather than an identifier is unreadable to half
// the product without it.
type ExtraFieldOption struct {
	Label  string            `json:"label"`
	Labels map[string]string `json:"labels,omitempty"`
	Value  string            `json:"value"`
}

// LocalizedLabel resolves the option label for a locale.
func (o ExtraFieldOption) LocalizedLabel(locale string) string {
	return localizedOr(o.Labels, locale, o.Label)
}

// ModelCost is priced per million tokens, in USD unless Currency says otherwise.
type ModelCost struct {
	Input      float64 `json:"input"`
	Output     float64 `json:"output"`
	CacheRead  float64 `json:"cache_read,omitempty"`
	CacheWrite float64 `json:"cache_write,omitempty"`
	Currency   string  `json:"currency,omitempty"`
}

// ModelSpec is one entry of a vendor's models.json.
//
// Only ID is required. API defaults to the vendor's API; Type defaults to
// KnowledgeQA. Compat is interpreted according to API (see compat.go).
type ModelSpec struct {
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`
	// Type is the WeKnora model type this entry describes. Chat models with
	// image input are still KnowledgeQA; the UI derives VLM eligibility from
	// Input. Embedding / Rerank / ASR entries exist for the model picker.
	Type types.ModelType `json:"type,omitempty"`
	API  api.API         `json:"api,omitempty"`
	// Aliases are additional ids that resolve to this entry (dated
	// snapshots, vendor-prefixed names on gateways).
	Aliases []string `json:"aliases,omitempty"`
	// Match is a case-insensitive glob ("gpt-5*", "*deepseek-r1*") that lets
	// one entry describe a family. Exact ids and aliases win over patterns;
	// among patterns the longest literal prefix wins.
	Match string `json:"match,omitempty"`
	// Reasoning reports whether the model can emit thinking at all.
	Reasoning bool `json:"reasoning"`
	// Input lists accepted modalities: text, image, audio, video.
	Input           []string   `json:"input,omitempty"`
	ContextWindow   int        `json:"context_window,omitempty"`
	MaxOutputTokens int        `json:"max_output_tokens,omitempty"`
	Cost            *ModelCost `json:"cost,omitempty"`
	// ThinkingLevels maps protocol-neutral levels to vendor values; missing
	// keys inherit the vendor map. See api.ThinkingLevelMap.
	ThinkingLevels api.ThinkingLevelMap `json:"thinking_levels,omitempty"`
	// Compat is the protocol-specific overlay for this model (flat object,
	// fields depend on API). Decoded lazily by Resolve.
	Compat json.RawMessage `json:"compat,omitempty"`
	// Dimension is the embedding vector width (embedding entries only).
	Dimension int `json:"dimension,omitempty"`
	// Deprecated hides the entry from pickers but keeps resolution working.
	Deprecated bool `json:"deprecated,omitempty"`
	// Source is the documentation URL the facts were taken from.
	Source string `json:"source,omitempty"`
}

// DisplayName returns Name or the id.
func (m ModelSpec) DisplayName() string {
	if m.Name != "" {
		return m.Name
	}
	return m.ID
}

// AcceptsImages reports whether Input lists image.
func (m ModelSpec) AcceptsImages() bool {
	for _, in := range m.Input {
		if in == "image" {
			return true
		}
	}
	return false
}

// Credentials is what the operator stored for one model row.
type Credentials struct {
	APIKey    string
	AppID     string
	AppSecret string
}

// EndpointRequest is the input to a vendor's Endpoint hook.
type EndpointRequest struct {
	BaseURL   string
	Model     string
	ModelType types.ModelType
	API       api.API
	// EmbeddingAPI is the resolved embedding protocol on an embedding
	// request. Aliyun and Volcengine each serve their text and multimodal
	// embeddings on different paths under one base URL, so the hook has to
	// know which one the model speaks.
	EmbeddingAPI api.EmbeddingAPI
	Extra        map[string]string
}

// Vendor is one model vendor / gateway / self-hosted runtime.
type Vendor struct {
	// ID is the stable identifier stored in models.parameters.provider.
	ID string
	// Name is the brand name; Names carries localized variants keyed by
	// locale ("zh-CN"). Description / Descriptions likewise.
	Name         string
	Names        map[string]string
	Description  string
	Descriptions map[string]string
	Website      string
	// Icon is the brand mark as SVG bytes (served inline to the UI).
	Icon []byte
	// API is the default chat protocol; a ModelSpec may override it.
	API api.API
	// RerankAPI is the rerank protocol. Register defaults it to the Cohere
	// shape for any vendor that serves rerank without naming another.
	RerankAPI api.RerankAPI
	// EmbeddingAPI is the embedding protocol. Register defaults it to the
	// OpenAI shape for any vendor that serves embeddings without naming
	// another.
	EmbeddingAPI api.EmbeddingAPI
	// TranscriptionAPI is the speech-to-text protocol. Register defaults it
	// to the OpenAI shape for any vendor that serves ASR.
	TranscriptionAPI api.TranscriptionAPI
	// DefaultBaseURLs by model type; GetDefaultURL falls back to chat.
	DefaultBaseURLs map[types.ModelType]string
	ModelTypes      []types.ModelType
	RequiresAuth    bool
	Auth            AuthStyle
	// AuthByAPI overrides Auth for one protocol. A vendor that exposes a
	// second protocol on a sub-path may authenticate it differently: Gemini's
	// OpenAI-compatible facade documents Authorization: Bearer while the
	// native generateContent API takes x-goog-api-key. Protocols absent from
	// the map use Auth.
	AuthByAPI map[api.API]AuthStyle
	// URLPatterns are substrings of a base URL that identify this vendor
	// when the operator left provider empty (legacy rows).
	URLPatterns []string
	ExtraFields []ExtraField
	// CredentialLabels rename the primary credential input per model type.
	CredentialLabels []CredentialLabel
	// Compat holds vendor-level protocol defaults.
	Compat VendorCompat
	// ThinkingLevels is the vendor-level level map.
	ThinkingLevels api.ThinkingLevelMap
	// Models is the built-in catalog (from models.json).
	Models []ModelSpec
	// Order sorts the vendor list in the UI (lower first).
	Order int

	// Endpoint, when set, computes the request URL instead of the protocol
	// default (BaseURL + protocol path). Returning "" falls back.
	Endpoint func(req EndpointRequest) (url string, query map[string]string)
	// PreferAPI, when set, may switch the protocol for a base URL / model
	// pair after URL inference ran (OpenAI uses Responses on its own host
	// and Chat Completions on relays). Returning "" keeps the current choice.
	PreferAPI func(baseURL string, spec ModelSpec) api.API
	// Signer, when set (AuthSigned), builds the request signer from the
	// stored credentials.
	Signer func(creds Credentials) api.AuthFunc
	// Validate checks a configuration before it is saved or used. Nil means
	// the default check (key required when RequiresAuth; model name required).
	Validate func(cfg *Config) error
	// Headers are static headers sent on every request (vendor betas).
	Headers map[string]string
	// DefaultAPIKey is a deployment-level key from config/models.json used
	// when the model row stores none.
	DefaultAPIKey string
}

// Config is the operator-supplied configuration of one model row.
type Config struct {
	Provider  string
	BaseURL   string
	APIKey    string
	ModelName string
	ModelID   string
	ModelType types.ModelType
	Extra     map[string]string
}

// GetDefaultURL returns the default base URL for a model type, falling back
// to the chat URL.
func (v *Vendor) GetDefaultURL(modelType types.ModelType) string {
	if v == nil {
		return ""
	}
	if url, ok := v.DefaultBaseURLs[modelType]; ok {
		return url
	}
	if url, ok := v.DefaultBaseURLs[types.ModelTypeKnowledgeQA]; ok {
		return url
	}
	return ""
}

// SupportsType reports whether the vendor lists the model type.
func (v *Vendor) SupportsType(modelType types.ModelType) bool {
	for _, t := range v.ModelTypes {
		if t == modelType {
			return true
		}
	}
	return false
}

// LocalizedName returns the name for a locale, falling back to Name.
func (v *Vendor) LocalizedName(locale string) string {
	if n, ok := v.Names[locale]; ok && n != "" {
		return n
	}
	return v.Name
}

// LocalizedDescription returns the description for a locale.
func (v *Vendor) LocalizedDescription(locale string) string {
	if d, ok := v.Descriptions[locale]; ok && d != "" {
		return d
	}
	return v.Description
}

// AuthStyleFor returns the auth style this vendor uses for one protocol,
// falling back to the vendor-wide Auth.
func (v *Vendor) AuthStyleFor(protocol api.API) AuthStyle {
	if style, ok := v.AuthByAPI[protocol]; ok && style != "" {
		return style
	}
	return v.Auth
}

// AuthFunc builds the request authenticator for the stored credentials. The
// protocol matters because a vendor that serves a second protocol on a
// sub-path may authenticate it differently (see Vendor.AuthByAPI).
func (v *Vendor) AuthFunc(protocol api.API, creds Credentials) api.AuthFunc {
	switch v.AuthStyleFor(protocol) {
	case AuthAPIKeyHeader:
		return api.HeaderAuth("api-key", creds.APIKey)
	case AuthXAPIKey:
		return api.HeaderAuth("x-api-key", creds.APIKey)
	case AuthGoogleAPIKey:
		return api.HeaderAuth("x-goog-api-key", creds.APIKey)
	case AuthNone:
		return api.NoAuth()
	case AuthSigned:
		if v.Signer != nil {
			return v.Signer(creds)
		}
		return api.NoAuth()
	default:
		return api.BearerAuth(creds.APIKey)
	}
}

// ValidateConfig runs the vendor's validation.
func (v *Vendor) ValidateConfig(cfg *Config) error {
	if v.Validate != nil {
		return v.Validate(cfg)
	}
	return defaultValidate(v, cfg)
}

// IconContentType is the media type of vendor icons served inline.
const IconContentType = "image/svg+xml"

// Ptr returns a pointer to v; vendor packages use it to fill overlay structs.
func Ptr[T any](v T) *T { return &v }
