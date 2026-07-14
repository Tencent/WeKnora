// Package client provides the implementation for interacting with the WeKnora API
// The Tenant related interfaces are used to manage tenants in the system
// Tenants can be created, retrieved, updated, deleted, and queried
// They can also be used to manage retriever engines for different tasks
package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// RetrieverEngines defines a collection of retriever engine parameters
type RetrieverEngines struct {
	Engines []RetrieverEngineParams `json:"engines"`
}

// RetrieverEngineParams contains configuration for retriever engines
type RetrieverEngineParams struct {
	RetrieverType       string `json:"retriever_type"`        // Type of retriever (e.g., keywords, vector)
	RetrieverEngineType string `json:"retriever_engine_type"` // Type of engine implementing the retriever
}

// Tenant represents tenant information in the system
type Tenant struct {
	ID uint64 `yaml:"id"                json:"id"                gorm:"primaryKey"`
	// Tenant name
	Name string `yaml:"name"              json:"name"`
	// Tenant description
	Description string `yaml:"description"       json:"description"`
	// Tenant status (active, inactive)
	Status string `yaml:"status"            json:"status"            gorm:"default:'active'"`
	// Configured retrieval engines
	RetrieverEngines RetrieverEngines `yaml:"retriever_engines" json:"retriever_engines" gorm:"type:json"`
	// Business/department information
	Business string `yaml:"business"          json:"business"`
	// Storage quota (Bytes), default is 10GB
	StorageQuota int64 `yaml:"storage_quota"     json:"storage_quota"     gorm:"default:10737418240"`
	// Storage used (Bytes)
	StorageUsed int64 `yaml:"storage_used"      json:"storage_used"      gorm:"default:0"`
	// APIKey is only populated by CreateTenant when the server has
	// tenant.auto_create_api_key (env WEKNORA_TENANT_AUTO_CREATE_API_KEY)
	// enabled: it carries the plaintext token of an auto-created full_access
	// key. Empty otherwise. Save it on receipt — it is never returned again.
	APIKey string `yaml:"api_key,omitempty" json:"api_key,omitempty"`
	// Creation timestamp
	CreatedAt time.Time `yaml:"created_at"        json:"created_at"`
	// Last update timestamp
	UpdatedAt time.Time `yaml:"updated_at"        json:"updated_at"`
}

// TenantResponse represents the API response structure for tenant operations
type TenantResponse struct {
	Success bool   `json:"success"` // Whether the operation was successful
	Data    Tenant `json:"data"`    // Tenant data
}

// TenantListResponse represents the API response structure for listing tenants
type TenantListResponse struct {
	Success bool `json:"success"` // Whether the operation was successful
	Data    struct {
		Items []Tenant `json:"items"` // List of tenant items
	} `json:"data"`
}

// TenantAPIKeyRole is the tenant RBAC role bound to a revocable API key.
type TenantAPIKeyRole string

const (
	TenantAPIKeyRoleViewer      TenantAPIKeyRole = "viewer"
	TenantAPIKeyRoleContributor TenantAPIKeyRole = "contributor"
	TenantAPIKeyRoleAdmin       TenantAPIKeyRole = "admin"
)

// TenantAPIKey is the API key metadata returned by list/create APIs.
type TenantAPIKey struct {
	ID               uint64           `json:"id"`
	TenantID         uint64           `json:"tenant_id"`
	Name             string           `json:"name"`
	APIKey           string           `json:"api_key"`
	Role             TenantAPIKeyRole `json:"role"`
	KnowledgeBaseIDs []string         `json:"knowledge_base_ids"`
	LastUsedAt       *time.Time          `json:"last_used_at,omitempty"`
	ExpiresAt        *time.Time          `json:"expires_at,omitempty"`
	CreatedAt        time.Time           `json:"created_at"`
	UpdatedAt        time.Time           `json:"updated_at"`
}

// CreateTenantAPIKeyRequest creates a revocable tenant API key.
type CreateTenantAPIKeyRequest struct {
	Name             string           `json:"name"`
	Role             TenantAPIKeyRole `json:"role,omitempty"`
	KnowledgeBaseIDs []string         `json:"knowledge_base_ids,omitempty"`
	ExpiresAtUnix    *int64           `json:"expires_at_unix,omitempty"`
}

// CreatedTenantAPIKey includes the created API key. Token is kept for
// backward-compatible clients; APIKey is also returned by list APIs.
type CreatedTenantAPIKey struct {
	TenantAPIKey
	Token string `json:"token,omitempty"`
}

type tenantAPIKeyListResponse struct {
	Success bool           `json:"success"`
	Data    []TenantAPIKey `json:"data"`
}

type tenantAPIKeyCreateResponse struct {
	Success bool                `json:"success"`
	Data    CreatedTenantAPIKey `json:"data"`
}

// CreateTenant creates a new tenant
func (c *Client) CreateTenant(ctx context.Context, tenant *Tenant) (*Tenant, error) {
	resp, err := c.doRequest(ctx, http.MethodPost, "/api/v1/tenants", tenant, nil)
	if err != nil {
		return nil, err
	}

	var response TenantResponse
	if err := parseResponse(resp, &response); err != nil {
		return nil, err
	}

	return &response.Data, nil
}

// GetTenant retrieves a tenant by ID
func (c *Client) GetTenant(ctx context.Context, tenantID uint64) (*Tenant, error) {
	path := fmt.Sprintf("/api/v1/tenants/%d", tenantID)
	resp, err := c.doRequest(ctx, http.MethodGet, path, nil, nil)
	if err != nil {
		return nil, err
	}

	var response TenantResponse
	if err := parseResponse(resp, &response); err != nil {
		return nil, err
	}

	return &response.Data, nil
}

// UpdateTenant updates an existing tenant
func (c *Client) UpdateTenant(ctx context.Context, tenant *Tenant) (*Tenant, error) {
	path := fmt.Sprintf("/api/v1/tenants/%d", tenant.ID)
	resp, err := c.doRequest(ctx, http.MethodPut, path, tenant, nil)
	if err != nil {
		return nil, err
	}

	var response TenantResponse
	if err := parseResponse(resp, &response); err != nil {
		return nil, err
	}

	return &response.Data, nil
}

// DeleteTenant removes a tenant by ID
func (c *Client) DeleteTenant(ctx context.Context, tenantID uint64) error {
	path := fmt.Sprintf("/api/v1/tenants/%d", tenantID)
	resp, err := c.doRequest(ctx, http.MethodDelete, path, nil, nil)
	if err != nil {
		return err
	}

	var response struct {
		Success bool   `json:"success"`
		Message string `json:"message,omitempty"`
	}

	return parseResponse(resp, &response)
}

// ListTenants retrieves all tenants
func (c *Client) ListTenants(ctx context.Context) ([]Tenant, error) {
	resp, err := c.doRequest(ctx, http.MethodGet, "/api/v1/tenants", nil, nil)
	if err != nil {
		return nil, err
	}

	var response TenantListResponse
	if err := parseResponse(resp, &response); err != nil {
		return nil, err
	}

	return response.Data.Items, nil
}

// ListAllTenants retrieves all tenants in the system (requires cross-tenant access)
func (c *Client) ListAllTenants(ctx context.Context) ([]Tenant, error) {
	resp, err := c.doRequest(ctx, http.MethodGet, "/api/v1/tenants/all", nil, nil)
	if err != nil {
		return nil, err
	}

	var response TenantListResponse
	if err := parseResponse(resp, &response); err != nil {
		return nil, err
	}

	return response.Data.Items, nil
}

// TenantSearchResponse represents the API response for searching tenants
type TenantSearchResponse struct {
	Success bool `json:"success"`
	Data    struct {
		Items    []Tenant `json:"items"`
		Total    int64    `json:"total"`
		Page     int      `json:"page"`
		PageSize int      `json:"page_size"`
	} `json:"data"`
}

// SearchTenants searches tenants with pagination (requires cross-tenant access)
func (c *Client) SearchTenants(ctx context.Context, keyword string, tenantID uint64, page, pageSize int) ([]Tenant, int64, error) {
	queryParams := url.Values{}
	if keyword != "" {
		queryParams.Set("keyword", keyword)
	}
	if tenantID > 0 {
		queryParams.Set("tenant_id", strconv.FormatUint(tenantID, 10))
	}
	queryParams.Set("page", strconv.Itoa(page))
	queryParams.Set("page_size", strconv.Itoa(pageSize))

	resp, err := c.doRequest(ctx, http.MethodGet, "/api/v1/tenants/search", nil, queryParams)
	if err != nil {
		return nil, 0, err
	}

	var response TenantSearchResponse
	if err := parseResponse(resp, &response); err != nil {
		return nil, 0, err
	}

	return response.Data.Items, response.Data.Total, nil
}

// ListTenantAPIKeys lists API keys for a tenant.
func (c *Client) ListTenantAPIKeys(ctx context.Context, tenantID uint64) ([]TenantAPIKey, error) {
	path := fmt.Sprintf("/api/v1/tenants/%d/api-keys", tenantID)
	resp, err := c.doRequest(ctx, http.MethodGet, path, nil, nil)
	if err != nil {
		return nil, err
	}

	var response tenantAPIKeyListResponse
	if err := parseResponse(resp, &response); err != nil {
		return nil, err
	}
	return response.Data, nil
}

// CreateTenantAPIKey creates a scoped API key.
func (c *Client) CreateTenantAPIKey(
	ctx context.Context, tenantID uint64, req *CreateTenantAPIKeyRequest,
) (*CreatedTenantAPIKey, error) {
	path := fmt.Sprintf("/api/v1/tenants/%d/api-keys", tenantID)
	resp, err := c.doRequest(ctx, http.MethodPost, path, req, nil)
	if err != nil {
		return nil, err
	}

	var response tenantAPIKeyCreateResponse
	if err := parseResponse(resp, &response); err != nil {
		return nil, err
	}
	return &response.Data, nil
}

// DeleteTenantAPIKey revokes a tenant API key.
func (c *Client) DeleteTenantAPIKey(ctx context.Context, tenantID uint64, keyID uint64) error {
	path := fmt.Sprintf("/api/v1/tenants/%d/api-keys/%d", tenantID, keyID)
	resp, err := c.doRequest(ctx, http.MethodDelete, path, nil, nil)
	if err != nil {
		return err
	}

	var response struct {
		Success bool   `json:"success"`
		Message string `json:"message,omitempty"`
	}
	return parseResponse(resp, &response)
}

// GetTenantKV retrieves a tenant KV configuration by key
func (c *Client) GetTenantKV(ctx context.Context, key string) (json.RawMessage, error) {
	path := fmt.Sprintf("/api/v1/tenants/kv/%s", key)
	resp, err := c.doRequest(ctx, http.MethodGet, path, nil, nil)
	if err != nil {
		return nil, err
	}

	var result struct {
		Success bool            `json:"success"`
		Data    json.RawMessage `json:"data"`
	}
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}
	return result.Data, nil
}

// UpdateTenantKV updates a tenant KV configuration by key
func (c *Client) UpdateTenantKV(ctx context.Context, key string, value any) (json.RawMessage, error) {
	path := fmt.Sprintf("/api/v1/tenants/kv/%s", key)
	resp, err := c.doRequest(ctx, http.MethodPut, path, value, nil)
	if err != nil {
		return nil, err
	}

	var result struct {
		Success bool            `json:"success"`
		Data    json.RawMessage `json:"data"`
	}
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}
	return result.Data, nil
}

// APIPrincipalMode controls how X-API-Key requests map to terminal principals.
type APIPrincipalMode string

const (
	APIPrincipalModeTenant      APIPrincipalMode = "tenant"
	APIPrincipalModeDirect      APIPrincipalMode = "direct_header"
	APIPrincipalModeSignedToken APIPrincipalMode = "signed_token"
)

// APIPrincipalConfig describes tenant API-key principal mapping settings.
type APIPrincipalConfig struct {
	Mode                  APIPrincipalMode `json:"mode"`
	DirectHeaderName      string           `json:"direct_header_name"`
	SignedTokenHeaderName string           `json:"signed_token_header_name"`
	RequireDirectHeader   bool             `json:"require_direct_header"`
	HasHMACSecret         bool             `json:"has_hmac_secret"`
	HMACSecret            string           `json:"hmac_secret,omitempty"`
}

type apiPrincipalConfigResponse struct {
	Success bool               `json:"success"`
	Data    APIPrincipalConfig `json:"data"`
}

// UpdateAPIPrincipalConfigRequest updates tenant API-key principal mapping.
type UpdateAPIPrincipalConfigRequest struct {
	Mode                  APIPrincipalMode `json:"mode"`
	DirectHeaderName      string           `json:"direct_header_name,omitempty"`
	SignedTokenHeaderName string           `json:"signed_token_header_name,omitempty"`
	RequireDirectHeader   bool             `json:"require_direct_header,omitempty"`
	HMACSecret            string           `json:"hmac_secret,omitempty"`
}

// CreateAPIPrincipalTestTokenRequest signs a short-lived JWT for API integration testing.
type CreateAPIPrincipalTestTokenRequest struct {
	ExternalUserID   string `json:"external_user_id"`
	ExpiresInSeconds int    `json:"expires_in_seconds,omitempty"`
}

// APIPrincipalTestToken is a short-lived JWT signed with the tenant API principal HMAC secret.
type APIPrincipalTestToken struct {
	Token            string `json:"token"`
	HeaderName       string `json:"header_name"`
	ExpiresInSeconds int    `json:"expires_in_seconds"`
	ExpiresAtUnix    int64  `json:"expires_at_unix"`
	ExternalUserID   string `json:"external_user_id"`
}

type apiPrincipalTestTokenResponse struct {
	Success bool                  `json:"success"`
	Data    APIPrincipalTestToken `json:"data"`
}

// GetAPIPrincipalConfig returns how X-API-Key requests map to principals for a tenant.
func (c *Client) GetAPIPrincipalConfig(ctx context.Context, tenantID uint64) (*APIPrincipalConfig, error) {
	path := fmt.Sprintf("/api/v1/tenants/%d/api-principal-config", tenantID)
	resp, err := c.doRequest(ctx, http.MethodGet, path, nil, nil)
	if err != nil {
		return nil, err
	}

	var response apiPrincipalConfigResponse
	if err := parseResponse(resp, &response); err != nil {
		return nil, err
	}
	return &response.Data, nil
}

// UpdateAPIPrincipalConfig updates how X-API-Key requests map to principals for a tenant.
func (c *Client) UpdateAPIPrincipalConfig(
	ctx context.Context, tenantID uint64, req *UpdateAPIPrincipalConfigRequest,
) (*APIPrincipalConfig, error) {
	path := fmt.Sprintf("/api/v1/tenants/%d/api-principal-config", tenantID)
	resp, err := c.doRequest(ctx, http.MethodPut, path, req, nil)
	if err != nil {
		return nil, err
	}

	var response apiPrincipalConfigResponse
	if err := parseResponse(resp, &response); err != nil {
		return nil, err
	}
	return &response.Data, nil
}

// CreateAPIPrincipalTestToken signs a short-lived JWT with the tenant API principal HMAC secret.
func (c *Client) CreateAPIPrincipalTestToken(
	ctx context.Context, tenantID uint64, req *CreateAPIPrincipalTestTokenRequest,
) (*APIPrincipalTestToken, error) {
	path := fmt.Sprintf("/api/v1/tenants/%d/api-principal-test-token", tenantID)
	resp, err := c.doRequest(ctx, http.MethodPost, path, req, nil)
	if err != nil {
		return nil, err
	}

	var response apiPrincipalTestTokenResponse
	if err := parseResponse(resp, &response); err != nil {
		return nil, err
	}
	return &response.Data, nil
}
