package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/application/service/retriever"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/hibiken/asynq"
	"golang.org/x/sync/errgroup"
)

// collectImageURLs extracts unique provider:// image URLs from image_info JSON strings.
func collectImageURLs(ctx context.Context, imageInfos []string) []string {
	seen := make(map[string]struct{})
	var urls []string
	for _, info := range imageInfos {
		if info == "" {
			continue
		}
		var images []*types.ImageInfo
		if err := json.Unmarshal([]byte(info), &images); err != nil {
			logger.Warnf(ctx, "Failed to parse image_info JSON: %v", err)
			continue
		}
		for _, img := range images {
			if img.URL != "" {
				if _, exists := seen[img.URL]; !exists {
					seen[img.URL] = struct{}{}
					urls = append(urls, img.URL)
				}
			}
		}
	}
	return urls
}

// deleteExtractedImages deletes all extracted image files from storage.
// Standalone function — callable from both knowledgeService and knowledgeBaseService.
// Errors are logged but do not fail the overall deletion.
func deleteExtractedImages(ctx context.Context, fileSvc interfaces.FileService, imageURLs []string) {
	if len(imageURLs) == 0 {
		return
	}
	logger.Infof(ctx, "Deleting %d extracted images", len(imageURLs))
	for _, url := range imageURLs {
		if err := fileSvc.DeleteFile(ctx, url); err != nil {
			logger.Errorf(ctx, "Failed to delete extracted image %s: %v", url, err)
		}
	}
}

// DeleteKnowledge deletes a knowledge entry and all related resources
func (s *knowledgeService) DeleteKnowledge(ctx context.Context, id string) error {
	// Get the knowledge entry
	knowledge, err := s.repo.GetKnowledgeByID(ctx, ctx.Value(types.TenantIDContextKey).(uint64), id)
	if err != nil {
		return err
	}

	// Mark as deleting first to prevent async task conflicts
	// This ensures that any running async tasks will detect the deletion and abort
	originalStatus := knowledge.ParseStatus
	knowledge.ParseStatus = types.ParseStatusDeleting
	knowledge.UpdatedAt = time.Now()
	if err := s.repo.UpdateKnowledge(ctx, knowledge); err != nil {
		logger.GetLogger(ctx).WithField("error", err).Errorf("DeleteKnowledge failed to mark as deleting")
		// Continue with deletion even if marking fails
	} else {
		logger.Infof(ctx, "Marked knowledge %s as deleting (previous status: %s)", id, originalStatus)
	}

	// Resolve file service for this KB before spawning goroutines
	kb, _ := s.kbService.GetKnowledgeBaseByID(ctx, knowledge.KnowledgeBaseID)
	kbFileSvc := s.resolveFileService(ctx, kb)

	// Collect image URLs before chunks are deleted (ImageInfo references are lost after deletion)
	tenantID := ctx.Value(types.TenantIDContextKey).(uint64)
	chunkImageInfos, err := s.chunkService.GetRepository().ListImageInfoByKnowledgeIDs(ctx, tenantID, []string{id})
	if err != nil {
		logger.Errorf(ctx, "Failed to collect image URLs for cleanup: %v", err)
	}
	var imageInfoStrs []string
	for _, ci := range chunkImageInfos {
		imageInfoStrs = append(imageInfoStrs, ci.ImageInfo)
	}
	imageURLs := collectImageURLs(ctx, imageInfoStrs)

	wg := errgroup.Group{}
	// Delete knowledge embeddings from vector store.
	// Skip entirely when the knowledge has no embedding model (e.g. Wiki-only KB):
	// nothing was ever written to the vector store, so there is nothing to delete,
	// and GetEmbeddingModel would fail with "model ID cannot be empty".
	if strings.TrimSpace(knowledge.EmbeddingModelID) != "" {
		wg.Go(func() error {
			tenantInfo := ctx.Value(types.TenantInfoContextKey).(*types.Tenant)
			retrieveEngine, err := retriever.NewCompositeRetrieveEngine(
				s.retrieveEngine,
				tenantInfo.GetEffectiveEngines(),
			)
			if err != nil {
				logger.GetLogger(ctx).WithField("error", err).Errorf("DeleteKnowledge delete knowledge embedding failed")
				return err
			}
			embeddingModel, err := s.modelService.GetEmbeddingModel(ctx, knowledge.EmbeddingModelID)
			if err != nil {
				logger.GetLogger(ctx).WithField("error", err).Errorf("DeleteKnowledge delete knowledge embedding failed")
				return err
			}
			if err := retrieveEngine.DeleteByKnowledgeIDList(ctx, []string{knowledge.ID}, embeddingModel.GetDimensions(), knowledge.Type); err != nil {
				logger.GetLogger(ctx).WithField("error", err).Errorf("DeleteKnowledge delete knowledge embedding failed")
				return err
			}
			return nil
		})
	} else {
		logger.Infof(ctx, "Knowledge %s has no embedding model, skipping vector store cleanup", knowledge.ID)
	}

	// Delete all chunks associated with this knowledge
	wg.Go(func() error {
		if err := s.chunkService.DeleteChunksByKnowledgeID(ctx, knowledge.ID); err != nil {
			logger.GetLogger(ctx).WithField("error", err).Errorf("DeleteKnowledge delete chunks failed")
			return err
		}
		return nil
	})

	// Delete the physical file and extracted images if they exist
	wg.Go(func() error {
		if knowledge.FilePath != "" {
			if err := kbFileSvc.DeleteFile(ctx, knowledge.FilePath); err != nil {
				logger.GetLogger(ctx).WithField("error", err).Errorf("DeleteKnowledge delete file failed")
			}
		}
		deleteExtractedImages(ctx, kbFileSvc, imageURLs)
		tenantInfo := ctx.Value(types.TenantInfoContextKey).(*types.Tenant)
		tenantInfo.StorageUsed -= knowledge.StorageSize
		if err := s.tenantRepo.AdjustStorageUsed(ctx, tenantInfo.ID, -knowledge.StorageSize); err != nil {
			logger.GetLogger(ctx).WithField("error", err).Errorf("DeleteKnowledge update tenant storage used failed")
		}
		return nil
	})

	// Delete the knowledge graph
	wg.Go(func() error {
		namespace := types.NameSpace{KnowledgeBase: knowledge.KnowledgeBaseID, Knowledge: knowledge.ID}
		if err := s.graphEngine.DelGraph(ctx, []types.NameSpace{namespace}); err != nil {
			logger.GetLogger(ctx).WithField("error", err).Errorf("DeleteKnowledge delete knowledge graph failed")
			return err
		}
		return nil
	})

	// Clean up wiki pages that reference this knowledge. Pass the full
	// knowledge object so cleanup can source title/summary from the row
	// itself rather than reaching into possibly-not-yet-written wiki pages.
	if kb != nil && kb.IsWikiEnabled() {
		wg.Go(func() error {
			s.cleanupWikiOnKnowledgeDelete(ctx, knowledge)
			return nil
		})
	}

	if err = wg.Wait(); err != nil {
		return err
	}
	// Delete the knowledge entry itself from the database
	return s.repo.DeleteKnowledge(ctx, ctx.Value(types.TenantIDContextKey).(uint64), id)
}

// cleanupWikiOnKnowledgeDelete handles wiki pages when a source document is deleted.
//
// There are three sources of truth we must keep consistent:
//   - The knowledge row (being soft-deleted right now by the caller)
//   - Wiki pages whose source_refs include this knowledge
//   - Pending/in-flight wiki_ingest tasks that may create *new* pages pointing at it
//
// The function is deliberately best-effort and idempotent:
//   - It writes a tombstone + scrubs pending ingest ops so new pages cannot be
//     born with a stale source_ref (guards (a) queued ingest and (b) ingest
//     tasks mid-LLM call — both consult the tombstone before writing).
//   - It immediately reconciles any pages already present (delete-if-only-ref
//     or strip-ref-if-multi).
//   - It *unconditionally* enqueues a retract task. Crucially we DO NOT gate
//     enqueue on "pages currently exist": in the ingest/delete race the
//     knowledge may have pages that exist only after this function returns
//     (the ingest task fires later and, absent the tombstone, would have
//     created them). The retract handler re-queries ListPagesBySourceRef at
//     run time, so even with an empty PageSlugs it will do the right thing —
//     and at worst it's a cheap no-op.
func (s *knowledgeService) cleanupWikiOnKnowledgeDelete(ctx context.Context, knowledge *types.Knowledge) {
	if knowledge == nil {
		return
	}
	kbID := knowledge.KnowledgeBaseID
	knowledgeID := knowledge.ID
	if kbID == "" || knowledgeID == "" {
		return
	}

	// (1) Tombstone + scrub pending ingest — must happen first so any
	// wiki_ingest task that wakes up between here and the retract enqueue
	// below sees "knowledge gone" and bails out.
	s.markKnowledgeDeletedForWiki(ctx, kbID, knowledgeID)
	s.scrubWikiPendingIngest(ctx, kbID, knowledgeID, "cleanup")

	// Pull title/summary from the knowledge itself — do NOT read them from
	// existing wiki pages. In the race window wiki pages may not exist yet,
	// and even when they do their "summary" is the LLM-extracted one which
	// we're about to invalidate anyway. The knowledge row still has the
	// original Title/FileName/Description, which is what the retract prompt
	// actually wants.
	docTitle := knowledge.Title
	if docTitle == "" {
		docTitle = knowledge.FileName
	}
	if docTitle == "" {
		docTitle = knowledgeID
	}
	docSummary := knowledge.Description

	// (2) Immediate reconciliation for pages already present. If ingest
	// hasn't run yet this simply finds nothing; that's fine — see (3).
	pages, err := s.wikiRepo.ListBySourceRef(ctx, kbID, knowledgeID)
	if err != nil {
		logger.Warnf(ctx, "wiki cleanup: failed to list pages by source ref %s: %v", knowledgeID, err)
		pages = nil
	}

	// Prefer the on-disk summary if the summary page already exists (it's
	// richer than the raw user-provided description). Leave docSummary
	// untouched otherwise so we still pass something meaningful downstream.
	for _, page := range pages {
		if page.PageType == types.WikiPageTypeSummary && page.Summary != "" {
			docSummary = page.Summary
			break
		}
	}

	var deletedSlugs []string
	var retractSlugs []string
	for _, page := range pages {
		if page.PageType == types.WikiPageTypeIndex || page.PageType == types.WikiPageTypeLog {
			continue
		}

		remaining := removeSourceRef(page.SourceRefs, knowledgeID)

		if len(remaining) == 0 {
			if err := s.wikiService.DeletePage(ctx, kbID, page.Slug); err != nil {
				logger.Warnf(ctx, "wiki cleanup: failed to delete page %s: %v", page.Slug, err)
			} else {
				deletedSlugs = append(deletedSlugs, page.Slug)
			}
		} else {
			page.SourceRefs = remaining
			if err := s.wikiService.UpdatePageMeta(ctx, page); err != nil {
				logger.Warnf(ctx, "wiki cleanup: failed to update source refs for page %s: %v", page.Slug, err)
			} else {
				retractSlugs = append(retractSlugs, page.Slug)
			}
		}
	}

	if len(deletedSlugs) > 0 {
		logger.Infof(ctx, "wiki cleanup: deleted %d pages after knowledge %s deletion: %v",
			len(deletedSlugs), knowledgeID, deletedSlugs)
	}

	allAffectedSlugs := append(retractSlugs, deletedSlugs...)

	// (3) Unconditionally enqueue the retract task. See function comment —
	// an empty PageSlugs is not a bug, it's the signal "re-query at run
	// time". The handler will ListPagesBySourceRef again, pick up any
	// pages that materialised after we looked, and also rebuild index/log
	// so the knowledge's disappearance is reflected in the UI.
	lang, _ := types.LanguageFromContext(ctx)
	tenantID, _ := types.TenantIDFromContext(ctx)
	EnqueueWikiRetract(ctx, s.task, s.taskPendingRepo, WikiRetractPayload{
		TenantID:        tenantID,
		KnowledgeBaseID: kbID,
		KnowledgeID:     knowledgeID,
		DocTitle:        docTitle,
		DocSummary:      docSummary,
		Language:        lang,
		PageSlugs:       allAffectedSlugs,
	})
	logger.Infof(ctx, "wiki cleanup: enqueued retract task for knowledge %s (%d known slugs: %v)",
		knowledgeID, len(allAffectedSlugs), allAffectedSlugs)
}

// markKnowledgeDeletedForWiki writes a short-TTL tombstone so any wiki_ingest
// task still running or queued for this knowledge can short-circuit before
// resurrecting a page with a stale source_ref. No-op when Redis is absent.
func (s *knowledgeService) markKnowledgeDeletedForWiki(ctx context.Context, kbID, knowledgeID string) {
	if s.redisClient == nil || kbID == "" || knowledgeID == "" {
		return
	}
	key := WikiDeletedTombstoneKey(kbID, knowledgeID)
	if err := s.redisClient.Set(ctx, key, "1", wikiDeletedTTL).Err(); err != nil {
		logger.Warnf(ctx, "wiki cleanup: failed to write tombstone %s: %v", key, err)
	}
}

// scrubWikiPendingIngest removes queued WikiOpIngest entries for a knowledge
// from task_pending_ops. Used by both the delete path (we're about to
// soft-delete the doc, no point ingesting it) and the reparse path (the
// old chunks are about to vanish, so any pending ingest would either race
// with the cleanup or no-op on an empty chunk set — and the post-process
// task will enqueue a fresh ingest once new chunks land anyway).
//
// Retract entries stay put — delete still needs them to unlink referencing
// pages, and reparse never enqueues retracts for the doc being reparsed.
// We pass op=WikiOpIngest so DeleteByDedupKey filters to the ingest rows
// only.
func (s *knowledgeService) scrubWikiPendingIngest(ctx context.Context, kbID, knowledgeID, reason string) {
	if s.taskPendingRepo == nil || kbID == "" || knowledgeID == "" {
		return
	}
	if err := s.taskPendingRepo.DeleteByDedupKey(ctx, wikiTaskType, wikiTaskScope, kbID, knowledgeID, WikiOpIngest); err != nil {
		logger.Warnf(ctx, "wiki %s: failed to scrub pending ingest ops for knowledge %s: %v", reason, knowledgeID, err)
		return
	}
	logger.Infof(ctx, "wiki %s: scrubbed pending ingest ops for knowledge %s", reason, knowledgeID)
}

// prepareWikiForReparse is the reparse counterpart to
// cleanupWikiOnKnowledgeDelete. It aligns reparse with the same "pending
// queue hygiene" the delete path already enforces, without taking any
// destructive action against existing pages.
//
// Why no retract / tombstone here: reparse is not a "K is gone" event, it's
// a "K's contribution is about to be swapped for a new version" event. The
// actual swap happens asynchronously inside mapOneDocument (see its
// oldPageSlugs handling) — that's where we have both the old page set and
// the freshly extracted candidate slugs, which is exactly the information
// the WikiPageModifyPrompt needs to do a correct replace-not-append.
//
// So the only thing worth doing synchronously at reparse time is keeping
// the Redis pending list clean so the re-ingest enqueued by
// KnowledgePostProcess doesn't race with a stale ingest op that would
// fire mid-flight against zero chunks.
func (s *knowledgeService) prepareWikiForReparse(ctx context.Context, knowledge *types.Knowledge) {
	if knowledge == nil {
		return
	}
	kbID := knowledge.KnowledgeBaseID
	knowledgeID := knowledge.ID
	if kbID == "" || knowledgeID == "" {
		return
	}
	s.scrubWikiPendingIngest(ctx, kbID, knowledgeID, "reparse")
}

// removeSourceRef removes entries from source_refs that match a knowledge ID.
// Handles both old format ("knowledgeID") and new format ("knowledgeID|title").
func removeSourceRef(refs types.StringArray, knowledgeID string) types.StringArray {
	var result types.StringArray
	prefix := knowledgeID + "|"
	for _, ref := range refs {
		if ref == knowledgeID || strings.HasPrefix(ref, prefix) {
			continue
		}
		result = append(result, ref)
	}
	return result
}

// DeleteKnowledgeList deletes a knowledge entry and all related resources
func (s *knowledgeService) DeleteKnowledgeList(ctx context.Context, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	// 1. Get the knowledge entry
	tenantInfo := ctx.Value(types.TenantInfoContextKey).(*types.Tenant)
	knowledgeList, err := s.repo.GetKnowledgeBatch(ctx, tenantInfo.ID, ids)
	if err != nil {
		return err
	}

	// Mark all as deleting first to prevent async task conflicts
	for _, knowledge := range knowledgeList {
		knowledge.ParseStatus = types.ParseStatusDeleting
		knowledge.UpdatedAt = time.Now()
		if err := s.repo.UpdateKnowledge(ctx, knowledge); err != nil {
			logger.GetLogger(ctx).WithField("error", err).WithField("knowledge_id", knowledge.ID).
				Errorf("DeleteKnowledgeList failed to mark as deleting")
			// Continue with deletion even if marking fails
		}
	}
	logger.Infof(ctx, "Marked %d knowledge entries as deleting", len(knowledgeList))

	// Pre-resolve file services per KB so goroutines don't need DB access
	kbFileServices := make(map[string]interfaces.FileService)
	for _, knowledge := range knowledgeList {
		if _, ok := kbFileServices[knowledge.KnowledgeBaseID]; !ok {
			kb, _ := s.kbService.GetKnowledgeBaseByID(ctx, knowledge.KnowledgeBaseID)
			kbFileServices[knowledge.KnowledgeBaseID] = s.resolveFileService(ctx, kb)
		}
	}

	// Collect image URLs before chunks are deleted
	chunkImageInfos, err := s.chunkService.GetRepository().ListImageInfoByKnowledgeIDs(ctx, tenantInfo.ID, ids)
	if err != nil {
		logger.Errorf(ctx, "Failed to collect image URLs for batch cleanup: %v", err)
	}
	knowledgeToKB := make(map[string]string)
	for _, k := range knowledgeList {
		knowledgeToKB[k.ID] = k.KnowledgeBaseID
	}
	kbImageInfos := make(map[string][]string) // kbID → []imageInfo JSON
	for _, ci := range chunkImageInfos {
		kbID := knowledgeToKB[ci.KnowledgeID]
		kbImageInfos[kbID] = append(kbImageInfos[kbID], ci.ImageInfo)
	}
	kbImageURLs := make(map[string][]string) // kbID → []imageURL (deduplicated)
	for kbID, infos := range kbImageInfos {
		kbImageURLs[kbID] = collectImageURLs(ctx, infos)
	}

	wg := errgroup.Group{}
	// 2. Delete knowledge embeddings from vector store
	wg.Go(func() error {
		tenantInfo := ctx.Value(types.TenantInfoContextKey).(*types.Tenant)
		retrieveEngine, err := retriever.NewCompositeRetrieveEngine(
			s.retrieveEngine,
			tenantInfo.GetEffectiveEngines(),
		)
		if err != nil {
			logger.GetLogger(ctx).WithField("error", err).Errorf("DeleteKnowledge delete knowledge embedding failed")
			return err
		}
		// Group by EmbeddingModelID and Type
		type groupKey struct {
			EmbeddingModelID string
			Type             string
		}
		group := map[groupKey][]string{}
		for _, knowledge := range knowledgeList {
			key := groupKey{EmbeddingModelID: knowledge.EmbeddingModelID, Type: knowledge.Type}
			group[key] = append(group[key], knowledge.ID)
		}
		for key, knowledgeIDs := range group {
			// Wiki-only knowledge never had embeddings written to the vector store,
			// and its EmbeddingModelID is intentionally empty. Skip the whole group
			// to avoid the spurious "model ID cannot be empty" failure.
			if strings.TrimSpace(key.EmbeddingModelID) == "" {
				logger.Infof(ctx, "Skipping vector store cleanup for %d knowledge entries without embedding model", len(knowledgeIDs))
				continue
			}
			embeddingModel, err := s.modelService.GetEmbeddingModel(ctx, key.EmbeddingModelID)
			if err != nil {
				logger.GetLogger(ctx).WithField("error", err).Errorf("DeleteKnowledge get embedding model failed")
				return err
			}
			if err := retrieveEngine.DeleteByKnowledgeIDList(ctx, knowledgeIDs, embeddingModel.GetDimensions(), key.Type); err != nil {
				logger.GetLogger(ctx).
					WithField("error", err).
					Errorf("DeleteKnowledge delete knowledge embedding failed")
				return err
			}
		}
		return nil
	})

	// 3. Delete all chunks associated with this knowledge
	wg.Go(func() error {
		if err := s.chunkService.DeleteByKnowledgeList(ctx, ids); err != nil {
			logger.GetLogger(ctx).WithField("error", err).Errorf("DeleteKnowledge delete chunks failed")
			return err
		}
		return nil
	})

	// 4. Delete the physical file and extracted images if they exist
	wg.Go(func() error {
		storageAdjust := int64(0)
		for _, knowledge := range knowledgeList {
			if knowledge.FilePath != "" {
				fSvc := kbFileServices[knowledge.KnowledgeBaseID]
				if err := fSvc.DeleteFile(ctx, knowledge.FilePath); err != nil {
					logger.GetLogger(ctx).WithField("error", err).Errorf("DeleteKnowledge delete file failed")
				}
			}
			storageAdjust -= knowledge.StorageSize
		}
		// Delete extracted images per KB
		for kbID, urls := range kbImageURLs {
			fSvc := kbFileServices[kbID]
			if fSvc == nil {
				logger.Warnf(ctx, "No file service for KB %s, skipping %d image deletions", kbID, len(urls))
				continue
			}
			deleteExtractedImages(ctx, fSvc, urls)
		}
		tenantInfo.StorageUsed += storageAdjust
		if err := s.tenantRepo.AdjustStorageUsed(ctx, tenantInfo.ID, storageAdjust); err != nil {
			logger.GetLogger(ctx).WithField("error", err).Errorf("DeleteKnowledge update tenant storage used failed")
		}
		return nil
	})

	// Delete the knowledge graph
	wg.Go(func() error {
		namespaces := []types.NameSpace{}
		for _, knowledge := range knowledgeList {
			namespaces = append(
				namespaces,
				types.NameSpace{KnowledgeBase: knowledge.KnowledgeBaseID, Knowledge: knowledge.ID},
			)
		}
		if err := s.graphEngine.DelGraph(ctx, namespaces); err != nil {
			logger.GetLogger(ctx).WithField("error", err).Errorf("DeleteKnowledge delete knowledge graph failed")
			return err
		}
		return nil
	})

	// Clean up wiki pages that reference deleted knowledge. cleanup needs
	// the full knowledge object (Title / Description) so the retract prompt
	// can describe the vanished document even when wiki pages haven't been
	// ingested yet — which is common in the batch-delete-shortly-after-upload
	// flow.
	wg.Go(func() error {
		for _, knowledge := range knowledgeList {
			kb, _ := s.kbService.GetKnowledgeBaseByID(ctx, knowledge.KnowledgeBaseID)
			if kb != nil && kb.IsWikiEnabled() {
				s.cleanupWikiOnKnowledgeDelete(ctx, knowledge)
			}
		}
		return nil
	})

	if err = wg.Wait(); err != nil {
		return err
	}
	// 5. Delete the knowledge entry itself from the database
	return s.repo.DeleteKnowledgeList(ctx, tenantInfo.ID, ids)
}

func (s *knowledgeService) cleanupKnowledgeResources(ctx context.Context, knowledge *types.Knowledge) error {
	logger.GetLogger(ctx).Infof("Cleaning knowledge resources before manual update, knowledge ID: %s", knowledge.ID)

	var cleanupErr error

	if knowledge.ParseStatus == types.ManualKnowledgeStatusDraft && knowledge.StorageSize == 0 {
		// Draft without indexed data, skip cleanup.
		return nil
	}

	tenantInfo := ctx.Value(types.TenantInfoContextKey).(*types.Tenant)
	if knowledge.EmbeddingModelID != "" {
		retrieveEngine, err := retriever.NewCompositeRetrieveEngine(
			s.retrieveEngine,
			tenantInfo.GetEffectiveEngines(),
		)
		if err != nil {
			logger.GetLogger(ctx).WithField("error", err).Error("Failed to init retrieve engine during cleanup")
			cleanupErr = errors.Join(cleanupErr, err)
		} else {
			embeddingModel, modelErr := s.modelService.GetEmbeddingModel(ctx, knowledge.EmbeddingModelID)
			if modelErr != nil {
				logger.GetLogger(ctx).WithField("error", modelErr).Error("Failed to get embedding model during cleanup")
				cleanupErr = errors.Join(cleanupErr, modelErr)
			} else {
				if err := retrieveEngine.DeleteByKnowledgeIDList(ctx, []string{knowledge.ID}, embeddingModel.GetDimensions(), knowledge.Type); err != nil {
					logger.GetLogger(ctx).WithField("error", err).Error("Failed to delete manual knowledge index")
					cleanupErr = errors.Join(cleanupErr, err)
				}
			}
		}
	}

	// Collect image URLs before chunks are deleted
	kb, _ := s.kbService.GetKnowledgeBaseByID(ctx, knowledge.KnowledgeBaseID)
	fileSvc := s.resolveFileService(ctx, kb)
	chunkImageInfos, imgErr := s.chunkService.GetRepository().ListImageInfoByKnowledgeIDs(ctx, tenantInfo.ID, []string{knowledge.ID})
	if imgErr != nil {
		logger.GetLogger(ctx).WithField("error", imgErr).Error("Failed to collect image URLs for cleanup")
		cleanupErr = errors.Join(cleanupErr, imgErr)
	}
	var imageInfoStrs []string
	for _, ci := range chunkImageInfos {
		imageInfoStrs = append(imageInfoStrs, ci.ImageInfo)
	}
	imageURLs := collectImageURLs(ctx, imageInfoStrs)

	if err := s.chunkService.DeleteChunksByKnowledgeID(ctx, knowledge.ID); err != nil {
		logger.GetLogger(ctx).WithField("error", err).Error("Failed to delete manual knowledge chunks")
		cleanupErr = errors.Join(cleanupErr, err)
	}

	// Delete extracted images after chunks are deleted
	deleteExtractedImages(ctx, fileSvc, imageURLs)

	namespace := types.NameSpace{KnowledgeBase: knowledge.KnowledgeBaseID, Knowledge: knowledge.ID}
	if err := s.graphEngine.DelGraph(ctx, []types.NameSpace{namespace}); err != nil {
		logger.GetLogger(ctx).WithField("error", err).Error("Failed to delete manual knowledge graph data")
		cleanupErr = errors.Join(cleanupErr, err)
	}

	if knowledge.StorageSize > 0 {
		tenantInfo.StorageUsed -= knowledge.StorageSize
		if tenantInfo.StorageUsed < 0 {
			tenantInfo.StorageUsed = 0
		}
		if err := s.tenantRepo.AdjustStorageUsed(ctx, tenantInfo.ID, -knowledge.StorageSize); err != nil {
			logger.GetLogger(ctx).WithField("error", err).Error("Failed to adjust storage usage during manual cleanup")
			cleanupErr = errors.Join(cleanupErr, err)
		}
		knowledge.StorageSize = 0
	}

	return cleanupErr
}

// ProcessKnowledgeListDelete handles Asynq knowledge list delete tasks
func (s *knowledgeService) ProcessKnowledgeListDelete(ctx context.Context, t *asynq.Task) error {
	var payload types.KnowledgeListDeletePayload
	if err := json.Unmarshal(t.Payload(), &payload); err != nil {
		logger.Errorf(ctx, "Failed to unmarshal knowledge list delete payload: %v", err)
		return err
	}

	logger.Infof(ctx, "Processing knowledge list delete task for %d knowledge items", len(payload.KnowledgeIDs))

	// Get tenant info
	tenant, err := s.tenantRepo.GetTenantByID(ctx, payload.TenantID)
	if err != nil {
		logger.Errorf(ctx, "Failed to get tenant %d: %v", payload.TenantID, err)
		return err
	}

	// Set context values
	ctx = context.WithValue(ctx, types.TenantIDContextKey, payload.TenantID)
	ctx = context.WithValue(ctx, types.TenantInfoContextKey, tenant)

	// Delete knowledge list
	if err := s.DeleteKnowledgeList(ctx, payload.KnowledgeIDs); err != nil {
		logger.Errorf(ctx, "Failed to delete knowledge list: %v", err)
		return err
	}

	logger.Infof(ctx, "Successfully deleted %d knowledge items", len(payload.KnowledgeIDs))
	return nil
}
