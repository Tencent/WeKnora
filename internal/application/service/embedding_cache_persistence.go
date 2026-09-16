package service

import (
	"context"
	"encoding/binary"
	"errors"
	"math"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/embedding"
	"github.com/Tencent/WeKnora/internal/types"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type embeddingCacheRecord struct {
	TenantID  uint64    `gorm:"primaryKey"`
	CacheKey  string    `gorm:"primaryKey;size:64"`
	ModelID   string    `gorm:"size:64;index"`
	Vector    []byte    `gorm:"not null"`
	ExpiresAt time.Time `gorm:"not null;index"`
	UpdatedAt time.Time `gorm:"not null"`
}

func (embeddingCacheRecord) TableName() string { return "embedding_cache_entries" }

// EmbeddingCachePersistence is the tenant-isolated durable tier below the
// process-local LRU. Cache keys are SHA-256 digests; raw document text is never
// stored in this table.
type EmbeddingCachePersistence struct {
	db              *gorm.DB
	startOnce       sync.Once
	stopOnce        sync.Once
	started         atomic.Bool
	stopCh          chan struct{}
	doneCh          chan struct{}
	cleanupInterval time.Duration
	cleanupDelay    time.Duration
}

func NewEmbeddingCachePersistence(db *gorm.DB) *EmbeddingCachePersistence {
	return &EmbeddingCachePersistence{
		db: db, stopCh: make(chan struct{}), doneCh: make(chan struct{}),
		cleanupInterval: 24 * time.Hour, cleanupDelay: 10 * time.Minute,
	}
}

func (p *EmbeddingCachePersistence) Start(ctx context.Context) {
	if p == nil || p.db == nil {
		return
	}
	p.startOnce.Do(func() {
		p.started.Store(true)
		embedding.SetPersistentCache(p)
		logger.Infof(ctx, "[embedding-cache] durable tenant cache enabled")
		go p.cleanupLoop()
	})
}

func (p *EmbeddingCachePersistence) Stop() {
	if p == nil || !p.started.Load() {
		return
	}
	p.stopOnce.Do(func() {
		embedding.SetPersistentCache(nil)
		close(p.stopCh)
	})
	<-p.doneCh
}

func (p *EmbeddingCachePersistence) Get(
	ctx context.Context, keys []string, now time.Time,
) (map[string]embedding.PersistentCacheEntry, error) {
	tenantID, ok := types.TenantIDFromContext(ctx)
	if !ok || tenantID == 0 || len(keys) == 0 {
		return map[string]embedding.PersistentCacheEntry{}, nil
	}
	var records []embeddingCacheRecord
	if err := p.db.WithContext(ctx).
		Where("tenant_id = ? AND cache_key IN ? AND expires_at > ?", tenantID, keys, now).
		Find(&records).Error; err != nil {
		return nil, err
	}
	result := make(map[string]embedding.PersistentCacheEntry, len(records))
	for _, record := range records {
		vector, err := decodeEmbeddingVector(record.Vector)
		if err != nil {
			continue
		}
		result[record.CacheKey] = embedding.PersistentCacheEntry{
			Vector: vector, ExpiresAt: record.ExpiresAt,
		}
	}
	return result, nil
}

func (p *EmbeddingCachePersistence) Put(
	ctx context.Context, modelID string, vectors map[string][]float32, expiresAt time.Time,
) error {
	tenantID, ok := types.TenantIDFromContext(ctx)
	if !ok || tenantID == 0 || len(vectors) == 0 {
		return nil
	}
	records := make([]embeddingCacheRecord, 0, len(vectors))
	now := time.Now().UTC()
	for key, vector := range vectors {
		if key == "" || len(vector) == 0 {
			continue
		}
		records = append(records, embeddingCacheRecord{
			TenantID: tenantID, CacheKey: key, ModelID: modelID,
			Vector: encodeEmbeddingVector(vector), ExpiresAt: expiresAt.UTC(), UpdatedAt: now,
		})
	}
	if len(records) == 0 {
		return nil
	}
	writeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return p.db.WithContext(writeCtx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "tenant_id"}, {Name: "cache_key"}},
		DoUpdates: clause.AssignmentColumns([]string{"model_id", "vector", "expires_at", "updated_at"}),
	}).CreateInBatches(records, 100).Error
}

func (p *EmbeddingCachePersistence) cleanupLoop() {
	defer close(p.doneCh)
	timer := time.NewTimer(p.cleanupDelay)
	defer timer.Stop()
	select {
	case <-timer.C:
		p.purgeExpired()
	case <-p.stopCh:
		return
	}
	ticker := time.NewTicker(p.cleanupInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			p.purgeExpired()
		case <-p.stopCh:
			return
		}
	}
}

func (p *EmbeddingCachePersistence) purgeExpired() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	result := p.db.WithContext(ctx).Where("expires_at <= ?", time.Now().UTC()).Delete(&embeddingCacheRecord{})
	if result.Error != nil {
		logger.Warnf(ctx, "[embedding-cache] cleanup failed: %v", result.Error)
	} else if result.RowsAffected > 0 {
		logger.Infof(ctx, "[embedding-cache] cleanup deleted=%d", result.RowsAffected)
	}
}

func encodeEmbeddingVector(vector []float32) []byte {
	encoded := make([]byte, len(vector)*4)
	for index, value := range vector {
		binary.LittleEndian.PutUint32(encoded[index*4:], math.Float32bits(value))
	}
	return encoded
}

func decodeEmbeddingVector(encoded []byte) ([]float32, error) {
	if len(encoded) == 0 || len(encoded)%4 != 0 {
		return nil, errors.New("invalid embedding vector encoding")
	}
	vector := make([]float32, len(encoded)/4)
	for index := range vector {
		vector[index] = math.Float32frombits(binary.LittleEndian.Uint32(encoded[index*4:]))
	}
	return vector, nil
}
