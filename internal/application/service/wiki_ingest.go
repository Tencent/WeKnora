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

	// wikiActiveKeyPrefix is the Redis key for the "batch in progress" flag.
	// Key format: wiki:active:{kbID} → "1" with TTL. Prevents concurrent batches.
	wikiActiveKeyPrefix = "wiki:active:"

	// wikiIngestDelay is how long to wait after a document is added before
	// the batch task fires. Debounces rapid uploads.
	wikiIngestDelay = 30 * time.Second

	// wikiMaxDocsPerBatch limits how many documents a single batch processes.
	// Prevents unbounded execution time. Remaining ops stay in
	// task_pending_ops and are picked up by the follow-up task.
	wikiMaxDocsPerBatch = 5

	// wikiMaxFailRetries is the maximum number of times a single document op
	// may be re-attempted via requeueFailedOps before it is permanently
	// archived to task_dead_letters. 5 retries ≈ five full batch cycles
	// (each with a ~30 s delay), giving transient LLM errors a fair chance
	// to recover without letting a persistently-broken doc clog the queue
	// indefinitely.
	wikiMaxFailRetries = 5

	// wikiIngestMaxRetry controls asynq retry budget for wiki:ingest tasks.
	// Keep this moderate: lock conflicts already retry every 15s via
	// asynqRetryDelayFunc, and follow-up/retract paths fire quickly.
	wikiIngestMaxRetry = 10

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

	// wikiLLMMaxAttempts is the total attempt count (initial + retries) for
	// every LLM call routed through generateWithTemplate. 3 was chosen to
	// absorb transient 504/timeouts from upstream gateways without
	// materially prolonging task runtime when the remote is genuinely down.
	wikiLLMMaxAttempts = 3

	// wikiLLMBackoffBase is the base delay for the exponential backoff
	// between retry attempts. The nth retry waits base << (n-1) — so with
	// a 2s base we wait 2s, 4s, 8s between attempts.
	wikiLLMBackoffBase = 2 * time.Second

	// wikiTaskType is the task_type stamp used in task_pending_ops and
	// task_dead_letters rows for this pipeline. Stable across the lifetime
	// of any pending op so the follow-up consumer can pull it back.
	wikiTaskType = "wiki:ingest"

	// wikiTaskScope is the scope used by both pending ops and dead letters.
	// Wiki ingest is per-KB, so every op is scoped to a knowledge_base.
	wikiTaskScope = types.TaskScopeKnowledgeBase
)

// WikiDeletedTombstoneKey returns the Redis key used to mark a knowledge as
// recently deleted, so wiki_ingest tasks in flight can short-circuit. Exposed
// so knowledgeService.cleanupWikiOnKnowledgeDelete can write the same key
// without duplicating the format string.
func WikiDeletedTombstoneKey(kbID, knowledgeID string) string {
	return wikiDeletedKeyPrefix + kbID + ":" + knowledgeID
}

// WikiIngestPayload is the asynq task payload for wiki ingest batch trigger.
// The actual document IDs are stored in the task_pending_ops table; this
// payload only carries the trigger metadata so the worker can resolve
// the queue tuple (task_type, scope, scope_id) and process whatever rows
// are queued under it.
type WikiIngestPayload struct {
	types.TracingContext
	TenantID        uint64 `json:"tenant_id"`
	KnowledgeBaseID string `json:"knowledge_base_id"`
	Language        string `json:"language,omitempty"`
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

// WikiPendingOp represents a single operation queued in task_pending_ops
// under task_type="wiki:ingest". The struct is the JSON payload of the
// task_pending_ops row; the surrounding (task_type, scope, scope_id,
// dedup_key) fields live as separate columns and are not serialized
// here.
//
// dbID is the auto-increment primary key of the task_pending_ops row
// the op was loaded from. PeekBatch fills it; consumers carry it
// through Map/Reduce so DeleteByIDs (after consume) and IncrFailCount
// (after failure) can address the right row. It is intentionally
// unexported and excluded from JSON so the persisted payload does not
// duplicate the column.
type WikiPendingOp struct {
	Op          string `json:"op"`
	KnowledgeID string `json:"knowledge_id"`
	// Ingest fields
	Language string `json:"language,omitempty"`
	// Retract fields
	DocTitle   string   `json:"doc_title,omitempty"`
	DocSummary string   `json:"doc_summary,omitempty"`
	PageSlugs  []string `json:"page_slugs,omitempty"`

	// dbID is set by peekPendingList from task_pending_ops.id. Zero in
	// constructions made outside the queue (e.g. legacy tests).
	dbID int64 `json:"-"`
}

// wikiIngestService handles the LLM-powered wiki generation pipeline.
//
// Durable state lives in two places:
//   - task_pending_ops (rows tagged task_type="wiki:ingest", scope=
//     "knowledge_base"): the per-document op queue. Replaces the
//     legacy Redis wiki:pending:<kbID> list, which was vulnerable to
//     24h TTL eviction at 4w-document scale.
//   - task_dead_letters: in-batch failures that exhausted
//     wikiMaxFailRetries land here. The asynq dead-letter middleware
//     also writes asynq-level archived rows here uniformly across
//     every task type.
//
// Redis is still used for the per-KB active-batch lock
// (wiki:active:<kbID>) and the delete tombstone (wiki:deleted:<...>),
// both of which are correctness-critical short-lived flags rather
// than data the system should survive without.
type wikiIngestService struct {
	wikiService    interfaces.WikiPageService
	kbService      interfaces.KnowledgeBaseService
	knowledgeSvc   interfaces.KnowledgeService
	chunkRepo      interfaces.ChunkRepository
	modelService   interfaces.ModelService
	task           interfaces.TaskEnqueuer
	logEntrySvc    interfaces.WikiLogEntryService
	pendingRepo    interfaces.TaskPendingOpsRepository
	deadLetterRepo interfaces.TaskDeadLetterRepository
	redisClient    *redis.Client // nil in Lite mode (no Redis)
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
	logEntrySvc interfaces.WikiLogEntryService,
	pendingRepo interfaces.TaskPendingOpsRepository,
	deadLetterRepo interfaces.TaskDeadLetterRepository,
	redisClient *redis.Client,
) interfaces.TaskHandler {
	svc := &wikiIngestService{
		wikiService:    wikiService,
		kbService:      kbService,
		knowledgeSvc:   knowledgeSvc,
		chunkRepo:      chunkRepo,
		modelService:   modelService,
		task:           task,
		logEntrySvc:    logEntrySvc,
		pendingRepo:    pendingRepo,
		deadLetterRepo: deadLetterRepo,
		redisClient:    redisClient,
	}
	return svc
}

// EnqueueWikiIngest queues a document for wiki ingestion.
//
// Architecture: each upload inserts one row into task_pending_ops
// (task_type="wiki:ingest", scope="knowledge_base", scope_id=kbID,
// dedup_key=knowledgeID), then schedules a debounced asynq trigger task.
// When the trigger fires, the worker peeks a batch from
// task_pending_ops, processes it, deletes consumed rows, and (if more
// remain) schedules a follow-up. Multiple debounced triggers within the
// 30s window all coalesce: the first one to acquire the per-KB active
// lock drains the batch; subsequent ones see an empty queue and exit.
//
// Lite mode (no Redis) still works as long as Postgres is reachable —
// the queue lives in PG, only the active-batch lock is Redis-only and
// has a process-local fallback (liteLocks) inside the worker.
func EnqueueWikiIngest(
	ctx context.Context,
	task interfaces.TaskEnqueuer,
	pendingRepo interfaces.TaskPendingOpsRepository,
	tenantID uint64,
	kbID, knowledgeID string,
) {
	lang, _ := types.LanguageFromContext(ctx)

	// Persist the pending op. A re-ingest of the same knowledge id while
	// a previous op is still queued simply appends another row; the
	// peekPendingList consumer collapses by dedup_key (== knowledge_id),
	// keeping the LATEST op for each knowledge — matching the legacy
	// "RPush + reverse-dedupe" semantics.
	op := WikiPendingOp{
		Op:          WikiOpIngest,
		KnowledgeID: knowledgeID,
		Language:    lang,
	}
	payloadBytes, err := json.Marshal(op)
	if err != nil {
		logger.Warnf(ctx, "wiki ingest: failed to marshal pending op for %s: %v", knowledgeID, err)
		return
	}
	if pendingRepo != nil {
		if err := pendingRepo.Enqueue(ctx, &types.TaskPendingOp{
			TenantID: tenantID,
			TaskType: wikiTaskType,
			Scope:    wikiTaskScope,
			ScopeID:  kbID,
			Op:       WikiOpIngest,
			DedupKey: knowledgeID,
			Payload:  payloadBytes,
		}); err != nil {
			logger.Warnf(ctx, "wiki ingest: failed to enqueue pending op for %s: %v", knowledgeID, err)
			// Fall through and still schedule the trigger task — the
			// next upload (or the next retry pass) will catch the gap.
		}
	}

	trigger := WikiIngestPayload{
		TenantID:        tenantID,
		KnowledgeBaseID: kbID,
		Language:        lang,
	}
	langfuse.InjectTracing(ctx, &trigger)
	triggerBytes, _ := json.Marshal(trigger)

	t := asynq.NewTask(types.TypeWikiIngest, triggerBytes,
		asynq.Queue("low"),
		asynq.MaxRetry(wikiIngestMaxRetry),
		asynq.Timeout(60*time.Minute),
		asynq.ProcessIn(wikiIngestDelay),
	)
	if _, err := task.Enqueue(t); err != nil {
		logger.Warnf(ctx, "wiki ingest: failed to enqueue trigger task: %v", err)
	}
}

// EnqueueWikiRetract queues a wiki retraction op (a delete cleanup).
// Identical persistence model as EnqueueWikiIngest — the op rides in
// task_pending_ops and an asynq trigger fires shortly after to
// process the batch. Retracts use a slightly shorter ProcessIn delay
// because there is no "user upload arriving in waves" pattern to
// debounce against — a deletion fires once and we want the cleanup
// to land promptly.
func EnqueueWikiRetract(
	ctx context.Context,
	task interfaces.TaskEnqueuer,
	pendingRepo interfaces.TaskPendingOpsRepository,
	payload WikiRetractPayload,
) {
	op := WikiPendingOp{
		Op:          WikiOpRetract,
		KnowledgeID: payload.KnowledgeID,
		DocTitle:    payload.DocTitle,
		DocSummary:  payload.DocSummary,
		PageSlugs:   payload.PageSlugs,
		Language:    payload.Language,
	}
	payloadBytes, err := json.Marshal(op)
	if err != nil {
		logger.Warnf(ctx, "wiki retract: failed to marshal pending op: %v", err)
		return
	}
	if pendingRepo != nil {
		if err := pendingRepo.Enqueue(ctx, &types.TaskPendingOp{
			TenantID: payload.TenantID,
			TaskType: wikiTaskType,
			Scope:    wikiTaskScope,
			ScopeID:  payload.KnowledgeBaseID,
			Op:       WikiOpRetract,
			DedupKey: payload.KnowledgeID,
			Payload:  payloadBytes,
		}); err != nil {
			logger.Warnf(ctx, "wiki retract: failed to enqueue pending op: %v", err)
		}
	}

	trigger := WikiIngestPayload{
		TenantID:        payload.TenantID,
		KnowledgeBaseID: payload.KnowledgeBaseID,
		Language:        payload.Language,
	}
	langfuse.InjectTracing(ctx, &trigger)
	triggerBytes, _ := json.Marshal(trigger)
	t := asynq.NewTask(types.TypeWikiIngest, triggerBytes,
		asynq.Queue("low"),
		asynq.MaxRetry(wikiIngestMaxRetry),
		asynq.Timeout(60*time.Minute),
		asynq.ProcessIn(5*time.Second), // Retract can trigger the batch quickly
	)
	if _, err := task.Enqueue(t); err != nil {
		logger.Warnf(ctx, "wiki retract: failed to enqueue trigger task: %v", err)
	}
}

// Handle implements interfaces.TaskHandler for asynq task processing.
// Wiki ingest tasks are debounced via asynq.Unique + ProcessIn, so at most
// one ingest task runs per KB at a time. No distributed lock needed.
func (s *wikiIngestService) Handle(ctx context.Context, t *asynq.Task) error {
	return s.ProcessWikiIngest(ctx, t)
}

// peekPendingList loads up to `limit` ops from task_pending_ops for
// this KB, ordered FIFO. Rows are NOT removed; callers must
// DeleteByIDs once they have been consumed (or IncrFailCount + leave
// them in place for the next pass).
//
// peekedIDs returns the DB ids of every row included in the peek
// (NOT just the ones that survived dedup) so trimPendingList can
// delete them all in one statement at the end of the batch — this
// matches the legacy "LTrim peekedCount entries" semantics, where
// duplicates collapsed by the consumer were also drained from the
// list once their canonical sibling had been processed.
func (s *wikiIngestService) peekPendingList(ctx context.Context, kbID string, limit int) (ops []WikiPendingOp, peekedIDs []int64) {
	if s.pendingRepo == nil {
		return nil, nil
	}
	if limit <= 0 {
		limit = wikiMaxDocsPerBatch
	}
	rows, err := s.pendingRepo.PeekBatch(ctx, wikiTaskType, wikiTaskScope, kbID, limit)
	if err != nil {
		logger.Warnf(ctx, "wiki ingest: failed to peek pending list: %v", err)
		return nil, nil
	}
	if len(rows) == 0 {
		return nil, nil
	}

	all := make([]WikiPendingOp, 0, len(rows))
	peekedIDs = make([]int64, 0, len(rows))
	for _, r := range rows {
		peekedIDs = append(peekedIDs, r.ID)
		var op WikiPendingOp
		if len(r.Payload) > 0 {
			if err := json.Unmarshal(r.Payload, &op); err != nil {
				logger.Warnf(ctx, "wiki ingest: failed to unmarshal pending op id=%d: %v", r.ID, err)
				continue
			}
		} else {
			// Defensive: if payload was lost, fall back to column data
			// so the row is still drainable (otherwise it would loop
			// on every batch as un-deletable).
			op = WikiPendingOp{
				Op:          r.Op,
				KnowledgeID: r.DedupKey,
			}
		}
		op.dbID = r.ID
		all = append(all, op)
	}

	// Deduplicate by KnowledgeID, keeping only the *last* operation for
	// each document. Optimizes out redundant sequences (e.g., upload
	// then immediate delete: [ingest, retract] → [retract]). The
	// non-canonical rows still get drained at trim time — their dbIDs
	// are in peekedIDs.
	seen := make(map[string]bool)
	reversedUnique := make([]WikiPendingOp, 0, len(all))
	for i := len(all) - 1; i >= 0; i-- {
		op := all[i]
		if op.KnowledgeID == "" {
			// No dedup key — keep verbatim (rare; edge case for
			// future ops without a knowledge anchor).
			reversedUnique = append(reversedUnique, op)
			continue
		}
		if seen[op.KnowledgeID] {
			continue
		}
		seen[op.KnowledgeID] = true
		reversedUnique = append(reversedUnique, op)
	}

	ops = make([]WikiPendingOp, 0, len(reversedUnique))
	for i := len(reversedUnique) - 1; i >= 0; i-- {
		ops = append(ops, reversedUnique[i])
	}
	return ops, peekedIDs
}

// trimPendingList deletes consumed rows from task_pending_ops. Empty
// input is a no-op so callers can invoke unconditionally at the end
// of a batch.
func (s *wikiIngestService) trimPendingList(ctx context.Context, ids []int64) {
	if s.pendingRepo == nil || len(ids) == 0 {
		return
	}
	if err := s.pendingRepo.DeleteByIDs(ctx, ids); err != nil {
		logger.Warnf(ctx, "wiki ingest: failed to trim %d pending rows: %v", len(ids), err)
	}
}

// requeueFailedOps records in-batch failures.
//
// For each failed op:
//
//   - IncrFailCount on the source row. The repo returns the new total,
//     so a single round trip handles both bookkeeping and retry-budget
//     check.
//   - If the count is <= wikiMaxFailRetries: leave the row in place.
//     The next follow-up batch's PeekBatch will pick it up naturally
//     (rows are ordered by id ASC and we never moved/touched it).
//   - If the count exceeds the retry cap: archive the op into
//     task_dead_letters and DeleteByIDs to remove it from the queue.
//     Both writes are best-effort — a DB failure here is logged and
//     swallowed so a single transient blip doesn't recursively spawn
//     more failures.
func (s *wikiIngestService) requeueFailedOps(ctx context.Context, payload WikiIngestPayload, ops []WikiPendingOp) {
	if s.pendingRepo == nil || len(ops) == 0 {
		return
	}
	for _, op := range ops {
		if op.dbID == 0 {
			// Op was never persisted (synthetic / test) — nothing to
			// retry against.
			continue
		}
		count, err := s.pendingRepo.IncrFailCount(ctx, op.dbID)
		if err != nil {
			logger.Warnf(ctx, "wiki ingest: failed to increment fail count for %s (id=%d): %v", op.KnowledgeID, op.dbID, err)
			// Without a fresh count we can't tell whether to drop. Be
			// conservative: leave the row in place; the next PeekBatch
			// will see it again and we'll try once more.
			continue
		}
		if count <= wikiMaxFailRetries {
			logger.Infof(ctx, "wiki ingest: re-queued failed op %s (%s) for retry (attempt %d/%d)", op.KnowledgeID, op.DocTitle, count, wikiMaxFailRetries)
			continue
		}

		// Exhausted in-batch retries — archive and remove.
		logger.Warnf(ctx, "wiki ingest: dropping op %s (%s) after %d failures (limit %d)", op.KnowledgeID, op.DocTitle, count, wikiMaxFailRetries)
		if s.deadLetterRepo != nil {
			payloadBytes, _ := json.Marshal(op)
			if dlErr := s.deadLetterRepo.Insert(ctx, &types.TaskDeadLetter{
				TenantID:  payload.TenantID,
				TaskType:  wikiTaskType,
				Scope:     wikiTaskScope,
				ScopeID:   payload.KnowledgeBaseID,
				RelatedID: op.KnowledgeID,
				Payload:   payloadBytes,
				LastError: fmt.Sprintf("exceeded wikiMaxFailRetries=%d (in-batch retries)", wikiMaxFailRetries),
				FailCount: count,
			}); dlErr != nil {
				logger.Warnf(ctx, "wiki ingest: failed to archive op %s to dead letters: %v", op.KnowledgeID, dlErr)
			}
		}
		if err := s.pendingRepo.DeleteByIDs(ctx, []int64{op.dbID}); err != nil {
			logger.Warnf(ctx, "wiki ingest: failed to drop dead-lettered row id=%d: %v", op.dbID, err)
		}
	}
}

// docIngestResult captures per-document info for batch post-processing.
type docIngestResult struct {
	KnowledgeID string
	DocTitle    string
	Summary     string // one-line summary of the document (from summary page)
	// Pages records the wiki pages this document touched, carrying both
	// the slug (for navigation / retract lookups) and the human-readable
	// title captured at ingest time (for the log feed's display layer).
	Pages []types.WikiLogPageRef
}

// WikiBatchContext holds shared data across Map and Reduce phases.
//
// Historically this carried a fully materialized `AllPages` slice plus
// pre-built SlugTitleMap / SummaryContentByKnowledgeID lookup tables.
// At 4w-document scale that meant the very first thing every batch
// did was load 100K+ wiki_pages rows (content TEXT included) into Go
// memory — and then walk them several more times for cleanDeadLinks /
// injectCrossLinks / getExistingPageSlugsForKnowledge.
//
// We now lazy-load via fetchers backed by lightweight projections
// (ListBySlugs / ListSummariesByKnowledgeIDs). Each fetcher caches
// results keyed by its input so repeat lookups within a batch are
// free; the cache is per-batch and goroutine-local-via-mutex (sync.Map
// would also work but mutex keeps the surface small).
type WikiBatchContext struct {
	// SlugTitle resolves a slug to its current title (or "" if missing).
	// Backed by ListBySlugs; cache is populated as callers ask, so we
	// only pay for the slugs we actually look at.
	SlugTitle func(ctx context.Context, slug string) string

	// SlugTitleMany batches a slug-set into a single ListBySlugs query
	// and returns the resolved titles map. Convenient when a caller
	// already has the full slug list; results are still cached.
	SlugTitleMany func(ctx context.Context, slugs []string) map[string]string

	// SummaryContentByKnowledgeID returns the surviving summary page's
	// content for the given knowledge id (or "" if no summary page
	// exists / was archived). Backed by ListSummariesByKnowledgeIDs;
	// cache is populated lazily as well.
	SummaryContentByKnowledgeID func(ctx context.Context, kid string) string

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

// wikiLinkRE matches `[[slug]]` and `[[slug|display text]]` references
// inside wiki page content. The slug capture group rejects whitespace and
// the closing-bracket / pipe characters so we don't accidentally swallow
// adjacent text. Display text (group 2) is optional.
var wikiLinkRE = regexp.MustCompile(`\[\[([^\[\]\|\s]+)(?:\|([^\]]+))?\]\]`)

// sanitizeDeadSummaryLinks rewrites the summary pages produced by THIS
// batch to fix `[[slug]]` / `[[slug|display]]` references that point
// at slugs whose entity/concept page generation failed in reduce.
//
// Background: WikiSummaryPrompt instructs the LLM to embed wiki links
// for every extracted slug it knows about, but slug extraction happens
// during map (parallel with summary generation) and the actual page
// creation happens later in reduce. When reduce's WikiPageModifyPrompt
// fails on an entity/concept slug the page never gets written — and
// the already-persisted summary is left holding a `[[entity/foo|name]]`
// link that 404s.
//
// We pass the batch's affected-slug set + the SlugTitleMany fetcher
// to the resolver so that LLM-mangled slugs (e.g. extra pinyin hyphens
// in "shang-hai-tower" vs "shanghai-tower") are healed in place rather
// than stripped to plain text — preserving cross-link information
// whenever the display text or surface form unambiguously identifies a
// live page.
//
// Pure text replacement, no LLM call. Scoped to the doc-summary slugs
// in this batch (`summary/<slugify(knowledgeID)>`), keeping the work
// proportional to batch size.
func (s *wikiIngestService) sanitizeDeadSummaryLinks(
	ctx context.Context,
	kbID string,
	docResults []*docIngestResult,
	failedSlugs map[string]struct{},
	batchCtx *WikiBatchContext,
) {
	if len(failedSlugs) == 0 || len(docResults) == 0 {
		return
	}
	// Build a (live-slug-set, title->slug) pair the resolver can consult.
	// We seed liveSlugs from batchCtx (the slugs that DID make it into
	// pages this batch) and expand it lazily as needed via SlugTitleMany.
	// titleToSlug is filled with the same successful pages' titles so the
	// display-text reverse lookup works on first try.
	for _, r := range docResults {
		if r == nil || r.KnowledgeID == "" {
			continue
		}
		summarySlug := "summary/" + slugify(r.KnowledgeID)
		page, err := s.wikiService.GetPageBySlug(ctx, kbID, summarySlug)
		if err != nil || page == nil {
			continue
		}

		// Collect the slugs this summary actually links to (so the
		// resolver has a non-empty pool of candidates), plus all the
		// successfully-written sibling pages from the same doc. These
		// two sets together cover the LLM-vs-actual mismatch cases
		// without paying for a full ListAll scan.
		candidateSlugs := make(map[string]struct{}, len(page.OutLinks)+len(r.Pages))
		for _, slug := range page.OutLinks {
			candidateSlugs[slug] = struct{}{}
		}
		for _, ref := range r.Pages {
			if _, bad := failedSlugs[ref.Slug]; bad {
				continue
			}
			candidateSlugs[ref.Slug] = struct{}{}
		}
		liveSlugs, titleToSlug := s.resolveLiveSlugs(ctx, batchCtx, candidateSlugs)

		newContent, changed := stripDeadWikiLinks(page.Content, failedSlugs, liveSlugs, titleToSlug)
		if !changed {
			continue
		}
		page.Content = newContent
		if err := s.wikiService.UpdateAutoLinkedContent(ctx, page); err != nil {
			logger.Warnf(ctx, "wiki ingest: failed to sanitize dead links in summary %s: %v", summarySlug, err)
			continue
		}
		logger.Infof(ctx, "wiki ingest: sanitized dead [[slug]] refs in summary %s", summarySlug)
	}
}

// resolveLiveSlugs builds the (liveSlugs, titleToSlug) pair that
// stripDeadWikiLinks / cleanDeadLinks pass into resolveDeadSlug.
//
// We start from a caller-supplied candidate set (typically the page's
// own out-links + this batch's freshly-written slugs) and ask the
// batch's SlugTitleMany fetcher to resolve them in one batched query.
// The fetcher already filters out archived / system pages, so missing
// entries naturally translate to "not live" without an extra check.
//
// titleToSlug is keyed by the page's exact title only — we don't have
// aliases in the lite projection. That's an acceptable trade-off: the
// reported breakage pattern is "slug munged, display = title", not
// "slug munged, display = alias", so display-by-title carries the
// majority of the rescue value at a fraction of the storage cost.
func (s *wikiIngestService) resolveLiveSlugs(
	ctx context.Context,
	batchCtx *WikiBatchContext,
	candidates map[string]struct{},
) (map[string]struct{}, map[string]string) {
	if len(candidates) == 0 || batchCtx == nil || batchCtx.SlugTitleMany == nil {
		return nil, nil
	}
	slugList := make([]string, 0, len(candidates))
	for s := range candidates {
		slugList = append(slugList, s)
	}
	titles := batchCtx.SlugTitleMany(ctx, slugList)
	live := make(map[string]struct{}, len(titles))
	titleToSlug := make(map[string]string, len(titles))
	for slug, title := range titles {
		live[slug] = struct{}{}
		if title != "" {
			titleToSlug[title] = slug
		}
	}
	return live, titleToSlug
}

// stripDeadWikiLinks rewrites `[[slug]]` / `[[slug|display]]` references
// whose `slug` falls into the dead set. The handling depends on whether
// the dead slug can be repaired:
//
//   - If the resolver maps the dead slug to a live one (typically via
//     display-text reverse lookup or hyphen-normalized equality —
//     see resolveDeadSlug), the link is REWRITTEN with the corrected
//     slug. Display text is preserved.
//   - If no live candidate is close enough, the link is STRIPPED to
//     plain text (display text when present; otherwise a humanized
//     last-segment of the slug). This is the original behaviour.
//
// The resolver is optional: when liveSlugs / titleToSlug are nil or
// empty, every dead slug falls through to the strip path. This keeps
// backward compatibility for tests / call sites that don't yet wire
// the resolution data.
func stripDeadWikiLinks(
	content string,
	deadSlugs map[string]struct{},
	liveSlugs map[string]struct{},
	titleToSlug map[string]string,
) (string, bool) {
	if len(deadSlugs) == 0 || content == "" {
		return content, false
	}
	changed := false
	out := wikiLinkRE.ReplaceAllStringFunc(content, func(match string) string {
		sub := wikiLinkRE.FindStringSubmatch(match)
		if len(sub) < 2 {
			return match
		}
		slug := sub[1]
		if _, dead := deadSlugs[slug]; !dead {
			return match
		}
		display := ""
		if len(sub) >= 3 {
			display = strings.TrimSpace(sub[2])
		}

		// (1) Try fuzzy resolve before falling back to strip. The
		// resolver consults display-text reverse lookup, hyphen-
		// normalized equality, and bigram similarity in that order;
		// returns "" only when no candidate is safe.
		if resolved, ok := resolveDeadSlug(slug, display, liveSlugs, titleToSlug); ok && resolved != slug {
			changed = true
			if display != "" {
				return "[[" + resolved + "|" + display + "]]"
			}
			return "[[" + resolved + "]]"
		}

		// (2) Strip — best-effort plain text. Prefer the LLM-supplied
		// display text; otherwise humanize the slug's last path segment
		// so the prose stays readable.
		changed = true
		if display != "" {
			return display
		}
		parts := strings.Split(slug, "/")
		label := parts[len(parts)-1]
		label = strings.ReplaceAll(label, "-", " ")
		return label
	})
	return out, changed
}

// cleanDeadLinks rewrites `[[slug]]` references in the batch's affected
// pages whose targets no longer exist (or were archived). Pure text
// cleanup — no LLM call.
//
// Scope is intentionally limited to the slugs touched by this batch:
// at 4w-document scale the legacy "scan every page in the KB" path was
// the dominant tail in the post-batch phase, and the long-tail
// historical dead links are better handled by the lint AutoFix pipeline
// (which runs out-of-band and can afford a full table walk).
//
// For each affected page:
//
//  1. Pull its lite projection (out_links + status) via the batch's
//     SlugTitle fetcher (one IN query for the whole affected set,
//     amortized via the batchCtx cache).
//  2. Probe the union of out-link targets through ExistsSlugs to
//     classify them as live vs dead.
//  3. For each dead link, try resolveDeadSlug first; rewrite if a
//     safe candidate exists, otherwise strip to plain text.
//  4. Persist the rewritten content via UpdateAutoLinkedContent so
//     the version counter stays unchanged (this is a maintenance
//     pass, not a user-visible edit).
func (s *wikiIngestService) cleanDeadLinks(ctx context.Context, kbID string, affectedSlugs []string, batchCtx *WikiBatchContext) {
	if len(affectedSlugs) == 0 {
		return
	}

	// (1) Load the affected pages' content + out-links in one go.
	// We need the full WikiPage rows here (not just lite projections)
	// because we're going to rewrite content; the lite path saves
	// nothing once we're touching content anyway.
	cleaned := 0
	for _, slug := range affectedSlugs {
		page, err := s.wikiService.GetPageBySlug(ctx, kbID, slug)
		if err != nil || page == nil {
			continue
		}
		if page.Status == types.WikiPageStatusArchived {
			continue
		}
		if page.PageType == types.WikiPageTypeIndex || page.PageType == types.WikiPageTypeLog {
			continue
		}
		if len(page.OutLinks) == 0 {
			continue
		}

		// (2) Classify out-links as live vs dead via one batched
		// ExistsSlugs query. Empty slug list → no-op.
		liveMap, err := s.wikiService.ExistsSlugs(ctx, kbID, []string(page.OutLinks))
		if err != nil {
			logger.Warnf(ctx, "wiki: ExistsSlugs failed during dead-link cleanup for %s: %v", slug, err)
			continue
		}
		deadSlugs := make(map[string]struct{})
		liveSlugs := make(map[string]struct{}, len(liveMap))
		for outSlug, alive := range liveMap {
			if alive {
				liveSlugs[outSlug] = struct{}{}
			} else {
				deadSlugs[outSlug] = struct{}{}
			}
		}
		if len(deadSlugs) == 0 {
			continue
		}

		// (3) Build the title->slug reverse-lookup map for fuzzy
		// resolve. We pull titles for the live slugs only — those
		// are the candidates a dead reference could be remapped to.
		titles := batchCtx.SlugTitleMany(ctx, []string(page.OutLinks))
		titleToSlug := make(map[string]string, len(titles))
		for s, t := range titles {
			if t != "" {
				titleToSlug[t] = s
			}
		}

		newContent, changed := stripDeadWikiLinks(page.Content, deadSlugs, liveSlugs, titleToSlug)
		if !changed {
			continue
		}

		// (4) Persist. UpdateAutoLinkedContent skips the version bump
		// because dead-link cleanup is a machine-only edit.
		page.Content = newContent
		if err := s.wikiService.UpdateAutoLinkedContent(ctx, page); err != nil {
			logger.Warnf(ctx, "wiki: failed to clean dead links in page %s: %v", page.Slug, err)
			continue
		}
		cleaned++
	}

	if cleaned > 0 {
		logger.Infof(ctx, "wiki: cleaned dead links in %d pages", cleaned)
	}
}

// injectCrossLinks scans the batch's affected pages and injects
// `[[wiki-links]]` for mentions of other wiki page titles / aliases
// in the content. Pure text replacement, no LLM call.
//
// Scope is intentionally limited to two slug sets:
//
//  1. The affected pages themselves — we only rewrite their content.
//  2. The candidate refs come from (a) the affected pages' existing
//     out-links (already known to be relevant via prior linkification
//     or manual edits) plus (b) the batch's freshly-written sibling
//     slugs supplied via `linkRefs` from the caller.
//
// At 4w-document scale this is the difference between loading 100K+
// pages just to find link candidates vs O(batch-size) lookups. We
// trade off some long-tail recall (a brand new entity in this batch
// won't be linkified into pages from previous batches until they get
// re-edited), but lint AutoFix is the right place for that.
//
// linkifyContent does the actual matching work, including code-block /
// existing-link / word-boundary exclusions.
func (s *wikiIngestService) injectCrossLinks(
	ctx context.Context,
	kbID string,
	affectedSlugs []string,
	freshRefs []linkRef,
	batchCtx *WikiBatchContext,
) {
	if len(affectedSlugs) == 0 {
		return
	}

	updated := 0
	for _, slug := range affectedSlugs {
		page, err := s.wikiService.GetPageBySlug(ctx, kbID, slug)
		if err != nil || page == nil {
			continue
		}
		if page.PageType == types.WikiPageTypeIndex || page.PageType == types.WikiPageTypeLog {
			continue
		}

		// Build the per-page candidate ref set: the existing out-links
		// (resolved via the batch's title fetcher to skip archived /
		// system pages) plus the freshly-written sibling slugs from
		// this batch.
		var refs []linkRef
		if len(page.OutLinks) > 0 {
			titles := batchCtx.SlugTitleMany(ctx, []string(page.OutLinks))
			for outSlug, title := range titles {
				if title == "" || outSlug == slug {
					continue
				}
				refs = append(refs, linkRef{slug: outSlug, matchText: title})
			}
		}
		for _, fr := range freshRefs {
			if fr.slug == slug {
				continue
			}
			refs = append(refs, fr)
		}
		if len(refs) == 0 {
			continue
		}

		newContent, changed := linkifyContent(page.Content, refs, page.Slug)
		if !changed {
			continue
		}
		page.Content = newContent
		if err := s.wikiService.UpdateAutoLinkedContent(ctx, page); err != nil {
			logger.Warnf(ctx, "wiki ingest: cross-link injection failed for %s: %v", page.Slug, err)
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

// getExistingPageSlugsForKnowledge returns all page slugs that currently
// reference a given knowledge ID in their source_refs. Used to snapshot
// state before re-ingest so the reduce phase can reconcile additions vs
// retractions.
//
// Backed by idx_wiki_pages_source_refs (GIN jsonb_path_ops, migration
// 000041) and the legacy text-index fallback for "kid|title" entries.
// We project to slugs only — no need to load full row content for a
// per-doc snapshot.
//
// Index/log slugs (wiki-intrinsic system pages) never carry real
// source_refs in practice, but we filter them out explicitly here as
// a defense-in-depth measure: an old buggy ingest that mistakenly
// stamped a system page with a knowledge ref would otherwise show up
// in the reparse "old set" and confuse the reduce stage.
func (s *wikiIngestService) getExistingPageSlugsForKnowledge(ctx context.Context, kbID, knowledgeID string) map[string]bool {
	slugs, err := s.wikiService.ListSlugsBySourceRef(ctx, kbID, knowledgeID)
	if err != nil {
		logger.Warnf(ctx, "wiki ingest: ListSlugsBySourceRef(%s) failed: %v", knowledgeID, err)
		return nil
	}
	if len(slugs) == 0 {
		return nil
	}
	out := make(map[string]bool, len(slugs))
	for _, slug := range slugs {
		// Defense-in-depth: skip wiki-intrinsic slugs that never have
		// real source refs.
		if slug == "index" || slug == "log" {
			continue
		}
		out[slug] = true
	}
	return out
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

// rebuildIndexPage refreshes the LLM-generated intro that sits on the
// index wiki_pages row.
//
// History: the index page used to store "intro + full directory listing" as
// a single multi-MB markdown blob in content. Every ingest batch rewrote
// the whole column, which on KBs with tens of thousands of pages caused
// O(N) TOAST writes per batch. The directory was lifted out into the
// structured GET /wiki/index endpoint (see wikiPageService.GetIndexView),
// and this method now only maintains the intro.
//
// Intro lifecycle:
//   - First time (empty or legacy placeholder): generate from all document
//     summaries via WikiIndexIntroPrompt.
//   - Subsequent calls with a change description: incremental update via
//     WikiIndexIntroUpdatePrompt so the intro reflects what just landed.
//   - No change description: keep the existing intro untouched.
//
// The new intro is written to both Content and Summary so readers that
// still fall back to Summary (older clients, legacy migrations) stay in
// sync with the column the view actually renders.
// indexIntroSummaryCap caps how many summary pages we feed into the
// LLM when generating the wiki index intro from scratch. A 4w-document
// KB would otherwise blow the context window every batch, and the
// intro is a "set the scene" artifact where the most-recently-touched
// documents carry disproportionately more signal anyway. We pick the
// top-N most-recently-updated summaries and add a "showing N of M"
// hint to the prompt so the LLM can be honest about its sample.
const indexIntroSummaryCap = 200

// rebuildIndexPage refreshes the LLM-generated intro on the index
// page. Two paths:
//
//   - First-time generation (no existing intro, or only the legacy
//     placeholder): the LLM gets a CAPPED window of the most recent
//     summary pages (most-recently-updated wins). Compare with the
//     legacy path which loaded ALL summaries — at 4w-document scale
//     that produced multi-MB prompts that simply broke the context
//     window and silently fell back to a hardcoded intro.
//   - Incremental update: the LLM gets only the existing intro plus
//     the change description for THIS batch. Document summaries are
//     intentionally NOT included — at scale the change-description
//     alone is enough signal for "what landed?", and excluding the
//     full summary set keeps the prompt size bounded regardless of
//     KB size.
//
// The intro is written to both Content and Summary so legacy readers
// that fall through to Summary stay in sync.
func (s *wikiIngestService) rebuildIndexPage(ctx context.Context, chatModel chat.Chat, payload WikiIngestPayload, changeDesc, lang string) error {
	indexPage, _ := s.wikiService.GetIndex(ctx, payload.KnowledgeBaseID)
	if indexPage == nil {
		return nil
	}

	// The intro lives on both Content and Summary. Prefer Content since
	// that's what the new index view returns; fall back to Summary for
	// rows written before this refactor so the incremental-update prompt
	// has something to work with.
	existingIntro := strings.TrimSpace(indexPage.Content)
	if existingIntro == "" {
		existingIntro = strings.TrimSpace(indexPage.Summary)
	}
	// Detect the legacy "intro + directory" payload. Such rows embed the
	// fence-separated "## Summary" sections right after the intro, so we
	// clip everything from the first directory heading onward to keep the
	// intro length bounded when we feed it back into the update prompt.
	if idx := strings.Index(existingIntro, "\n## "); idx >= 0 {
		existingIntro = strings.TrimSpace(existingIntro[:idx])
	}

	var intro string
	switch {
	case existingIntro == "" || existingIntro == "Wiki index - table of contents":
		// First-time generation: pull the top-N most-recent summary
		// pages via the lite projection. CountByType lets us tell the
		// LLM "showing N of M" so it can frame the intro honestly when
		// the KB is bigger than what we're sampling.
		recentSummaries, listErr := s.wikiService.ListByTypeRecent(ctx, payload.KnowledgeBaseID, types.WikiPageTypeSummary, indexIntroSummaryCap)
		if listErr != nil {
			return listErr
		}
		var docSummaries strings.Builder
		for _, e := range recentSummaries {
			fmt.Fprintf(&docSummaries, "<document>\n<title>%s</title>\n<summary>%s</summary>\n</document>\n\n", e.Title, e.Summary)
		}
		// Best-effort total count for the framing hint. CountByType
		// counts every page type; we need just summary, so we read
		// directly. A failure here doesn't block intro generation.
		totalSummaries := int64(len(recentSummaries))
		if counts, cntErr := s.wikiService.CountByType(ctx, payload.KnowledgeBaseID); cntErr == nil {
			if t, ok := counts[types.WikiPageTypeSummary]; ok {
				totalSummaries = t
			}
		}
		framing := ""
		if int(totalSummaries) > len(recentSummaries) && len(recentSummaries) > 0 {
			framing = fmt.Sprintf("(showing %d most recent of %d total documents)\n\n", len(recentSummaries), totalSummaries)
		}
		if docSummaries.Len() == 0 {
			docSummaries.WriteString("(no documents yet)")
		}
		generatedIntro, genErr := s.generateWithTemplate(ctx, chatModel, agent.WikiIndexIntroPrompt, map[string]string{
			"DocumentSummaries": framing + docSummaries.String(),
			"Language":          lang,
		})
		if genErr != nil {
			intro = "# Wiki Index\n\nThis wiki contains knowledge extracted from uploaded documents.\n"
		} else {
			intro = strings.TrimSpace(generatedIntro)
		}
	case changeDesc != "":
		// Incremental update: only the existing intro + this batch's
		// change description go into the prompt. We deliberately stop
		// passing the full DocumentSummaries set here — at 4w docs it
		// would re-flood the context every batch, and the
		// change-description block already encodes the "what just
		// changed" signal the prompt is asking for.
		updatedIntro, genErr := s.generateWithTemplate(ctx, chatModel, agent.WikiIndexIntroUpdatePrompt, map[string]string{
			"ExistingIntro":     existingIntro,
			"ChangeDescription": changeDesc,
			"DocumentSummaries": "",
			"Language":          lang,
		})
		if genErr != nil {
			intro = existingIntro // keep existing on error
		} else {
			intro = strings.TrimSpace(updatedIntro)
		}
	default:
		// No change description and an existing intro: leave it as-is so
		// we don't bump the version for a no-op.
		intro = existingIntro
	}

	// Defensive: some LLM outputs occasionally bleed into a directory-
	// like section even when the intro prompt doesn't ask for one. If
	// the freshly-generated intro starts to look like a legacy payload,
	// clip it at the first "\n## " just like we did on the read path
	// above. This keeps indexPage.Content a bounded intro-only blob.
	if idx := strings.Index(intro, "\n## "); idx >= 0 {
		intro = strings.TrimSpace(intro[:idx])
	}

	indexPage.Content = intro
	indexPage.Summary = intro
	_, err := s.wikiService.UpdatePage(ctx, indexPage)
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

// buildLogEntry builds a WikiLogEntry struct for the current batch. It is
// pure (no DB access) so callers can accumulate entries cheaply under their
// lock and flush them in a single AppendBatch call at the end of the batch.
//
// Historically this was a per-event `GetLog + UpdatePage` round trip, which
// rewrote the entire log page's TEXT column on every ingest/retract op —
// O(n^2) write amplification as the log grew. The batch writer now uses
// wikiLogEntryService.AppendBatch instead; see ProcessWikiIngest.
func (s *wikiIngestService) buildLogEntry(tenantID uint64, kbID, action, knowledgeID, docTitle, summary string, pagesAffected []types.WikiLogPageRef) *types.WikiLogEntry {
	// Copy pagesAffected so the entry does not alias caller-owned slices.
	// The batch accumulates SlugUpdate results that may be reused downstream.
	var pages types.WikiLogPageRefs
	if len(pagesAffected) > 0 {
		pages = make(types.WikiLogPageRefs, len(pagesAffected))
		copy(pages, pagesAffected)
	}
	return &types.WikiLogEntry{
		TenantID:        tenantID,
		KnowledgeBaseID: kbID,
		Action:          action,
		KnowledgeID:     knowledgeID,
		DocTitle:        docTitle,
		Summary:         summary,
		PagesAffected:   pages,
		CreatedAt:       time.Now(),
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
// deduplicateExtractedBatch deduplicates both entities and concepts against
// existing wiki pages in a single LLM call. Pre-filters candidates via the
// pg_trgm trigram index on lower(title) — every new item issues a
// FindSimilarPages probe and the union of top-K hits across all items is
// the candidate set. This replaces the legacy "ListAllPages + Go-side
// surface-form Jaccard" path that scaled O(P × N) on large KBs.
//
// The KB-id-keyed query relies on idx_wiki_pages_title_trgm (added in
// migration 000041); pg_search environments load pg_trgm in the same
// init step (see migrations/paradedb/00-init-db.sql).
func (s *wikiIngestService) deduplicateExtractedBatch(
	ctx context.Context,
	chatModel chat.Chat,
	kbID string,
	entities, concepts []extractedItem,
) ([]extractedItem, []extractedItem) {
	if len(entities) == 0 && len(concepts) == 0 {
		return entities, concepts
	}
	if s.wikiService == nil {
		return entities, concepts
	}

	// Build the candidate set: for each new item, ask the repo for
	// the top-K trigram-similar pages and union the results. Dedup by
	// slug as we go so the prompt only carries each candidate once.
	candidatePages := make(map[string]*types.WikiPageLite)
	probe := func(item extractedItem) {
		queries := make([]string, 0, 1+len(item.Aliases))
		if item.Name != "" {
			queries = append(queries, item.Name)
		}
		for _, alias := range item.Aliases {
			if alias != "" {
				queries = append(queries, alias)
			}
		}
		for _, q := range queries {
			pages, err := s.wikiService.FindSimilarPages(ctx, kbID, q,
				[]string{types.WikiPageTypeEntity, types.WikiPageTypeConcept},
				dedupCandidateTopK)
			if err != nil {
				logger.Warnf(ctx, "wiki ingest: dedup FindSimilarPages(%q) failed: %v", q, err)
				continue
			}
			for _, p := range pages {
				if p == nil || p.Slug == "" {
					continue
				}
				if _, ok := candidatePages[p.Slug]; !ok {
					candidatePages[p.Slug] = p
				}
			}
		}
	}
	for _, e := range entities {
		probe(e)
	}
	for _, c := range concepts {
		probe(c)
	}
	if len(candidatePages) == 0 {
		// No similar existing pages — nothing to merge against. The
		// items pass through unchanged.
		logger.Infof(ctx, "wiki ingest: no similar existing pages found for %d new items", len(entities)+len(concepts))
		return entities, concepts
	}
	logger.Infof(ctx, "wiki ingest: similar existing pages selected for %d new items",
		len(candidatePages), len(entities)+len(concepts))

	var existingBuf strings.Builder
	for _, p := range candidatePages {
		writeDedupItemXML(&existingBuf, p.Slug, p.Title, p.PageType, []string(p.Aliases))
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

	// Build the existing-slug set from the candidate map: anything not
	// in candidates is rejected as an LLM hallucination, since by
	// construction the model only ever saw those slugs as merge
	// targets. Compare with the legacy "look up against allPages"
	// path which had a wider acceptance window.
	existingSlugs := make(map[string]bool, len(candidatePages))
	for slug := range candidatePages {
		existingSlugs[slug] = true
	}

	validMerge := func(srcSlug, dstSlug string) bool {
		if !existingSlugs[dstSlug] {
			logger.Warnf(ctx, "wiki ingest: dedup rejected %s → %s (target slug does not exist in candidate set)", srcSlug, dstSlug)
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

// generateWithTemplate executes a prompt template and calls the LLM with
// bounded exponential-backoff retries for transient infrastructure errors.
//
// Retry policy:
//   - Up to wikiLLMMaxAttempts total attempts (initial + retries).
//   - Only retry errors classified as transient by isTransientLLMError:
//     HTTP 408/429/5xx, context deadline exceeded (when the parent ctx is
//     still alive), or generic "timeout"/"connection reset" wording.
//     4xx (except 408/429) is a caller-side fault and fails fast.
//   - Backoff is exponential base 2s: 2s, 4s, 8s — roughly wikiLLMBackoffBase
//   - 2^(attempt-1). Honors ctx cancellation so the task can abort.
//
// This exists because wiki ingest makes several independent LLM calls per
// document (extraction, summary, dedup, citations, intro) and a single
// transient 504 from the upstream gateway used to drop the document's
// summary page permanently. Retries plus failedOps requeuing (see
// mapOneDocument) turn those events into at-most-a-few-minute hiccups.
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

	var lastErr error
	for attempt := 1; attempt <= wikiLLMMaxAttempts; attempt++ {
		response, err := chatModel.Chat(ctx, []chat.Message{
			{Role: "user", Content: prompt},
		}, &chat.ChatOptions{
			Temperature: 0.3,
			Thinking:    &thinking,
		})
		if err == nil {
			return response.Content, nil
		}
		lastErr = err

		// Abort immediately on non-retryable errors (4xx except 408/429,
		// parse/marshal failures, tool-side bugs, etc.). Retrying a
		// hard "invalid arguments" error just wastes the model's budget.
		if !isTransientLLMError(ctx, err) {
			return "", fmt.Errorf("LLM call failed: %w", err)
		}
		if attempt == wikiLLMMaxAttempts {
			break
		}

		backoff := wikiLLMBackoffBase << (attempt - 1)
		logger.Warnf(ctx, "wiki ingest: LLM call failed (attempt %d/%d), retrying in %s: %v",
			attempt, wikiLLMMaxAttempts, backoff, err)
		select {
		case <-ctx.Done():
			return "", fmt.Errorf("LLM call aborted during backoff: %w", ctx.Err())
		case <-time.After(backoff):
		}
	}
	return "", fmt.Errorf("LLM call failed after %d attempts: %w", wikiLLMMaxAttempts, lastErr)
}

// isTransientLLMError reports whether an error from the chat provider
// looks like an infrastructure hiccup worth retrying. Classification is
// intentionally conservative: the truthful "could not tell, assume
// permanent" choice keeps retries cheap and avoids masking real bugs.
//
// We treat the following as transient:
//   - HTTP 408 (client request timeout — upstream usually didn't process),
//     429 (rate-limited — retry after backoff may succeed), 5xx (any
//     server-side fault, including the 504 "Remote error, timeout with
//     60" we see from the gateway in front of several LLM providers).
//   - Wrapped context.DeadlineExceeded when the parent ctx is still alive
//     (nested per-call timeouts).
//   - Substring matches on the error text for common transport failures
//     ("timeout", "connection reset", "EOF") that providers surface
//     without a structured status code.
func isTransientLLMError(ctx context.Context, err error) bool {
	if err == nil {
		return false
	}
	// Never retry after the parent ctx itself expired — the task is
	// being cancelled and the next attempt would just fail again.
	if ctx.Err() != nil {
		return false
	}

	msg := err.Error()
	// Providers that bubble HTTP status up formatted as
	// "API request failed with status NNN: ..." — match that first.
	for _, s := range []string{
		"status 408", "status 429",
		"status 500", "status 501", "status 502", "status 503", "status 504",
		"status 520", "status 521", "status 522", "status 523", "status 524",
	} {
		if strings.Contains(msg, s) {
			return true
		}
	}

	lower := strings.ToLower(msg)
	for _, s := range []string{
		"timeout",
		"timed out",
		"connection reset",
		"connection refused",
		"broken pipe",
		"no such host", // DNS hiccup
		"i/o timeout",
		"unexpected eof",
		"tls handshake",
		"context deadline exceeded", // nested per-call deadline
	} {
		if strings.Contains(lower, s) {
			return true
		}
	}
	return false
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
