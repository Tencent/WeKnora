package handler

import (
	"fmt"
	"net/http"

	"github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
)

// GetTenantDefaultSettings returns workspace defaults as resource IDs only,
// never connection details.
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

// updateTenantDefaultSettingsInternal replaces the default model slots.
// An omitted vector-store slot is preserved for older clients, while an
// explicitly empty slot restores deployment defaults. Unchanged stale IDs
// are allowed so admins can repair slots one at a time.
func (h *TenantHandler) updateTenantDefaultSettingsInternal(c *gin.Context) {
	ctx := c.Request.Context()

	var body struct {
		types.DefaultSettings
		VectorStoreID *string `json:"vector_store_id"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		logger.Error(ctx, "Failed to parse request parameters", err)
		_ = c.Error(errors.NewValidationError("Invalid request data").WithDetails(err.Error()))
		return
	}
	cfg := body.DefaultSettings
	if body.VectorStoreID != nil {
		cfg.VectorStoreID = *body.VectorStoreID
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

	if body.VectorStoreID == nil {
		cfg.VectorStoreID = previous.VectorStoreID
	} else if id := cfg.VectorStoreID; id != "" && id != previous.VectorStoreID {
		if types.IsEnvStoreID(id) {
			_ = c.Error(errors.NewBadRequestError("vector_store_id: vector store not found"))
			return
		}
		if h.vectorStores == nil {
			_ = c.Error(errors.NewServiceUnavailableError("Vector store repository is unavailable"))
			return
		}
		store, err := h.vectorStores.GetByID(ctx, tenant.ID, id)
		if err != nil {
			logger.ErrorWithFields(ctx, err, nil)
			_ = c.Error(errors.NewInternalServerError("Failed to read vector store"))
			return
		}
		if store == nil || store.TenantID != tenant.ID || store.DeletedAt.Valid {
			_ = c.Error(errors.NewBadRequestError("vector_store_id: vector store not found"))
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
