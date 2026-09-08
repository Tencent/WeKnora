package handler

import (
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
)

const modelUsageAnalyticsDefaultRange = 30 * 24 * time.Hour

// ModelUsageAnalyticsHandler serves model usage analytics requests.
type ModelUsageAnalyticsHandler struct {
	service interfaces.ModelUsageAnalyticsService
	now     func() time.Time
}

// NewModelUsageAnalyticsHandler creates a model usage analytics handler.
func NewModelUsageAnalyticsHandler(service interfaces.ModelUsageAnalyticsService) *ModelUsageAnalyticsHandler {
	return &ModelUsageAnalyticsHandler{service: service, now: time.Now}
}

// GetAnalytics godoc
// @Summary      Aggregate model usage analytics
// @Description  Returns tenant-scoped model usage summary and UTC trend buckets using model_usage.created_at.
// @Tags         Model Usage
// @Produce      json
// @Param        model_id   query string false "Exact model ID"
// @Param        start_time query string false "Inclusive RFC3339 start time (defaults to 30 days before end_time)"
// @Param        end_time   query string false "Exclusive RFC3339 end time (defaults to now)"
// @Param        interval   query string false "UTC trend interval: hour or day (default day)"
// @Success      200 {object} map[string]interface{}
// @Failure      400 {object} errors.AppError
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /model-usage/analytics [get]
func (h *ModelUsageAnalyticsHandler) GetAnalytics(c *gin.Context) {
	query, err := h.parseQuery(c)
	if err != nil {
		_ = c.Error(err)
		return
	}
	result, serviceErr := h.service.GetAnalytics(c.Request.Context(), query)
	if serviceErr != nil {
		logger.ErrorWithFields(c.Request.Context(), serviceErr, nil)
		_ = c.Error(errors.NewInternalServerError("Failed to aggregate model usage analytics"))
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": result})
}

func (h *ModelUsageAnalyticsHandler) parseQuery(c *gin.Context) (types.ModelUsageAnalyticsQuery, *errors.AppError) {
	if _, supplied := c.GetQuery("tenant_id"); supplied {
		return types.ModelUsageAnalyticsQuery{}, errors.NewBadRequestError("tenant_id query parameter is not allowed")
	}

	interval := types.ModelUsageAnalyticsIntervalDay
	if raw, supplied := c.GetQuery("interval"); supplied {
		interval = types.ModelUsageAnalyticsInterval(raw)
	}
	switch interval {
	case types.ModelUsageAnalyticsIntervalHour, types.ModelUsageAnalyticsIntervalDay:
	default:
		return types.ModelUsageAnalyticsQuery{}, errors.NewBadRequestError("interval must be hour or day")
	}

	modelID := ""
	if raw, supplied := c.GetQuery("model_id"); supplied {
		if raw == "" || strings.TrimSpace(raw) != raw || utf8.RuneCountInString(raw) > types.ModelIDMaxLen {
			return types.ModelUsageAnalyticsQuery{}, errors.NewBadRequestError(
				"model_id must be a non-empty model ID of at most 64 characters",
			)
		}
		modelID = raw
	}

	now := h.now().UTC().Truncate(time.Second)
	endTime := now
	if raw, supplied := c.GetQuery("end_time"); supplied {
		parsed, parseErr := time.Parse(time.RFC3339, raw)
		if parseErr != nil {
			return types.ModelUsageAnalyticsQuery{}, errors.NewBadRequestError("end_time must be RFC3339")
		}
		endTime = parsed.UTC()
	}
	startTime := endTime.Add(-modelUsageAnalyticsDefaultRange)
	if raw, supplied := c.GetQuery("start_time"); supplied {
		parsed, parseErr := time.Parse(time.RFC3339, raw)
		if parseErr != nil {
			return types.ModelUsageAnalyticsQuery{}, errors.NewBadRequestError("start_time must be RFC3339")
		}
		startTime = parsed.UTC()
	}
	if !startTime.Before(endTime) {
		return types.ModelUsageAnalyticsQuery{}, errors.NewBadRequestError("start_time must be before end_time")
	}
	return types.ModelUsageAnalyticsQuery{
		ModelID: modelID, StartTime: startTime, EndTime: endTime, Interval: interval,
	}, nil
}
