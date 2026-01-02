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

// ModelHandler는 모델 관련 작업을 위한 HTTP 요청을 처리합니다.
// 모델 생성, 조회, 업데이트, 삭제에 필요한 메서드를 구현합니다.
type ModelHandler struct {
	service interfaces.ModelService
}

// NewModelHandler는 ModelHandler의 새 인스턴스를 생성합니다.
// 비즈니스 로직을 처리하는 모델 서비스 구현이 필요합니다.
// 매개변수:
//   - service: ModelService 인터페이스 구현체
//
// 반환값: 새로 생성된 ModelHandler에 대한 포인터
func NewModelHandler(service interfaces.ModelService) *ModelHandler {
	return &ModelHandler{service: service}
}

// hideSensitiveInfo는 내장 모델의 민감한 정보(APIKey, BaseURL)를 숨깁니다.
// 내장 모델인 경우 민감한 필드가 지워진 모델 복사본을 반환합니다.
func hideSensitiveInfo(model *types.Model) *types.Model {
	if !model.IsBuiltin {
		return model
	}

	// 민감한 정보가 숨겨진 복사본 생성
	return &types.Model{
		ID:          model.ID,
		TenantID:    model.TenantID,
		Name:        model.Name,
		Type:        model.Type,
		Source:      model.Source,
		Description: model.Description,
		Parameters: types.ModelParameters{
			// 내장 모델의 APIKey와 BaseURL 숨김
			BaseURL: "",
			APIKey:  "",
			// 임베딩 차원과 같은 다른 매개변수는 유지
			EmbeddingParameters: model.Parameters.EmbeddingParameters,
			ParameterSize:       model.Parameters.ParameterSize,
		},
		IsBuiltin: model.IsBuiltin,
		Status:    model.Status,
		CreatedAt: model.CreatedAt,
		UpdatedAt: model.UpdatedAt,
	}
}

// CreateModelRequest는 모델 생성 요청을 위한 구조를 정의합니다.
// 시스템에 새 모델을 생성하는 데 필요한 모든 필드를 포함합니다.
type CreateModelRequest struct {
	Name        string                `json:"name"        binding:"required"`
	Type        types.ModelType       `json:"type"        binding:"required"`
	Source      types.ModelSource     `json:"source"      binding:"required"`
	Description string                `json:"description"`
	Parameters  types.ModelParameters `json:"parameters"  binding:"required"`
}

// CreateModel godoc
// @Summary      모델 생성
// @Description  새 모델 구성 생성
// @Tags         모델 관리
// @Accept       json
// @Produce      json
// @Param        request  body      CreateModelRequest  true  "모델 정보"
// @Success      201      {object}  map[string]interface{}  "생성된 모델"
// @Failure      400      {object}  errors.AppError         "요청 매개변수 오류"
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

	// 내장 모델에 대한 민감한 정보 숨김 (새로 생성된 모델이 내장 모델일 가능성은 낮음)
	responseModel := hideSensitiveInfo(model)

	c.JSON(http.StatusCreated, gin.H{
		"success": true,
		"data":    responseModel,
	})
}

// GetModel godoc
// @Summary      모델 상세 정보 조회
// @Description  ID를 기반으로 모델 상세 정보 조회
// @Tags         모델 관리
// @Accept       json
// @Produce      json
// @Param        id   path      string  true  "모델 ID"
// @Success      200  {object}  map[string]interface{}  "모델 상세 정보"
// @Failure      404  {object}  errors.AppError         "모델을 찾을 수 없음"
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

	// 내장 모델에 대한 민감한 정보 숨김
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
// @Summary      모델 목록 조회
// @Description  현재 테넌트의 모든 모델 조회
// @Tags         모델 관리
// @Accept       json
// @Produce      json
// @Success      200  {object}  map[string]interface{}  "모델 목록"
// @Failure      400  {object}  errors.AppError         "요청 매개변수 오류"
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

	// 목록에서 내장 모델에 대한 민감한 정보 숨김
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

// UpdateModelRequest는 모델 업데이트 요청을 위한 구조를 정의합니다.
// 기존 모델에 대해 업데이트할 수 있는 필드를 포함합니다.
type UpdateModelRequest struct {
	Name        string                `json:"name"`
	Description string                `json:"description"`
	Parameters  types.ModelParameters `json:"parameters"`
	Source      types.ModelSource     `json:"source"`
	Type        types.ModelType       `json:"type"`
}

// UpdateModel godoc
// @Summary      모델 업데이트
// @Description  모델 구성 정보 업데이트
// @Tags         모델 관리
// @Accept       json
// @Produce      json
// @Param        id       path      string              true  "모델 ID"
// @Param        request  body      UpdateModelRequest  true  "업데이트 정보"
// @Success      200      {object}  map[string]interface{}  "업데이트된 모델"
// @Failure      404      {object}  errors.AppError         "모델을 찾을 수 없음"
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

	// 요청에 제공된 경우 모델 필드 업데이트
	if req.Name != "" {
		model.Name = req.Name
	}
	model.Description = req.Description
	// Parameters 필드가 설정되었는지 확인 (맵 필드로 인해 구조체 비교 불가)
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

	// 내장 모델에 대한 민감한 정보 숨김 (내장 모델은 업데이트할 수 없지만)
	responseModel := hideSensitiveInfo(model)

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    responseModel,
	})
}

// DeleteModel godoc
// @Summary      모델 삭제
// @Description  지정된 모델 삭제
// @Tags         모델 관리
// @Accept       json
// @Produce      json
// @Param        id   path      string  true  "모델 ID"
// @Success      200  {object}  map[string]interface{}  "삭제 성공"
// @Failure      404  {object}  errors.AppError         "모델을 찾을 수 없음"
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

// ModelProviderDTO 모델 공급자 정보 DTO
type ModelProviderDTO struct {
	Value       string            `json:"value"`       // provider 식별자
	Label       string            `json:"label"`       // 표시 이름
	Description string            `json:"description"` // 설명
	DefaultURLs map[string]string `json:"defaultUrls"` // 모델 유형별 기본 URL
	ModelTypes  []string          `json:"modelTypes"`  // 지원되는 모델 유형
}

// modelTypeToFrontend 백엔드 ModelType을 프론트엔드 호환 문자열로 변환
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
// @Summary      모델 공급자 목록 조회
// @Description  모델 유형에 따른 지원되는 공급자 목록 및 구성 정보 조회
// @Tags         모델 관리
// @Accept       json
// @Produce      json
// @Param        model_type  query     string  false  "모델 유형 (chat, embedding, rerank, vllm)"
// @Success      200         {object}  map[string]interface{}  "공급자 목록"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /models/providers [get]
func (h *ModelHandler) ListModelProviders(c *gin.Context) {
	ctx := c.Request.Context()

	modelType := c.Query("model_type")
	logger.Infof(ctx, "Listing model providers for type: %s", secutils.SanitizeForLog(modelType))

	// 프론트엔드 유형을 백엔드 유형으로 매핑
	// 프론트엔드: chat, embedding, rerank, vllm
	// 백엔드: KnowledgeQA, Embedding, Rerank, VLLM
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
		// 모델 유형별 필터링
		providers = provider.ListByModelType(backendModelType)
	} else {
		// 모든 공급자 반환
		providers = provider.List()
	}

	// DTO로 변환
	result := make([]ModelProviderDTO, 0, len(providers))
	for _, p := range providers {
		// DefaultURLs map[types.ModelType]string -> map[string]string 변환
		// 프론트엔드 호환 키 사용 (KnowledgeQA 대신 chat)
		defaultURLs := make(map[string]string)
		for mt, url := range p.DefaultURLs {
			frontendType := modelTypeToFrontend(mt)
			defaultURLs[frontendType] = url
		}

		// ModelTypes를 프론트엔드 호환 형식으로 변환
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
