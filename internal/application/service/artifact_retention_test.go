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

type stubArtifactRepoForRetention struct {
	interfaces.ProcessingArtifactRepository

	mu          sync.Mutex
	calls       []artifactDeleteCall
	deleted     int64
	deleteError error
}

type artifactDeleteCall struct {
	before time.Time
	limit  int
}

func (s *stubArtifactRepoForRetention) DeleteExpired(
	_ context.Context, before time.Time, limit int,
) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, artifactDeleteCall{before: before, limit: limit})
	if s.deleteError != nil {
		return 0, s.deleteError
	}
	return s.deleted, nil
}

func TestArtifactRetentionSweep_NoOpWhenRetentionDisabled(t *testing.T) {
	repo := &stubArtifactRepoForRetention{}
	clock := &fakeClock{t: time.Date(2026, 5, 14, 10, 0, 0, 0, time.UTC)}
	svc := &artifactRetentionService{repo: repo, now: clock.Now}

	for _, days := range []int{0, -1, -90} {
		deleted, err := svc.Sweep(context.Background(), days, 100)
		if err != nil {
			t.Fatalf("Sweep(%d) unexpected error: %v", days, err)
		}
		if deleted != 0 {
			t.Fatalf("Sweep(%d) returned non-zero count: %d", days, deleted)
		}
	}
	if len(repo.calls) != 0 {
		t.Fatalf("expected zero repo.DeleteExpired calls, got %d", len(repo.calls))
	}
}

func TestArtifactRetentionSweep_DeletesExpiredRowsAtConfiguredLimit(t *testing.T) {
	repo := &stubArtifactRepoForRetention{deleted: 7}
	clock := &fakeClock{t: time.Date(2026, 5, 14, 10, 0, 0, 0, time.UTC)}
	svc := &artifactRetentionService{repo: repo, now: clock.Now}

	deleted, err := svc.Sweep(context.Background(), 14, 250)
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if deleted != 7 {
		t.Fatalf("expected delete count 7 propagated, got %d", deleted)
	}
	if len(repo.calls) != 1 {
		t.Fatalf("expected 1 repo call, got %d", len(repo.calls))
	}
	wantCutoff := clock.Now().Add(-14 * 24 * time.Hour)
	if !repo.calls[0].before.Equal(wantCutoff) {
		t.Fatalf("cutoff: want %v, got %v", wantCutoff, repo.calls[0].before)
	}
	if repo.calls[0].limit != 250 {
		t.Fatalf("limit: want 250, got %d", repo.calls[0].limit)
	}
}

func TestArtifactRetentionSweep_DefaultsInvalidLimit(t *testing.T) {
	repo := &stubArtifactRepoForRetention{}
	clock := &fakeClock{t: time.Date(2026, 5, 14, 10, 0, 0, 0, time.UTC)}
	svc := &artifactRetentionService{repo: repo, now: clock.Now}

	_, err := svc.Sweep(context.Background(), 30, 0)
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if len(repo.calls) != 1 {
		t.Fatalf("expected 1 repo call, got %d", len(repo.calls))
	}
	if repo.calls[0].limit != defaultArtifactRetentionBatchSize {
		t.Fatalf("limit: want %d, got %d",
			defaultArtifactRetentionBatchSize, repo.calls[0].limit)
	}
}

func TestArtifactRetentionSweep_PropagatesRepoError(t *testing.T) {
	repo := &stubArtifactRepoForRetention{deleteError: errors.New("connection lost")}
	clock := &fakeClock{t: time.Date(2026, 5, 14, 10, 0, 0, 0, time.UTC)}
	svc := &artifactRetentionService{repo: repo, now: clock.Now}

	if _, err := svc.Sweep(context.Background(), 30, 100); err == nil {
		t.Fatalf("expected error to propagate from repo")
	}
}

type artifactSweepCountingService struct {
	calls atomic.Int64
}

func (p *artifactSweepCountingService) Sweep(_ context.Context, _ int, _ int) (int64, error) {
	p.calls.Add(1)
	return 0, nil
}

func TestArtifactRetentionRunner_StartIsNoOpWhenDisabled(t *testing.T) {
	svc := &artifactSweepCountingService{}
	r := &ArtifactRetentionRunner{
		svc:           svc,
		retentionDays: 0,
		batchSize:     100,
		interval:      time.Millisecond,
		stopCh:        make(chan struct{}),
		doneCh:        make(chan struct{}),
	}
	r.Start(context.Background())
	r.Stop()
	if got := svc.calls.Load(); got != 0 {
		t.Fatalf("expected 0 Sweep calls when disabled, got %d", got)
	}
}

func TestArtifactRetentionRunner_StopIsIdempotent(t *testing.T) {
	svc := &artifactSweepCountingService{}
	r := &ArtifactRetentionRunner{
		svc:           svc,
		retentionDays: 0,
		batchSize:     100,
		interval:      time.Millisecond,
		stopCh:        make(chan struct{}),
		doneCh:        make(chan struct{}),
	}
	r.Start(context.Background())
	r.Stop()
	r.Stop()
}

func TestArtifactRetentionRunner_StartIsIdempotent(t *testing.T) {
	svc := &artifactSweepCountingService{}
	r := &ArtifactRetentionRunner{
		svc:           svc,
		retentionDays: 0,
		batchSize:     100,
		interval:      time.Millisecond,
		stopCh:        make(chan struct{}),
		doneCh:        make(chan struct{}),
	}
	r.Start(context.Background())
	r.Start(context.Background())
	r.Stop()
}

func TestArtifactRetentionRunner_NilSvcShortCircuits(t *testing.T) {
	r := &ArtifactRetentionRunner{
		retentionDays: 30,
		batchSize:     100,
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

func TestArtifactRetentionRunner_StopBeforeStart(t *testing.T) {
	r := &ArtifactRetentionRunner{
		svc:           &artifactSweepCountingService{},
		retentionDays: 30,
		batchSize:     100,
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

func artifactRetentionRunnerWithImmediateStartup(
	svc artifactRetentionSweeper, days int,
) *ArtifactRetentionRunner {
	return &ArtifactRetentionRunner{
		svc:           svc,
		retentionDays: days,
		batchSize:     100,
		interval:      30 * time.Millisecond,
		stopCh:        make(chan struct{}),
		doneCh:        make(chan struct{}),
	}
}

func (r *ArtifactRetentionRunner) runLoopWithoutStartupDelay() {
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

func TestArtifactRetentionRunner_SweepsOnTickerCadence(t *testing.T) {
	svc := &artifactSweepCountingService{}
	r := artifactRetentionRunnerWithImmediateStartup(svc, 30)

	go r.runLoopWithoutStartupDelay()
	time.Sleep(100 * time.Millisecond)
	r.Stop()

	if got := svc.calls.Load(); got < 2 {
		t.Fatalf("expected >=2 Sweep calls in 100ms with 30ms interval, got %d", got)
	}
}

func TestArtifactRetentionRunner_RunOnceLogsButDoesNotPanicOnError(t *testing.T) {
	repo := &stubArtifactRepoForRetention{deleteError: errors.New("simulated")}
	clock := &fakeClock{t: time.Date(2026, 5, 14, 10, 0, 0, 0, time.UTC)}
	svc := &artifactRetentionService{repo: repo, now: clock.Now}

	r := artifactRetentionRunnerWithImmediateStartup(svc, 30)
	r.runOnce()
	if got := len(repo.calls); got != 1 {
		t.Fatalf("expected runOnce to call repo once, got %d", got)
	}
}

var _ interfaces.ProcessingArtifactRepository = (*stubArtifactRepoForRetention)(nil)

func TestProcessingArtifactModel_HasExpiresAtField(t *testing.T) {
	artifact := types.ProcessingArtifact{}
	expiresAt := time.Now()
	artifact.ExpiresAt = &expiresAt
	if artifact.ExpiresAt == nil || artifact.ExpiresAt.IsZero() {
		t.Fatal("ProcessingArtifact.ExpiresAt must be assignable")
	}
}
