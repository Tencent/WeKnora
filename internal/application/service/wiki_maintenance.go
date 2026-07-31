package service

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// Wiki repair/lint staleness horizons.
//
// A worker can vanish after claiming an issue or a lint run, leaving a row that
// blocks the next attempt: an issue keeps its active_attempt_id, and the partial
// unique index on wiki_lint_runs refuses a second queued run. Both are retired
// on a timer so the block is always temporary.
const (
	// wikiRepairAttemptStaleAfter bounds how long an unreported repair holds its
	// issue. Agent sessions finish in minutes; 30 is generous headroom that
	// still frees the issue within one working pause.
	wikiRepairAttemptStaleAfter = 30 * time.Minute

	// wikiLintRunStaleAfter is deliberately much longer: a full scan of a very
	// large KB legitimately takes hours, and killing a live run would be worse
	// than making the user wait.
	wikiLintRunStaleAfter = 6 * time.Hour

	wikiRepairAttemptExpiredMessage = "Repair attempt expired before it reported a verified result."
	wikiLintRunExpiredMessage       = "Lint run expired after the worker stopped reporting progress."
)

// wikiMaintenanceInterval is the sweep cadence. It is well below
// wikiRepairAttemptStaleAfter so a freed issue becomes retryable promptly after
// crossing the horizon, without the sweep itself being chatty.
const wikiMaintenanceInterval = 5 * time.Minute

// wikiMaintenanceStartupDelay holds the first sweep until after boot so it does
// not compete with migrations and the initial request flood.
const wikiMaintenanceStartupDelay = 2 * time.Minute

// WikiMaintenanceRunner retires abandoned wiki repair attempts and lint runs on
// a timer.
//
// Before this existed, expiry rode along inside read paths — listing active
// attempts failed the stale ones, and creating a lint run swept old ones. That
// made a GET mutate state and left recovery dependent on someone happening to
// call the right endpoint. Owning the sweep here keeps the query paths pure and
// makes recovery hold even on an idle deployment.
//
// It follows the same shape as AuditLogRetentionRunner: a bare time.Ticker in a
// goroutine, no cron and no asynq, because "eventually, roughly every few
// minutes" is all the guarantee we need.
type WikiMaintenanceRunner struct {
	repo     interfaces.WikiPageRepository
	interval time.Duration

	startOnce sync.Once
	stopOnce  sync.Once
	stopCh    chan struct{}
	doneCh    chan struct{}
	// started is set inside startOnce.Do before doneCh is handed to a
	// goroutine, so Stop can tell "never started" from "running" without
	// blocking on a channel nobody will close.
	started atomic.Bool
}

// NewWikiMaintenanceRunner wires the runner with production defaults. Nothing
// fires until Start is called.
func NewWikiMaintenanceRunner(repo interfaces.WikiPageRepository) *WikiMaintenanceRunner {
	return &WikiMaintenanceRunner{
		repo:     repo,
		interval: wikiMaintenanceInterval,
		stopCh:   make(chan struct{}),
		doneCh:   make(chan struct{}),
	}
}

// Start spins up the sweep goroutine. Repeat calls are no-ops.
func (r *WikiMaintenanceRunner) Start(ctx context.Context) {
	if r == nil || r.repo == nil {
		return
	}
	r.startOnce.Do(func() {
		r.started.Store(true)
		logger.Infof(ctx,
			"[wiki-maintenance] starting sweep: interval=%s attempt_horizon=%s run_horizon=%s",
			r.interval, wikiRepairAttemptStaleAfter, wikiLintRunStaleAfter)
		go r.loop()
	})
}

// Stop signals the loop and blocks until it returns. Idempotent, and a no-op
// when Start never ran.
func (r *WikiMaintenanceRunner) Stop() {
	if r == nil || !r.started.Load() {
		return
	}
	r.stopOnce.Do(func() {
		close(r.stopCh)
	})
	<-r.doneCh
}

func (r *WikiMaintenanceRunner) loop() {
	defer close(r.doneCh)

	startupTimer := time.NewTimer(wikiMaintenanceStartupDelay)
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

// runOnce performs a single sweep. Failures are logged at WARN and retried on
// the next tick: a missed sweep only delays recovery, it never corrupts state.
func (r *WikiMaintenanceRunner) runOnce() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	now := time.Now()
	if retired, err := r.repo.ExpireStaleRepairAttempts(
		ctx, now.Add(-wikiRepairAttemptStaleAfter), wikiRepairAttemptExpiredMessage, now,
	); err != nil {
		logger.Warnf(ctx, "[wiki-maintenance] expiring repair attempts failed: %v", err)
	} else if retired > 0 {
		logger.Infof(ctx, "[wiki-maintenance] retired %d stale repair attempts", retired)
	}

	if retired, err := r.repo.ExpireStaleLintRuns(
		ctx, now.Add(-wikiLintRunStaleAfter), wikiLintRunExpiredMessage, now,
	); err != nil {
		logger.Warnf(ctx, "[wiki-maintenance] expiring lint runs failed: %v", err)
	} else if retired > 0 {
		logger.Infof(ctx, "[wiki-maintenance] retired %d stale lint runs", retired)
	}
}
