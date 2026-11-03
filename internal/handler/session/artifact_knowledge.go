package session

import (
	"bytes"
	stderrors "errors"
	"io"
	"mime/multipart"
	"net/http"
	"strconv"
	"strings"

	"github.com/Tencent/WeKnora/internal/application/access"
	"github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/types"
	secutils "github.com/Tencent/WeKnora/internal/utils"
	"github.com/gin-gonic/gin"
)

// addArtifactToKnowledgeRequest is the body of the artifact → knowledge base
// quick action. The artifact itself is identified by the URL path.
type addArtifactToKnowledgeRequest struct {
	KnowledgeBaseID string `json:"knowledge_base_id" binding:"required"`
}

// AddMessageArtifactToKnowledge imports a generated artifact directly into a
// knowledge base as a file, preserving its format. It is the file-level
// counterpart of the "add answer to knowledge base" editor action.
//
// Access: the caller must be able to read the session (the same ownership and
// channel rules as artifact download) and hold editor/write permission on the
// target knowledge base. The artifact bytes are read through the existing
// artifact access resolver, so a shared-session artifact cannot be used to
// import a blob the caller could not download.
//
// @Summary      将消息产物加入知识库
// @Description  把 assistant 消息中生成的产物文件直接导入指定知识库（保留原文件格式）
// @Tags         会话
// @Accept       json
// @Produce      json
// @Param        session_id  path  string  true  "会话ID"
// @Param        message_id  path  string  true  "消息ID"
// @Param        index       path  int     true  "产物索引"
// @Param        request     body  addArtifactToKnowledgeRequest  true  "{knowledge_base_id}"
// @Success      200  {object}  map[string]interface{}
// @Failure      400  {object}  errors.AppError
// @Failure      403  {object}  errors.AppError
// @Failure      404  {object}  errors.AppError
// @Failure      409  {object}  map[string]interface{}
// @Security     Bearer
// @Router       /sessions/{session_id}/messages/{message_id}/artifacts/{index}/knowledge [post]
func (h *Handler) AddMessageArtifactToKnowledge(c *gin.Context) {
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

	var req addArtifactToKnowledgeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(errors.NewBadRequestError("knowledge_base_id is required"))
		return
	}
	kbID := strings.TrimSpace(req.KnowledgeBaseID)
	if kbID == "" {
		c.Error(errors.NewBadRequestError("knowledge_base_id is required"))
		return
	}

	// This action writes into a workspace resource. Public embed visitors, IM
	// users and MCP endpoints have no account that can hold KB write permission
	// on their behalf; only signed-in web users and API-key principals (which
	// carry the ingest capability) may import. Without this gate an embed
	// session could write to any same-tenant KB ResolveKB resolves.
	if principal, ok := types.PrincipalFromContext(ctx); !ok ||
		principal.Type == types.PrincipalEmbedSession ||
		principal.Type == types.PrincipalEmbedVisitor ||
		principal.Type == types.PrincipalIMUser ||
		principal.Type == types.PrincipalMCPEndpoint ||
		strings.HasPrefix(strings.TrimSpace(principal.ActingVia), "im:") {
		c.Error(errors.NewForbiddenError("adding artifacts to a knowledge base requires a signed-in user"))
		return
	}

	// Session ownership + tenant check (same gate as list/download). Returns
	// 404 for unknown or non-owned sessions to avoid leaking existence.
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
	if artifact.Deleted() {
		c.Error(errors.NewNotFoundError("artifact deleted"))
		return
	}
	if artifact.URL == "" {
		c.Error(errors.NewNotFoundError("artifact storage path missing"))
		return
	}

	// Knowledge-base write permission. ResolveKB handles tenant API-key KB
	// scoping, organization shares and agent shares; RequireKBWrite rejects a
	// read-only grant and an API key without the ingest capability.
	if h.knowledgebaseService == nil || h.knowledgeService == nil {
		c.Error(errors.NewInternalServerError("knowledge service unavailable"))
		return
	}
	kb, err := h.knowledgebaseService.GetKnowledgeBaseByID(ctx, kbID)
	if err != nil || kb == nil {
		c.Error(errors.NewNotFoundError("knowledge base not found"))
		return
	}
	grant, err := access.ResolveKB(
		ctx,
		middleware.KBAccessRequest(c),
		kb,
		types.OrgRoleEditor,
		h.kbShareService,
		h.agentShareService,
	)
	if err != nil {
		writeArtifactKnowledgeAccessError(c, err)
		return
	}
	if err := access.RequireKBWrite(grant.Context(ctx), kb); err != nil {
		writeArtifactKnowledgeAccessError(c, err)
		return
	}

	file, err := access.ResolveMessageArtifact(ctx, msg, index, h.agentShareService, h.resourceCatalog,
		access.MessageKBShareAuthorizer{ShareGuard: h.kbShareService, KBs: h.knowledgebaseService})
	if err != nil {
		c.Error(errors.NewNotFoundError("artifact not accessible"))
		return
	}
	fileService, readCtx, ok := h.resolveArtifactFileService(
		ctx, file.OwnerTenantID, file.Path, file.StorageBackendID, "artifact to knowledge",
	)
	if !ok {
		c.Error(errors.NewNotFoundError("artifact storage unavailable"))
		return
	}
	reader, err := fileService.GetFile(readCtx, file.Path)
	if err != nil {
		logger.Warnf(ctx, "artifact knowledge read failed: session=%s message=%s idx=%d err=%v",
			sessionID, messageID, index, err)
		c.Error(errors.NewNotFoundError("artifact blob missing"))
		return
	}
	defer reader.Close()

	maxBytes := int64(secutils.GetMaxFileSizeMB()) << 20
	var buf bytes.Buffer
	n, err := io.Copy(&buf, io.LimitReader(reader, maxBytes+1))
	if err != nil {
		logger.Warnf(ctx, "artifact knowledge copy failed: session=%s message=%s idx=%d err=%v",
			sessionID, messageID, index, err)
		c.Error(errors.NewInternalServerError("failed to read artifact"))
		return
	}
	if n > maxBytes {
		c.Error(errors.NewBadRequestError("artifact exceeds the maximum upload size"))
		return
	}
	if n == 0 {
		c.Error(errors.NewBadRequestError("artifact is empty"))
		return
	}

	fileHeader, cleanup, err := multipartFileHeader(artifact.FileName, buf.Bytes())
	if err != nil {
		c.Error(errors.NewInternalServerError("failed to prepare artifact upload"))
		return
	}
	defer cleanup()

	// grant.Context scopes the write to the KB's effective tenant (shared KBs
	// write into the owner's workspace) while keeping the authenticated caller.
	knowledge, err := h.knowledgeService.CreateKnowledgeFromFile(
		grant.Context(ctx), kbID, fileHeader, nil, nil, "", nil, types.ChannelWeb, nil,
	)
	if err != nil {
		if dup, ok := err.(*types.DuplicateKnowledgeError); ok {
			logger.Infof(ctx, "artifact already in knowledge base: session=%s message=%s idx=%d kb=%s knowledge=%s",
				sessionID, messageID, index, kbID, dup.Knowledge.ID)
			c.JSON(http.StatusOK, gin.H{
				"success": true,
				"data": gin.H{
					"knowledge": dup.Knowledge,
					"duplicate": true,
				},
			})
			return
		}
		if appErr, ok := errors.IsAppError(err); ok {
			c.Error(appErr)
			return
		}
		logger.Errorf(ctx, "artifact to knowledge failed: session=%s message=%s idx=%d kb=%s err=%v",
			sessionID, messageID, index, kbID, err)
		c.Error(errors.NewInternalServerError(err.Error()))
		return
	}

	logger.Infof(ctx, "artifact added to knowledge base: session=%s message=%s idx=%d kb=%s knowledge=%s",
		sessionID, messageID, index, kbID, knowledge.ID)
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"knowledge": knowledge,
			"duplicate": false,
		},
	})
}

// multipartFileHeader builds the *multipart.FileHeader shape the knowledge
// import service consumes from bytes already in memory. ReadForm keeps the
// payload in memory up to 32 MiB and spills larger files to a temp file; the
// returned cleanup removes any temp file.
func multipartFileHeader(filename string, data []byte) (*multipart.FileHeader, func(), error) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", filename)
	if err != nil {
		return nil, nil, err
	}
	if _, err := part.Write(data); err != nil {
		return nil, nil, err
	}
	if err := writer.Close(); err != nil {
		return nil, nil, err
	}

	const maxMemory = 32 << 20
	form, err := multipart.NewReader(bytes.NewReader(body.Bytes()), writer.Boundary()).
		ReadForm(maxMemory)
	if err != nil {
		return nil, nil, err
	}
	cleanup := func() { _ = form.RemoveAll() }
	files := form.File["file"]
	if len(files) == 0 {
		cleanup()
		return nil, nil, stderrors.New("multipart artifact missing")
	}
	return files[0], cleanup, nil
}

// writeArtifactKnowledgeAccessError maps access-layer errors onto HTTP errors.
func writeArtifactKnowledgeAccessError(c *gin.Context, err error) {
	switch {
	case stderrors.Is(err, access.ErrUnauthorized):
		c.Error(errors.NewUnauthorizedError("Unauthorized"))
	case stderrors.Is(err, access.ErrForbidden):
		c.Error(errors.NewForbiddenError("No permission to add knowledge to this knowledge base"))
	case stderrors.Is(err, access.ErrNotFound):
		c.Error(errors.NewNotFoundError("knowledge base not found"))
	default:
		c.Error(errors.NewInternalServerError(err.Error()))
	}
}
