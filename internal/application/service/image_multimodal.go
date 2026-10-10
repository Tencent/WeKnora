package service

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/application/service/retriever"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/embedding"
	"github.com/Tencent/WeKnora/internal/models/imageprep"
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
	vlmOCRPrompt = "You are an OCR assistant. Transcribe only text that is visibly present in the image, " +
		"preserving its original language.\n" +
		"The image may be a screenshot, photograph, diagram, or document. For scattered labels, " +
		"buttons, scores, and other UI text, output plain text in reading order, one item per line. " +
		"Do not force this content into a table.\n" +
		"Use Markdown tables only when the image contains a real table with readable cells. Never " +
		"output empty table cells, invented layout, repeated filler, or descriptions of the image.\n" +
		"Preserve readable numbers and symbols. Do not guess unreadable text. Stop after all visible " +
		"text has been transcribed; repeat text only when it visibly appears more than once.\n" +
		"Output only the extracted text, without explanations, HTML tags, or code fences. If no text " +
		"is recognizable, reply only: No text content."
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

// buildVLMOCRPrompt returns the system-owned OCR prompt for the given image
// source type. cfg is part of the signature and deliberately unused: knowledge
// base custom instructions must never reach the OCR prompt — free-form
// business rules compete with the "No text content" output contract and turn
// decorative images into poisoned, vectorized image_ocr chunks. Custom OCR
// guidance, if ever needed, deserves a dedicated OCR instruction field rather
// than reusing CustomInstructions.
func buildVLMOCRPrompt(sourceType string, _ types.VLMConfig) string {
	if sourceType == "scanned_pdf" {
		return vlmOCRScannedPDFPrompt
	}
	return vlmOCRPrompt
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
func (s *ImageMultimodalService) Handle(ctx context.Context, task *asynq.Task) (retErr error) {
	var payload types.ImageMultimodalPayload
	if err := json.Unmarshal(task.Payload(), &payload); err != nil {
		return fmt.Errorf("unmarshal image multimodal payload: %w", err)
	}

	logger.Infof(ctx,
		"[ImageMultimodal] Processing image: chunk=%s, url=%s, ocr=%v, caption=%v, attrs=%v",
		payload.ChunkID, payload.ImageURL, payload.EnableOCR, payload.EnableCaption, payload.ImageAttrsEnabled)

	ctx = context.WithValue(ctx, types.TenantIDContextKey, payload.TenantID)
	if payload.Language != "" {
		ctx = context.WithValue(ctx, types.LanguageContextKey, payload.Language)
	}

	// A reparse re-seeded the fan-in counter for its own images. Counting
	// this stale image would drain a slot of the new run and finalize it
	// before its images are done.
	if attemptSuperseded(ctx, s.tracker(), payload.KnowledgeID, payload.Attempt) {
		logger.Infof(ctx, "[ImageMultimodal] Attempt %d of %s superseded, dropping image %s",
			payload.Attempt, payload.KnowledgeID, payload.ImageURL)
		return nil
	}

	// Drop orphaned or user-aborted work before touching VLM. Missing
	// knowledge/KB rows are permanent failures — retrying only burns queue
	// capacity (asynq default MaxRetry=25 on legacy tasks).
	drop, dropErr := s.shouldDropOrphanedMultimodal(ctx, &payload)
	if dropErr != nil {
		if isFinalAsynqAttempt(ctx) {
			// No retry follows, so this image must still be counted or
			// the parent never reaches post-process.
			if err := s.checkAndFinalizeAllImages(ctx, payload); err != nil {
				return errors.Join(dropErr, err)
			}
		}
		return dropErr
	}
	if drop {
		logger.Infof(ctx,
			"[ImageMultimodal] Dropping task chunk=%s knowledge=%s kb=%s image=%s",
			payload.ChunkID, payload.KnowledgeID, payload.KnowledgeBaseID, payload.ImageURL)
		// Still count this image toward the parent finalize gate so a task
		// of dropped orphans cannot strand multimodal:pending forever.
		return s.checkAndFinalizeAllImages(ctx, payload)
	}

	tracker := s.tracker()
	// The trace map is shared with the per-image stage below: fields Handle
	// knows before the pipeline starts (which VLM ran) and fields the pipeline
	// discovers (attributes, policy, caption/OCR outcome) land on the same span.
	imgOut := types.JSONMap{}

	// finalize-once semantics: on success we always decrement the parent's
	// pending counter. On failure we only decrement when this is the last
	// asynq retry, so a permanently-failing image cannot leave the parent
	// knowledge stuck in "processing" forever — which was the #1 cause of
	// "stuck parsing" reports. Intermediate retries skip finalize so we don't
	// double-count and prematurely trigger post-process.
	var handleErr error
	defer func() {
		if handleErr == nil || isFinalAsynqAttempt(ctx) {
			if err := s.checkAndFinalizeAllImages(ctx, payload); err != nil && retErr == nil {
				retErr = err
			}
		} else {
			logger.Infof(ctx,
				"[ImageMultimodal] Skip finalize on retryable error for %s (will count on last attempt)",
				payload.ImageURL)
		}
	}()

	vlmModel, vlmCfg, err := s.resolveVLM(ctx, payload.KnowledgeBaseID, payload.KnowledgeID)
	if err != nil {
		handleErr = fmt.Errorf("resolve VLM: %w", err)
		return handleErr
	}
	// Capture the resolved VLM model id (or "legacy_inline" for the legacy
	// inline-config path) so the trace shows WHICH model handled this image.
	// Without this, debugging "VLM is slow" requires a separate hop to the KB
	// config.
	if id := strings.TrimSpace(vlmCfg.ModelID); id != "" {
		imgOut["vlm_model_id"] = id
	} else {
		imgOut["vlm_model_id"] = "legacy_inline"
	}

	handleErr = s.processImage(ctx, &payload, vlmModel, vlmCfg, tracker, imgOut)
	return handleErr
}

// processImage runs the unified image action loop for one image. The selector
// plans rounds from the payload switches; the dispatcher runs each round
// (observation and caption share one VLM call); the result processor reads the
// observation and, when the OCR policy wants it, appends an OCR round. The
// observation must be known before any OCR call is spent — that ordering is
// what makes "observe first, then act" possible: an image whose text is
// reliably absent does not pay for OCR.
//
// An observation (describe) failure is not fatal. The attributes then stay at
// their conservative defaults, which keeps OCR running, so a model that cannot
// observe costs one extra call rather than losing the text. This mirrors the
// upstream caption path, where a failed caption is also just a warning.
//
// out is the per-image trace map, owned by the caller and closed by the span
// this function ends; every field the pipeline discovers is written into it.
func (s *ImageMultimodalService) processImage(
	ctx context.Context,
	payload *types.ImageMultimodalPayload,
	vlmModel vlm.VLM,
	vlmCfg types.VLMConfig,
	tracker SpanTracker,
	out types.JSONMap,
) error {
	// Open a per-image subspan under the parent attempt's multimodal stage.
	// If the parent stage row is missing (legacy in-flight task, or the
	// upstream code shipped without span tracking), the tracker is a no-op so
	// we silently fall back to the existing counter-based finalize semantics.
	var imgSpan *Span
	if payload.Attempt > 0 {
		parent := tracker.LookupStage(ctx, payload.KnowledgeID, payload.Attempt, types.StageMultimodal)
		if parent != nil {
			name := fmt.Sprintf("multimodal.image[%d]", payload.ImageIndex)
			// The observation, and the policy it resolves to, are only known
			// after the describe round, so they land on the span's output (see
			// out["image_attrs"] and out["attr_policy"]) instead of here.
			imgSpan = tracker.BeginSubSpan(ctx, parent, name, types.SpanKindGeneration, types.JSONMap{
				"image_url":         payload.ImageURL,
				"image_source_type": payload.ImageSourceType,
				"parent_chunk_id":   payload.ChunkID,
				// Which pipeline the trace is looking at: observation_driven
				// (a describe round that also observes the attributes, then OCR
				// only if the policy says so) or caption_ocr (caption then OCR,
				// no observation). Reading image_info alone cannot tell a
				// caption_ocr run from an observation_driven run that skipped
				// OCR.
				"pipeline": string(types.PipelineModeFor(payload.ImageAttrsEnabled)),
			})
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
	// "unsupported URL scheme"). On unrecoverable read failure the image is
	// skipped (the deferred finalize still counts it).
	imgBytes, readErr := s.readImageBytes(ctx, *payload)
	if readErr != nil {
		logger.Errorf(ctx, "[ImageMultimodal] Skip unreadable image %s: %v", payload.ImageURL, readErr)
		out["skipped"] = "unreadable_image"
		out["read_error"] = readErr.Error()
		handleErr = fmt.Errorf("read source image: %w", readErr)
		return handleErr
	}
	out["image_bytes"] = len(imgBytes)

	imageInfo := types.ImageInfo{
		SHA256:      fmt.Sprintf("%x", sha256.Sum256(imgBytes)),
		URL:         payload.ImageURL,
		OriginalURL: payload.ImageURL,
	}

	// --- Unified action loop: the decision + dispatch half of the image
	// pipeline framework. Every image step is a normal action (caption,
	// observation, ocr). The selector plans rounds from the payload switches;
	// the dispatcher runs each round (observation + caption share one VLM call);
	// the result processor reads the observation and, when OCR is wanted,
	// appends an ocr round - the "observe, decide, then act" closure. The VLM
	// transport here is a direct-call shim; PR-E routes it through a request
	// manager without touching this dispatch logic.
	if !payload.ImageAttrsEnabled && !payload.EnableOCR {
		// Caption+OCR mode with OCR off: recorded so the trace explains the
		// missing OCR chunk rather than leaving it to be inferred from an
		// absence.
		out["ocr_skipped"] = "disabled"
	}
	plan := selectImageActionRounds(payload)
	for i := 0; i < len(plan); i++ {
		round := plan[i]
		var ocrSpan *Span
		if round.Contains(types.ActionOCR) && imgSpan != nil {
			ocrSpan = tracker.BeginSubSpan(ctx, imgSpan, imgSpan.Name+".ocr", types.SpanKindGeneration, nil)
		}
		s.executeActionRound(ctx, payload, vlmModel, imgBytes, vlmCfg, &imageInfo, out, round)
		if ocrSpan != nil {
			if message, failed := out["ocr_error"].(string); failed {
				code, _ := out["ocr_error_code"].(string)
				tracker.FailSpan(ctx, ocrSpan, code, message, nil)
			} else {
				tracker.EndSpan(ctx, ocrSpan, types.JSONMap{
					"status": out["ocr_status"], "chars": out["ocr_chars"],
				})
			}
		}
		// Result processor: only an observation round feeds the OCR decision.
		if round.Contains(types.ActionObservation) {
			ocrWanted := payload.EnableOCR && DecideOCR(imageInfo.Attrs, payload.ImageActions)
			out["attr_policy"] = types.JSONMap{"ocr": ocrWanted}
			out["image_attrs"] = imageInfo.Attrs.Attrs
			if ocrWanted {
				plan = append(plan, ActionRound{Actions: []types.ActionKind{types.ActionOCR}})
			} else {
				logger.Infof(ctx, "[ImageMultimodal] Skipping OCR for %s (attrs=%v)",
					payload.ImageURL, imageInfo.Attrs.Attrs)
				out["ocr_skipped"] = "attr_policy"
			}
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
			SourceLocators:  payload.SourceLocators,
			IsEnabled:       true,
			Flags:           types.ChunkFlagRecommended,
			ImageInfo:       string(imageInfoJSON),
			CreatedAt:       time.Now(),
			UpdatedAt:       time.Now(),
		})
	}

	if payload.EnableCaption && imageInfo.Caption != "" {
		newChunks = append(newChunks, &types.Chunk{
			ID:              uuid.New().String(),
			TenantID:        payload.TenantID,
			KnowledgeID:     payload.KnowledgeID,
			KnowledgeBaseID: payload.KnowledgeBaseID,
			Content:         imageInfo.Caption,
			ChunkType:       types.ChunkTypeImageCaption,
			ParentChunkID:   payload.ChunkID,
			SourceLocators:  payload.SourceLocators,
			IsEnabled:       true,
			Flags:           types.ChunkFlagRecommended,
			ImageInfo:       string(imageInfoJSON),
			CreatedAt:       time.Now(),
			UpdatedAt:       time.Now(),
		})
	}
	out["chunks_created"] = len(newChunks)
	if out["ocr_status"] == "failed" {
		out["outcome"] = "partial_failure"
		if len(newChunks) == 0 {
			out["outcome"] = "failed"
		}
	}

	// A retry re-runs this whole function: an index write or the parent finalize
	// failed on the previous attempt, and asynq handed the same payload back. The
	// chunks that attempt persisted are still there, so they are replaced here
	// first — otherwise every retry appends another copy of the image's OCR and
	// caption chunks to the knowledge. The removal happens only now, once the
	// replacement content exists, so a VLM failure never leaves the image without
	// its previously indexed chunks. A first attempt has nothing to replace, and
	// the scan reads every multimodal chunk of the knowledge, so it is skipped
	// there: running it for every image would cost O(N²) reads per document.
	//
	// The scan sits before the "nothing extracted" early return on purpose. A
	// previous attempt may have persisted chunks and then failed to index them,
	// while this attempt extracts nothing new (the VLM call that produced them
	// timed out). Such a chunk is still this image's only content, so it has to be
	// indexed here: returning early would leave it unsearchable while the task
	// reports success — the silent index failure this PR exists to remove.
	var retained []*types.Chunk
	if isTaskRetryAttempt(ctx) {
		var err error
		retained, err = s.dropStaleImageChunks(ctx, *payload, newChunks)
		if err != nil {
			handleErr = err
			return handleErr
		}
	}

	// Index this attempt's chunks plus every chunk of the image a previous
	// attempt persisted without marking it indexed. Per-type replacement keeps a
	// chunk whose type this attempt did not reproduce, and that is exactly the
	// shape a failed index write leaves behind.
	toIndex := make([]*types.Chunk, 0, len(newChunks)+len(retained))
	toIndex = append(toIndex, newChunks...)
	for _, chunk := range retained {
		if chunk.Status != int(types.ChunkStatusIndexed) {
			toIndex = append(toIndex, chunk)
		}
	}
	if len(toIndex) == 0 {
		// Deferred finalize will count this image on success.
		out["skipped"] = "no_extracted_content"
		if out["ocr_status"] == "failed" {
			out["skipped"] = "ocr_failed"
		}
		return nil
	}

	// Persist chunks
	if len(newChunks) > 0 {
		if err := s.chunkService.GetRepository().CreateChunks(ctx, newChunks); err != nil {
			handleErr = fmt.Errorf("create multimodal chunks: %w", err)
			return handleErr
		}
		for _, c := range newChunks {
			logger.Infof(ctx, "[ImageMultimodal] Created %s chunk %s for image %s, len=%d",
				c.ChunkType, c.ID, payload.ImageURL, len(c.Content))
		}
	}

	// Index chunks so they can be retrieved. The outcome is recorded only once
	// the write succeeded: the trace used to report indexed: true
	// unconditionally, so chunks whose index write failed still looked
	// searchable and the task finished as a success.
	outcome, indexErr := s.indexChunks(ctx, *payload, toIndex, imgBytes, out)
	if indexErr != nil {
		logger.Errorf(ctx, "[ImageMultimodal] %d of %d multimodal chunks were not indexed: %v",
			outcome.Failed, len(toIndex), indexErr)
		handleErr = fmt.Errorf("index multimodal chunks: %w", indexErr)
		return handleErr
	}
	out["indexed"] = outcome

	return nil
}

// imageMultimodalChunkTypes are the chunk types processImage writes for one
// image. Every cleanup below is restricted to them, so repairing an image can
// never touch the document's text chunks.
func imageMultimodalChunkTypes() []types.ChunkType {
	return []types.ChunkType{types.ChunkTypeImageOCR, types.ChunkTypeImageCaption}
}

// producedImageChunkTypes is the set of multimodal chunk types this attempt
// produced content for. It scopes the replacement below: a type the attempt did
// not reproduce is left alone.
func producedImageChunkTypes(chunks []*types.Chunk) map[types.ChunkType]bool {
	produced := make(map[types.ChunkType]bool, len(chunks))
	for _, chunk := range chunks {
		produced[chunk.ChunkType] = true
	}
	return produced
}

// dropStaleImageChunks removes the chunks a previous attempt of this very image
// left behind, making "one image → one set of multimodal chunks" hold across
// asynq retries, and returns the previous attempt's chunks it did NOT remove.
//
// Scope: same tenant and knowledge, chunk type in imageMultimodalChunkTypes, and
// an image_info entry pointing at this payload's ImageURL. The parent text chunk
// is deliberately NOT part of the scope: two images of a document can share one
// parent chunk, and deleting by parent would take the sibling image's chunks —
// possibly already indexed — with it. Matching the image URL keeps the sibling
// untouched, and it also covers legacy payloads whose owning chunk could not be
// resolved (ChunkID == "").
//
// The replacement is per chunk type: only the types newChunks covers are dropped
// and rewritten. A retry that reproduces one type but not the other (OCR times
// out while the caption comes back, say) must keep the previous attempt's chunk
// of the missing type — it is still the image's only content for it, and deleting
// it would drop searchable text from the knowledge, which main keeps. The kept
// chunks are returned so the caller can index the ones the previous attempt
// persisted without indexing.
func (s *ImageMultimodalService) dropStaleImageChunks(
	ctx context.Context, payload types.ImageMultimodalPayload, newChunks []*types.Chunk,
) ([]*types.Chunk, error) {
	repo := s.chunkService.GetRepository()
	existing, err := repo.ListChunksByKnowledgeIDAndTypes(
		ctx, payload.TenantID, payload.KnowledgeID, imageMultimodalChunkTypes())
	if err != nil {
		return nil, fmt.Errorf("list multimodal chunks of knowledge %s: %w", payload.KnowledgeID, err)
	}

	produced := producedImageChunkTypes(newChunks)
	stale := make([]*types.Chunk, 0, len(existing))
	retained := make([]*types.Chunk, 0, len(existing))
	for _, chunk := range existing {
		if !chunkBelongsToImage(chunk.ImageInfo, payload.ImageURL) {
			continue
		}
		if produced[chunk.ChunkType] {
			stale = append(stale, chunk)
			continue
		}
		retained = append(retained, chunk)
	}
	if len(stale) == 0 {
		return retained, nil
	}

	// The rows go only after their retrieval entries are gone: if the purge fails,
	// keeping the rows is what lets the next attempt find the IDs and retry it.
	if err := s.purgeStaleIndexEntries(ctx, payload, stale); err != nil {
		return nil, err
	}

	ids := make([]string, 0, len(stale))
	for _, chunk := range stale {
		ids = append(ids, chunk.ID)
	}
	if err := repo.DeleteChunks(ctx, payload.TenantID, ids); err != nil {
		return nil, fmt.Errorf("delete %d stale chunk(s) of image %s: %w", len(ids), payload.ImageURL, err)
	}
	logger.Infof(ctx, "[ImageMultimodal] Dropped %d stale chunk(s) from a previous attempt for image %s",
		len(ids), payload.ImageURL)
	return retained, nil
}

// chunkBelongsToImage reports whether a chunk was built from the image at
// imageURL. Multimodal chunks carry a one-entry image_info array with the image
// they were extracted from; an unreadable or empty value means the chunk cannot
// be attributed and must be left alone.
func chunkBelongsToImage(imageInfo, imageURL string) bool {
	if imageURL == "" {
		return false
	}
	var infos []types.ImageInfo
	if err := json.Unmarshal([]byte(imageInfo), &infos); err != nil {
		return false
	}
	for _, info := range infos {
		if info.URL == imageURL || info.OriginalURL == imageURL {
			return true
		}
	}
	return false
}

// purgeStaleIndexEntries drops the retrieval-store entries of the stale chunks
// the previous attempt left behind, before their rows are deleted: the entries
// are keyed by chunk ID, so one left behind would be an orphan that occupies a
// top-k slot and is then dropped when the results are assembled.
//
// Every stale ID is purged, not only the ones flagged indexed. That flag is
// written after the store accepts the write, and it under-reports exactly in the
// situations a retry cleans up after: CompositeRetrieveEngine.BatchIndex writes
// to several engines concurrently, so one engine succeeding while another fails
// returns an error with part of the batch already stored, and a BatchIndex that
// succeeded followed by a failed UpdateChunk leaves the status unset. Deleting an
// ID that has no entry is a no-op, so the unconditional purge cannot leave an
// orphan behind.
//
// Unlike the row deletion it guards, the purge is not best-effort: if it failed
// while the rows were deleted anyway, the IDs of the entries still to remove
// would be gone with them and no later attempt could find them. A failure
// therefore aborts the replacement — no rows deleted, no replacement chunks
// written — and is returned so asynq retries the image with both sides still
// consistent.
func (s *ImageMultimodalService) purgeStaleIndexEntries(
	ctx context.Context, payload types.ImageMultimodalPayload, stale []*types.Chunk,
) error {
	ids := make([]string, 0, len(stale))
	for _, chunk := range stale {
		ids = append(ids, chunk.ID)
	}
	if len(ids) == 0 {
		return nil
	}

	kb, err := s.kbService.GetKnowledgeBaseByIDOnly(ctx, payload.KnowledgeBaseID)
	if err != nil {
		return fmt.Errorf("purge stale index entries of image %s: get knowledge base %s: %w",
			payload.ImageURL, payload.KnowledgeBaseID, err)
	}
	if kb == nil {
		return fmt.Errorf("purge stale index entries of image %s: knowledge base %s not found",
			payload.ImageURL, payload.KnowledgeBaseID)
	}
	if !kb.NeedsEmbeddingModel() {
		// "Indexed" is only a status flag for such KBs — nothing was written to
		// a retrieval store, so there is nothing to remove.
		return nil
	}

	embeddingModel, err := s.modelService.GetEmbeddingModel(ctx, kb.EmbeddingModelID)
	if err != nil {
		return fmt.Errorf("purge stale index entries of image %s: embedding model %s: %w",
			payload.ImageURL, kb.EmbeddingModelID, err)
	}
	tenantInfo, err := s.tenantRepo.GetTenantByID(ctx, payload.TenantID)
	if err != nil {
		return fmt.Errorf("purge stale index entries of image %s: tenant %d: %w",
			payload.ImageURL, payload.TenantID, err)
	}
	ctx = context.WithValue(ctx, types.TenantInfoContextKey, tenantInfo)
	engine, err := retriever.CreateRetrieveEngineForKB(
		ctx, s.retrieveEngine, s.ownership, payload.TenantID, kb.VectorStoreID)
	if err != nil {
		return fmt.Errorf("purge stale index entries of image %s: create retrieve engine: %w",
			payload.ImageURL, err)
	}
	if err := engine.DeleteBySourceIDList(
		ctx, ids, embeddingModel.GetDimensions(), types.KnowledgeBaseTypeDocument,
	); err != nil {
		return fmt.Errorf("delete %d stale index entr(ies) of image %s: %w",
			len(ids), payload.ImageURL, err)
	}
	logger.Infof(ctx, "[ImageMultimodal] Removed %d stale index entr(ies) for image %s",
		len(ids), payload.ImageURL)
	return nil
}

// buildImageAttrsPrompt asks the model to observe the registered image
// attributes and describe the image in the same answer, so the attributes are
// known before the image's OCR call is spent. The attribute list and questions
// come from the registry, so the prompt cannot drift from the attributes the
// parser folds answers back onto.
func buildImageAttrsPrompt(ctx context.Context, cfg types.VLMConfig) string {
	language := strings.TrimSpace(cfg.DescriptionLanguage)
	if language == "" {
		language = types.LanguageNameFromContext(ctx)
	}
	var b strings.Builder
	fmt.Fprintf(&b,
		"Observe the following about the image, then describe its main content in %s.\n\n"+
			"Output one line per observation using exactly this format, then a DESCRIPTION line:\n\n",
		language)
	for _, spec := range types.ImageAttrRegistry {
		allowed := make([]string, 0, len(spec.Values))
		for _, value := range spec.Values {
			allowed = append(allowed, value.Value)
		}
		fmt.Fprintf(&b, "%s: <%s>\n", spec.Name, strings.Join(allowed, " | "))
	}
	b.WriteString("DESCRIPTION: <one or two sentences>\n\nRules:\n")
	for _, spec := range types.ImageAttrRegistry {
		fmt.Fprintf(&b, "- %s: %s\n", spec.Name, spec.Question)
	}
	b.WriteString("- Output only those lines. No preamble, no summary, no extra commentary.\n")
	return types.AppendCustomPromptInstructions(b.String(), cfg.CustomInstructions, "image_description")
}

// applyImageObservation copies a parsed verdict onto the image record so it is
// persisted with the chunk, and mirrors it into the trace map. The attributes
// are always kept — they are what the OCR policy reads next — while the
// description is stored only when this run wants a caption: the observe round is
// not optional even with captions off, because the attributes are the whole
// point of it.
func applyImageObservation(
	imageInfo *types.ImageInfo,
	attrs types.ImageAttrs,
	description string,
	out types.JSONMap,
	wantCaption bool,
) {
	if attrs.Attrs != nil {
		imageInfo.Attrs = attrs
	}
	if description == "" {
		return
	}
	if !wantCaption {
		out["caption_skipped"] = "disabled"
		return
	}
	imageInfo.Caption = description
	out["caption_chars"] = len([]rune(description))
	out["caption_preview"] = previewText(description, 200)
}

// runImageOCR runs the second pipeline round: text extraction at full
// resolution. Both pipelines share it — the observation-driven one reaches it
// only when the attribute policy allows it, caption+OCR mode runs it for every
// image.
func (s *ImageMultimodalService) runImageOCR(
	ctx context.Context,
	payload *types.ImageMultimodalPayload,
	vlmModel vlm.VLM,
	imgBytes []byte,
	imageInfo *types.ImageInfo,
	out types.JSONMap,
	vlmCfg types.VLMConfig,
) {
	ctx, span := langfuse.GetManager().StartSpan(ctx, langfuse.SpanOptions{
		Name: "image.ocr", Metadata: map[string]interface{}{"model_id": vlmModel.GetModelID()},
	})
	var failure error
	defer func() {
		span.Finish(map[string]interface{}{
			"status": out["ocr_status"], "error_code": out["ocr_error_code"],
			"rejection_reason": out["ocr_rejection_reason"], "chars": out["ocr_chars"],
			"raw_chars": out["ocr_raw_chars"],
		}, nil, failure)
	}()
	// A failed extraction must never leave text from a previous attempt behind.
	imageInfo.OCRText = ""
	out["ocr_chars"] = 0
	// The OCR prompt is system-owned: knowledge base custom instructions must
	// never reach it, or free-form business rules compete with the "No text
	// content" contract and poison image_ocr chunks. buildVLMOCRPrompt picks
	// the scanned-PDF or default prompt and ignores vlmCfg on purpose.
	prompt := buildVLMOCRPrompt(payload.ImageSourceType, vlmCfg)
	if payload.ImageSourceType == "scanned_pdf" {
		logger.Infof(ctx, "[ImageMultimodal] Using scanned PDF prompt for OCR: %s", payload.ImageURL)
		out["ocr_prompt"] = "scanned_pdf"
	} else {
		out["ocr_prompt"] = "default"
	}

	ocrText, ocrErr := vlmModel.Predict(ctx, [][]byte{imgBytes}, prompt)
	if ocrErr != nil {
		failure = ocrErr
		logger.Warnf(ctx, "[ImageMultimodal] OCR failed for %s: %v", payload.ImageURL, ocrErr)
		out["ocr_status"] = "failed"
		out["ocr_error_code"] = "OCR_REQUEST_FAILED"
		if errors.Is(ocrErr, vlm.ErrTruncatedCompletion) {
			out["ocr_error_code"] = "OCR_TRUNCATED"
		}
		out["ocr_error"] = ocrErr.Error()
		return
	}
	out["ocr_raw_chars"] = len([]rune(ocrText))
	ocrText, ocrErr = validateOCRText(ocrText)
	if ocrErr != nil {
		failure = ocrErr
		out["ocr_status"] = "failed"
		out["ocr_error_code"] = "OCR_INVALID_OUTPUT"
		out["ocr_error"] = ocrErr.Error()
		var invalid *ocrValidationError
		if errors.As(ocrErr, &invalid) {
			out["ocr_rejection_reason"] = invalid.reason
		}
		logger.Warnf(ctx, "[ImageMultimodal] OCR rejected for %s: %v", payload.ImageURL, ocrErr)
		return
	}
	if ocrText != "" {
		imageInfo.OCRText = ocrText
		out["ocr_status"] = "succeeded"
		out["ocr_chars"] = len([]rune(ocrText))
		out["ocr_preview"] = previewText(ocrText, 200)
	} else {
		out["ocr_status"] = "no_text"
		out["ocr_skipped"] = "no_text"
	}
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

// isTaskRetryAttempt reports whether the current task context is a retry, i.e.
// a previous attempt of the same task already ran. Asynq also bumps the retry
// count when it re-delivers a task after a timeout or a crashed worker, so
// every attempt that may have left chunks behind is covered. Returns false
// outside a task worker.
func isTaskRetryAttempt(ctx context.Context) bool {
	if retried, ok := asynq.GetRetryCount(ctx); ok {
		return retried > 0
	}
	retried, _, ok := types.TaskRetryMetadataFromContext(ctx)
	return ok && retried > 0
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

// multimodalIndexOutcome is the per-chunk result of one index write, written to
// out["indexed"] as {"succeeded":N,"failed":M}. It replaces the unconditional
// `true` this trace used to carry: a persisted-but-unindexed image chunk is
// invisible to search, so the trace must not claim it was indexed.
type multimodalIndexOutcome struct {
	// Succeeded counts the chunks the retrieval engine accepted and that were
	// marked as indexed afterwards.
	Succeeded int `json:"succeeded"`
	// Failed counts the chunks the retrieval engine did not accept, so it is
	// non-zero only together with the error indexChunks returns. The caller
	// logs that count and fails the task instead of recording this value.
	Failed int `json:"failed"`
}

// indexChunks indexes the newly created multimodal chunks into the retrieval engine
// so they can participate in semantic search, and reports how many chunks made it.
// Every failure is returned to the caller: swallowing it left the chunks persisted
// but unsearchable while the task still finished successfully, so the caller must
// fail the task (and let asynq retry) instead of recording a successful image.
// When the knowledge base opted in and its embedding model takes images, it also
// embeds the image itself (see indexImageVector).
func (s *ImageMultimodalService) indexChunks(
	ctx context.Context,
	payload types.ImageMultimodalPayload,
	chunks []*types.Chunk,
	img []byte, out types.JSONMap,
) (multimodalIndexOutcome, error) {
	// Pre-set the failure count: every early return below leaves all chunks
	// unindexed, and only a completed write lowers it.
	outcome := multimodalIndexOutcome{Failed: len(chunks)}

	kb, err := s.kbService.GetKnowledgeBaseByIDOnly(ctx, payload.KnowledgeBaseID)
	if err != nil {
		logger.Warnf(ctx, "[ImageMultimodal] Failed to get KB for indexing: %v", err)
		return outcome, fmt.Errorf("get knowledge base %s: %w", payload.KnowledgeBaseID, err)
	}
	if kb == nil {
		logger.Warnf(ctx, "[ImageMultimodal] Failed to get KB for indexing: knowledge base not found")
		return outcome, fmt.Errorf("knowledge base %s not found", payload.KnowledgeBaseID)
	}

	// Skip vector/keyword indexing when the KB has no embedding-based pipeline enabled
	// (e.g. Wiki-only KBs). Without this check, GetEmbeddingModel would fail because
	// EmbeddingModelID is intentionally empty for such KBs. The multimodal chunks
	// themselves are already persisted in the DB above, so skipping index here is safe.
	if !kb.NeedsEmbeddingModel() {
		logger.Infof(ctx,
			"[ImageMultimodal] Vector/keyword indexing disabled for KB %s, skipping index for %d multimodal chunks",
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
		return multimodalIndexOutcome{Succeeded: len(chunks)}, nil
	}

	embeddingModel, err := s.modelService.GetEmbeddingModel(ctx, kb.EmbeddingModelID)
	if err != nil {
		logger.Warnf(ctx, "[ImageMultimodal] Failed to get embedding model for indexing: %v", err)
		return outcome, fmt.Errorf("get embedding model %s: %w", kb.EmbeddingModelID, err)
	}

	tenantInfo, err := s.tenantRepo.GetTenantByID(ctx, payload.TenantID)
	if err != nil {
		logger.Warnf(ctx, "[ImageMultimodal] Failed to get tenant for indexing: %v", err)
		return outcome, fmt.Errorf("get tenant %d: %w", payload.TenantID, err)
	}
	// The factory's unbound path reads TenantInfo from ctx; make sure it's there.
	ctx = context.WithValue(ctx, types.TenantInfoContextKey, tenantInfo)

	// Resolve engine via the factory using the KB's VectorStore binding
	// (nil → tenant effective engines fallback; verified tenant ownership otherwise).
	engine, err := retriever.CreateRetrieveEngineForKB(
		ctx, s.retrieveEngine, s.ownership, payload.TenantID, kb.VectorStoreID)
	if err != nil {
		logger.Warnf(ctx, "[ImageMultimodal] Failed to init retrieve engine: %v", err)
		return outcome, fmt.Errorf("create retrieve engine for KB %s: %w", kb.ID, err)
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
			// Engines other than Postgres store the zero value verbatim and
			// filter on is_enabled = true, so leaving this unset made OCR and
			// caption chunks unsearchable there.
			IsEnabled: chunk.IsEnabled,
		})
	}

	if err := engine.BatchIndex(ctx, embeddingModel, indexInfoList); err != nil {
		logger.Errorf(ctx, "[ImageMultimodal] Failed to index multimodal chunks: %v", err)
		return outcome, fmt.Errorf("batch index %d multimodal chunks: %w", len(chunks), err)
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

	logger.Infof(ctx, "[ImageMultimodal] Indexed %d multimodal chunks for knowledge %s",
		len(chunks), payload.KnowledgeID)

	if status := s.indexImageVector(ctx, kb, payload, img, chunks, embeddingModel, engine); status != "" {
		out["image_vector"] = status
	}

	return multimodalIndexOutcome{Succeeded: len(chunks)}, nil
}

// indexImageVector embeds the image itself with a multimodal embedding model
// and stores the vector under an image_vector chunk, so a query can find the
// image by what it shows even where the caption left that out. It returns a
// short status for the trace, "" when image indexing is disabled for this
// document (falling back to the KB setting) or the model takes no images.
//
// The caption and OCR chunks are already indexed, so a failure here only
// loses the extra recall and never fails the task.
func (s *ImageMultimodalService) indexImageVector(
	ctx context.Context, kb *types.KnowledgeBase, payload types.ImageMultimodalPayload, img []byte,
	chunks []*types.Chunk, model embedding.Embedder, engine *retriever.CompositeRetrieveEngine,
) string {
	// The task's explicit choice wins over the KB default, including false.
	// A vector index is still required; old tasks inherit the KB setting.
	enabled := kb.IsImageVectorEnabled()
	if payload.ImageVectorEnabled != nil {
		enabled = kb.IsVectorEnabled() && *payload.ImageVectorEnabled
	}
	if !enabled {
		return ""
	}
	imageModel, ok := embedding.AsImageEmbedder(model)
	if !ok {
		return ""
	}
	// A scanned page is text, which its OCR chunk already carries better
	// than an image vector can: multimodal embeddings are weakest on dense
	// text, and a document of such pages would cost a call each.
	if payload.ImageSourceType == "scanned_pdf" {
		return "skipped: scanned page"
	}
	// The chunk needs text for reranking and the answer context; the caption
	// describes the image, the OCR text is the fallback.
	var source *types.Chunk
	for _, c := range chunks {
		if c.ChunkType == types.ChunkTypeImageCaption {
			source = c
			break
		}
		if c.ChunkType == types.ChunkTypeImageOCR {
			source = c
		}
	}
	if source == nil {
		return "skipped: no caption or OCR text"
	}

	prepared, err := imageprep.Prepare(img, imageModel.ImageLimits())
	if err != nil {
		logger.Warnf(ctx, "[ImageMultimodal] Image %s not embeddable: %v", payload.ImageURL, err)
		return "failed: " + err.Error()
	}
	vectors, err := imageModel.BatchEmbedImages(ctx, []embedding.Image{prepared})
	if err != nil {
		logger.Warnf(ctx, "[ImageMultimodal] Image embedding failed for %s: %v", payload.ImageURL, err)
		return "failed: " + err.Error()
	}
	if dims := model.GetDimensions(); dims > 0 && len(vectors[0]) != dims {
		// The index is laid out for the text vectors' width; a vector of
		// another would land in a different collection or be refused.
		logger.Warnf(ctx, "[ImageMultimodal] Image vector for %s has %d dimensions, the index %d",
			payload.ImageURL, len(vectors[0]), dims)
		return fmt.Sprintf("failed: %d dimensions, index has %d", len(vectors[0]), dims)
	}

	now := time.Now()
	chunk := &types.Chunk{
		ID:              uuid.New().String(),
		TenantID:        payload.TenantID,
		KnowledgeID:     payload.KnowledgeID,
		KnowledgeBaseID: payload.KnowledgeBaseID,
		Content:         source.Content,
		ChunkType:       types.ChunkTypeImageVector,
		ParentChunkID:   payload.ChunkID,
		IsEnabled:       true,
		Flags:           types.ChunkFlagRecommended,
		ImageInfo:       source.ImageInfo,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if err := s.chunkService.GetRepository().CreateChunks(ctx, []*types.Chunk{chunk}); err != nil {
		logger.Warnf(ctx, "[ImageMultimodal] Failed to create image vector chunk for %s: %v", payload.ImageURL, err)
		return "failed: " + err.Error()
	}
	err = engine.BatchIndexVectors(ctx, model, []*types.IndexInfo{{
		Content:         chunk.Content,
		SourceID:        chunk.ID,
		SourceType:      types.ImageSourceType,
		ChunkID:         chunk.ID,
		KnowledgeID:     chunk.KnowledgeID,
		KnowledgeBaseID: chunk.KnowledgeBaseID,
		IsEnabled:       true,
	}}, vectors)
	if err != nil {
		// The chunk stays unindexed: nothing retrieves it, and a re-parse
		// replaces it with the rest of the knowledge's chunks.
		logger.Warnf(ctx, "[ImageMultimodal] Failed to index image vector for %s: %v", payload.ImageURL, err)
		return "failed: " + err.Error()
	}
	// Re-fetch for the same reason as indexChunks: GORM Save would zero
	// the generated fields the in-memory chunk lacks.
	if dbChunk, err := s.chunkService.GetChunkByIDOnly(ctx, chunk.ID); err == nil {
		dbChunk.Status = int(types.ChunkStatusIndexed)
		if err := s.chunkService.GetRepository().UpdateChunk(ctx, dbChunk); err != nil {
			logger.Warnf(ctx, "[ImageMultimodal] Failed to mark image vector chunk %s indexed: %v", chunk.ID, err)
		}
	}
	logger.Infof(ctx, "[ImageMultimodal] Indexed image vector chunk %s for image %s", chunk.ID, payload.ImageURL)
	return "indexed"
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
func (s *ImageMultimodalService) resolveFileServiceForPayload(
	ctx context.Context, payload types.ImageMultimodalPayload,
) interfaces.FileService {
	kb := func() *types.KnowledgeBase {
		kb, err := s.kbService.GetKnowledgeBaseByIDOnly(ctx, payload.KnowledgeBaseID)
		if err != nil {
			logger.Warnf(ctx, "[ImageMultimodal] GetKnowledgeBaseByIDOnly failed: kb=%s err=%v",
				payload.KnowledgeBaseID, err)
			return nil
		}
		return kb
	}
	return s.imageReader().fileService(ctx, payload.TenantID, kb, payload.ImageURL)
}

func (s *ImageMultimodalService) imageReader() knowledgeImageReader {
	return knowledgeImageReader{
		tenantRepo:      s.tenantRepo,
		fileSvc:         s.fileSvc,
		storageResolver: s.storageResolver,
		resourceCatalog: s.resourceCatalog,
	}
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
func (s *ImageMultimodalService) readImageBytes(ctx context.Context, payload types.ImageMultimodalPayload) ([]byte, error) {
	if isStoredImageURL(payload.ImageURL) {
		fileSvc := s.resolveFileServiceForPayload(ctx, payload)
		if fileSvc == nil {
			return nil, fmt.Errorf("no file service available for %s", payload.ImageURL)
		}
		reader, err := fileSvc.GetFile(ctx, payload.ImageURL)
		if err != nil {
			return nil, fmt.Errorf("file service get %s: %w", payload.ImageURL, err)
		}
		defer reader.Close()
		data, err := io.ReadAll(reader)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", payload.ImageURL, err)
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

	data, err := downloadImageFromURL(payload.ImageURL)
	if err != nil {
		return nil, fmt.Errorf("download %s: %w", payload.ImageURL, err)
	}
	logger.Infof(ctx, "[ImageMultimodal] Image downloaded from URL, len=%d", len(data))
	return data, nil
}

// downloadImageFromURL downloads image bytes from an HTTP(S) URL.
func downloadImageFromURL(imageURL string) ([]byte, error) {
	return secutils.DownloadBytes(imageURL)
}

// multimodalPendingKey is the Redis key holding how many image tasks a
// knowledge is still waiting on. The fan-out (enqueueImageMultimodalTasks)
// seeds it and the fan-in (checkAndFinalizeAllImages) drains it, so the two
// must agree on the name — hence one helper rather than two format strings.
func multimodalPendingKey(knowledgeID string) string {
	return fmt.Sprintf("multimodal:pending:%s", knowledgeID)
}

// checkAndFinalizeAllImages counts one image of the parent knowledge and,
// once every image is counted, hands the knowledge to post-process. An error
// means post-process could not be enqueued; the caller must fail the task so
// the row is not left in "processing" with nobody to move it on.
func (s *ImageMultimodalService) checkAndFinalizeAllImages(
	ctx context.Context, payload types.ImageMultimodalPayload,
) error {
	redisKey := multimodalPendingKey(payload.KnowledgeID)

	var pendingCount int64
	if s.redisClient == nil {
		pendingCount = liteMultimodalDecrBy(redisKey, 1)
	} else {
		var err error
		pendingCount, err = s.redisClient.Decr(ctx, redisKey).Result()
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
			return s.enqueueKnowledgePostProcessTask(ctx, payload)
		}
	}

	if pendingCount > 0 {
		return nil
	}
	logger.Infof(ctx, "[ImageMultimodal] All images processed for knowledge %s. Finalizing...", payload.KnowledgeID)
	// Enqueue before dropping the counter: a retry of this task then finds
	// the key at or below zero and enqueues again.
	if err := s.enqueueKnowledgePostProcessTask(ctx, payload); err != nil {
		return err
	}
	if s.redisClient == nil {
		liteMultimodalDel(redisKey)
	} else {
		s.redisClient.Del(ctx, redisKey)
	}
	return nil
}

func (s *ImageMultimodalService) enqueueKnowledgePostProcessTask(
	ctx context.Context, payload types.ImageMultimodalPayload,
) error {
	if s.taskEnqueuer == nil {
		return nil
	}

	taskPayload := types.KnowledgePostProcessPayload{
		TenantID:        payload.TenantID,
		KnowledgeID:     payload.KnowledgeID,
		KnowledgeBaseID: payload.KnowledgeBaseID,
		Language:        payload.Language,
		// Lets post-process skip itself when a reparse superseded the run.
		Attempt: payload.Attempt,
	}
	langfuse.InjectTracing(ctx, &taskPayload)
	payloadBytes, err := json.Marshal(taskPayload)
	if err != nil {
		logger.Warnf(ctx, "[ImageMultimodal] Failed to marshal post process payload: %v", err)
		return fmt.Errorf("marshal post process payload: %w", err)
	}

	task := asynq.NewTask(types.TypeKnowledgePostProcess, payloadBytes,
		knowledgePostProcessTaskOptions()...)
	if err := enqueueWithRetry(ctx, s.taskEnqueuer, task); err != nil {
		logger.Errorf(ctx, "[ImageMultimodal] Failed to enqueue post process task for %s: %v", payload.KnowledgeID, err)
		return err
	}
	logger.Infof(ctx, "[ImageMultimodal] Enqueued post process task for %s", payload.KnowledgeID)
	return nil
}
