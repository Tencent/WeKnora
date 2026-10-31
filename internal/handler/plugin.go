package handler

import (
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/Tencent/WeKnora/internal/application/service"
	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
)

// PluginHandler handles HTTP endpoints for plugin marketplaces and plugin management.
type PluginHandler struct {
	pluginSvc *service.PluginService
}

// NewPluginHandler creates a new PluginHandler.
func NewPluginHandler(pluginSvc *service.PluginService) *PluginHandler {
	return &PluginHandler{
		pluginSvc: pluginSvc,
	}
}

func getTenantID(c *gin.Context) (uint64, error) {
	tenantID := c.GetUint64(types.TenantIDContextKey.String())
	if tenantID == 0 {
		return 0, apperrors.NewUnauthorizedError("Workspace ID cannot be empty")
	}
	return tenantID, nil
}

// AddMarketplaceRequest is the body for adding a marketplace via URL source.
type AddMarketplaceRequest struct {
	Source string `json:"source" binding:"required"`
}

// InstallPluginRequest is the body for installing a plugin.
type InstallPluginRequest struct {
	SandboxConfigIDs []string          `json:"sandbox_config_ids"`
	Env              map[string]string `json:"env"`
}

// UpdatePluginSandboxesRequest is the body for modifying target sandboxes of an installed plugin.
type UpdatePluginSandboxesRequest struct {
	SandboxConfigIDs []string `json:"sandbox_config_ids" binding:"required"`
}

// ListMarketplaces godoc
// @Summary      List marketplaces
// @Description  Returns all plugin marketplaces configured for this workspace.
// @Tags         Plugins
// @Produce      json
// @Success      200  {object}  handler.DataResponse{data=[]service.MarketplaceDetailView}
// @Router       /plugin-marketplaces [get]
func (h *PluginHandler) ListMarketplaces(c *gin.Context) {
	tenantID, err := getTenantID(c)
	if err != nil {
		respondSkillServiceError(c, err)
		return
	}
	list, err := h.pluginSvc.ListMarketplaces(c.Request.Context(), tenantID)
	if err != nil {
		respondSkillServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": list})
}

// AddMarketplace godoc
// @Summary      Add marketplace
// @Description  Adds a plugin marketplace from an uploaded archive file or git/URL source.
// @Tags         Plugins
// @Accept       multipart/form-data,json
// @Produce      json
// @Success      200  {object}  handler.DataResponse{data=types.PluginMarketplaceEntity}
// @Router       /plugin-marketplaces [post]
func (h *PluginHandler) AddMarketplace(c *gin.Context) {
	tenantID, err := getTenantID(c)
	if err != nil {
		respondSkillServiceError(c, err)
		return
	}

	contentType := c.GetHeader("Content-Type")
	if strings.HasPrefix(contentType, "multipart/form-data") {
		fileHeader, err := c.FormFile("file")
		if err != nil {
			_ = c.Error(apperrors.NewBadRequestError("missing file in form-data"))
			return
		}
		f, err := fileHeader.Open()
		if err != nil {
			_ = c.Error(apperrors.NewBadRequestError("cannot open uploaded file"))
			return
		}
		defer func() { _ = f.Close() }()

		if fileHeader.Size > service.MaxMarketplaceArchiveBytes {
			_ = c.Error(apperrors.NewBadRequestError("uploaded archive is too large"))
			return
		}
		data, err := io.ReadAll(io.LimitReader(f, service.MaxMarketplaceArchiveBytes+1))
		if err != nil {
			_ = c.Error(apperrors.NewBadRequestError("cannot read uploaded file"))
			return
		}
		if int64(len(data)) > service.MaxMarketplaceArchiveBytes {
			_ = c.Error(apperrors.NewBadRequestError("uploaded archive is too large"))
			return
		}

		mp, err := h.pluginSvc.AddMarketplaceFromArchive(c.Request.Context(), tenantID, data)
		if err != nil {
			respondSkillServiceError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"success": true, "data": mp})
		return
	}

	var req AddMarketplaceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		_ = c.Error(apperrors.NewBadRequestError("invalid request body: " + err.Error()))
		return
	}

	mp, err := h.pluginSvc.AddMarketplaceFromSource(c.Request.Context(), tenantID, req.Source)
	if err != nil {
		respondSkillServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": mp})
}

// GetMarketplace godoc
// @Summary      Get marketplace
// @Description  Returns detailed plugin list and information for a specific marketplace.
// @Tags         Plugins
// @Produce      json
// @Success      200  {object}  handler.DataResponse{data=service.MarketplaceDetailView}
// @Router       /plugin-marketplaces/:id [get]
func (h *PluginHandler) GetMarketplace(c *gin.Context) {
	tenantID, err := getTenantID(c)
	if err != nil {
		respondSkillServiceError(c, err)
		return
	}
	id := c.Param("id")
	mp, err := h.pluginSvc.GetMarketplace(c.Request.Context(), tenantID, id)
	if err != nil {
		respondSkillServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": mp})
}

// DeleteMarketplace godoc
// @Summary      Delete marketplace
// @Description  Deletes a marketplace when no installed plugins remain.
// @Tags         Plugins
// @Produce      json
// @Success      200  {object}  handler.DataResponse{data=bool}
// @Router       /plugin-marketplaces/:id [delete]
func (h *PluginHandler) DeleteMarketplace(c *gin.Context) {
	tenantID, err := getTenantID(c)
	if err != nil {
		respondSkillServiceError(c, err)
		return
	}
	id := c.Param("id")
	if err := h.pluginSvc.DeleteMarketplace(c.Request.Context(), tenantID, id); err != nil {
		respondSkillServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": true})
}

// PreflightPlugin godoc
// @Summary      Plugin install preflight
// @Description  Returns required env declarations and metadata before installing a plugin.
// @Tags         Plugins
// @Produce      json
// @Success      200  {object}  handler.DataResponse{data=service.PluginPreflightResponse}
// @Router       /plugin-marketplaces/:id/plugins/:name [get]
func (h *PluginHandler) PreflightPlugin(c *gin.Context) {
	tenantID, err := getTenantID(c)
	if err != nil {
		respondSkillServiceError(c, err)
		return
	}
	mpID := c.Param("id")
	name := c.Param("name")
	res, err := h.pluginSvc.PreflightPlugin(c.Request.Context(), tenantID, mpID, name)
	if err != nil {
		respondSkillServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": res})
}

// InstallPlugin godoc
// @Summary      Install plugin
// @Description  Installs a plugin into the workspace (sync for pure MCP, async 202 for skills).
// @Tags         Plugins
// @Accept       json
// @Produce      json
// @Success      200  {object}  handler.DataResponse{data=service.PluginInstallResult}
// @Success      202  {object}  handler.DataResponse{data=service.PluginInstallResult}
// @Router       /plugin-marketplaces/:id/plugins/:name/install [post]
func (h *PluginHandler) InstallPlugin(c *gin.Context) {
	tenantID, err := getTenantID(c)
	if err != nil {
		respondSkillServiceError(c, err)
		return
	}
	mpID := c.Param("id")
	name := c.Param("name")

	var req InstallPluginRequest
	if err := c.ShouldBindJSON(&req); err != nil && !errors.Is(err, io.EOF) {
		_ = c.Error(apperrors.NewBadRequestError("invalid request body: " + err.Error()))
		return
	}

	result, err := h.pluginSvc.InstallPlugin(
		c.Request.Context(),
		tenantID,
		mpID,
		name,
		req.SandboxConfigIDs,
		req.Env,
	)
	if err != nil {
		respondSkillServiceError(c, err)
		return
	}

	status := http.StatusOK
	if result.AsyncInstallation {
		status = http.StatusAccepted
	}
	c.JSON(status, gin.H{"success": true, "data": result})
}

// ProbePlugin godoc
// @Summary      Probe plugin MCP OAuth
// @Description  Re-runs authentication detection for installed MCP services of a plugin.
// @Tags         Plugins
// @Produce      json
// @Success      200  {object}  handler.DataResponse{data=map[string]string}
// @Router       /plugin-marketplaces/:id/plugins/:name/probe [post]
func (h *PluginHandler) ProbePlugin(c *gin.Context) {
	tenantID, err := getTenantID(c)
	if err != nil {
		respondSkillServiceError(c, err)
		return
	}
	mpID := c.Param("id")
	name := c.Param("name")
	states, err := h.pluginSvc.ProbePluginMCPOAuth(c.Request.Context(), tenantID, mpID, name)
	if err != nil {
		respondSkillServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": states})
}

// UpdatePluginSandboxes godoc
// @Summary      Manage plugin sandboxes
// @Description  Increments or decrements target sandbox configs for an installed plugin.
// @Tags         Plugins
// @Accept       json
// @Produce      json
// @Success      202  {object}  handler.DataResponse{data=bool}
// @Router       /plugin-marketplaces/:id/plugins/:name/sandboxes [put]
func (h *PluginHandler) UpdatePluginSandboxes(c *gin.Context) {
	tenantID, err := getTenantID(c)
	if err != nil {
		respondSkillServiceError(c, err)
		return
	}
	mpID := c.Param("id")
	name := c.Param("name")

	var req UpdatePluginSandboxesRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		_ = c.Error(apperrors.NewBadRequestError("invalid request body: " + err.Error()))
		return
	}

	err = h.pluginSvc.UpdatePluginSandboxes(
		c.Request.Context(), tenantID, mpID, name, req.SandboxConfigIDs,
	)
	if err != nil {
		respondSkillServiceError(c, err)
		return
	}
	c.JSON(http.StatusAccepted, gin.H{"success": true, "data": true})
}

// UninstallPlugin godoc
// @Summary      Uninstall plugin
// @Description  Uninstalls a plugin from the workspace.
// @Tags         Plugins
// @Produce      json
// @Success      202  {object}  handler.DataResponse{data=bool}
// @Router       /plugin-marketplaces/:id/plugins/:name [delete]
func (h *PluginHandler) UninstallPlugin(c *gin.Context) {
	tenantID, err := getTenantID(c)
	if err != nil {
		respondSkillServiceError(c, err)
		return
	}
	mpID := c.Param("id")
	name := c.Param("name")

	if err := h.pluginSvc.UninstallPlugin(c.Request.Context(), tenantID, mpID, name); err != nil {
		respondSkillServiceError(c, err)
		return
	}
	c.JSON(http.StatusAccepted, gin.H{"success": true, "data": true})
}

// ListInstalledPlugins godoc
// @Summary      List installed plugins
// @Description  Returns all installed plugins in the workspace with aggregated status.
// @Tags         Plugins
// @Produce      json
// @Success      200  {object}  handler.DataResponse{data=[]types.PluginInstall}
// @Router       /plugins [get]
func (h *PluginHandler) ListInstalledPlugins(c *gin.Context) {
	tenantID, err := getTenantID(c)
	if err != nil {
		respondSkillServiceError(c, err)
		return
	}
	list, err := h.pluginSvc.ListInstalledPlugins(c.Request.Context(), tenantID)
	if err != nil {
		respondSkillServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": list})
}

// GetPluginDetail godoc
// @Summary      Get installed plugin detail
// @Description  Returns full details of an installed plugin including MCP and skill status.
// @Tags         Plugins
// @Produce      json
// @Success      200  {object}  handler.DataResponse{data=service.PluginDetailView}
// @Router       /plugin-marketplaces/:id/plugins/:name/detail [get]
func (h *PluginHandler) GetPluginDetail(c *gin.Context) {
	tenantID, err := getTenantID(c)
	if err != nil {
		respondSkillServiceError(c, err)
		return
	}
	mpID := c.Param("id")
	name := c.Param("name")

	detail, err := h.pluginSvc.GetPluginDetail(c.Request.Context(), tenantID, mpID, name)
	if err != nil {
		respondSkillServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": detail})
}
