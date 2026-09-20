package session

import (
	"net/http"
	"strings"

	"github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
)

// ListArtifactLibrary godoc
// @Summary      列出我的产物
// @Description  跨会话列出当前用户可见会话中技能生成的文件，每个文件只返回最新版本（不含存储 URL）
// @Tags         会话
// @Produce      json
// @Param        keyword     query  string  false  "按文件名过滤"
// @Param        file_types  query  string  false  "逗号分隔的扩展名，如 .pdf,.pptx"
// @Param        page        query  int     false  "页码"
// @Param        page_size   query  int     false  "每页数量（最大 100）"
// @Success      200  {object}  map[string]interface{}
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /artifacts [get]
//
// Items carry session_id, message_id and index so the client downloads
// through the per-session endpoint, which re-runs the ownership check.
func (h *Handler) ListArtifactLibrary(c *gin.Context) {
	ctx := c.Request.Context()

	var pagination types.Pagination
	if err := c.ShouldBindQuery(&pagination); err != nil {
		_ = c.Error(errors.NewBadRequestError(err.Error()))
		return
	}
	var fileTypes []string
	if raw := c.Query("file_types"); raw != "" {
		fileTypes = strings.Split(raw, ",")
	}

	result, err := h.messageService.ListArtifactLibrary(ctx, &types.ArtifactLibraryQuery{
		Keyword:   c.Query("keyword"),
		FileTypes: fileTypes,
		Page:      pagination.Page,
		PageSize:  pagination.PageSize,
	})
	if err != nil {
		logger.Errorf(ctx, "list artifact library failed: %v", err)
		_ = c.Error(errors.NewInternalServerError(err.Error()))
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success":   true,
		"data":      result.Data,
		"total":     result.Total,
		"page":      result.Page,
		"page_size": result.PageSize,
	})
}
