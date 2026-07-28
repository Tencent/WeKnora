package service

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

const processingArtifactRetentionStartupDelay = 10 * time.Minute

type ProcessingArtifactRetentionRunner struct {
	service       interfaces.ProcessingArtifactRetentionService
	counters      interfaces.ProcessingArtifactCounterRegistry
	retentionDays int
	batchSize     int
	interval      time.Duration
	invalid       bool

	mu      sync.Mutex
	stopCh  chan struct{}
	doneCh  chan struct{}
	started bool
	stopped bool
}

func NewProcessingArtifactRetentionRunner(
	cfg *config.Config,
	service interfaces.ProcessingArtifactRetentionService,
	counters interfaces.ProcessingArtifactCounterRegistry,
) *ProcessingArtifactRetentionRunner {
	retentionDays, batchSize, intervalHours := 0, 100, 24
	invalid := false
	if cfg != nil && cfg.ProcessingArtifact != nil {
		batchSize = cfg.ProcessingArtifact.CleanupBatchSize
		if processingArtifactRetentionSettingsValid(
			cfg.ProcessingArtifact.RetentionDays,
			cfg.ProcessingArtifact.CleanupIntervalHours,
		) {
			retentionDays = cfg.ProcessingArtifact.RetentionDays
			intervalHours = cfg.ProcessingArtifact.CleanupIntervalHours
		} else {
			invalid = true
		}
	}
	return &ProcessingArtifactRetentionRunner{
		service:       service,
		counters:      counters,
		retentionDays: retentionDays,
		batchSize:     batchSize,
		interval:      time.Duration(intervalHours) * time.Hour,
		invalid:       invalid,
		stopCh:        make(chan struct{}),
		doneCh:        make(chan struct{}),
	}
}

func (r *ProcessingArtifactRetentionRunner) Start(ctx context.Context) {
	if r == nil || r.service == nil {
		return
	}
	r.mu.Lock()
	if r.stopped || r.started {
		r.mu.Unlock()
		return
	}
	r.started = true
	if r.invalid {
		close(r.doneCh)
		r.mu.Unlock()
		logger.Warnf(ctx, "[processing-artifact-retention] disabled due to invalid duration settings")
		return
	}
	r.mu.Unlock()
	if r.retentionDays == 0 {
		logger.Infof(ctx, "[processing-artifact-retention] purge disabled; counter reporting remains active")
		go r.loop()
		return
	}
	logger.Infof(ctx,
		"[processing-artifact-retention] starting sweep: retention_days=%d cleanup_interval_hours=%d cleanup_batch_size=%d",
		r.retentionDays, int(r.interval/time.Hour), r.batchSize,
	)
	go r.loop()
}

func (r *ProcessingArtifactRetentionRunner) Stop() {
	if r == nil {
		return
	}
	r.mu.Lock()
	if !r.stopped {
		r.stopped = true
		close(r.stopCh)
		if !r.started {
			close(r.doneCh)
		}
	}
	doneCh := r.doneCh
	r.mu.Unlock()
	<-doneCh
}

func (r *ProcessingArtifactRetentionRunner) loop() {
	defer close(r.doneCh)
	startupTimer := time.NewTimer(processingArtifactRetentionStartupDelay)
	defer startupTimer.Stop()
	select {
	case <-startupTimer.C:
	case <-r.stopCh:
		return
	}

	r.runOnce()
	if r.interval <= 0 {
		logger.Warnf(context.Background(), "[processing-artifact-retention] invalid cleanup interval")
		return
	}
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

func (r *ProcessingArtifactRetentionRunner) runOnce() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	r.reportCounters(ctx)

	if r.invalid {
		logger.Warnf(ctx, "[processing-artifact-retention] invalid duration settings")
		return
	}
	if r.retentionDays == 0 {
		return
	}
	cutoff, ok := processingArtifactRetentionCutoff(time.Now(), r.retentionDays)
	if !ok {
		logger.Warnf(ctx, "[processing-artifact-retention] invalid retention_days=%d", r.retentionDays)
		return
	}
	result, err := r.service.PurgeExpired(
		ctx,
		cutoff,
		r.batchSize,
	)
	if err != nil {
		logger.GetLogger(ctx).WithField(
			"failure_kind", processingArtifactRetentionFailureKind(err),
		).Warnf(
			"[processing-artifact-retention] sweep incomplete: retention_days=%d cleanup_batch_size=%d scanned=%d deleted=%d failed=%d deleted_bytes=%d",
			r.retentionDays, r.batchSize, result.Scanned, result.Deleted, result.Failed, result.DeletedBytes,
		)
		return
	}
	logger.Infof(ctx,
		"[processing-artifact-retention] sweep complete: retention_days=%d cleanup_batch_size=%d scanned=%d deleted=%d failed=%d deleted_bytes=%d",
		r.retentionDays, r.batchSize, result.Scanned, result.Deleted, result.Failed, result.DeletedBytes,
	)
}

type processingArtifactFailureKindError interface {
	ProcessingArtifactFailureKind() string
}

func processingArtifactRetentionFailureKind(err error) string {
	var classified processingArtifactFailureKindError
	if !errors.As(err, &classified) {
		return "unknown"
	}
	switch kind := classified.ProcessingArtifactFailureKind(); kind {
	case "manifest_list", "manifest_invalid", "storage_resolve", "ownership", "object_delete", "manifest_delete":
		return kind
	default:
		return "unknown"
	}
}

func (r *ProcessingArtifactRetentionRunner) reportCounters(ctx context.Context) {
	if r.counters == nil {
		return
	}
	for _, counter := range r.counters.Snapshot() {
		logger.GetLogger(ctx).WithFields(logger.Fields{
			"stage":   counter.Stage,
			"outcome": counter.Outcome,
			"count":   counter.Count,
		}).Info("[processing-artifact-counter]")
	}
}

func processingArtifactRetentionSettingsValid(retentionDays, intervalHours int) bool {
	return retentionDays >= 0 && retentionDays <= config.MaxProcessingArtifactRetentionDays &&
		intervalHours > 0 && intervalHours <= config.MaxProcessingArtifactCleanupIntervalHours
}

func processingArtifactRetentionCutoff(now time.Time, retentionDays int) (time.Time, bool) {
	if retentionDays < 0 || retentionDays > config.MaxProcessingArtifactRetentionDays {
		return time.Time{}, false
	}
	return now.Add(-time.Duration(retentionDays) * 24 * time.Hour), true
}
