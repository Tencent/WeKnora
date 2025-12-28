package handler

import (
	"net/http"

	"github.com/Tencent/WeKnora/internal/application/service"
	"github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/provider"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	secutils "github.com/Tencent/WeKnora/internal/utils"
	"github.com/gin-gonic/gin"
)

// ModelHandler handles HTTP requests for model-related operations
// It implements the necessary methods to create, retrieve, update, and delete models
type ModelHandler struct {
	service interfaces.ModelService
}

// NewModelHandler creates a new instance of ModelHandler
// It requires a model service implementation that handles business logic
// Parameters:
//   - service: An implementation of the ModelService interface
//
// Returns a pointer to the newly created ModelHandler
func NewModelHandler(service interfaces.ModelService) *ModelHandler {
	return &ModelHandler{service: service}
}

// hideSensitiveInfo hides sensitive information (APIKey, BaseURL) for builtin models
// Returns a copy of the model with sensitive fields cleared if it's a builtin model
func hideSensitiveInfo(model *types.Model) *types.Model {
	if !model.IsBuiltin {
		return model
	}

	// Create a copy with sensitive information hidden
	return &types.Model{
		ID:          model.ID,
		TenantID:    model.TenantID,
		Name:        model.Name,
		Type:        model.Type,
		Source:      model.Source,
		Description: model.Description,
		Parameters: types.ModelParameters{
			// Hide APIKey and BaseURL for builtin models
			BaseURL: "",
			APIKey:  "",
			// Keep other parameters like embedding dimensions
			EmbeddingParameters: model.Parameters.EmbeddingParameters,
			ParameterSize:       model.Parameters.ParameterSize,
		},
		IsBuiltin: model.IsBuiltin,
		Status:    model.Status,
		CreatedAt: model.CreatedAt,
		UpdatedAt: model.UpdatedAt,
	}
}

// CreateModelRequest defines the structure for model creation requests
// Contains all fields required to create a new model in the system
type CreateModelRequest struct {
	Name        string                `json:"name"        binding:"required"`
	Type        types.ModelType       `json:"type"        binding:"required"`
	Source      types.ModelSource     `json:"source"      binding:"required"`
	Description string                `json:"description"`
	Parameters  types.ModelParameters `json:"parameters"  binding:"required"`
}

// CreateModel godoc
// @Summary      创建模型
// @Description  创建新的模型配置
// @Tags         模型管理
// @Accept       json
// @Produce      json
// @Param        request  body      CreateModelRequest  true  "模型信息"
// @Success      201      {object}  map[string]interface{}  "创建的模型"
// @Failure      400      {object}  errors.AppError         "请求参数错误"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /models [post]
func (h *ModelHandler) CreateModel(c *gin.Context) {
	ctx := c.Request.Context()

	logger.Info(ctx, "Start creating model")

	var req CreateModelRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Error(ctx, "Failed to parse request parameters", err)
		c.Error(errors.NewBadRequestError(err.Error()))
		return
	}
	tenantID := c.GetUint64(types.TenantIDContextKey.String())
	if tenantID == 0 {
		logger.Error(ctx, "Tenant ID is empty")
		c.Error(errors.NewBadRequestError("Tenant ID cannot be empty"))
		return
	}

	logger.Infof(ctx, "Creating model, Tenant ID: %d, Model name: %s, Model type: %s",
		tenantID, secutils.SanitizeForLog(req.Name), secutils.SanitizeForLog(string(req.Type)))

	model := &types.Model{
		TenantID:    tenantID,
		Name:        secutils.SanitizeForLog(req.Name),
		Type:        types.ModelType(secutils.SanitizeForLog(string(req.Type))),
		Source:      req.Source,
		Description: secutils.SanitizeForLog(req.Description),
		Parameters:  req.Parameters,
	}

	if err := h.service.CreateModel(ctx, model); err != nil {
		logger.ErrorWithFields(ctx, err, nil)
		c.Error(errors.NewInternalServerError(err.Error()))
		return
	}

	logger.Infof(
		ctx,
		"Model created successfully, ID: %s, Name: %s",
		secutils.SanitizeForLog(model.ID),
		secutils.SanitizeForLog(model.Name),
	)

	// Hide sensitive information for builtin models (though newly created models are unlikely to be builtin)
	responseModel := hideSensitiveInfo(model)

	c.JSON(http.StatusCreated, gin.H{
		"success": true,
		"data":    responseModel,
	})
}

// GetModel godoc
// @Summary      获取模型详情
// @Description  根据ID获取模型详情
// @Tags         模型管理
// @Accept       json
// @Produce      json
// @Param        id   path      string  true  "模型ID"
// @Success      200  {object}  map[string]interface{}  "模型详情"
// @Failure      404  {object}  errors.AppError         "模型不存在"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /models/{id} [get]
func (h *ModelHandler) GetModel(c *gin.Context) {
	ctx := c.Request.Context()

	logger.Info(ctx, "Start retrieving model")

	id := secutils.SanitizeForLog(c.Param("id"))
	if id == "" {
		logger.Error(ctx, "Model ID is empty")
		c.Error(errors.NewBadRequestError("Model ID cannot be empty"))
		return
	}

	logger.Infof(ctx, "Retrieving model, ID: %s", id)
	model, err := h.service.GetModelByID(ctx, id)
	if err != nil {
		if err == service.ErrModelNotFound {
			logger.Warnf(ctx, "Model not found, ID: %s", id)
			c.Error(errors.NewNotFoundError("Model not found"))
			return
		}
		logger.ErrorWithFields(ctx, err, nil)
		c.Error(errors.NewInternalServerError(err.Error()))
		return
	}

	logger.Infof(ctx, "Retrieved model successfully, ID: %s, Name: %s", model.ID, model.Name)

	// Hide sensitive information for builtin models
	responseModel := hideSensitiveInfo(model)
	if model.IsBuiltin {
		logger.Infof(ctx, "Builtin model detected, hiding sensitive information for model: %s", model.ID)
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    responseModel,
	})
}

// ListModels godoc
// @Summary      获取模型列表
// @Description  获取当前租户的所有模型
// @Tags         模型管理
// @Accept       json
// @Produce      json
// @Success      200  {object}  map[string]interface{}  "模型列表"
// @Failure      400  {object}  errors.AppError         "请求参数错误"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /models [get]
func (h *ModelHandler) ListModels(c *gin.Context) {
	ctx := c.Request.Context()

	logger.Info(ctx, "Start retrieving model list")

	tenantID := c.GetUint64(types.TenantIDContextKey.String())
	if tenantID == 0 {
		logger.Error(ctx, "Tenant ID is empty")
		c.Error(errors.NewBadRequestError("Tenant ID cannot be empty"))
		return
	}

	models, err := h.service.ListModels(ctx)
	if err != nil {
		logger.ErrorWithFields(ctx, err, nil)
		c.Error(errors.NewInternalServerError(err.Error()))
		return
	}

	logger.Infof(ctx, "Retrieved model list successfully, Tenant ID: %d, Total: %d models", tenantID, len(models))

	// Hide sensitive information for builtin models in the list
	responseModels := make([]*types.Model, len(models))
	for i, model := range models {
		responseModels[i] = hideSensitiveInfo(model)
		if model.IsBuiltin {
			logger.Infof(ctx, "Builtin model detected in list, hiding sensitive information for model: %s", model.ID)
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    responseModels,
	})
}

// UpdateModelRequest defines the structure for model update requests
// Contains fields that can be updated for an existing model
type UpdateModelRequest struct {
	Name        string                `json:"name"`
	Description string                `json:"description"`
	Parameters  types.ModelParameters `json:"parameters"`
	Source      types.ModelSource     `json:"source"`
	Type        types.ModelType       `json:"type"`
}

// UpdateModel godoc
// @Summary      更新模型
// @Description  更新模型配置信息
// @Tags         模型管理
// @Accept       json
// @Produce      json
// @Param        id       path      string              true  "模型ID"
// @Param        request  body      UpdateModelRequest  true  "更新信息"
// @Success      200      {object}  map[string]interface{}  "更新后的模型"
// @Failure      404      {object}  errors.AppError         "模型不存在"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /models/{id} [put]
func (h *ModelHandler) UpdateModel(c *gin.Context) {
	ctx := c.Request.Context()

	logger.Info(ctx, "Start updating model")

	id := secutils.SanitizeForLog(c.Param("id"))
	if id == "" {
		logger.Error(ctx, "Model ID is empty")
		c.Error(errors.NewBadRequestError("Model ID cannot be empty"))
		return
	}

	var req UpdateModelRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Error(ctx, "Failed to parse request parameters", err)
		c.Error(errors.NewBadRequestError(err.Error()))
		return
	}

	logger.Infof(ctx, "Retrieving model information, ID: %s", id)
	model, err := h.service.GetModelByID(ctx, id)
	if err != nil {
		if err == service.ErrModelNotFound {
			logger.Warnf(ctx, "Model not found, ID: %s", id)
			c.Error(errors.NewNotFoundError("Model not found"))
			return
		}
		logger.ErrorWithFields(ctx, err, nil)
		c.Error(errors.NewInternalServerError(err.Error()))
		return
	}

	// Update model fields if they are provided in the request
	if req.Name != "" {
		model.Name = req.Name
	}
	model.Description = req.Description
	// Check if any Parameters field is set (can't use struct comparison due to map field)
	if req.Parameters.BaseURL != "" || req.Parameters.APIKey != "" || req.Parameters.Provider != "" {
		model.Parameters = req.Parameters
	}
	model.Source = req.Source
	model.Type = req.Type

	logger.Infof(ctx, "Updating model, ID: %s, Name: %s", id, model.Name)
	if err := h.service.UpdateModel(ctx, model); err != nil {
		logger.ErrorWithFields(ctx, err, nil)
		c.Error(errors.NewInternalServerError(err.Error()))
		return
	}

	logger.Infof(ctx, "Model updated successfully, ID: %s", id)

	// Hide sensitive information for builtin models (though builtin models cannot be updated)
	responseModel := hideSensitiveInfo(model)

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    responseModel,
	})
}

// DeleteModel godoc
// @Summary      删除模型
// @Description  删除指定的模型
// @Tags         模型管理
// @Accept       json
// @Produce      json
// @Param        id   path      string  true  "模型ID"
// @Success      200  {object}  map[string]interface{}  "删除成功"
// @Failure      404  {object}  errors.AppError         "模型不存在"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /models/{id} [delete]
func (h *ModelHandler) DeleteModel(c *gin.Context) {
	ctx := c.Request.Context()

	logger.Info(ctx, "Start deleting model")

	id := secutils.SanitizeForLog(c.Param("id"))
	if id == "" {
		logger.Error(ctx, "Model ID is empty")
		c.Error(errors.NewBadRequestError("Model ID cannot be empty"))
		return
	}

	logger.Infof(ctx, "Deleting model, ID: %s", id)
	if err := h.service.DeleteModel(ctx, id); err != nil {
		if err == service.ErrModelNotFound {
			logger.Warnf(ctx, "Model not found, ID: %s", id)
			c.Error(errors.NewNotFoundError("Model not found"))
			return
		}
		logger.ErrorWithFields(ctx, err, nil)
		c.Error(errors.NewInternalServerError(err.Error()))
		return
	}

	logger.Infof(ctx, "Model deleted successfully, ID: %s", id)
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Model deleted",
	})
}

// ModelProviderDTO 模型厂商信息 DTO
type ModelProviderDTO struct {
	Value       string            `json:"value"`       // provider 标识符
	Label       string            `json:"label"`       // 显示名称
	Description string            `json:"description"` // 描述
	DefaultURLs map[string]string `json:"defaultUrls"` // 按模型类型区分的默认 URL
	ModelTypes  []string          `json:"modelTypes"`  // 支持的模型类型
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
	default:
		return string(mt)
	}
}

// ListModelProviders godoc
// @Summary      获取模型厂商列表
// @Description  根据模型类型获取支持的厂商列表及配置信息
// @Tags         模型管理
// @Accept       json
// @Produce      json
// @Param        model_type  query     string  false  "模型类型 (chat, embedding, rerank, vllm)"
// @Success      200         {object}  map[string]interface{}  "厂商列表"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /models/providers [get]
func (h *ModelHandler) ListModelProviders(c *gin.Context) {
	ctx := c.Request.Context()

	modelType := c.Query("model_type")
	logger.Infof(ctx, "Listing model providers for type: %s", secutils.SanitizeForLog(modelType))

	// 将前端类型映射到后端类型
	// 前端: chat, embedding, rerank, vllm
	// 后端: KnowledgeQA, Embedding, Rerank, VLLM
	var backendModelType types.ModelType
	switch modelType {
	case "chat":
		backendModelType = types.ModelTypeKnowledgeQA
	case "embedding":
		backendModelType = types.ModelTypeEmbedding
	case "rerank":
		backendModelType = types.ModelTypeRerank
	case "vllm":
		backendModelType = types.ModelTypeVLLM
	default:
		backendModelType = types.ModelType(modelType)
	}

	var providers []provider.ProviderInfo
	if modelType != "" {
		// 按模型类型过滤
		providers = provider.ListByModelType(backendModelType)
	} else {
		// 返回所有 provider
		providers = provider.List()
	}

	// 转换为 DTO
	result := make([]ModelProviderDTO, 0, len(providers))
	for _, p := range providers {
		// 转换 DefaultURLs map[types.ModelType]string -> map[string]string
		// 使用前端兼容的 key (chat 而不是 KnowledgeQA)
		defaultURLs := make(map[string]string)
		for mt, url := range p.DefaultURLs {
			frontendType := modelTypeToFrontend(mt)
			defaultURLs[frontendType] = url
		}

		// 转换 ModelTypes 为前端兼容格式
		modelTypes := make([]string, 0, len(p.ModelTypes))
		for _, mt := range p.ModelTypes {
			modelTypes = append(modelTypes, modelTypeToFrontend(mt))
		}

		result = append(result, ModelProviderDTO{
			Value:       string(p.Name),
			Label:       p.DisplayName,
			Description: p.Description,
			DefaultURLs: defaultURLs,
			ModelTypes:  modelTypes,
		})
	}

	logger.Infof(ctx, "Retrieved %d providers", len(result))
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    result,
	})
}
