package session

import (
	stderrors "errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	secutils "github.com/Tencent/WeKnora/internal/utils"
	"github.com/gin-gonic/gin"
)

// DeleteMessageArtifact godoc
// @Summary      删除会话中的产物文件
// @Description  删除某条 assistant 消息生成的单个文件；all_versions=true 时连同本会话中该文件的所有历史版本一并删除
// @Tags         会话
// @Produce      json
// @Param        session_id    path   string  true   "会话ID"
// @Param        message_id    path   string  true   "消息ID"
// @Param        index         path   int     true   "产物在消息中的位置"
// @Param        all_versions  query  bool    false  "是否删除该文件的所有版本"
// @Success      200  {object}  map[string]interface{}
// @Failure      404  {object}  errors.AppError
// @Security     Bearer
// @Router       /sessions/{session_id}/messages/{message_id}/artifacts/{index} [delete]
//
// Addressed by the same (message, index) coordinates as the download endpoint,
// so a client deletes exactly the row it was showing. The in-chat panel lists
// every version separately and omits all_versions; the artifact library folds
// versions into one entry and sets it.
func (h *Handler) DeleteMessageArtifact(c *gin.Context) {
	sessionID := secutils.SanitizeForLog(paramSessionID(c))
	messageID := secutils.SanitizeForLog(c.Param("message_id"))
	index, err := strconv.Atoi(c.Param("index"))
	if err != nil || index < 0 {
		_ = c.Error(errors.NewBadRequestError("invalid artifact index"))
		return
	}
	h.deleteArtifact(c, &types.ArtifactDeleteRequest{
		SessionID:   sessionID,
		MessageID:   messageID,
		Index:       index,
		AllVersions: artifactAllVersions(c, false),
	})
}

// DeleteLibraryArtifact godoc
// @Summary      删除产物库中的文件
// @Description  删除产物库中的一个文件。产物库一行代表一个文件的最新版本，默认连同该文件在本会话中的所有历史版本一并删除
// @Tags         会话
// @Produce      json
// @Param        session_id    query  string  true   "会话ID"
// @Param        message_id    query  string  true   "消息ID"
// @Param        index         query  int     true   "产物在消息中的位置"
// @Param        all_versions  query  bool    false  "是否删除所有版本，默认 true"
// @Success      200  {object}  map[string]interface{}
// @Failure      404  {object}  errors.AppError
// @Security     Bearer
// @Router       /artifacts [delete]
//
// Versions default to being deleted together here: the library row IS the file,
// with a version count rather than a row per regeneration, so deleting one
// version would leave the entry in place showing the previous one.
func (h *Handler) DeleteLibraryArtifact(c *gin.Context) {
	index, err := strconv.Atoi(c.Query("index"))
	if err != nil || index < 0 {
		_ = c.Error(errors.NewBadRequestError("invalid artifact index"))
		return
	}
	h.deleteArtifact(c, &types.ArtifactDeleteRequest{
		SessionID:   secutils.SanitizeForLog(c.Query("session_id")),
		MessageID:   secutils.SanitizeForLog(c.Query("message_id")),
		Index:       index,
		AllVersions: artifactAllVersions(c, true),
	})
}

// artifactAllVersions reads the all_versions flag. The two routes differ only
// in what an absent flag means, so they share one parser: an explicit value is
// read the same way on both, and "1"/"TRUE"/"yes" cannot mean one thing here
// and another there.
func artifactAllVersions(c *gin.Context, whenAbsent bool) bool {
	raw := strings.TrimSpace(c.Query("all_versions"))
	if raw == "" {
		return whenAbsent
	}
	parsed, err := strconv.ParseBool(raw)
	if err != nil {
		return whenAbsent
	}
	return parsed
}

// deleteArtifact runs the tombstone-then-reclaim sequence both delete routes
// share. The order matters: the rows are marked first, and only the rows this
// call actually claimed have their bytes reclaimed, so two concurrent deletes
// cannot both try to remove the same object.
func (h *Handler) deleteArtifact(c *gin.Context, req *types.ArtifactDeleteRequest) {
	ctx := c.Request.Context()

	result, err := h.messageService.DeleteSessionArtifact(ctx, req)
	if err != nil {
		// A session the caller does not own reads as absent, matching the rest
		// of the session routes.
		if stderrors.Is(err, errors.ErrSessionNotFound) {
			_ = c.Error(errors.NewNotFoundError(err.Error()))
			return
		}
		var appErr *errors.AppError
		if stderrors.As(err, &appErr) {
			_ = c.Error(appErr)
			return
		}
		logger.Errorf(ctx, "delete artifact failed: session=%s message=%s idx=%d err=%v",
			req.SessionID, req.MessageID, req.Index, err)
		_ = c.Error(errors.NewInternalServerError(err.Error()))
		return
	}

	// The rows are gone from every listing at this point. Reclaiming the bytes
	// is best-effort cleanup, not part of the user-visible outcome, so its
	// failures are logged rather than turned into a failed delete.
	h.reclaimArtifactBlobs(ctx, types.MustTenantIDFromContext(ctx), result.Reclaim)

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"file_name": result.FileName,
			"deleted":   result.Deleted,
		},
	})
}
