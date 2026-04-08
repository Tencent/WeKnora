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
	"github.com/Tencent/WeKnora/internal/models/asr"
	"github.com/Tencent/WeKnora/internal/models/utils/ollama"
	"github.com/Tencent/WeKnora/internal/models/video"
	"github.com/Tencent/WeKnora/internal/models/vlm"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	secutils "github.com/Tencent/WeKnora/internal/utils"
	"github.com/google/uuid"
	"github.com/hibiken/asynq"
)

const (
	vlmFramePrompt = "Analyze this video frame and provide a detailed description in Chinese. Requirements:\n" +
		"1. Describe visible objects, people, and their actions\n" +
		"2. Describe the scene and environment\n" +
		"3. Extract and describe any text on the screen\n" +
		"4. Note any notable events or changes\n" +
		"5. Be concise but comprehensive"
	vlmSummaryPrompt = "Summarize the following video frame descriptions into a coherent video summary in Chinese. Requirements:\n" +
		"1. The descriptions are ordered chronologically by timestamp\n" +
		"2. Focus on the main storyline and key events\n" +
		"3. Maintain the temporal flow of the video\n" +
		"4. Be concise but comprehensive"
)

// VideoMultimodalService handles video:multimodal asynq tasks.
// It reads videos from storage (via FileService for provider:// URLs),
// extracts frames using FFmpeg, performs VLM analysis and ASR transcription,
// and creates child chunks.
type VideoMultimodalService struct {
	chunkService   interfaces.ChunkService
	modelService   interfaces.ModelService
	kbService      interfaces.KnowledgeBaseService
	knowledgeRepo  interfaces.KnowledgeRepository
	tenantRepo     interfaces.TenantRepository
	retrieveEngine interfaces.RetrieveEngineRegistry
	ollamaService  *ollama.OllamaService
	taskEnqueuer   interfaces.TaskEnqueuer
}

func NewVideoMultimodalService(
	chunkService interfaces.ChunkService,
	modelService interfaces.ModelService,
	kbService interfaces.KnowledgeBaseService,
	knowledgeRepo interfaces.KnowledgeRepository,
	tenantRepo interfaces.TenantRepository,
	retrieveEngine interfaces.RetrieveEngineRegistry,
	ollamaService *ollama.OllamaService,
	taskEnqueuer interfaces.TaskEnqueuer,
) interfaces.TaskHandler {
	return &VideoMultimodalService{
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

// Handle implements asynq handler for TypeVideoMultimodal.
func (s *VideoMultimodalService) Handle(ctx context.Context, task *asynq.Task) error {
	var payload types.VideoMultimodalPayload
	if err := json.Unmarshal(task.Payload(), &payload); err != nil {
		return fmt.Errorf("unmarshal video multimodal payload: %w", err)
	}

	logger.Infof(ctx, "[VideoMultimodal] Processing video: chunk=%s, url=%s, vlm=%v, asr=%v",
		payload.ChunkID, payload.VideoURL, payload.EnableVLM, payload.EnableASR)

	ctx = context.WithValue(ctx, types.TenantIDContextKey, payload.TenantID)
	if payload.Language != "" {
		ctx = context.WithValue(ctx, types.LanguageContextKey, payload.Language)
	}

	var videoBytes []byte
	videoBytes, err := s.readVideoBytes(ctx, payload)
	if err != nil {
		return fmt.Errorf("read video bytes: %w", err)
	}

	if !payload.EnableVLM && !payload.EnableASR {
		logger.Infof(ctx, "[VideoMultimodal] Both VLM and ASR are disabled, nothing to do")
		return nil
	}

	var frames []*video.Frame
	var extractor video.Extractor
	if payload.EnableVLM {
		var extErr error
		extractor, extErr = video.NewExtractor(nil)
		if extErr != nil {
			logger.Warnf(ctx, "[VideoMultimodal] FFmpeg not found or not executable, frame extraction disabled: %v", extErr)
			logger.Warnf(ctx, "[VideoMultimodal] Note: Please install FFmpeg to enable VLM frame analysis")
			frames = []*video.Frame{}
		} else {
			options := video.DefaultExtractOptions()
			if payload.MaxFrames > 0 {
				options.MaxFrames = payload.MaxFrames
			}
			var extractErr error
			frames, extractErr = extractor.ExtractFrames(ctx, videoBytes, options)
			if extractErr != nil {
				logger.Warnf(ctx, "[VideoMultimodal] Frame extraction failed: %v", extractErr)
				frames = []*video.Frame{}
			}
		}
	} else {
		logger.Infof(ctx, "[VideoMultimodal] VLM disabled, skipping frame extraction")
		frames = []*video.Frame{}
	}

	frameDescriptions := make([]string, 0, len(frames))

	if payload.EnableVLM {
		vlmMdl, vlmErr := s.resolveVLM(ctx, payload.KnowledgeBaseID)
		if vlmErr != nil {
			logger.Warnf(ctx, "[VideoMultimodal] Failed to resolve VLM: %v", vlmErr)
		} else {
			for i, frame := range frames {
				desc, descErr := vlmMdl.Predict(ctx, frame.ImageData, vlmFramePrompt)
				if descErr != nil {
					logger.Warnf(ctx, "[VideoMultimodal] VLM analysis failed for frame %d at %.2fs: %v",
						i, frame.Timestamp, descErr)
					continue
				}
				frameDesc := fmt.Sprintf("[%.2fs] %s", frame.Timestamp, desc)
				frameDescriptions = append(frameDescriptions, frameDesc)
				logger.Infof(ctx, "[VideoMultimodal] Frame %d analyzed at %.2fs, desc len=%d",
					i, frame.Timestamp, len(desc))
			}
		}
	}

	var asrText string
	if payload.EnableASR {
		asrText, err = s.transcribeAudio(ctx, videoBytes, payload.KnowledgeBaseID)
		if err != nil {
			logger.Warnf(ctx, "[VideoMultimodal] ASR transcription failed: %v", err)
		} else if asrText != "" {
			logger.Infof(ctx, "[VideoMultimodal] ASR transcription completed, len=%d", len(asrText))
		}
	}

	var videoSummary string
	if len(frameDescriptions) > 0 && payload.EnableVLM {
		vlmMdl, _ := s.resolveVLM(ctx, payload.KnowledgeBaseID)
		if vlmMdl != nil {
			combinedInput := strings.Join(frameDescriptions, "\n")
			summary, sumErr := vlmMdl.Predict(ctx, []byte(combinedInput), vlmSummaryPrompt)
			if sumErr != nil {
				logger.Warnf(ctx, "[VideoMultimodal] Video summarization failed: %v", sumErr)
				videoSummary = combinedInput
			} else {
				videoSummary = summary
			}
		} else {
			videoSummary = strings.Join(frameDescriptions, "\n")
		}
	}

	videoInfo := types.VideoInfo{
		URL:               payload.VideoURL,
		FrameCount:        len(frameDescriptions),
		HasVLMAnalysis:    len(frameDescriptions) > 0,
		HasASR:            asrText != "",
		VideoSummary:      videoSummary,
		ASRText:           asrText,
		FrameDescriptions: frameDescriptions,
	}
	videoInfoJSON, _ := json.Marshal([]types.VideoInfo{videoInfo})

	newChunks := s.buildVideoChunks(payload, frameDescriptions, videoSummary, asrText, string(videoInfoJSON))

	if len(newChunks) == 0 {
		// Even if VLM/ASR both failed, mark knowledge as completed
		s.finalizeVideoKnowledge(ctx, payload, "")
		return nil
	}

	if err := s.chunkService.CreateChunks(ctx, newChunks); err != nil {
		return fmt.Errorf("create video multimodal chunks: %w", err)
	}
	for _, c := range newChunks {
		logger.Infof(ctx, "[VideoMultimodal] Created %s chunk %s for video %s, len=%d",
			c.ChunkType, c.ID, payload.VideoURL, len(c.Content))
	}

	// Index chunks so they can be retrieved
	s.indexChunks(ctx, payload, newChunks)

	// Update the parent text chunk's VideoInfo (mirrors old docreader behaviour)
	s.updateParentChunkVideoInfo(ctx, payload, videoInfo)

	// For standalone video files, use summary as the knowledge description
	// and mark the knowledge as completed (it was kept in "processing" until now).
	s.finalizeVideoKnowledge(ctx, payload, videoSummary)

	// Enqueue question generation for the frame descriptions/summary/ASR content if KB has it enabled.
	// During initial processChunks, question generation is skipped for video-type
	// knowledge because the text chunk is just a markdown reference. Now that we
	// have real textual content (frame descriptions, summary, ASR), we can generate questions.
	s.enqueueQuestionGenerationIfEnabled(ctx, payload)

	return nil
}

func (s *VideoMultimodalService) readVideoBytes(ctx context.Context, payload types.VideoMultimodalPayload) ([]byte, error) {
	var err error
	var videoBytes []byte
	if types.ParseProviderScheme(payload.VideoURL) != "" {
		fileSvc := s.resolveFileServiceForPayload(ctx, payload)
		if fileSvc == nil {
			logger.Warnf(ctx, "[VideoMultimodal] Resolve tenant file service failed, fallback to URL/local: tenant=%d kb=%s",
				payload.TenantID, payload.KnowledgeBaseID)
		} else {
			// provider:// scheme — read via FileService
			reader, getErr := fileSvc.GetFile(ctx, payload.VideoURL)
			if getErr != nil {
				logger.Warnf(ctx, "[VideoMultimodal] FileService.GetFile(%s) failed: %v", payload.VideoURL, getErr)
			} else {
				videoBytes, err = io.ReadAll(reader)
				reader.Close()
				if err != nil {
					logger.Warnf(ctx, "[VideoMultimodal] Read provider file %s failed: %v", payload.VideoURL, err)
					videoBytes = nil
				}
			}
		}
	}
	if videoBytes == nil && payload.VideoLocalPath != "" {
		videoBytes, err = os.ReadFile(payload.VideoLocalPath)
		if err != nil {
			logger.Warnf(ctx, "[VideoMultimodal] Local file %s not available (%v), trying URL", payload.VideoLocalPath, err)
			videoBytes = nil
		}
	}
	if videoBytes == nil {
		videoBytes, err = downloadVideoFromURL(payload.VideoURL)
		if err != nil {
			logger.Errorf(ctx, "[VideoMultimodal] Failed to download video from URL %s: %v", payload.VideoURL, err)
			return nil, fmt.Errorf("read video from URL %s failed: %w", payload.VideoURL, err)
		}
		logger.Infof(ctx, "[VideoMultimodal] Video downloaded from URL, len=%d", len(videoBytes))
	}
	return videoBytes, nil
}

// downloadVideoFromURL downloads video bytes from an HTTP(S) URL.
func downloadVideoFromURL(videoURL string) ([]byte, error) {
	return secutils.DownloadBytes(videoURL)
}

// transcribeAudio extracts and transcribes audio from a video using ASR.
func (s *VideoMultimodalService) transcribeAudio(ctx context.Context, videoBytes []byte, kbID string) (string, error) {
	asrMdl, err := s.resolveASR(ctx, kbID)
	if err != nil {
		return "", fmt.Errorf("resolve ASR: %w", err)
	}
	return asrMdl.Transcribe(ctx, videoBytes, "video.mp4")
}

// buildVideoChunks creates child chunks for video multimodal analysis results:
//   - Frame descriptions (one chunk per frame)
//   - Video summary (single chunk)
//   - ASR transcription (single chunk)
func (s *VideoMultimodalService) buildVideoChunks(
	payload types.VideoMultimodalPayload,
	frameDescriptions []string,
	videoSummary string,
	asrText string,
	videoInfoJSON string,
) []*types.Chunk {
	var newChunks []*types.Chunk

	for _, desc := range frameDescriptions {
		chunk := &types.Chunk{
			ID:              uuid.New().String(),
			TenantID:        payload.TenantID,
			KnowledgeID:     payload.KnowledgeID,
			KnowledgeBaseID: payload.KnowledgeBaseID,
			Content:         desc,
			ChunkType:       types.ChunkTypeVideoFrame,
			ParentChunkID:   payload.ChunkID,
			IsEnabled:       true,
			Flags:           types.ChunkFlagRecommended,
			VideoInfo:       videoInfoJSON,
			CreatedAt:       time.Now(),
			UpdatedAt:       time.Now(),
		}
		newChunks = append(newChunks, chunk)
	}

	if videoSummary != "" {
		newChunks = append(newChunks, &types.Chunk{
			ID:              uuid.New().String(),
			TenantID:        payload.TenantID,
			KnowledgeID:     payload.KnowledgeID,
			KnowledgeBaseID: payload.KnowledgeBaseID,
			Content:         videoSummary,
			ChunkType:       types.ChunkTypeVideoCaption,
			ParentChunkID:   payload.ChunkID,
			IsEnabled:       true,
			Flags:           types.ChunkFlagRecommended,
			VideoInfo:       videoInfoJSON,
			CreatedAt:       time.Now(),
			UpdatedAt:       time.Now(),
		})
	}

	if asrText != "" {
		newChunks = append(newChunks, &types.Chunk{
			ID:              uuid.New().String(),
			TenantID:        payload.TenantID,
			KnowledgeID:     payload.KnowledgeID,
			KnowledgeBaseID: payload.KnowledgeBaseID,
			Content:         asrText,
			ChunkType:       types.ChunkTypeVideoASR,
			ParentChunkID:   payload.ChunkID,
			IsEnabled:       true,
			Flags:           types.ChunkFlagRecommended,
			VideoInfo:       videoInfoJSON,
			CreatedAt:       time.Now(),
			UpdatedAt:       time.Now(),
		})
	}

	return newChunks
}

// finalizeVideoKnowledge updates the knowledge after multimodal processing:
//   - For standalone video files: sets Description from summary and marks ParseStatus as completed.
//   - For videos extracted from documents: no-op (description comes from summary generation).
func (s *VideoMultimodalService) finalizeVideoKnowledge(ctx context.Context, payload types.VideoMultimodalPayload, summary string) {
	knowledge, err := s.knowledgeRepo.GetKnowledgeByIDOnly(ctx, payload.KnowledgeID)
	if err != nil {
		logger.Warnf(ctx, "[VideoMultimodal] Failed to get knowledge %s: %v", payload.KnowledgeID, err)
		return
	}
	if knowledge == nil {
		return
	}
	if !IsVideoType(knowledge.FileType) {
		return
	}

	if summary != "" {
		knowledge.Description = summary
	}
	knowledge.ParseStatus = types.ParseStatusCompleted
	knowledge.UpdatedAt = time.Now()
	if err := s.knowledgeRepo.UpdateKnowledge(ctx, knowledge); err != nil {
		logger.Warnf(ctx, "[VideoMultimodal] Failed to finalize knowledge: %v", err)
	} else {
		logger.Infof(ctx, "[VideoMultimodal] Finalized video knowledge %s (status=completed, description=%d chars)",
			payload.KnowledgeID, len(knowledge.Description))
	}
}

// indexChunks indexes the newly created video chunks into the retrieval engine
// so they can participate in semantic search.
func (s *VideoMultimodalService) indexChunks(ctx context.Context, payload types.VideoMultimodalPayload, chunks []*types.Chunk) {
	kb, err := s.kbService.GetKnowledgeBaseByIDOnly(ctx, payload.KnowledgeBaseID)
	if err != nil || kb == nil {
		logger.Warnf(ctx, "[VideoMultimodal] Failed to get KB for indexing: %v", err)
		return
	}

	embeddingModel, err := s.modelService.GetEmbeddingModel(ctx, kb.EmbeddingModelID)
	if err != nil {
		logger.Warnf(ctx, "[VideoMultimodal] Failed to get embedding model for indexing: %v", err)
		return
	}

	tenantInfo, err := s.tenantRepo.GetTenantByID(ctx, payload.TenantID)
	if err != nil {
		logger.Warnf(ctx, "[VideoMultimodal] Failed to get tenant for indexing: %v", err)
		return
	}

	engine, err := retriever.NewCompositeRetrieveEngine(s.retrieveEngine, tenantInfo.GetEffectiveEngines())
	if err != nil {
		logger.Warnf(ctx, "[VideoMultimodal] Failed to init retrieve engine: %v", err)
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
		logger.Errorf(ctx, "[VideoMultimodal] Failed to index video chunks: %v", err)
		return
	}

	// Mark chunks as indexed.
	// Must re-fetch from DB because the in-memory objects lack auto-generated fields
	// (e.g. seq_id), and GORM Save would overwrite them with zero values.
	for _, chunk := range chunks {
		dbChunk, err := s.chunkService.GetChunkByIDOnly(ctx, chunk.ID)
		if err != nil {
			logger.Warnf(ctx, "[VideoMultimodal] Failed to fetch chunk %s for status update: %v", chunk.ID, err)
			continue
		}
		dbChunk.Status = int(types.ChunkStatusIndexed)
		if err := s.chunkService.UpdateChunk(ctx, dbChunk); err != nil {
			logger.Warnf(ctx, "[VideoMultimodal] Failed to update chunk %s status to indexed: %v", chunk.ID, err)
		}
	}

	logger.Infof(ctx, "[VideoMultimodal] Indexed %d video chunks for video %s", len(chunks), payload.VideoURL)
}

// updateParentChunkVideoInfo updates the parent text chunk's VideoInfo field,
// replicating the behaviour of the old docreader flow where the parent chunk
// carried the full video metadata (URL, frame descriptions, summary, ASR).
func (s *VideoMultimodalService) updateParentChunkVideoInfo(
	ctx context.Context,
	payload types.VideoMultimodalPayload,
	videoInfo types.VideoInfo,
) {
	if payload.ChunkID == "" {
		return
	}

	chunk, err := s.chunkService.GetChunkByIDOnly(ctx, payload.ChunkID)
	if err != nil {
		logger.Warnf(ctx, "[VideoMultimodal] Failed to get parent chunk %s: %v", payload.ChunkID, err)
		return
	}

	var existingInfos []types.VideoInfo
	if chunk.VideoInfo != "" {
		_ = json.Unmarshal([]byte(chunk.VideoInfo), &existingInfos)
	}

	found := false
	for i, info := range existingInfos {
		if info.URL == videoInfo.URL {
			existingInfos[i] = videoInfo
			found = true
			break
		}
	}
	if !found {
		existingInfos = append(existingInfos, videoInfo)
	}

	videoInfoJSON, _ := json.Marshal(existingInfos)
	chunk.VideoInfo = string(videoInfoJSON)
	chunk.UpdatedAt = time.Now()
	if err := s.chunkService.UpdateChunk(ctx, chunk); err != nil {
		logger.Warnf(ctx, "[VideoMultimodal] Failed to update parent chunk %s VideoInfo: %v", chunk.ID, err)
	} else {
		logger.Infof(ctx, "[VideoMultimodal] Updated parent chunk %s VideoInfo for video", chunk.ID)
	}
}

// resolveVLM creates a vlm.VLM instance for the given knowledge base,
// supporting both new-style (ModelID) and legacy (inline BaseURL) configs.
func (s *VideoMultimodalService) resolveVLM(ctx context.Context, kbID string) (vlm.VLM, error) {
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

// resolveASR creates an asr.ASR instance for the given knowledge base.
func (s *VideoMultimodalService) resolveASR(ctx context.Context, kbID string) (asr.ASR, error) {
	kb, err := s.kbService.GetKnowledgeBaseByIDOnly(ctx, kbID)
	if err != nil {
		return nil, fmt.Errorf("get knowledge base %s: %w", kbID, err)
	}
	if kb == nil {
		return nil, fmt.Errorf("knowledge base %s not found", kbID)
	}

	asrCfg := kb.ASRConfig
	if !asrCfg.IsASREnabled() {
		return nil, fmt.Errorf("ASR is not enabled for knowledge base %s", kbID)
	}

	return s.modelService.GetASRModel(ctx, asrCfg.ModelID)
}

// resolveFileServiceForPayload resolves tenant/KB scoped file service for reading provider:// URLs.
func (s *VideoMultimodalService) resolveFileServiceForPayload(ctx context.Context, payload types.VideoMultimodalPayload) interfaces.FileService {
	tenant, err := s.tenantRepo.GetTenantByID(ctx, payload.TenantID)
	if err != nil || tenant == nil {
		logger.Warnf(ctx, "[VideoMultimodal] GetTenantByID failed: tenant=%d err=%v", payload.TenantID, err)
		return nil
	}

	provider := types.ParseProviderScheme(payload.VideoURL)
	if provider == "" {
		kb, kbErr := s.kbService.GetKnowledgeBaseByIDOnly(ctx, payload.KnowledgeBaseID)
		if kbErr != nil {
			logger.Warnf(ctx, "[VideoMultimodal] GetKnowledgeBaseByIDOnly failed: kb=%s err=%v", payload.KnowledgeBaseID, kbErr)
		} else if kb != nil {
			provider = strings.ToLower(strings.TrimSpace(kb.GetStorageProvider()))
		}
	}

	baseDir := strings.TrimSpace(os.Getenv("LOCAL_STORAGE_BASE_DIR"))
	fileSvc, _, svcErr := filesvc.NewFileServiceFromStorageConfig(provider, tenant.StorageEngineConfig, baseDir)
	if svcErr != nil {
		logger.Warnf(ctx, "[VideoMultimodal] resolve file service failed: tenant=%d provider=%s err=%v", payload.TenantID, provider, svcErr)
		return nil
	}
	return fileSvc
}

// enqueueQuestionGenerationIfEnabled checks if the knowledge base has question
// generation enabled and, if so, enqueues a task for the video knowledge.
func (s *VideoMultimodalService) enqueueQuestionGenerationIfEnabled(ctx context.Context, payload types.VideoMultimodalPayload) {
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
		logger.Warnf(ctx, "[VideoMultimodal] Failed to marshal question generation payload: %v", err)
		return
	}

	task := asynq.NewTask(types.TypeQuestionGeneration, payloadBytes, asynq.Queue("low"), asynq.MaxRetry(3))
	if _, err := s.taskEnqueuer.Enqueue(task); err != nil {
		logger.Warnf(ctx, "[VideoMultimodal] Failed to enqueue question generation for %s: %v", payload.KnowledgeID, err)
	} else {
		logger.Infof(ctx, "[VideoMultimodal] Enqueued question generation task for video knowledge %s (count=%d)",
			payload.KnowledgeID, questionCount)
	}
}
