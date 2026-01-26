package service

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"regexp"
	"runtime"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/docreader/client"
	"github.com/Tencent/WeKnora/docreader/proto"
	"github.com/Tencent/WeKnora/internal/application/service/retriever"
	"github.com/Tencent/WeKnora/internal/config"
	werrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/models/embedding"
	"github.com/Tencent/WeKnora/internal/tracing"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	secutils "github.com/Tencent/WeKnora/internal/utils"
	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/redis/go-redis/v9"
	"go.opentelemetry.io/otel/attribute"
	"golang.org/x/sync/errgroup"
)

// Error definitions for knowledge service operations
var (
	// ErrInvalidFileType is returned when an unsupported file type is provided
	ErrInvalidFileType = errors.New("unsupported file type")
	// ErrInvalidURL is returned when an invalid URL is provided
	ErrInvalidURL = errors.New("invalid URL")
	// ErrChunkNotFound is returned when a requested chunk cannot be found
	ErrChunkNotFound = errors.New("chunk not found")
	// ErrDuplicateFile is returned when trying to add a file that already exists
	ErrDuplicateFile = errors.New("file already exists")
	// ErrDuplicateURL is returned when trying to add a URL that already exists
	ErrDuplicateURL = errors.New("URL already exists")
	// ErrImageNotParse is returned when trying to update image information without enabling multimodel
	ErrImageNotParse = errors.New("image not parse without enable multimodel")
)

// knowledgeService implements the knowledge service interface
// service 实现知识服务接口
type knowledgeService struct {
	config          *config.Config
	retrieveEngine  interfaces.RetrieveEngineRegistry
	repo            interfaces.KnowledgeRepository
	kbService       interfaces.KnowledgeBaseService
	tenantRepo      interfaces.TenantRepository
	docReaderClient *client.Client
	chunkService    interfaces.ChunkService
	chunkRepo       interfaces.ChunkRepository
	tagRepo         interfaces.KnowledgeTagRepository
	tagService      interfaces.KnowledgeTagService
	fileSvc         interfaces.FileService
	modelService    interfaces.ModelService
	task            *asynq.Client
	graphEngine     interfaces.RetrieveGraphRepository
	redisClient     *redis.Client
}

const (
	manualContentMaxLength = 200000
	manualFileExtension    = ".md"
	faqImportBatchSize     = 50 // 每批处理的FAQ条目数
)

// NewKnowledgeService creates a new knowledge service instance
func NewKnowledgeService(
	config *config.Config,
	repo interfaces.KnowledgeRepository,
	docReaderClient *client.Client,
	kbService interfaces.KnowledgeBaseService,
	tenantRepo interfaces.TenantRepository,
	chunkService interfaces.ChunkService,
	chunkRepo interfaces.ChunkRepository,
	tagRepo interfaces.KnowledgeTagRepository,
	tagService interfaces.KnowledgeTagService,
	fileSvc interfaces.FileService,
	modelService interfaces.ModelService,
	task *asynq.Client,
	graphEngine interfaces.RetrieveGraphRepository,
	retrieveEngine interfaces.RetrieveEngineRegistry,
	redisClient *redis.Client,
) (interfaces.KnowledgeService, error) {
	return &knowledgeService{
		config:          config,
		repo:            repo,
		kbService:       kbService,
		tenantRepo:      tenantRepo,
		docReaderClient: docReaderClient,
		chunkService:    chunkService,
		chunkRepo:       chunkRepo,
		tagRepo:         tagRepo,
		tagService:      tagService,
		fileSvc:         fileSvc,
		modelService:    modelService,
		task:            task,
		graphEngine:     graphEngine,
		retrieveEngine:  retrieveEngine,
		redisClient:     redisClient,
	}, nil
}

// GetRepository gets the knowledge repository
// Parameters:
//   - ctx: Context with authentication and request information
//
// Returns:
//   - interfaces.KnowledgeRepository: Knowledge repository
func (s *knowledgeService) GetRepository() interfaces.KnowledgeRepository {
	return s.repo
}

// isKnowledgeDeleting checks if a knowledge entry is being deleted.
// This is used to prevent async tasks from conflicting with deletion operations.
func (s *knowledgeService) isKnowledgeDeleting(ctx context.Context, tenantID uint64, knowledgeID string) bool {
	knowledge, err := s.repo.GetKnowledgeByID(ctx, tenantID, knowledgeID)
	if err != nil {
		// If we can't find the knowledge, assume it's deleted
		logger.Warnf(ctx, "Failed to check knowledge deletion status (assuming deleted): %v", err)
		return true
	}
	if knowledge == nil {
		return true
	}
	return knowledge.ParseStatus == types.ParseStatusDeleting
}

// CreateKnowledgeFromFile creates a knowledge entry from an uploaded file
func (s *knowledgeService) CreateKnowledgeFromFile(ctx context.Context,
	kbID string, file *multipart.FileHeader, metadata map[string]string, enableMultimodel *bool, customFileName string, tagID string,
) (*types.Knowledge, error) {
	logger.Info(ctx, "Start creating knowledge from file")

	// Use custom filename if provided, otherwise use original filename
	fileName := file.Filename
	if customFileName != "" {
		fileName = customFileName
		logger.Infof(ctx, "Using custom filename: %s (original: %s)", customFileName, file.Filename)
	}

	logger.Infof(ctx, "Knowledge base ID: %s, file: %s", kbID, fileName)

	// Get knowledge base configuration
	logger.Info(ctx, "Getting knowledge base configuration")
	kb, err := s.kbService.GetKnowledgeBaseByID(ctx, kbID)
	if err != nil {
		logger.Errorf(ctx, "Failed to get knowledge base: %v", err)
		return nil, err
	}

	// 检查多模态配置完整性 - 只在图片文件时校验
	// 检查是否为图片文件
	if !IsImageType(getFileType(fileName)) {
		logger.Info(ctx, "Non-image file with multimodal enabled, skipping COS/VLM validation")
	} else {
		// 检查COS配置
		switch kb.StorageConfig.Provider {
		case "cos":
			if kb.StorageConfig.SecretID == "" || kb.StorageConfig.SecretKey == "" ||
				kb.StorageConfig.Region == "" || kb.StorageConfig.BucketName == "" ||
				kb.StorageConfig.AppID == "" {
				logger.Error(ctx, "COS configuration incomplete for image multimodal processing")
				return nil, werrors.NewBadRequestError("上传图片文件需要完整的对象存储配置信息, 请前往系统设置页面进行补全")
			}
		case "minio":
			if kb.StorageConfig.BucketName == "" {
				logger.Error(ctx, "MinIO configuration incomplete for image multimodal processing")
				return nil, werrors.NewBadRequestError("上传图片文件需要完整的对象存储配置信息, 请前往系统设置页面进行补全")
			}
		}

		// 检查VLM配置
		if !kb.VLMConfig.Enabled || kb.VLMConfig.ModelID == "" {
			logger.Error(ctx, "VLM model is not configured")
			return nil, werrors.NewBadRequestError("上传图片文件需要设置VLM模型")
		}

		logger.Info(ctx, "Image multimodal configuration validation passed")
	}

	// Validate file type
	logger.Infof(ctx, "Checking file type: %s", fileName)
	if !isValidFileType(fileName) {
		logger.Error(ctx, "Invalid file type")
		return nil, ErrInvalidFileType
	}

	// Calculate file hash for deduplication
	logger.Info(ctx, "Calculating file hash")
	hash, err := calculateFileHash(file)
	if err != nil {
		logger.Errorf(ctx, "Failed to calculate file hash: %v", err)
		return nil, err
	}

	// Check if file already exists
	tenantID := ctx.Value(types.TenantIDContextKey).(uint64)
	logger.Infof(ctx, "Checking if file exists, tenant ID: %d", tenantID)
	exists, existingKnowledge, err := s.repo.CheckKnowledgeExists(ctx, tenantID, kbID, &types.KnowledgeCheckParams{
		Type:     "file",
		FileName: fileName,
		FileSize: file.Size,
		FileHash: hash,
	})
	if err != nil {
		logger.Errorf(ctx, "Failed to check knowledge existence: %v", err)
		return nil, err
	}
	if exists {
		logger.Infof(ctx, "File already exists: %s", fileName)
		// Update creation time for existing knowledge
		if err := s.repo.UpdateKnowledgeColumn(ctx, existingKnowledge.ID, "created_at", time.Now()); err != nil {
			logger.Errorf(ctx, "Failed to update existing knowledge: %v", err)
			return nil, err
		}
		return existingKnowledge, types.NewDuplicateFileError(existingKnowledge)
	}

	// Check storage quota
	tenantInfo := ctx.Value(types.TenantInfoContextKey).(*types.Tenant)
	if tenantInfo.StorageQuota > 0 && tenantInfo.StorageUsed >= tenantInfo.StorageQuota {
		logger.Error(ctx, "Storage quota exceeded")
		return nil, types.NewStorageQuotaExceededError()
	}

	// Convert metadata to JSON format if provided
	var metadataJSON types.JSON
	if metadata != nil {
		metadataBytes, err := json.Marshal(metadata)
		if err != nil {
			logger.Errorf(ctx, "Failed to marshal metadata: %v", err)
			return nil, err
		}
		metadataJSON = types.JSON(metadataBytes)
	}

	// 验证文件名安全性
	safeFilename, isValid := secutils.ValidateInput(fileName)
	if !isValid {
		logger.Errorf(ctx, "Invalid filename: %s", fileName)
		return nil, werrors.NewValidationError("文件名包含非法字符")
	}

	// Create knowledge record
	logger.Info(ctx, "Creating knowledge record")
	knowledge := &types.Knowledge{
		TenantID:         tenantID,
		KnowledgeBaseID:  kbID,
		TagID:            tagID, // 设置分类ID，用于知识分类管理
		Type:             "file",
		Title:            safeFilename,
		FileName:         safeFilename,
		FileType:         getFileType(safeFilename),
		FileSize:         file.Size,
		FileHash:         hash,
		ParseStatus:      "pending",
		EnableStatus:     "disabled",
		CreatedAt:        time.Now(),
		UpdatedAt:        time.Now(),
		EmbeddingModelID: kb.EmbeddingModelID,
		Metadata:         metadataJSON,
	}
	// Save knowledge record to database
	logger.Info(ctx, "Saving knowledge record to database")
	if err := s.repo.CreateKnowledge(ctx, knowledge); err != nil {
		logger.Errorf(ctx, "Failed to create knowledge record, ID: %s, error: %v", knowledge.ID, err)
		return nil, err
	}
	// Save the file to storage
	logger.Infof(ctx, "Saving file, knowledge ID: %s", knowledge.ID)
	filePath, err := s.fileSvc.SaveFile(ctx, file, knowledge.TenantID, knowledge.ID)
	if err != nil {
		logger.Errorf(ctx, "Failed to save file, knowledge ID: %s, error: %v", knowledge.ID, err)
		return nil, err
	}
	knowledge.FilePath = filePath

	// Update knowledge record with file path
	logger.Info(ctx, "Updating knowledge record with file path")
	if err := s.repo.UpdateKnowledge(ctx, knowledge); err != nil {
		logger.Errorf(ctx, "Failed to update knowledge with file path, ID: %s, error: %v", knowledge.ID, err)
		return nil, err
	}

	// Enqueue document processing task to Asynq
	logger.Info(ctx, "Enqueuing document processing task to Asynq")
	enableMultimodelValue := false
	if enableMultimodel != nil {
		enableMultimodelValue = *enableMultimodel
	} else {
		enableMultimodelValue = kb.IsMultimodalEnabled()
	}

	// Check question generation config
	enableQuestionGeneration := false
	questionCount := 3 // default
	if kb.QuestionGenerationConfig != nil && kb.QuestionGenerationConfig.Enabled {
		enableQuestionGeneration = true
		if kb.QuestionGenerationConfig.QuestionCount > 0 {
			questionCount = kb.QuestionGenerationConfig.QuestionCount
		}
	}

	taskPayload := types.DocumentProcessPayload{
		TenantID:                 tenantID,
		KnowledgeID:              knowledge.ID,
		KnowledgeBaseID:          kbID,
		FilePath:                 filePath,
		FileName:                 safeFilename,
		FileType:                 getFileType(safeFilename),
		EnableMultimodel:         enableMultimodelValue,
		EnableQuestionGeneration: enableQuestionGeneration,
		QuestionCount:            questionCount,
	}

	payloadBytes, err := json.Marshal(taskPayload)
	if err != nil {
		logger.Errorf(ctx, "Failed to marshal document process task payload: %v", err)
		// 即使入队失败，也返回knowledge，因为文件已保存
		return knowledge, nil
	}

	task := asynq.NewTask(types.TypeDocumentProcess, payloadBytes, asynq.Queue("default"))
	info, err := s.task.Enqueue(task)
	if err != nil {
		logger.Errorf(ctx, "Failed to enqueue document process task: %v", err)
		// 即使入队失败，也返回knowledge，因为文件已保存
		return knowledge, nil
	}
	logger.Infof(
		ctx,
		"Enqueued document process task: id=%s queue=%s knowledge_id=%s",
		info.ID,
		info.Queue,
		knowledge.ID,
	)

	if slices.Contains([]string{"csv", "xlsx", "xls"}, getFileType(safeFilename)) {
		NewDataTableSummaryTask(ctx, s.task, tenantID, knowledge.ID, kb.SummaryModelID, kb.EmbeddingModelID)
	}

	logger.Infof(ctx, "Knowledge from file created successfully, ID: %s", knowledge.ID)
	return knowledge, nil
}

// CreateKnowledgeFromURL creates a knowledge entry from a URL source
// tagID is optional - when provided, the knowledge will be assigned to the specified tag/category.
func (s *knowledgeService) CreateKnowledgeFromURL(ctx context.Context,
	kbID string, url string, enableMultimodel *bool, title string, tagID string,
) (*types.Knowledge, error) {
	logger.Info(ctx, "Start creating knowledge from URL")
	logger.Infof(ctx, "Knowledge base ID: %s, URL: %s", kbID, url)

	// Get knowledge base configuration
	logger.Info(ctx, "Getting knowledge base configuration")
	kb, err := s.kbService.GetKnowledgeBaseByID(ctx, kbID)
	if err != nil {
		logger.Errorf(ctx, "Failed to get knowledge base: %v", err)
		return nil, err
	}

	// Validate URL format and security
	logger.Info(ctx, "Validating URL")
	if !isValidURL(url) || !secutils.IsValidURL(url) {
		logger.Error(ctx, "Invalid or unsafe URL format")
		return nil, ErrInvalidURL
	}

	// SSRF protection: validate URL is safe to fetch
	if safe, reason := secutils.IsSSRFSafeURL(url); !safe {
		logger.Errorf(ctx, "URL rejected for SSRF protection: %s, reason: %s", url, reason)
		return nil, ErrInvalidURL
	}

	// Check if URL already exists in the knowledge base
	tenantID := ctx.Value(types.TenantIDContextKey).(uint64)
	logger.Infof(ctx, "Checking if URL exists, tenant ID: %d", tenantID)
	fileHash := calculateStr(url)
	exists, existingKnowledge, err := s.repo.CheckKnowledgeExists(ctx, tenantID, kbID, &types.KnowledgeCheckParams{
		Type:     "url",
		URL:      url,
		FileHash: fileHash,
	})
	if err != nil {
		logger.Errorf(ctx, "Failed to check knowledge existence: %v", err)
		return nil, err
	}
	if exists {
		logger.Infof(ctx, "URL already exists: %s", url)
		// Update creation time for existing knowledge
		existingKnowledge.CreatedAt = time.Now()
		existingKnowledge.UpdatedAt = time.Now()
		if err := s.repo.UpdateKnowledge(ctx, existingKnowledge); err != nil {
			logger.Errorf(ctx, "Failed to update existing knowledge: %v", err)
			return nil, err
		}
		return existingKnowledge, types.NewDuplicateURLError(existingKnowledge)
	}

	// Check storage quota
	tenantInfo := ctx.Value(types.TenantInfoContextKey).(*types.Tenant)
	if tenantInfo.StorageQuota > 0 && tenantInfo.StorageUsed >= tenantInfo.StorageQuota {
		logger.Error(ctx, "Storage quota exceeded")
		return nil, types.NewStorageQuotaExceededError()
	}

	// Create knowledge record
	logger.Info(ctx, "Creating knowledge record")
	knowledge := &types.Knowledge{
		ID:               uuid.New().String(),
		TenantID:         tenantID,
		KnowledgeBaseID:  kbID,
		Type:             "url",
		Title:            title,
		Source:           url,
		FileHash:         fileHash,
		ParseStatus:      "pending",
		EnableStatus:     "disabled",
		CreatedAt:        time.Now(),
		UpdatedAt:        time.Now(),
		EmbeddingModelID: kb.EmbeddingModelID,
		TagID:            tagID, // 设置分类ID，用于知识分类管理
	}

	// Save knowledge record
	logger.Infof(ctx, "Saving knowledge record to database, ID: %s", knowledge.ID)
	if err := s.repo.CreateKnowledge(ctx, knowledge); err != nil {
		logger.Errorf(ctx, "Failed to create knowledge record: %v", err)
		return nil, err
	}

	// Enqueue URL processing task to Asynq
	logger.Info(ctx, "Enqueuing URL processing task to Asynq")
	enableMultimodelValue := false
	if enableMultimodel != nil {
		enableMultimodelValue = *enableMultimodel
	} else {
		enableMultimodelValue = kb.IsMultimodalEnabled()
	}

	// Check question generation config
	enableQuestionGeneration := false
	questionCount := 3 // default
	if kb.QuestionGenerationConfig != nil && kb.QuestionGenerationConfig.Enabled {
		enableQuestionGeneration = true
		if kb.QuestionGenerationConfig.QuestionCount > 0 {
			questionCount = kb.QuestionGenerationConfig.QuestionCount
		}
	}

	taskPayload := types.DocumentProcessPayload{
		TenantID:                 tenantID,
		KnowledgeID:              knowledge.ID,
		KnowledgeBaseID:          kbID,
		URL:                      url,
		EnableMultimodel:         enableMultimodelValue,
		EnableQuestionGeneration: enableQuestionGeneration,
		QuestionCount:            questionCount,
	}

	payloadBytes, err := json.Marshal(taskPayload)
	if err != nil {
		logger.Errorf(ctx, "Failed to marshal URL process task payload: %v", err)
		return knowledge, nil
	}

	task := asynq.NewTask(types.TypeDocumentProcess, payloadBytes, asynq.Queue("default"))
	info, err := s.task.Enqueue(task)
	if err != nil {
		logger.Errorf(ctx, "Failed to enqueue URL process task: %v", err)
		return knowledge, nil
	}
	logger.Infof(ctx, "Enqueued URL process task: id=%s queue=%s knowledge_id=%s", info.ID, info.Queue, knowledge.ID)

	logger.Infof(ctx, "Knowledge from URL created successfully, ID: %s", knowledge.ID)
	return knowledge, nil
}

// CreateKnowledgeFromPassage creates a knowledge entry from text passages
func (s *knowledgeService) CreateKnowledgeFromPassage(ctx context.Context,
	kbID string, passage []string,
) (*types.Knowledge, error) {
	return s.createKnowledgeFromPassageInternal(ctx, kbID, passage, false)
}

// CreateKnowledgeFromPassageSync creates a knowledge entry from text passages and waits for indexing to complete.
func (s *knowledgeService) CreateKnowledgeFromPassageSync(ctx context.Context,
	kbID string, passage []string,
) (*types.Knowledge, error) {
	return s.createKnowledgeFromPassageInternal(ctx, kbID, passage, true)
}

// CreateKnowledgeFromManual creates or saves manual Markdown knowledge content.
func (s *knowledgeService) CreateKnowledgeFromManual(ctx context.Context,
	kbID string, payload *types.ManualKnowledgePayload,
) (*types.Knowledge, error) {
	logger.Info(ctx, "Start creating manual knowledge entry")

	if payload == nil {
		return nil, werrors.NewBadRequestError("请求内容不能为空")
	}

	cleanContent := secutils.CleanMarkdown(payload.Content)
	if strings.TrimSpace(cleanContent) == "" {
		return nil, werrors.NewValidationError("内容不能为空")
	}
	if len([]rune(cleanContent)) > manualContentMaxLength {
		return nil, werrors.NewValidationError(fmt.Sprintf("内容长度超出限制（最多%d个字符）", manualContentMaxLength))
	}

	safeTitle, ok := secutils.ValidateInput(payload.Title)
	if !ok {
		return nil, werrors.NewValidationError("标题包含非法字符或超出长度限制")
	}

	status := strings.ToLower(strings.TrimSpace(payload.Status))
	if status == "" {
		status = types.ManualKnowledgeStatusDraft
	}
	if status != types.ManualKnowledgeStatusDraft && status != types.ManualKnowledgeStatusPublish {
		return nil, werrors.NewValidationError("状态仅支持 draft 或 publish")
	}

	kb, err := s.kbService.GetKnowledgeBaseByID(ctx, kbID)
	if err != nil {
		logger.Errorf(ctx, "Failed to get knowledge base: %v", err)
		return nil, err
	}

	tenantID := ctx.Value(types.TenantIDContextKey).(uint64)
	now := time.Now()
	title := safeTitle
	if title == "" {
		title = fmt.Sprintf("Knowledge-%s", now.Format("20060102-150405"))
	}

	fileName := ensureManualFileName(title)
	meta := types.NewManualKnowledgeMetadata(cleanContent, status, 1)

	knowledge := &types.Knowledge{
		TenantID:         tenantID,
		KnowledgeBaseID:  kbID,
		Type:             types.KnowledgeTypeManual,
		Title:            title,
		Description:      "",
		Source:           types.KnowledgeTypeManual,
		ParseStatus:      types.ManualKnowledgeStatusDraft,
		EnableStatus:     "disabled",
		CreatedAt:        now,
		UpdatedAt:        now,
		EmbeddingModelID: kb.EmbeddingModelID,
		FileName:         fileName,
		FileType:         types.KnowledgeTypeManual,
		TagID:            payload.TagID, // 设置分类ID，用于知识分类管理
	}
	if err := knowledge.SetManualMetadata(meta); err != nil {
		logger.Errorf(ctx, "Failed to set manual metadata: %v", err)
		return nil, err
	}
	knowledge.EnsureManualDefaults()

	if status == types.ManualKnowledgeStatusPublish {
		knowledge.ParseStatus = "pending"
	}

	if err := s.repo.CreateKnowledge(ctx, knowledge); err != nil {
		logger.Errorf(ctx, "Failed to create manual knowledge record: %v", err)
		return nil, err
	}

	if status == types.ManualKnowledgeStatusPublish {
		logger.Infof(ctx, "Manual knowledge created, scheduling indexing, ID: %s", knowledge.ID)
		s.triggerManualProcessing(ctx, kb, knowledge, cleanContent, false)
	}

	return knowledge, nil
}

// createKnowledgeFromPassageInternal consolidates the common logic for creating knowledge from passages.
// When syncMode is true, chunk processing is performed synchronously; otherwise, it's processed asynchronously.
func (s *knowledgeService) createKnowledgeFromPassageInternal(ctx context.Context,
	kbID string, passage []string, syncMode bool,
) (*types.Knowledge, error) {
	if syncMode {
		logger.Info(ctx, "Start creating knowledge from passage (sync)")
	} else {
		logger.Info(ctx, "Start creating knowledge from passage")
	}
	logger.Infof(ctx, "Knowledge base ID: %s, passage count: %d", kbID, len(passage))

	// 验证段落内容安全性
	safePassages := make([]string, 0, len(passage))
	for i, p := range passage {
		safePassage, isValid := secutils.ValidateInput(p)
		if !isValid {
			logger.Errorf(ctx, "Invalid passage content at index %d", i)
			return nil, werrors.NewValidationError(fmt.Sprintf("段落 %d 包含非法内容", i+1))
		}
		safePassages = append(safePassages, safePassage)
	}

	// Get knowledge base configuration
	logger.Info(ctx, "Getting knowledge base configuration")
	kb, err := s.kbService.GetKnowledgeBaseByID(ctx, kbID)
	if err != nil {
		logger.Errorf(ctx, "Failed to get knowledge base: %v", err)
		return nil, err
	}

	// Create knowledge record
	if syncMode {
		logger.Info(ctx, "Creating knowledge record (sync)")
	} else {
		logger.Info(ctx, "Creating knowledge record")
	}
	knowledge := &types.Knowledge{
		ID:               uuid.New().String(),
		TenantID:         ctx.Value(types.TenantIDContextKey).(uint64),
		KnowledgeBaseID:  kbID,
		Type:             "passage",
		ParseStatus:      "pending",
		EnableStatus:     "disabled",
		CreatedAt:        time.Now(),
		UpdatedAt:        time.Now(),
		EmbeddingModelID: kb.EmbeddingModelID,
	}

	// Save knowledge record
	logger.Infof(ctx, "Saving knowledge record to database, ID: %s", knowledge.ID)
	if err := s.repo.CreateKnowledge(ctx, knowledge); err != nil {
		logger.Errorf(ctx, "Failed to create knowledge record: %v", err)
		return nil, err
	}

	// Process passages
	if syncMode {
		logger.Info(ctx, "Processing passage synchronously")
		s.processDocumentFromPassage(ctx, kb, knowledge, safePassages)
		logger.Infof(ctx, "Knowledge from passage created successfully (sync), ID: %s", knowledge.ID)
	} else {
		// Enqueue passage processing task to Asynq
		logger.Info(ctx, "Enqueuing passage processing task to Asynq")
		tenantID := ctx.Value(types.TenantIDContextKey).(uint64)

		// Check question generation config
		enableQuestionGeneration := false
		questionCount := 3 // default
		if kb.QuestionGenerationConfig != nil && kb.QuestionGenerationConfig.Enabled {
			enableQuestionGeneration = true
			if kb.QuestionGenerationConfig.QuestionCount > 0 {
				questionCount = kb.QuestionGenerationConfig.QuestionCount
			}
		}

		taskPayload := types.DocumentProcessPayload{
			TenantID:                 tenantID,
			KnowledgeID:              knowledge.ID,
			KnowledgeBaseID:          kbID,
			Passages:                 safePassages,
			EnableMultimodel:         false, // 文本段落不支持多模态
			EnableQuestionGeneration: enableQuestionGeneration,
			QuestionCount:            questionCount,
		}

		payloadBytes, err := json.Marshal(taskPayload)
		if err != nil {
			logger.Errorf(ctx, "Failed to marshal passage process task payload: %v", err)
			// 即使入队失败，也返回knowledge
			return knowledge, nil
		}

		task := asynq.NewTask(types.TypeDocumentProcess, payloadBytes, asynq.Queue("default"))
		info, err := s.task.Enqueue(task)
		if err != nil {
			logger.Errorf(ctx, "Failed to enqueue passage process task: %v", err)
			return knowledge, nil
		}
		logger.Infof(ctx, "Enqueued passage process task: id=%s queue=%s knowledge_id=%s", info.ID, info.Queue, knowledge.ID)
		logger.Infof(ctx, "Knowledge from passage created successfully, ID: %s", knowledge.ID)
	}
	return knowledge, nil
}

// GetKnowledgeByID retrieves a knowledge entry by its ID
func (s *knowledgeService) GetKnowledgeByID(ctx context.Context, id string) (*types.Knowledge, error) {
	tenantID := ctx.Value(types.TenantIDContextKey).(uint64)

	knowledge, err := s.repo.GetKnowledgeByID(ctx, tenantID, id)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"knowledge_id": id,
			"tenant_id":    tenantID,
		})
		return nil, err
	}

	logger.Infof(ctx, "Knowledge retrieved successfully, ID: %s, type: %s", knowledge.ID, knowledge.Type)
	return knowledge, nil
}

// ListKnowledgeByKnowledgeBaseID returns all knowledge entries in a knowledge base
func (s *knowledgeService) ListKnowledgeByKnowledgeBaseID(ctx context.Context,
	kbID string,
) ([]*types.Knowledge, error) {
	return s.repo.ListKnowledgeByKnowledgeBaseID(ctx, ctx.Value(types.TenantIDContextKey).(uint64), kbID)
}

// ListPagedKnowledgeByKnowledgeBaseID returns paginated knowledge entries in a knowledge base
func (s *knowledgeService) ListPagedKnowledgeByKnowledgeBaseID(ctx context.Context,
	kbID string, page *types.Pagination, tagID string, keyword string, fileType string,
) (*types.PageResult, error) {
	knowledges, total, err := s.repo.ListPagedKnowledgeByKnowledgeBaseID(ctx,
		ctx.Value(types.TenantIDContextKey).(uint64), kbID, page, tagID, keyword, fileType)
	if err != nil {
		return nil, err
	}

	return types.NewPageResult(total, page, knowledges), nil
}

// DeleteKnowledge deletes a knowledge entry and all related resources
func (s *knowledgeService) DeleteKnowledge(ctx context.Context, id string) error {
	// Get the knowledge entry
	knowledge, err := s.repo.GetKnowledgeByID(ctx, ctx.Value(types.TenantIDContextKey).(uint64), id)
	if err != nil {
		return err
	}

	// Mark as deleting first to prevent async task conflicts
	// This ensures that any running async tasks will detect the deletion and abort
	originalStatus := knowledge.ParseStatus
	knowledge.ParseStatus = types.ParseStatusDeleting
	knowledge.UpdatedAt = time.Now()
	if err := s.repo.UpdateKnowledge(ctx, knowledge); err != nil {
		logger.GetLogger(ctx).WithField("error", err).Errorf("DeleteKnowledge failed to mark as deleting")
		// Continue with deletion even if marking fails
	} else {
		logger.Infof(ctx, "Marked knowledge %s as deleting (previous status: %s)", id, originalStatus)
	}

	wg := errgroup.Group{}
	// Delete knowledge embeddings from vector store
	wg.Go(func() error {
		tenantInfo := ctx.Value(types.TenantInfoContextKey).(*types.Tenant)
		retrieveEngine, err := retriever.NewCompositeRetrieveEngine(
			s.retrieveEngine,
			tenantInfo.GetEffectiveEngines(),
		)
		if err != nil {
			logger.GetLogger(ctx).WithField("error", err).Errorf("DeleteKnowledge delete knowledge embedding failed")
			return err
		}
		embeddingModel, err := s.modelService.GetEmbeddingModel(ctx, knowledge.EmbeddingModelID)
		if err != nil {
			logger.GetLogger(ctx).WithField("error", err).Errorf("DeleteKnowledge delete knowledge embedding failed")
			return err
		}
		if err := retrieveEngine.DeleteByKnowledgeIDList(ctx, []string{knowledge.ID}, embeddingModel.GetDimensions(), knowledge.Type); err != nil {
			logger.GetLogger(ctx).WithField("error", err).Errorf("DeleteKnowledge delete knowledge embedding failed")
			return err
		}
		return nil
	})

	// Delete all chunks associated with this knowledge
	wg.Go(func() error {
		if err := s.chunkService.DeleteChunksByKnowledgeID(ctx, knowledge.ID); err != nil {
			logger.GetLogger(ctx).WithField("error", err).Errorf("DeleteKnowledge delete chunks failed")
			return err
		}
		return nil
	})

	// Delete the physical file if it exists
	wg.Go(func() error {
		if knowledge.FilePath != "" {
			if err := s.fileSvc.DeleteFile(ctx, knowledge.FilePath); err != nil {
				logger.GetLogger(ctx).WithField("error", err).Errorf("DeleteKnowledge delete file failed")
			}
		}
		tenantInfo := ctx.Value(types.TenantInfoContextKey).(*types.Tenant)
		tenantInfo.StorageUsed -= knowledge.StorageSize
		if err := s.tenantRepo.AdjustStorageUsed(ctx, tenantInfo.ID, -knowledge.StorageSize); err != nil {
			logger.GetLogger(ctx).WithField("error", err).Errorf("DeleteKnowledge update tenant storage used failed")
		}
		return nil
	})

	// Delete the knowledge graph
	wg.Go(func() error {
		namespace := types.NameSpace{KnowledgeBase: knowledge.KnowledgeBaseID, Knowledge: knowledge.ID}
		if err := s.graphEngine.DelGraph(ctx, []types.NameSpace{namespace}); err != nil {
			logger.GetLogger(ctx).WithField("error", err).Errorf("DeleteKnowledge delete knowledge graph failed")
			return err
		}
		return nil
	})

	if err = wg.Wait(); err != nil {
		return err
	}
	// Delete the knowledge entry itself from the database
	return s.repo.DeleteKnowledge(ctx, ctx.Value(types.TenantIDContextKey).(uint64), id)
}

// DeleteKnowledgeList deletes a knowledge entry and all related resources
func (s *knowledgeService) DeleteKnowledgeList(ctx context.Context, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	// 1. Get the knowledge entry
	tenantInfo := ctx.Value(types.TenantInfoContextKey).(*types.Tenant)
	knowledgeList, err := s.repo.GetKnowledgeBatch(ctx, tenantInfo.ID, ids)
	if err != nil {
		return err
	}

	// Mark all as deleting first to prevent async task conflicts
	for _, knowledge := range knowledgeList {
		knowledge.ParseStatus = types.ParseStatusDeleting
		knowledge.UpdatedAt = time.Now()
		if err := s.repo.UpdateKnowledge(ctx, knowledge); err != nil {
			logger.GetLogger(ctx).WithField("error", err).WithField("knowledge_id", knowledge.ID).
				Errorf("DeleteKnowledgeList failed to mark as deleting")
			// Continue with deletion even if marking fails
		}
	}
	logger.Infof(ctx, "Marked %d knowledge entries as deleting", len(knowledgeList))

	wg := errgroup.Group{}
	// 2. Delete knowledge embeddings from vector store
	wg.Go(func() error {
		tenantInfo := ctx.Value(types.TenantInfoContextKey).(*types.Tenant)
		retrieveEngine, err := retriever.NewCompositeRetrieveEngine(
			s.retrieveEngine,
			tenantInfo.GetEffectiveEngines(),
		)
		if err != nil {
			logger.GetLogger(ctx).WithField("error", err).Errorf("DeleteKnowledge delete knowledge embedding failed")
			return err
		}
		// Group by EmbeddingModelID and Type
		type groupKey struct {
			EmbeddingModelID string
			Type             string
		}
		group := map[groupKey][]string{}
		for _, knowledge := range knowledgeList {
			key := groupKey{EmbeddingModelID: knowledge.EmbeddingModelID, Type: knowledge.Type}
			group[key] = append(group[key], knowledge.ID)
		}
		for key, knowledgeIDs := range group {
			embeddingModel, err := s.modelService.GetEmbeddingModel(ctx, key.EmbeddingModelID)
			if err != nil {
				logger.GetLogger(ctx).WithField("error", err).Errorf("DeleteKnowledge get embedding model failed")
				return err
			}
			if err := retrieveEngine.DeleteByKnowledgeIDList(ctx, knowledgeIDs, embeddingModel.GetDimensions(), key.Type); err != nil {
				logger.GetLogger(ctx).
					WithField("error", err).
					Errorf("DeleteKnowledge delete knowledge embedding failed")
				return err
			}
		}
		return nil
	})

	// 3. Delete all chunks associated with this knowledge
	wg.Go(func() error {
		if err := s.chunkService.DeleteByKnowledgeList(ctx, ids); err != nil {
			logger.GetLogger(ctx).WithField("error", err).Errorf("DeleteKnowledge delete chunks failed")
			return err
		}
		return nil
	})

	// 4. Delete the physical file if it exists
	wg.Go(func() error {
		storageAdjust := int64(0)
		for _, knowledge := range knowledgeList {
			if knowledge.FilePath != "" {
				if err := s.fileSvc.DeleteFile(ctx, knowledge.FilePath); err != nil {
					logger.GetLogger(ctx).WithField("error", err).Errorf("DeleteKnowledge delete file failed")
				}
			}
			storageAdjust -= knowledge.StorageSize
		}
		tenantInfo.StorageUsed += storageAdjust
		if err := s.tenantRepo.AdjustStorageUsed(ctx, tenantInfo.ID, storageAdjust); err != nil {
			logger.GetLogger(ctx).WithField("error", err).Errorf("DeleteKnowledge update tenant storage used failed")
		}
		return nil
	})

	// Delete the knowledge graph
	wg.Go(func() error {
		namespaces := []types.NameSpace{}
		for _, knowledge := range knowledgeList {
			namespaces = append(
				namespaces,
				types.NameSpace{KnowledgeBase: knowledge.KnowledgeBaseID, Knowledge: knowledge.ID},
			)
		}
		if err := s.graphEngine.DelGraph(ctx, namespaces); err != nil {
			logger.GetLogger(ctx).WithField("error", err).Errorf("DeleteKnowledge delete knowledge graph failed")
			return err
		}
		return nil
	})

	if err = wg.Wait(); err != nil {
		return err
	}
	// 5. Delete the knowledge entry itself from the database
	return s.repo.DeleteKnowledgeList(ctx, tenantInfo.ID, ids)
}

func (s *knowledgeService) cloneKnowledge(
	ctx context.Context,
	src *types.Knowledge,
	targetKB *types.KnowledgeBase,
) (err error) {
	if src.ParseStatus != "completed" {
		logger.GetLogger(ctx).WithField("knowledge_id", src.ID).Errorf("MoveKnowledge parse status is not completed")
		return nil
	}
	tenantInfo := ctx.Value(types.TenantInfoContextKey).(*types.Tenant)
	dst := &types.Knowledge{
		ID:               uuid.New().String(),
		TenantID:         targetKB.TenantID,
		KnowledgeBaseID:  targetKB.ID,
		Type:             src.Type,
		Title:            src.Title,
		Description:      src.Description,
		Source:           src.Source,
		ParseStatus:      "processing",
		EnableStatus:     "disabled",
		EmbeddingModelID: targetKB.EmbeddingModelID,
		FileName:         src.FileName,
		FileType:         src.FileType,
		FileSize:         src.FileSize,
		FileHash:         src.FileHash,
		FilePath:         src.FilePath,
		StorageSize:      src.StorageSize,
		Metadata:         src.Metadata,
	}
	defer func() {
		if err != nil {
			dst.ParseStatus = "failed"
			dst.ErrorMessage = err.Error()
			_ = s.repo.UpdateKnowledge(ctx, dst)
			logger.GetLogger(ctx).WithField("error", err).Errorf("MoveKnowledge failed to move knowledge")
		} else {
			dst.ParseStatus = "completed"
			dst.EnableStatus = "enabled"
			_ = s.repo.UpdateKnowledge(ctx, dst)
			logger.GetLogger(ctx).WithField("knowledge_id", dst.ID).Infof("MoveKnowledge move knowledge successfully")
		}
	}()

	if err = s.repo.CreateKnowledge(ctx, dst); err != nil {
		logger.GetLogger(ctx).WithField("error", err).Errorf("MoveKnowledge create knowledge failed")
		return
	}
	tenantInfo.StorageUsed += dst.StorageSize
	if err = s.tenantRepo.AdjustStorageUsed(ctx, tenantInfo.ID, dst.StorageSize); err != nil {
		logger.GetLogger(ctx).WithField("error", err).Errorf("MoveKnowledge update tenant storage used failed")
		return
	}
	if err = s.CloneChunk(ctx, src, dst); err != nil {
		logger.GetLogger(ctx).WithField("knowledge_id", dst.ID).
			WithField("error", err).Errorf("MoveKnowledge move chunks failed")
		return
	}
	return
}

// processDocumentFromPassage handles asynchronous processing of text passages
func (s *knowledgeService) processDocumentFromPassage(ctx context.Context,
	kb *types.KnowledgeBase, knowledge *types.Knowledge, passage []string,
) {
	// Update status to processing
	knowledge.ParseStatus = "processing"
	knowledge.UpdatedAt = time.Now()
	if err := s.repo.UpdateKnowledge(ctx, knowledge); err != nil {
		return
	}

	// Convert passages to chunks
	chunks := make([]*proto.Chunk, 0, len(passage))
	start, end := 0, 0
	for i, p := range passage {
		if p == "" {
			continue
		}
		end += len([]rune(p))
		chunk := &proto.Chunk{
			Content: p,
			Seq:     int32(i),
			Start:   int32(start),
			End:     int32(end),
		}
		start = end
		chunks = append(chunks, chunk)
	}
	// Process and store chunks
	s.processChunks(ctx, kb, knowledge, chunks)
}

// ProcessChunksOptions contains options for processing chunks
type ProcessChunksOptions struct {
	EnableQuestionGeneration bool
	QuestionCount            int
}

// processChunks processes chunks and creates embeddings for knowledge content
func (s *knowledgeService) processChunks(ctx context.Context,
	kb *types.KnowledgeBase, knowledge *types.Knowledge, chunks []*proto.Chunk,
	opts ...ProcessChunksOptions,
) {
	// Get options
	var options ProcessChunksOptions
	if len(opts) > 0 {
		options = opts[0]
	}

	ctx, span := tracing.ContextWithSpan(ctx, "knowledgeService.processChunks")
	defer span.End()
	span.SetAttributes(
		attribute.Int("tenant_id", int(knowledge.TenantID)),
		attribute.String("knowledge_base_id", knowledge.KnowledgeBaseID),
		attribute.String("knowledge_id", knowledge.ID),
		attribute.String("embedding_model_id", kb.EmbeddingModelID),
		attribute.Int("chunk_count", len(chunks)),
	)

	// Check if knowledge is being deleted before processing
	if s.isKnowledgeDeleting(ctx, knowledge.TenantID, knowledge.ID) {
		logger.Infof(ctx, "Knowledge is being deleted, aborting chunk processing: %s", knowledge.ID)
		span.AddEvent("aborted: knowledge is being deleted")
		return
	}

	// Get embedding model for vectorization
	embeddingModel, err := s.modelService.GetEmbeddingModel(ctx, kb.EmbeddingModelID)
	if err != nil {
		logger.GetLogger(ctx).WithField("error", err).Errorf("processChunks get embedding model failed")
		span.RecordError(err)
		return
	}

	// 幂等性处理：清理旧的chunks和索引数据，避免重复数据
	logger.Infof(ctx, "Cleaning up existing chunks and index data for knowledge: %s", knowledge.ID)

	// 删除旧的chunks
	if err := s.chunkService.DeleteChunksByKnowledgeID(ctx, knowledge.ID); err != nil {
		logger.Warnf(ctx, "Failed to delete existing chunks (may not exist): %v", err)
		// 不返回错误，继续处理（可能没有旧数据）
	}

	// 删除旧的索引数据
	tenantInfo := ctx.Value(types.TenantInfoContextKey).(*types.Tenant)
	retrieveEngine, err := retriever.NewCompositeRetrieveEngine(s.retrieveEngine, tenantInfo.GetEffectiveEngines())
	if err == nil {
		if err := retrieveEngine.DeleteByKnowledgeIDList(ctx, []string{knowledge.ID}, embeddingModel.GetDimensions(), knowledge.Type); err != nil {
			logger.Warnf(ctx, "Failed to delete existing index data (may not exist): %v", err)
			// 不返回错误，继续处理（可能没有旧数据）
		} else {
			logger.Infof(ctx, "Successfully deleted existing index data for knowledge: %s", knowledge.ID)
		}
	}

	// 删除知识图谱数据（如果存在）
	namespace := types.NameSpace{KnowledgeBase: knowledge.KnowledgeBaseID, Knowledge: knowledge.ID}
	if err := s.graphEngine.DelGraph(ctx, []types.NameSpace{namespace}); err != nil {
		logger.Warnf(ctx, "Failed to delete existing graph data (may not exist): %v", err)
		// 不返回错误，继续处理
	}

	logger.Infof(ctx, "Cleanup completed, starting to process new chunks")

	// ========== DocReader 解析结果日志 ==========
	logger.Infof(ctx, "[DocReader] ========== 解析结果概览 ==========")
	logger.Infof(ctx, "[DocReader] 知识ID: %s, 知识库ID: %s", knowledge.ID, knowledge.KnowledgeBaseID)
	logger.Infof(ctx, "[DocReader] 总Chunk数量: %d", len(chunks))

	// 统计图片信息
	totalImages := 0
	chunksWithImages := 0
	for _, chunkData := range chunks {
		if len(chunkData.Images) > 0 {
			chunksWithImages++
			totalImages += len(chunkData.Images)
		}
	}
	logger.Infof(ctx, "[DocReader] 包含图片的Chunk数: %d, 总图片数: %d", chunksWithImages, totalImages)

	// 打印每个Chunk的详细信息
	for idx, chunkData := range chunks {
		contentPreview := chunkData.Content
		if len(contentPreview) > 200 {
			contentPreview = contentPreview[:200] + "..."
		}
		logger.Infof(ctx, "[DocReader] Chunk #%d (seq=%d): 内容长度=%d, 图片数=%d, 范围=[%d-%d]",
			idx, chunkData.Seq, len(chunkData.Content), len(chunkData.Images), chunkData.Start, chunkData.End)
		logger.Debugf(ctx, "[DocReader] Chunk #%d 内容预览: %s", idx, contentPreview)

		// 打印图片详细信息
		for imgIdx, img := range chunkData.Images {
			logger.Infof(ctx, "[DocReader]   图片 #%d: URL=%s", imgIdx, img.Url)
			logger.Infof(ctx, "[DocReader]   图片 #%d: OriginalURL=%s", imgIdx, img.OriginalUrl)
			if img.Caption != "" {
				captionPreview := img.Caption
				if len(captionPreview) > 100 {
					captionPreview = captionPreview[:100] + "..."
				}
				logger.Infof(ctx, "[DocReader]   图片 #%d: Caption=%s", imgIdx, captionPreview)
			}
			if img.OcrText != "" {
				ocrPreview := img.OcrText
				if len(ocrPreview) > 100 {
					ocrPreview = ocrPreview[:100] + "..."
				}
				logger.Infof(ctx, "[DocReader]   图片 #%d: OCRText=%s", imgIdx, ocrPreview)
			}
			logger.Infof(ctx, "[DocReader]   图片 #%d: 位置=[%d-%d]", imgIdx, img.Start, img.End)
		}
	}
	logger.Infof(ctx, "[DocReader] ========== 解析结果概览结束 ==========")

	// Create chunk objects from proto chunks
	maxSeq := 0

	// 统计图片相关的子Chunk数量，用于扩展insertChunks的容量
	imageChunkCount := 0
	for _, chunkData := range chunks {
		if len(chunkData.Images) > 0 {
			// 为每个图片的OCR和Caption分别创建一个Chunk
			imageChunkCount += len(chunkData.Images) * 2
		}
		if int(chunkData.Seq) > maxSeq {
			maxSeq = int(chunkData.Seq)
		}
	}

	// 重新分配容量，考虑图片相关的Chunk
	insertChunks := make([]*types.Chunk, 0, len(chunks)+imageChunkCount)

	for _, chunkData := range chunks {
		if strings.TrimSpace(chunkData.Content) == "" {
			continue
		}

		// 创建主文本Chunk
		textChunk := &types.Chunk{
			ID:              uuid.New().String(),
			TenantID:        knowledge.TenantID,
			KnowledgeID:     knowledge.ID,
			KnowledgeBaseID: knowledge.KnowledgeBaseID,
			Content:         chunkData.Content,
			ChunkIndex:      int(chunkData.Seq),
			IsEnabled:       true,
			CreatedAt:       time.Now(),
			UpdatedAt:       time.Now(),
			StartAt:         int(chunkData.Start),
			EndAt:           int(chunkData.End),
			ChunkType:       types.ChunkTypeText,
		}
		var chunkImages []types.ImageInfo
		insertChunks = append(insertChunks, textChunk)

		// 处理图片信息
		if len(chunkData.Images) > 0 {
			logger.GetLogger(ctx).Infof("Processing %d images in chunk #%d", len(chunkData.Images), chunkData.Seq)

			for i, img := range chunkData.Images {
				// 保存图片信息到文本Chunk
				imageInfo := types.ImageInfo{
					URL:         img.Url,
					OriginalURL: img.OriginalUrl,
					StartPos:    int(img.Start),
					EndPos:      int(img.End),
					OCRText:     img.OcrText,
					Caption:     img.Caption,
				}
				chunkImages = append(chunkImages, imageInfo)

				// 将ImageInfo序列化为JSON
				imageInfoJSON, err := json.Marshal([]types.ImageInfo{imageInfo})
				if err != nil {
					logger.GetLogger(ctx).WithField("error", err).Errorf("Failed to marshal image info to JSON")
					continue
				}

				// 如果有OCR文本，创建OCR Chunk
				if img.OcrText != "" {
					ocrChunk := &types.Chunk{
						ID:              uuid.New().String(),
						TenantID:        knowledge.TenantID,
						KnowledgeID:     knowledge.ID,
						KnowledgeBaseID: knowledge.KnowledgeBaseID,
						Content:         img.OcrText,
						ChunkIndex:      maxSeq + i*100 + 1, // 使用不冲突的索引方式
						IsEnabled:       true,
						CreatedAt:       time.Now(),
						UpdatedAt:       time.Now(),
						StartAt:         int(img.Start),
						EndAt:           int(img.End),
						ChunkType:       types.ChunkTypeImageOCR,
						ParentChunkID:   textChunk.ID,
						ImageInfo:       string(imageInfoJSON),
					}
					insertChunks = append(insertChunks, ocrChunk)
					logger.GetLogger(ctx).Infof("Created OCR chunk for image %d in chunk #%d", i, chunkData.Seq)
				}

				// 如果有图片描述，创建Caption Chunk
				if img.Caption != "" {
					captionChunk := &types.Chunk{
						ID:              uuid.New().String(),
						TenantID:        knowledge.TenantID,
						KnowledgeID:     knowledge.ID,
						KnowledgeBaseID: knowledge.KnowledgeBaseID,
						Content:         img.Caption,
						ChunkIndex:      maxSeq + i*100 + 2, // 使用不冲突的索引方式
						IsEnabled:       true,
						CreatedAt:       time.Now(),
						UpdatedAt:       time.Now(),
						StartAt:         int(img.Start),
						EndAt:           int(img.End),
						ChunkType:       types.ChunkTypeImageCaption,
						ParentChunkID:   textChunk.ID,
						ImageInfo:       string(imageInfoJSON),
					}
					insertChunks = append(insertChunks, captionChunk)
					logger.GetLogger(ctx).Infof("Created caption chunk for image %d in chunk #%d", i, chunkData.Seq)
				}
			}

			imageInfoJSON, err := json.Marshal(chunkImages)
			if err != nil {
				logger.GetLogger(ctx).WithField("error", err).Errorf("Failed to marshal image info to JSON")
				continue
			}
			textChunk.ImageInfo = string(imageInfoJSON)
		}
	}

	// Sort chunks by index for proper ordering
	sort.Slice(insertChunks, func(i, j int) bool {
		return insertChunks[i].ChunkIndex < insertChunks[j].ChunkIndex
	})

	// 仅为文本类型的Chunk设置前后关系
	textChunks := make([]*types.Chunk, 0, len(chunks))
	for _, chunk := range insertChunks {
		if chunk.ChunkType == types.ChunkTypeText {
			textChunks = append(textChunks, chunk)
		}
	}

	// 设置文本Chunk之间的前后关系
	for i, chunk := range textChunks {
		if i > 0 {
			textChunks[i-1].NextChunkID = chunk.ID
		}
		if i < len(textChunks)-1 {
			textChunks[i+1].PreChunkID = chunk.ID
		}
	}

	// Create index information for each chunk (without generated questions for now)
	indexInfoList := make([]*types.IndexInfo, 0, len(insertChunks))
	for _, chunk := range insertChunks {
		// Add original chunk content to index
		indexInfoList = append(indexInfoList, &types.IndexInfo{
			Content:         chunk.Content,
			SourceID:        chunk.ID,
			SourceType:      types.ChunkSourceType,
			ChunkID:         chunk.ID,
			KnowledgeID:     knowledge.ID,
			KnowledgeBaseID: knowledge.KnowledgeBaseID,
		})
	}

	// Initialize retrieval engine

	// Calculate storage size required for embeddings
	span.AddEvent("estimate storage size")
	totalStorageSize := retrieveEngine.EstimateStorageSize(ctx, embeddingModel, indexInfoList)
	if tenantInfo.StorageQuota > 0 {
		// Re-fetch tenant storage information
		tenantInfo, err = s.tenantRepo.GetTenantByID(ctx, tenantInfo.ID)
		if err != nil {
			knowledge.ParseStatus = types.ParseStatusFailed
			knowledge.ErrorMessage = err.Error()
			knowledge.UpdatedAt = time.Now()
			s.repo.UpdateKnowledge(ctx, knowledge)
			span.RecordError(err)
			return
		}
		// Check if there's enough storage quota available
		if tenantInfo.StorageUsed+totalStorageSize > tenantInfo.StorageQuota {
			knowledge.ParseStatus = types.ParseStatusFailed
			knowledge.ErrorMessage = "存储空间不足"
			knowledge.UpdatedAt = time.Now()
			s.repo.UpdateKnowledge(ctx, knowledge)
			span.RecordError(errors.New("storage quota exceeded"))
			return
		}
	}

	// Check again if knowledge is being deleted before writing to database
	if s.isKnowledgeDeleting(ctx, knowledge.TenantID, knowledge.ID) {
		logger.Infof(ctx, "Knowledge is being deleted, aborting before saving chunks: %s", knowledge.ID)
		span.AddEvent("aborted: knowledge is being deleted before saving")
		return
	}

	// Save chunks to database
	span.AddEvent("create chunks")
	if err := s.chunkService.CreateChunks(ctx, insertChunks); err != nil {
		knowledge.ParseStatus = types.ParseStatusFailed
		knowledge.ErrorMessage = err.Error()
		knowledge.UpdatedAt = time.Now()
		s.repo.UpdateKnowledge(ctx, knowledge)
		span.RecordError(err)
		return
	}

	// Check again before batch indexing (this is a heavy operation)
	if s.isKnowledgeDeleting(ctx, knowledge.TenantID, knowledge.ID) {
		logger.Infof(ctx, "Knowledge is being deleted, cleaning up and aborting before indexing: %s", knowledge.ID)
		// Clean up the chunks we just created
		if err := s.chunkService.DeleteChunksByKnowledgeID(ctx, knowledge.ID); err != nil {
			logger.Warnf(ctx, "Failed to cleanup chunks after deletion detected: %v", err)
		}
		span.AddEvent("aborted: knowledge is being deleted before indexing")
		return
	}

	span.AddEvent("batch index")
	err = retrieveEngine.BatchIndex(ctx, embeddingModel, indexInfoList)
	if err != nil {
		knowledge.ParseStatus = types.ParseStatusFailed
		knowledge.ErrorMessage = err.Error()
		knowledge.UpdatedAt = time.Now()
		s.repo.UpdateKnowledge(ctx, knowledge)

		// delete failed chunks
		if err := s.chunkService.DeleteChunksByKnowledgeID(ctx, knowledge.ID); err != nil {
			logger.Errorf(ctx, "Delete chunks failed: %v", err)
		}

		// delete index
		if err := retrieveEngine.DeleteByKnowledgeIDList(
			ctx, []string{knowledge.ID}, embeddingModel.GetDimensions(), kb.Type,
		); err != nil {
			logger.Errorf(ctx, "Delete index failed: %v", err)
		}
		span.RecordError(err)
		return
	}
	logger.GetLogger(ctx).Infof("processChunks batch index successfully, with %d index", len(indexInfoList))

	logger.Infof(ctx, "processChunks create relationship rag task")
	if kb.ExtractConfig != nil && kb.ExtractConfig.Enabled {
		for _, chunk := range textChunks {
			err := NewChunkExtractTask(ctx, s.task, chunk.TenantID, chunk.ID, kb.SummaryModelID)
			if err != nil {
				logger.GetLogger(ctx).WithField("error", err).Errorf("processChunks create chunk extract task failed")
				span.RecordError(err)
			}
		}
	}

	// Final check before marking as completed - if deleted during processing, don't update status
	if s.isKnowledgeDeleting(ctx, knowledge.TenantID, knowledge.ID) {
		logger.Infof(ctx, "Knowledge was deleted during processing, skipping completion update: %s", knowledge.ID)
		// Clean up the data we just created since the knowledge is being deleted
		if err := s.chunkService.DeleteChunksByKnowledgeID(ctx, knowledge.ID); err != nil {
			logger.Warnf(ctx, "Failed to cleanup chunks after deletion detected: %v", err)
		}
		if err := retrieveEngine.DeleteByKnowledgeIDList(ctx, []string{knowledge.ID}, embeddingModel.GetDimensions(), kb.Type); err != nil {
			logger.Warnf(ctx, "Failed to cleanup index after deletion detected: %v", err)
		}
		span.AddEvent("aborted: knowledge was deleted during processing")
		return
	}

	// Update knowledge status to completed
	knowledge.ParseStatus = types.ParseStatusCompleted
	knowledge.EnableStatus = "enabled"
	knowledge.StorageSize = totalStorageSize
	now := time.Now()
	knowledge.ProcessedAt = &now
	knowledge.UpdatedAt = now

	// Set summary status based on whether summary generation will be triggered
	if len(textChunks) > 0 {
		knowledge.SummaryStatus = types.SummaryStatusPending
	} else {
		knowledge.SummaryStatus = types.SummaryStatusNone
	}

	if err := s.repo.UpdateKnowledge(ctx, knowledge); err != nil {
		logger.GetLogger(ctx).WithField("error", err).Errorf("processChunks update knowledge failed")
	}

	// Enqueue question generation task if enabled (async, non-blocking)
	if options.EnableQuestionGeneration && len(textChunks) > 0 {
		questionCount := options.QuestionCount
		if questionCount <= 0 {
			questionCount = 3
		}
		if questionCount > 10 {
			questionCount = 10
		}
		s.enqueueQuestionGenerationTask(ctx, knowledge.KnowledgeBaseID, knowledge.ID, questionCount)
	}

	// Enqueue summary generation task (async, non-blocking)
	if len(textChunks) > 0 {
		s.enqueueSummaryGenerationTask(ctx, knowledge.KnowledgeBaseID, knowledge.ID)
	}

	// Update tenant's storage usage
	tenantInfo.StorageUsed += totalStorageSize
	if err := s.tenantRepo.AdjustStorageUsed(ctx, tenantInfo.ID, totalStorageSize); err != nil {
		logger.GetLogger(ctx).WithField("error", err).Errorf("processChunks update tenant storage used failed")
	}
	logger.GetLogger(ctx).Infof("processChunks successfully")
}

// GetSummary generates a summary for knowledge content using an AI model
func (s *knowledgeService) getSummary(ctx context.Context,
	summaryModel chat.Chat, knowledge *types.Knowledge, chunks []*types.Chunk,
) (string, error) {
	// Get knowledge info from the first chunk
	if len(chunks) == 0 {
		return "", fmt.Errorf("no chunks provided for summary generation")
	}

	// concat chunk contents
	chunkContents := ""
	allImageInfos := make([]*types.ImageInfo, 0)

	// then, sort chunks by StartAt
	sortedChunks := make([]*types.Chunk, len(chunks))
	copy(sortedChunks, chunks)
	sort.Slice(sortedChunks, func(i, j int) bool {
		return sortedChunks[i].StartAt < sortedChunks[j].StartAt
	})

	// concat chunk contents and collect image infos
	for _, chunk := range sortedChunks {
		if chunk.EndAt > 4096 {
			break
		}
		// Ensure we don't slice beyond the current content length
		runes := []rune(chunkContents)
		if chunk.StartAt <= len(runes) {
			chunkContents = string(runes[:chunk.StartAt]) + chunk.Content
		} else {
			// If StartAt is beyond current content, just append
			chunkContents = chunkContents + chunk.Content
		}
		if chunk.ImageInfo != "" {
			var images []*types.ImageInfo
			if err := json.Unmarshal([]byte(chunk.ImageInfo), &images); err == nil {
				allImageInfos = append(allImageInfos, images...)
			}
		}
	}
	// remove markdown image syntax
	re := regexp.MustCompile(`!\[[^\]]*\]\([^)]+\)`)
	chunkContents = re.ReplaceAllString(chunkContents, "")
	// collect all image infos
	if len(allImageInfos) > 0 {
		// add image infos to chunk contents
		var imageAnnotations string
		for _, img := range allImageInfos {
			if img.Caption != "" {
				imageAnnotations += fmt.Sprintf("\n[图片描述: %s]", img.Caption)
			}
			if img.OCRText != "" {
				imageAnnotations += fmt.Sprintf("\n[图片文字: %s]", img.OCRText)
			}
		}

		// concat chunk contents and image annotations
		chunkContents = chunkContents + imageAnnotations
	}

	if len(chunkContents) < 300 {
		return chunkContents, nil
	}

	// Prepare content with metadata for summary generation
	contentWithMetadata := chunkContents

	// Add knowledge metadata if available
	if knowledge != nil {
		metadataIntro := fmt.Sprintf("文档类型: %s\n文件名称: %s\n", knowledge.FileType, knowledge.FileName)

		// Add additional metadata if available
		if knowledge.Type != "" {
			metadataIntro += fmt.Sprintf("知识类型: %s\n", knowledge.Type)
		}

		// Prepend metadata to content
		contentWithMetadata = metadataIntro + "\n内容:\n" + contentWithMetadata
	}

	// Generate summary using AI model
	thinking := false
	summary, err := summaryModel.Chat(ctx, []chat.Message{
		{
			Role:    "system",
			Content: s.config.Conversation.GenerateSummaryPrompt,
		},
		{
			Role:    "user",
			Content: contentWithMetadata,
		},
	}, &chat.ChatOptions{
		Temperature: 0.3,
		MaxTokens:   1024,
		Thinking:    &thinking,
	})
	if err != nil {
		logger.GetLogger(ctx).WithField("error", err).Errorf("GetSummary failed")
		return "", err
	}
	logger.GetLogger(ctx).WithField("summary", summary.Content).Infof("GetSummary success")
	return summary.Content, nil
}

// enqueueQuestionGenerationTask enqueues an async task for question generation
func (s *knowledgeService) enqueueQuestionGenerationTask(ctx context.Context,
	kbID, knowledgeID string, questionCount int,
) {
	tenantID := ctx.Value(types.TenantIDContextKey).(uint64)
	payload := types.QuestionGenerationPayload{
		TenantID:        tenantID,
		KnowledgeBaseID: kbID,
		KnowledgeID:     knowledgeID,
		QuestionCount:   questionCount,
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		logger.Errorf(ctx, "Failed to marshal question generation payload: %v", err)
		return
	}

	task := asynq.NewTask(types.TypeQuestionGeneration, payloadBytes, asynq.Queue("low"), asynq.MaxRetry(3))
	info, err := s.task.Enqueue(task)
	if err != nil {
		logger.Errorf(ctx, "Failed to enqueue question generation task: %v", err)
		return
	}
	logger.Infof(ctx, "Enqueued question generation task: %s for knowledge: %s", info.ID, knowledgeID)
}

// enqueueSummaryGenerationTask enqueues an async task for summary generation
func (s *knowledgeService) enqueueSummaryGenerationTask(ctx context.Context,
	kbID, knowledgeID string,
) {
	tenantID := ctx.Value(types.TenantIDContextKey).(uint64)
	payload := types.SummaryGenerationPayload{
		TenantID:        tenantID,
		KnowledgeBaseID: kbID,
		KnowledgeID:     knowledgeID,
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		logger.Errorf(ctx, "Failed to marshal summary generation payload: %v", err)
		return
	}

	task := asynq.NewTask(types.TypeSummaryGeneration, payloadBytes, asynq.Queue("low"), asynq.MaxRetry(3))
	info, err := s.task.Enqueue(task)
	if err != nil {
		logger.Errorf(ctx, "Failed to enqueue summary generation task: %v", err)
		return
	}
	logger.Infof(ctx, "Enqueued summary generation task: %s for knowledge: %s", info.ID, knowledgeID)
}

// ProcessSummaryGeneration handles async summary generation task
func (s *knowledgeService) ProcessSummaryGeneration(ctx context.Context, t *asynq.Task) error {
	var payload types.SummaryGenerationPayload
	if err := json.Unmarshal(t.Payload(), &payload); err != nil {
		logger.Errorf(ctx, "Failed to unmarshal summary generation payload: %v", err)
		return nil // Don't retry on unmarshal error
	}

	logger.Infof(ctx, "Processing summary generation for knowledge: %s", payload.KnowledgeID)

	// Set tenant context
	ctx = context.WithValue(ctx, types.TenantIDContextKey, payload.TenantID)

	// Get knowledge base
	kb, err := s.kbService.GetKnowledgeBaseByID(ctx, payload.KnowledgeBaseID)
	if err != nil {
		logger.Errorf(ctx, "Failed to get knowledge base: %v", err)
		return nil
	}

	// Get knowledge
	knowledge, err := s.repo.GetKnowledgeByID(ctx, payload.TenantID, payload.KnowledgeID)
	if err != nil {
		logger.Errorf(ctx, "Failed to get knowledge: %v", err)
		return nil
	}

	// Update summary status to processing
	knowledge.SummaryStatus = types.SummaryStatusProcessing
	knowledge.UpdatedAt = time.Now()
	if err := s.repo.UpdateKnowledge(ctx, knowledge); err != nil {
		logger.Warnf(ctx, "Failed to update summary status to processing: %v", err)
	}

	// Helper function to mark summary as failed
	markSummaryFailed := func() {
		knowledge.SummaryStatus = types.SummaryStatusFailed
		knowledge.UpdatedAt = time.Now()
		if err := s.repo.UpdateKnowledge(ctx, knowledge); err != nil {
			logger.Warnf(ctx, "Failed to update summary status to failed: %v", err)
		}
	}

	// Get text chunks for this knowledge
	chunks, err := s.chunkService.ListChunksByKnowledgeID(ctx, payload.KnowledgeID)
	if err != nil {
		logger.Errorf(ctx, "Failed to get chunks: %v", err)
		markSummaryFailed()
		return nil
	}

	// Filter text chunks only
	textChunks := make([]*types.Chunk, 0)
	for _, chunk := range chunks {
		if chunk.ChunkType == types.ChunkTypeText {
			textChunks = append(textChunks, chunk)
		}
	}

	if len(textChunks) == 0 {
		logger.Infof(ctx, "No text chunks found for knowledge: %s", payload.KnowledgeID)
		// Mark as completed since there's nothing to summarize
		knowledge.SummaryStatus = types.SummaryStatusCompleted
		knowledge.UpdatedAt = time.Now()
		s.repo.UpdateKnowledge(ctx, knowledge)
		return nil
	}

	// Sort chunks by ChunkIndex for proper ordering
	sort.Slice(textChunks, func(i, j int) bool {
		return textChunks[i].ChunkIndex < textChunks[j].ChunkIndex
	})

	// Initialize chat model for summary
	chatModel, err := s.modelService.GetChatModel(ctx, kb.SummaryModelID)
	if err != nil {
		logger.Errorf(ctx, "Failed to get chat model: %v", err)
		markSummaryFailed()
		return fmt.Errorf("failed to get chat model: %w", err)
	}

	// Generate summary
	summary, err := s.getSummary(ctx, chatModel, knowledge, textChunks)
	if err != nil {
		logger.Errorf(ctx, "Failed to generate summary for knowledge %s: %v", payload.KnowledgeID, err)
		// Use first chunk content as fallback
		if len(textChunks) > 0 {
			summary = textChunks[0].Content
			if len(summary) > 500 {
				summary = summary[:500]
			}
		}
	}

	// Update knowledge description
	knowledge.Description = summary
	knowledge.SummaryStatus = types.SummaryStatusCompleted
	knowledge.UpdatedAt = time.Now()
	if err := s.repo.UpdateKnowledge(ctx, knowledge); err != nil {
		logger.Errorf(ctx, "Failed to update knowledge description: %v", err)
		return fmt.Errorf("failed to update knowledge: %w", err)
	}

	// Create summary chunk and index it
	if strings.TrimSpace(summary) != "" {
		// Get max chunk index
		maxChunkIndex := 0
		for _, chunk := range chunks {
			if chunk.ChunkIndex > maxChunkIndex {
				maxChunkIndex = chunk.ChunkIndex
			}
		}

		summaryChunk := &types.Chunk{
			ID:              uuid.New().String(),
			TenantID:        knowledge.TenantID,
			KnowledgeID:     knowledge.ID,
			KnowledgeBaseID: knowledge.KnowledgeBaseID,
			Content:         fmt.Sprintf("# 文档名称\n%s\n\n# 摘要\n%s", knowledge.FileName, summary),
			ChunkIndex:      maxChunkIndex + 1,
			IsEnabled:       true,
			CreatedAt:       time.Now(),
			UpdatedAt:       time.Now(),
			StartAt:         0,
			EndAt:           0,
			ChunkType:       types.ChunkTypeSummary,
			ParentChunkID:   textChunks[0].ID,
		}

		// Save summary chunk
		if err := s.chunkService.CreateChunks(ctx, []*types.Chunk{summaryChunk}); err != nil {
			logger.Errorf(ctx, "Failed to create summary chunk: %v", err)
			return fmt.Errorf("failed to create summary chunk: %w", err)
		}

		// Index summary chunk
		tenantInfo, err := s.tenantRepo.GetTenantByID(ctx, payload.TenantID)
		if err != nil {
			logger.Errorf(ctx, "Failed to get tenant info: %v", err)
			return fmt.Errorf("failed to get tenant info: %w", err)
		}
		ctx = context.WithValue(ctx, types.TenantInfoContextKey, tenantInfo)

		retrieveEngine, err := retriever.NewCompositeRetrieveEngine(s.retrieveEngine, tenantInfo.GetEffectiveEngines())
		if err != nil {
			logger.Errorf(ctx, "Failed to init retrieve engine: %v", err)
			return fmt.Errorf("failed to init retrieve engine: %w", err)
		}

		embeddingModel, err := s.modelService.GetEmbeddingModel(ctx, kb.EmbeddingModelID)
		if err != nil {
			logger.Errorf(ctx, "Failed to get embedding model: %v", err)
			return fmt.Errorf("failed to get embedding model: %w", err)
		}

		indexInfo := []*types.IndexInfo{{
			Content:         summaryChunk.Content,
			SourceID:        summaryChunk.ID,
			SourceType:      types.ChunkSourceType,
			ChunkID:         summaryChunk.ID,
			KnowledgeID:     knowledge.ID,
			KnowledgeBaseID: knowledge.KnowledgeBaseID,
		}}

		if err := retrieveEngine.BatchIndex(ctx, embeddingModel, indexInfo); err != nil {
			logger.Errorf(ctx, "Failed to index summary chunk: %v", err)
			return fmt.Errorf("failed to index summary chunk: %w", err)
		}

		logger.Infof(ctx, "Successfully created and indexed summary chunk for knowledge: %s", payload.KnowledgeID)
	}

	logger.Infof(ctx, "Successfully generated summary for knowledge: %s", payload.KnowledgeID)
	return nil
}

// ProcessQuestionGeneration handles async question generation task
func (s *knowledgeService) ProcessQuestionGeneration(ctx context.Context, t *asynq.Task) error {
	ctx, span := tracing.ContextWithSpan(ctx, "knowledgeService.ProcessQuestionGeneration")
	defer span.End()

	var payload types.QuestionGenerationPayload
	if err := json.Unmarshal(t.Payload(), &payload); err != nil {
		logger.Errorf(ctx, "Failed to unmarshal question generation payload: %v", err)
		return nil // Don't retry on unmarshal error
	}

	logger.Infof(ctx, "Processing question generation for knowledge: %s", payload.KnowledgeID)

	// Set tenant context
	ctx = context.WithValue(ctx, types.TenantIDContextKey, payload.TenantID)

	// Get knowledge base
	kb, err := s.kbService.GetKnowledgeBaseByID(ctx, payload.KnowledgeBaseID)
	if err != nil {
		logger.Errorf(ctx, "Failed to get knowledge base: %v", err)
		return nil
	}

	// Get knowledge
	knowledge, err := s.repo.GetKnowledgeByID(ctx, payload.TenantID, payload.KnowledgeID)
	if err != nil {
		logger.Errorf(ctx, "Failed to get knowledge: %v", err)
		return nil
	}

	// Get text chunks for this knowledge
	chunks, err := s.chunkService.ListChunksByKnowledgeID(ctx, payload.KnowledgeID)
	if err != nil {
		logger.Errorf(ctx, "Failed to get chunks: %v", err)
		return nil
	}

	// Filter text chunks only
	textChunks := make([]*types.Chunk, 0)
	for _, chunk := range chunks {
		if chunk.ChunkType == types.ChunkTypeText {
			textChunks = append(textChunks, chunk)
		}
	}

	if len(textChunks) == 0 {
		logger.Infof(ctx, "No text chunks found for knowledge: %s", payload.KnowledgeID)
		return nil
	}

	// Sort chunks by StartAt for context building
	sort.Slice(textChunks, func(i, j int) bool {
		return textChunks[i].StartAt < textChunks[j].StartAt
	})

	// Initialize chat model
	chatModel, err := s.modelService.GetChatModel(ctx, kb.SummaryModelID)
	if err != nil {
		logger.Errorf(ctx, "Failed to get chat model: %v", err)
		return fmt.Errorf("failed to get chat model: %w", err)
	}

	// Initialize embedding model and retrieval engine
	embeddingModel, err := s.modelService.GetEmbeddingModel(ctx, kb.EmbeddingModelID)
	if err != nil {
		logger.Errorf(ctx, "Failed to get embedding model: %v", err)
		return fmt.Errorf("failed to get embedding model: %w", err)
	}

	tenantInfo, err := s.tenantRepo.GetTenantByID(ctx, payload.TenantID)
	if err != nil {
		logger.Errorf(ctx, "Failed to get tenant info: %v", err)
		return fmt.Errorf("failed to get tenant info: %w", err)
	}
	ctx = context.WithValue(ctx, types.TenantInfoContextKey, tenantInfo)

	retrieveEngine, err := retriever.NewCompositeRetrieveEngine(s.retrieveEngine, tenantInfo.GetEffectiveEngines())
	if err != nil {
		logger.Errorf(ctx, "Failed to init retrieve engine: %v", err)
		return fmt.Errorf("failed to init retrieve engine: %w", err)
	}

	questionCount := payload.QuestionCount
	if questionCount <= 0 {
		questionCount = 3
	}
	if questionCount > 10 {
		questionCount = 10
	}

	// Generate questions for each chunk with context
	var indexInfoList []*types.IndexInfo
	for i, chunk := range textChunks {
		// Build context from adjacent chunks
		var prevContent, nextContent string
		if i > 0 {
			prevContent = textChunks[i-1].Content
			// Limit context size
			if len(prevContent) > 500 {
				prevContent = prevContent[len(prevContent)-500:]
			}
		}
		if i < len(textChunks)-1 {
			nextContent = textChunks[i+1].Content
			// Limit context size
			if len(nextContent) > 500 {
				nextContent = nextContent[:500]
			}
		}

		questions, err := s.generateQuestionsWithContext(ctx, chatModel, chunk.Content, prevContent, nextContent, knowledge.Title, questionCount)
		if err != nil {
			logger.Warnf(ctx, "Failed to generate questions for chunk %s: %v", chunk.ID, err)
			continue
		}

		if len(questions) == 0 {
			continue
		}

		// Update chunk metadata with unique IDs for each question
		generatedQuestions := make([]types.GeneratedQuestion, len(questions))
		for j, question := range questions {
			questionID := fmt.Sprintf("q%d", time.Now().UnixNano()+int64(j))
			generatedQuestions[j] = types.GeneratedQuestion{
				ID:       questionID,
				Question: question,
			}
		}
		meta := &types.DocumentChunkMetadata{
			GeneratedQuestions: generatedQuestions,
		}
		if err := chunk.SetDocumentMetadata(meta); err != nil {
			logger.Warnf(ctx, "Failed to set document metadata for chunk %s: %v", chunk.ID, err)
			continue
		}

		// Update chunk in database
		if err := s.chunkService.UpdateChunk(ctx, chunk); err != nil {
			logger.Warnf(ctx, "Failed to update chunk %s: %v", chunk.ID, err)
			continue
		}

		// Create index entries for generated questions
		for _, gq := range generatedQuestions {
			sourceID := fmt.Sprintf("%s-%s", chunk.ID, gq.ID)
			indexInfoList = append(indexInfoList, &types.IndexInfo{
				Content:         gq.Question,
				SourceID:        sourceID,
				SourceType:      types.ChunkSourceType,
				ChunkID:         chunk.ID,
				KnowledgeID:     knowledge.ID,
				KnowledgeBaseID: knowledge.KnowledgeBaseID,
			})
		}
		logger.Debugf(ctx, "Generated %d questions for chunk %s", len(questions), chunk.ID)
	}

	// Index generated questions
	if len(indexInfoList) > 0 {
		if err := retrieveEngine.BatchIndex(ctx, embeddingModel, indexInfoList); err != nil {
			logger.Errorf(ctx, "Failed to index generated questions: %v", err)
			return fmt.Errorf("failed to index questions: %w", err)
		}
		logger.Infof(ctx, "Successfully indexed %d generated questions for knowledge: %s", len(indexInfoList), payload.KnowledgeID)
	}

	return nil
}

// generateQuestionsWithContext generates questions for a chunk with surrounding context
func (s *knowledgeService) generateQuestionsWithContext(ctx context.Context,
	chatModel chat.Chat, content, prevContent, nextContent, docName string, questionCount int,
) ([]string, error) {
	if content == "" || questionCount <= 0 {
		return nil, nil
	}

	// Build prompt with context
	prompt := s.config.Conversation.GenerateQuestionsPrompt
	if prompt == "" {
		prompt = defaultQuestionGenerationPrompt
	}

	// Build context section
	var contextSection string
	if prevContent != "" || nextContent != "" {
		contextSection = "## 上下文信息（仅供参考，帮助理解主要内容）\n"
		if prevContent != "" {
			contextSection += fmt.Sprintf("【前文】%s\n", prevContent)
		}
		if nextContent != "" {
			contextSection += fmt.Sprintf("【后文】%s\n", nextContent)
		}
		contextSection += "\n"
	}

	// Replace placeholders
	prompt = strings.ReplaceAll(prompt, "{{question_count}}", fmt.Sprintf("%d", questionCount))
	prompt = strings.ReplaceAll(prompt, "{{content}}", content)
	prompt = strings.ReplaceAll(prompt, "{{context}}", contextSection)
	prompt = strings.ReplaceAll(prompt, "{{doc_name}}", docName)

	thinking := false
	response, err := chatModel.Chat(ctx, []chat.Message{
		{
			Role:    "user",
			Content: prompt,
		},
	}, &chat.ChatOptions{
		Temperature: 0.7,
		MaxTokens:   512,
		Thinking:    &thinking,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to generate questions: %w", err)
	}

	// Parse response
	lines := strings.Split(response.Content, "\n")
	questions := make([]string, 0, questionCount)
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		line = strings.TrimLeft(line, "0123456789.-*) ")
		line = strings.TrimSpace(line)
		if line != "" && len(line) > 5 {
			questions = append(questions, line)
			if len(questions) >= questionCount {
				break
			}
		}
	}

	return questions, nil
}

// Default prompt for question generation with context support
const defaultQuestionGenerationPrompt = `你是一个专业的问题生成助手。你的任务是根据给定的【主要内容】生成用户可能会问的相关问题。

{{context}}
## 主要内容（请基于此内容生成问题）
文档名称：{{doc_name}}
文档内容：
{{content}}

## 核心要求
- 生成的问题必须与【主要内容】直接相关
- 问题中禁止使用任何代词或指代词（如"它"、"这个"、"该文档"、"本文"、"文中"、"其"等），必须用具体名称替代
- 问题必须是完整独立的，脱离上下文也能被理解
- 问题应该是用户在实际场景中可能会提出的自然问题
- 问题应该多样化，覆盖内容的不同方面
- 每个问题应该简洁明了，长度控制在30字以内
- 生成的问题数量为 {{question_count}} 个

## 问题类型建议
- 定义类：什么是...？...是什么？
- 原因类：为什么...？...的原因是什么？
- 方法类：如何...？怎样...？
- 比较类：...和...有什么区别？
- 应用类：...可以用于什么场景？

## 输出格式
直接输出问题列表，每行一个问题，不要有序号或其他前缀。`

// GetKnowledgeFile retrieves the physical file associated with a knowledge entry
func (s *knowledgeService) GetKnowledgeFile(ctx context.Context, id string) (io.ReadCloser, string, error) {
	// Get knowledge record
	tenantID := ctx.Value(types.TenantIDContextKey).(uint64)
	knowledge, err := s.repo.GetKnowledgeByID(ctx, tenantID, id)
	if err != nil {
		return nil, "", err
	}

	// Get the file from storage
	file, err := s.fileSvc.GetFile(ctx, knowledge.FilePath)
	if err != nil {
		return nil, "", err
	}

	return file, knowledge.FileName, nil
}

func (s *knowledgeService) UpdateKnowledge(ctx context.Context, knowledge *types.Knowledge) error {
	record, err := s.repo.GetKnowledgeByID(ctx, ctx.Value(types.TenantIDContextKey).(uint64), knowledge.ID)
	if err != nil {
		logger.Errorf(ctx, "Failed to get knowledge record: %v", err)
		return err
	}
	// if need other fields update, please add here
	if knowledge.Title != "" {
		record.Title = knowledge.Title
	}

	// Update knowledge record in the repository
	if err := s.repo.UpdateKnowledge(ctx, record); err != nil {
		logger.Errorf(ctx, "Failed to update knowledge: %v", err)
		return err
	}
	logger.Infof(ctx, "Knowledge updated successfully, ID: %s", knowledge.ID)
	return nil
}

// UpdateManualKnowledge updates manual Markdown knowledge content.
func (s *knowledgeService) UpdateManualKnowledge(ctx context.Context,
	knowledgeID string, payload *types.ManualKnowledgePayload,
) (*types.Knowledge, error) {
	logger.Info(ctx, "Start updating manual knowledge entry")
	if payload == nil {
		return nil, werrors.NewBadRequestError("请求内容不能为空")
	}

	cleanContent := secutils.CleanMarkdown(payload.Content)
	if strings.TrimSpace(cleanContent) == "" {
		return nil, werrors.NewValidationError("内容不能为空")
	}
	if len([]rune(cleanContent)) > manualContentMaxLength {
		return nil, werrors.NewValidationError(fmt.Sprintf("内容长度超出限制（最多%d个字符）", manualContentMaxLength))
	}

	safeTitle, ok := secutils.ValidateInput(payload.Title)
	if !ok {
		return nil, werrors.NewValidationError("标题包含非法字符或超出长度限制")
	}

	status := strings.ToLower(strings.TrimSpace(payload.Status))
	if status == "" {
		status = types.ManualKnowledgeStatusDraft
	}
	if status != types.ManualKnowledgeStatusDraft && status != types.ManualKnowledgeStatusPublish {
		return nil, werrors.NewValidationError("状态仅支持 draft 或 publish")
	}

	tenantID := ctx.Value(types.TenantIDContextKey).(uint64)
	existing, err := s.repo.GetKnowledgeByID(ctx, tenantID, knowledgeID)
	if err != nil {
		logger.Errorf(ctx, "Failed to load knowledge: %v", err)
		return nil, err
	}
	if !existing.IsManual() {
		return nil, werrors.NewBadRequestError("仅支持手工知识的在线编辑")
	}

	kb, err := s.kbService.GetKnowledgeBaseByID(ctx, existing.KnowledgeBaseID)
	if err != nil {
		logger.Errorf(ctx, "Failed to get knowledge base for manual update: %v", err)
		return nil, err
	}

	var version int
	if meta, err := existing.ManualMetadata(); err == nil && meta != nil {
		version = meta.Version + 1
	} else {
		version = 1
	}

	meta := types.NewManualKnowledgeMetadata(cleanContent, status, version)
	if err := existing.SetManualMetadata(meta); err != nil {
		logger.Errorf(ctx, "Failed to set manual metadata during update: %v", err)
		return nil, err
	}

	if safeTitle != "" {
		existing.Title = safeTitle
	} else if existing.Title == "" {
		existing.Title = fmt.Sprintf("手工知识-%s", time.Now().Format("20060102-150405"))
	}
	existing.FileName = ensureManualFileName(existing.Title)
	existing.FileType = types.KnowledgeTypeManual
	existing.Type = types.KnowledgeTypeManual
	existing.Source = types.KnowledgeTypeManual
	existing.EnableStatus = "disabled"
	existing.UpdatedAt = time.Now()

	if err := s.cleanupKnowledgeResources(ctx, existing); err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"knowledge_id": knowledgeID,
		})
		return nil, err
	}

	existing.EmbeddingModelID = kb.EmbeddingModelID

	if status == types.ManualKnowledgeStatusDraft {
		existing.ParseStatus = types.ManualKnowledgeStatusDraft
		existing.Description = ""
		existing.ProcessedAt = nil

		if err := s.repo.UpdateKnowledge(ctx, existing); err != nil {
			logger.Errorf(ctx, "Failed to persist manual draft: %v", err)
			return nil, err
		}
		return existing, nil
	}

	existing.ParseStatus = "pending"
	existing.Description = ""
	existing.ProcessedAt = nil

	if err := s.repo.UpdateKnowledge(ctx, existing); err != nil {
		logger.Errorf(ctx, "Failed to persist manual knowledge before indexing: %v", err)
		return nil, err
	}

	logger.Infof(ctx, "Manual knowledge updated, scheduling indexing, ID: %s", existing.ID)
	s.triggerManualProcessing(ctx, kb, existing, cleanContent, false)
	return existing, nil
}

// isValidFileType checks if a file type is supported
func isValidFileType(filename string) bool {
	switch strings.ToLower(getFileType(filename)) {
	case "pdf", "txt", "docx", "doc", "md", "markdown", "png", "jpg", "jpeg", "gif", "csv", "xlsx", "xls":
		return true
	default:
		return false
	}
}

// getFileType extracts the file extension from a filename
func getFileType(filename string) string {
	ext := strings.Split(filename, ".")
	if len(ext) < 2 {
		return "unknown"
	}
	return ext[len(ext)-1]
}

// isValidURL verifies if a URL is valid
// isValidURL 检查URL是否有效
func isValidURL(url string) bool {
	if strings.HasPrefix(url, "http://") || strings.HasPrefix(url, "https://") {
		return true
	}
	return false
}

// GetKnowledgeBatch retrieves multiple knowledge entries by their IDs
func (s *knowledgeService) GetKnowledgeBatch(ctx context.Context,
	tenantID uint64, ids []string,
) ([]*types.Knowledge, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	return s.repo.GetKnowledgeBatch(ctx, tenantID, ids)
}

// calculateFileHash calculates MD5 hash of a file
func calculateFileHash(file *multipart.FileHeader) (string, error) {
	f, err := file.Open()
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := md5.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}

	// Reset file pointer for subsequent operations
	if _, err := f.Seek(0, 0); err != nil {
		return "", err
	}

	return hex.EncodeToString(h.Sum(nil)), nil
}

func calculateStr(strList ...string) string {
	h := md5.New()
	input := strings.Join(strList, "")
	h.Write([]byte(input))
	return hex.EncodeToString(h.Sum(nil))
}

func (s *knowledgeService) CloneKnowledgeBase(ctx context.Context, srcID, dstID string) error {
	srcKB, dstKB, err := s.kbService.CopyKnowledgeBase(ctx, srcID, dstID)
	if err != nil {
		logger.Errorf(ctx, "Failed to copy knowledge base: %v", err)
		return err
	}

	addKnowledge, err := s.repo.AminusB(ctx, srcKB.TenantID, srcKB.ID, dstKB.TenantID, dstKB.ID)
	if err != nil {
		logger.Errorf(ctx, "Failed to get knowledge: %v", err)
		return err
	}

	delKnowledge, err := s.repo.AminusB(ctx, dstKB.TenantID, dstKB.ID, srcKB.TenantID, srcKB.ID)
	if err != nil {
		logger.Errorf(ctx, "Failed to get knowledge: %v", err)
		return err
	}
	logger.Infof(ctx, "Knowledge after update to add: %d, delete: %d", len(addKnowledge), len(delKnowledge))

	batch := 10
	g, gctx := errgroup.WithContext(ctx)
	for ids := range slices.Chunk(delKnowledge, batch) {
		g.Go(func() error {
			err := s.DeleteKnowledgeList(gctx, ids)
			if err != nil {
				logger.Errorf(gctx, "delete partial knowledge %v: %w", ids, err)
				return err
			}
			return nil
		})
	}
	err = g.Wait()
	if err != nil {
		logger.Errorf(ctx, "delete total knowledge %d: %v", len(delKnowledge), err)
		return err
	}

	// Copy context out of auto-stop task
	g, gctx = errgroup.WithContext(ctx)
	g.SetLimit(batch)
	for _, knowledge := range addKnowledge {
		g.Go(func() error {
			srcKn, err := s.repo.GetKnowledgeByID(gctx, srcKB.TenantID, knowledge)
			if err != nil {
				logger.Errorf(gctx, "get knowledge %s: %w", knowledge, err)
				return err
			}
			err = s.cloneKnowledge(gctx, srcKn, dstKB)
			if err != nil {
				logger.Errorf(gctx, "clone knowledge %s: %w", knowledge, err)
				return err
			}
			return nil
		})
	}
	err = g.Wait()
	if err != nil {
		logger.Errorf(ctx, "add total knowledge %d: %v", len(addKnowledge), err)
		return err
	}
	return nil
}

func (s *knowledgeService) updateChunkVector(ctx context.Context, kbID string, chunks []*types.Chunk) error {
	// Get embedding model from knowledge base
	sourceKB, err := s.kbService.GetKnowledgeBaseByID(ctx, kbID)
	if err != nil {
		return err
	}
	embeddingModel, err := s.modelService.GetEmbeddingModel(ctx, sourceKB.EmbeddingModelID)
	if err != nil {
		return err
	}

	// Initialize composite retrieve engine from tenant configuration
	indexInfo := make([]*types.IndexInfo, 0, len(chunks))
	ids := make([]string, 0, len(chunks))
	for _, chunk := range chunks {
		if chunk.KnowledgeBaseID != kbID {
			logger.Warnf(ctx, "Knowledge base ID mismatch: %s != %s", chunk.KnowledgeBaseID, kbID)
			continue
		}
		indexInfo = append(indexInfo, &types.IndexInfo{
			Content:         chunk.Content,
			SourceID:        chunk.ID,
			SourceType:      types.ChunkSourceType,
			ChunkID:         chunk.ID,
			KnowledgeID:     chunk.KnowledgeID,
			KnowledgeBaseID: chunk.KnowledgeBaseID,
		})
		ids = append(ids, chunk.ID)
	}

	tenantInfo := ctx.Value(types.TenantInfoContextKey).(*types.Tenant)
	retrieveEngine, err := retriever.NewCompositeRetrieveEngine(s.retrieveEngine, tenantInfo.GetEffectiveEngines())
	if err != nil {
		return err
	}

	// Delete old vector representation of the chunk
	err = retrieveEngine.DeleteByChunkIDList(ctx, ids, embeddingModel.GetDimensions(), sourceKB.Type)
	if err != nil {
		return err
	}

	// Index updated chunk content with new vector representation
	err = retrieveEngine.BatchIndex(ctx, embeddingModel, indexInfo)
	if err != nil {
		return err
	}
	return nil
}

func (s *knowledgeService) UpdateImageInfo(
	ctx context.Context,
	knowledgeID string,
	chunkID string,
	imageInfo string,
) error {
	var images []*types.ImageInfo
	if err := json.Unmarshal([]byte(imageInfo), &images); err != nil {
		logger.Errorf(ctx, "Failed to unmarshal image info: %v", err)
		return err
	}
	if len(images) != 1 {
		logger.Warnf(ctx, "Expected exactly one image info, got %d", len(images))
		return nil
	}
	image := images[0]

	// Retrieve all chunks with the given parent chunk ID
	chunk, err := s.chunkService.GetChunkByID(ctx, chunkID)
	if err != nil {
		logger.Errorf(ctx, "Failed to get chunk: %v", err)
		return err
	}
	chunk.ImageInfo = imageInfo
	tenantID := ctx.Value(types.TenantIDContextKey).(uint64)
	chunkChildren, err := s.chunkService.ListChunkByParentID(ctx, tenantID, chunkID)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"parent_chunk_id": chunkID,
			"tenant_id":       tenantID,
		})
		return err
	}
	logger.Infof(ctx, "Found %d chunks with parent chunk ID: %s", len(chunkChildren), chunkID)

	// Iterate through each chunk and update its content based on the image information
	updateChunk := []*types.Chunk{chunk}
	var addChunk []*types.Chunk

	// Track whether we've found OCR and caption child chunks for this image
	hasOCRChunk := false
	hasCaptionChunk := false

	for i, child := range chunkChildren {
		// Skip chunks that are not image types
		var cImageInfo []*types.ImageInfo
		err = json.Unmarshal([]byte(child.ImageInfo), &cImageInfo)
		if err != nil {
			logger.Warnf(ctx, "Failed to unmarshal image %s info: %v", child.ID, err)
			continue
		}
		if len(cImageInfo) == 0 {
			continue
		}
		if cImageInfo[0].OriginalURL != image.OriginalURL {
			logger.Warnf(ctx, "Skipping chunk ID: %s, image URL mismatch: %s != %s",
				child.ID, cImageInfo[0].OriginalURL, image.OriginalURL)
			continue
		}

		// Mark that we've found chunks for this image
		switch child.ChunkType {
		case types.ChunkTypeImageCaption:
			hasCaptionChunk = true
			// Update caption if it has changed
			if image.Caption != cImageInfo[0].Caption {
				child.Content = image.Caption
				child.ImageInfo = imageInfo
				updateChunk = append(updateChunk, chunkChildren[i])
			}
		case types.ChunkTypeImageOCR:
			hasOCRChunk = true
			// Update OCR if it has changed
			if image.OCRText != cImageInfo[0].OCRText {
				child.Content = image.OCRText
				child.ImageInfo = imageInfo
				updateChunk = append(updateChunk, chunkChildren[i])
			}
		}
	}

	// Create a new caption chunk if it doesn't exist and we have caption data
	if !hasCaptionChunk && image.Caption != "" {
		captionChunk := &types.Chunk{
			ID:              uuid.New().String(),
			TenantID:        tenantID,
			KnowledgeID:     chunk.KnowledgeID,
			KnowledgeBaseID: chunk.KnowledgeBaseID,
			Content:         image.Caption,
			ChunkType:       types.ChunkTypeImageCaption,
			ParentChunkID:   chunk.ID,
			ImageInfo:       imageInfo,
		}
		addChunk = append(addChunk, captionChunk)
		logger.Infof(ctx, "Created new caption chunk ID: %s for image URL: %s", captionChunk.ID, image.OriginalURL)
	}

	// Create a new OCR chunk if it doesn't exist and we have OCR data
	if !hasOCRChunk && image.OCRText != "" {
		ocrChunk := &types.Chunk{
			ID:              uuid.New().String(),
			TenantID:        tenantID,
			KnowledgeID:     chunk.KnowledgeID,
			KnowledgeBaseID: chunk.KnowledgeBaseID,
			Content:         image.OCRText,
			ChunkType:       types.ChunkTypeImageOCR,
			ParentChunkID:   chunk.ID,
			ImageInfo:       imageInfo,
		}
		addChunk = append(addChunk, ocrChunk)
		logger.Infof(ctx, "Created new OCR chunk ID: %s for image URL: %s", ocrChunk.ID, image.OriginalURL)
	}
	logger.Infof(ctx, "Updated %d chunks out of %d total chunks", len(updateChunk), len(chunkChildren)+1)

	if len(addChunk) > 0 {
		err := s.chunkService.CreateChunks(ctx, addChunk)
		if err != nil {
			logger.ErrorWithFields(ctx, err, map[string]interface{}{
				"add_chunk_size": len(addChunk),
			})
			return err
		}
	}

	// Update the chunks
	for _, c := range updateChunk {
		err := s.chunkService.UpdateChunk(ctx, c)
		if err != nil {
			logger.ErrorWithFields(ctx, err, map[string]interface{}{
				"chunk_id":     c.ID,
				"knowledge_id": c.KnowledgeID,
			})
			return err
		}
	}

	// Update the chunk vector
	err = s.updateChunkVector(ctx, chunk.KnowledgeBaseID, append(updateChunk, addChunk...))
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"chunk_id":     chunk.ID,
			"knowledge_id": chunk.KnowledgeID,
		})
		return err
	}

	// Update the knowledge file hash
	knowledge, err := s.repo.GetKnowledgeByID(ctx, tenantID, knowledgeID)
	if err != nil {
		logger.Errorf(ctx, "Failed to get knowledge: %v", err)
		return err
	}
	fileHash := calculateStr(knowledgeID, knowledge.FileHash, imageInfo)
	knowledge.FileHash = fileHash
	err = s.repo.UpdateKnowledge(ctx, knowledge)
	if err != nil {
		logger.Warnf(ctx, "Failed to update knowledge file hash: %v", err)
	}

	logger.Infof(ctx, "Updated chunk successfully, chunk ID: %s, knowledge ID: %s", chunk.ID, chunk.KnowledgeID)
	return nil
}

// CloneChunk clone chunks from one knowledge to another
// This method transfers a chunk from a source knowledge document to a target knowledge document
// It handles the creation of new chunks in the target knowledge and updates the vector database accordingly
// Parameters:
//   - ctx: Context with authentication and request information
//   - src: Source knowledge document containing the chunk to move
//   - dst: Target knowledge document where the chunk will be moved
//
// Returns:
//   - error: Any error encountered during the move operation
//
// This method handles the chunk transfer logic, including creating new chunks in the target knowledge
// and updating the vector database representation of the moved chunks.
// It also ensures that the chunk's relationships (like pre and next chunk IDs) are maintained
// by mapping the source chunk IDs to the new target chunk IDs.
func (s *knowledgeService) CloneChunk(ctx context.Context, src, dst *types.Knowledge) error {
	chunkPage := 1
	chunkPageSize := 100
	srcTodst := map[string]string{}
	tagIDMapping := map[string]string{} // srcTagID -> dstTagID
	targetChunks := make([]*types.Chunk, 0, 10)
	chunkType := []types.ChunkType{
		types.ChunkTypeText, types.ChunkTypeSummary,
		types.ChunkTypeImageCaption, types.ChunkTypeImageOCR,
	}
	for {
		sourceChunks, _, err := s.chunkRepo.ListPagedChunksByKnowledgeID(ctx,
			src.TenantID,
			src.ID,
			&types.Pagination{
				Page:     chunkPage,
				PageSize: chunkPageSize,
			},
			chunkType,
			"",
			"",
			"",
			"",
			"",
		)
		chunkPage++
		if err != nil {
			return err
		}
		if len(sourceChunks) == 0 {
			break
		}
		now := time.Now()
		for _, sourceChunk := range sourceChunks {
			// Map TagID to target knowledge base
			targetTagID := ""
			if sourceChunk.TagID != "" {
				if mappedTagID, ok := tagIDMapping[sourceChunk.TagID]; ok {
					targetTagID = mappedTagID
				} else {
					// Try to find or create the tag in target knowledge base
					targetTagID = s.getOrCreateTagInTarget(ctx, src.TenantID, dst.TenantID, dst.KnowledgeBaseID, sourceChunk.TagID, tagIDMapping)
				}
			}

			targetChunk := &types.Chunk{
				ID:              uuid.New().String(),
				TenantID:        dst.TenantID,
				KnowledgeID:     dst.ID,
				KnowledgeBaseID: dst.KnowledgeBaseID,
				TagID:           targetTagID,
				Content:         sourceChunk.Content,
				ChunkIndex:      sourceChunk.ChunkIndex,
				IsEnabled:       sourceChunk.IsEnabled,
				Flags:           sourceChunk.Flags,
				Status:          sourceChunk.Status,
				StartAt:         sourceChunk.StartAt,
				EndAt:           sourceChunk.EndAt,
				PreChunkID:      sourceChunk.PreChunkID,
				NextChunkID:     sourceChunk.NextChunkID,
				ChunkType:       sourceChunk.ChunkType,
				ParentChunkID:   sourceChunk.ParentChunkID,
				Metadata:        sourceChunk.Metadata,
				ContentHash:     sourceChunk.ContentHash,
				ImageInfo:       sourceChunk.ImageInfo,
				CreatedAt:       now,
				UpdatedAt:       now,
			}
			targetChunks = append(targetChunks, targetChunk)
			srcTodst[sourceChunk.ID] = targetChunk.ID
		}
	}
	for _, targetChunk := range targetChunks {
		if val, ok := srcTodst[targetChunk.PreChunkID]; ok {
			targetChunk.PreChunkID = val
		} else {
			targetChunk.PreChunkID = ""
		}
		if val, ok := srcTodst[targetChunk.NextChunkID]; ok {
			targetChunk.NextChunkID = val
		} else {
			targetChunk.NextChunkID = ""
		}
		if val, ok := srcTodst[targetChunk.ParentChunkID]; ok {
			targetChunk.ParentChunkID = val
		} else {
			targetChunk.ParentChunkID = ""
		}
	}
	for chunks := range slices.Chunk(targetChunks, chunkPageSize) {
		err := s.chunkRepo.CreateChunks(ctx, chunks)
		if err != nil {
			return err
		}
	}

	tenantInfo := ctx.Value(types.TenantInfoContextKey).(*types.Tenant)
	retrieveEngine, err := retriever.NewCompositeRetrieveEngine(s.retrieveEngine, tenantInfo.GetEffectiveEngines())
	if err != nil {
		return err
	}
	embeddingModel, err := s.modelService.GetEmbeddingModel(ctx, dst.EmbeddingModelID)
	if err != nil {
		return err
	}
	if err := retrieveEngine.CopyIndices(ctx, src.KnowledgeBaseID, dst.KnowledgeBaseID,
		map[string]string{src.ID: dst.ID},
		srcTodst,
		embeddingModel.GetDimensions(),
		dst.Type,
	); err != nil {
		return err
	}
	return nil
}

// ListFAQEntries lists FAQ entries under a FAQ knowledge base.
func (s *knowledgeService) ListFAQEntries(ctx context.Context,
	kbID string, page *types.Pagination, tagSeqID int64, keyword string, searchField string, sortOrder string,
) (*types.PageResult, error) {
	if page == nil {
		page = &types.Pagination{}
	}
	keyword = strings.TrimSpace(keyword)
	kb, err := s.validateFAQKnowledgeBase(ctx, kbID)
	if err != nil {
		return nil, err
	}
	tenantID := ctx.Value(types.TenantIDContextKey).(uint64)
	faqKnowledge, err := s.findFAQKnowledge(ctx, tenantID, kb.ID)
	if err != nil {
		return nil, err
	}
	if faqKnowledge == nil {
		return types.NewPageResult(0, page, []*types.FAQEntry{}), nil
	}

	// Convert tagSeqID to tagID (UUID)
	var tagID string
	if tagSeqID > 0 {
		tag, err := s.tagRepo.GetBySeqID(ctx, tenantID, tagSeqID)
		if err != nil {
			return nil, werrors.NewNotFoundError("标签不存在")
		}
		tagID = tag.ID
	}

	chunkType := []types.ChunkType{types.ChunkTypeFAQ}
	chunks, total, err := s.chunkRepo.ListPagedChunksByKnowledgeID(
		ctx, tenantID, faqKnowledge.ID, page, chunkType, tagID, keyword, searchField, sortOrder, types.KnowledgeTypeFAQ,
	)
	if err != nil {
		return nil, err
	}

	// Build tag ID to name and seq_id mapping for all unique tag IDs (batch query)
	tagNameMap := make(map[string]string)
	tagSeqIDMap := make(map[string]int64)
	tagIDs := make([]string, 0)
	tagIDSet := make(map[string]struct{})
	for _, chunk := range chunks {
		if chunk.TagID != "" {
			if _, exists := tagIDSet[chunk.TagID]; !exists {
				tagIDSet[chunk.TagID] = struct{}{}
				tagIDs = append(tagIDs, chunk.TagID)
			}
		}
	}
	if len(tagIDs) > 0 {
		tags, err := s.tagRepo.GetByIDs(ctx, tenantID, tagIDs)
		if err == nil {
			for _, tag := range tags {
				tagNameMap[tag.ID] = tag.Name
				tagSeqIDMap[tag.ID] = tag.SeqID
			}
		}
	}

	kb.EnsureDefaults()
	entries := make([]*types.FAQEntry, 0, len(chunks))
	for _, chunk := range chunks {
		entry, err := s.chunkToFAQEntry(chunk, kb, tagSeqIDMap)
		if err != nil {
			return nil, err
		}
		// Set tag name from mapping
		if chunk.TagID != "" {
			entry.TagName = tagNameMap[chunk.TagID]
		}
		entries = append(entries, entry)
	}
	return types.NewPageResult(total, page, entries), nil
}

// UpsertFAQEntries imports or appends FAQ entries asynchronously.
// Returns task ID (UUID) for tracking import progress.
func (s *knowledgeService) UpsertFAQEntries(ctx context.Context,
	kbID string, payload *types.FAQBatchUpsertPayload,
) (string, error) {
	if payload == nil || len(payload.Entries) == 0 {
		return "", werrors.NewBadRequestError("FAQ 条目不能为空")
	}
	if payload.Mode == "" {
		payload.Mode = types.FAQBatchModeAppend
	}
	if payload.Mode != types.FAQBatchModeAppend && payload.Mode != types.FAQBatchModeReplace {
		return "", werrors.NewBadRequestError("模式仅支持 append 或 replace")
	}

	// 验证知识库是否存在且有效
	kb, err := s.validateFAQKnowledgeBase(ctx, kbID)
	if err != nil {
		return "", err
	}

	tenantID := ctx.Value(types.TenantIDContextKey).(uint64)

	// 使用传入的TaskID，如果没传则生成增强的TaskID
	taskID := payload.TaskID
	if taskID == "" {
		taskID = secutils.GenerateTaskID("faq_import", tenantID, kbID)
	}

	var knowledgeID string

	// 检查是否有正在进行的导入任务（通过Redis）
	runningTaskID, err := s.getRunningFAQImportTaskID(ctx, kbID)
	if err != nil {
		logger.Errorf(ctx, "Failed to check running import task: %v", err)
		// 检查失败不影响导入，继续执行
	} else if runningTaskID != "" {
		logger.Warnf(ctx, "Import task already running for KB %s: %s", kbID, runningTaskID)
		return "", werrors.NewBadRequestError(fmt.Sprintf("该知识库已有导入任务正在进行中（任务ID: %s），请等待完成后再试", runningTaskID))
	}

	// 确保 FAQ knowledge 存在
	faqKnowledge, err := s.ensureFAQKnowledge(ctx, tenantID, kb)
	if err != nil {
		return "", fmt.Errorf("failed to ensure FAQ knowledge: %w", err)
	}
	knowledgeID = faqKnowledge.ID

	// 记录任务入队时间
	enqueuedAt := time.Now().Unix()

	// 设置 KB 的运行中任务信息
	if err := s.setRunningFAQImportInfo(ctx, kbID, &runningFAQImportInfo{
		TaskID:     taskID,
		EnqueuedAt: enqueuedAt,
	}); err != nil {
		logger.Errorf(ctx, "Failed to set running FAQ import task info: %v", err)
		// 不影响任务执行，继续
	}

	// 初始化导入任务状态到Redis
	progress := &types.FAQImportProgress{
		TaskID:        taskID,
		KBID:          kbID,
		KnowledgeID:   knowledgeID,
		Status:        types.FAQImportStatusPending,
		Progress:      0,
		Total:         len(payload.Entries),
		Processed:     0,
		SuccessCount:  0,
		FailedCount:   0,
		FailedEntries: make([]types.FAQFailedEntry, 0),
		Message:       "任务已创建，等待处理",
		CreatedAt:     time.Now().Unix(),
		UpdatedAt:     time.Now().Unix(),
		DryRun:        payload.DryRun,
	}
	if err := s.saveFAQImportProgress(ctx, progress); err != nil {
		logger.Errorf(ctx, "Failed to initialize FAQ import task status: %v", err)
		return "", fmt.Errorf("failed to initialize task: %w", err)
	}

	logger.Infof(ctx, "FAQ import task initialized: %s, kb_id: %s, total entries: %d, dry_run: %v",
		taskID, kbID, len(payload.Entries), payload.DryRun)

	// Enqueue FAQ import task to Asynq
	logger.Info(ctx, "Enqueuing FAQ import task to Asynq")

	// 构建任务 payload
	taskPayload := types.FAQImportPayload{
		TenantID:    tenantID,
		TaskID:      taskID,
		KBID:        kbID,
		KnowledgeID: knowledgeID,
		Mode:        payload.Mode,
		DryRun:      payload.DryRun,
		EnqueuedAt:  enqueuedAt,
	}

	// 阈值：超过 200 条或序列化后超过 50KB 时使用对象存储
	const (
		entryCountThreshold  = 200
		payloadSizeThreshold = 50 * 1024 // 50KB
	)

	entryCount := len(payload.Entries)
	if entryCount > entryCountThreshold {
		// 数据量较大，上传到对象存储
		entriesData, err := json.Marshal(payload.Entries)
		if err != nil {
			logger.Errorf(ctx, "Failed to marshal FAQ entries: %v", err)
			return "", fmt.Errorf("failed to marshal entries: %w", err)
		}

		logger.Infof(ctx, "FAQ entries size: %d bytes, uploading to object storage", len(entriesData))

		// 上传到私有桶（主桶），任务处理完成后清理
		fileName := fmt.Sprintf("faq_import_entries_%s_%d.json", taskID, enqueuedAt)
		entriesURL, err := s.fileSvc.SaveBytes(ctx, entriesData, tenantID, fileName, false)
		if err != nil {
			logger.Errorf(ctx, "Failed to upload FAQ entries to object storage: %v", err)
			return "", fmt.Errorf("failed to upload entries: %w", err)
		}

		logger.Infof(ctx, "FAQ entries uploaded to: %s", entriesURL)
		taskPayload.EntriesURL = entriesURL
		taskPayload.EntryCount = entryCount
	} else {
		// 数据量较小，直接存储在 payload 中
		taskPayload.Entries = payload.Entries
	}

	payloadBytes, err := json.Marshal(taskPayload)
	if err != nil {
		logger.Errorf(ctx, "Failed to marshal FAQ import task payload: %v", err)
		return "", fmt.Errorf("failed to marshal task payload: %w", err)
	}

	// 再次检查 payload 大小
	if len(payloadBytes) > payloadSizeThreshold && taskPayload.EntriesURL == "" {
		// payload 太大但还没上传，现在上传
		entriesData, _ := json.Marshal(payload.Entries)
		fileName := fmt.Sprintf("faq_import_entries_%s_%d.json", taskID, enqueuedAt)
		entriesURL, err := s.fileSvc.SaveBytes(ctx, entriesData, tenantID, fileName, false)
		if err != nil {
			logger.Errorf(ctx, "Failed to upload FAQ entries to object storage: %v", err)
			return "", fmt.Errorf("failed to upload entries: %w", err)
		}

		logger.Infof(ctx, "FAQ entries uploaded to (size exceeded): %s", entriesURL)
		taskPayload.Entries = nil
		taskPayload.EntriesURL = entriesURL
		taskPayload.EntryCount = entryCount

		payloadBytes, _ = json.Marshal(taskPayload)
	}

	logger.Infof(ctx, "FAQ import task payload size: %d bytes", len(payloadBytes))

	maxRetry := 5
	if payload.DryRun {
		maxRetry = 3 // dry run 重试次数少一些
	}

	// 使用 taskID:enqueuedAt 作为 asynq 的唯一任务标识
	// 这样同一个用户 TaskID 的不同次提交不会冲突
	asynqTaskID := fmt.Sprintf("%s:%d", taskID, enqueuedAt)

	task := asynq.NewTask(
		types.TypeFAQImport,
		payloadBytes,
		asynq.TaskID(asynqTaskID),
		asynq.Queue("default"),
		asynq.MaxRetry(maxRetry),
	)
	info, err := s.task.Enqueue(task)
	if err != nil {
		logger.Errorf(ctx, "Failed to enqueue FAQ import task: %v", err)
		return "", fmt.Errorf("failed to enqueue task: %w", err)
	}
	logger.Infof(ctx, "Enqueued FAQ import task: id=%s queue=%s task_id=%s dry_run=%v", info.ID, info.Queue, taskID, payload.DryRun)

	return taskID, nil
}

// generateFailedEntriesCSV 生成失败条目的 CSV 文件并上传
func (s *knowledgeService) generateFailedEntriesCSV(ctx context.Context,
	tenantID uint64, taskID string, failedEntries []types.FAQFailedEntry,
) (string, error) {
	// 生成 CSV 内容
	var buf strings.Builder

	// 写入 BOM 以支持 Excel 正确识别 UTF-8
	buf.WriteString("\xEF\xBB\xBF")

	// 写入表头
	buf.WriteString("错误原因,分类(必填),问题(必填),相似问题(选填-多个用##分隔),反例问题(选填-多个用##分隔),机器人回答(必填-多个用##分隔),是否全部回复(选填-默认FALSE),是否停用(选填-默认FALSE)\n")

	// 写入数据行
	for _, entry := range failedEntries {
		// CSV 转义：如果内容包含逗号、引号或换行，需要用引号包裹并转义内部引号
		reason := csvEscape(entry.Reason)
		tagName := csvEscape(entry.TagName)
		standardQ := csvEscape(entry.StandardQuestion)
		similarQs := ""
		if len(entry.SimilarQuestions) > 0 {
			similarQs = csvEscape(strings.Join(entry.SimilarQuestions, "##"))
		}
		negativeQs := ""
		if len(entry.NegativeQuestions) > 0 {
			negativeQs = csvEscape(strings.Join(entry.NegativeQuestions, "##"))
		}
		answers := ""
		if len(entry.Answers) > 0 {
			answers = csvEscape(strings.Join(entry.Answers, "##"))
		}
		answerAll := "false"
		if entry.AnswerAll {
			answerAll = "true"
		}
		isDisabled := "false"
		if entry.IsDisabled {
			isDisabled = "true"
		}

		buf.WriteString(fmt.Sprintf("%s,%s,%s,%s,%s,%s,%s,%s\n",
			reason, tagName, standardQ, similarQs, negativeQs, answers, answerAll, isDisabled))
	}

	// 上传 CSV 文件到临时存储（会自动过期）
	fileName := fmt.Sprintf("faq_dryrun_failed_%s.csv", taskID)
	filePath, err := s.fileSvc.SaveBytes(ctx, []byte(buf.String()), tenantID, fileName, true)
	if err != nil {
		return "", fmt.Errorf("failed to save CSV file: %w", err)
	}

	// 获取下载 URL
	fileURL, err := s.fileSvc.GetFileURL(ctx, filePath)
	if err != nil {
		return "", fmt.Errorf("failed to get file URL: %w", err)
	}

	logger.Infof(ctx, "Generated failed entries CSV: %s, entries: %d", fileURL, len(failedEntries))
	return fileURL, nil
}

// csvEscape 转义 CSV 字段
func csvEscape(s string) string {
	if strings.ContainsAny(s, ",\"\n\r") {
		// 将内部引号替换为两个引号，并用引号包裹整个字段
		return "\"" + strings.ReplaceAll(s, "\"", "\"\"") + "\""
	}
	return s
}

// saveFAQImportResultToDatabase 保存FAQ导入结果统计到数据库
func (s *knowledgeService) saveFAQImportResultToDatabase(ctx context.Context,
	payload *types.FAQImportPayload, progress *types.FAQImportProgress, originalTotalEntries int) error {

	// 获取FAQ知识库实例
	tenantID := ctx.Value(types.TenantIDContextKey).(uint64)
	knowledge, err := s.repo.GetKnowledgeByID(ctx, tenantID, payload.KnowledgeID)
	if err != nil {
		return fmt.Errorf("failed to get FAQ knowledge: %w", err)
	}

	// 计算跳过的条目数（总数 - 成功 - 失败）
	skippedCount := originalTotalEntries - progress.SuccessCount - progress.FailedCount
	if skippedCount < 0 {
		skippedCount = 0
	}

	// 创建导入结果统计
	importResult := &types.FAQImportResult{
		TotalEntries:   originalTotalEntries,
		SuccessCount:   progress.SuccessCount,
		FailedCount:    progress.FailedCount,
		SkippedCount:   skippedCount,
		ImportMode:     payload.Mode,
		ImportedAt:     time.Now(),
		TaskID:         payload.TaskID,
		ProcessingTime: time.Now().Unix() - progress.CreatedAt, // 处理耗时（秒）
		DisplayStatus:  "open",                                 // 新导入的结果默认显示
	}

	// 如果有失败条目且提供了下载URL，设置失败URL
	if progress.FailedCount > 0 && progress.FailedEntriesURL != "" {
		importResult.FailedEntriesURL = progress.FailedEntriesURL
	}

	// 设置导入结果到Knowledge的metadata中
	if err := knowledge.SetLastFAQImportResult(importResult); err != nil {
		return fmt.Errorf("failed to set FAQ import result: %w", err)
	}

	// 更新数据库
	if err := s.repo.UpdateKnowledge(ctx, knowledge); err != nil {
		return fmt.Errorf("failed to update knowledge with import result: %w", err)
	}

	logger.Infof(ctx, "Saved FAQ import result to database: knowledge_id=%s, task_id=%s, total=%d, success=%d, failed=%d, skipped=%d",
		payload.KnowledgeID, payload.TaskID, originalTotalEntries, progress.SuccessCount, progress.FailedCount, skippedCount)

	return nil
}

// buildFAQFailedEntry 构建 FAQFailedEntry
func buildFAQFailedEntry(idx int, reason string, entry *types.FAQEntryPayload) types.FAQFailedEntry {
	answerAll := false
	if entry.AnswerStrategy != nil && *entry.AnswerStrategy == types.AnswerStrategyAll {
		answerAll = true
	}
	isDisabled := false
	if entry.IsEnabled != nil && !*entry.IsEnabled {
		isDisabled = true
	}
	return types.FAQFailedEntry{
		Index:             idx,
		Reason:            reason,
		TagName:           entry.TagName,
		StandardQuestion:  strings.TrimSpace(entry.StandardQuestion),
		SimilarQuestions:  entry.SimilarQuestions,
		NegativeQuestions: entry.NegativeQuestions,
		Answers:           entry.Answers,
		AnswerAll:         answerAll,
		IsDisabled:        isDisabled,
	}
}

// executeFAQDryRunValidation 执行 FAQ dry run 验证，返回通过验证的条目索引
func (s *knowledgeService) executeFAQDryRunValidation(ctx context.Context,
	payload *types.FAQImportPayload, progress *types.FAQImportProgress,
) []int {
	entries := payload.Entries

	// 用于记录已通过基本验证和重复检查的条目索引，后续进行安全检查
	validEntryIndices := make([]int, 0, len(entries))

	// 根据模式选择不同的验证逻辑
	if payload.Mode == types.FAQBatchModeAppend {
		validEntryIndices = s.validateEntriesForAppendModeWithProgress(ctx, payload.TenantID, payload.KBID, entries, progress)
	} else {
		validEntryIndices = s.validateEntriesForReplaceModeWithProgress(ctx, entries, progress)
	}

	return validEntryIndices
}

// validateEntriesForAppendModeWithProgress 验证 Append 模式下的条目（带进度更新）
// 注意：验证阶段不更新 Processed，只有实际导入时才更新
func (s *knowledgeService) validateEntriesForAppendModeWithProgress(ctx context.Context,
	tenantID uint64, kbID string, entries []types.FAQEntryPayload, progress *types.FAQImportProgress,
) []int {
	validIndices := make([]int, 0, len(entries))

	// 查询知识库中已有的所有FAQ chunks的metadata
	existingChunks, err := s.chunkRepo.ListAllFAQChunksWithMetadataByKnowledgeBaseID(ctx, tenantID, kbID)
	if err != nil {
		logger.Warnf(ctx, "Failed to list existing FAQ chunks for dry run: %v", err)
		// 无法获取已有数据时，仅做批次内验证
	}

	// 构建已存在的标准问和相似问集合
	existingQuestions := make(map[string]bool)
	for _, chunk := range existingChunks {
		meta, err := chunk.FAQMetadata()
		if err != nil || meta == nil {
			continue
		}
		if meta.StandardQuestion != "" {
			existingQuestions[meta.StandardQuestion] = true
		}
		for _, q := range meta.SimilarQuestions {
			if q != "" {
				existingQuestions[q] = true
			}
		}
	}

	// 构建当前批次的标准问和相似问集合（用于批次内去重）
	batchQuestions := make(map[string]int) // value 为首次出现的索引

	for i, entry := range entries {
		// 验证条目基本格式
		if err := validateFAQEntryPayloadBasic(&entry); err != nil {
			progress.FailedCount++
			progress.FailedEntries = append(progress.FailedEntries, buildFAQFailedEntry(i, err.Error(), &entry))
			continue
		}

		standardQ := strings.TrimSpace(entry.StandardQuestion)

		// 检查标准问是否与已有知识库重复
		if existingQuestions[standardQ] {
			progress.FailedCount++
			progress.FailedEntries = append(progress.FailedEntries, buildFAQFailedEntry(i, "标准问与知识库中已有问题重复", &entry))
			continue
		}

		// 检查标准问是否与同批次重复
		if firstIdx, exists := batchQuestions[standardQ]; exists {
			progress.FailedCount++
			progress.FailedEntries = append(progress.FailedEntries, buildFAQFailedEntry(i, fmt.Sprintf("标准问与批次内第 %d 条重复", firstIdx+1), &entry))
			continue
		}

		// 检查相似问是否有重复
		hasDuplicate := false
		for _, q := range entry.SimilarQuestions {
			q = strings.TrimSpace(q)
			if q == "" {
				continue
			}
			if existingQuestions[q] {
				progress.FailedCount++
				progress.FailedEntries = append(progress.FailedEntries, buildFAQFailedEntry(i, fmt.Sprintf("相似问 \"%s\" 与知识库中已有问题重复", q), &entry))
				hasDuplicate = true
				break
			}
			if firstIdx, exists := batchQuestions[q]; exists {
				progress.FailedCount++
				progress.FailedEntries = append(progress.FailedEntries, buildFAQFailedEntry(i, fmt.Sprintf("相似问 \"%s\" 与批次内第 %d 条重复", q, firstIdx+1), &entry))
				hasDuplicate = true
				break
			}
		}
		if hasDuplicate {
			continue
		}

		// 将当前条目的标准问和相似问加入批次集合
		batchQuestions[standardQ] = i
		for _, q := range entry.SimilarQuestions {
			q = strings.TrimSpace(q)
			if q != "" {
				batchQuestions[q] = i
			}
		}

		// 记录通过验证的条目索引
		validIndices = append(validIndices, i)

		// 定期更新进度消息（验证阶段不更新 Processed）
		if (i+1)%100 == 0 {
			progress.Message = fmt.Sprintf("正在验证条目 %d/%d...", i+1, len(entries))
			progress.UpdatedAt = time.Now().Unix()
			if err := s.saveFAQImportProgress(ctx, progress); err != nil {
				logger.Warnf(ctx, "Failed to update FAQ dry run progress: %v", err)
			}
		}
	}

	return validIndices
}

// validateEntriesForReplaceModeWithProgress 验证 Replace 模式下的条目（带进度更新）
// 注意：验证阶段不更新 Processed，只有实际导入时才更新
func (s *knowledgeService) validateEntriesForReplaceModeWithProgress(ctx context.Context,
	entries []types.FAQEntryPayload, progress *types.FAQImportProgress,
) []int {
	validIndices := make([]int, 0, len(entries))

	// Replace 模式下只检查批次内重复
	batchQuestions := make(map[string]int) // value 为首次出现的索引

	for i, entry := range entries {
		// 验证条目基本格式
		if err := validateFAQEntryPayloadBasic(&entry); err != nil {
			progress.FailedCount++
			progress.FailedEntries = append(progress.FailedEntries, buildFAQFailedEntry(i, err.Error(), &entry))
			continue
		}

		standardQ := strings.TrimSpace(entry.StandardQuestion)

		// 检查标准问是否与同批次重复
		if firstIdx, exists := batchQuestions[standardQ]; exists {
			progress.FailedCount++
			progress.FailedEntries = append(progress.FailedEntries, buildFAQFailedEntry(i, fmt.Sprintf("标准问与批次内第 %d 条重复", firstIdx+1), &entry))
			continue
		}

		// 检查相似问是否有重复
		hasDuplicate := false
		for _, q := range entry.SimilarQuestions {
			q = strings.TrimSpace(q)
			if q == "" {
				continue
			}
			if firstIdx, exists := batchQuestions[q]; exists {
				progress.FailedCount++
				progress.FailedEntries = append(progress.FailedEntries, buildFAQFailedEntry(i, fmt.Sprintf("相似问 \"%s\" 与批次内第 %d 条重复", q, firstIdx+1), &entry))
				hasDuplicate = true
				break
			}
		}
		if hasDuplicate {
			continue
		}

		// 将当前条目的标准问和相似问加入批次集合
		batchQuestions[standardQ] = i
		for _, q := range entry.SimilarQuestions {
			q = strings.TrimSpace(q)
			if q != "" {
				batchQuestions[q] = i
			}
		}

		// 记录通过验证的条目索引
		validIndices = append(validIndices, i)

		// 定期更新进度消息（验证阶段不更新 Processed）
		if (i+1)%100 == 0 {
			progress.Message = fmt.Sprintf("正在验证条目 %d/%d...", i+1, len(entries))
			progress.UpdatedAt = time.Now().Unix()
			if err := s.saveFAQImportProgress(ctx, progress); err != nil {
				logger.Warnf(ctx, "Failed to update FAQ dry run progress: %v", err)
			}
		}
	}

	return validIndices
}

// validateFAQEntryPayloadBasic 验证 FAQ 条目的基本格式
func validateFAQEntryPayloadBasic(entry *types.FAQEntryPayload) error {
	if entry == nil {
		return fmt.Errorf("条目不能为空")
	}
	standardQ := strings.TrimSpace(entry.StandardQuestion)
	if standardQ == "" {
		return fmt.Errorf("标准问不能为空")
	}
	if len(entry.Answers) == 0 {
		return fmt.Errorf("答案不能为空")
	}
	hasValidAnswer := false
	for _, a := range entry.Answers {
		if strings.TrimSpace(a) != "" {
			hasValidAnswer = true
			break
		}
	}
	if !hasValidAnswer {
		return fmt.Errorf("答案不能全为空")
	}
	return nil
}

// calculateAppendOperations 计算Append模式下需要处理的条目，跳过已存在且内容相同的条目
// 同时过滤掉标准问或相似问与同批次或已有知识库中重复的条目
func (s *knowledgeService) calculateAppendOperations(ctx context.Context,
	tenantID uint64, kbID string, entries []types.FAQEntryPayload,
) ([]types.FAQEntryPayload, int, error) {
	if len(entries) == 0 {
		return []types.FAQEntryPayload{}, 0, nil
	}

	// 1. 查询知识库中已有的所有FAQ chunks的metadata
	existingChunks, err := s.chunkRepo.ListAllFAQChunksWithMetadataByKnowledgeBaseID(ctx, tenantID, kbID)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to list existing FAQ chunks: %w", err)
	}

	// 2. 构建已存在的标准问和相似问集合
	existingQuestions := make(map[string]bool)
	for _, chunk := range existingChunks {
		meta, err := chunk.FAQMetadata()
		if err != nil || meta == nil {
			continue
		}
		// 添加标准问
		if meta.StandardQuestion != "" {
			existingQuestions[meta.StandardQuestion] = true
		}
		// 添加相似问
		for _, q := range meta.SimilarQuestions {
			if q != "" {
				existingQuestions[q] = true
			}
		}
	}

	// 3. 构建当前批次的标准问和相似问集合（用于批次内去重）
	batchQuestions := make(map[string]bool)
	entriesToProcess := make([]types.FAQEntryPayload, 0, len(entries))
	skippedCount := 0

	for _, entry := range entries {
		meta, err := sanitizeFAQEntryPayload(&entry)
		if err != nil {
			// 跳过无效条目
			skippedCount++
			logger.Warnf(ctx, "Skipping invalid FAQ entry: %v", err)
			continue
		}

		// 检查标准问是否重复（与已有或同批次）
		if existingQuestions[meta.StandardQuestion] || batchQuestions[meta.StandardQuestion] {
			skippedCount++
			logger.Infof(ctx, "Skipping FAQ entry with duplicate standard question: %s", meta.StandardQuestion)
			continue
		}

		// 检查相似问是否有重复（与已有或同批次）
		hasDuplicateSimilar := false
		for _, q := range meta.SimilarQuestions {
			if existingQuestions[q] || batchQuestions[q] {
				hasDuplicateSimilar = true
				logger.Infof(ctx, "Skipping FAQ entry with duplicate similar question: %s (standard: %s)", q, meta.StandardQuestion)
				break
			}
		}
		if hasDuplicateSimilar {
			skippedCount++
			continue
		}

		// 将当前条目的标准问和相似问加入批次集合
		batchQuestions[meta.StandardQuestion] = true
		for _, q := range meta.SimilarQuestions {
			batchQuestions[q] = true
		}

		entriesToProcess = append(entriesToProcess, entry)
	}

	return entriesToProcess, skippedCount, nil
}

// calculateReplaceOperations 计算Replace模式下需要删除、创建、更新的条目
// 同时过滤掉同批次内标准问或相似问重复的条目
func (s *knowledgeService) calculateReplaceOperations(ctx context.Context,
	tenantID uint64, knowledgeID string, newEntries []types.FAQEntryPayload,
) ([]types.FAQEntryPayload, []*types.Chunk, int, error) {
	// 获取 kbID 用于解析 tag
	var kbID string
	if len(newEntries) > 0 {
		// 从 knowledgeID 获取 kbID
		knowledge, err := s.repo.GetKnowledgeByID(ctx, tenantID, knowledgeID)
		if err != nil {
			return nil, nil, 0, fmt.Errorf("failed to get knowledge: %w", err)
		}
		if knowledge != nil {
			kbID = knowledge.KnowledgeBaseID
		}
	}

	// 计算所有新条目的 content hash，并同时构建 hash 到 entry 的映射
	type entryWithHash struct {
		entry types.FAQEntryPayload
		hash  string
		meta  *types.FAQChunkMetadata
	}
	entriesWithHash := make([]entryWithHash, 0, len(newEntries))
	newHashSet := make(map[string]bool)
	// 用于批次内标准问和相似问去重
	batchQuestions := make(map[string]bool)
	batchSkippedCount := 0

	for _, entry := range newEntries {
		meta, err := sanitizeFAQEntryPayload(&entry)
		if err != nil {
			batchSkippedCount++
			logger.Warnf(ctx, "Skipping invalid FAQ entry in replace mode: %v", err)
			continue
		}

		// 检查标准问是否在同批次中重复
		if batchQuestions[meta.StandardQuestion] {
			batchSkippedCount++
			logger.Infof(ctx, "Skipping FAQ entry with duplicate standard question in batch: %s", meta.StandardQuestion)
			continue
		}

		// 检查相似问是否在同批次中重复
		hasDuplicateSimilar := false
		for _, q := range meta.SimilarQuestions {
			if batchQuestions[q] {
				hasDuplicateSimilar = true
				logger.Infof(ctx, "Skipping FAQ entry with duplicate similar question in batch: %s (standard: %s)", q, meta.StandardQuestion)
				break
			}
		}
		if hasDuplicateSimilar {
			batchSkippedCount++
			continue
		}

		// 将当前条目的标准问和相似问加入批次集合
		batchQuestions[meta.StandardQuestion] = true
		for _, q := range meta.SimilarQuestions {
			batchQuestions[q] = true
		}

		hash := types.CalculateFAQContentHash(meta)
		if hash != "" {
			entriesWithHash = append(entriesWithHash, entryWithHash{entry: entry, hash: hash, meta: meta})
			newHashSet[hash] = true
		}
	}

	// 查询所有已存在的chunks
	allExistingChunks, err := s.chunkRepo.ListAllFAQChunksByKnowledgeID(ctx, tenantID, knowledgeID)
	if err != nil {
		return nil, nil, 0, fmt.Errorf("failed to list existing chunks: %w", err)
	}

	// 在内存中过滤出匹配新条目hash的chunks，并构建map
	existingHashMap := make(map[string]*types.Chunk)
	for _, chunk := range allExistingChunks {
		if chunk.ContentHash != "" && newHashSet[chunk.ContentHash] {
			existingHashMap[chunk.ContentHash] = chunk
		}
	}

	// 计算需要删除的chunks（数据库中有但新批次中没有的，或hash不匹配的）
	chunksToDelete := make([]*types.Chunk, 0)
	for _, chunk := range allExistingChunks {
		if chunk.ContentHash == "" {
			// 如果没有hash，需要删除（可能是旧数据）
			chunksToDelete = append(chunksToDelete, chunk)
		} else if !newHashSet[chunk.ContentHash] {
			// hash不在新条目中，需要删除
			chunksToDelete = append(chunksToDelete, chunk)
		}
	}

	// 计算需要创建的条目（利用已经计算好的hash，避免重复计算）
	entriesToProcess := make([]types.FAQEntryPayload, 0, len(entriesWithHash))
	skippedCount := batchSkippedCount

	for _, ewh := range entriesWithHash {
		existingChunk := existingHashMap[ewh.hash]
		if existingChunk != nil {
			// hash 匹配，检查 tag 是否变化
			newTagID, err := s.resolveTagID(ctx, kbID, &ewh.entry)
			if err != nil {
				logger.Warnf(ctx, "Failed to resolve tag for entry, treating as new: %v", err)
				entriesToProcess = append(entriesToProcess, ewh.entry)
				continue
			}

			if existingChunk.TagID != newTagID {
				// tag 变化了，需要删除旧的并创建新的
				logger.Infof(ctx, "FAQ entry tag changed from %s to %s, will update", existingChunk.TagID, newTagID)
				chunksToDelete = append(chunksToDelete, existingChunk)
				entriesToProcess = append(entriesToProcess, ewh.entry)
			} else {
				// hash 和 tag 都相同，跳过
				skippedCount++
			}
			continue
		}

		// hash不匹配或不存在，需要创建
		entriesToProcess = append(entriesToProcess, ewh.entry)
	}

	return entriesToProcess, chunksToDelete, skippedCount, nil
}

// executeFAQImport 执行实际的FAQ导入逻辑
func (s *knowledgeService) executeFAQImport(ctx context.Context, taskID string, kbID string,
	payload *types.FAQBatchUpsertPayload, tenantID uint64, processedCount int,
	progress *types.FAQImportProgress,
) (err error) {
	// 保存知识库和embedding模型信息，用于清理索引
	var kb *types.KnowledgeBase
	var embeddingModel embedding.Embedder
	totalEntries := len(payload.Entries) + processedCount

	// Recovery机制：如果发生任何错误或panic，回滚所有已创建的chunks和索引数据
	defer func() {
		// 捕获panic
		if r := recover(); r != nil {
			buf := make([]byte, 8192)
			n := runtime.Stack(buf, false)
			stack := string(buf[:n])
			logger.Errorf(ctx, "FAQ import task %s panicked: %v\n%s", taskID, r, stack)
			err = fmt.Errorf("panic during FAQ import: %v", r)
		}
	}()

	kb, err = s.validateFAQKnowledgeBase(ctx, kbID)
	if err != nil {
		return err
	}

	kb.EnsureDefaults()

	// 获取embedding模型，用于后续清理索引
	embeddingModel, err = s.modelService.GetEmbeddingModel(ctx, kb.EmbeddingModelID)
	if err != nil {
		return fmt.Errorf("failed to get embedding model: %w", err)
	}
	faqKnowledge, err := s.ensureFAQKnowledge(ctx, tenantID, kb)
	if err != nil {
		return err
	}

	// 获取索引模式
	indexMode := types.FAQIndexModeQuestionOnly
	if kb.FAQConfig != nil && kb.FAQConfig.IndexMode != "" {
		indexMode = kb.FAQConfig.IndexMode
	}

	// 增量更新逻辑：计算需要处理的条目
	var entriesToProcess []types.FAQEntryPayload
	var chunksToDelete []*types.Chunk
	var skippedCount int

	if payload.Mode == types.FAQBatchModeReplace {
		// Replace模式：计算需要删除、创建、更新的条目
		entriesToProcess, chunksToDelete, skippedCount, err = s.calculateReplaceOperations(
			ctx,
			tenantID,
			faqKnowledge.ID,
			payload.Entries,
		)
		if err != nil {
			return fmt.Errorf("failed to calculate replace operations: %w", err)
		}

		// 删除需要删除的chunks（包括需要更新的旧chunks）
		if len(chunksToDelete) > 0 {
			chunkIDsToDelete := make([]string, 0, len(chunksToDelete))
			for _, chunk := range chunksToDelete {
				chunkIDsToDelete = append(chunkIDsToDelete, chunk.ID)
			}
			if err := s.chunkRepo.DeleteChunks(ctx, tenantID, chunkIDsToDelete); err != nil {
				return fmt.Errorf("failed to delete chunks: %w", err)
			}
			// 删除索引
			if err := s.deleteFAQChunkVectors(ctx, kb, faqKnowledge, chunksToDelete); err != nil {
				return fmt.Errorf("failed to delete chunk vectors: %w", err)
			}
			logger.Infof(ctx, "FAQ import task %s: deleted %d chunks (including updates)", taskID, len(chunksToDelete))
		}
	} else {
		// Append模式：查询已存在的条目，跳过未变化的
		entriesToProcess, skippedCount, err = s.calculateAppendOperations(ctx, tenantID, kb.ID, payload.Entries)
		if err != nil {
			return fmt.Errorf("failed to calculate append operations: %w", err)
		}
	}

	logger.Infof(
		ctx,
		"FAQ import task %s: total entries: %d, to process: %d, skipped: %d",
		taskID,
		len(payload.Entries),
		len(entriesToProcess),
		skippedCount,
	)

	// 如果没有需要处理的条目，直接返回
	if len(entriesToProcess) == 0 {
		logger.Infof(ctx, "FAQ import task %s: no entries to process, all skipped", taskID)
		return nil
	}

	// 分批处理需要创建的条目
	remainingEntries := len(entriesToProcess)
	totalStartTime := time.Now()
	actualProcessed := skippedCount + processedCount

	logger.Infof(
		ctx,
		"FAQ import task %s: starting batch processing, remaining entries: %d, total entries: %d, batch size: %d",
		taskID,
		remainingEntries,
		totalEntries,
		faqImportBatchSize,
	)

	for i := 0; i < remainingEntries; i += faqImportBatchSize {
		batchStartTime := time.Now()
		end := i + faqImportBatchSize
		if end > remainingEntries {
			end = remainingEntries
		}

		batch := entriesToProcess[i:end]
		logger.Infof(ctx, "FAQ import task %s: processing batch %d-%d (%d entries)", taskID, i+1, end, len(batch))

		// 构建chunks
		buildStartTime := time.Now()
		chunks := make([]*types.Chunk, 0, len(batch))
		chunkIds := make([]string, 0, len(batch))
		for idx, entry := range batch {
			meta, err := sanitizeFAQEntryPayload(&entry)
			if err != nil {
				logger.ErrorWithFields(ctx, err, map[string]interface{}{
					"entry":   entry,
					"task_id": taskID,
				})
				return fmt.Errorf("failed to sanitize entry at index %d: %w", i+idx, err)
			}

			// 解析 TagID
			tagID, err := s.resolveTagID(ctx, kbID, &entry)
			if err != nil {
				logger.ErrorWithFields(ctx, err, map[string]interface{}{
					"entry":   entry,
					"task_id": taskID,
				})
				return fmt.Errorf("failed to resolve tag for entry at index %d: %w", i+idx, err)
			}

			isEnabled := true
			if entry.IsEnabled != nil {
				isEnabled = *entry.IsEnabled
			}
			// ChunkIndex计算：startChunkIndex + (i+idx) + initialProcessed
			chunk := &types.Chunk{
				ID:              uuid.New().String(),
				TenantID:        tenantID,
				KnowledgeID:     faqKnowledge.ID,
				KnowledgeBaseID: kb.ID,
				Content:         buildFAQChunkContent(meta, indexMode),
				// ChunkIndex:      0,
				IsEnabled: isEnabled,
				ChunkType: types.ChunkTypeFAQ,
				TagID:     tagID,                        // 使用解析后的 TagID
				Status:    int(types.ChunkStatusStored), // store but not indexed
			}
			// 如果指定了 ID（用于数据迁移），设置 SeqID
			if entry.ID != nil && *entry.ID > 0 {
				chunk.SeqID = *entry.ID
			}
			if err := chunk.SetFAQMetadata(meta); err != nil {
				return fmt.Errorf("failed to set FAQ metadata: %w", err)
			}
			chunks = append(chunks, chunk)
			chunkIds = append(chunkIds, chunk.ID)
		}
		buildDuration := time.Since(buildStartTime)
		logger.Debugf(ctx, "FAQ import task %s: batch %d-%d built %d chunks in %v, chunk IDs: %v",
			taskID, i+1, end, len(chunks), buildDuration, chunkIds)
		// 创建chunks
		createStartTime := time.Now()
		if err := s.chunkService.CreateChunks(ctx, chunks); err != nil {
			return fmt.Errorf("failed to create chunks: %w", err)
		}
		createDuration := time.Since(createStartTime)
		logger.Infof(
			ctx,
			"FAQ import task %s: batch %d-%d created %d chunks in %v",
			taskID,
			i+1,
			end,
			len(chunks),
			createDuration,
		)

		// 索引chunks
		indexStartTime := time.Now()
		// 注意：如果索引失败，defer中的recovery机制会自动回滚已创建的chunks和索引数据
		if err := s.indexFAQChunks(ctx, kb, faqKnowledge, chunks, embeddingModel, true, false); err != nil {
			return fmt.Errorf("failed to index chunks: %w", err)
		}
		indexDuration := time.Since(indexStartTime)
		logger.Infof(
			ctx,
			"FAQ import task %s: batch %d-%d indexed %d chunks in %v",
			taskID,
			i+1,
			end,
			len(chunks),
			indexDuration,
		)

		// 更新chunks的Status为已索引
		chunksToUpdate := make([]*types.Chunk, 0, len(chunks))
		for _, chunk := range chunks {
			chunk.Status = int(types.ChunkStatusIndexed) // indexed
			chunksToUpdate = append(chunksToUpdate, chunk)
		}
		if err := s.chunkService.UpdateChunks(ctx, chunksToUpdate); err != nil {
			return fmt.Errorf("failed to update chunks status: %w", err)
		}

		// 收集成功条目信息
		for idx, chunk := range chunks {
			entryIdx := i + idx + processedCount // 原始条目索引
			meta, _ := chunk.FAQMetadata()
			standardQ := ""
			if meta != nil {
				standardQ = meta.StandardQuestion
			}
			// 获取 tag info
			var tagID int64
			tagName := ""
			if chunk.TagID != "" {
				if tag, err := s.tagRepo.GetByID(ctx, tenantID, chunk.TagID); err == nil && tag != nil {
					tagID = tag.SeqID
					tagName = tag.Name
				}
			}
			progress.SuccessEntries = append(progress.SuccessEntries, types.FAQSuccessEntry{
				Index:            entryIdx,
				SeqID:            chunk.SeqID,
				TagID:            tagID,
				TagName:          tagName,
				StandardQuestion: standardQ,
			})
		}

		actualProcessed += len(batch)
		// 更新任务进度
		progress := int(float64(actualProcessed) / float64(totalEntries) * 100)
		if err := s.updateFAQImportProgressStatus(ctx, taskID, types.FAQImportStatusProcessing, progress, totalEntries, actualProcessed, fmt.Sprintf("正在处理第 %d/%d 条", actualProcessed, totalEntries), ""); err != nil {
			logger.Errorf(ctx, "Failed to update task progress: %v", err)
		}

		batchDuration := time.Since(batchStartTime)
		logger.Infof(
			ctx,
			"FAQ import task %s: batch %d-%d completed in %v (build: %v, create: %v, index: %v), total progress: %d/%d (%d%%)",
			taskID,
			i+1,
			end,
			batchDuration,
			buildDuration,
			createDuration,
			indexDuration,
			actualProcessed,
			totalEntries,
			progress,
		)
	}

	totalDuration := time.Since(totalStartTime)
	logger.Infof(
		ctx,
		"FAQ import task %s: all batches completed, processed: %d entries (skipped: %d) in %v, avg: %v per entry",
		taskID,
		actualProcessed,
		skippedCount,
		totalDuration,
		totalDuration/time.Duration(actualProcessed),
	)

	return nil
}

// CreateFAQEntry creates a single FAQ entry synchronously.
func (s *knowledgeService) CreateFAQEntry(ctx context.Context,
	kbID string, payload *types.FAQEntryPayload,
) (*types.FAQEntry, error) {
	if payload == nil {
		return nil, werrors.NewBadRequestError("请求体不能为空")
	}

	kb, err := s.validateFAQKnowledgeBase(ctx, kbID)
	if err != nil {
		return nil, err
	}
	kb.EnsureDefaults()

	tenantID := ctx.Value(types.TenantIDContextKey).(uint64)

	// 验证并清理输入
	meta, err := sanitizeFAQEntryPayload(payload)
	if err != nil {
		return nil, err
	}

	// 解析 TagID
	tagID, err := s.resolveTagID(ctx, kbID, payload)
	if err != nil {
		return nil, err
	}

	// 检查标准问和相似问是否与其他条目重复
	if err := s.checkFAQQuestionDuplicate(ctx, tenantID, kb.ID, "", meta); err != nil {
		return nil, err
	}

	// 确保FAQ Knowledge存在
	faqKnowledge, err := s.ensureFAQKnowledge(ctx, tenantID, kb)
	if err != nil {
		return nil, fmt.Errorf("failed to ensure FAQ knowledge: %w", err)
	}

	// 获取索引模式
	indexMode := types.FAQIndexModeQuestionOnly
	if kb.FAQConfig != nil && kb.FAQConfig.IndexMode != "" {
		indexMode = kb.FAQConfig.IndexMode
	}

	// 获取embedding模型
	embeddingModel, err := s.modelService.GetEmbeddingModel(ctx, kb.EmbeddingModelID)
	if err != nil {
		return nil, fmt.Errorf("failed to get embedding model: %w", err)
	}

	// 创建chunk
	isEnabled := true
	if payload.IsEnabled != nil {
		isEnabled = *payload.IsEnabled
	}
	// 默认可推荐
	flags := types.ChunkFlagRecommended
	if payload.IsRecommended != nil && !*payload.IsRecommended {
		flags = 0
	}

	chunk := &types.Chunk{
		ID:              uuid.New().String(),
		TenantID:        tenantID,
		KnowledgeID:     faqKnowledge.ID,
		KnowledgeBaseID: kb.ID,
		Content:         buildFAQChunkContent(meta, indexMode),
		IsEnabled:       isEnabled,
		Flags:           flags,
		ChunkType:       types.ChunkTypeFAQ,
		TagID:           tagID, // 使用解析后的 TagID
		Status:          int(types.ChunkStatusStored),
	}
	// 如果指定了 ID（用于数据迁移），设置 SeqID
	if payload.ID != nil && *payload.ID > 0 {
		chunk.SeqID = *payload.ID
	}

	if err := chunk.SetFAQMetadata(meta); err != nil {
		return nil, fmt.Errorf("failed to set FAQ metadata: %w", err)
	}

	// 保存chunk
	if err := s.chunkService.CreateChunks(ctx, []*types.Chunk{chunk}); err != nil {
		return nil, fmt.Errorf("failed to create chunk: %w", err)
	}

	// 索引chunk
	if err := s.indexFAQChunks(ctx, kb, faqKnowledge, []*types.Chunk{chunk}, embeddingModel, true, false); err != nil {
		// 如果索引失败，删除已创建的chunk
		_ = s.chunkService.DeleteChunk(ctx, chunk.ID)
		return nil, fmt.Errorf("failed to index chunk: %w", err)
	}

	// 更新chunk状态为已索引
	chunk.Status = int(types.ChunkStatusIndexed)
	if err := s.chunkService.UpdateChunk(ctx, chunk); err != nil {
		return nil, fmt.Errorf("failed to update chunk status: %w", err)
	}

	// Build tag seq_id map for conversion
	tagSeqIDMap := make(map[string]int64)
	if chunk.TagID != "" {
		tag, tagErr := s.tagRepo.GetByID(ctx, tenantID, chunk.TagID)
		if tagErr == nil && tag != nil {
			tagSeqIDMap[tag.ID] = tag.SeqID
		}
	}

	// 转换为FAQEntry返回
	entry, err := s.chunkToFAQEntry(chunk, kb, tagSeqIDMap)
	if err != nil {
		return nil, err
	}

	// 查询TagName
	if chunk.TagID != "" {
		tag, tagErr := s.tagRepo.GetByID(ctx, tenantID, chunk.TagID)
		if tagErr == nil && tag != nil {
			entry.TagName = tag.Name
		}
	}

	return entry, nil
}

// GetFAQEntry retrieves a single FAQ entry by seq_id.
func (s *knowledgeService) GetFAQEntry(ctx context.Context,
	kbID string, entrySeqID int64,
) (*types.FAQEntry, error) {
	if entrySeqID <= 0 {
		return nil, werrors.NewBadRequestError("条目ID不能为空")
	}

	kb, err := s.validateFAQKnowledgeBase(ctx, kbID)
	if err != nil {
		return nil, err
	}
	kb.EnsureDefaults()

	tenantID := ctx.Value(types.TenantIDContextKey).(uint64)

	// 获取chunk by seq_id
	chunk, err := s.chunkRepo.GetChunkBySeqID(ctx, tenantID, entrySeqID)
	if err != nil {
		return nil, werrors.NewNotFoundError("FAQ条目不存在")
	}

	// 验证chunk属于当前知识库
	if chunk.KnowledgeBaseID != kb.ID || chunk.TenantID != tenantID {
		return nil, werrors.NewNotFoundError("FAQ条目不存在")
	}

	// 验证是FAQ类型
	if chunk.ChunkType != types.ChunkTypeFAQ {
		return nil, werrors.NewNotFoundError("FAQ条目不存在")
	}

	// Build tag seq_id map for conversion
	tagSeqIDMap := make(map[string]int64)
	if chunk.TagID != "" {
		tag, tagErr := s.tagRepo.GetByID(ctx, tenantID, chunk.TagID)
		if tagErr == nil && tag != nil {
			tagSeqIDMap[tag.ID] = tag.SeqID
		}
	}

	// 转换为FAQEntry返回
	entry, err := s.chunkToFAQEntry(chunk, kb, tagSeqIDMap)
	if err != nil {
		return nil, err
	}

	// 查询TagName
	if chunk.TagID != "" {
		tag, tagErr := s.tagRepo.GetByID(ctx, tenantID, chunk.TagID)
		if tagErr == nil && tag != nil {
			entry.TagName = tag.Name
		}
	}

	return entry, nil
}

// UpdateFAQEntry updates a single FAQ entry.
func (s *knowledgeService) UpdateFAQEntry(ctx context.Context,
	kbID string, entrySeqID int64, payload *types.FAQEntryPayload,
) (*types.FAQEntry, error) {
	if payload == nil {
		return nil, werrors.NewBadRequestError("请求体不能为空")
	}
	kb, err := s.validateFAQKnowledgeBase(ctx, kbID)
	if err != nil {
		return nil, err
	}
	kb.EnsureDefaults()
	tenantID := ctx.Value(types.TenantIDContextKey).(uint64)

	chunk, err := s.chunkRepo.GetChunkBySeqID(ctx, tenantID, entrySeqID)
	if err != nil {
		return nil, werrors.NewNotFoundError("FAQ条目不存在")
	}
	if chunk.KnowledgeBaseID != kb.ID {
		return nil, werrors.NewForbiddenError("无权操作该 FAQ 条目")
	}
	if chunk.ChunkType != types.ChunkTypeFAQ {
		return nil, werrors.NewBadRequestError("仅支持更新 FAQ 条目")
	}
	meta, err := sanitizeFAQEntryPayload(payload)
	if err != nil {
		return nil, err
	}

	// 检查标准问和相似问是否与其他条目重复
	if err := s.checkFAQQuestionDuplicate(ctx, tenantID, kb.ID, chunk.ID, meta); err != nil {
		return nil, err
	}

	// 获取旧的相似问列表，用于增量更新
	var oldSimilarQuestions []string
	var oldStandardQuestion string
	var oldAnswers []string
	questionIndexMode := types.FAQQuestionIndexModeCombined
	if kb.FAQConfig != nil && kb.FAQConfig.QuestionIndexMode != "" {
		questionIndexMode = kb.FAQConfig.QuestionIndexMode
	}
	if existing, err := chunk.FAQMetadata(); err == nil && existing != nil {
		meta.Version = existing.Version + 1
		// 保存旧的内容用于增量比较
		if questionIndexMode == types.FAQQuestionIndexModeSeparate {
			oldSimilarQuestions = existing.SimilarQuestions
			oldStandardQuestion = existing.StandardQuestion
			oldAnswers = existing.Answers
		}
	}
	if err := chunk.SetFAQMetadata(meta); err != nil {
		return nil, err
	}
	// 获取索引模式
	indexMode := types.FAQIndexModeQuestionOnly
	if kb.FAQConfig != nil && kb.FAQConfig.IndexMode != "" {
		indexMode = kb.FAQConfig.IndexMode
	}
	chunk.Content = buildFAQChunkContent(meta, indexMode)

	// Convert tag seq_id to UUID
	if payload.TagID > 0 {
		tag, tagErr := s.tagRepo.GetBySeqID(ctx, tenantID, payload.TagID)
		if tagErr != nil {
			return nil, werrors.NewNotFoundError("标签不存在")
		}
		chunk.TagID = tag.ID
	} else {
		chunk.TagID = ""
	}

	if payload.IsEnabled != nil {
		chunk.IsEnabled = *payload.IsEnabled
	}
	// 处理推荐状态
	if payload.IsRecommended != nil {
		if *payload.IsRecommended {
			chunk.Flags = chunk.Flags.SetFlag(types.ChunkFlagRecommended)
		} else {
			chunk.Flags = chunk.Flags.ClearFlag(types.ChunkFlagRecommended)
		}
	}
	chunk.UpdatedAt = time.Now()
	if err := s.chunkService.UpdateChunk(ctx, chunk); err != nil {
		return nil, err
	}

	// Note: We don't need to call BatchUpdateChunkEnabledStatus here because
	// indexFAQChunks will delete old vectors and re-insert with the latest chunk data
	// (including the updated is_enabled status). Calling both would cause version conflicts.

	faqKnowledge, err := s.repo.GetKnowledgeByID(ctx, tenantID, chunk.KnowledgeID)
	if err != nil {
		return nil, err
	}

	embeddingModel, err := s.modelService.GetEmbeddingModel(ctx, kb.EmbeddingModelID)
	if err != nil {
		return nil, err
	}

	// 增量索引优化：只对变化的内容进行索引操作
	if questionIndexMode == types.FAQQuestionIndexModeSeparate && len(oldSimilarQuestions) > 0 {
		// 分别索引模式下的增量更新
		if err := s.incrementalIndexFAQEntry(ctx, kb, faqKnowledge, chunk, embeddingModel,
			oldStandardQuestion, oldSimilarQuestions, oldAnswers, meta); err != nil {
			return nil, err
		}
	} else {
		// Combined 模式或首次创建，使用全量索引
		// 增量删除：只删除被移除的相似问索引
		oldSimilarQuestionCount := len(oldSimilarQuestions)
		newSimilarQuestionCount := len(meta.SimilarQuestions)
		if questionIndexMode == types.FAQQuestionIndexModeSeparate && oldSimilarQuestionCount > newSimilarQuestionCount {
			tenantInfo := ctx.Value(types.TenantInfoContextKey).(*types.Tenant)
			retrieveEngine, engineErr := retriever.NewCompositeRetrieveEngine(s.retrieveEngine, tenantInfo.GetEffectiveEngines())
			if engineErr == nil {
				sourceIDsToDelete := make([]string, 0, oldSimilarQuestionCount-newSimilarQuestionCount)
				for i := newSimilarQuestionCount; i < oldSimilarQuestionCount; i++ {
					sourceIDsToDelete = append(sourceIDsToDelete, fmt.Sprintf("%s-%d", chunk.ID, i))
				}
				if len(sourceIDsToDelete) > 0 {
					logger.Debugf(ctx, "UpdateFAQEntry: incremental delete %d obsolete source IDs", len(sourceIDsToDelete))
					if delErr := retrieveEngine.DeleteBySourceIDList(ctx, sourceIDsToDelete, embeddingModel.GetDimensions(), types.KnowledgeTypeFAQ); delErr != nil {
						logger.Warnf(ctx, "UpdateFAQEntry: failed to delete obsolete source IDs: %v", delErr)
					}
				}
			}
		}

		// 使用 needDelete=false，因为 EFPutDocument 会自动覆盖相同 SourceID 的文档
		if err := s.indexFAQChunks(ctx, kb, faqKnowledge, []*types.Chunk{chunk}, embeddingModel, false, false); err != nil {
			return nil, err
		}
	}

	// Build tag seq_id map for conversion
	tagSeqIDMap := make(map[string]int64)
	if chunk.TagID != "" {
		tag, tagErr := s.tagRepo.GetByID(ctx, tenantID, chunk.TagID)
		if tagErr == nil && tag != nil {
			tagSeqIDMap[tag.ID] = tag.SeqID
		}
	}

	// 转换为FAQEntry返回
	entry, err := s.chunkToFAQEntry(chunk, kb, tagSeqIDMap)
	if err != nil {
		return nil, err
	}

	// 查询TagName
	if chunk.TagID != "" {
		tag, tagErr := s.tagRepo.GetByID(ctx, tenantID, chunk.TagID)
		if tagErr == nil && tag != nil {
			entry.TagName = tag.Name
		}
	}

	return entry, nil
}

// AddSimilarQuestions adds similar questions to a FAQ entry.
// This will append the new questions to the existing similar questions list.
func (s *knowledgeService) AddSimilarQuestions(ctx context.Context,
	kbID string, entrySeqID int64, questions []string,
) (*types.FAQEntry, error) {
	if len(questions) == 0 {
		return nil, werrors.NewBadRequestError("相似问列表不能为空")
	}

	kb, err := s.validateFAQKnowledgeBase(ctx, kbID)
	if err != nil {
		return nil, err
	}
	kb.EnsureDefaults()
	tenantID := ctx.Value(types.TenantIDContextKey).(uint64)

	// Get existing FAQ entry
	chunk, err := s.chunkRepo.GetChunkBySeqID(ctx, tenantID, entrySeqID)
	if err != nil {
		return nil, werrors.NewNotFoundError("FAQ条目不存在")
	}
	if chunk.KnowledgeBaseID != kb.ID {
		return nil, werrors.NewForbiddenError("无权操作该 FAQ 条目")
	}
	if chunk.ChunkType != types.ChunkTypeFAQ {
		return nil, werrors.NewBadRequestError("仅支持更新 FAQ 条目")
	}

	// Get existing metadata
	meta, err := chunk.FAQMetadata()
	if err != nil || meta == nil {
		return nil, werrors.NewBadRequestError("获取 FAQ 元数据失败")
	}

	// Deduplicate and sanitize new questions
	existingSet := make(map[string]struct{})
	for _, q := range meta.SimilarQuestions {
		existingSet[q] = struct{}{}
	}
	// Also add standard question to prevent duplicates
	existingSet[meta.StandardQuestion] = struct{}{}

	newQuestions := make([]string, 0, len(questions))
	for _, q := range questions {
		q = strings.TrimSpace(q)
		if q == "" {
			continue
		}
		if _, exists := existingSet[q]; exists {
			continue
		}
		existingSet[q] = struct{}{}
		newQuestions = append(newQuestions, q)
	}

	if len(newQuestions) == 0 {
		// No new questions to add, return current entry
		tagSeqIDMap := make(map[string]int64)
		if chunk.TagID != "" {
			tag, tagErr := s.tagRepo.GetByID(ctx, tenantID, chunk.TagID)
			if tagErr == nil && tag != nil {
				tagSeqIDMap[tag.ID] = tag.SeqID
			}
		}
		return s.chunkToFAQEntry(chunk, kb, tagSeqIDMap)
	}

	// Check for duplicates with other entries
	tempMeta := &types.FAQChunkMetadata{
		StandardQuestion: meta.StandardQuestion,
		SimilarQuestions: append(meta.SimilarQuestions, newQuestions...),
	}
	if err := s.checkFAQQuestionDuplicate(ctx, tenantID, kb.ID, chunk.ID, tempMeta); err != nil {
		return nil, err
	}

	// Update metadata
	oldSimilarQuestions := meta.SimilarQuestions
	meta.SimilarQuestions = append(meta.SimilarQuestions, newQuestions...)
	meta.Version++

	if err := chunk.SetFAQMetadata(meta); err != nil {
		return nil, err
	}

	// Update chunk content
	indexMode := types.FAQIndexModeQuestionOnly
	if kb.FAQConfig != nil && kb.FAQConfig.IndexMode != "" {
		indexMode = kb.FAQConfig.IndexMode
	}
	chunk.Content = buildFAQChunkContent(meta, indexMode)
	chunk.UpdatedAt = time.Now()

	if err := s.chunkService.UpdateChunk(ctx, chunk); err != nil {
		return nil, err
	}

	// Index new similar questions
	faqKnowledge, err := s.repo.GetKnowledgeByID(ctx, tenantID, chunk.KnowledgeID)
	if err != nil {
		return nil, err
	}

	embeddingModel, err := s.modelService.GetEmbeddingModel(ctx, kb.EmbeddingModelID)
	if err != nil {
		return nil, err
	}

	questionIndexMode := types.FAQQuestionIndexModeCombined
	if kb.FAQConfig != nil && kb.FAQConfig.QuestionIndexMode != "" {
		questionIndexMode = kb.FAQConfig.QuestionIndexMode
	}

	if questionIndexMode == types.FAQQuestionIndexModeSeparate {
		// Only index the new similar questions
		if err := s.incrementalIndexFAQEntry(ctx, kb, faqKnowledge, chunk, embeddingModel,
			meta.StandardQuestion, oldSimilarQuestions, meta.Answers, meta); err != nil {
			return nil, err
		}
	} else {
		// Combined mode, re-index the whole entry
		if err := s.indexFAQChunks(ctx, kb, faqKnowledge, []*types.Chunk{chunk}, embeddingModel, false, false); err != nil {
			return nil, err
		}
	}

	// Build response
	tagSeqIDMap := make(map[string]int64)
	if chunk.TagID != "" {
		tag, tagErr := s.tagRepo.GetByID(ctx, tenantID, chunk.TagID)
		if tagErr == nil && tag != nil {
			tagSeqIDMap[tag.ID] = tag.SeqID
		}
	}

	entry, err := s.chunkToFAQEntry(chunk, kb, tagSeqIDMap)
	if err != nil {
		return nil, err
	}

	if chunk.TagID != "" {
		tag, tagErr := s.tagRepo.GetByID(ctx, tenantID, chunk.TagID)
		if tagErr == nil && tag != nil {
			entry.TagName = tag.Name
		}
	}

	return entry, nil
}

// UpdateFAQEntryStatus updates enable status for a FAQ entry.
func (s *knowledgeService) UpdateFAQEntryStatus(ctx context.Context,
	kbID string, entryID string, isEnabled bool,
) error {
	kb, err := s.validateFAQKnowledgeBase(ctx, kbID)
	if err != nil {
		return err
	}
	tenantID := ctx.Value(types.TenantIDContextKey).(uint64)
	chunk, err := s.chunkRepo.GetChunkByID(ctx, tenantID, entryID)
	if err != nil {
		return err
	}
	if chunk.KnowledgeBaseID != kb.ID || chunk.ChunkType != types.ChunkTypeFAQ {
		return werrors.NewBadRequestError("仅支持更新 FAQ 条目")
	}
	if chunk.IsEnabled == isEnabled {
		return nil
	}
	chunk.IsEnabled = isEnabled
	chunk.UpdatedAt = time.Now()
	if err := s.chunkService.UpdateChunk(ctx, chunk); err != nil {
		return err
	}

	// Sync update to retriever engines
	chunkStatusMap := map[string]bool{chunk.ID: isEnabled}
	tenantInfo := ctx.Value(types.TenantInfoContextKey).(*types.Tenant)
	retrieveEngine, err := retriever.NewCompositeRetrieveEngine(s.retrieveEngine, tenantInfo.GetEffectiveEngines())
	if err != nil {
		return err
	}
	if err := retrieveEngine.BatchUpdateChunkEnabledStatus(ctx, chunkStatusMap); err != nil {
		return err
	}

	return nil
}

// UpdateFAQEntryFieldsBatch updates multiple fields for FAQ entries in batch.
// This is the unified API for batch updating FAQ entry fields.
// Supports two modes:
// 1. By entry seq_id: use ByID field
// 2. By Tag seq_id: use ByTag field to apply the same update to all entries under a tag
func (s *knowledgeService) UpdateFAQEntryFieldsBatch(ctx context.Context,
	kbID string, req *types.FAQEntryFieldsBatchUpdate,
) error {
	if req == nil || (len(req.ByID) == 0 && len(req.ByTag) == 0) {
		return nil
	}
	kb, err := s.validateFAQKnowledgeBase(ctx, kbID)
	if err != nil {
		return err
	}
	tenantID := ctx.Value(types.TenantIDContextKey).(uint64)

	enabledUpdates := make(map[string]bool)
	tagUpdates := make(map[string]string)

	// Convert exclude seq_ids to UUIDs
	excludeUUIDs := make([]string, 0, len(req.ExcludeIDs))
	if len(req.ExcludeIDs) > 0 {
		excludeChunks, err := s.chunkRepo.ListChunksBySeqID(ctx, tenantID, req.ExcludeIDs)
		if err == nil {
			for _, c := range excludeChunks {
				excludeUUIDs = append(excludeUUIDs, c.ID)
			}
		}
	}

	// Handle ByTag updates first (by tag seq_id)
	if len(req.ByTag) > 0 {
		for tagSeqID, update := range req.ByTag {
			// Convert tag seq_id to UUID
			tag, err := s.tagRepo.GetBySeqID(ctx, tenantID, tagSeqID)
			if err != nil {
				return werrors.NewNotFoundError(fmt.Sprintf("标签 %d 不存在", tagSeqID))
			}

			var setFlags, clearFlags types.ChunkFlags

			// Handle IsRecommended
			if update.IsRecommended != nil {
				if *update.IsRecommended {
					setFlags = types.ChunkFlagRecommended
				} else {
					clearFlags = types.ChunkFlagRecommended
				}
			}

			// Convert new tag seq_id to UUID if provided
			var newTagUUID *string
			if update.TagID != nil {
				if *update.TagID > 0 {
					newTag, err := s.tagRepo.GetBySeqID(ctx, tenantID, *update.TagID)
					if err != nil {
						return werrors.NewNotFoundError(fmt.Sprintf("标签 %d 不存在", *update.TagID))
					}
					newTagUUID = &newTag.ID
				} else {
					emptyStr := ""
					newTagUUID = &emptyStr
				}
			}

			// Update all chunks with this tag
			affectedIDs, err := s.chunkRepo.UpdateChunkFieldsByTagID(
				ctx, tenantID, kb.ID, tag.ID,
				update.IsEnabled, setFlags, clearFlags, newTagUUID, excludeUUIDs,
			)
			if err != nil {
				return err
			}

			// Collect affected IDs for retriever sync
			if len(affectedIDs) > 0 {
				if update.IsEnabled != nil {
					for _, id := range affectedIDs {
						enabledUpdates[id] = *update.IsEnabled
					}
				}
				if newTagUUID != nil {
					for _, id := range affectedIDs {
						tagUpdates[id] = *newTagUUID
					}
				}
			}
		}
	}

	// Handle ByID updates (by entry seq_id)
	if len(req.ByID) > 0 {
		entrySeqIDs := make([]int64, 0, len(req.ByID))
		for entrySeqID := range req.ByID {
			entrySeqIDs = append(entrySeqIDs, entrySeqID)
		}
		chunks, err := s.chunkRepo.ListChunksBySeqID(ctx, tenantID, entrySeqIDs)
		if err != nil {
			return err
		}

		// Build chunk seq_id to chunk map
		chunkBySeqID := make(map[int64]*types.Chunk)
		for _, chunk := range chunks {
			chunkBySeqID[chunk.SeqID] = chunk
		}

		setFlags := make(map[string]types.ChunkFlags)
		clearFlags := make(map[string]types.ChunkFlags)
		chunksToUpdate := make([]*types.Chunk, 0)

		for entrySeqID, update := range req.ByID {
			chunk, exists := chunkBySeqID[entrySeqID]
			if !exists {
				continue
			}
			if chunk.KnowledgeBaseID != kb.ID || chunk.ChunkType != types.ChunkTypeFAQ {
				continue
			}

			needUpdate := false

			// Handle IsEnabled
			if update.IsEnabled != nil && chunk.IsEnabled != *update.IsEnabled {
				chunk.IsEnabled = *update.IsEnabled
				enabledUpdates[chunk.ID] = *update.IsEnabled
				needUpdate = true
			}

			// Handle IsRecommended (via Flags)
			if update.IsRecommended != nil {
				currentRecommended := chunk.Flags.HasFlag(types.ChunkFlagRecommended)
				if currentRecommended != *update.IsRecommended {
					if *update.IsRecommended {
						setFlags[chunk.ID] = types.ChunkFlagRecommended
					} else {
						clearFlags[chunk.ID] = types.ChunkFlagRecommended
					}
				}
			}

			// Handle TagID (convert seq_id to UUID)
			if update.TagID != nil {
				var newTagID string
				if *update.TagID > 0 {
					newTag, err := s.tagRepo.GetBySeqID(ctx, tenantID, *update.TagID)
					if err != nil {
						return werrors.NewNotFoundError(fmt.Sprintf("标签 %d 不存在", *update.TagID))
					}
					newTagID = newTag.ID
				}
				if chunk.TagID != newTagID {
					chunk.TagID = newTagID
					tagUpdates[chunk.ID] = newTagID
					needUpdate = true
				}
			}

			if needUpdate {
				chunk.UpdatedAt = time.Now()
				chunksToUpdate = append(chunksToUpdate, chunk)
			}
		}

		// Batch update chunks (for IsEnabled and TagID)
		if len(chunksToUpdate) > 0 {
			if err := s.chunkRepo.UpdateChunks(ctx, chunksToUpdate); err != nil {
				return err
			}
		}

		// Batch update flags (for IsRecommended)
		if len(setFlags) > 0 || len(clearFlags) > 0 {
			if err := s.chunkRepo.UpdateChunkFlagsBatch(ctx, tenantID, kb.ID, setFlags, clearFlags); err != nil {
				return err
			}
		}
	}

	// Sync to retriever engines
	if len(enabledUpdates) > 0 || len(tagUpdates) > 0 {
		tenantInfo := ctx.Value(types.TenantInfoContextKey).(*types.Tenant)
		retrieveEngine, err := retriever.NewCompositeRetrieveEngine(
			s.retrieveEngine,
			tenantInfo.GetEffectiveEngines(),
		)
		if err != nil {
			return err
		}
		if len(enabledUpdates) > 0 {
			if err := retrieveEngine.BatchUpdateChunkEnabledStatus(ctx, enabledUpdates); err != nil {
				return err
			}
		}
		if len(tagUpdates) > 0 {
			if err := retrieveEngine.BatchUpdateChunkTagID(ctx, tagUpdates); err != nil {
				return err
			}
		}
	}

	return nil
}

// UpdateKnowledgeTag updates the tag assigned to a knowledge document.
func (s *knowledgeService) UpdateKnowledgeTag(ctx context.Context, knowledgeID string, tagID *string) error {
	tenantID := ctx.Value(types.TenantIDContextKey).(uint64)
	knowledge, err := s.repo.GetKnowledgeByID(ctx, tenantID, knowledgeID)
	if err != nil {
		return err
	}

	var resolvedTagID string
	if tagID != nil && *tagID != "" {
		tag, err := s.tagRepo.GetByID(ctx, tenantID, *tagID)
		if err != nil {
			return err
		}
		if tag.KnowledgeBaseID != knowledge.KnowledgeBaseID {
			return werrors.NewBadRequestError("标签不属于当前知识库")
		}
		resolvedTagID = tag.ID
	}

	knowledge.TagID = resolvedTagID
	return s.repo.UpdateKnowledge(ctx, knowledge)
}

// UpdateKnowledgeTagBatch updates tags for document knowledge items in batch.
func (s *knowledgeService) UpdateKnowledgeTagBatch(ctx context.Context, updates map[string]*string) error {
	if len(updates) == 0 {
		return nil
	}
	tenantID := ctx.Value(types.TenantIDContextKey).(uint64)

	// Get all knowledge items in batch
	knowledgeIDs := make([]string, 0, len(updates))
	for knowledgeID := range updates {
		knowledgeIDs = append(knowledgeIDs, knowledgeID)
	}
	knowledgeList, err := s.repo.GetKnowledgeBatch(ctx, tenantID, knowledgeIDs)
	if err != nil {
		return err
	}

	// Build tag ID map for validation
	tagIDSet := make(map[string]bool)
	for _, tagID := range updates {
		if tagID != nil && *tagID != "" {
			tagIDSet[*tagID] = true
		}
	}

	// Validate all tags in batch
	tagMap := make(map[string]*types.KnowledgeTag)
	if len(tagIDSet) > 0 {
		tagIDs := make([]string, 0, len(tagIDSet))
		for tagID := range tagIDSet {
			tagIDs = append(tagIDs, tagID)
		}
		for _, tagID := range tagIDs {
			tag, err := s.tagRepo.GetByID(ctx, tenantID, tagID)
			if err != nil {
				return err
			}
			tagMap[tagID] = tag
		}
	}

	// Update knowledge items
	knowledgeToUpdate := make([]*types.Knowledge, 0)
	for _, knowledge := range knowledgeList {
		tagID, exists := updates[knowledge.ID]
		if !exists {
			continue
		}

		var resolvedTagID string
		if tagID != nil && *tagID != "" {
			tag, ok := tagMap[*tagID]
			if !ok {
				return werrors.NewBadRequestError(fmt.Sprintf("标签 %s 不存在", *tagID))
			}
			if tag.KnowledgeBaseID != knowledge.KnowledgeBaseID {
				return werrors.NewBadRequestError(fmt.Sprintf("标签 %s 不属于知识库 %s", *tagID, knowledge.KnowledgeBaseID))
			}
			resolvedTagID = tag.ID
		}

		knowledge.TagID = resolvedTagID
		knowledgeToUpdate = append(knowledgeToUpdate, knowledge)
	}

	if len(knowledgeToUpdate) > 0 {
		return s.repo.UpdateKnowledgeBatch(ctx, knowledgeToUpdate)
	}

	return nil
}

// UpdateFAQEntryTag updates the tag assigned to an FAQ entry.
func (s *knowledgeService) UpdateFAQEntryTag(ctx context.Context, kbID string, entryID string, tagID *string) error {
	kb, err := s.validateFAQKnowledgeBase(ctx, kbID)
	if err != nil {
		return err
	}
	tenantID := ctx.Value(types.TenantIDContextKey).(uint64)
	chunk, err := s.chunkRepo.GetChunkByID(ctx, tenantID, entryID)
	if err != nil {
		return err
	}
	if chunk.KnowledgeBaseID != kb.ID || chunk.ChunkType != types.ChunkTypeFAQ {
		return werrors.NewBadRequestError("仅支持更新 FAQ 条目标签")
	}

	var resolvedTagID string
	if tagID != nil && *tagID != "" {
		tag, err := s.tagRepo.GetByID(ctx, tenantID, *tagID)
		if err != nil {
			return err
		}
		if tag.KnowledgeBaseID != kb.ID {
			return werrors.NewBadRequestError("标签不属于当前知识库")
		}
		resolvedTagID = tag.ID
	}

	// Check if tag actually changed
	if chunk.TagID == resolvedTagID {
		return nil
	}

	chunk.TagID = resolvedTagID
	chunk.UpdatedAt = time.Now()
	if err := s.chunkRepo.UpdateChunk(ctx, chunk); err != nil {
		return err
	}

	// Sync tag update to retriever engines
	tenantInfo := ctx.Value(types.TenantInfoContextKey).(*types.Tenant)
	retrieveEngine, err := retriever.NewCompositeRetrieveEngine(
		s.retrieveEngine,
		tenantInfo.GetEffectiveEngines(),
	)
	if err != nil {
		return err
	}
	return retrieveEngine.BatchUpdateChunkTagID(ctx, map[string]string{chunk.ID: resolvedTagID})
}

// UpdateFAQEntryTagBatch updates tags for FAQ entries in batch.
// Key: entry seq_id, Value: tag seq_id (nil to remove tag)
func (s *knowledgeService) UpdateFAQEntryTagBatch(ctx context.Context, kbID string, updates map[int64]*int64) error {
	if len(updates) == 0 {
		return nil
	}
	kb, err := s.validateFAQKnowledgeBase(ctx, kbID)
	if err != nil {
		return err
	}
	tenantID := ctx.Value(types.TenantIDContextKey).(uint64)

	// Get all chunks in batch by seq_id
	entrySeqIDs := make([]int64, 0, len(updates))
	for entrySeqID := range updates {
		entrySeqIDs = append(entrySeqIDs, entrySeqID)
	}
	chunks, err := s.chunkRepo.ListChunksBySeqID(ctx, tenantID, entrySeqIDs)
	if err != nil {
		return err
	}

	// Build chunk seq_id to chunk map
	chunkBySeqID := make(map[int64]*types.Chunk)
	for _, chunk := range chunks {
		chunkBySeqID[chunk.SeqID] = chunk
	}

	// Build tag seq_id set for validation
	tagSeqIDSet := make(map[int64]bool)
	for _, tagSeqID := range updates {
		if tagSeqID != nil && *tagSeqID > 0 {
			tagSeqIDSet[*tagSeqID] = true
		}
	}

	// Validate all tags in batch by seq_id
	tagMap := make(map[int64]*types.KnowledgeTag)
	if len(tagSeqIDSet) > 0 {
		tagSeqIDs := make([]int64, 0, len(tagSeqIDSet))
		for tagSeqID := range tagSeqIDSet {
			tagSeqIDs = append(tagSeqIDs, tagSeqID)
		}
		tags, err := s.tagRepo.GetBySeqIDs(ctx, tenantID, tagSeqIDs)
		if err != nil {
			return err
		}
		for _, tag := range tags {
			if tag.KnowledgeBaseID != kb.ID {
				return werrors.NewBadRequestError(fmt.Sprintf("标签 %d 不属于当前知识库", tag.SeqID))
			}
			tagMap[tag.SeqID] = tag
		}
	}

	// Update chunks
	chunksToUpdate := make([]*types.Chunk, 0)
	for entrySeqID, tagSeqID := range updates {
		chunk, exists := chunkBySeqID[entrySeqID]
		if !exists {
			continue
		}
		if chunk.KnowledgeBaseID != kb.ID || chunk.ChunkType != types.ChunkTypeFAQ {
			continue
		}

		var resolvedTagID string
		if tagSeqID != nil && *tagSeqID > 0 {
			tag, ok := tagMap[*tagSeqID]
			if !ok {
				return werrors.NewBadRequestError(fmt.Sprintf("标签 %d 不存在", *tagSeqID))
			}
			resolvedTagID = tag.ID
		}

		chunk.TagID = resolvedTagID
		chunk.UpdatedAt = time.Now()
		chunksToUpdate = append(chunksToUpdate, chunk)
	}

	if len(chunksToUpdate) > 0 {
		if err := s.chunkRepo.UpdateChunks(ctx, chunksToUpdate); err != nil {
			return err
		}

		// Sync tag updates to retriever engines
		tagUpdates := make(map[string]string)
		for _, chunk := range chunksToUpdate {
			tagUpdates[chunk.ID] = chunk.TagID
		}
		tenantInfo := ctx.Value(types.TenantInfoContextKey).(*types.Tenant)
		retrieveEngine, err := retriever.NewCompositeRetrieveEngine(
			s.retrieveEngine,
			tenantInfo.GetEffectiveEngines(),
		)
		if err != nil {
			return err
		}
		if err := retrieveEngine.BatchUpdateChunkTagID(ctx, tagUpdates); err != nil {
			return err
		}
	}

	return nil
}

// SearchFAQEntries searches FAQ entries using hybrid search.
func (s *knowledgeService) SearchFAQEntries(ctx context.Context,
	kbID string, req *types.FAQSearchRequest,
) ([]*types.FAQEntry, error) {
	// Validate FAQ knowledge base
	kb, err := s.validateFAQKnowledgeBase(ctx, kbID)
	if err != nil {
		return nil, err
	}

	// Set default values
	if req.VectorThreshold <= 0 {
		req.VectorThreshold = 0.7
	}
	if req.MatchCount <= 0 {
		req.MatchCount = 10
	}
	if req.MatchCount > 50 {
		req.MatchCount = 50
	}

	tenantID := ctx.Value(types.TenantIDContextKey).(uint64)

	// Convert tag seq_ids to UUIDs
	var firstPriorityTagUUIDs, secondPriorityTagUUIDs []string
	firstPrioritySeqIDSet := make(map[int64]struct{})
	secondPrioritySeqIDSet := make(map[int64]struct{})

	if len(req.FirstPriorityTagIDs) > 0 {
		tags, err := s.tagRepo.GetBySeqIDs(ctx, tenantID, req.FirstPriorityTagIDs)
		if err == nil {
			firstPriorityTagUUIDs = make([]string, 0, len(tags))
			for _, tag := range tags {
				firstPriorityTagUUIDs = append(firstPriorityTagUUIDs, tag.ID)
				firstPrioritySeqIDSet[tag.SeqID] = struct{}{}
			}
		}
	}
	if len(req.SecondPriorityTagIDs) > 0 {
		tags, err := s.tagRepo.GetBySeqIDs(ctx, tenantID, req.SecondPriorityTagIDs)
		if err == nil {
			secondPriorityTagUUIDs = make([]string, 0, len(tags))
			for _, tag := range tags {
				secondPriorityTagUUIDs = append(secondPriorityTagUUIDs, tag.ID)
				secondPrioritySeqIDSet[tag.SeqID] = struct{}{}
			}
		}
	}

	// Build priority tag sets for sorting (using UUID)
	hasFirstPriority := len(firstPriorityTagUUIDs) > 0
	hasSecondPriority := len(secondPriorityTagUUIDs) > 0
	hasPriorityFilter := hasFirstPriority || hasSecondPriority

	firstPrioritySet := make(map[string]struct{}, len(firstPriorityTagUUIDs))
	for _, tagID := range firstPriorityTagUUIDs {
		firstPrioritySet[tagID] = struct{}{}
	}
	secondPrioritySet := make(map[string]struct{}, len(secondPriorityTagUUIDs))
	for _, tagID := range secondPriorityTagUUIDs {
		secondPrioritySet[tagID] = struct{}{}
	}

	// Perform separate searches for each priority level to ensure FirstPriority results
	// are not crowded out by higher-scoring SecondPriority results in TopK truncation
	var searchResults []*types.SearchResult

	if hasPriorityFilter {
		// Use goroutines to search both priority levels concurrently
		var (
			firstResults  []*types.SearchResult
			secondResults []*types.SearchResult
			firstErr      error
			secondErr     error
			wg            sync.WaitGroup
		)

		if hasFirstPriority {
			wg.Add(1)
			go func() {
				defer wg.Done()
				firstParams := types.SearchParams{
					QueryText:            secutils.SanitizeForLog(req.QueryText),
					VectorThreshold:      req.VectorThreshold,
					MatchCount:           req.MatchCount,
					DisableKeywordsMatch: true,
					TagIDs:               firstPriorityTagUUIDs,
					OnlyRecommended:      req.OnlyRecommended,
				}
				firstResults, firstErr = s.kbService.HybridSearch(ctx, kbID, firstParams)
			}()
		}

		if hasSecondPriority {
			wg.Add(1)
			go func() {
				defer wg.Done()
				secondParams := types.SearchParams{
					QueryText:            secutils.SanitizeForLog(req.QueryText),
					VectorThreshold:      req.VectorThreshold,
					MatchCount:           req.MatchCount,
					DisableKeywordsMatch: true,
					TagIDs:               secondPriorityTagUUIDs,
					OnlyRecommended:      req.OnlyRecommended,
				}
				secondResults, secondErr = s.kbService.HybridSearch(ctx, kbID, secondParams)
			}()
		}

		wg.Wait()

		// Check errors
		if firstErr != nil {
			return nil, firstErr
		}
		if secondErr != nil {
			return nil, secondErr
		}

		// Merge results: FirstPriority first, then SecondPriority (deduplicated)
		seenChunkIDs := make(map[string]struct{})
		for _, result := range firstResults {
			if _, exists := seenChunkIDs[result.ID]; !exists {
				seenChunkIDs[result.ID] = struct{}{}
				searchResults = append(searchResults, result)
			}
		}
		for _, result := range secondResults {
			if _, exists := seenChunkIDs[result.ID]; !exists {
				seenChunkIDs[result.ID] = struct{}{}
				searchResults = append(searchResults, result)
			}
		}
	} else {
		// No priority filter, search all
		searchParams := types.SearchParams{
			QueryText:            secutils.SanitizeForLog(req.QueryText),
			VectorThreshold:      req.VectorThreshold,
			MatchCount:           req.MatchCount,
			DisableKeywordsMatch: true,
		}
		var err error
		searchResults, err = s.kbService.HybridSearch(ctx, kbID, searchParams)
		if err != nil {
			return nil, err
		}
	}

	if len(searchResults) == 0 {
		return []*types.FAQEntry{}, nil
	}

	// Extract chunk IDs and build score/match type/matched content maps
	chunkIDs := make([]string, 0, len(searchResults))
	chunkScores := make(map[string]float64)
	chunkMatchTypes := make(map[string]types.MatchType)
	chunkMatchedContents := make(map[string]string)
	for _, result := range searchResults {
		// SearchResult.ID is the chunk ID
		chunkID := result.ID
		chunkIDs = append(chunkIDs, chunkID)
		chunkScores[chunkID] = result.Score
		chunkMatchTypes[chunkID] = result.MatchType
		chunkMatchedContents[chunkID] = result.MatchedContent
	}

	// Batch fetch chunks
	chunks, err := s.chunkRepo.ListChunksByID(ctx, tenantID, chunkIDs)
	if err != nil {
		return nil, err
	}

	// Build tag UUID to seq_id map for conversion
	tagSeqIDMap := make(map[string]int64)
	tagIDs := make([]string, 0)
	tagIDSet := make(map[string]struct{})
	for _, chunk := range chunks {
		if chunk.TagID != "" {
			if _, exists := tagIDSet[chunk.TagID]; !exists {
				tagIDSet[chunk.TagID] = struct{}{}
				tagIDs = append(tagIDs, chunk.TagID)
			}
		}
	}
	if len(tagIDs) > 0 {
		tags, err := s.tagRepo.GetByIDs(ctx, tenantID, tagIDs)
		if err == nil {
			for _, tag := range tags {
				tagSeqIDMap[tag.ID] = tag.SeqID
			}
		}
	}

	// Filter FAQ chunks and convert to FAQEntry
	kb.EnsureDefaults()
	entries := make([]*types.FAQEntry, 0, len(chunks))
	for _, chunk := range chunks {
		// Only process FAQ type chunks
		if chunk.ChunkType != types.ChunkTypeFAQ {
			continue
		}
		if !chunk.IsEnabled {
			continue
		}

		entry, err := s.chunkToFAQEntry(chunk, kb, tagSeqIDMap)
		if err != nil {
			logger.Warnf(ctx, "Failed to convert chunk to FAQ entry: %v", err)
			continue
		}

		// Preserve score and match type from search results
		// Note: Negative question filtering is now handled in HybridSearch
		if score, ok := chunkScores[chunk.ID]; ok {
			entry.Score = score
		}
		if matchType, ok := chunkMatchTypes[chunk.ID]; ok {
			entry.MatchType = matchType
		}

		// Set MatchedQuestion from search result's matched content
		if matchedContent, ok := chunkMatchedContents[chunk.ID]; ok && matchedContent != "" {
			entry.MatchedQuestion = matchedContent
		}

		entries = append(entries, entry)
	}

	// Sort entries with two-level priority tag support
	if hasPriorityFilter {
		// getPriorityLevel returns: 0 = first priority, 1 = second priority, 2 = no priority
		// Use chunk.TagID (UUID) for comparison
		getPriorityLevel := func(chunk *types.Chunk) int {
			if _, ok := firstPrioritySet[chunk.TagID]; ok {
				return 0
			}
			if _, ok := secondPrioritySet[chunk.TagID]; ok {
				return 1
			}
			return 2
		}

		// Build chunk map for priority lookup
		chunkMap := make(map[int64]*types.Chunk)
		for _, chunk := range chunks {
			chunkMap[chunk.SeqID] = chunk
		}

		slices.SortFunc(entries, func(a, b *types.FAQEntry) int {
			aChunk := chunkMap[a.ID]
			bChunk := chunkMap[b.ID]
			var aPriority, bPriority int
			if aChunk != nil {
				aPriority = getPriorityLevel(aChunk)
			} else {
				aPriority = 2
			}
			if bChunk != nil {
				bPriority = getPriorityLevel(bChunk)
			} else {
				bPriority = 2
			}

			// Compare by priority level first
			if aPriority != bPriority {
				return aPriority - bPriority // Lower level = higher priority
			}

			// Same priority level, sort by score descending
			if b.Score > a.Score {
				return 1
			} else if b.Score < a.Score {
				return -1
			}
			return 0
		})
	} else {
		// No priority tags, sort by score only
		slices.SortFunc(entries, func(a, b *types.FAQEntry) int {
			if b.Score > a.Score {
				return 1
			} else if b.Score < a.Score {
				return -1
			}
			return 0
		})
	}

	// Limit results to requested match count
	if len(entries) > req.MatchCount {
		entries = entries[:req.MatchCount]
	}

	// 批量查询TagName并补充到结果中
	if len(entries) > 0 {
		// 收集所有需要查询的TagID (seq_id)
		tagSeqIDs := make([]int64, 0)
		tagSeqIDSet := make(map[int64]struct{})
		for _, entry := range entries {
			if entry.TagID != 0 {
				if _, exists := tagSeqIDSet[entry.TagID]; !exists {
					tagSeqIDs = append(tagSeqIDs, entry.TagID)
					tagSeqIDSet[entry.TagID] = struct{}{}
				}
			}
		}

		// 批量查询标签
		if len(tagSeqIDs) > 0 {
			tags, err := s.tagRepo.GetBySeqIDs(ctx, tenantID, tagSeqIDs)
			if err != nil {
				logger.Warnf(ctx, "Failed to batch query tags: %v", err)
			} else {
				// 构建TagSeqID到TagName的映射
				tagNameMap := make(map[int64]string)
				for _, tag := range tags {
					tagNameMap[tag.SeqID] = tag.Name
				}

				// 补充TagName
				for _, entry := range entries {
					if entry.TagID != 0 {
						if tagName, exists := tagNameMap[entry.TagID]; exists {
							entry.TagName = tagName
						}
					}
				}
			}
		}
	}

	return entries, nil
}

// DeleteFAQEntries deletes FAQ entries in batch by seq_id.
func (s *knowledgeService) DeleteFAQEntries(ctx context.Context,
	kbID string, entrySeqIDs []int64,
) error {
	if len(entrySeqIDs) == 0 {
		return werrors.NewBadRequestError("请选择需要删除的 FAQ 条目")
	}
	kb, err := s.validateFAQKnowledgeBase(ctx, kbID)
	if err != nil {
		return err
	}

	tenantID := ctx.Value(types.TenantIDContextKey).(uint64)
	var faqKnowledge *types.Knowledge
	chunksToRemove := make([]*types.Chunk, 0, len(entrySeqIDs))
	for _, seqID := range entrySeqIDs {
		if seqID <= 0 {
			continue
		}
		chunk, err := s.chunkRepo.GetChunkBySeqID(ctx, tenantID, seqID)
		if err != nil {
			return werrors.NewNotFoundError("FAQ条目不存在")
		}
		if chunk.KnowledgeBaseID != kb.ID || chunk.ChunkType != types.ChunkTypeFAQ {
			return werrors.NewBadRequestError("包含无效的 FAQ 条目")
		}
		if err := s.chunkService.DeleteChunk(ctx, chunk.ID); err != nil {
			return err
		}
		if faqKnowledge == nil {
			faqKnowledge, err = s.repo.GetKnowledgeByID(ctx, tenantID, chunk.KnowledgeID)
			if err != nil {
				return err
			}
		}
		chunksToRemove = append(chunksToRemove, chunk)
	}
	if len(chunksToRemove) > 0 && faqKnowledge != nil {
		if err := s.deleteFAQChunkVectors(ctx, kb, faqKnowledge, chunksToRemove); err != nil {
			return err
		}
	}
	return nil
}

// ExportFAQEntries exports all FAQ entries for a knowledge base as CSV data.
// The CSV format matches the import example format with 8 columns:
// 分类(必填), 问题(必填), 相似问题(选填-多个用##分隔), 反例问题(选填-多个用##分隔),
// 机器人回答(必填-多个用##分隔), 是否全部回复(选填-默认FALSE), 是否停用(选填-默认FALSE),
// 是否禁止被推荐(选填-默认False 可被推荐)
func (s *knowledgeService) ExportFAQEntries(ctx context.Context, kbID string) ([]byte, error) {
	kb, err := s.validateFAQKnowledgeBase(ctx, kbID)
	if err != nil {
		return nil, err
	}

	tenantID := ctx.Value(types.TenantIDContextKey).(uint64)
	faqKnowledge, err := s.findFAQKnowledge(ctx, tenantID, kb.ID)
	if err != nil {
		return nil, err
	}
	if faqKnowledge == nil {
		// Return empty CSV with headers only
		return s.buildFAQCSV(nil, nil), nil
	}

	// Get all FAQ chunks
	chunks, err := s.chunkRepo.ListAllFAQChunksForExport(ctx, tenantID, faqKnowledge.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to list FAQ chunks: %w", err)
	}

	// Build tag map for tag_id -> tag_name conversion
	tagMap, err := s.buildTagMap(ctx, tenantID, kbID)
	if err != nil {
		return nil, fmt.Errorf("failed to build tag map: %w", err)
	}

	return s.buildFAQCSV(chunks, tagMap), nil
}

// buildTagMap builds a map from tag_id to tag_name for the given knowledge base.
func (s *knowledgeService) buildTagMap(ctx context.Context, tenantID uint64, kbID string) (map[string]string, error) {
	// Get all tags for this knowledge base (no pagination limit)
	page := &types.Pagination{Page: 1, PageSize: 10000}
	tags, _, err := s.tagRepo.ListByKB(ctx, tenantID, kbID, page, "")
	if err != nil {
		return nil, err
	}

	tagMap := make(map[string]string, len(tags))
	for _, tag := range tags {
		if tag != nil {
			tagMap[tag.ID] = tag.Name
		}
	}
	return tagMap, nil
}

// buildFAQCSV builds CSV content from FAQ chunks.
func (s *knowledgeService) buildFAQCSV(chunks []*types.Chunk, tagMap map[string]string) []byte {
	var buf strings.Builder

	// Write CSV header (matching import example format)
	headers := []string{
		"分类(必填)",
		"问题(必填)",
		"相似问题(选填-多个用##分隔)",
		"反例问题(选填-多个用##分隔)",
		"机器人回答(必填-多个用##分隔)",
		"是否全部回复(选填-默认FALSE)",
		"是否停用(选填-默认FALSE)",
		"是否禁止被推荐(选填-默认False 可被推荐)",
	}
	buf.WriteString(strings.Join(headers, ","))
	buf.WriteString("\n")

	// Write data rows
	for _, chunk := range chunks {
		meta, err := chunk.FAQMetadata()
		if err != nil || meta == nil {
			continue
		}

		// Get tag name
		tagName := ""
		if chunk.TagID != "" && tagMap != nil {
			if name, ok := tagMap[chunk.TagID]; ok {
				tagName = name
			}
		}

		// Build row
		row := []string{
			escapeCSVField(tagName),
			escapeCSVField(meta.StandardQuestion),
			escapeCSVField(strings.Join(meta.SimilarQuestions, "##")),
			escapeCSVField(strings.Join(meta.NegativeQuestions, "##")),
			escapeCSVField(strings.Join(meta.Answers, "##")),
			boolToCSV(meta.AnswerStrategy == types.AnswerStrategyAll),
			boolToCSV(!chunk.IsEnabled),                                 // 是否停用：取反
			boolToCSV(!chunk.Flags.HasFlag(types.ChunkFlagRecommended)), // 是否禁止被推荐：取反
		}
		buf.WriteString(strings.Join(row, ","))
		buf.WriteString("\n")
	}

	return []byte(buf.String())
}

// escapeCSVField escapes a field for CSV format.
func escapeCSVField(field string) string {
	// If field contains comma, newline, or quote, wrap in quotes and escape internal quotes
	if strings.ContainsAny(field, ",\"\n\r") {
		return "\"" + strings.ReplaceAll(field, "\"", "\"\"") + "\""
	}
	return field
}

// boolToCSV converts a boolean to CSV TRUE/FALSE string.
func boolToCSV(b bool) string {
	if b {
		return "TRUE"
	}
	return "FALSE"
}

func (s *knowledgeService) validateFAQKnowledgeBase(ctx context.Context, kbID string) (*types.KnowledgeBase, error) {
	if kbID == "" {
		return nil, werrors.NewBadRequestError("知识库 ID 不能为空")
	}
	kb, err := s.kbService.GetKnowledgeBaseByID(ctx, kbID)
	if err != nil {
		return nil, err
	}
	kb.EnsureDefaults()
	if kb.Type != types.KnowledgeBaseTypeFAQ {
		return nil, werrors.NewBadRequestError("仅 FAQ 知识库支持该操作")
	}
	return kb, nil
}

func (s *knowledgeService) findFAQKnowledge(
	ctx context.Context,
	tenantID uint64,
	kbID string,
) (*types.Knowledge, error) {
	knowledges, err := s.repo.ListKnowledgeByKnowledgeBaseID(ctx, tenantID, kbID)
	if err != nil {
		return nil, err
	}
	for _, knowledge := range knowledges {
		if knowledge.Type == types.KnowledgeTypeFAQ {
			return knowledge, nil
		}
	}
	return nil, nil
}

func (s *knowledgeService) ensureFAQKnowledge(
	ctx context.Context,
	tenantID uint64,
	kb *types.KnowledgeBase,
) (*types.Knowledge, error) {
	existing, err := s.findFAQKnowledge(ctx, tenantID, kb.ID)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		return existing, nil
	}
	knowledge := &types.Knowledge{
		TenantID:         tenantID,
		KnowledgeBaseID:  kb.ID,
		Type:             types.KnowledgeTypeFAQ,
		Title:            fmt.Sprintf("%s - FAQ", kb.Name),
		Description:      "FAQ 条目容器",
		Source:           types.KnowledgeTypeFAQ,
		ParseStatus:      "completed",
		EnableStatus:     "enabled",
		EmbeddingModelID: kb.EmbeddingModelID,
		CreatedAt:        time.Now(),
		UpdatedAt:        time.Now(),
	}
	if err := s.repo.CreateKnowledge(ctx, knowledge); err != nil {
		return nil, err
	}
	return knowledge, nil
}

// updateFAQImportProgressStatus updates the FAQ import progress in Redis
func (s *knowledgeService) updateFAQImportProgressStatus(
	ctx context.Context,
	taskID string,
	status types.FAQImportTaskStatus,
	progress, total, processed int,
	message, errorMsg string,
) error {
	// Get existing progress from Redis
	existingProgress, err := s.GetFAQImportProgress(ctx, taskID)
	if err != nil {
		// If not found, create a new progress entry
		existingProgress = &types.FAQImportProgress{
			TaskID:    taskID,
			CreatedAt: time.Now().Unix(),
		}
	}

	// Update progress fields
	existingProgress.Status = status
	existingProgress.Progress = progress
	existingProgress.Total = total
	existingProgress.Processed = processed
	if message != "" {
		existingProgress.Message = message
	}
	existingProgress.Error = errorMsg
	if status == types.FAQImportStatusCompleted {
		existingProgress.Error = ""
	}

	// 任务完成或失败时，清除 running key
	if status == types.FAQImportStatusCompleted || status == types.FAQImportStatusFailed {
		if existingProgress.KBID != "" {
			if clearErr := s.clearRunningFAQImportTaskID(ctx, existingProgress.KBID); clearErr != nil {
				logger.Errorf(ctx, "Failed to clear running FAQ import task ID: %v", clearErr)
			}
		}
	}

	return s.saveFAQImportProgress(ctx, existingProgress)
}

// cleanupFAQEntriesFileOnFinalFailure 在任务最终失败时清理对象存储中的 entries 文件
// 只有当 retryCount >= maxRetry 时才执行清理，否则重试时还需要使用这个文件
func (s *knowledgeService) cleanupFAQEntriesFileOnFinalFailure(ctx context.Context, entriesURL string, retryCount, maxRetry int) {
	if entriesURL == "" || retryCount < maxRetry {
		return
	}
	if err := s.fileSvc.DeleteFile(ctx, entriesURL); err != nil {
		logger.Warnf(ctx, "Failed to delete FAQ entries file from object storage on final failure: %v", err)
	} else {
		logger.Infof(ctx, "Deleted FAQ entries file from object storage on final failure: %s", entriesURL)
	}
}

// runningFAQImportInfo stores the task ID and enqueued timestamp for uniquely identifying a task instance
type runningFAQImportInfo struct {
	TaskID     string `json:"task_id"`
	EnqueuedAt int64  `json:"enqueued_at"`
}

// getRunningFAQImportInfo checks if there's a running FAQ import task for the given KB
// Returns the task info if found, nil otherwise
func (s *knowledgeService) getRunningFAQImportInfo(ctx context.Context, kbID string) (*runningFAQImportInfo, error) {
	key := getFAQImportRunningKey(kbID)
	data, err := s.redisClient.Get(ctx, key).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get running FAQ import task: %w", err)
	}

	// Try to parse as JSON first (new format)
	var info runningFAQImportInfo
	if err := json.Unmarshal([]byte(data), &info); err != nil {
		// Fallback: old format was just taskID string
		return &runningFAQImportInfo{TaskID: data, EnqueuedAt: 0}, nil
	}
	return &info, nil
}

// getRunningFAQImportTaskID checks if there's a running FAQ import task for the given KB
// Returns the task ID if found, empty string otherwise (for backward compatibility)
func (s *knowledgeService) getRunningFAQImportTaskID(ctx context.Context, kbID string) (string, error) {
	info, err := s.getRunningFAQImportInfo(ctx, kbID)
	if err != nil {
		return "", err
	}
	if info == nil {
		return "", nil
	}
	return info.TaskID, nil
}

// setRunningFAQImportInfo sets the running task info for a KB
func (s *knowledgeService) setRunningFAQImportInfo(ctx context.Context, kbID string, info *runningFAQImportInfo) error {
	key := getFAQImportRunningKey(kbID)
	data, err := json.Marshal(info)
	if err != nil {
		return fmt.Errorf("failed to marshal running info: %w", err)
	}
	return s.redisClient.Set(ctx, key, data, faqImportProgressTTL).Err()
}

// clearRunningFAQImportTaskID clears the running task ID for a KB
func (s *knowledgeService) clearRunningFAQImportTaskID(ctx context.Context, kbID string) error {
	key := getFAQImportRunningKey(kbID)
	return s.redisClient.Del(ctx, key).Err()
}

func (s *knowledgeService) chunkToFAQEntry(chunk *types.Chunk, kb *types.KnowledgeBase, tagSeqIDMap map[string]int64) (*types.FAQEntry, error) {
	meta, err := chunk.FAQMetadata()
	if err != nil {
		return nil, err
	}
	if meta == nil {
		meta = &types.FAQChunkMetadata{StandardQuestion: chunk.Content}
	}
	// 默认使用 all 策略
	answerStrategy := meta.AnswerStrategy
	if answerStrategy == "" {
		answerStrategy = types.AnswerStrategyAll
	}

	// Get tag seq_id from map
	var tagSeqID int64
	if chunk.TagID != "" && tagSeqIDMap != nil {
		tagSeqID = tagSeqIDMap[chunk.TagID]
	}

	entry := &types.FAQEntry{
		ID:                chunk.SeqID,
		ChunkID:           chunk.ID,
		KnowledgeID:       chunk.KnowledgeID,
		KnowledgeBaseID:   chunk.KnowledgeBaseID,
		TagID:             tagSeqID,
		IsEnabled:         chunk.IsEnabled,
		IsRecommended:     chunk.Flags.HasFlag(types.ChunkFlagRecommended),
		StandardQuestion:  meta.StandardQuestion,
		SimilarQuestions:  meta.SimilarQuestions,
		NegativeQuestions: meta.NegativeQuestions,
		Answers:           meta.Answers,
		AnswerStrategy:    answerStrategy,
		IndexMode:         kb.FAQConfig.IndexMode,
		UpdatedAt:         chunk.UpdatedAt,
		CreatedAt:         chunk.CreatedAt,
		ChunkType:         chunk.ChunkType,
	}
	return entry, nil
}

func buildFAQChunkContent(meta *types.FAQChunkMetadata, mode types.FAQIndexMode) string {
	var builder strings.Builder
	builder.WriteString(fmt.Sprintf("Q: %s\n", meta.StandardQuestion))
	if len(meta.SimilarQuestions) > 0 {
		builder.WriteString("Similar Questions:\n")
		for _, q := range meta.SimilarQuestions {
			builder.WriteString(fmt.Sprintf("- %s\n", q))
		}
	}
	// 负例不应该包含在 Content 中，因为它们不应该被索引
	// 答案根据索引模式决定是否包含
	if mode == types.FAQIndexModeQuestionAnswer && len(meta.Answers) > 0 {
		builder.WriteString("Answers:\n")
		for _, ans := range meta.Answers {
			builder.WriteString(fmt.Sprintf("- %s\n", ans))
		}
	}
	return builder.String()
}

// checkFAQQuestionDuplicate 检查标准问和相似问是否与知识库中其他条目重复
// excludeChunkID 用于排除当前正在编辑的条目（更新时使用）
func (s *knowledgeService) checkFAQQuestionDuplicate(
	ctx context.Context,
	tenantID uint64,
	kbID string,
	excludeChunkID string,
	meta *types.FAQChunkMetadata,
) error {
	// 首先检查当前条目自身的相似问是否与标准问重复
	for _, q := range meta.SimilarQuestions {
		if q == meta.StandardQuestion {
			return werrors.NewBadRequestError(fmt.Sprintf("相似问「%s」不能与标准问相同", q))
		}
	}

	// 检查当前条目自身的相似问之间是否有重复
	seen := make(map[string]struct{})
	for _, q := range meta.SimilarQuestions {
		if _, exists := seen[q]; exists {
			return werrors.NewBadRequestError(fmt.Sprintf("相似问「%s」重复", q))
		}
		seen[q] = struct{}{}
	}

	// 查询知识库中已有的所有FAQ chunks的metadata
	existingChunks, err := s.chunkRepo.ListAllFAQChunksWithMetadataByKnowledgeBaseID(ctx, tenantID, kbID)
	if err != nil {
		return fmt.Errorf("failed to list existing FAQ chunks: %w", err)
	}

	// 构建已存在的标准问和相似问集合
	for _, chunk := range existingChunks {
		// 排除当前正在编辑的条目
		if chunk.ID == excludeChunkID {
			continue
		}

		existingMeta, err := chunk.FAQMetadata()
		if err != nil || existingMeta == nil {
			continue
		}

		// 检查标准问是否重复
		if existingMeta.StandardQuestion == meta.StandardQuestion {
			return werrors.NewBadRequestError(fmt.Sprintf("标准问「%s」已存在", meta.StandardQuestion))
		}

		// 检查当前标准问是否与已有相似问重复
		for _, q := range existingMeta.SimilarQuestions {
			if q == meta.StandardQuestion {
				return werrors.NewBadRequestError(fmt.Sprintf("标准问「%s」与已有相似问重复", meta.StandardQuestion))
			}
		}

		// 检查当前相似问是否与已有标准问重复
		for _, q := range meta.SimilarQuestions {
			if q == existingMeta.StandardQuestion {
				return werrors.NewBadRequestError(fmt.Sprintf("相似问「%s」与已有标准问重复", q))
			}
		}

		// 检查当前相似问是否与已有相似问重复
		for _, q := range meta.SimilarQuestions {
			for _, existingQ := range existingMeta.SimilarQuestions {
				if q == existingQ {
					return werrors.NewBadRequestError(fmt.Sprintf("相似问「%s」已存在", q))
				}
			}
		}
	}

	return nil
}

// resolveTagID resolves tag ID (UUID) from payload, prioritizing tag_id (seq_id) over tag_name
// If no tag is specified, creates or finds the "未分类" tag
// Returns the internal UUID of the tag
func (s *knowledgeService) resolveTagID(ctx context.Context, kbID string, payload *types.FAQEntryPayload) (string, error) {
	tenantID := ctx.Value(types.TenantIDContextKey).(uint64)

	// 如果提供了 tag_id (seq_id)，优先使用 tag_id
	if payload.TagID != 0 {
		tag, err := s.tagRepo.GetBySeqID(ctx, tenantID, payload.TagID)
		if err != nil {
			return "", fmt.Errorf("failed to find tag by seq_id %d: %w", payload.TagID, err)
		}
		return tag.ID, nil
	}

	// 如果提供了 tag_name，查找或创建标签
	if payload.TagName != "" {
		tag, err := s.tagService.FindOrCreateTagByName(ctx, kbID, payload.TagName)
		if err != nil {
			return "", fmt.Errorf("failed to resolve tag by name '%s': %w", payload.TagName, err)
		}
		return tag.ID, nil
	}

	// 都没有提供，使用"未分类"标签
	tag, err := s.tagService.FindOrCreateTagByName(ctx, kbID, types.UntaggedTagName)
	if err != nil {
		return "", fmt.Errorf("failed to get or create default untagged tag: %w", err)
	}
	return tag.ID, nil
}

func sanitizeFAQEntryPayload(payload *types.FAQEntryPayload) (*types.FAQChunkMetadata, error) {
	// 处理 AnswerStrategy，默认为 all
	answerStrategy := types.AnswerStrategyAll
	if payload.AnswerStrategy != nil && *payload.AnswerStrategy != "" {
		switch *payload.AnswerStrategy {
		case types.AnswerStrategyAll, types.AnswerStrategyRandom:
			answerStrategy = *payload.AnswerStrategy
		default:
			return nil, werrors.NewBadRequestError("answer_strategy 必须是 'all' 或 'random'")
		}
	}
	meta := &types.FAQChunkMetadata{
		StandardQuestion:  strings.TrimSpace(payload.StandardQuestion),
		SimilarQuestions:  payload.SimilarQuestions,
		NegativeQuestions: payload.NegativeQuestions,
		Answers:           payload.Answers,
		AnswerStrategy:    answerStrategy,
		Version:           1,
		Source:            "faq",
	}
	meta.Normalize()
	if meta.StandardQuestion == "" {
		return nil, werrors.NewBadRequestError("标准问不能为空")
	}
	if len(meta.Answers) == 0 {
		return nil, werrors.NewBadRequestError("至少提供一个答案")
	}
	return meta, nil
}

func buildFAQIndexContent(meta *types.FAQChunkMetadata, mode types.FAQIndexMode) string {
	var builder strings.Builder
	builder.WriteString(meta.StandardQuestion)
	for _, q := range meta.SimilarQuestions {
		builder.WriteString("\n")
		builder.WriteString(q)
	}
	if mode == types.FAQIndexModeQuestionAnswer {
		for _, ans := range meta.Answers {
			builder.WriteString("\n")
			builder.WriteString(ans)
		}
	}
	return builder.String()
}

// buildFAQIndexInfoList 构建FAQ索引信息列表，支持分别索引模式
func (s *knowledgeService) buildFAQIndexInfoList(
	ctx context.Context,
	kb *types.KnowledgeBase,
	chunk *types.Chunk,
) ([]*types.IndexInfo, error) {
	indexMode := types.FAQIndexModeQuestionAnswer
	questionIndexMode := types.FAQQuestionIndexModeCombined
	if kb.FAQConfig != nil {
		if kb.FAQConfig.IndexMode != "" {
			indexMode = kb.FAQConfig.IndexMode
		}
		if kb.FAQConfig.QuestionIndexMode != "" {
			questionIndexMode = kb.FAQConfig.QuestionIndexMode
		}
	}

	meta, err := chunk.FAQMetadata()
	if err != nil {
		return nil, err
	}
	if meta == nil {
		meta = &types.FAQChunkMetadata{StandardQuestion: chunk.Content}
	}

	// 如果是一起索引模式，使用原有逻辑
	if questionIndexMode == types.FAQQuestionIndexModeCombined {
		content := buildFAQIndexContent(meta, indexMode)
		return []*types.IndexInfo{
			{
				Content:         content,
				SourceID:        chunk.ID,
				SourceType:      types.ChunkSourceType,
				ChunkID:         chunk.ID,
				KnowledgeID:     chunk.KnowledgeID,
				KnowledgeBaseID: chunk.KnowledgeBaseID,
				KnowledgeType:   types.KnowledgeTypeFAQ,
				TagID:           chunk.TagID,
				IsEnabled:       chunk.IsEnabled,
			},
		}, nil
	}

	// 分别索引模式：为每个问题创建独立的索引项
	indexInfoList := make([]*types.IndexInfo, 0)

	// 标准问索引项
	standardContent := meta.StandardQuestion
	if indexMode == types.FAQIndexModeQuestionAnswer && len(meta.Answers) > 0 {
		var builder strings.Builder
		builder.WriteString(meta.StandardQuestion)
		for _, ans := range meta.Answers {
			builder.WriteString("\n")
			builder.WriteString(ans)
		}
		standardContent = builder.String()
	}
	indexInfoList = append(indexInfoList, &types.IndexInfo{
		Content:         standardContent,
		SourceID:        chunk.ID,
		SourceType:      types.ChunkSourceType,
		ChunkID:         chunk.ID,
		KnowledgeID:     chunk.KnowledgeID,
		KnowledgeBaseID: chunk.KnowledgeBaseID,
		KnowledgeType:   types.KnowledgeTypeFAQ,
		TagID:           chunk.TagID,
		IsEnabled:       chunk.IsEnabled,
	})

	// 每个相似问创建一个索引项
	for i, similarQ := range meta.SimilarQuestions {
		similarContent := similarQ
		if indexMode == types.FAQIndexModeQuestionAnswer && len(meta.Answers) > 0 {
			var builder strings.Builder
			builder.WriteString(similarQ)
			for _, ans := range meta.Answers {
				builder.WriteString("\n")
				builder.WriteString(ans)
			}
			similarContent = builder.String()
		}
		sourceID := fmt.Sprintf("%s-%d", chunk.ID, i)
		indexInfoList = append(indexInfoList, &types.IndexInfo{
			Content:         similarContent,
			SourceID:        sourceID,
			SourceType:      types.ChunkSourceType,
			ChunkID:         chunk.ID,
			KnowledgeID:     chunk.KnowledgeID,
			KnowledgeBaseID: chunk.KnowledgeBaseID,
			KnowledgeType:   types.KnowledgeTypeFAQ,
			TagID:           chunk.TagID,
			IsEnabled:       chunk.IsEnabled,
		})
	}

	return indexInfoList, nil
}

// incrementalIndexFAQEntry 增量更新FAQ条目的索引
// 只对内容变化的部分进行embedding计算和索引更新，跳过未变化的部分
func (s *knowledgeService) incrementalIndexFAQEntry(
	ctx context.Context,
	kb *types.KnowledgeBase,
	knowledge *types.Knowledge,
	chunk *types.Chunk,
	embeddingModel embedding.Embedder,
	oldStandardQuestion string,
	oldSimilarQuestions []string,
	oldAnswers []string,
	newMeta *types.FAQChunkMetadata,
) error {
	indexStartTime := time.Now()

	tenantInfo := ctx.Value(types.TenantInfoContextKey).(*types.Tenant)
	retrieveEngine, err := retriever.NewCompositeRetrieveEngine(s.retrieveEngine, tenantInfo.GetEffectiveEngines())
	if err != nil {
		return err
	}

	indexMode := types.FAQIndexModeQuestionAnswer
	if kb.FAQConfig != nil && kb.FAQConfig.IndexMode != "" {
		indexMode = kb.FAQConfig.IndexMode
	}

	// 构建旧的内容（用于比较）
	buildOldContent := func(question string) string {
		if indexMode == types.FAQIndexModeQuestionAnswer && len(oldAnswers) > 0 {
			var builder strings.Builder
			builder.WriteString(question)
			for _, ans := range oldAnswers {
				builder.WriteString("\n")
				builder.WriteString(ans)
			}
			return builder.String()
		}
		return question
	}

	// 构建新的内容
	buildNewContent := func(question string) string {
		if indexMode == types.FAQIndexModeQuestionAnswer && len(newMeta.Answers) > 0 {
			var builder strings.Builder
			builder.WriteString(question)
			for _, ans := range newMeta.Answers {
				builder.WriteString("\n")
				builder.WriteString(ans)
			}
			return builder.String()
		}
		return question
	}

	// 检查答案是否变化
	answersChanged := !slices.Equal(oldAnswers, newMeta.Answers)

	// 收集需要更新的索引项
	var indexInfoToUpdate []*types.IndexInfo

	// 1. 检查标准问是否需要更新
	oldStdContent := buildOldContent(oldStandardQuestion)
	newStdContent := buildNewContent(newMeta.StandardQuestion)
	if oldStdContent != newStdContent {
		indexInfoToUpdate = append(indexInfoToUpdate, &types.IndexInfo{
			Content:         newStdContent,
			SourceID:        chunk.ID,
			SourceType:      types.ChunkSourceType,
			ChunkID:         chunk.ID,
			KnowledgeID:     chunk.KnowledgeID,
			KnowledgeBaseID: chunk.KnowledgeBaseID,
			KnowledgeType:   types.KnowledgeTypeFAQ,
			TagID:           chunk.TagID,
			IsEnabled:       chunk.IsEnabled,
			IsRecommended:   chunk.Flags.HasFlag(types.ChunkFlagRecommended),
		})
	}

	// 2. 检查每个相似问是否需要更新
	oldCount := len(oldSimilarQuestions)
	newCount := len(newMeta.SimilarQuestions)

	for i, newQ := range newMeta.SimilarQuestions {
		needUpdate := false
		if i >= oldCount {
			// 新增的相似问
			needUpdate = true
		} else {
			// 已存在的相似问，检查内容是否变化
			oldQ := oldSimilarQuestions[i]
			if oldQ != newQ || answersChanged {
				needUpdate = true
			}
		}

		if needUpdate {
			sourceID := fmt.Sprintf("%s-%d", chunk.ID, i)
			indexInfoToUpdate = append(indexInfoToUpdate, &types.IndexInfo{
				Content:         buildNewContent(newQ),
				SourceID:        sourceID,
				SourceType:      types.ChunkSourceType,
				ChunkID:         chunk.ID,
				KnowledgeID:     chunk.KnowledgeID,
				KnowledgeBaseID: chunk.KnowledgeBaseID,
				KnowledgeType:   types.KnowledgeTypeFAQ,
				TagID:           chunk.TagID,
				IsEnabled:       chunk.IsEnabled,
				IsRecommended:   chunk.Flags.HasFlag(types.ChunkFlagRecommended),
			})
		}
	}

	// 3. 删除多余的旧相似问索引
	if oldCount > newCount {
		sourceIDsToDelete := make([]string, 0, oldCount-newCount)
		for i := newCount; i < oldCount; i++ {
			sourceIDsToDelete = append(sourceIDsToDelete, fmt.Sprintf("%s-%d", chunk.ID, i))
		}
		logger.Debugf(ctx, "incrementalIndexFAQEntry: deleting %d obsolete source IDs", len(sourceIDsToDelete))
		if delErr := retrieveEngine.DeleteBySourceIDList(ctx, sourceIDsToDelete, embeddingModel.GetDimensions(), types.KnowledgeTypeFAQ); delErr != nil {
			logger.Warnf(ctx, "incrementalIndexFAQEntry: failed to delete obsolete source IDs: %v", delErr)
		}
	}

	// 4. 批量索引需要更新的内容
	if len(indexInfoToUpdate) > 0 {
		logger.Debugf(ctx, "incrementalIndexFAQEntry: updating %d index entries (skipped %d unchanged)",
			len(indexInfoToUpdate), 1+newCount-len(indexInfoToUpdate))
		if err := retrieveEngine.BatchIndex(ctx, embeddingModel, indexInfoToUpdate); err != nil {
			return err
		}
	} else {
		logger.Debugf(ctx, "incrementalIndexFAQEntry: all %d entries unchanged, skipping index update", 1+newCount)
	}

	// 5. 更新 knowledge 记录
	now := time.Now()
	knowledge.UpdatedAt = now
	knowledge.ProcessedAt = &now
	if err := s.repo.UpdateKnowledge(ctx, knowledge); err != nil {
		return err
	}

	totalDuration := time.Since(indexStartTime)
	logger.Debugf(ctx, "incrementalIndexFAQEntry: completed in %v, updated %d/%d entries",
		totalDuration, len(indexInfoToUpdate), 1+newCount)

	return nil
}

func (s *knowledgeService) indexFAQChunks(ctx context.Context,
	kb *types.KnowledgeBase, knowledge *types.Knowledge,
	chunks []*types.Chunk, embeddingModel embedding.Embedder,
	adjustStorage bool, needDelete bool,
) error {
	if len(chunks) == 0 {
		return nil
	}
	indexStartTime := time.Now()
	logger.Debugf(ctx, "indexFAQChunks: starting to index %d chunks", len(chunks))

	tenantInfo := ctx.Value(types.TenantInfoContextKey).(*types.Tenant)
	retrieveEngine, err := retriever.NewCompositeRetrieveEngine(s.retrieveEngine, tenantInfo.GetEffectiveEngines())
	if err != nil {
		return err
	}

	// 构建索引信息
	buildIndexInfoStartTime := time.Now()
	indexInfo := make([]*types.IndexInfo, 0)
	chunkIDs := make([]string, 0, len(chunks))
	for _, chunk := range chunks {
		infoList, err := s.buildFAQIndexInfoList(ctx, kb, chunk)
		if err != nil {
			return err
		}
		indexInfo = append(indexInfo, infoList...)
		chunkIDs = append(chunkIDs, chunk.ID)
	}
	buildIndexInfoDuration := time.Since(buildIndexInfoStartTime)
	logger.Debugf(
		ctx,
		"indexFAQChunks: built %d index info entries for %d chunks in %v",
		len(indexInfo),
		len(chunks),
		buildIndexInfoDuration,
	)

	var size int64
	if adjustStorage {
		estimateStartTime := time.Now()
		size = retrieveEngine.EstimateStorageSize(ctx, embeddingModel, indexInfo)
		estimateDuration := time.Since(estimateStartTime)
		logger.Debugf(ctx, "indexFAQChunks: estimated storage size %d bytes in %v", size, estimateDuration)
		if tenantInfo.StorageQuota > 0 && tenantInfo.StorageUsed+size > tenantInfo.StorageQuota {
			return types.NewStorageQuotaExceededError()
		}
	}

	// 删除旧向量
	var deleteDuration time.Duration
	if needDelete {
		deleteStartTime := time.Now()
		if err := retrieveEngine.DeleteByChunkIDList(ctx, chunkIDs, embeddingModel.GetDimensions(), types.KnowledgeTypeFAQ); err != nil {
			logger.Warnf(ctx, "Delete FAQ vectors failed: %v", err)
		}
		deleteDuration = time.Since(deleteStartTime)
		if deleteDuration > 100*time.Millisecond {
			logger.Debugf(ctx, "indexFAQChunks: deleted old vectors for %d chunks in %v", len(chunkIDs), deleteDuration)
		}
	}

	// 批量索引（这里可能是性能瓶颈）
	batchIndexStartTime := time.Now()
	if err := retrieveEngine.BatchIndex(ctx, embeddingModel, indexInfo); err != nil {
		return err
	}
	batchIndexDuration := time.Since(batchIndexStartTime)
	logger.Debugf(ctx, "indexFAQChunks: batch indexed %d index info entries in %v (avg: %v per entry)",
		len(indexInfo), batchIndexDuration, batchIndexDuration/time.Duration(len(indexInfo)))

	if adjustStorage && size > 0 {
		adjustStartTime := time.Now()
		if err := s.tenantRepo.AdjustStorageUsed(ctx, tenantInfo.ID, size); err == nil {
			tenantInfo.StorageUsed += size
		}
		knowledge.StorageSize += size
		adjustDuration := time.Since(adjustStartTime)
		if adjustDuration > 50*time.Millisecond {
			logger.Debugf(ctx, "indexFAQChunks: adjusted storage in %v", adjustDuration)
		}
	}

	updateStartTime := time.Now()
	now := time.Now()
	knowledge.UpdatedAt = now
	knowledge.ProcessedAt = &now
	err = s.repo.UpdateKnowledge(ctx, knowledge)
	updateDuration := time.Since(updateStartTime)
	if updateDuration > 50*time.Millisecond {
		logger.Debugf(ctx, "indexFAQChunks: updated knowledge in %v", updateDuration)
	}

	totalDuration := time.Since(indexStartTime)
	logger.Debugf(
		ctx,
		"indexFAQChunks: completed indexing %d chunks in %v (build: %v, delete: %v, batchIndex: %v, update: %v)",
		len(chunks),
		totalDuration,
		buildIndexInfoDuration,
		deleteDuration,
		batchIndexDuration,
		updateDuration,
	)

	return err
}

func (s *knowledgeService) deleteFAQChunkVectors(ctx context.Context,
	kb *types.KnowledgeBase, knowledge *types.Knowledge, chunks []*types.Chunk,
) error {
	if len(chunks) == 0 {
		return nil
	}
	embeddingModel, err := s.modelService.GetEmbeddingModel(ctx, kb.EmbeddingModelID)
	if err != nil {
		return err
	}
	tenantInfo := ctx.Value(types.TenantInfoContextKey).(*types.Tenant)
	retrieveEngine, err := retriever.NewCompositeRetrieveEngine(s.retrieveEngine, tenantInfo.GetEffectiveEngines())
	if err != nil {
		return err
	}

	indexInfo := make([]*types.IndexInfo, 0)
	chunkIDs := make([]string, 0, len(chunks))
	for _, chunk := range chunks {
		infoList, err := s.buildFAQIndexInfoList(ctx, kb, chunk)
		if err != nil {
			return err
		}
		indexInfo = append(indexInfo, infoList...)
		chunkIDs = append(chunkIDs, chunk.ID)
	}

	size := retrieveEngine.EstimateStorageSize(ctx, embeddingModel, indexInfo)
	if err := retrieveEngine.DeleteByChunkIDList(ctx, chunkIDs, embeddingModel.GetDimensions(), types.KnowledgeTypeFAQ); err != nil {
		return err
	}
	if size > 0 {
		if err := s.tenantRepo.AdjustStorageUsed(ctx, tenantInfo.ID, -size); err == nil {
			tenantInfo.StorageUsed -= size
			if tenantInfo.StorageUsed < 0 {
				tenantInfo.StorageUsed = 0
			}
		}
		if knowledge.StorageSize >= size {
			knowledge.StorageSize -= size
		} else {
			knowledge.StorageSize = 0
		}
	}
	knowledge.UpdatedAt = time.Now()
	return s.repo.UpdateKnowledge(ctx, knowledge)
}

func ensureManualFileName(title string) string {
	if title == "" {
		return fmt.Sprintf("manual-%s%s", time.Now().Format("20060102-150405"), manualFileExtension)
	}
	trimmed := strings.TrimSpace(title)
	if strings.HasSuffix(strings.ToLower(trimmed), manualFileExtension) {
		return trimmed
	}
	return trimmed + manualFileExtension
}

func (s *knowledgeService) triggerManualProcessing(ctx context.Context,
	kb *types.KnowledgeBase, knowledge *types.Knowledge, content string, sync bool,
) {
	clean := strings.TrimSpace(content)
	if clean == "" {
		return
	}

	// 使用 docreader 按照 MD 格式处理，并使用知识库配置的分隔符
	contentBytes := []byte(clean)
	fileName := ensureManualFileName(knowledge.Title)
	fileType := "md"

	// 检查是否需要启用多模态（对于手动内容通常不需要，但保持一致性）
	enableMultimodel := kb.IsMultimodalEnabled() && kb.StorageConfig.Provider != ""

	var vlmConfig *proto.VLMConfig
	if enableMultimodel {
		cfg, cfgErr := s.getVLMProtoConfig(ctx, kb)
		if cfgErr != nil {
			logger.GetLogger(ctx).WithField("knowledge_id", knowledge.ID).
				WithField("error", cfgErr).Errorf("triggerManualProcessing build VLM config failed")
			knowledge.ParseStatus = "failed"
			knowledge.ErrorMessage = cfgErr.Error()
			knowledge.UpdatedAt = time.Now()
			s.repo.UpdateKnowledge(ctx, knowledge)
			return
		}
		if cfg == nil {
			logger.GetLogger(ctx).WithField("knowledge_id", knowledge.ID).
				Error("triggerManualProcessing enable multimodal but VLM config missing")
		}
		vlmConfig = cfg
	}

	// 调用 docreader 解析 markdown 内容
	resp, err := s.docReaderClient.ReadFromFile(ctx, &proto.ReadFromFileRequest{
		FileContent: contentBytes,
		FileName:    fileName,
		FileType:    fileType,
		ReadConfig: &proto.ReadConfig{
			ChunkSize:        int32(kb.ChunkingConfig.ChunkSize),
			ChunkOverlap:     int32(kb.ChunkingConfig.ChunkOverlap),
			Separators:       kb.ChunkingConfig.Separators,
			EnableMultimodal: enableMultimodel,
			StorageConfig: &proto.StorageConfig{
				Provider: proto.StorageProvider(
					proto.StorageProvider_value[strings.ToUpper(kb.StorageConfig.Provider)],
				),
				Region:          kb.StorageConfig.Region,
				BucketName:      kb.StorageConfig.BucketName,
				AccessKeyId:     kb.StorageConfig.SecretID,
				SecretAccessKey: kb.StorageConfig.SecretKey,
				AppId:           kb.StorageConfig.AppID,
				PathPrefix:      kb.StorageConfig.PathPrefix,
			},
			VlmConfig: vlmConfig,
		},
		RequestId: ctx.Value(types.RequestIDContextKey).(string),
	})
	if err != nil {
		logger.GetLogger(ctx).WithField("knowledge_id", knowledge.ID).
			WithField("error", err).Errorf("triggerManualProcessing read file failed")
		knowledge.ParseStatus = "failed"
		knowledge.ErrorMessage = err.Error()
		knowledge.UpdatedAt = time.Now()
		s.repo.UpdateKnowledge(ctx, knowledge)
		return
	}

	if sync {
		s.processChunks(ctx, kb, knowledge, resp.Chunks)
		return
	}

	newCtx := logger.CloneContext(ctx)
	go s.processChunks(newCtx, kb, knowledge, resp.Chunks)
}

func (s *knowledgeService) cleanupKnowledgeResources(ctx context.Context, knowledge *types.Knowledge) error {
	logger.GetLogger(ctx).Infof("Cleaning knowledge resources before manual update, knowledge ID: %s", knowledge.ID)

	var cleanupErr error

	if knowledge.ParseStatus == types.ManualKnowledgeStatusDraft && knowledge.StorageSize == 0 {
		// Draft without indexed data, skip cleanup.
		return nil
	}

	tenantInfo := ctx.Value(types.TenantInfoContextKey).(*types.Tenant)
	if knowledge.EmbeddingModelID != "" {
		retrieveEngine, err := retriever.NewCompositeRetrieveEngine(
			s.retrieveEngine,
			tenantInfo.GetEffectiveEngines(),
		)
		if err != nil {
			logger.GetLogger(ctx).WithField("error", err).Error("Failed to init retrieve engine during cleanup")
			cleanupErr = errors.Join(cleanupErr, err)
		} else {
			embeddingModel, modelErr := s.modelService.GetEmbeddingModel(ctx, knowledge.EmbeddingModelID)
			if modelErr != nil {
				logger.GetLogger(ctx).WithField("error", modelErr).Error("Failed to get embedding model during cleanup")
				cleanupErr = errors.Join(cleanupErr, modelErr)
			} else {
				if err := retrieveEngine.DeleteByKnowledgeIDList(ctx, []string{knowledge.ID}, embeddingModel.GetDimensions(), knowledge.Type); err != nil {
					logger.GetLogger(ctx).WithField("error", err).Error("Failed to delete manual knowledge index")
					cleanupErr = errors.Join(cleanupErr, err)
				}
			}
		}
	}

	if err := s.chunkService.DeleteChunksByKnowledgeID(ctx, knowledge.ID); err != nil {
		logger.GetLogger(ctx).WithField("error", err).Error("Failed to delete manual knowledge chunks")
		cleanupErr = errors.Join(cleanupErr, err)
	}

	namespace := types.NameSpace{KnowledgeBase: knowledge.KnowledgeBaseID, Knowledge: knowledge.ID}
	if err := s.graphEngine.DelGraph(ctx, []types.NameSpace{namespace}); err != nil {
		logger.GetLogger(ctx).WithField("error", err).Error("Failed to delete manual knowledge graph data")
		cleanupErr = errors.Join(cleanupErr, err)
	}

	if knowledge.StorageSize > 0 {
		tenantInfo.StorageUsed -= knowledge.StorageSize
		if tenantInfo.StorageUsed < 0 {
			tenantInfo.StorageUsed = 0
		}
		if err := s.tenantRepo.AdjustStorageUsed(ctx, tenantInfo.ID, -knowledge.StorageSize); err != nil {
			logger.GetLogger(ctx).WithField("error", err).Error("Failed to adjust storage usage during manual cleanup")
			cleanupErr = errors.Join(cleanupErr, err)
		}
		knowledge.StorageSize = 0
	}

	return cleanupErr
}

func (s *knowledgeService) getVLMProtoConfig(ctx context.Context, kb *types.KnowledgeBase) (*proto.VLMConfig, error) {
	if kb == nil {
		return nil, nil
	}
	// 兼容老版本：直接使用 ModelName 和 BaseURL
	if kb.VLMConfig.ModelName != "" && kb.VLMConfig.BaseURL != "" {
		return &proto.VLMConfig{
			ModelName:     kb.VLMConfig.ModelName,
			BaseUrl:       kb.VLMConfig.BaseURL,
			ApiKey:        kb.VLMConfig.APIKey,
			InterfaceType: kb.VLMConfig.InterfaceType,
		}, nil
	}

	// 新版本：未启用或无模型ID时返回nil
	if !kb.VLMConfig.Enabled || kb.VLMConfig.ModelID == "" {
		return nil, nil
	}

	model, err := s.modelService.GetModelByID(ctx, kb.VLMConfig.ModelID)
	if err != nil {
		return nil, err
	}

	interfaceType := model.Parameters.InterfaceType
	if interfaceType == "" {
		interfaceType = "openai"
	}

	return &proto.VLMConfig{
		ModelName:     model.Name,
		BaseUrl:       model.Parameters.BaseURL,
		ApiKey:        model.Parameters.APIKey,
		InterfaceType: interfaceType,
	}, nil
}

func IsImageType(fileType string) bool {
	switch fileType {
	case "jpg", "jpeg", "png", "gif", "webp", "bmp", "svg", "tiff":
		return true
	default:
		return false
	}
}

// ProcessDocument handles Asynq document processing tasks
func (s *knowledgeService) ProcessDocument(ctx context.Context, t *asynq.Task) error {
	var payload types.DocumentProcessPayload
	if err := json.Unmarshal(t.Payload(), &payload); err != nil {
		logger.Errorf(ctx, "failed to unmarshal document process task payload: %v", err)
		return nil
	}

	ctx = logger.WithRequestID(ctx, payload.RequestId)
	ctx = logger.WithField(ctx, "document_process", payload.KnowledgeID)
	ctx = context.WithValue(ctx, types.TenantIDContextKey, payload.TenantID)

	// 获取任务重试信息，用于判断是否是最后一次重试
	retryCount, _ := asynq.GetRetryCount(ctx)
	maxRetry, _ := asynq.GetMaxRetry(ctx)
	isLastRetry := retryCount >= maxRetry

	tenantInfo, err := s.tenantRepo.GetTenantByID(ctx, payload.TenantID)
	if err != nil {
		logger.Errorf(ctx, "failed to get tenant: %v", err)
		return nil
	}
	ctx = context.WithValue(ctx, types.TenantInfoContextKey, tenantInfo)

	logger.Infof(ctx, "Processing document task: knowledge_id=%s, file_path=%s, retry=%d/%d",
		payload.KnowledgeID, payload.FilePath, retryCount, maxRetry)

	// 幂等性检查：获取knowledge记录
	knowledge, err := s.repo.GetKnowledgeByID(ctx, payload.TenantID, payload.KnowledgeID)
	if err != nil {
		logger.Errorf(ctx, "failed to get knowledge: %v", err)
		return nil
	}

	if knowledge == nil {
		return nil
	}

	// 检查是否正在删除 - 如果是则直接退出，避免与删除操作冲突
	if knowledge.ParseStatus == types.ParseStatusDeleting {
		logger.Infof(ctx, "Knowledge is being deleted, aborting processing: %s", payload.KnowledgeID)
		return nil
	}

	// 检查任务状态 - 幂等性处理
	if knowledge.ParseStatus == types.ParseStatusCompleted {
		logger.Infof(ctx, "Document already completed, skipping: %s", payload.KnowledgeID)
		return nil // 幂等：已完成的任务直接返回
	}

	if knowledge.ParseStatus == types.ParseStatusFailed {
		// 检查是否可恢复（例如：超时、临时错误等）
		// 对于不可恢复的错误，直接返回
		logger.Warnf(
			ctx,
			"Document processing previously failed: %s, error: %s",
			payload.KnowledgeID,
			knowledge.ErrorMessage,
		)
		// 这里可以根据错误类型判断是否可恢复，暂时允许重试
	}

	// 检查是否有部分处理（有chunks但状态不是completed）
	if knowledge.ParseStatus != "completed" && knowledge.ParseStatus != "pending" &&
		knowledge.ParseStatus != "processing" {
		// 状态异常，记录日志但继续处理
		logger.Warnf(ctx, "Unexpected parse status: %s for knowledge: %s", knowledge.ParseStatus, payload.KnowledgeID)
	}

	// 获取知识库信息
	kb, err := s.kbService.GetKnowledgeBaseByID(ctx, payload.KnowledgeBaseID)
	if err != nil {
		logger.Errorf(ctx, "failed to get knowledge base: %v", err)
		knowledge.ParseStatus = "failed"
		knowledge.ErrorMessage = fmt.Sprintf("failed to get knowledge base: %v", err)
		knowledge.UpdatedAt = time.Now()
		s.repo.UpdateKnowledge(ctx, knowledge)
		return nil
	}

	knowledge.ParseStatus = "processing"
	knowledge.UpdatedAt = time.Now()
	if err := s.repo.UpdateKnowledge(ctx, knowledge); err != nil {
		logger.Errorf(ctx, "failed to update knowledge status to processing: %v", err)
		return nil
	}

	// 构建VLM配置（如果需要）
	var vlmConfig *proto.VLMConfig
	if payload.EnableMultimodel {
		vlmConfig, err = s.getVLMProtoConfig(ctx, kb)
		if err != nil {
			logger.GetLogger(ctx).WithField("knowledge_id", knowledge.ID).
				WithField("error", err).Errorf("processDocument build VLM config failed")
		}
		if vlmConfig == nil {
			logger.GetLogger(ctx).WithField("knowledge_id", knowledge.ID).
				Warn("processDocument enable multimodal but VLM config missing")
		}
	}

	// 检查多模态配置（仅对文件导入）
	if payload.FilePath != "" && !payload.EnableMultimodel && IsImageType(payload.FileType) {
		logger.GetLogger(ctx).WithField("knowledge_id", knowledge.ID).
			WithField("error", ErrImageNotParse).Errorf("processDocument image without enable multimodel")
		knowledge.ParseStatus = "failed"
		knowledge.ErrorMessage = ErrImageNotParse.Error()
		knowledge.UpdatedAt = time.Now()
		s.repo.UpdateKnowledge(ctx, knowledge)
		return nil
	}

	// 处理不同类型的导入：文件、URL、文本段落
	var chunks []*proto.Chunk
	if payload.URL != "" {
		// URL导入 - 再次进行 SSRF 验证（防止 DNS 重绑定攻击）
		if safe, reason := secutils.IsSSRFSafeURL(payload.URL); !safe {
			logger.Errorf(ctx, "URL rejected for SSRF protection in ProcessDocument: %s, reason: %s", payload.URL, reason)
			knowledge.ParseStatus = "failed"
			knowledge.ErrorMessage = "URL is not allowed for security reasons"
			knowledge.UpdatedAt = time.Now()
			s.repo.UpdateKnowledge(ctx, knowledge)
			return nil
		}

		urlResp, err := s.docReaderClient.ReadFromURL(ctx, &proto.ReadFromURLRequest{
			Url:   payload.URL,
			Title: knowledge.Title,
			ReadConfig: &proto.ReadConfig{
				ChunkSize:        int32(kb.ChunkingConfig.ChunkSize),
				ChunkOverlap:     int32(kb.ChunkingConfig.ChunkOverlap),
				Separators:       kb.ChunkingConfig.Separators,
				EnableMultimodal: payload.EnableMultimodel,
				StorageConfig: &proto.StorageConfig{
					Provider: proto.StorageProvider(
						proto.StorageProvider_value[strings.ToUpper(kb.StorageConfig.Provider)],
					),
					Region:          kb.StorageConfig.Region,
					BucketName:      kb.StorageConfig.BucketName,
					AccessKeyId:     kb.StorageConfig.SecretID,
					SecretAccessKey: kb.StorageConfig.SecretKey,
					AppId:           kb.StorageConfig.AppID,
					PathPrefix:      kb.StorageConfig.PathPrefix,
				},
				VlmConfig: vlmConfig,
			},
			RequestId: payload.RequestId,
		})
		if err != nil {
			// 如果是最后一次重试，更新状态为失败
			if isLastRetry {
				knowledge.ParseStatus = "failed"
				knowledge.ErrorMessage = err.Error()
				knowledge.UpdatedAt = time.Now()
				s.repo.UpdateKnowledge(ctx, knowledge)
			}
			return fmt.Errorf("failed to read from URL: %w", err)
		}
		chunks = urlResp.Chunks
	} else if len(payload.Passages) > 0 {
		// 文本段落导入
		chunks := make([]*proto.Chunk, 0, len(payload.Passages))
		start, end := 0, 0
		for i, p := range payload.Passages {
			if p == "" {
				continue
			}
			end += len([]rune(p))
			chunk := &proto.Chunk{
				Content: p,
				Seq:     int32(i),
				Start:   int32(start),
				End:     int32(end),
			}
			start = end
			chunks = append(chunks, chunk)
		}
		// 直接处理chunks，不需要调用docReader
		s.processChunks(ctx, kb, knowledge, chunks)
		return nil
	} else {
		// 文件导入
		fileReader, err := s.fileSvc.GetFile(ctx, payload.FilePath)
		if err != nil {
			logger.GetLogger(ctx).WithField("knowledge_id", knowledge.ID).
				WithField("error", err).Errorf("processDocument get file failed")
			// 如果是最后一次重试，更新状态为失败
			if isLastRetry {
				knowledge.ParseStatus = "failed"
				knowledge.ErrorMessage = err.Error()
				knowledge.UpdatedAt = time.Now()
				s.repo.UpdateKnowledge(ctx, knowledge)
			}
			return fmt.Errorf("failed to get file: %w", err)
		}
		defer fileReader.Close()

		// 读取文件内容
		contentBytes, err := io.ReadAll(fileReader)
		if err != nil {
			// 如果是最后一次重试，更新状态为失败
			if isLastRetry {
				knowledge.ParseStatus = "failed"
				knowledge.ErrorMessage = err.Error()
				knowledge.UpdatedAt = time.Now()
				s.repo.UpdateKnowledge(ctx, knowledge)
			}
			return fmt.Errorf("failed to read file: %w", err)
		}

		// 调用docReader处理文件
		fileResp, err := s.docReaderClient.ReadFromFile(ctx, &proto.ReadFromFileRequest{
			FileContent: contentBytes,
			FileName:    payload.FileName,
			FileType:    payload.FileType,
			ReadConfig: &proto.ReadConfig{
				ChunkSize:        int32(kb.ChunkingConfig.ChunkSize),
				ChunkOverlap:     int32(kb.ChunkingConfig.ChunkOverlap),
				Separators:       kb.ChunkingConfig.Separators,
				EnableMultimodal: payload.EnableMultimodel,
				StorageConfig: &proto.StorageConfig{
					Provider:        proto.StorageProvider(proto.StorageProvider_value[strings.ToUpper(kb.StorageConfig.Provider)]),
					Region:          kb.StorageConfig.Region,
					BucketName:      kb.StorageConfig.BucketName,
					AccessKeyId:     kb.StorageConfig.SecretID,
					SecretAccessKey: kb.StorageConfig.SecretKey,
					AppId:           kb.StorageConfig.AppID,
					PathPrefix:      kb.StorageConfig.PathPrefix,
				},
				VlmConfig: vlmConfig,
			},
			RequestId: payload.RequestId,
		})
		if err != nil {
			logger.GetLogger(ctx).WithField("knowledge_id", knowledge.ID).
				WithField("error", err).Errorf("processDocument read file failed")
			// 如果是最后一次重试，更新状态为失败
			if isLastRetry {
				knowledge.ParseStatus = "failed"
				knowledge.ErrorMessage = err.Error()
				knowledge.UpdatedAt = time.Now()
				s.repo.UpdateKnowledge(ctx, knowledge)
			}
			return fmt.Errorf("failed to read file from docreader: %w", err)
		}
		chunks = fileResp.Chunks
	}

	// 处理chunks（这会更新状态为completed）
	s.processChunks(ctx, kb, knowledge, chunks, ProcessChunksOptions{
		EnableQuestionGeneration: payload.EnableQuestionGeneration,
		QuestionCount:            payload.QuestionCount,
	})

	return nil
}

// ProcessFAQImport handles Asynq FAQ import tasks (including dry run mode)
func (s *knowledgeService) ProcessFAQImport(ctx context.Context, t *asynq.Task) error {
	var payload types.FAQImportPayload
	if err := json.Unmarshal(t.Payload(), &payload); err != nil {
		logger.Errorf(ctx, "failed to unmarshal FAQ import task payload: %v", err)
		return fmt.Errorf("failed to unmarshal task payload: %w", err)
	}

	ctx = logger.WithRequestID(ctx, uuid.New().String())
	ctx = logger.WithField(ctx, "faq_import", payload.TaskID)
	ctx = context.WithValue(ctx, types.TenantIDContextKey, payload.TenantID)

	// 获取任务重试信息，用于判断是否是最后一次重试
	retryCount, _ := asynq.GetRetryCount(ctx)
	maxRetry, _ := asynq.GetMaxRetry(ctx)
	isLastRetry := retryCount >= maxRetry

	tenantInfo, err := s.tenantRepo.GetTenantByID(ctx, payload.TenantID)
	if err != nil {
		logger.Errorf(ctx, "failed to get tenant: %v", err)
		return nil
	}
	ctx = context.WithValue(ctx, types.TenantInfoContextKey, tenantInfo)

	// 如果 entries 存储在对象存储中，先下载
	if payload.EntriesURL != "" && len(payload.Entries) == 0 {
		logger.Infof(ctx, "Downloading FAQ entries from object storage: %s", payload.EntriesURL)
		reader, err := s.fileSvc.GetFile(ctx, payload.EntriesURL)
		if err != nil {
			logger.Errorf(ctx, "Failed to download FAQ entries from object storage: %v", err)
			return fmt.Errorf("failed to download entries: %w", err)
		}
		defer reader.Close()

		entriesData, err := io.ReadAll(reader)
		if err != nil {
			logger.Errorf(ctx, "Failed to read FAQ entries data: %v", err)
			return fmt.Errorf("failed to read entries data: %w", err)
		}

		var entries []types.FAQEntryPayload
		if err := json.Unmarshal(entriesData, &entries); err != nil {
			logger.Errorf(ctx, "Failed to unmarshal FAQ entries: %v", err)
			return fmt.Errorf("failed to unmarshal entries: %w", err)
		}

		payload.Entries = entries
		logger.Infof(ctx, "Downloaded %d FAQ entries from object storage", len(entries))
	}

	logger.Infof(ctx, "Processing FAQ import task: task_id=%s, kb_id=%s, total_entries=%d, dry_run=%v, retry=%d/%d",
		payload.TaskID, payload.KBID, len(payload.Entries), payload.DryRun, retryCount, maxRetry)

	// 保存原始总数量
	originalTotalEntries := len(payload.Entries)

	// 初始化进度
	// 检查是否已有验证结果（用于重试时跳过验证）
	// 注意：必须在保存新 progress 之前查询，否则会被覆盖
	existingProgress, _ := s.GetFAQImportProgress(ctx, payload.TaskID)

	progress := &types.FAQImportProgress{
		TaskID:         payload.TaskID,
		KBID:           payload.KBID,
		KnowledgeID:    payload.KnowledgeID,
		Status:         types.FAQImportStatusProcessing,
		Progress:       0,
		Total:          originalTotalEntries,
		Processed:      0,
		SuccessCount:   0,
		FailedCount:    0,
		FailedEntries:  make([]types.FAQFailedEntry, 0),
		SuccessEntries: make([]types.FAQSuccessEntry, 0),
		Message:        "正在验证条目...",
		CreatedAt:      time.Now().Unix(),
		UpdatedAt:      time.Now().Unix(),
		DryRun:         payload.DryRun,
	}
	if err := s.saveFAQImportProgress(ctx, progress); err != nil {
		logger.Warnf(ctx, "Failed to save initial FAQ import progress: %v", err)
	}

	var validEntryIndices []int
	if existingProgress != nil && len(existingProgress.ValidEntryIndices) > 0 {
		// 重试时直接使用之前的验证结果
		validEntryIndices = existingProgress.ValidEntryIndices
		progress.FailedCount = existingProgress.FailedCount
		progress.FailedEntries = existingProgress.FailedEntries
		logger.Infof(ctx, "Reusing previous validation result: valid=%d, failed=%d",
			len(validEntryIndices), progress.FailedCount)
	} else {
		// 第一步：执行验证（无论是 dry run 还是 import 模式都需要验证）
		validEntryIndices = s.executeFAQDryRunValidation(ctx, &payload, progress)
		// 保存验证通过的索引，用于重试时跳过验证
		progress.ValidEntryIndices = validEntryIndices
		if err := s.saveFAQImportProgress(ctx, progress); err != nil {
			logger.Warnf(ctx, "Failed to save validation result: %v", err)
		}
		logger.Infof(ctx, "FAQ validation completed: total=%d, valid=%d, failed=%d",
			originalTotalEntries, len(validEntryIndices), progress.FailedCount)
	}

	// Dry run 模式：验证完成后直接返回结果
	if payload.DryRun {
		return s.finalizeFAQValidation(ctx, &payload, progress, originalTotalEntries)
	}

	// Import 模式：检查是否有有效条目需要导入
	if len(validEntryIndices) == 0 {
		// 没有有效条目，直接完成
		return s.finalizeFAQValidation(ctx, &payload, progress, originalTotalEntries)
	}

	// 提取有效的条目
	validEntries := make([]types.FAQEntryPayload, 0, len(validEntryIndices))
	for _, idx := range validEntryIndices {
		validEntries = append(validEntries, payload.Entries[idx])
	}

	// 更新进度消息
	progress.Message = fmt.Sprintf("验证完成，开始导入 %d 条有效数据...", len(validEntries))
	progress.UpdatedAt = time.Now().Unix()
	if err := s.saveFAQImportProgress(ctx, progress); err != nil {
		logger.Warnf(ctx, "Failed to update FAQ import progress: %v", err)
	}

	// 幂等性检查：获取knowledge记录（FAQ任务使用knowledge ID作为taskID）
	knowledge, err := s.repo.GetKnowledgeByID(ctx, payload.TenantID, payload.KnowledgeID)
	if err != nil {
		logger.Errorf(ctx, "failed to get FAQ knowledge: %v", err)
		return nil
	}

	if knowledge == nil {
		return nil
	}

	kb, err := s.kbService.GetKnowledgeBaseByID(ctx, payload.KBID)
	if err != nil {
		logger.Errorf(ctx, "Failed to get knowledge base: %v", err)
		// 如果是最后一次重试，更新状态为失败
		if isLastRetry {
			if updateErr := s.updateFAQImportProgressStatus(ctx, payload.TaskID, types.FAQImportStatusFailed, 0, originalTotalEntries, 0, "获取知识库失败", err.Error()); updateErr != nil {
				logger.Errorf(ctx, "Failed to update task status to failed: %v", updateErr)
			}
		}
		s.cleanupFAQEntriesFileOnFinalFailure(ctx, payload.EntriesURL, retryCount, maxRetry)
		return fmt.Errorf("failed to get knowledge base: %w", err)
	}

	// 检查任务状态 - 幂等性处理（复用之前获取的 existingProgress）
	var processedCount int
	if existingProgress != nil {
		if existingProgress.Status == types.FAQImportStatusCompleted {
			logger.Infof(ctx, "FAQ import already completed, skipping: %s", payload.TaskID)
			return nil // 幂等：已完成的任务直接返回
		}
		// 获取已处理的数量（注意：这是相对于 validEntries 的索引）
		processedCount = existingProgress.Processed - progress.FailedCount // 已处理数 - 验证失败数 = 已导入的有效条目数
		if processedCount < 0 {
			processedCount = 0
		}
		logger.Infof(ctx, "Resuming FAQ import from progress: %d/%d (valid entries)", processedCount, len(validEntries))
	}

	// 幂等性处理：清理可能已部分处理的chunks和索引数据
	chunksDeleted, err := s.chunkRepo.DeleteUnindexedChunks(ctx, payload.TenantID, payload.KnowledgeID)
	if err != nil {
		logger.Errorf(ctx, "Failed to delete unindexed chunks: %v", err)
		// 如果是最后一次重试，更新状态为失败
		if isLastRetry {
			if updateErr := s.updateFAQImportProgressStatus(ctx, payload.TaskID, types.FAQImportStatusFailed, 0, originalTotalEntries, 0, "清理未索引数据失败", err.Error()); updateErr != nil {
				logger.Errorf(ctx, "Failed to update task status to failed: %v", updateErr)
			}
		}
		s.cleanupFAQEntriesFileOnFinalFailure(ctx, payload.EntriesURL, retryCount, maxRetry)
		return fmt.Errorf("failed to delete unindexed chunks: %w", err)
	}
	if len(chunksDeleted) > 0 {
		logger.Infof(ctx, "Deleted unindexed chunks: %d", len(chunksDeleted))

		// 删除索引数据
		embeddingModel, err := s.modelService.GetEmbeddingModel(ctx, kb.EmbeddingModelID)
		if err == nil {
			retrieveEngine, err := retriever.NewCompositeRetrieveEngine(
				s.retrieveEngine,
				tenantInfo.GetEffectiveEngines(),
			)
			if err == nil {
				chunkIDs := make([]string, 0, len(chunksDeleted))
				for _, chunk := range chunksDeleted {
					chunkIDs = append(chunkIDs, chunk.ID)
				}
				if err := retrieveEngine.DeleteByChunkIDList(ctx, chunkIDs, embeddingModel.GetDimensions(), types.KnowledgeTypeFAQ); err != nil {
					logger.Warnf(ctx, "Failed to delete index data for chunks (may not exist): %v", err)
				} else {
					logger.Infof(ctx, "Successfully deleted index data for %d chunks", len(chunksDeleted))
				}
			}
		}
	}

	// 如果已经处理了一部分有效条目，从该位置继续
	entriesToImport := validEntries
	importMode := payload.Mode
	if processedCount > 0 && processedCount < len(validEntries) {
		entriesToImport = validEntries[processedCount:]
		// 重试场景下，如果之前已经处理了一部分数据，需要切换到 Append 模式
		// 因为 Replace 模式的删除操作在第一次运行时已经执行过了
		// 如果继续使用 Replace 模式，calculateReplaceOperations 会将之前成功导入的数据标记为删除
		// 导致数据丢失
		if payload.Mode == types.FAQBatchModeReplace {
			importMode = types.FAQBatchModeAppend
			logger.Infof(ctx, "Switching to Append mode for retry, original mode was Replace")
		}
		logger.Infof(ctx, "Continuing FAQ import from entry %d, remaining: %d entries", processedCount, len(entriesToImport))
	}

	// 构建FAQBatchUpsertPayload（使用验证通过的有效条目）
	faqPayload := &types.FAQBatchUpsertPayload{
		Entries: entriesToImport,
		Mode:    importMode,
	}

	// 执行FAQ导入（传入已处理的偏移量，用于进度计算）
	if err := s.executeFAQImport(ctx, payload.TaskID, payload.KBID, faqPayload, payload.TenantID, progress.FailedCount+processedCount, progress); err != nil {
		logger.Errorf(ctx, "FAQ import task failed: %s, error: %v", payload.TaskID, err)
		// 如果是最后一次重试，更新状态为失败
		if isLastRetry {
			if updateErr := s.updateFAQImportProgressStatus(ctx, payload.TaskID, types.FAQImportStatusFailed, 0, originalTotalEntries, len(validEntries), "导入失败", err.Error()); updateErr != nil {
				logger.Errorf(ctx, "Failed to update task status to failed: %v", updateErr)
			}
		}
		s.cleanupFAQEntriesFileOnFinalFailure(ctx, payload.EntriesURL, retryCount, maxRetry)
		return fmt.Errorf("FAQ import failed: %w", err)
	}

	// 任务成功完成
	logger.Infof(ctx, "FAQ import task completed: %s, imported: %d, failed: %d",
		payload.TaskID, len(validEntries), progress.FailedCount)

	// 最终完成处理（生成失败条目 CSV 等）
	return s.finalizeFAQValidation(ctx, &payload, progress, originalTotalEntries)
}

// finalizeFAQValidation 完成 FAQ 验证/导入任务，生成失败条目 CSV（如果有）
func (s *knowledgeService) finalizeFAQValidation(ctx context.Context, payload *types.FAQImportPayload,
	progress *types.FAQImportProgress, originalTotalEntries int) error {
	// 清理对象存储中的 entries 文件（如果有）
	if payload.EntriesURL != "" {
		if err := s.fileSvc.DeleteFile(ctx, payload.EntriesURL); err != nil {
			logger.Warnf(ctx, "Failed to delete FAQ entries file from object storage: %v", err)
		} else {
			logger.Infof(ctx, "Deleted FAQ entries file from object storage: %s", payload.EntriesURL)
		}
	}
	progress.UpdatedAt = time.Now().Unix()

	// 如果有失败条目，生成 CSV 文件
	if len(progress.FailedEntries) > 0 {
		csvURL, err := s.generateFailedEntriesCSV(ctx, payload.TenantID, payload.TaskID, progress.FailedEntries)
		if err != nil {
			logger.Warnf(ctx, "Failed to generate failed entries CSV: %v", err)
		} else {
			progress.FailedEntriesURL = csvURL
			progress.FailedEntries = nil // 清空内联数据，使用 URL
			progress.Message += " (失败记录已导出为CSV)"
		}
	}

	// 如果不是 dry run 模式，保存导入结果统计到数据库
	if !payload.DryRun {
		if err := s.saveFAQImportResultToDatabase(ctx, payload, progress, originalTotalEntries); err != nil {
			logger.Warnf(ctx, "Failed to save FAQ import result to database: %v", err)
		}

		// 只有 replace 模式才清理未使用的 Tag
		// append 模式不应删除用户预先创建的空标签
		if payload.Mode == types.FAQBatchModeReplace {
			deletedTags, err := s.tagRepo.DeleteUnusedTags(ctx, payload.TenantID, payload.KBID)
			if err != nil {
				logger.Warnf(ctx, "FAQ import task %s: failed to cleanup unused tags: %v", payload.TaskID, err)
			} else if deletedTags > 0 {
				logger.Infof(ctx, "FAQ import task %s: cleaned up %d unused tags after replace import", payload.TaskID, deletedTags)
			}
		}
	}

	// 使用 updateFAQImportProgressStatus 来确保正确清理 running key
	// 但是需要先保存其他字段，因为 updateFAQImportProgressStatus 不会保存所有字段
	if err := s.saveFAQImportProgress(ctx, progress); err != nil {
		logger.Warnf(ctx, "Failed to save final FAQ import progress: %v", err)
	}

	// 然后调用状态更新来清理 running key
	if err := s.updateFAQImportProgressStatus(ctx, payload.TaskID, types.FAQImportStatusCompleted,
		100, originalTotalEntries, originalTotalEntries, progress.Message, ""); err != nil {
		logger.Warnf(ctx, "Failed to update final FAQ import status: %v", err)
	}

	logger.Infof(ctx, "FAQ task completed: %s, dry_run=%v, success: %d, failed: %d",
		payload.TaskID, payload.DryRun, progress.SuccessCount, progress.FailedCount)

	return nil
}

const (
	kbCloneProgressKeyPrefix = "kb_clone_progress:"
	kbCloneProgressTTL       = 24 * time.Hour
)

// getKBCloneProgressKey returns the Redis key for storing KB clone progress
func getKBCloneProgressKey(taskID string) string {
	return kbCloneProgressKeyPrefix + taskID
}

const (
	faqImportProgressKeyPrefix = "faq_import_progress:"
	faqImportRunningKeyPrefix  = "faq_import_running:"
	faqImportProgressTTL       = 3 * time.Hour
)

// getFAQImportProgressKey returns the Redis key for storing FAQ import progress
func getFAQImportProgressKey(taskID string) string {
	return faqImportProgressKeyPrefix + taskID
}

// getFAQImportRunningKey returns the Redis key for storing running task ID by KB ID
func getFAQImportRunningKey(kbID string) string {
	return faqImportRunningKeyPrefix + kbID
}

// saveFAQImportProgress saves the FAQ import progress to Redis
func (s *knowledgeService) saveFAQImportProgress(ctx context.Context, progress *types.FAQImportProgress) error {
	key := getFAQImportProgressKey(progress.TaskID)
	progress.UpdatedAt = time.Now().Unix()
	data, err := json.Marshal(progress)
	if err != nil {
		return fmt.Errorf("failed to marshal FAQ import progress: %w", err)
	}
	return s.redisClient.Set(ctx, key, data, faqImportProgressTTL).Err()
}

// GetFAQImportProgress retrieves the progress of an FAQ import task
func (s *knowledgeService) GetFAQImportProgress(ctx context.Context, taskID string) (*types.FAQImportProgress, error) {
	key := getFAQImportProgressKey(taskID)
	data, err := s.redisClient.Get(ctx, key).Bytes()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil, werrors.NewNotFoundError("FAQ import task not found")
		}
		return nil, fmt.Errorf("failed to get FAQ import progress from Redis: %w", err)
	}

	var progress types.FAQImportProgress
	if err := json.Unmarshal(data, &progress); err != nil {
		return nil, fmt.Errorf("failed to unmarshal FAQ import progress: %w", err)
	}

	// If task is completed, enrich with persisted result fields from database
	if progress.Status == types.FAQImportStatusCompleted && progress.KnowledgeID != "" {
		tenantID := ctx.Value(types.TenantIDContextKey).(uint64)
		knowledge, err := s.repo.GetKnowledgeByID(ctx, tenantID, progress.KnowledgeID)
		if err == nil && knowledge != nil {
			if result, err := knowledge.GetLastFAQImportResult(); err == nil && result != nil {
				progress.SkippedCount = result.SkippedCount
				progress.ImportMode = result.ImportMode
				progress.ImportedAt = result.ImportedAt
				progress.DisplayStatus = result.DisplayStatus
				progress.ProcessingTime = result.ProcessingTime
			}
		}
	}

	return &progress, nil
}

// UpdateLastFAQImportResultDisplayStatus updates the display status of FAQ import result
func (s *knowledgeService) UpdateLastFAQImportResultDisplayStatus(ctx context.Context, kbID string, displayStatus string) error {
	// 验证displayStatus参数
	if displayStatus != "open" && displayStatus != "close" {
		return werrors.NewBadRequestError("invalid display status, must be 'open' or 'close'")
	}

	// 获取当前租户ID
	tenantID := ctx.Value(types.TenantIDContextKey).(uint64)

	// 查找FAQ类型的knowledge
	knowledgeList, err := s.repo.ListKnowledgeByKnowledgeBaseID(ctx, tenantID, kbID)
	if err != nil {
		return fmt.Errorf("failed to list knowledge: %w", err)
	}

	// 查找FAQ类型的knowledge
	var faqKnowledge *types.Knowledge
	for _, k := range knowledgeList {
		if k.Type == types.KnowledgeTypeFAQ {
			faqKnowledge = k
			break
		}
	}

	if faqKnowledge == nil {
		return werrors.NewNotFoundError("FAQ knowledge not found in this knowledge base")
	}

	// 解析当前的导入结果
	result, err := faqKnowledge.GetLastFAQImportResult()
	if err != nil {
		return fmt.Errorf("failed to parse FAQ import result: %w", err)
	}

	if result == nil {
		return werrors.NewNotFoundError("no FAQ import result found")
	}

	// 更新显示状态
	result.DisplayStatus = displayStatus

	// 保存更新后的结果
	if err := faqKnowledge.SetLastFAQImportResult(result); err != nil {
		return fmt.Errorf("failed to set FAQ import result: %w", err)
	}

	// 更新数据库
	if err := s.repo.UpdateKnowledge(ctx, faqKnowledge); err != nil {
		return fmt.Errorf("failed to update knowledge: %w", err)
	}

	return nil
}

// ProcessKBClone handles Asynq knowledge base clone tasks
func (s *knowledgeService) ProcessKBClone(ctx context.Context, t *asynq.Task) error {
	var payload types.KBClonePayload
	if err := json.Unmarshal(t.Payload(), &payload); err != nil {
		return fmt.Errorf("failed to unmarshal KB clone payload: %w", err)
	}

	// Add tenant ID to context
	ctx = context.WithValue(ctx, types.TenantIDContextKey, payload.TenantID)

	// Get tenant info and add to context
	tenantInfo, err := s.tenantRepo.GetTenantByID(ctx, payload.TenantID)
	if err != nil {
		logger.Errorf(ctx, "Failed to get tenant info: %v", err)
		return fmt.Errorf("failed to get tenant info: %w", err)
	}
	ctx = context.WithValue(ctx, types.TenantInfoContextKey, tenantInfo)

	// Check if this is the last retry
	retryCount, _ := asynq.GetRetryCount(ctx)
	maxRetry, _ := asynq.GetMaxRetry(ctx)
	isLastRetry := retryCount >= maxRetry

	logger.Infof(ctx, "Processing KB clone task: %s, source: %s, target: %s, retry: %d/%d",
		payload.TaskID, payload.SourceID, payload.TargetID, retryCount, maxRetry)

	// Helper function to handle errors - only mark as failed on last retry
	handleError := func(progress *types.KBCloneProgress, err error, message string) {
		if isLastRetry {
			progress.Status = types.KBCloneStatusFailed
			progress.Error = err.Error()
			progress.Message = message
			progress.UpdatedAt = time.Now().Unix()
			_ = s.saveKBCloneProgress(ctx, progress)
		}
	}

	// Update progress to processing
	progress := &types.KBCloneProgress{
		TaskID:    payload.TaskID,
		SourceID:  payload.SourceID,
		TargetID:  payload.TargetID,
		Status:    types.KBCloneStatusProcessing,
		Progress:  0,
		Message:   "Starting knowledge base clone...",
		UpdatedAt: time.Now().Unix(),
	}
	if err := s.saveKBCloneProgress(ctx, progress); err != nil {
		logger.Errorf(ctx, "Failed to update KB clone progress: %v", err)
	}

	// Get source and target knowledge bases
	srcKB, dstKB, err := s.kbService.CopyKnowledgeBase(ctx, payload.SourceID, payload.TargetID)
	if err != nil {
		logger.Errorf(ctx, "Failed to copy knowledge base: %v", err)
		handleError(progress, err, "Failed to copy knowledge base configuration")
		return err
	}

	// Use different sync strategies based on knowledge base type
	if srcKB.Type == types.KnowledgeBaseTypeFAQ {
		return s.cloneFAQKnowledgeBase(ctx, srcKB, dstKB, progress, handleError)
	}

	// Document type: use Knowledge-level diff based on file_hash
	addKnowledge, err := s.repo.AminusB(ctx, srcKB.TenantID, srcKB.ID, dstKB.TenantID, dstKB.ID)
	if err != nil {
		logger.Errorf(ctx, "Failed to get knowledge to add: %v", err)
		handleError(progress, err, "Failed to calculate knowledge difference")
		return err
	}

	delKnowledge, err := s.repo.AminusB(ctx, dstKB.TenantID, dstKB.ID, srcKB.TenantID, srcKB.ID)
	if err != nil {
		logger.Errorf(ctx, "Failed to get knowledge to delete: %v", err)
		handleError(progress, err, "Failed to calculate knowledge difference")
		return err
	}

	totalOperations := len(addKnowledge) + len(delKnowledge)
	progress.Total = totalOperations
	progress.Message = fmt.Sprintf("Found %d knowledge to add, %d to delete", len(addKnowledge), len(delKnowledge))
	progress.UpdatedAt = time.Now().Unix()
	_ = s.saveKBCloneProgress(ctx, progress)

	logger.Infof(ctx, "Knowledge after update to add: %d, delete: %d", len(addKnowledge), len(delKnowledge))

	processedCount := 0
	batch := 10

	// Delete knowledge in target that doesn't exist in source
	g, gctx := errgroup.WithContext(ctx)
	for ids := range slices.Chunk(delKnowledge, batch) {
		g.Go(func() error {
			err := s.DeleteKnowledgeList(gctx, ids)
			if err != nil {
				logger.Errorf(gctx, "delete partial knowledge %v: %v", ids, err)
				return err
			}
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		logger.Errorf(ctx, "delete total knowledge %d: %v", len(delKnowledge), err)
		handleError(progress, err, "Failed to delete knowledge")
		return err
	}

	processedCount += len(delKnowledge)
	if totalOperations > 0 {
		progress.Progress = processedCount * 100 / totalOperations
	}
	progress.Processed = processedCount
	progress.Message = fmt.Sprintf("Deleted %d knowledge, cloning %d...", len(delKnowledge), len(addKnowledge))
	progress.UpdatedAt = time.Now().Unix()
	_ = s.saveKBCloneProgress(ctx, progress)

	// Clone knowledge from source to target
	g, gctx = errgroup.WithContext(ctx)
	g.SetLimit(batch)
	for _, knowledge := range addKnowledge {
		g.Go(func() error {
			srcKn, err := s.repo.GetKnowledgeByID(gctx, srcKB.TenantID, knowledge)
			if err != nil {
				logger.Errorf(gctx, "get knowledge %s: %v", knowledge, err)
				return err
			}
			err = s.cloneKnowledge(gctx, srcKn, dstKB)
			if err != nil {
				logger.Errorf(gctx, "clone knowledge %s: %v", knowledge, err)
				return err
			}

			// Update progress
			processedCount++
			if totalOperations > 0 {
				progress.Progress = processedCount * 100 / totalOperations
			}
			progress.Processed = processedCount
			progress.Message = fmt.Sprintf("Cloned %d/%d knowledge", processedCount-len(delKnowledge), len(addKnowledge))
			progress.UpdatedAt = time.Now().Unix()
			_ = s.saveKBCloneProgress(ctx, progress)

			return nil
		})
	}
	if err := g.Wait(); err != nil {
		logger.Errorf(ctx, "add total knowledge %d: %v", len(addKnowledge), err)
		handleError(progress, err, "Failed to clone knowledge")
		return err
	}

	// Mark as completed
	progress.Status = types.KBCloneStatusCompleted
	progress.Progress = 100
	progress.Processed = totalOperations
	progress.Message = "Knowledge base clone completed successfully"
	progress.UpdatedAt = time.Now().Unix()
	if err := s.saveKBCloneProgress(ctx, progress); err != nil {
		logger.Errorf(ctx, "Failed to update KB clone progress to completed: %v", err)
	}

	logger.Infof(ctx, "KB clone task completed: %s", payload.TaskID)
	return nil
}

// cloneFAQKnowledgeBase handles FAQ knowledge base cloning with chunk-level incremental sync
func (s *knowledgeService) cloneFAQKnowledgeBase(
	ctx context.Context,
	srcKB, dstKB *types.KnowledgeBase,
	progress *types.KBCloneProgress,
	handleError func(*types.KBCloneProgress, error, string),
) error {
	// Get source FAQ knowledge first (FAQ KB has exactly one Knowledge entry)
	srcKnowledgeList, err := s.repo.ListKnowledgeByKnowledgeBaseID(ctx, srcKB.TenantID, srcKB.ID)
	if err != nil {
		logger.Errorf(ctx, "Failed to get source FAQ knowledge: %v", err)
		handleError(progress, err, "Failed to get source FAQ knowledge")
		return err
	}
	if len(srcKnowledgeList) == 0 {
		// Source has no FAQ knowledge, nothing to clone
		progress.Status = types.KBCloneStatusCompleted
		progress.Progress = 100
		progress.Message = "Source FAQ knowledge base is empty"
		progress.UpdatedAt = time.Now().Unix()
		_ = s.saveKBCloneProgress(ctx, progress)
		return nil
	}
	srcKnowledge := srcKnowledgeList[0]

	// Get chunk-level differences based on content_hash
	chunksToAdd, chunksToDelete, err := s.chunkRepo.FAQChunkDiff(ctx, srcKB.TenantID, srcKB.ID, dstKB.TenantID, dstKB.ID)
	if err != nil {
		logger.Errorf(ctx, "Failed to calculate FAQ chunk difference: %v", err)
		handleError(progress, err, "Failed to calculate FAQ chunk difference")
		return err
	}

	totalOperations := len(chunksToAdd) + len(chunksToDelete)
	progress.Total = totalOperations
	progress.Message = fmt.Sprintf("Found %d FAQ entries to add, %d to delete", len(chunksToAdd), len(chunksToDelete))
	progress.UpdatedAt = time.Now().Unix()
	_ = s.saveKBCloneProgress(ctx, progress)

	logger.Infof(ctx, "FAQ chunks to add: %d, delete: %d", len(chunksToAdd), len(chunksToDelete))

	// If nothing to do, mark as completed
	if totalOperations == 0 {
		progress.Status = types.KBCloneStatusCompleted
		progress.Progress = 100
		progress.Message = "FAQ knowledge base is already in sync"
		progress.UpdatedAt = time.Now().Unix()
		_ = s.saveKBCloneProgress(ctx, progress)
		return nil
	}

	// Get tenant info and initialize retrieve engine
	tenantInfo := ctx.Value(types.TenantInfoContextKey).(*types.Tenant)
	retrieveEngine, err := retriever.NewCompositeRetrieveEngine(s.retrieveEngine, tenantInfo.GetEffectiveEngines())
	if err != nil {
		logger.Errorf(ctx, "Failed to init retrieve engine: %v", err)
		handleError(progress, err, "Failed to initialize retrieve engine")
		return err
	}

	// Get embedding model
	embeddingModel, err := s.modelService.GetEmbeddingModel(ctx, dstKB.EmbeddingModelID)
	if err != nil {
		logger.Errorf(ctx, "Failed to get embedding model: %v", err)
		handleError(progress, err, "Failed to get embedding model")
		return err
	}

	processedCount := 0

	// Delete FAQ chunks that don't exist in source
	if len(chunksToDelete) > 0 {
		// Delete from vector store
		if err := retrieveEngine.DeleteByChunkIDList(ctx, chunksToDelete, embeddingModel.GetDimensions(), types.KnowledgeTypeFAQ); err != nil {
			logger.Errorf(ctx, "Failed to delete FAQ chunks from vector store: %v", err)
			handleError(progress, err, "Failed to delete FAQ entries from vector store")
			return err
		}
		// Delete from database
		if err := s.chunkRepo.DeleteChunks(ctx, dstKB.TenantID, chunksToDelete); err != nil {
			logger.Errorf(ctx, "Failed to delete FAQ chunks from database: %v", err)
			handleError(progress, err, "Failed to delete FAQ entries from database")
			return err
		}
		processedCount += len(chunksToDelete)
		if totalOperations > 0 {
			progress.Progress = processedCount * 100 / totalOperations
		}
		progress.Processed = processedCount
		progress.Message = fmt.Sprintf("Deleted %d FAQ entries, adding %d...", len(chunksToDelete), len(chunksToAdd))
		progress.UpdatedAt = time.Now().Unix()
		_ = s.saveKBCloneProgress(ctx, progress)
	}

	// Get or create the FAQ knowledge entry in destination
	dstKnowledge, err := s.getOrCreateFAQKnowledge(ctx, dstKB, srcKnowledge)
	if err != nil {
		logger.Errorf(ctx, "Failed to get or create FAQ knowledge: %v", err)
		handleError(progress, err, "Failed to prepare FAQ knowledge entry")
		return err
	}

	// Clone FAQ chunks from source to destination
	batch := 50
	tagIDMapping := map[string]string{} // srcTagID -> dstTagID
	for i := 0; i < len(chunksToAdd); i += batch {
		end := i + batch
		if end > len(chunksToAdd) {
			end = len(chunksToAdd)
		}
		batchIDs := chunksToAdd[i:end]

		// Get source chunks
		srcChunks, err := s.chunkRepo.ListChunksByID(ctx, srcKB.TenantID, batchIDs)
		if err != nil {
			logger.Errorf(ctx, "Failed to get source FAQ chunks: %v", err)
			handleError(progress, err, "Failed to get source FAQ entries")
			return err
		}

		// Create new chunks for destination
		newChunks := make([]*types.Chunk, 0, len(srcChunks))
		for _, srcChunk := range srcChunks {
			// Map TagID to target knowledge base
			targetTagID := ""
			if srcChunk.TagID != "" {
				if mappedTagID, ok := tagIDMapping[srcChunk.TagID]; ok {
					targetTagID = mappedTagID
				} else {
					// Try to find or create the tag in target knowledge base
					targetTagID = s.getOrCreateTagInTarget(ctx, srcKB.TenantID, dstKB.TenantID, dstKB.ID, srcChunk.TagID, tagIDMapping)
				}
			}

			newChunk := &types.Chunk{
				ID:              uuid.New().String(),
				TenantID:        dstKB.TenantID,
				KnowledgeID:     dstKnowledge.ID,
				KnowledgeBaseID: dstKB.ID,
				TagID:           targetTagID,
				Content:         srcChunk.Content,
				ChunkIndex:      srcChunk.ChunkIndex,
				IsEnabled:       srcChunk.IsEnabled,
				Flags:           srcChunk.Flags,
				ChunkType:       types.ChunkTypeFAQ,
				Metadata:        srcChunk.Metadata,
				ContentHash:     srcChunk.ContentHash,
				ImageInfo:       srcChunk.ImageInfo,
				Status:          int(types.ChunkStatusStored), // Initially stored, will be indexed
				CreatedAt:       time.Now(),
				UpdatedAt:       time.Now(),
			}
			newChunks = append(newChunks, newChunk)
		}

		// Save to database
		if err := s.chunkRepo.CreateChunks(ctx, newChunks); err != nil {
			logger.Errorf(ctx, "Failed to create FAQ chunks: %v", err)
			handleError(progress, err, "Failed to create FAQ entries")
			return err
		}

		// Index in vector store using existing method
		// This will index standard question + similar questions based on FAQConfig
		if err := s.indexFAQChunks(ctx, dstKB, dstKnowledge, newChunks, embeddingModel, false, false); err != nil {
			logger.Errorf(ctx, "Failed to index FAQ chunks: %v", err)
			handleError(progress, err, "Failed to index FAQ entries")
			return err
		}

		// Update chunk status to indexed
		for _, chunk := range newChunks {
			chunk.Status = int(types.ChunkStatusIndexed)
		}
		if err := s.chunkService.UpdateChunks(ctx, newChunks); err != nil {
			logger.Warnf(ctx, "Failed to update FAQ chunks status: %v", err)
			// Don't fail the whole operation for status update failure
		}

		processedCount += len(batchIDs)
		if totalOperations > 0 {
			progress.Progress = processedCount * 100 / totalOperations
		}
		progress.Processed = processedCount
		progress.Message = fmt.Sprintf("Added %d/%d FAQ entries", processedCount-len(chunksToDelete), len(chunksToAdd))
		progress.UpdatedAt = time.Now().Unix()
		_ = s.saveKBCloneProgress(ctx, progress)
	}

	// Mark as completed
	progress.Status = types.KBCloneStatusCompleted
	progress.Progress = 100
	progress.Processed = totalOperations
	progress.Message = "FAQ knowledge base clone completed successfully"
	progress.UpdatedAt = time.Now().Unix()
	if err := s.saveKBCloneProgress(ctx, progress); err != nil {
		logger.Errorf(ctx, "Failed to update KB clone progress to completed: %v", err)
	}

	return nil
}

// getOrCreateFAQKnowledge gets or creates the FAQ knowledge entry for a knowledge base
// If srcKnowledge is provided, it will copy relevant fields from source when creating new knowledge
func (s *knowledgeService) getOrCreateFAQKnowledge(ctx context.Context, kb *types.KnowledgeBase, srcKnowledge *types.Knowledge) (*types.Knowledge, error) {
	// FAQ knowledge base should have exactly one Knowledge entry
	knowledgeList, err := s.repo.ListKnowledgeByKnowledgeBaseID(ctx, kb.TenantID, kb.ID)
	if err != nil {
		return nil, err
	}

	if len(knowledgeList) > 0 {
		return knowledgeList[0], nil
	}

	// Create a new FAQ knowledge entry, copying from source if available
	knowledge := &types.Knowledge{
		ID:               uuid.New().String(),
		TenantID:         kb.TenantID,
		KnowledgeBaseID:  kb.ID,
		Type:             types.KnowledgeTypeFAQ,
		Title:            "FAQ",
		ParseStatus:      "completed",
		EnableStatus:     "enabled",
		EmbeddingModelID: kb.EmbeddingModelID,
	}

	// Copy additional fields from source knowledge if available
	if srcKnowledge != nil {
		knowledge.Title = srcKnowledge.Title
		knowledge.Description = srcKnowledge.Description
		knowledge.Source = srcKnowledge.Source
		knowledge.Metadata = srcKnowledge.Metadata
	}

	if err := s.repo.CreateKnowledge(ctx, knowledge); err != nil {
		return nil, err
	}
	return knowledge, nil
}

// saveKBCloneProgress saves the KB clone progress to Redis
func (s *knowledgeService) saveKBCloneProgress(ctx context.Context, progress *types.KBCloneProgress) error {
	key := getKBCloneProgressKey(progress.TaskID)
	data, err := json.Marshal(progress)
	if err != nil {
		return fmt.Errorf("failed to marshal progress: %w", err)
	}
	return s.redisClient.Set(ctx, key, data, kbCloneProgressTTL).Err()
}

// SaveKBCloneProgress saves the KB clone progress to Redis (public method for handler use)
func (s *knowledgeService) SaveKBCloneProgress(ctx context.Context, progress *types.KBCloneProgress) error {
	return s.saveKBCloneProgress(ctx, progress)
}

// GetKBCloneProgress retrieves the progress of a knowledge base clone task
func (s *knowledgeService) GetKBCloneProgress(ctx context.Context, taskID string) (*types.KBCloneProgress, error) {
	key := getKBCloneProgressKey(taskID)
	data, err := s.redisClient.Get(ctx, key).Bytes()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil, werrors.NewNotFoundError("KB clone task not found")
		}
		return nil, fmt.Errorf("failed to get progress from Redis: %w", err)
	}

	var progress types.KBCloneProgress
	if err := json.Unmarshal(data, &progress); err != nil {
		return nil, fmt.Errorf("failed to unmarshal progress: %w", err)
	}
	return &progress, nil
}

// getOrCreateTagInTarget finds or creates a tag in the target knowledge base based on the source tag.
// It looks up the source tag by ID, then tries to find a tag with the same name in the target KB.
// If not found, it creates a new tag with the same properties.
// The mapping is cached in tagIDMapping for subsequent lookups.
func (s *knowledgeService) getOrCreateTagInTarget(
	ctx context.Context,
	srcTenantID, dstTenantID uint64,
	dstKnowledgeBaseID string,
	srcTagID string,
	tagIDMapping map[string]string,
) string {
	// Get source tag
	srcTag, err := s.tagRepo.GetByID(ctx, srcTenantID, srcTagID)
	if err != nil || srcTag == nil {
		logger.Warnf(ctx, "Failed to get source tag %s: %v", srcTagID, err)
		tagIDMapping[srcTagID] = "" // Cache empty result to avoid repeated lookups
		return ""
	}

	// Try to find existing tag with same name in target KB
	dstTag, err := s.tagRepo.GetByName(ctx, dstTenantID, dstKnowledgeBaseID, srcTag.Name)
	if err == nil && dstTag != nil {
		tagIDMapping[srcTagID] = dstTag.ID
		return dstTag.ID
	}

	// Create new tag in target KB
	// "未分类" tag should have the lowest sort order to appear first
	sortOrder := srcTag.SortOrder
	if srcTag.Name == types.UntaggedTagName {
		sortOrder = -1
	}
	newTag := &types.KnowledgeTag{
		ID:              uuid.New().String(),
		TenantID:        dstTenantID,
		KnowledgeBaseID: dstKnowledgeBaseID,
		Name:            srcTag.Name,
		Color:           srcTag.Color,
		SortOrder:       sortOrder,
		CreatedAt:       time.Now(),
		UpdatedAt:       time.Now(),
	}
	if err := s.tagRepo.Create(ctx, newTag); err != nil {
		logger.Warnf(ctx, "Failed to create tag %s in target KB: %v", srcTag.Name, err)
		tagIDMapping[srcTagID] = "" // Cache empty result
		return ""
	}

	tagIDMapping[srcTagID] = newTag.ID
	logger.Infof(ctx, "Created tag %s (ID: %s) in target KB %s", newTag.Name, newTag.ID, dstKnowledgeBaseID)
	return newTag.ID
}

// SearchKnowledge searches knowledge items by keyword across the tenant
// fileTypes: optional list of file extensions to filter by (e.g., ["csv", "xlsx"])
func (s *knowledgeService) SearchKnowledge(ctx context.Context, keyword string, offset, limit int, fileTypes []string) ([]*types.Knowledge, bool, error) {
	tenantID, ok := ctx.Value(types.TenantIDContextKey).(uint64)
	if !ok {
		return nil, false, werrors.NewUnauthorizedError("Tenant ID not found in context")
	}
	return s.repo.SearchKnowledge(ctx, tenantID, keyword, offset, limit, fileTypes)
}

// ProcessKnowledgeListDelete handles Asynq knowledge list delete tasks
func (s *knowledgeService) ProcessKnowledgeListDelete(ctx context.Context, t *asynq.Task) error {
	var payload types.KnowledgeListDeletePayload
	if err := json.Unmarshal(t.Payload(), &payload); err != nil {
		logger.Errorf(ctx, "Failed to unmarshal knowledge list delete payload: %v", err)
		return err
	}

	logger.Infof(ctx, "Processing knowledge list delete task for %d knowledge items", len(payload.KnowledgeIDs))

	// Get tenant info
	tenant, err := s.tenantRepo.GetTenantByID(ctx, payload.TenantID)
	if err != nil {
		logger.Errorf(ctx, "Failed to get tenant %d: %v", payload.TenantID, err)
		return err
	}

	// Set context values
	ctx = context.WithValue(ctx, types.TenantIDContextKey, payload.TenantID)
	ctx = context.WithValue(ctx, types.TenantInfoContextKey, tenant)

	// Delete knowledge list
	if err := s.DeleteKnowledgeList(ctx, payload.KnowledgeIDs); err != nil {
		logger.Errorf(ctx, "Failed to delete knowledge list: %v", err)
		return err
	}

	logger.Infof(ctx, "Successfully deleted %d knowledge items", len(payload.KnowledgeIDs))
	return nil
}
