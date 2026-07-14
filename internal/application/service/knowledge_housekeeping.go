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
	"os"
	"strings"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/config"
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
	// nil-safe — a nil inspector disables the queue check and the sweep
	// falls back to the span/updated_at heuristics alone.
	inspector interfaces.TaskInspector

	mu      sync.Mutex
	started bool
}

// NewHousekeepingService constructs a HousekeepingService. It does NOT start
// the cron — call Start in the application bootstrap so a misconfigured
// cron schedule cannot prevent the rest of the service from coming up.
func NewHousekeepingService(
	db *gorm.DB, cfg *config.Config, inspector interfaces.TaskInspector,
) *HousekeepingService {
	return &HousekeepingService{
		db:        db,
		cfg:       cfg,
		inspector: inspector,
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

	stuck := h.filterByLastSpanActivity(ctx, candidates, cutoff)
	spanSkipped := len(candidates) - len(stuck)

	// Second-stage gate: a row can have a stale span heartbeat yet still
	// be perfectly healthy when its enrichment subtasks (summary /
	// question / graph / wiki) are merely backlogged behind a busy queue
	// — no worker has picked them up, so no span has been written since
	// post-process fanned them out. Killing such a row is the false-
	// positive users hit under heavy upload bursts. Drop any candidate
	// that still has a queued/active task referencing it; only rows with
	// nothing left in the queue are treated as genuinely orphaned.
	stuck, queueSkipped := h.filterOutQueued(ctx, stuck)

	if len(stuck) > 0 {
		stuckIDs := make([]string, 0, len(stuck))
		for _, k := range stuck {
			stuckIDs = append(stuckIDs, k.ID)
		}
		res := h.db.WithContext(ctx).Model(&types.Knowledge{}).
			Where("id IN ? AND parse_status IN ?", stuckIDs,
				[]string{types.ParseStatusPending, types.ParseStatusProcessing, types.ParseStatusFinalizing}).
			Updates(map[string]interface{}{
				"parse_status":           types.ParseStatusFailed,
				"error_message":          "task stuck in processing > " + threshold.String() + ", recovered by housekeeping",
				"pending_subtasks_count": 0,
			})
		if res.Error != nil {
			logger.Warnf(ctx, "[Housekeeping] knowledge sweep update failed: %v", res.Error)
		} else if res.RowsAffected > 0 {
			logger.Infof(ctx, "[Housekeeping] recovered %d stuck knowledge rows (threshold=%s)",
				res.RowsAffected, threshold)
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
}

// filterByLastSpanActivity returns the subset of candidates whose most
// recent span row predates `cutoff` — i.e. genuinely stuck. Candidates
// with no span rows at all also pass through (they're lite-mode or
// pre-instrumentation tasks; the simple updated_at check already proved
// them stuck and we have no heartbeat to override that).
func (h *HousekeepingService) filterByLastSpanActivity(ctx context.Context, candidates []types.Knowledge, cutoff time.Time) []types.Knowledge {
	if len(candidates) == 0 {
		return candidates
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
		return candidates
	}
	heartbeat := make(map[string]time.Time, len(beats))
	for _, b := range beats {
		if t, ok := parseHeartbeatTime(b.LastSeen); ok {
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
	return out
}

// filterOutQueued returns the subset of candidates that have NO task left
// in the queue backend, plus a count of how many were dropped because a
// task still references them. A dropped candidate is "backlogged, not
// orphaned" — its enrichment subtasks are waiting for a worker, so the
// missing span heartbeat is expected and recovering it would be a false
// positive. When no inspector is wired (nil) the gate is a pass-through
// so behaviour matches the pre-existing span-only sweep. On inspector
// error we fail safe by KEEPING the candidate as stuck (recover it),
// matching the span heartbeat query's fail-safe direction.
func (h *HousekeepingService) filterOutQueued(
	ctx context.Context, candidates []types.Knowledge,
) (kept []types.Knowledge, skipped int) {
	if h.inspector == nil || len(candidates) == 0 {
		return candidates, 0
	}
	out := candidates[:0]
	for _, k := range candidates {
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
	return out, skipped
}

// parseHeartbeatTime accepts the timestamp formats Postgres and SQLite
// emit for a TIMESTAMP column read back through MAX(). Returns false if
// none parse — the caller treats unparseable rows as "no heartbeat",
// which fails safe (the row gets recovered as stuck rather than
// silently preserved).
func parseHeartbeatTime(s string) (time.Time, bool) {
	if s == "" {
		return time.Time{}, false
	}
	for _, layout := range []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02 15:04:05.999999999-07:00",
		"2006-01-02 15:04:05.999999999",
		"2006-01-02 15:04:05.999999",
		"2006-01-02 15:04:05",
	} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
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
