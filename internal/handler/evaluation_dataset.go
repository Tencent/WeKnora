package handler

import (
	"errors"
	"net/http"
	"strings"

	"github.com/Tencent/WeKnora/internal/config"
	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	secutils "github.com/Tencent/WeKnora/internal/utils"
	"github.com/gin-gonic/gin"
)

// EvaluationDatasetHandler exposes the evaluation dataset registry over HTTP.
type EvaluationDatasetHandler struct {
	registryService interfaces.EvaluationDatasetRegistryService
	limits          config.EvaluationDatasetLimits
}

// NewEvaluationDatasetHandler creates the dataset registry handler.
func NewEvaluationDatasetHandler(
	cfg *config.Config,
	registryService interfaces.EvaluationDatasetRegistryService,
) *EvaluationDatasetHandler {
	return &EvaluationDatasetHandler{
		registryService: registryService,
		limits:          config.EvaluationDatasetLimitsOrDefault(cfg),
	}
}

// CreateEvaluationDatasetRequest carries one tenant dataset creation request.
type CreateEvaluationDatasetRequest struct {
	Name        string `json:"name" binding:"required"`
	Description string `json:"description"`
}

// CreateDataset godoc
// @Summary      创建评测数据集
// @Description  创建租户评测数据集身份
// @Tags         评估
// @Accept       json
// @Produce      json
// @Param        request  body      CreateEvaluationDatasetRequest  true  "数据集"
// @Success      200      {object}  map[string]interface{}           "数据集"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /evaluation/datasets [post]
func (h *EvaluationDatasetHandler) CreateDataset(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID, ok := evaluationHandlerTenantID(c)
	if !ok {
		return
	}

	var request CreateEvaluationDatasetRequest
	if err := c.ShouldBind(&request); err != nil {
		_ = c.Error(apperrors.NewBadRequestError("Invalid request parameters").WithDetails(err.Error()))
		return
	}

	dataset, err := h.registryService.CreateDataset(ctx, tenantID, strings.TrimSpace(request.Name), request.Description)
	if err != nil {
		writeEvaluationDatasetError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": dataset})
}

// CreateVersion godoc
// @Summary      创建评测数据集版本
// @Description  以结构化输入创建不可变数据集版本
// @Tags         评估
// @Accept       json
// @Produce      json
// @Param        id       path      string                              true  "数据集 ID"
// @Param        request  body      types.EvaluationDatasetVersionInput true  "版本内容"
// @Success      200      {object}  map[string]interface{}               "版本"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /evaluation/datasets/{id}/versions [post]
func (h *EvaluationDatasetHandler) CreateVersion(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID, ok := evaluationHandlerTenantID(c)
	if !ok {
		return
	}
	datasetID := c.Param("id")

	var content types.EvaluationDatasetVersionInput
	if !h.decodeDatasetJSON(c, &content, false) {
		return
	}

	version, err := h.registryService.CreateVersion(ctx, tenantID, datasetID, &content)
	if err != nil {
		writeEvaluationDatasetError(c, err)
		return
	}
	logger.Infof(ctx, "Created evaluation dataset version %s for dataset %s",
		version.ID, secutils.SanitizeForLog(datasetID))
	c.JSON(http.StatusOK, gin.H{"success": true, "data": version})
}

// ListDatasets godoc
// @Summary      列出评测数据集
// @Description  列出当前租户可见的评测数据集
// @Tags         评估
// @Produce      json
// @Success      200  {object}  map[string]interface{}  "数据集列表"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /evaluation/datasets [get]
func (h *EvaluationDatasetHandler) ListDatasets(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID, ok := evaluationHandlerTenantID(c)
	if !ok {
		return
	}
	datasets, err := h.registryService.ListDatasets(ctx, tenantID)
	if err != nil {
		writeEvaluationDatasetError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"items": datasets}})
}

// ListVersions godoc
// @Summary      列出评测数据集版本
// @Description  列出一个可见数据集的全部不可变版本
// @Tags         评估
// @Produce      json
// @Param        id   path      string  true  "数据集 ID"
// @Success      200  {object}  map[string]interface{}  "版本列表"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /evaluation/datasets/{id}/versions [get]
func (h *EvaluationDatasetHandler) ListVersions(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID, ok := evaluationHandlerTenantID(c)
	if !ok {
		return
	}
	versions, err := h.registryService.ListVersions(ctx, tenantID, c.Param("id"))
	if err != nil {
		writeEvaluationDatasetError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"items": versions}})
}

func evaluationHandlerTenantID(c *gin.Context) (uint64, bool) {
	value, exists := c.Get(string(types.TenantIDContextKey))
	if !exists {
		_ = c.Error(apperrors.NewUnauthorizedError("Unauthorized"))
		return 0, false
	}
	tenantID, ok := value.(uint64)
	if !ok {
		_ = c.Error(apperrors.NewUnauthorizedError("Unauthorized"))
		return 0, false
	}
	return tenantID, true
}

func writeEvaluationDatasetError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, interfaces.ErrEvaluationDatasetLimitExceeded):
		_ = c.Error(apperrors.NewRequestEntityTooLargeError("Evaluation dataset exceeds the configured limits").
			WithDetails(err.Error()))
	case errors.Is(err, interfaces.ErrEvaluationDatasetNotFound),
		errors.Is(err, interfaces.ErrEvaluationDatasetVersionNotFound):
		_ = c.Error(apperrors.NewNotFoundError("Evaluation dataset not found").WithDetails(err.Error()))
	case errors.Is(err, interfaces.ErrEvaluationDatasetVersionConflict),
		errors.Is(err, interfaces.ErrEvaluationDatasetAlreadyExists):
		_ = c.Error(apperrors.NewConflictError("Evaluation dataset conflicts with an existing record").
			WithDetails(err.Error()))
	case errors.Is(err, interfaces.ErrEvaluationDatasetInvalid):
		_ = c.Error(apperrors.NewBadRequestError("Invalid evaluation dataset content").WithDetails(err.Error()))
	default:
		_ = c.Error(apperrors.NewInternalServerError(err.Error()))
	}
}
