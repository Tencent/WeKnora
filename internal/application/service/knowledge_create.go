package service

import (
	"context"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/url"
	"os"
	"path"
	"slices"
	"strings"
	"time"

	werrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/infrastructure/chunker"
	"github.com/Tencent/WeKnora/internal/infrastructure/docparser"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/tracing/langfuse"
	"github.com/Tencent/WeKnora/internal/types"
	secutils "github.com/Tencent/WeKnora/internal/utils"
	"github.com/google/uuid"
	"github.com/hibiken/asynq"
)

// CreateKnowledgeFromFile creates a knowledge entry from an uploaded file
func (s *knowledgeService) CreateKnowledgeFromFile(ctx context.Context,
	kbID string, file *multipart.FileHeader, metadata map[string]string, enableMultimodel *bool, customFileName string, tagID string, channel string,
) (*types.Knowledge, error) {
	logger.Info(ctx, "Start creating knowledge from file")

	// Use custom filename if provided, otherwise use original filename
	fileName := file.Filename
	if customFileName != "" {
		fileName = customFileName
		logger.Infof(ctx, "Using custom filename: %s (original: %s)", customFileName, file.Filename)
	}

	logger.Infof(ctx, "Knowledge base ID: %s, file: %s", kbID, fileName)

	if IsVideoType(getFileType(fileName)) {
		logger.Error(ctx, "Video file upload is not supported")
		return nil, werrors.NewBadRequestError("暂不支持上传视频文件")
	}

	// Get knowledge base configuration
	logger.Info(ctx, "Getting knowledge base configuration")
	kb, err := s.kbService.GetKnowledgeBaseByID(ctx, kbID)
	if err != nil {
		logger.Errorf(ctx, "Failed to get knowledge base: %v", err)
		return nil, err
	}

	// FAQ knowledge bases should not accept file uploads — use the FAQ import API instead
	if kb.Type == types.KnowledgeBaseTypeFAQ {
		return nil, werrors.NewBadRequestError("FAQ 知识库不支持文件上传，请使用 FAQ 导入功能")
	}

	if err := checkStorageEngineConfigured(ctx, kb); err != nil {
		return nil, err
	}

	// 检查多模态配置完整性 - 只在图片文件时校验
	if !IsImageType(getFileType(fileName)) {
		logger.Info(ctx, "Non-image file with multimodal enabled, skipping COS/VLM validation")
	} else {
		// 解析有效 provider：优先 KB 级别（新字段 > 旧字段），其次租户默认
		provider := kb.GetStorageProvider()
		tenant, _ := ctx.Value(types.TenantInfoContextKey).(*types.Tenant)
		if provider == "" && tenant != nil && tenant.StorageEngineConfig != nil {
			provider = strings.ToLower(strings.TrimSpace(tenant.StorageEngineConfig.DefaultProvider))
		}

		// 根据 provider 校验租户级存储引擎配置
		switch provider {
		case "cos":
			if tenant == nil || tenant.StorageEngineConfig == nil || tenant.StorageEngineConfig.COS == nil ||
				tenant.StorageEngineConfig.COS.SecretID == "" || tenant.StorageEngineConfig.COS.SecretKey == "" ||
				tenant.StorageEngineConfig.COS.Region == "" || tenant.StorageEngineConfig.COS.BucketName == "" {
				logger.Error(ctx, "COS configuration incomplete for image multimodal processing")
				return nil, werrors.NewBadRequestError("上传图片文件需要完整的对象存储配置信息, 请前往知识库存储设置或系统设置页面进行补全")
			}
		case "minio":
			ok := false
			if tenant != nil && tenant.StorageEngineConfig != nil && tenant.StorageEngineConfig.MinIO != nil {
				m := tenant.StorageEngineConfig.MinIO
				if m.Mode == "remote" {
					ok = m.Endpoint != "" && m.AccessKeyID != "" && m.SecretAccessKey != "" && m.BucketName != ""
				} else {
					ok = os.Getenv("MINIO_ENDPOINT") != "" && os.Getenv("MINIO_ACCESS_KEY_ID") != "" &&
						os.Getenv("MINIO_SECRET_ACCESS_KEY") != "" &&
						(m.BucketName != "" || os.Getenv("MINIO_BUCKET_NAME") != "")
				}
			}
			if !ok {
				logger.Error(ctx, "MinIO configuration incomplete for image multimodal processing")
				return nil, werrors.NewBadRequestError("上传图片文件需要完整的对象存储配置信息, 请前往知识库存储设置或系统设置页面进行补全")
			}
		}

		// 检查VLM配置
		if !kb.VLMConfig.Enabled || kb.VLMConfig.ModelID == "" {
			logger.Error(ctx, "VLM model is not configured")
			return nil, werrors.NewBadRequestError("上传图片文件需要设置VLM模型")
		}

		logger.Info(ctx, "Image multimodal configuration validation passed")
	}

	// 检查音频ASR配置完整性 - 只在音频文件时校验
	if IsAudioType(getFileType(fileName)) {
		if !kb.ASRConfig.IsASREnabled() {
			logger.Error(ctx, "ASR model is not configured")
			return nil, werrors.NewBadRequestError("上传音频文件需要设置ASR语音识别模型")
		}
		logger.Info(ctx, "Audio ASR configuration validation passed")
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
		Channel:          defaultChannel(channel),
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
	// Save the file to storage (use KB-level storage engine if configured)
	logger.Infof(ctx, "Saving file, knowledge ID: %s", knowledge.ID)
	filePath, err := s.resolveFileService(ctx, kb).SaveFile(ctx, file, knowledge.TenantID, knowledge.ID)
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

	lang, _ := types.LanguageFromContext(ctx)
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
		Language:                 lang,
	}

	langfuse.InjectTracing(ctx, &taskPayload)
	payloadBytes, err := json.Marshal(taskPayload)
	if err != nil {
		logger.Errorf(ctx, "Failed to marshal document process task payload: %v", err)
		// 即使入队失败，也返回knowledge，因为文件已保存
		return knowledge, nil
	}

	task := asynq.NewTask(types.TypeDocumentProcess, payloadBytes, asynq.Queue("default"), asynq.MaxRetry(3))
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
// isFileURL reports whether the given URL should be treated as a direct file download.
// Priority: URL path has a known file extension first, then fall back to user-provided fileName/fileType hints.
func isFileURL(rawURL, fileName, fileType string) bool {
	u, err := url.Parse(rawURL)
	if err == nil {
		ext := strings.ToLower(strings.TrimPrefix(path.Ext(u.Path), "."))
		if ext != "" && allowedFileURLExtensions[ext] {
			return true
		}
	}
	// Fall back to user-provided hints
	return fileName != "" || fileType != ""
}

func (s *knowledgeService) CreateKnowledgeFromURL(ctx context.Context,
	kbID string, rawURL string, fileName string, fileType string, enableMultimodel *bool, title string, tagID string, channel string,
) (*types.Knowledge, error) {
	logger.Info(ctx, "Start creating knowledge from URL")
	logger.Infof(ctx, "Knowledge base ID: %s, URL: %s", kbID, rawURL)

	// Route to file_url logic when the URL points to a downloadable file
	if isFileURL(rawURL, fileName, fileType) {
		return s.createKnowledgeFromFileURL(ctx, kbID, rawURL, fileName, fileType, enableMultimodel, title, tagID, channel)
	}

	url := rawURL

	// Get knowledge base configuration
	logger.Info(ctx, "Getting knowledge base configuration")
	kb, err := s.kbService.GetKnowledgeBaseByID(ctx, kbID)
	if err != nil {
		logger.Errorf(ctx, "Failed to get knowledge base: %v", err)
		return nil, err
	}

	if err := checkStorageEngineConfigured(ctx, kb); err != nil {
		return nil, err
	}

	// Validate URL format and security
	logger.Info(ctx, "Validating URL")
	if !isValidURL(url) || !secutils.IsValidURL(url) {
		logger.Error(ctx, "Invalid or unsafe URL format")
		return nil, ErrInvalidURL
	}

	// SSRF protection: validate URL is safe to fetch (uses centralised entry-point with whitelist support)
	if err := secutils.ValidateURLForSSRF(url); err != nil {
		logger.Errorf(ctx, "URL rejected for SSRF protection: %s, err: %v", url, err)
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
		Channel:          defaultChannel(channel),
		Title:            title,
		Source:           url,
		FileType:         "html",
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

	lang, _ := types.LanguageFromContext(ctx)
	taskPayload := types.DocumentProcessPayload{
		TenantID:                 tenantID,
		KnowledgeID:              knowledge.ID,
		KnowledgeBaseID:          kbID,
		URL:                      url,
		EnableMultimodel:         enableMultimodelValue,
		EnableQuestionGeneration: enableQuestionGeneration,
		QuestionCount:            questionCount,
		Language:                 lang,
	}

	langfuse.InjectTracing(ctx, &taskPayload)
	payloadBytes, err := json.Marshal(taskPayload)
	if err != nil {
		logger.Errorf(ctx, "Failed to marshal URL process task payload: %v", err)
		return knowledge, nil
	}

	task := asynq.NewTask(types.TypeDocumentProcess, payloadBytes, asynq.Queue("default"), asynq.MaxRetry(3))
	info, err := s.task.Enqueue(task)
	if err != nil {
		logger.Errorf(ctx, "Failed to enqueue URL process task: %v", err)
		return knowledge, nil
	}
	logger.Infof(ctx, "Enqueued URL process task: id=%s queue=%s knowledge_id=%s", info.ID, info.Queue, knowledge.ID)

	logger.Infof(ctx, "Knowledge from URL created successfully, ID: %s", knowledge.ID)
	return knowledge, nil
}

// allowedFileURLExtensions defines the supported file extensions for file URL import
var allowedFileURLExtensions = map[string]bool{
	"txt":  true,
	"md":   true,
	"pdf":  true,
	"docx": true,
	"doc":  true,
	"mp3":  true,
	"wav":  true,
	"m4a":  true,
	"flac": true,
	"ogg":  true,
}

// maxFileURLSize is the maximum allowed file size for file URL import (10MB)
const maxFileURLSize = 10 * 1024 * 1024

// extractFileNameFromURL extracts the filename from a URL path
func extractFileNameFromURL(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	base := path.Base(u.Path)
	if base == "." || base == "/" {
		return ""
	}
	return base
}

// extractFileNameFromContentDisposition extracts filename from Content-Disposition header
func extractFileNameFromContentDisposition(header string) string {
	// e.g. attachment; filename="document.pdf" or filename*=UTF-8''document.pdf
	for _, part := range strings.Split(header, ";") {
		part = strings.TrimSpace(part)
		if strings.HasPrefix(strings.ToLower(part), "filename=") {
			name := strings.TrimPrefix(part, "filename=")
			name = strings.TrimPrefix(part[len("filename="):], "")
			name = strings.Trim(name, `"'`)
			if name != "" {
				return name
			}
		}
	}
	return ""
}

// createKnowledgeFromFileURL is the internal implementation for file URL knowledge creation.
// Called by CreateKnowledgeFromURL when the URL is detected as a direct file download.
func (s *knowledgeService) createKnowledgeFromFileURL(
	ctx context.Context,
	kbID string,
	fileURL string,
	fileName string,
	fileType string,
	enableMultimodel *bool,
	title string,
	tagID string,
	channel string,
) (*types.Knowledge, error) {
	logger.Info(ctx, "Start creating knowledge from file URL")
	logger.Infof(ctx, "Knowledge base ID: %s, file URL: %s", kbID, fileURL)

	// Get knowledge base configuration
	kb, err := s.kbService.GetKnowledgeBaseByID(ctx, kbID)
	if err != nil {
		logger.Errorf(ctx, "Failed to get knowledge base: %v", err)
		return nil, err
	}

	if err := checkStorageEngineConfigured(ctx, kb); err != nil {
		return nil, err
	}

	// Validate URL format and security (static check only, no HEAD request)
	if !isValidURL(fileURL) || !secutils.IsValidURL(fileURL) {
		logger.Error(ctx, "Invalid or unsafe file URL format")
		return nil, ErrInvalidURL
	}
	if err := secutils.ValidateURLForSSRF(fileURL); err != nil {
		logger.Errorf(ctx, "File URL rejected for SSRF protection: %s, err: %v", fileURL, err)
		return nil, ErrInvalidURL
	}

	// Resolve fileName: user-provided > extracted from URL path
	if fileName == "" {
		fileName = extractFileNameFromURL(fileURL)
	}

	// Resolve fileType: user-provided > inferred from fileName
	if fileType == "" && fileName != "" {
		fileType = getFileType(fileName)
	}

	// Validate file extension against whitelist (if we can determine it)
	if fileType != "" {
		if !allowedFileURLExtensions[strings.ToLower(fileType)] {
			logger.Errorf(ctx, "Unsupported file type for file URL import: %s", fileType)
			return nil, werrors.NewBadRequestError(fmt.Sprintf("不支持的文件类型: %s，仅支持 txt, md, pdf, docx, doc", fileType))
		}
	}

	// Use title as display name if fileName is still empty
	displayName := fileName
	if displayName == "" {
		displayName = title
	}
	if displayName == "" {
		// Fallback: use last segment of URL
		displayName = extractFileNameFromURL(fileURL)
	}
	if displayName == "" {
		displayName = fileURL
	}

	// Check for duplicate (by URL hash)
	tenantID := ctx.Value(types.TenantIDContextKey).(uint64)
	fileHash := calculateStr(fileURL)
	exists, existingKnowledge, err := s.repo.CheckKnowledgeExists(ctx, tenantID, kbID, &types.KnowledgeCheckParams{
		Type:     "file_url",
		URL:      fileURL,
		FileHash: fileHash,
	})
	if err != nil {
		logger.Errorf(ctx, "Failed to check knowledge existence: %v", err)
		return nil, err
	}
	if exists {
		logger.Infof(ctx, "File URL already exists: %s", fileURL)
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
	knowledge := &types.Knowledge{
		ID:               uuid.New().String(),
		TenantID:         tenantID,
		KnowledgeBaseID:  kbID,
		Type:             "file_url",
		Channel:          defaultChannel(channel),
		Title:            title,
		FileName:         displayName,
		FileType:         fileType,
		Source:           fileURL,
		FileHash:         fileHash,
		ParseStatus:      "pending",
		EnableStatus:     "disabled",
		CreatedAt:        time.Now(),
		UpdatedAt:        time.Now(),
		EmbeddingModelID: kb.EmbeddingModelID,
		TagID:            tagID,
	}
	if knowledge.Title == "" {
		knowledge.Title = displayName
	}

	if err := s.repo.CreateKnowledge(ctx, knowledge); err != nil {
		logger.Errorf(ctx, "Failed to create knowledge record: %v", err)
		return nil, err
	}

	// Build async task payload
	enableMultimodelValue := false
	if enableMultimodel != nil {
		enableMultimodelValue = *enableMultimodel
	} else {
		enableMultimodelValue = kb.IsMultimodalEnabled()
	}

	enableQuestionGeneration := false
	questionCount := 3
	if kb.QuestionGenerationConfig != nil && kb.QuestionGenerationConfig.Enabled {
		enableQuestionGeneration = true
		if kb.QuestionGenerationConfig.QuestionCount > 0 {
			questionCount = kb.QuestionGenerationConfig.QuestionCount
		}
	}

	lang, _ := types.LanguageFromContext(ctx)
	taskPayload := types.DocumentProcessPayload{
		TenantID:                 tenantID,
		KnowledgeID:              knowledge.ID,
		KnowledgeBaseID:          kbID,
		FileURL:                  fileURL,
		FileName:                 fileName,
		FileType:                 fileType,
		EnableMultimodel:         enableMultimodelValue,
		EnableQuestionGeneration: enableQuestionGeneration,
		QuestionCount:            questionCount,
		Language:                 lang,
	}

	langfuse.InjectTracing(ctx, &taskPayload)
	payloadBytes, err := json.Marshal(taskPayload)
	if err != nil {
		logger.Errorf(ctx, "Failed to marshal file URL process task payload: %v", err)
		return knowledge, nil
	}

	task := asynq.NewTask(types.TypeDocumentProcess, payloadBytes, asynq.Queue("default"))
	info, err := s.task.Enqueue(task)
	if err != nil {
		logger.Errorf(ctx, "Failed to enqueue file URL process task: %v", err)
		return knowledge, nil
	}
	logger.Infof(ctx, "Enqueued file URL process task: id=%s queue=%s knowledge_id=%s", info.ID, info.Queue, knowledge.ID)

	logger.Infof(ctx, "Knowledge from file URL created successfully, ID: %s", knowledge.ID)
	return knowledge, nil
}

// CreateKnowledgeFromPassage creates a knowledge entry from text passages
func (s *knowledgeService) CreateKnowledgeFromPassage(ctx context.Context,
	kbID string, passage []string, channel string,
) (*types.Knowledge, error) {
	return s.createKnowledgeFromPassageInternal(ctx, kbID, passage, false, channel)
}

// CreateKnowledgeFromPassageSync creates a knowledge entry from text passages and waits for indexing to complete.
func (s *knowledgeService) CreateKnowledgeFromPassageSync(ctx context.Context,
	kbID string, passage []string, channel string,
) (*types.Knowledge, error) {
	return s.createKnowledgeFromPassageInternal(ctx, kbID, passage, true, channel)
}

// CreateKnowledgeFromManual creates or saves manual Markdown knowledge content.
func (s *knowledgeService) CreateKnowledgeFromManual(ctx context.Context,
	kbID string, payload *types.ManualKnowledgePayload, channel string,
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

	if err := checkStorageEngineConfigured(ctx, kb); err != nil {
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
		Channel:          defaultChannel(channel),
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
		logger.Infof(ctx, "Manual knowledge created, enqueuing async processing task, ID: %s", knowledge.ID)
		if err := s.enqueueManualProcessing(ctx, knowledge, cleanContent, false); err != nil {
			logger.Errorf(ctx, "Failed to enqueue manual processing task for new knowledge: %v", err)
			// Non-fatal: mark as failed so user can retry
			knowledge.ParseStatus = "failed"
			knowledge.ErrorMessage = "Failed to enqueue processing task"
			s.repo.UpdateKnowledge(ctx, knowledge)
		}
	}

	return knowledge, nil
}

// createKnowledgeFromPassageInternal consolidates the common logic for creating knowledge from passages.
// When syncMode is true, chunk processing is performed synchronously; otherwise, it's processed asynchronously.
func (s *knowledgeService) createKnowledgeFromPassageInternal(ctx context.Context,
	kbID string, passage []string, syncMode bool, channel string,
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
		Channel:          defaultChannel(channel),
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

		lang, _ := types.LanguageFromContext(ctx)
		taskPayload := types.DocumentProcessPayload{
			TenantID:                 tenantID,
			KnowledgeID:              knowledge.ID,
			KnowledgeBaseID:          kbID,
			Passages:                 safePassages,
			EnableMultimodel:         false, // 文本段落不支持多模态
			EnableQuestionGeneration: enableQuestionGeneration,
			QuestionCount:            questionCount,
			Language:                 lang,
		}

		langfuse.InjectTracing(ctx, &taskPayload)
		payloadBytes, err := json.Marshal(taskPayload)
		if err != nil {
			logger.Errorf(ctx, "Failed to marshal passage process task payload: %v", err)
			// 即使入队失败，也返回knowledge
			return knowledge, nil
		}

		task := asynq.NewTask(types.TypeDocumentProcess, payloadBytes, asynq.Queue("default"), asynq.MaxRetry(3))
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

// UpdateManualKnowledge updates manual Markdown knowledge content.
// For publish status, the heavy operations (cleanup old indexes, re-chunking,
// re-embedding) are offloaded to an Asynq task so the HTTP response returns quickly.
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

	// Publish: persist pending status and enqueue async task for cleanup + re-indexing
	existing.ParseStatus = "pending"
	existing.Description = ""
	existing.ProcessedAt = nil

	if err := s.repo.UpdateKnowledge(ctx, existing); err != nil {
		logger.Errorf(ctx, "Failed to persist manual knowledge before indexing: %v", err)
		return nil, err
	}

	logger.Infof(ctx, "Manual knowledge updated, enqueuing async processing task, ID: %s", existing.ID)
	if err := s.enqueueManualProcessing(ctx, existing, cleanContent, true); err != nil {
		logger.Errorf(ctx, "Failed to enqueue manual processing task: %v", err)
		// Non-fatal: mark as failed so user can retry
		existing.ParseStatus = "failed"
		existing.ErrorMessage = "Failed to enqueue processing task"
		s.repo.UpdateKnowledge(ctx, existing)
		return nil, werrors.NewInternalServerError("Failed to submit processing task")
	}
	return existing, nil
}

// enqueueManualProcessing enqueues a manual:process Asynq task for async cleanup + re-indexing.
func (s *knowledgeService) enqueueManualProcessing(ctx context.Context,
	knowledge *types.Knowledge, content string, needCleanup bool,
) error {
	requestID, _ := types.RequestIDFromContext(ctx)
	payload := types.ManualProcessPayload{
		RequestId:       requestID,
		TenantID:        knowledge.TenantID,
		KnowledgeID:     knowledge.ID,
		KnowledgeBaseID: knowledge.KnowledgeBaseID,
		Content:         content,
		NeedCleanup:     needCleanup,
	}
	langfuse.InjectTracing(ctx, &payload)
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal manual process payload: %w", err)
	}

	task := asynq.NewTask(types.TypeManualProcess, payloadBytes, asynq.Queue("default"), asynq.MaxRetry(3))
	info, err := s.task.Enqueue(task)
	if err != nil {
		return fmt.Errorf("failed to enqueue manual process task: %w", err)
	}
	logger.Infof(ctx, "Enqueued manual process task: knowledge_id=%s, asynq_id=%s", knowledge.ID, info.ID)
	return nil
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

// sanitizeManualDownloadFilename converts a knowledge title into a safe .md
// download filename. Characters that are illegal or dangerous in HTTP header
// values and file-system paths are removed or replaced; a blank result falls
// back to "untitled".
func sanitizeManualDownloadFilename(title string) string {
	safeName := strings.NewReplacer(
		"\n", "", "\r", "", "\t", "", "/", "-", "\\", "-", "\"", "'",
	).Replace(title)
	if strings.TrimSpace(safeName) == "" {
		safeName = "untitled"
	}
	if !strings.HasSuffix(strings.ToLower(safeName), manualFileExtension) {
		safeName += manualFileExtension
	}
	return safeName
}

func (s *knowledgeService) triggerManualProcessing(ctx context.Context,
	kb *types.KnowledgeBase, knowledge *types.Knowledge, content string, doSync bool,
) {
	clean := strings.TrimSpace(content)
	if clean == "" {
		return
	}

	// Resolve embedded data:base64 images and remote http(s) images → storage, replace URLs.
	// Runs before chunking so chunks contain stable provider:// URLs.
	var resolvedImages []docparser.StoredImage
	if s.imageResolver != nil {
		fileSvc := s.resolveFileService(ctx, kb)
		afterDataURI, fromDataURI, _ := s.imageResolver.ResolveDataURIImages(ctx, clean, fileSvc, knowledge.TenantID)
		if len(fromDataURI) > 0 {
			logger.Infof(ctx, "Resolved %d data-URI images for manual knowledge %s", len(fromDataURI), knowledge.ID)
			clean = afterDataURI
			resolvedImages = append(resolvedImages, fromDataURI...)
		}
		updatedContent, storedImages, resolveErr := s.imageResolver.ResolveRemoteImages(ctx, clean, fileSvc, knowledge.TenantID)
		if resolveErr != nil {
			logger.Warnf(ctx, "Remote image resolution partially failed: %v", resolveErr)
		}
		if len(storedImages) > 0 {
			logger.Infof(ctx, "Resolved %d remote images for manual knowledge %s", len(storedImages), knowledge.ID)
			clean = updatedContent
			resolvedImages = append(resolvedImages, storedImages...)
		}
	}

	// Manual content is markdown - chunk directly with Go chunker
	chunkCfg := buildSplitterConfig(kb)

	var parsed []types.ParsedChunk
	opts := ProcessChunksOptions{
		// When the KB has VLM enabled and we resolved remote images, pass them
		// through so processChunks will enqueue image:multimodal tasks (OCR + caption).
		EnableMultimodel: kb.IsMultimodalEnabled() && len(resolvedImages) > 0,
		StoredImages:     resolvedImages,
	}
	if kb.QuestionGenerationConfig != nil && kb.QuestionGenerationConfig.Enabled {
		opts.EnableQuestionGeneration = true
		opts.QuestionCount = kb.QuestionGenerationConfig.QuestionCount
		if opts.QuestionCount <= 0 {
			opts.QuestionCount = 3
		}
	}

	if kb.ChunkingConfig.EnableParentChild {
		parentCfg, childCfg := buildParentChildConfigs(kb.ChunkingConfig, chunkCfg)
		pcResult := chunker.SplitTextParentChild(clean, parentCfg, childCfg)
		parsed = make([]types.ParsedChunk, len(pcResult.Children))
		for i, c := range pcResult.Children {
			parsed[i] = types.ParsedChunk{
				Content:     c.Content,
				Seq:         c.Seq,
				Start:       c.Start,
				End:         c.End,
				ParentIndex: c.ParentIndex,
			}
		}
		parentChunks := make([]types.ParsedParentChunk, len(pcResult.Parents))
		for i, p := range pcResult.Parents {
			parentChunks[i] = types.ParsedParentChunk{Content: p.Content, Seq: p.Seq, Start: p.Start, End: p.End}
		}
		opts.ParentChunks = parentChunks
	} else {
		splitChunks := chunker.SplitText(clean, chunkCfg)
		parsed = make([]types.ParsedChunk, len(splitChunks))
		for i, c := range splitChunks {
			parsed[i] = types.ParsedChunk{
				Content: c.Content,
				Seq:     c.Seq,
				Start:   c.Start,
				End:     c.End,
			}
		}
	}

	if doSync {
		s.processChunks(ctx, kb, knowledge, parsed, opts)
		return
	}

	newCtx := logger.CloneContext(ctx)
	go s.processChunks(newCtx, kb, knowledge, parsed, opts)
}
