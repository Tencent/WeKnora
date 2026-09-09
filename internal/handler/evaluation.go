package handler

import (
	"fmt"
	"net/http"
	"strconv"
	"time"

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

// EvaluationRequest contains parameters for evaluation request
type EvaluationRequest struct {
	DatasetID       string `json:"dataset_id"`        // ID of dataset to evaluate
	KnowledgeBaseID string `json:"knowledge_base_id"` // ID of knowledge base to use
	ChatModelID     string `json:"chat_id"`           // ID of chat model to use
	RerankModelID   string `json:"rerank_id"`         // ID of rerank model to use
}

// Evaluation godoc
// @Summary      执行评估
// @Description  对知识库进行评估测试
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
		c.Error(errors.NewBadRequestError("Invalid request parameters").WithDetails(err.Error()))
		return
	}

	tenantID, exists := c.Get(string(types.TenantIDContextKey))
	if !exists {
		logger.Error(ctx, "Failed to get tenant ID")
		c.Error(errors.NewUnauthorizedError("Unauthorized"))
		return
	}

	logger.Infof(ctx, "Executing evaluation, tenant: %v, dataset: %s, knowledge_base: %s, chat: %s, rerank: %s",
		tenantID,
		secutils.SanitizeForLog(request.DatasetID),
		secutils.SanitizeForLog(request.KnowledgeBaseID),
		secutils.SanitizeForLog(request.ChatModelID),
		secutils.SanitizeForLog(request.RerankModelID),
	)

	task, err := e.evaluationService.Evaluation(ctx,
		secutils.SanitizeForLog(request.DatasetID),
		secutils.SanitizeForLog(request.KnowledgeBaseID),
		secutils.SanitizeForLog(request.ChatModelID),
		secutils.SanitizeForLog(request.RerankModelID),
	)
	if err != nil {
		logger.ErrorWithFields(ctx, err, nil)
		c.Error(errors.NewInternalServerError(err.Error()))
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
		c.Error(errors.NewBadRequestError("Invalid request parameters").WithDetails(err.Error()))
		return
	}

	result, err := e.evaluationService.EvaluationResult(ctx, secutils.SanitizeForLog(request.TaskID))
	if err != nil {
		logger.ErrorWithFields(ctx, err, nil)
		c.Error(errors.NewInternalServerError(err.Error()))
		return
	}

	logger.Info(ctx, "Retrieved evaluation result successfully")
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    result,
	})
}

// GetEvaluationEvidence returns a deterministic report whose SHA-256 can be
// recomputed after clearing report_sha256. It contains no prompt or answer text.
// @Summary      导出评测证据
// @Description  导出当前租户指定评测任务的可审计、无正文证据包
// @Tags         评估
// @Produce      json
// @Param        task_id  query  string  true  "评估任务ID"
// @Success      200  {object}  types.EvaluationEvidenceReport
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /evaluation/evidence [get]
func (e *EvaluationHandler) GetEvaluationEvidence(c *gin.Context) {
	var request GetEvaluationRequest
	if err := c.ShouldBind(&request); err != nil {
		c.Error(errors.NewBadRequestError("Invalid request parameters").WithDetails(err.Error()))
		return
	}
	report, err := e.evaluationService.EvaluationEvidence(
		c.Request.Context(), secutils.SanitizeForLog(request.TaskID),
	)
	if err != nil {
		logger.ErrorWithFields(c.Request.Context(), err, nil)
		c.Error(errors.NewInternalServerError(err.Error()))
		return
	}
	c.Header("Content-Disposition", "attachment; filename=evaluation-evidence.json")
	c.JSON(http.StatusOK, report)
}

// GetModelUsage returns tenant-scoped usage across evaluation, chat, Wiki, and
// background model calls. Prompt and response bodies are never returned.
// @Summary      获取模型用量
// @Description  按模型和可选时间区间聚合当前租户的模型调用量、Token、缓存、耗时和成本
// @Tags         评估
// @Produce      json
// @Param        start_time  query  string  false  "开始时间（RFC3339，含）"
// @Param        end_time    query  string  false  "结束时间（RFC3339，含）"
// @Success      200  {object}  map[string]interface{}
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /evaluation/model-usage [get]
func (e *EvaluationHandler) GetModelUsage(c *gin.Context) {
	ctx := c.Request.Context()
	startTime, err := parseOptionalRFC3339(c.Query("start_time"))
	if err != nil {
		c.Error(errors.NewBadRequestError("Invalid start_time").WithDetails(err.Error()))
		return
	}
	endTime, err := parseOptionalRFC3339(c.Query("end_time"))
	if err != nil {
		c.Error(errors.NewBadRequestError("Invalid end_time").WithDetails(err.Error()))
		return
	}
	if startTime != nil && endTime != nil && startTime.After(*endTime) {
		c.Error(errors.NewBadRequestError("start_time must not be after end_time"))
		return
	}
	stats, err := e.evaluationService.ModelUsage(ctx, startTime, endTime)
	if err != nil {
		logger.ErrorWithFields(ctx, err, nil)
		c.Error(errors.NewInternalServerError(err.Error()))
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": stats})
}

// GetEvaluationDatasets lists manifest-backed datasets and their readiness.
// @Summary      获取评测数据集
// @Description  列出可选择的数据集及其语言、场景、覆盖维度和文件完整性
// @Tags         评估
// @Produce      json
// @Success      200  {object}  map[string]interface{}
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /evaluation/datasets [get]
func (e *EvaluationHandler) GetEvaluationDatasets(c *gin.Context) {
	datasets, err := e.evaluationService.EvaluationDatasets(c.Request.Context())
	if err != nil {
		logger.ErrorWithFields(c.Request.Context(), err, nil)
		c.Error(errors.NewInternalServerError(err.Error()))
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": datasets})
}

// GetEvaluationRuns returns a tenant-scoped page of evaluation history.
// @Summary      获取评测历史
// @Description  按开始时间倒序列出当前租户的任务、指标、运行快照和聚合用量
// @Tags         评估
// @Produce      json
// @Param        limit   query  int  false  "每页数量（1-100，默认 20）"
// @Param        offset  query  int  false  "偏移量（默认 0）"
// @Success      200  {object}  map[string]interface{}
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /evaluation/runs [get]
func (e *EvaluationHandler) GetEvaluationRuns(c *gin.Context) {
	limit, err := boundedQueryInt(c, "limit", 20, 1, 100)
	if err != nil {
		c.Error(errors.NewBadRequestError("Invalid limit").WithDetails(err.Error()))
		return
	}
	offset, err := boundedQueryInt(c, "offset", 0, 0, 1_000_000)
	if err != nil {
		c.Error(errors.NewBadRequestError("Invalid offset").WithDetails(err.Error()))
		return
	}
	page, err := e.evaluationService.EvaluationRuns(c.Request.Context(), limit, offset)
	if err != nil {
		logger.ErrorWithFields(c.Request.Context(), err, nil)
		c.Error(errors.NewInternalServerError(err.Error()))
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": page})
}

func boundedQueryInt(c *gin.Context, name string, fallback, minimum, maximum int) (int, error) {
	raw := c.Query(name)
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < minimum || value > maximum {
		return 0, fmt.Errorf("%s must be an integer between %d and %d", name, minimum, maximum)
	}
	return value, nil
}

func parseOptionalRFC3339(raw string) (*time.Time, error) {
	if raw == "" {
		return nil, nil
	}
	parsed, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return nil, err
	}
	parsed = parsed.UTC()
	return &parsed, nil
}
