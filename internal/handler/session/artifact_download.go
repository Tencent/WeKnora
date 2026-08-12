package session

import (
	stderrors "errors"
	"io"
	"mime"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	secutils "github.com/Tencent/WeKnora/internal/utils"
	"github.com/gin-gonic/gin"
)

// paramSessionID resolves the session-id URL parameter regardless of which
// wildcard name the current route uses. GET-tree routes bind :id (to align
// with /sessions/:id), while POST-tree routes typically bind :session_id.
// Handlers call this helper instead of hard-coding one name so the same
// function serves both trees.
func paramSessionID(c *gin.Context) string {
	if v := c.Param("session_id"); v != "" {
		return v
	}
	return c.Param("id")
}

// ListSessionArtifacts godoc
// @Summary      列出会话生成的产物文件
// @Description  返回本会话中所有 assistant 消息产生的技能产物元数据（不含 URL）
// @Tags         会话
// @Produce      json
// @Param        session_id  path  string  true  "会话ID"
// @Success      200  {object}  map[string]interface{}
// @Failure      404  {object}  errors.AppError
// @Security     Bearer
// @Router       /sessions/{session_id}/artifacts [get]
//
// The endpoint powers the drawer that lists every file generated in the
// session; it does NOT return the storage URL (only names/sizes/mtimes), so
// clients cannot reach around the download endpoint by reading a
// provider:// path from the API response.
func (h *Handler) ListSessionArtifacts(c *gin.Context) {
	ctx := c.Request.Context()
	sessionID := secutils.SanitizeForLog(paramSessionID(c))
	if sessionID == "" {
		c.Error(errors.NewBadRequestError(errors.ErrInvalidSessionID.Error()))
		return
	}

	// Ownership + tenant check: GetSession enforces both. Returning 404 for
	// unknown / non-owned sessions matches the rest of the session routes.
	if _, err := h.sessionService.GetSession(ctx, sessionID); err != nil {
		if stderrors.Is(err, errors.ErrSessionNotFound) {
			c.Error(errors.NewNotFoundError(err.Error()))
			return
		}
		c.Error(errors.NewInternalServerError(err.Error()))
		return
	}

	artifacts, err := h.messageService.GetSessionArtifacts(ctx, sessionID)
	if err != nil {
		logger.Errorf(ctx, "list session artifacts failed: session=%s err=%v", sessionID, err)
		c.Error(errors.NewInternalServerError(err.Error()))
		return
	}

	// Strip the storage URL before serialising to the client; the URL is a
	// server-side implementation detail (provider:// path) that must never
	// escape the process. The client references artifacts by index in the
	// download URL instead.
	items := make([]artifactListItem, 0, len(artifacts))
	for i, a := range artifacts {
		items = append(items, artifactListItem{
			Index:      i,
			FileName:   a.FileName,
			FileType:   a.FileType,
			FileSize:   a.FileSize,
			SourcePath: a.SourcePath,
			ModTime:    a.ModTime,
			CreatedAt:  a.CreatedAt,
		})
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    items,
	})
}

// ListMessageArtifacts returns just the artifacts attached to a single
// assistant message. Used by the "download files from this reply" button on
// each bot message.
//
// Same-tenant/same-owner check flows through h.sessionService.GetSession
// exactly like ListSessionArtifacts.
//
// @Router /sessions/{session_id}/messages/{message_id}/artifacts [get]
func (h *Handler) ListMessageArtifacts(c *gin.Context) {
	ctx := c.Request.Context()
	sessionID := secutils.SanitizeForLog(paramSessionID(c))
	messageID := secutils.SanitizeForLog(c.Param("message_id"))
	if sessionID == "" || messageID == "" {
		c.Error(errors.NewBadRequestError("session_id and message_id are required"))
		return
	}

	if _, err := h.sessionService.GetSession(ctx, sessionID); err != nil {
		if stderrors.Is(err, errors.ErrSessionNotFound) {
			c.Error(errors.NewNotFoundError(err.Error()))
			return
		}
		c.Error(errors.NewInternalServerError(err.Error()))
		return
	}

	msg, err := h.messageService.GetMessage(ctx, sessionID, messageID)
	if err != nil || msg == nil {
		c.Error(errors.NewNotFoundError("message not found"))
		return
	}

	items := make([]artifactListItem, 0, len(msg.Artifacts))
	for i, a := range msg.Artifacts {
		items = append(items, artifactListItem{
			Index:      i,
			FileName:   a.FileName,
			FileType:   a.FileType,
			FileSize:   a.FileSize,
			SourcePath: a.SourcePath,
			ModTime:    a.ModTime,
			CreatedAt:  a.CreatedAt,
		})
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    items,
	})
}

// DownloadMessageArtifact streams a single skill-generated file to the
// client. Clients reference the artifact by its position (:index) in the
// assistant message's Artifacts array; the storage URL never leaves the
// server so callers cannot pivot to arbitrary blobs.
//
// @Router /sessions/{session_id}/messages/{message_id}/artifacts/{index}/download [get]
func (h *Handler) DownloadMessageArtifact(c *gin.Context) {
	ctx := c.Request.Context()

	sessionID := secutils.SanitizeForLog(paramSessionID(c))
	messageID := secutils.SanitizeForLog(c.Param("message_id"))
	indexParam := c.Param("index")
	if sessionID == "" || messageID == "" || indexParam == "" {
		c.Error(errors.NewBadRequestError("session_id, message_id and index are required"))
		return
	}
	index, err := strconv.Atoi(indexParam)
	if err != nil || index < 0 {
		c.Error(errors.NewBadRequestError("invalid artifact index"))
		return
	}

	// Ownership check: GetSession returns ErrSessionNotFound when the
	// session doesn't belong to the calling tenant/user, so a 404 covers
	// both "not found" and "forbidden" without leaking existence.
	if _, err := h.sessionService.GetSession(ctx, sessionID); err != nil {
		if stderrors.Is(err, errors.ErrSessionNotFound) {
			c.Error(errors.NewNotFoundError(err.Error()))
			return
		}
		c.Error(errors.NewInternalServerError(err.Error()))
		return
	}

	msg, err := h.messageService.GetMessage(ctx, sessionID, messageID)
	if err != nil || msg == nil {
		c.Error(errors.NewNotFoundError("message not found"))
		return
	}
	if index >= len(msg.Artifacts) {
		c.Error(errors.NewNotFoundError("artifact index out of range"))
		return
	}
	artifact := msg.Artifacts[index]
	if artifact.URL == "" {
		c.Error(errors.NewNotFoundError("artifact storage path missing"))
		return
	}

	if h.fileService == nil {
		c.Error(errors.NewInternalServerError("file service unavailable"))
		return
	}
	reader, err := h.fileService.GetFile(ctx, artifact.URL)
	if err != nil {
		logger.Warnf(ctx, "artifact download read failed: session=%s message=%s idx=%d err=%v",
			sessionID, messageID, index, err)
		c.Error(errors.NewNotFoundError("artifact blob missing"))
		return
	}
	defer reader.Close()

	// Force download semantics — artifacts are never rendered inline, matching
	// the /files endpoint's active-content protection.
	c.Header("Content-Type", mimeTypeFor(artifact.FileName))
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("Content-Disposition", buildAttachmentHeader(artifact.FileName))
	if artifact.FileSize > 0 {
		c.Header("Content-Length", strconv.FormatInt(artifact.FileSize, 10))
	}
	c.Status(http.StatusOK)
	if _, err := io.Copy(c.Writer, reader); err != nil {
		logger.Warnf(ctx, "artifact download stream failed: session=%s message=%s idx=%d err=%v",
			sessionID, messageID, index, err)
	}
}

// artifactListItem is the JSON shape returned by ListSessionArtifacts /
// ListMessageArtifacts. It intentionally omits the URL: clients reference
// each artifact through its position in the parent message's Artifacts
// array so the storage path never leaves the server.
type artifactListItem struct {
	Index      int    `json:"index"`
	FileName   string `json:"file_name"`
	FileType   string `json:"file_type"`
	FileSize   int64  `json:"file_size"`
	SourcePath string `json:"source_path"`
	// time-typed fields serialise as RFC3339 strings — same convention as
	// the rest of the messages API.
	ModTime   any `json:"mod_time"`
	CreatedAt any `json:"created_at"`
}

// mimeTypeFor picks a Content-Type by extension and falls back to
// application/octet-stream so unknown types force a download prompt rather
// than being sniffed. Kept private to this file — the /files route has a
// stricter version with an SVG-neutralising branch; we don't need that here
// because Content-Disposition already blocks inline rendering.
func mimeTypeFor(name string) string {
	ext := strings.ToLower(filepath.Ext(name))
	if ext == "" {
		return "application/octet-stream"
	}
	if ct := mime.TypeByExtension(ext); ct != "" {
		return ct
	}
	return "application/octet-stream"
}

// buildAttachmentHeader returns a Content-Disposition value that preserves
// non-ASCII filenames (RFC 5987) while providing a safe fallback for
// ASCII-only clients.
func buildAttachmentHeader(name string) string {
	// Strip control characters + quotes; keep the human-readable name.
	ascii := strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		if r == '"' || r == '\\' {
			return '_'
		}
		if r > 0x7e {
			return -1
		}
		return r
	}, name)
	if ascii == "" {
		ascii = "download"
	}
	encoded := (&urlPathEscaper{}).escape(name)
	return "attachment; filename=\"" + ascii + "\"; filename*=UTF-8''" + encoded
}

// urlPathEscaper is a minimal RFC 3986 percent-encoder for the subset of
// bytes allowed in a filename*= value. We inline it to avoid importing
// net/url just for a two-line call, and because url.PathEscape encodes
// spaces as "+" (form-encoding) which HTTP clients then decode as literal
// "+" characters in the filename.
type urlPathEscaper struct{}

// escape percent-encodes every byte outside the "attr-char" grammar of RFC 5987.
// See https://datatracker.ietf.org/doc/html/rfc5987#section-3.2.1
func (urlPathEscaper) escape(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	const hex = "0123456789ABCDEF"
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case (c >= 'A' && c <= 'Z'), (c >= 'a' && c <= 'z'), (c >= '0' && c <= '9'):
			b.WriteByte(c)
		case c == '-' || c == '.' || c == '_' || c == '~':
			b.WriteByte(c)
		default:
			b.WriteByte('%')
			b.WriteByte(hex[c>>4])
			b.WriteByte(hex[c&0x0f])
		}
	}
	return b.String()
}

// ensureAssistantOwnsArtifact is a small helper reserved for future
// authorisation refinements (per-message role checks). It is unused today
// but kept close to the handlers so future changes stay obvious.
var _ = types.MessageArtifact{}
