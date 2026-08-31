package handler

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
)

type entityGraphReader interface {
	GetGraph(context.Context, types.NameSpace, types.GraphQuery) (*types.GraphQueryResult, error)
}

type EntityGraphHandler struct {
	reader entityGraphReader
}

func NewEntityGraphHandler(repository interfaces.RetrieveGraphRepository) *EntityGraphHandler {
	reader, _ := repository.(entityGraphReader)
	return &EntityGraphHandler{reader: reader}
}

func (h *EntityGraphHandler) GetGraph(c *gin.Context) {
	if h.reader == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"success": false, "error": "entity graph is unavailable"})
		return
	}
	kbID := strings.TrimSpace(c.Param("kb_id"))
	if kbID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "knowledge base is required"})
		return
	}
	limit := 500
	if raw := strings.TrimSpace(c.Query("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 {
			c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "limit must be a positive integer"})
			return
		}
		limit = parsed
	}
	attributes := make([]string, 0)
	for _, value := range strings.Split(c.Query("attributes"), ",") {
		if value = strings.TrimSpace(value); value != "" {
			attributes = append(attributes, value)
		}
	}
	result, err := h.reader.GetGraph(c.Request.Context(), types.NameSpace{KnowledgeBase: kbID}, types.GraphQuery{
		Limit:      limit,
		Attributes: attributes,
	})
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"success": false, "error": err.Error()})
		return
	}
	if result == nil {
		result = &types.GraphQueryResult{}
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"nodes": result.Node,
			"edges": result.Relation,
			"meta": gin.H{
				"mode":      "overview",
				"total":     result.TotalNodes,
				"returned":  len(result.Node),
				"truncated": len(result.Node) < result.TotalNodes,
			},
		},
	})
}
