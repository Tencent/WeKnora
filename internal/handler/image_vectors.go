package handler

import (
	"net/http"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
)

// ImageVectorCoverage reports the active model's image index coverage.
func (h *KnowledgeHandler) ImageVectorCoverage(c *gin.Context) {
	_, id, tenant, _, err := h.validateKnowledgeBaseAccess(c)
	if err != nil {
		_ = c.Error(err)
		return
	}
	ctx := types.WithExecutionTenant(c.Request.Context(), tenant)
	result, err := h.imageVectors.Coverage(ctx, id)
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": result})
}

// BackfillImageVectors schedules missing/failed images; it never reparses documents.
func (h *KnowledgeHandler) BackfillImageVectors(c *gin.Context) {
	_, id, tenant, _, err := h.validateKnowledgeBaseWriteAccessWithKBID(c, c.Param("id"))
	if err != nil {
		_ = c.Error(err)
		return
	}
	if err := h.requireKBOwnershipOrAdmin(c, id); err != nil {
		_ = c.Error(err)
		return
	}
	ctx := types.WithExecutionTenant(c.Request.Context(), tenant)
	if err := h.imageVectors.Schedule(ctx, id, "", ""); err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusAccepted, gin.H{"success": true})
}
