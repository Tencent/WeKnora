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
	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/redis/go-redis/v9"
)

// ErrWikiIngestConcurrent is returned by the wiki ingest handler in Lite mode
// when another batch is already running for the same KB (the in-process
// liteLocks guard is held). The asynq/sync executor's RetryDelayFunc uses
// errors.Is on this sentinel to apply a short, fixed retry delay instead of
// exponential backoff, so the deferred batch retries promptly once the active
// one releases. Standard (Redis) mode no longer takes an exclusive per-KB
// lock (Phase 3) and never returns this.
var ErrWikiIngestConcurrent = errors.New("concurrent wiki task active")

const (
	// maxContentForWiki limits the document content sent to LLM for wiki generation
	maxContentForWiki = 32768

	// --- Phase 3: concurrent per-KB batches (standard/Redis mode) ---------
	//
	// Phase 3 removed the exclusive per-KB Redis "batch in progress" lock
	// (wiki:active:<kbID>). Standard mode now allows concurrent batches for
	// one KB, kept safe by row claiming (claimPendingList) plus per-slug
	// reduce locks (withSlugLock). Lite mode still serializes per KB via the
	// in-process liteLocks map.
	//
	// wikiClaimStaleAfter is how long a claimed-but-undrained ingest row
	// waits before another worker may re-claim it. It MUST exceed the asynq
	// task Timeout for wiki:ingest (60m) so a still-running batch's rows are
	// never stolen mid-flight — only genuinely crashed/abandoned claims are
	// recovered. This preserves the pre-claim crash behaviour where a dead
	// batch's undeleted rows were simply re-peeked by the next trigger.
	wikiClaimStaleAfter = 90 * time.Minute

	// wikiSlugLockPrefix guards read-modify-write on a single shared wiki
	// page (entity/concept/summary/index) so two concurrent batches for the
	// same KB can't lost-update the same slug. Key: wiki:slug:{kbID}:{slug}.
	wikiSlugLockPrefix = "wiki:slug:"
	// wikiSlugLockTTL bounds the per-slug lock so a crashed reducer can't
	// wedge a hot page forever. Comfortably longer than a single reduce
	// (one LLM modify call).
	wikiSlugLockTTL = 5 * time.Minute
	// wikiSlugLockWait / Poll bound how long a reduce goroutine blocks
	// waiting for a contended slug before falling back to a best-effort
	// skip (matching the pre-existing reduce-failure semantics).
	wikiSlugLockWait = 2 * time.Minute
	wikiSlugLockPoll = 50 * time.Millisecond

	// --- Phase 4: per-KB in-flight cap (standard/Redis mode) --------------
	//
	// Since Phase 3 lets many batches for one KB run at once, a single KB's
	// bulk import could otherwise occupy the entire wiki pool and starve
	// other KBs. Each running batch reserves a slot in a per-KB Redis sorted
	// set (score = expiry). ZREMRANGEBYSCORE purges slots left behind by a
	// crashed worker, so the cap is self-healing without an explicit lock.
	//
	// wikiInflightPrefix keys the per-KB slot set: wiki:inflight:{kbID}.
	wikiInflightPrefix = "wiki:inflight:"
	// wikiInflightDefault is the fallback cap when WikiConfig.IngestMaxInflight
	// is unset. 4 leaves half of the default 8-worker wiki pool for other
	// KBs while still giving one KB solid parallelism.
	wikiInflightDefault = 4
	// wikiInflightTTL is how long a reserved slot survives without renewal.
	// MUST exceed wikiInflightRenew by a comfortable margin so a single
	// missed renew (GC pause, Redis blip) doesn't drop a live batch's slot.
	wikiInflightTTL = 90 * time.Second
	// wikiInflightRenew is how often a running batch bumps its slot's expiry.
	wikiInflightRenew = 30 * time.Second
	// wikiInflightBackoff delays the follow-up trigger when a batch is turned
	// away by the cap, so it retries after a slot plausibly frees.
	wikiInflightBackoff = 10 * time.Second

	// wikiIngestDelay is how long to wait after a document is added before
	// the batch task fires. Debounces rapid uploads.
	wikiIngestDelay = 30 * time.Second

	// wikiFollowUpDelay is the normal light debounce before a follow-up
	// batch drains the remaining pending rows. Standard mode already fans a
	// KB's backlog across concurrent claiming batches, so this is just a
	// short breather, not a lock-release wait.
	wikiFollowUpDelay = 5 * time.Second

	// wikiRateLimitBackoff is the (much longer) follow-up delay used when a
	// batch's failures were caused by upstream rate limiting (HTTP 429 /
	// quota). Retrying a rate-limited document at the normal 5s cadence just
	// throws more requests at an already-saturated rpm budget — every retry
	// re-issues extract + classify + summary calls for the doc — which keeps
	// the limiter tripped and drags the whole KB's ingest out. Backing off
	// gives the per-minute window time to reset before we try again.
	wikiRateLimitBackoff = 60 * time.Second

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

	// --- Finalize lane (Phase 1: debounced KB-global convergence) ---------
	//
	// The KB-global convergence steps (index-intro rebuild, dead-link
	// cleanup, cross-link injection) used to run at the tail of EVERY
	// 5-doc ingest batch. On a bulk import that meant O(docs/batchSize)
	// index rebuilds — each an LLM call plus an index-page write. They are
	// now deferred to a debounced per-KB wiki:finalize task: each ingest
	// batch records what changed into a separate lane of task_pending_ops
	// (task_type=wiki:finalize) and schedules a coalesced trigger, so a
	// burst of N documents rebuilds the index ONCE at the end.

	// wikiFinalizeTaskType is the task_pending_ops lane for finalize work.
	// Keyed by (task_type, scope, scope_id) like the ingest lane, but with a
	// distinct task_type so PeekBatch never mixes the two.
	wikiFinalizeTaskType = "wiki:finalize"

	// wikiFinalizeOpSlug rows carry one affected page slug (+ its fresh title
	// when this batch wrote it) for the dead-link / cross-link passes.
	wikiFinalizeOpSlug = "slug"
	// wikiFinalizeOpChange rows carry a doc-level add/remove change entry for
	// the index-intro change description.
	wikiFinalizeOpChange = "change"

	wikiFinalizeAdded   = "added"
	wikiFinalizeRemoved = "removed"

	// wikiFinalizeDelay debounces the finalize trigger so multiple ingest
	// batches within the window coalesce into a single index rebuild.
	wikiFinalizeDelay = 20 * time.Second

	// wikiFinalizeMaxRows caps how many finalize rows one run drains. A run
	// that hits the cap reschedules itself for the remainder.
	wikiFinalizeMaxRows = 5000

	// wikiFinalizeLockTTL / Renew guard against two finalize runs for the
	// same KB writing the index page concurrently (belt-and-suspenders on
	// top of the asynq.TaskID coalescing).
	wikiFinalizeLockTTL   = 60 * time.Second
	wikiFinalizeLockRenew = 20 * time.Second

	// wikiIngestCleanupTimeout bounds detached tail cleanup after the asynq
	// task context has been cancelled or hit its timeout.
	wikiIngestCleanupTimeout = 10 * time.Second
)

// wikiFinalizeChange is a doc-level add/remove entry for the index-intro
// change description, persisted as a finalize-lane row payload.
type wikiFinalizeChange struct {
	Action     string `json:"action"` // wikiFinalizeAdded | wikiFinalizeRemoved
	DocTitle   string `json:"doc_title,omitempty"`
	DocSummary string `json:"doc_summary,omitempty"`
}

// wikiFinalizeRow is the JSON payload of a task_pending_ops row in the
// finalize lane. Exactly one of {Slug, Change} is set, distinguished by the
// row's Op column (wikiFinalizeOpSlug / wikiFinalizeOpChange).
type wikiFinalizeRow struct {
	Slug   string              `json:"slug,omitempty"`
	Title  string              `json:"title,omitempty"`
	Change *wikiFinalizeChange `json:"change,omitempty"`
}

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
	knowledgeRepo  interfaces.KnowledgeRepository
	chunkRepo      interfaces.ChunkRepository
	modelService   interfaces.ModelService
	task           interfaces.TaskEnqueuer
	logEntrySvc    interfaces.WikiLogEntryService
	pendingRepo    interfaces.TaskPendingOpsRepository
	deadLetterRepo interfaces.TaskDeadLetterRepository
	redisClient    *redis.Client // nil in Lite mode (no Redis)
	// spanTracker lets per-document map work surface as a
	// postprocess.wiki subspan in the knowledge trace tree. Async
	// batch design means we look up the parent attempt by knowledge
	// id at run-time (LatestAttempt) rather than carrying it in the
	// asynq payload, which is per-KB and would otherwise be ambiguous
	// for the 5-docs-per-batch fan-out.
	spanTracker SpanTracker
	// liteLocks provides per-KB mutual exclusion in Lite mode (no Redis).
	// Keys are kbID strings; values are unused (presence = locked).
	liteLocks sync.Map
	// liteFinalizeLocks is the Lite-mode counterpart of the Redis
	// wiki:finalize:active:<kbID> lock, keeping two finalize runs for the
	// same KB from overlapping when there is no Redis.
	liteFinalizeLocks sync.Map
}

// NewWikiIngestService creates a new wiki ingest service
func NewWikiIngestService(
	wikiService interfaces.WikiPageService,
	kbService interfaces.KnowledgeBaseService,
	knowledgeSvc interfaces.KnowledgeService,
	knowledgeRepo interfaces.KnowledgeRepository,
	chunkRepo interfaces.ChunkRepository,
	modelService interfaces.ModelService,
	task interfaces.TaskEnqueuer,
	logEntrySvc interfaces.WikiLogEntryService,
	pendingRepo interfaces.TaskPendingOpsRepository,
	deadLetterRepo interfaces.TaskDeadLetterRepository,
	redisClient *redis.Client,
	spanTracker SpanTracker,
) interfaces.TaskHandler {
	svc := &wikiIngestService{
		wikiService:    wikiService,
		kbService:      kbService,
		knowledgeSvc:   knowledgeSvc,
		knowledgeRepo:  knowledgeRepo,
		chunkRepo:      chunkRepo,
		modelService:   modelService,
		task:           task,
		logEntrySvc:    logEntrySvc,
		pendingRepo:    pendingRepo,
		deadLetterRepo: deadLetterRepo,
		redisClient:    redisClient,
		spanTracker:    spanTracker,
	}
	return svc
}

// tracker returns a non-nil span tracker so callers don't have to
// nil-check on every Begin/End. Matches the noopSpanTracker pattern
// used elsewhere (see knowledgeService.tracker, KnowledgePostProcessService.tracker).
func (s *wikiIngestService) tracker() SpanTracker {
	if s.spanTracker == nil {
		return noopSpanTracker{}
	}
	return s.spanTracker
}

// beginWikiSubspan opens a postprocess.wiki subspan for this document
// under the knowledge's most recent attempt. Returns nil when there is
// no parse attempt to attach to (e.g. a wiki ingest fired from a manual
// reparse path that never went through the tracker) — callers must
// pair every begin with a tolerant end / fail / skip below.
//
// Lookups are by `LatestAttempt(knowledgeID)` because the asynq task
// payload (WikiIngestPayload) is KB-scoped and carries no per-doc
// attempt — see the type's comment for the batch architecture.
func (s *wikiIngestService) beginWikiSubspan(ctx context.Context, knowledgeID string, input types.JSONMap) *Span {
	if knowledgeID == "" {
		return nil
	}
	attempt := s.tracker().LatestAttempt(ctx, knowledgeID)
	if attempt <= 0 {
		return nil
	}
	parent := s.tracker().LookupStage(ctx, knowledgeID, attempt, types.StagePostProcess)
	if parent == nil {
		return nil
	}
	return s.tracker().BeginSubSpan(ctx, parent, "postprocess.wiki", types.SpanKindSubSpan, input)
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
		asynq.Queue(types.QueueWiki),
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
		asynq.Queue(types.QueueWiki),
		asynq.MaxRetry(wikiIngestMaxRetry),
		asynq.Timeout(60*time.Minute),
		asynq.ProcessIn(5*time.Second), // Retract can trigger the batch quickly
	)
	if _, err := task.Enqueue(t); err != nil {
		logger.Warnf(ctx, "wiki retract: failed to enqueue trigger task: %v", err)
	}
}

// Handle implements interfaces.TaskHandler for asynq task processing. The
// wiki service owns two task types, dispatched here by type so a single
// registered handler covers both the ingest batch and the debounced
// KB-global finalize pass.
func (s *wikiIngestService) Handle(ctx context.Context, t *asynq.Task) error {
	switch t.Type() {
	case types.TypeWikiFinalize:
		return s.ProcessWikiFinalize(ctx, t)
	default:
		return s.ProcessWikiIngest(ctx, t)
	}
}

func wikiIngestCleanupContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithTimeout(context.WithoutCancel(ctx), wikiIngestCleanupTimeout)
}

// enqueueFinalize persists this batch's KB-global convergence work into the
// finalize lane of task_pending_ops and schedules a debounced trigger. One
// "slug" row per affected page (carrying its fresh title when this batch wrote
// it, for cross-linking) plus one "change" row per doc added/removed (for the
// index-intro change description).
func (s *wikiIngestService) enqueueFinalize(
	ctx context.Context,
	payload WikiIngestPayload,
	affectedSlugs []string,
	freshTitleBySlug map[string]string,
	changes []wikiFinalizeChange,
) {
	if s.pendingRepo == nil {
		return
	}
	for _, slug := range affectedSlugs {
		row := wikiFinalizeRow{Slug: slug, Title: freshTitleBySlug[slug]}
		b, err := json.Marshal(row)
		if err != nil {
			continue
		}
		if err := s.pendingRepo.Enqueue(ctx, &types.TaskPendingOp{
			TenantID: payload.TenantID,
			TaskType: wikiFinalizeTaskType,
			Scope:    wikiTaskScope,
			ScopeID:  payload.KnowledgeBaseID,
			Op:       wikiFinalizeOpSlug,
			DedupKey: slug,
			Payload:  b,
		}); err != nil {
			logger.Warnf(ctx, "wiki finalize: enqueue slug row for %s failed: %v", slug, err)
		}
	}
	for i := range changes {
		row := wikiFinalizeRow{Change: &changes[i]}
		b, err := json.Marshal(row)
		if err != nil {
			continue
		}
		if err := s.pendingRepo.Enqueue(ctx, &types.TaskPendingOp{
			TenantID: payload.TenantID,
			TaskType: wikiFinalizeTaskType,
			Scope:    wikiTaskScope,
			ScopeID:  payload.KnowledgeBaseID,
			Op:       wikiFinalizeOpChange,
			DedupKey: "",
			Payload:  b,
		}); err != nil {
			logger.Warnf(ctx, "wiki finalize: enqueue change row failed: %v", err)
		}
	}
	s.scheduleFinalize(ctx, payload)
}

// scheduleFinalize enqueues a debounced, coalesced KB-global finalize trigger.
// asynq.TaskID ("wiki-finalize-<kbID>") makes concurrent schedules within the
// debounce window collapse into one pending task; the conflict error is the
// expected coalescing signal, not a failure. In Lite mode the sync executor
// ignores TaskID, so finalize simply runs once per batch (acceptable at the
// small scale Lite mode targets).
func (s *wikiIngestService) scheduleFinalize(ctx context.Context, payload WikiIngestPayload) {
	langfuse.InjectTracing(ctx, &payload)
	b, _ := json.Marshal(payload)
	t := asynq.NewTask(types.TypeWikiFinalize, b,
		asynq.Queue(types.QueueWiki),
		asynq.MaxRetry(wikiIngestMaxRetry),
		asynq.Timeout(30*time.Minute),
		asynq.ProcessIn(wikiFinalizeDelay),
		asynq.TaskID("wiki-finalize-"+payload.KnowledgeBaseID),
	)
	if _, err := s.task.Enqueue(t); err != nil {
		if errors.Is(err, asynq.ErrTaskIDConflict) || errors.Is(err, asynq.ErrDuplicateTask) {
			return // a finalize is already scheduled/running for this KB — coalesced
		}
		logger.Warnf(ctx, "wiki finalize: schedule trigger failed: %v", err)
	}
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
func (s *wikiIngestService) peekPendingList(ctx context.Context, kbID string, limit int) (ops []WikiPendingOp, peekedIDs []int64, err error) {
	if s.pendingRepo == nil {
		return nil, nil, nil
	}
	if limit <= 0 {
		limit = wikiMaxDocsPerBatch
	}
	rows, err := s.pendingRepo.PeekBatch(ctx, wikiTaskType, wikiTaskScope, kbID, limit)
	if err != nil {
		// Surface the error so the caller can distinguish a transient DB
		// failure from a genuinely empty queue: the former must trigger an
		// asynq retry, whereas returning "no rows" here would ack the task
		// as a false success and strand the pending list.
		return nil, nil, err
	}
	ops, peekedIDs = s.decodePendingRows(ctx, rows)
	return ops, peekedIDs, nil
}

// claimPendingList is the standard-mode (Redis) counterpart of
// peekPendingList: it atomically CLAIMS up to `limit` ops (marks
// claimed_at) so concurrent batches for the same KB — allowed since Phase 3
// removed the exclusive per-KB lock — pull DISJOINT documents instead of
// double-processing. Stale claims (older than wikiClaimStaleAfter, i.e. from
// a crashed worker) are recovered. Dedup / peekedIDs semantics match
// peekPendingList; the returned peekedIDs are the claimed rows that the
// caller must DeleteByIDs on success or ReleaseByIDs to retry.
func (s *wikiIngestService) claimPendingList(ctx context.Context, kbID string, limit int) (ops []WikiPendingOp, peekedIDs []int64, err error) {
	if s.pendingRepo == nil {
		return nil, nil, nil
	}
	if limit <= 0 {
		limit = wikiMaxDocsPerBatch
	}
	rows, err := s.pendingRepo.ClaimBatch(ctx, wikiTaskType, wikiTaskScope, kbID, limit,
		time.Now().Add(-wikiClaimStaleAfter))
	if err != nil {
		// A claim failure is transient (DB blip). Propagate it so the batch
		// returns an error and asynq retries, instead of acking the trigger
		// as a false "no pending ops" success and stranding the queue.
		return nil, nil, err
	}
	ops, peekedIDs = s.decodePendingRows(ctx, rows)
	return ops, peekedIDs, nil
}

// withSlugLock serializes read-modify-write on one shared wiki page across
// concurrent batches. Phase 3 removed the exclusive per-KB batch lock, so
// two batches for the same KB can both produce updates for a shared
// entity/concept slug; without this lock their GetPageBySlug→UpdatePage
// cycles would race and lose one contribution.
//
// Redis mode only: in Lite mode a single process runs one batch per KB at a
// time (liteLocks), so same-slug contention cannot occur and fn runs
// directly. Returns (false, nil) if the lock could not be acquired within
// wikiSlugLockWait — the caller then treats the slug like a best-effort
// reduce miss (matching pre-existing reduce-failure handling). On a Redis
// error we fail OPEN (run fn unlocked): a rare lost-update on a shared page
// is tolerated by the finalize/dead-link passes, whereas silently dropping
// the update is strictly worse.
func (s *wikiIngestService) withSlugLock(ctx context.Context, kbID, slug string, fn func() error) (bool, error) {
	if s.redisClient == nil {
		return true, fn()
	}
	key := wikiSlugLockPrefix + kbID + ":" + slug
	deadline := time.Now().Add(wikiSlugLockWait)
	for {
		ok, rerr := s.redisClient.SetNX(ctx, key, "1", wikiSlugLockTTL).Result()
		if rerr != nil {
			logger.Warnf(ctx, "wiki reduce: slug lock SetNX failed for %s: %v (running unlocked)", slug, rerr)
			return true, fn()
		}
		if ok {
			break
		}
		if time.Now().After(deadline) {
			return false, nil
		}
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		case <-time.After(wikiSlugLockPoll):
		}
	}
	defer s.redisClient.Del(context.Background(), key)
	return true, fn()
}

// wikiInflightReserveScript atomically enforces the per-KB in-flight cap.
// It purges expired slots (crash recovery), counts live slots, and adds the
// caller's slot iff under the cap. Returning 1 = reserved, 0 = at cap.
// Keeping purge+count+add in one Lua call closes the check-then-act race two
// concurrent reservers would otherwise have.
const wikiInflightReserveScript = `
local now = tonumber(ARGV[1])
local expiry = tonumber(ARGV[2])
local maxInflight = tonumber(ARGV[3])
local token = ARGV[4]
local ttl = tonumber(ARGV[5])
redis.call('ZREMRANGEBYSCORE', KEYS[1], 0, now)
if redis.call('ZCARD', KEYS[1]) >= maxInflight then
  return 0
end
redis.call('ZADD', KEYS[1], expiry, token)
redis.call('PEXPIRE', KEYS[1], ttl)
return 1
`

// reserveInflightSlot claims one of KB's concurrent-batch slots (Phase 4,
// standard/Redis mode). Returns (release, true) when granted — release() MUST
// run when the batch finishes; (nil, false) when the KB is already at
// maxInflight, so the caller should reschedule and bail. A background renew
// keeps the slot alive for the batch's duration; a crashed batch's slot simply
// expires (wikiInflightTTL) and is purged by the next reserver. Lite mode has
// no shared-pool contention (liteLocks already serialize per KB), so it always
// grants a no-op slot. Fails OPEN on a Redis error: a blip must not halt wiki
// generation, and the pool size still bounds total work.
func (s *wikiIngestService) reserveInflightSlot(ctx context.Context, kbID string, maxInflight int) (func(), bool) {
	if s.redisClient == nil || maxInflight <= 0 {
		return func() {}, true
	}
	key := wikiInflightPrefix + kbID
	token := uuid.New().String()
	now := time.Now()
	res, err := s.redisClient.Eval(ctx, wikiInflightReserveScript,
		[]string{key},
		now.UnixMilli(),
		now.Add(wikiInflightTTL).UnixMilli(),
		maxInflight,
		token,
		wikiInflightTTL.Milliseconds(),
	).Int()
	if err != nil {
		logger.Warnf(ctx, "wiki ingest: inflight reserve failed for KB %s: %v (running uncapped)", kbID, err)
		return func() {}, true
	}
	if res == 0 {
		return nil, false
	}

	renewCtx, cancel := context.WithCancel(context.Background())
	go func() {
		ticker := time.NewTicker(wikiInflightRenew)
		defer ticker.Stop()
		for {
			select {
			case <-renewCtx.Done():
				return
			case <-ticker.C:
				exp := float64(time.Now().Add(wikiInflightTTL).UnixMilli())
				s.redisClient.ZAdd(context.Background(), key, redis.Z{Score: exp, Member: token})
				s.redisClient.PExpire(context.Background(), key, wikiInflightTTL)
			}
		}
	}()
	return func() {
		cancel()
		s.redisClient.ZRem(context.Background(), key, token)
	}, true
}

// scheduleCappedRetry enqueues a single coalesced follow-up trigger after a
// batch was turned away by the in-flight cap. asynq.TaskID collapses all
// turned-away triggers for one KB into a single pending retry (no thundering
// herd), and the running batches that hold the slots also chain their own
// follow-ups on completion, so the turned-away rows are guaranteed to drain
// once a slot frees.
func (s *wikiIngestService) scheduleCappedRetry(ctx context.Context, payload WikiIngestPayload) {
	langfuse.InjectTracing(ctx, &payload)
	b, _ := json.Marshal(payload)
	t := asynq.NewTask(types.TypeWikiIngest, b,
		asynq.Queue(types.QueueWiki),
		asynq.MaxRetry(wikiIngestMaxRetry),
		asynq.Timeout(60*time.Minute),
		asynq.ProcessIn(wikiInflightBackoff),
		asynq.TaskID("wiki-ingest-capped-"+payload.KnowledgeBaseID),
	)
	if _, err := s.task.Enqueue(t); err != nil {
		if errors.Is(err, asynq.ErrTaskIDConflict) || errors.Is(err, asynq.ErrDuplicateTask) {
			return // a capped retry is already pending for this KB — coalesced
		}
		logger.Warnf(ctx, "wiki ingest: capped-retry enqueue failed: %v", err)
	}
}

// scheduleStaleClaimRecheck arms a single, far-future safety-net trigger for a
// KB that still has pending rows but yielded nothing to claim (every eligible
// row is held by a FRESH claim). Normally a running batch drains those rows and
// chains its own fast follow-up on completion; this net exists only for the
// case where the claim holder CRASHED, leaving claimed_at stamped so no worker
// can re-claim until wikiClaimStaleAfter elapses — and where nothing else would
// ever re-trigger the KB afterwards.
//
// The delay is set past the stale threshold so that when the net fires the
// abandoned claims are guaranteed eligible again. asynq.TaskID coalesces all
// rechecks for one KB into a single pending net (no thundering herd from
// concurrent no-op batches). If PendingCount is already zero the KB has fully
// drained and no net is needed. Returns true if a net is (or already was)
// scheduled.
func (s *wikiIngestService) scheduleStaleClaimRecheck(ctx context.Context, payload WikiIngestPayload) bool {
	if s.pendingRepo == nil {
		return false
	}
	count, err := s.pendingRepo.PendingCount(ctx, wikiTaskType, wikiTaskScope, payload.KnowledgeBaseID)
	if err != nil || count == 0 {
		return false
	}

	logger.Infof(ctx, "wiki ingest: %d rows for KB %s held by fresh claims, arming stale-claim recheck", count, payload.KnowledgeBaseID)

	langfuse.InjectTracing(ctx, &payload)
	b, _ := json.Marshal(payload)
	t := asynq.NewTask(types.TypeWikiIngest, b,
		asynq.Queue(types.QueueWiki),
		asynq.MaxRetry(wikiIngestMaxRetry),
		asynq.Timeout(60*time.Minute),
		asynq.ProcessIn(wikiClaimStaleAfter+wikiFollowUpDelay),
		asynq.TaskID("wiki-ingest-recheck-"+payload.KnowledgeBaseID),
	)
	if _, err := s.task.Enqueue(t); err != nil {
		if errors.Is(err, asynq.ErrTaskIDConflict) || errors.Is(err, asynq.ErrDuplicateTask) {
			return true // a recheck is already armed for this KB — coalesced
		}
		logger.Warnf(ctx, "wiki ingest: stale-claim recheck enqueue failed: %v", err)
		return false
	}
	return true
}

// decodePendingRows converts raw task_pending_ops rows into WikiPendingOps,
// applying the last-write-wins dedup by knowledge_id. peekedIDs carries the
// db ids of EVERY row (including dedup-collapsed ones) so the caller can
// drain them all at trim time. Shared by peekPendingList (no claim) and
// claimPendingList (claimed rows).
func (s *wikiIngestService) decodePendingRows(ctx context.Context, rows []*types.TaskPendingOp) (ops []WikiPendingOp, peekedIDs []int64) {
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
func (s *wikiIngestService) trimPendingList(ctx context.Context, ids []int64) error {
	if s.pendingRepo == nil || len(ids) == 0 {
		return nil
	}
	if err := s.pendingRepo.DeleteByIDs(ctx, ids); err != nil {
		logger.Warnf(ctx, "wiki ingest: failed to trim %d pending rows: %v", len(ids), err)
		return err
	}
	return nil
}

// finalizeWikiSubtask releases this knowledge's slot in the finalizing
// counter once its wiki op reaches a terminal state (mapped successfully
// or dead-lettered). The matching +1 is seeded by
// KnowledgePostProcess.SetFinalizing when willSpawnWiki is true. Callers
// must only invoke this for ingest ops — retract ops are for deleted
// knowledge that has no counter to drain.
//
// Safe to call on a row that is already completed or whose counter is
// already zero: FinalizeSubtask guards both the decrement (count > 0) and
// the promote (parse_status = finalizing AND count = 0), so an op enqueued
// before this accounting shipped is a harmless no-op.
func (s *wikiIngestService) finalizeWikiSubtask(ctx context.Context, knowledgeID string) {
	// Wiki is only finalized when its op reaches a terminal state, so this is
	// always an intended drain (retErr=nil, final=true). Detached context: the
	// wiki batch worker may be mid-shutdown or have a cancelled ctx when this
	// runs; a swallowed failure would strand the parent in "finalizing".
	finalizeSubtaskDetached(ctx, s.knowledgeRepo, knowledgeID, "wiki", nil, false, true)
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
//     Settlement failures are returned so the caller does not mark claims
//     settled while rows are still claimed or undeleted.
func (s *wikiIngestService) requeueFailedOps(ctx context.Context, payload WikiIngestPayload, ops []WikiPendingOp) error {
	if s.pendingRepo == nil || len(ops) == 0 {
		return nil
	}
	var settleErrs []error
	for _, op := range ops {
		if op.dbID == 0 {
			// Op was never persisted (synthetic / test) — nothing to
			// retry against.
			continue
		}
		count, err := s.pendingRepo.IncrFailCount(ctx, op.dbID)
		if err != nil {
			logger.Warnf(ctx, "wiki ingest: failed to increment fail count for %s (id=%d): %v", op.KnowledgeID, op.dbID, err)
			settleErrs = append(settleErrs, fmt.Errorf("increment fail count id=%d: %w", op.dbID, err))
			// Without a fresh count we can't tell whether to drop. Be
			// conservative: leave the row in place; the next PeekBatch
			// will see it again and we'll try once more.
			continue
		}
		if count <= wikiMaxFailRetries {
			// Release the claim so the row is immediately eligible for the
			// next trigger's ClaimBatch instead of waiting out
			// wikiClaimStaleAfter. No-op in Lite mode (row was peeked, never
			// claimed). ReleaseByIDs preserves fail_count, so the retry
			// budget still counts down.
			if err := s.pendingRepo.ReleaseByIDs(ctx, []int64{op.dbID}); err != nil {
				logger.Warnf(ctx, "wiki ingest: failed to release claim for retry id=%d: %v", op.dbID, err)
				settleErrs = append(settleErrs, fmt.Errorf("release retry claim id=%d: %w", op.dbID, err))
			}
			logger.Infof(ctx, "wiki ingest: re-queued failed op %s (%s) for retry (attempt %d/%d)", op.KnowledgeID, op.DocTitle, count, wikiMaxFailRetries)
			continue
		}

		// Exhausted in-batch retries — archive and remove. This is the
		// terminal failure point for the op, so release its slot in the
		// knowledge's finalizing counter (ingest ops only; retracts are
		// for deleted knowledge that has no counter to drain). The
		// matching +1 was seeded by KnowledgePostProcess.SetFinalizing.
		if op.Op == WikiOpIngest {
			s.finalizeWikiSubtask(ctx, op.KnowledgeID)
		}
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
				settleErrs = append(settleErrs, fmt.Errorf("archive dead letter id=%d: %w", op.dbID, dlErr))
			}
		}
		if err := s.pendingRepo.DeleteByIDs(ctx, []int64{op.dbID}); err != nil {
			logger.Warnf(ctx, "wiki ingest: failed to drop dead-lettered row id=%d: %v", op.dbID, err)
			settleErrs = append(settleErrs, fmt.Errorf("drop dead-lettered row id=%d: %w", op.dbID, err))
		}
	}
	return errors.Join(settleErrs...)
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
	// MapStats are the per-doc map-phase metrics captured at the moment
	// mapOneDocument finishes. Surfaced into the postprocess.wiki span's
	// output so the trace viewer can show "what the map phase produced"
	// even though the span itself stays open until the batch's reduce +
	// cleanup phases complete (so the user-visible duration covers the
	// whole pipeline for this doc, not just LLM extraction).
	MapStats types.JSONMap
	// WikiSpan is the postprocess.wiki subspan opened at the start of
	// mapOneDocument. ProcessWikiIngest holds it open across the reduce
	// + cleanup phases and closes it once this doc's pages have all
	// been materialised — see the EndSpan call near the end of
	// ProcessWikiIngest. nil when no parent attempt was found, in which
	// case the tracker helpers are all no-ops anyway.
	WikiSpan *Span
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

	// ContentInstructions and ExtractionInstructions are KB-scoped business
	// guidance. Stable citation, merge, taxonomy and JSON rules remain in the
	// system templates and cannot be replaced by these fields.
	ContentInstructions    string
	ExtractionInstructions string

	// PlannedFolderID holds the per-slug wiki_folders.id assigned by the batch
	// taxonomy planning pass (planBatchTaxonomy + folder resolution), keyed by
	// page slug. Reduce applies it only to pages that aren't already filed
	// (FolderID == ""), so the whole batch lands on one coherent tree without
	// churning user-curated placements. The folders themselves are created
	// sequentially before reduce, so the parallel reduce phase only assigns
	// pre-resolved ids and never races on folder creation. Read-only during
	// reduce.
	PlannedFolderID map[string]string
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

// previewExtractedItems returns a JSON-friendly preview of the first
// `limit` extracted entities or concepts so the trace viewer's
// postprocess.wiki.extract span shows actual names/slugs/descriptions
// instead of bare counts. Each item is trimmed to a small fixed
// budget — these end up serialised into the spans table's JSONB
// output column, so the cumulative size matters more than per-item
// fidelity.
func previewExtractedItems(items []extractedItem, limit int) []map[string]string {
	if limit <= 0 {
		limit = 1
	}
	n := len(items)
	if n > limit {
		items = items[:limit]
	}
	out := make([]map[string]string, 0, len(items))
	for _, it := range items {
		out = append(out, map[string]string{
			"name":        previewText(it.Name, 60),
			"slug":        it.Slug,
			"description": previewText(it.Description, 120),
		})
	}
	return out
}

// topCitedSlugs returns the top `limit` slugs by chunk-citation count.
// Used by postprocess.wiki.classify so the trace surfaces which
// candidate slugs the citation pass attached the most chunks to —
// useful when triaging "this LLM run extracted weird things" without
// having to open and diff full chunk lists.
func topCitedSlugs(citations map[string][]string, limit int) []map[string]any {
	if len(citations) == 0 {
		return nil
	}
	type entry struct {
		slug  string
		count int
	}
	entries := make([]entry, 0, len(citations))
	for slug, ids := range citations {
		entries = append(entries, entry{slug: slug, count: len(ids)})
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].count != entries[j].count {
			return entries[i].count > entries[j].count
		}
		return entries[i].slug < entries[j].slug
	})
	if limit > 0 && len(entries) > limit {
		entries = entries[:limit]
	}
	out := make([]map[string]any, 0, len(entries))
	for _, e := range entries {
		out = append(out, map[string]any{
			"slug":   e.slug,
			"chunks": e.count,
		})
	}
	return out
}

// previewNewSlugs returns a JSON-friendly preview of the first
// `limit` slugs that the citation pass discovered (i.e. did not appear
// in pass-0's candidate list). Surfacing these makes "the citation
// LLM kept inventing entries" trivially diagnosable from the trace
// viewer.
func previewNewSlugs(items []newSlugFromCitation, limit int) []map[string]string {
	if limit <= 0 {
		limit = 1
	}
	n := len(items)
	if n > limit {
		items = items[:limit]
	}
	out := make([]map[string]string, 0, len(items))
	for _, it := range items {
		out = append(out, map[string]string{
			"name":   previewText(it.Name, 60),
			"slug":   it.Slug,
			"type":   it.Type,
			"chunks": fmt.Sprintf("%d", len(it.SourceChunks)),
		})
	}
	return out
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

// wikiTaxonomyPromptMaxPaths caps how many existing folders are rendered into a
// planning prompt as the set to reuse. Reached only for pathologically large
// taxonomies; the similarity preprocessing keeps the fed set well under it.
const wikiTaxonomyPromptMaxPaths = 150

// wikiTaxonomyFolderPoolMax bounds the existing folders pulled from the DB as the
// candidate pool for similarity selection. Distinct folders are few even for
// large KBs, so this only guards against a degenerate taxonomy.
const wikiTaxonomyFolderPoolMax = 400

// wikiTaxonomyFeedAllMaxFolders is the folder count at or below which the whole
// folder set is fed to the planner as-is: a healthy navigation directory is
// small, so feeding everything gives perfect reuse recall with no embedding cost
// (similarity preprocessing only earns its keep once folders are numerous).
const wikiTaxonomyFeedAllMaxFolders = 60

// wikiTaxonomyRelevantTopK is how many nearest existing deeper folders each item
// contributes to the reuse set when similarity preprocessing kicks in.
const wikiTaxonomyRelevantTopK = 3

// wikiTaxonomyPlanChunkSize caps how many items go into a single planning call.
// Larger batches are split into chunks; folders assigned by earlier chunks are
// fed forward as "existing folders" so later chunks converge onto the same tree.
const wikiTaxonomyPlanChunkSize = 60

const wikiTaxonomyEmptyTreeHint = "(none yet — this knowledge base has no folders, design a fresh directory)"

type wikiTaxonomyNode struct {
	children map[string]*wikiTaxonomyNode
}

func insertWikiTaxonomyPath(root *wikiTaxonomyNode, path []string) {
	if root == nil || len(path) == 0 {
		return
	}
	cur := root
	for _, part := range path {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if cur.children == nil {
			cur.children = make(map[string]*wikiTaxonomyNode)
		}
		child := cur.children[part]
		if child == nil {
			child = &wikiTaxonomyNode{}
			cur.children[part] = child
		}
		cur = child
	}
}

func appendWikiTaxonomyNode(buf *strings.Builder, label string, node *wikiTaxonomyNode, depth int) {
	if label != "" {
		fmt.Fprintf(buf, "%s%s\n", strings.Repeat("  ", depth), label)
	}
	if node == nil || len(node.children) == 0 {
		return
	}
	keys := make([]string, 0, len(node.children))
	for k := range node.children {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		appendWikiTaxonomyNode(buf, k, node.children[k], depth+1)
	}
}

// formatExistingTaxonomyForPrompt renders distinct category_path values as an
// indented folder tree for LLM extraction prompts.
func formatExistingTaxonomyForPrompt(paths [][]string) string {
	if len(paths) == 0 {
		return ""
	}
	root := &wikiTaxonomyNode{}
	for _, path := range paths {
		insertWikiTaxonomyPath(root, path)
	}
	if len(root.children) == 0 {
		return ""
	}
	var buf strings.Builder
	keys := make([]string, 0, len(root.children))
	for k := range root.children {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		appendWikiTaxonomyNode(&buf, k, root.children[k], 0)
	}
	return strings.TrimSpace(buf.String())
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
func (s *wikiIngestService) rebuildIndexPage(ctx context.Context, chatModel chat.Chat, payload WikiIngestPayload,
	changeDesc, lang, customInstructions string,
) error {
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
			"DocumentSummaries":  framing + docSummaries.String(),
			"Language":           lang,
			"CustomInstructions": customInstructions,
			"InstructionScope":   "wiki_content",
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
			"ExistingIntro":      existingIntro,
			"ChangeDescription":  changeDesc,
			"DocumentSummaries":  "",
			"Language":           lang,
			"CustomInstructions": customInstructions,
			"InstructionScope":   "wiki_content",
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
	logger.Infof(ctx, "wiki ingest: %d similar existing pages selected for %d new items",
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
		srcSlash := strings.Index(srcSlug, "/")
		dstSlash := strings.Index(dstSlug, "/")
		if srcSlash <= 0 || dstSlash <= 0 {
			// A type-prefixed slug must look like "entity/foo" or
			// "concept/bar". An LLM that emits an un-prefixed slug
			// here is hallucinating; reject rather than fall through
			// the prefix-equality check (which would treat both empty
			// prefixes as a match).
			logger.Warnf(ctx, "wiki ingest: dedup rejected %s → %s (missing type prefix)", srcSlug, dstSlug)
			return false
		}
		srcPrefix := srcSlug[:srcSlash+1]
		dstPrefix := dstSlug[:dstSlash+1]
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

	maskedData, urlMap := maskTemplateDataImageURLs(data)

	var buf strings.Builder
	if err := tmpl.Execute(&buf, maskedData); err != nil {
		return "", fmt.Errorf("execute template: %w", err)
	}

	prompt := buf.String()
	prompt = types.AppendCustomPromptInstructions(prompt, data["CustomInstructions"], data["InstructionScope"])
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
			return unmaskImageURLs(response.Content, urlMap), nil
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
	switch kn.ParseStatus {
	case types.ParseStatusDeleting, types.ParseStatusCancelled:
		return true
	}
	return false
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

	// 重叠去重与排序统一交给公共逻辑（按文本匹配，兼容补写表头 / HTML 实体）。
	return searchutil.MergeTextChunks(textChunks, "\n")
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

	// Markdown image references with the URL captured separately so LLM-bound
	// image URLs can be frozen while captions remain editable.
	mdImageURLRE = regexp.MustCompile(`!\[[^\]]*\]\(([^)]*)\)`)

	// Enriched image blocks store the original object URL as an attribute,
	// e.g. <image url="...">. Capture both double- and single-quoted forms.
	imageURLAttrRE = regexp.MustCompile(`(?i)<image\b[^>]*\surl\s*=\s*(?:"([^"]*)"|'([^']*)')`)

	imagePlaceholderTokenRE = regexp.MustCompile(`wkimg:[A-Za-z0-9_-]+`)
)

func maskTemplateDataImageURLs(data map[string]string) (map[string]string, map[string]string) {
	if len(data) == 0 {
		return data, nil
	}

	masked := make(map[string]string, len(data))
	urlToToken := make(map[string]string)
	tokenToURL := make(map[string]string)

	keys := make([]string, 0, len(data))
	for key := range data {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	for _, key := range keys {
		masked[key] = maskImageURLsWithState(data[key], urlToToken, tokenToURL)
	}

	return masked, tokenToURL
}

// maskImageURLs replaces image URLs with low-entropy placeholders. It only
// freezes URLs; alt/caption text remains in place for the LLM to edit.
func maskImageURLs(s string) (string, map[string]string) {
	urlToToken := make(map[string]string)
	tokenToURL := make(map[string]string)
	return maskImageURLsWithState(s, urlToToken, tokenToURL), tokenToURL
}

func maskImageURLsWithState(s string, urlToToken, tokenToURL map[string]string) string {
	urls := collectMaskableImageURLs(s)
	if len(urls) == 0 {
		return s
	}

	for _, url := range urls {
		if _, ok := urlToToken[url]; ok {
			continue
		}
		token := fmt.Sprintf("wkimg:%04d", len(tokenToURL)+1)
		urlToToken[url] = token
		tokenToURL[token] = url
	}

	replaceURLs := append([]string(nil), urls...)
	sort.SliceStable(replaceURLs, func(i, j int) bool {
		return len(replaceURLs[i]) > len(replaceURLs[j])
	})

	masked := s
	for _, url := range replaceURLs {
		masked = strings.ReplaceAll(masked, url, urlToToken[url])
	}
	return masked
}

func collectMaskableImageURLs(s string) []string {
	seen := make(map[string]struct{})
	var urls []string

	addURL := func(url string) {
		url = strings.TrimSpace(url)
		if url == "" {
			return
		}
		if _, ok := seen[url]; ok {
			return
		}
		seen[url] = struct{}{}
		urls = append(urls, url)
	}

	for _, match := range mdImageURLRE.FindAllStringSubmatch(s, -1) {
		addURL(match[1])
	}
	for _, match := range imageURLAttrRE.FindAllStringSubmatch(s, -1) {
		if match[1] != "" {
			addURL(match[1])
			continue
		}
		addURL(match[2])
	}

	return urls
}

// unmaskImageURLs restores known placeholders and drops any corrupted or
// invented image placeholders so broken image links never reach storage.
func unmaskImageURLs(out string, urlMap map[string]string) string {
	out = mdImageURLRE.ReplaceAllStringFunc(out, func(match string) string {
		parts := mdImageURLRE.FindStringSubmatch(match)
		if len(parts) != 2 {
			return match
		}
		url := strings.TrimSpace(parts[1])
		if realURL, ok := urlMap[url]; ok {
			idx := strings.LastIndex(match, "(")
			if idx < 0 {
				return match
			}
			return match[:idx+1] + realURL + ")"
		}
		if strings.HasPrefix(url, "wkimg:") {
			return ""
		}
		return match
	})

	return replaceImagePlaceholderTokensOutsideMarkdown(out, urlMap)
}

func replaceImagePlaceholderTokensOutsideMarkdown(s string, urlMap map[string]string) string {
	matches := mdImageURLRE.FindAllStringIndex(s, -1)
	if len(matches) == 0 {
		return replaceImagePlaceholderTokens(s, urlMap)
	}

	var b strings.Builder
	last := 0
	for _, match := range matches {
		if match[0] > last {
			b.WriteString(replaceImagePlaceholderTokens(s[last:match[0]], urlMap))
		}
		b.WriteString(s[match[0]:match[1]])
		last = match[1]
	}
	if last < len(s) {
		b.WriteString(replaceImagePlaceholderTokens(s[last:], urlMap))
	}
	return b.String()
}

func replaceImagePlaceholderTokens(s string, urlMap map[string]string) string {
	return imagePlaceholderTokenRE.ReplaceAllStringFunc(s, func(token string) string {
		if realURL, ok := urlMap[token]; ok {
			return realURL
		}
		return ""
	})
}

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
