package handler

import (
	"net/http"

	"github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
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

// Evaluation handles evaluation request
func (e *EvaluationHandler) Evaluation(c *gin.Context) {
	ctx := c.Request.Context()

	logger.Info(ctx, "Start processing evaluation request")

	var request *EvaluationRequest
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
		tenantID, request.DatasetID, request.KnowledgeBaseID, request.ChatModelID, request.RerankModelID)

	task, err := e.evaluationService.Evaluation(ctx,
		request.DatasetID,
		request.KnowledgeBaseID,
		request.ChatModelID,
		request.RerankModelID,
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

// GetEvaluationResult retrieves evaluation result by task ID
func (e *EvaluationHandler) GetEvaluationResult(c *gin.Context) {
	ctx := c.Request.Context()

	logger.Info(ctx, "Start retrieving evaluation result")

	var request *GetEvaluationRequest
	if err := c.ShouldBind(&request); err != nil {
		logger.Error(ctx, "Failed to parse request parameters", err)
		c.Error(errors.NewBadRequestError("Invalid request parameters").WithDetails(err.Error()))
		return
	}

	logger.Infof(ctx, "Retrieving evaluation result, task ID: %s", request.TaskID)

	result, err := e.evaluationService.EvaluationResult(ctx, request.TaskID)
	if err != nil {
		logger.ErrorWithFields(ctx, err, nil)
		c.Error(errors.NewInternalServerError(err.Error()))
		return
	}

	logger.Infof(ctx, "Retrieved evaluation result successfully, task ID: %s", request.TaskID)
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    result,
	})
}
