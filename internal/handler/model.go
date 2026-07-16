package handler

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/application/service"
	"github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/handler/dto"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/chat"
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

// Per-response redaction/stripping for Model now lives in
// dto.NewModelResponse — handlers must use it for every body that contains a
// model. The previous hideSensitiveInfo helper has been removed.

// CreateModelRequest defines the structure for model creation requests
// Contains all fields required to create a new model in the system
type CreateModelRequest struct {
	Name        string                `json:"name"        binding:"required"`
	DisplayName string                `json:"display_name"`
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
		c.Error(errors.NewBadRequestError("Workspace ID cannot be empty"))
		return
	}

	logger.Infof(ctx, "Creating model, Tenant ID: %d, Model name: %s, Model type: %s",
		tenantID, secutils.SanitizeForLog(req.Name), secutils.SanitizeForLog(string(req.Type)))

	// SSRF validation for model BaseURL
	if req.Parameters.BaseURL != "" {
		if err := secutils.ValidateURLForSSRF(req.Parameters.BaseURL); err != nil {
			logger.Warnf(ctx, "SSRF validation failed for model BaseURL: %v", err)
			c.Error(errors.NewBadRequestError(secutils.FormatSSRFError("Base URL", req.Parameters.BaseURL, err)))
			return
		}
	}

	model := &types.Model{
		TenantID:    tenantID,
		Name:        secutils.SanitizeForLog(req.Name),
		DisplayName: secutils.SanitizeForLog(req.DisplayName),
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

	c.JSON(http.StatusCreated, gin.H{
		"success": true,
		"data":    dto.NewModelResponse(ctx, model),
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

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    dto.NewModelResponse(ctx, model),
	})
}

// ListModels godoc
// @Summary      获取模型列表
// @Description  获取当前空间的所有模型
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
		c.Error(errors.NewBadRequestError("Workspace ID cannot be empty"))
		return
	}

	models, err := h.service.ListModels(ctx)
	if err != nil {
		logger.ErrorWithFields(ctx, err, nil)
		c.Error(errors.NewInternalServerError(err.Error()))
		return
	}

	logger.Infof(ctx, "Retrieved model list successfully, Tenant ID: %d, Total: %d models", tenantID, len(models))

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    dto.NewModelResponses(ctx, models),
	})
}

const (
	modelDebugMaxInputBytes = 64 * 1024
	modelDebugMaxFileBytes  = 20 * 1024 * 1024
)

// ModelDebugOptions contains the cross-provider parameters exposed by the
// model debugger. Pointer fields preserve explicit zero/false values.
type ModelDebugOptions struct {
	SystemPrompt string   `json:"system_prompt,omitempty"`
	Temperature  *float64 `json:"temperature,omitempty"`
	TopP         *float64 `json:"top_p,omitempty"`
	MaxTokens    *int     `json:"max_tokens,omitempty"`
	Thinking     *bool    `json:"thinking,omitempty"`
}

func parseModelDebugOptions(raw string) (ModelDebugOptions, error) {
	var opts ModelDebugOptions
	if strings.TrimSpace(raw) == "" {
		return opts, nil
	}
	if err := json.Unmarshal([]byte(raw), &opts); err != nil {
		return opts, fmt.Errorf("invalid options: %w", err)
	}
	if opts.MaxTokens != nil && (*opts.MaxTokens < 1 || *opts.MaxTokens > 8192) {
		return opts, fmt.Errorf("max_tokens must be between 1 and 8192")
	}
	if opts.Temperature != nil && (*opts.Temperature < 0 || *opts.Temperature > 2) {
		return opts, fmt.Errorf("temperature must be between 0 and 2")
	}
	if opts.TopP != nil && (*opts.TopP <= 0 || *opts.TopP > 1) {
		return opts, fmt.Errorf("top_p must be greater than 0 and at most 1")
	}
	return opts, nil
}

func redactedDebugConfig(config map[string]string) map[string]string {
	if len(config) == 0 {
		return nil
	}
	out := make(map[string]string, len(config))
	for key, value := range config {
		lower := strings.ToLower(key)
		if strings.Contains(lower, "secret") ||
			strings.Contains(lower, "token") ||
			strings.Contains(lower, "password") ||
			strings.Contains(lower, "api_key") ||
			strings.Contains(lower, "apikey") ||
			strings.Contains(lower, "authorization") {
			out[key] = "[REDACTED]"
			continue
		}
		out[key] = value
	}
	return out
}

func modelDebugRequestPreview(model *types.Model, input string, documents []string, opts ModelDebugOptions, fileName string, fileSize int64) gin.H {
	preview := gin.H{
		"model_id":   model.ID,
		"model_name": model.Name,
		"model_type": model.Type,
		"source":     model.Source,
		"provider":   model.Parameters.Provider,
		"input":      input,
		"options":    opts,
	}
	if len(documents) > 0 {
		preview["documents"] = documents
	}
	if fileName != "" {
		preview["file"] = gin.H{"name": fileName, "size": fileSize}
	}
	if model.Parameters.ExtraConfig != nil {
		preview["model_extra_config"] = redactedDebugConfig(model.Parameters.ExtraConfig)
	}
	if len(model.Parameters.CustomHeaders) > 0 {
		headerNames := make([]string, 0, len(model.Parameters.CustomHeaders))
		for name := range model.Parameters.CustomHeaders {
			headerNames = append(headerNames, name)
		}
		preview["custom_header_names"] = headerNames
	}
	return preview
}

func writeModelDebugResult(c *gin.Context, started time.Time, request gin.H, response any, callErr error, observations gin.H) {
	data := gin.H{
		"ok":           callErr == nil,
		"elapsed_ms":   time.Since(started).Milliseconds(),
		"request":      request,
		"raw_response": response,
		"observations": observations,
	}
	if callErr != nil {
		data["error"] = callErr.Error()
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": data})
}

type modelDebugChatStreamResponse struct {
	Content          string                 `json:"content"`
	ReasoningContent string                 `json:"reasoning_content,omitempty"`
	ToolCalls        []types.LLMToolCall    `json:"tool_calls,omitempty"`
	FinishReason     string                 `json:"finish_reason,omitempty"`
	Usage            *types.TokenUsage      `json:"usage,omitempty"`
	StreamEvents     []types.StreamResponse `json:"stream_events"`
}

func consumeModelDebugChatStream(stream <-chan types.StreamResponse) (*modelDebugChatStreamResponse, error) {
	result := &modelDebugChatStreamResponse{
		StreamEvents: make([]types.StreamResponse, 0),
	}
	for event := range stream {
		result.StreamEvents = append(result.StreamEvents, event)
		switch event.ResponseType {
		case types.ResponseTypeThinking:
			result.ReasoningContent += event.Content
		case types.ResponseTypeAnswer:
			result.Content += event.Content
			if len(event.ToolCalls) > 0 {
				result.ToolCalls = event.ToolCalls
			}
			if event.FinishReason != "" {
				result.FinishReason = event.FinishReason
			}
			if event.Usage != nil {
				result.Usage = event.Usage
			}
		case types.ResponseTypeToolCall:
			if len(event.ToolCalls) > 0 {
				result.ToolCalls = event.ToolCalls
			}
		case types.ResponseTypeError:
			return result, fmt.Errorf("%s", event.Content)
		}
	}
	return result, nil
}

// DebugModel executes a saved model through the same service constructors used
// by production calls and returns the complete normalized response. Credentials
// stay server-side; the request preview contains only non-secret fields.
func (h *ModelHandler) DebugModel(c *gin.Context) {
	ctx := c.Request.Context()
	started := time.Now()
	id := secutils.SanitizeForLog(c.Param("id"))
	if id == "" {
		c.Error(errors.NewBadRequestError("Model ID cannot be empty"))
		return
	}

	model, err := h.service.GetModelByID(ctx, id)
	if err != nil {
		if err == service.ErrModelNotFound {
			c.Error(errors.NewNotFoundError("Model not found"))
			return
		}
		c.Error(errors.NewInternalServerError(err.Error()))
		return
	}

	input := c.PostForm("input")
	if len(input) > modelDebugMaxInputBytes {
		c.Error(errors.NewBadRequestError("input is too long"))
		return
	}
	opts, err := parseModelDebugOptions(c.PostForm("options"))
	if err != nil {
		c.Error(errors.NewBadRequestError(err.Error()))
		return
	}
	var documents []string
	if rawDocuments := c.PostForm("documents"); strings.TrimSpace(rawDocuments) != "" {
		if err := json.Unmarshal([]byte(rawDocuments), &documents); err != nil {
			c.Error(errors.NewBadRequestError("documents must be a JSON string array"))
			return
		}
		if len(documents) > 100 {
			c.Error(errors.NewBadRequestError("documents cannot exceed 100 items"))
			return
		}
	}

	var (
		fileBytes []byte
		fileName  string
		fileSize  int64
	)
	if file, header, fileErr := c.Request.FormFile("file"); fileErr == nil {
		defer file.Close()
		fileName = header.Filename
		fileSize = header.Size
		if fileSize > modelDebugMaxFileBytes {
			c.Error(errors.NewBadRequestError("file cannot exceed 20 MB"))
			return
		}
		fileBytes, err = io.ReadAll(io.LimitReader(file, modelDebugMaxFileBytes+1))
		if err != nil {
			c.Error(errors.NewBadRequestError("failed to read uploaded file"))
			return
		}
		if len(fileBytes) > modelDebugMaxFileBytes {
			c.Error(errors.NewBadRequestError("file cannot exceed 20 MB"))
			return
		}
		fileSize = int64(len(fileBytes))
	}

	requestPreview := modelDebugRequestPreview(model, input, documents, opts, fileName, fileSize)
	observations := gin.H{}

	switch model.Type {
	case types.ModelTypeKnowledgeQA:
		if strings.TrimSpace(input) == "" {
			c.Error(errors.NewBadRequestError("query cannot be empty"))
			return
		}
		instance, callErr := h.service.GetChatModel(ctx, id)
		if callErr != nil {
			writeModelDebugResult(c, started, requestPreview, nil, callErr, observations)
			return
		}
		messages := make([]chat.Message, 0, 2)
		if strings.TrimSpace(opts.SystemPrompt) != "" {
			messages = append(messages, chat.Message{Role: "system", Content: opts.SystemPrompt})
		}
		messages = append(messages, chat.Message{Role: "user", Content: input})
		chatOpts := &chat.ChatOptions{}
		if opts.Temperature != nil {
			chatOpts.Temperature = *opts.Temperature
		}
		if opts.TopP != nil {
			chatOpts.TopP = *opts.TopP
		}
		if opts.MaxTokens != nil {
			chatOpts.MaxTokens = *opts.MaxTokens
		}
		chatOpts.Thinking = opts.Thinking
		chatConfig := chat.ConfigFromModel(model, "", "")
		thinkingControl := chat.EffectiveThinkingControl(chatConfig)
		observations["stream"] = true
		observations["requested_thinking"] = opts.Thinking != nil && *opts.Thinking
		observations["thinking_control"] = thinkingControl
		observations["thinking_parameter_sent"] = opts.Thinking != nil && thinkingControl != "none"

		stream, callErr := instance.ChatStream(ctx, messages, chatOpts)
		if callErr != nil {
			writeModelDebugResult(c, started, requestPreview, nil, callErr, observations)
			return
		}
		resp, callErr := consumeModelDebugChatStream(stream)
		if resp != nil {
			observations["reasoning_returned"] = strings.TrimSpace(resp.ReasoningContent) != ""
			observations["reasoning_characters"] = len([]rune(resp.ReasoningContent))
			observations["answer_characters"] = len([]rune(resp.Content))
		}
		writeModelDebugResult(c, started, requestPreview, resp, callErr, observations)
	case types.ModelTypeEmbedding:
		if strings.TrimSpace(input) == "" {
			c.Error(errors.NewBadRequestError("input cannot be empty"))
			return
		}
		instance, callErr := h.service.GetEmbeddingModel(ctx, id)
		if callErr != nil {
			writeModelDebugResult(c, started, requestPreview, nil, callErr, observations)
			return
		}
		vector, callErr := instance.Embed(ctx, input)
		observations["dimension"] = len(vector)
		writeModelDebugResult(c, started, requestPreview, vector, callErr, observations)
	case types.ModelTypeRerank:
		if strings.TrimSpace(input) == "" || len(documents) == 0 {
			c.Error(errors.NewBadRequestError("query and documents cannot be empty"))
			return
		}
		instance, callErr := h.service.GetRerankModel(ctx, id)
		if callErr != nil {
			writeModelDebugResult(c, started, requestPreview, nil, callErr, observations)
			return
		}
		results, callErr := instance.Rerank(ctx, input, documents)
		observations["result_count"] = len(results)
		writeModelDebugResult(c, started, requestPreview, results, callErr, observations)
	case types.ModelTypeVLLM:
		if len(fileBytes) == 0 {
			c.Error(errors.NewBadRequestError("image file is required"))
			return
		}
		instance, callErr := h.service.GetVLMModel(ctx, id)
		if callErr != nil {
			writeModelDebugResult(c, started, requestPreview, nil, callErr, observations)
			return
		}
		result, callErr := instance.Predict(ctx, [][]byte{fileBytes}, input)
		observations["answer_characters"] = len([]rune(result))
		writeModelDebugResult(c, started, requestPreview, result, callErr, observations)
	case types.ModelTypeASR:
		if len(fileBytes) == 0 {
			c.Error(errors.NewBadRequestError("audio file is required"))
			return
		}
		instance, callErr := h.service.GetASRModel(ctx, id)
		if callErr != nil {
			writeModelDebugResult(c, started, requestPreview, nil, callErr, observations)
			return
		}
		result, callErr := instance.Transcribe(ctx, fileBytes, fileName)
		if result != nil {
			observations["text_characters"] = len([]rune(result.Text))
			observations["segment_count"] = len(result.Segments)
		}
		writeModelDebugResult(c, started, requestPreview, result, callErr, observations)
	default:
		c.Error(errors.NewBadRequestError("unsupported model type"))
	}
}

// UpdateModelRequest defines the structure for model update requests
// Contains fields that can be updated for an existing model
type UpdateModelRequest struct {
	Name        string                `json:"name"`
	DisplayName *string               `json:"display_name"`
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
	if req.DisplayName != nil {
		model.DisplayName = secutils.SanitizeForLog(*req.DisplayName)
	}
	model.Description = req.Description

	// SSRF validation for updated model BaseURL
	if req.Parameters.BaseURL != "" {
		if err := secutils.ValidateURLForSSRF(req.Parameters.BaseURL); err != nil {
			logger.Warnf(ctx, "SSRF validation failed for model BaseURL: %v", err)
			c.Error(errors.NewBadRequestError(secutils.FormatSSRFError("Base URL", req.Parameters.BaseURL, err)))
			return
		}
	}
	// Credentials (api_key, app_secret) NEVER flow through this endpoint —
	// they live behind the /credentials subresource. Force-preserve them by
	// snapshotting the stored values before copying request fields in, so
	// that even a misbehaving caller that puts api_key in the body cannot
	// clobber a stored credential. Log a warning to spot stale callers.
	storedAPIKey := model.Parameters.APIKey
	storedAppSecret := model.Parameters.AppSecret
	if req.Parameters.APIKey != "" && req.Parameters.APIKey != storedAPIKey {
		logger.Warnf(ctx,
			"deprecated: api_key in PUT /models/%s body is ignored; use PUT /credentials instead", id)
	}
	if req.Parameters.AppSecret != "" && req.Parameters.AppSecret != storedAppSecret {
		logger.Warnf(ctx,
			"deprecated: app_secret in PUT /models/%s body is ignored; use PUT /credentials instead", id)
	}
	newParams := req.Parameters
	newParams.APIKey = storedAPIKey
	newParams.AppSecret = storedAppSecret
	// Preserve backend-managed fields not sent by the frontend either.
	newParams.ParameterSize = model.Parameters.ParameterSize
	if newParams.InterfaceType == "" {
		newParams.InterfaceType = model.Parameters.InterfaceType
	}
	if newParams.AppID == "" {
		newParams.AppID = model.Parameters.AppID
	}
	if newParams.ExtraConfig == nil {
		newParams.ExtraConfig = model.Parameters.ExtraConfig
	}
	model.Parameters = newParams

	model.Source = req.Source
	model.Type = req.Type

	logger.Infof(ctx, "Updating model, ID: %s, Name: %s", id, model.Name)
	if err := h.service.UpdateModel(ctx, model); err != nil {
		if appErr, ok := errors.IsAppError(err); ok {
			c.Error(appErr)
			return
		}
		logger.ErrorWithFields(ctx, err, nil)
		c.Error(errors.NewInternalServerError(err.Error()))
		return
	}

	logger.Infof(ctx, "Model updated successfully, ID: %s", id)

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    dto.NewModelResponse(ctx, model),
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
		if appErr, ok := errors.IsAppError(err); ok {
			c.Error(appErr)
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
	case types.ModelTypeASR:
		return "asr"
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
	case "asr":
		backendModelType = types.ModelTypeASR
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
