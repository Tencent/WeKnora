package handler

import (
	"errors"
	"slices"

	"github.com/Tencent/WeKnora/internal/application/access"
	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/config"
	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
)

func readerDownloadListed(c *gin.Context, cfg *config.Config, kbID string) bool {
	ctx := c.Request.Context()
	principal, ok := types.PrincipalFromContext(ctx)
	return ok && principal.Type == types.PrincipalWebUser &&
		types.PrincipalAuthSourceFromContext(ctx) == types.AuthSourceWeb &&
		kbID != "" && cfg != nil && cfg.KnowledgeBase != nil &&
		slices.Contains(cfg.KnowledgeBase.ReaderDownloadKBIDs, kbID)
}

func readerDownloadGrant(
	c *gin.Context, cfg *config.Config, kb *types.KnowledgeBase, shares access.KBShareLookup,
) (*access.KBAccess, bool) {
	if kb == nil || !readerDownloadListed(c, cfg, kb.ID) {
		return nil, false
	}
	// Sharing an assistant grants retrieval of evidence, not a copy of its source documents.
	grant, err := access.ResolveKB(
		c.Request.Context(), middleware.KBAccessRequest(c), kb, types.OrgRoleViewer, shares, nil,
	)
	return grant, err == nil
}

// DownloadKnowledgeFileAsReader godoc
// @Summary Download an individual original file from a reader-enabled knowledge base
// @Description Requires a web session and direct read access to a knowledge base enabled by the operator.
// @Tags 知识管理
// @Produce application/octet-stream
// @Param id path string true "Knowledge base ID"
// @Param knowledge_id path string true "Knowledge ID"
// @Success 200 {file} file "Original file"
// @Failure 403 {object} apperrors.AppError "Reader download unavailable"
// @Failure 404 {object} apperrors.AppError "Document not found in this knowledge base"
// @Security Bearer
// @Router /knowledge-bases/{id}/knowledge/{knowledge_id}/reader-download [get]
func (h *KnowledgeHandler) DownloadKnowledgeFileAsReader(c *gin.Context) {
	kbID, id := c.Param("id"), c.Param("knowledge_id")
	if !readerDownloadListed(c, h.cfg, kbID) {
		_ = c.Error(apperrors.NewForbiddenError("Reader downloads are unavailable"))
		return
	}
	kb, err := h.kbService.GetKnowledgeBaseByID(c.Request.Context(), kbID)
	if err != nil && !errors.Is(err, repository.ErrKnowledgeBaseNotFound) {
		_ = c.Error(apperrors.NewServiceUnavailableError("Cannot verify knowledge base access"))
		return
	}
	if err != nil || kb == nil || kb.ID != kbID {
		_ = c.Error(apperrors.NewNotFoundError("Knowledge base not found"))
		return
	}
	grant, ok := readerDownloadGrant(c, h.cfg, kb, h.kbShareService)
	if !ok {
		_ = c.Error(apperrors.NewForbiddenError("Reader downloads are unavailable"))
		return
	}
	ctx := grant.Context(c.Request.Context())
	file, filename, err := h.kgService.GetReaderDownloadFile(ctx, kbID, id)
	if errors.Is(err, repository.ErrKnowledgeNotFound) {
		_ = c.Error(apperrors.NewNotFoundError("Knowledge not found in this knowledge base"))
		return
	}
	writeKnowledgeFile(c, file, filename, err)
}
