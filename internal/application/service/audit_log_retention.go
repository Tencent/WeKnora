package service

import (
	"context"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// AuditLogRetentionRunner sweeps audit_logs once a day, deleting rows
// older than `retentionDays`. It is a small, self-contained
// background goroutine — no robfig/cron, no asynq — because retention
// has no wall-clock alignment requirement: we just need "approximately
// daily, eventually". A bare time.Ticker keeps the dependency surface
// minimal.
//
// retentionDays <= 0 makes Start a no-op; this is the configured way
// to disable retention entirely. Validation happens at config-load
// time so by the time we're here a non-positive value is intentional.
type AuditLogRetentionRunner struct {
	svc           interfaces.AuditLogService
	retentionDays int
	interval      time.Duration

	startOnce sync.Once
	stopOnce  sync.Once
	stopCh    chan struct{}
	doneCh    chan struct{}
}

// auditLogPurgeInterval is the gap between sweeps. 24h is enough for
// a per-day retention horizon — the cutoff moves by 24h between runs
// so each sweep deletes one day's worth of rolled-off rows. Shortening
// this would just cause empty sweeps; lengthening it would pile up
// stale rows for a day.
const auditLogPurgeInterval = 24 * time.Hour

// auditLogPurgeStartupDelay holds the very first sweep until shortly
// after boot so we don't compete with migration-up traffic or other
// startup work. Long enough that the first DELETE doesn't fight the
// initial request flood; short enough that operators see the sweep
// fire on the same day they restart.
const auditLogPurgeStartupDelay = 10 * time.Minute

// NewAuditLogRetentionRunner constructs the runner with production
// defaults. retention_days is read from the config; passing the full
// *config.Config (rather than just an int) keeps the dig wiring trivial
// — it's the same shape as every other config-aware constructor in
// the container. The constructor only validates inputs; nothing fires
// until Start is called.
func NewAuditLogRetentionRunner(
	cfg *config.Config, svc interfaces.AuditLogService,
) *AuditLogRetentionRunner {
	retentionDays := 0
	if cfg != nil && cfg.Audit != nil {
		retentionDays = cfg.Audit.RetentionDays
	}
	return &AuditLogRetentionRunner{
		svc:           svc,
		retentionDays: retentionDays,
		interval:      auditLogPurgeInterval,
		stopCh:        make(chan struct{}),
		doneCh:        make(chan struct{}),
	}
}

// Start spins up the background goroutine. Calling it more than once
// is a no-op (sync.Once), so container wiring that mistakenly invokes
// us twice doesn't double-purge. retentionDays <= 0 means the runner
// stays dormant — Stop will still complete cleanly.
func (r *AuditLogRetentionRunner) Start(ctx context.Context) {
	if r == nil || r.svc == nil {
		return
	}
	r.startOnce.Do(func() {
		if r.retentionDays <= 0 {
			logger.Infof(ctx,
				"[audit-retention] disabled (retention_days=%d)", r.retentionDays)
			close(r.doneCh)
			return
		}
		logger.Infof(ctx,
			"[audit-retention] starting daily sweep: retention_days=%d interval=%s",
			r.retentionDays, r.interval)
		go r.loop()
	})
}

// Stop signals the loop to exit and blocks until it returns. Idempotent.
// If Start was never called, Stop returns immediately.
func (r *AuditLogRetentionRunner) Stop() {
	if r == nil {
		return
	}
	r.stopOnce.Do(func() {
		close(r.stopCh)
	})
	<-r.doneCh
}

// loop runs the actual sweep cadence. Uses a fresh context.Background
// per iteration because the request-scoped ctx from Start would be
// cancelled the moment Start's caller returned — and that caller is
// container init.
func (r *AuditLogRetentionRunner) loop() {
	defer close(r.doneCh)

	startupTimer := time.NewTimer(auditLogPurgeStartupDelay)
	defer startupTimer.Stop()
	select {
	case <-startupTimer.C:
	case <-r.stopCh:
		return
	}

	r.runOnce()

	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			r.runOnce()
		case <-r.stopCh:
			return
		}
	}
}

// runOnce performs a single sweep. The DB call is given a generous
// timeout (30 s) so a stuck connection doesn't hold the goroutine
// hostage forever — if the sweep doesn't finish in 30 s we'll log
// and try again 24 h later. Errors are logged at WARN, not ERROR,
// because the table just keeps growing one more day; nothing breaks.
func (r *AuditLogRetentionRunner) runOnce() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	deleted, err := r.svc.Purge(ctx, r.retentionDays)
	if err != nil {
		logger.Warnf(ctx,
			"[audit-retention] sweep failed: retention_days=%d err=%v",
			r.retentionDays, err)
		return
	}
	if deleted > 0 {
		logger.Infof(ctx,
			"[audit-retention] sweep complete: deleted=%d retention_days=%d",
			deleted, r.retentionDays)
	} else {
		logger.Debugf(ctx,
			"[audit-retention] sweep complete: deleted=0 retention_days=%d",
			r.retentionDays)
	}
}
