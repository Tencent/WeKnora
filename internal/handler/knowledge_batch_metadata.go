package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
)

// BatchUpdateKnowledgeMetadataRequest is the body for
// PUT /knowledge/metadata. The metadata object replaces the current custom
// metadata of every selected document; sending {} clears it on all documents.
type BatchUpdateKnowledgeMetadataRequest struct {
	KBID           string          `json:"kb_id" binding:"required"`
	IDs            []string        `json:"ids" binding:"required"`
	CustomMetadata json.RawMessage `json:"custom_metadata" binding:"required"`
}

// UpdateKnowledgeMetadataBatch replaces custom metadata on multiple documents
// in one knowledge base. It deliberately reuses KnowledgeService.UpdateKnowledge
// for each item so the existing validation, persistence, and summary-refresh
// behavior remains identical to the single-document editor.
//
// The access and membership checks happen before any write. IDs are also
// verified to belong to the requested KB, preventing a body-only batch from
// crossing knowledge-base boundaries.
func (h *KnowledgeHandler) UpdateKnowledgeMetadataBatch(c *gin.Context) {
	ctx := c.Request.Context()
	var req BatchUpdateKnowledgeMetadataRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(errors.NewBadRequestError("invalid batch metadata request").WithDetails(err.Error()))
		return
	}

	ids := dedupeKnowledgeIDs(req.IDs)
	if len(ids) == 0 {
		c.Error(errors.NewBadRequestError("ids cannot be empty"))
		return
	}
	const maxBatch = 200
	if len(ids) > maxBatch {
		c.Error(errors.NewBadRequestError(fmt.Sprintf("too many ids (max %d per batch)", maxBatch)))
		return
	}

	metadata, err := decodeBatchCustomMetadata(req.CustomMetadata)
	if err != nil {
		c.Error(errors.NewBadRequestError(err.Error()))
		return
	}
	metadataBytes, err := json.Marshal(metadata)
	if err != nil {
		c.Error(errors.NewBadRequestError("custom_metadata must be a JSON object"))
		return
	}

	kbID, effectiveTenantID, err := h.requireKnowledgeWriteAccess(c, req.KBID)
	if err != nil {
		c.Error(err)
		return
	}
	ctx = context.WithValue(ctx, types.TenantIDContextKey, effectiveTenantID)
	if err := h.requireKnowledgeInKB(ctx, effectiveTenantID, kbID, ids); err != nil {
		c.Error(err)
		return
	}

	updated := 0
	for _, id := range ids {
		if err := h.kgService.UpdateKnowledge(ctx, &types.Knowledge{
			ID:             id,
			CustomMetadata: types.JSON(metadataBytes),
		}); err != nil {
			logger.Errorf(ctx, "failed to update batch metadata for knowledge %s: %v", id, err)
			c.Error(errors.NewInternalServerError("failed to update batch metadata").WithDetails(gin.H{
				"updated_count": updated,
			}))
			return
		}
		updated++
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Batch metadata updated successfully",
		"data": gin.H{
			"updated_count": updated,
		},
	})
}

func decodeBatchCustomMetadata(raw json.RawMessage) (map[string]interface{}, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("custom_metadata is required")
	}
	var metadata map[string]interface{}
	if err := json.Unmarshal(raw, &metadata); err != nil || metadata == nil {
		return nil, fmt.Errorf("custom_metadata must be a JSON object")
	}
	if len(metadata) > 20 {
		return nil, fmt.Errorf("custom_metadata supports at most 20 fields")
	}
	for key, value := range metadata {
		if strings.TrimSpace(key) == "" || len(key) > 64 || len(fmt.Sprint(value)) > 1000 {
			return nil, fmt.Errorf("invalid custom_metadata field %q", key)
		}
		switch value.(type) {
		case string, float64, bool, nil:
		default:
			return nil, fmt.Errorf("custom_metadata field %q must be a string, number, boolean, or null", key)
		}
	}
	return metadata, nil
}
