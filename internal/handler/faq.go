package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	secutils "github.com/Tencent/WeKnora/internal/utils"
)

// FAQHandler handles FAQ knowledge base operations.
type FAQHandler struct {
	knowledgeService interfaces.KnowledgeService
}

// NewFAQHandler creates a new FAQ handler
func NewFAQHandler(knowledgeService interfaces.KnowledgeService) *FAQHandler {
	return &FAQHandler{knowledgeService: knowledgeService}
}

// ListEntries godoc
// @Summary      获取FAQ条目列表
// @Description  获取知识库下的FAQ条目列表，支持分页和筛选
// @Tags         FAQ管理
// @Accept       json
// @Produce      json
// @Param        id           path      string  true   "知识库ID"
// @Param        page         query     int     false  "页码"
// @Param        page_size    query     int     false  "每页数量"
// @Param        tag_id       query     string  false  "标签ID筛选"
// @Param        keyword      query     string  false  "关键词搜索"
// @Param        search_field query     string  false  "搜索字段: standard_question(标准问题), similar_questions(相似问法), answers(答案), 默认搜索全部"
// @Param        sort_order   query     string  false  "排序方式: asc(按更新时间正序), 默认按更新时间倒序"
// @Success      200        {object}  map[string]interface{}  "FAQ列表"
// @Failure      400        {object}  errors.AppError         "请求参数错误"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /knowledge-bases/{id}/faq/entries [get]
func (h *FAQHandler) ListEntries(c *gin.Context) {
	ctx := c.Request.Context()
	var page types.Pagination
	if err := c.ShouldBindQuery(&page); err != nil {
		logger.Error(ctx, "Failed to bind pagination query", err)
		c.Error(errors.NewBadRequestError("分页参数不合法").WithDetails(err.Error()))
		return
	}

	tagID := secutils.SanitizeForLog(c.Query("tag_id"))
	keyword := secutils.SanitizeForLog(c.Query("keyword"))
	searchField := secutils.SanitizeForLog(c.Query("search_field"))
	sortOrder := secutils.SanitizeForLog(c.Query("sort_order"))

	result, err := h.knowledgeService.ListFAQEntries(ctx, secutils.SanitizeForLog(c.Param("id")), &page, tagID, keyword, searchField, sortOrder)
	if err != nil {
		logger.ErrorWithFields(ctx, err, nil)
		c.Error(err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    result,
	})
}

// UpsertEntries godoc
// @Summary      批量更新/插入FAQ条目
// @Description  异步批量更新或插入FAQ条目
// @Tags         FAQ管理
// @Accept       json
// @Produce      json
// @Param        id       path      string                    true  "知识库ID"
// @Param        request  body      types.FAQBatchUpsertPayload  true  "批量操作请求"
// @Success      200      {object}  map[string]interface{}    "任务ID"
// @Failure      400      {object}  errors.AppError           "请求参数错误"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /knowledge-bases/{id}/faq/entries [post]
func (h *FAQHandler) UpsertEntries(c *gin.Context) {
	ctx := c.Request.Context()
	var req types.FAQBatchUpsertPayload
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Error(ctx, "Failed to bind FAQ upsert payload", err)
		c.Error(errors.NewBadRequestError("请求参数不合法").WithDetails(err.Error()))
		return
	}

	taskID, err := h.knowledgeService.UpsertFAQEntries(ctx, secutils.SanitizeForLog(c.Param("id")), &req)
	if err != nil {
		logger.ErrorWithFields(ctx, err, nil)
		c.Error(err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"task_id": taskID,
		},
	})
}

// CreateEntry godoc
// @Summary      创建单个FAQ条目
// @Description  同步创建单个FAQ条目
// @Tags         FAQ管理
// @Accept       json
// @Produce      json
// @Param        id       path      string                true  "知识库ID"
// @Param        request  body      types.FAQEntryPayload true  "FAQ条目"
// @Success      200      {object}  map[string]interface{}  "创建的FAQ条目"
// @Failure      400      {object}  errors.AppError         "请求参数错误"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /knowledge-bases/{id}/faq/entry [post]
func (h *FAQHandler) CreateEntry(c *gin.Context) {
	ctx := c.Request.Context()
	var req types.FAQEntryPayload
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Error(ctx, "Failed to bind FAQ entry payload", err)
		c.Error(errors.NewBadRequestError("请求参数不合法").WithDetails(err.Error()))
		return
	}

	entry, err := h.knowledgeService.CreateFAQEntry(ctx, secutils.SanitizeForLog(c.Param("id")), &req)
	if err != nil {
		logger.ErrorWithFields(ctx, err, nil)
		c.Error(err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    entry,
	})
}

// UpdateEntry godoc
// @Summary      更新FAQ条目
// @Description  更新指定的FAQ条目
// @Tags         FAQ管理
// @Accept       json
// @Produce      json
// @Param        id        path      string                true  "知识库ID"
// @Param        entry_id  path      string                true  "FAQ条目ID"
// @Param        request   body      types.FAQEntryPayload true  "FAQ条目"
// @Success      200       {object}  map[string]interface{}  "更新成功"
// @Failure      400       {object}  errors.AppError         "请求参数错误"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /knowledge-bases/{id}/faq/entries/{entry_id} [put]
func (h *FAQHandler) UpdateEntry(c *gin.Context) {
	ctx := c.Request.Context()
	var req types.FAQEntryPayload
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Error(ctx, "Failed to bind FAQ entry payload", err)
		c.Error(errors.NewBadRequestError("请求参数不合法").WithDetails(err.Error()))
		return
	}

	if err := h.knowledgeService.UpdateFAQEntry(ctx,
		secutils.SanitizeForLog(c.Param("id")), secutils.SanitizeForLog(c.Param("entry_id")), &req); err != nil {
		logger.ErrorWithFields(ctx, err, nil)
		c.Error(err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
	})
}

// UpdateEntryTagBatch godoc
// @Summary      批量更新FAQ标签
// @Description  批量更新FAQ条目的标签
// @Tags         FAQ管理
// @Accept       json
// @Produce      json
// @Param        id       path      string  true  "知识库ID"
// @Param        request  body      object  true  "标签更新请求"
// @Success      200      {object}  map[string]interface{}  "更新成功"
// @Failure      400      {object}  errors.AppError         "请求参数错误"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /knowledge-bases/{id}/faq/entries/tags [put]
func (h *FAQHandler) UpdateEntryTagBatch(c *gin.Context) {
	ctx := c.Request.Context()
	var req faqEntryTagBatchRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Error(ctx, "Failed to bind FAQ entry tag batch payload", err)
		c.Error(errors.NewBadRequestError("请求参数不合法").WithDetails(err.Error()))
		return
	}
	if err := h.knowledgeService.UpdateFAQEntryTagBatch(ctx,
		secutils.SanitizeForLog(c.Param("id")), req.Updates); err != nil {
		logger.ErrorWithFields(ctx, err, nil)
		c.Error(err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
	})
}

// UpdateEntryFieldsBatch godoc
// @Summary      批量更新FAQ字段
// @Description  批量更新FAQ条目的多个字段（is_enabled, is_recommended, tag_id）
// @Tags         FAQ管理
// @Accept       json
// @Produce      json
// @Param        id       path      string                        true  "知识库ID"
// @Param        request  body      types.FAQEntryFieldsBatchUpdate  true  "字段更新请求"
// @Success      200      {object}  map[string]interface{}        "更新成功"
// @Failure      400      {object}  errors.AppError               "请求参数错误"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /knowledge-bases/{id}/faq/entries/fields [put]
func (h *FAQHandler) UpdateEntryFieldsBatch(c *gin.Context) {
	ctx := c.Request.Context()
	var req types.FAQEntryFieldsBatchUpdate
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Error(ctx, "Failed to bind FAQ entry fields batch payload", err)
		c.Error(errors.NewBadRequestError("请求参数不合法").WithDetails(err.Error()))
		return
	}
	if err := h.knowledgeService.UpdateFAQEntryFieldsBatch(ctx,
		secutils.SanitizeForLog(c.Param("id")), &req); err != nil {
		logger.ErrorWithFields(ctx, err, nil)
		c.Error(err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
	})
}

// faqDeleteRequest is a request for deleting FAQ entries in batch
type faqDeleteRequest struct {
	IDs []string `json:"ids" binding:"required,min=1,dive,required"`
}

// faqEntryTagBatchRequest is a request for updating tags for FAQ entries in batch
type faqEntryTagBatchRequest struct {
	Updates map[string]*string `json:"updates" binding:"required,min=1"`
}

// DeleteEntries godoc
// @Summary      批量删除FAQ条目
// @Description  批量删除指定的FAQ条目
// @Tags         FAQ管理
// @Accept       json
// @Produce      json
// @Param        id       path      string  true  "知识库ID"
// @Param        request  body      object{ids=[]string}  true  "要删除的FAQ ID列表"
// @Success      200      {object}  map[string]interface{}  "删除成功"
// @Failure      400      {object}  errors.AppError         "请求参数错误"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /knowledge-bases/{id}/faq/entries [delete]
func (h *FAQHandler) DeleteEntries(c *gin.Context) {
	ctx := c.Request.Context()
	var req faqDeleteRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Errorf(ctx, "Failed to bind FAQ delete payload: %s", secutils.SanitizeForLog(err.Error()))
		c.Error(errors.NewBadRequestError("请求参数不合法").WithDetails(err.Error()))
		return
	}

	if err := h.knowledgeService.DeleteFAQEntries(ctx,
		secutils.SanitizeForLog(c.Param("id")),
		secutils.SanitizeForLogArray(req.IDs)); err != nil {
		logger.ErrorWithFields(ctx, err, nil)
		c.Error(err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
	})
}

// SearchFAQ godoc
// @Summary      搜索FAQ
// @Description  使用混合搜索在FAQ中搜索，支持两级优先级标签召回：first_priority_tag_ids优先级最高，second_priority_tag_ids次之
// @Tags         FAQ管理
// @Accept       json
// @Produce      json
// @Param        id       path      string                true  "知识库ID"
// @Param        request  body      types.FAQSearchRequest  true  "搜索请求"
// @Success      200      {object}  map[string]interface{}  "搜索结果"
// @Failure      400      {object}  errors.AppError         "请求参数错误"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /knowledge-bases/{id}/faq/search [post]
func (h *FAQHandler) SearchFAQ(c *gin.Context) {
	ctx := c.Request.Context()
	var req types.FAQSearchRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Error(ctx, "Failed to bind FAQ search payload", err)
		c.Error(errors.NewBadRequestError("请求参数不合法").WithDetails(err.Error()))
		return
	}
	req.QueryText = secutils.SanitizeForLog(req.QueryText)
	if req.MatchCount <= 0 {
		req.MatchCount = 10
	}
	if req.MatchCount > 200 {
		req.MatchCount = 200
	}
	entries, err := h.knowledgeService.SearchFAQEntries(ctx, secutils.SanitizeForLog(c.Param("id")), &req)
	if err != nil {
		logger.ErrorWithFields(ctx, err, nil)
		c.Error(err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    entries,
	})
}

// ExportEntries godoc
// @Summary      导出FAQ条目
// @Description  将所有FAQ条目导出为CSV文件
// @Tags         FAQ管理
// @Accept       json
// @Produce      text/csv
// @Param        id   path      string  true  "知识库ID"
// @Success      200  {file}    file    "CSV文件"
// @Failure      400  {object}  errors.AppError  "请求参数错误"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /knowledge-bases/{id}/faq/entries/export [get]
func (h *FAQHandler) ExportEntries(c *gin.Context) {
	ctx := c.Request.Context()
	kbID := secutils.SanitizeForLog(c.Param("id"))

	csvData, err := h.knowledgeService.ExportFAQEntries(ctx, kbID)
	if err != nil {
		logger.ErrorWithFields(ctx, err, nil)
		c.Error(err)
		return
	}

	// Set response headers for CSV download
	c.Header("Content-Type", "text/csv; charset=utf-8")
	c.Header("Content-Disposition", "attachment; filename=faq_export.csv")
	// Add BOM for Excel compatibility with UTF-8
	bom := []byte{0xEF, 0xBB, 0xBF}
	c.Data(http.StatusOK, "text/csv; charset=utf-8", append(bom, csvData...))
}

// GetEntry godoc
// @Summary      获取FAQ条目详情
// @Description  根据ID获取单个FAQ条目的详情
// @Tags         FAQ管理
// @Accept       json
// @Produce      json
// @Param        id        path      string  true  "知识库ID"
// @Param        entry_id  path      string  true  "FAQ条目ID"
// @Success      200       {object}  map[string]interface{}  "FAQ条目详情"
// @Failure      400       {object}  errors.AppError         "请求参数错误"
// @Failure      404       {object}  errors.AppError         "条目不存在"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /knowledge-bases/{id}/faq/entries/{entry_id} [get]
func (h *FAQHandler) GetEntry(c *gin.Context) {
	ctx := c.Request.Context()
	kbID := secutils.SanitizeForLog(c.Param("id"))
	entryID := secutils.SanitizeForLog(c.Param("entry_id"))

	entry, err := h.knowledgeService.GetFAQEntry(ctx, kbID, entryID)
	if err != nil {
		logger.ErrorWithFields(ctx, err, nil)
		c.Error(err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    entry,
	})
}

// GetImportProgress godoc
// @Summary      获取FAQ导入进度
// @Description  获取FAQ导入任务的进度
// @Tags         FAQ管理
// @Accept       json
// @Produce      json
// @Param        task_id  path      string  true  "任务ID"
// @Success      200      {object}  map[string]interface{}  "导入进度"
// @Failure      404      {object}  errors.AppError         "任务不存在"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /faq/import/progress/{task_id} [get]
func (h *FAQHandler) GetImportProgress(c *gin.Context) {
	ctx := c.Request.Context()
	taskID := secutils.SanitizeForLog(c.Param("task_id"))

	progress, err := h.knowledgeService.GetFAQImportProgress(ctx, taskID)
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
