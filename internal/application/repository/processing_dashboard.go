package repository

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"gorm.io/gorm"
)

type ProcessingDashboardRepository interface {
	ListCandidateKnowledge(ctx context.Context, filter types.ProcessingDashboardFilter) ([]types.ProcessingKnowledgeRow, error)
	ListKnowledgeRowsByIDs(ctx context.Context, filter types.ProcessingDashboardFilter, ids []string) ([]types.ProcessingKnowledgeRow, error)
	ListLatestAttempts(ctx context.Context, knowledgeIDs []string) ([]types.ProcessingAttemptRow, error)
	ListCanonicalAndParentSpans(ctx context.Context, knowledgeIDs []string) ([]types.KnowledgeProcessingSpan, error)
	AggregateFanoutSpans(ctx context.Context, knowledgeIDs []string) ([]types.ProcessingFanoutSpanBucket, []types.KnowledgeProcessingSpan, error)
	ListWikiPendingOps(ctx context.Context, filter types.ProcessingDashboardFilter, knowledgeIDs []string) ([]types.ProcessingWikiPendingRow, error)
}

type processingDashboardRepository struct {
	db *gorm.DB
}

func NewProcessingDashboardRepository(db *gorm.DB) ProcessingDashboardRepository {
	return &processingDashboardRepository{db: db}
}

func (r *processingDashboardRepository) ListCandidateKnowledge(
	ctx context.Context,
	filter types.ProcessingDashboardFilter,
) ([]types.ProcessingKnowledgeRow, error) {
	if len(filter.AccessibleScopes) == 0 {
		return nil, nil
	}
	var rows []types.ProcessingKnowledgeRow
	q := r.baseKnowledgeRowsQuery(ctx, filter).
		Where("knowledges.parse_status IN ?", []string{
			types.ParseStatusPending,
			types.ParseStatusProcessing,
			types.ParseStatusFinalizing,
		}).
		Order("knowledges.updated_at DESC")
	if err := q.Scan(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

func (r *processingDashboardRepository) ListKnowledgeRowsByIDs(
	ctx context.Context,
	filter types.ProcessingDashboardFilter,
	ids []string,
) ([]types.ProcessingKnowledgeRow, error) {
	if len(ids) == 0 || len(filter.AccessibleScopes) == 0 {
		return nil, nil
	}
	out := make([]types.ProcessingKnowledgeRow, 0, len(ids))
	for _, batch := range batchStrings(ids, 500) {
		var rows []types.ProcessingKnowledgeRow
		q := r.baseKnowledgeRowsQuery(ctx, filter).
			Where("knowledges.id IN ?", batch)
		if err := q.Scan(&rows).Error; err != nil {
			return nil, err
		}
		out = append(out, rows...)
	}
	return out, nil
}

func (r *processingDashboardRepository) baseKnowledgeRowsQuery(
	ctx context.Context,
	filter types.ProcessingDashboardFilter,
) *gorm.DB {
	q := r.db.WithContext(ctx).
		Table("knowledges").
		Select(`knowledges.id,
			knowledges.tenant_id,
			knowledges.knowledge_base_id,
			knowledge_bases.name AS knowledge_base_name,
			knowledges.title,
			knowledges.file_name,
			knowledges.parse_status,
			knowledges.pending_subtasks_count,
			knowledges.updated_at,
			knowledges.created_at,
			knowledges.error_message`).
		Joins("JOIN knowledge_bases ON knowledge_bases.id = knowledges.knowledge_base_id AND knowledge_bases.tenant_id = knowledges.tenant_id").
		Where("knowledge_bases.type = ?", types.KnowledgeBaseTypeDocument).
		Where("knowledge_bases.is_temporary = ?", false).
		Where("knowledges.deleted_at IS NULL")
	q = applyScopeTupleFilter(q, "knowledges.tenant_id", "knowledges.knowledge_base_id", filter.AccessibleScopes)
	if filter.KnowledgeBaseID != "" {
		q = q.Where("knowledges.knowledge_base_id = ?", filter.KnowledgeBaseID)
	}
	if strings.TrimSpace(filter.Keyword) != "" {
		escaped := escapeLikeKeyword(strings.TrimSpace(filter.Keyword))
		q = q.Where("(knowledges.file_name LIKE ? OR knowledges.title LIKE ?)", "%"+escaped+"%", "%"+escaped+"%")
	}
	return q
}

func (r *processingDashboardRepository) ListLatestAttempts(
	ctx context.Context,
	knowledgeIDs []string,
) ([]types.ProcessingAttemptRow, error) {
	out := make([]types.ProcessingAttemptRow, 0, len(knowledgeIDs))
	for _, batch := range batchStrings(knowledgeIDs, 500) {
		var rows []types.ProcessingAttemptRow
		err := r.db.WithContext(ctx).
			Table("knowledge_processing_spans").
			Select("knowledge_id, COALESCE(MAX(attempt), 0) AS attempt").
			Where("knowledge_id IN ?", batch).
			Group("knowledge_id").
			Scan(&rows).Error
		if err != nil {
			return nil, err
		}
		out = append(out, rows...)
	}
	return out, nil
}

func (r *processingDashboardRepository) ListCanonicalAndParentSpans(
	ctx context.Context,
	knowledgeIDs []string,
) ([]types.KnowledgeProcessingSpan, error) {
	out := make([]types.KnowledgeProcessingSpan, 0)
	for _, batch := range batchStrings(knowledgeIDs, 500) {
		var latestIDs []int64
		err := r.db.WithContext(ctx).
			Table("knowledge_processing_spans").
			Select("MAX(id)").
			Where("knowledge_id IN ?", batch).
			Where(`name IN ?`,
				[]string{
					types.StageDocReader,
					types.StageChunking,
					types.StageEmbedding,
					types.StageMultimodal,
					types.StagePostProcess,
					"postprocess.summary",
					"postprocess.question",
					"postprocess.wiki",
				}).
			Group("knowledge_id, attempt, name").
			Scan(&latestIDs).Error
		if err != nil {
			return nil, err
		}
		if len(latestIDs) == 0 {
			continue
		}
		for _, idBatch := range batchInt64s(latestIDs, 500) {
			var rows []types.KnowledgeProcessingSpan
			if err := r.db.WithContext(ctx).
				Where("id IN ?", idBatch).
				Order("knowledge_id ASC, attempt ASC, id ASC").
				Find(&rows).Error; err != nil {
				return nil, err
			}
			out = append(out, rows...)
		}
	}
	return out, nil
}

func (r *processingDashboardRepository) AggregateFanoutSpans(
	ctx context.Context,
	knowledgeIDs []string,
) ([]types.ProcessingFanoutSpanBucket, []types.KnowledgeProcessingSpan, error) {
	if len(knowledgeIDs) == 0 {
		return nil, nil, nil
	}
	prefixes := []struct {
		stage  types.ProcessingLogicalStage
		prefix string
	}{
		{types.ProcessingStageMultimodal, "multimodal.image["},
		{types.ProcessingStageQuestion, "postprocess.question.batch["},
		{types.ProcessingStageGraph, "postprocess.graph.chunk["},
		{types.ProcessingStageWiki, "postprocess.wiki."},
	}
	buckets := make([]types.ProcessingFanoutSpanBucket, 0)
	details := make([]types.KnowledgeProcessingSpan, 0)
	for _, batch := range batchStrings(knowledgeIDs, 500) {
		for _, prefix := range prefixes {
			latest := r.db.WithContext(ctx).
				Table("knowledge_processing_spans").
				Select("MAX(id) AS id").
				Where("knowledge_id IN ?", batch).
				Where("name LIKE ?", prefix.prefix+"%").
				Group("knowledge_id, attempt, name")

			var histRows []struct {
				KnowledgeID string `gorm:"column:knowledge_id"`
				Attempt     int    `gorm:"column:attempt"`
				Stage       string `gorm:"column:stage"`
				Status      string `gorm:"column:status"`
				ErrorCode   string `gorm:"column:error_code"`
				Count       int    `gorm:"column:count"`
				UpdatedAt   string `gorm:"column:updated_at"`
			}
			if err := r.db.WithContext(ctx).
				Table("knowledge_processing_spans AS s").
				Select("? AS stage, s.knowledge_id, s.attempt, s.status, COALESCE(s.error_code, '') AS error_code, COUNT(*) AS count, MAX(s.updated_at) AS updated_at", string(prefix.stage)).
				Joins("JOIN (?) AS latest ON latest.id = s.id", latest).
				Group("s.knowledge_id, s.attempt, s.status, s.error_code").
				Scan(&histRows).Error; err != nil {
				return nil, nil, err
			}
			for _, row := range histRows {
				buckets = append(buckets, types.ProcessingFanoutSpanBucket{
					KnowledgeID: row.KnowledgeID,
					Attempt:     row.Attempt,
					Stage:       types.ProcessingLogicalStage(row.Stage),
					Status:      row.Status,
					ErrorCode:   row.ErrorCode,
					Count:       row.Count,
					UpdatedAt:   parseDashboardDBTime(row.UpdatedAt),
				})
			}

			var rows []types.KnowledgeProcessingSpan
			if err := r.db.WithContext(ctx).
				Table("knowledge_processing_spans AS s").
				Select("s.*").
				Joins("JOIN (?) AS latest ON latest.id = s.id", latest).
				Where("(s.status IN ? OR (s.status = ? AND s.error_code = ?))",
					[]string{types.SpanStatusRunning, types.SpanStatusPending, types.SpanStatusFailed},
					types.SpanStatusCancelled,
					"TASK_SUPERSEDED",
				).
				Order("s.knowledge_id ASC, s.attempt ASC, s.id ASC").
				Find(&rows).Error; err != nil {
				return nil, nil, err
			}
			details = append(details, rows...)
		}
	}
	return buckets, details, nil
}

func (r *processingDashboardRepository) ListWikiPendingOps(
	ctx context.Context,
	filter types.ProcessingDashboardFilter,
	knowledgeIDs []string,
) ([]types.ProcessingWikiPendingRow, error) {
	if len(filter.AccessibleScopes) == 0 {
		return nil, nil
	}
	out := make([]types.ProcessingWikiPendingRow, 0)
	run := func(ids []string) error {
		var rows []struct {
			KnowledgeID string `gorm:"column:knowledge_id"`
			QueuedAt    string `gorm:"column:queued_at"`
			FailCount   int    `gorm:"column:fail_count"`
			CursorID    int64  `gorm:"column:cursor_id"`
		}
		q := r.db.WithContext(ctx).
			Table("task_pending_ops").
			Select("dedup_key AS knowledge_id, MIN(enqueued_at) AS queued_at, MAX(fail_count) AS fail_count, MIN(id) AS cursor_id").
			Where("task_type = ? AND scope = ?", types.TypeWikiIngest, "knowledge_base").
			Where("dedup_key <> ''")
		q = applyScopeTupleFilter(q, "tenant_id", "scope_id", filter.AccessibleScopes)
		if filter.KnowledgeBaseID != "" {
			q = q.Where("scope_id = ?", filter.KnowledgeBaseID)
		}
		if len(ids) > 0 {
			q = q.Where("dedup_key IN ?", ids)
		}
		if err := q.Group("dedup_key").Scan(&rows).Error; err != nil {
			return err
		}
		for _, row := range rows {
			out = append(out, types.ProcessingWikiPendingRow{
				KnowledgeID: row.KnowledgeID,
				QueuedAt:    parseDashboardDBTime(row.QueuedAt),
				FailCount:   row.FailCount,
				CursorID:    row.CursorID,
			})
		}
		return nil
	}
	if len(knowledgeIDs) == 0 {
		if err := run(nil); err != nil {
			return nil, err
		}
		return out, nil
	}
	for _, batch := range batchStrings(knowledgeIDs, 500) {
		if err := run(batch); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func parseDashboardDBTime(raw string) time.Time {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}
	}
	layouts := []string{
		time.RFC3339Nano,
		"2006-01-02 15:04:05.999999999-07:00",
		"2006-01-02 15:04:05.999999999Z07:00",
		"2006-01-02 15:04:05.999999999",
		"2006-01-02 15:04:05",
	}
	for _, layout := range layouts {
		if t, err := time.Parse(layout, raw); err == nil {
			return t
		}
	}
	return time.Time{}
}

func applyScopeTupleFilter(q *gorm.DB, tenantColumn, kbColumn string, scopes []types.KnowledgeSearchScope) *gorm.DB {
	parts := make([]string, 0, len(scopes))
	args := make([]any, 0, len(scopes)*2)
	for _, scope := range scopes {
		if scope.TenantID == 0 || scope.KBID == "" {
			continue
		}
		parts = append(parts, fmt.Sprintf("(%s = ? AND %s = ?)", tenantColumn, kbColumn))
		args = append(args, scope.TenantID, scope.KBID)
	}
	if len(parts) == 0 {
		return q.Where("1 = 0")
	}
	return q.Where("("+strings.Join(parts, " OR ")+")", args...)
}

func batchStrings(in []string, size int) [][]string {
	if size <= 0 || len(in) == 0 {
		return nil
	}
	out := make([][]string, 0, (len(in)+size-1)/size)
	for start := 0; start < len(in); start += size {
		end := start + size
		if end > len(in) {
			end = len(in)
		}
		out = append(out, in[start:end])
	}
	return out
}

func batchInt64s(in []int64, size int) [][]int64 {
	if size <= 0 || len(in) == 0 {
		return nil
	}
	out := make([][]int64, 0, (len(in)+size-1)/size)
	for start := 0; start < len(in); start += size {
		end := start + size
		if end > len(in) {
			end = len(in)
		}
		out = append(out, in[start:end])
	}
	return out
}
