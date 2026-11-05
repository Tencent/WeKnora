package service

import (
	"context"
	"strings"
	"errors"
	"fmt"

	"github.com/Tencent/WeKnora/internal/application/service/retriever"
	werrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/infrastructure/docparser"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
)

// Reprocess-images: re-run image understanding (caption/OCR) for a document
// whose images were extracted and stored before the knowledge base enabled
// image processing. The document is not re-parsed, nothing is re-uploaded,
// and the knowledge ID never changes — only the image understanding results
// (and their multimodal chunks) are replaced. See specs/001-reprocess-images.

// reprocessImageChunkTypes are the chunk types produced by the multimodal
// pipeline. A reprocess run deletes rows of exactly these types for the
// document so the new run rebuilds them; text chunks are never touched.
var reprocessImageChunkTypes = []types.ChunkType{
	types.ChunkTypeImageOCR,
	types.ChunkTypeImageCaption,
	types.ChunkTypeImageVector,
}

// Sentinel errors for the reprocess guards. The HTTP layer maps each onto
// the contract's 409/422 responses; batch counts them as skip reasons.
var (
	// ErrImagePipelineDisabled: the knowledge base has no image processing
	// enabled — an explicit configuration error rather than a silent no-op,
	// so callers can tell "wrong setup" from "nothing to do".
	ErrImagePipelineDisabled = werrors.NewValidationError("知识库未启用图片处理，请先在知识库设置中开启")
	// ErrImagesInFlight: image tasks are already pending for the document
	// (a reprocess or upload fan-out in progress). Re-triggering would
	// double-drain the fan-in counter and interleave two result generations.
	ErrImagesInFlight = werrors.NewConflictError("该文档图片处理正在进行中，请稍后再试")
	// ErrDocumentBusy: the document is in a non-terminal parse state (parsing
	// / reparse / deleting); image reprocess must not race it.
	ErrDocumentBusy = werrors.NewConflictError("该文档正在解析或删除中，无法重处理图片")
	// ErrNoImagesToProcess: the document completed a parse without any stored
	// image; the request is understood but there is nothing to reprocess.
	ErrNoImagesToProcess = werrors.NewBadRequestError("该文档没有已存储的图片，无需重处理")
)

// providerImageURL maps a stored resource onto the provider:// URL it
// occupies on its storage backend. Physical paths are
// storage://<backend>/<provider> composites; legacy rows hold the bare
// provider path. Used only for chunk-ownership matching of legacy chunks —
// the payload itself carries the resource:// handle (see listDocumentImages).
func providerImageURL(resource *types.StoredResource) string {
	if _, providerPath, ok := types.ParseStorageBackendPath(resource.PhysicalPath); ok {
		return providerPath
	}
	return resource.PhysicalPath
}

// listDocumentImages enumerates the images a document's parse extracted and
// stored, through the resource bindings bindStoredImages claimed (#3342).
// Only live image-kind rows are kept; anything else bound to the knowledge
// (the source file, artifacts) is skipped silently, and an unresolvable
// reference degrades to a warning — the same best-effort contract as the
// bind path, because a missed claim must not fail the whole reprocess.
func (s *knowledgeService) listDocumentImages(
	ctx context.Context, knowledge *types.Knowledge,
) ([]docparser.StoredImage, map[string]string, error) {
	refs, err := s.resourceCatalog.ListReferencesByOwner(ctx, types.ResourceOwnerKnowledge, knowledge.ID)
	if err != nil {
		return nil, nil, err
	}
	images := make([]docparser.StoredImage, 0, len(refs))
	// refAliases maps each image's provider:// URL to its resource:// handle.
	// The payload carries the handle — the same form the upload path's
	// SaveBytes returns, which is what the file proxy authorizes and
	// image_info persists. Chunks parsed before the resource-catalog
	// migration embed the provider:// form instead, so ownership matching
	// rewrites provider URLs to handles (see textChunksForOwnership).
	refAliases := map[string]string{}
	for _, ref := range refs {
		resource, err := s.resourceCatalog.Resolve(ctx, ref)
		if err != nil || resource == nil {
			logger.Warnf(ctx, "Skip unresolvable image reference %s for knowledge %s: %v", ref, knowledge.ID, err)
			continue
		}
		if resource.State != types.ResourceStateActive || resource.Kind != "image" {
			continue
		}
		images = append(images, docparser.StoredImage{
			ServingURL:  ref,
			OriginalRef: resource.OriginalName,
			MimeType:    resource.MimeType,
		})
		if url := providerImageURL(resource); url != ref {
			refAliases[url] = ref
		}
	}
	return images, refAliases, nil
}

// textChunksForOwnership loads the document's text chunks in the shape the
// multimodal fan-out matches images against (imageChunkOwner pairs an image
// with the text chunk whose content embeds its URL). Parent-text chunks are
// included: they embed image URLs too and can legitimately own an image.
func (s *knowledgeService) textChunksForOwnership(
	ctx context.Context, tenantID uint64, knowledgeID string, refAliases map[string]string,
) ([]types.ParsedChunk, error) {
	// Text chunks only. A parent_text chunk duplicates its children's
	// content, so including it makes an image match two chunk ids and
	// imageChunkOwner — deliberately ambiguity-averse — returns none.
	// parent_text is consulted only when no text chunk owns the image.
	chunks, err := s.chunkRepo.ListChunksByKnowledgeIDAndTypes(ctx, tenantID, knowledgeID,
		[]types.ChunkType{types.ChunkTypeText})
	if err != nil {
		return nil, err
	}
	if len(chunks) == 0 {
		parents, err := s.chunkRepo.ListChunksByKnowledgeIDAndTypes(ctx, tenantID, knowledgeID,
			[]types.ChunkType{types.ChunkTypeParentText})
		if err != nil {
			return nil, err
		}
		chunks = parents
	}
	parsed := make([]types.ParsedChunk, 0, len(chunks))
	for _, c := range chunks {
		content := c.Content
		// Chunks parsed since the resource-catalog migration embed images as
		// resource:// handles — the same form the fan-out matches. Older
		// chunks speak provider:// URLs; rewrite those to handles so
		// imageChunkOwner's URL match sees one spelling. The rewritten copy
		// is match-only — nothing is persisted.
		for url, ref := range refAliases {
			if url != "" && ref != "" && strings.Contains(content, url) {
				content = strings.ReplaceAll(content, url, ref)
			}
		}
		parsed = append(parsed, types.ParsedChunk{ChunkID: c.ID, Content: content})
	}
	return parsed, nil
}

// deleteOldImageChunks removes the document's previous multimodal chunks from
// the retrieval engine and the database. Overwrite semantics: the new run
// rebuilds them, and leaving the old rows would mix two generations in the
// gallery. Scope is strictly knowledge_id + the three image chunk types.
func (s *knowledgeService) deleteOldImageChunks(
	ctx context.Context, knowledge *types.Knowledge, kb *types.KnowledgeBase,
) error {
	oldChunks, err := s.chunkRepo.ListChunksByKnowledgeIDAndTypes(
		ctx, knowledge.TenantID, knowledge.ID, reprocessImageChunkTypes)
	if err != nil {
		return err
	}
	if len(oldChunks) == 0 {
		return nil
	}
	chunkIDs := make([]string, 0, len(oldChunks))
	for _, c := range oldChunks {
		chunkIDs = append(chunkIDs, c.ID)
	}
	if kb.NeedsEmbeddingModel() {
		embedder, err := s.modelService.GetEmbeddingModel(ctx, kb.EmbeddingModelID)
		if err != nil {
			return fmt.Errorf("resolve embedding model for image chunk cleanup: %w", err)
		}
		engine, err := retriever.CreateRetrieveEngineForKB(ctx, s.retrieveEngine, s.ownership, knowledge.TenantID, kb.VectorStoreID)
		if err != nil {
			return fmt.Errorf("resolve retrieve engine for image chunk cleanup: %w", err)
		}
		if err := engine.DeleteByChunkIDList(ctx, chunkIDs, embedder.GetDimensions(), kb.Type); err != nil {
			return fmt.Errorf("delete image chunk vectors: %w", err)
		}
	}
	return s.chunkRepo.DeleteChunks(ctx, knowledge.TenantID, chunkIDs)
}

// multimodalInFlight reports whether image tasks are pending for the
// knowledge, on either the Redis or the lite in-memory counter.
func (s *knowledgeService) multimodalInFlight(knowledgeID string) bool {
	key := multimodalPendingKey(knowledgeID)
	if s.redisClient != nil {
		n, err := s.redisClient.Get(context.Background(), key).Int64()
		return err == nil && n > 0
	}
	return liteMultimodalGet(key) > 0
}

// imageTaskTriggerCtx carries what fanned a multimodal run out, so
// enqueueImageMultimodalTasks can stamp it on every payload without a
// signature change: the parse pipeline calls it with no marker, only the
// reprocess path sets one. Kept here (not in the counter/queue files) so the
// feature's additions stay in its own file.
type imageTaskTriggerCtx struct{}

func withImageTaskTrigger(ctx context.Context, trigger string) context.Context {
	return context.WithValue(ctx, imageTaskTriggerCtx{}, trigger)
}

func imageTaskTriggerFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(imageTaskTriggerCtx{}).(string); ok {
		return v
	}
	return ""
}

// liteMultimodalGet reads the lite-mode pending count, mirroring the Redis
// GET the reprocess in-flight guard uses (a missing key reads as zero).
// Defined here rather than in multimodal_pending_counter.go to keep that
// file untouched by this feature.
func liteMultimodalGet(key string) int64 {
	liteMultimodalPending.Lock()
	defer liteMultimodalPending.Unlock()
	return liteMultimodalPending.counts[key]
}

// clearStaleImageOverrides nils out the image-related document-level process
// overrides so the knowledge base's current image configuration governs the
// reprocess run (and later ones). Returns an error only when the stored
// metadata cannot be read or written back.
func clearStaleImageOverrides(knowledge *types.Knowledge) error {
	overrides, err := knowledge.ProcessOverrides()
	if err != nil {
		return err
	}
	if overrides == nil {
		return nil
	}
	if overrides.EnableMultimodel == nil && overrides.VLMConfig == nil &&
		overrides.ImageAttrsEnabled == nil && overrides.ImagePipelineID == nil &&
		overrides.ImagePipelineParams == nil && overrides.ImageVectorEnabled == nil &&
		overrides.ImageActions == nil {
		return nil
	}
	overrides.EnableMultimodel = nil
	overrides.VLMConfig = nil
	overrides.ImageAttrsEnabled = nil
	overrides.ImagePipelineID = nil
	overrides.ImagePipelineParams = nil
	overrides.ImageVectorEnabled = nil
	overrides.ImageActions = nil
	return knowledge.SetProcessOverrides(overrides)
}

// ReprocessKnowledgeImages re-runs image understanding for every image the
// document already has stored, using the knowledge base's image pipeline
// configuration as resolved at trigger time. The configuration snapshot
// travels with each task payload, so mid-run KB edits do not re-route
// in-flight images (same contract as the upload fan-out).
func (s *knowledgeService) ReprocessKnowledgeImages(
	ctx context.Context, knowledgeID string,
) (*types.Knowledge, error) {
	tenantID := types.MustTenantIDFromContext(ctx)
	knowledge, err := s.repo.GetKnowledgeByID(ctx, tenantID, knowledgeID)
	if err != nil {
		return nil, err
	}
	if knowledge == nil {
		return nil, werrors.NewNotFoundError("knowledge not found")
	}

	kb, err := s.kbService.GetKnowledgeBaseByID(ctx, knowledge.KnowledgeBaseID)
	if err != nil {
		return nil, err
	}

	// Guards before any mutation: terminal parse state, no in-flight image
	// tasks, and image processing actually enabled on the knowledge base.
	switch knowledge.ParseStatus {
	case types.ParseStatusCompleted, types.ParseStatusFailed:
		// terminal — reprocess allowed (a failed parse may still have
		// stored images worth recovering)
	default:
		return nil, ErrDocumentBusy
	}
	if s.multimodalInFlight(knowledge.ID) {
		return nil, ErrImagesInFlight
	}
	if !ResolveProcessConfig(kb, nil).EnableMultimodel {
		return nil, ErrImagePipelineDisabled
	}

	// Upload-era process overrides snapshot the image switches as they were
	// THEN — "off" for documents stored before the KB enabled image
	// processing. Left in place they shadow the KB's current setting in the
	// worker's config resolution, failing every image task with "VLM is not
	// enabled". Clearing the image fields re-exposes the KB's live config;
	// non-image overrides (chunking, summary, graph…) stay untouched. The
	// user's intent with this request is exactly "run images under the KB's
	// current setup", so the stale image half of the snapshot goes.
	if err := clearStaleImageOverrides(knowledge); err != nil {
		return nil, err
	}

	images, refAliases, err := s.listDocumentImages(ctx, knowledge)
	if err != nil {
		return nil, err
	}
	images = imagesForMultimodal(images)
	if len(images) == 0 {
		return nil, ErrNoImagesToProcess
	}
	ownerChunks, err := s.textChunksForOwnership(ctx, tenantID, knowledge.ID, refAliases)
	if err != nil {
		return nil, err
	}

	if err := s.deleteOldImageChunks(ctx, knowledge, kb); err != nil {
		return nil, err
	}

	// The row must read "processing" before tasks fan out so the finalize
	// counter has a state to leave; same ordering as processChunks.
	knowledge.ParseStatus = types.ParseStatusProcessing
	knowledge.ErrorMessage = ""
	if err := s.updateKnowledgeUnlessSourceReplaced(ctx, knowledge); err != nil {
		return nil, err
	}

	// A reprocess run is its own traceable attempt: opening one lets each
	// image task hang its multimodal.image[n] subspan off this run's stage
	// span, so the trace timeline shows per-image outcomes. Best-effort — a
	// tracker failure degrades to counter-based finalize exactly like the
	// legacy path (attempt 0).
	if _, attempt, err := s.tracker().OpenAttempt(ctx, knowledge.ID, ""); err == nil && attempt > 0 {
		ctx = withAttempt(ctx, attempt)
	} else if err != nil {
		logger.Warnf(ctx, "ReprocessImages: OpenAttempt failed for %s: %v", knowledge.ID, err)
	}

	s.beginStage(ctx, knowledge.ID, types.StageMultimodal, types.JSONMap{
		"image_count": len(images),
		"trigger":     "reprocess_images",
	})

	// enqueueImageMultimodalTasks seeds the fan-in counter, snapshots the KB
	// config into each payload and settles unowned slots. Metadata stays nil:
	// per-image source types come from the parser, whose output a reprocess
	// run deliberately does not have.
	// The trigger marker rides the context into the fan-out, stamping every
	// payload so the post-process finalize knows to skip enrichment.
	ctx = withImageTaskTrigger(ctx, types.ImageTaskTriggerReprocess)
	if err := s.enqueueImageMultimodalTasks(ctx, knowledge, kb, images, ownerChunks, nil); err != nil {
		return nil, err
	}
	return knowledge, nil
}

// BatchReprocessKnowledgeImages offers documents of the knowledge base an
// image reprocess run — every document when ids is nil, only the named ones
// otherwise (the batch bar's selection). Each document is accepted or
// skipped independently; a document-level failure is counted, never
// propagated — one bad row must not block the backlog the caller drains.
func (s *knowledgeService) BatchReprocessKnowledgeImages(
	ctx context.Context, kbID string, ids []string,
) (*types.BatchReprocessResult, error) {
	tenantID := types.MustTenantIDFromContext(ctx)
	kb, err := s.kbService.GetKnowledgeBaseByID(ctx, kbID)
	if err != nil {
		return nil, err
	}
	if !ResolveProcessConfig(kb, nil).EnableMultimodel {
		return nil, ErrImagePipelineDisabled
	}
	knowledgeList, err := s.repo.ListKnowledgeByKnowledgeBaseID(ctx, tenantID, kbID)
	if err != nil {
		return nil, err
	}
	if len(ids) > 0 {
		wanted := make(map[string]struct{}, len(ids))
		for _, id := range ids {
			wanted[id] = struct{}{}
		}
		filtered := knowledgeList[:0]
		for _, k := range knowledgeList {
			if _, ok := wanted[k.ID]; ok {
				filtered = append(filtered, k)
			}
		}
		knowledgeList = filtered
	}
	result := &types.BatchReprocessResult{Reasons: map[string]int{}}
	for _, k := range knowledgeList {
		if k.ParseStatus != types.ParseStatusCompleted && k.ParseStatus != types.ParseStatusFailed {
			result.Skipped++
			result.Reasons["not_terminal"]++
			continue
		}
		if s.multimodalInFlight(k.ID) {
			result.Skipped++
			result.Reasons["in_progress"]++
			continue
		}
		if _, err := s.ReprocessKnowledgeImages(ctx, k.ID); err != nil {
			switch {
			case errors.Is(err, ErrImagesInFlight):
				result.Skipped++
				result.Reasons["in_progress"]++
			case errors.Is(err, ErrNoImagesToProcess):
				result.Skipped++
				result.Reasons["no_images"]++
			case errors.Is(err, ErrDocumentBusy):
				result.Skipped++
				result.Reasons["not_terminal"]++
			case errors.Is(err, ErrImagePipelineDisabled):
				result.Skipped++
				result.Reasons["disabled"]++
			default:
				result.Failed++
				logger.Warnf(ctx, "Batch reprocess failed for knowledge %s: %v", k.ID, err)
			}
			continue
		}
		result.Accepted++
	}
	return result, nil
}
