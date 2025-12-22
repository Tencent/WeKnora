package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	secutils "github.com/Tencent/WeKnora/internal/utils"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/hibiken/asynq"
)

// KnowledgeBaseHandler defines the HTTP handler for knowledge base operations
type KnowledgeBaseHandler struct {
	service          interfaces.KnowledgeBaseService
	knowledgeService interfaces.KnowledgeService
	asynqClient      *asynq.Client
}

// NewKnowledgeBaseHandler creates a new knowledge base handler instance
func NewKnowledgeBaseHandler(
	service interfaces.KnowledgeBaseService,
	knowledgeService interfaces.KnowledgeService,
	asynqClient *asynq.Client,
) *KnowledgeBaseHandler {
	return &KnowledgeBaseHandler{
		service:          service,
		knowledgeService: knowledgeService,
		asynqClient:      asynqClient,
	}
}

// HybridSearch godoc
// @Summary      混合搜索
// @Description  在知识库中执行向量和关键词混合搜索
// @Tags         知识库
// @Accept       json
// @Produce      json
// @Param        id       path      string             true  "知识库ID"
// @Param        request  body      types.SearchParams true  "搜索参数"
// @Success      200      {object}  map[string]interface{}  "搜索结果"
// @Failure      400      {object}  errors.AppError         "请求参数错误"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /knowledge-bases/{id}/hybrid-search [get]
func (h *KnowledgeBaseHandler) HybridSearch(c *gin.Context) {
	ctx := c.Request.Context()

	logger.Info(ctx, "Start hybrid search")

	// Validate knowledge base ID
	id := secutils.SanitizeForLog(c.Param("id"))
	if id == "" {
		logger.Error(ctx, "Knowledge base ID is empty")
		c.Error(errors.NewBadRequestError("Knowledge base ID cannot be empty"))
		return
	}

	// Parse request body
	var req types.SearchParams
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Error(ctx, "Failed to parse request parameters", err)
		c.Error(errors.NewBadRequestError("Invalid request parameters").WithDetails(err.Error()))
		return
	}

	logger.Infof(ctx, "Executing hybrid search, knowledge base ID: %s, query: %s",
		secutils.SanitizeForLog(id), secutils.SanitizeForLog(req.QueryText))

	// Execute hybrid search with default search parameters
	results, err := h.service.HybridSearch(ctx, id, req)
	if err != nil {
		logger.ErrorWithFields(ctx, err, nil)
		c.Error(errors.NewInternalServerError(err.Error()))
		return
	}

	logger.Infof(ctx, "Hybrid search completed, knowledge base ID: %s, result count: %d",
		secutils.SanitizeForLog(id), len(results))
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    results,
	})
}

// CreateKnowledgeBase godoc
// @Summary      创建知识库
// @Description  创建新的知识库
// @Tags         知识库
// @Accept       json
// @Produce      json
// @Param        request  body      types.KnowledgeBase  true  "知识库信息"
// @Success      201      {object}  map[string]interface{}  "创建的知识库"
// @Failure      400      {object}  errors.AppError         "请求参数错误"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /knowledge-bases [post]
func (h *KnowledgeBaseHandler) CreateKnowledgeBase(c *gin.Context) {
	ctx := c.Request.Context()

	logger.Info(ctx, "Start creating knowledge base")

	// Parse request body
	var req types.KnowledgeBase
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Error(ctx, "Failed to parse request parameters", err)
		c.Error(errors.NewBadRequestError("Invalid request parameters").WithDetails(err.Error()))
		return
	}
	if err := validateExtractConfig(req.ExtractConfig); err != nil {
		logger.Error(ctx, "Invalid extract configuration", err)
		c.Error(err)
		return
	}

	logger.Infof(ctx, "Creating knowledge base, name: %s", secutils.SanitizeForLog(req.Name))
	// Create knowledge base using the service
	kb, err := h.service.CreateKnowledgeBase(ctx, &req)
	if err != nil {
		logger.ErrorWithFields(ctx, err, nil)
		c.Error(errors.NewInternalServerError(err.Error()))
		return
	}

	logger.Infof(ctx, "Knowledge base created successfully, ID: %s, name: %s",
		secutils.SanitizeForLog(kb.ID), secutils.SanitizeForLog(kb.Name))
	c.JSON(http.StatusCreated, gin.H{
		"success": true,
		"data":    kb,
	})
}

// validateAndGetKnowledgeBase validates request parameters and retrieves the knowledge base
// Returns the knowledge base, knowledge base ID, and any errors encountered
func (h *KnowledgeBaseHandler) validateAndGetKnowledgeBase(c *gin.Context) (*types.KnowledgeBase, string, error) {
	ctx := c.Request.Context()

	// Get tenant ID from context
	tenantID, exists := c.Get(types.TenantIDContextKey.String())
	if !exists {
		logger.Error(ctx, "Failed to get tenant ID")
		return nil, "", errors.NewUnauthorizedError("Unauthorized")
	}

	// Get knowledge base ID from URL parameter
	id := secutils.SanitizeForLog(c.Param("id"))
	if id == "" {
		logger.Error(ctx, "Knowledge base ID is empty")
		return nil, "", errors.NewBadRequestError("Knowledge base ID cannot be empty")
	}

	// Verify tenant has permission to access this knowledge base
	kb, err := h.service.GetKnowledgeBaseByID(ctx, id)
	if err != nil {
		logger.ErrorWithFields(ctx, err, nil)
		return nil, id, errors.NewInternalServerError(err.Error())
	}

	// Verify tenant ownership
	if kb.TenantID != tenantID.(uint64) {
		logger.Warnf(
			ctx,
			"Tenant has no permission to access this knowledge base, knowledge base ID: %s, "+
				"request tenant ID: %d, knowledge base tenant ID: %d",
			id, tenantID.(uint64), kb.TenantID,
		)
		return nil, id, errors.NewForbiddenError("No permission to operate")
	}

	return kb, id, nil
}

// GetKnowledgeBase godoc
// @Summary      获取知识库详情
// @Description  根据ID获取知识库详情
// @Tags         知识库
// @Accept       json
// @Produce      json
// @Param        id   path      string  true  "知识库ID"
// @Success      200  {object}  map[string]interface{}  "知识库详情"
// @Failure      400  {object}  errors.AppError         "请求参数错误"
// @Failure      404  {object}  errors.AppError         "知识库不存在"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /knowledge-bases/{id} [get]
func (h *KnowledgeBaseHandler) GetKnowledgeBase(c *gin.Context) {
	// Validate and get the knowledge base
	kb, _, err := h.validateAndGetKnowledgeBase(c)
	if err != nil {
		c.Error(err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    kb,
	})
}

// ListKnowledgeBases godoc
// @Summary      获取知识库列表
// @Description  获取当前租户的所有知识库
// @Tags         知识库
// @Accept       json
// @Produce      json
// @Success      200  {object}  map[string]interface{}  "知识库列表"
// @Failure      500  {object}  errors.AppError         "服务器错误"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /knowledge-bases [get]
func (h *KnowledgeBaseHandler) ListKnowledgeBases(c *gin.Context) {
	ctx := c.Request.Context()

	// Get all knowledge bases for this tenant
	kbs, err := h.service.ListKnowledgeBases(ctx)
	if err != nil {
		logger.ErrorWithFields(ctx, err, nil)
		c.Error(errors.NewInternalServerError(err.Error()))
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    kbs,
	})
}

// UpdateKnowledgeBaseRequest defines the request body structure for updating a knowledge base
type UpdateKnowledgeBaseRequest struct {
	Name        string                     `json:"name"        binding:"required"`
	Description string                     `json:"description"`
	Config      *types.KnowledgeBaseConfig `json:"config"      binding:"required"`
}

// UpdateKnowledgeBase godoc
// @Summary      更新知识库
// @Description  更新知识库的名称、描述和配置
// @Tags         知识库
// @Accept       json
// @Produce      json
// @Param        id       path      string                     true  "知识库ID"
// @Param        request  body      UpdateKnowledgeBaseRequest true  "更新请求"
// @Success      200      {object}  map[string]interface{}     "更新后的知识库"
// @Failure      400      {object}  errors.AppError            "请求参数错误"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /knowledge-bases/{id} [put]
func (h *KnowledgeBaseHandler) UpdateKnowledgeBase(c *gin.Context) {
	ctx := c.Request.Context()
	logger.Info(ctx, "Start updating knowledge base")

	// Validate and get the knowledge base
	_, id, err := h.validateAndGetKnowledgeBase(c)
	if err != nil {
		c.Error(err)
		return
	}

	// Parse request body
	var req UpdateKnowledgeBaseRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Error(ctx, "Failed to parse request parameters", err)
		c.Error(errors.NewBadRequestError("Invalid request parameters").WithDetails(err.Error()))
		return
	}

	logger.Infof(ctx, "Updating knowledge base, ID: %s, name: %s",
		secutils.SanitizeForLog(id), secutils.SanitizeForLog(req.Name))

	// Update the knowledge base
	kb, err := h.service.UpdateKnowledgeBase(ctx, id, req.Name, req.Description, req.Config)
	if err != nil {
		logger.ErrorWithFields(ctx, err, nil)
		c.Error(errors.NewInternalServerError(err.Error()))
		return
	}

	logger.Infof(ctx, "Knowledge base updated successfully, ID: %s",
		secutils.SanitizeForLog(id))
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    kb,
	})
}

// DeleteKnowledgeBase godoc
// @Summary      删除知识库
// @Description  删除指定的知识库及其所有内容
// @Tags         知识库
// @Accept       json
// @Produce      json
// @Param        id   path      string  true  "知识库ID"
// @Success      200  {object}  map[string]interface{}  "删除成功"
// @Failure      400  {object}  errors.AppError         "请求参数错误"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /knowledge-bases/{id} [delete]
func (h *KnowledgeBaseHandler) DeleteKnowledgeBase(c *gin.Context) {
	ctx := c.Request.Context()
	logger.Info(ctx, "Start deleting knowledge base")

	// Validate and get the knowledge base
	kb, id, err := h.validateAndGetKnowledgeBase(c)
	if err != nil {
		c.Error(err)
		return
	}

	logger.Infof(ctx, "Deleting knowledge base, ID: %s, name: %s",
		secutils.SanitizeForLog(id), secutils.SanitizeForLog(kb.Name))

	// Delete the knowledge base
	if err := h.service.DeleteKnowledgeBase(ctx, id); err != nil {
		logger.ErrorWithFields(ctx, err, nil)
		c.Error(errors.NewInternalServerError(err.Error()))
		return
	}

	logger.Infof(ctx, "Knowledge base deleted successfully, ID: %s",
		secutils.SanitizeForLog(id))
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Knowledge base deleted successfully",
	})
}

type CopyKnowledgeBaseRequest struct {
	SourceID string `json:"source_id" binding:"required"`
	TargetID string `json:"target_id"`
}

// CopyKnowledgeBaseResponse defines the response for copy knowledge base
type CopyKnowledgeBaseResponse struct {
	TaskID   string `json:"task_id"`
	SourceID string `json:"source_id"`
	TargetID string `json:"target_id"`
	Message  string `json:"message"`
}

// CopyKnowledgeBase godoc
// @Summary      复制知识库
// @Description  将一个知识库的内容复制到另一个知识库（异步任务）
// @Tags         知识库
// @Accept       json
// @Produce      json
// @Param        request  body      CopyKnowledgeBaseRequest   true  "复制请求"
// @Success      200      {object}  map[string]interface{}     "任务ID"
// @Failure      400      {object}  errors.AppError            "请求参数错误"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /knowledge-bases/copy [post]
func (h *KnowledgeBaseHandler) CopyKnowledgeBase(c *gin.Context) {
	ctx := c.Request.Context()
	var req CopyKnowledgeBaseRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Error(ctx, "Failed to parse request parameters", err)
		c.Error(errors.NewBadRequestError("Invalid request parameters").WithDetails(err.Error()))
		return
	}

	// Get tenant ID from context
	tenantID, exists := c.Get(types.TenantIDContextKey.String())
	if !exists {
		logger.Error(ctx, "Failed to get tenant ID")
		c.Error(errors.NewUnauthorizedError("Unauthorized"))
		return
	}

	// Generate task ID
	taskID := uuid.New().String()

	// Create KB clone payload
	payload := types.KBClonePayload{
		TenantID: tenantID.(uint64),
		TaskID:   taskID,
		SourceID: req.SourceID,
		TargetID: req.TargetID,
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		logger.Errorf(ctx, "Failed to marshal KB clone payload: %v", err)
		c.Error(errors.NewInternalServerError("Failed to create task"))
		return
	}

	// Enqueue KB clone task to Asynq
	task := asynq.NewTask(types.TypeKBClone, payloadBytes, asynq.Queue("default"), asynq.MaxRetry(3))
	info, err := h.asynqClient.Enqueue(task)
	if err != nil {
		logger.Errorf(ctx, "Failed to enqueue KB clone task: %v", err)
		c.Error(errors.NewInternalServerError("Failed to enqueue task"))
		return
	}

	logger.Infof(ctx, "KB clone task enqueued: %s, asynq task ID: %s, source: %s, target: %s",
		taskID, info.ID, secutils.SanitizeForLog(req.SourceID), secutils.SanitizeForLog(req.TargetID))

	// Save initial progress to Redis so frontend can query immediately
	initialProgress := &types.KBCloneProgress{
		TaskID:    taskID,
		SourceID:  req.SourceID,
		TargetID:  req.TargetID,
		Status:    types.KBCloneStatusPending,
		Progress:  0,
		Message:   "Task queued, waiting to start...",
		CreatedAt: time.Now().Unix(),
		UpdatedAt: time.Now().Unix(),
	}
	if err := h.knowledgeService.SaveKBCloneProgress(ctx, initialProgress); err != nil {
		logger.Warnf(ctx, "Failed to save initial KB clone progress: %v", err)
		// Don't fail the request, task is already enqueued
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": CopyKnowledgeBaseResponse{
			TaskID:   taskID,
			SourceID: req.SourceID,
			TargetID: req.TargetID,
			Message:  "Knowledge base copy task started",
		},
	})
}

// GetKBCloneProgress godoc
// @Summary      获取知识库复制进度
// @Description  获取知识库复制任务的进度
// @Tags         知识库
// @Accept       json
// @Produce      json
// @Param        task_id  path      string  true  "任务ID"
// @Success      200      {object}  map[string]interface{}  "进度信息"
// @Failure      404      {object}  errors.AppError         "任务不存在"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /knowledge-bases/copy/progress/{task_id} [get]
func (h *KnowledgeBaseHandler) GetKBCloneProgress(c *gin.Context) {
	ctx := c.Request.Context()

	taskID := c.Param("task_id")
	if taskID == "" {
		logger.Error(ctx, "Task ID is empty")
		c.Error(errors.NewBadRequestError("Task ID cannot be empty"))
		return
	}

	progress, err := h.knowledgeService.GetKBCloneProgress(ctx, taskID)
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

// validateExtractConfig validates the graph configuration parameters
func validateExtractConfig(config *types.ExtractConfig) error {
	logger.Errorf(context.Background(), "Validating extract configuration: %+v", config)
	if config == nil {
		return nil
	}
	if !config.Enabled {
		*config = types.ExtractConfig{Enabled: false}
		return nil
	}
	// Validate text field
	if config.Text == "" {
		return errors.NewBadRequestError("text cannot be empty")
	}

	// Validate tags field
	if len(config.Tags) == 0 {
		return errors.NewBadRequestError("tags cannot be empty")
	}
	for i, tag := range config.Tags {
		if tag == "" {
			return errors.NewBadRequestError("tag cannot be empty at index " + strconv.Itoa(i))
		}
	}

	// Validate nodes
	if len(config.Nodes) == 0 {
		return errors.NewBadRequestError("nodes cannot be empty")
	}
	nodeNames := make(map[string]bool)
	for i, node := range config.Nodes {
		if node.Name == "" {
			return errors.NewBadRequestError("node name cannot be empty at index " + strconv.Itoa(i))
		}
		// Check for duplicate node names
		if nodeNames[node.Name] {
			return errors.NewBadRequestError("duplicate node name: " + node.Name)
		}
		nodeNames[node.Name] = true
	}

	if len(config.Relations) == 0 {
		return errors.NewBadRequestError("relations cannot be empty")
	}
	// Validate relations
	for i, relation := range config.Relations {
		if relation.Node1 == "" {
			return errors.NewBadRequestError("relation node1 cannot be empty at index " + strconv.Itoa(i))
		}
		if relation.Node2 == "" {
			return errors.NewBadRequestError("relation node2 cannot be empty at index " + strconv.Itoa(i))
		}
		if relation.Type == "" {
			return errors.NewBadRequestError("relation type cannot be empty at index " + strconv.Itoa(i))
		}
		// Check if referenced nodes exist
		if !nodeNames[relation.Node1] {
			return errors.NewBadRequestError("relation references non-existent node1: " + relation.Node1)
		}
		if !nodeNames[relation.Node2] {
			return errors.NewBadRequestError("relation references non-existent node2: " + relation.Node2)
		}
	}

	return nil
}
