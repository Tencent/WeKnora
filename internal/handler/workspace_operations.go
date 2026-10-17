package handler

import (
	"net/http"
	"strconv"

	"github.com/Tencent/WeKnora/internal/handler/dto"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
)

// WorkspaceResponse wraps the authenticated workspace profile.
type WorkspaceResponse struct {
	Success bool                `json:"success"`
	Data    *dto.TenantResponse `json:"data"`
}

// bindCurrentWorkspace pins the existing handlers to the authenticated caller,
// even for system administrators. No model-supplied workspace ID is accepted.
func bindCurrentWorkspace(c *gin.Context) bool {
	id := types.CallerFromContext(c.Request.Context()).TenantID
	if id == 0 {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "workspace context missing"})
		return false
	}
	c.Params = gin.Params{{Key: "id", Value: strconv.FormatUint(id, 10)}}
	return true
}

// GetCurrentWorkspace godoc
// @Summary 查询当前空间信息
// @Tags 空间管理
// @Produce json
// @Success 200 {object} WorkspaceResponse
// @Security Bearer
// @x-agent {"id":"workspace.get","risk":"read"}
// @x-agent-hint "Reads the authenticated current workspace only: name, description, status and storage quota/usage in bytes. Does not switch workspaces. Credentials and internal configuration are not returned."
// @Router /tenants/current [get]
func (h *TenantHandler) GetCurrentWorkspace(c *gin.Context) {
	if bindCurrentWorkspace(c) {
		h.GetTenant(c)
	}
}

// UpdateCurrentWorkspace godoc
// @Summary 修改当前空间基本信息
// @Tags 空间管理
// @Accept json
// @Produce json
// @Param request body updateTenantRequest true "需要修改的名称或描述"
// @Success 200 {object} WorkspaceResponse
// @Security Bearer
// @x-agent {"id":"workspace.update","risk":"write","role":"owner","target":"workspace.get","label":"name"}
// @x-agent-hint "Changes only the current workspace name and/or description, visible to all its members. Requires workspace owner. At least one field is required; omitted fields are preserved, empty description clears it. Does not change quota, status, members, permissions or credentials."
// @Router /tenants/current [patch]
func (h *TenantHandler) UpdateCurrentWorkspace(c *gin.Context) {
	if bindCurrentWorkspace(c) {
		h.UpdateTenant(c)
	}
}

// WorkspaceMembersResponse describes the existing paginated member response.
type WorkspaceMembersResponse struct {
	Members  []types.TenantMemberResponse `json:"members"`
	Total    int64                        `json:"total"     extensions:"x-agent"`
	Page     int                          `json:"page"      extensions:"x-agent"`
	PageSize int                          `json:"page_size" extensions:"x-agent"`
}

// ListCurrentWorkspaceMembers godoc
// @Summary 查询当前空间成员
// @Tags 空间成员
// @Produce json
// @Param q query string false "按邮箱或用户名筛选" extensions(x-agent)
// @Param page query int false "页码，从 1 开始" default(1) minimum(1) extensions(x-agent,x-agent-maximum=10000)
// @Param page_size query int false "每页成员数量" default(20) minimum(1) maximum(100) extensions(x-agent,x-agent-maximum=50)
// @Success 200 {object} DataResponse{data=WorkspaceMembersResponse}
// @Security Bearer
// @x-agent {"id":"workspace.list_members","risk":"read","root":"data.members","max_items":50}
// @x-agent-hint "Lists active members of the current workspace with username, email and workspace role. q filters username/email, not role; to find all owners/admins paginate until meta.total is covered and filter role. Workspace owner/admin roles do not imply system administrator status. Does not include pending invitations or change membership."
// @Router /tenants/current/members [get]
func (h *TenantMemberHandler) ListCurrentWorkspaceMembers(c *gin.Context) {
	if bindCurrentWorkspace(c) {
		h.ListMembers(c)
	}
}
