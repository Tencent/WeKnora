package dto

import (
	"time"

	"github.com/Tencent/WeKnora/internal/types"
)

// WebSearchProviderResponse mirrors types.WebSearchProviderEntity for
// response bodies, with the APIKey field removed by construction. Credential
// presence is exposed via the /credentials subresource.
type WebSearchProviderResponse struct {
	ID          string                             `json:"id"`
	TenantID    uint64                             `json:"tenant_id"`
	Name        string                             `json:"name"`
	Provider    types.WebSearchProviderType        `json:"provider"`
	Description string                             `json:"description"`
	Parameters  WebSearchProviderParametersDTO     `json:"parameters"`
	IsDefault   bool                               `json:"is_default"`
	CreatedAt   time.Time                          `json:"created_at"`
	UpdatedAt   time.Time                          `json:"updated_at"`
	// Per-field "configured?" map. See MCPServiceResponse.Credentials.
	Credentials map[string]CredentialFieldMetadata `json:"credentials,omitempty"`
}

// WebSearchProviderParametersDTO holds every parameter field except APIKey.
// EngineID, BaseURL, ProxyURL and ExtraConfig are not secrets (they describe
// where to send the request, not how to authenticate) and remain visible.
type WebSearchProviderParametersDTO struct {
	EngineID    string            `json:"engine_id,omitempty"`
	BaseURL     string            `json:"base_url,omitempty"`
	ProxyURL    string            `json:"proxy_url,omitempty"`
	ExtraConfig map[string]string `json:"extra_config,omitempty"`
}

// NewWebSearchProviderResponse converts a stored entity into its response shape.
func NewWebSearchProviderResponse(e *types.WebSearchProviderEntity) *WebSearchProviderResponse {
	if e == nil {
		return nil
	}
	return &WebSearchProviderResponse{
		ID:          e.ID,
		TenantID:    e.TenantID,
		Name:        e.Name,
		Provider:    e.Provider,
		Description: e.Description,
		Parameters: WebSearchProviderParametersDTO{
			EngineID:    e.Parameters.EngineID,
			BaseURL:     e.Parameters.BaseURL,
			ProxyURL:    e.Parameters.ProxyURL,
			ExtraConfig: e.Parameters.ExtraConfig,
		},
		IsDefault: e.IsDefault,
		CreatedAt: e.CreatedAt,
		UpdatedAt: e.UpdatedAt,
		Credentials: map[string]CredentialFieldMetadata{
			"api_key": {Configured: e.Parameters.APIKey != ""},
		},
	}
}

func NewWebSearchProviderResponses(es []*types.WebSearchProviderEntity) []*WebSearchProviderResponse {
	out := make([]*WebSearchProviderResponse, 0, len(es))
	for _, e := range es {
		out = append(out, NewWebSearchProviderResponse(e))
	}
	return out
}
