package handler

import (
	"encoding/json"
	stderrors "errors"
	"io"
	"net/http"

	"github.com/Tencent/WeKnora/internal/application/service"
	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/handler/dto"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	secutils "github.com/Tencent/WeKnora/internal/utils"
	"github.com/gin-gonic/gin"
)

const knowledgeFolderMoveMaxBodyBytes int64 = 64 << 10
const knowledgeFolderMoveMaxKnowledgeIDs = 200

// MoveKnowledge moves knowledge placements within one knowledge base.
func (h *KnowledgeFolderHandler) MoveKnowledge(c *gin.Context) {
	limitedBody := http.MaxBytesReader(
		c.Writer,
		c.Request.Body,
		knowledgeFolderMoveMaxBodyBytes,
	)
	defer limitedBody.Close()

	var req dto.KnowledgeFolderMoveRequest
	decoder := json.NewDecoder(limitedBody)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		c.Error(apperrors.NewBadRequestError("请求参数不合法"))
		return
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); !stderrors.Is(err, io.EOF) {
		c.Error(apperrors.NewBadRequestError("请求参数不合法"))
		return
	}
	if req.TargetFolderID == nil ||
		len(req.KnowledgeIDs) < 1 ||
		len(req.KnowledgeIDs) > knowledgeFolderMoveMaxKnowledgeIDs {
		c.Error(apperrors.NewBadRequestError("请求参数不合法"))
		return
	}
	if h == nil || h.moveService == nil {
		writeKnowledgeFolderMoveError(c, service.ErrKnowledgeFolderInternal)
		return
	}

	ctx := c.Request.Context()
	tenantID, _ := types.TenantIDFromContext(ctx)
	result, err := h.moveService.MoveKnowledge(ctx, &types.KnowledgeFolderMoveInput{
		TenantID:        tenantID,
		KnowledgeBaseID: c.Param("id"),
		KnowledgeIDs:    req.KnowledgeIDs,
		TargetFolderID:  *req.TargetFolderID,
	})
	if err != nil {
		writeKnowledgeFolderMoveError(c, err)
		return
	}
	if result == nil || result.ChangedCount < 0 || result.UnchangedCount < 0 {
		writeKnowledgeFolderMoveError(c, service.ErrKnowledgeFolderInternal)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    dto.NewKnowledgeFolderMoveResponse(result),
	})
}

func writeKnowledgeFolderMoveError(c *gin.Context, err error) {
	var appErr error
	switch {
	case stderrors.Is(err, service.ErrKnowledgeFolderInvalidArgument):
		appErr = apperrors.NewBadRequestError("请求参数不合法")
	case stderrors.Is(err, service.ErrKnowledgeFolderNotFound),
		stderrors.Is(err, service.ErrKnowledgeFolderMoveKnowledgeNotFound):
		appErr = apperrors.NewNotFoundError("资源不存在")
	default:
		logger.ErrorWithFields(c.Request.Context(), err, map[string]interface{}{
			"knowledge_base_id": secutils.SanitizeForLog(c.Param("id")),
		})
		appErr = apperrors.NewInternalServerError("目录操作失败")
	}
	c.Error(appErr)
}
