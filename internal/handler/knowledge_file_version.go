package handler

import (
	"net/http"
	"strconv"

	"github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/filetransport"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/Tencent/WeKnora/internal/utils"
	"github.com/gin-gonic/gin"
)

func (h *KnowledgeHandler) fileVersionService(c *gin.Context) interfaces.KnowledgeFileVersionService {
	svc, ok := h.kgService.(interfaces.KnowledgeFileVersionService)
	if !ok {
		_ = c.Error(errors.NewServiceUnavailableError("file versions unavailable"))
		return nil
	}
	return svc
}

// ListKnowledgeFileVersions returns current and archived original-file versions.
func (h *KnowledgeHandler) ListKnowledgeFileVersions(c *gin.Context) {
	_, ctx, err := h.resolveKnowledgeAndValidateKBAccess(c, c.Param("id"), types.OrgRoleViewer)
	if err != nil {
		_ = c.Error(err)
		return
	}
	limit, err := strconv.Atoi(c.DefaultQuery("limit", "20"))
	if err != nil || limit < 1 || limit > 100 {
		_ = c.Error(errors.NewBadRequestError("limit must be between 1 and 100"))
		return
	}
	offset, err := strconv.Atoi(c.DefaultQuery("offset", "0"))
	if err != nil || offset < 0 {
		_ = c.Error(errors.NewBadRequestError("invalid offset"))
		return
	}
	svc := h.fileVersionService(c)
	if svc == nil {
		return
	}
	result, err := svc.ListKnowledgeFileVersions(ctx, c.Param("id"), limit, offset)
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": result})
}

// UploadKnowledgeFileVersion replaces a file while preserving its history.
func (h *KnowledgeHandler) UploadKnowledgeFileVersion(c *gin.Context) {
	_, ctx, err := h.resolveKnowledgeAndValidateKBAccess(c, c.Param("id"), types.OrgRoleEditor)
	if err != nil {
		_ = c.Error(err)
		return
	}
	svc := h.fileVersionService(c)
	if svc == nil {
		return
	}
	maxSize := utils.GetMaxFileSizeMB() * 1024 * 1024
	limitUploadBody(c, maxSize)
	file, err := c.FormFile("file")
	if c.Request.MultipartForm != nil {
		defer func() { _ = c.Request.MultipartForm.RemoveAll() }()
	}
	if err != nil || file.Size > maxSize {
		_ = c.Error(errors.NewBadRequestError("invalid file or file exceeds upload limit"))
		return
	}
	expected := 0
	if raw := c.PostForm("expected_version"); raw != "" {
		expected, err = strconv.Atoi(raw)
		if err != nil || expected < 1 {
			_ = c.Error(errors.NewBadRequestError("invalid expected_version"))
			return
		}
	}
	knowledge, err := svc.UploadKnowledgeFileVersion(ctx, c.Param("id"), file, expected)
	if err != nil {
		if _, duplicate := err.(*types.DuplicateKnowledgeError); duplicate {
			_ = c.Error(errors.NewConflictError("this file version already exists"))
			return
		}
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": knowledge})
}

// DownloadKnowledgeFileVersion serves an authorized original-file version.
func (h *KnowledgeHandler) DownloadKnowledgeFileVersion(c *gin.Context) {
	_, ctx, err := h.resolveKnowledgeAndValidateKBAccess(c, c.Param("id"), types.OrgRoleEditor)
	if err != nil {
		_ = c.Error(err)
		return
	}
	version, err := strconv.Atoi(c.Param("version"))
	if err != nil || version < 1 {
		_ = c.Error(errors.NewBadRequestError("invalid version"))
		return
	}
	svc := h.fileVersionService(c)
	if svc == nil {
		return
	}
	file, name, err := svc.GetKnowledgeFileVersion(ctx, c.Param("id"), version)
	if err != nil {
		_ = c.Error(err)
		return
	}
	if err := filetransport.Serve(c.Writer, c.Request, file, filetransport.Options{
		Filename: name, Download: true, ContentType: "application/octet-stream", CacheControl: "private, no-store",
	}); err != nil {
		logger.Errorf(ctx, "Failed to send file version: %v", err)
	}
}
