package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/application/access"
	"github.com/Tencent/WeKnora/internal/application/service/retriever"
	werrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/tracing/langfuse"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/redis/go-redis/v9"
)

// copyOwnedObject copies srcPath into a NEW object owned by the destination
// tenant, returning the new provider:// (resource) path.
//
// Extracted/embedded chunk images MUST land in the tenant's exports/ namespace,
// because GET /knowledge-bases/:id/files only serves objects that pass
// ValidateKBScopedStoragePath (i.e. {tenant}/exports/...). CopyFile writes to
// the knowledge-scoped upload layout ({tenant}/{knowledgeID}/...) used for raw
// source files, which the KB proxy rejects — so a clone that used CopyFile
// produced images that could no longer be rendered. Instead, read the source
// bytes and re-save them via SaveBytes, exactly mirroring how the original
// images were persisted during ingestion (see image_resolver.saveReferencedImage),
// so the copy is a genuine independent object in the servable namespace.
func copyOwnedObject(
	ctx context.Context,
	srcSvc, dstSvc interfaces.FileService,
	srcPath string,
	tenantID uint64,
	knowledgeID string,
) (string, error) {
	_ = knowledgeID // exports objects are tenant-scoped, not knowledge-scoped
	rc, err := srcSvc.GetFile(ctx, srcPath)
	if err != nil {
		return "", fmt.Errorf("read source image %q: %w", srcPath, err)
	}
	defer rc.Close()
	data, err := io.ReadAll(rc)
	if err != nil {
		return "", fmt.Errorf("buffer source image %q: %w", srcPath, err)
	}

	fileName := uuid.New().String() + imageExtForCopy(srcPath, data)
	newPath, err := dstSvc.SaveBytes(ctx, data, tenantID, fileName, false)
	if err != nil {
		return "", fmt.Errorf("save copied image for %q: %w", srcPath, err)
	}
	return newPath, nil
}

// imageExtForCopy resolves the file extension to use for a copied image. It
// prefers an image extension already present on the source path, then falls
// back to sniffing the content bytes, and finally defaults to ".png" (matching
// image_resolver's default) so the object is always served with a sane type.
func imageExtForCopy(srcPath string, data []byte) string {
	if ext := strings.ToLower(filepath.Ext(srcPath)); isImageExt(ext) {
		return ext
	}
	switch http.DetectContentType(data) {
	case "image/png":
		return ".png"
	case "image/jpeg":
		return ".jpg"
	case "image/gif":
		return ".gif"
	case "image/webp":
		return ".webp"
	case "image/bmp":
		return ".bmp"
	}
	return ".png"
}

func isImageExt(ext string) bool {
	switch ext {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".bmp", ".svg":
		return true
	default:
		return false
	}
}

// cloneChunkImageInfo parses a chunk's image_info JSON, copies every referenced
// object into a NEW object owned by (tenantID, knowledgeID), and returns the
// re-serialized image_info plus the list of newly-created object URLs (for
// rollback on failure). urlCache dedups identical source objects across chunks
// so the same source image is copied at most once per clone AND accumulates the
// full old->new URL mapping so callers can rewrite in-content Markdown image
// references (see rewriteContentImageURLs).
//
// An empty srcImageInfo yields ("", nil, nil). A JSON parse failure returns an
// error (the clone fails) rather than silently inheriting the shared-reference
// bug. When an image's OriginalURL points at the same object as its URL (the
// common case for extracted images), OriginalURL is rewritten to the new path
// too; an OriginalURL from a different/external source is preserved.
func cloneChunkImageInfo(
	ctx context.Context,
	dstSvc interfaces.FileService,
	srcImageInfo string,
	tenantID uint64,
	knowledgeID string,
	urlCache map[string]string,
) (newImageInfo string, copiedURLs []string, err error) {
	if srcImageInfo == "" {
		return "", nil, nil
	}

	var images []*types.ImageInfo
	if err := json.Unmarshal([]byte(srcImageInfo), &images); err != nil {
		return "", nil, fmt.Errorf("failed to parse chunk image_info JSON: %w", err)
	}

	for _, img := range images {
		if img == nil || img.URL == "" {
			continue
		}
		originalMatchedURL := img.OriginalURL == img.URL

		newURL, cached := urlCache[img.URL]
		if !cached {
			newURL, err = copyOwnedObject(ctx, dstSvc, dstSvc, img.URL, tenantID, knowledgeID)
			if err != nil {
				return "", copiedURLs, fmt.Errorf("failed to copy chunk image %q: %w", img.URL, err)
			}
			urlCache[img.URL] = newURL
			copiedURLs = append(copiedURLs, newURL)
		}

		if originalMatchedURL {
			img.OriginalURL = newURL
		}
		img.URL = newURL
	}

	out, err := json.Marshal(images)
	if err != nil {
		return "", copiedURLs, fmt.Errorf("failed to re-serialize chunk image_info: %w", err)
	}
	return string(out), copiedURLs, nil
}

// rewriteContentImageURLs replaces every occurrence of an old image URL with its
// new (copied) URL in content, using the old->new mapping accumulated in
// urlCache. It is the second half of the image deep-copy: chunk Content embeds
// image URLs as Markdown ![](url) references, but for document knowledge the
// image objects live in independent image_ocr/image_caption child chunks — the
// parent text chunk carries the ![](url) reference with an empty image_info. So
// the old->new mapping is only known after every chunk's image_info has been
// processed; this rewrite must therefore run as a final pass once urlCache is
// complete, over ALL cloned chunks, not per-chunk.
//
// Replacements are applied longest-old-URL first so a URL that is a prefix of
// another is not partially rewritten. Entries whose old==new are skipped.
func rewriteContentImageURLs(content string, urlCache map[string]string) string {
	if content == "" || len(urlCache) == 0 {
		return content
	}
	oldURLs := make([]string, 0, len(urlCache))
	for oldURL, newURL := range urlCache {
		if oldURL == "" || oldURL == newURL {
			continue
		}
		oldURLs = append(oldURLs, oldURL)
	}
	slices.SortFunc(oldURLs, func(a, b string) int { return len(b) - len(a) })
	for _, oldURL := range oldURLs {
		content = strings.ReplaceAll(content, oldURL, urlCache[oldURL])
	}
	return content
}

// cleanupCopiedObjects deletes objects that were newly created during a clone
// that subsequently failed, to avoid orphaning storage. It is best-effort:
// delete errors are logged but never returned (the original clone error wins).
func cleanupCopiedObjects(ctx context.Context, svc interfaces.FileService, paths []string) {
	if len(paths) == 0 || svc == nil {
		return
	}
	logger.Infof(ctx, "Cleaning up %d copied objects after clone failure", len(paths))
	for _, p := range paths {
		if p == "" {
			continue
		}
		if err := svc.DeleteFile(ctx, p); err != nil {
			logger.Errorf(ctx, "Failed to clean up copied object %s: %v", p, err)
		}
	}
}

func (s *knowledgeService) CloneKnowledgeBase(ctx context.Context, srcID, dstID string) error {
	source, target, err := s.kbService.CopyKnowledgeBase(ctx, srcID, dstID)
	if err != nil {
		return err
	}
	if source.Type == types.KnowledgeBaseTypeFAQ {
		p := &types.KBCloneProgress{TaskID: access.TransferTaskID(ctx), SourceID: source.ID, TargetID: target.ID}
		return s.cloneFAQKnowledgeBase(ctx, source, target, p, func(*types.KBCloneProgress, error, string) {})
	}
	return s.executeKnowledgeClone(ctx, source, target, nil)
}

// CloneChunk clone chunks from one knowledge to another
// This method transfers a chunk from a source knowledge document to a target knowledge document
// It handles the creation of new chunks in the target knowledge and updates the vector database accordingly
// Parameters:
//   - ctx: Context with authentication and request information
//   - src: Source knowledge document containing the chunk to move
//   - dst: Target knowledge document where the chunk will be moved
//
// Returns:
//   - error: Any error encountered during the move operation
//
// This method handles the chunk transfer logic, including creating new chunks in the target knowledge
// and updating the vector database representation of the moved chunks.
// It also ensures that the chunk's relationships (like pre and next chunk IDs) are maintained
// by mapping the source chunk IDs to the new target chunk IDs.
func (s *knowledgeService) CloneChunk(ctx context.Context, src, dst *types.Knowledge) (err error) {
	sourceKB, err := knowledgeWriteKB(ctx, s.kbService, src)
	if err != nil {
		return err
	}
	targetKB, err := knowledgeWriteKB(ctx, s.kbService, dst)
	if err != nil {
		return err
	}
	if err := access.RequireKBTransfer(ctx, sourceKB, targetKB, access.KBTransferClone); err != nil {
		return err
	}
	stored, err := s.repo.GetKnowledgeByID(ctx, dst.TenantID, dst.ID)
	if err != nil {
		return err
	}
	if stored == nil || stored.ID != dst.ID || stored.TenantID != dst.TenantID ||
		stored.KnowledgeBaseID != targetKB.ID {
		return access.ErrForbidden
	}
	sourceChunks, err := s.transferChunks(ctx, src, sourceKB.ID)
	if err != nil {
		return err
	}
	chunkPageSize := 100
	srcTodst := map[string]string{}
	tagIDMapping := map[string]string{} // srcTagID -> dstTagID
	targetChunks := make([]*types.Chunk, 0, 10)

	// Resolve the destination FileService so extracted images can be copied
	// into objects owned by the destination knowledge. urlCache dedups identical
	// source images across chunks; copiedURLs accumulates new objects so they can
	// be cleaned up if the clone fails partway through.
	dstKB, dstKBErr := s.kbService.GetKnowledgeBaseByID(ctx, dst.KnowledgeBaseID)
	if dstKBErr != nil {
		return fmt.Errorf("failed to load destination knowledge base for image copy: %w", dstKBErr)
	}
	dstSvc := s.resolveFileService(ctx, dstKB)
	urlCache := map[string]string{}
	var copiedURLs []string
	chunksAttempted := false
	var rollbackIndices func() error
	defer func() {
		if err == nil {
			return
		}
		if chunksAttempted {
			// A failed create may have committed before losing its acknowledgement.
			// Claim the original destination before removing any persisted data.
			before, after := *stored, *stored
			after.ParseStatus = types.ParseStatusFailed
			after.ErrorMessage = err.Error()
			if checkpointErr := s.repo.UpdateKnowledgeForTransfer(ctx, &before, &after); checkpointErr != nil {
				err = errors.Join(err, fmt.Errorf("claim failed clone cleanup: %w", checkpointErr))
				return
			}
			if rollbackIndices != nil {
				if cleanupErr := rollbackIndices(); cleanupErr != nil {
					err = errors.Join(err, fmt.Errorf("clean failed clone indices: %w", cleanupErr))
					return
				}
			}
			if cleanupErr := s.chunkRepo.DeleteChunksByKnowledgeID(ctx, dst.TenantID, dst.ID); cleanupErr != nil {
				err = errors.Join(err, fmt.Errorf("clean failed clone chunks: %w", cleanupErr))
				return
			}
		}
		cleanupCopiedObjects(ctx, dstSvc, copiedURLs)
	}()

	now := time.Now()
	for _, sourceChunk := range sourceChunks {
		// Map TagID to target knowledge base
		targetTagID := ""
		if sourceChunk.TagID != "" {
			if mappedTagID, ok := tagIDMapping[sourceChunk.TagID]; ok {
				targetTagID = mappedTagID
			} else {
				// Try to find or create the tag in target knowledge base
				targetTagID = s.getOrCreateTagInTarget(ctx,
					src.TenantID,
					dst.TenantID,
					dst.KnowledgeBaseID,
					sourceChunk.TagID,
					tagIDMapping)
			}
		}

		// Deep-copy extracted images into objects owned by the destination
		// knowledge so deleting the source never breaks this clone. Content
		// URL rewriting happens in a final pass below, once urlCache holds
		// the complete old->new mapping (image objects live in independent
		// child chunks, so a parent text chunk's ![](url) reference cannot be
		// rewritten until its child image chunk has been processed).
		newImageInfo, copied, copyErr := cloneChunkImageInfo(
			ctx, dstSvc, sourceChunk.ImageInfo, dst.TenantID, dst.ID, urlCache)
		copiedURLs = append(copiedURLs, copied...)
		if copyErr != nil {
			err = fmt.Errorf("clone chunk image copy failed: %w", copyErr)
			return err
		}

		targetChunk := &types.Chunk{
			ID:              uuid.New().String(),
			TenantID:        dst.TenantID,
			KnowledgeID:     dst.ID,
			KnowledgeBaseID: dst.KnowledgeBaseID,
			TagID:           targetTagID,
			Content:         sourceChunk.Content,
			ChunkIndex:      sourceChunk.ChunkIndex,
			IsEnabled:       sourceChunk.IsEnabled,
			Flags:           sourceChunk.Flags,
			Status:          sourceChunk.Status,
			IndexStatus:     sourceChunk.IndexStatus,
			StartAt:         sourceChunk.StartAt,
			EndAt:           sourceChunk.EndAt,
			PreChunkID:      sourceChunk.PreChunkID,
			NextChunkID:     sourceChunk.NextChunkID,
			ChunkType:       sourceChunk.ChunkType,
			ParentChunkID:   sourceChunk.ParentChunkID,
			Metadata:        sourceChunk.Metadata,
			ContentHash:     sourceChunk.ContentHash,
			ImageInfo:       newImageInfo,
			CreatedAt:       now,
			UpdatedAt:       now,
		}
		targetChunks = append(targetChunks, targetChunk)
		srcTodst[sourceChunk.ID] = targetChunk.ID
	}

	for _, targetChunk := range targetChunks {
		// Rewrite in-content Markdown image URLs now that urlCache holds the
		// complete old->new mapping across all chunks. This fixes parent text
		// chunks whose ![](url) reference points at a source object copied while
		// processing an independent image_ocr/image_caption child chunk.
		targetChunk.Content = rewriteContentImageURLs(targetChunk.Content, urlCache)
		if val, ok := srcTodst[targetChunk.PreChunkID]; ok {
			targetChunk.PreChunkID = val
		} else {
			targetChunk.PreChunkID = ""
		}
		if val, ok := srcTodst[targetChunk.NextChunkID]; ok {
			targetChunk.NextChunkID = val
		} else {
			targetChunk.NextChunkID = ""
		}
		if val, ok := srcTodst[targetChunk.ParentChunkID]; ok {
			targetChunk.ParentChunkID = val
		} else {
			targetChunk.ParentChunkID = ""
		}
	}
	for chunks := range slices.Chunk(targetChunks, chunkPageSize) {
		chunksAttempted = true
		err := s.chunkRepo.CreateChunks(ctx, chunks)
		if err != nil {
			return err
		}
	}

	tenantID := types.MustTenantIDFromContext(ctx)
	// Route CopyIndices via the source KB's bound store. This function does
	// not handle cross-store copies — embeddings written by different
	// VectorStore backends are not bit-compatible, so callers that allow
	// source/target KBs to bind to different stores must perform their own
	// cross-store migration before invoking this.
	if len(srcTodst) == 0 || dst.EmbeddingModelID == "" {
		return nil
	}
	sourceStoreID := sourceKB.VectorStoreID
	retrieveEngine, err := retriever.CreateRetrieveEngineForKB(
		ctx, s.retrieveEngine, s.ownership, tenantID, sourceStoreID)
	if err != nil {
		return err
	}
	embeddingModel, err := s.modelService.GetEmbeddingModel(ctx, dst.EmbeddingModelID)
	if err != nil {
		return err
	}
	rollbackIndices = func() error {
		return retrieveEngine.DeleteByKnowledgeIDList(ctx, []string{dst.ID}, embeddingModel.GetDimensions(), dst.Type)
	}
	if err := retrieveEngine.CopyIndices(ctx, src.KnowledgeBaseID, dst.KnowledgeBaseID,
		map[string]string{src.ID: dst.ID},
		srcTodst,
		embeddingModel.GetDimensions(),
		dst.Type,
	); err != nil {
		return err
	}
	return nil
}

const (
	kbCloneProgressKeyPrefix = "kb_clone_progress:"
	kbCloneProgressTTL       = 24 * time.Hour
)

// getKBCloneProgressKey returns the Redis key for storing KB clone progress
func getKBCloneProgressKey(taskID string) string {
	return kbCloneProgressKeyPrefix + taskID
}

// ProcessKBClone handles Asynq knowledge base clone tasks
func (s *knowledgeService) ProcessKBClone(ctx context.Context, t *asynq.Task) error {
	var payload types.KBClonePayload
	if err := json.Unmarshal(t.Payload(), &payload); err != nil {
		return fmt.Errorf("failed to unmarshal KB clone payload: %w", err)
	}
	ctx = payload.Initiator.Apply(ctx)
	ctx = withKBActivityTask(ctx, payload.TaskID, kbActivityTrigger(ctx))

	if payload.TenantID == 0 || payload.SourceID == "" || payload.TaskID == "" {
		return fmt.Errorf("invalid clone task: %w", asynq.SkipRetry)
	}
	ctx = types.WithExecutionTenant(ctx, payload.TenantID)
	source, err := s.kbService.GetKnowledgeBaseByID(ctx, payload.SourceID)
	if err != nil {
		return err
	}
	create := payload.CreateTarget || payload.TargetID == ""
	if payload.TargetID == "" {
		// Compatibility for tasks admitted before destination IDs were reserved at
		// enqueue time. The same legacy task always resolves the same target.
		payload.TargetID = uuid.NewSHA1(uuid.NameSpaceOID,
			[]byte(fmt.Sprintf("kb-clone:%d:%s:%s",
				payload.TenantID,
				payload.SourceID,
				payload.TaskID))).
			String()
	}
	target := &types.KnowledgeBase{ID: payload.TargetID, TenantID: payload.TenantID, CreatorID: payload.CreatorID}
	if !create {
		target, err = s.kbService.GetKnowledgeBaseByID(ctx, payload.TargetID)
		if err != nil {
			return err
		}
	}
	if source == nil || source.ID != payload.SourceID || target == nil || target.ID != payload.TargetID {
		return fmt.Errorf("invalid clone binding: %w", asynq.SkipRetry)
	}
	ctx, err = access.WithKBTransferTask(
		ctx,
		source,
		target,
		payload.TenantID,
		access.KBTransferClone,
		payload.TaskID,
		create,
	)
	if err != nil {
		return fmt.Errorf("invalid clone scope: %v: %w", err, asynq.SkipRetry)
	}

	// Get tenant info and add to context
	tenantInfo, err := s.tenantRepo.GetTenantByID(ctx, payload.TenantID)
	if err != nil {
		logger.Errorf(ctx, "Failed to get tenant info: %v", err)
		return fmt.Errorf("failed to get tenant info: %w", err)
	}
	if tenantInfo == nil || tenantInfo.ID != payload.TenantID {
		return fmt.Errorf("invalid clone tenant: %w", asynq.SkipRetry)
	}
	ctx = context.WithValue(ctx, types.TenantInfoContextKey, tenantInfo)

	// Get source and target knowledge bases
	srcKB, dstKB, err := s.kbService.CopyKnowledgeBase(ctx, payload.SourceID, payload.TargetID)
	if err != nil {
		retry, maxRetry := 0, 0
		retry, _ = asynq.GetRetryCount(ctx)
		maxRetry, _ = asynq.GetMaxRetry(ctx)
		status := types.KBCloneStatusProcessing
		if retry >= maxRetry {
			status = types.KBCloneStatusFailed
		}
		_ = s.saveKBCloneProgress(
			ctx,
			&types.KBCloneProgress{
				TaskID:    payload.TaskID,
				SourceID:  payload.SourceID,
				TargetID:  payload.TargetID,
				Status:    status,
				Error:     err.Error(),
				Message:   "Clone preflight failed",
				UpdatedAt: time.Now().Unix(),
			},
		)
		return err
	}

	// Check if this is the last retry
	retryCount, _ := asynq.GetRetryCount(ctx)
	maxRetry, _ := asynq.GetMaxRetry(ctx)
	isLastRetry := retryCount >= maxRetry

	logger.Infof(ctx, "Processing KB clone task: %s, source: %s, target: %s, retry: %d/%d",
		payload.TaskID, payload.SourceID, payload.TargetID, retryCount, maxRetry)

	// Helper function to handle errors - only mark as failed on last retry
	handleError := func(progress *types.KBCloneProgress, err error, message string) {
		if isLastRetry {
			progress.Status = types.KBCloneStatusFailed
			progress.Error = err.Error()
			progress.Message = message
			progress.UpdatedAt = time.Now().Unix()
			_ = s.saveKBCloneProgress(ctx, progress)
			recordKBActivity(ctx, s.audit, payload.TenantID, payload.TargetID, types.AuditActionKBCloneFailed,
				"knowledge_base", payload.TargetID, types.AuditOutcomeFailed,
				map[string]any{"source_kb_id": payload.SourceID, "task_id": payload.TaskID})
		}
	}

	// Update progress to processing
	progress := &types.KBCloneProgress{
		TaskID:    payload.TaskID,
		SourceID:  payload.SourceID,
		TargetID:  payload.TargetID,
		Status:    types.KBCloneStatusProcessing,
		Progress:  0,
		Message:   "Starting knowledge base clone...",
		UpdatedAt: time.Now().Unix(),
	}
	if err := s.saveKBCloneProgress(ctx, progress); err != nil {
		logger.Errorf(ctx, "Failed to update KB clone progress: %v", err)
	}

	if retryCount == 0 {
		recordKBActivity(ctx, s.audit, payload.TenantID, payload.TargetID, types.AuditActionKBCloneStarted,
			"knowledge_base", payload.TargetID, types.AuditOutcomeAccepted,
			map[string]any{"source_kb_id": payload.SourceID, "task_id": payload.TaskID})
	}

	// Use different sync strategies based on knowledge base type
	if srcKB.Type == types.KnowledgeBaseTypeFAQ {
		if err := s.cloneFAQKnowledgeBase(ctx, srcKB, dstKB, progress, handleError); err != nil {
			return err
		}
		recordKBActivity(ctx, s.audit, payload.TenantID, payload.TargetID, types.AuditActionKBCloneCompleted,
			"knowledge_base", payload.TargetID, types.AuditOutcomeSuccess,
			map[string]any{"source_kb_id": payload.SourceID, "task_id": payload.TaskID, "total": progress.Total})
		return nil
	}

	err = s.executeKnowledgeClone(ctx, srcKB, dstKB, func(done, total int) {
		progress.Total = total
		progress.Processed = done
		if total > 0 {
			progress.Progress = done * 100 / total
		}
		progress.Message = fmt.Sprintf("Processed %d/%d clone operations", done, total)
		progress.UpdatedAt = time.Now().Unix()
		_ = s.saveKBCloneProgress(ctx, progress)
	})
	if err != nil {
		handleError(progress, err, "Failed to clone knowledge")
		return err
	}
	totalOperations := progress.Total

	// Mark as completed
	progress.Status = types.KBCloneStatusCompleted
	progress.Progress = 100
	progress.Processed = totalOperations
	progress.Message = "Knowledge base clone completed successfully"
	progress.UpdatedAt = time.Now().Unix()
	if err := s.saveKBCloneProgress(ctx, progress); err != nil {
		logger.Errorf(ctx, "Failed to update KB clone progress to completed: %v", err)
	}

	logger.Infof(ctx, "KB clone task completed: %s", payload.TaskID)
	recordKBActivity(ctx, s.audit, payload.TenantID, payload.TargetID, types.AuditActionKBCloneCompleted,
		"knowledge_base", payload.TargetID, types.AuditOutcomeSuccess,
		map[string]any{"source_kb_id": payload.SourceID, "task_id": payload.TaskID, "total": totalOperations})
	return nil
}

// cloneFAQKnowledgeBase handles FAQ knowledge base cloning with chunk-level incremental sync
func (s *knowledgeService) cloneFAQKnowledgeBase(
	ctx context.Context,
	srcKB, dstKB *types.KnowledgeBase,
	progress *types.KBCloneProgress,
	handleError func(*types.KBCloneProgress, error, string),
) (retErr error) {
	srcByID, dstByID, err := s.preflightFAQClone(ctx, srcKB, dstKB)
	if err != nil {
		return err
	}

	// Deep-copy extracted FAQ images into objects owned by the destination KB.
	// urlCache dedups identical source images across chunks; copiedURLs tracks
	// new objects for best-effort cleanup if the clone fails partway through.
	dstSvc := s.resolveFileService(ctx, dstKB)
	imageURLCache := map[string]string{}
	var copiedImageURLs []string
	defer func() {
		if retErr != nil {
			cleanupCopiedObjects(ctx, dstSvc, copiedImageURLs)
		}
	}()

	// Get source FAQ knowledge first (FAQ KB has exactly one Knowledge entry)
	srcKnowledgeList, err := s.repo.ListKnowledgeByKnowledgeBaseID(ctx, srcKB.TenantID, srcKB.ID)
	if err != nil {
		logger.Errorf(ctx, "Failed to get source FAQ knowledge: %v", err)
		handleError(progress, err, "Failed to get source FAQ knowledge")
		return err
	}
	if len(srcKnowledgeList) == 0 {
		// Source has no FAQ knowledge, nothing to clone
		progress.Status = types.KBCloneStatusCompleted
		progress.Progress = 100
		progress.Message = "Source FAQ knowledge base is empty"
		progress.UpdatedAt = time.Now().Unix()
		_ = s.saveKBCloneProgress(ctx, progress)
		return nil
	}
	srcKnowledge := srcKnowledgeList[0]

	// Get chunk-level differences based on content_hash.
	diff, err := s.chunkRepo.FAQChunkDiff(ctx, srcKB.TenantID, srcKB.ID, dstKB.TenantID, dstKB.ID)
	if err != nil {
		logger.Errorf(ctx, "Failed to calculate FAQ chunk difference: %v", err)
		handleError(progress, err, "Failed to calculate FAQ chunk difference")
		return err
	}
	// Validate the complete diff, including matched status/tag updates, before
	// any tag creation, index deletion or chunk write. A stored-but-unindexed
	// destination from a failed delivery must be replaced, not counted as done.
	for _, id := range diff.ChunksToAdd {
		if srcByID[id] == nil {
			return fmt.Errorf("FAQ source diff escaped transfer scope")
		}
	}
	for _, id := range diff.ChunksToDelete {
		if dstByID[id] == nil {
			return fmt.Errorf("FAQ target diff escaped transfer scope")
		}
	}
	matched := make([]types.FAQChunkSyncPair, 0, len(diff.MatchedPairs))
	for _, pair := range diff.MatchedPairs {
		src, dst := srcByID[pair.SrcChunkID], dstByID[pair.DstChunkID]
		if src == nil || dst == nil {
			return fmt.Errorf("FAQ matched diff escaped transfer scope")
		}
		if dst.Status != int(types.ChunkStatusIndexed) {
			diff.ChunksToAdd = append(diff.ChunksToAdd, src.ID)
			diff.ChunksToDelete = append(diff.ChunksToDelete, dst.ID)
		} else {
			matched = append(matched, pair)
		}
	}
	diff.MatchedPairs = matched
	chunksToAdd := diff.ChunksToAdd
	chunksToDelete := diff.ChunksToDelete

	tagIDMapping := map[string]string{}
	resolveFAQTag := func(srcTagID string) string {
		if srcTagID == "" {
			return ""
		}
		if id, ok := tagIDMapping[srcTagID]; ok {
			return id
		}
		return s.getOrCreateTagInTarget(ctx, srcKB.TenantID, dstKB.TenantID, dstKB.ID, srcTagID, tagIDMapping)
	}

	syncPlan, err := s.buildFAQStatusSyncPlan(ctx, srcKB.TenantID, dstKB.TenantID, diff.MatchedPairs, resolveFAQTag)
	if err != nil {
		logger.Errorf(ctx, "Failed to build FAQ status sync plan: %v", err)
		handleError(progress, err, "Failed to build FAQ status sync plan")
		return err
	}
	chunksToUpdate := syncPlan.Pairs

	totalOperations := len(chunksToAdd) + len(chunksToDelete) + len(chunksToUpdate)
	progress.Total = totalOperations
	progress.Message = fmt.Sprintf(
		"Found %d FAQ entries to add, %d to delete, %d to update status",
		len(chunksToAdd), len(chunksToDelete), len(chunksToUpdate),
	)
	progress.UpdatedAt = time.Now().Unix()
	_ = s.saveKBCloneProgress(ctx, progress)

	logger.Infof(ctx, "FAQ chunks to add: %d, delete: %d, update status: %d",
		len(chunksToAdd), len(chunksToDelete), len(chunksToUpdate))

	if totalOperations == 0 {
		progress.Status = types.KBCloneStatusCompleted
		progress.Progress = 100
		progress.Message = "FAQ knowledge base is already in sync"
		progress.UpdatedAt = time.Now().Unix()
		_ = s.saveKBCloneProgress(ctx, progress)
		return nil
	}

	// Route the FAQ clone through the source KB's bound store. Same
	// constraint as CloneChunk: callers must ensure source and target share
	// the same VectorStore (cross-store FAQ clone is not handled here).
	var sourceStoreID *string
	if srcKB != nil {
		sourceStoreID = srcKB.VectorStoreID
	}
	retrieveEngine, err := retriever.CreateRetrieveEngineForKB(
		ctx, s.retrieveEngine, s.ownership, types.MustTenantIDFromContext(ctx), sourceStoreID)
	if err != nil {
		logger.Errorf(ctx, "Failed to init retrieve engine: %v", err)
		handleError(progress, err, "Failed to initialize retrieve engine")
		return err
	}

	// Get embedding model
	embeddingModel, err := s.modelService.GetEmbeddingModel(ctx, dstKB.EmbeddingModelID)
	if err != nil {
		logger.Errorf(ctx, "Failed to get embedding model: %v", err)
		handleError(progress, err, "Failed to get embedding model")
		return err
	}

	processedCount := 0

	// Delete FAQ chunks that don't exist in source
	if len(chunksToDelete) > 0 {
		// Delete from vector store
		if err := retrieveEngine.DeleteByChunkIDList(ctx, chunksToDelete, embeddingModel.GetDimensions(), types.KnowledgeTypeFAQ); err != nil {
			logger.Errorf(ctx, "Failed to delete FAQ chunks from vector store: %v", err)
			handleError(progress, err, "Failed to delete FAQ entries from vector store")
			return err
		}
		// Delete from database
		if err := s.chunkRepo.DeleteChunks(ctx, dstKB.TenantID, chunksToDelete); err != nil {
			logger.Errorf(ctx, "Failed to delete FAQ chunks from database: %v", err)
			handleError(progress, err, "Failed to delete FAQ entries from database")
			return err
		}
		processedCount += len(chunksToDelete)
		if totalOperations > 0 {
			progress.Progress = processedCount * 100 / totalOperations
		}
		progress.Processed = processedCount
		progress.Message = fmt.Sprintf("Deleted %d FAQ entries, adding %d...", len(chunksToDelete), len(chunksToAdd))
		progress.UpdatedAt = time.Now().Unix()
		_ = s.saveKBCloneProgress(ctx, progress)
	}

	// Get or create the FAQ knowledge entry in destination
	dstKnowledge, err := s.getOrCreateFAQKnowledge(ctx, dstKB, srcKnowledge)
	if err != nil {
		logger.Errorf(ctx, "Failed to get or create FAQ knowledge: %v", err)
		handleError(progress, err, "Failed to prepare FAQ knowledge entry")
		return err
	}

	// Clone FAQ chunks from source to destination
	batch := 50
	for i := 0; i < len(chunksToAdd); i += batch {
		end := i + batch
		if end > len(chunksToAdd) {
			end = len(chunksToAdd)
		}
		batchIDs := chunksToAdd[i:end]

		srcChunks := make([]*types.Chunk, 0, len(batchIDs))
		for _, id := range batchIDs {
			srcChunks = append(srcChunks, srcByID[id])
		}

		// Create new chunks for destination
		newChunks := make([]*types.Chunk, 0, len(srcChunks))
		for _, srcChunk := range srcChunks {
			// Map TagID to target knowledge base
			targetTagID := ""
			if srcChunk.TagID != "" {
				targetTagID = resolveFAQTag(srcChunk.TagID)
			}

			// Deep-copy extracted images into objects owned by the destination
			// FAQ knowledge so deleting the source never breaks this clone.
			newImageInfo, copied, copyErr := cloneChunkImageInfo(
				ctx, dstSvc, srcChunk.ImageInfo, dstKB.TenantID, dstKnowledge.ID, imageURLCache)
			if copyErr != nil {
				logger.Errorf(ctx, "Failed to copy FAQ chunk images: %v", copyErr)
				handleError(progress, copyErr, "Failed to copy FAQ entry images")
				retErr = copyErr
				return retErr
			}
			copiedImageURLs = append(copiedImageURLs, copied...)

			newChunk := &types.Chunk{
				ID:              uuid.New().String(),
				TenantID:        dstKB.TenantID,
				KnowledgeID:     dstKnowledge.ID,
				KnowledgeBaseID: dstKB.ID,
				TagID:           targetTagID,
				Content:         rewriteContentImageURLs(srcChunk.Content, imageURLCache),
				ChunkIndex:      srcChunk.ChunkIndex,
				IsEnabled:       srcChunk.IsEnabled,
				Flags:           srcChunk.Flags,
				ChunkType:       types.ChunkTypeFAQ,
				Metadata:        srcChunk.Metadata,
				ContentHash:     srcChunk.ContentHash,
				ImageInfo:       newImageInfo,
				Status:          int(types.ChunkStatusStored), // Initially stored, will be indexed
				CreatedAt:       time.Now(),
				UpdatedAt:       time.Now(),
			}
			newChunks = append(newChunks, newChunk)
		}

		// Save to database
		if err := s.chunkRepo.CreateChunks(ctx, newChunks); err != nil {
			logger.Errorf(ctx, "Failed to create FAQ chunks: %v", err)
			handleError(progress, err, "Failed to create FAQ entries")
			return err
		}

		// Saved rows now own these images, including when indexing later fails.
		// A later batch failure must not delete files from an earlier saved batch.
		copiedImageURLs = nil

		// Index in vector store using existing method
		// This will index standard question + similar questions based on FAQConfig
		if err := s.indexFAQChunks(ctx, dstKB, dstKnowledge, newChunks, embeddingModel, false, false); err != nil {
			logger.Errorf(ctx, "Failed to index FAQ chunks: %v", err)
			handleError(progress, err, "Failed to index FAQ entries")
			return err
		}

		// Update chunk status to indexed
		for _, chunk := range newChunks {
			chunk.Status = int(types.ChunkStatusIndexed)
		}
		if err := s.chunkRepo.UpdateChunks(ctx, newChunks); err != nil {
			return fmt.Errorf("failed to update FAQ chunks status: %w", err)
			// Don't fail the whole operation for status update failure
		}

		processedCount += len(batchIDs)
		if totalOperations > 0 {
			progress.Progress = processedCount * 100 / totalOperations
		}
		progress.Processed = processedCount
		progress.Message = fmt.Sprintf("Added %d/%d FAQ entries", processedCount-len(chunksToDelete), len(chunksToAdd))
		progress.UpdatedAt = time.Now().Unix()
		_ = s.saveKBCloneProgress(ctx, progress)
	}

	for i := 0; i < len(chunksToUpdate); i += batch {
		end := i + batch
		if end > len(chunksToUpdate) {
			end = len(chunksToUpdate)
		}
		if err := s.syncFAQChunkStatusBatch(
			ctx, dstKB, chunksToUpdate[i:end], syncPlan.SrcByID, syncPlan.DstByID, resolveFAQTag); err != nil {
			logger.Errorf(ctx, "Failed to sync FAQ status fields: %v", err)
			handleError(progress, err, "Failed to sync FAQ status fields")
			return err
		}
		processedCount += end - i
		if totalOperations > 0 {
			progress.Progress = processedCount * 100 / totalOperations
		}
		progress.Processed = processedCount
		progress.Message = fmt.Sprintf("Updated %d/%d FAQ status fields", processedCount-len(chunksToDelete)-len(chunksToAdd), len(chunksToUpdate))
		progress.UpdatedAt = time.Now().Unix()
		_ = s.saveKBCloneProgress(ctx, progress)
	}

	// Mark as completed
	progress.Status = types.KBCloneStatusCompleted
	progress.Progress = 100
	progress.Processed = totalOperations
	progress.Message = "FAQ knowledge base clone completed successfully"
	progress.UpdatedAt = time.Now().Unix()
	if err := s.saveKBCloneProgress(ctx, progress); err != nil {
		logger.Errorf(ctx, "Failed to update KB clone progress to completed: %v", err)
	}

	return nil
}

// getOrCreateFAQKnowledge gets or creates the FAQ knowledge entry for a knowledge base
// If srcKnowledge is provided, it will copy relevant fields from source when creating new knowledge
func (s *knowledgeService) getOrCreateFAQKnowledge(ctx context.Context, kb *types.KnowledgeBase, srcKnowledge *types.Knowledge) (*types.Knowledge, error) {
	// FAQ knowledge base should have exactly one Knowledge entry
	knowledgeList, err := s.repo.ListKnowledgeByKnowledgeBaseID(ctx, kb.TenantID, kb.ID)
	if err != nil {
		return nil, err
	}

	if len(knowledgeList) > 0 {
		return knowledgeList[0], nil
	}

	// Create a new FAQ knowledge entry, copying from source if available
	knowledge := &types.Knowledge{
		ID:               uuid.New().String(),
		TenantID:         kb.TenantID,
		KnowledgeBaseID:  kb.ID,
		Type:             types.KnowledgeTypeFAQ,
		Channel:          types.ChannelWeb,
		Title:            "FAQ",
		ParseStatus:      "completed",
		EnableStatus:     "enabled",
		EmbeddingModelID: kb.EmbeddingModelID,
	}

	// Copy additional fields from source knowledge if available
	if srcKnowledge != nil {
		knowledge.Title = srcKnowledge.Title
		knowledge.Description = srcKnowledge.Description
		knowledge.Source = srcKnowledge.Source
		knowledge.Channel = srcKnowledge.Channel
		fields := map[string]json.RawMessage{}
		if len(srcKnowledge.Metadata) > 0 {
			if err := json.Unmarshal(srcKnowledge.Metadata, &fields); err != nil {
				return nil, fmt.Errorf("decode source FAQ metadata: %w", err)
			}
			delete(fields, types.KnowledgeTransferMetadataKey)
			knowledge.Metadata, err = json.Marshal(fields)
			if err != nil {
				return nil, err
			}
		}
	}

	if err := s.repo.CreateKnowledge(ctx, knowledge); err != nil {
		return nil, err
	}
	return knowledge, nil
}

// saveKBCloneProgress saves the KB clone progress to Redis
func (s *knowledgeService) saveKBCloneProgress(ctx context.Context, progress *types.KBCloneProgress) error {
	key := getKBCloneProgressKey(progress.TaskID)
	data, err := json.Marshal(progress)
	if err != nil {
		return fmt.Errorf("failed to marshal progress: %w", err)
	}
	return s.redisClient.Set(ctx, key, data, kbCloneProgressTTL).Err()
}

// SaveKBCloneProgress saves the KB clone progress to Redis (public method for handler use)
func (s *knowledgeService) SaveKBCloneProgress(ctx context.Context, progress *types.KBCloneProgress) error {
	data, err := json.Marshal(progress)
	if err != nil {
		return err
	}
	return s.redisClient.SetNX(ctx, getKBCloneProgressKey(progress.TaskID), data, kbCloneProgressTTL).Err()
}

// GetKBCloneProgress retrieves the progress of a knowledge base clone task
func (s *knowledgeService) GetKBCloneProgress(ctx context.Context, taskID string) (*types.KBCloneProgress, error) {
	key := getKBCloneProgressKey(taskID)
	data, err := s.redisClient.Get(ctx, key).Bytes()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil, werrors.NewNotFoundError("KB clone task not found")
		}
		return nil, fmt.Errorf("failed to get progress from Redis: %w", err)
	}

	var progress types.KBCloneProgress
	if err := json.Unmarshal(data, &progress); err != nil {
		return nil, fmt.Errorf("failed to unmarshal progress: %w", err)
	}
	return &progress, nil
}

const (
	knowledgeMoveProgressKeyPrefix = "knowledge_move_progress:"
	knowledgeMoveProgressTTL       = 24 * time.Hour
)

func getKnowledgeMoveProgressKey(taskID string) string {
	return knowledgeMoveProgressKeyPrefix + taskID
}

func (s *knowledgeService) saveKnowledgeMoveProgress(ctx context.Context, progress *types.KnowledgeMoveProgress) error {
	key := getKnowledgeMoveProgressKey(progress.TaskID)
	data, err := json.Marshal(progress)
	if err != nil {
		return fmt.Errorf("failed to marshal move progress: %w", err)
	}
	return s.redisClient.Set(ctx, key, data, knowledgeMoveProgressTTL).Err()
}

// SaveKnowledgeMoveProgress saves the knowledge move progress to Redis (public method for handler use)
func (s *knowledgeService) SaveKnowledgeMoveProgress(ctx context.Context, progress *types.KnowledgeMoveProgress) error {
	data, err := json.Marshal(progress)
	if err != nil {
		return err
	}
	return s.redisClient.SetNX(ctx, getKnowledgeMoveProgressKey(progress.TaskID), data, knowledgeMoveProgressTTL).Err()
}

// GetKnowledgeMoveProgress retrieves the progress of a knowledge move task
func (s *knowledgeService) GetKnowledgeMoveProgress(ctx context.Context, taskID string) (*types.KnowledgeMoveProgress, error) {
	key := getKnowledgeMoveProgressKey(taskID)
	data, err := s.redisClient.Get(ctx, key).Bytes()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil, werrors.NewNotFoundError("Knowledge move task not found")
		}
		return nil, fmt.Errorf("failed to get move progress from Redis: %w", err)
	}

	var progress types.KnowledgeMoveProgress
	if err := json.Unmarshal(data, &progress); err != nil {
		return nil, fmt.Errorf("failed to unmarshal move progress: %w", err)
	}
	return &progress, nil
}

// ProcessKnowledgeMove handles Asynq knowledge move tasks
func (s *knowledgeService) ProcessKnowledgeMove(ctx context.Context, t *asynq.Task) error {
	var payload types.KnowledgeMovePayload
	if err := json.Unmarshal(t.Payload(), &payload); err != nil {
		return fmt.Errorf("invalid move payload: %v: %w", err, asynq.SkipRetry)
	}
	if payload.TenantID == 0 || payload.TaskID == "" || payload.SourceKBID == "" || payload.TargetKBID == "" {
		return fmt.Errorf("invalid move scope: %w", asynq.SkipRetry)
	}
	ctx = payload.Initiator.Apply(ctx)
	ctx = withKBActivityTask(ctx, payload.TaskID, kbActivityTrigger(ctx))
	ctx = types.WithExecutionTenant(ctx, payload.TenantID)
	sourceKB, err := s.kbService.GetKnowledgeBaseByID(ctx, payload.SourceKBID)
	if err != nil {
		return err
	}
	targetKB, err := s.kbService.GetKnowledgeBaseByID(ctx, payload.TargetKBID)
	if err != nil {
		return err
	}
	if sourceKB == nil || sourceKB.ID != payload.SourceKBID || targetKB == nil || targetKB.ID != payload.TargetKBID {
		return fmt.Errorf("invalid move binding: %w", asynq.SkipRetry)
	}
	ctx, err = access.WithKBTransferTask(
		ctx,
		sourceKB,
		targetKB,
		payload.TenantID,
		access.KBTransferMove,
		payload.TaskID,
		false,
	)
	if err != nil {
		return fmt.Errorf("invalid move scope: %v: %w", err, asynq.SkipRetry)
	}
	tenant, err := s.tenantRepo.GetTenantByID(ctx, payload.TenantID)
	if err != nil {
		return err
	}
	if tenant == nil || tenant.ID != payload.TenantID {
		return fmt.Errorf("invalid move tenant: %w", asynq.SkipRetry)
	}
	ctx = context.WithValue(ctx, types.TenantInfoContextKey, tenant)
	// No item is claimed and no failure callback can alter document state until
	// every requested document and its children passed the same pair preflight.
	retry, _ := asynq.GetRetryCount(ctx)
	maxRetry, _ := asynq.GetMaxRetry(ctx)
	items, err := s.planKnowledgeMove(ctx, sourceKB, targetKB, payload.KnowledgeIDs, payload.Mode)
	if err != nil {
		status := types.KBCloneStatusProcessing
		if retry >= maxRetry {
			status = types.KBCloneStatusFailed
		}
		_ = s.saveKnowledgeMoveProgress(
			ctx,
			&types.KnowledgeMoveProgress{
				TaskID:     payload.TaskID,
				SourceKBID: sourceKB.ID,
				TargetKBID: targetKB.ID,
				Status:     status,
				Error:      err.Error(),
				Message:    "Move preflight failed",
				UpdatedAt:  time.Now().Unix(),
			},
		)
		return err
	}
	progress := &types.KnowledgeMoveProgress{
		TaskID:     payload.TaskID,
		SourceKBID: sourceKB.ID,
		TargetKBID: targetKB.ID,
		Status:     types.KBCloneStatusProcessing,
		Total:      len(items),
		UpdatedAt:  time.Now().Unix(),
	}
	record := func(action types.AuditAction, outcome types.AuditOutcome) {
		for _, kbID := range []string{sourceKB.ID, targetKB.ID} {
			recordKBActivity(
				ctx,
				s.audit,
				payload.TenantID,
				kbID,
				action,
				"knowledge_move",
				payload.TaskID,
				outcome,
				map[string]any{
					"source_kb_id": sourceKB.ID,
					"target_kb_id": targetKB.ID,
					"task_id":      payload.TaskID,
					"count":        len(items),
					"mode":         payload.Mode,
				},
			)
		}
	}
	if retry == 0 {
		record(types.AuditActionKnowledgeMoveStarted, types.AuditOutcomeAccepted)
	}
	var failures error
	for _, item := range items {
		if err := s.moveOneKnowledge(ctx, item.ID, sourceKB, targetKB, payload.Mode); err != nil {
			failures = errors.Join(failures, fmt.Errorf("knowledge %s: %w", item.ID, err))
			if retry >= maxRetry {
				s.markMoveItemFailed(ctx, item.ID, sourceKB, targetKB, payload.Mode, err)
			}
			progress.Failed++
		}
		progress.Processed++
		progress.Progress = progress.Processed * 100 / progress.Total
		progress.Message = fmt.Sprintf(
			"Moved %d/%d knowledge items",
			progress.Processed-progress.Failed,
			progress.Total,
		)
		progress.UpdatedAt = time.Now().Unix()
		_ = s.saveKnowledgeMoveProgress(ctx, progress)
	}
	if failures != nil {
		progress.Error = failures.Error()
		if retry >= maxRetry {
			progress.Status = types.KBCloneStatusFailed
			outcome := types.AuditOutcomeFailed
			if progress.Failed < progress.Total {
				outcome = types.AuditOutcomePartial
			}
			record(types.AuditActionKnowledgeMoveFailed, outcome)
		}
		_ = s.saveKnowledgeMoveProgress(ctx, progress)
		// Return an error for partial failures too. Completed items are recognized
		// from their persisted task marker and skipped on the next delivery.
		return failures
	}
	progress.Status = types.KBCloneStatusCompleted
	progress.Progress = 100
	progress.Error = ""
	_ = s.saveKnowledgeMoveProgress(ctx, progress)
	record(types.AuditActionKnowledgeMoveCompleted, types.AuditOutcomeSuccess)
	return nil
}

// moveOneKnowledge moves a single knowledge item from source KB to target KB.
func (s *knowledgeService) moveOneKnowledge(
	ctx context.Context,
	knowledgeID string,
	sourceKB, targetKB *types.KnowledgeBase,
	mode string,
) error {
	if err := access.RequireKBTransfer(ctx, sourceKB, targetKB, access.KBTransferMove); err != nil {
		return err
	}
	tenant, _ := ctx.Value(types.TenantInfoContextKey).(*types.Tenant)
	if err := access.ValidateKBTransferCompatibility(sourceKB,
		targetKB,
		access.KBTransferMove,
		mode,
		tenant); err != nil {
		return err
	}
	tenantID := sourceKB.TenantID
	knowledge, err := s.repo.GetKnowledgeByID(ctx, tenantID, knowledgeID)
	if err != nil {
		return err
	}
	if knowledge == nil || knowledge.ID != knowledgeID {
		return access.ErrNotFound
	}
	if err := validateMoveItem(ctx, knowledge, sourceKB, targetKB, mode); err != nil {
		return err
	}
	copyOfKnowledge := *knowledge
	knowledge = &copyOfKnowledge
	state, err := transferState(knowledge)
	if err != nil {
		return err
	}
	if matchesTransfer(ctx, state, sourceKB, targetKB, access.KBTransferMove, knowledgeID, mode) {
		if state.Phase == "done" || state.Phase == "reparse_pending" {
			if err := s.cleanupMovedSourceWiki(ctx, knowledge, sourceKB, targetKB); err != nil {
				return err
			}
			if state.Phase == "reparse_pending" {
				return s.enqueueMovedKnowledge(ctx, knowledge, sourceKB, targetKB)
			}
			if mode == "reuse_vectors" && targetKB.IsWikiEnabled() {
				_, err := EnqueueWikiIngest(ctx, s.task, s.taskPendingRepo, tenantID, targetKB.ID, knowledge.ID)
				return err
			}
			return nil
		}
	} else {
		state = &knowledgeTransferState{
			TaskID:    access.TransferTaskID(ctx),
			Operation: access.KBTransferMove,
			SourceKB:  sourceKB.ID,
			TargetKB:  targetKB.ID,
			SourceID:  knowledge.ID,
			Mode:      mode,
			Phase:     "moving",
		}
		if sourceKB.IsWikiEnabled() {
			chunks, err := s.transferChunks(ctx, knowledge, sourceKB.ID)
			if err != nil {
				return err
			}
			for _, chunk := range chunks {
				state.WikiChunkIDs = append(state.WikiChunkIDs, chunk.ID)
			}
			state.WikiSummary = knowledge.Description
		}
		before := *knowledge
		knowledge.ParseStatus = types.ParseStatusProcessing
		if err := setTransferState(knowledge, *state); err != nil {
			return err
		}
		if err := s.repo.UpdateKnowledgeForTransfer(ctx, &before, knowledge); err != nil {
			return err
		}
	}

	switch mode {
	case "reuse_vectors":
		if err := s.moveKnowledgeReuseVectors(ctx, knowledge, sourceKB, targetKB); err != nil {
			return err
		}
		if err := s.cleanupMovedSourceWiki(ctx, knowledge, sourceKB, targetKB); err != nil {
			return err
		}
		// reparse re-ingests through KnowledgePostProcess once the new chunks
		// land; reuse_vectors keeps the existing chunks and never re-enters that
		// pipeline, so the target KB has to be told about the document here.
		if targetKB.IsWikiEnabled() {
			_, err := EnqueueWikiIngest(ctx, s.task, s.taskPendingRepo, tenantID, targetKB.ID, knowledge.ID)
			return err
		}
		return nil
	case "reparse":
		return s.moveKnowledgeReparse(ctx, knowledge, sourceKB, targetKB)
	default:
		return fmt.Errorf("unknown move mode: %s", mode)
	}
}

// moveKnowledgeReuseVectors relocates vector metadata while retaining physical IDs.
func (s *knowledgeService) moveKnowledgeReuseVectors(
	ctx context.Context,
	knowledge *types.Knowledge,
	sourceKB, targetKB *types.KnowledgeBase,
) error {
	tenantID := ctx.Value(types.TenantIDContextKey).(uint64)

	// Metadata relocation is supported only within the same vector store.
	// Cross-store moves must reparse into the target store.
	if !sourceKB.SharesStoreWith(targetKB) {
		return fmt.Errorf(
			"reuse_vectors move across different vector stores is not supported "+
				"(source KB %s, target KB %s); use reparse mode", sourceKB.ID, targetKB.ID)
	}

	if err := access.RequireKBTransfer(ctx, sourceKB, targetKB, access.KBTransferMove); err != nil {
		return err
	}
	oldChunks, err := s.transferChunks(ctx, knowledge, sourceKB.ID, targetKB.ID)
	if err != nil {
		return err
	}
	chunkIDs := make([]string, 0, len(oldChunks))
	for _, chunk := range oldChunks {
		chunkIDs = append(chunkIDs, chunk.ID)
	}
	if knowledge.EmbeddingModelID != "" {
		engine, err := retriever.CreateRetrieveEngineForKB(
			ctx,
			s.retrieveEngine,
			s.ownership,
			tenantID,
			sourceKB.VectorStoreID,
		)
		if err != nil {
			return err
		}
		model, err := s.modelService.GetEmbeddingModel(ctx, knowledge.EmbeddingModelID)
		if err != nil {
			return err
		}
		// Move index metadata in place. Copy + delete by the unchanged document ID
		// would also delete the just-created target indices.
		if err := engine.MoveKnowledgeIndices(ctx,
			sourceKB.ID,
			targetKB.ID,
			knowledge.ID,
			chunkIDs,
			model.GetDimensions(),
			sourceKB.Type); err != nil {
			return err
		}
	}

	// Source graph namespaces must not keep exposing a document after it
	// leaves the KB. The exact namespace deletion is repeatable on retry.
	if s.graphEngine != nil {
		if err := s.graphEngine.DelGraph(ctx,
			[]types.NameSpace{{
				KnowledgeBase: sourceKB.ID,
				Knowledge:     knowledge.ID,
			}}); err != nil {
			return err
		}
	}

	// 3. Update chunks' knowledge_base_id in DB
	if err := s.chunkRepo.MoveChunksByKnowledgeID(ctx, tenantID, knowledge.ID, targetKB.ID); err != nil {
		return fmt.Errorf("failed to move chunks: %w", err)
	}

	// 4. Update knowledge record (tags are KB-scoped; clear relations before moving)
	if err := s.repo.DeleteKnowledgeTagRelations(ctx, knowledge.ID); err != nil {
		return fmt.Errorf("failed to clear knowledge tag relations: %w", err)
	}
	before := *knowledge
	knowledge.KnowledgeBaseID = targetKB.ID
	knowledge.ParseStatus = types.ParseStatusCompleted
	knowledge.ErrorMessage = ""
	state, err := transferState(knowledge)
	if err != nil || state == nil {
		return fmt.Errorf("move state unavailable")
	}
	state.Phase = "done"
	if err := setTransferState(knowledge, *state); err != nil {
		return err
	}
	if err := s.repo.UpdateKnowledgeForTransfer(ctx, &before, knowledge); err != nil {
		return err
	}

	return nil
}

// moveKnowledgeReparse moves knowledge to target KB and re-parses it with target KB's configuration.
func (s *knowledgeService) moveKnowledgeReparse(
	ctx context.Context,
	knowledge *types.Knowledge,
	sourceKB, targetKB *types.KnowledgeBase,
) error {
	if err := access.RequireKBTransfer(ctx, sourceKB, targetKB, access.KBTransferMove); err != nil {
		return err
	}
	before := *knowledge
	cleanup := *knowledge
	cleanup.StorageSize = 0
	if err := s.cleanupKnowledgeResources(ctx, &cleanup); err != nil {
		return fmt.Errorf("failed to clean up source: %w", err)
	}
	if err := s.repo.DeleteKnowledgeTagRelations(ctx, knowledge.ID); err != nil {
		return err
	}
	knowledge.KnowledgeBaseID = targetKB.ID
	knowledge.EmbeddingModelID = targetKB.EmbeddingModelID
	knowledge.ParseStatus = types.ParseStatusPending
	knowledge.ErrorMessage = ""
	knowledge.EnableStatus = "disabled"
	knowledge.Description = ""
	knowledge.ProcessedAt = nil
	knowledge.StorageSize = 0
	state, err := transferState(knowledge)
	if err != nil || state == nil {
		return fmt.Errorf("move state unavailable")
	}
	state.Phase = "reparse_pending"
	if err := setTransferState(knowledge, *state); err != nil {
		return err
	}
	if err := s.repo.UpdateKnowledgeForTransfer(ctx, &before, knowledge); err != nil {
		return err
	}
	if err := s.cleanupMovedSourceWiki(ctx, knowledge, sourceKB, targetKB); err != nil {
		return err
	}
	return s.enqueueMovedKnowledge(ctx, knowledge, sourceKB, targetKB)
}

func (s *knowledgeService) enqueueMovedKnowledge(
	ctx context.Context,
	knowledge *types.Knowledge,
	sourceKB, targetKB *types.KnowledgeBase,
) error {
	if err := access.RequireKBTransfer(ctx, sourceKB, targetKB, access.KBTransferMove); err != nil {
		return err
	}
	if knowledge.ParseStatus == types.ParseStatusFailed {
		before := *knowledge
		knowledge.ParseStatus = types.ParseStatusPending
		knowledge.ErrorMessage = ""
		if err := s.repo.UpdateKnowledgeForTransfer(ctx, &before, knowledge); err != nil {
			return err
		}
	}
	tenantID := knowledge.TenantID
	taskID := "move-reparse:" + access.TransferTaskID(ctx) + ":" + knowledge.ID
	// 3. Enqueue document processing task with target KB's configuration
	if knowledge.IsManual() {
		meta, err := knowledge.ManualMetadata()
		if err != nil || meta == nil {
			return fmt.Errorf("failed to get manual metadata for reparse: %w", err)
		}
		_, err = s.enqueueManualProcessing(
			ctx,
			knowledge,
			meta.Content,
			false,
			asynq.TaskID(taskID),
			asynq.Retention(7*24*time.Hour),
		)
		if err != nil && !errors.Is(err, asynq.ErrTaskIDConflict) && !errors.Is(err, asynq.ErrDuplicateTask) {
			return err
		}
		return s.acknowledgeMovedReparse(ctx, knowledge, sourceKB, targetKB)
	}

	if knowledge.FilePath != "" {
		enableMultimodel := targetKB.IsMultimodalEnabled()
		enableQuestionGeneration := false
		questionCount := 3
		if targetKB.QuestionGenerationConfig != nil && targetKB.QuestionGenerationConfig.Enabled {
			enableQuestionGeneration = true
			if targetKB.QuestionGenerationConfig.QuestionCount > 0 {
				questionCount = targetKB.QuestionGenerationConfig.QuestionCount
			}
		}

		lang := types.LanguageFromContextOrDefault(ctx)
		taskPayload := types.DocumentProcessPayload{
			TenantID:                 tenantID,
			KnowledgeID:              knowledge.ID,
			KnowledgeBaseID:          targetKB.ID,
			FilePath:                 knowledge.FilePath,
			FileName:                 knowledge.FileName,
			FileType:                 getFileType(knowledge.FileName),
			EnableMultimodel:         enableMultimodel,
			EnableQuestionGeneration: enableQuestionGeneration,
			QuestionCount:            questionCount,
			Language:                 lang,
		}

		langfuse.InjectTracing(ctx, &taskPayload)
		payloadBytes, err := json.Marshal(taskPayload)
		if err != nil {
			return fmt.Errorf("failed to marshal document process payload: %w", err)
		}

		task := asynq.NewTask(
			types.TypeDocumentProcess,
			payloadBytes,
			documentProcessTaskOptions(
				s.config,
				asynq.MaxRetry(3),
				asynq.TaskID(taskID),
				asynq.Retention(7*24*time.Hour),
			)...)
		info, err := s.task.Enqueue(task)
		if err != nil && !errors.Is(err, asynq.ErrTaskIDConflict) && !errors.Is(err, asynq.ErrDuplicateTask) {
			return fmt.Errorf("failed to enqueue document process task: %w", err)
		}
		_ = info
	}

	return s.acknowledgeMovedReparse(ctx, knowledge, sourceKB, targetKB)
}

// getOrCreateTagInTarget finds or creates a tag in the target knowledge base based on the source tag.
// It looks up the source tag by ID, then tries to find a tag with the same name in the target KB.
// If not found, it creates a new tag with the same properties.
// The mapping is cached in tagIDMapping for subsequent lookups.
func (s *knowledgeService) getOrCreateTagInTarget(
	ctx context.Context,
	srcTenantID, dstTenantID uint64,
	dstKnowledgeBaseID string,
	srcTagID string,
	tagIDMapping map[string]string,
) string {
	// Get source tag
	srcTag, err := s.tagRepo.GetByID(ctx, srcTenantID, srcTagID)
	if err != nil || srcTag == nil {
		logger.Warnf(ctx, "Failed to get source tag %s: %v", srcTagID, err)
		tagIDMapping[srcTagID] = "" // Cache empty result to avoid repeated lookups
		return ""
	}

	// Try to find existing tag with same name in target KB
	dstTag, err := s.tagRepo.GetByName(ctx, dstTenantID, dstKnowledgeBaseID, srcTag.Name)
	if err == nil && dstTag != nil {
		tagIDMapping[srcTagID] = dstTag.ID
		return dstTag.ID
	}

	// Create new tag in target KB
	// "未分类" tag should have the lowest sort order to appear first
	sortOrder := srcTag.SortOrder
	if srcTag.Name == types.UntaggedTagName {
		sortOrder = -1
	}
	newTag := &types.KnowledgeTag{
		ID:              uuid.New().String(),
		TenantID:        dstTenantID,
		KnowledgeBaseID: dstKnowledgeBaseID,
		Name:            srcTag.Name,
		Color:           srcTag.Color,
		SortOrder:       sortOrder,
		CreatedAt:       time.Now(),
		UpdatedAt:       time.Now(),
	}
	if err := s.tagRepo.Create(ctx, newTag); err != nil {
		logger.Warnf(ctx, "Failed to create tag %s in target KB: %v", srcTag.Name, err)
		tagIDMapping[srcTagID] = "" // Cache empty result
		return ""
	}

	tagIDMapping[srcTagID] = newTag.ID
	logger.Infof(ctx, "Created tag %s (ID: %s) in target KB %s", newTag.Name, newTag.ID, dstKnowledgeBaseID)
	return newTag.ID
}
