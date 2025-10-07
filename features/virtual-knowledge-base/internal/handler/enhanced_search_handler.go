package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
	service "github.com/tencent/weknora/features/virtualkb/internal/service/interfaces"
	"github.com/tencent/weknora/features/virtualkb/internal/types"
)

// EnhancedSearchHandler provides HTTP endpoints for enhanced search.
type EnhancedSearchHandler struct {
	service service.EnhancedSearchService
}

// NewEnhancedSearchHandler constructs a new handler.
func NewEnhancedSearchHandler(service service.EnhancedSearchService) *EnhancedSearchHandler {
	return &EnhancedSearchHandler{service: service}
}

// RegisterRoutes wires enhanced search routes.
func (h *EnhancedSearchHandler) RegisterRoutes(rg *gin.RouterGroup) {
	rg.POST("", h.search)
}

func (h *EnhancedSearchHandler) search(c *gin.Context) {
	var req types.EnhancedSearchRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondBadRequest(c, "invalid search payload", err)
		return
	}

	resp, err := h.service.Search(c.Request.Context(), &req)
	if err != nil {
		respondBadRequest(c, "failed to perform enhanced search", err)
		return
	}
	respondSuccess(c, http.StatusOK, resp)
}
