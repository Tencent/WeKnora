package handler

import (
	"net/http"

	"github.com/Tencent/WeKnora/internal/application/service/web_search"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/gin-gonic/gin"
)

// WebSearchHandler handles web search related requests
type WebSearchHandler struct {
	registry *web_search.Registry
}

// NewWebSearchHandler creates a new web search handler
func NewWebSearchHandler(registry *web_search.Registry) *WebSearchHandler {
	return &WebSearchHandler{
		registry: registry,
	}
}

// GetProviders returns the list of available web search providers
// @Summary Get available web search providers
// @Description Returns the list of available web search providers from configuration
// @Tags web-search
// @Accept json
// @Produce json
// @Success 200 {object} map[string]interface{} "List of providers"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router /web-search/providers [get]
func (h *WebSearchHandler) GetProviders(c *gin.Context) {
	ctx := c.Request.Context()
	logger.Info(ctx, "Getting web search providers")

	providers := h.registry.GetAllProviderInfos()

	logger.Infof(ctx, "Returning %d web search providers", len(providers))
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    providers,
	})
}
