// Package providers defines vendor defaults and routing rules independently of the model catalog.
package providers

import (
	"fmt"
	"strings"

	"github.com/Tencent/WeKnora/internal/models"
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
func (v *Definition) CredentialLabelFor(modelType types.ModelType) *CredentialLabel {
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

// Definition is one model vendor / gateway / self-hosted runtime.
type Definition struct {
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
	// Order sorts the vendor list in the UI (lower first).
	Order int

	// Endpoint, when set, computes the request URL instead of the protocol
	// default (BaseURL + protocol path). Returning "" falls back.
	Endpoint func(req EndpointRequest) (url string, query map[string]string)
	// PreferAPI, when set, may switch the protocol for a base URL / model
	// pair after URL inference ran (OpenAI uses Responses on its own host
	// and Chat Completions on relays). Returning "" keeps the current choice.
	PreferAPI func(baseURL string, spec models.ModelSpec) api.API
	// Signer, when set (AuthSigned), builds the request signer from the
	// stored credentials.
	Signer func(creds api.Credentials) api.AuthFunc
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
func (v *Definition) GetDefaultURL(modelType types.ModelType) string {
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
func (v *Definition) SupportsType(modelType types.ModelType) bool {
	for _, t := range v.ModelTypes {
		if t == modelType {
			return true
		}
	}
	return false
}

// LocalizedName returns the name for a locale, falling back to Name.
func (v *Definition) LocalizedName(locale string) string {
	if n, ok := v.Names[locale]; ok && n != "" {
		return n
	}
	return v.Name
}

// LocalizedDescription returns the description for a locale.
func (v *Definition) LocalizedDescription(locale string) string {
	if d, ok := v.Descriptions[locale]; ok && d != "" {
		return d
	}
	return v.Description
}

// AuthStyleFor returns the auth style this vendor uses for one protocol,
// falling back to the vendor-wide Auth.
func (v *Definition) AuthStyleFor(protocol api.API) AuthStyle {
	if style, ok := v.AuthByAPI[protocol]; ok && style != "" {
		return style
	}
	return v.Auth
}

// AuthFunc builds the request authenticator for the stored credentials. The
// protocol matters because a vendor that serves a second protocol on a
// sub-path may authenticate it differently (see Definition.AuthByAPI).
func (v *Definition) AuthFunc(protocol api.API, creds api.Credentials) api.AuthFunc {
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
func (v *Definition) ValidateConfig(cfg *Config) error {
	if v.Validate != nil {
		return v.Validate(cfg)
	}
	return defaultValidate(v, cfg)
}

// IconContentType is the media type of vendor icons served inline.
const IconContentType = "image/svg+xml"

// GenericID is the catch-all OpenAI-compatible provider.
const GenericID = "generic"

func defaultValidate(v *Definition, cfg *Config) error {
	if cfg == nil {
		return fmt.Errorf("config is nil")
	}
	if v.RequiresAuth && v.Auth != AuthSigned && strings.TrimSpace(cfg.APIKey) == "" {
		return fmt.Errorf("API key is required for %s", v.Name)
	}
	if v.ID == GenericID && strings.TrimSpace(cfg.BaseURL) == "" {
		return fmt.Errorf("base URL is required for generic provider")
	}
	if strings.TrimSpace(cfg.ModelName) == "" {
		return fmt.Errorf("model name is required")
	}
	return nil
}
