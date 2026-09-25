package handler

import (
	"errors"
	"net/http"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/application/service"
	"github.com/gin-gonic/gin"
)

// GetModelCatalog returns the catalog layers, overlay and version history.
func (h *SystemHandler) GetModelCatalog(c *gin.Context) {
	state, err := h.modelCatalogSvc.State(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Model catalog is unavailable"})
		return
	}
	c.JSON(http.StatusOK, state)
}

// PreviewModelCatalog validates an overlay and returns the candidate catalog.
func (h *SystemHandler) PreviewModelCatalog(c *gin.Context) { h.changeModelCatalog(c, false) }

// PublishModelCatalog validates, persists and applies an overlay.
func (h *SystemHandler) PublishModelCatalog(c *gin.Context) { h.changeModelCatalog(c, true) }

func (h *SystemHandler) changeModelCatalog(c *gin.Context, publish bool) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, service.MaxCatalogOverlayBytes+4096)
	var req service.CatalogUpdate
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid model catalog request (maximum 1 MiB)"})
		return
	}
	var state *service.CatalogState
	var err error
	if publish {
		state, err = h.modelCatalogSvc.Publish(c.Request.Context(), req)
	} else {
		state, err = h.modelCatalogSvc.Preview(c.Request.Context(), req)
	}
	if err != nil {
		switch {
		case errors.Is(err, repository.ErrCatalogVersionConflict):
			c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		case errors.Is(err, service.ErrInvalidCatalog):
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		default:
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update model catalog"})
		}
		return
	}
	c.JSON(http.StatusOK, state)
}
