package dto

import (
	"context"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/models/catalog"
	"github.com/Tencent/WeKnora/internal/types"
)

// VendorRef identifies the vendor a set of model parameters belongs to.
// BaseURL covers legacy rows saved before provider was stored; the vendor is
// then detected from the endpoint.
type VendorRef struct {
	Provider string
	BaseURL  string
}

// vendorID resolves the ref to a comparable vendor id. An id that matches no
// registered vendor is returned as typed, so two rows pinned to the same
// hand-written provider still compare equal.
func (r VendorRef) vendorID() string {
	id := strings.ToLower(strings.TrimSpace(r.Provider))
	if id == "" {
		id = catalog.DetectByURL(r.BaseURL)
	}
	return id
}

// names reports whether the ref identifies a vendor at all. A PUT body that
// carries neither provider nor base URL names none, and must not be read as
// "the user moved this row to another vendor".
func (r VendorRef) names() bool {
	return strings.TrimSpace(r.Provider) != "" || strings.TrimSpace(r.BaseURL) != ""
}

func secretKeysOf(v *catalog.Vendor, into map[string]bool) map[string]bool {
	for _, f := range v.ExtraFields {
		if !f.Secret && f.Type != "password" {
			continue
		}
		if into == nil {
			into = map[string]bool{}
		}
		into[f.Key] = true
	}
	return into
}

// SecretExtraConfigKeys returns the extra_config keys the vendor declares as
// secret (catalog.ExtraField.Secret, or a password input). These are real
// credentials — LKEAP and Volcengine rerank keep an IAM/CAM secret key there
// — so they get the same treatment as api_key / app_secret: never echoed,
// only reported as present.
//
// The field's ModelTypes restriction is deliberately ignored: a key that is
// secret for any model type of the vendor is redacted for every row of that
// vendor.
func SecretExtraConfigKeys(provider, baseURL string) map[string]bool {
	v, ok := catalog.Get(VendorRef{Provider: provider, BaseURL: baseURL}.vendorID())
	if !ok {
		return nil
	}
	return secretKeysOf(v, nil)
}

// AllSecretExtraConfigKeys returns every extra_config key any registered
// vendor declares secret.
//
// Redaction must not depend on the row's own provider being the one that
// declared the key: a row whose provider was switched, hand-edited or never
// stored would otherwise echo a credential in plaintext simply because its
// current vendor declares no secret field. Treating a name that is secret
// somewhere as secret everywhere is the safe direction — the worst case is
// withholding a value that happened to reuse the name.
func AllSecretExtraConfigKeys() map[string]bool {
	var keys map[string]bool
	for _, v := range catalog.List() {
		keys = secretKeysOf(v, keys)
	}
	return keys
}

// redactedSecretPlaceholder reports whether an incoming extra_config value
// carries no new secret: empty, or a mask the UI shows for a value it never
// received (GET omits the key entirely, but forms happily render bullets).
func redactedSecretPlaceholder(v string) bool {
	t := strings.TrimSpace(v)
	return t == "" || strings.Trim(t, "*•·●") == ""
}

// PreserveStoredSecretExtras merges the stored secret extra_config values
// into an incoming map.
//
// PUT /models/{id} replaces extra_config wholesale and the frontend always
// sends the map for remote rows, but GET redacts the vendor's secret fields,
// so a plain edit-and-save round-trip carries no value for them. Without this
// merge the first save after any unrelated edit would erase the stored secret
// key and break rerank for that row. An incoming value that is absent, empty
// or a mask means "unchanged"; anything else replaces the stored secret.
//
// storedVendor identifies the vendor the stored values belong to; edited is
// the vendor the incoming values are for. They differ when the user moves the
// row to another vendor, and then the stored credential must be dropped
// rather than carried over: it belongs to the previous integration's account,
// the editor no longer renders a field for it, and the new vendor may not
// declare it secret at all — which is how it ended up echoed in plaintext.
//
// incoming is filled in place and returned.
func PreserveStoredSecretExtras(
	stored, incoming map[string]string, storedVendor, edited VendorRef,
) map[string]string {
	keys := SecretExtraConfigKeys(storedVendor.Provider, storedVendor.BaseURL)
	// A request that names no vendor at all changed none: keep the old
	// contract, which rerank rows rely on for their redacted secret.
	switched := edited.names() && edited.vendorID() != storedVendor.vendorID()
	if incoming == nil {
		if !switched || len(keys) == 0 || len(stored) == 0 {
			return stored
		}
		// extra_config omitted entirely still keeps the rest of the stored
		// map, but not the previous vendor's credential.
		out := make(map[string]string, len(stored))
		for k, v := range stored {
			if !keys[k] {
				out[k] = v
			}
		}
		return out
	}
	if len(keys) == 0 {
		return incoming
	}
	if switched {
		// Only a value the user actually typed survives a vendor switch; an
		// absent or masked one would otherwise resurrect the old secret when
		// the new vendor happens to reuse the key name.
		for key := range keys {
			if redactedSecretPlaceholder(incoming[key]) {
				delete(incoming, key)
			}
		}
		return incoming
	}
	for key := range keys {
		storedValue := stored[key]
		if storedValue == "" {
			continue
		}
		if redactedSecretPlaceholder(incoming[key]) {
			incoming[key] = storedValue
		}
	}
	return incoming
}

// HasAllSecretExtras reports whether extra already carries a usable value for
// every secret extra field the vendor declares. Callers that would otherwise
// skip a lookup of the stored row use it to notice that the request is
// missing a redacted secret.
func HasAllSecretExtras(provider, baseURL string, extra map[string]string) bool {
	for key := range SecretExtraConfigKeys(provider, baseURL) {
		if redactedSecretPlaceholder(extra[key]) {
			return false
		}
	}
	return true
}

// ModelResponse mirrors types.Model for response bodies, with all secret
// fields (APIKey, AppSecret) removed by construction. Credential presence
// metadata lives behind the /credentials subresource, not inlined here.
//
// BaseURL is preserved for tenant-owned models (the frontend needs it to
// render which endpoint a custom model points at). For builtin models it is
// stripped along with every other field that could leak how a particular
// tenant configured the upstream provider.
type ModelResponse struct {
	ID          string             `json:"id"`
	TenantID    uint64             `json:"tenant_id"`
	Name        string             `json:"name"`
	DisplayName string             `json:"display_name"`
	Type        types.ModelType    `json:"type"`
	Source      types.ModelSource  `json:"source"`
	Description string             `json:"description"`
	Parameters  ModelParametersDTO `json:"parameters"`
	IsDefault   bool               `json:"is_default"`
	IsBuiltin   bool               `json:"is_builtin"`
	Status      types.ModelStatus  `json:"status"`
	CreatedAt   time.Time          `json:"created_at"`
	UpdatedAt   time.Time          `json:"updated_at"`
	// Per-field "configured?" map. Omitted for builtin models unless the
	// caller is a system administrator. See MCPServiceResponse.Credentials.
	Credentials map[string]CredentialFieldMetadata `json:"credentials,omitempty"`
	// Capabilities is the catalog view of the model: protocol, whether it
	// can think and at which levels, context window. Chat models only.
	Capabilities *catalog.Capabilities `json:"capabilities,omitempty"`
}

// ModelParametersDTO carries every parameter field EXCEPT the two secret
// ones (APIKey, AppSecret). AppID is non-secret and stays — it's an account
// identifier the WeKnora Cloud frontend renders. CustomHeaders is also kept
// (structural metadata, not a credential).
type ModelParametersDTO struct {
	BaseURL             string                    `json:"base_url"`
	InterfaceType       string                    `json:"interface_type"`
	EmbeddingParameters types.EmbeddingParameters `json:"embedding_parameters"`
	ParameterSize       string                    `json:"parameter_size"`
	Provider            string                    `json:"provider"`
	ExtraConfig         map[string]string         `json:"extra_config,omitempty"`
	CustomHeaders       map[string]string         `json:"custom_headers,omitempty"`
	SupportsVision      bool                      `json:"supports_vision"`
	ContextWindow       int                       `json:"context_window,omitempty"`
	MaxOutputTokens     int                       `json:"max_output_tokens,omitempty"`
	MaxConcurrency      int                       `json:"max_concurrency,omitempty"`
	AppID               string                    `json:"app_id,omitempty"`
	Spec                *types.ModelSpecOverride  `json:"spec,omitempty"`
}

// NewModelResponse converts a stored Model into its response shape.
//
// Builtin models are shared across tenants — strip BaseURL (which can leak
// the tenant's private endpoint) and any non-shared parameters.
func NewModelResponse(ctx context.Context, m *types.Model) *ModelResponse {
	if m == nil {
		return nil
	}
	params := ModelParametersDTO{
		BaseURL:             m.Parameters.BaseURL,
		InterfaceType:       m.Parameters.InterfaceType,
		EmbeddingParameters: m.Parameters.EmbeddingParameters,
		ParameterSize:       m.Parameters.ParameterSize,
		Provider:            m.Parameters.Provider,
		ExtraConfig:         m.Parameters.ExtraConfig,
		CustomHeaders:       m.Parameters.CustomHeaders,
		SupportsVision:      m.Parameters.SupportsVision,
		ContextWindow:       m.Parameters.ContextWindow,
		MaxOutputTokens:     m.Parameters.MaxOutputTokens,
		MaxConcurrency:      m.Parameters.MaxConcurrency,
		AppID:               m.Parameters.AppID,
		Spec:                m.Parameters.Spec,
	}
	canManageBuiltin := m.IsBuiltin && types.IsSystemAdminFromContext(ctx)
	if !CanViewIntegrationSecrets(ctx) && !canManageBuiltin {
		params.ExtraConfig = nil
		params.CustomHeaders = nil
		params.BaseURL = ""
		// Spec is deployment configuration of the same kind: it pins the
		// protocol and carries the compat overlay, whose extra_body is
		// merged verbatim into every request. It belongs with ExtraConfig,
		// not with the capability fields kept below.
		params.Spec = nil
	}
	if m.IsBuiltin && !canManageBuiltin {
		// Builtin: strip everything that could reveal per-tenant config.
		// EmbeddingParameters and ParameterSize / Provider / InterfaceType /
		// SupportsVision / ContextWindow / MaxOutputTokens are intentionally
		// preserved (they describe the capability surface, not the configured
		// endpoint).
		params.BaseURL = ""
		params.ExtraConfig = nil
		params.CustomHeaders = nil
		params.AppID = ""
		params.Spec = nil
	}
	// Vendor-declared secret extra fields are credentials that happen to be
	// stored in extra_config. Drop them from the echoed map — even for
	// callers allowed to see integration detail, exactly as api_key is
	// withheld from an admin — and report presence instead. The stored map is
	// never mutated: params.ExtraConfig still aliases it here.
	//
	// Redaction spans every registered vendor, not just this row's: a row
	// whose provider was switched or hand-edited must not leak a leftover
	// credential just because its current vendor declares no secret field.
	secretKeys := SecretExtraConfigKeys(m.Parameters.Provider, m.Parameters.BaseURL)
	redactKeys := AllSecretExtraConfigKeys()
	if len(redactKeys) > 0 && len(params.ExtraConfig) > 0 {
		sanitized := make(map[string]string, len(params.ExtraConfig))
		for k, v := range params.ExtraConfig {
			if !redactKeys[k] {
				sanitized[k] = v
			}
		}
		params.ExtraConfig = sanitized
	}
	var creds map[string]CredentialFieldMetadata
	if !m.IsBuiltin || canManageBuiltin {
		creds = map[string]CredentialFieldMetadata{
			"api_key":    {Configured: m.Parameters.APIKey != ""},
			"app_secret": {Configured: m.Parameters.AppSecret != ""},
		}
		for key := range secretKeys {
			creds[key] = CredentialFieldMetadata{
				Configured: strings.TrimSpace(m.Parameters.ExtraConfig[key]) != "",
			}
		}
		// A key this row's vendor does not declare was still withheld above,
		// so say it is there — otherwise the value silently vanishes from the
		// editor with nothing to explain why.
		for key := range redactKeys {
			if _, reported := creds[key]; reported {
				continue
			}
			if strings.TrimSpace(m.Parameters.ExtraConfig[key]) != "" {
				creds[key] = CredentialFieldMetadata{Configured: true}
			}
		}
	}
	var caps *catalog.Capabilities
	if m.Type == types.ModelTypeKnowledgeQA || m.Type == types.ModelTypeVLLM {
		if resolved, err := catalog.Resolve(catalog.Ref{
			Provider: m.Parameters.Provider, Model: m.Name, BaseURL: m.Parameters.BaseURL,
			ModelType: m.Type, Extra: m.Parameters.ExtraConfig, Override: m.Parameters.Spec,
		}); err == nil && m.Source == types.ModelSourceRemote {
			c := resolved.Capabilities()
			caps = &c
		}
	}
	return &ModelResponse{
		ID:           m.ID,
		TenantID:     m.TenantID,
		Name:         m.Name,
		DisplayName:  m.DisplayName,
		Type:         m.Type,
		Source:       m.Source,
		Description:  m.Description,
		Parameters:   params,
		IsDefault:    m.IsDefault,
		IsBuiltin:    m.IsBuiltin,
		Status:       m.Status,
		CreatedAt:    m.CreatedAt,
		UpdatedAt:    m.UpdatedAt,
		Credentials:  creds,
		Capabilities: caps,
	}
}

// NewModelResponses is the slice convenience wrapper.
func NewModelResponses(ctx context.Context, models []*types.Model) []*ModelResponse {
	out := make([]*ModelResponse, 0, len(models))
	for _, m := range models {
		out = append(out, NewModelResponse(ctx, m))
	}
	return out
}
