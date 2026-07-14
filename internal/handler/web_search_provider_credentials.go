package handler

import (
	"net/http"

	"github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/handler/dto"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	secutils "github.com/Tencent/WeKnora/internal/utils"
	"github.com/gin-gonic/gin"
)

// WebSearchProviderCredentialsHandler handles credentials for web search
// providers via the dedicated /credentials subresource. Currently the only
// recognized field is "api_key" — every provider that needs credentials uses
// just one key (Bing / Google / Tavily / Ollama / Baidu), and DuckDuckGo /
// SearXNG don't need credentials at all.
type WebSearchProviderCredentialsHandler struct {
	repo interfaces.WebSearchProviderRepository
	svc  interfaces.WebSearchProviderService
}

func NewWebSearchProviderCredentialsHandler(
	repo interfaces.WebSearchProviderRepository,
	svc interfaces.WebSearchProviderService,
) *WebSearchProviderCredentialsHandler {
	return &WebSearchProviderCredentialsHandler{repo: repo, svc: svc}
}

func (h *WebSearchProviderCredentialsHandler) tenantID(c *gin.Context) uint64 {
	return c.GetUint64(types.TenantIDContextKey.String())
}

type webSearchCredentialsPutRequest struct {
	APIKey *string `json:"api_key,omitempty"`
}

func (h *WebSearchProviderCredentialsHandler) Put(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID := h.tenantID(c)
	if tenantID == 0 {
		c.Error(errors.NewBadRequestError("Workspace ID cannot be empty"))
		return
	}
	id := c.Param("id")
	var req webSearchCredentialsPutRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(errors.NewBadRequestError(err.Error()))
		return
	}
	if req.APIKey == nil {
		provider, err := h.repo.GetByID(ctx, tenantID, id)
		if err != nil || provider == nil {
			c.Error(errors.NewNotFoundError("web search provider not found"))
			return
		}
		c.JSON(http.StatusOK, gin.H{"success": true, "data": dto.CredentialsResponse{
			Fields: map[string]dto.CredentialFieldMetadata{
				"api_key": {Configured: provider.Parameters.APIKey != ""},
			},
		}})
		return
	}
	updated, err := h.svc.UpdateProviderCredentials(ctx, tenantID, id, req.APIKey)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"provider_id": secutils.SanitizeForLog(id),
		})
		c.Error(errors.NewInternalServerError("failed to update credentials: " + err.Error()))
		return
	}
	resp := dto.CredentialsResponse{
		Fields: map[string]dto.CredentialFieldMetadata{
			"api_key": {Configured: updated.Parameters.APIKey != ""},
		},
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": resp})
}

func (h *WebSearchProviderCredentialsHandler) DeleteField(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID := h.tenantID(c)
	if tenantID == 0 {
		c.Error(errors.NewBadRequestError("Workspace ID cannot be empty"))
		return
	}
	id := c.Param("id")
	field := c.Param("field")
	if field != "api_key" {
		c.Error(errors.NewBadRequestError("unknown credential field: " + secutils.SanitizeForLog(field)))
		return
	}
	if err := h.svc.ClearProviderCredential(ctx, tenantID, id, field); err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"provider_id": secutils.SanitizeForLog(id),
			"field":       field,
		})
		c.Error(errors.NewInternalServerError("failed to clear credential: " + err.Error()))
		return
	}
	c.Status(http.StatusNoContent)
}
