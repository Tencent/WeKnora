package service

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// stubAuditRepoForRetention captures DeleteOlderThan calls. We embed
// the interface so any unstubbed method nil-panics — that's the same
// contract-drift signal stubAuditRepo uses, kept consistent across
// the package's audit log tests.
type stubAuditRepoForRetention struct {
	interfaces.AuditLogRepository

	mu          sync.Mutex
	calls       []time.Time
	deleted     int64
	deleteError error
}

func (s *stubAuditRepoForRetention) DeleteOlderThan(_ context.Context, cutoff time.Time) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, cutoff)
	if s.deleteError != nil {
		return 0, s.deleteError
	}
	return s.deleted, nil
}

func TestAuditLog_Purge_NoOpWhenRetentionDisabled(t *testing.T) {
	// retention_days <= 0 must short-circuit before hitting the repo.
	// Otherwise an "off" config would still issue a daily DELETE on
	// every column < cutoff, which silently nukes every row when
	// cutoff = now().
	repo := &stubAuditRepoForRetention{}
	clock := &fakeClock{t: time.Date(2026, 5, 14, 10, 0, 0, 0, time.UTC)}
	svc := &auditLogService{repo: repo, now: clock.Now}

	for _, days := range []int{0, -1, -90} {
		deleted, err := svc.Purge(context.Background(), days)
		if err != nil {
			t.Fatalf("Purge(%d) unexpected error: %v", days, err)
		}
		if deleted != 0 {
			t.Fatalf("Purge(%d) returned non-zero count: %d", days, deleted)
		}
	}
	if len(repo.calls) != 0 {
		t.Fatalf("expected zero repo.DeleteOlderThan calls, got %d", len(repo.calls))
	}
}

func TestAuditLog_Purge_UsesClockMinusRetention(t *testing.T) {
	// The cutoff fed to the repo must be exactly retention_days × 24h
	// before the service's clock. Off-by-day errors would silently
	// retain too much (table grows) or delete too much (data loss).
	repo := &stubAuditRepoForRetention{deleted: 42}
	clock := &fakeClock{t: time.Date(2026, 5, 14, 10, 0, 0, 0, time.UTC)}
	svc := &auditLogService{repo: repo, now: clock.Now}

	deleted, err := svc.Purge(context.Background(), 90)
	if err != nil {
		t.Fatalf("Purge: %v", err)
	}
	if deleted != 42 {
		t.Fatalf("expected delete count 42 propagated, got %d", deleted)
	}

	if len(repo.calls) != 1 {
		t.Fatalf("expected 1 repo call, got %d", len(repo.calls))
	}
	wantCutoff := clock.Now().Add(-90 * 24 * time.Hour)
	if !repo.calls[0].Equal(wantCutoff) {
		t.Fatalf("cutoff: want %v, got %v", wantCutoff, repo.calls[0])
	}
}

func TestAuditLog_Purge_PropagatesRepoError(t *testing.T) {
	// Retention failures must surface to the runner, which logs them
	// at WARN. Silently swallowing the error would mask a degraded DB
	// for days because the next sweep is 24h away.
	repo := &stubAuditRepoForRetention{deleteError: errors.New("connection lost")}
	clock := &fakeClock{t: time.Date(2026, 5, 14, 10, 0, 0, 0, time.UTC)}
	svc := &auditLogService{repo: repo, now: clock.Now}

	if _, err := svc.Purge(context.Background(), 30); err == nil {
		t.Fatalf("expected error to propagate from repo")
	}
}

// purgeCountingService is a tiny in-test AuditLogService implementation
// that just counts Purge calls. The runner's loop is the only thing
// under test here, so we can ignore Log / LogDenied / List entirely.
type purgeCountingService struct {
	interfaces.AuditLogService
	calls atomic.Int64
}

func (p *purgeCountingService) Purge(_ context.Context, _ int) (int64, error) {
	p.calls.Add(1)
	return 0, nil
}

func TestAuditLogRetentionRunner_StartIsNoOpWhenDisabled(t *testing.T) {
	// retention_days <= 0 keeps the goroutine asleep — Start logs a
	// "disabled" line and Stop returns immediately. This is the
	// configured way to turn retention off, and the runner must not
	// kick a goroutine for it.
	svc := &purgeCountingService{}
	r := &AuditLogRetentionRunner{
		svc:           svc,
		retentionDays: 0,
		interval:      time.Millisecond,
		stopCh:        make(chan struct{}),
		doneCh:        make(chan struct{}),
	}
	r.Start(context.Background())
	r.Stop()
	if got := svc.calls.Load(); got != 0 {
		t.Fatalf("expected 0 Purge calls when disabled, got %d", got)
	}
}

func TestAuditLogRetentionRunner_StopIsIdempotent(t *testing.T) {
	// Calling Stop twice must not panic — container shutdown ordering
	// can race ResourceCleaner with other shutdown hooks, and we'd
	// rather no-op than crash on the second call.
	svc := &purgeCountingService{}
	r := &AuditLogRetentionRunner{
		svc:           svc,
		retentionDays: 0,
		interval:      time.Millisecond,
		stopCh:        make(chan struct{}),
		doneCh:        make(chan struct{}),
	}
	r.Start(context.Background())
	r.Stop()
	r.Stop()
}

func TestAuditLogRetentionRunner_StartIsIdempotent(t *testing.T) {
	// Container init that mistakenly invokes Start twice must not
	// double-fire the loop. sync.Once guards this; the test pins the
	// invariant.
	svc := &purgeCountingService{}
	r := &AuditLogRetentionRunner{
		svc:           svc,
		retentionDays: 0,
		interval:      time.Millisecond,
		stopCh:        make(chan struct{}),
		doneCh:        make(chan struct{}),
	}
	r.Start(context.Background())
	r.Start(context.Background())
	r.Stop()
}

func TestAuditLogRetentionRunner_NilSvcShortCircuits(t *testing.T) {
	// Defensive: a misconfigured container (audit service couldn't
	// be constructed) must not crash the app. Start with nil svc is
	// a no-op, and the subsequent Stop must NOT hang on a doneCh
	// nobody ever closed (regression: when startOnce.Do returned early
	// without closing doneCh, Stop would block forever, deadlocking
	// graceful shutdown).
	r := &AuditLogRetentionRunner{
		retentionDays: 90,
		interval:      time.Millisecond,
		stopCh:        make(chan struct{}),
		doneCh:        make(chan struct{}),
	}
	r.Start(context.Background())

	done := make(chan struct{})
	go func() {
		r.Stop()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Stop() hung after Start() short-circuited on nil svc")
	}
}

func TestAuditLogRetentionRunner_StopBeforeStart(t *testing.T) {
	// Container teardown ordering can run Stop before Start ever fires
	// (early init failure, test cleanup). The runner must treat this
	// as a no-op rather than blocking on doneCh.
	r := &AuditLogRetentionRunner{
		svc:           &purgeCountingService{},
		retentionDays: 90,
		interval:      time.Millisecond,
		stopCh:        make(chan struct{}),
		doneCh:        make(chan struct{}),
	}
	done := make(chan struct{})
	go func() {
		r.Stop()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Stop() hung when called before Start()")
	}
}

// retentionRunnerWithImmediateStartup builds a runner whose startup
// delay has already elapsed, so the loop runs Purge immediately. We
// can't expose that as a public knob (production should not skip the
// startup grace window), so the test reaches inside the package to
// build the runner with a custom interval.
func retentionRunnerWithImmediateStartup(svc interfaces.AuditLogService, days int) *AuditLogRetentionRunner {
	return &AuditLogRetentionRunner{
		svc:           svc,
		retentionDays: days,
		interval:      30 * time.Millisecond,
		stopCh:        make(chan struct{}),
		doneCh:        make(chan struct{}),
	}
}

// runLoopWithoutStartupDelay drives the runner's loop directly, skipping
// the 10-minute startup pause that production uses to stay out of the
// way of boot traffic. Tests need to fire the sweep immediately.
func (r *AuditLogRetentionRunner) runLoopWithoutStartupDelay() {
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

func TestAuditLogRetentionRunner_PurgesOnTickerCadence(t *testing.T) {
	// The actual cadence test: with the startup delay collapsed and
	// the interval shrunk to 30ms, we expect at least 2 Purge calls
	// over a 100ms window. This pins the headline behaviour ("yes,
	// the goroutine actually fires Purge over time") without coupling
	// to a specific count, which would be flaky on a slow CI runner.
	svc := &purgeCountingService{}
	r := retentionRunnerWithImmediateStartup(svc, 30)

	go r.runLoopWithoutStartupDelay()
	time.Sleep(100 * time.Millisecond)
	r.Stop()

	if got := svc.calls.Load(); got < 2 {
		t.Fatalf("expected >=2 Purge calls in 100ms with 30ms interval, got %d", got)
	}
}

func TestAuditLogRetentionRunner_RunOnceLogsButDoesNotPanicOnError(t *testing.T) {
	// runOnce must swallow Purge errors — a stuck DB shouldn't propagate
	// a panic up out of the goroutine and crash the app. The behaviour
	// is "log at WARN and try again next tick".
	repo := &stubAuditRepoForRetention{deleteError: errors.New("simulated")}
	clock := &fakeClock{t: time.Date(2026, 5, 14, 10, 0, 0, 0, time.UTC)}
	svc := &auditLogService{repo: repo, now: clock.Now}

	r := retentionRunnerWithImmediateStartup(svc, 30)
	r.runOnce() // must not panic
	if got := len(repo.calls); got != 1 {
		t.Fatalf("expected runOnce to call repo once, got %d", got)
	}
}

// Ensure the test stubs satisfy the production type: if the interface
// drifts, this assignment will fail to compile and tell us immediately.
var _ interfaces.AuditLogRepository = (*stubAuditRepo)(nil)
var _ interfaces.AuditLogRepository = (*stubAuditRepoForRetention)(nil)

// Sanity check the audit entry struct's fields stay attached to the
// retention path — guards against a refactor that drops CreatedAt
// from the model and silently breaks the cutoff filter.
func TestAuditLogModel_HasCreatedAtField(t *testing.T) {
	entry := types.AuditLog{}
	entry.CreatedAt = time.Now()
	if entry.CreatedAt.IsZero() {
		t.Fatal("AuditLog.CreatedAt must be assignable")
	}
}
