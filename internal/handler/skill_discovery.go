package handler

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"

	builtin "github.com/Tencent/WeKnora/internal/builtin/skills"
	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
)

type skillDiscoveryService interface {
	RegisterBuiltin(context.Context, uint64, string, bool) (*types.TenantSkillCatalogEntity, error)
}

// ListDiscovery returns built-in skill and external recommendation metadata.
func (h *SkillHandler) ListDiscovery(c *gin.Context) {
	entries := builtin.List()
	for i := range entries {
		if entries[i].Distribution != "builtin" {
			continue
		}
		archive, err := builtin.Archive(entries[i].ID)
		if err != nil {
			_ = c.Error(apperrors.NewInternalServerError("failed to read builtin skill archive"))
			return
		}
		sum := sha256.Sum256(archive)
		entries[i].BundleSHA256 = hex.EncodeToString(sum[:])
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": entries})
}

// RegisterBuiltin registers an embedded package, replacing a definition only on request.
func (h *SkillHandler) RegisterBuiltin(c *gin.Context) {
	svc, ok := h.catalog.(skillDiscoveryService)
	if !ok {
		_ = c.Error(apperrors.NewInternalServerError("skill discovery is not configured"))
		return
	}
	var request struct {
		ReplaceExisting bool `json:"replace_existing"`
	}
	if err := c.ShouldBindJSON(&request); err != nil && !errors.Is(err, io.EOF) {
		respondSkillServiceError(c, apperrors.NewBadRequestError("invalid builtin registration request"))
		return
	}
	row, err := svc.RegisterBuiltin(
		c.Request.Context(), sandboxConfigTenantID(c), c.Param("id"), request.ReplaceExisting,
	)
	if err != nil {
		respondSkillServiceError(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"success": true, "data": catalogIDResponse(row)})
}
