package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/docreader/client"
	"github.com/Tencent/WeKnora/docreader/proto"
	chatpipline "github.com/Tencent/WeKnora/internal/application/service/chat_pipline"
	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/models/embedding"
	"github.com/Tencent/WeKnora/internal/models/rerank"
	"github.com/Tencent/WeKnora/internal/models/utils/ollama"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/Tencent/WeKnora/internal/utils"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/ollama/ollama/api"
)

// DownloadTask 다운로드 작업 정보
type DownloadTask struct {
	ID        string     `json:"id"`
	ModelName string     `json:"modelName"`
	Status    string     `json:"status"` // pending, downloading, completed, failed
	Progress  float64    `json:"progress"`
	Message   string     `json:"message"`
	StartTime time.Time  `json:"startTime"`
	EndTime   *time.Time `json:"endTime,omitempty"`
}

// 전역 다운로드 작업 관리자
var (
	downloadTasks = make(map[string]*DownloadTask)
	tasksMutex    sync.RWMutex
)

// InitializationHandler 초기화 핸들러
type InitializationHandler struct {
	config           *config.Config
	tenantService    interfaces.TenantService
	modelService     interfaces.ModelService
	kbService        interfaces.KnowledgeBaseService
	kbRepository     interfaces.KnowledgeBaseRepository
	knowledgeService interfaces.KnowledgeService
	ollamaService    *ollama.OllamaService
	docReaderClient  *client.Client
}

// NewInitializationHandler 초기화 핸들러 생성
func NewInitializationHandler(
	config *config.Config,
	tenantService interfaces.TenantService,
	modelService interfaces.ModelService,
	kbService interfaces.KnowledgeBaseService,
	kbRepository interfaces.KnowledgeBaseRepository,
	knowledgeService interfaces.KnowledgeService,
	ollamaService *ollama.OllamaService,
	docReaderClient *client.Client,
) *InitializationHandler {
	return &InitializationHandler{
		config:           config,
		tenantService:    tenantService,
		modelService:     modelService,
		kbService:        kbService,
		kbRepository:     kbRepository,
		knowledgeService: knowledgeService,
		ollamaService:    ollamaService,
		docReaderClient:  docReaderClient,
	}
}

// KBModelConfigRequest 지식베이스 모델 구성 요청 (간소화됨, 모델 ID만 전달)
type KBModelConfigRequest struct {
	LLMModelID       string           `json:"llmModelId"       binding:"required"`
	EmbeddingModelID string           `json:"embeddingModelId" binding:"required"`
	VLMConfig        *types.VLMConfig `json:"vlm_config"`

	// 문서 분할 구성
	DocumentSplitting struct {
		ChunkSize    int      `json:"chunkSize"`
		ChunkOverlap int      `json:"chunkOverlap"`
		Separators   []string `json:"separators"`
	} `json:"documentSplitting"`

	// 멀티모달 구성
	Multimodal struct {
		Enabled     bool   `json:"enabled"`
		StorageType string `json:"storageType"` // "cos" or "minio"
		COS         *struct {
			SecretID   string `json:"secretId"`
			SecretKey  string `json:"secretKey"`
			Region     string `json:"region"`
			BucketName string `json:"bucketName"`
			AppID      string `json:"appId"`
			PathPrefix string `json:"pathPrefix"`
		} `json:"cos"`
		Minio *struct {
			BucketName string `json:"bucketName"`
			UseSSL     bool   `json:"useSSL"`
			PathPrefix string `json:"pathPrefix"`
		} `json:"minio"`
	} `json:"multimodal"`

	// 지식 그래프 구성
	NodeExtract struct {
		Enabled   bool                  `json:"enabled"`
		Text      string                `json:"text"`
		Tags      []string              `json:"tags"`
		Nodes     []types.GraphNode     `json:"nodes"`
		Relations []types.GraphRelation `json:"relations"`
	} `json:"nodeExtract"`

	// 질문 생성 구성
	QuestionGeneration struct {
		Enabled       bool `json:"enabled"`
		QuestionCount int  `json:"questionCount"`
	} `json:"questionGeneration"`
}

// InitializationRequest 초기화 요청 구조
type InitializationRequest struct {
	LLM struct {
		Source    string `json:"source" binding:"required"`
		ModelName string `json:"modelName" binding:"required"`
		BaseURL   string `json:"baseUrl"`
		APIKey    string `json:"apiKey"`
	} `json:"llm" binding:"required"`

	Embedding struct {
		Source    string `json:"source" binding:"required"`
		ModelName string `json:"modelName" binding:"required"`
		BaseURL   string `json:"baseUrl"`
		APIKey    string `json:"apiKey"`
		Dimension int    `json:"dimension"` // 임베딩 차원 필드 추가
	} `json:"embedding" binding:"required"`

	Rerank struct {
		Enabled   bool   `json:"enabled"`
		ModelName string `json:"modelName"`
		BaseURL   string `json:"baseUrl"`
		APIKey    string `json:"apiKey"`
	} `json:"rerank"`

	Multimodal struct {
		Enabled bool `json:"enabled"`
		VLM     *struct {
			ModelName     string `json:"modelName"`
			BaseURL       string `json:"baseUrl"`
			APIKey        string `json:"apiKey"`
			InterfaceType string `json:"interfaceType"` // "ollama" or "openai"
		} `json:"vlm,omitempty"`
		StorageType string `json:"storageType"`
		COS         *struct {
			SecretID   string `json:"secretId"`
			SecretKey  string `json:"secretKey"`
			Region     string `json:"region"`
			BucketName string `json:"bucketName"`
			AppID      string `json:"appId"`
			PathPrefix string `json:"pathPrefix"`
		} `json:"cos,omitempty"`
		Minio *struct {
			BucketName string `json:"bucketName"`
			PathPrefix string `json:"pathPrefix"`
		} `json:"minio,omitempty"`
	} `json:"multimodal"`

	DocumentSplitting struct {
		ChunkSize    int      `json:"chunkSize" binding:"required,min=100,max=10000"`
		ChunkOverlap int      `json:"chunkOverlap" binding:"min=0"`
		Separators   []string `json:"separators" binding:"required,min=1"`
	} `json:"documentSplitting" binding:"required"`

	NodeExtract struct {
		Enabled bool     `json:"enabled"`
		Text    string   `json:"text"`
		Tags    []string `json:"tags"`
		Nodes   []struct {
			Name       string   `json:"name"`
			Attributes []string `json:"attributes"`
		} `json:"nodes"`
		Relations []struct {
			Node1 string `json:"node1"`
			Node2 string `json:"node2"`
			Type  string `json:"type"`
		} `json:"relations"`
	} `json:"nodeExtract"`

	QuestionGeneration struct {
		Enabled       bool `json:"enabled"`
		QuestionCount int  `json:"questionCount"`
	} `json:"questionGeneration"`
}

// UpdateKBConfig godoc
// @Summary      지식베이스 구성 업데이트
// @Description  지식베이스 ID에 따른 모델 및 분할 구성 업데이트
// @Tags         초기화
// @Accept       json
// @Produce      json
// @Param        kbId     path      string               true  "지식베이스 ID"
// @Param        request  body      KBModelConfigRequest true  "구성 요청"
// @Success      200      {object}  map[string]interface{}  "업데이트 성공"
// @Failure      400      {object}  errors.AppError         "요청 매개변수 오류"
// @Failure      404      {object}  errors.AppError         "지식베이스를 찾을 수 없음"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /initialization/kb/{kbId}/config [put]
func (h *InitializationHandler) UpdateKBConfig(c *gin.Context) {
	ctx := c.Request.Context()
	kbIdStr := utils.SanitizeForLog(c.Param("kbId"))

	var req KBModelConfigRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Error(ctx, "Failed to parse KB config request", err)
		c.Error(errors.NewBadRequestError(err.Error()))
		return
	}

	// 지식베이스 정보 가져오기
	kb, err := h.kbService.GetKnowledgeBaseByID(ctx, kbIdStr)
	if err != nil || kb == nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{"kbId": utils.SanitizeForLog(kbIdStr)})
		c.Error(errors.NewNotFoundError("지식베이스가 존재하지 않습니다"))
		return
	}

	// Embedding 모델 수정 가능 여부 확인
	if kb.EmbeddingModelID != "" && kb.EmbeddingModelID != req.EmbeddingModelID {
		// 파일 존재 여부 확인
		knowledgeList, err := h.knowledgeService.ListPagedKnowledgeByKnowledgeBaseID(ctx,
			kbIdStr, &types.Pagination{
				Page:     1,
				PageSize: 1,
			}, "", "", "")
		if err == nil && knowledgeList != nil && knowledgeList.Total > 0 {
			logger.Error(ctx, "Cannot change embedding model when files exist")
			c.Error(errors.NewBadRequestError("지식베이스에 파일이 있어 Embedding 모델을 수정할 수 없습니다"))
			return
		}
	}

	// 데이터베이스에서 모델 상세 정보 가져오기 및 검증
	llmModel, err := h.modelService.GetModelByID(ctx, req.LLMModelID)
	if err != nil || llmModel == nil {
		logger.Error(ctx, "LLM model not found")
		c.Error(errors.NewBadRequestError("LLM 모델이 존재하지 않습니다"))
		return
	}

	embeddingModel, err := h.modelService.GetModelByID(ctx, req.EmbeddingModelID)
	if err != nil || embeddingModel == nil {
		logger.Error(ctx, "Embedding model not found")
		c.Error(errors.NewBadRequestError("Embedding 모델이 존재하지 않습니다"))
		return
	}

	// 지식베이스 모델 ID 업데이트
	kb.SummaryModelID = req.LLMModelID
	kb.EmbeddingModelID = req.EmbeddingModelID

	// 멀티모달 모델 구성 처리
	kb.VLMConfig = types.VLMConfig{}
	if req.VLMConfig != nil && req.Multimodal.Enabled && req.VLMConfig.ModelID != "" {
		vllmModel, err := h.modelService.GetModelByID(ctx, req.VLMConfig.ModelID)
		if err != nil || vllmModel == nil {
			logger.Warn(ctx, "VLM model not found")
		} else {
			kb.VLMConfig.Enabled = req.VLMConfig.Enabled
			kb.VLMConfig.ModelID = req.VLMConfig.ModelID
		}
	}
	if !kb.VLMConfig.Enabled {
		kb.VLMConfig.ModelID = ""
	}

	// 문서 분할 구성 업데이트
	if req.DocumentSplitting.ChunkSize > 0 {
		kb.ChunkingConfig.ChunkSize = req.DocumentSplitting.ChunkSize
	}
	if req.DocumentSplitting.ChunkOverlap >= 0 {
		kb.ChunkingConfig.ChunkOverlap = req.DocumentSplitting.ChunkOverlap
	}
	if len(req.DocumentSplitting.Separators) > 0 {
		kb.ChunkingConfig.Separators = req.DocumentSplitting.Separators
	}

	// 멀티모달 구성 업데이트
	if req.Multimodal.Enabled {
		switch strings.ToLower(req.Multimodal.StorageType) {
		case "cos":
			if req.Multimodal.COS != nil {
				kb.StorageConfig = types.StorageConfig{
					SecretID:   req.Multimodal.COS.SecretID,
					SecretKey:  req.Multimodal.COS.SecretKey,
					Region:     req.Multimodal.COS.Region,
					BucketName: req.Multimodal.COS.BucketName,
					AppID:      req.Multimodal.COS.AppID,
					PathPrefix: req.Multimodal.COS.PathPrefix,
					Provider:   "cos",
				}
			}
		case "minio":
			if req.Multimodal.Minio != nil {
				kb.StorageConfig = types.StorageConfig{
					BucketName: req.Multimodal.Minio.BucketName,
					PathPrefix: req.Multimodal.Minio.PathPrefix,
					Provider:   "minio",
					SecretID:   os.Getenv("MINIO_ACCESS_KEY_ID"),
					SecretKey:  os.Getenv("MINIO_SECRET_ACCESS_KEY"),
				}
			}
		}
	} else {
		// 멀티모달이 비활성화된 경우 저장소 구성 삭제
		kb.StorageConfig = types.StorageConfig{}
	}

	// 지식 그래프 구성 업데이트
	if req.NodeExtract.Enabled {
		// Nodes와 Relations를 포인터 타입으로 변환
		nodes := make([]*types.GraphNode, len(req.NodeExtract.Nodes))
		for i := range req.NodeExtract.Nodes {
			nodes[i] = &req.NodeExtract.Nodes[i]
		}
		relations := make([]*types.GraphRelation, len(req.NodeExtract.Relations))
		for i := range req.NodeExtract.Relations {
			relations[i] = &req.NodeExtract.Relations[i]
		}

		kb.ExtractConfig = &types.ExtractConfig{
			Enabled:   req.NodeExtract.Enabled,
			Text:      req.NodeExtract.Text,
			Tags:      req.NodeExtract.Tags,
			Nodes:     nodes,
			Relations: relations,
		}
	} else {
		kb.ExtractConfig = &types.ExtractConfig{Enabled: false}
	}
	if err := validateExtractConfig(kb.ExtractConfig); err != nil {
		logger.Error(ctx, "Invalid extract configuration", err)
		c.Error(err)
		return
	}

	// 질문 생성 구성 업데이트
	if req.QuestionGeneration.Enabled {
		questionCount := req.QuestionGeneration.QuestionCount
		if questionCount <= 0 {
			questionCount = 3
		}
		if questionCount > 10 {
			questionCount = 10
		}
		kb.QuestionGenerationConfig = &types.QuestionGenerationConfig{
			Enabled:       true,
			QuestionCount: questionCount,
		}
	} else {
		kb.QuestionGenerationConfig = &types.QuestionGenerationConfig{Enabled: false}
	}

	// 업데이트된 지식베이스 저장
	if err := h.kbRepository.UpdateKnowledgeBase(ctx, kb); err != nil {
		logger.Error(ctx, "Failed to update knowledge base", err)
		c.Error(errors.NewInternalServerError("지식베이스 업데이트 실패: " + err.Error()))
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "구성 업데이트 성공",
	})
}

// InitializeByKB godoc
// @Summary      지식베이스 구성 초기화
// @Description  지식베이스 ID에 따라 전체 구성 업데이트 실행
// @Tags         초기화
// @Accept       json
// @Produce      json
// @Param        kbId     path      string  true  "지식베이스 ID"
// @Param        request  body      object  true  "초기화 요청"
// @Success      200      {object}  map[string]interface{}  "초기화 성공"
// @Failure      400      {object}  errors.AppError         "요청 매개변수 오류"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /initialization/kb/{kbId} [post]
func (h *InitializationHandler) InitializeByKB(c *gin.Context) {
	ctx := c.Request.Context()
	kbIdStr := utils.SanitizeForLog(c.Param("kbId"))

	req, err := h.bindInitializationRequest(ctx, c)
	if err != nil {
		c.Error(err)
		return
	}

	logger.Infof(
		ctx,
		"Starting knowledge base configuration update, kbId: %s, request: %s",
		utils.SanitizeForLog(kbIdStr),
		utils.SanitizeForLog(utils.ToJSON(req)),
	)

	kb, err := h.getKnowledgeBaseForInitialization(ctx, kbIdStr)
	if err != nil {
		c.Error(err)
		return
	}

	if err := h.validateInitializationConfigs(ctx, req); err != nil {
		c.Error(err)
		return
	}

	processedModels, err := h.processInitializationModels(ctx, kb, kbIdStr, req)
	if err != nil {
		c.Error(err)
		return
	}

	h.applyKnowledgeBaseInitialization(kb, req, processedModels)

	if err := h.kbRepository.UpdateKnowledgeBase(ctx, kb); err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{"kbId": utils.SanitizeForLog(kbIdStr)})
		c.Error(errors.NewInternalServerError("지식베이스 구성 업데이트 실패: " + err.Error()))
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "지식베이스 구성 업데이트 성공",
		"data": gin.H{
			"models":         processedModels,
			"knowledge_base": kb,
		},
	})
}

func (h *InitializationHandler) bindInitializationRequest(ctx context.Context, c *gin.Context) (*InitializationRequest, error) {
	var req InitializationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Error(ctx, "Failed to parse initialization request", err)
		return nil, errors.NewBadRequestError(err.Error())
	}
	return &req, nil
}

func (h *InitializationHandler) getKnowledgeBaseForInitialization(ctx context.Context, kbIdStr string) (*types.KnowledgeBase, error) {
	kb, err := h.kbService.GetKnowledgeBaseByID(ctx, kbIdStr)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{"kbId": utils.SanitizeForLog(kbIdStr)})
		return nil, errors.NewInternalServerError("지식베이스 정보 가져오기 실패: " + err.Error())
	}
	if kb == nil {
		logger.Error(ctx, "Knowledge base not found")
		return nil, errors.NewNotFoundError("지식베이스가 존재하지 않습니다")
	}
	return kb, nil
}

func (h *InitializationHandler) validateInitializationConfigs(ctx context.Context, req *InitializationRequest) error {
	if err := h.validateMultimodalConfig(ctx, req); err != nil {
		return err
	}
	if err := validateRerankConfig(ctx, req); err != nil {
		return err
	}
	return validateNodeExtractConfig(ctx, req)
}

func (h *InitializationHandler) validateMultimodalConfig(ctx context.Context, req *InitializationRequest) error {
	if !req.Multimodal.Enabled {
		return nil
	}

	storageType := strings.ToLower(req.Multimodal.StorageType)
	if req.Multimodal.VLM == nil {
		logger.Error(ctx, "Multimodal enabled but missing VLM configuration")
		return errors.NewBadRequestError("멀티모달 활성화 시 VLM 정보 구성이 필요합니다")
	}
	if req.Multimodal.VLM.InterfaceType == "ollama" {
		req.Multimodal.VLM.BaseURL = os.Getenv("OLLAMA_BASE_URL") + "/v1"
	}
	if req.Multimodal.VLM.ModelName == "" || req.Multimodal.VLM.BaseURL == "" {
		logger.Error(ctx, "VLM configuration incomplete")
		return errors.NewBadRequestError("VLM 구성이 불완전합니다")
	}

	switch storageType {
	case "cos":
		if req.Multimodal.COS == nil || req.Multimodal.COS.SecretID == "" || req.Multimodal.COS.SecretKey == "" ||
			req.Multimodal.COS.Region == "" || req.Multimodal.COS.BucketName == "" ||
			req.Multimodal.COS.AppID == "" {
			logger.Error(ctx, "COS configuration incomplete")
			return errors.NewBadRequestError("COS 구성이 불완전합니다")
		}
	case "minio":
		if req.Multimodal.Minio == nil || req.Multimodal.Minio.BucketName == "" ||
			os.Getenv("MINIO_ACCESS_KEY_ID") == "" || os.Getenv("MINIO_SECRET_ACCESS_KEY") == "" {
			logger.Error(ctx, "MinIO configuration incomplete")
			return errors.NewBadRequestError("MinIO 구성이 불완전합니다")
		}
	}
	return nil
}

func validateRerankConfig(ctx context.Context, req *InitializationRequest) error {
	if !req.Rerank.Enabled {
		return nil
	}
	if req.Rerank.ModelName == "" || req.Rerank.BaseURL == "" {
		logger.Error(ctx, "Rerank configuration incomplete")
		return errors.NewBadRequestError("Rerank 구성이 불완전합니다")
	}
	return nil
}

func validateNodeExtractConfig(ctx context.Context, req *InitializationRequest) error {
	if !req.NodeExtract.Enabled {
		return nil
	}
	if strings.ToLower(os.Getenv("NEO4J_ENABLE")) != "true" {
		logger.Error(ctx, "Node Extractor configuration incomplete")
		return errors.NewBadRequestError("환경 변수 NEO4J_ENABLE을 올바르게 구성하십시오")
	}
	if req.NodeExtract.Text == "" || len(req.NodeExtract.Tags) == 0 {
		logger.Error(ctx, "Node Extractor configuration incomplete")
		return errors.NewBadRequestError("노드 추출기 구성이 불완전합니다")
	}
	if len(req.NodeExtract.Nodes) == 0 || len(req.NodeExtract.Relations) == 0 {
		logger.Error(ctx, "Node Extractor configuration incomplete")
		return errors.NewBadRequestError("엔티티 및 관계를 먼저 추출하십시오")
	}
	return nil
}

type modelDescriptor struct {
	modelType     types.ModelType
	name          string
	source        types.ModelSource
	description   string
	baseURL       string
	apiKey        string
	dimension     int
	interfaceType string
}

func buildModelDescriptors(req *InitializationRequest) []modelDescriptor {
	descriptors := []modelDescriptor{
		{
			modelType:   types.ModelTypeKnowledgeQA,
			name:        utils.SanitizeForLog(req.LLM.ModelName),
			source:      types.ModelSource(req.LLM.Source),
			description: "LLM Model for Knowledge QA",
			baseURL:     utils.SanitizeForLog(req.LLM.BaseURL),
			apiKey:      req.LLM.APIKey,
		},
		{
			modelType:   types.ModelTypeEmbedding,
			name:        utils.SanitizeForLog(req.Embedding.ModelName),
			source:      types.ModelSource(req.Embedding.Source),
			description: "Embedding Model",
			baseURL:     utils.SanitizeForLog(req.Embedding.BaseURL),
			apiKey:      req.Embedding.APIKey,
			dimension:   req.Embedding.Dimension,
		},
	}

	if req.Rerank.Enabled {
		descriptors = append(descriptors, modelDescriptor{
			modelType:   types.ModelTypeRerank,
			name:        utils.SanitizeForLog(req.Rerank.ModelName),
			source:      types.ModelSourceRemote,
			description: "Rerank Model",
			baseURL:     utils.SanitizeForLog(req.Rerank.BaseURL),
			apiKey:      req.Rerank.APIKey,
		})
	}

	if req.Multimodal.Enabled && req.Multimodal.VLM != nil {
		descriptors = append(descriptors, modelDescriptor{
			modelType:     types.ModelTypeVLLM,
			name:          utils.SanitizeForLog(req.Multimodal.VLM.ModelName),
			source:        types.ModelSourceRemote,
			description:   "VLM Model",
			baseURL:       utils.SanitizeForLog(req.Multimodal.VLM.BaseURL),
			apiKey:        req.Multimodal.VLM.APIKey,
			interfaceType: req.Multimodal.VLM.InterfaceType,
		})
	}

	return descriptors
}

func (h *InitializationHandler) processInitializationModels(
	ctx context.Context,
	kb *types.KnowledgeBase,
	kbIdStr string,
	req *InitializationRequest,
) ([]*types.Model, error) {
	descriptors := buildModelDescriptors(req)
	var processedModels []*types.Model

	for _, descriptor := range descriptors {
		model := descriptor.toModel()
		existingModelID := h.findExistingModelID(kb, descriptor.modelType)

		var existingModel *types.Model
		if existingModelID != "" {
			var err error
			existingModel, err = h.modelService.GetModelByID(ctx, existingModelID)
			if err != nil {
				logger.Warnf(ctx, "Failed to get existing model %s: %v, will create new one", existingModelID, err)
				existingModel = nil
			}
		}

		if existingModel != nil {
			existingModel.Name = model.Name
			existingModel.Source = model.Source
			existingModel.Description = model.Description
			existingModel.Parameters = model.Parameters
			existingModel.UpdatedAt = time.Now()

			if err := h.modelService.UpdateModel(ctx, existingModel); err != nil {
				logger.ErrorWithFields(ctx, err, map[string]interface{}{
					"model_id": model.ID,
					"kb_id":    kbIdStr,
				})
				return nil, errors.NewInternalServerError("모델 업데이트 실패: " + err.Error())
			}
			processedModels = append(processedModels, existingModel)
			continue
		}

		if err := h.modelService.CreateModel(ctx, model); err != nil {
			logger.ErrorWithFields(ctx, err, map[string]interface{}{
				"model_id": model.ID,
				"kb_id":    kbIdStr,
			})
			return nil, errors.NewInternalServerError("모델 생성 실패: " + err.Error())
		}
		processedModels = append(processedModels, model)
	}

	return processedModels, nil
}

func (descriptor modelDescriptor) toModel() *types.Model {
	model := &types.Model{
		Type:        descriptor.modelType,
		Name:        descriptor.name,
		Source:      descriptor.source,
		Description: descriptor.description,
		Parameters: types.ModelParameters{
			BaseURL:       descriptor.baseURL,
			APIKey:        descriptor.apiKey,
			InterfaceType: descriptor.interfaceType,
		},
		IsDefault: false,
		Status:    types.ModelStatusActive,
	}

	if descriptor.modelType == types.ModelTypeEmbedding {
		model.Parameters.EmbeddingParameters = types.EmbeddingParameters{
			Dimension: descriptor.dimension,
		}
	}

	return model
}

func (h *InitializationHandler) findExistingModelID(kb *types.KnowledgeBase, modelType types.ModelType) string {
	switch modelType {
	case types.ModelTypeEmbedding:
		return kb.EmbeddingModelID
	case types.ModelTypeKnowledgeQA:
		return kb.SummaryModelID
	case types.ModelTypeVLLM:
		return kb.VLMConfig.ModelID
	default:
		return ""
	}
}

func (h *InitializationHandler) applyKnowledgeBaseInitialization(
	kb *types.KnowledgeBase,
	req *InitializationRequest,
	processedModels []*types.Model,
) {
	embeddingModelID, llmModelID, vlmModelID := extractModelIDs(processedModels)

	kb.SummaryModelID = llmModelID
	kb.EmbeddingModelID = embeddingModelID

	kb.ChunkingConfig = types.ChunkingConfig{
		ChunkSize:    req.DocumentSplitting.ChunkSize,
		ChunkOverlap: req.DocumentSplitting.ChunkOverlap,
		Separators:   req.DocumentSplitting.Separators,
	}

	if req.Multimodal.Enabled {
		kb.VLMConfig = types.VLMConfig{
			Enabled: req.Multimodal.Enabled,
			ModelID: vlmModelID,
		}
		switch req.Multimodal.StorageType {
		case "cos":
			if req.Multimodal.COS != nil {
				kb.StorageConfig = types.StorageConfig{
					Provider:   req.Multimodal.StorageType,
					BucketName: req.Multimodal.COS.BucketName,
					AppID:      req.Multimodal.COS.AppID,
					PathPrefix: req.Multimodal.COS.PathPrefix,
					SecretID:   req.Multimodal.COS.SecretID,
					SecretKey:  req.Multimodal.COS.SecretKey,
					Region:     req.Multimodal.COS.Region,
				}
			}
		case "minio":
			if req.Multimodal.Minio != nil {
				kb.StorageConfig = types.StorageConfig{
					Provider:   req.Multimodal.StorageType,
					BucketName: req.Multimodal.Minio.BucketName,
					PathPrefix: req.Multimodal.Minio.PathPrefix,
					SecretID:   os.Getenv("MINIO_ACCESS_KEY_ID"),
					SecretKey:  os.Getenv("MINIO_SECRET_ACCESS_KEY"),
				}
			}
		}
	} else {
		kb.VLMConfig = types.VLMConfig{}
		kb.StorageConfig = types.StorageConfig{}
	}

	if req.NodeExtract.Enabled {
		kb.ExtractConfig = &types.ExtractConfig{
			Text:      req.NodeExtract.Text,
			Tags:      req.NodeExtract.Tags,
			Nodes:     make([]*types.GraphNode, 0),
			Relations: make([]*types.GraphRelation, 0),
		}
		for _, rnode := range req.NodeExtract.Nodes {
			node := &types.GraphNode{
				Name:       rnode.Name,
				Attributes: rnode.Attributes,
			}
			kb.ExtractConfig.Nodes = append(kb.ExtractConfig.Nodes, node)
		}
		for _, relation := range req.NodeExtract.Relations {
			kb.ExtractConfig.Relations = append(kb.ExtractConfig.Relations, &types.GraphRelation{
				Node1: relation.Node1,
				Node2: relation.Node2,
				Type:  relation.Type,
			})
		}
	}
}

func extractModelIDs(processedModels []*types.Model) (embeddingModelID, llmModelID, vlmModelID string) {
	for _, model := range processedModels {
		if model == nil {
			continue
		}
		switch model.Type {
		case types.ModelTypeEmbedding:
			embeddingModelID = model.ID
		case types.ModelTypeKnowledgeQA:
			llmModelID = model.ID
		case types.ModelTypeVLLM:
			vlmModelID = model.ID
		}
	}
	return
}

// CheckOllamaStatus godoc
// @Summary      Ollama 서비스 상태 확인
// @Description  Ollama 서비스 사용 가능 여부 확인
// @Tags         초기화
// @Accept       json
// @Produce      json
// @Success      200  {object}  map[string]interface{}  "Ollama 상태"
// @Router       /initialization/ollama/status [get]
func (h *InitializationHandler) CheckOllamaStatus(c *gin.Context) {
	ctx := c.Request.Context()

	logger.Info(ctx, "Checking Ollama service status")

	// 표시에 사용할 Ollama 기본 URL 결정
	baseURL := os.Getenv("OLLAMA_BASE_URL")
	if baseURL == "" {
		baseURL = "http://host.docker.internal:11434"
	}

	// Ollama 서비스 사용 가능 여부 확인
	err := h.ollamaService.StartService(ctx)
	if err != nil {
		logger.ErrorWithFields(ctx, err, nil)
		c.JSON(http.StatusOK, gin.H{
			"success": true,
			"data": gin.H{
				"available": false,
				"error":     err.Error(),
				"baseUrl":   baseURL,
			},
		})
		return
	}

	version, err := h.ollamaService.GetVersion(ctx)
	if err != nil {
		logger.ErrorWithFields(ctx, err, nil)
		version = "unknown"
	}

	logger.Info(ctx, "Ollama service is available")
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"available": h.ollamaService.IsAvailable(),
			"version":   version,
			"baseUrl":   baseURL,
		},
	})
}

// CheckOllamaModels godoc
// @Summary      Ollama 모델 상태 확인
// @Description  지정된 Ollama 모델 설치 여부 확인
// @Tags         초기화
// @Accept       json
// @Produce      json
// @Param        request  body      object{models=[]string}  true  "모델 이름 목록"
// @Success      200      {object}  map[string]interface{}   "모델 상태"
// @Failure      400      {object}  errors.AppError          "요청 매개변수 오류"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /initialization/ollama/models/check [post]
func (h *InitializationHandler) CheckOllamaModels(c *gin.Context) {
	ctx := c.Request.Context()

	logger.Info(ctx, "Checking Ollama models status")

	var req struct {
		Models []string `json:"models" binding:"required"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Error(ctx, "Failed to parse models check request", err)
		c.Error(errors.NewBadRequestError(err.Error()))
		return
	}

	// Ollama 서비스 사용 가능 여부 확인
	if !h.ollamaService.IsAvailable() {
		err := h.ollamaService.StartService(ctx)
		if err != nil {
			logger.ErrorWithFields(ctx, err, nil)
			c.Error(errors.NewInternalServerError("Ollama 서비스를 사용할 수 없습니다: " + err.Error()))
			return
		}
	}

	modelStatus := make(map[string]bool)

	// 각 모델 존재 여부 확인
	for _, modelName := range req.Models {
		available, err := h.ollamaService.IsModelAvailable(ctx, modelName)
		if err != nil {
			logger.ErrorWithFields(ctx, err, map[string]interface{}{
				"model_name": modelName,
			})
			modelStatus[modelName] = false
		} else {
			modelStatus[modelName] = available
		}

		logger.Infof(ctx, "Model %s availability: %v", utils.SanitizeForLog(modelName), modelStatus[modelName])
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"models": modelStatus,
		},
	})
}

// DownloadOllamaModel godoc
// @Summary      Ollama 모델 다운로드
// @Description  지정된 Ollama 모델 비동기 다운로드
// @Tags         초기화
// @Accept       json
// @Produce      json
// @Param        request  body      object{modelName=string}  true  "모델 이름"
// @Success      200      {object}  map[string]interface{}    "다운로드 작업 정보"
// @Failure      400      {object}  errors.AppError           "요청 매개변수 오류"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /initialization/ollama/models/download [post]
func (h *InitializationHandler) DownloadOllamaModel(c *gin.Context) {
	ctx := c.Request.Context()

	logger.Info(ctx, "Starting async Ollama model download")

	var req struct {
		ModelName string `json:"modelName" binding:"required"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Error(ctx, "Failed to parse model download request", err)
		c.Error(errors.NewBadRequestError(err.Error()))
		return
	}

	// Ollama 서비스 사용 가능 여부 확인
	if !h.ollamaService.IsAvailable() {
		err := h.ollamaService.StartService(ctx)
		if err != nil {
			logger.ErrorWithFields(ctx, err, nil)
			c.Error(errors.NewInternalServerError("Ollama 서비스를 사용할 수 없습니다: " + err.Error()))
			return
		}
	}

	// 모델이 이미 존재하는지 확인
	available, err := h.ollamaService.IsModelAvailable(ctx, req.ModelName)
	if err != nil {
		c.Error(errors.NewInternalServerError("모델 상태 확인 실패: " + err.Error()))
		return
	}

	if available {
		c.JSON(http.StatusOK, gin.H{
			"success": true,
			"message": "모델이 이미 존재합니다",
			"data": gin.H{
				"modelName": req.ModelName,
				"status":    "completed",
				"progress":  100.0,
			},
		})
		return
	}

	// 동일한 모델에 대한 다운로드 작업이 이미 있는지 확인
	tasksMutex.RLock()
	for _, task := range downloadTasks {
		if task.ModelName == req.ModelName && (task.Status == "pending" || task.Status == "downloading") {
			tasksMutex.RUnlock()
			c.JSON(http.StatusOK, gin.H{
				"success": true,
				"message": "모델 다운로드 작업이 이미 존재합니다",
				"data": gin.H{
					"taskId":    task.ID,
					"modelName": task.ModelName,
					"status":    task.Status,
					"progress":  task.Progress,
				},
			})
			return
		}
	}
	tasksMutex.RUnlock()

	// 다운로드 작업 생성
	taskID := uuid.New().String()
	task := &DownloadTask{
		ID:        taskID,
		ModelName: req.ModelName,
		Status:    "pending",
		Progress:  0.0,
		Message:   "다운로드 준비 중",
		StartTime: time.Now(),
	}

	tasksMutex.Lock()
	downloadTasks[taskID] = task
	tasksMutex.Unlock()

	// 비동기 다운로드 시작
	newCtx, cancel := context.WithTimeout(context.Background(), 12*time.Hour)
	go func() {
		defer cancel()
		h.downloadModelAsync(newCtx, taskID, req.ModelName)
	}()

	logger.Infof(ctx, "Created download task for model, task ID: %s", taskID)
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "모델 다운로드 작업이 생성되었습니다",
		"data": gin.H{
			"taskId":    taskID,
			"modelName": req.ModelName,
			"status":    "pending",
			"progress":  0.0,
		},
	})
}

// GetDownloadProgress godoc
// @Summary      다운로드 진행 상황 조회
// @Description  Ollama 모델 다운로드 작업의 진행 상황 조회
// @Tags         초기화
// @Accept       json
// @Produce      json
// @Param        taskId  path      string  true  "작업 ID"
// @Success      200     {object}  map[string]interface{}  "다운로드 진행 상황"
// @Failure      404     {object}  errors.AppError         "작업을 찾을 수 없음"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /initialization/ollama/download/{taskId} [get]
func (h *InitializationHandler) GetDownloadProgress(c *gin.Context) {
	taskID := c.Param("taskId")

	if taskID == "" {
		c.Error(errors.NewBadRequestError("작업 ID는 비워둘 수 없습니다"))
		return
	}

	tasksMutex.RLock()
	task, exists := downloadTasks[taskID]
	tasksMutex.RUnlock()

	if !exists {
		c.Error(errors.NewNotFoundError("다운로드 작업을 찾을 수 없습니다"))
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    task,
	})
}

// ListDownloadTasks godoc
// @Summary      다운로드 작업 목록 조회
// @Description  모든 Ollama 모델 다운로드 작업 조회
// @Tags         초기화
// @Accept       json
// @Produce      json
// @Success      200  {object}  map[string]interface{}  "작업 목록"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /initialization/ollama/download/tasks [get]
func (h *InitializationHandler) ListDownloadTasks(c *gin.Context) {
	tasksMutex.RLock()
	tasks := make([]*DownloadTask, 0, len(downloadTasks))
	for _, task := range downloadTasks {
		tasks = append(tasks, task)
	}
	tasksMutex.RUnlock()

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    tasks,
	})
}

// ListOllamaModels godoc
// @Summary      Ollama 모델 목록 조회
// @Description  설치된 Ollama 모델 목록 조회
// @Tags         초기화
// @Accept       json
// @Produce      json
// @Success      200  {object}  map[string]interface{}  "모델 목록"
// @Failure      500  {object}  errors.AppError         "서버 오류"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /initialization/ollama/models [get]
func (h *InitializationHandler) ListOllamaModels(c *gin.Context) {
	ctx := c.Request.Context()

	logger.Info(ctx, "Listing installed Ollama models")

	// 서비스 사용 가능 여부 확인
	if !h.ollamaService.IsAvailable() {
		if err := h.ollamaService.StartService(ctx); err != nil {
			logger.ErrorWithFields(ctx, err, nil)
			c.Error(errors.NewInternalServerError("Ollama 서비스를 사용할 수 없습니다: " + err.Error()))
			return
		}
	}

	// ListModelsDetailed를 사용하여 크기 등 상세 정보가 포함된 모델 목록 가져오기
	models, err := h.ollamaService.ListModelsDetailed(ctx)
	if err != nil {
		logger.ErrorWithFields(ctx, err, nil)
		c.Error(errors.NewInternalServerError("모델 목록 가져오기 실패: " + err.Error()))
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"models": models,
		},
	})
}

// downloadModelAsync 비동기 모델 다운로드
func (h *InitializationHandler) downloadModelAsync(ctx context.Context,
	taskID, modelName string,
) {
	logger.Infof(ctx, "Starting async download for model, task: %s", taskID)

	// 작업 상태를 다운로드 중으로 업데이트
	h.updateTaskStatus(taskID, "downloading", 0.0, "모델 다운로드 시작")

	// 진행 콜백과 함께 다운로드 실행
	err := h.pullModelWithProgress(ctx, modelName, func(progress float64, message string) {
		h.updateTaskStatus(taskID, "downloading", progress, message)
	})
	if err != nil {
		logger.Error(ctx, "Failed to download model", err)
		h.updateTaskStatus(taskID, "failed", 0.0, fmt.Sprintf("다운로드 실패: %v", err))
		return
	}

	// 다운로드 성공
	logger.Infof(ctx, "Model downloaded successfully, task: %s", taskID)
	h.updateTaskStatus(taskID, "completed", 100.0, "다운로드 완료")
}

// pullModelWithProgress 진행 콜백과 함께 모델 다운로드
func (h *InitializationHandler) pullModelWithProgress(ctx context.Context,
	modelName string,
	progressCallback func(float64, string),
) error {
	// 서비스 사용 가능 여부 확인
	if err := h.ollamaService.StartService(ctx); err != nil {
		logger.ErrorWithFields(ctx, err, nil)
		return err
	}

	// 모델이 이미 존재하는지 확인
	available, err := h.ollamaService.IsModelAvailable(ctx, modelName)
	if err != nil {
		logger.Error(ctx, "Failed to check model availability", err)
		return err
	}
	if available {
		progressCallback(100.0, "모델이 이미 존재합니다")
		return nil
	}

	// 다운로드 요청 생성
	pullReq := &api.PullRequest{
		Name: modelName,
	}

	// Ollama 클라이언트의 Pull 메서드 사용 (진행 콜백 포함)
	err = h.ollamaService.GetClient().Pull(ctx, pullReq, func(progress api.ProgressResponse) error {
		progressPercent := 0.0
		message := "다운로드 중"

		if progress.Total > 0 && progress.Completed > 0 {
			progressPercent = float64(progress.Completed) / float64(progress.Total) * 100
			message = fmt.Sprintf("다운로드 중: %.1f%% (%s)", progressPercent, progress.Status)
		} else if progress.Status != "" {
			message = progress.Status
		}

		// 진행 콜백 호출
		progressCallback(progressPercent, message)

		logger.Infof(ctx,
			"Download progress: %.2f%% - %s", progressPercent, message,
		)
		return nil
	})
	if err != nil {
		return fmt.Errorf("failed to pull model: %w", err)
	}

	return nil
}

// updateTaskStatus 작업 상태 업데이트
func (h *InitializationHandler) updateTaskStatus(
	taskID, status string, progress float64, message string,
) {
	tasksMutex.Lock()
	defer tasksMutex.Unlock()

	if task, exists := downloadTasks[taskID]; exists {
		task.Status = status
		task.Progress = progress
		task.Message = message

		if status == "completed" || status == "failed" {
			now := time.Now()
			task.EndTime = &now
		}
	}
}

// GetCurrentConfigByKB godoc
// @Summary      지식베이스 구성 조회
// @Description  지식베이스 ID에 따른 현재 구성 정보 조회
// @Tags         초기화
// @Accept       json
// @Produce      json
// @Param        kbId  path      string  true  "지식베이스 ID"
// @Success      200   {object}  map[string]interface{}  "구성 정보"
// @Failure      404   {object}  errors.AppError         "지식베이스를 찾을 수 없음"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /initialization/kb/{kbId}/config [get]
func (h *InitializationHandler) GetCurrentConfigByKB(c *gin.Context) {
	ctx := c.Request.Context()
	kbIdStr := utils.SanitizeForLog(c.Param("kbId"))

	logger.Info(ctx, "Getting configuration for knowledge base")

	// 지정된 지식베이스 정보 가져오기
	kb, err := h.kbService.GetKnowledgeBaseByID(ctx, kbIdStr)
	if err != nil {
		logger.Error(ctx, "Failed to get knowledge base", err)
		c.Error(errors.NewInternalServerError("지식베이스 정보 가져오기 실패: " + err.Error()))
		return
	}

	if kb == nil {
		logger.Error(ctx, "Knowledge base not found")
		c.Error(errors.NewNotFoundError("지식베이스가 존재하지 않습니다"))
		return
	}

	// 지식베이스 모델 ID에 따라 특정 모델 가져오기
	var models []*types.Model
	modelIDs := []string{
		kb.EmbeddingModelID,
		kb.SummaryModelID,
		kb.VLMConfig.ModelID,
	}

	for _, modelID := range modelIDs {
		if modelID != "" {
			model, err := h.modelService.GetModelByID(ctx, modelID)
			if err != nil {
				logger.Warn(ctx, "Failed to get model", err)
				// 모델이 없거나 가져오기 실패 시 계속 진행
				continue
			}
			if model != nil {
				models = append(models, model)
			}
		}
	}

	// 지식베이스에 파일이 있는지 확인
	knowledgeList, err := h.knowledgeService.ListPagedKnowledgeByKnowledgeBaseID(ctx,
		kbIdStr, &types.Pagination{
			Page:     1,
			PageSize: 1,
		}, "", "", "")
	hasFiles := err == nil && knowledgeList != nil && knowledgeList.Total > 0

	// 구성 응답 생성
	config := h.buildConfigResponse(ctx, models, kb, hasFiles)

	logger.Info(ctx, "Knowledge base configuration retrieved successfully")
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    config,
	})
}

// buildConfigResponse 구성 응답 데이터 생성
func (h *InitializationHandler) buildConfigResponse(ctx context.Context, models []*types.Model,
	kb *types.KnowledgeBase, hasFiles bool,
) map[string]interface{} {
	config := map[string]interface{}{
		"hasFiles": hasFiles,
	}

	// 모델 유형별 그룹화
	for _, model := range models {
		if model == nil {
			continue
		}
		// 내장 모델의 민감 정보 숨김
		baseURL := model.Parameters.BaseURL
		apiKey := model.Parameters.APIKey
		if model.IsBuiltin {
			baseURL = ""
			apiKey = ""
		}

		switch model.Type {
		case types.ModelTypeKnowledgeQA:
			config["llm"] = map[string]interface{}{
				"source":    string(model.Source),
				"modelName": model.Name,
				"baseUrl":   baseURL,
				"apiKey":    apiKey,
			}
		case types.ModelTypeEmbedding:
			config["embedding"] = map[string]interface{}{
				"source":    string(model.Source),
				"modelName": model.Name,
				"baseUrl":   baseURL,
				"apiKey":    apiKey,
				"dimension": model.Parameters.EmbeddingParameters.Dimension,
			}
		case types.ModelTypeRerank:
			config["rerank"] = map[string]interface{}{
				"enabled":   true,
				"modelName": model.Name,
				"baseUrl":   baseURL,
				"apiKey":    apiKey,
			}
		case types.ModelTypeVLLM:
			if config["multimodal"] == nil {
				config["multimodal"] = map[string]interface{}{
					"enabled": true,
				}
			}
			multimodal := config["multimodal"].(map[string]interface{})
			multimodal["vlm"] = map[string]interface{}{
				"modelName":     model.Name,
				"baseUrl":       baseURL,
				"apiKey":        apiKey,
				"interfaceType": model.Parameters.InterfaceType,
				"modelId":       model.ID,
			}
		}
	}

	// 멀티모달 활성화 여부 확인: VLM 모델 ID 또는 저장소 구성이 있는 경우
	hasMultimodal := (kb.VLMConfig.IsEnabled() ||
		kb.StorageConfig.SecretID != "" || kb.StorageConfig.BucketName != "")
	if config["multimodal"] == nil {
		config["multimodal"] = map[string]interface{}{
			"enabled": hasMultimodal,
		}
	} else {
		// 이미 multimodal이 설정된 경우 enabled 상태 업데이트
		config["multimodal"].(map[string]interface{})["enabled"] = hasMultimodal
	}

	// Rerank 모델이 없는 경우 disabled로 설정
	if config["rerank"] == nil {
		config["rerank"] = map[string]interface{}{
			"enabled":   false,
			"modelName": "",
			"baseUrl":   "",
			"apiKey":    "",
		}
	}

	// 지식베이스 문서 분할 구성 추가
	if kb != nil {
		config["documentSplitting"] = map[string]interface{}{
			"chunkSize":    kb.ChunkingConfig.ChunkSize,
			"chunkOverlap": kb.ChunkingConfig.ChunkOverlap,
			"separators":   kb.ChunkingConfig.Separators,
		}

		// 멀티모달 COS 구성 정보 추가
		if kb.StorageConfig.SecretID != "" {
			if config["multimodal"] == nil {
				config["multimodal"] = map[string]interface{}{
					"enabled": true,
				}
			}
			multimodal := config["multimodal"].(map[string]interface{})
			multimodal["storageType"] = kb.StorageConfig.Provider
			switch kb.StorageConfig.Provider {
			case "cos":
				multimodal["cos"] = map[string]interface{}{
					"secretId":   kb.StorageConfig.SecretID,
					"secretKey":  kb.StorageConfig.SecretKey,
					"region":     kb.StorageConfig.Region,
					"bucketName": kb.StorageConfig.BucketName,
					"appId":      kb.StorageConfig.AppID,
					"pathPrefix": kb.StorageConfig.PathPrefix,
				}
			case "minio":
				multimodal["minio"] = map[string]interface{}{
					"bucketName": kb.StorageConfig.BucketName,
					"pathPrefix": kb.StorageConfig.PathPrefix,
				}
			}
		}
	}

	if kb.ExtractConfig != nil {
		config["nodeExtract"] = map[string]interface{}{
			"enabled":   kb.ExtractConfig.Enabled,
			"text":      kb.ExtractConfig.Text,
			"tags":      kb.ExtractConfig.Tags,
			"nodes":     kb.ExtractConfig.Nodes,
			"relations": kb.ExtractConfig.Relations,
		}
	} else {
		config["nodeExtract"] = map[string]interface{}{
			"enabled": false,
		}
	}

	return config
}

// RemoteModelCheckRequest 원격 모델 확인 요청 구조
type RemoteModelCheckRequest struct {
	ModelName string `json:"modelName" binding:"required"`
	BaseURL   string `json:"baseUrl"   binding:"required"`
	APIKey    string `json:"apiKey"`
}

// CheckRemoteModel godoc
// @Summary      원격 모델 확인
// @Description  원격 API 모델 연결 상태 확인
// @Tags         초기화
// @Accept       json
// @Produce      json
// @Param        request  body      RemoteModelCheckRequest  true  "모델 확인 요청"
// @Success      200      {object}  map[string]interface{}   "확인 결과"
// @Failure      400      {object}  errors.AppError          "요청 매개변수 오류"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /initialization/models/remote/check [post]
func (h *InitializationHandler) CheckRemoteModel(c *gin.Context) {
	ctx := c.Request.Context()

	logger.Info(ctx, "Checking remote model connection")

	var req RemoteModelCheckRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Error(ctx, "Failed to parse remote model check request", err)
		c.Error(errors.NewBadRequestError(err.Error()))
		return
	}

	// 요청 매개변수 검증
	if req.ModelName == "" || req.BaseURL == "" {
		logger.Error(ctx, "Model name and base URL are required")
		c.Error(errors.NewBadRequestError("모델 이름과 Base URL은 필수입니다"))
		return
	}

	// 테스트용 모델 구성 생성
	modelConfig := &types.Model{
		Name:   req.ModelName,
		Source: "remote",
		Parameters: types.ModelParameters{
			BaseURL: req.BaseURL,
			APIKey:  req.APIKey,
		},
		Type: "llm", // 기본 유형, 실제 확인 시 구체적인 유형 구분 안 함
	}

	// 원격 모델 연결 확인
	available, message := h.checkRemoteModelConnection(ctx, modelConfig)

	logger.Infof(ctx, "Remote model check completed, available: %v, message: %s", available, message)

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"available": available,
			"message":   message,
		},
	})
}

// TestEmbeddingModel godoc
// @Summary      Embedding 모델 테스트
// @Description  Embedding 인터페이스 사용 가능 여부 테스트 및 벡터 차원 반환
// @Tags         초기화
// @Accept       json
// @Produce      json
// @Param        request  body      object  true  "Embedding 테스트 요청"
// @Success      200      {object}  map[string]interface{}  "테스트 결과"
// @Failure      400      {object}  errors.AppError         "요청 매개변수 오류"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /initialization/models/embedding/test [post]
func (h *InitializationHandler) TestEmbeddingModel(c *gin.Context) {
	ctx := c.Request.Context()

	logger.Info(ctx, "Testing embedding model connectivity and functionality")

	var req struct {
		Source    string `json:"source" binding:"required"`
		ModelName string `json:"modelName" binding:"required"`
		BaseURL   string `json:"baseUrl"`
		APIKey    string `json:"apiKey"`
		Dimension int    `json:"dimension"`
		Provider  string `json:"provider"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Error(ctx, "Failed to parse embedding test request", err)
		c.Error(errors.NewBadRequestError(err.Error()))
		return
	}

	// 알리클라우드 멀티모달 embedding 모델인지 확인 (미지원)
	if strings.ToLower(req.Provider) == "aliyun" {
		modelNameLower := strings.ToLower(req.ModelName)
		if strings.Contains(modelNameLower, "vision") || strings.Contains(modelNameLower, "multimodal") {
			logger.Infof(ctx, "Aliyun multimodal embedding model not supported: %s", req.ModelName)
			c.JSON(http.StatusOK, gin.H{
				"success": true,
				"data": gin.H{
					"available": false,
					"message":   "알리클라우드 멀티모달 Embedding 모델은 현재 지원되지 않습니다. 텍스트 전용 Embedding 모델(예: text-embedding-v4)을 사용하세요.",
					"dimension": 0,
				},
			})
			return
		}
	}

	// embedder 구성 생성
	cfg := embedding.Config{
		Source:               types.ModelSource(strings.ToLower(req.Source)),
		BaseURL:              req.BaseURL,
		ModelName:            req.ModelName,
		APIKey:               req.APIKey,
		TruncatePromptTokens: 256,
		Dimensions:           req.Dimension,
		ModelID:              "",
		Provider:             req.Provider,
	}

	emb, err := embedding.NewEmbedder(cfg)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{"model": utils.SanitizeForLog(req.ModelName)})
		c.JSON(http.StatusOK, gin.H{
			"success": true,
			"data":    gin.H{`available`: false, `message`: fmt.Sprintf("Embedder 생성 실패: %v", err), `dimension`: 0},
		})
		return
	}

	// 최소한의 embedding 호출 실행
	sample := "hello"
	vec, err := emb.Embed(ctx, sample)
	if err != nil {
		logger.Error(ctx, "Failed to create embedder", err)
		c.JSON(http.StatusOK, gin.H{
			"success": true,
			"data":    gin.H{`available`: false, `message`: fmt.Sprintf("Embedding 호출 실패: %v", err), `dimension`: 0},
		})
		return
	}

	logger.Infof(ctx, "Embedding test succeeded, dimension: %d", len(vec))
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    gin.H{`available`: true, `message`: fmt.Sprintf("테스트 성공, 벡터 차원=%d", len(vec)), `dimension`: len(vec)},
	})
}

// checkRemoteModelConnection 원격 모델 연결 확인 내부 메서드
func (h *InitializationHandler) checkRemoteModelConnection(ctx context.Context,
	model *types.Model,
) (bool, string) {
	// 연결 확인을 위해 models/chat 사용
	// 채팅 구성 생성
	chatConfig := &chat.ChatConfig{
		Source:    types.ModelSourceRemote,
		BaseURL:   model.Parameters.BaseURL,
		ModelName: model.Name,
		APIKey:    model.Parameters.APIKey,
		ModelID:   model.Name,
	}

	// 채팅 인스턴스 생성
	chatInstance, err := chat.NewChat(chatConfig)
	if err != nil {
		return false, fmt.Sprintf("채팅 인스턴스 생성 실패: %v", err)
	}

	// 테스트 메시지 생성
	testMessages := []chat.Message{
		{
			Role:    "user",
			Content: "test",
		},
	}

	// 테스트 옵션 생성
	testOptions := &chat.ChatOptions{
		MaxTokens: 1,
		Thinking:  &[]bool{false}[0], // dashscope.aliyuncs qwen3-32b 용
	}

	// 채팅 인스턴스를 사용하여 테스트
	_, err = chatInstance.Chat(ctx, testMessages, testOptions)
	if err != nil {
		// 오류 유형에 따라 다른 메시지 반환
		if strings.Contains(err.Error(), "401") || strings.Contains(err.Error(), "unauthorized") {
			return false, "인증 실패, API Key를 확인하세요"
		} else if strings.Contains(err.Error(), "403") || strings.Contains(err.Error(), "forbidden") {
			return false, "권한 부족, API Key 권한을 확인하세요: " + err.Error()
		} else if strings.Contains(err.Error(), "404") || strings.Contains(err.Error(), "not found") {
			return false, "API 엔드포인트를 찾을 수 없음, Base URL을 확인하세요"
		} else if strings.Contains(err.Error(), "timeout") {
			return false, "연결 시간 초과, 네트워크 연결을 확인하세요"
		} else {
			return false, fmt.Sprintf("연결 실패: %v", err)
		}
	}

	// 연결 성공, 모델 사용 가능
	return true, "연결 정상, 모델 사용 가능"
}

// checkRerankModelConnection Rerank 모델 연결 및 기능 확인 내부 메서드
func (h *InitializationHandler) checkRerankModelConnection(ctx context.Context,
	modelName, baseURL, apiKey string,
) (bool, string) {
	// Reranker 구성 생성
	config := &rerank.RerankerConfig{
		APIKey:    apiKey,
		BaseURL:   baseURL,
		ModelName: modelName,
		Source:    types.ModelSourceRemote, // 기본값, URL에 따라 실제 판단
	}

	// Reranker 인스턴스 생성
	reranker, err := rerank.NewReranker(config)
	if err != nil {
		return false, fmt.Sprintf("Reranker 생성 실패: %v", err)
	}

	// 간소화된 테스트 데이터
	testQuery := "ping"
	testDocuments := []string{
		"pong",
	}

	// Reranker를 사용하여 테스트
	results, err := reranker.Rerank(ctx, testQuery, testDocuments)
	if err != nil {
		return false, fmt.Sprintf("재순위 테스트 실패: %v", err)
	}

	// 결과 확인
	if len(results) > 0 {
		return true, fmt.Sprintf("재순위 기능 정상, %d개 결과 반환", len(results))
	} else {
		return false, "재순위 인터페이스 연결 성공했으나 결과를 반환하지 않음"
	}
}

// CheckRerankModel godoc
// @Summary      Rerank 모델 확인
// @Description  Rerank 모델 연결 및 기능 정상 여부 확인
// @Tags         초기화
// @Accept       json
// @Produce      json
// @Param        request  body      object  true  "Rerank 확인 요청"
// @Success      200      {object}  map[string]interface{}  "확인 결과"
// @Failure      400      {object}  errors.AppError         "요청 매개변수 오류"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /initialization/models/rerank/check [post]
func (h *InitializationHandler) CheckRerankModel(c *gin.Context) {
	ctx := c.Request.Context()

	logger.Info(ctx, "Checking rerank model connection and functionality")

	var req struct {
		ModelName string `json:"modelName" binding:"required"`
		BaseURL   string `json:"baseUrl" binding:"required"`
		APIKey    string `json:"apiKey"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Error(ctx, "Failed to parse rerank model check request", err)
		c.Error(errors.NewBadRequestError(err.Error()))
		return
	}

	// 요청 매개변수 검증
	if req.ModelName == "" || req.BaseURL == "" {
		logger.Error(ctx, "Model name and base URL are required")
		c.Error(errors.NewBadRequestError("모델 이름과 Base URL은 필수입니다"))
		return
	}

	// Rerank 모델 연결 및 기능 확인
	available, message := h.checkRerankModelConnection(
		ctx, req.ModelName, req.BaseURL, req.APIKey,
	)

	logger.Infof(ctx, "Rerank model check completed, available: %v, message: %s", available, message)

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"available": available,
			"message":   message,
		},
	})
}

// 폼 데이터 파싱을 위한 구조체
type testMultimodalForm struct {
	VLMModel         string `form:"vlm_model"`
	VLMBaseURL       string `form:"vlm_base_url"`
	VLMAPIKey        string `form:"vlm_api_key"`
	VLMInterfaceType string `form:"vlm_interface_type"`

	StorageType string `form:"storage_type"`

	// COS 구성
	COSSecretID   string `form:"cos_secret_id"`
	COSSecretKey  string `form:"cos_secret_key"`
	COSRegion     string `form:"cos_region"`
	COSBucketName string `form:"cos_bucket_name"`
	COSAppID      string `form:"cos_app_id"`
	COSPathPrefix string `form:"cos_path_prefix"`

	// MinIO 구성 (storage_type이 minio인 경우)
	MinioBucketName string `form:"minio_bucket_name"`
	MinioPathPrefix string `form:"minio_path_prefix"`

	// 문서 분할 구성 (타입 바인딩 오류 방지를 위해 문자열로 파싱)
	ChunkSize     string `form:"chunk_size"`
	ChunkOverlap  string `form:"chunk_overlap"`
	SeparatorsRaw string `form:"separators"`
}

// TestMultimodalFunction godoc
// @Summary      멀티모달 기능 테스트
// @Description  이미지 업로드 및 멀티모달 처리 기능 테스트
// @Tags         초기화
// @Accept       multipart/form-data
// @Produce      json
// @Param        image             formData  file    true   "테스트 이미지"
// @Param        vlm_model         formData  string  true   "VLM 모델 이름"
// @Param        vlm_base_url      formData  string  true   "VLM Base URL"
// @Param        vlm_api_key       formData  string  false  "VLM API Key"
// @Param        vlm_interface_type formData string  false  "VLM 인터페이스 유형"
// @Param        storage_type      formData  string  true   "저장소 유형(cos/minio)"
// @Success      200               {object}  map[string]interface{}  "테스트 결과"
// @Failure      400               {object}  errors.AppError         "요청 매개변수 오류"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /initialization/multimodal/test [post]
func (h *InitializationHandler) TestMultimodalFunction(c *gin.Context) {
	ctx := c.Request.Context()

	logger.Info(ctx, "Testing multimodal functionality")

	var req testMultimodalForm
	if err := c.ShouldBind(&req); err != nil {
		logger.Error(ctx, "Failed to parse form data", err)
		c.Error(errors.NewBadRequestError("폼 데이터 파싱 실패"))
		return
	}
	// ollama인 경우 base url 자동 연결
	if req.VLMInterfaceType == "ollama" {
		req.VLMBaseURL = os.Getenv("OLLAMA_BASE_URL") + "/v1"
	}

	req.StorageType = strings.ToLower(req.StorageType)

	if req.VLMModel == "" || req.VLMBaseURL == "" {
		logger.Error(ctx, "VLM model name and base URL are required")
		c.Error(errors.NewBadRequestError("VLM 모델 이름과 Base URL은 필수입니다"))
		return
	}
	switch req.StorageType {
	case "cos":
		// 필수: SecretID/SecretKey/Region/BucketName/AppID; PathPrefix는 선택 사항
		if req.COSSecretID == "" || req.COSSecretKey == "" ||
			req.COSRegion == "" || req.COSBucketName == "" ||
			req.COSAppID == "" {
			logger.Error(ctx, "COS configuration is required")
			c.Error(errors.NewBadRequestError("COS 구성 정보가 비어있을 수 없습니다"))
			return
		}
	case "minio":
		if req.MinioBucketName == "" {
			logger.Error(ctx, "MinIO configuration is required")
			c.Error(errors.NewBadRequestError("MinIO 구성 정보가 비어있을 수 없습니다"))
			return
		}
	default:
		logger.Error(ctx, "Invalid storage type")
		c.Error(errors.NewBadRequestError("유효하지 않은 저장소 유형"))
		return
	}

	// 업로드된 이미지 파일 가져오기
	file, header, err := c.Request.FormFile("image")
	if err != nil {
		logger.Error(ctx, "Failed to get uploaded image", err)
		c.Error(errors.NewBadRequestError("업로드된 이미지 가져오기 실패"))
		return
	}
	defer file.Close()

	// 파일 유형 검증
	if !strings.HasPrefix(header.Header.Get("Content-Type"), "image/") {
		logger.Error(ctx, "Invalid file type, only images are allowed")
		c.Error(errors.NewBadRequestError("이미지 파일만 업로드할 수 있습니다"))
		return
	}

	// 파일 크기 검증 (기본 50MB, MAX_FILE_SIZE_MB로 설정 가능)
	maxSize := utils.GetMaxFileSize()
	if header.Size > maxSize {
		logger.Error(ctx, "File size too large")
		c.Error(errors.NewBadRequestError(fmt.Sprintf("이미지 파일 크기는 %dMB를 초과할 수 없습니다", utils.GetMaxFileSizeMB())))
		return
	}
	logger.Infof(ctx, "Processing image: %s", utils.SanitizeForLog(header.Filename))

	// 문서 분할 구성 파싱
	chunkSizeInt32, err := strconv.ParseInt(req.ChunkSize, 10, 32)
	if err != nil {
		logger.Error(ctx, "Failed to parse chunk size", err)
		c.Error(errors.NewBadRequestError("Failed to parse chunk size"))
		return
	}
	chunkSize := int32(chunkSizeInt32)
	if chunkSize < 100 || chunkSize > 10000 {
		chunkSize = 1000
	}

	chunkOverlapInt32, err := strconv.ParseInt(req.ChunkOverlap, 10, 32)
	if err != nil {
		logger.Error(ctx, "Failed to parse chunk overlap", err)
		c.Error(errors.NewBadRequestError("Failed to parse chunk overlap"))
		return
	}
	chunkOverlap := int32(chunkOverlapInt32)
	if chunkOverlap < 0 || chunkOverlap >= chunkSize {
		chunkOverlap = 200
	}

	var separators []string
	if req.SeparatorsRaw != "" {
		if err := json.Unmarshal([]byte(req.SeparatorsRaw), &separators); err != nil {
			separators = []string{"\n\n", "\n", "。", "！", "？", ";", "；"}
		}
	} else {
		separators = []string{"\n\n", "\n", "。", "！", "？", ";", "；"}
	}

	// 이미지 파일 내용 읽기
	imageContent, err := io.ReadAll(file)
	if err != nil {
		logger.Error(ctx, "Failed to read image file", err)
		c.Error(errors.NewBadRequestError("이미지 파일 읽기 실패"))
		return
	}

	// 멀티모달 테스트 호출
	startTime := time.Now()
	result, err := h.testMultimodalWithDocReader(
		ctx,
		imageContent, header.Filename,
		chunkSize, chunkOverlap, separators, &req,
	)
	processingTime := time.Since(startTime).Milliseconds()

	if err != nil {
		logger.Error(ctx, "Failed to test multimodal", err)
		c.JSON(http.StatusOK, gin.H{
			"success": true,
			"data": gin.H{
				"success":         false,
				"message":         err.Error(),
				"processing_time": processingTime,
			},
		})
		return
	}

	logger.Infof(ctx, "Multimodal test completed successfully in %dms", processingTime)

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"success":         true,
			"caption":         result["caption"],
			"ocr":             result["ocr"],
			"processing_time": processingTime,
		},
	})
}

// testMultimodalWithDocReader docreader 서비스를 호출하여 멀티모달 처리
func (h *InitializationHandler) testMultimodalWithDocReader(
	ctx context.Context,
	imageContent []byte, filename string,
	chunkSize, chunkOverlap int32, separators []string,
	req *testMultimodalForm,
) (map[string]string, error) {
	// 파일 확장자 가져오기
	fileExt := ""
	if idx := strings.LastIndex(filename, "."); idx != -1 {
		fileExt = strings.ToLower(filename[idx+1:])
	}

	// docreader 서비스 구성 확인
	if h.docReaderClient == nil {
		return nil, fmt.Errorf("DocReader service not configured")
	}

	// 요청 생성
	request := &proto.ReadFromFileRequest{
		FileContent: imageContent,
		FileName:    filename,
		FileType:    fileExt,
		ReadConfig: &proto.ReadConfig{
			ChunkSize:        chunkSize,
			ChunkOverlap:     chunkOverlap,
			Separators:       separators,
			EnableMultimodal: true, // 멀티모달 처리 활성화
			VlmConfig: &proto.VLMConfig{
				ModelName:     req.VLMModel,
				BaseUrl:       req.VLMBaseURL,
				ApiKey:        req.VLMAPIKey,
				InterfaceType: req.VLMInterfaceType,
			},
		},
		RequestId: ctx.Value(types.RequestIDContextKey).(string),
	}

	// 저장소 구성 설정 (공통)
	switch strings.ToLower(req.StorageType) {
	case "cos":
		request.ReadConfig.StorageConfig = &proto.StorageConfig{
			Provider:        proto.StorageProvider_COS,
			Region:          req.COSRegion,
			BucketName:      req.COSBucketName,
			AccessKeyId:     req.COSSecretID,
			SecretAccessKey: req.COSSecretKey,
			AppId:           req.COSAppID,
			PathPrefix:      req.COSPathPrefix,
		}
	case "minio":
		request.ReadConfig.StorageConfig = &proto.StorageConfig{
			Provider:        proto.StorageProvider_MINIO,
			BucketName:      req.MinioBucketName,
			PathPrefix:      req.MinioPathPrefix,
			AccessKeyId:     os.Getenv("MINIO_ACCESS_KEY_ID"),
			SecretAccessKey: os.Getenv("MINIO_SECRET_ACCESS_KEY"),
		}
	}

	// docreader 서비스 호출
	response, err := h.docReaderClient.ReadFromFile(ctx, request)
	if err != nil {
		return nil, fmt.Errorf("DocReader 서비스 호출 실패: %v", err)
	}

	if response.Error != "" {
		return nil, fmt.Errorf("DocReader 서비스 오류 반환: %s", response.Error)
	}

	// 응답 처리, 캡션 및 OCR 정보 추출
	result := make(map[string]string)
	var allCaptions, allOCRTexts []string

	for _, chunk := range response.Chunks {
		if len(chunk.Images) > 0 {
			for _, image := range chunk.Images {
				if image.Caption != "" {
					allCaptions = append(allCaptions, image.Caption)
				}
				if image.OcrText != "" {
					allOCRTexts = append(allOCRTexts, image.OcrText)
				}
			}
		}
	}

	// 모든 캡션 및 OCR 결과 병합
	result["caption"] = strings.Join(allCaptions, "; ")
	result["ocr"] = strings.Join(allOCRTexts, "; ")

	return result, nil
}

// TextRelationExtractionRequest 텍스트 관계 추출 요청 구조
type TextRelationExtractionRequest struct {
	Text      string    `json:"text"      binding:"required"`
	Tags      []string  `json:"tags"      binding:"required"`
	LLMConfig LLMConfig `json:"llm_config"`
}

type LLMConfig struct {
	Source    string `json:"source"`
	ModelName string `json:"model_name"`
	BaseUrl   string `json:"base_url"`
	ApiKey    string `json:"api_key"`
}

// TextRelationExtractionResponse 텍스트 관계 추출 응답 구조
type TextRelationExtractionResponse struct {
	Nodes     []*types.GraphNode     `json:"nodes"`
	Relations []*types.GraphRelation `json:"relations"`
}

// ExtractTextRelations godoc
// @Summary      텍스트 관계 추출
// @Description  텍스트에서 엔티티 및 관계 추출
// @Tags         초기화
// @Accept       json
// @Produce      json
// @Param        request  body      TextRelationExtractionRequest  true  "추출 요청"
// @Success      200      {object}  map[string]interface{}         "추출 결과"
// @Failure      400      {object}  errors.AppError                "요청 매개변수 오류"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /initialization/extract/relations [post]
func (h *InitializationHandler) ExtractTextRelations(c *gin.Context) {
	ctx := c.Request.Context()

	var req TextRelationExtractionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Error(ctx, "텍스트 관계 추출 요청 매개변수 오류")
		c.Error(errors.NewBadRequestError("텍스트 관계 추출 요청 매개변수 오류"))
		return
	}

	// 텍스트 내용 검증
	if len(req.Text) == 0 {
		c.Error(errors.NewBadRequestError("텍스트 내용은 비워둘 수 없습니다"))
		return
	}

	if len(req.Text) > 5000 {
		c.Error(errors.NewBadRequestError("텍스트 내용은 5000자를 초과할 수 없습니다"))
		return
	}

	// 태그 검증
	if len(req.Tags) == 0 {
		c.Error(errors.NewBadRequestError("최소 하나의 관계 태그를 선택해야 합니다"))
		return
	}

	// 모델 서비스를 호출하여 텍스트 관계 추출
	result, err := h.extractRelationsFromText(ctx, req.Text, req.Tags, req.LLMConfig)
	if err != nil {
		logger.Error(ctx, "텍스트 관계 추출 실패", err)
		c.Error(errors.NewInternalServerError("텍스트 관계 추출 실패: " + err.Error()))
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    result,
	})
}

// extractRelationsFromText 텍스트에서 관계 추출
func (h *InitializationHandler) extractRelationsFromText(
	ctx context.Context,
	text string,
	tags []string,
	llm LLMConfig,
) (*TextRelationExtractionResponse, error) {
	chatModel, err := chat.NewChat(&chat.ChatConfig{
		ModelID:   "initialization",
		APIKey:    llm.ApiKey,
		BaseURL:   llm.BaseUrl,
		ModelName: llm.ModelName,
		Source:    types.ModelSource(llm.Source),
	})
	if err != nil {
		logger.Error(ctx, "모델 서비스 초기화 실패", err)
		return nil, err
	}

	template := &types.PromptTemplateStructured{
		Description: h.config.ExtractManager.ExtractGraph.Description,
		Tags:        tags,
		Examples:    h.config.ExtractManager.ExtractGraph.Examples,
	}

	extractor := chatpipline.NewExtractor(chatModel, template)
	graph, err := extractor.Extract(ctx, text)
	if err != nil {
		logger.Error(ctx, "텍스트 관계 추출 실패", err)
		return nil, err
	}
	extractor.RemoveUnknownRelation(ctx, graph)

	result := &TextRelationExtractionResponse{
		Nodes:     graph.Node,
		Relations: graph.Relation,
	}

	return result, nil
}

// FabriTextRequest 예제 텍스트 생성을 위한 요청
type FabriTextRequest struct {
	Tags      []string  `json:"tags"`
	LLMConfig LLMConfig `json:"llm_config"`
}

// FabriTextResponse 예제 텍스트 생성 응답
type FabriTextResponse struct {
	Text string `json:"text"`
}

// FabriText godoc
// @Summary      예제 텍스트 생성
// @Description  태그에 따라 예제 텍스트 생성
// @Tags         초기화
// @Accept       json
// @Produce      json
// @Param        request  body      FabriTextRequest  true  "생성 요청"
// @Success      200      {object}  map[string]interface{}  "생성된 텍스트"
// @Failure      400      {object}  errors.AppError         "요청 매개변수 오류"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /initialization/fabri/text [post]
func (h *InitializationHandler) FabriText(c *gin.Context) {
	ctx := c.Request.Context()

	var req FabriTextRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Error(ctx, "예제 텍스트 생성 요청 매개변수 오류")
		c.Error(errors.NewBadRequestError("예제 텍스트 생성 요청 매개변수 오류"))
		return
	}

	result, err := h.fabriText(ctx, req.Tags, req.LLMConfig)
	if err != nil {
		logger.Error(ctx, "예제 텍스트 생성 실패", err)
		c.Error(errors.NewInternalServerError("예제 텍스트 생성 실패: " + err.Error()))
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    FabriTextResponse{Text: result},
	})
}

// fabriText 예제 텍스트 생성
func (h *InitializationHandler) fabriText(ctx context.Context, tags []string, llm LLMConfig) (string, error) {
	chatModel, err := chat.NewChat(&chat.ChatConfig{
		ModelID:   "initialization",
		APIKey:    llm.ApiKey,
		BaseURL:   llm.BaseUrl,
		ModelName: llm.ModelName,
		Source:    types.ModelSource(llm.Source),
	})
	if err != nil {
		logger.Error(ctx, "모델 서비스 초기화 실패", err)
		return "", err
	}

	content := h.config.ExtractManager.FabriText.WithNoTag
	if len(tags) > 0 {
		tagStr, _ := json.Marshal(tags)
		content = fmt.Sprintf(h.config.ExtractManager.FabriText.WithTag, string(tagStr))
	}

	think := false
	result, err := chatModel.Chat(ctx, []chat.Message{
		{Role: "user", Content: content},
	}, &chat.ChatOptions{
		Temperature: 0.3,
		MaxTokens:   4096,
		Thinking:    &think,
	})
	if err != nil {
		logger.Error(ctx, "예제 텍스트 생성 실패", err)
		return "", err
	}
	return result.Content, nil
}

// FabriTagRequest 태그 생성을 위한 요청
type FabriTagRequest struct {
	LLMConfig LLMConfig `json:"llm_config"`
}

// FabriTagResponse 태그 생성 응답
type FabriTagResponse struct {
	Tags []string `json:"tags"`
}

var tagOptions = []string{
	"내용", "문화", "인물", "사건", "시간", "장소", "작품", "작가", "관계", "속성",
}

// FabriTag godoc
// @Summary      랜덤 태그 생성
// @Description  태그 집합 랜덤 생성
// @Tags         초기화
// @Accept       json
// @Produce      json
// @Success      200  {object}  map[string]interface{}  "생성된 태그"
// @Router       /initialization/fabri/tag [get]
func (h *InitializationHandler) FabriTag(c *gin.Context) {
	tagRandom := RandomSelect(tagOptions, rand.Intn(len(tagOptions)-1)+1)
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    FabriTagResponse{Tags: tagRandom},
	})
}

// RandomSelect 랜덤 문자열 선택
func RandomSelect(strs []string, n int) []string {
	if n <= 0 {
		return []string{}
	}
	result := make([]string, len(strs))
	copy(result, strs)
	rand.Shuffle(len(result), func(i, j int) {
		result[i], result[j] = result[j], result[i]
	})

	if n > len(strs) {
		n = len(strs)
	}
	return result[:n]
}
