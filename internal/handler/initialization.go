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
}

// InitializeByKB 根据知识库ID执行配置更新
func (h *InitializationHandler) InitializeByKB(c *gin.Context) {
	ctx := c.Request.Context()
	kbIdStr := c.Param("kbId")

	logger.Infof(ctx, "Starting knowledge base configuration update, kbId: %s", kbIdStr)

	var req InitializationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Error(ctx, "Failed to parse initialization request", err)
		c.Error(errors.NewBadRequestError(err.Error()))
		return
	}

	// 获取指定知识库信息
	kb, err := h.kbService.GetKnowledgeBaseByID(ctx, kbIdStr)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{"kbId": kbIdStr})
		c.Error(errors.NewInternalServerError("获取知识库信息失败: " + err.Error()))
		return
	}

	if kb == nil {
		logger.Error(ctx, "Knowledge base not found", "kbId", kbIdStr)
		c.Error(errors.NewNotFoundError("知识库不存在"))
		return
	}

	// 验证多模态配置（如果启用）
	if req.Multimodal.Enabled {
		storageType := strings.ToLower(req.Multimodal.StorageType)
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
			c.Error(errors.NewBadRequestError("Rerank配置不完整"))
			return
		}
	}

	// 验证Node Extractor配置（如果启用）
	if strings.ToLower(os.Getenv("NEO4J_ENABLE")) != "true" && req.NodeExtract.Enabled {
		logger.Error(ctx, "Node Extractor configuration incomplete")
		c.Error(errors.NewBadRequestError("请正确配置环境变量NEO4J_ENABLE"))
		return
	}
	if req.NodeExtract.Enabled {
		if req.NodeExtract.Text == "" || len(req.NodeExtract.Tags) == 0 {
			logger.Error(ctx, "Node Extractor configuration incomplete")
			c.Error(errors.NewBadRequestError("Node Extractor配置不完整"))
			return
		}
		if len(req.NodeExtract.Nodes) == 0 || len(req.NodeExtract.Relations) == 0 {
			logger.Error(ctx, "Node Extractor configuration incomplete")
			c.Error(errors.NewBadRequestError("请先提取实体和关系"))
			return
		}
	}

	// 处理模型创建/更新
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
			description: "VLM Model",
			baseURL:     req.Multimodal.VLM.BaseURL,
			apiKey:      req.Multimodal.VLM.APIKey,
		})
	}

	// 处理模型
	var processedModels []*types.Model
	for _, modelInfo := range modelsToProcess {
		model := &types.Model{
			Type:        modelInfo.modelType,
			Name:        modelInfo.name,
			Source:      modelInfo.source,
			Description: modelInfo.description,
			Parameters: types.ModelParameters{
				BaseURL: modelInfo.baseURL,
				APIKey:  modelInfo.apiKey,
			},
			IsDefault: false,
			Status:    types.ModelStatusActive,
		}

		if modelInfo.modelType == types.ModelTypeEmbedding {
			model.Parameters.EmbeddingParameters = types.EmbeddingParameters{
				Dimension: modelInfo.dimension,
			}
		}

		// 检查模型是否已存在
		existingModel, err := h.modelService.GetModelByID(ctx, model.ID)
		if err == nil && existingModel != nil {
			// 更新现有模型
			existingModel.Name = model.Name
			existingModel.Source = model.Source
			existingModel.Description = model.Description
			existingModel.Parameters = model.Parameters
			existingModel.UpdatedAt = time.Now()

			err = h.modelService.UpdateModel(ctx, existingModel)
			if err != nil {
				logger.ErrorWithFields(ctx, err, map[string]interface{}{
					"model_id": model.ID,
					"kb_id":    kbIdStr,
				})
				c.Error(errors.NewInternalServerError("更新模型失败: " + err.Error()))
				return
			}
			processedModels = append(processedModels, existingModel)
		} else {
			// 创建新模型
			err = h.modelService.CreateModel(ctx, model)
			if err != nil {
				logger.ErrorWithFields(ctx, err, map[string]interface{}{
					"model_id": model.ID,
					"kb_id":    kbIdStr,
				})
				c.Error(errors.NewInternalServerError("创建模型失败: " + err.Error()))
				return
			}
			processedModels = append(processedModels, model)
		}
	}

	// 找到模型ID
	var embeddingModelID, llmModelID, rerankModelID, vlmModelID string
	for _, model := range processedModels {
		if model.Type == types.ModelTypeEmbedding {
			embeddingModelID = model.ID
		}
		if model.Type == types.ModelTypeKnowledgeQA {
			llmModelID = model.ID
		}
		if model.Type == types.ModelTypeRerank {
			rerankModelID = model.ID
		}
		if model.Type == types.ModelTypeVLLM {
			vlmModelID = model.ID
		}
	}

	// 更新知识库配置
	kb.SummaryModelID = llmModelID
	kb.EmbeddingModelID = embeddingModelID
	if req.Rerank.Enabled {
		kb.RerankModelID = rerankModelID
	} else {
		kb.RerankModelID = ""
	}

	// 更新文档分割配置
	kb.ChunkingConfig = types.ChunkingConfig{
		ChunkSize:    req.DocumentSplitting.ChunkSize,
		ChunkOverlap: req.DocumentSplitting.ChunkOverlap,
		Separators:   req.DocumentSplitting.Separators,
	}

	// 更新多模态配置
	if req.Multimodal.Enabled {
		kb.ChunkingConfig.EnableMultimodal = true
		kb.VLMModelID = vlmModelID
		kb.VLMConfig = types.VLMConfig{
			ModelName:     req.Multimodal.VLM.ModelName,
			BaseURL:       req.Multimodal.VLM.BaseURL,
			APIKey:        req.Multimodal.VLM.APIKey,
			InterfaceType: req.Multimodal.VLM.InterfaceType,
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
		kb.VLMModelID = ""
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

	err = h.kbRepository.UpdateKnowledgeBase(ctx, kb)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{"kbId": kbIdStr})
		c.Error(errors.NewInternalServerError("更新知识库配置失败: " + err.Error()))
		return
	}

	logger.Info(ctx, "Knowledge base configuration updated successfully", "kbId", kbIdStr)
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "知识库配置更新成功",
		"data": gin.H{
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

// GetCurrentConfigByKB 根据知识库ID获取配置信息
func (h *InitializationHandler) GetCurrentConfigByKB(c *gin.Context) {
	ctx := c.Request.Context()
	kbIdStr := c.Param("kbId")

	logger.Info(ctx, "Getting configuration for knowledge base", "kbId", kbIdStr)

	// 获取指定知识库信息
	kb, err := h.kbService.GetKnowledgeBaseByID(ctx, kbIdStr)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{"kbId": kbIdStr})
		c.Error(errors.NewInternalServerError("获取知识库信息失败: " + err.Error()))
		return
	}

	if kb == nil {
		logger.Error(ctx, "Knowledge base not found", "kbId", kbIdStr)
		c.Error(errors.NewNotFoundError("知识库不存在"))
		return
	}

	// 根据知识库的模型ID获取特定模型
	var models []*types.Model
	modelIDs := []string{
		kb.EmbeddingModelID,
		kb.SummaryModelID,
		kb.RerankModelID,
		kb.VLMModelID,
	}

	for _, modelID := range modelIDs {
		if modelID != "" {
			model, err := h.modelService.GetModelByID(ctx, modelID)
			if err != nil {
				logger.Warn(ctx, "Failed to get model", "kbId", kbIdStr, "modelId", modelID, "error", err)
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
		})
	hasFiles := false
	if err == nil && knowledgeList != nil && knowledgeList.Total > 0 {
		hasFiles = true
	}

	// 构建配置响应
	config := buildConfigResponse(models, kb, hasFiles)

	logger.Info(ctx, "Knowledge base configuration retrieved successfully", "kbId", kbIdStr)
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

	if kb.ExtractConfig != nil {
		config["nodeExtract"] = map[string]interface{}{
			"enabled":   true,
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
	chatInstance, err := chat.NewChat(chatConfig)
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
			return false, "权限不足，请检查API Key权限"
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

// TextRelationExtractionRequest 文本关系提取请求结构
type TextRelationExtractionRequest struct {
	Text      string    `json:"text" binding:"required"`
	Tags      []string  `json:"tags" binding:"required"`
	LLMConfig LLMConfig `json:"llmConfig"`
}

type LLMConfig struct {
	Source    string `json:"source"`
	ModelName string `json:"modelName"`
	BaseUrl   string `json:"baseUrl"`
	ApiKey    string `json:"apiKey"`
}

// TextRelationExtractionResponse 文本关系提取响应结构
type TextRelationExtractionResponse struct {
	Nodes     []*types.GraphNode     `json:"nodes"`
	Relations []*types.GraphRelation `json:"relations"`
}

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
		c.JSON(http.StatusOK, gin.H{
			"success": true,
			"data": gin.H{
				"success": false,
				"message": err.Error(),
			},
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    result,
	})
}

// extractRelationsFromText 从文本中提取关系
func (h *InitializationHandler) extractRelationsFromText(ctx context.Context, text string, tags []string, llm LLMConfig) (*TextRelationExtractionResponse, error) {
	chatModel, err := chat.NewChat(&chat.ChatConfig{
		ModelID:   "initialization",
		APIKey:    llm.ApiKey,
		BaseURL:   llm.BaseUrl,
		ModelName: llm.ModelName,
		Source:    types.ModelSource(llm.Source),
	})
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

type FabriTextRequest struct {
	Tags      []string  `json:"tags"`
	LLMConfig LLMConfig `json:"llmConfig"`
}

type FabriTextResponse struct {
	Text string `json:"text"`
}

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
		c.JSON(http.StatusOK, gin.H{
			"success": true,
			"data": gin.H{
				"success": false,
				"message": err.Error(),
			},
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    FabriTextResponse{Text: result},
	})
}

func (h *InitializationHandler) fabriText(ctx context.Context, tags []string, llm LLMConfig) (string, error) {
	chatModel, err := chat.NewChat(&chat.ChatConfig{
		ModelID:   "initialization",
		APIKey:    llm.ApiKey,
		BaseURL:   llm.BaseUrl,
		ModelName: llm.ModelName,
		Source:    types.ModelSource(llm.Source),
	})
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

type FabriTagRequest struct {
	LLMConfig LLMConfig `json:"llmConfig"`
}

type FabriTagResponse struct {
	Tags []string `json:"tags"`
}

var tagOptions = []string{
	"内容", "文化", "人物", "事件", "时间", "地点", "作品", "作者", "关系", "属性",
}

func (h *InitializationHandler) FabriTag(c *gin.Context) {
	tagRandom := RandomSelect(tagOptions, rand.Intn(len(tagOptions)-1)+1)
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    FabriTagResponse{Tags: tagRandom},
	})
}

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
