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
	kbService          interfaces.KnowledgeBaseService
	knowledgeRepo      interfaces.KnowledgeRepository
	chunkRepo          interfaces.ChunkRepository
}

// NewOrganizationHandler creates a new organization handler
func NewOrganizationHandler(
	orgService interfaces.OrganizationService,
	shareService interfaces.KBShareService,
	agentShareService interfaces.AgentShareService,
	customAgentService interfaces.CustomAgentService,
	userService interfaces.UserService,
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
// @Failure      400      {object}  errors.AppError
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
// @Failure      404  {object}  errors.AppError
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

// ListMyOrganizations lists organizations that the current user belongs to.
// Response includes resource_counts (per-org KB/agent counts) for list sidebar so frontend does not need a separate GET /me/resource-counts.
// @Summary      获取我的组织列表
// @Description  获取当前用户所属的所有组织，并附带各空间内知识库/智能体数量
// @Tags         组织管理
// @Produce      json
// @Success      200  {object}  types.ListOrganizationsResponse
// @Security     Bearer
// @Router       /organizations [get]
func (h *OrganizationHandler) ListMyOrganizations(c *gin.Context) {
	ctx := c.Request.Context()
	userID := c.GetString(types.UserIDContextKey.String())
	tenantID := c.GetUint64(types.TenantIDContextKey.String())

	orgs, err := h.orgService.ListUserOrganizations(ctx, userID)
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
	directKBIDsByOrg, err := h.shareService.ListSharedKnowledgeBaseIDsByOrganizations(ctx, orgIDs, userID)
	if err != nil {
		logger.Warnf(ctx, "buildResourceCountsByOrg ListSharedKnowledgeBaseIDsByOrganizations: %v", err)
		return nil
	}
	agentListByOrg, err := h.agentShareService.ListSharedAgentsInOrganizations(ctx, orgIDs, userID, tenantID)
	if err != nil {
		logger.Warnf(ctx, "buildResourceCountsByOrg ListSharedAgentsInOrganizations: %v", err)
		return nil
	}
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
// @Failure      403      {object}  errors.AppError
// @Security     Bearer
// @Router       /organizations/{id} [put]
func (h *OrganizationHandler) UpdateOrganization(c *gin.Context) {
	ctx := c.Request.Context()

	orgID := c.Param("id")
	userID := c.GetString(types.UserIDContextKey.String())

	var req types.UpdateOrganizationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(apperrors.NewValidationError("Invalid request parameters").WithDetails(err.Error()))
		return
	}

	org, err := h.orgService.UpdateOrganization(ctx, orgID, userID, &req)
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
// @Failure      403  {object}  errors.AppError
// @Security     Bearer
// @Router       /organizations/{id} [delete]
func (h *OrganizationHandler) DeleteOrganization(c *gin.Context) {
	ctx := c.Request.Context()

	orgID := c.Param("id")
	userID := c.GetString(types.UserIDContextKey.String())

	if err := h.orgService.DeleteOrganization(ctx, orgID, userID); err != nil {
		logger.Errorf(ctx, "Failed to delete organization: %v", err)
		c.Error(apperrors.NewForbiddenError("Permission denied or organization not found"))
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Organization deleted successfully",
	})
}

// ListMembers lists all members of an organization
// @Summary      获取组织成员列表
// @Description  获取组织的所有成员
// @Tags         组织管理
// @Produce      json
// @Param        id  path  string  true  "组织ID"
// @Success      200  {object}  types.ListMembersResponse
// @Security     Bearer
// @Router       /organizations/{id}/members [get]
func (h *OrganizationHandler) ListMembers(c *gin.Context) {
	ctx := c.Request.Context()

	orgID := c.Param("id")

	members, err := h.orgService.ListMembers(ctx, orgID)
	if err != nil {
		logger.Errorf(ctx, "Failed to list members: %v", err)
		c.Error(apperrors.NewInternalServerError("Failed to list members").WithDetails(err.Error()))
		return
	}

	response := make([]types.OrganizationMemberResponse, 0, len(members))
	for _, m := range members {
		resp := types.OrganizationMemberResponse{
			ID:       m.ID,
			UserID:   m.UserID,
			Role:     string(m.Role),
			TenantID: m.TenantID,
			JoinedAt: m.CreatedAt,
		}
		if m.User != nil {
			resp.Username = m.User.Username
			resp.Email = m.User.Email
			resp.Avatar = m.User.Avatar
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

// UpdateMemberRole updates a member's role
// @Summary      更新成员角色
// @Description  更新组织成员的角色（需要管理员权限）
// @Tags         组织管理
// @Accept       json
// @Produce      json
// @Param        id       path      string                       true  "组织ID"
// @Param        user_id  path      string                       true  "用户ID"
// @Param        request  body      types.UpdateMemberRoleRequest  true  "角色信息"
// @Success      200      {object}  map[string]interface{}
// @Failure      403      {object}  errors.AppError
// @Security     Bearer
// @Router       /organizations/{id}/members/{user_id} [put]
func (h *OrganizationHandler) UpdateMemberRole(c *gin.Context) {
	ctx := c.Request.Context()

	orgID := c.Param("id")
	memberUserID := c.Param("user_id")
	operatorUserID := c.GetString(types.UserIDContextKey.String())

	var req types.UpdateMemberRoleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(apperrors.NewValidationError("Invalid request parameters").WithDetails(err.Error()))
		return
	}

	if err := h.orgService.UpdateMemberRole(ctx, orgID, memberUserID, req.Role, operatorUserID); err != nil {
		logger.Errorf(ctx, "Failed to update member role: %v", err)
		c.Error(apperrors.NewForbiddenError("Permission denied or invalid operation"))
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Member role updated successfully",
	})
}

// RemoveMember removes a member from an organization
// @Summary      移除成员
// @Description  从组织中移除成员（需要管理员权限）
// @Tags         组织管理
// @Param        id       path  string  true  "组织ID"
// @Param        user_id  path  string  true  "用户ID"
// @Success      200      {object}  map[string]interface{}
// @Failure      403      {object}  errors.AppError
// @Security     Bearer
// @Router       /organizations/{id}/members/{user_id} [delete]
func (h *OrganizationHandler) RemoveMember(c *gin.Context) {
	ctx := c.Request.Context()

	orgID := c.Param("id")
	memberUserID := c.Param("user_id")
	operatorUserID := c.GetString(types.UserIDContextKey.String())

	if err := h.orgService.RemoveMember(ctx, orgID, memberUserID, operatorUserID); err != nil {
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
// @Failure      403  {object}  errors.AppError
// @Security     Bearer
// @Router       /organizations/{id}/invite-code [post]
func (h *OrganizationHandler) GenerateInviteCode(c *gin.Context) {
	ctx := c.Request.Context()

	orgID := c.Param("id")
	userID := c.GetString(types.UserIDContextKey.String())

	code, err := h.orgService.GenerateInviteCode(ctx, orgID, userID)
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
// @Failure      404   {object}  errors.AppError
// @Security     Bearer
// @Router       /organizations/preview/{code} [get]
func (h *OrganizationHandler) PreviewByInviteCode(c *gin.Context) {
	ctx := c.Request.Context()

	inviteCode := c.Param("code")
	userID := c.GetString(types.UserIDContextKey.String())

	// Get organization by invite code
	org, err := h.orgService.GetOrganizationByInviteCode(ctx, inviteCode)
	if err != nil {
		c.Error(apperrors.NewNotFoundError("Invalid invite code"))
		return
	}

	// Get member count
	members, _ := h.orgService.ListMembers(ctx, org.ID)
	memberCount := len(members)

	// Get shared knowledge bases count
	shares, _ := h.shareService.ListSharesByOrganization(ctx, org.ID)
	shareCount := len(shares)
	// Get shared agents count
	agentShares, _ := h.agentShareService.ListSharesByOrganization(ctx, org.ID)
	agentShareCount := len(agentShares)

	// Check if user is already a member
	_, memberErr := h.orgService.GetMember(ctx, org.ID, userID)
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
// @Failure      404      {object}  errors.AppError
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
// @Failure      400      {object}  errors.AppError
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

	// Check if user is already a member
	_, memberErr := h.orgService.GetMember(ctx, org.ID, userID)
	if memberErr == nil {
		c.Error(apperrors.NewValidationError("You are already a member of this organization"))
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
	userID := c.GetString(types.UserIDContextKey.String())
	query := c.Query("q")
	limit := 20
	if l := c.Query("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 && n <= 100 {
			limit = n
		}
	}
	resp, err := h.orgService.SearchSearchableOrganizations(ctx, userID, query, limit)
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
// @Failure      403      {object}  errors.AppError
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
// @Failure      400      {object}  errors.AppError
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
// @Failure      403  {object}  errors.AppError
// @Security     Bearer
// @Router       /organizations/{id}/leave [post]
func (h *OrganizationHandler) LeaveOrganization(c *gin.Context) {
	ctx := c.Request.Context()

	orgID := c.Param("id")
	userID := c.GetString(types.UserIDContextKey.String())

	// Check if user is the owner
	org, err := h.orgService.GetOrganization(ctx, orgID)
	if err != nil {
		c.Error(apperrors.NewNotFoundError("Organization not found"))
		return
	}

	if org.OwnerID == userID {
		c.Error(apperrors.NewForbiddenError("Organization owner cannot leave. Please transfer ownership or delete the organization."))
		return
	}

	// Remove the user from the organization
	if err := h.orgService.RemoveMember(ctx, orgID, userID, userID); err != nil {
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
// @Failure      403  {object}  errors.AppError
// @Security     Bearer
// @Router       /organizations/{id}/join-requests [get]
func (h *OrganizationHandler) ListJoinRequests(c *gin.Context) {
	ctx := c.Request.Context()

	orgID := c.Param("id")
	userID := c.GetString(types.UserIDContextKey.String())

	// Check admin
	isAdmin, err := h.orgService.IsOrgAdmin(ctx, orgID, userID)
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
// @Failure      403  {object}  errors.AppError
// @Security     Bearer
// @Router       /organizations/{id}/join-requests/{request_id}/review [put]
func (h *OrganizationHandler) ReviewJoinRequest(c *gin.Context) {
	ctx := c.Request.Context()

	orgID := c.Param("id")
	requestID := c.Param("request_id")
	userID := c.GetString(types.UserIDContextKey.String())

	// Check admin
	isAdmin, err := h.orgService.IsOrgAdmin(ctx, orgID, userID)
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

	if err := h.orgService.ReviewJoinRequest(ctx, orgID, requestID, req.Approved, userID, req.Message, assignRole); err != nil {
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
// @Failure      403      {object}  errors.AppError
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
// @Failure      403       {object}  errors.AppError
// @Security     Bearer
// @Router       /knowledge-bases/{id}/shares/{share_id} [put]
func (h *OrganizationHandler) UpdateSharePermission(c *gin.Context) {
	ctx := c.Request.Context()

	shareID := c.Param("share_id")
	userID := c.GetString(types.UserIDContextKey.String())

	var req types.UpdateSharePermissionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(apperrors.NewValidationError("Invalid request parameters").WithDetails(err.Error()))
		return
	}

	if err := h.shareService.UpdateSharePermission(ctx, shareID, req.Permission, userID); err != nil {
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
// @Failure      403       {object}  errors.AppError
// @Security     Bearer
// @Router       /knowledge-bases/{id}/shares/{share_id} [delete]
func (h *OrganizationHandler) RemoveShare(c *gin.Context) {
	ctx := c.Request.Context()

	shareID := c.Param("share_id")
	userID := c.GetString(types.UserIDContextKey.String())

	if err := h.shareService.RemoveShare(ctx, shareID, userID); err != nil {
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
	userID := c.GetString(types.UserIDContextKey.String())

	// Check if user is a member and get their role for effective-permission calculation
	member, err := h.orgService.GetMember(ctx, orgID, userID)
	if err != nil {
		c.Error(apperrors.NewForbiddenError("You are not a member of this organization"))
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

	userID := c.GetString(types.UserIDContextKey.String())
	tenantID := ctx.Value(types.TenantIDContextKey).(uint64)

	sharedKBs, err := h.shareService.ListSharedKnowledgeBases(ctx, userID, tenantID)
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
			c.Error(apperrors.NewValidationError("Agent is not fully configured. Please set the chat model and, if using knowledge bases, the rerank model in agent settings."))
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

// RemoveAgentShare removes an agent share
func (h *OrganizationHandler) RemoveAgentShare(c *gin.Context) {
	ctx := c.Request.Context()
	shareID := c.Param("share_id")
	userID := c.GetString(types.UserIDContextKey.String())
	if err := h.agentShareService.RemoveShare(ctx, shareID, userID); err != nil {
		logger.Errorf(ctx, "Failed to remove agent share: %v", err)
		c.Error(apperrors.NewForbiddenError("Permission denied"))
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "Share removed successfully"})
}

// ListOrgAgentShares lists all agents shared to an organization
func (h *OrganizationHandler) ListOrgAgentShares(c *gin.Context) {
	ctx := c.Request.Context()
	orgID := c.Param("id")
	userID := c.GetString(types.UserIDContextKey.String())
	member, err := h.orgService.GetMember(ctx, orgID, userID)
	if err != nil {
		c.Error(apperrors.NewForbiddenError("You are not a member of this organization"))
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

// ListSharedAgents lists agents shared to the current user
func (h *OrganizationHandler) ListSharedAgents(c *gin.Context) {
	ctx := c.Request.Context()
	userID := c.GetString(types.UserIDContextKey.String())
	tenantID := c.GetUint64(types.TenantIDContextKey.String())
	list, err := h.agentShareService.ListSharedAgents(ctx, userID, tenantID)
	if err != nil {
		logger.Errorf(ctx, "Failed to list shared agents: %v", err)
		c.Error(apperrors.NewInternalServerError("Failed to list shared agents"))
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": list, "total": len(list)})
}

// listSpaceKnowledgeBasesInOrganization returns merged list of direct shared KBs and agent-carried KBs in the org (for list and count).
func (h *OrganizationHandler) listSpaceKnowledgeBasesInOrganization(ctx context.Context, orgID string, userID string, tenantID uint64) ([]*types.OrganizationSharedKnowledgeBaseItem, error) {
	directList, err := h.shareService.ListSharedKnowledgeBasesInOrganization(ctx, orgID, userID, tenantID)
	if err != nil {
		return nil, err
	}

	directKbIDs := make(map[string]bool)
	for _, item := range directList {
		if item.KnowledgeBase != nil && item.KnowledgeBase.ID != "" {
			directKbIDs[item.KnowledgeBase.ID] = true
		}
	}

	agentList, err := h.agentShareService.ListSharedAgentsInOrganization(ctx, orgID, userID, tenantID)
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
	userID := c.GetString(types.UserIDContextKey.String())
	tenantID := c.GetUint64(types.TenantIDContextKey.String())

	list, err := h.listSpaceKnowledgeBasesInOrganization(ctx, orgID, userID, tenantID)
	if err != nil {
		if errors.Is(err, service.ErrUserNotInOrg) {
			c.Error(apperrors.NewForbiddenError("You are not a member of this organization"))
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
	userID := c.GetString(types.UserIDContextKey.String())
	tenantID := c.GetUint64(types.TenantIDContextKey.String())

	list, err := h.agentShareService.ListSharedAgentsInOrganization(ctx, orgID, userID, tenantID)
	if err != nil {
		if errors.Is(err, service.ErrUserNotInOrg) {
			c.Error(apperrors.NewForbiddenError("You are not a member of this organization"))
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
		share, err := h.agentShareService.GetShareByAgentIDForUser(ctx, uid, req.AgentID, tid)
		if err != nil || share == nil {
			c.Error(apperrors.NewForbiddenError("No access to this agent"))
			return
		}
		sourceTenantID = share.SourceTenantID
	}
	if err := h.agentShareService.SetSharedAgentDisabledByMe(ctx, tid, req.AgentID, sourceTenantID, req.Disabled); err != nil {
		logger.Errorf(ctx, "SetSharedAgentDisabledByMe failed: %v", err)
		c.Error(apperrors.NewInternalServerError("Failed to update preference"))
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// toOrgResponse converts an organization to response format
func (h *OrganizationHandler) toOrgResponse(ctx context.Context, org *types.Organization, currentUserID string) types.OrganizationResponse {
	resp := types.OrganizationResponse{
		ID:                     org.ID,
		Name:                   org.Name,
		Description:            org.Description,
		Avatar:                 org.Avatar,
		OwnerID:                org.OwnerID,
		IsOwner:                org.OwnerID == currentUserID,
		RequireApproval:        org.RequireApproval,
		Searchable:             org.Searchable,
		MemberLimit:            org.MemberLimit,
		InviteCodeValidityDays: org.InviteCodeValidityDays,
		CreatedAt:              org.CreatedAt,
		UpdatedAt:              org.UpdatedAt,
	}

	// Get member count
	if members, err := h.orgService.ListMembers(ctx, org.ID); err == nil {
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

	// Get current user's role in this organization
	isAdmin := false
	if role, err := h.orgService.GetUserRoleInOrg(ctx, org.ID, currentUserID); err == nil {
		resp.MyRole = string(role)
		isAdmin = (role == types.OrgRoleAdmin)
	}
	if isAdmin || org.OwnerID == currentUserID {
		resp.InviteCode = org.InviteCode
		resp.InviteCodeExpiresAt = org.InviteCodeExpiresAt
		if n, err := h.orgService.CountPendingJoinRequests(ctx, org.ID); err == nil {
			resp.PendingJoinRequestCount = int(n)
		}
	}

	// Check if current user has pending upgrade request
	if _, err := h.orgService.GetPendingUpgradeRequest(ctx, org.ID, currentUserID); err == nil {
		resp.HasPendingUpgrade = true
	}

	return resp
}

// SearchUsersForInvite searches users for inviting to organization
// @Summary      搜索可邀请的用户
// @Description  搜索用户（排除已有成员）用于邀请加入组织
// @Tags         组织管理
// @Produce      json
// @Param        id     path   string  true   "组织ID"
// @Param        q      query  string  true   "搜索关键词（用户名或邮箱）"
// @Param        limit  query  int     false  "返回数量限制" default(10)
// @Success      200    {object}  map[string]interface{}
// @Failure      403    {object}  errors.AppError
// @Security     Bearer
// @Router       /organizations/{id}/search-users [get]
func (h *OrganizationHandler) SearchUsersForInvite(c *gin.Context) {
	ctx := c.Request.Context()

	orgID := c.Param("id")
	query := c.Query("q")
	userID := c.GetString(types.UserIDContextKey.String())

	// Check admin permission
	isAdmin, err := h.orgService.IsOrgAdmin(ctx, orgID, userID)
	if err != nil || !isAdmin {
		c.Error(apperrors.NewForbiddenError("Only organization admins can invite members"))
		return
	}

	if query == "" {
		c.JSON(http.StatusOK, gin.H{
			"success": true,
			"data":    []interface{}{},
		})
		return
	}

	// Get limit from query
	limit := 10
	if l := c.Query("limit"); l != "" {
		if _, err := c.GetQuery("limit"); err {
			limit = 10
		}
	}

	// Search users
	users, err := h.userService.SearchUsers(ctx, query, limit+20) // fetch more to filter out existing members
	if err != nil {
		logger.Errorf(ctx, "Failed to search users: %v", err)
		c.Error(apperrors.NewInternalServerError("Failed to search users"))
		return
	}

	// Get existing members
	existingMembers, _ := h.orgService.ListMembers(ctx, orgID)
	existingMemberIDs := make(map[string]bool)
	for _, m := range existingMembers {
		existingMemberIDs[m.UserID] = true
	}

	// Filter out existing members and build response
	var result []gin.H
	for _, u := range users {
		if existingMemberIDs[u.ID] {
			continue
		}
		result = append(result, gin.H{
			"id":       u.ID,
			"username": u.Username,
			"email":    u.Email,
			"avatar":   u.Avatar,
		})
		if len(result) >= limit {
			break
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    result,
	})
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
// @Failure      400      {object}  errors.AppError
// @Failure      403      {object}  errors.AppError
// @Security     Bearer
// @Router       /organizations/{id}/invite [post]
func (h *OrganizationHandler) InviteMember(c *gin.Context) {
	ctx := c.Request.Context()

	orgID := c.Param("id")
	userID := c.GetString(types.UserIDContextKey.String())

	// Check admin permission
	isAdmin, err := h.orgService.IsOrgAdmin(ctx, orgID, userID)
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

	// Check if user exists
	invitedUser, err := h.userService.GetUserByID(ctx, req.UserID)
	if err != nil {
		c.Error(apperrors.NewNotFoundError("User not found"))
		return
	}

	// Check if already a member
	_, memberErr := h.orgService.GetMember(ctx, orgID, req.UserID)
	if memberErr == nil {
		c.Error(apperrors.NewValidationError("User is already a member of this organization"))
		return
	}

	// Add member
	if err := h.orgService.AddMember(ctx, orgID, req.UserID, invitedUser.TenantID, req.Role); err != nil {
		logger.Errorf(ctx, "Failed to add member: %v", err)
		if errors.Is(err, service.ErrOrgMemberLimitReached) {
			c.Error(apperrors.NewValidationError("该空间成员已满，无法添加新成员"))
			return
		}
		c.Error(apperrors.NewInternalServerError("Failed to add member"))
		return
	}

	logger.Infof(ctx, "User %s invited user %s to organization %s with role %s",
		secutils.SanitizeForLog(userID),
		secutils.SanitizeForLog(req.UserID),
		orgID,
		req.Role)

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Member added successfully",
	})
}
