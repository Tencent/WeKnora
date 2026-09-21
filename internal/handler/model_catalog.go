package handler

import (
	"encoding/base64"
	"net/http"
	"strings"

	"github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/handler/dto"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/api"
	"github.com/Tencent/WeKnora/internal/models/catalog"
	"github.com/Tencent/WeKnora/internal/types"
	secutils "github.com/Tencent/WeKnora/internal/utils"
	"github.com/gin-gonic/gin"
)

// ModelProviderDTO is the vendor definition the model editor renders from.
// Everything the UI shows for a vendor (name, icon, URLs, extra fields,
// catalog models, thinking capabilities) comes from here; the frontend keeps
// no vendor table of its own.
type ModelProviderDTO struct {
	Value        string            `json:"value"`
	Label        string            `json:"label"`
	Labels       map[string]string `json:"labels,omitempty"`
	Description  string            `json:"description"`
	Descriptions map[string]string `json:"descriptions,omitempty"`
	Website      string            `json:"website,omitempty"`
	// Icon is a data: URI (image/svg+xml;base64) ready for <img src>.
	Icon         string               `json:"icon,omitempty"`
	API          api.API              `json:"api"`
	Auth         catalog.AuthStyle    `json:"auth"`
	RequiresAuth bool                 `json:"requiresAuth"`
	DefaultURLs  map[string]string    `json:"defaultUrls"`
	ModelTypes   []string             `json:"modelTypes"`
	ExtraFields  []catalog.ExtraField `json:"extraFields,omitempty"`
	// CredentialLabels rename the primary credential input for the model
	// types that do not take a plain API key (signed rerank APIs).
	CredentialLabels []catalog.CredentialLabel `json:"credentialLabels,omitempty"`
	Models           []ModelCatalogEntryDTO    `json:"models,omitempty"`
	Thinking         ProviderThinkingDTO       `json:"thinking"`
	Order            int                       `json:"order"`
}

// ProviderThinkingDTO summarizes how the vendor encodes thinking so the UI
// can explain the reasoning selector.
type ProviderThinkingDTO struct {
	Format string                `json:"format"`
	Levels []api.ReasoningEffort `json:"levels"`
}

// ModelCatalogEntryDTO is one catalog model for the picker.
type ModelCatalogEntryDTO struct {
	ID              string                `json:"id"`
	Name            string                `json:"name"`
	Type            string                `json:"type"`
	API             api.API               `json:"api,omitempty"`
	Reasoning       bool                  `json:"reasoning"`
	Input           []string              `json:"input,omitempty"`
	ContextWindow   int                   `json:"context_window,omitempty"`
	MaxOutputTokens int                   `json:"max_output_tokens,omitempty"`
	Dimension       int                   `json:"dimension,omitempty"`
	ThinkingLevels  []api.ReasoningEffort `json:"thinking_levels"`
	Cost            *catalog.ModelCost    `json:"cost,omitempty"`
	// Source is the vendor page these facts were read from, so the editor can
	// send an operator to the documentation for this exact model.
	Source string `json:"source,omitempty"`
}

// modelTypeToFrontend 将后端 ModelType 转换为前端兼容的字符串
// KnowledgeQA -> chat, Embedding -> embedding, Rerank -> rerank, VLLM -> vllm
func modelTypeToFrontend(mt types.ModelType) string {
	switch mt {
	case types.ModelTypeKnowledgeQA:
		return "chat"
	case types.ModelTypeEmbedding:
		return "embedding"
	case types.ModelTypeRerank:
		return "rerank"
	case types.ModelTypeVLLM:
		return "vllm"
	case types.ModelTypeASR:
		return "asr"
	default:
		return string(mt)
	}
}

func iconDataURI(svg []byte) string {
	if len(svg) == 0 {
		return ""
	}
	return "data:image/svg+xml;base64," + base64.StdEncoding.EncodeToString(svg)
}

func providerDTO(v *catalog.Vendor, modelType types.ModelType, includeModels bool) ModelProviderDTO {
	defaultURLs := make(map[string]string, len(v.DefaultBaseURLs))
	for mt, url := range v.DefaultBaseURLs {
		defaultURLs[modelTypeToFrontend(mt)] = url
	}
	modelTypes := make([]string, 0, len(v.ModelTypes))
	for _, mt := range v.ModelTypes {
		modelTypes = append(modelTypes, modelTypeToFrontend(mt))
	}
	dto := ModelProviderDTO{
		Value:        v.ID,
		Label:        v.Name,
		Labels:       v.Names,
		Description:  v.Description,
		Descriptions: v.Descriptions,
		Website:      v.Website,
		Icon:         iconDataURI(v.Icon),
		API:          v.API,
		Auth:         v.Auth,
		RequiresAuth: v.RequiresAuth,
		DefaultURLs:  defaultURLs,
		ModelTypes:   modelTypes,
		ExtraFields:  v.ExtraFields,
		// Passed through raw, like ExtraFields: the editor resolves the
		// locale and the model type, so a new vendor needs no UI change.
		CredentialLabels: v.CredentialLabels,
		Order:            v.Order,
	}
	// Vendor-level thinking summary: resolve an unknown model so only the
	// vendor defaults contribute.
	if resolved, err := catalog.Resolve(catalog.Ref{Provider: v.ID, Model: "__vendor_default__"}); err == nil {
		caps := resolved.Capabilities()
		dto.Thinking = ProviderThinkingDTO{Format: caps.ThinkingFormat, Levels: caps.ThinkingLevels}
	}
	if dto.Thinking.Levels == nil {
		dto.Thinking.Levels = []api.ReasoningEffort{}
	}
	if !includeModels {
		return dto
	}
	wanted := []types.ModelType{modelType}
	if modelType == "" {
		wanted = v.ModelTypes
	}
	seen := map[string]bool{}
	for _, mt := range wanted {
		for _, m := range v.ModelsByType(mt) {
			if seen[m.ID] {
				continue
			}
			seen[m.ID] = true
			entry := ModelCatalogEntryDTO{
				ID:              m.ID,
				Name:            m.DisplayName(),
				Type:            modelTypeToFrontend(m.Type),
				API:             m.API,
				Reasoning:       m.Reasoning,
				Input:           m.Input,
				ContextWindow:   m.ContextWindow,
				MaxOutputTokens: m.MaxOutputTokens,
				Dimension:       m.Dimension,
				Cost:            m.Cost,
				ThinkingLevels:  []api.ReasoningEffort{},
				Source:          m.Source,
			}
			// A vision chat model keeps Type KnowledgeQA (VLM eligibility is
			// derived from Input), so this covers reasoning VLMs too;
			// embedding / rerank / ASR entries have no thinking levels.
			if m.Type == "" || m.Type == "KnowledgeQA" {
				if resolved, err := catalog.Resolve(catalog.Ref{Provider: v.ID, Model: m.ID}); err == nil {
					entry.ThinkingLevels = resolved.Capabilities().ThinkingLevels
				}
			}
			dto.Models = append(dto.Models, entry)
		}
	}
	return dto
}

// ListModelProviders godoc
// @Summary      获取模型厂商列表
// @Description  根据模型类型获取支持的厂商定义（含图标、默认地址、额外字段、内置模型目录与思考能力）
// @Tags         模型管理
// @Accept       json
// @Produce      json
// @Param        model_type  query     string  false  "模型类型 (chat, embedding, rerank, vllm, asr)"
// @Success      200         {object}  map[string]interface{}  "厂商列表"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /models/providers [get]
func (h *ModelHandler) ListModelProviders(c *gin.Context) {
	ctx := c.Request.Context()
	modelType := c.Query("model_type")
	logger.Infof(ctx, "Listing model providers for type: %s", secutils.SanitizeForLog(modelType))

	var backendType types.ModelType
	if modelType != "" {
		parsed, ok := catalog.ParseModelType(modelType)
		if !ok {
			_ = c.Error(errors.NewBadRequestError("unknown model_type"))
			return
		}
		backendType = parsed
	}

	var vendors []*catalog.Vendor
	if backendType != "" {
		vendors = catalog.ListByType(backendType)
	} else {
		vendors = catalog.List()
	}
	// Default base URLs are the editor's prefill, and only a caller who may
	// configure integrations can use them. A deployment overlay may also
	// repoint a vendor at an internal gateway, which would otherwise reach
	// every viewer here while the same URL is stripped from the model rows
	// themselves (dto.NewModelResponse).
	includeURLs := dto.CanViewIntegrationSecrets(ctx)
	result := make([]ModelProviderDTO, 0, len(vendors))
	for _, v := range vendors {
		p := providerDTO(v, backendType, true)
		if !includeURLs {
			p.DefaultURLs = nil
		}
		result = append(result, p)
	}
	logger.Infof(ctx, "Retrieved %d providers", len(result))
	c.JSON(http.StatusOK, gin.H{"success": true, "data": result})
}

// ResolveModelCatalog godoc
// @Summary      解析模型的有效接入配置
// @Description  根据厂商、模型名、Base URL 与 extra_config 返回目录解析结果（协议、思考等级、上下文等），供模型编辑器实时展示
// @Tags         模型管理
// @Accept       json
// @Produce      json
// @Param        provider    query     string  true   "厂商标识"
// @Param        model       query     string  false  "模型名"
// @Param        base_url    query     string  false  "Base URL"
// @Param        model_type  query     string  false  "模型类型"
// @Success      200         {object}  map[string]interface{}  "解析结果"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /models/catalog/resolve [get]
func (h *ModelHandler) ResolveModelCatalog(c *gin.Context) {
	ctx := c.Request.Context()
	providerID := strings.TrimSpace(c.Query("provider"))
	modelName := strings.TrimSpace(c.Query("model"))
	baseURL := strings.TrimSpace(c.Query("base_url"))
	modelType := types.ModelTypeKnowledgeQA
	if raw := c.Query("model_type"); raw != "" {
		if parsed, ok := catalog.ParseModelType(raw); ok {
			modelType = parsed
		}
	}
	// The preview must resolve against the same inputs the runtime will see,
	// or it describes a different request than the one the row will make —
	// Azure is the sharp case: api_version alone decides between the v1 data
	// plane and the dated deployments path. Forward the vendor's own declared
	// fields rather than a hardcoded list, so a new vendor needs no change
	// here. Secret fields are never accepted: this is a GET, and a credential
	// in a query string lands in access logs and browser history.
	extra := map[string]string{}
	for _, key := range []string{catalog.ExtraAPI, catalog.ExtraThinkingControl, catalog.ExtraRemoteModelName} {
		if v := strings.TrimSpace(c.Query(key)); v != "" {
			extra[key] = v
		}
	}
	if vendor, ok := catalog.Get(providerID); ok {
		for _, field := range vendor.ExtraFields {
			if field.Secret || field.Type == "password" {
				continue
			}
			if v := strings.TrimSpace(c.Query(field.Key)); v != "" {
				extra[field.Key] = v
			}
		}
	}
	resolved, err := catalog.Resolve(catalog.Ref{
		Provider: providerID, Model: modelName, BaseURL: baseURL, ModelType: modelType, Extra: extra,
	})
	if err != nil {
		_ = c.Error(errors.NewBadRequestError(err.Error()))
		return
	}
	caps := resolved.Capabilities()
	data := gin.H{
		"provider":     resolved.Vendor.ID,
		"api":          resolved.API,
		"remote_model": resolved.RemoteModel,
		"cataloged":    resolved.Cataloged,
		"model":        resolved.Spec,
		"capabilities": caps,
	}
	// base_url and the resolved endpoint are configuration, and with no
	// base_url in the query they fall back to the vendor default — which a
	// deployment overlay may have repointed at an internal gateway. Same
	// gate as the vendor list and as the model rows themselves.
	if dto.CanViewIntegrationSecrets(ctx) {
		data["base_url"] = resolved.BaseURL
		// Only the vendors that compute their own URL report one. Azure is
		// why this is here: api_version alone decides between the v1 data
		// plane and the dated deployments path, and nothing else in this
		// response would show which one the row will call. The protocols
		// that build their URL inside the client (Anthropic, Gemini
		// normalise several base-URL shapes) report nothing rather than a
		// path this endpoint would have to guess.
		if resolved.Vendor.Endpoint != nil {
			endpointURL, query := resolved.Vendor.Endpoint(catalog.EndpointRequest{
				BaseURL:   resolved.BaseURL,
				Model:     resolved.RemoteModel,
				ModelType: modelType,
				API:       resolved.API,
				Extra:     extra,
			})
			if endpointURL != "" {
				data["url"] = api.Endpoint{URL: endpointURL, Query: query}.Resolve("")
			}
		}
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": data})
}
