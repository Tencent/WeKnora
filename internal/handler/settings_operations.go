package handler

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/handler/dto"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
)

// PatchWebSearchProviderRequest changes display settings without replacing connection parameters.
type PatchWebSearchProviderRequest struct {
	// Display name; cannot be blank.
	Name *string `json:"name"        extensions:"x-agent"`
	// Description; empty clears it.
	Description *string `json:"description" extensions:"x-agent"`
	// Whether agents without an explicit provider use this service by default.
	IsDefault *bool `json:"is_default"  extensions:"x-agent"`
}

// PatchProvider godoc
// @Summary 修改网络搜索服务设置
// @Tags 网络搜索
// @Accept json
// @Produce json
// @Param id path string true "搜索服务 ID" extensions(x-agent=web_search_provider_id)
// @Param request body PatchWebSearchProviderRequest true "需要修改的字段"
// @Success 200 {object} DataResponse{data=dto.WebSearchProviderResponse}
// @Security Bearer
// @Security ApiKeyAuth
// @x-agent {"id":"web_search_provider.update","risk":"write","role":"admin","target":"web_search_provider.get","label":"name"}
// @x-agent-hint "Changes name, description or workspace default only; omitted fields and all saved connection parameters and credentials are preserved. Making this the default affects agents that follow the workspace default, not agents with an explicit provider. Providers have no enabled flag: toggle web_search_enabled on the agent instead."
// @Router /web-search-providers/{id} [patch]
func (h *WebSearchProviderHandler) PatchProvider(c *gin.Context) {
	tenant := h.getTenantID(c)
	if tenant == 0 {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "workspace context missing"})
		return
	}
	var patch PatchWebSearchProviderRequest
	if err := c.ShouldBindJSON(&patch); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid settings"})
		return
	}
	if patch.Name == nil && patch.Description == nil && patch.IsDefault == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "at least one setting is required"})
		return
	}
	existing, status, msg := h.getOwnedProvider(c.Request.Context(), tenant, c.Param("id"))
	if status != http.StatusOK {
		c.JSON(status, gin.H{"error": msg})
		return
	}
	updated := *existing
	var fields []string
	if patch.Name != nil {
		fields = append(fields, "name")
		updated.Name = strings.TrimSpace(*patch.Name)
		if updated.Name == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "name cannot be empty"})
			return
		}
	}
	if patch.Description != nil {
		fields = append(fields, "description")
		updated.Description = *patch.Description
	}
	if patch.IsDefault != nil {
		fields = append(fields, "is_default")
		updated.IsDefault = *patch.IsDefault
	}
	if err := h.service.PatchProviderSettings(c.Request.Context(), &updated, fields); err != nil {
		c.JSON(
			http.StatusBadRequest,
			gin.H{"error": "Could not update search provider; check its saved configuration."},
		)
		return
	}
	c.JSON(
		http.StatusOK,
		gin.H{"success": true, "data": dto.NewWebSearchProviderResponse(c.Request.Context(), &updated)},
	)
}

// WebSearchProviderTestResponse reports a credential-safe connection test result.
type WebSearchProviderTestResponse struct {
	Success bool   `json:"success"         extensions:"x-agent"`
	Error   string `json:"error,omitempty" extensions:"x-agent"`
}

// PatchDataSourceSyncRequest updates only the named synchronization settings.
type PatchDataSourceSyncRequest struct {
	// Six-field cron including seconds, optionally prefixed with CRON_TZ=Area/City. Empty disables scheduled sync.
	SyncSchedule *string `json:"sync_schedule"     extensions:"x-agent"`
	// Incremental sync or a full scan.
	SyncMode *string `json:"sync_mode"         extensions:"x-agent"                    enums:"incremental,full"`
	// Overwrite existing content or skip conflicts.
	ConflictStrategy *string `json:"conflict_strategy" extensions:"x-agent"                    enums:"overwrite,skip"`
	// Whether future syncs delete documents that were removed upstream.
	SyncDeletions *bool `json:"sync_deletions"    extensions:"x-agent"`
	// Replaces the complete selection. Get IDs from data_source.list_resources; never infer them from names.
	ResourceIDs *[]string `json:"resource_ids"      extensions:"x-agent,x-agent-maximum=50"`
}

func (p PatchDataSourceSyncRequest) apply(existing *types.DataSource) (*types.DataSource, error) {
	if p.SyncSchedule == nil && p.SyncMode == nil && p.ConflictStrategy == nil && p.SyncDeletions == nil &&
		p.ResourceIDs == nil {
		return nil, fmt.Errorf("at least one sync setting is required")
	}
	updated := *existing
	if p.SyncSchedule != nil {
		updated.SyncSchedule = strings.TrimSpace(*p.SyncSchedule)
		if err := datasource.ValidateSyncSchedule(updated.SyncSchedule); err != nil {
			return nil, err
		}
	}
	if p.SyncMode != nil {
		if *p.SyncMode != "incremental" && *p.SyncMode != "full" {
			return nil, fmt.Errorf("sync_mode must be incremental or full")
		}
		updated.SyncMode = *p.SyncMode
	}
	if p.ConflictStrategy != nil {
		if *p.ConflictStrategy != "overwrite" && *p.ConflictStrategy != "skip" {
			return nil, fmt.Errorf("conflict_strategy must be overwrite or skip")
		}
		updated.ConflictStrategy = *p.ConflictStrategy
	}
	if p.SyncDeletions != nil {
		updated.SyncDeletions = *p.SyncDeletions
	}
	if p.ResourceIDs != nil {
		if len(*p.ResourceIDs) == 0 || len(*p.ResourceIDs) > 50 {
			return nil, fmt.Errorf("resource_ids must contain 1 to 50 explicit resource IDs")
		}
		seen := map[string]bool{}
		for _, id := range *p.ResourceIDs {
			if strings.TrimSpace(id) == "" || id != strings.TrimSpace(id) || seen[id] {
				return nil, fmt.Errorf("resource_ids must be nonempty, unique IDs")
			}
			seen[id] = true
		}
		// Keep connector settings as raw JSON. Credentials are merged by the service,
		// so the partial update never decrypts or replaces them.
		var cfg map[string]json.RawMessage
		if err := json.Unmarshal(existing.Config, &cfg); err != nil || cfg == nil {
			return nil, fmt.Errorf("saved connector configuration is invalid")
		}
		delete(cfg, "credentials")
		cfg["resource_ids"], _ = json.Marshal(*p.ResourceIDs)
		blob, err := json.Marshal(cfg)
		if err != nil {
			return nil, err
		}
		updated.Config = types.JSON(blob)
	}
	return &updated, nil
}

// PatchSyncSettings godoc
// @Summary 修改数据源同步配置
// @Tags DataSource
// @Accept json
// @Produce json
// @Param id path string true "数据源 ID" extensions(x-agent=data_source_id)
// @Param request body PatchDataSourceSyncRequest true "需要修改的同步配置"
// @Success 200 {object} dto.DataSourceResponse
// @Security Bearer
// @Security ApiKeyAuth
// @x-agent {"id":"data_source.update_sync","risk":"write","role":"admin","target":"data_source.get","label":"name"}
// @x-agent-hint "Partially updates saved sync settings; does not start a sync or resume a paused source. Schedule uses SIX cron fields (seconds first), e.g. CRON_TZ=Asia/Shanghai 0 0 9 * * * for 09:00 daily in Shanghai; use the user's timezone, or clarify it, rather than assume server time. Empty schedule disables automatic sync. resource_ids replaces the WHOLE selection (1-50 explicit IDs from list_resources); preserve intended existing selections and do not infer all from truncated results. overwrite may replace edited content; sync_deletions true allows future syncs to delete documents removed upstream. These settings affect future scheduled/manual runs. Credentials, connector-specific settings and unspecified fields stay unchanged."
// @Router /datasource/{id}/sync-settings [patch]
func (h *DataSourceHandler) PatchSyncSettings(c *gin.Context) {
	tenant := h.getTenantID(c)
	if tenant == 0 {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "workspace context missing"})
		return
	}
	var patch PatchDataSourceSyncRequest
	if err := c.ShouldBindJSON(&patch); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid sync settings"})
		return
	}
	existing, status, msg := h.getOwnedDataSource(c.Request.Context(), tenant, c.Param("id"))
	if status != http.StatusOK {
		c.JSON(status, gin.H{"error": msg})
		return
	}
	updated, err := patch.apply(existing)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	fields := []string{}
	for name, present := range map[string]bool{
		"sync_schedule": patch.SyncSchedule != nil, "sync_mode": patch.SyncMode != nil,
		"conflict_strategy": patch.ConflictStrategy != nil, "sync_deletions": patch.SyncDeletions != nil,
		"config": patch.ResourceIDs != nil,
	} {
		if present {
			fields = append(fields, name)
		}
	}
	if patch.ResourceIDs == nil {
		// A schedule-only change must not rewrite or validate connector configuration.
		updated.Config = nil
	}
	saved, err := h.service.PatchDataSourceSyncSettings(c.Request.Context(), existing, updated, fields)
	if errors.Is(err, types.ErrDataSourceSettingsConflict) {
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}
	if err != nil {
		c.JSON(
			http.StatusBadRequest,
			gin.H{"error": "Could not update sync settings; check the saved connector configuration."},
		)
		return
	}
	c.JSON(http.StatusOK, dto.NewDataSourceResponse(saved))
}
