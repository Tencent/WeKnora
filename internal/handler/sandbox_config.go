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
	QueryTemplates(context.Context, uint64, service.SandboxTemplateQueryInput) (*service.SandboxTemplateCatalog, error)
}

type sandboxTemplateQueryRequest struct {
	Config          *types.TenantSandboxConfig `json:"config"`
	ConfigID        string                     `json:"config_id,omitempty"`
	EnsureStandard  bool                       `json:"ensure_standard"`
	ReplaceStandard bool                       `json:"replace_standard"`
}

// QueryTemplates returns the templates visible through an unsaved workspace
// connection. ensure_standard starts a build only when the cluster has no
// usable WeKnora template; replace_standard rebuilds that template so a
// new spec (DNS, image) can take effect. replace_standard requires config_id.
func (h *SandboxConfigHandler) QueryTemplates(c *gin.Context) {
	var req sandboxTemplateQueryRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(apperrors.NewBadRequestError(err.Error()))
		return
	}
	result, err := h.service.QueryTemplates(c.Request.Context(), sandboxConfigTenantID(c),
		service.SandboxTemplateQueryInput{
			Config:          req.Config,
			ConfigID:        req.ConfigID,
			EnsureStandard:  req.EnsureStandard,
			ReplaceStandard: req.ReplaceStandard,
		})
	if err != nil {
		if respondSandboxConfigRefusal(c, err) {
			return
		}
		respondSandboxConfigServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": result})
}

type SandboxConfigHandler struct {
	service sandboxConfigService
}

func NewSandboxConfigHandler(
	service *service.TenantSandboxConfigService,
) *SandboxConfigHandler {
	return &SandboxConfigHandler{service: service}
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

func respondSkillSnapshotReleaseFailed(c *gin.Context, remaining []string) {
	c.JSON(http.StatusConflict, gin.H{
		"success": false,
		"error": gin.H{
			"code":    "skill_snapshot_release_failed",
			"message": "无法销毁该配置下的技能快照，已中止删除以免快照继续计费",
			"data":    gin.H{"snapshot_ids": remaining},
		},
	})
}

func respondSkillSnapshotBlocksTemplate(c *gin.Context) {
	c.JSON(http.StatusConflict, gin.H{
		"success": false,
		"error": gin.H{
			"code":    "skill_snapshot_blocks_template",
			"message": "该配置已安装 Skill，不能更换连接、DNS 或重建运行模板。请新建一份沙箱后再装 Skill。",
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
	var releaseErr *service.SkillSnapshotReleaseFailedError
	if stderrors.As(err, &releaseErr) {
		respondSkillSnapshotReleaseFailed(c, releaseErr.Remaining)
		return true
	}
	if stderrors.Is(err, service.ErrSkillSnapshotReleaseFailed) {
		respondSkillSnapshotReleaseFailed(c, nil)
		return true
	}
	if stderrors.Is(err, service.ErrSkillSnapshotBlocksTemplateChange) {
		respondSkillSnapshotBlocksTemplate(c)
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
		stderrors.Is(err, sandbox.ErrSandboxConfigIncomplete),
		stderrors.Is(err, sandbox.ErrDockerBackendDisabled):
		c.Error(apperrors.NewBadRequestError(err.Error()))
	default:
		c.Error(err)
	}
}

// List godoc
// @Summary      List sandbox configs
// @Description  List workspace sandbox backend configs with credentials masked.
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
		"workspace_scripts_disabled": disabled,
	})
}

type workspacePolicyRequest struct {
	ScriptsDisabled bool `json:"scripts_disabled"`
}

// SetWorkspacePolicy toggles script execution for the whole workspace.
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
