package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/Tencent/WeKnora/internal/common"
	"github.com/Tencent/WeKnora/internal/infrastructure/chunker"
	"github.com/Tencent/WeKnora/internal/infrastructure/docparser"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	secutils "github.com/Tencent/WeKnora/internal/utils"
	"github.com/google/uuid"
	"github.com/hibiken/asynq"
)

const (
	temporaryDocumentDefaultTTL     = 24 * time.Hour
	temporaryDocumentInlineTokens   = 12000
	temporaryDocumentPromptBudget   = 12000
	temporaryDocumentMaxPromptParts = 16
	// defaultTemporaryDocumentImageOCRMaxPages caps how many page images a
	// scanned / image-only document may send to the VLM, bounding OCR latency.
	// Override via WEKNORA_CHAT_ATTACHMENT_OCR_MAX_PAGES.
	defaultTemporaryDocumentImageOCRMaxPages = 8
	// temporaryDocumentLowTextRunes is the extracted-text threshold (in runes,
	// ignoring image markdown) below which a document is treated as
	// scanned/image-only and eligible for the VLM OCR fallback.
	temporaryDocumentLowTextRunes = 200
	// defaultTemporaryDocumentOCRConcurrency bounds how many page images are
	// OCR'd by the VLM at once. Multi-page scans benefit from concurrent OCR
	// (wall-clock latency drops roughly linearly); the default matches the page
	// cap so a full scan finishes in a single wave. Raising it loads the VLM
	// backend harder — tune via WEKNORA_CHAT_ATTACHMENT_OCR_CONCURRENCY.
	defaultTemporaryDocumentOCRConcurrency = 8
	// temporaryDocumentOCRSufficientRunes is the OCR-yield threshold (in runes)
	// above which a standalone image is treated as text-rich enough that a VLM
	// caption fallback is unnecessary. Mirrors RAGFlow's OCR>VLM cascade cutoff
	// (~32 chars): text-bearing screenshots / scans are served by OCR alone,
	// while sparse-text images (diagrams, photos, icons) fall back to a caption
	// whose semantic description is more useful than the little text OCR found.
	temporaryDocumentOCRSufficientRunes = 32
)

// temporaryDocumentImageOCRMaxPages returns the max page count OCR'd per
// scanned document, honoring WEKNORA_CHAT_ATTACHMENT_OCR_MAX_PAGES.
func temporaryDocumentImageOCRMaxPages() int {
	return envPositiveInt("WEKNORA_CHAT_ATTACHMENT_OCR_MAX_PAGES", defaultTemporaryDocumentImageOCRMaxPages)
}

// temporaryDocumentOCRConcurrency returns the VLM OCR concurrency, honoring
// WEKNORA_CHAT_ATTACHMENT_OCR_CONCURRENCY.
func temporaryDocumentOCRConcurrency() int {
	return envPositiveInt("WEKNORA_CHAT_ATTACHMENT_OCR_CONCURRENCY", defaultTemporaryDocumentOCRConcurrency)
}

// envPositiveInt reads a positive integer from the environment, falling back to
// def when the variable is unset, non-numeric, or non-positive.
func envPositiveInt(key string, def int) int {
	if raw := strings.TrimSpace(os.Getenv(key)); raw != "" {
		if v, err := strconv.Atoi(raw); err == nil && v > 0 {
			return v
		}
	}
	return def
}

// markdownImagePattern matches markdown image references so text-yield
// estimation ignores image-only content (e.g. scanned PDFs).
var markdownImagePattern = regexp.MustCompile(`!\[[^\]]*\]\([^)]*\)`)

var temporaryDocumentExtensions = map[string]struct{}{
	".docx": {}, ".doc": {}, ".pdf": {}, ".ppt": {}, ".pptx": {}, ".epub": {}, ".mhtml": {},
	".xlsx": {}, ".xls": {},
	".md": {}, ".markdown": {}, ".txt": {}, ".csv": {}, ".json": {}, ".xml": {}, ".yaml": {}, ".yml": {}, ".log": {}, ".html": {},
	".jpg": {}, ".jpeg": {}, ".png": {}, ".gif": {}, ".bmp": {}, ".tiff": {}, ".webp": {},
	".mp3": {}, ".wav": {}, ".m4a": {}, ".flac": {}, ".ogg": {}, ".aac": {},
}

var temporaryTextExtensions = map[string]struct{}{
	".md": {}, ".markdown": {}, ".txt": {}, ".csv": {}, ".json": {}, ".xml": {}, ".yaml": {}, ".yml": {}, ".log": {},
}

type temporaryDocumentService struct {
	repo            interfaces.TemporaryDocumentRepository
	fileService     interfaces.FileService
	resourceCatalog interfaces.ResourceCatalog
	documentReader  interfaces.DocumentReader
	imageResolver   *docparser.ImageResolver
	modelService    interfaces.ModelService
	tenantService   interfaces.TenantService
	taskEnqueuer    interfaces.TaskEnqueuer
}

func NewTemporaryDocumentService(
	repo interfaces.TemporaryDocumentRepository,
	fileService interfaces.FileService,
	resourceCatalog interfaces.ResourceCatalog,
	documentReader interfaces.DocumentReader,
	imageResolver *docparser.ImageResolver,
	modelService interfaces.ModelService,
	tenantService interfaces.TenantService,
	taskEnqueuer interfaces.TaskEnqueuer,
) interfaces.TemporaryDocumentService {
	return &temporaryDocumentService{
		repo: repo, fileService: fileService, resourceCatalog: resourceCatalog,
		documentReader: documentReader, imageResolver: imageResolver,
		modelService: modelService, tenantService: tenantService, taskEnqueuer: taskEnqueuer,
	}
}

func temporaryDocumentTTL() time.Duration {
	if raw := strings.TrimSpace(os.Getenv("WEKNORA_CHAT_ATTACHMENT_TTL_HOURS")); raw != "" {
		if hours, err := strconv.Atoi(raw); err == nil && hours > 0 {
			return time.Duration(hours) * time.Hour
		}
	}
	return temporaryDocumentDefaultTTL
}

func (s *temporaryDocumentService) Create(
	ctx context.Context,
	tenantID uint64,
	sessionID, fileName, mimeType string,
	fileSize int64,
	reader io.Reader,
	options types.TemporaryDocumentCreateOptions,
) (*types.TemporaryDocument, error) {
	if tenantID == 0 || strings.TrimSpace(sessionID) == "" {
		return nil, fmt.Errorf("invalid attachment scope")
	}
	safeName, valid := secutils.ValidateInput(fileName)
	if !valid {
		return nil, fmt.Errorf("invalid characters in file name")
	}
	baseName, err := secutils.SafeFileName(safeName)
	if err != nil {
		return nil, fmt.Errorf("unsafe file name: %w", err)
	}
	ext := strings.ToLower(filepath.Ext(baseName))
	if !s.supportsExtension(ctx, tenantID, ext) {
		return nil, fmt.Errorf("unsupported file type: %s", ext)
	}
	maxSize := secutils.GetMaxFileSizeMB() * 1024 * 1024
	if fileSize <= 0 || fileSize > maxSize {
		return nil, fmt.Errorf("file size must be between 1 byte and %dMB", secutils.GetMaxFileSizeMB())
	}
	data, err := io.ReadAll(io.LimitReader(reader, maxSize+1))
	if err != nil {
		return nil, fmt.Errorf("read attachment: %w", err)
	}
	if int64(len(data)) > maxSize {
		return nil, fmt.Errorf("file exceeds size limit of %dMB", secutils.GetMaxFileSizeMB())
	}
	if int64(len(data)) != fileSize {
		fileSize = int64(len(data))
	}

	storageName := fmt.Sprintf("chat_attachment_%s%s", uuid.NewString()[:12], ext)
	resourceRef, err := s.fileService.SaveBytes(ctx, data, tenantID, storageName, true)
	if err != nil {
		return nil, fmt.Errorf("save attachment: %w", err)
	}
	optionsJSON, _ := json.Marshal(options)
	document := &types.TemporaryDocument{
		TenantID: tenantID, SessionID: sessionID, ResourceRef: resourceRef,
		FileName: baseName, FileType: ext, MimeType: strings.TrimSpace(mimeType), FileSize: fileSize,
		Status: types.TemporaryDocumentStatusUploaded, ExpiresAt: time.Now().Add(temporaryDocumentTTL()),
		ProcessingOptions: types.JSON(optionsJSON),
	}
	if err := s.repo.Create(ctx, document); err != nil {
		_ = s.fileService.DeleteFile(ctx, resourceRef)
		return nil, fmt.Errorf("create attachment record: %w", err)
	}
	if s.resourceCatalog != nil {
		if err := s.resourceCatalog.Bind(ctx, resourceRef, "temporary_document", document.ID, "source_file"); err != nil {
			_ = s.repo.DeleteScoped(ctx, tenantID, sessionID, document.ID)
			_ = s.fileService.DeleteFile(ctx, resourceRef)
			return nil, fmt.Errorf("bind attachment resource: %w", err)
		}
	}
	payload, _ := json.Marshal(types.TemporaryDocumentTaskPayload{TenantID: tenantID, DocumentID: document.ID})
	queue, _ := types.QueueForTaskType(types.TypeTemporaryDocumentProcess)
	if _, err := s.taskEnqueuer.Enqueue(
		asynq.NewTask(types.TypeTemporaryDocumentProcess, payload),
		asynq.Queue(queue), asynq.MaxRetry(2), asynq.Timeout(10*time.Minute),
	); err != nil {
		_ = s.repo.MarkFailed(ctx, tenantID, document.ID, "failed to schedule document parsing")
		document.Status = types.TemporaryDocumentStatusFailed
		document.ErrorMessage = "failed to schedule document parsing"
		return document, fmt.Errorf("schedule attachment parsing: %w", err)
	}
	return document, nil
}

func (s *temporaryDocumentService) supportsExtension(ctx context.Context, tenantID uint64, ext string) bool {
	if _, ok := temporaryDocumentExtensions[ext]; ok {
		return true
	}
	if s.documentReader == nil {
		return false
	}
	var overrides map[string]string
	if tenant, err := s.tenantService.GetTenantByID(ctx, tenantID); err == nil && tenant != nil {
		overrides = tenant.ParserEngineConfig.ToOverridesMap()
	}
	engines, err := s.documentReader.ListEngines(ctx, overrides)
	if err != nil {
		return false
	}
	wanted := strings.TrimPrefix(strings.ToLower(ext), ".")
	if wanted == "" || wanted == "url" {
		return false
	}
	for _, engine := range engines {
		if !engine.Available {
			continue
		}
		for _, fileType := range engine.FileTypes {
			if strings.TrimPrefix(strings.ToLower(strings.TrimSpace(fileType)), ".") == wanted {
				return true
			}
		}
	}
	return false
}

func (s *temporaryDocumentService) Get(ctx context.Context, tenantID uint64, sessionID, documentID string) (*types.TemporaryDocument, error) {
	return s.repo.GetScoped(ctx, tenantID, sessionID, documentID)
}

func (s *temporaryDocumentService) OpenFile(ctx context.Context, tenantID uint64, sessionID, documentID string) (io.ReadCloser, string, error) {
	document, err := s.repo.GetScoped(ctx, tenantID, sessionID, documentID)
	if err != nil {
		return nil, "", err
	}
	if document == nil {
		return nil, "", fmt.Errorf("attachment not found")
	}
	file, err := s.fileService.GetFile(ctx, document.ResourceRef)
	if err != nil {
		return nil, "", err
	}
	return file, document.FileName, nil
}

func (s *temporaryDocumentService) List(ctx context.Context, tenantID uint64, sessionID string) ([]*types.TemporaryDocument, error) {
	return s.repo.ListScoped(ctx, tenantID, sessionID)
}

func (s *temporaryDocumentService) Delete(ctx context.Context, tenantID uint64, sessionID, documentID string) error {
	document, err := s.repo.GetScoped(ctx, tenantID, sessionID, documentID)
	if err != nil || document == nil {
		return err
	}
	for _, ref := range temporaryDocumentImageRefs(document.ImageRefs) {
		_ = s.fileService.DeleteFile(ctx, ref.URL)
	}
	_ = s.fileService.DeleteFile(ctx, document.ResourceRef)
	return s.repo.DeleteScoped(ctx, tenantID, sessionID, documentID)
}

func (s *temporaryDocumentService) Process(ctx context.Context, task *asynq.Task) error {
	var payload types.TemporaryDocumentTaskPayload
	if err := json.Unmarshal(task.Payload(), &payload); err != nil {
		return fmt.Errorf("decode temporary document task: %w", err)
	}
	document, err := s.repo.GetByID(ctx, payload.TenantID, payload.DocumentID)
	if err != nil || document == nil {
		return err
	}
	if document.Status == types.TemporaryDocumentStatusReady {
		return nil
	}
	startedAt := time.Now()
	if err := s.repo.MarkProcessing(ctx, payload.TenantID, payload.DocumentID, startedAt); err != nil {
		return err
	}
	// Ensure model resolution (VLM/ASR) has a tenant ID in context.
	ctx = context.WithValue(ctx, types.TenantIDContextKey, payload.TenantID)
	if tenant, tenantErr := s.tenantService.GetTenantByID(ctx, payload.TenantID); tenantErr == nil && tenant != nil {
		ctx = context.WithValue(ctx, types.TenantInfoContextKey, tenant)
	}
	content, images, metadata, parseErr := s.parse(ctx, document)
	if parseErr != nil {
		retryCount, hasRetryCount := asynq.GetRetryCount(ctx)
		maxRetry, hasMaxRetry := asynq.GetMaxRetry(ctx)
		if hasRetryCount && hasMaxRetry && retryCount < maxRetry {
			logger.Warnf(ctx, "temporary document parse will retry: document_id=%s attempt=%d/%d err=%v",
				payload.DocumentID, retryCount+1, maxRetry+1, parseErr)
			return parseErr
		}
		message := parseErr.Error()
		if len(message) > 2000 {
			message = message[:2000]
		}
		_ = s.repo.MarkFailed(ctx, payload.TenantID, payload.DocumentID, message)
		logger.Errorf(ctx, "temporary document parse failed: document_id=%s err=%v", payload.DocumentID, parseErr)
		if hasRetryCount && hasMaxRetry {
			return parseErr
		}
		// Lite mode doesn't expose Asynq retry metadata in the context. Surface
		// a terminal state immediately instead of leaving the UI spinning.
		return nil
	}
	content = common.CleanInvalidUTF8(content)
	lang := chunker.DetectLanguage(content)
	cfg := chunker.DefaultConfig()
	cfg.Strategy = chunker.StrategyAuto
	cfg.ChunkSize = 1600
	cfg.ChunkOverlap = 160
	parts := chunker.Split(content, cfg)
	chunks := make([]types.TemporaryDocumentChunk, 0, len(parts))
	for _, part := range parts {
		chunks = append(chunks, types.TemporaryDocumentChunk{
			Seq: part.Seq, Content: part.Content, ContextHeader: part.ContextHeader,
			Start: part.Start, End: part.End, TokenCount: chunker.ApproxTokenCount(part.EmbeddingContent(), lang),
		})
	}
	chunksJSON, _ := json.Marshal(chunks)
	imagesJSON, _ := json.Marshal(images)
	metadataJSON, _ := json.Marshal(metadata)
	return s.repo.MarkReady(ctx, payload.TenantID, payload.DocumentID, content,
		types.JSON(chunksJSON), types.JSON(imagesJSON), types.JSON(metadataJSON),
		chunker.ApproxTokenCount(content, lang), len(chunks), time.Now())
}

func (s *temporaryDocumentService) parse(ctx context.Context, document *types.TemporaryDocument) (string, []types.TemporaryDocumentImage, map[string]string, error) {
	file, err := s.fileService.GetFile(ctx, document.ResourceRef)
	if err != nil {
		return "", nil, nil, fmt.Errorf("open source file: %w", err)
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, secutils.GetMaxFileSizeMB()*1024*1024+1))
	if err != nil {
		return "", nil, nil, fmt.Errorf("read source file: %w", err)
	}
	ext := document.FileType
	var options types.TemporaryDocumentCreateOptions
	_ = json.Unmarshal(document.ProcessingOptions, &options)
	if options.ParserEngine == "" || options.ParserEngine == "auto" {
		if tenant, ok := ctx.Value(types.TenantInfoContextKey).(*types.Tenant); ok && tenant != nil {
			options.ParserEngine = tenant.ParserEngineConfig.ResolveChatParserEngine(ext)
		}
	}
	if _, ok := temporaryTextExtensions[ext]; ok && (options.ParserEngine == "" || options.ParserEngine == "auto") {
		return string(data), nil, map[string]string{"parser": "plain_text"}, nil
	}
	if docparser.IsAudioFormat(ext) {
		if options.ASRModelID == "" {
			return "", nil, nil, fmt.Errorf("audio transcription model is not configured")
		}
		asrModel, err := s.modelService.GetASRModel(ctx, options.ASRModelID)
		if err != nil {
			return "", nil, nil, fmt.Errorf("load ASR model: %w", err)
		}
		result, err := asrModel.Transcribe(ctx, data, document.FileName)
		if err != nil {
			return "", nil, nil, fmt.Errorf("transcribe audio: %w", err)
		}
		return result.Text, nil, map[string]string{"parser": "asr"}, nil
	}

	parserEngine := strings.TrimSpace(options.ParserEngine)
	if parserEngine == "auto" {
		parserEngine = ""
	}
	request := &types.ReadRequest{
		FileContent: data, FileName: document.FileName, FileType: strings.TrimPrefix(ext, "."),
		ParserEngine: parserEngine,
	}
	if tenant, ok := ctx.Value(types.TenantInfoContextKey).(*types.Tenant); ok && tenant != nil && tenant.ParserEngineConfig != nil {
		request.ParserEngineOverrides = tenant.ParserEngineConfig.ToOverridesMap()
	}
	var result *types.ReadResult
	if docparser.IsSimpleFormat(ext) && (request.ParserEngine == "" || request.ParserEngine == "auto") {
		result, err = (&docparser.SimpleFormatReader{}).Read(ctx, request)
	} else if s.documentReader != nil {
		result, err = s.documentReader.Read(ctx, request)
	} else {
		err = fmt.Errorf("document reader is not configured")
	}
	if err != nil {
		return "", nil, nil, fmt.Errorf("parse document: %w", err)
	}
	// Capture raw page-image bytes before ResolveAndStore stores/rewrites them,
	// so the VLM OCR fallback for scanned documents has bytes to work with.
	maxOCRPages := temporaryDocumentImageOCRMaxPages()
	if options.OCRMaxPages > 0 {
		maxOCRPages = options.OCRMaxPages
	}
	pageImages := collectImageBytes(result.ImageRefs, maxOCRPages)
	images := make([]types.TemporaryDocumentImage, 0)
	if s.imageResolver != nil {
		updated, stored, resolveErr := s.imageResolver.ResolveAndStore(
			ctx, result, temporarySaveFileService{s.fileService}, document.TenantID,
		)
		if resolveErr != nil {
			logger.Warnf(ctx, "temporary document image resolution failed: %v", resolveErr)
		} else {
			result.MarkdownContent = updated
			for _, image := range stored {
				images = append(images, types.TemporaryDocumentImage{OriginalRef: image.OriginalRef, URL: image.ServingURL, MimeType: image.MimeType})
				if s.resourceCatalog != nil {
					_ = s.resourceCatalog.Bind(ctx, image.ServingURL, "temporary_document", document.ID, "extracted_image")
				}
			}
		}
	}
	metadata := result.Metadata
	if metadata == nil {
		metadata = make(map[string]string)
	}
	if _, exists := metadata["parser"]; !exists {
		metadata["parser"] = "document_reader"
		if request.ParserEngine != "" {
			metadata["parser"] = request.ParserEngine
		}
	}
	content := result.MarkdownContent
	if enriched := s.applyImageUnderstanding(ctx, ext, options, data, pageImages, content); enriched != "" {
		content = enriched
		metadata["image_understanding"] = "vlm"
	}
	return content, images, metadata, nil
}

// applyImageUnderstanding uses the configured VLM to turn image content into
// text. Standalone image uploads run an OCR-first cascade: OCR is attempted and
// a caption is only generated as a fallback when OCR yields little text (so a
// text-bearing screenshot costs one VLM call, while a diagram/photo falls back
// to a caption). This only kicks in when the parsed content is text-poor, i.e.
// no dedicated OCR engine already ran. Image-only / scanned documents get an
// OCR-only pass, gated by the agent opt-in and the low-text threshold to keep
// latency predictable. Returns the enriched content, or "" to keep the original
// content unchanged.
func (s *temporaryDocumentService) applyImageUnderstanding(
	ctx context.Context,
	ext string,
	options types.TemporaryDocumentCreateOptions,
	fileData []byte,
	pageImages [][]byte,
	content string,
) string {
	if options.VLMModelID == "" {
		return ""
	}
	lowText := approxTextContentRunes(content) < temporaryDocumentLowTextRunes
	var extracted string
	switch {
	case docparser.IsImageFormat(ext):
		// A dedicated OCR parser engine may already have produced text; only
		// fall back to the VLM when the parsed content is text-poor. OCR runs
		// first and a caption is generated only if OCR comes back sparse.
		if !lowText || len(fileData) == 0 {
			return ""
		}
		extracted = s.understandImagesWithVLM(ctx, options.VLMModelID, [][]byte{fileData}, false, true)
	case options.ImageUnderstanding && lowText && len(pageImages) > 0:
		extracted = s.understandImagesWithVLM(ctx, options.VLMModelID, pageImages, true, false)
	default:
		return ""
	}
	extracted = strings.TrimSpace(extracted)
	if extracted == "" {
		return ""
	}
	if strings.TrimSpace(content) == "" {
		return extracted
	}
	return content + "\n\n" + extracted
}

// understandImagesWithVLM runs an OCR-first cascade over the given image bytes
// using the configured VLM. Every page is OCR'd (with bounded concurrency); a
// caption is generated only when captionFallback is set AND the combined OCR
// yield is too sparse to be useful (below temporaryDocumentOCRSufficientRunes).
// This mirrors RAGFlow's OCR>VLM cascade: text-bearing images are served by OCR
// alone in a single round-trip, while diagrams/photos/icons — where OCR finds
// little — get a semantic caption instead. Errors on individual images are
// logged and skipped; the combined extracted text is returned best-effort.
func (s *temporaryDocumentService) understandImagesWithVLM(
	ctx context.Context, vlmModelID string, images [][]byte, scanned, captionFallback bool,
) string {
	model, err := s.modelService.GetVLMModel(ctx, vlmModelID)
	if err != nil {
		logger.Warnf(ctx, "temporary document VLM model load failed: %v", err)
		return ""
	}
	ocrPrompt := vlmOCRPrompt
	if scanned {
		ocrPrompt = vlmOCRScannedPDFPrompt
	}

	// Page OCR runs with bounded concurrency so multi-page scans don't pay the
	// full sequential latency of one VLM round-trip per page. Results are
	// collected per index and re-assembled in page order afterwards.
	ocrResults := make([]string, len(images))
	var wg sync.WaitGroup
	sem := make(chan struct{}, temporaryDocumentOCRConcurrency())
	acquire := func() { sem <- struct{}{} }
	release := func() { <-sem }

	for idx, img := range images {
		if len(img) == 0 {
			continue
		}
		wg.Add(1)
		acquire()
		go func(idx int, img []byte) {
			defer wg.Done()
			defer release()
			ocrText, ocrErr := model.Predict(ctx, [][]byte{img}, ocrPrompt)
			if ocrErr != nil {
				logger.Warnf(ctx, "temporary document VLM OCR failed on image %d: %v", idx, ocrErr)
				return
			}
			ocrResults[idx] = sanitizeOCRText(ocrText)
		}(idx, img)
	}
	wg.Wait()

	parts := make([]string, 0, len(images)+1)
	ocrRunes := 0
	for _, t := range ocrResults {
		if t != "" {
			parts = append(parts, t)
			ocrRunes += len([]rune(t))
		}
	}

	// Caption fallback: only when OCR came back sparse. A single caption over
	// the first image is prepended so the semantic description leads the (thin)
	// OCR text. Text-rich images skip this entirely, saving a VLM round-trip.
	if captionFallback && len(images) > 0 && len(images[0]) > 0 &&
		ocrRunes < temporaryDocumentOCRSufficientRunes {
		c, capErr := model.Predict(ctx, [][]byte{images[0]}, buildVLMCaptionPrompt(ctx, types.VLMConfig{}))
		if capErr != nil {
			logger.Warnf(ctx, "temporary document VLM caption failed: %v", capErr)
		} else if caption := strings.TrimSpace(c); caption != "" {
			parts = append([]string{caption}, parts...)
		}
	}

	return strings.Join(parts, "\n\n")
}

// collectImageBytes gathers inline image bytes from parsed image refs, up to a
// cap, for the VLM OCR fallback. Refs without inline data are skipped.
func collectImageBytes(refs []types.ImageRef, limit int) [][]byte {
	if limit <= 0 {
		return nil
	}
	out := make([][]byte, 0, limit)
	for _, ref := range refs {
		if len(ref.ImageData) == 0 {
			continue
		}
		out = append(out, ref.ImageData)
		if len(out) >= limit {
			break
		}
	}
	return out
}

// approxTextContentRunes counts the runes of real text in markdown, ignoring
// image references, so scanned/image-only documents register as low-text.
func approxTextContentRunes(md string) int {
	stripped := markdownImagePattern.ReplaceAllString(md, "")
	return len([]rune(strings.TrimSpace(stripped)))
}

func (s *temporaryDocumentService) ResolveForPrompt(ctx context.Context, tenantID uint64, sessionID string, documentIDs []string, query string) (*types.TemporaryDocumentPromptResult, error) {
	result := &types.TemporaryDocumentPromptResult{}
	if len(documentIDs) > types.MaxTemporaryAttachmentsPerMessage {
		return nil, fmt.Errorf("a message can use at most %d attachments", types.MaxTemporaryAttachmentsPerMessage)
	}
	perDocumentBudget := temporaryDocumentPromptBudget
	if len(documentIDs) > 0 {
		perDocumentBudget = temporaryDocumentPromptBudget / len(documentIDs)
	}
	seen := make(map[string]struct{}, len(documentIDs))
	for _, documentID := range documentIDs {
		if _, duplicate := seen[documentID]; duplicate {
			continue
		}
		seen[documentID] = struct{}{}
		document, err := s.repo.GetScoped(ctx, tenantID, sessionID, documentID)
		if err != nil {
			return nil, err
		}
		if document == nil {
			return nil, fmt.Errorf("attachment %s was not found in this session", documentID)
		}
		if document.Status != types.TemporaryDocumentStatusReady {
			if document.Status == types.TemporaryDocumentStatusFailed {
				return nil, fmt.Errorf("attachment %s failed to parse: %s", document.FileName, document.ErrorMessage)
			}
			return nil, fmt.Errorf("attachment %s is still being processed", document.FileName)
		}
		content, selected, total := selectTemporaryDocumentContentWithBudget(document, query, perDocumentBudget)
		result.Attachments = append(result.Attachments, types.MessageAttachment{
			ID: document.ID, URL: document.ResourceRef, FileName: document.FileName,
			FileType: document.FileType, FileSize: document.FileSize, Content: content,
			ContentMode: map[bool]string{true: "full", false: "selected_chunks"}[selected == total],
			TokenCount:  document.TokenCount, SelectedChunks: selected, TotalChunks: total,
		})
		// Image-type attachments always expose their image so vision models can
		// see it directly; text documents only attach extracted images when the
		// question is visual, to avoid gratuitous multimodal latency.
		if docparser.IsImageFormat(document.FileType) || isVisualDocumentQuery(query) {
			for _, image := range temporaryDocumentImageRefs(document.ImageRefs) {
				if image.URL != "" && len(result.ImageURLs) < 4 {
					result.ImageURLs = append(result.ImageURLs, image.URL)
				}
			}
		}
	}
	return result, nil
}

func selectTemporaryDocumentContent(document *types.TemporaryDocument, query string) (string, int, int) {
	return selectTemporaryDocumentContentWithBudget(document, query, temporaryDocumentPromptBudget)
}

func selectTemporaryDocumentContentWithBudget(document *types.TemporaryDocument, query string, budget int) (string, int, int) {
	var chunks []types.TemporaryDocumentChunk
	_ = json.Unmarshal(document.Chunks, &chunks)
	if budget <= 0 {
		budget = temporaryDocumentPromptBudget
	}
	if len(chunks) == 0 || (document.TokenCount <= temporaryDocumentInlineTokens && document.TokenCount <= budget) {
		return document.Content, len(chunks), len(chunks)
	}
	terms := temporaryDocumentQueryTerms(query)
	type rankedChunk struct {
		chunk types.TemporaryDocumentChunk
		score int
	}
	ranked := make([]rankedChunk, 0, len(chunks))
	for _, part := range chunks {
		text := strings.ToLower(part.ContextHeader + "\n" + part.Content)
		score := 0
		for _, term := range terms {
			score += strings.Count(text, term) * (1 + len([]rune(term))/2)
		}
		ranked = append(ranked, rankedChunk{chunk: part, score: score})
	}
	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].score == ranked[j].score {
			return ranked[i].chunk.Seq < ranked[j].chunk.Seq
		}
		return ranked[i].score > ranked[j].score
	})
	selected := make([]types.TemporaryDocumentChunk, 0, temporaryDocumentMaxPromptParts)
	tokens := 0
	for _, candidate := range ranked {
		if len(selected) >= temporaryDocumentMaxPromptParts {
			break
		}
		if tokens > 0 && tokens+candidate.chunk.TokenCount > budget {
			continue
		}
		selected = append(selected, candidate.chunk)
		tokens += candidate.chunk.TokenCount
	}
	sort.Slice(selected, func(i, j int) bool { return selected[i].Seq < selected[j].Seq })
	var builder strings.Builder
	for _, part := range selected {
		if builder.Len() > 0 {
			builder.WriteString("\n\n---\n\n")
		}
		if part.ContextHeader != "" {
			builder.WriteString(part.ContextHeader)
			builder.WriteString("\n\n")
		}
		builder.WriteString(strings.TrimSpace(part.Content))
	}
	return builder.String(), len(selected), len(chunks)
}

// temporarySaveFileService makes images extracted from an expiring chat
// document use temporary storage without changing the shared ImageResolver API.
// Embedded interface methods forward to the original service.
type temporarySaveFileService struct{ interfaces.FileService }

func (s temporarySaveFileService) SaveBytes(ctx context.Context, data []byte, tenantID uint64, fileName string, _ bool) (string, error) {
	return s.FileService.SaveBytes(ctx, data, tenantID, fileName, true)
}

func temporaryDocumentQueryTerms(query string) []string {
	query = strings.ToLower(strings.TrimSpace(query))
	seen := make(map[string]struct{})
	var terms []string
	for _, field := range strings.FieldsFunc(query, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsPunct(r) }) {
		if len([]rune(field)) < 2 {
			continue
		}
		if _, ok := seen[field]; !ok {
			seen[field] = struct{}{}
			terms = append(terms, field)
		}
	}
	runes := []rune(query)
	for i := 0; i+1 < len(runes); i++ {
		if unicode.Is(unicode.Han, runes[i]) && unicode.Is(unicode.Han, runes[i+1]) {
			term := string(runes[i : i+2])
			if _, ok := seen[term]; !ok {
				seen[term] = struct{}{}
				terms = append(terms, term)
			}
		}
	}
	return terms
}

func isVisualDocumentQuery(query string) bool {
	lower := strings.ToLower(query)
	for _, marker := range []string{"图", "表格", "截图", "页面", "排版", "chart", "figure", "diagram", "image", "layout"} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func temporaryDocumentImageRefs(raw types.JSON) []types.TemporaryDocumentImage {
	var images []types.TemporaryDocumentImage
	_ = json.Unmarshal(raw, &images)
	return images
}

func (s *temporaryDocumentService) CleanupExpired(ctx context.Context) error {
	for {
		documents, err := s.repo.ListExpired(ctx, time.Now(), 100)
		if err != nil {
			return err
		}
		if len(documents) == 0 {
			return nil
		}
		for _, document := range documents {
			if err := s.Delete(ctx, document.TenantID, document.SessionID, document.ID); err != nil {
				logger.Warnf(ctx, "cleanup temporary document failed: document_id=%s err=%v", document.ID, err)
			}
		}
		if len(documents) < 100 {
			return nil
		}
	}
}
