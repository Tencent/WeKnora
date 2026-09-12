package handler

import (
	stderrors "errors"
	"mime"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/application/service"
	"github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	secutils "github.com/Tencent/WeKnora/internal/utils"
	"github.com/gin-gonic/gin"
)

// EvaluationHandler handles evaluation related HTTP requests
type EvaluationHandler struct {
	evaluationService interfaces.EvaluationService // Service for evaluation operations
}

// NewEvaluationHandler creates a new EvaluationHandler instance
func NewEvaluationHandler(evaluationService interfaces.EvaluationService) *EvaluationHandler {
	return &EvaluationHandler{evaluationService: evaluationService}
}

// ListEvaluationMetrics godoc
// @Summary      列出版本化评测指标
// @Description  返回指标 key、版本、类别、默认配置和配置 schema
// @Tags         评估
// @Produce      json
// @Success      200  {object}  map[string]interface{}  "指标目录"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /evaluation/metrics [get]
func (e *EvaluationHandler) ListEvaluationMetrics(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    gin.H{"items": e.evaluationService.EvaluationMetricDefinitions()},
	})
}

// EvaluationRequest contains parameters for evaluation request
type EvaluationRequest struct {
	DatasetID       string `json:"dataset_id"`        // ID of dataset to evaluate
	KnowledgeBaseID string `json:"knowledge_base_id"` // ID of knowledge base to use
	ChatModelID     string `json:"chat_id"`           // ID of chat model to use
	RerankModelID   string `json:"rerank_id"`         // ID of rerank model to use

	// DatasetVersionID optionally pins one immutable dataset version.
	DatasetVersionID string `json:"dataset_version_id,omitempty"`
	// Configuration optionally overrides resolved retrieval/rerank/generation
	// parameters field by field. Omitted fields keep defaults; explicit zero
	// values apply. The resolved configuration enters the experiment snapshot.
	Configuration *types.EvaluationConfigurationOverrides `json:"configuration,omitempty"`
	// Seed distinguishes "not provided" (nil) from an explicit seed=0.
	Seed *int `json:"seed,omitempty"`
}

// Evaluation godoc
// @Summary      执行评估
// @Description  对知识库进行评估测试；configuration 按字段覆盖，省略保留默认值，显式 0 生效
// @Tags         评估
// @Accept       json
// @Produce      json
// @Param        request  body      EvaluationRequest  true  "评估请求参数"
// @Success      200      {object}  map[string]interface{}  "评估任务"
// @Failure      400      {object}  errors.AppError         "请求参数错误"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /evaluation/ [post]
func (e *EvaluationHandler) Evaluation(c *gin.Context) {
	ctx := c.Request.Context()

	logger.Info(ctx, "Start processing evaluation request")

	var request EvaluationRequest
	if err := c.ShouldBind(&request); err != nil {
		logger.Error(ctx, "Failed to parse request parameters", err)
		_ = c.Error(errors.NewBadRequestError("Invalid request parameters").WithDetails(err.Error()))
		return
	}

	tenantID, exists := c.Get(string(types.TenantIDContextKey))
	if !exists {
		logger.Error(ctx, "Failed to get tenant ID")
		_ = c.Error(errors.NewUnauthorizedError("Unauthorized"))
		return
	}

	logger.Infof(ctx, "Executing evaluation, tenant: %v, dataset: %s, knowledge_base: %s, chat: %s, rerank: %s",
		tenantID,
		secutils.SanitizeForLog(request.DatasetID),
		secutils.SanitizeForLog(request.KnowledgeBaseID),
		secutils.SanitizeForLog(request.ChatModelID),
		secutils.SanitizeForLog(request.RerankModelID),
	)

	task, err := e.evaluationService.EvaluationWithOptions(ctx, &types.EvaluationOptions{
		DatasetID:        secutils.SanitizeForLog(request.DatasetID),
		KnowledgeBaseID:  secutils.SanitizeForLog(request.KnowledgeBaseID),
		ChatModelID:      secutils.SanitizeForLog(request.ChatModelID),
		RerankModelID:    secutils.SanitizeForLog(request.RerankModelID),
		DatasetVersionID: secutils.SanitizeForLog(request.DatasetVersionID),
		Seed:             request.Seed,
		Configuration:    request.Configuration,
	})
	if err != nil {
		logger.ErrorWithFields(ctx, err, nil)
		switch {
		case stderrors.Is(err, service.ErrEvaluationSeedUnsupported):
			_ = c.Error(errors.NewUnprocessableEntityError(
				"The requested seed is not supported by the chat model provider").WithDetails(err.Error()))
		case stderrors.Is(err, interfaces.ErrEvaluationDatasetNotFound),
			stderrors.Is(err, interfaces.ErrEvaluationDatasetVersionNotFound):
			_ = c.Error(errors.NewNotFoundError("Evaluation dataset not found").WithDetails(err.Error()))
		case stderrors.Is(err, service.ErrEvaluationSourceKnowledgeBaseNotFound):
			_ = c.Error(errors.NewNotFoundError("Evaluation source knowledge base not found"))
		default:
			_ = c.Error(errors.NewInternalServerError(err.Error()))
		}
		return
	}

	logger.Infof(ctx, "Evaluation task created successfully")
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    task,
	})
}

// GetEvaluationRequest contains parameters for getting evaluation result
type GetEvaluationRequest struct {
	TaskID string `form:"task_id" binding:"required"` // ID of evaluation task
}

// DeleteEvaluation godoc
// @Summary      删除已结束的评估任务
// @Description  软删除终态评估任务；缺失、跨租户与已删除任务返回 204，活动任务返回 409
// @Tags         评估
// @Accept       json
// @Produce      json
// @Param        task_id  path      string  true  "评估任务ID"
// @Success      204      "删除成功或任务本就不存在"
// @Failure      409      {object}  errors.AppError  "任务仍在活动状态"
// @Security     Bearer
// @Router       /evaluation/{task_id} [delete]
func (e *EvaluationHandler) DeleteEvaluation(c *gin.Context) {
	ctx := c.Request.Context()

	taskID := c.Param("task_id")
	if taskID == "" {
		_ = c.Error(errors.NewBadRequestError("task_id is required"))
		return
	}
	logger.Infof(ctx, "Processing evaluation delete request, task ID: %s", secutils.SanitizeForLog(taskID))

	if err := e.evaluationService.DeleteEvaluation(ctx, taskID); err != nil {
		if stderrors.Is(err, interfaces.ErrEvaluationTaskStateConflict) {
			_ = c.Error(errors.NewConflictError("Evaluation task is still active"))
			return
		}
		logger.ErrorWithFields(ctx, err, nil)
		_ = c.Error(errors.NewInternalServerError(err.Error()))
		return
	}

	c.Status(http.StatusNoContent)
}

// ListEvaluationTasks godoc
// @Summary      列出评估任务
// @Description  按 (start_time DESC, id DESC) keyset 分页列出当前租户的评估任务
// @Tags         评估
// @Accept       json
// @Produce      json
// @Param        status     query     int     false  "数值状态筛选"
// @Param        dataset_id query     string  false  "数据集 ID"
// @Param        dataset_version_id query string false "数据集版本 ID"
// @Param        model_id   query     string  false  "冻结实验中使用的模型 ID"
// @Param        started_from query   string  false  "开始时间下界（RFC 3339，含）"
// @Param        started_to query     string  false  "开始时间上界（RFC 3339，不含）"
// @Param        label      query     []string false "标签交集筛选，可重复"
// @Param        page_size  query     int     false  "每页条数（默认 20，最大 100）"
// @Param        cursor     query     string  false  "上一页返回的 next_cursor"
// @Success      200        {object}  map[string]interface{}  "任务页"
// @Failure      400        {object}  errors.AppError         "非法游标或筛选"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /evaluation/tasks [get]
func (e *EvaluationHandler) ListEvaluationTasks(c *gin.Context) {
	ctx := c.Request.Context()

	var input types.EvaluationTaskListInput
	if raw := c.Query("status"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil {
			_ = c.Error(errors.NewBadRequestError("status must be a numeric evaluation status"))
			return
		}
		status := types.EvaluationStatue(value)
		input.Status = &status
	}
	input.DatasetID = strings.TrimSpace(c.Query("dataset_id"))
	input.DatasetVersionID = strings.TrimSpace(c.Query("dataset_version_id"))
	input.ModelID = strings.TrimSpace(c.Query("model_id"))
	if raw := strings.TrimSpace(c.Query("started_from")); raw != "" {
		value, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			_ = c.Error(errors.NewBadRequestError("started_from must be an RFC3339 timestamp"))
			return
		}
		value = value.UTC()
		input.StartedFrom = &value
	}
	if raw := strings.TrimSpace(c.Query("started_to")); raw != "" {
		value, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			_ = c.Error(errors.NewBadRequestError("started_to must be an RFC3339 timestamp"))
			return
		}
		value = value.UTC()
		input.StartedTo = &value
	}
	input.Labels = c.QueryArray("label")
	if raw := c.Query("page_size"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil {
			_ = c.Error(errors.NewBadRequestError("page_size must be numeric"))
			return
		}
		input.PageSize = value
	}
	input.Cursor = c.Query("cursor")

	page, err := e.evaluationService.ListEvaluations(ctx, input)
	if err != nil {
		if stderrors.Is(err, service.ErrEvaluationTaskListInvalidCursor) ||
			stderrors.Is(err, types.ErrEvaluationTaskLabelInvalid) ||
			stderrors.Is(err, types.ErrEvaluationTaskQueryInvalid) {
			_ = c.Error(errors.NewBadRequestError(err.Error()))
			return
		}
		logger.ErrorWithFields(ctx, err, nil)
		_ = c.Error(errors.NewInternalServerError(err.Error()))
		return
	}

	items := make([]*types.EvaluationTask, 0, len(page.Items))
	for _, entity := range page.Items {
		task, err := service.EvaluationTaskEntityToAPITask(entity)
		if err != nil {
			logger.ErrorWithFields(ctx, err, nil)
			_ = c.Error(errors.NewInternalServerError(err.Error()))
			return
		}
		items = append(items, task)
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"items":       items,
			"next_cursor": page.NextCursor,
		},
	})
}

// ReplaceEvaluationTaskLabelsRequest is an atomic full replacement body.
type ReplaceEvaluationTaskLabelsRequest struct {
	Labels []string `json:"labels"`
}

// ReplaceEvaluationTaskLabels godoc
// @Summary      替换评估任务标签
// @Description  在一个事务内全量替换规范化标签，不修改任务版本与更新时间
// @Tags         评估
// @Accept       json
// @Produce      json
// @Param        task_id  path  string                              true  "评估任务ID"
// @Param        request  body  ReplaceEvaluationTaskLabelsRequest true  "标签集合"
// @Success      200      {object} map[string]interface{}           "规范化后的标签"
// @Failure      400      {object} errors.AppError                  "标签非法"
// @Failure      404      {object} errors.AppError                  "任务不存在"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /evaluation/tasks/{task_id}/labels [put]
func (e *EvaluationHandler) ReplaceEvaluationTaskLabels(c *gin.Context) {
	taskID := strings.TrimSpace(c.Param("task_id"))
	if taskID == "" {
		_ = c.Error(errors.NewBadRequestError("task_id is required"))
		return
	}
	var request ReplaceEvaluationTaskLabelsRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		_ = c.Error(errors.NewBadRequestError("Invalid request parameters").WithDetails(err.Error()))
		return
	}
	labels, err := e.evaluationService.ReplaceEvaluationTaskLabels(
		c.Request.Context(),
		taskID,
		request.Labels,
	)
	if err != nil {
		switch {
		case stderrors.Is(err, interfaces.ErrEvaluationTaskNotFound):
			_ = c.Error(errors.NewNotFoundError("Evaluation task not found"))
		case stderrors.Is(err, types.ErrEvaluationTaskLabelInvalid),
			stderrors.Is(err, types.ErrEvaluationTaskQueryInvalid):
			_ = c.Error(errors.NewBadRequestError("Invalid evaluation task labels").WithDetails(err.Error()))
		default:
			logger.ErrorWithFields(c.Request.Context(), err, nil)
			_ = c.Error(errors.NewInternalServerError(err.Error()))
		}
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    gin.H{"task_id": taskID, "labels": labels},
	})
}

// CompareEvaluationTasksRequest selects two to ten runs and an optional baseline.
type CompareEvaluationTasksRequest struct {
	TaskIDs        []string `json:"task_ids"`
	BaselineTaskID string   `json:"baseline_task_id,omitempty"`
}

// CompareEvaluationTasks godoc
// @Summary      对比评估运行
// @Description  返回成功运行的冻结参数差异、指标绝对值和相对基线增减值
// @Tags         评估
// @Accept       json
// @Produce      json
// @Param        request  body      CompareEvaluationTasksRequest true "运行与基线"
// @Success      200      {object}  map[string]interface{}        "对比结果"
// @Failure      400      {object}  errors.AppError               "请求参数错误"
// @Failure      404      {object}  errors.AppError               "运行不存在"
// @Failure      409      {object}  errors.AppError               "运行不可对比"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /evaluation/comparisons [post]
func (e *EvaluationHandler) CompareEvaluationTasks(c *gin.Context) {
	var request CompareEvaluationTasksRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		_ = c.Error(errors.NewBadRequestError("Invalid request parameters").WithDetails(err.Error()))
		return
	}
	response, err := e.evaluationService.CompareEvaluations(
		c.Request.Context(),
		types.EvaluationComparisonRequest{
			TaskIDs: request.TaskIDs, BaselineTaskID: request.BaselineTaskID,
		},
	)
	if err != nil {
		switch {
		case stderrors.Is(err, types.ErrEvaluationComparisonInvalid):
			_ = c.Error(errors.NewBadRequestError("Invalid evaluation comparison request").WithDetails(err.Error()))
		case stderrors.Is(err, types.ErrEvaluationComparisonTaskNotFound):
			_ = c.Error(errors.NewNotFoundError("Evaluation task not found"))
		case stderrors.Is(err, types.ErrEvaluationComparisonConflict):
			_ = c.Error(errors.NewConflictError("Evaluation runs are not comparable").WithDetails(err.Error()))
		default:
			logger.ErrorWithFields(c.Request.Context(), err, nil)
			_ = c.Error(errors.NewInternalServerError(err.Error()))
		}
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": response})
}

// ExportEvaluationTask godoc
// @Summary      导出评估运行
// @Description  将终态任务与逐题事实导出为有界 JSON 或 CSV 文件
// @Tags         评估
// @Produce      application/json,text/csv
// @Param        task_id  path   string true "评估任务ID"
// @Param        format   query  string true "json 或 csv"
// @Success      200      {file} binary "导出文件"
// @Failure      400      {object} errors.AppError "格式错误"
// @Failure      404      {object} errors.AppError "任务不存在"
// @Failure      409      {object} errors.AppError "任务仍在运行"
// @Failure      413      {object} errors.AppError "导出超过边界"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /evaluation/tasks/{task_id}/export [get]
func (e *EvaluationHandler) ExportEvaluationTask(c *gin.Context) {
	prepared, err := e.evaluationService.PrepareEvaluationExport(
		c.Request.Context(),
		c.Param("task_id"),
		c.Query("format"),
	)
	if err != nil {
		switch {
		case stderrors.Is(err, types.ErrEvaluationExportFormatInvalid):
			_ = c.Error(errors.NewBadRequestError("Invalid evaluation export format").WithDetails(err.Error()))
		case stderrors.Is(err, interfaces.ErrEvaluationTaskNotFound):
			_ = c.Error(errors.NewNotFoundError("Evaluation task not found"))
		case stderrors.Is(err, types.ErrEvaluationExportTaskConflict):
			_ = c.Error(errors.NewConflictError("Evaluation task is not terminal").WithDetails(err.Error()))
		case stderrors.Is(err, types.ErrEvaluationExportLimitExceeded):
			requestError := errors.NewRequestEntityTooLargeError(
				"Evaluation export exceeds the configured limits",
			).WithDetails(err.Error())
			_ = c.Error(requestError)
		default:
			logger.ErrorWithFields(c.Request.Context(), err, nil)
			_ = c.Error(errors.NewInternalServerError(err.Error()))
		}
		return
	}
	if prepared == nil || prepared.Path == "" {
		_ = c.Error(errors.NewInternalServerError("Evaluation export preparation returned no file"))
		return
	}
	defer func() { _ = os.Remove(prepared.Path) }()
	file, err := os.Open(prepared.Path)
	if err != nil {
		_ = c.Error(errors.NewInternalServerError("Failed to open prepared evaluation export"))
		return
	}
	defer func() { _ = file.Close() }()
	headers := map[string]string{
		"Content-Disposition": mime.FormatMediaType("attachment", map[string]string{
			"filename": prepared.Filename,
		}),
	}
	c.DataFromReader(http.StatusOK, prepared.Size, prepared.ContentType, file, headers)
}

// CancelEvaluation godoc
// @Summary      取消评估任务
// @Description  持久化取消请求；运行实例处理取消并完成资源清理后任务进入 Canceled
// @Tags         评估
// @Accept       json
// @Produce      json
// @Param        task_id  path      string  true  "评估任务ID"
// @Success      200      {object}  map[string]interface{}  "当前任务状态"
// @Failure      404      {object}  errors.AppError         "任务不存在或属于其他租户"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /evaluation/{task_id}/cancel [post]
func (e *EvaluationHandler) CancelEvaluation(c *gin.Context) {
	ctx := c.Request.Context()

	taskID := c.Param("task_id")
	if taskID == "" {
		_ = c.Error(errors.NewBadRequestError("task_id is required"))
		return
	}
	logger.Infof(ctx, "Processing evaluation cancel request, task ID: %s", secutils.SanitizeForLog(taskID))

	result, err := e.evaluationService.CancelEvaluation(ctx, taskID)
	if err != nil {
		if stderrors.Is(err, interfaces.ErrEvaluationTaskNotFound) {
			_ = c.Error(errors.NewNotFoundError("Evaluation task not found"))
			return
		}
		logger.ErrorWithFields(ctx, err, nil)
		_ = c.Error(errors.NewInternalServerError(err.Error()))
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    result,
	})
}

// GetEvaluationResult godoc
// @Summary      获取评估结果
// @Description  根据任务ID获取评估结果
// @Tags         评估
// @Accept       json
// @Produce      json
// @Param        task_id  query     string  true  "评估任务ID"
// @Success      200      {object}  map[string]interface{}  "评估结果"
// @Failure      400      {object}  errors.AppError         "请求参数错误"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /evaluation/ [get]
func (e *EvaluationHandler) GetEvaluationResult(c *gin.Context) {
	ctx := c.Request.Context()

	logger.Info(ctx, "Start retrieving evaluation result")

	var request GetEvaluationRequest
	if err := c.ShouldBind(&request); err != nil {
		logger.Error(ctx, "Failed to parse request parameters", err)
		_ = c.Error(errors.NewBadRequestError("Invalid request parameters").WithDetails(err.Error()))
		return
	}

	result, err := e.evaluationService.EvaluationResult(ctx, secutils.SanitizeForLog(request.TaskID))
	if err != nil {
		if stderrors.Is(err, interfaces.ErrEvaluationTaskNotFound) {
			_ = c.Error(errors.NewNotFoundError("Evaluation task not found"))
			return
		}
		logger.ErrorWithFields(ctx, err, nil)
		_ = c.Error(errors.NewInternalServerError(err.Error()))
		return
	}

	logger.Info(ctx, "Retrieved evaluation result successfully")
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    result,
	})
}
