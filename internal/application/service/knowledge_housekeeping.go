// Package service: knowledge housekeeping.
//
// HousekeepingService periodically scans for knowledge rows that have been
// stuck in "processing" longer than any reasonable execution window and
// marks them as failed. This is the safety net that catches anything the
// other defences (asynq retry, dead-letter callback, image_multimodal
// finalize-on-last-attempt) miss — for example:
//
//   - Worker process killed mid-handler before any defer could run.
//   - DocReader call genuinely exceeding DocReaderCallTimeout AND the
//     worker subsequently being lost before retry kicks in.
//   - Multimodal Redis counter set to N but ALL N image tasks failing in
//     ways that bypass finalize (extremely rare; defence-in-depth here).
//
// Without this sweep, a single unlucky failure mode can leave a knowledge
// row in "processing" forever — invisible to users except as a permanent
// spinner. With this sweep the worst-case latency from stall to user-
// visible failure is bounded to ~1 stale-threshold + 1 sweep interval.
package service

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	apprepo "github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/config"
	werrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/robfig/cron/v3"
	"gorm.io/gorm"
)

// HousekeepingService runs background sweeps to recover stuck rows.
type HousekeepingService struct {
	db   *gorm.DB
	cfg  *config.Config
	cron *cron.Cron

	// inspector lets the sweep distinguish a genuinely orphaned row from
	// one whose enrichment subtasks are merely backlogged behind a busy
	// queue (no span heartbeat yet because no worker has picked them up).
	// nil-safe — a nil inspector disables only the transient queue check.
	// Durable Wiki ownership in task_pending_ops is always probed through db,
	// so the sweep never falls back to the span/updated_at heuristics alone.
	inspector interfaces.TaskInspector
	// task re-arms the wiki trigger for rows kept alive only by a durable
	// Wiki op, which a lost trigger would otherwise strand. nil disables it.
	task interfaces.TaskEnqueuer

	mu      sync.Mutex
	started bool

	kickMu    sync.Mutex
	wikiKicks map[string]time.Time

	queuedMu  sync.Mutex
	queuedIDs map[string]struct{}
	queuedAt  time.Time
}

// queuedProbeTTL bounds how often QueuedWork rescans the queue; clients poll
// far more often than a backlog changes.
const queuedProbeTTL = time.Minute

// NewHousekeepingService constructs a HousekeepingService. It does NOT start
// the cron — call Start in the application bootstrap so a misconfigured
// cron schedule cannot prevent the rest of the service from coming up.
func NewHousekeepingService(
	db *gorm.DB, cfg *config.Config, inspector interfaces.TaskInspector, task interfaces.TaskEnqueuer,
) *HousekeepingService {
	return &HousekeepingService{
		db:        db,
		cfg:       cfg,
		inspector: inspector,
		task:      task,
		wikiKicks: make(map[string]time.Time),
		cron: cron.New(cron.WithSeconds(), cron.WithChain(
			cron.Recover(cron.DefaultLogger),
		)),
	}
}

// Start registers the sweep schedule and begins the background runner.
// Idempotent — repeated calls are a no-op so wiring code can call Start
// without coordinating ordering.
func (h *HousekeepingService) Start(ctx context.Context) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.started {
		return nil
	}
	if !housekeepingEnabled() {
		logger.Infof(ctx, "[Housekeeping] disabled via WEKNORA_HOUSEKEEPING_ENABLED=false")
		return nil
	}
	// Every 5 minutes — frequent enough that user-visible recovery latency
	// is acceptable, infrequent enough that the SQL sweep is invisible to
	// query load even on large knowledge tables.
	if _, err := h.cron.AddFunc("0 */5 * * * *", func() {
		// Use Background so a cancelled bootstrap ctx doesn't stop sweeps.
		h.runSweep(context.Background())
	}); err != nil {
		return err
	}
	h.cron.Start()
	h.started = true
	logger.Infof(ctx, "[Housekeeping] started with 5-minute sweep")
	return nil
}

// Stop halts the cron and waits for in-flight sweeps to finish.
func (h *HousekeepingService) Stop() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.started {
		return
	}
	c := h.cron.Stop()
	<-c.Done()
	h.started = false
}

// runSweep is exported on the type for testability — tests can drive a
// single sweep without waiting for the cron tick.
func (h *HousekeepingService) runSweep(ctx context.Context) {
	threshold := h.staleThreshold()
	cutoff := time.Now().Add(-threshold)

	// Sweep A: knowledge stuck in "pending", "processing", or "finalizing".
	//
	// Two-stage check is critical here: knowledge.updated_at advances
	// only at parse_status transitions, but a long stage (DocReader on
	// a 500MB PDF, embedding 5K chunks) can run for an hour with no
	// status change. Using updated_at alone would falsely kill that
	// run. So we OR-combine knowledge.updated_at with the most recent
	// span row's updated_at — every Begin/End/Fail/Skip from
	// SpanTracker bumps the span row, so an actively-progressing
	// pipeline always has a recent span heartbeat even when the
	// parent knowledge row is "frozen" mid-stage.
	//
	// Knowledge rows with no spans at all (lite mode, in-flight tasks
	// from before this code shipped) fall back to the simple
	// updated_at check — they have no heartbeat to consult.
	// Include 'pending' so a task whose enqueue was lost is eventually
	// recovered too; filterOutQueued below protects legitimately backlogged
	// tasks. Include 'finalizing' alongside 'processing': finalizing rows still
	// consume LLM compute via enrichment subtasks (summary/question/graph),
	// and the same stall modes (subtask worker dies, retry budget exhausted
	// without decrementing the counter) leave the row hanging just as
	// visibly. Housekeeping promotes both states to 'failed' once the
	// span heartbeat is older than the threshold.
	var candidates []types.Knowledge
	if err := h.db.WithContext(ctx).
		Where("parse_status IN ? AND updated_at < ?",
			[]string{types.ParseStatusPending, types.ParseStatusProcessing, types.ParseStatusFinalizing}, cutoff).
		Find(&candidates).Error; err != nil {
		logger.Warnf(ctx, "[Housekeeping] knowledge candidate query failed: %v", err)
		return
	}

	stuck, heartbeat := h.filterByLastSpanActivity(ctx, candidates, cutoff)
	spanSkipped := len(candidates) - len(stuck)

	// Second-stage gate: a row can have a stale span heartbeat yet still
	// be perfectly healthy when its enrichment subtasks (summary /
	// question / graph / wiki) are merely backlogged behind a busy queue
	// — no worker has picked them up, so no span has been written since
	// post-process fanned them out. Killing such a row is the false-
	// positive users hit under heavy upload bursts. Drop any candidate
	// that still has a queued/active task referencing it; only rows with
	// nothing left in the queue are treated as genuinely orphaned.
	stuck, queueSkipped, wikiHeld := h.filterOutQueued(ctx, stuck)
	h.rearmWikiTriggers(ctx, wikiHeld, threshold)

	if len(stuck) > 0 {
		if recovered := h.recoverStalled(ctx, stuck, heartbeat, threshold); recovered > 0 {
			logger.Infof(ctx, "[Housekeeping] recovered %d stuck knowledge rows (threshold=%s)",
				recovered, threshold)
		}
	}
	if spanSkipped > 0 {
		// Visibility into "we considered killing N rows but their
		// span tree showed they're still progressing". Ops can grep
		// for this if they suspect housekeeping over- or under-fires.
		logger.Infof(ctx,
			"[Housekeeping] %d candidate(s) skipped — span heartbeat within threshold",
			spanSkipped)
	}
	if queueSkipped > 0 {
		// Visibility into "stale span heartbeat but tasks still queued"
		// — i.e. backpressure, not a stuck row. Persistent counts here
		// mean the queue is the bottleneck (raise the matching per-pool or
		// shared asynq concurrency, or document_process_timeout), not that
		// housekeeping misfires.
		logger.Infof(ctx,
			"[Housekeeping] %d candidate(s) skipped — tasks still queued (backpressure, not stuck)",
			queueSkipped)
	}

	// Sweep B: knowledge summary stuck. Summary is post-parse; threshold
	// is shorter because summary tasks are bounded by a single LLM call.
	// No span heartbeat exists for the summary stage (it lives in a
	// downstream asynq task), so we accept the original simple check.
	summaryCutoff := time.Now().Add(-1 * time.Hour)
	resSummary := h.db.WithContext(ctx).Model(&types.Knowledge{}).
		Where("summary_status = ? AND updated_at < ?", types.SummaryStatusProcessing, summaryCutoff).
		Update("summary_status", types.SummaryStatusFailed)
	if resSummary.Error != nil {
		logger.Warnf(ctx, "[Housekeeping] summary sweep failed: %v", resSummary.Error)
	} else if resSummary.RowsAffected > 0 {
		logger.Infof(ctx, "[Housekeeping] recovered %d stuck summary rows", resSummary.RowsAffected)
	}

	// Sweep C: rows stuck in "deleting" whose delete task no longer exists
	// (issues #3338/#3345). The delete dead-letter callback only recovers
	// rows that are still "deleting" when retries exhaust; a worker death
	// mid-delete or a lost queue leaves the row hidden from the document
	// list forever. Same recovery contract as Sweep A: flip to failed with
	// a clear error so the row becomes visible and actionable (the user's
	// delete intent can then be retried; the delete executor is idempotent
	// and plans by row ID, so a late-arriving task still finishes cleanly).
	//
	// Liveness gate: a backlogged delete is not stranded — probe the queue
	// for a live knowledge:list_delete covering the row before touching it.
	// A nil inspector (nothing wired at all) defers every candidate to the
	// next sweep: we cannot tell backlog from orphan without it, and
	// wrongly failing a live delete is visible to users, while waiting one
	// more interval is not. A probe error defers that single row.
	if h.inspector != nil {
		var strandedDeletes []types.Knowledge
		if err := h.db.WithContext(ctx).
			Where("parse_status = ? AND updated_at < ?", types.ParseStatusDeleting, cutoff).
			Find(&strandedDeletes).Error; err != nil {
			logger.Warnf(ctx, "[Housekeeping] deleting candidate query failed: %v", err)
		} else {
			recoveredDeletes := int64(0)
			for _, k := range strandedDeletes {
				queued, err := h.inspector.HasQueuedDeleteTasksForKnowledge(ctx, k.ID)
				if err != nil {
					logger.Warnf(ctx,
						"[Housekeeping] delete-task probe failed for %s: %v (deferring to next sweep)",
						k.ID, err)
					continue
				}
				if queued {
					continue
				}
				res := h.db.WithContext(ctx).Model(&types.Knowledge{}).
					Where("id = ? AND parse_status = ?", k.ID, types.ParseStatusDeleting).
					Updates(map[string]interface{}{
						"parse_status":  types.ParseStatusFailed,
						"error_message": "delete task stranded (no queued/active delete task) > " + threshold.String() + ", recovered by housekeeping",
					})
				if res.Error != nil {
					logger.Warnf(ctx, "[Housekeeping] delete sweep update failed for %s: %v", k.ID, res.Error)
					continue
				}
				recoveredDeletes += res.RowsAffected
			}
			if recoveredDeletes > 0 {
				logger.Infof(ctx,
					"[Housekeeping] recovered %d stranded deleting row(s) (threshold=%s)",
					recoveredDeletes, threshold)
			}
		}
	}
}

// filterByLastSpanActivity returns the subset of candidates whose most
// recent span row predates `cutoff` — i.e. genuinely stuck. Candidates
// with no span rows at all also pass through (they're lite-mode or
// pre-instrumentation tasks; the simple updated_at check already proved
// them stuck and we have no heartbeat to override that).
func (h *HousekeepingService) filterByLastSpanActivity(
	ctx context.Context, candidates []types.Knowledge, cutoff time.Time,
) ([]types.Knowledge, map[string]time.Time) {
	if len(candidates) == 0 {
		return candidates, nil
	}
	ids := make([]string, 0, len(candidates))
	for _, k := range candidates {
		ids = append(ids, k.ID)
	}

	// We scan MAX(updated_at) as string then parse client-side. That
	// dodges the SQLite driver's well-known refusal to auto-convert
	// aggregate datetime values into time.Time on its own — Postgres
	// happily round-trips, but the same query shape must work in
	// Lite mode too. Since we only compare against a cutoff, the
	// parse layer below tries the formats both Postgres and SQLite
	// emit and takes the first that parses.
	type spanHeartbeat struct {
		KnowledgeID string `gorm:"column:knowledge_id"`
		LastSeen    string `gorm:"column:last_seen"`
	}
	var beats []spanHeartbeat
	err := h.db.WithContext(ctx).
		Table("knowledge_processing_spans").
		Select("knowledge_id, MAX(updated_at) AS last_seen").
		Where("knowledge_id IN ?", ids).
		Group("knowledge_id").
		Find(&beats).Error
	if err != nil {
		// On query failure, fail safe — assume nothing has a
		// heartbeat (so all candidates are "stuck"). This matches
		// the previous-version behaviour and never under-recovers.
		logger.Warnf(ctx, "[Housekeeping] span heartbeat query failed: %v (will fail safe and recover all candidates)", err)
		return candidates, nil
	}
	heartbeat := make(map[string]time.Time, len(beats))
	for _, b := range beats {
		if t, ok := apprepo.ParseAggregateTime(b.LastSeen); ok {
			heartbeat[b.KnowledgeID] = t
		}
	}

	out := candidates[:0]
	for _, k := range candidates {
		if last, ok := heartbeat[k.ID]; ok && last.After(cutoff) {
			// Active span heartbeat — leave alone.
			continue
		}
		out = append(out, k)
	}
	return out, heartbeat
}

// recoverStalled fails each stuck row with a message naming where it stalled
// and when it last made progress, and closes its open spans so the timeline
// points at that spot instead of spinning. heartbeat is nil when the span
// heartbeat query failed; the message then gives no time rather than the
// row's updated_at, which can predate the last span write. Returns rows
// recovered.
func (h *HousekeepingService) recoverStalled(
	ctx context.Context, stuck []types.Knowledge, heartbeat map[string]time.Time, threshold time.Duration,
) int64 {
	ids := make([]string, 0, len(stuck))
	for _, k := range stuck {
		ids = append(ids, k.ID)
	}
	sites := h.locateStalls(ctx, ids)

	var recovered int64
	for _, k := range stuck {
		site := sites[k.ID]
		msg := stallMessage(k, site, heartbeat, threshold)
		res := h.db.WithContext(ctx).Model(&types.Knowledge{}).
			Where("id = ? AND parse_status IN ?", k.ID,
				[]string{types.ParseStatusPending, types.ParseStatusProcessing, types.ParseStatusFinalizing}).
			Updates(map[string]interface{}{
				"parse_status":           types.ParseStatusFailed,
				"error_message":          msg,
				"pending_subtasks_count": 0,
			})
		if res.Error != nil {
			logger.Warnf(ctx, "[Housekeeping] knowledge sweep update failed for %s: %v", k.ID, res.Error)
			continue
		}
		if res.RowsAffected == 0 {
			continue
		}
		recovered += res.RowsAffected
		if site != nil {
			h.closeStalledSpans(ctx, k.ID, site, msg)
		}
	}
	return recovered
}

// stallSite is where a stuck row stopped, read from its latest attempt.
type stallSite struct {
	// stages the run stalled in, in pipeline order.
	stages []string
	// tasks names the stalled spans when they are below a finished stage,
	// e.g. postprocess.summary while the row sits in finalizing.
	tasks []string
	// open holds the attempt's pending/running spans; those in failed are
	// marked failed, the rest cancelled.
	open   []types.KnowledgeProcessingSpan
	failed map[int64]bool
}

// locateStalls finds each row's stall site. The stalled spans are the running
// stages; when no stage is running (post-process closes its stage once the
// enrichment tasks are fanned out) they are the innermost running spans, and
// the stage is the one they belong to. Best-effort: a query error yields no
// sites and the rows are still failed.
func (h *HousekeepingService) locateStalls(ctx context.Context, ids []string) map[string]*stallSite {
	var rows []types.KnowledgeProcessingSpan
	if err := h.db.WithContext(ctx).
		Select("id", "knowledge_id", "attempt", "span_id", "parent_span_id", "name", "kind", "status", "started_at").
		Where("knowledge_id IN ?", ids).
		Order("id").
		Find(&rows).Error; err != nil {
		logger.Warnf(ctx, "[Housekeeping] stalled span query failed: %v", err)
		return nil
	}
	latest := make(map[string]int, len(ids))
	for _, r := range rows {
		if r.Attempt > latest[r.KnowledgeID] {
			latest[r.KnowledgeID] = r.Attempt
		}
	}
	byKnowledge := make(map[string][]types.KnowledgeProcessingSpan, len(ids))
	for _, r := range rows {
		if r.Attempt == latest[r.KnowledgeID] {
			byKnowledge[r.KnowledgeID] = append(byKnowledge[r.KnowledgeID], r)
		}
	}
	sites := make(map[string]*stallSite, len(byKnowledge))
	for kid, spans := range byKnowledge {
		sites[kid] = stallSiteOf(spans)
	}
	return sites
}

func stallSiteOf(spans []types.KnowledgeProcessingSpan) *stallSite {
	bySpanID := make(map[string]types.KnowledgeProcessingSpan, len(spans))
	runningChildren := make(map[string]int)
	for _, sp := range spans {
		bySpanID[sp.SpanID] = sp
		if sp.Status == types.SpanStatusRunning && sp.ParentSpanID != "" {
			runningChildren[sp.ParentSpanID]++
		}
	}
	stageOf := func(sp types.KnowledgeProcessingSpan) string {
		for depth := 0; depth < 64; depth++ {
			if sp.Kind == types.SpanKindStage {
				return sp.Name
			}
			parent, ok := bySpanID[sp.ParentSpanID]
			if !ok {
				return ""
			}
			sp = parent
		}
		return ""
	}

	site := &stallSite{failed: make(map[int64]bool)}
	var stalled []types.KnowledgeProcessingSpan
	for _, sp := range spans {
		if sp.Status == types.SpanStatusPending || sp.Status == types.SpanStatusRunning {
			site.open = append(site.open, sp)
		}
		if sp.Status == types.SpanStatusRunning && sp.Kind == types.SpanKindStage {
			stalled = append(stalled, sp)
		}
	}
	if len(stalled) == 0 {
		for _, sp := range spans {
			innermost := runningChildren[sp.SpanID] == 0
			if sp.Status == types.SpanStatusRunning && sp.Kind != types.SpanKindRoot && innermost {
				stalled = append(stalled, sp)
				site.tasks = append(site.tasks, sp.Name)
			}
		}
	}
	seen := make(map[string]bool)
	for _, sp := range stalled {
		site.failed[sp.ID] = true
		if stage := stageOf(sp); stage != "" {
			seen[stage] = true
		}
	}
	for _, stage := range types.AllStages {
		if seen[stage] {
			site.stages = append(site.stages, stage)
		}
	}
	return site
}

// stallMessage is the error_message for a recovered row, e.g. "task stuck in
// finalizing at postprocess stage (postprocess.summary): no progress since
// 2026-09-22T09:37:54Z (> 2h10m0s), recovered by housekeeping".
func stallMessage(
	k types.Knowledge, site *stallSite, heartbeat map[string]time.Time, threshold time.Duration,
) string {
	where := ""
	if site != nil && len(site.stages) > 0 {
		where = " at " + strings.Join(site.stages, "/") + " stage"
		if len(site.tasks) > 0 {
			tasks := site.tasks
			if len(tasks) > 3 {
				tasks = append(tasks[:3:3], "…")
			}
			where += " (" + strings.Join(tasks, ", ") + ")"
		}
	}
	if heartbeat == nil {
		return fmt.Sprintf("task stuck in %s%s: no progress for > %s, recovered by housekeeping",
			k.ParseStatus, where, threshold)
	}
	last := k.UpdatedAt
	if beat, ok := heartbeat[k.ID]; ok && beat.After(last) {
		last = beat
	}
	return fmt.Sprintf("task stuck in %s%s: no progress since %s (> %s), recovered by housekeeping",
		k.ParseStatus, where, last.UTC().Format(time.RFC3339), threshold)
}

// closeStalledSpans fails the site's stalled spans and cancels its other open
// spans, all with TASK_STALLED and a duration, the way FailSpan closes a span.
// Best-effort: the row is already failed.
func (h *HousekeepingService) closeStalledSpans(
	ctx context.Context, knowledgeID string, site *stallSite, msg string,
) {
	now := time.Now()
	for _, sp := range site.open {
		status := types.SpanStatusCancelled
		if site.failed[sp.ID] {
			status = types.SpanStatusFailed
		}
		var durationMs int64
		if sp.StartedAt != nil {
			durationMs = now.Sub(*sp.StartedAt).Milliseconds()
		}
		if err := h.db.WithContext(ctx).Model(&types.KnowledgeProcessingSpan{}).
			Where("id = ? AND status IN ?", sp.ID, []string{types.SpanStatusPending, types.SpanStatusRunning}).
			Updates(map[string]interface{}{
				"status":        status,
				"error_code":    werrors.ErrCodeTaskStalled,
				"error_message": msg,
				"finished_at":   now,
				"duration_ms":   durationMs,
				"updated_at":    now,
			}).Error; err != nil {
			logger.Warnf(ctx, "[Housekeeping] close stalled span %s of %s failed: %v", sp.SpanID, knowledgeID, err)
		}
	}
}

// filterOutQueued returns the subset of candidates that have NO work left
// in either the durable pending-op table or the queue backend, plus a count
// of how many were dropped because work still references them. A dropped
// candidate is "backlogged, not orphaned" — its enrichment subtasks are
// waiting for a worker, so the missing span heartbeat is expected and
// recovering it would be a false positive.
//
// The asynq inspector alone cannot answer this for Wiki. Wiki ingest work is
// durable per document in task_pending_ops (dedup_key = knowledge ID), while
// asynq only carries a per-KB wake-up trigger whose payload has no knowledge
// ID — and TypeWikiIngest is deliberately absent from the inspector's
// taskTypesForKnowledgeCancel set anyway. So HasQueuedTasksForKnowledge
// structurally reports "nothing queued" for a document whose only outstanding
// work is a queued Wiki ingest, and the sweep force-fails a perfectly healthy
// row. Probe the durable table directly before consulting the inspector.
//
// When no inspector is wired (nil) the durable gate still applies; only the
// transient queue check is skipped. On probe error we fail safe, but in
// opposite directions by design: an inspector error KEEPS the candidate as
// stuck (matching the span heartbeat query), whereas a durable-table error
// DEFERS every candidate to the next sweep — we cannot tell backlog from
// orphan without it, and wrongly failing a live document is not recoverable
// by the user, while waiting one more interval is.
func (h *HousekeepingService) filterOutQueued(
	ctx context.Context, candidates []types.Knowledge,
) (kept []types.Knowledge, skipped int, wikiHeld []types.Knowledge) {
	if len(candidates) == 0 {
		return candidates, 0, nil
	}

	ids := make([]string, 0, len(candidates))
	for _, k := range candidates {
		ids = append(ids, k.ID)
	}
	var durableIDs []string
	if err := h.db.WithContext(ctx).
		Model(&types.TaskPendingOp{}).
		Where("task_type = ? AND scope = ? AND op = ? AND dedup_key IN ?",
			wikiTaskType, wikiTaskScope, WikiOpIngest, ids).
		Distinct("dedup_key").
		Pluck("dedup_key", &durableIDs).Error; err != nil {
		logger.Warnf(ctx,
			"[Housekeeping] durable queue probe failed: %v (deferring %d candidate(s))",
			err, len(candidates))
		return candidates[:0], len(candidates), nil
	}
	durable := make(map[string]struct{}, len(durableIDs))
	for _, id := range durableIDs {
		durable[id] = struct{}{}
	}

	out := candidates[:0]
	for _, k := range candidates {
		if _, ok := durable[k.ID]; ok {
			skipped++
			wikiHeld = append(wikiHeld, k)
			continue
		}
		if h.inspector == nil {
			out = append(out, k)
			continue
		}
		queued, err := h.inspector.HasQueuedTasksForKnowledge(ctx, k.ID)
		if err != nil {
			logger.Warnf(ctx,
				"[Housekeeping] queue probe failed for %s: %v (will fail safe and treat as stuck)", k.ID, err)
			out = append(out, k)
			continue
		}
		if queued {
			skipped++
			continue
		}
		out = append(out, k)
	}
	return out, skipped, wikiHeld
}

// QueuedWork reports which of ids still have work waiting in the queue or in
// the durable Wiki table, i.e. are backlogged rather than stuck, using the
// same probes as the sweep. The queue side is one shared scan (see
// queuedSnapshot), so answering a whole page costs one DB query. An error
// means the answer is unknown.
func (h *HousekeepingService) QueuedWork(ctx context.Context, ids []string) (map[string]bool, error) {
	out := make(map[string]bool, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	var durableIDs []string
	if err := h.db.WithContext(ctx).
		Model(&types.TaskPendingOp{}).
		Where("task_type = ? AND scope = ? AND op = ? AND dedup_key IN ?",
			wikiTaskType, wikiTaskScope, WikiOpIngest, ids).
		Distinct("dedup_key").
		Pluck("dedup_key", &durableIDs).Error; err != nil {
		return nil, fmt.Errorf("durable queue probe: %w", err)
	}
	queued, err := h.queuedSnapshot(ctx)
	if err != nil {
		return nil, err
	}
	for _, id := range ids {
		_, out[id] = queued[id]
	}
	for _, id := range durableIDs {
		out[id] = true
	}
	return out, nil
}

// queuedSnapshot returns the knowledge IDs referenced by queued tasks. One
// scan of the whole queue serves every caller for queuedProbeTTL; concurrent
// callers wait for the scan in flight rather than starting their own. A
// failed scan is not cached.
func (h *HousekeepingService) queuedSnapshot(ctx context.Context) (map[string]struct{}, error) {
	if h.inspector == nil {
		return nil, nil
	}
	h.queuedMu.Lock()
	defer h.queuedMu.Unlock()
	if h.queuedIDs != nil && time.Since(h.queuedAt) < queuedProbeTTL {
		return h.queuedIDs, nil
	}
	ids, err := h.inspector.QueuedKnowledgeIDs(ctx)
	if err != nil {
		return nil, fmt.Errorf("queue probe: %w", err)
	}
	h.queuedIDs, h.queuedAt = ids, time.Now()
	return ids, nil
}

// rearmWikiTriggers enqueues one wiki ingest trigger per KB whose stale rows
// are held only by durable Wiki ops. The trigger is ephemeral: once lost (or
// archived after its retries), nothing wakes the consumer until a restart or
// another upload, and the row sits in "finalizing" indefinitely. A KB is
// re-armed at most once per threshold so a genuine backlog is not flooded.
func (h *HousekeepingService) rearmWikiTriggers(
	ctx context.Context, held []types.Knowledge, threshold time.Duration,
) {
	if h.task == nil || len(held) == 0 {
		return
	}
	h.kickMu.Lock()
	defer h.kickMu.Unlock()
	now := time.Now()
	for kbID, last := range h.wikiKicks {
		if now.Sub(last) >= threshold {
			delete(h.wikiKicks, kbID)
		}
	}
	rearmed := 0
	for _, k := range held {
		if k.KnowledgeBaseID == "" {
			continue
		}
		if _, recent := h.wikiKicks[k.KnowledgeBaseID]; recent {
			continue
		}
		triggerCtx := ctx
		if lang := WikiPendingLanguage(ctx, h.db, k.TenantID, k.KnowledgeBaseID); lang != "" {
			triggerCtx = context.WithValue(ctx, types.LanguageContextKey, lang)
		}
		if err := enqueueWikiIngestTrigger(triggerCtx, h.task, k.TenantID, k.KnowledgeBaseID); err != nil {
			logger.Warnf(ctx, "[Housekeeping] re-arm wiki trigger for KB %s failed: %v", k.KnowledgeBaseID, err)
			continue
		}
		h.wikiKicks[k.KnowledgeBaseID] = now
		rearmed++
	}
	if rearmed > 0 {
		logger.Infof(ctx, "[Housekeeping] re-armed wiki ingest trigger for %d knowledge base(s)", rearmed)
	}
}

// staleThreshold returns how long a "processing" row may sit untouched
// before housekeeping treats it as orphaned. The floor is 1 hour so that a
// genuinely slow large-PDF parse cannot be killed mid-flight; the ceiling
// scales with the operator-configured DocumentProcessTimeout plus 10 minute
// buffer to absorb scheduling jitter.
func (h *HousekeepingService) staleThreshold() time.Duration {
	base := 1 * time.Hour
	if h.cfg != nil && h.cfg.KnowledgeBase != nil && h.cfg.KnowledgeBase.DocumentProcessTimeout > base {
		base = h.cfg.KnowledgeBase.DocumentProcessTimeout
	}
	return base + 10*time.Minute
}

func housekeepingEnabled() bool {
	// Default-on: missing/empty env enables the sweep. Operators must
	// explicitly set "false" to opt out, matching the plan's commitment
	// that no env change is required for the safety net to engage.
	v := strings.TrimSpace(os.Getenv("WEKNORA_HOUSEKEEPING_ENABLED"))
	if v == "" {
		return true
	}
	switch strings.ToLower(v) {
	case "0", "false", "off", "no":
		return false
	}
	return true
}
