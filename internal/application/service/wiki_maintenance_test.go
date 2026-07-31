package service

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type sweepCountingRepo struct {
	interfaces.WikiPageRepository
	attemptSweeps atomic.Int32
	runSweeps     atomic.Int32
	attemptCutoff atomic.Int64
	runCutoff     atomic.Int64
	failWith      error
}

func (r *sweepCountingRepo) ExpireStaleRepairAttempts(
	_ context.Context, cutoff time.Time, _ string, _ time.Time,
) (int64, error) {
	r.attemptSweeps.Add(1)
	r.attemptCutoff.Store(cutoff.UnixNano())
	return 1, r.failWith
}

func (r *sweepCountingRepo) ExpireStaleLintRuns(
	_ context.Context, cutoff time.Time, _ string, _ time.Time,
) (int64, error) {
	r.runSweeps.Add(1)
	r.runCutoff.Store(cutoff.UnixNano())
	return 2, r.failWith
}

// runnerWithImmediateSweeps builds a runner whose startup grace window has been
// collapsed. Production must keep the 2-minute pause (it stays out of the way of
// boot traffic), so the knob is not exported and the test reaches into the
// package instead.
func runnerWithImmediateSweeps(repo interfaces.WikiPageRepository) *WikiMaintenanceRunner {
	return &WikiMaintenanceRunner{
		repo:     repo,
		interval: 20 * time.Millisecond,
		stopCh:   make(chan struct{}),
		doneCh:   make(chan struct{}),
	}
}

func (r *WikiMaintenanceRunner) loopWithoutStartupDelay() {
	defer close(r.doneCh)
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

// TestWikiMaintenanceRunnerSweepsBothHorizons is the behavioural test: the
// runner must actually retire abandoned repair attempts and lint runs over time.
// Before it existed those recoveries rode along inside read endpoints, so an
// idle deployment never recovered at all.
func TestWikiMaintenanceRunnerSweepsBothHorizons(t *testing.T) {
	repo := &sweepCountingRepo{}
	runner := runnerWithImmediateSweeps(repo)
	runner.started.Store(true)
	go runner.loopWithoutStartupDelay()
	time.Sleep(90 * time.Millisecond)
	runner.Stop()

	assert.GreaterOrEqual(t, repo.attemptSweeps.Load(), int32(2))
	assert.GreaterOrEqual(t, repo.runSweeps.Load(), int32(2))

	// The two horizons are deliberately different: a repair that stopped
	// reporting for half an hour is dead, while a full scan of a very large KB
	// can legitimately still be running after that long.
	attemptAge := time.Since(time.Unix(0, repo.attemptCutoff.Load()))
	runAge := time.Since(time.Unix(0, repo.runCutoff.Load()))
	assert.InDelta(t, wikiRepairAttemptStaleAfter.Seconds(), attemptAge.Seconds(), 5)
	assert.InDelta(t, wikiLintRunStaleAfter.Seconds(), runAge.Seconds(), 5)
	assert.Greater(t, wikiLintRunStaleAfter, wikiRepairAttemptStaleAfter)
}

// TestWikiMaintenanceRunnerSurvivesSweepErrors pins that a broken database
// delays recovery rather than killing the goroutine — a missed sweep never
// corrupts state, so the next tick should simply try again.
func TestWikiMaintenanceRunnerSurvivesSweepErrors(t *testing.T) {
	repo := &sweepCountingRepo{failWith: errors.New("simulated outage")}
	runner := runnerWithImmediateSweeps(repo)
	runner.started.Store(true)
	go runner.loopWithoutStartupDelay()
	time.Sleep(70 * time.Millisecond)
	runner.Stop()

	assert.GreaterOrEqual(t, repo.attemptSweeps.Load(), int32(2),
		"a failing sweep must not stop the loop")
}

// TestWikiMaintenanceRunnerLifecycleIsIdempotent covers container wiring and
// teardown ordering: double Start must not double-sweep, and Stop must never
// block on a doneCh nobody is going to close.
func TestWikiMaintenanceRunnerLifecycleIsIdempotent(t *testing.T) {
	t.Run("stop before start returns immediately", func(t *testing.T) {
		runner := NewWikiMaintenanceRunner(&sweepCountingRepo{})
		done := make(chan struct{})
		go func() { runner.Stop(); close(done) }()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatal("Stop() hung although Start() was never called")
		}
	})

	t.Run("nil repo short-circuits without deadlocking stop", func(t *testing.T) {
		runner := NewWikiMaintenanceRunner(nil)
		runner.Start(context.Background())
		done := make(chan struct{})
		go func() { runner.Stop(); close(done) }()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatal("Stop() hung after Start() short-circuited on a nil repository")
		}
	})

	t.Run("repeated start and stop are no-ops", func(t *testing.T) {
		repo := &sweepCountingRepo{}
		runner := NewWikiMaintenanceRunner(repo)
		runner.Start(context.Background())
		runner.Start(context.Background())
		runner.Stop()
		runner.Stop()
		// The 2-minute startup grace window means production never swept here.
		require.Zero(t, repo.attemptSweeps.Load())
	})
}
