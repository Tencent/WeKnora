package handler

import (
	"context"
	stderrors "errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/application/service"
	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/Tencent/WeKnora/internal/types"
)

type sandboxConfigService interface {
	Create(context.Context, uint64, service.CreateSandboxConfigInput) (*types.TenantSandboxConfigEntity, error)
	List(context.Context, uint64) ([]*types.TenantSandboxConfigEntity, error)
	Get(context.Context, uint64, string) (*types.TenantSandboxConfigEntity, error)
	Update(context.Context, uint64, string, service.UpdateSandboxConfigInput) (*types.TenantSandboxConfigEntity, error)
	Delete(context.Context, uint64, string, bool) error
	Inventory(context.Context, uint64, string) (service.SandboxInventory, error)
	WorkspaceScriptsDisabled(context.Context, uint64) (bool, error)
	SetWorkspaceScriptsDisabled(context.Context, uint64, bool) error
}

type SandboxConfigHandler struct {
	service  sandboxConfigService
	defaults *sandbox.ConfigDefaults
}

func NewSandboxConfigHandler(
	service *service.TenantSandboxConfigService,
	defaults *sandbox.ConfigDefaults,
) *SandboxConfigHandler {
	return &SandboxConfigHandler{service: service, defaults: defaults}
}

type sandboxConfigRequest struct {
	Name        string                     `json:"name" binding:"required"`
	Description string                     `json:"description,omitempty"`
	Config      *types.TenantSandboxConfig `json:"config"`
}

// sandboxConfigResponse is the only outward projection of stored configs, so a
// new read path cannot accidentally return decrypted credentials.
type sandboxConfigResponse struct {
	ID          string                     `json:"id"`
	Name        string                     `json:"name"`
	Description string                     `json:"description,omitempty"`
	SandboxType string                     `json:"sandbox_type"`
	Config      *types.TenantSandboxConfig `json:"config"`
	CreatedAt   time.Time                  `json:"created_at"`
	UpdatedAt   time.Time                  `json:"updated_at"`
}

func sandboxConfigTenantID(c *gin.Context) uint64 {
	return c.GetUint64(types.TenantIDContextKey.String())
}

func toSandboxConfigResponse(e *types.TenantSandboxConfigEntity) sandboxConfigResponse {
	if e == nil {
		return sandboxConfigResponse{}
	}
	return sandboxConfigResponse{
		ID:          e.ID,
		Name:        e.Name,
		Description: e.Description,
		SandboxType: e.SandboxType,
		Config:      types.SandboxConfigForResponse(e.Config, true),
		CreatedAt:   e.CreatedAt,
		UpdatedAt:   e.UpdatedAt,
	}
}

// respondSandboxesStillLive carries the exact inventory that refused the write;
// recomputing it could race and show the admin a different reason.
func respondSandboxesStillLive(c *gin.Context, inv service.SandboxInventory) {
	c.JSON(http.StatusConflict, gin.H{
		"success": false,
		"error": gin.H{
			"code":    "sandboxes_still_live",
			"message": "该配置仍有运行中或已暂停的沙箱，请先结束或删除相关会话，或新建一份配置",
			"data":    inv,
		},
	})
}

func respondSandboxInventoryUnverifiable(c *gin.Context) {
	c.JSON(http.StatusConflict, gin.H{
		"success": false,
		"error": gin.H{
			"code":    "sandbox_inventory_unverifiable",
			"message": "无法连接该后端核实是否仍有沙箱",
		},
	})
}

func respondSandboxConfigCordoned(c *gin.Context) {
	c.JSON(http.StatusLocked, gin.H{
		"success": false,
		"error": gin.H{
			"code":    "sandbox_config_cordoned",
			"message": "该配置正在被其他人修改，请稍后重试",
		},
	})
}

func respondSandboxConfigRefusal(c *gin.Context, err error) bool {
	var liveErr *service.SandboxesStillLiveError
	if stderrors.As(err, &liveErr) {
		respondSandboxesStillLive(c, liveErr.Inventory)
		return true
	}
	if stderrors.Is(err, service.ErrSandboxInventoryUnverifiable) {
		respondSandboxInventoryUnverifiable(c)
		return true
	}
	if stderrors.Is(err, repository.ErrSandboxConfigCordoned) {
		respondSandboxConfigCordoned(c)
		return true
	}
	return false
}

// respondSandboxConfigServiceError promotes the service's input-validation
// sentinels to 400. They are matched as sentinels rather than by message so a
// reworded error cannot silently start returning 500 for bad input.
func respondSandboxConfigServiceError(c *gin.Context, err error) {
	switch {
	case stderrors.Is(err, service.ErrSandboxConfigNameRequired),
		stderrors.Is(err, service.ErrNamedSandboxBackendUnsupported),
		stderrors.Is(err, sandbox.ErrUnsupportedSandboxType),
		stderrors.Is(err, sandbox.ErrUnsafeOutboundURL),
		stderrors.Is(err, sandbox.ErrSandboxConfigIncomplete):
		c.Error(apperrors.NewBadRequestError(err.Error()))
	default:
		c.Error(err)
	}
}

// List godoc
// @Summary      List sandbox configs
// @Description  List workspace sandbox backend configs with credentials masked and deployment defaults included.
// @Tags         SandboxConfig
// @Produce      json
// @Success      200  {object}  map[string]interface{}   "Sandbox configs and defaults"
// @Failure      401  {object}  map[string]interface{}   "Unauthorized"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /sandbox-configs [get]
func (h *SandboxConfigHandler) List(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID := sandboxConfigTenantID(c)
	configs, err := h.service.List(ctx, tenantID)
	if err != nil {
		c.Error(err)
		return
	}
	disabled, err := h.service.WorkspaceScriptsDisabled(ctx, tenantID)
	if err != nil {
		c.Error(err)
		return
	}
	data := make([]sandboxConfigResponse, 0, len(configs))
	for _, cfg := range configs {
		data = append(data, toSandboxConfigResponse(cfg))
	}
	c.JSON(http.StatusOK, gin.H{
		"success":                    true,
		"data":                       data,
		"defaults":                   h.defaults,
		"workspace_scripts_disabled": disabled,
	})
}

type workspacePolicyRequest struct {
	ScriptsDisabled bool `json:"scripts_disabled"`
}

// SetWorkspacePolicy toggles script execution for agents that use the deployment default.
func (h *SandboxConfigHandler) SetWorkspacePolicy(c *gin.Context) {
	var req workspacePolicyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(apperrors.NewBadRequestError(err.Error()))
		return
	}
	tenantID := sandboxConfigTenantID(c)
	if err := h.service.SetWorkspaceScriptsDisabled(c.Request.Context(), tenantID, req.ScriptsDisabled); err != nil {
		c.Error(err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "workspace_scripts_disabled": req.ScriptsDisabled})
}

// Create godoc
// @Summary      Create sandbox config
// @Description  Create a named workspace sandbox backend config. Credentials are masked in the response.
// @Tags         SandboxConfig
// @Accept       json
// @Produce      json
// @Param        request  body      sandboxConfigRequest    true  "Sandbox backend config"
// @Success      201      {object}  map[string]interface{}  "Created sandbox config"
// @Failure      400      {object}  apperrors.AppError      "Invalid request or validation failure"
// @Failure      401      {object}  map[string]interface{}  "Unauthorized"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /sandbox-configs [post]
func (h *SandboxConfigHandler) Create(c *gin.Context) {
	var req sandboxConfigRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(apperrors.NewBadRequestError(err.Error()))
		return
	}
	created, err := h.service.Create(c.Request.Context(), sandboxConfigTenantID(c),
		service.CreateSandboxConfigInput{
			Name:        req.Name,
			Description: req.Description,
			Config:      req.Config,
		})
	if err != nil {
		respondSandboxConfigServiceError(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"success": true, "data": toSandboxConfigResponse(created)})
}

// Get godoc
// @Summary      Get sandbox config
// @Description  Retrieve a workspace sandbox backend config with credentials masked.
// @Tags         SandboxConfig
// @Produce      json
// @Param        id   path      string  true  "Sandbox config ID"
// @Success      200  {object}  map[string]interface{}   "Sandbox config"
// @Failure      401  {object}  map[string]interface{}   "Unauthorized"
// @Failure      404  {object}  apperrors.AppError       "Sandbox config not found"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /sandbox-configs/{id} [get]
func (h *SandboxConfigHandler) Get(c *gin.Context) {
	cfg, err := h.service.Get(c.Request.Context(), sandboxConfigTenantID(c), c.Param("id"))
	if err != nil {
		c.Error(err)
		return
	}
	if cfg == nil {
		c.Error(apperrors.NewNotFoundError("sandbox config not found"))
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": toSandboxConfigResponse(cfg)})
}

// Update godoc
// @Summary      Update sandbox config
// @Description  Update a sandbox backend config. Identity-field changes are refused while the config owns live or paused sandboxes.
// @Tags         SandboxConfig
// @Accept       json
// @Produce      json
// @Param        id       path      string                true  "Sandbox config ID"
// @Param        request  body      sandboxConfigRequest  true  "Updated sandbox config"
// @Success      200      {object}  map[string]interface{}  "Updated sandbox config"
// @Failure      400      {object}  apperrors.AppError      "Invalid request or validation failure"
// @Failure      401      {object}  map[string]interface{}  "Unauthorized"
// @Failure      404      {object}  apperrors.AppError      "Sandbox config not found"
// @Failure      409      {object}  map[string]interface{}  "Live sandboxes or unverifiable inventory"
// @Failure      423      {object}  map[string]interface{}  "Sandbox config is being modified by another request"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /sandbox-configs/{id} [put]
func (h *SandboxConfigHandler) Update(c *gin.Context) {
	var req sandboxConfigRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(apperrors.NewBadRequestError(err.Error()))
		return
	}
	updated, err := h.service.Update(c.Request.Context(), sandboxConfigTenantID(c), c.Param("id"),
		service.UpdateSandboxConfigInput{
			Name:        req.Name,
			Description: req.Description,
			Config:      req.Config,
		})
	if err != nil {
		if respondSandboxConfigRefusal(c, err) {
			return
		}
		respondSandboxConfigServiceError(c, err)
		return
	}
	if updated == nil {
		c.Error(apperrors.NewNotFoundError("sandbox config not found"))
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": toSandboxConfigResponse(updated)})
}

// Delete godoc
// @Summary      Delete sandbox config
// @Description  Soft-delete a sandbox backend config. force=true only overrides unverifiable provider inventory, never confirmed live sandboxes.
// @Tags         SandboxConfig
// @Produce      json
// @Param        id     path   string  true   "Sandbox config ID"
// @Param        force  query  bool    false  "Force delete when inventory is unverifiable"
// @Success      200    {object}  map[string]interface{}  "Deletion success"
// @Failure      401    {object}  map[string]interface{}  "Unauthorized"
// @Failure      409    {object}  map[string]interface{}  "Live sandboxes or unverifiable inventory"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /sandbox-configs/{id} [delete]
func (h *SandboxConfigHandler) Delete(c *gin.Context) {
	force := c.Query("force") == "true"
	if err := h.service.Delete(c.Request.Context(), sandboxConfigTenantID(c), c.Param("id"), force); err != nil {
		if respondSandboxConfigRefusal(c, err) {
			return
		}
		c.Error(err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// Inventory godoc
// @Summary      Inspect sandbox config inventory
// @Description  Return live/paused sandbox inventory and affected agent names for one config.
// @Tags         SandboxConfig
// @Produce      json
// @Param        id   path      string  true  "Sandbox config ID"
// @Success      200  {object}  map[string]interface{}  "Sandbox inventory"
// @Failure      401  {object}  map[string]interface{}  "Unauthorized"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /sandbox-configs/{id}/sandboxes [get]
func (h *SandboxConfigHandler) Inventory(c *gin.Context) {
	inv, err := h.service.Inventory(c.Request.Context(), sandboxConfigTenantID(c), c.Param("id"))
	if err != nil {
		c.Error(err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": inv})
}
