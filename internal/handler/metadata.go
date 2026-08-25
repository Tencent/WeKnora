package handler

import (
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/Tencent/WeKnora/internal/application/repository"
	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/handler/dto"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

type MetadataHandler struct {
	service  interfaces.KnowledgeMetadataService
	autoFill interfaces.MetadataAutoFillService
}

func NewMetadataHandler(
	service interfaces.KnowledgeMetadataService,
	autoFill interfaces.MetadataAutoFillService,
) *MetadataHandler {
	return &MetadataHandler{service: service, autoFill: autoFill}
}

func (h *MetadataHandler) ListDefinitions(c *gin.Context) {
	schema, err := h.service.ReadSchema(c.Request.Context(), strings.TrimSpace(c.Param("id")))
	respondMetadata(c, schema, err)
}

func (h *MetadataHandler) CreateDefinition(c *gin.Context) {
	var request dto.ConfigureMetadataDefinitionRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		c.Error(apperrors.NewBadRequestError("invalid metadata definition request").WithDetails(err.Error()))
		return
	}
	definition, err := h.service.ConfigureDefinition(
		c.Request.Context(),
		request.Command(strings.TrimSpace(c.Param("id")), ""),
	)
	respondMetadata(c, definition, err)
}

func (h *MetadataHandler) UpdateDefinition(c *gin.Context) {
	var request dto.ConfigureMetadataDefinitionRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		c.Error(apperrors.NewBadRequestError("invalid metadata definition request").WithDetails(err.Error()))
		return
	}
	definition, err := h.service.ConfigureDefinition(
		c.Request.Context(),
		request.Command(
			strings.TrimSpace(c.Param("id")),
			strings.TrimSpace(c.Param("definition_id")),
		),
	)
	respondMetadata(c, definition, err)
}

func (h *MetadataHandler) ArchiveDefinition(c *gin.Context) {
	err := h.service.ArchiveDefinition(
		c.Request.Context(),
		strings.TrimSpace(c.Param("id")),
		strings.TrimSpace(c.Param("definition_id")),
	)
	if err != nil {
		c.Error(metadataHTTPError(err))
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

func (h *MetadataHandler) ConfigureAutoRule(c *gin.Context) {
	var request dto.ConfigureMetadataAutoRuleRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		c.Error(apperrors.NewBadRequestError("invalid metadata automatic rule request").WithDetails(err.Error()))
		return
	}
	rule, err := h.service.ConfigureAutoRule(c.Request.Context(), types.ConfigureMetadataAutoRule{
		KnowledgeBaseID: strings.TrimSpace(c.Param("id")),
		DefinitionID:    strings.TrimSpace(c.Param("definition_id")),
		Strategy:        request.Strategy,
		Config:          request.Config,
	})
	respondMetadata(c, rule, err)
}

func (h *MetadataHandler) DeleteAutoRule(c *gin.Context) {
	err := h.service.DeleteAutoRule(
		c.Request.Context(),
		strings.TrimSpace(c.Param("id")),
		strings.TrimSpace(c.Param("definition_id")),
	)
	if err != nil {
		c.Error(metadataHTTPError(err))
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

func (h *MetadataHandler) GetDocumentMetadata(c *gin.Context) {
	knowledgeID := metadataKnowledgeIDParam(c)
	metadata, err := h.service.ReadDocumentMetadata(c.Request.Context(), []string{knowledgeID})
	if err != nil {
		c.Error(metadataHTTPError(err))
		return
	}
	if len(metadata) == 0 {
		c.Error(apperrors.NewNotFoundError("knowledge not found"))
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": metadata[0]})
}

func (h *MetadataHandler) BatchGetDocumentMetadata(c *gin.Context) {
	var request dto.BatchReadDocumentMetadataRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		c.Error(apperrors.NewBadRequestError("invalid metadata batch request").WithDetails(err.Error()))
		return
	}
	metadata, err := h.service.ReadDocumentMetadata(c.Request.Context(), request.KnowledgeIDs)
	respondMetadata(c, metadata, err)
}

func (h *MetadataHandler) ChangeDocumentMetadata(c *gin.Context) {
	var request dto.ChangeDocumentMetadataRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		c.Error(apperrors.NewBadRequestError("invalid metadata value request").WithDetails(err.Error()))
		return
	}
	changes := make([]types.MetadataValueChange, 0, len(request.Changes))
	for _, item := range request.Changes {
		change, err := item.Change()
		if err != nil {
			c.Error(apperrors.NewBadRequestError("invalid metadata value").WithDetails(err.Error()))
			return
		}
		changes = append(changes, change)
	}
	userID, _ := types.UserIDFromContext(c.Request.Context())
	metadata, err := h.service.ChangeDocumentMetadata(c.Request.Context(), types.ChangeDocumentMetadata{
		KnowledgeID: metadataKnowledgeIDParam(c),
		UpdatedBy:   userID,
		Changes:     changes,
	})
	respondMetadata(c, metadata, err)
}

func (h *MetadataHandler) ConfirmDocumentMetadata(c *gin.Context) {
	var request dto.ConfirmDocumentMetadataRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		c.Error(apperrors.NewBadRequestError("invalid metadata confirmation request").WithDetails(err.Error()))
		return
	}
	metadata, err := h.service.ConfirmDocumentMetadata(c.Request.Context(), types.ConfirmDocumentMetadata{
		KnowledgeID:           metadataKnowledgeIDParam(c),
		MetadataDefinitionIDs: request.MetadataDefinitionIDs,
	})
	respondMetadata(c, metadata, err)
}

func (h *MetadataHandler) RerunDocumentAutoFill(c *gin.Context) {
	if h.autoFill == nil {
		c.Error(apperrors.NewInternalServerError("metadata auto-fill is unavailable"))
		return
	}
	tenantID, ok := types.TenantIDFromContext(c.Request.Context())
	if !ok {
		c.Error(apperrors.NewUnauthorizedError("tenant ID not found in context"))
		return
	}
	payload := types.MetadataAutoFillPayload{
		TenantID: tenantID, KnowledgeBaseID: metadataKnowledgeBaseIDParam(c),
		KnowledgeID: metadataKnowledgeIDParam(c), Trigger: "manual_rerun",
	}
	taskID, err := h.autoFill.Enqueue(c.Request.Context(), payload)
	if err != nil {
		c.Error(metadataHTTPError(err))
		return
	}
	c.JSON(http.StatusAccepted, gin.H{"success": true, "data": gin.H{"task_id": taskID}})
}

func metadataKnowledgeIDParam(c *gin.Context) string {
	if knowledgeID := strings.TrimSpace(c.Param("knowledge_id")); knowledgeID != "" {
		return knowledgeID
	}
	return strings.TrimSpace(c.Param("id"))
}

func metadataKnowledgeBaseIDParam(c *gin.Context) string {
	if strings.TrimSpace(c.Param("knowledge_id")) == "" {
		return ""
	}
	return strings.TrimSpace(c.Param("id"))
}

func (h *MetadataHandler) RerunKnowledgeBaseAutoFill(c *gin.Context) {
	if h.autoFill == nil {
		c.Error(apperrors.NewInternalServerError("metadata auto-fill is unavailable"))
		return
	}
	var request dto.RerunMetadataAutoFillRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		c.Error(apperrors.NewBadRequestError("invalid metadata auto-fill request").WithDetails(err.Error()))
		return
	}
	tenantID, ok := types.TenantIDFromContext(c.Request.Context())
	if !ok {
		c.Error(apperrors.NewUnauthorizedError("tenant ID not found in context"))
		return
	}
	knowledgeBaseID := strings.TrimSpace(c.Param("id"))
	taskIDs := make([]string, 0, len(request.KnowledgeIDs))
	for _, knowledgeID := range request.KnowledgeIDs {
		payload := types.MetadataAutoFillPayload{
			TenantID: tenantID, KnowledgeBaseID: knowledgeBaseID,
			KnowledgeID: strings.TrimSpace(knowledgeID), Trigger: "batch_rerun",
		}
		taskID, err := h.autoFill.Enqueue(c.Request.Context(), payload)
		if err != nil {
			c.Error(metadataHTTPError(err))
			return
		}
		if taskID != "" {
			taskIDs = append(taskIDs, taskID)
		}
	}
	c.JSON(http.StatusAccepted, gin.H{"success": true, "data": gin.H{
		"enqueued_count": len(taskIDs),
		"task_ids":       taskIDs,
	}})
}

func respondMetadata(c *gin.Context, data any, err error) {
	if err != nil {
		c.Error(metadataHTTPError(err))
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": data})
}

func metadataHTTPError(err error) error {
	switch {
	case errors.Is(err, repository.ErrMetadataDefinitionNotFound),
		errors.Is(err, repository.ErrMetadataAutoRuleNotFound),
		errors.Is(err, repository.ErrMetadataValueNotFound),
		errors.Is(err, repository.ErrKnowledgeNotFound):
		return apperrors.NewNotFoundError(err.Error())
	case errors.Is(err, repository.ErrMetadataOptionNotInDefinition):
		return apperrors.NewBadRequestError(err.Error())
	case errors.Is(err, repository.ErrMetadataVersionConflict):
		return apperrors.NewConflictError(err.Error())
	default:
		return err
	}
}
