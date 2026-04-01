package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	filesvc "github.com/Tencent/WeKnora/internal/application/service/file"
	"github.com/Tencent/WeKnora/internal/application/service/retriever"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/utils/ollama"
	"github.com/Tencent/WeKnora/internal/models/vlm"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	secutils "github.com/Tencent/WeKnora/internal/utils"
	"github.com/google/uuid"
	"github.com/hibiken/asynq"
)

const (
	vlmOCRPrompt = "Extract all body text content from this document image and output in pure Markdown format. Requirements:\n" +
		"1. Ignore headers and footers\n" +
		"2. Use Markdown table syntax for tables\n" +
		"3. Use LaTeX format for formulas (wrapped with $ or $$)\n" +
		"4. Organize content in the original reading order\n" +
		"5. Only output extracted text content, do not add any HTML tags\n" +
		"If there is no recognizable text content in the image, reply: No text content."
	vlmCaptionPrompt = "Provide a brief and concise description of the main content of the image in Chinese"
)

// ImageMultimodalService handles image:multimodal asynq tasks.
// It reads images from storage (via FileService for provider:// URLs),
// performs OCR and VLM caption, and creates child chunks.
type ImageMultimodalService struct {
	chunkService   interfaces.ChunkService
	modelService   interfaces.ModelService
	kbService      interfaces.KnowledgeBaseService
	knowledgeRepo  interfaces.KnowledgeRepository
	tenantRepo     interfaces.TenantRepository
	retrieveEngine interfaces.RetrieveEngineRegistry
	ollamaService  *ollama.OllamaService
	taskEnqueuer   interfaces.TaskEnqueuer
}

func NewImageMultimodalService(
	chunkService interfaces.ChunkService,
	modelService interfaces.ModelService,
	kbService interfaces.KnowledgeBaseService,
	knowledgeRepo interfaces.KnowledgeRepository,
	tenantRepo interfaces.TenantRepository,
	retrieveEngine interfaces.RetrieveEngineRegistry,
	ollamaService *ollama.OllamaService,
	taskEnqueuer interfaces.TaskEnqueuer,
) interfaces.TaskHandler {
	return &ImageMultimodalService{
		chunkService:   chunkService,
		modelService:   modelService,
		kbService:      kbService,
		knowledgeRepo:  knowledgeRepo,
		tenantRepo:     tenantRepo,
		retrieveEngine: retrieveEngine,
		ollamaService:  ollamaService,
		taskEnqueuer:   taskEnqueuer,
	}
}

// Handle implements asynq handler for TypeImageMultimodal.
func (s *ImageMultimodalService) Handle(ctx context.Context, task *asynq.Task) error {
	var payload types.ImageMultimodalPayload
	if err := json.Unmarshal(task.Payload(), &payload); err != nil {
		return fmt.Errorf("unmarshal image multimodal payload: %w", err)
	}

	logger.Infof(ctx, "[ImageMultimodal] Processing image: chunk=%s, url=%s, ocr=%v, caption=%v",
		payload.ChunkID, payload.ImageURL, payload.EnableOCR, payload.EnableCaption)

	ctx = context.WithValue(ctx, types.TenantIDContextKey, payload.TenantID)
	if payload.Language != "" {
		ctx = context.WithValue(ctx, types.LanguageContextKey, payload.Language)
	}

	vlmModel, err := s.resolveVLM(ctx, payload.KnowledgeBaseID)
	if err != nil {
		return fmt.Errorf("resolve VLM: %w", err)
	}

	// Read image bytes: try provider:// via tenant-resolved FileService,
	// then legacy local path, then HTTP URL.
	var imgBytes []byte
	if types.ParseProviderScheme(payload.ImageURL) != "" {
		fileSvc := s.resolveFileServiceForPayload(ctx, payload)
		if fileSvc == nil {
			logger.Warnf(ctx, "[ImageMultimodal] Resolve tenant file service failed, fallback to URL/local: tenant=%d kb=%s",
				payload.TenantID, payload.KnowledgeBaseID)
		} else {
			// provider:// scheme — read via FileService
			reader, getErr := fileSvc.GetFile(ctx, payload.ImageURL)
			if getErr != nil {
				logger.Warnf(ctx, "[ImageMultimodal] FileService.GetFile(%s) failed: %v", payload.ImageURL, getErr)
			} else {
				imgBytes, err = io.ReadAll(reader)
				reader.Close()
				if err != nil {
					logger.Warnf(ctx, "[ImageMultimodal] Read provider file %s failed: %v", payload.ImageURL, err)
					imgBytes = nil
				}
			}
		}
	}
	if imgBytes == nil && payload.ImageLocalPath != "" {
		imgBytes, err = os.ReadFile(payload.ImageLocalPath)
		if err != nil {
			logger.Warnf(ctx, "[ImageMultimodal] Local file %s not available (%v), trying URL", payload.ImageLocalPath, err)
			imgBytes = nil
		}
	}
	if imgBytes == nil {
		imgBytes, err = downloadImageFromURL(payload.ImageURL)
		if err != nil {
			logger.Errorf(ctx, "[ImageMultimodal] Failed to download image from URL %s: %v", payload.ImageURL, err)
			return fmt.Errorf("read image from URL %s failed: %w", payload.ImageURL, err)
		}
		logger.Infof(ctx, "[ImageMultimodal] Image downloaded from URL, len=%d", len(imgBytes))
	}

	imageInfo := types.ImageInfo{
		URL:         payload.ImageURL,
		OriginalURL: payload.ImageURL,
	}

	if payload.EnableOCR {
		ocrText, ocrErr := vlmModel.Predict(ctx, imgBytes, vlmOCRPrompt)
		if ocrErr != nil {
			logger.Warnf(ctx, "[ImageMultimodal] OCR failed for %s: %v", payload.ImageURL, ocrErr)
		} else {
			ocrText = sanitizeOCRText(ocrText)
			if ocrText != "" {
				imageInfo.OCRText = ocrText
			} else {
				logger.Warnf(ctx, "[ImageMultimodal] OCR returned empty/invalid content for %s, discarded", payload.ImageURL)
			}
		}
	}

	if payload.EnableCaption {
		caption, capErr := vlmModel.Predict(ctx, imgBytes, vlmCaptionPrompt)
		if capErr != nil {
			logger.Warnf(ctx, "[ImageMultimodal] Caption failed for %s: %v", payload.ImageURL, capErr)
		} else if caption != "" {
			imageInfo.Caption = caption
		}
	}

	// Build child chunks for OCR and caption results
	imageInfoJSON, _ := json.Marshal([]types.ImageInfo{imageInfo})
	var newChunks []*types.Chunk

	if imageInfo.OCRText != "" {
		newChunks = append(newChunks, &types.Chunk{
			ID:              uuid.New().String(),
			TenantID:        payload.TenantID,
			KnowledgeID:     payload.KnowledgeID,
			KnowledgeBaseID: payload.KnowledgeBaseID,
			Content:         imageInfo.OCRText,
			ChunkType:       types.ChunkTypeImageOCR,
			ParentChunkID:   payload.ChunkID,
			IsEnabled:       true,
			Flags:           types.ChunkFlagRecommended,
			ImageInfo:       string(imageInfoJSON),
			CreatedAt:       time.Now(),
			UpdatedAt:       time.Now(),
		})
	}

	if imageInfo.Caption != "" {
		newChunks = append(newChunks, &types.Chunk{
			ID:              uuid.New().String(),
			TenantID:        payload.TenantID,
			KnowledgeID:     payload.KnowledgeID,
			KnowledgeBaseID: payload.KnowledgeBaseID,
			Content:         imageInfo.Caption,
			ChunkType:       types.ChunkTypeImageCaption,
			ParentChunkID:   payload.ChunkID,
			IsEnabled:       true,
			Flags:           types.ChunkFlagRecommended,
			ImageInfo:       string(imageInfoJSON),
			CreatedAt:       time.Now(),
			UpdatedAt:       time.Now(),
		})
	}

	if len(newChunks) == 0 {
		// Even if OCR/caption both failed, mark knowledge as completed
		s.finalizeImageKnowledge(ctx, payload, "")
		return nil
	}

	// Persist chunks
	if err := s.chunkService.CreateChunks(ctx, newChunks); err != nil {
		return fmt.Errorf("create multimodal chunks: %w", err)
	}
	for _, c := range newChunks {
		logger.Infof(ctx, "[ImageMultimodal] Created %s chunk %s for image %s, len=%d",
			c.ChunkType, c.ID, payload.ImageURL, len(c.Content))
	}

	// Index chunks so they can be retrieved
	s.indexChunks(ctx, payload, newChunks)

	// Update the parent text chunk's ImageInfo (mirrors old docreader behaviour)
	s.updateParentChunkImageInfo(ctx, payload, imageInfo)

	// For standalone image files, use caption as the knowledge description
	// and mark the knowledge as completed (it was kept in "processing" until now).
	s.finalizeImageKnowledge(ctx, payload, imageInfo.Caption)

	// Enqueue question generation for the caption/OCR content if KB has it enabled.
	// During initial processChunks, question generation is skipped for image-type
	// knowledge because the text chunk is just a markdown reference. Now that we
	// have real textual content (caption/OCR), we can generate questions.
	s.enqueueQuestionGenerationIfEnabled(ctx, payload)

	return nil
}

// finalizeImageKnowledge updates the knowledge after multimodal processing:
//   - For standalone image files: sets Description from caption and marks ParseStatus as completed
//     (processChunks kept it in "processing" to wait for multimodal results).
//   - For images extracted from PDFs: no-op (description comes from summary generation).
func (s *ImageMultimodalService) finalizeImageKnowledge(ctx context.Context, payload types.ImageMultimodalPayload, caption string) {
	knowledge, err := s.knowledgeRepo.GetKnowledgeByIDOnly(ctx, payload.KnowledgeID)
	if err != nil {
		logger.Warnf(ctx, "[ImageMultimodal] Failed to get knowledge %s: %v", payload.KnowledgeID, err)
		return
	}
	if knowledge == nil {
		return
	}
	if !IsImageType(knowledge.FileType) {
		return
	}

	if caption != "" {
		knowledge.Description = caption
	}
	knowledge.ParseStatus = types.ParseStatusCompleted
	knowledge.UpdatedAt = time.Now()
	if err := s.knowledgeRepo.UpdateKnowledge(ctx, knowledge); err != nil {
		logger.Warnf(ctx, "[ImageMultimodal] Failed to finalize knowledge: %v", err)
	} else {
		logger.Infof(ctx, "[ImageMultimodal] Finalized image knowledge %s (status=completed, description=%d chars)",
			payload.KnowledgeID, len(knowledge.Description))
	}
}

// indexChunks indexes the newly created multimodal chunks into the retrieval engine
// so they can participate in semantic search.
func (s *ImageMultimodalService) indexChunks(ctx context.Context, payload types.ImageMultimodalPayload, chunks []*types.Chunk) {
	kb, err := s.kbService.GetKnowledgeBaseByIDOnly(ctx, payload.KnowledgeBaseID)
	if err != nil || kb == nil {
		logger.Warnf(ctx, "[ImageMultimodal] Failed to get KB for indexing: %v", err)
		return
	}

	embeddingModel, err := s.modelService.GetEmbeddingModel(ctx, kb.EmbeddingModelID)
	if err != nil {
		logger.Warnf(ctx, "[ImageMultimodal] Failed to get embedding model for indexing: %v", err)
		return
	}

	tenantInfo, err := s.tenantRepo.GetTenantByID(ctx, payload.TenantID)
	if err != nil {
		logger.Warnf(ctx, "[ImageMultimodal] Failed to get tenant for indexing: %v", err)
		return
	}

	engine, err := retriever.NewCompositeRetrieveEngine(s.retrieveEngine, tenantInfo.GetEffectiveEngines())
	if err != nil {
		logger.Warnf(ctx, "[ImageMultimodal] Failed to init retrieve engine: %v", err)
		return
	}

	indexInfoList := make([]*types.IndexInfo, 0, len(chunks))
	for _, chunk := range chunks {
		indexInfoList = append(indexInfoList, &types.IndexInfo{
			Content:         chunk.Content,
			SourceID:        chunk.ID,
			SourceType:      types.ChunkSourceType,
			ChunkID:         chunk.ID,
			KnowledgeID:     chunk.KnowledgeID,
			KnowledgeBaseID: chunk.KnowledgeBaseID,
		})
	}

	if err := engine.BatchIndex(ctx, embeddingModel, indexInfoList); err != nil {
		logger.Errorf(ctx, "[ImageMultimodal] Failed to index multimodal chunks: %v", err)
		return
	}

	// Mark chunks as indexed.
	// Must re-fetch from DB because the in-memory objects lack auto-generated fields
	// (e.g. seq_id), and GORM Save would overwrite them with zero values.
	for _, chunk := range chunks {
		dbChunk, err := s.chunkService.GetChunkByIDOnly(ctx, chunk.ID)
		if err != nil {
			logger.Warnf(ctx, "[ImageMultimodal] Failed to fetch chunk %s for status update: %v", chunk.ID, err)
			continue
		}
		dbChunk.Status = int(types.ChunkStatusIndexed)
		if err := s.chunkService.UpdateChunk(ctx, dbChunk); err != nil {
			logger.Warnf(ctx, "[ImageMultimodal] Failed to update chunk %s status to indexed: %v", chunk.ID, err)
		}
	}

	logger.Infof(ctx, "[ImageMultimodal] Indexed %d multimodal chunks for image %s", len(chunks), payload.ImageURL)
}

// updateParentChunkImageInfo updates the parent text chunk's ImageInfo field,
// replicating the behaviour of the old docreader flow where the parent chunk
// carried the full image metadata (URL, OCR, caption).
func (s *ImageMultimodalService) updateParentChunkImageInfo(ctx context.Context, payload types.ImageMultimodalPayload, imageInfo types.ImageInfo) {
	if payload.ChunkID == "" {
		return
	}

	chunk, err := s.chunkService.GetChunkByIDOnly(ctx, payload.ChunkID)
	if err != nil {
		logger.Warnf(ctx, "[ImageMultimodal] Failed to get parent chunk %s: %v", payload.ChunkID, err)
		return
	}

	var existingInfos []types.ImageInfo
	if chunk.ImageInfo != "" {
		_ = json.Unmarshal([]byte(chunk.ImageInfo), &existingInfos)
	}

	found := false
	for i, info := range existingInfos {
		if info.URL == imageInfo.URL {
			existingInfos[i] = imageInfo
			found = true
			break
		}
	}
	if !found {
		existingInfos = append(existingInfos, imageInfo)
	}

	imageInfoJSON, _ := json.Marshal(existingInfos)
	chunk.ImageInfo = string(imageInfoJSON)
	chunk.UpdatedAt = time.Now()
	if err := s.chunkService.UpdateChunk(ctx, chunk); err != nil {
		logger.Warnf(ctx, "[ImageMultimodal] Failed to update parent chunk %s ImageInfo: %v", chunk.ID, err)
	} else {
		logger.Infof(ctx, "[ImageMultimodal] Updated parent chunk %s ImageInfo for image %s", chunk.ID, payload.ImageURL)
	}
}

// resolveVLM creates a vlm.VLM instance for the given knowledge base,
// supporting both new-style (ModelID) and legacy (inline BaseURL) configs.
func (s *ImageMultimodalService) resolveVLM(ctx context.Context, kbID string) (vlm.VLM, error) {
	kb, err := s.kbService.GetKnowledgeBaseByIDOnly(ctx, kbID)
	if err != nil {
		return nil, fmt.Errorf("get knowledge base %s: %w", kbID, err)
	}
	if kb == nil {
		return nil, fmt.Errorf("knowledge base %s not found", kbID)
	}

	vlmCfg := kb.VLMConfig
	if !vlmCfg.IsEnabled() {
		return nil, fmt.Errorf("VLM is not enabled for knowledge base %s", kbID)
	}

	// New-style: resolve model through ModelService
	if vlmCfg.ModelID != "" {
		return s.modelService.GetVLMModel(ctx, vlmCfg.ModelID)
	}

	// Legacy: create VLM from inline config
	return vlm.NewVLMFromLegacyConfig(vlmCfg, s.ollamaService)
}

// resolveFileServiceForPayload resolves tenant/KB scoped file service for reading provider:// URLs.
func (s *ImageMultimodalService) resolveFileServiceForPayload(ctx context.Context, payload types.ImageMultimodalPayload) interfaces.FileService {
	tenant, err := s.tenantRepo.GetTenantByID(ctx, payload.TenantID)
	if err != nil || tenant == nil {
		logger.Warnf(ctx, "[ImageMultimodal] GetTenantByID failed: tenant=%d err=%v", payload.TenantID, err)
		return nil
	}

	provider := types.ParseProviderScheme(payload.ImageURL)
	if provider == "" {
		kb, kbErr := s.kbService.GetKnowledgeBaseByIDOnly(ctx, payload.KnowledgeBaseID)
		if kbErr != nil {
			logger.Warnf(ctx, "[ImageMultimodal] GetKnowledgeBaseByIDOnly failed: kb=%s err=%v", payload.KnowledgeBaseID, kbErr)
		} else if kb != nil {
			provider = strings.ToLower(strings.TrimSpace(kb.GetStorageProvider()))
		}
	}

	baseDir := strings.TrimSpace(os.Getenv("LOCAL_STORAGE_BASE_DIR"))
	fileSvc, _, svcErr := filesvc.NewFileServiceFromStorageConfig(provider, tenant.StorageEngineConfig, baseDir)
	if svcErr != nil {
		logger.Warnf(ctx, "[ImageMultimodal] resolve file service failed: tenant=%d provider=%s err=%v", payload.TenantID, provider, svcErr)
		return nil
	}
	return fileSvc
}

// enqueueQuestionGenerationIfEnabled checks if the knowledge base has question
// generation enabled and, if so, enqueues a task for the image knowledge.
func (s *ImageMultimodalService) enqueueQuestionGenerationIfEnabled(ctx context.Context, payload types.ImageMultimodalPayload) {
	if s.taskEnqueuer == nil {
		return
	}

	kb, err := s.kbService.GetKnowledgeBaseByIDOnly(ctx, payload.KnowledgeBaseID)
	if err != nil || kb == nil {
		return
	}
	if kb.QuestionGenerationConfig == nil || !kb.QuestionGenerationConfig.Enabled {
		return
	}

	questionCount := kb.QuestionGenerationConfig.QuestionCount
	if questionCount <= 0 {
		questionCount = 3
	}
	if questionCount > 10 {
		questionCount = 10
	}

	taskPayload := types.QuestionGenerationPayload{
		TenantID:        payload.TenantID,
		KnowledgeBaseID: payload.KnowledgeBaseID,
		KnowledgeID:     payload.KnowledgeID,
		QuestionCount:   questionCount,
		Language:        payload.Language,
	}
	payloadBytes, err := json.Marshal(taskPayload)
	if err != nil {
		logger.Warnf(ctx, "[ImageMultimodal] Failed to marshal question generation payload: %v", err)
		return
	}

	task := asynq.NewTask(types.TypeQuestionGeneration, payloadBytes, asynq.Queue("low"), asynq.MaxRetry(3))
	if _, err := s.taskEnqueuer.Enqueue(task); err != nil {
		logger.Warnf(ctx, "[ImageMultimodal] Failed to enqueue question generation for %s: %v", payload.KnowledgeID, err)
	} else {
		logger.Infof(ctx, "[ImageMultimodal] Enqueued question generation task for image knowledge %s (count=%d)",
			payload.KnowledgeID, questionCount)
	}
}

// downloadImageFromURL downloads image bytes from an HTTP(S) URL.
func downloadImageFromURL(imageURL string) ([]byte, error) {
	return secutils.DownloadBytes(imageURL)
}
