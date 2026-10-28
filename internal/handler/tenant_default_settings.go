package handler

import (
	"fmt"
	"net/http"

	"github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
)

// GetTenantDefaultSettings returns the workspace's default model choices.
// Members read it to see which model a feature falls back to; it holds model
// IDs only, never connection details.
func (h *TenantHandler) GetTenantDefaultSettings(c *gin.Context) {
	ctx := c.Request.Context()
	tenant, _ := types.TenantInfoFromContext(ctx)
	if tenant == nil {
		logger.Error(ctx, "Workspace is empty")
		_ = c.Error(errors.NewBadRequestError("Workspace is empty"))
		return
	}
	data := types.DefaultSettings{}
	if tenant.DefaultSettings != nil {
		data = *tenant.DefaultSettings
	}
	data.Normalize()
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    data,
	})
}

// updateTenantDefaultSettingsInternal replaces the workspace's default model
// choices. Newly selected IDs must be usable models of the slot's type.
// Unchanged stale IDs are allowed so admins can repair slots one at a time;
// an empty ID returns the slot to the deployment default.
func (h *TenantHandler) updateTenantDefaultSettingsInternal(c *gin.Context) {
	ctx := c.Request.Context()

	var cfg types.DefaultSettings
	if err := c.ShouldBindJSON(&cfg); err != nil {
		logger.Error(ctx, "Failed to parse request parameters", err)
		_ = c.Error(errors.NewValidationError("Invalid request data").WithDetails(err.Error()))
		return
	}
	cfg.Normalize()

	tenant, _ := types.TenantInfoFromContext(ctx)
	if tenant == nil {
		logger.Error(ctx, "Workspace is empty")
		_ = c.Error(errors.NewBadRequestError("Workspace is empty"))
		return
	}
	if h.modelService == nil {
		_ = c.Error(errors.NewServiceUnavailableError("Model service is unavailable"))
		return
	}
	models, err := h.modelService.ListModels(ctx)
	if err != nil {
		logger.ErrorWithFields(ctx, err, nil)
		_ = c.Error(errors.NewInternalServerError("Failed to read models"))
		return
	}
	modelsByID := make(map[string]*types.Model, len(models))
	for _, m := range models {
		if m != nil {
			modelsByID[m.ID] = m
		}
	}
	previous := types.DefaultSettings{}
	if tenant.DefaultSettings != nil {
		previous = *tenant.DefaultSettings
		previous.Normalize()
	}
	slots := []struct {
		field    string
		id       string
		previous string
		want     types.ModelType
	}{
		{"chat_model_id", cfg.ChatModelID, previous.ChatModelID, types.ModelTypeKnowledgeQA},
		{"utility_model_id", cfg.UtilityModelID, previous.UtilityModelID, types.ModelTypeKnowledgeQA},
		{"embedding_model_id", cfg.EmbeddingModelID, previous.EmbeddingModelID, types.ModelTypeEmbedding},
		{"rerank_model_id", cfg.RerankModelID, previous.RerankModelID, types.ModelTypeRerank},
		{"vlm_model_id", cfg.VLMModelID, previous.VLMModelID, types.ModelTypeVLLM},
		{"asr_model_id", cfg.ASRModelID, previous.ASRModelID, types.ModelTypeASR},
	}
	for _, slot := range slots {
		if slot.id == "" || slot.id == slot.previous {
			continue
		}
		model, ok := modelsByID[slot.id]
		if !ok {
			_ = c.Error(errors.NewBadRequestError(fmt.Sprintf("%s: model not found", slot.field)))
			return
		}
		if !model.SupportsType(slot.want) {
			_ = c.Error(errors.NewBadRequestError(fmt.Sprintf("%s must be a %s model", slot.field, slot.want)))
			return
		}
		if model.Status != types.ModelStatusActive {
			_ = c.Error(errors.NewBadRequestError(fmt.Sprintf("%s: model is not active", slot.field)))
			return
		}
	}

	updated := *tenant
	updated.DefaultSettings = &cfg
	updatedTenant, err := h.service.UpdateTenant(ctx, &updated)
	if err != nil {
		if appErr, ok := errors.IsAppError(err); ok {
			_ = c.Error(appErr)
		} else {
			logger.ErrorWithFields(ctx, err, nil)
			_ = c.Error(errors.NewInternalServerError("Failed to update default settings").WithDetails(err.Error()))
		}
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    updatedTenant.DefaultSettings,
		"message": "Default settings updated successfully",
	})
}
