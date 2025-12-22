package handler

import (
	"net/http"

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
