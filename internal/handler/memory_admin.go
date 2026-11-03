package handler

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/Tencent/WeKnora/internal/application/service/memory"
	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// AdminMemorySettingsRequest 表示管理员显式设置目标主体的记忆开关。
type AdminMemorySettingsRequest struct {
	Enabled *bool `json:"enabled" binding:"required" example:"false"`
}

// ListMemorySubjectsResponse 返回当前租户的记忆主体摘要，不包含记忆正文。
type ListMemorySubjectsResponse struct {
	Success bool                          `json:"success" example:"true"`
	Data    []*types.MemorySubjectSummary `json:"data"`
	Total   int64                         `json:"total"`
}

// MemoryAdminErrorResponse 表示管理接口错误；角色中间件的 error 为字符串，其余为应用错误对象。
type MemoryAdminErrorResponse struct {
	Success bool `json:"success" example:"false"`
	Error   any  `json:"error"`
}

// AdminListSubjects 分页列出租户内的记忆主体，每次请求记录一条分页审计。
// @Summary      列出租户记忆主体
// @Description  仅允许 JWT 登录的租户管理员或所有者访问；返回主体 UUID、开关、分类计数与最近活动时间，不返回记忆正文
// @Tags         记忆管理
// @Produce      json
// @Param        limit   query     int  false  "每页条数，默认 50，最大 200"  default(50)
// @Param        offset  query     int  false  "非负偏移量"  default(0)
// @Success      200     {object}  handler.ListMemorySubjectsResponse
// @Failure      403     {object}  handler.MemoryAdminErrorResponse
// @Failure      500     {object}  handler.MemoryAdminErrorResponse
// @Security     Bearer
// @Router       /admin/memory/subjects [get]
func (h *MemoryHandler) AdminListSubjects(c *gin.Context) {
	limit, offset := memoryListPaging(c)
	subjects, total, err := h.memoryService.AdminListSubjects(c.Request.Context(), limit, offset)
	if err != nil {
		h.failMemoryAdmin(c, err, "Failed to list memory subjects")
		return
	}
	h.auditMemoryAdmin(c, types.AuditActionMemorySubjectsListed, "", map[string]any{
		"limit": limit, "offset": offset, "count": len(subjects),
	})
	c.JSON(http.StatusOK, ListMemorySubjectsResponse{Success: true, Data: subjects, Total: total})
}

// AdminListItems 查看指定主体 UUID 的记忆条目。
// @Summary      查看指定主体的记忆
// @Description  仅允许 JWT 登录的租户管理员或所有者访问；subject_id 是主体 UUID，并非 principal storage ID
// @Tags         记忆管理
// @Produce      json
// @Param        subject_id  path      string  true   "记忆主体 UUID"
// @Param        status      query     string  false  "状态过滤"  Enums(active, superseded, archived, pending)
// @Param        limit       query     int     false  "每页条数，默认 50，最大 200"  default(50)
// @Param        offset      query     int     false  "非负偏移量"  default(0)
// @Success      200         {object}  handler.ListItemsResponse
// @Failure      400         {object}  handler.MemoryAdminErrorResponse
// @Failure      403         {object}  handler.MemoryAdminErrorResponse
// @Failure      404         {object}  handler.MemoryAdminErrorResponse
// @Failure      500         {object}  handler.MemoryAdminErrorResponse
// @Security     Bearer
// @Router       /admin/memory/subjects/{subject_id}/items [get]
func (h *MemoryHandler) AdminListItems(c *gin.Context) {
	status := c.Query("status")
	switch status {
	case "", types.MemoryStatusActive, types.MemoryStatusSuperseded,
		types.MemoryStatusArchived, types.MemoryStatusPending:
	default:
		_ = c.Error(apperrors.NewBadRequestError("unsupported status"))
		return
	}
	limit, offset := memoryListPaging(c)
	subjectID := c.Param("subject_id")
	items, total, err := h.memoryService.AdminListItems(c.Request.Context(), subjectID, status, limit, offset)
	if err != nil {
		h.failMemoryAdmin(c, err, "Failed to list subject memories")
		return
	}
	h.auditMemoryAdmin(c, types.AuditActionMemorySubjectInspected, subjectID, map[string]any{
		"status": status, "total": total, "count": len(items),
	})
	c.JSON(http.StatusOK, gin.H{"success": true, "data": items, "total": total})
}

// AdminConfirmItem 确认目标主体的一条待审核记忆。
// @Summary      确认指定主体的待审核记忆
// @Description  仅允许 JWT 登录的租户管理员或所有者访问；确认 pending 条目，已生效条目可幂等重试，其他状态冲突返回 409
// @Tags         记忆管理
// @Produce      json
// @Param        subject_id  path      string  true  "记忆主体 UUID"
// @Param        id          path      string  true  "记忆条目 ID"
// @Success      200         {object}  handler.DataResponse{data=types.MemoryItem}
// @Failure      403         {object}  handler.MemoryAdminErrorResponse
// @Failure      404         {object}  handler.MemoryAdminErrorResponse
// @Failure      409         {object}  handler.MemoryAdminErrorResponse
// @Failure      500         {object}  handler.MemoryAdminErrorResponse
// @Security     Bearer
// @Router       /admin/memory/subjects/{subject_id}/items/{id}/confirm [post]
func (h *MemoryHandler) AdminConfirmItem(c *gin.Context) {
	subjectID, itemID := c.Param("subject_id"), c.Param("id")
	item, err := h.memoryService.AdminConfirmItem(c.Request.Context(), subjectID, itemID)
	if err != nil {
		h.failMemoryAdmin(c, err, "Failed to confirm subject memory")
		return
	}
	h.auditMemoryAdmin(c, types.AuditActionMemoryItemConfirmed, subjectID, map[string]any{
		"item_id": itemID, "status": item.Status,
	})
	c.JSON(http.StatusOK, gin.H{"success": true, "data": item})
}

// AdminRejectItem 否决目标主体的一条待审核记忆，并保留拒绝记录。
// @Summary      否决指定主体的待审核记忆
// @Description  仅允许 JWT 登录的租户管理员或所有者访问；只有 pending 条目可否决，状态冲突返回 409
// @Tags         记忆管理
// @Produce      json
// @Param        subject_id  path      string  true  "记忆主体 UUID"
// @Param        id          path      string  true  "记忆条目 ID"
// @Success      200         {object}  handler.SuccessResponse
// @Failure      403         {object}  handler.MemoryAdminErrorResponse
// @Failure      404         {object}  handler.MemoryAdminErrorResponse
// @Failure      409         {object}  handler.MemoryAdminErrorResponse
// @Failure      500         {object}  handler.MemoryAdminErrorResponse
// @Security     Bearer
// @Router       /admin/memory/subjects/{subject_id}/items/{id}/reject [post]
func (h *MemoryHandler) AdminRejectItem(c *gin.Context) {
	subjectID, itemID := c.Param("subject_id"), c.Param("id")
	if err := h.memoryService.AdminRejectItem(c.Request.Context(), subjectID, itemID); err != nil {
		h.failMemoryAdmin(c, err, "Failed to reject subject memory")
		return
	}
	h.auditMemoryAdmin(c, types.AuditActionMemoryItemRejected, subjectID, map[string]any{
		"item_id": itemID, "status": "rejected",
	})
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// AdminClear 清空目标主体的记忆，保留其开关和已完成抽取进度。
// @Summary      清空指定主体的记忆
// @Description  仅允许 JWT 登录的租户管理员或所有者访问；支持清空已关闭记忆的主体，不阻止未来新消息产生记忆
// @Tags         记忆管理
// @Produce      json
// @Param        subject_id  path      string  true  "记忆主体 UUID"
// @Success      200         {object}  handler.ClearResponse
// @Failure      403         {object}  handler.MemoryAdminErrorResponse
// @Failure      404         {object}  handler.MemoryAdminErrorResponse
// @Failure      500         {object}  handler.MemoryAdminErrorResponse
// @Security     Bearer
// @Router       /admin/memory/subjects/{subject_id}/items [delete]
func (h *MemoryHandler) AdminClear(c *gin.Context) {
	subjectID := c.Param("subject_id")
	removed, err := h.memoryService.AdminClear(c.Request.Context(), subjectID)
	if err != nil {
		h.failMemoryAdmin(c, err, "Failed to clear subject memories")
		return
	}
	h.auditMemoryAdmin(c, types.AuditActionMemorySubjectCleared, subjectID, map[string]any{"removed": removed})
	c.JSON(http.StatusOK, gin.H{"success": true, "removed": removed})
}

// AdminUpdateSettings 更新目标主体的记忆开关。
// @Summary      更新指定主体的记忆设置
// @Description  仅允许 JWT 登录的租户管理员或所有者访问；enabled 必须显式传入布尔值，允许 false
// @Tags         记忆管理
// @Accept       json
// @Produce      json
// @Param        subject_id  path      string                              true  "记忆主体 UUID"
// @Param        request     body      handler.AdminMemorySettingsRequest  true  "记忆开关"
// @Success      200         {object}  handler.SuccessResponse
// @Failure      400         {object}  handler.MemoryAdminErrorResponse
// @Failure      403         {object}  handler.MemoryAdminErrorResponse
// @Failure      404         {object}  handler.MemoryAdminErrorResponse
// @Failure      500         {object}  handler.MemoryAdminErrorResponse
// @Security     Bearer
// @Router       /admin/memory/subjects/{subject_id}/settings [put]
func (h *MemoryHandler) AdminUpdateSettings(c *gin.Context) {
	var req AdminMemorySettingsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		_ = c.Error(apperrors.NewValidationError("enabled must be a boolean"))
		return
	}
	subjectID := c.Param("subject_id")
	if err := h.memoryService.AdminSetEnabled(c.Request.Context(), subjectID, *req.Enabled); err != nil {
		h.failMemoryAdmin(c, err, "Failed to update subject memory settings")
		return
	}
	h.auditMemoryAdmin(c, types.AuditActionMemorySubjectSettingsUpdated, subjectID, map[string]any{
		"enabled": *req.Enabled,
	})
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// failMemoryAdmin 对不存在的主体、外租户主体和条目归属错误返回相同的 404。
func (h *MemoryHandler) failMemoryAdmin(c *gin.Context, err error, message string) {
	switch {
	case errors.Is(err, memory.ErrMemoryAdminForbidden):
		_ = c.Error(apperrors.NewForbiddenError("memory administration requires a tenant administrator"))
	case errors.Is(err, memory.ErrSubjectNotFound), errors.Is(err, memory.ErrItemNotFound):
		_ = c.Error(apperrors.NewNotFoundError("memory not found"))
	case errors.Is(err, types.ErrMemoryConflict):
		_ = c.Error(apperrors.NewConflictError("memory state conflict"))
	default:
		logger.ErrorWithFields(c.Request.Context(), err, nil)
		_ = c.Error(apperrors.NewInternalServerError(message))
	}
}

// auditMemoryAdmin 仅记录主体 UUID（列表请求使用工作区 ID）和操作元数据；审计失败不改变业务结果。
func (h *MemoryHandler) auditMemoryAdmin(
	c *gin.Context, action types.AuditAction, subjectID string, details map[string]any,
) {
	svc := middleware.AuditServiceFromContext(c)
	if svc == nil {
		return
	}
	ctx := c.Request.Context()
	data, err := json.Marshal(details)
	if err != nil {
		logger.Warnf(ctx, "memory: encode admin audit failed: %v", err)
		return
	}
	caller := types.CallerFromContext(ctx)
	targetType, targetID := "workspace", fmt.Sprintf("%d", caller.TenantID)
	if action != types.AuditActionMemorySubjectsListed {
		subjectUUID, err := uuid.Parse(subjectID)
		if err != nil {
			logger.Warnf(ctx, "memory: parse admin audit subject failed: %v", err)
			return
		}
		targetType, targetID = "memory_subject", subjectUUID.String()
	}
	// 租户审计 feed 只查询未设置作用域的事件；资源身份保存在 Target 字段中。
	entry := &types.AuditLog{
		TenantID: caller.TenantID, ActorUserID: caller.UserID, ActorRole: string(caller.Role), Action: action,
		TargetType: targetType, TargetID: targetID,
		RequestPath: c.FullPath(), RequestMethod: c.Request.Method,
		Outcome: types.AuditOutcomeSuccess, Details: types.JSON(data),
	}
	if err := svc.Log(ctx, entry); err != nil {
		logger.Warnf(ctx, "memory: record admin audit failed: %v", err)
	}
}
