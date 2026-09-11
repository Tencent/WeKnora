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

// ListSandboxLiveFiles godoc
// @Summary      列出会话沙箱 /workspace/output 下一层文件
// @Description  返回固定输出根下的一层目录条目。路径为相对 POSIX 路径，空路径表示根。不会创建或唤醒已暂停的沙箱。
// @Tags         会话
// @Produce      json
// @Param        id    path   string  true  "会话ID"
// @Param        path  query  string  false "相对目录，空为输出根"
// @Success      200   {object}  map[string]interface{}
// @Failure      400   {object}  apperrors.AppError
// @Failure      404   {object}  apperrors.AppError
// @Failure      409   {object}  apperrors.AppError
// @Security     Bearer
// @Router       /sessions/{id}/sandbox/files [get]
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

// DownloadSandboxLiveFile godoc
// @Summary      下载会话沙箱中的一个输出文件
// @Description  以附件形式下载 /workspace/output 下的单个常规文件，上限 16 MiB。不会唤醒已暂停的沙箱。
// @Tags         会话
// @Produce      application/octet-stream
// @Param        id    path   string  true  "会话ID"
// @Param        path  query  string  true  "相对文件路径"
// @Success      200   {file}    file
// @Failure      400   {object}  apperrors.AppError
// @Failure      404   {object}  apperrors.AppError
// @Failure      409   {object}  apperrors.AppError
// @Failure      413   {object}  map[string]interface{}
// @Security     Bearer
// @Router       /sessions/{id}/sandbox/files/content [get]
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

// UploadSandboxLiveFile godoc
// @Summary      上传文件到会话沙箱输出目录
// @Description  以 multipart 写入一个新文件，不覆盖已有条目。字段 path 为相对路径，file 为文件本体，上限 16 MiB。
// @Tags         会话
// @Accept       multipart/form-data
// @Produce      json
// @Param        session_id  path      string  true  "会话ID"
// @Param        path        formData  string  true  "相对目标路径"
// @Param        file        formData  file    true  "文件内容"
// @Success      201         {object}  map[string]interface{}
// @Failure      400         {object}  apperrors.AppError
// @Failure      404         {object}  apperrors.AppError
// @Failure      409         {object}  apperrors.AppError
// @Failure      413         {object}  map[string]interface{}
// @Security     Bearer
// @Router       /sessions/{session_id}/sandbox/files [post]
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

// RenameSandboxLiveFile godoc
// @Summary      重命名会话沙箱输出条目
// @Description  在 /workspace/output 内原子重命名，目标已存在则冲突。不会覆盖。
// @Tags         会话
// @Accept       json
// @Produce      json
// @Param        id       path      string                 true  "会话ID"
// @Param        request  body      renameLiveFileRequest  true  "源路径与目标路径"
// @Success      200      {object}  map[string]interface{}
// @Failure      400      {object}  apperrors.AppError
// @Failure      404      {object}  apperrors.AppError
// @Failure      409      {object}  apperrors.AppError
// @Security     Bearer
// @Router       /sessions/{id}/sandbox/files [patch]
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

// DeleteSandboxLiveFile godoc
// @Summary      删除会话沙箱输出条目
// @Description  删除 /workspace/output 下的安全文件或已预校验的目录树。
// @Tags         会话
// @Param        id    path   string  true  "会话ID"
// @Param        path  query  string  true  "相对路径"
// @Success      204   "No Content"
// @Failure      400   {object}  apperrors.AppError
// @Failure      404   {object}  apperrors.AppError
// @Failure      409   {object}  apperrors.AppError
// @Security     Bearer
// @Router       /sessions/{id}/sandbox/files [delete]
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
	case stderrors.Is(err, sandbox.ErrLiveFileTooMany):
		_ = c.Error(apperrors.NewBadRequestError("directory is too large to list or delete"))
	case stderrors.Is(err, sandbox.ErrLiveFileNotFound):
		_ = c.Error(apperrors.NewNotFoundError("file not found"))
	case stderrors.Is(err, sandbox.ErrLiveFileConflict):
		_ = c.Error(apperrors.NewConflictError("destination already exists"))
	case stderrors.Is(err, sandbox.ErrLiveFileTooLarge):
		handleLiveFileTooLarge(c)
	case stderrors.Is(err, sandbox.ErrNoLiveSessionSandbox):
		_ = c.Error(apperrors.NewConflictError("session has no live sandbox"))
	case stderrors.Is(err, sandbox.ErrSandboxPaused):
		_ = c.Error(apperrors.NewConflictError("session sandbox is paused"))
	case stderrors.Is(err, service.ErrLiveFilesUnsupported):
		_ = c.Error(apperrors.NewServiceUnavailableError("sandbox files are unsupported"))
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
