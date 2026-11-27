package handler

import (
	"net/http"

	"github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
)

// UpdateKnowledgeSourceRequest requires the revision returned by GET /content.
type UpdateKnowledgeSourceRequest struct {
	Content  string `json:"content" binding:"required"`
	Revision string `json:"revision" binding:"required"`
}

// GetKnowledgeSourceContent godoc
// @Summary Read an uploaded UTF-8 text source for editing
// @Tags Knowledge
// @Produce json
// @Param id path string true "Knowledge ID"
// @Success 200 {object} handler.DataResponse{data=types.KnowledgeSourceContent}
// @Failure 400,403,409 {object} errors.AppError
// @Security Bearer
// @Security ApiKeyAuth
// @Router /knowledge/{id}/content [get]
func (h *KnowledgeHandler) GetKnowledgeSourceContent(c *gin.Context) {
	_, ctx, err := h.resolveKnowledgeAndValidateKBAccess(c, c.Param("id"), types.OrgRoleEditor)
	if err != nil {
		_ = c.Error(err)
		return
	}
	source, err := h.kgService.GetKnowledgeSourceContent(ctx, c.Param("id"))
	if err != nil {
		knowledgeSourceError(c, err)
		return
	}
	c.Header("Cache-Control", "private, no-store")
	c.JSON(http.StatusOK, gin.H{"success": true, "data": source})
}

// UpdateKnowledgeSourceContent godoc
// @Summary Save an uploaded UTF-8 text source and rebuild its derived content
// @Tags Knowledge
// @Accept json
// @Produce json
// @Param id path string true "Knowledge ID"
// @Param request body handler.UpdateKnowledgeSourceRequest true "Content and source revision"
// @Success 200 {object} handler.DataResponse{data=types.Knowledge}
// @Failure 400,403,409,413 {object} errors.AppError
// @Security Bearer
// @Security ApiKeyAuth
// @Router /knowledge/{id}/content [put]
func (h *KnowledgeHandler) UpdateKnowledgeSourceContent(c *gin.Context) {
	_, ctx, err := h.resolveKnowledgeAndValidateKBAccess(c, c.Param("id"), types.OrgRoleEditor)
	if err != nil {
		_ = c.Error(err)
		return
	}
	// JSON escapes can expand a UTF-8 source up to sixfold. The service also
	// bounds decoded bytes, so chunked requests cannot bypass the editor limit.
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 6*types.MaxKnowledgeSourceBytes+1024)
	var request UpdateKnowledgeSourceRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		if isRequestBodyTooLarge(err) {
			limitErr := errors.NewBadRequestError("Source request exceeds the editor limit")
			limitErr.HTTPCode = http.StatusRequestEntityTooLarge
			_ = c.Error(limitErr)
			return
		}
		_ = c.Error(errors.NewBadRequestError("Invalid source content or missing revision"))
		return
	}
	knowledge, err := h.kgService.UpdateKnowledgeSourceContent(ctx, c.Param("id"), request.Content, request.Revision)
	if err != nil {
		knowledgeSourceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": knowledge})
}

func knowledgeSourceError(c *gin.Context, err error) {
	if appErr, ok := errors.IsAppError(err); ok {
		_ = c.Error(appErr)
		return
	}
	logger.Errorf(c.Request.Context(), "Unable to access source for knowledge %s: %v", c.Param("id"), err)
	_ = c.Error(errors.NewInternalServerError("Unable to access document source"))
}
