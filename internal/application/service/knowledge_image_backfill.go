package service

import (
	"context"
	"regexp"
	"strings"

	"github.com/Tencent/WeKnora/internal/application/access"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/embedding"
	"github.com/Tencent/WeKnora/internal/types"
)

// Backfilling image vectors for documents ingested before the image-vector
// switch existed. Re-index only flips the visibility of vectors already
// stored, so without this pass enabling image search would return nothing for
// every pre-existing document. Re-parsing would also fix it but re-runs (and
// re-bills) OCR and the VLM captions; backfilling only embeds the pixels.

// markdownImageURLPattern extracts the URL out of a markdown image reference.
// Deliberately narrower than markdownImagePattern in temporary_document.go:
// that one only strips image markup when estimating text yield, whereas this
// one has to hand back a fetchable URL, so it stops at whitespace and parens.
var markdownImageURLPattern = regexp.MustCompile(`!\[[^\]]*\]\(([^()\s]+)\)`)

// imageBackfillTarget is one stored image that should have an image_vector
// chunk and does not.
type imageBackfillTarget struct {
	info          types.ImageInfo
	parentChunkID string
}

// collectImageBackfillTargets inventories the images of one document that are
// missing an image_vector chunk, from two sources because neither is complete:
// the ImageInfo column of the OCR / caption chunks (structured and exact, but
// an image whose OCR and caption both came back empty leaves no such chunk
// behind — and those drawings and scans benefit most from picture search), and
// markdown image references inside text chunks, which survive that case.
// Images that already have an image_vector chunk are excluded, so re-running
// the re-index is idempotent.
func collectImageBackfillTargets(chunks []*types.Chunk) []imageBackfillTarget {
	haveVector := make(map[string]bool)
	for _, chunk := range chunks {
		if chunk.ChunkType != types.ChunkTypeImageVector {
			continue
		}
		for _, info := range decodeChunkImageInfos(chunk.ImageInfo) {
			if url := strings.TrimSpace(info.URL); url != "" {
				haveVector[url] = true
			}
		}
	}

	var targets []imageBackfillTarget
	seen := make(map[string]int)
	add := func(info types.ImageInfo, parentChunkID string) {
		info.URL = strings.TrimSpace(info.URL)
		if info.URL == "" || haveVector[info.URL] {
			return
		}
		if info.OriginalURL == "" {
			info.OriginalURL = info.URL
		}
		if idx, ok := seen[info.URL]; ok {
			// The same image described a second time. Keeping whichever was
			// listed first would drop the other half of the description, so
			// the two are merged instead — see mergeImageInfo.
			mergeImageInfo(&targets[idx].info, &info)
			return
		}
		seen[info.URL] = len(targets)
		targets = append(targets, imageBackfillTarget{info: info, parentChunkID: parentChunkID})
	}

	// Structured source first: it carries the caption and OCR text, which the
	// image_vector chunk stores alongside the picture.
	for _, chunk := range chunks {
		if chunk.ChunkType != types.ChunkTypeImageOCR && chunk.ChunkType != types.ChunkTypeImageCaption {
			continue
		}
		parentID := chunk.ParentChunkID
		if parentID == "" {
			parentID = chunk.ID
		}
		for _, info := range decodeChunkImageInfos(chunk.ImageInfo) {
			add(info, parentID)
		}
	}

	// Fallback source: images referenced by the text but without a chunk of
	// their own.
	for _, chunk := range chunks {
		if chunk.ChunkType != types.ChunkTypeText {
			continue
		}
		for _, match := range markdownImageURLPattern.FindAllStringSubmatch(chunk.Content, -1) {
			add(types.ImageInfo{URL: match[1], OriginalURL: match[1]}, chunk.ID)
		}
	}

	return targets
}

// mergeImageInfo fills the empty fields of dst from src.
//
// Every image produces an OCR chunk and a caption chunk, both carrying
// ImageInfo with different halves of the detail (extracted text vs. caption),
// either of which can be missing. Whichever the repository lists first must
// not decide which half the image vector chunk ends up with.
func mergeImageInfo(dst, src *types.ImageInfo) {
	if dst.OriginalURL == "" {
		dst.OriginalURL = src.OriginalURL
	}
	if dst.Caption == "" {
		dst.Caption = src.Caption
	}
	if dst.OCRText == "" {
		dst.OCRText = src.OCRText
	}
}

// imageByteReader reads the bytes of a stored image.
//
// imageFileResolver is the production implementation; depending on the narrow
// interface is what lets the backfill be tested without a storage backend.
type imageByteReader interface {
	ReadImageBytes(ctx context.Context, payload types.ImageMultimodalPayload) ([]byte, error)
}

// imageFileResolver returns the reader the ingestion pipeline uses, so the
// backfill reads through the same storage backend that wrote the image.
func (s *knowledgeService) imageFileResolver() imageFileResolver {
	return imageFileResolver{
		tenantRepo:      s.tenantRepo,
		kbService:       s.kbService,
		resourceCatalog: s.resourceCatalog,
		storageResolver: s.storageResolver,
		fileSvc:         s.fileSvc,
	}
}

// imageByteReader returns the reader used to load image bytes for backfill,
// honouring the test override when one is installed.
func (s *knowledgeService) imageByteReader() imageByteReader {
	if s.imageReader != nil {
		return s.imageReader
	}
	return s.imageFileResolver()
}

// backfillImageVectors creates the missing image_vector chunks of one document.
//
// Every failure is per-image and non-fatal: an image whose bytes can no longer
// be read must not take the rest of the document down with it, nor make the
// re-index task retry — a retry would redo every other document first.
func (s *knowledgeService) backfillImageVectors(
	ctx context.Context,
	reader imageByteReader,
	kb *types.KnowledgeBase,
	knowledge *types.Knowledge,
	embedder embedding.Embedder,
	engine imageVectorIndexer,
	targets []imageBackfillTarget,
) (created, failed int) {
	for _, target := range targets {
		payload := types.ImageMultimodalPayload{
			TenantID:        knowledge.TenantID,
			KnowledgeID:     knowledge.ID,
			KnowledgeBaseID: kb.ID,
			ChunkID:         target.parentChunkID,
			ImageURL:        target.info.URL,
		}
		imgBytes, err := reader.ReadImageBytes(ctx, payload)
		if err != nil {
			failed++
			logger.Warnf(ctx, "[ImageVector] Backfill: cannot read image %s of knowledge %s: %v",
				target.info.URL, knowledge.ID, err)
			continue
		}
		if len(imgBytes) == 0 {
			failed++
			logger.Warnf(ctx, "[ImageVector] Backfill: empty image %s of knowledge %s",
				target.info.URL, knowledge.ID)
			continue
		}
		if err := writeImageVectorChunk(ctx, s.chunkService, engine, kb, embedder,
			imageVectorLocation{
				TenantID:        knowledge.TenantID,
				KnowledgeID:     knowledge.ID,
				KnowledgeBaseID: kb.ID,
				ParentChunkID:   target.parentChunkID,
			}, target.info, imgBytes); err != nil {
			failed++
			logger.Warnf(ctx, "[ImageVector] Backfill: failed for image %s of knowledge %s: %v",
				target.info.URL, knowledge.ID, err)
			continue
		}
		created++
	}
	return created, failed
}

// maybeIndexImageVectorsOnIngest writes a document's image vectors while it is
// being ingested. The multimodal task writes them as a side effect of holding
// the pixels for OCR and the caption, so as its sole writer a KB got image
// vectors only if it had also configured a VLM — two independent opt-ins the
// ingestion path had chained together, which silently produced nothing for
// every later upload on a KB with image vectors on and no VLM. Embedding the
// picture needs no VLM. When the fan-out is going to run it still owns the
// images: running both would return the same picture twice per query.
func (s *knowledgeService) maybeIndexImageVectorsOnIngest(
	ctx context.Context,
	kb *types.KnowledgeBase,
	knowledge *types.Knowledge,
	embedder embedding.Embedder,
	engine imageVectorIndexer,
	options ProcessChunksOptions,
	parsedChunks []types.ParsedChunk,
) {
	if kb == nil || knowledge == nil || embedder == nil || engine == nil {
		return
	}
	multimodalOwnsImages := options.EnableMultimodel && len(options.StoredImages) > 0 && s.task != nil
	if multimodalOwnsImages || len(options.StoredImages) == 0 {
		return
	}
	if !usesMultimodalEnvelope(kb, embedder) {
		return
	}

	targets := make([]imageBackfillTarget, 0, len(options.StoredImages))
	seen := make(map[string]bool, len(options.StoredImages))
	for _, img := range options.StoredImages {
		url := strings.TrimSpace(img.ServingURL)
		if url == "" || seen[url] {
			continue
		}
		seen[url] = true
		info := types.ImageInfo{URL: url, OriginalURL: url}
		if orig := strings.TrimSpace(img.OriginalRef); orig != "" {
			info.OriginalURL = orig
		}
		for i := range parsedChunks {
			if parsedChunks[i].ChunkID != "" && strings.Contains(parsedChunks[i].Content, url) {
				targets = append(targets, imageBackfillTarget{
					info:          info,
					parentChunkID: parsedChunks[i].ChunkID,
				})
				break
			}
		}
	}
	if len(targets) == 0 {
		return
	}

	if !access.HasKBGrant(ctx, kb.ID, kb.TenantID, types.OrgRoleEditor) {
		var err error
		if ctx, err = access.WithKBTaskWrite(ctx, kb, knowledge.TenantID); err != nil {
			logger.Warnf(ctx, "[ImageVector] Ingestion: KB %s not writable for tenant %d: %v",
				kb.ID, knowledge.TenantID, err)
			return
		}
	}

	created, failed := s.backfillImageVectors(ctx, s.imageByteReader(), kb, knowledge,
		embedder, engine, targets)
	logger.Infof(ctx, "[ImageVector] Ingestion: knowledge %s: %d created, %d failed",
		knowledge.ID, created, failed)
}
