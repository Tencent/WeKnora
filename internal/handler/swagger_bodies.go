package handler

// Concrete response bodies for Swagger. Handlers still write these
// shapes with gin.H; the structs exist so the documented JSON matches.

import (
	"time"

	"github.com/Tencent/WeKnora/internal/handler/dto"
	"github.com/Tencent/WeKnora/internal/im"
	"github.com/Tencent/WeKnora/internal/types"
)

// RefreshTokenResponse is the JSON body written by this handler.
type RefreshTokenResponse struct {
	Success      bool   `json:"success" example:"true"`
	Message      string `json:"message"`
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
}

// GetAuthConfigResponse is the JSON body written by this handler.
type GetAuthConfigResponse struct {
	Success                bool   `json:"success" example:"true"`
	RegistrationMode       string `json:"registration_mode"`
	ComplexPasswordEnabled bool   `json:"complex_password_enabled"`
}

// ValidateTokenResponse is the JSON body written by this handler.
type ValidateTokenResponse struct {
	Success bool           `json:"success" example:"true"`
	Message string         `json:"message"`
	User    types.UserInfo `json:"user"`
}

// UpdateChunkResponse is the JSON body written by this handler.
type UpdateChunkResponse struct {
	Success       bool        `json:"success" example:"true"`
	Data          types.Chunk `json:"data"`
	SummaryStatus string      `json:"summary_status"`
	Description   string      `json:"description"`
}

// ListAgentsResponse is the JSON body written by this handler.
type ListAgentsResponse struct {
	Data                []types.CustomAgent `json:"data"`
	DisabledOwnAgentIDs []string            `json:"disabled_own_agent_ids"`
	Success             bool                `json:"success" example:"true"`
}

// GetDeploymentCapabilitiesResponse is the JSON body written by this handler.
type GetDeploymentCapabilitiesResponse struct {
	Code int                        `json:"code"`
	Msg  string                     `json:"msg"`
	Data DeploymentCapabilitiesData `json:"data"`
}

// SuccessResponse is the JSON body written by this handler.
type SuccessResponse struct {
	Success bool `json:"success" example:"true" extensions:"x-agent"`
}

// UpdateIMChannelResponse is the JSON body written by this handler.
type UpdateIMChannelResponse struct {
	Data im.IMChannel `json:"data"`
}

// UpdateKnowledgeResponse is the JSON body written by this handler.
type UpdateKnowledgeResponse struct {
	Message string          `json:"message"`
	Data    types.Knowledge `json:"data"`
	Success bool            `json:"success" example:"true"`
}

// KnowledgeSearchKnowledgeResponse is the JSON body written by this handler.
type KnowledgeSearchKnowledgeResponse struct {
	Total   int               `json:"total"`
	Success bool              `json:"success" example:"true"`
	Data    []types.Knowledge `json:"data"`
	HasMore bool              `json:"has_more"`
}

// ListItemsResponse is the JSON body written by this handler.
type ListItemsResponse struct {
	Total   int                `json:"total"`
	Success bool               `json:"success" example:"true"`
	Data    []types.MemoryItem `json:"data"`
}

// ListTopicsResponse is the JSON body written by this handler.
type ListTopicsResponse struct {
	Success bool                    `json:"success" example:"true"`
	Data    []types.MemoryTopicView `json:"data"`
	Total   int                     `json:"total"`
}

// ListDocumentsResponse is the JSON body written by this handler.
type ListDocumentsResponse struct {
	Total   int                   `json:"total"`
	Success bool                  `json:"success" example:"true"`
	Data    []types.MemoryDocView `json:"data"`
}

// ClearResponse is the JSON body written by this handler.
type ClearResponse struct {
	Success bool `json:"success" example:"true"`
	Removed int  `json:"removed"`
}

// ExportResponse is the JSON body written by this handler.
type ExportResponse struct {
	Data      []types.MemoryItem `json:"data"`
	Success   bool               `json:"success" example:"true"`
	Total     int                `json:"total"`
	Truncated bool               `json:"truncated"`
}

// SearchOrganizationsResponse is the JSON body written by this handler.
type SearchOrganizationsResponse struct {
	Success bool                               `json:"success" example:"true"`
	Data    []types.SearchableOrganizationItem `json:"data"`
	Total   int                                `json:"total"`
}

// ListSharedKnowledgeBasesResponse is the JSON body written by this handler.
type ListSharedKnowledgeBasesResponse struct {
	Total   int                      `json:"total" extensions:"x-agent"`
	Success bool                     `json:"success" example:"true"`
	Data    []SharedKnowledgeBaseRow `json:"data"`
}

// SharedKnowledgeBaseRow is the row sharedKBRow builds.
type SharedKnowledgeBaseRow struct {
	KnowledgeBase  KnowledgeBaseView   `json:"knowledge_base" extensions:"x-agent"`
	ShareID        string              `json:"share_id"`
	OrganizationID string              `json:"organization_id"`
	OrgName        string              `json:"org_name" extensions:"x-agent"`
	Permission     types.OrgMemberRole `json:"permission" extensions:"x-agent"`
	SourceTenantID uint64              `json:"source_tenant_id"`
	SharedAt       time.Time           `json:"shared_at"`
}

// FAQEntryPage is the page of entries the FAQ list writes.
type FAQEntryPage struct {
	Total    int64            `json:"total"`
	Page     int              `json:"page"`
	PageSize int              `json:"page_size"`
	Data     []types.FAQEntry `json:"data"`
}

// TagPageResult is the page of tags the tag list writes.
type TagPageResult struct {
	Total    int64                         `json:"total" extensions:"x-agent"`
	Page     int                           `json:"page" extensions:"x-agent"`
	PageSize int                           `json:"page_size" extensions:"x-agent"`
	Data     []types.KnowledgeTagWithStats `json:"data"`
}

// ListSharedAgentsResponse is the JSON body written by this handler.
type ListSharedAgentsResponse struct {
	Success bool                    `json:"success" example:"true"`
	Data    []types.SharedAgentInfo `json:"data"`
	Total   int                     `json:"total"`
}

// ListOrganizationSharedKnowledgeBasesResponse is the JSON body written by this handler.
type ListOrganizationSharedKnowledgeBasesResponse struct {
	Success bool             `json:"success" example:"true"`
	Data    []map[string]any `json:"data"`
	Total   int              `json:"total"`
}

// ListOrganizationSharedAgentsResponse is the JSON body written by this handler.
type ListOrganizationSharedAgentsResponse struct {
	Success bool                                `json:"success" example:"true"`
	Data    []types.OrganizationSharedAgentItem `json:"data"`
	Total   int                                 `json:"total"`
}

// ListSkillsResponse is the JSON body written by this handler.
type ListSkillsResponse struct {
	Success         bool                `json:"success" example:"true"`
	Data            []SkillInfoResponse `json:"data"`
	SkillsAvailable bool                `json:"skills_available" extensions:"x-agent"`
}

// ListResponse is the JSON body written by this handler.
type ListResponse struct {
	Success                 bool                   `json:"success" example:"true"`
	Data                    []types.StorageBackend `json:"data"`
	DefaultStorageBackendID string                 `json:"default_storage_backend_id"`
}

// SuccessErrorResponse is the JSON body written by this handler.
type SuccessErrorResponse struct {
	Success bool   `json:"success" example:"true"`
	Error   string `json:"error,omitempty"`
}

// ListParserEnginesResponse is the JSON body written by this handler.
type ListParserEnginesResponse struct {
	DocreaderTransport string                   `json:"docreader_transport"`
	Connected          bool                     `json:"connected"`
	Code               int                      `json:"code"`
	Msg                string                   `json:"msg"`
	Data               []types.ParserEngineInfo `json:"data"`
	DocreaderAddr      string                   `json:"docreader_addr"`
}

// SetUserStatusResponse is the JSON body written by this handler.
type SetUserStatusResponse struct {
	User     types.UserInfo `json:"user"`
	IsActive bool           `json:"is_active"`
	Message  string         `json:"message"`
}

// ResetUserPasswordResponse is the JSON body written by this handler.
type ResetUserPasswordResponse struct {
	Message string `json:"message"`
}

// PurgeArchivedRuntimeTasksResponse is the JSON body written by this handler.
type PurgeArchivedRuntimeTasksResponse struct {
	Success bool `json:"success" example:"true"`
	Deleted int  `json:"deleted"`
}

// ApplyDefaultStorageQuotaToAllTenantsResponse is the JSON body written by this handler.
type ApplyDefaultStorageQuotaToAllTenantsResponse struct {
	Affected   int `json:"affected"`
	QuotaBytes int `json:"quota_bytes"`
	QuotaGb    int `json:"quota_gb"`
}

// TestStoreByIDResponse is the JSON body written by this handler.
type TestStoreByIDResponse struct {
	Error   string `json:"error,omitempty"`
	Version string `json:"version"`
	Success bool   `json:"success" example:"true"`
}

// TestStoreRawResponse is the JSON body written by this handler.
type TestStoreRawResponse struct {
	Success bool   `json:"success" example:"true"`
	Error   string `json:"error,omitempty"`
	Version string `json:"version"`
}

// AutoFixResponse is the JSON body written by this handler.
type AutoFixResponse struct {
	Fixed   int    `json:"fixed"`
	Message string `json:"message"`
}

// FolderMoveData is the data object of a knowledge-folder move or rename.
type FolderMoveData struct {
	MovedCount int    `json:"moved_count" extensions:"x-agent"`
	FolderPath string `json:"folder_path" extensions:"x-agent"`
}

// SubmittedTaskData is the data object of an async knowledge batch task.
// task_id is omitted when the knowledge base was already empty.
type SubmittedTaskData struct {
	TaskID       string `json:"task_id,omitempty"`
	DeletedCount int    `json:"deleted_count,omitempty" extensions:"x-agent"`
	ReparseCount int    `json:"reparse_count,omitempty" extensions:"x-agent"`
}

// SubmittedTaskResponse is success, message, and SubmittedTaskData.
type SubmittedTaskResponse struct {
	Success bool              `json:"success" example:"true"`
	Message string            `json:"message"`
	Data    SubmittedTaskData `json:"data"`
}

// TaskIDData is a task id payload.
type TaskIDData struct {
	TaskID string `json:"task_id" extensions:"x-agent"`
}

// AllowedToolsWarningsDoc mirrors allowedToolsWarnings for Swagger.
type AllowedToolsWarningsDoc struct {
	KeptUnknown []string `json:"kept_unknown,omitempty"`
	Dropped     []string `json:"dropped,omitempty"`
}

// AgentWriteResponse is the create/update agent body.
// allowed_tools_warnings is present only when saving rewrote the tool list.
type AgentWriteResponse struct {
	Success              bool                     `json:"success" example:"true"`
	Data                 types.CustomAgent        `json:"data"`
	AllowedToolsWarnings *AllowedToolsWarningsDoc `json:"allowed_tools_warnings,omitempty"`
}

// PlaceholderGroups is the data object of GET /agents/placeholders.
type PlaceholderGroups struct {
	All                 []types.PromptPlaceholder `json:"all"`
	SystemPrompt        []types.PromptPlaceholder `json:"system_prompt"`
	AgentSystemPrompt   []types.PromptPlaceholder `json:"agent_system_prompt"`
	ContextTemplate     []types.PromptPlaceholder `json:"context_template"`
	RewriteSystemPrompt []types.PromptPlaceholder `json:"rewrite_system_prompt"`
	RewritePrompt       []types.PromptPlaceholder `json:"rewrite_prompt"`
	FallbackPrompt      []types.PromptPlaceholder `json:"fallback_prompt"`
}

// SuggestedQuestionsData is the data object of suggested questions.
type SuggestedQuestionsData struct {
	Questions []types.SuggestedQuestion `json:"questions"`
}

// PreferenceDefaults is the preference_defaults object on GET /auth/me.
type PreferenceDefaults struct {
	BrowserSearchInstructions string `json:"browser_search_instructions"`
}

// CurrentUserCapabilities is the capabilities object on GET /auth/me.
type CurrentUserCapabilities struct {
	CanCreateTenant      bool `json:"can_create_tenant"`
	AutoAcceptInvitation bool `json:"auto_accept_invitation"`
}

// CurrentUserData is the data object of GET /auth/me.
type CurrentUserData struct {
	User               types.UserInfo          `json:"user"`
	PreferenceDefaults PreferenceDefaults      `json:"preference_defaults"`
	Tenant             *dto.TenantResponse     `json:"tenant"`
	Memberships        []types.Membership      `json:"memberships"`
	TenantRequired     bool                    `json:"tenant_required"`
	Capabilities       CurrentUserCapabilities `json:"capabilities"`
}

// TenantListData is the data object of tenant list endpoints.
type TenantListData struct {
	Items []*dto.TenantResponse `json:"items"`
}

// TenantSearchData is the data object of GET /tenants/search.
type TenantSearchData struct {
	Items    []*dto.TenantResponse `json:"items"`
	Total    int64                 `json:"total"`
	Page     int                   `json:"page"`
	PageSize int                   `json:"page_size"`
}
