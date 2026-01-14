package handler

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	secutils "github.com/Tencent/WeKnora/internal/utils"
	"github.com/gin-gonic/gin"
)

// KnowledgeHandler processes HTTP requests related to knowledge resources
type KnowledgeHandler struct {
	kgService interfaces.KnowledgeService
	kbService interfaces.KnowledgeBaseService
}

// NewKnowledgeHandler creates a new knowledge handler instance
func NewKnowledgeHandler(
	kgService interfaces.KnowledgeService,
	kbService interfaces.KnowledgeBaseService,
) *KnowledgeHandler {
	return &KnowledgeHandler{kgService: kgService, kbService: kbService}
}

// validateKnowledgeBaseAccess validates access permissions to a knowledge base
// Returns the knowledge base, the knowledge base ID, and any errors encountered
func (h *KnowledgeHandler) validateKnowledgeBaseAccess(c *gin.Context) (*types.KnowledgeBase, string, error) {
	ctx := c.Request.Context()

	// Get knowledge base ID from URL path parameter
	kbID := secutils.SanitizeForLog(c.Param("id"))
	if kbID == "" {
		logger.Error(ctx, "Knowledge base ID is empty")
		return nil, "", errors.NewBadRequestError("Knowledge base ID cannot be empty")
	}

	// Get knowledge base details
	kb, err := h.kbService.GetKnowledgeBaseByID(ctx, kbID)
	if err != nil {
		logger.ErrorWithFields(ctx, err, nil)
		return nil, kbID, errors.NewInternalServerError(err.Error())
	}

	// Verify tenant permissions
	if kb.TenantID != c.GetUint64(types.TenantIDContextKey.String()) {
		logger.Warnf(
			ctx,
			"Permission denied to access this knowledge base, tenant ID mismatch, "+
				"requested tenant ID: %d, knowledge base tenant ID: %d",
			c.GetUint64(types.TenantIDContextKey.String()),
			kb.TenantID,
		)
		return nil, kbID, errors.NewForbiddenError("Permission denied to access this knowledge base")
	}

	return kb, kbID, nil
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

	// Validate access to the knowledge base
	_, kbID, err := h.validateKnowledgeBaseAccess(c)
	if err != nil {
		c.Error(err)
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

	// Create knowledge entry from the file
	knowledge, err := h.kgService.CreateKnowledgeFromFile(ctx, kbID, file, metadata, enableMultimodel, customFileName, tagID)
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
// @Description  从指定URL抓取内容并创建知识条目
// @Tags         知识管理
// @Accept       json
// @Produce      json
// @Param        id       path      string  true  "知识库ID"
// @Param        request  body      object{url=string,enable_multimodel=bool,title=string,tag_id=string}  true  "URL请求"
// @Success      201      {object}  map[string]interface{}  "创建的知识"
// @Failure      400      {object}  errors.AppError         "请求参数错误"
// @Failure      409      {object}  map[string]interface{}  "URL重复"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /knowledge-bases/{id}/knowledge/url [post]
func (h *KnowledgeHandler) CreateKnowledgeFromURL(c *gin.Context) {
	ctx := c.Request.Context()
	logger.Info(ctx, "Start creating knowledge from URL")

	// Validate access to the knowledge base
	_, kbID, err := h.validateKnowledgeBaseAccess(c)
	if err != nil {
		c.Error(err)
		return
	}

	// Parse URL from request body
	var req struct {
		URL              string `json:"url" binding:"required"`
		EnableMultimodel *bool  `json:"enable_multimodel"`
		Title            string `json:"title"`
		TagID            string `json:"tag_id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Error(ctx, "Failed to parse URL request", err)
		c.Error(errors.NewBadRequestError(err.Error()))
		return
	}

	logger.Infof(ctx, "Received URL request: %s", secutils.SanitizeForLog(req.URL))
	logger.Infof(
		ctx,
		"Creating knowledge from URL, knowledge base ID: %s, URL: %s",
		secutils.SanitizeForLog(kbID),
		secutils.SanitizeForLog(req.URL),
	)

	// Create knowledge entry from the URL
	knowledge, err := h.kgService.CreateKnowledgeFromURL(ctx, kbID, req.URL, req.EnableMultimodel, req.Title, req.TagID)
	// Check for duplicate knowledge error
	if err != nil {
		if h.handleDuplicateKnowledgeError(c, err, knowledge, "url") {
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

	_, kbID, err := h.validateKnowledgeBaseAccess(c)
	if err != nil {
		c.Error(err)
		return
	}

	var req types.ManualKnowledgePayload
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Error(ctx, "Failed to parse manual knowledge request", err)
		c.Error(errors.NewBadRequestError(err.Error()))
		return
	}

	knowledge, err := h.kgService.CreateKnowledgeFromManual(ctx, kbID, &req)
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

	// Get knowledge ID from URL path parameter
	id := secutils.SanitizeForLog(c.Param("id"))
	if id == "" {
		logger.Error(ctx, "Knowledge ID is empty")
		c.Error(errors.NewBadRequestError("Knowledge ID cannot be empty"))
		return
	}

	logger.Infof(ctx, "Retrieving knowledge, ID: %s", secutils.SanitizeForLog(id))
	knowledge, err := h.kgService.GetKnowledgeByID(ctx, id)
	if err != nil {
		logger.ErrorWithFields(ctx, err, nil)
		c.Error(errors.NewInternalServerError(err.Error()))
		return
	}

	logger.Infof(
		ctx,
		"Knowledge retrieved successfully, ID: %s, title: %s",
		secutils.SanitizeForLog(knowledge.ID),
		secutils.SanitizeForLog(knowledge.Title),
	)
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

	// Get knowledge base ID from URL path parameter
	kbID := secutils.SanitizeForLog(c.Param("id"))
	if kbID == "" {
		logger.Error(ctx, "Knowledge base ID is empty")
		c.Error(errors.NewBadRequestError("Knowledge base ID cannot be empty"))
		return
	}

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
		"Retrieving knowledge list under knowledge base, knowledge base ID: %s, tag_id: %s, keyword: %s, file_type: %s, page: %d, page size: %d",
		secutils.SanitizeForLog(kbID),
		secutils.SanitizeForLog(tagID),
		secutils.SanitizeForLog(keyword),
		secutils.SanitizeForLog(fileType),
		pagination.Page,
		pagination.PageSize,
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

	// Get knowledge ID from URL path parameter
	id := secutils.SanitizeForLog(c.Param("id"))
	if id == "" {
		logger.Error(ctx, "Knowledge ID is empty")
		c.Error(errors.NewBadRequestError("Knowledge ID cannot be empty"))
		return
	}

	logger.Infof(ctx, "Deleting knowledge, ID: %s", secutils.SanitizeForLog(id))
	err := h.kgService.DeleteKnowledge(ctx, id)
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

	// Get knowledge ID from URL path parameter
	id := secutils.SanitizeForLog(c.Param("id"))
	if id == "" {
		logger.Error(ctx, "Knowledge ID is empty")
		c.Error(errors.NewBadRequestError("Knowledge ID cannot be empty"))
		return
	}

	logger.Infof(ctx, "Retrieving knowledge file, ID: %s", secutils.SanitizeForLog(id))

	// Get file content and filename
	file, filename, err := h.kgService.GetKnowledgeFile(ctx, id)
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
	c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=%s", filename))
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

// GetKnowledgeBatchRequest defines parameters for batch knowledge retrieval
type GetKnowledgeBatchRequest struct {
	IDs []string `form:"ids" binding:"required"` // List of knowledge IDs
}

// GetKnowledgeBatch godoc
// @Summary      批量获取知识
// @Description  根据ID列表批量获取知识条目
// @Tags         知识管理
// @Accept       json
// @Produce      json
// @Param        ids  query     []string  true  "知识ID列表"
// @Success      200  {object}  map[string]interface{}  "知识列表"
// @Failure      400  {object}  errors.AppError         "请求参数错误"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /knowledge/batch [get]
func (h *KnowledgeHandler) GetKnowledgeBatch(c *gin.Context) {
	ctx := c.Request.Context()

	// Get tenant ID from context
	tenantID, ok := c.Get(types.TenantIDContextKey.String())
	if !ok {
		logger.Error(ctx, "Failed to get tenant ID")
		c.Error(errors.NewUnauthorizedError("Unauthorized"))
		return
	}

	// Parse request parameters from query string
	var req GetKnowledgeBatchRequest
	if err := c.ShouldBindQuery(&req); err != nil {
		logger.Error(ctx, "Failed to parse request parameters", err)
		c.Error(errors.NewBadRequestError("Invalid request parameters").WithDetails(err.Error()))
		return
	}

	logger.Infof(
		ctx,
		"Batch retrieving knowledge, tenant ID: %d, number of knowledge IDs: %d",
		tenantID, len(req.IDs),
	)

	// Retrieve knowledge entries in batch
	knowledges, err := h.kgService.GetKnowledgeBatch(ctx, tenantID.(uint64), req.IDs)
	if err != nil {
		logger.ErrorWithFields(ctx, err, nil)
		c.Error(errors.NewInternalServerError("Failed to retrieve knowledge list").WithDetails(err.Error()))
		return
	}

	logger.Infof(
		ctx,
		"Batch knowledge retrieval successful, requested count: %d, returned count: %d",
		len(req.IDs), len(knowledges),
	)

	// Return results
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
	// Get knowledge ID from URL path parameter
	id := secutils.SanitizeForLog(c.Param("id"))
	if id == "" {
		logger.Error(ctx, "Knowledge ID is empty")
		c.Error(errors.NewBadRequestError("Knowledge ID cannot be empty"))
		return
	}

	var knowledge types.Knowledge
	if err := c.ShouldBindJSON(&knowledge); err != nil {
		logger.Error(ctx, "Failed to parse request parameters", err)
		c.Error(errors.NewBadRequestError(err.Error()))
		return
	}

	if err := h.kgService.UpdateKnowledge(ctx, &knowledge); err != nil {
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

	var req types.ManualKnowledgePayload
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Error(ctx, "Failed to parse manual knowledge update request", err)
		c.Error(errors.NewBadRequestError(err.Error()))
		return
	}

	knowledge, err := h.kgService.UpdateManualKnowledge(ctx, id, &req)
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

type knowledgeTagBatchRequest struct {
	Updates map[string]*string `json:"updates" binding:"required,min=1"`
}

// UpdateKnowledgeTagBatch godoc
// @Summary      批量更新知识标签
// @Description  批量更新知识条目的标签
// @Tags         知识管理
// @Accept       json
// @Produce      json
// @Param        request  body      object  true  "标签更新请求"
// @Success      200      {object}  map[string]interface{}  "更新成功"
// @Failure      400      {object}  errors.AppError         "请求参数错误"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /knowledge/tags [put]
func (h *KnowledgeHandler) UpdateKnowledgeTagBatch(c *gin.Context) {
	ctx := c.Request.Context()
	var req knowledgeTagBatchRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Error(ctx, "Failed to parse knowledge tag batch request", err)
		c.Error(errors.NewBadRequestError("请求参数不合法").WithDetails(err.Error()))
		return
	}
	if err := h.kgService.UpdateKnowledgeTagBatch(ctx, req.Updates); err != nil {
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

	// Get knowledge ID from URL path parameter
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

	var request struct {
		ImageInfo string `json:"image_info"`
	}

	if err := c.ShouldBindJSON(&request); err != nil {
		logger.Error(ctx, "Failed to parse request parameters", err)
		c.Error(errors.NewBadRequestError(err.Error()))
		return
	}

	// Update chunk properties
	logger.Infof(ctx, "Updating knowledge chunk, knowledge ID: %s, chunk ID: %s", id, chunkID)
	err := h.kgService.UpdateImageInfo(ctx, id, chunkID, secutils.SanitizeForLog(request.ImageInfo))
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
// @Description  Search knowledge files by keyword across all knowledge bases
// @Tags         Knowledge
// @Accept       json
// @Produce      json
// @Param        keyword     query     string  false "Keyword to search"
// @Param        offset      query     int     false "Offset for pagination"
// @Param        limit       query     int     false "Limit for pagination (default 20)"
// @Param        file_types  query     string  false "Comma-separated file extensions to filter (e.g., csv,xlsx)"
// @Success      200         {object}  map[string]interface{}     "Search results"
// @Failure      400         {object}  errors.AppError            "Invalid request"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /knowledge/search [get]
func (h *KnowledgeHandler) SearchKnowledge(c *gin.Context) {
	ctx := c.Request.Context()
	keyword := c.Query("keyword")
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))

	// Parse file_types parameter (comma-separated)
	var fileTypes []string
	if fileTypesStr := c.Query("file_types"); fileTypesStr != "" {
		for _, ft := range strings.Split(fileTypesStr, ",") {
			ft = strings.TrimSpace(ft)
			if ft != "" {
				fileTypes = append(fileTypes, ft)
			}
		}
	}

	// Retrieve knowledge entries (empty keyword returns recent files)
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
