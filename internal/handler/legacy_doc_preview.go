package handler

import (
	"context"
	"errors"
	"io"
	"mime"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/infrastructure/docparser"
	"github.com/gin-gonic/gin"
)

// Called only after the existing Viewer authorization check. A ready preview
// never opens the original DOC or invokes the converter in the HTTP request.
func (h *KnowledgeHandler) previewLegacyDoc(
	ctx context.Context,
	c *gin.Context,
	tenant uint64,
	id string,
	filename string,
) {
	c.Header("Cache-Control", "no-store")
	if h.preview == nil {
		c.JSON(http.StatusUnsupportedMediaType, gin.H{"code": "preview_unsupported"})
		return
	}
	result, err := h.preview.Get(ctx, tenant, id, c.Query("retry") == "1")
	if err != nil {
		if errors.Is(err, repository.ErrPreviewGone) {
			c.JSON(http.StatusNotFound, gin.H{"code": "preview_not_found"})
			return
		}
		c.JSON(http.StatusServiceUnavailable, gin.H{"code": "preview_unavailable"})
		return
	}
	if result.Status == "pending" {
		c.Header("Retry-After", "2")
		c.JSON(http.StatusAccepted, gin.H{"code": "preview_pending", "retry_after": 2})
		return
	}
	if result.Status != "ready" || result.Content == nil {
		c.JSON(http.StatusUnsupportedMediaType, gin.H{"code": "preview_unsupported"})
		return
	}
	defer func() { _ = result.Content.Close() }()
	filename = filepath.Base(strings.ReplaceAll(filename, "\\", "/"))
	filename = strings.TrimSuffix(filename, filepath.Ext(filename)) + ".docx"
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("Content-Disposition", mime.FormatMediaType("inline", map[string]string{"filename": filename}))
	c.Header("Content-Type", docparser.LegacyDocPreviewMIME)
	c.Status(http.StatusOK)
	_, _ = io.Copy(c.Writer, result.Content)
}
