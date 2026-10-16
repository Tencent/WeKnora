package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/common"
	"github.com/Tencent/WeKnora/internal/infrastructure/chunker"
	"github.com/Tencent/WeKnora/internal/infrastructure/docparser"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	secutils "github.com/Tencent/WeKnora/internal/utils"
	"github.com/hibiken/asynq"
)

const (
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
	// The attachment row and file remain scoped to payload.TenantID, while
	// parser/model dependencies may belong to a verified shared-agent source.
	resourceTenantID := payload.TenantID
	var options types.TemporaryDocumentCreateOptions
	if json.Unmarshal(document.ProcessingOptions, &options) == nil && options.ResourceTenantID != 0 {
		resourceTenantID = options.ResourceTenantID
	}
	ctx = context.WithValue(ctx, types.TenantIDContextKey, resourceTenantID)
	if tenant, tenantErr := s.tenantService.GetTenantByID(ctx, resourceTenantID); tenantErr == nil && tenant != nil {
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
	content = chunker.NormalizeLineEndings(content)
	content = docparser.NormalizeHTMLTables(content)
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

func (s *temporaryDocumentService) parse(
	ctx context.Context, document *types.TemporaryDocument,
) (string, []types.TemporaryDocumentImage, map[string]string, error) {
	file, err := s.fileService.GetFile(ctx, document.ResourceRef)
	if err != nil {
		return "", nil, nil, fmt.Errorf("open source file: %w", err)
	}
	defer func() { _ = file.Close() }()
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
	if tenant, ok := ctx.Value(types.TenantInfoContextKey).(*types.Tenant); ok && tenant != nil &&
		tenant.ParserEngineConfig != nil {
		request.ParserEngineOverrides = tenant.ParserEngineConfig.ToOverridesMap()
	}
	deps := docparser.ReaderDeps{Overrides: request.ParserEngineOverrides, Remote: s.documentReader}
	if s.tenantService != nil {
		deps.WeKnoraCloudCredentials = s.tenantService.GetWeKnoraCloudCredentials
	}
	reader, err := docparser.NewReader(ctx, parserEngine, strings.TrimPrefix(ext, "."), false, deps)
	if err != nil {
		return "", nil, nil, fmt.Errorf("parse document: %w", err)
	}
	result, err := reader.Read(ctx, request)
	if err != nil {
		return "", nil, nil, fmt.Errorf("parse document: %w", err)
	}
	if result != nil && result.MarkdownContent != "" {
		result.MarkdownContent = chunker.NormalizeLineEndings(result.MarkdownContent)
		result.MarkdownContent = docparser.NormalizeHTMLTables(result.MarkdownContent)
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
			ctx, result, s.fileService, document.TenantID,
		)
		if resolveErr != nil {
			logger.Warnf(ctx, "temporary document image resolution failed: %v", resolveErr)
		} else {
			result.MarkdownContent = updated
			for _, image := range stored {
				images = append(images, types.TemporaryDocumentImage{
					OriginalRef: image.OriginalRef, URL: image.ServingURL, MimeType: image.MimeType,
				})
				if s.resourceCatalog != nil {
					_ = s.resourceCatalog.Bind(ctx, image.ServingURL, types.ResourceOwnerTemporaryDocument,
						document.ID, types.ResourceRelationExtractedImage)
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
			ocrResults[idx], ocrErr = validateOCRText(ocrText)
			if ocrErr != nil {
				logger.Warnf(ctx, "temporary document VLM OCR rejected on image %d: %v", idx, ocrErr)
			}
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
