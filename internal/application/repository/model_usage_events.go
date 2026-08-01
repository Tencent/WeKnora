package repository

import (
	"context"
	"fmt"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
)

// modelUsageRepository implements interfaces.ModelUsageRepository on top of
// the model_usage_events table (see migrations 000079).
type modelUsageRepository struct {
	db *gorm.DB
}

const (
	defaultModelUsageTimelineBucket = 2 * time.Hour
	// minModelUsageTimelineBucketCalls hides timeline buckets with too little
	// traffic: a bucket with a handful of calls renders as noise next to
	// high-traffic buckets on the stacked-bar chart.
	minModelUsageTimelineBucketCalls = int64(100)
)

// NewModelUsageRepository creates a repository for model usage metering events.
func NewModelUsageRepository(db *gorm.DB) interfaces.ModelUsageRepository {
	return &modelUsageRepository{db: db}
}

func (r *modelUsageRepository) Create(ctx context.Context, event *types.ModelUsageEvent) error {
	return r.db.WithContext(ctx).Create(event).Error
}

func (r *modelUsageRepository) Report(
	ctx context.Context,
	tenantID uint64,
	query types.ModelUsageQuery,
) (*types.ModelUsageReport, error) {
	models, err := r.modelStats(ctx, tenantID, query)
	if err != nil {
		return nil, err
	}
	timeline, err := r.timeline(ctx, tenantID, query)
	if err != nil {
		return nil, err
	}
	recent, err := r.recentEvents(ctx, tenantID, query)
	if err != nil {
		return nil, err
	}
	if models == nil {
		models = []types.ModelUsageModelStats{}
	}
	if timeline == nil {
		timeline = []types.ModelUsageTimelinePoint{}
	}
	if recent == nil {
		recent = []types.ModelUsageEvent{}
	}

	report := &types.ModelUsageReport{
		Summary: types.ModelUsageSummary{
			WindowStart:    query.Start,
			WindowEnd:      query.End,
			RefreshSeconds: 5,
		},
		Models:       models,
		Timeline:     timeline,
		RecentEvents: recent,
	}
	for i := range report.Models {
		row := &report.Models[i]
		row.SuccessRate = successRate(row.Calls, row.ErrorCount)
		if row.Calls > 0 {
			row.AvgTokensPerCall = float64(row.TotalTokens) / float64(row.Calls)
		}
		report.Summary.TotalCalls += row.Calls
		report.Summary.TotalTokens += row.TotalTokens
		report.Summary.PromptTokens += row.PromptTokens
		report.Summary.CompletionTokens += row.CompletionTokens
		report.Summary.CachedTokens += row.CachedTokens
		report.Summary.ErrorCount += row.ErrorCount
	}
	report.Summary.SuccessRate = successRate(report.Summary.TotalCalls, report.Summary.ErrorCount)
	return report, nil
}

func (r *modelUsageRepository) filteredEvents(
	ctx context.Context,
	tenantID uint64,
	query types.ModelUsageQuery,
) *gorm.DB {
	tx := r.db.WithContext(ctx).
		Table("model_usage_events AS e").
		Where("e.tenant_id = ? AND e.created_at >= ? AND e.created_at < ?", tenantID, query.Start, query.End)
	if query.ModelType != "" {
		tx = tx.Where("e.model_type = ?", query.ModelType)
	}
	if query.ModelID != "" {
		tx = tx.Where("e.model_id = ?", query.ModelID)
	}
	return tx
}

func (r *modelUsageRepository) modelStats(
	ctx context.Context,
	tenantID uint64,
	query types.ModelUsageQuery,
) ([]types.ModelUsageModelStats, error) {
	// MAX() loses the column's temporal type under sqlite (the driver returns
	// a string that cannot scan into *time.Time), so aggregate as epoch
	// seconds and convert in Go on both dialects.
	lastUsedExpr := "CAST(FLOOR(EXTRACT(EPOCH FROM MAX(e.created_at))) AS BIGINT)"
	if r.db.Name() == "sqlite" {
		lastUsedExpr = "CAST(strftime('%s', MAX(e.created_at)) AS INTEGER)"
	}

	type modelStatsRow struct {
		ModelID          string
		ModelName        string
		DisplayName      string
		ModelType        types.ModelType
		ModelSource      types.ModelSource
		Provider         string
		Calls            int64
		PromptTokens     int64
		CompletionTokens int64
		CachedTokens     int64
		TotalTokens      int64
		InputItems       int64
		DurationMs       int64
		ErrorCount       int64
		LastUsedEpoch    int64
	}
	var rows []modelStatsRow
	err := r.filteredEvents(ctx, tenantID, query).
		Select(fmt.Sprintf(`
			e.model_id AS model_id,
			e.model_name AS model_name,
			COALESCE(m.display_name, '') AS display_name,
			e.model_type AS model_type,
			e.model_source AS model_source,
			e.provider AS provider,
			COUNT(*) AS calls,
			COALESCE(SUM(e.prompt_tokens), 0) AS prompt_tokens,
			COALESCE(SUM(e.completion_tokens), 0) AS completion_tokens,
			COALESCE(SUM(e.cached_tokens), 0) AS cached_tokens,
			COALESCE(SUM(e.total_tokens), 0) AS total_tokens,
			COALESCE(SUM(e.input_items), 0) AS input_items,
			COALESCE(SUM(e.duration_ms), 0) AS duration_ms,
			COALESCE(SUM(CASE WHEN e.success THEN 0 ELSE 1 END), 0) AS error_count,
			%s AS last_used_epoch`, lastUsedExpr)).
		Joins(`LEFT JOIN models m ON m.id = e.model_id
			AND (m.tenant_id = e.tenant_id OR m.is_builtin = true)
			AND m.deleted_at IS NULL`).
		Group("e.model_id, e.model_name, m.display_name, e.model_type, e.model_source, e.provider").
		Order("total_tokens DESC, calls DESC, model_name ASC").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}

	stats := make([]types.ModelUsageModelStats, 0, len(rows))
	for _, row := range rows {
		lastUsedAt := time.Unix(row.LastUsedEpoch, 0).UTC()
		stats = append(stats, types.ModelUsageModelStats{
			ModelID:          row.ModelID,
			ModelName:        row.ModelName,
			DisplayName:      row.DisplayName,
			ModelType:        row.ModelType,
			ModelSource:      row.ModelSource,
			Provider:         row.Provider,
			Calls:            row.Calls,
			PromptTokens:     row.PromptTokens,
			CompletionTokens: row.CompletionTokens,
			CachedTokens:     row.CachedTokens,
			TotalTokens:      row.TotalTokens,
			InputItems:       row.InputItems,
			DurationMs:       row.DurationMs,
			ErrorCount:       row.ErrorCount,
			LastUsedAt:       &lastUsedAt,
		})
	}
	return stats, nil
}

func (r *modelUsageRepository) timeline(
	ctx context.Context,
	tenantID uint64,
	query types.ModelUsageQuery,
) ([]types.ModelUsageTimelinePoint, error) {
	bucketSeconds := int64(query.BucketSize.Seconds())
	if bucketSeconds <= 0 {
		bucketSeconds = int64(defaultModelUsageTimelineBucket.Seconds())
	}
	bucketExpr := "CAST(FLOOR(EXTRACT(EPOCH FROM e.created_at) / ?) * ? AS BIGINT)"
	if r.db.Name() == "sqlite" {
		bucketExpr = "(CAST(strftime('%s', e.created_at) AS INTEGER) / ?) * ?"
	}

	type row struct {
		BucketEpoch      int64
		ModelID          string
		ModelName        string
		ModelType        types.ModelType
		Calls            int64
		PromptTokens     int64
		CompletionTokens int64
		CachedTokens     int64
		TotalTokens      int64
		ErrorCount       int64
	}
	var rows []row
	err := r.filteredEvents(ctx, tenantID, query).
		Select(fmt.Sprintf(`
			%s AS bucket_epoch,
			e.model_id AS model_id,
			e.model_name AS model_name,
			e.model_type AS model_type,
			COUNT(*) AS calls,
			COALESCE(SUM(e.prompt_tokens), 0) AS prompt_tokens,
			COALESCE(SUM(e.completion_tokens), 0) AS completion_tokens,
			COALESCE(SUM(e.cached_tokens), 0) AS cached_tokens,
			COALESCE(SUM(e.total_tokens), 0) AS total_tokens,
			COALESCE(SUM(CASE WHEN e.success THEN 0 ELSE 1 END), 0) AS error_count`, bucketExpr), bucketSeconds, bucketSeconds).
		Group("1, e.model_id, e.model_name, e.model_type").
		Order("bucket_epoch ASC, total_tokens DESC").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}

	bucketCalls := make(map[int64]int64, len(rows))
	for _, item := range rows {
		bucketCalls[item.BucketEpoch] += item.Calls
	}

	points := make([]types.ModelUsageTimelinePoint, 0, len(rows))
	for _, r := range rows {
		if bucketCalls[r.BucketEpoch] < minModelUsageTimelineBucketCalls {
			continue
		}
		points = append(points, types.ModelUsageTimelinePoint{
			BucketStart:      time.Unix(r.BucketEpoch, 0).UTC(),
			ModelID:          r.ModelID,
			ModelName:        r.ModelName,
			ModelType:        r.ModelType,
			Calls:            r.Calls,
			PromptTokens:     r.PromptTokens,
			CompletionTokens: r.CompletionTokens,
			CachedTokens:     r.CachedTokens,
			TotalTokens:      r.TotalTokens,
			ErrorCount:       r.ErrorCount,
		})
	}
	return points, nil
}

func (r *modelUsageRepository) recentEvents(
	ctx context.Context,
	tenantID uint64,
	query types.ModelUsageQuery,
) ([]types.ModelUsageEvent, error) {
	limit := query.RecentLimit
	if limit <= 0 || limit > 100 {
		limit = 30
	}
	var events []types.ModelUsageEvent
	err := r.filteredEvents(ctx, tenantID, query).
		Select("e.*").
		Order("e.created_at DESC, e.id DESC").
		Limit(limit).
		Find(&events).Error
	return events, err
}

func successRate(calls, errors int64) float64 {
	if calls <= 0 {
		return 1
	}
	successes := calls - errors
	if successes < 0 {
		successes = 0
	}
	return float64(successes) / float64(calls)
}
