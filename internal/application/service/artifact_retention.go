package service

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

const defaultArtifactRetentionBatchSize = 1000

const artifactRetentionSweepInterval = 24 * time.Hour

const artifactRetentionStartupDelay = 10 * time.Minute

type artifactRetentionSweeper interface {
	Sweep(ctx context.Context, retentionDays int, batchSize int) (int64, error)
}

type artifactRetentionService struct {
	repo interfaces.ProcessingArtifactRepository
	now  func() time.Time
}

func newArtifactRetentionService(
	repo interfaces.ProcessingArtifactRepository,
) *artifactRetentionService {
	return &artifactRetentionService{
		repo: repo,
		now:  time.Now,
	}
}

func (s *artifactRetentionService) Sweep(
	ctx context.Context, retentionDays int, batchSize int,
) (int64, error) {
	if s == nil || s.repo == nil || retentionDays <= 0 {
		return 0, nil
	}
	now := s.now
	if now == nil {
		now = time.Now
	}
	if batchSize <= 0 {
		batchSize = defaultArtifactRetentionBatchSize
	}
	cutoff := now().Add(-time.Duration(retentionDays) * 24 * time.Hour)
	return s.repo.DeleteExpired(ctx, cutoff, batchSize)
}

// ArtifactRetentionRunner sweeps expired processing_artifacts. The
// artifact cache is ownership-free and shared across generations, so
// generation GC must not delete it; TTL cleanup is a separate,
// best-effort background task.
type ArtifactRetentionRunner struct {
	svc           artifactRetentionSweeper
	retentionDays int
	batchSize     int
	interval      time.Duration

	startOnce sync.Once
	stopOnce  sync.Once
	stopCh    chan struct{}
	doneCh    chan struct{}
	started   atomic.Bool
}

func NewArtifactRetentionRunner(
	cfg *config.Config,
	repo interfaces.ProcessingArtifactRepository,
) *ArtifactRetentionRunner {
	retentionDays := 0
	if cfg != nil && cfg.ArtifactCache != nil {
		retentionDays = cfg.ArtifactCache.RetentionDays
	}
	return &ArtifactRetentionRunner{
		svc:           newArtifactRetentionService(repo),
		retentionDays: retentionDays,
		batchSize:     defaultArtifactRetentionBatchSize,
		interval:      artifactRetentionSweepInterval,
		stopCh:        make(chan struct{}),
		doneCh:        make(chan struct{}),
	}
}

func (r *ArtifactRetentionRunner) Start(ctx context.Context) {
	if r == nil || r.svc == nil {
		return
	}
	r.startOnce.Do(func() {
		r.started.Store(true)
		if r.retentionDays <= 0 {
			logger.Infof(ctx,
				"[artifact-retention] disabled (retention_days=%d)", r.retentionDays)
			close(r.doneCh)
			return
		}
		logger.Infof(ctx,
			"[artifact-retention] starting daily sweep: retention_days=%d batch_size=%d interval=%s",
			r.retentionDays, r.batchSize, r.interval)
		go r.loop()
	})
}

func (r *ArtifactRetentionRunner) Stop() {
	if r == nil {
		return
	}
	if !r.started.Load() {
		return
	}
	r.stopOnce.Do(func() {
		close(r.stopCh)
	})
	<-r.doneCh
}

func (r *ArtifactRetentionRunner) loop() {
	defer close(r.doneCh)

	startupTimer := time.NewTimer(artifactRetentionStartupDelay)
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

func (r *ArtifactRetentionRunner) runOnce() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	deleted, err := r.svc.Sweep(ctx, r.retentionDays, r.batchSize)
	if err != nil {
		logger.Warnf(ctx,
			"[artifact-retention] sweep failed: retention_days=%d batch_size=%d err=%v",
			r.retentionDays, r.batchSize, err)
		return
	}
	if deleted > 0 {
		logger.Infof(ctx,
			"[artifact-retention] sweep complete: deleted=%d retention_days=%d batch_size=%d",
			deleted, r.retentionDays, r.batchSize)
	} else {
		logger.Debugf(ctx,
			"[artifact-retention] sweep complete: deleted=0 retention_days=%d batch_size=%d",
			r.retentionDays, r.batchSize)
	}
}
