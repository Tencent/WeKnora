package handler

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/modelstats"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
)

type modelStatisticsService interface {
	Usage(context.Context, uint64, []string, *time.Time, *time.Time) (*types.ModelUsageResponse, error)
	Prices(context.Context, uint64, string) ([]*types.ModelPriceVersion, error)
	PutPrice(context.Context, uint64, string, *types.ModelPriceVersion) error
}

// ModelStatisticsHandler serves usage aggregates and immutable price versions.
type ModelStatisticsHandler struct{ service modelStatisticsService }

type modelUsageEnvelope struct {
	Success bool                      `json:"success"`
	Data    *types.ModelUsageResponse `json:"data"`
}

type modelPricesEnvelope struct {
	Success bool                       `json:"success"`
	Data    []*types.ModelPriceVersion `json:"data"`
}

type modelPriceEnvelope struct {
	Success bool                     `json:"success"`
	Data    *types.ModelPriceVersion `json:"data"`
}

// NewModelStatisticsHandler creates the HTTP handler for model usage and prices.
func NewModelStatisticsHandler(service *modelstats.Service) *ModelStatisticsHandler {
	return &ModelStatisticsHandler{service: service}
}

// ListUsage returns tenant-scoped usage for an optional set of model identifiers.
// @Summary List model usage
// @Tags models
// @Produce json
// @Param model_ids query string false "Comma-separated model identifiers"
// @Param from query string false "Inclusive start time (RFC3339)"
// @Param to query string false "Exclusive end time (RFC3339)"
// @Success 200 {object} modelUsageEnvelope
// @Failure 400 {object} errors.AppError
// @Security Bearer
// @Security ApiKeyAuth
// @Router /models/usage [get]
func (h *ModelStatisticsHandler) ListUsage(c *gin.Context) {
	h.usage(c, splitModelIDs(c.Query("model_ids")))
}

// GetUsage returns tenant-scoped usage for one model identifier.
// @Summary Get model usage
// @Tags models
// @Produce json
// @Param id path string true "Model identifier"
// @Param from query string false "Inclusive start time (RFC3339)"
// @Param to query string false "Exclusive end time (RFC3339)"
// @Success 200 {object} modelUsageEnvelope
// @Failure 400 {object} errors.AppError
// @Security Bearer
// @Security ApiKeyAuth
// @Router /models/{id}/usage [get]
func (h *ModelStatisticsHandler) GetUsage(c *gin.Context) {
	modelID := strings.TrimSpace(c.Param("id"))
	if modelID == "" {
		_ = c.Error(errors.NewBadRequestError("Model ID cannot be empty"))
		return
	}
	h.usage(c, []string{modelID})
}

func (h *ModelStatisticsHandler) usage(c *gin.Context, modelIDs []string) {
	from, err := optionalRFC3339(c.Query("from"))
	if err != nil {
		_ = c.Error(errors.NewBadRequestError("from must use RFC3339 format"))
		return
	}
	to, err := optionalRFC3339(c.Query("to"))
	if err != nil {
		_ = c.Error(errors.NewBadRequestError("to must use RFC3339 format"))
		return
	}
	result, err := h.service.Usage(c.Request.Context(), tenantID(c), modelIDs, from, to)
	if err != nil {
		_ = c.Error(errors.NewBadRequestError(err.Error()))
		return
	}
	c.JSON(http.StatusOK, modelUsageEnvelope{Success: true, Data: result})
}

// ListPrices returns immutable price versions for one model identifier.
// @Summary List model price versions
// @Tags models
// @Produce json
// @Param id path string true "Model identifier"
// @Success 200 {object} modelPricesEnvelope
// @Failure 400 {object} errors.AppError
// @Security Bearer
// @Security ApiKeyAuth
// @Router /models/{id}/pricing [get]
func (h *ModelStatisticsHandler) ListPrices(c *gin.Context) {
	prices, err := h.service.Prices(c.Request.Context(), tenantID(c), c.Param("id"))
	if err != nil {
		_ = c.Error(errors.NewBadRequestError(err.Error()))
		return
	}
	c.JSON(http.StatusOK, modelPricesEnvelope{Success: true, Data: prices})
}

type putModelPriceRequest struct {
	CachePricing               *types.ModelCachePricing `json:"cache_pricing,omitempty"`
	ValidFrom                  time.Time                `json:"valid_from" binding:"required"`
	ValidTo                    *time.Time               `json:"valid_to"`
	InputMicrounitsPerMillion  int64                    `json:"input_microunits_per_million" binding:"min=0"`
	OutputMicrounitsPerMillion int64                    `json:"output_microunits_per_million" binding:"min=0"`
	Currency                   string                   `json:"currency" binding:"required,len=3"`
}

// PutPrice appends one effective-dated price version for a model identifier.
// @Summary Create an effective-dated model price
// @Tags models
// @Accept json
// @Produce json
// @Param id path string true "Model identifier"
// @Param request body putModelPriceRequest true "Price interval and rates"
// @Success 201 {object} modelPriceEnvelope
// @Failure 400 {object} errors.AppError
// @Security Bearer
// @Security ApiKeyAuth
// @Router /models/{id}/pricing [put]
func (h *ModelStatisticsHandler) PutPrice(c *gin.Context) {
	var request putModelPriceRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		_ = c.Error(errors.NewBadRequestError(err.Error()))
		return
	}
	price := &types.ModelPriceVersion{
		CachePricing: request.CachePricing,
		ValidFrom:    request.ValidFrom, ValidTo: request.ValidTo,
		InputMicrounitsPerMillion:  request.InputMicrounitsPerMillion,
		OutputMicrounitsPerMillion: request.OutputMicrounitsPerMillion,
		Currency:                   request.Currency,
	}
	if err := h.service.PutPrice(c.Request.Context(), tenantID(c), c.Param("id"), price); err != nil {
		_ = c.Error(errors.NewBadRequestError(err.Error()))
		return
	}
	c.JSON(http.StatusCreated, modelPriceEnvelope{Success: true, Data: price})
}

func tenantID(c *gin.Context) uint64 { return c.GetUint64(types.TenantIDContextKey.String()) }

func optionalRFC3339(raw string) (*time.Time, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	value, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return nil, err
	}
	value = value.UTC()
	return &value, nil
}

func splitModelIDs(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	return strings.Split(raw, ",")
}
