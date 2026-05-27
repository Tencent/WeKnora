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
	"github.com/robfig/cron/v3"
	"gorm.io/gorm"
)

// HousekeepingService runs background sweeps to recover stuck rows.
type HousekeepingService struct {
	db   *gorm.DB
	cfg  *config.Config
	cron *cron.Cron

	mu      sync.Mutex
	started bool
}

// NewHousekeepingService constructs a HousekeepingService. It does NOT start
// the cron — call Start in the application bootstrap so a misconfigured
// cron schedule cannot prevent the rest of the service from coming up.
func NewHousekeepingService(db *gorm.DB, cfg *config.Config) *HousekeepingService {
	return &HousekeepingService{
		db:  db,
		cfg: cfg,
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

	// Sweep A: knowledge stuck in "processing".
	resKnowledge := h.db.WithContext(ctx).Model(&types.Knowledge{}).
		Where("parse_status = ? AND updated_at < ?", types.ParseStatusProcessing, cutoff).
		Updates(map[string]interface{}{
			"parse_status":  types.ParseStatusFailed,
			"error_message": "task stuck in processing > " + threshold.String() + ", recovered by housekeeping",
		})
	if resKnowledge.Error != nil {
		logger.Warnf(ctx, "[Housekeeping] knowledge sweep failed: %v", resKnowledge.Error)
	} else if resKnowledge.RowsAffected > 0 {
		logger.Infof(ctx, "[Housekeeping] recovered %d stuck knowledge rows (threshold=%s)",
			resKnowledge.RowsAffected, threshold)
	}

	// Sweep B: knowledge summary stuck. Summary is post-parse; threshold
	// is shorter because summary tasks are bounded by a single LLM call.
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
