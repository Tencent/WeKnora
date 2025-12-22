package handler

import (
	"net/http"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
)

// WebSearchHandler handles web search related requests
type WebSearchHandler struct {
	cfg *config.Config
}

// NewWebSearchHandler creates a new web search handler
func NewWebSearchHandler(cfg *config.Config) *WebSearchHandler {
	return &WebSearchHandler{
		cfg: cfg,
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

	if h.cfg.WebSearch == nil || len(h.cfg.WebSearch.Providers) == 0 {
		logger.Warn(ctx, "No web search providers configured")
		c.JSON(http.StatusOK, gin.H{
			"success": true,
			"data":    []types.WebSearchProviderInfo{},
		})
		return
	}

	// Convert config providers to API response format
	providers := make([]types.WebSearchProviderInfo, 0, len(h.cfg.WebSearch.Providers))
	for _, provider := range h.cfg.WebSearch.Providers {
		providers = append(providers, types.WebSearchProviderInfo{
			ID:             provider.ID,
			Name:           provider.Name,
			Free:           provider.Free,
			RequiresAPIKey: provider.RequiresAPIKey,
			Description:    provider.Description,
			APIURL:         provider.APIURL,
		})
	}

	logger.Infof(ctx, "Returning %d web search providers", len(providers))
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    providers,
	})
}
