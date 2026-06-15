package handler

import (
	stderrors "errors"
	"net/http"

	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type EvaluationHandler struct{ evaluationService interfaces.EvaluationService }

func NewEvaluationHandler(service interfaces.EvaluationService) *EvaluationHandler {
	return &EvaluationHandler{evaluationService: service}
}
func evaluationError(c *gin.Context, err error) {
	if stderrors.Is(err, gorm.ErrRecordNotFound) {
		_ = c.Error(apperrors.NewNotFoundError("evaluation resource not found"))
		return
	}
	_ = c.Error(apperrors.NewInternalServerError(err.Error()))
}
func evaluationBadRequest(c *gin.Context, err error) {
	_ = c.Error(apperrors.NewBadRequestError(err.Error()))
}
func evaluationOK(c *gin.Context, data interface{}) {
	c.JSON(http.StatusOK, gin.H{"success": true, "data": data})
}

func (e *EvaluationHandler) ListMetrics(c *gin.Context) {
	evaluationOK(c, e.evaluationService.ListMetrics(c.Request.Context()))
}
func (e *EvaluationHandler) CreateDataset(c *gin.Context) {
	var req types.CreateEvaluationDatasetRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		evaluationBadRequest(c, err)
		return
	}
	d, err := e.evaluationService.CreateDataset(c.Request.Context(), &req)
	if err != nil {
		evaluationBadRequest(c, err)
		return
	}
	evaluationOK(c, d)
}
func (e *EvaluationHandler) GetDataset(c *gin.Context) {
	d, err := e.evaluationService.GetDataset(c.Request.Context(), c.Param("dataset_id"))
	if err != nil {
		evaluationError(c, err)
		return
	}
	evaluationOK(c, d)
}
func (e *EvaluationHandler) ListDatasets(c *gin.Context) {
	var page types.Pagination
	if err := c.ShouldBindQuery(&page); err != nil {
		evaluationBadRequest(c, err)
		return
	}
	result, err := e.evaluationService.ListDatasets(c.Request.Context(), &page)
	if err != nil {
		evaluationError(c, err)
		return
	}
	evaluationOK(c, result)
}
func (e *EvaluationHandler) UpdateDataset(c *gin.Context) {
	var req types.UpdateEvaluationDatasetRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		evaluationBadRequest(c, err)
		return
	}
	d, err := e.evaluationService.UpdateDataset(c.Request.Context(), c.Param("dataset_id"), &req)
	if err != nil {
		evaluationBadRequest(c, err)
		return
	}
	evaluationOK(c, d)
}
func (e *EvaluationHandler) DeleteDataset(c *gin.Context) {
	if err := e.evaluationService.DeleteDataset(c.Request.Context(), c.Param("dataset_id")); err != nil {
		evaluationError(c, err)
		return
	}
	evaluationOK(c, gin.H{})
}
func (e *EvaluationHandler) CreateSample(c *gin.Context) {
	var req types.CreateEvaluationSampleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		evaluationBadRequest(c, err)
		return
	}
	s, err := e.evaluationService.CreateSample(c.Request.Context(), c.Param("dataset_id"), &req)
	if err != nil {
		evaluationBadRequest(c, err)
		return
	}
	evaluationOK(c, s)
}
func (e *EvaluationHandler) ListSamples(c *gin.Context) {
	var page types.Pagination
	if err := c.ShouldBindQuery(&page); err != nil {
		evaluationBadRequest(c, err)
		return
	}
	result, err := e.evaluationService.ListSamples(c.Request.Context(), c.Param("dataset_id"), &page)
	if err != nil {
		evaluationError(c, err)
		return
	}
	evaluationOK(c, result)
}
func (e *EvaluationHandler) UpdateSample(c *gin.Context) {
	var req types.UpdateEvaluationSampleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		evaluationBadRequest(c, err)
		return
	}
	s, err := e.evaluationService.UpdateSample(c.Request.Context(), c.Param("dataset_id"), c.Param("sample_id"), &req)
	if err != nil {
		evaluationBadRequest(c, err)
		return
	}
	evaluationOK(c, s)
}
func (e *EvaluationHandler) DeleteSample(c *gin.Context) {
	if err := e.evaluationService.DeleteSample(c.Request.Context(), c.Param("dataset_id"), c.Param("sample_id")); err != nil {
		evaluationError(c, err)
		return
	}
	evaluationOK(c, gin.H{})
}
func (e *EvaluationHandler) CreateRun(c *gin.Context) {
	var req types.CreateEvaluationRunRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		evaluationBadRequest(c, err)
		return
	}
	run, err := e.evaluationService.CreateRun(c.Request.Context(), &req)
	if err != nil {
		evaluationBadRequest(c, err)
		return
	}
	evaluationOK(c, run)
}
func (e *EvaluationHandler) GetRun(c *gin.Context) {
	run, err := e.evaluationService.GetRun(c.Request.Context(), c.Param("run_id"))
	if err != nil {
		evaluationError(c, err)
		return
	}
	evaluationOK(c, run)
}
func (e *EvaluationHandler) ListRuns(c *gin.Context) {
	var page types.Pagination
	if err := c.ShouldBindQuery(&page); err != nil {
		evaluationBadRequest(c, err)
		return
	}
	result, err := e.evaluationService.ListRuns(c.Request.Context(), &page)
	if err != nil {
		evaluationError(c, err)
		return
	}
	evaluationOK(c, result)
}
func (e *EvaluationHandler) ListRunResults(c *gin.Context) {
	var page types.Pagination
	if err := c.ShouldBindQuery(&page); err != nil {
		evaluationBadRequest(c, err)
		return
	}
	result, err := e.evaluationService.ListRunResults(c.Request.Context(), c.Param("run_id"), &page)
	if err != nil {
		evaluationError(c, err)
		return
	}
	evaluationOK(c, result)
}
func (e *EvaluationHandler) CompareRuns(c *gin.Context) {
	baseline := c.Query("baseline_run_id")
	candidate := c.Query("candidate_run_id")
	if baseline == "" || candidate == "" {
		evaluationBadRequest(c, stderrors.New("baseline_run_id and candidate_run_id are required"))
		return
	}
	result, err := e.evaluationService.CompareRuns(c.Request.Context(), baseline, candidate)
	if err != nil {
		evaluationBadRequest(c, err)
		return
	}
	evaluationOK(c, result)
}

type EvaluationRequest struct {
	DatasetID       string `json:"dataset_id"`
	KnowledgeBaseID string `json:"knowledge_base_id"`
	ChatModelID     string `json:"chat_id"`
	RerankModelID   string `json:"rerank_id"`
}

func (e *EvaluationHandler) Evaluation(c *gin.Context) {
	var req EvaluationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		evaluationBadRequest(c, err)
		return
	}
	detail, err := e.evaluationService.Evaluation(c.Request.Context(), req.DatasetID, req.KnowledgeBaseID, req.ChatModelID, req.RerankModelID)
	if err != nil {
		evaluationBadRequest(c, err)
		return
	}
	evaluationOK(c, gin.H{"task": detail.Task, "params": detail.Params})
}

type GetEvaluationRequest struct {
	TaskID string `form:"task_id" binding:"required"`
}

func (e *EvaluationHandler) GetEvaluationResult(c *gin.Context) {
	var req GetEvaluationRequest
	if err := c.ShouldBindQuery(&req); err != nil {
		evaluationBadRequest(c, err)
		return
	}
	detail, err := e.evaluationService.EvaluationResult(c.Request.Context(), req.TaskID)
	if err != nil {
		evaluationError(c, err)
		return
	}
	evaluationOK(c, gin.H{"task": detail.Task, "params": detail.Params, "metric": detail.Metric})
}
