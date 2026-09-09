// Package dto holds response shapes for handler responses that need to differ
// from the persisted GORM model — most notably, response types that must NOT
// carry secret fields.
//
// Why a separate package: response DTOs are deliberately distinct from the
// internal model so the "no secret in responses" guarantee is a compile-time
// invariant rather than a runtime redaction step. If a future contributor
// wants to expose a credential in a response, they must add it to the DTO
// explicitly, which makes the leak surface review-able in a single diff.
package dto

import (
	"context"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
)

// MCPServiceResponse mirrors types.MCPService for response bodies, omitting
// every secret field (api_key, token). Credential presence/absence is exposed
// separately via the /credentials subresource endpoint; this shape carries
// only a boolean per credential field so the frontend can render a
// "configured / not configured" badge without an additional round-trip.
type MCPServiceResponse struct {
	UsageInstructions string                   `json:"usage_instructions"`
	ID                string                   `json:"id"`
	TenantID          uint64                   `json:"tenant_id"`
	Name              string                   `json:"name"`
	Description       string                   `json:"description"`
	Enabled           bool                     `json:"enabled"`
	TransportType     types.MCPTransportType   `json:"transport_type"`
	URL               *string                  `json:"url,omitempty"`
	Headers           types.MCPHeaders         `json:"headers,omitempty"`
	AuthConfig        *MCPAuthConfigResponse   `json:"auth_config,omitempty"`
	AdvancedConfig    *types.MCPAdvancedConfig `json:"advanced_config,omitempty"`
	StdioConfig       *types.MCPStdioConfig    `json:"stdio_config,omitempty"`
	EnvVars           types.MCPEnvVars         `json:"env_vars,omitempty"`
	IsBuiltin         bool                     `json:"is_builtin"`
	CreatedAt         time.Time                `json:"created_at"`
	UpdatedAt         time.Time                `json:"updated_at"`
	// Credentials is the per-field "configured?" map. Embedded on the main
	// response so the credential UI doesn't need a follow-up GET. The
	// frontend never sees the actual secret value — only whether one is
	// stored. Omitted entirely for builtin services (they can't have
	// per-tenant credentials).
	Credentials map[string]CredentialFieldMetadata `json:"credentials,omitempty"`
	// Catalog is the persisted tool-directory summary for list cards.
	// Omitted when this principal has never synchronized the service.
	Catalog *MCPCatalogSummary `json:"catalog,omitempty"`
}

// MCPCatalogSummary is the list-card view of a saved MCP directory.
type MCPCatalogSummary struct {
	ToolCount int       `json:"tool_count"`
	Stale     bool      `json:"stale"`
	SyncedAt  time.Time `json:"synced_at"`
}

// MCPAuthConfigResponse intentionally has no APIKey or Token fields. Their
// presence is signalled via MCPServiceResponse.Credentials. AuthType, Scopes
// and AuthServerMetadataURL are non-secret OAuth configuration and are safe to
// echo back so the UI can render the current strategy.
type MCPAuthConfigResponse struct {
	AuthType              types.MCPAuthType `json:"auth_type,omitempty"`
	APIKeyHeader          string            `json:"api_key_header,omitempty"`
	CustomHeaders         map[string]string `json:"custom_headers,omitempty"`
	Scopes                []string          `json:"scopes,omitempty"`
	AuthServerMetadataURL string            `json:"auth_server_metadata_url,omitempty"`
}

// CredentialFieldMetadata reports whether a credential field has a value
// stored server-side, without exposing the value itself.
type CredentialFieldMetadata struct {
	Configured bool `json:"configured"`
}

// NewMCPServiceResponse converts a stored MCPService into its response shape.
//
// Builtin MCP services have their tenant-specific transport details (URL,
// Headers, EnvVars, StdioConfig) stripped — these reveal how the tenant
// configured an upstream provider and must not be visible to other tenants
// that see the same builtin row via the cross-tenant list.
func NewMCPServiceResponse(ctx context.Context, svc *types.MCPService) *MCPServiceResponse {
	if svc == nil {
		return nil
	}
	includeDetail := CanViewIntegrationSecrets(ctx)
	resp := &MCPServiceResponse{
		ID:                svc.ID,
		TenantID:          svc.TenantID,
		Name:              svc.Name,
		Description:       svc.Description,
		UsageInstructions: svc.UsageInstructions,
		Enabled:           svc.Enabled,
		TransportType:     svc.TransportType,
		URL:               svc.URL,
		Headers:           svc.Headers,
		AdvancedConfig:    svc.AdvancedConfig,
		StdioConfig:       svc.StdioConfig,
		EnvVars:           svc.EnvVars,
		IsBuiltin:         svc.IsBuiltin,
		CreatedAt:         svc.CreatedAt,
		UpdatedAt:         svc.UpdatedAt,
	}
	if !includeDetail {
		resp.Headers = nil
		resp.EnvVars = nil
		resp.URL = nil
		resp.StdioConfig = nil
		resp.AdvancedConfig = nil
	}
	if svc.AuthConfig != nil {
		auth := &MCPAuthConfigResponse{
			AuthType:              svc.AuthConfig.AuthType,
			APIKeyHeader:          svc.AuthConfig.APIKeyHeader,
			CustomHeaders:         svc.AuthConfig.CustomHeaders,
			Scopes:                svc.AuthConfig.Scopes,
			AuthServerMetadataURL: svc.AuthConfig.AuthServerMetadataURL,
		}
		if !includeDetail {
			auth.CustomHeaders = nil
		}
		resp.AuthConfig = auth
	}
	if svc.IsBuiltin {
		// Builtin services are shared across tenants — strip everything that
		// could leak how this tenant configured the underlying provider.
		resp.URL = nil
		resp.Headers = nil
		resp.EnvVars = nil
		resp.StdioConfig = nil
		resp.AuthConfig = nil
	} else {
		resp.Credentials = map[string]CredentialFieldMetadata{
			"api_key": {Configured: svc.AuthConfig != nil && svc.AuthConfig.APIKey != ""},
			"token":   {Configured: svc.AuthConfig != nil && svc.AuthConfig.Token != ""},
		}
	}
	return resp
}

// NewMCPServiceResponses is the slice convenience wrapper used by ListMCPServices.
func NewMCPServiceResponses(ctx context.Context, svcs []*types.MCPService) []*MCPServiceResponse {
	out := make([]*MCPServiceResponse, 0, len(svcs))
	for _, s := range svcs {
		out = append(out, NewMCPServiceResponse(ctx, s))
	}
	return out
}

// AttachMCPCatalogs copies persisted directory counts onto list/detail responses.
func AttachMCPCatalogs(
	resp []*MCPServiceResponse,
	services []*types.MCPService,
	summaries map[string]*types.MCPMetadataSummary,
) {
	if len(resp) != len(services) || summaries == nil {
		return
	}
	for i, service := range services {
		if resp[i] == nil || service == nil {
			continue
		}
		summary := summaries[service.ID]
		if summary == nil {
			continue
		}
		resp[i].Catalog = &MCPCatalogSummary{
			ToolCount: summary.ToolCount,
			Stale:     summary.ConfigFingerprint != types.MCPConfigFingerprint(service),
			SyncedAt:  summary.SyncedAt,
		}
	}
}

// CredentialsResponse is the shared shape returned by PUT
// /{resource}/{id}/credentials. Keyed by field name (e.g. "api_key",
// "token"). The frontend uses this to update its in-memory metadata after a
// successful save without needing to re-fetch the whole resource.
type CredentialsResponse struct {
	Fields map[string]CredentialFieldMetadata `json:"fields"`
}
