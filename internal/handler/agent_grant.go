package handler

import (
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
)

// grantOwner is the caller as grants record it.
func grantOwner(c *gin.Context) (uint64, string, bool) {
	tenantID := c.GetUint64(types.TenantIDContextKey.String())
	principal, _ := types.PrincipalFromContext(c.Request.Context())
	userID := principal.StorageID()
	return tenantID, userID, tenantID != 0 && strings.TrimSpace(userID) != ""
}

// ListAgentGrants godoc
// @Summary      列出会话内的 Agent 授权
// @Description  列出当前用户在某个会话（根会话）中通过“本会话允许”记住的授权，包括其委派子任务上批准的授权。
// @Tags         Agent
// @Produce      json
// @Param        session_id  query     string  true  "根会话 ID"
// @Success      200         {object}  map[string]interface{}  "授权列表"
// @Failure      400         {object}  errors.AppError         "请求参数错误"
// @Security     Bearer
// @Router       /agent/grants [get]
func (h *MCPServiceHandler) ListAgentGrants(c *gin.Context) {
	ctx := c.Request.Context()
	if h.grants == nil {
		_ = c.Error(errors.NewInternalServerError("agent grants are not configured"))
		return
	}
	tenantID, userID, ok := grantOwner(c)
	if !ok {
		_ = c.Error(errors.NewUnauthorizedError("a signed-in user is required"))
		return
	}
	sessionID := strings.TrimSpace(c.Query("session_id"))
	if sessionID == "" {
		_ = c.Error(errors.NewBadRequestError("session_id is required"))
		return
	}
	grants, err := h.grants.ListGrants(ctx, tenantID, userID, types.AgentGrantScopeSession, sessionID)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{"session_id": sessionID})
		_ = c.Error(errors.NewInternalServerError(err.Error()))
		return
	}
	if grants == nil {
		grants = []*types.AgentGrant{}
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": grants})
}

// RevokeAgentGrant godoc
// @Summary      撤销 Agent 授权
// @Description  撤销当前用户的一条授权；之后同类调用会重新询问。
// @Tags         Agent
// @Produce      json
// @Param        id   path      string  true  "授权 ID"
// @Success      200  {object}  map[string]interface{}  "撤销结果"
// @Failure      404  {object}  errors.AppError         "授权不存在"
// @Security     Bearer
// @Router       /agent/grants/{id} [delete]
func (h *MCPServiceHandler) RevokeAgentGrant(c *gin.Context) {
	ctx := c.Request.Context()
	if h.grants == nil {
		_ = c.Error(errors.NewInternalServerError("agent grants are not configured"))
		return
	}
	tenantID, userID, ok := grantOwner(c)
	if !ok {
		_ = c.Error(errors.NewUnauthorizedError("a signed-in user is required"))
		return
	}
	revoked, err := h.grants.RevokeGrant(ctx, tenantID, userID, c.Param("id"), time.Now())
	if err != nil {
		logger.ErrorWithFields(ctx, err, nil)
		_ = c.Error(errors.NewInternalServerError(err.Error()))
		return
	}
	if !revoked {
		_ = c.Error(errors.NewNotFoundError("grant not found"))
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}
