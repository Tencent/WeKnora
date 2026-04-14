package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	filesvc "github.com/Tencent/WeKnora/internal/application/service/file"
	"github.com/Tencent/WeKnora/internal/application/service/retriever"
	"github.com/Tencent/WeKnora/internal/infrastructure/chunker"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/asr"
	"github.com/Tencent/WeKnora/internal/models/chat"
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
	vlmBatchPromptTemplate = `Analyze this sequence of {{count}} video frames and provide a detailed description in {{language}}.

{{previous_batch_instruction}}

<Requirements>
1. The images are provided in chronological order.
2. Describe visible objects, people, and their actions.
3. Describe the scene and environment.
4. Extract and describe any text on the screen.
5. Note any notable events or changes across these frames.
6. Be concise but comprehensive, keeping the word count under 150 words.
7. The output must be in {{language}}.
</Requirements>`

	vlmFirstBatchInstruction = "<PreviousContext>\nNone. These are the first frames of the video. Describe them completely.\n</PreviousContext>"
	vlmNextBatchInstruction  = "<PreviousContext>\n{{previous_description}}\n</PreviousContext>\n\nThese are the subsequent frames. Focus ONLY on what has changed or what is new compared to the previous context description provided above. If there are no significant changes, just state 'No significant changes'."

	vlmSummarySystemPromptTemplate = `Please summarize the provided video frame descriptions into a coherent video summary in {{language}}.

<Requirements>
1. The descriptions are ordered chronologically by timestamp. Please maintain this temporal flow.
2. Focus on the main storyline, key events, and the interactions between people and objects.
3. Identify and include any important text or dialogue that appears on screen.
4. Provide a clear, structured summary that captures the essence of the video.
5. Be concise but comprehensive, avoiding redundant descriptions of the same scene.
6. Keep the summary under 1000 words.
7. The output must be in {{language}}.
</Requirements>`

	vlmSummaryUserPromptTemplate = `Here are the video frame descriptions:

<Input>
{{descriptions}}
</Input>

Please provide the summary based on the requirements and input.`
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

	var extractor video.Extractor
	var extErr error
	if payload.EnableVLM || payload.EnableASR {
		extractor, extErr = video.NewExtractor(nil)
		if extErr != nil {
			logger.Warnf(ctx, "[VideoMultimodal] FFmpeg not found or not executable: %v", extErr)
		}
	}

	var wg sync.WaitGroup
	type FrameAnalysis struct {
		StartTimestamp float64
		EndTimestamp   float64
		Description    string
	}
	var frameAnalyses []FrameAnalysis
	var asrSegments []asr.Segment
	var mu sync.Mutex // Mutex to protect concurrent access if needed

	lang := payload.Language
	if lang == "" {
		lang = "zh-CN"
	}
	batchPrompt := strings.ReplaceAll(vlmBatchPromptTemplate, "{{language}}", lang)

	if payload.EnableVLM {
		wg.Add(1)
		go func() {
			defer wg.Done()

			var frames []*video.Frame
			if extractor == nil {
				logger.Warnf(ctx, "[VideoMultimodal] FFmpeg not found or not executable, frame extraction disabled")
				logger.Warnf(ctx, "[VideoMultimodal] Note: Please install FFmpeg to enable VLM frame analysis")
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
				} else {
					logger.Debugf(ctx, "[VideoMultimodal] Successfully extracted %d frames from video", len(frames))
				}
			}

			if len(frames) == 0 {
				return
			}

			vlmMdl, vlmErr := s.resolveVLM(ctx, payload.KnowledgeBaseID)
			if vlmErr != nil {
				logger.Warnf(ctx, "[VideoMultimodal] Failed to resolve VLM: %v", vlmErr)
			} else {
				// Initialize the slice within the goroutine before appending
				localAnalyses := make([]FrameAnalysis, 0)
				var previousDescription string

				// Batch size configuration (number of frames per API call)
				batchSize := 3
				for i := 0; i < len(frames); i += batchSize {
					end := i + batchSize
					if end > len(frames) {
						end = len(frames)
					}
					batchFrames := frames[i:end]

					var batchImages [][]byte
					for _, f := range batchFrames {
						batchImages = append(batchImages, f.ImageData)
					}

					startTs := batchFrames[0].Timestamp
					endTs := batchFrames[len(batchFrames)-1].Timestamp

					logger.Debugf(ctx, "[VideoMultimodal] Starting VLM analysis for frames %d-%d/%d (%.2fs - %.2fs)", i+1, end, len(frames), startTs, endTs)

					currentPrompt := strings.ReplaceAll(batchPrompt, "{{count}}", fmt.Sprintf("%d", len(batchFrames)))
					if i == 0 {
						currentPrompt = strings.ReplaceAll(currentPrompt, "{{previous_batch_instruction}}", vlmFirstBatchInstruction)
					} else {
						nextInstruction := strings.ReplaceAll(vlmNextBatchInstruction, "{{previous_description}}", previousDescription)
						currentPrompt = strings.ReplaceAll(currentPrompt, "{{previous_batch_instruction}}", nextInstruction)
					}

					desc, descErr := vlmMdl.Predict(ctx, batchImages, currentPrompt)
					if descErr != nil {
						logger.Warnf(ctx, "[VideoMultimodal] VLM analysis failed for frames %d-%d at %.2fs - %.2fs: %v",
							i+1, end, startTs, endTs, descErr)
						continue
					}

					// Update previous description for the next iteration if there are changes
					if !strings.Contains(strings.ToLower(desc), "no significant changes") {
						previousDescription = desc
					}

					localAnalyses = append(localAnalyses, FrameAnalysis{
						StartTimestamp: startTs,
						EndTimestamp:   endTs,
						Description:    desc,
					})
					logger.Infof(ctx, "[VideoMultimodal] Frames %d-%d/%d analyzed (%.2fs - %.2fs), desc len=%d",
						i+1, end, len(frames), startTs, endTs, len(desc))
				}
				mu.Lock()
				frameAnalyses = localAnalyses
				mu.Unlock()
			}
		}()
	}

	if payload.EnableASR {
		wg.Add(1)
		go func() {
			defer wg.Done()

			audioBytes := videoBytes
			fileName := "video.mp4"
			if extractor != nil {
				extractedAudio, err := extractor.ExtractAudio(ctx, videoBytes)
				if err != nil {
					logger.Warnf(ctx, "[VideoMultimodal] Audio extraction failed, falling back to original video: %v", err)
				} else if len(extractedAudio) == 0 {
					logger.Infof(ctx, "[VideoMultimodal] No audio stream found in video, skipping ASR transcription")
					return
				} else {
					audioBytes = extractedAudio
					fileName = "audio.mp3"
					logger.Debugf(ctx, "[VideoMultimodal] Extracted audio track for ASR, size: %d bytes", len(audioBytes))
				}
			}

			asrRes, asrErr := s.transcribeAudio(ctx, audioBytes, payload.KnowledgeBaseID, fileName)
			if asrErr != nil {
				logger.Warnf(ctx, "[VideoMultimodal] ASR transcription failed: kb=%s file=%s audioBytes=%d err=%v",
					payload.KnowledgeBaseID, fileName, len(audioBytes), asrErr)
			} else if asrRes == nil {
				logger.Warnf(ctx, "[VideoMultimodal] ASR returned nil result without error: kb=%s file=%s audioBytes=%d",
					payload.KnowledgeBaseID, fileName, len(audioBytes))
			} else {
				asrSegments = asrRes.Segments
				nSeg := len(asrRes.Segments)
				nRunes := utf8.RuneCountInString(asrRes.Text)
				var spanStart, spanEnd float64
				if nSeg > 0 {
					spanStart = asrRes.Segments[0].Start
					spanEnd = asrRes.Segments[0].End
					for _, seg := range asrRes.Segments[1:] {
						if seg.Start < spanStart {
							spanStart = seg.Start
						}
						if seg.End > spanEnd {
							spanEnd = seg.End
						}
					}
				}
				const previewRunes = 200
				preview := strings.TrimSpace(asrRes.Text)
				if utf8.RuneCountInString(preview) > previewRunes {
					preview = string([]rune(preview)[:previewRunes]) + "..."
				}
				logger.Infof(ctx, "[VideoMultimodal] ASR transcription completed: kb=%s file=%s audioBytes=%d textRunes=%d segments=%d timeline=%.2fs-%.2fs preview=%q",
					payload.KnowledgeBaseID, fileName, len(audioBytes), nRunes, nSeg, spanStart, spanEnd, preview)
			}
		}()
	}

	logger.Debugf(ctx, "[VideoMultimodal] Waiting for VLM and ASR tasks to complete...")
	wg.Wait()
	logger.Debugf(ctx, "[VideoMultimodal] VLM and ASR tasks completed")

	var videoSummary string
	var combinedInput string
	var kb *types.KnowledgeBase
	if (len(frameAnalyses) > 0 || len(asrSegments) > 0) && (payload.EnableVLM || payload.EnableASR) {
		var err error
		kb, err = s.kbService.GetKnowledgeBaseByIDOnly(ctx, payload.KnowledgeBaseID)

		type TimelineEvent struct {
			Start float64
			End   float64
			Text  string
			Type  string
		}
		var events []TimelineEvent
		for _, f := range frameAnalyses {
			events = append(events, TimelineEvent{Start: f.StartTimestamp, End: f.EndTimestamp, Text: f.Description, Type: "frame"})
		}
		for _, seg := range asrSegments {
			events = append(events, TimelineEvent{Start: seg.Start, End: seg.End, Text: seg.Text, Type: "audio"})
		}
		sort.Slice(events, func(i, j int) bool {
			return events[i].Start < events[j].Start
		})

		var combinedBuilder strings.Builder
		for _, ev := range events {
			if ev.Type == "frame" {
				if ev.Start == ev.End {
					combinedBuilder.WriteString(fmt.Sprintf("[%.2fs] Visual: %s\n", ev.Start, ev.Text))
				} else {
					combinedBuilder.WriteString(fmt.Sprintf("[%.2fs - %.2fs] Visual: %s\n", ev.Start, ev.End, ev.Text))
				}
			} else {
				combinedBuilder.WriteString(fmt.Sprintf("[%.2fs - %.2fs] Audio: %s\n", ev.Start, ev.End, ev.Text))
			}
		}
		combinedInput = combinedBuilder.String()

		if err != nil || kb == nil {
			logger.Warnf(ctx, "[VideoMultimodal] Failed to get KB for summary: %v", err)
			videoSummary = combinedInput
		} else {
			chatMdl, err := s.modelService.GetChatModel(ctx, kb.SummaryModelID)
			if err != nil || chatMdl == nil {
				logger.Warnf(ctx, "[VideoMultimodal] Failed to get chat model for summary (modelID=%s): %v", kb.SummaryModelID, err)
				videoSummary = combinedInput
			} else {
				// Limit combinedInput to avoid context length overflow.
				// Using ~32000 runes to be safe for typical models.
				if utf8.RuneCountInString(combinedInput) > 32000 {
					runes := []rune(combinedInput)
					combinedInput = string(runes[:32000]) + "\n...[truncated due to length]..."
				}

				systemPrompt := strings.ReplaceAll(vlmSummarySystemPromptTemplate, "{{language}}", lang)
				userPrompt := strings.ReplaceAll(vlmSummaryUserPromptTemplate, "{{descriptions}}", combinedInput)

				logger.Debugf(ctx, "[VideoMultimodal] Generating video summary with model %s, input length: %d chars", kb.SummaryModelID, len(combinedInput))
				resp, sumErr := chatMdl.Chat(ctx, []chat.Message{
					{Role: "system", Content: systemPrompt},
					{Role: "user", Content: userPrompt},
				}, nil)

				if sumErr != nil || resp == nil {
					logger.Warnf(ctx, "[VideoMultimodal] Video summarization failed: %v", sumErr)
					videoSummary = combinedInput
				} else {
					videoSummary = resp.Content
				}
			}
		}
	}

	if kb == nil {
		var err error
		kb, err = s.kbService.GetKnowledgeBaseByIDOnly(ctx, payload.KnowledgeBaseID)
		if err != nil {
			logger.Warnf(ctx, "[VideoMultimodal] Failed to get KB for video chunking: %v", err)
		}
	}

	videoInfo := types.VideoInfo{
		URL: payload.VideoURL,
	}
	videoInfoJSON, _ := json.Marshal([]types.VideoInfo{videoInfo})

	newChunks := s.buildVideoChunks(payload, combinedInput, videoSummary, string(videoInfoJSON), kb)

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

// transcribeAudio transcribes audio from a video using ASR.
func (s *VideoMultimodalService) transcribeAudio(ctx context.Context, audioBytes []byte, kbID string, fileName string) (*asr.TranscriptionResult, error) {
	asrMdl, err := s.resolveASR(ctx, kbID)
	if err != nil {
		return nil, fmt.Errorf("resolve ASR: %w", err)
	}
	return asrMdl.Transcribe(ctx, audioBytes, fileName)
}

// buildVideoChunks creates child chunks for video multimodal analysis results:
//   - Video summary (split into multiple chunks if too long)
//   - Timeline combined input (split into multiple chunks if too long)
func (s *VideoMultimodalService) buildVideoChunks(
	payload types.VideoMultimodalPayload,
	combinedInput string,
	videoSummary string,
	videoInfoJSON string,
	kb *types.KnowledgeBase,
) []*types.Chunk {
	var newChunks []*types.Chunk

	var parentContentBuilder strings.Builder
	if videoSummary != "" {
		parentContentBuilder.WriteString("视频摘要：\n")
		parentContentBuilder.WriteString(videoSummary)
		parentContentBuilder.WriteString("\n\n")
	}
	if combinedInput != "" {
		parentContentBuilder.WriteString("详细时间轴解析：\n")
		parentContentBuilder.WriteString(combinedInput)
	}
	parentContent := strings.TrimSpace(parentContentBuilder.String())
	if parentContent == "" {
		parentContent = "该视频未解析出有效内容"
	}

	// Construct a Parent Chunk representing the entire video
	parentChunkID := uuid.New().String()
	newChunks = append(newChunks, &types.Chunk{
		ID:              parentChunkID,
		TenantID:        payload.TenantID,
		KnowledgeID:     payload.KnowledgeID,
		KnowledgeBaseID: payload.KnowledgeBaseID,
		Content:         parentContent,
		ChunkType:       types.ChunkTypeParentText,
		ParentChunkID:   payload.ChunkID, // Link back to the original text chunk if extracted from a document
		IsEnabled:       true,
		Flags:           types.ChunkFlagRecommended,
		VideoInfo:       videoInfoJSON,
		CreatedAt:       time.Now(),
		UpdatedAt:       time.Now(),
	})

	var chunkCfg chunker.SplitterConfig
	if kb != nil {
		baseCfg := buildSplitterConfig(kb)
		if kb.ChunkingConfig.EnableParentChild {
			// If parent-child is globally enabled, these chunks act as child chunks
			// for the newly created video parent chunk.
			_, childCfg := buildParentChildConfigs(kb.ChunkingConfig, baseCfg)
			chunkCfg = childCfg
		} else {
			chunkCfg = baseCfg
		}
	} else {
		chunkCfg = chunker.SplitterConfig{
			ChunkSize:    512,
			ChunkOverlap: 64,
			Separators:   []string{"\n\n", "\n", "。"},
		}
	}

	if combinedInput != "" {
		inputChunks := chunker.SplitText(combinedInput, chunkCfg)
		for _, sc := range inputChunks {
			newChunks = append(newChunks, &types.Chunk{
				ID:              uuid.New().String(),
				TenantID:        payload.TenantID,
				KnowledgeID:     payload.KnowledgeID,
				KnowledgeBaseID: payload.KnowledgeBaseID,
				Content:         sc.Content,
				ChunkType:       types.ChunkTypeText,
				ParentChunkID:   parentChunkID,
				IsEnabled:       true,
				Flags:           types.ChunkFlagRecommended,
				VideoInfo:       videoInfoJSON,
				CreatedAt:       time.Now(),
				UpdatedAt:       time.Now(),
			})
		}
	}

	if videoSummary != "" {
		summaryChunks := chunker.SplitText(videoSummary, chunkCfg)
		for _, sc := range summaryChunks {
			newChunks = append(newChunks, &types.Chunk{
				ID:              uuid.New().String(),
				TenantID:        payload.TenantID,
				KnowledgeID:     payload.KnowledgeID,
				KnowledgeBaseID: payload.KnowledgeBaseID,
				Content:         sc.Content,
				ChunkType:       types.ChunkTypeText,
				IsEnabled:       true,
				Flags:           types.ChunkFlagRecommended,
				VideoInfo:       videoInfoJSON,
				CreatedAt:       time.Now(),
				UpdatedAt:       time.Now(),
			})
		}
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
