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

// DownloadTask 下载任务信息
type DownloadTask struct {
	ID        string     `json:"id"`
	ModelName string     `json:"modelName"`
	Status    string     `json:"status"` // pending, downloading, completed, failed
	Progress  float64    `json:"progress"`
	Message   string     `json:"message"`
	StartTime time.Time  `json:"startTime"`
	EndTime   *time.Time `json:"endTime,omitempty"`
}

// 全局下载任务管理器
var (
	downloadTasks = make(map[string]*DownloadTask)
	tasksMutex    sync.RWMutex
)

// InitializationHandler 初始化处理器
type InitializationHandler struct {
	config           *config.Config
	tenantService    interfaces.TenantService
	modelService     interfaces.ModelService
	kbService        interfaces.KnowledgeBaseService
	kbRepository     interfaces.KnowledgeBaseRepository
	knowledgeService interfaces.KnowledgeService
	ollamaService    *ollama.OllamaService
	docReaderClient  *client.Client
	pooler           embedding.EmbedderPooler
}

// NewInitializationHandler 创建初始化处理器
func NewInitializationHandler(
	config *config.Config,
	tenantService interfaces.TenantService,
	modelService interfaces.ModelService,
	kbService interfaces.KnowledgeBaseService,
	kbRepository interfaces.KnowledgeBaseRepository,
	knowledgeService interfaces.KnowledgeService,
	ollamaService *ollama.OllamaService,
	docReaderClient *client.Client,
	pooler embedding.EmbedderPooler,
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
		pooler:           pooler,
	}
}

// KBModelConfigRequest 知识库模型配置请求（简化版，只传模型ID）
type KBModelConfigRequest struct {
	LLMModelID       string           `json:"llmModelId"       binding:"required"`
	EmbeddingModelID string           `json:"embeddingModelId" binding:"required"`
	VLMConfig        *types.VLMConfig `json:"vlm_config"`

	// 文档分块配置
	DocumentSplitting struct {
		ChunkSize    int      `json:"chunkSize"`
		ChunkOverlap int      `json:"chunkOverlap"`
		Separators   []string `json:"separators"`
	} `json:"documentSplitting"`

	// 多模态配置
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

	// 知识图谱配置
	NodeExtract struct {
		Enabled   bool                  `json:"enabled"`
		Text      string                `json:"text"`
		Tags      []string              `json:"tags"`
		Nodes     []types.GraphNode     `json:"nodes"`
		Relations []types.GraphRelation `json:"relations"`
	} `json:"nodeExtract"`

	// 问题生成配置
	QuestionGeneration struct {
		Enabled       bool `json:"enabled"`
		QuestionCount int  `json:"questionCount"`
	} `json:"questionGeneration"`
}

// InitializationRequest 初始化请求结构
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
		Dimension int    `json:"dimension"` // 添加embedding维度字段
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
// @Summary      更新知识库配置
// @Description  根据知识库ID更新模型和分块配置
// @Tags         初始化
// @Accept       json
// @Produce      json
// @Param        kbId     path      string               true  "知识库ID"
// @Param        request  body      KBModelConfigRequest true  "配置请求"
// @Success      200      {object}  map[string]interface{}  "更新成功"
// @Failure      400      {object}  errors.AppError         "请求参数错误"
// @Failure      404      {object}  errors.AppError         "知识库不存在"
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

	// 获取知识库信息
	kb, err := h.kbService.GetKnowledgeBaseByID(ctx, kbIdStr)
	if err != nil || kb == nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{"kbId": utils.SanitizeForLog(kbIdStr)})
		c.Error(errors.NewNotFoundError("知识库不存在"))
		return
	}

	// 检查Embedding模型是否可以修改
	if kb.EmbeddingModelID != "" && kb.EmbeddingModelID != req.EmbeddingModelID {
		// 检查是否已有文件
		knowledgeList, err := h.knowledgeService.ListPagedKnowledgeByKnowledgeBaseID(ctx,
			kbIdStr, &types.Pagination{
				Page:     1,
				PageSize: 1,
			}, "", "", "")
		if err == nil && knowledgeList != nil && knowledgeList.Total > 0 {
			logger.Error(ctx, "Cannot change embedding model when files exist")
			c.Error(errors.NewBadRequestError("知识库中已有文件，无法修改Embedding模型"))
			return
		}
	}

	// 从数据库获取模型详情并验证
	llmModel, err := h.modelService.GetModelByID(ctx, req.LLMModelID)
	if err != nil || llmModel == nil {
		logger.Error(ctx, "LLM model not found")
		c.Error(errors.NewBadRequestError("LLM模型不存在"))
		return
	}

	embeddingModel, err := h.modelService.GetModelByID(ctx, req.EmbeddingModelID)
	if err != nil || embeddingModel == nil {
		logger.Error(ctx, "Embedding model not found")
		c.Error(errors.NewBadRequestError("Embedding模型不存在"))
		return
	}

	// 更新知识库的模型ID
	kb.SummaryModelID = req.LLMModelID
	kb.EmbeddingModelID = req.EmbeddingModelID

	// 处理多模态模型配置
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

	// 更新文档分块配置
	if req.DocumentSplitting.ChunkSize > 0 {
		kb.ChunkingConfig.ChunkSize = req.DocumentSplitting.ChunkSize
	}
	if req.DocumentSplitting.ChunkOverlap >= 0 {
		kb.ChunkingConfig.ChunkOverlap = req.DocumentSplitting.ChunkOverlap
	}
	if len(req.DocumentSplitting.Separators) > 0 {
		kb.ChunkingConfig.Separators = req.DocumentSplitting.Separators
	}

	// 更新多模态配置
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
		// 多模态未启用时，清空存储配置
		kb.StorageConfig = types.StorageConfig{}
	}

	// 更新知识图谱配置
	if req.NodeExtract.Enabled {
		// 转换 Nodes 和 Relations 为指针类型
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

	// 更新问题生成配置
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

	// 保存更新后的知识库
	if err := h.kbRepository.UpdateKnowledgeBase(ctx, kb); err != nil {
		logger.Error(ctx, "Failed to update knowledge base", err)
		c.Error(errors.NewInternalServerError("更新知识库失败: " + err.Error()))
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "配置更新成功",
	})
}

// InitializeByKB godoc
// @Summary      初始化知识库配置
// @Description  根据知识库ID执行完整配置更新
// @Tags         初始化
// @Accept       json
// @Produce      json
// @Param        kbId     path      string  true  "知识库ID"
// @Param        request  body      object  true  "初始化请求"
// @Success      200      {object}  map[string]interface{}  "初始化成功"
// @Failure      400      {object}  errors.AppError         "请求参数错误"
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
		c.Error(errors.NewInternalServerError("更新知识库配置失败: " + err.Error()))
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "知识库配置更新成功",
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
		return nil, errors.NewInternalServerError("获取知识库信息失败: " + err.Error())
	}
	if kb == nil {
		logger.Error(ctx, "Knowledge base not found")
		return nil, errors.NewNotFoundError("知识库不存在")
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
		return errors.NewBadRequestError("启用多模态时需要配置VLM信息")
	}
	if req.Multimodal.VLM.InterfaceType == "ollama" {
		req.Multimodal.VLM.BaseURL = os.Getenv("OLLAMA_BASE_URL") + "/v1"
	}
	if req.Multimodal.VLM.ModelName == "" || req.Multimodal.VLM.BaseURL == "" {
		logger.Error(ctx, "VLM configuration incomplete")
		return errors.NewBadRequestError("VLM配置不完整")
	}

	switch storageType {
	case "cos":
		if req.Multimodal.COS == nil || req.Multimodal.COS.SecretID == "" || req.Multimodal.COS.SecretKey == "" ||
			req.Multimodal.COS.Region == "" || req.Multimodal.COS.BucketName == "" ||
			req.Multimodal.COS.AppID == "" {
			logger.Error(ctx, "COS configuration incomplete")
			return errors.NewBadRequestError("COS配置不完整")
		}
	case "minio":
		if req.Multimodal.Minio == nil || req.Multimodal.Minio.BucketName == "" ||
			os.Getenv("MINIO_ACCESS_KEY_ID") == "" || os.Getenv("MINIO_SECRET_ACCESS_KEY") == "" {
			logger.Error(ctx, "MinIO configuration incomplete")
			return errors.NewBadRequestError("MinIO配置不完整")
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
		return errors.NewBadRequestError("Rerank配置不完整")
	}
	return nil
}

func validateNodeExtractConfig(ctx context.Context, req *InitializationRequest) error {
	if !req.NodeExtract.Enabled {
		return nil
	}
	if strings.ToLower(os.Getenv("NEO4J_ENABLE")) != "true" {
		logger.Error(ctx, "Node Extractor configuration incomplete")
		return errors.NewBadRequestError("请正确配置环境变量NEO4J_ENABLE")
	}
	if req.NodeExtract.Text == "" || len(req.NodeExtract.Tags) == 0 {
		logger.Error(ctx, "Node Extractor configuration incomplete")
		return errors.NewBadRequestError("Node Extractor配置不完整")
	}
	if len(req.NodeExtract.Nodes) == 0 || len(req.NodeExtract.Relations) == 0 {
		logger.Error(ctx, "Node Extractor configuration incomplete")
		return errors.NewBadRequestError("请先提取实体和关系")
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
				return nil, errors.NewInternalServerError("更新模型失败: " + err.Error())
			}
			processedModels = append(processedModels, existingModel)
			continue
		}

		if err := h.modelService.CreateModel(ctx, model); err != nil {
			logger.ErrorWithFields(ctx, err, map[string]interface{}{
				"model_id": model.ID,
				"kb_id":    kbIdStr,
			})
			return nil, errors.NewInternalServerError("创建模型失败: " + err.Error())
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
// @Summary      检查Ollama服务状态
// @Description  检查Ollama服务是否可用
// @Tags         初始化
// @Accept       json
// @Produce      json
// @Success      200  {object}  map[string]interface{}  "Ollama状态"
// @Router       /initialization/ollama/status [get]
func (h *InitializationHandler) CheckOllamaStatus(c *gin.Context) {
	ctx := c.Request.Context()

	logger.Info(ctx, "Checking Ollama service status")

	// Determine Ollama base URL for display
	baseURL := os.Getenv("OLLAMA_BASE_URL")
	if baseURL == "" {
		baseURL = "http://host.docker.internal:11434"
	}

	// 检查Ollama服务是否可用
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
// @Summary      检查Ollama模型状态
// @Description  检查指定的Ollama模型是否已安装
// @Tags         初始化
// @Accept       json
// @Produce      json
// @Param        request  body      object{models=[]string}  true  "模型名称列表"
// @Success      200      {object}  map[string]interface{}   "模型状态"
// @Failure      400      {object}  errors.AppError          "请求参数错误"
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

	// 检查Ollama服务是否可用
	if !h.ollamaService.IsAvailable() {
		err := h.ollamaService.StartService(ctx)
		if err != nil {
			logger.ErrorWithFields(ctx, err, nil)
			c.Error(errors.NewInternalServerError("Ollama服务不可用: " + err.Error()))
			return
		}
	}

	modelStatus := make(map[string]bool)

	// 检查每个模型是否存在
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
// @Summary      下载Ollama模型
// @Description  异步下载指定的Ollama模型
// @Tags         初始化
// @Accept       json
// @Produce      json
// @Param        request  body      object{modelName=string}  true  "模型名称"
// @Success      200      {object}  map[string]interface{}    "下载任务信息"
// @Failure      400      {object}  errors.AppError           "请求参数错误"
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

	// 检查Ollama服务是否可用
	if !h.ollamaService.IsAvailable() {
		err := h.ollamaService.StartService(ctx)
		if err != nil {
			logger.ErrorWithFields(ctx, err, nil)
			c.Error(errors.NewInternalServerError("Ollama服务不可用: " + err.Error()))
			return
		}
	}

	// 检查模型是否已存在
	available, err := h.ollamaService.IsModelAvailable(ctx, req.ModelName)
	if err != nil {
		c.Error(errors.NewInternalServerError("检查模型状态失败: " + err.Error()))
		return
	}

	if available {
		c.JSON(http.StatusOK, gin.H{
			"success": true,
			"message": "模型已存在",
			"data": gin.H{
				"modelName": req.ModelName,
				"status":    "completed",
				"progress":  100.0,
			},
		})
		return
	}

	// 检查是否已有相同模型的下载任务
	tasksMutex.RLock()
	for _, task := range downloadTasks {
		if task.ModelName == req.ModelName && (task.Status == "pending" || task.Status == "downloading") {
			tasksMutex.RUnlock()
			c.JSON(http.StatusOK, gin.H{
				"success": true,
				"message": "模型下载任务已存在",
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

	// 创建下载任务
	taskID := uuid.New().String()
	task := &DownloadTask{
		ID:        taskID,
		ModelName: req.ModelName,
		Status:    "pending",
		Progress:  0.0,
		Message:   "准备下载",
		StartTime: time.Now(),
	}

	tasksMutex.Lock()
	downloadTasks[taskID] = task
	tasksMutex.Unlock()

	// 启动异步下载
	newCtx, cancel := context.WithTimeout(context.Background(), 12*time.Hour)
	go func() {
		defer cancel()
		h.downloadModelAsync(newCtx, taskID, req.ModelName)
	}()

	logger.Infof(ctx, "Created download task for model, task ID: %s", taskID)
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "模型下载任务已创建",
		"data": gin.H{
			"taskId":    taskID,
			"modelName": req.ModelName,
			"status":    "pending",
			"progress":  0.0,
		},
	})
}

// GetDownloadProgress godoc
// @Summary      获取下载进度
// @Description  获取Ollama模型下载任务的进度
// @Tags         初始化
// @Accept       json
// @Produce      json
// @Param        taskId  path      string  true  "任务ID"
// @Success      200     {object}  map[string]interface{}  "下载进度"
// @Failure      404     {object}  errors.AppError         "任务不存在"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /initialization/ollama/download/{taskId} [get]
func (h *InitializationHandler) GetDownloadProgress(c *gin.Context) {
	taskID := c.Param("taskId")

	if taskID == "" {
		c.Error(errors.NewBadRequestError("任务ID不能为空"))
		return
	}

	tasksMutex.RLock()
	task, exists := downloadTasks[taskID]
	tasksMutex.RUnlock()

	if !exists {
		c.Error(errors.NewNotFoundError("下载任务不存在"))
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    task,
	})
}

// ListDownloadTasks godoc
// @Summary      列出下载任务
// @Description  列出所有Ollama模型下载任务
// @Tags         初始化
// @Accept       json
// @Produce      json
// @Success      200  {object}  map[string]interface{}  "任务列表"
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
// @Summary      列出Ollama模型
// @Description  列出已安装的Ollama模型
// @Tags         初始化
// @Accept       json
// @Produce      json
// @Success      200  {object}  map[string]interface{}  "模型列表"
// @Failure      500  {object}  errors.AppError         "服务器错误"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /initialization/ollama/models [get]
func (h *InitializationHandler) ListOllamaModels(c *gin.Context) {
	ctx := c.Request.Context()

	logger.Info(ctx, "Listing installed Ollama models")

	// 确保服务可用
	if !h.ollamaService.IsAvailable() {
		if err := h.ollamaService.StartService(ctx); err != nil {
			logger.ErrorWithFields(ctx, err, nil)
			c.Error(errors.NewInternalServerError("Ollama服务不可用: " + err.Error()))
			return
		}
	}

	// 使用 ListModelsDetailed 获取包含大小等详细信息的模型列表
	models, err := h.ollamaService.ListModelsDetailed(ctx)
	if err != nil {
		logger.ErrorWithFields(ctx, err, nil)
		c.Error(errors.NewInternalServerError("获取模型列表失败: " + err.Error()))
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"models": models,
		},
	})
}

// downloadModelAsync 异步下载模型
func (h *InitializationHandler) downloadModelAsync(ctx context.Context,
	taskID, modelName string,
) {
	logger.Infof(ctx, "Starting async download for model, task: %s", taskID)

	// 更新任务状态为下载中
	h.updateTaskStatus(taskID, "downloading", 0.0, "开始下载模型")

	// 执行下载，带进度回调
	err := h.pullModelWithProgress(ctx, modelName, func(progress float64, message string) {
		h.updateTaskStatus(taskID, "downloading", progress, message)
	})
	if err != nil {
		logger.Error(ctx, "Failed to download model", err)
		h.updateTaskStatus(taskID, "failed", 0.0, fmt.Sprintf("下载失败: %v", err))
		return
	}

	// 下载成功
	logger.Infof(ctx, "Model downloaded successfully, task: %s", taskID)
	h.updateTaskStatus(taskID, "completed", 100.0, "下载完成")
}

// pullModelWithProgress 下载模型并提供进度回调
func (h *InitializationHandler) pullModelWithProgress(ctx context.Context,
	modelName string,
	progressCallback func(float64, string),
) error {
	// 检查服务是否可用
	if err := h.ollamaService.StartService(ctx); err != nil {
		logger.ErrorWithFields(ctx, err, nil)
		return err
	}

	// 检查模型是否已存在
	available, err := h.ollamaService.IsModelAvailable(ctx, modelName)
	if err != nil {
		logger.Error(ctx, "Failed to check model availability", err)
		return err
	}
	if available {
		progressCallback(100.0, "模型已存在")
		return nil
	}

	// 创建下载请求
	pullReq := &api.PullRequest{
		Name: modelName,
	}

	// 使用Ollama客户端的Pull方法，带进度回调
	err = h.ollamaService.GetClient().Pull(ctx, pullReq, func(progress api.ProgressResponse) error {
		progressPercent := 0.0
		message := "下载中"

		if progress.Total > 0 && progress.Completed > 0 {
			progressPercent = float64(progress.Completed) / float64(progress.Total) * 100
			message = fmt.Sprintf("下载中: %.1f%% (%s)", progressPercent, progress.Status)
		} else if progress.Status != "" {
			message = progress.Status
		}

		// 调用进度回调
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

// updateTaskStatus 更新任务状态
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
// @Summary      获取知识库配置
// @Description  根据知识库ID获取当前配置信息
// @Tags         初始化
// @Accept       json
// @Produce      json
// @Param        kbId  path      string  true  "知识库ID"
// @Success      200   {object}  map[string]interface{}  "配置信息"
// @Failure      404   {object}  errors.AppError         "知识库不存在"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /initialization/kb/{kbId}/config [get]
func (h *InitializationHandler) GetCurrentConfigByKB(c *gin.Context) {
	ctx := c.Request.Context()
	kbIdStr := utils.SanitizeForLog(c.Param("kbId"))

	logger.Info(ctx, "Getting configuration for knowledge base")

	// 获取指定知识库信息
	kb, err := h.kbService.GetKnowledgeBaseByID(ctx, kbIdStr)
	if err != nil {
		logger.Error(ctx, "Failed to get knowledge base", err)
		c.Error(errors.NewInternalServerError("获取知识库信息失败: " + err.Error()))
		return
	}

	if kb == nil {
		logger.Error(ctx, "Knowledge base not found")
		c.Error(errors.NewNotFoundError("知识库不存在"))
		return
	}

	// 根据知识库的模型ID获取特定模型
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
				// 如果模型不存在或获取失败，继续处理其他模型
				continue
			}
			if model != nil {
				models = append(models, model)
			}
		}
	}

	// 检查知识库是否有文件
	knowledgeList, err := h.knowledgeService.ListPagedKnowledgeByKnowledgeBaseID(ctx,
		kbIdStr, &types.Pagination{
			Page:     1,
			PageSize: 1,
		}, "", "", "")
	hasFiles := err == nil && knowledgeList != nil && knowledgeList.Total > 0

	// 构建配置响应
	config := h.buildConfigResponse(ctx, models, kb, hasFiles)

	logger.Info(ctx, "Knowledge base configuration retrieved successfully")
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    config,
	})
}

// buildConfigResponse 构建配置响应数据
func (h *InitializationHandler) buildConfigResponse(ctx context.Context, models []*types.Model,
	kb *types.KnowledgeBase, hasFiles bool,
) map[string]interface{} {
	config := map[string]interface{}{
		"hasFiles": hasFiles,
	}

	// 按类型分组模型
	for _, model := range models {
		if model == nil {
			continue
		}
		// Hide sensitive information for builtin models
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

	// 判断多模态是否启用：有VLM模型ID或有存储配置
	hasMultimodal := (kb.VLMConfig.IsEnabled() ||
		kb.StorageConfig.SecretID != "" || kb.StorageConfig.BucketName != "")
	if config["multimodal"] == nil {
		config["multimodal"] = map[string]interface{}{
			"enabled": hasMultimodal,
		}
	} else {
		// 如果已经设置过 multimodal，更新 enabled 状态
		config["multimodal"].(map[string]interface{})["enabled"] = hasMultimodal
	}

	// 如果没有Rerank模型，设置rerank为disabled
	if config["rerank"] == nil {
		config["rerank"] = map[string]interface{}{
			"enabled":   false,
			"modelName": "",
			"baseUrl":   "",
			"apiKey":    "",
		}
	}

	// 添加知识库的文档分割配置
	if kb != nil {
		config["documentSplitting"] = map[string]interface{}{
			"chunkSize":    kb.ChunkingConfig.ChunkSize,
			"chunkOverlap": kb.ChunkingConfig.ChunkOverlap,
			"separators":   kb.ChunkingConfig.Separators,
		}

		// 添加多模态的COS配置信息
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

// RemoteModelCheckRequest 远程模型检查请求结构
type RemoteModelCheckRequest struct {
	ModelName string `json:"modelName" binding:"required"`
	BaseURL   string `json:"baseUrl"   binding:"required"`
	APIKey    string `json:"apiKey"`
}

// CheckRemoteModel godoc
// @Summary      检查远程模型
// @Description  检查远程API模型连接是否正常
// @Tags         初始化
// @Accept       json
// @Produce      json
// @Param        request  body      RemoteModelCheckRequest  true  "模型检查请求"
// @Success      200      {object}  map[string]interface{}   "检查结果"
// @Failure      400      {object}  errors.AppError          "请求参数错误"
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

	// 验证请求参数
	if req.ModelName == "" || req.BaseURL == "" {
		logger.Error(ctx, "Model name and base URL are required")
		c.Error(errors.NewBadRequestError("模型名称和Base URL不能为空"))
		return
	}

	// 创建模型配置进行测试
	modelConfig := &types.Model{
		Name:   req.ModelName,
		Source: "remote",
		Parameters: types.ModelParameters{
			BaseURL: req.BaseURL,
			APIKey:  req.APIKey,
		},
		Type: "llm", // 默认类型，实际检查时不区分具体类型
	}

	// 检查远程模型连接
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
// @Summary      测试Embedding模型
// @Description  测试Embedding接口是否可用并返回向量维度
// @Tags         初始化
// @Accept       json
// @Produce      json
// @Param        request  body      object  true  "Embedding测试请求"
// @Success      200      {object}  map[string]interface{}  "测试结果"
// @Failure      400      {object}  errors.AppError         "请求参数错误"
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

	// 检查是否是阿里云多模态 embedding 模型（暂不支持）
	if strings.ToLower(req.Provider) == "aliyun" {
		modelNameLower := strings.ToLower(req.ModelName)
		if strings.Contains(modelNameLower, "vision") || strings.Contains(modelNameLower, "multimodal") {
			logger.Infof(ctx, "Aliyun multimodal embedding model not supported: %s", req.ModelName)
			c.JSON(http.StatusOK, gin.H{
				"success": true,
				"data": gin.H{
					"available": false,
					"message":   "阿里云多模态 Embedding 模型暂不支持，请使用纯文本 Embedding 模型（如 text-embedding-v4）",
					"dimension": 0,
				},
			})
			return
		}
	}

	// 构造 embedder 配置
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

	emb, err := embedding.NewEmbedder(cfg, h.pooler, h.ollamaService)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{"model": utils.SanitizeForLog(req.ModelName)})
		c.JSON(http.StatusOK, gin.H{
			"success": true,
			"data":    gin.H{`available`: false, `message`: fmt.Sprintf("创建Embedder失败: %v", err), `dimension`: 0},
		})
		return
	}

	// 执行一次最小化 embedding 调用
	sample := "hello"
	vec, err := emb.Embed(ctx, sample)
	if err != nil {
		logger.Error(ctx, "Failed to create embedder", err)
		c.JSON(http.StatusOK, gin.H{
			"success": true,
			"data":    gin.H{`available`: false, `message`: fmt.Sprintf("调用Embedding失败: %v", err), `dimension`: 0},
		})
		return
	}

	logger.Infof(ctx, "Embedding test succeeded, dimension: %d", len(vec))
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    gin.H{`available`: true, `message`: fmt.Sprintf("测试成功，向量维度=%d", len(vec)), `dimension`: len(vec)},
	})
}

// checkRemoteModelConnection 检查远程模型连接的内部方法
func (h *InitializationHandler) checkRemoteModelConnection(ctx context.Context,
	model *types.Model,
) (bool, string) {
	// 使用 models/chat 进行连接检查
	// 创建聊天配置
	chatConfig := &chat.ChatConfig{
		Source:    types.ModelSourceRemote,
		BaseURL:   model.Parameters.BaseURL,
		ModelName: model.Name,
		APIKey:    model.Parameters.APIKey,
		ModelID:   model.Name,
	}

	// 创建聊天实例
	chatInstance, err := chat.NewChat(chatConfig, h.ollamaService)
	if err != nil {
		return false, fmt.Sprintf("创建聊天实例失败: %v", err)
	}

	// 构造测试消息
	testMessages := []chat.Message{
		{
			Role:    "user",
			Content: "test",
		},
	}

	// 构造测试选项
	testOptions := &chat.ChatOptions{
		MaxTokens: 1,
		Thinking:  &[]bool{false}[0], // for dashscope.aliyuncs qwen3-32b
	}

	// 使用聊天实例进行测试
	_, err = chatInstance.Chat(ctx, testMessages, testOptions)
	if err != nil {
		// 根据错误类型返回不同的错误信息
		if strings.Contains(err.Error(), "401") || strings.Contains(err.Error(), "unauthorized") {
			return false, "认证失败，请检查API Key"
		} else if strings.Contains(err.Error(), "403") || strings.Contains(err.Error(), "forbidden") {
			return false, "权限不足，请检查API Key权限：" + err.Error()
		} else if strings.Contains(err.Error(), "404") || strings.Contains(err.Error(), "not found") {
			return false, "API端点不存在，请检查Base URL"
		} else if strings.Contains(err.Error(), "timeout") {
			return false, "连接超时，请检查网络连接"
		} else {
			return false, fmt.Sprintf("连接失败: %v", err)
		}
	}

	// 连接成功，模型可用
	return true, "连接正常，模型可用"
}

// checkRerankModelConnection 检查Rerank模型连接和功能的内部方法
func (h *InitializationHandler) checkRerankModelConnection(ctx context.Context,
	modelName, baseURL, apiKey string,
) (bool, string) {
	// 创建Reranker配置
	config := &rerank.RerankerConfig{
		APIKey:    apiKey,
		BaseURL:   baseURL,
		ModelName: modelName,
		Source:    types.ModelSourceRemote, // 默认值，实际会根据URL判断
	}

	// 创建Reranker实例
	reranker, err := rerank.NewReranker(config)
	if err != nil {
		return false, fmt.Sprintf("创建Reranker失败: %v", err)
	}

	// 简化的测试数据
	testQuery := "ping"
	testDocuments := []string{
		"pong",
	}

	// 使用Reranker进行测试
	results, err := reranker.Rerank(ctx, testQuery, testDocuments)
	if err != nil {
		return false, fmt.Sprintf("重排测试失败: %v", err)
	}

	// 检查结果
	if len(results) > 0 {
		return true, fmt.Sprintf("重排功能正常，返回%d个结果", len(results))
	} else {
		return false, "重排接口连接成功，但未返回重排结果"
	}
}

// CheckRerankModel godoc
// @Summary      检查Rerank模型
// @Description  检查Rerank模型连接和功能是否正常
// @Tags         初始化
// @Accept       json
// @Produce      json
// @Param        request  body      object  true  "Rerank检查请求"
// @Success      200      {object}  map[string]interface{}  "检查结果"
// @Failure      400      {object}  errors.AppError         "请求参数错误"
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

	// 验证请求参数
	if req.ModelName == "" || req.BaseURL == "" {
		logger.Error(ctx, "Model name and base URL are required")
		c.Error(errors.NewBadRequestError("模型名称和Base URL不能为空"))
		return
	}

	// 检查Rerank模型连接和功能
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

// 使用结构体解析表单数据
type testMultimodalForm struct {
	VLMModel         string `form:"vlm_model"`
	VLMBaseURL       string `form:"vlm_base_url"`
	VLMAPIKey        string `form:"vlm_api_key"`
	VLMInterfaceType string `form:"vlm_interface_type"`

	StorageType string `form:"storage_type"`

	// COS 配置
	COSSecretID   string `form:"cos_secret_id"`
	COSSecretKey  string `form:"cos_secret_key"`
	COSRegion     string `form:"cos_region"`
	COSBucketName string `form:"cos_bucket_name"`
	COSAppID      string `form:"cos_app_id"`
	COSPathPrefix string `form:"cos_path_prefix"`

	// MinIO 配置（当存储为 minio 时）
	MinioBucketName string `form:"minio_bucket_name"`
	MinioPathPrefix string `form:"minio_path_prefix"`

	// 文档切分配置（字符串后续自行解析，以避免类型绑定失败）
	ChunkSize     string `form:"chunk_size"`
	ChunkOverlap  string `form:"chunk_overlap"`
	SeparatorsRaw string `form:"separators"`
}

// TestMultimodalFunction godoc
// @Summary      测试多模态功能
// @Description  上传图片测试多模态处理功能
// @Tags         初始化
// @Accept       multipart/form-data
// @Produce      json
// @Param        image             formData  file    true   "测试图片"
// @Param        vlm_model         formData  string  true   "VLM模型名称"
// @Param        vlm_base_url      formData  string  true   "VLM Base URL"
// @Param        vlm_api_key       formData  string  false  "VLM API Key"
// @Param        vlm_interface_type formData string  false  "VLM接口类型"
// @Param        storage_type      formData  string  true   "存储类型(cos/minio)"
// @Success      200               {object}  map[string]interface{}  "测试结果"
// @Failure      400               {object}  errors.AppError         "请求参数错误"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /initialization/multimodal/test [post]
func (h *InitializationHandler) TestMultimodalFunction(c *gin.Context) {
	ctx := c.Request.Context()

	logger.Info(ctx, "Testing multimodal functionality")

	var req testMultimodalForm
	if err := c.ShouldBind(&req); err != nil {
		logger.Error(ctx, "Failed to parse form data", err)
		c.Error(errors.NewBadRequestError("表单参数解析失败"))
		return
	}
	// ollama 场景自动拼接 base url
	if req.VLMInterfaceType == "ollama" {
		req.VLMBaseURL = os.Getenv("OLLAMA_BASE_URL") + "/v1"
	}

	req.StorageType = strings.ToLower(req.StorageType)

	if req.VLMModel == "" || req.VLMBaseURL == "" {
		logger.Error(ctx, "VLM model name and base URL are required")
		c.Error(errors.NewBadRequestError("VLM模型名称和Base URL不能为空"))
		return
	}
	switch req.StorageType {
	case "cos":
		// 必填：SecretID/SecretKey/Region/BucketName/AppID；PathPrefix 可选
		if req.COSSecretID == "" || req.COSSecretKey == "" ||
			req.COSRegion == "" || req.COSBucketName == "" ||
			req.COSAppID == "" {
			logger.Error(ctx, "COS configuration is required")
			c.Error(errors.NewBadRequestError("COS配置信息不能为空"))
			return
		}
	case "minio":
		if req.MinioBucketName == "" {
			logger.Error(ctx, "MinIO configuration is required")
			c.Error(errors.NewBadRequestError("MinIO配置信息不能为空"))
			return
		}
	default:
		logger.Error(ctx, "Invalid storage type")
		c.Error(errors.NewBadRequestError("无效的存储类型"))
		return
	}

	// 获取上传的图片文件
	file, header, err := c.Request.FormFile("image")
	if err != nil {
		logger.Error(ctx, "Failed to get uploaded image", err)
		c.Error(errors.NewBadRequestError("获取上传图片失败"))
		return
	}
	defer file.Close()

	// 验证文件类型
	if !strings.HasPrefix(header.Header.Get("Content-Type"), "image/") {
		logger.Error(ctx, "Invalid file type, only images are allowed")
		c.Error(errors.NewBadRequestError("只允许上传图片文件"))
		return
	}

	// 验证文件大小 (default 50MB, configurable via MAX_FILE_SIZE_MB)
	maxSize := utils.GetMaxFileSize()
	if header.Size > maxSize {
		logger.Error(ctx, "File size too large")
		c.Error(errors.NewBadRequestError(fmt.Sprintf("图片文件大小不能超过%dMB", utils.GetMaxFileSizeMB())))
		return
	}
	logger.Infof(ctx, "Processing image: %s", utils.SanitizeForLog(header.Filename))

	// 解析文档分割配置
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

	// 读取图片文件内容
	imageContent, err := io.ReadAll(file)
	if err != nil {
		logger.Error(ctx, "Failed to read image file", err)
		c.Error(errors.NewBadRequestError("读取图片文件失败"))
		return
	}

	// 调用多模态测试
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

// testMultimodalWithDocReader 调用docreader服务进行多模态处理
func (h *InitializationHandler) testMultimodalWithDocReader(
	ctx context.Context,
	imageContent []byte, filename string,
	chunkSize, chunkOverlap int32, separators []string,
	req *testMultimodalForm,
) (map[string]string, error) {
	// 获取文件扩展名
	fileExt := ""
	if idx := strings.LastIndex(filename, "."); idx != -1 {
		fileExt = strings.ToLower(filename[idx+1:])
	}

	// 检查docreader服务配置
	if h.docReaderClient == nil {
		return nil, fmt.Errorf("DocReader service not configured")
	}

	// 构造请求
	request := &proto.ReadFromFileRequest{
		FileContent: imageContent,
		FileName:    filename,
		FileType:    fileExt,
		ReadConfig: &proto.ReadConfig{
			ChunkSize:        chunkSize,
			ChunkOverlap:     chunkOverlap,
			Separators:       separators,
			EnableMultimodal: true, // 启用多模态处理
			VlmConfig: &proto.VLMConfig{
				ModelName:     req.VLMModel,
				BaseUrl:       req.VLMBaseURL,
				ApiKey:        req.VLMAPIKey,
				InterfaceType: req.VLMInterfaceType,
			},
		},
		RequestId: ctx.Value(types.RequestIDContextKey).(string),
	}

	// 设置对象存储配置（通用）
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

	// 调用docreader服务
	response, err := h.docReaderClient.ReadFromFile(ctx, request)
	if err != nil {
		return nil, fmt.Errorf("调用DocReader服务失败: %v", err)
	}

	if response.Error != "" {
		return nil, fmt.Errorf("DocReader服务返回错误: %s", response.Error)
	}

	// 处理响应，提取Caption和OCR信息
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

	// 合并所有Caption和OCR结果
	result["caption"] = strings.Join(allCaptions, "; ")
	result["ocr"] = strings.Join(allOCRTexts, "; ")

	return result, nil
}

// TextRelationExtractionRequest 文本关系提取请求结构
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

// TextRelationExtractionResponse 文本关系提取响应结构
type TextRelationExtractionResponse struct {
	Nodes     []*types.GraphNode     `json:"nodes"`
	Relations []*types.GraphRelation `json:"relations"`
}

// ExtractTextRelations godoc
// @Summary      提取文本关系
// @Description  从文本中提取实体和关系
// @Tags         初始化
// @Accept       json
// @Produce      json
// @Param        request  body      TextRelationExtractionRequest  true  "提取请求"
// @Success      200      {object}  map[string]interface{}         "提取结果"
// @Failure      400      {object}  errors.AppError                "请求参数错误"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /initialization/extract/relations [post]
func (h *InitializationHandler) ExtractTextRelations(c *gin.Context) {
	ctx := c.Request.Context()

	var req TextRelationExtractionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Error(ctx, "文本关系提取请求参数错误")
		c.Error(errors.NewBadRequestError("文本关系提取请求参数错误"))
		return
	}

	// 验证文本内容
	if len(req.Text) == 0 {
		c.Error(errors.NewBadRequestError("文本内容不能为空"))
		return
	}

	if len(req.Text) > 5000 {
		c.Error(errors.NewBadRequestError("文本内容长度不能超过5000字符"))
		return
	}

	// 验证标签
	if len(req.Tags) == 0 {
		c.Error(errors.NewBadRequestError("至少需要选择一个关系标签"))
		return
	}

	// 调用模型服务进行文本关系提取
	result, err := h.extractRelationsFromText(ctx, req.Text, req.Tags, req.LLMConfig)
	if err != nil {
		logger.Error(ctx, "文本关系提取失败", err)
		c.Error(errors.NewInternalServerError("文本关系提取失败: " + err.Error()))
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    result,
	})
}

// extractRelationsFromText 从文本中提取关系
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
	}, h.ollamaService)
	if err != nil {
		logger.Error(ctx, "初始化模型服务失败", err)
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
		logger.Error(ctx, "文本关系提取失败", err)
		return nil, err
	}
	extractor.RemoveUnknownRelation(ctx, graph)

	result := &TextRelationExtractionResponse{
		Nodes:     graph.Node,
		Relations: graph.Relation,
	}

	return result, nil
}

// FabriTextRequest is a request for generating example text
type FabriTextRequest struct {
	Tags      []string  `json:"tags"`
	LLMConfig LLMConfig `json:"llm_config"`
}

// FabriTextResponse is a response for generating example text
type FabriTextResponse struct {
	Text string `json:"text"`
}

// FabriText godoc
// @Summary      生成示例文本
// @Description  根据标签生成示例文本
// @Tags         初始化
// @Accept       json
// @Produce      json
// @Param        request  body      FabriTextRequest  true  "生成请求"
// @Success      200      {object}  map[string]interface{}  "生成的文本"
// @Failure      400      {object}  errors.AppError         "请求参数错误"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /initialization/fabri/text [post]
func (h *InitializationHandler) FabriText(c *gin.Context) {
	ctx := c.Request.Context()

	var req FabriTextRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Error(ctx, "生成示例文本请求参数错误")
		c.Error(errors.NewBadRequestError("生成示例文本请求参数错误"))
		return
	}

	result, err := h.fabriText(ctx, req.Tags, req.LLMConfig)
	if err != nil {
		logger.Error(ctx, "生成示例文本失败", err)
		c.Error(errors.NewInternalServerError("生成示例文本失败: " + err.Error()))
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    FabriTextResponse{Text: result},
	})
}

// fabriText generates example text
func (h *InitializationHandler) fabriText(ctx context.Context, tags []string, llm LLMConfig) (string, error) {
	chatModel, err := chat.NewChat(&chat.ChatConfig{
		ModelID:   "initialization",
		APIKey:    llm.ApiKey,
		BaseURL:   llm.BaseUrl,
		ModelName: llm.ModelName,
		Source:    types.ModelSource(llm.Source),
	}, h.ollamaService)
	if err != nil {
		logger.Error(ctx, "初始化模型服务失败", err)
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
		logger.Error(ctx, "生成示例文本失败", err)
		return "", err
	}
	return result.Content, nil
}

// FabriTagRequest is a request for generating tags
type FabriTagRequest struct {
	LLMConfig LLMConfig `json:"llm_config"`
}

// FabriTagResponse is a response for generating tags
type FabriTagResponse struct {
	Tags []string `json:"tags"`
}

var tagOptions = []string{
	"内容", "文化", "人物", "事件", "时间", "地点", "作品", "作者", "关系", "属性",
}

// FabriTag godoc
// @Summary      生成随机标签
// @Description  随机生成一组标签
// @Tags         初始化
// @Accept       json
// @Produce      json
// @Success      200  {object}  map[string]interface{}  "生成的标签"
// @Router       /initialization/fabri/tag [get]
func (h *InitializationHandler) FabriTag(c *gin.Context) {
	tagRandom := RandomSelect(tagOptions, rand.Intn(len(tagOptions)-1)+1)
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    FabriTagResponse{Tags: tagRandom},
	})
}

// RandomSelect selects random strings
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
