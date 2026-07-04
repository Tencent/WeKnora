// internal/application/service/cache_housekeeping.go
package service

import (
	"context"
	"time"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/logger"
	"gorm.io/gorm"
)

// CacheTableGCConfig holds per-table GC configuration
type CacheTableGCConfig struct {
	TableName  string
	Retention  time.Duration
	MaxRows    int64
	TimeColumn string
}

// buildCacheGCConfigs returns GC config for all 4 cache tables from the app config.
func buildCacheGCConfigs(cfg *config.Config) []CacheTableGCConfig {
	retention := 7 * 24 * time.Hour // default: 168 hours
	maxRows := int64(100000)

	if cfg != nil && cfg.CacheGC != nil {
		if cfg.CacheGC.RetentionHours > 0 {
			retention = time.Duration(cfg.CacheGC.RetentionHours) * time.Hour
		}
		if cfg.CacheGC.MaxRows > 0 {
			maxRows = cfg.CacheGC.MaxRows
		}
	}

	col := "last_accessed_at"
	return []CacheTableGCConfig{
		{TableName: "vlm_cache", Retention: retention, MaxRows: maxRows, TimeColumn: col},
		{TableName: "embedding_cache", Retention: retention, MaxRows: maxRows, TimeColumn: col},
		{TableName: "wiki_doc_map_cache", Retention: retention, MaxRows: maxRows, TimeColumn: col},
		{TableName: "graph_chunk_cache", Retention: retention, MaxRows: maxRows, TimeColumn: col},
	}
}

// CacheHousekeepingService manages periodic LRU GC for all cache tables
type CacheHousekeepingService struct {
	db       *gorm.DB
	configs  []CacheTableGCConfig
	interval time.Duration
	cancel   context.CancelFunc
}

func NewCacheHouseKeepingService(db *gorm.DB, cfg *config.Config) *CacheHousekeepingService {
	interval := 1 * time.Hour // default
	if cfg != nil && cfg.CacheGC != nil && cfg.CacheGC.IntervalMinutes > 0 {
		interval = time.Duration(cfg.CacheGC.IntervalMinutes) * time.Minute
	}
	return &CacheHousekeepingService{
		db:       db,
		configs:  buildCacheGCConfigs(cfg),
		interval: interval,
	}
}

// Start launches the GC ticker in a background goroutine.
func (c *CacheHousekeepingService) Start(ctx context.Context) {
	ctx, c.cancel = context.WithCancel(ctx)
	go c.run(ctx)
	logger.Infof(ctx, "[CacheGC] Housekeeping started, interval=%v, tables=%d", c.interval, len(c.configs))
}

// Stop cancels the background goroutine.
func (c *CacheHousekeepingService) Stop() {
	if c.cancel != nil {
		c.cancel()
	}
}

func (c *CacheHousekeepingService) run(ctx context.Context) {
	c.runGCCycles(ctx)
	ticker := time.NewTicker(c.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			logger.Infof(ctx, "[CacheGC] Housekeeping stopped")
			return
		case <-ticker.C:
			c.runGCCycles(ctx)
		}
	}
}

func (c *CacheHousekeepingService) runGCCycles(ctx context.Context) {
	for _, cfg := range c.configs {
		c.gcOneTable(ctx, cfg)
	}
}

// allowedCacheTables is the whitelist of table names that the GC is allowed to operate on.
var allowedCacheTables = map[string]bool{
	"vlm_cache":          true,
	"embedding_cache":    true,
	"wiki_doc_map_cache": true,
	"graph_chunk_cache":  true,
}

func (c *CacheHousekeepingService) gcOneTable(ctx context.Context, cfg CacheTableGCConfig) {
	if !allowedCacheTables[cfg.TableName] {
		logger.Errorf(ctx, "[CacheGC] refusing to GC unknown table: %s", cfg.TableName)
		return
	}

	// Step 1: Delete rows older than retention
	cutoff := time.Now().Add(-cfg.Retention)
	result := c.db.WithContext(ctx).
		Exec("DELETE FROM "+cfg.TableName+" WHERE "+cfg.TimeColumn+" < ?", cutoff)
	if result.Error != nil {
		logger.Errorf(ctx, "[CacheGC] %s retention cleanup failed: %v", cfg.TableName, result.Error)
		return
	}
	if result.RowsAffected > 0 {
		logger.Infof(ctx, "[CacheGC] %s deleted %d rows older than %v", cfg.TableName, result.RowsAffected, cfg.Retention)
	}

	// Step 2: LRU GC based on row count
	var count int64
	c.db.WithContext(ctx).Table(cfg.TableName).Count(&count)
	if count <= cfg.MaxRows {
		return
	}

	excess := count - cfg.MaxRows

	// Dialect-specific deletion for row-cap enforcement
	var sql string
	switch c.db.Dialector.Name() {
	case "postgres":
		sql = "DELETE FROM " + cfg.TableName + " WHERE ctid IN (SELECT ctid FROM " + cfg.TableName +
			" ORDER BY " + cfg.TimeColumn + " ASC LIMIT ?)"
	case "sqlite":
		sql = "DELETE FROM " + cfg.TableName + " WHERE rowid IN (SELECT rowid FROM " + cfg.TableName +
			" ORDER BY " + cfg.TimeColumn + " ASC LIMIT ?)"
	default:
		sql = "DELETE FROM " + cfg.TableName + " WHERE " + cfg.TimeColumn +
			" <= (SELECT t." + cfg.TimeColumn + " FROM (SELECT " + cfg.TimeColumn + " FROM " + cfg.TableName +
			" ORDER BY " + cfg.TimeColumn + " ASC LIMIT ?) t ORDER BY t." + cfg.TimeColumn + " DESC LIMIT 1)"
	}

	result = c.db.WithContext(ctx).Exec(sql, excess)
	if result.Error != nil {
		logger.Errorf(ctx, "[CacheGC] %s row-cap cleanup failed: %v", cfg.TableName, result.Error)
		return
	}
	logger.Infof(ctx, "[CacheGC] %s: trimmed %d rows to meet cap %d", cfg.TableName, result.RowsAffected, cfg.MaxRows)
}
