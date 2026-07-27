package handler

import (
	stdErrors "errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/Tencent/WeKnora/internal/application/repository"
	appErrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/skillpkg"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
)

const maxSkillUploadBody = (20 << 20) + (1 << 20)

type SkillHandler struct{ skillService interfaces.SkillService }

func NewSkillHandler(skillService interfaces.SkillService) *SkillHandler {
	return &SkillHandler{skillService: skillService}
}

func (h *SkillHandler) ListSkills(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID, _ := types.TenantIDFromContext(ctx)
	manager := isSkillManager(ctx)
	items, err := h.skillService.ListVisible(ctx, tenantID, manager)
	if err != nil {
		h.writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true, "data": items,
		"skills_available":           len(items) > 0,
		"tenant_upload_available":    h.skillService.TenantUploadAvailable(),
		"script_execution_available": h.skillService.ScriptExecutionAvailable(),
	})
}

func (h *SkillHandler) GetSkill(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID, _ := types.TenantIDFromContext(ctx)
	ref := skillReference(c.Param("id"), c.Query("source"))
	item, err := h.skillService.GetVisible(ctx, tenantID, ref, isSkillManager(ctx))
	if err != nil {
		h.writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": item})
}

func (h *SkillHandler) UploadSkill(c *gin.Context) {
	if !h.skillService.TenantUploadAvailable() {
		c.Status(http.StatusNotFound)
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxSkillUploadBody)
	file, header, err := c.Request.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "skill_zip_required"})
		return
	}
	defer file.Close()
	tenantID, _ := types.TenantIDFromContext(c.Request.Context())
	userID, _ := types.UserIDFromContext(c.Request.Context())
	item, err := h.skillService.Upload(c.Request.Context(), tenantID, userID, file, header.Size)
	if err != nil {
		h.writeError(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"success": true, "data": item})
}

func (h *SkillHandler) UpdatePackage(c *gin.Context) {
	if !h.skillService.TenantUploadAvailable() {
		c.Status(http.StatusNotFound)
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxSkillUploadBody)
	file, header, err := c.Request.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "skill_zip_required"})
		return
	}
	defer file.Close()
	expected, err := strconv.ParseInt(c.PostForm("expected_version"), 10, 64)
	if err != nil || expected < 1 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "expected_version_required"})
		return
	}
	ctx := c.Request.Context()
	tenantID, _ := types.TenantIDFromContext(ctx)
	userID, _ := types.UserIDFromContext(ctx)
	item, err := h.skillService.UpdatePackage(ctx, tenantID, userID, c.Param("id"), file, header.Size, expected)
	if err != nil {
		h.writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": item})
}

func (h *SkillHandler) SetSkillStatus(c *gin.Context) {
	var request struct {
		Status types.TenantSkillStatus `json:"status"`
	}
	if err := c.ShouldBindJSON(&request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request"})
		return
	}
	h.setStatuses(c, []types.SkillStatusUpdate{{SkillID: c.Param("id"), Status: request.Status}})
}

func (h *SkillHandler) SetSkillStatuses(c *gin.Context) {
	var request struct {
		Updates []types.SkillStatusUpdate `json:"updates"`
	}
	if err := c.ShouldBindJSON(&request); err != nil || len(request.Updates) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "updates_required"})
		return
	}
	h.setStatuses(c, request.Updates)
}

func (h *SkillHandler) setStatuses(c *gin.Context, updates []types.SkillStatusUpdate) {
	tenantID, _ := types.TenantIDFromContext(c.Request.Context())
	results := h.skillService.SetStatuses(c.Request.Context(), tenantID, updates)
	status := http.StatusOK
	for _, result := range results {
		if !result.Success {
			status = http.StatusMultiStatus
			break
		}
	}
	c.JSON(status, gin.H{"success": status == http.StatusOK, "data": results})
}

func (h *SkillHandler) DeleteSkill(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID, _ := types.TenantIDFromContext(ctx)
	userID, _ := types.UserIDFromContext(ctx)
	if err := h.skillService.Delete(ctx, tenantID, userID, c.Param("id")); err != nil {
		h.writeError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *SkillHandler) ListTenantSkillsForSystemAdmin(c *gin.Context) {
	tenantID, err := strconv.ParseUint(c.Query("tenant_id"), 10, 64)
	if err != nil || tenantID == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "tenant_id_required"})
		return
	}
	items, err := h.skillService.ListVisible(c.Request.Context(), tenantID, true)
	if err != nil {
		h.writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": items})
}

func (h *SkillHandler) writeError(c *gin.Context, err error) {
	var packageError *skillpkg.PackageError
	switch {
	case stdErrors.Is(err, repository.ErrTenantSkillNotFound):
		c.Status(http.StatusNotFound)
	case stdErrors.As(err, &packageError):
		c.JSON(http.StatusBadRequest, gin.H{"error": packageError.Code, "path": packageError.Path})
	case strings.Contains(err.Error(), "version conflict"):
		c.JSON(http.StatusConflict, gin.H{"error": "version_conflict"})
	default:
		_ = c.Error(appErrors.NewInternalServerError("skill request failed"))
	}
}

func skillReference(id, source string) types.SkillReference {
	if strings.HasPrefix(id, "preloaded:") {
		return types.SkillReference{Source: types.SkillSourcePreloaded, SkillID: strings.TrimPrefix(id, "preloaded:")}
	}
	if source == string(types.SkillSourcePreloaded) {
		return types.SkillReference{Source: types.SkillSourcePreloaded, SkillID: id}
	}
	return types.SkillReference{Source: types.SkillSourceTenant, SkillID: id}
}

func isSkillManager(ctx interface{ Value(any) any }) bool {
	if _, apiKey := ctx.Value(types.TenantAPIKeyScopeContextKey).(types.TenantAPIKeyScope); apiKey {
		return false
	}
	role, _ := ctx.Value(types.TenantRoleContextKey).(types.TenantRole)
	return role == types.TenantRoleOwner || role == types.TenantRoleAdmin
}
