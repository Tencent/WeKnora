package handler

import (
	"context"
	"net/http"

	builtin "github.com/Tencent/WeKnora/internal/builtin/skills"
	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
)

type skillDiscoveryService interface {
	RegisterBuiltin(context.Context, uint64, string) (*types.TenantSkillCatalogEntity, error)
}

// ListDiscovery returns built-in skill and external recommendation metadata.
func (h *SkillHandler) ListDiscovery(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"success": true, "data": builtin.List()})
}

// RegisterBuiltin registers an embedded package without replacing user resources.
func (h *SkillHandler) RegisterBuiltin(c *gin.Context) {
	svc, ok := h.catalog.(skillDiscoveryService)
	if !ok {
		_ = c.Error(apperrors.NewInternalServerError("skill discovery is not configured"))
		return
	}
	row, err := svc.RegisterBuiltin(c.Request.Context(), sandboxConfigTenantID(c), c.Param("id"))
	if err != nil {
		respondSkillServiceError(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"success": true, "data": catalogIDResponse(row)})
}
