package handler

import (
	"context"
	"net/http"
	"strconv"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
)

// DataSourceHandler handles HTTP requests for data source management
type DataSourceHandler struct {
	service   interfaces.DataSourceService
	kbService interfaces.KnowledgeBaseService
}

// NewDataSourceHandler creates a new data source handler
func NewDataSourceHandler(
	service interfaces.DataSourceService,
	kbService interfaces.KnowledgeBaseService,
) *DataSourceHandler {
	return &DataSourceHandler{
		service:   service,
		kbService: kbService,
	}
}

// getTenantID safely extracts and validates tenant ID from context
// Returns 0 if tenant ID is not found (caller should return 401)
func (h *DataSourceHandler) getTenantID(c *gin.Context) uint64 {
	tenantID := c.GetUint64(types.TenantIDContextKey.String())
	return tenantID
}

// Data source settings contain connector credentials, so only the owning tenant can access them.
func (h *DataSourceHandler) getOwnedKnowledgeBase(
	ctx context.Context,
	tenantID uint64,
	kbID string,
) (*types.KnowledgeBase, int, string) {
	if kbID == "" {
		return nil, http.StatusBadRequest, "kb_id is required"
	}

	kb, err := h.kbService.GetKnowledgeBaseByID(ctx, kbID)
	if err != nil || kb == nil {
		return nil, http.StatusNotFound, "knowledge base not found"
	}

	if kb.TenantID != tenantID {
		return nil, http.StatusForbidden, "access denied"
	}

	return kb, http.StatusOK, ""
}

func (h *DataSourceHandler) getOwnedDataSource(
	ctx context.Context,
	tenantID uint64,
	id string,
) (*types.DataSource, int, string) {
	ds, err := h.service.GetDataSource(ctx, id)
	if err != nil {
		return nil, http.StatusNotFound, "data source not found"
	}

	if _, status, msg := h.getOwnedKnowledgeBase(ctx, tenantID, ds.KnowledgeBaseID); status != http.StatusOK {
		return nil, status, msg
	}

	return ds, http.StatusOK, ""
}

// CreateDataSource godoc
// @Summary Create a new data source
// @Description Create a new data source configuration for a knowledge base
// @Tags DataSource
// @Accept json
// @Produce json
// @Param request body types.DataSource true "Data source configuration"
// @Success 201 {object} types.DataSource
// @Failure 400 {object} map[string]string
// @Router /api/v1/datasource [post]
func (h *DataSourceHandler) CreateDataSource(c *gin.Context) {
	ctx := c.Request.Context()

	// Extract tenant ID from context (set by auth middleware)
	tenantID := c.GetUint64(types.TenantIDContextKey.String())
	if tenantID == 0 {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized: tenant context missing"})
		return
	}

	var req types.DataSource
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}

	if _, status, msg := h.getOwnedKnowledgeBase(ctx, tenantID, req.KnowledgeBaseID); status != http.StatusOK {
		c.JSON(status, gin.H{"error": msg})
		return
	}

	// Enforce tenant isolation
	req.TenantID = tenantID

	ds, err := h.service.CreateDataSource(ctx, &req)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, ds)
}

// GetDataSource godoc
// @Summary Get a data source by ID
// @Description Retrieve a data source configuration by ID
// @Tags DataSource
// @Produce json
// @Param id path string true "Data source ID"
// @Success 200 {object} types.DataSource
// @Failure 404 {object} map[string]string
// @Router /api/v1/datasource/{id} [get]
func (h *DataSourceHandler) GetDataSource(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID := h.getTenantID(c)
	if tenantID == 0 {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	id := c.Param("id")

	ds, status, msg := h.getOwnedDataSource(ctx, tenantID, id)
	if status != http.StatusOK {
		c.JSON(status, gin.H{"error": msg})
		return
	}

	c.JSON(http.StatusOK, ds)
}

// ListDataSources godoc
// @Summary List data sources for a knowledge base
// @Description List all data sources for a specific knowledge base
// @Tags DataSource
// @Produce json
// @Param kb_id query string true "Knowledge base ID"
// @Success 200 {object} []types.DataSource
// @Failure 400 {object} map[string]string
// @Router /api/v1/datasource [get]
func (h *DataSourceHandler) ListDataSources(c *gin.Context) {
	ctx := c.Request.Context()

	// Extract tenant ID from context
	tenantID := c.GetUint64(types.TenantIDContextKey.String())
	if tenantID == 0 {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized: tenant context missing"})
		return
	}

	kbID := c.Query("kb_id")
	if _, status, msg := h.getOwnedKnowledgeBase(ctx, tenantID, kbID); status != http.StatusOK {
		c.JSON(status, gin.H{"error": msg})
		return
	}

	dataSources, err := h.service.ListDataSources(ctx, kbID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list data sources"})
		return
	}

	if dataSources == nil {
		dataSources = make([]*types.DataSource, 0)
	}
	c.JSON(http.StatusOK, dataSources)
}

// UpdateDataSource godoc
// @Summary Update a data source
// @Description Update an existing data source configuration
// @Tags DataSource
// @Accept json
// @Produce json
// @Param id path string true "Data source ID"
// @Param request body types.DataSource true "Updated configuration"
// @Success 200 {object} types.DataSource
// @Failure 400 {object} map[string]string
// @Router /api/v1/datasource/{id} [put]
func (h *DataSourceHandler) UpdateDataSource(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID := h.getTenantID(c)
	if tenantID == 0 {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	id := c.Param("id")

	var req types.DataSource
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}

	existing, status, msg := h.getOwnedDataSource(ctx, tenantID, id)
	if status != http.StatusOK {
		c.JSON(status, gin.H{"error": msg})
		return
	}

	req.ID = id
	req.TenantID = existing.TenantID
	req.KnowledgeBaseID = existing.KnowledgeBaseID
	ds, err := h.service.UpdateDataSource(ctx, &req)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, ds)
}

// DeleteDataSource godoc
// @Summary Delete a data source
// @Description Delete a data source (soft delete)
// @Tags DataSource
// @Param id path string true "Data source ID"
// @Success 204
// @Failure 404 {object} map[string]string
// @Router /api/v1/datasource/{id} [delete]
func (h *DataSourceHandler) DeleteDataSource(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID := h.getTenantID(c)
	if tenantID == 0 {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	id := c.Param("id")

	if _, status, msg := h.getOwnedDataSource(ctx, tenantID, id); status != http.StatusOK {
		c.JSON(status, gin.H{"error": msg})
		return
	}

	if err := h.service.DeleteDataSource(ctx, id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete data source"})
		return
	}

	c.Status(http.StatusNoContent)
}

// ValidateConnection godoc
// @Summary Test data source connection
// @Description Validate the connection to an external data source
// @Tags DataSource
// @Param id path string true "Data source ID"
// @Success 200 {object} map[string]string
// @Failure 400 {object} map[string]string
// @Router /api/v1/datasource/{id}/validate [post]
func (h *DataSourceHandler) ValidateConnection(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID := h.getTenantID(c)
	if tenantID == 0 {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	id := c.Param("id")

	if _, status, msg := h.getOwnedDataSource(ctx, tenantID, id); status != http.StatusOK {
		c.JSON(status, gin.H{"error": msg})
		return
	}

	if err := h.service.ValidateConnection(ctx, id); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "connected"})
}

// ValidateCredentials godoc
// @Summary Test connection with raw credentials (no persistence)
// @Description Validate connectivity to an external data source using type + credentials
//
//	without creating or updating any database records.
//	Used by the frontend "Test Connection" button during data source creation.
//
// @Tags DataSource
// @Accept json
// @Produce json
// @Param request body object true "type and credentials"
// @Success 200 {object} map[string]string
// @Failure 400 {object} map[string]string
// @Router /api/v1/datasource/validate-credentials [post]
func (h *DataSourceHandler) ValidateCredentials(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID := h.getTenantID(c)
	if tenantID == 0 {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	var req struct {
		Type        string                 `json:"type" binding:"required"`
		Credentials map[string]interface{} `json:"credentials" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request: type and credentials are required"})
		return
	}

	if err := h.service.ValidateCredentials(ctx, req.Type, req.Credentials); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "connected"})
}

// @Summary List available resources in data source
// @Description List resources available for sync in the external system
// @Tags DataSource
// @Produce json
// @Param id path string true "Data source ID"
// @Success 200 {object} []types.Resource
// @Failure 400 {object} map[string]string
// @Router /api/v1/datasource/{id}/resources [get]
func (h *DataSourceHandler) ListAvailableResources(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID := h.getTenantID(c)
	if tenantID == 0 {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	id := c.Param("id")

	if _, status, msg := h.getOwnedDataSource(ctx, tenantID, id); status != http.StatusOK {
		c.JSON(status, gin.H{"error": msg})
		return
	}

	resources, err := h.service.ListAvailableResources(ctx, id)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if resources == nil {
		resources = make([]types.Resource, 0)
	}
	c.JSON(http.StatusOK, resources)
}

// ManualSync godoc
// @Summary Trigger immediate sync
// @Description Trigger an immediate sync for a data source
// @Tags DataSource
// @Param id path string true "Data source ID"
// @Success 200 {object} types.SyncLog
// @Failure 400 {object} map[string]string
// @Router /api/v1/datasource/{id}/sync [post]
func (h *DataSourceHandler) ManualSync(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID := h.getTenantID(c)
	if tenantID == 0 {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	id := c.Param("id")

	if _, status, msg := h.getOwnedDataSource(ctx, tenantID, id); status != http.StatusOK {
		c.JSON(status, gin.H{"error": msg})
		return
	}

	syncLog, err := h.service.ManualSync(ctx, id)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, syncLog)
}

// PauseDataSource godoc
// @Summary Pause data source
// @Description Pause a data source's scheduled syncs
// @Tags DataSource
// @Param id path string true "Data source ID"
// @Success 200 {object} map[string]string
// @Failure 400 {object} map[string]string
// @Router /api/v1/datasource/{id}/pause [post]
func (h *DataSourceHandler) PauseDataSource(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID := h.getTenantID(c)
	if tenantID == 0 {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	id := c.Param("id")

	if _, status, msg := h.getOwnedDataSource(ctx, tenantID, id); status != http.StatusOK {
		c.JSON(status, gin.H{"error": msg})
		return
	}

	if err := h.service.PauseDataSource(ctx, id); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "paused"})
}

// ResumeDataSource godoc
// @Summary Resume data source
// @Description Resume a paused data source
// @Tags DataSource
// @Param id path string true "Data source ID"
// @Success 200 {object} map[string]string
// @Failure 400 {object} map[string]string
// @Router /api/v1/datasource/{id}/resume [post]
func (h *DataSourceHandler) ResumeDataSource(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID := h.getTenantID(c)
	if tenantID == 0 {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	id := c.Param("id")

	if _, status, msg := h.getOwnedDataSource(ctx, tenantID, id); status != http.StatusOK {
		c.JSON(status, gin.H{"error": msg})
		return
	}

	if err := h.service.ResumeDataSource(ctx, id); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "active"})
}

// GetSyncLogs godoc
// @Summary Get sync logs
// @Description Retrieve sync history for a data source
// @Tags DataSource
// @Produce json
// @Param id path string true "Data source ID"
// @Param limit query int false "Limit (default: 10)"
// @Param offset query int false "Offset (default: 0)"
// @Success 200 {object} []types.SyncLog
// @Failure 400 {object} map[string]string
// @Router /api/v1/datasource/{id}/logs [get]
func (h *DataSourceHandler) GetSyncLogs(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID := h.getTenantID(c)
	if tenantID == 0 {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	id := c.Param("id")

	if _, status, msg := h.getOwnedDataSource(ctx, tenantID, id); status != http.StatusOK {
		c.JSON(status, gin.H{"error": msg})
		return
	}

	limit := 10
	offset := 0

	if l := c.Query("limit"); l != "" {
		if v, err := strconv.Atoi(l); err == nil && v > 0 {
			limit = v
		}
	}

	if o := c.Query("offset"); o != "" {
		if v, err := strconv.Atoi(o); err == nil && v >= 0 {
			offset = v
		}
	}

	logs, err := h.service.GetSyncLogs(ctx, id, limit, offset)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if logs == nil {
		logs = make([]*types.SyncLog, 0)
	}
	c.JSON(http.StatusOK, logs)
}

// GetSyncLog godoc
// @Summary Get specific sync log
// @Description Retrieve a specific sync log entry
// @Tags DataSource
// @Produce json
// @Param log_id path string true "Sync log ID"
// @Success 200 {object} types.SyncLog
// @Failure 404 {object} map[string]string
// @Router /api/v1/datasource/logs/{log_id} [get]
func (h *DataSourceHandler) GetSyncLog(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID := h.getTenantID(c)
	if tenantID == 0 {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	logID := c.Param("log_id")

	log, err := h.service.GetSyncLog(ctx, logID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "sync log not found"})
		return
	}

	if _, status, msg := h.getOwnedDataSource(ctx, tenantID, log.DataSourceID); status != http.StatusOK {
		c.JSON(status, gin.H{"error": msg})
		return
	}

	c.JSON(http.StatusOK, log)
}

// GetAvailableConnectors godoc
// @Summary Get available connectors
// @Description Get list of available data source connectors
// @Tags DataSource
// @Produce json
// @Success 200 {object} []datasource.ConnectorMetadata
// @Router /api/v1/datasource/types [get]
func (h *DataSourceHandler) GetAvailableConnectors(c *gin.Context) {
	connectors := datasource.ListAvailableConnectors()
	c.JSON(http.StatusOK, connectors)
}
