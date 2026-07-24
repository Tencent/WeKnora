package router

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/application/service"
	"github.com/Tencent/WeKnora/internal/common"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/middleware/asynqdl"
	"github.com/Tencent/WeKnora/internal/tracing/langfuse"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/hibiken/asynq"
	"go.uber.org/dig"
)

type AsynqTaskParams struct {
	dig.In

	// Dedicated servers provide minimum capacity for parse, post-process,
	// enrichment, maintenance, and Wiki work. SharedServer is the elastic tier:
	// it also subscribes to core/enrichment queues so either stage can borrow
	// otherwise-idle capacity without consuming the other stage's guarantee.
	CoreServer           *asynq.Server `name:"coreAsynqServer"`
	PostProcessServer    *asynq.Server `name:"postProcessAsynqServer"`
	EnrichmentServer     *asynq.Server `name:"enrichmentAsynqServer"`
	MaintenanceServer    *asynq.Server `name:"maintenanceAsynqServer"`
	SharedServer         *asynq.Server `name:"sharedAsynqServer"`
	WikiServer           *asynq.Server `name:"wikiAsynqServer"`
	KnowledgeService     interfaces.KnowledgeService
	KnowledgeBaseService interfaces.KnowledgeBaseService
	TagService           interfaces.KnowledgeTagService
	DataSourceService    interfaces.DataSourceService
	ChunkExtractor       interfaces.TaskHandler `name:"chunkExtractor"`
	DataTableSummary     interfaces.TaskHandler `name:"dataTableSummary"`
	ImageMultimodal      interfaces.TaskHandler `name:"imageMultimodal"`
	KnowledgePostProcess interfaces.TaskHandler `name:"knowledgePostProcess"`
	WikiIngest           interfaces.TaskHandler `name:"wikiIngest"`
	TemporaryDocument    interfaces.TemporaryDocumentService
	DeadLetterRepo       interfaces.TaskDeadLetterRepository
	SpanTracker          service.SpanTracker
}

// defaultRedisOpTimeout is the previous hard-coded read timeout. The 100ms
// floor was tight enough to cause spurious i/o timeout errors during bursty
// workloads (large batch uploads, multimodal counter DECRs under load), so we
// raise the default to 500ms while still allowing operators to tune via env.
const defaultRedisOpTimeoutMs = 500

// readRedisOpTimeoutMs reads WEKNORA_REDIS_OP_TIMEOUT_MS, falling back to
// defaultRedisOpTimeoutMs on missing/invalid input. Kept as a separate helper
// so both ReadTimeout and WriteTimeout share the same source of truth.
func readRedisOpTimeoutMs() int {
	if v := strings.TrimSpace(os.Getenv("WEKNORA_REDIS_OP_TIMEOUT_MS")); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil && parsed > 0 {
			return parsed
		}
	}
	return defaultRedisOpTimeoutMs
}

func getAsynqRedisClientOpt() *asynq.RedisClientOpt {
	db := 0
	if dbStr := os.Getenv("REDIS_DB"); dbStr != "" {
		if parsed, err := strconv.Atoi(dbStr); err == nil {
			db = parsed
		}
	}
	timeoutMs := readRedisOpTimeoutMs()
	opt := &asynq.RedisClientOpt{
		Addr:        os.Getenv("REDIS_ADDR"),
		Username:    os.Getenv("REDIS_USERNAME"),
		Password:    os.Getenv("REDIS_PASSWORD"),
		ReadTimeout: time.Duration(timeoutMs) * time.Millisecond,
		// Writes are typically more sensitive to congestion than reads
		// (RESP pipelining, BRPOPLPUSH on Asynq dequeue), so we keep
		// WriteTimeout slightly larger to absorb head-of-line stalls.
		WriteTimeout: time.Duration(timeoutMs*2) * time.Millisecond,
		DB:           db,
		TLSConfig:    common.RedisTLSConfig(),
	}
	return opt
}

func NewAsyncqClient() (*asynq.Client, error) {
	opt := getAsynqRedisClientOpt()
	client := asynq.NewClient(opt)
	err := client.Ping()
	if err != nil {
		return nil, err
	}
	return client, nil
}

// wikiIngestRetryDelay is a fixed, short backoff for wiki ingest lock
// conflicts. Must be slightly longer than the active-lock TTL's worst-case
// "just got set" window so the retry is highly likely to succeed without
// burning through retries; but short enough that users don't feel the stall.
const wikiIngestRetryDelay = 15 * time.Second

// asynqRetryDelayFunc customizes per-task retry backoff.
//
// Default asynq backoff is exponential (≈10s, 40s, 90s, 2.5m, ...), which
// is appropriate for transient errors like remote HTTP failures. But for
// wiki ingest lock conflicts (ErrWikiIngestConcurrent), exponential
// backoff is harmful: a freshly orphaned lock expires in ≤60s, so a 15s
// fixed retry virtually guarantees the next attempt succeeds. Without
// this override, a crash-restart cycle can leave a KB unable to make
// progress for 7–10 minutes while the orphan lock expires AND the retry
// schedule catches up.
func asynqRetryDelayFunc(n int, e error, t *asynq.Task) time.Duration {
	if errors.Is(e, service.ErrWikiIngestConcurrent) {
		return wikiIngestRetryDelay
	}
	return asynq.DefaultRetryDelayFunc(n, e, t)
}

// Worker defaults live in types so server construction and runtime reporting
// cannot drift. The upstream budget is divided without increasing historical
// total capacity; Wiki remains separate because its model-heavy workload has a
// different provider-capacity profile.

// newAsynqServer builds an asynq server bound to a specific queue set and
// concurrency. Every worker pool uses it so Redis options and retry-delay
// policy stay consistent across the topology.
func newAsynqServer(concurrency int, queues map[string]int) *asynq.Server {
	opt := getAsynqRedisClientOpt()
	return asynq.NewServer(
		opt,
		asynq.Config{
			Concurrency:    concurrency,
			Queues:         queues,
			RetryDelayFunc: asynqRetryDelayFunc,
		},
	)
}

// backgroundTaskMiddleware tags every task's context as a background worker
// execution (types.WithBackgroundTask) so the per-model chat concurrency
// governor applies to ingestion/enrichment LLM calls but not to interactive
// user-facing chat.
func backgroundTaskMiddleware() asynq.MiddlewareFunc {
	return func(next asynq.Handler) asynq.Handler {
		return asynq.HandlerFunc(func(ctx context.Context, t *asynq.Task) error {
			return next.ProcessTask(types.WithBackgroundTask(ctx), t)
		})
	}
}

func resolveWorkerPoolConcurrency(svc interfaces.SystemSettingService) types.WorkerPoolConcurrency {
	if svc == nil {
		return types.DefaultWorkerPoolConcurrency()
	}
	ctx := context.Background()
	return types.ResolveWorkerPoolConcurrency(func(key, env string, fallback int) int {
		value := svc.GetInt(ctx, key, env, int64(fallback))
		return int(value)
	})
}

// NewCoreAsynqServer runs document and manual parsing with guaranteed capacity.
func NewCoreAsynqServer(svc interfaces.SystemSettingService) *asynq.Server {
	allocation := resolveWorkerPoolConcurrency(svc)
	log.Printf("asynq core-pool server starting with concurrency=%d total_upstream=%d redis_op_timeout=%dms",
		allocation.Core, allocation.UpstreamTotal(), readRedisOpTimeoutMs())
	return newAsynqServer(allocation.Core, types.QueueWeightsForPool(types.WorkerPoolCore))
}

// NewPostProcessAsynqServer reserves capacity for the lightweight but
// latency-sensitive fan-out stage. It cannot be trapped behind a burst of
// long-running document parses in QueueDefault.
func NewPostProcessAsynqServer(svc interfaces.SystemSettingService) *asynq.Server {
	allocation := resolveWorkerPoolConcurrency(svc)
	log.Printf("asynq postprocess-pool server starting with concurrency=%d total_upstream=%d",
		allocation.PostProcess, allocation.UpstreamTotal())
	return newAsynqServer(allocation.PostProcess, types.QueueWeightsForPool(types.WorkerPoolPostProcess))
}

// NewEnrichmentAsynqServer runs high-fanout summary, multimodal, graph, and
// question generation without consuming core parsing capacity.
func NewEnrichmentAsynqServer(svc interfaces.SystemSettingService) *asynq.Server {
	allocation := resolveWorkerPoolConcurrency(svc)
	log.Printf("asynq enrichment-pool server starting with concurrency=%d total_upstream=%d",
		allocation.Enrichment, allocation.UpstreamTotal())
	return newAsynqServer(allocation.Enrichment, types.QueueWeightsForPool(types.WorkerPoolEnrichment))
}

// NewMaintenanceAsynqServer runs connector sync, cleanup, deletion, and batch
// dispatch work. QueueMaintenance keeps the legacy Redis name "low", so old
// tasks are drained safely during rolling upgrades.
func NewMaintenanceAsynqServer(svc interfaces.SystemSettingService) *asynq.Server {
	allocation := resolveWorkerPoolConcurrency(svc)
	log.Printf("asynq maintenance-pool server starting with concurrency=%d total_upstream=%d",
		allocation.Maintenance, allocation.UpstreamTotal())
	return newAsynqServer(allocation.Maintenance, types.QueueWeightsForPool(types.WorkerPoolMaintenance))
}

// NewSharedAsynqServer is the elastic tier. Asynq dequeue is atomic, so it is
// safe for this server and the dedicated servers to subscribe to the same
// queues: every task is still executed by exactly one worker.
func NewSharedAsynqServer(svc interfaces.SystemSettingService) *asynq.Server {
	allocation := resolveWorkerPoolConcurrency(svc)
	log.Printf("asynq shared-pool server starting with concurrency=%d total_upstream=%d",
		allocation.Shared, allocation.UpstreamTotal())
	return newAsynqServer(allocation.Shared, types.QueueWeightsForSharedPool())
}

// NewWikiAsynqServer builds the dedicated wiki pool: QueueWiki only. It runs
// the shared handler mux but only ever pulls wiki tasks, so its concurrency
// budget (WEKNORA_WIKI_ASYNQ_CONCURRENCY, default 8) is spent exclusively on
// wiki generation. This is the hard capacity isolation that prevents the parse
// pipeline from starving wiki (and vice-versa) during concurrent uploads.
func NewWikiAsynqServer(svc interfaces.SystemSettingService) *asynq.Server {
	concurrency := resolveWorkerPoolConcurrency(svc).Wiki
	log.Printf("asynq wiki-pool server starting with concurrency=%d", concurrency)
	return newAsynqServer(concurrency, types.QueueWeightsForPool(types.WorkerPoolWiki))
}

func RunAsynqServer(params AsynqTaskParams) *asynq.ServeMux {
	// Create a new mux and register all handlers
	mux := asynq.NewServeMux()

	// Install the dead-letter middleware FIRST so it sees the raw error
	// returned by the handler, before any other middleware that might
	// transform it. The middleware records one task_dead_letters row per
	// task that exhausts its retry budget — operators can then SQL-query
	// failures by task type, scope, or tenant without scraping logs.
	// Best-effort: a DB failure is logged and swallowed; the original task
	// error always propagates upstream to asynq for retry/archival.
	//
	// The callback flips Knowledge.parse_status to "failed" the moment a
	// document-related task exhausts its retry budget. Without this hook,
	// a permanently-failing task left its parent knowledge stranded in
	// "processing" until housekeeping cron caught it minutes later — the
	// UI signal users actually see.
	knowledgeFailer := newDeadLetterKnowledgeFailer(params.KnowledgeService, params.SpanTracker)
	mux.Use(asynqdl.MiddlewareWithCallback(params.DeadLetterRepo, knowledgeFailer))

	// Mark every asynq worker execution as a background task so the chat
	// concurrency governor throttles ingestion/enrichment LLM traffic while
	// leaving user-facing interactive chat ungated. Installed early so all
	// downstream handlers (and their model calls) inherit the flag.
	mux.Use(backgroundTaskMiddleware())

	// Install Langfuse middleware BEFORE handler registration so every task
	// type is automatically wrapped. When Langfuse is disabled the middleware
	// is a pass-through; when enabled it resumes the upstream HTTP trace (if
	// the payload carries one) or opens a standalone trace, then wraps the
	// handler execution in a SPAN so all child generations (embedding / VLM /
	// chat / rerank / ASR) nest correctly in the Langfuse UI.
	mux.Use(langfuse.AsynqMiddleware())

	// Register extract handlers - router will dispatch to appropriate handler
	mux.HandleFunc(types.TypeChunkExtract, params.ChunkExtractor.Handle)
	mux.HandleFunc(types.TypeDataTableSummary, params.DataTableSummary.Handle)

	// Register document processing handler
	mux.HandleFunc(types.TypeDocumentProcess, params.KnowledgeService.ProcessDocument)
	mux.HandleFunc(types.TypeTemporaryDocumentProcess, params.TemporaryDocument.Process)

	// Register manual knowledge processing handler (cleanup + re-indexing)
	mux.HandleFunc(types.TypeManualProcess, params.KnowledgeService.ProcessManualUpdate)

	// Register FAQ import handler (includes dry run mode)
	mux.HandleFunc(types.TypeFAQImport, params.KnowledgeService.ProcessFAQImport)

	// Register question generation handler
	mux.HandleFunc(types.TypeQuestionGeneration, params.KnowledgeService.ProcessQuestionGeneration)

	// Register summary generation handler
	mux.HandleFunc(types.TypeSummaryGeneration, params.KnowledgeService.ProcessSummaryGeneration)

	// Register KB clone handler
	mux.HandleFunc(types.TypeKBClone, params.KnowledgeService.ProcessKBClone)

	// Register knowledge move handler
	mux.HandleFunc(types.TypeKnowledgeMove, params.KnowledgeService.ProcessKnowledgeMove)

	// Register knowledge list delete handler
	mux.HandleFunc(types.TypeKnowledgeListDelete, params.KnowledgeService.ProcessKnowledgeListDelete)

	// Register knowledge list reparse handler
	mux.HandleFunc(types.TypeKnowledgeListReparse, params.KnowledgeService.ProcessKnowledgeListReparse)

	// Register index delete handler
	mux.HandleFunc(types.TypeIndexDelete, params.TagService.ProcessIndexDelete)

	// Register KB delete handler
	mux.HandleFunc(types.TypeKBDelete, params.KnowledgeBaseService.ProcessKBDelete)

	// Register image multimodal handler
	mux.HandleFunc(types.TypeImageMultimodal, params.ImageMultimodal.Handle)

	// Register knowledge post process handler
	mux.HandleFunc(types.TypeKnowledgePostProcess, params.KnowledgePostProcess.Handle)

	// Register data source sync handler
	mux.HandleFunc(types.TypeDataSourceSync, params.DataSourceService.ProcessSync)

	// Register wiki ingest handler + the debounced KB-global finalize handler.
	// Both route to the same dispatch (WikiIngest.Handle switches on task type)
	// and both land on QueueWiki, so the dedicated wiki pool serves them.
	mux.HandleFunc(types.TypeWikiIngest, params.WikiIngest.Handle)
	mux.HandleFunc(types.TypeWikiFinalize, params.WikiIngest.Handle)

	// Run the same mux on every pool. Shared and dedicated servers intentionally
	// overlap, but Redis dequeue is atomic, so each task still executes once.
	runPool := func(name string, srv *asynq.Server) {
		go func() {
			if err := srv.Run(mux); err != nil {
				log.Fatalf("could not run %s asynq server: %v", name, err)
			}
		}()
	}
	runPool("core-pool", params.CoreServer)
	runPool("postprocess-pool", params.PostProcessServer)
	runPool("enrichment-pool", params.EnrichmentServer)
	runPool("maintenance-pool", params.MaintenanceServer)
	runPool("shared-pool", params.SharedServer)
	runPool("wiki-pool", params.WikiServer)
	return mux
}

// deadLetterKnowledgePayload extracts only the field we need from any
// document-related asynq payload. Kept narrow so we don't accidentally
// depend on the full payload schema and survive future field churn.
type deadLetterKnowledgePayload struct {
	KnowledgeID string `json:"knowledge_id,omitempty"`
	// Attempt threads through DocumentProcess / ManualProcess /
	// KnowledgePostProcess payloads (added when span tracking shipped)
	// — extracted here so the dead-letter callback can also close the
	// matching root span as failed. Older in-flight payloads without
	// this field decode as 0 and the tracker call no-ops.
	Attempt int `json:"attempt,omitempty"`
}

// taskTypesAffectingKnowledgeStatus enumerates the asynq task types whose
// dead-letter event should flip the parent Knowledge to "failed". Only
// terminal task types are listed here:
//
//   - TypeDocumentProcess: the entry point of the parsing pipeline.
//   - TypeImageMultimodal: a single image hitting dead-letter would have
//     been counted by isFinalAsynqAttempt (see image_multimodal.go), so
//     the parent might still complete via remaining images. We DO NOT mark
//     the parent failed for this case — finalize-on-last-attempt already
//     ensures progress.
//   - TypeKnowledgePostProcess: terminal stage; failure here strands the
//     knowledge in "processing".
//   - TypeManualProcess: same shape as DocumentProcess for re-indexing.
//
// Question/Summary generation are NOT included: they run after parse_status
// has already become "completed" and have their own status fields.
var taskTypesAffectingKnowledgeStatus = map[string]struct{}{
	types.TypeDocumentProcess:      {},
	types.TypeKnowledgePostProcess: {},
	types.TypeManualProcess:        {},
}

type deadLetterKnowledgeListDeletePayload struct {
	KnowledgeIDs []string `json:"knowledge_ids,omitempty"`
}

// newDeadLetterKnowledgeFailer returns the callback wired into the asynq
// dead-letter middleware. When a document-related task exhausts its retry
// budget, this callback marks the corresponding Knowledge row as failed so
// the UI surfaces the error instead of a perpetual spinner.
//
// All work is best-effort: missing payload, missing knowledge_id, or DB
// errors are logged and swallowed. The dead-letter record is the source of
// truth — this is purely a UX shortcut so users don't wait for the
// housekeeping cron's next sweep.
func newDeadLetterKnowledgeFailer(ks interfaces.KnowledgeService, tracker service.SpanTracker) asynqdl.OnDeadLetter {
	if ks == nil {
		return nil
	}
	repo := ks.GetRepository()
	if repo == nil {
		return nil
	}
	return func(ctx context.Context, t *asynq.Task, taskErr error) {
		if t == nil {
			return
		}
		if t.Type() == types.TypeKnowledgeListDelete {
			markKnowledgeListDeleteFailed(ctx, repo, t, taskErr)
			return
		}
		if _, ok := taskTypesAffectingKnowledgeStatus[t.Type()]; !ok {
			return
		}
		var probe deadLetterKnowledgePayload
		if err := json.Unmarshal(t.Payload(), &probe); err != nil || probe.KnowledgeID == "" {
			return
		}
		errMsg := "task " + t.Type() + " exhausted retries: " + taskErr.Error()
		// 8KB is the same cap the dead-letter row uses for last_error.
		if len(errMsg) > 8192 {
			errMsg = errMsg[:8192]
		}
		// Single UPDATE so we never end up with parse_status=failed but
		// stale error_message (or vice versa) when the second write
		// fails.
		if err := repo.UpdateKnowledgeColumns(ctx, probe.KnowledgeID, map[string]interface{}{
			"parse_status":  types.ParseStatusFailed,
			"error_message": errMsg,
		}); err != nil {
			logger.Warnf(ctx, "dead-letter callback: failed to mark knowledge %s as failed: %v", probe.KnowledgeID, err)
			return
		}
		// Close the matching root span so the timeline stops showing
		// "进行中" after dead-letter exhaustion. Best-effort: nil
		// tracker / missing attempt / missing root all no-op cleanly.
		if tracker != nil && probe.Attempt > 0 {
			tracker.FinalizeAttempt(ctx, probe.KnowledgeID, probe.Attempt,
				types.SpanStatusFailed, nil, "TASK_TIMEOUT", errMsg)
		}
		logger.Infof(ctx, "dead-letter callback: marked knowledge %s as failed (task=%s)", probe.KnowledgeID, t.Type())
	}
}

func markKnowledgeListDeleteFailed(
	ctx context.Context,
	repo interfaces.KnowledgeRepository,
	t *asynq.Task,
	taskErr error,
) {
	var payload deadLetterKnowledgeListDeletePayload
	if err := json.Unmarshal(t.Payload(), &payload); err != nil || len(payload.KnowledgeIDs) == 0 {
		return
	}
	errMsg := "delete task exhausted retries: " + taskErr.Error()
	if len(errMsg) > 8192 {
		errMsg = errMsg[:8192]
	}
	for _, knowledgeID := range payload.KnowledgeIDs {
		if knowledgeID == "" {
			continue
		}
		updated, err := repo.UpdateActiveDeletingKnowledgeColumns(ctx, knowledgeID, map[string]interface{}{
			"parse_status":  types.ParseStatusFailed,
			"error_message": errMsg,
		})
		if err != nil {
			logger.Warnf(ctx, "dead-letter callback: failed to mark delete failure for knowledge %s: %v", knowledgeID, err)
			continue
		}
		if !updated {
			logger.Infof(ctx, "dead-letter callback: skipped marking knowledge %s after delete task exhaustion because it is no longer active deleting", knowledgeID)
			continue
		}
		logger.Infof(ctx, "dead-letter callback: marked knowledge %s as failed after delete task exhausted retries", knowledgeID)
	}
}
