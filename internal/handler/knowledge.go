package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"

	goerrors "errors"

	"github.com/Tencent/WeKnora/internal/agent/tools"
	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/application/service"
	"github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/tracing/langfuse"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/Tencent/WeKnora/internal/utils"
	secutils "github.com/Tencent/WeKnora/internal/utils"
	"github.com/gin-gonic/gin"
	"github.com/hibiken/asynq"
)

// KnowledgeHandler processes HTTP requests related to knowledge resources
type KnowledgeHandler struct {
	kgService         interfaces.KnowledgeService
	kbService         interfaces.KnowledgeBaseService
	kbShareService    interfaces.KBShareService
	agentShareService interfaces.AgentShareService
	asynqClient       interfaces.TaskEnqueuer
}

// NewKnowledgeHandler creates a new knowledge handler instance
func NewKnowledgeHandler(
	kgService interfaces.KnowledgeService,
	kbService interfaces.KnowledgeBaseService,
	kbShareService interfaces.KBShareService,
	agentShareService interfaces.AgentShareService,
	asynqClient interfaces.TaskEnqueuer,
) *KnowledgeHandler {
	return &KnowledgeHandler{
		kgService:         kgService,
		kbService:         kbService,
		kbShareService:    kbShareService,
		agentShareService: agentShareService,
		asynqClient:       asynqClient,
	}
}

// validateKnowledgeBaseAccess validates access permissions to a knowledge base
// using the ":id" URL path parameter. It delegates to validateKnowledgeBaseAccessWithKBID.
func (h *KnowledgeHandler) validateKnowledgeBaseAccess(c *gin.Context) (*types.KnowledgeBase, string, uint64, types.OrgMemberRole, error) {
	kbID := secutils.SanitizeForLog(c.Param("id"))
	return h.validateKnowledgeBaseAccessWithKBID(c, kbID)
}

// validateKnowledgeBaseAccessWithKBID validates access to the given knowledge base ID (e.g. from query or body).
// Returns the knowledge base, kbID, effective tenant ID, permission, and error.
func (h *KnowledgeHandler) validateKnowledgeBaseAccessWithKBID(c *gin.Context, kbID string) (*types.KnowledgeBase, string, uint64, types.OrgMemberRole, error) {
	ctx := c.Request.Context()
	tenantID := c.GetUint64(types.TenantIDContextKey.String())
	if tenantID == 0 {
		logger.Error(ctx, "Failed to get tenant ID")
		return nil, "", 0, "", errors.NewUnauthorizedError("Unauthorized")
	}
	userID, userExists := c.Get(types.UserIDContextKey.String())
	kbID = secutils.SanitizeForLog(kbID)
	if kbID == "" {
		return nil, "", 0, "", errors.NewBadRequestError("Knowledge base ID cannot be empty")
	}
	kb, err := h.kbService.GetKnowledgeBaseByID(ctx, kbID)
	if err != nil {
		logger.ErrorWithFields(ctx, err, nil)
		return nil, kbID, 0, "", errors.NewInternalServerError(err.Error())
	}
	if kb.TenantID == tenantID {
		return kb, kbID, tenantID, types.OrgRoleAdmin, nil
	}
	if userExists && h.kbShareService != nil {
		permission, isShared, permErr := h.kbShareService.CheckUserKBPermission(ctx, kbID, userID.(string))
		if permErr == nil && isShared {
			sourceTenantID, srcErr := h.kbShareService.GetKBSourceTenant(ctx, kbID)
			if srcErr == nil {
				logger.Infof(ctx, "User %s accessing shared KB %s with permission %s, source tenant: %d",
					userID.(string), kbID, permission, sourceTenantID)
				return kb, kbID, sourceTenantID, permission, nil
			}
		}
	}
	if userExists && h.agentShareService != nil {
		can, err := h.agentShareService.UserCanAccessKBViaSomeSharedAgent(ctx, userID.(string), tenantID, kb)
		if err == nil && can {
			logger.Infof(ctx, "User %s accessing KB %s via some shared agent", userID.(string), kbID)
			return kb, kbID, kb.TenantID, types.OrgRoleViewer, nil
		}
	}
	logger.Warnf(ctx, "Permission denied to access KB %s, tenant ID: %d, KB tenant: %d", kbID, tenantID, kb.TenantID)
	return nil, kbID, 0, "", errors.NewForbiddenError("Permission denied to access this knowledge base")
}

// resolveKnowledgeAndValidateKBAccess resolves knowledge by ID and validates KB access (owner or shared with required permission).
// Returns the knowledge, context with effectiveTenantID set for downstream service calls, and error.
func (h *KnowledgeHandler) resolveKnowledgeAndValidateKBAccess(c *gin.Context, knowledgeID string, requiredPermission types.OrgMemberRole) (*types.Knowledge, context.Context, error) {
	ctx := c.Request.Context()
	tenantID := c.GetUint64(types.TenantIDContextKey.String())
	if tenantID == 0 {
		return nil, ctx, errors.NewUnauthorizedError("Unauthorized")
	}
	userID, userExists := c.Get(types.UserIDContextKey.String())

	knowledge, err := h.kgService.GetKnowledgeByIDOnly(ctx, knowledgeID)
	if err != nil {
		return nil, ctx, errors.NewNotFoundError("Knowledge not found")
	}

	// Owner: knowledge belongs to caller's tenant
	if knowledge.TenantID == tenantID {
		return knowledge, context.WithValue(ctx, types.TenantIDContextKey, tenantID), nil
	}

	// Shared KB: check organization permission
	if userExists && h.kbShareService != nil {
		permission, isShared, permErr := h.kbShareService.CheckUserKBPermission(ctx, knowledge.KnowledgeBaseID, userID.(string))
		if permErr == nil && isShared && permission.HasPermission(requiredPermission) {
			effectiveTenantID := knowledge.TenantID
			return knowledge, context.WithValue(ctx, types.TenantIDContextKey, effectiveTenantID), nil
		}
	}
	// Shared agent: request passes agent_id, or user has any shared agent that can access this KB
	if userExists && h.agentShareService != nil && requiredPermission == types.OrgRoleViewer {
		agentID := c.Query("agent_id")
		if agentID != "" {
			agent, err := h.agentShareService.GetSharedAgentForUser(ctx, userID.(string), tenantID, agentID)
			if err == nil && agent != nil {
				if knowledge.TenantID != agent.TenantID {
					return nil, ctx, errors.NewForbiddenError("Permission denied to access this knowledge")
				}
				mode := agent.Config.KBSelectionMode
				if mode == "none" {
					return nil, ctx, errors.NewForbiddenError("Permission denied to access this knowledge")
				}
				if mode == "all" {
					return knowledge, context.WithValue(ctx, types.TenantIDContextKey, knowledge.TenantID), nil
				}
				if mode == "selected" {
					for _, kbID := range agent.Config.KnowledgeBases {
						if kbID == knowledge.KnowledgeBaseID {
							return knowledge, context.WithValue(ctx, types.TenantIDContextKey, knowledge.TenantID), nil
						}
					}
					return nil, ctx, errors.NewForbiddenError("Permission denied to access this knowledge")
				}
			}
		} else {
			kbRef := &types.KnowledgeBase{ID: knowledge.KnowledgeBaseID, TenantID: knowledge.TenantID}
			can, err := h.agentShareService.UserCanAccessKBViaSomeSharedAgent(ctx, userID.(string), tenantID, kbRef)
			if err == nil && can {
				return knowledge, context.WithValue(ctx, types.TenantIDContextKey, knowledge.TenantID), nil
			}
		}
	}
	return nil, ctx, errors.NewForbiddenError("Permission denied to access this knowledge")
}

// handleDuplicateKnowledgeError handles cases where duplicate knowledge is detected
// Returns true if the error was a duplicate error and was handled, false otherwise
func (h *KnowledgeHandler) handleDuplicateKnowledgeError(c *gin.Context,
	err error, knowledge *types.Knowledge, duplicateType string,
) bool {
	if dupErr, ok := err.(*types.DuplicateKnowledgeError); ok {
		ctx := c.Request.Context()
		logger.Warnf(ctx, "Detected duplicate %s: %s", duplicateType, secutils.SanitizeForLog(dupErr.Error()))
		c.JSON(http.StatusConflict, gin.H{
			"success": false,
			"message": dupErr.Error(),
			"data":    knowledge, // knowledge contains the existing document
			"code":    fmt.Sprintf("duplicate_%s", duplicateType),
		})
		return true
	}
	return false
}

// enqueueKnowledgeListDelete enqueues an async batch-delete task for the
// given knowledge IDs and returns the asynq task ID.
func (h *KnowledgeHandler) enqueueKnowledgeListDelete(
	ctx context.Context, tenantID uint64, ids []string,
) (string, error) {
	payload := types.KnowledgeListDeletePayload{
		TenantID:     tenantID,
		KnowledgeIDs: ids,
	}
	langfuse.InjectTracing(ctx, &payload)
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal payload: %w", err)
	}
	task := asynq.NewTask(types.TypeKnowledgeListDelete, payloadBytes,
		asynq.Queue("low"), asynq.MaxRetry(3))
	info, err := h.asynqClient.Enqueue(task)
	if err != nil {
		return "", fmt.Errorf("enqueue task: %w", err)
	}
	return info.ID, nil
}

// CreateKnowledgeFromFile godoc
// @Summary      从文件创建知识
// @Description  上传文件并创建知识条目
// @Tags         知识管理
// @Accept       multipart/form-data
// @Produce      json
// @Param        id                path      string  true   "知识库ID"
// @Param        file              formData  file    true   "上传的文件"
// @Param        fileName          formData  string  false  "自定义文件名"
// @Param        metadata          formData  string  false  "元数据JSON"
// @Param        enable_multimodel formData  bool    false  "启用多模态处理"
// @Success      200               {object}  map[string]interface{}  "创建的知识"
// @Failure      400               {object}  errors.AppError         "请求参数错误"
// @Failure      409               {object}  map[string]interface{}  "文件重复"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /knowledge-bases/{id}/knowledge/file [post]
func (h *KnowledgeHandler) CreateKnowledgeFromFile(c *gin.Context) {
	ctx := c.Request.Context()
	logger.Info(ctx, "Start creating knowledge from file")

	// Validate access to the knowledge base (only owner or admin/editor can create)
	_, kbID, effectiveTenantID, permission, err := h.validateKnowledgeBaseAccess(c)
	if err != nil {
		c.Error(err)
		return
	}
	ctx = context.WithValue(ctx, types.TenantIDContextKey, effectiveTenantID)

	// Check write permission
	if permission != types.OrgRoleAdmin && permission != types.OrgRoleEditor {
		c.Error(errors.NewForbiddenError("No permission to create knowledge"))
		return
	}

	// Get the uploaded file
	file, err := c.FormFile("file")
	if err != nil {
		logger.Error(ctx, "File upload failed", err)
		c.Error(errors.NewBadRequestError("File upload failed").WithDetails(err.Error()))
		return
	}

	// Validate file size (configurable via MAX_FILE_SIZE_MB)
	maxSize := secutils.GetMaxFileSize()
	if file.Size > maxSize {
		logger.Error(ctx, "File size too large")
		c.Error(errors.NewBadRequestError(fmt.Sprintf("文件大小不能超过%dMB", secutils.GetMaxFileSizeMB())))
		return
	}

	// Get custom filename if provided (for folder uploads with path)
	customFileName := c.PostForm("fileName")
	customFileName = secutils.SanitizeForLog(customFileName)
	displayFileName := file.Filename
	displayFileName = secutils.SanitizeForLog(displayFileName)
	if customFileName != "" {
		displayFileName = customFileName
		logger.Infof(ctx, "Using custom filename: %s (original: %s)", customFileName, displayFileName)
	}

	logger.Infof(ctx, "File upload successful, filename: %s, size: %.2f KB", displayFileName, float64(file.Size)/1024)
	logger.Infof(ctx, "Creating knowledge, knowledge base ID: %s, filename: %s", kbID, displayFileName)

	// Parse metadata if provided
	var metadata map[string]string
	metadataStr := c.PostForm("metadata")
	if metadataStr != "" {
		if err := json.Unmarshal([]byte(metadataStr), &metadata); err != nil {
			logger.Error(ctx, "Failed to parse metadata", err)
			c.Error(errors.NewBadRequestError("Invalid metadata format").WithDetails(err.Error()))
			return
		}
		logger.Infof(ctx, "Received file metadata: %s", secutils.SanitizeForLog(fmt.Sprintf("%v", metadata)))
	}

	enableMultimodelForm := c.PostForm("enable_multimodel")
	var enableMultimodel *bool
	if enableMultimodelForm != "" {
		parseBool, err := strconv.ParseBool(enableMultimodelForm)
		if err != nil {
			logger.Error(ctx, "Failed to parse enable_multimodel", err)
			c.Error(errors.NewBadRequestError("Invalid enable_multimodel format").WithDetails(err.Error()))
			return
		}
		enableMultimodel = &parseBool
	}

	// 获取分类ID（如果提供），用于知识分类管理
	tagID := c.PostForm("tag_id")
	// 过滤特殊值，空字符串或 "__untagged__" 表示未分类
	if tagID == "__untagged__" || tagID == "" {
		tagID = ""
	}

	channel := c.PostForm("channel")

	// Create knowledge entry from the file
	knowledge, err := h.kgService.CreateKnowledgeFromFile(ctx, kbID, file, metadata, enableMultimodel, customFileName, tagID, channel)
	// Check for duplicate knowledge error
	if err != nil {
		if h.handleDuplicateKnowledgeError(c, err, knowledge, "file") {
			return
		}
		if appErr, ok := errors.IsAppError(err); ok {
			c.Error(appErr)
			return
		}
		logger.ErrorWithFields(ctx, err, nil)
		c.Error(errors.NewInternalServerError(err.Error()))
		return
	}

	logger.Infof(
		ctx,
		"Knowledge created successfully, ID: %s, title: %s",
		secutils.SanitizeForLog(knowledge.ID),
		secutils.SanitizeForLog(knowledge.Title),
	)
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    knowledge,
	})
}

// CreateKnowledgeFromURL godoc
// @Summary      从URL创建知识
// @Description  从指定URL抓取内容并创建知识条目。当提供 file_name/file_type 或 URL 路径含已知文件扩展名时，自动切换为文件下载模式
// @Tags         知识管理
// @Accept       json
// @Produce      json
// @Param        id       path      string  true  "知识库ID"
// @Param        request  body      object{url=string,file_name=string,file_type=string,enable_multimodel=bool,title=string,tag_id=string}  true  "URL请求"
// @Success      201      {object}  map[string]interface{}  "创建的知识"
// @Failure      400      {object}  errors.AppError         "请求参数错误"
// @Failure      409      {object}  map[string]interface{}  "URL重复"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /knowledge-bases/{id}/knowledge/url [post]
func (h *KnowledgeHandler) CreateKnowledgeFromURL(c *gin.Context) {
	ctx := c.Request.Context()
	logger.Info(ctx, "Start creating knowledge from URL")

	// Validate access to the knowledge base (only owner or admin/editor can create)
	_, kbID, effectiveTenantID, permission, err := h.validateKnowledgeBaseAccess(c)
	if err != nil {
		c.Error(err)
		return
	}
	ctx = context.WithValue(ctx, types.TenantIDContextKey, effectiveTenantID)

	// Check write permission
	if permission != types.OrgRoleAdmin && permission != types.OrgRoleEditor {
		c.Error(errors.NewForbiddenError("No permission to create knowledge"))
		return
	}

	// Parse URL from request body
	var req struct {
		URL              string `json:"url" binding:"required"`
		FileName         string `json:"file_name"`
		FileType         string `json:"file_type"`
		EnableMultimodel *bool  `json:"enable_multimodel"`
		Title            string `json:"title"`
		TagID            string `json:"tag_id"`
		Channel          string `json:"channel"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Error(ctx, "Failed to parse URL request", err)
		c.Error(errors.NewBadRequestError(err.Error()))
		return
	}

	logger.Infof(ctx, "Received URL request: %s, file_name: %s, file_type: %s",
		secutils.SanitizeForLog(req.URL),
		secutils.SanitizeForLog(req.FileName),
		secutils.SanitizeForLog(req.FileType),
	)

	// SSRF validation for user-supplied URL
	if err := secutils.ValidateURLForSSRF(req.URL); err != nil {
		logger.Warnf(ctx, "SSRF validation failed for knowledge URL: %v", err)
		c.Error(errors.NewBadRequestError(fmt.Sprintf("URL 未通过安全校验: %v", err)))
		return
	}

	logger.Infof(ctx,
		"Creating knowledge from URL, knowledge base ID: %s, URL: %s",
		secutils.SanitizeForLog(kbID),
		secutils.SanitizeForLog(req.URL),
	)

	// Create knowledge entry from the URL
	knowledge, err := h.kgService.CreateKnowledgeFromURL(ctx, kbID, req.URL, req.FileName, req.FileType, req.EnableMultimodel, req.Title, req.TagID, req.Channel)
	// Check for duplicate knowledge error
	if err != nil {
		if h.handleDuplicateKnowledgeError(c, err, knowledge, "url") {
			return
		}
		if appErr, ok := errors.IsAppError(err); ok {
			c.Error(appErr)
			return
		}
		logger.ErrorWithFields(ctx, err, nil)
		c.Error(errors.NewInternalServerError(err.Error()))
		return
	}

	logger.Infof(
		ctx,
		"Knowledge created successfully from URL, ID: %s, title: %s",
		secutils.SanitizeForLog(knowledge.ID),
		secutils.SanitizeForLog(knowledge.Title),
	)
	c.JSON(http.StatusCreated, gin.H{
		"success": true,
		"data":    knowledge,
	})
}

// CreateManualKnowledge godoc
// @Summary      手工创建知识
// @Description  手工录入Markdown格式的知识内容
// @Tags         知识管理
// @Accept       json
// @Produce      json
// @Param        id       path      string                       true  "知识库ID"
// @Param        request  body      types.ManualKnowledgePayload true  "手工知识内容"
// @Success      200      {object}  map[string]interface{}       "创建的知识"
// @Failure      400      {object}  errors.AppError              "请求参数错误"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /knowledge-bases/{id}/knowledge/manual [post]
func (h *KnowledgeHandler) CreateManualKnowledge(c *gin.Context) {
	ctx := c.Request.Context()
	logger.Info(ctx, "Start creating manual knowledge")

	// Validate access to the knowledge base (only owner or admin/editor can create)
	_, kbID, effectiveTenantID, permission, err := h.validateKnowledgeBaseAccess(c)
	if err != nil {
		c.Error(err)
		return
	}
	ctx = context.WithValue(ctx, types.TenantIDContextKey, effectiveTenantID)

	// Check write permission
	if permission != types.OrgRoleAdmin && permission != types.OrgRoleEditor {
		c.Error(errors.NewForbiddenError("No permission to create knowledge"))
		return
	}

	var req types.ManualKnowledgePayload
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Error(ctx, "Failed to parse manual knowledge request", err)
		c.Error(errors.NewBadRequestError(err.Error()))
		return
	}

	knowledge, err := h.kgService.CreateKnowledgeFromManual(ctx, kbID, &req, req.Channel)
	if err != nil {
		if appErr, ok := errors.IsAppError(err); ok {
			c.Error(appErr)
			return
		}
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"kb_id": kbID,
		})
		c.Error(errors.NewInternalServerError(err.Error()))
		return
	}

	logger.Infof(ctx, "Manual knowledge created successfully, knowledge ID: %s",
		secutils.SanitizeForLog(knowledge.ID))
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    knowledge,
	})
}

// GetKnowledge godoc
// @Summary      获取知识详情
// @Description  根据ID获取知识条目详情
// @Tags         知识管理
// @Accept       json
// @Produce      json
// @Param        id   path      string  true  "知识ID"
// @Success      200  {object}  map[string]interface{}  "知识详情"
// @Failure      400  {object}  errors.AppError         "请求参数错误"
// @Failure      404  {object}  errors.AppError         "知识不存在"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /knowledge/{id} [get]
func (h *KnowledgeHandler) GetKnowledge(c *gin.Context) {
	ctx := c.Request.Context()

	logger.Info(ctx, "Start retrieving knowledge")

	id := secutils.SanitizeForLog(c.Param("id"))
	if id == "" {
		logger.Error(ctx, "Knowledge ID is empty")
		c.Error(errors.NewBadRequestError("Knowledge ID cannot be empty"))
		return
	}

	// Resolve knowledge and validate KB access (at least viewer)
	knowledge, _, err := h.resolveKnowledgeAndValidateKBAccess(c, id, types.OrgRoleViewer)
	if err != nil {
		c.Error(err)
		return
	}

	logger.Infof(ctx, "Knowledge retrieved successfully, ID: %s, title: %s",
		secutils.SanitizeForLog(knowledge.ID), secutils.SanitizeForLog(knowledge.Title))
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    knowledge,
	})
}

// ListKnowledge godoc
// @Summary      获取知识列表
// @Description  获取知识库下的知识列表，支持分页和筛选
// @Tags         知识管理
// @Accept       json
// @Produce      json
// @Param        id         path      string  true   "知识库ID"
// @Param        page       query     int     false  "页码"
// @Param        page_size  query     int     false  "每页数量"
// @Param        tag_id     query     string  false  "标签ID筛选"
// @Param        keyword    query     string  false  "关键词搜索"
// @Param        file_type  query     string  false  "文件类型筛选"
// @Success      200        {object}  map[string]interface{}  "知识列表"
// @Failure      400        {object}  errors.AppError         "请求参数错误"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /knowledge-bases/{id}/knowledge [get]
func (h *KnowledgeHandler) ListKnowledge(c *gin.Context) {
	ctx := c.Request.Context()

	logger.Info(ctx, "Start retrieving knowledge list")

	// Validate access to the knowledge base (read access - any permission level)
	_, kbID, effectiveTenantID, _, err := h.validateKnowledgeBaseAccess(c)
	if err != nil {
		c.Error(err)
		return
	}

	// Update context with effective tenant ID for shared KB access
	ctx = context.WithValue(ctx, types.TenantIDContextKey, effectiveTenantID)

	// Parse pagination parameters from query string
	var pagination types.Pagination
	if err := c.ShouldBindQuery(&pagination); err != nil {
		logger.Error(ctx, "Failed to parse pagination parameters", err)
		c.Error(errors.NewBadRequestError(err.Error()))
		return
	}

	tagID := c.Query("tag_id")
	keyword := c.Query("keyword")
	fileType := c.Query("file_type")

	logger.Infof(
		ctx,
		"Retrieving knowledge list under knowledge base, knowledge base ID: %s, tag_id: %s, keyword: %s, file_type: %s, page: %d, page size: %d, effectiveTenantID: %d",
		secutils.SanitizeForLog(kbID),
		secutils.SanitizeForLog(tagID),
		secutils.SanitizeForLog(keyword),
		secutils.SanitizeForLog(fileType),
		pagination.Page,
		pagination.PageSize,
		effectiveTenantID,
	)

	// Retrieve paginated knowledge entries
	result, err := h.kgService.ListPagedKnowledgeByKnowledgeBaseID(ctx, kbID, &pagination, tagID, keyword, fileType)
	if err != nil {
		logger.ErrorWithFields(ctx, err, nil)
		c.Error(errors.NewInternalServerError(err.Error()))
		return
	}

	logger.Infof(
		ctx,
		"Knowledge list retrieved successfully, knowledge base ID: %s, total: %d",
		secutils.SanitizeForLog(kbID),
		result.Total,
	)
	c.JSON(http.StatusOK, gin.H{
		"success":   true,
		"data":      result.Data,
		"total":     result.Total,
		"page":      result.Page,
		"page_size": result.PageSize,
	})
}

// DeleteKnowledge godoc
// @Summary      删除知识
// @Description  根据ID删除知识条目
// @Tags         知识管理
// @Accept       json
// @Produce      json
// @Param        id   path      string  true  "知识ID"
// @Success      200  {object}  map[string]interface{}  "删除成功"
// @Failure      400  {object}  errors.AppError         "请求参数错误"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /knowledge/{id} [delete]
func (h *KnowledgeHandler) DeleteKnowledge(c *gin.Context) {
	ctx := c.Request.Context()

	logger.Info(ctx, "Start deleting knowledge")

	id := secutils.SanitizeForLog(c.Param("id"))
	if id == "" {
		logger.Error(ctx, "Knowledge ID is empty")
		c.Error(errors.NewBadRequestError("Knowledge ID cannot be empty"))
		return
	}

	_, effCtx, err := h.resolveKnowledgeAndValidateKBAccess(c, id, types.OrgRoleEditor)
	if err != nil {
		c.Error(err)
		return
	}
	logger.Infof(ctx, "Deleting knowledge, ID: %s", secutils.SanitizeForLog(id))
	err = h.kgService.DeleteKnowledge(effCtx, id)
	if err != nil {
		logger.ErrorWithFields(ctx, err, nil)
		c.Error(errors.NewInternalServerError(err.Error()))
		return
	}

	logger.Infof(ctx, "Knowledge deleted successfully, ID: %s", secutils.SanitizeForLog(id))
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Deleted successfully",
	})
}

// BatchDeleteKnowledgeRequest is the body schema for POST /knowledge/batch-delete.
type BatchDeleteKnowledgeRequest struct {
	KBID string   `json:"kb_id" binding:"required"`
	IDs  []string `json:"ids"  binding:"required"`
}

// BatchDeleteKnowledge godoc
// @Summary      批量删除知识
// @Description  按 ID 列表批量删除单个知识库下的多个知识条目
// @Tags         知识管理
// @Accept       json
// @Produce      json
// @Param        request  body      BatchDeleteKnowledgeRequest  true  "批量删除请求"
// @Success      200      {object}  map[string]interface{}       "删除成功"
// @Failure      400      {object}  errors.AppError              "请求参数错误"
// @Failure      403      {object}  errors.AppError              "权限不足"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /knowledge/batch-delete [post]
func (h *KnowledgeHandler) BatchDeleteKnowledge(c *gin.Context) {
	ctx := c.Request.Context()

	var req BatchDeleteKnowledgeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(errors.NewBadRequestError("Invalid request parameters: " + err.Error()))
		return
	}

	// Deduplicate and drop empty IDs.
	seen := make(map[string]struct{}, len(req.IDs))
	ids := make([]string, 0, len(req.IDs))
	for _, raw := range req.IDs {
		id := strings.TrimSpace(raw)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		c.Error(errors.NewBadRequestError("ids cannot be empty"))
		return
	}
	const maxBatch = 200
	if len(ids) > maxBatch {
		c.Error(errors.NewBadRequestError(fmt.Sprintf("too many ids (max %d per batch)", maxBatch)))
		return
	}

	// Validate KB access (editor or admin) using the kb_id from body.
	_, kbID, effectiveTenantID, permission, err := h.validateKnowledgeBaseAccessWithKBID(c, req.KBID)
	if err != nil {
		c.Error(err)
		return
	}
	if permission != types.OrgRoleAdmin && permission != types.OrgRoleEditor {
		c.Error(errors.NewForbiddenError("No permission to delete knowledge"))
		return
	}
	ctx = context.WithValue(ctx, types.TenantIDContextKey, effectiveTenantID)

	// Single batch fetch to validate that every id exists and belongs to the
	// requested KB. The service-layer DeleteKnowledgeList only enforces tenant
	// scope, not KB scope, so the handler must guard against cross-KB deletion.
	knowledgeList, err := h.kgService.GetKnowledgeBatch(ctx, effectiveTenantID, ids)
	if err != nil {
		logger.ErrorWithFields(ctx, err, nil)
		c.Error(errors.NewInternalServerError(err.Error()))
		return
	}
	if len(knowledgeList) != len(ids) {
		c.Error(errors.NewBadRequestError("One or more knowledge entries not found"))
		return
	}
	for _, k := range knowledgeList {
		if k.KnowledgeBaseID != kbID {
			c.Error(errors.NewBadRequestError(
				fmt.Sprintf("Knowledge %s does not belong to knowledge base %s",
					secutils.SanitizeForLog(k.ID), secutils.SanitizeForLog(kbID))))
			return
		}
	}

	taskID, err := h.enqueueKnowledgeListDelete(ctx, effectiveTenantID, ids)
	if err != nil {
		logger.Errorf(ctx, "Failed to enqueue batch knowledge delete task: %v", err)
		c.Error(errors.NewInternalServerError("Failed to enqueue batch delete task"))
		return
	}

	logger.Infof(ctx, "Batch knowledge delete task enqueued: %s, kb_id: %s, count: %d",
		taskID, secutils.SanitizeForLog(kbID), len(ids))

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Batch delete task submitted",
		"data": gin.H{
			"task_id":       taskID,
			"deleted_count": len(ids),
		},
	})
}

// ClearKnowledgeBaseContents godoc
// @Summary      清空知识库内容
// @Description  删除知识库下的所有知识条目（异步任务）。知识库本身保留，仅清空其中的内容
// @Tags         知识管理
// @Accept       json
// @Produce      json
// @Param        id   path      string  true  "知识库ID"
// @Success      200  {object}  map[string]interface{}  "清空任务已提交"
// @Failure      400  {object}  errors.AppError         "请求参数错误"
// @Failure      403  {object}  errors.AppError         "权限不足"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /knowledge-bases/{id}/knowledge [delete]
func (h *KnowledgeHandler) ClearKnowledgeBaseContents(c *gin.Context) {
	ctx := c.Request.Context()
	logger.Info(ctx, "Start clearing knowledge base contents")

	kb, kbID, effectiveTenantID, permission, err := h.validateKnowledgeBaseAccess(c)
	if err != nil {
		c.Error(err)
		return
	}

	// Only owner (admin with matching tenant) can clear knowledge base contents
	tenantID := c.GetUint64(types.TenantIDContextKey.String())
	if kb.TenantID != tenantID || permission != types.OrgRoleAdmin {
		c.Error(errors.NewForbiddenError("Only knowledge base owner can clear contents"))
		return
	}

	ctx = context.WithValue(ctx, types.TenantIDContextKey, effectiveTenantID)

	knowledgeList, err := h.kgService.ListKnowledgeByKnowledgeBaseID(ctx, kbID)
	if err != nil {
		logger.ErrorWithFields(ctx, err, nil)
		c.Error(errors.NewInternalServerError("Failed to list knowledge entries").WithDetails(err.Error()))
		return
	}

	if len(knowledgeList) == 0 {
		logger.Infof(ctx, "Knowledge base %s is already empty", secutils.SanitizeForLog(kbID))
		c.JSON(http.StatusOK, gin.H{
			"success": true,
			"message": "Knowledge base is already empty",
			"data":    gin.H{"deleted_count": 0},
		})
		return
	}

	knowledgeIDs := make([]string, 0, len(knowledgeList))
	for _, knowledge := range knowledgeList {
		knowledgeIDs = append(knowledgeIDs, knowledge.ID)
	}

	taskID, err := h.enqueueKnowledgeListDelete(ctx, effectiveTenantID, knowledgeIDs)
	if err != nil {
		logger.Errorf(ctx, "Failed to enqueue knowledge list delete task: %v", err)
		c.Error(errors.NewInternalServerError("Failed to enqueue cleanup task"))
		return
	}

	logger.Infof(ctx, "Knowledge base contents clear task enqueued: %s, kb_id: %s, count: %d",
		taskID, secutils.SanitizeForLog(kbID), len(knowledgeIDs))

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Knowledge base contents clear task submitted",
		"data":    gin.H{"deleted_count": len(knowledgeIDs)},
	})
}

// DownloadKnowledgeFile godoc
// @Summary      下载知识文件
// @Description  下载知识条目关联的原始文件
// @Tags         知识管理
// @Accept       json
// @Produce      application/octet-stream
// @Param        id   path      string  true  "知识ID"
// @Success      200  {file}    file    "文件内容"
// @Failure      400  {object}  errors.AppError  "请求参数错误"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /knowledge/{id}/download [get]
func (h *KnowledgeHandler) DownloadKnowledgeFile(c *gin.Context) {
	ctx := c.Request.Context()

	logger.Info(ctx, "Start downloading knowledge file")

	id := secutils.SanitizeForLog(c.Param("id"))
	if id == "" {
		logger.Error(ctx, "Knowledge ID is empty")
		c.Error(errors.NewBadRequestError("Knowledge ID cannot be empty"))
		return
	}

	_, effCtx, err := h.resolveKnowledgeAndValidateKBAccess(c, id, types.OrgRoleViewer)
	if err != nil {
		c.Error(err)
		return
	}
	logger.Infof(ctx, "Retrieving knowledge file, ID: %s", secutils.SanitizeForLog(id))

	file, filename, err := h.kgService.GetKnowledgeFile(effCtx, id)
	if err != nil {
		logger.ErrorWithFields(ctx, err, nil)
		c.Error(errors.NewInternalServerError("Failed to retrieve file").WithDetails(err.Error()))
		return
	}
	defer file.Close()

	logger.Infof(
		ctx,
		"Knowledge file retrieved successfully, ID: %s, filename: %s",
		secutils.SanitizeForLog(id),
		secutils.SanitizeForLog(filename),
	)

	// Set response headers for file download
	c.Header("Content-Description", "File Transfer")
	c.Header("Content-Transfer-Encoding", "binary")
	cd := mime.FormatMediaType("attachment", map[string]string{"filename": filename})
	c.Header("Content-Disposition", cd)
	c.Header("Content-Type", "application/octet-stream")
	c.Header("Expires", "0")
	c.Header("Cache-Control", "must-revalidate")
	c.Header("Pragma", "public")

	// Stream file content to response
	c.Stream(func(w io.Writer) bool {
		if _, err := io.Copy(w, file); err != nil {
			logger.Errorf(ctx, "Failed to send file: %v", err)
			return false
		}
		logger.Debug(ctx, "File sending completed")
		return false
	})
}

// mimeTypeByExt returns the MIME type for a given file extension.
func mimeTypeByExt(filename string) string {
	ext := strings.ToLower(filename)
	if idx := strings.LastIndex(ext, "."); idx >= 0 {
		ext = ext[idx:]
	} else {
		ext = ""
	}
	m := map[string]string{
		".pdf":      "application/pdf",
		".docx":     "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
		".doc":      "application/msword",
		".pptx":     "application/vnd.openxmlformats-officedocument.presentationml.presentation",
		".ppt":      "application/vnd.ms-powerpoint",
		".xlsx":     "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
		".xls":      "application/vnd.ms-excel",
		".csv":      "text/csv",
		".jpg":      "image/jpeg",
		".jpeg":     "image/jpeg",
		".png":      "image/png",
		".gif":      "image/gif",
		".bmp":      "image/bmp",
		".webp":     "image/webp",
		".svg":      "image/svg+xml",
		".tiff":     "image/tiff",
		".txt":      "text/plain; charset=utf-8",
		".md":       "text/markdown; charset=utf-8",
		".markdown": "text/markdown; charset=utf-8",
		".json":     "application/json; charset=utf-8",
		".xml":      "application/xml; charset=utf-8",
		".html":     "text/html; charset=utf-8",
		".css":      "text/css; charset=utf-8",
		".js":       "text/javascript; charset=utf-8",
		".ts":       "text/typescript; charset=utf-8",
		".py":       "text/x-python; charset=utf-8",
		".go":       "text/x-go; charset=utf-8",
		".java":     "text/x-java; charset=utf-8",
		".yaml":     "text/yaml; charset=utf-8",
		".yml":      "text/yaml; charset=utf-8",
		".sh":       "text/x-shellscript; charset=utf-8",
	}
	if ct, ok := m[ext]; ok {
		return ct
	}
	return "application/octet-stream"
}

// PreviewKnowledgeFile godoc
// @Summary      预览知识文件
// @Description  返回知识条目关联的原始文件，Content-Type 根据文件类型设置，用于浏览器内嵌预览
// @Tags         知识管理
// @Accept       json
// @Produce      application/pdf,image/jpeg,image/png,text/plain
// @Param        id   path      string  true  "知识ID"
// @Success      200  {file}    file    "文件内容"
// @Failure      400  {object}  errors.AppError  "请求参数错误"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /knowledge/{id}/preview [get]
func (h *KnowledgeHandler) PreviewKnowledgeFile(c *gin.Context) {
	ctx := c.Request.Context()

	id := secutils.SanitizeForLog(c.Param("id"))
	if id == "" {
		c.Error(errors.NewBadRequestError("Knowledge ID cannot be empty"))
		return
	}

	_, effCtx, err := h.resolveKnowledgeAndValidateKBAccess(c, id, types.OrgRoleViewer)
	if err != nil {
		c.Error(err)
		return
	}

	file, filename, err := h.kgService.GetKnowledgeFile(effCtx, id)
	if err != nil {
		logger.ErrorWithFields(ctx, err, nil)
		c.Error(errors.NewInternalServerError("Failed to retrieve file").WithDetails(err.Error()))
		return
	}
	defer file.Close()

	contentType := mimeTypeByExt(filename)
	c.Header("Content-Type", contentType)
	c.Header("Content-Disposition", mime.FormatMediaType("inline", map[string]string{"filename": filename}))
	c.Header("Cache-Control", "private, max-age=3600")

	c.Stream(func(w io.Writer) bool {
		if _, err := io.Copy(w, file); err != nil {
			logger.Errorf(ctx, "Failed to stream preview: %v", err)
			return false
		}
		return false
	})
}

// GetKnowledgeBatchRequest defines parameters for batch knowledge retrieval
type GetKnowledgeBatchRequest struct {
	IDs     []string `form:"ids" binding:"required"` // List of knowledge IDs
	KBID    string   `form:"kb_id"`                  // Optional: scope to this KB (validates access and uses effective tenant for shared KB)
	AgentID string   `form:"agent_id"`               // Optional: when using a shared agent, use agent's tenant for retrieval (validates shared agent access)
}

// GetKnowledgeBatch godoc
// @Summary      批量获取知识
// @Description  根据ID列表批量获取知识条目。可选 kb_id：指定时按该知识库校验权限并用于共享知识库的租户解析；可选 agent_id：使用共享智能体时传此参数，后端按智能体所属租户查询（用于刷新后恢复共享知识库下的文件）
// @Tags         知识管理
// @Accept       json
// @Produce      json
// @Param        ids       query     []string  true   "知识ID列表"
// @Param        kb_id     query     string   false  "可选，知识库ID（用于共享知识库时指定范围）"
// @Param        agent_id  query     string   false  "可选，共享智能体ID（用于按智能体租户批量拉取文件详情）"
// @Success      200       {object}  map[string]interface{}  "知识列表"
// @Failure      400       {object}  errors.AppError        "请求参数错误"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /knowledge/batch [get]
func (h *KnowledgeHandler) GetKnowledgeBatch(c *gin.Context) {
	ctx := c.Request.Context()

	tenantID, ok := c.Get(types.TenantIDContextKey.String())
	if !ok {
		logger.Error(ctx, "Failed to get tenant ID")
		c.Error(errors.NewUnauthorizedError("Unauthorized"))
		return
	}
	effectiveTenantID := tenantID.(uint64)

	var req GetKnowledgeBatchRequest
	if err := c.ShouldBindQuery(&req); err != nil {
		logger.Error(ctx, "Failed to parse request parameters", err)
		c.Error(errors.NewBadRequestError("Invalid request parameters").WithDetails(err.Error()))
		return
	}

	// agentAllowedKBIDs restricts results to the agent's configured KB scope.
	// nil = no agent restriction; empty slice = agent has no KB access (none mode).
	var agentAllowedKBIDs []string

	// Optional agent_id: when using shared agent, resolve agent and use its tenant for batch retrieval (so shared KB files can be loaded after refresh)
	if agentID := secutils.SanitizeForLog(req.AgentID); agentID != "" && h.agentShareService != nil {
		userIDVal, ok := c.Get(types.UserIDContextKey.String())
		if !ok {
			c.Error(errors.NewUnauthorizedError("Unauthorized"))
			return
		}
		userID, _ := userIDVal.(string)
		currentTenantID := c.GetUint64(types.TenantIDContextKey.String())
		if currentTenantID == 0 {
			c.Error(errors.NewUnauthorizedError("Unauthorized"))
			return
		}
		agent, err := h.agentShareService.GetSharedAgentForUser(ctx, userID, currentTenantID, agentID)
		if err != nil || agent == nil {
			logger.Warnf(ctx, "GetKnowledgeBatch: invalid or inaccessible shared agent %s: %v", agentID, err)
			c.Error(errors.NewForbiddenError("Invalid or inaccessible shared agent").WithDetails(err.Error()))
			return
		}
		effectiveTenantID = agent.TenantID
		agentAllowedKBIDs = resolveAgentAllowedKBIDs(agent)

		if agentAllowedKBIDs != nil && len(agentAllowedKBIDs) == 0 {
			c.JSON(http.StatusOK, gin.H{"success": true, "data": []*types.Knowledge{}})
			return
		}
		logger.Infof(ctx, "Batch retrieving knowledge with agent_id, effective tenant ID: %d, IDs count: %d, allowed KBs: %v",
			effectiveTenantID, len(req.IDs), agentAllowedKBIDs)
	}

	var knowledges []*types.Knowledge
	var err error

	// scopeKBID tracks the single KB the results must belong to (set by explicit kb_id).
	var scopeKBID string

	// Optional kb_id: validate KB access and use effective tenant for shared KB
	if kbID := secutils.SanitizeForLog(req.KBID); kbID != "" {
		_, _, effID, _, err := h.validateKnowledgeBaseAccessWithKBID(c, kbID)
		if err != nil {
			c.Error(err)
			return
		}
		if agentAllowedKBIDs != nil && !sliceContains(agentAllowedKBIDs, kbID) {
			c.Error(errors.NewForbiddenError("Knowledge base not accessible through this agent"))
			return
		}
		scopeKBID = kbID
		effectiveTenantID = effID
		ctx = context.WithValue(ctx, types.TenantIDContextKey, effectiveTenantID)

		logger.Infof(ctx, "Batch retrieving knowledge with kb_id, effective tenant ID: %d, IDs count: %d",
			effectiveTenantID, len(req.IDs))

		knowledges, err = h.kgService.GetKnowledgeBatch(ctx, effectiveTenantID, req.IDs)
	} else {
		// No kb_id: use GetKnowledgeBatchWithSharedAccess (or effectiveTenantID may already be set by agent_id for shared agent)
		logger.Infof(ctx, "Batch retrieving knowledge without kb_id, effective tenant ID: %d, IDs count: %d",
			effectiveTenantID, len(req.IDs))

		knowledges, err = h.kgService.GetKnowledgeBatchWithSharedAccess(ctx, effectiveTenantID, req.IDs)
	}

	// Build the effective allowed-KB set from both scopeKBID and agentAllowedKBIDs.
	// scopeKBID (from explicit kb_id) restricts to a single KB;
	// agentAllowedKBIDs (from shared agent) restricts to the agent's configured KBs.
	var allowedKBSet map[string]bool
	if scopeKBID != "" {
		allowedKBSet = map[string]bool{scopeKBID: true}
	} else if agentAllowedKBIDs != nil {
		allowedKBSet = make(map[string]bool, len(agentAllowedKBIDs))
		for _, id := range agentAllowedKBIDs {
			allowedKBSet[id] = true
		}
	}
	if allowedKBSet != nil && len(knowledges) > 0 {
		filtered := make([]*types.Knowledge, 0, len(knowledges))
		for _, k := range knowledges {
			if allowedKBSet[k.KnowledgeBaseID] {
				filtered = append(filtered, k)
			}
		}
		knowledges = filtered
	}

	if err != nil {
		logger.ErrorWithFields(ctx, err, nil)
		c.Error(errors.NewInternalServerError("Failed to retrieve knowledge list").WithDetails(err.Error()))
		return
	}

	logger.Infof(ctx, "Batch knowledge retrieval successful, requested count: %d, returned count: %d",
		len(req.IDs), len(knowledges))

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    knowledges,
	})
}

// UpdateKnowledge godoc
// @Summary      更新知识
// @Description  更新知识条目信息
// @Tags         知识管理
// @Accept       json
// @Produce      json
// @Param        id       path      string          true  "知识ID"
// @Param        request  body      types.Knowledge true  "知识信息"
// @Success      200      {object}  map[string]interface{}  "更新成功"
// @Failure      400      {object}  errors.AppError         "请求参数错误"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /knowledge/{id} [put]
func (h *KnowledgeHandler) UpdateKnowledge(c *gin.Context) {
	ctx := c.Request.Context()

	id := secutils.SanitizeForLog(c.Param("id"))
	if id == "" {
		logger.Error(ctx, "Knowledge ID is empty")
		c.Error(errors.NewBadRequestError("Knowledge ID cannot be empty"))
		return
	}

	_, effCtx, err := h.resolveKnowledgeAndValidateKBAccess(c, id, types.OrgRoleEditor)
	if err != nil {
		c.Error(err)
		return
	}

	var knowledge types.Knowledge
	if err := c.ShouldBindJSON(&knowledge); err != nil {
		logger.Error(ctx, "Failed to parse request parameters", err)
		c.Error(errors.NewBadRequestError(err.Error()))
		return
	}
	knowledge.ID = id

	if err := h.kgService.UpdateKnowledge(effCtx, &knowledge); err != nil {
		logger.ErrorWithFields(ctx, err, nil)
		c.Error(errors.NewInternalServerError(err.Error()))
		return
	}

	logger.Infof(ctx, "Knowledge updated successfully, knowledge ID: %s", id)
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Knowledge chunk updated successfully",
	})
}

// UpdateManualKnowledge godoc
// @Summary      更新手工知识
// @Description  更新手工录入的Markdown知识内容
// @Tags         知识管理
// @Accept       json
// @Produce      json
// @Param        id       path      string                       true  "知识ID"
// @Param        request  body      types.ManualKnowledgePayload true  "手工知识内容"
// @Success      200      {object}  map[string]interface{}       "更新后的知识"
// @Failure      400      {object}  errors.AppError              "请求参数错误"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /knowledge/manual/{id} [put]
func (h *KnowledgeHandler) UpdateManualKnowledge(c *gin.Context) {
	ctx := c.Request.Context()
	logger.Info(ctx, "Start updating manual knowledge")

	id := secutils.SanitizeForLog(c.Param("id"))
	if id == "" {
		logger.Error(ctx, "Knowledge ID is empty")
		c.Error(errors.NewBadRequestError("Knowledge ID cannot be empty"))
		return
	}

	_, effCtx, err := h.resolveKnowledgeAndValidateKBAccess(c, id, types.OrgRoleEditor)
	if err != nil {
		c.Error(err)
		return
	}

	var req types.ManualKnowledgePayload
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Error(ctx, "Failed to parse manual knowledge update request", err)
		c.Error(errors.NewBadRequestError(err.Error()))
		return
	}

	knowledge, err := h.kgService.UpdateManualKnowledge(effCtx, id, &req)
	if err != nil {
		if appErr, ok := errors.IsAppError(err); ok {
			c.Error(appErr)
			return
		}
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"knowledge_id": id,
		})
		c.Error(errors.NewInternalServerError(err.Error()))
		return
	}

	logger.Infof(ctx, "Manual knowledge updated successfully, knowledge ID: %s", id)
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    knowledge,
	})
}

// ReparseKnowledge godoc
// @Summary      重新解析知识
// @Description  删除知识中现有的文档内容并重新解析，使用异步任务方式处理
// @Tags         知识管理
// @Accept       json
// @Produce      json
// @Param        id   path      string  true  "知识ID"
// @Success      200  {object}  map[string]interface{}  "重新解析任务已提交"
// @Failure      400  {object}  errors.AppError         "请求参数错误"
// @Failure      403  {object}  errors.AppError         "权限不足"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /knowledge/{id}/reparse [post]
func (h *KnowledgeHandler) ReparseKnowledge(c *gin.Context) {
	ctx := c.Request.Context()
	logger.Info(ctx, "Start re-parsing knowledge")

	id := secutils.SanitizeForLog(c.Param("id"))
	if id == "" {
		logger.Error(ctx, "Knowledge ID is empty")
		c.Error(errors.NewBadRequestError("Knowledge ID cannot be empty"))
		return
	}

	// Validate KB access with editor permission (reparse requires write access)
	_, effCtx, err := h.resolveKnowledgeAndValidateKBAccess(c, id, types.OrgRoleEditor)
	if err != nil {
		c.Error(err)
		return
	}

	// Call service to reparse knowledge
	knowledge, err := h.kgService.ReparseKnowledge(effCtx, id)
	if err != nil {
		if appErr, ok := errors.IsAppError(err); ok {
			c.Error(appErr)
			return
		}
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"knowledge_id": id,
		})
		c.Error(errors.NewInternalServerError(err.Error()))
		return
	}

	logger.Infof(ctx, "Knowledge reparse task submitted successfully, knowledge ID: %s", id)
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Knowledge reparse task submitted",
		"data":    knowledge,
	})
}

type knowledgeTagBatchRequest struct {
	Updates map[string]*string `json:"updates" binding:"required,min=1"`
	KBID    string             `json:"kb_id"` // Optional: scope to this KB (validates editor access and uses effective tenant for shared KB)
}

// UpdateKnowledgeTagBatch godoc
// @Summary      批量更新知识标签
// @Description  批量更新知识条目的标签。可选 kb_id：指定时按该知识库校验编辑权限并用于共享知识库的租户解析
// @Tags         知识管理
// @Accept       json
// @Produce      json
// @Param        request  body      object  true  "标签更新请求（updates 必填，kb_id 可选）"
// @Success      200      {object}  map[string]interface{}  "更新成功"
// @Failure      400      {object}  errors.AppError         "请求参数错误"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /knowledge/tags [put]
func (h *KnowledgeHandler) UpdateKnowledgeTagBatch(c *gin.Context) {
	ctx := c.Request.Context()

	// Ensure tenant ID is in context (service reads it; may be missing if request context was not set by auth)
	tenantID := c.GetUint64(types.TenantIDContextKey.String())
	if tenantID == 0 {
		c.Error(errors.NewUnauthorizedError("Unauthorized"))
		return
	}
	ctx = context.WithValue(ctx, types.TenantIDContextKey, tenantID)

	var req knowledgeTagBatchRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Error(ctx, "Failed to parse knowledge tag batch request", err)
		c.Error(errors.NewBadRequestError("请求参数不合法").WithDetails(err.Error()))
		return
	}
	// Resolve effective tenant and the authorized KB scope.
	var authorizedKBID string
	if kbID := secutils.SanitizeForLog(req.KBID); kbID != "" {
		_, _, effID, permission, err := h.validateKnowledgeBaseAccessWithKBID(c, kbID)
		if err != nil {
			c.Error(err)
			return
		}
		if permission != types.OrgRoleAdmin && permission != types.OrgRoleEditor {
			c.Error(errors.NewForbiddenError("No permission to update knowledge tags"))
			return
		}
		authorizedKBID = kbID
		ctx = context.WithValue(ctx, types.TenantIDContextKey, effID)
	} else if len(req.Updates) > 0 {
		// No kb_id: infer from first knowledge ID so shared-KB updates work without client sending kb_id
		var firstKnowledgeID string
		for id := range req.Updates {
			firstKnowledgeID = id
			break
		}
		if firstKnowledgeID != "" {
			knowledge, effCtx, err := h.resolveKnowledgeAndValidateKBAccess(c, firstKnowledgeID, types.OrgRoleEditor)
			if err != nil {
				c.Error(err)
				return
			}
			authorizedKBID = knowledge.KnowledgeBaseID
			ctx = effCtx
		}
	}
	if err := h.kgService.UpdateKnowledgeTagBatch(ctx, authorizedKBID, req.Updates); err != nil {
		logger.ErrorWithFields(ctx, err, nil)
		c.Error(err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
	})
}

// UpdateImageInfo godoc
// @Summary      更新图像信息
// @Description  更新知识分块的图像信息
// @Tags         知识管理
// @Accept       json
// @Produce      json
// @Param        id        path      string  true  "知识ID"
// @Param        chunk_id  path      string  true  "分块ID"
// @Param        request   body      object{image_info=string}  true  "图像信息"
// @Success      200       {object}  map[string]interface{}     "更新成功"
// @Failure      400       {object}  errors.AppError            "请求参数错误"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /knowledge/image/{id}/{chunk_id} [put]
func (h *KnowledgeHandler) UpdateImageInfo(c *gin.Context) {
	ctx := c.Request.Context()
	logger.Info(ctx, "Start updating image info")

	id := secutils.SanitizeForLog(c.Param("id"))
	if id == "" {
		logger.Error(ctx, "Knowledge ID is empty")
		c.Error(errors.NewBadRequestError("Knowledge ID cannot be empty"))
		return
	}
	chunkID := secutils.SanitizeForLog(c.Param("chunk_id"))
	if chunkID == "" {
		logger.Error(ctx, "Chunk ID is empty")
		c.Error(errors.NewBadRequestError("Chunk ID cannot be empty"))
		return
	}

	_, effCtx, err := h.resolveKnowledgeAndValidateKBAccess(c, id, types.OrgRoleEditor)
	if err != nil {
		c.Error(err)
		return
	}

	var request struct {
		ImageInfo string `json:"image_info"`
	}

	if err := c.ShouldBindJSON(&request); err != nil {
		logger.Error(ctx, "Failed to parse request parameters", err)
		c.Error(errors.NewBadRequestError(err.Error()))
		return
	}

	logger.Infof(ctx, "Updating knowledge chunk, knowledge ID: %s, chunk ID: %s", id, chunkID)
	err = h.kgService.UpdateImageInfo(effCtx, id, chunkID, secutils.SanitizeForLog(request.ImageInfo))
	if err != nil {
		logger.ErrorWithFields(ctx, err, nil)
		c.Error(errors.NewInternalServerError(err.Error()))
		return
	}

	logger.Infof(ctx, "Knowledge chunk updated successfully, knowledge ID: %s, chunk ID: %s", id, chunkID)
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Knowledge chunk image updated successfully",
	})
}

// SearchKnowledge godoc
// @Summary      Search knowledge
// @Description  Search knowledge files by keyword. When agent_id is set (shared agent), scope is the agent's configured knowledge bases.
// @Tags         Knowledge
// @Accept       json
// @Produce      json
// @Param        keyword    query     string  false "Keyword to search"
// @Param        offset     query     int     false "Offset for pagination"
// @Param        limit      query     int     false "Limit for pagination (default 20)"
// @Param        file_types query     string  false "Comma-separated file extensions to filter (e.g., csv,xlsx)"
// @Param        agent_id   query     string  false "Shared agent ID (search within agent's KB scope)"
// @Success      200         {object}  map[string]interface{}     "Search results"
// @Failure      400         {object}  errors.AppError            "Invalid request"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /knowledge/search [get]
func (h *KnowledgeHandler) SearchKnowledge(c *gin.Context) {
	ctx := c.Request.Context()
	if userID, ok := c.Get(types.UserIDContextKey.String()); ok {
		ctx = context.WithValue(ctx, types.UserIDContextKey, userID)
	}
	keyword := c.Query("keyword")
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))

	var fileTypes []string
	if fileTypesStr := c.Query("file_types"); fileTypesStr != "" {
		for _, ft := range strings.Split(fileTypesStr, ",") {
			ft = strings.TrimSpace(ft)
			if ft != "" {
				fileTypes = append(fileTypes, ft)
			}
		}
	}

	agentID := c.Query("agent_id")
	if agentID != "" {
		userIDVal, ok := c.Get(types.UserIDContextKey.String())
		if !ok {
			c.Error(errors.NewUnauthorizedError("user ID not found"))
			return
		}
		userID, _ := userIDVal.(string)
		currentTenantID := c.GetUint64(types.TenantIDContextKey.String())
		if currentTenantID == 0 {
			c.Error(errors.NewUnauthorizedError("tenant ID not found"))
			return
		}
		agent, err := h.agentShareService.GetSharedAgentForUser(ctx, userID, currentTenantID, agentID)
		if err != nil {
			if goerrors.Is(err, service.ErrAgentShareNotFound) || goerrors.Is(err, service.ErrAgentSharePermission) || goerrors.Is(err, service.ErrAgentNotFoundForShare) {
				c.Error(errors.NewForbiddenError("no permission for this shared agent"))
				return
			}
			logger.ErrorWithFields(ctx, err, nil)
			c.Error(errors.NewInternalServerError("Failed to verify shared agent access").WithDetails(err.Error()))
			return
		}
		sourceTenantID := agent.TenantID
		mode := agent.Config.KBSelectionMode
		if mode == "none" {
			c.JSON(http.StatusOK, gin.H{
				"success":  true,
				"data":     []interface{}{},
				"has_more": false,
			})
			return
		}
		var scopes []types.KnowledgeSearchScope
		if mode == "selected" && len(agent.Config.KnowledgeBases) > 0 {
			for _, kbID := range agent.Config.KnowledgeBases {
				if kbID != "" {
					scopes = append(scopes, types.KnowledgeSearchScope{TenantID: sourceTenantID, KBID: kbID})
				}
			}
		}
		if len(scopes) == 0 {
			kbs, err := h.kbService.ListKnowledgeBasesByTenantID(ctx, sourceTenantID)
			if err != nil {
				logger.ErrorWithFields(ctx, err, nil)
				c.Error(errors.NewInternalServerError("Failed to list knowledge bases").WithDetails(err.Error()))
				return
			}
			// `all` mode: authoritative server-side capability filter. Mirrors the
			// logic in ListKnowledgeBases so @file search, KB listing, and runtime
			// all agree on what "mode=all" actually means for this agent. The
			// filter is agent-mode aware so quick-answer (RAG-only) skips
			// wiki-only KBs even though it has no `allowed_tools`.
			filter := tools.DeriveKBFilterForAgent(agent.Config.AgentMode, agent.Config.AllowedTools)
			removed := 0
			for _, kb := range kbs {
				if kb == nil || kb.Type != types.KnowledgeBaseTypeDocument {
					continue
				}
				if !filter.IsEmpty() && !tools.KBSatisfiesAgentRequirements(kb.Capabilities(), agent.Config.AgentMode, agent.Config.AllowedTools) {
					removed++
					continue
				}
				scopes = append(scopes, types.KnowledgeSearchScope{TenantID: sourceTenantID, KBID: kb.ID})
			}
			if removed > 0 {
				logger.Infof(ctx,
					"SearchKnowledge(agent=%s, mode=all): capability filter removed %d KBs",
					agentID, removed)
			}
		}
		knowledges, hasMore, err := h.kgService.SearchKnowledgeForScopes(ctx, scopes, keyword, offset, limit, fileTypes)
		if err != nil {
			logger.ErrorWithFields(ctx, err, nil)
			c.Error(errors.NewInternalServerError("Failed to search knowledge").WithDetails(err.Error()))
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"success":  true,
			"data":     knowledges,
			"has_more": hasMore,
		})
		return
	}

	// Default: own + shared KBs
	knowledges, hasMore, err := h.kgService.SearchKnowledge(ctx, keyword, offset, limit, fileTypes)
	if err != nil {
		logger.ErrorWithFields(ctx, err, nil)
		c.Error(errors.NewInternalServerError("Failed to search knowledge").WithDetails(err.Error()))
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success":  true,
		"data":     knowledges,
		"has_more": hasMore,
	})
}

// MoveKnowledgeRequest defines the request for moving knowledge items
type MoveKnowledgeRequest struct {
	KnowledgeIDs []string `json:"knowledge_ids" binding:"required,min=1"`
	SourceKBID   string   `json:"source_kb_id"  binding:"required"`
	TargetKBID   string   `json:"target_kb_id"  binding:"required"`
	Mode         string   `json:"mode"          binding:"required,oneof=reuse_vectors reparse"`
}

// MoveKnowledgeResponse defines the response for move knowledge
type MoveKnowledgeResponse struct {
	TaskID         string `json:"task_id"`
	SourceKBID     string `json:"source_kb_id"`
	TargetKBID     string `json:"target_kb_id"`
	KnowledgeCount int    `json:"knowledge_count"`
	Message        string `json:"message"`
}

// MoveKnowledge moves knowledge items from one knowledge base to another (async task).
//
// MoveKnowledge godoc
// @Summary      移动知识到其他知识库
// @Description  将一条或多条知识从源知识库移动到目标知识库（异步），返回任务 ID 用于查询进度
// @Tags         知识
// @Accept       json
// @Produce      json
// @Param        request  body      handler.MoveKnowledgeRequest  true  "{source_kb_id, target_kb_id, knowledge_ids}"
// @Success      200      {object}  handler.MoveKnowledgeResponse  "任务信息"
// @Failure      400      {object}  errors.AppError                "请求参数错误"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /knowledge/move [post]
func (h *KnowledgeHandler) MoveKnowledge(c *gin.Context) {
	ctx := c.Request.Context()

	var req MoveKnowledgeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Error(ctx, "MoveKnowledge: failed to parse request", err)
		c.Error(errors.NewBadRequestError("Invalid request parameters: " + err.Error()))
		return
	}

	// Validate source != target
	if req.SourceKBID == req.TargetKBID {
		c.Error(errors.NewBadRequestError("Source and target knowledge base cannot be the same"))
		return
	}

	tenantID, exists := c.Get(types.TenantIDContextKey.String())
	if !exists {
		c.Error(errors.NewUnauthorizedError("Unauthorized"))
		return
	}

	// Validate source KB
	sourceKB, err := h.kbService.GetKnowledgeBaseByID(ctx, req.SourceKBID)
	if err != nil {
		if goerrors.Is(err, repository.ErrKnowledgeBaseNotFound) {
			c.Error(errors.NewNotFoundError("Source knowledge base not found"))
			return
		}
		c.Error(errors.NewInternalServerError(err.Error()))
		return
	}
	if sourceKB.TenantID != tenantID.(uint64) {
		c.Error(errors.NewForbiddenError("No permission to access source knowledge base"))
		return
	}

	// Validate target KB
	targetKB, err := h.kbService.GetKnowledgeBaseByID(ctx, req.TargetKBID)
	if err != nil {
		if goerrors.Is(err, repository.ErrKnowledgeBaseNotFound) {
			c.Error(errors.NewNotFoundError("Target knowledge base not found"))
			return
		}
		c.Error(errors.NewInternalServerError(err.Error()))
		return
	}
	if targetKB.TenantID != tenantID.(uint64) {
		c.Error(errors.NewForbiddenError("No permission to access target knowledge base"))
		return
	}

	// Validate type match
	if sourceKB.Type != targetKB.Type {
		c.Error(errors.NewBadRequestError("Source and target knowledge bases must be the same type"))
		return
	}

	// Validate embedding model match
	if sourceKB.EmbeddingModelID != targetKB.EmbeddingModelID {
		c.Error(errors.NewBadRequestError("Source and target must use the same embedding model"))
		return
	}

	// Validate all knowledge IDs belong to source KB and are in completed status
	for _, kID := range req.KnowledgeIDs {
		knowledge, err := h.kgService.GetKnowledgeByID(ctx, kID)
		if err != nil {
			c.Error(errors.NewBadRequestError(fmt.Sprintf("Knowledge item %s not found", kID)))
			return
		}
		if knowledge.KnowledgeBaseID != req.SourceKBID {
			c.Error(errors.NewBadRequestError(fmt.Sprintf("Knowledge item %s does not belong to the source knowledge base", kID)))
			return
		}
		if knowledge.ParseStatus != types.ParseStatusCompleted {
			c.Error(errors.NewBadRequestError(fmt.Sprintf("Knowledge item %s is not in completed status (current: %s)", kID, knowledge.ParseStatus)))
			return
		}
	}

	// Generate task ID
	taskID := utils.GenerateTaskID("kg_move", tenantID.(uint64), req.SourceKBID)

	// Create move payload
	payload := types.KnowledgeMovePayload{
		TenantID:     tenantID.(uint64),
		TaskID:       taskID,
		KnowledgeIDs: req.KnowledgeIDs,
		SourceKBID:   req.SourceKBID,
		TargetKBID:   req.TargetKBID,
		Mode:         req.Mode,
	}
	langfuse.InjectTracing(ctx, &payload)

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		logger.Errorf(ctx, "MoveKnowledge: failed to marshal payload: %v", err)
		c.Error(errors.NewInternalServerError("Failed to create task"))
		return
	}

	// Enqueue move task
	task := asynq.NewTask(types.TypeKnowledgeMove, payloadBytes,
		asynq.TaskID(taskID), asynq.Queue("default"), asynq.MaxRetry(3))
	info, err := h.asynqClient.Enqueue(task)
	if err != nil {
		logger.Errorf(ctx, "MoveKnowledge: failed to enqueue task: %v", err)
		c.Error(errors.NewInternalServerError("Failed to enqueue task"))
		return
	}

	logger.Infof(ctx, "MoveKnowledge: task enqueued: %s, asynq_id: %s, source: %s, target: %s, count: %d",
		taskID, info.ID, secutils.SanitizeForLog(req.SourceKBID), secutils.SanitizeForLog(req.TargetKBID), len(req.KnowledgeIDs))

	// Save initial progress
	initialProgress := &types.KnowledgeMoveProgress{
		TaskID:     taskID,
		SourceKBID: req.SourceKBID,
		TargetKBID: req.TargetKBID,
		Status:     types.KBCloneStatusPending,
		Total:      len(req.KnowledgeIDs),
		Progress:   0,
		Message:    "Task queued, waiting to start...",
		CreatedAt:  time.Now().Unix(),
		UpdatedAt:  time.Now().Unix(),
	}
	if err := h.kgService.SaveKnowledgeMoveProgress(ctx, initialProgress); err != nil {
		logger.Warnf(ctx, "MoveKnowledge: failed to save initial progress: %v", err)
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": MoveKnowledgeResponse{
			TaskID:         taskID,
			SourceKBID:     req.SourceKBID,
			TargetKBID:     req.TargetKBID,
			KnowledgeCount: len(req.KnowledgeIDs),
			Message:        "Knowledge move task started",
		},
	})
}

// GetKnowledgeMoveProgress retrieves the progress of a knowledge move task.
//
// GetKnowledgeMoveProgress godoc
// @Summary      获取知识移动进度
// @Description  按任务 ID 查询移动进度
// @Tags         知识
// @Produce      json
// @Param        task_id  path      string                       true  "移动任务 ID"
// @Success      200      {object}  types.KnowledgeMoveProgress  "进度信息"
// @Failure      404      {object}  errors.AppError              "任务不存在"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /knowledge/move/progress/{task_id} [get]
func (h *KnowledgeHandler) GetKnowledgeMoveProgress(c *gin.Context) {
	ctx := c.Request.Context()

	taskID := c.Param("task_id")
	if taskID == "" {
		c.Error(errors.NewBadRequestError("Task ID cannot be empty"))
		return
	}

	progress, err := h.kgService.GetKnowledgeMoveProgress(ctx, taskID)
	if err != nil {
		logger.ErrorWithFields(ctx, err, nil)
		c.Error(err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    progress,
	})
}

// resolveAgentAllowedKBIDs returns the set of knowledge base IDs that the
// shared agent is allowed to access based on its KBSelectionMode config.
// Returns nil when no restriction applies ("all" mode), or a concrete slice
// (possibly empty for "none" mode) when the results must be filtered.
func resolveAgentAllowedKBIDs(agent *types.CustomAgent) []string {
	switch agent.Config.KBSelectionMode {
	case "all":
		return nil
	case "none":
		return []string{}
	case "selected":
		return agent.Config.KnowledgeBases
	default:
		if len(agent.Config.KnowledgeBases) > 0 {
			return agent.Config.KnowledgeBases
		}
		return nil
	}
}

func sliceContains(ss []string, target string) bool {
	for _, s := range ss {
		if s == target {
			return true
		}
	}
	return false
}
