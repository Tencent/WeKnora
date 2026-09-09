package service

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"gorm.io/gorm"
)

const (
	modelCallFlushInterval   = time.Second
	modelCallCleanupInterval = 24 * time.Hour
	modelCallCleanupDelay    = 10 * time.Minute
)

// ModelCallRecorder persists tenant-scoped call metadata asynchronously. It is
// deliberately best-effort: observability failures never fail or delay a model
// response, and prompt/response bodies are not accepted by the observation type.
type ModelCallRecorder struct {
	db              *gorm.DB
	enabled         bool
	retentionDays   int
	batchSize       int
	queue           chan types.LLMCallObservation
	fingerprintKey  []byte
	flushInterval   time.Duration
	cleanupInterval time.Duration
	cleanupDelay    time.Duration
	startOnce       sync.Once
	stopOnce        sync.Once
	started         atomic.Bool
	dropped         atomic.Uint64
	stopCh          chan struct{}
	doneCh          chan struct{}
}

func NewModelCallRecorder(cfg *config.Config, db *gorm.DB) *ModelCallRecorder {
	enabled, retentionDays, queueSize, batchSize := true, 30, 4096, 100
	if cfg != nil && cfg.ModelObservability != nil {
		enabled = cfg.ModelObservability.IsEnabled()
		retentionDays = cfg.ModelObservability.RetentionDays
		queueSize = cfg.ModelObservability.QueueSize
		batchSize = cfg.ModelObservability.BatchSize
	}
	if queueSize <= 0 {
		queueSize = 4096
	}
	if batchSize <= 0 {
		batchSize = 100
	}
	if retentionDays <= 0 {
		retentionDays = 30
	}
	return &ModelCallRecorder{
		db: db, enabled: enabled, retentionDays: retentionDays, batchSize: batchSize,
		queue:          make(chan types.LLMCallObservation, queueSize),
		fingerprintKey: []byte(os.Getenv("WEKNORA_MODEL_CALL_FINGERPRINT_KEY")),
		flushInterval:  modelCallFlushInterval, cleanupInterval: modelCallCleanupInterval,
		cleanupDelay: modelCallCleanupDelay, stopCh: make(chan struct{}), doneCh: make(chan struct{}),
	}
}

func (r *ModelCallRecorder) Start(ctx context.Context) {
	if r == nil || r.db == nil {
		return
	}
	r.startOnce.Do(func() {
		r.started.Store(true)
		if !r.enabled {
			logger.Infof(ctx, "[model-observability] disabled")
			close(r.doneCh)
			return
		}
		types.SetGlobalLLMCallObserver(r)
		logger.Infof(ctx, "[model-observability] enabled: retention_days=%d queue_size=%d batch_size=%d", r.retentionDays, cap(r.queue), r.batchSize)
		go r.loop()
	})
}

func (r *ModelCallRecorder) Stop() {
	if r == nil || !r.started.Load() {
		return
	}
	r.stopOnce.Do(func() {
		types.SetGlobalLLMCallObserver(nil)
		close(r.stopCh)
	})
	<-r.doneCh
}

func (r *ModelCallRecorder) ObserveLLMCall(observation types.LLMCallObservation) {
	if r == nil || !r.enabled || observation.TenantID == 0 {
		return
	}
	// Provider errors can contain request excerpts. Keep only the success bit
	// for global telemetry; evaluation-scoped records retain their existing
	// diagnostic error field through the request observer.
	observation.Error = ""
	observation.PromptPrefixFingerprint = r.protectFingerprint(observation.PromptPrefixFingerprint)
	select {
	case r.queue <- observation:
	default:
		dropped := r.dropped.Add(1)
		if dropped == 1 || dropped&(dropped-1) == 0 {
			logger.Warnf(context.Background(), "[model-observability] queue full; dropped=%d", dropped)
		}
	}
}

func (r *ModelCallRecorder) protectFingerprint(fingerprint string) string {
	return protectModelCallFingerprint(fingerprint, r.fingerprintKey)
}

func protectModelCallFingerprint(fingerprint string, key []byte) string {
	if fingerprint == "" || len(key) == 0 {
		return ""
	}
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(fingerprint))
	return hex.EncodeToString(mac.Sum(nil))
}

func (r *ModelCallRecorder) loop() {
	defer close(r.doneCh)
	flushTicker := time.NewTicker(r.flushInterval)
	defer flushTicker.Stop()
	cleanupTimer := time.NewTimer(r.cleanupDelay)
	defer cleanupTimer.Stop()
	var cleanupTicker *time.Ticker
	var cleanupC <-chan time.Time
	batch := make([]types.LLMCallObservation, 0, r.batchSize)
	flush := func() {
		if len(batch) == 0 {
			return
		}
		r.persist(batch)
		batch = batch[:0]
	}
	for {
		select {
		case observation := <-r.queue:
			batch = append(batch, observation)
			if len(batch) >= r.batchSize {
				flush()
			}
		case <-flushTicker.C:
			flush()
		case <-cleanupTimer.C:
			r.purgeExpired()
			cleanupTicker = time.NewTicker(r.cleanupInterval)
			cleanupC = cleanupTicker.C
		case <-cleanupC:
			r.purgeExpired()
		case <-r.stopCh:
			if cleanupTicker != nil {
				cleanupTicker.Stop()
			}
			for {
				select {
				case observation := <-r.queue:
					batch = append(batch, observation)
					if len(batch) >= r.batchSize {
						flush()
					}
				default:
					flush()
					return
				}
			}
		}
	}
}

func (r *ModelCallRecorder) persist(observations []types.LLMCallObservation) {
	records := make([]*evaluationModelCallRecord, 0, len(observations))
	now := time.Now().UTC()
	for _, observation := range observations {
		records = append(records, newModelCallRecord(nil, observation.TenantID, observation, now))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := r.db.WithContext(ctx).CreateInBatches(records, r.batchSize).Error; err != nil {
		logger.Warnf(ctx, "[model-observability] batch write failed: calls=%d err=%v", len(records), err)
	}
}

func (r *ModelCallRecorder) purgeExpired() {
	cutoff := time.Now().UTC().AddDate(0, 0, -r.retentionDays)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	result := r.db.WithContext(ctx).Where("created_at < ?", cutoff).Delete(&evaluationModelCallRecord{})
	if result.Error != nil {
		logger.Warnf(ctx, "[model-observability] retention sweep failed: %v", result.Error)
	} else if result.RowsAffected > 0 {
		logger.Infof(ctx, "[model-observability] retention sweep deleted=%d", result.RowsAffected)
	}
}
