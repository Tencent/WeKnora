package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
		return err
	} else {
		logger.Infof(ctx, "Marked knowledge %s as deleting (previous status: %s)", id, originalStatus)
	}

	// Best-effort: purge any queued downstream tasks for this knowledge
	// (multimodal / post-process / question / summary / graph extract).
	// Worker checkpoints already drop them on the floor, but dequeuing
	// here avoids waking workers just to no-op when the parse was still
	// in flight at delete time. No-op in Lite mode and on completed rows
	// (no queued descendants anyway).
	if originalStatus == types.ParseStatusPending ||
		originalStatus == types.ParseStatusProcessing {
		s.dequeueKnowledgeTasks(ctx, id)
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

	// Remove provenance before chunks or the physical file. If this fails,
	// leave the source intact so deletion can be retried safely.
	if err := s.cleanupWikiProvenanceOnKnowledgeDelete(ctx, knowledge); err != nil {
		return err
	}

	wg := errgroup.Group{}
	// Delete knowledge embeddings from vector store.
	// Skip entirely when the knowledge has no embedding model (e.g. Wiki-only KB):
	// nothing was ever written to the vector store, so there is nothing to delete,
	// and GetEmbeddingModel would fail with "model ID cannot be empty".
	if strings.TrimSpace(knowledge.EmbeddingModelID) != "" {
		wg.Go(func() error {
			// kb was already loaded above for resolveFileService — reuse its
			// VectorStoreID for engine routing.
			var boundStoreID *string
			if kb != nil {
				boundStoreID = kb.VectorStoreID
			}
			retrieveEngine, err := retriever.CreateRetrieveEngineForKB(
				ctx,
				s.retrieveEngine,
				s.ownership,
				tenantID,
				boundStoreID,
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

	// Delete the knowledge graph
	wg.Go(func() error {
		namespace := types.NameSpace{KnowledgeBase: knowledge.KnowledgeBaseID, Knowledge: knowledge.ID}
		if err := s.graphEngine.DelGraph(ctx, []types.NameSpace{namespace}); err != nil {
			logger.GetLogger(ctx).WithField("error", err).Errorf("DeleteKnowledge delete knowledge graph failed")
			return err
		}
		return nil
	})

	if err = wg.Wait(); err != nil {
		return err
	}
	if err := s.repo.DeleteKnowledgeTagRelations(ctx, id); err != nil {
		logger.Warnf(ctx, "Failed to delete tag relations for knowledge %s: %v", id, err)
	}
	// Delete the knowledge row FIRST, then drop its physical file. Physical
	// cleanup is deliberately deferred until the row is gone: if any of the
	// index/chunk/graph cleanups above failed we already returned early with the
	// row (and its file) intact, so the queued retry — or a user-triggered
	// reparse — can still read the original file. Deleting the file before the
	// row could leave a "file missing but row present" zombie that can neither be
	// reparsed nor cleanly re-deleted (issue #2192). Orphaning a file after the
	// row is gone is the tolerable failure mode instead.
	if err := s.repo.DeleteKnowledge(ctx, tenantID, id); err != nil {
		return err
	}

	// Best-effort physical cleanup. Errors here only leak storage; they must not
	// fail the delete now that the row is already gone.
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
	recordKBActivity(ctx, s.audit, tenantID, knowledge.KnowledgeBaseID, types.AuditActionKnowledgeDeleted,
		"knowledge", knowledge.ID, types.AuditOutcomeSuccess,
		map[string]any{"title": knowledge.Title, "type": knowledge.Type})
	return nil
}

// cleanupWikiProvenanceOnKnowledgeDelete removes a deleted document from the provenance
// ledger before chunks and the physical file are removed. The source ledger is
// authoritative; legacy wiki_pages.source_refs is no longer used to discover
// affected pages.
func (s *knowledgeService) cleanupWikiProvenanceOnKnowledgeDelete(ctx context.Context, knowledge *types.Knowledge) error {
	if knowledge == nil {
		return nil
	}
	kbID := knowledge.KnowledgeBaseID
	knowledgeID := knowledge.ID
	if kbID == "" || knowledgeID == "" {
		return nil
	}
	if s.wikiProvenance == nil {
		return errors.New("wiki provenance lifecycle service is not configured")
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

	tenantID := knowledge.TenantID
	if tenantID == 0 {
		tenantID, _ = types.TenantIDFromContext(ctx)
	}
	cleanup, err := s.wikiProvenance.DeleteKnowledgeSources(
		ctx, tenantID, kbID, knowledgeID, time.Now(),
	)
	if err != nil {
		return fmt.Errorf("delete wiki provenance for knowledge %s: %w", knowledgeID, err)
	}
	if len(cleanup.AffectedPages) == 0 {
		logger.Infof(ctx,
			"wiki cleanup: knowledge %s had no current affected pages (block_sources=%d page_sources=%d revisions=%d)",
			knowledgeID, cleanup.DeletedBlockSources, cleanup.DeletedPageSources, cleanup.DeletedKnowledgeRevisions,
		)
	}

	// Prefer the generated summary if the summary page already exists (it's
	// richer than the raw user-provided description). Leave docSummary
	// untouched otherwise so we still pass something meaningful downstream.
	for _, impact := range cleanup.AffectedPages {
		if impact.PageType == types.WikiPageTypeSummary && impact.Summary != "" {
			docSummary = impact.Summary
			break
		}
	}

	var deletedSlugs []string
	var retractSlugs []string
	var affectedFolderIDs []string
	for _, impact := range cleanup.AffectedPages {
		if impact.PageType == types.WikiPageTypeIndex {
			continue
		}
		if impact.FolderID != "" {
			affectedFolderIDs = append(affectedFolderIDs, impact.FolderID)
		}
		if impact.TotalSourceCount <= 1 {
			if err := s.wikiService.DeletePage(ctx, kbID, impact.Slug); err != nil {
				logger.Warnf(ctx, "wiki cleanup: failed to delete page %s: %v", impact.Slug, err)
			} else {
				deletedSlugs = append(deletedSlugs, impact.Slug)
			}
		} else {
			retractSlugs = append(retractSlugs, impact.Slug)
		}
	}

	if len(deletedSlugs) > 0 {
		logger.Infof(ctx, "wiki cleanup: deleted %d pages after knowledge %s deletion: %v",
			len(deletedSlugs), knowledgeID, deletedSlugs)
	}

	allAffectedSlugs := append(retractSlugs, deletedSlugs...)

	// Always enqueue a retract, including when the ledger currently has no
	// affected page. That closes the ingest/delete or ingest/move race: a page
	// published after the atomic cleanup is re-queried and reconciled by the
	// retract worker. A deleting knowledge also cannot be republished because
	// the atomic publisher rejects parse_status=deleting.
	lang, _ := types.LanguageFromContext(ctx)
	EnqueueWikiRetract(ctx, s.task, s.taskPendingRepo, WikiRetractPayload{
		TenantID:        tenantID,
		KnowledgeBaseID: kbID,
		KnowledgeID:     knowledgeID,
		DocTitle:        docTitle,
		DocSummary:      docSummary,
		Language:        lang,
		PageSlugs:       allAffectedSlugs,
		FolderIDs:       uniqueWikiFolderIDs(affectedFolderIDs),
	})
	logger.Infof(ctx, "wiki cleanup: enqueued retract task for knowledge %s (%d known slugs: %v)",
		knowledgeID, len(allAffectedSlugs), allAffectedSlugs)
	logger.Infof(ctx,
		"wiki cleanup: removed provenance for knowledge %s (block_sources=%d page_sources=%d revisions=%d)",
		knowledgeID, cleanup.DeletedBlockSources, cleanup.DeletedPageSources, cleanup.DeletedKnowledgeRevisions,
	)
	return nil
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
// cleanupWikiProvenanceOnKnowledgeDelete. It aligns reparse with the same "pending
// queue hygiene" the delete path already enforces, without taking any
// destructive action against existing pages.
//
// Why no retract / tombstone here: reparse is not a "K is gone" event, it's
// a "K's contribution is about to be swapped for a new version" event. The
// actual swap happens asynchronously inside mapOneDocument (see its
// oldPageSlugs handling) — that's where we have both the old page set and
// the freshly extracted candidate slugs, which is exactly the information
// the WikiPageModifyUserPrompt needs to do a correct replace-not-append.
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

type knowledgeVectorDeleteGroup struct {
	VectorStoreID    string
	EmbeddingModelID string
	Type             string
	KnowledgeIDs     []string
}

func buildKnowledgeVectorDeleteGroups(
	knowledges []*types.Knowledge,
	knowledgeBases map[string]*types.KnowledgeBase,
) []knowledgeVectorDeleteGroup {
	type groupKey struct {
		VectorStoreID    string
		EmbeddingModelID string
		Type             string
	}

	grouped := make(map[groupKey][]string)
	for _, knowledge := range knowledges {
		if knowledge == nil {
			continue
		}
		var vectorStoreID string
		if kb := knowledgeBases[knowledge.KnowledgeBaseID]; kb != nil && kb.VectorStoreID != nil {
			vectorStoreID = strings.TrimSpace(*kb.VectorStoreID)
		}
		key := groupKey{
			VectorStoreID:    vectorStoreID,
			EmbeddingModelID: knowledge.EmbeddingModelID,
			Type:             knowledge.Type,
		}
		grouped[key] = append(grouped[key], knowledge.ID)
	}

	groups := make([]knowledgeVectorDeleteGroup, 0, len(grouped))
	for key, knowledgeIDs := range grouped {
		groups = append(groups, knowledgeVectorDeleteGroup{
			VectorStoreID:    key.VectorStoreID,
			EmbeddingModelID: key.EmbeddingModelID,
			Type:             key.Type,
			KnowledgeIDs:     knowledgeIDs,
		})
	}
	return groups
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

	// Mark all as deleting first to prevent async task conflicts.
	// Remember which entries still had queued / in-flight downstream tasks
	// so we can dequeue them in one pass after marking.
	var inFlightIDs []string
	for _, knowledge := range knowledgeList {
		prev := knowledge.ParseStatus
		knowledge.ParseStatus = types.ParseStatusDeleting
		knowledge.UpdatedAt = time.Now()
		if err := s.repo.UpdateKnowledge(ctx, knowledge); err != nil {
			logger.GetLogger(ctx).WithField("error", err).WithField("knowledge_id", knowledge.ID).
				Errorf("DeleteKnowledgeList failed to mark as deleting")
			return err
		}
		if prev == types.ParseStatusPending || prev == types.ParseStatusProcessing {
			inFlightIDs = append(inFlightIDs, knowledge.ID)
		}
	}
	logger.Infof(ctx, "Marked %d knowledge entries as deleting", len(knowledgeList))

	// Best-effort dequeue of downstream tasks for in-flight entries.
	// See DeleteKnowledge for the rationale; loop is per-knowledge because
	// the inspector only filters by knowledge_id, not by ID set.
	for _, kid := range inFlightIDs {
		s.dequeueKnowledgeTasks(ctx, kid)
	}

	// Pre-resolve KB metadata and file services so goroutines don't need DB access.
	knowledgeBases := make(map[string]*types.KnowledgeBase)
	kbFileServices := make(map[string]interfaces.FileService)
	for _, knowledge := range knowledgeList {
		if _, ok := kbFileServices[knowledge.KnowledgeBaseID]; !ok {
			kb, _ := s.kbService.GetKnowledgeBaseByID(ctx, knowledge.KnowledgeBaseID)
			knowledgeBases[knowledge.KnowledgeBaseID] = kb
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

	// Finish every provenance transaction before irreversible chunk/file
	// cleanup. A failure leaves source files available for a safe retry.
	for _, knowledge := range knowledgeList {
		if err := s.cleanupWikiProvenanceOnKnowledgeDelete(ctx, knowledge); err != nil {
			return err
		}
	}

	wg := errgroup.Group{}
	// 2. Delete knowledge embeddings from vector store
	wg.Go(func() error {
		tenantID := types.MustTenantIDFromContext(ctx)
		for _, group := range buildKnowledgeVectorDeleteGroups(knowledgeList, knowledgeBases) {
			// Wiki-only knowledge never had embeddings written to the vector store,
			// and its EmbeddingModelID is intentionally empty. Skip the whole group
			// to avoid the spurious "model ID cannot be empty" failure.
			if strings.TrimSpace(group.EmbeddingModelID) == "" {
				logger.Infof(ctx, "Skipping vector store cleanup for %d knowledge entries without embedding model", len(group.KnowledgeIDs))
				continue
			}

			var vectorStoreID *string
			if group.VectorStoreID != "" {
				storeID := group.VectorStoreID
				vectorStoreID = &storeID
			}
			retrieveEngine, err := retriever.CreateRetrieveEngineForKB(
				ctx, s.retrieveEngine, s.ownership, tenantID, vectorStoreID)
			if err != nil {
				logger.GetLogger(ctx).WithField("error", err).Errorf("DeleteKnowledge delete knowledge embedding failed")
				return err
			}
			embeddingModel, err := s.modelService.GetEmbeddingModel(ctx, group.EmbeddingModelID)
			if err != nil {
				logger.GetLogger(ctx).WithField("error", err).Errorf("DeleteKnowledge get embedding model failed")
				return err
			}
			if err := retrieveEngine.DeleteByKnowledgeIDList(ctx, group.KnowledgeIDs, embeddingModel.GetDimensions(), group.Type); err != nil {
				logger.GetLogger(ctx).
					WithField("error", err).
					Errorf("DeleteKnowledge delete knowledge embedding failed")
				return err
			}
		}
		return nil
	})

	// 4. Delete all chunks associated with this knowledge
	wg.Go(func() error {
		if err := s.chunkService.DeleteByKnowledgeList(ctx, ids); err != nil {
			logger.GetLogger(ctx).WithField("error", err).Errorf("DeleteKnowledge delete chunks failed")
			return err
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

	if err = wg.Wait(); err != nil {
		return err
	}
	for _, knowledgeID := range ids {
		if err := s.repo.DeleteKnowledgeTagRelations(ctx, knowledgeID); err != nil {
			logger.Warnf(ctx, "Failed to delete tag relations for knowledge %s: %v", knowledgeID, err)
		}
	}
	// 6. Delete the knowledge rows FIRST, then drop their physical files. See
	// DeleteKnowledge for the rationale: deferring file removal until the rows are
	// gone avoids "file missing but row present" zombies that break reparse /
	// re-delete when an earlier cleanup step failed (issue #2192). A failure below
	// only orphans storage.
	if err := s.repo.DeleteKnowledgeList(ctx, tenantInfo.ID, ids); err != nil {
		return err
	}

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
	byKB := make(map[string][]*types.Knowledge)
	for i := range knowledgeList {
		knowledge := knowledgeList[i]
		byKB[knowledge.KnowledgeBaseID] = append(byKB[knowledge.KnowledgeBaseID], knowledge)
	}
	for kbID, knowledges := range byKB {
		knowledgeIDs := make([]string, 0, len(knowledges))
		titles := make([]string, 0, len(knowledges))
		for _, knowledge := range knowledges {
			knowledgeIDs = append(knowledgeIDs, knowledge.ID)
			titles = append(titles, knowledge.Title)
		}
		details := map[string]any{"count": len(knowledgeIDs)}
		if len(knowledgeIDs) <= 20 {
			details["knowledge_ids"] = knowledgeIDs
		}
		kbActivityAppendSampleTitles(details, titles...)
		recordKBActivity(ctx, s.audit, tenantInfo.ID, kbID, types.AuditActionKnowledgeBatchDeleted,
			"knowledge", "", types.AuditOutcomeSuccess, details)
	}
	return nil
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
		// Load KB to discover its VectorStoreID binding. Falls back to tenant
		// effective engines if the KB has no binding or the load fails.
		//
		// Silent fallback risk: if a bound KB fails to load here due to a
		// transient DB error, the cleanup will delete from env engines and
		// leave orphan vectors in the bound store. Warn so operators can spot it.
		var boundStoreID *string
		if kb, loadErr := s.kbService.GetKnowledgeBaseByID(ctx, knowledge.KnowledgeBaseID); loadErr == nil && kb != nil {
			boundStoreID = kb.VectorStoreID
		} else if loadErr != nil {
			logger.GetLogger(ctx).WithField("error", loadErr).WithField("knowledge_base_id", knowledge.KnowledgeBaseID).
				Warnf("cleanupKnowledgeResources: failed to load KB for vector store resolution; falling back to tenant effective engines")
		}
		retrieveEngine, err := retriever.CreateRetrieveEngineForKB(
			ctx, s.retrieveEngine, s.ownership, tenantInfo.ID, boundStoreID)
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
	ctx = payload.Initiator.Apply(ctx)
	taskID, _ := asynq.GetTaskID(ctx)
	ctx = withKBActivityTask(ctx, taskID, kbActivityTrigger(ctx))

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
