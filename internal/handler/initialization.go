package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"strconv"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/embedding"
	"github.com/Tencent/WeKnora/internal/models/utils/ollama"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/Tencent/WeKnora/services/docreader/src/client"
	"github.com/Tencent/WeKnora/services/docreader/src/proto"
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

// InitializationRequest 初始化请求结构
type InitializationRequest struct {
	// 前端传入的存储类型：cos 或 minio
	StorageType string `json:"storageType"`
	LLM         struct {
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
		COS *struct {
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
		ChunkOverlap int      `json:"chunkOverlap" binding:"required,min=0"`
		Separators   []string `json:"separators" binding:"required,min=1"`
	} `json:"documentSplitting" binding:"required"`
}

// CheckStatus 检查系统初始化状态
func (h *InitializationHandler) CheckStatus(c *gin.Context) {
	ctx := c.Request.Context()
	logger.Info(ctx, "Checking system initialization status")

	// 检查是否存在租户
	tenant, err := h.tenantService.GetTenantByID(ctx, types.InitDefaultTenantID)
	if err != nil {
		logger.ErrorWithFields(ctx, err, nil)
		c.JSON(http.StatusOK, gin.H{
			"success": true,
			"data": gin.H{
				"initialized": false,
			},
		})
		return
	}

	// 如果没有租户，说明系统未初始化
	if tenant == nil {
		logger.Info(ctx, "No tenants found, system not initialized")
		c.JSON(http.StatusOK, gin.H{
			"success": true,
			"data": gin.H{
				"initialized": false,
			},
		})
		return
	}
	ctx = context.WithValue(ctx, types.TenantIDContextKey, types.InitDefaultTenantID)

	// 检查是否存在模型
	models, err := h.modelService.ListModels(ctx)
	if err != nil || len(models) == 0 {
		logger.Info(ctx, "No models found, system not initialized")
		c.JSON(http.StatusOK, gin.H{
			"success": true,
			"data": gin.H{
				"initialized": false,
			},
		})
		return
	}

	logger.Info(ctx, "System is already initialized")
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"initialized": true,
		},
	})
}

// Initialize 执行系统初始化
func (h *InitializationHandler) Initialize(c *gin.Context) {
	ctx := c.Request.Context()

	logger.Info(ctx, "Starting system initialization")

	var req InitializationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Error(ctx, "Failed to parse initialization request", err)
		c.Error(errors.NewBadRequestError(err.Error()))
		return
	}

	// 验证多模态配置
	if req.Multimodal.Enabled {
		storageType := strings.ToLower(req.StorageType)
		if req.Multimodal.VLM == nil {
			logger.Error(ctx, "Multimodal enabled but missing VLM configuration")
			c.Error(errors.NewBadRequestError("启用多模态时需要配置VLM信息"))
			return
		}
		if req.Multimodal.VLM.InterfaceType == "ollama" {
			req.Multimodal.VLM.BaseURL = os.Getenv("OLLAMA_BASE_URL") + "/v1"
		}
		if req.Multimodal.VLM.ModelName == "" || req.Multimodal.VLM.BaseURL == "" {
			logger.Error(ctx, "VLM configuration incomplete")
			c.Error(errors.NewBadRequestError("VLM配置不完整"))
			return
		}
		switch storageType {
		case "cos":
			if req.Multimodal.COS == nil || req.Multimodal.COS.SecretID == "" || req.Multimodal.COS.SecretKey == "" ||
				req.Multimodal.COS.Region == "" || req.Multimodal.COS.BucketName == "" ||
				req.Multimodal.COS.AppID == "" {
				logger.Error(ctx, "COS configuration incomplete")
				c.Error(errors.NewBadRequestError("COS配置不完整"))
				return
			}
		case "minio":
			if req.Multimodal.Minio == nil || req.Multimodal.Minio.BucketName == "" ||
				os.Getenv("MINIO_ACCESS_KEY_ID") == "" || os.Getenv("MINIO_SECRET_ACCESS_KEY") == "" {
				logger.Error(ctx, "MinIO configuration incomplete")
				c.Error(errors.NewBadRequestError("MinIO配置不完整"))
				return
			}
		}
	}

	// 验证Rerank配置（如果启用）
	if req.Rerank.Enabled {
		if req.Rerank.ModelName == "" || req.Rerank.BaseURL == "" {
			logger.Error(ctx, "Rerank configuration incomplete")
			c.Error(errors.NewBadRequestError("启用Rerank时需要配置模型名称和Base URL"))
			return
		}
	}

	// 验证文档分割配置
	if req.DocumentSplitting.ChunkOverlap >= req.DocumentSplitting.ChunkSize {
		logger.Error(ctx, "Chunk overlap must be less than chunk size")
		c.Error(errors.NewBadRequestError("分块重叠大小必须小于分块大小"))
		return
	}
	if len(req.DocumentSplitting.Separators) == 0 {
		logger.Error(ctx, "Document separators cannot be empty")
		c.Error(errors.NewBadRequestError("文档分隔符不能为空"))
		return
	}
	var err error
	// 1. 处理租户 - 检查是否存在，不存在则创建
	tenant, _ := h.tenantService.GetTenantByID(ctx, types.InitDefaultTenantID)
	if tenant == nil {
		logger.Info(ctx, "Tenant not found, creating tenant")
		// 创建默认租户
		tenant = &types.Tenant{
			ID:          types.InitDefaultTenantID,
			Name:        "Default Tenant",
			Description: "System Default Tenant",
			RetrieverEngines: types.RetrieverEngines{
				Engines: []types.RetrieverEngineParams{
					{
						RetrieverType:       types.KeywordsRetrieverType,
						RetrieverEngineType: types.PostgresRetrieverEngineType,
					},
					{
						RetrieverType:       types.VectorRetrieverType,
						RetrieverEngineType: types.PostgresRetrieverEngineType,
					},
				},
			},
		}
		logger.Info(ctx, "Creating default tenant")
		tenant, err = h.tenantService.CreateTenant(ctx, tenant)
		if err != nil {
			logger.ErrorWithFields(ctx, err, nil)
			c.Error(errors.NewInternalServerError("创建租户失败: " + err.Error()))
			return
		}
	} else {
		logger.Info(ctx, "Tenant exists, updating if needed")
		// 更新租户信息（如果需要）
		updated := false
		if tenant.Name != "Default Tenant" {
			tenant.Name = "Default Tenant"
			updated = true
		}
		if tenant.Description != "System Default Tenant" {
			tenant.Description = "System Default Tenant"
			updated = true
		}

		if updated {
			_, err = h.tenantService.UpdateTenant(ctx, tenant)
			if err != nil {
				logger.ErrorWithFields(ctx, err, nil)
				c.Error(errors.NewInternalServerError("更新租户失败: " + err.Error()))
				return
			}
			logger.Info(ctx, "Tenant updated successfully")
		}
	}

	// 创建带有租户ID的新上下文
	newCtx := context.WithValue(ctx, types.TenantIDContextKey, types.InitDefaultTenantID)

	// 2. 处理模型 - 检查现有模型并更新或创建
	existingModels, err := h.modelService.ListModels(newCtx)
	if err != nil {
		logger.ErrorWithFields(ctx, err, nil)
		// 如果获取失败，继续执行创建流程
		existingModels = []*types.Model{}
	}

	// 构建模型映射，按类型分组
	modelMap := make(map[types.ModelType]*types.Model)
	for _, model := range existingModels {
		modelMap[model.Type] = model
	}

	// 要处理的模型配置
	modelsToProcess := []struct {
		modelType   types.ModelType
		name        string
		source      types.ModelSource
		description string
		baseURL     string
		apiKey      string
		dimension   int
	}{
		{
			modelType:   types.ModelTypeKnowledgeQA,
			name:        req.LLM.ModelName,
			source:      types.ModelSource(req.LLM.Source),
			description: "LLM Model for Knowledge QA",
			baseURL:     req.LLM.BaseURL,
			apiKey:      req.LLM.APIKey,
		},
		{
			modelType:   types.ModelTypeEmbedding,
			name:        req.Embedding.ModelName,
			source:      types.ModelSource(req.Embedding.Source),
			description: "Embedding Model",
			baseURL:     req.Embedding.BaseURL,
			apiKey:      req.Embedding.APIKey,
			dimension:   req.Embedding.Dimension,
		},
	}

	// 如果启用Rerank，添加Rerank模型
	if req.Rerank.Enabled {
		modelsToProcess = append(modelsToProcess, struct {
			modelType   types.ModelType
			name        string
			source      types.ModelSource
			description string
			baseURL     string
			apiKey      string
			dimension   int
		}{
			modelType:   types.ModelTypeRerank,
			name:        req.Rerank.ModelName,
			source:      types.ModelSourceRemote,
			description: "Rerank Model",
			baseURL:     req.Rerank.BaseURL,
			apiKey:      req.Rerank.APIKey,
		})
	}

	// 如果启用多模态，添加VLM模型
	if req.Multimodal.Enabled && req.Multimodal.VLM != nil {
		modelsToProcess = append(modelsToProcess, struct {
			modelType   types.ModelType
			name        string
			source      types.ModelSource
			description string
			baseURL     string
			apiKey      string
			dimension   int
		}{
			modelType:   types.ModelTypeVLLM,
			name:        req.Multimodal.VLM.ModelName,
			source:      types.ModelSourceRemote,
			description: "Vision Language Model",
			baseURL:     req.Multimodal.VLM.BaseURL,
			apiKey:      req.Multimodal.VLM.APIKey,
		})
	}

	// 处理每个模型
	var processedModels []*types.Model
	for _, modelConfig := range modelsToProcess {
		existingModel, exists := modelMap[modelConfig.modelType]

		if exists {
			// 更新现有模型
			logger.Infof(ctx, "Updating existing model: %s (%s)",
				modelConfig.name, modelConfig.modelType,
			)
			existingModel.Name = modelConfig.name
			existingModel.Source = modelConfig.source
			existingModel.Description = modelConfig.description
			existingModel.Parameters = types.ModelParameters{
				BaseURL: modelConfig.baseURL,
				APIKey:  modelConfig.apiKey,
				EmbeddingParameters: types.EmbeddingParameters{
					Dimension: modelConfig.dimension,
				},
			}
			existingModel.IsDefault = true
			existingModel.Status = types.ModelStatusActive

			err := h.modelService.UpdateModel(newCtx, existingModel)
			if err != nil {
				logger.ErrorWithFields(ctx, err, map[string]interface{}{
					"model_name": modelConfig.name,
					"model_type": modelConfig.modelType,
				})
				c.Error(errors.NewInternalServerError("更新模型失败: " + err.Error()))
				return
			}
			processedModels = append(processedModels, existingModel)
		} else {
			// 创建新模型
			logger.Infof(ctx,
				"Creating new model: %s (%s)",
				modelConfig.name, modelConfig.modelType,
			)
			newModel := &types.Model{
				TenantID:    types.InitDefaultTenantID,
				Name:        modelConfig.name,
				Type:        modelConfig.modelType,
				Source:      modelConfig.source,
				Description: modelConfig.description,
				Parameters: types.ModelParameters{
					BaseURL: modelConfig.baseURL,
					APIKey:  modelConfig.apiKey,
					EmbeddingParameters: types.EmbeddingParameters{
						Dimension: modelConfig.dimension,
					},
				},
				IsDefault: true,
				Status:    types.ModelStatusActive,
			}

			err := h.modelService.CreateModel(newCtx, newModel)
			if err != nil {
				logger.ErrorWithFields(ctx, err, map[string]interface{}{
					"model_name": modelConfig.name,
					"model_type": modelConfig.modelType,
				})
				c.Error(errors.NewInternalServerError("创建模型失败: " + err.Error()))
				return
			}
			processedModels = append(processedModels, newModel)
		}
	}

	// 删除不需要的VLM模型（如果多模态被禁用）
	if !req.Multimodal.Enabled {
		if existingVLM, exists := modelMap[types.ModelTypeVLLM]; exists {
			logger.Info(ctx, "Deleting VLM model as multimodal is disabled")
			err := h.modelService.DeleteModel(newCtx, existingVLM.ID)
			if err != nil {
				logger.ErrorWithFields(ctx, err, map[string]interface{}{
					"model_id": existingVLM.ID,
				})
				// 记录错误但不阻止流程
				logger.Warn(ctx, "Failed to delete VLM model, but continuing")
			}
		}
	}

	// 删除不需要的Rerank模型（如果Rerank被禁用）
	if !req.Rerank.Enabled {
		if existingRerank, exists := modelMap[types.ModelTypeRerank]; exists {
			logger.Info(ctx, "Deleting Rerank model as rerank is disabled")
			err := h.modelService.DeleteModel(newCtx, existingRerank.ID)
			if err != nil {
				logger.ErrorWithFields(ctx, err, map[string]interface{}{
					"model_id": existingRerank.ID,
				})
				// 记录错误但不阻止流程
				logger.Warn(ctx, "Failed to delete Rerank model, but continuing")
			}
		}
	}

	// 3. 处理知识库 - 检查是否存在，不存在则创建，存在则更新
	kb, err := h.kbService.GetKnowledgeBaseByID(newCtx, types.InitDefaultKnowledgeBaseID)

	// 找到embedding模型ID和LLM模型ID
	var embeddingModelID, llmModelID, rerankModelID, vlmModelID string
	for _, model := range processedModels {
		if model.Type == types.ModelTypeEmbedding {
			embeddingModelID = model.ID
		}
		if model.Type == types.ModelTypeKnowledgeQA {
			llmModelID = model.ID
		}
		if model.Type == types.ModelTypeRerank && req.Rerank.Enabled {
			rerankModelID = model.ID
		}
		if model.Type == types.ModelTypeVLLM {
			vlmModelID = model.ID
		}
	}

	if kb == nil {
		// 创建新知识库
		logger.Info(ctx, "Creating default knowledge base")
		kb = &types.KnowledgeBase{
			ID:          types.InitDefaultKnowledgeBaseID,
			Name:        "Default Knowledge Base",
			Description: "System Default Knowledge Base",
			TenantID:    types.InitDefaultTenantID,
			ChunkingConfig: types.ChunkingConfig{
				ChunkSize:        req.DocumentSplitting.ChunkSize,
				ChunkOverlap:     req.DocumentSplitting.ChunkOverlap,
				Separators:       req.DocumentSplitting.Separators,
				EnableMultimodal: req.Multimodal.Enabled,
			},
			EmbeddingModelID: embeddingModelID,
			SummaryModelID:   llmModelID,
			RerankModelID:    rerankModelID,
			VLMModelID:       vlmModelID,
			VLMConfig: types.VLMConfig{
				ModelName:     req.Multimodal.VLM.ModelName,
				BaseURL:       req.Multimodal.VLM.BaseURL,
				APIKey:        req.Multimodal.VLM.APIKey,
				InterfaceType: req.Multimodal.VLM.InterfaceType,
			},
		}
		switch req.StorageType {
		case "cos":
			if req.Multimodal.COS != nil {
				kb.StorageConfig = types.StorageConfig{
					Provider:   req.StorageType,
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
					Provider:   req.StorageType,
					BucketName: req.Multimodal.Minio.BucketName,
					PathPrefix: req.Multimodal.Minio.PathPrefix,
					SecretID:   os.Getenv("MINIO_ACCESS_KEY_ID"),
					SecretKey:  os.Getenv("MINIO_SECRET_ACCESS_KEY"),
				}
			}
		}

		_, err = h.kbService.CreateKnowledgeBase(newCtx, kb)
		if err != nil {
			logger.ErrorWithFields(ctx, err, nil)
			c.Error(errors.NewInternalServerError("创建知识库失败: " + err.Error()))
			return
		}
	} else {
		// 更新现有知识库
		logger.Info(ctx, "Updating existing knowledge base")

		// 检查是否有文件，如果有文件则不允许修改Embedding模型
		knowledgeList, err := h.knowledgeService.ListKnowledgeByKnowledgeBaseID(
			newCtx, types.InitDefaultKnowledgeBaseID,
		)
		hasFiles := err == nil && len(knowledgeList) > 0

		// 先更新模型ID（直接在对象上）
		kb.SummaryModelID = llmModelID
		if req.Rerank.Enabled {
			kb.RerankModelID = rerankModelID
		} else {
			kb.RerankModelID = "" // 清空Rerank模型ID
		}
		if req.Multimodal.Enabled {
			kb.VLMModelID = vlmModelID
			// 更新VLM配置
			kb.VLMConfig = types.VLMConfig{
				ModelName:     req.Multimodal.VLM.ModelName,
				BaseURL:       req.Multimodal.VLM.BaseURL,
				APIKey:        req.Multimodal.VLM.APIKey,
				InterfaceType: req.Multimodal.VLM.InterfaceType,
			}
			switch req.StorageType {
			case "cos":
				if req.Multimodal.COS != nil {
					kb.StorageConfig = types.StorageConfig{
						Provider:   req.StorageType,
						SecretID:   req.Multimodal.COS.SecretID,
						SecretKey:  req.Multimodal.COS.SecretKey,
						Region:     req.Multimodal.COS.Region,
						BucketName: req.Multimodal.COS.BucketName,
						AppID:      req.Multimodal.COS.AppID,
						PathPrefix: req.Multimodal.COS.PathPrefix,
					}
				}
			case "minio":
				if req.Multimodal.Minio != nil {
					kb.StorageConfig = types.StorageConfig{
						Provider:   req.StorageType,
						BucketName: req.Multimodal.Minio.BucketName,
						PathPrefix: req.Multimodal.Minio.PathPrefix,
						SecretID:   os.Getenv("MINIO_ACCESS_KEY_ID"),
						SecretKey:  os.Getenv("MINIO_SECRET_ACCESS_KEY"),
					}
				}
			}
		} else {
			kb.VLMModelID = "" // 清空VLM模型ID
			// 清空VLM配置
			kb.VLMConfig = types.VLMConfig{}
			kb.StorageConfig = types.StorageConfig{}
		}
		if !hasFiles {
			kb.EmbeddingModelID = embeddingModelID
		}
		kb.ChunkingConfig = types.ChunkingConfig{
			ChunkSize:        req.DocumentSplitting.ChunkSize,
			ChunkOverlap:     req.DocumentSplitting.ChunkOverlap,
			Separators:       req.DocumentSplitting.Separators,
			EnableMultimodal: req.Multimodal.Enabled,
		}

		// 更新基本信息和配置
		err = h.kbRepository.UpdateKnowledgeBase(newCtx, kb)
		if err != nil {
			logger.ErrorWithFields(ctx, err, nil)
			c.Error(errors.NewInternalServerError("更新知识库配置失败: " + err.Error()))
			return
		}

		// 如果需要更新模型ID，使用repository直接更新
		if !hasFiles || kb.SummaryModelID != llmModelID {
			// 刷新知识库对象以获取最新信息
			kb, err = h.kbService.GetKnowledgeBaseByID(newCtx, types.InitDefaultKnowledgeBaseID)
			if err != nil {
				logger.ErrorWithFields(ctx, err, nil)
				c.Error(errors.NewInternalServerError("获取更新后的知识库失败: " + err.Error()))
				return
			}

			// 更新模型ID
			kb.SummaryModelID = llmModelID
			if req.Rerank.Enabled {
				kb.RerankModelID = rerankModelID
			} else {
				kb.RerankModelID = "" // 清空Rerank模型ID
			}

			// 使用repository直接更新模型ID
			err = h.kbRepository.UpdateKnowledgeBase(newCtx, kb)
			if err != nil {
				logger.ErrorWithFields(ctx, err, nil)
				c.Error(errors.NewInternalServerError("更新知识库模型ID失败: " + err.Error()))
				return
			}

			logger.Info(ctx, "Model IDs updated successfully")
		}
	}

	logger.Info(ctx, "System initialization completed successfully")
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "系统初始化成功",
		"data": gin.H{
			"tenant":         tenant,
			"models":         processedModels,
			"knowledge_base": kb,
		},
	})
}

// CheckOllamaStatus 检查Ollama服务状态
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

// CheckOllamaModels 检查Ollama模型状态
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
		checkModelName := modelName
		if !strings.Contains(modelName, ":") {
			checkModelName = modelName + ":latest"
		}
		available, err := h.ollamaService.IsModelAvailable(ctx, checkModelName)
		if err != nil {
			logger.ErrorWithFields(ctx, err, map[string]interface{}{
				"model_name": modelName,
			})
			modelStatus[modelName] = false
		} else {
			modelStatus[modelName] = available
		}

		logger.Infof(ctx, "Model %s availability: %v", modelName, modelStatus[modelName])
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"models": modelStatus,
		},
	})
}

// DownloadOllamaModel 异步下载Ollama模型
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
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"model_name": req.ModelName,
		})
		c.Error(errors.NewInternalServerError("检查模型状态失败: " + err.Error()))
		return
	}

	if available {
		logger.Infof(ctx, "Model %s already exists", req.ModelName)
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

	logger.Infof(ctx, "Created download task for model: %s, task ID: %s", req.ModelName, taskID)
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

// GetDownloadProgress 获取下载进度
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

// ListDownloadTasks 列出所有下载任务
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

// ListOllamaModels 列出已安装的 Ollama 模型
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

	models, err := h.ollamaService.ListModels(ctx)
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
	logger.Infof(ctx, "Starting async download for model: %s, task: %s", modelName, taskID)

	// 更新任务状态为下载中
	h.updateTaskStatus(taskID, "downloading", 0.0, "开始下载模型")

	// 执行下载，带进度回调
	err := h.pullModelWithProgress(ctx, modelName, func(progress float64, message string) {
		h.updateTaskStatus(taskID, "downloading", progress, message)
	})

	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"model_name": modelName,
			"task_id":    taskID,
		})
		h.updateTaskStatus(taskID, "failed", 0.0, fmt.Sprintf("下载失败: %v", err))
		return
	}

	// 下载成功
	logger.Infof(ctx, "Model %s downloaded successfully, task: %s", modelName, taskID)
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
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"model_name": modelName,
		})
		return err
	}
	if available {
		progressCallback(100.0, "模型已存在")
		return nil
	}

	logger.GetLogger(ctx).Infof("Pulling model %s...", modelName)

	// 创建下载请求
	pullReq := &api.PullRequest{
		Name: modelName,
	}

	// 使用Ollama客户端的Pull方法，带进度回调
	err = h.ollamaService.GetClient().Pull(ctx, pullReq, func(progress api.ProgressResponse) error {
		var progressPercent float64 = 0.0
		var message string = "下载中"

		if progress.Total > 0 && progress.Completed > 0 {
			progressPercent = float64(progress.Completed) / float64(progress.Total) * 100
			message = fmt.Sprintf("下载中: %.1f%% (%s)", progressPercent, progress.Status)
		} else if progress.Status != "" {
			message = progress.Status
		}

		// 调用进度回调
		progressCallback(progressPercent, message)

		logger.Infof(ctx,
			"Download progress for %s: %.2f%% - %s",
			modelName, progressPercent, message,
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

// GetCurrentConfig 获取当前系统配置信息
func (h *InitializationHandler) GetCurrentConfig(c *gin.Context) {
	ctx := c.Request.Context()

	logger.Info(ctx, "Getting current system configuration")

	// 设置租户上下文
	newCtx := context.WithValue(ctx, types.TenantIDContextKey, types.InitDefaultTenantID)

	// 获取模型信息
	models, err := h.modelService.ListModels(newCtx)
	if err != nil {
		logger.ErrorWithFields(ctx, err, nil)
		c.Error(errors.NewInternalServerError("获取模型列表失败: " + err.Error()))
		return
	}

	// 获取知识库信息
	kb, err := h.kbService.GetKnowledgeBaseByID(newCtx, types.InitDefaultKnowledgeBaseID)
	if err != nil {
		logger.ErrorWithFields(ctx, err, nil)
		c.Error(errors.NewInternalServerError("获取知识库信息失败: " + err.Error()))
		return
	}

	// 检查知识库是否有文件
	knowledgeList, err := h.knowledgeService.ListPagedKnowledgeByKnowledgeBaseID(newCtx,
		types.InitDefaultKnowledgeBaseID, &types.Pagination{
			Page:     1,
			PageSize: 1,
		})
	hasFiles := false
	if err == nil && knowledgeList != nil && knowledgeList.Total > 0 {
		hasFiles = true
	}

	// 构建配置响应
	config := buildConfigResponse(models, kb, hasFiles)

	logger.Info(ctx, "Current system configuration retrieved successfully")
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    config,
	})
}

// buildConfigResponse 构建配置响应数据
func buildConfigResponse(models []*types.Model,
	kb *types.KnowledgeBase, hasFiles bool,
) map[string]interface{} {
	config := map[string]interface{}{
		"hasFiles": hasFiles,
	}

	// 按类型分组模型
	for _, model := range models {
		switch model.Type {
		case types.ModelTypeKnowledgeQA:
			config["llm"] = map[string]interface{}{
				"source":    string(model.Source),
				"modelName": model.Name,
				"baseUrl":   model.Parameters.BaseURL,
				"apiKey":    model.Parameters.APIKey,
			}
		case types.ModelTypeEmbedding:
			config["embedding"] = map[string]interface{}{
				"source":    string(model.Source),
				"modelName": model.Name,
				"baseUrl":   model.Parameters.BaseURL,
				"apiKey":    model.Parameters.APIKey,
				"dimension": model.Parameters.EmbeddingParameters.Dimension,
			}
		case types.ModelTypeRerank:
			config["rerank"] = map[string]interface{}{
				"enabled":   true,
				"modelName": model.Name,
				"baseUrl":   model.Parameters.BaseURL,
				"apiKey":    model.Parameters.APIKey,
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
				"baseUrl":       model.Parameters.BaseURL,
				"apiKey":        model.Parameters.APIKey,
				"interfaceType": kb.VLMConfig.InterfaceType,
			}
		}
	}

	// 如果没有VLM模型，设置multimodal为disabled
	if config["multimodal"] == nil {
		config["multimodal"] = map[string]interface{}{
			"enabled": false,
		}
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

	return config
}

// RemoteModelCheckRequest 远程模型检查请求结构
type RemoteModelCheckRequest struct {
	ModelName string `json:"modelName" binding:"required"`
	BaseURL   string `json:"baseUrl" binding:"required"`
	APIKey    string `json:"apiKey"`
}

// CheckRemoteModel 检查远程API模型连接
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

	logger.Info(ctx,
		fmt.Sprintf(
			"Remote model check completed: modelName=%s, baseUrl=%s, available=%v, message=%s",
			req.ModelName, req.BaseURL, available, message,
		),
	)

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"available": available,
			"message":   message,
		},
	})
}

// TestEmbeddingModel 测试 Embedding 接口（本地或远程）是否可用
func (h *InitializationHandler) TestEmbeddingModel(c *gin.Context) {
	ctx := c.Request.Context()

	logger.Info(ctx, "Testing embedding model connectivity and functionality")

	var req struct {
		Source    string `json:"source" binding:"required"`
		ModelName string `json:"modelName" binding:"required"`
		BaseURL   string `json:"baseUrl"`
		APIKey    string `json:"apiKey"`
		Dimension int    `json:"dimension"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Error(ctx, "Failed to parse embedding test request", err)
		c.Error(errors.NewBadRequestError(err.Error()))
		return
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
	}

	emb, err := embedding.NewEmbedder(cfg)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{"model": req.ModelName})
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
		logger.ErrorWithFields(ctx, err, map[string]interface{}{"model": req.ModelName})
		c.JSON(http.StatusOK, gin.H{
			"success": true,
			"data":    gin.H{`available`: false, `message`: fmt.Sprintf("调用Embedding失败: %v", err), `dimension`: 0},
		})
		return
	}

	logger.Infof(ctx, "Embedding test succeeded, dim=%d", len(vec))
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    gin.H{`available`: true, `message`: fmt.Sprintf("测试成功，向量维度=%d", len(vec)), `dimension`: len(vec)},
	})
}

// checkRemoteModelConnection 检查远程模型连接的内部方法
func (h *InitializationHandler) checkRemoteModelConnection(ctx context.Context,
	model *types.Model,
) (bool, string) {
	// 这里需要根据实际情况实现远程API的连接检查
	// 可以发送一个简单的请求来验证连接和认证

	client := &http.Client{
		Timeout: 10 * time.Second,
	}

	// 根据不同的API类型构造测试请求
	testEndpoint := ""
	if model.Parameters.BaseURL != "" {
		// 常见的API端点模式
		if strings.Contains(model.Parameters.BaseURL, "openai.com") ||
			strings.Contains(model.Parameters.BaseURL, "api.openai.com") {
			testEndpoint = model.Parameters.BaseURL + "/models"
		} else if strings.Contains(model.Parameters.BaseURL, "v1") {
			testEndpoint = model.Parameters.BaseURL + "/models"
		} else {
			testEndpoint = model.Parameters.BaseURL + "/v1/models"
		}
	}

	req, err := http.NewRequestWithContext(ctx, "GET", testEndpoint, nil)
	if err != nil {
		return false, fmt.Sprintf("创建请求失败: %v", err)
	}

	// 添加认证头
	if model.Parameters.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+model.Parameters.APIKey)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return false, fmt.Sprintf("连接失败: %v", err)
	}
	defer resp.Body.Close()

	// 检查响应状态
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		// 连接成功，现在检查模型是否存在
		return true, "连接正常，请自行确保模型存在"
	} else if resp.StatusCode == 401 {
		return false, "认证失败，请检查API Key"
	} else if resp.StatusCode == 403 {
		return false, "权限不足，请检查API Key权限"
	} else if resp.StatusCode == 404 {
		return false, "API端点不存在，请检查Base URL"
	} else {
		return false, fmt.Sprintf("API返回错误状态: %d", resp.StatusCode)
	}
}

// checkModelExistence 检查指定模型是否在模型列表中存在
func (h *InitializationHandler) checkModelExistence(ctx context.Context,
	resp *http.Response, modelName string) (bool, string) {
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return true, "连接正常，但无法验证模型列表"
	}

	var modelsResp struct {
		Data []struct {
			ID     string `json:"id"`
			Object string `json:"object"`
		} `json:"data"`
		Object string `json:"object"`
	}

	// 尝试解析模型列表响应
	if err := json.Unmarshal(body, &modelsResp); err != nil {
		// 如果无法解析，可能是非标准API，只要连接成功就认为可用
		return true, "连接正常"
	}

	// 检查模型是否在列表中
	for _, model := range modelsResp.Data {
		if model.ID == modelName {
			return true, "连接正常，模型存在"
		}
	}

	// 模型不在列表中，返回可用的模型建议
	if len(modelsResp.Data) > 0 {
		availableModels := make([]string, 0, min(3, len(modelsResp.Data)))
		for i, model := range modelsResp.Data {
			if i >= 3 {
				break
			}
			availableModels = append(availableModels, model.ID)
		}
		return false, fmt.Sprintf("模型 '%s' 不存在，可用模型: %s", modelName, strings.Join(availableModels, ", "))
	}

	return false, fmt.Sprintf("模型 '%s' 不存在", modelName)
}

// min returns the minimum of two integers
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// checkRerankModelConnection 检查Rerank模型连接和功能的内部方法
func (h *InitializationHandler) checkRerankModelConnection(ctx context.Context,
	modelName, baseURL, apiKey string) (bool, string) {
	client := &http.Client{
		Timeout: 15 * time.Second,
	}

	// 构造重排API端点
	rerankEndpoint := baseURL + "/rerank"

	// Mock测试数据
	testQuery := "什么是人工智能？"
	testPassages := []string{
		"机器学习是人工智能的一个子领域，专注于算法和统计模型，使计算机系统能够通过经验自动改进。",
		"深度学习是机器学习的一个子集，使用人工神经网络来模拟人脑的工作方式。",
	}

	// 构造重排请求
	rerankRequest := map[string]interface{}{
		"model":                  modelName,
		"query":                  testQuery,
		"documents":              testPassages,
		"truncate_prompt_tokens": 512,
	}

	jsonData, err := json.Marshal(rerankRequest)
	if err != nil {
		return false, fmt.Sprintf("构造请求失败: %v", err)
	}

	logger.Infof(ctx, "Rerank request: %s, modelName=%s, baseURL=%s, apiKey=%s",
		string(jsonData), modelName, baseURL, apiKey)

	req, err := http.NewRequestWithContext(
		ctx, "POST", rerankEndpoint, strings.NewReader(string(jsonData)),
	)
	if err != nil {
		return false, fmt.Sprintf("创建请求失败: %v", err)
	}

	// 添加认证头
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return false, fmt.Sprintf("连接失败: %v", err)
	}
	defer resp.Body.Close()

	// 读取响应
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return false, fmt.Sprintf("读取响应失败: %v", err)
	}

	// 检查响应状态
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		// 尝试解析重排响应
		var rerankResp struct {
			Results []struct {
				Index          int     `json:"index"`
				Document       string  `json:"document"`
				RelevanceScore float64 `json:"relevance_score"`
			} `json:"results"`
		}

		if err := json.Unmarshal(body, &rerankResp); err != nil {
			// 如果无法解析标准重排响应，检查是否有其他格式
			return true, "连接正常，但响应格式非标准"
		}

		// 检查是否返回了重排结果
		if len(rerankResp.Results) > 0 {
			return true, fmt.Sprintf("重排功能正常，返回%d个结果", len(rerankResp.Results))
		} else {
			return false, "重排接口连接成功，但未返回重排结果"
		}
	} else if resp.StatusCode == 401 {
		return false, "认证失败，请检查API Key"
	} else if resp.StatusCode == 403 {
		return false, "权限不足，请检查API Key权限"
	} else if resp.StatusCode == 404 {
		return false, "重排API端点不存在，请检查Base URL"
	} else if resp.StatusCode == 422 {
		return false, fmt.Sprintf("请求参数错误: %s", string(body))
	} else {
		return false, fmt.Sprintf("API返回错误状态: %d, 响应: %s", resp.StatusCode, string(body))
	}
}

// CheckRerankModel 检查Rerank模型连接和功能
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

	logger.Info(ctx,
		fmt.Sprintf("Rerank model check completed: modelName=%s, baseUrl=%s, available=%v, message=%s",
			req.ModelName, req.BaseURL, available, message,
		),
	)

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

// TestMultimodalFunction 测试多模态功能
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
		logger.Infof(ctx, "COS config: Region=%s, Bucket=%s, App=%s, Prefix=%s",
			req.COSRegion, req.COSBucketName, req.COSAppID, req.COSPathPrefix)
		// 必填：SecretID/SecretKey/Region/BucketName/AppID；PathPrefix 可选
		if req.COSSecretID == "" || req.COSSecretKey == "" ||
			req.COSRegion == "" || req.COSBucketName == "" ||
			req.COSAppID == "" {
			logger.Error(ctx, "COS configuration is required")
			c.Error(errors.NewBadRequestError("COS配置信息不能为空"))
			return
		}
	case "minio":
		logger.Infof(ctx, "MinIO config: Bucket=%s, PathPrefix=%s", req.MinioBucketName, req.MinioPathPrefix)
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

	logger.Infof(ctx, "VLM config: Model=%s, URL=%s, HasKey=%v, Type=%s",
		req.VLMModel, req.VLMBaseURL, req.VLMAPIKey != "", req.VLMInterfaceType)

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

	// 验证文件大小 (10MB)
	if header.Size > 10*1024*1024 {
		logger.Error(ctx, "File size too large")
		c.Error(errors.NewBadRequestError("图片文件大小不能超过10MB"))
		return
	}
	logger.Infof(ctx, "Processing image: %s, size: %d bytes", header.Filename, header.Size)

	// 解析文档分割配置
	chunkSize, err := strconv.Atoi(req.ChunkSize)
	if err != nil || chunkSize < 100 || chunkSize > 10000 {
		chunkSize = 1000
	}

	chunkOverlap, err := strconv.Atoi(req.ChunkOverlap)
	if err != nil || chunkOverlap < 0 || chunkOverlap >= chunkSize {
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
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"vlm_model":    req.VLMModel,
			"vlm_base_url": req.VLMBaseURL,
			"filename":     header.Filename,
		})
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

	logger.Info(ctx, fmt.Sprintf("Multimodal test completed successfully in %dms", processingTime))

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
	chunkSize, chunkOverlap int, separators []string,
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
			ChunkSize:        int32(chunkSize),
			ChunkOverlap:     int32(chunkOverlap),
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
