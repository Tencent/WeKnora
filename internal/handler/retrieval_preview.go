package handler

import (
	"net/http"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
)

// RetrievalPreviewRequest compares retrieval modes within one authorized KB.
type RetrievalPreviewRequest struct {
	// Search query to compare across retrieval modes.
	Query string `json:"query" binding:"required,max=2000" extensions:"x-agent,x-agent-max-bytes=8000"`
	// Retrieval mode to inspect.
	Mode string `json:"mode" binding:"required,oneof=hybrid keyword vector" enums:"hybrid,keyword,vector" extensions:"x-agent"`
	// Maximum candidates to return, from 1 to 20; default 10.
	Limit int `json:"limit" binding:"omitempty,min=1,max=20" extensions:"x-agent,x-agent-maximum=20"`
}

// PreviewRetrieval godoc
// @Summary Diagnose keyword, vector or hybrid retrieval
// @Tags 知识库
// @Accept json
// @Produce json
// @Param id path string true "Knowledge base ID" extensions(x-agent=knowledge_base_id)
// @Param request body handler.RetrievalPreviewRequest true "Bounded retrieval query"
// @Success 200 {object} handler.HybridSearchResponse
// @x-agent {"id":"knowledge_base.preview_retrieval","risk":"read","max_items":20}
// @x-agent-hint "Compare keyword, vector and hybrid candidates, scores and source documents for one query. Does not generate an answer or change index settings. Vector/hybrid may incur embedding usage; scores from different modes are not directly comparable."
// @Router /knowledge-bases/{id}/retrieval-preview [post]
func (h *KnowledgeBaseHandler) PreviewRetrieval(c *gin.Context) {
	var req RetrievalPreviewRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid retrieval preview request"})
		return
	}
	if req.Limit == 0 {
		req.Limit = 10
	}
	results, err := h.service.HybridSearch(c.Request.Context(), c.Param("id"), types.SearchParams{
		QueryText: req.Query, MatchCount: req.Limit,
		DisableKeywordsMatch: req.Mode == "vector", DisableVectorMatch: req.Mode == "keyword",
	})
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "Retrieval unavailable; inspect model and index status"})
		return
	}
	if len(results) > req.Limit {
		results = results[:req.Limit]
	}
	c.JSON(http.StatusOK, HybridSearchResponse{Success: true, Data: results})
}
