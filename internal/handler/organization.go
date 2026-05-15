package handler

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/Tencent/WeKnora/internal/application/service"
	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	secutils "github.com/Tencent/WeKnora/internal/utils"
)

// OrganizationHandler implements HTTP request handlers for organization management
type OrganizationHandler struct {
	orgService         interfaces.OrganizationService
	shareService       interfaces.KBShareService
	agentShareService  interfaces.AgentShareService
	customAgentService interfaces.CustomAgentService
	userService        interfaces.UserService
	// tenantService is used to resolve tenant_name in member listings
	// and to back the tenant-centric invite picker. Plan 3 lifts org
	// membership to the tenant level, so the UI needs to surface the
	// tenant identity rather than the representative user alone.
	tenantService interfaces.TenantService
	kbService     interfaces.KnowledgeBaseService
	knowledgeRepo interfaces.KnowledgeRepository
	chunkRepo     interfaces.ChunkRepository
}

// NewOrganizationHandler creates a new organization handler
func NewOrganizationHandler(
	orgService interfaces.OrganizationService,
	shareService interfaces.KBShareService,
	agentShareService interfaces.AgentShareService,
	customAgentService interfaces.CustomAgentService,
	userService interfaces.UserService,
	tenantService interfaces.TenantService,
	kbService interfaces.KnowledgeBaseService,
	knowledgeRepo interfaces.KnowledgeRepository,
	chunkRepo interfaces.ChunkRepository,
) *OrganizationHandler {
	return &OrganizationHandler{
		orgService:         orgService,
		shareService:       shareService,
		agentShareService:  agentShareService,
		customAgentService: customAgentService,
		userService:        userService,
		tenantService:      tenantService,
		kbService:          kbService,
		knowledgeRepo:      knowledgeRepo,
		chunkRepo:          chunkRepo,
	}
}

// CreateOrganization creates a new organization
// @Summary      创建组织
// @Description  创建新的组织，创建者自动成为管理员
// @Tags         组织管理
// @Accept       json
// @Produce      json
// @Param        request  body      types.CreateOrganizationRequest  true  "组织信息"
// @Success      201      {object}  map[string]interface{}
// @Failure      400      {object}  apperrors.AppError
// @Security     Bearer
// @Router       /organizations [post]
func (h *OrganizationHandler) CreateOrganization(c *gin.Context) {
	ctx := c.Request.Context()

	userID := c.GetString(types.UserIDContextKey.String())
	tenantID := c.GetUint64(types.TenantIDContextKey.String())

	var req types.CreateOrganizationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Errorf(ctx, "Invalid request parameters: %v", err)
		c.Error(apperrors.NewValidationError("Invalid request parameters").WithDetails(err.Error()))
		return
	}

	org, err := h.orgService.CreateOrganization(ctx, userID, tenantID, &req)
	if err != nil {
		logger.Errorf(ctx, "Failed to create organization: %v", err)
		if errors.Is(err, service.ErrInvalidValidityDays) {
			c.Error(apperrors.NewValidationError(err.Error()))
			return
		}
		c.Error(apperrors.NewInternalServerError("Failed to create organization").WithDetails(err.Error()))
		return
	}

	logger.Infof(ctx, "Organization created: %s", org.ID)
	c.JSON(http.StatusCreated, gin.H{
		"success": true,
		"data":    h.toOrgResponse(ctx, org, userID),
	})
}

// GetOrganization gets an organization by ID
// @Summary      获取组织详情
// @Description  根据ID获取组织详情
// @Tags         组织管理
// @Produce      json
// @Param        id   path      string  true  "组织ID"
// @Success      200  {object}  map[string]interface{}
// @Failure      404  {object}  apperrors.AppError
// @Security     Bearer
// @Router       /organizations/{id} [get]
func (h *OrganizationHandler) GetOrganization(c *gin.Context) {
	ctx := c.Request.Context()

	orgID := c.Param("id")
	userID := c.GetString(types.UserIDContextKey.String())

	org, err := h.orgService.GetOrganization(ctx, orgID)
	if err != nil {
		logger.Errorf(ctx, "Failed to get organization: %v", err)
		c.Error(apperrors.NewNotFoundError("Organization not found"))
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    h.toOrgResponse(ctx, org, userID),
	})
}

// ListMyOrganizations lists organizations that the current tenant belongs to.
// Response includes resource_counts (per-org KB/agent counts) for list sidebar so frontend does not need a separate GET /me/resource-counts.
// @Summary      获取我的组织列表
// @Description  获取当前租户所属的所有组织，并附带各空间内知识库/智能体数量
// @Tags         组织管理
// @Produce      json
// @Success      200  {object}  types.ListOrganizationsResponse
// @Security     Bearer
// @Router       /organizations [get]
func (h *OrganizationHandler) ListMyOrganizations(c *gin.Context) {
	ctx := c.Request.Context()
	userID := c.GetString(types.UserIDContextKey.String())
	tenantID := c.GetUint64(types.TenantIDContextKey.String())

	orgs, err := h.orgService.ListTenantOrganizations(ctx, tenantID)
	if err != nil {
		logger.Errorf(ctx, "Failed to list organizations: %v", err)
		c.Error(apperrors.NewInternalServerError("Failed to list organizations").WithDetails(err.Error()))
		return
	}

	response := make([]types.OrganizationResponse, 0, len(orgs))
	for _, org := range orgs {
		response = append(response, h.toOrgResponse(ctx, org, userID))
	}

	resp := types.ListOrganizationsResponse{
		Organizations: response,
		Total:         int64(len(response)),
	}
	// 附带各空间资源数量，供知识库/智能体列表页侧栏展示
	resp.ResourceCounts = h.buildResourceCountsByOrg(ctx, orgs, userID, tenantID)
	if resp.ResourceCounts != nil {
		// 补齐未出现在 map 中的 org 为 0
		for _, o := range orgs {
			if _, ok := resp.ResourceCounts.KnowledgeBases.ByOrganization[o.ID]; !ok {
				resp.ResourceCounts.KnowledgeBases.ByOrganization[o.ID] = 0
			}
			if _, ok := resp.ResourceCounts.Agents.ByOrganization[o.ID]; !ok {
				resp.ResourceCounts.Agents.ByOrganization[o.ID] = 0
			}
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    resp,
	})
}

// buildResourceCountsByOrg 返回各空间内知识库数与智能体数，供 ListMyOrganizations 和侧栏使用；失败时返回 nil。
// 使用批量接口：一次拉取所有空间的直接共享 KB ID、一次拉取所有空间的智能体列表，再在内存中按空间合并计数。
func (h *OrganizationHandler) buildResourceCountsByOrg(ctx context.Context, orgs []*types.Organization, userID string, tenantID uint64) *types.ResourceCountsByOrgResponse {
	orgIDs := make([]string, 0, len(orgs))
	for _, o := range orgs {
		orgIDs = append(orgIDs, o.ID)
	}
	agentCounts, err := h.agentShareService.CountByOrganizations(ctx, orgIDs)
	if err != nil {
		logger.Warnf(ctx, "buildResourceCountsByOrg CountByOrganizations: %v", err)
		return nil
	}
	directKBIDsByOrg, err := h.shareService.ListSharedKnowledgeBaseIDsByOrganizations(ctx, orgIDs, tenantID)
	if err != nil {
		logger.Warnf(ctx, "buildResourceCountsByOrg ListSharedKnowledgeBaseIDsByOrganizations: %v", err)
		return nil
	}
	callerTenantRole := types.TenantRoleFromContext(ctx)
	agentListByOrg, err := h.agentShareService.ListSharedAgentsInOrganizations(ctx, orgIDs, tenantID, callerTenantRole)
	if err != nil {
		logger.Warnf(ctx, "buildResourceCountsByOrg ListSharedAgentsInOrganizations: %v", err)
		return nil
	}
	_ = userID
	byOrgKB := make(map[string]int)
	tenantKBCache := make(map[uint64][]string) // cache ListKnowledgeBasesByTenantID by tenantID
	for _, o := range orgs {
		oid := o.ID
		directIDs := directKBIDsByOrg[oid]
		directSet := make(map[string]bool)
		for _, id := range directIDs {
			directSet[id] = true
		}
		count := len(directIDs)
		for _, item := range agentListByOrg[oid] {
			if item.Agent == nil {
				continue
			}
			agent := item.Agent
			mode := agent.Config.KBSelectionMode
			if mode == "none" {
				continue
			}
			var kbIDs []string
			switch mode {
			case "selected":
				if len(agent.Config.KnowledgeBases) == 0 {
					continue
				}
				kbIDs = agent.Config.KnowledgeBases
			case "all":
				tid := agent.TenantID
				if _, ok := tenantKBCache[tid]; !ok {
					kbs, err := h.kbService.ListKnowledgeBasesByTenantID(ctx, tid)
					if err != nil {
						logger.Warnf(ctx, "ListKnowledgeBasesByTenantID tenant %d: %v", tid, err)
						tenantKBCache[tid] = nil
						continue
					}
					ids := make([]string, 0, len(kbs))
					for _, kb := range kbs {
						if kb != nil && kb.ID != "" {
							ids = append(ids, kb.ID)
						}
					}
					tenantKBCache[tid] = ids
				}
				kbIDs = tenantKBCache[tid]
			default:
				if len(agent.Config.KnowledgeBases) > 0 {
					kbIDs = agent.Config.KnowledgeBases
				}
			}
			for _, kbID := range kbIDs {
				if kbID != "" && !directSet[kbID] {
					directSet[kbID] = true
					count++
				}
			}
		}
		byOrgKB[oid] = count
	}
	byOrgAgent := make(map[string]int)
	for _, o := range orgs {
		byOrgAgent[o.ID] = 0
	}
	for id, n := range agentCounts {
		byOrgAgent[id] = int(n)
	}
	return &types.ResourceCountsByOrgResponse{
		KnowledgeBases: struct {
			ByOrganization map[string]int `json:"by_organization"`
		}{ByOrganization: byOrgKB},
		Agents: struct {
			ByOrganization map[string]int `json:"by_organization"`
		}{ByOrganization: byOrgAgent},
	}
}

// UpdateOrganization updates an organization
// @Summary      更新组织
// @Description  更新组织信息（需要管理员权限）
// @Tags         组织管理
// @Accept       json
// @Produce      json
// @Param        id       path      string                           true  "组织ID"
// @Param        request  body      types.UpdateOrganizationRequest  true  "更新信息"
// @Success      200      {object}  map[string]interface{}
// @Failure      403      {object}  apperrors.AppError
// @Security     Bearer
// @Router       /organizations/{id} [put]
func (h *OrganizationHandler) UpdateOrganization(c *gin.Context) {
	ctx := c.Request.Context()

	orgID := c.Param("id")
	userID := c.GetString(types.UserIDContextKey.String())
	tenantID := c.GetUint64(types.TenantIDContextKey.String())

	var req types.UpdateOrganizationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(apperrors.NewValidationError("Invalid request parameters").WithDetails(err.Error()))
		return
	}

	org, err := h.orgService.UpdateOrganization(ctx, orgID, userID, tenantID, &req)
	if err != nil {
		logger.Errorf(ctx, "Failed to update organization: %v", err)
		if errors.Is(err, service.ErrInvalidValidityDays) {
			c.Error(apperrors.NewValidationError(err.Error()))
			return
		}
		if errors.Is(err, service.ErrOrgMemberLimitTooLow) {
			c.Error(apperrors.NewValidationError("当前成员数已超过新的上限，请先移除成员或设置更大的上限"))
			return
		}
		c.Error(apperrors.NewForbiddenError("Permission denied or organization not found"))
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    h.toOrgResponse(ctx, org, userID),
	})
}

// DeleteOrganization deletes an organization
// @Summary      删除组织
// @Description  删除组织（仅组织创建者可操作）
// @Tags         组织管理
// @Param        id  path  string  true  "组织ID"
// @Success      200  {object}  map[string]interface{}
// @Failure      403  {object}  apperrors.AppError
// @Security     Bearer
// @Router       /organizations/{id} [delete]
func (h *OrganizationHandler) DeleteOrganization(c *gin.Context) {
	ctx := c.Request.Context()

	orgID := c.Param("id")
	userID := c.GetString(types.UserIDContextKey.String())
	tenantID := c.GetUint64(types.TenantIDContextKey.String())

	if err := h.orgService.DeleteOrganization(ctx, orgID, userID, tenantID); err != nil {
		logger.Errorf(ctx, "Failed to delete organization: %v", err)
		c.Error(apperrors.NewForbiddenError("Permission denied or organization not found"))
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Organization deleted successfully",
	})
}

// ListMembers lists all tenant-members of an organization
// @Summary      获取组织成员列表
// @Description  获取组织的所有成员（按租户）
// @Tags         组织管理
// @Produce      json
// @Param        id  path  string  true  "组织ID"
// @Success      200  {object}  types.ListMembersResponse
// @Security     Bearer
// @Router       /organizations/{id}/members [get]
func (h *OrganizationHandler) ListMembers(c *gin.Context) {
	ctx := c.Request.Context()

	orgID := c.Param("id")

	members, err := h.orgService.ListTenantMembers(ctx, orgID)
	if err != nil {
		logger.Errorf(ctx, "Failed to list members: %v", err)
		c.Error(apperrors.NewInternalServerError("Failed to list members").WithDetails(err.Error()))
		return
	}

	// Collect tenant IDs to resolve tenant names in one round-trip.
	tenantIDs := make([]uint64, 0, len(members))
	for _, m := range members {
		tenantIDs = append(tenantIDs, m.TenantID)
	}
	tenantByID, _ := h.tenantService.GetTenantsByIDs(ctx, tenantIDs)

	response := make([]types.OrganizationMemberResponse, 0, len(members))
	for _, m := range members {
		resp := types.OrganizationMemberResponse{
			ID:                   m.ID,
			UserID:               m.RepresentativeUserID,
			RepresentativeUserID: m.RepresentativeUserID,
			Role:                 string(m.Role),
			TenantID:             m.TenantID,
			JoinedAt:             m.CreatedAt,
		}
		if t, ok := tenantByID[m.TenantID]; ok && t != nil {
			resp.TenantName = t.Name
		}
		if m.RepresentativeUser != nil {
			resp.Username = m.RepresentativeUser.Username
			resp.Email = m.RepresentativeUser.Email
			resp.Avatar = m.RepresentativeUser.Avatar
		}
		response = append(response, resp)
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": types.ListMembersResponse{
			Members: response,
			Total:   int64(len(response)),
		},
	})
}

// UpdateMemberRole updates a tenant-member's role
// @Summary      更新成员角色
// @Description  更新组织成员（租户）的角色（需要管理员权限）
// @Tags         组织管理
// @Accept       json
// @Produce      json
// @Param        id          path      string                       true  "组织ID"
// @Param        tenant_id   path      string                       true  "成员租户ID"
// @Param        request     body      types.UpdateMemberRoleRequest  true  "角色信息"
// @Success      200      {object}  map[string]interface{}
// @Failure      403      {object}  apperrors.AppError
// @Security     Bearer
// @Router       /organizations/{id}/members/{tenant_id} [put]
func (h *OrganizationHandler) UpdateMemberRole(c *gin.Context) {
	ctx := c.Request.Context()

	orgID := c.Param("id")
	memberTenantIDStr := c.Param("tenant_id")
	memberTenantID, err := strconv.ParseUint(memberTenantIDStr, 10, 64)
	if err != nil {
		c.Error(apperrors.NewValidationError("Invalid tenant ID"))
		return
	}
	operatorUserID := c.GetString(types.UserIDContextKey.String())
	operatorTenantID := c.GetUint64(types.TenantIDContextKey.String())

	var req types.UpdateMemberRoleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(apperrors.NewValidationError("Invalid request parameters").WithDetails(err.Error()))
		return
	}

	if err := h.orgService.UpdateTenantMemberRole(ctx, orgID, memberTenantID, req.Role, operatorUserID, operatorTenantID); err != nil {
		logger.Errorf(ctx, "Failed to update member role: %v", err)
		c.Error(apperrors.NewForbiddenError("Permission denied or invalid operation"))
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Member role updated successfully",
	})
}

// RemoveMember removes a tenant-member from an organization
// @Summary      移除成员
// @Description  从组织中移除成员租户（需要管理员权限）
// @Tags         组织管理
// @Param        id         path  string  true  "组织ID"
// @Param        tenant_id  path  string  true  "成员租户ID"
// @Success      200      {object}  map[string]interface{}
// @Failure      403      {object}  apperrors.AppError
// @Security     Bearer
// @Router       /organizations/{id}/members/{tenant_id} [delete]
func (h *OrganizationHandler) RemoveMember(c *gin.Context) {
	ctx := c.Request.Context()

	orgID := c.Param("id")
	memberTenantIDStr := c.Param("tenant_id")
	memberTenantID, err := strconv.ParseUint(memberTenantIDStr, 10, 64)
	if err != nil {
		c.Error(apperrors.NewValidationError("Invalid tenant ID"))
		return
	}
	operatorUserID := c.GetString(types.UserIDContextKey.String())
	operatorTenantID := c.GetUint64(types.TenantIDContextKey.String())

	if err := h.orgService.RemoveTenantMember(ctx, orgID, memberTenantID, operatorUserID, operatorTenantID); err != nil {
		logger.Errorf(ctx, "Failed to remove member: %v", err)
		c.Error(apperrors.NewForbiddenError("Permission denied or invalid operation"))
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Member removed successfully",
	})
}

// GenerateInviteCode generates a new invite code
// @Summary      生成邀请码
// @Description  生成新的组织邀请码（需要管理员权限）
// @Tags         组织管理
// @Produce      json
// @Param        id  path  string  true  "组织ID"
// @Success      200  {object}  map[string]interface{}
// @Failure      403  {object}  apperrors.AppError
// @Security     Bearer
// @Router       /organizations/{id}/invite-code [post]
func (h *OrganizationHandler) GenerateInviteCode(c *gin.Context) {
	ctx := c.Request.Context()

	orgID := c.Param("id")
	userID := c.GetString(types.UserIDContextKey.String())
	tenantID := c.GetUint64(types.TenantIDContextKey.String())

	code, err := h.orgService.GenerateInviteCode(ctx, orgID, userID, tenantID)
	if err != nil {
		logger.Errorf(ctx, "Failed to generate invite code: %v", err)
		c.Error(apperrors.NewForbiddenError("Permission denied"))
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success":     true,
		"invite_code": code,
	})
}

// PreviewByInviteCode previews organization info by invite code (without joining)
// @Summary      通过邀请码预览组织
// @Description  通过邀请码获取组织基本信息（不加入）
// @Tags         组织管理
// @Produce      json
// @Param        code  path  string  true  "邀请码"
// @Success      200   {object}  map[string]interface{}
// @Failure      404   {object}  apperrors.AppError
// @Security     Bearer
// @Router       /organizations/preview/{code} [get]
func (h *OrganizationHandler) PreviewByInviteCode(c *gin.Context) {
	ctx := c.Request.Context()

	inviteCode := c.Param("code")
	tenantID := c.GetUint64(types.TenantIDContextKey.String())

	// Get organization by invite code
	org, err := h.orgService.GetOrganizationByInviteCode(ctx, inviteCode)
	if err != nil {
		c.Error(apperrors.NewNotFoundError("Invalid invite code"))
		return
	}

	// Get member count
	members, _ := h.orgService.ListTenantMembers(ctx, org.ID)
	memberCount := len(members)

	// Get shared knowledge bases count
	shares, _ := h.shareService.ListSharesByOrganization(ctx, org.ID)
	shareCount := len(shares)
	// Get shared agents count
	agentShares, _ := h.agentShareService.ListSharesByOrganization(ctx, org.ID)
	agentShareCount := len(agentShares)

	// Check if caller's tenant is already a member
	_, memberErr := h.orgService.GetTenantMember(ctx, org.ID, tenantID)
	isAlreadyMember := memberErr == nil

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"id":                org.ID,
			"name":              org.Name,
			"description":       org.Description,
			"avatar":            org.Avatar,
			"member_count":      memberCount,
			"share_count":       shareCount,
			"agent_share_count": agentShareCount,
			"is_already_member": isAlreadyMember,
			"require_approval":  org.RequireApproval,
			"created_at":        org.CreatedAt,
		},
	})
}

// JoinByInviteCode joins an organization by invite code
// @Summary      通过邀请码加入组织
// @Description  使用邀请码加入组织
// @Tags         组织管理
// @Accept       json
// @Produce      json
// @Param        request  body      types.JoinOrganizationRequest  true  "邀请码"
// @Success      200      {object}  map[string]interface{}
// @Failure      404      {object}  apperrors.AppError
// @Security     Bearer
// @Router       /organizations/join [post]
func (h *OrganizationHandler) JoinByInviteCode(c *gin.Context) {
	ctx := c.Request.Context()

	userID := c.GetString(types.UserIDContextKey.String())
	tenantID := c.GetUint64(types.TenantIDContextKey.String())

	var req types.JoinOrganizationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(apperrors.NewValidationError("Invalid request parameters").WithDetails(err.Error()))
		return
	}

	org, err := h.orgService.JoinByInviteCode(ctx, req.InviteCode, userID, tenantID)
	if err != nil {
		logger.Errorf(ctx, "Failed to join organization: %v", err)
		if errors.Is(err, service.ErrOrgMemberLimitReached) {
			c.Error(apperrors.NewValidationError("该空间成员已满，无法加入"))
			return
		}
		c.Error(apperrors.NewNotFoundError("Invalid invite code"))
		return
	}

	logger.Infof(ctx, "User %s joined organization %s", secutils.SanitizeForLog(userID), org.ID)
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    h.toOrgResponse(ctx, org, userID),
	})
}

// SubmitJoinRequest submits a join request for organizations that require approval
// @Summary      提交加入申请
// @Description  对需要审核的组织提交加入申请
// @Tags         组织管理
// @Accept       json
// @Produce      json
// @Param        request  body      types.SubmitJoinRequestRequest  true  "申请信息"
// @Success      200      {object}  map[string]interface{}
// @Failure      400      {object}  apperrors.AppError
// @Security     Bearer
// @Router       /organizations/join-request [post]
func (h *OrganizationHandler) SubmitJoinRequest(c *gin.Context) {
	ctx := c.Request.Context()

	userID := c.GetString(types.UserIDContextKey.String())
	tenantID := c.GetUint64(types.TenantIDContextKey.String())

	var req types.SubmitJoinRequestRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(apperrors.NewValidationError("Invalid request parameters").WithDetails(err.Error()))
		return
	}

	// Get organization by invite code
	org, err := h.orgService.GetOrganizationByInviteCode(ctx, req.InviteCode)
	if err != nil {
		c.Error(apperrors.NewNotFoundError("Invalid invite code"))
		return
	}

	// Check if organization requires approval
	if !org.RequireApproval {
		c.Error(apperrors.NewValidationError("This organization does not require approval. Use the join endpoint instead."))
		return
	}

	// Check if caller's tenant is already a member
	_, memberErr := h.orgService.GetTenantMember(ctx, org.ID, tenantID)
	if memberErr == nil {
		c.Error(apperrors.NewValidationError("Your tenant is already a member of this organization"))
		return
	}

	// Validate requested role: only viewer/editor/admin allowed
	requestedRole := req.Role
	if requestedRole != "" && !requestedRole.IsValid() {
		c.Error(apperrors.NewValidationError("Invalid role; must be viewer, editor, or admin"))
		return
	}

	// Submit join request (service defaults to viewer if role empty)
	request, err := h.orgService.SubmitJoinRequest(ctx, org.ID, userID, tenantID, req.Message, requestedRole)
	if err != nil {
		logger.Errorf(ctx, "Failed to submit join request: %v", err)
		if errors.Is(err, service.ErrOrgMemberLimitReached) {
			c.Error(apperrors.NewValidationError("该空间成员已满，无法提交加入申请"))
			return
		}
		if err.Error() == "pending request already exists" {
			c.Error(apperrors.NewValidationError("You have already submitted a request to join this organization"))
			return
		}
		c.Error(apperrors.NewInternalServerError("Failed to submit join request"))
		return
	}

	logger.Infof(ctx, "User %s submitted join request for organization %s", secutils.SanitizeForLog(userID), org.ID)
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    request,
	})
}

// SearchOrganizations returns searchable (discoverable) organizations
// @Summary      搜索可加入的空间
// @Description  搜索已开放可被搜索的空间，用于发现并加入
// @Tags         组织管理
// @Produce      json
// @Param        q      query  string  false  "搜索关键词（空间名称或描述）"
// @Param        limit  query  int     false  "返回数量限制" default(20)
// @Success      200    {object}  map[string]interface{}
// @Security     Bearer
// @Router       /organizations/search [get]
func (h *OrganizationHandler) SearchOrganizations(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID := c.GetUint64(types.TenantIDContextKey.String())
	query := c.Query("q")
	limit := 20
	if l := c.Query("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 && n <= 100 {
			limit = n
		}
	}
	resp, err := h.orgService.SearchSearchableOrganizations(ctx, tenantID, query, limit)
	if err != nil {
		logger.Errorf(ctx, "Failed to search organizations: %v", err)
		c.Error(apperrors.NewInternalServerError("Failed to search organizations"))
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    resp.Organizations,
		"total":   resp.Total,
	})
}

// JoinByOrganizationID joins a searchable organization by ID (no invite code)
// @Summary      通过空间 ID 加入（可搜索空间）
// @Description  加入已开放可被搜索的空间，无需邀请码
// @Tags         组织管理
// @Accept       json
// @Produce      json
// @Param        request  body      types.JoinByOrganizationIDRequest  true  "空间 ID"
// @Success      200      {object}  map[string]interface{}
// @Failure      403      {object}  apperrors.AppError
// @Security     Bearer
// @Router       /organizations/join-by-id [post]
func (h *OrganizationHandler) JoinByOrganizationID(c *gin.Context) {
	ctx := c.Request.Context()
	userID := c.GetString(types.UserIDContextKey.String())
	tenantID := c.GetUint64(types.TenantIDContextKey.String())
	var req types.JoinByOrganizationIDRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(apperrors.NewValidationError("Invalid request parameters").WithDetails(err.Error()))
		return
	}
	// Validate requested role if provided
	requestedRole := req.Role
	if requestedRole != "" && !requestedRole.IsValid() {
		c.Error(apperrors.NewValidationError("Invalid role; must be viewer, editor, or admin"))
		return
	}
	org, err := h.orgService.JoinByOrganizationID(ctx, req.OrganizationID, userID, tenantID, req.Message, requestedRole)
	if err != nil {
		logger.Errorf(ctx, "Failed to join organization by ID: %v", err)
		if errors.Is(err, service.ErrOrgNotFound) {
			c.Error(apperrors.NewNotFoundError("Organization not found or not open for search"))
			return
		}
		if errors.Is(err, service.ErrOrgPermissionDenied) {
			c.Error(apperrors.NewForbiddenError("Organization not open for search"))
			return
		}
		if errors.Is(err, service.ErrOrgMemberLimitReached) {
			c.Error(apperrors.NewValidationError("该空间成员已满，无法加入"))
			return
		}
		if errors.Is(err, service.ErrInvalidRole) {
			c.Error(apperrors.NewValidationError("Invalid role"))
			return
		}
		c.Error(apperrors.NewInternalServerError("Failed to join organization"))
		return
	}
	logger.Infof(ctx, "User %s joined organization %s by ID", secutils.SanitizeForLog(userID), org.ID)
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    h.toOrgResponse(ctx, org, userID),
	})
}

// RequestRoleUpgrade submits a request to upgrade role in an organization
// @Summary      申请权限升级
// @Description  现有成员申请更高权限
// @Tags         组织管理
// @Accept       json
// @Produce      json
// @Param        id       path      string                          true  "组织ID"
// @Param        request  body      types.RequestRoleUpgradeRequest  true  "申请信息"
// @Success      200      {object}  map[string]interface{}
// @Failure      400      {object}  apperrors.AppError
// @Security     Bearer
// @Router       /organizations/{id}/request-upgrade [post]
func (h *OrganizationHandler) RequestRoleUpgrade(c *gin.Context) {
	ctx := c.Request.Context()

	orgID := c.Param("id")
	userID := c.GetString(types.UserIDContextKey.String())
	tenantID := c.GetUint64(types.TenantIDContextKey.String())

	var req types.RequestRoleUpgradeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(apperrors.NewValidationError("Invalid request parameters").WithDetails(err.Error()))
		return
	}

	// Validate requested role
	if !req.RequestedRole.IsValid() {
		c.Error(apperrors.NewValidationError("Invalid role; must be viewer, editor, or admin"))
		return
	}

	request, err := h.orgService.RequestRoleUpgrade(ctx, orgID, userID, tenantID, req.RequestedRole, req.Message)
	if err != nil {
		logger.Errorf(ctx, "Failed to submit role upgrade request: %v", err)
		if err.Error() == "pending request already exists" {
			c.Error(apperrors.NewValidationError("You already have a pending upgrade request"))
			return
		}
		if err.Error() == "user is not a member of this organization" {
			c.Error(apperrors.NewValidationError("You are not a member of this organization"))
			return
		}
		if err.Error() == "user is already an admin" {
			c.Error(apperrors.NewValidationError("You are already an admin"))
			return
		}
		if err.Error() == "cannot request upgrade to same or lower role" {
			c.Error(apperrors.NewValidationError("Cannot request upgrade to same or lower role"))
			return
		}
		c.Error(apperrors.NewInternalServerError("Failed to submit upgrade request"))
		return
	}

	logger.Infof(ctx, "User %s submitted role upgrade request for organization %s", secutils.SanitizeForLog(userID), orgID)
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    request,
	})
}

// LeaveOrganization allows a user to leave an organization
// @Summary      退出组织
// @Description  退出指定组织
// @Tags         组织管理
// @Param        id  path  string  true  "组织ID"
// @Success      200  {object}  map[string]interface{}
// @Failure      403  {object}  apperrors.AppError
// @Security     Bearer
// @Router       /organizations/{id}/leave [post]
func (h *OrganizationHandler) LeaveOrganization(c *gin.Context) {
	ctx := c.Request.Context()

	orgID := c.Param("id")
	userID := c.GetString(types.UserIDContextKey.String())
	tenantID := c.GetUint64(types.TenantIDContextKey.String())

	// Check if caller's tenant is the owner tenant. Post-Plan-3, "owner
	// can't leave" is a tenant-level rule: the owner_tenant_id row is the
	// one that may not depart the org. Legacy rows with OwnerTenantID == 0
	// fall back to the user-level rule so we don't break pre-000046 data.
	org, err := h.orgService.GetOrganization(ctx, orgID)
	if err != nil {
		c.Error(apperrors.NewNotFoundError("Organization not found"))
		return
	}

	isOwnerTenant := org.OwnerTenantID != 0 && org.OwnerTenantID == tenantID
	if isOwnerTenant || (org.OwnerTenantID == 0 && org.OwnerID == userID) {
		c.Error(apperrors.NewForbiddenError("Organization owner cannot leave. Please transfer ownership or delete the organization."))
		return
	}

	// Remove the caller's tenant from the organization (self-leave)
	if err := h.orgService.RemoveTenantMember(ctx, orgID, tenantID, userID, tenantID); err != nil {
		logger.Errorf(ctx, "Failed to leave organization: %v", err)
		c.Error(apperrors.NewInternalServerError("Failed to leave organization"))
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Left organization successfully",
	})
}

// ListJoinRequests lists pending join requests for an organization (admin only)
// @Summary      获取待审核加入申请列表
// @Description  获取组织的待审核加入申请（仅管理员）
// @Tags         组织管理
// @Produce      json
// @Param        id   path  string  true  "组织ID"
// @Success      200  {object}  map[string]interface{}
// @Failure      403  {object}  apperrors.AppError
// @Security     Bearer
// @Router       /organizations/{id}/join-requests [get]
func (h *OrganizationHandler) ListJoinRequests(c *gin.Context) {
	ctx := c.Request.Context()

	orgID := c.Param("id")
	tenantID := c.GetUint64(types.TenantIDContextKey.String())

	// Check admin: caller's tenant must be admin in the org
	isAdmin, err := h.orgService.IsTenantOrgAdmin(ctx, orgID, tenantID)
	if err != nil || !isAdmin {
		c.Error(apperrors.NewForbiddenError("Only organization admins can view join requests"))
		return
	}

	requests, err := h.orgService.ListJoinRequests(ctx, orgID)
	if err != nil {
		logger.Errorf(ctx, "Failed to list join requests: %v", err)
		c.Error(apperrors.NewInternalServerError("Failed to list join requests"))
		return
	}

	// Only return pending requests for approval UI
	resp := make([]types.JoinRequestResponse, 0)
	for _, r := range requests {
		if r.Status != types.JoinRequestStatusPending {
			continue
		}
		item := types.JoinRequestResponse{
			ID:            r.ID,
			UserID:        r.UserID,
			Message:       r.Message,
			RequestType:   string(r.RequestType),
			PrevRole:      string(r.PrevRole),
			RequestedRole: string(r.RequestedRole),
			Status:        string(r.Status),
			CreatedAt:     r.CreatedAt,
			ReviewedAt:    r.ReviewedAt,
		}
		// Default request_type to 'join' for backward compatibility
		if item.RequestType == "" {
			item.RequestType = string(types.JoinRequestTypeJoin)
		}
		if r.User != nil {
			item.Username = r.User.Username
			item.Email = r.User.Email
		}
		resp = append(resp, item)
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": types.ListJoinRequestsResponse{
			Requests: resp,
			Total:    int64(len(resp)),
		},
	})
}

// ReviewJoinRequest approves or rejects a join request (admin only)
// @Summary      审核加入申请
// @Description  通过或拒绝加入申请（仅管理员）
// @Tags         组织管理
// @Accept       json
// @Produce      json
// @Param        id          path  string  true  "组织ID"
// @Param        request_id  path  string  true  "申请ID"
// @Param        request    body  types.ReviewJoinRequestRequest  true  "审核结果"
// @Success      200  {object}  map[string]interface{}
// @Failure      403  {object}  apperrors.AppError
// @Security     Bearer
// @Router       /organizations/{id}/join-requests/{request_id}/review [put]
func (h *OrganizationHandler) ReviewJoinRequest(c *gin.Context) {
	ctx := c.Request.Context()

	orgID := c.Param("id")
	requestID := c.Param("request_id")
	userID := c.GetString(types.UserIDContextKey.String())
	tenantID := c.GetUint64(types.TenantIDContextKey.String())

	// Check admin: caller's tenant must be admin in the org
	isAdmin, err := h.orgService.IsTenantOrgAdmin(ctx, orgID, tenantID)
	if err != nil || !isAdmin {
		c.Error(apperrors.NewForbiddenError("Only organization admins can review join requests"))
		return
	}

	var req types.ReviewJoinRequestRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(apperrors.NewValidationError("Invalid request parameters").WithDetails(err.Error()))
		return
	}
	var assignRole *types.OrgMemberRole
	if req.Role != "" {
		if !req.Role.IsValid() {
			c.Error(apperrors.NewValidationError("Invalid role; must be viewer, editor, or admin"))
			return
		}
		assignRole = &req.Role
	}

	if err := h.orgService.ReviewJoinRequest(ctx, orgID, requestID, req.Approved, userID, tenantID, req.Message, assignRole); err != nil {
		logger.Errorf(ctx, "Failed to review join request: %v", err)
		if errors.Is(err, service.ErrOrgMemberLimitReached) {
			c.Error(apperrors.NewValidationError("空间成员已满，无法通过该加入申请"))
			return
		}
		if err.Error() == "request has already been reviewed" {
			c.Error(apperrors.NewValidationError("Request has already been reviewed"))
			return
		}
		c.Error(apperrors.NewInternalServerError("Failed to review join request"))
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Review completed",
	})
}

// ShareKnowledgeBase shares a knowledge base to an organization
// @Summary      共享知识库到组织
// @Description  将知识库共享到指定组织
// @Tags         知识库共享
// @Accept       json
// @Produce      json
// @Param        id       path      string                         true  "知识库ID"
// @Param        request  body      types.ShareKnowledgeBaseRequest  true  "共享信息"
// @Success      201      {object}  map[string]interface{}
// @Failure      403      {object}  apperrors.AppError
// @Security     Bearer
// @Router       /knowledge-bases/{id}/shares [post]
func (h *OrganizationHandler) ShareKnowledgeBase(c *gin.Context) {
	ctx := c.Request.Context()

	kbID := c.Param("id")
	userID := c.GetString(types.UserIDContextKey.String())
	tenantID := c.GetUint64(types.TenantIDContextKey.String())

	var req types.ShareKnowledgeBaseRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(apperrors.NewValidationError("Invalid request parameters").WithDetails(err.Error()))
		return
	}

	share, err := h.shareService.ShareKnowledgeBase(ctx, kbID, req.OrganizationID, userID, tenantID, req.Permission)
	if err != nil {
		logger.Errorf(ctx, "Failed to share knowledge base: %v", err)
		if errors.Is(err, service.ErrOrgRoleCannotShare) {
			c.Error(apperrors.NewForbiddenError("Only editors and admins can share knowledge bases to this organization"))
			return
		}
		c.Error(apperrors.NewForbiddenError("Permission denied or invalid operation"))
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"success": true,
		"data":    share,
	})
}

// ListKBShares lists all shares for a knowledge base
// @Summary      获取知识库的共享列表
// @Description  获取知识库的所有共享记录
// @Tags         知识库共享
// @Produce      json
// @Param        id  path  string  true  "知识库ID"
// @Success      200  {object}  types.ListSharesResponse
// @Security     Bearer
// @Router       /knowledge-bases/{id}/shares [get]
func (h *OrganizationHandler) ListKBShares(c *gin.Context) {
	ctx := c.Request.Context()

	kbID := c.Param("id")
	tenantID := c.GetUint64(types.TenantIDContextKey.String())
	if tenantID == 0 {
		c.Error(apperrors.NewUnauthorizedError("Unauthorized"))
		return
	}

	shares, err := h.shareService.ListSharesByKnowledgeBase(ctx, kbID, tenantID)
	if err != nil {
		if errors.Is(err, service.ErrKBNotFound) {
			c.Error(apperrors.NewNotFoundError("Knowledge base not found"))
			return
		}
		if errors.Is(err, service.ErrNotKBOwner) {
			c.Error(apperrors.NewForbiddenError("Only the knowledge base owner can list its shares"))
			return
		}
		logger.Errorf(ctx, "Failed to list shares: %v", err)
		c.Error(apperrors.NewInternalServerError("Failed to list shares"))
		return
	}

	response := make([]types.KnowledgeBaseShareResponse, 0, len(shares))
	for _, s := range shares {
		resp := types.KnowledgeBaseShareResponse{
			ID:              s.ID,
			KnowledgeBaseID: s.KnowledgeBaseID,
			OrganizationID:  s.OrganizationID,
			SharedByUserID:  s.SharedByUserID,
			SourceTenantID:  s.SourceTenantID,
			Permission:      string(s.Permission),
			CreatedAt:       s.CreatedAt,
		}
		if s.Organization != nil {
			resp.OrganizationName = s.Organization.Name
		}
		response = append(response, resp)
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": types.ListSharesResponse{
			Shares: response,
			Total:  int64(len(response)),
		},
	})
}

// UpdateSharePermission updates the permission of a share
// @Summary      更新共享权限
// @Description  更新知识库共享的权限级别
// @Tags         知识库共享
// @Accept       json
// @Produce      json
// @Param        id        path      string                          true  "知识库ID"
// @Param        share_id  path      string                          true  "共享记录ID"
// @Param        request   body      types.UpdateSharePermissionRequest  true  "权限信息"
// @Success      200       {object}  map[string]interface{}
// @Failure      403       {object}  apperrors.AppError
// @Security     Bearer
// @Router       /knowledge-bases/{id}/shares/{share_id} [put]
func (h *OrganizationHandler) UpdateSharePermission(c *gin.Context) {
	ctx := c.Request.Context()

	shareID := c.Param("share_id")
	userID := c.GetString(types.UserIDContextKey.String())
	tenantID := c.GetUint64(types.TenantIDContextKey.String())

	var req types.UpdateSharePermissionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(apperrors.NewValidationError("Invalid request parameters").WithDetails(err.Error()))
		return
	}

	if err := h.shareService.UpdateSharePermission(ctx, shareID, req.Permission, userID, tenantID); err != nil {
		logger.Errorf(ctx, "Failed to update share permission: %v", err)
		c.Error(apperrors.NewForbiddenError("Permission denied"))
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Share permission updated successfully",
	})
}

// RemoveShare removes a share
// @Summary      取消共享
// @Description  取消知识库的共享
// @Tags         知识库共享
// @Param        id        path  string  true  "知识库ID"
// @Param        share_id  path  string  true  "共享记录ID"
// @Success      200       {object}  map[string]interface{}
// @Failure      403       {object}  apperrors.AppError
// @Security     Bearer
// @Router       /knowledge-bases/{id}/shares/{share_id} [delete]
func (h *OrganizationHandler) RemoveShare(c *gin.Context) {
	ctx := c.Request.Context()

	shareID := c.Param("share_id")
	userID := c.GetString(types.UserIDContextKey.String())
	tenantID := c.GetUint64(types.TenantIDContextKey.String())

	if err := h.shareService.RemoveShare(ctx, shareID, userID, tenantID); err != nil {
		logger.Errorf(ctx, "Failed to remove share: %v", err)
		c.Error(apperrors.NewForbiddenError("Permission denied"))
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Share removed successfully",
	})
}

// ListOrgShares lists all knowledge bases shared to a specific organization
// @Summary      获取组织的共享知识库列表
// @Description  获取共享到指定组织的所有知识库
// @Tags         组织管理
// @Produce      json
// @Param        id  path  string  true  "组织ID"
// @Success      200  {object}  types.ListSharesResponse
// @Security     Bearer
// @Router       /organizations/{id}/shares [get]
func (h *OrganizationHandler) ListOrgShares(c *gin.Context) {
	ctx := c.Request.Context()

	orgID := c.Param("id")
	tenantID := c.GetUint64(types.TenantIDContextKey.String())

	// Check if caller's tenant is a member and get its role for effective-permission calculation
	member, err := h.orgService.GetTenantMember(ctx, orgID, tenantID)
	if err != nil {
		c.Error(apperrors.NewForbiddenError("Your tenant is not a member of this organization"))
		return
	}
	myRoleInOrg := member.Role

	shares, err := h.shareService.ListSharesByOrganization(ctx, orgID)
	if err != nil {
		logger.Errorf(ctx, "Failed to list organization shares: %v", err)
		c.Error(apperrors.NewInternalServerError("Failed to list shares"))
		return
	}

	response := make([]types.KnowledgeBaseShareResponse, 0, len(shares))
	for _, s := range shares {
		// Effective permission for current user = min(share permission, my role in org)
		effectivePerm := s.Permission
		if !myRoleInOrg.HasPermission(s.Permission) {
			effectivePerm = myRoleInOrg
		}
		resp := types.KnowledgeBaseShareResponse{
			ID:              s.ID,
			KnowledgeBaseID: s.KnowledgeBaseID,
			OrganizationID:  s.OrganizationID,
			SharedByUserID:  s.SharedByUserID,
			SourceTenantID:  s.SourceTenantID,
			Permission:      string(s.Permission),
			MyRoleInOrg:     string(myRoleInOrg),
			MyPermission:    string(effectivePerm),
			CreatedAt:       s.CreatedAt,
		}
		if s.KnowledgeBase != nil {
			resp.KnowledgeBaseName = s.KnowledgeBase.Name
			resp.KnowledgeBaseType = s.KnowledgeBase.Type
			// Get knowledge count for document type
			if count, err := h.knowledgeRepo.CountKnowledgeByKnowledgeBaseID(ctx, s.SourceTenantID, s.KnowledgeBaseID); err == nil {
				resp.KnowledgeCount = count
			}
			// Get chunk count for FAQ type
			if count, err := h.chunkRepo.CountChunksByKnowledgeBaseID(ctx, s.SourceTenantID, s.KnowledgeBaseID); err == nil {
				resp.ChunkCount = count
			}
		}
		// Get shared by user info
		if user, err := h.userService.GetUserByID(ctx, s.SharedByUserID); err == nil && user != nil {
			resp.SharedByUsername = user.Username
		}
		response = append(response, resp)
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": types.ListSharesResponse{
			Shares: response,
			Total:  int64(len(response)),
		},
	})
}

// ListSharedKnowledgeBases lists all knowledge bases shared to the current user
// @Summary      获取共享给我的知识库列表
// @Description  获取通过组织共享给当前用户的所有知识库
// @Tags         知识库共享
// @Produce      json
// @Success      200  {object}  map[string]interface{}
// @Security     Bearer
// @Router       /shared-knowledge-bases [get]
func (h *OrganizationHandler) ListSharedKnowledgeBases(c *gin.Context) {
	ctx := c.Request.Context()

	tenantID := types.MustTenantIDFromContext(ctx)
	callerTenantRole := types.TenantRoleFromContext(ctx)

	sharedKBs, err := h.shareService.ListSharedKnowledgeBases(ctx, tenantID, callerTenantRole)
	if err != nil {
		logger.Errorf(ctx, "Failed to list shared knowledge bases: %v", err)
		c.Error(apperrors.NewInternalServerError("Failed to list shared knowledge bases"))
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    sharedKBs,
		"total":   len(sharedKBs),
	})
}

// ShareAgent shares an agent to an organization
func (h *OrganizationHandler) ShareAgent(c *gin.Context) {
	ctx := c.Request.Context()
	agentID := c.Param("id")
	userID := c.GetString(types.UserIDContextKey.String())
	tenantID := c.GetUint64(types.TenantIDContextKey.String())

	var req types.ShareKnowledgeBaseRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(apperrors.NewValidationError("Invalid request parameters").WithDetails(err.Error()))
		return
	}

	share, err := h.agentShareService.ShareAgent(ctx, agentID, req.OrganizationID, userID, tenantID, req.Permission)
	if err != nil {
		logger.Errorf(ctx, "Failed to share agent: %v", err)
		if errors.Is(err, service.ErrOrgRoleCannotShareAgent) {
			c.Error(apperrors.NewForbiddenError("Only editors and admins can share agents to this organization"))
			return
		}
		if errors.Is(err, service.ErrAgentNotConfigured) {
			c.Error(apperrors.NewValidationError("Agent is not fully configured. Please set the chat model, and set the rerank model if the knowledge_search tool is enabled in agent settings."))
			return
		}
		c.Error(apperrors.NewForbiddenError("Permission denied or invalid operation"))
		return
	}
	c.JSON(http.StatusCreated, gin.H{"success": true, "data": share})
}

// ListAgentShares lists all shares for an agent
func (h *OrganizationHandler) ListAgentShares(c *gin.Context) {
	ctx := c.Request.Context()
	agentID := c.Param("id")
	shares, err := h.agentShareService.ListSharesByAgent(ctx, agentID)
	if err != nil {
		logger.Errorf(ctx, "Failed to list agent shares: %v", err)
		c.Error(apperrors.NewInternalServerError("Failed to list shares"))
		return
	}
	response := make([]types.AgentShareResponse, 0, len(shares))
	for _, s := range shares {
		resp := types.AgentShareResponse{
			ID: s.ID, AgentID: s.AgentID, OrganizationID: s.OrganizationID,
			SharedByUserID: s.SharedByUserID, SourceTenantID: s.SourceTenantID,
			Permission: string(s.Permission), CreatedAt: s.CreatedAt,
		}
		if s.Organization != nil {
			resp.OrganizationName = s.Organization.Name
		}
		response = append(response, resp)
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"shares": response, "total": len(response)}})
}

// RemoveAgentShare removes an agent share.
//
// RemoveAgentShare godoc
// @Summary      取消智能体共享
// @Description  从智能体的共享列表中移除指定共享关系
// @Tags         组织
// @Produce      json
// @Param        id        path      string                  true  "智能体 ID"
// @Param        share_id  path      string                  true  "共享记录 ID"
// @Success      200       {object}  map[string]interface{}  "success: true"
// @Failure      403       {object}  apperrors.AppError         "无权限"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /agents/{id}/shares/{share_id} [delete]
func (h *OrganizationHandler) RemoveAgentShare(c *gin.Context) {
	ctx := c.Request.Context()
	shareID := c.Param("share_id")
	userID := c.GetString(types.UserIDContextKey.String())
	tenantID := c.GetUint64(types.TenantIDContextKey.String())
	if err := h.agentShareService.RemoveShare(ctx, shareID, userID, tenantID); err != nil {
		logger.Errorf(ctx, "Failed to remove agent share: %v", err)
		c.Error(apperrors.NewForbiddenError("Permission denied"))
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "Share removed successfully"})
}

// ListOrgAgentShares lists all agents shared to an organization.
//
// ListOrgAgentShares godoc
// @Summary      获取共享到本组织的智能体
// @Description  返回所有被共享到指定组织的智能体（含我的有效权限）
// @Tags         组织
// @Produce      json
// @Param        id   path      string                  true  "组织 ID"
// @Success      200  {object}  map[string]interface{}  "智能体共享列表 + total"
// @Failure      403  {object}  apperrors.AppError         "非组织成员"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /organizations/{id}/agent-shares [get]
func (h *OrganizationHandler) ListOrgAgentShares(c *gin.Context) {
	ctx := c.Request.Context()
	orgID := c.Param("id")
	tenantID := c.GetUint64(types.TenantIDContextKey.String())
	member, err := h.orgService.GetTenantMember(ctx, orgID, tenantID)
	if err != nil {
		c.Error(apperrors.NewForbiddenError("Your tenant is not a member of this organization"))
		return
	}
	myRoleInOrg := member.Role
	shares, err := h.agentShareService.ListSharesByOrganization(ctx, orgID)
	if err != nil {
		logger.Errorf(ctx, "Failed to list organization agent shares: %v", err)
		c.Error(apperrors.NewInternalServerError("Failed to list shares"))
		return
	}
	response := make([]types.AgentShareResponse, 0, len(shares))
	for _, s := range shares {
		effectivePerm := s.Permission
		if !myRoleInOrg.HasPermission(s.Permission) {
			effectivePerm = myRoleInOrg
		}
		resp := types.AgentShareResponse{
			ID: s.ID, AgentID: s.AgentID, OrganizationID: s.OrganizationID,
			SharedByUserID: s.SharedByUserID, SourceTenantID: s.SourceTenantID,
			Permission: string(s.Permission), MyRoleInOrg: string(myRoleInOrg), MyPermission: string(effectivePerm), CreatedAt: s.CreatedAt,
		}
		if s.Agent != nil {
			resp.AgentName = s.Agent.Name
			resp.AgentAvatar = s.Agent.Avatar
			cfg := &s.Agent.Config
			if cfg.KBSelectionMode != "" {
				resp.ScopeKB = cfg.KBSelectionMode
				if cfg.KBSelectionMode == "selected" && len(cfg.KnowledgeBases) > 0 {
					resp.ScopeKBCount = len(cfg.KnowledgeBases)
				}
			} else {
				resp.ScopeKB = "none"
			}
			resp.ScopeWebSearch = cfg.WebSearchEnabled
			if cfg.MCPSelectionMode != "" {
				resp.ScopeMCP = cfg.MCPSelectionMode
				if cfg.MCPSelectionMode == "selected" && len(cfg.MCPServices) > 0 {
					resp.ScopeMCPCount = len(cfg.MCPServices)
				}
			} else {
				resp.ScopeMCP = "none"
			}
		}
		if s.Organization != nil {
			resp.OrganizationName = s.Organization.Name
		}
		if u, err := h.userService.GetUserByID(ctx, s.SharedByUserID); err == nil && u != nil {
			resp.SharedByUsername = u.Username
		}
		response = append(response, resp)
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"shares": response, "total": len(response)}})
}

// ListSharedAgents lists agents shared to the current user.
//
// ListSharedAgents godoc
// @Summary      获取我可访问的共享智能体
// @Description  返回所有共享给当前用户所在组织的智能体
// @Tags         组织
// @Produce      json
// @Success      200  {object}  map[string]interface{}  "智能体列表 + total"
// @Failure      500  {object}  apperrors.AppError         "服务器错误"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /shared-agents [get]
func (h *OrganizationHandler) ListSharedAgents(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID := c.GetUint64(types.TenantIDContextKey.String())
	callerTenantRole := types.TenantRoleFromContext(ctx)
	list, err := h.agentShareService.ListSharedAgents(ctx, tenantID, callerTenantRole)
	if err != nil {
		logger.Errorf(ctx, "Failed to list shared agents: %v", err)
		c.Error(apperrors.NewInternalServerError("Failed to list shared agents"))
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": list, "total": len(list)})
}

// listSpaceKnowledgeBasesInOrganization returns merged list of direct shared KBs and agent-carried KBs in the org (for list and count).
func (h *OrganizationHandler) listSpaceKnowledgeBasesInOrganization(ctx context.Context, orgID string, tenantID uint64, callerTenantRole types.TenantRole) ([]*types.OrganizationSharedKnowledgeBaseItem, error) {
	directList, err := h.shareService.ListSharedKnowledgeBasesInOrganization(ctx, orgID, tenantID, callerTenantRole)
	if err != nil {
		return nil, err
	}

	directKbIDs := make(map[string]bool)
	for _, item := range directList {
		if item.KnowledgeBase != nil && item.KnowledgeBase.ID != "" {
			directKbIDs[item.KnowledgeBase.ID] = true
		}
	}

	agentList, err := h.agentShareService.ListSharedAgentsInOrganization(ctx, orgID, tenantID, callerTenantRole)
	if err != nil {
		return directList, nil
	}

	orgName := ""
	if len(agentList) > 0 && agentList[0].OrganizationID == orgID {
		orgName = agentList[0].OrgName
	}
	if orgName == "" {
		if org, err := h.orgService.GetOrganization(ctx, orgID); err == nil && org != nil {
			orgName = org.Name
		}
	}

	merged := make([]*types.OrganizationSharedKnowledgeBaseItem, 0, len(directList)+64)
	merged = append(merged, directList...)

	for _, agentItem := range agentList {
		if agentItem.Agent == nil {
			continue
		}
		agent := agentItem.Agent
		mode := agent.Config.KBSelectionMode
		if mode == "none" {
			continue
		}

		var kbIDs []string
		switch mode {
		case "selected":
			if len(agent.Config.KnowledgeBases) == 0 {
				continue
			}
			kbIDs = agent.Config.KnowledgeBases
		case "all":
			kbs, err := h.kbService.ListKnowledgeBasesByTenantID(ctx, agent.TenantID)
			if err != nil {
				logger.Warnf(ctx, "ListKnowledgeBasesByTenantID for agent %s: %v", agent.ID, err)
				continue
			}
			kbIDs = make([]string, 0, len(kbs))
			for _, kb := range kbs {
				if kb != nil && kb.ID != "" {
					kbIDs = append(kbIDs, kb.ID)
				}
			}
		default:
			if len(agent.Config.KnowledgeBases) > 0 {
				kbIDs = agent.Config.KnowledgeBases
			}
		}

		agentName := agent.Name
		if agentName == "" {
			agentName = agent.ID
		}
		sourceTenantID := agent.TenantID

		for _, kbID := range kbIDs {
			if kbID == "" || directKbIDs[kbID] {
				continue
			}
			kb, err := h.kbService.GetKnowledgeBaseByIDOnly(ctx, kbID)
			if err != nil || kb == nil {
				continue
			}
			if kb.TenantID != sourceTenantID {
				continue
			}
			directKbIDs[kbID] = true

			switch kb.Type {
			case types.KnowledgeBaseTypeDocument:
				if count, err := h.knowledgeRepo.CountKnowledgeByKnowledgeBaseID(ctx, sourceTenantID, kb.ID); err == nil {
					kb.KnowledgeCount = count
				}
			case types.KnowledgeBaseTypeFAQ:
				if count, err := h.chunkRepo.CountChunksByKnowledgeBaseID(ctx, sourceTenantID, kb.ID); err == nil {
					kb.ChunkCount = count
				}
			}

			merged = append(merged, &types.OrganizationSharedKnowledgeBaseItem{
				SharedKnowledgeBaseInfo: types.SharedKnowledgeBaseInfo{
					KnowledgeBase:  kb,
					ShareID:        "",
					OrganizationID: orgID,
					OrgName:        orgName,
					Permission:     types.OrgRoleViewer,
					SourceTenantID: sourceTenantID,
					SharedAt:       agentItem.SharedAt,
				},
				IsMine: false,
				SourceFromAgent: &types.SourceFromAgentInfo{
					AgentID:         agent.ID,
					AgentName:       agentName,
					KBSelectionMode: agent.Config.KBSelectionMode,
				},
			})
		}
	}

	return merged, nil
}

// ListOrganizationSharedKnowledgeBases lists all knowledge bases in the given organization (including those shared by the current tenant and those from shared agents), for the list page when a space is selected.
// @Summary      获取空间内全部知识库（含我共享的、含智能体携带的）
// @Description  获取指定空间下所有共享知识库，包含直接共享的与通过共享智能体可见的，用于列表页空间视角
// @Tags         组织管理
// @Produce      json
// @Param        id  path  string  true  "组织ID"
// @Success      200  {object}  map[string]interface{}
// @Security     Bearer
// @Router       /organizations/{id}/shared-knowledge-bases [get]
func (h *OrganizationHandler) ListOrganizationSharedKnowledgeBases(c *gin.Context) {
	ctx := c.Request.Context()
	orgID := c.Param("id")
	tenantID := c.GetUint64(types.TenantIDContextKey.String())
	callerTenantRole := types.TenantRoleFromContext(ctx)

	list, err := h.listSpaceKnowledgeBasesInOrganization(ctx, orgID, tenantID, callerTenantRole)
	if err != nil {
		if errors.Is(err, service.ErrTenantNotInOrg) {
			c.Error(apperrors.NewForbiddenError("Your tenant is not a member of this organization"))
			return
		}
		logger.Errorf(ctx, "Failed to list organization shared knowledge bases: %v", err)
		c.Error(apperrors.NewInternalServerError("Failed to list shared knowledge bases"))
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": list, "total": len(list)})
}

// ListOrganizationSharedAgents lists all agents in the given organization (including those shared by the current tenant), for the list page when a space is selected.
// @Summary      获取空间内全部智能体（含我共享的）
// @Description  获取指定空间下所有共享智能体，包含他人共享的与我共享的，用于列表页空间视角
// @Tags         组织管理
// @Produce      json
// @Param        id  path  string  true  "组织ID"
// @Success      200  {object}  map[string]interface{}
// @Security     Bearer
// @Router       /organizations/{id}/shared-agents [get]
func (h *OrganizationHandler) ListOrganizationSharedAgents(c *gin.Context) {
	ctx := c.Request.Context()
	orgID := c.Param("id")
	tenantID := c.GetUint64(types.TenantIDContextKey.String())
	callerTenantRole := types.TenantRoleFromContext(ctx)

	list, err := h.agentShareService.ListSharedAgentsInOrganization(ctx, orgID, tenantID, callerTenantRole)
	if err != nil {
		if errors.Is(err, service.ErrTenantNotInOrg) {
			c.Error(apperrors.NewForbiddenError("Your tenant is not a member of this organization"))
			return
		}
		logger.Errorf(ctx, "Failed to list organization shared agents: %v", err)
		c.Error(apperrors.NewInternalServerError("Failed to list shared agents"))
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": list, "total": len(list)})
}

// SetSharedAgentDisabledByMeRequest is the body for POST /shared-agents/disabled
type SetSharedAgentDisabledByMeRequest struct {
	AgentID  string `json:"agent_id" binding:"required"`
	Disabled bool   `json:"disabled"`
}

// SetSharedAgentDisabledByMe sets whether the current tenant has disabled this shared agent for their conversation dropdown
func (h *OrganizationHandler) SetSharedAgentDisabledByMe(c *gin.Context) {
	ctx := c.Request.Context()
	userID := c.GetString(types.UserIDContextKey.String())
	tenantID := c.GetUint64(types.TenantIDContextKey.String())
	uid := userID
	tid := tenantID

	var req SetSharedAgentDisabledByMeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(apperrors.NewBadRequestError("Invalid request").WithDetails(err.Error()))
		return
	}
	// Derive sourceTenantID: own agent (current tenant) or from shared list
	var sourceTenantID uint64
	agent, err := h.customAgentService.GetAgentByID(ctx, req.AgentID)
	if err == nil && agent != nil && agent.TenantID == tid {
		sourceTenantID = tid
	} else {
		share, err := h.agentShareService.GetShareByAgentIDForTenant(ctx, tid, req.AgentID, tid)
		if err != nil || share == nil {
			c.Error(apperrors.NewForbiddenError("No access to this agent"))
			return
		}
		sourceTenantID = share.SourceTenantID
	}
	_ = uid
	if err := h.agentShareService.SetSharedAgentDisabledByMe(ctx, tid, req.AgentID, sourceTenantID, req.Disabled); err != nil {
		logger.Errorf(ctx, "SetSharedAgentDisabledByMe failed: %v", err)
		c.Error(apperrors.NewInternalServerError("Failed to update preference"))
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// toOrgResponse converts an organization to response format
func (h *OrganizationHandler) toOrgResponse(ctx context.Context, org *types.Organization, currentUserID string) types.OrganizationResponse {
	currentTenantID := types.MustTenantIDFromContext(ctx)
	// Post-Plan-3 the canonical "is the caller the owner side?" check
	// is tenant-based: org.OwnerTenantID is the pinned column; legacy
	// rows with OwnerTenantID == 0 (pre-000046, unlikely in prod)
	// fall back to the user-id check so we don't show the wrong tenant
	// as "owner" in those edge cases.
	isOwner := false
	if org.OwnerTenantID != 0 {
		isOwner = org.OwnerTenantID == currentTenantID
	} else {
		isOwner = org.OwnerID == currentUserID
	}
	resp := types.OrganizationResponse{
		ID:                     org.ID,
		Name:                   org.Name,
		Description:            org.Description,
		Avatar:                 org.Avatar,
		OwnerID:                org.OwnerID,
		OwnerTenantID:          org.OwnerTenantID,
		IsOwner:                isOwner,
		RequireApproval:        org.RequireApproval,
		Searchable:             org.Searchable,
		MemberLimit:            org.MemberLimit,
		InviteCodeValidityDays: org.InviteCodeValidityDays,
		CreatedAt:              org.CreatedAt,
		UpdatedAt:              org.UpdatedAt,
	}

	// Get member count (per-tenant)
	if members, err := h.orgService.ListTenantMembers(ctx, org.ID); err == nil {
		resp.MemberCount = len(members)
	}

	// Get shared knowledge base count for this organization
	if shares, err := h.shareService.ListSharesByOrganization(ctx, org.ID); err == nil {
		resp.ShareCount = len(shares)
	}
	// Get shared agent count for this organization
	if agentShares, err := h.agentShareService.ListSharesByOrganization(ctx, org.ID); err == nil {
		resp.AgentShareCount = len(agentShares)
	}

	// Get current tenant's role in this organization
	isAdmin := false
	if role, err := h.orgService.GetTenantRoleInOrg(ctx, org.ID, currentTenantID); err == nil {
		resp.MyRole = string(role)
		isAdmin = (role == types.OrgRoleAdmin)
	}
	// Invite-code / pending-request visibility is keyed on whether the
	// caller can administer the org. Post-Plan-3 that's "isAdmin in the
	// caller's tenant context, OR the caller's tenant is the owner
	// tenant"; we already computed isOwner with the tenant-first logic
	// above, so reuse it instead of comparing user IDs again.
	if isAdmin || isOwner {
		resp.InviteCode = org.InviteCode
		resp.InviteCodeExpiresAt = org.InviteCodeExpiresAt
		if n, err := h.orgService.CountPendingJoinRequests(ctx, org.ID); err == nil {
			resp.PendingJoinRequestCount = int(n)
		}
	}

	// Check if current tenant has pending upgrade request
	if _, err := h.orgService.GetPendingUpgradeRequest(ctx, org.ID, currentTenantID); err == nil {
		resp.HasPendingUpgrade = true
	}

	return resp
}

// SearchTenantsForInvite searches candidate tenants for inviting to organization.
//
// Plan 3 (#1303) makes the tenant the unit of membership. This endpoint replaces
// the older per-user search: it accepts a free-text query, looks up matching users
// (by username/email), groups them by their TenantID, resolves the tenant's
// canonical name, filters out tenants already in the org, and returns one row
// per candidate tenant with one representative user attached for display.
//
// @Summary      搜索可邀请的租户
// @Description  搜索租户（排除已加入的租户）用于邀请加入组织；按租户去重，附带代表用户
// @Tags         组织管理
// @Produce      json
// @Param        id     path   string  true   "组织ID"
// @Param        q      query  string  true   "搜索关键词（租户名、用户名或邮箱）"
// @Param        limit  query  int     false  "返回数量限制" default(10)
// @Success      200    {object}  map[string]interface{}
// @Failure      403    {object}  apperrors.AppError
// @Security     Bearer
// @Router       /organizations/{id}/search-tenants [get]
func (h *OrganizationHandler) SearchTenantsForInvite(c *gin.Context) {
	ctx := c.Request.Context()

	orgID := c.Param("id")
	query := c.Query("q")
	tenantID := c.GetUint64(types.TenantIDContextKey.String())

	// Check admin permission: caller's tenant must be org admin.
	isAdmin, err := h.orgService.IsTenantOrgAdmin(ctx, orgID, tenantID)
	if err != nil || !isAdmin {
		c.Error(apperrors.NewForbiddenError("Only organization admins can invite members"))
		return
	}

	if query == "" {
		c.JSON(http.StatusOK, gin.H{
			"success": true,
			"data":    []types.TenantInviteCandidate{},
		})
		return
	}

	limit := 10
	if l := c.Query("limit"); l != "" {
		if n, errConv := strconv.Atoi(l); errConv == nil && n > 0 && n <= 50 {
			limit = n
		}
	}

	// Exclude tenants already in the org.
	existingMembers, _ := h.orgService.ListTenantMembers(ctx, orgID)
	existingTenantIDs := make(map[uint64]bool, len(existingMembers))
	for _, m := range existingMembers {
		existingTenantIDs[m.TenantID] = true
	}

	// 1) Match users by query and group by TenantID. We over-fetch so the
	//    de-duplication after filtering "already a member" tenants still
	//    leaves us with enough candidates to fill `limit`.
	users, err := h.userService.SearchUsers(ctx, query, limit*3+20)
	if err != nil {
		logger.Errorf(ctx, "Failed to search users: %v", err)
		c.Error(apperrors.NewInternalServerError("Failed to search candidates"))
		return
	}

	// 2) Direct tenant-name match (admins may want to invite by tenant name).
	//    SearchTenants uses page/pageSize; pageSize=limit*2 is a safe ceiling
	//    given the soft cap of 50 above.
	tenantsByName, _, _ := h.tenantService.SearchTenants(ctx, query, 0, 1, limit*2)

	// Insertion-ordered map: first match wins, so the first user that
	// brought a tenant in becomes the representative.
	type entry struct {
		idx       int // preserve search ordering
		candidate types.TenantInviteCandidate
	}
	seen := make(map[uint64]*entry)
	addUser := func(u *types.User) {
		if u == nil || u.TenantID == 0 {
			return
		}
		if existingTenantIDs[u.TenantID] {
			return
		}
		if _, ok := seen[u.TenantID]; ok {
			return
		}
		seen[u.TenantID] = &entry{
			idx: len(seen),
			candidate: types.TenantInviteCandidate{
				TenantID:                u.TenantID,
				RepresentativeUserID:    u.ID,
				RepresentativeUsername:  u.Username,
				RepresentativeEmail:     u.Email,
				RepresentativeAvatar:    u.Avatar,
			},
		}
	}
	for _, u := range users {
		addUser(u)
	}
	addTenantByID := func(tid uint64) {
		if tid == 0 || existingTenantIDs[tid] {
			return
		}
		if _, ok := seen[tid]; ok {
			return
		}
		seen[tid] = &entry{
			idx: len(seen),
			candidate: types.TenantInviteCandidate{
				TenantID: tid,
			},
		}
	}
	for _, t := range tenantsByName {
		if t == nil {
			continue
		}
		addTenantByID(t.ID)
	}

	// Resolve tenant names for all candidates in one round-trip.
	ids := make([]uint64, 0, len(seen))
	for tid := range seen {
		ids = append(ids, tid)
	}
	tenantByID, _ := h.tenantService.GetTenantsByIDs(ctx, ids)
	for tid, e := range seen {
		if t, ok := tenantByID[tid]; ok && t != nil {
			e.candidate.TenantName = t.Name
		}
	}

	// Restore insertion order (idx is unique in [0, len(seen))).
	byIdx := make([]types.TenantInviteCandidate, len(seen))
	for _, e := range seen {
		byIdx[e.idx] = e.candidate
	}
	// Drop tenants we couldn't resolve a name for (defunct rows or
	// deleted tenants) and cap at `limit`.
	sorted := make([]types.TenantInviteCandidate, 0, limit)
	for _, c := range byIdx {
		if c.TenantName == "" {
			continue
		}
		sorted = append(sorted, c)
		if len(sorted) >= limit {
			break
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    sorted,
	})
}

// SearchUsersForInvite is retained as a thin compatibility shim that
// delegates to SearchTenantsForInvite, so older frontends still get
// tenant-grouped results without breaking the call site. The response
// shape here is (intentionally) the new tenant-candidate shape; the
// previous shape returned one row per matching user, which leaked the
// pre-Plan-3 mental model.
//
// @Deprecated  Use SearchTenantsForInvite. Kept for one release.
// @Router      /organizations/{id}/search-users [get]
func (h *OrganizationHandler) SearchUsersForInvite(c *gin.Context) {
	h.SearchTenantsForInvite(c)
}

// InviteMember directly adds a user to organization
// @Summary      邀请成员
// @Description  管理员直接添加用户为组织成员
// @Tags         组织管理
// @Accept       json
// @Produce      json
// @Param        id       path      string                         true  "组织ID"
// @Param        request  body      types.InviteMemberRequest      true  "邀请信息"
// @Success      200      {object}  map[string]interface{}
// @Failure      400      {object}  apperrors.AppError
// @Failure      403      {object}  apperrors.AppError
// @Security     Bearer
// @Router       /organizations/{id}/invite [post]
func (h *OrganizationHandler) InviteMember(c *gin.Context) {
	ctx := c.Request.Context()

	orgID := c.Param("id")
	userID := c.GetString(types.UserIDContextKey.String())
	tenantID := c.GetUint64(types.TenantIDContextKey.String())

	// Check admin permission: caller's tenant must be org admin
	isAdmin, err := h.orgService.IsTenantOrgAdmin(ctx, orgID, tenantID)
	if err != nil || !isAdmin {
		c.Error(apperrors.NewForbiddenError("Only organization admins can invite members"))
		return
	}

	var req types.InviteMemberRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(apperrors.NewValidationError("Invalid request parameters").WithDetails(err.Error()))
		return
	}

	// Validate role
	if !req.Role.IsValid() {
		c.Error(apperrors.NewValidationError("Invalid role; must be viewer, editor, or admin"))
		return
	}

	// Plan 3: resolve the (target tenant, representative user) pair.
	//
	//  - Preferred: caller supplies tenant_id directly (and optionally
	//    representative_user_id) — this matches the tenant-centric mental
	//    model and lets admins invite any user as the rep.
	//  - Legacy:   caller supplies only user_id — handler looks up that
	//    user's tenant and uses the same user as the rep, preserving the
	//    pre-Plan-3 SDK contract.
	targetTenantID := req.TenantID
	representativeUserID := req.RepresentativeUserID
	switch {
	case targetTenantID != 0:
		// Tenant-id path: validate the tenant exists; pick a sensible
		// representative when the caller didn't pin one.
		if _, err := h.tenantService.GetTenantByID(ctx, targetTenantID); err != nil {
			c.Error(apperrors.NewNotFoundError("Tenant not found"))
			return
		}
		if representativeUserID == "" {
			// Fall back to the legacy user_id field if it was sent, so
			// existing clients that learned to send both keep working.
			representativeUserID = req.UserID
		}
		if representativeUserID != "" {
			// If a representative is named, sanity-check it belongs to
			// the target tenant. We don't hard-fail when it doesn't —
			// the membership row is keyed by tenant_id, the rep field
			// is informational — but we strip the inconsistent value
			// so the audit log doesn't lie.
			if u, err := h.userService.GetUserByID(ctx, representativeUserID); err != nil || u == nil || u.TenantID != targetTenantID {
				logger.Warnf(ctx, "representative_user_id %s does not belong to tenant %d; dropping",
					secutils.SanitizeForLog(representativeUserID), targetTenantID)
				representativeUserID = ""
			}
		}
	case req.UserID != "":
		// Legacy path: resolve target tenant from the user.
		invitedUser, err := h.userService.GetUserByID(ctx, req.UserID)
		if err != nil {
			c.Error(apperrors.NewNotFoundError("User not found"))
			return
		}
		targetTenantID = invitedUser.TenantID
		if representativeUserID == "" {
			representativeUserID = req.UserID
		}
	default:
		c.Error(apperrors.NewValidationError("Either tenant_id or user_id is required"))
		return
	}

	// Check if target tenant is already a member of this org.
	if _, memberErr := h.orgService.GetTenantMember(ctx, orgID, targetTenantID); memberErr == nil {
		c.Error(apperrors.NewValidationError("Tenant is already a member of this organization"))
		return
	}

	// Add tenant member with the chosen representative.
	if err := h.orgService.AddTenantMember(ctx, orgID, targetTenantID, representativeUserID, req.Role); err != nil {
		logger.Errorf(ctx, "Failed to add member: %v", err)
		if errors.Is(err, service.ErrOrgMemberLimitReached) {
			c.Error(apperrors.NewValidationError("该空间成员已满，无法添加新成员"))
			return
		}
		c.Error(apperrors.NewInternalServerError("Failed to add member"))
		return
	}

	logger.Infof(ctx, "User %s invited tenant %d (rep user %s) to organization %s with role %s",
		secutils.SanitizeForLog(userID),
		targetTenantID,
		secutils.SanitizeForLog(representativeUserID),
		orgID,
		req.Role)

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Member added successfully",
	})
}
