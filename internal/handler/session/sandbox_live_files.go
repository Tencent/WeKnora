package session

import (
	"context"
	stderrors "errors"
	"io"
	"net/http"
	"path"

	"github.com/gin-gonic/gin"

	"github.com/Tencent/WeKnora/internal/application/service"
	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/sandbox"
	secutils "github.com/Tencent/WeKnora/internal/utils"
)

const maxLiveFileUploadRequestBytes = sandbox.MaxSessionLiveFileBytes + (1 << 20)

type sandboxLiveFilesService interface {
	List(context.Context, string, string) ([]sandbox.SessionLiveFileEntry, error)
	Read(context.Context, string, string) ([]byte, error)
	Write(context.Context, string, string, []byte) error
	Rename(context.Context, string, string, string) error
	Remove(context.Context, string, string) error
}

type renameLiveFileRequest struct {
	Source string `json:"source" binding:"required"`
	Target string `json:"target" binding:"required"`
}

// ListSandboxLiveFiles returns one live output directory level.
func (h *Handler) ListSandboxLiveFiles(c *gin.Context) {
	sessionID, ok := h.authorizeLiveFilesSession(c, false)
	if !ok {
		return
	}
	entries, err := h.liveFilesService.List(c.Request.Context(), sessionID, c.Query("path"))
	if err != nil {
		h.handleLiveFileError(c, sessionID, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": entries})
}

// DownloadSandboxLiveFile streams one live file as a non-cacheable attachment.
func (h *Handler) DownloadSandboxLiveFile(c *gin.Context) {
	sessionID, ok := h.authorizeLiveFilesSession(c, false)
	if !ok {
		return
	}
	relativePath := c.Query("path")
	content, err := h.liveFilesService.Read(c.Request.Context(), sessionID, relativePath)
	if err != nil {
		h.handleLiveFileError(c, sessionID, err)
		return
	}
	name := path.Base(relativePath)
	c.Header("Cache-Control", "private, no-store")
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("Content-Disposition", buildAttachmentHeader(name))
	c.Data(http.StatusOK, mimeTypeFor(name), content)
}

// UploadSandboxLiveFile writes one bounded multipart upload into the live output tree.
func (h *Handler) UploadSandboxLiveFile(c *gin.Context) {
	sessionID, ok := h.authorizeLiveFilesSession(c, true)
	if !ok {
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxLiveFileUploadRequestBytes)
	if err := c.Request.ParseMultipartForm(maxLiveFileUploadRequestBytes); err != nil {
		var maxBytesError *http.MaxBytesError
		if stderrors.As(err, &maxBytesError) {
			handleLiveFileTooLarge(c)
		} else {
			_ = c.Error(apperrors.NewBadRequestError("invalid multipart upload"))
		}
		return
	}
	if c.Request.MultipartForm != nil {
		defer func() {
			if err := c.Request.MultipartForm.RemoveAll(); err != nil {
				logger.Warnf(c.Request.Context(),
					"clean sandbox upload form failed: session=%s err=%v", sessionID, err)
			}
		}()
	}
	relativePath := c.PostForm("path")
	header, err := c.FormFile("file")
	if err != nil {
		_ = c.Error(apperrors.NewBadRequestError("file is required"))
		return
	}
	file, err := header.Open()
	if err != nil {
		_ = c.Error(apperrors.NewBadRequestError("invalid upload"))
		return
	}
	defer func() {
		if err := file.Close(); err != nil {
			logger.Warnf(c.Request.Context(),
				"close sandbox upload failed: session=%s err=%v", sessionID, err)
		}
	}()
	content, err := io.ReadAll(io.LimitReader(file, sandbox.MaxSessionLiveFileBytes+1))
	if err != nil {
		_ = c.Error(apperrors.NewBadRequestError("invalid upload"))
		return
	}
	if len(content) > sandbox.MaxSessionLiveFileBytes {
		handleLiveFileTooLarge(c)
		return
	}
	if err := h.liveFilesService.Write(c.Request.Context(), sessionID, relativePath, content); err != nil {
		h.handleLiveFileError(c, sessionID, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"success": true})
}

// RenameSandboxLiveFile moves one live-file entry within the fixed output tree.
func (h *Handler) RenameSandboxLiveFile(c *gin.Context) {
	sessionID, ok := h.authorizeLiveFilesSession(c, true)
	if !ok {
		return
	}
	var request renameLiveFileRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		_ = c.Error(apperrors.NewBadRequestError("source and target are required"))
		return
	}
	if err := h.liveFilesService.Rename(
		c.Request.Context(), sessionID, request.Source, request.Target,
	); err != nil {
		h.handleLiveFileError(c, sessionID, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// DeleteSandboxLiveFile removes one live-file entry from the fixed output tree.
func (h *Handler) DeleteSandboxLiveFile(c *gin.Context) {
	sessionID, ok := h.authorizeLiveFilesSession(c, true)
	if !ok {
		return
	}
	if err := h.liveFilesService.Remove(c.Request.Context(), sessionID, c.Query("path")); err != nil {
		h.handleLiveFileError(c, sessionID, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *Handler) authorizeLiveFilesSession(c *gin.Context, mutation bool) (string, bool) {
	sessionID := secutils.SanitizeForLog(paramSessionID(c))
	if sessionID == "" {
		_ = c.Error(apperrors.NewBadRequestError(apperrors.ErrInvalidSessionID.Error()))
		return "", false
	}
	if h.liveFilesService == nil {
		_ = c.Error(apperrors.NewServiceUnavailableError("sandbox files are unavailable"))
		return "", false
	}
	var err error
	if mutation {
		_, err = h.sessionService.GetOwnedSession(c.Request.Context(), sessionID)
	} else {
		_, err = h.sessionService.GetSession(c.Request.Context(), sessionID)
	}
	if err == nil {
		return sessionID, true
	}
	if stderrors.Is(err, apperrors.ErrSessionNotFound) {
		_ = c.Error(apperrors.NewNotFoundError("session not found"))
		return "", false
	}
	logger.Errorf(c.Request.Context(), "authorize sandbox files failed: session=%s err=%v", sessionID, err)
	_ = c.Error(apperrors.NewInternalServerError("failed to authorize session"))
	return "", false
}

func (h *Handler) handleLiveFileError(c *gin.Context, sessionID string, err error) {
	switch {
	case stderrors.Is(err, sandbox.ErrLiveFileInvalidPath), stderrors.Is(err, sandbox.ErrLiveFileUnsafe):
		_ = c.Error(apperrors.NewBadRequestError("invalid or unsafe file path"))
	case stderrors.Is(err, sandbox.ErrLiveFileNotFound):
		_ = c.Error(apperrors.NewNotFoundError("file not found"))
	case stderrors.Is(err, sandbox.ErrLiveFileConflict):
		_ = c.Error(apperrors.NewConflictError("destination already exists"))
	case stderrors.Is(err, sandbox.ErrLiveFileTooLarge):
		handleLiveFileTooLarge(c)
	case stderrors.Is(err, sandbox.ErrNoLiveSessionSandbox):
		_ = c.Error(apperrors.NewConflictError("session has no live sandbox"))
	case stderrors.Is(err, service.ErrLiveFilesUnsupported):
		c.JSON(http.StatusNotImplemented, gin.H{"success": false, "message": "sandbox files are unsupported"})
	default:
		logger.Errorf(c.Request.Context(), "sandbox live-file operation failed: session=%s err=%v", sessionID, err)
		_ = c.Error(apperrors.NewInternalServerError("sandbox file operation failed"))
	}
}

func handleLiveFileTooLarge(c *gin.Context) {
	c.JSON(http.StatusRequestEntityTooLarge, gin.H{
		"success": false,
		"message": "file exceeds the 16 MiB limit",
	})
}

var _ sandboxLiveFilesService = (*service.SandboxLiveFilesService)(nil)
