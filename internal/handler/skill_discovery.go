package handler

import (
	"context"
	"net/http"

	"github.com/Tencent/WeKnora/internal/application/service"
	builtin "github.com/Tencent/WeKnora/internal/builtin/skills"
	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
)

type skillDiscoveryService interface {
	RegisterBuiltin(context.Context, uint64, string) (*types.TenantSkillCatalogEntity, error)
	PlanSkillMigration(context.Context, uint64, string, string) ([]service.SkillMigrationItem, error)
	MigrateSkills(
		context.Context,
		uint64,
		string,
		string,
		[]service.SkillMigrationItem,
	) (*service.CatalogInstallResult, error)
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

// MigrateSkills validates the selected bundle versions before installing them into the target.
func (h *SkillHandler) MigrateSkills(c *gin.Context) {
	svc, ok := h.catalog.(skillDiscoveryService)
	if !ok {
		_ = c.Error(apperrors.NewInternalServerError("skill migration is not configured"))
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 64*1024)
	var in struct {
		Skills   []service.SkillMigrationItem `json:"skills"`
		SourceID string                       `json:"source_config_id" binding:"required"`
		TargetID string                       `json:"target_config_id" binding:"required"`
		Preview  bool                         `json:"preview"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		_ = c.Error(apperrors.NewBadRequestError("source_config_id and target_config_id are required"))
		return
	}
	if in.Preview {
		items, err := svc.PlanSkillMigration(c.Request.Context(), sandboxConfigTenantID(c), in.SourceID, in.TargetID)
		if err != nil {
			respondSkillServiceError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"success": true, "data": items})
		return
	}
	result, err := svc.MigrateSkills(c.Request.Context(), sandboxConfigTenantID(c), in.SourceID, in.TargetID, in.Skills)
	if err != nil {
		respondSkillServiceError(c, err)
		return
	}
	c.JSON(http.StatusAccepted, gin.H{"success": true, "data": result})
}
