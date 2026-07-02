package dto

import (
	"context"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"gorm.io/gorm"
)

// TenantResponse is the viewer-safe tenant profile shape. Secret-bearing
// columns are omitted or redacted unless the caller has Admin+ (Owner for
// api_key).
type TenantResponse struct {
	ID                  uint64                      `json:"id"`
	Name                string                      `json:"name"`
	Description         string                      `json:"description"`
	APIKey              string                      `json:"api_key,omitempty"`
	Status              string                      `json:"status"`
	RetrieverEngines    types.RetrieverEngines      `json:"retriever_engines"`
	Business            string                      `json:"business"`
	StorageQuota        int64                       `json:"storage_quota"`
	StorageUsed         int64                       `json:"storage_used"`
	ContextConfig       *types.ContextConfig        `json:"context_config,omitempty"`
	WebSearchConfig     *types.WebSearchConfig      `json:"web_search_config,omitempty"`
	ParserEngineConfig  *types.ParserEngineConfig   `json:"parser_engine_config,omitempty"`
	Credentials         *types.CredentialsConfig    `json:"credentials,omitempty"`
	StorageEngineConfig *types.StorageEngineConfig  `json:"storage_engine_config,omitempty"`
	ChatHistoryConfig   *types.ChatHistoryConfig    `json:"chat_history_config,omitempty"`
	RetrievalConfig     *types.RetrievalConfig      `json:"retrieval_config,omitempty"`
	CreatedAt           time.Time                   `json:"created_at"`
	UpdatedAt           time.Time                   `json:"updated_at"`
	DeletedAt           gorm.DeletedAt              `json:"deleted_at"`
}

// NewTenantResponse converts a stored tenant into its HTTP response shape.
func NewTenantResponse(ctx context.Context, tenant *types.Tenant) *TenantResponse {
	if tenant == nil {
		return nil
	}
	includeSecrets := CanViewIntegrationSecrets(ctx)
	includeAPIKey := CanViewTenantAPIKey(ctx)

	resp := &TenantResponse{
		ID:               tenant.ID,
		Name:             tenant.Name,
		Description:      tenant.Description,
		Status:           tenant.Status,
		RetrieverEngines: tenant.RetrieverEngines,
		Business:         tenant.Business,
		StorageQuota:     tenant.StorageQuota,
		StorageUsed:      tenant.StorageUsed,
		ContextConfig:    tenant.ContextConfig,
		ChatHistoryConfig: tenant.ChatHistoryConfig,
		RetrievalConfig:  tenant.RetrievalConfig,
		CreatedAt:        tenant.CreatedAt,
		UpdatedAt:        tenant.UpdatedAt,
		DeletedAt:        tenant.DeletedAt,
	}
	if includeAPIKey {
		resp.APIKey = tenant.APIKey
	}
	if includeSecrets {
		resp.WebSearchConfig = types.WebSearchConfigForResponse(tenant.WebSearchConfig, true)
		resp.ParserEngineConfig = types.ParserEngineConfigForResponse(tenant.ParserEngineConfig, true)
		resp.Credentials = types.CredentialsConfigForResponse(tenant.Credentials, true)
		resp.StorageEngineConfig = types.StorageEngineConfigForResponse(tenant.StorageEngineConfig, true)
	}
	return resp
}

// NewTenantResponses is the slice convenience wrapper.
func NewTenantResponses(ctx context.Context, tenants []*types.Tenant) []*TenantResponse {
	out := make([]*TenantResponse, 0, len(tenants))
	for _, t := range tenants {
		out = append(out, NewTenantResponse(ctx, t))
	}
	return out
}
