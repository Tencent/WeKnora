package handler

import (
	"net/http"
	"strconv"
	"time"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
)

// ModelTokenUsageHandler serves tenant-scoped token usage reports.
type ModelTokenUsageHandler struct {
	repo *repository.ModelTokenUsageRepository
}

// NewModelTokenUsageHandler creates a handler backed by the model usage ledger.
func NewModelTokenUsageHandler(repo *repository.ModelTokenUsageRepository) *ModelTokenUsageHandler {
	return &ModelTokenUsageHandler{repo: repo}
}

// List returns daily or monthly aggregates within the caller's administrative scope.
func (h *ModelTokenUsageHandler) List(c *gin.Context) {
	ctx := c.Request.Context()
	caller := types.CallerFromContext(ctx)
	systemAdmin := types.IsSystemAdminFromContext(ctx)
	if !systemAdmin && (caller.TenantID == 0 || !caller.Role.HasPermission(types.TenantRoleAdmin)) {
		c.JSON(http.StatusForbidden, gin.H{"error": "Administrator access required"})
		return
	}

	tenantID := caller.TenantID
	if systemAdmin {
		tenantID = 0
	}
	if raw, exists := c.GetQuery("tenant_id"); exists {
		requestedTenant, err := strconv.ParseUint(raw, 10, 63)
		if err != nil || requestedTenant == 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "tenant_id must be a positive integer"})
			return
		}
		if !systemAdmin && requestedTenant != tenantID {
			c.JSON(http.StatusForbidden, gin.H{"error": "Cannot query another tenant's model usage"})
			return
		}
		tenantID = requestedTenant
	}

	from, fromErr := time.Parse(time.DateOnly, c.Query("from"))
	to, toErr := time.Parse(time.DateOnly, c.Query("to"))
	if fromErr != nil || toErr != nil || !to.After(from) || to.After(from.AddDate(0, 0, 366)) {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "Use YYYY-MM-DD UTC dates spanning 1 to 366 days. The end date is exclusive.",
		})
		return
	}
	interval := c.DefaultQuery("interval", "day")
	if interval != "day" && interval != "month" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "interval must be day or month"})
		return
	}
	rows, err := h.repo.Summarize(ctx, tenantID, from.Format(time.DateOnly), to.Format(time.DateOnly),
		c.Query("model_id"), interval == "month")
	if err != nil {
		logger.Errorf(ctx, "query model token usage: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Could not query model token usage"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": rows})
}
