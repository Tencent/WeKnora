package dto

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
)

// DataSourceResponse mirrors types.DataSource for response bodies, with the
// connector Credentials map stripped from the Config jsonb. Credential
// presence is exposed via the dedicated /credentials subresource.
//
// Unlike MCP / Model / WebSearch (which have a flat set of named credential
// fields), DataSource credentials are a per-connector atomic map — an OAuth
// token pair, a Confluence email+token bundle, etc. Splitting them at the
// field level would leave half-configured states that can't actually
// authenticate. The subresource therefore exposes only one logical field,
// "credentials", with PUT replacing the whole map and DELETE wiping it.
type DataSourceResponse struct {
	ID                   string               `json:"id" extensions:"x-agent=data_source_id"`
	TenantID             uint64               `json:"tenant_id"`
	KnowledgeBaseID      string               `json:"knowledge_base_id" extensions:"x-agent"`
	Name                 string               `json:"name" extensions:"x-agent"`
	Type                 string               `json:"type" extensions:"x-agent"`
	Config               *DataSourceConfigDTO `json:"config,omitempty" extensions:"x-agent"`
	SyncSchedule         string               `json:"sync_schedule" extensions:"x-agent"`
	SyncMode             string               `json:"sync_mode" extensions:"x-agent"`
	Status               string               `json:"status" extensions:"x-agent"`
	ConflictStrategy     string               `json:"conflict_strategy" extensions:"x-agent"`
	SyncDeletions        bool                 `json:"sync_deletions" extensions:"x-agent"`
	LastSyncAt           *time.Time           `json:"last_sync_at" extensions:"x-agent"`
	LastSyncCursor       json.RawMessage      `json:"last_sync_cursor,omitempty"`
	LastSyncResult       json.RawMessage      `json:"last_sync_result,omitempty"`
	ErrorMessage         string               `json:"error_message,omitempty"`
	SyncLogRetentionDays int                  `json:"sync_log_retention_days"`
	CreatedAt            time.Time            `json:"created_at"`
	UpdatedAt            time.Time            `json:"updated_at"`
	TotalItemsSynced     int64                `json:"total_items_synced" extensions:"x-agent"`
	LatestSyncLog        *types.SyncLog       `json:"latest_sync_log,omitempty"`
	// Single logical credential field — DataSource credentials are a
	// per-connector atomic map, so "configured?" applies to the whole set.
	Credentials map[string]CredentialFieldMetadata `json:"credentials,omitempty"`
}

// DataSourceConfigDTO is types.DataSourceConfig with the Credentials map
// removed by construction. Type, ResourceIDs and Settings remain visible.
type DataSourceConfigDTO struct {
	Type        string                 `json:"type"`
	ResourceIDs []string               `json:"resource_ids,omitempty" extensions:"x-agent"`
	Settings    map[string]interface{} `json:"settings,omitempty"`
}

// NewDataSourceResponse converts a stored entity into its response shape.
// Returns nil for nil input.
func NewDataSourceResponse(ds *types.DataSource) *DataSourceResponse {
	if ds == nil {
		return nil
	}
	var cfgDTO *DataSourceConfigDTO
	configured := false
	if parsed, err := ds.ParseConfig(); err == nil && parsed != nil {
		cfgDTO = &DataSourceConfigDTO{
			Type:        parsed.Type,
			ResourceIDs: parsed.ResourceIDs,
			Settings:    parsed.Settings,
		}
		enrichRSSFeedURLsInSettings(ds.Type, parsed, cfgDTO)
		configured = parsed.HasConfiguredCredentials(ds.Type)
	}
	return &DataSourceResponse{
		ID:                   ds.ID,
		TenantID:             ds.TenantID,
		KnowledgeBaseID:      ds.KnowledgeBaseID,
		Name:                 ds.Name,
		Type:                 ds.Type,
		Config:               cfgDTO,
		SyncSchedule:         ds.SyncSchedule,
		SyncMode:             ds.SyncMode,
		Status:               ds.Status,
		ConflictStrategy:     ds.ConflictStrategy,
		SyncDeletions:        ds.SyncDeletions,
		LastSyncAt:           ds.LastSyncAt,
		LastSyncCursor:       json.RawMessage(ds.LastSyncCursor),
		LastSyncResult:       json.RawMessage(ds.LastSyncResult),
		ErrorMessage:         ds.ErrorMessage,
		SyncLogRetentionDays: ds.SyncLogRetentionDays,
		CreatedAt:            ds.CreatedAt,
		UpdatedAt:            ds.UpdatedAt,
		TotalItemsSynced:     ds.TotalItemsSynced,
		LatestSyncLog:        ds.LatestSyncLog,
		Credentials: map[string]CredentialFieldMetadata{
			"credentials": {Configured: configured},
		},
	}
}

// enrichRSSFeedURLsInSettings copies feed_urls from credentials into settings
// for API responses. Feed URLs are not secrets but may still live in the
// encrypted credentials blob on rows created before they moved to settings.
func enrichRSSFeedURLsInSettings(dsType string, parsed *types.DataSourceConfig, cfgDTO *DataSourceConfigDTO) {
	if dsType != types.ConnectorTypeRSS || parsed == nil || cfgDTO == nil {
		return
	}
	if cfgDTO.Settings != nil {
		if v, ok := cfgDTO.Settings["feed_urls"].(string); ok && strings.TrimSpace(v) != "" {
			return
		}
	}
	raw, ok := parsed.Credentials["feed_urls"]
	if !ok {
		return
	}
	feedURLs, ok := raw.(string)
	if !ok || strings.TrimSpace(feedURLs) == "" {
		return
	}
	if cfgDTO.Settings == nil {
		cfgDTO.Settings = make(map[string]interface{})
	}
	cfgDTO.Settings["feed_urls"] = feedURLs
}

// NewDataSourceResponses converts a list of entities for the list endpoint.
// The sync cursor is stripped from every element: the settings page polls
// the list every few seconds during a sync, cursors can grow to megabytes,
// and no list consumer reads them. Detail, create and update responses
// still carry the cursor through NewDataSourceResponse.
func NewDataSourceResponses(dss []*types.DataSource) []*DataSourceResponse {
	out := make([]*DataSourceResponse, 0, len(dss))
	for _, d := range dss {
		resp := NewDataSourceResponse(d)
		if resp != nil {
			resp.LastSyncCursor = nil
		}
		out = append(out, resp)
	}
	return out
}
