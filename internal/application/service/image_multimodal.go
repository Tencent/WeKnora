package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/application/service/retriever"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/utils/ollama"
	"github.com/Tencent/WeKnora/internal/models/vlm"
	"github.com/Tencent/WeKnora/internal/tracing/langfuse"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	secutils "github.com/Tencent/WeKnora/internal/utils"
	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/redis/go-redis/v9"
)

const (
	vlmOCRPrompt = "<system_prompt>\n" +
		"You are an OCR assistant. Your task is to extract all body text content from this document image and output in pure Markdown format.\n" +
		"</system_prompt>\n\n" +
		"<instructions>\n" +
		"1. Ignore headers and footers.\n" +
		"2. Use Markdown table syntax for tables.\n" +
		"3. Use LaTeX format for formulas (wrapped with $ or $$).\n" +
		"4. Organize content in the original reading order.\n" +
		"5. Output ONLY the extracted text content. Do NOT include any HTML tags, reasoning, or unrelated comments.\n" +
		"6. If there is absolutely no recognizable text content in the image, reply ONLY with: No text content.\n" +
		"</instructions>"
	vlmOCRScannedPDFPrompt = "<system_prompt>\n" +
		"You are an OCR and document layout extraction assistant. The input image is a page from a scanned PDF document.\n" +
		"Your task is to carefully extract all text and layout structure from the image, and output the result in pure Markdown format.\n" +
		"</system_prompt>\n\n" +
		"<instructions>\n" +
		"1. Ignore headers, footers, and page numbers.\n" +
		"2. Preserve the original document's paragraph and hierarchical structure as much as possible.\n" +
		"3. If there are tables, use Markdown table syntax to represent them.\n" +
		"4. If there are mathematical formulas, use LaTeX format wrapped in $ or $$.\n" +
		"5. Output ONLY the extracted text content. Do NOT include any HTML tags, reasoning, or unrelated comments.\n" +
		"6. If there is absolutely no recognizable text content in the image, reply ONLY with: No text content.\n" +
		"</instructions>"
)

func buildVLMCaptionPrompt(ctx context.Context, cfg types.VLMConfig) string {
	language := strings.TrimSpace(cfg.DescriptionLanguage)
	if language == "" {
		language = types.LanguageNameFromContext(ctx)
	}
	prompt := fmt.Sprintf("Provide a brief and concise description of the main content of the image in %s.", language)
	return types.AppendCustomPromptInstructions(prompt, cfg.CustomInstructions, "image_description")
}

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
	ownership      retriever.TenantStoreOwnership
	ollamaService  *ollama.OllamaService
	taskEnqueuer   interfaces.TaskEnqueuer
	redisClient    *redis.Client
	// fileSvc is the globally configured default FileService used as a fallback
	// when the tenant-scoped storage config cannot produce a usable service
	// (e.g. images were saved using the global MINIO_* env vars while the
	// tenant's StorageEngineConfig.MinIO is empty). Mirrors the write-side
	// fallback in knowledgeService.resolveFileService.
	fileSvc         interfaces.FileService
	storageResolver interfaces.StorageBackendResolver
	// resourceCatalog resolves resource:// references to their owning storage
	// backend so multimodal reads target the resource's real backend instead of
	// the knowledge base's currently configured one.
	resourceCatalog interfaces.ResourceCatalog

	// spanTracker records this image's subspan under the parent attempt's
	// multimodal stage. nil-safe — falls back to no-op via tracker().
	spanTracker SpanTracker
}

func NewImageMultimodalService(
	chunkService interfaces.ChunkService,
	modelService interfaces.ModelService,
	kbService interfaces.KnowledgeBaseService,
	knowledgeRepo interfaces.KnowledgeRepository,
	tenantRepo interfaces.TenantRepository,
	retrieveEngine interfaces.RetrieveEngineRegistry,
	ownership retriever.TenantStoreOwnership,
	ollamaService *ollama.OllamaService,
	taskEnqueuer interfaces.TaskEnqueuer,
	redisClient *redis.Client,
	fileSvc interfaces.FileService,
	storageResolver interfaces.StorageBackendResolver,
	resourceCatalog interfaces.ResourceCatalog,
	spanTracker SpanTracker,
) interfaces.TaskHandler {
	return &ImageMultimodalService{
		chunkService:    chunkService,
		modelService:    modelService,
		kbService:       kbService,
		knowledgeRepo:   knowledgeRepo,
		tenantRepo:      tenantRepo,
		retrieveEngine:  retrieveEngine,
		ownership:       ownership,
		ollamaService:   ollamaService,
		taskEnqueuer:    taskEnqueuer,
		redisClient:     redisClient,
		fileSvc:         fileSvc,
		storageResolver: storageResolver,
		resourceCatalog: resourceCatalog,
		spanTracker:     spanTracker,
	}
}

// tracker returns a usable SpanTracker — falls back to a no-op when the
// service was constructed without one.
func (s *ImageMultimodalService) tracker() SpanTracker {
	if s.spanTracker == nil {
		return noopSpanTracker{}
	}
	return s.spanTracker
}

// Handle implements asynq handler for TypeImageMultimodal.
func (s *ImageMultimodalService) Handle(ctx context.Context, task *asynq.Task) error {
	var payload types.ImageMultimodalPayload
	if err := json.Unmarshal(task.Payload(), &payload); err != nil {
		return fmt.Errorf("unmarshal image multimodal payload: %w", err)
	}

	// A payload carries either one image (legacy) or a batch; normalise once
	// here so the rest of the pipeline never branches on the payload shape.
	refs := payload.ImageRefs()
	logger.Infof(ctx, "[ImageMultimodal] Processing %d image(s): knowledge=%s, ocr=%v, caption=%v",
		len(refs), payload.KnowledgeID, payload.EnableOCR, payload.EnableCaption)

	ctx = context.WithValue(ctx, types.TenantIDContextKey, payload.TenantID)
	if payload.Language != "" {
		ctx = context.WithValue(ctx, types.LanguageContextKey, payload.Language)
	}

	// Drop orphaned or user-aborted work before touching VLM. Missing
	// knowledge/KB rows are permanent failures — retrying only burns queue
	// capacity (asynq default MaxRetry=25 on legacy tasks).
	drop, dropErr := s.shouldDropOrphanedMultimodal(ctx, &payload)
	if dropErr != nil {
		return dropErr
	}
	if drop {
		logger.Infof(ctx,
			"[ImageMultimodal] Dropping task chunk=%s knowledge=%s kb=%s image=%s",
			payload.ChunkID, payload.KnowledgeID, payload.KnowledgeBaseID, payload.ImageURL)
		// Still count these images toward the parent finalize gate so a task
		// of dropped orphans cannot strand multimodal:pending forever.
		s.checkAndFinalizeAllImages(ctx, &payload, len(refs))
		return nil
	}

	// Each image in the payload gets its own subspan and its own result map,
	// so a batched task still shows per-image outcomes on the timeline. The
	// pending counter is decremented ONCE per task (see the deferred finalize
	// below) rather than once per image: a task that is retried after a
	// partial success must not have already counted the images it processed
	// before failing, or the parent document would finalize early.
	tracker := s.tracker()
	imgOut := types.JSONMap{}
	var perImageOut []types.JSONMap
	var handleErr error

	// finalize-once semantics: on success we always decrement the parent's
	// pending counter. On failure we only decrement when this is the last
	// asynq retry, so a permanently-failing image cannot leave the parent
	// knowledge stuck in "processing" forever — which was the #1 cause of
	// "stuck parsing" reports. Intermediate retries skip finalize so we don't
	// double-count and prematurely trigger post-process.
	defer func() {
		switch len(perImageOut) {
		case 0:
			// Nothing was processed (e.g. the VLM could not be resolved).
		case 1:
			// Legacy single-image shape: keep the output flat so existing
			// traces read exactly as they did before batching shipped.
			for k, v := range perImageOut[0] {
				imgOut[k] = v
			}
		default:
			imgOut["images"] = perImageOut
			imgOut["image_count"] = len(perImageOut)
		}

		if handleErr == nil || isFinalAsynqAttempt(ctx) {
			s.checkAndFinalizeAllImages(ctx, &payload, len(refs))
		} else {
			logger.Infof(ctx,
				"[ImageMultimodal] Skip finalize on retryable error for %s (will count on last attempt)",
				payload.KnowledgeID)
		}
	}()

	vlmModel, vlmCfg, err := s.resolveVLM(ctx, payload.KnowledgeBaseID, payload.KnowledgeID)
	if err != nil {
		handleErr = fmt.Errorf("resolve VLM: %w", err)
		return handleErr
	}
	// Capture the resolved VLM model id (or "legacy_inline" for the legacy
	// inline-config path) so the trace shows WHICH model handled these images.
	// Without this, debugging "VLM is slow" requires a separate hop to the KB
	// config.
	if id := strings.TrimSpace(vlmCfg.ModelID); id != "" {
		imgOut["vlm_model_id"] = id
	} else {
		imgOut["vlm_model_id"] = "legacy_inline"
	}

	// A task that carries more than one image describes the whole batch in a
	// single request and then runs the per-image pipeline for OCR. A task with
	// one image (the default, and every legacy payload) keeps the historical
	// pipeline untouched: OCR first, then caption, both on the original bytes.
	if len(refs) > 1 {
		perImageOut, handleErr = s.processImageBatch(ctx, &payload, refs, vlmModel, vlmCfg, tracker)
		return handleErr
	}
	if len(refs) == 1 {
		out, imgErr := s.processOneImage(ctx, &payload, refs[0], vlmModel, vlmCfg, tracker, imageProcessInput{})
		if out == nil {
			out = types.JSONMap{}
		}
		perImageOut = append(perImageOut, out)
		handleErr = imgErr
	}
	return handleErr
}

// processImageBatch describes a whole batch in one VLM request, then runs the
// per-image pipeline for OCR and for any image the batch response did not
// cover. Describing N images together is what turns N requests into one; the
// per-image round stays because OCR needs each image at full resolution.
//
// Two failure modes are deliberately distinguished:
//   - a transport/API error on the batch call fails the task so asynq retries
//     it — none of the images were processed, so the retry is a clean redo;
//   - a response that cannot be mapped back onto the inputs does NOT fail the
//     task, because repeating the same prompt would produce the same answer.
//     Instead each unmapped image is described on its own, so a sloppy answer
//     costs a few extra calls rather than the batch's captions.
//
// Round 2 is per image on purpose: OCR is the step where resolution matters
// most, so it is never handed a downscaled copy.
func (s *ImageMultimodalService) processImageBatch(
	ctx context.Context,
	payload *types.ImageMultimodalPayload,
	refs []types.ImageBatchRef,
	vlmModel vlm.VLM,
	vlmCfg types.VLMConfig,
	tracker SpanTracker,
) ([]types.JSONMap, error) {
	outs := make([]types.JSONMap, len(refs))

	// Read every image up front: the batch call needs them all in one request,
	// and a single unreadable file must not cost its siblings their captions.
	imgBytes := make([][]byte, len(refs))
	loaded := make([]int, 0, len(refs))
	for i, ref := range refs {
		outs[i] = types.JSONMap{}
		data, err := s.readImageBytes(ctx, *payload, ref.URL)
		if err != nil {
			logger.Errorf(ctx, "[ImageMultimodal] Skip unreadable image %s: %v", ref.URL, err)
			outs[i]["skipped"] = "unreadable_image"
			outs[i]["read_error"] = err.Error()
			continue
		}
		imgBytes[i] = data
		outs[i]["image_bytes"] = len(data)
		loaded = append(loaded, i)
	}

	descriptions := map[int]string{}
	if len(loaded) > 0 {
		// The describe round works on downscaled copies: measurements showed
		// that a short long-edge cuts prompt tokens to roughly a seventh
		// without changing what the model reports. The per-image round below
		// still gets the original bytes, because OCR is where detail decides
		// the outcome.
		payloads := make([][]byte, 0, len(loaded))
		downscaled := 0
		for _, i := range loaded {
			img := imgBytes[i]
			if small, changed := downscaleForDescribe(img, payload.ClassifyMaxEdge); changed {
				img = small
				downscaled++
			}
			payloads = append(payloads, img)
		}
		if downscaled > 0 {
			logger.Infof(ctx,
				"[ImageMultimodal] Downscaled %d of %d image(s) to max edge %d for the describe round",
				downscaled, len(payloads), payload.ClassifyMaxEdge)
		}

		raw, err := vlmModel.Predict(ctx, payloads, buildBatchImagePrompt(ctx, vlmCfg, len(payloads)))
		if err != nil {
			return outs, fmt.Errorf("describe %d image(s) in one request: %w", len(payloads), err)
		}

		parsed := parseBatchImageResponse(raw, len(payloads))
		for pos, i := range loaded {
			if text, ok := parsed[pos+1]; ok {
				descriptions[i] = text
			}
		}
		logger.Infof(ctx,
			"[ImageMultimodal] One request described %d of %d image(s) for knowledge %s",
			len(descriptions), len(payloads), payload.KnowledgeID)
	}

	for i, ref := range refs {
		if imgBytes[i] == nil {
			continue // unreadable, already recorded above
		}
		desc, hasDesc := descriptions[i]
		out, imgErr := s.processOneImage(ctx, payload, ref, vlmModel, vlmCfg, tracker, imageProcessInput{
			Bytes:      imgBytes[i],
			Caption:    desc,
			HasCaption: hasDesc,
			BatchSize:  len(refs),
			Out:        outs[i],
		})
		if out != nil {
			outs[i] = out
		}
		if imgErr != nil {
			// Fail fast: the batch shares one task, so the retry covers the
			// remaining images instead of half-processing them here.
			return outs, imgErr
		}
	}
	return outs, nil
}

// buildBatchImagePrompt asks for one labelled description block per image so a
// single request can describe a whole batch. The 1-based numbering the model
// echoes back is what maps each block onto its input image.
func buildBatchImagePrompt(ctx context.Context, cfg types.VLMConfig, count int) string {
	language := strings.TrimSpace(cfg.DescriptionLanguage)
	if language == "" {
		language = types.LanguageNameFromContext(ctx)
	}
	prompt := fmt.Sprintf(
		"You are given %d images, in order: image 1 is the first image after this instruction, image 2 the second, and so on.\n"+
			"For each image, write a brief and concise description of its main content in %s.\n\n"+
			"Output exactly one block per image, in ascending order, using this format:\n\n"+
			"### IMAGE <n>\n"+
			"<description>\n\n"+
			"Rules:\n"+
			"- Replace <n> with the image number, starting at 1.\n"+
			"- Produce %d blocks in total: one per image, none merged, none skipped.\n"+
			"- Output only the blocks. No preamble, no summary, no extra commentary.\n",
		count, language, count)
	return types.AppendCustomPromptInstructions(prompt, cfg.CustomInstructions, "image_description")
}

// batchImageBlockRe matches the "### IMAGE <n>" marker emitted by the batch
// prompt. One to six leading '#' characters are accepted so a model rendering
// the marker at a different heading level still maps back correctly.
var batchImageBlockRe = regexp.MustCompile(`(?m)^#{1,6}\s*IMAGE\s+(\d+)\s*$`)

// parseBatchImageResponse maps 1-based image numbers onto the description that
// follows each marker. Missing, out-of-range, and repeated numbers are simply
// absent from the result: the caller describes those images on their own, so an
// imprecise answer never discards a usable description.
func parseBatchImageResponse(raw string, count int) map[int]string {
	out := make(map[int]string, count)
	locs := batchImageBlockRe.FindAllStringSubmatchIndex(raw, -1)
	for pos, loc := range locs {
		n, err := strconv.Atoi(raw[loc[2]:loc[3]])
		if err != nil || n < 1 || n > count {
			continue
		}
		bodyStart := loc[1]
		bodyEnd := len(raw)
		if pos+1 < len(locs) {
			bodyEnd = locs[pos+1][0]
		}
		text := strings.TrimSpace(raw[bodyStart:bodyEnd])
		text = strings.TrimSpace(strings.TrimLeft(text, "-*# "))
		if text == "" {
			continue
		}
		if _, dup := out[n]; dup {
			// A repeated number means the model lost track of the numbering;
			// the first block is the one that follows the input order.
			continue
		}
		out[n] = text
	}
	return out
}

// processOneImage runs the multimodal pipeline (OCR, then caption) for a single
// image and persists the derived child chunks. It returns the per-image trace
// map; a non-nil error means the failure is worth retrying the whole task for.
// Unreadable images are skipped (nil error) so one bad file cannot fail a batch
// of otherwise healthy images.
//
// A payload may carry one image (legacy) or a batch, so this function reads the
// image through ref and never touches payload.ImageURL / payload.ChunkID.
// imageProcessInput carries work the caller has already done for one image so
// the per-image pipeline does not repeat it. The zero value means "nothing
// precomputed", which is exactly the historical single-image behaviour.
type imageProcessInput struct {
	// Bytes is the image the caller already loaded. A batched caller reads
	// every image up front to build its single request, so handing the bytes
	// over here avoids reading the same object twice.
	Bytes []byte
	// Caption is the description the batch response already produced for this
	// image. When HasCaption is true, no per-image describe call is made.
	Caption    string
	HasCaption bool
	// BatchSize is how many images shared this task's batch request. Values
	// above 1 mark the image as a batch member on the trace.
	BatchSize int
	// Out receives the per-image trace map. Nil uses a fresh map.
	Out types.JSONMap
}

func (s *ImageMultimodalService) processOneImage(
	ctx context.Context,
	payload *types.ImageMultimodalPayload,
	ref types.ImageBatchRef,
	vlmModel vlm.VLM,
	vlmCfg types.VLMConfig,
	tracker SpanTracker,
	in imageProcessInput,
) (types.JSONMap, error) {
	out := in.Out
	if out == nil {
		out = types.JSONMap{}
	}

	// Open a per-image subspan under the parent attempt's multimodal stage.
	// If the parent stage row is missing (legacy in-flight task, or the
	// upstream code shipped without span tracking), the tracker is a no-op so
	// we silently fall back to the existing counter-based finalize semantics.
	var imgSpan *Span
	if payload.Attempt > 0 {
		parent := tracker.LookupStage(ctx, payload.KnowledgeID, payload.Attempt, types.StageMultimodal)
		if parent != nil {
			name := fmt.Sprintf("multimodal.image[%d]", ref.Index)
			spanInput := types.JSONMap{
				"image_url":         ref.URL,
				"image_source_type": payload.ImageSourceType,
				"enable_ocr":        payload.EnableOCR,
				"enable_caption":    payload.EnableCaption,
				"parent_chunk_id":   ref.ChunkID,
			}
			if in.BatchSize > 1 {
				spanInput["batched"] = true
				spanInput["batch_size"] = in.BatchSize
			}
			imgSpan = tracker.BeginSubSpan(ctx, parent, name, types.SpanKindGeneration, spanInput)
		}
	}

	var handleErr error
	defer func() {
		// Finalize the image subspan with the actual outcome — not the
		// finalize-counter outcome. The counter logic counts a "tried" image
		// regardless of inner success; the span surface tells the UI whether
		// THIS specific image worked.
		if imgSpan == nil {
			return
		}
		if handleErr == nil {
			tracker.EndSpan(ctx, imgSpan, out)
		} else if isFinalAsynqAttempt(ctx) {
			tracker.FailSpan(ctx, imgSpan,
				"MULTIMODAL_VLM_FAILED",
				handleErr.Error(),
				handleErr)
		}
	}()

	// Read image bytes. A provider:// URL must be resolved via FileService —
	// it must NEVER be handed to the HTTP downloader (which would fail with
	// "unsupported URL scheme"). On unrecoverable read failure for a single
	// image, skip it (the deferred finalize still counts it).
	imgBytes := in.Bytes
	if imgBytes == nil {
		var readErr error
		imgBytes, readErr = s.readImageBytes(ctx, *payload, ref.URL)
		if readErr != nil {
			logger.Errorf(ctx, "[ImageMultimodal] Skip unreadable image %s: %v", ref.URL, readErr)
			out["skipped"] = "unreadable_image"
			out["read_error"] = readErr.Error()
			return out, nil
		}
	}
	out["image_bytes"] = len(imgBytes)

	imageInfo := types.ImageInfo{
		URL:         ref.URL,
		OriginalURL: ref.URL,
	}

	if payload.EnableOCR {
		prompt := vlmOCRPrompt
		if payload.ImageSourceType == "scanned_pdf" {
			prompt = vlmOCRScannedPDFPrompt
			logger.Infof(ctx, "[ImageMultimodal] Using scanned PDF prompt for OCR: %s", ref.URL)
			out["ocr_prompt"] = "scanned_pdf"
		} else {
			out["ocr_prompt"] = "default"
		}
		prompt = types.AppendCustomPromptInstructions(prompt, vlmCfg.CustomInstructions, "image_ocr")

		ocrText, ocrErr := vlmModel.Predict(ctx, [][]byte{imgBytes}, prompt)
		if ocrErr != nil {
			logger.Warnf(ctx, "[ImageMultimodal] OCR failed for %s: %v", ref.URL, ocrErr)
			out["ocr_error"] = ocrErr.Error()
		} else {
			ocrText = sanitizeOCRText(ocrText)
			if ocrText != "" {
				imageInfo.OCRText = ocrText
				out["ocr_chars"] = len([]rune(ocrText))
				out["ocr_preview"] = previewText(ocrText, 200)
			} else {
				logger.Warnf(ctx, "[ImageMultimodal] OCR returned empty/invalid content for %s, discarded", ref.URL)
				out["ocr_chars"] = 0
				out["ocr_skipped"] = "empty_or_invalid"
			}
		}
	}

	if in.HasCaption {
		// A batched caller already described this image in its single request;
		// reuse that text instead of paying for a second call.
		if in.Caption != "" {
			imageInfo.Caption = in.Caption
			out["caption_chars"] = len([]rune(in.Caption))
			out["caption_preview"] = previewText(in.Caption, 200)
		}
	} else {
		caption, capErr := vlmModel.Predict(ctx, [][]byte{imgBytes}, buildVLMCaptionPrompt(ctx, vlmCfg))
		if capErr != nil {
			logger.Warnf(ctx, "[ImageMultimodal] Caption failed for %s: %v", ref.URL, capErr)
			out["caption_error"] = capErr.Error()
		} else if caption != "" {
			imageInfo.Caption = caption
			out["caption_chars"] = len([]rune(caption))
			out["caption_preview"] = previewText(caption, 200)
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
			ParentChunkID:   ref.ChunkID,
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
			ParentChunkID:   ref.ChunkID,
			IsEnabled:       true,
			Flags:           types.ChunkFlagRecommended,
			ImageInfo:       string(imageInfoJSON),
			CreatedAt:       time.Now(),
			UpdatedAt:       time.Now(),
		})
	}
	out["chunks_created"] = len(newChunks)

	if len(newChunks) == 0 {
		// Deferred finalize will count this image on success.
		out["skipped"] = "no_extracted_content"
		return out, nil
	}

	// Persist chunks
	if err := s.chunkService.GetRepository().CreateChunks(ctx, newChunks); err != nil {
		handleErr = fmt.Errorf("create multimodal chunks: %w", err)
		return out, handleErr
	}
	for _, c := range newChunks {
		logger.Infof(ctx, "[ImageMultimodal] Created %s chunk %s for image %s, len=%d",
			c.ChunkType, c.ID, ref.URL, len(c.Content))
	}

	// Index chunks so they can be retrieved
	s.indexChunks(ctx, *payload, newChunks)
	out["indexed"] = true

	return out, nil
}

// shouldDropOrphanedMultimodal reports whether the task should exit without
// retrying. True for user-cancelled/deleting knowledge, or when the parent
// knowledge / knowledge-base row no longer exists (deleted while queue entries
// survived).
func (s *ImageMultimodalService) shouldDropOrphanedMultimodal(
	ctx context.Context, payload *types.ImageMultimodalPayload,
) (bool, error) {
	if payload.KnowledgeID != "" && s.knowledgeRepo != nil {
		k, err := s.knowledgeRepo.GetKnowledgeByIDOnly(ctx, payload.KnowledgeID)
		if errors.Is(err, repository.ErrKnowledgeNotFound) {
			return true, nil
		}
		if err != nil {
			return false, err
		}
		switch k.ParseStatus {
		case types.ParseStatusCancelled, types.ParseStatusDeleting:
			return true, nil
		}
	}
	if payload.KnowledgeBaseID != "" && s.kbService != nil {
		kb, err := s.kbService.GetKnowledgeBaseByIDOnly(ctx, payload.KnowledgeBaseID)
		if errors.Is(err, repository.ErrKnowledgeBaseNotFound) {
			return true, nil
		}
		if err != nil {
			return false, err
		}
		if kb == nil {
			return true, nil
		}
	}
	return false, nil
}

// isFinalAsynqAttempt reports whether the current task context belongs to the
// last retry attempt before Asynq (or the Lite executor) archives the task. We use
// this to flip multimodal finalize semantics: during normal retries we skip
// counter decrement (the retry might still succeed), but on the final attempt
// we count the image regardless of outcome so a permanently-failing image
// cannot pin the parent knowledge in "processing" forever.
//
// Returns false when the values are unavailable (e.g. when the handler is
// invoked outside an asynq worker, as in unit tests). Treating that case as
// "not final" keeps test ergonomics — tests should drive finalize explicitly.
func isFinalAsynqAttempt(ctx context.Context) bool {
	retried, ok := asynq.GetRetryCount(ctx)
	if ok {
		maxRetry, maxRetryOK := asynq.GetMaxRetry(ctx)
		if maxRetryOK {
			return retried >= maxRetry
		}
	}
	retried, maxRetry, ok := types.TaskRetryMetadataFromContext(ctx)
	return ok && retried >= maxRetry
}

// indexChunks indexes the newly created multimodal chunks into the retrieval engine
// so they can participate in semantic search.
func (s *ImageMultimodalService) indexChunks(ctx context.Context, payload types.ImageMultimodalPayload, chunks []*types.Chunk) {
	kb, err := s.kbService.GetKnowledgeBaseByIDOnly(ctx, payload.KnowledgeBaseID)
	if err != nil || kb == nil {
		logger.Warnf(ctx, "[ImageMultimodal] Failed to get KB for indexing: %v", err)
		return
	}

	// Skip vector/keyword indexing when the KB has no embedding-based pipeline enabled
	// (e.g. Wiki-only KBs). Without this check, GetEmbeddingModel would fail because
	// EmbeddingModelID is intentionally empty for such KBs. The multimodal chunks
	// themselves are already persisted in the DB above, so skipping index here is safe.
	if !kb.NeedsEmbeddingModel() {
		logger.Infof(ctx, "[ImageMultimodal] Vector/keyword indexing disabled for KB %s, skipping index for %d multimodal chunks",
			kb.ID, len(chunks))
		// Still mark chunks as indexed so downstream finalization sees a consistent state.
		for _, chunk := range chunks {
			dbChunk, gerr := s.chunkService.GetChunkByIDOnly(ctx, chunk.ID)
			if gerr != nil {
				logger.Warnf(ctx, "[ImageMultimodal] Failed to fetch chunk %s for status update: %v", chunk.ID, gerr)
				continue
			}
			dbChunk.Status = int(types.ChunkStatusIndexed)
			if uerr := s.chunkService.GetRepository().UpdateChunk(ctx, dbChunk); uerr != nil {
				logger.Warnf(ctx, "[ImageMultimodal] Failed to update chunk %s status to indexed: %v", chunk.ID, uerr)
			}
		}
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
	// The factory's unbound path reads TenantInfo from ctx; make sure it's there.
	ctx = context.WithValue(ctx, types.TenantInfoContextKey, tenantInfo)

	// Resolve engine via the factory using the KB's VectorStore binding
	// (nil → tenant effective engines fallback; verified tenant ownership otherwise).
	engine, err := retriever.CreateRetrieveEngineForKB(
		ctx, s.retrieveEngine, s.ownership, payload.TenantID, kb.VectorStoreID)
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
		if err := s.chunkService.GetRepository().UpdateChunk(ctx, dbChunk); err != nil {
			logger.Warnf(ctx, "[ImageMultimodal] Failed to update chunk %s status to indexed: %v", chunk.ID, err)
		}
	}

	logger.Infof(ctx, "[ImageMultimodal] Indexed %d multimodal chunks for knowledge %s", len(chunks), payload.KnowledgeID)
}

// resolveVLM creates a vlm.VLM instance for the given knowledge base,
// supporting both new-style (ModelID) and legacy (inline BaseURL) configs.
// Per-upload process_overrides on the knowledge entry take precedence over KB defaults.
func (s *ImageMultimodalService) resolveVLM(ctx context.Context, kbID, knowledgeID string) (vlm.VLM, types.VLMConfig, error) {
	kb, err := s.kbService.GetKnowledgeBaseByIDOnly(ctx, kbID)
	if err != nil {
		return nil, types.VLMConfig{}, fmt.Errorf("get knowledge base %s: %w", kbID, err)
	}
	if kb == nil {
		return nil, types.VLMConfig{}, fmt.Errorf("knowledge base %s not found", kbID)
	}

	var processOverrides *types.KnowledgeProcessOverrides
	if knowledgeID != "" && s.knowledgeRepo != nil {
		if k, kerr := s.knowledgeRepo.GetKnowledgeByIDOnly(ctx, knowledgeID); kerr == nil && k != nil {
			processOverrides, _ = k.ProcessOverrides()
		}
	}
	vlmCfg := ResolveProcessConfig(kb, processOverrides).VLMConfig
	if !vlmCfg.IsEnabled() {
		return nil, types.VLMConfig{}, fmt.Errorf("VLM is not enabled for knowledge base %s", kbID)
	}

	// New-style: resolve model through ModelService
	if vlmCfg.ModelID != "" {
		model, err := s.modelService.GetVLMModel(ctx, vlmCfg.ModelID)
		return model, vlmCfg, err
	}

	// Legacy: create VLM from inline config
	model, err := vlm.NewVLMFromLegacyConfig(vlmCfg, s.ollamaService)
	return model, vlmCfg, err
}

// resolveFileServiceForPayload resolves tenant/KB scoped file service for reading provider:// URLs.
// Falls back to the globally configured default FileService when the tenant's
// StorageEngineConfig does not carry a usable configuration for the URL's provider.
// This mirrors the write-side fallback in knowledgeService.resolveFileService
// and is required because images can be saved using global STORAGE_TYPE/MINIO_*
// env vars while tenant.StorageEngineConfig.MinIO is left empty (issue #1282).
func (s *ImageMultimodalService) resolveFileServiceForPayload(ctx context.Context, payload types.ImageMultimodalPayload) interfaces.FileService {
	tenant, err := s.tenantRepo.GetTenantByID(ctx, payload.TenantID)
	if err != nil || tenant == nil {
		logger.Warnf(ctx, "[ImageMultimodal] GetTenantByID failed: tenant=%d err=%v", payload.TenantID, err)
		return s.fileSvc
	}

	backendID, _, _ := types.ParseStorageBackendPath(payload.ImageURL)
	provider := types.ParseProviderScheme(payload.ImageURL)
	// A resource:// reference carries no provider/backend in the URL itself; the
	// authoritative backend lives on the stored resource record. Using the KB's
	// currently configured backend here would break reads when the resource was
	// stored on a different backend (multi-backend / post-migration).
	if _, isResourceRef := types.ParseResourcePath(payload.ImageURL); isResourceRef && s.resourceCatalog != nil {
		if resource, resErr := s.resourceCatalog.Resolve(ctx, payload.ImageURL); resErr != nil {
			logger.Warnf(ctx, "[ImageMultimodal] resolve resource reference failed: url=%s err=%v", payload.ImageURL, resErr)
		} else if resource != nil {
			backendID = resource.StorageBackendID
			provider = strings.ToLower(strings.TrimSpace(resource.Provider))
		}
	}
	if provider == "" {
		kb, kbErr := s.kbService.GetKnowledgeBaseByIDOnly(ctx, payload.KnowledgeBaseID)
		if kbErr != nil {
			logger.Warnf(ctx, "[ImageMultimodal] GetKnowledgeBaseByIDOnly failed: kb=%s err=%v", payload.KnowledgeBaseID, kbErr)
		} else if kb != nil {
			provider = strings.ToLower(strings.TrimSpace(kb.GetStorageProvider()))
			if backendID == "" && kb.StorageBackendID != nil {
				backendID = *kb.StorageBackendID
			}
		}
	}

	if s.storageResolver == nil {
		return s.fileSvc
	}

	baseDir := strings.TrimSpace(os.Getenv("LOCAL_STORAGE_BASE_DIR"))
	logger.Infof(ctx, "[ImageMultimodal] resolving file service: tenant=%d provider=%q LOCAL_STORAGE_BASE_DIR=%q imageURL=%s",
		payload.TenantID, provider, baseDir, payload.ImageURL)
	fileSvc, _, svcErr := s.storageResolver.ResolveFileService(ctx, tenant, backendID, provider, baseDir)
	if svcErr != nil {
		logger.Warnf(ctx, "[ImageMultimodal] resolve file service failed (falling back to default): tenant=%d provider=%s err=%v",
			payload.TenantID, provider, svcErr)
		return s.fileSvc
	}
	return fileSvc
}

// readImageBytes loads the image bytes for one image of a multimodal payload.
//   - For provider:// URLs (local://, minio://, s3://, cos://, ...) it reads via
//     the resolved FileService and NEVER falls back to HTTP — handing a
//     provider:// URL to the HTTP downloader is what caused issue #1282.
//   - For legacy in-flight payloads with ImageLocalPath set, it tries the local
//     file before falling back to the URL.
//   - For plain http(s):// URLs it uses the SSRF-safe downloader.
//
// imageURL is passed explicitly (instead of read from payload.ImageURL) because
// a batched payload carries several images and only the first one is mirrored
// onto the legacy single-image fields.
func (s *ImageMultimodalService) readImageBytes(
	ctx context.Context, payload types.ImageMultimodalPayload, imageURL string,
) ([]byte, error) {
	_, isResourceRef := types.ParseResourcePath(imageURL)
	if isResourceRef || types.ParseProviderScheme(imageURL) != "" {
		fileSvc := s.resolveFileServiceForPayload(ctx, payload)
		if fileSvc == nil {
			return nil, fmt.Errorf("no file service available for %s", imageURL)
		}
		reader, err := fileSvc.GetFile(ctx, imageURL)
		if err != nil {
			return nil, fmt.Errorf("file service get %s: %w", imageURL, err)
		}
		defer reader.Close()
		data, err := io.ReadAll(reader)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", imageURL, err)
		}
		return data, nil
	}

	if payload.ImageLocalPath != "" {
		if data, err := os.ReadFile(payload.ImageLocalPath); err == nil {
			return data, nil
		} else {
			logger.Warnf(ctx, "[ImageMultimodal] Local file %s not available (%v), falling back to URL", payload.ImageLocalPath, err)
		}
	}

	data, err := downloadImageFromURL(imageURL)
	if err != nil {
		return nil, fmt.Errorf("download %s: %w", imageURL, err)
	}
	logger.Infof(ctx, "[ImageMultimodal] Image downloaded from URL, len=%d", len(data))
	return data, nil
}

// downloadImageFromURL downloads image bytes from an HTTP(S) URL.
func downloadImageFromURL(imageURL string) ([]byte, error) {
	return secutils.DownloadBytes(imageURL)
}

// checkAndFinalizeAllImages decrements the parent's pending-image counter by
// count (one task may cover a whole batch) and enqueues post-process once the
// counter reaches zero. count <= 0 is a no-op: an empty payload must not
// consume someone else's pending slot.
func (s *ImageMultimodalService) checkAndFinalizeAllImages(
	ctx context.Context, payload *types.ImageMultimodalPayload, count int,
) {
	if count <= 0 {
		return
	}
	if s.redisClient == nil {
		s.enqueueKnowledgePostProcessTask(ctx, *payload)
		return
	}

	redisKey := fmt.Sprintf("multimodal:pending:%s", payload.KnowledgeID)

	pendingCount, err := s.redisClient.DecrBy(ctx, redisKey, int64(count)).Result()
	if err != nil && err != redis.Nil {
		// Redis hiccup must not strand the parent knowledge. Best-effort:
		// enqueue post-process anyway. KnowledgePostProcess is idempotent
		// (it transitions parse_status processing → completed under a row
		// guard), so a duplicate triggered by a sibling image is harmless.
		// The alternative — silently returning — is what produced the
		// "permanently stuck" reports we are fixing here.
		logger.Warnf(ctx,
			"[ImageMultimodal] Decrement failed for %s (%v); fallback-enqueueing post-process",
			payload.KnowledgeID, err)
		s.enqueueKnowledgePostProcessTask(ctx, *payload)
		return
	}

	if pendingCount <= 0 {
		logger.Infof(ctx, "[ImageMultimodal] All images processed for knowledge %s. Finalizing...", payload.KnowledgeID)
		s.redisClient.Del(ctx, redisKey)

		s.enqueueKnowledgePostProcessTask(ctx, *payload)
	}
}

func (s *ImageMultimodalService) enqueueKnowledgePostProcessTask(ctx context.Context, payload types.ImageMultimodalPayload) {
	if s.taskEnqueuer == nil {
		return
	}

	taskPayload := types.KnowledgePostProcessPayload{
		TenantID:        payload.TenantID,
		KnowledgeID:     payload.KnowledgeID,
		KnowledgeBaseID: payload.KnowledgeBaseID,
		Language:        payload.Language,
	}
	langfuse.InjectTracing(ctx, &taskPayload)
	payloadBytes, err := json.Marshal(taskPayload)
	if err != nil {
		logger.Warnf(ctx, "[ImageMultimodal] Failed to marshal post process payload: %v", err)
		return
	}

	task := asynq.NewTask(types.TypeKnowledgePostProcess, payloadBytes,
		knowledgePostProcessTaskOptions()...)
	if _, err := s.taskEnqueuer.Enqueue(task); err != nil {
		logger.Warnf(ctx, "[ImageMultimodal] Failed to enqueue post process task for %s: %v", payload.KnowledgeID, err)
	} else {
		logger.Infof(ctx, "[ImageMultimodal] Enqueued post process task for %s", payload.KnowledgeID)
	}
}
