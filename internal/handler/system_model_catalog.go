package handler

import (
	"errors"
	"net/http"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/application/service"
	"github.com/gin-gonic/gin"
)

// GetModelCatalog returns the catalog layers, overlay and version history.
// @Summary      Get the system model catalog
// @Description  System administrator JWT only; API keys are not accepted. Returns built-in, deployment and effective
// @Description  catalogs, the current overlay, version/baseline tokens, and the latest 20 published revisions.
// @Tags         系统管理
// @Produce      json
// @Success      200  {object}  service.CatalogState
// @Failure      401  {object}  map[string]interface{}
// @Failure      403  {object}  map[string]interface{}
// @Failure      503  {object}  map[string]interface{}
// @Security     Bearer
// @Router       /system/admin/model-catalog [get]
func (h *SystemHandler) GetModelCatalog(c *gin.Context) {
	state, err := h.modelCatalogSvc.State(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Model catalog is unavailable"})
		return
	}
	c.JSON(http.StatusOK, state)
}

// PreviewModelCatalog validates an overlay and returns the candidate catalog.
// @Summary      Preview a model catalog overlay
// @Description  System administrator JWT only; API keys are not accepted. Validates the overlay without publishing
// @Description  it. Supply the current version and baseline; stale values return 409. The response contains the
// @Description  candidate effective catalog and normalized overlay; history, builtin and deployment are null.
// @Tags         系统管理
// @Accept       json
// @Produce      json
// @Param        request  body  service.CatalogUpdate  true  "Version, baseline and overlay object (max 1 MiB)"
// @Success      200  {object}  service.CatalogState
// @Failure      400  {object}  map[string]interface{}
// @Failure      401  {object}  map[string]interface{}
// @Failure      403  {object}  map[string]interface{}
// @Failure      409  {object}  map[string]interface{}
// @Failure      500  {object}  map[string]interface{}
// @Security     Bearer
// @Router       /system/admin/model-catalog/preview [post]
func (h *SystemHandler) PreviewModelCatalog(c *gin.Context) { h.changeModelCatalog(c, false) }

// PublishModelCatalog validates, persists and applies an overlay.
// @Summary      Publish a model catalog overlay
// @Description  System administrator JWT only; API keys are not accepted. Validates and publishes a new overlay
// @Description  revision using the current version and baseline. Stale values return 409. To restore a historical
// @Description  configuration, submit its overlay with the current version and baseline.
// @Tags         系统管理
// @Accept       json
// @Produce      json
// @Param        request  body  service.CatalogUpdate  true  "Version, baseline and overlay object (max 1 MiB)"
// @Success      200  {object}  service.CatalogState
// @Failure      400  {object}  map[string]interface{}
// @Failure      401  {object}  map[string]interface{}
// @Failure      403  {object}  map[string]interface{}
// @Failure      409  {object}  map[string]interface{}
// @Failure      500  {object}  map[string]interface{}
// @Security     Bearer
// @Router       /system/admin/model-catalog [put]
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
