package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/Tencent/WeKnora/internal/models/embedding"
)

// EmbeddingCacheHandler exposes embedding cache statistics.
type EmbeddingCacheHandler struct{}

// NewEmbeddingCacheHandler constructs the handler.
func NewEmbeddingCacheHandler() *EmbeddingCacheHandler {
	return &EmbeddingCacheHandler{}
}

// Stats godoc
// @Summary      Embedding 缓存统计
// @Description  返回进程级命中/未命中计数
// @Tags         Embedding 缓存
// @Produce      json
// @Router       /embedding-cache/stats [get]
func (h *EmbeddingCacheHandler) Stats(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"success": true, "data": embedding.CacheStats()})
}
