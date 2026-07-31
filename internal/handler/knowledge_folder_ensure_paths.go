package handler

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"

	"github.com/Tencent/WeKnora/internal/application/service"
	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/handler/dto"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
)

const knowledgeFolderEnsurePathsMaxBodyBytes int64 = 1 << 20

// EnsurePaths resolves or creates folder paths atomically.
func (h *KnowledgeFolderHandler) EnsurePaths(c *gin.Context) {
	limitedBody := http.MaxBytesReader(
		c.Writer,
		c.Request.Body,
		knowledgeFolderEnsurePathsMaxBodyBytes,
	)
	defer limitedBody.Close()

	// Consume the complete body so the byte limit also covers trailing data.
	body, err := io.ReadAll(limitedBody)
	if err != nil {
		c.Error(apperrors.NewBadRequestError("请求参数不合法"))
		return
	}
	if !json.Valid(body) {
		c.Error(apperrors.NewBadRequestError("请求参数不合法"))
		return
	}
	c.Request.Body = io.NopCloser(bytes.NewReader(body))

	var req types.KnowledgeFolderEnsurePathsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(apperrors.NewBadRequestError("请求参数不合法"))
		return
	}

	results, err := h.service.EnsurePaths(c.Request.Context(), c.Param("id"), &req)
	if err != nil {
		writeKnowledgeFolderError(c, err)
		return
	}
	if len(results) == 0 {
		writeKnowledgeFolderError(c, service.ErrKnowledgeFolderDataIntegrity)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    dto.NewKnowledgeFolderEnsurePathsResponse(results),
	})
}
