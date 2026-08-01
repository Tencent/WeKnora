package handler

import (
	"net/http"
	"strings"

	"github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
)

// ModelUsageHandler serves the model usage dashboard report.
type ModelUsageHandler struct {
	service interfaces.ModelUsageService
}

// NewModelUsageHandler creates a ModelUsageHandler.
func NewModelUsageHandler(service interfaces.ModelUsageService) *ModelUsageHandler {
	return &ModelUsageHandler{service: service}
}

// GetUsage GET /api/v1/models/usage?range=24h&model_type=all&model_id=
func (h *ModelUsageHandler) GetUsage(c *gin.Context) {
	ctx := c.Request.Context()
	rangeValue := c.DefaultQuery("range", "24h")
	if !validUsageRange(rangeValue) {
		_ = c.Error(errors.NewBadRequestError("invalid range"))
		return
	}

	modelType := types.ModelType(c.DefaultQuery("model_type", "all"))
	if !validUsageModelType(modelType) {
		_ = c.Error(errors.NewBadRequestError("invalid model_type"))
		return
	}

	query := types.ModelUsageQuery{
		Range:     rangeValue,
		ModelType: modelType,
		ModelID:   strings.TrimSpace(c.Query("model_id")),
	}
	report, err := h.service.GetUsageReport(ctx, query)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"range":      rangeValue,
			"model_type": modelType,
		})
		_ = c.Error(errors.NewInternalServerError(err.Error()))
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    report,
	})
}

func validUsageRange(value string) bool {
	switch value {
	case "15m", "1h", "24h", "7d":
		return true
	default:
		return false
	}
}

func validUsageModelType(value types.ModelType) bool {
	switch value {
	case "all",
		types.ModelTypeKnowledgeQA,
		types.ModelTypeEmbedding,
		types.ModelTypeRerank,
		types.ModelTypeVLLM,
		types.ModelTypeASR:
		return true
	default:
		return false
	}
}
