package handler

import (
	"net/http"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
)

// WebSearchHandler handles legacy web search related requests
type WebSearchHandler struct{}

// NewWebSearchHandler creates a new web search handler
func NewWebSearchHandler() *WebSearchHandler {
	return &WebSearchHandler{}
}

// GetProviders returns the list of available web search provider types
func (h *WebSearchHandler) GetProviders(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    types.GetWebSearchProviderTypes(),
	})
}
