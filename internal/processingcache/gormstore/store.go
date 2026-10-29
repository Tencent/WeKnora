// Package gormstore persists processing results in the application's primary DB.
package gormstore

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/processingcache"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Model struct {
	TenantID  uint64    `gorm:"primaryKey;autoIncrement:false"`
	ScopeID   string    `gorm:"primaryKey;type:varchar(36)"`
	Stage     string    `gorm:"primaryKey;type:varchar(64)"`
	CacheKey  string    `gorm:"primaryKey;type:varchar(64)"`
	Payload   []byte    `gorm:"not null"`
	Checksum  string    `gorm:"type:char(64);not null"`
	ExpiresAt time.Time `gorm:"not null;index:idx_processing_cache_expires_at"`
	CreatedAt time.Time `gorm:"not null"`
}

func (Model) TableName() string { return "processing_cache" }

// SQLite stores timestamps as text. Use UTC consistently so comparisons have
// the same chronological order as PostgreSQL TIMESTAMPTZ comparisons.
func (m *Model) BeforeSave(*gorm.DB) error {
	m.ExpiresAt = m.ExpiresAt.UTC()
	m.CreatedAt = m.CreatedAt.UTC()
	return nil
}

type Store struct {
	db          *gorm.DB
	mu          sync.Mutex
	nextCleanup time.Time
}

var _ processingcache.Store = (*Store)(nil)

func New(db *gorm.DB) *Store { return &Store{db: db} }

func keyQuery(db *gorm.DB, tenant uint64, scope, stage, key string) *gorm.DB {
	return db.Where("tenant_id = ? AND scope_id = ? AND stage = ? AND cache_key = ?", tenant, scope, stage, key)
}

func (s *Store) lookup(ctx context.Context, tenant uint64, scope, stage, key string) (*processingcache.Record, error) {
	var row Model
	err := keyQuery(s.db.WithContext(ctx), tenant, scope, stage, key).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, processingcache.ErrMiss
	}
	if err != nil {
		return nil, err
	}
	return &processingcache.Record{TenantID: row.TenantID, ScopeID: row.ScopeID, Stage: row.Stage, Key: row.CacheKey, Payload: row.Payload, Checksum: row.Checksum, ExpiresAt: row.ExpiresAt}, nil
}

func (s *Store) Get(ctx context.Context, tenant uint64, scope, stage, key string) (*processingcache.Record, error) {
	r, err := s.lookup(ctx, tenant, scope, stage, key)
	if err == nil && !r.ExpiresAt.IsZero() && !time.Now().Before(r.ExpiresAt) {
		return nil, processingcache.ErrMiss
	}
	return r, err
}

// GetShared finds a live artifact without crossing the tenant boundary. The
// runtime validates its checksum and payload before copying it to a new scope.
func (s *Store) GetShared(ctx context.Context, tenant uint64, stage, key string) (*processingcache.Record, error) {
	var row Model
	err := s.db.WithContext(ctx).Where("tenant_id = ? AND stage = ? AND cache_key = ?", tenant, stage, key).
		Where("expires_at = ? OR expires_at > ?", time.Time{}, time.Now().UTC()).Order("scope_id").Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, processingcache.ErrMiss
	}
	if err != nil {
		return nil, err
	}
	return &processingcache.Record{TenantID: row.TenantID, ScopeID: row.ScopeID, Stage: row.Stage, Key: row.CacheKey, Payload: row.Payload, Checksum: row.Checksum, ExpiresAt: row.ExpiresAt}, nil
}

func (s *Store) PutIfAbsent(ctx context.Context, r *processingcache.Record) (*processingcache.Record, error) {
	if r == nil {
		return nil, errors.New("processing cache: nil record")
	}
	now := time.Now().UTC()
	s.cleanup(ctx, now)
	row := Model{TenantID: r.TenantID, ScopeID: r.ScopeID, Stage: r.Stage, CacheKey: r.Key, Payload: r.Payload, Checksum: r.Checksum, ExpiresAt: r.ExpiresAt.UTC(), CreatedAt: now}
	// An atomic conditional UPDATE lets only one writer replace an expired row.
	// A live winner cannot be overwritten, including across processes.
	err := keyQuery(s.db.WithContext(ctx).Model(&Model{}), r.TenantID, r.ScopeID, r.Stage, r.Key).
		Where("expires_at <> ? AND expires_at <= ?", time.Time{}, now).
		Updates(map[string]any{"payload": row.Payload, "checksum": row.Checksum, "expires_at": row.ExpiresAt, "created_at": now}).Error
	if err != nil {
		return nil, err
	}
	if err := s.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&row).Error; err != nil {
		return nil, err
	}
	return s.lookup(ctx, r.TenantID, r.ScopeID, r.Stage, r.Key)
}

func (s *Store) Delete(ctx context.Context, r *processingcache.Record) error {
	if r == nil {
		return nil
	}
	return keyQuery(s.db.WithContext(ctx), r.TenantID, r.ScopeID, r.Stage, r.Key).
		Where("checksum = ? AND expires_at = ?", r.Checksum, r.ExpiresAt.UTC()).Delete(&Model{}).Error
}

func (s *Store) DeleteScope(ctx context.Context, tenant uint64, scope string) error {
	return s.db.WithContext(ctx).Where("tenant_id = ? AND scope_id = ?", tenant, scope).Delete(&Model{}).Error
}

func (s *Store) cleanup(ctx context.Context, now time.Time) {
	s.mu.Lock()
	due := !now.Before(s.nextCleanup)
	if due {
		s.nextCleanup = now.Add(time.Minute)
	}
	s.mu.Unlock()
	if due {
		_, _ = s.DeleteExpired(ctx, now, 1000)
	}
}

// DeleteExpired bounds both the candidate scan and deletion predicates. The
// observed expiry/checksum protects an entry refreshed after the scan.
func (s *Store) DeleteExpired(ctx context.Context, before time.Time, limit int) (int64, error) {
	if limit <= 0 {
		return 0, nil
	}
	var rows []Model
	err := s.db.WithContext(ctx).Select("tenant_id", "scope_id", "stage", "cache_key", "checksum", "expires_at").
		Where("expires_at <> ? AND expires_at <= ?", time.Time{}, before.UTC()).Order("expires_at").Limit(min(limit, 1000)).Find(&rows).Error
	if err != nil {
		return 0, err
	}
	var total int64
	for start := 0; start < len(rows); start += 100 {
		query := s.db.WithContext(ctx)
		for _, row := range rows[start:min(start+100, len(rows))] {
			query = query.Or("tenant_id = ? AND scope_id = ? AND stage = ? AND cache_key = ? AND checksum = ? AND expires_at = ?", row.TenantID, row.ScopeID, row.Stage, row.CacheKey, row.Checksum, row.ExpiresAt)
		}
		result := query.Delete(&Model{})
		total += result.RowsAffected
		if result.Error != nil {
			return total, result.Error
		}
	}
	return total, nil
}
