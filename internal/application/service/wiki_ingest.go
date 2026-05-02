package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
	"text/template"
	"time"
	"unicode/utf8"

	"github.com/Tencent/WeKnora/internal/agent"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/searchutil"
	"github.com/Tencent/WeKnora/internal/tracing/langfuse"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/hibiken/asynq"
	"github.com/redis/go-redis/v9"
)

// ErrWikiIngestConcurrent is returned by the wiki ingest handler when another
// batch is already running for the same KB (i.e. the `wiki:active:<kbID>`
// Redis lock is held). The asynq server's RetryDelayFunc uses errors.Is on
// this sentinel to apply a short, fixed retry delay instead of asynq's default
// exponential backoff — otherwise a freshly orphaned lock (e.g. from a crash
// or restart) would force newcomers to wait minutes even after the lock
// naturally expires.
var ErrWikiIngestConcurrent = errors.New("concurrent wiki task active")

const (
	// maxContentForWiki limits the document content sent to LLM for wiki generation
	maxContentForWiki = 32768

	// wikiPendingKeyPrefix is the Redis key prefix for pending wiki ingest document lists.
	// Key format: wiki:pending:{kbID} → Redis List of knowledge IDs.
	wikiPendingKeyPrefix = "wiki:pending:"

	// wikiActiveKeyPrefix is the Redis key for the "batch in progress" flag.
	// Key format: wiki:active:{kbID} → "1" with TTL. Prevents concurrent batches.
	wikiActiveKeyPrefix = "wiki:active:"

	// wikiIngestDelay is how long to wait after a document is added before
	// the batch task fires. Debounces rapid uploads.
	wikiIngestDelay = 30 * time.Second

	// wikiPendingTTL prevents stale pending lists from accumulating.
	wikiPendingTTL = 24 * time.Hour

	// wikiMaxDocsPerBatch limits how many documents a single batch processes.
	// Prevents unbounded execution time. Remaining docs stay in the pending list
	// and are picked up by the follow-up task.
	wikiMaxDocsPerBatch = 5

	// wikiDeletedKeyPrefix is the Redis key prefix for "recently deleted
	// knowledge" tombstones. Key: wiki:deleted:{kbID}:{knowledgeID}. Written
	// by cleanupWikiOnKnowledgeDelete so that any wiki_ingest task still in
	// flight (or queued) for this knowledge can fast-path skip without
	// hitting the DB. TTL > wikiIngestDelay so it's guaranteed to outlast
	// any in-flight ingest.
	wikiDeletedKeyPrefix = "wiki:deleted:"

	// wikiDeletedTTL bounds how long we remember a deletion. Must comfortably
	// exceed the longest plausible ingest run (LLM extraction + reduce).
	wikiDeletedTTL = 1 * time.Hour

	// wikiActiveLockTTL is the TTL for the per-KB "batch in progress" flag.
	// Kept short (relative to total batch runtime) so that if the owning
	// process crashes without running its `defer Del`, the orphaned lock
	// expires quickly and newcomers aren't blocked. A periodic renew
	// (wikiActiveLockRenew) keeps the lock alive while the handler is
	// genuinely still running.
	wikiActiveLockTTL = 60 * time.Second

	// wikiActiveLockRenew is how often the in-flight handler bumps the TTL.
	// Must be comfortably shorter than wikiActiveLockTTL so a single missed
	// tick (GC pause, Redis blip) doesn't let the lock slip out from under a
	// live handler.
	wikiActiveLockRenew = 20 * time.Second
)

// WikiDeletedTombstoneKey returns the Redis key used to mark a knowledge as
// recently deleted, so wiki_ingest tasks in flight can short-circuit. Exposed
// so knowledgeService.cleanupWikiOnKnowledgeDelete can write the same key
// without duplicating the format string.
func WikiDeletedTombstoneKey(kbID, knowledgeID string) string {
	return wikiDeletedKeyPrefix + kbID + ":" + knowledgeID
}

// WikiIngestPayload is the asynq task payload for wiki ingest batch trigger.
// The actual document IDs are stored in a Redis list (wiki:pending:{kbID}).
// KnowledgeID is only used as fallback in Lite mode (no Redis).
type WikiIngestPayload struct {
	types.TracingContext
	TenantID        uint64 `json:"tenant_id"`
	KnowledgeBaseID string `json:"knowledge_base_id"`
	Language        string `json:"language,omitempty"`
	// Fallback for Lite mode (no Redis)
	LiteOps []WikiPendingOp `json:"lite_ops,omitempty"`
}

// WikiRetractPayload is the asynq task payload for wiki content retraction
type WikiRetractPayload struct {
	types.TracingContext
	TenantID        uint64   `json:"tenant_id"`
	KnowledgeBaseID string   `json:"knowledge_base_id"`
	KnowledgeID     string   `json:"knowledge_id"`
	DocTitle        string   `json:"doc_title"`
	DocSummary      string   `json:"doc_summary,omitempty"` // one-line summary of the deleted document
	Language        string   `json:"language,omitempty"`
	PageSlugs       []string `json:"page_slugs"`
}

const (
	WikiOpIngest  = "ingest"
	WikiOpRetract = "retract"
)

// WikiPendingOp represents a single operation in the Redis pending queue
type WikiPendingOp struct {
	Op          string `json:"op"`
	KnowledgeID string `json:"knowledge_id"`
	// Ingest fields
	Language string `json:"language,omitempty"`
	// Retract fields
	DocTitle   string   `json:"doc_title,omitempty"`
	DocSummary string   `json:"doc_summary,omitempty"`
	PageSlugs  []string `json:"page_slugs,omitempty"`
}

// wikiIngestService handles the LLM-powered wiki generation pipeline
type wikiIngestService struct {
	wikiService  interfaces.WikiPageService
	kbService    interfaces.KnowledgeBaseService
	knowledgeSvc interfaces.KnowledgeService
	chunkRepo    interfaces.ChunkRepository
	modelService interfaces.ModelService
	task         interfaces.TaskEnqueuer
	redisClient  *redis.Client // nil in Lite mode (no Redis)
	// liteLocks provides per-KB mutual exclusion in Lite mode (no Redis).
	// Keys are kbID strings; values are unused (presence = locked).
	liteLocks sync.Map
}

// NewWikiIngestService creates a new wiki ingest service
func NewWikiIngestService(
	wikiService interfaces.WikiPageService,
	kbService interfaces.KnowledgeBaseService,
	knowledgeSvc interfaces.KnowledgeService,
	chunkRepo interfaces.ChunkRepository,
	modelService interfaces.ModelService,
	task interfaces.TaskEnqueuer,
	redisClient *redis.Client,
) interfaces.TaskHandler {
	svc := &wikiIngestService{
		wikiService:  wikiService,
		kbService:    kbService,
		knowledgeSvc: knowledgeSvc,
		chunkRepo:    chunkRepo,
		modelService: modelService,
		task:         task,
		redisClient:  redisClient,
	}
	return svc
}

// EnqueueWikiIngest adds a document to the wiki ingest queue.
//
// Architecture: each document upload pushes its knowledgeID to a Redis pending list,
// then schedules a delayed asynq task. When the task fires, it atomically drains the
// entire list and processes ALL pending documents in one batch.
//
// If multiple uploads happen within the delay window (30s), each one schedules a task,
// but the FIRST task to fire drains the list and processes everything. Subsequent tasks
// fire, find an empty list, and exit immediately (no-op). This gives us natural batching
// without any locks or task deduplication.
//
//	t=0s   doc1 → RPush + Enqueue(delay=30s, id=random1)
//	t=5s   doc2 → RPush + Enqueue(delay=30s, id=random2)
//	t=10s  doc3 → RPush + Enqueue(delay=30s, id=random3)
//	t=30s  random1 fires → drain [doc1,doc2,doc3] → process all
//	t=35s  random2 fires → drain [] → no-op return
//	t=40s  random3 fires → drain [] → no-op return
//
// In Lite mode (no Redis), falls back to immediate per-document execution.
func EnqueueWikiIngest(ctx context.Context, task interfaces.TaskEnqueuer, redisClient *redis.Client, tenantID uint64, kbID, knowledgeID string) {
	lang, _ := types.LanguageFromContext(ctx)

	payload := WikiIngestPayload{
		TenantID:        tenantID,
		KnowledgeBaseID: kbID,
		Language:        lang,
	}

	// Push to Redis pending list (if Redis available)
	if redisClient != nil {
		pendingKey := wikiPendingKeyPrefix + kbID
		op := WikiPendingOp{
			Op:          WikiOpIngest,
			KnowledgeID: knowledgeID,
			Language:    lang,
		}
		opBytes, _ := json.Marshal(op)
		redisClient.RPush(ctx, pendingKey, string(opBytes))
		redisClient.Expire(ctx, pendingKey, wikiPendingTTL)
	} else {
		// Fallback for Lite mode (no Redis)
		payload.LiteOps = []WikiPendingOp{{
			Op:          WikiOpIngest,
			KnowledgeID: knowledgeID,
			Language:    lang,
		}}
	}

	langfuse.InjectTracing(ctx, &payload)
	payloadBytes, _ := json.Marshal(payload)

	t := asynq.NewTask(types.TypeWikiIngest, payloadBytes,
		asynq.Queue("low"),
		asynq.MaxRetry(10), // Increased from 3 to 10 to ensure it can outlast the 5-minute active lock TTL
		asynq.Timeout(60*time.Minute),
		asynq.ProcessIn(wikiIngestDelay),
	)
	if _, err := task.Enqueue(t); err != nil {
		logger.Warnf(ctx, "wiki ingest: failed to enqueue task: %v", err)
	}
}

// EnqueueWikiRetract enqueues an async wiki content retraction task
func EnqueueWikiRetract(ctx context.Context, task interfaces.TaskEnqueuer, redisClient *redis.Client, payload WikiRetractPayload) {
	ingestPayload := WikiIngestPayload{
		TenantID:        payload.TenantID,
		KnowledgeBaseID: payload.KnowledgeBaseID,
		Language:        payload.Language,
	}

	op := WikiPendingOp{
		Op:          WikiOpRetract,
		KnowledgeID: payload.KnowledgeID,
		DocTitle:    payload.DocTitle,
		DocSummary:  payload.DocSummary,
		PageSlugs:   payload.PageSlugs,
		Language:    payload.Language,
	}

	if redisClient != nil {
		pendingKey := wikiPendingKeyPrefix + payload.KnowledgeBaseID
		opBytes, _ := json.Marshal(op)
		redisClient.RPush(ctx, pendingKey, string(opBytes))
		redisClient.Expire(ctx, pendingKey, wikiPendingTTL)
	} else {
		// Fallback for Lite mode (no Redis)
		ingestPayload.LiteOps = []WikiPendingOp{op}
	}

	langfuse.InjectTracing(ctx, &ingestPayload)
	payloadBytes, _ := json.Marshal(ingestPayload)
	t := asynq.NewTask(types.TypeWikiIngest, payloadBytes,
		asynq.Queue("low"),
		asynq.MaxRetry(10), // Increased from 3 to 10 to outlast the active lock TTL
		asynq.Timeout(60*time.Minute),
		asynq.ProcessIn(5*time.Second), // Retract can trigger the batch quickly
	)
	if _, err := task.Enqueue(t); err != nil {
		logger.Warnf(ctx, "wiki retract: failed to enqueue task: %v", err)
	}
}

// Handle implements interfaces.TaskHandler for asynq task processing.
// Wiki ingest tasks are debounced via asynq.Unique + ProcessIn, so at most
// one ingest task runs per KB at a time. No distributed lock needed.
func (s *wikiIngestService) Handle(ctx context.Context, t *asynq.Task) error {
	return s.ProcessWikiIngest(ctx, t)
}

// peekPendingList gets up to wikiMaxDocsPerBatch entries from the Redis pending list
// WITHOUT removing them. It returns the unique ops and the actual number of items peeked.
func (s *wikiIngestService) peekPendingList(ctx context.Context, kbID string) ([]WikiPendingOp, int) {
	if s.redisClient == nil {
		return nil, 0
	}
	pendingKey := wikiPendingKeyPrefix + kbID

	result, err := s.redisClient.LRange(ctx, pendingKey, 0, wikiMaxDocsPerBatch-1).Result()
	if err != nil {
		logger.Warnf(ctx, "wiki ingest: failed to peek pending list: %v", err)
		return nil, 0
	}

	var ops []WikiPendingOp
	for _, item := range result {
		if !strings.HasPrefix(item, "{") {
			// Backward compatibility: raw knowledgeID string
			ops = append(ops, WikiPendingOp{
				Op:          WikiOpIngest,
				KnowledgeID: item,
			})
			continue
		}
		var op WikiPendingOp
		if err := json.Unmarshal([]byte(item), &op); err == nil {
			ops = append(ops, op)
		} else {
			logger.Warnf(ctx, "wiki ingest: failed to unmarshal pending op: %v, raw_item: %.100s", err, item)
		}
	}

	// Deduplicate by KnowledgeID, keeping only the *last* operation for each document.
	// This optimizes out redundant sequences (e.g., upload then immediate delete: [ingest, retract] -> [retract]).
	seen := make(map[string]bool)
	var reversedUnique []WikiPendingOp
	for i := len(ops) - 1; i >= 0; i-- {
		op := ops[i]
		if !seen[op.KnowledgeID] {
			seen[op.KnowledgeID] = true
			reversedUnique = append(reversedUnique, op)
		}
	}

	// Reverse back to maintain chronological order
	var unique []WikiPendingOp
	for i := len(reversedUnique) - 1; i >= 0; i-- {
		unique = append(unique, reversedUnique[i])
	}

	return unique, len(result)
}

// trimPendingList removes the first `count` items from the Redis pending list.
func (s *wikiIngestService) trimPendingList(ctx context.Context, kbID string, count int) {
	if s.redisClient == nil || count <= 0 {
		return
	}
	pendingKey := wikiPendingKeyPrefix + kbID
	if err := s.redisClient.LTrim(ctx, pendingKey, int64(count), -1).Err(); err != nil {
		logger.Warnf(ctx, "wiki ingest: failed to trim pending list: %v", err)
	}
}

// requeueFailedOps re-enqueues failed operations for retry.
//
// Redis mode: appends ops back to the pending list tail so the next follow-up
// batch picks them up.
//
// Lite mode (no Redis): enqueues a new asynq task per failed op with a short
// delay, since there is no shared pending list to append to.
func (s *wikiIngestService) requeueFailedOps(ctx context.Context, payload WikiIngestPayload, ops []WikiPendingOp) {
	if s.redisClient != nil {
		pendingKey := wikiPendingKeyPrefix + payload.KnowledgeBaseID
		for _, op := range ops {
			data, err := json.Marshal(op)
			if err != nil {
				logger.Warnf(ctx, "wiki ingest: failed to marshal op for requeue: %v", err)
				continue
			}
			if err := s.redisClient.RPush(ctx, pendingKey, string(data)).Err(); err != nil {
				logger.Warnf(ctx, "wiki ingest: failed to requeue op %s: %v", op.KnowledgeID, err)
				continue
			}
			logger.Infof(ctx, "wiki ingest: re-queued failed op %s (%s) for retry", op.KnowledgeID, op.DocTitle)
		}
		return
	}

	// Lite mode: re-enqueue each failed op as a new asynq task.
	for _, op := range ops {
		retryPayload := WikiIngestPayload{
			TenantID:        payload.TenantID,
			KnowledgeBaseID: payload.KnowledgeBaseID,
			Language:        op.Language,
			LiteOps:         []WikiPendingOp{op},
		}
		langfuse.InjectTracing(ctx, &retryPayload)
		payloadBytes, _ := json.Marshal(retryPayload)
		t := asynq.NewTask(types.TypeWikiIngest, payloadBytes,
			asynq.Queue("low"),
			asynq.MaxRetry(10),
			asynq.Timeout(60*time.Minute),
			asynq.ProcessIn(wikiIngestDelay),
		)
		if _, err := s.task.Enqueue(t); err != nil {
			logger.Warnf(ctx, "wiki ingest: failed to requeue lite op %s: %v", op.KnowledgeID, err)
			continue
		}
		logger.Infof(ctx, "wiki ingest: re-queued failed lite op %s (%s) for retry", op.KnowledgeID, op.DocTitle)
	}
}

// docIngestResult captures per-document info for batch post-processing.
type docIngestResult struct {
	KnowledgeID string
	DocTitle    string
	Summary     string   // one-line summary of the document (from summary page)
	Pages       []string // affected page slugs
}

// WikiBatchContext holds shared data across Map and Reduce phases
type WikiBatchContext struct {
	AllPages                    []*types.WikiPage
	SlugTitleMap                map[string]string
	SummaryContentByKnowledgeID map[string]string
	// ExtractionGranularity drives Pass 0 (candidate slug extraction)
	// aggressiveness. Resolved once per batch from the KnowledgeBase's
	// WikiConfig so every doc in the batch sees the same scope rules.
	// Already Normalize()'d — consumers can assume it is one of the
	// three valid values.
	ExtractionGranularity types.WikiExtractionGranularity
}

// SlugUpdate represents a single update operation for a specific slug
type SlugUpdate struct {
	Slug              string
	Type              string        // "entity", "concept", "summary", "retract", "retractStale"
	Item              extractedItem // For entity/concept
	DocTitle          string
	KnowledgeID       string
	SourceRef         string
	Language          string
	SummaryBody       string // For summary
	SummaryLine       string // For summary
	RetractDocContent string // For retract / retractStale
	// SourceChunks lists the chunk IDs (within KnowledgeID) that substantively
	// support this update. Mirrors Item.SourceChunks for convenience — the
	// Reduce phase reads from here to avoid an extra field hop.
	SourceChunks []string
	// DocSummary is the document-level summary body produced by
	// WikiSummaryPrompt (everything after the SUMMARY: ... headline, falling
	// back to the raw output if no headline could be parsed out). Carried
	// here so the Reduce phase can frame cited chunks with a rich
	// <source_context> block that tells the editor model what the document
	// is about AND what kind of document it is (resume vs announcement vs
	// product page). The one-line headline alone was too terse to keep the
	// editor grounded on longer / multi-topic source documents.
	DocSummary string
}

func previewText(s string, maxRunes int) string {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\t", " ")
	for strings.Contains(s, "  ") {
		s = strings.ReplaceAll(s, "  ", " ")
	}
	r := []rune(s)
	if maxRunes <= 0 || len(r) <= maxRunes {
		return s
	}
	return string(r[:maxRunes]) + "...(truncated)"
}

func previewStringSlice(items []string, limit int) string {
	if len(items) == 0 {
		return "[]"
	}
	if limit <= 0 {
		limit = 1
	}
	n := len(items)
	if n > limit {
		items = items[:limit]
	}
	out := make([]string, 0, len(items))
	for _, it := range items {
		out = append(out, previewText(it, 48))
	}
	if n > limit {
		return fmt.Sprintf("[%s ...(+%d)]", strings.Join(out, ", "), n-limit)
	}
	return fmt.Sprintf("[%s]", strings.Join(out, ", "))
}

// cleanDeadLinks removes [[wiki-links]] that point to archived or deleted pages.
// Scans all published pages, checks each out_link, and removes references to
// pages that no longer exist or are archived. No LLM call — pure text cleanup.
func (s *wikiIngestService) cleanDeadLinks(ctx context.Context, kbID string) {
	allPages, err := s.wikiService.ListAllPages(ctx, kbID)
	if err != nil || len(allPages) == 0 {
		return
	}

	// Build set of live (non-archived, non-system) slugs
	liveSlugs := make(map[string]bool)
	for _, p := range allPages {
		if p.Status != types.WikiPageStatusArchived {
			liveSlugs[p.Slug] = true
		}
	}

	var cleaned int
	for _, p := range allPages {
		if p.Status == types.WikiPageStatusArchived {
			continue
		}
		if p.PageType == types.WikiPageTypeIndex || p.PageType == types.WikiPageTypeLog {
			continue
		}

		content := p.Content
		changed := false

		// Find all [[slug]] references and remove dead ones
		for _, outSlug := range p.OutLinks {
			if liveSlugs[outSlug] {
				continue // link is alive
			}
			// Dead link — remove the [[slug]] from content, keep the display text if any
			linkPattern := "[[" + outSlug + "]]"
			if strings.Contains(content, linkPattern) {
				// Replace [[dead-slug]] with just the slug's readable part
				parts := strings.Split(outSlug, "/")
				readableName := parts[len(parts)-1]
				readableName = strings.ReplaceAll(readableName, "-", " ")
				content = strings.ReplaceAll(content, linkPattern, readableName)
				changed = true
			}
		}

		if changed {
			p.Content = content
			if err := s.wikiService.UpdateAutoLinkedContent(ctx, p); err != nil {
				logger.Warnf(ctx, "wiki: failed to clean dead links in page %s: %v", p.Slug, err)
			} else {
				cleaned++
			}
		}
	}

	if cleaned > 0 {
		logger.Infof(ctx, "wiki: cleaned dead links in %d pages", cleaned)
	}
}

// injectCrossLinks scans affected pages and injects [[wiki-links]] for mentions
// of other wiki page titles in the content. Pure text replacement, no LLM call.
// Processes entity/concept/synthesis/comparison and summary pages (not index/log).
//
// The actual matching is delegated to linkifyContent, which handles code-block
// and existing-link exclusions plus ASCII word-boundary checks.
func (s *wikiIngestService) injectCrossLinks(ctx context.Context, kbID string, affectedSlugs []string) {
	allPages, err := s.wikiService.ListAllPages(ctx, kbID)
	if err != nil || len(allPages) < 2 {
		return
	}

	refs := collectLinkRefs(allPages)
	if len(refs) == 0 {
		return
	}

	affectedSet := make(map[string]bool, len(affectedSlugs))
	for _, slug := range affectedSlugs {
		affectedSet[slug] = true
	}

	var updated int
	for _, p := range allPages {
		if !affectedSet[p.Slug] {
			continue
		}
		if p.PageType == types.WikiPageTypeIndex || p.PageType == types.WikiPageTypeLog {
			continue
		}

		newContent, changed := linkifyContent(p.Content, refs, p.Slug)
		if !changed {
			continue
		}
		p.Content = newContent
		if err := s.wikiService.UpdateAutoLinkedContent(ctx, p); err != nil {
			logger.Warnf(ctx, "wiki ingest: cross-link injection failed for %s: %v", p.Slug, err)
			continue
		}
		updated++
	}

	if updated > 0 {
		logger.Infof(ctx, "wiki ingest: injected cross-links in %d pages", updated)
	}
}

// collectLinkRefs flattens (title + aliases) of all non-system pages into a
// single linkRef slice suitable for linkifyContent.
func collectLinkRefs(pages []*types.WikiPage) []linkRef {
	refs := make([]linkRef, 0, len(pages)*2)
	for _, p := range pages {
		if p.PageType == types.WikiPageTypeIndex || p.PageType == types.WikiPageTypeLog {
			continue
		}
		if p.Title != "" {
			refs = append(refs, linkRef{slug: p.Slug, matchText: p.Title})
		}
		for _, alias := range p.Aliases {
			if alias != "" {
				refs = append(refs, linkRef{slug: p.Slug, matchText: alias})
			}
		}
	}
	return refs
}

// getExistingPageSlugsForKnowledge returns all page slugs that currently reference
// a given knowledge ID in their source_refs. Used to snapshot state before re-ingest.
func (s *wikiIngestService) getExistingPageSlugsForKnowledge(ctx context.Context, kbID, knowledgeID string) map[string]bool {
	allPages, err := s.wikiService.ListAllPages(ctx, kbID)
	if err != nil || len(allPages) == 0 {
		return nil
	}

	slugs := make(map[string]bool)
	prefix := knowledgeID + "|"
	for _, p := range allPages {
		if p.PageType == types.WikiPageTypeIndex || p.PageType == types.WikiPageTypeLog {
			continue
		}
		for _, ref := range p.SourceRefs {
			if ref == knowledgeID || strings.HasPrefix(ref, prefix) {
				slugs[p.Slug] = true
				break
			}
		}
	}
	return slugs
}

// retractStalePages handles pages that were previously linked to this document
// but are no longer produced by the updated extraction.
// - Single-source stale pages → deleted
// - Multi-source stale pages → LLM retract to clean content synchronously

// Build set of newly affected slugs (including summary)

// Stale = was in old set but not in new set

// Remove this doc's source ref

// No other sources → delete the page

// Multi-source → remove ref, queue retract

// extractedItem represents a single extracted entity or concept.
//
// SourceChunks holds the stable chunk IDs (from the source document) that
// substantively discuss this item. Populated by the chunk-citation pass; when
// non-empty the Reduce phase uses these chunks verbatim as the item's
// evidence instead of the shorter Description/Details fields.
type extractedItem struct {
	Name         string   `json:"name"`
	Slug         string   `json:"slug"`
	Aliases      []string `json:"aliases"`
	Description  string   `json:"description"`
	Details      string   `json:"details"`
	SourceChunks []string `json:"source_chunks,omitempty"`
}

// combinedExtraction represents the parsed result of the combined entity+concept extraction
type combinedExtraction struct {
	Entities []extractedItem `json:"entities"`
	Concepts []extractedItem `json:"concepts"`
}

// rebuildIndexPage regenerates the index page.
//
// Strategy: Index = LLM-generated intro (stored in Summary field) + code-generated directory.
//   - Intro: stored in indexPage.Summary. First time: generated from document summaries.
//     Subsequent: incrementally updated with changeDescription.
//   - Directory: pure code, rebuilt every time. O(N) string concat, no LLM.
func (s *wikiIngestService) rebuildIndexPage(ctx context.Context, chatModel chat.Chat, payload WikiIngestPayload, changeDesc, lang string) error {
	indexPage, _ := s.wikiService.GetIndex(ctx, payload.KnowledgeBaseID)
	if indexPage == nil {
		return nil
	}

	// List all live pages
	allPages, err := s.wikiService.ListAllPages(ctx, payload.KnowledgeBaseID)
	if err != nil {
		return err
	}

	typeOrder := []string{
		types.WikiPageTypeSummary, types.WikiPageTypeEntity, types.WikiPageTypeConcept,
		types.WikiPageTypeSynthesis, types.WikiPageTypeComparison,
	}
	typeLabels := map[string]string{
		types.WikiPageTypeSummary: "Summary", types.WikiPageTypeEntity: "Entity",
		types.WikiPageTypeConcept: "Concept", types.WikiPageTypeSynthesis: "Synthesis",
		types.WikiPageTypeComparison: "Comparison",
	}

	grouped := make(map[string][]*types.WikiPage)
	totalPages := 0
	for _, p := range allPages {
		if p.PageType == types.WikiPageTypeIndex || p.PageType == types.WikiPageTypeLog {
			continue
		}
		if p.Status == types.WikiPageStatusArchived {
			continue
		}
		grouped[p.PageType] = append(grouped[p.PageType], p)
		totalPages++
	}

	// Build document summaries listing (only summary-type pages — they represent documents)
	var docSummaries strings.Builder
	for _, p := range grouped[types.WikiPageTypeSummary] {
		fmt.Fprintf(&docSummaries, "<document>\n<title>%s</title>\n<summary>%s</summary>\n</document>\n\n", p.Title, p.Summary)
	}
	if docSummaries.Len() == 0 {
		docSummaries.WriteString("(no documents yet)")
	}

	// Generate or update intro
	existingIntro := indexPage.Summary
	var intro string

	if existingIntro == "" || existingIntro == "Wiki index - table of contents" {
		// First time — generate intro from scratch
		generatedIntro, genErr := s.generateWithTemplate(ctx, chatModel, agent.WikiIndexIntroPrompt, map[string]string{
			"DocumentSummaries": docSummaries.String(),
			"Language":          lang,
		})
		if genErr != nil {
			intro = "# Wiki Index\n\nThis wiki contains knowledge extracted from uploaded documents.\n"
		} else {
			intro = strings.TrimSpace(generatedIntro)
		}
	} else if changeDesc != "" {
		// Incremental update — tell LLM what changed
		updatedIntro, genErr := s.generateWithTemplate(ctx, chatModel, agent.WikiIndexIntroUpdatePrompt, map[string]string{
			"ExistingIntro":     existingIntro,
			"ChangeDescription": changeDesc,
			"DocumentSummaries": docSummaries.String(),
			"Language":          lang,
		})
		if genErr != nil {
			intro = existingIntro // keep existing on error
		} else {
			intro = strings.TrimSpace(updatedIntro)
		}
	} else {
		intro = existingIntro // no change description, keep as-is
	}

	// Build directory (pure code, no LLM)
	var dir strings.Builder
	for _, pt := range typeOrder {
		pages := grouped[pt]
		if len(pages) == 0 {
			continue
		}
		fmt.Fprintf(&dir, "\n## %s (%d)\n\n", typeLabels[pt], len(pages))
		for _, p := range pages {
			summary := p.Summary
			fmt.Fprintf(&dir, "[[%s]] — %s\n", p.Slug, summary)
		}
	}
	for pt, pages := range grouped {
		inOrder := false
		for _, o := range typeOrder {
			if o == pt {
				inOrder = true
				break
			}
		}
		if inOrder || len(pages) == 0 {
			continue
		}
		fmt.Fprintf(&dir, "\n## %s (%d)\n\n", pt, len(pages))
		for _, p := range pages {
			fmt.Fprintf(&dir, "[[%s]] — %s\n", p.Slug, p.Summary)
		}
	}
	if totalPages == 0 {
		dir.WriteString("\n*No wiki pages yet. Upload documents to get started.*\n")
	}

	indexPage.Content = intro + "\n" + dir.String()
	indexPage.Summary = intro // persist intro for next incremental update
	_, err = s.wikiService.UpdatePage(ctx, indexPage)
	return err
}

// splitSummaryLine extracts the "SUMMARY: ..." line from LLM output.
// Returns (summary, content). If no SUMMARY line found, summary is empty.
func splitSummaryLine(raw string) (summary string, content string) {
	raw = strings.TrimSpace(raw)
	if strings.HasPrefix(raw, "SUMMARY:") || strings.HasPrefix(raw, "SUMMARY：") {
		idx := strings.IndexByte(raw, '\n')
		if idx < 0 {
			// Only one line
			return strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(raw, "SUMMARY:"), "SUMMARY：")), ""
		}
		summaryLine := raw[:idx]
		summaryLine = strings.TrimPrefix(summaryLine, "SUMMARY:")
		summaryLine = strings.TrimPrefix(summaryLine, "SUMMARY：")
		return strings.TrimSpace(summaryLine), strings.TrimSpace(raw[idx+1:])
	}
	return "", raw
}

// appendLogEntry appends a structured, grep-parseable entry to the log page.
// Format: ## [2026-04-07 19:50:02] action | title
// Followed by key-value metadata lines. No sub-headings — keeps `grep "^## \[" log.md` clean.
func (s *wikiIngestService) appendLogEntry(ctx context.Context, kbID string, action, knowledgeID, docTitle, summary string, pagesAffected []string) {
	logPage, _ := s.wikiService.GetLog(ctx, kbID)
	if logPage == nil {
		return
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "\n## [%s] %s | %s\n",
		time.Now().Format("2006-01-02 15:04:05"),
		action,
		docTitle,
	)
	if knowledgeID != "" {
		fmt.Fprintf(&sb, "- **KnowledgeID**: %s\n", knowledgeID)
	}
	if summary != "" {
		fmt.Fprintf(&sb, "- **Summary**: %s\n", summary)
	}
	if len(pagesAffected) > 0 {
		fmt.Fprintf(&sb, "- **Pages affected**: %d (%s)\n", len(pagesAffected), strings.Join(pagesAffected, ", "))
	}

	logPage.Content = logPage.Content + sb.String()
	if _, err := s.wikiService.UpdatePage(ctx, logPage); err != nil {
		logger.Warnf(ctx, "wiki ingest: failed to update log page: %v", err)
	}
}

// publishDraftPages transitions draft pages to published status after ingest completes.
// This ensures users don't see half-built pages during the ingest process.
func (s *wikiIngestService) publishDraftPages(ctx context.Context, kbID string, slugs []string) {
	for _, slug := range slugs {
		page, err := s.wikiService.GetPageBySlug(ctx, kbID, slug)
		if err != nil || page == nil {
			continue
		}
		if page.Status == types.WikiPageStatusDraft {
			page.Status = types.WikiPageStatusPublished
			if err := s.wikiService.UpdatePageMeta(ctx, page); err != nil {
				logger.Warnf(ctx, "wiki ingest: failed to publish page %s: %v", slug, err)
			}
		}
	}
}

// writeDedupItemXML renders a single entity/concept entry as a structured XML
// block for the deduplication prompt. Structured form (versus a single
// pipe-separated line) helps the LLM reliably tell name / aliases / type apart
// and reduces nonsensical merges like "居民身份证" → "工作居住证".
func writeDedupItemXML(buf *strings.Builder, slug, name, itemType string, aliases []string) {
	fmt.Fprintf(buf, "  <item slug=%q type=%q>\n", slug, itemType)
	fmt.Fprintf(buf, "    <name>%s</name>\n", xmlEscape(name))
	for _, alias := range aliases {
		if alias == "" {
			continue
		}
		fmt.Fprintf(buf, "    <alias>%s</alias>\n", xmlEscape(alias))
	}
	buf.WriteString("  </item>\n")
}

// xmlEscape escapes the minimal set of characters that can break XML text
// content. Slugs are ASCII-only so they don't need escaping when used as
// attribute values.
func xmlEscape(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	return s
}

// deduplicateExtractedBatch deduplicates both entities and concepts against
// existing wiki pages in a single LLM call. Uses pre-loaded allPages to avoid
// redundant DB queries. This replaces the two separate deduplicateItems calls
// that each queried ListAllPages + made a separate LLM call.
func (s *wikiIngestService) deduplicateExtractedBatch(
	ctx context.Context,
	chatModel chat.Chat,
	entities, concepts []extractedItem,
	allPages []*types.WikiPage,
) ([]extractedItem, []extractedItem) {
	if len(entities) == 0 && len(concepts) == 0 {
		return entities, concepts
	}

	if len(allPages) == 0 {
		return entities, concepts
	}

	// Pre-filter the candidate existing-pages set. Passing the full corpus
	// to the LLM on large KBs bloats the prompt and empirically lets the
	// model hallucinate merges between totally unrelated slugs. The filter
	// is conservative (high recall) and the downstream validMerge check
	// remains in place for defense in depth.
	newItems := make([]extractedItem, 0, len(entities)+len(concepts))
	newItems = append(newItems, entities...)
	newItems = append(newItems, concepts...)
	candidatePages := selectDedupCandidatePages(newItems, allPages)
	if len(candidatePages) == 0 {
		return entities, concepts
	}
	if origCount := countEntityConceptPages(allPages); origCount > len(candidatePages) {
		logger.Infof(ctx, "wiki ingest: dedup candidate filter kept %d/%d existing pages for %d new items",
			len(candidatePages), origCount, len(newItems))
	}

	var existingBuf strings.Builder
	for _, p := range candidatePages {
		writeDedupItemXML(&existingBuf, p.Slug, p.Title, string(p.PageType), []string(p.Aliases))
	}
	if existingBuf.Len() == 0 {
		return entities, concepts
	}

	var newBuf strings.Builder
	for _, item := range entities {
		writeDedupItemXML(&newBuf, item.Slug, item.Name, "entity", item.Aliases)
	}
	for _, item := range concepts {
		writeDedupItemXML(&newBuf, item.Slug, item.Name, "concept", item.Aliases)
	}

	dedupeJSON, err := s.generateWithTemplate(ctx, chatModel, agent.WikiDeduplicationPrompt, map[string]string{
		"NewItems":      newBuf.String(),
		"ExistingPages": existingBuf.String(),
	})
	if err != nil {
		logger.Warnf(ctx, "wiki ingest: deduplication LLM call failed: %v", err)
		return entities, concepts
	}

	dedupeJSON = cleanLLMJSON(dedupeJSON)

	var dedupeResult struct {
		Merges map[string]string `json:"merges"`
	}
	if err := json.Unmarshal([]byte(dedupeJSON), &dedupeResult); err != nil {
		logger.Warnf(ctx, "wiki ingest: failed to parse dedup JSON: %v\nRaw: %s", err, dedupeJSON)
		return entities, concepts
	}

	if len(dedupeResult.Merges) == 0 {
		return entities, concepts
	}

	existingSlugs := make(map[string]bool, len(allPages))
	for _, p := range allPages {
		existingSlugs[p.Slug] = true
	}

	validMerge := func(srcSlug, dstSlug string) bool {
		if !existingSlugs[dstSlug] {
			logger.Warnf(ctx, "wiki ingest: dedup rejected %s → %s (target slug does not exist)", srcSlug, dstSlug)
			return false
		}
		srcPrefix := srcSlug[:strings.Index(srcSlug, "/")+1]
		dstPrefix := dstSlug[:strings.Index(dstSlug, "/")+1]
		if srcPrefix != dstPrefix {
			logger.Warnf(ctx, "wiki ingest: dedup rejected %s → %s (type mismatch: %s vs %s)", srcSlug, dstSlug, srcPrefix, dstPrefix)
			return false
		}
		return true
	}

	for i, item := range entities {
		if existingSlug, ok := dedupeResult.Merges[item.Slug]; ok && validMerge(item.Slug, existingSlug) {
			logger.Infof(ctx, "wiki ingest: dedup merge %s → %s", item.Slug, existingSlug)
			entities[i].Slug = existingSlug
		}
	}
	for i, item := range concepts {
		if existingSlug, ok := dedupeResult.Merges[item.Slug]; ok && validMerge(item.Slug, existingSlug) {
			logger.Infof(ctx, "wiki ingest: dedup merge %s → %s", item.Slug, existingSlug)
			concepts[i].Slug = existingSlug
		}
	}

	return entities, concepts
}

// generateWithTemplate executes a prompt template and calls the LLM
func (s *wikiIngestService) generateWithTemplate(ctx context.Context, chatModel chat.Chat, promptTpl string, data map[string]string) (string, error) {
	tmpl, err := template.New("wiki").Parse(promptTpl)
	if err != nil {
		return "", fmt.Errorf("parse template: %w", err)
	}

	var buf strings.Builder
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("execute template: %w", err)
	}

	prompt := buf.String()
	thinking := false
	response, err := chatModel.Chat(ctx, []chat.Message{
		{Role: "user", Content: prompt},
	}, &chat.ChatOptions{
		Temperature: 0.3,
		Thinking:    &thinking,
	})
	if err != nil {
		return "", fmt.Errorf("LLM call failed: %w", err)
	}

	return response.Content, nil
}

// --- Helpers ---

// isKnowledgeGone returns true if the given knowledge has been deleted or is
// in the middle of being deleted. It first consults the Redis tombstone
// (written by cleanupWikiOnKnowledgeDelete) as a fast path, then falls back
// to the DB. A nil result from GetKnowledgeByIDOnly also counts as gone: the
// repo layer uses GORM First() which filters soft-deleted rows, so a
// soft-deleted knowledge surfaces as "not found" here — exactly what we want.
func (s *wikiIngestService) isKnowledgeGone(ctx context.Context, kbID, knowledgeID string) bool {
	if knowledgeID == "" {
		return true
	}
	if s.redisClient != nil {
		if exists, err := s.redisClient.Exists(ctx, WikiDeletedTombstoneKey(kbID, knowledgeID)).Result(); err == nil && exists > 0 {
			return true
		}
	}
	kn, err := s.knowledgeSvc.GetKnowledgeByIDOnly(ctx, knowledgeID)
	if err != nil || kn == nil {
		return true
	}
	return kn.ParseStatus == types.ParseStatusDeleting
}

// filterLiveUpdates drops additions/summaries whose source knowledge has been
// deleted since the Map phase finished. Retract updates are preserved so
// pages still get cleaned up. Caches per-knowledge results to avoid DB
// hammering when a single reduce slug carries many updates for the same doc.
func (s *wikiIngestService) filterLiveUpdates(ctx context.Context, kbID string, updates []SlugUpdate) []SlugUpdate {
	if len(updates) == 0 {
		return updates
	}
	goneCache := make(map[string]bool)
	isGone := func(kid string) bool {
		if kid == "" {
			return false
		}
		if v, ok := goneCache[kid]; ok {
			return v
		}
		v := s.isKnowledgeGone(ctx, kbID, kid)
		goneCache[kid] = v
		return v
	}
	filtered := make([]SlugUpdate, 0, len(updates))
	dropped := 0
	for _, u := range updates {
		switch u.Type {
		case "retract", "retractStale":
			filtered = append(filtered, u)
		default:
			if isGone(u.KnowledgeID) {
				dropped++
				continue
			}
			filtered = append(filtered, u)
		}
	}
	if dropped > 0 {
		logger.Infof(ctx, "wiki ingest: reduce dropped %d updates for deleted knowledge(s)", dropped)
	}
	return filtered
}

// reconstructContent rebuilds document text from chunks.
//
// This only concatenates text-type chunks — image OCR / caption information is
// stored on image_ocr / image_caption child chunks (see image_multimodal.go),
// not on the parent text chunk's ImageInfo field. Callers that need the full
// enriched content (with OCR / captions inlined) should call
// reconstructEnrichedContent instead so image info is fetched from child
// chunks and embedded alongside Markdown image links.
func reconstructContent(chunks []*types.Chunk) string {
	var textChunks []*types.Chunk
	for _, c := range chunks {
		if c.ChunkType == types.ChunkTypeText || c.ChunkType == "" {
			textChunks = append(textChunks, c)
		}
	}

	// Sort by StartAt, then ChunkIndex
	sort.Slice(textChunks, func(i, j int) bool {
		if textChunks[i].StartAt == textChunks[j].StartAt {
			return textChunks[i].ChunkIndex < textChunks[j].ChunkIndex
		}
		return textChunks[i].StartAt < textChunks[j].StartAt
	})

	var sb strings.Builder
	lastEndAt := -1
	for _, c := range textChunks {
		toAppend := c.Content

		if c.StartAt > lastEndAt || c.EndAt == 0 {
			// Non-overlapping or missing position info
			if sb.Len() > 0 {
				sb.WriteString("\n")
			}
			sb.WriteString(toAppend)
			if c.EndAt > 0 {
				lastEndAt = c.EndAt
			}
		} else if c.EndAt > lastEndAt {
			// Partial overlap
			contentRunes := []rune(toAppend)
			offset := len(contentRunes) - (c.EndAt - lastEndAt)
			if offset >= 0 && offset < len(contentRunes) {
				sb.WriteString(string(contentRunes[offset:]))
			} else {
				// Fallback if offset calculation is invalid
				if sb.Len() > 0 {
					sb.WriteString("\n")
				}
				sb.WriteString(toAppend)
			}
			lastEndAt = c.EndAt
		}
		// If c.EndAt <= lastEndAt, it's fully contained, so skip appending text
	}

	return sb.String()
}

// reconstructEnrichedContent rebuilds document text and inlines image_info
// (OCR text + caption) pulled from image_ocr / image_caption child chunks.
//
// Without this enrichment, image-heavy documents (e.g. a scanned PDF or a
// standalone .jpg) reach the LLM as bare Markdown image links, causing
// extraction / summarization to produce empty or "no textual content" output.
func reconstructEnrichedContent(
	ctx context.Context,
	chunkRepo interfaces.ChunkRepository,
	tenantID uint64,
	chunks []*types.Chunk,
) string {
	content := reconstructContent(chunks)

	var textChunkIDs []string
	for _, c := range chunks {
		if c.ChunkType == types.ChunkTypeText || c.ChunkType == "" {
			if c.ID != "" {
				textChunkIDs = append(textChunkIDs, c.ID)
			}
		}
	}
	if len(textChunkIDs) == 0 || chunkRepo == nil {
		return content
	}

	imageInfoMap := searchutil.CollectImageInfoByChunkIDs(ctx, chunkRepo, tenantID, textChunkIDs)
	mergedImageInfo := searchutil.MergeImageInfoJSON(imageInfoMap)
	if mergedImageInfo == "" {
		return content
	}
	return searchutil.EnrichContentWithImageInfo(content, mergedImageInfo)
}

// slugify creates a URL-friendly slug from a string
func slugify(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '/' {
			return r
		}
		if r == ' ' || r == '_' {
			return '-'
		}
		// Keep CJK characters
		if r >= 0x4E00 && r <= 0x9FFF {
			return r
		}
		return -1
	}, s)
	// Collapse multiple hyphens
	for strings.Contains(s, "--") {
		s = strings.ReplaceAll(s, "--", "-")
	}
	s = strings.Trim(s, "-")
	if len(s) > 200 {
		s = s[:200]
	}
	return s
}

// truncateString truncates a string to maxLen runes
func truncateString(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[:maxLen]) + "..."
}

// appendUnique appends a string to a StringArray if not already present
func appendUnique(arr types.StringArray, s string) types.StringArray {
	for _, v := range arr {
		if v == s {
			return arr
		}
	}
	return append(arr, s)
}

// minTextContentRunes is the minimum number of non-whitespace, non-image-reference
// runes required for content to be considered substantive enough for LLM
// summarization or wiki extraction. Documents below this threshold (e.g. a
// scanned PDF where OCR yielded nothing AND no caption either) are routed to
// a deterministic empty-content fallback instead of being passed to the LLM,
// which would otherwise hallucinate based on metadata alone.
//
// The threshold is intentionally low: legitimate short documents (brief
// memos, single-line notes) must still pass. The goal is only to catch
// the empty-image-only case.
//
// Declared as a var (not const) so tests can override it and future config
// plumbing can adjust it at runtime without a rebuild.
var minTextContentRunes = 10

var (
	// Markdown image references like ![alt](path) — pure visual placeholders
	// with no extractable text, so the whole reference is removed.
	mdImageRefRE = regexp.MustCompile(`!\[[^\]]*\]\([^)]*\)`)

	// <image_original>...</image_original> blocks wrap the verbatim Markdown
	// image reference inside an enriched <image> block (see
	// searchutil.EnrichContentWithImageInfo). The content is just a redundant
	// copy of an already-stripped image link, so the whole block (tags +
	// content) is removed.
	imageOriginalBlockRE = regexp.MustCompile(`(?is)<image_original\b[^>]*>.*?</image_original>`)

	// Self-closing or attribute-only HTML <img> tags.
	htmlImgTagRE = regexp.MustCompile(`(?i)<img\b[^>]*/?>`)

	// Wrapper-style <image>, <images>, <image_caption>, <image_ocr> tags
	// (opening or closing). Matches ONLY the tag; the text content between
	// open and close tags is preserved. This is critical: VLM-generated OCR
	// and caption text live inside <image_ocr>...</image_ocr> and
	// <image_caption>...</image_caption> blocks, and stripping the content
	// would silently destroy the very text we want to keep.
	imageWrapperTagRE = regexp.MustCompile(`(?i)</?image[a-z_]*\b[^>]*/?>`)
)

// stripImageMarkup removes image-only placeholders (Markdown image refs,
// <img> tags, <image_original> redundancy blocks) and unwraps the
// <image>/<image_caption>/<image_ocr> XML wrappers produced by the search
// enrichment layer, leaving any OCR or caption text as plain inline text.
//
// This shape matters: when VLM OCR succeeds on a scanned PDF page, the
// extracted text reaches downstream code wrapped in <image_ocr> tags inside
// an <image> block. A naive "strip the whole <image>...</image> block"
// approach would discard the OCR text — the exact opposite of what we want.
func stripImageMarkup(s string) string {
	s = imageOriginalBlockRE.ReplaceAllString(s, "")
	s = mdImageRefRE.ReplaceAllString(s, "")
	s = htmlImgTagRE.ReplaceAllString(s, "")
	s = imageWrapperTagRE.ReplaceAllString(s, "")
	return s
}

// extractRealText returns the trimmed content with image markup stripped.
// Cached at the call site for use both in the threshold check and in any
// subsequent log message, avoiding redundant regex passes over large docs.
func extractRealText(content string) string {
	return strings.TrimSpace(stripImageMarkup(content))
}

// hasSufficientTextContent reports whether the given content carries enough
// real text (after image markup is stripped, with OCR/caption text retained)
// to warrant an LLM call. It is the primary defence against filename-driven
// hallucinations on scanned PDFs that have NO usable text at all.
func hasSufficientTextContent(content string) bool {
	return realTextRuneCount(content) >= minTextContentRunes
}

// realTextRuneCount returns the rune length of the content after image
// markup is stripped. Uses utf8.RuneCountInString to avoid allocating a
// rune slice for the count.
func realTextRuneCount(content string) int {
	return utf8.RuneCountInString(extractRealText(content))
}

// cleanLLMJSON strips markdown code-fence wrappers and sanitizes control characters
// from LLM-generated JSON output so it can be safely unmarshalled.
func cleanLLMJSON(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "```json")
	s = strings.TrimPrefix(s, "```")
	s = strings.TrimSuffix(s, "```")
	s = strings.TrimSpace(s)
	return sanitizeJSONString(s)
}

// sanitizeJSONString sanitizes a string that is intended to be parsed as JSON,
// by properly escaping unescaped control characters (like newlines) inside string literals.
func sanitizeJSONString(s string) string {
	var buf strings.Builder
	buf.Grow(len(s))
	inString := false
	escape := false
	for _, r := range s {
		if escape {
			if r == '\n' {
				buf.WriteString(`n`)
			} else if r == '\r' {
				buf.WriteString(`r`)
			} else if r == '\t' {
				buf.WriteString(`t`)
			} else {
				buf.WriteRune(r)
			}
			escape = false
			continue
		}
		if r == '\\' {
			escape = true
			buf.WriteRune(r)
			continue
		}
		if r == '"' {
			inString = !inString
			buf.WriteRune(r)
			continue
		}
		if inString {
			if r == '\n' {
				buf.WriteString(`\n`)
				continue
			}
			if r == '\r' {
				buf.WriteString(`\r`)
				continue
			}
			if r == '\t' {
				buf.WriteString(`\t`)
				continue
			}
		}
		buf.WriteRune(r)
	}
	return buf.String()
}
