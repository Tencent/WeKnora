package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/agent"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/tracing/langfuse"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"golang.org/x/sync/errgroup"
)

// scheduleFollowUp enqueues another asynq trigger task if there are
// still pending ops in task_pending_ops for this KB. Returns true when
// a follow-up was scheduled.
//
// We use a short ProcessIn (5s) so the active-batch lock has time to
// release before the next worker tries to acquire it; otherwise we'd
// just bounce on ErrWikiIngestConcurrent and burn an asynq retry slot.
func (s *wikiIngestService) scheduleFollowUp(ctx context.Context, payload WikiIngestPayload) bool {
	if s.pendingRepo == nil {
		return false
	}
	count, err := s.pendingRepo.PendingCount(ctx, wikiTaskType, wikiTaskScope, payload.KnowledgeBaseID)
	if err != nil || count == 0 {
		return false
	}

	logger.Infof(ctx, "wiki ingest: %d more documents pending for KB %s, scheduling follow-up", count, payload.KnowledgeBaseID)

	langfuse.InjectTracing(ctx, &payload)
	payloadBytes, _ := json.Marshal(payload)
	t := asynq.NewTask(types.TypeWikiIngest, payloadBytes,
		asynq.Queue("low"),
		asynq.MaxRetry(wikiIngestMaxRetry),
		asynq.Timeout(60*time.Minute),
		asynq.ProcessIn(5*time.Second), // short delay — active flag will be released by then
	)
	if _, err := s.task.Enqueue(t); err != nil {
		logger.Warnf(ctx, "wiki ingest: follow-up enqueue failed: %v", err)
		return false
	}
	return true
}

func (s *wikiIngestService) ProcessWikiIngest(ctx context.Context, t *asynq.Task) error {
	taskStartedAt := time.Now()
	retryCount, _ := asynq.GetRetryCount(ctx)
	maxRetry, _ := asynq.GetMaxRetry(ctx)

	var payload WikiIngestPayload
	exitStatus := "success"
	mode := "redis"
	lockAcquired := false
	pendingOpsCount := 0
	ingestOps := 0
	retractOps := 0
	ingestSucceeded := 0
	ingestFailed := 0
	retractHandled := 0
	indexRebuildAttempted := false
	indexRebuildSucceeded := false
	followUpScheduled := false
	totalPagesAffected := 0
	docPreview := make([]string, 0, 6)
	// Tunables resolved from KB.WikiConfig once we've loaded the KB.
	// Captured up here so the deferred stats log can observe them
	// regardless of which exit path we took.
	loggedBatchSize := 0
	loggedMapPar := 0
	loggedReducePar := 0

	defer func() {
		logger.Infof(
			ctx,
			"wiki ingest stats: kb=%s tenant=%d retry=%d/%d status=%s elapsed=%s mode=%s lock_acquired=%v pending_ops=%d ops(ingest=%d,retract=%d) ingest(success=%d,failed=%d) retract_handled=%d pages(total=%d) index(rebuild_attempted=%v,rebuild_succeeded=%v) followup=%v tunables(batch=%d,map_par=%d,reduce_par=%d) preview=%s",
			payload.KnowledgeBaseID,
			payload.TenantID,
			retryCount,
			maxRetry,
			exitStatus,
			time.Since(taskStartedAt).Round(time.Millisecond),
			mode,
			lockAcquired,
			pendingOpsCount,
			ingestOps,
			retractOps,
			ingestSucceeded,
			ingestFailed,
			retractHandled,
			totalPagesAffected,
			indexRebuildAttempted,
			indexRebuildSucceeded,
			followUpScheduled,
			loggedBatchSize,
			loggedMapPar,
			loggedReducePar,
			previewStringSlice(docPreview, 6),
		)
	}()

	if err := json.Unmarshal(t.Payload(), &payload); err != nil {
		exitStatus = "invalid_payload"
		return fmt.Errorf("wiki ingest: unmarshal payload: %w", err)
	}

	// Inject context
	ctx = context.WithValue(ctx, types.TenantIDContextKey, payload.TenantID)
	if payload.Language != "" {
		ctx = context.WithValue(ctx, types.LanguageContextKey, payload.Language)
	}

	// Try to acquire the "active batch" flag (non-blocking).
	//
	// TTL is intentionally short (wikiActiveLockTTL ≈ 60s) so that if the
	// owning process dies without releasing the lock (crash, kill -9,
	// container restart), the orphaned key expires within ~1 minute and new
	// tasks aren't starved. A renew goroutine keeps the lock alive while
	// the handler is genuinely running.
	if s.redisClient != nil {
		activeKey := wikiActiveKeyPrefix + payload.KnowledgeBaseID
		acquired, err := s.redisClient.SetNX(ctx, activeKey, "1", wikiActiveLockTTL).Result()
		if err != nil {
			logger.Warnf(ctx, "wiki ingest: redis SetNX failed: %v", err)
		} else if !acquired {
			exitStatus = "active_lock_conflict"
			// If task_pending_ops is already empty for this KB, the active
			// batch will drain whatever was queued. Returning nil avoids
			// burning through the retry budget on tasks that would be
			// no-ops when they eventually acquire the lock. If rows still
			// remain, retry so we don't miss them in case the active
			// batch drained its peek before our op landed.
			n, nErr := s.pendingRepo.PendingCount(ctx, wikiTaskType, wikiTaskScope, payload.KnowledgeBaseID)
			if nErr != nil {
				logger.Warnf(ctx, "wiki ingest: failed to read pending count during lock conflict for KB %s: %v", payload.KnowledgeBaseID, nErr)
				logger.Infof(ctx, "wiki ingest: another batch active for KB %s, deferring to asynq retry", payload.KnowledgeBaseID)
				return ErrWikiIngestConcurrent
			}
			if n == 0 {
				exitStatus = "active_lock_conflict_empty"
				logger.Infof(ctx, "wiki ingest: concurrent batch active for KB %s, pending queue empty — skipping", payload.KnowledgeBaseID)
				return nil
			}
			logger.Infof(ctx, "wiki ingest: another batch active for KB %s, deferring to asynq retry", payload.KnowledgeBaseID)
			return ErrWikiIngestConcurrent
		}
		lockAcquired = acquired

		lockCtx, cancelLock := context.WithCancel(context.Background())
		defer func() {
			cancelLock()
			s.redisClient.Del(context.Background(), activeKey)
		}()

		go func() {
			ticker := time.NewTicker(wikiActiveLockRenew)
			defer ticker.Stop()
			for {
				select {
				case <-lockCtx.Done():
					return
				case <-ticker.C:
					s.redisClient.Expire(context.Background(), activeKey, wikiActiveLockTTL)
				}
			}
		}()
	} else {
		mode = "lite"
		// In-process mutual exclusion: mirrors the Redis SetNX lock above.
		if _, loaded := s.liteLocks.LoadOrStore(payload.KnowledgeBaseID, struct{}{}); loaded {
			exitStatus = "active_lock_conflict"
			logger.Infof(ctx, "wiki ingest: another batch active for KB %s (lite lock), deferring to asynq retry", payload.KnowledgeBaseID)
			return ErrWikiIngestConcurrent
		}
		lockAcquired = true
		defer s.liteLocks.Delete(payload.KnowledgeBaseID)
	}

	kb, err := s.kbService.GetKnowledgeBaseByIDOnly(ctx, payload.KnowledgeBaseID)
	if err != nil {
		exitStatus = "get_kb_failed"
		return fmt.Errorf("wiki ingest: get KB: %w", err)
	}
	if !kb.IsWikiEnabled() {
		exitStatus = "kb_not_wiki_enabled"
		return fmt.Errorf("wiki ingest: KB %s is not wiki type", kb.ID)
	}

	var synthesisModelID string
	if kb.WikiConfig != nil {
		synthesisModelID = kb.WikiConfig.SynthesisModelID
	}
	if synthesisModelID == "" {
		synthesisModelID = kb.SummaryModelID
	}
	if synthesisModelID == "" {
		exitStatus = "missing_synthesis_model"
		return fmt.Errorf("wiki ingest: no synthesis model configured for KB %s", kb.ID)
	}
	chatModel, err := s.modelService.GetChatModel(ctx, synthesisModelID)
	if err != nil {
		exitStatus = "get_chat_model_failed"
		return fmt.Errorf("wiki ingest: get chat model: %w", err)
	}

	// Resolve per-KB tunables once. WikiConfig.IngestBatchSize /
	// IngestMapParallel / IngestReduceParallel let operators on
	// 4w-document KBs raise the throughput knob (more docs per batch +
	// more concurrent LLM calls) without a code deploy. Zero falls back
	// to the historical defaults so existing KBs see no behaviour
	// change until they opt in.
	batchSize := kb.WikiConfig.IngestBatchSizeOrDefault(wikiMaxDocsPerBatch)
	mapParallel := kb.WikiConfig.IngestMapParallelOrDefault(10)
	reduceParallel := kb.WikiConfig.IngestReduceParallelOrDefault(10)
	loggedBatchSize = batchSize
	loggedMapPar = mapParallel
	loggedReducePar = reduceParallel
	loggedBatchSize = batchSize
	loggedMapPar = mapParallel
	loggedReducePar = reduceParallel

	lang := types.LanguageNameFromContext(ctx)

	pendingOps, peekedIDs := s.peekPendingList(ctx, payload.KnowledgeBaseID, batchSize)
	pendingOpsCount = len(pendingOps)
	if len(pendingOps) == 0 {
		exitStatus = "no_pending_ops"
		logger.Infof(ctx, "wiki ingest: no pending operations for KB %s", payload.KnowledgeBaseID)
		return nil
	}

	logger.Infof(ctx, "wiki ingest: batch processing %d ops for KB %s", len(pendingOps), payload.KnowledgeBaseID)

	// Resolve extraction granularity once per batch. Historical rows with
	// empty/unknown values fall back to Standard via Normalize(). Failures
	// to load the KB (unlikely since we're already acting on it) also
	// degrade gracefully to Standard.
	granularity := types.WikiExtractionStandard
	if kb, kbErr := s.kbService.GetKnowledgeBaseByID(ctx, payload.KnowledgeBaseID); kbErr == nil && kb != nil && kb.WikiConfig != nil {
		granularity = kb.WikiConfig.ExtractionGranularity.Normalize()
	}

	// Build the per-batch lazy fetchers. These replace the legacy
	// pre-batch ListAllPages dump: instead of pulling ~100MB of rows
	// up front (and walking them several more times during the batch),
	// callers pay only for the slugs / knowledge ids they actually
	// reach for. Cache hits keep repeat lookups within the batch free.
	var (
		fetchMu          sync.Mutex
		slugTitleCache   = make(map[string]string) // slug -> title; "" = known-missing
		summaryKIDCache  = make(map[string]string) // kid -> content; "" = known-missing
	)

	resolveSlugs := func(ctx context.Context, slugs []string) map[string]string {
		// Filter to the slugs we don't already have cached.
		fetchMu.Lock()
		need := slugs[:0:0]
		for _, slug := range slugs {
			if _, ok := slugTitleCache[slug]; ok {
				continue
			}
			need = append(need, slug)
		}
		fetchMu.Unlock()

		if len(need) > 0 {
			pages, err := s.wikiService.ListBySlugs(ctx, payload.KnowledgeBaseID, need)
			if err != nil {
				logger.Warnf(ctx, "wiki ingest: ListBySlugs(%d slugs) failed: %v", len(need), err)
			}
			fetchMu.Lock()
			for _, slug := range need {
				if p, ok := pages[slug]; ok && p != nil {
					if p.Status == types.WikiPageStatusArchived ||
						p.PageType == types.WikiPageTypeIndex ||
						p.PageType == types.WikiPageTypeLog {
						// Treat archived / system pages as missing from the
						// title-resolution map: cleanDeadLinks shouldn't link
						// to them, and the log-feed slug-title fallback
						// should degrade to slug-only display.
						slugTitleCache[slug] = ""
						continue
					}
					slugTitleCache[slug] = p.Title
				} else {
					slugTitleCache[slug] = ""
				}
			}
			fetchMu.Unlock()
		}

		out := make(map[string]string, len(slugs))
		fetchMu.Lock()
		for _, slug := range slugs {
			if title := slugTitleCache[slug]; title != "" {
				out[slug] = title
			}
		}
		fetchMu.Unlock()
		return out
	}

	resolveSummaries := func(ctx context.Context, kids []string) map[string]string {
		fetchMu.Lock()
		need := kids[:0:0]
		for _, kid := range kids {
			if _, ok := summaryKIDCache[kid]; ok {
				continue
			}
			need = append(need, kid)
		}
		fetchMu.Unlock()

		if len(need) > 0 {
			contents, err := s.wikiService.ListSummariesByKnowledgeIDs(ctx, payload.KnowledgeBaseID, need)
			if err != nil {
				logger.Warnf(ctx, "wiki ingest: ListSummariesByKnowledgeIDs(%d kids) failed: %v", len(need), err)
			}
			fetchMu.Lock()
			for _, kid := range need {
				if c, ok := contents[kid]; ok && c != "" {
					summaryKIDCache[kid] = c
				} else {
					summaryKIDCache[kid] = ""
				}
			}
			fetchMu.Unlock()
		}

		out := make(map[string]string, len(kids))
		fetchMu.Lock()
		for _, kid := range kids {
			if content := summaryKIDCache[kid]; content != "" {
				out[kid] = content
			}
		}
		fetchMu.Unlock()
		return out
	}

	batchCtx := &WikiBatchContext{
		SlugTitle: func(ctx context.Context, slug string) string {
			m := resolveSlugs(ctx, []string{slug})
			return m[slug]
		},
		SlugTitleMany:               resolveSlugs,
		SummaryContentByKnowledgeID: func(ctx context.Context, kid string) string {
			m := resolveSummaries(ctx, []string{kid})
			return m[kid]
		},
		ExtractionGranularity: granularity,
	}

	// 1. MAP PHASE (Parallel extraction and generation of updates)
	var mapMu sync.Mutex
	var failedOps []WikiPendingOp
	slugUpdates := make(map[string][]SlugUpdate)
	var docResults []*docIngestResult
	var retractChangeDesc strings.Builder

	eg, mapCtx := errgroup.WithContext(ctx)
	eg.SetLimit(mapParallel) // Map phase limit (configurable via WikiConfig)

	for _, op := range pendingOps {
		op := op
		eg.Go(func() error {
			if op.Op == WikiOpRetract {
				// Resolve the authoritative page set at run-time. The caller
				// (knowledgeService.cleanupWikiOnKnowledgeDelete) captures
				// PageSlugs from a DB snapshot taken *before* this task fires,
				// but there is a window where:
				//   - cleanup ran before ingest → snapshot is empty, but a
				//     concurrent ingest may have already created pages by now
				//   - a previous ingest batch created new pages after cleanup
				//     captured its snapshot
				// Re-querying ListPagesBySourceRef here unions the caller's
				// slugs with whatever currently references the knowledge, so
				// no page is left un-retracted. It also lets us support
				// callers that deliberately enqueue retract with empty
				// PageSlugs as "figure it out yourself" — see
				// cleanupWikiOnKnowledgeDelete's comment (3).
				slugSet := make(map[string]struct{}, len(op.PageSlugs))
				for _, slug := range op.PageSlugs {
					if slug == "" {
						continue
					}
					slugSet[slug] = struct{}{}
				}
				if op.KnowledgeID != "" {
					livePages, err := s.wikiService.ListPagesBySourceRef(mapCtx, payload.KnowledgeBaseID, op.KnowledgeID)
					if err != nil {
						logger.Warnf(mapCtx, "wiki ingest: retract lookup failed for %s: %v", op.KnowledgeID, err)
					} else {
						for _, p := range livePages {
							if p == nil || p.Slug == "" {
								continue
							}
							// Index/log pages never carry real source_refs;
							// if they somehow surface here, skip — the
							// reduce stage would be a no-op anyway.
							if p.PageType == types.WikiPageTypeIndex || p.PageType == types.WikiPageTypeLog {
								continue
							}
							slugSet[p.Slug] = struct{}{}
						}
					}
				}

				mapMu.Lock()
				retractOps++
				retractHandled++
				docPreview = append(docPreview, fmt.Sprintf("retract[%s]: %s (%d slugs)", previewText(op.KnowledgeID, 24), previewText(op.DocTitle, 48), len(slugSet)))
				fmt.Fprintf(&retractChangeDesc, "<document_removed>\n<title>%s</title>\n<summary>%s</summary>\n</document_removed>\n\n", op.DocTitle, op.DocSummary)

				for slug := range slugSet {
					slugUpdates[slug] = append(slugUpdates[slug], SlugUpdate{
						Slug:              slug,
						Type:              "retract",
						RetractDocContent: op.DocSummary,
						DocTitle:          op.DocTitle,
						KnowledgeID:       op.KnowledgeID,
						Language:          op.Language,
					})
				}
				mapMu.Unlock()
				return nil
			}

			// Ingest
			mapMu.Lock()
			ingestOps++
			mapMu.Unlock()

			logger.Infof(mapCtx, "wiki ingest: processing document '%s' (%s)", op.DocTitle, op.KnowledgeID)
			result, updates, err := s.mapOneDocument(mapCtx, chatModel, payload, op, batchCtx)
			if err != nil {
				mapMu.Lock()
				ingestFailed++
				failedOps = append(failedOps, op)
				mapMu.Unlock()
				logger.Warnf(mapCtx, "wiki ingest: failed to map knowledge %s: %v", op.KnowledgeID, err)
				return nil // Don't fail the whole batch
			}

			if result != nil {
				mapMu.Lock()
				ingestSucceeded++
				docResults = append(docResults, result)
				docPreview = append(docPreview, fmt.Sprintf("ingest[%s]: title=%s summary=%s", previewText(result.KnowledgeID, 24), previewText(result.DocTitle, 40), previewText(result.Summary, 64)))
				for _, u := range updates {
					slugUpdates[u.Slug] = append(slugUpdates[u.Slug], u)
				}
				mapMu.Unlock()

				// No fail-count reset needed: a successful op is added
				// to peekedIDs and gets DELETEd from task_pending_ops at
				// trim time, so there is no stale fail_count column to
				// scrub. Compare with the legacy Redis path, which kept
				// a separate wiki:failcount:<...> key alive for 24h
				// regardless of whether the original op had drained.
			}
			return nil
		})
	}
	_ = eg.Wait()

	// 2. REDUCE PHASE (Parallel upserting grouped by Slug)
	egReduce, reduceCtx := errgroup.WithContext(ctx)
	egReduce.SetLimit(reduceParallel) // Reduce phase limit (LLM + DB concurrent connections, configurable)

	var reduceMu sync.Mutex
	var allPagesAffected []string
	var ingestPagesAffected []string
	var retractPagesAffected []string
	// failedAdditionSlugs collects entity/concept slugs whose page
	// generation LLM call failed (so the page was never written). The
	// post-reduce cleanup step uses this set to (a) strip dead [[slug]]
	// references from the same batch's summary pages, and (b) prune the
	// slugs out of the wiki log feed so users don't see clickable entries
	// pointing at missing pages.
	failedAdditionSlugs := make(map[string]struct{})

	for slug, updates := range slugUpdates {
		slug := slug
		updates := updates
		egReduce.Go(func() error {
			changed, affectedType, additionFailed, err := s.reduceSlugUpdates(reduceCtx, chatModel, payload.KnowledgeBaseID, slug, updates, payload.TenantID, batchCtx)
			if err != nil {
				logger.Warnf(reduceCtx, "wiki ingest: reduce failed for slug %s: %v", slug, err)
			}
			if changed {
				reduceMu.Lock()
				allPagesAffected = append(allPagesAffected, slug)
				if affectedType == "ingest" {
					ingestPagesAffected = append(ingestPagesAffected, slug)
				} else if affectedType == "retract" {
					retractPagesAffected = append(retractPagesAffected, slug)
				}
				reduceMu.Unlock()
			}
			if additionFailed {
				reduceMu.Lock()
				failedAdditionSlugs[slug] = struct{}{}
				reduceMu.Unlock()
			}
			return nil
		})
	}
	_ = egReduce.Wait()

	// Sanitize the doc summary pages produced by this batch BEFORE we
	// build log entries / rebuild the index. The summary LLM (run during
	// map) was free to inject [[entity/foo|name]] links to every slug it
	// saw extracted, but reduce may have failed to materialize some of
	// those slugs into actual pages. Rewrite those dead links to plain
	// text so the summary doesn't contain unresolvable references.
	if len(failedAdditionSlugs) > 0 && len(docResults) > 0 {
		s.sanitizeDeadSummaryLinks(ctx, payload.KnowledgeBaseID, docResults, failedAdditionSlugs, batchCtx)
	}

	totalPagesAffected = len(allPagesAffected)

	// Collect log entries for this batch and flush them in a single INSERT.
	// Historically each op triggered its own `GetLog + UpdatePage` round
	// trip, which rewrote the entire log page TEXT column and caused O(n^2)
	// write amplification as the log grew. AppendBatch writes one row per
	// event into wiki_log_entries instead.
	//
	// slugsToRefs resolves each retract slug against the batch-start
	// snapshot (batchCtx.SlugTitleMap) so the log feed carries titles for
	// pages that existed when the batch began. Pages created or renamed
	// during this batch fall through the map lookup and log as slug-only
	// refs, which the frontend renders as the slug itself — a sensible
	// fallback given retracts only touch pre-existing pages.
	slugsToRefs := func(slugs []string) []types.WikiLogPageRef {
		if len(slugs) == 0 {
			return nil
		}
		titles := batchCtx.SlugTitleMany(ctx, slugs)
		out := make([]types.WikiLogPageRef, 0, len(slugs))
		for _, slug := range slugs {
			out = append(out, types.WikiLogPageRef{Slug: slug, Title: titles[slug]})
		}
		return out
	}
	logEntries := make([]*types.WikiLogEntry, 0, len(pendingOps)+len(docResults))
	for _, op := range pendingOps {
		if op.Op == WikiOpRetract {
			logEntries = append(logEntries, s.buildLogEntry(payload.TenantID, payload.KnowledgeBaseID, "retract", op.KnowledgeID, op.DocTitle, op.DocSummary, slugsToRefs(op.PageSlugs)))
		}
	}
	for _, r := range docResults {
		// Drop any slugs whose page generation failed in reduce so the
		// log feed never offers a clickable entry that 404s. The summary
		// page itself (slug = summary/<knowledgeID>) is always created
		// unconditionally upstream, so it survives the filter.
		pages := r.Pages
		if len(failedAdditionSlugs) > 0 {
			pages = pages[:0:0]
			for _, ref := range r.Pages {
				if _, bad := failedAdditionSlugs[ref.Slug]; bad {
					continue
				}
				pages = append(pages, ref)
			}
		}
		logEntries = append(logEntries, s.buildLogEntry(payload.TenantID, payload.KnowledgeBaseID, "ingest", r.KnowledgeID, r.DocTitle, r.Summary, pages))
	}
	if len(logEntries) > 0 && s.logEntrySvc != nil {
		if err := s.logEntrySvc.AppendBatch(ctx, logEntries); err != nil {
			logger.Warnf(ctx, "wiki ingest: failed to append %d log entries: %v", len(logEntries), err)
		}
	}

	// Build change description for the Index Intro LLM prompt
	var changeDesc strings.Builder
	if len(docResults) > 0 {
		for _, r := range docResults {
			fmt.Fprintf(&changeDesc, "<document_added>\n<title>%s</title>\n<summary>%s</summary>\n</document_added>\n\n", r.DocTitle, r.Summary)
		}
	}
	if retractChangeDesc.Len() > 0 {
		changeDesc.WriteString(retractChangeDesc.String())
	}

	// Rebuild index page
	if changeDesc.Len() > 0 {
		indexRebuildAttempted = true
		logger.Infof(ctx, "wiki ingest: rebuilding index page")
		if err := s.rebuildIndexPage(ctx, chatModel, payload, changeDesc.String(), lang); err != nil {
			logger.Warnf(ctx, "wiki ingest: rebuild index failed: %v", err)
			docPreview = append(docPreview, fmt.Sprintf("index_change=%s", previewText(changeDesc.String(), 160)))
		} else {
			indexRebuildSucceeded = true
			docPreview = append(docPreview, fmt.Sprintf("index_change=%s", previewText(changeDesc.String(), 160)))
		}
	}

	// Clean dead [[slug]] references whenever ANY page was touched this
	// batch (not just retracts). Reduce-phase failures can leave stale
	// references in pages we just rewrote (e.g. summary pages cite
	// failed entity slugs); sanitizeDeadSummaryLinks above handles the
	// well-known summary case, and this pass is the safety net for the
	// long tail (cross-doc citations, prior batches' lingering refs).
	// Dead-link cleanup: scoped to this batch's affected pages so the
	// pass scales with batch size, not with KB size. The lint
	// AutoFix path takes care of long-tail cleanup across the whole
	// KB out-of-band.
	if len(allPagesAffected) > 0 {
		logger.Infof(ctx, "wiki ingest: cleaning dead links")
		s.cleanDeadLinks(ctx, payload.KnowledgeBaseID, allPagesAffected, batchCtx)
	}

	if len(allPagesAffected) > 0 {
		// Build the freshRefs set: every (slug, title) pair this batch
		// successfully wrote, minus any that landed in failedAdditionSlugs.
		// These are the "newly-mentionable" pages — links to them will
		// not have appeared in older content yet, so injectCrossLinks
		// targets exactly the affected pages with this fresh ref set.
		freshRefs := make([]linkRef, 0, len(docResults)*4)
		for _, dr := range docResults {
			if dr == nil {
				continue
			}
			for _, p := range dr.Pages {
				if p.Slug == "" || p.Title == "" {
					continue
				}
				if _, bad := failedAdditionSlugs[p.Slug]; bad {
					continue
				}
				freshRefs = append(freshRefs, linkRef{slug: p.Slug, matchText: p.Title})
			}
		}

		logger.Infof(ctx, "wiki ingest: injecting cross links")
		s.injectCrossLinks(ctx, payload.KnowledgeBaseID, allPagesAffected, freshRefs, batchCtx)

		logger.Infof(ctx, "wiki ingest: publishing draft pages")
		s.publishDraftPages(ctx, payload.KnowledgeBaseID, allPagesAffected)
	}

	// Build the trim set: rows that should be removed from
	// task_pending_ops. We start from the full peekedIDs (every row we
	// pulled, even ones de-duplicated by knowledge_id) and subtract
	// any failed op's dbID — those need to stay in place so the
	// requeueFailedOps path can decide between retry and dead-letter.
	failedIDSet := make(map[int64]struct{}, len(failedOps))
	for _, op := range failedOps {
		if op.dbID != 0 {
			failedIDSet[op.dbID] = struct{}{}
		}
	}
	trimIDs := make([]int64, 0, len(peekedIDs))
	for _, id := range peekedIDs {
		if _, fail := failedIDSet[id]; fail {
			continue
		}
		trimIDs = append(trimIDs, id)
	}
	s.trimPendingList(ctx, trimIDs)

	// Process failed ops: increment fail_count and dead-letter once
	// the cap is hit. Must come AFTER trim so successful siblings are
	// already gone from the queue — otherwise a follow-up batch could
	// re-pick them up.
	if len(failedOps) > 0 {
		s.requeueFailedOps(ctx, payload, failedOps)
	}

	logger.Infof(ctx, "wiki ingest: batch completed for KB %s, %d ops, %d pages affected", payload.KnowledgeBaseID, len(pendingOps), len(allPagesAffected))

	followUpScheduled = s.scheduleFollowUp(ctx, payload)
	return nil
}

func (s *wikiIngestService) mapOneDocument(
	ctx context.Context,
	chatModel chat.Chat,
	payload WikiIngestPayload,
	op WikiPendingOp,
	batchCtx *WikiBatchContext,
) (*docIngestResult, []SlugUpdate, error) {
	docStartedAt := time.Now()
	knowledgeID := op.KnowledgeID
	lang := op.Language

	// Guard against the ingest/delete race: if the user deleted the doc while
	// this task was queued (wikiIngestDelay = 30s) or while an earlier stage
	// was in flight, we must NOT proceed to LLM extraction — doing so would
	// create wiki pages whose source_refs point at a ghost knowledge ID,
	// permanently unreachable via wiki_read_source_doc.
	if s.isKnowledgeGone(ctx, payload.KnowledgeBaseID, knowledgeID) {
		logger.Infof(ctx, "wiki ingest: knowledge %s has been deleted, skip map", knowledgeID)
		return nil, nil, nil
	}

	chunks, err := s.chunkRepo.ListChunksByKnowledgeID(ctx, payload.TenantID, knowledgeID)
	if err != nil {
		return nil, nil, fmt.Errorf("get chunks: %w", err)
	}
	if len(chunks) == 0 {
		logger.Infof(ctx, "wiki ingest: document %s has no chunks, skip", knowledgeID)
		return nil, nil, nil
	}

	content := reconstructEnrichedContent(ctx, s.chunkRepo, payload.TenantID, chunks)
	rawRuneCount := len([]rune(content))
	if len([]rune(content)) > maxContentForWiki {
		content = string([]rune(content)[:maxContentForWiki])
	}
	logger.Infof(ctx, "wiki ingest: doc %s chunks=%d content_len(raw=%d,truncated=%d)", knowledgeID, len(chunks), rawRuneCount, len([]rune(content)))

	// Refuse to run LLM-based extraction when the document carries no real
	// text — e.g. a scanned PDF whose pages were converted to images but where
	// VLM OCR produced nothing usable. Without this guard the LLM would have
	// only image markup left and would happily fabricate entities/concepts.
	if !hasSufficientTextContent(content) {
		logger.Warnf(ctx,
			"wiki ingest: doc %s has insufficient text content after stripping image markup (raw_len=%d), skipping LLM extraction",
			knowledgeID, rawRuneCount,
		)
		return nil, nil, nil
	}

	docTitle := knowledgeID
	if kn, err := s.knowledgeSvc.GetKnowledgeByIDOnly(ctx, knowledgeID); err == nil && kn != nil && kn.Title != "" {
		docTitle = kn.Title
	} else {
		for _, ch := range chunks {
			if ch.Content != "" {
				lines := strings.SplitN(ch.Content, "\n", 2)
				if len(lines) > 0 && len(lines[0]) > 0 && len(lines[0]) < 200 {
					docTitle = strings.TrimPrefix(strings.TrimSpace(lines[0]), "# ")
					break
				}
			}
		}
	}

	// Citation source reference. We deliberately use only the knowledge ID
	// (not docTitle, which is typically the upload filename) so the filename
	// does not leak into citation strings that downstream LLM prompts may
	// surface during wiki page editing.
	sourceRef := knowledgeID
	oldPageSlugs := s.getExistingPageSlugsForKnowledge(ctx, payload.KnowledgeBaseID, knowledgeID)

	// Pass 0: lightweight candidate slug extraction (skeleton only).
	// On failure we fall back to the legacy single-shot extractor so the doc
	// still gets ingested, just without chunk-level citations.
	var (
		extractedEntities []extractedItem
		extractedConcepts []extractedItem
		slugItems         map[string]extractedItem
		pass0Failed       bool
	)
	logger.Infof(ctx, "wiki ingest: pass 0 — extracting candidate slugs for %s", knowledgeID)
	extractedEntities, extractedConcepts, slugItems, err = s.extractCandidateSlugs(ctx, chatModel, payload.KnowledgeBaseID, content, lang, oldPageSlugs, batchCtx)
	if err != nil {
		logger.Warnf(ctx, "wiki ingest: pass 0 failed for %s (%v) — falling back to legacy extractor", knowledgeID, err)
		pass0Failed = true
		extractedEntities, extractedConcepts, slugItems, err = s.extractEntitiesAndConceptsNoUpsert(ctx, chatModel, payload.KnowledgeBaseID, content, lang, oldPageSlugs, batchCtx)
		if err != nil {
			logger.Warnf(ctx, "wiki ingest: legacy fallback also failed for %s: %v", knowledgeID, err)
			return nil, nil, err
		}
	}

	// Build slug listing for Summary's wiki-link input.
	var summaryExtractedPages []string
	for slug := range slugItems {
		summaryExtractedPages = append(summaryExtractedPages, slug)
	}
	// Wiki summary slug is derived from the knowledge ID rather than the
	// docTitle (which is typically the upload filename). Filename-based slugs
	// like "summary/mx5280-pdf" expose the filename in cross-link contexts
	// that downstream LLM prompts read; a UUID-based slug is uglier but
	// hallucination-safe.
	summarySlug := fmt.Sprintf("summary/%s", slugify(knowledgeID))
	var slugListing string
	for _, slug := range summaryExtractedPages {
		if item, ok := slugItems[slug]; ok {
			aliases := ""
			if len(item.Aliases) > 0 {
				aliases = fmt.Sprintf(" (Aliases: %s)", strings.Join(item.Aliases, ", "))
			}
			slugListing += fmt.Sprintf("- [[%s]] = %s%s\n", slug, item.Name, aliases)
		} else {
			slugListing += fmt.Sprintf("- [[%s]]\n", slug)
		}
	}

	// Summary and chunk classification are independent given Pass 0 output —
	// run them in parallel. Summary handles wiki-link injection; classification
	// attaches concrete chunk IDs to each candidate slug.
	var (
		summaryContent string
		summaryErr     error
		citations      map[string][]string
		newSlugs       []newSlugFromCitation
		batchCount     int
	)

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		summaryContent, summaryErr = s.generateWithTemplate(ctx, chatModel, agent.WikiSummaryPrompt, map[string]string{
			"Content":        content,
			"Language":       lang,
			"ExtractedSlugs": slugListing,
		})
	}()
	go func() {
		defer wg.Done()
		// Skip citation pass when Pass 0 has fallen back to the legacy path —
		// the legacy output already contains paraphrased Details, so chunk
		// citations would be redundant and we'd spend LLM calls for nothing.
		if pass0Failed {
			citations = map[string][]string{}
			return
		}
		candidatesXML := renderCandidateSlugsXML(extractedEntities, extractedConcepts)
		citations, newSlugs, batchCount = s.classifyChunkCitations(ctx, chatModel, candidatesXML, chunks, lang)
	}()
	wg.Wait()

	// Merge citations back into the item structs (non-failing; items without
	// citations simply keep their Description+Details fallback).
	var uncited int
	extractedEntities, extractedConcepts, uncited = mergeCitationsIntoItems(extractedEntities, extractedConcepts, citations, newSlugs)

	// Rebuild slugItems so stale entries (for slugs that did not survive the
	// merge) and brand-new slugs discovered by the citation pass are both
	// reflected in summaryExtractedPages tracking.
	slugItems = make(map[string]extractedItem, len(extractedEntities)+len(extractedConcepts))
	for _, item := range extractedEntities {
		if item.Slug != "" && item.Name != "" {
			slugItems[item.Slug] = item
		}
	}
	for _, item := range extractedConcepts {
		if item.Slug != "" && item.Name != "" {
			slugItems[item.Slug] = item
		}
	}

	// extractedPages records every wiki page this document materialized
	// (entities, concepts, plus the summary page appended below). The
	// slug is used for link/retract bookkeeping; the title is captured
	// for the log feed so the user sees "提供本学位在线验证报告查询…"
	// rather than "entity/xue-xin-wang".
	extractedPages := make([]types.WikiLogPageRef, 0, len(slugItems)+1)
	for slug, item := range slugItems {
		title := item.Name
		if title == "" {
			title = slug
		}
		extractedPages = append(extractedPages, types.WikiLogPageRef{Slug: slug, Title: title})
	}

	// Count total distinct chunks cited across all slugs for logging.
	citedChunkSet := make(map[string]bool)
	for _, ids := range citations {
		for _, id := range ids {
			citedChunkSet[id] = true
		}
	}

	var updates []SlugUpdate
	// docSummaryLine is the one-sentence headline used for terse log/audit
	// previews and for <document_added> blocks in retract prompts.
	// docSummary is the full summary body attached to each entity/concept
	// update so the editor model gets rich framing in <source_context>.
	var docSummaryLine string
	var docSummary string

	if summaryErr != nil {
		// Summary is the headline artifact of an ingested document — a
		// document with no summary page is half-ingested and leaves the
		// entity/concept updates hanging without a root to link back to
		// from the index. Historically we just logged and moved on,
		// which meant a single transient 504 permanently dropped the
		// summary page for that document.
		//
		// Returning an error here sends the op to failedOps (see the
		// map-phase loop in ProcessWikiIngest), which requeueFailedOps
		// appends back onto the pending list so the next batch retries.
		// The internal retries in generateWithTemplate already exhaust
		// the LLM's own transient-error budget before we give up here.
		logger.Errorf(ctx, "wiki ingest: generate summary failed for %s, will requeue: %v", knowledgeID, summaryErr)
		return nil, nil, fmt.Errorf("generate summary: %w", summaryErr)
	}
	sumLine, sumBody := splitSummaryLine(summaryContent)
	if sumBody == "" {
		sumBody = summaryContent
	}
	if sumLine == "" {
		sumLine = docTitle
	}
	docSummaryLine = sumLine
	docSummary = sumBody
	if strings.TrimSpace(docSummary) == "" {
		docSummary = sumLine
	}
	updates = append(updates, SlugUpdate{
		Slug:        summarySlug,
		Type:        types.WikiPageTypeSummary,
		DocTitle:    docTitle,
		KnowledgeID: knowledgeID,
		SourceRef:   sourceRef,
		Language:    lang,
		SummaryLine: sumLine,
		SummaryBody: sumBody,
	})
	extractedPages = append(extractedPages, types.WikiLogPageRef{Slug: summarySlug, Title: docTitle})

	// Entities
	for _, item := range extractedEntities {
		if item.Slug != "" {
			updates = append(updates, SlugUpdate{
				Slug:         item.Slug,
				Type:         types.WikiPageTypeEntity,
				Item:         item,
				DocTitle:     docTitle,
				KnowledgeID:  knowledgeID,
				SourceRef:    sourceRef,
				Language:     lang,
				SourceChunks: item.SourceChunks,
				DocSummary:   docSummary,
			})
		}
	}

	// Concepts
	for _, item := range extractedConcepts {
		if item.Slug != "" {
			updates = append(updates, SlugUpdate{
				Slug:         item.Slug,
				Type:         types.WikiPageTypeConcept,
				Item:         item,
				DocTitle:     docTitle,
				KnowledgeID:  knowledgeID,
				SourceRef:    sourceRef,
				Language:     lang,
				SourceChunks: item.SourceChunks,
				DocSummary:   docSummary,
			})
		}
	}

	// Reconcile old page set against new extraction.
	//
	// Three cases:
	//
	//  (a) oldSlug ∉ new  → "retractStale": the doc no longer mentions this
	//      page's subject, so strip its ref (and possibly delete the page
	//      if this was the only source). Passes the NEW content as the
	//      retract context — if the LLM finds matching facts it trims
	//      them, otherwise the retract is a near no-op, which is fine.
	//
	//  (b) oldSlug ∈ new AND slug is an entity/concept page  → reparse
	//      swap: emit BOTH a "retract" (carrying the doc's PRIOR summary
	//      body as the old-version signal) AND the normal addition. The
	//      reduce stage sees HasAdditions=1 + HasRetractions=1 and the
	//      WikiPageModifyPrompt correctly tells the editor model to
	//      remove the old K section and add the new K section in one
	//      pass — giving us replace-not-append semantics that "append
	//      new K on top of old K" would otherwise violate.
	//
	//  (c) oldSlug ∈ new AND slug is a summary page (summary/...) →
	//      nothing to do here. reduceSlugUpdates' summary branch
	//      unconditionally overwrites the whole page from the new
	//      SummaryBody, so emitting an extra retract would just be
	//      dead weight that the summary branch discards anyway.
	//
	// priorContribution is the doc's LAST summary body, fetched lazily
	// at this point (rather than pre-loaded into the batch context).
	// Empty on first-ever ingest — in that case oldPageSlugs is also
	// empty, so we never consult it.
	priorContribution := batchCtx.SummaryContentByKnowledgeID(ctx, knowledgeID)

	newSlugSet := make(map[string]bool, len(extractedPages))
	for _, ns := range extractedPages {
		newSlugSet[ns.Slug] = true
	}

	var reparseOverlap, staleCount int
	for oldSlug := range oldPageSlugs {
		if newSlugSet[oldSlug] {
			// Skip summary slugs — they're overwritten wholesale by the
			// summary update, retract would be ignored downstream.
			if strings.HasPrefix(oldSlug, "summary/") {
				continue
			}
			reparseOverlap++
			updates = append(updates, SlugUpdate{
				Slug:              oldSlug,
				Type:              "retract",
				RetractDocContent: priorContribution,
				DocTitle:          docTitle,
				KnowledgeID:       knowledgeID,
				Language:          lang,
			})
			continue
		}
		staleCount++
		updates = append(updates, SlugUpdate{
			Slug:              oldSlug,
			Type:              "retractStale",
			RetractDocContent: content,
			DocTitle:          docTitle,
			KnowledgeID:       knowledgeID,
			Language:          lang,
		})
	}

	logger.Infof(ctx,
		"wiki ingest: mapped knowledge %s title=%q candidates=%d chunks=%d batches=%d cited_chunks=%d uncited_slugs=%d new_slugs=%d updates=%d reparse_slugs=%d stale_slugs=%d pass0_fallback=%v elapsed=%s",
		knowledgeID, previewText(docTitle, 80),
		len(slugItems), len(chunks), batchCount, len(citedChunkSet), uncited, len(newSlugs),
		len(updates), reparseOverlap, staleCount, pass0Failed,
		time.Since(docStartedAt).Round(time.Millisecond),
	)

	return &docIngestResult{
		KnowledgeID: knowledgeID,
		DocTitle:    docTitle,
		Summary:     docSummaryLine,
		Pages:       extractedPages,
	}, updates, nil
}

func (s *wikiIngestService) extractEntitiesAndConceptsNoUpsert(
	ctx context.Context,
	chatModel chat.Chat,
	kbID string,
	content, lang string,
	oldPageSlugs map[string]bool,
	batchCtx *WikiBatchContext,
) ([]extractedItem, []extractedItem, map[string]extractedItem, error) {
	// Only entity/* and concept/* slugs are relevant for LLM slug-continuity —
	// summary slugs are code-generated from the knowledge ID and never appear
	// in the extraction output, so including them just wastes tokens and risks
	// confusing the model.
	var prevSlugsText string
	if len(oldPageSlugs) > 0 {
		var sb strings.Builder
		for slug := range oldPageSlugs {
			if !strings.HasPrefix(slug, "entity/") && !strings.HasPrefix(slug, "concept/") {
				continue
			}
			fmt.Fprintf(&sb, "- %s\n", slug)
		}
		prevSlugsText = sb.String()
	}
	if prevSlugsText == "" {
		prevSlugsText = "(none — this is a new document)"
	}

	extractionJSON, err := s.generateWithTemplate(ctx, chatModel, agent.WikiKnowledgeExtractPrompt, map[string]string{
		"Content":       content,
		"Language":      lang,
		"PreviousSlugs": prevSlugsText,
	})
	if err != nil {
		return nil, nil, nil, fmt.Errorf("combined extraction failed: %w", err)
	}

	extractionJSON = cleanLLMJSON(extractionJSON)

	var result combinedExtraction
	if err := json.Unmarshal([]byte(extractionJSON), &result); err != nil {
		logger.Warnf(ctx, "wiki ingest: failed to parse combined extraction JSON: %v\nRaw: %s", err, extractionJSON)
		return nil, nil, nil, fmt.Errorf("parse combined extraction JSON: %w", err)
	}

	// Dedup pre-filter is dispatched against the wiki page repo via
	// pg_trgm (see deduplicateExtractedBatch). Until the trgm path
	// lands the dedup pre-filter degrades to "no dedup" which is the
	// safe default — the LLM merge call simply doesn't get a candidate
	// list and the items pass through unchanged.
	result.Entities, result.Concepts = s.deduplicateExtractedBatch(
		ctx, chatModel, kbID, result.Entities, result.Concepts,
	)

	slugItems := make(map[string]extractedItem)
	for _, item := range result.Entities {
		if item.Slug != "" && item.Name != "" {
			slugItems[item.Slug] = item
		}
	}
	for _, item := range result.Concepts {
		if item.Slug != "" && item.Name != "" {
			slugItems[item.Slug] = item
		}
	}

	return result.Entities, result.Concepts, slugItems, nil
}

// reduceSlugUpdates returns:
//   - changed:          whether the wiki page was created or updated
//   - affectedType:     "ingest" or "retract" — drives downstream bookkeeping
//   - additionFailed:   true iff the slug had entity/concept additions queued
//     AND the WikiPageModifyPrompt LLM call failed, so no page exists/was
//     refreshed for it. Callers use this to sanitize dead [[slug]] links
//     elsewhere (e.g. in the doc's summary page) and to drop the slug from
//     the wiki log feed so users don't see a clickable entry that 404s.
//   - err:              transport / repo error from the persisted upsert.
func (s *wikiIngestService) reduceSlugUpdates(
	ctx context.Context,
	chatModel chat.Chat,
	kbID string,
	slug string,
	updates []SlugUpdate,
	tenantID uint64,
	batchCtx *WikiBatchContext,
) (changed bool, affectedType string, additionFailed bool, err error) {
	// Final safety net for the ingest/delete race: between Map (which already
	// checks isKnowledgeGone) and Reduce there is a long LLM call where the
	// source document may be deleted. Drop any addition/summary updates whose
	// knowledge no longer exists so we don't resurrect a ghost source_ref.
	// Retract updates are kept — they actively remove refs, which is what we
	// want when the doc is gone.
	updates = s.filterLiveUpdates(ctx, kbID, updates)
	if len(updates) == 0 {
		return false, "", false, nil
	}

	var page *types.WikiPage
	page, err = s.wikiService.GetPageBySlug(ctx, kbID, slug)
	exists := (err == nil && page != nil)

	if !exists {
		hasAdditions := false
		for _, u := range updates {
			if u.Type == types.WikiPageTypeEntity || u.Type == types.WikiPageTypeConcept || u.Type == "summary" {
				hasAdditions = true
				break
			}
		}
		if !hasAdditions {
			return false, "", false, nil
		}

		page = &types.WikiPage{
			ID:              uuid.New().String(),
			TenantID:        tenantID,
			KnowledgeBaseID: kbID,
			Slug:            slug,
			Status:          types.WikiPageStatusDraft,
			SourceRefs:      types.StringArray{},
			Aliases:         types.StringArray{},
		}
		// Reset err: GetPageBySlug returned "not found" which we just
		// handled by synthesizing the page. Don't leak that error to
		// the named return — subsequent assignments would mask it
		// anyway, but be explicit.
		err = nil
	}

	affectedType = "ingest"

	var summaryUpdate *SlugUpdate
	var retracts []SlugUpdate
	var additions []SlugUpdate

	for i, u := range updates {
		if u.Type == "summary" {
			summaryUpdate = &updates[i]
		} else if u.Type == "retract" || u.Type == "retractStale" {
			retracts = append(retracts, u)
			affectedType = "retract"
		} else if u.Type == types.WikiPageTypeEntity || u.Type == types.WikiPageTypeConcept {
			additions = append(additions, u)
			affectedType = "ingest" // Additions override retracts type
		}
	}

	if summaryUpdate != nil {
		page.Title = summaryUpdate.DocTitle + " - Summary"
		page.Content = summaryUpdate.SummaryBody
		page.Summary = summaryUpdate.SummaryLine
		page.PageType = types.WikiPageTypeSummary
		page.SourceRefs = appendUnique(page.SourceRefs, summaryUpdate.SourceRef)
		// Summary pages don't carry chunk-level citations (they are document-
		// level synopses generated from the whole content). Clear any stale
		// chunk refs that may remain if this slug was once an entity page
		// and got converted to a summary page.
		page.ChunkRefs = types.StringArray{}
		changed = true

		if exists {
			_, err = s.wikiService.UpdatePage(ctx, page)
		} else {
			_, err = s.wikiService.CreatePage(ctx, page)
		}
		return changed, affectedType, false, err
	}

	var remainingSourcesContent strings.Builder
	var deletedContent strings.Builder
	var relatedSlugs strings.Builder
	var newContentBuilder strings.Builder
	var docTitles []string
	var language string

	if len(retracts) > 0 {
		language = retracts[0].Language

		for _, r := range retracts {
			fmt.Fprintf(&deletedContent, "<document>\n<title>%s</title>\n<content>\n%s\n</content>\n</document>\n\n", r.DocTitle, r.RetractDocContent)
		}

		retractKIDs := make(map[string]bool)
		for _, r := range retracts {
			retractKIDs[r.KnowledgeID] = true
		}

		for _, ref := range page.SourceRefs {
			pipeIdx := strings.Index(ref, "|")
			var refKnowledgeID, refTitle string
			if pipeIdx > 0 {
				refKnowledgeID = ref[:pipeIdx]
				refTitle = ref[pipeIdx+1:]
			} else {
				refKnowledgeID = ref
				refTitle = ref
			}

			if retractKIDs[refKnowledgeID] {
				continue
			}

			if content := batchCtx.SummaryContentByKnowledgeID(ctx, refKnowledgeID); content != "" {
				fmt.Fprintf(&remainingSourcesContent, "<document>\n<title>%s</title>\n<content>\n%s\n</content>\n</document>\n\n", refTitle, content)
			} else {
				fmt.Fprintf(&remainingSourcesContent, "<document>\n<title>%s</title>\n<content>\n(summary not available)\n</content>\n</document>\n\n", refTitle)
			}
		}
		if remainingSourcesContent.Len() == 0 {
			remainingSourcesContent.WriteString("(no remaining sources)")
		}

		newRefs := types.StringArray{}
		for _, ref := range page.SourceRefs {
			pipeIdx := strings.Index(ref, "|")
			refKnowledgeID := ref
			if pipeIdx > 0 {
				refKnowledgeID = ref[:pipeIdx]
			}
			if !retractKIDs[refKnowledgeID] {
				newRefs = append(newRefs, ref)
			}
		}
		page.SourceRefs = newRefs
	}

	if len(additions) > 0 {
		language = additions[0].Language

		// Resolve SourceChunks → chunk contents in a single batched query per
		// knowledge ID, so the <new_information> block can quote the chunks
		// verbatim instead of relying on the short Details paraphrase.
		chunkContentByID := s.resolveCitedChunks(ctx, tenantID, additions)

		for _, add := range additions {
			cited := collectCitedChunkContent(add.SourceChunks, chunkContentByID)
			// Frame the chunks with the document-level summary body so the
			// editor model knows BOTH what the document is about AND what
			// kind of document it is (resume vs announcement vs product
			// page vs schedule). The one-sentence headline alone was too
			// terse to keep the editor grounded on longer or multi-topic
			// source documents, and calibrating tone (self-reported vs
			// third-party authoritative) benefits from the richer context.
			sourceCtx := strings.TrimSpace(add.DocSummary)
			sourceCtxBlock := ""
			if sourceCtx != "" {
				sourceCtxBlock = fmt.Sprintf("<source_context>\n%s\n</source_context>\n", sourceCtx)
			}
			if cited != "" {
				fmt.Fprintf(&newContentBuilder,
					"<document>\n<title>%s</title>\n%s<content>\n**%s**: %s\n\n%s\n</content>\n</document>\n\n",
					add.DocTitle, sourceCtxBlock, add.Item.Name, add.Item.Description, cited)
			} else {
				// Fallback: no citations available (legacy path, citation pass
				// failed, or bad chunk IDs were filtered out) — stick with
				// the short Details summary so the page still gets real text.
				fmt.Fprintf(&newContentBuilder,
					"<document>\n<title>%s</title>\n%s<content>\n**%s**: %s\n\n%s\n</content>\n</document>\n\n",
					add.DocTitle, sourceCtxBlock, add.Item.Name, add.Item.Description, add.Item.Details)
			}
			docTitles = appendUnique(docTitles, add.DocTitle)

			for _, alias := range add.Item.Aliases {
				page.Aliases = appendUnique(page.Aliases, alias)
			}
			page.SourceRefs = appendUnique(page.SourceRefs, add.SourceRef)

			if page.Title == "" {
				page.Title = add.Item.Name
			}
			if page.PageType == "" {
				page.PageType = add.Type
			}
		}
	}

	if len(additions) > 0 || len(retracts) > 0 {
		titles := batchCtx.SlugTitleMany(ctx, []string(page.OutLinks))
		for _, outSlug := range page.OutLinks {
			if title := titles[outSlug]; title != "" {
				fmt.Fprintf(&relatedSlugs, "- %s (%s)\n", outSlug, title)
			}
		}

		existingContent := page.Content
		if !exists || existingContent == "" {
			existingContent = "(New page)"
		}

		hasAdditionsStr := ""
		if len(additions) > 0 {
			hasAdditionsStr = "1"
		}
		hasRetractionsStr := ""
		if len(retracts) > 0 {
			hasRetractionsStr = "1"
		}

		// Fall back gracefully if title/type are still unset (shouldn't happen
		// for well-formed updates — both get populated from `additions` above,
		// and retract-only paths require an existing page — but stay defensive
		// so we never feed the LLM an empty identity block).
		pageTitle := page.Title
		if pageTitle == "" {
			pageTitle = slug
		}
		pageType := string(page.PageType)
		if pageType == "" {
			pageType = "wiki page"
		}
		pageAliases := strings.Join(page.Aliases, ", ")

		var updatedContent string
		updatedContent, err = s.generateWithTemplate(ctx, chatModel, agent.WikiPageModifyPrompt, map[string]string{
			"HasAdditions":            hasAdditionsStr,
			"HasRetractions":          hasRetractionsStr,
			"PageSlug":                slug,
			"PageTitle":               pageTitle,
			"PageType":                pageType,
			"PageAliases":             pageAliases,
			"ExistingContent":         existingContent,
			"NewContent":              newContentBuilder.String(),
			"DeletedContent":          deletedContent.String(),
			"RemainingSourcesContent": remainingSourcesContent.String(),
			"AvailableSlugs":          relatedSlugs.String(),
			"Language":                language,
		})

		if err == nil && updatedContent != "" {
			updatedSummary, updatedBody := splitSummaryLine(updatedContent)
			if updatedBody != "" {
				page.Content = updatedBody
			} else {
				page.Content = updatedContent
			}
			if updatedSummary != "" {
				page.Summary = updatedSummary
			}
			changed = true
		} else if err != nil {
			logger.Warnf(ctx, "wiki ingest: update/retract failed for slug %s: %v", slug, err)
			// Flag addition failures so the batch can sanitize stale
			// [[slug]] references in the doc's summary page and prune
			// the slug from log entries — otherwise the wiki feed shows
			// a clickable entry whose target page doesn't exist.
			// Retract-only failures don't poison anything (they leave
			// the existing page unchanged), so don't flag those.
			if len(additions) > 0 {
				additionFailed = true
			}
			// Don't propagate the LLM error to the named return: it has
			// already been logged, and the eg.Go caller would otherwise
			// log it a second time as "reduce failed for slug".
			err = nil
		}
	}

	if changed {
		// Refresh chunk refs in-place on the page so they persist alongside
		// the rest of the row. Retract-only updates (no additions) preserve
		// the existing refs; addition rounds append the newly-cited chunks
		// on top of what was already there, deduplicated.
		page.ChunkRefs = mergeChunkRefs(page.ChunkRefs, additions)
		if exists {
			_, err = s.wikiService.UpdatePage(ctx, page)
		} else {
			_, err = s.wikiService.CreatePage(ctx, page)
		}
		return true, affectedType, additionFailed, err
	}

	return false, "", additionFailed, nil
}

// mergeChunkRefs unions the chunk IDs currently on the page with the ones
// cited by this batch's additions, preserving insertion order and dropping
// duplicates. Empty strings are filtered out so a malformed source_chunks
// array can't leave junk in the column.
//
// A retract round with no additions leaves the current refs untouched —
// retract-only paths don't carry chunk IDs (only knowledge IDs), and we
// can't surgically filter without that info. The next time the slug is
// re-materialized via additions the fresh chunks will overlay on top.
func mergeChunkRefs(current types.StringArray, additions []SlugUpdate) types.StringArray {
	seen := make(map[string]bool, len(current))
	out := make(types.StringArray, 0, len(current))
	for _, id := range current {
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	for _, add := range additions {
		for _, chunkID := range add.SourceChunks {
			if chunkID == "" || seen[chunkID] {
				continue
			}
			seen[chunkID] = true
			out = append(out, chunkID)
		}
	}
	return out
}
