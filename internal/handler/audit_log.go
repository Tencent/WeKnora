package handler

import (
	"net/http"
	"strconv"

	"github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
)

// AuditLogHandler exposes the per-tenant audit-log feed (PR 6, #1303).
// The route group lives under /tenants/:id/audit-log, gated by
// PathTenantMatch (URL :id == active tenant) plus an Admin role
// requirement — leaks of denied-action histories should not surface
// to ordinary members.
type AuditLogHandler struct {
	auditService interfaces.AuditLogService
}

// NewAuditLogHandler constructs the handler.
func NewAuditLogHandler(auditService interfaces.AuditLogService) *AuditLogHandler {
	return &AuditLogHandler{auditService: auditService}
}

// auditLogListResponse is the response envelope for ListTenantAuditLog.
// Mirrors wiki_log_entries' shape: data array + an opaque cursor (here
// the integer id of the last entry, or 0 if no more rows).
type auditLogListResponse struct {
	Success    bool              `json:"success"`
	Data       []*types.AuditLog `json:"data"`
	NextCursor uint64            `json:"next_cursor"`
}

// ListTenantAuditLog godoc
// @Summary      获取租户审计日志
// @Description  返回该租户最近的审计事件，按 id 倒序。游标分页：将上次响应的 next_cursor 作为下一次请求的 after_id。
// @Tags         审计日志
// @Produce      json
// @Param        id        path   string  true   "租户ID"
// @Param        after_id  query  int     false  "游标：返回 id 小于此值的记录（默认从最新开始）"
// @Param        limit     query  int     false  "页大小，1-100，默认 50"
// @Param        action    query  string  false  "按 action 精确过滤（如 rbac.member_added / rbac.access_denied）"
// @Param        outcome   query  string  false  "按 outcome 精确过滤（success / denied）"
// @Param        actor     query  string  false  "按 actor_user_id 精确过滤"
// @Success      200  {object}  auditLogListResponse
// @Failure      400  {object}  errors.AppError
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /tenants/{id}/audit-log [get]
func (h *AuditLogHandler) ListTenantAuditLog(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID, ok := parseTenantIDFromPath(c)
	if !ok {
		// parseTenantIDFromPath has already attached an error to gin.
		return
	}

	// after_id cursor — invalid values are tolerated (treated as "from
	// the top") so a misconfigured client doesn't see a hard 400 on
	// the empty / first request. Tighter validation belongs at the
	// frontend.
	var afterID uint64
	if raw := c.Query("after_id"); raw != "" {
		if v, err := strconv.ParseUint(raw, 10, 64); err == nil {
			afterID = v
		}
	}
	limit := 0 // 0 lets the repository pick its default (50)
	if raw := c.Query("limit"); raw != "" {
		if v, err := strconv.Atoi(raw); err == nil && v > 0 {
			limit = v
		}
	}

	q := &interfaces.AuditLogQuery{
		AfterID:     afterID,
		Limit:       limit,
		Action:      types.AuditAction(c.Query("action")),
		Outcome:     types.AuditOutcome(c.Query("outcome")),
		ActorUserID: c.Query("actor"),
	}

	entries, err := h.auditService.List(ctx, tenantID, q)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{"tenant_id": tenantID})
		c.Error(errors.NewInternalServerError(err.Error()))
		return
	}

	// next_cursor is the smallest id in the page (since rows are sorted
	// id DESC). Empty page ⇒ 0, telling the client there's nothing
	// older to fetch.
	var nextCursor uint64
	if n := len(entries); n > 0 {
		nextCursor = entries[n-1].ID
	}

	c.JSON(http.StatusOK, auditLogListResponse{
		Success:    true,
		Data:       entries,
		NextCursor: nextCursor,
	})
}

// ListSystemAuditLog godoc
// @Summary      获取平台审计日志
// @Description  返回 system-scope（tenant_id=0）的审计事件，覆盖 system.setting_changed / system.admin_promoted / system.admin_revoked 等 SystemAdmin 操作。按 id 倒序的游标分页。
// @Tags         审计日志
// @Produce      json
// @Param        after_id  query  int     false  "游标：返回 id 小于此值的记录（默认从最新开始）"
// @Param        limit     query  int     false  "页大小，1-100，默认 50"
// @Param        action    query  string  false  "按 action 精确过滤（如 system.setting_changed）"
// @Param        outcome   query  string  false  "按 outcome 精确过滤（success / denied）"
// @Param        actor     query  string  false  "按 actor_user_id 精确过滤"
// @Success      200  {object}  auditLogListResponse
// @Failure      500  {object}  errors.AppError
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /system/admin/audit-log [get]
//
// Mounted on /api/v1/system/admin/audit-log under the SystemAdmin()
// guard. Unlike ListTenantAuditLog this route is *not* tenant-scoped:
// the system_settings table, admin promote/revoke, and the
// apply-default-storage-quota bulk write all emit audit rows with
// tenant_id=0 (see audit_log.go for the action constants). Those rows
// would never surface in any tenant's audit feed, so this endpoint
// fixes the "wrote audit, can't see it" gap.
func (h *AuditLogHandler) ListSystemAuditLog(c *gin.Context) {
	ctx := c.Request.Context()

	// Cursor / page-size parsing mirrors ListTenantAuditLog so the
	// frontend can share the same call shape; tolerant of garbage
	// because the empty / first request shouldn't bounce.
	var afterID uint64
	if raw := c.Query("after_id"); raw != "" {
		if v, err := strconv.ParseUint(raw, 10, 64); err == nil {
			afterID = v
		}
	}
	limit := 0
	if raw := c.Query("limit"); raw != "" {
		if v, err := strconv.Atoi(raw); err == nil && v > 0 {
			limit = v
		}
	}

	q := &interfaces.AuditLogQuery{
		AfterID:     afterID,
		Limit:       limit,
		Action:      types.AuditAction(c.Query("action")),
		Outcome:     types.AuditOutcome(c.Query("outcome")),
		ActorUserID: c.Query("actor"),
	}

	// tenant_id=0 is the system-scope convention; see
	// AuditActionSystemSettingChanged comment in types/audit_log.go.
	entries, err := h.auditService.List(ctx, 0, q)
	if err != nil {
		logger.Error(ctx, err)
		c.Error(errors.NewInternalServerError(err.Error()))
		return
	}

	var nextCursor uint64
	if n := len(entries); n > 0 {
		nextCursor = entries[n-1].ID
	}

	c.JSON(http.StatusOK, auditLogListResponse{
		Success:    true,
		Data:       entries,
		NextCursor: nextCursor,
	})
}
