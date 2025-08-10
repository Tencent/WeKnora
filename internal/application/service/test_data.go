// Package service 提供应用程序的核心业务逻辑服务层
// 此包包含了知识库管理、用户租户管理、模型服务等核心功能实现
package service

import (
	"context"
	"fmt"
	"os"
	"strconv"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/models/embedding"
	"github.com/Tencent/WeKnora/internal/models/rerank"
	"github.com/Tencent/WeKnora/internal/models/utils/ollama"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// TestDataService 测试数据服务
// 负责初始化测试环境所需的数据，包括创建测试租户、测试知识库
// 以及配置必要的模型服务实例
type TestDataService struct {
	config        *config.Config                     // 应用程序配置
	kbRepo        interfaces.KnowledgeBaseRepository // 知识库存储库接口
	tenantService interfaces.TenantService           // 租户服务接口
	ollamaService *ollama.OllamaService              // Ollama模型服务
	modelService  interfaces.ModelService            // 模型服务接口
	EmbedModel    embedding.Embedder                 // 嵌入模型实例
	RerankModel   rerank.Reranker                    // 重排模型实例
	LLMModel      chat.Chat                          // 大语言模型实例
}

// NewTestDataService 创建测试数据服务
// 注入所需的依赖服务和组件
func NewTestDataService(
	config *config.Config,
	kbRepo interfaces.KnowledgeBaseRepository,
	tenantService interfaces.TenantService,
	ollamaService *ollama.OllamaService,
	modelService interfaces.ModelService,
) *TestDataService {
	return &TestDataService{
		config:        config,
		kbRepo:        kbRepo,
		tenantService: tenantService,
		ollamaService: ollamaService,
		modelService:  modelService,
	}
}

// initTenant 初始化测试租户
// 通过环境变量获取租户ID，如果租户不存在则创建新租户，否则更新现有租户
// 同时配置租户的检索引擎参数
func (s *TestDataService) initTenant(ctx context.Context) error {
	logger.Info(ctx, "Start initializing test tenant")

	// 从环境变量获取租户ID
	tenantID := os.Getenv("INIT_TEST_TENANT_ID")
	logger.Infof(ctx, "Test tenant ID from environment: %s", tenantID)

	// 将字符串ID转换为uint64
	tenantIDUint, err := strconv.ParseUint(tenantID, 10, 64)
	if err != nil {
		logger.Errorf(ctx, "Failed to parse tenant ID: %v", err)
		return err
	}

	// 创建租户配置
	tenantConfig := &types.Tenant{
		Name:        "Test Tenant",
		Description: "Test Tenant for Testing",
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

	// 获取或创建测试租户
	logger.Infof(ctx, "Attempting to get tenant with ID: %d", tenantIDUint)
	tenant, err := s.tenantService.GetTenantByID(ctx, uint(tenantIDUint))
	if err != nil {
		// 租户不存在，创建新租户
		logger.Info(ctx, "Tenant not found, creating a new test tenant")
		tenant, err = s.tenantService.CreateTenant(ctx, tenantConfig)
		if err != nil {
			logger.Errorf(ctx, "Failed to create tenant: %v", err)
			return err
		}
		logger.Infof(ctx, "Created new test tenant with ID: %d", tenant.ID)
	} else {
		// 租户存在，更新检索引擎配置
		logger.Info(ctx, "Test tenant found, updating retriever engines")
		tenant.RetrieverEngines = tenantConfig.RetrieverEngines
		tenant, err = s.tenantService.UpdateTenant(ctx, tenant)
		if err != nil {
			logger.Errorf(ctx, "Failed to update tenant: %v", err)
			return err
		}
		logger.Info(ctx, "Test tenant updated successfully")
	}

	logger.Infof(ctx, "Test tenant configured - ID: %d, Name: %s, API Key: %s",
		tenant.ID, tenant.Name, tenant.APIKey)
	return nil
}

// initKnowledgeBase 初始化测试知识库
// 从环境变量获取知识库ID，创建或更新知识库
// 配置知识库的分块策略、嵌入模型和摘要模型
func (s *TestDataService) initKnowledgeBase(ctx context.Context) error {
	logger.Info(ctx, "Start initializing test knowledge base")

	// 检查上下文中的租户ID
	if ctx.Value(types.TenantIDContextKey).(uint) == 0 {
		logger.Warn(ctx, "Tenant ID is 0, skipping knowledge base initialization")
		return nil
	}

	// 从环境变量获取知识库ID
	knowledgeBaseID := os.Getenv("INIT_TEST_KNOWLEDGE_BASE_ID")
	logger.Infof(ctx, "Test knowledge base ID from environment: %s", knowledgeBaseID)

	// 创建知识库配置
	kbConfig := &types.KnowledgeBase{
		ID:          knowledgeBaseID,
		Name:        "Test Knowledge Base",
		Description: "Knowledge Base for Testing",
		TenantID:    ctx.Value(types.TenantIDContextKey).(uint),
		ChunkingConfig: types.ChunkingConfig{
			ChunkSize:        s.config.KnowledgeBase.ChunkSize,
			ChunkOverlap:     s.config.KnowledgeBase.ChunkOverlap,
			Separators:       s.config.KnowledgeBase.SplitMarkers,
			EnableMultimodal: s.config.KnowledgeBase.ImageProcessing.EnableMultimodal,
		},
		EmbeddingModelID: s.EmbedModel.GetModelID(),
		SummaryModelID:   s.LLMModel.GetModelID(),
		RerankModelID:    s.RerankModel.GetModelID(),
	}

	// 初始化测试知识库
	logger.Info(ctx, "Attempting to get existing knowledge base")
	_, err := s.kbRepo.GetKnowledgeBaseByID(ctx, knowledgeBaseID)
	if err != nil {
		// 知识库不存在，创建新知识库
		logger.Info(ctx, "Knowledge base not found, creating a new one")
		logger.Infof(ctx, "Creating knowledge base with ID: %s, tenant ID: %d",
			kbConfig.ID, kbConfig.TenantID)

		if err := s.kbRepo.CreateKnowledgeBase(ctx, kbConfig); err != nil {
			logger.Errorf(ctx, "Failed to create knowledge base: %v", err)
			return err
		}
		logger.Info(ctx, "Knowledge base created successfully")
	} else {
		// 知识库存在，更新配置
		logger.Info(ctx, "Knowledge base found, updating configuration")
		logger.Infof(ctx, "Updating knowledge base with ID: %s", kbConfig.ID)

		err = s.kbRepo.UpdateKnowledgeBase(ctx, kbConfig)
		if err != nil {
			logger.Errorf(ctx, "Failed to update knowledge base: %v", err)
			return err
		}
		logger.Info(ctx, "Knowledge base updated successfully")
	}

	logger.Infof(ctx, "Test knowledge base configured - ID: %s, Name: %s", kbConfig.ID, kbConfig.Name)
	return nil
}

// InitializeTestData 初始化测试数据
// 这是对外暴露的主要方法，负责协调所有测试数据的初始化过程
// 包括初始化租户、嵌入模型、重排模型、LLM模型和知识库
func (s *TestDataService) InitializeTestData(ctx context.Context) error {
	logger.Info(ctx, "Start initializing test data")

	// 从环境变量获取租户ID
	tenantID := os.Getenv("INIT_TEST_TENANT_ID")
	logger.Infof(ctx, "Test tenant ID from environment: %s", tenantID)

	// 解析租户ID
	tenantIDUint, err := strconv.ParseUint(tenantID, 10, 64)
	if err != nil {
		// 解析失败时使用默认值0
		logger.Warn(ctx, "Failed to parse tenant ID, using default value 0")
		tenantIDUint = 0
	} else {
		// 初始化租户
		logger.Info(ctx, "Initializing tenant")
		err = s.initTenant(ctx)
		if err != nil {
			logger.Errorf(ctx, "Failed to initialize tenant: %v", err)
			return err
		}
		logger.Info(ctx, "Tenant initialized successfully")
	}

	// 创建带有租户ID的新上下文
	newCtx := context.Background()
	newCtx = context.WithValue(newCtx, types.TenantIDContextKey, uint(tenantIDUint))
	logger.Infof(ctx, "Created new context with tenant ID: %d", tenantIDUint)

	// 初始化模型
	modelInitFuncs := []struct {
		name string
		fn   func(context.Context) error
	}{
		{"embedding model", s.initEmbeddingModel},
		{"rerank model", s.initRerankModel},
		{"LLM model", s.initLLMModel},
	}

	for _, initFunc := range modelInitFuncs {
		logger.Infof(ctx, "Initializing %s", initFunc.name)
		if err := initFunc.fn(newCtx); err != nil {
			logger.Errorf(ctx, "Failed to initialize %s: %v", initFunc.name, err)
			return err
		}
		logger.Infof(ctx, "%s initialized successfully", initFunc.name)
	}

	// 初始化知识库
	logger.Info(ctx, "Initializing knowledge base")
	if err := s.initKnowledgeBase(newCtx); err != nil {
		logger.Errorf(ctx, "Failed to initialize knowledge base: %v", err)
		return err
	}
	logger.Info(ctx, "Knowledge base initialized successfully")

	logger.Info(ctx, "Test data initialization completed")
	return nil
}

// getEnvOrError 获取环境变量值，如果不存在则返回错误
func (s *TestDataService) getEnvOrError(name string) (string, error) {
	value := os.Getenv(name)
	if value == "" {
		return "", fmt.Errorf("%s environment variable is not set", name)
	}
	return value, nil
}

// updateOrCreateModel 更新或创建模型
func (s *TestDataService) updateOrCreateModel(ctx context.Context, modelConfig *types.Model) error {
	model, err := s.modelService.GetModelByID(ctx, modelConfig.ID)
	if err != nil {
		// 模型不存在，创建新模型
		return s.modelService.CreateModel(ctx, modelConfig)
	}

	// 模型存在，更新属性
	model.TenantID = modelConfig.TenantID
	model.Name = modelConfig.Name
	model.Source = modelConfig.Source
	model.Type = modelConfig.Type
	model.Parameters = modelConfig.Parameters
	model.Status = modelConfig.Status

	return s.modelService.UpdateModel(ctx, model)
}

// initEmbeddingModel 初始化嵌入模型
func (s *TestDataService) initEmbeddingModel(ctx context.Context) error {
	// 从环境变量获取模型参数
	modelName, err := s.getEnvOrError("INIT_EMBEDDING_MODEL_NAME")
	if err != nil {
		return err
	}

	dimensionStr := os.Getenv("INIT_EMBEDDING_MODEL_DIMENSION")
	dimension, err := strconv.Atoi(dimensionStr)
	if err != nil || dimension == 0 {
		return fmt.Errorf("invalid embedding model dimension: %s", dimensionStr)
	}

	baseURL := os.Getenv("INIT_EMBEDDING_MODEL_BASE_URL")
	apiKey := os.Getenv("INIT_EMBEDDING_MODEL_API_KEY")

	// 确定模型来源
	source := types.ModelSourceRemote
	if baseURL == "" {
		source = types.ModelSourceLocal
	}

	// 确定模型ID
	modelID := os.Getenv("INIT_EMBEDDING_MODEL_ID")
	if modelID == "" {
		modelID = fmt.Sprintf("builtin:%s:%d", modelName, dimension)
	}

	// 创建嵌入模型实例
	s.EmbedModel, err = embedding.NewEmbedder(embedding.Config{
		Source:     source,
		BaseURL:    baseURL,
		ModelName:  modelName,
		APIKey:     apiKey,
		Dimensions: dimension,
		ModelID:    modelID,
	})
	if err != nil {
		return fmt.Errorf("failed to create embedder: %w", err)
	}

	// 如果是本地模型，使用Ollama拉取模型
	if source == types.ModelSourceLocal && s.ollamaService != nil {
		if err := s.ollamaService.PullModel(context.Background(), modelName); err != nil {
			return fmt.Errorf("failed to pull embedding model: %w", err)
		}
	}

	// 创建模型配置
	modelConfig := &types.Model{
		ID:       modelID,
		TenantID: ctx.Value(types.TenantIDContextKey).(uint),
		Name:     modelName,
		Source:   source,
		Type:     types.ModelTypeEmbedding,
		Parameters: types.ModelParameters{
			BaseURL: baseURL,
			APIKey:  apiKey,
			EmbeddingParameters: types.EmbeddingParameters{
				Dimension: dimension,
			},
		},
		Status: "active",
	}

	// 更新或创建模型
	return s.updateOrCreateModel(ctx, modelConfig)
}

// initRerankModel 初始化重排模型
func (s *TestDataService) initRerankModel(ctx context.Context) error {
	// 从环境变量获取模型参数
	modelName, err := s.getEnvOrError("INIT_RERANK_MODEL_NAME")
	if err != nil {
		logger.Warnf(ctx, "Skip Rerank Model: %v", err)
		return nil
	}

	baseURL, err := s.getEnvOrError("INIT_RERANK_MODEL_BASE_URL")
	if err != nil {
		return err
	}

	apiKey := os.Getenv("INIT_RERANK_MODEL_API_KEY")
	modelID := fmt.Sprintf("builtin:%s:rerank:%s", types.ModelSourceRemote, modelName)

	// 创建重排模型实例
	s.RerankModel, err = rerank.NewReranker(&rerank.RerankerConfig{
		Source:    types.ModelSourceRemote,
		BaseURL:   baseURL,
		ModelName: modelName,
		APIKey:    apiKey,
		ModelID:   modelID,
	})
	if err != nil {
		return fmt.Errorf("failed to create reranker: %w", err)
	}

	// 创建模型配置
	modelConfig := &types.Model{
		ID:       modelID,
		TenantID: ctx.Value(types.TenantIDContextKey).(uint),
		Name:     modelName,
		Source:   types.ModelSourceRemote,
		Type:     types.ModelTypeRerank,
		Parameters: types.ModelParameters{
			BaseURL: baseURL,
			APIKey:  apiKey,
		},
		Status: "active",
	}

	// 更新或创建模型
	return s.updateOrCreateModel(ctx, modelConfig)
}

// initLLMModel 初始化大语言模型
func (s *TestDataService) initLLMModel(ctx context.Context) error {
	// 从环境变量获取模型参数
	modelName, err := s.getEnvOrError("INIT_LLM_MODEL_NAME")
	if err != nil {
		return err
	}

	baseURL := os.Getenv("INIT_LLM_MODEL_BASE_URL")
	apiKey := os.Getenv("INIT_LLM_MODEL_API_KEY")

	// 确定模型来源
	source := types.ModelSourceRemote
	if baseURL == "" {
		source = types.ModelSourceLocal
	}

	// 确定模型ID
	modelID := fmt.Sprintf("builtin:%s:llm:%s", source, modelName)

	// 创建大语言模型实例
	s.LLMModel, err = chat.NewChat(&chat.ChatConfig{
		Source:    source,
		BaseURL:   baseURL,
		ModelName: modelName,
		APIKey:    apiKey,
		ModelID:   modelID,
	})
	if err != nil {
		return fmt.Errorf("failed to create llm: %w", err)
	}

	// 如果是本地模型，使用Ollama拉取模型
	if source == types.ModelSourceLocal && s.ollamaService != nil {
		if err := s.ollamaService.PullModel(context.Background(), modelName); err != nil {
			return fmt.Errorf("failed to pull llm model: %w", err)
		}
	}

	// 创建模型配置
	modelConfig := &types.Model{
		ID:       modelID,
		TenantID: ctx.Value(types.TenantIDContextKey).(uint),
		Name:     modelName,
		Source:   source,
		Type:     types.ModelTypeKnowledgeQA,
		Parameters: types.ModelParameters{
			BaseURL: baseURL,
			APIKey:  apiKey,
		},
		Status: "active",
	}

	// 更新或创建模型
	return s.updateOrCreateModel(ctx, modelConfig)
}
