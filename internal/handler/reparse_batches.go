package handler

import (
	stderrors "errors"
	"net/http"
	"strconv"

	"github.com/Tencent/WeKnora/internal/application/service"
	"github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func reparseBatchPage(c *gin.Context, defaultSize, maxSize int) (int, int, bool) {
	page, size := 1, defaultSize
	var err error
	if raw := c.Query("page"); raw != "" {
		page, err = strconv.Atoi(raw)
		if err != nil || page < 1 {
			return 0, 0, false
		}
	}
	if raw := c.Query("page_size"); raw != "" {
		size, err = strconv.Atoi(raw)
		if err != nil || size < 1 || size > maxSize {
			return 0, 0, false
		}
	}
	if page-1 > int(^uint(0)>>1)/size {
		return 0, 0, false
	}
	return page, size, true
}

func (h *KnowledgeHandler) reparseBatchService(c *gin.Context) (service.ReparseBatchService, uint64, bool) {
	tenant, ok := types.TenantIDFromContext(c.Request.Context())
	if !ok || tenant == 0 {
		_ = c.Error(errors.NewUnauthorizedError("Unauthorized"))
		return nil, 0, false
	}
	svc, ok := h.kgService.(service.ReparseBatchService)
	if !ok {
		_ = c.Error(errors.NewInternalServerError("reparse batch persistence is unavailable"))
		return nil, 0, false
	}
	return svc, tenant, true
}

// ListReparseBatches lists batches only for the current tenant and applies the
// existing KB access check to an optional KB selector.
func (h *KnowledgeHandler) ListReparseBatches(c *gin.Context) {
	svc, tenant, ok := h.reparseBatchService(c)
	if !ok {
		return
	}
	page, size, valid := reparseBatchPage(c, 20, 100)
	if !valid {
		_ = c.Error(errors.NewBadRequestError("invalid pagination"))
		return
	}
	kbID := c.Query("kb_id")
	if kbID != "" {
		_, _, _, _, err := h.validateKnowledgeBaseAccessWithKBID(c, kbID)
		if err != nil {
			_ = c.Error(err)
			return
		}
	}
	if kbID != "" {
		items, total, err := svc.ListReparseBatchViews(c.Request.Context(), tenant, kbID, (page-1)*size, size)
		if err != nil {
			_ = c.Error(errors.NewInternalServerError("failed to list reparse batches"))
			return
		}
		c.JSON(
			http.StatusOK,
			gin.H{"success": true, "data": gin.H{"items": items, "total": total, "page": page, "page_size": size}},
		)
		return
	}
	// A tenant may contain private or space-restricted KBs. Enumerate only
	// bounded batch pages and apply the same KB resolver used by every other
	// read endpoint before computing both visible items and total.
	visible := make([]*types.ReparseBatch, 0, size)
	allowedTotal, offset := int64(0), 0
	accessCache := make(map[string]bool)
	for {
		candidates, total, err := svc.ListReparseBatches(c.Request.Context(), tenant, "", offset, 100)
		if err != nil {
			_ = c.Error(errors.NewInternalServerError("failed to list reparse batches"))
			return
		}
		for _, candidate := range candidates {
			allowed, checked := accessCache[candidate.KnowledgeBaseID]
			if !checked {
				_, _, effective, _, accessErr := h.validateKnowledgeBaseAccessWithKBID(c, candidate.KnowledgeBaseID)
				if accessErr != nil {
					var appErr *errors.AppError
					if !stderrors.As(accessErr, &appErr) || appErr.HTTPCode >= 500 {
						_ = c.Error(errors.NewInternalServerError("failed to resolve batch knowledge base access"))
						return
					}
				}
				allowed = accessErr == nil && effective == tenant
				accessCache[candidate.KnowledgeBaseID] = allowed
			}
			if !allowed {
				continue
			}
			if allowedTotal >= int64((page-1)*size) && len(visible) < size {
				visible = append(visible, candidate)
			}
			allowedTotal++
		}
		offset += len(candidates)
		if int64(offset) >= total || len(candidates) == 0 {
			break
		}
	}
	visibleIDs := make([]string, 0, len(visible))
	for _, batch := range visible {
		visibleIDs = append(visibleIDs, batch.ID)
	}
	views, viewErr := svc.ListReparseBatchViewsByIDs(c.Request.Context(), tenant, visibleIDs)
	if viewErr != nil {
		_ = c.Error(errors.NewInternalServerError("failed to read reparse batches"))
		return
	}
	c.JSON(
		http.StatusOK,
		gin.H{"success": true, "data": gin.H{"items": views, "total": allowedTotal, "page": page, "page_size": size}},
	)
}

// GetReparseBatch returns authorized current document-state totals for one batch.
func (h *KnowledgeHandler) GetReparseBatch(c *gin.Context) {
	svc, tenant, ok := h.reparseBatchService(c)
	if !ok {
		return
	}
	batch, err := svc.GetReparseBatch(c.Request.Context(), tenant, c.Param("batch_id"))
	if err != nil {
		_ = c.Error(reparseBatchLookupError(err))
		return
	}
	_, _, effective, _, accessErr := h.validateKnowledgeBaseAccessWithKBID(c, batch.KnowledgeBaseID)
	if accessErr != nil || effective != tenant {
		if accessErr != nil {
			_ = c.Error(accessErr)
		} else {
			_ = c.Error(errors.NewForbiddenError("Permission denied"))
		}
		return
	}
	view, err := svc.GetReparseBatchView(c.Request.Context(), tenant, batch.ID)
	if err != nil {
		_ = c.Error(errors.NewInternalServerError("failed to read reparse batch"))
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": view})
}

// ListReparseBatchMembers returns a bounded, authorized page without task payloads.
func (h *KnowledgeHandler) ListReparseBatchMembers(c *gin.Context) {
	svc, tenant, ok := h.reparseBatchService(c)
	if !ok {
		return
	}
	page, size, valid := reparseBatchPage(c, 50, 200)
	if !valid {
		_ = c.Error(errors.NewBadRequestError("invalid pagination"))
		return
	}
	batch, err := svc.GetReparseBatch(c.Request.Context(), tenant, c.Param("batch_id"))
	if err != nil {
		_ = c.Error(reparseBatchLookupError(err))
		return
	}
	_, _, effective, _, accessErr := h.validateKnowledgeBaseAccessWithKBID(c, batch.KnowledgeBaseID)
	if accessErr != nil || effective != tenant {
		if accessErr != nil {
			_ = c.Error(accessErr)
		} else {
			_ = c.Error(errors.NewForbiddenError("Permission denied"))
		}
		return
	}
	state := c.Query("state")
	if state != "" && !validReparseBatchState(state) {
		_ = c.Error(errors.NewBadRequestError("invalid reparse batch state"))
		return
	}
	items, total, err := svc.ListReparseBatchMemberViews(
		c.Request.Context(),
		tenant,
		batch.ID,
		state,
		(page-1)*size,
		size,
	)
	if err != nil {
		_ = c.Error(errors.NewInternalServerError("failed to list reparse batch members"))
		return
	}
	c.JSON(
		http.StatusOK,
		gin.H{"success": true, "data": gin.H{"items": items, "total": total, "page": page, "page_size": size}},
	)
}

func reparseBatchLookupError(err error) error {
	if stderrors.Is(err, gorm.ErrRecordNotFound) {
		return errors.NewNotFoundError("reparse batch not found")
	}
	return errors.NewInternalServerError("failed to read reparse batch")
}

func validReparseBatchState(state string) bool {
	switch state {
	case "waiting_start",
		"queued",
		"processing",
		"finalizing",
		"completed",
		"failed",
		"cancelled",
		"missing",
		"submission_failed",
		"unknown":
		return true
	}
	return false
}
