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
	"os"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/application/service/retriever"
	"github.com/Tencent/WeKnora/internal/config"
	werrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/models/utils"
	"github.com/Tencent/WeKnora/internal/tracing"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	secutils "github.com/Tencent/WeKnora/internal/utils"
	"github.com/Tencent/WeKnora/services/docreader/src/client"
	"github.com/Tencent/WeKnora/services/docreader/src/proto"
	"github.com/google/uuid"
	"github.com/hibiken/asynq"
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
	repo            interfaces.KnowledgeRepository
	kbService       interfaces.KnowledgeBaseService
	tenantRepo      interfaces.TenantRepository
	docReaderClient *client.Client
	chunkService    interfaces.ChunkService
	chunkRepo       interfaces.ChunkRepository
	fileSvc         interfaces.FileService
	modelService    interfaces.ModelService
	task            *asynq.Client
	graphEngine     interfaces.RetrieveGraphRepository
}

// NewKnowledgeService creates a new knowledge service instance
func NewKnowledgeService(
	config *config.Config,
	repo interfaces.KnowledgeRepository,
	docReaderClient *client.Client,
	kbService interfaces.KnowledgeBaseService,
	tenantRepo interfaces.TenantRepository,
	chunkService interfaces.ChunkService,
	chunkRepo interfaces.ChunkRepository,
	fileSvc interfaces.FileService,
	modelService interfaces.ModelService,
	task *asynq.Client,
	graphEngine interfaces.RetrieveGraphRepository,
) (interfaces.KnowledgeService, error) {
	return &knowledgeService{
		config:          config,
		repo:            repo,
		kbService:       kbService,
		tenantRepo:      tenantRepo,
		docReaderClient: docReaderClient,
		chunkService:    chunkService,
		chunkRepo:       chunkRepo,
		fileSvc:         fileSvc,
		modelService:    modelService,
		task:            task,
		graphEngine:     graphEngine,
	}, nil
}

// CreateKnowledgeFromFile creates a knowledge entry from an uploaded file
func (s *knowledgeService) CreateKnowledgeFromFile(ctx context.Context,
	kbID string, file *multipart.FileHeader, metadata map[string]string, enableMultimodel *bool,
) (*types.Knowledge, error) {
	logger.Info(ctx, "Start creating knowledge from file")
	logger.Infof(ctx, "Knowledge base ID: %s, file: %s", kbID, file.Filename)
	if metadata != nil {
		logger.Infof(ctx, "Received metadata: %v", metadata)
	}

	// Get knowledge base configuration
	logger.Info(ctx, "Getting knowledge base configuration")
	kb, err := s.kbService.GetKnowledgeBaseByID(ctx, kbID)
	if err != nil {
		logger.Errorf(ctx, "Failed to get knowledge base: %v", err)
		return nil, err
	}

	// 检查多模态配置完整性 - 只在图片文件时校验
	// 检查是否为图片文件
	if !IsImageType(getFileType(file.Filename)) {
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
		if kb.VLMConfig.ModelName == "" || kb.VLMConfig.BaseURL == "" {
			logger.Error(ctx, "VLM configuration incomplete for image multimodal processing")
			return nil, werrors.NewBadRequestError("上传图片文件需要完整的VLM配置信息, 请前往系统设置页面进行补全")
		}

		logger.Info(ctx, "Image multimodal configuration validation passed")
	}

	// Validate file type
	logger.Infof(ctx, "Checking file type: %s", file.Filename)
	if !isValidFileType(file.Filename) {
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
	tenantID := ctx.Value(types.TenantIDContextKey).(uint)
	logger.Infof(ctx, "Checking if file exists, tenant ID: %d", tenantID)
	exists, existingKnowledge, err := s.repo.CheckKnowledgeExists(ctx, tenantID, kbID, &types.KnowledgeCheckParams{
		Type:     "file",
		FileName: file.Filename,
		FileSize: file.Size,
		FileHash: hash,
	})
	if err != nil {
		logger.Errorf(ctx, "Failed to check knowledge existence: %v", err)
		return nil, err
	}
	if exists {
		logger.Infof(ctx, "File already exists: %s", file.Filename)
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
	safeFilename, isValid := secutils.ValidateInput(file.Filename)
	if !isValid {
		logger.Errorf(ctx, "Invalid filename: %s", file.Filename)
		return nil, werrors.NewValidationError("文件名包含非法字符")
	}

	// Create knowledge record
	logger.Info(ctx, "Creating knowledge record")
	knowledge := &types.Knowledge{
		TenantID:         tenantID,
		KnowledgeBaseID:  kbID,
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

	// Process document asynchronously
	logger.Info(ctx, "Starting asynchronous document processing")
	newCtx := logger.CloneContext(ctx)
	if enableMultimodel == nil {
		enableMultimodel = &kb.ChunkingConfig.EnableMultimodal
	}
	go s.processDocument(newCtx, kb, knowledge, file, *enableMultimodel)

	logger.Infof(ctx, "Knowledge from file created successfully, ID: %s", knowledge.ID)
	return knowledge, nil
}

// CreateKnowledgeFromURL creates a knowledge entry from a URL source
func (s *knowledgeService) CreateKnowledgeFromURL(ctx context.Context,
	kbID string, url string, enableMultimodel *bool,
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

	// Check if URL already exists in the knowledge base
	tenantID := ctx.Value(types.TenantIDContextKey).(uint)
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
		Source:           url,
		FileHash:         fileHash,
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

	// Process URL asynchronously
	logger.Info(ctx, "Starting asynchronous URL processing")
	if enableMultimodel == nil {
		enableMultimodel = &kb.ChunkingConfig.EnableMultimodal
	}
	newCtx := logger.CloneContext(ctx)
	go s.processDocumentFromURL(newCtx, kb, knowledge, url, *enableMultimodel)

	logger.Infof(ctx, "Knowledge from URL created successfully, ID: %s", knowledge.ID)
	return knowledge, nil
}

// CreateKnowledgeFromPassage creates a knowledge entry from text passages
func (s *knowledgeService) CreateKnowledgeFromPassage(ctx context.Context,
	kbID string, passage []string,
) (*types.Knowledge, error) {
	logger.Info(ctx, "Start creating knowledge from passage")
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
	logger.Info(ctx, "Creating knowledge record")
	knowledge := &types.Knowledge{
		ID:               uuid.New().String(),
		TenantID:         ctx.Value(types.TenantIDContextKey).(uint),
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

	// Process passages asynchronously
	logger.Info(ctx, "Starting asynchronous passage processing")
	go s.processDocumentFromPassage(ctx, kb, knowledge, safePassages)

	logger.Infof(ctx, "Knowledge from passage created successfully, ID: %s", knowledge.ID)
	return knowledge, nil
}

// GetKnowledgeByID retrieves a knowledge entry by its ID
func (s *knowledgeService) GetKnowledgeByID(ctx context.Context, id string) (*types.Knowledge, error) {
	logger.Info(ctx, "Start getting knowledge by ID")
	logger.Infof(ctx, "Knowledge ID: %s", id)

	tenantID := ctx.Value(types.TenantIDContextKey).(uint)
	logger.Infof(ctx, "Tenant ID: %d", tenantID)

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
	return s.repo.ListKnowledgeByKnowledgeBaseID(ctx, ctx.Value(types.TenantIDContextKey).(uint), kbID)
}

// ListPagedKnowledgeByKnowledgeBaseID returns paginated knowledge entries in a knowledge base
func (s *knowledgeService) ListPagedKnowledgeByKnowledgeBaseID(ctx context.Context,
	kbID string, page *types.Pagination,
) (*types.PageResult, error) {
	knowledges, total, err := s.repo.ListPagedKnowledgeByKnowledgeBaseID(ctx,
		ctx.Value(types.TenantIDContextKey).(uint), kbID, page)
	if err != nil {
		return nil, err
	}

	return types.NewPageResult(total, page, knowledges), nil
}

// DeleteKnowledge deletes a knowledge entry and all related resources
func (s *knowledgeService) DeleteKnowledge(ctx context.Context, id string) error {
	// Get the knowledge entry
	knowledge, err := s.repo.GetKnowledgeByID(ctx, ctx.Value(types.TenantIDContextKey).(uint), id)
	if err != nil {
		return err
	}
	wg := errgroup.Group{}
	// Delete knowledge embeddings from vector store
	wg.Go(func() error {
		tenantInfo := ctx.Value(types.TenantInfoContextKey).(*types.Tenant)
		retrieveEngine, err := retriever.NewCompositeRetrieveEngine(tenantInfo.RetrieverEngines.Engines)
		if err != nil {
			logger.GetLogger(ctx).WithField("error", err).Errorf("DeleteKnowledge delete knowledge embedding failed")
			return err
		}
		embeddingModel, err := s.modelService.GetEmbeddingModel(ctx, knowledge.EmbeddingModelID)
		if err != nil {
			logger.GetLogger(ctx).WithField("error", err).Errorf("DeleteKnowledge delete knowledge embedding failed")
			return err
		}
		if err := retrieveEngine.DeleteByKnowledgeIDList(ctx, []string{knowledge.ID}, embeddingModel.GetDimensions()); err != nil {
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
	return s.repo.DeleteKnowledge(ctx, ctx.Value(types.TenantIDContextKey).(uint), id)
}

// DeleteKnowledge deletes a knowledge entry and all related resources
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

	wg := errgroup.Group{}
	// 2. Delete knowledge embeddings from vector store
	wg.Go(func() error {
		tenantInfo := ctx.Value(types.TenantInfoContextKey).(*types.Tenant)
		retrieveEngine, err := retriever.NewCompositeRetrieveEngine(tenantInfo.RetrieverEngines.Engines)
		if err != nil {
			logger.GetLogger(ctx).WithField("error", err).Errorf("DeleteKnowledge delete knowledge embedding failed")
			return err
		}
		group := map[string][]string{}
		for _, knowledge := range knowledgeList {
			group[knowledge.EmbeddingModelID] = append(group[knowledge.EmbeddingModelID], knowledge.ID)
		}
		for embeddingModelID, knowledgeList := range group {
			embeddingModel, err := s.modelService.GetEmbeddingModel(ctx, embeddingModelID)
			if err != nil {
				logger.GetLogger(ctx).WithField("error", err).Errorf("DeleteKnowledge get embedding model failed")
				return err
			}
			if err := retrieveEngine.DeleteByKnowledgeIDList(ctx, knowledgeList, embeddingModel.GetDimensions()); err != nil {
				logger.GetLogger(ctx).WithField("error", err).Errorf("DeleteKnowledge delete knowledge embedding failed")
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
			namespaces = append(namespaces, types.NameSpace{KnowledgeBase: knowledge.KnowledgeBaseID, Knowledge: knowledge.ID})
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

func (s *knowledgeService) cloneKnowledge(ctx context.Context, src *types.Knowledge, targetKB *types.KnowledgeBase) (err error) {
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

// processDocument handles asynchronous processing of document files
func (s *knowledgeService) processDocument(ctx context.Context,
	kb *types.KnowledgeBase, knowledge *types.Knowledge, file *multipart.FileHeader, enableMultimodel bool,
) {
	logger.GetLogger(ctx).Infof("processDocument enableMultimodel: %v", enableMultimodel)

	ctx, span := tracing.ContextWithSpan(ctx, "knowledgeService.processDocument")
	defer span.End()
	span.SetAttributes(
		attribute.String("request_id", ctx.Value(types.RequestIDContextKey).(string)),
		attribute.String("knowledge_base_id", kb.ID),
		attribute.Int("tenant_id", int(kb.TenantID)),
		attribute.String("knowledge_id", knowledge.ID),
		attribute.String("file_name", knowledge.FileName),
		attribute.String("file_type", knowledge.FileType),
		attribute.String("file_path", knowledge.FilePath),
		attribute.Int64("file_size", knowledge.FileSize),
		attribute.String("embedding_model", knowledge.EmbeddingModelID),
		attribute.Bool("enable_multimodal", enableMultimodel),
	)
	logger.GetLogger(ctx).Infof("processDocument trace id: %s", span.SpanContext().TraceID().String())

	if !enableMultimodel && IsImageType(knowledge.FileType) {
		logger.GetLogger(ctx).WithField("knowledge_id", knowledge.ID).
			WithField("error", ErrImageNotParse).Errorf("processDocument image without enable multimodel")
		knowledge.ParseStatus = "failed"
		knowledge.ErrorMessage = ErrImageNotParse.Error()
		s.repo.UpdateKnowledge(ctx, knowledge)
		span.RecordError(ErrImageNotParse)
		return
	}

	// Update status to processing
	knowledge.ParseStatus = "processing"
	knowledge.UpdatedAt = time.Now()
	if err := s.repo.UpdateKnowledge(ctx, knowledge); err != nil {
		span.RecordError(err)
		return
	}

	// Read and chunk the document
	f, err := file.Open()
	if err != nil {
		logger.GetLogger(ctx).WithField("knowledge_id", knowledge.ID).
			WithField("error", err).Errorf("processDocument open file failed")
		knowledge.ParseStatus = "failed"
		knowledge.ErrorMessage = err.Error()
		knowledge.UpdatedAt = time.Now()
		s.repo.UpdateKnowledge(ctx, knowledge)
		span.RecordError(err)
		return
	}
	defer f.Close()

	span.AddEvent("start read file")
	contentBytes, err := io.ReadAll(f)
	if err != nil {
		knowledge.ParseStatus = "failed"
		knowledge.ErrorMessage = err.Error()
		knowledge.UpdatedAt = time.Now()
		s.repo.UpdateKnowledge(ctx, knowledge)
		span.RecordError(err)
		return
	}

	// Split file into chunks using document reader service
	span.AddEvent("start split file")
	resp, err := s.docReaderClient.ReadFromFile(ctx, &proto.ReadFromFileRequest{
		FileContent: contentBytes,
		FileName:    knowledge.FileName,
		FileType:    knowledge.FileType,
		ReadConfig: &proto.ReadConfig{
			ChunkSize:        int32(kb.ChunkingConfig.ChunkSize),
			ChunkOverlap:     int32(kb.ChunkingConfig.ChunkOverlap),
			Separators:       kb.ChunkingConfig.Separators,
			EnableMultimodal: enableMultimodel,
			StorageConfig: &proto.StorageConfig{
				Provider:        proto.StorageProvider(proto.StorageProvider_value[strings.ToUpper(kb.StorageConfig.Provider)]),
				Region:          kb.StorageConfig.Region,
				BucketName:      kb.StorageConfig.BucketName,
				AccessKeyId:     kb.StorageConfig.SecretID,
				SecretAccessKey: kb.StorageConfig.SecretKey,
				AppId:           kb.StorageConfig.AppID,
				PathPrefix:      kb.StorageConfig.PathPrefix,
			},
			VlmConfig: &proto.VLMConfig{
				ModelName:     kb.VLMConfig.ModelName,
				BaseUrl:       kb.VLMConfig.BaseURL,
				ApiKey:        kb.VLMConfig.APIKey,
				InterfaceType: kb.VLMConfig.InterfaceType,
			},
		},
		RequestId: ctx.Value(types.RequestIDContextKey).(string),
	})
	if err != nil {
		logger.GetLogger(ctx).WithField("knowledge_id", knowledge.ID).
			WithField("error", err).Errorf("processDocument read file failed")
		knowledge.ParseStatus = "failed"
		knowledge.ErrorMessage = err.Error()
		knowledge.UpdatedAt = time.Now()
		s.repo.UpdateKnowledge(ctx, knowledge)
		span.RecordError(err)
		return
	}

	// Process and store chunks
	span.AddEvent("start process chunks")
	s.processChunks(ctx, kb, knowledge, resp.Chunks)
}

// processDocumentFromURL handles asynchronous processing of URL content
func (s *knowledgeService) processDocumentFromURL(ctx context.Context,
	kb *types.KnowledgeBase, knowledge *types.Knowledge, url string, enableMultimodel bool,
) {
	// Update status to processing
	knowledge.ParseStatus = "processing"
	knowledge.UpdatedAt = time.Now()
	if err := s.repo.UpdateKnowledge(ctx, knowledge); err != nil {
		return
	}
	logger.GetLogger(ctx).Infof("processDocumentFromURL enableMultimodel: %v", enableMultimodel)

	// Fetch and chunk content from URL
	resp, err := s.docReaderClient.ReadFromURL(ctx, &proto.ReadFromURLRequest{
		Url:   url,
		Title: knowledge.Title,
		ReadConfig: &proto.ReadConfig{
			ChunkSize:        int32(kb.ChunkingConfig.ChunkSize),
			ChunkOverlap:     int32(kb.ChunkingConfig.ChunkOverlap),
			Separators:       kb.ChunkingConfig.Separators,
			EnableMultimodal: enableMultimodel,
			StorageConfig: &proto.StorageConfig{
				Provider:        proto.StorageProvider(proto.StorageProvider_value[strings.ToUpper(kb.StorageConfig.Provider)]),
				Region:          kb.StorageConfig.Region,
				BucketName:      kb.StorageConfig.BucketName,
				AccessKeyId:     kb.StorageConfig.SecretID,
				SecretAccessKey: kb.StorageConfig.SecretKey,
				AppId:           kb.StorageConfig.AppID,
				PathPrefix:      kb.StorageConfig.PathPrefix,
			},
			VlmConfig: &proto.VLMConfig{
				ModelName:     kb.VLMConfig.ModelName,
				BaseUrl:       kb.VLMConfig.BaseURL,
				ApiKey:        kb.VLMConfig.APIKey,
				InterfaceType: kb.VLMConfig.InterfaceType,
			},
		},
		RequestId: ctx.Value(types.RequestIDContextKey).(string),
	})
	if err != nil {
		knowledge.ParseStatus = "failed"
		knowledge.ErrorMessage = err.Error()
		knowledge.UpdatedAt = time.Now()
		s.repo.UpdateKnowledge(ctx, knowledge)
		return
	}

	// Process and store chunks
	s.processChunks(ctx, kb, knowledge, resp.Chunks)
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

// processChunks processes chunks and creates embeddings for knowledge content
func (s *knowledgeService) processChunks(ctx context.Context,
	kb *types.KnowledgeBase, knowledge *types.Knowledge, chunks []*proto.Chunk,
) {
	ctx, span := tracing.ContextWithSpan(ctx, "knowledgeService.processChunks")
	defer span.End()
	span.SetAttributes(
		attribute.Int("tenant_id", int(knowledge.TenantID)),
		attribute.String("knowledge_base_id", knowledge.KnowledgeBaseID),
		attribute.String("knowledge_id", knowledge.ID),
		attribute.String("embedding_model_id", kb.EmbeddingModelID),
		attribute.Int("chunk_count", len(chunks)),
	)

	// Get embedding model for vectorization
	embeddingModel, err := s.modelService.GetEmbeddingModel(ctx, kb.EmbeddingModelID)
	if err != nil {
		logger.GetLogger(ctx).WithField("error", err).Errorf("processChunks get embedding model failed")
		span.RecordError(err)
		return
	}

	// Generate document summary - 只使用文本类型的 Chunk
	chatModel, err := s.modelService.GetChatModel(ctx, kb.SummaryModelID)
	if err != nil {
		logger.GetLogger(ctx).WithField("error", err).Errorf("processChunks get summary model failed")
		span.RecordError(err)
		return
	}

	enableGraphRAG := os.Getenv("ENABLE_GRAPH_RAG") == "true"

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
	if enableGraphRAG {
		relationChunkSize := 5
		indirectRelationChunkSize := 5
		graphBuilder := NewGraphBuilder(s.config, chatModel)
		err = graphBuilder.BuildGraph(ctx, textChunks)
		if err != nil {
			logger.GetLogger(ctx).WithField("error", err).Errorf("processChunks build graph failed")
			span.RecordError(err)
		} else {
			for _, chunk := range textChunks {
				chunk.RelationChunks, _ = json.Marshal(graphBuilder.GetRelationChunks(chunk.ID, relationChunkSize))
				chunk.IndirectRelationChunks, _ = json.Marshal(graphBuilder.GetIndirectRelationChunks(chunk.ID, indirectRelationChunkSize))
			}
			for i, entity := range graphBuilder.GetAllEntities() {
				relationChunks, _ := json.Marshal(entity.ChunkIDs)
				entityChunk := &types.Chunk{
					ID:              entity.ID,
					TenantID:        knowledge.TenantID,
					KnowledgeID:     knowledge.ID,
					KnowledgeBaseID: knowledge.KnowledgeBaseID,
					Content:         entity.Description,
					ChunkIndex:      maxSeq + i*100 + 3,
					IsEnabled:       true,
					CreatedAt:       time.Now(),
					UpdatedAt:       time.Now(),
					ChunkType:       types.ChunkTypeEntity,
					RelationChunks:  types.JSON(relationChunks),
				}
				insertChunks = append(insertChunks, entityChunk)
			}
			for i, relationship := range graphBuilder.GetAllRelationships() {
				relationChunks, _ := json.Marshal(relationship.ChunkIDs)
				relationshipChunk := &types.Chunk{
					ID:              relationship.ID,
					TenantID:        knowledge.TenantID,
					KnowledgeID:     knowledge.ID,
					KnowledgeBaseID: knowledge.KnowledgeBaseID,
					Content:         relationship.Description,
					ChunkIndex:      maxSeq + i*100 + 4,
					IsEnabled:       true,
					CreatedAt:       time.Now(),
					UpdatedAt:       time.Now(),
					ChunkType:       types.ChunkTypeRelationship,
					RelationChunks:  types.JSON(relationChunks),
				}
				insertChunks = append(insertChunks, relationshipChunk)
			}
		}
	}

	span.AddEvent("extract summary")
	summary, err := s.getSummary(ctx, chatModel, knowledge, textChunks)
	if err != nil {
		logger.GetLogger(ctx).WithField("knowledge_id", knowledge.ID).
			WithField("error", err).Errorf("processChunks get summary failed, use first chunk as description")
		if len(textChunks) > 0 {
			knowledge.Description = textChunks[0].Content
		}
	} else {
		knowledge.Description = summary
	}
	span.SetAttributes(attribute.String("summary", knowledge.Description))

	// 批量索引
	if strings.TrimSpace(knowledge.Description) != "" && len(textChunks) > 0 {
		sChunk := &types.Chunk{
			ID:              uuid.New().String(),
			TenantID:        knowledge.TenantID,
			KnowledgeID:     knowledge.ID,
			KnowledgeBaseID: knowledge.KnowledgeBaseID,
			Content:         fmt.Sprintf("# 文档名称\n%s\n\n# 摘要\n%s", knowledge.FileName, knowledge.Description),
			ChunkIndex:      maxSeq + 3, // 使用不冲突的索引方式
			IsEnabled:       true,
			CreatedAt:       time.Now(),
			UpdatedAt:       time.Now(),
			StartAt:         0,
			EndAt:           0,
			ChunkType:       types.ChunkTypeSummary,
			ParentChunkID:   textChunks[0].ID,
		}
		logger.GetLogger(ctx).Infof("Created summary chunk for %s with index %d",
			sChunk.ParentChunkID, sChunk.ChunkIndex)
		insertChunks = append(insertChunks, sChunk)
	}

	// Create index information for each chunk
	indexInfoList := utils.MapSlice(insertChunks, func(chunk *types.Chunk) *types.IndexInfo {
		return &types.IndexInfo{
			Content:         chunk.Content,
			SourceID:        chunk.ID,
			SourceType:      types.ChunkSourceType,
			ChunkID:         chunk.ID,
			KnowledgeID:     knowledge.ID,
			KnowledgeBaseID: knowledge.KnowledgeBaseID,
		}
	})

	// Initialize retrieval engine
	tenantInfo := ctx.Value(types.TenantInfoContextKey).(*types.Tenant)
	retrieveEngine, err := retriever.NewCompositeRetrieveEngine(tenantInfo.RetrieverEngines.Engines)
	if err != nil {
		knowledge.ParseStatus = "failed"
		knowledge.ErrorMessage = err.Error()
		knowledge.UpdatedAt = time.Now()
		s.repo.UpdateKnowledge(ctx, knowledge)
		span.RecordError(err)
		return
	}

	// Calculate storage size required for embeddings
	span.AddEvent("estimate storage size")
	totalStorageSize := retrieveEngine.EstimateStorageSize(ctx, embeddingModel, indexInfoList)
	if tenantInfo.StorageQuota > 0 {
		// Re-fetch tenant storage information
		tenantInfo, err = s.tenantRepo.GetTenantByID(ctx, tenantInfo.ID)
		if err != nil {
			knowledge.ParseStatus = "failed"
			knowledge.ErrorMessage = err.Error()
			knowledge.UpdatedAt = time.Now()
			s.repo.UpdateKnowledge(ctx, knowledge)
			span.RecordError(err)
			return
		}
		// Check if there's enough storage quota available
		if tenantInfo.StorageUsed+totalStorageSize > tenantInfo.StorageQuota {
			knowledge.ParseStatus = "failed"
			knowledge.ErrorMessage = "存储空间不足"
			knowledge.UpdatedAt = time.Now()
			s.repo.UpdateKnowledge(ctx, knowledge)
			span.RecordError(errors.New("storage quota exceeded"))
			return
		}
	}

	// Save chunks to database
	span.AddEvent("create chunks")
	if err := s.chunkService.CreateChunks(ctx, insertChunks); err != nil {
		knowledge.ParseStatus = "failed"
		knowledge.ErrorMessage = err.Error()
		knowledge.UpdatedAt = time.Now()
		s.repo.UpdateKnowledge(ctx, knowledge)
		span.RecordError(err)
		return
	}

	span.AddEvent("batch index")
	err = retrieveEngine.BatchIndex(ctx, embeddingModel, indexInfoList)
	if err != nil {
		knowledge.ParseStatus = "failed"
		knowledge.ErrorMessage = err.Error()
		knowledge.UpdatedAt = time.Now()
		s.repo.UpdateKnowledge(ctx, knowledge)

		// delete failed chunks
		if err := s.chunkService.DeleteChunksByKnowledgeID(ctx, knowledge.ID); err != nil {
			logger.Errorf(ctx, "Delete chunks failed: %v", err)
		}

		// delete index
		if err := retrieveEngine.DeleteByKnowledgeIDList(
			ctx, []string{knowledge.ID}, embeddingModel.GetDimensions(),
		); err != nil {
			logger.Errorf(ctx, "Delete index failed: %v", err)
		}
		span.RecordError(err)
		return
	}
	logger.GetLogger(ctx).Infof("processChunks batch index successfully, with %d index", len(indexInfoList))

	logger.Infof(ctx, "processChunks create relationship rag task")
	for _, chunk := range textChunks {
		err := NewChunkExtractTask(ctx, s.task, chunk.TenantID, chunk.ID, kb.SummaryModelID)
		if err != nil {
			logger.GetLogger(ctx).WithField("error", err).Errorf("processChunks create chunk extract task failed")
			span.RecordError(err)
		}
	}

	// Update knowledge status to completed
	knowledge.ParseStatus = "completed"
	knowledge.EnableStatus = "enabled"
	knowledge.StorageSize = totalStorageSize
	now := time.Now()
	knowledge.ProcessedAt = &now
	knowledge.UpdatedAt = now
	if err := s.repo.UpdateKnowledge(ctx, knowledge); err != nil {
		logger.GetLogger(ctx).WithField("error", err).Errorf("processChunks update knowledge failed")
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
		chunkContents = string([]rune(chunkContents)[:chunk.StartAt]) + chunk.Content
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

// GetKnowledgeFile retrieves the physical file associated with a knowledge entry
func (s *knowledgeService) GetKnowledgeFile(ctx context.Context, id string) (io.ReadCloser, string, error) {
	// Get knowledge record
	knowledge, err := s.repo.GetKnowledgeByID(ctx, ctx.Value(types.TenantIDContextKey).(uint), id)
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
	record, err := s.repo.GetKnowledgeByID(ctx, ctx.Value(types.TenantIDContextKey).(uint), knowledge.ID)
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

// isValidFileType checks if a file type is supported
func isValidFileType(filename string) bool {
	switch strings.ToLower(getFileType(filename)) {
	case "pdf", "txt", "docx", "doc", "md", "markdown", "png", "jpg", "jpeg", "gif":
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
	tenantID uint, ids []string,
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
		ids = ids
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
		knowledge = knowledge
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
	retrieveEngine, err := retriever.NewCompositeRetrieveEngine(tenantInfo.RetrieverEngines.Engines)
	if err != nil {
		return err
	}

	// Delete old vector representation of the chunk
	err = retrieveEngine.DeleteByChunkIDList(ctx, ids, embeddingModel.GetDimensions())
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

func (s *knowledgeService) UpdateImageInfo(ctx context.Context, knowledgeID string, chunkID string, imageInfo string) error {
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
	chunk, err := s.chunkService.GetChunkByID(ctx, knowledgeID, chunkID)
	if err != nil {
		logger.Errorf(ctx, "Failed to get chunk: %v", err)
		return err
	}
	chunk.ImageInfo = imageInfo
	tenantID := ctx.Value(types.TenantIDContextKey).(uint)
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
		if child.ChunkType == types.ChunkTypeImageCaption {
			hasCaptionChunk = true
			// Update caption if it has changed
			if image.Caption != cImageInfo[0].Caption {
				child.Content = image.Caption
				child.ImageInfo = imageInfo
				updateChunk = append(updateChunk, chunkChildren[i])
			}
		} else if child.ChunkType == types.ChunkTypeImageOCR {
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
		)
		chunkPage++
		if err != nil {
			return err
		}
		if len(sourceChunks) == 0 {
			break
		}
		for _, sourceChunk := range sourceChunks {
			targetChunk := &types.Chunk{
				ID:              uuid.New().String(),
				TenantID:        dst.TenantID,
				KnowledgeID:     dst.ID,
				KnowledgeBaseID: dst.KnowledgeBaseID,
				Content:         sourceChunk.Content,
				ChunkIndex:      sourceChunk.ChunkIndex,
				IsEnabled:       sourceChunk.IsEnabled,
				StartAt:         sourceChunk.StartAt,
				EndAt:           sourceChunk.EndAt,
				PreChunkID:      sourceChunk.PreChunkID,
				NextChunkID:     sourceChunk.NextChunkID,
				ChunkType:       sourceChunk.ChunkType,
				ParentChunkID:   sourceChunk.ParentChunkID,
				ImageInfo:       sourceChunk.ImageInfo,
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
	retrieveEngine, err := retriever.NewCompositeRetrieveEngine(tenantInfo.RetrieverEngines.Engines)
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
	); err != nil {
		return err
	}
	return nil
}

func IsImageType(fileType string) bool {
	switch fileType {
	case "jpg", "jpeg", "png", "gif", "webp", "bmp", "svg", "tiff":
		return true
	default:
		return false
	}
}
