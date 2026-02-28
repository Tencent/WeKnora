package service

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

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
	vlmOCRPrompt = "请提取这张文档图片中的所有正文内容，用纯 Markdown 格式输出。要求：\n" +
		"1. 忽略页眉、页脚\n" +
		"2. 表格使用 Markdown 表格语法\n" +
		"3. 公式使用 LaTeX 格式（用 $ 或 $$ 包裹）\n" +
		"4. 按照原文阅读顺序组织\n" +
		"5. 只输出提取到的文本内容，不要添加任何 HTML 标签\n" +
		"如果图片中没有可识别的文字内容，请回复：无文字内容。"
	vlmCaptionPrompt = "简单凝炼的描述图片的主要内容"
)

// ImageMultimodalService handles image:multimodal asynq tasks.
// It reads images from local storage, performs OCR and VLM caption,
// and creates child chunks for the processed content.
type ImageMultimodalService struct {
	chunkService   interfaces.ChunkService
	modelService   interfaces.ModelService
	kbService      interfaces.KnowledgeBaseService
	knowledgeRepo  interfaces.KnowledgeRepository
	tenantRepo     interfaces.TenantRepository
	retrieveEngine interfaces.RetrieveEngineRegistry
	ollamaService  *ollama.OllamaService
}

func NewImageMultimodalService(
	chunkService interfaces.ChunkService,
	modelService interfaces.ModelService,
	kbService interfaces.KnowledgeBaseService,
	knowledgeRepo interfaces.KnowledgeRepository,
	tenantRepo interfaces.TenantRepository,
	retrieveEngine interfaces.RetrieveEngineRegistry,
	ollamaService *ollama.OllamaService,
) interfaces.TaskHandler {
	return &ImageMultimodalService{
		chunkService:   chunkService,
		modelService:   modelService,
		kbService:      kbService,
		knowledgeRepo:  knowledgeRepo,
		tenantRepo:     tenantRepo,
		retrieveEngine: retrieveEngine,
		ollamaService:  ollamaService,
	}
}

// Handle implements asynq handler for TypeImageMultimodal.
func (s *ImageMultimodalService) Handle(ctx context.Context, task *asynq.Task) error {
	var payload types.ImageMultimodalPayload
	if err := json.Unmarshal(task.Payload(), &payload); err != nil {
		return fmt.Errorf("unmarshal image multimodal payload: %w", err)
	}

	logger.Infof(ctx, "[ImageMultimodal] Processing image: chunk=%s, url=%s, local_path=%s, ocr=%v, caption=%v",
		payload.ChunkID, payload.ImageURL, payload.ImageLocalPath, payload.EnableOCR, payload.EnableCaption)

	ctx = context.WithValue(ctx, types.TenantIDContextKey, payload.TenantID)

	vlmModel, err := s.resolveVLM(ctx, payload.KnowledgeBaseID)
	if err != nil {
		return fmt.Errorf("resolve VLM: %w", err)
	}

	imgBytes, err := os.ReadFile(payload.ImageLocalPath)
	if err != nil {
		logger.Warnf(ctx, "[ImageMultimodal] Local file %s not available (%v), trying to download from URL", payload.ImageLocalPath, err)
		imgBytes, err = downloadImageFromURL(payload.ImageURL)
		if err != nil {
			logger.Errorf(ctx, "[ImageMultimodal] Failed to download image from URL %s: %v", payload.ImageURL, err)
			return fmt.Errorf("read image: local file %s not found and URL download failed: %w", payload.ImageLocalPath, err)
		}
		logger.Infof(ctx, "[ImageMultimodal] Image downloaded from URL, len=%d", len(imgBytes))
	} else {
		logger.Infof(ctx, "[ImageMultimodal] Image bytes read from %s, len=%d", payload.ImageLocalPath, len(imgBytes))
		// Only clean up the local file when images have been re-uploaded to
		// external object storage (COS/TOS) — the serving URL will be an
		// absolute http(s) URL in that case. For local and MinIO storage the
		// local file IS the serving file and must be kept.
		if strings.HasPrefix(payload.ImageURL, "http://") || strings.HasPrefix(payload.ImageURL, "https://") {
			defer os.Remove(payload.ImageLocalPath)
		}
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

// downloadImageFromURL downloads image bytes from an HTTP(S) URL.
func downloadImageFromURL(imageURL string) ([]byte, error) {
	return secutils.DownloadBytes(imageURL)
}
